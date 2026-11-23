---
id: developer-runs-and-local-services
kind: decision
title: No role the harness starts reaches a service the harness runs for itself, while a developer run's own tests keep their local servers
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-22T20:25:39.847715Z
      reason: 'yoyodyne-ifd.428.126.4.32: rules that a developer run never reaches a service the harness runs for itself. Services listen on a socket file in a folder every developer sandbox denies, or on 127.0.0.1 with a credential kept only there. Local listening for tests stays on. The two parked server mode pieces may be released once the folder lands, with conditions. Four build pieces are named.'
    - action: amended
      by: architect
      at: 2026-11-23T23:33:28.386088Z
      reason: 'A developer''s warning (report b803259a, from the local services folder build, yoyodyne-ifd.428.126.4.33) asked whether the denial covers every role. It does: a reviewer or management role that reads the password could repeat it. Adds build piece 5, says piece 1 is built, holds a tracker server that needs a password (not one on a socket file) until piece 5, and moves the standing rule to after piece 5. Codex adapter read whole at main 1998757b3508; the Claude Code adapter was not read.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-22T20:25:39.847715Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3537, for document-3537.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-23T23:33:28.386088Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3763, for document-3763.1 under the automatic approval policy
---
# No role the harness starts reaches a service the harness runs for itself, while a developer run's own tests keep their local servers

## What was asked

The measurement of the tracker's server mode (yoyodyne-ifd.428.126.4.28) found two things, reported by its developer and its reviewer:

- every Claude Code developer run can connect to any port on this machine's own address, 127.0.0.1, including ports opened by programs outside the run, because the developer settings let a sandboxed command listen on that address (`allowLocalBinding`, in the Claude Code adapter);
- a run can read the harness's state folder, where a local server's password would be kept.

So any service on this machine, such as the dashboard or a tracker server, is protected only by its own password or token, and the token may be readable. Server mode was fast enough (median one item read 0.10 seconds, slowest listing 0.25 seconds, under load), so this decision alone settles whether its two parked pieces are ever released: setup installing dolt (yoyodyne-ifd.428.126.4.29) and the supervisor running bd's server (yoyodyne-ifd.428.126.4.30).

This decision was first made for developer runs only. The developer who built the folder (yoyodyne-ifd.428.126.4.33) then reported that a reviewer or a management role on Codex reads every file on the machine, so it could still read a password kept there. The same rule now covers every role.

This serves the goal that safety invariants hold whatever the configuration says. A role reaching programs the harness runs for itself is a gap in the boundary that keeps agents inside the harness.

## What was read, and what was not

Read at main caed7e958 for the first version: the item and the two reports it covers, [repository-confined writes](../designs/repository-confined-writes.md), and [one owner for each guarantee](../designs/one-owner-for-each-guarantee.md).

Read at main 1998757b3508 for this version: the Codex adapter, `internal/backend/codex/backend.go`, whole. Its developer sessions are given the services folder with the access `deny`. Its sessions for every other role are given no such entry and may read the whole machine. Every Codex session has its network switched off, which its comments say refuses a socket file too.

Not read: the Claude Code adapter's settings for a reviewer, a conversation or a helper session, the dashboard's code, and whether bd and dolt can use a socket file. Each build piece below reads its part first, and where the code differs from what is said here, the piece stops and the architect is asked.

## The decision

No session the harness starts for a role reaches a service the harness runs for itself: not by connecting to it, and not by reading the credential or the socket file it would need to connect. This covers a developer run, a reviewer, a conversation or recurring pass of any management role, and any helper session started on a role's behalf. The boundary is drawn at the harness's own services, not at every port on this machine.

1. **One folder holds what a service needs to be reached.** The harness keeps one folder under its state directory, `services/`, created with access for its owner only. Every socket file a harness service listens on, and every password or token a harness service checks, lives there and nowhere else.
2. **Every session that can read files or run commands refuses that folder.** For each provider, the settings the harness writes for such a session deny reading any file under `services/` and deny connecting to any socket file under it. A session given no tools that read files or run commands meets this without a setting of its own. This is a rule the adapter writes; no configuration removes it, and a provider that cannot hold a role to it does not fill that role until it can.
3. **A harness service listens in one of two ways, and no other.** Either it listens on a socket file in `services/`, which no session can open; or it listens on 127.0.0.1 only and refuses every connection without a credential kept only in `services/`. A socket file is preferred wherever the program supports one. A service listening on any other address, or on 127.0.0.1 with no credential, is a defect.
4. **A developer run's own tests keep their local servers.** The setting that lets a sandboxed command listen on 127.0.0.1 stays on for developer runs. A test that starts a server on a port the system chooses, and connects to it, works as before.
5. **Other programs the person runs on this machine are not the harness's to protect.** A database or web server the person started for their own work is reached or not as their own sandbox settings say. This decision covers what the harness itself starts.

