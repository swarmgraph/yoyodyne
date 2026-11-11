package chat

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// autonomyGoal is the autonomy goal in the words v1-goals states it in. It is
// longer than a concern's subject may be, which is the case these tests exist
// for: a concern naming it in full has to be accepted.
const autonomyGoal = "Run development nearly autonomously. The human's routine interface is the product manager: they state intent, approve the brief and goals, and answer questions the product manager escalates. Directing the architect, development manager, developer, or reviewer individually is available for inspection, recovery, and override, but is not part of the normal loop."

func concernAbout(t *testing.T, goalNamed string) string {
	t.Helper()
	encoded, err := json.Marshal(Concern{
		Kind:     ConcernConflict,
		Subject:  "Ask the operator to approve every merge",
		Goal:     goalNamed,
		Detail:   "It would put him back in the loop for routine work.",
		Question: "Do you want it anyway?",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestAConcernMayNameALongGoalInFull(t *testing.T) {
	t.Parallel()

	if len(autonomyGoal) <= maxConcernSubjectBytes {
		t.Fatalf("the autonomy goal is %d bytes, so it no longer tests a goal longer than a subject", len(autonomyGoal))
	}
	concern := Concern{Kind: ConcernConflict, Subject: "s", Goal: autonomyGoal, Detail: "d", Question: "q?"}
	if err := concern.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want a concern naming the autonomy goal in full accepted", err)
	}
	// The bound is the one every goal statement is held to, and no looser.
	concern.Goal = strings.Repeat("g", goal.MaxStatementBytes+1)
	if err := concern.Validate(); err == nil || !strings.Contains(err.Error(), "limit is "+strconv.Itoa(goal.MaxStatementBytes)) {
		t.Fatalf("Validate() = %v, want a goal over %d bytes refused", err, goal.MaxStatementBytes)
	}
	// The role is told the bound that is enforced.
	if maxConcernGoalBytesText != strconv.Itoa(maxConcernGoalBytes) {
		t.Fatalf("contract states a goal bound of %s, enforced bound is %d", maxConcernGoalBytesText, maxConcernGoalBytes)
	}
	if !strings.Contains(SystemPrompt(domain.RoleProductManager, Admission{}, nil, hostilePersona), "at most "+maxConcernGoalBytesText+" bytes") {
		t.Fatal("the contract does not state the concern goal's bound")
	}
}

func TestAConcernNamingARecordedGoalIsPutToTheOperator(t *testing.T) {
	t.Parallel()

	for _, named := range []string{autonomyGoal, "[nearly-autonomous]", "[nearly-autonomous] " + autonomyGoal} {
		t.Run(named[:min(len(named), 24)], func(t *testing.T) {
			t.Parallel()
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: concernReply("This cuts against a goal.", concernAbout(t, named))},
			}}
			options := testOptions(t, provider)
			options.Tracker = &fakeTracker{}
			options.Goals = recordedGoalsWithIdentity("nearly-autonomous", autonomyGoal)

			reply, err := openTestSession(t, options).Send(context.Background(), "should every merge wait for me?")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(reply.Concerns) != 1 || reply.Concerns[0].Concern.Goal != named || len(reply.ConcernRefusals) != 0 {
				t.Fatalf("concerns = %#v, refusals = %#v, want the concern raised", reply.Concerns, reply.ConcernRefusals)
			}
		})
	}
}

func TestAConcernNamingAGoalNothingRecordsIsHandedBackToTheRole(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: concernReply("This cuts against a goal.", concernAbout(t, "Grow the ecosystem."))},
		{SessionID: "session-1", FinalText: "I named a goal nobody recorded; I will raise it again."},
	}}
	options := testOptions(t, provider)
	options.Tracker = &fakeTracker{}
	options.Goals = recordedGoalsWithIdentity("nearly-autonomous", autonomyGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "should every merge wait for me?")
	// A refused concern is a result for the role, not a failed turn.
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	// Nothing reaches the operator.
	if len(reply.Concerns) != 0 || len(session.Concerns()) != 0 {
		t.Fatalf("concerns = %#v, want none put to the operator", reply.Concerns)
	}
	if len(reply.ConcernRefusals) != 1 || !strings.Contains(reply.ConcernRefusals[0], `"Grow the ecosystem."`) {
		t.Fatalf("refusals = %#v, want one naming the goal that was not found", reply.ConcernRefusals)
	}
	// The refusal is handed back to the role within the same message, saying
	// which goal was not found.
	if len(provider.requests) != 2 {
		t.Fatalf("provider was asked %d times, want the refusal handed back once", len(provider.requests))
	}
	handedBack := provider.requests[1].Prompt
	for _, required := range []string{"# Concerns refused", `"Grow the ecosystem."`, "no goal recorded in v1-goals"} {
		if !strings.Contains(handedBack, required) {
			t.Fatalf("handed-back prompt does not say %q:\n%s", required, handedBack)
		}
	}
}
