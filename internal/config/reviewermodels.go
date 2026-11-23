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

// reviewerModelProblems refuses a kind that is not one of the three, a code
// review below the stronger model, a document review on the lightest, and a
// kind the reviewer agent's provider does not name.
func reviewerModelProblems(c Config) []string {
	var problems []string
	setting := c.Execution.ReviewerModels
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
