package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// AirPlay mirroring goes through doubletake, an AirPlay 2 sender. Its daemon
// runs only for the length of a cast, because it keeps browsing the network
// while it lives, and is driven over its control socket: one JSON request per
// connection, polled for state since it pushes nothing. doubletake captures
// the screen itself, so it opens its own portal dialog. Sound stays on this
// computer: doubletake sets the receiver to full volume whenever it sends
// sound (0.5.0), and has no option to keep the current level.
type airplaySession struct {
	h    *helper
	dev  device
	opts castOptions

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	pins   chan string

	mu         sync.Mutex
	phase      string // starting, pairing, casting
	since      time.Time
	credential string // pin or password, while pairing
}

func newAirplaySession(h *helper, d device, opts castOptions) *airplaySession {
	opts.defaults()
	ctx, cancel := context.WithCancel(context.Background())
	return &airplaySession{h: h, dev: d, opts: opts, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		pins: make(chan string, 1), phase: "starting", since: time.Now()}
}

func (a *airplaySession) deviceID() string  { return a.dev.ID }
func (a *airplaySession) Stop()             { a.cancel() }
func (a *airplaySession) SetVolume(float64) {}

func (a *airplaySession) Wait(d time.Duration) {
	select {
	case <-a.done:
	case <-time.After(d):
	}
}

func (a *airplaySession) status() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := map[string]any{"kind": "mirror", "protocol": "airplay", "device": a.dev.ID, "name": a.dev.Name,
		"phase": a.phase, "since": a.since.Unix(), "volume": 0, "canVolume": false}
	if a.phase == "pairing" {
		st["credential"] = a.credential
	}
	return st
}

// pair hands the PIN or password the user typed to the waiting connection.
func (a *airplaySession) pair(pin string) {
	select {
	case a.pins <- pin:
	default:
	}
}

func (a *airplaySession) setPhase(p, credential string) {
	a.mu.Lock()
	changed := a.phase != p || a.credential != credential
	if p == "casting" && a.phase != "casting" {
		a.since = time.Now()
	}
	a.phase, a.credential = p, credential
	a.mu.Unlock()
	if changed {
		a.h.emitState()
	}
}

type dtStatus struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Streams []struct {
		DeviceIP       string `json:"device_ip"`
		State          string `json:"state"`
		CredentialKind string `json:"credential_kind"`
	} `json:"streams"`
}

