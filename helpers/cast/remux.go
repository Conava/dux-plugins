package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"
)

// EBML element IDs, with their length marker bits kept.
const (
	ebmlHeader   = 0x1A45DFA3
	ebmlDocType  = 0x4282
	mkvSegment   = 0x18538067
	mkvTracks    = 0x1654AE6B
	mkvEntry     = 0xAE
	mkvType      = 0x83
	mkvCodec     = 0x86
	mkvDefault   = 0x88
	mkvLanguage  = 0x22B59C
	mkvName      = 0x536E
	mkvTypeVideo = 1
	mkvTypeAudio = 2
	mkvTypeText  = 17
)

type mkvTrack struct {
	kind     int // 1 video, 2 audio, 17 subtitle
	codec    string
	language string
	name     string
	isDef    bool
}

type mkvInfo struct {
	docType string
	tracks  []mkvTrack
}

func isMatroska(file string) bool {
	info, err := probeMKV(file)
	return err == nil && info.docType == "matroska"
}

// probeMKV reads the EBML header and the Tracks element, which muxers put
// before the clusters.
func probeMKV(file string) (mkvInfo, error) {
	f, err := os.Open(file)
	if err != nil {
		return mkvInfo{}, err
	}
	defer f.Close()
	return parseMKV(bufio.NewReader(io.LimitReader(f, 64<<20)))
}

