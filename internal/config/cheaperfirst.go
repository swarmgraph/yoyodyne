package config

// Models named by kind, and developer runs that start on the cheaper one.
//
// docs/designs/reducing-what-the-factory-costs.md, sections 4 and 8. A project
// names, for each provider, which of that provider's models is the stronger,
// the cheaper and the lightest (execution.model_kinds); the shipped defaults
// name them for Claude Code only. With execution.developer_cheaper_first on, a
// developer run over an eligible item starts on the cheaper model and moves
// once to the stronger after its first failed review or failed checks
// (internal/orchestrator/cheaperfirst.go), and eligible runs alternate between
// starting cheaper and starting stronger so the two can be compared.
//
// Two things here are held in code and no configuration reaches them, because
// the management loop protocol rules them: a priority 0 item is never
// eligible, and neither is an item carrying one of the four fixed high risk
// labels. execution.high_risk_labels adds labels to that list and cannot take
// any away. Every setting selects a model and nothing else, so nothing here can
// widen what a run may do (configuration-never-grants-authority).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ModelKind is one of the three kinds of model the cost design speaks of.
type ModelKind string

const (
	ModelKindStronger ModelKind = "stronger"
	ModelKindCheaper  ModelKind = "cheaper"
	ModelKindLightest ModelKind = "lightest"
)

// ModelKindNames is which of one provider's models is each kind. A kind left
// empty takes the shipped default for that provider, where there is one; a
// provider with no default and nothing named has no model of that kind.
type ModelKindNames struct {
	Stronger string `yaml:"stronger,omitempty" json:"stronger,omitempty"`
	Cheaper  string `yaml:"cheaper,omitempty" json:"cheaper,omitempty"`
	Lightest string `yaml:"lightest,omitempty" json:"lightest,omitempty"`
}

func (n ModelKindNames) of(kind ModelKind) string {
	switch kind {
	case ModelKindStronger:
		return strings.TrimSpace(n.Stronger)
	case ModelKindCheaper:
		return strings.TrimSpace(n.Cheaper)
	case ModelKindLightest:
		return strings.TrimSpace(n.Lightest)
	}
	return ""
}

// defaultModelKinds is what the shipped bundle names. Codex, and any provider
// a project declares, names none: a project says which of its models is which.
var defaultModelKinds = map[domain.Backend]ModelKindNames{
	domain.BackendClaudeCode: {Stronger: "opus", Cheaper: "sonnet", Lightest: "haiku"},
}

// FixedHighRiskLabels are the labels that mark work on security, migrations,
// concurrency and public interfaces. Work carrying one never starts on the
// cheaper model, whatever the configuration says.
var FixedHighRiskLabels = []string{"security", "migration", "concurrency", "public-interface"}

const (
	// DefaultCheaperFirstTrialRuns is how many eligible runs the trial
	// alternates before its comparison is due, and also the least a project may
	// set: fewer runs than this compares too little to decide anything on.
	DefaultCheaperFirstTrialRuns = 40
	// MaxCheaperFirstTrialRuns bounds the trial so it ends in something a
	// development manager can read and decide on.
	MaxCheaperFirstTrialRuns = 1000
	// lowestTrackerPriority is the tracker's lowest priority; 0 is its highest.
	lowestTrackerPriority = 4
)

// DeveloperCheaperFirst is execution.developer_cheaper_first: whether eligible
// developer runs start on the cheaper model, and which items are eligible.
type DeveloperCheaperFirst struct {
	// Enabled turns the change on. Off, which is the default, every run starts
	// on the model it always did.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Labels, when any are named, limits eligibility to items carrying one of
	// them. Empty is every item that is otherwise eligible.
	Labels []string `yaml:"labels,omitempty" json:"labels,omitempty"`
	// Priorities is which tracker priorities are eligible, each 1 to 4. Empty
	// is all four. Priority 0 is never eligible and is refused if named.
	Priorities []int `yaml:"priorities,omitempty" json:"priorities,omitempty"`
	// TrialRuns is how many eligible runs the trial alternates before its
	// comparison is due; zero is DefaultCheaperFirstTrialRuns.
	TrialRuns int `yaml:"trial_runs,omitempty" json:"trial_runs,omitempty"`
}

