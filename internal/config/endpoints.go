package config

// The pool an invocation is chosen from, keyed on endpoints.
//
// An account was the unit while there was one provider and one model per
// project: rotating accounts was the whole of the choice, because everything
// else about where an invocation went was fixed. It stops being the unit the
// moment a project names two providers or two models. The same account reaching
// two providers is two sandboxes with two postures; the same provider under two
// accounts is two subscriptions with two limits; the same account and provider
// asking two models is two capacity windows. A pool keyed on any one of those
// alone cannot say which of them refused, and — worse — cannot refuse to send a
// role somewhere its tool posture cannot be held.
//
// So the pool holds endpoints. The accounts underneath it rotate and reserve
// exactly as they always did, which is why the ordering here is the same
// ordering ChooseAccount reads: what changes is that the answer names the
// provider, the adapter that reaches it, the account, and the model together,
// and that an endpoint whose provider cannot serve the asking role is left out
// with the reason named rather than chosen and then refused by somebody else.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// EndpointChoice is one endpoint the pool holds, with the account it
// authenticates under. The two travel together because an invocation needs
// both: the endpoint is what the record says served the turn, and the account
// endpoint is where on this machine the provider is actually reached.
type EndpointChoice struct {
	Endpoint backend.Endpoint
	Account  AccountEndpoint
}

// EndpointFor is the endpoint one configured agent's invocation is made on when
// the account is already settled — a run that reserved one, or an agent assigned
// to one. It refuses an agent this configuration does not name, a provider this
// project does not name, an account that holds another provider's
// authentication, and an endpoint the agent's role may not be served on, each
// with the reason named.
//
// The role check is here as well as at configuration load deliberately. Loading
// validates the configuration as it stands; this answers for the endpoint an
// invocation is actually about to be made on, which is the same question asked
// where it can still be refused. It is the same question in the strict sense —
// Registry.Serves and configuration validation read one derivation — so nothing
// the loader accepted is refused here. Whether this build ships an adapter for
// the provider is a different question, refused where a run is dispatched.
func (c Config) EndpointFor(providers *backend.Registry, stateRoot, agentName, alias string) (EndpointChoice, error) {
	agent, named := c.Agents[strings.TrimSpace(agentName)]
	if !named {
		return EndpointChoice{}, fmt.Errorf("agent %q is not one this configuration names", agentName)
	}
	account, err := c.Endpoint(stateRoot, alias)
	if err != nil {
		return EndpointChoice{}, err
	}
	if err := c.accountServes(providers, account.Alias, agent.Backend); err != nil {
		return EndpointChoice{}, fmt.Errorf("agent %q cannot be served: %w", agentName, err)
	}
	endpoint, err := providers.Endpoint(agent.Backend, account.Alias, agent.Model)
	if err != nil {
		return EndpointChoice{}, fmt.Errorf("resolve the endpoint agent %q runs on: %w", agentName, err)
	}
	if err := providers.ServesAt(agent.Backend, agent.Role, agent.Posture()); err != nil {
		return EndpointChoice{}, fmt.Errorf("agent %q cannot be served: %w", agentName, err)
	}
	return EndpointChoice{Endpoint: endpoint, Account: account}, nil
}

// AgentEndpoint is the endpoint one configured agent's own invocations are made
// on. It is the endpoint form of AgentAccountAlias and inherits its answer,
// including why it is a stable choice rather than a rotating one: a conversation
// and an exchange round last longer than a run, and an agent moved between
// accounts each turn would have no provider session left to resume.
func (c Config) AgentEndpoint(providers *backend.Registry, stateRoot, agentName string) (EndpointChoice, error) {
	return c.EndpointFor(providers, stateRoot, agentName, c.agentAccountAlias(providers, agentName))
}

