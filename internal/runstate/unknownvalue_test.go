package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordFromALaterBuild writes a stopped run and then rewrites its record on
// disk the way a newer build would have written it: a stop class and a landing
// outcome this build does not know, and, where asked, a field it has never heard
// of.
func recordFromALaterBuild(t *testing.T, store *Store) State {
	return recordFromALaterBuildWith(t, store, true)
}

func recordFromALaterBuildWith(t *testing.T, store *Store, newField bool) State {
	t.Helper()

	state := testState(t, StatusFailed)
	state.StopClass = StopRelaunchBudget
	state.LandingOutcome = LandingEvidence
	state.LandingReason = "the work is waiting on another item"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path := filepath.Join(store.root, state.RunID+".json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var carried map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &carried); err != nil {
		t.Fatal(err)
	}
	carried["stop_class"] = json.RawMessage(`"a-cause-from-a-later-build"`)
	carried["landing_outcome"] = json.RawMessage(`"an-outcome-from-a-later-build"`)
	if newField {
		carried["a_field_from_a_later_build"] = json.RawMessage(`{"held": true}`)
	}
	rewritten, err := json.Marshal(carried)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	return state
}

// A reading for showing keeps a record a newer build wrote: the values this
// build does not know stay as written and are listed as unknown, the field it
// has never heard of is stepped over, and the run is in every listing rather
// than the listing failing for it.
func TestARecordWithUnknownValuesIsReadAndListedWithThemMarkedUnknown(t *testing.T) {
	said := captureUnknownFieldNotices(t)

	store := newTestStore(t)
	state := recordFromALaterBuild(t, store)

	read, err := store.Read(state.RunID)
	if err != nil {
		t.Fatalf("Read() error = %v, want the record kept", err)
	}
	if read.StopClass != "a-cause-from-a-later-build" || read.LandingOutcome != "an-outcome-from-a-later-build" {
		t.Errorf("Read() = stop class %q, landing outcome %q, want both kept as written", read.StopClass, read.LandingOutcome)
	}
	want := map[string]string{"stop_class": "a-cause-from-a-later-build", "landing_outcome": "an-outcome-from-a-later-build"}
	if len(read.UnknownValues) != len(want) {
		t.Fatalf("Read().UnknownValues = %+v, want %v", read.UnknownValues, want)
	}
	for _, unknown := range read.UnknownValues {
		if want[unknown.Field] != unknown.Value {
			t.Errorf("Read().UnknownValues names %s %q, want %v", unknown.Field, unknown.Value, want)
		}
	}

	recorded, err := store.Recorded()
	if err != nil || len(recorded) != 1 || recorded[0].RunID != state.RunID {
		t.Fatalf("Recorded() = %d runs, %v, want the run listed", len(recorded), err)
	}
	runs, err := store.Runs(state.WorkItemID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("Runs() = %d runs, %v, want the run listed", len(runs), err)
	}

	for _, notice := range []string{`stop_class "a-cause-from-a-later-build"`, `landing_outcome "an-outcome-from-a-later-build"`, "a_field_from_a_later_build"} {
		if !strings.Contains(said.String(), notice) {
			t.Errorf("notices = %q, want %q named", said.String(), notice)
		}
	}
}

