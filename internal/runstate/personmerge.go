package runstate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// PersonMerge is a run's approved change waiting for a person to merge its pull
// request. Under a merge setting that leaves the merge to a person, a run that
// has published its approved change has nothing left to do with a developer
// slot, so it ends holding its item's claim and no slot, and this records the
// wait until a check of the forge finds the request merged or closed.
//
// Number is the request waited on, and Since is when the run published it and
// ended. Exactly one of the two endings is recorded once the wait is over:
//
//   - Merged: MergedAt is when the harness found the merge, and MergeCommit is
//     the commit the forge recorded as the merge of the request, where it named
//     one.
//   - Closed without merging: ClosedAt and ClosedBy are when and by whom the
//     forge recorded the closing (ClosedBy is empty where the forge named
//     nobody), and ReturnedAt is when the harness gave the item back to the
//     Lead Product Manager. The branch and checkout are kept either way.
type PersonMerge struct {
	Number      int        `json:"number"`
	Since       time.Time  `json:"since"`
	MergedAt    *time.Time `json:"merged_at,omitempty"`
	MergeCommit string     `json:"merge_commit,omitempty"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	ClosedBy    string     `json:"closed_by,omitempty"`
	ReturnedAt  *time.Time `json:"returned_at,omitempty"`
}

// maxForgeLoginBytes bounds the forge account a closing is recorded under. A
// forge login is a few dozen characters; this is room for one with plenty over.
const maxForgeLoginBytes = 256

// Merged reports a wait that ended in the request's merge.
func (p PersonMerge) Merged() bool { return p.MergedAt != nil }

// Returned reports a wait that ended in the request being closed unmerged and
// the item given back.
func (p PersonMerge) Returned() bool { return p.ReturnedAt != nil }

// Waiting reports a wait that has not ended.
func (p PersonMerge) Waiting() bool { return !p.Merged() && !p.Returned() }

// Says is the wait in the words every surface uses for it.
func (p PersonMerge) Says() string {
	return fmt.Sprintf("waiting for a person to merge pull request #%d", p.Number)
}

// Validate checks the wait is one ending at most, and that each ending carries
// what it is recorded with.
func (p PersonMerge) Validate() error {
	var problems []error
	if p.Number <= 0 {
		problems = append(problems, errors.New("person_merge number must be positive"))
	}
	if p.Since.IsZero() {
		problems = append(problems, errors.New("person_merge since is required"))
	}
	if p.Merged() && p.Returned() {
		problems = append(problems, errors.New("person_merge cannot be both merged and returned"))
	}
	if p.MergeCommit != "" && !commitPattern.MatchString(p.MergeCommit) {
		problems = append(problems, errors.New("person_merge merge_commit is invalid"))
	}
	if !p.Merged() && strings.TrimSpace(p.MergeCommit) != "" {
		problems = append(problems, errors.New("person_merge merge_commit requires merged_at"))
	}
	if p.Returned() && p.ClosedAt == nil {
		problems = append(problems, errors.New("a returned person_merge requires when the request was closed"))
	}
	if !p.Returned() && (p.ClosedAt != nil || strings.TrimSpace(p.ClosedBy) != "") {
		problems = append(problems, errors.New("person_merge closed_at and closed_by require returned_at"))
	}
	return errors.Join(problems...)
}

// WaitingForPersonMerge reports a run whose approved change waits on its pull
// request for a person to merge. Such a run is over and holds no developer
// slot, and it holds its item's claim until the forge says the request merged
// or was closed.
func (s State) WaitingForPersonMerge() bool {
	return s.PersonMerge != nil && s.PersonMerge.Waiting() && s.Retirement == nil &&
		s.Status == StatusSucceeded && s.Integration == nil && s.PullRequest != nil
}

// WaitingForPersonMerge lists every run waiting for a person to merge its pull
// request. The index chooses the records (runindex.go), so only the runs it
// names as waiting are read. A record the index could not read is left out
// rather than refusing the listing: it is reported where unreadable records
// are, and a check that misses one wait leaves that item claimed, which costs
// nothing a later check cannot settle.
func (s *Store) WaitingForPersonMerge() ([]State, error) {
	return s.indexed("waiting for a person to merge", func(entry runIndexEntry) bool { return entry.PersonMerge },
		func(unreadableRun) bool { return false }, func(state State) bool { return state.WaitingForPersonMerge() })
}

// PersonMergeMerged records a wait ended by the request's merge, as the forge
// check settles it after the item is settled: the wait's end and the merge
// commit, the request recorded merged, and the item's disposition where the
// run's landing did not discharge it. The merge setting stops being what the
// run is recorded as ended by, because its change landed.
//
// The run is recorded as integrated through its pull request, exactly as a
// landing the forge merged for the harness is: the published head as both the
// promoted and the target commit, the run's base as the commit it moved from,
// and the forge's merge commit on the pull request. No merge method is
// recorded, because the harness did not ask for this merge. The run is put at
// its cleanup, which the sweep that settled it finishes (completeIntegrated in
// internal/orchestrator), since a run complete with an integration is one
// whose branch and checkout are gone. A record naming no target branch, or a
// head that is its own base, has no promotion to describe and is left without
// one.
type PersonMergeMerged struct {
	Number      int
	MergeCommit string
	// ForgeState is the request's state as the forge named it.
	ForgeState string
	// LandingBlockedBy and LandingImpedimentProblem are the disposition the
	// settlement of an undischarged item arrived at (State's fields of the
	// same names), carried onto the run as the settlement left them.
	LandingBlockedBy         string
	LandingImpedimentProblem string
	At                       time.Time
}

func (u PersonMergeMerged) apply(state *State) (bool, error) {
	if !state.WaitingForPersonMerge() || state.PersonMerge.Number != u.Number {
		return false, fmt.Errorf("run %s is not waiting for a person to merge pull request %d, so the merge is not recorded on it", state.RunID, u.Number)
	}
	wait := *state.PersonMerge
	at := u.At
	wait.MergedAt = &at
	wait.MergeCommit = u.MergeCommit
	state.PersonMerge = &wait
	published := *state.PullRequest
	if u.ForgeState != "" {
		published.State = u.ForgeState
	}
	published.Merged = true
	if u.MergeCommit != "" {
		published.MergeCommit = u.MergeCommit
	}
	state.PullRequest = &published
	state.LandingBlockedBy = u.LandingBlockedBy
	state.LandingImpedimentProblem = u.LandingImpedimentProblem
	if state.StopClass == StopIntegrationPolicy {
		state.StopClass = ""
	}
	head := published.HeadCommit
	if target := strings.TrimSpace(state.TargetBranch); target != "" && commitPattern.MatchString(head) && commitPattern.MatchString(state.BaseCommit) && head != state.BaseCommit {
		state.Integration = &Integration{
			TargetBranch:         target,
			SourceCommit:         head,
			TargetCommit:         head,
			PreviousTargetCommit: state.BaseCommit,
			ThroughPullRequest:   true,
		}
		state.Phase = PhaseCleaningUp
	}
	state.UpdatedAt = u.At
	return true, nil
}

// PersonMergeReturned records a wait ended by the request being closed without
// merging, as the forge check settles it after the item is given back: who
// closed it and when, and when the item was given back.
type PersonMergeReturned struct {
	Number     int
	ClosedBy   string
	ClosedAt   time.Time
	ForgeState string
	At         time.Time
}

func (u PersonMergeReturned) apply(state *State) (bool, error) {
	if !state.WaitingForPersonMerge() || state.PersonMerge.Number != u.Number {
		return false, fmt.Errorf("run %s is not waiting for a person to merge pull request %d, so its closing is not recorded on it", state.RunID, u.Number)
	}
	wait := *state.PersonMerge
	closedAt, at := u.ClosedAt, u.At
	wait.ClosedAt = &closedAt
	wait.ClosedBy = u.ClosedBy
	wait.ReturnedAt = &at
	state.PersonMerge = &wait
	published := *state.PullRequest
	if u.ForgeState != "" {
		published.State = u.ForgeState
	}
	published.Merged = false
	state.PullRequest = &published
	state.UpdatedAt = u.At
	return true, nil
}
