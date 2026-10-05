package config

// What serves an agent's turn when the model it is configured for has no
// capacity.
//
// On 2026-09-07 at 06:00 the model the management roles run on closed its
// capacity window while the model the developers and reviewers run on still had
// one. The deciders stopped and the doers did not, so every item held for a
// development manager decision sat behind a role that could not take a turn,
// until a person hand-edited three agents onto the other model. Nothing about
// that was a failure the harness could see: each turn was refused rather than
// answered badly, and a refused turn is exactly what waiting out a window looks
// like.
//
// So an agent may name one alternate and be served by it while its own model's
// window is closed. It is per agent and off until the agent says otherwise,
// because which personas are worth serving from a second model is the operator's
// judgement rather than the harness's: a management role that must keep deciding
// and a developer whose work can wait out a window are different answers to the
// same question.
//
// What this never does is widen anything. The alternate is an endpoint the
// operator wrote in this agent's own block, so failover chooses between two
// stated endpoints and can never reach one nobody named — the same line every
// other key here holds: configuration selects, and never grants.
//
// The alternate names a provider and an account as well as a model, because the
// window that closes is not always the model's. A whole provider can decline —
// an account suspended, a subscription exhausted, a provider down — and an
// alternate that could only ever name another model on the same provider is no
// answer to that. Both keys default to this agent's own, so an agent that names
// only a model fails over exactly as it did before, within its own provider.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Failover is one agent's answer to the endpoint it runs on having no capacity.
//
// Enabled and the alternate are read together and stated together. A named
// alternate with Enabled false is how an operator turns the behaviour off
// without losing the endpoint they had chosen, which is what "willing to try it"
// needs: turning it back on is one word rather than a decision made again.
type Failover struct {
	// Effort overrides the level in developer and reviewer run endpoint
	// resolution. Empty requests the alternate model's default. Execution
	// consumers are connected separately from this configuration support.
	Effort *string `yaml:"effort,omitempty" json:"effort,omitempty"`
	// Enabled says this agent's turn may be served by the alternate below. It is
	// false unless the agent says otherwise, deliberately: an agent that acquired
	// failover by inheriting a bundle or by upgrading the executable would be one
	// nobody chose it for.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Model is the permitted alternate's model selector. There is exactly one
	// alternate because a list is a routing policy and this is a fallback: the
	// second endpoint either has capacity or the turn waits, and a third to try
	// after it would be the harness deciding which endpoints an agent is
	// interchangeable across.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// Provider is the provider that alternate is served by, and empty for this
	// agent's own — which is every agent that fails over within one provider, and
	// is what the key meant before it existed. Where it names another provider the
	// turn crosses providers: no session is resumed there, the context is rebuilt
	// from the durable record, and the substitution is refused outright unless
	// that provider can be held to this role's tool posture.
	Provider domain.Backend `yaml:"provider,omitempty" json:"provider,omitempty"`
	// Account is the alias the alternate authenticates under, and empty for this
	// agent's own. It is separate from the provider because the two are separate
	// facts: crossing to a provider this project's own account already signs in to
	// needs no second alias, and crossing to one it does not needs an alias and
	// nothing else.
	Account string `yaml:"account,omitempty" json:"account,omitempty"`
}

// Alternate is the model this agent's turn may be served by, and empty where
// nothing may serve it. Reading the enablement and the model as one answer is
// what keeps every caller from having to remember that a named alternate an
// operator switched off is not one.
func (f Failover) Alternate() string {
	if !f.Enabled {
		return ""
	}
	return strings.TrimSpace(f.Model)
}

// AlternateProvider is the provider the alternate is served by, given the
// provider this agent otherwise runs on. An agent that named none fails over
// within its own provider, which is what it did before the key existed.
func (f Failover) AlternateProvider(own domain.Backend) domain.Backend {
	if named := domain.Backend(strings.TrimSpace(string(f.Provider))); named != "" {
		return named
	}
	return own
}

