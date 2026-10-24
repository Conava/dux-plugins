package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	avahiName      = "org.freedesktop.Avahi"
	avahiServer    = "org.freedesktop.Avahi.Server2"
	avahiBrowserIf = "org.freedesktop.Avahi.ServiceBrowser"

	ifaceUnspec = int32(-1)
	protoInet   = int32(0) // IPv4 only: some Cast firmware refuses the control connection over IPv6
)

var serviceTypes = map[string]string{
	"_googlecast._tcp": "chromecast",
	"_airplay._tcp":    "airplay",
}

// ErrAvahiUnavailable maps to "avahi" in the helper's check result.
var ErrAvahiUnavailable = errors.New("avahi not available")

type Device struct {
	ID       string // "<protocol>:<txt id>", so a TV doing both shows twice
	Name     string
	Model    string
	Protocol string
	Host     string
	Addr     string
	Port     uint16
	Features string // AirPlay feature bits, raw
}

type DeviceEvent struct {
	Added  bool
	Device Device
}

// parseTXT turns a resolved service into a Device. name is the mDNS instance name.
func parseTXT(protocol, name string, txt [][]byte) Device {
	kv := map[string]string{}
	for _, rec := range txt {
		k, v, _ := strings.Cut(string(rec), "=")
		kv[strings.ToLower(k)] = v
	}
	d := Device{Protocol: protocol, Name: name}
	id := ""
	switch protocol {
	case "chromecast":
		id, d.Model = kv["id"], kv["md"]
		if kv["fn"] != "" {
			d.Name = kv["fn"]
		}
	case "airplay":
		id, d.Model, d.Features = strings.ToLower(kv["deviceid"]), kv["model"], kv["features"]
	}
	if id == "" {
		id = name
	}
	d.ID = protocol + ":" + id
	return d
}

// instance is one mDNS service as avahi reports it; the same device shows once per interface.
type instance struct {
	iface               int32
	name, stype, domain string
}

// registry dedupes instances into devices. Not goroutine-safe: Discovery's loop owns it.
type registry struct {
	inst map[instance]string // instance -> device id ("" while resolving)
	devs map[string]Device
	refs map[string]int
}

func newRegistry() *registry {
	return &registry{inst: map[instance]string{}, devs: map[string]Device{}, refs: map[string]int{}}
}

func (r *registry) pending(in instance) { r.inst[in] = "" }

// resolved returns true when this is the device's first instance.
func (r *registry) resolved(in instance, d Device) bool {
	if id, ok := r.inst[in]; !ok || id != "" {
		return false // removed while resolving, or already counted
	}
	r.inst[in] = d.ID
	r.refs[d.ID]++
	if r.refs[d.ID] > 1 {
		return false
	}
	r.devs[d.ID] = d // first interface's address wins
	return true
}

// removed returns the device when its last instance is gone.
func (r *registry) removed(in instance) (Device, bool) {
	id, ok := r.inst[in]
	delete(r.inst, in)
	if !ok || id == "" {
		return Device{}, false
	}
	if r.refs[id]--; r.refs[id] > 0 {
		return Device{}, false
	}
	d := r.devs[id]
	delete(r.devs, id)
	delete(r.refs, id)
	return d, true
}

func (r *registry) clear() []Device {
	var gone []Device
	for _, d := range r.devs {
		gone = append(gone, d)
	}
	*r = *newRegistry()
	return gone
}

type Discovery struct {
	Events chan DeviceEvent
	Errors chan error

	conn     *dbus.Conn
	sig      chan *dbus.Signal
	resolves chan resolveResult
	browsers map[dbus.ObjectPath]string // path -> protocol
	reg      *registry
	done     chan struct{}
}

// Stop ends discovery; nothing blocks on Events or Errors after it.
func (d *Discovery) Stop() { close(d.done) }

func (d *Discovery) event(e DeviceEvent) {
	select {
	case d.Events <- e:
	case <-d.done:
	}
}

func (d *Discovery) fail(err error) {
	select {
	case d.Errors <- err:
	case <-d.done:
	}
}

type resolveResult struct {
	in  instance
	dev Device
	err error
}

