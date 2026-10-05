package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The conversation steers work through this, which is a compile-time fact
// worth stating where the construction lives.
var _ chat.Work = conversationWork{}

func TestSurveyReadsRunStateAndEveryTrackerGroup(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	resetsAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	if err := store.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         "run-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.16",
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     started,
		UpdatedAt:     started,
		// A run waiting out a usage limit is in flight and owed a continuation,
		// so the survey has to say that rather than only that it is running.
		UsageLimitResetsAt: &resetsAt,
		UsageLimitKind:     "five-hour",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	runner := &recordingRunner{stdout: `[{"id":"yoyodyne-9","title":"Pause on a usage limit","status":"open","priority":2,"issue_type":"task"}]`}
	work := conversationWork{
		tracker: chatTracker(runner, "/repo"),
		store:   store,
		timeout: chatTrackerTimeout,
	}
	survey, err := work.Survey(context.Background())
	if err != nil {
		t.Fatalf("Survey() error = %v", err)
	}

	if len(survey.InFlight) != 1 {
		t.Fatalf("in flight = %#v", survey.InFlight)
	}
	inFlight := survey.InFlight[0]
	if inFlight.WorkItemID != "yoyodyne-ifd.16" || inFlight.Status != "running" || inFlight.Phase != "developing" {
		t.Fatalf("in-flight run = %#v", inFlight)
	}
	if !strings.Contains(inFlight.Detail, "five-hour usage limit until 2026-08-16T09:00:00Z") {
		t.Fatalf("in-flight detail = %q", inFlight.Detail)
	}

	// Every group is asked for by name, in the order the survey reports them,
	// so a group is never missing merely because the tracker's default listing
	// left it out.
	wantStatuses := []string{"--status=in_progress", "--status=blocked", "--status=open", "--status=closed"}
	if len(runner.commands) != len(wantStatuses) {
		t.Fatalf("tracker commands = %#v", runner.commands)
	}
	for i, command := range runner.commands {
		if command.Name != "bd" || command.Dir != "/repo" || command.Timeout != chatTrackerTimeout {
			t.Fatalf("command %d = %#v", i, command)
		}
		if !contains(command.Args, wantStatuses[i]) {
			t.Fatalf("command %d args = %#v, want %s", i, command.Args, wantStatuses[i])
		}
	}
	for name, group := range map[string][]chat.WorkItemSummary{
		"claimed":   survey.Claimed,
		"blocked":   survey.Blocked,
		"available": survey.Available,
		"completed": survey.Completed,
	} {
		if len(group) != 1 || group[0].ID != "yoyodyne-9" || group[0].Priority != 2 {
			t.Fatalf("%s = %#v", name, group)
		}
	}
}

