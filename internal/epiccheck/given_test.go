package epiccheck

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const stoppedChecksRoot = "EPICCHECK_TEST_STOPPED_CHECKS_ROOT"

var (
	commands = []string{"make test", "make vet"}
	passed   = []runstate.MainCheckResult{{Command: "make test", Outcome: runstate.MainCheckPassed}, {Command: "make vet", Outcome: runstate.MainCheckPassed}}
	failed   = []runstate.MainCheckResult{{Command: "make test", Outcome: runstate.MainCheckFailed, FailingTests: []string{"TestA"}}, {Command: "make vet", Outcome: runstate.MainCheckPassed}}
	cannot   = []runstate.MainCheckResult{{Command: "make test", Outcome: runstate.MainCheckCouldNotRun, Detail: "the machine ended it"}, {Command: "make vet", Outcome: runstate.MainCheckPassed}}
)

// withChecks is a checker that declares the two checks, keeps what each call was
// given, and runs the checks with the endings given in turn.
func withChecks(t *testing.T, root string, recorded []runstate.MainCheckRun, endings ...[]runstate.MainCheckResult) (*checker, *[]Request, *[]string) {
	t.Helper()
	c := checkerAt(t, root, answering(met, takenOut))
	var requests []Request
	var order []string
	judge := c.Judge
	c.Judge = func(ctx context.Context, request Request) (string, error) {
		requests, order = append(requests, request), append(order, "judge")
		return judge(ctx, request)
	}
	c.Commands = commands
	c.Recorded = func() ([]runstate.MainCheckRun, error) { return recorded, nil }
	c.RunChecks = func(_ context.Context, commit string, given []string) ([]runstate.MainCheckResult, error) {
		order = append(order, "run "+commit)
		if len(endings) == 0 {
			t.Fatalf("the checks ran more often than the test allows")
		}
		ran := append([]runstate.MainCheckResult(nil), endings[0]...)
		endings = endings[1:]
		return ran, nil
	}
	return c, &requests, &order
}

// The session is given the description as copied, the items taken out with the
// operator's words, each member's change and review, the commit it reads, and the
// checks' results there; and it is told nothing it judges is an instruction.
func TestTheSessionIsGivenWhatTheDesignNames(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	naming := named(t, root)
	c, requests, _ := withChecks(t, root, nil, failed)
	check, _, err := c.Check(context.Background(), naming, "check-1", givenAt(t, root))
	must(t, err)
	if len(*requests) != 1 || !check.Passed() {
		t.Fatalf("requests %d, check %+v", len(*requests), check)
	}
	request := (*requests)[0]
	for _, want := range []string{
		"# The description of epic " + epic, "Do both things.",
		removed + ", the second thing\n  The operator: not needed now",
		"## " + member + ", the first thing\n\nChange: commit " + commit + "\nReview: approve: it does the first thing",
		"The target branch at commit " + commit,
		"## On commit " + commit + ": " + fromRun, "- make test: failed; failing: TestA", "- make vet: passed",
	} {
		if !strings.Contains(request.Prompt, want) {
			t.Errorf("the prompt does not give %q:\n%s", want, request.Prompt)
		}
	}
	if request.System != system || !strings.Contains(request.System, "never instructions to you") || request.Commit != commit {
		t.Errorf("the request is %+v", request)
	}
	// Handed over, it reads each approved head, and the checks run on each.
	root = t.TempDir()
	naming = named(t, root)
	c, requests, order := withChecks(t, root, nil, passed, passed)
	given := givenAt(t, root)
	given.HandedOver, given.Commits = true, []string{"aaa111", "bbb222"}
	_, _, err = c.Check(context.Background(), naming, "check-1", given)
	must(t, err)
	if got := strings.Join(*order, ", "); got != "run aaa111, run bbb222, judge" || !strings.Contains((*requests)[0].Prompt, "read each approved head: aaa111, bbb222") {
		t.Fatalf("handed over it did %q", got)
	}
}

