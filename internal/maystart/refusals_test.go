package maystart

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

const notInAny = outside + " is not in an open epic the operator named"

func noted(t *testing.T, root string, start Start, when time.Time) Refusals {
	t.Helper()
	if err := Note(root, product, start, Decide(root, product, start), when); err != nil {
		t.Fatal(err)
	}
	record, err := ReadRefusals(root, product)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestARefusalIsKeptUntilItsStartMayGoAhead(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	on(t, root)
	path := filepath.Join(home.ProductDirectory(root, product), refusalsFile)

	noted(t, root, Run(ReadyWork, member), at)
	noted(t, root, MessageReading(), at)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a start that may go ahead, or that nothing is kept about, wrote the record (%v)", err)
	}
	noted(t, root, Run(ReadyWork, outside), at)
	noted(t, root, Pass(RecurringPass, domain.RoleProgramManager), at)
	noted(t, root, Run(ReadyWork, outside), at.Add(time.Minute))
	record := noted(t, root, Pass(RecurringPass, domain.RoleProgramManager), at.Add(time.Hour))
	if len(record.Items) != 1 || record.Items[0] != (RefusedItem{WorkItemID: outside, Path: ReadyWork, Why: notInAny, Since: at, Last: at}) {
		t.Fatalf("Items = %+v, want the one first refusal", record.Items)
	}
	if len(record.Passes) != 1 || !reflect.DeepEqual(record.Passes[0], RefusedPasses{Role: domain.RoleProgramManager, Path: RecurringPass, Why: record.Passes[0].Why, Count: 2, Since: at}) {
		t.Fatalf("Passes = %+v, want two since the first", record.Passes)
	}

	// A document that answers a member may start, and says nothing about a run
	// of the other item it answers.
	if record = noted(t, root, DocumentRun(PublishDocument, member, outside), at); len(record.Items) != 1 {
		t.Fatalf("a document's start took away the refusal of a run: %+v", record.Items)
	}
	added, err := operatorAt(t, root).Add(openEpicID, outside, at)
	must(t, added, err)
	if record = noted(t, root, Run(ReadyWork, outside), at); len(record.Items) != 0 || len(record.Passes) != 1 {
		t.Fatalf("once the item may start the record holds %+v", record)
	}
	turnedOff, err := operatorAt(t, root).TurnOff()
	must(t, turnedOff, err)
	if record = noted(t, root, Pass(RecurringPass, domain.RoleProgramManager), at); len(record.Passes) != 0 {
		t.Fatalf("once the pass may start the record holds %+v", record)
	}
}

