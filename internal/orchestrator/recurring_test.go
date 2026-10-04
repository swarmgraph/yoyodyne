package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

var recurringNow = time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)

// wokenRole is a role's conversation as a test reaches it: what it was asked, and
// what it was told to answer.
type wokenRole struct {
	messages []string
	// passes is the firing each turn was told it belonged to.
	passes []string
	// models is the model each turn was asked on, empty where the task named none.
	models []string
	// agents is the agent each turn woke, empty where the firing named the role.
	agents  []string
	answers []scriptedTurn
	failure error
}

// scriptedTurn is one turn a test hands back: the account the role gave, what it
// cost, and the failure where the turn is meant to fail.
type scriptedTurn struct {
	result *sweep.Result
	// problem is what the turn says about its account beside it, where a test
	// hands back one that was read with something worth noting.
	problem string
	err     error
	cost    float64
	// model is the model the turn says served it.
	model string
	// saved, reports, and admitted are the traces the turn says it left: the
	// memory and lane-report writes it saved, how many reports it filed, and
	// the work it admitted.
	saved    []runstate.SavedWrite
	reports  int
	admitted []string
	wording  []terms.Finding
}

func (r *wokenRole) Wake(_ context.Context, _ domain.AgentRole, agent, pass, model, message string) (Turn, error) {
	r.messages = append(r.messages, message)
	r.agents = append(r.agents, agent)
	r.passes = append(r.passes, pass)
	r.models = append(r.models, model)
	if r.failure != nil {
		return Turn{}, r.failure
	}
	if len(r.answers) == 0 {
		return Turn{ConversationID: "chat-1"}, nil
	}
	answer := r.answers[0]
	r.answers = r.answers[1:]
	return Turn{
		ConversationID: "chat-1", CostUSD: answer.cost, Model: answer.model, Result: answer.result, ResultProblem: answer.problem,
		Saved: answer.saved, ReportsFiled: answer.reports, Admitted: answer.admitted, Wording: answer.wording,
	}, answer.err
}

func sweepStore(t *testing.T) *runstate.SweepStore {
	t.Helper()
	store, err := runstate.NewSweepStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	return store
}

func hourlyTask(prompt string) map[string]config.RecurringTask {
	return map[string]config.RecurringTask{
		"a-sweep": {
			Role:     domain.RoleDevelopmentManager,
			Every:    config.Duration(time.Hour),
			Enabled:  true,
			Prompt:   prompt,
			MaxTurns: 3,
		},
	}
}

func complete(summary string, findings ...sweep.Finding) *sweep.Result {
	return &sweep.Result{Status: sweep.StatusComplete, Summary: summary, Findings: findings}
}

