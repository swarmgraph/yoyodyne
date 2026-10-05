package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestCostRefusesMoreThanOneItemAndReportsConfigurationFailureAsJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"cost", "yoyodyne-ifd.41", "yoyodyne-ifd.42"}, &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "at most one Beads work item id") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if code := Run([]string{"cost", "--config", missing, "--json"}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result costOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Error == "" || len(result.Prices) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

// The ledger is read down a column, and a run nothing survives to price is
// counted as unpriced rather than added to the total as a zero: every total it
// touched would otherwise read as complete while being short.
func TestCostLedgerCountsUnpricedRunsRatherThanChargingNothingForThem(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	prices := []runstate.ItemPrice{
		{
			WorkItemID: "yoyodyne-ifd.2.7",
			Runs: []runstate.RunPrice{
				{RunID: "run-1", Status: runstate.StatusFailed, StartedAt: started, CostUSD: 8.91, Invocations: 3},
				{RunID: "run-2", Status: runstate.StatusSucceeded, StartedAt: started, CostUSD: 19.02, Invocations: 2},
			},
			TotalUSD: 27.93,
			Phases: runstate.PhaseSpend{
				Development: runstate.PhaseCost{CostUSD: 18.02, Invocations: 2},
				Review:      runstate.PhaseCost{CostUSD: 6.10, Invocations: 2},
				Repair:      runstate.PhaseCost{CostUSD: 3.81, Invocations: 1},
				Waits:       runstate.Waits{UsageLimitSeconds: 9540},
			},
		},
		{
			WorkItemID: "yoyodyne-ifd.41",
			Runs: []runstate.RunPrice{
				{RunID: "run-3", Status: runstate.StatusFailed, StartedAt: started, Unknown: "the run's event log is no longer recorded"},
			},
			UnknownRuns: 1,
		},
		// An item nothing ran is left out entirely rather than listed at nothing.
		{WorkItemID: "yoyodyne-ifd.99"},
	}

	var out bytes.Buffer
	printPrices(&out, prices, nil, nil, false)
	rendered := out.String()
	for _, required := range []string{
		"yoyodyne-ifd.2.7",
		"$27.93",
		"TOTAL",
		"≥ $27.93",
		// A row whose own runs went unpriced is marked as a floor like every
		// other figure, rather than reading as an exact price of nothing because
		// the count that says otherwise is in the next column.
		"≥ $0.00",
		// Where the money went is a column of the ledger rather than something
		// only a per-item question answers.
		"develop",
		"$18.02",
		"$6.10",
		"$3.81",
		"2h39m",
		"develop, review and repair account for every priced run invocation",
		"counted as unpriced and left out of the total",
		"conversation turns are recorded but not attributed",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered ledger = %q, want it to contain %q", rendered, required)
		}
	}
	if strings.Contains(rendered, "yoyodyne-ifd.99") {
		t.Fatalf("an item with no recorded run was priced: %q", rendered)
	}
	// A product whose roles have never asked each other anything says nothing
	// about asks: reporting a row of nothing would be reporting the absence of
	// something nobody did.
	if strings.Contains(rendered, askLedgerLabel) {
		t.Fatalf("a ledger with no exchanges carried an ask row: %q", rendered)
	}
}

