package readmodel

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeHeavyCheckPlaces struct {
	record runstate.HeavyCheckRecord
	err    error
}

func (f fakeHeavyCheckPlaces) Record() (runstate.HeavyCheckRecord, error) { return f.record, f.err }

// keptHeavyCheckFixture is a check that exited an hour before moment and left
// a process behind in its group.
func keptHeavyCheckFixture(moment time.Time) KeptHeavyCheck {
	asked, exited := moment.Add(-2*time.Hour), moment.Add(-time.Hour)
	return KeptHeavyCheck{Permit: runstate.HeavyCheckPermit{
		ID: "permit-0123456789abcdef", Product: "yoyodyne", RunID: "run-kept", WorkItemID: "yoyodyne-ifd.429.47",
		Check: "make race", Command: "make race", Priority: domain.HeavyCheckOrdinary, State: runstate.HeavyCheckExited,
		AskedAt: asked, GrantedAt: &asked, ExitedAt: &exited, Hold: "permit-0123456789abcdef.hold", HoldMark: "mark",
		Problem: "it exited and its process group 4242 still has members, which may be processes it started",
	}}
}

// A place kept for leftover processes is on the attention line as the
// development manager's, naming the check, the run, its item, and why, and
// saying the command that gives it back; a place running or released is not.
func TestAKeptHeavyCheckPlaceIsOnTheAttentionLineAsTheDevelopmentManagers(t *testing.T) {
	t.Parallel()
	moment := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	kept := keptHeavyCheckFixture(moment).Permit
	running := kept
	running.ID, running.State, running.ExitedAt, running.Problem, running.RunID = "permit-1111111111111111", runstate.HeavyCheckRunning, nil, "", "run-running"
	sources := Sources{HeavyChecks: fakeHeavyCheckPlaces{record: runstate.HeavyCheckRecord{
		SchemaVersion: runstate.HeavyCheckSchemaVersion,
		Permits:       []runstate.HeavyCheckPermit{running, kept},
	}}}
	places, problem := ReadKeptHeavyChecks(sources)
	if problem != "" || len(places) != 1 || places[0].Permit.ID != kept.ID {
		t.Fatalf("kept = %#v, problem = %q; want the one kept place", places, problem)
	}
	entry := keptHeavyCheckAttention(places[0])
	if entry.Mover != MoverDevelopmentManager || entry.WorkItemID != "yoyodyne-ifd.429.47" || entry.Label() != "check place kept" {
		t.Fatalf("entry = %+v, label %q", entry, entry.Label())
	}
	if whose := entry.Whose(); !strings.Contains(whose, "yoyo heavy-checks release "+kept.ID+" --by <name> --reason <why>") {
		t.Fatalf("whose = %q, want the command that gives the place back", whose)
	}
	if since := entry.Since(); !since.Equal(*kept.ExitedAt) {
		t.Fatalf("since = %s, want when the check exited", since)
	}
}

// A record nobody can read is said, rather than read as every place free; and
// no record wired says nothing.
func TestAnUnreadableRecordOfPlacesIsSaid(t *testing.T) {
	t.Parallel()
	if places, problem := ReadKeptHeavyChecks(Sources{}); places != nil || problem != "" {
		t.Fatalf("no record wired: %#v, %q", places, problem)
	}
	_, problem := ReadKeptHeavyChecks(Sources{HeavyChecks: fakeHeavyCheckPlaces{err: errors.New("decode: bad")}})
	if !strings.Contains(problem, "could not be read: decode: bad") {
		t.Fatalf("problem = %q", problem)
	}
}
