package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// mediaSession plays a queue of files and links on the cast device: a single
// file is a queue of one, a slideshow a queue with an interval. The device
// fetches and decodes each item itself; the helper only prepares what the
// device can't take as it is (MKV, too large or unsupported video, huge
// photos, HEIC), one or two items ahead so the next one shows at once.
// A YouTube link plays alone, in the device's YouTube app.
type mediaSession struct {
	h    *helper
	dev  device
	opts queueOptions

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	cmds   chan command
	ready  chan int // an item finished preparing

	mu        sync.Mutex
	items     []*queueItem
	index     int
	paused    bool // a slideshow's auto-advance is held
	phase     string
	st        playerStatus
	subsOn    bool
	volume    float64
	canVolume bool
	cast      *castConn
	transport string
	requestID int64
	loadReq   int64 // the LOAD whose answer names the media session
	working   bool  // the preparing goroutine runs
	srv       *server
	stopMpris func()
	player    *Player
}

type queueOptions struct {
	Interval      int     // seconds per photo; 0 = advance by hand
	Loop          bool    // start over after the last item
	ShrinkPhotos  bool    // compress photos above 4K to 4K JPEG; off sends originals
	ConvertVideos bool    // convert to formats the device plays: video above 4K or in other codecs, HEIC
	Port          int     // file server port
	StartAt       float64 // position in the first item, for a moved cast
}

type queueItem struct {
	source   string // absolute path or http(s) link
	title    string
	artist   string // songs
	kind     string // image, video, audio, link, youtube
	status   string // waiting, preparing, ready, failed
	progress float64
	media    mediaInfo
	files    []string // temporary files to delete
	err      error
	code     string
}

func newQueueItem(source string) *queueItem {
	it := &queueItem{source: source, title: filepath.Base(source), status: "waiting"}
	if u, err := url.Parse(source); err == nil && u.Host != "" {
		it.title = u.Host
		if base := path.Base(u.Path); base != "/" && base != "." {
			it.title = base
		}
	}
	ct := typeByExt(source)
	switch {
	case isLink(source):
		it.kind = "link"
		if _, ok := parseYouTube(source); ok {
			it.kind = "youtube"
		}
	case isHEIC(source), strings.HasPrefix(ct, "image/"):
		it.kind = "image"
	case strings.HasPrefix(ct, "audio/"):
		it.kind = "audio"
	default:
		it.kind = "video"
	}
	return it
}

func newMediaSession(h *helper, d device, sources []string, index int, opts queueOptions) *mediaSession {
	ctx, cancel := context.WithCancel(context.Background())
	m := &mediaSession{h: h, dev: d, opts: opts, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		cmds: make(chan command, 8), ready: make(chan int, 8), phase: "starting", index: index}
	for _, s := range sources {
		m.items = append(m.items, newQueueItem(s))
	}
	if index < 0 || index >= len(m.items) {
		m.index = 0
	}
	return m
}

func (m *mediaSession) deviceID() string { return m.dev.ID }

func (m *mediaSession) status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.items[m.index]
	title := cur.title
	if m.st.Title != "" && (cur.kind == "youtube" || cur.kind == "link") {
		title = m.st.Title
	}
	at := int64(0)
	if !m.st.at.IsZero() {
		at = m.st.at.UnixMilli()
	}
	queue := make([]map[string]any, len(m.items))
	for i, it := range m.items {
		q := map[string]any{"title": it.title, "artist": it.artist, "kind": it.kind, "status": it.status, "progress": it.progress}
		if it.status == "failed" {
			q["error"] = it.code
			var miss *missingError
			if errors.As(it.err, &miss) {
				q["feature"], q["missing"] = miss.feature, miss.missing
			}
		}
		queue[i] = q
	}
	interval := m.opts.Interval
	if m.paused {
		interval = 0
	}
	return map[string]any{"kind": "media", "device": m.dev.ID, "name": m.dev.Name, "phase": m.phase,
		"title": title, "progress": cur.progress, "playerState": m.st.PlayerState, "position": m.st.CurrentTime,
		"positionAt": at, "duration": m.st.Duration, "rate": max(m.st.Rate, 1),
		"subtitles": cur.media.subURL != "", "subtitlesOn": m.subsOn, "volume": m.volume, "canVolume": m.canVolume,
		"queue": queue, "index": m.index, "interval": interval, "paused": m.paused, "loop": m.opts.Loop}
}

