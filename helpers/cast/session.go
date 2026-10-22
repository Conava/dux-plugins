package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const mirroringAppID = "0F5096E8"

type castOptions struct {
	Audio        string `json:"audio"` // "tv" (the cast device only), "both", or "local" (this computer only)
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FPS          int    `json:"fps"`
	Kbps         int    `json:"kbps"`  // 0: pick from resolution and frame rate
	Codec        string `json:"codec"` // "auto", "h264" or "hevc"
	Delay        int    `json:"delay"` // target playout delay in ms
	RestoreToken string `json:"restoreToken"`
	Protocol     string `json:"protocol"`     // "airplay" to mirror over AirPlay where a device has both
	AirplayPorts string `json:"airplayPorts"` // doubletake's UDP port range, e.g. "47840-47849"
}

func (o *castOptions) defaults() {
	if o.Width == 0 || o.Height == 0 {
		o.Width, o.Height = 1920, 1080
	}
	if o.FPS == 0 {
		o.FPS = 30
	}
	if o.Kbps == 0 {
		// 12 Mbit/s held up at 1080p30; scale by pixels, and half again for 60 fps.
		o.Kbps = 12000 * o.Width * o.Height / (1920 * 1080)
		if o.FPS > 30 {
			o.Kbps = o.Kbps * 3 / 2
		}
	}
	if o.Delay == 0 {
		o.Delay = 200
	}
	if o.Codec == "" {
		o.Codec = "auto"
	}
}

// session is one cast to one Chromecast: portal, Cast control channel, the
// two pipelines and the Cast Streaming senders.
type session struct {
	h    *helper
	dev  device
	opts castOptions

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	phase     string
	since     time.Time
	volume    float64
	canVolume bool
	cast      *castConn
	transport string
	requestID int
}

func newSession(h *helper, d device, opts castOptions) *session {
	opts.defaults()
	ctx, cancel := context.WithCancel(context.Background())
	return &session{h: h, dev: d, opts: opts, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		phase: "starting", since: time.Now()}
}

func (s *session) deviceID() string { return s.dev.ID }

func (s *session) status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"kind": "mirror", "device": s.dev.ID, "name": s.dev.Name, "phase": s.phase,
		"since": s.since.Unix(), "volume": s.volume, "canVolume": s.canVolume}
}

func (s *session) setPhase(p string) {
	s.mu.Lock()
	s.phase = p
	if p == "casting" {
		s.since = time.Now()
	}
	s.mu.Unlock()
	s.h.emitState()
}

// Stop ends the cast; run tells the TV and cleans up.
func (s *session) Stop() { s.cancel() }

func (s *session) Wait(d time.Duration) {
	select {
	case <-s.done:
	case <-time.After(d):
	}
}

func (s *session) SetVolume(level float64) {
	s.mu.Lock()
	c, can := s.cast, s.canVolume
	s.requestID++
	id := s.requestID
	s.mu.Unlock()
	if c == nil || !can {
		return
	}
	c.send(platformID, nsReceiver, map[string]any{"type": "SET_VOLUME", "volume": map[string]any{"level": level}, "requestId": 1000 + id})
}

type cleanups []func()

func (c *cleanups) add(f func()) { *c = append(*c, f) }
func (c cleanups) run() {
	for i := len(c) - 1; i >= 0; i-- {
		c[i]()
	}
}

