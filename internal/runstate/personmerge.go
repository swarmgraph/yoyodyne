package runstate

import (
	"errors"
	"fmt"
	"sort"
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
	// UpdateRefused is why the harness cannot bring the request up to date with
	// a target that moved past it, recorded once by the forge check when nothing
	// the harness does will change the answer (PersonMergeUpdateRefused): the
	// run's worktree or branch is gone, or its record carries no approval or no
	// developer session. While it is set the wait is the development manager's
	// to decide, not only the person's to merge.
	UpdateRefused string `json:"update_refused,omitempty"`
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
	if len(p.UpdateRefused) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("person_merge update_refused exceeds %d bytes", MaxSelectionReasonBytes))
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

// MaxUpToDateConflicts is how many conflicts a run's developer is handed while
// its waiting pull request is brought up to date with a target other
// contributors moved (State.UpToDateConflicts). The next conflict after them
// goes to the development manager with the change kept.
const MaxUpToDateConflicts = 3

// BringingPersonMergeUpToDate reports a run whose latest resumption was the
// forge check putting it back at its promotion to bring its pull request,
// waiting for a person to merge it, up to date with a target that moved
// (CausePersonMergeBehind), and that has not landed since. What the run
// publishes from there replaces the head a person may be about to merge, so it
// reads the pull request's state again immediately before it pushes.
func (s State) BringingPersonMergeUpToDate() bool {
	if s.Integration != nil || s.PullRequest == nil || len(s.IntegrationResumptions) == 0 {
		return false
	}
	return s.IntegrationResumptions[len(s.IntegrationResumptions)-1].Cause == CausePersonMergeBehind
}

// PutBackToBringUpToDate is a run waiting for a person to merge its pull
// request, put back at its promotion so its change is brought up to date with
// a target that moved past the request (CausePersonMergeBehind). The wait ends,
// and starts again when the run publishes the updated head; while it is live the
// run holds a developer slot. The caller saves it through Store.ReserveLive,
// which takes that slot in the same act.
//
// These updates are not stops the environment caused, and a request in a busy
// shared repository is brought up to date as often as the target moves, so they
// do not spend MaxIntegrationResumptions: the record keeps only the latest one,
// in place of any earlier, and where the record is still full of other
// resumptions the oldest of them gives way. Each update is also told on the
// work item, which keeps the whole account.
func (s State) PutBackToBringUpToDate(reason string, at time.Time) State {
	resumed := s
	kept := make([]IntegrationResumption, 0, len(s.IntegrationResumptions)+1)
	for _, earlier := range s.IntegrationResumptions {
		if earlier.Cause != CausePersonMergeBehind {
			kept = append(kept, earlier)
		}
	}
	if over := len(kept) + 1 - MaxIntegrationResumptions; over > 0 {
		kept = kept[over:]
	}
	resumed.IntegrationResumptions = append(kept,
		IntegrationResumption{
			Cause:             CausePersonMergeBehind,
			Reason:            reason,
			ResumedAt:         at,
			SupersededFailure: s.Failure,
			SupersededBlocker: s.Blocker,
		})
	resumed.PersonMerge = nil
	resumed.Failure = ""
	resumed.Blocker = ""
	resumed.StopClass = ""
	resumed.Status = StatusRunning
	resumed.Phase = PhaseIntegrating
	resumed.CompletedAt = nil
	resumed.SettledQuietSince = nil
	resumed.UpdatedAt = at
	return resumed
}

// StartUpToDateConflictRound counts one more conflict handed to the developer
// while the run brings its waiting pull request up to date, and puts the run at
// its developing step for it. The caller saves it before the developer is
// invoked, so an interrupted round is still one the run had.
func (s *State) StartUpToDateConflictRound(at time.Time) {
	s.UpToDateConflicts++
	s.Phase = PhaseDeveloping
	s.UpdatedAt = at
}

// PersonMergeUpdateRefused records, once, why the forge check cannot bring a
// waiting request up to date, so the wait is shown as the development
// manager's to decide and the item is not told again at every check. It
// changes nothing where the same reason is already recorded.
type PersonMergeUpdateRefused struct {
	Number int
	Reason string
	At     time.Time
}