// AlternateAccount is the alias the alternate authenticates under, given the
// alias this agent otherwise runs under. An agent that named none stays on its
// own account, which is the right answer for a substitution that does not leave
// the provider and the honest default for one that does — an account holding no
// provider of its own signs every one of them in.
func (f Failover) AlternateAccount(own string) string {
	if named := strings.TrimSpace(f.Account); named != "" {
		return named
	}
	return strings.TrimSpace(own)
}

// CrossesProviders reports an alternate served by a provider other than the one
// this agent runs on. It is the one question the whole of the rebuild path turns
// on: a substitution that stays on the provider resumes the session it was
// already holding, and one that leaves it has no session to resume.
func (f Failover) CrossesProviders(own domain.Backend) bool {
	return f.AlternateProvider(own) != own
}

// problems reports what makes one agent's failover block unusable. The agent's
// own block is passed because the alternate is only wrong relative to it: an
// alternate is the endpoint that is not this one.
func (f Failover) problems(name string, agent AgentConfig) []string {
	var problems []string
	if f.Effort != nil && agent.Role != domain.RoleDeveloper && agent.Role != domain.RoleReviewer {
		problems = append(problems, fmt.Sprintf("agent %q failover.effort is supported only for developer and reviewer run endpoint resolution", name))
	}
	alternate := strings.TrimSpace(f.Model)
	crosses := f.CrossesProviders(agent.Backend)
	if f.Enabled && alternate == "" {
		problems = append(problems, fmt.Sprintf("agent %q enables failover and names no alternate model; there is nothing for its turn to be served by", name))
	}
	if alternate != "" {
		if err := validateModelSelector(alternate); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q failover %s", name, err))
		}
		// Failing over to the endpoint whose window just closed is a second refusal
		// rather than an alternate, so it is refused here rather than met as a turn
		// that fails twice. The same model on another provider is a different
		// endpoint and a real alternate, which is why the provider is part of the
		// comparison rather than the model alone.
		if !crosses && alternate == strings.TrimSpace(agent.Model) &&
			f.AlternateAccount(agent.Account) == strings.TrimSpace(agent.Account) {
			problems = append(problems, fmt.Sprintf("agent %q names its own endpoint as its failover; an alternate is the endpoint that is not the one whose window closed", name))
		}
	}
	// A provider or an account is a statement about where the alternate is served,
	// so naming one while the block names nothing to serve is a half-written block
	// rather than a harmless key. It is refused whether or not failover is switched
	// on, because switching it off is how an operator keeps a choice they made and
	// there is no choice here to keep: `enabled: false` beside a model is a
	// decision parked, and beside a provider alone it is a decision never finished.
	provider := strings.TrimSpace(string(f.Provider))
	account := strings.TrimSpace(f.Account)
	if alternate == "" && (provider != "" || account != "") {
		problems = append(problems, fmt.Sprintf(
			"agent %q failover says where an alternate would be served and names no alternate model; %s answers where and failover.model answers what",
			name, describeFailoverPlacement(provider, account)))
	}
	if alternate == "" && f.Effort != nil {
		problems = append(problems, fmt.Sprintf("agent %q failover.effort names an alternate effort but failover.model is missing", name))
	}
	if provider != "" {
		if err := domain.ValidateIdentifier("failover provider", provider); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q %s", name, err))
		}
	}
	if account != "" {
		if err := domain.ValidateIdentifier("failover account", account); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q %s", name, err))
		}
	}
	return problems
}

// describeFailoverPlacement names the keys a half-written block actually wrote,
// so the refusal points at what is there rather than at both keys whichever one
// the operator used.
func describeFailoverPlacement(provider, account string) string {
	switch {
	case provider != "" && account != "":
		return "failover.provider and failover.account"
	case provider != "":
		return "failover.provider"
	default:
		return "failover.account"
	}
}

