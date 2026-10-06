package runstate

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// An exchange is a provider invocation the harness made and nobody's run, so
// nothing that reads runs finds it. Reading it here is what keeps it out of the
// item prices and in what the harness has spent altogether.
func TestExchangeSpendSumsWhatTheRolesSpentAskingEachOther(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// A product whose roles have never asked each other anything has no exchange
	// directory at all, which is not a failure to read.
	if spend := store.ExchangeSpend(); spend.Recorded() || spend.CostUSD != 0 || !spend.Known() {
		t.Fatalf("ExchangeSpend() = %#v, want nothing recorded", spend)
	}

	// The exchange store is built the way the harness builds it, from the state
	// root, so this is also the claim that the read model finds the same
	// directory the conductor writes to.
	exchanges := newTestExchangeStore(t, root)
	if err := exchanges.Save(testExchange("a")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	second := testExchange("b")
	answered := second.UpdatedAt.Add(time.Minute)
	second.Rounds = append(second.Rounds, exchange.Round{
		Number:     2,
		Question:   "and what does it cost to leave it until later?",
		Answer:     "More again, and the difference is the interest.",
		CostUSD:    0.75,
		AskedAt:    second.UpdatedAt,
		AnsweredAt: &answered,
	})
	second.UpdatedAt = answered
	if err := exchanges.Save(second); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	spend := store.ExchangeSpend()
	if !spend.Known() || !spend.Recorded() {
		t.Fatalf("ExchangeSpend() = %#v, want a known recorded figure", spend)
	}
	if spend.Exchanges != 2 || spend.Rounds != 3 {
		t.Fatalf("ExchangeSpend() = %#v, want 2 exchanges over 3 rounds", spend)
	}
	// Every round the provider charged for, whichever thread it belonged to.
	if spend.CostUSD != 1.25 {
		t.Fatalf("ExchangeSpend() total = %v, want 1.25", spend.CostUSD)
	}
	// None of it reaches an item's price, because the record holds nothing to
	// join an item on. The test below is what holds that to the record rather
	// than to this comment.
	prices, err := store.Prices()
	if err != nil {
		t.Fatalf("Prices() error = %v", err)
	}
	if len(prices) != 0 {
		t.Fatalf("Prices() = %#v, want no item priced by an exchange", prices)
	}
}

// The aggregate is product-wide rather than per item because the exchange
// record names no item to be per. That is a fact about the recorded shape, and
// this is where it is checked: the moment an exchange can name a work item — or
// name a run, which names one — the join belongs in ItemPrice beside the runs
// rather than on a row of its own, and the report reading it is short until it
// is written.
//
// It is asserted over the type instead of over an instance so that a field
// added to the record fails here, at the join that would have to change, rather
// than passing silently and leaving the money unattributed.
func TestAnExchangeRecordNamesNoWorkItemToAttributeItsSpendTo(t *testing.T) {
	t.Parallel()

	attributable := func(field reflect.StructField) bool {
		name := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
		return strings.Contains(name, "workitem") || strings.Contains(name, "work_item") ||
			strings.Contains(name, "runid") || strings.Contains(name, "run_id")
	}
	for _, recorded := range []reflect.Type{
		reflect.TypeOf(exchange.Exchange{}),
		// The parties are checked too: what an exchange names about each side is
		// a role, an agent, and the conversation it spoke from, and a conversation
		// is not a work item — the same conversation asks about whatever it is
		// discussing that day.
		reflect.TypeOf(exchange.Party{}),
		reflect.TypeOf(exchange.Round{}),
	} {
		for i := 0; i < recorded.NumField(); i++ {
			if attributable(recorded.Field(i)) {
				t.Fatalf("%s.%s names work an exchange's spend could be attributed to; "+
					"Store.ExchangeSpend sums the product's exchanges into one figure and must now join them onto the item instead",
					recorded.Name(), recorded.Field(i).Name)
			}
		}
	}
}

// An exchange nobody can read is not an exchange that cost nothing — and it is
// not the exchanges beside it either. It is counted and left out, they are
// priced as usual, and the figure that results says it is a floor. A record
// that reduced the whole total to nothing would reintroduce the undercount this
// figure exists to remove, one corrupt file at a time.
func TestExchangeSpendPricesWhatItCanReadAndCountsWhatItCannot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	exchanges := newTestExchangeStore(t, root)
	readable := testExchange("c")
	if err := exchanges.Save(readable); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	broken := filepath.Join(exchanges.Root(), "exchange-"+strings.Repeat("d", 32)+".json")
	if err := os.WriteFile(broken, []byte("{not an exchange"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	spend := store.ExchangeSpend()
	if spend.Known() {
		t.Fatalf("ExchangeSpend() = %#v, want a figure that says it is short", spend)
	}
	if !spend.Recorded() || !spend.Enumerated() {
		t.Fatalf("ExchangeSpend() = %#v, want records that were counted", spend)
	}
	if spend.Unreadable != 1 || spend.Unknown == "" {
		t.Fatalf("ExchangeSpend() = %#v, want one unreadable record and the reason", spend)
	}
	// The readable exchange keeps its price. This is the whole finding: the
	// broken file must cost the total that one thread and nothing else.
	if spend.Exchanges != 1 || spend.Rounds != 1 || spend.CostUSD != readable.CostUSD() {
		t.Fatalf("ExchangeSpend() = %#v, want the readable exchange still priced at %v",
			spend, readable.CostUSD())
	}

	// Exchanges that cannot even be listed are a different answer again: what is
	// missing is unknown in number as well as in amount, so there is not even a
	// floor to state. A file where the directory should be is that, and unlike a
	// mode bit it means the same thing whoever is running the test.
	unlistable, err := NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	blocked := unlistable.exchanges().Root()
	if err := os.MkdirAll(filepath.Dir(blocked), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	blind := unlistable.ExchangeSpend()
	if blind.Known() || blind.Enumerated() {
		t.Fatalf("ExchangeSpend() = %#v, want nothing enumerated", blind)
	}
	if blind.Unknown == "" || blind.Unreadable != 0 {
		t.Fatalf("ExchangeSpend() = %#v, want the reason and no count it cannot have", blind)
	}
}

// An item's price is what every run made for it cost, which is the whole reason
// it is not answerable per run: the rejected attempt and the successful one are
// both part of what the work cost.
func TestStorePricesEveryRunMadeForAnItem(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	first := testState(t, StatusFailed)
	first.WorkItemID = "yoyodyne-ifd.2.7"
	first.Phase = PhaseReviewing
	first.ProviderSessionID = "session-developer"
	if err := store.Create(first); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// A developer invocation, a repair attempt, and one the provider ended in an
	// error: the failure cost money too, so it is priced rather than ignored.
	appendLegacyCostEvents(t, store, first.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 6.5)
	appendLegacyCostEvents(t, store, first.RunID, 2, execution.EventRunCompleted, domain.RoleDeveloper, 2.25)
	appendLegacyCostEvents(t, store, first.RunID, 3, execution.EventRunFailed, domain.RoleDeveloper, 0.25)

	second := testState(t, StatusSucceeded)
	second.RunID = mustRunID(t)
	second.WorkItemID = first.WorkItemID
	second.StartedAt = first.StartedAt.Add(time.Hour)
	second.UpdatedAt = second.StartedAt
	completedAt := second.StartedAt
	second.CompletedAt = &completedAt
	second.Phase = PhaseComplete
	second.ProviderSessionID = "session-developer-2"
	second.ReviewSessionID = "session-reviewer"
	if err := store.Create(second); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, second.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 19.0)

	// Another item's run must never reach this item's price.
	other := testState(t, StatusSucceeded)
	other.RunID = mustRunID(t)
	other.WorkItemID = "yoyodyne-ifd.41"
	other.ProviderSessionID = "session-other"
	if err := store.Create(other); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, other.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 100)

	price, err := store.Price(first.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if !price.Priced() || !price.Recorded() {
		t.Fatalf("Price() = %#v, want a complete recorded price", price)
	}
	if len(price.Runs) != 2 {
		t.Fatalf("Price() priced %d run(s), want 2", len(price.Runs))
	}
	// Oldest first, so the breakdown reads as the history it is.
	if price.Runs[0].RunID != first.RunID || price.Runs[1].RunID != second.RunID {
		t.Fatalf("Price() run order = %q, %q", price.Runs[0].RunID, price.Runs[1].RunID)
	}
	if price.Runs[0].Invocations != 3 || price.Runs[0].CostUSD != 9.0 {
		t.Fatalf("failed run = %#v, want 3 invocations costing 9", price.Runs[0])
	}
	if price.TotalUSD != 28.0 {
		t.Fatalf("Price() total = %v, want 28", price.TotalUSD)
	}
}

// A run whose evidence is gone is stated as unknown. Pricing it as zero would
// silently understate every total it entered.
func TestStorePricesARunWithNoSurvivingRecordAsUnknown(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	lost := testState(t, StatusSucceeded)
	lost.WorkItemID = "yoyodyne-ifd.41"
	lost.ProviderSessionID = "session-developer"
	if err := store.Create(lost); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	kept := testState(t, StatusSucceeded)
	kept.RunID = mustRunID(t)
	kept.WorkItemID = lost.WorkItemID
	kept.StartedAt = lost.StartedAt.Add(time.Hour)
	kept.UpdatedAt = kept.StartedAt
	completedAt := kept.StartedAt
	kept.CompletedAt = &completedAt
	kept.ProviderSessionID = "session-developer-2"
	if err := store.Create(kept); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, kept.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 3.5)

	price, err := store.Price(lost.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.UnknownRuns != 1 || price.Priced() {
		t.Fatalf("Price() = %#v, want one unknown run", price)
	}
	if price.Runs[0].Known() || price.Runs[0].CostUSD != 0 {
		t.Fatalf("lost run = %#v, want unknown and no claimed cost", price.Runs[0])
	}
	// The known run is still priced, and the total is what is known rather than
	// a guess at what is not.
	if price.TotalUSD != 3.5 {
		t.Fatalf("Price() total = %v, want 3.5", price.TotalUSD)
	}
}

// The two ways a log holds no invocation are different facts: a run that never
// invoked a provider really did cost nothing, and a run that recorded a session
// and holds no invocation has lost its evidence.
func TestStoreTellsARunThatSpentNothingFromOneThatLostItsEvidence(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	unspent := testState(t, StatusFailed)
	unspent.WorkItemID = "yoyodyne-ifd.41"
	if err := store.Create(unspent); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendEvent(t, store, unspent.RunID, 1, execution.EventRunStarted, nil)

	price, err := store.Price(unspent.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if !price.Priced() || price.TotalUSD != 0 || price.Runs[0].Invocations != 0 {
		t.Fatalf("Price() = %#v, want a run priced at nothing", price)
	}

	emptied := testState(t, StatusFailed)
	emptied.RunID = mustRunID(t)
	emptied.WorkItemID = "yoyodyne-ifd.42"
	emptied.ProviderSessionID = "session-developer"
	if err := store.Create(emptied); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendEvent(t, store, emptied.RunID, 1, execution.EventRunStarted, nil)

	price, err = store.Price(emptied.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.UnknownRuns != 1 {
		t.Fatalf("Price() = %#v, want the run with a session and no invocation to be unknown", price)
	}
}

// An item nobody has run has no price at all. Reporting zero would put a price
// tag reading nothing on work that was never done.
func TestStorePricesAnItemWithNoRecordedRunsAsUnrecorded(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	price, err := store.Price("yoyodyne-ifd.99")
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.Recorded() || len(price.Runs) != 0 {
		t.Fatalf("Price() = %#v, want nothing recorded", price)
	}
	if _, err := store.Price("  "); err == nil {
		t.Fatal("Price() without a work item error = nil")
	}
}

// The ledger is what prices items closed before anything recorded a price: it
// groups every recorded run by the item it served.
func TestStorePricesEveryItemItHasRun(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	for _, item := range []string{"yoyodyne-ifd.42", "yoyodyne-ifd.41", "yoyodyne-ifd.41"} {
		state := testState(t, StatusSucceeded)
		state.RunID = mustRunID(t)
		state.WorkItemID = item
		state.ProviderSessionID = "session-" + state.RunID
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		appendLegacyCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 1.5)
	}

	prices, err := store.Prices()
	if err != nil {
		t.Fatalf("Prices() error = %v", err)
	}
	if len(prices) != 2 {
		t.Fatalf("Prices() = %#v, want two items", prices)
	}
	if prices[0].WorkItemID != "yoyodyne-ifd.41" || len(prices[0].Runs) != 2 || prices[0].TotalUSD != 3.0 {
		t.Fatalf("Prices()[0] = %#v", prices[0])
	}
	if prices[1].WorkItemID != "yoyodyne-ifd.42" || prices[1].TotalUSD != 1.5 {
		t.Fatalf("Prices()[1] = %#v", prices[1])
	}
}

// An event log that cannot be read prices as unknown rather than failing the
// whole answer: the runs beside it are still priceable, and losing them would
// answer less than the records hold.
func TestStorePricesAnUnreadableEventLogAsUnknown(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.41"
	state.ProviderSessionID = "session-developer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.eventPath(state.RunID)
	if err != nil {
		t.Fatalf("eventPath() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("{\"type\":\"run.completed\" not json\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.UnknownRuns != 1 || price.Runs[0].Known() {
		t.Fatalf("Price() = %#v, want the unreadable log priced as unknown", price)
	}
}

// The cheap line filter is allowed to over-match; what decides is the decoded
// event type, so an agent quoting an event name never invents an invocation.
func TestStorePricesOnlyRealInvocations(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.41"
	state.ProviderSessionID = "session-developer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendEvent(t, store, state.RunID, 1, execution.EventAgentMessage, map[string]any{
		"text":           `the last "run.completed" event said it was done`,
		"total_cost_usd": 99.0,
	})
	appendLegacyCostEvents(t, store, state.RunID, 2, execution.EventRunCompleted, domain.RoleDeveloper, 4.0)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.TotalUSD != 4.0 || price.Runs[0].Invocations != 1 {
		t.Fatalf("Price() = %#v, want only the real invocation priced", price)
	}
}

// money is what a phase cost and how many invocations it took, with the token
// usage beside it set aside. The assertions that use it are about where a run's
// money landed, and the invocations they are made of carry no usage object --
// which is itself recorded, so a whole-struct comparison would be asserting the
// absence of a measurement in tests that are not about one.
func money(phase PhaseCost) PhaseCost {
	phase.Tokens = TokenUsage{}
	return phase
}

// A price that says only what a piece of work cost invites the question it
// cannot answer: whether the money went on making the change, on judging it, or
// on making it again. The split comes out of the log's own shape -- a review
// announces itself and makes one invocation, and the developer invocations
// between the reviews group into attempts -- and it always adds back up to the
// total it was split from.
func TestStoreSplitsWhatARunSpentByThePhaseItServed(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.83"
	state.ProviderSessionID = "session-developer"
	state.ReviewSessionID = "session-reviewer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The change, a review that asked for repair, the repair, and the review that
	// took it: the shape every run of any length has.
	appendLegacyCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 9.0)
	appendEvent(t, store, state.RunID, 2, execution.EventReviewStarted, nil)
	appendLegacyCostEvents(t, store, state.RunID, 3, execution.EventRunCompleted, domain.RoleReviewer, 2.0)
	appendEvent(t, store, state.RunID, 4, execution.EventReviewCompleted, nil)
	appendLegacyCostEvents(t, store, state.RunID, 5, execution.EventRunCompleted, domain.RoleDeveloper, 4.0)
	appendEvent(t, store, state.RunID, 6, execution.EventReviewStarted, nil)
	appendLegacyCostEvents(t, store, state.RunID, 7, execution.EventRunCompleted, domain.RoleReviewer, 1.5)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if money(phases.Development) != (PhaseCost{CostUSD: 9.0, Invocations: 1}) {
		t.Fatalf("development = %#v, want the first attempt alone", phases.Development)
	}
	if money(phases.Review) != (PhaseCost{CostUSD: 3.5, Invocations: 2}) {
		t.Fatalf("review = %#v, want both reviewer invocations", phases.Review)
	}
	if money(phases.Repair) != (PhaseCost{CostUSD: 4.0, Invocations: 1}) {
		t.Fatalf("repair = %#v, want the second developer attempt", phases.Repair)
	}
	// The split is a decomposition of the price rather than a second opinion
	// about it, so it has to reconstruct what it decomposed.
	if phases.TotalUSD() != price.Runs[0].CostUSD || phases.Invocations() != price.Runs[0].Invocations {
		t.Fatalf("split = %v across %d, run = %v across %d",
			phases.TotalUSD(), phases.Invocations(), price.Runs[0].CostUSD, price.Runs[0].Invocations)
	}
	if price.Phases.TotalUSD() != price.TotalUSD {
		t.Fatalf("item split = %v, item total = %v", price.Phases.TotalUSD(), price.TotalUSD)
	}
}

// An attempt the provider refused or killed is reissued in the same session, so
// the invocation after it is the same attempt still being made. Counting it as a
// fresh attempt would charge the change to repair for something nobody asked a
// developer to fix.
func TestStoreChargesAReissuedInvocationToTheAttemptItReissues(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.83"
	state.ProviderSessionID = "session-developer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The provider refused the first attempt for want of capacity, the reissue
	// finished it, and only then did a review send it back for repair.
	appendLegacyCostEvents(t, store, state.RunID, 1, execution.EventRunFailed, domain.RoleDeveloper, 10.5)
	appendLegacyCostEvents(t, store, state.RunID, 2, execution.EventRunCompleted, domain.RoleDeveloper, 8.0)
	appendEvent(t, store, state.RunID, 3, execution.EventReviewStarted, nil)
	appendLegacyCostEvents(t, store, state.RunID, 4, execution.EventRunCompleted, domain.RoleReviewer, 2.0)
	appendLegacyCostEvents(t, store, state.RunID, 5, execution.EventRunCompleted, domain.RoleDeveloper, 3.0)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if money(phases.Development) != (PhaseCost{CostUSD: 18.5, Invocations: 2}) {
		t.Fatalf("development = %#v, want the refused attempt and its reissue", phases.Development)
	}
	if money(phases.Repair) != (PhaseCost{CostUSD: 3.0, Invocations: 1}) {
		t.Fatalf("repair = %#v, want only the attempt the review asked for", phases.Repair)
	}
	if phases.TotalUSD() != price.TotalUSD {
		t.Fatalf("split = %v, total = %v", phases.TotalUSD(), price.TotalUSD)
	}
}

// A review the provider never answered closes with nothing at all. The bracket
// is closed by the one invocation a review makes rather than by the review
// closing, so an unanswered one cannot swallow whatever the run does next.
func TestStoreClosesAReviewBracketOnTheInvocationItMade(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusFailed)
	state.WorkItemID = "yoyodyne-ifd.83"
	state.ProviderSessionID = "session-developer"
	state.ReviewSessionID = "session-reviewer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 6.0)
	appendEvent(t, store, state.RunID, 2, execution.EventReviewStarted, nil)
	// The reviewer died without a verdict, so nothing closed the review.
	appendLegacyCostEvents(t, store, state.RunID, 3, execution.EventRunFailed, domain.RoleReviewer, 0.5)
	appendLegacyCostEvents(t, store, state.RunID, 4, execution.EventRunCompleted, domain.RoleDeveloper, 2.5)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if money(phases.Review) != (PhaseCost{CostUSD: 0.5, Invocations: 1}) {
		t.Fatalf("review = %#v, want only the reviewer's own invocation", phases.Review)
	}
	if money(phases.Repair) != (PhaseCost{CostUSD: 2.5, Invocations: 1}) {
		t.Fatalf("repair = %#v, want the developer invocation after the lost review", phases.Repair)
	}
}

// Every invocation says which role made it, so the split reads the phase off
// the invocation rather than off where its terminal happens to sit. Nothing here
// announces a review, and the reviewer's money still lands in review: a bracket
// somebody forgot to open, or a reviewer asked again without re-announcing, no
// longer moves a review into the developer's columns.
func TestStoreReadsAnInvocationsPhaseFromTheRoleItRecorded(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.172"
	state.ProviderSessionID = "session-developer"
	state.ReviewSessionID = "session-reviewer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendRoleCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 9.0)
	appendRoleCostEvents(t, store, state.RunID, 2, execution.EventRunCompleted, domain.RoleReviewer, 2.0)
	appendRoleCostEvents(t, store, state.RunID, 3, execution.EventRunCompleted, domain.RoleDeveloper, 4.0)
	appendRoleCostEvents(t, store, state.RunID, 4, execution.EventRunCompleted, domain.RoleReviewer, 1.5)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if money(phases.Development) != (PhaseCost{CostUSD: 9.0, Invocations: 1}) {
		t.Fatalf("development = %#v, want the first developer attempt alone", phases.Development)
	}
	if money(phases.Review) != (PhaseCost{CostUSD: 3.5, Invocations: 2}) {
		t.Fatalf("review = %#v, want both reviewer invocations without an announcement", phases.Review)
	}
	if money(phases.Repair) != (PhaseCost{CostUSD: 4.0, Invocations: 1}) {
		t.Fatalf("repair = %#v, want the second developer attempt", phases.Repair)
	}
	if money(phases.Unattributed) != (PhaseCost{}) {
		t.Fatalf("unattributed = %#v, want nothing left over", phases.Unattributed)
	}
	if phases.TotalUSD() != price.TotalUSD {
		t.Fatalf("split = %v, total = %v", phases.TotalUSD(), price.TotalUSD)
	}
}

// A phase bucket is somewhere an invocation is put, never somewhere it lands by
// default. An invocation from a role the split has no place for -- a summarizer,
// a second reviewer nobody accounted for, anything a later feature invokes into
// a run -- is money that stays visible as unplaced: it is in the run's total,
// because the harness spent it, and in none of the three, because charging it to
// repair would inflate repair on exactly the runs the figure is read from.
func TestStoreLeavesAnInvocationThatNamedNoPhaseOutOfEveryPhase(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.172"
	state.ProviderSessionID = "session-developer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendRoleCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 9.0)
	// An invocation the phase split knows nothing about, sitting exactly where an
	// unannounced one used to be read as a repair attempt.
	appendRoleCostEvents(t, store, state.RunID, 2, execution.EventRunCompleted, domain.RoleProductManager, 6.0)
	appendRoleCostEvents(t, store, state.RunID, 3, execution.EventRunCompleted, domain.RoleDeveloper, 4.0)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if money(phases.Unattributed) != (PhaseCost{CostUSD: 6.0, Invocations: 1}) {
		t.Fatalf("unattributed = %#v, want the invocation that named no phase", phases.Unattributed)
	}
	if money(phases.Development) != (PhaseCost{CostUSD: 9.0, Invocations: 1}) {
		t.Fatalf("development = %#v, want the first developer attempt alone", phases.Development)
	}
	// The developer attempt after it is still the second attempt, and still the
	// only repair: an invocation nothing placed does not end an attempt either.
	if money(phases.Repair) != (PhaseCost{CostUSD: 4.0, Invocations: 1}) {
		t.Fatalf("repair = %#v, want only the developer's second attempt", phases.Repair)
	}
	if phases.TotalUSD() != price.TotalUSD || price.TotalUSD != 19.0 {
		t.Fatalf("split = %v, total = %v, want the unplaced money in both", phases.TotalUSD(), price.TotalUSD)
	}
}

// The criterion the whole attribution exists for: no priced terminal lands in a
// phase because of where it sits. An invocation recorded today that says nothing
// about whose it is has failed to say, and it is placed nowhere -- which is what
// makes a shadow reviewer, a summarizer, or anything a later feature invokes
// into a run visible as unplaced money instead of silently inflating repair on
// the runs the figures are read from.
//
// The schema version is what tells that apart from a log written before there
// was a role to omit. Both terminals below carry no role and sit in exactly the
// same place; only the version differs, and only one of them is placed.
func TestStoreWillNotPlaceATerminalThatCouldHaveNamedItsPhaseAndDidNot(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	current := testState(t, StatusSucceeded)
	current.WorkItemID = "yoyodyne-ifd.172"
	current.ProviderSessionID = "session-developer"
	if err := store.Create(current); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendRoleCostEvents(t, store, current.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 9.0)
	// Recorded at today's schema with no role: an invocation that could have said
	// whose it was and did not.
	appendEvent(t, store, current.RunID, 2, execution.EventRunCompleted, map[string]any{"total_cost_usd": 6.0})

	price, err := store.Price(current.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if money(phases.Unattributed) != (PhaseCost{CostUSD: 6.0, Invocations: 1}) {
		t.Fatalf("unattributed = %#v, want the terminal that named no phase", phases.Unattributed)
	}
	if money(phases.Repair) != (PhaseCost{}) {
		t.Fatalf("repair = %#v, want nothing charged to a phase by position", phases.Repair)
	}
	if phases.TotalUSD() != price.TotalUSD || price.TotalUSD != 15.0 {
		t.Fatalf("split = %v, total = %v, want the unplaced money in both", phases.TotalUSD(), price.TotalUSD)
	}

	// The same shape at the schema those runs were actually recorded at is a
	// developer's second attempt, exactly as it always was: nothing can be added
	// to a log that is already closed, so reading it any other way would rewrite
	// what the recorded history cost rather than describe it.
	legacy := testState(t, StatusSucceeded)
	legacy.RunID = mustRunID(t)
	legacy.WorkItemID = current.WorkItemID
	legacy.StartedAt = current.StartedAt.Add(time.Hour)
	legacy.UpdatedAt = legacy.StartedAt
	legacy.ProviderSessionID = "session-developer-2"
	if err := store.Create(legacy); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, legacy.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 9.0)
	appendLegacyCostEvents(t, store, legacy.RunID, 2, execution.EventRunCompleted, domain.RoleDeveloper, 6.0)

	price, err = store.Price(current.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	older := price.Runs[1].Phases
	if money(older.Repair) != (PhaseCost{CostUSD: 6.0, Invocations: 1}) {
		t.Fatalf("repair = %#v, want the older run read the way it was written", older.Repair)
	}
	if money(older.Unattributed) != (PhaseCost{}) {
		t.Fatalf("unattributed = %#v, want nothing unplaced in a log with no role to omit", older.Unattributed)
	}
}

// What a run waited comes from its own state rather than from its event log,
// which is why it survives what the money does not: a run nothing can price
// still says how long it was held up, and by whom.
func TestStoreReportsWhatARunWaitedEvenWhenItCannotBePriced(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	priced := testState(t, StatusSucceeded)
	priced.WorkItemID = "yoyodyne-ifd.83"
	priced.ProviderSessionID = "session-developer"
	priced.UsageLimitPausedSeconds = 3600
	priced.OperatorHeldSeconds = 900
	if err := store.Create(priced); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, priced.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 4.0)

	lost := testState(t, StatusFailed)
	lost.RunID = mustRunID(t)
	lost.WorkItemID = priced.WorkItemID
	lost.StartedAt = priced.StartedAt.Add(time.Hour)
	lost.UpdatedAt = lost.StartedAt
	lost.ProviderSessionID = "session-developer-2"
	lost.UsageLimitPausedSeconds = 1800
	if err := store.Create(lost); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	price, err := store.Price(priced.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.Runs[0].Phases.Waits != (Waits{UsageLimitSeconds: 3600, OperatorHoldSeconds: 900}) {
		t.Fatalf("priced run waits = %#v", price.Runs[0].Phases.Waits)
	}
	if price.Runs[1].Known() || price.Runs[1].Phases.Waits.UsageLimitSeconds != 1800 {
		t.Fatalf("unpriced run = %#v, want its wait recorded anyway", price.Runs[1])
	}
	if price.Phases.Waits.Total() != 105*time.Minute {
		t.Fatalf("item waits = %v, want every run's wait including the unpriced one", price.Phases.Waits.Total())
	}
}

// What a change to the assembled prompt bought is not a question dollars can
// answer -- a provider that changed its prices moves them too -- so the token
// usage the provider reported on every terminal is read beside the money: per
// run, and summed across the runs of an item, which is the window a cache-read
// share is actually measured over.
func TestStoreReadsTheCacheReadShareOfEveryPricedInvocation(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	first := testState(t, StatusSucceeded)
	first.WorkItemID = "yoyodyne-ifd.84"
	first.ProviderSessionID = "session-developer"
	first.ReviewSessionID = "session-reviewer"
	if err := store.Create(first); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// A developer attempt served mostly from the cache, and a review served
	// entirely fresh: the share is over both, because what a window cost in
	// tokens is what every invocation in it read.
	appendUsageCostEvents(t, store, first.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 9.0,
		usageTokens{InputTokens: 100, OutputTokens: 4000, CacheReadTokens: 700, CacheCreationTokens: 200})
	appendUsageCostEvents(t, store, first.RunID, 2, execution.EventRunCompleted, domain.RoleReviewer, 2.0,
		usageTokens{InputTokens: 1000, OutputTokens: 500})

	second := testState(t, StatusSucceeded)
	second.RunID = mustRunID(t)
	second.WorkItemID = first.WorkItemID
	second.StartedAt = first.StartedAt.Add(time.Hour)
	second.UpdatedAt = second.StartedAt
	second.ProviderSessionID = "session-developer-2"
	if err := store.Create(second); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendUsageCostEvents(t, store, second.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 5.0,
		usageTokens{InputTokens: 200, OutputTokens: 1000, CacheReadTokens: 1800})

	price, err := store.Price(first.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	// 700 cached of 1000 input for the attempt, and nothing of 1000 for the
	// review: 700 of 2000 across the run.
	if got := price.Runs[0].Tokens; got != (TokenUsage{Priced: 2, InputTokens: 1100, CacheReadTokens: 700, CacheCreationTokens: 200, OutputTokens: 4500, Measured: 2}) {
		t.Fatalf("run tokens = %#v, want every invocation in the run", got)
	}
	if share := price.Runs[0].Tokens.CacheReadShare(); share != 0.35 {
		t.Fatalf("run cache-read share = %v, want 0.35", share)
	}
	// The denominator is every input token however it was billed, not the fresh
	// input alone: a prompt served entirely from the cache reports almost no
	// fresh input, and dividing by that would read as the best cached prompt
	// there has ever been.
	if total := price.Tokens.InputTotal(); total != 4000 {
		t.Fatalf("item input total = %d, want fresh, cached and cache writes across both runs", total)
	}
	if share := price.Tokens.CacheReadShare(); share != 0.625 {
		t.Fatalf("item cache-read share = %v, want 2500 of 4000", share)
	}
	if price.Tokens.Measured != 3 || price.Tokens.Unreported != 0 {
		t.Fatalf("tokens = %#v, want all three invocations measured and none unreported", price.Tokens)
	}
}

// A run's aggregate share is decided by whichever phase reads the most, and the
// phases do not read alike: a developer session re-reads its own conversation
// every turn, and a review is one short invocation whose only cacheable part is
// the prefix it shares with the last review. Summed, a review that reads nothing
// at all is invisible behind a developer that reads nearly everything -- which is
// how yoyodyne-ifd.84 came to be measured against a figure its own effect could
// not move. The split is what makes that readable, and the run's total is still
// the sum of it.
func TestStoreSplitsWhatARunReadFromTheCacheByThePhaseThatReadIt(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.205"
	state.ProviderSessionID = "session-developer"
	state.ReviewSessionID = "session-reviewer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendUsageCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 9.0,
		usageTokens{InputTokens: 100, OutputTokens: 4000, CacheReadTokens: 900})
	// The reviewer pays to write a prefix and reads none of it back, which is
	// what every recorded review did before ifd.205.
	appendUsageCostEvents(t, store, state.RunID, 2, execution.EventRunCompleted, domain.RoleReviewer, 2.0,
		usageTokens{InputTokens: 0, OutputTokens: 500, CacheCreationTokens: 1000})
	appendUsageCostEvents(t, store, state.RunID, 3, execution.EventRunCompleted, domain.RoleDeveloper, 3.0,
		usageTokens{InputTokens: 100, OutputTokens: 300, CacheReadTokens: 100})

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if share := phases.Development.Tokens.CacheReadShare(); share != 0.9 {
		t.Fatalf("development cache-read share = %v, want 900 of 1000", share)
	}
	if share := phases.Review.Tokens.CacheReadShare(); share != 0 {
		t.Fatalf("review cache-read share = %v, want the nought a review that read nothing reports", share)
	}
	// A review measured at nothing and a review nobody measured are the same
	// figure and opposite facts, so the count is what tells them apart here as it
	// does everywhere else.
	if !phases.Review.Tokens.Reported() || phases.Review.Tokens.Measured != 1 {
		t.Fatalf("review tokens = %#v, want one measured invocation", phases.Review.Tokens)
	}
	if share := phases.Repair.Tokens.CacheReadShare(); share != 0.5 {
		t.Fatalf("repair cache-read share = %v, want 100 of 200", share)
	}
	// The split is a decomposition of the run's usage rather than a second
	// reading of it, so the phases add back up to what the run was billed.
	var summed TokenUsage
	for _, phase := range []PhaseCost{phases.Development, phases.Review, phases.Repair, phases.Unattributed} {
		summed.Merge(phase.Tokens)
	}
	if summed != price.Runs[0].Tokens {
		t.Fatalf("phases summed = %#v, want the run's own usage %#v", summed, price.Runs[0].Tokens)
	}
	// And an item aggregates the phases exactly as it aggregates the money.
	if price.Phases.Review.Tokens != phases.Review.Tokens {
		t.Fatalf("item review tokens = %#v, want the run's %#v", price.Phases.Review.Tokens, phases.Review.Tokens)
	}
}

// The provider's usage object arrives with more in it than the four figures a
// share is computed from, and two of the things beside them -- the cache_creation
// breakdown and the per-iteration array -- repeat the very key names being read.
// A reader that descended into either would count the same tokens twice and
// three times over, so this prices a payload copied verbatim from a recorded
// invocation rather than the tidy four-key object a test would otherwise write
// for itself.
//
// What it cannot say is that this is where the writer puts it. That is asserted
// from the writing side, by
// TestATerminalCarriesTheProvidersUsageWhereThePriceReaderLooksForIt in
// internal/backend/claudecode.
func TestStoreReadsTheProvidersUsageObjectAsItIsActuallyRecorded(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.84"
	state.ProviderSessionID = "session-developer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// run-bd535e5ee0027b61fc5b190053699e0b, sequence 439.
	recorded := json.RawMessage(`{"is_error":false,"result":"done","role":"developer","session_id":"session-developer",` +
		`"terminal_reason":"completed","total_cost_usd":9.41,"usage":{` +
		`"cache_creation":{"ephemeral_1h_input_tokens":187181,"ephemeral_5m_input_tokens":0},` +
		`"cache_creation_input_tokens":187181,"cache_read_input_tokens":7796697,"inference_geo":"not_available",` +
		`"input_tokens":114,"iterations":[{"cache_creation_input_tokens":31672,"cache_read_input_tokens":118409,` +
		`"input_tokens":7,"output_tokens":1204}],"output_tokens":58231,"service_tier":"standard"}}`)
	appendEvent(t, store, state.RunID, 1, execution.EventRunCompleted, recorded)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	tokens := price.Runs[0].Tokens
	if tokens != (TokenUsage{Priced: 1, InputTokens: 114, CacheReadTokens: 7796697, CacheCreationTokens: 187181, CacheWrite1hTokens: 187181, OutputTokens: 58231, Measured: 1}) {
		t.Fatalf("tokens = %#v, want the object's own top-level figures, the write split by lifetime, and nothing counted twice", tokens)
	}
	if price.Runs[0].CostUSD != 9.41 {
		t.Fatalf("cost = %v, want the recorded invocation's own price", price.Runs[0].CostUSD)
	}
}

// A reported cost is apportioned across what the invocation was billed for at
// the provider's rate multiples, so the parts add up to the provider's figure
// and a model's base rate never enters it. The fixture is the recorded review
// invocation the multiples were checked against: at a base rate of $5 per
// million the four weighted parts price to $0.615218, which is what the
// provider reported, so the split is exact rather than approximate.
func TestTokenUsageSplitsAReportedCostAtTheProvidersMultiples(t *testing.T) {
	t.Parallel()

	// run-f37599de6a6f36427ab9e94114f93e7c, sequence 955: a review that read its
	// six-thousand-token prefix and wrote the rest for an hour.
	usage := TokenUsage{InputTokens: 2, CacheReadTokens: 6076, CacheCreationTokens: 39517, CacheWrite1hTokens: 39517, OutputTokens: 8680, Measured: 1}
	split := usage.Split(0.615218)
	near := func(got, want float64) bool { return got > want-0.000001 && got < want+0.000001 }
	if !near(split.FreshUSD, 0.00001) || !near(split.CacheReadUSD, 0.003038) || !near(split.CacheWriteUSD, 0.39517) || !near(split.OutputUSD, 0.217) || split.UnsplitUSD != 0 {
		t.Fatalf("split = %+v, want the parts priced at 1x, 0.1x, 2x, and 5x of $5 per million", split)
	}
	if sum := split.FreshUSD + split.CacheReadUSD + split.CacheWriteUSD + split.OutputUSD; !near(sum, 0.615218) {
		t.Fatalf("the parts add up to %v, want the provider's own figure", sum)
	}

	// A write whose lifetime was not recorded is weighted as the shorter one, so
	// the same tokens written for five minutes cost a quarter over rather than
	// double, and a terminal recorded before the split existed is never charged
	// the higher premium on a guess.
	short := TokenUsage{CacheCreationTokens: 1000, Measured: 1}.Split(1.25)
	if !near(short.CacheWriteUSD, 1.25) || short.UnsplitUSD != 0 {
		t.Fatalf("an unlifetimed write splits to %+v, want all of it on the write", short)
	}
	fiveMinute := TokenUsage{InputTokens: 1000, CacheCreationTokens: 1000, CacheWrite5mTokens: 1000, Measured: 1}.Split(2.25)
	if !near(fiveMinute.FreshUSD, 1) || !near(fiveMinute.CacheWriteUSD, 1.25) {
		t.Fatalf("a five-minute write beside fresh input splits to %+v, want 1 : 1.25", fiveMinute)
	}

	// An invocation nothing measured, or one measured at nothing, still cost what
	// it cost; the money is carried whole rather than placed by guesswork.
	if unmeasured := (TokenUsage{Unreported: 1}).Split(3); unmeasured != (CostSplit{UnsplitUSD: 3}) {
		t.Fatalf("an unmeasured invocation splits to %+v, want its cost unsplit", unmeasured)
	}
	if nought := (TokenUsage{Measured: 1}).Split(3); nought != (CostSplit{UnsplitUSD: 3}) {
		t.Fatalf("an invocation that used nothing splits to %+v, want its cost unsplit", nought)
	}
}

// A terminal that reported a usage object of noughts is an invocation the
// provider measured and measured at nothing, which is a reading. A terminal that
// carried no usage object is no reading at all. They contribute the same nought
// to every total, so the counts beside them are the only thing that tells them
// apart -- and the difference decides whether a window reports a share of 0% or
// reports that it cannot answer.
func TestStoreTellsAMeasuredNoughtApartFromNoMeasurement(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.84"
	state.ProviderSessionID = "session-developer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The all-zero usage object a provider really does send: recorded on
	// run-2df9ba45fc350a285b3356e558288230 fifteen times over on 2026-08-24.
	appendUsageCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 0, usageTokens{})

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	measured := price.Runs[0].Tokens
	if measured != (TokenUsage{Priced: 1, Measured: 1}) {
		t.Fatalf("tokens = %#v, want one invocation measured at nothing", measured)
	}
	if !measured.Reported() || measured.CacheReadShare() != 0 {
		t.Fatalf("tokens = %#v, want a reading of 0%%, which is a reading", measured)
	}
	// The same figures with nothing measured behind them is the opposite fact,
	// and says so.
	if unmeasured := (TokenUsage{Unreported: 1}); unmeasured.Reported() {
		t.Fatalf("tokens = %#v, want no share where nothing was measured", unmeasured)
	}
}

// An invocation whose terminal carried no usage object is one nobody has a
// measurement for, and it is counted apart rather than added in as zero: folding
// it in would drag the share down by exactly the invocations the measure cannot
// see, which is the reading that would revert a caching change on evidence about
// something else. Every run recorded before the harness kept the provider's
// usage object is one of these.
func TestStoreCountsAnInvocationThatReportedNoUsageApartFromTheShare(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.84"
	state.ProviderSessionID = "session-developer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendUsageCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 9.0,
		usageTokens{InputTokens: 250, OutputTokens: 3000, CacheReadTokens: 750})
	appendLegacyCostEvents(t, store, state.RunID, 2, execution.EventRunCompleted, domain.RoleDeveloper, 4.0)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	tokens := price.Runs[0].Tokens
	if tokens.Measured != 1 || tokens.Unreported != 1 {
		t.Fatalf("tokens = %#v, want one invocation measured and one not", tokens)
	}
	// The share is over what was measured, and the money is over everything: the
	// unmeasured invocation is priced like any other.
	if share := tokens.CacheReadShare(); share != 0.75 {
		t.Fatalf("cache-read share = %v, want 750 of the 1000 tokens anybody measured", share)
	}
	if price.TotalUSD != 13.0 {
		t.Fatalf("total = %v, want the unmeasured invocation priced like any other", price.TotalUSD)
	}

	// A window where nothing reported usage has no share rather than a share of
	// nothing, which is what Reported is for: the two are the same float and
	// opposite facts.
	legacy := TokenUsage{Unreported: 3}
	if legacy.Reported() || legacy.CacheReadShare() != 0 {
		t.Fatalf("legacy usage = %#v, want no share to report", legacy)
	}
}

