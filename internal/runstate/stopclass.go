package runstate

import (
	"slices"
	"strings"
)

// StopClass names which gate or bound stopped a run, as the pipeline knew it at the
// moment it stopped. It is the one field a reader can answer "what stopped this"
// from without inferring it, and inferring it is what went wrong before it
// existed: a check failure, a refused path, and a reviewer's findings are all
// evidence a run carries while it is still repairing, so every one of them can be
// on the record of a run the provider killed, and a reader who took the first of
// them it found as the reason read that run as a failed change.
//
// It is written where the run is stopped and by nothing that reads the record
// afterwards. A record written before the field existed reads as unknown
// rather than having its cause guessed from the remaining evidence.
type StopClass string

const (
	// These causes distinguish the bounds within a gate. The older gate values
	// remain valid so historical records keep what was actually recorded.
	StopUnknown           StopClass = "unknown"
	StopCheckTimeout      StopClass = "check-timeout"
	StopProviderIdle      StopClass = "provider-idle"
	StopProviderBudget    StopClass = "provider-budget"
	StopRelaunchBudget    StopClass = "relaunch-budget"
	StopRepairBudget      StopClass = "repair-budget"
	StopIntegrationBudget StopClass = "integration-budget"
	StopPromotionWait     StopClass = "promotion-wait"
	StopUsagePause        StopClass = "usage-pause"
	StopOperator          StopClass = "operator-stop"
	StopManager           StopClass = "manager-stop"
	StopRedeploy          StopClass = "redeploy-drain"
	StopDeadClaim         StopClass = "dead-claim"
	StopDeveloperAccount  StopClass = "developer-account"
	StopReviewAccount     StopClass = "review-account"
	StopEscalated         StopClass = "work-item-escalated"
	StopContextBound      StopClass = "context-bound"
	StopStateBound        StopClass = "state-bound"
	StopEventBound        StopClass = "event-bound"
	StopIntegrationPolicy StopClass = "integration-policy"
	StopRecoveryWindow    StopClass = "recovery-window"
	// StopReviewBound is a change whose source or test files the review bound
	// would keep out of the reviewer's copy, measured before any check ran.
	StopReviewBound StopClass = "review-bound"

	// StopChecks is the checking gate: a configured check that kept failing or
	// could not run, a protected path the change kept touching, or a change nobody
	// recorded running anything against.
	StopChecks StopClass = "checks"
	// StopReview is the independent review: findings the repair budget could not
	// resolve, a reviewer that could not answer the verdict contract, or an
	// approval whose independence could not be shown.
	StopReview StopClass = "review"
	// StopIntegration is the promotion onto the target branch: a target that kept
	// moving, a replay that conflicts, or a local and remote target that went
	// different ways.
	StopIntegration StopClass = "integration"
	// StopPublish is the publication of a promotion to the forge, which a run
	// reports rather than fails on when the local promotion landed.
	StopPublish StopClass = "publish"
	// StopCleanup is the removal of the run's worktree and branch after its work
	// integrated, which leaves the run succeeded with an artifact standing.
	StopCleanup StopClass = "cleanup"
	// StopRecording is the run's completion record: the outcome, the closure, or
	// the terminal state of a run whose work was done could not be written down.
	StopRecording StopClass = "recording"
	// StopProvider is the provider ending the run without judging the work: it
	// died past the relaunch budget, failed an invocation, was stopped on time with
	// nothing to continue from, or refused the run for a wait the harness will not
	// take.
	StopProvider StopClass = "provider"
	// StopOutside is something outside the work refusing the round rather than the work
	// failing, which is the stop the round's budgets are given back for.
	StopOutside StopClass = "outside"
	// StopCancelled is the operator asking the run to stop, or the context the run
	// was given being cancelled.
	StopCancelled StopClass = "cancelled"
	// StopHarness is the harness's own step failing around the work: a state it
	// could not save, a tracker it could not write before the work was done, a
	// scratch directory it could not cut. It is the class a stop gets when none of
	// the gates above stopped it, and it is named rather than left empty because
	// an empty class reads as a record written before the field existed.
	StopHarness StopClass = "harness"
)

