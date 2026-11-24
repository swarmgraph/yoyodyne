# What closed the parked items on November 15, and what each still owes

This is the diagnosis for the item asking why parked items were closed
(yoyodyne-ifd.428.149).

Between 13:13 and 13:28 PST on November 15, 2026, items whose notes say they stay
open were closed, and nobody could say by what. This names the cause, what
changed, and what every item on the list owes. It was read from the tracker's
copy of November 23, 2026 and the code on `main` that day. No tracker write was
made: whether to reopen anything is the Lead Product Manager's decision.

Three words are used throughout. A *parked* item stays in the backlog and is not
started until somebody releases it. *Evidence* is what a run merges when it found
the work could not be done yet: an account of why, instead of the work. The
harness then parks the item, with that account as the reason. *Retired* means
closed without the work being done.

## What closed them

**The Lead Product Manager did, in her own conversation.** Every item in the
table carries a close reason opening "Closed as done by the Lead Product Manager
in conversation chat-91253e0e070c17b0663651cc48602122, after turn …" or "Retired
from the backlog without being done by …". Those words are written only when that
role's reply asks to close or retire an item (`internal/chat/tracker.go`). Most
of the reasons say "Backlog review Nov 15". Turns 5611 to 5634 of that
conversation closed several dozen items that afternoon; these are some of them.
No run, automatic check, or merge closed any of them.

**Why it looked otherwise.** The tracker keeps a close reason apart from an
item's notes, and what a role was shown when it read an item left the reason out.
It showed `closed` and then the notes, whose last entry was a run's or a
reviewer's, saying the item stays open. That entry was taken for the close
reason. The result of closing an item also did not say that the item had been
parked.

## What changed

1. A role that closes or retires a parked item is told, in the result, that the
   item was parked and for what. A role that reads a closed item is shown when it
   was closed, by whom and why, ahead of the notes (`internal/chat/tracker.go`,
   tested in `closedparked_test.go`).
2. An item that a role's conversation does, rather than a developer run, is
   closed by the harness when the role's document for it is in the repository.
   That closing now leaves a parked item open, records nothing about the
   closing on it, and says what it is parked for
   (`internal/orchestrator/conversationlanding.go`, tested by
   `TestAParkedItemIsNotClosedByTheLandingSweep`). It was the one place the
   harness closed an item without asking whether its work was finished. Among the
   items the tracker still shows as parked, it had closed one that was parked
   because its run merged evidence: the companion document for writing goals
   (yoyodyne-ifd.262), described at the end.

### The other places the harness closes an item

Each reads, from the run's record, whether that run's change finishes the item:

| Where | Test that the item stays open when its run merged evidence |
| --- | --- |
| The run itself (`(*activeRun) complete` in `pipeline.go`), which reopens and parks an item its change does not finish (`reopenUndischargedOnce`) and does not close it | `TestAnHonestNotDoableYetLandingIntegratesAndReParksItsItem` (the developer says the work is not doable yet) and `TestAnApprovalOfEvidenceLeavesTheItemOpenInTheReviewersWords` (the reviewer does), both in `internal/orchestrator/landingtests/landing_test.go`; each checks the item is open and parked |
| The clean-up of a run that was interrupted after its change merged | `TestReconciliationSettlesAnInterruptedRunByTheLandingItClaimed`, same file |
| The merge queue (`settleLandedItem`) | `TestALandingThatDoesNotDischargeItsItemLeavesItOpen` |
| A merge the forge made later, a merge a person made, and a merge the team made | `TestEveryRouteThatClosesAWorkItemIsAudited` in `internal/landing` checks that each of them asks |

The harness never starts a parked item by itself either
(`TestSelectionNeverPicksAnUndischargedItemUntilItIsReleased`).

## The items, and what each one's notes say it still owes

Times are PST on November 15. "Parked on evidence" means the harness parked the
item because its run merged evidence. Where "now" says something is met, that was
read from `main` or the tracker; no item's work was run again.

