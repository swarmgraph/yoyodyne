package modelfailover

// Serving a role's turn from a permitted alternate while the model it is
// configured for has no capacity.
//
// The shape this exists for was met rather than imagined. On 2026-09-07 at 06:00
// the model the management roles run on closed its capacity window while the
// model the developers and reviewers run on still had one; every item held for a
// development manager decision stopped behind a role that could not take a turn,
// and what unstuck it was a person editing three agents onto the other model. A
// refused turn looks exactly like a role with nothing to say, so nothing in the
// harness was going to notice.
//
// What is here is the substitution and nothing else. Which agents may take it is
// the configuration's — one alternate, named in the agent's own block, off until
// the agent says otherwise — and this package is handed the answer rather than
// deciding it. So an agent that has not enabled failover runs through here
// byte-for-byte as it ran before: one invocation, with the model it named, and
// no record written that would not have been written anyway.
//
// Three things follow from a substitution being a fact about a model rather than
// about a conversation:
//
//   - It is recorded in the product's usage-limit log, where every other refusal
//     outside a run is already recorded, carrying the model that was refused and
//     the alternate that served. A record that named neither could not be read
//     back as a window, and a second log would be a second answer to the question
//     the first one already answers.
//   - The window is read back from that log, so the turn after a substitution
//     goes straight to the alternate instead of paying a refused invocation to
//     rediscover a window the harness already watched close. It is durable
//     because the process that met the refusal is rarely the process that takes
//     the next turn.
//   - Affinity is the named model's. The moment the provider's own reset time
//     passes, the next turn asks the named model again — so a substitution lasts
//     a window rather than becoming a quiet permanent move to another model.
//
// A second mechanism moves a turn off the model it named, and it is here rather
// than beside here. An agent may pin an exact model version, and a pin the
// provider has not got falls back to the family alias the agent already names —
// availability rather than capacity, within a family rather than between two.
// What makes it the same mechanism is what it leaves behind: a turn served by a
// model other than the one it asked for, which has to be recorded and said. One
// record answers "which model served this turn, and why" whichever of the two
// chose it, because a reader who had to consult two logs to answer that would
// get two answers to it.
//
// The two compose rather than sit beside each other, and the composition is the
// part worth stating: whichever selector a turn is asking for at the moment it is
// refused, that selector's refusal is answered. So the pinned version is asked
// through the capacity path rather than in front of it — an agent that pinned a
// version and permitted an alternate keeps both, and a pin the provider has but
// has no capacity for is a closed window like any other, answered by the
// alternate.
//
// Which of the two answers a refusal is decided by the refusal and not by an
// order fixed here:
//
//   - No capacity at the pinned version is failover's, and the alternate answers
//     it. Falling back to the family alias there would buy nothing, because the
//     alias floats over the same family and is therefore inside the same window.
//   - The provider not having the pinned version is the fallback's, and the
//     family alias answers it — the alias is the family's latest by definition,
//     so it is the version that exists. That attempt is then subject to failover
//     exactly as it would have been had nothing been pinned.
//
// Each hop is recorded as itself — refused for availability, refused for
// capacity — because a single entry collapsing two would name a model that
// refused a turn nobody asked it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Invoker is the whole of what failover needs of the thing it wraps: something
// that can make the invocation. It is narrower than backend.Backend
// deliberately — the harness puts its provider through the cost meter before an
// invocation reaches here, and a meter is not a backend — and the narrower
// interface is what lets the substitution sit outside the meter, where each
// attempt is priced against the model that attempt actually asked for.
type Invoker interface {
	Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error)
}

// Windows is the durable account of which models a provider has refused. It is
// satisfied by runstate.UsageLimitStore, which is where every refusal met
// outside a run is already written down.
type Windows interface {
	Record(exhaustion runstate.UsageLimitExhaustion) error
	List() ([]runstate.UsageLimitExhaustion, error)
}

// Eligibility says whether one endpoint may stand in for another for a role. It
// is satisfied by *backend.Registry, which derives the answer from what each
// provider declared about the roles it serves and the tool postures it can be
// held to.
//
// A substitution is the one moment a role's endpoint changes without anybody
// having configured the change, so it is the moment the posture has to be asked
// about again. Configuration validation held every configured agent to the
// posture its role requires; nothing about that reaches an endpoint arrived at
// because a window closed. This is where the two meet, and it never widens
// anything: an endpoint it permits is one the configuration would have
// permitted, and a refusal here leaves the turn exactly where it was.
type Eligibility interface {
	Substitutable(from, to backend.Endpoint, role domain.AgentRole) error
}