// position is where the current item is now, for moving the cast.
func (m *mediaSession) position() (index int, at float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	at = m.st.CurrentTime
	if m.st.PlayerState == "PLAYING" && !m.st.at.IsZero() {
		at += time.Since(m.st.at).Seconds() * max(m.st.Rate, 1)
	}
	return m.index, at
}

func (m *mediaSession) sources() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := make([]string, len(m.items))
	for i, it := range m.items {
		s[i] = it.source
	}
	return s
}

func (m *mediaSession) setPhase(p string) {
	m.mu.Lock()
	m.phase = p
	m.mu.Unlock()
	m.h.emitState()
}

// Stop ends playback; run closes the media app on the device and cleans up.
func (m *mediaSession) Stop() { m.cancel() }

func (m *mediaSession) Wait(d time.Duration) {
	select {
	case <-m.done:
	case <-time.After(d):
	}
}

func (m *mediaSession) SetVolume(level float64) {
	m.mu.Lock()
	c, can := m.cast, m.canVolume
	m.requestID++
	id := m.requestID
	m.mu.Unlock()
	if c != nil && can {
		c.send(platformID, nsReceiver, map[string]any{"type": "SET_VOLUME", "volume": map[string]any{"level": level}, "requestId": id})
	}
}

// control hands a command to the run loop, which owns the queue.
func (m *mediaSession) control(c command) {
	select {
	case m.cmds <- c:
	case <-m.ctx.Done():
	}
}

// send issues a media command for the loaded media.
func (m *mediaSession) send(typ string, extra map[string]any) {
	m.mu.Lock()
	c, tr, sid := m.cast, m.transport, m.st.SessionID
	m.requestID++
	id := m.requestID
	m.mu.Unlock()
	if c == nil || sid == 0 {
		return
	}
	req := map[string]any{"type": typ, "mediaSessionId": sid, "requestId": id}
	for k, v := range extra {
		req[k] = v
	}
	c.send(tr, nsMedia, req)
}

