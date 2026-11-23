package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The development manager decides on the trial of starting developers on the
// cheaper model, so every pass of hers carries its figures while the trial has
// anything to say; a program manager's pass is handed none of it.
func TestADevelopmentManagerPassCarriesTheModelTrialAndNoOtherRolesDoes(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(time.Hour, config.TriggerStoppages))
	h.trigger.Tasks = map[string]config.RecurringTask{"triage": {Enabled: true, Role: domain.RoleDevelopmentManager, Every: config.Duration(time.Hour), Prompt: "triage"}}
	h.trigger.ModelTrial = func() string {
		return "# The trial of starting developers on the cheaper model\n\n3 of at least 40 trial runs so far."
	}
	h.trigger.ConflictFiles = func() string { return "# The files changes conflicted in\n\nstatus.go in 2" }
	for range 4 {
		h.role.answers = append(h.role.answers, scriptedTurn{result: complete("looked")})
	}
	for range 3 {
		if taken := h.fire(t, recurringNow); len(taken.Fired) == 0 {
			break
		}
	}
	carried := map[string]string{}
	for i, message := range h.role.messages {
		carried[h.role.agents[i]] = message
	}
	if !strings.Contains(carried[""], "3 of at least 40 trial runs so far.") {
		t.Fatalf("the development manager's pass was not handed the trial:\n%s", carried[""])
	}
	if !strings.Contains(carried[""], "status.go in 2") {
		t.Fatalf("the development manager's pass was not handed the files in conflict:\n%s", carried[""])
	}
	if message, fired := carried[instanceName]; fired && (strings.Contains(message, "trial runs") || strings.Contains(message, "status.go in 2")) {
		t.Fatalf("the program manager's pass was handed the trial:\n%s", message)
	}
}
