package chat

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/health"
)

const healthRequest = "```yoyodyne-health\n" + `{"requests":[{"action":"observe"},{"action":"follow","observation":"health-20261016T150000Z-1a2b3c4d","cursor":"20"}]}` + "\n```"

var healthObservedAt = time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)

// fixedHealth answers every request the same way and records who asked, so
// two backends asked the same thing can be held to the same answer.
type fixedHealth struct {
	calls   int
	callers []health.Caller
}

func (f *fixedHealth) Observe(_ context.Context, caller health.Caller, runs []string) health.Observation {
	f.calls++
	f.callers = append(f.callers, caller)
	return health.Observation{ID: "health-20261016T150000Z-1a2b3c4d", Product: "yoyodyne", ObservedAt: healthObservedAt, RestrictedTo: runs,
		Complete: false, Says: "Partial: runs in flight could not be read whole, so this observation is not a healthy result whatever it lists.",
		Sources:  []health.Source{{Name: health.SourceRuns, Problem: "unreadable"}},
		Failures: []string{"runs in flight: unreadable"}, Totals: health.Totals{Conditions: 21, Actionable: 21},
		Anomalies: []health.Anomaly{{Key: "yoyodyne/run-1/no-process", Classification: health.ClassWorkerless, Actionable: true}},
		Omitted:   []string{"yoyodyne/run-2/no-process"}, Cursor: "20"}
}

func (f *fixedHealth) Follow(id, cursor string) (health.Part, error) {
	f.calls++
	return health.Part{Observation: id, ObservedAt: healthObservedAt, From: 20, Totals: health.Totals{Conditions: 21, Actionable: 21},
		Anomalies: []health.Anomaly{{Key: "yoyodyne/run-2/no-process", Classification: health.ClassWorkerless, Actionable: true}}}, nil
}

func healthToolOptions(t *testing.T, adapter domain.Backend, role domain.AgentRole, answers ...string) (Options, *fakeBackend, *fixedHealth) {
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
	observer := &fixedHealth{}
	options.HealthObserver = observer
	return options, provider, observer
}

