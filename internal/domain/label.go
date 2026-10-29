package domain

// A work item's labels, and the developer slot that prefers one.
//
// A label is the tracker's own: a word the product manager or the development
// manager puts on an item, which everything that filters on it then reads. It is
// defined here rather than in the tracker client because two things hold a label
// to the same rule and only one of them talks to the tracker — the client refuses
// to write a label that is not one, and the configuration refuses a developer
// slot that prefers one that is not. One rule in one place is what keeps a slot
// from preferring a label no item could ever carry.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// labelPattern is what a label the harness writes or reads is held to. bd itself
// stores any string, spaces and all, and the narrowing is deliberate: a label is
// a word things are filtered on, so it is an identifier — one token, no
// whitespace, nothing bd's comma-separated flag spelling would split — and a
// label that is a sentence is a note wearing a label's clothes.
var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// MaxLabelBytes bounds one label. It is exported beside ValidateLabel so a
// caller can state the bound it refuses on rather than only that it refused.
const MaxLabelBytes = 64

// ValidateLabel refuses a label the harness will not write and no slot may
// prefer: anything that is not one identifier-shaped token within MaxLabelBytes.
// bd would accept what this refuses, so a caller that did not ask would find out
// from nobody.
func ValidateLabel(label string) error {
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		return errors.New("a label cannot be empty")
	}
	if len(trimmed) > MaxLabelBytes {
		return fmt.Errorf("label %q is %d bytes, limit is %d", trimmed, len(trimmed), MaxLabelBytes)
	}
	if !labelPattern.MatchString(trimmed) {
		return fmt.Errorf("label %q is not an identifier: one word of letters, digits, dots, underscores, and hyphens, starting with a letter or digit", trimmed)
	}
	return nil
}

// NeedsDesignLabel marks an item that is not to be started until the architect's
// design for it is readable (docs/designs/needs-design-gate.md). The harness
// holds such an item from every start it chooses for itself, exactly as it holds
// a parked one; a run already going on it carries on, and a run the operator
// names is not held, because naming one is an override.
const NeedsDesignLabel = "needs-design"

// NeedsDesign reports labels carrying NeedsDesignLabel, compared exactly as the
// tracker stores it, as every label is.
func NeedsDesign(labels []string) bool {
	for _, label := range labels {
		if strings.TrimSpace(label) == NeedsDesignLabel {
			return true
		}
	}
	return false
}

// DeveloperSlot is what one of the configured developer slots prefers. A slot is
// one unit of execution.max_concurrent_developers — the capacity one developer
// run takes — and a slot that prefers a label pulls the backlog's ready work
// carrying that label ahead of the rest, and the rest only when none of its
// label's work is ready. A slot preferring nothing pulls in the product
// manager's order, which is what every slot did before this existed.
//
// It is a preference and never a permission. A slot with a preference runs
// exactly what any slot runs, under the same contract, the same checks, and the
// same reviewer: configuration selects what a slot pulls first and never widens
// what it may do, which is the invariant configuration-never-grants-authority.
type DeveloperSlot struct {
	// Number, when stated, confirms this entry's one-based position. Keeping
	// identities in slot order preserves existing work preferences.
	Number *int `yaml:"number,omitempty" json:"number,omitempty"`
	// Prefer is the labels this slot pulls first, any one of which on an item is
	// enough. Empty is a slot with no preference, which is how a project says
	// slot 1 prefers nothing and slot 2 prefers something.
	Prefer []string `yaml:"prefer,omitempty" json:"prefer,omitempty"`
	// Dedicated keeps the slot for its labels: it takes their ready work ahead
	// of priority-0 work that does not carry them, where a slot that only
	// prefers a label takes priority-0 work first. With none of its labels'
	// work ready it still takes the ordinary order, and work on a change a run
	// already wrote still comes before either. A dedicated slot must name a
	// label, and not every slot may be dedicated, so priority-0 work always has
	// a slot that takes it first.
	Dedicated bool          `yaml:"dedicated,omitempty" json:"dedicated,omitempty"`
	Routing   *EndpointPair `yaml:"routing,omitempty" json:"routing,omitempty"`
	// Subagents overrides the developer agent's sub-agent setting for this
	// slot. Nil leaves the agent's setting as it is.
	Subagents *SlotSubagents `yaml:"subagents,omitempty" json:"subagents,omitempty"`
}

