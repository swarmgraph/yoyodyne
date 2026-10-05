package backend

// The execution endpoint: the unit work is routed over, and the identity a
// record says a turn was served by.
//
// A provider alone does not say what served an invocation, and neither does an
// account or a model. The same provider reached under two accounts is two
// different subscriptions with two different limits; the same account asking two
// models is two different capacity windows; and the same provider read by a
// different compiled adapter is a different piece of harness code deciding what
// the provider said. What identifies an invocation is all four together, so the
// four together are one value rather than four fields each caller assembles for
// itself.
//
// The adapter version is the one of the four that is not obvious. A provider a
// project declared for itself is reached by a compiled adapter this build ships
// -- the declaration names which -- so "provider" and "what read the stream" are
// separate facts, and a record carrying only the first cannot say which harness
// code classified the refusal it recorded. It is the adapter's version rather
// than the provider CLI's: the CLI's version is the provider's own business and
// is already reported as availability, and what a record has to be able to tell
// apart is two harness builds reading one provider differently.
//
// Nothing here decides anything about capacity, cost, or which endpoint a turn
// should go to. It says what an endpoint is, whether a role may be served on
// one, and -- for a substitution -- whether moving a role from one endpoint to
// another would put it somewhere its tool posture cannot be held.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ClaudeCodeAdapterVersion is the version of the compiled Claude Code adapter,
// which is what a record naming this endpoint says read the provider's stream.
//
// It is bumped when what that adapter sends or how it reads a stream changes in
// a way a record has to be able to tell apart -- a different session mode for a
// posture, a different terminal, a different reading of a refusal -- and not for
// a change that leaves both of those alone. Every provider a project declares
// runs on this adapter, so a declared provider's endpoints carry this version
// too: the declaration supplies the dialect, and this is the code that reads the
// stream with it.
const ClaudeCodeAdapterVersion = "claude-code/1"

// CodexAdapterVersion is the version of the compiled Codex adapter, and is what
// a record naming a Codex endpoint says read the provider's stream. It is bumped
// on the same terms as the one above: a change to what the adapter sends or to
// how it reads a stream, and not a change that leaves both alone.
//
// "codex/1" was the first Codex adapter this build carried. "codex/2" reads the
// newer vocabulary's reply, usage, and ending — an agent_message item, and
// turn.completed or turn.failed — where codex/1 read none of them and failed
// every such stream as unreadable, so a record says which of the two read it.
// "codex/3" adds native read-only invocations for reviewers and management roles.
// "codex/4" admits the developer's confined cache and scratch on launch and resume.
// "codex/5" passes effort explicitly and records provider-reported effort separately.
// "codex/6" omits the effort override when the agent sets none and records whether
// effort comes from the agent or from the Codex configuration.
const CodexAdapterVersion = "codex/6"

// Endpoint is one execution endpoint: which provider, read by which compiled
// adapter, under which account, asking which model.
//
// It is a value rather than a handle. An endpoint identifies an invocation for
// the record and keys the pool an invocation is chosen from; reaching the
// provider is the adapter's, and nothing here holds a process, a session, or a
// credential.
type Endpoint struct {
	// Provider is the backend the invocation names -- a built-in this build
	// ships, or one the project declared.
	Provider domain.Backend `json:"provider"`
	// AdapterVersion is the compiled adapter that reaches the provider, and is
	// empty for a provider this build ships no adapter for. An empty one is not a
	// missing field: it is an endpoint nothing here can launch, which is what
	// Runnable reports and what a refusal has to be able to say.
	AdapterVersion string `json:"adapter_version,omitempty"`
	// AccountAlias is the provider account the invocation is made under, by the
	// name every record says it by.
	AccountAlias string `json:"account_alias"`
	// Model is the selector the invocation asks for. It is what was asked rather
	// than what the provider resolved it to: the resolved identifier is evidence
	// the invocation reports back, and it belongs beside the result rather than in
	// the endpoint's identity, because a floating alias asked twice is one
	// endpoint and not two.
	Model string `json:"model"`
}

