package epicscope

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/writertest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	product = "yoyodyne"
	epicA   = "yoyodyne-9k1y"
	epicB   = "yoyodyne-7abc"
)

var namedAt = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

func operatorAt(t *testing.T, root string) *Operator {
	t.Helper()
	operator, err := ForOperator(root, product)
	if err != nil {
		t.Fatal(err)
	}
	return operator
}

func namingA() Naming {
	return Naming{
		Epic:        epicA,
		From:        "terminal",
		Words:       "work only on this",
		Description: "Epic <mode>\n\nas it read when named.\n",
		Members:     []string{"yoyodyne-9k1y.2", "yoyodyne-9k1y.3"},
	}
}

func startA(t *testing.T, root string) Record {
	t.Helper()
	record, err := start(operatorAt(t, root), namingA())
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func start(operator *Operator, naming Naming) (Record, error) {
	started, err := operator.Start(naming, namedAt)
	return started.Record, err
}

func filePath(root string) string {
	return filepath.Join(home.ProductDirectory(root, product), fileName)
}

func read(t *testing.T, root string) Record {
	t.Helper()
	record, err := Read(root, product)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestNoFileReadsAsEpicModeOffWithNoEpics(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if record := read(t, root); record.On() || len(record.Epics) != 0 {
		t.Fatalf("Read() = %+v, want epic mode off and no epics", record)
	}
	if _, err := operatorAt(t, root).TurnOff(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filePath(root)); !os.IsNotExist(err) {
		t.Fatalf("turning off what is off wrote the file: %v", err)
	}
}

func TestEveryFieldIsWrittenAndReadBack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	operator := operatorAt(t, root)
	startA(t, root)
	if _, err := operator.Add(epicA, "yoyodyne-9k1y.8", namedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := operator.End(epicA, " done ", namedAt.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	want := Record{SchemaVersion: SchemaVersion, ProductID: product, Mode: ModeEpic, Epics: []Epic{{
		WorkItemID:  epicA,
		NamedAt:     namedAt,
		NamedFrom:   "terminal",
		Words:       "work only on this",
		Description: namingA().Description,
		Members: []Member{
			{"yoyodyne-9k1y.2", namedAt, ActStart},
			{"yoyodyne-9k1y.3", namedAt, ActStart},
			{"yoyodyne-9k1y.8", namedAt.Add(time.Hour), ActAdd},
		},
		Ended: &Ending{At: namedAt.Add(2 * time.Hour), Words: "done"},
	}}}
	if got := read(t, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("Read() = %+v\nwant %+v", got, want)
	}
	if got, want := filepath.Base(filePath(root)), "epic-scope.json"; got != want {
		t.Fatalf("the file is %s, want %s", got, want)
	}
	// Beside the hold on new work.
	holds, err := runstate.NewIntakeHoldStore(root, product)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filePath(root)) != holds.Root() {
		t.Fatalf("the file is in %s and the hold on new work in %s", filepath.Dir(filePath(root)), holds.Root())
	}
}

func TestEndingTheLastEpicLeavesEpicModeOnUntilItIsTurnedOff(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	operator := operatorAt(t, root)
	startA(t, root)
	if _, err := operator.End(epicA, "", namedAt); err != nil {
		t.Fatal(err)
	}
	record := read(t, root)
	if !record.On() {
		t.Fatal("ending the last epic turned epic mode off")
	}
	if _, held := record.Holding("yoyodyne-9k1y.2"); held {
		t.Fatal("an ended epic still holds its members")
	}
	if _, err := operator.TurnOff(); err != nil {
		t.Fatal(err)
	}
	if record := read(t, root); record.On() || len(record.Epics) != 1 {
		t.Fatalf("after turning off, Read() = %+v, want off with the epic's entry kept", record)
	}
}

func TestAnEpicIsNotAMemberAndMembersAreTheListGiven(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	record := startA(t, root)
	if _, held := record.Holding(epicA); held {
		t.Fatal("the epic is its own member")
	}
	if _, held := record.Holding("yoyodyne-9k1y.4"); held {
		t.Fatal("an item below the epic that was not on the list is a member")
	}
	if epic, held := record.Holding("yoyodyne-9k1y.3"); !held || epic.WorkItemID != epicA {
		t.Fatalf("Holding() = %+v, %t", epic, held)
	}
}

func TestRefusedActsWriteNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	operator := operatorAt(t, root)
	startA(t, root)
	if _, err := operator.Start(Naming{Epic: epicB, From: "terminal", Members: []string{"yoyodyne-7abc.1"}}, namedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := operator.End(epicB, "", namedAt); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filePath(root))
	if err != nil {
		t.Fatal(err)
	}
	other := Naming{Epic: "yoyodyne-new", From: "terminal"}
	with := func(change func(*Naming)) Naming { changed := other; change(&changed); return changed }
	for name, test := range map[string]struct {
		act  func() (Record, error)
		want string
	}{
		"an open epic named again": {func() (Record, error) { return start(operator, namingA()) }, "`yoyo epic add " + epicA},
		"an epic another open one has": {func() (Record, error) {
			return start(operator, with(func(n *Naming) { n.Epic = "yoyodyne-9k1y.2" }))
		}, "already in epic " + epicA},
		"another open epic's item": {func() (Record, error) {
			return start(operator, with(func(n *Naming) { n.Members = []string{"yoyodyne-9k1y.2"} }))
		}, "already in epic " + epicA},
		"the epic as its own member": {func() (Record, error) { return operator.Add(epicA, epicA, namedAt) }, "not one of its own members"},
		"a member given twice": {func() (Record, error) {
			return start(operator, with(func(n *Naming) { n.Members = []string{"yoyodyne-x", "yoyodyne-x"} }))
		}, "listed twice"},
		"no place it was named from": {func() (Record, error) { return start(operator, with(func(n *Naming) { n.From = " " })) }, "named from is required"},
		"words past their limit":     {func() (Record, error) { return operator.End(epicA, strings.Repeat("x", MaxWordsBytes+1), namedAt) }, "limit is"},
		"a description past the file": {func() (Record, error) {
			return start(operator, with(func(n *Naming) { n.Description = strings.Repeat("x", maxFileBytes) }))
		}, "larger than"},
		"adding to an ended epic":   {func() (Record, error) { return operator.Add(epicB, "yoyodyne-x", namedAt) }, "has already ended; `yoyo epic start " + epicB},
		"adding to an unnamed epic": {func() (Record, error) { return operator.Add("yoyodyne-new", "yoyodyne-x", namedAt) }, "was never named; `yoyo epic start yoyodyne-new"},
		"ending an unnamed epic":    {func() (Record, error) { return operator.End("yoyodyne-new", "", namedAt) }, "was never named"},
		"adding no identifier":      {func() (Record, error) { return operator.Add(epicA, "../x", namedAt) }, "not a work item identifier"},
		"ending twice":              {func() (Record, error) { return operator.End(epicB, "", namedAt) }, "has already ended"},
	} {
		if _, err := test.act(); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: error = %v, want one saying %q", name, err, test.want)
		}
	}
	// Adding what is already there is not refused, and does not say it was added anew.
	if _, err := operator.Add(epicA, "yoyodyne-9k1y.2", namedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("the file changed:\n%s\nwas\n%s", after, before)
	}
}