// run casts until stopped or until something breaks, and returns an error
// code for the plugin ("" when the user stopped it).
func (s *session) run() (code string, err error) {
	defer close(s.done)
	var undo cleanups
	defer func() { undo.run() }()
	stopped := func(c string, e error) (string, error) {
		if s.ctx.Err() != nil {
			return "", nil
		}
		return c, e
	}

	if err := need("mirroring"); err != nil {
		return "missingDependency", err
	}
	if s.opts.Audio == "tv" {
		// Without pw-metadata the cast sink can't become the default output,
		// so sound would go nowhere: send it from both instead, and say why.
		if err := need("tvOnlySound"); err != nil {
			var m *missingError
			errors.As(err, &m)
			s.h.emit(map[string]any{"event": "warning", "code": "soundOnBoth", "feature": m.feature, "missing": m.missing})
			s.opts.Audio = "both"
		}
	}
	codecs, err := s.videoCodecs()
	if err != nil {
		return "encoderMissing", err
	}

	// The portal picker comes first, so the TV prompt doesn't time out behind it.
	portal, token, err := requestScreencast(s.ctx, s.opts.RestoreToken)
	if err != nil {
		return stopped("portalDenied", err)
	}
	undo.add(portal.Close)
	if token != "" && token != s.opts.RestoreToken {
		s.h.emit(map[string]any{"event": "restoreToken", "token": token})
		s.h.mu.Lock()
		s.h.restoreToken = token
		s.h.mu.Unlock()
	}

	cast, err := dialCast(net.JoinHostPort(s.dev.Host, strconv.Itoa(s.dev.Port)))
	if err != nil {
		return stopped("deviceLost", err)
	}
	undo.add(cast.Close)
	s.mu.Lock()
	s.cast = cast
	s.mu.Unlock()
	if s.ctx.Err() != nil {
		return "", nil
	}

	transport, sessionID, code, err := s.launch(cast)
	if err != nil {
		return stopped(code, err)
	}
	undo.add(func() {
		cast.send(platformID, nsReceiver, map[string]any{"type": "STOP", "sessionId": sessionID, "requestId": 3})
		time.Sleep(200 * time.Millisecond)
	})

	audio, video, conn, err := s.negotiate(cast, transport, codecs)
	if err != nil {
		return stopped("launchRefused", err)
	}
	undo.add(func() { conn.Close() })

	streams := map[uint32]*stream{video.ssrc: video}
	if audio != nil {
		streams[audio.ssrc] = audio
	}
	go readRTCP(conn, streams)
	tick := time.NewTicker(50 * time.Millisecond)
	undo.add(tick.Stop)
	go func() {
		for i := 0; ; i++ {
			select {
			case now := <-tick.C:
				for _, st := range streams {
					st.tick(now, i%10 == 0)
				}
				if i%200 == 0 {
					for _, st := range streams {
						debugf("%s", st.stats())
					}
				}
			case <-s.ctx.Done():
				return
			}
		}
	}()

	vpipe, err := startGstPipeline(s.videoPipeline(portal, video.codec))
	if err != nil {
		return stopped("pipelineFailed", err)
	}
	undo.add(vpipe.Stop)
	video.keyframe = vpipe.KeyFrame
	video.bitrate = newBitrate(s.opts.Kbps, max(1000, s.opts.Kbps/4), vpipe.SetBitrate)
	videoEnded := make(chan error, 1)
	go func() {
		first := true
		err := readAccessUnits(vpipe, nalCodec{hevc: video.codec == "hevc"}, func(au []byte, key bool) {
			if first {
				if !key {
					return
				}
				first = false
				s.setPhase("casting")
			}
			if key && video.codec == "h264" {
				au = fixColour(au)
			}
			video.enqueue(au, key, video.rtpNow(time.Now()))
		})
		videoEnded <- err
	}()

	var apipe *gstPipeline
	audioFailed := make(chan string)
	if audio != nil {
		apipe, err = startGstPipeline(audioPipelineFor(s.opts.Audio))
		if err != nil {
			return stopped("pipelineFailed", err)
		}
		undo.add(apipe.Stop)
		if s.opts.Audio == "tv" {
			undo.add(holdDefault())
		}
		audioFailed = apipe.failed
		go func() {
			var rtp uint32
			for first := true; ; first = false {
				p, err := apipe.Pull()
				if err != nil {
					return
				}
				if first {
					rtp = audio.rtpNow(time.Now())
				}
				audio.enqueue(p, true, rtp)
				rtp += 480 // 10 ms at 48 kHz, matches frame-size
			}
		}()
	}

	stopMpris := startMirrorMpris(s.dev.Name, s.Stop)
	undo.add(stopMpris)

	ended := s.watchReceiver(cast, sessionID)
	select {
	case <-s.ctx.Done():
		return "", nil
	case reason := <-ended:
		return stopped(reason, errors.New("the receiver ended the session"))
	case msg := <-vpipe.failed:
		return stopped("pipelineFailed", errors.New(msg))
	case msg := <-audioFailed:
		return stopped("pipelineFailed", errors.New(msg))
	case err := <-videoEnded:
		return stopped("pipelineFailed", fmt.Errorf("video ended: %v", err))
	}
}

