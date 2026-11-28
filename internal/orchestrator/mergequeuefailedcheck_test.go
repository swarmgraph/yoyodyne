package orchestrator

// A merge queue hand-back for a failed check carries the failure itself: each
// failing test's name and directory, whether the change touches it, and the end
// of the check's output with secrets removed, on the queue's record, the run's
// failed check, and the item's note.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// queueGoTestReading reads Go's test output, as this repository declares it.
var queueGoTestReading = checks.TestReading{
	Checks:      []string{"sh scripts/go-test.sh"},
	Test:        `^\s*--- FAIL: (?P<test>[^\s/]+)`,
	File:        `^\s+(?P<file>[^\s/:]+_test\.go):[0-9]+: `,
	Group:       `^FAIL\s+(?P<directory>\S+)`,
	StripPrefix: "example.com/project",
}

// fakeSecret is a value shaped like a provider key, which the hand-back must
// never carry.
const fakeSecret = "sk-abcdefghijklmnopqrstuvwxyz0123"

func TestAMergeQueueHandBackRecordsTheFailingTestsAndTheEndOfTheOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		command string
		script  string
		// says are what the hand-back's reason, the run's failed check and the
		// item's note must each carry.
		says  []string
		tests int
	}{
		{
			name:    "one failing test in a directory the change does not touch",
			command: "sh scripts/go-test.sh",
			script: "--- FAIL: TestBeads (0.01s)\n" +
				"    beads_test.go:12: the client said " + fakeSecret + "\n" +
				"FAIL\n" +
				"FAIL\texample.com/project/internal/beads\t0.2s\n",
			says: []string{
				"Failing test: TestBeads in internal/beads (internal/beads/beads_test.go), a directory the change does not touch",
				"beads_test.go:12: the client said",
			},
			tests: 1,
		},
		{
			name:    "several failing tests, one the change touches",
			command: "sh scripts/go-test.sh",
			script: "--- FAIL: TestRoot (0.01s)\n" +
				"    root_test.go:3: feature.txt says implemented\n" +
				"FAIL\n" +
				"FAIL\texample.com/project\t0.1s\n" +
				"--- FAIL: TestBeads (0.01s)\n" +
				"    beads_test.go:12: timed out waiting for the store\n" +
				"FAIL\n" +
				"FAIL\texample.com/project/internal/beads\t0.2s\n",
			says: []string{
				"Failing tests: TestRoot in . (root_test.go), a directory the change touches: feature.txt changed in its directory",
				"TestBeads in internal/beads (internal/beads/beads_test.go), a directory the change does not touch",
				"timed out waiting for the store",
			},
			tests: 2,
		},
		{
			name:    "a check no test reading applies to keeps its output",
			command: "sh scripts/vet.sh",
			script:  "feature.go:3:2: unreachable code, token " + fakeSecret + "\n",
			says:    []string{"feature.go:3:2: unreachable code"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newPromotionFixture(t, landLocally)
			// The check fails only with the change on it, so the failure is the
			// change's: the queue runs it on the base as well.
			script := "if grep -q implemented feature.txt; then printf '%s' '" + tc.script + "'; exit 1; fi\n"
			writeQueueFile(t, f.repository, strings.TrimPrefix(tc.command, "sh "), script)
			runPipelineGit(t, f.repository, "add", ".")
			runPipelineGit(t, f.repository, "commit", "-m", "the check")
			runner, ok := f.checks.runner.(checks.Runner)
			if !ok {
				t.Fatalf("the fixture's checks run on %T, not the checks runner", f.checks.runner)
			}
			runner.FailingTests = []checks.TestReading{queueGoTestReading}
			f.checks.runner = runner
			f.worker.Pipeline.Config.Checks = []string{tc.command}
			f.worker.Pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2
			f.run.admittedRun.state.Branch = "change"
			f.run.admittedRun.state.Status = runstate.StatusSucceeded
			f.run.admittedRun.state.ProductID = "yoyodyne"
			f.run.admittedRun.state.TargetBranch = "main"
			f.run.admittedRun.state.WorktreePath = f.repository
			f.run.admittedRun.state.BaseCommit = f.local("main")
			f.promoter.Repair = MergeQueueRunHandback{Store: f.run, Tracker: f.tracker}

			if verification, err := f.work(); err != nil || verification.Verified {
				t.Fatalf("Work() = %#v, %v; want the candidate refused", verification, err)
			}
			result := f.generations()[0].CheckRun.Results[0]
			if result.Failure == nil || result.Failure.Command != tc.command || strings.Contains(result.Failure.Output, fakeSecret) {
				t.Fatalf("the candidate's record of the failed check = %#v, want its failure kept with secrets removed", result.Failure)
			}
			if tc.tests == 0 && (result.Failure.Tests != nil || result.Failure.Touched != nil) {
				t.Fatalf("a check no reading applies to recorded tests: %#v", result.Failure)
			}
			if tc.tests > 0 && (result.Failure.Tests == nil || len(result.Failure.Tests.Tests) != tc.tests || result.Failure.Touched == nil || len(result.Failure.Touched.Tests) != tc.tests) {
				t.Fatalf("failing tests = %#v, judged %#v; want %d, each judged", result.Failure.Tests, result.Failure.Touched, tc.tests)
			}

			recovered, err := f.recover()
			if err != nil || recovered.Handback == nil {
				t.Fatalf("Recover() = %#v, %v; want the change handed back", recovered, err)
			}
			handback := f.landing().Handback
			if handback == nil || handback.FailedCheck == nil || handback.FailedCheck.Output == "" || handback.HandedBackAt == nil {
				t.Fatalf("handback = %#v, want it given to the run carrying the failed check", handback)
			}
			// What the run's record keeps, read back as a repair reads it.
			encoded, err := json.Marshal(f.run.latest())
			if err != nil {
				t.Fatal(err)
			}
			var saved runstate.State
			if err := json.Unmarshal(encoded, &saved); err != nil {
				t.Fatal(err)
			}
			failure := saved.CheckFailure
			if failure == nil || failure.Validate() != nil || failure.Command != tc.command || !strings.Contains(failure.Output, mergeQueueHandbackMarker(f.entry, *handback)) {
				t.Fatalf("the run's failed check = %#v, want the queue's check, valid and found by its marker", failure)
			}
			if tc.tests > 0 && (failure.FailingTests == nil || failure.TouchedTests == nil || len(failure.TouchedTests.Tests) != tc.tests) {
				t.Fatalf("the run's failed check names tests %#v, judged %#v; want %d", failure.FailingTests, failure.TouchedTests, tc.tests)
			}
			notes := f.tracker.Record().Item.Notes
			for _, recorded := range []struct{ where, text string }{
				{"the hand-back's record", handback.Reason + "\n" + handback.FailedCheck.Output},
				{"the run's failed check", failure.Output},
				{"the item's note", notes},
			} {
				if strings.Contains(recorded.text, fakeSecret) {
					t.Fatalf("%s carries the secret:\n%s", recorded.where, recorded.text)
				}
				for _, want := range tc.says {
					if !strings.Contains(recorded.text, want) {
						t.Fatalf("%s does not say %q:\n%s", recorded.where, want, recorded.text)
					}
				}
			}
		})
	}
}
