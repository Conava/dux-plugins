# dux-plugins

Plugins for [Dux Shell](https://github.com/duxlabs-dev/dux-shell).

## Plugins

| Plugin | |
|---|---|
| [Screencast](Screencast) | Mirror your screen to Chromecast, Google TV and AirPlay devices, and play files, playlists, slideshows and links on Chromecast. Needs a helper program, see its [installation guide](Screencast/README.md#installation). |

## Installing

Install from Settings → Plugins once a plugin is in the [registry](https://plugins.duxlabs.dev/),
or by hand:

```bash
git clone https://github.com/Conava/dux-plugins.git
ln -s "$PWD/dux-plugins/<Plugin>" ~/.config/dms/plugins/<pluginId>
```

Then enable it in Settings → Plugins.

## Contributing

Fixes, improvements and translations are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).
