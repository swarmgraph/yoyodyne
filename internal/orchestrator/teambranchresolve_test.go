package orchestrator

// A conflict met while bringing the target branch up to date with the team's
// branch, resolved by a run the development manager decided on, over the same
// real Git the update's own tests use.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// conflictFixture is an update waiting on a conflict in app.txt, with the item
// that touched it waiting for the team's merge.
func conflictFixture(t *testing.T) (*teamUpdateFixture, string, string) {
	t.Helper()
	f := newTeamUpdateFixture(t)
	landing := f.landsOnTarget("app.txt", "one\nstaging two\nthree\n", "staging changes the second line")
	f.waiting.runs = []runstate.State{{
		RunID: "run-staging-item", WorkItemID: "yoyodyne-task", WorkItemTitle: "Reword the greeting", Status: runstate.StatusSucceeded,
		Integration: &runstate.Integration{TargetBranch: "staging", SourceCommit: landing, TargetCommit: landing},
		TeamMerge:   &runstate.TeamMerge{TargetBranch: "staging", UpstreamBranch: "main", LandingCommit: landing, Since: time.Now()},
	}}
	tip := f.teamMerges("app.txt", "one\nteam two\nthree\n", "the team changes the second line")
	if outcome := f.update(); len(outcome.Conflict) != 1 {
		t.Fatalf("Update() = %#v, want the conflict", outcome)
	}
	return f, landing, tip
}

func (f *teamUpdateFixture) decide() (runstate.TeamBranchUpdate, error) {
	f.t.Helper()
	return f.records.DecideConflict(context.Background(), "staging", runstate.TeamBranchConflictDecision{
		Item:   runstate.TeamBranchUpdateConflictItem{ID: "yoyodyne-task", Title: "Reword the greeting"},
		Reason: "the item changed that line on staging, so it knows both wordings", DecidedBy: "the development manager in conversation chat-test, turn 1",
		DecidedAt: time.Now(),
	})
}

func writeIn(directory, file, content string) func(backend.RunRequest) error {
	return func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, directory, file), []byte(content), 0o600)
	}
}

func TestADecidedRunResolvesAConflictAndTheMergeLandsThroughTheChecksAndTheReview(t *testing.T) {
	t.Parallel()

	f, landing, tip := conflictFixture(t)
	f.developer = writeIn("", "app.txt", "one\nstaging and team two\nthree\n")
	checksBefore := f.checks.calls()

	if _, err := f.decide(); err != nil {
		t.Fatalf("decide: %v", err)
	}
	outcome := f.update()
	if outcome.Landed == "" || f.local("staging") != outcome.Landed {
		t.Fatalf("Update() = %#v, want the run's merge landed on staging", outcome)
	}
	if parents := gitLine(t, f.repository, "rev-list", "--parents", "-n", "1", outcome.Landed); parents != outcome.Landed+" "+landing+" "+tip {
		t.Fatalf("landed parents = %q, want the target it was built on first and the team's tip second", parents)
	}
	if content := gitLine(t, f.repository, "show", outcome.Landed+":app.txt"); !strings.Contains(content, "staging and team two") || strings.Contains(content, "<<<<<<<") {
		t.Fatalf("landed app.txt = %q, want the developer's resolution", content)
	}
	if f.checks.calls() == checksBefore || f.reviewer.calls() != 1 {
		t.Fatalf("checks ran %d time(s) more and the reviewer %d time(s), want the merge checked and reviewed once", f.checks.calls()-checksBefore, f.reviewer.calls())
	}
	record := f.record()
	if len(record.Landed) != 1 || record.Landed[0].ResolvedBy == "" || record.ConflictRounds != 0 || record.Decision != nil || record.Pending != nil {
		t.Fatalf("record = %#v, want the update landed as resolved by a run, with the rounds counted afresh", record)
	}
	// The run is the item's own, counted nowhere against its repair attempts.
	run, err := f.pipeline.Store.Load(record.Landed[0].ResolvedBy)
	if err != nil {
		t.Fatal(err)
	}
	if run.WorkItemID != "yoyodyne-task" || run.Status != runstate.StatusSucceeded || run.RepairAttempts != 0 || run.TeamConflict == nil ||
		run.TeamConflict.Round != 1 || run.TeamConflict.Merge == "" || run.Integration != nil {
		t.Fatalf("run = %#v, want a finished run of the item for round 1 that promoted nothing and spent no repair attempt", run)
	}
	if told := f.toldTheDevelopmentManager(); len(told) != 1 {
		t.Fatalf("%d reports, want only the one about the conflict", len(told))
	}
}

