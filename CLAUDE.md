# Instructions for an AI session working in this repository

This file is the architect's instructions to a developer session — any AI coding
session working in this repository, whether the harness started it or a person
did — about how the repository is worked on. It states no product intent of its
own. What the product is for, its goals, and the standing rules every role works
under live in [the product home](docs/product/README.md), which is authoritative
wherever this file seems to disagree with it; the standing rules are in
[the operating rules](docs/product/operating-rules.md) there.

`CLAUDE.md` and `AGENTS.md` are one file kept in two places, because Claude Code
reads the first and Codex the second. A change goes into both, byte for byte;
`make test` fails when they differ (`internal/composition`).

## Build and test

`make check` runs the four checks this project declares — `make fmtcheck`,
`make test`, `make race`, and `make vet` — and is what CI runs.
[Working on yoyo itself](docs/developing-yoyo.md) is the rest: what a checkout
needs, where the build cache goes, how tests wait, and what `make test` checks
besides the Go code.

A package with very large files carries a `README.md` code map
(`internal/orchestrator`, `internal/chat`): read it before paging through
`pipeline.go` or `chat.go`, and keep it current when you move what it names.

## The tracker

The harness reads and writes the tracker on the session's behalf.

The tracker is Beads (`bd`), whose store is a Dolt database in the primary
checkout under `.beads/embeddeddolt`. The product home's operating rules say a
developer run never runs `bd`, and in a developer run it cannot: the run may
write only to its own worktree and to `.git`, every `bd` command opens the store
for writing, `bd show` included, and every one of them fails with

```
failed to open database: embeddeddolt: init schema: embeddeddolt: open db: failed to load database "yoyodyne": openat LOCK: operation not permitted
```

That refusal is the run's sandbox doing its job. Do not look for a way round it;
there is none (`docs/diagnoses/yoyodyne-ifd-206-coined-terms-sweep.md`).

What a developer run uses instead:

- **Its own work item** is the one the harness puts in the run's prompt: title,
  description, design guidance, acceptance criteria, and notes. That is what the
  run is for.
- **The work around it** is in `.beads/issues.jsonl` in the worktree, read with
  ordinary file tools. The harness copies it from the primary checkout when it
  makes the worktree, so it holds every item that existed when the run started,
  the run's own included, and nothing added since. It is held out of the run's
  change, so do not edit it. Where it and the work item in the prompt disagree,
  the prompt is right.
- **Its own progress** is recorded by the run itself. Do not open, claim, update,
  or close items, and do not file work you find. Name found work in the run's
  summary, for the Lead Product Manager to admit or decline.

A work item that cannot be finished without writing to the tracker was admitted
by mistake. Do the rest of it, and say in the summary which part needs a tracker
write and that a developer run cannot make one.

`bd setup` writes a section of its own into this file and `AGENTS.md`, between
`BEADS INTEGRATION` or `BEADS CODEX SETUP` markers, telling every session to use
`bd` for all its tracking. That section has been taken out, and `make test`
fails if it comes back, so if `bd setup` is ever run here, remove what it wrote
before committing. The Beads skill it installs under `.agents/skills/beads/` says
the same and is read the same way: it describes a session that can reach the
store, and a developer run is not one.

## Writing to the tracker from a session that can

These are for whoever can reach the store: the harness, the Lead Product
Manager's conversation, and a person at an interactive session. They are not a
reason for a developer run to try.

### Never replace a work item's notes

