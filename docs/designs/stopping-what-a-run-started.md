---
id: stopping-what-a-run-started
kind: design
title: Stopping every process a run started when the run ends
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-27T17:56:47.39387Z
      reason: 'Answers yoyodyne-ifd.428.218: a run''s processes carry its identifier in their environment and are stopped when it ends, after each session, and on maintenance for a run with nothing going. A process only found inside the run''s folders is named and never stopped, because stopping the wrong one is the worse mistake. Unstoppable ones are named with their port on status. The search lives in internal/execution; the process start code was not read.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-27T17:56:47.39387Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4256, for document-4256.1 under the automatic approval policy
---
# Stopping every process a run started when the run ends

This design answers the item on stopping every process a run started (yoyodyne-ifd.428.218). It serves the goal that implementation tasks are isolated in worktrees the harness manages: a run's work must not outlive the run and reach into later runs.

## What happened

In the operator's game server plugin project, a developer run started a Java test server at 19:35 PST on November 26, 2026. About 14 hours later it was still running, with these traits:

- it was listening on a local port;
- its working directory was in the run's scratch folder;
- its parent was process 1, and it led its own process group.

The developer and the reviewer could not stop it from inside the sandbox, and ending the run did not stop it. A server left holding a port can make a later run's tests fail for a reason that has nothing to do with that run's change.

Today the harness ends a session by ending its process group. A process that leaves the group, as this one did, is out of reach. The developer's question asks how the harness can tell which processes outside the group are the run's.

## Decision 1: a run's processes carry its identifier

The harness puts `YOYODYNE_RUN_ID`, set to the run's identifier, in the environment of every developer session, every reviewer session, and every check it runs for that run. A process passes its environment to the processes it starts unless it is made to clear it. A server started by a test started by a shell keeps the identifier after it leaves the group and after its parent ends.

The identifier is the run's own, so two runs never share one, and attempts within one run share it on purpose.

## Decision 2: what counts as the run's, and what is only named

The harness lists the processes of the user it runs as. It never looks at another user's processes, at process 1, or at the harness's own process and the processes it descends from.

For each process it reads the environment the process started with. On macOS this is the kernel's argument and environment record for the process. On Linux it is the process's environ file.

A process is the run's, and is stopped, when either of these holds:

- its starting environment carries this run's identifier;
- it holds the file the harness already hands a launch to tell whether anything from it still lives.

A process whose working directory is inside the run's worktree or scratch folder, but which shows neither, is **never stopped**. It is named on the run and on `yoyo status`, with the words "not stopped: it carries no sign that this run started it". This is the case of a process that cleared its environment, and also the case of a person's own shell opened in the worktree.

**Stopping the wrong process is the worse mistake.** It can destroy a person's work, or another run's, and nothing brings that back. A process missed is named and can still be stopped, so a miss costs a port or some memory until someone acts.

## Decision 3: when it runs

1. **When a developer or reviewer session ends**, before the harness starts the run's next step. Nothing of that session should be running while the checks run.
2. **When the run ends, however it ends**: approved, failed, stopped, kept for a continuation, or paused for a move onto a new build. A run kept for later starts fresh sessions when it goes on, so nothing left from before is of use to it.
3. **On the scheduler's start and on every maintenance pass.** These cover a harness that stopped or crashed between a session's end and the stop. The pass finds every process carrying a run's identifier, of any run. It stops one only where that run's record shows no session and no check going now.

A process is never stopped while a session or check of its own run is going. The code that ends the session or the run makes the first two calls itself, before it lets go of the run.

## Decision 4: how a process is stopped

1. When it finds a process, the harness records its number and the time it started.
2. Before every signal, it reads the start time again. If the number now belongs to a process started at a different time, that process is left alone.
3. It sends the stop signal. Where the process leads a process group, the signal goes to that group, and only to the members that also carry the identifier.
4. It waits up to 10 seconds, then sends the kill signal and waits up to 5 more seconds.
5. A process that is gone is stopped.

The harness runs outside the developer's sandbox, so the sandbox that stopped the developer and the reviewer does not stop it.

## Decision 5: a process that could not be stopped is named

A process the harness could not stop is written into the run's record with:

- its number;
- its command;
- every address and port it listens on, read with `lsof`;
- why it could not be stopped.

The same record is kept for a process named under Decision 2. `yoyo status` shows each one under its run, for example:

```text
a process this run started is still running: process 4242, java, listening on 127.0.0.1:25565; the harness could not stop it: permission refused
```

Every maintenance pass tries again and clears the line once the process is gone. A process still running after three passes files one report to the development manager, naming the run, the process and the port. A named process that carries no sign of the run is shown and never reported, because only a person can say whether it is theirs.

## Where it lives

The search and the stop belong to `internal/execution`, the package the provider adapters hand each command to. It already starts the commands for both providers, so one owner both marks a process and stops it. The code that reads another process's environment and working directory is split by operating system.

The orchestrator calls it at the end of a session and of a run, and the maintenance pass calls it as point 3 says. Neither keeps a list of its own.

The code that starts and signals processes today was not read for this design. A builder who finds the start of processes elsewhere than `internal/execution` puts the search beside it, and stops and asks the architect if the two are in different places.

## Not covered here

Checks the merge queue runs on a candidate belong to a queue entry rather than a run. They are left as they are. The same rule can later mark them with the entry's identifier, as a separate item.

## Alternatives rejected

- **The file handed to a launch, alone.** The reported server never held it: the shell tool in a Claude Code session does not pass it on.
- **The working directory, alone.** It would have caught the reported server. It would also stop a person's shell opened in the worktree, and miss a server that moves to the root folder.
- **Watching the process tree while the session runs.** A server that starts itself in the background passes to process 1 between one look and the next.
- **Process groups alone.** That is how this server got away.
- **Grouping the processes in the kernel.** macOS has nothing like Linux's control groups.

## Build pieces

This is one piece, expected at about 400 to 600 changed lines with tests:

- the identifier in the environment;
- the search for each operating system;
- the stop of Decision 4;
- the calls at the end of a session and of a run, and from maintenance;
- the record and status line of Decision 5.

If it is expected to pass about 1,200 lines, the development manager splits off the maintenance pass and the report into a second piece.

## What a reviewer can check

- A test session starts a child that leaves the run's process group and its session and keeps listening on a port. When the run ends, the child is gone.
- A process with its working directory in the worktree and no identifier is named on the run and on status, and is still running afterwards.
- A process number reused by a process with a different start time is not signalled.
- A process that ignores the stop signal is gone after the kill signal.
- A process that cannot be signalled is shown on status with its number and port.
- A harness stopped between a session's end and the stop finds the process on its next start or maintenance pass and stops it. A process of a run with a session still going is left alone.
- Another run's process, which carries that run's identifier, is untouched.
- make test passes.