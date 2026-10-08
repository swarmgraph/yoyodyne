package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func interventionStore(t *testing.T, root string) *runstate.InterventionStore {
	t.Helper()
	store, err := runstate.NewInterventionStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewInterventionStore() error = %v", err)
	}
	return store
}

func recordedSteps(t *testing.T, store *runstate.InterventionStore) map[intervention.Kind][]intervention.Event {
	t.Helper()
	listed, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	byKind := map[intervention.Kind][]intervention.Event{}
	for _, event := range listed {
		byKind[event.Kind] = append(byKind[event.Kind], event)
	}
	return byKind
}

// Approving what the product manager proposed, running the item it became by
// name, and stopping that run are three hand steps, each recorded once as the
// harness carries it out, naming the item and the conversation it came in
// through. What the product manager says between them records nothing.
func TestTheOperatorsCommandsInAConversationAreRecordedAsHandSteps(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	steps := interventionStore(t, root)
	work := &fakeWork{gate: make(chan struct{})}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: proposalReply(
			"Pausing rather than failing is the smaller change.",
			`{"kind":"feature","title":"Pause on a usage limit","description":"Wait and resume.","rationale":"You said capacity is not failure.","goal":"Run development nearly autonomously."}`,
		)},
	}})
	options.Store = newTestStore(t, root)
	options.Tracker = &fakeTracker{}
	options.Work = work
	options.Interventions = steps
	session := openTestSession(t, options)

	var out strings.Builder
	input := strings.NewReader(strings.Join([]string{
		"a run should pause instead of failing when the provider is out of capacity",
		"y",
		"/work yoyodyne-1",
		"/stop we are doing something else first",
		"/exit",
	}, "\n") + "\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	byKind := recordedSteps(t, steps)
	via := "conversation " + session.Evidence().ConversationID
	for _, kind := range []intervention.Kind{intervention.KindApprove, intervention.KindRun, intervention.KindStop} {
		recorded := byKind[kind]
		if len(recorded) != 1 {
			t.Fatalf("recorded %d %s step(s), want one: %+v", len(recorded), kind, byKind)
		}
		if event := recorded[0]; event.Observed || event.Via != via || !event.Names("yoyodyne-1", "") {
			t.Errorf("the %s was recorded as %+v, want a step carried out through %s on yoyodyne-1", kind, event, via)
		}
	}
	if total := len(byKind); total != 3 {
		t.Errorf("recorded kinds %v, want exactly the three the operator took", byKind)
	}
	if subject := byKind[intervention.KindApprove][0].Subject; subject == "" {
		t.Error("the approval does not name the proposal it decided")
	}
}

// Declining a proposal is a hand step too, and it names no item because none
// was created.
func TestDecliningAProposalIsRecordedAsAHandStep(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	steps := interventionStore(t, root)
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: proposalReply(
			"Here is one.",
			`{"kind":"feature","title":"Pause on a usage limit","description":"Wait and resume.","rationale":"Because.","goal":"Run development nearly autonomously."}`,
		)},
	}})
	options.Store = newTestStore(t, root)
	options.Tracker = &fakeTracker{}
	options.Interventions = steps
	session := openTestSession(t, options)

	var out strings.Builder
	input := strings.NewReader("propose something\nn not now\n/exit\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	declined := recordedSteps(t, steps)[intervention.KindDecline]
	if len(declined) != 1 || len(declined[0].Items) != 0 || declined[0].Subject == "" {
		t.Fatalf("declined = %+v, want one step naming the proposal and no item", declined)
	}
}

func interventionBlock(payload string) string {
	return interventionFence + "\n" + payload + "\n```\n"
}