// The backlog a conversation shows is assembled from the tracker the harness
// already reads, so the order the operator sees is the order that is actually
// pulled from rather than a second account of it.
func TestBacklogIsTheAdmittedWorkInPriorityOrder(t *testing.T) {
	t.Parallel()

	runner := &statusRunner{stdout: map[string]string{
		// A listing carrying the dependency bd records between two items, in the
		// shape bd reports one: the client decodes it, and the queue has to hold
		// the waiting item back and say what it waits for.
		"--status=open": `[{"id":"yoyodyne-ifd.3","title":"The scheduler that runs it","status":"open","priority":0,"issue_type":"task",
		                     "dependencies":[{"issue_id":"yoyodyne-ifd.3","depends_on_id":"yoyodyne-ifd.4","dependency_type":"blocks","status":"open"}]},
		                   {"id":"yoyodyne-ifd.26","title":"See and stop what is pulled","status":"open","priority":3,"issue_type":"task"},
		                   {"id":"yoyodyne-ifd.4","title":"The development manager that pulls","status":"open","priority":1,"issue_type":"task"}]`,
		// Admitted work the harness has blocked is still queued: it is unfinished
		// work in the order, and leaving it out would understate the backlog.
		"--status=blocked": `[{"id":"yoyodyne-ifd.9","title":"A run that failed","status":"blocked","priority":0,"issue_type":"task"}]`,
		// What can actually be pulled is the tracker's own answer.
		"ready": `[{"id":"yoyodyne-ifd.4","title":"The development manager that pulls","status":"open","priority":1,"issue_type":"task"},
		           {"id":"yoyodyne-ifd.26","title":"See and stop what is pulled","status":"open","priority":3,"issue_type":"task"}]`,
	}}
	work := conversationWork{tracker: chatTracker(runner, "/repo"), timeout: chatTrackerTimeout}

	queue, err := work.Backlog(context.Background())
	if err != nil {
		t.Fatalf("Backlog() error = %v", err)
	}
	var order []string
	for _, entry := range queue.Entries {
		order = append(order, entry.ID)
	}
	if strings.Join(order, ",") != "yoyodyne-ifd.3,yoyodyne-ifd.9,yoyodyne-ifd.4,yoyodyne-ifd.26" {
		t.Fatalf("backlog order = %v", order)
	}
	// The dependency survived the tracker's JSON, so the entry says what it is
	// waiting for rather than only that it is not ready.
	waiting := queue.Entries[0]
	if waiting.Ready || len(waiting.WaitingOn) != 1 || waiting.WaitingOn[0] != "yoyodyne-ifd.4" {
		t.Fatalf("waiting entry = %#v", waiting)
	}
	// A dependency-blocked item and a harness-blocked one both lead the order and
	// neither is what gets pulled.
	next, ok := queue.Next()
	if !ok || next.ID != "yoyodyne-ifd.4" {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
	if !contains(runner.subcommands(), "ready") {
		t.Fatalf("the backlog never asked the tracker what is ready: %#v", runner.subcommands())
	}
	for i, command := range runner.commands {
		if command.Name != "bd" || command.Dir != "/repo" || command.Timeout != chatTrackerTimeout {
			t.Fatalf("command %d = %#v", i, command)
		}
	}
}

// The failure this guards against is silent: a listing that carries no
// dependency data looks exactly like work with nothing in its way, so a queue
// that decided readiness from the listing alone would name a blocked item as the
// next thing to pull. Readiness comes from the tracker instead, which answers
// that question from its own dependency graph.
func TestBacklogDoesNotCallAnItemReadyOnAListingWithNoDependencies(t *testing.T) {
	t.Parallel()

	runner := &statusRunner{stdout: map[string]string{
		// Neither item carries a dependency here, which is what a listing without
		// dependency data looks like from the harness's side.
		"--status=open": `[{"id":"yoyodyne-ifd.3","title":"Blocked, though this listing does not say so","status":"open","priority":0,"issue_type":"task"},
		                   {"id":"yoyodyne-ifd.4","title":"The development manager that pulls","status":"open","priority":1,"issue_type":"task"}]`,
		"ready": `[{"id":"yoyodyne-ifd.4","title":"The development manager that pulls","status":"open","priority":1,"issue_type":"task"}]`,
	}}
	work := conversationWork{tracker: chatTracker(runner, "/repo"), timeout: chatTrackerTimeout}

	queue, err := work.Backlog(context.Background())
	if err != nil {
		t.Fatalf("Backlog() error = %v", err)
	}
	if queue.Entries[0].ID != "yoyodyne-ifd.3" || queue.Entries[0].Ready {
		t.Fatalf("the head of the queue was reported ready without the tracker offering it: %#v", queue.Entries[0])
	}
	next, ok := queue.Next()
	if !ok || next.ID != "yoyodyne-ifd.4" {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
	// It says why it is holding the item rather than inventing a blocker.
	if !strings.Contains(queue.Render(), "the tracker does not report it as ready to pull") {
		t.Fatalf("rendered backlog = %q", queue.Render())
	}
}

// A readiness answer nobody could read decides nothing, so the backlog is
// refused rather than reported with everything in it held back — which would
// read as a queue that had stalled.
func TestBacklogFailsWhenTheTrackerWillNotSayWhatIsReady(t *testing.T) {
	t.Parallel()

	runner := &statusRunner{
		stdout: map[string]string{
			"--status=open": `[{"id":"yoyodyne-ifd.4","title":"The development manager that pulls","status":"open","priority":1,"issue_type":"task"}]`,
		},
		fail: map[string]bool{"ready": true},
	}
	work := conversationWork{tracker: chatTracker(runner, "/repo"), timeout: chatTrackerTimeout}

	queue, err := work.Backlog(context.Background())
	if err == nil || !strings.Contains(err.Error(), "reports as ready") {
		t.Fatalf("Backlog() error = %v, want the unreadable readiness reported", err)
	}
	if len(queue.Entries) != 0 {
		t.Fatalf("a backlog that could not be read still returned %#v", queue.Entries)
	}
}

// A queue read from half a tracker would answer "what is next" wrongly rather
// than incompletely, so it is refused instead of reported.
func TestBacklogFailsRatherThanReportingHalfAQueue(t *testing.T) {
	t.Parallel()

	runner := &flakyRunner{stdout: `[{"id":"yoyodyne-9","title":"Pause on a usage limit","status":"open","priority":2,"issue_type":"task"}]`, failAfter: 1}
	work := conversationWork{tracker: chatTracker(runner, "/repo"), timeout: chatTrackerTimeout}

	queue, err := work.Backlog(context.Background())
	if err == nil {
		t.Fatalf("Backlog() error = nil, want the unreadable slice reported; queue = %#v", queue)
	}
	if len(queue.Entries) != 0 {
		t.Fatalf("a backlog that could not be read still returned %#v", queue.Entries)
	}
}

func TestSurveyNamesThePartsItCouldNotReadInsteadOfFailing(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// The tracker answers for the first group and then stops answering, which
	// is what a tracker that becomes unavailable mid-survey looks like.
	runner := &flakyRunner{stdout: `[{"id":"yoyodyne-9","title":"Pause on a usage limit","status":"in_progress","issue_type":"task"}]`, failAfter: 1}
	work := conversationWork{tracker: chatTracker(runner, "/repo"), store: store, timeout: chatTrackerTimeout}

	survey, err := work.Survey(context.Background())
	if err != nil {
		t.Fatalf("Survey() error = %v, want the failures reported inside the survey", err)
	}
	if len(survey.Claimed) != 1 {
		t.Fatalf("claimed = %#v, want the group that was readable", survey.Claimed)
	}
	if len(survey.Unavailable) != 3 {
		t.Fatalf("unavailable = %#v, want the three groups that could not be read", survey.Unavailable)
	}
	rendered := survey.Render(console.Theme{})
	// An unreadable group must never read as an empty one: it says so where it
	// would have been listed.
	if !strings.Contains(rendered, "blocked: could not be read, so treat it as unknown rather than empty: list blocked work items") {
		t.Fatalf("rendered survey = %q", rendered)
	}
	if strings.Contains(rendered, "blocked: none") {
		t.Fatalf("rendered survey described an unreadable group as empty: %q", rendered)
	}
	if !strings.Contains(rendered, "claimed (1):") {
		t.Fatalf("rendered survey lost the group it could read: %q", rendered)
	}
}

func TestDirectRecordsOperatorDirectionWithoutChangingStatus(t *testing.T) {
	t.Parallel()

	// The answer carries the direction, which is what bd's own answer to an append
	// does: a direction reported as recorded and not on the item is refused rather
	// than reported, so a fixture that dropped it would be a tracker that lost it.
	runner := &recordingRunner{stdout: `[{"id":"yoyodyne-9","title":"Pause on a usage limit","status":"open",` +
		`"issue_type":"task","notes":"prefer the smaller change"}]`}
	work := conversationWork{tracker: chatTracker(runner, "/repo"), timeout: chatTrackerTimeout}
	if err := work.Direct(context.Background(), "yoyodyne-9", "prefer the smaller change"); err != nil {
		t.Fatalf("Direct() error = %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("tracker commands = %#v", runner.commands)
	}
	args := runner.commands[0].Args
	if !contains(args, "--append-notes="+beads.FrameNote("prefer the smaller change")) {
		t.Fatalf("direction was not appended to the item's notes: %#v", args)
	}
	// Direction says what to do differently. Deciding the item is done, blocked,
	// or claimed is not part of saying it.
	for _, argument := range args {
		if strings.HasPrefix(argument, "--status") || argument == "close" || argument == "--claim" {
			t.Fatalf("recording direction changed the item's state: %#v", args)
		}
	}
}

// The claim /redirect rests on is that the direction reaches whoever picks the
// work up next. This walks that path with the real code on both sides of the
// tracker: the direction is appended to the item's notes, the item the next
// attempt claims carries them, and the context the pipeline assembles from that
// item (pipeline.go assembles exactly this bundle from the claimed item) puts
// them in front of the developer.
func TestDirectionRecordedInAConversationIsReadByTheNextAttempt(t *testing.T) {
	t.Parallel()

	direction := "Operator direction from product-manager conversation chat-1, after turn 2. The next attempt at this item is expected to follow it.\n\nKeep the CLI surface unchanged."
	runner := &appendingRunner{id: "yoyodyne-9", title: "Pause on a usage limit"}
	work := conversationWork{tracker: chatTracker(runner, "/repo"), timeout: chatTrackerTimeout}
	if err := work.Direct(context.Background(), "yoyodyne-9", direction); err != nil {
		t.Fatalf("Direct() error = %v", err)
	}

	// What the next attempt does: claim the item and assemble its context.
	item, _, err := chatTracker(runner, "/repo").Claim(context.Background(), "yoyodyne-9")
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	bundle, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: t.TempDir(), WorkItem: item})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "Keep the CLI surface unchanged.") {
		t.Fatalf("developer context = %q, want it to carry the operator's direction", bundle.Text)
	}
	// It arrives as the item's notes, which is where the developer reads what it
	// has been told rather than somewhere it might skip.
	notesAt := strings.Index(bundle.Text, "## Notes")
	if notesAt < 0 || strings.Index(bundle.Text, "Keep the CLI surface unchanged.") < notesAt {
		t.Fatalf("developer context = %q, want the direction under the item's notes", bundle.Text)
	}
}