The folder and the denial for developer runs are built (yoyodyne-ifd.428.126.4.33). The denial for the other roles is build piece 5, not yet built.

## Why the boundary is drawn there

On macOS, the sandbox a run works in cannot tell a port the run's own test opened on 127.0.0.1 from a port another program opened. Both are connections to the same address, and the test's port is chosen by the system at the moment it starts. So refusing every connection to this machine's own address would also refuse every test that starts a local server, which this repository's tests and most projects' tests do.

What the sandbox can refuse reliably is a file path. A socket file is reached through its path, and a credential is read through its path, so a folder the sandbox denies keeps both out of reach whatever port numbers are in use.

**Why every role.** A reviewer or a management role has no more business with these services than a developer has. One that cannot connect can still read a password and repeat it in a review, a report or a note, where a later developer run would be handed it.

## Alternatives rejected

- **Turning off local listening for developer runs.** It closes the gap and breaks every test that starts a local server.
- **A separate network for each run.** Linux can give a run its own network with its own 127.0.0.1. macOS, where this harness runs today, cannot. It is recorded as a later strengthening for Linux, not as the answer.
- **A password alone, kept in the readable state folder.** That is the gap the measurement found.
- **A fixed range of ports for each run, with the sandbox allowing only those.** Tests that ask the system for any free port get one outside the range.
- **Leaving server mode parked for good.** It is fast enough, and the gap exists for the dashboard already, so refusing server mode would not close it.
- **Denying the folder to developer runs only, because other roles do not write code.** The password could still be read and passed on in what those roles write.
- **Holding every piece until the other roles are denied too.** A socket file needs no password, and moving the dashboard only narrows what is open today, so neither has a reason to wait.

## What becomes of the two parked server mode pieces

Both may be released now that build piece 1 below has landed. The supervisor running bd's server (yoyodyne-ifd.428.126.4.30) also carries these conditions, which its reviewer checks:

- the piece first reads whether bd's server and dolt can listen on a socket file and whether bd can connect through one. If both can, the server listens only on a socket file in `services/`;
- if either cannot, the server listens on 127.0.0.1 only, with a password the supervisor creates, readable by its owner only, in `services/`, and refuses every connection without it. In that case the server is not switched on until build piece 5 has landed, because until then a role other than a developer could read the password;
- in either case the harness reaches the server only through its tracker client, as before, and developer runs still never run the tracker's command;
- if neither condition can be met, server mode stays off, and the piece records which condition failed on the item.

Setup installing dolt (yoyodyne-ifd.428.126.4.29) needs nothing more from this decision.

## Build pieces

Each is bounded and verified on its own. Turning them into work items and their dependencies is the development manager's; their order is the Lead Product Manager's.

1. **The folder and the denial for developer runs.** Built (yoyodyne-ifd.428.126.4.33). The harness creates `services/` under the state directory with access for its owner only, and the settings for a developer run on each provider deny it. The live check that a developer run is refused is a separate item (yoyodyne-ifd.428.126.4.38).
2. **The dashboard.** The piece reads how the dashboard listens and whether its acts on the operator's behalf require a credential. It then moves the dashboard to one of the two ways in point 3 of the decision, with any credential in `services/`. An act on the operator's behalf that a request without the credential can take is fixed in this piece. It does not wait for piece 5.
3. **A list of every listener the harness opens.** A check in `make test` lists each file, not a test, that opens a network listener or a socket file. The list is kept in the repository and may only shrink: a file added to it fails the check unless the change also names, in the list, which of the two ways in it uses. This is the same kind of check [one owner for each guarantee](../designs/one-owner-for-each-guarantee.md) uses.
4. **Server mode's conditions.** They are carried by the supervisor running bd's server (yoyodyne-ifd.428.126.4.30), as written above, not by a new item.
5. **The denial for every other role.** The Codex adapter names the services folder with the access `deny` for every session, starting with `servicesDenied` and `profileArgs`. A conversation is not a developer run and has no run folder, so the piece first reads how such a session is to find the folder. The Claude Code adapter does the same for every session that can read files or run commands, after the piece reads which can. A session whose folder cannot be found is not started, as for a developer run today. Tests for each provider show the denial for a developer, a reviewer, a management conversation and a helper session, or show that the session holds no tool that reads files or runs commands.

## What a reviewer can check

- No harness service listens anywhere but a socket file in `services/` or 127.0.0.1 with a credential kept only in `services/`.
- The settings written for every developer run, on every provider, deny reading and connecting under `services/`.
- Once piece 5 lands, the same holds for every session that can read files or run commands, whatever its role.
- A developer run's own test that starts a local server on a port the system chooses still passes.
- The list of listeners only shrinks.

## A standing rule to follow

A service added later, by a change that never mentions this decision, could open a port with no credential. That is the case a standing rule exists for, so the architect records it as an invariant once build piece 5 lands, worded from what was built for every role.
