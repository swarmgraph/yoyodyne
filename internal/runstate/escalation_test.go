package runstate

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const escalationDocketKey = "stopped_run:run-0123456789abcdef0123456789abcdef"

func newEscalationStore(t *testing.T) *EscalationStore {
	t.Helper()
	store, err := NewEscalationStore(t.TempDir(), domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewEscalationStore() error = %v", err)
	}
	return store
}

var firstAttemptAt = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

func attemptedEscalation() Escalation {
	return attemptedAt(firstAttemptAt)
}

// attemptedAt is the same attempt made at a given moment. The moment matters
// because an attempt made too soon after the last one is refused, so a test
// making several has to say when each of them was.
func attemptedAt(at time.Time) Escalation {
	return Escalation{
		DocketKey:        escalationDocketKey,
		RunID:            "run-0123456789abcdef0123456789abcdef",
		WorkItemID:       "yoyodyne-ifd.209.16",
		FirstAttemptedAt: at,
	}
}

func delivered() Delivery {
	return Delivery{
		At:             time.Date(2026, 9, 1, 9, 1, 0, 0, time.UTC),
		ConversationID: "chat-91253e0e070c17b0663651cc48602122",
		Decision:       "repair",
		Reason:         "the findings are narrow and the change is preserved, so one more bounded attempt is worth it",
	}
}

// The bound this record exists for. One docketed stoppage is put in front of the
// development manager once, so the same evidence is never in front of her twice
// under two decisions.
func TestADeliveredStoppageIsNotDeliveredAgain(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if _, err := store.Settle(context.Background(), escalationDocketKey, delivered()); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	_, err := store.Attempt(context.Background(), attemptedEscalation())
	if !errors.Is(err, ErrEscalationSpent) {
		t.Fatalf("second Attempt() error = %v, want the delivery refused", err)
	}
	var spent EscalationSpentError
	if !errors.As(err, &spent) || !spent.Existing.Delivered() {
		t.Fatalf("refusal = %#v, want it to carry the delivery that already happened", err)
	}
}

// The bound is per stoppage rather than per key. A key names a run, a repair
// continues the run that stopped, and a repaired run that dies again is docketed
// under the key its settled entry carried — so a record keyed alone would refuse
// the fresh stoppage as a delivery already made. What the record is about is the
// stoppage docketed at a moment, and a later one starts the delivery over.
func TestAStoppageDocketedAfterTheDeliveryIsDeliveredAfresh(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	docketed := firstAttemptAt.Add(-time.Hour)
	first := attemptedEscalation()
	first.DocketedAt = docketed
	if _, err := store.Attempt(context.Background(), first); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if _, err := store.Settle(context.Background(), escalationDocketKey, delivered()); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	// The same stoppage, asked about again, is still refused.
	if _, err := store.Attempt(context.Background(), first); !errors.Is(err, ErrEscalationSpent) {
		t.Fatalf("Attempt() of the same stoppage error = %v, want it refused", err)
	}
	// A record written before the stoppage was kept on it says nothing about
	// when, and is read as the same stoppage rather than reset by every entry
	// that carries a moment.
	unknown := attemptedEscalation()
	if _, err := store.Attempt(context.Background(), unknown); !errors.Is(err, ErrEscalationSpent) {
		t.Fatalf("Attempt() naming no stoppage error = %v, want the standing record kept", err)
	}

	// The run stopped again after that delivery was decided.
	again := attemptedAt(firstAttemptAt.Add(2 * time.Hour))
	again.DocketedAt = firstAttemptAt.Add(time.Hour)
	fresh, err := store.Attempt(context.Background(), again)
	if err != nil {
		t.Fatalf("Attempt() of the later stoppage error = %v, want a fresh delivery", err)
	}
	if fresh.Attempts != 1 || fresh.Delivered() || fresh.Decision != "" || !fresh.DocketedAt.Equal(again.DocketedAt) {
		t.Fatalf("record = %#v, want the delivery started over for the later stoppage", fresh)
	}
	if !fresh.FirstAttemptedAt.Equal(again.FirstAttemptedAt) {
		t.Fatalf("first attempted at = %s, want the later stoppage's own first attempt", fresh.FirstAttemptedAt)
	}
	// One record per key, and it is now about the later stoppage.
	listed, err := store.List()
	if err != nil || len(listed) != 1 || !listed[0].DocketedAt.Equal(again.DocketedAt) {
		t.Fatalf("List() = %#v, %v, want one record about the later stoppage", listed, err)
	}
}

