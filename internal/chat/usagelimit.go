package chat

// A turn the provider declined for want of capacity: written down where somebody
// who is not at this terminal can read it, and waited out rather than failed on.
//
// A run that meets an exhausted limit records the reset time, waits, and reissues
// the attempt it was refused. A conversation turn used to do none of that: it
// failed, and whatever the turn was about to do never happened. On 2026-08-17 a
// product-manager turn read the tracker seven times, delivered its reply
// preamble, and died on the continuation invocation with a session limit that
// reset an hour later; the decomposition it was in the middle of creating was
// simply lost, and the operator asking "did we stop?" is what surfaced it.
//
// So a turn the operator is waiting on waits the way a run waits, under the same
// configured bounds and the same polling discipline: sleep the probe interval or
// the time left to the quoted reset, whichever is shorter, and ask again. What
// differs is what can be done with a wait the harness will not take. A run parks
// — its deadline is durable, and a later invocation continues it — and a
// conversation has no such record to be continued from, so it fails with what
// the provider said stated on the way out, which is the one thing the operator
// needs to decide when to say it again.
//
// Nothing a reissued turn does is done twice. Tracker actions this message
// already applied were applied by rounds that finished, and the reissue resumes
// the latest recorded provider session that holds them: what is asked again is
// the single invocation the provider refused. A turn taken while it waits is
// continued from rather than overwritten.
//
// The refusal is recorded either way. An exhausted limit is not this
// conversation's problem — it is every process's, for as long as it lasts — so
// it goes into the product's own log rather than only onto the screen of whoever
// typed the message.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/modelfailover"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// UsageLimitPause bounds how long a refused turn waits for the provider to serve
// it again. It is the run's own configuration rather than a second set of
// numbers: an operator who has said how long the harness may wait out a limit has
// said it about every provider invocation they pay for, and a conversation that
// answered to a different bound would be a wait they never configured. The
// interval between attempts is Options.UsageLimitUnknownResetPause, which the
// conversation already reads for the same reason and which is not repeated here.
//
// Its zero value waits for nothing, which is what a conversation wired without
// it gets: a refused turn fails exactly as it did before waiting existed. That is
// what every conversation the harness takes for itself is wired with — a stopped
// run delivered to the development manager, a recurring firing, a correction —
// because each of those already paces itself on the refusal and a turn that slept
// through the window instead would hold the scheduler that took it for hours.
type UsageLimitPause struct {
	// Maximum is execution.usage_limit_max_pause: the longest one message may
	// spend waiting, across every wait its rounds take rather than each of them
	// separately. Bounding each wait on its own would let a provider that keeps
	// refusing walk one message far past it, one acceptable-looking wait at a
	// time.
	Maximum time.Duration
	// InProcess is execution.usage_limit_in_process_pause: how much of that a run
	// will spend asleep in one process. A conversation cannot spend more than it
	// either, and cannot do what a run does when it reaches it — exit with the
	// deadline durable for a later invocation to continue — so reaching it fails
	// the turn rather than parking it.
	InProcess time.Duration
}

// bound is the longest one message will spend waiting. It is the smaller of the
// two bounds a run waits under, because the larger of them describes something a
// conversation cannot do: a run that reaches its in-process bound leaves the work
// in flight with its deadline recorded, and there is no such record behind a
// turn to leave anything in.
func (p UsageLimitPause) bound() time.Duration {
	if p.InProcess < p.Maximum {
		return p.InProcess
	}
	return p.Maximum
}

// waitsOutUsageLimits reports a conversation configured to wait at all. Both
// halves are required, and each missing one means the same thing: no budget to
// spend, or no interval to spend it in, is a conversation that fails a refused
// turn the way it always did rather than one that reissues with nothing between
// the attempts.
func (o Options) waitsOutUsageLimits() bool {
	return o.UsageLimitPause.bound() > 0 && o.UsageLimitUnknownResetPause > 0
}

// UsageLimitError reports a turn the provider refused and whose wait the harness
// will not take. It is its own type for the same reason the operator's hold is:
// nothing about the conversation is broken, and what the operator needs is when
// the provider said it lifts rather than a failure to interpret.
type UsageLimitError struct {
	// Kind is the provider's own name for the exhausted limit, and ResetsAt is
	// when it said the limit lifts. ResetsAt is zero where the provider named no
	// reset time at all, which is a different fact from a reset already behind us.
	Kind     string
	ResetsAt time.Time
	// Reason says why this wait was not one the harness would take.
	Reason string
}

func (e *UsageLimitError) Error() string {
	described := runstate.DescribePause(runstate.PauseUsageLimit, e.Kind)
	if !e.ResetsAt.IsZero() {
		described += ", which it reports resetting at " + e.ResetsAt.UTC().Format(time.RFC3339)
	} else {
		described += ", for which it named no reset time"
	}
	return fmt.Sprintf("this turn was refused by %s, and the harness will not wait it out: %s; nothing was lost, and saying this again takes the turn that was refused",
		described, e.Reason)
}

