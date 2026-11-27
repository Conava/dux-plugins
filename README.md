# dux-plugins

[![Dux Shell](https://img.shields.io/badge/Dux%20Shell-2.0%2B-blue)](https://github.com/duxlabs-dev/dux-shell)
[![Check](https://github.com/Conava/dux-plugins/actions/workflows/check.yml/badge.svg)](https://github.com/Conava/dux-plugins/actions/workflows/check.yml)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

Plugins for [Dux Shell](https://github.com/duxlabs-dev/dux-shell). 🦆

> [!TIP]
> If you use a plugin from this repository, please give it a thumbs up on its registry issue, which is linked in the table below.
> The rating decides how visible a plugin is in the registry, so a thumbs up helps other users find it.

## Plugins

| Plugin | Description | Registry |
|---|---|---|
| [Cast](Cast) | Mirror your screen to Chromecast, Google TV and AirPlay devices, and play files, music, playlists, slideshows and links on Chromecast. | [Thumbs up here](https://github.com/duxlabs-dev/dux-plugin-registry/issues/1089) |

Check each plugin's README for screenshots, dependencies and settings.

## Installation

Install a plugin from Settings > Plugins once it is in the [registry](https://plugins.duxlabs.dev/).
To install a plugin by hand, clone this repository and link the plugin folder:

```bash
git clone https://github.com/Conava/dux-plugins.git
ln -s "$PWD/dux-plugins/<Plugin>" ~/.config/dms/plugins/<pluginId>
```

`<Plugin>` is the folder name, and `<pluginId>` is the `id` in the `plugin.json` of the plugin.
Then enable the plugin in Settings > Plugins.
Updates are installed with the update button in Settings > Plugins or with `dms plugins update`.

## Reporting issues

1. Read the README of the plugin first. Some plugins require additional information, such as a report from their settings, or handle part of their issues in another repository.
2. [Open an issue](https://github.com/Conava/dux-plugins/issues/new/choose) with one of the forms: Bug Report, Feature Request or Support Request. Fill out the information the form asks for.
3. Include the name and version of the plugin (Settings > Plugins), your distribution and your compositor. For a visual issue, attach a screenshot.

## Contributing

Fixes, improvements, translations and testers are welcome.
See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT, see [LICENSE](LICENSE).
