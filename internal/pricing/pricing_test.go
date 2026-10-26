package pricing

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/usage"
)

// fixture is rate data written for these tests. Its figures are nobody's
// price: they are round numbers so an expected cost can be worked out by eye.
const fixture = `{
  "version": "test.1",
  "entries": [
    {
      "id": "m-api-standard-old", "provider": "codex", "model": "m", "billing_mode": "api_key",
      "tier": "standard", "default_tier": true, "effective_from": "2026-01-01", "effective_until": "2026-10-01",
      "unit": "usd",
      "prices": {
        "fresh_input": {"per_million": 1}, "cached_input": {"per_million": 0.1},
        "cache_write": {"rule": "no_charge"}, "output": {"per_million": 10},
        "reasoning": {"rule": "charged_as", "as": "output"}
      },
      "source": "https://example.com/prices", "checked": "2026-09-30"
    },
    {
      "id": "m-api-standard", "provider": "codex", "model": "m", "billing_mode": "api_key",
      "tier": "standard", "default_tier": true, "effective_from": "2026-10-01",
      "unit": "usd",
      "prices": {
        "fresh_input": {"per_million": 2}, "cached_input": {"per_million": 0.2},
        "cache_write": {"rule": "no_charge"}, "output": {"per_million": 20},
        "reasoning": {"rule": "charged_as", "as": "output"}
      },
      "long_context": {
        "above_input_tokens": 1000,
        "prices": {
          "fresh_input": {"per_million": 4}, "cached_input": {"per_million": 0.4},
          "cache_write": {"rule": "no_charge"}, "output": {"per_million": 30},
          "reasoning": {"rule": "charged_as", "as": "output"}
        }
      },
      "source": "https://example.com/prices", "checked": "2026-10-02"
    },
    {
      "id": "m-api-priority", "provider": "codex", "model": "m", "billing_mode": "api_key",
      "tier": "priority", "effective_from": "2026-10-01",
      "unit": "usd",
      "prices": {
        "fresh_input": {"per_million": 5}, "cached_input": {"per_million": 0.5},
        "cache_write": {"rule": "no_charge"}, "output": {"per_million": 50},
        "reasoning": {"rule": "charged_as", "as": "output"}
      },
      "source": "https://example.com/prices", "checked": "2026-10-02"
    },
    {
      "id": "m-credits", "provider": "codex", "model": "m", "billing_mode": "credits",
      "tier": "standard", "default_tier": true, "effective_from": "2026-10-01",
      "unit": "credits",
      "prices": {
        "fresh_input": {"per_million": 100}, "cached_input": {"per_million": 10},
        "cache_write": {"rule": "no_charge"}, "output": {"per_million": 1000},
        "reasoning": {"rule": "charged_as", "as": "output"}
      },
      "source": "https://example.com/credits", "checked": "2026-10-02"
    },
    {
      "id": "c-api", "provider": "claude-code", "model": "c", "billing_mode": "api_key",
      "tier": "standard", "default_tier": true, "effective_from": "2026-10-01",
      "unit": "usd",
      "prices": {
        "fresh_input": {"per_million": 3}, "cached_input": {"per_million": 0.3},
        "cache_write": {"per_million": 3.75}, "output": {"per_million": 15},
        "reasoning": {"rule": "not_separate"}
      },
      "source": "https://example.com/claude", "checked": "2026-10-02"
    }
  ]
}`

func service(t *testing.T) Service {
	t.Helper()
	schedule, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	return Service{Schedule: schedule}
}

var october = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// codexCall is one Codex call's usage: 600 fresh, 300 cached, no cache writes,
// 50 output and 20 reasoning, which is 900 input tokens -- under the fixture's
// long-context threshold of 1000.
func codexCall(billing usage.BillingMode) Call {
	return Call{
		Provider: "codex", Model: "m", Billing: billing, At: october,
		Counters: usage.Counters{
			FreshInput: usage.Measured(600), CachedInput: usage.Measured(300), CacheWrite: usage.Measured(0),
			Output: usage.Measured(50), Reasoning: usage.Measured(20),
		},
	}
}

