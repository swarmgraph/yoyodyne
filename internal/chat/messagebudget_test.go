package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/budget"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/health"
	"github.com/mason-bryant/yoyodyne/internal/logread"
	"github.com/mason-bryant/yoyodyne/internal/repositoryread"
	"github.com/mason-bryant/yoyodyne/internal/research"
	"github.com/mason-bryant/yoyodyne/internal/runinspect"
	"github.com/mason-bryant/yoyodyne/internal/runrecover"
)

// neverStops is a provider whose every answer asks for the same thing again.
// It holds far more answers than any message may take, so a loop that failed to
// stop would show as extra turns rather than as a provider out of answers.
func neverStops(ask string) *fakeBackend {
	results := make([]backendapi.RunResult, 40)
	for i := range results {
		results[i] = backendapi.RunResult{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Let me look again.\n\n" + ask}
	}
	return &fakeBackend{results: results}
}

const (
	researchAsk   = research.Fence + "\n" + `{"queries":[{"source":"web","question":"q","why":"w"}]}` + "\n```"
	repositoryAsk = repositoryread.Fence + "\n" + `{"requests":[{"action":"read","path":"a.md"}]}` + "\n```"
)

// A model that never stops asking, whichever tool it asks for, is answered up to
// that tool's own limit, given one closing turn, and then stopped: the message
// ends within its budget, the stop is recorded on the conversation's log, and
// the reply says why in ordinary words. Every tool reaches the same decision.
func TestAModelThatNeverStopsAskingEndsWithinTheMessageBudget(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		ask   string
		limit int
		tool  string
		setup func(*Options)
	}{
		{name: "research", ask: researchAsk, limit: maxResearchRounds, tool: toolResearch, setup: func(options *Options) {
			options.Research = &fakeResearch{
				policy:   research.Policy{Sources: []research.Source{{Name: "web", Command: "search"}}},
				findings: []research.Finding{{Source: "web", Question: "q", RetrievedAt: fixedClock{}.Now(), Evidence: "something"}},
			}
		}},
		{name: "repository", ask: repositoryAsk, limit: maxRepositoryRounds, tool: toolRepository, setup: func(options *Options) {
			options.RepositoryReader = &fakeRepositoryReader{results: []repositoryread.Result{{
				Action: repositoryread.ActionRead, Path: "a.md", Commit: "0123456789abcdef0123456789abcdef01234567",
				ReadAt: time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC), Content: "a", Size: 1,
			}}}
		}},
		{name: "logs", ask: logRequest, limit: logread.MaxRoundsPerMessage, tool: toolLogs, setup: func(options *Options) {
			options.Role, options.Agent = domain.RoleProgramManager, string(domain.RoleProgramManager)
			options.LogReader = &countedLogReader{}
		}},
		{name: "runs", ask: runReadRequest, limit: runinspect.MaxRoundsPerMessage, tool: toolRuns, setup: func(options *Options) {
			options.Role, options.Agent = domain.RoleDevelopmentManager, string(domain.RoleDevelopmentManager)
			options.RunInspector = &fixedRunInspector{}
		}},
		{name: "health", ask: healthRequest, limit: health.MaxRoundsPerMessage, tool: toolHealth, setup: func(options *Options) {
			options.Role, options.Agent = domain.RoleDevelopmentManager, string(domain.RoleDevelopmentManager)
			options.HealthObserver = &fixedHealth{}
		}},
		{name: "recovery", ask: recoverRequest, limit: runrecover.MaxRoundsPerMessage, tool: toolRecovery, setup: func(options *Options) {
			options.Role, options.Agent = domain.RoleDevelopmentManager, string(domain.RoleDevelopmentManager)
			options.RunInspector = &fixedRunInspector{}
			options.RunRecoverer = &fixedRecoverer{}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider := neverStops(test.ask)
			options := testOptions(t, provider)
			test.setup(&options)
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "Keep looking.")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			// The tool's own rounds, then the one closing turn, and nothing more.
			if want := test.limit + 1; len(provider.requests) != want {
				t.Fatalf("provider turns = %d, want %d", len(provider.requests), want)
			}
			if !strings.Contains(provider.requests[test.limit].Prompt, "This is the last turn this message may take") {
				t.Fatalf("the closing turn was not told it was the last: %q", provider.requests[test.limit].Prompt)
			}
			want := "The harness stopped answering this message: " + test.tool + " reached the most one message may do, and the turn after that was its last."
			if reply.Stopped != want {
				t.Fatalf("reply.Stopped = %q, want %q", reply.Stopped, want)
			}
			stopped := messageStoppedEvents(t, options.Store, session.state.ConversationID)
			if len(stopped) != 1 || stopped[0]["tool_limit"] != test.tool || stopped[0]["turns"] != float64(test.limit+1) {
				t.Fatalf("message.stopped events = %+v", stopped)
			}
		})
	}
}

