package pricing

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/usage"
)

// shippedRates is the rate data this build prices with. It is a file beside
// this one rather than constants in code, so a price is changed by a reviewed
// change to data and nowhere else; see rates.json and README.md beside it.
//
//go:embed rates.json
var shippedRates []byte

// dateLayout is how every date in the rate data is written.
const dateLayout = "2006-01-02"

// Unit is what an entry's prices are in.
type Unit string

const (
	// UnitUSD is US dollars per million tokens, which is what API prices are.
	UnitUSD Unit = "usd"
	// UnitCredits is a provider's purchased credits per million tokens. Credits
	// are never added to dollars.
	UnitCredits Unit = "credits"
)

// Rule says how a counter is charged where it has no price of its own.
type Rule string

const (
	// RuleNoCharge is a counter the provider states it charges nothing for.
	RuleNoCharge Rule = "no_charge"
	// RuleChargedAs is a counter the provider charges at another counter's
	// price, named in As.
	RuleChargedAs Rule = "charged_as"
	// RuleNotSeparate is a counter the provider does not report apart: its
	// tokens are inside another counter the adapter reports, so it needs no
	// reading and adds nothing of its own.
	RuleNotSeparate Rule = "not_separate"
)

// Price is how one counter is charged: a rate per million tokens, or the
// provider's stated rule. Exactly one of the two.
type Price struct {
	PerMillion *float64      `json:"per_million,omitempty"`
	Rule       Rule          `json:"rule,omitempty"`
	As         usage.Counter `json:"as,omitempty"`
}

// Prices is a price for each of the five counters. Every counter has one: a
// counter the provider does not charge separately says how it is charged
// rather than leaving the price missing.
type Prices struct {
	FreshInput  *Price `json:"fresh_input"`
	CachedInput *Price `json:"cached_input"`
	CacheWrite  *Price `json:"cache_write"`
	Output      *Price `json:"output"`
	Reasoning   *Price `json:"reasoning"`
}

func (p Prices) get(counter usage.Counter) *Price {
	switch counter {
	case usage.FreshInput:
		return p.FreshInput
	case usage.CachedInput:
		return p.CachedInput
	case usage.CacheWrite:
		return p.CacheWrite
	case usage.Output:
		return p.Output
	case usage.Reasoning:
		return p.Reasoning
	}
	return nil
}

// LongContext is the different rate a provider charges a request whose own
// input is above a set number of tokens.
type LongContext struct {
	AboveInputTokens int64  `json:"above_input_tokens"`
	Prices           Prices `json:"prices"`
}

// Entry is one rate: what one provider charges for one model under one
// billing mode and processing tier, from one date, and where that was read.
type Entry struct {
	ID       string            `json:"id"`
	Provider domain.Backend    `json:"provider"`
	Model    string            `json:"model"`
	Billing  usage.BillingMode `json:"billing_mode"`
	Tier     string            `json:"tier"`
	// DefaultTier says this is the tier the provider applies to a request that
	// names none. The harness names no tier on any request, so a call whose
	// provider reported no tier was served at this one.
	DefaultTier bool `json:"default_tier,omitempty"`
	// EffectiveFrom is the first day the rate applies and EffectiveUntil the
	// first day it no longer does, empty while it still applies.
	EffectiveFrom  string       `json:"effective_from"`
	EffectiveUntil string       `json:"effective_until,omitempty"`
	Unit           Unit         `json:"unit"`
	Prices         Prices       `json:"prices"`
	LongContext    *LongContext `json:"long_context,omitempty"`
	// Source is the page the price was read from, and Checked the day somebody
	// read it there.
	Source  string `json:"source"`
	Checked string `json:"checked"`
}

func (e Entry) from() time.Time {
	day, _ := time.Parse(dateLayout, e.EffectiveFrom)
	return day
}

// until is the first instant the entry no longer applies, and the zero time
// where it still does.
func (e Entry) until() time.Time {
	if e.EffectiveUntil == "" {
		return time.Time{}
	}
	day, _ := time.Parse(dateLayout, e.EffectiveUntil)
	return day
}

func (e Entry) covers(at time.Time) bool {
	day := at.UTC()
	if day.Before(e.from()) {
		return false
	}
	until := e.until()
	return until.IsZero() || day.Before(until)
}

func (e Entry) key() string {
	return strings.Join([]string{string(e.Provider), e.Model, string(e.Billing), e.Tier}, "\x00")
}

// Schedule is the whole rate data: a version that changes whenever any entry
// does, and the entries.
type Schedule struct {
	Version string  `json:"version"`
	Entries []Entry `json:"entries"`
}

// Shipped is the rate data this build carries, validated.
func Shipped() (Schedule, error) {
	return Parse(shippedRates)
}

// Parse reads rate data and refuses it whole if any entry is wrong, because a
// schedule with one bad entry is one whose estimates nobody can trust.
func Parse(data []byte) (Schedule, error) {
	var schedule Schedule
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schedule); err != nil {
		return Schedule{}, fmt.Errorf("read rate data: %w", err)
	}
	if err := schedule.Validate(); err != nil {
		return Schedule{}, err
	}
	return schedule, nil
}