// UsageLimits is where a provider's refusal is collected. It is satisfied by
// runstate.UsageLimitStore.
//
// It is read as well as written because failover reads it: which models the
// provider has refused, and until when, is what says whether the next turn
// should ask the configured model at all. That is the same log rather than a
// second one, because a window is one fact and two records of it would be two
// answers.
type UsageLimits interface {
	Record(exhaustion runstate.UsageLimitExhaustion) error
	List() ([]runstate.UsageLimitExhaustion, error)
}

// noteUsageLimit records a provider refusal this turn met, and reports only what
// went wrong recording it. A turn that was not refused, and a conversation with
// nowhere to record one, both record nothing and say nothing: the caller is
// already deciding what to do about the refusal itself, and this adds a durable
// trace of it rather than another way for the turn to fail.
//
// The model is the one the turn was refused on — the alternate, where failover
// had already moved the turn there — rather than the one the agent is
// configured for. A refusal that names it is what lets the record be read back
// as a hold: whether the provider is refusing every role at once is a question
// about which models are refused, and a refusal that named none could only be
// attributed by guessing. Between 2026-09-08 and 09-13 every refusal in the log
// named none. The account is the one the refused endpoint ran under, because a
// later turn served on that account and model is what reads the refusal as
// lifted before the reset it quoted.
//
// One limit is written down once for as long as one message is waiting it out. A
// wait probes the same closed window at the configured interval and is refused
// again by every probe, so recording each of them would tell somebody who is not
// at this terminal about one limit a dozen times — at the severity that means
// hours in which nothing will happen, which a dozen of reads as a dozen separate
// stoppages rather than as one that is still going. What is new information is a
// refusal naming a different limit or model, or the same limit with a reset that
// has moved, and either is written down.
func (s *Session) noteUsageLimit(result backend.RunResult, err error, model, account string) error {
	limit := refusedForUsageLimit(result, err)
	if limit == nil || s.options.UsageLimits == nil {
		return nil
	}
	noted := describeRefusal(*limit, model)
	if noted == s.notedRefusal {
		return nil
	}
	exhaustion := runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     s.options.ProductID,
		At:            s.options.clock().Now(),
		// Which conversation was stopped is the whole of what an operator needs
		// beside the limit itself: a refused architect and a refused product
		// manager are the same limit stopping different work.
		Waiting:        fmt.Sprintf("the %s conversation %s", RoleTitle(s.state.Role), s.state.ConversationID),
		Kind:           limit.Kind,
		ConversationID: s.state.ConversationID,
		Model:          strings.TrimSpace(model),
		AccountAlias:   strings.TrimSpace(account),
	}
	if !limit.ResetsAt.IsZero() {
		resetsAt := limit.ResetsAt.UTC()
		exhaustion.ResetsAt = &resetsAt
	}
	if err := s.options.UsageLimits.Record(exhaustion); err != nil {
		// A refusal that was not written down is not one this message has said, so
		// the next probe tries again rather than treating the failed write as one.
		return fmt.Errorf("record the provider's refusal: %w", err)
	}
	s.notedRefusal = noted
	return nil
}

// describeRefusal is one refusal as the thing to be told about once: the limit
// the provider named, the model it refused, and when it said it lifts. Two
// refusals that agree on all three are the same closed window met twice, which
// is what a wait does by design; a reset that has moved is the provider saying
// something new about it.
func describeRefusal(limit backend.UsageLimit, model string) string {
	described := limit.Kind + "|" + strings.TrimSpace(model)
	if limit.ResetsAt.IsZero() {
		return described + "|no reset time"
	}
	return described + "|" + limit.ResetsAt.UTC().Format(time.RFC3339)
}

