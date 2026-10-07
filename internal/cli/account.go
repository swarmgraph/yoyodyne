package cli

// Choosing which configured provider account serves the next run.
//
// The choosing lives here rather than in the configuration or in the pipeline
// because it needs both halves and neither package holds them: the configuration
// says which accounts exist, which half of the pool each is in, and what the
// operator is willing to spend on it, and the run records say which account was
// spent last and what each has cost since. This is the join, and it is the only
// place the two meet.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// accountLoginCommand is what an operator runs to sign one configured account
// in, for the provider whose home it is. It is the diagnosis's own command
// rather than a second spelling of it: `yoyo doctor` reports an account that is
// not authenticated and a conversation refuses to open on one, and an operator
// who met that condition from either direction has to be handed the same thing
// to run.
//
// The provider is the account's own where it names one, and the agent's
// otherwise — an account that names none authenticates where the machine does,
// which is the home whichever provider is asking reads.
func accountLoginCommand(cfg config.Config, named domain.Backend, account config.AccountEndpoint) string {
	if held := cfg.AccountProvider(account.Alias); held != "" {
		named = held
	}
	descriptor, _ := providerDescriptor(cfg, named)
	return doctor.AccountLoginCommand(descriptor.Adapter, account.Directory, descriptor.Binary)
}

// weeklyBudgetWindow is the seven days a weekly budget is measured over. It is a
// rolling window rather than a calendar week deliberately: a calendar week has a
// boundary an operator would have to know about to understand why a run was
// refused on Sunday evening and served on Monday morning, and the provider's own
// limits roll rather than reset on a day.
const weeklyBudgetWindow = 7 * 24 * time.Hour

// accountPool picks the account the next run is served by, from the
// configuration's pool and the evidence the run records already hold.
type accountPool struct {
	config    config.Config
	stateRoot string
	// runs is the product's own run records, which is the scope a weekly budget
	// is measured over: the store is built per product id, so what an account has
	// spent means what this product spent on it. Two products on one machine
	// sharing a subscription therefore bound it separately, and the operator's
	// configuration is where that is stated — see the weekly_budget_usd note in
	// docs/configuration.md. A budget read across products would need a ledger
	// none of them owns, which is the thing this pool exists without.
	runs *runstate.Store
	// now is when the budget window is measured back from. It is a field because
	// a budget that can only be exercised by waiting a week is one nothing tests.
	now func() time.Time
}

// ChooseAccount rotates the active half of the pool and honours the weekly
// budgets, falling back to the reserved half when the active one has nothing
// left to spend.
//
// What each account has spent is only read when a budget was actually stated. A
// project that budgeted nothing — which is every project until one says
// otherwise — would otherwise price a week of runs on every start to arrive at
// an answer that could exclude nobody, and would acquire a new way for a run to
// fail: an event log that cannot be read.
//
// The cursor is read here and written when the caller reserves the run's
// record, and the two are one step: the caller holds the rotation lease across
// both, so a start that has chosen has already moved the cursor the next one
// reads and simultaneous starts are served by different accounts rather than
// all by the same one. Nothing about that is this function's business — it
// reads the cursor as it stands and answers — but it is why answering the same
// question twice before either answer was recorded cannot happen.
//
// The choice still happens before anything is claimed, so a pool with nothing
// left refuses without taking a work item.
//
// The pool it rotates is a pool of endpoints rather than of accounts alone. The
// endpoints are the developer's, because the developer's invocations are what a
// run exists to make and the account chosen here is the run's: what the pool
// therefore skips is an endpoint the developer role may not be served on, named
// as such, rather than an account that would have been chosen and refused
// somewhere further in.
func (p accountPool) ChooseAccount() (config.AccountEndpoint, error) {
	lastServed, err := p.runs.LastAccountAlias()
	if err != nil {
		return config.AccountEndpoint{}, fmt.Errorf("read which account the last run was served by: %w", err)
	}
	var spent map[string]float64
	if p.config.HasAccountBudgets() {
		spent, err = p.runs.SpentByAccountSince(p.clock().Add(-weeklyBudgetWindow))
		if err != nil {
			return config.AccountEndpoint{}, fmt.Errorf("read what each account has spent this week: %w", err)
		}
	}
	developer := agentNameForRole(p.config, domain.RoleDeveloper)
	if developer == "" {
		// A project with no developer agent has no run to serve, so there is no
		// endpoint to choose one for. Configuration validation refuses such a
		// project; answering with the account rotation alone keeps this the join it
		// is rather than a second place that decides a configuration is unusable.
		return p.config.ChooseAccount(p.stateRoot, lastServed, spent)
	}
	providers, err := p.config.ProviderRegistry()
	if err != nil {
		return config.AccountEndpoint{}, fmt.Errorf("read which providers this project may name: %w", err)
	}
	// A run record keeps the alias it was served by and not yet the whole endpoint,
	// so the cursor is that alias read into the endpoint family the pool is
	// choosing over. That is the rotation the pool has always made; the day a run
	// records its endpoint, the cursor is that record rather than this assembly.
	agent := agentForRole(p.config, domain.RoleDeveloper)
	cursor := backend.Endpoint{Provider: agent.Backend, Model: agent.Model, AccountAlias: lastServed}
	choice, err := p.config.ChooseEndpoint(providers, p.stateRoot, developer, cursor, spent)
	if err != nil {
		return config.AccountEndpoint{}, err
	}
	return choice.Account, nil
}

// clock reads the field, and falls back rather than panicking on a pool nothing
// gave one to. Every construction supplies it; the fallback is here because the
// alternative to a wrong window is a nil dereference in the middle of starting a
// run, and of those two a correct window is plainly better.
func (p accountPool) clock() time.Time {
	if p.now == nil {
		return time.Now().UTC()
	}
	return p.now()
}
