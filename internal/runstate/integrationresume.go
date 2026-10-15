package runstate

// An approved change the environment stopped on its way to the target branch,
// and what resuming it costs the item: nothing.
//
// A run's budgets bound the work's own failures, and a stop between the
// reviewer's approval and the promotion is not one. yoyodyne-ifd.309's rerun
// passed independent review and then stopped at integration twice — once for an
// uncommitted edit in the primary checkout, once for a tracker read that timed
// out under load — and every verb that could have picked it up spent something:
// a repair grant for a run that recorded no findings, or a fresh run and a fresh
// review for a change nobody disputed. Four operator overrides were signed to
// get one approved change onto the target, none of them for a verdict.
//
// So the stop is a durable fact on the run, written where the run fails from
// the error that ended it — a dirty checkout by its sentinel, a replay the
// harness killed by its own, a transport that did not answer by the recovery
// package's closed reading of the error — and a
// resumption is a continuation rather than an attempt: the run is made live
// again at the promotion it stopped short of, with the approval it already had,
// and the item's counters stay where the review left them. What leaves that path is a replay that conflicts, which is a
// person's to settle exactly as it always was.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// MaxIntegrationResumptions bounds how many times one run's integration is
// resumed. Nothing in the harness spends toward it — a resumption charges no
// budget, which is the point of it — so it is the record's own bound rather
// than a cap on the item: an environment that keeps refusing the same
// promotion sixteen times is a machine somebody has to look at, not a state
// file that grows until it does. The resuming action refuses at it before it
// writes anything, and the record refuses past it, so the two say the same
// thing; a run at the bound is a re-run's or a person's.
const MaxIntegrationResumptions = 16

// ResumingIntegrationSays is what every surface says of a run resumed at its
// promotion while that promotion is going: the approval stands, and what is
// happening is the integration the environment stopped, not a new round. It is
// one phrase here rather than one per surface for the reason the outcome
// vocabulary is: `yoyo status` and the channel must not say different words
// about one run.
const ResumingIntegrationSays = "approved, resuming integration"