// What a surface prints for such a run says the value is unknown and what was
// recorded, in words, rather than presenting it as a class this build could
// explain or leaving the run out.
func TestAValueThisBuildDoesNotKnowIsShownAsUnknownWithTheRawValue(t *testing.T) {
	captureUnknownFieldNotices(t)

	store := newTestStore(t)
	state := recordFromALaterBuild(t, store)
	read, err := store.Read(state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	summary := store.summarize(read)
	if len(summary.UnknownValues) != 2 {
		t.Errorf("summary.UnknownValues = %+v, want both values carried to the surfaces", summary.UnknownValues)
	}
	shown := `stop cause unknown to this build (recorded as "a-cause-from-a-later-build")`
	if got := StopCauseShown(summary.StopClass); got != shown {
		t.Errorf("StopCauseShown() = %q, want %q", got, shown)
	}
	if got := summary.Reason(); !strings.HasPrefix(got, shown) {
		t.Errorf("Reason() = %q, want it to lead with %q", got, shown)
	}
	if got := StopSays(summary.StopClass); !strings.Contains(got, shown) {
		t.Errorf("StopSays() = %q, want %q in it", got, shown)
	}
	// A class this build knows is printed exactly as it always was.
	if got := StopCauseShown(StopRelaunchBudget); got != string(StopRelaunchBudget) {
		t.Errorf("StopCauseShown(%q) = %q, want the class itself", StopRelaunchBudget, got)
	}
}

// A reading that precedes acting on the run still refuses it, naming the value,
// and so does every write: the same copy the listing read is never saved back,
// and a build never writes a value it does not know.
func TestAnUnknownValueIsRefusedByEveryReadingThatActsAndEveryWrite(t *testing.T) {
	captureUnknownFieldNotices(t)

	store := newTestStore(t)
	// A field this build does not know is refused by the strict door already
	// (tolerantread_test.go), so this record carries only the unknown values, and
	// the refusal is the one that names them.
	state := recordFromALaterBuildWith(t, store, false)

	if _, err := store.Load(state.RunID); err == nil || !strings.Contains(err.Error(), "a-cause-from-a-later-build") {
		t.Errorf("Load() error = %v, want the unknown value named", err)
	}
	// Resuming takes the run's lease and reads it to act on it, which is refused
	// with the value named, and leaves the lease free for the development manager.
	if _, lease, err := store.AdoptRun(context.Background(), state.RunID); err == nil {
		lease.Release()
		t.Error("AdoptRun() resumed a run holding a value this build does not know")
	} else {
		var unknown UnknownValueError
		if !errors.As(err, &unknown) || !strings.Contains(err.Error(), unknown.Value) {
			t.Errorf("AdoptRun() error = %v, want the unknown value named", err)
		}
	}

	read, err := store.Read(state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(read); err == nil {
		t.Error("Save() wrote back a record read holding values this build does not know")
	}
	read.UnknownValues = nil
	if err := store.Save(read); err == nil || !strings.Contains(err.Error(), "is not a value this build knows") {
		t.Errorf("Save() error = %v, want the unknown value refused by validation", err)
	}

	fresh := testState(t, StatusFailed)
	fresh.StopClass = "a-cause-from-a-later-build"
	err = store.Create(fresh)
	var refused RefusedStateError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), `stop_class is invalid: "a-cause-from-a-later-build"`) {
		t.Errorf("Create() error = %v, want the unknown stop class refused", err)
	}
}

// A record that is wrong in some other way as well is still refused by the
// listing: only a value this build does not know is explained by another build
// having written the record.
func TestARecordWithAnUnknownValueAndAnotherProblemIsStillRefused(t *testing.T) {
	captureUnknownFieldNotices(t)

	store := newTestStore(t)
	state := recordFromALaterBuild(t, store)
	path := filepath.Join(store.root, state.RunID+".json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var carried map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &carried); err != nil {
		t.Fatal(err)
	}
	carried["landing_reason"] = json.RawMessage(`""`)
	rewritten, err := json.Marshal(carried)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(state.RunID); err == nil || !strings.Contains(err.Error(), "landing_reason") {
		t.Errorf("Read() error = %v, want the record refused for the missing landing reason", err)
	}
}

// A value inside a nested part of the record is named where it sits.
func TestAnUnknownValueInANestedPartIsNamedWhereItSits(t *testing.T) {
	t.Parallel()

	state := testState(t, StatusRunning)
	state.ReviewDecision = ReviewRepair
	state.ReviewFindings = 1
	state.ReviewFindingDetails = []Finding{{Severity: "advisory", Message: "this reads oddly"}}
	values, only := onlyUnknownValues(state.Validate())
	if !only || len(values) != 1 || values[0].Field != "review_finding_details[0].severity" || values[0].Value != "advisory" {
		t.Errorf("onlyUnknownValues() = %+v, %v, want review_finding_details[0].severity \"advisory\"", values, only)
	}
}