// Policy is what one agent's turn may be served by, and where the substitution
// is written down. Its zero value is failover off, which is what every caller
// that has not been configured for it passes.
type Policy struct {
	// Alternate is the permitted alternate model, from the agent's own
	// configuration. Empty is failover off and is the whole of the switch: this
	// package never reads a configuration and never chooses a model nobody named.
	Alternate string
	// AlternateEndpoint is where that alternate is served, and the zero value is
	// this turn's own endpoint with the model replaced — which is the substitution
	// as it was before an alternate could name a provider. Where it names another
	// provider the turn crosses providers, and the three fields below are what
	// makes that a turn rather than a request sent to the wrong place.
	AlternateEndpoint backend.Endpoint
	// AlternateProvider is the invoker that reaches the endpoint above, and is
	// required for a substitution that crosses providers: a different provider is
	// a different adapter, reached under a different account and priced against a
	// different subscription, so the caller hands the whole metered invoker rather
	// than this package trying to build one. Nil leaves the substitution on the
	// invoker the turn was already using, which is correct exactly while the
	// alternate does not leave the provider.
	AlternateProvider Invoker
	// AlternateAccountConfigDir is where the alternate's provider keeps its own
	// authentication on this machine, empty for the machine's own provider home.
	// It travels with the endpoint above for the reason the account alias does:
	// crossing providers is crossing logins, and an invocation made in the first
	// provider's home would authenticate as nobody.
	AlternateAccountConfigDir string
	// AlternateSessionID is the session the alternate endpoint already holds for
	// this turn's conversation, and empty where it holds none — which is the first
	// crossing, and every turn of an agent that has never crossed.
	//
	// A window lasts longer than one turn, so the second and third turns of an
	// outage go to the same alternate the first one did, and by then that provider
	// is holding a session of its own. Resuming it is what keeps a crossing costing
	// one reconstruction per outage rather than one per turn.
	//
	// It is stated by the caller rather than read out of the result, and it is what
	// this package sends: the session the refusing endpoint held is dropped before
	// the rebuild is asked for anything and this replaces it afterwards, so no
	// rebuild can put one provider's session in front of another whatever it
	// returns.
	AlternateSessionID string
	// AlternateDropsEffort says the provider a crossing lands on does not accept
	// the effort level the turn asks for, so the crossed invocation is made with
	// none. It is meaningful only for a crossing: an alternate on the turn's own
	// provider accepts whatever the turn's provider accepts, and keeps the level.
	// What was done instead is on the record twice over — Served.Effort says the
	// level the serving invocation asked for, and its cost line says the same.
	AlternateDropsEffort bool
	// Rebuild is how a turn that crosses providers gets its context. The provider
	// taking it has never seen this conversation and holds no session to resume,
	// so what it is handed has to be assembled from the durable record: the caller
	// is given the request the refused attempt was made with and returns the one
	// the alternate is asked, with the session identifier gone and whatever the
	// record holds in the prompt.
	//
	// It is required for a crossing and unused for everything else. A crossing
	// with no way to rebuild is refused rather than attempted, because a turn sent
	// to a second provider carrying the first one's session identifier is not a
	// continuation of anything — it is the conversation silently starting over,
	// which is the durable-state guarantee failing in the one place it was
	// supposed to hold.
	Rebuild func(backend.RunRequest) (backend.RunRequest, error)
	// Version is the exact model version the agent pinned, from that same
	// configuration. Empty is no pin, which is every agent until one names one,
	// and then the request's own selector is asked for and nothing here does
	// anything at all. Where it is named it is asked for instead, and the
	// request's selector — the family alias, which is the family's latest by
	// definition — is what answers when the provider has not got the version.
	Version string
	// Windows is where a substitution is recorded and where a window still open
	// against the named model is read from. A policy without one still fails over
	// — the turn is what matters — but pays a refused invocation each turn and
	// tells nobody, so the harness always wires it.
	Windows Windows
	// Endpoint is where this turn is being served: the provider, the adapter that
	// reaches it, the account, and the model the request names. Role is the
	// contract the turn is taken under. Together they are what a substitution is
	// checked against, so what a candidate endpoint has to hold is the posture
	// this role requires rather than whatever the endpoint it replaced happened to
	// hold.
	Endpoint backend.Endpoint
	Role     domain.AgentRole
	// Eligibility is asked before any substitution is made. A policy without one
	// substitutes unchecked, which is what every caller did before the check
	// existed; the harness wires it everywhere, so an unwired one is a test that
	// does not care rather than a path a running harness takes.
	Eligibility Eligibility
	// Now is the clock the reset times are compared against. Nil is time.Now.
	Now func() time.Time
	// UnknownResetPause is how long a refusal that named no usable reset time
	// stands before the configured model is asked again. It is the project's
	// `execution.usage_limit_unknown_reset_pause`, which is the same interval a
	// run probes an unknown-reset limit on, so the harness has one polling
	// discipline rather than two that could disagree about the same account.
	//
	// Without it every turn re-asks a model that is still exhausted, substitutes
	// again, and writes another substitution down — so the operator is told once
	// per turn for as long as the outage lasts, which is the channel-muting
	// outcome saying it once per window exists to avoid. Zero restores exactly
	// that, and is why the harness always passes the configured value.
	UnknownResetPause time.Duration
	// ProductID, Waiting, ConversationID and WorkItemID are what the recorded
	// substitution says about itself: whose product it happened on, what would
	// have stopped in words, and what it can be read back to. Waiting is required
	// by the record for the reason it is required of every refusal — an
	// exhaustion nobody can say what it stopped is not worth recording.
	ProductID      domain.ProductID
	Waiting        string
	ConversationID string
	WorkItemID     string
	// RecordFailure is handed whatever went wrong with the log: a substitution
	// that could not be written down, or a set of windows that could not be read.
	// A turn the alternate has already answered is not thrown away because the log
	// would not take the line, which is the same trade the cost meter makes, and a
	// window that could not be read costs one refused invocation rather than the
	// turn.
	//
	// A caller that leaves it nil is handed a failed write joined onto the
	// invocation's own error rather than losing it. A failed read has nowhere to
	// go in that case and is dropped, because the turn it belongs to succeeded;
	// the harness wires this everywhere for exactly that reason.
	RecordFailure func(error)
}

