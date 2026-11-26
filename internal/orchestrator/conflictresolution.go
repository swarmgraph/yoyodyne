package orchestrator

// The session that resolves a conflict handed back to a run.
//
// This is for a change a review approved; a conflict found before review goes
// on in the session that wrote the change (ciconflict.go).
//
// Each attempt a run gets to bring its approved change up to date is made in a
// new developer session rather than the one that wrote the change: after hours in
// the queue the provider no longer keeps that session's text, so resuming it
// pays to write all of it again. The new session is given what the old one would
// have known and this attempt needs: the work item with its goals and
// invariants, the approved change and its approval, the conflicts as Git left
// them in the checkout, and what the target's own commits did to those files.
// Both sessions are authors of the change from then on (runstate.AuthorSession).

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// maxConflictCommits bounds how many of the target's commits the session is
// listed; the newest are kept, and it is told how many were left out.
const maxConflictCommits = 100

// keepApproved copies the approval and the approved commits onto the outstanding
// conflict before the move onto the target discards them. A move made again
// after an interruption keeps what the first one copied.
func (a *activeRun) keepApproved() {
	if a.state.ReplayConflict == nil || a.state.ReplayConflict.Approved != nil || a.state.ReviewDecision != string(review.DecisionApprove) {
		return
	}
	conflict := *a.state.ReplayConflict
	conflict.Approved = &runstate.ApprovedChange{
		BaseCommit: a.state.BaseCommit, HeadCommit: a.state.HarnessCommit,
		ReviewSessionID: a.state.ReviewSessionID, Approves: a.state.ReviewApproves, Summary: a.state.ReviewSummary,
	}
	a.state.ReplayConflict = &conflict
}

// noteMoved records the conflict as moved onto the target, with the commits the
// change lay between and, where the move met conflicts, the target it met them
// on and the files, which can have moved on since the conflict was first found.
func (a *activeRun) noteMoved(rebase gitworktree.Rebase) {
	conflict := *a.state.ReplayConflict
	conflict.Moved = true
	if conflict.Approved != nil && rebase.PreviousHeadCommit != "" {
		approved := *conflict.Approved
		approved.BaseCommit, approved.HeadCommit = rebase.PreviousBaseCommit, rebase.PreviousHeadCommit
		conflict.Approved = &approved
	}
	if len(rebase.Conflicts) > 0 {
		conflict.TargetCommit = rebase.BaseCommit
		conflict.Paths, conflict.Omitted = runstate.BoundConflictPaths(rebase.Conflicts)
	}
	a.state.ReplayConflict = &conflict
}

// conflictResolutionPrompt is what the session that resolves the outstanding
// conflict is given. A resumed attempt is given it again.
func (a *activeRun) conflictResolutionPrompt(ctx context.Context) string {
	p, conflict := a.pipeline, *a.state.ReplayConflict
	var prompt strings.Builder
	prompt.WriteString(developerContract(a.scratch, p.Config.Checks, p.reviewDiffLimits()))
	prompt.WriteString("\n\n")
	prompt.WriteString(personaSection(p.developer().Persona.Text))
	prompt.WriteString(deliveredInvariantSection(a.deliveredInvariants().Text()))
	prompt.WriteString("# Integration conflict: bring the change level with the target\n\n")
	fmt.Fprintf(&prompt, "The change for the work item below was written by another session, passed its checks and was approved by an independent reviewer. %s moved before the change landed, and the two no longer merge. The change has been moved onto %s for you, and the parts that would not merge are in your worktree between Git's conflict markers.\n\n", conflict.TargetBranch, conflict.TargetBranch)
	prompt.WriteString("Resolve every conflict, keeping what the target branch does and what the change does, and leave no conflict markers behind. Change nothing else, unless the resolved code would not otherwise build or pass its tests. Do not start the work over: everything else the change does is already in your worktree, and so is everything the target did.\n\n")
	fmt.Fprintf(&prompt, "This is attempt %d of the %d this run gets to bring the approved change level with the target branch. It is not counted against your repair attempts, and a conflict after the last of them stops the run for the development manager. ", max(a.state.ReconciliationsUsed(), 1), runstate.MaxReconciliations)
	prompt.WriteString("The checks and an independent review both run again on what you produce. If the two cannot both stand, because the target did this work differently or made it unnecessary, say so plainly in your summary rather than forcing a resolution.\n\n")
	fmt.Fprintf(&prompt, "Target branch: %s\n", conflict.TargetBranch)
	if conflict.TargetCommit != "" {
		fmt.Fprintf(&prompt, "Your worktree now sits on: %s\n", conflict.TargetCommit)
	}
	prompt.WriteString("\n## The conflicting files\n\n")
	if len(conflict.Paths) == 0 {
		prompt.WriteString("Git named none; look for what would not merge with `git diff`.\n")
	}
	for _, path := range conflict.Paths {
		prompt.WriteString("- " + path + "\n")
	}
	if conflict.Omitted > 0 {
		fmt.Fprintf(&prompt, "- and %d further path(s) not listed here\n", conflict.Omitted)
	}
	if conflict.Detail != "" {
		prompt.WriteString("\nWhat Git reported when the conflict was found:\n\n")
		writeFenced(&prompt, "", conflict.Detail)
	}
	a.writeTargetCommits(ctx, &prompt, conflict)
	a.writeApprovedChange(ctx, &prompt, conflict)
	prompt.WriteString("\n")
	prompt.WriteString(a.context)
	return prompt.String()
}