// appendUsageCostEvents records one invocation's terminal the way a backend
// records it now, carrying the provider's own usage object beside the cost. It
// is the shape every measurable run has; the two helpers below stand for the
// runs recorded before there was a role or a usage object to carry.
func appendUsageCostEvents(t *testing.T, store *Store, runID string, sequence uint64, eventType execution.EventType, role domain.AgentRole, cost float64, usage usageTokens) {
	t.Helper()
	// The keys are the provider's own, written out rather than marshalled from
	// the type that decodes them: a tag renamed on one side of that would rename
	// itself on the other and this would keep passing.
	reported := map[string]any{
		"input_tokens":                usage.InputTokens,
		"output_tokens":               usage.OutputTokens,
		"cache_read_input_tokens":     usage.CacheReadTokens,
		"cache_creation_input_tokens": usage.CacheCreationTokens,
	}
	appendEvent(t, store, runID, sequence, eventType, map[string]any{
		"role":           string(role),
		"session_id":     "session-" + string(role),
		"total_cost_usd": cost,
		"usage":          reported,
	})
}

// appendLegacyCostEvents records one invocation's terminal the way the harness
// wrote them before a terminal named the role that made it: at the schema
// version of the day, with nothing in the payload to say whose invocation it
// ended. Those runs are most of the recorded history and they are still priced,
// so what they look like is worth stating explicitly rather than producing by
// leaving a field out of a current event -- which is a different thing entirely,
// and is what the test below this one is about.
// The role is taken so the fixture can say which session the invocation ran in,
// which a log of that vintage did record and which is what the price reader
// takes a reported figure as an increment over. It is deliberately not written
// into the payload: a terminal that does not say whose invocation it was is the
// whole of what makes these logs legacy, and a helper that leaked the role into
// the record would be testing the current shape under an older name.
func appendLegacyCostEvents(t *testing.T, store *Store, runID string, sequence uint64, eventType execution.EventType, role domain.AgentRole, cost float64) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"session_id":     "session-" + string(role),
		"total_cost_usd": cost,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	event := execution.Event{
		SchemaVersion: execution.TerminalRoleSchemaVersion - 1,
		RunID:         runID,
		Sequence:      sequence,
		Timestamp:     time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		Type:          eventType,
		Source:        "claude-code",
		Payload:       payload,
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

// appendRoleCostEvents records one invocation's terminal the way a backend
// records it now: naming the role the invocation was made as beside what the
// provider said it cost. The helper above deliberately does not, because the
// runs it stands for are the ones recorded before a terminal said whose it was.
func appendRoleCostEvents(t *testing.T, store *Store, runID string, sequence uint64, eventType execution.EventType, role domain.AgentRole, cost float64) {
	t.Helper()
	appendEvent(t, store, runID, sequence, eventType, map[string]any{
		"role":           string(role),
		"session_id":     "session-" + string(role),
		"total_cost_usd": cost,
	})
}

func appendEvent(t *testing.T, store *Store, runID string, sequence uint64, eventType execution.EventType, payload any) {
	t.Helper()
	event, err := execution.NewEvent(runID, sequence, time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC), eventType, "claude-code", payload)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func mustRunID(t *testing.T) string {
	t.Helper()
	runID, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	return runID
}

// A run's repair attempts resume the developer's session, so every terminal
// after the first reports what that session has cost since the run began rather
// than what the attempt cost. Adding those figures up charged the development
// attempt to the item again on every repair after it: measured across this
// product's recorded history, repair read at $1,467 against an actual $351.
//
// The reviewer's invocations run in a session of their own and are unaffected,
// which is why a run of one attempt and one review was never overstated and why
// this had to be found in the repairs rather than in the totals.
func TestStorePricesRepairAttemptsAtWhatEachAddedToTheDevelopersSession(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.432.10"
	state.ProviderSessionID = "session-developer"
	state.ReviewSessionID = "session-reviewer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The change, a review that sent it back, the repair, and the review that
	// took it. The developer's figures are the session's running total: $6.00 for
	// the change, $10.00 by the end of the repair. The reviewer's are its own
	// session's, $0.40 and then $0.90.
	appendRoleCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 6.0)
	appendEvent(t, store, state.RunID, 2, execution.EventReviewStarted, nil)
	appendRoleCostEvents(t, store, state.RunID, 3, execution.EventRunCompleted, domain.RoleReviewer, 0.4)
	appendEvent(t, store, state.RunID, 4, execution.EventReviewCompleted, nil)
	appendRoleCostEvents(t, store, state.RunID, 5, execution.EventRunCompleted, domain.RoleDeveloper, 10.0)
	appendEvent(t, store, state.RunID, 6, execution.EventReviewStarted, nil)
	appendRoleCostEvents(t, store, state.RunID, 7, execution.EventRunCompleted, domain.RoleReviewer, 0.9)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if money(phases.Development) != (PhaseCost{CostUSD: 6.0, Invocations: 1}) {
		t.Fatalf("development = %#v, want the first attempt alone", phases.Development)
	}
	// $4.00 rather than the $10.00 the terminal reported: the repair is what the
	// developer's session moved by, not what it had cost altogether.
	if money(phases.Repair) != (PhaseCost{CostUSD: 4.0, Invocations: 1}) {
		t.Fatalf("repair = %#v, want what the repair added to the session", phases.Repair)
	}
	if money(phases.Review) != (PhaseCost{CostUSD: 0.9, Invocations: 2}) {
		t.Fatalf("review = %#v, want what the reviewer's own session moved by", phases.Review)
	}
	// The run's total is the two sessions' final totals, which is the whole claim:
	// what the run cost is what the provider last said each of its sessions had
	// cost, and never the sum of every figure it reported on the way there.
	if price.Runs[0].CostUSD != 10.9 {
		t.Fatalf("run = %v, want the developer's 10 and the reviewer's 0.9; "+
			"summing the reported figures would have made it %v", price.Runs[0].CostUSD, 17.3)
	}
	if phases.TotalUSD() != price.Runs[0].CostUSD {
		t.Fatalf("split = %v, run = %v", phases.TotalUSD(), price.Runs[0].CostUSD)
	}
}