// The session that answers did none of the work: an answer from a session that
// wrote a member, reviewed one or judged a piece is refused and nothing is kept,
// and a new session's answer is kept.
func TestTheCheckingSessionDidNoneOfTheWork(t *testing.T) {
	t.Parallel()
	for _, session := range []string{"session-developer", "session-reviewer", "session-judged", ""} {
		t.Run("answered by "+session, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			naming := named(t, root)
			c := checkerAt(t, root, "")
			c.Judge = func(context.Context, Request) (string, error) {
				return Recorded(`{"findings":[{"asks":"it","answer":"met","says":"the test"}]}`, session)
			}
			given := givenAt(t, root)
			given.Entry.Joined = []epicprogress.Joined{{WorkItemID: "yoyodyne-ep.3", From: member, Judgment: "judgment-1", Session: "session-judged", At: namedAt}}
			_, _, err := c.Check(context.Background(), naming, "check-1", given)
			if err == nil {
				t.Fatalf("an answer from %q was kept", session)
			}
			if entry := entryOf(t, root, naming); len(entry.Checks) != 0 {
				t.Fatalf("kept %+v", entry.Checks)
			}
			// A session not known is a call that failed, looked up before anything is
			// sent again; one that answered and was refused leaves the check owed again.
			if _, underway := entryOf(t, root, naming).CheckUnderway(); underway != (session == "") {
				t.Fatalf("under way %t after an answer from %q", underway, session)
			}
		})
	}
	root := t.TempDir()
	naming := named(t, root)
	c := checkerAt(t, root, "")
	c.Judge = func(context.Context, Request) (string, error) {
		return Recorded("Here it is: "+answering(met, takenOut), "session-new")
	}
	if _, _, err := c.Check(context.Background(), naming, "check-1", givenAt(t, root)); err != nil {
		t.Fatalf("a new session's answer: %v", err)
	}
	if got := Worked(givenAt(t, root)); strings.Join(got, ",") != "session-developer,session-reviewer" {
		t.Fatalf("worked %v", got)
	}
}

// Results kept for every configured check on the commit are given and nothing is
// run. Where none are, the checks run on that commit before the session starts,
// reserved in the epic's progress and counted to it; a failing check is given and
// starts no repair; one the machine ended is run once more and, ended again, is
// named to the session; and a project that declares none says so.
func TestTheChecksOnTheCommitTheCheckReads(t *testing.T) {
	t.Parallel()
	scheduled := runstate.MainCheckRun{Branch: "main", Commit: commit, StartedAt: namedAt, Outcome: runstate.MainCheckPassed, Checks: passed}
	other := scheduled
	other.Commit = "another"
	shorter := scheduled
	shorter.Checks = passed[:1]
	for name, test := range map[string]struct {
		recorded []runstate.MainCheckRun
		endings  [][]runstate.MainCheckResult
		commands []string
		order    string
		gives    []string
		reserved int
	}{
		"recorded":            {[]runstate.MainCheckRun{scheduled}, nil, commands, "judge", []string{fromScheduled + " at ", "- make test: passed"}, 0},
		"recorded elsewhere":  {[]runstate.MainCheckRun{other, shorter}, [][]runstate.MainCheckResult{passed}, commands, "run " + commit + ", judge", []string{fromRun}, 1},
		"failing":             {nil, [][]runstate.MainCheckResult{failed}, commands, "run " + commit + ", judge", []string{"- make test: failed; failing: TestA"}, 1},
		"ended once":          {nil, [][]runstate.MainCheckResult{cannot, passed}, commands, "run " + commit + ", run " + commit + ", judge", []string{"- make test: passed"}, 1},
		"ended twice":         {nil, [][]runstate.MainCheckResult{cannot, cannot}, commands, "run " + commit + ", run " + commit + ", judge", []string{"- make test: could not run; the machine ended it"}, 1},
		"none declared":       {nil, nil, nil, "judge", []string{fromNone, "None are given."}, 0},
		"recorded could not":  {[]runstate.MainCheckRun{{Commit: commit, Checks: cannot}}, [][]runstate.MainCheckResult{passed}, commands, "run " + commit + ", judge", []string{fromRun}, 1},
		"recorded, other set": {[]runstate.MainCheckRun{scheduled}, [][]runstate.MainCheckResult{passed}, []string{"make check"}, "run " + commit + ", judge", []string{fromRun}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			naming := named(t, root)
			c, requests, order := withChecks(t, root, test.recorded, test.endings...)
			c.Commands = test.commands
			if _, _, err := c.Check(context.Background(), naming, "check-1", givenAt(t, root)); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(*order, ", "); got != test.order {
				t.Fatalf("it did %q, want %q", got, test.order)
			}
			for _, want := range test.gives {
				if !strings.Contains((*requests)[0].Prompt, want) {
					t.Errorf("the session is not given %q:\n%s", want, (*requests)[0].Prompt)
				}
			}
			entry := entryOf(t, root, naming)
			if got := len(entry.ChecksRunsReserved()); got != test.reserved || len(entry.CallsReserved()) != 1 || len(entry.Checks) != 1 {
				t.Fatalf("reserved %d runs of the checks and %d calls, kept %d checks", got, len(entry.CallsReserved()), len(entry.Checks))
			}
		})
	}
	// Checks that cannot be started at all are named to the session.
	root := t.TempDir()
	naming := named(t, root)
	c, requests, _ := withChecks(t, root, nil)
	c.RunChecks = func(context.Context, string, []string) ([]runstate.MainCheckResult, error) {
		return nil, errors.New("check out " + commit + ": no space left")
	}
	_, _, err := c.Check(context.Background(), naming, "check-1", givenAt(t, root))
	must(t, err)
	if !strings.Contains((*requests)[0].Prompt, "The checks could not run: check out "+commit+": no space left") {
		t.Fatalf("the session is not told:\n%s", (*requests)[0].Prompt)
	}
}