// AgentFailoverEndpoint is the endpoint one agent's turn may be served on while
// the endpoint it runs on has no capacity: the alternate model, on the provider
// and account that agent's own failover block names, each defaulting to the ones
// it already runs on.
//
// It reports false for every agent that has not enabled failover, which is every
// agent until one says otherwise, and refuses rather than resolving where the
// alternate names a provider this project does not name, one that may not serve
// this agent's role, or an account that could not sign it in. Those are the same
// three refusals configuration validation already makes, asked again here for the
// reason EndpointFor asks its own again: this answers for the endpoint a
// substitution is actually about to be made on.
func (c Config) AgentFailoverEndpoint(providers *backend.Registry, stateRoot, agentName string) (EndpointChoice, bool, error) {
	name := strings.TrimSpace(agentName)
	agent, named := c.Agents[name]
	if !named {
		return EndpointChoice{}, false, fmt.Errorf("agent %q is not one this configuration names", agentName)
	}
	alternate := agent.Failover.Alternate()
	if alternate == "" {
		return EndpointChoice{}, false, nil
	}
	provider := agent.Failover.AlternateProvider(agent.Backend)
	alias := agent.Failover.AlternateAccount(c.agentAccountAlias(providers, name))
	account, err := c.Endpoint(stateRoot, alias)
	if err != nil {
		return EndpointChoice{}, false, fmt.Errorf("resolve the endpoint agent %q fails over to: %w", agentName, err)
	}
	if err := c.accountServes(providers, account.Alias, provider); err != nil {
		return EndpointChoice{}, false, fmt.Errorf("agent %q cannot fail over to provider %q: %w", agentName, provider, err)
	}
	endpoint, err := providers.Endpoint(provider, account.Alias, alternate)
	if err != nil {
		return EndpointChoice{}, false, fmt.Errorf("resolve the endpoint agent %q fails over to: %w", agentName, err)
	}
	// The role is asked of the alternate exactly as it is asked of the agent's own
	// provider, because a fallback that reached a weaker posture would be the one
	// thing a fallback may never do. EligibleFor rather than Serves: this is a
	// concrete endpoint an invocation is about to be made on, so whether anything
	// in this build could launch it is part of the answer.
	if err := providers.EligibleFor(endpoint, agent.Role); err != nil {
		return EndpointChoice{}, false, fmt.Errorf("agent %q cannot fail over: %w", agentName, err)
	}
	return EndpointChoice{Endpoint: endpoint, Account: account}, true, nil
}

// AgentAccountEndpoint is where one agent's own invocations authenticate: the
// account that agent is assigned to, or — where it is assigned to none — the
// first account in the pool that can sign its provider in.
//
// It answers for the callers that need the account without the rest of the
// endpoint: a conversation and a branch review, each of which belongs to its
// agent rather than to a run. They ask this rather than AgentAccountAlias
// directly, because an alias chosen without regard to the provider is how a
// conversation on one provider comes to be pointed at another provider's home.
func (c Config) AgentAccountEndpoint(stateRoot, agentName string) (AccountEndpoint, error) {
	providers, err := c.ProviderRegistry()
	if err != nil {
		// A configuration whose declared providers will not build is refused where
		// it is loaded. The alias is still answerable without them, so this answers
		// rather than becoming a second place that decides a configuration is
		// unusable.
		return c.Endpoint(stateRoot, c.AgentAccountAlias(agentName))
	}
	return c.Endpoint(stateRoot, c.agentAccountAlias(providers, agentName))
}

// agentAccountAlias is AgentAccountAlias with the pool's providers in hand: an
// agent that named no account is served by the first active account that can
// sign its provider in, rather than by the first active account full stop. That
// is the same stable choice — the pool's own order, read from the top — narrowed
// to the accounts the agent could actually authenticate as.
//
// An agent that named an account is answered with that account whether or not it
// holds the right provider, because an operator's statement is not something to
// route around: what it earns is the refusal naming the mismatch. A pool with
// nothing that serves falls through to the answer AgentAccountAlias gives, for
// the same reason — a refusal that names a real account is worth more than one
// that names none.
func (c Config) agentAccountAlias(providers *backend.Registry, agentName string) string {
	name := strings.TrimSpace(agentName)
	if alias := strings.TrimSpace(c.Agents[name].Account); alias != "" {
		return alias
	}
	if alias := c.AccountAlias(); alias != "" {
		return alias
	}
	for _, alias := range c.ActiveAccountAliases() {
		if c.accountServes(providers, alias, c.Agents[name].Backend) == nil {
			return alias
		}
	}
	return c.AgentAccountAlias(name)
}