// SlotSubagents is a slot's own sub-agent setting: on or off, and the model,
// each of which wins over the developer agent's where it is stated.
type SlotSubagents struct {
	Enabled *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Model   string `yaml:"model,omitempty" json:"model,omitempty"`
}

// EndpointSpec selects an existing provider, model and account. Omitted
// placement inherits the role's defaults; an explicit empty effort clears the
// inherited level. It contains no credentials or machine-local paths.
type EndpointSpec struct {
	Provider     Backend `yaml:"provider,omitempty" json:"provider,omitempty"`
	Model        string  `yaml:"model,omitempty" json:"model,omitempty"`
	ModelVersion string  `yaml:"model_version,omitempty" json:"model_version,omitempty"`
	Account      string  `yaml:"account,omitempty" json:"account,omitempty"`
	Effort       *string `yaml:"effort,omitempty" json:"effort,omitempty"`
}

// EndpointPair is the ordered choice for one slot. A complete pair remains
// validated while disabled so enabling it cannot expose an unchecked endpoint.
type EndpointPair struct {
	Enabled   bool          `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Primary   *EndpointSpec `yaml:"primary,omitempty" json:"primary,omitempty"`
	Alternate *EndpointSpec `yaml:"alternate,omitempty" json:"alternate,omitempty"`
}

// Clone gives configuration resolution its own copy of the slot's choices.
func (s DeveloperSlot) Clone() DeveloperSlot {
	s.Prefer = append([]string(nil), s.Prefer...)
	if s.Number != nil {
		number := *s.Number
		s.Number = &number
	}
	if s.Routing != nil {
		pair := *s.Routing
		pair.Primary = cloneEndpointSpec(pair.Primary)
		pair.Alternate = cloneEndpointSpec(pair.Alternate)
		s.Routing = &pair
	}
	if s.Subagents != nil {
		subagents := *s.Subagents
		if subagents.Enabled != nil {
			enabled := *subagents.Enabled
			subagents.Enabled = &enabled
		}
		s.Subagents = &subagents
	}
	return s
}

func cloneEndpointSpec(spec *EndpointSpec) *EndpointSpec {
	if spec == nil {
		return nil
	}
	copy := *spec
	if spec.Effort != nil {
		effort := *spec.Effort
		copy.Effort = &effort
	}
	return &copy
}

// Preferring reports a slot that pulls labelled work first.
func (s DeveloperSlot) Preferring() bool {
	return len(s.Prefer) > 0
}

// Prefers reports whether an item carrying these labels is what the slot pulls
// first. Labels are compared exactly, as the tracker stores them: "Reliability"
// and "reliability" are two labels, and a slot that folded them would pull work
// nobody labelled for it.
func (s DeveloperSlot) Prefers(labels []string) bool {
	for _, preferred := range s.Prefer {
		for _, label := range labels {
			if strings.TrimSpace(label) == strings.TrimSpace(preferred) {
				return true
			}
		}
	}
	return false
}

// Problems is everything wrong with what a slot prefers: a label the tracker
// would not carry, a label named twice, and a slot kept for its label that
// names none. A slot preferring nothing and not dedicated has none.
func (s DeveloperSlot) Problems() []error {
	var problems []error
	if s.Dedicated && len(s.Prefer) == 0 {
		problems = append(problems, errors.New("it is marked dedicated and names no label under prefer, so there is no work it is kept for"))
	}
	seen := make(map[string]struct{}, len(s.Prefer))
	for _, label := range s.Prefer {
		if err := ValidateLabel(label); err != nil {
			problems = append(problems, err)
			continue
		}
		trimmed := strings.TrimSpace(label)
		if _, repeated := seen[trimmed]; repeated {
			problems = append(problems, fmt.Errorf("label %q is named twice", trimmed))
		}
		seen[trimmed] = struct{}{}
	}
	return problems
}
