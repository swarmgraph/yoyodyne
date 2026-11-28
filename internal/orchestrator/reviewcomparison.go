package orchestrator

// A document publication's review made a second time on the cheaper model
// (execution.reviewer_models.compare_documents; runstate/reviewcomparison.go;
// docs/designs/reducing-what-the-factory-costs.md, section 5).
//
// After the deciding review of a document publication reaches a verdict, the
// same reviewer — the reviewer role, under its own persona and contract, never
// the role that wrote the document — is asked again on the cheaper model, over
// the same request: the same change, the same evidence, the same account. Its
// verdict is written onto the run beside the deciding one and nothing reads it
// to decide anything: whether the publication lands is the deciding review's
// alone, and a second review that fails is recorded as having reached no
// verdict rather than stopping the run.
//
// Of the second review's events, the run's event log is given only those that
// carry what it cost, which is what the run is priced from. Its review events
// are left out: the delivery and accomplishment readings take those as the
// run's verdicts, and a verdict that decided nothing must not be among them.
// The cost log carries it too, against this run and item as a review, on a line
// marked as a comparison: the accomplishment reading takes every other review
// line as a review that judged the change. The sessions those lines name are
// recorded on the run's record of the second review, which is how the reading
// counts each line as that review's own cost (readmodel/comparisoncost.go).
//
// It is a provider call, so it is not made while the operator holds harness
// activity or once a stop of the run was requested. It is skipped rather than
// waited for, because the verdict the run acts on is already in hand.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/cost/costevent"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// compareDocumentReview makes the second review when the setting is on and
// records it on the run. It returns nothing: the run goes on exactly as the
// deciding review left it.
func (a *activeRun) compareDocumentReview(ctx context.Context, request review.Request, deciding review.Result, headCommit string) {
	p := a.pipeline
	model, ok := p.Config.DocumentComparisonModel(p.reviewer())
	if !ok {
		return
	}
	choosing, ok := p.Reviewer.(modelChoosingReviewer)
	if !ok {
		return
	}
	comparison := runstate.ReviewComparison{
		At:               p.clock().Now().UTC(),
		HeadCommit:       headCommit,
		DecidingModel:    deciding.RequestedModel,
		DecidingDecision: string(deciding.Decision),
		CheaperModel:     model,
	}
	if reason := a.comparisonWithheld(); reason != "" {
		comparison.Problem = "the second review was not made: " + reason
		a.state.RecordReviewComparison(comparison)
		return
	}
	request.Reask = nil
	request.LastSequence = a.state.LastSequence
	request.EventSink = func(event execution.Event) error {
		if !costevent.Carries(event) {
			return nil
		}
		comparison.Sessions = withSession(comparison.Sessions, eventSession(event))
		return a.sink(event)
	}
	request.Spend.Comparison = true
	result, err := choosing.OnModel(model).Review(ctx, request)
	if result.LastSequence > a.state.LastSequence {
		a.state.LastSequence = result.LastSequence
	}
	comparison.Sessions = withSession(comparison.Sessions, result.SessionID)
	comparison.At = p.clock().Now().UTC()
	switch {
	case err != nil:
		comparison.Problem = "the second review failed: " + err.Error()
	case !result.Decision.Valid():
		comparison.Problem = "the second review returned no verdict the contract accepts"
	default:
		comparison.CheaperDecision = string(result.Decision)
		comparison.CheaperSummary = strings.TrimSpace(result.Verdict.Summary)
		comparison.CheaperFindings = len(result.Verdict.Findings)
	}
	a.state.RecordReviewComparison(comparison)
}

// eventSession is the provider session a cost-carrying event names, and empty
// where it names none.
func eventSession(event execution.Event) string {
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.SessionID)
}

// withSession adds a session to the list once.
func withSession(sessions []string, session string) []string {
	session = strings.TrimSpace(session)
	if session == "" || slices.Contains(sessions, session) {
		return sessions
	}
	return append(sessions, session)
}

// comparisonWithheld says why a second review must not be started now, and is
// empty where it may be. A record that cannot be read withholds it too: nothing
// waits on the second review, so there is nothing to weigh against spending.
func (a *activeRun) comparisonWithheld() string {
	if _, held, err := a.pipeline.operatorHold(); err != nil {
		return err.Error()
	} else if held {
		return "the operator has paused harness activity"
	}
	if err := a.stopRequestedAtNewAttempt(); err != nil {
		return err.Error()
	}
	return ""
}
