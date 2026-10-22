# Cast: design

Mirror the screen, with sound, to a Chromecast or an AirPlay receiver, from the bar, the Control
Center or the island. Successor to the closed shell PR
[duxlabs-dev/dux-shell#2697](https://github.com/duxlabs-dev/dux-shell/pull/2697), rebuilt as a
plugin.

## Scope of v1

- **Play a file on the TV** (Chromecast only; doubletake can't): the TV fetches and decodes the
  original itself, so a 4K movie plays at full quality with no re-encoding. Play, pause, seek,
  volume and subtitles from the plugin.
- **Play a link:** YouTube links open in the TV's YouTube app, direct media links (MP4, WebM, HLS,
  DASH, audio) in its media player. Other pages need the site's own cast button or mirroring.
- Screen mirroring, video and audio, to **Chromecast** (real time) and **AirPlay 2**.
- One monitor at a time, picked in the portal dialog.
- A favorite device to start casting to in one click.
- Not in v1: single-window casting, several targets at once.

Chromecast is tested by the maintainer. AirPlay is built on doubletake, checked against a local
UxPlay receiver, and tested by community volunteers once Chromecast works.

## Architecture

```
Cast plugin (QML)      ──JSON lines over stdin/stdout──►  cast-helper (Go, no CGO)
  bar pill + popout                                            │
  Control Center tile + detail panel                           ├─ avahi (D-Bus)            discovery
  settings, setup page                                         ├─ xdg-desktop-portal       screen access
        ▲                                                      ├─ libgstreamer (dlopen)    capture + encode
        │ MPRIS                                                ├─ Cast v2 + Cast Streaming  Chromecast
  island, media widgets ◄──────── helper's MPRIS player ───────┤
                                                               └─ doubletake (daemon)     AirPlay
```

**Plugin:** one `composite` plugin with two surfaces.
- **`daemon` (`CastService.qml`):** owns the helper process and holds the state the UI binds
  to. The shell creates it once and destroys it when the plugin is disabled or reloaded, which stops
  the helper. A `qmldir` singleton would survive both.
- **`widget` (`CastWidget.qml`):** the bar pill, the popout and the Control Center tile and
  detail panel. Every instance reads the daemon through `pluginService.pluginDaemonInstances`.
- **One file per surface:** reload only recompiles a plugin's entry files, so shared sibling files
  would go stale. The device panel is an inline component of the widget, and Settings keeps its own
  copy of the option lists.

**Helper:** a single Go binary built without CGO, `cast-helper`, in `cast-helper/` at the repo root.
- **Lifetime:** runs only while the popout or detail panel is open, or a cast is running. Exits on
  stdin EOF, so it can't outlive the shell.
- **Owns:** discovery, sessions, the portal request, the capture pipeline, and the MPRIS player.

**External requirements:** avahi-daemon running, GStreamer with the plugins listed under Setup,
PipeWire with `pw-metadata`, and a portal backend with ScreenCast (every DMS compositor has one).
AirPlay also needs doubletake, which itself needs `gst-launch-1.0`, `gst-inspect-1.0` and `pactl`.

## Helper protocol

One JSON object per line. The plugin sends commands, the helper sends events. The full state is
pushed on every change, so the plugin never polls.

```jsonc
// plugin → helper
{"cmd": "check"}
{"cmd": "discover", "on": true}
{"cmd": "start", "device": "<id>", "audio": "tv|both|local", "width": 1920, "height": 1080, "fps": 30,
 "kbps": 0 /* auto */, "codec": "auto|h264|hevc", "delay": 200, "restoreToken": "…",
 "protocol": "chromecast|airplay" /* where a device has both */, "airplayPorts": "47840-47849"}
{"cmd": "pair", "pin": "1234"}                                // AirPlay PIN or password
{"cmd": "play", "device": "<id>", "path": "/abs/movie.mkv" | "url": "https://…", "port": 47830}
{"cmd": "queue", "device": "<id>", "items": [{"path": "…"} | {"url": "…"}], "index": 0, "interval": 8,
 "loop": false, "shrinkPhotos": true, "convertVideos": true, "port": 47830}
{"cmd": "pause"} {"cmd": "resume"} {"cmd": "seek", "position": 30} {"cmd": "subtitles", "on": false}
{"cmd": "next"} {"cmd": "previous"} {"cmd": "jump", "index": 3} {"cmd": "slideshow", "interval": 0}
{"cmd": "move", "device": "<id>"}                        // same cast, other device
{"cmd": "thumbnail", "path": "/abs/video.mp4", "size": 256}   // a song's thumbnail is its cover
{"cmd": "playlist", "path": "/abs/list.m3u8"}                // .m3u, .m3u8, .pls
{"cmd": "stop"}
{"cmd": "volume", "level": 0.4}                          // receiver volume, where supported

// helper → plugin
{"event": "state", "devices": [{"id", "name", "model", "protocol": "chromecast|airplay", "protocols": [...]}],
 "session": null | {"kind": "mirror|media", "device", "name",
   "phase": "starting|authorizing|pairing|converting|casting", "since", "volume", "canVolume",
   "protocol"?: "airplay", "credential"?: "pin|password" /* while pairing */,
   // media only:
   "title", "progress", "playerState", "position", "positionAt", "duration", "rate",
   "subtitles", "subtitlesOn", "index", "interval", "loop",
   "queue": [{"title", "kind": "image|video|audio|link|youtube", "status": "waiting|preparing|ready|failed",
              "progress", "artist", "error"?, "feature"?, "missing"?}]}}
{"event": "restoreToken", "token": "…"}                  // the plugin stores it in its state
{"event": "thumbnail", "path": "…", "file": "~/.cache/dms/cast/thumbs/<hash>.jpg" | ""}
{"event": "playlist", "path": "…", "items": [{"path"?, "url"?, "title"?}]}  // entries in order
{"event": "error", "code": "missingDependency|portalDenied|deviceLost|receiverStopped|…", "detail": "…",
 "feature"?: "mirroring", "missing"?: ["gst-plugin-va"]}
{"event": "warning", "code": "soundOnBoth", "feature": "tvOnlySound", "missing": ["pipewire"]}
{"event": "warning", "code": "airplaySound"}                 // AirPlay sound stays on this computer
{"event": "check", "distro": "arch|fedora|debian|opensuse|void|nixos", "missing": [required ids],
 "deps": [{"id", "package", "required", "present", "features": ["mirroring", …]}]}
```

- **Dependencies are optional at start.** The helper runs with nothing installed; each feature checks
  its packages when used and fails with `missingDependency` naming them. Features: `discovery`,
  `mirroring`, `sound`, `tvOnlySound`, `files`, `mkv`, `convert`, `photos`, `heic`, `thumbnails`.
  Required packages are those for discovery and mirroring; the rest are recommended. Without
  `pw-metadata`, "only the cast device" falls back to both and says so; without the photo
  elements, photos go out unshrunk.
- **Queue:** a single `play` is a queue of one. The current item and the next video or next two
  photos are prepared ahead (served, remuxed, converted, shrunk), one at a time; prepared copies more
  than two behind are deleted. Photos advance on the interval, videos when they finish. A YouTube
  link plays alone, in the YouTube app.
- **Music:** songs go out with their title, artist, album and cover (embedded, else `cover.jpg` /
  `folder.jpg` in the folder), so the device shows a music screen. A playlist file inside a `queue`
  expands in place; relative entries are relative to the playlist's folder.
- **Mixed shows:** songs, photos, videos and links can share a queue, played one after another:
  photos for the interval (or until next in Manual mode), the rest to their end. The device plays
  one thing at a time, so music can't run under photos; rendering that on the PC was ruled out.
- **Devices:** a TV that speaks Chromecast and AirPlay is one device, merged by address, keeping
  its Chromecast id. Mirroring uses Chromecast unless `protocol` says AirPlay; files, links and
  shows always use Chromecast, and an AirPlay-only device mirrors only (`airplayNoMedia`).
- **Position:** the receiver reports it only on changes. The plugin counts on from `positionAt`
  while playing, with a timer that runs only while the panel is on screen.
- **MPRIS:** file playback exposes play/pause, next/previous and seek; mirroring stays display-only.
- **Debugging:** `CAST_DEBUG=1` in the shell's environment logs every Cast message and HTTP request.
- **Error codes** map to translated strings in QML. The helper never sends user-facing text.

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
4. **Encoding:** the helper runs the GStreamer pipeline in its own process, loading
   `libgstreamer-1.0` at run time through purego, so it needs no CGO. The pipeline ends in an
   `appsink`; the helper splits frames and hands them to the sender. That gives the sender a key
   frame on demand (upstream force-key-unit, about 40 ms) and bitrate changes on the running
   encoder. The cost: the binary is dynamically linked against glibc's loader, so musl systems
   and NixOS without nix-ld need a build from source or a distro package. What the spike needed:
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
   to `pipewiresrc` as its `fd`. Request `persist_mode` 2 and store the restore token so the dialog only
   appears on the first cast. On Hyprland the picker issues a token only when its "Allow a restore
   token" box is ticked, so the UI should say so the first time.

Reference implementations to read, not copy: Open Screen's `cast/streaming` (BSD-3) and omacast
(Rust, MIT), which follows the same design. Ported code keeps its license notice.

## AirPlay: doubletake

[doubletake](https://github.com/omarroth/doubletake) (Go, LGPL-3.0-or-later, active). The spike
paired and streamed to its test receiver and to a local UxPlay.
- **Lifetime:** the helper starts `doubletake -daemonize -socket … -creds <plugin data>/…` per AirPlay
  session and stops it with SIGTERM. Its daemon always runs its own mDNS browsing, so it must not
  stay up. `-creds` keeps its pairings out of a standalone install's `~/.config/doubletake`.
- **Control:** one JSON request per connection on the socket, no events: `connect {target, port,
  pin?}`, `disconnect`, `status`, `mute`/`unmute`. The helper polls `status` about once a second
  during a session.
- **Pairing:** `pin_required` with `credential_kind` `pin` → `needPin`. A `password` kind exists for
  receivers set to require one. doubletake stores pairings in its creds file.
- **Capture:** doubletake runs `gst-launch-1.0` and opens **its own portal dialog**, storing that
  restore token in its creds file per receiver.
- **Audio:** off (`-no-audio`), sound stays on this computer. With sound, doubletake 0.5.0 sets the
  receiver to 0 dB, full volume (it turned an LG C3 up to 100), and has no option to keep the
  current level. Worth reporting upstream: sending the receiver's `initialVolume` from `/info`
  instead would fix it.
- **Firewall:** random ports unless `-port-range` is given: three UDP ports per receiver, inbound.
- **Limits:** doubletake reads the receiver's display size itself; frame rate is ours (`-fps`).
- **No version flag:** the helper tracks it by the pinned checksum.
- **Encoder:** the helper passes `-hwaccel vaapi` when VA-API is there. doubletake 0.5.0's automatic
  choice, the Vulkan encoder, sends no frames from a real screen at 60 fps (its test pattern works);
  worth reporting upstream.
- **60 fps** decodes on the LG C3 over AirPlay in H.264 and HEVC (HEVC even at 4K), but lags like
  60 fps over Chromecast, so it stays experimental.

## Audio

Setting **Play sound on**: *TV only* (default) or *TV and this computer*.

- **TV only:** the helper's capture stream is itself a sink (`media.class=Audio/Sink`), owned by the
  helper's own PipeWire connection, so PipeWire removes it however the helper ends. It becomes the
  default only through the runtime key `default.audio.sink`, which WirePlumber never saves. The
  user's chosen default is never changed, so the speakers come back by themselves.
  - While casting, the helper watches the metadata and claims the default again after a device is
    plugged in, unless the user picked another output.
  - Apps the user once moved to a device keep playing there.
  - Volume keys control what the TV receives.
- **TV and this computer:** capture the current default sink's monitor. The TV runs behind the
  speakers, which is why this isn't the default.

## Island and media widgets

The helper registers an MPRIS player (`org.mpris.MediaPlayer2.cast`) while mirroring: title
"Casting to <device>", playback status Playing, CanPlay true (playerctl skips players without it),
no artist (so no lyrics or artwork lookups). The island, the media widget, the dash and `playerctl`
show it. The shell's buttons only call play/pause and never Stop, so the player is display-only and
the cast is ended from the pill or tile. Whichever player was playing first stays the active one.

## Surfaces

**Bar pill:**
- **Idle:** cast icon. Hidden when the "show only while casting" setting is on.
- **Casting:** a live indicator (pulsing dot in the error color, like a recording light), the device
  name, and a stop button. The pulse runs only while casting.
- **Vertical bar:** live indicator and stop button, no name.
- Clicking anywhere but the stop button opens the popout.

**Popout and Control Center detail panel (one shared component):**
- **Header:** state ("Casting to Living Room TV" / "Not casting").
- **Device list:** found devices, with icons by protocol, a pin for the favorite, a button to play
  a file on that device (the shell's file browser), and a spinner while scanning.
- **While casting:** Stop button and receiver volume slider where the receiver supports it. For a
  file also play/pause, a seek bar with times, and a subtitles toggle.
- **Options:** Play sound on and Quality, the same settings as the settings page.
- **Setup required:** when something is missing, the list is replaced by the setup view (below).

**Control Center tile:**
- Toggle: starts casting to the favorite device, or stops.
- Secondary text: device name or "Off".
- With no favorite set, the toggle shows a toast; the plugin API can't open the detail panel.

Every user-facing string goes through `I18n.trFor("cast", …)`. Plugin id: `cast`,
directory `Cast/`, helper in `helpers/cast/`.

## Settings

- **Favorite device**, also settable from the star in the list.
- **Play sound on:** TV only / TV and this computer.
- **Quality presets:** Balanced (1080p30), Smooth (1080p60), Low bandwidth (720p30), or Custom,
  which shows the three values below.
- **Resolution:** 720p or 1080p for Chromecast mirroring (the protocol's ceiling); AirPlay may
  allow more, which its testers will tell. File playback always plays the original.
- **Frame rate:** 30 (default) or 60, where the receiver keeps up.
- **Bitrate:** Auto (from resolution and frame rate) or a fixed value in Mbit/s.
- **Codec:** Auto (best one the TV accepts), or a fixed one for troubleshooting.
- **Delay:** how far the TV plays behind (100–1000 ms, default 200).
- **Show bar pill only while casting**, and **show the device name** in the pill.
- **Helper:** installed version, location, and Update / Reinstall buttons.

## Setup and installation

The helper's `check` reports what's missing. The plugin shows it as a setup view and, the first
time, as a notification (`dms notify --action`) with a button that opens the setup view.

| Missing | What the plugin offers |
|---|---|
| helper / doubletake | **Install** (default): download the pinned release into `~/.local/share/dms/cast/bin`, verify its SHA-256, done. **Install system-wide**: the same files into `/usr/local/bin` via `pkexec install`, hidden on NixOS. **Build from source**: `go build` the helper from the plugin's own checkout and doubletake from its pinned tag; shown only when `go` is on PATH. |
| avahi not running | The command for the distro (`sudo systemctl enable --now avahi-daemon`; on NixOS `services.avahi.enable = true;`) with a copy button |
| GStreamer elements | The package names for the distro (Arch, Fedora, Debian/Ubuntu, openSUSE, Void, NixOS), detected from `/etc/os-release`, with a copy button |

- **Pinning:** versions and checksums live in the plugin source. A plugin update that pins a newer
  helper shows "Update helper".
- **Lookup:** a helper or doubletake on `PATH` is used before the downloaded copy.
- **Releases:** built by GitHub Actions for amd64 and arm64 on a `cast-helper-v*` tag,
  `CGO_ENABLED=0`, with a checksums file.

## Safety and cost

- **Screen access** always goes through the portal: the user picks the monitor once, and the restore
  token is kept in the plugin's state (AirPlay: in doubletake's creds file).
- **Network:** traffic goes only to the chosen device on the LAN, with three exceptions, each after
  an explicit action: playing a YouTube link calls youtube.com's lounge API (lounge token, bind,
  setPlaylist) like a phone's YouTube app does; a direct link without a known extension gets one
  HEAD request for its type; and a release download from GitHub.
- **Idle cost is zero.** No helper process, no timers. Discovery runs only while a device list is on
  screen.
- **Cleanup:** the user's default output is never changed, so nothing needs restoring. Stopping
  closes the portal session and the receiver app; remuxed copies in `~/.cache/dms/cast` are
  deleted after playback and on the next start after a crash.

## Milestones

0. **Spike:** desktop to the maintainer's Chromecast in real time from a throwaway Go program.
   *Done for mirroring (2026-10-21), see "Spike results". Audio routing, discovery, MPRIS, file
   playback and doubletake spiked on 2026-10-22 without the TV.*
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

## File playback

- **Default Media Receiver** (`CC1AD845`): LOAD with the file's URL; play, pause, seek and
  `EDIT_TRACKS_INFO` need the receiver's `mediaSessionId`. Volume goes to the device, not the media.
- **Serving:** a small HTTP server on the address that routes to the TV, one unguessable path,
  Range support and CORS, answering only the TV's IP. It listens on a fixed TCP port (setting,
  default 47830), because the TV connects in and a firewall has to allow it, e.g.
  `sudo ufw allow from 192.168.0.0/16 to any port 47830 proto tcp`. Mirroring and YouTube need no
  rule. When the TV hasn't fetched anything 10 s after LOAD, the plugin says the firewall blocks it.
- **Formats:** MP4, WebM and audio files are sent as they are. MKV isn't documented for Cast, so it
  is remuxed to a temporary MP4 (`moov` first, so the TV can seek) without re-encoding video.
  Matroska has no decode timestamps, so H.264/HEVC need `h26Xtimestamper` before `mp4mux`. Audio the
  TV can't take in MP4 (DTS, TrueHD, FLAC) is re-encoded to AAC. A 2.4 GB movie took about 5 s on
  NVMe; it needs free space equal to the file.
- **Too much for the TV:** each video is probed first (GStreamer's discoverer). Above 4K, or in a
  codec the TV lacks (AV1, MPEG-2, …), it is converted on the GPU to 4K HEVC at 30 Mbit/s with AAC
  audio, with progress in the panel. A 68 s 8K phone clip took 20 s on an RX 7900 XTX. `decodebin3`
  stalls without a GLib main loop, so the pipeline uses `decodebin`.
- **Bandwidth:** files are sent untouched, so a 60 Mbit/s 4K120 phone video can rebuffer over the
  TV's Wi-Fi.
- **Subtitles:** a `movie.srt` or `movie.vtt` next to `movie.mkv` is converted to WebVTT and attached.
- **Direct links** go into LOAD as they are; the TV fetches them, so they need to work without the
  browser's cookies, and HLS/DASH servers must send CORS headers.
- **YouTube links** (watch, youtu.be, shorts, live, music, with `list` and `t`) follow pychromecast
  and catt (casttube): launch `233637DE`, `getMdxSessionStatus` on `urn:x-cast:com.google.youtube.mdx`
  for the screen id, then youtube.com's lounge API: `get_lounge_token_batch`, `bc/bind`, and
  `setPlaylist`. Play, pause, seek and volume then go through the media namespace. The API is
  undocumented and can break; the error says so and mirroring still works.
- Untested until the TV: whether it plays remuxed HEVC and AC-3, and whether HDR metadata survives.

## Open questions

- **Install:** downloading a binary and `pkexec install` conflict with this repo's rules (no
  downloading and running code, no sudo or pkexec). Settle with the registry before milestone 3;
  source builds and distro or AUR packages avoid it.
- Drag and drop a file onto the bar pill to play it (a `DropArea`, like the clipboard button).
