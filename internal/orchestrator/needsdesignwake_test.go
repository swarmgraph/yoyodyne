package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// labelledItems is the tracker's needs-design items as a test sets them.
type labelledItems struct {
	items []NeedsDesignItem
	err   error
}

func (l *labelledItems) Labelled(context.Context) ([]NeedsDesignItem, error) {
	return append([]NeedsDesignItem(nil), l.items...), l.err
}

func (l *labelledItems) label(id, title string) {
	l.items = append(l.items, NeedsDesignItem{ID: id, Title: title})
}

func (l *labelledItems) unlabel(id string) {
	var kept []NeedsDesignItem
	for _, item := range l.items {
		if item.ID != id {
			kept = append(kept, item)
		}
	}
	l.items = kept
}

// architectWake is the trigger with the needs-design wake wired and nothing
// else, over a sweep store and a wake record in one state root.
type architectWake struct {
	trigger Trigger
	sweeps  *runstate.SweepStore
	wakes   *runstate.NeedsDesignWakeStore
	items   *labelledItems
	role    *wokenRole
	clock   *movingRecurringClock
}

func newArchitectWake(t *testing.T) *architectWake {
	t.Helper()
	root := t.TempDir()
	sweeps, err := runstate.NewSweepStore(root, "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	wakes, err := runstate.NewNeedsDesignWakeStore(root, "example")
	if err != nil {
		t.Fatalf("NewNeedsDesignWakeStore() error = %v", err)
	}
	w := &architectWake{sweeps: sweeps, wakes: wakes, items: &labelledItems{}, role: &wokenRole{}, clock: &movingRecurringClock{now: recurringNow}}
	w.trigger = Trigger{
		Claims:           sweeps,
		Reports:          sweeps,
		Roles:            w.role,
		NeedsDesign:      w.items,
		NeedsDesignWakes: wakes,
		Clock:            w.clock,
	}
	return w
}

// fire takes one pull at the moment given, with the architect answering each
// turn with a complete account.
func (w *architectWake) fire(t *testing.T, at time.Time) RecurringSweep {
	t.Helper()
	w.clock.now = at
	w.role.answers = append(w.role.answers, scriptedTurn{result: complete("looked at the labelled work")})
	fired, err := w.trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() at %s error = %v", at.Format(time.RFC3339), err)
	}
	w.role.answers = nil
	return fired
}

func (w *architectWake) lastMessage(t *testing.T) string {
	t.Helper()
	if len(w.role.messages) == 0 {
		t.Fatal("the architect was never woken")
	}
	return w.role.messages[len(w.role.messages)-1]
}

// Adding the label wakes the architect's conversation at the next pull, with
// the item named, and the pass is recorded under the wake's own name as one its
// event woke.
func TestAddingTheNeedsDesignLabelWakesTheArchitectWithTheItemNamed(t *testing.T) {
	t.Parallel()

	w := newArchitectWake(t)
	if fired := w.fire(t, recurringNow); len(fired.Fired) != 0 {
		t.Fatalf("fired = %+v, want nothing while no item carries the label", fired.Fired)
	}
	w.items.label("yoyodyne-ifd.501", "Choose the record's owner")

	fired := w.fire(t, recurringNow.Add(time.Minute))
	if len(fired.Fired) != 1 || fired.Fired[0].Task != NeedsDesignWakeTask || fired.Fired[0].Role != domain.RoleArchitect {
		t.Fatalf("fired = %+v, want one pass of the architect's under %s", fired.Fired, NeedsDesignWakeTask)
	}
	message := w.lastMessage(t)
	for _, want := range []string{"the needs-design label was added", "Newly labelled (1):", "- yoyodyne-ifd.501: Choose the record's owner", "docs/designs/needs-design-gate.md"} {
		if !strings.Contains(message, want) {
			t.Errorf("message lacks %q:\n%s", want, message)
		}
	}
	if w.role.agents[0] != "" {
		t.Errorf("woke agent %q, want the architect's role conversation", w.role.agents[0])
	}
	claim, found, err := w.sweeps.Find(NeedsDesignWakeTask)
	if err != nil || !found || !claim.Summoned {
		t.Errorf("Find() = %+v, %v, %v, want the wake claimed out of any schedule", claim, found, err)
	}
	record, _, err := w.wakes.Load()
	if err != nil || !record.Has("yoyodyne-ifd.501") {
		t.Errorf("record = %+v, %v, want the item recorded as woken for once the pass completed", record, err)
	}
}

