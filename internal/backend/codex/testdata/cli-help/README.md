# Recorded Codex CLI help

What the Codex CLI says each command level accepts, recorded so the command
contract check in `../../conformance_test.go` has something genuine to check the
backend's arguments against on a machine with no Codex installed.

- `exec.txt` is the standard output of `codex exec --help`.
- `exec-resume.txt` is the standard output of `codex exec resume --help`.

Both are the CLI's own output, unedited. Neither was written by hand.

**Where they came from.** `codex-cli 0.159.2` (`codex --version`), the
`aarch64-apple-darwin` build bundled inside the ChatGPT desktop app at
`/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex` (SHA-256
`50ab38ba21d0d9f8346f32f41848382f15b556190f3c7a07e885a4fb73e379c8`), recorded on
2026-09-30 by the developer run for yoyodyne-ifd.435.8. The CLI printed a warning
about PATH aliases on standard error, which is not part of the help and is not
recorded.

**Rechecked against 0.160.0**, the supported version, on 2026-10-07 by the
developer run moving the adapter to permission profiles (yoyodyne-ifd.435.27):
both commands' standard output was byte for byte the same as the files here, so
they stand for 0.160.0 unchanged. The same wrapper path (same SHA-256 as above)
now starts the executable at
`/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex`
(SHA-256 `6b582e8813ce7e8ed4c52814ee5cf230dba647bf2292df747a4003f2657ef201`).

The defect this copy was recorded for was first reproduced against
`codex-cli 0.153.4`, which rejects `--sandbox` after `exec resume` in the same
way. The machine this was recorded on had no 0.153.4 to ask, so this copy is the
newer CLI's rather than that one's.

**Replacing them.** Rerun both commands against the Codex you mean to support and
overwrite the files with their standard output, then update the version, path,
and checksum above. Where an installed `codex` is on `PATH`, the contract check
also asks it directly, so a newer CLI that moved an option shows up there before
anybody records it.
