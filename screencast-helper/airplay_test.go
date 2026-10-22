package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMergeDevices(t *testing.T) {
	got := mergeDevices(map[string]Device{
		"chromecast:abc": {ID: "chromecast:abc", Name: "Living Room", Protocol: "chromecast", Addr: "192.168.2.63", Port: 8009},
		"airplay:aa:bb":  {ID: "airplay:aa:bb", Name: "[LG] webOS TV", Protocol: "airplay", Addr: "192.168.2.63", Port: 7000},
		"airplay:cc:dd":  {ID: "airplay:cc:dd", Name: "Apple TV", Protocol: "airplay", Addr: "192.168.2.70", Port: 7000},
	})
	byID := map[string]device{}
	for _, d := range got {
		byID[d.ID] = d
	}
	tv, apple := byID["chromecast:abc"], byID["airplay:cc:dd"]
	if len(got) != 2 || tv.Name != "Living Room" || !tv.has("airplay") || tv.AirplayPort != 7000 || tv.Port != 8009 {
		t.Errorf("both protocols: %+v", got)
	}
	if apple.Protocol != "airplay" || apple.has("chromecast") {
		t.Errorf("AirPlay only: %+v", apple)
	}
}

// Set AIRPLAY_TEST_PORT to a running doubletake-test-receiver
// (-auth pin -code 4321) to check the session end to end.
func TestAirplayReceiver(t *testing.T) {
	port := os.Getenv("AIRPLAY_TEST_PORT")
	if port == "" || doubletakePath() == "" {
		t.Skip("set AIRPLAY_TEST_PORT and put doubletake on PATH")
	}
	doubletakeTestArgs = []string{"-test"}
	var events []string
	out := writerFunc(func(p []byte) { events = append(events, strings.TrimSpace(string(p))) })
	h := &helper{out: json.NewEncoder(out), devices: map[string]device{}}
	var ap int
	json.Unmarshal([]byte(port), &ap)
	a := newAirplaySession(h, device{ID: "airplay:test", Name: "Test", Protocols: []string{"airplay"}, Host: "127.0.0.1", AirplayPort: ap},
		castOptions{Audio: "local"})
	h.sess = a
	go func() {
		for range 30 {
			time.Sleep(time.Second)
			st := a.status()
			if st["phase"] == "pairing" {
				a.pair("4321")
			}
			if st["phase"] == "casting" {
				time.Sleep(2 * time.Second)
				break
			}
		}
		a.Stop()
	}()
	code, err := a.run()
	phases := []string{}
	for _, e := range events {
		if i := strings.Index(e, `"phase":"`); i >= 0 {
			p := e[i+9:]
			p = p[:strings.Index(p, `"`)]
			if len(phases) == 0 || phases[len(phases)-1] != p {
				phases = append(phases, p)
			}
		}
	}
	t.Logf("phases %v, ended %q %v", phases, code, err)
	if !strings.Contains(strings.Join(phases, " "), "pairing starting casting") {
		t.Errorf("expected pairing then casting, got %v", phases)
	}
}

type writerFunc func([]byte)

func (f writerFunc) Write(p []byte) (int, error) { f(p); return len(p), nil }
