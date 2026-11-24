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
// The second review is not given the run's event sink. The event log's review
// events are what the delivery and accomplishment readings take as the run's
// verdicts, and a verdict that decided nothing must not be among them. What it
// costs is still recorded, in the cost log, against this run and item as a
// review, on a line marked as a comparison: the accomplishment reading takes
// every other review line as a review that judged the change.

import (
	"context"
	"strings"

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
	request.Reask = nil
	request.EventSink = nil
	request.Spend.Comparison = true
	result, err := choosing.OnModel(model).Review(ctx, request)
	comparison := runstate.ReviewComparison{
		At:               p.clock().Now().UTC(),
		HeadCommit:       headCommit,
		DecidingModel:    deciding.RequestedModel,
		DecidingDecision: string(deciding.Decision),
		CheaperModel:     model,
	}
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