func TestAConflictReportSaysWhatTheDevelopmentManagerCanDecide(t *testing.T) {
	t.Parallel()

	f, _, _ := conflictFixture(t)
	told := f.toldTheDevelopmentManager()
	if len(told) != 1 {
		t.Fatalf("%d reports, want one", len(told))
	}
	for _, want := range []string{
		"Reword the greeting (yoyodyne-task)", "You can decide to have a run resolve it", "name one of Reword the greeting (yoyodyne-task)",
		"3 of the update's", "3 left", "not charged to the item's repair attempts", "independent review",
	} {
		if !strings.Contains(told[0].Message, want) && !strings.Contains(strings.ReplaceAll(told[0].Message, "one of the update's 3 rounds", "3 of the update's"), want) {
			t.Fatalf("report = %q, want it to say %q", told[0].Message, want)
		}
	}
}

func TestAFourthRoundOfRunsForOneUpdateIsRefusedInPlainWords(t *testing.T) {
	t.Parallel()

	f, _, _ := conflictFixture(t)
	f.developer = writeIn("", "app.txt", "one\nstaging and team two\nthree\n")
	f.verdict = teamRepair // the reviewer turns every merge down, so no round lands the update
	started := 0
	f.pipeline.NewRunID = func() (string, error) {
		started++
		return fmt.Sprintf("run-%08x%024x", started*0x1010101, 0), nil
	}
	for round := 1; round <= runstate.MaxTeamBranchConflictRounds; round++ {
		if round > 1 {
			// The team's branch moves, and the merge conflicts again.
			f.teamMerges("app.txt", "one\nteam two, round "+string(rune('0'+round))+"\nthree\n", "the team changes the line again")
			if outcome := f.update(); len(outcome.Conflict) != 1 {
				t.Fatalf("round %d: Update() = %#v, want a conflict again", round, outcome)
			}
		}
		decided, err := f.decide()
		if err != nil || decided.Decision == nil || decided.Decision.Round != round {
			t.Fatalf("round %d: decide = %#v, %v, want round %d recorded", round, decided.Decision, err, round)
		}
		if outcome := f.update(); outcome.Landed != "" || !strings.Contains(outcome.Waiting, "did not approve") {
			t.Fatalf("round %d: Update() = %#v, want the merge the run made turned down by the reviewer", round, outcome)
		}
	}
	f.teamMerges("app.txt", "one\nteam two, once more\nthree\n", "the team changes the line a fourth time")
	if outcome := f.update(); len(outcome.Conflict) != 1 {
		t.Fatalf("Update() = %#v, want a conflict again", outcome)
	}
	_, err := f.decide()
	if err == nil || !strings.Contains(err.Error(), "a fourth round is refused") || !strings.Contains(err.Error(), "spent") {
		t.Fatalf("fourth decision error = %v, want a refusal saying a fourth round is refused because the three are spent", err)
	}
	if record := f.record(); record.ConflictRounds != runstate.MaxTeamBranchConflictRounds || record.Decision != nil {
		t.Fatalf("record rounds %d, decision %#v, want three rounds spent and nothing standing", record.ConflictRounds, record.Decision)
	}
	// None of the three was charged to the item.
	runs, err := f.waiting.StateStore.(*runstate.Store).Runs("yoyodyne-task")
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.RepairAttempts != 0 {
			t.Fatalf("run %s spent %d repair attempt(s), want none", run.RunID, run.RepairAttempts)
		}
	}
	// The last conflict's report says the rounds are spent.
	told := f.toldTheDevelopmentManager()
	if last := told[len(told)-1]; !strings.Contains(last.Message, "spent") {
		t.Fatalf("last report = %q, want it to say the rounds are spent", last.Message)
	}
}