// videoCodecs lists the codecs to offer, in Chrome's order of preference,
// limited to the encoders this machine has.
func (s *session) videoCodecs() ([]string, error) {
	want := []string{"hevc", "h264"}
	if s.opts.Codec != "auto" {
		want = []string{s.opts.Codec}
	}
	var have []string
	for _, c := range want {
		if hasElement(encoderFor(c)) {
			have = append(have, c)
		}
	}
	if len(have) == 0 {
		return nil, fmt.Errorf("no VA-API encoder for %s", strings.Join(want, " or "))
	}
	return have, nil
}

func encoderFor(codec string) string {
	if codec == "hevc" {
		return "vah265enc"
	}
	return "vah264enc"
}

// launch starts the mirroring receiver app.
func (s *session) launch(cast *castConn) (transport, sessionID, code string, err error) {
	transport, sessionID, st, code, err := launchApp(s.ctx, cast, mirroringAppID, func() { s.setPhase("authorizing") })
	if err != nil {
		return "", "", code, err
	}
	s.updateVolume(st)
	s.setPhase("starting")
	return transport, sessionID, "", nil
}

// launchApp starts a receiver app and connects to it, waiting while the TV
// asks the viewer to allow the cast.
func launchApp(ctx context.Context, cast *castConn, appID string, onPending func()) (transport, sessionID string, st receiverStatus, code string, err error) {
	cast.send(platformID, nsConnection, map[string]string{"type": "CONNECT"})
	cast.send(platformID, nsReceiver, map[string]any{"type": "LAUNCH", "appId": appID, "requestId": 1})
	pending := false
	raw, err := cast.await(ctx, nsReceiver, 90*time.Second, func(p []byte) bool {
		if !pending && strings.Contains(string(p), "USER_PENDING_AUTHORIZATION") {
			pending = true
			onPending()
		}
		t := typeOf(p)
		return t == "LAUNCH_ERROR" || (t == "RECEIVER_STATUS" && strings.Contains(string(p), appID) &&
			strings.Contains(string(p), "transportId"))
	})
	if err != nil {
		if strings.Contains(err.Error(), "timed out") {
			return "", "", st, "authorizationTimeout", err
		}
		return "", "", st, "deviceLost", err
	}
	if typeOf(raw) == "LAUNCH_ERROR" {
		return "", "", st, "launchRefused", fmt.Errorf("%s", raw)
	}
	st = parseReceiverStatus(raw)
	for _, a := range st.Status.Applications {
		if a.AppID == appID {
			cast.send(a.TransportID, nsConnection, map[string]string{"type": "CONNECT"})
			return a.TransportID, a.SessionID, st, "", nil
		}
	}
	return "", "", st, "launchRefused", fmt.Errorf("app %s missing from status: %s", appID, raw)
}

type receiverStatus struct {
	Status struct {
		Applications []struct {
			AppID       string `json:"appId"`
			TransportID string `json:"transportId"`
			SessionID   string `json:"sessionId"`
		}
		Volume *struct {
			Level       float64 `json:"level"`
			ControlType string  `json:"controlType"`
		}
	}
}

func parseReceiverStatus(raw []byte) receiverStatus {
	var st receiverStatus
	json.Unmarshal(raw, &st)
	return st
}

func (s *session) updateVolume(st receiverStatus) {
	v := st.Status.Volume
	if v == nil {
		return
	}
	s.mu.Lock()
	changed := s.volume != v.Level || s.canVolume != (v.ControlType != "fixed")
	s.volume, s.canVolume = v.Level, v.ControlType != "fixed"
	s.mu.Unlock()
	if changed {
		s.h.emitState()
	}
}