// Enabled reports a policy that may substitute anything, by either mechanism.
func (p Policy) Enabled() bool {
	return strings.TrimSpace(p.Alternate) != "" || strings.TrimSpace(p.Version) != ""
}

func (p Policy) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// Served is what actually answered, said by the harness rather than inferred
// from the configuration. The record keeps this rather than the configured
// selector, because "which model served this turn" is the question a
// substitution makes worth asking.
type Served struct {
	// Model is the selector this turn was actually asked for under.
	Model string
	// Refused is the model that was moved off, and empty where the model the turn
	// asked for served it. It is separate from Model because a record that says
	// only what served cannot say that anything was moved.
	Refused string
	// Why is which mechanism moved it, meaningful only where Refused names
	// something. Where both moved one turn — a pinned version the provider had not
	// got, falling back to an alias that then had no capacity — this is the last
	// hop, which is the one that decided Model. Each hop is recorded separately
	// in the log, which is where the whole of what happened is kept.
	Why runstate.SubstitutionReason
	// Endpoint is where the turn was actually served: the provider, the adapter
	// that reached it, the account it authenticated under, and the model it asked.
	// It is the whole identity rather than the model alone because a substitution
	// can now change any of the four, and a record that carried only the model
	// could not say a turn had crossed providers at all.
	//
	// It is the zero endpoint where the policy named none, which is a caller that
	// never had one to name. A caller reading this keeps whatever it already knew
	// rather than recording an endpoint nobody resolved.
	Endpoint backend.Endpoint
	// RefusedEndpoint is where the turn was refused, and the zero endpoint where
	// nothing was — it is the pair to Refused, on the same terms Endpoint is the
	// pair to Model.
	RefusedEndpoint backend.Endpoint
	// Effort is the effort level the serving invocation asked for: the request's
	// own on every turn but a crossing onto a provider that does not accept it,
	// where it is empty because none was passed.
	Effort string
}

// Substituted reports a turn served by a model other than the one it asked for.
func (s Served) Substituted() bool { return strings.TrimSpace(s.Refused) != "" }

// CrossedProviders reports a turn served by a provider other than the one it was
// refused by. It is what says a context was rebuilt rather than a session
// resumed, which is the difference a reader of the record most needs.
func (s Served) CrossedProviders() bool {
	refused := strings.TrimSpace(string(s.RefusedEndpoint.Provider))
	served := strings.TrimSpace(string(s.Endpoint.Provider))
	return refused != "" && served != "" && refused != served
}

