package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// The factory-flow program manager's pass is handed the harness's reading of
// open pull requests and documents waiting to land, and the stalls it names
// join her account as findings of their own, beside hers. A program manager
// watching another lane is handed none of it.
func TestTheFactoryFlowPassIsHandedTheFlowReadingAndItsPileUpsJoinItsAccount(t *testing.T) {
	t.Parallel()

	instances := instance(time.Hour, config.TriggerStoppages)
	instances["flow-watcher"] = config.AgentConfig{Role: domain.RoleProgramManager, Model: "opus", Lane: "factory-flow",
		Triggers: config.Triggers{Every: config.Duration(time.Hour)}}
	h := newPassHarness(t, instances)
	calls := 0
	pileUp := sweep.Finding{Issue: `document "Machine home" (machine-home), the architect's, has 2 open publication pull requests at once: #101, #102`, Disposition: sweep.DispositionLeft, Detail: "noticed by the harness"}
	h.trigger.FactoryFlow = func(context.Context) (string, []sweep.Finding, []string) {
		calls++
		return "## Factory flow: open pull requests and documents waiting to land\n\n#101 and #102", []sweep.Finding{pileUp}, []string{"the documents waiting could not be read"}
	}
	for range 4 {
		h.role.answers = append(h.role.answers, scriptedTurn{result: complete("looked")})
	}
	for range 4 {
		if taken := h.fire(t, recurringNow); len(taken.Fired) == 0 {
			break
		}
	}
	if calls != 1 {
		t.Fatalf("the flow was read %d times, want once, for the factory-flow pass alone", calls)
	}
	for i, message := range h.role.messages {
		flow := strings.Contains(message, "## Factory flow: open pull requests")
		if (h.role.agents[i] == "flow-watcher") != flow {
			t.Fatalf("the pass of %q carried the flow reading: %v\n%s", h.role.agents[i], flow, message)
		}
	}
	var found bool
	for _, pass := range h.recorded(t) {
		if pass.Agent != "flow-watcher" {
			continue
		}
		found = true
		if pass.Result == nil || len(pass.Result.Findings) == 0 || pass.Result.Findings[len(pass.Result.Findings)-1].Issue != pileUp.Issue {
			t.Fatalf("the stall did not join the account: %+v", pass.Result)
		}
		if pass.Result.Summary != "looked" {
			t.Fatalf("the role's own account was replaced: %q", pass.Result.Summary)
		}
		if !strings.Contains(pass.Problem, "the documents waiting could not be read") {
			t.Fatalf("what could not be read is not on the record: %q", pass.Problem)
		}
	}
	if !found {
		t.Fatal("no factory-flow pass was recorded")
	}
}

// A pass that gave no account is given the harness's, and what does not fit the
// account's bound is said rather than dropped.
func TestHarnessFindingsAreAddedWithinTheAccountsBound(t *testing.T) {
	t.Parallel()

	finding := sweep.Finding{Issue: "a stall", Disposition: sweep.DispositionLeft}
	var recorded runstate.Sweep
	if problem := addHarnessFindings(&recorded, domain.RoleProgramManager, []sweep.Finding{finding}, "the stalls"); problem != "" || recorded.Result == nil || len(recorded.Result.Findings) != 1 {
		t.Fatalf("problem %q, account %+v", problem, recorded.Result)
	}
	if !strings.Contains(recorded.Result.Summary, "No account of this pass came from the program manager") {
		t.Fatalf("summary = %q", recorded.Result.Summary)
	}
	full := runstate.Sweep{Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "s", Findings: make([]sweep.Finding, sweep.MaxPassFindings)}}
	problem := addHarnessFindings(&full, domain.RoleProgramManager, []sweep.Finding{finding, finding}, "the stalls")
	if len(full.Result.Findings) != sweep.MaxPassFindings || !strings.Contains(problem, "2 of the stalls are not listed") {
		t.Fatalf("problem %q with %d findings", problem, len(full.Result.Findings))
	}
}

// A stop reason that fits is handed whole; one that does not is cut with the
// cut declared and where the whole reason is read, never ending mid-sentence
// with nothing said.
func TestAStoppageIsListedWholeOrCutWithTheCutDeclared(t *testing.T) {
	t.Parallel()

	stop := &runstate.StopEvent{Run: "run-1a2b3c4d"}
	reason := "the check make test failed: " + strings.Repeat("TestSomething output line ", 20)
	whole := describeEvent(PassEvent{Class: config.TriggerStoppages, At: recurringNow, Subject: "yoyodyne-7fl", Detail: "run run-1a2b3c4d stopped: " + reason, Stop: stop})
	if !strings.HasSuffix(whole, strings.TrimSpace(reason)) || strings.Contains(whole, "cut") {
		t.Fatalf("a reason inside the bound was not handed whole: %q", whole)
	}
	if len(reason) <= maxPassEventLineBytes {
		t.Fatal("the reason is short enough that the old bound would not have cut it, so this proves nothing")
	}

	long := "the check make test failed: " + strings.Repeat("é output ", 400) + "FAIL TestTheNamedCheck"
	cut := describeEvent(PassEvent{Class: config.TriggerStoppages, At: recurringNow, Subject: "yoyodyne-7fl", Detail: "run run-1a2b3c4d stopped: " + long, Stop: stop})
	if len(cut) > maxPassStopLineBytes {
		t.Fatalf("the cut line is %d bytes, over its bound of %d", len(cut), maxPassStopLineBytes)
	}
	if !strings.Contains(cut, "[the stop reason is cut here:") || !strings.Contains(cut, "run.inspect on run run-1a2b3c4d returns the whole reason as stop_reason]") {
		t.Fatalf("the cut is not declared with where the whole reason is: %q", cut)
	}
	if strings.Contains(cut, "FAIL TestTheNamedCheck") {
		t.Fatal("the reason was not long enough to be cut, so this proves nothing")
	}

	other := describeEvent(PassEvent{Class: config.TriggerLandings, At: recurringNow, Subject: "x", Detail: strings.Repeat("landed ", 100)})
	if len(other) > maxPassEventLineBytes {
		t.Fatalf("a landing kept the stoppage's wider bound: %d bytes", len(other))
	}
}
