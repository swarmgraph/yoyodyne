# yoyodyne-ifd.428.233: do Claude Code or Codex start shared helper processes inside a session?

Stopping a run's leftover processes (yoyodyne-ifd.428.218, design
[stopping what a run started](../designs/stopping-what-a-run-started.md)) stops
every process whose starting environment carries the run's identifier. The
architect asked whether Claude Code or Codex start a long-lived helper from
inside a session that other sessions then share, because the stop would end it
while they still use it. She ruled that, if such helpers exist, the stop leaves
alone a marked process that a live run or a person's own session still uses.

This records what was found on November 27, 2026, with Claude Code 2.1.332 and
Codex CLI 0.169.0-alpha.11.1, the versions installed on the operator's machine.
Times are PST.

## The answer

**Neither CLI starts a shared helper by itself in the mode the harness runs it
in.** The harness runs Claude Code as `claude -p` and Codex as `codex exec`
(`internal/backend/claudecode/backend.go`, `internal/backend/codex/backend.go`).
Both CLIs do have a shared helper, but each is started only by a person's
interactive use or by a command that asks for it:

- **Claude Code's background service.** `claude daemon --help` says it "runs on
  demand and exits once no client is connected and no background session is
  running". It runs background sessions (`claude --bg`, `claude agents`) for
  every project of the user at once: its own messages say stopping it "stops
  every project's background sessions".
- **Codex's app-server daemon.** `codex --help` offers the interactive program
  `--no-daemon`, "Run without the shared background server, even if it is
  already running", so the interactive program uses one shared server.
  `codex exec --help` has no such option, and `codex app-server daemon start`
  and `codex remote-control start` start the server on request.

So a session's own CLI never leaves one of these behind. A command a session
runs can still start one, and then the helper carries the run's identifier:

- a session's shell running `claude --bg`, `claude agents`, the interactive
  `codex`, or `codex app-server daemon start`;
- far more likely, a build tool that keeps a server for later builds, such as
  the Gradle daemon, the Kotlin compile daemon, or Bazel's server. The
  game-server project whose leftover Java server prompted the parent item is a
  Java project, which is where these are most common. Such a server is started
  by the first build that needs it, outlives that build, and is reused by later
  builds of the same user, including a person's own builds and other runs'.
  Its starting environment is the first build's, so it carries that run's
  identifier for as long as it lives.

The architect's ruling is therefore needed even though the two CLIs are not
the source: build servers started by a run's checks or a session's commands are
the case it protects.

## How it was found out

1. **The CLIs' own help**, read in full for `claude`, `claude daemon`,
   `codex`, `codex exec`, `codex app-server`, `codex app-server daemon`, and
   `codex remote-control`.
2. **Claude Code's background service log**, `~/.claude/daemon.log`. The harness
   names no separate provider home for this project, so its Claude Code
   sessions use the same `~/.claude` as the operator's own, and the log covers
   both. From October 24 to November 27 it records five starts: October 24,
   November 6 twice, and November 24 twice. Four of them were made for a
   background session started with a slash command, which only an interactive
   session can do, and the log names that session within a second of the
   start. The fifth, on November 24, was the service restarting itself onto a
   new Claude Code version. The harness has over 6,000 run records from that
   time, so a `claude -p` session does not start it. `claude daemon status`
   during this run, itself a harness session on Claude Code, said "not
   running".
3. **A Codex session with a home of its own.** `codex exec` was run with
   `CODEX_HOME` set to an empty folder and no network. It ran up to the point
   of calling the model and was stopped after 40 seconds. It left its session
   files, logs and locks in the folder, and no daemon folder, process number
   file or socket. The program's text names where the daemon keeps them
   (`app-server-daemon`, `daemon.pid`, `app-server.pid`), and none appeared.
4. **What could not be checked.** The sandbox this run works in refuses `ps`,
   and Claude Code's `-p` mode stopped at sign-in in a fresh home, so neither
   CLI was watched with a real model turn and a full process list. Points 2 and
   3 are the evidence instead.

## What the stop still needs

The stop this item changes is not on main yet: it is the parent item's change,
which on November 27 was still in the merge queue. Once it lands, the
remaining work is:

1. Before stopping a marked process of a run with nothing going, look for a
   sign that someone still uses it, and leave it alone when there is one.
2. Tests: a shared helper in use is left running, and one nobody uses is
   stopped.

What counts as "still uses" is not settled by the design. One reading that a
build server and both CLIs' helpers would satisfy: the process holds an open
connection, over a local socket or a loopback port, whose other end is a
process that is not a leftover of an ended run, that is, a live run's process
or a process carrying no run's identifier at all, such as a person's own
shell or build. A helper with no such connection is unused and is stopped.
That choice is the architect's.
