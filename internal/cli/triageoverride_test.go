package cli

// The sequence a cap-exhausted item actually goes through, driven end to end:
// the development manager's decision refused by a cap, the escalation that
// refusal is evidence for, the operator crossing the cap with the terminal
// command, and the same decision asked for again and recorded.
//
// It is here rather than in either package it spans because it is the whole
// path or it is nothing. The conversation refuses against the durable counters,
// `yoyo triage override` writes to the same counters through the real command,
// and the resubmission reads them back — and every previous version of this
// promise was true of one of those three and false of the sequence.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// cappedItem is the item this replay is about, and cappedRun the stopped run its
// docket entry named. The run identifier is the one stoppedRunOf mints, because
// the decision is checked against the harness's own record of that run.
const (
	cappedItem = "yoyodyne-ifd.224"
	cappedRun  = "run-0123456789abcdef0123456789abcdef"
)

// A cap refuses the answer to an escalation as readily as it refuses a machine,
// and the operator's override is what crosses it. The whole of that sequence is
// exercised here, because it was true in pieces and false as a path: the refusal
// named a remedy and no verb, the override was recorded in the item's notes
// where the words pointed, and the resubmitted decision met the identical
// refusal. Twice.
func TestAnOperatorOverrideMakesTheRefusedDecisionRecordable(t *testing.T) {
	// Not parallel: the state root the components read is set here, and the
	// override command builds its own components from the same environment.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)
	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	if err := parts.store.Create(stoppedRunOf(cappedItem)); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// The item at the end of its rounds, its last round an approval — which is
	// what 224 was: approved, and then a promotion that conflicted. The rounds
	// that put it against the cap are the ones that sent work back, so they are
	// the ones recorded here.
	caps := triageCapsOf(parts)
	triage := parts.store.Triage()
	for round := 0; round < caps.ReviewRounds; round++ {
		if _, err := triage.RecordReviewRound(context.Background(), cappedItem,
			runstate.RoundKey(cappedRun, round), "pid-1-000000000000000a", time.Now().UTC()); err != nil {
			t.Fatalf("RecordReviewRound() error = %v", err)
		}
	}

	// The development manager decides a repair, and the round cap refuses it.
	backend := &scriptedChatBackend{replies: []string{
		triageDecisionReply("The findings are actionable and the branch is preserved.",
			"repair", "every finding names a file and a line"),
		escalationReply(),
		triageDecisionReply("The cap was crossed, so the decision it refused is recordable now.",
			"repair", "every finding names a file and a line"),
	}}
	tracker := &openItemTracker{id: cappedItem, title: "the item that stopped"}
	session := developmentManagerSession(t, parts, backend, tracker)

	refused := sendTriageTurn(t, session, "Work the docket.")
	if refused.Applied {
		t.Fatalf("the decision was recorded past the cap: %#v", refused)
	}
	// The refusal names the cap, and — this is the part that was missing — the
	// command that crosses it, with the budget and the item already in it. It also
	// says what does not work, because "record an override against the item" was
	// twice read as the item's notes.
	for _, want := range []string{
		"repair grant is refused for " + cappedItem,
		`yoyo triage override --budget "review round"`,
		"--by \"<operator>\"",
		"--reason \"<why>\"",
		cappedItem,
		"Nothing written into the item's notes crosses that cap",
		// And his own crossing, offered before the escalation: the delegation is
		// what the ordinary refusal now points at first.
		`"decision":"cross"`,
	} {
		if !strings.Contains(refused.Failure, want) {
			t.Fatalf("the refusal is missing %q:\n%s", want, refused.Failure)
		}
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("a refused decision wrote on the item: %#v", tracker.updates)
	}

	// So the development manager escalates, which blocks the item and reaches the
	// Lead Product Manager. The cap is then crossed by the operator's hand.
	escalated := sendTriageTurn(t, session, "Nothing here is going to move without you.")
	if !escalated.Applied || len(tracker.blocked) != 1 {
		t.Fatalf("the escalation was not carried out: outcome %#v, blocked %#v", escalated, tracker.blocked)
	}

	// The operator crosses the cap, by hand, through the terminal command the
	// refusal named. Nothing about this goes through a conversation.
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"triage", "override", "--config", configPath,
		"--budget", runstate.TriageReviewRoundBudget,
		"--cap", fmt.Sprint(caps.ReviewRounds + 1),
		"--by", "mason",
		"--reason", "the last round was an approval and the promotion conflicted; this work is right",
		cappedItem,
	}, &stdout, &stderr, "test")
	if code != 0 {
		t.Fatalf("yoyo triage override code = %d; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "recorded an operator override on "+cappedItem) {
		t.Fatalf("the override reported = %q", stdout.String())
	}

	// And the decision the escalation was about is asked for again, and recorded.
	// This is the assertion the whole item is about: the sequence the refusal text
	// promises, ending in a record rather than in the same refusal.
	recorded := sendTriageTurn(t, session, "The operator crossed the cap; carry on.")
	if !recorded.Applied {
		t.Fatalf("the resubmitted decision was refused after the override: %s", recorded.Failure)
	}
	if !strings.Contains(recorded.Summary, `triaged `+cappedItem+` as "repair"`) {
		t.Fatalf("the resubmitted decision reads = %q", recorded.Summary)
	}
	if len(tracker.updates) != 1 {
		t.Fatalf("the item carries %d triage note(s), want the one the override made recordable", len(tracker.updates))
	}

	// The grant is on the item's own durable record, beside the override that
	// permitted it, so what the guards read and what an operator reads back are
	// one thing.
	counters, err := triage.Counters(cappedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.RepairGrants != 1 || counters.GrantedRounds != 1 {
		t.Fatalf("counters after the resubmission = %#v, want the grant the cap had refused", counters)
	}
	override, found := counters.OverrideOf(runstate.TriageReviewRoundBudget)
	if !found || override.DecidedBy != "mason" || override.Cap != caps.ReviewRounds+1 {
		t.Fatalf("recorded override = %#v (found %v), want the operator's own", override, found)
	}
}

// An override recorded in the item's notes is not an override, and nothing about
// the item's budgets moves when one is written there. That is the failure this
// work item exists to end, so it is asserted rather than left implied: the same
// decision, after the same prose, still meets the cap.
func TestANoteOnTheItemDoesNotCrossACap(t *testing.T) {
	// Not parallel: the state root the components read is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	parts, err := buildComponents(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	if err := parts.store.Create(stoppedRunOf(cappedItem)); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	caps := triageCapsOf(parts)
	triage := parts.store.Triage()
	for round := 0; round < caps.ReviewRounds; round++ {
		if _, err := triage.RecordReviewRound(context.Background(), cappedItem,
			runstate.RoundKey(cappedRun, round), "pid-1-000000000000000a", time.Now().UTC()); err != nil {
			t.Fatalf("RecordReviewRound() error = %v", err)
		}
	}

	// The operator's ruling, written where the old refusal text pointed: onto the
	// work item, in prose, in their own name and with their reason.
	tracker := &openItemTracker{id: cappedItem, title: "the item that stopped"}
	if _, err := tracker.Update(context.Background(), cappedItem, beads.WorkItemChange{
		AppendNotes: "Operator override (mason): the review round cap is raised by one round for this item only.",
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	backend := &scriptedChatBackend{replies: []string{
		triageDecisionReply("The operator said one more round.", "repair", "the operator crossed the cap"),
	}}
	session := developmentManagerSession(t, parts, backend, tracker)
	refused := sendTriageTurn(t, session, "Work the docket.")
	if refused.Applied {
		t.Fatalf("a note crossed a cap: %#v", refused)
	}
	if !strings.Contains(refused.Failure, "Nothing written into the item's notes crosses that cap") {
		t.Fatalf("the refusal does not say why the note changed nothing:\n%s", refused.Failure)
	}
	counters, err := triage.Counters(cappedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if len(counters.Overrides) != 0 || counters.RepairGrants != 0 {
		t.Fatalf("counters = %#v, want a note to have reached neither the overrides nor the budgets", counters)
	}
}

// triageCapsOf is the caps the harness assembles for this configuration, read
// the same way the conversation and the override command both read them.
func triageCapsOf(parts components) runstate.TriageCaps {
	return orchestrator.TriageCaps(parts.config.Execution, parts.config.Triage)
}

// developmentManagerSession is the conversation the command line opens for the
// role that decides about stopped work, wired to this product's own budgets and
// run records. Wiring it any other way would test a stand-in for the gate rather
// than the gate.
func developmentManagerSession(t *testing.T, parts components, provider chat.Backend, tracker chat.Tracker) *chat.Session {
	t.Helper()

	root := t.TempDir()
	store, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	reports, err := runstate.NewReportStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	session, err := chat.Open(chat.Options{
		Role:         domain.RoleDevelopmentManager,
		Agent:        string(domain.RoleDevelopmentManager),
		Backend:      provider,
		Store:        store,
		Reports:      reports,
		Tracker:      tracker,
		Triage:       conversationTriage(parts, domain.RoleDevelopmentManager),
		Stoppages:    conversationStoppages(parts, domain.RoleDevelopmentManager),
		Model:        "opus",
		Provider:     domain.BackendClaudeCode,
		AccountAlias: config.DefaultAccountAlias,
		Repository:   filepath.Join(root, "repository"),
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("chat.Open() error = %v", err)
	}
	return session
}

// sendTriageTurn takes one turn of the conversation and returns the single
// action it carried out or was refused.
func sendTriageTurn(t *testing.T, session *chat.Session, message string) chat.TrackerOutcome {
	t.Helper()

	reply, err := session.Send(context.Background(), message)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v, want the one decision the turn recorded", reply.Actions)
	}
	return reply.Actions[0]
}

// triageDecisionReply is one development manager's answer deciding one docketed
// stoppage, in the block the harness reads actions out of.
func triageDecisionReply(prose, decision, reason string) string {
	action := fmt.Sprintf(`{"action":"triage","id":%q,"run":%q,"decision":%q,"reason":%q}`,
		cappedItem, cappedRun, decision, reason)
	return prose + "\n\n```yoyodyne-tracker\n{\"actions\":[" + action + "]}\n```\n"
}

// escalationReply takes the refused decision up the escalation ladder: a
// blocker on the item and, in the same reply, a report at warning severity
// addressed to the Lead Product Manager, without which the harness refuses the
// escalation whole. Raising a cap is no reason on the operator's closed list, so
// the escalation names her rather than him.
func escalationReply() string {
	action := fmt.Sprintf(`{"action":"triage","id":%q,"run":%q,"decision":"escalate","to":"product-manager","reason":%q}`,
		cappedItem, cappedRun, "the round cap refuses every decision that would move it")
	return "This one needs the Lead Product Manager.\n\n```yoyodyne-tracker\n{\"actions\":[" + action + "]}\n```\n" +
		"\n```yoyodyne-report\n{\"reports\":[{\"severity\":\"warning\",\"for\":\"product-manager\",\"message\":\"" +
		cappedItem + " is past its review round cap; its last round was an approval and the promotion then conflicted.\"}]}\n```\n"
}

// scriptedChatBackend answers each turn with the next reply it was given, so one
// conversation can be carried through a sequence of turns the way an operator
// carries it.
//
// A turn that carried out actions asks the role once more, with what each action
// actually did, so every scripted reply is followed by an acknowledgement rather
// than by the next turn's answer. Threading that here keeps the sequence above
// readable as the sequence it is replaying.
type scriptedChatBackend struct {
	replies []string
	calls   int
}

func (b *scriptedChatBackend) Run(_ context.Context, _ backendapi.RunRequest) (backendapi.RunResult, error) {
	call := b.calls
	b.calls++
	reply := "That is what I decided."
	if call%2 == 0 {
		if call/2 >= len(b.replies) {
			return backendapi.RunResult{}, errors.New("the development manager was asked more turns than this test scripted")
		}
		reply = b.replies[call/2]
	}
	return backendapi.RunResult{
		SessionID:     "session-1",
		ResolvedModel: "claude-opus-5",
		Backend:       domain.BackendClaudeCode,
		FinalText:     reply,
	}, nil
}

// openItemTracker is a work tracker holding one open item, remembering what was
// written on it. What the notes hold is the point of one of these tests: an
// override written there reaches no guard.
type openItemTracker struct {
	id      string
	title   string
	notes   []string
	updates []beads.WorkItemChange
	blocked []string
}

func (t *openItemTracker) item() beads.WorkItem {
	return beads.WorkItem{ID: t.id, Title: t.title, Status: "open", Notes: strings.Join(t.notes, "\n")}
}

func (t *openItemTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	if id != t.id {
		return beads.WorkItem{}, fmt.Errorf("this tracker holds %s rather than %s", t.id, id)
	}
	return t.item(), nil
}

func (t *openItemTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	return []beads.WorkItem{t.item()}, nil
}

func (t *openItemTracker) Create(context.Context, beads.NewWorkItem) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("this test did not expect a creation")
}

func (t *openItemTracker) Update(_ context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error) {
	if id != t.id {
		return beads.WorkItem{}, fmt.Errorf("this tracker holds %s rather than %s", t.id, id)
	}
	t.updates = append(t.updates, change)
	if change.AppendNotes != "" {
		t.notes = append(t.notes, change.AppendNotes)
	}
	return t.item(), nil
}

func (t *openItemTracker) Block(_ context.Context, id, reason string) (beads.WorkItem, error) {
	if id != t.id {
		return beads.WorkItem{}, fmt.Errorf("this tracker holds %s rather than %s", t.id, id)
	}
	t.blocked = append(t.blocked, reason)
	return t.item(), nil
}

func (t *openItemTracker) Unblock(_ context.Context, id, note string) (beads.WorkItem, error) {
	if id != t.id {
		return beads.WorkItem{}, fmt.Errorf("this tracker holds %s rather than %s", t.id, id)
	}
	t.notes = append(t.notes, note)
	return t.item(), nil
}

func (t *openItemTracker) AddBlocker(context.Context, string, string) error    { return nil }
func (t *openItemTracker) RemoveBlocker(context.Context, string, string) error { return nil }

func (t *openItemTracker) Complete(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("this test did not expect a completion")
}

var _ chat.Tracker = (*openItemTracker)(nil)