// The stamp beside a run's lease, carrying a field a newer build writes, is
// read for showing and refused by a reading that acts on it.
func TestAHolderStampWithAFieldThisBuildDoesNotKnowIsShownAndRefusedToAct(t *testing.T) {
	said := captureUnknownFieldNotices(t)

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	path, err := store.holderPath(state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	stamp := []byte(`{"pid":` + itoa(os.Getpid()) + `,"held_at":"2026-10-21T14:30:00Z","program_started_at_from_a_later_build":"2026-10-21T14:29:00Z"}`)
	if err := os.WriteFile(path, stamp, 0o600); err != nil {
		t.Fatal(err)
	}

	holder, err := store.Holder(state.RunID)
	if err != nil || holder == nil || holder.PID != os.Getpid() {
		t.Fatalf("Holder() = %+v, %v, want the stamp read without the field", holder, err)
	}
	if _, err := store.Presence(state, time.Minute, time.Now()); err != nil {
		t.Errorf("Presence() error = %v, want the stamp read for showing", err)
	}
	if _, err := store.Held(state.RunID); err != nil {
		t.Errorf("Held() error = %v, want the stamp read for showing", err)
	}
	if !strings.Contains(said.String(), "program_started_at_from_a_later_build in a run holder") {
		t.Errorf("notices = %q, want the field named", said.String())
	}

	if _, err := readRunHolder(path, false); err == nil || !strings.Contains(err.Error(), "program_started_at_from_a_later_build") {
		t.Errorf("readRunHolder(strict) error = %v, want the field named", err)
	}
	if _, err := store.presence(state, time.Minute, time.Now(), false); err == nil {
		t.Error("presence(strict) read a stamp holding a field this build does not know")
	}
}

func itoa(n int) string {
	encoded, _ := json.Marshal(n)
	return string(encoded)
}

// The development manager's list of stopped runs keeps an entry a newer build
// recorded with a stop class this build does not know, and shows the class as
// unknown with what was recorded; recording one is still refused.
func TestADocketEntryWithAnUnknownStopClassIsListedAndShownAsUnknown(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	entry.StopClass = StopRelaunchBudget.Name()
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(encoded), `"relaunch-budget"`, `"a-cause-from-a-later-build"`, 1)
	if rewritten == string(encoded) {
		t.Fatal("the entry carries no stop class to rewrite")
	}
	if err := os.WriteFile(store.Path(), []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := store.List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("List() = %v, %v, want the entry listed", entries, err)
	}
	if !entries[0].StopClassUnknown || entries[0].StopClass != "a-cause-from-a-later-build" {
		t.Errorf("List()[0] = stop class %q, unknown %v, want it kept as written and marked unknown", entries[0].StopClass, entries[0].StopClassUnknown)
	}
	if rendered := entries[0].Render(); !strings.Contains(rendered, `Stop cause: unknown to this build (recorded as "a-cause-from-a-later-build")`) {
		t.Errorf("Render() = %q, want the stop cause shown as unknown with what was recorded", rendered)
	}

	later := testDocketEntry("run-fedcba9876543210fedcba9876543210", "yoyodyne-two")
	later.StopClass = "a-cause-from-a-later-build"
	if _, err := store.RecordOnce(later); err == nil {
		t.Error("RecordOnce() recorded a stop class this build does not know")
	}
}

// A status this build does not know is still refused by the listing: the
// listings choose runs by status, and reading one past as unknown would drop a
// live run out of the runs in flight unseen.
func TestARecordWithAnUnknownStatusIsStillRefused(t *testing.T) {
	captureUnknownFieldNotices(t)

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, state.RunID+".json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var carried map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &carried); err != nil {
		t.Fatal(err)
	}
	carried["status"] = json.RawMessage(`"a-status-from-a-later-build"`)
	rewritten, err := json.Marshal(carried)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(state.RunID); err == nil || !strings.Contains(err.Error(), "a-status-from-a-later-build") {
		t.Errorf("Read() error = %v, want the unknown status refused and named", err)
	}
}

// A pause cause this build does not know is said as unknown rather than read as
// the usage limit a record naming no cause means.
func TestAnUnknownPauseCauseIsNotDescribedAsAUsageLimit(t *testing.T) {
	t.Parallel()

	if got := DescribePause("a-cause-from-a-later-build", ""); got != `a pause whose cause is unknown to this build (recorded as "a-cause-from-a-later-build")` {
		t.Errorf("DescribePause() = %q", got)
	}
	if got := DescribePause("", ""); got != "an exhausted provider usage limit" {
		t.Errorf("DescribePause(no cause) = %q, want the usage limit an old record means", got)
	}
	if got := PhaseShown(PhaseReviewing); got != PhaseReviewing {
		t.Errorf("PhaseShown(%q) = %q, want the phase itself", PhaseReviewing, got)
	}
}
