// Package usage is the one shape every provider's token usage is recorded in,
// and the one piece of code that turns what a provider reported into what one
// call used. It implements the usage half of the provider usage and cost design
// (docs/designs/provider-usage-and-cost.md).
//
// Reading a provider's own events stays in its adapter. What the adapter hands
// over is a Reading: five counters that never overlap, each a measured number, a
// measured zero, or absent, marked as either a running session total or one
// call's own amount. Everything after that is here, so two adapters cannot
// disagree about what a running total, a reset, or a repeated final event means.
package usage

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Count is one counter: a number the provider reported, which may be zero, or
// nothing at all because it reported none. The two are opposite facts to
// anything adding tokens up, so the zero value is absent and a measured zero
// has to be said.
//
// It encodes as a bare number and is left out of an encoding where absent
// (every field holding one is tagged omitzero), so a record read back says
// exactly what was measured.
type Count struct {
	Tokens   int64
	Measured bool
}

// Measured is a counter the provider reported.
func Measured(tokens int64) Count { return Count{Tokens: tokens, Measured: true} }

// Of is a counter from a provider field that may have been missing.
func Of(tokens *int64) Count {
	if tokens == nil {
		return Count{}
	}
	return Measured(*tokens)
}

// IsZero reports an absent counter, which is what omitzero leaves out. A
// measured zero is not zero in this sense.
func (c Count) IsZero() bool { return !c.Measured }

func (c Count) MarshalJSON() ([]byte, error) {
	if !c.Measured {
		return []byte("null"), nil
	}
	return json.Marshal(c.Tokens)
}

func (c *Count) UnmarshalJSON(data []byte) error {
	if strings.TrimSpace(string(data)) == "null" {
		*c = Count{}
		return nil
	}
	var tokens int64
	if err := json.Unmarshal(data, &tokens); err != nil {
		return err
	}
	*c = Measured(tokens)
	return nil
}

// plus adds two counters. A sum with an absent side is the other side, because
// adding nothing measured to a measurement leaves the measurement; it is the
// aggregate's job, not the counter's, to say how many calls were unmeasured.
func (c Count) plus(other Count) Count {
	switch {
	case !c.Measured:
		return other
	case !other.Measured:
		return c
	}
	return Measured(c.Tokens + other.Tokens)
}

// Counters are the five things a provider call is billed for. They never
// overlap: fresh input excludes cached input and cache writes, and output
// excludes reasoning, so adding all five counts every token once. An adapter
// whose provider reports one inside another takes it out before handing them
// over, and says so in Reading.Adjustments.
type Counters struct {
	FreshInput  Count `json:"fresh_input,omitzero"`
	CachedInput Count `json:"cached_input,omitzero"`
	CacheWrite  Count `json:"cache_write,omitzero"`
	Output      Count `json:"output,omitzero"`
	Reasoning   Count `json:"reasoning,omitzero"`
}

// Counter names one of the five, as rate data and records name them.
type Counter string

const (
	FreshInput  Counter = "fresh_input"
	CachedInput Counter = "cached_input"
	CacheWrite  Counter = "cache_write"
	Output      Counter = "output"
	Reasoning   Counter = "reasoning"
)

// Every is the five counters in the order they are always listed.
func Every() []Counter {
	return []Counter{FreshInput, CachedInput, CacheWrite, Output, Reasoning}
}

// Valid reports one of the five.
func (c Counter) Valid() bool {
	for _, candidate := range Every() {
		if c == candidate {
			return true
		}
	}
	return false
}

// Words is the counter as a person reads it.
func (c Counter) Words() string {
	switch c {
	case FreshInput:
		return "fresh input"
	case CachedInput:
		return "cached input read"
	case CacheWrite:
		return "cache writes"
	case Output:
		return "output"
	case Reasoning:
		return "reasoning"
	}
	return string(c)
}