// ChooseEndpoint picks the endpoint the next invocation of one agent is served
// by: the pool rotated past the endpoint last served, the weekly budgets
// honoured, an account holding another provider's authentication left out, and
// an endpoint the agent's role may not be served on left out.
//
// A pool that holds nothing this agent's provider can sign in to refuses here,
// which for a run is before the work item is claimed. That is the whole of what
// the refusal is worth: the alternative is a run that claimed an item, cut a
// worktree, and then died unauthenticated because it was handed another
// provider's home.
//
// The cursor is an endpoint rather than an account, which is what keying the
// pool on endpoints buys: a cursor left by an invocation on some other endpoint
// family — another provider, another model — does not rotate this one, so two
// agents on different endpoints do not read each other's position as their own.
// An endpoint the pool no longer holds leaves the order as it stands, which
// starts the rotation from the top rather than from nowhere.
//
// spentUSD is what each alias has cost over the window the budgets are stated
// in, exactly as ChooseAccount reads it: a budget is the operator's limit on an
// account, so it stays keyed on the account whichever endpoint is asking.
func (c Config) ChooseEndpoint(providers *backend.Registry, stateRoot, agentName string, lastServed backend.Endpoint, spentUSD map[string]float64) (EndpointChoice, error) {
	agent, named := c.Agents[strings.TrimSpace(agentName)]
	if !named {
		return EndpointChoice{}, fmt.Errorf("agent %q is not one this configuration names", agentName)
	}
	// Whether this agent's role may be served on this provider at all does not
	// vary by account, so it is asked once and refused before any rotation: a pool
	// that passed over every endpoint in turn would report a budget where the
	// answer is a posture.
	if err := providers.ServesAt(agent.Backend, agent.Role, agent.Posture()); err != nil {
		return EndpointChoice{}, fmt.Errorf("agent %q cannot be served: %w", agentName, err)
	}

	cursor := ""
	if lastServed.Provider == agent.Backend && strings.TrimSpace(lastServed.Model) == strings.TrimSpace(agent.Model) {
		cursor = lastServed.AccountAlias
	}
	// An account holding another provider's authentication is left out of the
	// order rather than chosen and then refused, because a mixed pool is the
	// shape this is for: a project with a Claude Code account and a Codex account
	// serves each agent from the one its provider signs in to. What is left is
	// the pool as this agent can actually be served from, and the budgets are read
	// over that — an account this agent could never use has no budget to report.
	order := c.rotatedAliases(cursor)
	eligible := make([]string, 0, len(order))
	for _, alias := range order {
		if err := c.accountServes(providers, alias, agent.Backend); err != nil {
			continue
		}
		eligible = append(eligible, alias)
	}
	if len(eligible) == 0 {
		return EndpointChoice{}, c.noAccountForProvider(order, agentName, agent.Backend)
	}
	for _, alias := range eligible {
		if !c.withinBudget(alias, spentUSD) {
			continue
		}
		return c.EndpointFor(providers, stateRoot, agentName, alias)
	}
	return EndpointChoice{}, c.noAccountLeft(eligible, spentUSD)
}

// accountServes reports whether one account can authenticate an invocation on a
// provider, and says why not where it cannot.
//
// What has to agree is the adapter rather than the provider's name. A provider
// home is a directory one adapter reads through one variable, so two providers
// on one adapter — Claude Code and a fork or proxy of it a project declared —
// authenticate in the same shape of home, and refusing that pairing would refuse
// a configuration that works. Two providers on different adapters read different
// variables and different files, which is exactly the invocation that dies
// unauthenticated.
//
// An account that authenticates where the machine does names no provider and
// serves every one of them: each adapter reads its own home there, so there is
// nothing to disagree about.
func (c Config) accountServes(providers *backend.Registry, alias string, asking domain.Backend) error {
	held := c.AccountProvider(alias)
	if held == "" || held == asking {
		return nil
	}
	account, accountKnown := providers.Lookup(held)
	agent, agentKnown := providers.Lookup(asking)
	if accountKnown && agentKnown && account.Adapter != "" && account.Adapter == agent.Adapter {
		return nil
	}
	return fmt.Errorf("account %q holds provider %q's authentication, and provider %q reads a provider home of its own; an invocation made there would authenticate as nobody",
		alias, held, asking)
}