// waitOutUsageLimit waits for a provider that refused this turn, and reports
// nothing where the turn is to be asked again. It is the run's rule with the run's
// numbers — orchestrator.activeRun.awaitRecordedUsageLimit sleeps min(time left,
// probe) and reissues, whether or not a reset was named — so an unusable reset
// time or a wait that no longer fits what this message may spend refuses the wait
// instead of guessing one, and everything else sleeps a probe and reissues.
func (s *Session) waitOutUsageLimit(ctx context.Context, limit backend.UsageLimit) (bool, error) {
	pause := s.options.UsageLimitPause
	interval := s.options.UsageLimitUnknownResetPause
	refused := func(reason string) error {
		return &UsageLimitError{Kind: limit.Kind, ResetsAt: limit.ResetsAt, Reason: reason}
	}
	now := s.options.clock().Now()
	// A limit with no reset time is unknown rather than unwaitable. The overage
	// allowance reports this way while the ordinary rolling window keeps resetting
	// on its usual schedule, so the harness asks again rather than being told when.
	deadline := limit.ResetsAt
	if deadline.IsZero() {
		deadline = now.Add(interval)
	}
	remaining := deadline.Sub(now)
	if remaining <= 0 {
		// A limit still refusing work while naming a reset that has already passed
		// is not describing a wait. Honoring it would mean reissuing straight back
		// into the same refusal with nothing bounding the attempts, and a clock skew
		// or a window the provider has not rolled yet is a fact for a person.
		return false, refused("the reset it names is not in the future, and the harness does not guess a wait it was not given")
	}
	probe := min(remaining, interval)
	if s.usageLimitWaited+probe > pause.bound() {
		reason := fmt.Sprintf("waiting %s to ask again would take this message past the %s it may spend waiting", probe, pause.bound())
		if s.usageLimitWaited > 0 {
			reason += fmt.Sprintf(", and it has already waited %s", s.usageLimitWaited)
		}
		return false, refused(reason)
	}
	// What the wait spends is counted before it is spent, so a wait interrupted
	// part way cannot buy this message a fresh budget by forgetting it.
	s.usageLimitWaited += probe
	// Interactive or not, the wait is the same; where somebody is watching, this is
	// what stops a turn that is waiting out hours of provider silence from looking
	// exactly like a turn that has hung. What it names is the moment this probe
	// ends, which is when the turn actually asks again — the quoted reset only
	// where that is sooner than the next probe.
	reason := describeUsageLimitWait(limit, now.Add(probe))
	s.activity.doing(reason)
	if !limit.ResetsAt.IsZero() {
		reason += "; the window resets at " + limit.ResetsAt.Local().Format("2006-01-02 15:04:05 MST")
	}
	return s.waitForProvider(ctx, probe, reason)
}

// describeUsageLimitWait says what the turn is waiting on and until when, in the
// operator's language rather than the provider's. The moment it names is when
// the turn will ask again, which is what somebody watching wants to know.
func describeUsageLimitWait(limit backend.UsageLimit, asksAgainAt time.Time) string {
	described := runstate.DescribePause(runstate.PauseUsageLimit, limit.Kind)
	return fmt.Sprintf("waiting out %s; asking again at %s", described, asksAgainAt.Local().Format(time.Kitchen))
}

// failoverPolicy is what this conversation's turn may be served by when the
// model it would ask for will not take it — the pinned version the provider has
// not got, or the configured model whose window is closed — and where either
// substitution is written down. A conversation whose agent has pinned no version
// and enabled no failover produces the zero policy, which is both mechanisms
// off: the turn is one invocation under the configured model, exactly as it was.
//
// The substitution is recorded in the same log a refusal is, for the same
// reason a refusal is recorded there at all — what a provider will and will not
// serve is a fact about the product rather than about this conversation, and the
// process that meets it is rarely the process that takes the next turn.
func (s *Session) failoverPolicy() modelfailover.Policy {
	alternate := strings.TrimSpace(s.options.FailoverModel)
	version := strings.TrimSpace(s.options.ModelVersion)
	if alternate == "" && version == "" {
		return modelfailover.Policy{}
	}
	policy := modelfailover.Policy{
		Alternate:         alternate,
		Version:           version,
		Now:               s.options.clock().Now,
		UnknownResetPause: s.options.UsageLimitUnknownResetPause,
		ProductID:         s.options.ProductID,
		// The same sentence a refusal here writes, because it is the same thing
		// that would have stopped — and what makes this entry the other half of
		// that fact is that something served it anyway.
		Waiting:        fmt.Sprintf("the %s conversation %s", RoleTitle(s.state.Role), s.state.ConversationID),
		ConversationID: s.state.ConversationID,
		RecordFailure: func(err error) {
			s.failoverProblem = appendProblem(s.failoverProblem, singleLine(err.Error(), maxTrackerFailureBytes))
		},
	}
	// A conversation with nowhere to record one still fails over — the turn is
	// what matters — and pays a refused invocation each turn to rediscover the
	// window. The log is only wired where there is one, so a typed nil never
	// reaches the failover as a store it can call.
	if s.options.UsageLimits != nil {
		policy.Windows = s.options.UsageLimits
	}
	// The endpoint this conversation is held on, and the providers it may name,
	// so the substitution is checked against this role's tool posture before the
	// turn is moved.
	//
	// A conversation whose endpoint will not resolve substitutes as it did before
	// the check existed, and says so rather than doing it quietly. Refusing the
	// turn instead would trade an answer the operator wants for a check that
	// today can only fail on the provider — the alternate is another model on the
	// same provider — and a skip nobody is told about is the one path where the
	// guarantee silently does not hold. Every conversation the harness opens
	// resolves its endpoint, which is what TestAnOpenedConversationAlwaysResolves-
	// ItsEndpoint holds, so this is a path a running harness does not take.
	endpoint, resolved := s.options.endpoint()
	if !resolved {
		s.failoverProblem = appendProblem(s.failoverProblem, singleLine(fmt.Sprintf(
			"the substitution check was not made: this conversation's endpoint could not be resolved from provider %q, account %q, and model %q",
			s.options.Provider, s.options.AccountAlias, s.options.Model), maxTrackerFailureBytes))
		return policy
	}
	policy.Endpoint = endpoint
	policy.Role = s.options.Role
	policy.Eligibility = s.options.providers()
	// And where the alternate is served, for an agent whose alternate leaves the
	// provider. Everything a crossing needs travels together — the endpoint, the
	// adapter that reaches it, and the way back to the conversation's own record —
	// because a crossing that had two of the three would be a turn sent somewhere
	// it could not be answered from.
	if alternate := s.options.FailoverEndpoint; alternate.Provider != "" && alternate.Provider != endpoint.Provider {
		policy.AlternateEndpoint = alternate
		policy.AlternateAccountConfigDir = s.options.FailoverAccountConfigDir
		policy.Rebuild = s.rebuildForAlternate
		// And the session that endpoint already holds, where the record says it has
		// been serving this conversation since the window closed. A window outlasts a
		// turn, so the second and third turns of an outage go where the first one did
		// and resume rather than reconstructing again.
		policy.AlternateSessionID = s.alternateSession()
		// And whether the provider it crosses onto accepts this agent's effort
		// level. One that does not is asked with none, and the turn's record says
		// so, rather than the crossing failing on a flag its provider refuses.
		if effort := strings.TrimSpace(s.options.Effort); effort != "" {
			if descriptor, known := s.options.providers().Lookup(alternate.Provider); known && !descriptor.AcceptsEffort(effort) {
				policy.AlternateDropsEffort = true
			}
		}
		if s.options.FailoverBackend != nil {
			policy.AlternateProvider = s.meteredFailover(false)
		}
	}
	return policy
}

