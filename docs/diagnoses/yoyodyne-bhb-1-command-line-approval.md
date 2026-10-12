# yoyodyne-bhb.1: why moving `yoyo artifact approve` onto a reviewed run waits for the approval-landing fix

Moving `yoyo artifact approve` onto a reviewed run, so it stops writing approvals
into the primary checkout (yoyodyne-bhb.1), was admitted as a child of the
approval-landing fix (yoyodyne-bhb). This records why the child cannot land on
its own, and what lets it close. All times are Pacific (PDT), on October 12 2026.

## The answer

- **The child's code is already written, on the parent's branch.** The third
  attempt at the approval-landing fix, commit `dbbaf12f` at 13:48 on branch
  `yoyodyne/yoyodyne-bhb/54ddd614` (pull request #1205), moves the command-line
  approval onto a run. `Store.PrepareStandingApproval` in
  `internal/artifact/approval.go` renders the approved document without writing
  it; `Pipeline.PublishStandingApproval` in
  `internal/orchestrator/documentpublication.go` lands it through a run with its
  own checks and an independent review; `DocumentPublication.StandingApproval`
  in `internal/runstate/document.go` lets such a run carry no conversation; and
  `approveThroughRun` in `internal/cli/artifact.go` says which run carries the
  approval and what it came to, or, if the run stops without landing it, that
  the approval should be given again. The test
  `TestStandingApprovalLandsThroughReviewedRunAndLeavesCheckoutClean` covers a
  command-line approval landing through a run with the checkout left clean.
- **None of it is on main.** Main, up to and including the merge of the
  document-deadline fix (yoyodyne-trt, `2e6a4a43`), has none of the functions
  above; the parent's branch was cut
  from `d19d1b7b`, and nothing merged to main since carries its change. The
  command-line path depends on the parent's conversation path from its first
  attempt (`e50c2ff1`), so it cannot be taken alone.
- **The parent's remaining documents are now corrected.** Its third attempt was
  stopped after its reviewer found the code met the item, with every check
  passing, but asked for three passages to be corrected: `README.md` and
  `docs/conversation.md` told the operator to commit an approved document, and
  `docs/artifacts.md` said an approval lands in the checkout uncommitted. A
  fourth attempt on the same branch, commit `6645c6f3` at 14:48, corrects all
  three. What remains on the parent is for that attempt to be reviewed and pull
  request #1205 merged.

## Why this run does not redo the parent

Landing the child here means carrying the parent's whole change, about 900 lines
across 24 files, into a second branch. Pull request #1205 would then conflict
with it, and two changes would claim the same work. Nothing needs to be redone:
this item is met when pull request #1205 is reviewed and merged, apart from the
decision below.

## What is left to decide

The child asks that no approval path write into the checkout. The parent's
branch still writes the approval into the checkout in two cases where no
reviewed run can carry it: a project with no reviewer configured, and one that
keeps its intent in a separate repository, and its corrected `README.md` now
describes both. Whoever finishes the parent either keeps those two exceptions,
and the child is narrowed to match, or makes the command refuse in those cases
and say what to set up instead.
