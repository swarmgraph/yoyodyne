package runstate

import (
	"encoding/json"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"strings"
	"testing"
	"time"
)

func TestMissingPricesAndUsageSurviveTotals(t *testing.T) {
	var unpriced, priced, absent pricedEvent
	for _, sample := range []struct {
		text string
		into *pricedEvent
	}{
		{`{"payload":{"usage":{"input_tokens":3120,"cache_read_input_tokens":13184,"output_tokens":5}}}`, &unpriced},
		{`{"payload":{"total_cost_usd":2,"usage":{"input_tokens":10,"output_tokens":2}}}`, &priced},
		{`{"payload":{}}`, &absent},
	} {
		if err := json.Unmarshal([]byte(sample.text), sample.into); err != nil {
			t.Fatal(err)
		}
	}
	usage := unpriced.tokens()
	if usage.NoCost != 1 || usage.InputTotal() != 16304 || usage.Measured != 1 {
		t.Fatal(usage)
	}
	if text := usage.CostText(0); strings.Contains(text, "$0") || !strings.Contains(text, "no cost reported for 1 turn") {
		t.Fatal(text)
	}
	usage.Merge(priced.tokens())
	if text := usage.CostText(2); !strings.Contains(text, "$2.00 reported") || !strings.Contains(text, "no cost reported for 1 turn") {
		t.Fatal(text)
	}
	if missing := absent.tokens(); missing.Measured != 0 || missing.Unreported != 1 || !strings.Contains(missing.CostText(0), "token usage not reported") {
		t.Fatal(missing)
	}
	var zero pricedEvent
	if err := json.Unmarshal([]byte(`{"payload":{"total_cost_usd":0,"usage":{}}}`), &zero); err != nil {
		t.Fatal(err)
	}
	if zero.tokens().Measured != 1 || zero.tokens().NoCost != 0 {
		t.Fatal(zero.tokens())
	}
	totals := (SpendReport{Rows: []SpendRow{{Calls: 2, CostUSD: 2, Usage: &usage}}}).Totals()
	if totals.Usage != usage || totals.CostUSD != 2 {
		t.Fatal(totals)
	}
}

func TestExchangeRoundCarriesUnpricedUsage(t *testing.T) {
	reported := false
	round := exchange.Round{Usage: json.RawMessage(`{"input_tokens":3120,"cache_read_input_tokens":13184,"output_tokens":5}`), CostReported: &reported}
	rows := roundsByDay("exchange", "open", domain.RoleArchitect, []exchange.Round{round}, time.Time{})
	totals := (SpendReport{Rows: rows}).Totals()
	if totals.Usage.NoCost != 1 || totals.Usage.InputTotal() != 16304 || len(totals.ByRole) != 1 || totals.ByRole[0].Usage != totals.Usage {
		t.Fatalf("totals = %+v", totals)
	}
}

func TestOlderPricedExchangeAndCodexTurnKeepReportedDollars(t *testing.T) {
	legacy := exchange.Round{CostUSD: 2}
	priced := exchangeTokens(legacy)
	if priced.Priced != 1 || priced.Measured != 0 || priced.Unreported != 0 {
		t.Fatal(priced)
	}
	codex := TokenUsage{InputTokens: 10, OutputTokens: 2, Measured: 1, NoCost: 1}
	rows := roundsByDay("legacy", "closed", domain.RoleArchitect, []exchange.Round{legacy}, time.Time{})
	rows = append(rows, SpendRow{Calls: 1, Usage: &codex})
	totals := (SpendReport{Rows: rows}).Totals()
	if totals.Usage.Priced != 1 {
		t.Fatal(totals)
	}
	if text := totals.Usage.CostText(totals.CostUSD); !strings.Contains(text, "$2.00 reported") || !strings.Contains(text, "no cost reported for 1 turn") {
		t.Fatal(text)
	}
	priced.Merge(codex)
	if text := priced.CostText(2); !strings.Contains(text, "$2.00 reported") {
		t.Fatal(text)
	}
}