// A program manager that noticed a hand step taken outside the harness writes
// it down with a block: one observed event, taken by whom it names and recorded
// under the instance's own name and role, and the role is told what became of
// it. The block never reaches the operator's prose.
func TestAProgramManagerRecordsAHandStepItNoticed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	steps := interventionStore(t, root)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "The scheduler was restarted by hand this morning.\n\n" +
			interventionBlock(`{"kind":"restart","by":"Mason","at":"2026-08-15T03:30:00-07:00","subject":"the scheduler","said":"restarted the scheduler by hand after it stopped pulling work"}`)},
		{SessionID: "session-1", FinalText: "Noted."},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleProgramManager
	options.Agent = "factory-flow"
	options.Store = newTestStore(t, root)
	options.Interventions = steps
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "How is the line?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(reply.Text, "yoyodyne-intervention") {
		t.Errorf("the block reached the operator's prose:\n%s", reply.Text)
	}
	if !strings.Contains(provider.requests[0].SystemPrompt, "yoyodyne-intervention") {
		t.Error("the program manager's contract does not say how to record a hand step")
	}
	if reply.Observed == nil || !reply.Observed.Recorded {
		t.Fatalf("reply.Observed = %+v, want the step recorded", reply.Observed)
	}
	restarts := recordedSteps(t, steps)[intervention.KindRestart]
	if len(restarts) != 1 {
		t.Fatalf("recorded %+v, want one restart", restarts)
	}
	event := restarts[0]
	if !event.Observed || event.By != "Mason" || event.RecordedBy != "factory-flow" || event.RecordedRole != domain.RoleProgramManager {
		t.Errorf("recorded %+v, want it observed, taken by Mason, recorded by factory-flow as the program manager", event)
	}
	// Before the test clock's noon, as a step noticed afterwards is.
	if want := time.Date(2026, 8, 15, 10, 30, 0, 0, time.UTC); !event.At.Equal(want) {
		t.Errorf("taken at %s, want %s", event.At, want)
	}
	// What became of it travels with the role's next turn, as a restart
	// request's result does.
	if _, err := session.Send(context.Background(), "Anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(provider.requests[1].Prompt, "Recorded as "+event.ID) {
		t.Errorf("the role was not told what became of its block: %s", provider.requests[1].Prompt)
	}
}

// A step the record refuses is an outcome the role is told, never a failed turn.
func TestARefusedHandStepIsToldToTheRole(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	steps := interventionStore(t, root)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Somebody did something.\n\n" + interventionBlock(`{"kind":"restart","by":"","said":"restarted something"}`)},
		{SessionID: "session-1", FinalText: "Noted."},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleProgramManager
	options.Agent = "factory-flow"
	options.Store = newTestStore(t, root)
	options.Interventions = steps
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "How is the line?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if reply.Observed == nil || reply.Observed.Recorded || !strings.Contains(reply.Observed.Failure, "by is required") {
		t.Fatalf("reply.Observed = %+v, want it refused for naming nobody", reply.Observed)
	}
	if listed, _ := steps.List(); len(listed) != 0 {
		t.Errorf("a refused step was recorded: %+v", listed)
	}
}

// Only a role holding report.file may write down a hand step: every other
// role's block is refused before anything is recorded.
func TestOnlyAReportFilingRoleMayRecordAHandStep(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		session := &Session{}
		session.state.Role = role
		err := session.authorize(parsedReply{Observed: &ObservedAsk{Kind: "restart", By: "Mason", Said: "restarted it"}})
		var refusal *AuthorityError
		switch {
		case role == domain.RoleProgramManager && err != nil:
			t.Errorf("authorize() of the program manager's hand step = %v, want it permitted", err)
		case role != domain.RoleProgramManager && !errors.As(err, &refusal):
			t.Errorf("authorize() of the %s's hand step = %v, want an authority refusal", role, err)
		}
	}
}

func TestAnInterventionBlockIsReadStrictly(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{
		``,
		`{"kind":"restart","by":"Mason","said":"x","now":true}`,
		`{"kind":"restart","by":"Mason","said":"x"} {}`,
	} {
		if _, _, err := extractIntervention("prose\n\n" + interventionBlock(payload)); err == nil {
			t.Errorf("extractIntervention(%q) accepted the block", payload)
		}
	}
}
