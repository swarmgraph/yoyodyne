package runstate

// What a work item cost is a join nothing else makes. Every run records the
// item it served, and every provider invocation inside a run ends in an event
// carrying the provider's own cost for it, so an item's price is the sum over
// every run made for it: the failed attempt as well as the successful one, the
// repair attempts, and the reviewer's invocation beside the developer's. A
// per-run view of the same evidence shows several numbers where the question
// asked was about one piece of work.
//
// The cost is read from the recorded evidence and never estimated from a price
// table, which drifts the moment a provider changes what it charges. A run whose
// evidence is gone is priced as unknown rather than as nothing: a zero that
// means "no record" would corrupt every total it is added to.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// pricedEvents are the events that carry what one provider invocation cost.
// Both are terminal reports of a single invocation and both carry the cost, so a
// run whose provider ended in an error is priced with the money that error spent
// rather than as though it had been free.
var pricedEvents = []execution.EventType{execution.EventRunCompleted, execution.EventRunFailed}

// spendEvidenceEvents are every event the phase split is read from: the priced
// terminals above, which say whose invocation they ended, and the review that
// brackets one of them. A review announcing itself costs nothing and is decoded
// anyway, because it is what says the next terminal is the reviewer's in a log
// recorded before a terminal named its own role.
var spendEvidenceEvents = append([]execution.EventType{execution.EventReviewStarted}, pricedEvents...)

// PhaseCost is what one part of a run cost and how many provider invocations it
// took. The count travels with the money because the money alone does not say
// whether a phase was one expensive invocation or five cheap ones, and what a
// figure like this is read for -- how often work has to be repaired, how much a
// review round costs -- is as much about how often as about how much.
type PhaseCost struct {
	CostUSD     float64 `json:"cost_usd"`
	Invocations int     `json:"invocations,omitempty"`
	// Tokens is what the provider billed this phase's invocations for, split by
	// how it billed the input. It is here rather than only on the run because the
	// phases do not assemble their prompts alike and are not cached alike: the
	// developer's session re-reads its own conversation every turn and reads
	// nearly all of its input from the cache, and the reviewer makes one short
	// invocation whose only cacheable part is the prefix it shares with every
	// other review. Summed together those are one figure the larger of them
	// decides, which is exactly how yoyodyne-ifd.84 came to be measured against a
	// number that could not resolve what it was pointed at.
	Tokens TokenUsage `json:"tokens,omitempty"`
}

func (p *PhaseCost) add(cost float64, tokens TokenUsage) {
	p.CostUSD += cost
	p.Invocations++
	p.Tokens.Merge(tokens)
}

func (p *PhaseCost) merge(other PhaseCost) {
	p.CostUSD += other.CostUSD
	p.Invocations += other.Invocations
	p.Tokens.Merge(other.Tokens)
}

// Waits is time a run spent not working: waiting out a provider that refused it
// for want of capacity or could not serve it at all, and parked at a provider
// call while the operator held activity. It is counted in seconds rather than in
// dollars because a wait spends nothing, and adding it to the money would make a
// run that waited overnight read as expensive when what it was is slow.
//
// It is read from the run's own durable state rather than from its event log,
// which is why it survives what the money does not: a run whose log is gone
// still says how long it was held up.
type Waits struct {
	UsageLimitSeconds   int64 `json:"usage_limit_seconds,omitempty"`
	OperatorHoldSeconds int64 `json:"operator_hold_seconds,omitempty"`
}

// Total is how long the waits held work up altogether.
func (w Waits) Total() time.Duration {
	return time.Duration(w.UsageLimitSeconds+w.OperatorHoldSeconds) * time.Second
}

func (w *Waits) merge(other Waits) {
	w.UsageLimitSeconds += other.UsageLimitSeconds
	w.OperatorHoldSeconds += other.OperatorHoldSeconds
}

// PhaseSpend splits what was spent by the part of the work each invocation
// served. A single total says a piece of work was expensive; this says whether
// it was expensive because the change was hard, because it was reviewed over and
// over, or because it had to be repaired -- which is the difference between a
// number to look at and a number to act on.
//
// Every priced invocation in a run's log lands in exactly one of the four, so
// the money always adds back up to the total beside it. That is what makes this
// a decomposition of the price rather than a second opinion about it. Three of
// the four are the phases; the fourth is what an invocation that does not say
// which phase it served costs, and it is a bucket rather than a phase precisely
// so that no such invocation can be quietly charged to one.
type PhaseSpend struct {
	// Development is the developer's first attempt at the change, including any
	// invocation reissued into it after the provider refused or killed one: what
	// the attempt cost is what it took to get it made.
	Development PhaseCost `json:"development"`
	// Review is every reviewer invocation the run made, whichever way each
	// verdict went and including the ones that produced no verdict at all.
	Review PhaseCost `json:"review"`
	// Repair is every developer attempt after the first -- the failing check, the
	// refused path, and the reviewer's findings handed back are all repair, since
	// each is the same thing from the money's point of view: the change being made
	// again because it was not right the first time.
	Repair PhaseCost `json:"repair"`
	// Unattributed is every priced invocation in the log that did not say which
	// phase it served: a role the split has no place for, and anything else that
	// reached a run's log without announcing itself. It exists so that the answer
	// to an unrecognized invocation is a figure nobody can mistake for a phase,
	// rather than the phase the surrounding terminals happen to imply -- which
	// would put somebody else's money in the repair column of exactly the runs
	// being measured, and leave nothing to read that says so.
	//
	// It is expected to be zero. Anything in it is money the harness spent that
	// nothing in the record accounts for, which is a defect in whatever wrote the
	// log rather than a cost of the work.
	Unattributed PhaseCost `json:"unattributed"`
	// Waits is time rather than money, and is here rather than beside this because
	// it answers the same question: where a piece of work went.
	Waits Waits `json:"waits"`
}

// TotalUSD is what the split adds up to, which is the total it was split from.
// The unattributed money is in it because it was spent: leaving it out would
// make an invocation nobody can place cost nothing, which is the one answer
// about it that is certainly wrong.
func (p PhaseSpend) TotalUSD() float64 {
	return p.Development.CostUSD + p.Review.CostUSD + p.Repair.CostUSD + p.Unattributed.CostUSD
}

