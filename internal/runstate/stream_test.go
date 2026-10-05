package runstate

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The question an operator asks of a live machine is the same for all three
// kinds — is this alive, what is it doing — so the listing answers it for all
// three at once and says which kind each one is. What each is doing comes from a
// different place for each: a run keeps its own status, a conversation's is
// derived from whether the role is still in it, and a review's from whether its
// verdict has been made.
func TestStreamStoreListsAllThreeKindsNewestFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, conversations, reviews := streamStores(t, root)
	store := newStreamStore(t, root)

	state := testState(t, StatusRunning)
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendStreamEvent(t, runs, state.RunID, 1, execution.EventRunStarted, streamMoment, nil)

	current := testConversation(t)
	current.Turns = 1
	current.ProviderModel = "opus"
	if err := conversations.Save(current); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// The role is in this one and its last turn closed, so it is waiting on the
	// operator rather than being answered.
	appendConversationEvent(t, conversations, current.ConversationID, 1, execution.EventRunStarted, streamMoment, nil)
	appendConversationEvent(t, conversations, current.ConversationID, 2, execution.EventRunCompleted, streamMoment.Add(time.Minute), nil)
	// This one the role has since replaced, so nothing will happen in it again.
	replaced := mustConversationID(t)
	appendConversationEvent(t, conversations, replaced, 1, execution.EventRunStarted, streamMoment, nil)

	reviewID := mustBranchReviewID(t)
	appendReviewEvent(t, reviews, reviewID, 1, execution.EventReviewStarted, streamMoment, nil)

	// Newest means the stream something happened in most recently, which is what
	// an operator who named nothing meant, so the order is asserted against logs
	// whose last append is known rather than against whatever the filesystem
	// happened to record microseconds apart.
	touch(t, reviews.Root(), reviewID, streamMoment.Add(4*time.Hour))
	touch(t, conversations.Root(), current.ConversationID, streamMoment.Add(3*time.Hour))
	touch(t, runs.Root(), state.RunID, streamMoment.Add(2*time.Hour))
	touch(t, conversations.Root(), replaced, streamMoment.Add(time.Hour))

	listed, err := store.List(StreamQuery{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for index, want := range []string{reviewID, current.ConversationID, state.RunID, replaced} {
		if index < len(listed) && listed[index].ID != want {
			t.Fatalf("stream %d is %s, want %s: the listing is not newest first", index, listed[index].ID, want)
		}
	}
	if len(listed) != 4 {
		t.Fatalf("listed %d stream(s), want the run, both conversations, and the review: %+v", len(listed), listed)
	}
	status := map[string]Stream{}
	for _, stream := range listed {
		status[stream.ID] = stream
	}
	for _, want := range []struct {
		id     string
		kind   StreamKind
		status string
	}{
		{state.RunID, StreamRun, string(StatusRunning)},
		{current.ConversationID, StreamConversation, ConversationWaiting},
		{replaced, StreamConversation, ConversationEnded},
		{reviewID, StreamReview, ReviewInProgress},
	} {
		got, found := status[want.id]
		if !found {
			t.Fatalf("%s was not listed", want.id)
		}
		if got.Kind != want.kind || got.Status != want.status {
			t.Fatalf("%s listed as %s/%s, want %s/%s", want.id, got.Kind, got.Status, want.kind, want.status)
		}
	}
	// A run's opening moment is the one its own record keeps; the two kinds that
	// keep no record of their own take it from their first event, which is the
	// same moment.
	if !status[state.RunID].StartedAt.Equal(state.StartedAt) {
		t.Fatalf("the run opened at %v, want its recorded %v", status[state.RunID].StartedAt, state.StartedAt)
	}
	if !status[reviewID].StartedAt.Equal(streamMoment) {
		t.Fatalf("the review opened at %v, want %v", status[reviewID].StartedAt, streamMoment)
	}

	// Narrowing to one kind is what `--kind` asks for, and a query that names an
	// id prefix is how one stream is named without typing all of it.
	onlyRuns, err := store.List(StreamQuery{Kinds: []StreamKind{StreamRun}})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(onlyRuns) != 1 || onlyRuns[0].ID != state.RunID {
		t.Fatalf("runs only listed %+v", onlyRuns)
	}
	// An exchange records no stream, so asking for one to list contributes
	// nothing rather than reading the runs by default.
	if listed, err := store.List(StreamQuery{Kinds: []StreamKind{StreamExchange}}); err != nil || len(listed) != 0 {
		t.Fatalf("exchanges listed %+v, %v; want nothing, since an exchange has no stream", listed, err)
	}
	found, matched, err := store.Find(StreamQuery{Match: state.RunID[4:12]})
	if err != nil || !matched {
		t.Fatalf("Find() = %v, %v, %v", found, matched, err)
	}
	if found.ID != state.RunID {
		t.Fatalf("an id prefix found %s, want %s", found.ID, state.RunID)
	}
	if _, matched, err := store.Find(StreamQuery{Match: "nothing-is-named-this"}); err != nil || matched {
		t.Fatalf("Find() on an unmatched pattern = %v, %v, want no match and no failure", matched, err)
	}
}

// Whether a conversation is being answered is asked of the observed hold — the
// same question the four lines' Working line asks, of the same store — rather
// than read off the event log. The log cannot tell a turn in flight from a turn
// whose process was killed before it wrote a terminal, and two halves of one
// verb must not disagree about whether an agent is working.
func TestStreamStoreAsksTheObservedHoldWhetherAConversationIsAnswering(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, conversations, _ := streamStores(t, root)
	store := newStreamStore(t, root)

	current := testConversation(t)
	if err := conversations.Save(current); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	identity := current.Identity()
	// The log says a turn opened and never closed, which is what a process
	// killed mid-turn leaves behind. Nobody holds the conversation, so it is
	// waiting, exactly as the Working line would report it.
	appendConversationEvent(t, conversations, current.ConversationID, 1, execution.EventRunStarted, streamMoment, nil)
	listed, err := store.List(StreamQuery{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Status != ConversationWaiting {
		t.Fatalf("listed %+v, want the unheld conversation waiting whatever its log says", listed)
	}
	// Held, it is being answered — whatever the log says.
	held, err := conversations.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer held.Release()
	appendConversationEvent(t, conversations, current.ConversationID, 2, execution.EventRunCompleted, streamMoment, nil)
	if listed, err = store.List(StreamQuery{}); err != nil || len(listed) != 1 || listed[0].Status != ConversationAnswering {
		t.Fatalf("listed %+v, %v; want the held conversation answering", listed, err)
	}
	if inFlight, err := conversations.InFlight(identity); err != nil || !inFlight {
		t.Fatalf("InFlight() = %v, %v; the listing and the read model read the same stamp", inFlight, err)
	}
}

// A conversation whose record will not load is exactly the state an operator is
// reaching for this listing to diagnose, so it must not be the thing that makes
// the listing refuse to answer. Which conversation a role is in, and under which
// identity it is held, are read as the few fields that say so.
func TestStreamStoreListsPastAnUnloadableConversationRecord(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, conversations, _ := streamStores(t, root)
	store := newStreamStore(t, root)

	id := mustConversationID(t)
	appendConversationEvent(t, conversations, id, 1, execution.EventRunStarted, streamMoment, nil)
	// A record from a harness this one does not understand: it names its
	// conversation and its role, and nothing else here can be trusted.
	record := filepath.Join(conversations.Root(), "product-manager.json")
	if err := os.WriteFile(record, []byte(`{"schema_version":99,"conversation_id":"`+id+`","role":"product-manager"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	held, err := conversations.Hold(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer held.Release()

	listed, err := store.List(StreamQuery{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Status != ConversationAnswering {
		t.Fatalf("listed %+v, want the conversation still reported as being answered", listed)
	}
}

// A listing chooses from the directory and opens only the logs it prints. An
// operator's state directory holds hundreds of streams with logs that reach
// megabytes, and `--follow --latest` asks for the newest one every few seconds,
// so a listing that read every log to print one row would read the whole
// directory on a timer.
func TestStreamStoreOpensOnlyTheLogsItLists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, _, _ := streamStores(t, root)
	store := newStreamStore(t, root)

	older := testState(t, StatusSucceeded)
	if err := runs.Create(older); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendStreamEvent(t, runs, older.RunID, 1, execution.EventRunStarted, streamMoment, nil)
	newer := testState(t, StatusRunning)
	if err := runs.Create(newer); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendStreamEvent(t, runs, newer.RunID, 1, execution.EventRunStarted, streamMoment, nil)
	touch(t, runs.Root(), older.RunID, streamMoment)
	touch(t, runs.Root(), newer.RunID, streamMoment.Add(time.Hour))
	// The older log is made unreadable. A listing that opened it would fail;
	// one that chooses first never touches it.
	if err := os.Chmod(filepath.Join(runs.Root(), older.RunID+eventLogSuffix), 0o000); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	if os.Getuid() == 0 {
		t.Skip("root reads a file whatever its mode, so the unopened log cannot be told from an opened one")
	}

	found, matched, err := store.Find(StreamQuery{})
	if err != nil || !matched || found.ID != newer.RunID {
		t.Fatalf("Find() = %+v, %v, %v; want the newest run without the older log being opened", found, matched, err)
	}
	if _, err := store.List(StreamQuery{}); err == nil {
		t.Fatal("List() of everything read the unreadable log and reported nothing wrong")
	}
}

// What an operator budgets against is what today cost, so spend is grouped by
// the local day the money was spent on rather than by the log it was recorded
// in. A conversation open for days spends on each of them and contributes a row
// to each.
func TestStreamStoreSpendsByTheDayTheMoneyWasSpent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, conversations, _ := streamStores(t, root)
	store := newStreamStore(t, root)

	// Anchored at local noon so no invocation below can be pushed into a
	// neighbouring day by the timezone the test happens to run in.
	today := time.Date(2026, 8, 23, 12, 0, 0, 0, time.Local)
	id := mustConversationID(t)
	appendConversationEvent(t, conversations, id, 1, execution.EventRunStarted, today.AddDate(0, 0, -14), nil)
	// The conversation resumes one provider session across all three turns, so
	// each terminal reports what the session has cost since it opened: $1.50,
	// then $4.00, then $4.25. What each turn cost is the difference — $1.50,
	// $2.50, and $0.25 — which is what the day rows below are in.
	appendConversationEvent(t, conversations, id, 2, execution.EventRunCompleted, today.AddDate(0, 0, -14), invocationPayload(1.5, 10, 20, 30, 40))
	appendConversationEvent(t, conversations, id, 3, execution.EventRunCompleted, today, invocationPayload(4.0, 1, 2, 3, 4))
	// A turn the provider ended in an error cost money like any other, so it is
	// priced rather than left out of the total it belongs in.
	appendConversationEvent(t, conversations, id, 4, execution.EventRunFailed, today, invocationPayload(4.25, 5, 5, 5, 5))

	report, err := store.Spend(SpendQuery{Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if len(report.Rows) != 2 {
		t.Fatalf("report has %d row(s), want one per day the conversation spent on: %+v", len(report.Rows), report.Rows)
	}
	if report.Rows[0].Day != LocalDay(today.AddDate(0, 0, -14)) || report.Rows[1].Day != LocalDay(today) {
		t.Fatalf("rows are %s then %s, want the older day first", report.Rows[0].Day, report.Rows[1].Day)
	}
	if report.Rows[1].Calls != 2 || report.Rows[1].CostUSD != 2.75 {
		t.Fatalf("today's row = %+v, want both of today's invocations priced", report.Rows[1])
	}
	if report.Rows[1].Usage == nil || *report.Rows[1].Usage != (TokenUsage{InputTokens: 6, OutputTokens: 7, CacheCreationTokens: 8, CacheReadTokens: 9, Measured: 2}) {
		t.Fatalf("today's usage = %+v", report.Rows[1].Usage)
	}
	// The row for the day the work opened says when it opened; a later day says
	// when it next spent, because there is nothing else that column could mean.
	if !report.Rows[0].At.Equal(today.AddDate(0, 0, -14)) || !report.Rows[1].At.Equal(today) {
		t.Fatalf("row moments = %v, %v", report.Rows[0].At, report.Rows[1].At)
	}

	// A window is about when the money was spent, so a stream that opened before
	// it still reports what it spent inside it — and only that.
	windowed, err := store.Spend(SpendQuery{Days: 7, Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if len(windowed.Rows) != 1 || windowed.Rows[0].Day != LocalDay(today) {
		t.Fatalf("a seven day window reported %+v, want only today's spend", windowed.Rows)
	}
	if windowed.Oldest != LocalDay(today.AddDate(0, 0, -6)) {
		t.Fatalf("window reaches back to %s, want today and the six days before it", windowed.Oldest)
	}
	// Naming a stream prices it whatever day it ran on: the window is for a
	// report that has to choose what to show, and an id has already chosen.
	named, err := store.Spend(SpendQuery{Match: id[5:13], Days: 7, Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if len(named.Rows) != 1 {
		t.Fatalf("naming a stream inside a window reported %+v", named.Rows)
	}
}

// What two roles spent asking each other is money the harness spent, so an
// exchange is priced beside the streams: each round on the day it was answered,
// with no token usage because its record keeps none, and named as its own kind.
// A record that cannot be read is counted and named rather than dropped, and the
// exchanges beside it are still priced — which is the answer `yoyo cost` gives
// for the same records.
func TestStreamStoreSpendsOnExchangesBesideTheStreams(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, conversations, _ := streamStores(t, root)
	exchanges := newTestExchangeStore(t, root)
	store := newStreamStore(t, root)

	today := time.Date(2026, 8, 23, 12, 0, 0, 0, time.Local)
	chat := mustConversationID(t)
	appendConversationEvent(t, conversations, chat, 1, execution.EventRunCompleted, today, invocationPayload(1.5, 1, 1, 1, 1))

	// A thread answered on two days lands in both of their totals, exactly as a
	// conversation that spent on two days does.
	asked := testExchange("a")
	earlier, later := today.AddDate(0, 0, -2), today
	asked.Rounds = []exchange.Round{
		{Number: 1, Question: "first?", Answer: "yes", CostUSD: 0.6, AskedAt: earlier.Add(-time.Minute), AnsweredAt: &earlier},
		{Number: 2, Question: "second?", Answer: "also", CostUSD: 0.4, AskedAt: later.Add(-time.Minute), AnsweredAt: &later},
	}
	asked.Outcome, asked.ClosedAt, asked.UpdatedAt = exchange.OutcomeResolved, &later, later
	if err := exchanges.Save(asked); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// A record nobody can parse did not cost nothing.
	unreadable := "exchange-" + strings32('b')
	if err := os.WriteFile(filepath.Join(exchanges.Root(), unreadable+".json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	report, err := store.Spend(SpendQuery{Days: 7, Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if report.Streams != 1 || report.Exchanges != 1 {
		t.Fatalf("report read %d stream(s) and %d exchange(s), want one of each", report.Streams, report.Exchanges)
	}
	if len(report.UnreadableExchanges) != 1 || report.UnreadableExchanges[0] != unreadable || report.UnreadableReason == "" || !report.Floor() {
		t.Fatalf("unreadable = %v (%q), want the broken record named and the total marked a floor", report.UnreadableExchanges, report.UnreadableReason)
	}
	var rows []SpendRow
	for _, row := range report.Rows {
		if row.Kind == StreamExchange {
			rows = append(rows, row)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("exchange rows = %+v, want one per day a round was answered on", rows)
	}
	if rows[0].Day != LocalDay(earlier) || rows[0].CostUSD != 0.6 || rows[1].Day != LocalDay(today) || rows[1].CostUSD != 0.4 {
		t.Fatalf("exchange rows = %+v, want each day priced at what its round cost", rows)
	}
	if rows[0].Usage != nil || rows[0].Status != string(exchange.OutcomeResolved) || rows[0].Calls != 1 {
		t.Fatalf("exchange row = %+v, want no usage, the outcome as its status, and one round", rows[0])
	}
	// The window holds an exchange's rows exactly as it holds a stream's.
	narrow, err := store.Spend(SpendQuery{Days: 1, Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if len(narrow.Rows) != 2 {
		t.Fatalf("a one day window reported %+v, want today's turn and today's round only", narrow.Rows)
	}
	// Naming an exchange prices it on its own, and narrowing to a followable
	// kind narrows the exchanges out: somebody who asked what the conversations
	// cost is asking about those.
	alone, err := store.Spend(SpendQuery{Match: asked.ID[9:17], Now: today})
	if err != nil || alone.Streams != 0 || alone.Exchanges != 1 || len(alone.Rows) != 2 {
		t.Fatalf("naming an exchange reported %+v, %v", alone, err)
	}
	chats, err := store.Spend(SpendQuery{Kinds: []StreamKind{StreamConversation}, Now: today})
	if err != nil || chats.Exchanges != 0 || len(chats.UnreadableExchanges) != 0 || len(chats.Rows) != 1 {
		t.Fatalf("conversations only reported %+v, %v", chats, err)
	}
	// Nothing selected at all is a different answer from nothing spent, and an
	// exchange nobody could read still counts as something selected.
	if empty, err := store.Spend(SpendQuery{Match: "nothing-is-named-this", Now: today}); err != nil || !empty.Empty() {
		t.Fatalf("an unmatched name reported %+v, %v", empty, err)
	}
	if broken, err := store.Spend(SpendQuery{Match: unreadable[9:17], Now: today}); err != nil || broken.Empty() {
		t.Fatalf("naming the unreadable exchange reported %+v, %v; want it counted", broken, err)
	}
}

// The rolling window takes an exchange round by the moment it was answered
// rather than by its day: a thread with a round either side of the boundary
// puts only the later one in the rolling rows, while both still land on their
// own days. An exchange nobody could read is missing from the rolling window as
// much as from any other, so that window is a floor too.
func TestStreamStoreRollsAnExchangeByTheMomentEachRoundWasAnswered(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	exchanges := newTestExchangeStore(t, root)
	store := newStreamStore(t, root)

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.Local)
	since := now.Add(-24 * time.Hour)
	outside, inside := since.Add(-time.Hour), since.Add(time.Hour)
	asked := testExchange("a")
	asked.Rounds = []exchange.Round{
		{Number: 1, Question: "first?", Answer: "yes", CostUSD: 0.6, AskedAt: outside.Add(-time.Minute), AnsweredAt: &outside},
		{Number: 2, Question: "second?", Answer: "also", CostUSD: 0.4, AskedAt: inside.Add(-time.Minute), AnsweredAt: &inside},
	}
	asked.Outcome, asked.ClosedAt, asked.UpdatedAt = exchange.OutcomeResolved, &inside, inside
	if err := exchanges.Save(asked); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	unreadable := "exchange-" + strings32('b')
	if err := os.WriteFile(filepath.Join(exchanges.Root(), unreadable+".json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	report, err := store.Spend(SpendQuery{Days: 7, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	var daily float64
	for _, row := range report.Rows {
		daily += row.CostUSD
	}
	if daily != 1.0 {
		t.Fatalf("daily rows = %+v, want both rounds on their own days", report.Rows)
	}
	if len(report.Rolling) != 1 || report.Rolling[0].CostUSD != 0.4 || report.Rolling[0].Calls != 1 || report.Rolling[0].Kind != StreamExchange {
		t.Fatalf("rolling rows = %+v, want only the round answered inside the window", report.Rolling)
	}
	rolling := report.RollingWindow()
	if totals := rolling.Totals(); totals.CostUSD != 0.4 || totals.Calls != 1 {
		t.Fatalf("rolling totals = %+v, want the one round inside the window", totals)
	}
	if !rolling.Floor() || len(rolling.UnreadableExchanges) != 1 || rolling.UnreadableExchanges[0] != unreadable {
		t.Fatalf("rolling window = %+v, want the unreadable exchange carried and the total a floor", rolling)
	}
}

// Something whose moment cannot be read still cost money, so it is reported
// rather than dropped: it has no day to be outside of, so no window excludes it.
func TestStreamStoreReportsUndatedSpendUnderEveryWindow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, conversations, _ := streamStores(t, root)
	store := newStreamStore(t, root)

	id := mustConversationID(t)
	appendConversationEvent(t, conversations, id, 1, execution.EventRunCompleted, streamMoment, invocationPayload(3, 1, 1, 1, 1))
	// An event log line the harness can read enough of to price but not enough
	// to date is what an undated row is.
	log := filepath.Join(conversations.Root(), id+".events.jsonl")
	if err := os.WriteFile(log, []byte(`{"type":"run.completed","payload":{"total_cost_usd":3}}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	report, err := store.Spend(SpendQuery{Days: 1, Now: streamMoment.AddDate(0, 0, 400)})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if len(report.Rows) != 1 || report.Rows[0].Day != UndatedDay || report.Rows[0].CostUSD != 3 {
		t.Fatalf("report = %+v, want the undated spend reported anyway", report.Rows)
	}
}

// A report adds itself up once, for every surface that prints a total: in all,
// with the token usage of the rows that recorded one, and by kind in the order
// the kinds are priced. Narrowing it to a later first day keeps the rows from
// that day on and every undated row, and carries the unreadable exchanges
// whole, because a record nobody could read is missing from every window.
func TestSpendReportTotalsAndNarrowsByItsOwnRule(t *testing.T) {
	t.Parallel()

	report := SpendReport{
		Days:   7,
		Oldest: "2026-09-13",
		Rows: []SpendRow{
			{Day: "2026-09-18", StreamID: "chat-1", Kind: StreamConversation, Calls: 2, CostUSD: 4, Usage: &TokenUsage{InputTokens: 10, OutputTokens: 5}},
			{Day: "2026-09-18", StreamID: "run-1", Kind: StreamRun, Calls: 3, CostUSD: 10, Usage: &TokenUsage{InputTokens: 100, OutputTokens: 50}},
			{Day: "2026-09-19", StreamID: "run-2", Kind: StreamRun, Calls: 2, CostUSD: 5.5, Usage: &TokenUsage{InputTokens: 20, OutputTokens: 10}},
			{Day: "2026-09-19", StreamID: "review-1", Kind: StreamReview, Calls: 1, CostUSD: 1.25, Usage: &TokenUsage{InputTokens: 1, OutputTokens: 1}},
			{Day: UndatedDay, StreamID: "exchange-1", Kind: StreamExchange, Calls: 1, CostUSD: 0.5},
		},
		UnreadableExchanges: []string{"exchange-broken"},
		UnreadableReason:    "unexpected end of JSON input",
	}

	whole := report.Totals()
	if whole.Calls != 9 || whole.CostUSD != 21.25 || whole.Usage.InputTokens != 131 || whole.Usage.OutputTokens != 66 {
		t.Fatalf("the whole report totals %+v", whole)
	}
	if len(whole.ByKind) != 4 ||
		whole.ByKind[0] != (KindTotal{Kind: StreamRun, Calls: 5, CostUSD: 15.5, Usage: TokenUsage{InputTokens: 120, OutputTokens: 60}}) ||
		whole.ByKind[1] != (KindTotal{Kind: StreamConversation, Calls: 2, CostUSD: 4, Usage: TokenUsage{InputTokens: 10, OutputTokens: 5}}) ||
		whole.ByKind[2] != (KindTotal{Kind: StreamReview, Calls: 1, CostUSD: 1.25, Usage: TokenUsage{InputTokens: 1, OutputTokens: 1}}) ||
		whole.ByKind[3] != (KindTotal{Kind: StreamExchange, Calls: 1, CostUSD: 0.5}) {
		t.Fatalf("the whole report splits %+v, want the four kinds in the order they are priced", whole.ByKind)
	}

	today := report.Since("2026-09-19")
	if today.Oldest != "2026-09-19" || today.Days != 0 || len(today.Rows) != 3 || !today.Floor() || today.UnreadableReason != report.UnreadableReason {
		t.Fatalf("narrowed to today: %+v", today)
	}
	if day := today.Totals(); day.Calls != 4 || day.CostUSD != 7.25 || len(day.ByKind) != 3 || day.ByKind[0].Kind != StreamRun || day.ByKind[2].Kind != StreamExchange {
		t.Fatalf("today totals %+v", day)
	}
	// The report it was narrowed from is untouched.
	if len(report.Rows) != 5 || report.Oldest != "2026-09-13" {
		t.Fatalf("narrowing changed the report: %+v", report)
	}
}

// A run's row mixes the developer's session with the reviewer's one-shot
// invocations, and the two are not cached alike, so the row and the totals
// carry each role's spend apart with its cost apportioned across what it was
// billed for. A run log recorded before a terminal named its role is read by
// the ledger's bracket, a branch review's terminals are the reviewer's, and a
// conversation terminal that named nobody is nobody's rather than guessed.
func TestStreamStoreSpendsByTheRoleThatSpent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, conversations, reviews := streamStores(t, root)
	store := newStreamStore(t, root)

	today := time.Date(2026, 9, 20, 12, 0, 0, 0, time.Local)
	state := testState(t, StatusSucceeded)
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendStreamEvent(t, runs, state.RunID, 1, execution.EventRunStarted, today, nil)
	// The developer's session: nearly everything read back from the cache.
	appendStreamEvent(t, runs, state.RunID, 2, execution.EventRunCompleted, today, rolePayload("developer", 5.0, 0, 1000, 100000, 900000))
	// The reviewer's one turn: a small prefix read, the rest written for an hour.
	appendStreamEvent(t, runs, state.RunID, 3, execution.EventRunCompleted, today, rolePayload("reviewer", 0.615218, 2, 8680, 39517, 6076))
	// A branch review's terminal names no role, and is the reviewer's all the same.
	reviewID := mustBranchReviewID(t)
	appendReviewEvent(t, reviews, reviewID, 1, execution.EventReviewStarted, today, nil)
	appendReviewEvent(t, reviews, reviewID, 2, execution.EventRunCompleted, today, invocationPayload(1, 1, 1, 1, 1))
	// A conversation's terminal that named no role at a schema whose terminals
	// do is nobody's.
	conversationID := mustConversationID(t)
	appendConversationEvent(t, conversations, conversationID, 1, execution.EventRunCompleted, today, invocationPayload(2, 1, 1, 1, 1))

	report, err := store.Spend(SpendQuery{Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	var run SpendRow
	for _, row := range report.Rows {
		if row.Kind == StreamRun {
			run = row
		}
	}
	if len(run.Roles) != 2 || run.Roles[0].Role != domain.RoleDeveloper || run.Roles[1].Role != domain.RoleReviewer {
		t.Fatalf("the run's row splits by role as %+v, want the developer then the reviewer", run.Roles)
	}
	reviewer := run.Roles[1]
	if reviewer.Calls != 1 || reviewer.CostUSD != 0.615218 || reviewer.Usage.CacheWrite1hTokens != 39517 {
		t.Fatalf("the reviewer's part = %+v", reviewer)
	}
	if reviewer.Split.CacheWriteUSD < 0.395 || reviewer.Split.CacheWriteUSD > 0.3952 || reviewer.Split.CacheReadUSD > 0.0031 {
		t.Fatalf("the reviewer's cost splits to %+v, want nearly two thirds of it on the cache write and almost nothing on the read", reviewer.Split)
	}

	totals := report.Totals()
	if len(totals.ByRole) != 3 || totals.ByRole[0].Role != domain.RoleDeveloper || totals.ByRole[1].Role != domain.RoleReviewer || totals.ByRole[2].Role != "" {
		t.Fatalf("the totals split by role as %+v, want the developer, the reviewer, and the unattributed turn last", totals.ByRole)
	}
	if totals.ByRole[1].Calls != 2 || totals.ByRole[1].CostUSD != 1.615218 {
		t.Fatalf("the reviewer's total = %+v, want the run's review and the branch review together", totals.ByRole[1])
	}
	if totals.ByRole[2].Calls != 1 || totals.ByRole[2].CostUSD != 2 {
		t.Fatalf("the unattributed total = %+v, want the conversation's one turn", totals.ByRole[2])
	}
	var spent float64
	for _, part := range totals.ByRole {
		spent += part.CostUSD
	}
	if spent != totals.CostUSD {
		t.Fatalf("the roles add up to %v, the report to %v", spent, totals.CostUSD)
	}
}

// rolePayload is a terminal that names its role and splits its cache write by
// lifetime, the way the backend has recorded one since TerminalRoleSchemaVersion.
func rolePayload(role string, cost float64, input, output, cacheWrite, cacheRead int64) map[string]any {
	payload := invocationPayload(cost, input, output, cacheWrite, cacheRead)
	payload["role"] = role
	payload["usage"].(map[string]any)["cache_creation"] = map[string]any{
		"ephemeral_1h_input_tokens": cacheWrite,
		"ephemeral_5m_input_tokens": 0,
	}
	return payload
}

// streamMoment is when the fabricated streams here happened. It is fixed so
// every assertion about ordering and dating is a fact about the code rather than
// about when the test ran.
var streamMoment = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

// strings32 is thirty-two of one hex digit, which is the body of every id here.
func strings32(digit byte) string {
	body := make([]byte, 32)
	for i := range body {
		body[i] = digit
	}
	return string(body)
}

// touch says when a stream's log was last appended to, which is the only thing
// deciding which of two streams is newer.
func touch(t *testing.T, root, id string, at time.Time) {
	t.Helper()
	path := filepath.Join(root, id+".events.jsonl")
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("Chtimes(%s) error = %v", path, err)
	}
}

func newStreamStore(t *testing.T, root string) *StreamStore {
	t.Helper()
	store, err := NewStreamStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStreamStore() error = %v", err)
	}
	return store
}

// streamStores are the three stores that own the streams, all under one root, so
// what the reader sees is what the writers actually wrote.
func streamStores(t *testing.T, root string) (*Store, *ConversationStore, *BranchReviewStore) {
	t.Helper()
	runs, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	reviews, err := NewBranchReviewStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewBranchReviewStore() error = %v", err)
	}
	return runs, newConversationStore(t, root), reviews
}

func appendStreamEvent(t *testing.T, store *Store, runID string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, runID, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func appendConversationEvent(t *testing.T, store *ConversationStore, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, id, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func appendReviewEvent(t *testing.T, store *BranchReviewStore, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, id, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func newStreamEvent(t *testing.T, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) execution.Event {
	t.Helper()
	if payload == nil {
		payload = map[string]any{"session_id": "session-stream"}
	}
	event, err := execution.NewEvent(id, sequence, at, eventType, "claude-code", payload)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	return event
}

// invocationPayload is what the provider reports when an invocation ends, which
// is the only place the cost and the token counts are ever written down.
// The cost is what the provider reported, which for a second invocation of a
// session it was asked to resume is that session's running total rather than
// what the invocation itself cost. A test with more than one terminal on one
// stream therefore passes rising figures, and what the ledger reports is the
// difference between them.
func invocationPayload(reportedTotal float64, input, output, cacheWrite, cacheRead int64) map[string]any {
	return map[string]any{
		"session_id":     "session-stream",
		"total_cost_usd": reportedTotal,
		"usage": map[string]any{
			"input_tokens":                input,
			"output_tokens":               output,
			"cache_creation_input_tokens": cacheWrite,
			"cache_read_input_tokens":     cacheRead,
		},
	}
}

func mustConversationID(t *testing.T) string {
	t.Helper()
	id, err := NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	return id
}

func mustBranchReviewID(t *testing.T) string {
	t.Helper()
	id, err := NewBranchReviewID()
	if err != nil {
		t.Fatalf("NewBranchReviewID() error = %v", err)
	}
	return id
}

// A management conversation resumes one provider session across every turn it
// takes, so every terminal after the first reports what the conversation has
// cost since it opened. This is the ledger `yoyo status --spend` and the
// dashboard's throughput are read from, and summing those figures is what had
// the development manager's conversation reading at $24,659 against an actual
// $825 over this product's recorded history.
//
// What it reports is therefore the session's final total, however many turns it
// took to reach it.
func TestAResumedConversationIsPricedAtWhatEachTurnAddedRatherThanTheWholeAgain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, conversations, _ := streamStores(t, root)
	store := newStreamStore(t, root)

	today := time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local)
	id := mustConversationID(t)
	appendConversationEvent(t, conversations, id, 1, execution.EventRunStarted, today, nil)
	// Five turns of one session, each reporting what the session has cost so far.
	for sequence, reportedTotal := range []float64{0.50, 1.10, 1.75, 2.30, 3.00} {
		appendConversationEvent(t, conversations, id, uint64(sequence)+2,
			execution.EventRunCompleted, today, invocationPayload(reportedTotal, 1, 1, 1, 1))
	}

	report, err := store.Spend(SpendQuery{Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	totals := report.Totals()
	if totals.Calls != 5 {
		t.Fatalf("priced %d invocation(s), want every turn counted", totals.Calls)
	}
	if totals.CostUSD != 3.00 {
		t.Fatalf("the conversation reads as %v, want the session's final total of 3; "+
			"summing what each turn reported would have made it %v", totals.CostUSD, 8.65)
	}
}

// A duplicate terminal is priced by the ledger's rule in the listing too, so
// `yoyo status --spend` and `yoyo cost` count the same money for a run whose
// provider ended an invocation twice. The figures are run-f3755e3f's last
// attempt, which nothing resumed afterwards.
func TestStreamStoreSpendsWhatADuplicateTerminalCost(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, _, _ := streamStores(t, root)
	store := newStreamStore(t, root)

	today := time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local)
	state := testState(t, StatusSucceeded)
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendStreamEvent(t, runs, state.RunID, 1, execution.EventRunCompleted, today, invocationPayload(255.2897625, 1, 1, 1, 1))
	appendStreamEvent(t, runs, state.RunID, 2, execution.EventProcessOutput, today, map[string]any{
		"anomaly":        execution.DuplicateTerminalAnomaly,
		"total_cost_usd": 256.107327,
	})
	appendStreamEvent(t, runs, state.RunID, 3, execution.EventRunCompleted, today, invocationPayload(264.6636765, 1, 1, 1, 1))
	appendStreamEvent(t, runs, state.RunID, 4, execution.EventProcessOutput, today, map[string]any{
		"anomaly":        execution.DuplicateTerminalAnomaly,
		"total_cost_usd": 265.540555,
	})

	report, err := store.Spend(SpendQuery{Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	totals := report.Totals()
	if totals.Calls != 2 {
		t.Fatalf("priced %d invocation(s), want two: a duplicate is not an invocation of its own", totals.Calls)
	}
	if math.Abs(totals.CostUSD-265.540555) > 1e-9 {
		t.Fatalf("the run reads as %v, want the session's last reported total 265.540555", totals.CostUSD)
	}
}
