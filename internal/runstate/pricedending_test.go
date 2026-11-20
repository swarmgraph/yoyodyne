package runstate

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// One run log, a priced terminal in it, and after it a line that names a
// terminal and will not decode. The price of the run and the spend report read
// the same log by the same rule (internal/cost/costevent): both keep the cost
// that could be read, and both say the total is missing something, rather than
// one calling the whole run unknown and the other skipping the line unremarked.
func TestAPricedEndingIsKeptAndTheTotalMarkedIncompleteOnEverySurface(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, _, _ := streamStores(t, root)
	streams := newStreamStore(t, root)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.Local)

	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.41"
	state.ProviderSessionID = "session-developer"
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendStreamEvent(t, runs, state.RunID, 1, execution.EventRunCompleted, now, map[string]any{
		"role":           string(domain.RoleDeveloper),
		"session_id":     "session-developer",
		"total_cost_usd": 2.5,
		"usage":          map[string]any{"input_tokens": 10, "output_tokens": 20},
	})
	appendRawLine(t, runs, state.RunID, `{"type":"run.failed","payload":{"total_cost_usd":`)
	// A line that cannot be cost evidence is not missing evidence, however
	// broken it is.
	appendRawLine(t, runs, state.RunID, `{"type":"process.output" not json`)

	price, err := runs.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	run := price.Runs[0]
	if !run.Known() || run.Complete() || run.CostUSD != 2.5 || run.Invocations != 1 {
		t.Fatalf("run = %#v, want the readable 2.5 kept and the run marked incomplete", run)
	}
	if !strings.Contains(run.Incomplete, "1 line of the event log") || !strings.Contains(run.Incomplete, "at least") {
		t.Fatalf("Incomplete = %q, want it to say what is missing in words", run.Incomplete)
	}
	if price.TotalUSD != 2.5 || price.UnknownRuns != 1 || price.Priced() {
		t.Fatalf("item = %#v, want 2.5 as a total that is at least what was spent", price)
	}

	// Asked twice, because a finished stream's spend is kept after the first
	// report: one whose cost lines could not all be read must not be kept as
	// though they had.
	for attempt := 1; attempt <= 2; attempt++ {
		report, err := streams.Spend(SpendQuery{Kinds: []StreamKind{StreamRun}, Now: now})
		if err != nil {
			t.Fatalf("Spend() attempt %d error = %v", attempt, err)
		}
		if total := report.Totals(); total.CostUSD != 2.5 || total.Calls != 1 {
			t.Fatalf("attempt %d totals = %+v, want the readable 2.5 kept", attempt, total)
		}
		if !report.Floor() || len(report.IncompleteStreams) != 1 || report.IncompleteStreams[0] != state.RunID {
			t.Fatalf("attempt %d report = %+v, want the run named as incomplete and the total marked", attempt, report)
		}
		if !strings.Contains(report.IncompleteReason, state.RunID) {
			t.Fatalf("attempt %d reason = %q, want it to name the stream", attempt, report.IncompleteReason)
		}
	}
}

// A log every cost line of which reads is complete on both surfaces, so the
// rule marks nothing it should not.
func TestALogWhoseCostLinesAllReadIsComplete(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, _, _ := streamStores(t, root)
	streams := newStreamStore(t, root)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.Local)

	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.41"
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendStreamEvent(t, runs, state.RunID, 1, execution.EventRunCompleted, now, map[string]any{
		"role": string(domain.RoleDeveloper), "session_id": "session-developer", "total_cost_usd": 1.0,
	})
	appendRawLine(t, runs, state.RunID, `not an event at all`)

	price, err := runs.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if !price.Runs[0].Complete() || !price.Priced() || price.TotalUSD != 1.0 {
		t.Fatalf("price = %#v, want a complete 1.0", price)
	}
	report, err := streams.Spend(SpendQuery{Kinds: []StreamKind{StreamRun}, Now: now})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if report.Floor() || len(report.IncompleteStreams) != 0 {
		t.Fatalf("report = %+v, want a complete total", report)
	}
}

func appendRawLine(t *testing.T, store *Store, runID, line string) {
	t.Helper()
	path, err := store.eventPath(runID)
	if err != nil {
		t.Fatalf("eventPath() error = %v", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		t.Fatalf("append to event log: %v", err)
	}
}
