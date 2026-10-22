package main

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/godbus/dbus/v5"
)

// Nothing is needed to start the helper. Each feature checks the packages it
// uses when it's used and reports what's missing, so a system without, say,
// gst-libav still mirrors and only misses converting DTS audio.
//
// Features: discovery, mirroring, sound (sending sound), tvOnlySound, files
// (probing and serving), mkv, convert, photos (shrinking), heic, thumbnails.

type dependency struct {
	ID       string // the package's name on Arch, used as a stable id
	Required bool   // needed for discovery and mirroring, the core of the plugin
	Features []string
	present  func() bool
	Packages map[string]string // distro family → package; the ID is Arch's name and the upstream one
	// Hints name an extra step a distro needs beyond its package, such as a
	// third-party repository; the plugin turns the code into text.
	Hints  map[string]string
	Manual bool // installed by hand, never part of the install command
}

func elements(names ...string) func() bool {
	return func() bool {
		for _, n := range names {
			if !hasElement(n) {
				return false
			}
		}
		return true
	}
}

func onPath(name string) func() bool {
	return func() bool {
		_, err := exec.LookPath(name)
		return err == nil
	}
}

// Package names checked against the Arch, Debian trixie / Ubuntu noble,
// Fedora 43 and openSUSE Tumbleweed indexes (2026-10).
var dependencies = []dependency{
	{ID: "avahi", Required: true, Features: []string{"discovery"}, present: avahiRunning,
		Packages: map[string]string{"debian": "avahi-daemon", "fedora": "avahi", "opensuse": "avahi"}},
	{ID: "gstreamer", Required: true, Features: []string{"mirroring", "sound", "files", "mkv", "convert", "photos", "thumbnails"},
		present:  func() bool { return loadGst() == nil },
		Packages: map[string]string{"debian": "libgstreamer1.0-0", "fedora": "gstreamer1", "opensuse": "gstreamer"}},
	{ID: "gst-plugins-base-libs", Required: true, Features: []string{"mirroring", "files", "convert", "photos", "thumbnails"},
		present: func() bool {
			return loadDiscoverer() == nil && elements("videorate", "videoconvert", "videoscale", "audioconvert", "audioresample", "appsink", "decodebin")()
		},
		Packages: map[string]string{"debian": "gstreamer1.0-plugins-base", "fedora": "gstreamer1-plugins-base", "opensuse": "gstreamer-plugins-base"}},
	{ID: "gst-plugins-base", Required: true, Features: []string{"sound"}, present: elements("opusenc"),
		Packages: map[string]string{"debian": "gstreamer1.0-plugins-base", "fedora": "gstreamer1-plugins-base", "opensuse": "gstreamer-plugins-base"}},
	{ID: "gst-plugin-pipewire", Required: true, Features: []string{"mirroring", "sound"}, present: elements("pipewiresrc"),
		Packages: map[string]string{"debian": "gstreamer1.0-pipewire", "fedora": "pipewire-gstreamer", "opensuse": "gstreamer-plugin-pipewire"}},
	{ID: "gst-plugins-bad-libs", Required: true, Features: []string{"mirroring", "mkv"},
		present:  elements("h264parse", "h265parse", "h264timestamper", "h265timestamper"),
		Packages: map[string]string{"debian": "gstreamer1.0-plugins-bad", "fedora": "gstreamer1-plugins-bad-free", "opensuse": "gstreamer-plugins-bad"}},
	// The va elements only register when the VA-API driver can encode, so a
	// missing one can also mean a missing driver, or one built without
	// H.264/HEVC for patent reasons (Fedora's and openSUSE's Mesa).
	{ID: "gst-plugin-va", Required: true, Features: []string{"mirroring", "convert"},
		present:  func() bool { return hasElement("vapostproc") && (hasElement("vah264enc") || hasElement("vah265enc")) },
		Packages: map[string]string{"debian": "gstreamer1.0-plugins-bad", "fedora": "gstreamer1-plugins-bad-free", "opensuse": "gstreamer-plugins-bad"},
		Hints:    map[string]string{"fedora": "rpmfusionVA", "opensuse": "packmanVA"}},
	{ID: "gst-plugins-good", Features: []string{"mkv", "convert", "photos"},
		present:  elements("matroskademux", "mp4mux", "qtdemux", "aacparse", "ac3parse", "jpegdec", "jpegenc", "pngdec", "videoflip"),
		Packages: map[string]string{"debian": "gstreamer1.0-plugins-good", "fedora": "gstreamer1-plugins-good", "opensuse": "gstreamer-plugins-good"}},
	{ID: "gst-libav", Features: []string{"convert"}, present: elements("avenc_aac"),
		Packages: map[string]string{"debian": "gstreamer1.0-libav", "fedora": "gstreamer1-plugin-libav", "opensuse": "gstreamer-plugins-libav"}},
	{ID: "pipewire", Features: []string{"tvOnlySound"}, present: onPath("pw-metadata"),
		Packages: map[string]string{"debian": "pipewire-bin", "fedora": "pipewire-utils", "opensuse": "pipewire-tools"}},
	// doubletake runs gst-launch-1.0 and pactl itself.
	{ID: "gstreamer-tools", Features: []string{"airplay"}, present: func() bool { return onPath("gst-launch-1.0")() && onPath("gst-inspect-1.0")() },
		Packages: map[string]string{"arch": "gstreamer", "debian": "gstreamer1.0-tools", "fedora": "gstreamer1", "opensuse": "gstreamer-utils"}},
	{ID: "pactl", Features: []string{"airplay"}, present: onPath("pactl"),
		Packages: map[string]string{"arch": "libpulse", "debian": "pulseaudio-utils", "fedora": "pulseaudio-utils", "opensuse": "pulseaudio-utils"}},
	// doubletake isn't in the distributions' main repositories: the AUR on
	// Arch, a release binary or a source build elsewhere. Manual keeps it
	// out of the install command.
	{ID: "doubletake", Features: []string{"airplay"}, present: func() bool { return doubletakePath() != "" },
		Manual: true, Packages: map[string]string{"arch": "doubletake-bin"},
		Hints: map[string]string{"arch": "doubletakeAUR", "debian": "doubletakeRelease", "fedora": "doubletakeRelease",
			"opensuse": "doubletakeRelease", "other": "doubletakeRelease"}},
	// Ubuntu 24.04 ships only the older heif-convert.
	{ID: "libheif", Features: []string{"heic"}, present: func() bool { return heifTool() != "" },
		Packages: map[string]string{"debian": "libheif-examples", "fedora": "libheif-tools", "opensuse": "heif-examples"},
		Hints:    map[string]string{"fedora": "rpmfusionHeif"}},
}