func TestRunReportClaimsIntegrationOnlyFromARecordedPromotion(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		outcome orchestrator.Outcome
		want    chat.RunReport
	}{
		{
			name: "an integrated run reports where the work landed",
			outcome: orchestrator.Outcome{
				RunID: "run-1", WorkItemID: "yoyodyne-9", Status: runstate.StatusSucceeded, Branch: "b",
				Integration:    &gitworktree.Integration{TargetBranch: "main", SourceCommit: "abc123", TargetCommit: "abc123"},
				WorkItemClosed: true,
			},
			want: chat.RunReport{
				RunID: "run-1", WorkItemID: "yoyodyne-9", Status: "succeeded", Branch: "b",
				Integrated: true, TargetBranch: "main", Commit: "abc123", WorkItemClosed: true,
			},
		},
		{
			name: "a blocked run reports the blocker and the artifacts it kept",
			outcome: orchestrator.Outcome{
				RunID: "run-2", WorkItemID: "yoyodyne-9", Status: runstate.StatusFailed, Branch: "b",
				WorktreePath: "/wt", Blocked: true, RepairAttempts: 2, Failure: "the reviewer's findings are unresolved",
			},
			want: chat.RunReport{
				RunID: "run-2", WorkItemID: "yoyodyne-9", Status: "failed", Branch: "b",
				WorktreePath: "/wt", Blocked: true, RepairAttempts: 2, Failure: "the reviewer's findings are unresolved",
			},
		},
		{
			name: "a paused run is reported as still in flight",
			outcome: orchestrator.Outcome{
				RunID: "run-3", WorkItemID: "yoyodyne-9", Status: runstate.StatusRunning,
				Paused: true, UsageLimitKind: "weekly",
			},
			want: chat.RunReport{
				RunID: "run-3", WorkItemID: "yoyodyne-9", Status: "running", Paused: true, UsageLimitKind: "weekly",
			},
		},
		{
			name: "a run whose provider was stopped on time reports why, and no failure",
			outcome: orchestrator.Outcome{
				RunID: "run-4", WorkItemID: "yoyodyne-9", Status: runstate.StatusRunning,
				Paused: true, ProviderStop: runstate.ProviderStopStalled,
			},
			want: chat.RunReport{
				RunID: "run-4", WorkItemID: "yoyodyne-9", Status: "running", Paused: true, ProviderStop: "stalled",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := runReportOf(test.outcome); got != test.want {
				t.Fatalf("runReportOf() = %#v, want %#v", got, test.want)
			}
		})
	}
}

