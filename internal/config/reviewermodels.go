package config

// The reviewer's model, chosen by kind of change.
//
// docs/designs/reducing-what-the-factory-costs.md, sections 5 and 8.
// execution.reviewer_models names a model kind for reviews of code and for
// reviews of a document publication. Both default to the stronger kind, and
// the stronger kind for a review is the reviewer agent's own configured model,
// so a project that states nothing reviews exactly as it did before.
//
// Two bounds are held here and no configuration reaches them. A code review is
// never made below the stronger model: only `stronger` is accepted for `code`.
// A document review may be made on the stronger or the cheaper model and never
// the lightest, which nothing has compared with the stronger reviewer. The
// setting chooses a model and nothing else: the reviewer is still the reviewer
// role, under its own contract, and never the role that wrote the change
// (configuration-never-grants-authority).

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ReviewerModels is execution.reviewer_models: the model kind a review is
// made on, for each kind of change. An empty value is the stronger kind.
type ReviewerModels struct {
	// Documents is the kind for a review of a document publication: stronger
	// or cheaper.
	Documents ModelKind `yaml:"documents,omitempty" json:"documents,omitempty"`
	// Code is the kind for a review of every other change, and may only be
	// stronger.
	Code ModelKind `yaml:"code,omitempty" json:"code,omitempty"`
	// CompareDocuments has each document publication's review made a second
	// time on the cheaper model, over the same change and the same evidence,
	// for the trial section 5 of the design asks for. The second verdict is
	// recorded beside the deciding one and decides nothing. Off by default.
	CompareDocuments bool `yaml:"compare_documents,omitempty" json:"compare_documents,omitempty"`
}

// reviewerModelKinds is which kinds each kind of change may be reviewed on.
var reviewerModelKinds = map[string][]ModelKind{
	"documents": {ModelKindStronger, ModelKindCheaper},
	"code":      {ModelKindStronger},
}

// KindFor is the model kind a review of this kind of change is made on.
func (r ReviewerModels) KindFor(document bool) ModelKind {
	kind := r.Code
	if document {
		kind = r.Documents
	}
	if strings.TrimSpace(string(kind)) == "" {
		return ModelKindStronger
	}
	return ModelKind(strings.TrimSpace(string(kind)))
}

// ReviewerModelChoice is the model one review is made on, and why, in words a
// run's record can carry.
type ReviewerModelChoice struct {
	Model  string
	Kind   ModelKind
	Reason string
}

// ReviewerModelFor is the model a review of this kind of change is made on by
// the reviewer agent given. The stronger kind is the agent's own model; the
// cheaper kind is what execution.model_kinds names for the agent's provider.
// A provider naming no cheaper model keeps the agent's model, which a
// configuration that loaded cannot reach (reviewerModelProblems).
func (c Config) ReviewerModelFor(reviewer AgentConfig, document bool) ReviewerModelChoice {
	change := "code"
	if document {
		change = "a document publication"
	}
	configured := strings.TrimSpace(reviewer.Model)
	kind := c.Execution.ReviewerModels.KindFor(document)
	if kind == ModelKindStronger {
		return ReviewerModelChoice{Model: configured, Kind: kind,
			Reason: fmt.Sprintf("a review of %s is made on the stronger model, the reviewer agent's own %s", change, configured)}
	}
	model, named := c.Execution.ModelOfKind(reviewer.Backend, kind)
	if !named {
		return ReviewerModelChoice{Model: configured, Kind: ModelKindStronger,
			Reason: fmt.Sprintf("execution.reviewer_models names the %s model for a review of %s, but provider %s names none, so it is made on the reviewer agent's own %s", kind, change, reviewer.Backend, configured)}
	}
	return ReviewerModelChoice{Model: model, Kind: kind,
		Reason: fmt.Sprintf("execution.reviewer_models names the %s model for a review of %s, which is %s for provider %s", kind, change, model, reviewer.Backend)}
}

// DocumentComparisonModel is the model a document publication's second review
// is made on for the comparison (CompareDocuments): the cheaper model the
// reviewer agent's provider names. False where the comparison is off, where
// the provider names no cheaper model, or where the cheaper model is the one
// the deciding review is already made on, because a review compared with
// itself says nothing about the cheaper model.
func (c Config) DocumentComparisonModel(reviewer AgentConfig) (string, bool) {
	if !c.Execution.ReviewerModels.CompareDocuments {
		return "", false
	}
	model, named := c.Execution.ModelOfKind(reviewer.Backend, ModelKindCheaper)
	if !named || strings.TrimSpace(model) == "" {
		return "", false
	}
	if model == strings.TrimSpace(c.ReviewerModelFor(reviewer, true).Model) {
		return "", false
	}
	return model, true
}

// reviewerModelProblems refuses a kind that is not one of the three, a code
// review below the stronger model, a document review on the lightest, and a
// kind the reviewer agent's provider does not name. With compare_documents on
// it also refuses document reviews already made on the cheaper model, which
// leaves nothing stronger to compare the second review with, and a reviewer
// agent whose provider names no cheaper model to make the second review on.
func reviewerModelProblems(c Config) []string {
	var problems []string
	setting := c.Execution.ReviewerModels
	if setting.CompareDocuments {
		if setting.KindFor(true) != ModelKindStronger {
			problems = append(problems, "execution.reviewer_models.compare_documents is on while documents names the cheaper model; the comparison needs the deciding review of a document publication on the stronger model")
		}
		for _, name := range sortedConfigAgentNames(c.Agents) {
			agent := c.Agents[name]
			if agent.Role != domain.RoleReviewer {
				continue
			}
			if _, named := c.Execution.ModelOfKind(agent.Backend, ModelKindCheaper); !named {
				problems = append(problems, fmt.Sprintf("execution.reviewer_models.compare_documents is on, but provider %s, which reviewer agent %q runs on, names no cheaper model in execution.model_kinds", agent.Backend, name))
			}
		}
	}
	for _, entry := range []struct {
		key  string
		kind ModelKind
	}{{"documents", setting.Documents}, {"code", setting.Code}} {
		raw := strings.TrimSpace(string(entry.kind))
		if raw == "" {
			continue
		}
		allowed := reviewerModelKinds[entry.key]
		if !containsKind(allowed, ModelKind(raw)) {
			problems = append(problems, fmt.Sprintf("execution.reviewer_models.%s is %q; it must be %s", entry.key, raw, describeKinds(allowed)))
			continue
		}
		for _, name := range sortedConfigAgentNames(c.Agents) {
			agent := c.Agents[name]
			if agent.Role != domain.RoleReviewer || ModelKind(raw) == ModelKindStronger {
				continue
			}
			if _, named := c.Execution.ModelOfKind(agent.Backend, ModelKind(raw)); !named {
				problems = append(problems, fmt.Sprintf("execution.reviewer_models.%s names the %s model, but provider %s, which reviewer agent %q runs on, names no %s model in execution.model_kinds", entry.key, raw, agent.Backend, name, raw))
			}
		}
	}
	return problems
}

func containsKind(kinds []ModelKind, wanted ModelKind) bool {
	for _, kind := range kinds {
		if kind == wanted {
			return true
		}
	}
	return false
}

func describeKinds(kinds []ModelKind) string {
	if len(kinds) == 1 {
		return fmt.Sprintf("%q, because a review of code is never made below the stronger model", kinds[0])
	}
	quoted := make([]string, len(kinds))
	for i, kind := range kinds {
		quoted[i] = fmt.Sprintf("%q", kind)
	}
	return "one of " + strings.Join(quoted, " or ")
}
