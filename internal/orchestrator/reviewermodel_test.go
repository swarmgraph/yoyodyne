package orchestrator

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func reviewerModelPipeline(setting config.ReviewerModels) Pipeline {
	return Pipeline{
		Config: config.Config{
			Agents: map[string]config.AgentConfig{
				"developer": {Role: domain.RoleDeveloper, Backend: domain.BackendClaudeCode, Model: "opus"},
				"reviewer":  {Role: domain.RoleReviewer, Backend: domain.BackendClaudeCode, Model: "opus"},
			},
			Execution: config.Execution{ReviewerModels: setting},
		},
		Reviewer: review.Reviewer{Model: "opus", Persona: "the reviewer persona"},
	}
}

func TestADocumentPublicationIsReviewedOnTheModelItsKindNames(t *testing.T) {
	t.Parallel()

	pipeline := reviewerModelPipeline(config.ReviewerModels{Documents: config.ModelKindCheaper})
	document := runstate.State{Document: &runstate.DocumentPublication{}}
	if !isDocumentReview(document) || isDocumentReview(runstate.State{}) {
		t.Fatal("isDocumentReview does not tell a document publication from a code change")
	}

	reviewer, model := pipeline.reviewerForChange(isDocumentReview(document))
	if model != "sonnet" {
		t.Fatalf("document review model = %q, want sonnet", model)
	}
	chosen, ok := reviewer.(review.Reviewer)
	if !ok || chosen.Model != "sonnet" {
		t.Fatalf("document reviewer = %#v, want the reviewer on sonnet", reviewer)
	}
	// Only the model changes: the reviewer's persona, and so its role, is the
	// same reviewer's.
	if chosen.Persona != "the reviewer persona" {
		t.Fatalf("document reviewer persona = %q, want the reviewer's own", chosen.Persona)
	}

	reviewer, model = pipeline.reviewerForChange(false)
	if model != "opus" || reviewer.(review.Reviewer).Model != "opus" {
		t.Fatalf("code review model = %q, want the reviewer agent's own opus", model)
	}
}

func TestAProjectStatingNoReviewerModelsReviewsDocumentsOnTheReviewersOwnModel(t *testing.T) {
	t.Parallel()

	pipeline := reviewerModelPipeline(config.ReviewerModels{})
	for _, document := range []bool{false, true} {
		reviewer, model := pipeline.reviewerForChange(document)
		if model != "opus" || reviewer.(review.Reviewer).Model != "opus" {
			t.Fatalf("review of document=%v on %q, want the reviewer agent's own opus", document, model)
		}
	}
}