// A delivery that failed is worth making again, and not forever. The bound is
// what separates riding out a provider that was briefly unreachable from
// spending every pass on a conversation nothing can open.
func TestDeliveriesThatFailAreBounded(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	at := firstAttemptAt
	for attempt := 1; attempt <= MaxEscalationAttempts; attempt++ {
		recorded, err := store.Attempt(context.Background(), attemptedAt(at))
		if err != nil {
			t.Fatalf("attempt %d: Attempt() error = %v", attempt, err)
		}
		if recorded.Attempts != attempt {
			t.Fatalf("attempt %d recorded %d attempt(s)", attempt, recorded.Attempts)
		}
		if _, err := store.Settle(context.Background(), escalationDocketKey, Delivery{Problem: "the conversation could not be held"}); err != nil {
			t.Fatalf("attempt %d: Settle() error = %v", attempt, err)
		}
		at = at.Add(EscalationRetryDelay)
	}
	_, err := store.Attempt(context.Background(), attemptedAt(at))
	if !errors.Is(err, ErrEscalationSpent) {
		t.Fatalf("Attempt() past the bound error = %v, want it refused", err)
	}
	if !strings.Contains(err.Error(), "needs a person") {
		t.Fatalf("refusal = %q, want it to say a stoppage nobody could reach her about needs a person", err)
	}
}

// Two sessions polling one docket at the same instant are one attempt and the
// rest refused, because the pacing is decided where the lock is rather than by
// whoever was about to make the attempt. Deciding it outside would be a read and
// a write with a window between them, which is the window both of them land in.
func TestConcurrentAttemptsAtOneStoppageLeaveOne(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	var group sync.WaitGroup
	attempts := make([]int, 6)
	failures := make([]error, len(attempts))
	for index := range attempts {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			recorded, err := store.Attempt(context.Background(), attemptedEscalation())
			attempts[index] = recorded.Attempts
			failures[index] = err
		}(index)
	}
	group.Wait()
	taken := 0
	for index, err := range failures {
		switch {
		case err == nil:
			taken++
			if attempts[index] != 1 {
				t.Fatalf("attempt recorded as number %d, want the one attempt this moment permits", attempts[index])
			}
		case errors.Is(err, ErrEscalationCooling):
		default:
			t.Fatalf("Attempt() error = %v, want either the attempt or the pacing refusal", err)
		}
	}
	if taken != 1 {
		t.Fatalf("attempts taken = %d, want exactly one turn taken between them", taken)
	}
}

// The refusal says which stoppage and when the next attempt is due, so a caller
// that meets it can tell it from every other reason a delivery does not happen.
func TestAnAttemptTooSoonIsRefusedUnderTheLock(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	_, err := store.Attempt(context.Background(), attemptedAt(firstAttemptAt.Add(EscalationRetryDelay-time.Second)))
	if !errors.Is(err, ErrEscalationCooling) {
		t.Fatalf("Attempt() error = %v, want the attempt refused as too soon", err)
	}
	var cooling EscalationCoolingError
	if !errors.As(err, &cooling) || cooling.Existing.Attempts != 1 {
		t.Fatalf("refusal = %#v, want it to carry the attempt that is still cooling", err)
	}
	if !strings.Contains(err.Error(), "not due until 2026-09-01T09:15:00Z") {
		t.Fatalf("refusal = %q, want it to say when the next attempt is due", err)
	}
	// And the moment it is due, it is taken.
	recorded, err := store.Attempt(context.Background(), attemptedAt(firstAttemptAt.Add(EscalationRetryDelay)))
	if err != nil {
		t.Fatalf("Attempt() when it was due error = %v", err)
	}
	if recorded.Attempts != 2 {
		t.Fatalf("attempts = %d, want the second attempt taken once its delay had passed", recorded.Attempts)
	}
}

