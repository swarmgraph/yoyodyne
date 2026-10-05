package cli

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type reportRecoveryBackend struct {
	replies     []string
	requests    []backendapi.RunRequest
	interrupted bool
}

func (b *reportRecoveryBackend) Run(_ context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	b.requests = append(b.requests, request)
	index := len(b.requests) - 1
	if index >= len(b.replies) {
		return backendapi.RunResult{}, errors.New("unexpected provider invocation")
	}
	if b.interrupted && index == 1 {
		event, err := execution.NewEvent(request.RunID, request.LastSequence+1, time.Now(), execution.EventAgentMessage, "provider.claude-code", map[string]any{"text": b.replies[index]})
		if err != nil {
			return backendapi.RunResult{}, err
		}
		if err := request.EventSink(event); err != nil {
			return backendapi.RunResult{}, err
		}
		return backendapi.RunResult{LastEvent: event.Sequence, FinalText: b.replies[index]}, errors.New("the provider closed the stream")
	}
	if b.replies[index] == "" {
		return backendapi.RunResult{}, errors.New("report request refused")
	}
	return backendapi.RunResult{Backend: domain.BackendClaudeCode, SessionID: "session", FinalText: b.replies[index], CostUSD: 0.12, CostReported: true}, nil
}

const recoveredReport = "```yoyodyne-sweep\n" + `{"status":"complete","summary":"recorded the ruling","findings":[{"issue":"checks wait at review","disposition":"left","detail":"the ruling is in memory"}]}` + "\n```"
const rememberedRuling = "Recorded the ruling.\n```yoyodyne-memory\n" + `{"memories":[{"action":"remember","memory":"review-wait","text":"The checks wait at review until the provider answers."}]}` + "\n```"

type reportRecoveryPass struct {
	trigger       orchestrator.Trigger
	backend       *reportRecoveryBackend
	sweeps        *runstate.SweepStore
	conversations *runstate.ConversationStore
	memories      *runstate.MemoryStore
	reports       *runstate.ReportStore
	clock         *steppedClock
}

func newReportRecoveryPass(t *testing.T, replies ...string) *reportRecoveryPass {
	return newReportRecoveryPassWithOptions(t, nil, replies...)
}

