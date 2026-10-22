# Screencast

Mirror your screen to a Chromecast, Google TV or AirPlay device, and play files, playlists,
slideshows and links on Chromecast devices, from the bar or the Control Center.

![The player and the popout during a cast](screenshot.png)

## Features

- **Screen mirroring** with sound over Chromecast, in 720p or 1080p at 30 or 60 fps, encoded on the
  GPU. AirPlay mirroring is also available through [doubletake](https://github.com/omarroth/doubletake).
- **Sound** on the cast device, on this computer, or on both.
- **Files:** videos, photos and music, played directly when the device supports them and converted
  on the GPU when it doesn't (8K video, unsupported codecs, HEIC photos).
- **Player** for slideshows and playlists: pick files or folders, save them as shows, and play them
  with a timer or step through them manually. `.m3u` and `.pls` playlists work too.
- **Links:** YouTube videos and playlists open in the YouTube app on the device, and direct media
  links play as they are.
- **Controls** in the bar pill, the Control Center and the media player widgets: pause, seek,
  skip and volume.

Devices that speak both Chromecast and AirPlay show up once. Chromecast is used by default, and
mirroring can be switched to AirPlay per device.

## Installation

Screencast has two parts: the plugin and `screencast-helper`, a small program that does the
casting. Both are needed.

### 1. Packages

| | Arch | Debian / Ubuntu | Fedora | openSUSE |
|---|---|---|---|---|
| **Required** | `avahi` `gstreamer` `gst-plugins-base` `gst-plugins-bad` `gst-plugin-pipewire` `gst-plugin-va` | `avahi-daemon` `gstreamer1.0-plugins-base` `gstreamer1.0-plugins-bad` `gstreamer1.0-pipewire` | `avahi` `gstreamer1-plugins-base` `gstreamer1-plugins-bad-free` `pipewire-gstreamer` | `avahi` `gstreamer-plugins-base` `gstreamer-plugins-bad` `gstreamer-plugin-pipewire` |
| **Recommended** | `gst-plugins-good` `gst-libav` `libheif` | `gstreamer1.0-plugins-good` `gstreamer1.0-libav` `pipewire-bin` `libheif-examples` | `gstreamer1-plugins-good` `gstreamer1-plugin-libav` `pipewire-utils` `libheif-tools` | `gstreamer-plugins-good` `gstreamer-plugins-libav` `pipewire-tools` `heif-examples` |
| **AirPlay** | `doubletake-bin` (AUR) `libpulse` | `gstreamer1.0-tools` `pulseaudio-utils` | `pulseaudio-utils` | `gstreamer-utils` `pulseaudio-utils` |

The Avahi daemon has to be running (`sudo systemctl enable --now avahi-daemon`). Mirroring needs a
GPU with VA-API H.264 or HEVC encoding. Fedora and openSUSE leave those codecs out of their default
drivers, so mirroring there needs the drivers from RPM Fusion or Packman.

Settings → Plugins → Screencast lists every package with what it's used for, marks what's missing and shows
the install command for your distribution. Other distributions can use the table as a guide.

### 2. The plugin

Install it from Settings → Plugins, or by hand:

```bash
git clone https://github.com/Conava/dux-plugins.git ~/.local/src/dux-plugins
ln -s ~/.local/src/dux-plugins/Screencast ~/.config/dms/plugins/screencast
```

Then enable it in Settings → Plugins and add it to a bar or the Control Center.

### 3. The helper

The plugin looks for `screencast-helper` on your `PATH`, then in
`~/.local/share/dms/screencast/bin/`. Its source is in [`screencast-helper/`](../screencast-helper)
in this repository. Either download the build for this release, or build it from source.

**Download** (x86_64 and aarch64). The plugin's popout shows this command for the installed
version:

```bash
dms dl https://github.com/Conava/dux-plugins/releases/download/screencast-v0.0.1/screencast-helper-$(uname -m) \
  --timeout 300 -o ~/.local/share/dms/screencast/bin/screencast-helper \
  && chmod +x ~/.local/share/dms/screencast/bin/screencast-helper
```

The binaries are built by GitHub Actions from the tagged source. To check the download against the
release's checksums:

```bash
cd ~/.local/share/dms/screencast/bin
dms dl https://github.com/Conava/dux-plugins/releases/download/screencast-v0.0.1/SHA256SUMS \
  | sed "s/-$(uname -m)\$//" | grep "screencast-helper$" | sha256sum -c
```

**Build from source** (Go 1.25 or newer, no C compiler needed):

```bash
cd ~/.local/src/dux-plugins/screencast-helper
CGO_ENABLED=0 go build -o ~/.local/share/dms/screencast/bin/screencast-helper .
```

Click "Check again" in the popout, or reopen it, and the plugin starts using the helper.

### 4. Firewall

The cast device connects back to this computer to fetch files and, over AirPlay, for timing and
sound. With a firewall, allow:

| Port | Used for |
|---|---|
| TCP 47830 | Playing files |
| UDP 47840–47849 | AirPlay |

Both can be changed in Settings, which also shows the matching `ufw` command.

## AirPlay

AirPlay mirroring needs [doubletake](https://github.com/omarroth/doubletake), an independent
AirPlay sender that is not part of this project. It is available as `doubletake-bin` in the AUR,
or from its releases page. Review it before installing, as with any third-party program.

The first connection to a device asks for the PIN it shows. Sound stays on this computer during
AirPlay mirroring, since doubletake would set the device to full volume. Files, playlists and links
need Chromecast.

## Notes

- Anything above 1080p at 30 fps, such as 1080p at 60 fps, is marked experimental: it works, but
  it can lag on slower networks and devices.
- Discovery is IPv4 only.
- Screen mirroring asks for the screen through the desktop portal. The choice is remembered, so the
  picker usually only appears the first time.

## Network and files

Besides the cast devices on the local network, the plugin only contacts youtube.com, when a YouTube
link is played, to hand the video to the device's YouTube app. It writes to:

- `~/.cache/dms/screencast`: converted videos and photos while they play, and thumbnails.
- `~/.local/share/dms/screencast`: the helper, and AirPlay pairings.
- `$XDG_RUNTIME_DIR/dms-screencast`: doubletake's control socket during an AirPlay cast.

## License

See [LICENSE](../LICENSE).