// Invocations is how many provider invocations the split covers.
func (p PhaseSpend) Invocations() int {
	return p.Development.Invocations + p.Review.Invocations + p.Repair.Invocations + p.Unattributed.Invocations
}

// Merge adds another split into this one, which is what an aggregate across
// several runs or several work items is made of.
func (p *PhaseSpend) Merge(other PhaseSpend) {
	p.Development.merge(other.Development)
	p.Review.merge(other.Review)
	p.Repair.merge(other.Repair)
	p.Unattributed.merge(other.Unattributed)
	p.Waits.merge(other.Waits)
}

// TokenUsage is what the provider said it read and wrote to serve one or more
// invocations, with the input split by how the provider billed it. It travels
// beside the money because the money on its own cannot answer the question a
// token-efficiency change asks of itself: a run that got cheaper because the
// provider changed its prices and a run that got cheaper because more of its
// prompt was already cached are the same figure in dollars and opposite facts
// about the harness.
//
// The three input figures are disjoint parts of one input -- the provider bills
// what it served from its cache at the cached rate, what it wrote into the cache
// at the write rate, and the remainder fresh -- which is what makes the share
// below a share rather than a ratio between unrelated numbers.
type TokenUsage struct {
	// Priced counts turns with a reported dollar cost, independently of usage.
	Priced int `json:"priced_invocations,omitempty"`
	// NoCost counts turns whose provider did not report a dollar cost.
	NoCost int `json:"no_cost_invocations,omitempty"`
	// InputTokens is the fresh input alone, as the provider reports it: the part
	// of the prompt it neither served from its cache nor wrote into one.
	InputTokens         int64 `json:"input_tokens,omitempty"`
	CacheReadTokens     int64 `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
	// CacheWrite5mTokens and CacheWrite1hTokens split the cache writes by the
	// lifetime they were written at, as the provider reports the split beside
	// the total. The two lifetimes are billed at different multiples of the base
	// rate, so a cost split by what the input was billed as needs them apart;
	// they are kept beside the total rather than replacing it because a terminal
	// recorded before the provider reported the split carries the total alone,
	// and that total is still every token written.
	CacheWrite5mTokens int64 `json:"cache_write_5m_tokens,omitempty"`
	CacheWrite1hTokens int64 `json:"cache_write_1h_tokens,omitempty"`
	OutputTokens       int64 `json:"output_tokens,omitempty"`
	// Measured counts the priced invocations whose terminal carried a usage
	// object, and Unreported the ones that carried none. Both are kept because
	// the count is the only thing that tells them apart once they are summed: an
	// invocation the provider reported as reading nothing and an invocation
	// nobody has a reading for contribute the same nought to every figure above,
	// and they are opposite facts. The first is a measurement, and belongs in the
	// share; the second is the absence of one, and while it is non-zero the share
	// is a share of a subset.
	//
	// Unreported is the token side of ItemPrice.UnknownRuns, and is kept out of
	// the figures for the same reason: counting an unmeasured invocation as zero
	// would drag a share towards nothing by exactly the invocations the measure
	// cannot see, which is the one answer about them that is certainly wrong.
	Measured   int `json:"measured_invocations,omitempty"`
	Unreported int `json:"unreported_invocations,omitempty"`
}

// InputTotal is every token the provider billed as input, however it billed it.
// It is the denominator of the share, and it is the sum of the three rather than
// the reported fresh input alone: a prompt served entirely from the cache is
// reported with almost no fresh input, and dividing by that would make the
// emptiest prompt read as the best cached one.
func (t TokenUsage) InputTotal() int64 {
	return t.InputTokens + t.CacheReadTokens + t.CacheCreationTokens
}

// Reported reports at least one invocation the provider gave a reading for, so
// that the share below is a measurement rather than the absence of one. It asks
// whether anything was measured and not whether the measurement came to
// anything: an invocation that really did read nothing is a share of nought, and
// an invocation nobody measured has no share, and a caller that could not tell
// those apart would report the second as the first.
func (t TokenUsage) Reported() bool { return t.Measured > 0 }

// CacheReadShare is the part of the input the provider served from its own
// cache, between 0 and 1. It is the measure a prompt-caching change is kept or
// reverted on, and it is zero on usage nothing reported.
func (t TokenUsage) CacheReadShare() float64 {
	total := t.InputTotal()
	if total == 0 {
		return 0
	}
	return float64(t.CacheReadTokens) / float64(total)
}

// Merge adds another invocation's, run's, or item's usage into this one, which
// is what a share over a window of runs is made of.
func (t *TokenUsage) Merge(other TokenUsage) {
	t.InputTokens += other.InputTokens
	t.CacheReadTokens += other.CacheReadTokens
	t.CacheCreationTokens += other.CacheCreationTokens
	t.CacheWrite5mTokens += other.CacheWrite5mTokens
	t.CacheWrite1hTokens += other.CacheWrite1hTokens
	t.OutputTokens += other.OutputTokens
	t.Measured += other.Measured
	t.Unreported += other.Unreported
	t.NoCost += other.NoCost
	t.Priced += other.Priced
}

// The provider prices each kind of token at a fixed multiple of the model's
// base input rate, and the multiples are the same across its models: a cache
// read at a tenth, a write kept for five minutes at a quarter over, a write
// kept for an hour at double, and output at five times. They are what lets a
// reported cost be split by what it was billed for without a price table: the
// split is a share of the provider's own figure, so a model's base rate never
// enters it and a change to that rate moves nothing here. What would move it is
// a model priced off these multiples, which shifts the split and never the
// total. Every terminal recorded on this machine was checked against them
// before they were written down: 1,317 of 1,399 opus-5 invocations and 484 of
// 485 fable-5 invocations price to a single base rate within one per cent
// under exactly these weights (docs/diagnoses/yoyodyne-ifd-424-one-shot-cache-reads.md).
// A cache write whose lifetime the terminal did not record is weighted as the
// shorter one, which is the provider's default lifetime and the lower of the
// two premiums.
const (
	cacheReadWeight    = 0.1
	cacheWrite5mWeight = 1.25
	cacheWrite1hWeight = 2.0
	outputWeight       = 5.0
)

// CostSplit is a reported cost apportioned across what the provider billed the
// invocation for. It answers the question a cache-read share cannot: not what
// fraction of the input was served from the cache, but what the cache actually
// cost — a prompt written into the cache at the write premium and never read
// back is money the share does not show, because the share counts reads.
//
// Every figure is a share of the cost the provider reported, so the four add
// up to it exactly. Unsplit is the part nothing could apportion: an invocation
// whose terminal carried no usage, or reported using nothing at all, still cost
// what it cost and is carried whole rather than placed by guesswork.
type CostSplit struct {
	FreshUSD      float64 `json:"fresh_usd,omitempty"`
	CacheReadUSD  float64 `json:"cache_read_usd,omitempty"`
	CacheWriteUSD float64 `json:"cache_write_usd,omitempty"`
	OutputUSD     float64 `json:"output_usd,omitempty"`
	UnsplitUSD    float64 `json:"unsplit_usd,omitempty"`
}

// Merge adds another invocation's split into this one.
func (c *CostSplit) Merge(other CostSplit) {
	c.FreshUSD += other.FreshUSD
	c.CacheReadUSD += other.CacheReadUSD
	c.CacheWriteUSD += other.CacheWriteUSD
	c.OutputUSD += other.OutputUSD
	c.UnsplitUSD += other.UnsplitUSD
}

// Split apportions one invocation's reported cost across the usage its
// terminal recorded, at the multiples above. It is per invocation on purpose:
// a split over usage merged across invocations of differently priced models
// would weight one model's tokens at another's rate, and the sum of per-
// invocation splits has no such problem.
func (t TokenUsage) Split(costUSD float64) CostSplit {
	written5m, written1h := t.CacheWrite5mTokens, t.CacheWrite1hTokens
	if written5m+written1h == 0 {
		written5m = t.CacheCreationTokens
	}
	fresh := float64(t.InputTokens)
	read := cacheReadWeight * float64(t.CacheReadTokens)
	written := cacheWrite5mWeight*float64(written5m) + cacheWrite1hWeight*float64(written1h)
	output := outputWeight * float64(t.OutputTokens)
	weighted := fresh + read + written + output
	if !t.Reported() || weighted == 0 {
		return CostSplit{UnsplitUSD: costUSD}
	}
	return CostSplit{
		FreshUSD:      costUSD * fresh / weighted,
		CacheReadUSD:  costUSD * read / weighted,
		CacheWriteUSD: costUSD * written / weighted,
		OutputUSD:     costUSD * output / weighted,
	}
}

// RunPrice is what one recorded run cost, as its own event log reports it.
type RunPrice struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Status     Status `json:"status"`
	// Outcome is what became of the run, in the fixed vocabulary a listing says
	// it in. It is read from the same derivation the run history uses rather than
	// from the status here, so a run a reader meets in a price breakdown and in
	// `yoyo status` is described in one word rather than two.
	Outcome     RunOutcome `json:"outcome"`
	Phase       Phase      `json:"phase,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// Integrated reports a run that promoted its work, which is what separates
	// the attempt that finished a piece of work from the ones that did not.
	Integrated bool `json:"integrated,omitempty"`
	// Remains is the short preservation answer. A surface fills Found and
	// Remains from the repository before saying what survives. It rides with the
	// price because a reader of an item's runs asks two things of each — what it
	// cost and whether its work is still there — and one read answers both.
	Remains string        `json:"remains,omitempty"`
	Found   *triage.Found `json:"found,omitempty"`
	// Invocations counts the provider invocations priced from this run's log,
	// which is the developer's, the reviewer's, and one more per repair attempt.
	Invocations int     `json:"invocations,omitempty"`
	CostUSD     float64 `json:"cost_usd"`
	// Phases splits that cost by the part of the run each invocation served. Its
	// money is zero on a run that could not be priced; its waits are not, because
	// they come from the run's own state rather than from the log that is gone.
	Phases PhaseSpend `json:"phases"`
	// Tokens is what the provider billed this run's invocations for, split by how
	// it billed the input. It is the sum of what the phases beside it were billed,
	// and it is kept because a run's whole input is what a window-wide share is
	// taken over; which phase reads its prefix and which rewrites it every time is
	// the question Phases answers.
	Tokens TokenUsage `json:"tokens"`
	// Unknown says why this run could not be priced, and is empty on one that
	// was. A run carrying it is not a run that cost nothing: nothing survives to
	// say what it cost, which is a different fact and is stated as itself.
	Unknown string `json:"unknown,omitempty"`
}

