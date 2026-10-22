package main

import "testing"

func TestParseYouTube(t *testing.T) {
	for link, want := range map[string]youtubeVideo{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":                   {id: "dQw4w9WgXcQ"},
		"https://youtu.be/dQw4w9WgXcQ?t=42":                             {id: "dQw4w9WgXcQ", start: 42},
		"https://m.youtube.com/watch?v=dQw4w9WgXcQ&list=PL123&t=1m30s":  {id: "dQw4w9WgXcQ", list: "PL123", start: 90},
		"https://www.youtube.com/shorts/dQw4w9WgXcQ":                    {id: "dQw4w9WgXcQ"},
		"https://youtube.com/live/dQw4w9WgXcQ?feature=share":            {id: "dQw4w9WgXcQ"},
		"https://music.youtube.com/watch?v=dQw4w9WgXcQ&list=RDAMVM1234": {id: "dQw4w9WgXcQ", list: "RDAMVM1234"},
	} {
		got, ok := parseYouTube(link)
		if !ok || got != want {
			t.Errorf("%s: got %+v %v, want %+v", link, got, ok, want)
		}
	}
	for _, link := range []string{"https://example.com/watch?v=dQw4w9WgXcQ", "https://www.youtube.com/watch?v=short", "https://www.youtube.com/@channel"} {
		if _, ok := parseYouTube(link); ok {
			t.Errorf("%s: should not parse", link)
		}
	}
}

func TestBindResponse(t *testing.T) {
	// The shape casttube parses: a length-prefixed JSON array of events.
	body := []byte(`262
[[0,["c","ABCDEF123","",8]
]
,[1,["S","gsess-42"]]]`)
	sid, gs := sidRe.FindSubmatch(body), gsessionRe.FindSubmatch(body)
	if sid == nil || string(sid[1]) != "ABCDEF123" || gs == nil || string(gs[1]) != "gsess-42" {
		t.Errorf("sid %q gsessionid %q", sid, gs)
	}
}
