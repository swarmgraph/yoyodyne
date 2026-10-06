package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The surface this absorbed was the operator's daily one, and it shipped only to
// people working from a checkout. So what matters first is that the verb the
// install carries answers the same three questions: what has been running, what
// it spent, and what one of them is doing right now.
func TestStatusListsEveryKindOfStream(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// Nothing recorded is an answer rather than an empty listing.
	stdout, stderr, code := runCLI(t, "status", "--list", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	// An empty answer names the directory it read, because the state root comes
	// from the environment and a true answer about the wrong directory is the one
	// failure this surface cannot afford.
	if !strings.Contains(stdout, "no runs, conversations, branch reviews, or side threads are recorded under "+home.ProductDirectory(stateRoot, "yoyodyne")) {
		t.Fatalf("stdout = %q", stdout)
	}

	runID := recordStreamRun(t, stateRoot, runstate.StatusRunning, streamStart, 0)
	chatID := recordStreamConversation(t, stateRoot, streamStart, nil)
	reviewID := recordStreamReview(t, stateRoot, streamStart, 0)

	stdout, stderr, code = runCLI(t, "status", "--list", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{runID, "run", "running", chatID, "conversation", reviewID, "review", "reviewing"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// Narrowing to one kind is what an operator who only cares about runs asks
	// for, and it leaves the other two out rather than merely ordering them
	// lower.
	stdout, stderr, code = runCLI(t, "status", "--list", "--kind", "chats", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, chatID) || strings.Contains(stdout, runID) {
		t.Fatalf("chats only listed %q", stdout)
	}
	// --limit bounds the listing, and says so.
	stdout, _, _ = runCLI(t, "status", "--list", "--limit", "1", "--config", configPath)
	if strings.Count(stdout, "\n") != 2 {
		t.Fatalf("--limit 1 listed %q, want the header and one stream", stdout)
	}

	// A machine-readable listing carries the same facts, so a script never has to
	// parse the columns.
	stdout, stderr, code = runCLI(t, "status", "--list", "--json", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var listed streamListOutput
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if len(listed.Streams) != 3 {
		t.Fatalf("JSON listed %d stream(s), want three", len(listed.Streams))
	}
}

// An empty answer under --kind has to name the kinds actually queried. Worded
// for the unnarrowed question, `--list --kind reviews` on a machine with fifty
// runs and no branch reviews said that nothing at all was recorded, which is
// exactly the misreading this surface cannot afford: the operator was told their
// state directory was empty when it was not.
func TestStatusEmptyAnswerNamesTheKindsItWasAskedAbout(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	recordStreamRun(t, stateRoot, runstate.StatusSucceeded, streamStart, 3)
	root := home.ProductDirectory(stateRoot, "yoyodyne")

	for _, query := range []struct {
		args []string
		want string
	}{
		{[]string{"--list", "--kind", "reviews"}, "no branch reviews are recorded under " + root},
		{[]string{"--list", "--kind", "chats"}, "no conversations are recorded under " + root},
		{[]string{"--spend", "--kind", "reviews"}, "no branch reviews are recorded under " + root},
		{[]string{"--spend", "--kind", "exchanges"}, "no exchanges are recorded under " + root},
		{[]string{"--spend", "--kind", "chats", "chat-nothing"}, "no conversation matching \"chat-nothing\" is recorded under " + root},
	} {
		args := append(append([]string{"status"}, query.args...), "--config", configPath)
		stdout, stderr, _ := runCLI(t, args...)
		if !strings.Contains(stdout+stderr, query.want) {
			t.Fatalf("%v answered stdout = %q, stderr = %q; want %q", query.args, stdout, stderr, query.want)
		}
		// The unnarrowed wording would claim the run is not there.
		if strings.Contains(stdout+stderr, "no runs, conversations") {
			t.Fatalf("%v answered for the unnarrowed question: stdout = %q, stderr = %q", query.args, stdout, stderr)
		}
	}
	// The unnarrowed spend question names every kind, because it covers every
	// one; the unnarrowed listing names only the kinds it lists.
	stdout, _, _ := runCLI(t, "status", "--spend", "nothing-named-this", "--config", configPath)
	_, stderr, _ := runCLI(t, "status", "--spend", "nothing-named-this", "--config", configPath)
	if !strings.Contains(stdout+stderr, `no run, conversation, branch review, side thread, or exchange matching "nothing-named-this"`) {
		t.Fatalf("an unnarrowed spend query answered stdout = %q, stderr = %q", stdout, stderr)
	}
	// A narrowed kind with streams recorded but nothing spent in the window is the
	// other empty, and it still says which window it is empty about.
	stdout, _, code := runCLI(t, "status", "--spend", "--kind", "runs", "1", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "no completed provider invocations since") {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
}

// The spend rendering is the one the operator has been reading and asked to
// keep: grouped by the local day the money was spent on, each day's group closed
// by that day's spend, the total under a rule, and the split saying how much of
// it was each kind of work.
func TestStatusSpendGroupsByTheDayItWasSpent(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// A machine that has recorded nothing says so, rather than reporting an empty
	// week and inviting a wider window that would be just as empty.
	stdout, _, code := runCLI(t, "status", "--spend", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "no runs, conversations, branch reviews, side threads, or exchanges are recorded under "+stateRoot) {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
	// A machine that has recorded something but spent nothing in the days asked
	// about says which window it is empty about, because a wider one would answer
	// differently.
	recordStreamRun(t, stateRoot, runstate.StatusRunning, streamStart, 0)
	stdout, _, code = runCLI(t, "status", "--spend", "1", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "no completed provider invocations since") {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}

	today := time.Now()
	runID := recordStreamRun(t, stateRoot, runstate.StatusSucceeded, today.Add(-2*time.Hour), 12.5)
	chatID := recordStreamConversation(t, stateRoot, today.AddDate(0, 0, -14), []time.Time{today.AddDate(0, 0, -14), today})
	reviewID := recordStreamReview(t, stateRoot, today.Add(-time.Hour), 2)

	stdout, stderr, code := runCLI(t, "status", "--spend", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		runstate.LocalDay(today) + "  (today)",
		runID, reviewID, chatID,
		runstate.LocalDay(today) + " total",
		"TOTAL (last 7 days)",
		"cache reads",
		"cost: $15.50",
		"runs: $12.50 from 1 invocation(s)",
		"conversations: $1.00 from 1 turn(s)",
		"branch reviews: $2.00 from 1 invocation(s)",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	// A report with everything in front of it prints exactly the figure, with no
	// floor mark and nothing said about exchanges.
	if strings.Contains(stdout, "≥") || strings.Contains(stdout, "exchange") {
		t.Fatalf("stdout = %q, want no floor mark and no exchange line on a report with none", stdout)
	}
	// The fortnight-old turn of that conversation is outside a week, and only the
	// turn is: the same conversation is still reported for what it spent today.
	if strings.Count(stdout, chatID) != 1 {
		t.Fatalf("stdout = %q, want the conversation's out-of-window turn left out", stdout)
	}

	// A number asks for a different count of days, and it reaches the older turn
	// the default window left out.
	stdout, stderr, code = runCLI(t, "status", "--spend", "30", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if strings.Count(stdout, chatID) != 2 || !strings.Contains(stdout, "TOTAL (last 30 days)") {
		t.Fatalf("a thirty day window = %q", stdout)
	}

	// Naming a stream prices that one whatever day it ran on, because an id has
	// already chosen what to show.
	stdout, stderr, code = runCLI(t, "status", "--spend", chatID[:12], "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if strings.Count(stdout, chatID) != 2 || strings.Contains(stdout, runID) {
		t.Fatalf("naming a conversation reported %q", stdout)
	}
	if strings.Contains(stdout, "last") {
		t.Fatalf("a named stream was still windowed: %q", stdout)
	}

	// A machine-readable report is the same figures, and it says which window it
	// covered so a reader can tell an empty week from an empty machine.
	stdout, stderr, code = runCLI(t, "status", "--spend", "--json", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var priced spendOutput
	if err := json.Unmarshal([]byte(stdout), &priced); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if priced.Report.Days != 7 || priced.Report.Oldest == "" || len(priced.Report.Rows) != 3 {
		t.Fatalf("JSON report = %+v", priced.Report)
	}

	// A stream named but not recorded is a question that could not be asked,
	// rather than a machine that spent nothing.
	_, stderr, code = runCLI(t, "status", "--spend", "chat-nothing", "--config", configPath)
	if code != 1 || !strings.Contains(stderr, "no run, conversation, branch review, side thread, or exchange matching") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

// What the roles spent asking each other is in the report beside the streams,
// rendered the way the script rendered it: an exchange row says "-" where a
// stream's row says its tokens, carries how the exchange ended where a run
// carries its status, and is named as its own kind in the split. A record that
// cannot be read is counted and named, and the total it is missing from is
// marked as a floor rather than printed as a price — which is the answer `yoyo
// cost` gives for the same record.
func TestStatusSpendPricesExchangesBesideTheStreams(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	today := time.Now()
	chatID := recordStreamConversation(t, stateRoot, today.Add(-time.Hour), []time.Time{today.Add(-time.Hour)})
	askedID := recordStreamExchange(t, stateRoot, 'a', []time.Time{today.AddDate(0, 0, -2), today}, []float64{0.6, 0.4})
	unreadableID := "exchange-" + strings.Repeat("b", 32)

	stdout, stderr, code := runCLI(t, "status", "--spend", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"cost: $2.00",
		"conversations: $1.00 from 1 turn(s)   exchanges: $1.00 from 2 round(s)",
		"an exchange records what the provider charged and not what it used, so its rows carry no tokens",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	// A round counts on the day it was answered, so a thread answered on two days
	// gives a row to each of them.
	if strings.Count(stdout, askedID) != 2 {
		t.Fatalf("stdout = %q, want one row per day the exchange was answered on", stdout)
	}
	row := ""
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, askedID) {
			row = line
			break
		}
	}
	fields := strings.Fields(row)
	// id, the four fields of the moment, calls, four token columns, USD, status.
	if len(fields) != 12 || strings.Join(fields[6:10], " ") != "- - - -" || fields[11] != string(exchange.OutcomeResolved) {
		t.Fatalf("exchange row = %q, want the token columns unstated and the outcome as its status", row)
	}
	// Naming an exchange prices it on its own, and a report of nothing but
	// asking says why it reports no tokens rather than dividing by none.
	stdout, _, code = runCLI(t, "status", "--spend", askedID[:17], "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "cost: $1.00; no token usage is recorded for what this covers") || strings.Contains(stdout, chatID) {
		t.Fatalf("naming an exchange reported code = %d, stdout = %q", code, stdout)
	}
	// Narrowing to a kind that is followed narrows the asking out with it.
	stdout, _, _ = runCLI(t, "status", "--spend", "--kind", "chats", "--config", configPath)
	if !strings.Contains(stdout, "cost: $1.00") || strings.Contains(stdout, askedID) {
		t.Fatalf("conversations only reported %q", stdout)
	}

	// A record nobody can parse did not cost nothing.
	exchanges, err := runstate.NewExchangeStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewExchangeStore() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(exchanges.Root(), unreadableID+".json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	stdout, stderr, code = runCLI(t, "status", "--spend", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		askedID,
		"cost: ≥ $2.00",
		"1 exchange record(s) could not be read; they are left out rather than counted as nothing, so any total here is a floor:",
		"  " + unreadableID,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	// The floor is carried machine-readably too, so a script never reads a
	// floor as a price.
	stdout, _, _ = runCLI(t, "status", "--spend", "--json", "--config", configPath)
	var priced spendOutput
	if err := json.Unmarshal([]byte(stdout), &priced); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if len(priced.Report.UnreadableExchanges) != 1 || priced.Report.UnreadableExchanges[0] != unreadableID || priced.Report.Exchanges != 1 {
		t.Fatalf("JSON report = %+v", priced.Report)
	}
	// A product whose roles have only ever asked each other things, none of it
	// readable, still says what it could not read rather than reporting an empty
	// window.
	if err := os.Remove(filepath.Join(exchanges.Root(), askedID+".json")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	stdout, _, code = runCLI(t, "status", "--spend", "--kind", "exchanges", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "1 exchange record(s) could not be read") || strings.Contains(stdout, "no completed provider invocations") {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
}

// fixedLedger is a spend report handed to the dashboard's read model as it
// stands, so the read model and the terminal can be asked about one fixture.
type fixedLedger struct{ report runstate.SpendReport }

func (f fixedLedger) Spend(runstate.SpendQuery) (runstate.SpendReport, error) {
	return f.report, nil
}

// The total and its split by kind are added up once, by the report, and
// `yoyo status --spend` and the dashboard's read model both read that sum: the
// terminal prints no figure the dashboard would show differently. This holds
// the two against one report carrying a row of every kind the report prices,
// so a kind either surface summed for itself would show up as a figure the
// other does not have.
func TestStatusSpendPrintsTheFiguresTheReadModelReads(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.Local)
	report := runstate.SpendReport{Days: 7, Oldest: runstate.LocalDay(now.AddDate(0, 0, -6))}
	for index, kind := range runstate.EveryPricedKind {
		row := runstate.SpendRow{
			Day:      runstate.LocalDay(now),
			StreamID: fmt.Sprintf("%s-%d", kind, index),
			Kind:     kind,
			Calls:    index + 1,
			CostUSD:  float64(index+1) * 1.25,
		}
		// An exchange's row records what the provider charged and not what it
		// used, so it is the one row with no usage to add.
		if kind != runstate.StreamExchange {
			row.Usage = &runstate.TokenUsage{InputTokens: int64(100 * (index + 1)), OutputTokens: int64(10 * (index + 1))}
		}
		report.Rows = append(report.Rows, row)
	}

	reading := readmodel.ReadSpend(context.Background(), readmodel.SpendSources{
		Ledger: fixedLedger{report: report},
		Now:    func() time.Time { return now },
	})
	if reading.Problem != "" {
		t.Fatalf("the read model could not read the spend: %s", reading.Problem)
	}
	var week readmodel.SpendWindow
	for _, window := range reading.Windows {
		if window.Label == "last 7 days" {
			week = window
		}
	}
	if len(week.Kinds) != len(runstate.EveryPricedKind) {
		t.Fatalf("the week's split %+v, want one share per kind the report prices", week.Kinds)
	}

	var terminal bytes.Buffer
	printSpendTotals(&terminal, report)
	printed := terminal.String()

	// The one total: the read model's invocation count and cost are the figures
	// on the terminal's TOTAL line and its cost line.
	total, split := "", ""
	for _, line := range strings.Split(printed, "\n") {
		switch {
		case strings.HasPrefix(line, "TOTAL (last 7 days)"):
			total = line
		case strings.Contains(line, " from "):
			split = line
		}
	}
	// TOTAL, the three words of the window, calls, the four token columns, USD.
	fields := strings.Fields(total)
	if len(fields) != 10 || fields[4] != strconv.Itoa(week.Invocations) || fields[9] != fmt.Sprintf("%.2f", week.CostUSD) {
		t.Fatalf("terminal's total line = %q, the read model's window is %+v", total, week)
	}
	if want := fmt.Sprintf("cost: $%.2f", week.CostUSD); !strings.Contains(printed, want) {
		t.Fatalf("terminal = %q, want it to contain %q", printed, want)
	}
	// The one split: each kind's share as the read model read it is the share
	// the terminal prints for that kind, in the same order.
	if split == "" {
		t.Fatalf("terminal = %q, want a line splitting the total by kind", printed)
	}
	parts := strings.Split(split, "   ")
	if len(parts) != len(week.Kinds) {
		t.Fatalf("terminal splits %q into %d kinds, the read model into %d: %+v", split, len(parts), len(week.Kinds), week.Kinds)
	}
	for index, share := range week.Kinds {
		want := fmt.Sprintf(": $%.2f from %d ", share.CostUSD, share.Invocations)
		if !strings.Contains(parts[index], want) {
			t.Fatalf("terminal's share %d is %q, the read model's is %+v", index, parts[index], share)
		}
	}
	// And both are the report's own sum rather than two sums that happen to
	// agree: the figures are the ones the report's method produces.
	totals := report.Totals()
	if week.CostUSD != totals.CostUSD || week.Invocations != totals.Calls || len(week.Kinds) != len(totals.ByKind) {
		t.Fatalf("the read model's window %+v is not the report's totals %+v", week, totals)
	}
	for index, share := range totals.ByKind {
		if week.Kinds[index] != (readmodel.KindSpend{Kind: share.Kind, Invocations: share.Calls, CostUSD: share.CostUSD}) {
			t.Fatalf("the read model's share %d is %+v, the report's is %+v", index, week.Kinds[index], share)
		}
	}
}

// Following is the closest thing there is to watching an agent work, and what it
// emits is the recorded events themselves: the shaping drops the envelope every
// line repeats and the thinking-token pings that drown everything else, and
// --raw gives back exactly what was written.
func TestStatusFollowsAStreamAsItsEventsArrive(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	runID := recordStreamRun(t, stateRoot, runstate.StatusRunning, streamStart, 0)
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	appendStreamEvent(t, store, runID, 2, execution.EventProcessOutput, streamStart, map[string]any{
		"provider_subtype": "thinking_tokens",
	})

	stdout, stderr, code := runCLI(t, "status", "--events", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "==> "+runID+" [running]") {
		t.Fatalf("stderr = %q, want it to name the stream being read", stderr)
	}
	if !strings.Contains(stdout, `"type":"run.started"`) || strings.Contains(stdout, "thinking_tokens") {
		t.Fatalf("stdout = %q, want the shaped events without the thinking-token noise", stdout)
	}
	if strings.Contains(stdout, `"schema_version"`) {
		t.Fatalf("stdout = %q, want the envelope every line repeats left out", stdout)
	}
	if stdout, _, _ = runCLI(t, "status", "--events", "--all", "--config", configPath); !strings.Contains(stdout, "thinking_tokens") {
		t.Fatalf("--all = %q, want the noise included when it was asked for", stdout)
	}
	if stdout, _, _ = runCLI(t, "status", "--events", "--raw", "--config", configPath); !strings.Contains(stdout, `"schema_version"`) {
		t.Fatalf("--raw = %q, want each event exactly as it was recorded", stdout)
	}
	// --lines bounds the replay to the most recent events.
	if stdout, _, _ = runCLI(t, "status", "--events", "--all", "--lines", "1", "--config", configPath); strings.Contains(stdout, "run.started") || !strings.Contains(stdout, "thinking_tokens") {
		t.Fatalf("--lines 1 = %q, want only the last recorded event", stdout)
	}

	// Following keeps emitting what arrives, and stops when the operator does.
	// The events are appended after the follow is under way, so what is asserted
	// is that they were picked up rather than replayed.
	ctx, stop := context.WithCancel(context.Background())
	// The follow writes from its own goroutine while this one watches what has
	// arrived, which is two goroutines on one buffer: they are separated here
	// rather than in the command, because a command writing to whatever io.Writer
	// it was handed is exactly what every other one does.
	out, errs := newSyncBuffer(), newSyncBuffer()
	done := make(chan int, 1)
	go func() {
		done <- reportRunStatus(ctx, []string{"--follow", "--config", configPath}, out, errs)
	}()
	appendStreamEvent(t, store, runID, 3, execution.EventAgentMessage, streamStart, map[string]any{"text": "still working"})
	waitForOutput(t, out, "still working", errs)
	stop()
	if code := <-done; code != 0 {
		t.Fatalf("an interrupted follow exited %d", code)
	}
}

// --latest is a watch on the harness rather than on one stream: when a later
// stream starts, the follow moves to it. The one non-obvious invariant is that
// nothing is dropped between the two — whatever the stream being left behind
// wrote while the follow was deciding is emitted before the successor's events
// are. The poll here is made rare and the look frequent, so the only thing that
// can carry the old stream's tail across the switch is the drain itself.
func TestStatusFollowLatestDrainsTheStreamItLeaves(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	streams, err := runstate.NewStreamStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStreamStore() error = %v", err)
	}
	first := recordStreamRun(t, stateRoot, runstate.StatusRunning, streamStart, 0)

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	out, errs := newSyncBuffer(), newSyncBuffer()
	done := make(chan int, 1)
	go func() {
		done <- followStreams(ctx, streams, streamOptions{
			follow: true,
			latest: true,
			lines:  defaultStreamLines,
			poll:   time.Hour,
			look:   20 * time.Millisecond,
		}, out, errs)
	}()
	waitForOutput(t, errs, "==> "+first, out)

	// The stream being left behind writes once more, and then a later stream
	// starts. With the poll an hour away, only the drain can emit that tail.
	appendStreamEvent(t, store, first, 2, execution.EventAgentMessage, streamStart, map[string]any{"text": "the tail before the switch"})
	// The successor's log is put in place whole, by a rename, rather than
	// appended where the follow is looking. An append creates the log and then
	// writes it, and a look that fires between those two reads an empty
	// successor and then waits on the hour-long poll for its first words — a
	// window a loaded machine widens well past the look. So the log is written
	// under a store of its own, for the same run, and moved in with one rename.
	successorRun := recordedRun(t, store, runstate.StatusRunning, "yoyodyne-ifd.63", streamStart.Add(time.Minute))
	second := successorRun.RunID
	staged, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := staged.Create(successorRun); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendStreamEvent(t, staged, second, 1, execution.EventAgentMessage, streamStart, map[string]any{"text": "the successor's first words"})
	if err := os.Rename(filepath.Join(staged.Root(), second+".events.jsonl"), filepath.Join(store.Root(), second+".events.jsonl")); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	// Newest is decided by when a log was last written to, and two logs written
	// in one instant can tie on a coarse clock, so the successor is stamped later
	// outright rather than left to the filesystem.
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(store.Root(), second+".events.jsonl"), later, later); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	waitForOutput(t, out, "the successor's first words", errs)
	stop()
	if code := <-done; code != 0 {
		t.Fatalf("an interrupted follow exited %d; stderr = %q", code, errs.String())
	}
	tail, successor := strings.Index(out.String(), "the tail before the switch"), strings.Index(out.String(), "the successor's first words")
	if tail < 0 {
		t.Fatalf("the tail of the stream being left was dropped: stdout = %q", out.String())
	}
	if tail > successor {
		t.Fatalf("the tail arrived after the successor's events: stdout = %q", out.String())
	}
	if !strings.Contains(errs.String(), "==> "+second) {
		t.Fatalf("the follow never announced the successor: stderr = %q", errs.String())
	}
}

// A machine somebody paused and a machine that died look identical, and this is
// the one place an operator is already looking, so every live mode of the verb
// says so first — on the stream a banner belongs on, so machine-readable output
// stays machine-readable. Who held intake and why is the hold's own clause: a
// banner that guessed sent an operator looking for a decision the brake had
// made.
func TestStatusAnnouncesWhatTheOperatorHasStopped(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	holds, err := runstate.NewOperatorHoldStore(stateRoot)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	if _, err := holds.Hold(time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(stateRoot, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	if _, err := intake.Hold(runstate.IntakeHolderBrake, "3 run(s) blocked in a row", time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	for _, mode := range [][]string{{"--list"}, {"--spend"}, {"--events"}} {
		args := append([]string{"status"}, mode...)
		stdout, stderr, _ := runCLI(t, append(args, "--config", configPath)...)
		for _, want := range []string{
			"PAUSED: all harness activity is paused since 2026-08-20T09:00:00Z",
			"yoyo resume",
			"INTAKE HELD since 2026-08-21T09:00:00Z: the harness's own brake placed it after 3 run(s) blocked in a row",
			"yoyo release",
		} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("%v stderr = %q, want it to contain %q", mode, stderr, want)
			}
		}
		if strings.Contains(stderr, "the operator placed it") {
			t.Fatalf("%v attributed the brake's hold to the operator: %q", mode, stderr)
		}
		if strings.Contains(stdout, "PAUSED") {
			t.Fatalf("%v put a banner on the output stream: %q", mode, stdout)
		}
	}

	// The same two facts are carried in the machine-readable answer, because a
	// script reading only stdout would otherwise never learn them.
	stdout, _, code := runCLI(t, "status", "--list", "--json", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var listed streamListOutput
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if listed.Paused == nil || listed.Intake == nil {
		t.Fatalf("JSON listing = %+v, want both holds carried", listed)
	}
	// A running harness says nothing about either.
	if _, _, err := holds.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, _, err := intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, stderr, _ := runCLI(t, "status", "--list", "--config", configPath); strings.Contains(stderr, "PAUSED") || strings.Contains(stderr, "INTAKE HELD") {
		t.Fatalf("a running harness announced a hold: %q", stderr)
	}
}

// The modes are different questions rather than different amounts of one, so
// asking two of them is refused instead of getting a precedence nobody chose,
// and an option that belongs to the other half of the verb is refused rather
// than ignored — including the two whose default is not their zero, which are
// detected by having been given at all.
//
// Every refusal here is decided from the flags alone, and this asserts that
// rather than assuming it: each is run against a configuration path that cannot
// exist, so a refusal that had drifted below the point where the configuration
// is loaded would report that failure and exit 1 instead of refusing with 2. It
// is what keeps this test a fact about the code rather than about the machine —
// without it a refusal decided later would resolve the real configuration and
// the operator's own state root, and a `go test` run would read, and could
// create directories under, the state directory of whoever ran it.
func TestStatusRefusesStreamOptionsItCannotHonor(t *testing.T) {
	t.Parallel()

	unreadable := filepath.Join(t.TempDir(), "no-such-directory", "config.yaml")
	for _, refusal := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "--list", "--spend"}, "different questions"},
		{[]string{"status", "--latest"}, "needs --follow"},
		{[]string{"status", "--raw"}, "need --follow or --events"},
		{[]string{"status", "--kind", "runs"}, "--kind narrows which event streams are read"},
		{[]string{"status", "--list", "--failed"}, "cannot narrow a stream"},
		{[]string{"status", "--follow", "--json"}, "--raw is what asks for them untouched"},
		{[]string{"status", "--events", "--json"}, "--raw is what asks for them untouched"},
		{[]string{"status", "--kind", "sideways"}, "unknown kind"},
		{[]string{"status", "--events", "--lines", "-1"}, "lines cannot be negative"},
		{[]string{"status", "--spend", "0"}, "neither a positive number of days nor the id"},
		{[]string{"status", "--lines", "10"}, "--lines replays a stream's recorded events"},
		{[]string{"status", "--list", "--lines", "10"}, "--lines replays a stream's recorded events"},
		{[]string{"status", "--spend", "--lines", "10"}, "--lines replays a stream's recorded events"},
		{[]string{"status", "--follow", "--limit", "10"}, "--limit bounds a listing"},
		{[]string{"status", "--events", "--limit", "10"}, "--limit bounds a listing"},
		{[]string{"status", "--spend", "--limit", "10"}, "--limit bounds a listing"},
		{[]string{"status", "--list", "--kind", "exchanges"}, "--kind exchanges needs --spend"},
		{[]string{"status", "--follow", "--kind", "exchanges"}, "--kind exchanges needs --spend"},
	} {
		args := append(append([]string{}, refusal.args...), "--config", unreadable)
		_, stderr, code := runCLI(t, args...)
		if code != 2 {
			t.Fatalf("%v code = %d, want 2; stderr = %q", args, code, stderr)
		}
		if !strings.Contains(stderr, refusal.want) {
			t.Fatalf("%v stderr = %q, want it to contain %q", args, stderr, refusal.want)
		}
	}
	// The defaults of the two are not refusals: a listing given no --limit and a
	// follow given no --lines are the ordinary case, and they get as far as the
	// configuration, which is what cannot be read here.
	for _, ordinary := range [][]string{{"status", "--list"}, {"status", "--events"}, {"status", "--spend"}} {
		args := append(append([]string{}, ordinary...), "--config", unreadable)
		if _, stderr, code := runCLI(t, args...); code != 1 {
			t.Fatalf("%v code = %d, want the configuration failure; stderr = %q", args, code, stderr)
		}
	}
}

// waitForOutput blocks until what a still-running follow has written contains
// the text. It waits on the follow's own writes and on nothing else: the ten
// seconds it used to allow was reached, under the race detector beside another
// suite at a load average past twenty, with the follow working and the test
// failing a change that never touched this package. The other stream is taken
// so a caller reads as before; what it held was the failure message.
func waitForOutput(t *testing.T, watched *syncBuffer, want string, _ *syncBuffer) {
	t.Helper()
	for !strings.Contains(watched.String(), want) {
		<-watched.written
	}
}

// streamStart is when the fabricated streams here opened, fixed so nothing
// asserted about them depends on when the test ran.
var streamStart = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

// syncBuffer is what a still-running command writes to while the test reads
// what it has written so far. Nothing about following needs it — a command
// writes to the io.Writer it was handed and never reads it back — but a test
// that watches output arrive is by construction on a second goroutine.
//
// Every write is also announced on `written`, which holds one announcement
// however many writes made it, so a reader that checked the buffer and found
// nothing waits there for the next write rather than polling a clock.
type syncBuffer struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	written chan struct{}
}

func newSyncBuffer() *syncBuffer {
	return &syncBuffer{written: make(chan struct{}, 1)}
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buffer.Write(data)
	select {
	case b.written <- struct{}{}:
	default:
	}
	return n, err
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// The one cache-read share under the total is decided by whichever role reads
// the most, so a role writing its whole prompt into the cache and reading none
// of it back is invisible there. The report says what each role paid to write
// the cache and what it paid to read it, apportioned from the provider's own
// figure, and the same split travels on every row of the machine-readable
// report.
func TestStatusSpendSaysWhatEachRolePaidToWriteAndReadTheCache(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	today := time.Now()
	state := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.424", today.Add(-time.Hour))
	appendStreamEvent(t, store, state.RunID, 1, execution.EventRunStarted, today.Add(-time.Hour), map[string]any{"session_id": "session-developer"})
	// The developer read nearly everything from the cache; the reviewer read a
	// six-thousand-token prefix and wrote the rest for an hour, which at the
	// provider's multiples is two thirds of what it cost.
	appendStreamEvent(t, store, state.RunID, 2, execution.EventRunCompleted, today.Add(-time.Hour), roleCost("developer", 5, 0, 1000, 100000, 900000))
	appendStreamEvent(t, store, state.RunID, 3, execution.EventRunCompleted, today.Add(-time.Hour), roleCost("reviewer", 0.615218, 2, 8680, 39517, 6076))

	stdout, stderr, code := runCLI(t, "status", "--spend", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"by role, each role's cost apportioned across what its invocations were billed for",
		"cache_w USD", "cache_r USD",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	rows := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 8 && (fields[0] == "developer" || fields[0] == "reviewer") {
			rows[fields[0]] = line
		}
	}
	reviewer, developer := strings.Fields(rows["reviewer"]), strings.Fields(rows["developer"])
	if len(reviewer) != 8 || reviewer[1] != "1" || reviewer[2] != "39,517" || reviewer[3] != "$0.40" || reviewer[4] != "6,076" || reviewer[5] != "$0.00" || reviewer[6] != "13.3%" || reviewer[7] != "$0.62" {
		t.Fatalf("the reviewer's line = %q, want its calls, cache writes and what they cost, cache reads and what they cost, its read share, and its total", rows["reviewer"])
	}
	if len(developer) != 8 || developer[6] != "90.0%" || developer[7] != "$5.00" {
		t.Fatalf("the developer's line = %q", rows["developer"])
	}

	stdout, stderr, code = runCLI(t, "status", "--spend", "--json", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var priced spendOutput
	if err := json.Unmarshal([]byte(stdout), &priced); err != nil {
		t.Fatalf("Unmarshal() error = %v: %q", err, stdout)
	}
	if len(priced.Report.Rows) != 1 || len(priced.Report.Rows[0].Roles) != 2 {
		t.Fatalf("the report's rows = %+v, want one row carrying both roles", priced.Report.Rows)
	}
	split := priced.Report.Rows[0].Roles[1]
	if split.Role != domain.RoleReviewer || split.Split.CacheWriteUSD < 0.395 || split.Split.CacheWriteUSD > 0.3952 {
		t.Fatalf("the reviewer's part of the row = %+v", split)
	}
}

// roleCost is a terminal that names its role and splits its cache write by
// lifetime, as the backend records one.
func roleCost(role string, cost float64, input, output, cacheWrite, cacheRead int64) map[string]any {
	return map[string]any{
		"session_id":     "session-" + role,
		"role":           role,
		"total_cost_usd": cost,
		"usage": map[string]any{
			"input_tokens":                input,
			"output_tokens":               output,
			"cache_creation_input_tokens": cacheWrite,
			"cache_read_input_tokens":     cacheRead,
			"cache_creation": map[string]any{
				"ephemeral_1h_input_tokens": cacheWrite,
				"ephemeral_5m_input_tokens": 0,
			},
		},
	}
}

func recordStreamRun(t *testing.T, stateRoot string, status runstate.Status, startedAt time.Time, cost float64) string {
	t.Helper()
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	state := recordedRun(t, store, status, "yoyodyne-ifd.63", startedAt)
	appendStreamEvent(t, store, state.RunID, 1, execution.EventRunStarted, startedAt, map[string]any{"session_id": "session-developer"})
	if cost > 0 {
		appendStreamEvent(t, store, state.RunID, 2, execution.EventRunCompleted, startedAt, streamCost(cost))
	}
	return state.RunID
}

// recordStreamConversation writes a conversation the role is still in, with one
// completed turn at each of the moments given.
func recordStreamConversation(t *testing.T, stateRoot string, startedAt time.Time, turns []time.Time) string {
	t.Helper()
	store, err := runstate.NewConversationStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	id, err := runstate.NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	conversation := runstate.Conversation{
		SchemaVersion:  runstate.ConversationSchemaVersion,
		ConversationID: id,
		ProductID:      domain.ProductID("yoyodyne"),
		RepositoryID:   "yoyodyne",
		Role:           domain.RoleProductManager,
		Backend:        domain.BackendClaudeCode,
		ProviderModel:  "opus",
		Turns:          len(turns),
		StartedAt:      startedAt,
		UpdatedAt:      startedAt,
	}
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	sequence := uint64(1)
	appendConversationEvent(t, store, id, sequence, execution.EventRunStarted, startedAt, map[string]any{"session_id": "session-chat"})
	// Every turn resumes the one session, so each terminal reports what the
	// conversation has cost since it opened rather than what the turn cost: a
	// dollar a turn is recorded as $1, $2, $3. What the ledger reports is the
	// dollar each of them added.
	for turnIndex, turn := range turns {
		sequence++
		appendConversationEvent(t, store, id, sequence, execution.EventRunCompleted, turn, streamCost(float64(turnIndex+1)))
	}
	return id
}

func recordStreamReview(t *testing.T, stateRoot string, startedAt time.Time, cost float64) string {
	t.Helper()
	store, err := runstate.NewBranchReviewStore(stateRoot, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewBranchReviewStore() error = %v", err)
	}
	id, err := runstate.NewBranchReviewID()
	if err != nil {
		t.Fatalf("NewBranchReviewID() error = %v", err)
	}
	appendReviewEvent(t, store, id, 1, execution.EventReviewStarted, startedAt, map[string]any{"session_id": "session-review"})
	if cost > 0 {
		appendReviewEvent(t, store, id, 2, execution.EventRunCompleted, startedAt, streamCost(cost))
	}
	return id
}

// recordStreamExchange writes one resolved exchange with a round answered at
// each of the moments given, costing what the matching entry says.
func recordStreamExchange(t *testing.T, stateRoot string, seed byte, answered []time.Time, costs []float64) string {
	t.Helper()
	store, err := runstate.NewExchangeStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewExchangeStore() error = %v", err)
	}
	recorded := exchange.Exchange{
		SchemaVersion: exchange.SchemaVersion,
		ID:            "exchange-" + strings.Repeat(string(seed), 32),
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Asker:         exchange.Party{Role: domain.RoleProductManager, Agent: "product-manager"},
		Answerer:      exchange.Party{Role: domain.RoleArchitect, Agent: "architect"},
		Question:      "what does this cost?",
		MaxRounds:     10,
		OpenedAt:      answered[0].Add(-time.Minute),
	}
	for index, at := range answered {
		moment := at
		recorded.Rounds = append(recorded.Rounds, exchange.Round{
			Number:     index + 1,
			Question:   "and then?",
			Answer:     "this much",
			CostUSD:    costs[index],
			AskedAt:    at.Add(-time.Minute),
			AnsweredAt: &moment,
		})
	}
	closed := answered[len(answered)-1]
	recorded.Outcome, recorded.ClosedAt, recorded.UpdatedAt = exchange.OutcomeResolved, &closed, closed
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return recorded.ID
}

// streamCost is what the provider reports when an invocation ends, which is the
// only place the cost and the token counts are ever written down.
func streamCost(cost float64) map[string]any {
	return map[string]any{
		"session_id":     "session-stream",
		"total_cost_usd": cost,
		"usage": map[string]any{
			"input_tokens":                100,
			"output_tokens":               200,
			"cache_creation_input_tokens": 300,
			"cache_read_input_tokens":     4000,
		},
	}
}

func appendStreamEvent(t *testing.T, store *runstate.Store, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, id, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func appendConversationEvent(t *testing.T, store *runstate.ConversationStore, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, id, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func appendReviewEvent(t *testing.T, store *runstate.BranchReviewStore, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, id, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func newStreamEvent(t *testing.T, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) execution.Event {
	t.Helper()
	event, err := execution.NewEvent(id, sequence, at, eventType, "claude-code", payload)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	return event
}

// A count of tokens nobody can read at a glance defeats the line it appears on.
func TestGroupThousandsSeparatesWhatItCounts(t *testing.T) {
	t.Parallel()

	for value, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1067970: "1,067,970", -12345: "-12,345"} {
		if got := groupThousands(value); got != want {
			t.Fatalf("groupThousands(%d) = %q, want %q", value, got, want)
		}
	}
}

// A log the harness is halfway through writing holds no event yet, and one that
// was replaced underneath is not read from an offset that now means something
// else. Both are what following a live stream actually runs into.
func TestLogTailEmitsOnlyWholeLinesAndSurvivesReplacement(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "stream.events.jsonl")
	if err := os.WriteFile(path, []byte("first\nsec"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	tail := &logTail{path: path}
	lines, err := tail.read()
	if err != nil {
		t.Fatalf("read() error = %v", err)
	}
	if len(lines) != 1 || string(lines[0]) != "first" {
		t.Fatalf("read() = %q, want only the whole line", lines)
	}
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if lines, err = tail.read(); err != nil || len(lines) != 1 || string(lines[0]) != "second" {
		t.Fatalf("read() = %q, %v, want the line completed and nothing repeated", lines, err)
	}
	if err := os.WriteFile(path, []byte("replaced\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if lines, err = tail.read(); err != nil || len(lines) != 1 || string(lines[0]) != "replaced" {
		t.Fatalf("read() after truncation = %q, %v", lines, err)
	}
}