func TestAConflictCanBeDecidedOnce(t *testing.T) {
	t.Parallel()

	f, _, _ := conflictFixture(t)
	if _, err := f.decide(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.decide(); err == nil || !strings.Contains(err.Error(), "already has a decision") {
		t.Fatalf("second decision error = %v, want it refused because the conflict is already decided", err)
	}
	if record := f.record(); record.ConflictRounds != 1 {
		t.Fatalf("rounds = %d, want the refused decision not counted", record.ConflictRounds)
	}
}

func TestAMergeCarryingARunsResolutionOfAProtectedFileIsRefusedAndOneThatKeepsTheTeamsIsNot(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T, resolved string) (*teamUpdateFixture, TeamBranchUpdateOutcome) {
		f := newTeamUpdateFixture(t)
		landing := f.landsOnTarget("docs/product/plan.md", "the target's plan\n", "staging adds the plan")
		f.waiting.runs = []runstate.State{{
			RunID: "run-staging-item", WorkItemID: "yoyodyne-task", WorkItemTitle: "Reword the greeting", Status: runstate.StatusSucceeded,
			Integration: &runstate.Integration{TargetBranch: "staging", SourceCommit: landing, TargetCommit: landing},
			TeamMerge:   &runstate.TeamMerge{TargetBranch: "staging", UpstreamBranch: "main", LandingCommit: landing, Since: time.Now()},
		}}
		f.teamMerges("docs/product/plan.md", "the team's plan\n", "the team adds the plan")
		if outcome := f.update(); len(outcome.Conflict) != 1 {
			t.Fatalf("Update() = %#v, want the add/add conflict", outcome)
		}
		f.developer = writeIn("docs/product", "plan.md", resolved)
		if _, err := f.decide(); err != nil {
			t.Fatal(err)
		}
		return f, f.update()
	}

	t.Run("a path the run wrote is refused", func(t *testing.T) {
		t.Parallel()
		f, outcome := setup(t, "both plans\n")
		if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "docs/product/plan.md") || !strings.Contains(outcome.Waiting, "changes paths no run may write") {
			t.Fatalf("Update() = %#v, want the merge refused for the protected path the run wrote", outcome)
		}
		var named []report.Report
		for _, told := range f.toldTheDevelopmentManager() {
			if strings.Contains(told.Message, "docs/product/plan.md") && strings.Contains(told.Message, "changes paths no run may write") {
				named = append(named, told)
			}
		}
		if len(named) != 1 {
			t.Fatalf("%d reports name the refused path, want one", len(named))
		}
		for range 2 {
			f.update()
		}
		if len(f.toldTheDevelopmentManager()) != 2 {
			t.Fatalf("%d reports after three checks, want the conflict's and the refusal's, once each", len(f.toldTheDevelopmentManager()))
		}
	})

	t.Run("a path that holds the team's own content is the team's, not the run's", func(t *testing.T) {
		t.Parallel()
		f, outcome := setup(t, "the team's plan\n")
		if outcome.Landed == "" || f.local("staging") != outcome.Landed {
			t.Fatalf("Update() = %#v, want the merge landed: the run wrote no protected line of its own", outcome)
		}
	})
}

func TestADecisionForAConflictThatWentAwayIsNotChargedARound(t *testing.T) {
	t.Parallel()

	f, _, _ := conflictFixture(t)
	if _, err := f.decide(); err != nil {
		t.Fatal(err)
	}
	// The team settles on the target's wording, so the merge goes in cleanly.
	f.teamMerges("app.txt", "one\nstaging two\nthree\n", "the team settles on the target's wording")
	outcome := f.update()
	if outcome.Landed == "" {
		t.Fatalf("Update() = %#v, want the clean merge landed", outcome)
	}
	if record := f.record(); record.ConflictRounds != 0 || record.Decision != nil {
		t.Fatalf("record rounds %d, decision %#v, want no round spent on a conflict that went away", record.ConflictRounds, record.Decision)
	}
}

