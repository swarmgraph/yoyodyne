package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func withSharedRecords(records SharedRecords) Sources {
	sources := quietSources()
	sources.SharedRecords = records
	return sources
}

type fakeSharedRecords struct {
	state    runstate.SharedRecordsState
	recorded bool
	err      error
}

func (f fakeSharedRecords) Load() (runstate.SharedRecordsState, bool, error) {
	return f.state, f.recorded, f.err
}

func TestStandingSaysTheRecordsNotYetShared(t *testing.T) {
	standing := ReadStanding(context.Background(), withSharedRecords(fakeSharedRecords{
		state:    runstate.SharedRecordsState{Waiting: 3, Problem: "remote unreachable"},
		recorded: true,
	}))
	if standing.RecordsNotShared != 3 {
		t.Fatalf("RecordsNotShared = %d, want 3", standing.RecordsNotShared)
	}
	rendered := standing.RenderLines()
	for _, want := range []string{"Not yet shared with the team's other factories: 3 record(s)", "the last try did not get through, because remote unreachable."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendering lacks %q:\n%s", want, rendered)
		}
	}
}

func TestStandingOmitsTheProblemClauseWhenThereIsNone(t *testing.T) {
	count, said, problem := RecordsNotShared(Sources{SharedRecords: fakeSharedRecords{
		state:    runstate.SharedRecordsState{Waiting: 1},
		recorded: true,
	}})
	if count != 1 || problem != "" || strings.Contains(said, "did not get through") || !strings.HasSuffix(said, "forge check.") {
		t.Fatalf("got %d, %q, %q", count, said, problem)
	}
}

func TestStandingSaysNothingOfRecordsWhenNoneWaitOrNoneWereRecorded(t *testing.T) {
	for name, sources := range map[string]Sources{
		"none waiting": withSharedRecords(fakeSharedRecords{state: runstate.SharedRecordsState{Problem: "stale"}, recorded: true}),
		"never saved":  withSharedRecords(fakeSharedRecords{state: runstate.SharedRecordsState{Waiting: 2}}),
		"no source":    quietSources(),
	} {
		standing := ReadStanding(context.Background(), sources)
		if standing.RecordsNotShared != 0 || standing.RecordsNotSharedSays != "" || standing.RecordsNotSharedProblem != "" || strings.Contains(standing.Render(), "Not yet shared") {
			t.Errorf("%s: standing says %#v", name, standing)
		}
	}
}

func TestStandingSaysWhenTheSharedRecordsStateCannotBeRead(t *testing.T) {
	standing := ReadStanding(context.Background(), withSharedRecords(fakeSharedRecords{err: errors.New("disk gone")}))
	if !strings.Contains(standing.RecordsNotSharedProblem, "disk gone") || !strings.Contains(standing.RenderLines(), "disk gone") {
		t.Fatalf("problem = %q", standing.RecordsNotSharedProblem)
	}
}
