# yoyodyne-ifd.209.26: every route that closes a work item, and the one that closed ifd.284

yoyodyne-ifd.284 was closed on 2026-09-06 by `run-9ad1799e6a729458202a40ea9bb40d01`,
whose entire change was a diagnosis saying the machinery the item is written
against had not landed. That is the false-closure class yoyodyne-ifd.209.23 and
yoyodyne-ifd.209.24 shipped the landing claim to end, recurring after both of
them shipped. This item was admitted to establish which of two things it was: a
run that predated the guards, or a closure route that bypasses them.

**It is neither.** The run postdates the guards, every closure route consulted
the landing, and the item closed anyway — because the claim they consulted was
the default, and the default is the one claim nobody is ever shown.

## The run postdates the guards

`run-9ad1799e` recorded `build: 262372c74597b9fc6da01b9a9039030c849fc56c`. Both
guard commits are ancestors of it:

```sh
git merge-base --is-ancestor 8170101 262372c   # ifd.209.23, exit 0
git merge-base --is-ancestor 0f77fd9 262372c   # ifd.209.24, exit 0
```

So the harness that ran it could withhold a closure, and had been able to for
sixteen hours.

## Every route that closes a work item

When this was written there were four calls in this repository that close
one, in three declarations that settle a run and one that carries out a
person's decision. Each was read against the run's landing claim. A fifth was
added by yoyodyne-ifd.367 on 2026-09-20 for an item no run carries, and a
sixth by yoyodyne-ifd.366 the same day, which decides nothing — it is the
conversation's tracker with the recovery rule around it, forwarding the
person's decision and asking again while the store fails recoverably; both are
listed last:

| Route | What it settles | What it consults |
| --- | --- | --- |
| `(*activeRun) complete` — `internal/orchestrator/pipeline.go` | a run settling its own item once its promotion is where it will stay | `state.LandingDischarges()`; an undischarged landing goes to `settleUndischarged` instead |
| `(Reconciler) closeSettledMerge` — `internal/orchestrator/reconcile.go` | a run whose queued merge the forge has since performed | `state.LandingDischarges()`, read back from the durable record the ended run left |
| `(Reconciler) completeIntegrated` — `internal/orchestrator/reconcile.go` | a run somebody interrupted after its change was promoted | `landingSettled` and `state.LandingDischarges()` |
| `(*Session) carryOutTrackerAction` — `internal/chat/tracker.go` | the product manager closing or retiring an item in conversation | nothing, and correctly: nothing integrated and no developer claimed anything |
| `(ConversationLander) Settle` — `internal/orchestrator/conversationlanding.go` | an item a conversation carries, once its landing is in the repository | the owner's own record rather than a run's claim: a revision of a document the item's role owns, by that role, whose reason opens with the item's identifier, and never for a parked item ([the 367 diagnosis](yoyodyne-ifd-367-conversation-item-dispatched.md)) |
| `(recoveringTracker) Complete` — `internal/chat/trackerrecovery.go` | nothing of its own: it forwards the conversation's closure above to the configured tracker, asking again while the store fails recoverably | nothing, for the same reason the route it forwards consults nothing; the only path to it is `carryOutTrackerAction` |

ifd.284 was closed by the second of these — its notes carry
`Yoyodyne settled the merge this run left queued with the forge` — and that route
did consult the claim. `TestEveryRouteThatClosesAWorkItemIsAudited` in
`internal/landing/closure_audit_test.go` holds this table to the code: a
closure the table does not list, or a settlement that stops asking, fails a
check rather than being found by the next diagnosis.

## What actually happened

The run's durable record carries no `landing_outcome`, no `landing_reason`, and
no `landing_problem`. The developer wrote no block at all, so the zero claim was
in force, and the zero claim discharges.

Its two developer invocations both ended on an interim line rather than on an
account of the work — `"The check is still running ... I'll report when it
completes."` and `` "`make check` (fmtcheck, test, race, vet) is running; it takes
several minutes. I'll report when it lands." `` The developer never reached the
point in its own reply where the block goes.

The reviewer, meanwhile, approved the change while writing in its own summary
that it was *"offered as evidence rather than implementation"*. It had no reason
to connect that to the item: `Claim.Describe()` answered with nothing for a claim
nobody made, so the review prompt carried no **Claimed landing outcome** section,
and the reviewer was never told that approving this change closed the item.

Across the 290 succeeded-and-integrated runs on this machine, 6 claimed
`evidence` and 284 claimed nothing. **No run has ever claimed `discharged`
explicitly.** The claim that closes items is, in practice, the claim nobody
writes — and it was the only one nobody read.

## What ifd.209.26 changed

The default is described like any other claim. `Claim.Describe()` answers for the
unmade claim too, so every review prompt carries the landing in force. What a
reviewer does with a landing that closes the item — approve only if the change is
the work the item asked for, and ask for changes where it is a diagnosis — is
added by `describeLanding` on the review-prompt path rather than by the claim
itself: the same words are written onto the work item, where a direction to ask
for changes is addressed to nobody who can take one. Replaying the ifd.284 shape
now costs a repair round, in which the developer claims the evidence it actually
landed, and the item is parked rather than closed
(`TestADiagnosisWithNoClaimIsSentBackRatherThanClosingItsItem`;
`TestTheReviewersDirectionNeverReachesTheWorkItem` holds the two audiences
apart).

## What this does not do

It does not make the default safe against a reviewer that approves anyway. The
closure still rests on a claim no developer typed, and the only reader that can
tell a diagnosis from an implementation is a judgement rather than a gate. The
mechanical alternative — requiring every reply to claim its landing explicitly —
was measured and left alone: with no run in 290 having ever written `discharged`,
flipping the default would park every ordinary run whose developer omitted the
block, which is a change to the protocol ifd.209.23 deliberately chose and is the
product manager's to decide rather than a developer run's.