// IntegrationStop is the environment having stopped this run after its change
// was approved and before that change was promoted.
//
// The cause is one of the closed set in environmental.go, and it is what makes
// the stop resumable: a promotion the environment refused is one the environment
// can stop refusing, where a replay that conflicted is a decision about the
// change somebody has to make. A target that diverged, or a key the remote
// refused, needs a person too — but what they settle is the branches or the
// credential, not the change, so once they have the approval still stands and
// the resumption carries it on. The phase says which step the run was in when
// it stopped, which is the step a resumption re-enters.
type IntegrationStop struct {
	Cause EnvironmentalCause `json:"cause"`
	// Detail is the failure the run ended on, folded to a line. It is evidence for
	// whoever reads the record rather than a second classification of it.
	Detail string `json:"detail,omitempty"`
	// Phase is the phase the run stopped in: reviewing, for a stop between the
	// approving verdict and the promotion, or integrating.
	Phase      Phase     `json:"phase"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Validate reports every contract violation in the record at once.
func (s IntegrationStop) Validate() error {
	var problems []error
	if !s.Cause.Valid() {
		problems = append(problems, fmt.Errorf("cause %q is not one this harness records for a run ended from outside the work", s.Cause))
	}
	if len(s.Detail) > MaxEnvironmentalDetailBytes {
		problems = append(problems, fmt.Errorf("detail is %d bytes, which exceeds the %d byte bound", len(s.Detail), MaxEnvironmentalDetailBytes))
	}
	if s.Phase != PhaseReviewing && s.Phase != PhaseIntegrating {
		problems = append(problems, fmt.Errorf("phase %q is not one an approved change is stopped in before its promotion", s.Phase))
	}
	if s.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	return errors.Join(problems...)
}

// ContendedIntegrationFailure opens the failure a run records when its
// promotion kept losing its target branch to other promotions: the pipeline
// writes it when a run gives back its developer slot after losing the race as
// many times as it may (lostRacesBeforeGivingBackTheSlot in the orchestrator),
// and older runs stopped on the retry budget a lost race once spent carry it
// too. LostItsRace reads it off both.
const ContendedIntegrationFailure = "integration lost its target branch"

// LostItsRace reports a run that ended because the target branch kept moving
// under its promotion: approved, never promoted, and stopped on the contended
// integration rather than on anything the environment was recorded as
// refusing. It is no verdict on the change — other work landed first — and it
// is read from the failure because that is the one place the record has ever
// said it, including on every run stopped this way before this existed.
func (s State) LostItsRace() bool {
	return s.Integration == nil && s.IntegrationStop == nil &&
		strings.HasPrefix(strings.TrimSpace(s.Failure), ContendedIntegrationFailure)
}

// Describe says what the stop was, the way a docket entry or a listing reads it.
func (s IntegrationStop) Describe() string {
	return fmt.Sprintf("approved, then stopped at the %s phase by the environment: %s (%s)", s.Phase, s.Cause, s.Cause.Title())
}

// ResumeSays is the one sentence every surface says of this stop: that the
// run's change is approved, what stopped it, and that `yoyo triage resume` is
// what resumes it. It is the docket's own wording, so the repair verb's refusal,
// the docket entry, and the channel line are one sentence rather than three.
func (s IntegrationStop) ResumeSays(runID string) string {
	return triage.ResumeIntegrationSays(runID, string(s.Phase), string(s.Cause), s.Cause.Title())
}

// ReplayConflict is this run's approved change having conflicted when it was
// replayed onto what its target branch had become. It is the one outcome that
// leaves the resumed path: the environment did not stop the change, the target
// moved under it, and which side is right is a decision about the change.
//
// That decision is asked of the developer that wrote the change first
// (yoyodyne-ifd.132). The record is the repair input it is handed: the change
// is moved onto the target with the disagreement left in the worktree, and the
// same developer session is continued to reconcile the two, after which the
// change re-earns the whole gate. It is cleared by the checks passing on the
// reconciled change, because a change that passes on top of the target is the
// settlement. A run whose budget leaves no attempt to hand it stops blocked with
// the record standing, so a repair triage grants afterwards hands the same
// developer the same disagreement.
//
// It is recorded as its own fact rather than left to the blocker, because the
// blocker is a write to the tracker and the tracker can fail to take it. On
// yoyodyne-ifd.441 it did — the blocker write timed out — so the run's only
// account of the conflict was the error that ended it, with the timed-out
// write joined onto its tail, and the integration-stop classifier reading the
// closed set of transport errors anywhere in that message recorded the conflict
// as a transport failure the harness could resume past. Written where the
// conflict is decided, before any write about it is attempted, it survives the
// write failing; and a run carrying one is never an integration stop, which the
// record refuses rather than trusts.
type ReplayConflict struct {
	// TargetBranch is the branch the replay was onto, which is what the change
	// conflicts with, and TargetCommit is where that branch had got to: the
	// other side of the disagreement.
	TargetBranch string `json:"target_branch"`
	TargetCommit string `json:"target_commit,omitempty"`
	// Paths are the repository-relative paths the replay stopped on, in the
	// order Git listed them, and Omitted is how many further ones the bound
	// dropped. A replay refused for something other than content can name none.
	Paths   []string `json:"paths,omitempty"`
	Omitted int      `json:"omitted,omitempty"`
	// Detail is Git's own account of the refusal, bounded. It is evidence for
	// whoever reads the record rather than a second classification of it.
	Detail string `json:"detail,omitempty"`
	// Phase is the phase the run was in when the conflict was decided.
	Phase      Phase     `json:"phase"`
	RecordedAt time.Time `json:"recorded_at"`
	// Moved says the change has already been put onto the target with the
	// disagreement left in the worktree as conflict markers, which is the state
	// the developer is asked to reconcile it in. It is recorded because the move
	// and the attempt after it are two steps a process can die between, and a
	// run continued afterwards — by an interrupted process being picked up, or
	// by a repair triage granted once the run's own budget was spent — has to
	// know whether the move is still owed before it invokes anybody.
	Moved bool `json:"moved,omitempty"`
}

// Validate reports every contract violation in the record at once.
func (c ReplayConflict) Validate() error {
	var problems []error
	if strings.TrimSpace(c.TargetBranch) == "" {
		problems = append(problems, errors.New("target_branch is required, because it is what the change conflicts with"))
	} else if !validLocalBranch(c.TargetBranch) {
		problems = append(problems, errors.New("target_branch must be a local branch name"))
	}
	if c.TargetCommit != "" && !commitPattern.MatchString(c.TargetCommit) {
		problems = append(problems, errors.New("target_commit must be a full commit hash"))
	}
	if len(c.Paths) > MaxConflictedPaths {
		problems = append(problems, fmt.Errorf("%d conflicted paths are recorded, which exceeds the bound of %d", len(c.Paths), MaxConflictedPaths))
	}
	for index, conflicted := range c.Paths {
		if strings.TrimSpace(conflicted) == "" {
			problems = append(problems, fmt.Errorf("paths[%d] is empty", index))
		}
	}
	if c.Omitted < 0 {
		problems = append(problems, errors.New("omitted cannot be negative"))
	}
	if len(c.Detail) > MaxConflictDetailBytes {
		problems = append(problems, fmt.Errorf("detail is %d bytes, which exceeds the %d byte bound", len(c.Detail), MaxConflictDetailBytes))
	}
	if c.Phase != PhaseReviewing && c.Phase != PhaseIntegrating {
		problems = append(problems, fmt.Errorf("phase %q is not one an approved change is replayed in", c.Phase))
	}
	if c.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	return errors.Join(problems...)
}

// Describe says what the stop was, the way a docket entry or a listing reads it.
func (c ReplayConflict) Describe() string {
	return fmt.Sprintf("approved, then stopped at the %s phase by a replay conflict onto %s", c.Phase, c.TargetBranch)
}

// Says is the one sentence every surface says of this conflict: that the run's
// change is approved, what it conflicted with, and that the repair-continue or a
// person moves next rather than `yoyo triage resume`. It is the
// docket's own wording, for the reason IntegrationStop.ResumeSays is.
func (c ReplayConflict) Says(runID string) string {
	return triage.ReplayConflictSays(runID, c.TargetBranch)
}

// IntegrationResumption is one continuation of this run's integration after an
// environmental stop. It is the run's own account of having been made live
// again: which stop it superseded, why, and when. It is deliberately not an
// attempt — the repair count and the review evidence are untouched by it — and
// deliberately not a triage decision, because there was nothing to decide: the
// reviewer decided, and the environment got in the way.
type IntegrationResumption struct {
	// Cause is the environmental cause of the stop this resumption supersedes.
	Cause EnvironmentalCause `json:"cause"`
	// Reason is what the run records as why it is going again: the harness's own
	// account of the stop, and any reasoning given to the command that resumed it.
	Reason    string    `json:"reason"`
	ResumedAt time.Time `json:"resumed_at"`
	// SupersededFailure and SupersededBlocker are what the run ended on, in the
	// words it was recorded in. Re-entry clears both, because a run that is going
	// again has neither failed nor stopped; keeping the words here is what stops
	// the clearing losing the evidence of what stopped it.
	SupersededFailure string `json:"superseded_failure,omitempty"`
	SupersededBlocker string `json:"superseded_blocker,omitempty"`
	// SupersededRefusal is the environmental refusal the run carried when it was
	// resumed, where it carried one: the round that ended, settled and paid back.
	// Re-entry clears it from the run for the reason it clears the failure — the
	// promotion this resumes is not a round — and keeping it here is what stops
	// the clearing losing the account of what that round cost the item.
	SupersededRefusal *EnvironmentalRefusal `json:"superseded_refusal,omitempty"`
}

// Validate reports every contract violation in the record at once.
func (r IntegrationResumption) Validate() error {
	var problems []error
	if !r.Cause.Valid() {
		problems = append(problems, fmt.Errorf("cause %q is not one this harness records for a run ended from outside the work", r.Cause))
	}
	if strings.TrimSpace(r.Reason) == "" {
		problems = append(problems, errors.New("the reason this run's integration was resumed is required"))
	}
	if len(r.Reason) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("reason is %d bytes, which exceeds the %d byte bound", len(r.Reason), MaxSelectionReasonBytes))
	}
	if r.ResumedAt.IsZero() {
		problems = append(problems, errors.New("resumed_at is required"))
	}
	if len(r.SupersededFailure) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("superseded_failure is %d bytes, which exceeds the %d byte bound", len(r.SupersededFailure), MaxBlockerBytes))
	}
	if len(r.SupersededBlocker) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("superseded_blocker is %d bytes, which exceeds the %d byte bound", len(r.SupersededBlocker), MaxBlockerBytes))
	}
	if r.SupersededRefusal != nil {
		if err := r.SupersededRefusal.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("superseded_refusal: %w", err))
		}
	}
	return errors.Join(problems...)
}

// ResumptionsLeft reports how many more times this run's integration may be
// resumed before the bound above refuses it.
func (s State) ResumptionsLeft() int {
	if left := MaxIntegrationResumptions - len(s.IntegrationResumptions); left > 0 {
		return left
	}
	return 0
}

// validateIntegrationResume reports every contract violation in the record's
// account of an integration stop and of the resumptions after it.
func (s State) validateIntegrationResume() []error {
	var problems []error
	if s.IntegrationStop != nil {
		if err := s.IntegrationStop.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("integration_stop: %w", err))
		}
		// A stop is recorded on an approved change that was not promoted: it is what
		// makes the run resumable at its promotion, and a record carrying one beside
		// a promotion, or beside a verdict that was not an approval, describes a
		// resumption of something that either happened already or was never
		// authorized.
		if s.ReviewDecision != ReviewApprove {
			problems = append(problems, errors.New("integration_stop requires an approving review decision, which is what the resumed promotion is authorized by"))
		}
		if s.Integration != nil {
			problems = append(problems, errors.New("integration_stop cannot be recorded beside a promotion, which is what it says did not happen"))
		}
	}
	if s.ReplayConflict != nil {
		if err := s.ReplayConflict.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("replay_conflict: %w", err))
		}
		if s.Integration != nil {
			problems = append(problems, errors.New("replay_conflict cannot be recorded beside a promotion, which is what it says did not happen"))
		}
		// The two are the two classifications of one stop after an approval — a
		// decision for a person and weather for the harness — and a record carrying
		// both is the yoyodyne-ifd.441 misreading written down. It is refused here
		// so that no classifier, however it reads the error, can produce it.
		if s.IntegrationStop != nil {
			problems = append(problems, errors.New("replay_conflict cannot be recorded beside an integration stop: a conflict is a person's to settle and never a stop the harness resumes past"))
		}
	}
	if len(s.IntegrationResumptions) > MaxIntegrationResumptions {
		problems = append(problems, fmt.Errorf("%d integration resumptions are recorded, which exceeds the bound of %d", len(s.IntegrationResumptions), MaxIntegrationResumptions))
	}
	for index, resumption := range s.IntegrationResumptions {
		if err := resumption.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("integration_resumptions[%d]: %w", index, err))
		}
	}
	return problems
}

// ApprovedAwaitingIntegration reports a run whose change the independent
// reviewer approved and whose promotion has not happened: the approval is
// standing, with the reviewer's session recorded as the evidence of it, and
// nothing has been integrated. It is the condition on which a stop is
// environmental rather than a verdict, whatever else the record says.
func (s State) ApprovedAwaitingIntegration() bool {
	if s.ReviewDecision != ReviewApprove || strings.TrimSpace(s.ReviewSessionID) == "" {
		return false
	}
	if s.Integration != nil {
		return false
	}
	return s.Phase == PhaseReviewing || s.Phase == PhaseIntegrating
}

// ResumableIntegration reports a stopped run whose integration may be resumed
// where it stopped: it ended, its approval is standing, the environment is what
// stopped it, the branch that holds the approved change is still there, and it
// has not been resumed as many times as the record bounds. The worktree need
// not be: a checkout the convergence sweep retired is put back
// from the branch at the commit the run recorded, and only the branch going is
// the end of the change. Everything else about whether it may be resumed now — the
// checkout being clean again, the worktree being as the harness left it, the
// item having no run in flight — is the resuming action's to ask, because it is
// about the moment rather than about the record.
func (s State) ResumableIntegration() bool {
	if !s.Status.Terminal() || s.IntegrationStop == nil || !s.ApprovedAwaitingIntegration() {
		return false
	}
	if s.WorktreePath == "" || s.Branch == "" || s.BaseCommit == "" || s.TargetBranch == "" {
		return false
	}
	return !s.BranchRemoved && s.ResumptionsLeft() > 0
}

// ResumingIntegration reports a run that is at its promotion again after an
// environmental stop, which is what `yoyo status` says in the words of
// ResumingIntegrationSays: in flight, at the integrating phase, with the
// approval standing and a resumption recorded. A resumed run whose replay put it
// back through the checks and the review is at a different phase and is
// described as that phase; one that has re-earned its approval and is promoting
// again is resuming the same integration, and is described as such.
func (s State) ResumingIntegration() bool {
	if !s.Status.InFlight() || s.Phase != PhaseIntegrating || len(s.IntegrationResumptions) == 0 {
		return false
	}
	return s.ReviewDecision == ReviewApprove && s.Integration == nil
}

// LastIntegrationResumption is the most recent resumption of this run's
// integration, and whether there is one.
func (s State) LastIntegrationResumption() (IntegrationResumption, bool) {
	if len(s.IntegrationResumptions) == 0 {
		return IntegrationResumption{}, false
	}
	return s.IntegrationResumptions[len(s.IntegrationResumptions)-1], true
}