// Known reports a run the recorded evidence could actually price.
func (r RunPrice) Known() bool { return r.Unknown == "" }

// ItemPrice is what every recorded run of one work item cost. The total covers
// the runs that could be priced, so an item with unknown runs is priced at a
// floor rather than at a number that reads as complete.
type ItemPrice struct {
	WorkItemID string     `json:"work_item_id"`
	Runs       []RunPrice `json:"runs,omitempty"`
	TotalUSD   float64    `json:"total_usd"`
	// Phases splits that total across every run made for the item, which is where
	// the split earns its keep: an item that took three attempts is where the
	// money that went on repair rather than on the change itself is visible.
	Phases PhaseSpend `json:"phases"`
	// Tokens is what the provider billed across every run of the item that could
	// be priced, which is where the cache-read share earns its keep: one run is a
	// sample and a window of them is the measure.
	Tokens TokenUsage `json:"tokens"`
	// UnknownRuns counts the runs whose evidence is gone. While it is non-zero
	// the total is a lower bound on what the item cost.
	UnknownRuns int `json:"unknown_runs,omitempty"`
}

// Priced reports a total every run behind it could be priced from, which is
// what separates an exact price from a floor.
func (i ItemPrice) Priced() bool { return i.UnknownRuns == 0 }

// Recorded reports that the harness has at least one run recorded for this
// item. An item with none has no price rather than a price of zero: nothing was
// ever run for it, or the runs that were are no longer recorded, and neither is
// a thing to charge somebody nothing for.
func (i ItemPrice) Recorded() bool { return len(i.Runs) > 0 }