func TestAnEndedEpicNamedAgainHasTwoEntries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	operator := operatorAt(t, root)
	startA(t, root)
	if _, err := operator.End(epicA, "done", namedAt); err != nil {
		t.Fatal(err)
	}
	ended := read(t, root).Epics[0]
	again := namingA()
	again.Description, again.Members = "as it reads now", []string{"yoyodyne-9k1y.7"}
	started, err := operator.Start(again, namedAt.Add(time.Hour))
	if err != nil || !started.Named || started.TurnedOn {
		t.Fatalf("Start() = %+v, %v, want a new entry with epic mode already on", started, err)
	}
	record := read(t, root)
	if len(record.Epics) != 2 || !reflect.DeepEqual(record.Epics[0], ended) {
		t.Fatalf("epics = %+v, want the ended entry unchanged and a second", record.Epics)
	}
	want := Epic{WorkItemID: epicA, NamedAt: namedAt.Add(time.Hour), NamedFrom: "terminal", Words: again.Words,
		Description: "as it reads now", Members: []Member{{"yoyodyne-9k1y.7", namedAt.Add(time.Hour), ActStart}}}
	if latest, _ := record.Named(epicA); !reflect.DeepEqual(latest, want) {
		t.Fatalf("the second entry = %+v, want %+v", latest, want)
	}
	if _, held := record.Holding("yoyodyne-9k1y.2"); held {
		t.Fatal("a member of only the ended entry is still held")
	}
	// The acts reach the open entry and leave the ended one alone.
	if _, err := operator.Add(epicA, "yoyodyne-9k1y.8", namedAt); err != nil {
		t.Fatal(err)
	}
	if record, err = operator.End(epicA, "again", namedAt); err != nil || record.Epics[0].Ended.Words != "done" || record.Epics[1].Ended.Words != "again" {
		t.Fatalf("after ending it again: %+v, %v", record.Epics, err)
	}
	// Two entries for one epic are a record only while the earlier has ended.
	record.Epics[0].Ended = nil
	if err := record.Validate(); err == nil || !strings.Contains(err.Error(), "has not ended") {
		t.Fatalf("Validate() = %v, want an entry after an open one refused", err)
	}
}

