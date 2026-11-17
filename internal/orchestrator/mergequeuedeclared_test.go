package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// countingCheck is a check that appends a line to a file each time it runs, and
// the file.
func countingCheck(t *testing.T) (string, string) {
	t.Helper()
	counted := filepath.Join(t.TempDir(), "counted.txt")
	return "echo ran >> " + counted, counted
}

func lines(t *testing.T, path string) int {
	t.Helper()
	read, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(read)))
}

// declaredQueue is a queue run whose project configures three checks: one told
// what the change touches, one that counts its runs, and the race check, told
// the same way. Its queue declares only the first two, or only the first.
func declaredQueue(t *testing.T) (r *queueRun, wholeTold, counted, raceTold string) {
	t.Helper()
	r, wholeTold = newGoQueueRun(t)
	count, counted := countingCheck(t)
	race, raceTold := toldCheck(t)
	r.pipeline.Config.Checks = []string{r.pipeline.Config.Checks[0], count, race}
	r.pipeline.Config.Execution.MergeQueue = true
	return r, wholeTold, counted, raceTold
}

func landOne(t *testing.T, r *queueRun) (runstate.MergeQueueGeneration, runstate.State) {
	t.Helper()
	outcome := r.run(t)
	if outcome.Status != runstate.StatusSucceeded || outcome.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue", outcome)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if pass := r.driver().Pass(ctx, outcome.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the candidate landed", pass)
	}
	generations, err := r.queue.Generations(outcome.MergeQueue.Key(), outcome.MergeQueue.EntryID)
	if err != nil || len(generations) == 0 {
		t.Fatalf("generations = %#v, %v", generations, err)
	}
	state, err := r.runs.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return generations[len(generations)-1], state
}

// A project that declares no list has every configured check run whole on the
// candidate.
func TestWithNoDeclaredListTheQueueRunsEveryConfiguredCheckWhole(t *testing.T) {
	t.Parallel()

	r, wholeTold, counted, raceTold := declaredQueue(t)
	r.pipeline.Config.MergeQueueRaceCheck = "" // nothing declared, so nothing is added either
	generation, _ := landOne(t, r)
	if !slices.Equal(generation.Checks.Commands, r.pipeline.Config.Checks) || generation.Checks.Declared != nil {
		t.Fatalf("the queue checked the candidate by %#v, want every configured check and no declaration", generation.Checks)
	}
	if got := readTold(t, wholeTold); !slices.Equal(got, []string{"./internal/checks", "./..."}) {
		t.Fatalf("the first check was told %q, want the run's narrowed pass and the queue's whole one", got)
	}
	if got := readTold(t, raceTold); !slices.Equal(got, []string{"./internal/checks", "./..."}) {
		t.Fatalf("the race check was told %q, want it run whole by the queue", got)
	}
	if lines(t, counted) != 2 {
		t.Fatalf("the counting check ran %d times, want the run's and the queue's", lines(t, counted))
	}
}

// A project that declares a list has exactly those checks run whole on the
// candidate, and the list and its digest are on the candidate's evidence.
func TestADeclaredListIsExactlyWhatTheQueueRunsAndIsRecordedWithTheCandidate(t *testing.T) {
	t.Parallel()

	r, wholeTold, counted, raceTold := declaredQueue(t)
	declared := []string{r.pipeline.Config.Checks[0]}
	r.pipeline.Config.MergeQueueChecks = declared
	generation, _ := landOne(t, r)
	if !slices.Equal(generation.Checks.Commands, declared) {
		t.Fatalf("the queue checked the candidate by %q, want exactly the declared %q", generation.Checks.Commands, declared)
	}
	want := runstate.NewMergeQueueCheckConfiguration(declared).Digest
	if generation.Checks.Declared == nil || !slices.Equal(generation.Checks.Declared.Commands, declared) || generation.Checks.Declared.Digest != want || generation.Checks.Digest != want {
		t.Fatalf("recorded declaration = %#v (digest %q), want the list %q with digest %q", generation.Checks.Declared, generation.Checks.Digest, declared, want)
	}
	if got := readTold(t, wholeTold); !slices.Equal(got, []string{"./internal/checks", "./..."}) {
		t.Fatalf("the declared check was told %q, want it run whole over the candidate", got)
	}
	if lines(t, counted) != 1 || len(readTold(t, raceTold)) != 1 {
		t.Fatalf("the checks left off the list ran %d and %d times, want only the run's own before review", lines(t, counted), len(readTold(t, raceTold)))
	}
	if got := generation.CheckRun; got == nil || len(got.Results) != 1 || got.Results[0].Command != declared[0] || !got.Results[0].Passed {
		t.Fatalf("check run = %#v, want one passing result of the declared check", got)
	}
}

