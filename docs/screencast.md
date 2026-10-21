# Screencast: design

Mirror the screen, with sound, to a Chromecast or an AirPlay receiver, from the bar, the Control
Center or the island. Successor to the closed shell PR
[duxlabs-dev/dux-shell#2697](https://github.com/duxlabs-dev/dux-shell/pull/2697), rebuilt as a
plugin.

## Scope of v1

- **Play a file or URL on the TV:** the TV fetches and decodes the original itself, so a 4K movie
  plays at full quality with no re-encoding. Play, pause, seek, volume from the plugin.
- Screen mirroring, video and audio, to **Chromecast** (real time) and **AirPlay 2**.
- One monitor at a time, picked in the portal dialog.
- A favorite device to start casting to in one click.
- Not in v1: single-window casting, several targets at once.

Chromecast is tested by the maintainer. AirPlay is built from doubletake's documentation and
tested by community volunteers once Chromecast works.

## Architecture

```
Screencast plugin (QML)  ──JSON lines over stdin/stdout──►  screencast-helper (Go, no CGO)
  bar pill + popout                                            │
  Control Center tile + detail panel                           ├─ avahi (D-Bus)            discovery
  settings, setup page                                         ├─ xdg-desktop-portal       screen access
        ▲                                                      ├─ gst-launch-1.0           capture + encode
        │ MPRIS                                                ├─ Cast v2 + Cast Streaming  Chromecast
  island, media widgets ◄──────── helper's MPRIS player ───────┤
                                                               └─ doubletake (daemon)     AirPlay
```

**Plugin:** one `widget` plugin. It provides the bar pill and the Control Center tile from the same
component, and shares one service singleton (declared in the plugin's `qmldir`) between every
instance across bars and screens. The service owns the helper process. It holds the state the UI
binds to and does no work of its own.

**Helper:** a single static Go binary, `screencast-helper`, in `screencast-helper/` at the repo root.
- **Lifetime:** runs only while the popout or detail panel is open, or a cast is running. Exits on
  stdin EOF, so it can't outlive the shell.
- **Owns:** discovery, sessions, the portal request, the capture pipeline, and the MPRIS player.

**External requirements:** avahi-daemon running, GStreamer with the plugins listed under Setup,
PipeWire, and a portal backend with ScreenCast (every DMS compositor has one). AirPlay also needs
doubletake.

## Helper protocol

One JSON object per line. The plugin sends commands, the helper sends events. The full state is
pushed on every change, so the plugin never polls.

```jsonc
// plugin → helper
{"cmd": "discover", "on": true}
{"cmd": "start", "device": "<id>", "audio": "remote" /* or "both" */}
{"cmd": "stop"}
{"cmd": "pair", "device": "<id>", "pin": "1234"}     // AirPlay first connect
{"cmd": "volume", "level": 0.4}                      // receiver volume, where supported

// helper → plugin
{"event": "state", "devices": [{"id", "name", "model", "protocol": "chromecast|airplay"}],
 "session": {"device": "<id>", "phase": "starting|casting|stopping", "since": 0} | null}
{"event": "needPin", "device": "<id>"}
{"event": "error", "code": "portalDenied|deviceLost|encoderMissing|…", "detail": "…"}
{"event": "check", "missing": ["gst:vah264enc", "avahi", …]}   // answer to {"cmd": "check"}
```

Error codes map to translated strings in QML. The helper never sends user-facing text.

## Chromecast: Cast Streaming

This is the mirroring protocol Chrome uses for tab and desktop casting. The receiver is built into
every Chromecast and Google TV, so nothing gets installed on the TV. A sender built on Google's Open
Screen library has been measured at about 120 ms latency at 1080p.

1. **Control:** a TLS connection on port 8009 (Cast v2: length-prefixed protobuf with a JSON
   payload, about 200 lines without any library). Launch the mirroring receiver (`0F5096E8`), then
   exchange OFFER/ANSWER on the `urn:x-cast:com.google.cast.webrtc` namespace to agree on codecs,
   SSRCs, ports, AES keys and the target playout delay (200 ms works).
   The first cast from a new computer makes the TV ask the viewer for permission: LAUNCH answers
   `LAUNCH_STATUS` `USER_PENDING_AUTHORIZATION`, so the UI says "Accept on your TV" and waits up to
   90 s.
2. **Media:** RTP over UDP in Cast's profile. Frames are AES-128-CTR encrypted. RTCP sender reports
   go out; receiver reports, ACK/NACK and picture-loss requests come back. Retransmit on NACK, force
   a keyframe on picture loss.
3. **Codecs:** do it the way Chrome does. First ask the receiver with `GET_CAPABILITIES`; its
   `CAPABILITIES_RESPONSE` lists `mediaCaps` (`h264`, `vp8`, `vp9`, `hevc`, `av1`, `4k`). Then offer
   one video stream per codec this machine can encode, in Chrome's priority order (HEVC, AV1, VP9,
   H.264, VP8), each with its `codecParameter` (e.g. `hev1.1.6.L123.B0`, H.264 Main at level 4.2).
   The receiver's ANSWER picks a stream through `sendIndexes`. Offering a single codec is a trap:
   the spike's LG accepted a lone HEVC offer and then showed only a black screen.
   **Mirroring tops out at 1080p.** Chrome caps it at 1920×1080 and 30 fps (60 only behind a flag),
   and its codec levels stop at 1080p60. The spike's LG OLED C3 (2023) lists
   `aac, opus, vp9, h264, hevc, vp8, audio` with no `4k`, picks H.264 when offered HEVC and H.264,
   quits or stalls on anything above 1080p, and falls behind at 1080p60. 4K is the job of file
   playback, not mirroring.
4. **Encoding:** `gst-launch-1.0` writes the chosen video codec and Opus into the helper, which
   splits frames and hands them to the sender. What the spike needed:
   - `aud=true` on the encoder, so access units split cleanly at their delimiters; `b-frames=0`;
     Main profile, as the `codecParameter` declares.
   - `vapostproc disable-passthrough=true` right after `pipewiresrc`. Without it, a capture that
     already has the output size passes the portal's own buffer to the encoder, which keeps it as a
     reference frame until the portal's small pool runs dry and capture stops.
   - `videorate` after that copy for a steady frame rate on an idle desktop, then a one-frame leaky
     `queue` before the encoder to drop stale frames.
   - The VA H.264 encoder writes no colour description, and receivers may then assume BT.601 while
     vapostproc produces BT.709 (measured: within 2/255 when read as BT.709, off by up to 23 as
     BT.601). The helper rewrites each SPS to declare BT.709 limited range.
   - Defaults that held up: 1080p30, 12 Mbit/s CBR, a key frame every 2 s, 200 ms playout delay.
5. **Screen access:** the portal ScreenCast session (ported from #2697). The PipeWire fd is passed
   to gst-launch as fd 3. Request `persist_mode` 2 and store the restore token so the dialog only
   appears on the first cast. On Hyprland the picker issues a token only when its "Allow a restore
   token" box is ticked, so the UI should say so the first time.

Reference implementations to read, not copy: Open Screen's `cast/streaming` (BSD-3) and omacast
(Rust, MIT), which follows the same design. Ported code keeps its license notice.

## AirPlay: doubletake

- **Daemon mode:** doubletake runs with `-daemonize` and a control socket. The helper drives it and
  passes `-target` from avahi's results, so doubletake does no discovery of its own.
- **Capture:** doubletake does its own through GStreamer; the helper's pipeline isn't involved.
- **Pairing:** the first connect to an Apple TV shows a PIN on the TV, so the plugin asks for it
  (`needPin` → `pair`). doubletake stores the pairing.
- **Firewall:** a fixed local port range for its back channel, documented in the plugin README.
- **Limits:** receivers publish their mirroring display in the plist at `http://<device>:7000/info`
  (`displays`: `widthPixels`, `heightPixels`, `maxFPS`). The LG OLED C3 says 1920×1080 at 60.

## Audio

Setting **Play sound on**: *TV only* (default) or *TV and this computer*.

- **TV only:** while casting, the helper creates a virtual PipeWire sink, makes it the default
  output and captures its monitor. Local speakers stay silent, and the old default is restored when
  the cast ends, crashes or the helper exits.
- **TV and this computer:** capture the current default sink's monitor. The TV runs behind the
  speakers, which is why this isn't the default.

## Island and media widgets

The helper registers an MPRIS player (`org.mpris.MediaPlayer2.screencast`): title "Casting to
<device>", playback status Playing, `Stop` ends the cast. The island, the media widget, the lock
screen and `playerctl` show it without any shell change. If music is playing at the same time, the
shell's active-player choice may show the music instead.

## Surfaces

**Bar pill:**
- **Idle:** cast icon. Hidden when the "show only while casting" setting is on.
- **Casting:** a live indicator (pulsing dot in the error color, like a recording light), the device
  name, and a stop button. The pulse runs only while casting.
- **Vertical bar:** live indicator and stop button, no name.
- Clicking anywhere but the stop button opens the popout.

**Popout and Control Center detail panel (one shared component):**
- **Header:** state ("Casting to Living Room TV" / "Not casting").
- **Device list:** found devices, with icons by protocol, a star for the favorite and a spinner
  while scanning.
- **While casting:** Stop button, receiver volume slider where the receiver supports it, and the
  audio choice.
- **Setup required:** when something is missing, the list is replaced by the setup view (below).

**Control Center tile:**
- Toggle: starts casting to the favorite device, or stops.
- Secondary text: device name or "Off".
- With no favorite set, the toggle opens the detail panel instead.

Every user-facing string goes through `I18n.trFor("screencast", …)`. Plugin id: `screencast`,
directory `Screencast/`.

## Settings

- **Favorite device**, also settable from the star in the list.
- **Play sound on:** TV only / TV and this computer.
- **Quality presets** that fill in the three values below: Balanced (1080p30), Smooth (1080p60),
  Low bandwidth (720p30). Changing a value by hand switches to Custom.
- **Resolution:** 720p or 1080p for Chromecast mirroring (the protocol's ceiling); AirPlay may
  allow more, which its testers will tell. File playback always plays the original.
- **Frame rate:** 30 (default) or 60, where the receiver keeps up.
- **Bitrate:** Auto (from resolution and frame rate) or a fixed value in Mbit/s.
- **Codec:** Auto (best one the TV accepts), or a fixed one for troubleshooting.
- **Show bar pill only while casting.**
- **Helper:** installed version, location, and Update / Reinstall buttons.

## Setup and installation

The helper's `check` reports what's missing. The plugin shows it as a setup view and, the first
time, as a notification (`dms notify --action`) with a button that opens the setup view.

| Missing | What the plugin offers |
|---|---|
| helper / doubletake | **Install** (default): download the pinned release into `~/.local/share/dms/screencast/bin`, verify its SHA-256, done. **Install system-wide**: the same files into `/usr/local/bin` via `pkexec install`, hidden on NixOS. **Build from source**: `go build` the helper from the plugin's own checkout and doubletake from its pinned tag; shown only when `go` is on PATH. |
| avahi not running | The command for the distro (`sudo systemctl enable --now avahi-daemon`; on NixOS `services.avahi.enable = true;`) with a copy button |
| GStreamer elements | The package names for the distro (Arch, Fedora, Debian/Ubuntu, openSUSE, Void, NixOS), detected from `/etc/os-release`, with a copy button |

- **Pinning:** versions and checksums live in the plugin source. A plugin update that pins a newer
  helper shows "Update helper".
- **Lookup:** a helper or doubletake on `PATH` is used before the downloaded copy.
- **Releases:** built by GitHub Actions for amd64 and arm64 on a `screencast-helper-v*` tag,
  `CGO_ENABLED=0`, with a checksums file.

## Safety and cost

- **Screen access** always goes through the portal: the user picks the monitor once, and the restore
  token is kept in the plugin's state, nowhere else.
- **Network:** traffic goes only to the chosen device on the LAN, except a release download from
  GitHub after an explicit click.
- **Idle cost is zero.** No helper process, no timers. Discovery runs only while a device list is on
  screen.
- **Cleanup:** stopping, a crash or the helper exiting always restores the default audio sink and
  closes the portal session.

## Milestones

0. **Spike:** desktop to the maintainer's Chromecast in real time from a throwaway Go program.
   *Done for mirroring (2026-10-21), see "Spike results". File playback still to spike.*
1. **Helper:** discovery, Chromecast mirroring with audio, the protocol, MPRIS, and `check`.
2. **Plugin:** service, bar pill, popout, CC tile and detail panel, settings.
3. **Install:** releases, download, system-wide and source installs, the setup view.
4. **AirPlay:** doubletake integration and the PIN flow, then community testing.
5. **Registry submission.**

## Spike results

Tested from a Hyprland desktop (AMD RX 7900 XTX, VA-API) to an LG OLED65C3 over Ethernet to Wi-Fi:

- **Works:** discovery through avahi, the permission prompt on the TV, the Cast Streaming session,
  encrypted video and Opus audio, retransmission on loss. A test pattern's clock matched a phone to
  the second, and the real desktop with sound mirrored at 1080p30.
- **Stalls:** at 1080p30 the TV still occasionally stops acknowledging video for a few seconds
  without reporting any loss. The receiver seems to stop consuming frames, so the sender has to
  back off: answer picture-loss requests with a key frame at once and lower the bitrate while
  feedback lags, as Chrome's sender does.
- **Colours** are close but not right yet, even after the BT.709 rewrite. The next step is an A/B
  test of BT.709 vs BT.601 colour bars on the TV, with the TV in an accurate picture mode.
- **Latency:** 1–2 s at 1080p60 came from the TV falling behind. Still to measure at 1080p30.

## Open questions

- Runtime control of the encoder (key frame on demand, bitrate changes) isn't possible through
  `gst-launch-1.0`, and linking GStreamer normally needs CGO. Decide how the helper gets that
  control before milestone 1.
- File playback details: how to pick a file (portal file chooser, drag onto the popout, `dms` IPC
  for "open with"), which containers the TV plays directly (MP4 and WebM are documented, MKV is not),
  and whether to remux MKV on the fly without re-encoding. Subtitles (WebVTT tracks).
- Does disabling the plugin destroy its singleton (and so the helper)? Check in milestone 2; if
  not, stop the helper explicitly when the last widget instance goes away.
