package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/pricing"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeCalls struct {
	lines []runstate.Spend
	fail  error
}

func (f fakeCalls) List() ([]runstate.Spend, error) { return f.lines, f.fail }

func call(at time.Time, figure pricing.Figure) runstate.Spend {
	classification := runstate.SpendUnknown
	if figure.Kind == pricing.KindReported {
		classification = runstate.SpendKnown
	}
	return runstate.Spend{At: at, Classification: classification, Cost: &figure, ConversationID: "chat-1"}
}

// Each window's cost by kind is the one summation over the calls inside it, so
// the page, the terminal, and anything else reading the ledger cannot add the
// same calls up differently.
func TestEachWindowCarriesItsCallsByKindOfCost(t *testing.T) {
	t.Parallel()

	noon := time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local)
	lines := []runstate.Spend{
		call(noon.Add(-time.Hour), pricing.Figure{Kind: pricing.KindReported, USD: 1}),
		call(noon.Add(-2*time.Hour), pricing.Figure{Kind: pricing.KindEstimated, USD: 2, Entry: "e", Schedule: "v1"}),
		call(noon.Add(-72*time.Hour), pricing.Figure{Kind: pricing.KindComparison, USD: 4, Entry: "e", Schedule: "v1"}),
		call(noon.Add(-73*time.Hour), pricing.Figure{Kind: pricing.KindUnknown, Unknown: "no rate entry covers it"}),
		call(noon.Add(-30*24*time.Hour), pricing.Figure{Kind: pricing.KindReported, USD: 100}),
	}
	reading := ReadSpend(context.Background(), SpendSources{Ledger: &fakeLedger{}, Calls: fakeCalls{lines: lines}, Now: func() time.Time { return noon }})
	for _, window := range reading.Windows {
		since := window.Since
		want := runstate.KindsOf(lines, func(line runstate.Spend) bool { return !line.At.Before(since) })
		if window.CostKinds == nil || window.CostKinds.Calls != want.Calls || window.CostKinds.ReportedUSD != want.ReportedUSD || window.CostKindsText != want.Text() {
			t.Fatalf("%s: kinds = %+v, want %+v", window.Label, window.CostKinds, want)
		}
	}
	day := spendWindow(t, reading, "last 24 hours").CostKinds
	if day.Calls != 2 || day.ReportedUSD != 1 || day.EstimatedUSD != 2 || day.UnknownCalls != 0 {
		t.Fatalf("day = %+v", day)
	}
	week := spendWindow(t, reading, "last 7 days").CostKinds
	if week.Calls != 4 || week.ComparisonUSD != 4 || week.UnknownCalls != 1 || week.ReportedUSD != 1 {
		t.Fatalf("week = %+v", week)
	}
}

func TestALedgerThatCannotBeReadLeavesTheKindsAbsentAndSaysWhy(t *testing.T) {
	t.Parallel()

	noon := time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local)
	reading := ReadSpend(context.Background(), SpendSources{Ledger: &fakeLedger{}, Calls: fakeCalls{fail: errors.New("disk")}, Now: func() time.Time { return noon }})
	if !strings.Contains(reading.CostKindsProblem, "disk") || reading.Windows[0].CostKinds != nil {
		t.Fatalf("reading = %+v", reading)
	}
	unwired := ReadSpend(context.Background(), SpendSources{Ledger: &fakeLedger{}, Now: func() time.Time { return noon }})
	if unwired.CostKindsProblem == "" || unwired.Windows[0].CostKinds != nil {
		t.Fatalf("unwired = %+v", unwired)
	}
}