// appendingRunner stands in for the one thing this depends on bd to do: an
// appended note becomes part of the item the tracker reports afterwards.
// Everything downstream of that — decoding the item and assembling the
// developer's context from it — is the real code under test.
type appendingRunner struct {
	id    string
	title string
	notes string
}

func (r *appendingRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	for _, argument := range command.Args {
		note, appended := strings.CutPrefix(argument, "--append-notes=")
		if !appended {
			continue
		}
		if r.notes != "" {
			r.notes += "\n\n"
		}
		r.notes += note
	}
	encoded, err := json.Marshal([]map[string]any{{
		"id":         r.id,
		"title":      r.title,
		"status":     "in_progress",
		"issue_type": "task",
		"notes":      r.notes,
	}})
	if err != nil {
		return execution.ProcessResult{}, err
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(encoded)}, nil
}

// flakyRunner answers a fixed number of commands and then fails, so a survey
// can be checked against a tracker that stops answering partway through.
// statusRunner answers each tracker command with the slice it asked for, keyed
// by the subcommand or the status argument. That is what lets a test tell apart
// work that came from one listing, work that came from another, and the
// tracker's own answer about what can be pulled.
type statusRunner struct {
	stdout   map[string]string
	fail     map[string]bool
	commands []execution.Command
}