// ExchangeSpend is what this product's roles have spent asking each other
// things, summed over every exchange recorded for it. The invocation that
// answers a round is money the harness spent on the operator's behalf like any
// other, so leaving it out of what the harness has spent altogether would
// understate that total by exactly as much as the ask channel is used.
//
// It is summed per product rather than per work item because that is as far as
// the record goes. An exchange names a product, a repository, the two roles in
// it, and the conversation the asker spoke from, and nothing that identifies a
// piece of work: not a work item and not a run. The conversation it does name is
// no substitute — a role's conversation is long-lived and discusses whatever it
// is discussing that day, which is the same reason a conversation turn is left
// unattributed rather than charged to whichever item it last mentioned.
//
// The membership of the channel is why that is not an oversight waiting to be
// fixed here: the roles on it own documents and queues, and the two roles that
// work inside a run — the developer and the reviewer — are not on it, so there
// is no exchange today taken in the course of one item. If that changes, the
// join belongs on ItemPrice beside the runs rather than in this figure, and
// TestAnExchangeRecordNamesNoWorkItemToAttributeItsSpendTo fails the moment the
// record can carry one.
type ExchangeSpend struct {
	Tokens TokenUsage `json:"tokens"`
	// Exchanges is how many threads the figure covers and Rounds how many
	// provider invocations they came to between them. The two travel together for
	// the reason a phase's invocation count travels with its money: one long
	// thread and ten short ones are different things to be looking at.
	Exchanges int     `json:"exchanges,omitempty"`
	Rounds    int     `json:"rounds,omitempty"`
	CostUSD   float64 `json:"cost_usd"`
	// Unreadable counts the records that could not be read, and is the exchange
	// side of ItemPrice.UnknownRuns: those records are left out of the total
	// rather than counted as nothing, and while this is non-zero the total is a
	// lower bound. What it must never do is take the exchanges beside them with
	// it — money that was read is money the operator is owed a figure for.
	Unreadable int `json:"unreadable,omitempty"`
	// Unknown says why, and is empty only when nothing went wrong. It carries the
	// first record's reason where records failed, and the directory's own where
	// the exchanges could not even be enumerated — which is the one case with no
	// count to give, because how many are missing is itself unknown.
	Unknown string `json:"unknown,omitempty"`
}

// Known reports a figure every recorded exchange is in.
func (e ExchangeSpend) Known() bool { return e.Unreadable == 0 && e.Unknown == "" }

// Enumerated reports records that could at least be counted, which is what
// separates a total that is a lower bound from no total at all: an exchange
// directory nothing can list leaves not even a floor, because what is missing
// from it is unknown in number as well as in amount.
func (e ExchangeSpend) Enumerated() bool { return e.Exchanges > 0 || e.Unreadable > 0 }

// Recorded reports that there is something here to say: exchanges to price, or
// a reason there is no figure for them. A product whose roles have never asked
// each other anything has neither, and reporting it would be reporting the
// absence of something nobody did.
func (e ExchangeSpend) Recorded() bool { return e.Exchanges > 0 || !e.Known() }

// ExchangeSpend reports what conducting this product's inter-role exchanges has
// cost, as the provider reported each round. Every round carries the provider's
// own figure, so this is a sum rather than an estimate, exactly as a run's price
// is.
//
// It never fails, for the reason pricing a run never fails: exchanges that
// cannot be read are a price nobody knows rather than an error, and reporting
// one would take the runs beside them down with it.
//
// The records are read one at a time for the same reason a run is. One that
// cannot be read is counted and left out, and the exchanges beside it are still
// priced: a corrupt file must not be able to reduce this to nothing, because
// nothing is exactly the answer that produced the undercount this figure exists
// to remove. What it will not do either way is call an unread record free — a
// caller holding a figure with anything unread holds a floor.
func (s *Store) ExchangeSpend() ExchangeSpend {
	store := s.exchanges()
	ids, err := store.Records()
	if err != nil {
		// The directory itself could not be listed, so not even how many
		// exchanges are missing from the figure is known — which is a different
		// answer from a floor, and is given as itself.
		return ExchangeSpend{Unknown: err.Error()}
	}
	var spend ExchangeSpend
	for _, id := range ids {
		recorded, err := store.Read(id)
		if err != nil {
			spend.Unreadable++
			// The first reason stands for all of them. A caller reports the count
			// beside it, and the reason is there to say what kind of broken this is
			// rather than to enumerate every instance of it.
			if spend.Unknown == "" {
				spend.Unknown = err.Error()
			}
			continue
		}
		spend.Exchanges++
		spend.Rounds += recorded.Spent()
		spend.CostUSD += recorded.CostUSD()
		for _, round := range recorded.Rounds {
			spend.Tokens.Merge(exchangeTokens(round))
		}
	}
	return spend
}

// SideStreamSpend is what this product's side conversations have cost, in all
// and by the conversation each was opened beside. A side thread is an agent's
// own provider session, invoked turn after turn, and nothing it spends is in any
// run's log or any conversation's, so a total that did not read these would be
// short by exactly what the side threads cost.
//
// It is attributed to a conversation and never to a work item, for the reason a
// conversation turn is not: the thread it was opened beside is long-lived and
// discusses whatever it is discussing that day.
type SideStreamSpend struct {
	// Streams is how many side threads the figure covers and Invocations how many
	// provider invocations they came to between them.
	Streams     int        `json:"streams,omitempty"`
	Invocations int        `json:"invocations,omitempty"`
	CostUSD     float64    `json:"cost_usd"`
	Tokens      TokenUsage `json:"tokens"`
	// Conversations splits the same figure by the main conversation each thread
	// was opened beside, in identifier order. A thread whose record could not be
	// read is still priced from its log, and lands under an empty conversation
	// rather than under one it may not belong to.
	Conversations []ConversationSideSpend `json:"conversations,omitempty"`
	// Unreadable counts the side threads whose event log could not be read. They
	// are left out of the figure rather than counted as nothing, and while this
	// is non-zero the figure is a lower bound.
	Unreadable int `json:"unreadable,omitempty"`
	// Unknown says why: the first log's reason, or the directory's own where the
	// side threads could not even be listed.
	Unknown string `json:"unknown,omitempty"`
}

