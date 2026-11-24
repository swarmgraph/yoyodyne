package runstate

// The development manager's decision to repair an item whose run has ended, from
// feedback on a pull request into the team's branch that the harness tracks
// (docs/designs/integrating-to-a-local-branch.md, section 2, "Deciding a repair
// of an item whose run has ended").
//
// The decision cannot sit on a stopped run's triage record, because the run that
// landed the item ended as integrated and nothing stopped. It is bound to the
// list entry it answers instead: the pull request and the piece of feedback, as
// the report on her list names them. It is kept on the wait of the run that
// landed the item (TeamMerge.Repairs), beside the pull request the feedback is
// about, so that it goes where the run's record goes and is refused once the wait
// ends. A repair carried out starts a new run, and the decision records which.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The bounds one decision is held to, as a triage decision's are.
const (
	MaxTeamFeedbackRepairReasonBytes = MaxTriageDecisionReasonBytes
	// MaxTeamFeedbackRepairs bounds the decisions one landed run carries. An entry
	// is answered once, so reaching it takes that many pieces of feedback on one
	// change.
	MaxTeamFeedbackRepairs = 64
)

// TeamFeedbackRepair is one decision to repair one item from one piece of
// feedback on a tracked pull request.
type TeamFeedbackRepair struct {
	// PullRequest and FeedbackID name the list entry the decision answers.
	PullRequest int    `json:"pull_request"`
	FeedbackID  string `json:"feedback_id"`
	// Feedback is the feedback as it was read, which is what the new run's
	// developer is given as evidence.
	Feedback ForgeFeedbackItem `json:"feedback"`
	// WorkItemID is the one item she decided to repair.
	WorkItemID string `json:"work_item_id"`
	// Reason is her reasoning, verbatim. DecidedBy, Conversation and Turn say who
	// recorded it and where, so the new run's record can be traced back to it.
	Reason       string    `json:"reason"`
	DecidedBy    string    `json:"decided_by"`
	Conversation string    `json:"conversation"`
	Turn         int       `json:"turn"`
	DecidedAt    time.Time `json:"decided_at"`
	// TakenAt is when the harness took the decision to carry it out, before the
	// new run is reserved, so that no second pass carries it out while the first
	// is running or after it was interrupted. It is cleared when the run was
	// refused before it existed (TeamFeedbackRepairWithdrawn).
	TakenAt *time.Time `json:"taken_at,omitempty"`
	// StartedRunID is the new run the decision started, and StartedAt when. Both
	// are empty until the harness carries the decision out.
	StartedRunID string     `json:"started_run,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
}

// Started reports a decision the harness has carried out.
func (r TeamFeedbackRepair) Started() bool { return r.StartedRunID != "" }

// Validate reports every contract violation in the decision at once.
func (r TeamFeedbackRepair) Validate() error {
	var problems []error
	if r.PullRequest <= 0 {
		problems = append(problems, errors.New("a repair from feedback names the pull request the feedback is on"))
	}
	if !feedbackIDPattern.MatchString(r.FeedbackID) {
		problems = append(problems, fmt.Errorf("%q is not a forge review or comment identifier", r.FeedbackID))
	}
	if err := r.Feedback.Validate(); err != nil {
		problems = append(problems, fmt.Errorf("feedback: %w", err))
	} else if r.Feedback.ID != r.FeedbackID {
		problems = append(problems, fmt.Errorf("feedback %s is not the feedback %s the decision answers", r.Feedback.ID, r.FeedbackID))
	}
	if strings.TrimSpace(r.WorkItemID) == "" {
		problems = append(problems, errors.New("a repair from feedback names the one item it repairs"))
	}
	switch reason := strings.TrimSpace(r.Reason); {
	case reason == "":
		problems = append(problems, errors.New("a repair from feedback records the reasoning it was made on"))
	case len(reason) > MaxTeamFeedbackRepairReasonBytes:
		problems = append(problems, fmt.Errorf("the reason is %d bytes, limit is %d", len(reason), MaxTeamFeedbackRepairReasonBytes))
	}
	if strings.TrimSpace(r.DecidedBy) == "" || strings.TrimSpace(r.Conversation) == "" {
		problems = append(problems, errors.New("a repair from feedback names the role and the conversation that recorded it"))
	}
	if r.Turn < 0 {
		problems = append(problems, fmt.Errorf("turn %d is not a turn", r.Turn))
	}
	if r.DecidedAt.IsZero() {
		problems = append(problems, errors.New("a repair from feedback records when it was decided"))
	}
	if (r.StartedRunID == "") != (r.StartedAt == nil) {
		problems = append(problems, errors.New("a started repair records the run it started and when, together"))
	}
	if r.StartedRunID != "" && !ValidRunID(r.StartedRunID) {
		problems = append(problems, fmt.Errorf("%q is not a run identifier", r.StartedRunID))
	}
	return errors.Join(problems...)
}

// Cite names the record, for the reason a run starts from it and so that a
// reader can follow the chain from the team's feedback to the change it caused.
func (r TeamFeedbackRepair) Cite() string {
	return fmt.Sprintf("recorded by the %s in conversation %s after turn %d, at %s, answering %s on pull request #%d",
		strings.TrimSpace(r.DecidedBy), strings.TrimSpace(r.Conversation), r.Turn, r.DecidedAt.UTC().Format(time.RFC3339), r.FeedbackID, r.PullRequest)
}

// Repair is the decision answering one piece of feedback on one pull request,
// and whether this run's wait holds one.
func (t TeamMerge) Repair(number int, feedbackID string) (TeamFeedbackRepair, bool) {
	return repairAnswering(t.Repairs, number, feedbackID)
}

// RepairsOwed lists the decisions recorded that no pass has taken to carry out.
func (t TeamMerge) RepairsOwed() []TeamFeedbackRepair { return repairsOwed(t.Repairs) }

// RepairsTaken lists the decisions a pass took whose new run has not been
// recorded: one still running, or one that was interrupted.
func (t TeamMerge) RepairsTaken() []TeamFeedbackRepair { return repairsTaken(t.Repairs) }

// Repair, RepairsOwed and RepairsTaken are the same readings of the wait of a
// run whose change waits on a pull request the harness opened.
func (p PersonMerge) Repair(number int, feedbackID string) (TeamFeedbackRepair, bool) {
	return repairAnswering(p.Repairs, number, feedbackID)
}

func (p PersonMerge) RepairsOwed() []TeamFeedbackRepair { return repairsOwed(p.Repairs) }

func (p PersonMerge) RepairsTaken() []TeamFeedbackRepair { return repairsTaken(p.Repairs) }

// RepairInFlight reports a decision a pass took to carry out whose new run has
// not been recorded as started. While one is, the new run answers the request's
// feedback and replaces its head, so the run that waited on it before is left
// alone: its feedback is not read and it is not brought up to date.
func (p PersonMerge) RepairInFlight() bool { return len(repairsTaken(p.Repairs)) > 0 }

func repairAnswering(repairs []TeamFeedbackRepair, number int, feedbackID string) (TeamFeedbackRepair, bool) {
	for _, repair := range repairs {
		if repair.PullRequest == number && repair.FeedbackID == feedbackID {
			return repair, true
		}
	}
	return TeamFeedbackRepair{}, false
}

func repairsOwed(repairs []TeamFeedbackRepair) []TeamFeedbackRepair {
	var owed []TeamFeedbackRepair
	for _, repair := range repairs {
		if !repair.Started() && repair.TakenAt == nil {
			owed = append(owed, repair)
		}
	}
	return owed
}

func repairsTaken(repairs []TeamFeedbackRepair) []TeamFeedbackRepair {
	var taken []TeamFeedbackRepair
	for _, repair := range repairs {
		if !repair.Started() && repair.TakenAt != nil {
			taken = append(taken, repair)
		}
	}
	return taken
}

func validateTeamFeedbackRepairs(wait string, repairs []TeamFeedbackRepair) []error {
	var problems []error
	if len(repairs) > MaxTeamFeedbackRepairs {
		problems = append(problems, fmt.Errorf("%s carries %d repairs, which exceeds the bound of %d", wait, len(repairs), MaxTeamFeedbackRepairs))
	}
	seen := map[string]bool{}
	for index, repair := range repairs {
		if err := repair.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("%s repairs[%d]: %w", wait, index, err))
			continue
		}
		key := fmt.Sprintf("%d/%s", repair.PullRequest, repair.FeedbackID)
		if seen[key] {
			problems = append(problems, fmt.Errorf("%s repairs[%d]: feedback %s on pull request #%d is already answered", wait, index, repair.FeedbackID, repair.PullRequest))
		}
		seen[key] = true
	}
	return problems
}

// TeamFeedbackRepairDecided records a decision on the run that landed the item,
// or, where LandingCommit is empty, on the run whose change waits on a pull
// request the harness opened for a person to merge. It is refused when the run
// is no longer waiting, which is how a decision about an item that has closed is
// refused, and when the entry already has a decision on this run.
type TeamFeedbackRepairDecided struct {
	LandingCommit string
	Repair        TeamFeedbackRepair
}

func (u TeamFeedbackRepairDecided) apply(state *State) (bool, error) {
	if state.WorkItemID != u.Repair.WorkItemID {
		return false, fmt.Errorf("run %s carried %s, not %s", state.RunID, state.WorkItemID, u.Repair.WorkItemID)
	}
	if u.LandingCommit == "" {
		if !state.WaitingForPersonMerge() || state.PersonMerge.Number != u.Repair.PullRequest {
			return false, fmt.Errorf("run %s is not waiting for a person to merge pull request #%d, so a repair from feedback on it is not recorded on it", state.RunID, u.Repair.PullRequest)
		}
		if _, answered := state.PersonMerge.Repair(u.Repair.PullRequest, u.Repair.FeedbackID); answered {
			return false, fmt.Errorf("feedback %s on pull request #%d is already answered on run %s", u.Repair.FeedbackID, u.Repair.PullRequest, state.RunID)
		}
		wait := *state.PersonMerge
		wait.Repairs = append(append([]TeamFeedbackRepair(nil), wait.Repairs...), u.Repair)
		if err := wait.Validate(); err != nil {
			return false, fmt.Errorf("run %s cannot record the repair: %w", state.RunID, err)
		}
		state.PersonMerge = &wait
		state.UpdatedAt = u.Repair.DecidedAt
		return true, nil
	}
	if !state.WaitingForTeamMerge() || state.TeamMerge.LandingCommit != u.LandingCommit {
		return false, fmt.Errorf("run %s is not waiting for the team to merge %s, so a repair from feedback is not recorded on it", state.RunID, u.LandingCommit)
	}
	if state.TeamMerge.PullRequest == nil || state.TeamMerge.PullRequest.Number != u.Repair.PullRequest {
		return false, fmt.Errorf("run %s is not tracked under pull request #%d, so feedback on it is not answered on this run", state.RunID, u.Repair.PullRequest)
	}
	if _, answered := state.TeamMerge.Repair(u.Repair.PullRequest, u.Repair.FeedbackID); answered {
		return false, fmt.Errorf("feedback %s on pull request #%d is already answered on run %s", u.Repair.FeedbackID, u.Repair.PullRequest, state.RunID)
	}
	wait := *state.TeamMerge
	wait.Repairs = append(append([]TeamFeedbackRepair(nil), wait.Repairs...), u.Repair)
	if err := wait.Validate(); err != nil {
		return false, fmt.Errorf("run %s cannot record the repair: %w", state.RunID, err)
	}
	state.TeamMerge = &wait
	state.UpdatedAt = u.Repair.DecidedAt
	return true, nil
}

// TeamFeedbackRepairStarted records the new run a decision started. Recording it
// again for the same run changes nothing.
type TeamFeedbackRepairStarted struct {
	LandingCommit string
	PullRequest   int
	FeedbackID    string
	RunID         string
	At            time.Time
}

func (u TeamFeedbackRepairStarted) apply(state *State) (bool, error) {
	same := false
	changed, err := changeRepair(state, u.LandingCommit, u.PullRequest, u.FeedbackID, u.At, func(repair *TeamFeedbackRepair) error {
		if repair.StartedRunID == u.RunID {
			same = true
			return nil
		}
		if repair.Started() {
			return fmt.Errorf("the repair answering %s on pull request #%d already started run %s", u.FeedbackID, u.PullRequest, repair.StartedRunID)
		}
		at := u.At
		repair.StartedRunID, repair.StartedAt = u.RunID, &at
		return nil
	})
	if same {
		return false, nil
	}
	return changed, err
}

// TriageFeedbackRepair is the action a cap refuses when it refuses a repair from
// feedback.
const TriageFeedbackRepair = "repair from feedback"

// RecordFeedbackRepair spends one of the item's attempts for a repair decided
// from feedback, in the same unit and against the same caps as a re-run: the new
// run it pays for is a whole run, repair budget and all. It refuses once the item
// has none left, as a re-run does, and writes the spend before the run it pays
// for is started, so a crash between the two costs an attempt nobody took rather
// than one nobody counted. runID is the run that landed the item, which the spend
// is recorded against.
//
// It records no decision on the item's triage record: that record holds one
// decision per stopped run, and nothing here stopped. The decision is kept on the
// landed run's wait (TeamFeedbackRepairDecided).
func (s *TriageStore) RecordFeedbackRepair(ctx context.Context, workItemID, runID string, at time.Time, caps TriageCaps) (TriageCounters, error) {
	if err := caps.Validate(); err != nil {
		return TriageCounters{}, err
	}
	if !ValidRunID(runID) {
		return TriageCounters{}, fmt.Errorf("%q is not a run identifier; a repair from feedback is spent against the run that landed the item", runID)
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	return s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		permitted := caps.Overridden(counters.Overrides)
		check := func() error {
			var refusals []TriageCapRefusal
			if counters.Reruns >= permitted.Reruns {
				refusals = append(refusals, TriageCapRefusal{Budget: TriageRerunBudget, Spent: triageBudgetSpend(*counters, TriageRerunBudget), Cap: permitted.Reruns})
			}
			if counters.RoundsUncommitted(permitted.ReviewRounds) == 0 {
				refusals = append(refusals, TriageCapRefusal{Budget: TriageReviewRoundBudget, Spent: triageBudgetSpend(*counters, TriageReviewRoundBudget), Cap: permitted.ReviewRounds})
			}
			if len(refusals) > 0 {
				return TriageCapError{Action: TriageFeedbackRepair, WorkItemID: counters.WorkItemID, Refusals: refusals}
			}
			return nil
		}
		if err := s.spendShared(ctx, workItemID, counters, SharedSpend{Budget: TriageRerunBudget, Run: runID}, check); err != nil {
			return err
		}
		counters.Reruns++
		return nil
	})
}

// RepairMarker is the text a repair's new run carries in its selection reason,
// by which an interrupted pass finds the run its decision started.
func (r TeamFeedbackRepair) RepairMarker() string {
	return fmt.Sprintf(repairMarkerOpens+"%s on pull request #%d]", r.FeedbackID, r.PullRequest)
}

const repairMarkerOpens = "[repair from "

// ChosenForFeedbackRepair reports a selection reason that opens with a
// RepairMarker, which is a run carrying out a repair decided from feedback.
func ChosenForFeedbackRepair(reason string) bool {
	return strings.HasPrefix(strings.TrimSpace(reason), repairMarkerOpens)
}

// TeamFeedbackRepairTaken records that a pass took the decision to carry it out.
// It is refused when the decision is already taken or started, which is what
// keeps two passes from starting two runs: it is made under the run's write lock.
type TeamFeedbackRepairTaken struct {
	LandingCommit string
	PullRequest   int
	FeedbackID    string
	At            time.Time
}

func (u TeamFeedbackRepairTaken) apply(state *State) (bool, error) {
	return changeRepair(state, u.LandingCommit, u.PullRequest, u.FeedbackID, u.At, func(repair *TeamFeedbackRepair) error {
		if repair.Started() || repair.TakenAt != nil {
			return fmt.Errorf("the repair answering %s on pull request #%d is already taken", u.FeedbackID, u.PullRequest)
		}
		at := u.At
		repair.TakenAt = &at
		return nil
	})
}

// TeamFeedbackRepairWithdrawn gives a taken decision back where the new run was
// refused before it existed, so a later pass carries it out.
type TeamFeedbackRepairWithdrawn struct {
	LandingCommit string
	PullRequest   int
	FeedbackID    string
	At            time.Time
}

func (u TeamFeedbackRepairWithdrawn) apply(state *State) (bool, error) {
	return changeRepair(state, u.LandingCommit, u.PullRequest, u.FeedbackID, u.At, func(repair *TeamFeedbackRepair) error {
		if repair.Started() {
			return fmt.Errorf("the repair answering %s on pull request #%d already started run %s", u.FeedbackID, u.PullRequest, repair.StartedRunID)
		}
		repair.TakenAt = nil
		return nil
	})
}

// changeRepair changes the decision answering one piece of feedback, on the wait
// the landing commit names, or on the wait of a run whose change waits on a pull
// request the harness opened where landing is empty.
func changeRepair(state *State, landing string, number int, feedbackID string, at time.Time, change func(*TeamFeedbackRepair) error) (bool, error) {
	if landing == "" {
		if state.PersonMerge == nil || state.PersonMerge.Number != number {
			return false, fmt.Errorf("run %s did not wait on pull request #%d, so no repair of it is recorded on it", state.RunID, number)
		}
		wait := *state.PersonMerge
		wait.Repairs = append([]TeamFeedbackRepair(nil), wait.Repairs...)
		for index := range wait.Repairs {
			if wait.Repairs[index].PullRequest != number || wait.Repairs[index].FeedbackID != feedbackID {
				continue
			}
			if err := change(&wait.Repairs[index]); err != nil {
				return false, err
			}
			if err := wait.Validate(); err != nil {
				return false, err
			}
			state.PersonMerge = &wait
			state.UpdatedAt = at
			return true, nil
		}
		return false, fmt.Errorf("run %s holds no repair answering %s on pull request #%d", state.RunID, feedbackID, number)
	}
	if state.TeamMerge == nil || state.TeamMerge.LandingCommit != landing {
		return false, fmt.Errorf("run %s did not land %s, so no repair of it is recorded on it", state.RunID, landing)
	}
	wait := *state.TeamMerge
	wait.Repairs = append([]TeamFeedbackRepair(nil), wait.Repairs...)
	for index := range wait.Repairs {
		if wait.Repairs[index].PullRequest != number || wait.Repairs[index].FeedbackID != feedbackID {
			continue
		}
		if err := change(&wait.Repairs[index]); err != nil {
			return false, err
		}
		if err := wait.Validate(); err != nil {
			return false, err
		}
		state.TeamMerge = &wait
		state.UpdatedAt = at
		return true, nil
	}
	return false, fmt.Errorf("run %s holds no repair answering %s on pull request #%d", state.RunID, feedbackID, number)
}