// A change whose approved head has a passing race result is not checked by the
// race check again at the queue, and one whose head has none is, told only the
// packages the change touches.
func TestTheQueueAddsTheNarrowedRaceCheckWhereTheApprovedHeadHasNoPassingResult(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		forget    bool
		wantTold  []string
		wantAdded bool
	}{
		{name: "the head passed the race check", wantTold: []string{"./internal/checks"}},
		{name: "the head has no passing race result", forget: true, wantTold: []string{"./internal/checks", "./internal/checks"}, wantAdded: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			r, _, _, raceTold := declaredQueue(t)
			race := r.pipeline.Config.Checks[2]
			declared := r.pipeline.Config.Checks[:2:2]
			r.pipeline.Config.MergeQueueChecks, r.pipeline.Config.MergeQueueRaceCheck = declared, race
			outcome := r.run(t)
			if outcome.MergeQueue == nil {
				t.Fatalf("Run() = %#v, want the change admitted", outcome)
			}
			if test.forget {
				state, err := r.runs.Load(outcome.RunID)
				if err != nil {
					t.Fatal(err)
				}
				state.ChecksPassed.Commands = slices.DeleteFunc(slices.Clone(state.ChecksPassed.Commands), func(command string) bool { return command == race })
				if err := r.runs.Save(state); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if pass := r.driver().Pass(ctx, outcome.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassLanded {
				t.Fatalf("Pass() = %#v, want the candidate landed", pass)
			}
			generations, err := r.queue.Generations(outcome.MergeQueue.Key(), outcome.MergeQueue.EntryID)
			if err != nil || len(generations) == 0 {
				t.Fatalf("generations = %#v, %v", generations, err)
			}
			generation := generations[len(generations)-1]
			if got := slices.Contains(generation.Checks.Commands, race); got != test.wantAdded {
				t.Fatalf("the queue checked the candidate by %q; race check among them = %v, want %v", generation.Checks.Commands, got, test.wantAdded)
			}
			if generation.Checks.Declared == nil || !slices.Equal(generation.Checks.Declared.Commands, declared) {
				t.Fatalf("recorded declaration = %#v, want the declared list %q without the added check", generation.Checks.Declared, declared)
			}
			if got := readTold(t, raceTold); !slices.Equal(got, test.wantTold) {
				t.Fatalf("the race check was told %q, want %q", got, test.wantTold)
			}
		})
	}
}

func TestARaceResultOwedIsReadFromTheApprovedHeadsRecord(t *testing.T) {
	t.Parallel()

	const race, head = "make race", "abc"
	passed := func(mutate func(*runstate.ChecksPassed)) runstate.State {
		evidence := &runstate.ChecksPassed{Commit: head, Commands: []string{"make test", race}, Narrowed: "./a"}
		mutate(evidence)
		return runstate.State{ChecksPassed: evidence}
	}
	for _, test := range []struct {
		name string
		run  runstate.State
		want bool
	}{
		{name: "passed over the approved head", run: passed(func(*runstate.ChecksPassed) {}), want: false},
		{name: "passed whole over the approved head", run: passed(func(c *runstate.ChecksPassed) { c.Narrowed = "" }), want: false},
		{name: "no record of the change", run: runstate.State{}, want: true},
		{name: "passed over another commit", run: passed(func(c *runstate.ChecksPassed) { c.Commit = "other" }), want: true},
		{name: "could not run", run: passed(func(c *runstate.ChecksPassed) { c.Commands = []string{"make test"} }), want: true},
		{name: "the change touches no Go package", run: passed(func(c *runstate.ChecksPassed) {
			c.Commands, c.Narrowed = []string{"make test"}, "no Go package (the change touches none)"
		}), want: false},
	} {
		if got := raceOwed(test.run, head, race); got != test.want {
			t.Errorf("%s: raceOwed() = %v, want %v", test.name, got, test.want)
		}
	}
}

// With the queue on, the run's own whole check is left to the queue only where
// the queue ran its declared list whole on the landing commit, every check on it
// passed, and the approved head carries a passing race result or the queue ran
// the narrowed race check.
func TestTheQueueStandsInForADeclaredListOnlyWhereItsRaceCheckIsCovered(t *testing.T) {
	t.Parallel()

	finished := time.Now()
	generation := func(results ...runstate.MergeQueueCheckResult) runstate.MergeQueueGeneration {
		g := runstate.MergeQueueGeneration{Number: 3, EntryID: "entry", TargetBranch: "main", TargetBase: "base", Candidate: "candidate"}
		g.CheckRun = &runstate.MergeQueueCheckEvidence{Binding: g.Binding(), FinishedAt: &finished, Results: results}
		return g
	}
	pass := func(command string) runstate.MergeQueueCheckResult {
		return runstate.MergeQueueCheckResult{Command: command, Passed: true}
	}
	run := func(raceOnHead bool) runstate.State {
		commands := []string{"make test", "make vet", "make race"}
		if !raceOnHead {
			commands = commands[:2]
		}
		return runstate.State{RunID: "run-1", ChecksPassed: &runstate.ChecksPassed{Commit: "head", Commands: commands, Narrowed: "./a", WholeLeftToQueue: true}}
	}
	declaration := queueDeclaration{Checks: []string{"make test", "make vet"}, Race: "make race", Head: "head"}
	for _, test := range []struct {
		name       string
		run        runstate.State
		generation runstate.MergeQueueGeneration
		want       string
	}{
		{name: "the head passed the race check and the queue ran its list", run: run(true), generation: generation(pass("make test"), pass("make vet"))},
		{name: "the queue ran the narrowed race check the head lacked", run: run(false), generation: generation(pass("make test"), pass("make vet"), pass("make race"))},
		{name: "the head lacks the race check and the queue did not run it", run: run(false), generation: generation(pass("make test"), pass("make vet")), want: "make race is not among the queue's checks"},
		{name: "a declared check did not pass", run: run(true), generation: generation(pass("make test"), runstate.MergeQueueCheckResult{Command: "make vet"}), want: "make vet did not pass"},
		{name: "a declared check was not run", run: run(true), generation: generation(pass("make test")), want: "make vet is not among the queue's checks"},
	} {
		got := mergeQueueStandsIn(test.run, runstate.MergeQueueHarness, test.generation, nil, declaration)
		if test.want == "" && got != "" || test.want != "" && !strings.Contains(got, test.want) {
			t.Errorf("%s: mergeQueueStandsIn() = %q, want %q", test.name, got, test.want)
		}
	}
}
