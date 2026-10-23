package spend

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/pricing"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/usage"
)

// testRates is rate data for these tests: round figures that are nobody's
// price, one Codex model and one model of a provider a project declared on the
// Codex adapter.
const testRates = `{"version": "test.7", "entries": [
  {"id": "gpt-test-api", "provider": "codex", "model": "gpt-test", "billing_mode": "api_key", "tier": "standard",
   "default_tier": true, "effective_from": "2020-01-01", "unit": "usd",
   "prices": {"fresh_input": {"per_million": 1}, "cached_input": {"per_million": 0.1}, "cache_write": {"rule": "no_charge"},
              "output": {"per_million": 10}, "reasoning": {"rule": "charged_as", "as": "output"}},
   "source": "https://example.com/prices", "checked": "2026-10-23"},
  {"id": "declared-api", "provider": "acme", "model": "acme-1", "billing_mode": "api_key", "tier": "standard",
   "default_tier": true, "effective_from": "2020-01-01", "unit": "usd",
   "prices": {"fresh_input": {"per_million": 2}, "cached_input": {"per_million": 0.2}, "cache_write": {"rule": "no_charge"},
              "output": {"per_million": 20}, "reasoning": {"rule": "charged_as", "as": "output"}},
   "source": "https://example.com/acme", "checked": "2026-10-23"}
]}`

func testPricing(t *testing.T) *pricing.Service {
	t.Helper()
	schedule, err := pricing.Parse([]byte(testRates))
	if err != nil {
		t.Fatal(err)
	}
	return &pricing.Service{Schedule: schedule}
}

func testStore(t *testing.T) *runstate.SpendStore {
	t.Helper()
	store, err := runstate.NewSpendStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// codexTurn is what the Codex adapter hands over for one turn: a running total
// for the session, the overlaps already taken out.
func codexTurn(session string, fresh, cached, output, reasoning int64) backend.RunResult {
	return backend.RunResult{
		Backend:       "codex",
		SessionID:     session,
		ResolvedModel: "gpt-test",
		UsageReading: &usage.Reading{Session: session, Running: true, Counters: usage.Counters{
			FreshInput: usage.Measured(fresh), CachedInput: usage.Measured(cached), CacheWrite: usage.Measured(0),
			Output: usage.Measured(output), Reasoning: usage.Measured(reasoning),
		}},
		LastEvent: 41,
	}
}

func codexMetered(t *testing.T, log Log, billing usage.BillingMode, result backend.RunResult, err error) Metered {
	metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) { return result, err })
	metered.Attribution.Backend = "codex"
	metered.Attribution.Billing = billing
	metered.Pricing = testPricing(t)
	return metered
}

func runOnce(t *testing.T, metered Metered) runstate.Spend {
	t.Helper()
	var recorded runstate.Spend
	metered.Recorded = func(line runstate.Spend) { recorded = line }
	_, _ = metered.Run(context.Background(), testRequest())
	if err := recorded.Validate(); err != nil {
		t.Fatalf("the line does not satisfy the durable contract: %v", err)
	}
	return recorded
}

func TestAFreshCodexSessionIsSettledWholeAndEstimated(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	line := runOnce(t, codexMetered(t, store, usage.BillingAPIKey, codexTurn("thread-1", 600, 300, 50, 20), nil))
	want := usage.Counters{FreshInput: usage.Measured(600), CachedInput: usage.Measured(300), CacheWrite: usage.Measured(0), Output: usage.Measured(50), Reasoning: usage.Measured(20)}
	if line.Counters == nil || *line.Counters != want || line.UsageReset {
		t.Fatalf("counters = %+v", line.Counters)
	}
	if line.Cost == nil || line.Cost.Kind != pricing.KindEstimated || line.Cost.Entry != "gpt-test-api" || line.Cost.Schedule != "test.7" {
		t.Fatalf("cost = %+v", line.Cost)
	}
	if got, want := line.Cost.USD, (600*1+300*0.1+50*10+20*10)/1e6; got < want-1e-12 || got > want+1e-12 {
		t.Fatalf("estimate = %v, want %v", got, want)
	}
	if line.Known() || line.Billing != usage.BillingAPIKey || line.InvocationID == "" || line.EventSequence != 41 || line.EventSchemaVersion == 0 {
		t.Fatalf("the usage record is incomplete: %+v", line)
	}
}

