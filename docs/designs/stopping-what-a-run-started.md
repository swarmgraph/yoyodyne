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
    - action: amended
      by: architect
      at: 2026-11-27T23:57:41.876902Z
      reason: 'Answers the helper check (yoyodyne-ifd.428.233), reviewer report d485972e: the design never said when a leftover process is still used. A marked process holding an open local connection to a process the same stop would leave alone, or whose other end cannot be found, is left running and shown as in use; a listening port with nothing connected is not use, and connections are read again before every signal. It applies to every stop and needs a build piece of its own, since the scheduler start piece (428.218.1) was approved without it.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-27T17:56:47.39387Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4256, for document-4256.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-27T23:57:41.876902Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4298, for document-4298.1 under the automatic approval policy
---
# Stopping every process a run started when the run ends

This design answers the item on stopping every process a run started (yoyodyne-ifd.428.218). It serves the goal that implementation tasks are isolated in worktrees the harness manages: a run's work must not outlive the run and reach into later runs. Decision 6, on a process that something still alive is using, answers the item on the helper check (yoyodyne-ifd.428.233).

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

A process is the run's, and is stopped unless Decision 6 leaves it running, when either of these holds:

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
2. Before every signal, it reads the start time again. If the number now belongs to a process started at a different time, that process is left alone. It also reads the process's connections again, and leaves it running if Decision 6 now holds.
3. It sends the stop signal. Where the process leads a process group, the signal goes to that group, and only to the members that also carry the identifier and are not left running by Decision 6.
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

## Decision 6: a process something still alive is using is left running

The case that matters is a build server or other helper a run started, which another run, a person, or the harness itself has connected to since. Stopping it would break work that is not this run's.

Before any stop under Decision 3, whichever of its three points calls it, the harness reads each found process's open local connections with `lsof`: connections over 127.0.0.1 or the machine's other loopback addresses, and connections over a socket file. For each one it finds the process at the other end.

A found process is **in use, and left running**, when any of its open local connections leads to:

- a process this stop would leave alone: another run's process while that run has something going, a person's process, or the harness's own; or
- a process the harness cannot find, or a connection whose other end it cannot read.

The second case leans toward leaving the process running for the same reason as Decision 2: stopping the wrong process is the worse mistake.

These are **not** use:

- a port the process listens on with nothing connected;
- a connection to another process the same stop is stopping.

Connections are read when the process is found and again before every signal, as Decision 4 says, so a connection made between one look and the next keeps the process running.

An in-use process is written into the run's record and shown on `yoyo status` under its run, naming the process at the other end, for example:

```text
a process this run started is still running: process 4242, gradle, left running because process 5150 (java, run run-1a2b3c4d) is connected to it
```

Being in use is not a fault, so it never counts toward the three passes of Decision 5 and files no report. Every maintenance pass checks it again. Once nothing that would be left alone is connected, that pass stops it and clears the line.

## Where it lives

The search and the stop belong to `internal/execution`, the package the provider adapters hand each command to. It already starts the commands for both providers, so one owner both marks a process and stops it. The code that reads another process's environment, working directory and connections is split by operating system.

The orchestrator calls it at the end of a session and of a run, and the maintenance pass calls it as point 3 of Decision 3 says. Neither keeps a list of its own.

The code that starts and signals processes today was not read for this design. A builder who finds the start of processes elsewhere than `internal/execution` puts the search beside it, and stops and asks the architect if the two are in different places.

## Not covered here

Checks the merge queue runs on a candidate belong to a queue entry rather than a run. They are left as they are. The same rule can later mark them with the entry's identifier, as a separate item.

## Alternatives rejected

- **The file handed to a launch, alone.** The reported server never held it: the shell tool in a Claude Code session does not pass it on.
- **The working directory, alone.** It would have caught the reported server. It would also stop a person's shell opened in the worktree, and miss a server that moves to the root folder.
- **Watching the process tree while the session runs.** A server that starts itself in the background passes to process 1 between one look and the next.
- **Process groups alone.** That is how this server got away.
- **Grouping the processes in the kernel.** macOS has nothing like Linux's control groups.
- **Leaving every marked process running while it listens on a port.** Every leftover test server listens, so nothing would ever be stopped, and the reported server would still be running.
- **Stopping an in-use process and letting its users start another.** A build another run has under way would fail for a reason that has nothing to do with that run's change, which is the harm this design exists to prevent.

## Build pieces

The first piece, already approved and split into pieces, covers:

- the identifier in the environment;
- the search for each operating system;
- the stop of Decision 4;
- the calls at the end of a session and of a run, and from maintenance;
- the record and status line of Decision 5.

Decision 6 was ruled after those pieces were filed, so it is a further piece: reading each process's local connections and the processes at their other ends for each operating system, leaving an in-use process running, reading connections again before every signal, and its record and status line. It is expected at about 200 to 400 changed lines with tests. Where the scheduler start and maintenance piece (yoyodyne-ifd.428.218.1) has not started, the development manager may fold it in, provided the total stays under about 1,200 lines.

## What a reviewer can check

- A test session starts a child that leaves the run's process group and its session and keeps listening on a port. When the run ends, the child is gone.
- A process with its working directory in the worktree and no identifier is named on the run and on status, and is still running afterwards.
- A process number reused by a process with a different start time is not signalled.
- A process that ignores the stop signal is gone after the kill signal.
- A process that cannot be signalled is shown on status with its number and port.
- A harness stopped between a session's end and the stop finds the process on its next start or maintenance pass and stops it. A process of a run with a session still going is left alone.
- Another run's process, which carries that run's identifier, is untouched.
- A marked process of a run with nothing going, with an open local connection from a process that carries no identifier, is left running and shown on status naming that process. After that connection closes, the next maintenance pass stops it.
- A marked process that only listens, with nothing connected, is stopped.
- A connection made after the process was found but before the signal keeps it running.
- make test passes.