func (m *mediaSession) run() (code string, err error) {
	defer close(m.done)
	var undo cleanups
	defer func() { undo.run() }()
	defer func() {
		m.mu.Lock()
		for _, it := range m.items {
			for _, f := range it.files {
				os.Remove(f)
			}
		}
		m.mu.Unlock()
	}()
	stopped := func(c string, e error) (string, error) {
		if m.ctx.Err() != nil {
			return "", nil
		}
		return c, e
	}

	if !m.dev.has("chromecast") {
		return "airplayNoMedia", errors.New("files and links play over Chromecast only")
	}
	youtube := len(m.items) == 1 && m.items[0].kind == "youtube"
	if !youtube {
		for _, it := range m.items {
			if it.kind == "youtube" {
				it.status, it.code, it.err = "failed", "youtubeInQueue", errors.New("YouTube links play alone")
			}
		}
		needsServer := false
		for _, it := range m.items {
			needsServer = needsServer || (it.kind != "link" && it.kind != "youtube")
		}
		if needsServer {
			ip, err := sourceIP(m.dev.Host)
			if err != nil {
				return "deviceLost", err
			}
			srv, err := newServer(net.JoinHostPort(ip, strconv.Itoa(m.opts.Port)), m.dev.Host)
			if err != nil {
				return "portInUse", err
			}
			undo.add(srv.close)
			m.srv = srv
		}
		// Start preparing while the device app launches.
		m.prepareAhead()
	}

	cast, err := dialCast(net.JoinHostPort(m.dev.Host, strconv.Itoa(m.dev.Port)))
	if err != nil {
		return stopped("deviceLost", err)
	}
	undo.add(cast.Close)
	m.mu.Lock()
	m.cast = cast
	m.mu.Unlock()
	app := defaultMediaAppID
	if youtube {
		app = youtubeAppID
	}
	transport, sessionID, st, code, err := launchApp(m.ctx, cast, app, func() { m.setPhase("authorizing") })
	if err != nil {
		return stopped(code, err)
	}
	m.updateVolume(st)
	undo.add(func() {
		cast.send(platformID, nsReceiver, map[string]any{"type": "STOP", "sessionId": sessionID, "requestId": 2})
		time.Sleep(200 * time.Millisecond)
	})
	m.mu.Lock()
	m.transport = transport
	m.requestID = 10
	m.mu.Unlock()
	m.stopMpris = m.startMpris()
	undo.add(func() { m.stopMpris() })

	if youtube {
		m.items[0].status = "ready"
		m.mu.Lock()
		m.st.follow = true
		m.mu.Unlock()
		yt, _ := parseYouTube(m.items[0].source)
		yt.start = max(yt.start, int(m.opts.StartAt))
		if err := playOnYouTube(m.ctx, cast, transport, yt); err != nil {
			return stopped("youtubeFailed", err)
		}
		cast.send(transport, nsMedia, map[string]any{"type": "GET_STATUS", "requestId": 9})
	}

	pending := -1 // an item waiting to be shown once it's prepared
	var advance <-chan time.Time
	var unreachable <-chan time.Time
	show := func(i int) {
		advance = nil
		m.mu.Lock()
		m.index = i
		it := m.items[i]
		status := it.status
		m.mu.Unlock()
		m.prune()
		switch status {
		case "ready":
			pending = -1
			m.load(it)
			if m.srv != nil && it.kind != "link" && unreachable == nil && !m.srv.fetched.Load() {
				unreachable = time.After(10 * time.Second)
			}
			if it.kind == "image" && m.autoAdvance() {
				advance = time.After(time.Duration(m.opts.Interval) * time.Second)
			}
		case "failed":
			pending = -1
			m.h.emitError(it.code, it.err)
			if len(m.items) > 1 {
				advance = time.After(2 * time.Second)
			}
		default:
			pending = i
			m.setPhase("converting")
		}
		m.prepareAhead()
		m.h.emitState()
	}
	// step moves by delta, wrapping when looping; it reports false at an end.
	step := func(delta int) bool {
		m.mu.Lock()
		i, n := m.index+delta, len(m.items)
		m.mu.Unlock()
		if i < 0 || i >= n {
			if !m.opts.Loop {
				return false
			}
			i = (i + n) % n
		}
		show(i)
		return true
	}
	if !youtube {
		show(m.index)
		if len(m.items) == 1 && m.items[0].status == "failed" {
			return m.items[0].code, m.items[0].err
		}
	}

	for {
		select {
		case <-m.ctx.Done():
			return "", nil
		case <-unreachable:
			unreachable = nil
			if !m.srv.fetched.Load() {
				return stopped("serverUnreachable", fmt.Errorf("the device did not fetch anything from %s", m.srv.ln.Addr()))
			}
		case i := <-m.ready:
			if i != pending {
				continue
			}
			if len(m.items) == 1 && m.items[0].status == "failed" {
				return stopped(m.items[0].code, m.items[0].err)
			}
			show(i)
		case <-advance:
			if !step(1) {
				advance = nil
			}
		case c := <-m.cmds:
			switch c.Cmd {
			case "next":
				step(1)
			case "previous":
				step(-1)
			case "jump":
				if c.Index >= 0 && c.Index < len(m.items) {
					show(c.Index)
				}
			case "slideshow":
				m.mu.Lock()
				m.opts.Interval, m.paused = c.Interval, false
				cur := m.items[m.index]
				m.mu.Unlock()
				advance = nil
				if cur.kind == "image" && m.autoAdvance() {
					advance = time.After(time.Duration(c.Interval) * time.Second)
				}
				m.h.emitState()
			case "pause":
				m.mu.Lock()
				m.paused = true
				m.mu.Unlock()
				advance = nil
				m.send("PAUSE", nil)
				m.h.emitState()
			case "resume":
				m.mu.Lock()
				m.paused = false
				cur := m.items[m.index]
				m.mu.Unlock()
				m.send("PLAY", nil)
				if cur.kind == "image" && m.autoAdvance() {
					advance = time.After(time.Duration(m.opts.Interval) * time.Second)
				}
				m.h.emitState()
			case "seek":
				m.send("SEEK", map[string]any{"currentTime": max(c.Position, 0)})
			case "subtitles":
				ids := []int{}
				if c.On {
					ids = []int{1}
				}
				m.mu.Lock()
				m.subsOn = c.On
				m.mu.Unlock()
				m.send("EDIT_TRACKS_INFO", map[string]any{"activeTrackIds": ids})
				m.h.emitState()
			}
		case msg, ok := <-cast.msgs:
			if !ok {
				return stopped("deviceLost", fmt.Errorf("cast channel closed"))
			}
			switch {
			case msg.ns == nsMedia:
				m.mu.Lock()
				// Until the LOAD is answered, other statuses belong to the
				// media it replaced.
				if m.st.SessionID == 0 && !m.st.follow && requestIDOf(msg.payload) != m.loadReq {
					m.mu.Unlock()
					continue
				}
				idle, err := m.st.update(msg.payload)
				cur := m.items[m.index]
				m.mu.Unlock()
				m.player.update(m)
				if err != nil {
					if len(m.items) == 1 {
						return stopped("loadFailed", err)
					}
					m.h.emitError("loadFailed", fmt.Errorf("%s: %w", cur.title, err))
					advance = time.After(2 * time.Second)
					continue
				}
				switch {
				case idle == "CANCELLED":
					return "", nil // stopped on the device
				case idle == "FINISHED" && youtube:
					// The YouTube app idles between playlist videos.
				case idle == "FINISHED" && cur.kind != "image":
					if !step(1) {
						if len(m.items) == 1 {
							return "", nil
						}
						m.setPhase("casting")
					}
					continue
				}
				m.setPhase("casting")
			case msg.ns == nsReceiver && typeOf(msg.payload) == "RECEIVER_STATUS":
				st := parseReceiverStatus(msg.payload)
				m.updateVolume(st)
				running := false
				for _, a := range st.Status.Applications {
					running = running || a.SessionID == sessionID
				}
				if !running {
					return stopped("receiverStopped", fmt.Errorf("the media app was closed on the device"))
				}
			case msg.ns == nsConnection && msg.src == transport && typeOf(msg.payload) == "CLOSE":
				return stopped("receiverStopped", fmt.Errorf("the device closed the media session"))
			}
		}
	}
}