func (r *statusRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands = append(r.commands, command)
	for _, key := range command.Args {
		if r.fail[key] {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "bd is unavailable"}, nil
		}
		if answer, asked := r.stdout[key]; asked {
			return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: answer}, nil
		}
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]"}, nil
}

// subcommands names the bd commands that were run, which is how a test asserts
// that a question was actually asked of the tracker rather than answered locally.
func (r *statusRunner) subcommands() []string {
	run := make([]string, 0, len(r.commands))
	for _, command := range r.commands {
		if len(command.Args) > 0 {
			run = append(run, command.Args[0])
		}
	}
	return run
}

type flakyRunner struct {
	stdout    string
	failAfter int
	commands  int
}

func (r *flakyRunner) Run(_ context.Context, _ execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands++
	if r.commands > r.failAfter {
		return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "bd is unavailable"}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: r.stdout}, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// What a run changed is answered from the durable record and nothing else. That
// is what makes the answer survive success: a run is cleaned up once it
// integrates — worktree removed, branch deleted — so anything that answered by
// diffing a tree would stop having an answer exactly when the work landed.
// The price a completed run recorded is carried by the tracker, so a survey
// reads it beside everything else about the item rather than from a second
// place that could disagree with it.
func TestSurveyCarriesThePriceTheTrackerHolds(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	runner := &recordingRunner{stdout: `[
	  {"id":"yoyodyne-ifd.2.7","title":"Resume an interrupted run","status":"closed","priority":1,"issue_type":"task",
	   "metadata":{"yoyodyne_cost_usd":27.93,"yoyodyne_cost_runs":2}},
	  {"id":"yoyodyne-ifd.13","title":"Publish a pull request","status":"closed","priority":2,"issue_type":"task"}
	]`}
	work := conversationWork{tracker: chatTracker(runner, "/repo"), store: store, timeout: chatTrackerTimeout}
	survey, err := work.Survey(context.Background())
	if err != nil {
		t.Fatalf("Survey() error = %v", err)
	}
	if len(survey.Completed) != 2 {
		t.Fatalf("completed = %#v", survey.Completed)
	}
	priced := survey.Completed[0]
	if priced.Cost == nil || priced.Cost.TotalUSD != 27.93 || priced.Cost.Runs != 2 || priced.Cost.UnknownRuns != 0 {
		t.Fatalf("priced item = %#v", priced.Cost)
	}
	// An item nothing has priced carries no price, which is not the same fact as
	// an item that cost nothing.
	if survey.Completed[1].Cost != nil {
		t.Fatalf("unpriced item = %#v, want no price", survey.Completed[1].Cost)
	}
}