// TrialTarget is how many eligible runs the trial alternates before its
// comparison is due.
func (d DeveloperCheaperFirst) TrialTarget() int {
	if d.TrialRuns <= 0 {
		return DefaultCheaperFirstTrialRuns
	}
	return d.TrialRuns
}

// ModelOfKind is the model a provider names for a kind, and false where it
// names none. A kind the project names wins; otherwise the shipped default for
// that provider applies.
func (e Execution) ModelOfKind(provider domain.Backend, kind ModelKind) (string, bool) {
	if named := e.ModelKinds[provider].of(kind); named != "" {
		return named, true
	}
	if named := defaultModelKinds[provider].of(kind); named != "" {
		return named, true
	}
	return "", false
}

// HighRiskLabels is every label that keeps an item off the cheaper model: the
// four fixed ones, then whatever the project added, each once.
func (e Execution) HighRiskLabels() []string {
	labels := append([]string(nil), FixedHighRiskLabels...)
	seen := make(map[string]bool, len(labels))
	for _, label := range labels {
		seen[label] = true
	}
	for _, label := range e.HighRiskLabelsAdded {
		trimmed := strings.TrimSpace(label)
		if trimmed != "" && !seen[trimmed] {
			seen[trimmed] = true
			labels = append(labels, trimmed)
		}
	}
	return labels
}

// CheaperFirstEligibility is whether one item may start on the cheaper model,
// and why or why not, said in words a run's record can carry.
type CheaperFirstEligibility struct {
	Eligible bool
	Reason   string
}

// CheaperFirstEligible decides whether an item with these labels and this
// priority may start on the cheaper model. A priority nobody recorded is not
// eligible: an item whose priority is unknown might be priority 0.
func (e Execution) CheaperFirstEligible(labels []string, priority *int) CheaperFirstEligibility {
	setting := e.DeveloperCheaperFirst
	if !setting.Enabled {
		return CheaperFirstEligibility{Reason: "execution.developer_cheaper_first is off"}
	}
	if priority == nil {
		return CheaperFirstEligibility{Reason: "the item's priority was not recorded, so it is treated as priority 0, which is never eligible"}
	}
	if *priority == 0 {
		return CheaperFirstEligibility{Reason: "the item is priority 0, which never starts on the cheaper model"}
	}
	for _, label := range e.HighRiskLabels() {
		if carries(labels, label) {
			return CheaperFirstEligibility{Reason: fmt.Sprintf("the item carries the high risk label %q, so it never starts on the cheaper model", label)}
		}
	}
	if len(setting.Priorities) > 0 && !containsInt(setting.Priorities, *priority) {
		return CheaperFirstEligibility{Reason: fmt.Sprintf("the item is priority %d, which execution.developer_cheaper_first.priorities does not name", *priority)}
	}
	if len(setting.Labels) > 0 {
		for _, label := range setting.Labels {
			if carries(labels, label) {
				return CheaperFirstEligibility{Eligible: true, Reason: fmt.Sprintf("the item is priority %d and carries %q, which execution.developer_cheaper_first.labels names", *priority, strings.TrimSpace(label))}
			}
		}
		return CheaperFirstEligibility{Reason: "the item carries no label execution.developer_cheaper_first.labels names"}
	}
	return CheaperFirstEligibility{Eligible: true, Reason: fmt.Sprintf("the item is priority %d and carries no high risk label", *priority)}
}

