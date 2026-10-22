// Command screencast-helper does the casting for the Screencast plugin. It
// reads one JSON command per line on stdin and writes one JSON event per line
// on stdout, and exits when stdin closes.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// device is one TV or speaker. One that speaks Chromecast and AirPlay is a
// single device: it keeps its Chromecast id and lists both protocols.
type device struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Model       string   `json:"model"`
	Protocol    string   `json:"protocol"` // the main one: chromecast when there is one
	Protocols   []string `json:"protocols"`
	Host        string   `json:"-"`
	Port        int      `json:"-"` // Cast control port
	AirplayPort int      `json:"-"`
}

func (d device) has(protocol string) bool { return slices.Contains(d.Protocols, protocol) }

type command struct {
	Cmd      string  `json:"cmd"`
	On       bool    `json:"on"`
	Device   string  `json:"device"`
	Level    float64 `json:"level"`
	Path     string  `json:"path"`
	URL      string  `json:"url"`
	Port     int     `json:"port"` // file server port, 0 for any
	Pin      string  `json:"pin"`  // AirPlay pairing PIN or password
	Position float64 `json:"position"`
	Size     int     `json:"size"` // thumbnail width
	// queue
	Items         []struct{ Path, URL string } `json:"items"`
	Index         int                          `json:"index"`
	Interval      int                          `json:"interval"`
	Loop          bool                         `json:"loop"`
	ShrinkPhotos  *bool                        `json:"shrinkPhotos"`
	ConvertVideos *bool                        `json:"convertVideos"`
	castOptions
}

// source is the path or link of a play command or queue item.
func source(path, link string) (string, error) {
	if link != "" {
		link = strings.TrimSpace(link)
		if !isLink(link) {
			return "", fmt.Errorf("not an http(s) link: %q", link)
		}
		return link, nil
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("not an absolute path: %q", path)
	}
	return path, nil
}

func orTrue(b *bool) bool { return b == nil || *b }

// activeSession is a mirroring cast or a file playing on the TV.
type activeSession interface {
	deviceID() string
	status() map[string]any
	run() (code string, err error)
	Stop()
	Wait(time.Duration)
	SetVolume(level float64)
}

// pendingSession holds the slot while the device is looked up.
type pendingSession struct {
	h     *helper
	id    string
	since time.Time
}

func (p *pendingSession) deviceID() string { return p.id }
func (p *pendingSession) status() map[string]any {
	return map[string]any{"device": p.id, "phase": "starting", "since": p.since.Unix()}
}
func (p *pendingSession) run() (string, error) { return "", nil }
func (p *pendingSession) Wait(time.Duration)   {}
func (p *pendingSession) SetVolume(float64)    {}
func (p *pendingSession) Stop() {
	p.h.mu.Lock()
	if p.h.sess == p {
		p.h.sess = nil
	}
	p.h.mu.Unlock()
	p.h.emitState()
}

type helper struct {
	outMu sync.Mutex
	out   *json.Encoder

	mu           sync.Mutex
	devices      map[string]device
	stopDiscover context.CancelFunc
	discoverers  int // the plugin's request plus a start waiting for its device
	sess         activeSession
	// The portal's restore tokens are single use: the newest one is kept for
	// moving a cast, which opens a new portal session.
	restoreToken string
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetPrefix("")
	h := &helper{out: json.NewEncoder(os.Stdout), devices: map[string]device{}}
	removeStaleRemuxes()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	for {
		select {
		case <-sig:
			h.shutdown()
			return
		case line, ok := <-lines:
			if !ok {
				h.shutdown()
				return
			}
			var c command
			if err := json.Unmarshal([]byte(line), &c); err != nil {
				log.Printf("bad command %q: %v", line, err)
				continue
			}
			h.handle(c)
		}
	}
}