func TestTheRunRecordsWhoChoseItAndStartsNothingWhileIntakeIsHeld(t *testing.T) {
	t.Parallel()

	f, _, _ := conflictFixture(t)
	f.developer = writeIn("", "app.txt", "one\nstaging and team two\nthree\n")
	intake := newIntakeHoldStore(t)
	f.pipeline.Intake = intake
	if _, err := intake.Hold(runstate.IntakeHolderOperator, "the queue is heading somewhere wrong", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.decide(); err != nil {
		t.Fatal(err)
	}
	outcome := f.update()
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "holding the work the harness chooses") {
		t.Fatalf("Update() = %#v, want nothing started while intake is held", outcome)
	}
	if record := f.record(); record.Decision == nil || record.Decision.Started() || record.ConflictRounds != 1 {
		t.Fatalf("record = %#v, want the decision standing with its run not started", record.Decision)
	}
	if runs, err := f.waiting.StateStore.(*runstate.Store).Runs("yoyodyne-task"); err != nil || len(runs) != 0 {
		t.Fatalf("runs = %d, %v, want none reserved", len(runs), err)
	}

	if _, _, err := intake.Release(); err != nil {
		t.Fatal(err)
	}
	outcome = f.update()
	if outcome.Landed == "" {
		t.Fatalf("Update() after the release = %#v, want the merge landed", outcome)
	}
	run, err := f.waiting.StateStore.(*runstate.Store).Load(f.record().Landed[0].ResolvedBy)
	if err != nil {
		t.Fatal(err)
	}
	if run.Selection == nil || run.Selection.By != runstate.SelectedByDevelopmentManager ||
		!strings.Contains(run.Selection.Reason, "chat-test, turn 1") || !strings.Contains(run.Selection.Reason, "round 1 of 3") || !strings.Contains(run.Selection.Reason, "knows both wordings") {
		t.Fatalf("selection = %#v, want the development manager, her conversation and turn, the round and her reason", run.Selection)
	}
}

// The run asks about the item the development manager named, and no other. A
// refusal leaves the conflict and the decision standing and the round unspent,
// and the same decision starts once the item is added.
func TestARunOutsideTheNamedEpicsIsNotStartedOnAConflict(t *testing.T) {
	t.Parallel()

	f, _, _ := conflictFixture(t)
	f.developer = writeIn("", "app.txt", "one\nstaging and team two\nthree\n")
	decide, add, refused := epicModeKept(t, blockedMember)
	var asked []maystart.Start
	unreadable := false
	f.pipeline.MayStart = func(start maystart.Start) maystart.Decision {
		asked = append(asked, start)
		if unreadable {
			return maystart.Decision{Answer: maystart.CouldNotTell, Why: "the record could not be read"}
		}
		return decide(start)
	}
	if _, err := f.decide(); err != nil {
		t.Fatal(err)
	}
	told, landed := len(f.toldTheDevelopmentManager()), f.local("staging")
	for _, want := range []string{"`yoyo epic add " + namedEpic + " yoyodyne-task`", "the record could not be read"} {
		if outcome := f.update(); outcome.Landed != "" || !strings.Contains(outcome.Waiting, want) || !strings.Contains(outcome.Waiting, "tried again at the next check") {
			t.Fatalf("Update() = %#v, want it waiting and saying %q", outcome, want)
		}
		unreadable = true
		record := f.record()
		if record.Decision == nil || record.Decision.Started() || record.Decision.Item.ID != "yoyodyne-task" || record.ConflictRounds != 1 || record.Pending == nil || record.Pending.Conflict == nil {
			t.Fatalf("record = %#v, want the conflict and the decision as they were with no run started", record)
		}
	}
	if runs, err := f.waiting.StateStore.(*runstate.Store).Runs("yoyodyne-task"); err != nil || len(runs) != 0 {
		t.Fatalf("runs = %d, %v, want none reserved", len(runs), err)
	}
	if f.local("staging") != landed || len(f.toldTheDevelopmentManager()) != told {
		t.Fatal("the refusal moved the target or told the development manager")
	}

	refused(maystart.ResolveConflict, "yoyodyne-task")
	unreadable = false
	add("yoyodyne-task")
	if outcome := f.update(); outcome.Landed == "" {
		t.Fatalf("Update() of a member = %#v, want the merge landed", outcome)
	}
	if record := f.record(); record.Landed[0].ResolvedBy == "" {
		t.Fatalf("record = %#v, want a run's resolution landed", record)
	}
	for _, start := range asked {
		if !reflect.DeepEqual(start, maystart.Run(maystart.ResolveConflict, "yoyodyne-task")) {
			t.Fatalf("asked about %+v, want the item the development manager named", start)
		}
	}
	if len(asked) != 4 {
		t.Fatalf("asked %d times, want once for each start and once before the developer", len(asked))
	}
}

