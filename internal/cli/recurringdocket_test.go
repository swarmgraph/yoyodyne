package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// wakeCapture is a role's conversation as these tests reach it: every message a
// firing put into it.
type wakeCapture struct {
	messages []string
}

func (w *wakeCapture) Wake(_ context.Context, _ domain.AgentRole, _, _, _, message string, _ orchestrator.RecurringTurnOptions) (orchestrator.Turn, error) {
	w.messages = append(w.messages, message)
	return orchestrator.Turn{ConversationID: "chat-1", Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "looked"}}, nil
}

// threeStoppages records three runs of three items, each ended on a durable
// blocker an hour apart, and nothing else: no delivery of any of them to the
// development manager and no decision about any of them.
func threeStoppages(t *testing.T) *runstate.Store {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	for index, item := range []string{"yoyodyne-ifd.430.13.4", "yoyodyne-ifd.428.29", "yoyodyne-ifd.429.21"} {
		state := stoppedRunOf(item)
		state.RunID = fmt.Sprintf("run-%032x", index+1)
		completed := state.CompletedAt.Add(time.Duration(index) * time.Hour)
		state.CompletedAt = &completed
		state.UpdatedAt = completed
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	return store
}

func developmentManagerSweep() map[string]config.RecurringTask {
	return map[string]config.RecurringTask{
		"development-manager-sweep": {
			Role:     domain.RoleDevelopmentManager,
			Every:    config.Duration(time.Hour),
			Enabled:  true,
			Prompt:   "sweep for unresolved issues",
			MaxTurns: 1,
		},
	}
}

// A scheduled pass of the development manager's is handed the docket as it
// stands, in the message that wakes her, whether or not anything was delivered
// to her since her last turn. On 2026-09-25 three passes ran after the first of
// three approved changes stopped waiting on her and decided none of them: her
// conversation's docket was the one rendered when it opened, a pass resumes it,
// and stoppages otherwise reached her one per delivery.
func TestAScheduledSweepCarriesEveryUndecidedStoppageWithNoDeliveryMade(t *testing.T) {
	t.Parallel()

	runs := threeStoppages(t)
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	sweeps, err := runstate.NewSweepStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewSweepStore() error = %v", err)
	}
	role := &wakeCapture{}
	trigger := orchestrator.Trigger{
		Tasks:   developmentManagerSweep(),
		Claims:  sweeps,
		Reports: sweeps,
		Roles:   role,
		Docket:  sweepDocket{docketer: docketerOverDocket(runs, docket)},
	}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(role.messages) != 1 {
		t.Fatalf("messages = %d, want the one turn of the pass", len(role.messages))
	}
	message := role.messages[0]
	positions := make([]int, 0, 3)
	for index, item := range []string{"yoyodyne-ifd.430.13.4", "yoyodyne-ifd.428.29", "yoyodyne-ifd.429.21"} {
		run := fmt.Sprintf("run-%032x", index+1)
		at := strings.Index(message, "on "+item+" ("+run+")")
		if at < 0 {
			t.Fatalf("the pass was not handed the stoppage of %s (%s):\n%s", item, run, message)
		}
		positions = append(positions, at)
	}
	// Oldest first, as the conversation lists it.
	if positions[0] > positions[1] || positions[1] > positions[2] {
		t.Errorf("the stoppages are not listed oldest first: at %v", positions)
	}
	for _, want := range []string{
		"read for this pass",
		"## Triage docket",
		"sweep for unresolved issues",
		sweep.Fence,
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the wake message does not carry %q:\n%s", want, message)
		}
	}
	// The docket is ahead of the task, so what the task asks her to look over is
	// what she has just been shown.
	if strings.Index(message, "## Triage docket") > strings.Index(message, "sweep for unresolved issues") {
		t.Errorf("the docket follows the task rather than preceding it:\n%s", message)
	}
}

// An empty docket is said in words rather than left out, so a pass can tell
// nothing waiting from nothing read; and a docket that could not be built is
// said as unread, never as empty.
func TestASweepsDocketSaysWhenItIsEmptyAndWhenItCouldNotBeRead(t *testing.T) {
	t.Parallel()

	runs, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	empty := sweepDocket{docketer: docketerOverDocket(runs, docket)}.StartPass(nil).Window()
	if !strings.Contains(empty, "Nothing is on the docket") {
		t.Errorf("an empty docket rendered as %q, want it said in words", empty)
	}

	unread := sweepDocket{docketer: failingDocketer{}}.StartPass(nil).Window()
	for _, want := range []string{"could not be read", "the docket log is locked", "Do not assume nothing has stopped"} {
		if !strings.Contains(unread, want) {
			t.Errorf("an unreadable docket rendered as %q, want %q", unread, want)
		}
	}
}