// Runnable reports an endpoint something in this build can actually launch. An
// endpoint with no adapter is expressed in the model and refused at routing,
// which is the honest shape for a provider the vocabulary names and this build
// cannot reach.
func (e Endpoint) Runnable() bool { return strings.TrimSpace(e.AdapterVersion) != "" }

// Key is the endpoint as a pool keys on it. Two invocations sharing a key are on
// one endpoint, and the fields are joined with a separator none of them can
// contain -- each is either a validated identifier or a model selector -- so two
// different endpoints cannot spell one key between them.
func (e Endpoint) Key() string {
	return strings.Join([]string{string(e.Provider), e.AdapterVersion, e.AccountAlias, e.Model}, "|")
}

// Same reports two endpoints that are the same endpoint.
func (e Endpoint) Same(other Endpoint) bool { return e.Key() == other.Key() }

// String is the endpoint as a refusal names it, in the order somebody reads it:
// what is being asked, on whose account, for which model, by which adapter.
func (e Endpoint) String() string {
	adapter := "no adapter in this build"
	if e.Runnable() {
		adapter = "adapter " + e.AdapterVersion
	}
	return fmt.Sprintf("%s on account %q asking model %q (%s)", e.Provider, e.AccountAlias, e.Model, adapter)
}

// Validate reports everything wrong with an endpoint at once. What it holds is
// that the three the harness always knows are there and shaped like themselves;
// the adapter version is not among them, because an endpoint on a provider this
// build cannot launch is a real endpoint that is refused at routing rather than
// a malformed value.
func (e Endpoint) Validate() error {
	var problems []string
	if err := domain.ValidateIdentifier("provider", string(e.Provider)); err != nil {
		problems = append(problems, err.Error())
	}
	if err := domain.ValidateIdentifier("account alias", e.AccountAlias); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(e.Model) == "" {
		problems = append(problems, "model is required; an endpoint that names no model is one no invocation could ask for")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid endpoint: %s", strings.Join(problems, "; "))
	}
	return nil
}

// RoleRefusal says why this provider may not serve a role, and is empty where it
// may. It is the one place role eligibility is decided, derived from what the
// provider declared rather than from its name: the roles it serves, and whether
// it can be held to the tool posture that role requires.
//
// The two are separate answers on purpose. A provider that lists a role it
// cannot hold the posture for is refused for the posture, because "does not
// support the role" would send an operator to the roles list to fix something
// that is not wrong there -- which is the discriminator the reviewer's no-tools
// posture and the developer's worktree-write actually are.
func (d Descriptor) RoleRefusal(role domain.AgentRole) string {
	posture := PostureFor(role)
	switch {
	case !d.SupportsRole(role):
		return fmt.Sprintf("backend %q does not support role %q", d.ID, role)
	case posture == "":
		return fmt.Sprintf("role %q has no tool access decided for it, so no backend can be held to any", role)
	case !d.SupportsPosture(posture):
		return fmt.Sprintf("backend %q cannot hold the %q tool access that role %q requires", d.ID, posture, role)
	}
	return ""
}

// AdapterVersionFor is the adapter version a built-in provider's endpoints
// carry, and empty for anything else. It exists for the record written about an
// invocation that died before its adapter could say anything: what the harness
// still knows is the provider it was configured for, and for a built-in that
// names the adapter version too.
//
// A provider a project declared answers empty here, because which adapter runs
// it is in the project's declaration rather than in this build. That record
// carries the provider and not the adapter version, which is a gap stated rather
// than filled with a guess.
func AdapterVersionFor(id domain.Backend) string {
	descriptor, known := BuiltInDescriptor(id)
	if !known {
		return ""
	}
	return descriptor.AdapterVersion
}

// Endpoint is the endpoint an invocation on one of this project's providers is
// made on. A provider this project does not name is refused here rather than
// resolved: an endpoint naming one would be an identity nothing could route or
// read back.
func (r *Registry) Endpoint(provider domain.Backend, accountAlias, model string) (Endpoint, error) {
	descriptor, known := r.Lookup(provider)
	if !known {
		return Endpoint{}, fmt.Errorf("provider %q is not one this project names; it names %s",
			provider, describeBackends(r))
	}
	endpoint := Endpoint{
		Provider:       provider,
		AdapterVersion: descriptor.AdapterVersion,
		AccountAlias:   strings.TrimSpace(accountAlias),
		Model:          strings.TrimSpace(model),
	}
	if err := endpoint.Validate(); err != nil {
		return Endpoint{}, err
	}
	return endpoint, nil
}