func (u PersonMergeUpdateRefused) apply(state *State) (bool, error) {
	if !state.WaitingForPersonMerge() || state.PersonMerge.Number != u.Number {
		return false, fmt.Errorf("run %s is not waiting for a person to merge pull request %d, so why it cannot be brought up to date is not recorded on it", state.RunID, u.Number)
	}
	if state.PersonMerge.UpdateRefused == u.Reason {
		return false, nil
	}
	wait := *state.PersonMerge
	wait.UpdateRefused = u.Reason
	state.PersonMerge = &wait
	state.UpdatedAt = u.At
	return true, nil
}

// WaitingPullRequests is the limit on approved changes waiting for a person to
// merge them (execution.max_waiting_pull_requests) read against the runs that
// are waiting, oldest wait first. At the limit the scheduler starts no new
// developer work, and every surface says why in the words of Says. It is one
// reading shared by the scheduler, which decides from it, and the read model,
// which reports it, so the two cannot count the waits differently.
type WaitingPullRequests struct {
	// Limit is the configured limit, and zero is no limit.
	Limit   int                  `json:"limit"`
	Waiting []WaitingPullRequest `json:"waiting"`
}

// WaitingPullRequest is one pull request a person is to merge, with the work
// item its run carried.
type WaitingPullRequest struct {
	Number     int       `json:"number"`
	URL        string    `json:"url,omitempty"`
	WorkItemID string    `json:"work_item_id"`
	Title      string    `json:"title,omitempty"`
	RunID      string    `json:"run_id"`
	Since      time.Time `json:"since"`
}

// WaitingPullRequestsOf is the limit read against the runs given, keeping only
// those waiting for a person to merge their pull request. One pull request is
// counted once however many records name it.
func WaitingPullRequestsOf(limit int, states []State) WaitingPullRequests {
	reading := WaitingPullRequests{Limit: limit, Waiting: []WaitingPullRequest{}}
	seen := map[int]bool{}
	for _, state := range states {
		if !state.WaitingForPersonMerge() || seen[state.PersonMerge.Number] {
			continue
		}
		seen[state.PersonMerge.Number] = true
		reading.Waiting = append(reading.Waiting, WaitingPullRequest{
			Number:     state.PersonMerge.Number,
			URL:        state.PullRequest.URL,
			WorkItemID: state.WorkItemID,
			Title:      strings.TrimSpace(state.WorkItemTitle),
			RunID:      state.RunID,
			Since:      state.PersonMerge.Since,
		})
	}
	sort.SliceStable(reading.Waiting, func(i, j int) bool {
		left, right := reading.Waiting[i], reading.Waiting[j]
		if !left.Since.Equal(right.Since) {
			return left.Since.Before(right.Since)
		}
		return left.Number < right.Number
	})
	return reading
}

// Reached reports the limit standing: a limit is set and as many pull
// requests as it allows, or more, are waiting.
func (w WaitingPullRequests) Reached() bool {
	return w.Limit > 0 && len(w.Waiting) >= w.Limit
}

// Says is why no new work is starting, naming every pull request waited on by
// number and by its work item's title, and what starts it again. It is empty
// where the limit is not reached.
func (w WaitingPullRequests) Says() string {
	return w.SaysNaming(WaitingPullRequest.Says)
}

// SaysNaming is Says with each pull request named by the function given, for
// a surface that names work items its own way, such as with their current
// title from the tracker.
func (w WaitingPullRequests) SaysNaming(name func(WaitingPullRequest) string) string {
	if !w.Reached() {
		return ""
	}
	named := make([]string, 0, len(w.Waiting))
	for _, waiting := range w.Waiting {
		named = append(named, name(waiting))
	}
	requests := "pull requests are"
	if len(w.Waiting) == 1 {
		requests = "pull request is"
	}
	return fmt.Sprintf("Not starting new work because %d %s waiting for a person to merge (the limit is %d): %s. New work starts again by itself once one of them is merged or closed.",
		len(w.Waiting), requests, w.Limit, strings.Join(named, "; "))
}

// Says is the pull request by number and by its work item, titled where the
// run recorded a title.
func (w WaitingPullRequest) Says() string {
	if w.Title == "" {
		return fmt.Sprintf("#%d for %s", w.Number, w.WorkItemID)
	}
	return fmt.Sprintf("#%d for %s (%s)", w.Number, w.Title, w.WorkItemID)
}