// StartDiscovery browses Cast and AirPlay receivers until the connection closes.
// Returns ErrAvahiUnavailable when avahi isn't running and can't be activated.
func StartDiscovery(conn *dbus.Conn) (*Discovery, error) {
	d := &Discovery{
		Events:   make(chan DeviceEvent, 16),
		Errors:   make(chan error, 4),
		conn:     conn,
		sig:      make(chan *dbus.Signal, 64),
		resolves: make(chan resolveResult, 16),
		reg:      newRegistry(),
		done:     make(chan struct{}),
	}
	conn.Signal(d.sig)
	for _, m := range [][]dbus.MatchOption{
		{dbus.WithMatchSender(avahiName), dbus.WithMatchInterface(avahiBrowserIf)},
		{dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, avahiName)},
	} {
		if err := conn.AddMatchSignal(m...); err != nil {
			return nil, err
		}
	}
	if err := d.browse(); err != nil {
		return nil, err
	}
	go d.loop()
	return d, nil
}

// browse uses Prepare + Start (avahi >= 0.8) so no ItemNew can fire before we know the browser's path.
func (d *Discovery) browse() error {
	d.browsers = map[dbus.ObjectPath]string{}
	srv := d.conn.Object(avahiName, "/")
	for stype, protocol := range serviceTypes {
		var path dbus.ObjectPath
		err := srv.Call(avahiServer+".ServiceBrowserPrepare", 0, ifaceUnspec, protoInet, stype, "", uint32(0)).Store(&path)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrAvahiUnavailable, err)
		}
		d.browsers[path] = protocol
		if err := d.conn.Object(avahiName, path).Call(avahiBrowserIf+".Start", 0).Err; err != nil {
			return fmt.Errorf("%w: %v", ErrAvahiUnavailable, err)
		}
	}
	return nil
}

func (d *Discovery) loop() {
	for {
		select {
		case <-d.done:
			return
		case s, ok := <-d.sig:
			if !ok {
				return
			}
			d.handle(s)
		case r := <-d.resolves:
			if r.err != nil {
				d.reg.removed(r.in) // device advertises but doesn't answer; drop it until it re-announces
				continue
			}
			if d.reg.resolved(r.in, r.dev) {
				d.event(DeviceEvent{Added: true, Device: r.dev})
			}
		}
	}
}

func (d *Discovery) handle(s *dbus.Signal) {
	if s.Name == "org.freedesktop.DBus.NameOwnerChanged" {
		d.ownerChanged(s.Body[2].(string))
		return
	}
	protocol, ours := d.browsers[s.Path]
	if !ours {
		return // a stale browser from before an avahi restart, or someone else's
	}
	switch s.Name {
	case avahiBrowserIf + ".ItemNew", avahiBrowserIf + ".ItemRemove":
		in := instance{iface: s.Body[0].(int32), name: s.Body[2].(string), stype: s.Body[3].(string), domain: s.Body[4].(string)}
		if s.Name == avahiBrowserIf+".ItemRemove" {
			if dev, gone := d.reg.removed(in); gone {
				d.event(DeviceEvent{Added: false, Device: dev})
			}
			return
		}
		d.reg.pending(in)
		go d.resolve(in, protocol)
	case avahiBrowserIf + ".Failure":
		d.fail(fmt.Errorf("browser failed: %v", s.Body))
	}
}

// ownerChanged handles avahi stopping (all devices gone) and coming back (browse again).
func (d *Discovery) ownerChanged(newOwner string) {
	for _, dev := range d.reg.clear() {
		d.event(DeviceEvent{Added: false, Device: dev})
	}
	d.browsers = nil
	if newOwner == "" {
		d.fail(ErrAvahiUnavailable)
		return
	}
	if err := d.browse(); err != nil {
		d.fail(err)
	}
}

func (d *Discovery) resolve(in instance, protocol string) {
	var (
		iface, proto, aproto            int32
		name, stype, domain, host, addr string
		port                            uint16
		txt                             [][]byte
		flags                           uint32
	)
	err := d.conn.Object(avahiName, "/").Call(avahiServer+".ResolveService", 0,
		in.iface, protoInet, in.name, in.stype, in.domain, protoInet, uint32(0)).
		Store(&iface, &proto, &name, &stype, &domain, &host, &aproto, &addr, &port, &txt, &flags)
	dev := parseTXT(protocol, in.name, txt)
	dev.Host, dev.Addr, dev.Port = host, addr, port
	select {
	case d.resolves <- resolveResult{in: in, dev: dev, err: err}:
	case <-d.done:
	}
}
