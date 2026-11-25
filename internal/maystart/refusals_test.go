package maystart

import (
	"fmt"
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
	if record = noted(t, root, DocumentRun(PublishDocument, member, outside).Of(Document{ID: "a-design", WriteID: "document-1.1"}), at); len(record.Items) != 1 {
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

const documentOut = "the document answers no open epic the operator named and no work item in one"

func designRun(id, write string, confirmed time.Time, answers ...string) Start {
	return DocumentRun(PublishDocument, answers...).Of(Document{ID: id, Title: "A design", WriteID: write, ConfirmedAt: confirmed})
}

func documentAsked(t *testing.T, root string, start Start, when time.Time) {
	t.Helper()
	if decision := Ask(root, product, start, when); decision.MayStart() || decision.NotKept != nil {
		t.Fatalf("Ask() = %+v, want a refusal that was kept", decision)
	}
}

func documentsKept(t *testing.T, root string) []RefusedDocument {
	t.Helper()
	record, err := ReadRefusals(root, product)
	if err != nil {
		t.Fatal(err)
	}
	return record.Documents
}

func TestARefusedDocumentHasOneEntryWhateverItAnswers(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		answers []string
		why     string
	}{
		"nothing": {nil, "the document answers no work item, so it waits until epic mode is turned off or it is written again naming a member of an open epic"},
		"one":     {[]string{outside}, documentOut},
		"several": {[]string{outside, "yoyodyne-other"}, documentOut},
	} {
		root := t.TempDir()
		on(t, root)
		documentAsked(t, root, designRun("a-design", "document-1.1", at, test.answers...), at)
		documentAsked(t, root, designRun("a-design", "document-1.1", at, test.answers...), at.Add(time.Minute))
		want := RefusedDocument{DocumentID: "a-design", Title: "A design", Path: PublishDocument, WriteID: "document-1.1", Answers: test.answers, ConfirmedAt: at, Why: test.why, Since: at, Last: at}
		if got := documentsKept(t, root); len(got) != 1 || !reflect.DeepEqual(got[0], want) || len(kept(t, root)) != 0 {
			t.Errorf("%s: %+v and items %+v, want %+v alone", name, got, kept(t, root), want)
		}
		if err := ItemsClosed(root, product, func(string) bool { return true }); err != nil {
			t.Fatal(err)
		}
		if got := documentsKept(t, root); len(got) != 1 {
			t.Errorf("%s: an item it answers closing left %+v", name, got)
		}
		// A newer write updates the entry, and an older one asked after it does not.
		documentAsked(t, root, designRun("a-design", "document-2.1", at.Add(time.Hour), "yoyodyne-newer"), at.Add(time.Hour))
		documentAsked(t, root, designRun("a-design", "document-1.1", at, test.answers...), at.Add(2*time.Hour))
		want.WriteID, want.Answers, want.ConfirmedAt, want.Why, want.Last = "document-2.1", []string{"yoyodyne-newer"}, at.Add(time.Hour), documentOut, at.Add(time.Hour)
		if got := documentsKept(t, root); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
			t.Errorf("%s: after a newer write %+v, want %+v", name, got, want)
		}
	}
}

func TestARefusedDocumentIsTakenAwayWhenItNoLongerWaits(t *testing.T) {
	t.Parallel()
	add := func(t *testing.T, root string) {
		record, err := operatorAt(t, root).Add(openEpicID, outside, at)
		must(t, record, err)
	}
	for name, test := range map[string]struct {
		ends func(t *testing.T, root string) error
		left int
	}{
		"a run of it starts": {func(t *testing.T, root string) error {
			return Ask(root, product, designRun("a-design", "document-2.1", at, member), at).NotKept
		}, 1},
		"the decision would allow it": {func(t *testing.T, root string) error { add(t, root); return Settle(root, product) }, 1},
		"it is withdrawn, or handed back": {func(t *testing.T, root string) error {
			return DocumentEnded(root, product, "a-design")
		}, 1},
		"epic mode is turned off": {func(t *testing.T, root string) error {
			record, err := operatorAt(t, root).TurnOff()
			must(t, record, err)
			return Settle(root, product)
		}, 0},
		"nothing ended it": {func(t *testing.T, root string) error { return Settle(root, product) }, 2},
	} {
		root := t.TempDir()
		on(t, root)
		documentAsked(t, root, designRun("a-design", "document-1.1", at, outside), at)
		documentAsked(t, root, designRun("another-design", "document-1.2", at), at)
		if err := test.ends(t, root); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if got := documentsKept(t, root); len(got) != test.left || (test.left == 1 && got[0].DocumentID != "another-design") {
			t.Errorf("%s: the record holds %+v, want %d", name, got, test.left)
		}
	}
}

