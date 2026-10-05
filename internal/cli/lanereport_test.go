package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// answeringBackend answers every turn with the same text.
type answeringBackend struct {
	text string
}

func (b answeringBackend) Run(_ context.Context, _ backendapi.RunRequest) (backendapi.RunResult, error) {
	return backendapi.RunResult{Backend: domain.BackendClaudeCode, SessionID: "session-1", FinalText: b.text}, nil
}

const laneReportSweep = "\n\n```yoyodyne-sweep\n" + `{"status":"complete","summary":"looked at the lane"}` + "\n```\n"

// firePassOfProgramManager fires one pass of a program manager's recurring task
// over a provider that answers with the reply given, and returns what the pass
// recorded and the lane report store.
func firePassOfProgramManager(t *testing.T, reply string) (runstate.Sweep, *runstate.LaneReportStore) {
	t.Helper()

	root := t.TempDir()
	conversations, err := runstate.NewConversationStore(root, "example")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	laneReports, err := runstate.NewLaneReportStore(root, "example", readmodel.CheckLaneReportMover)
	if err != nil {
		t.Fatalf("NewLaneReportStore() error = %v", err)
	}
	open := func(_ context.Context, role domain.AgentRole, _, _ string, _ orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
		session, err := chat.Open(chat.Options{
			Role:         role,
			Agent:        "factory",
			Backend:      answeringBackend{text: reply},
			Store:        conversations,
			Model:        "opus",
			Provider:     domain.BackendClaudeCode,
			AccountAlias: config.DefaultAccountAlias,
			Repository:   filepath.Join(root, "repository"),
			ProductID:    "example",
			RepositoryID: "example",
			Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
			LaneReports:  laneReports,
		})
		return session, nil, nil, err
	}
	sweeps, err := runstate.NewSweepStore(root, "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	trigger := orchestrator.Trigger{
		Tasks: map[string]config.RecurringTask{
			"factory-watch": {Role: domain.RoleProgramManager, Every: config.Duration(time.Hour), Enabled: true, Prompt: "watch the line"},
		},
		Claims:  sweeps,
		Reports: sweeps,
		Roles:   roleConversation{open: open},
	}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	recorded, _, err := sweeps.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %d sweeps, %v; want the one pass", len(recorded), err)
	}
	return recorded[0], laneReports
}

// A pass's lane report is stamped with the pass that wrote it as well as the
// conversation turn.
func TestAPassStampsTheLaneReportItWrites(t *testing.T) {
	t.Parallel()

	pass, laneReports := firePassOfProgramManager(t, "The line is moving.\n\n```yoyodyne-lane-report\n"+
		`{"summary":"the line is moving","remaining":[],"blockers":[]}`+"\n```"+laneReportSweep)
	if pass.Problem != "" {
		t.Errorf("the pass recorded a problem: %s", pass.Problem)
	}
	current, found, err := laneReports.Current("factory")
	if err != nil || !found {
		t.Fatalf("Current() = %v, %v", found, err)
	}
	if current.Stamp.Pass != "factory-watch#1" || current.Stamp.Turn != 1 || current.Stamp.ConversationID != pass.ConversationID {
		t.Errorf("the report is stamped %+v, want the first firing of factory-watch in the pass's conversation %s", current.Stamp, pass.ConversationID)
	}
}

// scriptedBackend answers each invocation with the next of its replies, fails
// where the reply is empty, and keeps every prompt it was sent.
type scriptedBackend struct {
	replies []string
	prompts *[]string
}

func (b scriptedBackend) Run(_ context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	*b.prompts = append(*b.prompts, request.Prompt)
	index := len(*b.prompts) - 1
	if index >= len(b.replies) || b.replies[index] == "" {
		return backendapi.RunResult{}, errors.New("the provider went away mid-pass")
	}
	return backendapi.RunResult{Backend: domain.BackendClaudeCode, SessionID: "session-1", FinalText: b.replies[index]}, nil
}

type settableClock struct{ at *time.Time }

func (c settableClock) Now() time.Time { return *c.at }