func TestAResumedCodexSessionIsSettledAgainstItsLastRecordedTotalAcrossProcesses(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	runOnce(t, codexMetered(t, store, usage.BillingAPIKey, codexTurn("thread-1", 600, 300, 50, 20), nil))
	// A second meter is a later process: the previous total is read from the
	// ledger, not from memory.
	second := runOnce(t, codexMetered(t, store, usage.BillingAPIKey, codexTurn("thread-1", 900, 2300, 80, 25), nil))
	want := usage.Counters{FreshInput: usage.Measured(300), CachedInput: usage.Measured(2000), CacheWrite: usage.Measured(0), Output: usage.Measured(30), Reasoning: usage.Measured(5)}
	if *second.Counters != want || second.UsageReset {
		t.Fatalf("second = %+v", second.Counters)
	}
	// Another session is settled on its own, from zero.
	other := runOnce(t, codexMetered(t, store, usage.BillingAPIKey, codexTurn("thread-2", 10, 0, 1, 0), nil))
	if other.Counters.FreshInput != usage.Measured(10) {
		t.Fatalf("a fresh session was settled against another's total: %+v", other.Counters)
	}
	// A total that fell is a reset, taken whole and said.
	reset := runOnce(t, codexMetered(t, store, usage.BillingAPIKey, codexTurn("thread-1", 40, 0, 4, 0), nil))
	if !reset.UsageReset || reset.Counters.FreshInput != usage.Measured(40) {
		t.Fatalf("reset = %+v", reset)
	}
	lines, err := store.List()
	if err != nil || len(lines) != 4 {
		t.Fatalf("lines = %d, %v", len(lines), err)
	}
}

func TestAFailedCallWithNoUsageHasEveryCounterAbsentAndAnUnknownCost(t *testing.T) {
	t.Parallel()

	line := runOnce(t, codexMetered(t, testStore(t), usage.BillingAPIKey, backend.RunResult{Backend: "codex"}, errors.New("stream ended")))
	if line.Counters == nil || line.Counters.Any() {
		t.Fatalf("counters = %+v", line.Counters)
	}
	if line.Cost.Kind != pricing.KindUnknown || !strings.Contains(line.Cost.Unknown, "neither a cost nor any token usage") {
		t.Fatalf("cost = %+v", line.Cost)
	}
}

func TestACutOffCallAndARepeatedEndingAreSaid(t *testing.T) {
	t.Parallel()

	result := codexTurn("thread-1", 10, 0, 1, 0)
	result.UsagePartial = true
	result.UsageRepeated = 1
	line := runOnce(t, codexMetered(t, testStore(t), usage.BillingAPIKey, result, context.Canceled))
	if !line.UsagePartial || line.UsageRepeated != 1 || line.Counters.FreshInput != usage.Measured(10) {
		t.Fatalf("line = %+v", line)
	}
}

func TestTheBillingModeDecidesWhatAComputedCostIs(t *testing.T) {
	t.Parallel()

	cases := map[usage.BillingMode]pricing.Kind{
		usage.BillingAPIKey:       pricing.KindEstimated,
		usage.BillingSubscription: pricing.KindComparison,
		usage.BillingCredits:      pricing.KindComparison,
		usage.BillingNotRecorded:  pricing.KindUnknown,
		"":                        pricing.KindUnknown,
	}
	for billing, want := range cases {
		line := runOnce(t, codexMetered(t, testStore(t), billing, codexTurn("thread-1", 10, 0, 1, 0), nil))
		if line.Cost.Kind != want || line.Billing != billing.Resolved() {
			t.Fatalf("%q: cost = %+v, billing = %q", billing, line.Cost, line.Billing)
		}
	}
	// Credits nobody has a credits rate for are said to be unknown beside the
	// comparison, rather than left out.
	credits := runOnce(t, codexMetered(t, testStore(t), usage.BillingCredits, codexTurn("thread-1", 10, 0, 1, 0), nil))
	if credits.Cost.Credits != nil || len(credits.Cost.Notes) == 0 || !strings.Contains(credits.Cost.Notes[len(credits.Cost.Notes)-1], "credits used are not known") {
		t.Fatalf("credits = %+v", credits.Cost)
	}
}