func (m *mediaSession) autoAdvance() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opts.Interval > 0 && !m.paused && len(m.items) > 1
}

// load sends a prepared item to the device.
func (m *mediaSession) load(it *queueItem) {
	m.mu.Lock()
	m.requestID++
	id := m.requestID
	m.loadReq = id
	m.st = playerStatus{}
	m.subsOn = it.media.subURL != ""
	start := m.opts.StartAt
	m.opts.StartAt = 0
	c, tr := m.cast, m.transport
	m.mu.Unlock()
	c.send(tr, nsMedia, loadRequest(it.media, id, start))
}

func requestIDOf(p []byte) int64 {
	var m struct {
		RequestID int64 `json:"requestId"`
	}
	json.Unmarshal(p, &m)
	return m.RequestID
}

// prepareAhead keeps the current item and the next ones prepared, one at a
// time: the current one first, then the next video or the next two photos.
func (m *mediaSession) prepareAhead() {
	m.mu.Lock()
	if m.working || m.srv == nil && !m.hasLinks() {
		m.mu.Unlock()
		return
	}
	m.working = true
	m.mu.Unlock()
	go func() {
		for {
			m.mu.Lock()
			i := m.nextToPrepare()
			if i < 0 || m.ctx.Err() != nil {
				m.working = false
				m.mu.Unlock()
				return
			}
			it := m.items[i]
			it.status = "preparing"
			m.mu.Unlock()
			m.h.emitState()

			err := m.prepare(i, it)
			m.mu.Lock()
			if err != nil {
				it.status, it.err = "failed", err
				var miss *missingError
				switch {
				case errors.As(err, &miss):
					it.code = "missingDependency"
				case it.code == "":
					it.code = "unsupportedFile"
				}
			} else {
				it.status = "ready"
			}
			m.mu.Unlock()
			if err != nil {
				log.Printf("preparing %s: %v", it.source, err)
			}
			m.h.emitState()
			select {
			case m.ready <- i:
			case <-m.ctx.Done():
				return
			}
		}
	}()
}