// A pass that fails after saving a memory and a lane report keeps both: the
// stores hold them, the pass's record names them as standing beside the
// failure, and the pass run after it is told they are already saved.
func TestAPassThatFailsAfterSavingKeepsItsWritesAndTheNextPassIsToldOfThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	conversations, err := runstate.NewConversationStore(root, "example")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	laneReports, err := runstate.NewLaneReportStore(root, "example", readmodel.CheckLaneReportMover)
	if err != nil {
		t.Fatalf("NewLaneReportStore() error = %v", err)
	}
	memories, err := runstate.NewMemoryStore(root, "example")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	sweeps, err := runstate.NewSweepStore(root, "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	var prompts []string
	backend := scriptedBackend{prompts: &prompts, replies: []string{
		// The first pass's first turn saves a memory and a report and has more to do.
		"Half way down the line.\n\n```yoyodyne-memory\n" +
			`{"memories":[{"action":"remember","memory":"line-stalls-at-review","text":"The line stalls at review when the reviewer is on the small model."}]}` +
			"\n```\n\n```yoyodyne-lane-report\n" +
			`{"summary":"half way down the line","remaining":[],"blockers":[]}` +
			"\n```\n\n```yoyodyne-sweep\n" + `{"status":"more","summary":"half way"}` + "\n```\n",
		// Its second turn fails.
		"",
		// The pass run after it completes.
		"The rest of the line." + laneReportSweep,
	}}
	open := func(_ context.Context, role domain.AgentRole, _, _ string, _ orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
		session, err := chat.Open(chat.Options{
			Role:         role,
			Agent:        "factory",
			Backend:      backend,
			Store:        conversations,
			Model:        "opus",
			Provider:     domain.BackendClaudeCode,
			AccountAlias: config.DefaultAccountAlias,
			Repository:   filepath.Join(root, "repository"),
			ProductID:    "example",
			RepositoryID: "example",
			Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
			LaneReports:  laneReports,
			Memories:     memories,
		})
		return session, nil, nil, err
	}
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	trigger := orchestrator.Trigger{
		Tasks: map[string]config.RecurringTask{
			"factory-watch": {Role: domain.RoleProgramManager, Every: config.Duration(time.Hour), Enabled: true, MaxTurns: 2, Prompt: "watch the line"},
		},
		Claims:  sweeps,
		Reports: sweeps,
		Roles:   roleConversation{open: open},
		Clock:   settableClock{at: &now},
	}

	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("first Fire() error = %v", err)
	}
	// The writes stand in their stores.
	live, _, err := memories.Live("factory")
	if err != nil || len(live) != 1 || live[0].Name != "line-stalls-at-review" {
		t.Fatalf("Live() = %+v, %v; want the memory the failed pass saved", live, err)
	}
	report, found, err := laneReports.Current("factory")
	if err != nil || !found || report.Version != 1 {
		t.Fatalf("Current() = %+v, %v, %v; want the report the failed pass saved", report, found, err)
	}
	recorded, _, err := sweeps.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %d sweeps, %v; want the failed pass", len(recorded), err)
	}
	failed := recorded[0]
	if !failed.Failed || !failed.Unfinished() {
		t.Errorf("the pass is recorded as failed %v; want it recorded as not having completed", failed.Failed)
	}
	wantSaved := []runstate.SavedWrite{
		{Kind: runstate.SavedMemory, Action: "remember", Memory: "line-stalls-at-review", Revision: live[0].Current().Sequence},
		{Kind: runstate.SavedLaneReport, Revision: 1},
	}
	if len(failed.Saved) != len(wantSaved) || failed.Saved[0] != wantSaved[0] || failed.Saved[1] != wantSaved[1] {
		t.Errorf("the record's saved writes = %+v, want %+v", failed.Saved, wantSaved)
	}
	for _, want := range []string{"turn 2 of the recurring task factory-watch failed", "saved 2 write(s), which stand and were not undone", `memory "line-stalls-at-review"`, "lane report version 1"} {
		if !strings.Contains(failed.Problem, want) {
			t.Errorf("the record's problem = %q, want it to say %q", failed.Problem, want)
		}
	}

	// The pass run after it is told what is already saved.
	now = now.Add(time.Hour + time.Minute)
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("second Fire() error = %v", err)
	}
	if len(prompts) != 3 {
		t.Fatalf("the provider was asked %d times, want 3", len(prompts))
	}
	for _, want := range []string{"did not complete", "do not write them again", `memory "line-stalls-at-review" (remember, revision`, "lane report version 1"} {
		if !strings.Contains(prompts[2], want) {
			t.Errorf("the next pass's message does not say %q:\n%s", want, prompts[2])
		}
	}
	recorded, _, err = sweeps.List()
	if err != nil || len(recorded) != 2 || recorded[1].Failed || recorded[1].Unfinished() {
		t.Fatalf("List() = %+v, %v; want the second pass completed", recorded, err)
	}

	// Once a pass has completed, the one after it is told nothing of the old writes.
	backend.replies = append(backend.replies, "Quiet."+laneReportSweep)
	now = now.Add(time.Hour + time.Minute)
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("third Fire() error = %v", err)
	}
	if len(prompts) != 4 || strings.Contains(prompts[3], "did not complete") {
		t.Errorf("a pass after a completed one was told of saved writes again: %d prompts", len(prompts))
	}
}

// A lane report refused whole is on the pass record, and nothing is written.
func TestARefusedLaneReportIsOnThePassRecord(t *testing.T) {
	t.Parallel()

	pass, laneReports := firePassOfProgramManager(t, "The line is moving.\n\n```yoyodyne-lane-report\n"+
		`{"summary":"the line is moving","remaining":[]}`+"\n```"+laneReportSweep)
	if !strings.Contains(pass.Problem, "lane report") || !strings.Contains(pass.Problem, "refused whole") {
		t.Errorf("the pass record does not carry the refusal: %q", pass.Problem)
	}
	if pass.Result == nil {
		t.Error("the refusal cost the pass its account")
	}
	if _, found, err := laneReports.Current("factory"); found || err != nil {
		t.Errorf("Current() = %v, %v; a refused report was written", found, err)
	}
}