// Serve makes one provider invocation, under the version the agent pinned where
// it named one the provider has, and served by the permitted alternate where the
// model that would take it has no capacity.
//
// The pinned version is asked through the capacity path rather than in front of
// it, so an agent that pinned a version and permitted an alternate keeps both:
// a pin with no capacity is answered by the alternate, exactly as the family
// alias would have been had nothing been pinned. Only the provider saying it has
// not got the version falls back to the alias, and that attempt is then subject
// to failover in its turn.
//
// Everything else passes through untouched. A turn that failed for any other
// reason is that failure, with the model that was asked, and nothing about it is
// retried here.
func Serve(ctx context.Context, provider Invoker, request backend.RunRequest, policy Policy) (backend.RunResult, Served, error) {
	family := strings.TrimSpace(request.Model)
	version := strings.TrimSpace(policy.Version)
	if version == "" || version == family {
		return serveWithCapacity(ctx, provider, request, policy, Served{})
	}

	// A version the log already says this provider has not got goes unasked for as
	// long as that stands, the way a closed window does: an invocation refused for
	// something the harness has already been told is a round trip that buys
	// nothing. Nothing is recorded here, because the entry that made this skip
	// possible has already said it.
	fellBack := Served{Model: family, Refused: version, Why: runstate.SubstitutedForAvailability}
	if missing, err := versionMissing(policy, version); err != nil {
		policy.report(err)
	} else if missing {
		return serveWithCapacity(ctx, provider, request, policy, fellBack)
	}

	request.Model = version
	result, served, err := serveWithCapacity(ctx, provider, request, policy, Served{})
	// Only a refusal met at the pinned attempt itself is the provider saying it has
	// not got that version, and only that is worth falling back from. One met after
	// the alternate had already taken the turn is about the alternate — a model
	// nobody pinned — and the family alias is no answer to it. A substitution
	// having happened is what tells the two apart, because the pinned attempt is
	// the only one made before there is one.
	if result.ModelUnavailable == nil || served.Substituted() || (err == nil && !result.IsError) {
		return result, served, err
	}

	// The refused attempt emitted events of its own before it was declined, so the
	// substituted one starts after them rather than numbering over them.
	advanceSequence(&request, result)
	request.Model = family
	substituted, servedByFamily, substitutedErr := serveWithCapacity(ctx, provider, request, policy, fellBack)
	// The fallback is written down only where the turn was served, for the reason a
	// capacity substitution is: a turn nothing could take is the failure the caller
	// already handles, and an entry claiming the work carried on would be the log
	// contradicting it.
	if refusal(substituted, substitutedErr) != nil || substituted.ModelUnavailable != nil {
		return substituted, servedByFamily, substitutedErr
	}
	// What is recorded is the hop the pin made — the version refused, and the alias
	// that stood in for it — rather than whatever eventually answered. Where
	// failover then moved the alias too, that hop recorded itself, and two true
	// entries are what let the next turn skip both invocations rather than one.
	if err := recordUnavailable(policy, version, family, result.ModelUnavailable.Detail); err != nil {
		if policy.RecordFailure == nil {
			return substituted, servedByFamily, errors.Join(substitutedErr, err)
		}
		policy.RecordFailure(err)
	}
	return substituted, servedByFamily, substitutedErr
}

