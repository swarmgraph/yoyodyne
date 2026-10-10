# yoyodyne-ifd.433.35: why open tracker items carry an assignee nobody set on purpose

Three items carry Mason Bryant as assignee though nobody says he is working on
them: the tracker-write retry fix (yoyodyne-ifd.433.22), re-classifying a run
that stops again after a repair (yoyodyne-ifd.428.74), and tool interface
slice 1 (yoyodyne-ifd.430.19). The development manager's reading was that the
assignee reads as a claim, so nothing pulls these items. This diagnosis checks
that against the harness's code and the recorded history.

All times are Pacific (PDT). The tracker export read is the one copied into
this run's worktree on October 9 at about 22:47; the run records and the
scheduler's log are the harness's own, read at the same time.

## The answer

- **Every claim the harness makes sets the assignee, and nothing the harness
  does clears it.** The harness claims an item with `bd update <id> --claim`
  (`Client.claim` in `internal/beads/client.go`). `bd` documents that flag as
  setting the assignee "to you" and the status to in progress, and "you" is
  `$BEADS_ACTOR`, then the checkout's `git config user.name`, then `$USER`. The
  harness sets no actor, so the name is this machine's Git identity, Mason
  Bryant. Every write that gives an item back — `Release`, `Reopen`, `Unblock`,
  and the clear inside `claimPastStaleBlock` — sets `--status=open` and appends
  a note, and none of them touches the assignee. A run that is cancelled, fails,
  or lands evidence therefore leaves the assignee exactly where its claim put it.
  This is not specific to cancelled runs: it is every run.
- **The assignee does not keep an item out of the queue.** The harness reads
  ready work with `bd ready` (`Client.Ready`), which leaves out items that are in
  progress, blocked, or deferred, and leaves in items that merely have an
  assignee; it would leave them out only if asked with `--unassigned`, and the
  harness never asks. No code in the harness's selection reads the assignee: the
  one place it appears in `internal/orchestrator` is the fingerprint that notices
  an item changed. The scheduler's own log lists the retry fix as one of the
  items "the tracker reports as ready" on pull after pull, and 240 items, 44 of
  them still open, were claimed again by a later run while already carrying it.
