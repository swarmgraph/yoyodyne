package maystart

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

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
	noted(t, root, Run(NamedRun, outside), at.Add(time.Hour))
	record := noted(t, root, Pass(RecurringPass, domain.RoleProgramManager), at.Add(time.Hour))
	if len(record.Items) != 1 || record.Items[0] != (RefusedItem{WorkItemID: outside, Path: ReadyWork, Since: at}) {
		t.Fatalf("Items = %+v, want the one first refusal", record.Items)
	}
	if len(record.Passes) != 1 || record.Passes[0] != (RefusedPasses{Role: domain.RoleProgramManager, Count: 2, Since: at}) {
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
