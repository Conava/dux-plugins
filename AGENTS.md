# Dux Shell plugins

Third-party plugins for [Dux Shell](https://github.com/duxlabs-dev/dux-shell) (DMS), maintained by the owner of this repository.
Each top-level PascalCase folder with a `plugin.json` is one plugin, listed in the [official registry](https://github.com/duxlabs-dev/dux-plugin-registry) with `path` pointing at that folder.
Outside contributions are mostly fixes, improvements and translations for existing plugins.
A new plugin requires an agreed issue first.
The plugins are not part of upstream, so the bar is lower than for the code of the shell itself, but a plugin has to look and behave as if it shipped with the shell.

## Setup

```bash
./dev setup [path/to/dux-shell]   # links .dux-shell, or sparse-clones upstream into it
```

`.dux-shell/` is gitignored and is the reference for everything below.
The plugin API is experimental and changes between minor releases, so **check every property, signal, service and component against `.dux-shell/quickshell`**, not against memory or old examples.
Users run the latest release, not master.
`git -C .dux-shell describe --tags --abbrev=0` prints that release, and `git -C .dux-shell grep <symbol> <tag> -- quickshell` checks that a symbol exists in it.
Using anything newer requires raising `requires_dms`.

API documentation:

- `.dux-shell/.agents/skills/dms-plugin-dev/`: the plugin skill of upstream (types, manifest, templates, references). The automated review of the registry checks plugins against it. Claude Code loads it as `/dms-plugin-dev`.
- `.dux-shell/quickshell/PLUGINS/`: `README.md`, `plugin-schema.json` and an example for each plugin type.
- `.dux-shell/quickshell/Modules/Plugins/`: the base components (`PluginComponent`, settings widgets).
- A built-in shell module that does the same kind of thing is the best style reference.

## Layout

```
MyPlugin/
├── plugin.json      # id camelCase (lowercase first), version semver
├── MyWidget.qml     # PascalCase QML files
├── Settings.qml     # optional, requires the settings_write permission
├── README.md        # what it does, dependencies, screenshots
├── screenshot.png   # used by the registry listing
└── translations/    # optional, <locale>.json, no en.json
```

A new plugin also gets one row in the plugin table of the root `README.md` (name, description, link to its registry issue).
Everything that applies to all plugins belongs in the root README, everything specific to one plugin in the README of that plugin.

## Code map

| File | Description |
|---|---|
| `dev` | The dev script: setup, manifest checks, linking into the running shell, translation sync. `link` and `reload` also build the helper of a plugin, `unlink` removes that build. |
| `Cast/plugin.json` | Manifest: a composite plugin with a daemon, a widget and a settings page. |
| `Cast/CastService.qml` | The daemon. Finds and runs the helper and owns the single connection to it (`cast-helper attach`, JSON lines). Contains the state that every surface binds to (`devices`, `noCast`, `session`, `deps`), the setup and firewall checks, the install, update and terminal commands, the Cast report (`reportState`, `reportScript`) and the error texts for each `code` (`errorText`, `errorHint`). Also hosts the player and the AirPlay PIN window, which require a single instance. |
| `Cast/CastWidget.qml` | Bar pills (horizontal and vertical), the Control Center tile and detail, and `DevicePanel` (device list, now playing, cast controls), which the bar popout and the Control Center detail share. |
| `Cast/Settings.qml` | The settings page: Setup, quality, media playback, bar, firewall and ports, helper path, packages, Troubleshooting (Detailed logging, Copy report). |

## Dev loop

```bash
./dev link MyPlugin     # builds the helper if there is one, symlinks into ~/.config/dms/plugins/<id>, rescans the shell
./dev reload MyPlugin   # after each edit: reload and print loaded or the error
./dev check             # validate every manifest and trFor call (CI runs the same check)
./dev strings MyPlugin  # sync translations/*.json with the strings in the code
./dev unlink MyPlugin
```

Enable the plugin once in Settings > Plugins and place it (bar, dock, dash, desktop).
Load errors are shown in the status line of `./dev reload` and in `journalctl --user -u dms -f`.
Test on a horizontal and a vertical bar, in light and dark mode, and with the plugin disabled and enabled again.

## Rules

The registry review rejects plugins that break these rules, and a plugin that breaks them feels foreign in the shell.

### Look native

- Tokens: `Theme.*` for every color, font size, spacing, radius and icon size. No literals.
- Widgets: from `qs.DCommon.Widgets` (`StyledText`, `StyledRect`, `DIcon`, `DButton`, `DToggle`, `DTextField`, `DListView`, `DFlickable` and so on). Never raw Qt controls or the deprecated `Dank*` names.
- Bar widgets: provide both `horizontalBarPill` and `verticalBarPill` and respect `compactMode`.
- Settings: use the `PluginSettings` components. Copy the structure of a built-in page or popout that does the same thing instead of inventing a layout.

### Cost nothing when idle

The shell runs 24/7.

- Polling: none where a service already exposes the state (`AudioService`, `NetworkService`, `BluetoothService`, `CompositorService` and so on). Timers run only while visible (`live` in dash surfaces). No animations that run all the time, no processes spawned on a short interval.
- Built-ins: prefer `dms` built-ins over external tools (`dms cl copy`, `dms notify`, `dms dl`, `dms screenshot`, `dms trash`). Each declared dependency is one more package that a user has to install.
- Dependencies: declare external tools in `dependencies` and gate on them with a `startupCheck`.

### Safe

- Commands: run through `Proc.runCommand` or `Process` with argv arrays. Never build a `sh -c` string from settings, user input or fetched data.
- Timeouts: `Proc.runCommand` kills a command after 10 s by default. Pass `Proc.noTimeout` for long work.
- Untrusted input: anything fetched or read from outside is untrusted. No downloading and running code, no `sudo` or `pkexec`, no undisclosed network calls, no writing outside the data of the plugin (`pluginData`, `savePluginState`).
- Readable code: no minified or generated code, because reviewers have to be able to read all of it.

### Bindings

In per-instance components (bar pills, delegates, popouts), bind to a typed readonly property and use its change handler instead of `Connections { function on<Prop>Changed }`.
Keep `Connections` for plain signals and for `pluginService.pluginDataChanged`.

### Strings

- trFor: every user-facing string goes through `I18n.trFor("<plugin id>", "…")` with the id and the string as literals. `./dev check` enforces this, and `./dev strings` finds strings by that pattern.
- Format: translations are stored in `translations/<locale>.json` in the format of the shell, `{"English": {"English": "translation"}}`. An empty translation falls back to the catalog of the shell, then to English.
- Sync: after adding or changing strings, run `./dev strings <Plugin>` so that every locale file lists them.
- Wording: do not pass the real-context flag (4th argument). One English string means one thing, so reword instead. Reuse wording that the shell already uses, because a changed English string loses its translations.
- Translations are plain PRs to this repository. The plugins are not part of the POEditor project of upstream.

### Code

- Structure: guard clauses and early returns.
- Comments: only what the code cannot say, in a line or two.
- Libraries: none for a small problem.
- Tests: only for non-trivial `.js` logic. The running shell is the real check.

## Cast and cast-helper

Cast does its work in [cast-helper](https://github.com/Conava/cast-helper), a Go service in its own repository.
One shared helper runs per user.
`CastService.qml` starts `cast-helper attach` and exchanges JSON lines with it.
`PROTOCOL.md` in cast-helper is the contract.
Changes to the protocol are additive, so a client works with any helper whose protocol is at least the one the client was written for (the Versions table lists what each protocol added).

- `helperProtocol` in `CastService.qml` is the lowest protocol that the plugin requires. A helper below it counts as outdated, and the device list then only links to Setup. Raise it only when the plugin cannot work without a newer feature, after checking the Versions table. A feature that the plugin can do without stays optional (`msg.noCast ?? []`).
- `helperRelease` is the release that the plugin installs and offers as an update to older helpers. To bump it: set the new tag, check that the release has `cast-helper-x86_64`, `cast-helper-aarch64` and `SHA256SUMS`, check that every link into the README of cast-helper still finds its anchor, and bump `version` of the plugin.
- Lookup order: the Helper path setting (`pluginData.helperPath`), then a dev build, then `cast-helper` on `PATH`, then the helper that the plugin downloaded itself.
- Local work on both: `./dev link Cast` and `./dev reload Cast` build a cast-helper checkout next to this repository (`../<id>-helper`, so `../cast-helper`) into `~/.local/share/dms/cast/dev/`, which the plugin prefers over an installed helper. Both commands stop the running shared helper, so the plugin starts the new build. Dev builds log in debug mode to `~/.cache/dms/cast/serve.log`.
- Fake missing packages: `FAKE_MISSING=a,b ./dev reload Cast` builds a helper that reports those dependency ids as missing, and a plain reload undoes it. `./dev unlink Cast` removes the dev build again.
- A change that also requires a change in cast-helper links the matching PR there.

## Shipping

- Release channel: `master`. `dms plugins update` and the update button in Settings pull its HEAD, so nothing lands there that does not load cleanly.
- Version: bump `version` in `plugin.json` with every user-facing change (semver).
- Commits: `<plugin folder in lowercase>: short lowercase summary`, e.g. `mywidget: hide when offline`. Repo-wide changes use `repo:`. Commit bodies put each sentence on its own line.
- Scope: one plugin per PR.
- Releases: for every plugin version on `master` without a release, a workflow creates the tag `<id>-v<version>` and a draft release.
  The owner edits and publishes the draft.

## Registry submission

A registry submission is done once per plugin, as a PR to `duxlabs-dev/dux-plugin-registry` that adds `plugins/<github-user>-<plugin-name>.json` (lowercase, hyphens).
`CONTRIBUTING.md` in the registry is the source. In short:

- Entry fields: `id` and `name` exactly as in `plugin.json`, `capabilities`, `category`, `repo` (the URL of this repository), `path` (the plugin folder), `author`, `description`, `dependencies`, `compositors`, `distro`, `screenshot` (a reachable direct image URL). Leave `i18n` out, because it opts into the POEditor of upstream and translations stay in this repository.
- Accuracy: capabilities, compositors, dependencies and `requires_dms` must match what the code does.
- Duplicates: no duplicate of an existing registry plugin unless the original is unmaintained or the new one is clearly better. Check `plugins/` in the registry first and explain the reason in the PR.
- Validation: run `python3 .github/generate.py --validate` and `python3 .github/validate_links.py` in a registry checkout (requires this repository to be pushed).
- Automated review: it reads the full plugin source for malware and convention breaks (Theme tokens, D* widgets, polling, APIs that do not exist). Run the `review-plugin` skill first.
- AI policy: AI-assisted code is allowed if the submitter has read and tested all of it and says so in the PR. PR text, issues and comments must be written by a person, otherwise the PR is closed.
