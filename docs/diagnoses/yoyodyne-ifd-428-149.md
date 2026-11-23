# What closed the parked items on November 15, and what each still owes (yoyodyne-ifd.428.149)

Between 13:13 and 13:28 PST on November 15, 2026, items whose notes say they stay
open were closed, and nobody could say by what. This names the cause, what
changed, and what every item on the list owes. It was read from the tracker's
copy of November 23 and the code on `main` at `48fa4fa293`. No tracker write was
made: whether to reopen anything is the Lead Product Manager's decision.

## What closed them

**The Lead Product Manager's own conversation did.** Every item in the table carries a
close reason opening "Closed as done by the Lead Product Manager in conversation
chat-91253e0e070c17b0663651cc48602122, after turn …" or "Retired from the backlog
without being done by …". Those words are written only by the `close` and
`retire` actions in `internal/chat/tracker.go`, when that role's reply asks for
them. Most reasons say "Backlog review Nov 15".
Turns 5611 to 5634 closed several dozen items that afternoon; these are some of them.
No run, sweep, or merge queue landing closed any of them.

**Why it looked otherwise.** The tracker keeps a close reason beside the notes,
and a role's read of an item did not show it. The read showed `closed` and then
the notes, whose last account was a run's or a reviewer's, saying the item stays
open. That was taken for the close reason. The action's own result also said
nothing about the parking it was ending.

## What changed

1. A role that closes or retires a parked item is told, in that action's result,
   that the item was parked and for what. A role's read of a closed item says when
   it was closed, by whom and why, ahead of the notes
   (`internal/chat/tracker.go`, `closedparked_test.go`).
2. The scheduler's sweep that closes an item carried in a role's conversation when
   its document lands now leaves a parked item open, records nothing on it, and
   names it with its parking (`internal/orchestrator/conversationlanding.go`,
   `TestAParkedItemIsNotClosedByTheLandingSweep`). This was the one harness
   closing path that never asked whether the work was finished. It closed an item
   parked on evidence once among the items the tracker
   still shows as parked: yoyodyne-ifd.262, below.

### The harness's other closing paths

Each asks whether the run's own change finishes the item, from the run's record:

| Where | Test that an evidence landing stays open |
| --- | --- |
| the run itself, `(*activeRun) complete` in `pipeline.go`: an undischarged change goes to `reopenUndischargedOnce`, never to `Complete` | `TestAnHonestNotDoableYetLandingIntegratesAndReParksItsItem` (developer's claim) and `TestAnApprovalOfEvidenceLeavesTheItemOpenInTheReviewersWords` (reviewer's approval), both in `internal/orchestrator/landingtests/landing_test.go`; each asserts the item is open and parked |
| reconcile, an interrupted run | `TestReconciliationSettlesAnInterruptedRunByTheLandingItClaimed`, same file |
| the merge queue, `settleLandedItem` | `TestALandingThatDoesNotDischargeItsItemLeavesItOpen` |
| a queued merge, a person's merge, the team's merge | held to asking by `TestEveryRouteThatClosesAWorkItemIsAudited`, `internal/landing` |

A pull never starts a parked item
(`TestSelectionNeverPicksAnUndischargedItemUntilItIsReleased`).

## The items, and what each one's notes say it still owes

Times are PST on November 15. "Parked on evidence" means a run's evidence landing
parked it. Where "now" says something is met, that was read from `main` or the
tracker; no item's work was run again.