// serveWithCapacity makes the invocation the caller built, serving it from the
// permitted alternate where the model it names will not take it.
//
// The model in the request is asked unless the log already says its window is
// open no longer, in which case the alternate is asked directly — an invocation
// refused for a window the harness watched close is a round trip that buys
// nothing. A refusal met there is failed over once, and only once: the alternate
// either serves the turn or the caller is handed the refusal it would have been
// handed anyway, because a third model to try after the second is routing rather
// than fallback.
//
// fellBack is what the caller already knows about how this turn got here — a
// pinned version the provider had not got, or nothing at all — and it is what is
// reported where nothing further moves the turn. It never carries a model to
// ask: request.Model is that, whichever way it was arrived at.
func serveWithCapacity(ctx context.Context, provider Invoker, request backend.RunRequest, policy Policy, fellBack Served) (backend.RunResult, Served, error) {
	named := strings.TrimSpace(request.Model)
	alternate := strings.TrimSpace(policy.Alternate)
	stood := fellBack
	stood.Model = request.Model
	stood.Endpoint = policy.servingEndpoint(request.Model)
	stood.RefusedEndpoint = backend.Endpoint{}
	stood.Effort = request.Effort
	// An alternate that is where the turn already is is not an alternate. It is
	// the whole endpoint that has to differ rather than the model alone: the same
	// selector asked of another provider is a different endpoint with a capacity
	// window of its own, which is exactly what a crossing is for.
	if alternate == "" || (alternate == named && !policy.crosses()) {
		result, err := provider.Run(ctx, request)
		return result, stood, err
	}
	// An alternate the role may not be served on is not an alternate, and neither
	// is one that crosses providers with no way to rebuild the context the second
	// provider has never seen. Both are asked once, before either path below can
	// take it, so a turn is never moved onto an endpoint whose sandbox cannot hold
	// this role's tool posture and never moved onto one that would silently start
	// the conversation over — and the refusal says which, rather than leaving the
	// turn to fail somewhere else.
	if err := policy.permitSubstitution(alternate); err != nil {
		policy.report(err)
		result, err := provider.Run(ctx, request)
		return result, stood, err
	}
	moved := Served{
		Model:           alternate,
		Refused:         named,
		Why:             runstate.SubstitutedForCapacity,
		Endpoint:        policy.alternateEndpoint(alternate),
		RefusedEndpoint: policy.servingEndpoint(named),
		Effort:          policy.alternateEffort(request.Effort),
	}

	// A window the provider said has not lifted is taken at its word for as long
	// as it stands, and for no longer: the comparison is against the clock at the
	// moment of the turn, so affinity returns to the named model on the first turn
	// after the reset time passes without anything having to notice it did.
	if closed, err := windowClosed(policy, named); err != nil {
		policy.report(err)
	} else if closed {
		result, err := policy.runAlternate(ctx, provider, request, alternate)
		if err != nil && errors.Is(err, errRebuildFailed) {
			// The context could not be assembled, so there is nothing to send the
			// second provider. The turn is put back on the endpoint it was already
			// on, which either serves it or is refused exactly as it would have been.
			policy.report(err)
			result, err := provider.Run(ctx, request)
			return result, stood, err
		}
		return result, moved, err
	}

	result, err := provider.Run(ctx, request)
	refused := refusal(result, err)
	if refused == nil {
		return result, stood, err
	}

	// The refused attempt emitted events of its own before it was declined, so the
	// substituted one starts after them rather than numbering over them. Both
	// attempts write to one event log, and a sequence used twice is a log whose
	// numbering no longer says what order anything happened in.
	advanceSequence(&request, result)
	substituted, substitutedErr := policy.runAlternate(ctx, provider, request, alternate)
	if substitutedErr != nil && errors.Is(substitutedErr, errRebuildFailed) {
		// Nothing was asked of the alternate, so the turn ends on the refusal it
		// already met rather than on the rebuild's failure, which is reported beside
		// it. Recording a substitution here would claim work carried on that did not.
		policy.report(substitutedErr)
		return result, stood, err
	}
	// The substitution is written down only where it served. A turn the alternate
	// could not take either is the refusal the caller already handles, and
	// recording it here would put the same stoppage in the log twice — once as a
	// substitution that did not happen, and once by whoever fails the turn.
	if refusal(substituted, substitutedErr) != nil {
		return substituted, moved, substitutedErr
	}
	if err := record(policy, named, alternate, *refused, moved); err != nil {
		if policy.RecordFailure == nil {
			return substituted, moved, errors.Join(substitutedErr, err)
		}
		policy.RecordFailure(err)
	}
	return substituted, moved, substitutedErr
}

// errRebuildFailed marks a crossing that never reached the second provider
// because the context could not be assembled from the durable record. It is a
// sentinel because it is not a provider failure and must not be reported as one:
// nothing was asked, nothing was charged, and what the caller owes the turn is
// the refusal it already had rather than this.
var errRebuildFailed = errors.New("the context for the substituted turn could not be rebuilt from the durable record")