// What one role spent asking another is a provider invocation the harness made,
// so it belongs in the total beside the runs. It belongs to no work item, which
// is why it is a row of its own rather than money folded into somebody's item.
func TestCostLedgerCarriesWhatTheRolesSpentAskingEachOtherIntoTheTotal(t *testing.T) {
	t.Parallel()

	prices := []runstate.ItemPrice{{
		WorkItemID: "yoyodyne-ifd.2.7",
		Runs:       []runstate.RunPrice{{RunID: "run-1", Status: runstate.StatusSucceeded, CostUSD: 10.00, Invocations: 1}},
		TotalUSD:   10.00,
		Phases:     runstate.PhaseSpend{Development: runstate.PhaseCost{CostUSD: 10.00, Invocations: 1}},
	}}

	var out bytes.Buffer
	printPrices(&out, prices, &runstate.ExchangeSpend{Exchanges: 2, Rounds: 3, CostUSD: 1.50}, nil, false)
	rendered := out.String()
	for _, required := range []string{
		askLedgerLabel,
		"$1.50",
		// Ten dollars of runs and a dollar fifty of asks: a total that skipped the
		// asks would read as complete while being short by exactly them.
		"$11.50",
		"asks between roles is 2 exchange(s) over 3 round(s)",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered ledger = %q, want it to contain %q", rendered, required)
		}
	}
	// The phase columns split a run's price, and an ask is none of the three, so
	// the row leaves them empty rather than claiming an ask cost nothing to
	// develop.
	askRow := ledgerLine(rendered, askLedgerLabel)
	if askRow == "" || strings.Contains(askRow, "$0.00") {
		t.Fatalf("ask row = %q, want the run phases left as unstated rather than zero", askRow)
	}
	// Nothing went unread, so the total is a price rather than a floor.
	if strings.Contains(ledgerLine(rendered, "TOTAL"), "≥") {
		t.Fatalf("rendered ledger = %q, want an exact total when every exchange was read", rendered)
	}

	// An exchange nobody can read is counted and left out, and the exchanges
	// beside it are still priced. A record that took the whole figure down with
	// it would put back the undercount this row exists to remove.
	out.Reset()
	printPrices(&out, prices, &runstate.ExchangeSpend{
		Exchanges: 1, Rounds: 2, CostUSD: 0.30,
		Unreadable: 1, Unknown: "decode exchange exchange-dddd: unexpected end of JSON input",
	}, nil, false)
	rendered = out.String()
	for _, required := range []string{
		// The readable exchange keeps its price, marked as the floor it now is.
		"≥ $0.30",
		// And it reaches the total: ten dollars of runs and thirty cents of asks.
		"≥ $10.30",
		"1 exchange record(s) could not be read and are left out of that figure",
		"unexpected end of JSON input",
		"every exchange beside them is priced as usual",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered ledger = %q, want it to contain %q", rendered, required)
		}
	}
	// The unread money belongs to no phase, so the marker goes on the total
	// column and nowhere else: three figures that are exact must not be made to
	// read as floors by it.
	total := ledgerLine(rendered, "TOTAL")
	if strings.Count(total, "≥") != 1 || !strings.Contains(total, " $10.00 ") {
		t.Fatalf("TOTAL row = %q, want the floor marked on the total alone and the phases left exact", total)
	}
	// The unreadable count sits in the same column an item's unpriced runs do.
	if asks := ledgerLine(rendered, askLedgerLabel); !strings.Contains(asks, " 1 ") {
		t.Fatalf("ask row = %q, want the unreadable record counted on it", asks)
	}

	// Exchanges that cannot even be listed are not a floor: how much is missing
	// is unknown, and so is how many records it is missing from.
	out.Reset()
	printPrices(&out, prices, &runstate.ExchangeSpend{Unknown: "read exchange directory: permission denied"}, nil, false)
	rendered = out.String()
	for _, required := range []string{
		"unknown",
		"≥ $10.00",
		"permission denied",
		"missing from the total entirely rather than counted as nothing",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered ledger = %q, want it to contain %q", rendered, required)
		}
	}
}

// ledgerLine is the rendered row a label opens, or empty where there is none.
func ledgerLine(rendered, label string) string {
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(line, label) {
			return line
		}
	}
	return ""
}

