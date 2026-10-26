package runstate

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/pricing"
	"github.com/mason-bryant/yoyodyne/internal/usage"
)

func rates(t *testing.T, version string, perMillion string) pricing.Service {
	t.Helper()
	schedule, err := pricing.Parse([]byte(`{"version": "` + version + `", "entries": [
  {"id": "gpt-test-` + version + `", "provider": "codex", "model": "gpt-test", "billing_mode": "api_key", "tier": "standard",
   "default_tier": true, "effective_from": "2020-01-01", "unit": "usd",
   "prices": {"fresh_input": {"per_million": ` + perMillion + `}, "cached_input": {"rule": "no_charge"}, "cache_write": {"rule": "no_charge"},
              "output": {"rule": "no_charge"}, "reasoning": {"rule": "no_charge"}},
   "source": "https://example.com/prices", "checked": "2026-10-23"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return pricing.Service{Schedule: schedule}
}

func spendStore(t *testing.T) *SpendStore {
	t.Helper()
	store, err := NewSpendStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// codexLine is a call recorded under the usage and cost design: a million
// fresh input tokens on an API-key account, priced by the service given.
func codexLine(t *testing.T, service pricing.Service) Spend {
	t.Helper()
	id, err := NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	counters := usage.Counters{FreshInput: usage.Measured(1_000_000)}
	line := Spend{
		SchemaVersion:  SpendSchemaVersion,
		ProductID:      "yoyodyne",
		At:             time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		Role:           "developer",
		Phase:          SpendPhaseDevelopment,
		Classification: SpendUnknown,
		Unknown:        "the provider ended the invocation without reporting what it cost",
		AccountAlias:   "default",
		ConfigRevision: "cfg-0123456789ab",
		RunID:          "run-0123456789abcdef0123456789abcdef",
		WorkItemID:     "yoyodyne-ani",
		Backend:        "codex",
		ResolvedModel:  "gpt-test",
		InvocationID:   id,
		Billing:        usage.BillingAPIKey,
		Counters:       &counters,
	}
	figure := service.Price(line.PricedCall())
	line.Cost = &figure
	return line
}

func TestARecalculationIsAppendedAndTheEarlierEstimateStaysReadable(t *testing.T) {
	t.Parallel()

	store := spendStore(t)
	first := rates(t, "v1", "2")
	line := codexLine(t, first)
	if line.Cost.Kind != pricing.KindEstimated || line.Cost.USD != 2 {
		t.Fatalf("cost = %+v", line.Cost)
	}
	if err := store.Append(line); err != nil {
		t.Fatal(err)
	}
	// Nothing to do while the rate data has not changed.
	if appended, err := store.Reprice(first, time.Now); err != nil || appended != 0 {
		t.Fatalf("appended = %d, %v", appended, err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	corrected := rates(t, "v2", "3")
	if appended, err := store.Reprice(corrected, time.Now); err != nil || appended != 1 {
		t.Fatalf("appended = %d, %v", appended, err)
	}
	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatal("a recalculation rewrote the ledger rather than appending to it")
	}
	lines, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("the later estimate was listed as a call of its own: %d lines", len(lines))
	}
	shown := lines[0]
	if shown.Cost.USD != 3 || shown.Cost.Schedule != "v2" || len(shown.Earlier) != 1 || shown.Earlier[0].Schedule != "v1" || shown.Earlier[0].USD != 2 {
		t.Fatalf("shown = %+v, earlier = %+v", shown.Cost, shown.Earlier)
	}
	if kinds := KindsOf(lines, nil); kinds.Calls != 1 || kinds.EstimatedUSD != 3 || kinds.RateVersions[0] != "v2" {
		t.Fatalf("kinds = %+v", kinds)
	}
	if appended, err := store.Reprice(corrected, time.Now); err != nil || appended != 0 {
		t.Fatalf("a second recalculation at the same rates appended %d, %v", appended, err)
	}
}

func TestAReportedCostAndALineFromBeforeFiguresAreNeverRecalculated(t *testing.T) {
	t.Parallel()

	store := spendStore(t)
	reported := codexLine(t, rates(t, "v1", "2"))
	reported.Classification, reported.Unknown, reported.AmountUSD = SpendKnown, "", 0.5
	figure := pricing.Figure{Kind: pricing.KindReported, USD: 0.5}
	reported.Cost = &figure
	old := codexLine(t, rates(t, "v1", "2"))
	old.Cost, old.Counters, old.Billing, old.InvocationID = nil, nil, "", ""
	for _, line := range []Spend{reported, old} {
		if err := store.Append(line); err != nil {
			t.Fatal(err)
		}
	}
	if appended, err := store.Reprice(rates(t, "v2", "9"), time.Now); err != nil || appended != 0 {
		t.Fatalf("appended = %d, %v", appended, err)
	}
	lines, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	// The old line reads with every counter absent and the kind its
	// classification says, never as zero.
	if lines[1].UsageCounters().Any() || lines[1].Figure().Kind != pricing.KindUnknown || lines[1].Figure().Unknown == "" {
		t.Fatalf("old = %+v", lines[1].Figure())
	}
	kinds := KindsOf(lines, nil)
	if kinds.ReportedUSD != 0.5 || kinds.UnknownCalls != 1 || kinds.EstimateCalls != 0 || kinds.UnknownShare() != 0.5 {
		t.Fatalf("kinds = %+v", kinds)
	}
}

func TestACostFigureThatDisagreesWithItsLineIsRefused(t *testing.T) {
	t.Parallel()

	base := codexLine(t, rates(t, "v1", "2"))
	cases := map[string]func(*Spend){
		"reported on unknown": func(s *Spend) { s.Cost = &pricing.Figure{Kind: pricing.KindReported, USD: 1} },
		"estimate unnamed":    func(s *Spend) { s.Cost = &pricing.Figure{Kind: pricing.KindEstimated, USD: 1} },
		"unknown silent":      func(s *Spend) { s.Cost = &pricing.Figure{Kind: pricing.KindUnknown} },
		"unknown with figure": func(s *Spend) { s.Cost = &pricing.Figure{Kind: pricing.KindUnknown, USD: 1, Unknown: "x"} },
		"no such kind":        func(s *Spend) { s.Cost = &pricing.Figure{Kind: "blended", USD: 1} },
		"billing":             func(s *Spend) { s.Billing = "monthly" },
		"revision of reported": func(s *Spend) {
			s.Revises = "call-x"
			s.Classification, s.Unknown, s.AmountUSD = SpendKnown, "", 1
			s.Cost = &pricing.Figure{Kind: pricing.KindReported, USD: 1}
		},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("the base line is refused: %v", err)
	}
	for name, change := range cases {
		line := base
		change(&line)
		if err := line.Validate(); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestCostKindsSayEachKindApartAndWhyCostsAreUnknown(t *testing.T) {
	t.Parallel()

	credits := 12.0
	var kinds CostKinds
	for _, figure := range []pricing.Figure{
		{Kind: pricing.KindReported, USD: 1},
		{Kind: pricing.KindEstimated, USD: 2, Entry: "e", Schedule: "v1"},
		{Kind: pricing.KindComparison, USD: 4, Entry: "e", Schedule: "v1", Credits: &credits},
		{Kind: pricing.KindUnknown, Unknown: "no rate entry covers it"},
	} {
		figure := figure
		kinds.Add(Spend{Cost: &figure, Classification: map[bool]SpendClassification{true: SpendKnown, false: SpendUnknown}[figure.Kind == pricing.KindReported]})
	}
	text := kinds.Text()
	for _, want := range []string{"$1.00 reported by the provider", "$2.00 estimated (1 call) at rate data v1", "$4.00 at API prices", "not money spent", "12.00 credits", "cost unknown for 1 of 4 calls (25%)", "no rate entry covers it"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q does not say %q", text, want)
		}
	}
	if kinds.ReportedUSD+kinds.EstimatedUSD != 3 || kinds.ComparisonUSD != 4 {
		t.Fatalf("kinds were added together: %+v", kinds)
	}
	var merged CostKinds
	merged.Merge(kinds)
	merged.Merge(kinds)
	if merged.Calls != 8 || merged.UnknownReasons["no rate entry covers it"] != 2 || len(merged.RateVersions) != 1 {
		t.Fatalf("merged = %+v", merged)
	}
}

func TestASpendQueryCoversALineByKindNameAndDay(t *testing.T) {
	t.Parallel()

	now := time.Now()
	line := Spend{RunID: "run-1", WorkItemID: "yoyodyne-ani", At: now}
	if !(SpendQuery{Now: now, Days: 1}).Covers(line) || !(SpendQuery{Match: "yoyodyne-ani"}).Covers(line) {
		t.Fatal("a run's call was not covered")
	}
	if (SpendQuery{Kinds: []StreamKind{StreamConversation}}).Covers(line) || (SpendQuery{Match: "other"}).Covers(line) {
		t.Fatal("a run's call was covered by a query that excludes it")
	}
	if (SpendQuery{Now: now, Days: 1}).Covers(Spend{ConversationID: "c", At: now.AddDate(0, 0, -3)}) {
		t.Fatal("a call outside the days was covered")
	}
}
