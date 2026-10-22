package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
)

const (
	portalBus  = "org.freedesktop.portal.Desktop"
	portalPath = "/org/freedesktop/portal/desktop"
	scIface    = "org.freedesktop.portal.ScreenCast"
	reqIface   = "org.freedesktop.portal.Request"
)

var tokenSeq atomic.Uint64

type portalSession struct {
	conn    *dbus.Conn
	session dbus.ObjectPath
	node    uint32
	fd      *os.File
}

func (p *portalSession) Close() {
	if p.fd != nil {
		p.fd.Close()
	}
	if p.session != "" {
		p.conn.Object(portalBus, p.session).Call("org.freedesktop.portal.Session.Close", 0)
	}
	p.conn.Close()
}

// requestScreencast opens a ScreenCast portal session for one monitor with an
// embedded cursor and returns it with the token that skips the share dialog
// next time. Shows the dialog unless restoreToken is still valid.
func requestScreencast(ctx context.Context, restoreToken string) (p *portalSession, newToken string, err error) {
	p, newToken, err = openScreencast(ctx, restoreToken)
	return
}

func openScreencast(ctx context.Context, restoreToken string) (*portalSession, string, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, "", err
	}
	p := &portalSession{conn: conn}
	sender := strings.ReplaceAll(strings.TrimPrefix(conn.Names()[0], ":"), ".", "_")
	portal := conn.Object(portalBus, portalPath)

	res, err := portalRequest(ctx, conn, sender, portal, scIface+".CreateSession", map[string]dbus.Variant{
		"handle_token":         dbus.MakeVariant(token("req")),
		"session_handle_token": dbus.MakeVariant(token("ses")),
	})
	if err != nil {
		p.Close()
		return nil, "", fmt.Errorf("CreateSession: %w", err)
	}
	handle, _ := res["session_handle"].Value().(string)
	if handle == "" {
		p.Close()
		return nil, "", errors.New("CreateSession: no session_handle")
	}
	p.session = dbus.ObjectPath(handle)

	opts := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token("req")),
		"types":        dbus.MakeVariant(uint32(1)), // monitor
		"multiple":     dbus.MakeVariant(false),
		"cursor_mode":  dbus.MakeVariant(uint32(2)), // embedded
		"persist_mode": dbus.MakeVariant(uint32(2)), // until revoked
	}
	if restoreToken != "" {
		opts["restore_token"] = dbus.MakeVariant(restoreToken)
	}
	if _, err := portalRequest(ctx, conn, sender, portal, scIface+".SelectSources", p.session, opts); err != nil {
		p.Close()
		return nil, "", fmt.Errorf("SelectSources: %w", err)
	}

	res, err = portalRequest(ctx, conn, sender, portal, scIface+".Start", p.session, "", map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token("req")),
	})
	if err != nil {
		p.Close()
		return nil, "", fmt.Errorf("Start: %w", err)
	}
	newToken, _ := res["restore_token"].Value().(string)
	// streams: a(ua{sv})
	streams, ok := res["streams"].Value().([][]any)
	if !ok || len(streams) == 0 || len(streams[0]) == 0 {
		p.Close()
		return nil, "", errors.New("Start: no streams")
	}
	if p.node, ok = streams[0][0].(uint32); !ok {
		p.Close()
		return nil, "", errors.New("Start: bad stream node")
	}

	var fd dbus.UnixFD
	if err := portal.CallWithContext(ctx, scIface+".OpenPipeWireRemote", 0, p.session,
		map[string]dbus.Variant{}).Store(&fd); err != nil {
		p.Close()
		return nil, "", fmt.Errorf("OpenPipeWireRemote: %w", err)
	}
	p.fd = os.NewFile(uintptr(fd), "pipewire-remote")
	return p, newToken, nil
}

func token(prefix string) string {
	return fmt.Sprintf("cast_%s_%d", prefix, tokenSeq.Add(1))
}

// portalRequest calls a portal method returning a Request and waits for its
// Response. The options map, carrying handle_token, must be the last argument.
func portalRequest(ctx context.Context, conn *dbus.Conn, sender string, portal dbus.BusObject, method string, args ...any) (map[string]dbus.Variant, error) {
	opts := args[len(args)-1].(map[string]dbus.Variant)
	tok := opts["handle_token"].Value().(string)
	path := dbus.ObjectPath(fmt.Sprintf("%s/request/%s/%s", portalPath, sender, tok))
	match := []dbus.MatchOption{dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(reqIface), dbus.WithMatchMember("Response")}
	// Subscribe before calling so a fast Response isn't missed.
	if err := conn.AddMatchSignal(match...); err != nil {
		return nil, err
	}
	defer conn.RemoveMatchSignal(match...)
	ch := make(chan *dbus.Signal, 4)
	conn.Signal(ch)
	defer conn.RemoveSignal(ch)

	if call := portal.CallWithContext(ctx, method, 0, args...); call.Err != nil {
		return nil, call.Err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case sig := <-ch:
			if sig.Path != path || len(sig.Body) < 2 {
				continue
			}
			if code, _ := sig.Body[0].(uint32); code != 0 {
				return nil, fmt.Errorf("denied or cancelled (response %d)", code)
			}
			results, _ := sig.Body[1].(map[string]dbus.Variant)
			return results, nil
		}
	}
}
