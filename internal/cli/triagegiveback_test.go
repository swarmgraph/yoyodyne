package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A process the harness launched for a role is refused the give-back, before
// any configuration or state is read: a claim given back is a re-run the
// harness then starts, and an agent freeing one is work deciding what work
// happens next.
func TestTriageGiveBackIsRefusedToAProcessLaunchedForARole(t *testing.T) {
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleDevelopmentManager} {
		t.Setenv(execution.AgentRoleVariable, string(role))
		var stdout, stderr bytes.Buffer
		code := Run([]string{"triage", "give-back", "--by", "someone", "--config", "/nonexistent/config.yaml",
			"run-0123456789abcdef0123456789abcdef"}, &stdout, &stderr, "test")
		if code != 1 || !strings.Contains(stderr.String(), "refused from a process the harness launched for the "+role.Title()) {
			t.Fatalf("%s: code = %d, stderr = %q; want it refused", role, code, stderr.String())
		}
	}
}

func TestTriageGiveBackRequiresTheRunItActsOn(t *testing.T) {
	t.Setenv(execution.AgentRoleVariable, "")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"triage", "give-back", "--by", "someone"}, &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "exactly one run identifier") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// What a give-back reports names whose decision it carried out and why, and
// says the re-run is outstanding again.
func TestTriageGiveBackReportsWhoseDecisionItCarriedOut(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	result := orchestrator.ClaimGiveBackResult{
		WorkItemID: "yoyodyne-ifd.435.1",
		PriorRunID: "run-78ab99a5abf224b52cdc013cd1aaec61",
		RunID:      "run-d234e83c6c426420648e95a01f9e5211",
		GivenBack:  true,
		Claim: runstate.Rerun{GivenBack: &runstate.RerunGivenBack{
			Decision: runstate.RerunClaimDecision{
				Decision: runstate.RerunClaimGiveBack, Reason: "the replacement died at its tracker claim",
				DecidedBy: "development manager", Conversation: "chat-e62712cfa48dbe18", Turn: 475, DecidedAt: at,
			},
			ClaimedAt: at.Add(-time.Hour), RunID: "run-d234e83c6c426420648e95a01f9e5211",
			By: "mason with yoyo triage give-back", At: at,
		}},
	}
	var stdout, stderr bytes.Buffer
	if code := reportGiveBack(&stdout, &stderr, false, result, nil); code != 0 {
		t.Fatalf("code = %d; stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"gave back the re-run claim of yoyodyne-ifd.435.1", "mason with yoyo triage give-back",
		"chat-e62712cfa48dbe18", "died at its tracker claim", "outstanding again"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, is missing %q", stdout.String(), want)
		}
	}
}
