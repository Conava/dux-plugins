package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEncoderOutputSplits(t *testing.T) {
	out, err := startGstPipeline("videotestsrc num-buffers=90 ! video/x-raw,width=1280,height=720,framerate=30/1 ! " +
		"vapostproc ! video/x-raw(memory:VAMemory),format=NV12 ! vah264enc name=enc aud=true b-frames=0 key-int-max=30 ! " +
		"h264parse config-interval=-1 ! video/x-h264,stream-format=byte-stream,alignment=au ! appsink name=out sync=false")
	if err != nil {
		t.Skip(err)
	}
	var units, keys []int
	readAccessUnits(out, nalCodec{}, func(au []byte, key bool) {
		if key {
			keys = append(keys, len(units))
		}
		units = append(units, len(au))
	})
	// The last unit stays buffered until a following delimiter arrives.
	if len(units) != 89 {
		t.Errorf("got %d access units, want 89", len(units))
	}
	if len(keys) != 3 || keys[0] != 0 || keys[1] != 30 || keys[2] != 60 {
		t.Errorf("key frames at %v, want [0 30 60]", keys)
	}

	out, err = startGstPipeline("videotestsrc num-buffers=90 ! video/x-raw,width=1280,height=720,framerate=30/1 ! " +
		"vapostproc ! video/x-raw(memory:VAMemory),format=NV12 ! vah265enc name=enc aud=true b-frames=0 key-int-max=30 ! " +
		"h265parse config-interval=-1 ! video/x-h265,stream-format=byte-stream,alignment=au ! appsink name=out sync=false")
	if err != nil {
		t.Skip(err)
	}
	units, keys = nil, nil
	readAccessUnits(out, nalCodec{hevc: true}, func(au []byte, key bool) {
		if key {
			keys = append(keys, len(units))
		}
		units = append(units, len(au))
	})
	if len(units) != 89 || len(keys) != 3 || keys[1] != 30 {
		t.Errorf("hevc: %d units, key frames at %v", len(units), keys)
	}

	out, err = startGstPipeline("audiotestsrc num-buffers=50 samplesperbuffer=960 ! audio/x-raw,rate=48000,channels=2 ! " +
		"opusenc name=enc frame-size=10 ! appsink name=out sync=false")
	if err != nil {
		t.Skip(err)
	}
	packets := 0
	for {
		if _, err := out.Pull(); err != nil {
			break
		}
		packets++
	}
	// 50 buffers of 20 ms is one second, so about 100 packets of 10 ms.
	if packets < 98 || packets > 101 {
		t.Errorf("got %d opus packets, want about 100", packets)
	}
}

func TestSPSColourRewrite(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip(err)
	}
	out, err := startGstPipeline("videotestsrc num-buffers=10 ! video/x-raw,width=1920,height=1080,framerate=60/1 ! " +
		"vapostproc ! video/x-raw(memory:VAMemory),format=NV12 ! vah264enc name=enc aud=true b-frames=0 key-int-max=5 ! " +
		"video/x-h264,profile=main ! h264parse config-interval=-1 ! video/x-h264,stream-format=byte-stream,alignment=au ! appsink name=out sync=false")
	if err != nil {
		t.Skip(err)
	}
	var stream []byte
	readAccessUnits(out, nalCodec{}, func(au []byte, key bool) { stream = append(stream, fixColour(au)...) })
	path := filepath.Join(t.TempDir(), "out.h264")
	os.WriteFile(path, stream, 0o644)
	got, err := exec.Command(ffprobe, "-v", "error", "-show_entries",
		"stream=color_range,color_space,color_primaries,color_transfer,width,height", "-of", "compact", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "stream|width=1920|height=1080|color_range=tv|color_space=bt709|color_transfer=bt709|color_primaries=bt709\n"
	if string(got) != want {
		t.Errorf("ffprobe says %q", got)
	}
	frames, _ := exec.Command("ffprobe", "-v", "error", "-count_frames", "-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", path).Output()
	if string(frames) != "9\n" {
		t.Errorf("decoded %q frames, want 9", frames)
	}
}