// A record an earlier build wrote has an entry for each item a document answers.
// It loads, and the document's own entry takes the place of those.
func TestARecordWithAnEntryForEachItemADocumentAnswersStillLoads(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	on(t, root)
	entry := `{"work_item_id":"%s","path":"%s","why":"an earlier reason","since":"2026-11-24T09:00:00Z","last":"2026-11-24T09:00:00Z"}`
	text := fmt.Sprintf(`{"schema_version":1,"product_id":"%s","items":[`+entry+","+entry+","+entry+`]}`,
		product, outside, PublishDocument, "yoyodyne-other", PublishDocument, outside, ReadyWork)
	if err := os.WriteFile(filepath.Join(home.ProductDirectory(root, product), refusalsFile), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Settle(root, product); err != nil || len(kept(t, root)) != 3 {
		t.Fatalf("Settle() = %v and left %+v, want all three", err, kept(t, root))
	}
	documentAsked(t, root, designRun("a-design", "document-1.1", at, outside, "yoyodyne-other"), at)
	if got := kept(t, root); len(got) != 1 || got[0].Path != ReadyWork || len(documentsKept(t, root)) != 1 {
		t.Fatalf("items %+v and documents %+v, want the run's entry and the document's", got, documentsKept(t, root))
	}
}

func TestARefusedDocumentThatCouldNotBeRecordedIsStillRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	on(t, root)
	// A document that was not named is refused and never silently dropped.
	if decision := Ask(root, product, DocumentRun(PublishDocument, outside), at); decision.Answer != MayNot || decision.NotKept == nil {
		t.Fatalf("Ask() = %+v", decision)
	}
	if err := os.Mkdir(filepath.Join(home.ProductDirectory(root, product), refusalsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	decision := Ask(root, product, designRun("a-design", "document-1.1", at), at)
	if decision.Answer != MayNot || decision.NotKept == nil || !strings.Contains(decision.Why, "could not be recorded") {
		t.Fatalf("Ask() = %+v, want a refusal that says it was not recorded", decision)
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

// A refused update is one entry for its target branch however often it is tried,
// and it ends when the update begins or is no longer due, and not before.
func TestARefusedUpdateIsOneEntryForItsTargetBranchUntilItBeginsOrIsNotDue(t *testing.T) {
	t.Parallel()
	updates := func(root string) []RefusedUpdate {
		t.Helper()
		record, err := ReadRefusals(root, product)
		if err != nil {
			t.Fatal(err)
		}
		return record.Updates
	}
	refuse := func(root, target string, when time.Time) {
		t.Helper()
		if decision := Ask(root, product, UpdateFromTeam(target), when); decision.Answer != MayNot || decision.NotKept != nil {
			t.Fatalf("%+v, want it refused and kept", decision)
		}
	}
	const why = "every epic the operator named has ended"
	for name, test := range map[string]struct {
		does func(root string) error
		left int
	}{
		"tried again": {func(root string) error { refuse(root, "staging", at.Add(time.Hour)); return nil }, 2},
		"may begin": {func(root string) error {
			off(t, root)
			return Settle(root, product)
		}, 2},
		"begins": {func(root string) error {
			off(t, root)
			return Ask(root, product, UpdateFromTeam("staging"), at).NotKept
		}, 1},
		"the only one due": {func(root string) error { return KeepUpdateOf(root, product, "staging") }, 1},
		"none due":         {func(root string) error { return KeepUpdateOf(root, product, "") }, 0},
	} {
		root := t.TempDir()
		allEnded(t, root)
		refuse(root, "staging", at)
		refuse(root, "other", at)
		if err := test.does(root); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		got := updates(root)
		if len(got) != test.left {
			t.Errorf("%s: %+v, want %d", name, got, test.left)
		}
		if name == "tried again" && got[0] != (RefusedUpdate{TargetBranch: "staging", Path: TeamBranchUpdate, Why: why, Since: at, Last: at.Add(time.Hour)}) {
			t.Errorf("%s: %+v", name, got[0])
		}
	}
	// A refusal made because the record could not be read is given the reason the
	// record then gives.
	root := t.TempDir()
	allEnded(t, root)
	unreadable(t, root)
	if decision := Ask(root, product, UpdateFromTeam("staging"), at); decision.Answer != CouldNotTell || len(updates(root)) != 1 || updates(root)[0].Why != decision.Why {
		t.Fatalf("%+v and %+v, want one entry with that reason", decision, updates(root))
	}
}