// watchReceiver follows the control channel after setup: volume changes, and
// the TV closing our app or the connection.
func (s *session) watchReceiver(cast *castConn, sessionID string) <-chan string {
	ended := make(chan string, 1)
	go func() {
		for m := range cast.msgs {
			switch {
			case m.ns == nsReceiver && typeOf(m.payload) == "RECEIVER_STATUS":
				st := parseReceiverStatus(m.payload)
				s.updateVolume(st)
				running := false
				for _, a := range st.Status.Applications {
					running = running || a.SessionID == sessionID
				}
				if !running {
					ended <- "receiverStopped"
					return
				}
			case m.ns == nsConnection && typeOf(m.payload) == "CLOSE":
				ended <- "receiverStopped"
				return
			}
		}
		ended <- "deviceLost"
	}()
	return ended
}

type keys struct{ key, iv [16]byte }

func newKeys() (k keys) {
	rand.Read(k.key[:])
	rand.Read(k.iv[:])
	return
}

// negotiate asks for the receiver's capabilities, then offers Opus audio and
// one video stream per codec. The receiver's ANSWER picks the streams.
func (s *session) negotiate(cast *castConn, transport string, codecs []string) (audio, video *stream, conn *net.UDPConn, err error) {
	const audioPT, videoPT = 96, 101
	o := s.opts
	cast.send(transport, nsWebrtc, map[string]any{"type": "GET_CAPABILITIES", "seqNum": 1})
	if caps, err := cast.await(s.ctx, nsWebrtc, 5*time.Second, func(p []byte) bool { return typeOf(p) == "CAPABILITIES_RESPONSE" }); err == nil {
		debugf("receiver capabilities: %s", caps)
	}

	type offered struct {
		index int
		codec string
		ssrc  uint32
		keys  keys
	}
	audioOffer := offered{index: 0, codec: "opus", ssrc: randSSRC(), keys: newKeys()}
	var streams []any
	if o.Audio != "local" { // "local" keeps the sound here and sends picture only
		streams = append(streams, map[string]any{
			"index": 0, "type": "audio_source", "codecName": "opus", "channels": 2,
			"rtpProfile": "cast", "rtpPayloadType": audioPT, "ssrc": audioOffer.ssrc,
			"targetDelay": o.Delay, "aesKey": hex.EncodeToString(audioOffer.keys.key[:]),
			"aesIvMask": hex.EncodeToString(audioOffer.keys.iv[:]), "receiverRtcpEventLog": false,
			"timeBase": "1/48000", "codecParameter": "", "bitRate": 128000,
		})
	}
	var videoOffers []offered
	for i, c := range codecs {
		v := offered{index: 1 + i, codec: c, ssrc: randSSRC(), keys: newKeys()}
		videoOffers = append(videoOffers, v)
		streams = append(streams, map[string]any{
			"index": v.index, "type": "video_source", "codecName": c, "channels": 1,
			"rtpProfile": "cast", "rtpPayloadType": videoPT, "ssrc": v.ssrc,
			"targetDelay": o.Delay, "aesKey": hex.EncodeToString(v.keys.key[:]),
			"aesIvMask": hex.EncodeToString(v.keys.iv[:]), "receiverRtcpEventLog": false,
			"timeBase": "1/90000", "codecParameter": codecParameter(c, o.Width, o.Height, o.FPS),
			"maxFrameRate": fmt.Sprint(o.FPS), "maxBitRate": o.Kbps * 1000, "protection": "",
			"profile": "", "level": "", "errorRecoveryMode": "",
			"resolutions": []any{map[string]int{"width": o.Width, "height": o.Height}},
		})
	}
	if err := cast.send(transport, nsWebrtc, map[string]any{
		"type": "OFFER", "seqNum": 2,
		"offer": map[string]any{"castMode": "mirroring", "supportedStreams": streams},
	}); err != nil {
		return nil, nil, nil, err
	}
	raw, err := cast.await(s.ctx, nsWebrtc, 10*time.Second, func(p []byte) bool { return typeOf(p) == "ANSWER" })
	if err != nil {
		return nil, nil, nil, err
	}
	var answer struct {
		Result string
		Answer struct {
			UDPPort     int   `json:"udpPort"`
			SendIndexes []int `json:"sendIndexes"`
		}
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Result != "ok" {
		return nil, nil, nil, fmt.Errorf("receiver rejected the offer: %s", raw)
	}

	conn, err = net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(s.dev.Host), Port: answer.Answer.UDPPort})
	if err != nil {
		return nil, nil, nil, err
	}
	start := time.Now()
	for _, idx := range answer.Answer.SendIndexes {
		if idx == 0 {
			audio = newStream("audio", conn, audioOffer.ssrc, audioPT, 48000, audioOffer.keys.key, audioOffer.keys.iv, start, false)
			continue
		}
		for _, v := range videoOffers {
			if v.index == idx && video == nil {
				video = newStream("video", conn, v.ssrc, videoPT, 90000, v.keys.key, v.keys.iv, start, true)
				video.codec = v.codec
			}
		}
	}
	if video == nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("receiver accepted no video stream: %s", raw)
	}
	log.Printf("receiver chose %s, audio: %v", video.codec, audio != nil)
	return audio, video, conn, nil
}

