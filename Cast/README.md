# Cast

Mirror your screen to Chromecast, Google TV and AirPlay devices, and play files, music, playlists,
slideshows and links on Chromecast devices, from the bar or the Control Center.

![The player and the popout during a cast](screenshot.png)

## Features

- Screen mirroring with sound: Chromecast (720p or 1080p, 30 or 60 fps, GPU encoded) or AirPlay.
- Sound on the cast device, on this computer, or both.
- Videos, photos and music. Files the device can't play are converted on the GPU (8K, unsupported
  codecs, HEIC).
- A player for shows: mix files and folders, save them, play them on a timer or step through them.
  `.m3u` and `.pls` playlists work too.
- YouTube links open in the device's YouTube app. Direct media links play as they are.
- Controls in the bar pill, the Control Center and the media player widgets.

A device with both Chromecast and AirPlay shows up once. Mirroring uses Chromecast unless AirPlay
is picked for that device. Files, music and links always use Chromecast.

## Installation

Cast needs the plugin, `cast-helper` (does the casting) and a few system packages. AirPlay
additionally needs doubletake.

### 1. Install the packages

Arch:

```bash
sudo pacman -S --needed avahi gstreamer gst-plugins-base gst-plugins-bad gst-plugin-pipewire gst-plugin-va \
  gst-plugins-good gst-libav libheif
```

Debian / Ubuntu:

```bash
sudo apt install avahi-daemon gstreamer1.0-plugins-base gstreamer1.0-plugins-bad gstreamer1.0-pipewire \
  gstreamer1.0-plugins-good gstreamer1.0-libav pipewire-bin libheif-examples
```

Fedora:

```bash
sudo dnf install avahi gstreamer1-plugins-base gstreamer1-plugins-bad-free pipewire-gstreamer \
  gstreamer1-plugins-good gstreamer1-plugin-libav pipewire-utils libheif-tools
```

openSUSE:

```bash
sudo zypper install avahi gstreamer-plugins-base gstreamer-plugins-bad gstreamer-plugin-pipewire \
  gstreamer-plugins-good gstreamer-plugins-libav pipewire-tools heif-examples
```

The first line of each is required, the second recommended (more formats, sound on the cast device
only, HEIC photos). Then start Avahi:

```bash
sudo systemctl enable --now avahi-daemon
```

Mirroring needs VA-API H.264 or HEVC encoding. Fedora and openSUSE ship drivers without them; get
the drivers from RPM Fusion or Packman. Settings > Plugins > Cast lists every package, marks what's
missing and shows the install command for your distribution.

### 2. Install the plugin

From Settings > Plugins, or by hand:

```bash
git clone https://github.com/Conava/dux-plugins.git ~/.local/src/dux-plugins
ln -s ~/.local/src/dux-plugins/Cast ~/.config/dms/plugins/cast
```

Enable it in Settings > Plugins and add it to a bar or the Control Center.

### 3. Install cast-helper

The plugin looks for `cast-helper` on `PATH`, then in `~/.local/share/dms/cast/bin/`. A helper
installed somewhere else can be set in Settings > Helper path. It lives in its own repository,
[dux-cast](https://github.com/Conava/dux-cast), so other apps can share it. Use one of these:

Arch: install it from the AUR. This also installs the required packages from step 1.

```bash
yay -S cast-helper
```

Download the release build (x86_64 or aarch64, built by GitHub Actions from the tagged source).
It is only installed if it matches the release's checksums:

```bash
(mkdir -p ~/.local/share/dms/cast/bin && cd ~/.local/share/dms/cast/bin \
  && dms dl https://github.com/Conava/dux-cast/releases/latest/download/cast-helper-$(uname -m) \
    --timeout 300 -o cast-helper-$(uname -m) \
  && dms dl https://github.com/Conava/dux-cast/releases/latest/download/SHA256SUMS \
    | grep " cast-helper-$(uname -m)$" | sha256sum -c \
  && mv cast-helper-$(uname -m) cast-helper && chmod +x cast-helper)
```

Or build it from source (Go 1.25 or newer, no C compiler):

```bash
git clone https://github.com/Conava/dux-cast.git ~/.local/src/dux-cast && cd ~/.local/src/dux-cast
CGO_ENABLED=0 go build -o ~/.local/share/dms/cast/bin/cast-helper .
```

The popout shows these commands (the AUR one on Arch). Click "Check again" there once the helper
is in place. After a plugin update that needs a newer helper, the popout asks for the update with
the same commands.

### 4. Install doubletake (AirPlay only)

[doubletake](https://github.com/omarroth/doubletake) is an independent AirPlay sender, not part of
this project. Review it before installing. Cast is tested with 0.5.0.

Arch (AUR):

```bash
yay -S doubletake-bin
```

Other distributions, from its releases (x86_64 shown, use `doubletake-arm64` on aarch64):

```bash
dms dl https://github.com/omarroth/doubletake/releases/download/v0.5.0/doubletake \
  --timeout 300 -o ~/.local/share/dms/cast/bin/doubletake \
  && chmod +x ~/.local/share/dms/cast/bin/doubletake
```

Cast also finds doubletake on `PATH` or in `~/go/bin`.

doubletake also runs `gst-launch-1.0` and `pactl`: install `gstreamer1.0-tools` and
`pulseaudio-utils` on Debian / Ubuntu, `pulseaudio-utils` on Fedora, `gstreamer-utils` and
`pulseaudio-utils` on openSUSE, `libpulse` on Arch.

### 5. Open the firewall

Only needed with a firewall. The ports can be changed in Settings, which also shows the `ufw`
commands.

| Port | Used for |
|---|---|
| TCP 47830 | Playing files |
| UDP 47840-47849 | AirPlay |

## Notes

- AirPlay asks for the PIN on the device the first time. Sound stays on this computer, because
  doubletake would set the device to full volume.
- Settings above 1080p at 30 fps are marked experimental: they can lag on slow networks or devices.
- Mirroring asks for the screen through the desktop portal. The choice is remembered.
- Discovery is IPv4 only.
- Other apps can use the same helper. Their casts show in the bar and the Control Center too, with
  the app they play from.

## Network and files

Apart from the cast devices on the local network, Cast only contacts youtube.com, when a YouTube
link is played, and the server of a pasted direct link (or a link in a playlist file), to ask for
its media type when the address doesn't show it. Covers of songs another app casts are loaded from
where that app says. It writes to:

| Path | Contents |
|---|---|
| `~/.cache/dms/cast` | Converted files while they play, thumbnails, the helper's log |
| `~/.local/share/dms/cast` | The helpers, AirPlay pairings |
| `$XDG_RUNTIME_DIR` | The helper's socket and lock, `dms-cast/` during an AirPlay cast |

## License

See [LICENSE](../LICENSE).