func TestCostBreaksOneItemDownByRunAndSaysWhenNothingWasRun(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	printPrices(&out, []runstate.ItemPrice{{
		WorkItemID: "yoyodyne-ifd.2.7",
		Runs: []runstate.RunPrice{
			{
				// A run the reviewer blocked: the price listing says what became of
				// it in the same words the run listing does.
				RunID: "run-1", Status: runstate.StatusFailed, Outcome: runstate.OutcomeStopped,
				Phase: runstate.PhaseReviewing, StartedAt: started,
				CostUSD: 8.91, Invocations: 3,
				Phases: runstate.PhaseSpend{
					Development: runstate.PhaseCost{CostUSD: 5.00, Invocations: 1},
					Review:      runstate.PhaseCost{CostUSD: 2.00, Invocations: 1},
					Repair:      runstate.PhaseCost{CostUSD: 1.91, Invocations: 1},
					Waits:       runstate.Waits{UsageLimitSeconds: 3600, OperatorHoldSeconds: 900},
				},
			},
			{
				RunID: "run-2", Status: runstate.StatusSucceeded, Outcome: runstate.OutcomeSucceeded,
				Phase: runstate.PhaseComplete, StartedAt: started,
				Integrated: true, CostUSD: 19.02, Invocations: 2,
				Phases: runstate.PhaseSpend{
					Development: runstate.PhaseCost{CostUSD: 13.02, Invocations: 1},
					Review:      runstate.PhaseCost{CostUSD: 4.10, Invocations: 1},
					Repair:      runstate.PhaseCost{CostUSD: 1.90, Invocations: 1},
				},
			},
		},
		TotalUSD: 27.93,
		Phases: runstate.PhaseSpend{
			Development: runstate.PhaseCost{CostUSD: 18.02, Invocations: 2},
			Review:      runstate.PhaseCost{CostUSD: 6.10, Invocations: 2},
			Repair:      runstate.PhaseCost{CostUSD: 3.81, Invocations: 2},
			Waits:       runstate.Waits{UsageLimitSeconds: 3600, OperatorHoldSeconds: 900},
		},
	}}, nil, nil, true)
	rendered := out.String()
	for _, required := range []string{
		"yoyodyne-ifd.2.7: $27.93 across 2 run(s)",
		"development $18.02 from 2 invocation(s), review $6.10 from 2, repair $3.81 from 2",
		"[stopped, reviewing] $8.91 from 3 invocation(s)",
		// Each attempt says where its own money went, because an item's split
		// says which attempts were expensive and not which part of one was.
		"development $5.00 from 1 invocation(s), review $2.00 from 1, repair $1.91 from 1",
		"[succeeded, complete, integrated] $19.02 from 2 invocation(s)",
		// Two waits of different kinds are named apart: one is a provider that
		// would not serve the account and the other is the operator's own doing.
		"waited 1h15m (1h00m for the provider, 15m00s on the operator's hold)",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered breakdown = %q, want it to contain %q", rendered, required)
		}
	}

	out.Reset()
	printPrices(&out, []runstate.ItemPrice{{WorkItemID: "yoyodyne-ifd.99"}}, nil, nil, true)
	if !strings.Contains(out.String(), "no price rather than a price of nothing") {
		t.Fatalf("rendered breakdown = %q", out.String())
	}

	// A run that is still going has not finished spending, so its figure says so
	// rather than reading as what the attempt will have cost.
	out.Reset()
	printPrices(&out, []runstate.ItemPrice{{
		WorkItemID: "yoyodyne-ifd.41",
		Runs: []runstate.RunPrice{{
			RunID: "run-4", Status: runstate.StatusRunning, Outcome: runstate.RunOutcome(runstate.StatusRunning),
			Phase: runstate.PhaseDeveloping, StartedAt: started,
		}},
	}}, nil, nil, true)
	if !strings.Contains(out.String(), "$0.00 so far from 0 invocation(s)") {
		t.Fatalf("rendered breakdown = %q", out.String())
	}

	// A run nothing survives to price has no split to show, and what it waited
	// came from its own record rather than from the log that is gone, so that is
	// reported on its own rather than lost with the money.
	out.Reset()
	printPrices(&out, []runstate.ItemPrice{{
		WorkItemID: "yoyodyne-ifd.41",
		Runs: []runstate.RunPrice{{
			RunID: "run-5", Status: runstate.StatusFailed, Outcome: runstate.OutcomeFailed, StartedAt: started,
			Unknown: "the run's event log is no longer recorded",
			Phases:  runstate.PhaseSpend{Waits: runstate.Waits{UsageLimitSeconds: 1800}},
		}},
		UnknownRuns: 1,
	}}, nil, nil, true)
	rendered = out.String()
	if !strings.Contains(rendered, "waited 30m00s for the provider") {
		t.Fatalf("rendered breakdown = %q, want the unpriceable run's wait", rendered)
	}
	if strings.Contains(rendered, "development $0.00") {
		t.Fatalf("rendered breakdown = %q, want no split claimed for a run nothing could price", rendered)
	}
}