func TestStartingAnOpenEpicOnlyTurnsEpicModeBackOn(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	operator := operatorAt(t, root)
	before := startA(t, root)
	if _, err := operator.TurnOff(); err != nil {
		t.Fatal(err)
	}
	again := namingA()
	again.Description, again.Members = "changed", []string{"yoyodyne-9k1y.7"}
	started, err := operator.Start(again, namedAt.Add(time.Hour))
	if err != nil || started.Named || !started.TurnedOn || !reflect.DeepEqual(started.Epic, before.Epics[0]) {
		t.Fatalf("Start() = %+v, %v, want only epic mode turned on", started, err)
	}
	if got := read(t, root); !reflect.DeepEqual(got, before) {
		t.Fatalf("Read() = %+v, want the record as it was before off, %+v", got, before)
	}
}

func TestAFileThatCannotBeReadIsAnErrorAndIsNotWrittenOver(t *testing.T) {
	t.Parallel()
	sample := t.TempDir()
	startA(t, sample)
	good, err := os.ReadFile(filePath(sample))
	if err != nil {
		t.Fatal(err)
	}
	without := func(field string) string {
		var fields map[string]any
		if err := json.Unmarshal(good, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, field)
		encoded, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	replaced := func(old, replacement string) string {
		changed := strings.Replace(string(good), old, replacement, 1)
		if changed == string(good) {
			t.Fatalf("the sample does not hold %s", old)
		}
		return changed
	}
	contents := map[string]string{
		"empty":                  "",
		"null":                   "null",
		"an empty object":        "{}",
		"half of it":             string(good[:len(good)/2]),
		"something after it":     string(good) + "{}",
		"a field not known":      replaced(`"mode"`, `"from_a_newer_build": 1, "mode"`),
		"a mode not known":       replaced(`"epic"`, `"half"`),
		"an act not known":       replaced(`"act": "start"`, `"act": "role"`),
		"another schema version": replaced(`"schema_version": 1`, `"schema_version": 2`),
		"another product":        replaced(`"product_id": "yoyodyne"`, `"product_id": "other"`),
		"a file past its limit":  strings.Repeat(" ", maxFileBytes) + string(good),
		"no mode":                without("mode"),
		"no epics":               without("epics"),
		"a repeated member":      replaced("yoyodyne-9k1y.3", "yoyodyne-9k1y.2"),
		"no list of members":     regexp.MustCompile(`(?s)"members": \[.*?\]`).ReplaceAllString(string(good), `"members": null`),
	}
	plant := map[string]func(path string) error{
		"a directory":             func(path string) error { return os.Mkdir(path, 0o700) },
		"a link pointing nowhere": func(path string) error { return os.Symlink(filepath.Join(filepath.Dir(path), "gone"), path) },
		"a link to a good record": func(path string) error { return os.Symlink(filePath(sample), path) },
		"a file it may not open":  func(path string) error { return os.WriteFile(path, good, 0o000) },
	}
	for name, content := range contents {
		plant[name] = func(path string) error { return os.WriteFile(path, []byte(content), 0o600) }
	}
	for name, put := range plant {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if name == "a file it may not open" && os.Geteuid() == 0 {
				t.Skip("root opens every file")
			}
			root := t.TempDir()
			path := filePath(root)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := put(path); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if record, err := Read(root, product); err == nil {
				t.Fatalf("Read() = %+v, want an error", record)
			}
			operator := operatorAt(t, root)
			acts := map[string]func() (Record, error){
				"Start":   func() (Record, error) { return start(operator, namingA()) },
				"Add":     func() (Record, error) { return operator.Add(epicA, "yoyodyne-x", namedAt) },
				"End":     func() (Record, error) { return operator.End(epicA, "", namedAt) },
				"TurnOff": operator.TurnOff,
			}
			for act, do := range acts {
				if _, err := do(); err == nil {
					t.Errorf("%s was accepted over a file that cannot be read", act)
				}
			}
			if after, err := os.Lstat(path); err != nil || !os.SameFile(before, after) {
				t.Fatalf("the file that cannot be read was replaced: %v", err)
			}
		})
	}
}

// Each act reads and replaces the whole file, so without the lock two at once
// would lose one of them.
func TestActsAtOnceLoseNone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	startA(t, root)
	const adds = 20
	var waiting sync.WaitGroup
	failures := make(chan error, adds)
	for index := range adds {
		operator := operatorAt(t, root)
		waiting.Go(func() {
			if _, err := operator.Add(epicA, fmt.Sprintf("yoyodyne-add.%d", index), namedAt); err != nil {
				failures <- err
			}
		})
	}
	waiting.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	epic, _ := read(t, root).Named(epicA)
	if got, want := len(epic.Members), adds+2; got != want {
		t.Fatalf("%d members are recorded, want %d", got, want)
	}
}

func TestTheRecordIsWrittenOnlyInsideTheStateRoot(t *testing.T) {
	writertest.Run(t, writertest.Writer{
		Name:      "the record of named epics",
		Directory: home.ProductDirectoryWithin(t.TempDir(), product),
		File:      fileName,
		Write: func(t *testing.T, root string) error {
			operator, err := ForOperator(root, product)
			if err != nil {
				return err
			}
			_, err = operator.Start(namingA(), namedAt)
			return err
		},
	})
}