// An item labelled twice — by a second role, or by the same role again — wakes
// the architect once; one that loses the label and is labelled again later
// wakes her again.
func TestAnItemLabelledTwiceWakesTheArchitectOnce(t *testing.T) {
	t.Parallel()

	w := newArchitectWake(t)
	w.items.label("yoyodyne-ifd.501", "first labelling")
	if fired := w.fire(t, recurringNow); len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the first labelling to wake her", fired.Fired)
	}
	// Labelled a second time: the tracker holds the label once, whoever added
	// it again.
	w.items.unlabel("yoyodyne-ifd.501")
	w.items.label("yoyodyne-ifd.501", "second labelling, read at the same pull")
	for _, at := range []time.Duration{10 * time.Minute, time.Hour, 6 * time.Hour} {
		if fired := w.fire(t, recurringNow.Add(at)); len(fired.Fired) != 0 {
			t.Fatalf("fired at +%s = %+v, want no second wake for the same item", at, fired.Fired)
		}
	}
	if len(w.role.messages) != 1 {
		t.Fatalf("woken %d times, want once", len(w.role.messages))
	}

	w.items.unlabel("yoyodyne-ifd.501")
	if fired := w.fire(t, recurringNow.Add(7*time.Hour)); len(fired.Fired) != 0 {
		t.Fatalf("fired = %+v, want nothing once the label is off", fired.Fired)
	}
	w.items.label("yoyodyne-ifd.501", "labelled again later")
	if fired := w.fire(t, recurringNow.Add(8*time.Hour)); len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the item labelled again to wake her again", fired.Fired)
	}
}

// A second item labelled while the first is still waiting wakes her for the
// second, naming the first as still carrying the label.
func TestASecondLabelledItemWakesTheArchitectForItAlone(t *testing.T) {
	t.Parallel()

	w := newArchitectWake(t)
	w.items.label("yoyodyne-ifd.501", "the first")
	w.fire(t, recurringNow)
	w.items.label("yoyodyne-ifd.502", "the second")
	if fired := w.fire(t, recurringNow.Add(time.Minute)); len(fired.Fired) != 0 {
		t.Fatalf("fired = %+v, want no wake sooner than the recurring minimum after the last", fired.Fired)
	}
	if fired := w.fire(t, recurringNow.Add(6*time.Minute)); len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the second item to wake her", fired.Fired)
	}
	message := w.lastMessage(t)
	newly := strings.Index(message, "Newly labelled (1):\n- yoyodyne-ifd.502")
	before := strings.Index(message, "Still carrying the label from before (1):\n- yoyodyne-ifd.501")
	if newly < 0 || before < 0 {
		t.Fatalf("message = %s, want the second item as new and the first as still waiting", message)
	}
}

// Once a day since her last pass of this kind, every item still carrying the
// label is put in front of her, which catches a wake that was lost: here the
// item is recorded as woken for although no pass ever named it to her.
func TestTheDailyPassPicksUpALabelledItemAWakeMissed(t *testing.T) {
	t.Parallel()

	w := newArchitectWake(t)
	w.items.label("yoyodyne-ifd.501", "the lost wake")
	if _, err := w.wakes.Record(context.Background(), []string{"yoyodyne-ifd.501"}, []string{"yoyodyne-ifd.501"}, recurringNow); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if _, err := w.sweeps.Summon(context.Background(), NeedsDesignWakeTask, recurringNow); err != nil {
		t.Fatalf("Summon() error = %v", err)
	}
	for _, at := range []time.Duration{time.Minute, time.Hour, 23 * time.Hour} {
		if fired := w.fire(t, recurringNow.Add(at)); len(fired.Fired) != 0 {
			t.Fatalf("fired at +%s = %+v, want nothing before the day is out", at, fired.Fired)
		}
	}
	fired := w.fire(t, recurringNow.Add(NeedsDesignBackstop))
	if len(fired.Fired) != 1 || fired.Fired[0].Task != NeedsDesignWakeTask {
		t.Fatalf("fired = %+v, want the daily pass", fired.Fired)
	}
	message := w.lastMessage(t)
	if !strings.Contains(message, "daily pass over the work items carrying the needs-design label") ||
		!strings.Contains(message, "Still carrying the label from before (1):\n- yoyodyne-ifd.501") {
		t.Fatalf("message = %s, want the daily pass naming the item", message)
	}
	claim, _, err := w.sweeps.Find(NeedsDesignWakeTask)
	if err != nil || claim.Summoned || claim.Every.String() != NeedsDesignBackstop.String() {
		t.Errorf("claim = %+v, %v, want the daily pass claimed on its cadence", claim, err)
	}
}

