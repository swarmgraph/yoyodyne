package orchestrator

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// observedHealth is the hook a test wires: it reads the workerless runs the
// test holds at the moment it is called, as the real one reads the run store,
// and records who it was called for.
type observedHealth struct {
	workerless []string
	calls      []string
}

func (o *observedHealth) observe(_ context.Context, role domain.AgentRole, agent string) (string, *runstate.SweepHealth) {
	o.calls = append(o.calls, string(role)+"/"+agent)
	id := fmt.Sprintf("health-20260905T090000Z-%08d", len(o.calls))
	var delivered []string
	for _, run := range o.workerless {
		delivered = append(delivered, "example/"+run+"/no-process")
	}
	section := "## Factory health, observed for this pass\n\nworkerless: " + strings.Join(o.workerless, ", ")
	return section, &runstate.SweepHealth{Observation: id, ObservedAt: recurringNow, Complete: true, Delivered: delivered}
}

// Every pass of the development manager and of the factory-flow program
// manager carries an observation taken as the pass is taken: a schedule due
// with no event past the cursor still hands the factory-flow instance the
// workerless runs that stand now, and the record keeps what it was handed. A
// program manager watching another lane is handed none.
func TestEveryManagerPassCarriesAFreshHealthObservationWithNoEventsPastItsCursor(t *testing.T) {
	t.Parallel()

	instances := instance(time.Hour, config.TriggerStoppages, config.TriggerLandings)
	instances["flow-watcher"] = config.AgentConfig{Role: domain.RoleProgramManager, Model: "opus", Lane: "factory-flow",
		Triggers: config.Triggers{Every: config.Duration(time.Hour), On: []config.TriggerEvent{config.TriggerStoppages, config.TriggerLandings}}}
	h := newPassHarness(t, instances)
	h.trigger.Tasks = map[string]config.RecurringTask{"triage": {Enabled: true, Role: domain.RoleDevelopmentManager, Every: config.Duration(time.Hour), Prompt: "triage"}}
	observed := &observedHealth{workerless: []string{"run-dead"}}
	h.trigger.Health = observed.observe
	for range 6 {
		h.role.answers = append(h.role.answers, scriptedTurn{result: complete("looked")})
	}
	// Each pull takes the passes due at it, one at a time, until none is.
	pull := func(at time.Time) int {
		fired := 0
		for range 5 {
			taken := h.fire(t, at)
			if len(taken.Fired) == 0 {
				break
			}
			fired += len(taken.Fired)
		}
		return fired
	}

	// The first pull positions the cursors, so there is nothing past them.
	if fired := pull(recurringNow); fired != 3 {
		t.Fatalf("first pull fired %d passes, want 3", fired)
	}
	// Two hours later the schedule is due, nothing has happened in any stream,
	// and a second run has lost its worker in the meantime.
	observed.workerless = []string{"run-dead", "run-later"}
	h.role.messages, h.role.agents = nil, nil
	if fired := pull(recurringNow.Add(2 * time.Hour)); fired != 3 {
		t.Fatalf("second pull fired %d passes, want 3", fired)
	}
	carried := map[string]string{}
	for i, message := range h.role.messages {
		carried[h.role.agents[i]] = message
	}
	for _, agent := range []string{"", "flow-watcher"} {
		message := carried[agent]
		if !strings.HasPrefix(message, "## Factory health, observed for this pass") || !strings.Contains(message, "workerless: run-dead, run-later") {
			t.Fatalf("the pass of %q was not handed the factory as it stands now:\n%s", agent, message)
		}
	}
	if !strings.Contains(carried["flow-watcher"], "has happened since your last pass") {
		t.Fatalf("the factory-flow pass had events past its cursor, so it proves nothing:\n%s", carried["flow-watcher"])
	}
	if strings.Contains(carried[instanceName], "Factory health") {
		t.Fatalf("the reliability instance was handed a health observation:\n%s", carried[instanceName])
	}
	if want := []string{"development-manager/", "program-manager/flow-watcher", "development-manager/", "program-manager/flow-watcher"}; !sameItems(observed.calls, want) {
		t.Fatalf("observed for %v, want %v", observed.calls, want)
	}

	var handed []runstate.Sweep
	for _, pass := range h.recorded(t) {
		if pass.StartedAt.Equal(recurringNow.Add(2 * time.Hour)) {
			handed = append(handed, pass)
		}
	}
	for _, pass := range handed {
		switch {
		case pass.Agent == instanceName && pass.Health != nil:
			t.Fatalf("the reliability pass recorded an observation: %+v", pass.Health)
		case pass.Agent != instanceName && (pass.Health == nil || !reflect.DeepEqual(pass.Health.Delivered, []string{"example/run-dead/no-process", "example/run-later/no-process"})):
			t.Fatalf("the pass of %q recorded %+v", pass.Agent, pass.Health)
		}
	}
}

func sameItems(got, want []string) bool {
	counts := map[string]int{}
	for _, item := range got {
		counts[item]++
	}
	for _, item := range want {
		counts[item]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}