func (h *helper) handle(c command) {
	switch c.Cmd {
	case "check":
		h.emit(dependencyReport())
	case "discover":
		h.setDiscovery(c.On)
	case "start":
		// Chromecast unless the device only speaks AirPlay, or the user chose
		// AirPlay for mirroring it.
		h.start(c.Device, func(d device) activeSession {
			if d.has("airplay") && (c.Protocol == "airplay" || !d.has("chromecast")) {
				return newAirplaySession(h, d, c.castOptions)
			}
			return newSession(h, d, c.castOptions)
		})
	case "pair":
		h.mu.Lock()
		a, _ := h.sess.(*airplaySession)
		h.mu.Unlock()
		if a != nil {
			a.pair(c.Pin)
		}
	case "play":
		src, err := source(c.Path, c.URL)
		if err != nil {
			h.emitError("unsupportedLink", err)
			return
		}
		opts := queueOptions{ShrinkPhotos: c.ShrinkPhotos != nil && *c.ShrinkPhotos, ConvertVideos: orTrue(c.ConvertVideos), Port: c.Port}
		h.start(c.Device, func(d device) activeSession { return newMediaSession(h, d, []string{src}, 0, opts) })
	case "playlist":
		go h.expandPlaylist(c.Path)
	case "queue":
		var srcs []string
		add := func(path, link string) {
			if src, err := source(path, link); err == nil {
				srcs = append(srcs, src)
			}
		}
		for _, it := range c.Items {
			if it.URL == "" && isPlaylist(it.Path) {
				entries, _ := readPlaylist(it.Path)
				for _, e := range entries {
					add(e.Path, e.URL)
				}
				continue
			}
			add(it.Path, it.URL)
		}
		if len(srcs) == 0 {
			h.emitError("unsupportedLink", errors.New("empty queue"))
			return
		}
		opts := queueOptions{Interval: c.Interval, Loop: c.Loop, ShrinkPhotos: c.ShrinkPhotos != nil && *c.ShrinkPhotos,
			ConvertVideos: orTrue(c.ConvertVideos), Port: c.Port}
		h.start(c.Device, func(d device) activeSession { return newMediaSession(h, d, srcs, c.Index, opts) })
	case "move":
		go h.move(c.Device)
	case "thumbnail":
		go h.thumbnail(c.Path, c.Size)
	case "pause", "resume", "seek", "subtitles", "next", "previous", "jump", "slideshow":
		h.mu.Lock()
		m, _ := h.sess.(*mediaSession)
		h.mu.Unlock()
		if m != nil {
			m.control(c)
		}
	case "stop":
		h.mu.Lock()
		s := h.sess
		h.mu.Unlock()
		if s != nil {
			s.Stop()
		}
	case "volume":
		h.mu.Lock()
		s := h.sess
		h.mu.Unlock()
		if s != nil {
			s.SetVolume(c.Level)
		}
	default:
		log.Printf("unknown command %q", c.Cmd)
	}
}

func (h *helper) emit(v any) {
	h.outMu.Lock()
	defer h.outMu.Unlock()
	if err := h.out.Encode(v); err != nil {
		log.Printf("stdout: %v", err)
	}
}

func (h *helper) emitError(code string, err error) {
	log.Printf("error %s: %v", code, err)
	msg := map[string]any{"event": "error", "code": code}
	var m *missingError
	if errors.As(err, &m) {
		msg["feature"], msg["missing"] = m.feature, m.missing
	}
	if err != nil {
		msg["detail"] = err.Error()
	}
	h.emit(msg)
}

// missingError says which packages a feature needs; the plugin names them.
type missingError struct {
	feature string
	missing []string
}

func (e *missingError) Error() string {
	return e.feature + " needs " + strings.Join(e.missing, ", ")
}

// need returns a missingError when a feature's packages aren't installed.
func need(feature string) error {
	if missing := missingFor(feature); len(missing) > 0 {
		return &missingError{feature, missing}
	}
	return nil
}

// emitState pushes the full state; the plugin keeps no state of its own.
func (h *helper) emitState() {
	h.mu.Lock()
	devs := make([]device, 0, len(h.devices))
	for _, d := range h.devices {
		devs = append(devs, d)
	}
	var sess any
	if h.sess != nil {
		sess = h.sess.status()
	}
	h.mu.Unlock()
	slices.SortFunc(devs, func(a, b device) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	h.emit(map[string]any{"event": "state", "devices": devs, "session": sess})
}

// setDiscovery follows the plugin's request to browse while a device list is
// on screen.
func (h *helper) setDiscovery(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if on == (h.discoverers&1 == 1) {
		return
	}
	if on {
		h.discoverers |= 1
	} else {
		h.discoverers &^= 1
	}
	h.updateDiscoveryLocked()
}

func (h *helper) updateDiscoveryLocked() {
	if h.discoverers != 0 && h.stopDiscover == nil {
		ctx, cancel := context.WithCancel(context.Background())
		h.stopDiscover = cancel
		go func() {
			err := discover(ctx, func(devs []device) {
				h.mu.Lock()
				h.devices = map[string]device{}
				for _, d := range devs {
					h.devices[d.ID] = d
				}
				h.mu.Unlock()
				h.emitState()
			})
			if err != nil && ctx.Err() == nil {
				if missing := need("discovery"); missing != nil {
					h.emitError("missingDependency", missing)
				} else {
					h.emitError("avahi", err)
				}
			}
		}()
	}
	if h.discoverers == 0 && h.stopDiscover != nil {
		h.stopDiscover()
		h.stopDiscover = nil
		// Keep the device being cast to; forget the rest.
		for id := range h.devices {
			if h.sess == nil || h.sess.deviceID() != id {
				delete(h.devices, id)
			}
		}
		go h.emitState()
	}
}

// lookup finds a device, browsing for a few seconds if it isn't known yet
// (the Control Center tile starts a cast without showing the list).
func (h *helper) lookup(id string) (device, bool) {
	h.mu.Lock()
	d, ok := h.devices[id]
	if ok {
		h.mu.Unlock()
		return d, true
	}
	h.discoverers |= 2
	h.updateDiscoveryLocked()
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.discoverers &^= 2
		h.updateDiscoveryLocked()
		h.mu.Unlock()
	}()
	for range 30 {
		time.Sleep(200 * time.Millisecond)
		h.mu.Lock()
		d, ok = h.devices[id]
		h.mu.Unlock()
		if ok {
			return d, true
		}
	}
	return device{}, false
}