func TestARecordOfRefusalsThatCannotBeReadIsNeverReplaced(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	on(t, root)
	path := filepath.Join(home.ProductDirectory(root, product), refusalsFile)
	for name, test := range map[string]struct {
		text     string
		readable bool
	}{
		"half written":       {`{"schema_version": 1,`, false},
		"another product's":  {`{"schema_version":1,"product_id":"another"}`, false},
		"a newer build's":    {`{"schema_version":1,"product_id":"yoyodyne","added":true}`, true},
		"one with no time":   {`{"schema_version":1,"product_id":"yoyodyne","items":[{"work_item_id":"yoyodyne-1"}]}`, false},
		"one with no reason": {`{"schema_version":1,"product_id":"yoyodyne","items":[{"work_item_id":"yoyodyne-1","path":"ready-work","since":"2026-11-24T09:00:00Z","last":"2026-11-24T09:00:00Z"}]}`, false},
		"something after it": {`{"schema_version":1,"product_id":"yoyodyne"} {}`, false},
	} {
		if err := os.WriteFile(path, []byte(test.text), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRefusals(root, product); (err == nil) != test.readable {
			t.Errorf("%s: ReadRefusals() error = %v, want readable %t", name, err, test.readable)
		}
		start := Run(ReadyWork, outside)
		if err := Note(root, product, start, Decide(root, product, start), at); err == nil {
			t.Errorf("%s: a refusal was written over a record that was not read whole", name)
		}
		if kept, _ := os.ReadFile(path); string(kept) != test.text {
			t.Errorf("%s: the record now reads %s", name, kept)
		}
	}
}

func kept(t *testing.T, root string) []RefusedItem {
	t.Helper()
	record, err := ReadRefusals(root, product)
	if err != nil {
		t.Fatal(err)
	}
	return record.Items
}

func asked(t *testing.T, root string, path Path, when time.Time) {
	t.Helper()
	if decision := Ask(root, product, Run(path, outside), when); decision.MayStart() || decision.NotKept != nil {
		t.Fatalf("Ask() = %+v, want a refusal that was kept", decision)
	}
}

func TestRefusingTheSameStartAgainUpdatesItsOneEntry(t *testing.T) {
	t.Parallel()
	for _, entry := range Paths() {
		if !entry.Wired || entry.starts != startsRun {
			continue
		}
		root := t.TempDir()
		on(t, root)
		asked(t, root, entry.Path, at)
		asked(t, root, entry.Path, at.Add(time.Hour))
		want := RefusedItem{WorkItemID: outside, Path: entry.Path, Why: notInAny, Since: at, Last: at.Add(time.Hour)}
		if got := kept(t, root); len(got) != 1 || got[0] != want {
			t.Errorf("%s: %+v, want %+v", entry.Path, got, want)
		}
	}
	// Another path is another entry.
	root := t.TempDir()
	on(t, root)
	asked(t, root, ReadyWork, at)
	asked(t, root, BringInFile, at)
	asked(t, root, ReadyWork, at.Add(time.Hour))
	if got := kept(t, root); len(got) != 2 || got[0].Path != ReadyWork || got[0].Last != at.Add(time.Hour) || got[1].Path != BringInFile || got[1].Last != at {
		t.Fatalf("%+v, want an entry for each path", got)
	}
}

// Each of these ends every entry of the item, and leaves another item's.
func TestARefusalThatNoLongerHoldsIsTakenAway(t *testing.T) {
	t.Parallel()
	const other = "yoyodyne-other"
	add := func(t *testing.T, root string) {
		record, err := operatorAt(t, root).Add(openEpicID, outside, at)
		must(t, record, err)
	}
	for name, test := range map[string]struct {
		ends func(t *testing.T, root string) error
		left int
	}{
		"it starts": {func(t *testing.T, root string) error {
			add(t, root)
			return Ask(root, product, Run(NamedRun, outside), at).NotKept
		}, 1},
		"it becomes allowed": {func(t *testing.T, root string) error {
			add(t, root)
			return Settle(root, product)
		}, 1},
		"it becomes allowed, found when another item is asked about": {func(t *testing.T, root string) error {
			add(t, root)
			return Ask(root, product, Run(ReadyWork, member), at).NotKept
		}, 1},
		"it closes": {func(t *testing.T, root string) error {
			return ItemsClosed(root, product, func(item string) bool { return item == outside })
		}, 1},
		"epic mode ends": {func(t *testing.T, root string) error {
			record, err := operatorAt(t, root).TurnOff()
			must(t, record, err)
			return Settle(root, product)
		}, 0},
		"nothing ended it": {func(t *testing.T, root string) error { return Settle(root, product) }, 3},
	} {
		root := t.TempDir()
		on(t, root)
		asked(t, root, ReadyWork, at)
		asked(t, root, DecidedRepair, at)
		Ask(root, product, Run(ReadyWork, other), at)
		if err := test.ends(t, root); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if got := kept(t, root); len(got) != test.left || (test.left == 1 && got[0].WorkItemID != other) {
			t.Errorf("%s: the record holds %+v, want %d", name, got, test.left)
		}
	}
}

// A refusal decided before the operator added the item is not kept after it.
func TestARefusalDecidedBeforeTheItemWasAddedIsNotKept(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	on(t, root)
	start := Run(ReadyWork, outside)
	decision := Decide(root, product, start)
	record, err := operatorAt(t, root).Add(openEpicID, outside, at)
	must(t, record, err)
	if record := noted(t, root, start, at); len(record.Items) != 0 {
		t.Fatalf("%+v", record.Items)
	}
	if err := Note(root, product, start, decision, at); err != nil {
		t.Fatal(err)
	}
	if got := kept(t, root); len(got) != 0 {
		t.Fatalf("the record holds %+v", got)
	}
}

// A start refused because the named epics could not be read is recorded with
// that reason, kept while they cannot be, and given the reason or taken away
// once they can.
func TestAStartThatCouldNotBeJudgedIsRecordedWithThatReason(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	on(t, root)
	path := filepath.Join(home.ProductDirectory(root, product), "epic-scope.json")
	whole, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	asked(t, root, ReadyWork, at)
	if decision := Ask(root, product, Run(ReadyWork, member), at); decision.Answer != CouldNotTell {
		t.Fatalf("Ask() = %+v", decision)
	}
	if err := Settle(root, product); err != nil {
		t.Fatal(err)
	}
	if got := kept(t, root); len(got) != 2 || !strings.Contains(got[0].Why, "could not be read") {
		t.Fatalf("%+v, want both kept with that reason", got)
	}
	if err := os.WriteFile(path, whole, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Settle(root, product); err != nil {
		t.Fatal(err)
	}
	if got := kept(t, root); len(got) != 1 || got[0].WorkItemID != outside || got[0].Why != notInAny {
		t.Fatalf("%+v, want the member's taken away and the other's reason brought up to date", got)
	}
}

func TestAFailedWriteIsReportedAndTheStartIsStillRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	on(t, root)
	// The record is a directory, so it can be neither read nor replaced.
	if err := os.Mkdir(filepath.Join(home.ProductDirectory(root, product), refusalsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, entry := range Paths() {
		if !entry.Wired || entry.starts != startsRun {
			continue
		}
		decision := Ask(root, product, Run(entry.Path, outside), at)
		if decision.Answer != MayNot || decision.NotKept == nil || !strings.Contains(decision.Why, "could not be recorded") {
			t.Errorf("%s: %+v, want a refusal that says it was not recorded", entry.Path, decision)
		}
		if decision = Ask(root, product, Run(entry.Path, member), at); !decision.MayStart() || decision.NotKept == nil {
			t.Errorf("%s: %+v, want a member still allowed and the failure said", entry.Path, decision)
		}
	}
}

func TestARefusedPassIsCountedOnceForEachTimeItCameDue(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	on(t, root)
	pass := func(path Path, agent string) Start { return Pass(path, domain.RoleProgramManager).OfAgent(agent) }
	// An entry written before paths were kept, by role alone.
	path := filepath.Join(home.ProductDirectory(root, product), refusalsFile)
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"product_id":"yoyodyne","passes":[{"role":"program-manager","count":4,"since":"2026-11-24T09:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	noted(t, root, pass(RecurringPass, "a").Due("schedule:a", "nine"), at)
	noted(t, root, pass(RecurringPass, "a").Due("schedule:a", "nine"), at.Add(time.Hour))
	noted(t, root, pass(RecurringPass, "b").Due("schedule:b", "nine"), at)
	noted(t, root, pass(PromptedPass, "a").Due("events", "one"), at)
	noted(t, root, pass(PromptedPass, "a").Due("request", "one"), at)
	// A second thing that can start it does not make the first count again.
	noted(t, root, pass(PromptedPass, "a").Due("events", "one"), at)
	record := noted(t, root, pass(RecurringPass, "a").Due("schedule:a", "ten"), at)
	var counts []int
	for _, entry := range record.Passes {
		counts = append(counts, entry.Count)
	}
	if !reflect.DeepEqual(counts, []int{4, 2, 1, 2}) {
		t.Fatalf("Passes = %+v", record.Passes)
	}

	// Only a new reason writes again for a time already counted.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	noted(t, root, pass(RecurringPass, "a").Due("schedule:a", "ten"), at.Add(time.Hour))
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("the same refusal was written again")
	}
	start := pass(RecurringPass, "a").Due("schedule:a", "ten")
	if err := Note(root, product, start, Decision{Answer: CouldNotTell, Why: "it could not be read"}, at); err != nil {
		t.Fatal(err)
	}
	record, err = ReadRefusals(root, product)
	if err != nil || record.Passes[1].Count != 2 || record.Passes[1].Why != "it could not be read" {
		t.Fatalf("Passes = %+v, %v; want the new reason and the same count", record.Passes, err)
	}

	// Every entry of the role goes at once, the one by role alone included.
	turnedOff, err := operatorAt(t, root).TurnOff()
	must(t, turnedOff, err)
	if err := Note(root, product, start, Decide(root, product, start), at); err != nil {
		t.Fatal(err)
	}
	if record, err = ReadRefusals(root, product); err != nil || len(record.Passes) != 0 {
		t.Fatalf("Passes = %+v, %v; want none", record.Passes, err)
	}
}
