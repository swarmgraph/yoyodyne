package runstate

// The run that resolves a conflict between the team's branch and the target
// branch (docs/designs/integrating-to-a-local-branch.md, section 3). It is a run
// of the harness's own recorded under a bookkeeping item of its own, which
// invokes a developer in a checkout cut from a commit that carries the conflict
// as Git left it, and which ends once its change is checked, independently
// reviewed and committed: it integrates nothing. What the update then builds
// from the approved head is the update's business (TeamBranchUpdateAttempt).

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// TeamBranchResolutionItemPrefix opens the bookkeeping item of every run that
// resolves a conflict, and no work item the tracker holds.
const TeamBranchResolutionItemPrefix = "teamresolve-"

// MaxTeamBranchResolutionPaths and MaxTeamBranchResolutionFieldBytes bound the
// conflicting paths and the identifiers a resolution run keeps.
const (
	MaxTeamBranchResolutionPaths      = 200
	MaxTeamBranchResolutionFieldBytes = 1024
)

// TeamBranchResolutionRun is set on a run that resolves a conflict on a target
// branch.
type TeamBranchResolutionRun struct {
	TargetBranch   string `json:"target_branch"`
	UpstreamBranch string `json:"upstream_branch"`
	// TeamCommit is the tip of the team's branch whose merge conflicts, and
	// TargetBase the target commit it was to be merged onto.
	TeamCommit string `json:"team_commit"`
	TargetBase string `json:"target_base"`
	// ConflictedBase is the commit the run's checkout is cut from: the target
	// commit with the merge applied as Git left it, conflict markers included,
	// as a commit whose only parent is the target commit.
	ConflictedBase string `json:"conflicted_base"`
	// Paths are the files that conflict.
	Paths []string `json:"paths"`
	// WorkItemID is the item the development manager named. The run is recorded
	// under a bookkeeping item of its own, and nothing it does changes this one
	// beyond a note.
	WorkItemID string `json:"work_item_id"`
	// Round counts from one.
	Round int `json:"round"`
	// Attempt counts the runs of this conflict that were ended for a cause
	// outside the work (TeamBranchResolution.Restarts), so a round started again
	// after one is a run of its own rather than the ended one found again.
	Attempt int `json:"attempt,omitempty"`
	// ApprovedHead is the head of the run's change once it passed its checks and
	// the independent review, and empty until it did.
	ApprovedHead string `json:"approved_head,omitempty"`
}

// TeamBranchResolutionRunID is the run that makes one round of resolving one
// conflict. It is derived rather than drawn, so a round started and a round
// found again after a restart name the same run.
func TeamBranchResolutionRunID(targetBranch, teamCommit, targetBase string, round, attempt int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "team branch resolution\x00%s\x00%s\x00%s\x00%d\x00%d", targetBranch, teamCommit, targetBase, round, attempt))
	return fmt.Sprintf("run-%x", sum[:16])
}

// RunID is the run that makes this round.
func (t TeamBranchResolutionRun) RunID() string {
	return TeamBranchResolutionRunID(t.TargetBranch, t.TeamCommit, t.TargetBase, t.Round, t.Attempt)
}

// WorkItemBookkeepingID is the bookkeeping item the run is recorded under.
func (t TeamBranchResolutionRun) WorkItemBookkeepingID() string {
	return TeamBranchResolutionItemPrefix + strings.TrimPrefix(t.RunID(), "run-")
}

// TeamBranchResolutionRunOfItem is the run a bookkeeping item names, and false
// for a work item the tracker holds.
func TeamBranchResolutionRunOfItem(id string) (string, bool) {
	rest, ok := strings.CutPrefix(id, TeamBranchResolutionItemPrefix)
	if !ok || !runIDPattern.MatchString("run-"+rest) {
		return "", false
	}
	return "run-" + rest, true
}

// Validate checks the record names its branches and commits and a round.
func (t TeamBranchResolutionRun) Validate() error {
	var problems []error
	if !validLocalBranch(t.TargetBranch) {
		problems = append(problems, fmt.Errorf("target_branch %q is not a local branch name", t.TargetBranch))
	}
	if t.UpstreamBranch != "" && !validLocalBranch(t.UpstreamBranch) {
		problems = append(problems, fmt.Errorf("upstream_branch %q is not a local branch name", t.UpstreamBranch))
	}
	// In a fixed order, so that what a record is refused for reads the same each
	// time it is asked.
	for _, commit := range []struct{ name, value string }{{"team_commit", t.TeamCommit}, {"target_base", t.TargetBase}, {"conflicted_base", t.ConflictedBase}} {
		if !commitPattern.MatchString(commit.value) {
			problems = append(problems, fmt.Errorf("%s is not a full commit", commit.name))
		}
	}
	if t.ApprovedHead != "" && !commitPattern.MatchString(t.ApprovedHead) {
		problems = append(problems, errors.New("approved_head is not a full commit"))
	}
	if len(t.Paths) == 0 {
		problems = append(problems, errors.New("a resolution run names the paths that conflict"))
	}
	if len(t.Paths) > MaxTeamBranchResolutionPaths {
		problems = append(problems, fmt.Errorf("a resolution run names %d paths, more than the %d it keeps", len(t.Paths), MaxTeamBranchResolutionPaths))
	}
	for _, path := range t.Paths {
		if path == "" || len(path) > MaxTeamBranchResolutionFieldBytes {
			problems = append(problems, fmt.Errorf("a resolution run's path is empty or longer than %d bytes", MaxTeamBranchResolutionFieldBytes))
			break
		}
	}
	if strings.TrimSpace(t.WorkItemID) == "" || len(t.WorkItemID) > MaxTeamBranchResolutionFieldBytes {
		problems = append(problems, errors.New("a resolution run names the work item the development manager chose, by an identifier of reasonable length"))
	}
	if t.Round < 1 || t.Round > MaxTeamBranchResolutionRounds {
		problems = append(problems, fmt.Errorf("round %d is outside 1 to %d", t.Round, MaxTeamBranchResolutionRounds))
	}
	return errors.Join(problems...)
}
