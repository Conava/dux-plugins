# Dux Shell plugins

Third-party plugins for [Dux Shell](https://github.com/duxlabs-dev/dux-shell) (DMS), started by the
repo owner. Outside contributions are mostly fixes, improvements and translations to existing
plugins; a new plugin needs an issue agreed first. Each top-level
PascalCase directory with a `plugin.json` is one plugin, listed in the
[official registry](https://github.com/duxlabs-dev/dux-plugin-registry) with `path` pointing at it.
Not part of upstream, so the bar is lower than the shell's own code, but a plugin should look and
behave like it shipped with the shell.

## Setup

```bash
./dev setup [path/to/dux-shell]   # links .dux-shell, or sparse-clones upstream into it
```

`.dux-shell/` is the reference for everything below and is gitignored. The plugin API is
experimental and changes between minor releases, so **check every property, signal, service and
component you use against `.dux-shell/quickshell`** instead of trusting memory or old examples.
Users run the latest release, not master: `git -C .dux-shell describe --tags --abbrev=0` names it,
and `git -C .dux-shell grep <symbol> <tag> -- quickshell` checks a symbol exists there. Using
something newer means raising `requires_dms`.

Where the API is documented:

- `.dux-shell/.agents/skills/dms-plugin-dev/` — upstream's plugin skill: types, manifest,
  templates, references. The registry's automated review checks plugins against it. Claude Code
  loads it as `/dms-plugin-dev`.
- `.dux-shell/quickshell/PLUGINS/` — `README.md`, `plugin-schema.json`, and an example per plugin type.
- `.dux-shell/quickshell/Modules/Plugins/` — the base components (`PluginComponent`, settings widgets).
- A built-in shell module doing the same kind of thing is the best style reference.

## Layout

```
MyPlugin/
├── plugin.json      # id camelCase (lowercase first), version semver
├── MyWidget.qml     # PascalCase QML files
├── Settings.qml     # optional, needs the settings_write permission
├── README.md        # what it does, dependencies, screenshot
├── screenshot.png   # used by the registry listing
└── translations/    # optional, <locale>.json, no en.json
```

## Dev loop

```bash
./dev link MyPlugin     # symlink into ~/.config/dms/plugins/<id> and rescan the running shell
./dev reload MyPlugin   # after each edit: reload and print loaded/error
./dev check             # validate every manifest and trFor call (CI runs the same)
./dev strings MyPlugin  # sync translations/*.json with the strings in the code
./dev unlink MyPlugin
```

Enable the plugin once in Settings → Plugins and place it (bar, dock, dash, desktop). Load errors
show in `./dev reload`'s status line and in `journalctl --user -u dms -f`. Test on a horizontal and
a vertical bar, in light and dark mode, and with the plugin disabled and re-enabled.

## Rules

These are the things the registry review rejects and that make a plugin feel foreign.

**Look native**
- `Theme.*` tokens for every color, font size, spacing, radius and icon size. No literals.
- Widgets from `qs.DCommon.Widgets` (`StyledText`, `StyledRect`, `DIcon`, `DButton`, `DToggle`,
  `DTextField`, `DListView`, `DFlickable`…), never raw Qt controls or the deprecated `Dank*` names.
- Bar widgets provide both `horizontalBarPill` and `verticalBarPill` and respect `compactMode`.
- Settings use the `PluginSettings` components. Copy the shape of a built-in page or popout doing
  the same thing rather than inventing a layout.

**Cost nothing when idle.** The shell runs 24/7.
- No polling where a service already exposes the state (`AudioService`, `NetworkService`,
  `BluetoothService`, `CompositorService`, …). Timers run only while visible (`live` in dash
  surfaces), no always-running animations, no processes spawned on a short interval.
- Prefer `dms` built-ins over external tools: `dms cl copy`, `dms notify`, `dms dl`, `dms screenshot`,
  `dms trash`. Each declared dependency is one more thing a user has to install.
- Declare external tools in `dependencies` and gate on them with a `startupCheck`.

**Safe**
- Commands go through `Proc.runCommand` or `Process` with argv arrays. Never build a `sh -c`
  string from settings, user input or fetched data.
- `Proc.runCommand` kills after 10 s by default; pass `Proc.noTimeout` for long work.
- Anything fetched or read from outside is untrusted. No downloading and running code, no `sudo`
  or `pkexec`, no undisclosed network calls, no writing outside the plugin's own data
  (`pluginData`, `savePluginState`).
- No minified or generated code; reviewers have to be able to read all of it.

**Bindings.** In per-instance components (bar pills, delegates, popouts) bind to a typed readonly
property and use its change handler instead of `Connections { function on<Prop>Changed }`. Keep
`Connections` for plain signals and for `pluginService.pluginDataChanged`.

**Strings.** Every user-facing string goes through `I18n.trFor("<plugin id>", "…")` with the id and
the string as literals (`./dev check` enforces it). `./dev strings` finds them by that pattern and
keeps `translations/<locale>.json` in the shell's format, `{"English": {"English": "translation"}}`.
An empty translation falls back to the shell's own catalog, then English. Don't pass the
real-context flag (4th argument): one English string means one thing, so reword instead. Reuse
wording the shell already uses; a changed English string loses its translations. After adding or
changing strings, run `./dev strings <Plugin>` so every locale file lists them. Translations are
plain PRs to this repo; the plugins aren't in upstream's POEditor project.

**Code.** Guard clauses and early returns. Comment only what the code can't say, in a line or two.
No library for a small problem. Tests only for non-trivial `.js` logic; the running shell is the
real check.

## Shipping

- `master` is the release channel: `dms plugins update` and the Settings update button pull its
  HEAD. Nothing lands there that doesn't load cleanly.
- Bump `version` in `plugin.json` with every user-facing change (semver).
- Commits: `<plugin dir in lowercase>: short lowercase summary`, e.g. `mywidget: hide when offline`.
  Repo-wide changes use `repo:`.
- One plugin per PR.

## Registry submission

Done once per plugin, as a PR to `duxlabs-dev/dux-plugin-registry` adding
`plugins/<github-user>-<plugin-name>.json` (lowercase, hyphens). Its `CONTRIBUTING.md` is the source;
in short:

- Entry fields: `id` and `name` exactly as in `plugin.json`, `capabilities`, `category`, `repo`
  (this repo's URL), `path` (the plugin directory), `author`, `description`, `dependencies`,
  `compositors`, `distro`, `screenshot` (a reachable direct image URL). Leave `i18n` out: it opts
  into upstream's POEditor, and translations here stay in this repo.
- Capabilities, compositors, dependencies and `requires_dms` must match what the code does.
- No duplicate of an existing registry plugin unless the original is unmaintained or this one is
  clearly better; check `plugins/` there first and say why in the PR.
- Validate in a registry checkout with `python3 .github/generate.py --validate` and
  `python3 .github/validate_links.py` (needs this repo pushed).
- An automated review reads the full plugin source for malware and convention breaks (Theme tokens,
  D* widgets, polling, APIs that don't exist). Run the `review-plugin` skill first.
- AI policy: AI-assisted code is allowed if the submitter has read and tested all of it and says
  so in the PR. PR text, issues and comments must be written by a person; AI-written text gets the PR
  closed.
