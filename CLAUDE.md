@AGENTS.md

## Claude Code

- Load `/dms-plugin-dev` before writing or changing plugin code. It links into `.dux-shell`, so run
  `./dev setup` first if it's missing.
- Run `/review-plugin <Plugin>` before opening a PR or a registry submission.
- Hand the user `./dev link <Plugin>` / `./dev reload <Plugin>` and say what to look at. A passing
  `./dev check` isn't the end; trying it in the running shell is.
