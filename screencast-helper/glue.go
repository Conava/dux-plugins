package main

import (
	"context"
	"log"
	"os"
	"slices"

	"github.com/godbus/dbus/v5"
)

// discover reports the full device list on every change until ctx ends.
func discover(ctx context.Context, onChange func([]device)) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	d, err := StartDiscovery(conn)
	if err != nil {
		return err
	}
	found := map[string]Device{}
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-d.Errors:
			log.Printf("discovery: %v", err)
		case e := <-d.Events:
			if e.Added {
				found[e.Device.ID] = e.Device
			} else {
				delete(found, e.Device.ID)
			}
			onChange(mergeDevices(found))
		}
	}
}

// mergeDevices joins a device's Chromecast and AirPlay entries, which avahi
// reports separately, by address.
func mergeDevices(found map[string]Device) []device {
	byAddr := map[string]*device{}
	var order []string
	for _, f := range found {
		d := byAddr[f.Addr]
		if d == nil {
			d = &device{Host: f.Addr}
			byAddr[f.Addr] = d
			order = append(order, f.Addr)
		}
		d.Protocols = append(d.Protocols, f.Protocol)
		switch f.Protocol {
		case "chromecast":
			d.ID, d.Name, d.Model, d.Protocol, d.Port = f.ID, f.Name, f.Model, "chromecast", int(f.Port)
		case "airplay":
			d.AirplayPort = int(f.Port)
			if d.Protocol == "" {
				d.ID, d.Name, d.Model, d.Protocol = f.ID, f.Name, f.Model, "airplay"
			}
		}
	}
	list := make([]device, 0, len(order))
	for _, a := range order {
		d := byAddr[a]
		slices.Sort(d.Protocols)
		list = append(list, *d)
	}
	return list
}

// debug turns on per-message and per-request logging, for troubleshooting:
// SCREENCAST_DEBUG=1 in the shell's environment.
var debug = os.Getenv("SCREENCAST_DEBUG") != ""

func debugf(format string, args ...any) {
	if debug {
		log.Printf(format, args...)
	}
}