// ConversationSideSpend is what the side threads opened beside one conversation
// cost.
type ConversationSideSpend struct {
	Conversation string     `json:"conversation,omitempty"`
	Streams      int        `json:"streams"`
	Invocations  int        `json:"invocations"`
	CostUSD      float64    `json:"cost_usd"`
	Tokens       TokenUsage `json:"tokens"`
}

// Known reports a figure every recorded side thread is in.
func (s SideStreamSpend) Known() bool { return s.Unreadable == 0 && s.Unknown == "" }

// Enumerated reports side threads that could at least be counted, which is what
// separates a floor from no figure at all.
func (s SideStreamSpend) Enumerated() bool { return s.Streams > 0 || s.Unreadable > 0 }

// Recorded reports that there is something to say: side threads to price, or a
// reason there is no figure for them.
func (s SideStreamSpend) Recorded() bool { return s.Streams > 0 || !s.Known() }

// SideStreamSpend prices this product's side conversations from their own event
// logs, by the same reader `yoyo status --spend` prices every stream with, so
// the two surfaces cannot disagree about what one thread cost. Like
// ExchangeSpend it never fails: a log that cannot be read is counted and left
// out, and the threads beside it are still priced.
func (s *Store) SideStreamSpend() SideStreamSpend {
	sides := s.sideStreams()
	listed, err := os.ReadDir(sides.Root())
	if errors.Is(err, os.ErrNotExist) {
		return SideStreamSpend{}
	}
	if err != nil {
		return SideStreamSpend{Unknown: fmt.Sprintf("read side stream directory: %v", err)}
	}
	var spend SideStreamSpend
	byConversation := make(map[string]*ConversationSideSpend)
	for _, entry := range listed {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, eventLogSuffix) {
			continue
		}
		id := strings.TrimSuffix(name, eventLogSuffix)
		scanned, err := scanStreamLog(filepath.Join(sides.Root(), name), StreamSide, true)
		if err != nil {
			spend.Unreadable++
			if spend.Unknown == "" {
				spend.Unknown = fmt.Sprintf("side stream %s: %v", id, err)
			}
			continue
		}
		conversation := ""
		if recorded, err := sides.Load(id); err == nil {
			conversation = recorded.Conversation
		}
		part, seen := byConversation[conversation]
		if !seen {
			part = &ConversationSideSpend{Conversation: conversation}
			byConversation[conversation] = part
		}
		spend.Streams++
		part.Streams++
		for _, invocation := range scanned.invocations {
			spend.Invocations++
			spend.CostUSD += invocation.CostUSD
			spend.Tokens.Merge(invocation.Usage)
			part.Invocations++
			part.CostUSD += invocation.CostUSD
			part.Tokens.Merge(invocation.Usage)
		}
	}
	conversations := make([]string, 0, len(byConversation))
	for conversation := range byConversation {
		conversations = append(conversations, conversation)
	}
	sort.Strings(conversations)
	for _, conversation := range conversations {
		spend.Conversations = append(spend.Conversations, *byConversation[conversation])
	}
	return spend
}

// sideStreams is this product's side stream store, reached from the run store's
// own root for the reason the exchange store is.
func (s *Store) sideStreams() *SideStreamStore {
	return &SideStreamStore{
		root:      filepath.Join(filepath.Dir(s.root), "sidestreams"),
		productID: s.productID,
	}
}

// exchanges is this product's exchange store. Exchanges sit beside the runs
// rather than inside them, so the store is reached from the run store's own root
// rather than from a state root every caller of the read model would otherwise
// have to carry a second time.
func (s *Store) exchanges() *ExchangeStore {
	return &ExchangeStore{
		root:      filepath.Join(filepath.Dir(s.root), "exchanges"),
		productID: s.productID,
	}
}

// Price reports what every recorded run of one work item cost. Reading the
// record decides nothing about the runs, so a run another process is executing
// is priced here exactly as a finished one is — with whatever its log holds so
// far.
func (s *Store) Price(workItemID string) (ItemPrice, error) {
	id := strings.TrimSpace(workItemID)
	if id == "" {
		return ItemPrice{}, errors.New("a work item is required to price it")
	}
	states, err := s.scan("recorded", func(state State) bool { return state.WorkItemID == id })
	if err != nil {
		return ItemPrice{}, err
	}
	return s.price(id, states), nil
}

// Prices reports what every work item the harness has ever run cost, one entry
// per item in identifier order. It is what prices the items that were closed
// before anything recorded a price on them: the runs and their event logs are
// still here, so the ledger starts from what already happened rather than from
// today.
func (s *Store) Prices() ([]ItemPrice, error) {
	states, err := s.scan("recorded", func(State) bool { return true })
	if err != nil {
		return nil, err
	}
	byItem := make(map[string][]State)
	for _, state := range states {
		byItem[state.WorkItemID] = append(byItem[state.WorkItemID], state)
	}
	items := make([]string, 0, len(byItem))
	for item := range byItem {
		items = append(items, item)
	}
	sort.Strings(items)
	prices := make([]ItemPrice, 0, len(items))
	for _, item := range items {
		prices = append(prices, s.price(item, byItem[item]))
	}
	return prices, nil
}

