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
- **None of it is on main.** This run's base, `33d59a62`, is the parent's base
  `d19d1b7b` plus the non-goals revision (yoyodyne-48j), and has none of the
  functions above. The command-line path depends on the parent's conversation
  path from its first attempt (`e50c2ff1`), so it cannot be taken alone.
- **The parent stopped over documents, not code.** Its reviewer asked for repair
  twice. The last review found the code met the item, with every check passing,
  and asked for three passages to be corrected: `README.md` and
  `docs/conversation.md` still tell the operator to commit an approved document,
  and `docs/artifacts.md` still says an approval lands in the checkout
  uncommitted. With its two repairs used, the parent was stopped and its branch
  kept for a replan.

## Why this run does not redo the parent

Landing the child here means carrying the parent's whole change, about 900 lines
across 24 files, into a second branch. Pull request #1205 would then conflict
with it, and two changes would claim the same work. Finishing the parent is the
smaller step: correct the three passages above on its kept branch, and the child
is met when it merges.

## What is left to decide

The child asks that no approval path write into the checkout. The parent's
branch still writes the approval into the checkout in two cases where no
reviewed run can carry it: a project with no reviewer configured, and one that
keeps its intent in a separate repository. Whoever finishes the parent either
keeps those two exceptions, documented as they are, and the child is narrowed
to match, or makes the command refuse in those cases and say what to set up
instead.