// writeTargetCommits lists the target's commits since the change's base that
// touch the conflicting files.
func (a *activeRun) writeTargetCommits(ctx context.Context, prompt *strings.Builder, conflict runstate.ReplayConflict) {
	fmt.Fprintf(prompt, "\n## What %s did to those files since the change's base\n\n", conflict.TargetBranch)
	if conflict.Approved == nil || conflict.Approved.BaseCommit == "" || conflict.TargetCommit == "" || len(conflict.Paths) == 0 {
		prompt.WriteString("The run's record does not name the commits. Read the target's history with `git log`.\n")
		return
	}
	commits, omitted, err := a.pipeline.Worktrees.CommitsTouching(ctx, conflict.Approved.BaseCommit, conflict.TargetCommit, conflict.Paths, maxConflictCommits)
	if err != nil {
		fmt.Fprintf(prompt, "The commits between %s and %s could not be listed (%v). Read them with `git log`.\n", conflict.Approved.BaseCommit, conflict.TargetCommit, err)
		return
	}
	for _, commit := range commits {
		fmt.Fprintf(prompt, "- %s %s\n", commit.Commit, commit.Subject)
	}
	if omitted > 0 {
		fmt.Fprintf(prompt, "- and %d older commit(s) not listed here\n", omitted)
	}
	if len(commits) == 0 {
		prompt.WriteString("None.\n")
	}
	prompt.WriteString("\nRead what each one did with `git show <commit> -- <file>`.\n")
}

// writeApprovedChange gives the approved change as a diff, with its approval.
func (a *activeRun) writeApprovedChange(ctx context.Context, prompt *strings.Builder, conflict runstate.ReplayConflict) {
	prompt.WriteString("\n## The approved change\n\n")
	approved := conflict.Approved
	if approved == nil {
		prompt.WriteString("The run's record does not hold the approval or the commits of the approved change. The whole change is in your worktree, uncommitted above the target.\n")
		return
	}
	says := "The independent reviewer approved it"
	if approved.ReviewSessionID != "" {
		says += " in session " + approved.ReviewSessionID
	}
	if approved.Approves != "" {
		says += ", as " + approved.Approves
	}
	if summary := strings.TrimSpace(approved.Summary); summary != "" {
		says += ", and said:\n\n" + summary
	} else {
		says += ", and left no summary."
	}
	prompt.WriteString(says + "\n\n")
	if approved.BaseCommit == "" || approved.HeadCommit == "" {
		prompt.WriteString("The run's record does not name the commits the change lay between. The whole change is in your worktree, uncommitted above the target.\n")
		return
	}
	changes, err := a.pipeline.Worktrees.RangeChanges(ctx, approved.BaseCommit, approved.HeadCommit, a.pipeline.reviewDiffLimits())
	if err != nil {
		fmt.Fprintf(prompt, "The diff from %s to %s could not be read (%v). The whole change is in your worktree, uncommitted above the target.\n", approved.BaseCommit, approved.HeadCommit, err)
		return
	}
	fmt.Fprintf(prompt, "As it was approved, from %s to %s:\n\n", approved.BaseCommit, approved.HeadCommit)
	writeFenced(prompt, "", changes.Status)
	writeFenced(prompt, "diff", changes.Patch)
	var absent []string
	for _, file := range changes.OmittedFiles {
		absent = append(absent, file.Path)
	}
	for _, file := range changes.DeletedFiles {
		absent = append(absent, file.Path)
	}
	slices.Sort(absent)
	for _, path := range slices.Compact(absent) {
		fmt.Fprintf(prompt, "- not shown in the diff: %s; read it with `git diff %s %s -- %s`\n", path, approved.BaseCommit, approved.HeadCommit, path)
	}
}

// writeFenced writes text in a fence longer than any run of backticks in it, so
// text that holds a fence of its own cannot end this one early.
func writeFenced(prompt *strings.Builder, language, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	prompt.WriteString(fence + language + "\n" + strings.TrimRight(text, "\n") + "\n" + fence + "\n")
}