// meteredFailover is the alternate provider with the cost log wired behind it,
// charged to the account and the provider that actually served the turn. It is
// built here rather than beside the conversation's own meter because it is only
// ever used by a substitution: an agent that never crosses providers never builds
// one, and one that does gets a line saying what the crossing cost and where.
func (s *Session) meteredFailover(savingMemory bool) modelfailover.Invoker {
	provider := spend.Metered{
		Provider:    s.options.FailoverBackend,
		Log:         s.options.Spend,
		Attribution: s.failoverAttribution(),
		Clock:       s.options.Clock,
		// The same trade the conversation's own meter makes, for the same reason: a
		// turn the alternate has already answered is not thrown away because the
		// cost log would not take the line.
		RecordFailure: func(err error) {
			s.spendProblem = appendProblem(s.spendProblem, singleLine(err.Error(), maxTrackerFailureBytes))
		},
		// And the crossing is counted the same way the conversation's own turns
		// are. A turn the alternate served cost what it cost, and a figure that
		// left it out would be an operator watching a conversation whose spend
		// stopped moving the moment it crossed.
		Recorded: s.countSpend,
	}
	return requestBounded{session: s, adapter: s.options.FailoverBackend, provider: provider, savingMemory: savingMemory}
}

// ErrProviderCapacity marks the failure of a turn the provider declined for
// want of capacity, rather than one it answered badly. It is a sentinel joined
// into the error the turn fails with, so what an operator reads is unchanged and
// a caller that is not a person can still tell the two apart.
//
// The distinction is what the caller owes the turn. A turn nobody was asked is
// worth asking again once the limit resets; one the role answered is not, and
// asking again would be the same money spent on the same answer. It exists
// because the harness now takes turns nobody is sitting in front of — a stopped
// run delivered into the development manager's conversation — and a refusal met
// there must not read as her having been asked.
var ErrProviderCapacity = errors.New("the provider declined this turn for want of capacity")

// providerDeclined is that sentinel for a turn the provider refused, and nothing
// for every other ending.
func providerDeclined(result backend.RunResult, err error) error {
	if refusedForUsageLimit(result, err) == nil {
		return nil
	}
	return ErrProviderCapacity
}

// refusedForUsageLimit reports a turn the provider declined for want of
// capacity, rather than one it answered. The limit is only a refusal where the
// invocation actually failed: a provider may report a limit alongside an answer
// it still gave, and an answered turn is not something anybody is waiting on.
func refusedForUsageLimit(result backend.RunResult, err error) *backend.UsageLimit {
	if result.UsageLimit == nil || (err == nil && !result.IsError) {
		return nil
	}
	return result.UsageLimit
}