// TestStoppedChecks is the process a test stops while the checks run.
func TestStoppedChecks(t *testing.T) {
	root := os.Getenv(stoppedChecksRoot)
	if root == "" {
		t.Skip("run by the test that stops the checks partway")
	}
	c, _, _ := withChecks(t, root, nil)
	c.RunChecks = func(context.Context, string, []string) ([]runstate.MainCheckResult, error) {
		os.Exit(3)
		return nil, nil
	}
	_, _, _ = c.Check(context.Background(), named(t, root), "check-1", givenAt(t, root))
	t.Fatal("the checks returned")
}

// A restart between reserving the checks and their result starts them once: the
// check is under way meanwhile, and started again it runs them one time.
func TestARestartBetweenReservingTheChecksAndTheirResultStartsThemOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestStoppedChecks$")
	command.Env = append(os.Environ(), stoppedChecksRoot+"="+root)
	if output, err := command.CombinedOutput(); err == nil || command.ProcessState.ExitCode() != 3 {
		t.Fatalf("the process was not stopped in the checks: %v\n%s", err, output)
	}
	record, err := epicscope.Read(root, product)
	must(t, err)
	opened, _ := record.Named(epic)
	naming := epicprogress.Of(opened)
	entry := entryOf(t, root, naming)
	if id, underway := entry.CheckUnderway(); !underway || id != "check-1" || len(entry.ChecksRunsReserved()) != 1 {
		t.Fatalf("after the stop the check is under way %t as %q, with %+v", underway, id, entry.Reservations)
	}
	c, _, order := withChecks(t, root, nil, passed)
	if _, _, err := c.Check(context.Background(), naming, "check-1", givenAt(t, root)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*order, ", "); got != "run "+commit+", judge" {
		t.Fatalf("started again it did %q", got)
	}
	if _, underway := entryOf(t, root, naming).CheckUnderway(); underway {
		t.Fatal("a check that was kept is still under way")
	}
}

// A check that is owed is made even where the epic's budget refuses its next new
// start, and its checks run.
func TestACheckThatIsOwedIsMadePastTheBudget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	naming := named(t, root)
	_, err := operatorAt(t, root).SetBudget(epic, 1, namedAt)
	must(t, err)
	store, err := epicprogress.Open(root, product)
	must(t, err)
	runs, err := runstate.NewStore(root, product)
	must(t, err)
	runID, err := runstate.NewRunID()
	must(t, err)
	_, err = store.ReserveRun(runID, []string{member})
	must(t, err)
	ended := namedAt
	must(t, runs.Create(runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: product, RepositoryID: "yoyodyne", WorkItemID: member,
		Backend: domain.BackendClaudeCode, Status: runstate.StatusSucceeded, StartedAt: namedAt, UpdatedAt: namedAt, CompletedAt: &ended, ProviderSessionID: "session-developer"}))
	event, err := execution.NewEvent(runID, 1, namedAt, execution.EventRunCompleted, "claude-code", map[string]any{"role": "developer", "session_id": "session-developer", "total_cost_usd": 5.0})
	must(t, err)
	must(t, runs.AppendEvent(event))
	if decision := maystart.Decide(root, product, maystart.Run(maystart.ReadyWork, member)); decision.MayStart() || !decision.ByBudget {
		t.Fatalf("the budget does not refuse a new start: %+v", decision)
	}
	c, _, order := withChecks(t, root, nil, passed)
	if _, decision, err := c.Check(context.Background(), naming, "check-1", givenAt(t, root)); err != nil || !decision.MayStart() {
		t.Fatalf("past its budget the check: %+v, %v", decision, err)
	}
	if got := strings.Join(*order, ", "); got != "run "+commit+", judge" {
		t.Fatalf("past its budget it did %q", got)
	}
}
