# yoyodyne-ifd.433.35: why open tracker items carry an assignee nobody set on purpose

Three items carry Mason Bryant as assignee though nobody says he is working on
them: the tracker-write retry fix (yoyodyne-ifd.433.22), re-classifying a run
that stops again after a repair (yoyodyne-ifd.428.74), and tool interface
slice 1 (yoyodyne-ifd.430.19). The development manager's reading was that the
assignee reads as a person's claim, so nothing picks these items up. This
diagnosis checks that against the harness's code and the recorded history.

All times are Pacific (PDT). The tracker export read is the one copied into
this run's worktree on October 11 at about 15:20; the harness's run records were
read at the same time. What the scheduler's log said was read by the first
attempt at this diagnosis, on October 9 at about 22:47, and is marked where it
is used.

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
  an item changed. The plainest proof is the retry fix itself: it carried the
  assignee from October 1 and was picked up, run, and merged on October 10 with
  the assignee still on it. See [the three named items](#the-three-named-items).
- **Runs stopped by the provider's usage limit spread the assignee fastest.**
  When the limit is exhausted, the scheduler keeps choosing work, and each run
  claims its item and is cancelled a few seconds later. On October 1, 775 runs
  were cancelled this way between 12:35 and 20:48, half of them within about
  four seconds; on October 10, 104 runs between 00:36 and 07:06. Every one of
  them left its item carrying the assignee. Of the 133 items listed below, 30
  were first claimed on October 1 and 53 on October 10.
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
| Give an item back after a run that ended | `Client.Release` (the harness's check for claims that outlived their run), `Client.Reopen` (evidence landings, and items raised as not meetable) | none; the status goes to open and the assignee stays |
| Clear a stale blocked status | `Client.Unblock`, the clear in `claimPastStaleBlock` | none |
| Block, record an outcome, cost, landing, origin | `Client.Block`, `RecordOutcome`, `RecordCost`, `RecordLanding`, `RecordOrigin` | none |
| Close | `Client.Complete` | none; the closed item keeps it |
| Create | `Client.Create` | none; a new item has no assignee |

No harness code passes `--assignee`, `--actor`, or `BEADS_ACTOR`. A person
running `bd update --claim` or `bd create --assignee` by hand would set it too,
and the record cannot tell that apart from a run's claim except by whether a run
record shows a claim at the same moment.

## The whole tracker agrees

Of the 1,174 items in the export:

| | Items |
| --- | --- |
| Have at least one run record, and carry the assignee | 950 |
| Have at least one run record, and carry no assignee | 0 |
| Have no run record, and carry no assignee | 204 |
| Have no run record, and carry the assignee | 20 |

The last 20 are nineteen closed items claimed between August 14 and October 2,
nine of them before the earliest run record (August 15 at 10:24), and the
top-level epic (yoyodyne-ifd), claimed on August 17 at 15:30 with no run. Those
are the only assignees a person can have set.

For every one of the 132 unfinished items whose assignee came from a run, the
time the tracker records the item first moving to in progress is within ten
seconds of the claim time on one of that item's run records. That is the claim
setting the assignee, item by item.

## The three named items

**The tracker-write retry fix (yoyodyne-ifd.433.22).** Its first thirteen runs,
on October 1 from 13:02 to 18:55, were each cancelled within seconds because
the provider's seven-day usage limit was exhausted until October 3 at 20:00,
later than the six hours the harness will wait. The first of them set the
assignee. From then until the evening of October 9 the scheduler's log (read on
October 9) listed the item as ready each time it looked for work, and passed it
over because it would have run at the same time as another run changing the
same file: almost always run-7d92c67b, on holding a check stopped by its own
time limit (yoyodyne-ifd.428.37), over `internal/orchestrator/triage.go`. That
run's process stopped writing on October 2 at 07:02 and never came back, but
its record still said running, so the scheduler kept counting it as running
until the harness's check for claims that outlived their run ended it on
October 9 at 21:24. Top-priority work could not go ahead of it either: the rule
that lets a P0 item start beside a run on less urgent work (`outranks` in
`internal/orchestrator/conflict.go`) was only committed with the fix for P0
items not being selected (yoyodyne-j2u) at 18:27 the same evening. Whether a
run whose process has died still holds its developer slot and its files is
already admitted as the question of a dead run's slot (yoyodyne-7mk). On
October 10 two more runs were cancelled by the usage limit, at 00:50 and 03:44,
and the run started at 20:00 succeeded; its pull request (#1157) merged and the
item closed on October 10 at 21:39. It still carries the assignee.

**Re-classifying a run that stops again after a repair (yoyodyne-ifd.428.74).**
The tracker gives it priority 1; the work item for this diagnosis calls it P0.
Its run run-9b58b617 claimed it on October 4 at 21:34 and was stopped by the
development manager at 22:14 with its change kept, and the harness's check for
claims that outlived their run gave the item back at 23:16. On October 9 the
scheduler's log showed it passing the item over 58 times because "it is held
for a decision nobody has made": the stop kept a change and nobody had yet
decided what happens to it. The assignee plays no part.

**Tool interface slice 1 (yoyodyne-ifd.430.19).** Its only run, run-69bf6614,
claimed it on October 4 at 07:30 and raised it as not meetable as it stands.
The item was parked — kept in the Lead Product Manager's order but not selected
— until the ruling on reading logs (yoyodyne-ifd.430.19.1) was made. The Lead
Product Manager has since released it against that ruling, and it is open and
waiting to be picked up. The assignee is the run's claim.

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

## Every unfinished item that carries an assignee

133 items that are not closed carry Mason Bryant as assignee: 110 open, 15
blocked, and 8 in progress. "Assignee set" is when the tracker records the item
first moving to in progress, which is the claim. "Set by" is the run whose
recorded claim matches that time, or a person where no run record does. "Where
it stands" is as the export and run records read on October 11 at about 15:20:
"parked" is an item kept in the Lead Product Manager's order but not selected
until somebody releases it, and otherwise it is how the item's latest run ended,
or the run still working on it.

| Item | Status | Priority | Assignee set (PDT) | Set by | Runs | Where it stands |
| --- | --- | --- | --- | --- | --- | --- |
| When the scheduler passes over ready work or leaves a decided repair... (yoyodyne-37h) | in progress | P0 | Oct 5 00:00 | run 376a3748 | 2 | last run failed |
| A hold the architect puts on sending documents delays only the sendi... (yoyodyne-4qw) | in progress | P0 | Oct 10 03:56 | run d3113324 | 2 | run 5817073e running |
| A decision record written from a conversation is confirmed under the... (yoyodyne-66h) | in progress | P0 | Oct 11 12:12 | run 570b9a9b | 1 | run 570b9a9b running |
| When a role's own scheduled pass keeps failing, the finding goes to... (yoyodyne-ifd.428.76) | in progress | P0 | Oct 5 06:18 | run 88804f0e | 1 | last run failed |
| The Codex adapter launches runs under a Codex permission profile tha... (yoyodyne-ifd.435.27) | in progress | P0 | Oct 7 20:24 | run e389ddbd | 1 | run e389ddbd running |
| Build Yoyodyne v1 through early self-hosting (yoyodyne-ifd) | in progress | P1 | Aug 17 15:30 | a person (no run record) | 0 | top-level epic, no run |
| A program manager's pass opens with the read model and the other ins... (yoyodyne-ifd.430.13.8) | in progress | P1 | Sep 28 11:28 | run 008b0e25 | 1 | last run succeeded |
| Find what leaves an assignee on tracker items nobody is working on,... (yoyodyne-ifd.433.35) | in progress | P1 | Oct 9 22:47 | run abdb557c | 2 | run a5646fb7 running |
| One role's scheduled pass does not wait behind another role's: passe... (yoyodyne-ifd.428.75) | blocked | P0 | Oct 5 04:50 | run 5d9005f6 | 1 | blocked |
| One ownership registry and resolver: every waiting entry's owner, re... (yoyodyne-ifd.432.25.1) | blocked | P0 | Sep 30 09:48 | run d4c31756 | 2 | blocked |
| An agent block's role: may name an activated role definition, and ca... (yoyodyne-ifd.434.6) | blocked | P0 | Oct 3 01:08 | run 6049f65f | 2 | blocked |
| Developer-run sandboxes allow loopback binding, so the Codex native-... (yoyodyne-ifd.435.20) | blocked | P0 | Oct 7 06:33 | run a55ad7d7 | 1 | blocked |
| Interactive sessions get the notes-writer guard: the uncovered loss... (yoyodyne-ifd.153) | blocked | P1 | Aug 23 14:09 | run 5035c832 | 2 | blocked |
| The development manager's brake summons and the wake that corrects a... (yoyodyne-ifd.428.60) | blocked | P1 | Oct 1 13:05 | run 0f36153f | 15 | blocked |
| The development manager's docket never leaves out an entry: where it... (yoyodyne-ifd.428.82) | blocked | P1 | Oct 9 01:06 | run f835c861 | 1 | blocked |
| Connect the merge queue to the configuration switch and verify activ... (yoyodyne-ifd.429.59.6) | blocked | P1 | Oct 9 14:37 | run 83b41c71 | 1 | blocked |
| A reply with one malformed block loses only that block: the rest is... (yoyodyne-ifd.430.17) | blocked | P1 | Oct 1 12:58 | run fff8b12b | 14 | blocked |
| Any document in the product home may carry a revision marked consist... (yoyodyne-ifd.433.19) | blocked | P1 | Oct 1 13:01 | run dc97df55 | 14 | blocked |
| The maintenance job's remaining duties move into the product before... (yoyodyne-ifd.434.10) | blocked | P1 | Sep 27 20:28 | run 6fdcae7a | 1 | blocked |
| Shared confined writers preserve containment through filesystem repl... (yoyodyne-xdj) | blocked | P1 | Oct 3 14:34 | run 1953fb46 | 1 | blocked |
| Agents speak in their threads through the sink, and answers land whe... (yoyodyne-ifd.68.25) | blocked | P2 | Sep 1 07:03 | run 7172270f | 1 | blocked |
| configuration.md split tranche 4: reduce to the index, reconcile inb... (yoyodyne-ifd.117.4) | blocked | P2 | Sep 24 11:25 | run 45cc3236 | 1 | parked |
| A governed-document defect fails its owner's gate, not every develop... (yoyodyne-ifd.174) | blocked | P2 | Aug 24 00:10 | run f28ebe44 | 1 | blocked |
| Each check's own time limit scales with machine load as the stage li... (yoyodyne-ifd.429.41) | open | P0 | Oct 1 12:55 | run a3788b2e | 14 | parked |
| Profile current orchestrator Git cost and identify bounded test redu... (yoyodyne-ifd.429.45) | open | P0 | Oct 3 10:24 | run aeea1445 | 2 | parked |
| Tool interface, slice 1: the tool descriptor type, the registry mark... (yoyodyne-ifd.430.19) | open | P0 | Oct 4 07:30 | run 69bf6614 | 1 | last run succeeded |
| Run the existing architect pass hourly and verify scheduler adoption (yoyodyne-ifd.434.18) | open | P0 | Oct 2 15:30 | run 4aff5070 | 1 | parked |
| An exhausted provider endpoint receives no fresh dispatch until its... (yoyodyne-ifd.435.10) | open | P0 | Oct 1 21:55 | run 07feebbe | 2 | last run timed out |
| Apply developer-slot endpoint pairs during initial work, repair and... (yoyodyne-ifd.435.14.4) | open | P0 | Oct 7 14:12 | run 23809e0d | 1 | parked |
| A Codex agent that sets no effort is invoked with none, so the Codex... (yoyodyne-ifd.435.18) | open | P0 | Oct 5 10:31 | run 0d93564e | 32 | last run failed |
| Up and down recall earlier messages in yoyo chat and move between th... (yoyodyne-403) | open | P1 | Oct 10 03:48 | run 91177050 | 1 | last run cancelled |
| The architect can submit an invariant amendment from her conversatio... (yoyodyne-0bj) | open | P1 | Oct 10 03:42 | run e2f494b0 | 1 | last run cancelled |
| Typing / in yoyo chat opens a list of commands that narrows as more... (yoyodyne-64f) | open | P1 | Oct 10 03:54 | run 20e7eed4 | 1 | last run cancelled |
| Long harness output in yoyo chat, such as tracker results and reposi... (yoyodyne-7xx) | open | P1 | Oct 10 03:54 | run ccb1e548 | 1 | last run cancelled |
| Secret scrubbing is tested against what Claude Code and Codex print... (yoyodyne-7yn) | open | P1 | Oct 10 03:45 | run 1aceea8f | 1 | last run cancelled |
| The readiness check holds an item for a missing cited name only in l... (yoyodyne-8mc) | open | P1 | Oct 10 04:04 | run c6c1964f | 1 | last run cancelled |
| A message typed and sent while yoyo chat is running a turn is kept a... (yoyodyne-99z) | open | P1 | Oct 10 03:49 | run 05c13f4c | 1 | last run cancelled |
| Prevent prerequisite work from inheriting a block from the implement... (yoyodyne-aaw) | open | P1 | Oct 10 07:06 | run bd4e07c5 | 1 | last run cancelled |
| The check that refuses done-conditions naming a document a run may n... (yoyodyne-c4i) | open | P1 | Oct 10 03:43 | run 8df1b45f | 1 | last run cancelled |
| An ask from one role to another that refers to a work item is delive... (yoyodyne-dfk) | open | P1 | Oct 10 04:01 | run 63f5d19c | 1 | last run cancelled |
| An approved change whose first merge is refused still records how it... (yoyodyne-doa) | open | P1 | Oct 10 03:40 | run 1ab45b4f | 1 | last run cancelled |
| yoyo chat's prompt gains the editing keys Claude Code's prompt has:... (yoyodyne-ede) | open | P1 | Oct 10 03:51 | run e59a19d3 | 1 | last run cancelled |
| A long paste shows in yoyo chat's prompt as one line naming how many... (yoyodyne-etw) | open | P1 | Oct 10 03:51 | run c938a228 | 1 | last run cancelled |
| When a newer revision of a document gets its own publishing run, the... (yoyodyne-g5x) | open | P1 | Oct 10 03:57 | run b3e340c7 | 1 | last run cancelled |
| The merge-queue hold checks a process group twice before deciding it... (yoyodyne-i4v) | open | P1 | Oct 10 04:00 | run fd3c0ad7 | 1 | last run cancelled |
| A tracker status is never moved backwards without a note: find and c... (yoyodyne-ifd.392) | open | P1 | Sep 18 22:50 | run 06b93741 | 1 | parked |
| Product 4 of 4: the dashboard is a supervised child, with its token... (yoyodyne-ifd.414) | open | P1 | Oct 1 12:57 | run 42f4f578 | 14 | last run succeeded |
| A check stopped by its own execution.check_timeout is held and conti... (yoyodyne-ifd.428.37) | open | P1 | Oct 1 12:59 | run 2085f5c7 | 14 | last run cancelled |
| A wait recorded on a stopped-run entry takes that entry off the dock... (yoyodyne-ifd.428.71) | open | P1 | Oct 4 10:10 | run 12bb2725 | 1 | parked |
| A run that stops again after a decided repair was carried out is cla... (yoyodyne-ifd.428.74) | open | P1 | Oct 4 21:34 | run 9b58b617 | 2 | last run cancelled |
| A re-run recorded against a run that never reached the development m... (yoyodyne-ifd.428.94) | open | P1 | Oct 10 04:05 | run 048383ba | 1 | last run cancelled |
| An integration stop is classified from the error's cause, not its ta... (yoyodyne-ifd.429.1) | open | P1 | Sep 21 20:07 | run de4a74ff | 2 | parked |
| The queued-merge check reads the forge's check state without failing... (yoyodyne-ifd.429.27) | open | P1 | Oct 1 12:58 | run 6f0b35eb | 14 | parked |
| The reviewer checks every change against the standing goals as well... (yoyodyne-ifd.429.34) | open | P1 | Oct 4 20:23 | run f6a0210e | 1 | parked |
| Build integration through a merge queue as designed: one switch, a h... (yoyodyne-ifd.429.59) | open | P1 | Oct 5 03:05 | run 1167e267 | 1 | last run succeeded |
| Merge-queue records are stored durably and shown through the shared... (yoyodyne-ifd.429.59.6.1) | open | P1 | Oct 10 03:56 | run d3815d32 | 1 | last run cancelled |
| A check that fails after a review keeps the reviewer's findings on t... (yoyodyne-ifd.429.68) | open | P1 | Oct 9 14:43 | run 5d62f75e | 2 | last run failed |
| The test that guards the old configuration path reads only files und... (yoyodyne-ifd.429.72) | open | P1 | Oct 10 03:50 | run f15cc134 | 1 | last run cancelled |
| When a provider refuses to resume the reviewer's session for the sec... (yoyodyne-ifd.429.73) | open | P1 | Oct 10 04:14 | run 5d4a7d9d | 1 | last run cancelled |
| Build the program manager as designed: the sixth role bundle, config... (yoyodyne-ifd.430.13) | open | P1 | Sep 25 03:38 | run a9924c13 | 1 | parked |
| The factory-flow program manager post-mortems every stopped run: one... (yoyodyne-ifd.430.13.11) | open | P1 | Oct 2 22:13 | run ed681925 | 1 | last run succeeded |
| Every role's contract and shipped persona applies the standing goals... (yoyodyne-ifd.430.24) | open | P1 | Oct 4 19:38 | run f8cbeaa4 | 1 | parked |
| A pass that found something leaves a trace: a memory write, a lane r... (yoyodyne-ifd.430.29) | open | P1 | Sep 29 07:30 | run e59006f4 | 15 | last run succeeded |
| Every role's contract and live persona says how to name the operator... (yoyodyne-ifd.432.25.4) | open | P1 | Oct 1 13:04 | run 54c9e611 | 14 | last run succeeded |
| Every work item named to a person is shown as (priority, labels) tit... (yoyodyne-ifd.432.28) | open | P1 | Oct 1 13:04 | run 1b0c11be | 14 | parked |
| An admission records the goals relevant to a work item beside the on... (yoyodyne-ifd.433.12) | open | P1 | Oct 5 00:06 | run a336c4b9 | 1 | parked |
| A role never sees a write as lost when it was not: a reprioritize wr... (yoyodyne-ifd.433.25) | open | P1 | Oct 1 13:06 | run 7b5f5c13 | 15 | last run timed out |
| A second terminal after a clean first terminal does not relaunch a d... (yoyodyne-ifd.435.1) | open | P1 | Sep 22 23:53 | run 4d9f253e | 3 | last run failed |
| The Codex adapter is tested against real recorded streams from the s... (yoyodyne-ifd.435.7) | open | P1 | Sep 30 17:52 | run a10d7b1a | 2 | parked |
| Unexpected tool use across every provider produces a prominent ERROR... (yoyodyne-ifd.435.9) | open | P1 | Oct 10 03:49 | run 8dea75f9 | 1 | last run cancelled |
| The Codex resume test is skipped inside a developer run's sandbox on... (yoyodyne-ifd.435.26) | open | P1 | Oct 8 01:08 | run 2dc7ceed | 1 | parked |
| The project's checks include one that runs the Codex resume test wit... (yoyodyne-ifd.435.28) | open | P1 | Oct 8 04:17 | run 78fd30cc | 2 | parked |
| A decided release is cut by the harness: maintenance validates revie... (yoyodyne-ifd.436.2) | open | P1 | Oct 1 12:57 | run a49f7a9e | 15 | last run succeeded |
| The coined vocabulary is replaced in printed strings, role contracts... (yoyodyne-ifd.437.19) | open | P1 | Oct 1 13:01 | run 26d3f531 | 15 | parked |
| Shift-return adds a new line in yoyo chat on terminals that do not r... (yoyodyne-kuw) | open | P1 | Oct 10 03:47 | run 50357c13 | 1 | last run cancelled |
| The reviewer sends incomplete work back to the developer rather than... (yoyodyne-o1b) | open | P1 | Oct 10 03:41 | run 22d9f6f5 | 1 | last run cancelled |
| Esc stops the turn yoyo chat is running without ending the chat, Ctr... (yoyodyne-q4q) | open | P1 | Oct 10 03:52 | run a849f4ac | 1 | last run cancelled |
| A check stopped at its time limit reports how long it actually ran a... (yoyodyne-qzj) | open | P1 | Oct 10 03:59 | run 6dec9d8c | 1 | last run cancelled |
| With human merge approval configured, a change still goes through th... (yoyodyne-xps) | open | P1 | Oct 10 04:12 | run f903bab5 | 1 | last run cancelled |
| Pipeline.Run decides whether a paused run can resume through one res... (yoyodyne-0rm) | open | P2 | Oct 10 04:22 | run 0cb05aaa | 1 | last run cancelled |
| An ask round whose reply is tool-call text with no answer is not del... (yoyodyne-4f8) | open | P2 | Oct 10 04:24 | run 0c75426e | 1 | last run cancelled |
| The guides refer to the development manager the same way throughout,... (yoyodyne-66x) | open | P2 | Oct 10 04:25 | run b6886141 | 1 | last run cancelled |
| A closed item the tracker returns without a closing time is counted... (yoyodyne-8o7) | open | P2 | Oct 10 04:25 | run 05fc3f36 | 1 | last run cancelled |
| Role exchanges use the receiving role's configured eligible provider... (yoyodyne-bxc) | open | P2 | Oct 10 04:22 | run 2370102d | 1 | last run cancelled |
| A design or goals document whose home is a companion intent reposito... (yoyodyne-ifd.78.2) | open | P2 | Oct 10 04:25 | run 636a8afc | 1 | last run cancelled |
| Report artifact identity filed outside every home and inert frontmat... (yoyodyne-ifd.87.2) | open | P2 | Oct 1 13:11 | run 945a83c5 | 13 | last run cancelled |
| One pane of glass: yoyo chat is the entry point and the PM manages t... (yoyodyne-ifd.130) | open | P2 | Aug 20 12:48 | run d1b1f813 | 1 | last run failed |
| The delivery workflow routes the 'reconciling' integrate outcome bac... (yoyodyne-ifd.209.31) | open | P2 | Oct 1 13:10 | run 54b4dc16 | 15 | parked |
| The development manager pre-judges the ready set: dispatch marks and... (yoyodyne-ifd.305) | open | P2 | Oct 1 13:06 | run 652a5d09 | 14 | parked |
| Probe Gemini/Antigravity and write the capability report (yoyodyne-ifd.341) | open | P2 | Sep 7 08:35 | run bc68d05f | 1 | parked |
| A goal has a stable identity, so amending its wording breaks nothing (yoyodyne-ifd.344) | open | P2 | Sep 7 10:08 | run 394c68c9 | 1 | parked |
| A parked item is never dispatched, whatever a status repair or a cla... (yoyodyne-ifd.428.36) | open | P2 | Oct 1 13:07 | run 61d81c57 | 14 | last run cancelled |
| A spent re-run claim is given back by a decision on the triage surfa... (yoyodyne-ifd.428.41) | open | P2 | Oct 1 13:09 | run 6d7cb3e8 | 13 | last run cancelled |
| A re-run carry-out that loses its claim after reserving the fresh ru... (yoyodyne-ifd.428.53) | open | P2 | Oct 1 13:08 | run 09e7dc66 | 14 | last run cancelled |
| A repair grant and a repair attempt are counted in one unit on the r... (yoyodyne-ifd.428.86) | open | P2 | Oct 10 04:24 | run c39846e2 | 1 | last run cancelled |
| Run cleanup deletes a run's write-lock file and its attempts' hold f... (yoyodyne-ifd.428.91) | open | P2 | Oct 10 04:24 | run 0ef81c43 | 1 | last run cancelled |
| internal/orchestrator's fakes of other packages' interfaces (tracker... (yoyodyne-ifd.429.13) | open | P2 | Sep 25 07:50 | run 8bcb69bc | 1 | parked |
| Tracker-backed conformance tests fail rather than skip when the trac... (yoyodyne-ifd.429.32) | open | P2 | Oct 1 13:10 | run 2eebb61b | 14 | last run cancelled |
| Nine thirty-second budgets on real processes in the tracker and exec... (yoyodyne-ifd.429.36) | open | P2 | Oct 1 13:08 | run e078c028 | 14 | last run cancelled |
| A program manager that has never completed a pass says what its pass... (yoyodyne-ifd.430.13.12) | open | P2 | Oct 1 13:08 | run 4889b227 | 14 | last run cancelled |
| Opening a resumed conversation builds the briefing only where a turn... (yoyodyne-ifd.430.16) | open | P2 | Oct 1 13:07 | run 66772811 | 14 | last run cancelled |
| A report repeating one already handled is matched to it on filing: t... (yoyodyne-ifd.430.35) | open | P2 | Oct 1 13:11 | run 5a4235c3 | 13 | last run cancelled |
| Stores that atomically replace a JSON file share one primitive with... (yoyodyne-ifd.431.5) | open | P2 | Oct 1 13:09 | run 45fa241e | 12 | last run cancelled |
| Acting from the dashboard, narrowly: approve or decline an amendment... (yoyodyne-ifd.432.8) | open | P2 | Oct 1 13:07 | run 150e7b26 | 14 | parked |
| The tracker is swept once for open items that duplicate closed work... (yoyodyne-ifd.433.8) | open | P2 | Oct 1 13:07 | run 0c50e4c3 | 14 | last run cancelled |
| A document filed in the specification home is the Lead Product Manag... (yoyodyne-ifd.433.15) | open | P2 | Oct 1 13:10 | run a759ccf5 | 14 | last run cancelled |
| An admission says whether it fixes a defect, and one that does is re... (yoyodyne-ifd.433.26) | open | P2 | Oct 10 04:22 | run d33376f1 | 1 | last run cancelled |
| Every admitted item records whether it is a bug fix or a feature, so... (yoyodyne-ifd.433.32) | open | P2 | Oct 7 18:53 | run 34cbb01c | 1 | last run succeeded |
| The temporary Codex home the harness builds for a run keeps what Cod... (yoyodyne-ifd.435.23) | open | P2 | Oct 10 04:23 | run 414ae4be | 1 | last run cancelled |
| A hand step is recorded as such: yoyo intervention records what was... (yoyodyne-ifd.436.8) | open | P2 | Oct 1 13:08 | run 3ea9f196 | 12 | last run cancelled |
| Retired-term matching respects word boundaries, so 'whose move' neve... (yoyodyne-ifd.437.28) | open | P2 | Oct 1 13:10 | run 9eb2fce5 | 14 | last run cancelled |
| The developer persona and the template it is generated from carry th... (yoyodyne-ifd.437.37) | open | P2 | Oct 10 04:23 | run 2a90367b | 1 | last run cancelled |
| The operations guide and the run-stops inventory say that a decided... (yoyodyne-ifd.437.39) | open | P2 | Oct 10 04:23 | run eb901f5b | 1 | last run cancelled |
| Break up Session.Send and Session.takeTurn in internal/chat/chat.go;... (yoyodyne-a5h) | open | P3 | Oct 10 04:29 | run 011277f9 | 1 | last run cancelled |
| Map or split internal/orchestrator/schedule.go; Scheduler.Schedule i... (yoyodyne-cw6) | open | P3 | Oct 10 04:30 | run 10fc02f6 | 1 | last run cancelled |
| Split docs/configuration.md before it eats every context budget (yoyodyne-ifd.117) | open | P3 | Aug 23 07:06 | run e2d6a204 | 1 | last run failed |
| One probe proves a run can read outside its worktree, before a provi... (yoyodyne-ifd.217) | open | P3 | Aug 31 16:32 | run 657c2fc2 | 1 | last run failed |
| A recorded re-run decision is claimable exactly once, across stoppag... (yoyodyne-ifd.244) | open | P3 | Sep 2 16:38 | run ac4c9655 | 1 | last run failed |
| The promotion lease's hold window is verified at every branch-moving... (yoyodyne-ifd.320) | open | P3 | Oct 10 04:44 | run aa790580 | 1 | last run cancelled |
| Strings the tracker, forge, or provider supply are bounded where the... (yoyodyne-ifd.428.48) | open | P3 | Oct 10 04:27 | run 21e83672 | 1 | last run cancelled |
| The developer's change summary is delivered to the reviewer as evide... (yoyodyne-ifd.429.17) | open | P3 | Oct 10 04:26 | run 7cf40020 | 1 | last run cancelled |
| internal/orchestrator/promotion_test.go's arrivalGate timer is armed... (yoyodyne-ifd.429.20) | open | P3 | Sep 26 08:23 | run ff095569 | 1 | last run timed out |
| A side thread that ends with no prose can still be concluded, and si... (yoyodyne-ifd.430.36) | open | P3 | Oct 10 04:28 | run fff4c498 | 1 | last run cancelled |
| Small cleanups from the review: one shared reading of whether a run'... (yoyodyne-ifd.431.6) | open | P3 | Oct 10 04:28 | run 921c6e07 | 1 | last run cancelled |
| A parking placed by an unmeetable raise carries a structured marker,... (yoyodyne-ifd.433.16) | open | P3 | Oct 10 04:27 | run 5633237b | 1 | last run cancelled |
| A running part's record of which configuration keys it reads names t... (yoyodyne-ifd.434.16) | open | P3 | Oct 10 04:27 | run eef5d05f | 1 | last run cancelled |
| A developer's execution record is corroborated by the harness: the B... (yoyodyne-ifd.435.4) | open | P3 | Oct 10 04:26 | run fd4996c0 | 1 | last run cancelled |
| The reporting and Slack setup guides no longer cite the retired main... (yoyodyne-ifd.437.29) | open | P3 | Oct 10 04:28 | run 6b929532 | 1 | last run cancelled |
| The terms check's ordinary-compound rule does not pass a coinage by... (yoyodyne-ifd.437.40) | open | P3 | Oct 10 04:30 | run 454ade2f | 1 | last run cancelled |
| The reviewer persona says the terms check catches new hyphenated com... (yoyodyne-ifd.437.41) | open | P3 | Oct 10 04:31 | run 68a0c980 | 1 | last run cancelled |
| Split internal/orchestrator/pipeline.go along its natural seams, wit... (yoyodyne-qli) | open | P3 | Oct 10 04:29 | run 9d5d60f6 | 1 | last run cancelled |
| Contributor mode: the traceability chain in a sidecar, outside the m... (yoyodyne-ifd.78) | open | P4 | Aug 27 13:56 | run 95b34031 | 1 | last run failed |
| Role names and identifiers are validated the same way everywhere: co... (yoyodyne-ifd.410) | open | P4 | Oct 10 04:31 | run cf010335 | 1 | last run cancelled |