// Get is one counter by name.
func (c Counters) Get(counter Counter) Count {
	switch counter {
	case FreshInput:
		return c.FreshInput
	case CachedInput:
		return c.CachedInput
	case CacheWrite:
		return c.CacheWrite
	case Output:
		return c.Output
	case Reasoning:
		return c.Reasoning
	}
	return Count{}
}

func (c *Counters) set(counter Counter, value Count) {
	switch counter {
	case FreshInput:
		c.FreshInput = value
	case CachedInput:
		c.CachedInput = value
	case CacheWrite:
		c.CacheWrite = value
	case Output:
		c.Output = value
	case Reasoning:
		c.Reasoning = value
	}
}

// Any reports at least one measured counter. A record with none measured is a
// call nobody has a reading for, which is never the same as a call that used
// nothing.
func (c Counters) Any() bool {
	for _, counter := range Every() {
		if c.Get(counter).Measured {
			return true
		}
	}
	return false
}

// Plus adds another call's counters into these, counter by counter.
func (c Counters) Plus(other Counters) Counters {
	var sum Counters
	for _, counter := range Every() {
		sum.set(counter, c.Get(counter).plus(other.Get(counter)))
	}
	return sum
}

// Input is everything one call sent as input: fresh, cached, and written to the
// cache. It is what a per-request rate threshold is measured against, and it is
// absent unless all three were measured, because a threshold decided from part
// of an input is decided by guess.
func (c Counters) Input() Count {
	if !c.FreshInput.Measured || !c.CachedInput.Measured || !c.CacheWrite.Measured {
		return Count{}
	}
	return Measured(c.FreshInput.Tokens + c.CachedInput.Tokens + c.CacheWrite.Tokens)
}

// BillingMode is how an account is paid for. It is read from the account's
// configuration and never inferred from a model's name, because the same model
// is paid for by an API key on one account and by a subscription on another.
type BillingMode string

const (
	// BillingAPIKey is an account billed per token at the provider's API prices.
	// An estimate for it is money spent.
	BillingAPIKey BillingMode = "api_key"
	// BillingSubscription is an account paid for by a subscription. What the
	// same use would cost at API prices is a comparison, never money spent.
	BillingSubscription BillingMode = "subscription"
	// BillingCredits is an account paid for with purchased credits, which are
	// priced in credits rather than dollars.
	BillingCredits BillingMode = "credits"
	// BillingNotRecorded is an account whose configuration does not say.
	BillingNotRecorded BillingMode = "not_recorded"
)

// BillingModes lists every mode, in the order a refusal names them.
func BillingModes() []BillingMode {
	return []BillingMode{BillingAPIKey, BillingSubscription, BillingCredits, BillingNotRecorded}
}

// OrNotRecorded is the mode, with an unstated one said as not recorded.
func (m BillingMode) OrNotRecorded() BillingMode {
	if strings.TrimSpace(string(m)) == "" {
		return BillingNotRecorded
	}
	return m
}

// Valid reports a mode the harness recognizes. Empty is valid and means not
// recorded.
func (m BillingMode) Valid() bool {
	for _, candidate := range BillingModes() {
		if m.OrNotRecorded() == candidate {
			return true
		}
	}
	return false
}

// Words is the mode as a person reads it.
func (m BillingMode) Words() string {
	switch m.OrNotRecorded() {
	case BillingAPIKey:
		return "API key"
	case BillingSubscription:
		return "subscription"
	case BillingCredits:
		return "purchased credits"
	}
	return "billing not recorded"
}

// ParseBillingMode reads a mode from configuration.
func ParseBillingMode(text string) (BillingMode, error) {
	mode := BillingMode(strings.TrimSpace(text))
	if !mode.Valid() {
		names := make([]string, 0, len(BillingModes()))
		for _, candidate := range BillingModes() {
			names = append(names, string(candidate))
		}
		return "", fmt.Errorf("billing mode %q must be one of %s", text, strings.Join(names, ", "))
	}
	return mode.OrNotRecorded(), nil
}