- **What actually held the three items back is something else in each case**,
  and none of it is the assignee. See [the three named items](#the-three-named-items).
- **Where the assignee does mislead is in what the roles read.** The tracker
  view the conversations are given prints `assignee: Mason Bryant` on any item
  that has one (`renderWorkItemHead` in `internal/chat/tracker.go`), with nothing
  saying a run's claim put it there or that selection ignores it. That bare line
  is what the development manager and the Lead Product Manager read as a person's
  claim.

## Which writes set an assignee

| Write | Code | Effect on the assignee |
| --- | --- | --- |
| Claim at the start of a run | `Client.claim`, called by `Client.Claim` | sets it to the `bd` actor: Mason Bryant |
| Claim after clearing a stale blocked status | `claimOnConfirmedClear` | the same, through `Client.claim` |
| Give an item back after a run that ended | `Client.Release` (the claim audit), `Client.Reopen` (evidence landings and raises) | none; the status goes to open and the assignee stays |
| Clear a stale blocked status | `Client.Unblock`, the clear in `claimPastStaleBlock` | none |
| Block, record an outcome, cost, landing, origin | `Client.Block`, `RecordOutcome`, `RecordCost`, `RecordLanding`, `RecordOrigin` | none |
| Close | `Client.Complete` | none; the closed item keeps it |
| Create | `Client.Create` | none; a new item has no assignee |

No harness code passes `--assignee`, `--actor`, or `BEADS_ACTOR`. A person
running `bd update --claim` or `bd create --assignee` by hand would set it too,
and the record cannot tell that apart from a run's claim except by whether a run
record shows a claim at the same moment.

## The whole tracker agrees

Of the 1,144 items in the export:

| | Items |
| --- | --- |
| Have at least one run record, and carry the assignee | 890 |
| Have at least one run record, and carry no assignee | 0 |
| Have no run record, and carry no assignee | 234 |
| Have no run record, and carry the assignee | 20 |

The last 20 are nineteen closed items claimed between August 15 and October 2,
nine of them before the earliest run record (August 15 at 10:24), and the top-level epic (yoyodyne-ifd), claimed on
August 17 at 15:30 with no run. Those are the only assignees a person can have
set.

For every one of the 85 open items whose assignee came from a run, the time the
tracker records the item first moving to in progress is within seven seconds
of the claim time on one of that item's run records. That is the claim setting
the assignee, item by item.

## The three named items

**The tracker-write retry fix (yoyodyne-ifd.433.22).** Its thirteen runs, on
October 1 from 13:02 to 18:55, were each refused within five
seconds because the provider's seven-day usage limit was exhausted until
October 3 at 20:00, past the six-hour longest pause. That is why each ended
almost at once with nothing kept; it left the assignee from the first claim.
From then until the evening of October 9 the scheduler did read it as ready,
and held it back at every pull because it would race a run already in flight
over a shared file: almost always run-7d92c67b, on holding a check stopped by
its own time limit (yoyodyne-ifd.428.37), over `internal/orchestrator/triage.go`.
That run's process stopped writing on October 2 at 07:02 and never came back,
but its record still said running, so every pull counted it as in flight until
the claim audit ended it on October 9 at 21:24. Top-priority work could not go
ahead of it either: the rule that lets a P0 item start beside a run over less
urgent work (`outranks` in `internal/orchestrator/conflict.go`) was only
committed with the fix for P0 items not being selected (yoyodyne-j2u) at 18:27
the same evening. The question of whether that run still held a slot and the file is
already admitted as yoyodyne-7mk. With the run ended and the rule in place,
nothing found here holds the retry fix back any more.

**Re-classifying a run that stops again after a repair (yoyodyne-ifd.428.74).**
The tracker gives it priority 1; the work item for this diagnosis calls it P0.
Its run run-9b58b617 was stopped by the development manager on October 4 at
22:14 with the change preserved, and the claim audit gave the item back on
October 4 at 23:16. Since then the scheduler has passed it over at every pull
(58 times in the log) because "it is held for a decision nobody has made": the
stop preserved a change and nobody has yet decided what happens to it. The
assignee plays no part.

**Tool interface slice 1 (yoyodyne-ifd.430.19).** Its only run, run-69bf6614,
claimed it on October 4 at 07:30 and raised it as not meetable as it stands.
The item is parked until the log-reading access reconciliation
(yoyodyne-ifd.430.19.1) lands, and the scheduler says so. The assignee is the
run's claim.

## What would fix the misreading

Not done here, because changing how claims work and clearing assignees are both
out of scope for this item:

1. Say what the assignee is wherever a role reads it. The tracker view in
   `internal/chat/tracker.go` either leaves the assignee out, or prints it as
   the identity a run's claim was made under, beside the status that says
   whether anything is working on the item now.
2. Optionally, make the assignee mean "being worked on now": the writes that
   give an item back (`Release`, `Reopen`, `Unblock`) also clear it. Whether
   `bd update` clears an assignee given an empty value needs checking against
   `bd` 1.1.2 first. This is a change to how claims work, so it is for the Lead
   Product Manager to admit or decline.

## Every open item that carries an assignee

86 items that are not closed carry Mason Bryant as assignee: 59 open, 18
blocked, and 9 in progress. "Assignee set" is when the tracker records the item
first moving to in progress, which is the claim. "Set by" is the run whose
recorded claim matches that time, or a person where no run record does. "Where
it stands" is as the export and run records read on October 9 at about 22:47.

| Item | Status | Priority | Assignee set (PDT) | Set by | Runs | Where it stands |
| --- | --- | --- | --- | --- | --- | --- |
| When a role's own scheduled pass keeps failing, the finding goes to... (yoyodyne-ifd.428.76) | in progress | P0 | Oct 5 06:18 | run 88804f0e | 1 | last run failed |
| A recurring pass records which conversation items it was given and... (yoyodyne-ifd.430.51) | in progress | P0 | Oct 9 21:28 | run a4cff637 | 1 | run a4cff637 in flight |
| A confirmed document whose landing run found every developer slot b... (yoyodyne-ifd.433.34) | in progress | P0 | Oct 9 12:53 | run b22bc15f | 2 | run 63bcff61 in flight |
| The Codex adapter launches runs under a Codex permission profile th... (yoyodyne-ifd.435.27) | in progress | P0 | Oct 7 20:24 | run e389ddbd | 1 | run e389ddbd in flight |
| Build Yoyodyne v1 through early self-hosting (yoyodyne-ifd) | in progress | P1 | Aug 17 15:30 | a person (no run record) | 0 | epic, no run |
| The readiness check does not hold an item for citing a name from a... (yoyodyne-ifd.428.93) | in progress | P1 | Oct 9 19:22 | run e8d09ff2 | 1 | last run succeeded |
| A check that fails after a review keeps the reviewer's findings on... (yoyodyne-ifd.429.68) | in progress | P1 | Oct 9 14:43 | run 5d62f75e | 1 | last run succeeded |
| A program manager's pass opens with the read model and the other in... (yoyodyne-ifd.430.13.8) | in progress | P1 | Sep 28 11:28 | run 008b0e25 | 1 | last run succeeded |
| Find what leaves an assignee on tracker items nobody is working on,... (yoyodyne-ifd.433.35) | in progress | P1 | Oct 9 22:47 | run abdb557c | 1 | run abdb557c in flight |
| When the scheduler passes over ready work or leaves a decided repai... (yoyodyne-37h) | blocked | P0 | Oct 5 00:00 | run 376a3748 | 2 | blocked |
| One role's scheduled pass does not wait behind another role's: pass... (yoyodyne-ifd.428.75) | blocked | P0 | Oct 5 04:50 | run 5d9005f6 | 1 | blocked |
| One ownership registry and resolver: every waiting entry's owner, r... (yoyodyne-ifd.432.25.1) | blocked | P0 | Sep 30 09:48 | run d4c31756 | 2 | blocked |
| An agent block's role: may name an activated role definition, and c... (yoyodyne-ifd.434.6) | blocked | P0 | Oct 3 01:08 | run 6049f65f | 2 | blocked |
| An exhausted provider endpoint receives no fresh dispatch until its... (yoyodyne-ifd.435.10) | blocked | P0 | Oct 1 21:55 | run 07feebbe | 1 | blocked |
| A Codex agent that sets no effort is invoked with none, so the Code... (yoyodyne-ifd.435.18) | blocked | P0 | Oct 5 10:31 | run 0d93564e | 1 | blocked |
| Developer-run sandboxes allow loopback binding, so the Codex native... (yoyodyne-ifd.435.20) | blocked | P0 | Oct 7 06:33 | run a55ad7d7 | 1 | blocked |
| Interactive sessions get the notes-writer guard: the uncovered loss... (yoyodyne-ifd.153) | blocked | P1 | Aug 23 14:09 | run 5035c832 | 2 | blocked |
| The development manager's docket never leaves out an entry: where i... (yoyodyne-ifd.428.82) | blocked | P1 | Oct 9 01:06 | run f835c861 | 1 | blocked |
| Connect the merge queue to the configuration switch and verify acti... (yoyodyne-ifd.429.59.6) | blocked | P1 | Oct 9 14:37 | run 83b41c71 | 1 | blocked |
| A reply with one malformed block loses only that block: the rest is... (yoyodyne-ifd.430.17) | blocked | P1 | Oct 1 12:58 | run fff8b12b | 14 | blocked |
| Any document in the product home may carry a revision marked consis... (yoyodyne-ifd.433.19) | blocked | P1 | Oct 1 13:01 | run dc97df55 | 14 | blocked |
| A role never sees a write as lost when it was not: a reprioritize w... (yoyodyne-ifd.433.25) | blocked | P1 | Oct 1 13:06 | run 7b5f5c13 | 14 | blocked |
| The maintenance job's remaining duties move into the product before... (yoyodyne-ifd.434.10) | blocked | P1 | Sep 27 20:28 | run 6fdcae7a | 1 | blocked |
| Shared confined writers preserve containment through filesystem rep... (yoyodyne-xdj) | blocked | P1 | Oct 3 14:34 | run 1953fb46 | 1 | blocked |
| configuration.md split tranche 4: reduce to the index, reconcile in... (yoyodyne-ifd.117.4) | blocked | P2 | Sep 24 11:25 | run 45cc3236 | 1 | parked |
| A governed-document defect fails its owner's gate, not every develo... (yoyodyne-ifd.174) | blocked | P2 | Aug 24 00:10 | run f28ebe44 | 1 | blocked |
| Agents speak in their threads through the sink, and answers land wh... (yoyodyne-ifd.68.25) | blocked | P2 | Sep 1 07:03 | run 7172270f | 1 | blocked |
| Each check's own time limit scales with machine load as the stage l... (yoyodyne-ifd.429.41) | open | P0 | Oct 1 12:55 | run a3788b2e | 14 | parked |
| Profile current orchestrator Git cost and identify bounded test red... (yoyodyne-ifd.429.45) | open | P0 | Oct 3 10:24 | run aeea1445 | 2 | parked |
| Tool interface, slice 1: the tool descriptor type, the registry mar... (yoyodyne-ifd.430.19) | open | P0 | Oct 4 07:30 | run 69bf6614 | 1 | parked |
| A tracker write that outlasts its per-call bound is checked for hav... (yoyodyne-ifd.433.22) | open | P0 | Oct 1 13:02 | run eeca6701 | 13 | last run cancelled |
| Run the existing architect pass hourly and verify scheduler adoption (yoyodyne-ifd.434.18) | open | P0 | Oct 2 15:30 | run 4aff5070 | 1 | parked |
| Apply developer-slot endpoint pairs during initial work, repair and... (yoyodyne-ifd.435.14.4) | open | P0 | Oct 7 14:12 | run 23809e0d | 1 | parked |
| A tracker status is never moved backwards without a note: find and... (yoyodyne-ifd.392) | open | P1 | Sep 18 22:50 | run 06b93741 | 1 | parked |
| Product 4 of 4: the dashboard is a supervised child, with its token... (yoyodyne-ifd.414) | open | P1 | Oct 1 12:57 | run 42f4f578 | 14 | last run succeeded |
| A check stopped by its own execution.check_timeout is held and cont... (yoyodyne-ifd.428.37) | open | P1 | Oct 1 12:59 | run 2085f5c7 | 14 | last run cancelled |
| The development manager's brake summons and the wake that corrects... (yoyodyne-ifd.428.60) | open | P1 | Oct 1 13:05 | run 0f36153f | 13 | last run cancelled |
| A wait recorded on a stopped-run entry takes that entry off the doc... (yoyodyne-ifd.428.71) | open | P1 | Oct 4 10:10 | run 12bb2725 | 1 | parked |
| A run that stops again after a decided repair was carried out is cl... (yoyodyne-ifd.428.74) | open | P1 | Oct 4 21:34 | run 9b58b617 | 2 | last run cancelled |
| An integration stop is classified from the error's cause, not its t... (yoyodyne-ifd.429.1) | open | P1 | Sep 21 20:07 | run de4a74ff | 2 | parked |
| The queued-merge check reads the forge's check state without failin... (yoyodyne-ifd.429.27) | open | P1 | Oct 1 12:58 | run 6f0b35eb | 14 | parked |
| The reviewer checks every change against the standing goals as well... (yoyodyne-ifd.429.34) | open | P1 | Oct 4 20:23 | run f6a0210e | 1 | parked |
| Build integration through a merge queue as designed: one switch, a... (yoyodyne-ifd.429.59) | open | P1 | Oct 5 03:05 | run 1167e267 | 1 | last run succeeded |
| Build the program manager as designed: the sixth role bundle, confi... (yoyodyne-ifd.430.13) | open | P1 | Sep 25 03:38 | run a9924c13 | 1 | parked |
| The factory-flow program manager post-mortems every stopped run: on... (yoyodyne-ifd.430.13.11) | open | P1 | Oct 2 22:13 | run ed681925 | 1 | parked |
| Every role's contract and shipped persona applies the standing goal... (yoyodyne-ifd.430.24) | open | P1 | Oct 4 19:38 | run f8cbeaa4 | 1 | parked |
| A pass that found something leaves a trace: a memory write, a lane... (yoyodyne-ifd.430.29) | open | P1 | Sep 29 07:30 | run e59006f4 | 15 | last run succeeded |
| Every role's contract and live persona says how to name the operato... (yoyodyne-ifd.432.25.4) | open | P1 | Oct 1 13:04 | run 54c9e611 | 14 | last run succeeded |
| Claude Code's usage-window readings are recorded, and the dashboard... (yoyodyne-ifd.432.26) | open | P1 | Oct 1 13:03 | run 871c1960 | 13 | last run cancelled |
| Every work item named to a person is shown as (priority, labels) ti... (yoyodyne-ifd.432.28) | open | P1 | Oct 1 13:04 | run 1b0c11be | 14 | parked |
| An admission records the goals relevant to a work item beside the o... (yoyodyne-ifd.433.12) | open | P1 | Oct 5 00:06 | run a336c4b9 | 1 | parked |
| A second terminal after a clean first terminal does not relaunch a... (yoyodyne-ifd.435.1) | open | P1 | Sep 22 23:53 | run 4d9f253e | 3 | last run failed |
| The Codex resume test is skipped inside a developer run's sandbox o... (yoyodyne-ifd.435.26) | open | P1 | Oct 8 01:08 | run 2dc7ceed | 1 | parked |
| The project's checks include one that runs the Codex resume test wi... (yoyodyne-ifd.435.28) | open | P1 | Oct 8 04:17 | run 78fd30cc | 2 | parked |
| The Codex adapter is tested against real recorded streams from the... (yoyodyne-ifd.435.7) | open | P1 | Sep 30 17:52 | run a10d7b1a | 2 | parked |
| A decided release is cut by the harness: maintenance validates revi... (yoyodyne-ifd.436.2) | open | P1 | Oct 1 12:57 | run a49f7a9e | 15 | parked |
| The coined vocabulary is replaced in printed strings, role contract... (yoyodyne-ifd.437.19) | open | P1 | Oct 1 13:01 | run 26d3f531 | 15 | parked |
| One pane of glass: yoyo chat is the entry point and the PM manages... (yoyodyne-ifd.130) | open | P2 | Aug 20 12:48 | run d1b1f813 | 1 | last run failed |
| The delivery workflow routes the 'reconciling' integrate outcome ba... (yoyodyne-ifd.209.31) | open | P2 | Oct 1 13:10 | run 54b4dc16 | 13 | last run failed |
| The development manager pre-judges the ready set: dispatch marks an... (yoyodyne-ifd.305) | open | P2 | Oct 1 13:06 | run 652a5d09 | 14 | parked |
| Probe Gemini/Antigravity and write the capability report (yoyodyne-ifd.341) | open | P2 | Sep 7 08:35 | run bc68d05f | 1 | parked |
| A goal has a stable identity, so amending its wording breaks nothing (yoyodyne-ifd.344) | open | P2 | Sep 7 10:08 | run 394c68c9 | 1 | parked |
| A parked item is never dispatched, whatever a status repair or a cl... (yoyodyne-ifd.428.36) | open | P2 | Oct 1 13:07 | run 61d81c57 | 13 | last run cancelled |
| A spent re-run claim is given back by a decision on the triage surf... (yoyodyne-ifd.428.41) | open | P2 | Oct 1 13:09 | run 6d7cb3e8 | 12 | last run cancelled |
| A re-run carry-out that loses its claim after reserving the fresh r... (yoyodyne-ifd.428.53) | open | P2 | Oct 1 13:08 | run 09e7dc66 | 13 | last run cancelled |
| internal/orchestrator's fakes of other packages' interfaces (tracke... (yoyodyne-ifd.429.13) | open | P2 | Sep 25 07:50 | run 8bcb69bc | 1 | parked |
| The in-package fakes and aliases in internal/orchestrator are remov... (yoyodyne-ifd.429.13.6) | open | P2 | Oct 1 13:06 | run 1387a57d | 15 | parked |
| Tracker-backed conformance tests fail rather than skip when the tra... (yoyodyne-ifd.429.32) | open | P2 | Oct 1 13:10 | run 2eebb61b | 13 | last run cancelled |
| Nine thirty-second budgets on real processes in the tracker and exe... (yoyodyne-ifd.429.36) | open | P2 | Oct 1 13:08 | run e078c028 | 13 | last run cancelled |
| A program manager that has never completed a pass says what its pas... (yoyodyne-ifd.430.13.12) | open | P2 | Oct 1 13:08 | run 4889b227 | 13 | last run cancelled |
| Opening a resumed conversation builds the briefing only where a tur... (yoyodyne-ifd.430.16) | open | P2 | Oct 1 13:07 | run 66772811 | 13 | last run cancelled |
| A report repeating one already handled is matched to it on filing:... (yoyodyne-ifd.430.35) | open | P2 | Oct 1 13:11 | run 5a4235c3 | 12 | last run cancelled |
| Stores that atomically replace a JSON file share one primitive with... (yoyodyne-ifd.431.5) | open | P2 | Oct 1 13:09 | run 45fa241e | 12 | last run cancelled |
| Acting from the dashboard, narrowly: approve or decline an amendmen... (yoyodyne-ifd.432.8) | open | P2 | Oct 1 13:07 | run 150e7b26 | 14 | parked |
| A document filed in the specification home is the Lead Product Mana... (yoyodyne-ifd.433.15) | open | P2 | Oct 1 13:10 | run a759ccf5 | 13 | last run cancelled |
| Every admitted item records whether it is a bug fix or a feature, s... (yoyodyne-ifd.433.32) | open | P2 | Oct 7 18:53 | run 34cbb01c | 1 | last run succeeded |
| The tracker is swept once for open items that duplicate closed work... (yoyodyne-ifd.433.8) | open | P2 | Oct 1 13:07 | run 0c50e4c3 | 13 | last run cancelled |
| A hand step is recorded as such: yoyo intervention records what was... (yoyodyne-ifd.436.8) | open | P2 | Oct 1 13:08 | run 3ea9f196 | 12 | last run cancelled |
| The terms check reads the strings of internal/orchestrator, interna... (yoyodyne-ifd.437.23) | open | P2 | Oct 1 13:10 | run 49567937 | 13 | last run failed |
| Retired-term matching respects word boundaries, so 'whose move' nev... (yoyodyne-ifd.437.28) | open | P2 | Oct 1 13:10 | run 9eb2fce5 | 13 | last run cancelled |
| Report artifact identity filed outside every home and inert frontma... (yoyodyne-ifd.87.2) | open | P2 | Oct 1 13:11 | run 945a83c5 | 12 | last run cancelled |
| Split docs/configuration.md before it eats every context budget (yoyodyne-ifd.117) | open | P3 | Aug 23 07:06 | run e2d6a204 | 1 | last run failed |
| One probe proves a run can read outside its worktree, before a prov... (yoyodyne-ifd.217) | open | P3 | Aug 31 16:32 | run 657c2fc2 | 1 | last run failed |
| A recorded re-run decision is claimable exactly once, across stoppages (yoyodyne-ifd.244) | open | P3 | Sep 2 16:38 | run ac4c9655 | 1 | last run failed |
| internal/orchestrator/promotion_test.go's arrivalGate timer is arme... (yoyodyne-ifd.429.20) | open | P3 | Sep 26 08:23 | run ff095569 | 1 | last run timed out |
| Contributor mode: the traceability chain in a sidecar, outside the... (yoyodyne-ifd.78) | open | P4 | Aug 27 13:56 | run 95b34031 | 1 | last run failed |