| Item | Closed | What its notes say it owes | Now |
| --- | --- | --- | --- |
| Command-line approval through a reviewed run (yoyodyne-bhb.1), parked on evidence | 13:13, as done | A repeat `yoyo artifact approve` while the first approval's run is going starts no second run and says the approval is already landing, with a test. A decision on the two cases with no reviewed run (no reviewer configured; intent kept in a separate repository). The recheck of a document approved by hand before it goes to a run (report f4f1955c). | **The repeat approval is still owed as written.** The approval itself merged (`faa3dcb3`). `PublishStandingApproval` still names each approval separately. A second run of the same document now waits behind the first (`PublishDocument`), but nothing says "already landing", and the test in `documentpublication_test.go` covers only a repeat after the first has merged. Whether the two-case decision and the recheck were made was not established here. |
| Hourly architect pass (yoyodyne-ifd.434.18), parked on evidence | 13:15, as done | Rewording "daily pass" in the configurable-workflows design, once the time between passes was settled. | Nothing found owed. That design's revision log records the rewording, and the pass was confirmed running on October 2. Its present 45 minutes is the operator's own later change. |
| Codex resume test (yoyodyne-ifd.435.26), parked on evidence | 13:15, as done | The `make codex-resume` check in the project configuration, and that check having passed once with the real Codex. | It is configured (`.yoyodyne/config.yaml`), by the item adding the check with the real Codex present (yoyodyne-ifd.435.28). **No record of it passing with the real Codex was found here.** |
| Conversation document confirmation (yoyodyne-ifd.433.21), parked on evidence | 13:16, as done | Owners reading, withdrawing or replacing a saved document. A work item for every publication run. A check that publishing no longer holds a conversation's turn for the whole run. | The first merged as one waiting revision per document (yoyodyne-fp3.2), and the second as publishing runs that say they wait in the merge queue (yoyodyne-fp3.8) on November 21. **The check of the turn is not recorded as made.** |
| Developer-run local ports (yoyodyne-ifd.435.20), not parked | 13:17, as done | Only the same work built again from current `main` (yoyodyne-ifd.435.20.1), and closing pull request #1081 unmerged. | That work merged on October 18 (`d1ce6bd6`). Nothing found owed in code; whether #1081 is closed was not read. |
| Configuration guide split, last part (yoyodyne-ifd.117.4), parked because its run said it could not be done as written | 13:18, retired | The whole reduction of `docs/configuration.md` to an index, in pieces small enough to review. | **All of it is still owed, and other items carry it.** The item repeating it, cutting the guide to an index of seven guides (yoyodyne-ifd.117.5), was itself retired on November 22 for seven open pieces: configuration guide split, pieces A to G (yoyodyne-ifd.117.6 to yoyodyne-ifd.117.12). |
| Status changes that lost their note (yoyodyne-ifd.392), parked on evidence; not on this item's list until now | 13:22, as done | The line that changes a status without a note removed from both operator scripts. The audit of claimed items whose run died (yoyodyne-ifd.272) and the named state for refusals from outside the work (yoyodyne-ifd.187) put back to blocked with a note, or claimed by their repairs. | Its close reason says the scripts were checked and both items are closed. The scripts are outside the repository and were not read here. |
| Passes that leave a trace (yoyodyne-ifd.430.29), not parked; it waited on the dashboard's section for what is broken (yoyodyne-ps2) | 13:25, as done | Showing, among the factory's problems, a pass that left no trace, with the role that has to act on it. | The dashboard's section (yoyodyne-ps2) merged on November 20, and `TestAnUntracedPassReachesTheDashboardWithItsRoleAsMover` covers the dashboard's data. Nothing found owed. |
| One window for the operator (yoyodyne-ifd.130), not parked | 13:26, retired | Two of three remaining parts were replaced by operating rules (6de828a8, 789c052c). The third: a live view of the queue with the release and run commands. | **Still owed, and carried by another item.** The design of a live view of the team at work (yoyodyne-ifd.432.30) does not cover it: that shows who is working on what, with no queue in order and no commands. The dashboard's live work queue (yoyodyne-ifd.432.56) was admitted since, carries it, and is open. |
| Queued-merge check reading the forge's check state (yoyodyne-ifd.429.27), parked on evidence | 13:27, retired | A comparison captured from the real forge and a test that replays it. Verified check reads for pull requests 732 and 700. A record of where the comparison test really runs. | **Not done.** It was retired as obsolete: those pull requests settled long ago, and the harness's own merge queue replaced that way of merging. Whether the replay test is still wanted is the Lead Product Manager's to say. |
| Maintenance job's remaining duties (yoyodyne-ifd.434.10), not parked | 13:28, retired | Only the same work built again from current `main` (yoyodyne-ifd.434.10.1): `yoyo start` retires the job only when every duty has an owner. | Neither merged; both were retired in the same turn. The job ran only on the operator's machine and was retired on October 1, and the close reason says every duty's owner has merged. This is a recorded decision and not the same fault. |
| Repair round on a run waiting for a person's merge (yoyodyne-ifd.429.120), not parked | 13:40, retired | Everything: the decision, carrying it out, the refusal when the allowance is spent, and tests. | **Still owed, and carried by another item** of the same name (yoyodyne-ifd.429.219), open. It does have a close reason: it was merged into the design of repairing an already-landed item (yoyodyne-ifd.429.176), which is closed and settles a neighbouring case. |
| Passes of different roles side by side (yoyodyne-ifd.428.75), not parked | 15:34, as done (turn 5634) | Passes of different roles running side by side, one at a time within a conversation, and an alarm that is not critical for a wait shorter than the time between passes. | Its own run's change (pull request #1033) is not what merged. The work is on `main` through one role's pass never holding another's (yoyodyne-ifd.428.56, `aca581c1`). The alarm speaks only after a whole interval passes with no pass started (`Scheduler.missed`). The disagreement between the designs stays with the architect as scheduled-pass concurrency across the designs (yoyodyne-ifd.428.75.1), open. Not checked: whether every test the item names exists. |

Six further parked items were closed or retired in the same conversation that
afternoon. None was parked on evidence, and their close reasons say why. They are
named so that the list above is not read as every parked item closed that day:

- an agent filling a role definition is told only about the tools that
  definition holds (yoyodyne-ifd.434.30);
- converting the coordination part to the workflow runtime
  (yoyodyne-ifd.209.22);
- removing one repeated verification step shown to be equivalent
  (yoyodyne-ifd.429.54);
- the architect restating her seven replies that were cut short
  (yoyodyne-ifd.437.24);
- the architect recording the rulings of twelve passes that ended without a
  report (yoyodyne-ifd.437.32);
- building the program manager as designed (yoyodyne-ifd.430.13).

## The one the harness did close

The companion document for writing goals (yoyodyne-ifd.262) was parked on evidence
by run run-f4a6034d7a786eee2528e444fbe1e512. The harness closed it at 09:48 PDT on
September 30, because that document had a revision opening with the
item's identifier. Its notes say it owes one step: giving
`docs/product/goals/writing-goals.md` a kind the harness's document reader
accepts, once the artifact contract has one. **Still owed**: that document still
says so itself.

The search covers only items that still carry their parking. An item that was
released and then closed by a run that finished it is not in question. One whose
parking was removed some other way would not be found.