// Serves reports whether a provider may serve a role, and says why not where it
// may not. What it decides is the provider's own declaration — the roles it
// serves and the tool postures it can be held to — and nothing else, which is
// deliberately the same question configuration validation asks and the same
// derivation it reads. A configuration the loader accepted is therefore never
// refused here.
//
// Whether this build ships an adapter that could launch the provider is a
// separate question and is not asked here. It is refused where a run is
// dispatched, before anything is claimed, and asking it again in the pool would
// turn a configuration this loader accepts into a failure at work-claim time.
// EligibleFor is where it is asked of a concrete endpoint.
//
// It answers before an account or a model is chosen, because none of what it
// decides varies by either. A pool that asked this of each endpoint in turn
// would report the last account it tried where the answer is a posture.
func (r *Registry) Serves(provider domain.Backend, role domain.AgentRole) error {
	if !role.Valid() {
		return fmt.Errorf("role %q is not one of the harness's roles", role)
	}
	descriptor, known := r.Lookup(provider)
	if !known {
		return fmt.Errorf("provider %q is not one this project names; it names %s",
			provider, describeBackends(r))
	}
	if refusal := descriptor.RoleRefusal(role); refusal != "" {
		return errors.New(refusal)
	}
	return nil
}

// EligibleFor reports whether a role may be served on an endpoint: the
// provider's declaration as Serves reads it, and — because an endpoint is a
// place an invocation is actually made rather than a configuration being checked
// — that something in this build can launch it.
//
// That second half is what separates it from Serves. It is asked where a new
// endpoint is being chosen at run time rather than where a configuration is
// validated, which today is the substitution behind a closed capacity window:
// moving a turn onto an endpoint nothing here could launch would be answering a
// refusal with something that cannot run at all.
func (r *Registry) EligibleFor(endpoint Endpoint, role domain.AgentRole) error {
	if err := r.Serves(endpoint.Provider, role); err != nil {
		return fmt.Errorf("%s cannot serve role %q: %w", endpoint, role, err)
	}
	// The adapter and its version are set together — a descriptor that names one
	// names both — so an endpoint carrying no version is one this build ships no
	// adapter for, which is what Runnable reports off the endpoint alone.
	if !endpoint.Runnable() {
		return fmt.Errorf("%s cannot serve role %q: this build ships no adapter for provider %q",
			endpoint, role, endpoint.Provider)
	}
	return nil
}

// Substitutable reports whether one endpoint may stand in for another for a
// role, and says why not where it may not.
//
// A substitution is the one moment a role's endpoint changes without anybody
// having configured the change, so it is the moment the posture has to be
// checked again. Configuration validation holds every configured agent to the
// posture its role requires; nothing about that reaches an endpoint chosen at
// the moment a window closed. A substitution that moved a reviewer onto a
// provider whose sandbox cannot express no-tools would be exactly the weakening
// the fallback clause forbids, arrived at by a route no configuration ever
// stated.
//
// The endpoint being moved off is named in the refusal rather than checked: it
// is where the turn already is, and whether it was eligible is settled by its
// having been serving.
func (r *Registry) Substitutable(from, to Endpoint, role domain.AgentRole) error {
	if from.Same(to) {
		return nil
	}
	if err := r.EligibleFor(to, role); err != nil {
		return fmt.Errorf("role %q cannot be moved off %s: %w", role, from, err)
	}
	return nil
}

func describeBackends(r *Registry) string {
	named := make([]string, 0, len(r.Backends()))
	for _, id := range r.Backends() {
		named = append(named, fmt.Sprintf("%q", id))
	}
	if len(named) == 0 {
		return "none"
	}
	return strings.Join(named, ", ")
}