func TestANativeCostIsReportedAndItsCountersRecordedAsTheCallsOwn(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	claude := func(total float64) Metered {
		metered := testMetered(store, func(backend.RunRequest) (backend.RunResult, error) {
			return backend.RunResult{
				Backend: "claude-code", SessionID: "session-1", CostUSD: total, CostReported: true,
				UsageReading: &usage.Reading{Session: "session-1", Counters: usage.Counters{
					FreshInput: usage.Measured(5), CachedInput: usage.Measured(100), CacheWrite: usage.Measured(7), Output: usage.Measured(9),
				}},
			}, nil
		})
		metered.Attribution.Billing = usage.BillingSubscription
		metered.Pricing = testPricing(t)
		return metered
	}
	runOnce(t, claude(2))
	second := runOnce(t, claude(3.5))
	// The session's reported running total is still turned into the call's own
	// cost, and that is the reported figure: a provider's own number is never
	// replaced by an estimate or a comparison.
	if second.Cost.Kind != pricing.KindReported || second.Cost.USD != 1.5 || second.AmountUSD != 1.5 {
		t.Fatalf("cost = %+v, amount = %v", second.Cost, second.AmountUSD)
	}
	if second.RunningCounters != nil || second.Counters.FreshInput != usage.Measured(5) || second.Counters.Reasoning.Measured {
		t.Fatalf("counters = %+v, running = %+v", second.Counters, second.RunningCounters)
	}
}

func TestADeclaredProviderOnACompiledAdapterIsPricedByItsOwnName(t *testing.T) {
	t.Parallel()

	result := codexTurn("thread-1", 1000, 0, 100, 0)
	result.Backend = "acme"
	result.ResolvedModel = "acme-1"
	line := runOnce(t, codexMetered(t, testStore(t), usage.BillingAPIKey, result, nil))
	if line.Backend != domain.Backend("acme") || line.Cost.Kind != pricing.KindEstimated || line.Cost.Entry != "declared-api" {
		t.Fatalf("line = %+v, cost = %+v", line, line.Cost)
	}
	// Its model is never priced at another provider's rate for the same name.
	result.ResolvedModel = "gpt-test"
	if line := runOnce(t, codexMetered(t, testStore(t), usage.BillingAPIKey, result, nil)); line.Cost.Kind != pricing.KindUnknown {
		t.Fatalf("a declared provider was priced at another provider's rate: %+v", line.Cost)
	}
}

func TestACallMadeForNoWorkItemIsCountedUnderItsRoleAndInTheTotal(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	runOnce(t, codexMetered(t, store, usage.BillingAPIKey, codexTurn("thread-1", 10, 0, 1, 0), nil))
	conversation := testMetered(store, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: "claude-code", CostUSD: 0.25, CostReported: true}, nil
	})
	conversation.Attribution.Phase = runstate.SpendPhaseConversation
	conversation.Attribution.RunID, conversation.Attribution.WorkItemID = "", ""
	conversation.Attribution.ConversationID = "chat-0123456789abcdef"
	request := testRequest()
	request.Role = domain.RoleProductManager
	if _, err := conversation.Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	lines, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	total := runstate.KindsOf(lines, nil)
	if total.Calls != 2 || total.ReportedCalls != 1 || total.EstimateCalls != 1 || total.ReportedUSD != 0.25 {
		t.Fatalf("total = %+v", total)
	}
	byRole := runstate.KindsOf(lines, func(line runstate.Spend) bool { return line.Role == domain.RoleProductManager })
	item := runstate.KindsOf(lines, func(line runstate.Spend) bool { return line.WorkItemID != "" })
	if byRole.Calls != 1 || item.Calls != 1 || byRole.Calls+item.Calls != total.Calls {
		t.Fatalf("by role = %+v, by item = %+v", byRole, item)
	}
}