// failoverEndpointProblems reports an alternate this project could not serve the
// agent's turn on: a provider it does not name, a provider that cannot be held to
// the tool posture this agent's role requires, or an account holding another
// provider's authentication.
//
// It is asked here as well as at the moment of the substitution deliberately, and
// for the reason every other endpoint question is asked twice: hearing it when the
// file is read costs an edit, and hearing it when a window closes costs the turn
// the failover existed to save. The substitution asks again anyway, because a
// posture is not something to take on trust from a check that ran earlier.
func (c Config) failoverEndpointProblems(providers *backend.Registry, name string, agent AgentConfig) []string {
	failover := agent.Failover
	if failover.Alternate() == "" {
		return nil
	}
	alternate := failover.AlternateProvider(agent.Backend)
	if alternate == agent.Backend {
		return nil
	}
	var problems []string
	if _, known := providers.Lookup(alternate); !known {
		return append(problems, fmt.Sprintf("agent %q fails over to provider %q, which is not a provider this project names", name, alternate))
	}
	// The alternate has to hold this agent's role, on the same derivation the
	// agent's own provider is held to. A substitution that moved a reviewer onto a
	// provider whose sandbox cannot express no-tools would be a configuration
	// granting itself a weaker posture by naming a fallback, which is the one thing
	// a fallback may never do.
	if err := providers.Serves(alternate, agent.Role); err != nil {
		problems = append(problems, fmt.Sprintf("agent %q cannot fail over to provider %q: %v", name, alternate, err))
	}
	// The alias is resolved exactly as AgentFailoverEndpoint resolves it, through
	// the agent's own account rather than through the key it wrote down: an agent
	// that named no account is served by the pool's first account that can sign its
	// provider in, and asking about the key alone would let every such agent load
	// clean and then find at the first closed window that its alternate
	// authenticates as nobody. One resolution rather than two is what makes the
	// refusal here and the endpoint there the same answer.
	if alias := failover.AlternateAccount(c.agentAccountAlias(providers, name)); alias != "" {
		if _, declared := c.Accounts[alias]; !declared && strings.TrimSpace(failover.Account) != "" {
			problems = append(problems, fmt.Sprintf("agent %q fails over onto account %q, which this project does not declare", name, alias))
		} else if err := c.accountServes(providers, alias, alternate); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q cannot fail over to provider %q: %v", name, alternate, err))
		}
	}
	return problems
}

// AgentFailoverModel is the alternate one configured agent's turn may be served
// by, and empty for every agent that has not enabled failover — which is every
// agent until one says otherwise.
//
// It answers for an invocation that belongs to an agent rather than to a work
// item, which is the same set AgentAccountAlias answers for: a conversation and
// an exchange round are turns a role takes, and a role that cannot take one is
// the shape this exists for.
func (c Config) AgentFailoverModel(name string) string {
	return c.Agents[name].Failover.Alternate()
}

// AgentFailoverModelWithinProvider is the alternate for a caller that can only
// make the substitution on the provider the agent already runs on, and empty
// where the agent's alternate leaves it.
//
// It exists because asking a provider for a model that belongs to a different
// provider is not a fallback — it is an invocation that fails on a selector
// nobody there has heard of, and it would fail at exactly the moment the fallback
// was supposed to save the turn. A caller that cannot cross therefore does not
// substitute at all, which is the behaviour it had before crossings existed.
func (c Config) AgentFailoverModelWithinProvider(name string) string {
	agent := c.Agents[strings.TrimSpace(name)]
	if agent.Failover.CrossesProviders(agent.Backend) {
		return ""
	}
	return agent.Failover.Alternate()
}

// AgentFailover is one agent's whole failover block, which is what a caller that
// has to know where the alternate is served needs rather than only what model it
// asks for. An agent this configuration does not name has none, which is failover
// off.
func (c Config) AgentFailover(name string) Failover {
	return c.Agents[strings.TrimSpace(name)].Failover
}