// MatchesDeveloperModelRule reports an item execution.developer_models sends
// to a model by one of its labels. Such an item keeps that model: the mapping
// is a choice somebody made for that work, and starting it cheaper would undo
// it.
func MatchesDeveloperModelRule(mapping []DeveloperModelRule, labels []string) bool {
	for _, rule := range mapping {
		if carries(labels, rule.Label) {
			return true
		}
	}
	return false
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// cheaperFirstProblems refuses a value outside the bounds held here: a
// provider nobody can run, a model name that names no model, priority 0 or a
// priority the tracker does not have, a trial too short to compare, a label
// the tracker would not carry, an eligible label that is also a high risk one,
// and the change turned on for a developer whose provider names no cheaper
// model.
func cheaperFirstProblems(c Config) []string {
	var problems []string
	execution := c.Execution
	providers := make([]string, 0, len(execution.ModelKinds))
	for provider := range execution.ModelKinds {
		providers = append(providers, string(provider))
	}
	sort.Strings(providers)
	for _, name := range providers {
		provider := domain.Backend(name)
		if !knownProvider(c, provider) {
			problems = append(problems, fmt.Sprintf("execution.model_kinds names provider %q, which is neither claude-code, codex, nor a provider this project declares", name))
			continue
		}
		names := execution.ModelKinds[provider]
		for _, kind := range []ModelKind{ModelKindStronger, ModelKindCheaper, ModelKindLightest} {
			raw := names.of(kind)
			if raw == "" {
				continue
			}
			if err := ValidateModelSelector(raw); err != nil {
				problems = append(problems, fmt.Sprintf("execution.model_kinds.%s.%s: %v", name, kind, err))
			}
		}
	}

	setting := execution.DeveloperCheaperFirst
	seenPriority := map[int]bool{}
	for _, priority := range setting.Priorities {
		switch {
		case priority == 0:
			problems = append(problems, "execution.developer_cheaper_first.priorities names priority 0, which is never eligible to start on the cheaper model")
		case priority < 0 || priority > lowestTrackerPriority:
			problems = append(problems, fmt.Sprintf("execution.developer_cheaper_first.priorities names priority %d; the tracker's priorities run from 0 to %d, and only 1 to %d may be eligible", priority, lowestTrackerPriority, lowestTrackerPriority))
		case seenPriority[priority]:
			problems = append(problems, fmt.Sprintf("execution.developer_cheaper_first.priorities names priority %d twice", priority))
		}
		seenPriority[priority] = true
	}
	if setting.TrialRuns != 0 && (setting.TrialRuns < DefaultCheaperFirstTrialRuns || setting.TrialRuns > MaxCheaperFirstTrialRuns) {
		problems = append(problems, fmt.Sprintf("execution.developer_cheaper_first.trial_runs is %d; it must be between %d and %d, or left out for %d", setting.TrialRuns, DefaultCheaperFirstTrialRuns, MaxCheaperFirstTrialRuns, DefaultCheaperFirstTrialRuns))
	}
	highRisk := map[string]bool{}
	for _, label := range execution.HighRiskLabels() {
		highRisk[label] = true
	}
	problems = append(problems, labelListProblems("execution.high_risk_labels", execution.HighRiskLabelsAdded, nil)...)
	problems = append(problems, labelListProblems("execution.developer_cheaper_first.labels", setting.Labels, highRisk)...)

	if setting.Enabled {
		for _, name := range sortedConfigAgentNames(c.Agents) {
			agent := c.Agents[name]
			if agent.Role != domain.RoleDeveloper {
				continue
			}
			if _, named := execution.ModelOfKind(agent.Backend, ModelKindCheaper); !named {
				problems = append(problems, fmt.Sprintf("execution.developer_cheaper_first is on, but provider %s, which developer agent %q runs on, names no cheaper model in execution.model_kinds", agent.Backend, name))
			}
		}
	}
	return problems
}

// labelListProblems refuses a label the tracker would not carry, a label named
// twice, and, where highRisk is given, a label that is also a high risk one:
// an eligible label that is high risk would make nothing eligible.
func labelListProblems(key string, labels []string, highRisk map[string]bool) []string {
	var problems []string
	seen := map[string]bool{}
	for index, label := range labels {
		if err := domain.ValidateLabel(label); err != nil {
			problems = append(problems, fmt.Sprintf("%s entry %d: %v", key, index+1, err))
			continue
		}
		trimmed := strings.TrimSpace(label)
		if seen[trimmed] {
			problems = append(problems, fmt.Sprintf("%s names %q twice", key, trimmed))
		}
		seen[trimmed] = true
		if highRisk[trimmed] {
			problems = append(problems, fmt.Sprintf("%s names %q, which is a high risk label; work carrying it never starts on the cheaper model", key, trimmed))
		}
	}
	return problems
}

func knownProvider(c Config, provider domain.Backend) bool {
	if provider == domain.BackendClaudeCode || provider == domain.BackendCodex {
		return true
	}
	_, declared := c.Providers[string(provider)]
	return declared
}

func sortedConfigAgentNames(agents map[string]AgentConfig) []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
