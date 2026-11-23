package config

import (
	"strings"
	"testing"
)

// The reviewer's model by kind of change: the default a project stating
// nothing gets, the bound on code reviews, and the refusals at load.

func TestAProjectStatingNothingReviewsBothKindsOfChangeOnTheReviewersOwnModel(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	reviewer := cfg.Agents["reviewer"]
	for _, document := range []bool{false, true} {
		if kind := cfg.Execution.ReviewerModels.KindFor(document); kind != ModelKindStronger {
			t.Fatalf("KindFor(document=%v) = %q, want stronger", document, kind)
		}
		choice := cfg.ReviewerModelFor(reviewer, document)
		if choice.Model != reviewer.Model || choice.Kind != ModelKindStronger {
			t.Fatalf("ReviewerModelFor(document=%v) = %+v, want the reviewer agent's own model %q", document, choice, reviewer.Model)
		}
	}
}

func TestDocumentReviewsMayBeSetToTheCheaperModelAndCodeReviewsStayStronger(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`execution:
  reviewer_models:
    documents: cheaper
    code: stronger
`, nil)
	cfg := resolved.Config
	reviewer := cfg.Agents["reviewer"]
	documents := cfg.ReviewerModelFor(reviewer, true)
	if documents.Model != "sonnet" || documents.Kind != ModelKindCheaper {
		t.Fatalf("document review = %+v, want Claude Code's cheaper model sonnet", documents)
	}
	if !strings.Contains(documents.Reason, "execution.reviewer_models names the cheaper model for a review of a document publication") {
		t.Fatalf("document review reason = %q", documents.Reason)
	}
	code := cfg.ReviewerModelFor(reviewer, false)
	if code.Model != reviewer.Model || code.Kind != ModelKindStronger {
		t.Fatalf("code review = %+v, want the reviewer agent's own model %q", code, reviewer.Model)
	}
	for _, key := range []string{"execution.reviewer_models.documents", "execution.reviewer_models.code"} {
		if resolved.Origins[key] == "" {
			t.Fatalf("no origin recorded for %s", key)
		}
	}
}

func TestACodeReviewBelowTheStrongerModelIsRefusedAtLoad(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"cheaper", "lightest"} {
		_, err := loadProjectError(t, minimalProjectConfig+`execution:
  reviewer_models:
    code: `+kind+`
`, nil)
		if err == nil {
			t.Fatalf("code reviews on the %s model loaded", kind)
		}
		want := `execution.reviewer_models.code is "` + kind + `"; it must be "stronger", because a review of code is never made below the stronger model`
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %v\nwant it to say %q", err, want)
		}
	}
}

func TestReviewerModelValuesOutsideTheirBoundsAreRefusedAtLoad(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ value, want string }{
		{"lightest", `execution.reviewer_models.documents is "lightest"; it must be one of "stronger" or "cheaper"`},
		{"opus", `execution.reviewer_models.documents is "opus"; it must be one of "stronger" or "cheaper"`},
	} {
		_, err := loadProjectError(t, minimalProjectConfig+`execution:
  reviewer_models:
    documents: `+tc.value+`
`, nil)
		if err == nil {
			t.Fatalf("document reviews on %q loaded", tc.value)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("refusal = %v\nwant it to say %q", err, tc.want)
		}
	}
}

func TestCheaperDocumentReviewsForAReviewerWhoseProviderNamesNoCheaperModelAreRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  reviewer:
    role: reviewer
    backend: codex
    model: gpt-5
execution:
  reviewer_models:
    documents: cheaper
`, nil)
	if err == nil {
		t.Fatal("cheaper document reviews loaded for a reviewer on a provider naming no cheaper model")
	}
	want := `execution.reviewer_models.documents names the cheaper model, but provider codex, which reviewer agent "reviewer" runs on, names no cheaper model`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %v\nwant it to say %q", err, want)
	}
}

func TestTheDevelopmentManagerMayApplyTheReviewerModels(t *testing.T) {
	t.Parallel()

	if err := RoleMayApply("execution.reviewer_models.documents"); err != nil {
		t.Fatalf("RoleMayApply = %v, want it applicable", err)
	}
}