// price prices one item's runs, oldest first, so a breakdown reads as the
// history it is: the attempt that was rejected, then the one that was not.
func (s *Store) price(workItemID string, states []State) ItemPrice {
	sort.SliceStable(states, func(first, second int) bool {
		if !states[first].StartedAt.Equal(states[second].StartedAt) {
			return states[first].StartedAt.Before(states[second].StartedAt)
		}
		return states[first].RunID < states[second].RunID
	})
	price := ItemPrice{WorkItemID: workItemID}
	for _, state := range states {
		run := s.priceRun(state)
		price.Runs = append(price.Runs, run)
		// The split is merged before the run is judged priceable, which is what
		// keeps an unpriceable run's waits in the item's total: it spent no money
		// anybody can name and it still held the work up for as long as it did.
		price.Phases.Merge(run.Phases)
		if !run.Known() {
			price.UnknownRuns++
			continue
		}
		price.TotalUSD += run.CostUSD
		price.Tokens.Merge(run.Tokens)
	}
	return price
}

// priceRun prices one run from its own event log. It never fails: an event log
// that is gone or cannot be read is what an unknown price is, and reporting that
// as an error would lose the runs beside it that can be priced.
func (s *Store) priceRun(state State) RunPrice {
	run := RunPrice{
		RunID:       state.RunID,
		WorkItemID:  state.WorkItemID,
		Status:      state.Status,
		Outcome:     state.Outcome(),
		Phase:       state.Phase,
		StartedAt:   state.StartedAt,
		CompletedAt: state.CompletedAt,
		Integrated:  state.Integration != nil,
	}
	// What the run waited is read from its own state rather than from its event
	// log, so it is recorded before anything that can fail: a run whose log is
	// gone still says how long it was held up, because what held it up was never
	// written in the log to begin with.
	waits := Waits{
		UsageLimitSeconds:   state.UsageLimitPausedSeconds,
		OperatorHoldSeconds: state.OperatorHeldSeconds,
	}
	run.Phases.Waits = waits
	path, err := s.eventPath(state.RunID)
	if err != nil {
		run.Unknown = err.Error()
		return run
	}
	spend, tokens, err := scanEventSpend(path)
	if err != nil {
		run.Unknown = err.Error()
		return run
	}
	// A run that recorded a provider session made at least one invocation, so a
	// log with none in it is a log that lost them rather than a run that never
	// spent anything. A run with no session recorded never got as far as invoking
	// a provider, and costing nothing is the truth about it.
	if spend.Invocations() == 0 && (strings.TrimSpace(state.ProviderSessionID) != "" || strings.TrimSpace(state.ReviewSessionID) != "") {
		run.Unknown = "the run recorded a provider session but its event log holds no invocation to price"
		return run
	}
	spend.Waits = waits
	run.Phases = spend
	run.Tokens = tokens
	run.CostUSD = spend.TotalUSD()
	run.Invocations = spend.Invocations()
	return run
}

// scanEventCost sums what the provider reported for every invocation in one
// event log, without regard for which part of the run each served. It is what a
// log with no phases in it is priced by -- a branch review is one reviewer and
// nothing else -- and it is the split's own total said the short way.
func scanEventCost(path string) (float64, int, error) {
	spend, _, err := scanEventSpend(path)
	if err != nil {
		return 0, 0, err
	}
	return spend.TotalUSD(), spend.Invocations(), nil
}

