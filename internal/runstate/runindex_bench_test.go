package runstate

// What the routine reads cost at the size of history the harness actually
// keeps, before the indexes and after.
//
// The history is the product's own as the outside review found it: 4,420 run
// records of about ten kilobytes each, a dozen of them in flight, spread over
// half a year. The event logs are scaled down: the real ones average about a
// quarter of a megabyte, and writing a gigabyte for every benchmark would make
// the benchmark the slow part, so each log here holds a tenth of that, and the
// cost of reading every log scales with what the real ones hold. Run with
//
//	go test ./internal/runstate -run '^$' -bench 'RoutineReads' -benchtime 3x
//
// and compare each "every-record" or "every-log" figure with the one beside it.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const (
	benchRuns         = 4420
	benchRunsInFlight = 12
	benchLogEvents    = 60
	benchHistoryDays  = 180
)

// benchHistory writes the history straight to the directory, as the store
// would have left it, and builds the run index once.
func benchHistory(b *testing.B, withLogs bool) (*Store, *StreamStore, time.Time) {
	b.Helper()
	root := b.TempDir()
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		b.Fatalf("NewStore() error = %v", err)
	}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		b.Fatalf("MkdirAll() error = %v", err)
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.Local)
	padding := strings.Repeat("what the run recorded about itself ", 120)
	chatter := strings.Repeat("a line of what the developer said it was doing ", 80)
	for index := 0; index < benchRuns; index++ {
		status := StatusSucceeded
		if index < benchRunsInFlight {
			status = StatusRunning
		}
		state := testState(b, status)
		state.WorkItemID = fmt.Sprintf("yoyodyne-bench.%d", index)
		started := now.Add(-time.Duration(index) * benchHistoryDays * 24 * time.Hour / benchRuns)
		state.StartedAt, state.UpdatedAt = started, started
		if status.Terminal() {
			state.CompletedAt = &started
		}
		state.WorkItemTitle = padding[:200]
		state.WorkflowDivergence = padding
		encoded, err := encodeRecord("run state", state)
		if err != nil {
			b.Fatalf("encodeRecord() error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(store.Root(), state.RunID+".json"), encoded, 0o600); err != nil {
			b.Fatalf("WriteFile() error = %v", err)
		}
		if !withLogs {
			continue
		}
		var log strings.Builder
		for sequence := 1; sequence <= benchLogEvents; sequence++ {
			eventType, payload := execution.EventAgentMessage, any(map[string]any{"text": chatter})
			if sequence%15 == 0 {
				eventType, payload = execution.EventRunCompleted, invocationPayload(float64(sequence)/10, 10, 20, 30, 40)
			}
			event, err := execution.NewEvent(state.RunID, uint64(sequence), started.Add(time.Duration(sequence)*time.Minute), eventType, "claude-code", payload)
			if err != nil {
				b.Fatalf("NewEvent() error = %v", err)
			}
			line, err := encodeEvent(event)
			if err != nil {
				b.Fatalf("encodeEvent() error = %v", err)
			}
			log.Write(line)
		}
		if err := os.WriteFile(filepath.Join(store.Root(), state.RunID+eventLogSuffix), []byte(log.String()), 0o600); err != nil {
			b.Fatalf("WriteFile() error = %v", err)
		}
	}
	if err := store.RebuildRunIndex(); err != nil {
		b.Fatalf("RebuildRunIndex() error = %v", err)
	}
	streams, err := NewStreamStore(root, "yoyodyne")
	if err != nil {
		b.Fatalf("NewStreamStore() error = %v", err)
	}
	return store, streams, now
}

func BenchmarkRoutineReadsActiveRuns(b *testing.B) {
	store, _, _ := benchHistory(b, false)
	b.Run("every-record", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			active, err := store.scan("incomplete", func(state State) bool { return state.Status.InFlight() })
			if err != nil || len(active) != benchRunsInFlight {
				b.Fatalf("scan() = %d, %v", len(active), err)
			}
		}
	})
	b.Run("index", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			active, err := store.Incomplete()
			if err != nil || len(active) != benchRunsInFlight {
				b.Fatalf("Incomplete() = %d, %v", len(active), err)
			}
		}
	})
}

func BenchmarkRoutineReadsItemLookup(b *testing.B) {
	store, _, _ := benchHistory(b, false)
	item := fmt.Sprintf("yoyodyne-bench.%d", benchRuns/2)
	b.Run("every-record", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			runs, err := store.scan("recorded", func(state State) bool { return state.WorkItemID == item })
			if err != nil || len(runs) != 1 {
				b.Fatalf("scan() = %d, %v", len(runs), err)
			}
		}
	})
	b.Run("index", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			runs, err := store.Runs(item)
			if err != nil || len(runs) != 1 {
				b.Fatalf("Runs() = %d, %v", len(runs), err)
			}
		}
	})
}

