package orchestrator

// The model a review is made on, chosen by kind of change
// (execution.reviewer_models, internal/config/reviewermodels.go;
// docs/designs/reducing-what-the-factory-costs.md, section 5).
//
// A run publishing a document is a document review; every other run is a code
// review. Only the model changes: the reviewer is the same reviewer, under the
// same contract and permissions, and never the role that wrote the change. A
// review made on a reviewer endpoint pair (reviewerrouting.go) keeps the
// endpoint's model, because the endpoint names its model, as a developer run on
// a slot's endpoint pair keeps its own.

import (
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// modelChoosingReviewer is a reviewer that can be asked to review on another
// model. review.Reviewer is one; a reviewer that is not keeps its own model.
type modelChoosingReviewer interface {
	OnModel(model string) review.Reviewer
}

// isDocumentReview reports a run whose change is a document publication.
func isDocumentReview(state runstate.State) bool {
	return state.Document != nil
}

// reviewerForChange is the reviewer for a change of this kind and the model it
// is asked to review on, which the reviewer's reported model is held to.
func (p Pipeline) reviewerForChange(document bool) (ChangeReviewer, string) {
	agent := p.reviewer()
	configured := agent.Model
	choice := p.Config.ReviewerModelFor(agent, document)
	model := strings.TrimSpace(choice.Model)
	if model == "" || model == strings.TrimSpace(configured) {
		return p.Reviewer, configured
	}
	choosing, ok := p.Reviewer.(modelChoosingReviewer)
	if !ok {
		// Staying on the reviewer's own model is the stronger review, never a
		// weaker one.
		return p.Reviewer, configured
	}
	return choosing.OnModel(model), model
}
