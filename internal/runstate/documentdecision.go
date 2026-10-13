package runstate

// What the development manager decided about a document run that stopped.
//
// A document run has no work item behind it: it is recorded under an item of
// its own making (see orchestrator's documentItem), so none of the triage
// decisions recorded on a work item's triage record can be taken on it. Once a
// document run is hers — every attempt stopped without judging the document, or
// it left a pull request open that its conversation will never land — two
// decisions answer it, and both are recorded on the run itself: retrying it,
// which lands the same confirmed text through a fresh run, and retiring it,
// which closes its pull request and says why.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The two decisions about a stopped document run.
const (
	// DocumentDecisionRetry lands the run's confirmed text again through a fresh
	// run, which the scheduler starts as a document landing frees.
	DocumentDecisionRetry = triage.DocumentRetry
	// DocumentDecisionRetire closes the run's pull request and lands nothing.
	DocumentDecisionRetire = triage.DocumentRetire
)

// DocumentDecision is the development manager's decision about one stopped
// document run, recorded on that run.
type DocumentDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	// DecidedBy is the role and the conversation the decision was recorded in.
	DecidedBy string    `json:"decided_by"`
	DecidedAt time.Time `json:"decided_at"`
	// RetriedAs is the run a retry's fresh handoff is first published by.
	RetriedAs string `json:"retried_as,omitempty"`
	// PullRequestClosed says the decision closed the run's pull request, which
	// both decisions do where the run left one open.
	PullRequestClosed bool `json:"pull_request_closed,omitempty"`
	// Problem is what the decision could not do beside what it did: a retry
	// whose old pull request could not be closed, or a branch that could not be
	// deleted. Empty where it did everything.
	Problem string `json:"problem,omitempty"`
}

func (d DocumentDecision) Validate() error {
	var problems []error
	switch d.Decision {
	case DocumentDecisionRetry:
		if !ValidRunID(d.RetriedAs) {
			problems = append(problems, errors.New("a retry names the run its fresh handoff is published by"))
		}
	case DocumentDecisionRetire:
		if d.RetriedAs != "" {
			problems = append(problems, errors.New("a retirement starts no run"))
		}
	default:
		problems = append(problems, fmt.Errorf("document decision %q is neither %q nor %q", d.Decision, DocumentDecisionRetry, DocumentDecisionRetire))
	}
	if strings.TrimSpace(d.Reason) == "" {
		problems = append(problems, errors.New("a document decision records why"))
	}
	if strings.TrimSpace(d.DecidedBy) == "" {
		problems = append(problems, errors.New("a document decision records who decided it"))
	}
	if d.DecidedAt.IsZero() {
		problems = append(problems, errors.New("a document decision records when it was made"))
	}
	return errors.Join(problems...)
}

// DocumentRetried marks a handoff as the development manager's retry of a
// stopped document run: the same confirmed text, under a write of its own so
// its runs are fresh ones. It is carried on the handoff because that is what
// every run of it records, so each of those runs says why it exists.
type DocumentRetried struct {
	FromRun   string    `json:"from_run"`
	Reason    string    `json:"reason"`
	DecidedBy string    `json:"decided_by"`
	DecidedAt time.Time `json:"decided_at"`
}

func (r DocumentRetried) Validate() error {
	var problems []error
	if !ValidRunID(r.FromRun) {
		problems = append(problems, errors.New("a retried handoff names the run it retries"))
	}
	if strings.TrimSpace(r.Reason) == "" || strings.TrimSpace(r.DecidedBy) == "" || r.DecidedAt.IsZero() {
		problems = append(problems, errors.New("a retried handoff records why, by whom, and when it was decided"))
	}
	return errors.Join(problems...)
}

// RetryOf is the handoff a retry of one of this handoff's runs publishes. Its
// write is named for the run retried, so recording the same decision twice
// names the same handoff and starts nothing a second time.
func (d DocumentPublication) RetryOf(runID string, retried DocumentRetried) DocumentPublication {
	named := strings.TrimPrefix(runID, "run-")
	if len(named) > 8 {
		named = named[:8]
	}
	next := d
	next.WriteID = d.WriteID + "-retry-" + named
	next.Retried = &retried
	return next
}

// DocumentDecidable reports a document run the development manager may retry
// or retire: one that ended without landing and has not been decided already.
func (s State) DocumentDecidable() bool {
	return s.Document != nil && s.Status.Terminal() && s.Status != StatusSucceeded && s.DocumentDecision == nil
}