func newReportRecoveryPassWithOptions(t *testing.T, configure func(*chat.Options), replies ...string) *reportRecoveryPass {
	t.Helper()
	root := t.TempDir()
	conversations, err := runstate.NewConversationStore(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	memories, err := runstate.NewMemoryStore(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	sweeps, err := runstate.NewSweepStore(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	reports, err := runstate.NewReportStore(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	b := &reportRecoveryBackend{replies: replies}
	clock := &steppedClock{at: time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC)}
	open := func(ctx context.Context, role domain.AgentRole, agent, _ string, recovery orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
		if agent == "" {
			agent = string(role)
		}
		identity := runstate.ConversationIdentity{Agent: agent, Role: role}
		hold, err := conversations.Claim(ctx, identity)
		if err != nil {
			return nil, nil, nil, err
		}
		replacement, err := recurringReplacement(conversations, identity, recovery)
		if err != nil {
			hold.Release()
			return nil, nil, nil, err
		}
		options := chat.Options{
			Role: role, Agent: agent, Backend: b, Store: conversations, Hold: hold,
			Fresh: replacement != nil, Model: "gpt-6-astra", Provider: domain.BackendClaudeCode,
			AccountAlias: config.DefaultAccountAlias, Repository: filepath.Join(root, "repository"),
			ProductID: "example", RepositoryID: "example", Memories: memories, Reports: reports,
			Briefing: chat.Briefing{Text: "A fresh briefing for the product.", GatheredAt: clock.Now()},
		}
		if configure != nil {
			configure(&options)
		}
		session, err := chat.Open(options)
		if err != nil {
			hold.Release()
			return nil, nil, nil, err
		}
		return session, hold, replacement, nil
	}
	trigger := orchestrator.Trigger{
		Tasks:  map[string]config.RecurringTask{"architect-pass": {Role: domain.RoleArchitect, Every: config.Duration(time.Hour), Enabled: true, MaxTurns: 1, Prompt: "review the product"}},
		Claims: sweeps, Reports: sweeps, Roles: roleConversation{open: open}, Clock: clock,
		RecordFailures: func(ctx context.Context) error {
			return sweeps.RecordPassFailures(ctx, reports, report.Attribution{RepositoryID: "example"}, "factory-watch")
		},
	}
	return &reportRecoveryPass{trigger, b, sweeps, conversations, memories, reports, clock}
}

func (p *reportRecoveryPass) fire(t *testing.T) runstate.Sweep {
	t.Helper()
	if fired, err := p.trigger.Fire(context.Background()); err != nil || len(fired.Fired) != 1 {
		t.Fatalf("Fire() = %+v, %v", fired, err)
	}
	passes, unread, err := p.sweeps.List()
	if err != nil || len(unread) > 0 || len(passes) == 0 {
		t.Fatalf("List() = %+v, %+v, %v", passes, unread, err)
	}
	p.clock.at = p.clock.at.Add(time.Hour)
	return passes[len(passes)-1]
}

func TestMissingRecurringReportIsRecoveredWithoutRepeatingTheWork(t *testing.T) {
	t.Parallel()
	p := newReportRecoveryPass(t, rememberedRuling, recoveredReport)
	pass := p.fire(t)
	if pass.Failed || pass.MissingReport || !pass.ReportRetried || pass.Turns != 2 || pass.Result == nil || len(pass.Result.Findings) != 1 || pass.Untraced {
		t.Fatalf("pass = %+v, want one completed pass with its finding and trace", pass)
	}
	if math.Abs(pass.CostUSD-0.24) > 1e-9 || len(pass.Saved) != 1 || pass.Saved[0].Revision != 1 {
		t.Fatalf("lost cost or first reply's write: %+v", pass)
	}
	memories, _, err := p.memories.Live("architect")
	if err != nil || len(memories) != 1 || len(memories[0].Revisions) != 1 {
		t.Fatalf("memory = %+v, %v; work was repeated", memories, err)
	}
	requests := p.backend.requests
	if len(requests) != 2 || requests[1].SessionID == "" || !strings.Contains(requests[1].Prompt, "Reply with the block alone") || !strings.Contains(requests[1].Prompt, "Do not repeat any action") || !strings.Contains(requests[1].Prompt, "```yoyodyne-sweep") {
		t.Fatalf("report request = %+v", requests)
	}
	passes, _, _ := p.sweeps.List()
	if len(passes) != 1 {
		t.Fatalf("recorded %d passes for one firing", len(passes))
	}
}

func TestMissingRecurringReportTwiceFailsThePass(t *testing.T) {
	t.Parallel()
	p := newReportRecoveryPass(t, rememberedRuling, "Still prose.")
	pass := p.fire(t)
	if !pass.Failed || !pass.MissingReport || !pass.ReportRetried || !pass.Unfinished() || pass.Result != nil || len(p.backend.requests) != 2 || pass.Turns != 2 {
		t.Fatalf("pass = %+v, want failed with unrecorded findings after exactly one request", pass)
	}
	if !strings.Contains(renderSweep(pass), "FAILED PASS") || !strings.Contains(renderSweep(pass), "findings remain unrecorded") {
		t.Fatalf("listing hides the failure: %s", renderSweep(pass))
	}
	if !strings.Contains(pass.Problem, "which stand and were not undone") {
		t.Fatalf("listing hides the first reply's saved write: %s", renderSweep(pass))
	}
}

func TestThreeMissingRecurringReportsOpenAFreshConversationWithMemory(t *testing.T) {
	t.Parallel()
	p := newReportRecoveryPass(t, rememberedRuling, "No block.", "Prose.", "No block.", "Prose.", "No block.", recoveredReport, recoveredReport)
	var old string
	for i := 0; i < 3; i++ {
		pass := p.fire(t)
		if !pass.Failed || pass.ConversationReplacement != nil {
			t.Fatalf("pass %d = %+v", i, pass)
		}
		if i == 0 {
			old = pass.ConversationID
		}
		if pass.ConversationID != old {
			t.Fatal("replaced the conversation before the bound")
		}
	}
	filed, err := p.reports.List()
	if err != nil || len(filed) != 1 {
		t.Fatalf("repeated-failure finding = %+v, %v", filed, err)
	}
	pass := p.fire(t)
	if pass.Failed || pass.ConversationID == old || pass.ConversationReplacement == nil || pass.ConversationReplacement.Previous != old || !strings.Contains(pass.ConversationReplacement.Reason, "3 consecutive passes") {
		t.Fatalf("fourth pass = %+v, want recorded replacement", pass)
	}
	fresh := p.backend.requests[6]
	if fresh.SessionID != "" || !strings.Contains(fresh.Prompt, "A fresh briefing") || !strings.Contains(fresh.Prompt, "The checks wait at review until the provider answers.") {
		t.Fatalf("fresh conversation lacks briefing or memory: %+v", fresh)
	}
	if events, err := p.conversations.LoadEvents(old); err != nil || len(events) == 0 {
		t.Fatalf("lost old conversation's durable events: %d, %v", len(events), err)
	}
	if next := p.fire(t); next.ConversationReplacement != nil || next.ConversationID != pass.ConversationID {
		t.Fatalf("successful report did not reset misses: %+v", next)
	}
	if handled, err := p.reports.Handlings(); err != nil || len(handled) != 1 {
		t.Fatalf("success did not clear failure finding: %+v, %v", handled, err)
	}
}

func TestRecurringReportAlreadyPresentIsNeverRequestedAgain(t *testing.T) {
	t.Parallel()
	p := newReportRecoveryPass(t, recoveredReport)
	pass := p.fire(t)
	if pass.ReportRetried || pass.Failed || pass.Turns != 1 || len(p.backend.requests) != 1 {
		t.Fatalf("pass = %+v; unnecessary report request", pass)
	}
}

func TestARecurringPassRequestsItsReportAtMostOnceAcrossWorkTurns(t *testing.T) {
	t.Parallel()
	more := "```yoyodyne-sweep\n" + `{"status":"more","summary":"one ruling recorded"}` + "\n```"
	p := newReportRecoveryPass(t, "Prose.", more, "More prose.")
	task := p.trigger.Tasks["architect-pass"]
	task.MaxTurns = 3
	p.trigger.Tasks["architect-pass"] = task
	pass := p.fire(t)
	if !pass.Failed || !pass.ReportRetried || !pass.MissingReport || len(p.backend.requests) != 3 {
		t.Fatalf("pass = %+v, requests = %d; asked twice", pass, len(p.backend.requests))
	}
}

func TestReportRequestFailureKeepsTheFirstReplysCostAndWrites(t *testing.T) {
	t.Parallel()
	p := newReportRecoveryPass(t, rememberedRuling, "")
	pass := p.fire(t)
	if !pass.Failed || pass.NotStarted != "" || pass.Turns != 1 || len(pass.Saved) != 1 || math.Abs(pass.CostUSD-0.12) > 1e-9 || !pass.ReportRetried {
		t.Fatalf("lost preceding reply on recovery failure: %+v", pass)
	}
}

func TestReportRequestFailureKeepsTheDocketDeliveredByTheFirstReply(t *testing.T) {
	t.Parallel()
	p := newReportRecoveryPass(t, rememberedRuling, "")
	docket, _, _, _ := docketPassFixture(t, 26, 1, &docketPassRole{})
	p.trigger.Tasks = docket.Tasks
	p.trigger.Docket = docket.Docket
	pass := p.fire(t)
	if !pass.Failed || pass.Docket == nil || pass.Docket.Delivered != 25 || len(pass.Docket.Undelivered) != 1 || pass.Turns != 1 {
		t.Fatalf("report request failure lost the preceding reply's delivery: %+v", pass)
	}
}

func TestAProgramManagerMissingItsReportUsesTheSameRecoveryAndFreshConversation(t *testing.T) {
	t.Parallel()
	p := newReportRecoveryPass(t, rememberedRuling, "No report.", recoveredReport)
	p.trigger.Tasks = nil
	p.trigger.Instances = map[string]config.AgentConfig{"factory-watch": {Role: domain.RoleProgramManager, Triggers: config.Triggers{Every: config.Duration(time.Hour)}}}
	p.trigger.MissingReportLimit = 1
	failed := p.fire(t)
	if !failed.Failed || !failed.MissingReport || failed.Agent != "factory-watch" {
		t.Fatalf("instance pass = %+v", failed)
	}
	recovered := p.fire(t)
	if recovered.Failed || recovered.ConversationReplacement == nil || recovered.ConversationID == failed.ConversationID || recovered.Agent != "factory-watch" {
		t.Fatalf("recovered instance = %+v", recovered)
	}
}

func TestMissingReportCountFollowsTheRoleAcrossTasks(t *testing.T) {
	t.Parallel()
	p := newReportRecoveryPass(t, "Prose.", "No report.", recoveredReport)
	p.trigger.MissingReportLimit = 1
	first := p.fire(t)
	task := p.trigger.Tasks["architect-pass"]
	p.trigger.Tasks = map[string]config.RecurringTask{"another-look": task}
	next := p.fire(t)
	if next.ConversationReplacement == nil || next.ConversationReplacement.Previous != first.ConversationID {
		t.Fatalf("renaming the task lost the role's consecutive misses: %+v", next)
	}
}

func TestAnExternallyReplacedConversationIsReusedAfterMissingReports(t *testing.T) {
	t.Parallel()
	p := newReportRecoveryPass(t, "Prose.", "No report.", recoveredReport)
	p.trigger.MissingReportLimit = 1
	first := p.fire(t)
	identity := runstate.ConversationIdentity{Agent: "architect", Role: domain.RoleArchitect}
	hold, err := p.conversations.Claim(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	// Open and save the new conversation as an operator would, without another
	// provider invocation. The stale pass count must not replace it again.
	current, err := p.conversations.Load(identity)
	if err != nil {
		t.Fatal(err)
	}
	current.ConversationID, err = runstate.NewConversationID()
	if err != nil {
		t.Fatal(err)
	}
	current.ProviderSessionID = ""
	if err := p.conversations.Save(current); err != nil {
		t.Fatal(err)
	}
	if err := hold.Release(); err != nil {
		t.Fatal(err)
	}
	next := p.fire(t)
	if next.ConversationReplacement != nil || next.ConversationID != current.ConversationID || next.ConversationID == first.ConversationID {
		t.Fatalf("replaced a conversation the operator already replaced: %+v", next)
	}
}

// Only Show is permitted by these replies; embedding the interface makes any
// unexpected tracker operation fail the test rather than hiding it.
type documentPassTracker struct {
	chat.Tracker
	reads []string
}

func (t *documentPassTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	t.reads = append(t.reads, id)
	return beads.WorkItem{ID: id, Title: "Inspect document recovery", Status: "open"}, nil
}

func TestARecurringDocumentRefusalKeepsTheOtherActionsAndDoesNotFailThePass(t *testing.T) {
	t.Parallel()
	tracker := &documentPassTracker{}
	document := func(id string) string {
		return artifact.WriteFence + "\n" + `{"documents":[{"action":"create","id":"` + id + `","kind":"design","title":"` + id + `","directory":"docs/designs","body":"# Design","reason":"record the design"}]}` + "\n```\n"
	}
	p := newReportRecoveryPassWithOptions(t, func(o *chat.Options) {
		if err := os.MkdirAll(o.Repository, 0o755); err != nil {
			t.Fatal(err)
		}
		o.Documents = artifact.StoreFor(o.Repository, config.Product{Specifications: config.DefaultSpecifications, Designs: config.DefaultDesigns, Decisions: config.DefaultDecisions, Invariants: config.DefaultInvariants})
		o.Tracker = tracker
	}, document("first-design")+recoveredReport, document("second-design")+recoveredReport,
		document("refused-design")+rememberedRuling+"\n```yoyodyne-tracker\n"+`{"actions":[{"action":"read","id":"example-1"}]}`+"\n```\n```yoyodyne-report\n"+`{"reports":[{"severity":"note","message":"The design is ready. Its submission is waiting."}]}`+"\n```\n", recoveredReport)
	for i := 0; i < 2; i++ {
		seed := p.fire(t)
		if seed.Failed || seed.Result == nil || len(p.backend.requests) != i+1 {
			t.Fatalf("seed pass %d = %+v; requests = %+v", i, seed, p.backend.requests)
		}
	}
	pass := p.fire(t)
	if pass.Failed || pass.Result == nil || pass.Result.Status != "complete" || len(pass.Saved) != 1 || pass.ReportsFiled != 1 {
		t.Fatalf("pass = %+v", pass)
	}
	if len(tracker.reads) != 1 || tracker.reads[0] != "example-1" {
		t.Fatalf("tracker reads = %v", tracker.reads)
	}
	if len(p.backend.requests) != 4 {
		t.Fatalf("requests = %d", len(p.backend.requests))
	}
	prompt := p.backend.requests[3].Prompt
	for _, want := range []string{"Document submission refused", "limit is 2", "document-1.1", "first-design", "document-2.1", "second-design", "example-1", "review-wait"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("refusal prompt lacks %q: %s", want, prompt)
		}
	}
	memories, _, err := p.memories.Live("architect")
	if err != nil || len(memories) != 1 {
		t.Fatalf("memories = %+v, %v", memories, err)
	}
	// Both waiting documents remain undecided in the durable conversation.
	state, err := p.conversations.Load(runstate.ConversationIdentity{Agent: "architect", Role: domain.RoleArchitect})
	if err != nil || len(state.PendingWrites) != 2 {
		t.Fatalf("waiting documents = %+v, %v", state.PendingWrites, err)
	}
	for i, id := range []string{"first-design", "second-design"} {
		write := state.PendingWrites[i]
		if write.Artifact != id || write.Body != "# Design" || write.Action != "create" {
			t.Fatalf("waiting document changed: %+v", write)
		}
		path := filepath.Join(p.backend.requests[0].WorkingDirectory, "docs", "designs", id+".md")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("a waiting document was written: %s, %v", path, err)
		}
	}
}

func TestARecurringInterruptedReplyStillFailsAndKeepsItsSavedWrites(t *testing.T) {
	t.Parallel()
	more := "```yoyodyne-sweep\n" + `{"status":"more","summary":"saved the ruling"}` + "\n```"
	p := newReportRecoveryPass(t, rememberedRuling+more, "The next ruling begins, because")
	p.backend.interrupted = true
	task := p.trigger.Tasks["architect-pass"]
	task.MaxTurns = 2
	p.trigger.Tasks["architect-pass"] = task
	pass := p.fire(t)
	if !pass.Failed || len(pass.Saved) != 1 || !strings.Contains(pass.Problem, "the provider closed the stream") {
		t.Fatalf("pass = %+v", pass)
	}
	events, err := p.conversations.LoadEvents(pass.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == execution.EventAgentMessage && strings.Contains(string(event.Payload), "The next ruling begins, because") {
			found = true
		}
	}
	if !found {
		t.Fatal("the partial reply was not saved")
	}
}

func TestARecurringDocumentOwnershipRefusalKeepsTheOtherActionsAndDoesNotFailThePass(t *testing.T) {
	t.Parallel()
	tracker := &documentPassTracker{}
	answer := artifact.WriteFence + "\n" + `{"documents":[{"action":"create","id":"refused-goals","kind":"goals","title":"Goals","directory":"docs/product","body":"# Goals","reason":"record the goals"}]}` + "\n```\n" +
		rememberedRuling + "\n```yoyodyne-tracker\n" + `{"actions":[{"action":"read","id":"example-1"}]}` + "\n```\n```yoyodyne-report\n" + `{"reports":[{"severity":"note","message":"The goals belong to the product manager. The design work continues."}]}` + "\n```\n"
	p := newReportRecoveryPassWithOptions(t, func(o *chat.Options) {
		if err := os.MkdirAll(o.Repository, 0o755); err != nil {
			t.Fatal(err)
		}
		o.Documents = artifact.StoreFor(o.Repository, config.Product{Specifications: config.DefaultSpecifications, Designs: config.DefaultDesigns, Decisions: config.DefaultDecisions, Invariants: config.DefaultInvariants})
		o.Tracker = tracker
	}, answer, recoveredReport)
	pass := p.fire(t)
	if pass.Failed || pass.Result == nil || pass.Result.Status != "complete" || len(pass.Saved) != 1 || pass.ReportsFiled != 1 {
		t.Fatalf("pass = %+v", pass)
	}
	if len(tracker.reads) != 1 || tracker.reads[0] != "example-1" {
		t.Fatalf("tracker reads = %v", tracker.reads)
	}
	if len(p.backend.requests) != 2 {
		t.Fatalf("requests = %d", len(p.backend.requests))
	}
	prompt := p.backend.requests[1].Prompt
	for _, want := range []string{"Document submission refused", "only the role that owns", "example-1", "review-wait"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("refusal prompt lacks %q: %s", want, prompt)
		}
	}
	memories, _, err := p.memories.Live("architect")
	if err != nil || len(memories) != 1 {
		t.Fatalf("memories = %+v, %v", memories, err)
	}
	state, err := p.conversations.Load(runstate.ConversationIdentity{Agent: "architect", Role: domain.RoleArchitect})
	if err != nil || len(state.PendingWrites) != 0 {
		t.Fatalf("waiting documents = %+v, %v", state.PendingWrites, err)
	}
	events, err := p.conversations.LoadEvents(pass.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == execution.EventDocumentDrafted {
			t.Fatalf("refused document was recorded: %+v", event)
		}
	}
	path := filepath.Join(p.backend.requests[0].WorkingDirectory, "docs", "product", "refused-goals.md")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("refused document was written: %s, %v", path, err)
	}
}
