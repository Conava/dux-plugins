package main

import (
	"context"
	"os"
	"testing"
)

func TestNeedsConvert(t *testing.T) {
	for _, c := range []struct {
		v      videoInfo
		want   bool
		tw, th int
	}{
		{videoInfo{width: 7680, height: 4320, codec: "video/x-h265"}, true, 3840, 2160},
		{videoInfo{width: 4320, height: 7680, codec: "video/x-h265"}, true, 2160, 3840},
		{videoInfo{width: 3840, height: 2160, codec: "video/x-h265"}, false, 3840, 2160},
		{videoInfo{width: 1920, height: 1080, codec: "video/x-av1"}, true, 1920, 1080},
		{videoInfo{width: 5760, height: 2880, codec: "video/x-h264"}, true, 3840, 1920},
	} {
		w, h := c.v.target()
		if c.v.needsConvert() != c.want || w != c.tw || h != c.th {
			t.Errorf("%+v: convert %v to %dx%d", c.v, c.v.needsConvert(), w, h)
		}
	}
}

// Set CONVERT_FILE to a real video to check probing and GPU conversion.
func TestConvertFile(t *testing.T) {
	file := os.Getenv("CONVERT_FILE")
	if file == "" {
		t.Skip("set CONVERT_FILE")
	}
	v, err := probeVideo(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("probe: %+v needsConvert=%v", v, v.needsConvert())
	last := 0.0
	out, err := convertVideo(context.Background(), file, v, func(p float64) { last = p })
	defer os.Remove(out)
	if err != nil {
		t.Fatal(err)
	}
	got, err := probeVideo(out)
	t.Logf("output: %+v %v, last progress %.2f", got, err, last)
	if got.width*got.height > maxPixels || got.codec != "video/x-h265" || !got.audio {
		t.Errorf("bad output %+v", got)
	}
}
