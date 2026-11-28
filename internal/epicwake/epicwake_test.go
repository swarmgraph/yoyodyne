package epicwake

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

var (
	start = time.Date(2026, 11, 28, 17, 0, 0, 0, time.UTC)
	one   = epicprogress.Naming{WorkItemID: "yoyodyne-one", NamedAt: start}
	two   = epicprogress.Naming{WorkItemID: "yoyodyne-two", NamedAt: start}
)

func owed(key string, role domain.AgentRole, namings ...epicprogress.Naming) Event {
	return Event{Key: key, Role: role, Namings: namings, Says: key}
}

// Events within ten minutes of each other are one pass, which carries them all.
func TestEventsWithinTenMinutesAreOnePass(t *testing.T) {
	t.Parallel()
	var record Record
	record.settle([]Event{owed("a", domain.RoleArchitect)}, start)
	record.settle([]Event{owed("a", domain.RoleArchitect), owed("b", domain.RoleArchitect)}, start.Add(5*time.Minute))
	if due := record.Due(start.Add(9 * time.Minute)); len(due) != 0 {
		t.Fatalf("due while gathering: %+v", due)
	}
	due := record.Due(start.Add(Gather))
	if len(due) != 1 || len(due[domain.RoleArchitect].Events) != 2 || !due[domain.RoleArchitect].At.Equal(start.Add(Gather)) {
		t.Fatalf("due = %+v, want one pass of the architect carrying both", due)
	}
	if err := record.woken(domain.RoleArchitect, "pass-1", []string{"a", "b"}, start.Add(Gather)); err != nil {
		t.Fatal(err)
	}
	if due := record.Due(start.Add(Gather + time.Minute)); len(due) != 0 {
		t.Fatalf("due right after the pass: %+v", due)
	}
}

// A pass that does not settle an event is followed by another after an hour;
// after two the event moves up, and from the Lead Product Manager to status.
func TestAnEventLeftStandingIsRepeatedThenMovedUpThenRaised(t *testing.T) {
	t.Parallel()
	var record Record
	events := []Event{owed("stop", domain.RoleDevelopmentManager, one)}
	record.settle(events, start)
	var woken []domain.AgentRole
	// A pull every ten minutes for six hours, the event never settled.
	for at := start; at.Before(start.Add(6 * time.Hour)); at = at.Add(10 * time.Minute) {
		record.settle(events, at)
		for role, due := range record.Due(at) {
			must(t, record.woken(role, at.String(), []string{due.Events[0].Key}, at))
			woken = append(woken, role)
		}
	}
	want := []domain.AgentRole{domain.RoleDevelopmentManager, domain.RoleDevelopmentManager, domain.RoleProductManager, domain.RoleProductManager}
	if len(woken) != len(want) {
		t.Fatalf("woken %v, want %v", woken, want)
	}
	for i := range want {
		if woken[i] != want[i] {
			t.Fatalf("woken %v, want %v", woken, want)
		}
	}
	raised := record.Raised()
	if len(raised) != 1 || raised[0].From != domain.RoleDevelopmentManager || raised[0].Role != domain.RoleProductManager {
		t.Fatalf("raised = %+v", raised)
	}
	if record.settle(nil, start.Add(7*time.Hour)); len(record.Events) != 0 || len(record.Passes) != 4 {
		t.Fatalf("settled, the record is %+v; want no event and the passes kept", record)
	}
}

// A pass's cost goes to its epics, shared equally; one whose cost is not known
// counts as unknown and adds nothing.
func TestAPassIsChargedToItsEpicsSharedEqually(t *testing.T) {
	t.Parallel()
	var record Record
	record.settle([]Event{owed("both", domain.RoleArchitect, one, two), owed("first", domain.RoleProductManager, one)}, start)
	at := start.Add(Gather)
	must(t, record.woken(domain.RoleArchitect, "shared", []string{"both"}, at))
	must(t, record.woken(domain.RoleProductManager, "unpriced", []string{"first"}, at))
	must(t, record.ended("shared", Ended{At: at, CostUSD: 3, Known: true}))
	if err := record.ended("never-started", Ended{}); err == nil {
		t.Fatal("a pass that was never started was given a cost")
	}
	if usd, passes, unknown := record.Cost(one); usd != 1.5 || passes != 2 || unknown != 1 {
		t.Fatalf("one: $%v over %d, %d unknown", usd, passes, unknown)
	}
	if usd, passes, unknown := record.Cost(two); usd != 1.5 || passes != 1 || unknown != 0 {
		t.Fatalf("two: $%v over %d, %d unknown", usd, passes, unknown)
	}
}

// The file is written only where something changed, and a file that cannot be
// read whole is not read as no file.
func TestTheFileIsWrittenOnlyOnAChangeAndReadWhole(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := start
	store, err := Open(root, "yoyodyne", func() time.Time { return now })
	must(t, err)
	path := filepath.Join(home.ProductDirectory(root, "yoyodyne"), fileName)
	if _, err := store.Owed(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("nothing owed wrote the file: %v", err)
	}
	if _, err := store.Owed([]Event{owed("a", domain.RoleArchitect, one)}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(Gather)
	due, err := store.Owed([]Event{owed("a", domain.RoleArchitect, one)})
	must(t, err)
	must(t, store.Woken(domain.RoleArchitect, "pass", due[domain.RoleArchitect].Events))
	must(t, store.Ended("pass", 2, true))
	record, err := Read(root, "yoyodyne")
	if err != nil || len(record.Passes) != 1 || record.Passes[0].Ended == nil || len(record.Events[0].Woken) != 1 {
		t.Fatalf("record = %+v, %v", record, err)
	}
	must(t, os.WriteFile(path, []byte("{"), 0o600))
	if _, err := Read(root, "yoyodyne"); err == nil {
		t.Fatal("a file that is not whole was read")
	}
	if _, err := store.Owed(nil); err == nil {
		t.Fatal("a file that is not whole was written over")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