// An item's price comes from the runs recorded for it, which is what lets an
// item closed long before anything recorded a price still be priced.
func TestPriceReadsEveryRecordedRunOfAnItem(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	for index, run := range []struct {
		runID     string
		status    runstate.Status
		startedAt time.Time
		cost      float64
	}{
		{runID: "run-fedcba9876543210fedcba9876543210", status: runstate.StatusFailed, startedAt: started.Add(-2 * time.Hour), cost: 8.91},
		{runID: "run-0123456789abcdef0123456789abcdef", status: runstate.StatusSucceeded, startedAt: started, cost: 19.02},
	} {
		completed := run.startedAt.Add(time.Hour)
		if err := store.Create(runstate.State{
			SchemaVersion:     runstate.StateSchemaVersion,
			RunID:             run.runID,
			ProductID:         "yoyodyne",
			RepositoryID:      "yoyodyne",
			WorkItemID:        "yoyodyne-ifd.2.7",
			Backend:           domain.BackendClaudeCode,
			Status:            run.status,
			Phase:             runstate.PhaseReviewing,
			StartedAt:         run.startedAt,
			UpdatedAt:         completed,
			CompletedAt:       &completed,
			ProviderSessionID: "session-1",
		}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		event, err := execution.NewEvent(run.runID, uint64(index+1), completed, execution.EventRunCompleted, "claude-code",
			map[string]any{"session_id": "session-1", "total_cost_usd": run.cost})
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		if err := store.AppendEvent(event); err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}

	work := conversationWork{store: store, timeout: chatTrackerTimeout}
	price, err := work.Price(context.Background(), "yoyodyne-ifd.2.7")
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.TotalUSD != 27.93 || len(price.Runs) != 2 || price.UnknownRuns != 0 {
		t.Fatalf("Price() = %#v", price)
	}
	// Oldest first: the rejected attempt, then the one that finished the work.
	if price.Runs[0].Status != "failed" || price.Runs[0].CostUSD != 8.91 || price.Runs[1].CostUSD != 19.02 {
		t.Fatalf("Price() runs = %#v", price.Runs)
	}
	rendered := price.Render()
	for _, required := range []string{"cost: $27.93 across 2 run(s)", "$8.91 from 1 invocation(s)"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered price = %q, want it to contain %q", rendered, required)
		}
	}

	// An item nothing has run has no price rather than a price of nothing.
	unrun, err := work.Price(context.Background(), "yoyodyne-ifd.99")
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if len(unrun.Runs) != 0 || unrun.TotalUSD != 0 {
		t.Fatalf("Price() = %#v, want nothing recorded", unrun)
	}
}

