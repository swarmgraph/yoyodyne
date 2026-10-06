package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeLedger is the spend as the test wrote it, and remembers what it was asked.
type fakeLedger struct {
	report runstate.SpendReport
	fail   error
	asked  []runstate.SpendQuery
}

func (f *fakeLedger) Spend(query runstate.SpendQuery) (runstate.SpendReport, error) {
	f.asked = append(f.asked, query)
	return f.report, f.fail
}

func spendWindow(t *testing.T, reading Spend, label string) SpendWindow {
	t.Helper()
	for _, window := range reading.Windows {
		if window.Label == label {
			return window
		}
	}
	t.Fatalf("no %q window in %+v", label, reading.Windows)
	return SpendWindow{}
}

func spendDay(t *testing.T, reading Spend, day string) SpendDay {
	t.Helper()
	for _, listed := range reading.Days {
		if listed.Day == day {
			return listed
		}
	}
	t.Fatalf("no %s in the listing of %d days", day, len(reading.Days))
	return SpendDay{}
}

// The two windows and the month come off one read of the logs: the query names
// the month it covers and the moment the rolling day is reckoned from together,
// because pricing a stream reads the whole of its log whatever window is asked
// for and asking twice would read every log twice.
func TestSpendReadsTheLedgerOnceForBothWindowsAndTheMonth(t *testing.T) {
	t.Parallel()
	today := runstate.LocalDay(noon)
	yesterday := runstate.LocalDay(noon.AddDate(0, 0, -1))
	ledger := &fakeLedger{report: runstate.SpendReport{
		Days:   30,
		Oldest: runstate.LocalDay(noon.AddDate(0, 0, -29)),
		Rows: []runstate.SpendRow{
			{Day: yesterday, StreamID: "run-1", Kind: runstate.StreamRun, Calls: 3, CostUSD: 10},
			{Day: yesterday, StreamID: "chat-1", Kind: runstate.StreamConversation, Calls: 2, CostUSD: 4},
			{Day: today, StreamID: "run-2", Kind: runstate.StreamRun, Calls: 2, CostUSD: 5.5},
			{Day: today, StreamID: "review-1", Kind: runstate.StreamReview, Calls: 1, CostUSD: 1.25},
			{Day: runstate.UndatedDay, StreamID: "exchange-1", Kind: runstate.StreamExchange, Calls: 1, CostUSD: 0.5},
		},
		// The rolling day holds all of today and the part of yesterday inside it.
		Rolling: []runstate.SpendRow{
			{Day: yesterday, StreamID: "run-1", Kind: runstate.StreamRun, Calls: 1, CostUSD: 4},
			{Day: today, StreamID: "run-2", Kind: runstate.StreamRun, Calls: 2, CostUSD: 5.5},
			{Day: today, StreamID: "review-1", Kind: runstate.StreamReview, Calls: 1, CostUSD: 1.25},
			{Day: runstate.UndatedDay, StreamID: "exchange-1", Kind: runstate.StreamExchange, Calls: 1, CostUSD: 0.5},
		},
		RollingSince:        noon.Add(-24 * time.Hour),
		Reaches:             runstate.LocalDay(noon.AddDate(0, 0, -10)),
		UnreadableExchanges: []string{"exchange-broken"},
		UnreadableReason:    "unexpected end of JSON input",
	}}
	reading := ReadSpend(context.Background(), SpendSources{Ledger: ledger, Now: func() time.Time { return noon }})
	if reading.Problem != "" {
		t.Fatalf("a problem on a readable reading: %q", reading.Problem)
	}
	if len(ledger.asked) != 1 || ledger.asked[0].Days != 30 || !ledger.asked[0].Since.Equal(noon.Add(-24*time.Hour)) || !ledger.asked[0].Now.Equal(noon) {
		t.Fatalf("the ledger was asked %+v", ledger.asked)
	}

	day := spendWindow(t, reading, "last 24 hours")
	if !day.Rolling || !day.Since.Equal(noon.Add(-24*time.Hour)) || day.Days != 0 || day.SinceDay != "" {
		t.Fatalf("the rolling window is %+v", day)
	}
	if day.CostUSD != 11.25 || day.Invocations != 5 || !day.Floor || day.Unpriced != 1 {
		t.Fatalf("the rolling window's spend %+v", day)
	}
	if len(day.Kinds) != 3 || day.Kinds[0].Kind != runstate.StreamRun || day.Kinds[0].CostUSD != 9.5 ||
		day.Kinds[1].Kind != runstate.StreamReview || day.Kinds[2].Kind != runstate.StreamExchange {
		t.Fatalf("the rolling window's kinds %+v", day.Kinds)
	}

	week := spendWindow(t, reading, "last 7 days")
	if week.Rolling || week.Days != 7 || week.SinceDay != runstate.LocalDay(noon.AddDate(0, 0, -6)) {
		t.Fatalf("the week's window is %+v", week)
	}
	if week.CostUSD != 21.25 || week.Invocations != 9 || !week.Floor {
		t.Fatalf("the week's spend %+v", week)
	}
	if len(week.Kinds) != 4 || week.Kinds[1].Kind != runstate.StreamConversation || week.Kinds[1].CostUSD != 4 {
		t.Fatalf("the week's kinds %+v", week.Kinds)
	}

	// The month: one line per local day, newest first, each priced by the same
	// summation. Undated spend is on none of them and on its own line instead,
	// because it is in every window above and on no day.
	if len(reading.Days) != 30 || reading.Days[0].Day != today || reading.Days[29].Day != runstate.LocalDay(noon.AddDate(0, 0, -29)) {
		t.Fatalf("the listing is %d days from %s", len(reading.Days), reading.Days[0].Day)
	}
	if got := spendDay(t, reading, today); got.CostUSD != 6.75 || got.Invocations != 3 || !got.Reached || len(got.Kinds) != 2 {
		t.Fatalf("today's line %+v", got)
	}
	if got := spendDay(t, reading, yesterday); got.CostUSD != 14 || got.Invocations != 5 {
		t.Fatalf("yesterday's line %+v", got)
	}
	if reading.Undated.Invocations != 1 || reading.Undated.CostUSD != 0.5 || reading.Undated.Day != runstate.UndatedDay {
		t.Fatalf("the undated line %+v", reading.Undated)
	}
	if reading.Unpriced != 1 || !reading.Floor {
		t.Fatalf("the reading does not carry the unpriced record: %+v", reading)
	}
}

