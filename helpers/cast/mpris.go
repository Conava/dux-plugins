package main

import (
	"errors"
	"log"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	mprisName   = "org.mpris.MediaPlayer2.cast"
	mprisPath   = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	mprisRoot   = "org.mpris.MediaPlayer2"
	mprisPlayer = "org.mpris.MediaPlayer2.Player"
)

// playerControls are what the MPRIS buttons do. A nil control is shown as
// unavailable; mirroring has only stop, so the shell's pause button ends it.
type playerControls struct {
	stop, playPause, next, previous func()
	seek                            func(seconds float64)
}

// Player is the MPRIS face of a running cast, shown by the island and the
// media widgets.
type Player struct {
	props *prop.Properties
	c     playerControls
}

type mprisRootMethods struct{}

func (mprisRootMethods) Raise() *dbus.Error { return nil }
func (mprisRootMethods) Quit() *dbus.Error  { return nil }

type mprisPlayerMethods struct{ p *Player }

func (m mprisPlayerMethods) Stop() *dbus.Error { m.p.c.stop(); return nil }
func (m mprisPlayerMethods) Pause() *dbus.Error {
	m.p.playPauseOr(m.p.c.stop, "Playing")
	return nil
}
func (m mprisPlayerMethods) PlayPause() *dbus.Error { m.p.playPauseOr(m.p.c.stop, ""); return nil }
func (m mprisPlayerMethods) Play() *dbus.Error      { m.p.playPauseOr(func() {}, "Paused"); return nil }
func (m mprisPlayerMethods) Next() *dbus.Error      { call(m.p.c.next); return nil }
func (m mprisPlayerMethods) Previous() *dbus.Error  { call(m.p.c.previous); return nil }

// MPRIS Seek, not io.Seeker (go vet -stdmethods=false).
func (m mprisPlayerMethods) SetPosition(_ dbus.ObjectPath, us int64) *dbus.Error {
	if m.p.c.seek != nil {
		m.p.c.seek(float64(us) / 1e6)
	}
	return nil
}
func (mprisPlayerMethods) OpenUri(string) *dbus.Error { return nil }

func call(f func()) {
	if f != nil {
		f()
	}
}

// playPauseOr toggles when the player can, only from the given status when
// one is named, and otherwise runs fallback.
func (p *Player) playPauseOr(fallback func(), only string) {
	if p.c.playPause == nil {
		fallback()
		return
	}
	if only != "" {
		if v, err := p.props.Get(mprisPlayer, "PlaybackStatus"); err != nil || v.Value() != only {
			return
		}
	}
	p.c.playPause()
}

// metadata has no xesam:artist on purpose: the shell then shows the
// identity as the subtitle and skips its lyrics and artwork lookups.
func metadata(title string, length float64) map[string]dbus.Variant {
	md := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/cast/item")),
		"xesam:title":   dbus.MakeVariant(title),
	}
	if length > 0 {
		md["mpris:length"] = dbus.MakeVariant(int64(length * 1e6))
	}
	return md
}