// The dashboard's spend box: the last thirty local days and the last
// twenty-four hours, asked about once a minute.
func BenchmarkRoutineReadsSpend(b *testing.B) {
	_, streams, now := benchHistory(b, true)
	query := SpendQuery{Days: 30, Since: now.Add(-24 * time.Hour), Now: now}
	b.Run("every-log", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := spendReadingEveryLog(streams, query); err != nil {
				b.Fatalf("spend error = %v", err)
			}
		}
	})
	if _, err := streams.Spend(query); err != nil {
		b.Fatalf("Spend() error = %v", err)
	}
	b.Run("kept", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := streams.Spend(query); err != nil {
				b.Fatalf("Spend() error = %v", err)
			}
		}
	})
}

// spendReadingEveryLog is the spend report as it was made before finished
// streams were kept: every selected log read whole, every run's record read
// for its status, on every report.
func spendReadingEveryLog(s *StreamStore, query SpendQuery) (SpendReport, error) {
	found, err := s.choose(StreamQuery{Kinds: query.kinds(), Match: query.Match})
	if err != nil {
		return SpendReport{}, err
	}
	report := SpendReport{Streams: len(found), RollingSince: query.Since}
	if query.Days > 0 {
		report.Days = query.Days
		report.Oldest = oldestLocalDay(query.Now, query.Days)
	}
	var current map[string]ConversationIdentity
	for _, entry := range found {
		if current, err = s.currentFor(entry, current); err != nil {
			return SpendReport{}, err
		}
		stream, scanned, err := s.describe(entry, current, true)
		if err != nil {
			return SpendReport{}, err
		}
		rows := spendByDay(stream, scanned.invocations)
		report.reaching(rows)
		report.take(rows)
		report.roll(query.Since, func() []SpendRow {
			return spendByDay(stream, invocationsFrom(scanned.invocations, query.Since))
		})
	}
	sort.Slice(report.Rows, func(i, j int) bool { return report.Rows[i].At.Before(report.Rows[j].At) })
	return report, nil
}

// The kept spend gives the figures reading every log gives: the same rows, the
// same rolling window, and the same reach.
func TestKeptSpendMatchesReadingEveryLog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, _, _ := streamStores(t, root)
	streams := newStreamStore(t, root)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.Local)
	for index := 0; index < 30; index++ {
		status := StatusSucceeded
		if index < 3 {
			status = StatusRunning
		}
		state := testState(t, status)
		state.WorkItemID = fmt.Sprintf("yoyodyne-kept.%d", index)
		started := now.Add(-time.Duration(index) * 30 * time.Hour)
		state.StartedAt, state.UpdatedAt = started, started
		if status.Terminal() {
			state.CompletedAt = &started
		}
		if err := runs.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		appendStreamEvent(t, runs, state.RunID, 1, execution.EventRunStarted, started, nil)
		appendStreamEvent(t, runs, state.RunID, 2, execution.EventRunCompleted, started.Add(time.Hour), invocationPayload(1.25, 1, 2, 3, 4))
		appendStreamEvent(t, runs, state.RunID, 3, execution.EventRunCompleted, started.Add(20*time.Hour), invocationPayload(2, 1, 2, 3, 4))
	}
	for _, query := range []SpendQuery{
		{Now: now},
		{Days: 7, Now: now, Since: now.Add(-24 * time.Hour)},
		{Days: 30, Now: now, Since: now.Add(-24 * time.Hour)},
		{Days: 1, Now: now},
	} {
		want, err := spendReadingEveryLog(streams, query)
		if err != nil {
			t.Fatalf("reading every log error = %v", err)
		}
		// Twice: the first report keeps what it read, the second reads what
		// was kept.
		for pass := 0; pass < 2; pass++ {
			got, err := streams.Spend(query)
			if err != nil {
				t.Fatalf("Spend() error = %v", err)
			}
			if got.Reaches != want.Reaches || len(got.Rows) != len(want.Rows) || len(got.Rolling) != len(want.Rolling) {
				t.Fatalf("query %+v pass %d: reaches %q rows %d rolling %d, want %q %d %d", query, pass, got.Reaches, len(got.Rows), len(got.Rolling), want.Reaches, len(want.Rows), len(want.Rolling))
			}
			if gotTotal, wantTotal := got.Totals(), want.Totals(); gotTotal.CostUSD != wantTotal.CostUSD || gotTotal.Calls != wantTotal.Calls {
				t.Fatalf("query %+v pass %d: totals %+v, want %+v", query, pass, gotTotal, wantTotal)
			}
			if gotRolling, wantRolling := got.RollingWindow().Totals(), want.RollingWindow().Totals(); gotRolling.CostUSD != wantRolling.CostUSD {
				t.Fatalf("query %+v pass %d: rolling %+v, want %+v", query, pass, gotRolling, wantRolling)
			}
		}
	}
}
