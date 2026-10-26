// Package pricing is the one place a provider call is given a cost the
// provider did not state. It implements the pricing half of the provider usage
// and cost design (docs/designs/provider-usage-and-cost.md): it takes what one
// call used and returns one of four kinds of figure, and no price, and no
// condition on a model's name to find one, appears anywhere else in the code.
//
// The prices are data, in rates.json beside this file. A call that matches no
// entry, or matches one whose tier its own evidence cannot decide, is unknown
// and says why; it is never priced by a guessed rate or a blended one.
package pricing

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/usage"
)

// Kind is which of the four kinds a cost figure is. No total adds two kinds
// together.
type Kind string

const (
	// KindReported is the provider's own figure for the call. It is what the
	// provider said, not an invoice, and it is never recalculated.
	KindReported Kind = "reported"
	// KindEstimated is computed here from the counters and a rate entry, for a
	// call paid per token at API prices: money spent, worked out rather than
	// stated.
	KindEstimated Kind = "estimated"
	// KindComparison is what the same use would cost at the provider's API
	// prices, for a call paid for by a subscription or purchased credits. It is
	// not money spent.
	KindComparison Kind = "comparison"
	// KindUnknown is neither reported nor computable, and says why.
	KindUnknown Kind = "unknown"
)

// Kinds lists the four in the order every total shows them.
func Kinds() []Kind { return []Kind{KindReported, KindEstimated, KindComparison, KindUnknown} }

// Valid reports one of the four.
func (k Kind) Valid() bool {
	for _, candidate := range Kinds() {
		if k == candidate {
			return true
		}
	}
	return false
}

// Call is what one provider call is priced from.
type Call struct {
	Provider domain.Backend
	// Model is the model that answered where the provider said, and the model
	// the harness asked for where it did not. Rate entries are matched on it
	// exactly.
	Model   string
	Billing usage.BillingMode
	// At is when the call was made, which picks the rate in force then.
	At       time.Time
	Counters usage.Counters
	// Tier is the processing tier the provider reported, empty where none.
	Tier string
	// SingleRequest says the call was one request to the model, so its input is
	// the request's input. A call that may have been several requests -- an
	// agent turn -- cannot be placed above a per-request threshold by its total.
	SingleRequest bool
	// ReportedUSD is the provider's own figure for the call, and nil where it
	// stated none.
	ReportedUSD *float64
}

// Figure is one call's cost, of exactly one kind.
type Figure struct {
	Kind Kind `json:"kind"`
	// USD is the figure in dollars, on every kind but unknown.
	USD float64 `json:"usd"`
	// Credits is what a call paid for with purchased credits came to in
	// credits, where a credits rate entry matched. It sits beside a comparison
	// figure and is never added to dollars.
	Credits *float64 `json:"credits,omitempty"`
	// Entry and Schedule name the rate entry and the version of the rate data
	// an estimate or a comparison was computed from; CreditsEntry the entry the
	// credits were.
	Entry        string `json:"rate_entry,omitempty"`
	CreditsEntry string `json:"credits_rate_entry,omitempty"`
	Schedule     string `json:"rate_version,omitempty"`
	// Tier is the processing tier the figure was priced at, and LongContext
	// says the long-context rate applied.
	Tier        string `json:"tier,omitempty"`
	LongContext bool   `json:"long_context,omitempty"`
	// Unknown says why there is no figure.
	Unknown string `json:"unknown,omitempty"`
	// Notes says what an estimate rests on that a reader should know, such as a
	// tier taken as the provider's default because it reported none.
	Notes []string `json:"notes,omitempty"`
}

// Service prices calls against one schedule.
type Service struct {
	Schedule Schedule
}

// New is a service over the rate data this build ships.
func New() (Service, error) {
	schedule, err := Shipped()
	if err != nil {
		return Service{}, err
	}
	return Service{Schedule: schedule}, nil
}

// Price is one call's figure. The provider's own figure takes precedence over
// anything computed here; then the billing mode decides whether a computed
// figure is money spent, a comparison, or nothing anybody can say.
func (s Service) Price(call Call) Figure {
	if call.ReportedUSD != nil {
		return Figure{Kind: KindReported, USD: *call.ReportedUSD}
	}
	billing := call.Billing.OrNotRecorded()
	if billing == usage.BillingNotRecorded {
		return unknown("the account's billing mode is not recorded, so a figure at API prices could be money spent or only a comparison")
	}
	if !call.Counters.Any() {
		return unknown("the provider reported neither a cost nor any token usage for the call")
	}
	model := strings.TrimSpace(call.Model)
	if model == "" {
		return unknown("the call names no model to find a rate for")
	}
	api := s.price(call, model, usage.BillingAPIKey)
	if api.Kind == KindUnknown {
		if billing == usage.BillingCredits {
			// The credits figure is not dollars and is still worth saying where
			// it is known, beside the reason there is no dollar figure.
			if credits := s.price(call, model, usage.BillingCredits); credits.Kind != KindUnknown {
				api.Credits, api.CreditsEntry = &credits.USD, credits.Entry
				if api.Schedule == "" {
					api.Schedule = credits.Schedule
				}
			}
		}
		return api
	}
	switch billing {
	case usage.BillingAPIKey:
		api.Kind = KindEstimated
	case usage.BillingSubscription:
		api.Kind = KindComparison
	case usage.BillingCredits:
		api.Kind = KindComparison
		if credits := s.price(call, model, usage.BillingCredits); credits.Kind != KindUnknown {
			api.Credits, api.CreditsEntry = &credits.USD, credits.Entry
		} else {
			api.Notes = append(api.Notes, "credits used are not known: "+credits.Unknown)
		}
	}
	return api
}