// scanEventSpend splits what the provider reported for every invocation in one
// event log by the part of the run that invocation served. It reads the log a
// line at a time and decodes only the lines that can carry a cost or open a
// review, because an event log is mostly the invocation's own chatter and
// pricing a run must not mean decoding all of it.
//
// Which phase an invocation served is read from the invocation itself: every
// terminal names the role it was made as. A reviewer's is the review, a
// developer's is one of the developer's attempts, and a terminal naming anything
// else -- or naming nothing at all, at a schema whose terminals carry a role --
// is money this split will not place. It goes to Unattributed, where a reader
// can see it, rather than into the phase its neighbours suggest. That is the
// whole of why the role is written down: an invocation nobody anticipated,
// landing in a run's log without saying whose it is, used to be indistinguishable
// from a repair attempt and was charged as one.
//
// The developer's terminals still group into attempts by how each one ended. An
// attempt that reached a terminal of its own is over, so the next developer
// invocation is the next attempt; an attempt the provider refused for want of
// capacity or killed mid-flight is reissued in the same session, so the
// invocation after it is the same attempt still being made and its cost belongs
// to that attempt. The first attempt is the development and every attempt after
// it is a repair.
//
// What each terminal contributes is that invocation's own cost rather than the
// figure it carries. A provider asked to resume a session reports what the
// session has cost since it began, and a run's repair attempts resume the
// developer's session, so the reported figures rise across the log and adding
// them up charges the first attempt again to every attempt after it. Each is
// therefore taken as the increment over what its own session was last reported
// at, by the rule in OwnCostUSD, which leaves a log whose invocations each
// opened a session -- a development attempt and the review beside it -- priced
// exactly as it always was.
//
// The bound on that is the log: a session resumed by a later run is a beginning
// again here, because this scan sees one run's terminals. That is the right
// answer for what a run cost and the wrong one for what the session cost, and
// the two are different questions; the cost log is where a session is followed
// across the processes that resumed it.
//
// The token usage beside the money is read off the same terminals, and lands in
// the phase the money did as well as in the run's total, so a share can be taken
// over one phase or over all of them. A terminal that carried no usage object is
// counted apart rather than added in as zero, in the phase exactly as in the
// total -- an invocation nobody measured must not be able to drag a share down
// by exactly itself.
//
// Runs recorded before execution.TerminalRoleSchemaVersion had no role to omit,
// so their terminals are read the way they were written: the reviewer announces
// itself with a review.started and then makes exactly one invocation, so the
// terminal after that announcement is the reviewer's and every other terminal is
// a developer's. Those runs are priced exactly as they always were, because
// nothing can now be added to a log that is already closed, and the schema
// version is what keeps that reading confined to them. What made the inference
// sound for those runs is that the only two things that ever wrote into a run's
// log were the developer's attempts and the reviewer, each announcing itself;
// the role on the terminal is what stops that from having to stay true.
func scanEventSpend(path string) (PhaseSpend, TokenUsage, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return PhaseSpend{}, TokenUsage{}, errors.New("the event log is no longer recorded, so what it cost cannot be read")
	}
	if err != nil {
		return PhaseSpend{}, TokenUsage{}, fmt.Errorf("open event log to price the run: %w", err)
	}
	defer file.Close()

	var (
		spend  PhaseSpend
		tokens TokenUsage
		// costs turns each terminal's reported figure into what that invocation
		// cost. A run's repair attempts resume the developer's session, so a log
		// with three attempts in it carries the session's running total three
		// times; summing those charged the first attempt to the item again on
		// every later one. The developer's session and the reviewer's are
		// separate sessions and are tracked apart, which falls out of keying on
		// the session the terminal names.
		costs SessionCosts
		// reviewing is set by a review announcing itself and cleared by the single
		// invocation it makes rather than by the review closing, because a review
		// the provider never answered closes with nothing at all -- and a bracket
		// waiting for a close that never comes would charge the developer
		// invocations after it to the reviewer. It decides nothing for a terminal
		// that names its own role, and is kept for the logs that predate one.
		reviewing bool
		// attempt is which developer attempt the log has reached and ended says
		// the last one finished being made. Together they are what puts a reissued
		// invocation on the attempt it is reissuing rather than on a repair nobody
		// asked for.
		attempt int
		ended   bool
		// invoked is the phase the last terminal was placed in and session the
		// session it named, which is where a duplicate terminal after it belongs.
		invoked *PhaseCost
		session string
	)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedEventBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !carriesSpendEvidence(line) {
			continue
		}
		var priced pricedEvent
		if err := json.Unmarshal(line, &priced); err != nil {
			return PhaseSpend{}, TokenUsage{}, fmt.Errorf("decode event log to price the run: %w", err)
		}
		// The quick filter matches text anywhere in the line, so the decoded type
		// is what decides: an agent quoting an event name is not an invocation and
		// does not open a review either.
		if priced.Type == execution.EventReviewStarted {
			reviewing = true
			continue
		}
		// A second terminal of one invocation is more of that invocation's cost and
		// not another invocation, so it moves nothing the phase split counts: it is
		// added to the phase its first terminal was placed in, priced against the
		// session that terminal named.
		if priced.duplicateTerminal() {
			cost := costs.Own(priced.session(session), priced.Payload.TotalCostUSD)
			if invoked == nil {
				invoked = &spend.Unattributed
			}
			invoked.CostUSD += cost
			continue
		}
		if !priced.priced() {
			continue
		}
		usage := priced.tokens()
		tokens.Merge(usage)
		// An open bracket is spent by the first terminal that arrives whatever that
		// terminal turns out to be, because the review that opened it has by then
		// made the one invocation it makes.
		announced := reviewing
		reviewing = false
		session = priced.Payload.SessionID
		cost := costs.Own(session, priced.Payload.TotalCostUSD)
		switch priced.phase(announced) {
		case phaseReview:
			invoked = &spend.Review
		case phaseDevelopment:
			if ended {
				attempt++
			}
			if attempt == 0 {
				invoked = &spend.Development
			} else {
				invoked = &spend.Repair
			}
			ended = priced.Type == execution.EventRunCompleted
		default:
			invoked = &spend.Unattributed
		}
		invoked.add(cost, usage)
	}
	if err := scanner.Err(); err != nil {
		return PhaseSpend{}, TokenUsage{}, fmt.Errorf("read event log to price the run: %w", err)
	}
	return spend, tokens, nil
}

// pricedEvent is the little of an event pricing needs: which event it is, whose
// invocation it ended, what the provider said that invocation cost and read to
// serve it, and the schema it was written at -- which is what says whether a
// missing role is an omission or a field that did not exist yet.
type pricedEvent struct {
	SchemaVersion int                 `json:"schema_version"`
	Type          execution.EventType `json:"type"`
	// Timestamp is when the invocation ended, which is the day its money is
	// counted on by the readers that group spend by day.
	Timestamp time.Time `json:"timestamp"`
	Payload   struct {
		Role string `json:"role"`
		// SessionID is the provider session the invocation ran in, and it is what
		// says whether TotalCostUSD beside it is this invocation's cost or the
		// session's running total: a provider asked to resume reports the latter.
		// A terminal that named no session is priced at what it reported, which is
		// what an invocation with no session to have resumed cost.
		SessionID    string  `json:"session_id"`
		TotalCostUSD float64 `json:"total_cost_usd"`
		CostReported *bool   `json:"cost_reported"`
		// Usage is the provider's own usage object, recorded verbatim on every
		// terminal. It is a pointer so that a terminal carrying no usage at all is
		// distinguishable from one that reported zeros: the first is an invocation
		// nobody has a measurement for, the second is a measurement of nothing, and
		// a share that treated them alike would be wrong by the first.
		Usage *usageTokens `json:"usage"`
		// Anomaly marks the process output a backend records for a terminal that
		// arrived after the invocation's first, which carries a cost of its own.
		Anomaly string `json:"anomaly"`
	} `json:"payload"`
}

func (p *pricedEvent) UnmarshalJSON(data []byte) error {
	type plain pricedEvent
	if err := json.Unmarshal(data, (*plain)(p)); err != nil {
		return err
	}
	var presence struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return err
	}
	if p.Payload.CostReported == nil {
		raw, exists := presence.Payload["total_cost_usd"]
		reported := exists && string(raw) != "null"
		p.Payload.CostReported = &reported
	}
	return nil
}

// duplicateTerminal reports the record of a second terminal to one invocation.
// It is not a priced event, because it is not an invocation, but the provider
// charged for the turn behind it and the readers add that to the invocation it
// followed.
func (p pricedEvent) duplicateTerminal() bool {
	return p.Type == execution.EventProcessOutput && p.Payload.Anomaly == execution.DuplicateTerminalAnomaly
}

// session is the session a duplicate terminal's figure is a running total of.
// It names its own from the time the backend recorded one on it; before that
// it is the session of the terminal it followed, since both ended the same
// invocation.
func (p pricedEvent) session(previous string) string {
	if session := strings.TrimSpace(p.Payload.SessionID); session != "" {
		return session
	}
	return previous
}

