# Contributing

Any help on the existing plugins is very welcome: bug fixes, improvements,
support for another compositor, and translations. If you have an idea for a new feature, new plugin,
notice any bugs or want to help translate, please open an issue or reach out to me.
PRs are welcomed from everyone!

## Translations

Each plugin keeps its translations in `translations/<locale>.json`, using the shell's locale names
(`de`, `es`, `pt`, `zh_CN`, …). To start a new language or bring one up to date:

```bash
./dev strings MyPlugin de
```

This writes every string the plugin uses into `MyPlugin/translations/de.json`. Fill in the empty
values and open a PR. Anything left empty shows in English. Run it again later to pick up new
strings; it keeps what's already translated.

## Plugin development

You need Dux Shell running, plus `jq` and Python's `jsonschema`.

```bash
./dev setup            # links ../dux-shell if you have a checkout, else clones the parts it needs
./dev link MyPlugin    # makes the plugin visible to your running shell
./dev reload MyPlugin  # after each change
./dev check            # validates the manifests and strings, same as CI
```

If you already installed the plugin from the registry, remove it first in Settings > Plugins.

[AGENTS.md](AGENTS.md) has the rules a plugin follows. It's written for AI agents but it's
the same guide for people.

## Pull requests

- One plugin per PR. Bump its `version` if users will notice the change.
- Say how you tested it: compositor, bar orientation, light and dark theme.
- Screenshots for anything visual.

## AI

Using AI to write code is fine. You're responsible for every line: you've read it, tested it in
your shell, and can explain it. Say in the PR if a meaningful part was generated. Write the PR
description and comments yourself.