// Money that landed in no phase is money the phase columns do not add up to, so
// the ledger says so out loud rather than leaving the reader to take the
// difference. It stays silent on a healthy record: an unattributed figure is a
// defect in whatever wrote the run's log, and a line reporting nought of them
// every time is a line nobody reads on the day there is one.
func TestCostSaysWhenPricedInvocationsLandedInNoPhase(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 8, 10, 9, 14, 2, 0, time.UTC)
	stray := runstate.ItemPrice{
		WorkItemID: "yoyodyne-ifd.172",
		Runs: []runstate.RunPrice{{
			RunID: "run-1", Status: runstate.StatusSucceeded, Outcome: runstate.OutcomeSucceeded,
			Phase: runstate.PhaseComplete, StartedAt: started, CostUSD: 19.00, Invocations: 3,
			Phases: runstate.PhaseSpend{
				Development:  runstate.PhaseCost{CostUSD: 9.00, Invocations: 1},
				Repair:       runstate.PhaseCost{CostUSD: 4.00, Invocations: 1},
				Unattributed: runstate.PhaseCost{CostUSD: 6.00, Invocations: 1},
			},
		}},
		TotalUSD: 19.00,
		Phases: runstate.PhaseSpend{
			Development:  runstate.PhaseCost{CostUSD: 9.00, Invocations: 1},
			Repair:       runstate.PhaseCost{CostUSD: 4.00, Invocations: 1},
			Unattributed: runstate.PhaseCost{CostUSD: 6.00, Invocations: 1},
		},
	}

	var out bytes.Buffer
	printPrices(&out, []runstate.ItemPrice{stray}, nil, nil, false)
	if !strings.Contains(out.String(), "1 priced invocation(s) worth $6.00 named no phase") {
		t.Fatalf("ledger = %q, want the unplaced money named under the table", out.String())
	}

	// The per-item breakdown carries it in the split line itself, where the three
	// phases are already spelled out and a fourth figure is what makes them add up.
	out.Reset()
	printPrices(&out, []runstate.ItemPrice{stray}, nil, nil, true)
	if !strings.Contains(out.String(), "unattributed $6.00 from 1") {
		t.Fatalf("breakdown = %q, want the unplaced money in the split", out.String())
	}

	// And nothing is said about it when there is none of it.
	out.Reset()
	healthy := stray
	healthy.Phases.Unattributed = runstate.PhaseCost{}
	printPrices(&out, []runstate.ItemPrice{healthy}, nil, nil, false)
	if strings.Contains(out.String(), "named no phase") {
		t.Fatalf("ledger = %q, want nothing said about money that all landed somewhere", out.String())
	}
}