// Validate reports every problem in the rate data at once.
func (s Schedule) Validate() error {
	var problems []error
	if strings.TrimSpace(s.Version) == "" {
		problems = append(problems, errors.New("the rate data names no version"))
	}
	ids := make(map[string]bool, len(s.Entries))
	byKey := make(map[string][]Entry)
	defaults := make(map[string]string)
	for _, entry := range s.Entries {
		if err := entry.validate(); err != nil {
			problems = append(problems, err)
			continue
		}
		if ids[entry.ID] {
			problems = append(problems, fmt.Errorf("rate entry %s is named twice", entry.ID))
		}
		ids[entry.ID] = true
		byKey[entry.key()] = append(byKey[entry.key()], entry)
		if entry.DefaultTier {
			group := strings.Join([]string{string(entry.Provider), entry.Model, string(entry.Billing), entry.EffectiveFrom}, "\x00")
			if other, seen := defaults[group]; seen {
				problems = append(problems, fmt.Errorf("rate entries %s and %s are both the default tier from %s", other, entry.ID, entry.EffectiveFrom))
			}
			defaults[group] = entry.ID
		}
	}
	for _, entries := range byKey {
		sort.Slice(entries, func(i, j int) bool { return entries[i].from().Before(entries[j].from()) })
		for index := 1; index < len(entries); index++ {
			before, after := entries[index-1], entries[index]
			if until := before.until(); until.IsZero() || after.from().Before(until) {
				problems = append(problems, fmt.Errorf("rate entries %s and %s overlap in time for %s %s, %s, tier %s", before.ID, after.ID, after.Provider, after.Model, after.Billing, after.Tier))
			}
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid rate data: %w", err)
	}
	return nil
}

func (e Entry) validate() error {
	var problems []error
	name := e.ID
	if strings.TrimSpace(name) == "" {
		name = "(unnamed)"
		problems = append(problems, errors.New("names no id"))
	}
	if !e.Provider.Valid() {
		problems = append(problems, fmt.Errorf("provider %q is not a provider identifier", e.Provider))
	}
	if strings.TrimSpace(e.Model) == "" {
		problems = append(problems, errors.New("names no model"))
	}
	switch e.Billing {
	case usage.BillingAPIKey, usage.BillingCredits:
	default:
		// A subscription has no per-token price, and a mode nobody recorded is
		// not one a provider publishes a price for.
		problems = append(problems, fmt.Errorf("billing mode %q is not one a provider prices per token; it must be %s or %s", e.Billing, usage.BillingAPIKey, usage.BillingCredits))
	}
	if (e.Billing == usage.BillingCredits) != (e.Unit == UnitCredits) {
		problems = append(problems, fmt.Errorf("unit %q does not match billing mode %q: credits are priced in credits and API keys in usd", e.Unit, e.Billing))
	}
	if strings.TrimSpace(e.Tier) == "" {
		problems = append(problems, errors.New("names no processing tier"))
	}
	from, err := time.Parse(dateLayout, e.EffectiveFrom)
	if err != nil {
		problems = append(problems, fmt.Errorf("effective_from %q is not a date", e.EffectiveFrom))
	}
	if e.EffectiveUntil != "" {
		until, err := time.Parse(dateLayout, e.EffectiveUntil)
		if err != nil {
			problems = append(problems, fmt.Errorf("effective_until %q is not a date", e.EffectiveUntil))
		} else if !until.After(from) {
			problems = append(problems, errors.New("effective_until is not after effective_from"))
		}
	}
	if parsed, err := url.Parse(e.Source); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		problems = append(problems, fmt.Errorf("source %q is not the https page the price was read from", e.Source))
	}
	if _, err := time.Parse(dateLayout, e.Checked); err != nil {
		problems = append(problems, fmt.Errorf("checked %q is not the date the source was read", e.Checked))
	}
	problems = append(problems, e.Prices.validate("prices"))
	if e.LongContext != nil {
		if e.LongContext.AboveInputTokens <= 0 {
			problems = append(problems, errors.New("long_context names no input size above which it applies"))
		}
		problems = append(problems, e.LongContext.Prices.validate("long_context prices"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("rate entry %s: %w", name, err)
	}
	return nil
}

func (p Prices) validate(where string) error {
	var problems []error
	for _, counter := range usage.Every() {
		price := p.get(counter)
		if price == nil {
			problems = append(problems, fmt.Errorf("%s has no price or rule for %s", where, counter))
			continue
		}
		switch {
		case price.PerMillion != nil && price.Rule != "":
			problems = append(problems, fmt.Errorf("%s gives %s both a price and a rule", where, counter))
		case price.PerMillion != nil:
			if *price.PerMillion < 0 {
				problems = append(problems, fmt.Errorf("%s prices %s below nothing", where, counter))
			}
			if price.As != "" {
				problems = append(problems, fmt.Errorf("%s names a counter for %s to be charged as without the charged_as rule", where, counter))
			}
		case price.Rule == RuleNoCharge || price.Rule == RuleNotSeparate:
			if price.As != "" {
				problems = append(problems, fmt.Errorf("%s names a counter for %s to be charged as under rule %s", where, counter, price.Rule))
			}
		case price.Rule == RuleChargedAs:
			target := p.get(price.As)
			if !price.As.Valid() || price.As == counter || target == nil || target.PerMillion == nil {
				problems = append(problems, fmt.Errorf("%s charges %s as %q, which is not another counter with a price", where, counter, price.As))
			}
		default:
			problems = append(problems, fmt.Errorf("%s has neither a price nor a known rule for %s", where, counter))
		}
	}
	return errors.Join(problems...)
}
