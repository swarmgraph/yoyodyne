---
id: what-each-invocation-was-sent
kind: design
title: What each developer and reviewer invocation was sent, kept whole and read back exactly
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-28T02:43:36.362467Z
      reason: 'yoyodyne-ifd.428.126.1.2: the operator asked on October 27 that each run keep exactly what each invocation was sent. Rules whole text over a rebuild record, each distinct part stored once by digest, written by the run store before the provider starts, and read back exactly by a command. Written from reads at main 18b3d3d5.'
    - action: amended
      by: architect
      at: 2026-11-22T18:52:10.167083Z
      reason: 'amendment-6e188caa, approved Nov 22: an adapter is handed a recorder with each request and starts the provider only after the record succeeds, sending exactly the bytes recorded, in place of a start that takes a recorded invocation as its argument, which cannot exist because both adapters compose the final text themselves. The check list, the invariant section and the rejected alternatives follow. Written from main a767be20b.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-28T02:43:36.362467Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1880, for document-1880.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-22T18:52:10.167083Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3517, for document-3517.1 under the automatic approval policy
---
# What each invocation was sent

## What this is for

Serves the goal of a traceable chain from the brief to verification. On October 27 the operator asked that each run keep exactly what each of its invocations was sent, readable from the run (yoyodyne-ifd.428.126.1.2). Today a person can read each part of a developer's instructions in the repository, but no record keeps the whole text an invocation received. The parts that are not in the repository have usually changed by the time anyone looks: the work item and its notes, the findings a repair is shown, the change a reviewer reads.

## What it rests on, and what was not read

Read whole at commit 18b3d3d5:

- the item;
- [the design for guarantees the whole system relies on](one-owner-for-each-guarantee.md).

Not read:

- the code that puts a developer's or reviewer's instructions together;
- the provider adapters;
- `internal/runstate`;
- the command help;
- the operations guide.

The build item reads that code first. If the harness puts instructions together in more than one place, the item lists each place and moves it onto the way in below. If it finds a part this design does not name, it records the part by the same rule and says so in its closing note.

The revision of November 22 follows what the developer building the record found: both adapters put the final text together themselves, so a recorded invocation cannot exist before the adapter runs. How the record is required changed to match; what is recorded did not.

## Decided: the whole text, each distinct part stored once

Each invocation keeps the whole text it was sent. A record from which the text could be rebuilt is not enough.

A rebuild is exact only if every part is pinned. A part in the repository can be pinned by its path and commit. Many parts are not in the repository:

- the work item and its notes as they were read;
- the agent's memory;
- check output and review findings;
- the change under review;
- the harness's own contract, which is built into the program.

Pinning those means keeping their text anyway. A rebuild would also depend on later code putting the parts together exactly as the code that sent them did, and a later build may not.

What it costs:

- Each part is stored once, compressed, named by the digest of its content. The contract, persona and invariants that many invocations share are stored once.
- What is new for each invocation is mostly the work item, its notes, the findings, and for a reviewer the change.
- The size has not been measured. The build item measures the store on this repository for one week and reports it in its closing note. No limit is set before that.
- All history is kept, as guarantee 8 of the guarantees design says. Pruning, if the measured size ever calls for it, is a later ruling and no part of this one.

## What counts as sent

- Every message the harness passes to the provider for that invocation, in order: the system instructions, the first message, and any later message in the same invocation, such as a second ask to a reviewer whose verdict could not be read.
- The settings that shape it: provider, model, effort, the tools allowed, the working directory and the commit it was at, and the version of the provider's program.
- A repair or continuation that starts a new provider session is a new invocation with its own record.

Not counted: what the provider's own program adds by itself, such as its built-in instructions and files it loads from the checkout on its own, like `CLAUDE.md`. The harness cannot see those. The record names the program's version and the commit, so a reader can find the files, and the command that shows the text says plainly that the provider's own additions are not in it.

## Where it is kept, and who writes it

Owner: the run store in `internal/runstate`, under guarantee 1 of the guarantees design.

- The parts' text is kept in a store in the harness's state directory, one file for each distinct digest, written through the confined writer that [repository confined writes](repository-confined-writes.md) describes.
- The run record gains one entry for each invocation, written by one narrow update that records an invocation. The entry holds:
  - the invocation's number, its role (developer or reviewer), and when it started;
  - the settings above;
  - the parts in order, each with its name (such as contract, persona, invariants, work item, notes, findings, change under review), its digest, its size, and where it came from: a repository path and commit, a tracker item and the time it was read, or the harness build;
  - the digest of the whole text.
- The harness hands the adapter a recorder with each request. The recorder is the run store's way in for this record.
- The adapter puts the final text together, as it does today. Before it starts the provider, it hands the recorder the parts in order, the joined text and the settings. The recorder writes every part and the entry, and returns.
- The adapter starts the provider only after the recorder returns without an error. It sends exactly the bytes it handed the recorder, never text put together a second time afterwards.
- In the live wiring every request carries a recorder. A test fails if the live wiring of any adapter lacks one.
- If the entry or a part cannot be written, the invocation is not started. The run stops, with that reason in ordinary words. The cause is outside the work, so no repair attempt is spent, and the stop is handled as [recoverable and terminal failures](recoverable-and-terminal-failures.md) handles a state write that fails.

## Reading it back

- A command beside the existing run commands lists a run's invocations with their parts and sizes, and prints the exact text of any one invocation. It checks the digest of the whole text as it reads. A part that is missing or does not match is said plainly, and never filled in. The command's name is the development manager's to choose; its help says in ordinary words what it shows.
- The tool that inspects a run shows the list of invocations and their parts, not the text.
- The text stays on this machine, with the same file permissions as run records. It is never copied into tracker notes, Slack or the dashboard. It is shown exactly as sent, because a cleaned copy would not be what was sent. A secret found in it is a defect in what the harness sent, which this record makes findable.

## Scope

Developer and reviewer invocations, as the item asks. Management conversation turns are not covered. The same record could serve them later; that is not ruled here.

## What a reviewer can check

- A test with a stand-in provider shows that the bytes the provider received equal what the command prints, for every invocation. It covers a second message in one invocation, and a repair that starts a new invocation. This is what catches an adapter that sends text other than what it recorded.
- A part shared by two invocations is stored once.
- A recorder that fails leaves the provider unstarted, stops the run, and spends no repair attempt.
- A test fails if the live wiring of any adapter lacks a recorder.
- `make test` and `make race` pass.

## Alternatives rejected

- **A record to rebuild the text, alone.** It is not exact for parts outside the repository, or once the code that puts parts together changes.
- **The whole text inside the run record.** Every record would grow large and every routine read would slow, against guarantee 8, and identical parts would be repeated.
- **The text in tracker notes.** Notes are bounded, and the tracker store's growth is already being measured (yoyodyne-ifd.428.126.4.1).
- **Writing the record when the invocation ends.** A run that stops partway would leave nothing.
- **An adapter start that takes the recorded invocation as its argument,** so an unrecorded invocation would not compile. This design first ruled it. Both adapters put the final text together themselves, so the recorded invocation cannot exist before the adapter runs; building it would mean moving that work out of every adapter and rewriting every adapter test. The recorder, the test on the live wiring, and the byte equality test give the same protection.

## Invariant

None. The run store owns the record, and the recorder on every request, with the tests above, guards it. The wording proposed in the guarantees design, against copies of a guarantee's rule, already covers a second place that puts instructions together.