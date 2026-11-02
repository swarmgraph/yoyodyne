# Recorded Codex streams

What `codex exec --json` wrote to standard output on real invocations, recorded
so the parser in `../../parser.go` is tested against what the CLI actually says
rather than against anybody's account of it. Each file is one invocation's
standard output, one event per line. None was written by hand, and each is
unedited except where its entry below names a redaction.

Each directory is named for the `codex --version` that wrote the streams in it.

## codex-cli-0.159.2

Recorded on 2026-10-01 by the developer run for yoyodyne-ifd.435.7 from
`/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex` (SHA-256
`50ab38ba21d0d9f8346f32f41848382f15b556190f3c7a07e885a4fb73e379c8`, the same
wrapper `../cli-help` was recorded from), which runs the `aarch64-apple-darwin`
binary at `../CodexCLI.app/Contents/MacOS/codex` (SHA-256
`50ac633af64851511f9bbc71032cdae7f1ba20b3234c189687d61ba846c354c5`).

- `no-provider-reached.jsonl` is
  `echo hi | codex exec --sandbox read-only --json --skip-git-repo-check -`, run
  with `CODEX_HOME` set to an empty directory, inside a sandbox whose network
  proxy refused every connection to `api.openai.com` and `chatgpt.com`. **It never
  reached the provider**, so it carries no reply, no usage, and no terminal: the
  CLI was still waiting for the network when the recording was stopped after
  twelve lines. What it does show is genuine — the CLI writes bare events with
  no `msg` envelope, names them `thread.started`, `turn.started`, and
  `item.completed`, carries the session as `thread_id`, and reports each
  reconnect attempt as a top-level `error` event that does not end the turn.
  Nothing in it was secret: the thread id is the throwaway session this
  invocation started, and no credential was loaded.

- `reply-and-turn-completed.jsonl` is
  `echo "Reply with the single word: ready" | codex exec --sandbox read-only --json --skip-git-repo-check -`,
  run on 2026-10-01 by the operator's assistant outside any sandbox, against the
  same `codex-cli 0.159.2` wrapper, with the operator's own `CODEX_HOME` signed in
  through ChatGPT. **It reached the provider and completed.** It shows the reply
  as an `item.completed` whose item is `{"type":"agent_message","text":"ready"}`,
  and the turn ending in `turn.completed`, which carries no text and carries the
  turn's `usage`: `input_tokens`, `cached_input_tokens`,
  `cache_write_input_tokens`, `output_tokens`, and `reasoning_output_tokens`. Two
  `item.completed` events of item type `error` arrive before `turn.started`, and
  are warnings rather than failures: the CLI was ignoring two settings in the
  operator's `config.toml`, and the turn went on to complete. **One redaction:**
  the warning named the configuration file by its absolute path under the
  operator's home directory, and that home directory is written here as `~`
  (`~/.codex/config.toml`); nothing else was changed. The thread id is the
  throwaway session the invocation started, and no credential appears. The CLI's
  stderr on that run held only MCP transport errors for a local server that was
  not running, and is not recorded.

## What is still missing

- **A failed turn.** The `codex-cli 0.159.2` binary names a `turn.failed` event,
  and the parser reads it as the turn's failed ending with the `message` of its
  `error` object as its prose, which is the shape the provider's exec protocol
  gives it. No stream here shows one, so that shape is not yet evidence; a
  turn refused for a usage limit or a bad model would be.
- **Shell, patch, and tool items.** A developer turn runs commands and edits
  files, which the CLI reports as items of other types (`command_execution`,
  `file_change`, and so on, with `item.started` beside them). None is recorded
  here; the parser names the first one as unrecognized and otherwise records it
  and carries on, which matters to the turn's outcome only if the stream then ends
  with no terminal. Each one is also reported as tool use the harness could not
  see (`internal/tooluse`), because it may be a tool call nobody read.
  A recording of a turn that runs a shell command is the next one to make, outside
  the developer sandbox, which refuses network to the provider and refuses writes
  to the operator's `~/.codex`:

```sh
mkdir -p "$HOME/codex-recording" && cd "$HOME/codex-recording"
echo "Run ls in this directory, then reply with the word: done" |
  codex exec --sandbox read-only --json --skip-git-repo-check - > command-and-reply.jsonl
codex --version
```
