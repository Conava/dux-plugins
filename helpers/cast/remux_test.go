package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// el encodes one EBML element; ids are given with their marker bits.
func el(id uint64, body ...[]byte) []byte {
	var b []byte
	for s := 56; s >= 0; s -= 8 {
		if c := byte(id >> s); c != 0 || len(b) > 0 {
			b = append(b, c)
		}
	}
	data := bytes.Join(body, nil)
	n := len(data)
	b = append(b, 0x10|byte(n>>24), byte(n>>16), byte(n>>8), byte(n)) // 4-byte vint
	return append(b, data...)
}

func TestParseMKVAndPipeline(t *testing.T) {
	file := bytes.Join([][]byte{
		el(ebmlHeader, el(ebmlDocType, []byte("matroska"))),
		el(mkvSegment,
			el(0x1549A966, []byte{1, 2, 3}), // Info, skipped
			el(mkvTracks,
				el(mkvEntry, el(mkvType, []byte{1}), el(mkvCodec, []byte("V_MPEGH/ISO/HEVC"))),
				el(mkvEntry, el(mkvType, []byte{2}), el(mkvCodec, []byte("A_DTS")), el(mkvDefault, []byte{0})),
				el(mkvEntry, el(mkvType, []byte{2}), el(mkvCodec, []byte("A_EAC3")), el(mkvLanguage, []byte("ger"))),
				el(mkvEntry, el(mkvType, []byte{17}), el(mkvCodec, []byte("S_TEXT/UTF8"))),
			)),
	}, nil)
	info, err := parseMKV(bufio.NewReader(bytes.NewReader(file)))
	if err != nil {
		t.Fatal(err)
	}
	if info.docType != "matroska" || len(info.tracks) != 4 || info.tracks[2].language != "ger" || info.tracks[1].isDef {
		t.Fatalf("parsed %+v", info)
	}

	cases := []struct {
		mode  string
		track int
		want  string
	}{
		{"auto", -1, "d.audio_1 ! queue ! ac3parse"}, // the default track, E-AC-3 copied
		{"auto", 0, "d.audio_0 ! queue ! decodebin"}, // DTS re-encoded
		{"aac", -1, "d.audio_1 ! queue ! decodebin"}, // forced
	}
	for _, c := range cases {
		p, err := remuxPipeline(info, c.mode, c.track)
		if err != nil || !strings.Contains(p, c.want) || !strings.Contains(p, "h265parse ! h265timestamper") {
			t.Errorf("%s/%d: %q %v", c.mode, c.track, p, err)
		}
	}
	info.tracks[0].codec = "V_MS/VFW/FOURCC"
	if _, err := remuxPipeline(info, "auto", -1); err == nil {
		t.Error("VC-1 should be refused")
	}
}

func TestSRTToVTT(t *testing.T) {
	in := "\xEF\xBB\xBF1\r\n00:00:01,000 --> 00:01:02,500\r\n<font color=\"red\">Hi</font> <i>there</i>, 1,5\r\n"
	want := "WEBVTT\n\n1\n00:00:01.000 --> 00:01:02.500\nHi <i>there</i>, 1,5\n"
	if got := string(srtToVTT([]byte(in))); got != want {
		t.Errorf("got %q", got)
	}
	if got := string(srtToVTT([]byte("1\n00:00:01,000 --> 00:00:02,000\nM\xfcller\n"))); !strings.Contains(got, "Müller") {
		t.Errorf("latin-1: %q", got)
	}
}

func TestParseMKVRejectsBadSizes(t *testing.T) {
	for _, file := range [][]byte{
		{0x1A, 0x45, 0xDF, 0xA3, 0xFF},                         // unknown size
		{0x1A, 0x45, 0xDF, 0xA3, 0x08, 0xFF, 0xFF, 0xFF, 0xFF}, // 4 GiB header
	} {
		if _, err := parseMKV(bufio.NewReader(bytes.NewReader(file))); err == nil {
			t.Errorf("% x parsed", file)
		}
	}
}
