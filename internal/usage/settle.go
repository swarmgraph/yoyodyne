package usage

import "strings"

// Reading is what an adapter read from its provider about one call: the five
// counters, and whether they are a running total for the session or this
// call's own amount. It carries the provider's identity for the event the
// counters came from, so a final event the provider repeats is counted once.
type Reading struct {
	// Session is the provider session the reading belongs to, and empty where
	// the provider named none.
	Session string
	// EventID is the provider's own identity for the event, and empty where it
	// gives none.
	EventID string
	// Running says the counters are the session's running total rather than
	// what this call used.
	Running  bool
	Counters Counters
	// Tier is the processing tier the provider reported serving the call at,
	// and empty where it reported none.
	Tier string
	// Adjustments says what the adapter took out of a provider's figure to keep
	// the counters from overlapping, in words.
	Adjustments []string
}

// Collector keeps the readings one call produced, in order, so the call is
// settled from the last of them. A final event the provider repeats with the
// same event identity is seen once; a running total read twice is the later of
// the two; and two distinct per-call readings within one call are both the
// call's and are added together.
type Collector struct {
	seen     map[string]bool
	reading  *Reading
	repeated int
}

// Observe takes one reading. It reports false for a repeat of an event it has
// already taken.
func (c *Collector) Observe(reading Reading) bool {
	if id := strings.TrimSpace(reading.EventID); id != "" {
		if c.seen == nil {
			c.seen = make(map[string]bool)
		}
		if c.seen[id] {
			c.repeated++
			return false
		}
		c.seen[id] = true
	}
	if c.reading == nil || reading.Running || c.reading.Running {
		copied := reading
		copied.Adjustments = append([]string(nil), reading.Adjustments...)
		c.reading = &copied
		return true
	}
	c.reading.Counters = c.reading.Counters.Plus(reading.Counters)
	c.reading.EventID = reading.EventID
	if reading.Tier != "" {
		c.reading.Tier = reading.Tier
	}
	c.reading.Adjustments = mergeWords(c.reading.Adjustments, reading.Adjustments)
	return true
}

// Reading is what the call reported, or nil where it reported nothing.
func (c *Collector) Reading() *Reading {
	if c == nil || c.reading == nil {
		return nil
	}
	copied := *c.reading
	return &copied
}

// Repeated is how many repeated events were set aside.
func (c *Collector) Repeated() int {
	if c == nil {
		return 0
	}
	return c.repeated
}

// Settled is one call's usage, ready to record: what the call itself used, and
// what was learned settling it.
type Settled struct {
	// Counters is this call's own usage. Every counter is absent on a call the
	// provider reported nothing for.
	Counters Counters `json:"counters"`
	// Running is the running total the call was settled from, kept so the
	// session's next call is settled against it. It is absent for a per-call
	// reading.
	Running *Counters `json:"running,omitempty"`
	// Reset says the running total fell below the session's previous one, so
	// the reading was taken whole as this call's amount.
	Reset bool `json:"reset,omitempty"`
	// Partial says the call was cut off before the provider ended it, and the
	// counters are what it reported before the cut.
	Partial bool `json:"partial,omitempty"`
	// Repeated counts final events the provider repeated and that were counted
	// once.
	Repeated int `json:"repeated,omitempty"`
	// Tier is the processing tier the provider reported.
	Tier string `json:"tier,omitempty"`
	// Adjustments is what the adapter took out of a provider figure.
	Adjustments []string `json:"adjustments,omitempty"`
}

// Settle turns what a call reported into what it used. It is the only code
// that does: an adapter hands its readings over and never subtracts one total
// from another itself.
//
// previous is the last running total recorded for the same session, and nil
// where none was. The rules are the design's:
//
//   - a running total becomes the difference from the previous one; a fresh
//     session, with no previous total, starts from zero;
//   - a running total below the previous one on any measured counter is a
//     reset, and is taken whole;
//   - a per-call amount is taken as it is;
//   - a call that reported nothing has every counter absent, never zero;
//   - partial marks a call that was cut off, whose counters are what it
//     reported before the cut.
func Settle(previous *Counters, reading *Reading, partial bool, repeated int) Settled {
	settled := Settled{Partial: partial, Repeated: repeated}
	if reading == nil {
		return settled
	}
	settled.Tier = reading.Tier
	settled.Adjustments = append([]string(nil), reading.Adjustments...)
	if !reading.Running {
		settled.Counters = reading.Counters
		return settled
	}
	running := reading.Counters
	settled.Running = &running
	if previous == nil || fell(*previous, running) {
		settled.Counters = running
		settled.Reset = previous != nil
		return settled
	}
	for _, counter := range Every() {
		now := running.Get(counter)
		if !now.Measured {
			continue
		}
		before := previous.Get(counter)
		settled.Counters.set(counter, Measured(now.Tokens-before.Tokens))
	}
	return settled
}

// fell reports a running total below the previous one on a counter both
// measured, which is a total that restarted rather than one that moved.
func fell(previous, now Counters) bool {
	for _, counter := range Every() {
		before, after := previous.Get(counter), now.Get(counter)
		if before.Measured && after.Measured && after.Tokens < before.Tokens {
			return true
		}
	}
	return false
}

func mergeWords(into, more []string) []string {
	for _, word := range more {
		found := false
		for _, have := range into {
			if have == word {
				found = true
				break
			}
		}
		if !found {
			into = append(into, word)
		}
	}
	return into
}