// A task that names its own model asks for it on every turn of its pass, and
// the pass's record names the model that served; a task naming none asks for
// nothing in particular, which is the role's own model.
func TestAFiringAsksForTheTasksModelAndRecordsWhatServed(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	tasks := hourlyTask("look")
	task := tasks["a-sweep"]
	task.Model = "sonnet"
	tasks["a-sweep"] = task
	role := &wokenRole{answers: []scriptedTurn{
		{result: &sweep.Result{Status: sweep.StatusMore, Summary: "half"}, cost: 0.1, model: "sonnet"},
		{result: complete("the rest"), cost: 0.1, model: "sonnet"},
	}}
	trigger := Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}
	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(role.models) != 2 || role.models[0] != "sonnet" || role.models[1] != "sonnet" {
		t.Fatalf("turns asked for %v, want the task's sonnet on both", role.models)
	}
	if fired.Fired[0].Model != "sonnet" {
		t.Errorf("fired model = %q, want sonnet", fired.Fired[0].Model)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 1 || recorded[0].Model != "sonnet" {
		t.Fatalf("recorded = %+v (%v), want one pass naming sonnet", recorded, err)
	}

	unnamed := sweepStore(t)
	plain := &wokenRole{answers: []scriptedTurn{{result: complete("nothing"), model: "fable"}}}
	trigger = Trigger{Tasks: hourlyTask("look"), Claims: unnamed, Reports: unnamed, Roles: plain, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(plain.models) != 1 || plain.models[0] != "" {
		t.Fatalf("turns asked for %q, want no model named so the role's own applies", plain.models)
	}
	recorded, _, err = unnamed.List()
	if err != nil || len(recorded) != 1 || recorded[0].Model != "fable" {
		t.Fatalf("recorded = %+v (%v), want the role's model the turn reported serving", recorded, err)
	}
}

func TestFiringWakesTheRoleAndRecordsWhatItFound(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{{
		result: complete("one dead claim, released", sweep.Finding{
			Issue:       "a claim on a run nothing is running",
			Disposition: sweep.DispositionFixed,
			Filed:       []string{"yoyodyne-ifd.300"},
		}),
		cost: 0.25,
	}}}
	trigger := Trigger{Tasks: hourlyTask("look for unresolved issues"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the one due task", fired.Fired)
	}
	if fired.Fired[0].Findings != 1 || fired.Fired[0].Turns != 1 {
		t.Errorf("fired = %+v, want one finding in one turn", fired.Fired[0])
	}
	if fired.Fired[0].CostUSD != 0.25 {
		t.Errorf("cost = %v, want what the turn cost", fired.Fired[0].CostUSD)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result == nil {
		t.Fatalf("recorded = %+v, want one durable report with an account on it", recorded)
	}
	if recorded[0].ConversationID != "chat-1" {
		t.Errorf("conversation = %q, want the role's own conversation", recorded[0].ConversationID)
	}
}

// The message the harness sends carries three things the configuration cannot
// change: who woke the role, that being woken grants it nothing, and that a
// finding is checked against admitted work before it is filed.
func TestTheWakeMessageCarriesTheStandingConstraints(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{{result: complete("nothing")}}}
	trigger := Trigger{Tasks: hourlyTask("the project's own instruction"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(role.messages) != 1 {
		t.Fatalf("messages = %v, want one", role.messages)
	}
	for _, want := range []string{
		"the project's own instruction",
		"authority your role already holds",
		"check it against the work already admitted",
		sweep.Fence,
	} {
		if !strings.Contains(role.messages[0], want) {
			t.Errorf("the wake message does not carry %q:\n%s", want, role.messages[0])
		}
	}
}

// A heavy pass iterates rather than truncating: the role says it has more to do
// and is given another turn, and the findings of every turn are kept.
func TestAHeavyPassIteratesItsTurns(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{
		{result: &sweep.Result{Status: sweep.StatusMore, Summary: "started", Findings: []sweep.Finding{{Issue: "one", Disposition: sweep.DispositionFiled}}}},
		{result: complete("finished", sweep.Finding{Issue: "two", Disposition: sweep.DispositionFiled})},
	}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Fired[0].Turns != 2 || fired.Fired[0].Findings != 2 {
		t.Errorf("fired = %+v, want two turns and both turns' findings", fired.Fired[0])
	}
	if fired.Fired[0].Truncated {
		t.Error("a pass that finished inside its bound is reported as truncated")
	}
}

// A turn can carry its account and something worth saying about it at once — a
// reply with more than one block, of which the last was read. The account is
// recorded exactly as a clean one is, and the note goes on the record beside it
// rather than in place of it: the pass's decisions were taken, and a problem
// that cost the record would be the report-pile problem in miniature.
func TestATurnWithAnAccountAndANoteRecordsBoth(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{{
		result:  complete("twelve decided, nothing behind them", sweep.Finding{Issue: "a stale report", Disposition: sweep.DispositionFiled, Filed: []string{"yoyodyne-ifd.400"}}),
		problem: "the development-manager answered with more than one sweep block: the reply carried 2 sweep blocks where the contract asks for one, and the last of them is the account recorded",
	}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Fired[0].Turns != 1 || fired.Fired[0].Findings != 1 {
		t.Errorf("fired = %+v, want the account read in one turn", fired.Fired[0])
	}
	if !strings.Contains(fired.Fired[0].Problem, "more than one sweep block") {
		t.Errorf("problem = %q, want the note carried", fired.Fired[0].Problem)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result == nil || len(recorded[0].Result.Findings) != 1 {
		t.Fatalf("recorded = %+v, want the durable report to carry the account", recorded)
	}
	if !strings.Contains(recorded[0].Problem, "more than one sweep block") {
		t.Errorf("recorded problem = %q, want the note on the record beside the account", recorded[0].Problem)
	}
}

// A pass that still has more to do when its turn bound runs out says so. A
// truncated pass and a finished one produce the same short report, and nothing
// else distinguishes them.
func TestAPassTruncatedByItsBoundSaysSo(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	more := func() *sweep.Result {
		return &sweep.Result{Status: sweep.StatusMore, Summary: "still going"}
	}
	role := &wokenRole{answers: []scriptedTurn{{result: more()}, {result: more()}, {result: more()}, {result: more()}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Fired[0].Turns != 3 {
		t.Errorf("turns = %d, want the configured bound of 3", fired.Fired[0].Turns)
	}
	if !fired.Fired[0].Truncated {
		t.Error("a pass stopped by its turn bound is not reported as truncated")
	}
	if !strings.Contains(fired.Fired[0].Problem, "still had more to do") {
		t.Errorf("problem = %q, want it to say the bound ended the pass", fired.Fired[0].Problem)
	}
}

// A pass that fixed something and filed nothing for its root cause is a silent
// repair, and the whole reason for these reports is that a week of them shows
// whether repairs are silent.
func TestASilentRepairIsCountedOnTheFiring(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{{result: complete("fixed it",
		sweep.Finding{Issue: "a stuck delivery", Disposition: sweep.DispositionFixed})}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Fired[0].SilentRepairs != 1 {
		t.Errorf("silent repairs = %d, want 1", fired.Fired[0].SilentRepairs)
	}
	if !strings.Contains(RecurringSweep{Fired: fired.Fired}.Render(), "filed nothing for their root cause") {
		t.Errorf("the rendered pass does not say a fix filed nothing:\n%s", RecurringSweep{Fired: fired.Fired}.Render())
	}
}

// A task fires once per cadence however many passes walk past it, which is the
// whole of what the claim is for.
func TestATaskFiresOncePerCadence(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{{result: complete("nothing")}, {result: complete("nothing")}}}
	clock := &movingRecurringClock{now: recurringNow}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: clock}

	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("first Fire() error = %v", err)
	}
	clock.now = recurringNow.Add(10 * time.Minute)
	second, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("second Fire() error = %v", err)
	}
	if len(second.Fired) != 0 {
		t.Errorf("fired = %+v, want nothing from a pass whose task is not due", second.Fired)
	}
	clock.now = recurringNow.Add(time.Hour)
	third, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("third Fire() error = %v", err)
	}
	if len(third.Fired) != 1 {
		t.Errorf("fired = %+v, want the task fired once its cadence passed", third.Fired)
	}
}

func TestADisabledTaskNeverFires(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	tasks := hourlyTask("sweep")
	task := tasks["a-sweep"]
	task.Enabled = false
	tasks["a-sweep"] = task
	role := &wokenRole{}
	trigger := Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 0 || len(role.messages) != 0 {
		t.Errorf("fired = %+v, messages = %v, want a disabled task left alone", fired.Fired, role.messages)
	}
}

// A firing is a provider invocation, so the operator's pause covers it exactly as
// it covers a run, a turn, and a delivery. Nothing is claimed, so every task
// keeps its cadence.
func TestAPausedHarnessFiresNothing(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{}
	trigger := Trigger{
		Tasks:   hourlyTask("sweep"),
		Claims:  store,
		Reports: store,
		Roles:   role,
		Holds:   pausedHolds{hold: runstate.OperatorHold{HeldAt: recurringNow}, held: true},
		Clock:   recurringClock{},
	}
	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Paused == nil {
		t.Fatal("a paused harness did not report the pause")
	}
	if len(role.messages) != 0 {
		t.Errorf("messages = %v, want nothing said to the role", role.messages)
	}
	if _, found, err := store.Find("a-sweep"); err != nil || found {
		t.Errorf("Find() = %v, %v, want no claim taken under a pause", found, err)
	}
}

// A conversation that could never be opened asked the role nothing. The record
// says so, in words that distinguish it from a pass that ran and found nothing.
func TestAnUnreachableRoleIsRecordedRatherThanLost(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{failure: ErrRoleUnreachable}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 || fired.Fired[0].Turns != 0 {
		t.Fatalf("fired = %+v, want a firing that took no turn", fired.Fired)
	}
	if !strings.Contains(fired.Fired[0].Problem, "could not be put to the") {
		t.Errorf("problem = %q, want it to say the role was never asked", fired.Fired[0].Problem)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result != nil || recorded[0].Problem == "" {
		t.Errorf("recorded = %+v, want a durable record of the firing that produced nothing", recorded)
	}
}

// A later turn losing the conversation is a partial pass, not a firing that
// asked nothing, and the record has to say which.
//
// The turns before it answered and their account is what the record carries, so
// the sentence a first-turn failure earns — "could not be put to the role at
// all, so nothing was asked" — would contradict the turn count sitting beside it
// and leave a reader to decide which half to believe.
func TestALaterTurnLosingTheRoleIsRecordedAsPartialRatherThanUnasked(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{
		{result: &sweep.Result{Status: sweep.StatusMore, Summary: "the first turn, with more to do"}},
		{err: ErrRoleUnreachable},
	}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 || fired.Fired[0].Turns != 1 {
		t.Fatalf("fired = %+v, want the one turn that answered kept", fired.Fired)
	}
	problem := fired.Fired[0].Problem
	if strings.Contains(problem, "nothing was asked") {
		t.Errorf("problem = %q, want it not to claim the role was never asked when a turn had already answered", problem)
	}
	if !strings.Contains(problem, "turn 2") {
		t.Errorf("problem = %q, want it to name the turn that could not be reached", problem)
	}
}

// A turn that answered in prose without a block is not a failed turn: the role
// answered, and what is lost is the structure. It is said out loud rather than
// recorded as a pass that found nothing.
func TestAnAnswerWithoutAnAccountIsSaidOutLoud(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{{}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Fired[0].Turns != 1 {
		t.Errorf("turns = %d, want the turn that answered", fired.Fired[0].Turns)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result != nil || recorded[0].Problem == "" {
		t.Errorf("recorded = %+v, want a record saying no account came back", recorded)
	}
}

// One firing per pass, whatever else is due. A pass that fired three tasks would
// hold the queue closed for as long as all three took.
func TestOnlyOneTaskFiresPerPass(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	tasks := hourlyTask("sweep")
	tasks["b-sweep"] = tasks["a-sweep"]
	role := &wokenRole{answers: []scriptedTurn{{result: complete("nothing")}, {result: complete("nothing")}}}
	trigger := Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want one task per pass", fired.Fired)
	}
	if fired.Fired[0].Task != "a-sweep" {
		t.Errorf("task = %q, want the first in name order", fired.Fired[0].Task)
	}
	// The one it passed over is the next pass's, and it is still due.
	second, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("second Fire() error = %v", err)
	}
	if len(second.Fired) != 1 || second.Fired[0].Task != "b-sweep" {
		t.Errorf("fired = %+v, want the other task on the next pass", second.Fired)
	}
}

func TestATriggerWithNothingToRecordWithIsRefused(t *testing.T) {
	t.Parallel()

	if _, err := (Trigger{Tasks: hourlyTask("sweep")}).Fire(context.Background()); err == nil {
		t.Fatal("a trigger with no claims, reports, or roles fired")
	}
}

type recurringClock struct{}

func (recurringClock) Now() time.Time { return recurringNow }

type movingRecurringClock struct{ now time.Time }

func (c *movingRecurringClock) Now() time.Time { return c.now }

// The case iteration exists for, end to end: a firing that takes every one of its
// turns and reports the per-turn maximum on each of them still lands a durable
// report. It is here rather than only in the sweep package because the defect it
// guards was in the seam — per-turn bounds applied to a merged account — and what
// it cost was the whole report of the busiest passes.
func TestAFiringAtEveryTurnsMaximumStillLandsItsReport(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	tasks := hourlyTask("sweep")
	task := tasks["a-sweep"]
	task.MaxTurns = config.MaxRecurringTurns
	tasks["a-sweep"] = task

	crowded := func(status sweep.Status) *sweep.Result {
		result := &sweep.Result{Status: status, Summary: "a heavy pass"}
		for i := 0; i < sweep.MaxFindings; i++ {
			result.Findings = append(result.Findings, sweep.Finding{
				Issue:       "a thing that was found",
				Disposition: sweep.DispositionFiled,
			})
		}
		for i := 0; i < sweep.MaxQuestions; i++ {
			result.Questions = append(result.Questions, "something only a person can settle")
		}
		return result
	}
	var answers []scriptedTurn
	for turn := 0; turn < task.MaxTurns-1; turn++ {
		answers = append(answers, scriptedTurn{result: crowded(sweep.StatusMore)})
	}
	answers = append(answers, scriptedTurn{result: crowded(sweep.StatusComplete)})
	role := &wokenRole{answers: answers}
	trigger := Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Fired[0].Turns != task.MaxTurns {
		t.Errorf("turns = %d, want every one of the %d the task allows", fired.Fired[0].Turns, task.MaxTurns)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded = %+v, want the firing's durable report", recorded)
	}
	if recorded[0].Result == nil {
		t.Fatalf("the heaviest pass recorded no account: %s", recorded[0].Problem)
	}
	if got := len(recorded[0].Result.Findings); got != task.MaxTurns*sweep.MaxFindings {
		t.Errorf("findings = %d, want every turn's %d", got, task.MaxTurns*sweep.MaxFindings)
	}
	if fired.Fired[0].Problem != "" {
		t.Errorf("problem = %q, want a heavy pass recorded without complaint", fired.Fired[0].Problem)
	}
}

// The turn bound a task may configure has to fit inside the number of turns one
// account can fold together, or the configuration permits a firing whose own
// findings the merge would start dropping.
func TestTheConfigurableTurnBoundFitsTheMergeBound(t *testing.T) {
	t.Parallel()

	if config.MaxRecurringTurns > sweep.MaxMergedTurns {
		t.Fatalf("a task may configure %d turns and an account folds %d together, so the extra turns' findings would be dropped",
			config.MaxRecurringTurns, sweep.MaxMergedTurns)
	}
}

// Every problem sentence ends with a provider's error message, whose length
// nothing here controls. A record refused because those ran long would lose the
// pass over the description of a smaller failure.
func TestLongFailureMessagesDoNotCostTheRecord(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	tasks := hourlyTask("sweep")
	task := tasks["a-sweep"]
	task.MaxTurns = config.MaxRecurringTurns
	tasks["a-sweep"] = task
	role := &wokenRole{answers: []scriptedTurn{{
		result: &sweep.Result{Status: sweep.StatusMore, Summary: "started"},
	}, {
		err: errors.New(strings.Repeat("the provider said something very long. ", 400)),
	}}}
	trigger := Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if strings.Contains(fired.Fired[0].Problem, "reaches nobody") {
		t.Errorf("problem = %q, want the record kept despite the long failure", fired.Fired[0].Problem)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded = %+v, want the firing's durable report", recorded)
	}
	if len(recorded[0].Problem) > runstate.MaxSweepTextBytes {
		t.Errorf("problem is %d bytes, and the record's bound is %d", len(recorded[0].Problem), runstate.MaxSweepTextBytes)
	}
	if recorded[0].Result == nil {
		t.Error("the turn that did answer left no account on the record")
	}
}

// The bounds above are meant to make the first write always succeed, and this is
// what happens when something makes it fail anyway: the firing is recorded
// without its account rather than not at all, because a pass that spent turns and
// left nothing behind is indistinguishable from one that never happened.
func TestARefusedRecordStillLeavesTheFiringBehind(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	reports := &refusingReports{store: store}
	role := &wokenRole{answers: []scriptedTurn{{result: complete("found one thing",
		sweep.Finding{Issue: "a dead claim", Disposition: sweep.DispositionFixed})}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: reports, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if !strings.Contains(fired.Fired[0].Problem, "would not store") {
		t.Errorf("problem = %q, want it to say the account would not store", fired.Fired[0].Problem)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded = %+v, want the firing recorded without its account", recorded)
	}
	if recorded[0].Result != nil {
		t.Errorf("recorded = %+v, want the account left off the second attempt", recorded[0])
	}
	if recorded[0].Turns != 1 || recorded[0].Task != "a-sweep" {
		t.Errorf("recorded = %+v, want what the harness itself knows about the firing", recorded[0])
	}
}

// refusingReports refuses any record carrying an account, and takes one without.
// It stands for whatever would make a full record unwritable.
type refusingReports struct{ store *runstate.SweepStore }

func (r *refusingReports) Append(recorded runstate.Sweep) error {
	if recorded.Result != nil {
		return errors.New("this record carries an account and will not store")
	}
	return r.store.Append(recorded)
}

func (r *refusingReports) List() ([]runstate.Sweep, []runstate.UnreadableSweep, error) {
	return r.store.List()
}

// A firing due while the provider is answering nobody records the wait rather
// than a turn that failed, asks the role nothing, and keeps its cadence: what a
// week of passes then says about the outage is the outage, rather than a column
// of zero turns nobody could read a login out of.
func TestAFiringIntoAProviderAnsweringNobodyRecordsTheWait(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	outages, err := runstate.NewProviderOutageStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewProviderOutageStore() error = %v", err)
	}
	if _, err := outages.Notice(runstate.ProviderOutageObservation{Cause: domain.ProviderUnauthenticated, Waiting: "the dispatch of an item", At: recurringNow}); err != nil {
		t.Fatal(err)
	}
	role := &wokenRole{}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Outages: outages, OutageProbe: 30 * time.Minute, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 || fired.Fired[0].Turns != 0 || len(role.messages) != 0 {
		t.Fatalf("fired = %+v (messages=%d), want a firing recorded with no turn taken", fired.Fired, len(role.messages))
	}
	if !strings.Contains(fired.Fired[0].Problem, "The provider is not authenticated; the operator must log in") {
		t.Errorf("problem = %q, want the login named as what the pass waited on", fired.Fired[0].Problem)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result != nil || !strings.Contains(recorded[0].Problem, "the operator must log in") {
		t.Errorf("recorded = %+v, want a durable record naming the wait", recorded)
	}
	// The cadence moved: the next pull does not fire the same task again into
	// the same refusal.
	again, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("second Fire() error = %v", err)
	}
	if len(again.Fired) != 0 {
		t.Fatalf("second Fire() = %+v, want the task not due again for an hour", again.Fired)
	}
}

// Once the probe interval has passed since the provider was last met refusing,
// a due firing is made into the outage rather than refused: the firing is the
// only probe this path has, and a machine with nothing in its backlog and no
// watch running — the laptop that was asleep — would otherwise never find the
// network back.
func TestAFiringPastTheProbeIntervalIsMadeIntoTheOutage(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	outages, err := runstate.NewProviderOutageStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewProviderOutageStore() error = %v", err)
	}
	clock := recurringClock{}
	if _, err := outages.Notice(runstate.ProviderOutageObservation{
		Cause: domain.ProviderUnreachable, Waiting: "an earlier run", At: clock.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	role := &wokenRole{}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Outages: outages, OutageProbe: 30 * time.Minute, Clock: clock}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 || fired.Fired[0].Turns != 1 || len(role.messages) != 1 {
		t.Fatalf("fired = %+v (messages=%d), want the role woken as the probe", fired.Fired, len(role.messages))
	}
	if fired.Fired[0].Problem != "" && strings.Contains(fired.Fired[0].Problem, "cannot be reached") {
		t.Fatalf("problem = %q, want the outage not recorded against a firing that was made", fired.Fired[0].Problem)
	}
}

// twoHeldOpen is the forge the work item describes, as the trigger sees it:
// one request whose item is closed, one whose branch main already carries; the
// live third is not a notice at all, which is the reading's own silence.
func twoHeldOpen() *orchestratortest.NoticingForge {
	return &orchestratortest.NoticingForge{Notices: []runstate.ForgeNotice{
		{Number: 445, URL: "https://forge.invalid/pull/445", HeadBranch: "yoyodyne/yoyodyne-ifd-283/aaaaaaaa", BaseBranch: "main", WorkItemID: "yoyodyne-ifd.283", ItemClosed: true},
		{Number: 460, URL: "https://forge.invalid/pull/460", HeadBranch: "yoyodyne/yoyodyne-ifd-300/bbbbbbbb", BaseBranch: "main", WorkItemID: "yoyodyne-ifd.300", Contained: true},
	}}
}

// The development manager's pass reads the forge and states what it holds open
// for nothing as findings beside the role's own, keyed on the request; the
// next pass over the same forge states nothing again.
func TestTheForgeIsReadOnThePassAndEachRequestIsReportedOnce(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	forge := twoHeldOpen()
	role := &wokenRole{answers: []scriptedTurn{{result: complete("one dead claim, released", sweep.Finding{
		Issue: "a claim on a run nothing is running", Disposition: sweep.DispositionFixed, Filed: []string{"yoyodyne-ifd.400"},
	})}, {result: complete("nothing")}}}
	clock := &movingRecurringClock{now: recurringNow}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Forge: forge, Clock: clock}

	first, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("first Fire() error = %v", err)
	}
	if len(first.Fired) != 1 || first.Fired[0].Findings != 3 || first.Fired[0].PullRequests != 2 {
		t.Fatalf("fired = %+v, want the role's finding and the two requests counted, and the two said to be the harness's", first.Fired)
	}
	if rendered := first.Render(); !strings.Contains(rendered, "2 of the findings are open pull requests the harness noticed") {
		t.Errorf("the rendered pass does not say which findings are the harness's:\n%s", rendered)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result == nil {
		t.Fatalf("recorded = %+v, want one report with an account", recorded)
	}
	findings := recorded[0].Result.Findings
	if len(findings) != 3 || findings[0].Issue != "a claim on a run nothing is running" {
		t.Fatalf("findings = %+v, want the role's own first and the two requests after it", findings)
	}
	for i, want := range []string{"#445", "#460"} {
		finding := findings[i+1]
		if !strings.Contains(finding.Issue, want) || finding.Disposition != sweep.DispositionLeft {
			t.Errorf("findings[%d] = %+v, want %s stated and left", i+1, finding, want)
		}
	}
	if !strings.Contains(findings[1].Issue, "yoyodyne-ifd.283 is closed") {
		t.Errorf("finding = %q, want the closed item named", findings[1].Issue)
	}
	if !strings.Contains(findings[2].Issue, "already contained in main") {
		t.Errorf("finding = %q, want the contained branch named", findings[2].Issue)
	}
	if len(recorded[0].PullRequests) != 2 || recorded[0].PullRequests[0].Number != 445 || recorded[0].PullRequests[1].Number != 460 {
		t.Errorf("pull requests = %+v, want the two requests keyed on the record", recorded[0].PullRequests)
	}
	if first.Fired[0].Problem != "" {
		t.Errorf("problem = %q, want none on a pass that read the forge whole", first.Fired[0].Problem)
	}

	clock.now = recurringNow.Add(time.Hour)
	second, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("second Fire() error = %v", err)
	}
	if len(second.Fired) != 1 || second.Fired[0].Findings != 0 {
		t.Fatalf("fired = %+v, want a second pass over the same forge to report nothing again", second.Fired)
	}
	if len(forge.Reported) != 2 || !forge.Reported[1][445] || !forge.Reported[1][460] {
		t.Errorf("reported = %+v, want the second pass told which requests the first already reported", forge.Reported)
	}
	recorded, _, err = store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 || len(recorded[1].PullRequests) != 0 || recorded[1].Result == nil || len(recorded[1].Result.Findings) != 0 {
		t.Errorf("second record = %+v, want no request on it", recorded[1])
	}
}

// A task of another role is not the forge's reader: the reading is the
// development manager's and a product manager's pass leaves the forge alone.
func TestOnlyTheDevelopmentManagersPassReadsTheForge(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	forge := twoHeldOpen()
	tasks := hourlyTask("scan")
	task := tasks["a-sweep"]
	task.Role = domain.RoleProductManager
	tasks["a-sweep"] = task
	role := &wokenRole{answers: []scriptedTurn{{result: complete("nothing")}}}
	trigger := Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: role, Forge: forge, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 || fired.Fired[0].Findings != 0 || len(forge.Reported) != 0 {
		t.Fatalf("fired = %+v (forge read %d times), want the forge left alone", fired.Fired, len(forge.Reported))
	}
}

// The forge is not the provider. A pass whose role could not be reached still
// reads it, and what it found is stated in an account the harness writes,
// beside the problem that says the role's own is missing.
func TestTheForgeIsReadEvenWhenTheRoleCouldNotBe(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	forge := twoHeldOpen()
	role := &wokenRole{failure: fmt.Errorf("%w: the operator is mid-turn", ErrRoleUnreachable)}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Forge: forge, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 || fired.Fired[0].Turns != 0 || fired.Fired[0].Findings != 2 {
		t.Fatalf("fired = %+v, want no turn and the two requests stated", fired.Fired)
	}
	if !strings.Contains(fired.Fired[0].Problem, "could not be put to the development-manager") {
		t.Errorf("problem = %q, want the unreached role still said", fired.Fired[0].Problem)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result == nil || !strings.Contains(recorded[0].Result.Summary, "harness's own reading") {
		t.Fatalf("recorded = %+v, want an account the harness wrote for its own findings", recorded)
	}
}

// A forge that could not be read is a problem on the record, never a lost
// pass: the role's account stands and the next pass reads the forge again.
func TestAForgeThatCouldNotBeReadIsSaidAndCostsNothingElse(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	forge := &orchestratortest.NoticingForge{Err: errors.New("gh: not logged in")}
	role := &wokenRole{answers: []scriptedTurn{{result: complete("nothing")}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Forge: forge, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 || !strings.Contains(fired.Fired[0].Problem, "the forge could not be fully read") || !strings.Contains(fired.Fired[0].Problem, "not logged in") {
		t.Fatalf("fired = %+v, want the forge's refusal on the record", fired.Fired)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result == nil || recorded[0].Result.Summary != "nothing" {
		t.Fatalf("recorded = %+v, want the role's own account kept", recorded)
	}
}

// A summons is the development manager's sweep fired out of its cadence with
// the brake's trip in front of her: the three runs and the reason each blocked
// in the message that wakes her, her three decisions named, and when the brake
// probes by itself if she records none. It is a firing like any other — claimed
// whether or not the task is due, recorded as summoned, and paced from.
func TestASummonsFiresTheSweepOutOfCadenceWithTheTripInTheWake(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	// The scheduled pass fired a minute ago, so the cadence would refuse.
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, recurringNow.Add(-time.Minute)); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	role := &wokenRole{answers: []scriptedTurn{{result: complete("released the hold: three verdicts on three changes"), cost: 0.4}}}
	trigger := Trigger{Tasks: hourlyTask("sweep for unresolved issues"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}
	trippedAt := recurringNow.Add(-30 * time.Second)
	hold := runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "example",
		HeldAt:        trippedAt,
		HeldBy:        runstate.IntakeHolderBrake,
		Reason:        "3 run(s) blocked in a row with nothing landing between them, which is the configured brake at 3",
		Brake: &runstate.IntakeBrake{
			Blocked: []runstate.BrakeBlockedRun{
				{RunID: "run-398", WorkItemID: "yoyodyne-ifd.398", Reason: "independent review still required repair after every permitted attempt"},
				{RunID: "run-353", WorkItemID: "yoyodyne-ifd.353", Reason: "a configured check still failed after every permitted attempt"},
				{RunID: "run-404", WorkItemID: "yoyodyne-ifd.404", Reason: "independent review still required repair after every permitted attempt"},
			},
			CooldownEndsAt: trippedAt.Add(30 * time.Minute),
		},
	}

	fired, err := trigger.Summon(context.Background(), BrakeSummons{Hold: hold})
	if err != nil {
		t.Fatalf("Summon() error = %v", err)
	}
	if fired.Turns != 1 || fired.CostUSD != 0.4 || fired.Task != "a-sweep" || fired.Summoned == "" {
		t.Fatalf("fired = %+v, want one summoned turn of the development manager's task", fired)
	}
	if len(role.messages) != 1 {
		t.Fatalf("messages = %d, want one", len(role.messages))
	}
	message := role.messages[0]
	for _, want := range []string{
		"The intake brake summoned you now",
		"run-398 of yoyodyne-ifd.398: independent review still required repair",
		"run-353 of yoyodyne-ifd.353: a configured check still failed",
		"run-404 of yoyodyne-ifd.404",
		`"release"`, `"probe"`, `"escalate"`,
		"a probe run starts by itself at " + trippedAt.Add(30*time.Minute).Format(time.RFC3339),
		"sweep for unresolved issues",
		"authority your role already holds",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the summons does not say %q:\n%s", want, message)
		}
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %d record(s), %v; want the summoned pass recorded", len(recorded), err)
	}
	if !strings.Contains(recorded[0].Summoned, "the intake brake") {
		t.Fatalf("recorded summoned = %q, want the brake named as what summoned the pass", recorded[0].Summoned)
	}
	// The cadence runs on from the summons rather than from the pass before it.
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, recurringNow.Add(59*time.Minute)); !errors.Is(err, runstate.ErrSweepNotDue) {
		t.Fatalf("Claim() an hour after the earlier pass error = %v, want the cadence paced from the summons", err)
	}
	// A second summons — a probe that blocked — carries the probe beside the trip.
	blockedAt := recurringNow.Add(40 * time.Minute)
	hold.Brake.Probe = &runstate.IntakeProbe{WorkItemID: "yoyodyne-ifd.410", RunID: "run-410", StartedAt: recurringNow.Add(31 * time.Minute), EndedAt: &blockedAt, Blocked: true, Reason: "a configured check still failed"}
	role.answers = []scriptedTurn{{result: complete("escalated: the same check fails on every change")}}
	if _, err := trigger.Summon(context.Background(), BrakeSummons{Hold: hold}); err != nil {
		t.Fatalf("second Summon() error = %v", err)
	}
	if again := role.messages[1]; !strings.Contains(again, "probe run run-410 of yoyodyne-ifd.410") || !strings.Contains(again, "blocked as well: a configured check still failed") {
		t.Errorf("the second summons does not name the blocked probe:\n%s", again)
	}
}

// A project that schedules no development manager's sweep has nothing to
// summon, and says so rather than waking a role under a task that does not
// exist.
func TestASummonsWithNoDevelopmentManagerTaskIsRefused(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{}
	tasks := map[string]config.RecurringTask{"pm-scan": {Role: domain.RoleProductManager, Every: config.Duration(time.Hour), Enabled: true, Prompt: "scan", MaxTurns: 1}}
	trigger := Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}
	hold := runstate.IntakeHold{HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: []runstate.BrakeBlockedRun{{WorkItemID: "x", Reason: "blocked"}}, CooldownEndsAt: recurringNow}}
	if _, err := trigger.Summon(context.Background(), BrakeSummons{Hold: hold}); !errors.Is(err, ErrNoSummonableTask) {
		t.Fatalf("Summon() error = %v, want %v", err, ErrNoSummonableTask)
	}
	if len(role.messages) != 0 {
		t.Fatalf("messages = %v, want nobody woken", role.messages)
	}
}

// A firing's problems joined past what the record accepts are cut on a rune
// boundary and marked, never stored with half a rune at the cut.
func TestABoundedProblemIsCutOnARuneBoundary(t *testing.T) {
	t.Parallel()

	problem := boundedProblem([]string{"x" + strings.Repeat("é", runstate.MaxSweepTextBytes)})
	if !utf8.ValidString(problem) || len(problem) > runstate.MaxSweepTextBytes || !strings.HasSuffix(problem, " […]") {
		t.Fatalf("boundedProblem() is %d bytes, valid UTF-8 %t; want valid text within %d bytes marked as cut",
			len(problem), utf8.ValidString(problem), runstate.MaxSweepTextBytes)
	}
}

// Every turn of a firing is told which pass it belongs to — the task and which of
// its firings this is — so what it writes on the pass's behalf can say so.
func TestEveryTurnOfAFiringIsToldItsPass(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{
		{result: &sweep.Result{Status: sweep.StatusMore, Summary: "half"}},
		{result: complete("the rest")},
	}}
	trigger := Trigger{Tasks: hourlyTask("look"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(role.passes) != 2 || role.passes[0] != "a-sweep#1" || role.passes[1] != "a-sweep#1" {
		t.Fatalf("the turns were told passes %v, want a-sweep#1 on both", role.passes)
	}
}

// fixedDocket is the docket as a test hands it to the trigger, counting how
// often it was read.
type fixedDocket struct {
	rendered string
	reads    int
}

func (d *fixedDocket) Window() string {
	d.reads++
	return d.rendered
}

// The development manager's pass carries the docket as it stands in the message
// that wakes her, read once for the firing however many turns it takes; a pass
// of any other role's carries none, because no other role decides about it.
func TestADevelopmentManagersFiringCarriesTheDocketAndNoOtherRolesDoes(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	docket := &fixedDocket{rendered: "## Triage docket\n\n[stopped run] on yoyodyne-ifd.428.29"}
	role := &wokenRole{answers: []scriptedTurn{
		{result: &sweep.Result{Status: sweep.StatusMore, Summary: "half"}},
		{result: complete("the rest")},
	}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Docket: docket, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(role.messages) != 2 || !strings.Contains(role.messages[0], "on yoyodyne-ifd.428.29") || !strings.Contains(role.messages[0], "read for this pass") {
		t.Fatalf("messages = %q, want the docket in the message that woke her", role.messages)
	}
	if strings.Contains(role.messages[1], "Triage docket") || docket.reads != 1 {
		t.Errorf("the docket was read %d time(s) and continued into %q, want it read once for the firing", docket.reads, role.messages[1])
	}

	other := sweepStore(t)
	tasks := hourlyTask("scan")
	task := tasks["a-sweep"]
	task.Role = domain.RoleProductManager
	tasks["a-sweep"] = task
	pm := &wokenRole{answers: []scriptedTurn{{result: complete("nothing")}}}
	unread := &fixedDocket{rendered: "## Triage docket"}
	trigger = Trigger{Tasks: tasks, Claims: other, Reports: other, Roles: pm, Docket: unread, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if unread.reads != 0 || strings.Contains(pm.messages[0], "Triage docket") {
		t.Errorf("a product manager's pass read the docket %d time(s):\n%s", unread.reads, pm.messages[0])
	}
}

// A summons is a firing of her sweep like any other, so it carries the docket
// beside the trip that summoned her.
func TestASummonsCarriesTheDocketBesideTheTrip(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	docket := &fixedDocket{rendered: "## Triage docket\n\n[stopped run] on yoyodyne-ifd.429.21"}
	role := &wokenRole{answers: []scriptedTurn{{result: complete("probed")}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Docket: docket, Clock: recurringClock{}}
	hold := runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "example",
		HeldAt:        recurringNow,
		HeldBy:        runstate.IntakeHolderBrake,
		Reason:        "3 run(s) blocked in a row",
	}
	if _, err := trigger.Summon(context.Background(), BrakeSummons{Hold: hold}); err != nil {
		t.Fatalf("Summon() error = %v", err)
	}
	if len(role.messages) != 1 || !strings.Contains(role.messages[0], "The intake brake summoned you now") || !strings.Contains(role.messages[0], "on yoyodyne-ifd.429.21") {
		t.Fatalf("messages = %q, want the trip and the docket in the summons", role.messages)
	}
}

// filedReports is the report pile as a missed cadence files into it.
type filedReports struct {
	filed []report.Report
}

func (f *filedReports) Append(reported report.Report) error {
	f.filed = append(f.filed, reported)
	return nil
}

// The schedule is read without firing it, so a session waiting on a run can
// wake when a task falls due: a task that has fired is due one interval after,
// one that has never fired is due at once, and reading claims nothing.
func TestTheCadenceSaysWhenEachTaskIsDueWithoutClaimingIt(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{{result: complete("nothing")}}}
	clock := &movingRecurringClock{now: recurringNow}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: clock}

	dues, err := trigger.Cadence(context.Background())
	if err != nil {
		t.Fatalf("Cadence() error = %v", err)
	}
	if len(dues) != 1 || !dues[0].At.IsZero() || dues[0].Every != time.Hour || dues[0].Role != domain.RoleDevelopmentManager {
		t.Fatalf("dues = %+v, want the task that has never fired due at once", dues)
	}
	if _, found, err := store.Find("a-sweep"); err != nil || found {
		t.Fatalf("Find() = %v, %v; want reading the cadence to claim nothing", found, err)
	}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	dues, err = trigger.Cadence(context.Background())
	if err != nil {
		t.Fatalf("Cadence() error = %v", err)
	}
	if len(dues) != 1 || !dues[0].At.Equal(recurringNow.Add(time.Hour)) {
		t.Fatalf("dues = %+v, want the task due an hour after it fired", dues)
	}
}

// A missed cadence is recorded in the sweep log beside the passes, spanning the
// gap and saying what kept it, and is said as the harness's own report at the
// severity it was given. The cadence is not moved: the task is still due.
func TestAMissedCadenceIsRecordedAndReported(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	filed := &filedReports{}
	due := recurringNow.Add(-2 * time.Hour)
	trigger := Trigger{
		Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: &wokenRole{},
		Breakage:    filed,
		Attribution: report.Attribution{ProductID: "example", RepositoryID: "example"},
		Clock:       recurringClock{},
	}

	err := trigger.Missed(context.Background(), RecurringMiss{
		Task: "a-sweep", Role: domain.RoleDevelopmentManager, Every: time.Hour, Due: due,
		Why:      "the harness could not fire its recurring schedule: the claim could not be taken",
		Severity: report.SeverityCritical,
	})
	if err != nil {
		t.Fatalf("Missed() error = %v", err)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded = %+v, want the miss in the sweep log", recorded)
	}
	missed := recorded[0]
	if missed.Turns != 0 || !missed.StartedAt.Equal(due) || !missed.EndedAt.Equal(recurringNow) {
		t.Errorf("recorded = %+v, want a firing that took no turn spanning the gap", missed)
	}
	for _, want := range []string{"fell due at " + due.Local().Format("2006-01-02 15:04 MST"), "2h0m0s later", "the claim could not be taken"} {
		if !strings.Contains(missed.Problem, want) {
			t.Errorf("problem = %q, want it to say %q", missed.Problem, want)
		}
	}
	if len(filed.filed) != 1 {
		t.Fatalf("filed = %+v, want the miss said as one report", filed.filed)
	}
	said := filed.filed[0]
	if said.Role != report.HarnessReporter || said.Severity != report.SeverityCritical {
		t.Errorf("report = %+v, want the harness's own at critical", said)
	}
	if !strings.Contains(said.Message, "the claim could not be taken") || !strings.Contains(said.Message, "a-sweep") {
		t.Errorf("message = %q, want the task and what kept it", said.Message)
	}
	if _, found, err := store.Find("a-sweep"); err != nil || found {
		t.Errorf("Find() = %v, %v; want the cadence left where it was", found, err)
	}
}

// The operator's pause is recorded against the cadence and said to nobody: a
// stop somebody placed on purpose is not breakage.
func TestAMissUnderThePauseIsRecordedAndNotReported(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	filed := &filedReports{}
	trigger := Trigger{
		Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: &wokenRole{},
		Breakage:    filed,
		Attribution: report.Attribution{ProductID: "example", RepositoryID: "example"},
		Clock:       recurringClock{},
	}
	if err := trigger.Missed(context.Background(), RecurringMiss{
		Task: "a-sweep", Role: domain.RoleDevelopmentManager, Every: time.Hour,
		Due: recurringNow.Add(-90 * time.Minute), Why: "the operator paused harness activity",
	}); err != nil {
		t.Fatalf("Missed() error = %v", err)
	}
	if recorded, _, _ := store.List(); len(recorded) != 1 {
		t.Errorf("recorded = %+v, want the miss in the sweep log", recorded)
	}
	if len(filed.filed) != 0 {
		t.Errorf("filed = %+v, want nothing said about the operator's own pause", filed.filed)
	}
}

// A gap already in the sweep log — recorded by the session that was running
// before a restart — is not recorded or reported a second time by the session
// that comes after it.
func TestAMissAlreadyRecordedIsNotRecordedAgain(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	filed := &filedReports{}
	trigger := Trigger{
		Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: &wokenRole{},
		Breakage:    filed,
		Attribution: report.Attribution{ProductID: "example", RepositoryID: "example"},
		Clock:       recurringClock{},
	}
	miss := RecurringMiss{
		Task: "a-sweep", Role: domain.RoleDevelopmentManager, Every: time.Hour,
		Due: recurringNow.Add(-2 * time.Hour), Why: "the harness could not be read", Severity: report.SeverityCritical,
	}
	for range 2 {
		if err := trigger.Missed(context.Background(), miss); err != nil {
			t.Fatalf("Missed() error = %v", err)
		}
	}
	if recorded, _, _ := store.List(); len(recorded) != 1 {
		t.Errorf("recorded = %+v, want the gap once", recorded)
	}
	if len(filed.filed) != 1 {
		t.Errorf("filed = %+v, want the gap said once", filed.filed)
	}
}

// What the real trigger does with a capacity refusal across hours of it: each
// cadence is a firing that reached nobody, recorded with the provider's words
// and its reset, and the cadence moves on from it. So no firing goes missing,
// and a session reading the schedule finds nothing to record as missed.
func TestACapacityRefusalIsRecordedAtEachCadenceAndMissesNothing(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	clock := &movingRecurringClock{now: recurringNow}
	refusal := fmt.Errorf("%w: api_error: You've hit your weekly limit · resets Sep 5 at 12:00Z", ErrRoleUnreachable)
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: &wokenRole{failure: refusal}, Clock: clock}

	for hour := range 3 {
		clock.now = recurringNow.Add(time.Duration(hour) * time.Hour)
		if _, err := trigger.Fire(context.Background()); err != nil {
			t.Fatalf("Fire() at hour %d error = %v", hour, err)
		}
	}

	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 3 {
		t.Fatalf("recorded = %+v, want one pass for each cadence", recorded)
	}
	for _, pass := range recorded {
		if pass.Turns != 0 || !strings.Contains(pass.Problem, "weekly limit") || !strings.Contains(pass.Problem, "resets Sep 5 at 12:00Z") {
			t.Errorf("pass = %+v, want a firing that reached nobody, naming the wait and its reset", pass)
		}
	}
	dues, err := trigger.Cadence(context.Background())
	if err != nil {
		t.Fatalf("Cadence() error = %v", err)
	}
	if len(dues) != 1 || !dues[0].At.Equal(clock.now.Add(time.Hour)) {
		t.Fatalf("dues = %+v, want the cadence moved on by the last refused firing", dues)
	}
	watch := recurringWatch{opened: recurringNow.Add(-time.Hour), missed: map[string]time.Time{}}
	schedule := Schedule{}
	Scheduler{Now: func() time.Time { return clock.now }}.missed(context.Background(), &schedule, Pull{Recurring: trigger}, &watch)
	if len(watch.missed) != 0 {
		t.Errorf("missed = %v, want nothing missed on a cadence the refusals kept moving", watch.missed)
	}
}
