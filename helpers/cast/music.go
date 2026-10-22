package main

import (
	"bufio"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Songs go to the device with their title, artist, album and cover, so it
// shows a music screen instead of a file name. Playlists (.m3u, .m3u8, .pls)
// expand into their entries for the player.

type songInfo struct {
	title, artist, album string
	cover                []byte // embedded picture, any image type
	coverType            string
}

var (
	discovererInfoTags func(info uintptr) uintptr
	tagListGetString   func(list uintptr, tag string, value **byte) bool
	tagListGetSample   func(list uintptr, tag string, sample *uintptr) bool
	gstSampleGetCaps   func(sample uintptr) uintptr
	gFree              func(p unsafe.Pointer)
)

func loadTags() error {
	if err := loadDiscoverer(); err != nil {
		return err
	}
	if discovererInfoTags != nil {
		return nil
	}
	for name, fns := range map[string]map[string]any{
		"libgstpbutils-1.0.so.0": {"gst_discoverer_info_get_tags": &discovererInfoTags},
		"libgstreamer-1.0.so.0": {
			"gst_tag_list_get_string": &tagListGetString, "gst_tag_list_get_sample": &tagListGetSample,
			"gst_sample_get_caps": &gstSampleGetCaps,
		},
		"libglib-2.0.so.0": {"g_free": &gFree},
	} {
		lib, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return err
		}
		for sym, fn := range fns {
			purego.RegisterLibFunc(fn, lib, sym)
		}
	}
	return nil
}

// probeSong reads a song's tags and cover; a missing tag is just empty.
func probeSong(file string) songInfo {
	var s songInfo
	if loadTags() != nil {
		return s
	}
	var gerr *gError
	d := discovererNew(uint64(10*time.Second), unsafe.Pointer(&gerr))
	if d == 0 {
		return s
	}
	defer gObjectUnref(d)
	info := discovererDiscoverURI(d, (&url.URL{Scheme: "file", Path: file}).String(), unsafe.Pointer(&gerr))
	if info == 0 {
		return s
	}
	defer gObjectUnref(info)
	tags := discovererInfoTags(info)
	if tags == 0 {
		return s
	}
	str := func(tag string) string {
		var p *byte
		if !tagListGetString(tags, tag, &p) || p == nil {
			return ""
		}
		defer gFree(unsafe.Pointer(p))
		return cString(p)
	}
	s.title, s.artist, s.album = str("title"), str("artist"), str("album")
	for _, tag := range []string{"image", "preview-image"} {
		var sample uintptr
		if !tagListGetSample(tags, tag, &sample) || sample == 0 {
			continue
		}
		buf := gstSampleGetBuffer(sample)
		if n := gstBufferGetSize(buf); n > 0 && n < 16<<20 {
			s.cover = make([]byte, n)
			gstBufferExtract(buf, 0, unsafe.Pointer(&s.cover[0]), n)
			s.coverType = "image/jpeg"
			if caps := gstSampleGetCaps(sample); caps != 0 {
				if t := cString(gstStructureGetName(gstCapsGetStructure(caps, 0))); strings.HasPrefix(t, "image/") {
					s.coverType = t
				}
			}
		}
		gstMiniObjectUnref(sample)
		break
	}
	return s
}

// folderCover finds the cover image an album folder usually has.
func folderCover(file string) string {
	dir := filepath.Dir(file)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, want := range []string{"cover", "folder", "front", "album"} {
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			base := strings.TrimSuffix(name, filepath.Ext(name))
			if base == want && strings.HasPrefix(typeByExt(name), "image/") {
				return filepath.Join(dir, e.Name())
			}
		}
	}
	return ""
}

func isPlaylist(file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".m3u", ".m3u8", ".pls":
		return true
	}
	return false
}

type playlistEntry struct {
	Path  string `json:"path,omitempty"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}

// readPlaylist lists a playlist's entries in order. Relative paths are
// relative to the playlist's folder.
func readPlaylist(file string) ([]playlistEntry, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if strings.EqualFold(filepath.Ext(file), ".pls") {
		return parsePLS(f, filepath.Dir(file)), nil
	}
	return parseM3U(f, filepath.Dir(file)), nil
}

func parseM3U(r io.Reader, dir string) []playlistEntry {
	var out []playlistEntry
	title := ""
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXTINF:"):
			if _, t, ok := strings.Cut(line, ","); ok {
				title = strings.TrimSpace(t)
			}
		case strings.HasPrefix(line, "#"):
		default:
			out = append(out, playlistEntryFor(line, dir, title))
			title = ""
		}
	}
	return out
}

func parsePLS(r io.Reader, dir string) []playlistEntry {
	files, titles := map[int]string{}, map[int]string{}
	maxN := 0
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		lk := strings.ToLower(k)
		for prefix, into := range map[string]map[int]string{"file": files, "title": titles} {
			if n, err := strconv.Atoi(strings.TrimPrefix(lk, prefix)); err == nil && strings.HasPrefix(lk, prefix) {
				into[n] = strings.TrimSpace(v)
				maxN = max(maxN, n)
			}
		}
	}
	var out []playlistEntry
	for n := 1; n <= maxN; n++ {
		if f, ok := files[n]; ok {
			out = append(out, playlistEntryFor(f, dir, titles[n]))
		}
	}
	return out
}

func playlistEntryFor(ref, dir, title string) playlistEntry {
	if isLink(ref) {
		return playlistEntry{URL: ref, Title: title}
	}
	if u, err := url.Parse(ref); err == nil && u.Scheme == "file" {
		ref = u.Path
	}
	ref = filepath.FromSlash(strings.ReplaceAll(ref, `\`, "/")) // Windows-made playlists
	if !filepath.IsAbs(ref) {
		ref = filepath.Join(dir, ref)
	}
	return playlistEntry{Path: filepath.Clean(ref), Title: title}
}

// expandPlaylist answers {"cmd":"playlist"} with the entries, or an empty
// list when the file can't be read.
func (h *helper) expandPlaylist(file string) {
	entries, err := readPlaylist(file)
	if err != nil {
		h.emitError("unsupportedFile", err)
	}
	if entries == nil {
		entries = []playlistEntry{}
	}
	h.emit(map[string]any{"event": "playlist", "path": file, "items": entries})
}