// An attempt whose turn provably never happened is given back, so the stoppage
// keeps every delivery it is owed — and what is given back is the attempt rather
// than the pacing, because the failures this is for last minutes or hours and
// asking again at once would meet the same refusal.
func TestAnUndeliveredAttemptIsWithdrawn(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if err := store.Withdraw(context.Background(), escalationDocketKey, "the conversation is already held"); err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	given, found, err := store.Find(escalationDocketKey)
	if err != nil || !found {
		t.Fatalf("Find() = found %v, error %v, want the record kept to pace the next attempt", found, err)
	}
	if given.Attempts != 0 || given.Spent() {
		t.Fatalf("recorded = %#v, want the attempt given back and the stoppage still deliverable", given)
	}
	if !given.Cooling(given.LastAttemptedAt.Add(EscalationRetryDelay-time.Second)) ||
		given.Cooling(given.LastAttemptedAt.Add(EscalationRetryDelay)) {
		t.Fatalf("recorded = %#v, want the next attempt paced exactly as a failed one is", given)
	}
	if !strings.Contains(given.Problem, "already held") {
		t.Fatalf("problem = %q, want what stopped the delivery kept on the record", given.Problem)
	}
	// And the stoppage may be delivered again once the delay has passed, which is
	// the whole point of giving the attempt back.
	recorded, err := store.Attempt(context.Background(), attemptedAt(firstAttemptAt.Add(EscalationRetryDelay)))
	if err != nil {
		t.Fatalf("Attempt() after a withdrawal error = %v", err)
	}
	if recorded.Attempts != 1 {
		t.Fatalf("attempts after a withdrawal = %d, want the stoppage to start again at 1", recorded.Attempts)
	}
}

// What was said to her is not something to un-say. A delivered escalation is
// refused rather than removed, because a second delivery of it is the thing this
// record exists to prevent.
func TestADeliveredEscalationIsNotWithdrawn(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if _, err := store.Settle(context.Background(), escalationDocketKey, delivered()); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	err := store.Withdraw(context.Background(), escalationDocketKey, "")
	if err == nil || !strings.Contains(err.Error(), "delivered to the development manager") {
		t.Fatalf("Withdraw() error = %v, want a delivered escalation refused", err)
	}
}

// What she decided outlives the process that delivered the stoppage, which is
// what makes this a record rather than a log line.
func TestWhatSheDecidedIsReadBack(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if _, err := store.Settle(context.Background(), escalationDocketKey, delivered()); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	recorded, found, err := store.Find(escalationDocketKey)
	if err != nil || !found {
		t.Fatalf("Find() = found %v, error %v", found, err)
	}
	if recorded.Decision != "repair" || recorded.ConversationID != "chat-91253e0e070c17b0663651cc48602122" {
		t.Fatalf("recorded = %#v, want her decision and the conversation it was made in", recorded)
	}
	if !recorded.Delivered() || !recorded.Spent() {
		t.Fatalf("recorded = %#v, want a delivered stoppage nothing delivers again", recorded)
	}
	listed, err := store.List()
	if err != nil || len(listed) != 1 || listed[0].DocketKey != escalationDocketKey {
		t.Fatalf("List() = %#v, error %v, want the one escalation", listed, err)
	}
}

// A delivery she answered without deciding anything is an answer. What must
// never be recordable is the opposite: a decision on a stoppage nobody ever put
// to her.
func TestADecisionCannotBeRecordedOnAnUndeliveredStoppage(t *testing.T) {
	t.Parallel()

	escalation := attemptedEscalation()
	escalation.SchemaVersion = EscalationSchemaVersion
	escalation.ProductID = domain.ProductID("yoyodyne")
	escalation.Attempts = 1
	escalation.UpdatedAt = escalation.FirstAttemptedAt
	escalation.Decision = "repair"
	err := escalation.Validate()
	if err == nil || !strings.Contains(err.Error(), "never delivered") {
		t.Fatalf("Validate() error = %v, want a decision on an undelivered stoppage refused", err)
	}
}

// A record nobody delivered says so, because a stoppage that reached nobody is
// exactly what somebody has to be able to find.
func TestAnUndeliveredEscalationSaysSo(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	recorded, err := store.Settle(context.Background(), escalationDocketKey, Delivery{Problem: "Claude Code is not authenticated"})
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if recorded.Delivered() {
		t.Fatalf("recorded = %#v, want a stoppage that reached nobody", recorded)
	}
	rendered := recorded.Render()
	if !strings.Contains(rendered, "has not reached the development manager") || !strings.Contains(rendered, "not authenticated") {
		t.Fatalf("Render() = %q, want it to say the stoppage reached nobody and why", rendered)
	}
}