func TestTheReviewerIsToldARunResolvedTheMergeAndWhere(t *testing.T) {
	t.Parallel()

	r := &teamBranchRun{upstream: "main", target: "staging"}
	attempt := runstate.TeamBranchUpdateAttempt{
		TeamCommit: strings.Repeat("a", 40), TargetBase: strings.Repeat("b", 40), Candidate: strings.Repeat("c", 40),
		ResolvedBy: "run-1", ResolvedItem: "yoyodyne-task", ResolvedItemTitle: "Reword the greeting", ResolvedPaths: []string{"app.txt"},
	}
	for name, text := range map[string]string{"review context": r.reviewContext(attempt), "pull request": r.pullRequestBody(attempt)} {
		if !strings.Contains(text, "run-1") || !strings.Contains(text, "Reword the greeting (yoyodyne-task)") || !strings.Contains(text, "app.txt") ||
			strings.Contains(text, "wrote no line of its own") {
			t.Fatalf("%s = %q, want it to name the run, the item and the paths and not say the harness wrote nothing", name, text)
		}
	}
	if clean := r.reviewContext(runstate.TeamBranchUpdateAttempt{TeamCommit: attempt.TeamCommit, TargetBase: attempt.TargetBase, Candidate: attempt.Candidate}); !strings.Contains(clean, "wrote no line of its own") {
		t.Fatalf("a merge no run touched lost its statement: %q", clean)
	}
}

func TestADecisionWhoseItemHasClosedIsDroppedWithItsRoundGivenBackAndTold(t *testing.T) {
	t.Parallel()

	f, _, _ := conflictFixture(t)
	if _, err := f.decide(); err != nil {
		t.Fatal(err)
	}
	// The item closes before the run starts, as it does once the team's merge
	// carries its landing.
	f.pipeline.Tracker.(*orchestratortest.Tracker).Item.Status = "closed"
	outcome := f.update()
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "could not be started") {
		t.Fatalf("Update() = %#v, want the run not started", outcome)
	}
	record := f.record()
	if record.Decision != nil || record.ConflictRounds != 0 {
		t.Fatalf("decision %#v, rounds %d; want the decision dropped and its round given back", record.Decision, record.ConflictRounds)
	}
	told := f.toldTheDevelopmentManager()
	if len(told) != 2 || !strings.Contains(told[1].Message, "Reword the greeting (yoyodyne-task)") || !strings.Contains(told[1].Message, "could not be started") || !strings.Contains(told[1].Message, "you can decide again") {
		t.Fatalf("reports = %#v, want a second one naming the item by title with its identifier and saying she can decide again", told)
	}
	// She can decide again, and the next check does not tell her twice.
	if _, err := f.decide(); err != nil {
		t.Fatalf("a new decision after the drop: %v", err)
	}
}