// usageTokens is the part of the provider's usage object a share is computed
// from. The field names are the provider's rather than the harness's, because
// this decodes what the provider wrote: the backend records the object verbatim
// under "usage" on the terminal's payload, and everything beside these four --
// the nested cache_creation breakdown, the per-iteration array, the service tier
// -- is left where it is rather than descended into.
//
// That this is where the writer actually puts it is not something this file can
// state on its own, and a fixture written on this side would agree with itself
// whatever the writer did. TestATerminalCarriesTheProvidersUsageWhereThePriceReaderLooksForIt
// in internal/backend/claudecode is what holds the two together, from the side
// that writes.
type usageTokens struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	// CacheCreation is the one nested object that is descended into: the
	// provider's split of the write above by the lifetime it was written at,
	// which is what a cost split needs because the two lifetimes carry different
	// premiums. It is a pointer so a terminal recorded before the provider
	// reported the split is told apart from one that reported a split of nothing.
	CacheCreation *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// tokens is what this invocation read and wrote, or an invocation counted as
// unmeasured where its terminal carried no usage object.
func (p pricedEvent) tokens() TokenUsage {
	usage := p.Payload.Usage
	if usage == nil {
		return TokenUsage{Unreported: 1, NoCost: p.noCost(), Priced: 1 - p.noCost()}
	}
	tokens := TokenUsage{
		InputTokens:         usage.InputTokens,
		CacheReadTokens:     usage.CacheReadTokens,
		CacheCreationTokens: usage.CacheCreationTokens,
		OutputTokens:        usage.OutputTokens,
		Measured:            1,
		NoCost:              p.noCost(),
		Priced:              1 - p.noCost(),
	}
	if usage.CacheCreation != nil {
		tokens.CacheWrite5mTokens = usage.CacheCreation.Ephemeral5m
		tokens.CacheWrite1hTokens = usage.CacheCreation.Ephemeral1h
	}
	return tokens
}

// noCost uses the explicit adapter flag; older events retain their recorded meaning.
func (p pricedEvent) noCost() int {
	if p.Payload.CostReported != nil && !*p.Payload.CostReported {
		return 1
	}
	return 0
}

// CostText describes reported dollars and tokens without treating missing prices as free.
func (t TokenUsage) CostText(cost float64) string {
	if t.NoCost == 0 {
		return fmt.Sprintf("$%.2f", cost)
	}
	turns := "turns"
	if t.NoCost == 1 {
		turns = "turn"
	}
	tokens := fmt.Sprintf("%d input tokens (%d cached), %d output tokens", t.InputTotal(), t.CacheReadTokens, t.OutputTokens)
	if t.Measured == 0 {
		tokens = "token usage not reported"
	}
	if t.Unreported > 0 && t.Measured > 0 {
		tokens += fmt.Sprintf("; usage not reported for %d turns", t.Unreported)
	}
	if t.Priced == 0 && cost == 0 {
		return tokens + fmt.Sprintf("; no cost reported for %d %s", t.NoCost, turns)
	}
	return fmt.Sprintf("$%.2f reported; %s; no cost reported for %d %s", cost, tokens, t.NoCost, turns)
}

// role is whose invocation this terminal ended, by the rule phase applies: a
// terminal names its own role from TerminalRoleSchemaVersion on, and one
// recorded before that is read the way it was written, with the review bracket
// deciding between the only two roles that ever wrote into a run's log. A
// terminal that could have named itself and did not is nobody's, which is what
// the empty role says.
func (p pricedEvent) role(announced bool) domain.AgentRole {
	named := domain.AgentRole(strings.TrimSpace(p.Payload.Role))
	if named != "" || p.SchemaVersion >= execution.TerminalRoleSchemaVersion {
		return named
	}
	if announced {
		return domain.RoleReviewer
	}
	return domain.RoleDeveloper
}

// invocationPhase is which part of a run one priced invocation served. Its zero
// value is the unattributed one deliberately: a phase this reaches without
// deciding on is money placed nowhere rather than money placed by default.
type invocationPhase int

const (
	phaseUnattributed invocationPhase = iota
	phaseDevelopment
	phaseReview
)

// phase says which part of the run this invocation served, from the role it
// recorded for itself.
//
// A terminal that named no role is the case the schema decides. Recorded at a
// version whose terminals carry one, it is an invocation that failed to say
// whose it was -- a summarizer, a shadow reviewer, anything a later feature
// invokes into a run -- and it is placed nowhere, which is the whole point:
// there is no phase an unannounced invocation falls into by default. Recorded
// before that version, the field did not exist to omit, so the log is read the
// way it was written, and announced -- whether a review had opened a bracket in
// front of it -- is what decides. That fallback reaches nothing written since,
// so it can never place a terminal that could have named itself and did not.
func (p pricedEvent) phase(announced bool) invocationPhase {
	switch p.role(announced) {
	case domain.RoleDeveloper:
		return phaseDevelopment
	case domain.RoleReviewer:
		return phaseReview
	default:
		return phaseUnattributed
	}
}

func (p pricedEvent) priced() bool {
	for _, candidate := range pricedEvents {
		if p.Type == candidate {
			return true
		}
	}
	return false
}

// carriesSpendEvidence is the cheap test that decides whether a line is worth
// decoding. It over-matches deliberately: what it must never do is skip a line
// that carries a cost or opens a review, and the decoded type rejects everything
// else. A duplicate terminal carries a cost under an event type that everything
// else is recorded under too, so it is matched by its anomaly instead.
func carriesSpendEvidence(line []byte) bool {
	for _, candidate := range spendEvidenceEvents {
		if bytes.Contains(line, []byte(`"`+string(candidate)+`"`)) {
			return true
		}
	}
	return bytes.Contains(line, []byte(`"`+execution.DuplicateTerminalAnomaly+`"`))
}

func exchangeTokens(round exchange.Round) TokenUsage {
	if round.CostReported == nil && len(round.Usage) == 0 {
		return TokenUsage{Priced: 1}
	}
	var event pricedEvent
	event.Payload.CostReported = round.CostReported
	if len(round.Usage) > 0 {
		_ = json.Unmarshal(round.Usage, &event.Payload.Usage)
	}
	return event.tokens()
}