func (h *helper) start(id string, create func(device) activeSession) {
	h.mu.Lock()
	if h.sess != nil {
		h.mu.Unlock()
		h.emitError("busy", fmt.Errorf("already casting to %s", h.sess.deviceID()))
		return
	}
	// Reserve the slot so a second start can't race this one.
	placeholder := &pendingSession{h: h, id: id, since: time.Now()}
	h.sess = placeholder
	h.mu.Unlock()
	h.emitState()

	go func() {
		d, ok := h.lookup(id)
		h.mu.Lock()
		cancelled := h.sess != placeholder
		h.mu.Unlock()
		if cancelled {
			return
		}
		if !ok {
			h.endSession(placeholder, "deviceNotFound", fmt.Errorf("%s did not show up on the network", id))
			return
		}
		s := create(d)
		h.mu.Lock()
		if h.sess != placeholder { // stopped while looking up
			h.mu.Unlock()
			return
		}
		h.sess = s
		h.mu.Unlock()
		code, err := s.run()
		h.endSession(s, code, err)
	}()
}

func (h *helper) endSession(s activeSession, code string, err error) {
	h.mu.Lock()
	if h.sess == s {
		h.sess = nil
	}
	h.mu.Unlock()
	if code != "" {
		h.emitError(code, err)
	}
	h.emitState()
}

// move ends what is casting and starts the same on another device: a
// mirroring cast with the same options, playback at the same item and time.
func (h *helper) move(id string) {
	h.mu.Lock()
	s := h.sess
	token := h.restoreToken
	h.mu.Unlock()
	if s == nil || s.deviceID() == id {
		return
	}
	var create func(device) activeSession
	switch s := s.(type) {
	case *session:
		opts := s.opts
		if token != "" {
			opts.RestoreToken = token
		}
		create = func(d device) activeSession { return newSession(h, d, opts) }
	case *airplaySession:
		opts := s.opts
		create = func(d device) activeSession {
			if d.has("airplay") && !d.has("chromecast") {
				return newAirplaySession(h, d, opts)
			}
			return newSession(h, d, opts)
		}
	case *mediaSession:
		index, at := s.position()
		opts := s.opts
		opts.StartAt = at
		srcs := s.sources()
		create = func(d device) activeSession { return newMediaSession(h, d, srcs, index, opts) }
	default:
		return
	}
	s.Stop()
	s.Wait(5 * time.Second)
	for range 50 { // until run has handed the slot back
		h.mu.Lock()
		free := h.sess != s
		h.mu.Unlock()
		if free {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.start(id, create)
}

func (h *helper) shutdown() {
	h.mu.Lock()
	s := h.sess
	if h.stopDiscover != nil {
		h.stopDiscover()
	}
	h.mu.Unlock()
	if s != nil {
		s.Stop()
		s.Wait(5 * time.Second)
	}
}

// removeStaleRemuxes deletes MP4 copies a crashed helper left in the cache.
// Only one helper runs at a time, so none of them is in use.
func removeStaleRemuxes() {
	cache, err := os.UserCacheDir()
	if err != nil {
		return
	}
	stale, _ := filepath.Glob(filepath.Join(cache, "dms", "screencast", "[0-9]*-*"))
	for _, f := range stale {
		os.Remove(f)
	}
}
