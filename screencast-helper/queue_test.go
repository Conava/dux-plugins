package main

import "testing"

func TestLookAhead(t *testing.T) {
	m := newMediaSession(nil, device{}, []string{"/a.jpg", "/b.jpg", "/c.mp4", "/d.jpg", "/e.mp4"}, 0, queueOptions{})
	order := func() (got []int) {
		for i := m.nextToPrepare(); i >= 0; i = m.nextToPrepare() {
			got = append(got, i)
			m.items[i].status = "ready"
		}
		return got
	}
	// The current photo, the next photo, and a video two ahead is skipped.
	if got := order(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("from 0: %v", got)
	}
	m.index = 3
	m.items[0].files = []string{"/nonexistent"}
	if got := order(); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Errorf("from 3: %v", got)
	}
	// Items more than two behind give their prepared copies back.
	m.prune()
	if m.items[0].status != "waiting" || m.items[1].status != "ready" && m.items[1].files != nil {
		t.Errorf("prune: %s %s", m.items[0].status, m.items[1].status)
	}
	// Looping wraps the look-ahead around the end.
	m.opts.Loop, m.index = true, 4
	m.items[0].status = "waiting"
	if got := order(); len(got) != 1 || got[0] != 0 {
		t.Errorf("loop from 4: %v", got)
	}
}