func (m *mediaSession) hasLinks() bool {
	for _, it := range m.items {
		if it.kind == "link" {
			return true
		}
	}
	return false
}

// nextToPrepare picks the first waiting item in the look-ahead window.
// Called with mu held.
func (m *mediaSession) nextToPrepare() int {
	n := len(m.items)
	for ahead := 0; ahead <= 2 && ahead < n; ahead++ {
		i := m.index + ahead
		if i >= n {
			if !m.opts.Loop {
				break
			}
			i %= n
		}
		it := m.items[i]
		if ahead == 2 && it.kind != "image" {
			break // converting a video two ahead would compete with the current one
		}
		if it.status == "waiting" {
			return i
		}
	}
	return -1
}

// prune frees the prepared copies of items left well behind, so a long
// slideshow doesn't fill the cache.
func (m *mediaSession) prune() {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.items)
	for i, it := range m.items {
		// When looping, the items at the start are also the ones coming up.
		far := m.index-i > 2
		if m.opts.Loop {
			behind := (m.index - i + n) % n
			far = behind > 2 && behind < n-2
		}
		if far && it.status == "ready" && len(it.files) > 0 {
			for _, f := range it.files {
				os.Remove(f)
			}
			it.files, it.status = nil, "waiting"
		}
	}
}

func (m *mediaSession) setItemProgress(it *queueItem) func(float64) {
	return func(p float64) {
		m.mu.Lock()
		it.progress = p
		m.mu.Unlock()
		m.h.emitState()
	}
}

// prepare makes one item ready to load: its URL on the file server, after
// any conversion the device needs.
func (m *mediaSession) prepare(i int, it *queueItem) error {
	if it.kind == "link" {
		it.media = mediaInfo{url: it.source, contentType: remoteType(m.ctx, it.source), title: it.title}
		return nil
	}
	if _, err := os.Stat(it.source); err != nil {
		return err
	}
	var file, ct string
	var err error
	if it.kind == "image" {
		file, ct, err = m.prepareImage(it)
	} else {
		file, ct, err = m.prepareVideo(it)
	}
	if err != nil {
		return err
	}
	it.media = mediaInfo{url: m.srv.add(fmt.Sprintf("%d%s", i, path.Ext(file)), file, ct), contentType: ct, title: it.title}
	if it.kind == "audio" {
		song := probeSong(it.source)
		if song.title != "" {
			it.title = song.title
		}
		it.artist = song.artist
		it.media.title, it.media.artist, it.media.album = it.title, song.artist, song.album
		switch cover := folderCover(it.source); {
		case song.cover != nil:
			it.media.coverURL = m.srv.addBytes(fmt.Sprintf("%d-cover", i), song.cover, song.coverType)
		case cover != "":
			it.media.coverURL = m.srv.add(fmt.Sprintf("%d-cover%s", i, path.Ext(cover)), cover, typeByExt(cover))
		}
	}
	if it.kind == "video" {
		if sub := sidecarSubtitle(it.source); sub != "" {
			if vtt, err := loadSubtitle(sub); err == nil {
				it.media.subURL, it.media.subLang = m.srv.addBytes(fmt.Sprintf("%d.vtt", i), vtt, "text/vtt"), "und"
			}
		}
	}
	return nil
}

