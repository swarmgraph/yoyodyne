package chat

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runinspect"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const runReadRequest = "```yoyodyne-run\n" + `{"requests":[{"action":"inspect","run":"run-1a2b3c4d"},{"action":"verify","run":"run-1a2b3c4d","expect":"worker live"}]}` + "\n```"

var runObservedAt = time.Date(2026, 10, 15, 16, 30, 0, 0, time.UTC)

// fixedRunInspector answers every request the same way and counts the calls,
// so two backends asked the same thing can be held to the same answer.
type fixedRunInspector struct{ calls int }

func (f *fixedRunInspector) Inspect(_ context.Context, runID string) runinspect.Inspection {
	f.calls++
	return runinspect.Inspection{ObservedAt: runObservedAt, Record: `{"run_id":"` + runID + `"}`, RecordBytes: 25, Status: runstate.StatusRunning,
		Worker: runstate.WorkerReading{Liveness: runstate.WorkerLive, Says: "attempt 2's worker holds the run's lease", ObservedAt: runObservedAt}}
}

func (f *fixedRunInspector) Verify(_ context.Context, _ string, expect runinspect.Expected) runinspect.Verification {
	f.calls++
	return runinspect.Verification{Expect: expect, Verdict: runinspect.Met, ObservedAt: runObservedAt, Evidence: []string{"attempt 2's worker holds the run's lease"}}
}

func runToolOptions(t *testing.T, adapter domain.Backend, role domain.AgentRole, answers ...string) (Options, *fakeBackend, *fixedRunInspector) {
	t.Helper()
	var results []backendapi.RunResult
	for _, answer := range answers {
		results = append(results, backendapi.RunResult{Backend: adapter, SessionID: "session-1", FinalText: answer})
	}
	provider := &fakeBackend{results: results}
	options := testOptions(t, provider)
	if adapter == domain.BackendCodex {
		options.Provider, options.Model = domain.BackendCodex, "gpt-6.1-sol"
	}
	options.Role, options.Agent = role, string(role)
	inspector := &fixedRunInspector{}
	options.RunInspector = inspector
	return options, provider, inspector
}

func toolEvents(t *testing.T, store Store, conversation string) []ToolAudit {
	t.Helper()
	events, err := store.LoadEvents(conversation)
	if err != nil {
		t.Fatal(err)
	}
	var audits []ToolAudit
	for _, event := range events {
		if !strings.HasPrefix(string(event.Type), "tool.") {
			continue
		}
		var audit ToolAudit
		if err := json.Unmarshal(event.Payload, &audit); err != nil {
			t.Fatal(err)
		}
		audit.ID = string(event.Type)
		audits = append(audits, audit)
	}
	return audits
}

// Both tools are reached by the tool dispatch a reply's block sets off, from an
// owning conversation turn and from a scheduled pass, on Claude Code and on
// Codex, and every one of the four hands back the same request and result
// fields. Every call is audited as requested and then performed.
func TestRunToolsAreDispatchedFromTurnsAndPassesOnBothBackends(t *testing.T) {
	t.Parallel()

	var rounds []RunRound
	for _, adapter := range []domain.Backend{domain.BackendClaudeCode, domain.BackendCodex} {
		for _, pass := range []string{"", "development-manager#7"} {
			options, provider, inspector := runToolOptions(t, adapter, domain.RoleDevelopmentManager, runReadRequest, "The worker is live.")
			session := openTestSession(t, options)
			if pass != "" {
				session.ForPass(pass)
			}
			reply, err := session.Send(context.Background(), "Is run-1a2b3c4d's worker live?")
			if err != nil {
				t.Fatalf("%s pass %q: Send() error = %v", adapter, pass, err)
			}
			if inspector.calls != 2 || len(reply.RunReads) != 1 || len(provider.requests) != 2 {
				t.Fatalf("%s pass %q: calls=%d rounds=%+v turns=%d", adapter, pass, inspector.calls, reply.RunReads, len(provider.requests))
			}
			handedBack := provider.requests[1].Prompt
			for _, want := range []string{`"action":"inspect"`, `"liveness":"live"`, `"verdict":"met"`, "untrusted evidence"} {
				if !strings.Contains(handedBack, want) {
					t.Fatalf("%s pass %q: the next round was not handed %s: %q", adapter, pass, want, handedBack)
				}
			}
			rounds = append(rounds, reply.RunReads[0])
			var kinds []string
			for _, audit := range toolEvents(t, options.Store, session.state.ConversationID) {
				kinds = append(kinds, audit.ID+" "+string(audit.Tool))
				if audit.Pass != pass || audit.Role != domain.RoleDevelopmentManager || audit.Parameters["run"] != "run-1a2b3c4d" {
					t.Fatalf("%s pass %q: audit = %+v", adapter, pass, audit)
				}
			}
			want := []string{"tool.requested run.inspect", "tool.performed run.inspect", "tool.requested run.verify", "tool.performed run.verify"}
			if !reflect.DeepEqual(kinds, want) {
				t.Fatalf("%s pass %q: audit = %v, want %v", adapter, pass, kinds, want)
			}
		}
	}
	for _, round := range rounds[1:] {
		if !reflect.DeepEqual(round, rounds[0]) {
			t.Fatalf("the request and result fields differ between backends or invokers:\n%+v\n%+v", round, rounds[0])
		}
	}
}