// stopClasses is the vocabulary stated as a list, closed for the reason every
// list in state.go is: a class nothing recognizes is refused at the save rather
// than printed at the front of a reason nobody can act on.
// TestTheDurableSchemaStoresEveryStopClassThePipelineRecords holds the pipeline
// to it.
var stopClasses = []StopClass{
	StopChecks, StopReview, StopIntegration, StopPublish, StopCleanup,
	StopRecording, StopProvider, StopOutside, StopCancelled, StopHarness,
	StopUnknown, StopCheckTimeout, StopProviderIdle, StopProviderBudget,
	StopRelaunchBudget, StopRepairBudget, StopIntegrationBudget, StopPromotionWait,
	StopUsagePause, StopOperator, StopManager, StopRedeploy, StopDeadClaim,
	StopDeveloperAccount, StopReviewAccount, StopEscalated, StopContextBound,
	StopStateBound, StopEventBound, StopIntegrationPolicy, StopRecoveryWindow,
	StopReviewBound,
}

// StopClasses is the stop vocabulary as a caller outside this package reads it,
// answered with a copy for the reason the other vocabularies are.
func StopClasses() []StopClass {
	classes := slices.Clone(stopClasses)
	for _, cause := range EnvironmentalCauses() {
		classes = append(classes, cause.StopClass())
	}
	return classes
}

// StopClass is the one named conversion from budget-accounting causes to the
// stop vocabulary. It changes no refund policy and introduces no second list.
func (c EnvironmentalCause) StopClass() StopClass { return StopClass(c) }

// Name is the wire name for projections that cannot import this package.
func (c StopClass) Name() string { return string(c) }

// StopError carries a bound's cause through callers that add a gate label.
// It is evidence of a refusal only; it authorizes no recovery or budget return.
type StopError struct {
	Class StopClass
	Cause error
}

func (e StopError) Error() string { return e.Cause.Error() }
func (e StopError) Unwrap() error { return e.Cause }

// RecordedStopClass returns unknown for a stopped historical record. It never
// infers a cause from prose or from evidence left by an earlier attempt.
func (s State) RecordedStopClass() StopClass {
	return recordedStopClass(s.StopClass, s.Status, s.Outcome(), s.Integration != nil)
}

func (s RunSummary) RecordedStopClass() StopClass {
	return recordedStopClass(s.StopClass, s.Status, s.Outcome, s.Integrated)
}

func recordedStopClass(class StopClass, status Status, outcome RunOutcome, integrated bool) StopClass {
	if class != "" {
		return class
	}
	if status.Terminal() && (outcome != OutcomeSucceeded || !integrated) {
		return StopUnknown
	}
	return ""
}

// ProviderStopClass distinguishes the invocation's two clocks.
func ProviderStopClass(reason string) StopClass {
	switch reason {
	case ProviderStopStalled:
		return StopProviderIdle
	case ProviderStopBudgetExhausted:
		return StopProviderBudget
	default:
		return StopProvider
	}
}

// Valid reports a class the durable schema stores.
func (c StopClass) Valid() bool {
	return slices.Contains(stopClasses, c) || EnvironmentalCause(c).Valid()
}

// Gate reports the older, broader labels that a more specific bound replaces.
func (c StopClass) Gate() bool {
	switch c {
	case StopChecks, StopReview, StopIntegration, StopPublish, StopCleanup,
		StopRecording, StopProvider, StopOutside, StopCancelled, StopHarness:
		return true
	default:
		return false
	}
}

// StopReason is a recorded reason with the class that stopped the run as its
// first word, which is how every surface prints the reason. A record naming no
// class prints its reason as it was written, and a class with no reason beside it
// prints on its own, so neither half is lost for want of the other.
func StopReason(class StopClass, reason string) string {
	reason = strings.TrimSpace(reason)
	switch {
	case class == "" || class == StopUnknown:
		return reason
	case reason == "":
		return string(class)
	default:
		return string(class) + ": " + reason
	}
}