// The keep-or-revert criterion of a prompt-caching change is the cache-read
// share of input tokens, and it is worth nothing unless the shipped tooling
// reports it. The ledger carries it per item and over everything, and the
// per-item breakdown carries it per run, which is the window the measure is
// actually taken over.
func TestCostReportsTheCacheReadShareOfInputTokens(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	prices := []runstate.ItemPrice{{
		WorkItemID: "yoyodyne-ifd.84",
		Runs: []runstate.RunPrice{{
			RunID: "run-1", Status: runstate.StatusSucceeded, Outcome: runstate.OutcomeSucceeded,
			StartedAt: started, CostUSD: 9.00, Invocations: 1,
			Phases: runstate.PhaseSpend{Development: runstate.PhaseCost{CostUSD: 9.00, Invocations: 1}},
			Tokens: runstate.TokenUsage{InputTokens: 1300, CacheReadTokens: 2500, CacheCreationTokens: 200, OutputTokens: 5500, Measured: 4},
		}},
		TotalUSD: 9.00,
		Phases:   runstate.PhaseSpend{Development: runstate.PhaseCost{CostUSD: 9.00, Invocations: 1}},
		Tokens:   runstate.TokenUsage{InputTokens: 1300, CacheReadTokens: 2500, CacheCreationTokens: 200, OutputTokens: 5500, Measured: 4},
	}}

	var out bytes.Buffer
	printPrices(&out, prices, nil, nil, false)
	rendered := out.String()
	for _, required := range []string{
		"cached",
		// 2500 of the 4000 input tokens, however the provider billed each of them.
		"62.5%",
		"cached is the cache-read share of input tokens: 2500 of 4000 input token(s) over 4 priced invocation(s)",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered ledger = %q, want it to contain %q", rendered, required)
		}
	}
	// An exchange record carries what its rounds cost and no token counts at all,
	// so the ask row states no share rather than a share of nothing.
	out.Reset()
	printPrices(&out, prices, &runstate.ExchangeSpend{Exchanges: 1, Rounds: 1, CostUSD: 0.50}, nil, false)
	if asks := ledgerLine(out.String(), askLedgerLabel); strings.Contains(asks, "%") {
		t.Fatalf("ask row = %q, want no cache-read share claimed for an exchange", asks)
	}

	out.Reset()
	printPrices(&out, prices, nil, nil, true)
	if !strings.Contains(out.String(), "cache-read share 62.5% of 4000 input token(s) over 4 invocation(s): 2500 cached, 1300 fresh, 200 written to the cache; 5500 output") {
		t.Fatalf("rendered breakdown = %q, want the share spelled out per item and per run", out.String())
	}
}

// The aggregate share is decided by whichever phase reads the most, so a review
// that reads none of its prefix hides behind a developer session that reads
// nearly all of its input. That is not a hypothetical: it is how yoyodyne-ifd.84
// came to be measured against a figure its own effect could not move, and this
// line is what a later change to what one phase sends is read on instead.
func TestCostReportsTheCacheReadShareOfEachPhaseBesideTheWhole(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	phases := runstate.PhaseSpend{
		Development: runstate.PhaseCost{CostUSD: 9.00, Invocations: 1,
			Tokens: runstate.TokenUsage{InputTokens: 100, CacheReadTokens: 9900, OutputTokens: 4000, Measured: 1}},
		Review: runstate.PhaseCost{CostUSD: 2.00, Invocations: 2,
			Tokens: runstate.TokenUsage{CacheCreationTokens: 1000, OutputTokens: 500, Measured: 2}},
	}
	tokens := runstate.TokenUsage{InputTokens: 100, CacheReadTokens: 9900, CacheCreationTokens: 1000, OutputTokens: 4500, Measured: 3}
	prices := []runstate.ItemPrice{{
		WorkItemID: "yoyodyne-ifd.205",
		Runs: []runstate.RunPrice{{
			RunID: "run-1", Status: runstate.StatusSucceeded, Outcome: runstate.OutcomeSucceeded,
			StartedAt: started, CostUSD: 11.00, Invocations: 3, Phases: phases, Tokens: tokens,
		}},
		TotalUSD: 11.00,
		Phases:   phases,
		Tokens:   tokens,
	}}

	var out bytes.Buffer
	printPrices(&out, prices, nil, nil, false)
	rendered := out.String()
	for _, required := range []string{
		// 9900 of 11000 across everything, which is the figure the review is
		// invisible in.
		"cached is the cache-read share of input tokens: 9900 of 11000 input token(s)",
		// And the same measure once per phase, where it is not.
		"cache-read share by phase: development 99.0% over 1 invocation(s), review 0.0% over 2 invocation(s), repair - over 0 invocation(s)",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered ledger = %q, want it to contain %q", rendered, required)
		}
	}

	// The same split under each item and each run, so a before-and-after window
	// can be cut on the runs rather than only on the whole ledger.
	out.Reset()
	printPrices(&out, prices, nil, nil, true)
	if breakdown := out.String(); strings.Count(breakdown, "review 0.0% over 2 invocation(s)") != 2 {
		t.Fatalf("rendered breakdown = %q, want the phase split under the item and under its run", breakdown)
	}

	// A window where nothing reported usage says so once, above; four dashes
	// under it would repeat that in a shape that reads like a measurement.
	unmeasured := prices
	unmeasured[0].Phases = runstate.PhaseSpend{Development: runstate.PhaseCost{CostUSD: 9.00, Invocations: 1, Tokens: runstate.TokenUsage{Unreported: 1}}}
	unmeasured[0].Runs[0].Phases = unmeasured[0].Phases
	unmeasured[0].Tokens = runstate.TokenUsage{Unreported: 1}
	unmeasured[0].Runs[0].Tokens = unmeasured[0].Tokens
	out.Reset()
	printPrices(&out, unmeasured, nil, nil, false)
	if strings.Contains(out.String(), "cache-read share by phase") {
		t.Fatalf("ledger = %q, want no per-phase share where nothing was measured", out.String())
	}
}

