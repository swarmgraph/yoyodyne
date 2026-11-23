package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func landedDefectBlock(payload string) string {
	return landedDefectFence + "\n" + payload + "\n```\n"
}

// fakeLandedDefects records what it was asked and answers with the run as the
// store would write it, or with the store's refusal.
type fakeLandedDefects struct {
	runs  []string
	notes []string
	arm   runstate.CheaperFirstArm
	err   error
}

func (f *fakeLandedDefects) RecordDefectAfterLanding(_ context.Context, runID, note string, _ time.Time) (runstate.State, error) {
	f.runs = append(f.runs, runID)
	f.notes = append(f.notes, note)
	if f.err != nil {
		return runstate.State{}, f.err
	}
	return runstate.State{RunID: runID, CheaperFirst: &runstate.CheaperFirst{Arm: f.arm, DefectAfterLanding: &runstate.LandedDefect{Note: note}}}, nil
}

func landedDefectSession(t *testing.T, role domain.AgentRole, defects LandedDefects, replies ...string) (*Session, *fakeBackend) {
	t.Helper()
	results := make([]backendapi.RunResult, 0, len(replies))
	for _, reply := range replies {
		results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: reply})
	}
	provider := &fakeBackend{results: results}
	options := testOptions(t, provider)
	options.Role = role
	options.Store = newTestStore(t, t.TempDir())
	options.LandedDefects = defects
	return openTestSession(t, options), provider
}

// The development manager records a defect after landing on a run that started
// on the cheaper model, and is told the trial is stopped.
func TestTheDevelopmentManagerRecordsADefectAfterLandingAndIsToldTheTrialStopped(t *testing.T) {
	t.Parallel()

	defects := &fakeLandedDefects{arm: runstate.CheaperFirstCheaper}
	session, provider := landedDefectSession(t, domain.RoleDevelopmentManager, defects,
		"The status line broke after that landing.\n\n"+landedDefectBlock(`{"run":"run-0123456789abcdef0123456789abcdef","note":"broke the status line"}`),
		"Noted.",
	)
	reply, err := session.Send(context.Background(), "Anything landed badly?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(reply.Text, "yoyodyne-landed-defect") {
		t.Errorf("the block reached the operator's prose:\n%s", reply.Text)
	}
	if !strings.Contains(provider.requests[0].SystemPrompt, "yoyodyne-landed-defect") {
		t.Error("the development manager's contract does not say how to record a defect after landing")
	}
	if reply.LandedDefect == nil || !reply.LandedDefect.Recorded || !reply.LandedDefect.StoppedTrial {
		t.Fatalf("reply.LandedDefect = %+v, want it recorded and the trial stopped", reply.LandedDefect)
	}
	if len(defects.runs) != 1 || defects.runs[0] != "run-0123456789abcdef0123456789abcdef" || defects.notes[0] != "broke the status line" {
		t.Fatalf("recorded %v %v", defects.runs, defects.notes)
	}
	if _, err := session.Send(context.Background(), "And now?"); err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if next := provider.requests[1].Prompt; !strings.Contains(next, "# Defect after landing") || !strings.Contains(next, "the trial of starting developers on the cheaper model is stopped") {
		t.Errorf("the next turn is not told the trial stopped:\n%s", next)
	}
}

// The store's refusal — a run outside the trial — is an outcome the role is
// told, and never a failed turn.
func TestARefusedDefectAfterLandingIsToldToTheRole(t *testing.T) {
	t.Parallel()

	defects := &fakeLandedDefects{err: errors.New("run run-1: the run took no part in the trial of starting developers on the cheaper model")}
	session, _ := landedDefectSession(t, domain.RoleDevelopmentManager, defects,
		landedDefectBlock(`{"run":"run-1","note":"broken"}`),
	)
	reply, err := session.Send(context.Background(), "record it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if reply.LandedDefect == nil || reply.LandedDefect.Recorded || !strings.Contains(reply.LandedDefect.Failure, "took no part in the trial") {
		t.Fatalf("reply.LandedDefect = %+v, want the store's refusal", reply.LandedDefect)
	}
}

// A role that does not hold run.record-landed-defect is refused the block, and
// nothing is recorded on any run.
func TestOnlyTheDevelopmentManagerMayRecordADefectAfterLanding(t *testing.T) {
	t.Parallel()

	defects := &fakeLandedDefects{arm: runstate.CheaperFirstCheaper}
	session, provider := landedDefectSession(t, domain.RoleArchitect, defects,
		landedDefectBlock(`{"run":"run-1","note":"broken"}`),
		"Understood.",
	)
	reply, _ := session.Send(context.Background(), "record it")
	if len(defects.runs) != 0 {
		t.Fatalf("the architect's block recorded %v", defects.runs)
	}
	if reply.LandedDefect != nil && reply.LandedDefect.Recorded {
		t.Fatalf("reply.LandedDefect = %+v, want nothing recorded", reply.LandedDefect)
	}
	if strings.Contains(provider.requests[0].SystemPrompt, "yoyodyne-landed-defect") {
		t.Error("the architect's contract offers the block that records a defect after landing")
	}
}