// The development manager and the factory-flow program manager hold both
// tools, and the architect holds run.inspect alone. Every other call is
// refused before the inspector is asked, and the refusal is audited.
func TestARunToolCallWithoutTheGrantIsRefused(t *testing.T) {
	t.Parallel()

	inspect := "```yoyodyne-run\n" + `{"requests":[{"action":"inspect","run":"run-1a2b3c4d"}]}` + "\n```"
	verify := "```yoyodyne-run\n" + `{"requests":[{"action":"verify","run":"run-1a2b3c4d","expect":"reservation released"}]}` + "\n```"
	refusals := []struct {
		role   domain.AgentRole
		lane   string
		answer string
		tool   capability.Capability
	}{
		{domain.RoleArchitect, "", verify, capability.RunVerify},
		{domain.RoleProductManager, "", inspect, capability.RunInspect},
		{domain.RoleProductManager, "", verify, capability.RunVerify},
		{domain.RoleDeveloper, "", inspect, capability.RunInspect},
		{domain.RoleDeveloper, "", verify, capability.RunVerify},
		{domain.RoleReviewer, "", inspect, capability.RunInspect},
		{domain.RoleReviewer, "", verify, capability.RunVerify},
	}
	for _, refusal := range refusals {
		options, _, inspector := runToolOptions(t, domain.BackendClaudeCode, refusal.role, refusal.answer)
		session := openTestSession(t, options)
		if _, err := session.Send(context.Background(), "look at the run"); err == nil || !strings.Contains(err.Error(), "grant") {
			t.Errorf("the %s's %s was not refused for want of the grant: %v", refusal.role, refusal.tool, err)
		}
		if inspector.calls != 0 {
			t.Errorf("the %s's ungranted %s reached the inspector", refusal.role, refusal.tool)
		}
		audits := toolEvents(t, options.Store, session.state.ConversationID)
		if len(audits) != 2 || audits[0].ID != string(execution.EventToolRequested) || audits[1].ID != string(execution.EventToolRefused) || audits[1].Tool != refusal.tool {
			t.Errorf("the %s's refused %s was audited as %+v", refusal.role, refusal.tool, audits)
		}
	}

	// A program manager watching another lane holds the grant through its role
	// and is refused by the tools' scope, each request audited as refused.
	for _, answer := range []string{inspect, verify} {
		options, _, inspector := runToolOptions(t, domain.BackendClaudeCode, domain.RoleProgramManager, answer, "Refused.")
		options.Lane = "reliability"
		session := openTestSession(t, options)
		reply, err := session.Send(context.Background(), "look at the run")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if inspector.calls != 0 || len(reply.RunReads) != 1 || !strings.Contains(reply.RunReads[0].Results[0].Problem, "factory-flow") {
			t.Fatalf("a program manager on another lane read a run: calls=%d rounds=%+v", inspector.calls, reply.RunReads)
		}
		audits := toolEvents(t, options.Store, session.state.ConversationID)
		if len(audits) != 2 || audits[1].ID != string(execution.EventToolRefused) {
			t.Fatalf("the out-of-lane read was audited as %+v", audits)
		}
	}
}

// The calls the grants allow reach the inspector: the architect's inspect, and
// both of the factory-flow program manager's.
func TestTheGrantedRolesReachTheInspector(t *testing.T) {
	t.Parallel()

	inspect := "```yoyodyne-run\n" + `{"requests":[{"action":"inspect","run":"run-1a2b3c4d"}]}` + "\n```"
	for _, c := range []struct {
		role   domain.AgentRole
		lane   string
		answer string
		calls  int
	}{
		{domain.RoleArchitect, "", inspect, 1},
		{domain.RoleProgramManager, "factory-flow", runReadRequest, 2},
		{domain.RoleDevelopmentManager, "", runReadRequest, 2},
	} {
		options, _, inspector := runToolOptions(t, domain.BackendCodex, c.role, c.answer, "Read.")
		options.Lane = c.lane
		reply, err := openTestSession(t, options).Send(context.Background(), "look at the run")
		if err != nil {
			t.Fatalf("the %s: Send() error = %v", c.role, err)
		}
		if inspector.calls != c.calls || len(reply.RunReads) != 1 {
			t.Fatalf("the %s: calls=%d rounds=%+v", c.role, inspector.calls, reply.RunReads)
		}
		for _, result := range reply.RunReads[0].Results {
			if result.Problem != "" {
				t.Fatalf("the %s was refused: %s", c.role, result.Problem)
			}
		}
	}
}

// A request naming an expected state off the closed list refuses the block
// whole, and the inspector is never asked.
func TestAnExpectedStateOffTheListRefusesTheBlock(t *testing.T) {
	t.Parallel()

	answer := "```yoyodyne-run\n" + `{"requests":[{"action":"verify","run":"run-1a2b3c4d","expect":"recovered"}]}` + "\n```"
	options, _, inspector := runToolOptions(t, domain.BackendClaudeCode, domain.RoleDevelopmentManager, answer)
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "check it"); err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("Send() error = %v, want the expected state refused", err)
	}
	if inspector.calls != 0 {
		t.Fatal("a refused block reached the inspector")
	}
	audits := toolEvents(t, options.Store, session.state.ConversationID)
	if len(audits) != 2 || audits[1].ID != string(execution.EventToolRefused) {
		t.Fatalf("the refused block was audited as %+v", audits)
	}
}
