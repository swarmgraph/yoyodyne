package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// plainLanguageGoal is the standing goal the closed item below breaks.
const plainLanguageGoal = "The harness's surfaces read clearly: boundaries between topics and speakers are visible, important findings stand out, every distinction survives a terminal that cannot render emphasis, and user-facing language chooses the ordinary, literal word over metaphor, coinage, or term of art."

// memoryTracker is a tracker held in memory: what the audit lists closed work
// from, and what the correction is admitted into.
type memoryTracker struct {
	items   []beads.WorkItem
	created []beads.NewWorkItem
}

func (m *memoryTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	for _, item := range m.items {
		if item.ID == id {
			return item, nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no item %s", id)
}

func (m *memoryTracker) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	var listed []beads.WorkItem
	for _, item := range m.items {
		if status == "" || item.Status == status {
			listed = append(listed, item)
		}
	}
	return listed, nil
}

func (m *memoryTracker) Create(_ context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	m.created = append(m.created, item)
	priority := 2
	if item.Priority != nil {
		priority = *item.Priority
	}
	created := beads.WorkItem{ID: fmt.Sprintf("example-new.%d", len(m.created)), Title: item.Title, Description: item.Description,
		Notes: item.Notes, Status: "open", Priority: priority, Labels: item.Labels}
	m.items = append(m.items, created)
	return created, nil
}

func (m *memoryTracker) Update(_ context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error) {
	for i, item := range m.items {
		if item.ID == id {
			if change.AppendNotes != "" {
				m.items[i].Notes += "\n" + change.AppendNotes
			}
			return m.items[i], nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no item %s", id)
}

func (m *memoryTracker) Block(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, fmt.Errorf("not used")
}

func (m *memoryTracker) Unblock(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, fmt.Errorf("not used")
}
func (m *memoryTracker) AddBlocker(context.Context, string, string) error    { return nil }
func (m *memoryTracker) RemoveBlocker(context.Context, string, string) error { return nil }
func (m *memoryTracker) Complete(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, fmt.Errorf("not used")
}

// A sweep of the Lead Product Manager's is handed the work closed since its last
// pass with what landed for each, and an item whose landed summary shows a
// standing goal broken is corrected by a work item admitted at priority 0 that
// names the closed item, with the audit recorded in the pass's account.
func TestASweepOverClosedWorkThatBreaksAStandingGoalAdmitsAPriorityZeroCorrection(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Date(2026, 10, 8, 16, 0, 0, 0, time.UTC)
	tracker := &memoryTracker{items: []beads.WorkItem{
		{ID: "example-1", Title: "Say why the line is held on the status line", Status: "closed",
			ClosedAt: now.Add(-2 * time.Hour), CloseReason: "merged by the forge",
			Notes: "Review decision: approve\nReview summary: The status line now names a held line as CAPHOLD-7, the harness's internal code for a provider capacity hold, with no words beside it."},
		{ID: "example-2", Title: "Something closed long ago", Status: "closed", ClosedAt: now.Add(-30 * 24 * time.Hour),
			Notes: "Review summary: an old change"},
		{ID: "example-3", Title: "Open work", Status: "open"},
	}}
	conversations, err := runstate.NewConversationStore(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	sweeps, err := runstate.NewSweepStore(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	var prompts []string
	backend := scriptedBackend{prompts: &prompts, replies: []string{
		"The status line change breaks the plain-language goal: it shows a code word.\n\n```yoyodyne-tracker\n" +
			`{"actions":[{"action":"create","kind":"bug","title":"Name a held line in words on the status line","description":"The status line shows CAPHOLD-7 for a provider capacity hold. Say it in words.","goal":"` + plainLanguageGoal + `","corrects":["example-1"],"reason":"the audit found the code word"}]}` +
			"\n```\n",
		"Admitted the correction.\n\n```yoyodyne-sweep\n" +
			`{"status":"complete","summary":"Audited one closed item; it breaks the plain-language goal and a correction is admitted.","findings":[{"issue":"example-1 shows a code word on the status line","disposition":"filed","filed":["example-new.1"]}],"audits":[{"item":"example-1","goals":["the plain-language goal","the autonomy goal"],"finding":"broken","detail":"the status line shows CAPHOLD-7 rather than words","correction":"example-new.1"}]}` +
			"\n```\n",
	}}
	goals := goal.Set{Sources: []string{"v1-goals"}, Goals: []goal.Goal{{
		Statement: plainLanguageGoal, ArtifactID: "v1-goals", Path: "docs/product/goals/v1-goals.md",
		InForce: true, Approval: artifact.ApprovalApproved,
	}}}
	open := func(_ context.Context, role domain.AgentRole, _, _ string, _ orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
		session, err := chat.Open(chat.Options{
			Role:         role,
			Agent:        "product-manager",
			Backend:      backend,
			Store:        conversations,
			Model:        "opus",
			Provider:     domain.BackendClaudeCode,
			AccountAlias: config.DefaultAccountAlias,
			Repository:   filepath.Join(root, "repository"),
			ProductID:    "example",
			RepositoryID: "example",
			Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
			Tracker:      tracker,
			Goals:        goals,
			Admission:    chat.Admission{WorkItems: domain.ApprovalAutomatic},
		})
		return session, nil, nil, err
	}
	trigger := orchestrator.Trigger{
		Tasks: map[string]config.RecurringTask{"product-manager-sweep": {
			Role: domain.RoleProductManager, Every: config.Duration(12 * time.Hour), Enabled: true, MaxTurns: 2,
			Prompt: "Three jobs this pass. Third, audit the work closed since your last pass.",
		}},
		Claims:     sweeps,
		Reports:    sweeps,
		Roles:      roleConversation{open: open},
		ClosedWork: closedWork{items: tracker},
		Clock:      settableClock{at: &now},
	}
	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}

	// The pass was handed the closed item with what landed, and not the item
	// closed before the lookback or the open one.
	if len(prompts) == 0 {
		t.Fatalf("the role was never asked anything: %+v", fired)
	}
	first := prompts[0]
	for _, want := range []string{"## Work closed since your last pass", "example-1 (Say why the line is held on the status line)", "CAPHOLD-7", `"audits"`} {
		if !strings.Contains(first, want) {
			t.Errorf("the pass's message does not carry %q:\n%s", want, first)
		}
	}
	for _, unwanted := range []string{"example-2", "example-3"} {
		if strings.Contains(first, unwanted) {
			t.Errorf("the pass's message lists %s, which did not close since the last pass", unwanted)
		}
	}

	// The correction was admitted at priority 0, naming the closed item.
	if len(tracker.created) != 1 {
		t.Fatalf("created = %+v, want one correction; the pass: %+v", tracker.created, fired)
	}
	correction := tracker.created[0]
	if correction.Priority == nil || *correction.Priority != 0 {
		t.Errorf("correction priority = %v, want 0", correction.Priority)
	}
	if !strings.Contains(correction.Notes, "Corrects closed item example-1: Say why the line is held on the status line") {
		t.Errorf("correction notes = %q, want the closed item it corrects named", correction.Notes)
	}
	if !strings.Contains(correction.Notes, "Admitted as a standing-goal correction on the pass product-manager-sweep#1.") {
		t.Errorf("correction notes = %q, want the pass that admitted it named", correction.Notes)
	}

	// The pass recorded the audit, the admission, and how far the listing reached.
	recorded, _, err := sweeps.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %d, %v", len(recorded), err)
	}
	pass := recorded[0]
	if pass.Result == nil || len(pass.Result.Audits) != 1 {
		t.Fatalf("pass = %+v, problem %q, want one audit", pass.Result, pass.Problem)
	}
	audit := pass.Result.Audits[0]
	if audit.Item != "example-1" || audit.Finding != sweep.AuditBroken || audit.Correction != "example-new.1" {
		t.Errorf("audit = %+v, want example-1 broken and corrected by example-new.1", audit)
	}
	if len(pass.Admitted) != 1 || pass.Admitted[0] != "example-new.1" {
		t.Errorf("admitted = %v, want the correction", pass.Admitted)
	}
	if !pass.ClosedThrough.Equal(now) {
		t.Errorf("closed through = %s, want the moment the listing was read", pass.ClosedThrough)
	}
	rendered := renderSweep(pass)
	for _, want := range []string{"audited 1 closed item(s) against the standing goals", "* BROKEN example-1, checked against the plain-language goal; the autonomy goal", "corrected by example-new.1"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("yoyo sweeps does not show %q:\n%s", want, rendered)
		}
	}
}