// A run nobody measured and a run measured at nothing are the same figure and
// opposite facts. Every run recorded before the harness kept the provider's
// usage object is the first kind, and reporting it as a share of nought would
// read as a caching change that achieved nothing.
func TestCostSaysWhenThereIsNoCacheReadShareRatherThanReportingNought(t *testing.T) {
	t.Parallel()

	unmeasured := []runstate.ItemPrice{{
		WorkItemID: "yoyodyne-ifd.2.7",
		Runs: []runstate.RunPrice{{
			RunID: "run-1", Status: runstate.StatusSucceeded, CostUSD: 4.00, Invocations: 2,
			Tokens: runstate.TokenUsage{Unreported: 2},
		}},
		TotalUSD: 4.00,
		Tokens:   runstate.TokenUsage{Unreported: 2},
	}}

	var out bytes.Buffer
	printPrices(&out, unmeasured, nil, nil, false)
	rendered := out.String()
	if !strings.Contains(rendered, "2 priced invocation(s) reported no token usage and none reported any") {
		t.Fatalf("ledger = %q, want the window said to be unmeasurable", rendered)
	}
	if strings.Contains(rendered, "0.0%") {
		t.Fatalf("ledger = %q, want no share where nothing was measured", rendered)
	}

	// An invocation the provider measured at nothing is the opposite case, and it
	// keeps its nought: that is a reading, and reporting it as no reading would
	// lose the one figure the provider actually gave.
	unmeasured[0].Tokens = runstate.TokenUsage{Measured: 1}
	out.Reset()
	printPrices(&out, unmeasured, nil, nil, false)
	if rendered := out.String(); !strings.Contains(rendered, "0.0%") {
		t.Fatalf("ledger = %q, want a measured nought reported as the reading it is", rendered)
	}

	// Where some invocations reported usage and others did not, the share is over
	// what was measured and the rest is named beside it rather than folded in.
	unmeasured[0].Tokens = runstate.TokenUsage{InputTokens: 250, CacheReadTokens: 750, Measured: 3, Unreported: 1}
	out.Reset()
	printPrices(&out, unmeasured, nil, nil, false)
	rendered = out.String()
	for _, required := range []string{
		"75.0%",
		"1 priced invocation(s) reported no token usage at all and are outside that share entirely",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("ledger = %q, want it to contain %q", rendered, required)
		}
	}
}

