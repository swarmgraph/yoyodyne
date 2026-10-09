# Recorded answers to the launch-settings check

What Claude Code said when the check in `../../settingscheck.go` asked it, over
the control protocol of `--input-format stream-json`, which settings it applied
(`get_settings`), which hooks it will run (`get_hooks_listing`), and whether its
sandbox is running (`get_sandbox_dialog`). Each file is the CLI's standard
output, unedited, one answer per line. Neither was written by hand.

- `claude-2.1.286-accepted.jsonl` is the answer to the developer's settings as
  the adapter passes them: the sandbox, the `yoyo goals guard` hook,
  `autoMemoryEnabled`, `disableClaudeAiConnectors`, and one `claudeMdExcludes`
  pattern. The tests replace the merged settings in the first answer with the
  settings they launched with, because the excluded patterns depend on the
  directory a test runs in.
- `claude-2.1.286-dropped.jsonl` is the answer to the same settings with
  `autoMemoryEnabled` given as the string `"no"`. The CLI rejected that one key,
  ignored the whole payload, and started anyway: no merged settings, no hooks,
  and the sandbox off with a fallback to running commands unconfined.

**Where they came from.** `2.1.286 (Claude Code)` (`claude --version`), the
Homebrew cask build at `/opt/homebrew/Caskroom/claude-code@latest/2.1.286/claude`,
recorded on 2026-10-09 by the developer run for yoyodyne-ifd.435.24. The CLI ran
with a fresh, empty `CLAUDE_CONFIG_DIR` and no login, in an empty directory, with
`--no-session-persistence`; no prompt was sent and no provider call was made.

**Replacing them.** Launch the CLI the way `settingsCheckArgs` does, write the
three control requests to its standard input and close it, and overwrite the
files with what it prints; then update the version, path, and date above.
