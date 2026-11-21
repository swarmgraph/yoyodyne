package costevent

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The rule every reader of cost evidence shares: a line that may carry a cost
// and decodes is handed back, one that may and will not decode is counted as
// missing, and a line that cannot carry a cost is nobody's business however
// broken it is.
func TestTheReaderCountsUnreadableCostLinesAsMissing(t *testing.T) {
	var reader Reader
	priced, ok := reader.Read([]byte(`{"type":"run.completed","payload":{"session_id":"s","total_cost_usd":2.5}}`))
	if !ok || !priced.Terminal() || priced.Payload.TotalCostUSD != 2.5 || priced.NoCost() {
		t.Fatalf("Read() = %+v, %t; want the priced terminal", priced, ok)
	}
	if _, ok := reader.Read([]byte(`{"type":"process.output" not json`)); ok {
		t.Fatal("a broken line that cannot carry a cost was handed back")
	}
	if !reader.Complete() || reader.Missing() != "" {
		t.Fatalf("a broken line that cannot carry a cost made the total incomplete: %q", reader.Missing())
	}
	if _, ok := reader.Read([]byte(`{"type":"run.failed","payload":{"total_cost_usd":`)); ok {
		t.Fatal("an unreadable cost line was handed back as evidence")
	}
	if reader.Complete() || reader.Unreadable() != 1 {
		t.Fatalf("Unreadable() = %d, want the cost line counted as missing", reader.Unreadable())
	}
	if missing := reader.Missing(); !strings.Contains(missing, "1 line of the event log") || !strings.Contains(missing, "at least") {
		t.Fatalf("Missing() = %q, want what is missing said in words", missing)
	}
}

// A terminal recorded before the backends said whether the provider priced it
// is priced where it carries a figure at all.
func TestATerminalWithoutTheFlagIsPricedByWhetherItCarriesAFigure(t *testing.T) {
	var reader Reader
	unpriced, _ := reader.Read([]byte(`{"type":"run.completed","payload":{"session_id":"s"}}`))
	if !unpriced.NoCost() {
		t.Fatal("a terminal carrying no figure read as priced")
	}
	duplicate, ok := reader.Read([]byte(`{"type":"process.output","payload":{"anomaly":"` + execution.DuplicateTerminalAnomaly + `","total_cost_usd":1}}`))
	if !ok || !duplicate.DuplicateTerminal() || duplicate.Session("previous") != "previous" {
		t.Fatalf("Read() = %+v, %t; want a duplicate terminal priced against the session before it", duplicate, ok)
	}
}
