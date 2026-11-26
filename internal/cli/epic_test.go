package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// epicBench runs `yoyo epic` as it will run once every path is wired, against a
// tracker the test holds.
type epicBench struct {
	t         *testing.T
	stateRoot string
	config    string
	items     *listedItems
	trackerIs error // why the tracker cannot be read, where it cannot
	paths     []maystart.PathEntry
}

func newEpicBench(t *testing.T) *epicBench {
	t.Helper()
	bench := &epicBench{t: t, stateRoot: t.TempDir(), config: writeConfig(t, validConfig), paths: maystart.Paths()}
	t.Setenv("YOYODYNE_STATE_HOME", bench.stateRoot)
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv(execution.StartedByVariable, "")
	t.Setenv(execution.LaunchdJobVariable, "")
	for at := range bench.paths {
		bench.paths[at].Wired = true
	}
	bench.items = &listedItems{
		// Listed below its parent's place, and closed with open work below it.
		{ID: "yoyodyne-a.1.1", Title: "Deep", Status: "in_progress", Priority: 1, Labels: []string{"reliability"}, Parent: "yoyodyne-a.1"},
		{ID: "yoyodyne-a", Title: "Epic A", Status: "open", Description: "What A is.\nAll of it."},
		{ID: "yoyodyne-a.1", Title: "Done", Status: "closed", Parent: "yoyodyne-a"},
		{ID: "yoyodyne-a.2", Title: "Linked", Status: "blocked", Dependencies: []beads.Dependency{{ID: "yoyodyne-a", Type: "parent-child"}}},
		{ID: "yoyodyne-b", Title: "Epic B", Status: "open", Description: "What B is."},
		{ID: "yoyodyne-b.1", Title: "Other", Status: "open", Parent: "yoyodyne-b"},
		{ID: "yoyodyne-loose", Title: "Loose", Status: "open", Description: "?"},
		{ID: "yoyodyne-blank", Title: "Blank", Status: "open", Description: " \n\t"},
	}
	return bench
}

func (b *epicBench) run(args ...string) (string, string, int) {
	b.t.Helper()
	var stdout, stderr bytes.Buffer
	command := epicCommand{paths: b.paths, stdout: &stdout, stderr: &stderr,
		tracker: func(string) (itemLister, error) { return b.items, b.trackerIs }}
	code := command.run(context.Background(), append(args, "--config", b.config))
	return stdout.String(), stderr.String(), code
}

func (b *epicBench) does(args ...string) string {
	b.t.Helper()
	stdout, stderr, code := b.run(args...)
	if code != 0 {
		b.t.Fatalf("epic %v exited %d: %s", args, code, stderr)
	}
	return stdout
}

func (b *epicBench) record() epicscope.Record {
	b.t.Helper()
	record, err := epicscope.Read(b.stateRoot, "yoyodyne")
	if err != nil {
		b.t.Fatal(err)
	}
	return record
}

// refuses runs an act that must be refused in words holding every want, and
// leave the record as it was.
func (b *epicBench) refuses(args []string, wants ...string) {
	b.t.Helper()
	before := b.record()
	_, stderr, code := b.run(args...)
	if code != 1 {
		b.t.Fatalf("epic %v exited %d, want 1", args, code)
	}
	says(b.t, stderr, wants...)
	if after := b.record(); !reflect.DeepEqual(before, after) {
		b.t.Fatalf("refused epic %v changed the record to %+v", args, after)
	}
}

func says(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%q does not say %q", text, want)
		}
	}
}

func members(epic epicscope.Epic) []string {
	ids := []string{}
	for _, member := range epic.Members {
		ids = append(ids, member.WorkItemID)
	}
	return ids
}