// A day the recorded evidence does not reach back to says so rather than
// reading as a day nothing was spent on, because only one of the two is zero
// and a page showing the other as zero reports a figure nobody measured.
func TestSpendSaysWhichDaysTheLogDoesNotReach(t *testing.T) {
	t.Parallel()
	reaches := runstate.LocalDay(noon.AddDate(0, 0, -3))
	ledger := &fakeLedger{report: runstate.SpendReport{
		Days:    30,
		Reaches: reaches,
		Rows: []runstate.SpendRow{
			{Day: reaches, StreamID: "run-1", Kind: runstate.StreamRun, Calls: 2, CostUSD: 8},
		},
	}}
	reading := ReadSpend(context.Background(), SpendSources{Ledger: ledger, Now: func() time.Time { return noon }})
	if reading.Reaches != reaches {
		t.Fatalf("the reading reaches %q, want %q", reading.Reaches, reaches)
	}
	// Inside the reach and nothing spent is a zero; outside it is no figure at
	// all, so a surface cannot present one.
	quiet := spendDay(t, reading, runstate.LocalDay(noon))
	if !quiet.Reached || quiet.Invocations != 0 || quiet.CostUSD != 0 {
		t.Fatalf("a day inside the reach with nothing on it is %+v", quiet)
	}
	if spent := spendDay(t, reading, reaches); !spent.Reached || spent.CostUSD != 8 {
		t.Fatalf("the oldest day the log reaches is %+v", spent)
	}
	for _, day := range reading.Days {
		if (day.Day >= reaches) != day.Reached {
			t.Fatalf("%s is reported reached=%v against a reach of %s", day.Day, day.Reached, reaches)
		}
	}

	// A ledger with nothing dated in it reaches nowhere, so no day is reached.
	empty := ReadSpend(context.Background(), SpendSources{Ledger: &fakeLedger{}, Now: func() time.Time { return noon }})
	if empty.Reaches != "" {
		t.Fatalf("an empty ledger reaches %q", empty.Reaches)
	}
	for _, day := range empty.Days {
		if day.Reached {
			t.Fatalf("%s is reported reached over an empty ledger", day.Day)
		}
	}
}