func TestChangesAnswerFromTheRecordEvenAfterCleanup(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	completed := started.Add(time.Hour)
	cleaned := runstate.State{
		SchemaVersion:   runstate.StateSchemaVersion,
		RunID:           "run-0123456789abcdef0123456789abcdef",
		ProductID:       "yoyodyne",
		RepositoryID:    "yoyodyne",
		WorkItemID:      "yoyodyne-ifd.39",
		Backend:         domain.BackendClaudeCode,
		Status:          runstate.StatusSucceeded,
		Phase:           runstate.PhaseComplete,
		StartedAt:       started,
		UpdatedAt:       completed,
		CompletedAt:     &completed,
		WorktreePath:    "/state/worktrees/yoyodyne-ifd.39",
		Branch:          "yoyodyne/yoyodyne-ifd.39/abcd",
		BaseCommit:      strings.Repeat("a", 40),
		WorktreeRemoved: true,
		BranchRemoved:   true,
		// A promotion is only recorded on a run that developed and reviewed its
		// work, so the record carries that evidence too.
		ProviderSessionID:     "session-1",
		ProviderModel:         "opus",
		ProviderResolvedModel: "claude-opus-5",
		ReviewSessionID:       "session-2",
		ReviewModel:           "opus",
		ReviewResolvedModel:   "claude-opus-5",
		ReviewDecision:        runstate.ReviewApprove,
		Changes:               runstate.RecordChanges("M internal/chat/chat.go", " internal/chat/chat.go | 12 ++++++++----"),
		Integration: &runstate.Integration{
			TargetBranch:         "main",
			SourceCommit:         strings.Repeat("b", 40),
			TargetCommit:         strings.Repeat("b", 40),
			PreviousTargetCommit: strings.Repeat("a", 40),
		},
		PullRequest: &runstate.PullRequest{
			Remote:     "origin",
			Branch:     "yoyodyne/yoyodyne-ifd.39/abcd",
			Number:     19,
			URL:        "https://forge/pull/19",
			HeadCommit: strings.Repeat("b", 40),
			State:      "closed",
			Merged:     true,
		},
	}
	if err := store.Create(cleaned); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// An earlier attempt at the same item, so the answer is about the latest one
	// rather than whichever file the directory listed first.
	earlier := cleaned
	earlier.RunID = "run-fedcba9876543210fedcba9876543210"
	earlier.StartedAt = started.Add(-2 * time.Hour)
	earlier.UpdatedAt = earlier.StartedAt
	earlierCompleted := earlier.StartedAt
	earlier.CompletedAt = &earlierCompleted
	earlier.Status = runstate.StatusFailed
	earlier.Phase = runstate.PhaseDeveloping
	earlier.Integration = nil
	earlier.PullRequest = nil
	// It failed, so its artifacts were preserved rather than cleaned up.
	earlier.WorktreeRemoved = false
	earlier.BranchRemoved = false
	earlier.ReviewDecision = ""
	earlier.Changes = runstate.RecordChanges("M internal/chat/steer.go", "")
	if err := store.Create(earlier); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	work := conversationWork{store: store, timeout: chatTrackerTimeout}
	changes, err := work.Changes(context.Background(), "yoyodyne-ifd.39")
	if err != nil {
		t.Fatalf("Changes() error = %v", err)
	}
	if changes.RunID != cleaned.RunID {
		t.Fatalf("Changes() described %s, want the most recent run %s", changes.RunID, cleaned.RunID)
	}
	if changes.Files != "M internal/chat/chat.go" || changes.DiffStat == "" {
		t.Fatalf("Changes() = %#v, want the recorded account of the change", changes)
	}
	// The worktree is gone, and the answer says so rather than pointing at a
	// directory that is not there.
	if changes.Preserved {
		t.Fatalf("Changes() = %#v, want a removed worktree reported as removed", changes)
	}
	if !changes.Integrated || changes.TargetBranch != "main" {
		t.Fatalf("Changes() = %#v, want the recorded promotion", changes)
	}
	if changes.PullRequest == nil || changes.PullRequest.Number != 19 || !changes.PullRequest.Merged {
		t.Fatalf("Changes() = %#v, want the published request to point at", changes)
	}
	rendered := changes.Render()
	for _, required := range []string{", removed\n", "pull request: #19", "M internal/chat/chat.go"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered changes = %q, want it to contain %q", rendered, required)
		}
	}

	if _, err := work.Changes(context.Background(), "yoyodyne-ifd.99"); err == nil {
		t.Fatal("Changes() invented a run for a work item nothing has run")
	}
}