// prepareVideo converts what the device can't play, remuxes MKV so it can
// seek, and passes everything else through untouched.
func (m *mediaSession) prepareVideo(it *queueItem) (string, string, error) {
	file, ct := it.source, typeByExt(it.source)
	if it.kind == "video" {
		v, err := probeVideo(file)
		switch {
		case err != nil:
			log.Printf("probe %s: %v", file, err)
		case v.needsConvert() && !m.opts.ConvertVideos:
			return "", "", fmt.Errorf("%dx%d %s needs converting, which is off", v.width, v.height, v.codec)
		case v.needsConvert():
			if err := need("convert"); err != nil {
				return "", "", err
			}
			out, err := convertVideo(m.ctx, file, v, m.setItemProgress(it))
			if out != "" {
				it.files = append(it.files, out, out+".part")
			}
			return out, "video/mp4", err
		}
	}
	if isMatroska(file) {
		if err := need("mkv"); err != nil {
			return "", "", err
		}
		out, err := remuxMKV(m.ctx, file, "auto", -1)
		if out != "" {
			it.files = append(it.files, out, out+".part")
		}
		return out, "video/mp4", err
	}
	if ct == "" {
		return "", "", fmt.Errorf("unknown file type %q", filepath.Ext(file))
	}
	return file, ct, nil
}

// prepareImage decodes HEIC and shrinks photos larger than 4K, which the
// device would otherwise download and scale itself, slowly.
func (m *mediaSession) prepareImage(it *queueItem) (string, string, error) {
	file := it.source
	if isHEIC(file) {
		if !m.opts.ConvertVideos {
			return "", "", errors.New("HEIC needs converting, which is off")
		}
		if err := need("heic"); err != nil {
			return "", "", err
		}
		out, err := cacheFile(fmt.Sprintf("%p.jpg", it))
		if err != nil {
			return "", "", err
		}
		it.files = append(it.files, out)
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		tool := heifTool()
		if b, err := exec.CommandContext(ctx, tool, "-q", "90", file, out).CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("%s: %v: %s", tool, err, strings.TrimSpace(string(b)))
		}
		file = out
	}
	ct := typeByExt(file)
	if !m.opts.ShrinkPhotos || !needsShrink(file) {
		return file, ct, nil
	}
	if err := need("photos"); err != nil {
		log.Printf("sending %s as it is: %v", file, err)
		return file, ct, nil
	}
	out, err := cacheFile(fmt.Sprintf("%p-4k.jpg", it))
	if err != nil {
		return "", "", err
	}
	it.files = append(it.files, out)
	// videoflip applies the camera's orientation tag; the caps let videoscale
	// pick the largest size within 4K that keeps the aspect ratio.
	err = runGst(m.ctx, "filesrc name=src ! decodebin ! videoflip video-direction=auto ! videoconvert ! videoscale ! "+
		"video/x-raw,width=[1,3840],height=[1,2160],pixel-aspect-ratio=1/1 ! jpegenc quality=90 snapshot=true ! filesink name=sink",
		map[string]map[string]string{"src": {"location": file}, "sink": {"location": out}}, nil)
	if err != nil {
		return "", "", err
	}
	return out, "image/jpeg", nil
}

// needsShrink: photos above 4K, or big files, load slowly on the device.
func needsShrink(file string) bool {
	st, err := os.Stat(file)
	if err != nil {
		return false
	}
	f, err := os.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return st.Size() > 4<<20 // WebP and other formats Go doesn't read
	}
	return cfg.Width*cfg.Height > maxPixels || st.Size() > 4<<20
}

func isHEIC(file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".heic", ".heif":
		return true
	}
	return false
}

func (m *mediaSession) updateVolume(st receiverStatus) {
	v := st.Status.Volume
	if v == nil {
		return
	}
	m.mu.Lock()
	changed := m.volume != v.Level || m.canVolume != (v.ControlType != "fixed")
	m.volume, m.canVolume = v.Level, v.ControlType != "fixed"
	m.mu.Unlock()
	if changed {
		m.h.emitState()
	}
}