func TestEpicStartRecordsTheListItPrints(t *testing.T) {
	bench := newEpicBench(t)
	stdout := bench.does("start", "yoyodyne-a", "--words", "only this")
	record := bench.record()
	if !record.On() || len(record.Epics) != 1 {
		t.Fatalf("record = %+v, want epic mode on with one epic", record)
	}
	epic := record.Epics[0]
	if got, want := members(epic), []string{"yoyodyne-a.1.1", "yoyodyne-a.2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
	if epic.Description != "What A is.\nAll of it." || epic.Words != "only this" || epic.NamedFrom != viaCommandLine {
		t.Fatalf("epic = %+v", epic)
	}
	if !strings.HasSuffix(stdout, "2 member(s), the only items that may start in it:\n  (P1, reliability) Deep (yoyodyne-a.1.1)\n  (P0) Linked (yoyodyne-a.2)\n") {
		t.Fatalf("stdout = %q, want exactly the members recorded", stdout)
	}
	says(t, bench.does("list"), "epic mode is on, with 1 epic(s) open", "(P0) Epic A (yoyodyne-a): open", "only this", "(P1, reliability) Deep (yoyodyne-a.1.1), on the list", "    All of it.")
	bench.refuses([]string{"start", "yoyodyne-gone"}, "no work item yoyodyne-gone")
}

// An epic whose description is empty is refused, in words that say why and what
// to do, and no other description is: a single mark is accepted.
func TestEpicStartRefusesAnEmptyDescriptionAndNoOther(t *testing.T) {
	bench := newEpicBench(t)
	for _, empty := range []string{"", " \n\t"} {
		(*bench.items)[len(*bench.items)-1].Description = empty
		bench.refuses([]string{"start", "yoyodyne-blank"}, "yoyodyne-blank (P0: Blank) has an empty description, so it cannot be named", "Write what the epic is for in its description")
	}
	if record := bench.record(); record.On() || len(record.Epics) != 0 {
		t.Fatalf("record = %+v, want nothing named", record)
	}
	says(t, bench.does("start", "yoyodyne-loose"), "named epic (P0) Loose (yoyodyne-loose)")
	if epic := bench.record().Epics[0]; epic.Description != "?" {
		t.Fatalf("the description kept is %q", epic.Description)
	}
	// An epic still open keeps the copy it was named with, so epic mode is turned
	// back on for it even where its description has since been emptied.
	bench.does("off")
	(*bench.items)[len(*bench.items)-2].Description = ""
	says(t, bench.does("start", "yoyodyne-loose"), "epic mode is on again")
}

func TestMovingItemsAfterwardsChangesNothingInTheRecord(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a")
	before := bench.record()
	// One member moves away, an item moves in, and one is created below.
	for at := range *bench.items {
		switch item := &(*bench.items)[at]; item.ID {
		case "yoyodyne-a.2":
			item.Dependencies, item.Parent = nil, "yoyodyne-b"
		case "yoyodyne-loose":
			item.Parent = "yoyodyne-a"
		}
	}
	*bench.items = append(*bench.items, beads.WorkItem{ID: "yoyodyne-a.3", Title: "New", Status: "open", Parent: "yoyodyne-a"})
	bench.does("list")
	if after := bench.record(); !reflect.DeepEqual(before, after) {
		t.Fatalf("record = %+v, want %+v", after, before)
	}
	for item, want := range map[string]bool{"yoyodyne-a.2": true, "yoyodyne-loose": false, "yoyodyne-a.3": false} {
		if got := maystart.Decide(bench.stateRoot, "yoyodyne", maystart.Run(maystart.ReadyWork, item)).MayStart(); got != want {
			t.Errorf("%s may start: %t, want %t", item, got, want)
		}
	}
}

func TestAnItemInAnotherOpenEpicIsRefusedNamingIt(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a")
	bench.does("add", "yoyodyne-a", "yoyodyne-b.1")
	held := []string{"yoyodyne-b.1 (P0: Other)", "already in epic yoyodyne-a (P0: Epic A)"}
	bench.refuses([]string{"start", "yoyodyne-b"}, held...)
	bench.does("add", "yoyodyne-a", "yoyodyne-loose")
	bench.refuses([]string{"start", "yoyodyne-loose"}, "already in epic yoyodyne-a")
	bench.refuses([]string{"add", "yoyodyne-a", "yoyodyne-typo"}, "no work item yoyodyne-typo")
	bench.does("end", "yoyodyne-a")
	bench.does("start", "yoyodyne-b")
	bench.does("start", "yoyodyne-a")
	bench.refuses([]string{"add", "yoyodyne-a", "yoyodyne-b.1"}, "yoyodyne-b.1", "already in epic yoyodyne-b (P0: Epic B)")
}

func TestAnEndedEpicNamedAgainGetsANewEntryWithTheListPrintedNow(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a")
	bench.does("add", "yoyodyne-a", "yoyodyne-loose")
	says(t, bench.does("end", "yoyodyne-a", "--words", "enough"), "ended epic (P0) Epic A (yoyodyne-a)", "epic mode is still on")
	ended := bench.record().Epics[0]
	(*bench.items)[0].Status = "closed"
	says(t, bench.does("start", "yoyodyne-a"), "1 member(s)", "Linked (yoyodyne-a.2)")
	record := bench.record()
	if len(record.Epics) != 2 || !reflect.DeepEqual(record.Epics[0], ended) || !reflect.DeepEqual(members(record.Epics[1]), []string{"yoyodyne-a.2"}) {
		t.Fatalf("epics = %+v, want the first unchanged and the second with the list printed", record.Epics)
	}
	if maystart.Decide(bench.stateRoot, "yoyodyne", maystart.Run(maystart.NamedRun, "yoyodyne-loose")).MayStart() {
		t.Fatal("a member of only the ended entry may start")
	}
}

func TestOffThenStartOnAnEpicStillOpen(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a")
	bench.refuses([]string{"start", "yoyodyne-a"}, "yoyodyne-a (P0: Epic A) is already open", "`yoyo epic add yoyodyne-a")
	before := bench.record()
	says(t, bench.does("off"), "goal mode", "(P0) Epic A (yoyodyne-a): yoyo epic end yoyodyne-a")
	if record := bench.record(); record.On() || !reflect.DeepEqual(record.Epics, before.Epics) {
		t.Fatalf("after off: %+v, want only the mode changed", record)
	}
	*bench.items = append(*bench.items, beads.WorkItem{ID: "yoyodyne-a.3", Title: "New", Status: "open", Parent: "yoyodyne-a"})
	says(t, bench.does("start", "yoyodyne-a"), "its members are unchanged", "  (P0) Linked (yoyodyne-a.2)\n", "(P0) New (yoyodyne-a.3): yoyo epic add yoyodyne-a yoyodyne-a.3")
	if after := bench.record(); !reflect.DeepEqual(before, after) {
		t.Fatalf("record = %+v, want %+v", after, before)
	}
}

func TestTurningEpicModeOnSaysWhatElseIsWorkedOnAgain(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a")
	bench.does("off")
	says(t, bench.does("start", "yoyodyne-b"), "named epic (P0) Epic B (yoyodyne-b)", "Epic A (yoyodyne-a): yoyo epic end yoyodyne-a")
	bench.does("end", "yoyodyne-b")
	if stdout := bench.does("start", "yoyodyne-b"); strings.Contains(stdout, "worked on again") {
		t.Fatalf("epic mode was already on, and start said %q", stdout)
	}
	bench.does("off")
	says(t, bench.does("start", "yoyodyne-b"), "Epic A (yoyodyne-a): yoyo epic end yoyodyne-a")
}

func TestAddAndEndNeedAnOpenEntryAndWorkWhileEpicModeIsOff(t *testing.T) {
	bench := newEpicBench(t)
	bench.refuses([]string{"add", "yoyodyne-a", "yoyodyne-loose"}, "never named", "yoyo epic start yoyodyne-a")
	bench.refuses([]string{"end", "yoyodyne-a"}, "never named")
	says(t, bench.does("list"), "no epic has been named")
	bench.does("start", "yoyodyne-a")
	bench.does("off")
	says(t, bench.does("add", "yoyodyne-a", "yoyodyne-loose"), "Loose (yoyodyne-loose) is a member", "epic mode is off")
	bench.does("end", "yoyodyne-a")
	bench.refuses([]string{"add", "yoyodyne-a", "yoyodyne-loose"}, "already ended", "yoyo epic start yoyodyne-a")
	bench.refuses([]string{"end", "yoyodyne-a"}, "yoyodyne-a (P0: Epic A) has already ended")
	if record := bench.record(); record.On() || !record.Epics[0].Has("yoyodyne-loose") {
		t.Fatalf("record = %+v", record)
	}
}

func TestEpicStartIsRefusedWhileAnyPathIsNotWired(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-b")
	bench.does("end", "yoyodyne-b")
	bench.does("start", "yoyodyne-a")
	bench.does("off")
	// One is enough, whichever it is.
	for at := range bench.paths {
		bench.paths[at].Wired = false
		bench.refuses([]string{"start", "yoyodyne-a"}, bench.paths[at].Says)
		bench.paths[at].Wired = true
	}
	for _, at := range []int{0, len(bench.paths) - 1} {
		bench.paths[at].Wired = false
	}
	// Naming anew, naming again, and turning back on.
	for _, epic := range []string{"yoyodyne-loose", "yoyodyne-b", "yoyodyne-a"} {
		bench.refuses([]string{"start", epic}, bench.paths[0].Says, bench.paths[len(bench.paths)-1].Says)
	}
	if _, stderr, _ := bench.run("start", "yoyodyne-a"); strings.Contains(stderr, bench.paths[1].Says) {
		t.Fatalf("the refusal names a wired path: %q", stderr)
	}
	// The command as it ships asks the real list.
	_, stderr, code := runCLI(t, "epic", "start", "yoyodyne-a", "--config", bench.config)
	for _, path := range maystart.Paths() {
		if !path.Wired && (code != 1 || !strings.Contains(stderr, path.Says)) {
			t.Errorf("exited %d without naming %q: %s", code, path.Says, stderr)
		}
	}
}

// The command as it ships is accepted: every way of starting work is wired.
func TestEpicStartAsItShipsIsAccepted(t *testing.T) {
	bench := newEpicBench(t)
	if err := refusedUntilWired(maystart.Paths()); err != nil {
		t.Fatal(err)
	}
	// The list it ships, with nothing marked wired here, against a tracker.
	bench.paths = maystart.Paths()
	bench.does("start", "yoyodyne-a")
	if !bench.record().On() {
		t.Fatal("epic mode is not on")
	}
	if _, stderr, _ := runCLI(t, "epic", "start", "yoyodyne-a", "--config", bench.config); strings.Contains(stderr, "cannot be turned on yet") {
		t.Fatalf("the command as it ships refuses: %s", stderr)
	}
}

func TestOnlyAPersonNamesEpics(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a")
	for variable, value := range map[string]string{
		execution.AgentRoleVariable: string(domain.RoleProductManager),
		execution.StartedByVariable: "the scheduler",
	} {
		t.Setenv(variable, value)
		for _, act := range [][]string{{"start", "yoyodyne-b"}, {"add", "yoyodyne-a", "yoyodyne-loose"}, {"remove", "yoyodyne-a", "yoyodyne-a.2", "--words", "no"}, {"end", "yoyodyne-a"}, {"off"}} {
			bench.refuses(act, "refused from a process the harness", "a person names the epics")
		}
		bench.does("list")
		t.Setenv(variable, "")
	}
}

func TestEpicSaysWhatItDidAsJSON(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a")
	bench.does("off")
	says(t, bench.does("start", "yoyodyne-b", "--json"), `"named":true`, `"turned_on":true`, `"worked_on_again"`, `"yoyodyne-b.1"`)
	stdout, _, code := bench.run("end", "yoyodyne-gone", "--json")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	says(t, stdout, `"error"`, "never named")
}

func TestATrackerThatCannotBeReadStopsNoActThatDoesNotNeedIt(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a", "--words", "before yoyodyne-b.1")
	says(t, bench.does("list"), "before yoyodyne-b.1 (P0: Other)")
	bench.trackerIs = errors.New("the store is locked")
	says(t, bench.does("list"), "yoyodyne-a (tracker unreadable at ", "yoyodyne-a.1.1 (tracker unreadable at ")
	says(t, bench.does("off"), "yoyodyne-a (tracker unreadable at ", ": yoyo epic end yoyodyne-a")
	says(t, bench.does("end", "yoyodyne-a"), "ended epic yoyodyne-a (tracker unreadable at ")
	bench.refuses([]string{"end", "yoyodyne-a"}, "yoyodyne-a (tracker unreadable at ", "has already ended")
	bench.refuses([]string{"start", "yoyodyne-a"}, "the store is locked")
}

// What an act lets start is not left recorded as refused, and an act whose
// record of it cannot be updated stands and says so.
func TestAnActTakesAwayTheRefusalsItEnds(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-b")
	refuse := func(items ...string) {
		t.Helper()
		for _, item := range items {
			if maystart.Ask(bench.stateRoot, "yoyodyne", maystart.Run(maystart.ReadyWork, item), time.Now()).MayStart() {
				t.Fatalf("%s may start", item)
			}
		}
	}
	refused := func(want ...string) {
		t.Helper()
		refusals, err := maystart.ReadRefusals(bench.stateRoot, "yoyodyne")
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, item := range refusals.Items {
			got = append(got, item.WorkItemID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("refused = %v, want %v", got, want)
		}
	}
	refuse("yoyodyne-loose", "yoyodyne-a.2", "yoyodyne-else")
	bench.does("add", "yoyodyne-b", "yoyodyne-loose")
	refused("yoyodyne-a.2", "yoyodyne-else")
	bench.does("start", "yoyodyne-a")
	refused("yoyodyne-else")
	bench.does("end", "yoyodyne-a")
	refused("yoyodyne-else")
	bench.does("off")
	refused()

	bench.does("start", "yoyodyne-a")
	path := filepath.Join(home.ProductDirectory(bench.stateRoot, "yoyodyne"), "not-started.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := bench.run("off")
	if code != 0 || bench.record().On() {
		t.Fatalf("off exited %d: %s", code, stderr)
	}
	says(t, stderr, "was not updated")
}

func TestEpicStartAndAddRefuseAnItemThatJoinedAnotherOpenEpic(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a")
	store, err := epicprogress.Open(bench.stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Join("yoyodyne-a", epicprogress.Joined{WorkItemID: "yoyodyne-b.1", From: "yoyodyne-a.2", Judgment: "inside", Session: "session-1", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	bench.refuses([]string{"start", "yoyodyne-b"}, "yoyodyne-b.1 (P0: Other) is already in epic yoyodyne-a (P0: Epic A)")
	bench.does("start", "yoyodyne-loose")
	bench.refuses([]string{"add", "yoyodyne-loose", "yoyodyne-b.1"}, "yoyodyne-b.1 (P0: Other) is already in epic yoyodyne-a (P0: Epic A)")
	bench.does("off")
	says(t, bench.does("start", "yoyodyne-a"), "3 member(s)", "(P0) Other (yoyodyne-b.1), which joined it")
	bench.does("end", "yoyodyne-a")
	bench.does("add", "yoyodyne-loose", "yoyodyne-b.1")
}

func TestEpicRemoveTakesOneItemOut(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a")
	store, err := epicprogress.Open(bench.stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	join := func(epic, item, from string) error {
		return store.Join(epic, epicprogress.Joined{WorkItemID: item, From: from, Judgment: "inside", Session: "session-1", At: time.Now()})
	}
	for item, from := range map[string]string{"yoyodyne-b.1": "yoyodyne-a.2", "yoyodyne-loose": "check-1"} {
		if err := join("yoyodyne-a", item, from); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(home.ProductDirectory(bench.stateRoot, "yoyodyne"), "epic-progress.json")
	progress := func() string {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	mayStart := func(item string, want bool) {
		t.Helper()
		if got := maystart.Decide(bench.stateRoot, "yoyodyne", maystart.Run(maystart.ReadyWork, item)).MayStart(); got != want {
			t.Errorf("%s may start: %t, want %t", item, got, want)
		}
	}
	before := progress()
	bench.refuses([]string{"remove", "yoyodyne-a", "yoyodyne-a.2"}, "needs your words")
	bench.refuses([]string{"remove", "yoyodyne-a", "yoyodyne-a.1", "--words", "no"}, "yoyodyne-a.1 (P0: Done) is not in epic yoyodyne-a")
	bench.refuses([]string{"remove", "yoyodyne-b", "yoyodyne-b.1", "--words", "no"}, "never named")
	says(t, bench.does("remove", "yoyodyne-a", "yoyodyne-a.2", "--words", "not needed"), "took (P0) Linked (yoyodyne-a.2) out of epic (P0) Epic A",
		"(P0) Other (yoyodyne-b.1): yoyo epic remove yoyodyne-a yoyodyne-b.1 --words <text>")
	bench.does("remove", "yoyodyne-a", "yoyodyne-b.1", "--words", "nor this")
	for _, item := range []string{"yoyodyne-a.2", "yoyodyne-b.1"} {
		mayStart(item, false)
		if err := join("yoyodyne-a", item, "check-1"); err == nil || !strings.Contains(err.Error(), "cannot join it again") {
			t.Errorf("%s joined again: %v", item, err)
		}
	}
	if after := progress(); after != before {
		t.Errorf("the progress file became\n%s", after)
	}
	epic := bench.record().Epics[0]
	if len(epic.Removed) != 2 || epic.Removed[0].Act != epicscope.ActStart || epic.Removed[0].AddedAt == nil || epic.Removed[1].AddedAt != nil || epic.Removed[1].Words != "nor this" {
		t.Errorf("removed = %+v", epic.Removed)
	}
	says(t, bench.does("list"), "Linked (yoyodyne-a.2), taken out at", "not needed")
	bench.refuses([]string{"remove", "yoyodyne-a", "yoyodyne-a.2", "--words", "no"}, "is not in epic")

	// It can be put back, and what is out can be taken by another epic.
	bench.does("add", "yoyodyne-a", "yoyodyne-a.2")
	mayStart("yoyodyne-a.2", true)
	if len(bench.record().Epics[0].Removed) != 2 {
		t.Error("putting it back took its removal out of the record")
	}
	bench.does("start", "yoyodyne-b")
	mayStart("yoyodyne-b.1", true)

	// A progress that cannot be read stops only what may have joined.
	for _, name := range []string{"epic-progress-begun.json", "epic-progress.json"} {
		if name == "epic-progress.json" {
			if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Remove(filepath.Join(filepath.Dir(path), name)); err != nil {
			t.Fatal(err)
		}
		bench.refuses([]string{"remove", "yoyodyne-a", "yoyodyne-loose", "--words", "no"}, "whether work item yoyodyne-loose", "cannot be known", name)
	}
	says(t, bench.does("remove", "yoyodyne-a", "yoyodyne-a.2", "--words", "again"), "which pieces came from it could not be read")
	bench.does("end", "yoyodyne-a")
	bench.refuses([]string{"remove", "yoyodyne-a", "yoyodyne-a.1.1", "--words", "no"}, "already ended")
}

func TestAReportAboutAMemberNamesItsEpic(t *testing.T) {
	bench := newEpicBench(t)
	store, err := runstate.NewReportStore(bench.stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	parts := components{reports: store}
	parts.stateRoot, parts.config.Product.ID = bench.stateRoot, "yoyodyne"
	reports := parts.epicReports()
	// Filing a report of a design conflict asks what was already filed.
	lister, lists := reports.(interface {
		List() ([]report.Report, error)
	})
	if !lists {
		t.Fatal("the reports wired to a run cannot be listed")
	}
	file := func(item, want string) {
		t.Helper()
		collected, err := report.Collect([]report.Entry{{Severity: report.SeverityNote, Message: "noticed"}}, report.Attribution{
			Role: domain.RoleDeveloper, RunID: "run-0123456789abcdef0123456789abcdef", WorkItemID: item, ProductID: "yoyodyne", RepositoryID: "yoyodyne"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := reports.Append(collected[0]); err != nil {
			t.Fatal(err)
		}
		listed, err := lister.List()
		if err != nil {
			t.Fatal(err)
		}
		at := slices.IndexFunc(listed, func(r report.Report) bool { return r.ID == collected[0].ID })
		if at < 0 || listed[at].DuringEpic != want {
			t.Errorf("the reports are %+v, want the one about %q to name epic %q", listed, item, want)
		}
	}
	file("yoyodyne-a.2", "")
	bench.does("start", "yoyodyne-a")
	file("yoyodyne-a.2", "yoyodyne-a")
	file("yoyodyne-loose", "")
	bench.does("end", "yoyodyne-a")
	file("yoyodyne-a.2", "")
}
