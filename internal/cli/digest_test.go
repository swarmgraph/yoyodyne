package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fireDigestPass fires one pass of a program manager in the lane "factory" over
// a provider that answers with the reply given, and returns what the pass
// recorded and what reached the pile.
func fireDigestPass(t *testing.T, reply string) (runstate.Sweep, []report.Report) {
	t.Helper()

	root := t.TempDir()
	conversations, err := runstate.NewConversationStore(root, "example")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	pile, err := runstate.NewReportStore(root, "example")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	open := func(_ context.Context, _ func() context.Context, role domain.AgentRole, _, _ string, _ orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
		session, err := chat.Open(chat.Options{
			Role:         role,
			Agent:        "factory",
			Lane:         "factory",
			Backend:      answeringBackend{text: reply},
			Store:        conversations,
			Model:        "opus",
			Provider:     domain.BackendClaudeCode,
			AccountAlias: config.DefaultAccountAlias,
			Repository:   filepath.Join(root, "repository"),
			ProductID:    "example",
			RepositoryID: "example",
			Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
			Reports:      pile,
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
	filed, err := pile.List()
	if err != nil {
		t.Fatalf("pile List() error = %v", err)
	}
	return recorded[0], filed
}

func digestReply(entry string) string {
	return "The line is moving." + laneReportSweep + "\n" + report.Fence + "\n" + `{"reports":[` + entry + `]}` + "\n```\n"
}

// A pass's digest lands in the pile stamped with the pass that filed it, and
// the pass records no problem.
func TestAPassFilesItsDigestStampedWithThePass(t *testing.T) {
	t.Parallel()

	pass, filed := fireDigestPass(t, digestReply(`{"severity":"note","digest":{"requests":[{"work":"a stall alarm on the scheduler","goal":"Run development nearly autonomously.","priority":1,"why":"the scheduler died twice"}]}}`))
	if pass.Problem != "" {
		t.Errorf("the pass recorded a problem: %s", pass.Problem)
	}
	if len(filed) != 1 || filed[0].Digest == nil || filed[0].Digest.Pass != "factory-watch#1" || filed[0].Digest.Lane != "factory" {
		t.Fatalf("filed = %#v, want the digest stamped with the lane and the first firing", filed)
	}
}

// A malformed digest is refused with the reason on the pass record, and
// nothing reaches the pile.
func TestAMalformedDigestIsRefusedWithTheReasonOnThePassRecord(t *testing.T) {
	t.Parallel()

	pass, filed := fireDigestPass(t, digestReply(`{"severity":"note","digest":{"requests":[{"work":"a stall alarm","goal":"Run development nearly autonomously.","why":"it died"}]}}`))
	if len(filed) != 0 {
		t.Fatalf("a malformed digest was filed: %#v", filed)
	}
	if !strings.Contains(pass.Problem, "a report this turn carried was not filed") || !strings.Contains(pass.Problem, "requests[0].priority is required") {
		t.Errorf("the pass record does not carry the refusal and its reason: %q", pass.Problem)
	}
	if pass.Result == nil {
		t.Error("the refusal cost the pass its account")
	}
}