func (s *session) videoPipeline(portal *portalSession, codec string) string {
	o := s.opts
	enc, parse, caps := encoderFor(codec), "h264parse", "video/x-h264"
	if codec == "hevc" {
		parse, caps = "h265parse", "video/x-h265"
	}
	raw := fmt.Sprintf("video/x-raw(memory:VAMemory),format=NV12,width=%d,height=%d,pixel-aspect-ratio=1/1", o.Width, o.Height)
	// The portal's buffer pool is small: always copy into a fresh VA surface so
	// the encoder's reference frames never pin a portal buffer.
	return fmt.Sprintf("pipewiresrc fd=%d path=%d do-timestamp=true keepalive-time=%d ! "+
		"video/x-raw(ANY),pixel-aspect-ratio=1/1 ! vapostproc disable-passthrough=true add-borders=true ! "+
		"%s ! videorate skip-to-first=true ! %s,framerate=%d/1 ! "+
		"queue max-size-buffers=1 max-size-bytes=0 max-size-time=0 leaky=downstream ! "+
		"%s name=enc aud=true b-frames=0 key-int-max=%d rate-control=cbr bitrate=%d target-usage=4 ! %s,profile=main ! "+
		"%s config-interval=-1 ! %s,stream-format=byte-stream,alignment=au ! appsink name=out sync=false max-buffers=4",
		portal.fd.Fd(), portal.node, 1+999/o.FPS, raw, raw, o.FPS, enc, 2*o.FPS, o.Kbps, caps, parse, caps)
}

// codecParameter describes a stream the way Chrome's mirroring does: Main
// profile, at the level the resolution and frame rate need.
func codecParameter(codec string, width, height, fps int) string {
	big, fast := width*height > 1920*1080, fps > 30
	switch codec {
	case "hevc":
		level := 120 // 4.0: 1080p30
		switch {
		case big && fast:
			level = 153 // 5.1: 4K60
		case big:
			level = 150 // 5.0: 4K30
		case fast:
			level = 123 // 4.1: 1080p60
		}
		return fmt.Sprintf("hev1.1.6.L%d.B0", level)
	case "h264":
		level := 0x28 // 4.0
		switch {
		case big && fast:
			level = 0x34 // 5.2
		case big:
			level = 0x33 // 5.1
		case fast:
			level = 0x2a // 4.2
		}
		return fmt.Sprintf("avc1.4D40%02X", level)
	}
	return ""
}

func typeOf(p []byte) string {
	var m struct{ Type string }
	json.Unmarshal(p, &m)
	return m.Type
}

func randSSRC() uint32 {
	var b [4]byte
	rand.Read(b[:])
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
