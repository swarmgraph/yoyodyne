package runstate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The stop causes builds added after the dashboard's last hand restart are the
// ones an older dashboard refused on 2026-10-03; every one of them, and every
// cause outside the work, is a record this build reads back through every door
// a surface reads runs through, and none of them is dropped from a listing.
func TestEveryNewerStopCauseIsReadBackByEveryListing(t *testing.T) {
	t.Parallel()

	classes := []StopClass{StopRelaunchBudget, StopEscalated}
	for _, cause := range []EnvironmentalCause{CauseHandbackMissingChange, CauseProcessVanished} {
		classes = append(classes, cause.StopClass())
	}
	for _, cause := range EnvironmentalCauses() {
		classes = append(classes, cause.StopClass())
	}
	store := newTestStore(t)
	saved := map[string]StopClass{}
	for _, class := range classes {
		state := testState(t, StatusFailed)
		state.StopClass = class
		state.Failure = string(class) + ": what stopped it"
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() with stop class %q error = %v", class, err)
		}
		saved[state.RunID] = class
	}
	for runID, class := range saved {
		for door, read := range map[string]func(string) (State, error){"Load": store.Load, "Read": store.Read} {
			state, err := read(runID)
			if err != nil {
				t.Fatalf("%s(%s) with stop class %q error = %v", door, runID, class, err)
			}
			if state.StopClass != class {
				t.Errorf("%s(%s) stop class = %q, want %q", door, runID, state.StopClass, class)
			}
		}
	}
	recorded, err := store.Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v", err)
	}
	if len(recorded) != len(saved) {
		t.Errorf("Recorded() listed %d runs, want all %d", len(recorded), len(saved))
	}
	if _, err := store.Incomplete(); err != nil {
		t.Errorf("Incomplete() error = %v", err)
	}
	if _, err := store.Outstanding(); err != nil {
		t.Errorf("Outstanding() error = %v", err)
	}
}

// A record carrying a value this build does not know is refused, as strictly as
// it ever was, and the refusal names the record, the field, and the value, so
// nobody is left guessing at corruption — and a listing fails rather than
// skipping the record.
func TestARefusedRecordIsNamedWithItsFieldAndValue(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusFailed)
	state.StopClass = StopRelaunchBudget
	state.Build = strings.Repeat("c", 40)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, state.RunID+".json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(encoded), `"relaunch-budget"`, `"a-cause-from-a-later-build"`, 1)
	if rewritten == string(encoded) {
		t.Fatal("the record carries no stop class to rewrite")
	}
	if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = store.Read(state.RunID)
	var refused RefusedRecordError
	if !errors.As(err, &refused) {
		t.Fatalf("Read() error = %v, want the refused record named", err)
	}
	var unknown UnknownValueError
	if !errors.As(err, &unknown) || unknown.Field != "stop_class" || unknown.Value != "a-cause-from-a-later-build" {
		t.Errorf("Read() error = %v, want the field and the value named", err)
	}
	var stateRefused RefusedStateError
	if !errors.As(err, &stateRefused) {
		t.Errorf("Read() error = %v, want it still the refusal it always was", err)
	}
	for _, want := range []string{path, `stop_class is invalid: "a-cause-from-a-later-build" is not a value this build knows`, "the record was made by build cccccccccccc"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Read() error = %q, want %q in it", err, want)
		}
	}
	if _, err := store.Recorded(); err == nil || !strings.Contains(err.Error(), "a-cause-from-a-later-build") {
		t.Errorf("Recorded() error = %v, want the listing refused with the record named rather than the record skipped", err)
	}
}

// Which builds bear on an unknown value decides what the refusal calls it: a
// reader behind the deployed build is an incompatibility between builds, the
// deployed build refusing it is invalid data, and nothing known is said so.
func TestARefusalSaysWhetherTheBuildsShowIncompatibilityOrInvalidData(t *testing.T) {
	t.Parallel()

	unknown := UnknownValueError{Field: "stop_class", Value: "later"}
	old, current, other := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("d", 40)
	for _, tc := range []struct {
		name   string
		builds RecordBuilds
		want   []string
		not    string
	}{
		{"behind the deployed build", RecordBuilds{Reader: old, Deployed: current, Writer: current},
			[]string{"This process runs build aaaaaaaaaaaa", "the binary on disk is build bbbbbbbbbbbb", "an incompatibility between builds, not damaged data", "Restarting this process onto the deployed build reads it"}, "invalid data"},
		{"the deployed build", RecordBuilds{Reader: current, Deployed: current, Writer: current},
			[]string{"This process is the deployed build", "invalid data rather than a newer build's value"}, "incompatibility"},
		{"made by another build", RecordBuilds{Reader: current, Writer: other},
			[]string{"the record was made by build dddddddddddd", "may be that build's value"}, "invalid data"},
		{"nothing known", RecordBuilds{},
			[]string{"recorded no revision", "cannot be shown from here"}, "invalid data"},
	} {
		said := RefusedRecordError{Record: "/state/run.json", Problem: unknown, Builds: tc.builds}.Error()
		for _, want := range tc.want {
			if !strings.Contains(said, want) {
				t.Errorf("%s: said %q, want %q in it", tc.name, said, want)
			}
		}
		if strings.Contains(said, tc.not) {
			t.Errorf("%s: said %q, which claims %q the builds do not show", tc.name, said, tc.not)
		}
	}
	// A refusal that is not an unknown value says what refused it and no more.
	plain := RefusedRecordError{Record: "/state/run.json", Problem: errors.New("started_at and updated_at are required"), Builds: RecordBuilds{Reader: old, Deployed: current}}
	if said := plain.Error(); said != "the record /state/run.json was refused: started_at and updated_at are required" {
		t.Errorf("plain refusal = %q", said)
	}
}
