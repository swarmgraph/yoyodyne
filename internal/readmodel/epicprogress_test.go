package readmodel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// runsOfItems is fakeRuns that can also list one item's runs.
type runsOfItems struct {
	fakeRuns
	byItem map[string][]runstate.State
}

func (r runsOfItems) Runs(workItemID string) ([]runstate.State, error) {
	return r.byItem[workItemID], nil
}

// later is after the harness first kept progress.
var afterKept = time.Date(2027, 1, 4, 9, 0, 0, 0, time.UTC)

// progressFixture is two open epics, named after progress was kept.
func progressFixture(t *testing.T) (root string, sources Sources, store *epicprogress.Store, epic epicprogress.Naming) {
	t.Helper()
	root = t.TempDir()
	for _, naming := range []epicscope.Naming{
		{Epic: "yoyodyne-ep", Members: []string{"yoyodyne-ep.1"}},
		{Epic: "yoyodyne-two", Members: []string{"yoyodyne-two.1"}},
	} {
		naming.From = "terminal"
		started, err := operatorOf(t, root).Start(naming, afterKept)
		if err != nil {
			t.Fatal(err)
		}
		if epic.WorkItemID == "" {
			epic = epicprogress.Of(started.Epic)
		}
	}
	store, err := epicprogress.Open(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	sources = quietSources()
	sources.Tracker = statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"open": {
		{ID: "yoyodyne-ep", Title: "Epic mode"}, {ID: "yoyodyne-ep.1", Title: "Keep it"}, {ID: "yoyodyne-piece", Title: "The piece"},
		{ID: "yoyodyne-two", Title: "Second epic"}, {ID: "yoyodyne-two.1", Title: "Other"},
	}}}}
	sources.Epics = NewEpicRecords(root, "yoyodyne")
	return root, sources, store, epic
}

func problemsOf(t *testing.T, sources Sources) string {
	t.Helper()
	return strings.Join(ReadStanding(context.Background(), sources).Epics.Problems, "\n")
}

func fileOf(root, name string) string {
	return filepath.Join(home.ProductDirectory(root, "yoyodyne"), name)
}

func join(t *testing.T, store *epicprogress.Store) {
	t.Helper()
	if err := store.Join("yoyodyne-ep", epicprogress.Joined{WorkItemID: "yoyodyne-piece", From: "yoyodyne-ep.1", Judgment: "inside", Session: "session-1", At: afterKept}); err != nil {
		t.Fatal(err)
	}
}

func wantSaid(t *testing.T, said string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(said, want) {
			t.Errorf("status says\n%s\nwant it to say %q", said, want)
		}
	}
}

func TestStatusCountsAPieceThatJoined(t *testing.T) {
	t.Parallel()
	_, sources, store, _ := progressFixture(t)
	join(t, store)
	epics := ReadStanding(context.Background(), sources).Epics
	if open := epics.Open[0]; open.Waiting != 2 || len(epics.Problems) != 0 {
		t.Fatalf("Epics = %+v, want the member and the piece, and no problem", epics)
	}
}

func TestStatusSaysProgressIsMissingAfterBothFilesAreRemoved(t *testing.T) {
	t.Parallel()
	root, sources, store, _ := progressFixture(t)
	run := func(id string, at time.Time) runstate.State {
		return runstate.State{RunID: id, WorkItemID: "yoyodyne-ep.1", StartedAt: at}
	}
	sources.Runs = runsOfItems{byItem: map[string][]runstate.State{"yoyodyne-ep.1": {run("run-1", afterKept.Add(time.Minute)), run("run-0", afterKept.Add(-time.Hour))}}}
	if _, err := store.ReserveRun("run-1", []string{"yoyodyne-ep.1"}); err != nil {
		t.Fatal(err)
	}
	if said := problemsOf(t, sources); said != "" {
		t.Fatalf("with its run reserved and another from before it was named, status says %q", said)
	}
	for _, name := range []string{"epic-progress.json", "epic-progress-begun.json"} {
		if err := os.Remove(fileOf(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	wantSaid(t, problemsOf(t, sources), "The progress of Epic mode (yoyodyne-ep) is missing: 1 run of it is older than anything kept", fileOf(root, "epic-progress.json"))
}

func TestStatusNamesAnUnreadableEpicAndTheFile(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"epic-progress.json", "epic-progress-begun.json"} {
		root, sources, store, _ := progressFixture(t)
		join(t, store)
		if err := os.Remove(fileOf(root, name)); err != nil {
			t.Fatal(err)
		}
		said := problemsOf(t, sources)
		wantSaid(t, said, "Nothing starts in Epic mode (yoyodyne-ep)", fileOf(root, name), "yoyo epic end yoyodyne-ep, then yoyo epic start yoyodyne-ep")
		if strings.Contains(said, "yoyodyne-two") {
			t.Errorf("status names the epic that carries on: %s", said)
		}
		if err := os.WriteFile(fileOf(root, name), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		wantSaid(t, problemsOf(t, sources), "Every start that asks about the epics you named is refused", fileOf(root, name))
	}
}

func TestStatusNamesAnItemInTwoOpenEpics(t *testing.T) {
	t.Parallel()
	root, sources, store, _ := progressFixture(t)
	join(t, store)
	content, err := os.ReadFile(fileOf(root, "epic-scope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileOf(root, "epic-scope.json"), []byte(strings.Replace(string(content), `"yoyodyne-two.1"`, `"yoyodyne-piece"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	said := problemsOf(t, sources)
	wantSaid(t, said, "The piece (yoyodyne-piece) is in more than one open epic, so it does not start: Epic mode (yoyodyne-ep), Second epic (yoyodyne-two).", "yoyo epic end yoyodyne-ep, or yoyo epic end yoyodyne-two")
	if strings.Count(said, "more than one open epic") != 1 {
		t.Errorf("it is said more than once:\n%s", said)
	}
}

func TestStatusSaysAnUnknownResultOfAReservation(t *testing.T) {
	t.Parallel()
	_, sources, store, epic := progressFixture(t)
	if _, err := store.Call(epic, "judge-1", "a joining judgment", nil, func() (string, error) { return "", errors.New("cut off") }); err == nil {
		t.Fatal("the call was not cut off")
	}
	if said := problemsOf(t, sources); said != "" {
		t.Fatalf("before it is looked up, status says %q", said)
	}
	if _, err := store.Call(epic, "judge-1", "a joining judgment", func() (string, epicprogress.Looked, error) { return "", epicprogress.CannotTell, nil }, nil); !errors.Is(err, epicprogress.ErrResultUnknown) {
		t.Fatal(err)
	}
	wantSaid(t, problemsOf(t, sources), "In Epic mode (yoyodyne-ep), the result of a joining judgment", "cannot be known: nothing shows whether it was sent. The development manager decides.")
}
