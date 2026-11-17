package config

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Checks on CI: execution.ci_checks, the switch that moves most of a run's
// testing from this machine to the forge (docs/designs/checks-on-ci.md).
//
// This file is the setting and what is refused when it is read. Nothing here
// pushes, waits or reads a result; the builds that do consult it.
//
// It chooses where checks run and grants nothing: the checks named are ones the
// project declares, and every approval, protected-path and review rule applies
// as it did.

// DefaultCIWaitLimit is how long the harness waits for the required checks,
// measured from the push, where the configuration names no limit.
const DefaultCIWaitLimit = Duration(60 * time.Minute)

// CIChecks is execution.ci_checks. The zero value is the switch off, which is
// every project's behaviour before the key existed.
type CIChecks struct {
	// Enabled turns the flow on. Off, a run's checks, timing and records are
	// exactly what they were: no push before review, no wait, and the full
	// declared checks run on this machine before review.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Local names the declared checks that form the short check run on this
	// machine before the push, each written as Checks writes it.
	Local []string `yaml:"local,omitempty" json:"local,omitempty"`
	// Required names the forge's checks that must pass on the candidate commit.
	// Only these are counted. It must not be empty while Enabled is true.
	Required []string `yaml:"required,omitempty" json:"required,omitempty"`
	// WaitLimit is how long to wait for the required checks to finish, measured
	// from the push. Zero reads as DefaultCIWaitLimit; see WaitLimitInForce.
	WaitLimit Duration `yaml:"wait_limit,omitempty" json:"wait_limit,omitempty"`
}

// WaitLimitInForce is the wait limit a run uses: the configured one, or the
// default where none is configured.
func (c CIChecks) WaitLimitInForce() time.Duration {
	if c.WaitLimit <= 0 {
		return DefaultCIWaitLimit.Duration()
	}
	return c.WaitLimit.Duration()
}

// ForgeCapability is one thing the flow needs from the project's forge adapter.
type ForgeCapability string

const (
	ForgePushBranch       ForgeCapability = "push a branch"
	ForgeOpenDraftRequest ForgeCapability = "open a draft pull request"
	ForgeReadCheckResults ForgeCapability = "read check results for a named commit"
	ForgeReadCheckOutput  ForgeCapability = "read check output for a named commit"
)

// CIForgeCapabilities lists what checks on CI needs of a forge adapter, in the
// order a refusal names what is missing.
func CIForgeCapabilities() []ForgeCapability {
	return []ForgeCapability{ForgePushBranch, ForgeOpenDraftRequest, ForgeReadCheckResults, ForgeReadCheckOutput}
}

// forgeAdapterCapabilities is what each forge adapter the harness ships can do
// today. GitHub is the only one. It pushes run branches and reads check results
// and the output of a failed job, but it does not yet open a pull request as a
// draft, so enabling the flow is refused until the forge adapter build adds
// that and records it here.
var forgeAdapterCapabilities = map[string]map[ForgeCapability]bool{
	"github": {
		ForgePushBranch:       true,
		ForgeOpenDraftRequest: false,
		ForgeReadCheckResults: true,
		ForgeReadCheckOutput:  true,
	},
}

// ProjectForgeAdapter names the forge adapter the project's publishing uses.
// The harness ships one, GitHub, and no key chooses another.
func (c Config) ProjectForgeAdapter() string { return "github" }

// MissingCIForgeCapabilities lists what checks on CI needs and the project's
// forge adapter cannot do, in the order CIForgeCapabilities lists them.
func (c Config) MissingCIForgeCapabilities() []ForgeCapability {
	can := forgeAdapterCapabilities[c.ProjectForgeAdapter()]
	var missing []ForgeCapability
	for _, capability := range CIForgeCapabilities() {
		if !can[capability] {
			missing = append(missing, capability)
		}
	}
	return missing
}

// ciCheckProblems is what is refused when the setting is read. A switch that is
// off is checked for the names it holds, so a typo is found before it is
// turned on; the requirements that only matter to a flow in use are refused
// only once it is.
func ciCheckProblems(c Config) []string {
	ci := c.Execution.CIChecks
	var problems []string
	declared := map[string]bool{}
	for _, check := range c.Checks {
		declared[strings.TrimSpace(check)] = true
	}
	seen := map[string]bool{}
	for _, name := range ci.Local {
		key := strings.TrimSpace(name)
		switch {
		case key == "":
			problems = append(problems, "execution.ci_checks.local names a check with no name")
		case !declared[key]:
			problems = append(problems, fmt.Sprintf("execution.ci_checks.local names %q, which is no check this project declares: name a command exactly as checks writes it", name))
		case seen[key]:
			problems = append(problems, fmt.Sprintf("execution.ci_checks.local names %q more than once", name))
		}
		seen[key] = true
	}
	for _, name := range ci.Required {
		if strings.TrimSpace(name) == "" {
			problems = append(problems, "execution.ci_checks.required names a check with no name")
		}
	}
	if ci.WaitLimit < 0 {
		problems = append(problems, "execution.ci_checks.wait_limit cannot be negative")
	}
	if !ci.Enabled {
		return problems
	}
	if len(ci.Required) == 0 {
		problems = append(problems, "execution.ci_checks.required must name at least one forge check while execution.ci_checks.enabled is true: with none, nothing would decide whether the change passed on the forge")
	}
	if missing := c.MissingCIForgeCapabilities(); len(missing) > 0 {
		names := make([]string, len(missing))
		for i, capability := range missing {
			names[i] = string(capability)
		}
		sort.Strings(names)
		problems = append(problems, fmt.Sprintf("execution.ci_checks.enabled is true, but this project's forge adapter (%s) cannot %s, and checks on CI needs it", c.ProjectForgeAdapter(), strings.Join(names, " or ")))
	}
	return problems
}