// runAlternate makes the same turn on the alternate.
//
// Where the alternate is another model on the same provider, everything else
// about the request is the one the caller built — the same prompt, the same
// session, the same account — so what changes is the model and nothing else.
//
// Where it is another provider, three more things change and they change
// together. The invocation is made through that provider's own invoker, so it is
// reached by the right adapter and priced against the right subscription; it
// authenticates under that provider's account rather than the first one's; and it
// carries no session identifier, because the session it would name belongs to a
// provider that is not being asked. What replaces the session is the rebuilt
// context, which is the durable record doing the work a resumption cannot.
func (p Policy) runAlternate(ctx context.Context, provider Invoker, request backend.RunRequest, model string) (backend.RunResult, error) {
	request.Model = model
	request.Effort = p.alternateEffort(request.Effort)
	if !p.crosses() {
		return provider.Run(ctx, request)
	}
	request.AccountAlias = p.AlternateEndpoint.AccountAlias
	request.AccountConfigDir = p.AlternateAccountConfigDir
	// The refusing endpoint's session is dropped before the rebuild is asked for
	// anything, so nothing it returns can carry one provider's session to another.
	request.SessionID = ""
	rebuilt, err := p.Rebuild(request)
	if err != nil {
		return backend.RunResult{}, fmt.Errorf("%w: %v", errRebuildFailed, err)
	}
	// And what is sent is the alternate's own session where the caller says it holds
	// one, rather than whatever the rebuild put back. A session is the only thing
	// about the request this package decides for itself, because it is the one
	// thing that must never be the other provider's.
	rebuilt.SessionID = strings.TrimSpace(p.AlternateSessionID)
	rebuilt.Effort = p.alternateEffort(rebuilt.Effort)
	return p.AlternateProvider.Run(ctx, rebuilt)
}

// alternateEffort is the effort level the alternate is asked with: the turn's
// own, except on a crossing onto a provider that would not accept it.
func (p Policy) alternateEffort(effort string) string {
	if p.crosses() && p.AlternateDropsEffort {
		effort = ""
	}
	endpoint := p.alternateEndpoint(p.Alternate)
	if registry, ok := p.Eligibility.(*backend.Registry); ok {
		if descriptor, known := registry.Lookup(endpoint.Provider); known {
			return descriptor.InvocationEffort(endpoint.Model, effort)
		}
	}
	if descriptor, known := backend.BuiltInDescriptor(endpoint.Provider); known {
		return descriptor.InvocationEffort(endpoint.Model, effort)
	}
	return effort
}

// ServesElsewhere reports a turn this policy will move onto the alternate before
// it is attempted: the alternate is permitted, and the window on the model the
// request names is one the log already says is closed.
//
// It exists for a caller that has to prepare the turn differently for each
// endpoint — a crossing rebuilds its context from the durable record, and the
// endpoint the turn was already on resumes a session instead — so that the
// preparation and the routing cannot disagree about where the turn is going. A
// caller that asked the question for itself would be reading the same log through
// a second derivation, which is exactly the disagreement one turn cannot afford.
//
// It answers false for the refusal that has not happened yet, which is the honest
// answer: whether the endpoint will refuse this turn is not knowable until it is
// asked, and a caller that prepared for a crossing on the strength of a guess
// would rebuild a turn that was about to be served where it was.
func (p Policy) ServesElsewhere(model string) bool {
	alternate := strings.TrimSpace(p.Alternate)
	named := strings.TrimSpace(model)
	if alternate == "" || (alternate == named && !p.crosses()) {
		return false
	}
	if p.permitSubstitution(alternate) != nil {
		return false
	}
	closed, err := windowClosed(p, named)
	return err == nil && closed
}

// crosses reports a substitution that leaves the provider the turn is on.
func (p Policy) crosses() bool {
	alternate := strings.TrimSpace(string(p.AlternateEndpoint.Provider))
	return alternate != "" && alternate != strings.TrimSpace(string(p.Endpoint.Provider))
}

// servingEndpoint is this turn's own endpoint asking one model, and the zero
// endpoint for a policy that names none.
func (p Policy) servingEndpoint(model string) backend.Endpoint {
	if p.Endpoint.Provider == "" {
		return backend.Endpoint{}
	}
	endpoint := p.Endpoint
	endpoint.Model = strings.TrimSpace(model)
	return endpoint
}

// alternateEndpoint is where the substituted turn is served: the stated alternate
// endpoint where one was named, and this turn's own with the model replaced where
// none was — which is what a substitution that stays on the provider produces.
func (p Policy) alternateEndpoint(model string) backend.Endpoint {
	if p.AlternateEndpoint.Provider != "" {
		endpoint := p.AlternateEndpoint
		endpoint.Model = strings.TrimSpace(model)
		return endpoint
	}
	return p.servingEndpoint(model)
}

