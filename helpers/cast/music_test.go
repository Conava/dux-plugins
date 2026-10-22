package main

import (
	"os"
	"strings"
	"testing"
)

func TestPlaylists(t *testing.T) {
	m3u := "\ufeff#EXTM3U\n#EXTINF:123,Artist - Song\nsongs/a.mp3\n\n/music/b.flac\nfile:///music/c%20d.ogg\nAlbum\\e.mp3\nhttps://radio.example/stream.mp3\n"
	got := parseM3U(strings.NewReader(m3u), "/lists")
	want := []playlistEntry{
		{Path: "/lists/songs/a.mp3", Title: "Artist - Song"},
		{Path: "/music/b.flac"},
		{Path: "/music/c d.ogg"},
		{Path: "/lists/Album/e.mp3"},
		{URL: "https://radio.example/stream.mp3"},
	}
	if len(got) != len(want) {
		t.Fatalf("m3u: %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("m3u %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	pls := "[playlist]\nFile2=two.mp3\nTitle2=Two\nFile1=/abs/one.mp3\nNumberOfEntries=2\n"
	gotPLS := parsePLS(strings.NewReader(pls), "/lists")
	if len(gotPLS) != 2 || gotPLS[0].Path != "/abs/one.mp3" || gotPLS[1] != (playlistEntry{Path: "/lists/two.mp3", Title: "Two"}) {
		t.Errorf("pls: %+v", gotPLS)
	}
}

// Set SONG_FILE to a tagged song to check tags and cover reading.
func TestProbeSong(t *testing.T) {
	file := os.Getenv("SONG_FILE")
	if file == "" {
		t.Skip("set SONG_FILE")
	}
	s := probeSong(file)
	t.Logf("title %q artist %q album %q cover %d bytes %s", s.title, s.artist, s.album, len(s.cover), s.coverType)
	if s.title == "" || len(s.cover) == 0 {
		t.Error("missing title or cover")
	}
}
