package main

import "testing"

func TestHolder(t *testing.T) {
	line := func(k, name string) (string, string) {
		key, v, ok := parseUpdate(`update: id:0 key:'` + k + `' value:'{"name":"` + name + `"}' type:'Spa:String:JSON'`)
		if !ok || key != k || v != name {
			t.Fatalf("parse %s: %q %q %v", k, key, v, ok)
		}
		return key, v
	}
	if _, _, ok := parseUpdate(`Found "default" metadata 40`); ok {
		t.Fatal("parsed a non-update line")
	}
	var h holder
	steps := []struct {
		key, name string
		claim     bool
	}{
		{"default.configured.audio.sink", "speakers", false},
		{"default.audio.sink", "speakers", true}, // initial claim
		{"default.audio.sink", sinkName, false},  // ours now
		{"default.audio.sink", "speakers", true}, // hotplug rescan: reclaim
		{"default.configured.audio.sink", "headphones", false},
		{"default.audio.sink", "headphones", false}, // user picked an output: let go
	}
	for i, s := range steps {
		if got := h.update(line(s.key, s.name)); got != s.claim {
			t.Fatalf("step %d: claim %v, want %v", i, got, s.claim)
		}
	}
}
