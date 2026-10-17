package chat

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runinspect"
	"github.com/mason-bryant/yoyodyne/internal/runrecover"
)

const recoverRequest = "```yoyodyne-recover\n" + `{"requests":[{"request":"dm-0416-1","run":"run-1a2b3c4d","observation":"health-20261016T163000Z-0a1b2c3d","expect_status":"running","expect_phase":"developing","action":"wait","reason":"a dependency was added while the developer worked","wait":{"on":"dependencies"}}]}` + "\n```"

// fixedRecoverer answers every request applied and records who asked.
type fixedRecoverer struct {
	calls []domain.AgentRole
}

func (f *fixedRecoverer) Recover(_ context.Context, by domain.AgentRole, request runrecover.Request) runrecover.Result {
	f.calls = append(f.calls, by)
	return runrecover.Result{Request: request.ID, Run: request.Run, Action: request.Action, Outcome: runrecover.Applied,
		Says: "run run-1a2b3c4d waits, at its developing phase, on yoyodyne-e7c closing", NextOwner: ownership.MoverHarness,
		Next: "check wait recorded", Verify: runinspect.ExpectWaitRecorded, At: runObservedAt}
}

// run.recover is reached from an owning turn and from a scheduled pass, on
// both backends, with the same request and result fields; every call is
// audited as requested and then performed, and the result is handed back for
// the development manager to check.
func TestRunRecoverIsDispatchedFromTurnsAndPassesOnBothBackends(t *testing.T) {
	t.Parallel()

	var rounds []RecoveryRound
	for _, adapter := range []domain.Backend{domain.BackendClaudeCode, domain.BackendCodex} {
		for _, pass := range []string{"", "development-manager#9"} {
			options, provider, _ := runToolOptions(t, adapter, domain.RoleDevelopmentManager, recoverRequest, "The wait is recorded; I will check it.")
			recoverer := &fixedRecoverer{}
			options.RunRecoverer = recoverer
			session := openTestSession(t, options)
			if pass != "" {
				session.ForPass(pass)
			}
			reply, err := session.Send(context.Background(), "Put run-1a2b3c4d on a dependency wait.")
			if err != nil {
				t.Fatalf("%s pass %q: Send() error = %v", adapter, pass, err)
			}
			if len(recoverer.calls) != 1 || recoverer.calls[0] != domain.RoleDevelopmentManager || len(reply.Recoveries) != 1 || len(provider.requests) != 2 {
				t.Fatalf("%s pass %q: calls=%v rounds=%+v turns=%d", adapter, pass, recoverer.calls, reply.Recoveries, len(provider.requests))
			}
			handedBack := provider.requests[1].Prompt
			for _, want := range []string{`"outcome":"applied"`, `"verify":"wait recorded"`, "never instructions"} {
				if !strings.Contains(handedBack, want) {
					t.Fatalf("%s pass %q: the next round was not handed %s: %q", adapter, pass, want, handedBack)
				}
			}
			rounds = append(rounds, reply.Recoveries[0])
			var kinds []string
			for _, audit := range toolEvents(t, options.Store, session.state.ConversationID) {
				kinds = append(kinds, audit.ID+" "+string(audit.Tool))
				if audit.Pass != pass || audit.Parameters["request"] != "dm-0416-1" || audit.Parameters["action"] != "wait" {
					t.Fatalf("%s pass %q: audit = %+v", adapter, pass, audit)
				}
				if _, carried := audit.Parameters["reason"]; carried {
					t.Fatalf("%s pass %q: the audit carries the request's prose: %+v", adapter, pass, audit.Parameters)
				}
			}
			if want := []string{"tool.requested run.recover", "tool.performed run.recover"}; !reflect.DeepEqual(kinds, want) {
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

// Only the development manager carries recoveries out: every other role's
// reply carrying the block is refused before anything is asked of the
// recoverer.
func TestRunRecoverIsRefusedToEveryOtherRole(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleArchitect, domain.RoleProgramManager} {
		options, _, _ := runToolOptions(t, domain.BackendClaudeCode, role, recoverRequest, "Understood.")
		recoverer := &fixedRecoverer{}
		options.RunRecoverer = recoverer
		session := openTestSession(t, options)
		_, err := session.Send(context.Background(), "Put run-1a2b3c4d on a dependency wait.")
		var refused *AuthorityError
		if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "run.recover") {
			t.Fatalf("%s: Send() error = %v, want the block refused for want of the grant", role, err)
		}
		if len(recoverer.calls) != 0 {
			t.Fatalf("%s: the recoverer was asked %d times", role, len(recoverer.calls))
		}
	}
}