// A spend that cannot be read costs the whole reading its figures and says why;
// it never reports a zero, and the listing is absent rather than a month of
// days a page would show as nothing having been spent.
func TestSpendSaysItCouldNotBeRead(t *testing.T) {
	t.Parallel()
	unpriced := ReadSpend(context.Background(), SpendSources{
		Ledger: &fakeLedger{fail: errors.New("open streams: permission denied")},
		Now:    func() time.Time { return noon },
	})
	if !strings.Contains(unpriced.Problem, "permission denied") || unpriced.Days != nil {
		t.Fatalf("an unreadable ledger reads as %+v", unpriced)
	}
	encoded, err := json.Marshal(unpriced)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"problem":"the spend could not be read: open streams: permission denied"`) {
		t.Fatalf("an unreadable ledger encodes as %s", encoded)
	}
	// The windows are still labeled, so the page has something to say the
	// failure against, and their kinds are empty rather than absent.
	if len(unpriced.Windows) != 2 || unpriced.Windows[0].Kinds == nil || unpriced.Windows[0].Label != "last 24 hours" {
		t.Fatalf("an unreadable ledger drops its windows: %+v", unpriced.Windows)
	}

	unwired := ReadSpend(context.Background(), SpendSources{Now: func() time.Time { return noon }})
	if !strings.Contains(unwired.Problem, "nothing was wired") {
		t.Fatalf("an unwired reading reads as %+v", unwired)
	}
	// A source the caller could not open is named by the reason it gave, which
	// is what the page's error state has to say: what failed, not that a wire
	// was missing.
	unopened := ReadSpend(context.Background(), SpendSources{
		LedgerProblem: "open streams: permission denied",
		Now:           func() time.Time { return noon },
	})
	if unopened.Problem != "the spend could not be opened: open streams: permission denied" {
		t.Fatalf("an unopened reading reads as %+v", unopened)
	}
}

// The cost the page reports is the spend report's own figure — the one
// derivation `yoyo status --spend` prices from — read over a real state
// directory rather than a fake: for the same fabricated runs and conversation,
// the week's cost and invocation count equal what runstate.StreamStore.Spend
// answers for that window, the rolling day holds what falls inside it, and each
// day of the listing equals the report's rows for that day. Two surfaces
// pricing one week their own way is the disagreement this test exists to make
// impossible.
func TestSpendPricesTheSameRecordsTheSpendReportPrices(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	streams, err := runstate.NewStreamStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	conversations, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}

	// Four runs: succeeded today, stopped yesterday, succeeded eight days ago,
	// and one that finished yesterday evening — inside the rolling day but on
	// yesterday's line, which is what makes the two windows different questions.
	// Each is priced by one invocation at its completion.
	record := func(started, completed time.Time, status runstate.Status, blocker string, cost float64) {
		t.Helper()
		id, err := runstate.NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		state := runstate.State{
			SchemaVersion: runstate.StateSchemaVersion,
			RunID:         id,
			ProductID:     "yoyodyne",
			RepositoryID:  "yoyodyne",
			WorkItemID:    "yoyodyne-ifd.1",
			Backend:       "claude-code",
			Status:        status,
			StartedAt:     started,
			UpdatedAt:     completed,
			CompletedAt:   &completed,
			Blocker:       blocker,
		}
		if err := store.Create(state); err != nil {
			t.Fatal(err)
		}
		for sequence, event := range []struct {
			kind    execution.EventType
			payload map[string]any
		}{
			{execution.EventRunStarted, map[string]any{"session_id": "session-developer"}},
			{execution.EventRunCompleted, map[string]any{"session_id": "session-developer", "total_cost_usd": cost,
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 20, "cache_creation_input_tokens": 30, "cache_read_input_tokens": 40}}},
		} {
			recorded, err := execution.NewEvent(id, uint64(sequence+1), completed, event.kind, "claude-code", event.payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.AppendEvent(recorded); err != nil {
				t.Fatal(err)
			}
		}
	}
	record(noon.Add(-3*time.Hour), noon.Add(-time.Hour), runstate.StatusSucceeded, "", 3.5)
	record(noon.Add(-32*time.Hour), noon.Add(-30*time.Hour), runstate.StatusFailed, "the reviewer asked for repair", 1.25)
	record(noon.Add(-9*24*time.Hour), noon.Add(-8*24*time.Hour), runstate.StatusSucceeded, "", 40)
	record(noon.Add(-22*time.Hour), noon.Add(-20*time.Hour), runstate.StatusSucceeded, "", 0.90)

	// One conversation, opened a fortnight ago, with a turn yesterday and one
	// today: it spends on both days however long ago it opened.
	chatID, err := runstate.NewConversationID()
	if err != nil {
		t.Fatal(err)
	}
	opened := noon.Add(-14 * 24 * time.Hour)
	if err := conversations.Save(runstate.Conversation{
		SchemaVersion: runstate.ConversationSchemaVersion, ConversationID: chatID, ProductID: "yoyodyne", RepositoryID: "yoyodyne",
		Role: "product-manager", Backend: "claude-code", ProviderModel: "opus", Turns: 2, StartedAt: opened, UpdatedAt: noon.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	for sequence, turn := range []struct {
		at   time.Time
		kind execution.EventType
		cost float64
	}{{opened, execution.EventRunStarted, 0}, {noon.Add(-26 * time.Hour), execution.EventRunCompleted, 2}, {noon.Add(-time.Minute), execution.EventRunCompleted, 0.75}} {
		payload := map[string]any{"session_id": "session-chat"}
		if turn.kind == execution.EventRunCompleted {
			payload["total_cost_usd"] = turn.cost
		}
		event, err := execution.NewEvent(chatID, uint64(sequence+1), turn.at, turn.kind, "claude-code", payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := conversations.AppendEvent(event); err != nil {
			t.Fatal(err)
		}
	}

	reading := ReadSpend(context.Background(), SpendSources{Ledger: streams, Now: func() time.Time { return noon }})
	if reading.Problem != "" {
		t.Fatalf("a problem over a readable state directory: %q", reading.Problem)
	}

	// The week of local days is the report's own window, added up the report's
	// own way.
	report, err := streams.Spend(runstate.SpendQuery{Days: 7, Now: noon})
	if err != nil {
		t.Fatal(err)
	}
	var cost float64
	var calls int
	for _, row := range report.Rows {
		cost += row.CostUSD
		calls += row.Calls
	}
	week := spendWindow(t, reading, "last 7 days")
	if week.CostUSD != cost || week.Invocations != calls || week.SinceDay != report.Oldest {
		t.Fatalf("the week is %+v, but the spend report prices $%.2f from %d calls since %s", week, cost, calls, report.Oldest)
	}
	if week.CostUSD != 8.4 || week.Invocations != 5 {
		t.Fatalf("the week is %+v: the fabricated spend is $3.50 and $0.75 today, and $1.25, $2.00 and $0.90 more yesterday", week)
	}

	// The rolling day holds what falls inside twenty-four hours: today's two,
	// and yesterday evening's run. It is neither today's line nor the week's,
	// which is the whole reason it is a window of its own.
	day := spendWindow(t, reading, "last 24 hours")
	if day.CostUSD != 5.15 || day.Invocations != 3 {
		t.Fatalf("the rolling day is %+v, want today's $3.50 and $0.75 with yesterday evening's $0.90", day)
	}
	if today := spendDay(t, reading, runstate.LocalDay(noon)); today.CostUSD != 4.25 || today.Invocations != 2 {
		t.Fatalf("today's line is %+v, want $3.50 and $0.75", today)
	}
	if yesterday := spendDay(t, reading, runstate.LocalDay(noon.AddDate(0, 0, -1))); yesterday.CostUSD != 4.15 || yesterday.Invocations != 3 {
		t.Fatalf("yesterday's line is %+v, want $1.25, $2.00 and $0.90", yesterday)
	}
	// Every day of the listing is the report's own rows for that day.
	month, err := streams.Spend(runstate.SpendQuery{Days: 30, Now: noon})
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range reading.Days {
		var spent float64
		for _, row := range month.Rows {
			if row.Day == listed.Day {
				spent += row.CostUSD
			}
		}
		if listed.Reached && listed.CostUSD != spent {
			t.Fatalf("%s is listed at $%.2f against the report's $%.2f", listed.Day, listed.CostUSD, spent)
		}
	}
	// The reach is the oldest priced record rather than the oldest stream: the
	// conversation opened a fortnight ago and first spent yesterday, so what
	// reaches furthest back is the run that completed eight days ago.
	oldest := runstate.LocalDay(noon.Add(-8 * 24 * time.Hour))
	if reading.Reaches != oldest {
		t.Fatalf("the reading reaches %q, want the oldest priced record's day, %q", reading.Reaches, oldest)
	}
	if listed := spendDay(t, reading, oldest); !listed.Reached || listed.CostUSD != 40 {
		t.Fatalf("the oldest priced day is %+v, want the $40 run", listed)
	}
}

func TestSpendShowsTokensAndMissingPrices(t *testing.T) {
	usage := runstate.TokenUsage{InputTokens: 3120, CacheReadTokens: 13184, OutputTokens: 5, Measured: 1, NoCost: 1}
	// Exercise the same projection used by both surfaces without asking either
	// surface to calculate its own totals.
	window := SpendWindow{}
	report := runstate.SpendReport{Rows: []runstate.SpendRow{{Calls: 1, Usage: &usage, Kind: runstate.StreamRun}}}
	sumInto(&window, report)
	if window.Tokens != usage || !strings.Contains(window.CostText, "no cost reported") || strings.Contains(window.CostText, "$0") {
		t.Fatalf("window = %+v", window)
	}
}