type failingDocketer struct{}

func (failingDocketer) Build() (orchestrator.DocketBuild, error) {
	return orchestrator.DocketBuild{}, errors.New("the docket log is locked")
}

// A product with a docket wires it into the trigger, and one without wires
// nothing rather than a docket that can only fail.
func TestTheTriggerCarriesTheProductsDocketWhereThereIsOne(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	parts := components{config: config.Config{RecurringTasks: developmentManagerSweep()}, store: store}
	if trigger := recurringTrigger(parts, "", io.Discard).(*orchestrator.Trigger); trigger.Docket != nil {
		t.Errorf("docket = %#v, want none wired for a product with no docket", trigger.Docket)
	}
	parts.docket, err = runstate.NewDocketStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	if trigger := recurringTrigger(parts, "", io.Discard).(*orchestrator.Trigger); trigger.Docket == nil {
		t.Error("the trigger carries no docket, so a scheduled pass of the development manager's would see none")
	}
}

// fixedItems is a tracker listing every work item it was given.
type fixedItems []beads.WorkItem

func (f fixedItems) List(context.Context, string) ([]beads.WorkItem, error) { return f, nil }

// A pass's docket is the same window as her conversation's: an entry on a closed
// item is counted rather than listed, and the walk position the pass leaves is
// the one the next window, in a pass or in her conversation, resumes past.
func TestASweepsDocketIsTheLiveWindowAndAdvancesTheSharedWalk(t *testing.T) {
	t.Parallel()

	runs := threeStoppages(t)
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	pass := sweepDocket{
		docketer: docketerOverDocket(runs, docket),
		items:    fixedItems{{ID: "yoyodyne-ifd.430.13.4", Status: "closed"}},
		window:   docket,
	}
	delivery := pass.StartPass(nil)
	rendered := delivery.Window()
	if problem := delivery.Delivered(); problem != "" {
		t.Fatal(problem)
	}
	if strings.Contains(rendered, "on yoyodyne-ifd.430.13.4 (") {
		t.Errorf("an entry on a closed item was listed:\n%s", rendered)
	}
	for _, want := range []string{
		"on yoyodyne-ifd.428.29 (",
		"on yoyodyne-ifd.429.21 (",
		"1 docket entry(s) are not listed because the work item they stopped is closed.",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the pass's docket is missing %q:\n%s", want, rendered)
		}
	}
	position, err := docket.WindowPosition()
	if err != nil {
		t.Fatalf("WindowPosition() error = %v", err)
	}
	if position.Key != triage.Key(triage.ClassStoppedRun, fmt.Sprintf("run-%032x", 3)) {
		t.Errorf("the pass left the walk at %+v, want past the newest entry it listed", position)
	}
}