// A model that asks for several tools at once is stopped by whichever reaches
// its limit first: the limits do not add up into more turns for one message.
func TestAModelAskingForEveryToolAtOnceTakesOneClosingTurn(t *testing.T) {
	t.Parallel()
	provider := neverStops(runReadRequest + "\n\n" + healthRequest + "\n\n" + recoverRequest)
	options := testOptions(t, provider)
	options.Role, options.Agent = domain.RoleDevelopmentManager, string(domain.RoleDevelopmentManager)
	options.RunInspector = &fixedRunInspector{}
	options.HealthObserver = &fixedHealth{}
	options.RunRecoverer = &fixedRecoverer{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Keep looking.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	// Run reads and recoveries both reach their limit of two on the second
	// round; health would allow a third, but the closing turn is the last.
	if len(provider.requests) != 3 || len(reply.HealthReads) != 3 || reply.HealthReads[2].Problem != "" {
		t.Fatalf("provider turns = %d, health rounds = %+v", len(provider.requests), reply.HealthReads)
	}
	if !strings.Contains(reply.Stopped, toolRuns+" reached the most one message may do") {
		t.Fatalf("reply.Stopped = %q", reply.Stopped)
	}
	// What the closing turn's health read returned is owed to the role, so it
	// is kept for its next turn rather than dropped.
	if !reply.ResultsCarriedOver || !strings.Contains(session.state.PendingTrackerResults, "# Factory health") {
		t.Fatalf("carried over = %v, pending = %q", reply.ResultsCarriedOver, session.state.PendingTrackerResults)
	}
}

// The message's own turn limit stops a model that never reaches any tool's
// limit, and the configured limit is the one applied.
func TestTheMessageTurnLimitStopsAModelBeforeAnyToolLimit(t *testing.T) {
	t.Parallel()
	provider := neverStops(healthRequest)
	options := testOptions(t, provider)
	options.Role, options.Agent = domain.RoleDevelopmentManager, string(domain.RoleDevelopmentManager)
	options.HealthObserver = &fixedHealth{}
	options.MessageBudget = budget.Limits{Turns: 2}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Keep looking.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider turns = %d, want 2", len(provider.requests))
	}
	if reply.Stopped != "The harness stopped answering this message: this message has taken 2 provider turns, the most one message may take." {
		t.Fatalf("reply.Stopped = %q", reply.Stopped)
	}
	if stopped := messageStoppedEvents(t, options.Store, session.state.ConversationID); len(stopped) != 1 {
		t.Fatalf("message.stopped events = %+v", stopped)
	}
}

// A role that finishes on its own is not reported as stopped.
func TestAMessageTheRoleFinishesIsNotReportedStopped(t *testing.T) {
	t.Parallel()
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: healthRequest},
		{SessionID: "session-1", FinalText: "Two workerless runs."},
	}}
	options := testOptions(t, provider)
	options.Role, options.Agent = domain.RoleDevelopmentManager, string(domain.RoleDevelopmentManager)
	options.HealthObserver = &fixedHealth{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "How is the factory?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.requests) != 2 || reply.Stopped != "" {
		t.Fatalf("turns = %d, stopped = %q", len(provider.requests), reply.Stopped)
	}
	if stopped := messageStoppedEvents(t, options.Store, session.state.ConversationID); len(stopped) != 0 {
		t.Fatalf("message.stopped events = %+v", stopped)
	}
}

func messageStoppedEvents(t *testing.T, store Store, conversation string) []map[string]any {
	t.Helper()
	events, err := store.LoadEvents(conversation)
	if err != nil {
		t.Fatal(err)
	}
	var stopped []map[string]any
	for _, event := range events {
		if event.Type != execution.EventMessageStopped {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		stopped = append(stopped, payload)
	}
	return stopped
}

// A message the budget stops after a single tracker action keeps the action's
// results for the next turn, and the operator is told the real reason it
// stopped: nothing says the tracker ran out of rounds when it did not.
func TestABudgetStopAfterOneTrackerActionIsNotSaidToBeTheTrackers(t *testing.T) {
	t.Parallel()
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Reading it.", `{"action":"read","id":"yoyodyne-ifd.22"}`)},
		{SessionID: "session-1", FinalText: "That is what it says."},
	}}
	options := testOptions(t, provider)
	options.Tracker = &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.22": {ID: "yoyodyne-ifd.22", Title: "Readable conversations", Description: "Say who is speaking.", Status: "open"},
	}}
	options.MessageBudget = budget.Limits{Turns: 1}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tell me about ifd.22.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.requests) != 1 || len(reply.Actions) != 1 {
		t.Fatalf("turns = %d, actions = %d", len(provider.requests), len(reply.Actions))
	}
	if !reply.ResultsCarriedOver || reply.TrackerRoundsSpent {
		t.Fatalf("carried over = %v, tracker rounds spent = %v", reply.ResultsCarriedOver, reply.TrackerRoundsSpent)
	}
	if !strings.Contains(reply.Stopped, "this message has taken 1 provider turn,") {
		t.Fatalf("reply.Stopped = %q", reply.Stopped)
	}
	var said strings.Builder
	session.reportTrackerActions(&said, reply)
	if strings.Contains(said.String(), "rounds of actions") {
		t.Fatalf("the operator was told the tracker ran out of rounds: %q", said.String())
	}
}
