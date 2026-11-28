package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A harness environment whose check passed has its test run after the
// developer submits and before review, at the submitted commit, in a folder of
// its own named in YOYODYNE_TEST_SCRATCH that is empty when it starts and gone
// afterwards. The developer is told the environment exists before its turn and
// is given no result until it has submitted; a failing test comes back to it
// as a failed check with the test's output, spending one repair round; and the
// reviewer is given the passing result with its commit and where it ran.
func TestAnEnvironmentsTestRunsAtTheSubmittedCommitBeforeReview(t *testing.T) {
	t.Parallel()

	seen := filepath.Join(t.TempDir(), "folders-seen")
	tracker := environmentItem("environment-tests-1", "Environments: web ui (criteria 1, 2)")
	developerTurns := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		developerTurns++
		if developerTurns == 2 {
			// The repair: what the test looks for.
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "src", "page-ready.txt"), []byte("ready\n"), 0o600); err != nil {
				return err
			}
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "src", "farewell.ts"), []byte(nonGoSource("  ")), 0o600)
	}, approveVerdict)
	test := `test -z "$(ls -A "$YOYODYNE_TEST_SCRATCH")" || { echo 'the folder was not empty'; exit 9; }; ` +
		`echo "$YOYODYNE_TEST_SCRATCH" >> ` + seen + `; touch "$YOYODYNE_TEST_SCRATCH/profile"; ` +
		`test -f src/page-ready.txt || { echo 'the page never said it was ready'; exit 4; }; echo 'the page loaded'`
	pipeline, store, recorded := environmentPipeline(t, tracker, provider, []config.Environment{
		{Name: "web ui", Where: config.EnvironmentInHarness, Check: `test -d "$YOYODYNE_TEST_SCRATCH"`, Test: test, TimeLimit: config.Duration(time.Minute)},
	})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	developers := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developers) != 2 {
		t.Fatalf("developer invocations = %d, want the first attempt and one repair", len(developers))
	}
	first, repair := developers[0].Prompt, developers[1].Prompt
	if !strings.Contains(first, "# Environments this work item names") || !strings.Contains(first, `"web ui", for criteria 1, 2`) || !strings.Contains(first, "You cannot run these tests yourself") {
		t.Fatalf("first prompt does not name the harness environment and its criteria:\n%s", first)
	}
	if strings.Contains(first, "the page never said it was ready") {
		t.Fatal("the developer was given the test's result before it submitted")
	}
	for _, want := range []string{"The test of an environment the work item names failed on your change", "the page never said it was ready", "you cannot run it yourself", "repair attempt 1 of"} {
		if !strings.Contains(repair, want) {
			t.Errorf("repair prompt does not say %q:\n%s", want, repair)
		}
	}

	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.RepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want the failing test to spend one", state.RepairAttempts)
	}
	if state.HarnessCommit == "" {
		t.Fatal("the run recorded no harness commit")
	}
	at := state.EnvironmentTestsAt(state.HarnessCommit)
	if len(state.EnvironmentTests) != 1 || len(at) != 1 || !at[0].Passed() || at[0].Where != "harness" || at[0].Commit != state.HarnessCommit {
		t.Fatalf("environment tests = %#v, want one passing result at %s", state.EnvironmentTests, state.HarnessCommit)
	}

	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) == 0 {
		t.Fatal("the change was never reviewed")
	}
	reviewed := reviews[len(reviews)-1].Prompt
	for _, want := range []string{"## Environments the work item names", `the test of the environment "web ui", for criteria 1, 2, passed at commit ` + state.HarnessCommit, "where the declared checks run", "the page loaded"} {
		if !strings.Contains(reviewed, want) {
			t.Errorf("review prompt does not say %q", want)
		}
	}

	// Every run of the check and the test was given its own folder, under the
	// run's scratch space, and each is gone afterwards.
	var given []string
	for _, request := range recorded.requests {
		for _, variable := range request.Env {
			if folder, ok := strings.CutPrefix(variable, checks.TestScratchVariable+"="); ok {
				given = append(given, folder)
			}
		}
	}
	if len(given) != 3 {
		t.Fatalf("folders given = %#v, want one for the check and one for each of the two tests", given)
	}
	under := filepath.Join("scratch", outcome.RunID, "environment-runs") + string(filepath.Separator)
	for _, folder := range given {
		if !strings.Contains(folder, under) {
			t.Errorf("folder %q is not under the run's scratch space %q", folder, under)
		}
		if _, err := os.Stat(folder); !os.IsNotExist(err) {
			t.Errorf("folder %q is still there after its command ended (%v)", folder, err)
		}
	}
	written, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Fields(string(written)); len(lines) != 2 {
		t.Fatalf("the test saw folders %q, want it run twice, each time in an empty folder", lines)
	}
}