// A scheduled pass of the development manager's carries, beside the docket,
// every needs-a-human entry whose move is the operator's — among them an owed
// step and a publication named as his — each with its kind,
// what it says, since when in this machine's zone, and how long ago — and
// nothing another mover moves. On 2026-09-28 her sweep was asked to check what
// appears to wait on him and does not really need him, and was shown only her
// own docket to check it against.
func TestAScheduledSweepCarriesTheOperatorsEntriesWithTheirAges(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	escalated := now.Add(-3 * time.Hour)
	died := now.Add(-50 * time.Hour)
	failing := now.Add(-20 * time.Minute)
	owedEnded := now.Add(-5 * time.Hour)
	publishedEnded := now.Add(-26 * time.Hour)
	standing := readmodel.Standing{NeedsHuman: []readmodel.Attention{
		{
			Kind: readmodel.AttentionOperatorAction, ID: "run:run-escalated", Mover: readmodel.MoverOperator, WorkItemID: "yoyodyne-ifd.272",
			OperatorAction: &readmodel.OperatorAction{
				Key: "run:run-escalated", Subject: "yoyodyne-ifd.272", RunID: "run-escalated", WorkItemID: "yoyodyne-ifd.272",
				Needs: "the target branch diverged from the forge", RecordedIn: "the triage decision on run-escalated",
				FoundBy: "the development manager, escalating the stopped run to the operator", Since: escalated,
			},
		},
		{
			Kind: readmodel.AttentionDegradedService, ID: "dashboard", Mover: readmodel.MoverOperator,
			Service: &runstate.SupervisedChild{Service: "dashboard", Reason: "died 6 times within 2m0s of being started", DiedAt: died},
		},
		{
			Kind: readmodel.AttentionFailingTask, ID: "report-triage", Mover: readmodel.MoverOperator,
			FailingTask: &readmodel.FailingTask{
				Task: "report-triage", Role: domain.RoleProductManager, Cause: runstate.PreTurnConversationUnopened,
				Problem: "no agent fills the role", Failures: 2, FirstAt: failing, RaisedAt: failing, LatestAt: failing,
			},
		},
		{
			Kind: readmodel.AttentionOwedStep, ID: "run-owed", Mover: readmodel.MoverHarness, WorkItemID: "yoyodyne-ifd.400",
			OwedStep: &readmodel.OwedStep{Status: runstate.StatusSucceeded, EndedAt: owedEnded},
		},
		{
			Kind: readmodel.AttentionPublication, ID: "run-published", Mover: readmodel.MoverOperator, WorkItemID: "yoyodyne-ifd.401",
			Publication: &readmodel.Publication{
				TargetBranch: "main", Branch: "yoyodyne/yoyodyne-ifd-401/abc", EndedAt: publishedEnded,
				PullRequest: &runstate.PullRequest{Number: 42, URL: "https://example.test/pull/42"},
			},
		},
		{
			Kind: readmodel.AttentionHeldWork, Mover: readmodel.MoverDevelopmentManager,
			HeldWork: &readmodel.HeldWork{Awaiting: readmodel.HeldAwaitingDecision, Count: 4},
		},
	}}

	runs, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	sweeps, err := runstate.NewSweepStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewSweepStore() error = %v", err)
	}
	role := &wakeCapture{}
	trigger := orchestrator.Trigger{
		Tasks:   developmentManagerSweep(),
		Claims:  sweeps,
		Reports: sweeps,
		Roles:   role,
		Docket: sweepDocket{
			docketer: docketerOverDocket(runs, docket),
			standing: func() readmodel.Standing { return standing },
			now:      func() time.Time { return now },
		},
	}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(role.messages) != 1 {
		t.Fatalf("messages = %d, want the one turn of the pass", len(role.messages))
	}
	message := role.messages[0]
	local := func(moment time.Time) string { return moment.Local().Format("2006-01-02 15:04 MST") }
	for _, want := range []string{
		"## Triage docket",
		"## Waiting on the operator",
		"4 entries on the needs-a-human line are the operator's",
		"- [operator-action run:run-escalated, item yoyodyne-ifd.272] yoyodyne-ifd.272 needs your hand: the target branch diverged from the forge",
		"since " + local(escalated) + ", 3 hours ago",
		"- [degraded-service dashboard] the dashboard service is degraded: died 6 times within 2m0s of being started — since " + local(died) + ", 2 days ago",
		"- [failing-task report-triage] the recurring task report-triage has failed before its first turn 2 times in a row",
		"since " + local(failing) + ", 20 minutes ago",
		"- [publication run-published, item yoyodyne-ifd.401] run run-published promoted yoyodyne-ifd.401 into main and the forge has not published it",
		"since " + local(publishedEnded) + ", 26 hours ago",
		"file a defect with the Lead Product Manager saying why it reached him",
		"Record what you did on the record the entry is about",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the wake message does not carry %q:\n%s", want, message)
		}
	}
	if strings.Contains(message, "run-owed") || strings.Contains(message, "admitted items await the development manager's decision") {
		t.Errorf("an entry the development manager moves was carried as the operator's:\n%s", message)
	}
	// Beside the docket, and ahead of the task that asks her to check them.
	if strings.Index(message, "## Triage docket") > strings.Index(message, "## Waiting on the operator") ||
		strings.Index(message, "## Waiting on the operator") > strings.Index(message, "sweep for unresolved issues") {
		t.Errorf("the operator's entries are not between the docket and the task:\n%s", message)
	}
}

// A line with nothing of the operator's on it says so in words, and one that
// could not be read whole says that, so a pass can tell nothing waiting on him
// from nothing read.
func TestASweepSaysWhenNothingWaitsOnTheOperatorAndWhenTheLineWasNotRead(t *testing.T) {
	t.Parallel()

	empty := readmodel.Standing{}.RenderOperatorWaits(time.Now())
	if !strings.Contains(empty, "Nothing on the needs-a-human line is the operator's.") {
		t.Errorf("an empty line rendered as %q, want it said in words", empty)
	}
	partial := readmodel.Standing{NeedsHumanProblem: "the directive log is locked"}.RenderOperatorWaits(time.Now())
	if !strings.Contains(partial, "not fully read: the directive log is locked") {
		t.Errorf("a partial reading rendered as %q, want it said", partial)
	}
}