[The operating rules](docs/product/operating-rules.md#rules) govern changes to a
work item's notes. For a session that can reach the store, `--append-notes`
adds a note; `--notes` replaces the whole field:

```bash
bd update <id> --append-notes="what you want to record"   # adds to the notes
bd update <id> --notes="what you want to record"          # DESTROYS everything already there
```

An item records the goal it serves as a `Goal served:` line in its notes, so a
replacement can erase that with it. It has happened twice, to eighteen items, every
time from `bd update --notes=` typed into an agent session
(`docs/diagnoses/yoyodyne-ifd-122-goal-attribution-loss.md`).

`yoyo goals guard` refuses every recognized `bd update --notes` replacement,
even one carrying a `Goal served:` line. Notes are append-only: preserve every
earlier note and add a correction with `--append-notes`. The guard reads only the
command line and never opens the tracker.
The harness adds the guard to every developer run on Claude Code. An
interactive Claude Code session gets it from a `PreToolUse` hook on `Bash` in
`.claude/settings.json`:

```json
{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"yoyo goals guard"}]}]}}
```

Codex hooks cannot stop a command, so a Codex session has only this paragraph.
`yoyo goals attribution` reports any item, open or closed, whose goal line has
been destroyed.

### Never move a status backwards without a note

`bd update <id> --status=...` on its own changes a status and records nothing
about why. Put the reason on the same command, with `--append-notes`, and never
reopen an item that closed because its change merged:

```bash
bd update <id> --status=open --append-notes="released for the repair the development manager handed back at turn N"   # says what moved it
bd update <id> --status=open                                                                                        # silent; REFUSED by the guard
```

On 2026-09-18 an operator's script outside the repository did exactly that to
four items in a day, reopening two whose changes had merged and releasing two
that were waiting on a person
(`docs/diagnoses/yoyodyne-ifd-392-status-rewrites-by-the-carry-out-queue.md`).
Every status change the harness makes carries its note on the same command
(`internal/beads/client.go`), and its re-run and repair commands claim the item
themselves, so nothing has to reopen an item before asking the harness to run
it. `yoyo goals guard` refuses a `bd update --status` with no note, and
`bd reopen`, before either runs; it passes `--claim`. A script is a command line
the guard cannot see inside, so whoever writes one is bound by this too.

## Scratch files go in the directory the run was given

The harness gives every developer run its own directory, outside its worktree,
and names it in the run's prompt. Anything the work needs that the change must
not carry goes there, check logs first among them.

`$TMPDIR` is not a substitute: it is one directory shared by every run on the
machine. On 2026-09-01 two runs five seconds apart both wrote
`$TMPDIR/probe-check.log`, and one reported a broken toolchain from the other's
compile error while its own `make check` passed
(`docs/diagnoses/yoyodyne-ifd-238-probe-verdict-crosstalk.md`). The worktree is
not a substitute either: a scratch file there is untracked content every reviewer
is shown.

A session the harness did not start has no such directory and shares `$TMPDIR`
with whatever else is running.

## Anything started in the background ends on its own

Whatever you start in the background has to stop by itself, however the thing
that started it ends. A cleanup that only runs when everything goes well is not
enough.

On 2026-09-05 a load test started twenty-four endless loops and died before the
line that would have killed them. They ran for hours after the run was over and
slowed the run working beside it.

Two things guard against that, and only the first is the harness's:

- **The harness kills the process group.** Every command it runs — the AI
  session and every check — leads a process group of its own, killed when the
  command ends, pass or fail (`internal/execution/process.go`). Background work
  stays in that group unless something moved it out.
- **The work sets its own end.** The group kill does not reach something that
  started a session of its own — `setsid`, a launchd job, a tool that detaches
  what it starts — so put the deadline in the work itself:

```sh
# a loop that stops by itself after sixty seconds, however the test ends
( end=$(( $(date +%s) + 60 )); while [ "$(date +%s)" -lt "$end" ]; do :; done ) &
```

`timeout(1)` is not on macOS by default, so a deadline the work computes itself
is the portable form. The same goes for a server, a watcher, or a poller started
for a test.

## Shell commands must not prompt

`cp`, `mv`, and `rm` may be aliased to ask before overwriting, which leaves a
session waiting forever. Use the forms that do not ask:

```bash
cp -f source dest           # NOT: cp source dest
mv -f source dest           # NOT: mv source dest
rm -f file                  # NOT: rm file
rm -rf directory            # NOT: rm -r directory
cp -rf source dest          # NOT: cp -r source dest
```

Likewise `scp` and `ssh` with `-o BatchMode=yes`, `apt-get` with `-y`, and
`brew` with `HOMEBREW_NO_AUTO_UPDATE=1`.

## Documents written in an interactive session

A design document, report, or analysis written in an interactive session is
Markdown under `ai-output/`, unless the person asks for another format or place.

- Read `ai-output/markdowns/AGENTS.md` first and follow its file-name and
  metadata rules. This project's directory is `ai-output/markdowns/yoyodyne/`.
- A requested page count is a length, not a request for Word or PDF.
- `ai-output` is a symlink; resolve it before writing, and if writing there needs
  permission, ask for it rather than choosing somewhere else.
- Before handing a document over, check its format and path and link to it.
  After a requested move, check the old path is gone and the text came across.

This does not apply to governed repository documents or to a developer run's
scratch files, which follow their own paths.