func parseMKV(r *bufio.Reader) (mkvInfo, error) {
	var info mkvInfo
	id, size, err := ebmlElement(r)
	if err != nil || id != ebmlHeader {
		return info, errors.New("not an EBML file")
	}
	head := make([]byte, size)
	if _, err := io.ReadFull(r, head); err != nil {
		return info, err
	}
	hr := bufio.NewReader(bytes.NewReader(head))
	for {
		id, size, err := ebmlElement(hr)
		if err != nil {
			break
		}
		v := make([]byte, size)
		io.ReadFull(hr, v)
		if id == ebmlDocType {
			info.docType = string(v)
		}
	}
	if id, _, err = ebmlElement(r); err != nil || id != mkvSegment {
		return info, errors.New("no segment")
	}
	// Walk the segment's top-level children, skipping everything but Tracks.
	for {
		id, size, err := ebmlElement(r)
		if err != nil {
			return info, errors.New("no tracks element")
		}
		if id != mkvTracks {
			if _, err := r.Discard(int(size)); err != nil {
				return info, err
			}
			continue
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(r, body); err != nil {
			return info, err
		}
		info.tracks = parseTracks(body)
		return info, nil
	}
}

func parseTracks(body []byte) []mkvTrack {
	var tracks []mkvTrack
	r := bufio.NewReader(bytes.NewReader(body))
	for {
		id, size, err := ebmlElement(r)
		if err != nil {
			return tracks
		}
		entry := make([]byte, size)
		io.ReadFull(r, entry)
		if id != mkvEntry {
			continue
		}
		t := mkvTrack{language: "eng", isDef: true} // spec defaults
		er := bufio.NewReader(bytes.NewReader(entry))
		for {
			id, size, err := ebmlElement(er)
			if err != nil {
				break
			}
			v := make([]byte, size)
			io.ReadFull(er, v)
			switch id {
			case mkvType:
				t.kind = int(uintBE(v))
			case mkvCodec:
				t.codec = string(v)
			case mkvDefault:
				t.isDef = uintBE(v) == 1
			case mkvLanguage:
				t.language = strings.TrimRight(string(v), "\x00")
			case mkvName:
				t.name = string(v)
			}
		}
		tracks = append(tracks, t)
	}
}

// ebmlElement reads an element ID and its data size. An unknown size (all
// ones) comes back as -1, which only live streams use for the segment.
func ebmlElement(r *bufio.Reader) (id uint64, size int64, err error) {
	id, n, err := ebmlVint(r)
	if err != nil {
		return 0, 0, err
	}
	id |= 1 << (7 * n) // IDs keep their marker bit
	sz, n, err := ebmlVint(r)
	if err != nil {
		return 0, 0, err
	}
	if sz == 1<<(7*n)-1 {
		return id, -1, nil
	}
	if sz > 1<<32 {
		return 0, 0, errors.New("element too large")
	}
	return id, int64(sz), nil
}

// ebmlVint reads a variable-length integer and returns it without its marker
// bit, along with its length in bytes.
func ebmlVint(r *bufio.Reader) (uint64, int, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	n := 1
	for mask := byte(0x80); b&mask == 0; mask >>= 1 {
		n++
		if n > 8 {
			return 0, 0, errors.New("bad vint")
		}
	}
	v := uint64(b) & (0xFF >> n)
	for i := 1; i < n; i++ {
		c, err := r.ReadByte()
		if err != nil {
			return 0, 0, err
		}
		v = v<<8 | uint64(c)
	}
	return v, n, nil
}

func uintBE(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

// remuxPipeline builds a GStreamer pipeline that copies the first video
// track and one audio track of an MKV into an MP4. Matroska carries no
// decode timestamps, so H.264/HEVC with B-frames need the timestamper or
// mp4mux writes a broken timeline. Audio the TV can't take in MP4 is
// re-encoded to AAC; video never is.
func remuxPipeline(info mkvInfo, audioMode string, audioTrack int) (string, error) {
	var video string
	var audios []mkvTrack
	for _, t := range info.tracks {
		switch t.kind {
		case mkvTypeAudio:
			audios = append(audios, t)
		case mkvTypeVideo:
			if video != "" {
				continue
			}
			switch t.codec {
			case "V_MPEG4/ISO/AVC":
				video = "h264parse ! h264timestamper"
			case "V_MPEGH/ISO/HEVC":
				video = "h265parse ! h265timestamper"
			case "V_VP9":
				video = "vp9parse"
			default:
				return "", fmt.Errorf("video codec %s would need re-encoding", t.codec)
			}
		}
	}
	if video == "" {
		return "", errors.New("no video track")
	}
	pick := audioTrack
	if pick >= len(audios) {
		return "", fmt.Errorf("there are only %d audio tracks", len(audios))
	}
	if pick < 0 && len(audios) > 0 {
		pick = 0
		for i, t := range audios {
			if t.isDef {
				pick = i
				break
			}
		}
	}
	p := "filesrc name=src ! matroskademux name=d d.video_0 ! queue ! " + video + " ! mux.video_0 "
	if pick >= 0 {
		pickCodec := audios[pick].codec
		audio := "decodebin ! audioconvert ! audioresample ! avenc_aac bitrate=256000"
		if audioMode != "aac" {
			switch {
			case strings.HasPrefix(pickCodec, "A_AAC"):
				audio = "aacparse"
			case pickCodec == "A_MPEG/L3":
				audio = "mpegaudioparse"
			case pickCodec == "A_OPUS":
				audio = "opusparse"
			case pickCodec == "A_AC3", pickCodec == "A_EAC3":
				audio = "ac3parse"
			}
		}
		p += fmt.Sprintf("d.audio_%d ! queue ! %s ! mux.audio_0 ", pick, audio)
	}
	return p + "mp4mux name=mux faststart=true ! filesink name=sink", nil
}

// remuxMKV writes an MP4 copy of file into the cache directory, so it can be
// served with Range requests and the TV can seek.
func remuxMKV(ctx context.Context, file, audioMode string, audioTrack int) (string, error) {
	info, err := probeMKV(file)
	if err != nil {
		return "", err
	}
	for _, t := range info.tracks {
		log.Printf("mkv track: type=%d codec=%s lang=%s default=%v %s", t.kind, t.codec, t.language, t.isDef, t.name)
	}
	desc, err := remuxPipeline(info, audioMode, audioTrack)
	if err != nil {
		return "", err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "dms", "cast")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	out := filepath.Join(dir, fmt.Sprintf("%d.mp4", os.Getpid()))
	start := time.Now()
	log.Printf("remuxing to %s: %s", out, desc)
	if err := runGst(ctx, desc, map[string]map[string]string{
		"src":  {"location": file},
		"sink": {"location": out},
		// Default is /tmp, often a RAM disk too small for a movie.
		"mux": {"faststart-file": out + ".part"},
	}, nil); err != nil {
		os.Remove(out)
		os.Remove(out + ".part")
		return "", err
	}
	st, _ := os.Stat(out)
	log.Printf("remuxed %d MB in %s", st.Size()>>20, time.Since(start).Round(time.Millisecond))
	return out, nil
}

// runGst runs a pipeline to EOS, or until ctx ends. Paths are set as
// properties after parsing, so they never pass through the pipeline syntax.
func runGst(ctx context.Context, desc string, props map[string]map[string]string, progress func(float64)) error {
	if err := loadGst(); err != nil {
		return fmt.Errorf("loading GStreamer: %w", err)
	}
	var gerr *gError
	pipe := gstParseLaunch(desc, unsafe.Pointer(&gerr))
	if pipe == 0 || gerr != nil {
		return fmt.Errorf("pipeline: %s", gerrorMessage(gerr))
	}
	defer gstObjectUnref(pipe)
	for name, kv := range props {
		el := gstBinGetByName(pipe, name)
		if el == 0 {
			return fmt.Errorf("no element %s", name)
		}
		for k, v := range kv {
			gstUtilSetObjectArg(el, k, v)
		}
		gstObjectUnref(el)
	}
	defer gstElementSetState(pipe, gstStateNull)
	if gstElementSetState(pipe, gstStatePlaying) == 0 {
		return errors.New("pipeline refused to start")
	}
	bus := gstElementGetBus(pipe)
	defer gstObjectUnref(bus)
	for ctx.Err() == nil {
		msg := gstBusTimedPopFiltered(bus, uint64(500*time.Millisecond), gstMsgEOS|gstMsgError)
		if msg == 0 {
			var pos, dur int64
			if progress != nil && gstElementQueryPosition(pipe, 3, &pos) && gstElementQueryDuration(pipe, 3, &dur) && dur > 0 {
				progress(float64(pos) / float64(dur))
			}
			continue
		}
		defer gstMiniObjectUnref(msg)
		if gstMessageGetStructure(msg) == 0 {
			return nil // EOS
		}
		var debug uintptr
		gstMessageParseError(msg, unsafe.Pointer(&gerr), unsafe.Pointer(&debug))
		return errors.New(gerrorMessage(gerr))
	}
	return ctx.Err()
}

var srtTime = regexp.MustCompile(`(\d+:\d{2}:\d{2}),(\d{3})`)
var srtFont = regexp.MustCompile(`(?i)</?font[^>]*>`)

// loadSubtitle returns a subtitle file as WebVTT.
func loadSubtitle(file string) ([]byte, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(file), ".vtt") {
		return b, nil
	}
	return srtToVTT(b), nil
}

// srtToVTT converts SubRip to WebVTT: a header, dots in timestamps, and no
// <font> tags, which VTT doesn't have. <i>, <b> and <u> are valid in both.
func srtToVTT(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF"))
	if !utf8.Valid(b) {
		// ponytail: assumes Latin-1 for non-UTF-8 files; Windows-1252 differs in 0x80-0x9F.
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		b = []byte(string(r))
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if strings.Contains(line, "-->") {
			line = srtTime.ReplaceAllString(line, "$1.$2")
		} else {
			line = srtFont.ReplaceAllString(line, "")
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return []byte(out.String())
}
