package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeTeamConflicts struct {
	decisions []TeamConflictDecision
	err       error
}

func (f *fakeTeamConflicts) DecideConflict(_ context.Context, decision TeamConflictDecision) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.decisions = append(f.decisions, decision)
	return "recorded your decision that a run of Reword the greeting (yoyodyne-item) resolves the conflict: round 1 of 3", nil
}

const teamConflictAction = `{"action":"team_conflict","id":"yoyodyne-item","reason":"the item changed that line, so it knows both wordings"}`

func teamConflictOptions(t *testing.T, conflicts TeamConflicts, action string) Options {
	t.Helper()
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-item": {ID: "yoyodyne-item", Title: "Reword the greeting", Status: "in_progress"},
	}}
	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	options := triageOptions(t, tracker, budgets, trackerReply("Deciding the conflict.", action))
	options.Reports = &fakeReports{}
	if conflicts != nil {
		options.TeamConflicts = conflicts
	}
	return options
}

// She decides that a run of one item resolves the conflict, and the decision
// reaches the update in her name, with her reason, for the item she named.
func TestTheDevelopmentManagerDecidesThatARunResolvesATeamBranchConflict(t *testing.T) {
	t.Parallel()

	conflicts := &fakeTeamConflicts{}
	reply := triageSend(t, teamConflictOptions(t, conflicts, teamConflictAction))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(conflicts.decisions) != 1 {
		t.Fatalf("actions = %#v, decisions %#v; want one decision recorded", reply.Actions, conflicts.decisions)
	}
	decision := conflicts.decisions[0]
	if decision.ItemID != "yoyodyne-item" || !strings.Contains(decision.Reason, "knows both wordings") || !strings.HasPrefix(decision.By, "the development manager in conversation ") {
		t.Fatalf("decision = %#v, want the item, her reason, and her named from the conversation's own facts", decision)
	}
	if !strings.Contains(reply.Actions[0].Summary, "round 1 of 3") {
		t.Fatalf("summary = %q, want what the update said it recorded", reply.Actions[0].Summary)
	}
}

// A decision the update refuses, such as a fourth round, or one nothing can
// reach the update for, is refused whole and nothing is said to have been done.
func TestADecisionOnATeamBranchConflictIsRefusedWhereItCannotBeRecorded(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		conflicts TeamConflicts
		want      string
	}{
		"no update":    {conflicts: nil, want: "cannot reach the update of the target branch"},
		"fourth round": {conflicts: &fakeTeamConflicts{err: errors.New("a fourth round is refused: the 3 rounds are spent")}, want: "a fourth round is refused"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reply, err := openTestSession(t, teamConflictOptions(t, test.conflicts, teamConflictAction)).Send(context.Background(), "Work the docket.")
			said := refusalOf(reply, err) + renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
			if !strings.Contains(said, test.want) {
				t.Fatalf("Send() said %q, want it to contain %q", said, test.want)
			}
			for _, outcome := range reply.Actions {
				if outcome.Applied {
					t.Fatalf("actions = %#v, want the decision refused", reply.Actions)
				}
			}
		})
	}
}
