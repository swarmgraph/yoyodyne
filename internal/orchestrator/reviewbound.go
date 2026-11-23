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

// reviewDiffLimits is the limits on the reviewer's copy of a change, as the
// project's configuration sets them (review_limits). The size check below, the
// rendering a review is shown, and the limits the developer's contract states
// all read them from here, so a change the check lets through is the change the
// reviewer is shown whole, and the numbers the developer is told are the ones it
// is held to. They are the
// project's and never a run's: nothing in a run's own state changes them, and a
// run reads them from the configuration it runs under.
func (p Pipeline) reviewDiffLimits() gitworktree.DiffLimits {
	return p.Config.ReviewLimits.DiffLimits()
}

// gateReviewBound measures the change the way the reviewer's copy of it is
// rendered, before any check is run on it, and refuses one whose source or test
// files that copy would keep out.
//
// Such a change cannot be approved: a source or test file the patch does not
// show refuses an approval whatever the reviewer thinks of the rest, and the
// bound is the project's, not the run's, so no review round changes it; only a
// smaller change does, which is why the first refusal goes back to the
// developer once (repairLoop). The bound is measured here, not found after the
// checks have passed and a review round has been spent on a change that could
// not land. Measuring it costs one rendering of the
// diff, which is the reading the protected-path gate in front of it already
// makes of the same worktree.
//
// It is asked of every run whose checks a review follows, which is every run
// that is not a document publication, whichever integration policy it runs
// under.
//
// A change whose only omissions are test data proceeds as it always has: the
// rules allow an approval over omitted fixtures that are listed whole, and the
// review decides that.
func (a *activeRun) gateReviewBound(ctx context.Context) error {
	p := a.pipeline
	if a.state.Document != nil {
		return nil
	}
	changes, err := p.Worktrees.UnifiedChanges(ctx, a.worktree, p.reviewDiffLimits())
	if err != nil {
		return fmt.Errorf("measure the change against the review bound: %w", err)
	}
	over := changes.OverReviewBound()
	if len(over) == 0 {
		// The change fits the reviewer's copy now, so a refusal an earlier attempt
		// was handed no longer describes it.
		a.state.ReviewBoundRefusal = nil
		return nil
	}
	// Nothing was checked, so the run never left developing: the phase the gate
	// set on its way in is put back before anything records it.
	a.state.Phase = runstate.PhaseDeveloping
	return reviewBoundRefusal{over: over}
}