func (a *airplaySession) run() (code string, err error) {
	defer close(a.done)
	defer a.cancel() // also when the device ends the session
	var undo cleanups
	defer func() { undo.run() }()
	stopped := func(c string, e error) (string, error) {
		if a.ctx.Err() != nil {
			return "", nil
		}
		return c, e
	}
	if err := need("airplay"); err != nil {
		return "missingDependency", err
	}
	if a.opts.Audio != "local" {
		a.h.emit(map[string]any{"event": "warning", "code": "airplaySound"})
	}

	// Not /tmp: another user could create the folder there first.
	dir, err := cacheDir()
	if runtime := os.Getenv("XDG_RUNTIME_DIR"); runtime != "" {
		dir = filepath.Join(runtime, "dms-cast")
		err = os.MkdirAll(dir, 0o700)
	}
	if err != nil {
		return "airplayFailed", err
	}
	sock := filepath.Join(dir, "doubletake.sock")
	// A doubletake that died left its socket behind.
	os.Remove(sock)
	data, err := dataDir()
	if err != nil {
		return "airplayFailed", err
	}
	args := []string{"-daemonize", "-socket", sock, "-creds", filepath.Join(data, "doubletake-credentials.json"),
		"-no-audio", "-fps", strconv.Itoa(a.opts.FPS), "-target-latency-ms", strconv.Itoa(a.opts.Delay), "-port", strconv.Itoa(a.dev.AirplayPort)}
	if a.opts.Codec == "h264" || a.opts.Codec == "hevc" {
		args = append(args, "-video-codec", a.opts.Codec)
	}
	if a.opts.Kbps > 0 {
		args = append(args, "-bitrate", strconv.Itoa(a.opts.Kbps))
	}
	// doubletake's automatic choice is the Vulkan encoder, which sends no
	// frames from a real screen at 60 fps (doubletake 0.5.0, AMD); VA-API,
	// which Chromecast mirroring uses too, works.
	if hasElement("vah264enc") {
		args = append(args, "-hwaccel", "vaapi")
	}
	if a.opts.AirplayPorts != "" {
		args = append(args, "-port-range", a.opts.AirplayPorts)
	}
	cmd := exec.Command(doubletakePath(), append(args, doubletakeTestArgs...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if stderr, err := cmd.StderrPipe(); err == nil {
		go func() {
			sc := bufio.NewScanner(stderr)
			for sc.Scan() {
				debugf("doubletake: %s", sc.Text())
			}
		}()
	}
	if err := cmd.Start(); err != nil {
		return "airplayFailed", err
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	undo.add(func() {
		dtRequest(sock, map[string]any{"cmd": "disconnect", "target": a.dev.Host})
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
			<-exited
		}
	})

	// The socket appears once the daemon is up.
	for i := 0; ; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		select {
		case <-exited:
			return stopped("airplayFailed", errors.New("doubletake exited at startup"))
		case <-a.ctx.Done():
			return "", nil
		case <-time.After(50 * time.Millisecond):
		}
		if i > 100 {
			return stopped("airplayFailed", errors.New("doubletake didn't open its control socket"))
		}
	}
	connect := map[string]any{"cmd": "connect", "target": a.dev.Host, "port": a.dev.AirplayPort}
	if st, err := dtRequest(sock, connect); err != nil || !st.OK {
		return stopped("airplayFailed", fmt.Errorf("connect: %v %s", err, st.Error))
	}

	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	seen := false
	for {
		select {
		case <-a.ctx.Done():
			return "", nil
		case <-exited:
			return stopped("airplayFailed", errors.New("doubletake exited"))
		case pin := <-a.pins:
			pair := map[string]any{"cmd": "connect", "target": a.dev.Host, "port": a.dev.AirplayPort, "pin": pin}
			if st, err := dtRequest(sock, pair); err != nil || !st.OK {
				log.Printf("airplay pairing: %v %s", err, st.Error)
			}
			a.setPhase("starting", "")
		case <-poll.C:
			st, err := dtRequest(sock, map[string]any{"cmd": "status"})
			if err != nil {
				return stopped("airplayFailed", err)
			}
			found := false
			for _, s := range st.Streams {
				if s.DeviceIP != a.dev.Host {
					continue
				}
				found, seen = true, true
				switch s.State {
				case "pin_required":
					a.setPhase("pairing", s.CredentialKind)
				case "streaming":
					a.setPhase("casting", "")
				default:
					a.setPhase("starting", "")
				}
			}
			// A stream that ended or failed drops out of the list.
			if !found && seen {
				if st.Error != "" {
					return stopped("airplayFailed", errors.New(st.Error))
				}
				return stopped("deviceLost", errors.New("the AirPlay stream ended"))
			}
		}
	}
}

// doubletakeTestArgs lets tests use doubletake's synthetic video instead of
// the screen, so no portal dialog opens.
var doubletakeTestArgs []string

// dtRequest sends one request to doubletake's control socket.
func dtRequest(sock string, req map[string]any) (dtStatus, error) {
	var st dtStatus
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return st, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return st, err
	}
	err = json.NewDecoder(conn).Decode(&st)
	return st, err
}

// doubletakePath finds doubletake on PATH, else where the plugin or a
// source build put it.
func doubletakePath() string {
	if p, err := exec.LookPath("doubletake"); err == nil {
		return p
	}
	var candidates []string
	if d, err := dataDir(); err == nil {
		candidates = append(candidates, filepath.Join(d, "bin", "doubletake"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "go", "bin", "doubletake"))
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.Mode()&0o111 != 0 {
			return c
		}
	}
	return ""
}

// dataDir is the plugin's data folder, ~/.local/share/dms/cast.
func dataDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(base, "dms", "cast")
	return dir, os.MkdirAll(dir, 0o700)
}