// The record belongs to one product, like every other record beside it: one read
// from another product's store is refused rather than answered.
func TestAnEscalationOfAnotherProductIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewEscalationStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewEscalationStore() error = %v", err)
	}
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	other := &EscalationStore{root: store.Root(), state: store.state, productID: domain.ProductID("elsewhere")}
	if _, _, err := other.Find(escalationDocketKey); err == nil || !strings.Contains(err.Error(), "belongs to product") {
		t.Fatalf("Find() error = %v, want another product's record refused", err)
	}
}

// The retry delay is measured from the attempt rather than from the write, so
// pacing is read against the clock that makes the deliveries rather than the
// store's own.
func TestAnAttemptIsPacedFromWhenItWasMade(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	attempted, err := store.Attempt(context.Background(), attemptedEscalation())
	if err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if !attempted.LastAttemptedAt.Equal(attemptedEscalation().FirstAttemptedAt) {
		t.Fatalf("last attempted at = %v, want the moment the caller said it attempted", attempted.LastAttemptedAt)
	}
	if _, err := store.Settle(context.Background(), escalationDocketKey, Delivery{Problem: "the provider refused the turn"}); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	settled, _, err := store.Find(escalationDocketKey)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	// Settling stamps the record, and leaves what the pacing is read against
	// alone: the two are different clocks and a delay measured across them would
	// be nonsense in whichever direction they disagree.
	if !settled.LastAttemptedAt.Equal(attempted.LastAttemptedAt) {
		t.Fatalf("last attempted at after settling = %v, want the attempt's own moment", settled.LastAttemptedAt)
	}
	at := attempted.LastAttemptedAt
	if !settled.Cooling(at.Add(EscalationRetryDelay - time.Second)) {
		t.Fatal("an attempt made a moment ago is not cooling, so the bound would be spent in a burst")
	}
	if settled.Cooling(at.Add(EscalationRetryDelay)) {
		t.Fatal("an attempt is still cooling once its delay has passed, so it would never be made again")
	}
	// And the next attempt moves it, so a delivery that failed twice waits twice
	// rather than being abandoned on a clock that started before anybody knew
	// there was a problem.
	again := attemptedEscalation()
	again.FirstAttemptedAt = at.Add(EscalationRetryDelay)
	second, err := store.Attempt(context.Background(), again)
	if err != nil {
		t.Fatalf("second Attempt() error = %v", err)
	}
	if !second.LastAttemptedAt.Equal(again.FirstAttemptedAt) || !second.FirstAttemptedAt.Equal(at) {
		t.Fatalf("second attempt = %#v, want the latest attempt moved and the first left where it was", second)
	}
}

func TestAPermanentRefusalIsANewDeliveryEvenAfterAnUndatedLegacyDelivery(t *testing.T) {
	t.Parallel()
	store := newEscalationStore(t)
	ctx := context.Background()
	if _, err := store.Attempt(ctx, attemptedEscalation()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Settle(ctx, escalationDocketKey, delivered()); err != nil {
		t.Fatal(err)
	}
	refusal := attemptedAt(firstAttemptAt.Add(2 * time.Hour))
	refusal.CarryOutRefusedAt = firstAttemptAt.Add(time.Hour)
	refusal.DocketedAt = refusal.CarryOutRefusedAt
	fresh, err := store.Attempt(ctx, refusal)
	if err != nil || fresh.Attempts != 1 || fresh.Delivered() {
		t.Fatalf("permanent refusal = %+v, %v; want a new delivery", fresh, err)
	}
	deliveredRefusal := delivered()
	deliveredRefusal.At = refusal.FirstAttemptedAt.Add(time.Minute)
	if _, err := store.Settle(ctx, escalationDocketKey, deliveredRefusal); err != nil {
		t.Fatal(err)
	}
	refusal.FirstAttemptedAt = firstAttemptAt.Add(24 * time.Hour)
	if _, err := store.Attempt(ctx, refusal); !errors.Is(err, ErrEscalationSpent) {
		t.Fatalf("duplicate refusal delivery = %v; want it refused", err)
	}
}
