## Description

<!-- What does this PR change, and why? -->

## Type of change

- [ ] Bug fix
- [ ] New feature or improvement
- [ ] New plugin
- [ ] Translation
- [ ] Documentation
- [ ] Other

## Related issues

<!-- "Closes #123" closes the issue when this is merged; "Refs #123" only links it. Leave empty if there's none. -->

## Screenshots / recordings

<!-- Required for every visual change: before and after, in light and dark theme. -->

## How I tested it

<!-- Compositor, horizontal and/or vertical bar, light and dark theme, and what you tried. -->

## Checklist

- [ ] One plugin per PR
- [ ] Tested in my running shell with `./dev reload <Plugin>`, without errors in `journalctl --user -u dms`
- [ ] `./dev check` passes
- [ ] New user-facing strings use `I18n.trFor("<plugin id>", "…")`, and I ran `./dev strings <Plugin>`
- [ ] Bumped `version` in `plugin.json` if users will notice the change
- [ ] Updated the plugin's README if setup, dependencies or features changed
- [ ] Screenshots attached for visual changes
- [ ] Changes that also need cast-helper link the matching PR in [dux-cast](https://github.com/Conava/dux-cast)
- [ ] If AI wrote a meaningful part, I've said so here, and I've read, tested and can explain every line
