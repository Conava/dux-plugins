package main

import (
	"bufio"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
)

// "TV only" sound: the helper's capture stream is itself a sink, so PipeWire
// removes it with the helper's connection however the helper ends. It becomes
// the default only through the runtime key default.audio.sink, which
// WirePlumber never saves and recomputes from the user's configured default
// once the sink is gone. Nothing needs restoring.
const sinkName = "dux_cast"

func audioPipelineFor(target string) string {
	src := "pipewiresrc stream-properties=props,stream.capture.sink=true"
	if target != "both" {
		// String-typed values: GstStructure writes a plain false as FALSE, which
		// WirePlumber ignores. always-process starts the node before anything
		// plays; without it pipewiresrc waits and fails after 30 s.
		src = `pipewiresrc stream-properties="props,media.class=Audio/Sink,node.name=` + sinkName +
			`,node.description=Cast,state.restore-props=(string)false,node.always-process=(string)true"`
	}
	return src + " ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2 ! " +
		"opusenc name=enc bitrate=128000 frame-size=10 ! appsink name=out sync=false"
}

// holdDefault keeps the cast sink the default while the user's configured
// default stays what it was. Plugging in a device makes WirePlumber recompute
// the runtime default; a changed configured default means the user picked an
// output, and then the speakers win.
func holdDefault() (stop func()) {
	cmd := exec.Command("pw-metadata", "-m", "-n", "default", "0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		log.Printf("audio: %v, sound stays on this computer", err)
		return func() {}
	}
	go func() {
		var h holder
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			key, value, ok := parseUpdate(sc.Text())
			if !ok || !h.update(key, value) {
				continue
			}
			exec.Command("pw-metadata", "-n", "default", "0", "default.audio.sink",
				`{"name":"`+sinkName+`"}`, "Spa:String:JSON").Run()
		}
	}()
	return func() {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

type holder struct {
	configured, initial string
	seenConfigured      bool
}

// update records one metadata change and says whether to claim the default.
func (h *holder) update(key, value string) bool {
	switch key {
	case "default.configured.audio.sink":
		if !h.seenConfigured {
			h.initial, h.seenConfigured = value, true
		}
		h.configured = value
		return false
	case "default.audio.sink":
		// ponytail: no brake; WirePlumber only recomputes on device or
		// configured-default changes, so nothing ping-pongs with it.
		return value != sinkName && h.configured == h.initial
	}
	return false
}

var updateRe = regexp.MustCompile(`^update: id:0 key:'([^']*)' value:'(.*)' type:'[^']*'$`)
var nameRe = regexp.MustCompile(`"name"\s*:\s*"([^"]*)"`)

// parseUpdate reads a pw-metadata line; value is the node name, "" if unset.
func parseUpdate(line string) (key, value string, ok bool) {
	m := updateRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", "", false
	}
	if n := nameRe.FindStringSubmatch(m[2]); n != nil {
		value = n[1]
	}
	return m[1], value, true
}