// reviewBoundRefusal is a change the review bound would keep source or test
// files of out of the reviewer's copy. It is its own error type because it is
// repair input only once: the first refusal on a run goes back to the developer,
// who can often make the change fit — a generated file left out, a fixture
// recorded as test data, a smaller edit — and every later one goes to the
// development manager, because splitting the work is a decision about the item
// rather than an edit to the change (docs/decisions/review-size-limits.md).
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
	if err := a.block(renderReviewBoundBlockerNotes(a.outcome, refused, a.state.ReviewBoundReturned)); err != nil {
		return stoppedBy(runstate.StopReviewBound, withFailedRecord(refused, fmt.Errorf("record the change's size against the review bound as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopReviewBound, refused)
}

// renderReviewBoundBlockerNotes describes a run stopped before its checks
// because its change is too large to be reviewed. It lists every over-bound file
// it can with its size and the bound, because those numbers are what deciding
// how to split the work starts from.
func renderReviewBoundBlockerNotes(outcome Outcome, refused reviewBoundRefusal, returned bool) string {
	lines := []string{
		"Yoyodyne stopped this item before checking or reviewing it: its change is too large for the reviewer's copy, so it could not be approved however sound it is.",
		"Source and test files have to be shown to the reviewer whole, and the review bound would keep the files below out. No check ran and no review round was spent.",
	}
	if returned {
		lines = append(lines, "The change was already handed back to its developer once to be made to fit, and it still does not.")
	}
	lines = append(lines,
		"Run: "+outcome.RunID,
		"Branch: "+outcome.Branch,
		"Worktree: "+outcome.WorktreePath,
		"The branch and worktree are preserved. The change needs splitting into items each small enough to review, or a smaller way of doing the work.",
		"Kept out by the review bound:",
	)
	recorded := recordedReviewBoundRefusal(refused)
	for _, file := range recorded.Files {
		lines = append(lines, "- "+file)
	}
	if recorded.Omitted > 0 {
		lines = append(lines, fmt.Sprintf("- and %d more source or test files", recorded.Omitted))
	}
	return strings.Join(lines, "\n")
}

// recordedReviewBoundRefusal is the refusal as the run's record keeps it: the
// first files by name, each with its size and the limit it was compared
// against, and the rest counted.
func recordedReviewBoundRefusal(refused reviewBoundRefusal) runstate.ReviewBoundRefusal {
	named := refused.over
	if len(named) > maxReviewBoundFilesNamed {
		named = named[:maxReviewBoundFilesNamed]
	}
	recorded := runstate.ReviewBoundRefusal{Files: make([]string, 0, len(named)), Omitted: len(refused.over) - len(named)}
	for _, file := range named {
		recorded.Files = append(recorded.Files, overBoundFile(file))
	}
	return recorded
}

// recordReviewBoundRefusal makes the size check's refusal the run's outstanding
// repair input and marks the run as having been handed it, so a later refusal
// stops the run. The size check is decided after the protected-path check and
// in front of the developer's execution record and the checks, so what those
// later gates recorded no longer describes the change and is cleared, as the
// path refusal clears it.
func (a *activeRun) recordReviewBoundRefusal(refused reviewBoundRefusal) {
	a.clearReviewEvidence()
	a.state.CheckFailure = nil
	a.state.ChecksPassed = nil
	a.state.ReplayConflict = nil
	recorded := recordedReviewBoundRefusal(refused)
	a.state.ReviewBoundRefusal = &recorded
	a.state.ReviewBoundReturned = true
}

// reviewBoundRepairPrompt hands a change the size check refused back to the
// developer that produced it, once. It lists each file kept out with its size
// and the limit it was compared against, which are the numbers making the
// change fit starts from, and it says that a second refusal stops the run, so a
// developer that cannot make the change smaller says so rather than trying
// again.
func reviewBoundRepairPrompt(invariants, scratchDirectory string, checks []string, limits gitworktree.DiffLimits, refusal runstate.ReviewBoundRefusal, attempt, limit int) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract(scratchDirectory, checks, limits))
	prompt.WriteString("\n\n")
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString("# Too large to review: repair required\n\n")
	fmt.Fprintf(&prompt, "Your change is too large for the reviewer's copy, so it was refused before any check ran and before it reached a reviewer: source and test files have to be shown to the reviewer whole, and the size limits would keep the files below out. This is repair attempt %d of %d. Continue the change already in your worktree instead of starting over, and make it fit.\n\n", attempt, limit)
	prompt.WriteString("Kept out by the size limits:\n\n")
	for _, file := range refusal.Files {
		prompt.WriteString("- " + file + "\n")
	}
	if refusal.Omitted > 0 {
		fmt.Fprintf(&prompt, "- and %d more source or test files not listed here\n", refusal.Omitted)
	}
	prompt.WriteString("\nA change can often be made to fit: leave out a generated or vendored file, record a large fixture as test data under a testdata directory, or do the work with a smaller edit. This change is handed back once. If it is refused again, the run stops and goes to the development manager, most often to split the item, with nothing checked or reviewed. If the change cannot be made smaller and still do what the item asks, leave it as it is and say so and why in your summary. Finish with a concise summary of the whole change.")
	return prompt.String()
}

// reviewBoundContract is the section of the developer's contract that states
// the size limits the run is held to, beside what the developer verifies before
// submitting. It is built from the limits the reviewer's copy is rendered with,
// the project's review_limits as reviewDiffLimits reads them, so the numbers the
// developer is told are the ones the size check compares.
func reviewBoundContract(limits gitworktree.DiffLimits) string {
	resolved := limits.Resolved()
	return fmt.Sprintf(`# How large a change the reviewer can be shown

Before any check runs, the harness measures your change the way the reviewer's copy of it is rendered. Every source and test file has to fit in that copy whole. The limits are: %d KiB of diff for the whole change; %d KiB for one new file; and %d new files. A change that would keep a source or test file out of the copy is refused before it is checked or reviewed. Test data and generated files — under a testdata or fixtures directory, golden files, lock files, files marked as generated — are not held to them. The first refusal on a run is handed back to you, with each file kept out and its size, and spends one repair attempt; a second refusal stops the run and goes to the development manager. Keep generated and vendored files out of the change, and check its size before you hand it over.`,
		resolved.MaxTotalBytes>>10, resolved.MaxFileBytes>>10, resolved.MaxFiles)
}
