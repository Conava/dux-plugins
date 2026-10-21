---
name: review-plugin
description: Review a plugin in this repo the way the Dux Shell registry's automated review and its maintainers will, before a PR or registry submission. Use when asked to review a plugin, check it is ready, or before submitting it to the registry.
argument-hint: <PluginDir>
---

# Review a plugin

Target: `$ARGUMENTS` (a plugin directory; if empty, the plugins touched by `git diff master...HEAD`).

1. Run `./dev check <Plugin>`. Failures are blockers.
2. Read **every** file of the plugin, including scripts and anything it downloads or runs.
3. Check it against the rules in `AGENTS.md` and these, which mirror the registry's own review:
   - **Malware and abuse:** data sent off the machine, downloading and running code, obfuscated or
     minified code, persistence, `sudo`/`pkexec`, files touched outside its own config and cache,
     shell commands built from untrusted strings, hardcoded credentials, undisclosed network calls.
   - **Convention breaks:** fast polling, processes spawned on short intervals, always-running
     animations, polling where DMS exposes the state; hardcoded colors, font sizes, spacing or
     radii; raw Qt controls where a D* component exists; any property, signal, service or component
     that doesn't exist in DMS.
   - **Existence check:** grep each DMS symbol the plugin uses in `.dux-shell/quickshell` at the
     latest release tag (`git -C .dux-shell describe --tags --abbrev=0`). Missing at the tag but on
     master means `requires_dms` has to say so.
   - **Manifest truth:** `capabilities`, `dependencies`, `requires_dms` and the description match
     what the code does. Every user-facing string uses `I18n.trFor` with the literal plugin id.
   - **Native feel:** compare with a built-in module for the same surface in `.dux-shell/quickshell`
     (layout, spacing, controls, vertical bar, compact mode).
4. Report only what you confirmed in the files, ranked: **blocker** (registry would reject or it
   breaks the shell) then **issue** (should fix). One line each, `file:line`, what's wrong and the fix.
   End with a one-line footprint: commands run, hosts contacted, files written.

For an independent read, run steps 2–4 in a subagent that hasn't seen the plugin being written.