// A backfill that could not reach an item has to say which one, or the ledger
// reads as complete while part of it was never written.
func TestCostNamesThePricesItCouldNotRecord(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	printRecordedPrices(&stdout, &stderr, []recordedCost{
		{WorkItemID: "yoyodyne-ifd.41"},
		{WorkItemID: "yoyodyne-ifd.42", Failure: "bd update failed"},
	})
	if !strings.Contains(stdout.String(), "recorded the price of 1 of 2 work item(s)") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "yoyodyne-ifd.42 was priced but not recorded: bd update failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestCostReadsPreservationFromTheRepositoryForTextAndJSON(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	repository := gitProject(t)
	git(t, repository, "config", "user.name", "Yoyodyne Test")
	git(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	commit(t, repository, "base")
	configPath := writeConfig(t, strings.Replace(validConfig, "repository: .", "repository: "+repository, 1))
	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatal(err)
	}
	state := recordedRun(t, parts.store, runstate.StatusFailed, "yoyodyne-ifd.428.47", time.Now().Add(-time.Hour))
	worktree, err := parts.worktrees.Create(context.Background(), gitworktree.CreateRequest{RunID: state.RunID, WorkItemID: state.WorkItemID, BaseRef: "HEAD", TargetBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	git(t, repository, "worktree", "remove", "--force", worktree.Path)
	state.Branch, state.WorktreePath, state.BaseCommit, state.TargetBranch = worktree.Branch, worktree.Path, worktree.BaseCommit, worktree.TargetBranch
	for _, there := range []bool{true, false} {
		state.BranchRemoved, state.WorktreeRemoved = there, there
		state.ArtifactsRetiredBy = ""
		if there {
			state.ArtifactsRetiredBy = "run-" + strings.Repeat("f", 32)
		} else {
			git(t, repository, "branch", "-D", state.Branch)
		}
		saveRun(t, parts.store, state)
		want := "work removed, checked"
		if there {
			want = "work preserved, checked"
		}
		stdout, stderr, code := runCLI(t, "cost", "--config", configPath, state.WorkItemID)
		if code != 0 || !strings.Contains(stdout, want) {
			t.Fatalf("cost returned %d: %s %s, want %s", code, stdout, stderr, want)
		}
		if !strings.Contains(stdout, state.Branch) || !strings.Contains(stdout, state.WorktreePath) {
			t.Fatalf("cost does not name what was checked: %s", stdout)
		}
		stdout, stderr, code = runCLI(t, "cost", "--config", configPath, "--json", state.WorkItemID)
		if code != 0 {
			t.Fatalf("cost returned %d: %s", code, stderr)
		}
		var output costOutput
		if err := json.Unmarshal([]byte(stdout), &output); err != nil {
			t.Fatal(err)
		}
		run := output.Prices[0].Runs[0]
		if run.Remains != want || run.Found == nil || !run.Found.Looked() || run.Found.BranchThere != there {
			t.Fatalf("priced run = %#v, want %s", run, want)
		}
		conversationPrice, err := newConversationWork(parts).Price(context.Background(), state.WorkItemID)
		if err != nil || len(conversationPrice.Runs) != 1 || conversationPrice.Runs[0].Remains != want {
			t.Fatalf("conversation price = %#v, %v, want %s", conversationPrice, err, want)
		}
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("retired checkout = %v", err)
	}
}

func TestCostLedgerTotalIncludesUncostedCodexSideThread(t *testing.T) {
	usage := runstate.TokenUsage{InputTokens: 10, CacheReadTokens: 20, OutputTokens: 3, Measured: 1, NoCost: 1}
	sides := runstate.SideStreamSpend{Streams: 1, Invocations: 1, Tokens: usage}
	var output bytes.Buffer
	printPrices(&output, nil, nil, &sides, false)
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "TOTAL") {
			if !strings.Contains(line, "30 input tokens (20 cached), 3 output tokens") || !strings.Contains(line, "no cost reported for 1 turn") {
				t.Fatal(line)
			}
			return
		}
	}
	t.Fatalf("no total: %s", output.String())
}