// heifTool is the HEIC decoder on PATH, if any.
func heifTool() string {
	for _, t := range []string{"heif-dec", "heif-convert"} {
		if _, err := exec.LookPath(t); err == nil {
			return t
		}
	}
	return ""
}

// missingFor lists the packages a feature needs that aren't there.
func missingFor(features ...string) []string {
	var missing []string
	for _, d := range dependencies {
		needed := false
		for _, f := range features {
			needed = needed || slices.Contains(d.Features, f)
		}
		if needed && !d.present() {
			missing = append(missing, d.ID)
		}
	}
	return missing
}

// dependencyReport is the answer to "check": every package with what it's
// for, whether it's there, and its name on this distro.
func dependencyReport() map[string]any {
	distro := distroFamily()
	deps := make([]map[string]any, 0, len(dependencies))
	missing := []string{}
	for _, d := range dependencies {
		pkg := d.ID
		if p, ok := d.Packages[distro]; ok {
			pkg = p
		}
		present := d.present()
		if !present && d.Required {
			missing = append(missing, d.ID)
		}
		dep := map[string]any{"id": d.ID, "package": pkg, "required": d.Required,
			"features": d.Features, "present": present}
		if d.Manual {
			dep["manual"] = true
		}
		if hint, ok := d.Hints[distro]; ok {
			dep["hint"] = hint
		}
		deps = append(deps, dep)
	}
	// missing (required only) keeps the setup view working as before.
	return map[string]any{"event": "check", "distro": distro, "deps": deps, "missing": missing}
}

// distroFamily maps os-release to a package naming the table knows: arch,
// debian, fedora or opensuse, which their derivatives follow. Anything else
// is "other" and gets the upstream names without a command.
func distroFamily() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "other"
	}
	defer f.Close()
	return familyOf(f)
}

func familyOf(r io.Reader) string {
	var ids []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		if k == "ID" || k == "ID_LIKE" {
			ids = append(ids, strings.Fields(strings.Trim(v, `"'`))...)
		}
	}
	for _, id := range ids {
		switch {
		case id == "arch", id == "fedora", id == "debian":
			return id
		case id == "ubuntu":
			return "debian"
		case strings.HasPrefix(id, "opensuse"), id == "suse":
			return "opensuse"
		case id == "rhel", id == "centos":
			return "fedora"
		}
	}
	return "other"
}

func avahiRunning() bool {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return false
	}
	defer conn.Close()
	var has bool
	conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, avahiName).Store(&has)
	return has
}