// advanceSequence moves the next attempt past the events the refused one already
// emitted, so two attempts at one turn write one log whose numbering still says
// what order things happened in.
func advanceSequence(request *backend.RunRequest, result backend.RunResult) {
	if result.LastEvent > request.LastSequence {
		request.LastSequence = result.LastEvent
	}
}

// windowClosed reports the named model still inside a window the last refusal
// of it describes: the provider's own reset time where it named a usable one,
// and the configured probe interval where it did not. A limit reported without a
// reset is a wait of unknown length rather than of no length, so the harness
// waits its interval and asks again — which is what makes a substitution said
// once per window rather than once per turn.
//
// The log is the product's rather than one account's, so under a pool a window
// met on one account is read as closed for an agent held on another. That errs
// the harmless way round — the agent is served by its permitted alternate for a
// window it might not have needed to skip — and it is never a stoppage, which is
// the failure this exists to prevent. Keying windows by account would need the
// account on the refusal record, and that is worth doing when a pooled project
// actually runs its management roles on separate subscriptions.
func windowClosed(policy Policy, model string) (bool, error) {
	if policy.Windows == nil || strings.TrimSpace(model) == "" {
		return false, nil
	}
	exhaustions, err := policy.Windows.List()
	if err != nil {
		return false, fmt.Errorf("read which models the provider has refused: %w", err)
	}
	at := policy.now()
	for _, exhaustion := range exhaustions {
		if strings.TrimSpace(exhaustion.Model) != strings.TrimSpace(model) {
			continue
		}
		// An availability substitution names the same field and means something
		// else: the provider has not got that selector, which is not a window and
		// must not be read as one. Skipping it here is what keeps one agent's pinned
		// version from reading as another agent's closed window.
		if exhaustion.Substituted() && exhaustion.Reason() == runstate.SubstitutedForAvailability {
			continue
		}
		if exhaustion.WindowClosed(at, policy.UnknownResetPause) {
			return true, nil
		}
	}
	return false, nil
}

// versionMissing reports a pinned version this provider was already found not to
// have, inside the interval the harness rechecks on. There is no reset time to
// read: a provider that has not got a model quotes no deadline for getting one,
// so what stands in is UnknownResetPause — the same interval an unknown-reset
// limit is probed on, so the harness has one polling discipline rather than two.
//
// Rechecking at all is the point. A version can arrive, and a version withdrawn
// in error can come back, and a pin that was skipped once and then forever would
// be a floating alias the operator thinks is a pin.
func versionMissing(policy Policy, version string) (bool, error) {
	if policy.Windows == nil || strings.TrimSpace(version) == "" {
		return false, nil
	}
	exhaustions, err := policy.Windows.List()
	if err != nil {
		return false, fmt.Errorf("read which models the provider has refused: %w", err)
	}
	at := policy.now()
	for _, exhaustion := range exhaustions {
		if !exhaustion.Substituted() || exhaustion.Reason() != runstate.SubstitutedForAvailability {
			continue
		}
		if strings.TrimSpace(exhaustion.Model) != strings.TrimSpace(version) {
			continue
		}
		if exhaustion.WindowClosed(at, policy.UnknownResetPause) {
			return true, nil
		}
	}
	return false, nil
}

// record writes the substitution down where every refusal met outside a run is
// written down. The providers travel with the models, because a turn that
// crossed providers and a turn that changed model on one are the same two fields
// otherwise and are not the same news: only the first one rebuilt its context.
func record(policy Policy, named, alternate string, refused backend.UsageLimit, moved Served) error {
	if policy.Windows == nil {
		return nil
	}
	exhaustion := runstate.UsageLimitExhaustion{
		SchemaVersion:  runstate.UsageLimitSchemaVersion,
		ProductID:      policy.ProductID,
		At:             policy.now(),
		Waiting:        policy.Waiting,
		Kind:           refused.Kind,
		ConversationID: policy.ConversationID,
		WorkItemID:     policy.WorkItemID,
		Model:          named,
		AccountAlias:   strings.TrimSpace(moved.RefusedEndpoint.AccountAlias),
		ServedBy:       alternate,
		Substitution:   runstate.SubstitutedForCapacity,
	}
	if moved.CrossedProviders() {
		exhaustion.Provider = moved.RefusedEndpoint.Provider
		exhaustion.ServedByProvider = moved.Endpoint.Provider
	}
	if !refused.ResetsAt.IsZero() {
		resetsAt := refused.ResetsAt.UTC()
		exhaustion.ResetsAt = &resetsAt
	}
	if err := policy.Windows.Record(exhaustion); err != nil {
		return fmt.Errorf("record the turn %s served while %s had no capacity: %w", alternate, named, err)
	}
	return nil
}