// Nothing wakes her on a schedule while no item carries the label, however
// long it has been.
func TestNoLabelledItemMeansNoPassEvenWhenTheDayIsOut(t *testing.T) {
	t.Parallel()

	w := newArchitectWake(t)
	for _, at := range []time.Duration{0, NeedsDesignBackstop, 3 * NeedsDesignBackstop} {
		if fired := w.fire(t, recurringNow.Add(at)); len(fired.Fired) != 0 {
			t.Fatalf("fired at +%s = %+v, want nothing", at, fired.Fired)
		}
	}
	if _, found, err := w.sweeps.Find(NeedsDesignWakeTask); err != nil || found {
		t.Fatalf("Find() = %v, %v, want nothing ever claimed", found, err)
	}
}

// A wake whose pass did not complete leaves the item unwoken, so the next pull
// past the recurring minimum wakes her for it again rather than losing it.
func TestAWakeThatFailedIsTakenAgain(t *testing.T) {
	t.Parallel()

	w := newArchitectWake(t)
	w.items.label("yoyodyne-ifd.501", "a turn that fails")
	w.role.failure = errors.New("the provider failed")
	w.fire(t, recurringNow)
	if record, _, err := w.wakes.Load(); err != nil || record.Has("yoyodyne-ifd.501") {
		t.Fatalf("record = %+v, %v, want the item still unwoken after a failed pass", record, err)
	}
	w.role.failure = nil
	if fired := w.fire(t, recurringNow.Add(6*time.Minute)); len(fired.Fired) != 1 || !strings.Contains(w.lastMessage(t), "yoyodyne-ifd.501") {
		t.Fatalf("fired = %+v, want the wake taken again", fired.Fired)
	}
}

// A pass of the architect's already in flight in this session keeps the wake
// waiting rather than refusing it, and the next pull takes it.
func TestTheWakeWaitsForATurnInFlightInTheArchitectsConversation(t *testing.T) {
	t.Parallel()

	w := newArchitectWake(t)
	w.items.label("yoyodyne-ifd.501", "labelled while she is busy")
	start, err := w.trigger.Start(context.Background(), map[string]bool{roleConversationKey(domain.RoleArchitect): true}, MaxConcurrentFirings)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(start.Started) != 0 {
		t.Fatalf("started = %+v, want nothing into a conversation already taking a turn", start.Started)
	}
	if _, found, _ := w.sweeps.Find(NeedsDesignWakeTask); found {
		t.Fatal("the wake was claimed while her conversation was busy, want it left for the next pull")
	}
	if fired := w.fire(t, recurringNow.Add(time.Minute)); len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the wake taken once her conversation is free", fired.Fired)
	}
}

// A reading of the label that fails wakes nothing and is said beside the pull.
func TestAnUnreadableLabelWakesNothing(t *testing.T) {
	t.Parallel()

	w := newArchitectWake(t)
	w.items.err = errors.New("the export could not be read")
	w.clock.now = recurringNow
	fired, err := w.trigger.Fire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "read which work items carry the needs-design label") {
		t.Fatalf("Fire() error = %v, want the reading's failure said", err)
	}
	if len(fired.Fired) != 0 {
		t.Fatalf("fired = %+v, want nothing", fired.Fired)
	}
}