// noAccountForProvider says why no account in the pool could serve an agent's
// provider, naming what each account holds. It is a different fact from a pool
// that has spent its budgets and reads as a different sentence: nothing here is
// exhausted, and no amount of waiting makes one of these accounts able to sign
// this agent in.
func (c Config) noAccountForProvider(order []string, agentName string, asking domain.Backend) error {
	// Every alias here failed accountServes, so every one of them names a provider
	// of its own; an account with no provider serves whoever asks and never
	// reaches this.
	held := make([]string, 0, len(order))
	for _, alias := range order {
		held = append(held, fmt.Sprintf("%s holds %q", alias, c.AccountProvider(alias)))
	}
	if len(held) == 0 {
		return fmt.Errorf("no provider account is configured to run agent %q on provider %q", agentName, asking)
	}
	return fmt.Errorf("no configured account holds provider %q's authentication, which agent %q runs on: %s",
		asking, agentName, strings.Join(held, "; "))
}

// accountProviderProblems reports an account naming a provider this project does
// not name, and an agent no account can sign in.
//
// The second half is asked here rather than left to the pool, so that the pool
// goes on refusing nothing the loader accepted: a project whose every account
// holds one provider's authentication and whose developer runs on another is a
// project no run can ever be served for, and hearing that when the file is read
// costs an edit, while hearing it at the moment a run is started costs the run.
// The pool asks the same question again anyway, because a mapping can be edited
// under a harness that is already running.
func (c Config) accountProviderProblems(providers *backend.Registry) []string {
	var problems []string
	for _, alias := range c.AccountAliases() {
		provider := domain.Backend(strings.TrimSpace(string(c.Accounts[alias].Provider)))
		if provider == "" {
			continue
		}
		if _, known := providers.Lookup(provider); !known {
			problems = append(problems, fmt.Sprintf("accounts.%s.provider is %q, which is not a provider this project names", alias, provider))
		}
	}
	for _, name := range sortedNames(c.Agents) {
		agent := c.Agents[name]
		// A backend this project does not name is reported where the agent is
		// validated, and asking which account could sign in a provider nobody
		// declared would be the same complaint a second time.
		if _, known := providers.Lookup(agent.Backend); !known {
			continue
		}
		// The account an agent names serves its own invocations — a conversation, an
		// exchange round, a branch review — and an alias the mapping does not
		// declare is already reported by accountProblems.
		if alias := strings.TrimSpace(agent.Account); alias != "" {
			if _, declared := c.Accounts[alias]; declared {
				if err := c.accountServes(providers, alias, agent.Backend); err != nil {
					problems = append(problems, fmt.Sprintf("agent %q cannot be served: %v", name, err))
				}
			}
		}
		// And the pool serves the runs, whichever account the agent named.
		if !c.anyAccountServes(providers, agent.Backend) {
			problems = append(problems, fmt.Sprintf("agent %q runs on provider %q and no configured account holds that provider's authentication; an account that authenticates there says so with accounts.<alias>.provider",
				name, agent.Backend))
		}
	}
	return problems
}

// anyAccountServes reports a pool that holds somewhere an agent on this provider
// could be signed in.
func (c Config) anyAccountServes(providers *backend.Registry, asking domain.Backend) bool {
	aliases := c.AccountAliases()
	if len(aliases) == 0 {
		// A project that declares no account still has one, and it authenticates
		// where the machine does — which is every provider's own home.
		return true
	}
	for _, alias := range aliases {
		if c.accountServes(providers, alias, asking) == nil {
			return true
		}
	}
	return false
}