// recordUnavailable writes down a pinned version the provider has not got, in
// the same log and the same shape a capacity substitution is written in. It
// names no kind and no reset time, because neither exists: nothing about the
// account was exhausted and no condition was said to lift. The provider's own
// words about the model it would not serve are what the entry says was waiting
// on it, joined to the caller's sentence, so a reader has the evidence rather
// than only the category.
func recordUnavailable(policy Policy, version, family, detail string) error {
	if policy.Windows == nil {
		return nil
	}
	exhaustion := runstate.UsageLimitExhaustion{
		SchemaVersion:  runstate.UsageLimitSchemaVersion,
		ProductID:      policy.ProductID,
		At:             policy.now(),
		Waiting:        waitingWithDetail(policy.Waiting, detail),
		ConversationID: policy.ConversationID,
		WorkItemID:     policy.WorkItemID,
		Model:          version,
		ServedBy:       family,
		Substitution:   runstate.SubstitutedForAvailability,
	}
	if err := policy.Windows.Record(exhaustion); err != nil {
		return fmt.Errorf("record the turn %s served because the provider has not got %s: %w", family, version, err)
	}
	return nil
}

// waitingWithDetail joins the provider's own account of the model it would not
// serve onto the caller's sentence, bounded so the pair stays a phrase somebody
// reads rather than a record they study. A provider that said nothing leaves the
// sentence exactly as the caller wrote it.
func waitingWithDetail(waiting, detail string) string {
	trimmed := strings.TrimSpace(detail)
	if trimmed == "" {
		return waiting
	}
	joined := waiting + " (" + trimmed + ")"
	if len(joined) > runstate.MaxUsageLimitWaitingBytes {
		return waiting
	}
	return joined
}

// permitSubstitution reports whether this turn may be moved onto the endpoint
// that would serve it, and says why not where it may not.
//
// The candidate is the alternate endpoint the policy names, or — where it names
// none — this turn's own with the model replaced, which is what a substitution
// that stays on the provider produces. The check is written over the whole
// endpoint rather than over the model alone precisely so that it holds when the
// alternate names a provider: one whose sandbox cannot express this role's
// posture is refused by the same line that refuses a model.
//
// A crossing carries a second condition of its own. The second provider holds no
// session for this turn, so a crossing with no invoker to reach it or no way to
// rebuild the context is refused here rather than attempted — an invocation that
// arrived with the first provider's session identifier and none of its history
// would be the conversation quietly starting over.
//
// A policy that names no endpoint or no eligibility has nothing to ask about the
// posture and permits the substitution, which is what every caller did before the
// check existed. The crossing condition still holds there, because it is about
// what this package was handed rather than about what a registry says.
func (p Policy) permitSubstitution(model string) error {
	if p.crosses() {
		switch {
		case p.AlternateProvider == nil:
			return fmt.Errorf("role %q cannot be moved off %s onto %s: no invoker was supplied for the alternate provider",
				p.Role, p.Endpoint, p.alternateEndpoint(model))
		case p.Rebuild == nil:
			return fmt.Errorf("role %q cannot be moved off %s onto %s: crossing providers rebuilds the turn's context from the durable record, and no way to rebuild it was supplied",
				p.Role, p.Endpoint, p.alternateEndpoint(model))
		}
	}
	if p.Eligibility == nil || p.Endpoint.Provider == "" {
		return nil
	}
	return p.Eligibility.Substitutable(p.Endpoint, p.alternateEndpoint(model), p.Role)
}

func (p Policy) report(err error) {
	if err == nil {
		return
	}
	if p.RecordFailure != nil {
		p.RecordFailure(err)
	}
}

// refusal reports an invocation the provider declined for want of capacity
// rather than one it answered. A limit reported alongside an answer the provider
// still gave is not a refusal: nothing was stopped, so there is nothing to serve
// from anywhere else.
func refusal(result backend.RunResult, err error) *backend.UsageLimit {
	if result.UsageLimit == nil || (err == nil && !result.IsError) {
		return nil
	}
	return result.UsageLimit
}
