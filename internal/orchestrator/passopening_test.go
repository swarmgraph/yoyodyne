package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// fixedReadModel is the read model as a pass reads it, the same every time.
type fixedReadModel struct {
	opening readmodel.PassOpening
	reads   int
}

func (f *fixedReadModel) Opening(context.Context) readmodel.PassOpening {
	f.reads++
	return f.opening
}

// A pass's assembled first message opens with the six parts of the read model —
// the standing, the throughput windows, the capacity state, the docket and the
// reports pile as counts, and a line per other instance with its name, its lane,
// its status, and its open requests — with two instances configured, each told
// about the other and not about itself.
func TestAPassOpensWithTheReadModelAndTheOtherInstancesLines(t *testing.T) {
	t.Parallel()

	instances := map[string]config.AgentConfig{
		"reliability-pm": {Role: domain.RoleProgramManager, Model: "opus", Lane: "reliability", Triggers: config.Triggers{Every: config.Duration(2 * time.Hour)}},
		"writing-pm":     {Role: domain.RoleProgramManager, Model: "opus", Lane: "writing", Triggers: config.Triggers{Every: config.Duration(2 * time.Hour)}},
	}
	h := newPassHarness(t, instances)
	model := &fixedReadModel{opening: readmodel.PassOpening{
		Standing:   "Running: 1 of 2 developer slots\nNeeds a human (0)",
		Throughput: "- today (since 2026-09-28): 4 started; 2 landed",
		Capacity:   "- Developer slots: 1 of 2 in use",
		Docket:     "3 stopped runs waiting on the development manager, 1 of them critical.",
		Reports:    "12 of 40 collected report(s) are unhandled.",
		Instances: []readmodel.ProgramManager{
			{Agent: "reliability-pm", Lane: "reliability", Status: readmodel.ProgramManagerWorking, OpenRequests: []string{"report-00000000000000000000000000000001"}},
			{Agent: "writing-pm", Lane: "writing", Status: readmodel.ProgramManagerBlocked, OpenRequests: []string{"exchange-7", "restart-0123456789abcdef"}},
		},
	}}
	h.trigger.ReadModel = model
	h.role.answers = []scriptedTurn{{result: complete("reliability looked at")}, {result: complete("writing looked at")}}

	h.fire(t, recurringNow)
	h.fire(t, recurringNow.Add(time.Minute))
	if len(h.role.messages) != 2 || h.role.agents[0] != "reliability-pm" || h.role.agents[1] != "writing-pm" {
		t.Fatalf("woke %v, want both instances once each", h.role.agents)
	}
	if model.reads != 2 {
		t.Errorf("the read model was read %d times, want once per pass", model.reads)
	}
	for index, want := range []struct {
		other, otherLine, self string
	}{
		{"writing-pm", "- writing-pm — lane writing — blocked — open requests: exchange-7, restart-0123456789abcdef", "- reliability-pm —"},
		{"reliability-pm", "- reliability-pm — lane reliability — working — open requests: report-00000000000000000000000000000001", "- writing-pm —"},
	} {
		message := h.role.messages[index]
		for _, part := range []string{
			"# Where the product stands, from the read model",
			"## Standing\n\nRunning: 1 of 2 developer slots",
			"## Throughput\n\n- today (since 2026-09-28): 4 started; 2 landed",
			"## Capacity\n\n- Developer slots: 1 of 2 in use",
			"## Docket\n\n3 stopped runs waiting on the development manager",
			"## Reports\n\n12 of 40 collected report(s) are unhandled.",
			"## The other program managers\n\n" + want.otherLine,
			"one digest in your report block",
		} {
			if !strings.Contains(message, part) {
				t.Errorf("the pass of %s opens without %q:\n%s", h.role.agents[index], part, message)
			}
		}
		if strings.Contains(message, want.self) {
			t.Errorf("the pass of %s lists itself among the other instances:\n%s", h.role.agents[index], message)
		}
		// The read model leads: it comes before the events and the account contract.
		if strings.Index(message, "## Standing") > strings.Index(message, "You are woken on your schedule alone") {
			t.Errorf("the read model does not open the pass of %s:\n%s", h.role.agents[index], message)
		}
	}
}

// A trigger wired without a read model says so in the message rather than
// opening the pass as though there were nothing to report.
func TestAPassWithNoReadModelWiredSaysSo(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(2*time.Hour))
	h.role.answers = []scriptedTurn{{result: complete("looked")}}
	h.fire(t, recurringNow)
	if len(h.role.messages) != 1 || !strings.Contains(h.role.messages[0], "Nothing was wired to read the read model for this pass") {
		t.Fatalf("messages = %q", h.role.messages)
	}
}