// StartPlayer exports the player and claims the bus name.
func StartPlayer(conn *dbus.Conn, title string, c playerControls) (*Player, error) {
	p := &Player{c: c}
	ro := func(v any) *prop.Prop { return &prop.Prop{Value: v, Emit: prop.EmitTrue} }
	props, err := prop.Export(conn, mprisPath, prop.Map{
		mprisRoot: {
			"Identity":            ro("Cast"),
			"CanQuit":             ro(false),
			"CanRaise":            ro(false),
			"HasTrackList":        ro(false),
			"SupportedUriSchemes": ro([]string{}),
			"SupportedMimeTypes":  ro([]string{}),
		},
		mprisPlayer: {
			"PlaybackStatus": ro("Playing"),
			"Metadata":       ro(metadata(title, 0)),
			"Position":       &prop.Prop{Value: int64(0), Emit: prop.EmitFalse}, // clients poll it
			"Rate":           ro(1.0),
			"MinimumRate":    ro(1.0),
			"MaximumRate":    ro(1.0),
			"CanGoNext":      ro(c.next != nil),
			"CanGoPrevious":  ro(c.previous != nil),
			"CanPlay":        ro(true), // playerctl skips players with CanPlay false for metadata
			"CanPause":       ro(c.playPause != nil),
			"CanSeek":        ro(false),
			"CanControl":     ro(true),
		},
	})
	if err != nil {
		return nil, err
	}
	p.props = props
	if err := conn.Export(mprisRootMethods{}, mprisPath, mprisRoot); err != nil {
		return nil, err
	}
	if err := conn.Export(mprisPlayerMethods{p}, mprisPath, mprisPlayer); err != nil {
		return nil, err
	}
	node := &introspect.Node{Name: string(mprisPath), Interfaces: []introspect.Interface{
		introspect.IntrospectData, prop.IntrospectData,
		{Name: mprisRoot, Methods: introspect.Methods(mprisRootMethods{}), Properties: props.Introspection(mprisRoot)},
		{Name: mprisPlayer, Methods: introspect.Methods(mprisPlayerMethods{}), Properties: props.Introspection(mprisPlayer)},
	}}
	if err := conn.Export(introspect.NewIntrospectable(node), mprisPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return nil, err
	}
	// DoNotQueue: a second helper must fail loudly rather than wait for the name.
	reply, err := conn.RequestName(mprisName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, errors.New("another cast player owns " + mprisName)
	}
	return p, nil
}

// set changes a property only when it differs, so clients see few signals.
func (p *Player) set(name string, v any) {
	if cur, err := p.props.Get(mprisPlayer, name); err == nil && equalVariant(cur.Value(), v) {
		return
	}
	p.props.SetMust(mprisPlayer, name, v)
}

func equalVariant(a, b any) bool {
	am, aok := a.(map[string]dbus.Variant)
	bm, bok := b.(map[string]dbus.Variant)
	if aok && bok {
		if len(am) != len(bm) {
			return false
		}
		for k, v := range am {
			if bv, ok := bm[k]; !ok || bv.String() != v.String() {
				return false
			}
		}
		return true
	}
	return a == b
}

// startMirrorMpris shows a mirroring cast until the returned function runs.
// Failing to register only costs that display.
func startMirrorMpris(device string, stop func()) func() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Printf("mpris: %v", err)
		return func() {}
	}
	if _, err := StartPlayer(conn, "Casting to "+device, playerControls{stop: stop}); err != nil {
		log.Printf("mpris: %v", err)
	}
	return func() { conn.Close() }
}

// startMpris shows file playback with real controls.
func (m *mediaSession) startMpris() func() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Printf("mpris: %v", err)
		return func() {}
	}
	c := playerControls{
		stop: m.Stop,
		playPause: func() {
			m.mu.Lock()
			paused := m.paused || m.st.PlayerState == "PAUSED"
			m.mu.Unlock()
			if paused {
				m.control(command{Cmd: "resume"})
			} else {
				m.control(command{Cmd: "pause"})
			}
		},
		seek: func(s float64) { m.control(command{Cmd: "seek", Position: s}) },
	}
	if len(m.items) > 1 {
		c.next = func() { m.control(command{Cmd: "next"}) }
		c.previous = func() { m.control(command{Cmd: "previous"}) }
	}
	p, err := StartPlayer(conn, m.items[m.index].title, c)
	if err != nil {
		log.Printf("mpris: %v", err)
	}
	m.mu.Lock()
	m.player = p
	m.mu.Unlock()
	return func() { conn.Close() }
}

// update copies the session's state to the MPRIS properties.
func (p *Player) update(m *mediaSession) {
	if p == nil {
		return
	}
	st := m.status()
	status := "Playing"
	if st["playerState"] == "PAUSED" || st["interval"] == 0 && st["playerState"] != "PLAYING" && len(m.items) > 1 {
		status = "Paused"
	}
	p.set("PlaybackStatus", status)
	p.set("Metadata", metadata(st["title"].(string), st["duration"].(float64)))
	p.set("CanSeek", st["duration"].(float64) > 0)
	p.props.SetMust(mprisPlayer, "Position", int64(st["position"].(float64)*1e6))
}