// A result recorded on one commit is never shown as evidence for another: the
// reviewer's account of a run whose tests ran on an earlier commit says no
// result is recorded on the commit under review, and names the environment as
// still to be checked.
func TestAnEnvironmentsTestResultIsNeverShownForAnotherCommit(t *testing.T) {
	t.Parallel()

	earlier := strings.Repeat("a", 40)
	current := strings.Repeat("b", 40)
	passed := runstate.EnvironmentCheck{Name: "web ui", Result: runstate.EnvironmentPassed, Where: "harness", Criteria: []int{1}}
	tested := passed
	tested.Commit = earlier
	tested.Output = "the page loaded on the earlier commit"
	state := runstate.State{HarnessCommit: current, Environments: []runstate.EnvironmentCheck{passed}, EnvironmentTests: []runstate.EnvironmentCheck{tested}}

	if got := state.EnvironmentTestsAt(current); len(got) != 0 {
		t.Fatalf("EnvironmentTestsAt(current) = %#v, want nothing from the earlier commit", got)
	}
	if got := state.EnvironmentTestsAt(""); len(got) != 0 {
		t.Fatalf("EnvironmentTestsAt(\"\") = %#v, want nothing for a change with no commit", got)
	}
	said := describeEnvironmentEvidence(state)
	if strings.Contains(said, earlier) || strings.Contains(said, "the page loaded on the earlier commit") || strings.Contains(said, "passed at commit") {
		t.Fatalf("the reviewer was shown the earlier commit's result:\n%s", said)
	}
	if !strings.Contains(said, "Still to be checked") || !strings.Contains(said, "no result of its test is recorded on the commit under review") {
		t.Fatalf("the reviewer was not told the environment is still to be checked:\n%s", said)
	}

	state.HarnessCommit = earlier
	if said := describeEnvironmentEvidence(state); !strings.Contains(said, "passed at commit "+earlier) {
		t.Fatalf("on its own commit the result is not shown:\n%s", said)
	}
}

// An environment whose check did not pass has no test run, and the reviewer is
// told it is still to be checked.
func TestAnEnvironmentWhoseCheckFailedRunsNoTestAndIsStillToBeChecked(t *testing.T) {
	t.Parallel()

	run := &activeRun{state: runstate.State{HarnessCommit: strings.Repeat("c", 40), Environments: []runstate.EnvironmentCheck{
		{Name: "ios client", Result: runstate.EnvironmentFailed, Where: "harness", Criteria: []int{3}},
	}}}
	run.pipeline.Config.Environments = []config.Environment{{Name: "ios client", Where: config.EnvironmentInHarness, Check: "exit 1", Test: "exit 1"}}
	if err := run.testEnvironmentsAtSubmittedHead(context.Background(), nil); err != nil {
		t.Fatalf("error = %v, want no test run for an environment whose check failed", err)
	}
	if len(run.state.EnvironmentTests) != 0 {
		t.Fatalf("tests = %#v, want none", run.state.EnvironmentTests)
	}
	if said := describeEnvironmentEvidence(run.state); !strings.Contains(said, `"ios client", which criterion 3 blocks`) || !strings.Contains(said, "its test did not run") {
		t.Fatalf("reviewer evidence = %q", said)
	}
	if section := environmentsBeforeTurnSection(run.state); !strings.Contains(section, "its test will not run on this run") {
		t.Fatalf("developer section = %q", section)
	}
}