func near(t *testing.T, got, want float64) {
	t.Helper()
	if diff := got - want; diff > 1e-12 || diff < -1e-12 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func TestTheShippedRateDataIsValid(t *testing.T) {
	if _, err := New(); err != nil {
		t.Fatal(err)
	}
}

func TestAProviderReportedFigureTakesPrecedence(t *testing.T) {
	reported := 0.42
	call := codexCall(usage.BillingAPIKey)
	call.ReportedUSD = &reported
	figure := service(t).Price(call)
	if figure.Kind != KindReported || figure.USD != 0.42 || figure.Entry != "" {
		t.Fatalf("figure = %+v", figure)
	}
	zero := 0.0
	call.ReportedUSD = &zero
	if figure := service(t).Price(call); figure.Kind != KindReported || figure.USD != 0 {
		t.Fatalf("a reported zero = %+v", figure)
	}
}

func TestAnAPIKeyCallIsEstimatedNamingItsEntryAndVersion(t *testing.T) {
	figure := service(t).Price(codexCall(usage.BillingAPIKey))
	if figure.Kind != KindEstimated || figure.Entry != "m-api-standard" || figure.Schedule != "test.1" || figure.Tier != "standard" {
		t.Fatalf("figure = %+v", figure)
	}
	// 600*2 + 300*0.2 + 50*20 + 20*20 (reasoning charged as output), per million.
	near(t, figure.USD, (1200+60+1000+400)/1e6)
	if len(figure.Notes) != 1 || !strings.Contains(figure.Notes[0], "no processing tier") {
		t.Fatalf("a default tier was taken without saying so: %v", figure.Notes)
	}
}

func TestTheRateInForceOnTheDayOfTheCallIsUsed(t *testing.T) {
	call := codexCall(usage.BillingAPIKey)
	call.At = time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
	figure := service(t).Price(call)
	if figure.Entry != "m-api-standard-old" {
		t.Fatalf("figure = %+v", figure)
	}
	near(t, figure.USD, (600+30+500+200)/1e6)
	call.At = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if figure := service(t).Price(call); figure.Kind != KindUnknown || !strings.Contains(figure.Unknown, "no rate entry") {
		t.Fatalf("a call before any entry = %+v", figure)
	}
}

func TestAReportedTierIsMatchedExactly(t *testing.T) {
	call := codexCall(usage.BillingAPIKey)
	call.Tier = "priority"
	if figure := service(t).Price(call); figure.Entry != "m-api-priority" || len(figure.Notes) != 0 {
		t.Fatalf("figure = %+v", figure)
	}
	call.Tier = "flex"
	if figure := service(t).Price(call); figure.Kind != KindUnknown || !strings.Contains(figure.Unknown, "flex") {
		t.Fatalf("an unpriced tier = %+v", figure)
	}
}

func TestTheLongContextRateIsDecidedFromTheCallsOwnInput(t *testing.T) {
	call := codexCall(usage.BillingAPIKey)
	call.Counters.FreshInput = usage.Measured(900) // 1,200 input tokens, above 1,000
	call.SingleRequest = true
	figure := service(t).Price(call)
	if figure.Kind != KindEstimated || !figure.LongContext {
		t.Fatalf("figure = %+v", figure)
	}
	near(t, figure.USD, (900*4+300*0.4+50*30+20*30)/1e6)

	// The same total over what may have been several requests cannot be placed.
	call.SingleRequest = false
	if figure := service(t).Price(call); figure.Kind != KindUnknown || !strings.Contains(figure.Unknown, "several requests") {
		t.Fatalf("an agent turn above the threshold = %+v", figure)
	}
	// Under the threshold every request inside it was under it too, whatever
	// the session's running total stands at.
	if figure := service(t).Price(codexCall(usage.BillingAPIKey)); figure.LongContext || figure.Kind != KindEstimated {
		t.Fatalf("a call under the threshold = %+v", figure)
	}
	// An input not fully reported cannot be placed either side.
	call = codexCall(usage.BillingAPIKey)
	call.Counters.CacheWrite = usage.Count{}
	if figure := service(t).Price(call); figure.Kind != KindUnknown || !strings.Contains(figure.Unknown, "not fully reported") {
		t.Fatalf("an incomplete input = %+v", figure)
	}
}

func TestASubscriptionCallIsAComparisonNotMoneySpent(t *testing.T) {
	figure := service(t).Price(codexCall(usage.BillingSubscription))
	if figure.Kind != KindComparison || figure.Entry != "m-api-standard" || figure.Credits != nil {
		t.Fatalf("figure = %+v", figure)
	}
}

func TestACreditsCallIsComparedInDollarsAndCountedInCreditsApart(t *testing.T) {
	figure := service(t).Price(codexCall(usage.BillingCredits))
	if figure.Kind != KindComparison || figure.Credits == nil || figure.CreditsEntry != "m-credits" {
		t.Fatalf("figure = %+v", figure)
	}
	near(t, *figure.Credits, (600*100+300*10+50*1000+20*1000)/1e6)
	near(t, figure.USD, (1200+60+1000+400)/1e6)
}

func TestMissingEvidenceIsUnknownAndSaysWhy(t *testing.T) {
	cases := map[string]struct {
		call Call
		want string
	}{
		"billing": {codexCall(usage.BillingNotRecorded), "billing mode is not recorded"},
		"usage":   {Call{Provider: "codex", Model: "m", Billing: usage.BillingAPIKey, At: october}, "neither a cost nor any token usage"},
		"model":   {func() Call { c := codexCall(usage.BillingAPIKey); c.Model = ""; return c }(), "names no model"},
		"rate":    {func() Call { c := codexCall(usage.BillingAPIKey); c.Model = "other"; return c }(), "no rate entry"},
		"counter": {func() Call { c := codexCall(usage.BillingAPIKey); c.Counters.Reasoning = usage.Count{}; return c }(), "did not report reasoning"},
	}
	for name, test := range cases {
		figure := service(t).Price(test.call)
		if figure.Kind != KindUnknown || figure.USD != 0 || !strings.Contains(figure.Unknown, test.want) {
			t.Fatalf("%s: figure = %+v", name, figure)
		}
	}
}

func TestACounterTheProviderDoesNotReportApartNeedsNoReading(t *testing.T) {
	call := Call{Provider: "claude-code", Model: "c", Billing: usage.BillingAPIKey, At: october, Counters: usage.Counters{
		FreshInput: usage.Measured(10), CachedInput: usage.Measured(1000), CacheWrite: usage.Measured(200), Output: usage.Measured(40),
	}}
	figure := service(t).Price(call)
	if figure.Kind != KindEstimated {
		t.Fatalf("figure = %+v", figure)
	}
	near(t, figure.USD, (10*3+1000*0.3+200*3.75+40*15)/1e6)
}

func TestRateDataThatBreaksTheRulesIsRefusedWhole(t *testing.T) {
	cases := map[string]struct {
		replace, with, want string
	}{
		"overlap":   {`"effective_until": "2026-10-01"`, `"effective_until": "2026-10-02"`, "overlap in time"},
		"source":    {`"source": "https://example.com/claude"`, `"source": ""`, "not the https page"},
		"checked":   {`"checked": "2026-10-02"`, `"checked": "soon"`, "not the date the source was read"},
		"price":     {`"reasoning": {"rule": "not_separate"}`, `"reasoning": null`, "no price or rule for reasoning"},
		"unit":      {`"billing_mode": "credits",`, `"billing_mode": "api_key",`, "does not match billing mode"},
		"subscript": {`"id": "c-api", "provider": "claude-code", "model": "c", "billing_mode": "api_key"`, `"id": "c-api", "provider": "claude-code", "model": "c", "billing_mode": "subscription"`, "not one a provider prices per token"},
		"chargedas": {`{"rule": "charged_as", "as": "output"}`, `{"rule": "charged_as", "as": "cache_write"}`, "not another counter with a price"},
		"unknown":   {`"version": "test.1",`, `"version": "test.1", "blended_rate": 1,`, "unknown field"},
		"version":   {`"version": "test.1",`, `"version": "",`, "names no version"},
	}
	for name, test := range cases {
		if !strings.Contains(fixture, test.replace) {
			t.Fatalf("%s: the fixture has no %s", name, test.replace)
		}
		_, err := Parse([]byte(strings.Replace(fixture, test.replace, test.with, 1)))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s: err = %v, want %q", name, err, test.want)
		}
	}
}