// price computes a figure from the entry for one billing mode. It returns an
// unknown figure where no entry matches or the call's evidence cannot decide
// which rate applies; on success the kind is left for Price to decide.
func (s Service) price(call Call, model string, billing usage.BillingMode) Figure {
	entry, notes, reason := s.match(call, model, billing)
	if entry == nil {
		figure := unknown(reason)
		figure.Schedule = s.Schedule.Version
		return figure
	}
	prices := entry.Prices
	long := false
	if entry.LongContext != nil {
		input := call.Counters.Input()
		switch {
		case !input.Measured:
			return Figure{Kind: KindUnknown, Schedule: s.Schedule.Version, Unknown: fmt.Sprintf("rate entry %s charges more above %d input tokens a request, and the call's own input was not fully reported, so which rate applies is unknown", entry.ID, entry.LongContext.AboveInputTokens)}
		case input.Tokens > entry.LongContext.AboveInputTokens && !call.SingleRequest:
			return Figure{Kind: KindUnknown, Schedule: s.Schedule.Version, Unknown: fmt.Sprintf("rate entry %s charges more above %d input tokens a request, and the call read %d input tokens in what may have been several requests, so which rate each request was charged is unknown", entry.ID, entry.LongContext.AboveInputTokens, input.Tokens)}
		case input.Tokens > entry.LongContext.AboveInputTokens:
			prices, long = entry.LongContext.Prices, true
		}
	}
	total := 0.0
	for _, counter := range usage.Every() {
		amount, ok, missing := charge(prices, counter, call.Counters)
		if !ok {
			return Figure{Kind: KindUnknown, Schedule: s.Schedule.Version, Unknown: fmt.Sprintf("rate entry %s prices %s, and the provider did not report %s for the call", entry.ID, counter.Words(), missing.Words())}
		}
		total += amount
	}
	return Figure{Kind: KindEstimated, USD: total, Entry: entry.ID, Schedule: s.Schedule.Version, Tier: entry.Tier, LongContext: long, Notes: notes}
}

// charge is what one counter costs under the prices. It reports false, with the
// counter that was missing, where a counter with a price was not reported.
func charge(prices Prices, counter usage.Counter, counters usage.Counters) (float64, bool, usage.Counter) {
	price := prices.get(counter)
	switch {
	case price == nil:
		return 0, false, counter
	case price.Rule == RuleNoCharge || price.Rule == RuleNotSeparate:
		return 0, true, ""
	}
	count := counters.Get(counter)
	if !count.Measured {
		return 0, false, counter
	}
	rate := price.PerMillion
	if price.Rule == RuleChargedAs {
		rate = prices.get(price.As).PerMillion
	}
	return float64(count.Tokens) * *rate / 1e6, true, ""
}

// match finds the one entry for the call. A tier the provider reported is
// matched exactly; where it reported none, the entry marked as the provider's
// default tier is used, and the figure says so.
func (s Service) match(call Call, model string, billing usage.BillingMode) (*Entry, []string, string) {
	var candidates []Entry
	for _, entry := range s.Schedule.Entries {
		if entry.Provider == call.Provider && entry.Model == model && entry.Billing == billing && entry.covers(call.At) {
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		return nil, nil, fmt.Sprintf("no rate entry in rate data %s covers %s model %s under %s on %s", s.Schedule.Version, call.Provider, model, billing.Words(), call.At.UTC().Format(dateLayout))
	}
	tier := strings.TrimSpace(call.Tier)
	for index := range candidates {
		entry := candidates[index]
		if tier != "" && entry.Tier == tier {
			return &entry, nil, ""
		}
		if tier == "" && entry.DefaultTier {
			return &entry, []string{fmt.Sprintf("the provider reported no processing tier; priced at %s, the tier it applies to a request naming none, which is every request the harness makes", entry.Tier)}, ""
		}
	}
	if tier == "" {
		return nil, nil, fmt.Sprintf("the provider reported no processing tier for the call, and no rate entry for %s model %s names the tier it applies by default", call.Provider, model)
	}
	return nil, nil, fmt.Sprintf("no rate entry for %s model %s covers the %s processing tier the provider reported", call.Provider, model, tier)
}

func unknown(reason string) Figure {
	return Figure{Kind: KindUnknown, Unknown: reason}
}
