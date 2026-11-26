package config

// The model a small first conflict is tried on.
//
// docs/designs/approved-changes-that-no-longer-merge.md, decision 5.
// execution.conflict_resolution_model names a model kind. Left out, which is
// the default, every attempt to bring an approved change up to date asks for
// the model the run would have asked for anyway. Named, it reaches only an
// attempt whose conflicts lie in at most ConflictModelMaxFiles files and
// ConflictModelMaxLines conflicting lines, on an item that is not priority 0
// and carries no high risk label (the same list that keeps an item off the
// cheaper model at its start, Execution.HighRiskLabels), and only the run's
// first such attempt on a run that has not moved to the stronger model; the
// last two are read off the run (internal/orchestrator/conflictmodel.go).
//
// The bounds are held here and no configuration reaches them. The setting
// chooses a model and nothing else: the attempt is checked and reviewed exactly
// as any is (configuration-never-grants-authority).

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const (
	// ConflictModelMaxFiles is the most conflicting files an attempt may have
	// for the setting to reach it.
	ConflictModelMaxFiles = 3
	// ConflictModelMaxLines is the most conflicting lines, of either side, an
	// attempt may have for the setting to reach it.
	ConflictModelMaxLines = 200
	// ConflictModelTrialAttempts is how many eligible attempts alternate the
	// cheaper and the stronger model before the comparison is due.
	ConflictModelTrialAttempts = 20
)

// conflictModelKinds is the kinds the setting may name. The lightest model has
// never been compared with the stronger on any work, so it is refused.
var conflictModelKinds = []ModelKind{ModelKindCheaper, ModelKindStronger}

// ConflictModelKind is the kind the setting names, and false where it names
// none.
func (e Execution) ConflictModelKind() (ModelKind, bool) {
	kind := ModelKind(strings.TrimSpace(string(e.ConflictResolutionModel)))
	return kind, kind != ""
}

// ConflictModelConflict is the size of the conflict an attempt answers: the
// files Git named, how many more the record's bound left out, and the
// conflicting lines where they could be counted.
type ConflictModelConflict struct {
	Files   int
	Omitted int
	Lines   *int
}

// ConflictModelEligibility is whether the setting reaches one attempt, and why
// or why not, in words a run's record can carry.
type ConflictModelEligibility struct {
	Eligible bool
	Reason   string
}

// ConflictModelEligible decides what the configuration and the item decide
// about one attempt: the setting is named, the item is not priority 0 and
// carries no high risk label, and the conflict is small. A priority nobody
// recorded counts as priority 0, and a conflict whose lines were not counted
// is not known to be small, so neither is eligible.
func (e Execution) ConflictModelEligible(labels []string, priority *int, conflict ConflictModelConflict) ConflictModelEligibility {
	kind, named := e.ConflictModelKind()
	if !named {
		return ConflictModelEligibility{Reason: "execution.conflict_resolution_model is not set"}
	}
	if priority == nil {
		return ConflictModelEligibility{Reason: "the item's priority was not recorded, so it is treated as priority 0"}
	}
	if *priority == 0 {
		return ConflictModelEligibility{Reason: "the item is priority 0"}
	}
	for _, label := range e.HighRiskLabels() {
		if carries(labels, label) {
			return ConflictModelEligibility{Reason: fmt.Sprintf("the item carries the high risk label %q", label)}
		}
	}
	files := conflict.Files + conflict.Omitted
	if files == 0 {
		return ConflictModelEligibility{Reason: "Git named no conflicting file"}
	}
	if files > ConflictModelMaxFiles {
		return ConflictModelEligibility{Reason: fmt.Sprintf("the conflicts lie in %d files, more than %d", files, ConflictModelMaxFiles)}
	}
	if conflict.Lines == nil {
		return ConflictModelEligibility{Reason: "the conflicting lines could not be counted"}
	}
	if *conflict.Lines > ConflictModelMaxLines {
		return ConflictModelEligibility{Reason: fmt.Sprintf("the conflicts are %d lines, more than %d", *conflict.Lines, ConflictModelMaxLines)}
	}
	return ConflictModelEligibility{Eligible: true, Reason: fmt.Sprintf("execution.conflict_resolution_model names the %s model, and the item is priority %d with no high risk label and its conflicts lie in %d file(s) and %d line(s)",
		kind, *priority, files, *conflict.Lines)}
}

// conflictModelProblems refuses a kind that is not cheaper or stronger, and a
// kind a developer agent's provider does not name.
func conflictModelProblems(c Config) []string {
	kind, named := c.Execution.ConflictModelKind()
	if !named {
		return nil
	}
	if !containsKind(conflictModelKinds, kind) {
		return []string{fmt.Sprintf("execution.conflict_resolution_model is %q; it must be %q or %q, or left out", kind, ModelKindCheaper, ModelKindStronger)}
	}
	var problems []string
	for _, name := range sortedConfigAgentNames(c.Agents) {
		agent := c.Agents[name]
		if agent.Role != domain.RoleDeveloper || kind == ModelKindStronger {
			continue
		}
		if _, named := c.Execution.ModelOfKind(agent.Backend, kind); !named {
			problems = append(problems, fmt.Sprintf("execution.conflict_resolution_model names the %s model, but provider %s, which developer agent %q runs on, names no %s model in execution.model_kinds", kind, agent.Backend, name, kind))
		}
	}
	return problems
}
