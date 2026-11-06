# dux-plugins

Plugins for [Dux Shell](https://github.com/duxlabs-dev/dux-shell).

## Plugins

Please raise Issues, Feature Requests and alike in this repository. Discussion and status updates are in the Plugin Registry.
Please give the Plugins a thumbs up on the Plugin Registry issue to increase its rating and visibility to others.

| Plugin | Description | Plugin Registry |
|---|---|---|
| [Cast](Cast) | Mirror your screen to Chromecast, Google TV and AirPlay devices, and play files, music, playlists, slideshows and links on Chromecast. Needs a helper program, see its [installation guide](Cast/README.md#installation). | https://github.com/duxlabs-dev/dux-plugin-registry/issues/1089

## Installing

Install from Settings > Plugins once a plugin is in the [registry](https://plugins.duxlabs.dev/),
or by hand:

```bash
git clone https://github.com/Conava/dux-plugins.git
ln -s "$PWD/dux-plugins/<Plugin>" ~/.config/dms/plugins/<pluginId>
```

Then enable it in Settings > Plugins.

## Contributing

Fixes, improvements and translations are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).
