package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The TV decodes up to 4K in H.264, HEVC, VP9 and VP8. Anything beyond that
// (an 8K phone video, AV1, MPEG-2) is converted on the GPU to 4K HEVC first;
// everything else is sent untouched.
const maxPixels = 3840 * 2160

var tvCodecs = []string{"video/x-h264", "video/x-h265", "video/x-vp9", "video/x-vp8"}

type videoInfo struct {
	width, height int
	codec         string // caps media type, e.g. video/x-h265
	audio         bool
}

func (v videoInfo) needsConvert() bool {
	known := false
	for _, c := range tvCodecs {
		known = known || v.codec == c
	}
	return v.width*v.height > maxPixels || !known
}

// target fits the video into 4K, keeping its aspect ratio and even sizes.
func (v videoInfo) target() (w, h int) {
	w, h = v.width, v.height
	if w*h > maxPixels {
		scale := min(3840/float64(max(w, h)), 2160/float64(min(w, h)))
		w, h = int(float64(w)*scale), int(float64(h)*scale)
	}
	return w &^ 1, h &^ 1
}

var (
	discovererNew           func(timeout uint64, gerr unsafe.Pointer) uintptr
	discovererDiscoverURI   func(d uintptr, uri string, gerr unsafe.Pointer) uintptr
	discovererVideoStreams  func(info uintptr) *gList
	discovererAudioStreams  func(info uintptr) *gList
	discovererVideoWidth    func(vinfo uintptr) uint32
	discovererVideoHeight   func(vinfo uintptr) uint32
	discovererStreamCaps    func(sinfo uintptr) uintptr
	discovererListFree      func(list *gList)
	gstCapsGetStructure     func(caps uintptr, index uint32) uintptr
	gstStructureGetName     func(s uintptr) *byte
	gstElementQueryPosition func(el uintptr, format int32, cur *int64) bool
	gstElementQueryDuration func(el uintptr, format int32, dur *int64) bool
	gObjectUnref            func(obj uintptr)
)

func loadDiscoverer() error {
	if err := loadGst(); err != nil {
		return err
	}
	if discovererNew != nil {
		return nil
	}
	for name, fns := range map[string]map[string]any{
		"libgstpbutils-1.0.so.0": {
			"gst_discoverer_new": &discovererNew, "gst_discoverer_discover_uri": &discovererDiscoverURI,
			"gst_discoverer_info_get_video_streams": &discovererVideoStreams,
			"gst_discoverer_info_get_audio_streams": &discovererAudioStreams,
			"gst_discoverer_video_info_get_width":   &discovererVideoWidth,
			"gst_discoverer_video_info_get_height":  &discovererVideoHeight,
			"gst_discoverer_stream_info_get_caps":   &discovererStreamCaps,
			"gst_discoverer_stream_info_list_free":  &discovererListFree,
		},
		"libgstreamer-1.0.so.0": {
			"gst_caps_get_structure": &gstCapsGetStructure, "gst_structure_get_name": &gstStructureGetName,
			"gst_element_query_position": &gstElementQueryPosition,
			"gst_element_query_duration": &gstElementQueryDuration,
		},
		"libgobject-2.0.so.0": {"g_object_unref": &gObjectUnref},
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

// probeVideo reads the first video stream's size and codec.
func probeVideo(file string) (videoInfo, error) {
	if err := loadDiscoverer(); err != nil {
		return videoInfo{}, err
	}
	var gerr *gError
	d := discovererNew(uint64(10*time.Second), unsafe.Pointer(&gerr))
	if d == 0 {
		return videoInfo{}, fmt.Errorf("discoverer: %s", gerrorMessage(gerr))
	}
	defer gObjectUnref(d)
	info := discovererDiscoverURI(d, (&url.URL{Scheme: "file", Path: file}).String(), unsafe.Pointer(&gerr))
	if info == 0 {
		return videoInfo{}, fmt.Errorf("probe: %s", gerrorMessage(gerr))
	}
	defer gObjectUnref(info)

	var v videoInfo
	videos := discovererVideoStreams(info)
	if videos == nil {
		return v, fmt.Errorf("no video stream")
	}
	defer discovererListFree(videos)
	first := videos.data
	v.width, v.height = int(discovererVideoWidth(first)), int(discovererVideoHeight(first))
	if caps := discovererStreamCaps(first); caps != 0 {
		v.codec = cString(gstStructureGetName(gstCapsGetStructure(caps, 0)))
		gstMiniObjectUnref(caps)
	}
	if audios := discovererAudioStreams(info); audios != nil {
		v.audio = true
		discovererListFree(audios)
	}
	return v, nil
}

// convertVideo re-encodes file to 4K HEVC in an MP4 on the GPU, reporting
// progress from 0 to 1.
func convertVideo(ctx context.Context, file string, v videoInfo, progress func(float64)) (string, error) {
	w, h := v.target()
	desc := fmt.Sprintf("filesrc name=src ! decodebin name=d "+
		"d. ! queue ! vapostproc ! video/x-raw(memory:VAMemory),width=%d,height=%d ! "+
		"vah265enc rate-control=cbr bitrate=30000 key-int-max=60 b-frames=0 ! h265parse ! video/x-h265,stream-format=hvc1 ! mux.video_0 ", w, h)
	if v.audio {
		desc += "d. ! queue ! audioconvert ! audioresample ! avenc_aac bitrate=256000 ! aacparse ! mux.audio_0 "
	}
	desc += "mp4mux name=mux faststart=true ! filesink name=sink"
	out, err := cacheFile("conv.mp4")
	if err != nil {
		return "", err
	}
	start := time.Now()
	log.Printf("converting %dx%d %s to %dx%d HEVC: %s", v.width, v.height, v.codec, w, h, desc)
	err = runGst(ctx, desc, map[string]map[string]string{
		"src":  {"location": file},
		"sink": {"location": out},
		"mux":  {"faststart-file": out + ".part"},
	}, progress)
	if err != nil {
		return out, err
	}
	log.Printf("converted in %s", time.Since(start).Round(time.Millisecond))
	return out, nil
}

// cacheFile names a temporary file in the helper's cache directory; stale
// ones are removed at startup.
func cacheFile(suffix string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "dms", "screencast")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%d-%s", os.Getpid(), suffix)), nil
}

type gList struct{ data, next, prev uintptr }

func cString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return strings.Clone(string(unsafe.Slice(p, n)))
}