// Both tools are reached by the tool dispatch a reply's block sets off, from an
// owning conversation turn and from a scheduled pass, on Claude Code and on
// Codex, and every one of the four hands back the same request and result
// fields. Every call is audited as requested and then performed.
func TestHealthToolsAreDispatchedFromTurnsAndPassesOnBothBackends(t *testing.T) {
	t.Parallel()

	var rounds []HealthRound
	for _, adapter := range []domain.Backend{domain.BackendClaudeCode, domain.BackendCodex} {
		for _, pass := range []string{"", "development-manager#7"} {
			options, provider, observer := healthToolOptions(t, adapter, domain.RoleDevelopmentManager, healthRequest, "Two workerless runs.")
			session := openTestSession(t, options)
			if pass != "" {
				session.ForPass(pass)
			}
			reply, err := session.Send(context.Background(), "How is the factory?")
			if err != nil {
				t.Fatalf("%s pass %q: Send() error = %v", adapter, pass, err)
			}
			if observer.calls != 2 || len(reply.HealthReads) != 1 || len(provider.requests) != 2 {
				t.Fatalf("%s pass %q: calls=%d rounds=%+v turns=%d", adapter, pass, observer.calls, reply.HealthReads, len(provider.requests))
			}
			if !reflect.DeepEqual(observer.callers, []health.Caller{{Role: domain.RoleDevelopmentManager}}) {
				t.Fatalf("%s pass %q: observed for %+v", adapter, pass, observer.callers)
			}
			handedBack := provider.requests[1].Prompt
			for _, want := range []string{`"action":"observe"`, `"cursor":"20"`, `"omitted":["yoyodyne/run-2/no-process"]`, `"action":"follow"`, `"from":20`, "Partial:", "untrusted evidence"} {
				if !strings.Contains(handedBack, want) {
					t.Fatalf("%s pass %q: the next round was not handed %s: %q", adapter, pass, want, handedBack)
				}
			}
			rounds = append(rounds, reply.HealthReads[0])
			var kinds []string
			for _, audit := range toolEvents(t, options.Store, session.state.ConversationID) {
				kinds = append(kinds, audit.ID+" "+string(audit.Tool))
				if audit.Pass != pass || audit.Role != domain.RoleDevelopmentManager {
					t.Fatalf("%s pass %q: audit = %+v", adapter, pass, audit)
				}
			}
			want := []string{"tool.requested health.observe", "tool.performed health.observe", "tool.requested health.follow", "tool.performed health.follow"}
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

// The development manager, the factory-flow program manager, and the architect
// hold both tools. Every other role's call is refused before the observer is
// asked, and the refusal is audited; so is a program manager watching another
// lane, which holds the grant through its role and is refused by the tools'
// scope.
func TestAHealthToolCallWithoutTheGrantIsRefused(t *testing.T) {
	t.Parallel()

	observe := "```yoyodyne-health\n" + `{"requests":[{"action":"observe"}]}` + "\n```"
	follow := "```yoyodyne-health\n" + `{"requests":[{"action":"follow","observation":"health-20261016T150000Z-1a2b3c4d","cursor":"20"}]}` + "\n```"
	for _, refusal := range []struct {
		role   domain.AgentRole
		answer string
		tool   capability.Capability
	}{
		{domain.RoleProductManager, observe, capability.HealthObserve},
		{domain.RoleProductManager, follow, capability.HealthFollow},
		{domain.RoleDeveloper, observe, capability.HealthObserve},
		{domain.RoleDeveloper, follow, capability.HealthFollow},
		{domain.RoleReviewer, observe, capability.HealthObserve},
		{domain.RoleReviewer, follow, capability.HealthFollow},
	} {
		options, _, observer := healthToolOptions(t, domain.BackendClaudeCode, refusal.role, refusal.answer)
		session := openTestSession(t, options)
		if _, err := session.Send(context.Background(), "how is the factory"); err == nil || !strings.Contains(err.Error(), "grant") {
			t.Errorf("the %s's %s was not refused for want of the grant: %v", refusal.role, refusal.tool, err)
		}
		if observer.calls != 0 {
			t.Errorf("the %s's ungranted %s reached the observer", refusal.role, refusal.tool)
		}
		audits := toolEvents(t, options.Store, session.state.ConversationID)
		if len(audits) != 2 || audits[0].ID != string(execution.EventToolRequested) || audits[1].ID != string(execution.EventToolRefused) || audits[1].Tool != refusal.tool {
			t.Errorf("the %s's refused %s was audited as %+v", refusal.role, refusal.tool, audits)
		}
	}

	for _, answer := range []string{observe, follow} {
		options, _, observer := healthToolOptions(t, domain.BackendClaudeCode, domain.RoleProgramManager, answer, "Refused.")
		options.Lane = "reliability"
		reply, err := openTestSession(t, options).Send(context.Background(), "how is the factory")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if observer.calls != 0 || len(reply.HealthReads) != 1 || !strings.Contains(reply.HealthReads[0].Results[0].Problem, "factory-flow") {
			t.Fatalf("a program manager on another lane observed the factory: calls=%d rounds=%+v", observer.calls, reply.HealthReads)
		}
	}
}

// The calls the grants allow reach the observer, and a program manager is
// observed for under its instance's name.
func TestTheGrantedRolesReachTheHealthObserver(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		role   domain.AgentRole
		lane   string
		agent  string
		caller health.Caller
	}{
		{domain.RoleArchitect, "", "architect", health.Caller{Role: domain.RoleArchitect}},
		{domain.RoleProgramManager, "factory-flow", "flow-watcher", health.Caller{Role: domain.RoleProgramManager, Agent: "flow-watcher"}},
		{domain.RoleDevelopmentManager, "", "development-manager", health.Caller{Role: domain.RoleDevelopmentManager}},
	} {
		options, _, observer := healthToolOptions(t, domain.BackendCodex, c.role, healthRequest, "Read.")
		options.Lane, options.Agent = c.lane, c.agent
		reply, err := openTestSession(t, options).Send(context.Background(), "how is the factory")
		if err != nil {
			t.Fatalf("the %s: Send() error = %v", c.role, err)
		}
		if observer.calls != 2 || len(reply.HealthReads) != 1 || !reflect.DeepEqual(observer.callers, []health.Caller{c.caller}) {
			t.Fatalf("the %s: calls=%d callers=%+v rounds=%+v", c.role, observer.calls, observer.callers, reply.HealthReads)
		}
		for _, result := range reply.HealthReads[0].Results {
			if result.Problem != "" {
				t.Fatalf("the %s was refused: %s", c.role, result.Problem)
			}
		}
	}
}

// An observation of another product is refused, a follow the observer refuses
// is said as refused, and neither fails the turn.
func TestAHealthRequestTheObserverCannotAnswerIsRefusedPlainly(t *testing.T) {
	t.Parallel()

	answer := "```yoyodyne-health\n" + `{"requests":[{"action":"observe","product":"another"},{"action":"follow","observation":"health-20261016T150000Z-1a2b3c4d","cursor":"20"}]}` + "\n```"
	options, _, _ := healthToolOptions(t, domain.BackendClaudeCode, domain.RoleDevelopmentManager, answer, "Noted.")
	options.HealthObserver = refusingHealth{}
	reply, err := openTestSession(t, options).Send(context.Background(), "how is the factory")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	results := reply.HealthReads[0].Results
	if len(results) != 2 || !strings.Contains(results[0].Problem, "not another") || !strings.Contains(results[1].Problem, "observe again") {
		t.Fatalf("results = %+v", results)
	}
}

type refusingHealth struct{}

func (refusingHealth) Observe(context.Context, health.Caller, []string) health.Observation {
	return health.Observation{}
}

func (refusingHealth) Follow(string, string) (health.Part, error) {
	return health.Part{}, errors.New("observation health-20261016T150000Z-1a2b3c4d was taken more than 15m0s ago: observe again")
}