| Item | Closed | What its notes say it owes | Now |
| --- | --- | --- | --- |
| Command-line approval through a reviewed run (yoyodyne-bhb.1), parked on evidence | 13:13, done | A repeat `yoyo artifact approve` while the first approval's run is going starts no second run and says the approval is already landing, with a test. A decision on the two cases with no reviewed run (no reviewer configured; intent in a separate repository). The recheck of a document approved by hand before it goes to a landing run (report f4f1955c). | **Repeat approval still owed as written.** The approval itself landed (`faa3dcb3`). `PublishStandingApproval` still names each approval separately; a second run of the same document now waits behind the first (`PublishDocument`), but nothing says "already landing", and the test in `documentpublication_test.go` covers only a repeat after landing. The two-case decision and the recheck were not established here. |
| Hourly architect pass (yoyodyne-ifd.434.18), parked on evidence | 13:15, done | Rewording "daily pass" in the configurable-workflows design, once the cadence was settled. | Nothing found owed. That design's revision log records the rewording; the pass was confirmed running October 2. Its 45 minutes is the operator's own later change. |
| Codex resume test (yoyodyne-ifd.435.26), parked on evidence | 13:15, done | The `make codex-resume` check in the project configuration, and that check having passed once with the real Codex. | Configured (`.yoyodyne/config.yaml`, through yoyodyne-ifd.435.28). **A recorded passing run with the real Codex was not found here.** |
| Conversation document confirmation (yoyodyne-ifd.433.21), parked on evidence | 13:16, done | Owners inspecting, withdrawing or replacing a saved document; a work item for every publication run; a check that publishing no longer holds a conversation turn for the whole run. | The first landed as yoyodyne-fp3.2 and the second as yoyodyne-fp3.8 on November 21. **The turn check is not recorded as made.** |
| Developer-run local ports (yoyodyne-ifd.435.20), not parked | 13:17, done | Only its rebuild, yoyodyne-ifd.435.20.1, and closing pull request #1081 unmerged. | Rebuild landed October 18 (`d1ce6bd6`). Nothing found owed in code; whether #1081 is closed was not read. |
| Configuration guide split, last part (yoyodyne-ifd.117.4), parked because its run raised it as impossible as written | 13:18, retired | The whole reduction of `docs/configuration.md` to an index, in pieces small enough to review. | **All of it still owed, and carried**: its duplicate yoyodyne-ifd.117.5 was itself retired November 22 for seven open pieces, yoyodyne-ifd.117.6 to yoyodyne-ifd.117.12. |
| The finding that four status changes lost their note (yoyodyne-ifd.392), parked on evidence; not named before | 13:22, done | The bare line gone from both operator scripts, and yoyodyne-ifd.272 and yoyodyne-ifd.187 put back to blocked with a note or claimed by their repairs. | Its close reason says the scripts were checked and both items are closed. The scripts are outside the repository and were not read here. |
| Passes that leave a trace (yoyodyne-ifd.430.29), waiting on yoyodyne-ps2 rather than parked | 13:25, done | Showing the flag on the factory-problems view with the role as mover. | yoyodyne-ps2 landed November 20 and `TestAnUntracedPassReachesTheDashboardWithItsRoleAsMover` covers the dashboard's data. Nothing found owed. |
| One window for the operator (yoyodyne-ifd.130), not parked | 13:26, retired | Two of three remaining parts were replaced by operating rules (6de828a8, 789c052c). The third: a live queue view with the release and run commands. | **Still owed.** yoyodyne-ifd.432.30 does not cover it: it is a design of who is working on what, with no queue in order and no commands. yoyodyne-ifd.432.56 was admitted since and carries it, open. |
| Queued-merge check reading (yoyodyne-ifd.429.27), parked on evidence | 13:27, retired | A real captured forge comparison and its replay test; verified check reads for pull requests 732 and 700; a record of where the comparison test really runs. | **Not done.** Retired as obsolete: those requests settled long ago and the harness's queue replaced that landing path. Whether the replay is still wanted is the Lead Product Manager's. |
| Maintenance job's duties (yoyodyne-ifd.434.10), not parked | 13:28, retired | Only its rebuild, yoyodyne-ifd.434.10.1: `yoyo start` retires the job only when every duty has an owner. | It did not land. It was replaced by the rebuild, retired in the same turn: the job ran only on the operator's machine and was retired October 1, and the close reason says every owner has landed. Not this fault; a recorded decision. |
| Repair round on a run waiting for a person's merge (yoyodyne-ifd.429.120), not parked | 13:40, retired | Everything: the decision, carrying it out, the refusal when the budget is spent, and tests. | **Still owed, and carried** by yoyodyne-ifd.429.219, open. It was merged into yoyodyne-ifd.429.176, which is closed and settles a neighbouring case. It has a close reason. |
| Passes of different roles side by side (yoyodyne-ifd.428.75), not parked | 15:34, done (turn 5634) | Side-by-side passes, one at a time within a conversation, and an alarm that is not critical for a wait shorter than the interval. The design wording is yoyodyne-ifd.428.75.1, open with the architect. | Its own run's change (pull request #1033) is not what landed; the work is on `main` as yoyodyne-ifd.428.56 (`aca581c1`). The alarm speaks only after a whole interval unfired (`Scheduler.missed`). Not checked: whether every test the item names exists. |

Six further parked items were closed or retired in the same conversation that
afternoon, not parked on evidence: yoyodyne-ifd.434.30, yoyodyne-ifd.209.22,
yoyodyne-ifd.429.54, yoyodyne-ifd.437.24, yoyodyne-ifd.437.32
and yoyodyne-ifd.430.13. Their close reasons say why; they are
named so the list is not read as every parked item closed that day.

## The one the harness did close

The companion document for writing goals (yoyodyne-ifd.262) was parked by run
run-f4a6034d7a786eee2528e444fbe1e512 as evidence and closed by the scheduler's
sweep at 09:48 PDT on September 30. Its notes say it owes one step: giving
`docs/product/goals/writing-goals.md` a kind the loader accepts, once the artifact
contract has one. **Still owed**: that document still says so itself.

The search covers only items that still carry their parking. An item released and
then closed by a run that finished it is not in question; one whose parking was
removed some other way would not be found.
