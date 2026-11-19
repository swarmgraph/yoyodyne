package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeTeamBranchUpdates struct {
	updates []runstate.TeamBranchUpdate
	err     error
}

func (f fakeTeamBranchUpdates) All() ([]runstate.TeamBranchUpdate, error) { return f.updates, f.err }

// A landed update and a conflict are said in the records' own words, in the
// standing's lines and its JSON; with no source nothing is said.
func TestTheStandingSaysWhereTheTargetBranchStandsAgainstTheTeamsBranch(t *testing.T) {
	t.Parallel()
	landedAt := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	landed := runstate.TeamBranchUpdate{TargetBranch: "staging", UpstreamBranch: "main"}
	landed.Pending = &runstate.TeamBranchUpdateAttempt{TeamCommit: strings.Repeat("a", 40)}
	landed.Land(landedAt, strings.Repeat("b", 40))
	conflicted := runstate.TeamBranchUpdate{
		TargetBranch: "release", UpstreamBranch: "main",
		Pending: &runstate.TeamBranchUpdateAttempt{
			TeamCommit: strings.Repeat("c", 40),
			Conflict:   &runstate.TeamBranchUpdateConflict{Paths: []string{"a.go", "b.go"}, FoundAt: landedAt},
		},
	}

	sources := quietSources()
	sources.TeamBranchUpdates = fakeTeamBranchUpdates{updates: []runstate.TeamBranchUpdate{landed, conflicted}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.TeamBranchUpdates) != 2 || len(standing.TeamBranchUpdateSays) < 2 {
		t.Fatalf("updates = %#v, says = %#v", standing.TeamBranchUpdates, standing.TeamBranchUpdateSays)
	}
	rendered := standing.RenderLines()
	for _, want := range []string{
		"staging was last brought up to date with main",
		"merge bbbbbbbbbbbb",
		"Bringing release up to date with main is waiting: merging main at cccccccccccc conflicts in a.go, b.go",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("lines lack %q:\n%s", want, rendered)
		}
	}
	encoded, err := json.Marshal(standing)
	if err != nil || !strings.Contains(string(encoded), `"team_branch_update_says":[`) {
		t.Fatalf("json = %s, %v", encoded, err)
	}

	sources.TeamBranchUpdates = nil
	none := ReadStanding(context.Background(), sources)
	if len(none.TeamBranchUpdates) != 0 || strings.Contains(none.Render(), "up to date with") {
		t.Fatalf("no source, but %q is said", none.Render())
	}
}

// A record that cannot be read is said as a problem rather than as none.
func TestTheStandingSaysWhenTheTeamBranchUpdateRecordCannotBeRead(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.TeamBranchUpdates = fakeTeamBranchUpdates{err: errors.New("disk gone")}
	standing := ReadStanding(context.Background(), sources)
	if !strings.Contains(standing.TeamBranchUpdateProblem, "disk gone") || !strings.Contains(standing.RenderLines(), "disk gone") {
		t.Fatalf("problem = %q", standing.TeamBranchUpdateProblem)
	}
}
