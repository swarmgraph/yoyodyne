package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// maxReviewBoundFilesNamed is how many over-bound files a refusal names one by
// one; the rest are counted. A change too big for the reviewer can be too big to
// list, and the blocker that carries the list onto the item and the docket is
// itself bounded.
const maxReviewBoundFilesNamed = 20

// gateReviewBound measures the change the way the reviewer's copy of it is
// rendered, before any check is run on it, and refuses one whose source or test
// files that copy would keep out.
//
// Such a change cannot be approved: a source or test file the patch does not
// show refuses an approval whatever the reviewer thinks of the rest, and the
// bound is the harness's, so no repair round changes it. Before this gate the
// bound was found only once the checks had passed and a review round had been
// spent on a change that could not land. Measuring it costs one rendering of the
// diff, which is the reading the protected-path gate in front of it already
// makes of the same worktree.
//
// It is asked only where a review follows the checks — an automatically
// integrated run that is not a document publication — because a run whose
// integration a person approves is never shown to a reviewer, and a bound on a
// review that never happens refuses nothing.
//
// A change whose only omissions are test data proceeds as it always has: the
// rules allow an approval over omitted fixtures that are listed whole, and the
// review decides that.
func (a *activeRun) gateReviewBound(ctx context.Context) error {
	p := a.pipeline
	if a.state.Document != nil || !p.automatic() {
		return nil
	}
	changes, err := p.Worktrees.UnifiedChanges(ctx, a.worktree, gitworktree.DiffLimits{})
	if err != nil {
		return fmt.Errorf("measure the change against the review bound: %w", err)
	}
	over := changes.OverReviewBound()
	if len(over) == 0 {
		return nil
	}
	// Nothing was checked, so the run never left developing: the phase the gate
	// set on its way in is put back before anything records it.
	a.state.Phase = runstate.PhaseDeveloping
	return reviewBoundRefusal{over: over}
}

// reviewBoundRefusal is a change the review bound would keep source or test
// files of out of the reviewer's copy. It is its own error type because it is
// not repair input: the developer cannot make the bound larger, and splitting
// the work into smaller items is a decision about the item rather than an edit
// to the change, so it goes to the development manager rather than back to the
// developer.
type reviewBoundRefusal struct {
	over []gitworktree.OmittedFile
}

func (e reviewBoundRefusal) Error() string {
	return fmt.Sprintf("the change is too large for the reviewer's copy: %s kept out by the review bound, so it could not be approved; nothing was checked or reviewed",
		describeOverBound(e.over))
}

// describeOverBound is the over-bound files in one line: each with its size and
// the bound it was compared against, the first few by name and the rest counted.
func describeOverBound(over []gitworktree.OmittedFile) string {
	named := over
	if len(named) > maxReviewBoundFilesNamed {
		named = named[:maxReviewBoundFilesNamed]
	}
	parts := make([]string, 0, len(named)+1)
	for _, file := range named {
		parts = append(parts, overBoundFile(file))
	}
	if rest := len(over) - len(named); rest > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", rest))
	}
	noun := "files"
	if len(over) == 1 {
		noun = "file"
	}
	return fmt.Sprintf("%d source or test %s (%s)", len(over), noun, strings.Join(parts, "; "))
}

// overBoundFile is one file's size against the bound that kept it out, in the
// numbers the bound actually compared.
func overBoundFile(file gitworktree.OmittedFile) string {
	measured := fmt.Sprintf("%s, %s, %d bytes", file.Path, file.Class.Describe(), file.Bytes)
	switch {
	case file.Reason == gitworktree.OmittedTooManyFiles:
		return fmt.Sprintf("%s, past the bound of %d new files", measured, file.Bound)
	case file.Reason == gitworktree.OmittedTooLarge && file.DiffBytes == 0:
		return fmt.Sprintf("%s, over the %d-byte bound on one new file", measured, file.Bound)
	case file.Reason == gitworktree.OmittedTooLarge:
		return fmt.Sprintf("%s, diff %d bytes, over the whole %d-byte patch bound", measured, file.DiffBytes, file.Bound)
	case file.DiffBytes > 0:
		return fmt.Sprintf("%s, diff %d bytes, over what was left of the %d-byte patch bound", measured, file.DiffBytes, file.Bound)
	default:
		return fmt.Sprintf("%s, over what was left of the %d-byte patch bound", measured, file.Bound)
	}
}

// blockOnReviewBound ends a run whose change the review bound would keep source
// or test files of out of the reviewer's copy. Nothing is handed back and no
// check or review is spent: the item is blocked with the file sizes and the
// bound on it, which is what the development manager's docket entry carries,
// and the branch and worktree are preserved for whatever she decides — most
// often splitting the item.
func (a *activeRun) blockOnReviewBound(refused reviewBoundRefusal) error {
	if err := a.block(renderReviewBoundBlockerNotes(a.outcome, refused)); err != nil {
		return stoppedBy(runstate.StopReviewBound, withFailedRecord(refused, fmt.Errorf("record the change's size against the review bound as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopReviewBound, refused)
}

// renderReviewBoundBlockerNotes describes a run stopped before its checks
// because its change is too large to be reviewed. It lists every over-bound file
// it can with its size and the bound, because those numbers are what deciding
// how to split the work starts from.
func renderReviewBoundBlockerNotes(outcome Outcome, refused reviewBoundRefusal) string {
	lines := []string{
		"Yoyodyne stopped this item before checking or reviewing it: its change is too large for the reviewer's copy, so it could not be approved however sound it is.",
		"Source and test files have to be shown to the reviewer whole, and the review bound would keep the files below out. No check ran and no review round was spent.",
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"The branch and worktree are preserved. The change needs splitting into items each small enough to review, or a smaller way of doing the work.",
		"Kept out by the review bound:",
	}
	named := refused.over
	if len(named) > maxReviewBoundFilesNamed {
		named = named[:maxReviewBoundFilesNamed]
	}
	for _, file := range named {
		lines = append(lines, "- "+overBoundFile(file))
	}
	if rest := len(refused.over) - len(named); rest > 0 {
		lines = append(lines, fmt.Sprintf("- and %d more source or test files", rest))
	}
	return strings.Join(lines, "\n")
}