// appendDuplicateTerminal records the anomaly a backend writes when the provider
// ends one invocation a second time, in the shape run-f3755e3f recorded it: no
// role and no session in the payload, which is what the reader has to price by
// the terminal before it.
func appendDuplicateTerminal(t *testing.T, store *Store, runID string, sequence uint64, cost float64) {
	t.Helper()
	appendEvent(t, store, runID, sequence, execution.EventProcessOutput, map[string]any{
		"provider_type":   "result",
		"anomaly":         execution.DuplicateTerminalAnomaly,
		"terminal_reason": "completed",
		"total_cost_usd":  cost,
	})
}

// run-f3755e3f's developer was ended twice by the provider four times, each time
// after a watcher woke the session for one more turn. The first terminal decided
// the invocation, and the later turn's spend was on the anomaly event alone, so
// a run whose last invocation ended that way was priced without it: $0.88 of the
// run's closing turn was on no cost surface at all. The figures below are that
// run's own, from its first development attempt, its first review, and its last
// attempt, which is the one nothing resumed afterwards.
func TestStorePricesEveryTerminalAnInvocationEndedWith(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.435.1"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendRoleCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 44.4602215)
	appendDuplicateTerminal(t, store, state.RunID, 2, 44.8557805)
	appendRoleCostEvents(t, store, state.RunID, 3, execution.EventRunCompleted, domain.RoleDeveloper, 45.694919)
	appendEvent(t, store, state.RunID, 4, execution.EventReviewStarted, nil)
	appendRoleCostEvents(t, store, state.RunID, 5, execution.EventRunCompleted, domain.RoleReviewer, 0.78572375)
	appendEvent(t, store, state.RunID, 6, execution.EventReviewCompleted, nil)
	appendRoleCostEvents(t, store, state.RunID, 7, execution.EventRunCompleted, domain.RoleDeveloper, 264.6636765)
	appendDuplicateTerminal(t, store, state.RunID, 8, 265.540555)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	run := price.Runs[0]
	// Every ending's increment, each priced against the figure before it in the
	// developer's session: 44.46, then 0.40, 0.84, 218.97, and 0.88. The sum is
	// the session's last reported total, plus the reviewer's own session.
	want := 265.540555 + 0.78572375
	if !nearCost(run.CostUSD, want) {
		t.Fatalf("run = %v, want %v; the first terminal alone would have made it %v",
			run.CostUSD, want, 264.6636765+0.78572375)
	}
	if !nearCost(run.Phases.TotalUSD(), run.CostUSD) {
		t.Fatalf("split = %v, run = %v", run.Phases.TotalUSD(), run.CostUSD)
	}
	// A duplicate is charged to the invocation it followed, and is not one of its
	// own: the development attempt carries its duplicate's $0.40, and the phase
	// split still counts three developer invocations and one review.
	if !nearCost(run.Phases.Development.CostUSD, 44.8557805) || run.Phases.Development.Invocations != 1 {
		t.Fatalf("development = %#v, want the first terminal and its duplicate as one invocation", run.Phases.Development)
	}
	if run.Phases.Repair.Invocations != 2 || run.Phases.Review.Invocations != 1 {
		t.Fatalf("phases = %#v, want the duplicates to add no invocation", run.Phases)
	}
}

// A duplicate that names its own session, as the backend records one now, is
// priced against that session.
func TestStorePricesADuplicateTerminalAgainstTheSessionItNames(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.435.2"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendRoleCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, domain.RoleDeveloper, 6.0)
	appendEvent(t, store, state.RunID, 2, execution.EventProcessOutput, map[string]any{
		"anomaly":        execution.DuplicateTerminalAnomaly,
		"role":           string(domain.RoleDeveloper),
		"session_id":     "session-" + string(domain.RoleDeveloper),
		"total_cost_usd": 6.5,
	})

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if run := price.Runs[0]; !nearCost(run.CostUSD, 6.5) || run.Phases.Development.Invocations != 1 {
		t.Fatalf("run = %v with development %#v, want 6.5 from one invocation", run.CostUSD, run.Phases.Development)
	}
}

func nearCost(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}
