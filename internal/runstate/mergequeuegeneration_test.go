package runstate

// The merge queue's generations: evidence bound to exactly one candidate, a
// gate that gives no credit for anything missing, unfinished or about another
// candidate, and a record only the queue's worker writes, which says the same
// thing after a save that may not have landed.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

var testChecks = NewMergeQueueCheckConfiguration([]string{"npm test", "./scripts/lint.sh"})

func TestAVerifiedGenerationPassesItsGate(t *testing.T) {
	t.Parallel()

	if err := verifiedGeneration(t).Gate(testChecks); err != nil {
		t.Fatalf("Gate() = %v, want a verified generation", err)
	}
}

func TestEvidenceForOneGenerationNeverAuthorizesAChangedOne(t *testing.T) {
	t.Parallel()

	// Each field the binding covers is changed on its own, with the evidence
	// left as it was: none of them may be authorized by checks and a review
	// that judged something else.
	changes := map[string]func(*MergeQueueGeneration){
		"target base":         func(g *MergeQueueGeneration) { g.TargetBase = strings.Repeat("e", 40) },
		"target branch":       func(g *MergeQueueGeneration) { g.TargetBranch = "release" },
		"another head":        func(g *MergeQueueGeneration) { g.Heads = append(g.Heads, strings.Repeat("f", 40)) },
		"a different head":    func(g *MergeQueueGeneration) { g.Heads = []string{strings.Repeat("f", 40)} },
		"heads reordered":     func(g *MergeQueueGeneration) { g.Heads = []string{g.Heads[1], g.Heads[0]} },
		"candidate":           func(g *MergeQueueGeneration) { g.Candidate = strings.Repeat("9", 40) },
		"content":             func(g *MergeQueueGeneration) { g.Content = strings.Repeat("8", 40) },
		"author session":      func(g *MergeQueueGeneration) { g.AuthorSession = "session-other-author" },
		"entry":               func(g *MergeQueueGeneration) { g.EntryID = "mqe-" + strings.Repeat("1", 32) },
		"entry order":         func(g *MergeQueueGeneration) { g.EntryOrder++ },
		"check configuration": func(g *MergeQueueGeneration) { g.Checks = NewMergeQueueCheckConfiguration([]string{"npm test"}) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			generation := verifiedGeneration(t)
			binding := generation.Binding()
			change(&generation)
			if generation.Binding() == binding {
				t.Fatalf("changing the %s left the binding %s unchanged", name, binding)
			}
			configured := testChecks
			if name == "check configuration" {
				configured = generation.Checks
			}
			err := generation.Gate(configured)
			if !errors.As(err, new(MergeQueueGateError)) || !strings.Contains(err.Error(), "another generation") {
				t.Fatalf("Gate() = %v, want the earlier evidence refused as another generation's", err)
			}
		})
	}
}

func TestTheGateGivesNoCreditForMissingOrIncompleteEvidence(t *testing.T) {
	t.Parallel()

	refusals := map[string]struct {
		change func(*MergeQueueGeneration)
		want   string
	}{
		"no checks":                {func(g *MergeQueueGeneration) { g.CheckRun = nil }, "no checks were recorded"},
		"checks never finished":    {func(g *MergeQueueGeneration) { g.CheckRun.FinishedAt = nil }, "never finished"},
		"checks interrupted":       {func(g *MergeQueueGeneration) { g.CheckRun.Problem = "the runner died" }, "did not complete"},
		"a check without a result": {func(g *MergeQueueGeneration) { g.CheckRun.Results = g.CheckRun.Results[:1] }, "1 of 2"},
		"a result for another check": {func(g *MergeQueueGeneration) { g.CheckRun.Results[1].Command = "make test" },
			"where"},
		"a check that could not run": {func(g *MergeQueueGeneration) { g.CheckRun.Results[0].CouldNotRun = "no node here" },
			"could not run"},
		"a failing check":       {func(g *MergeQueueGeneration) { g.CheckRun.Results[1].Passed = false }, "did not pass"},
		"no review":             {func(g *MergeQueueGeneration) { g.Review = nil }, "no independent review"},
		"review never finished": {func(g *MergeQueueGeneration) { g.Review.FinishedAt = nil }, "never finished"},
		"review without verdict": {func(g *MergeQueueGeneration) { g.Review.Decision = ""; g.Review.Problem = "the provider stopped" },
			"did not complete"},
		"repair verdict":      {func(g *MergeQueueGeneration) { g.Review.Decision = "repair" }, `"repair"`},
		"no reviewer session": {func(g *MergeQueueGeneration) { g.Review.SessionID = "" }, "no session"},
		"reviewer is the author": {func(g *MergeQueueGeneration) { g.Review.SessionID = g.AuthorSession },
			"wrote the change"},
		"invalidated": {func(g *MergeQueueGeneration) {
			g.Invalidated = &MergeQueueInvalidation{Reason: MergeQueueTargetMoved, At: time.Now()}
		}, "invalidated"},
	}
	for name, refusal := range refusals {
		t.Run(name, func(t *testing.T) {
			generation := verifiedGeneration(t)
			refusal.change(&generation)
			if err := generation.Gate(testChecks); err == nil || !strings.Contains(err.Error(), refusal.want) {
				t.Fatalf("Gate() = %v, want a refusal naming %q", err, refusal.want)
			}
		})
	}
	// And the checks configured now are the ones that count: a generation
	// verified by an earlier configuration earns nothing under a new one.
	if err := verifiedGeneration(t).Gate(NewMergeQueueCheckConfiguration([]string{"npm test", "./scripts/lint.sh", "npm run e2e"})); err == nil {
		t.Fatal("Gate() passed a generation verified by checks the project no longer configures")
	}
}

func TestOnlyTheQueuesWorkerRecordsItsGenerations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newMergeQueueStore(t, root)
	generation := testGeneration(t, 1)
	if err := store.RecordGeneration(nil, mainQueue, generation); !errors.Is(err, ErrMergeQueueWorkerLeaseRequired) {
		t.Fatalf("RecordGeneration(no lease) = %v, want it refused", err)
	}
	other := MergeQueueKey{Repository: "yoyodyne", TargetBranch: "release"}
	otherLease := workerLease(t, store, other)
	if err := store.RecordGeneration(otherLease, mainQueue, generation); !errors.Is(err, ErrMergeQueueWorkerLeaseRequired) {
		t.Fatalf("RecordGeneration(another queue's lease) = %v, want it refused", err)
	}
	promotion := &Lease{label: "promotion", file: otherLease.file}
	if err := store.RecordGeneration(promotion, mainQueue, generation); !errors.Is(err, ErrMergeQueueWorkerLeaseRequired) {
		t.Fatalf("RecordGeneration(a lease that is not a worker's) = %v, want it refused", err)
	}
	if err := store.RecordGeneration(workerLease(t, store, mainQueue), mainQueue, generation); err != nil {
		t.Fatalf("RecordGeneration(the worker's lease) = %v", err)
	}
}

func TestARecordedGenerationOnlyEverGainsEvidence(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	lease := workerLease(t, store, mainQueue)
	first := testGeneration(t, 1)
	record := func(generation MergeQueueGeneration) error {
		return store.RecordGeneration(lease, mainQueue, generation)
	}
	if err := record(first); err != nil {
		t.Fatalf("RecordGeneration() = %v", err)
	}
	conflicts := map[string]MergeQueueGeneration{}

	rebound := first
	rebound.Candidate = strings.Repeat("9", 40)
	conflicts["a rewritten binding"] = rebound

	second := testGeneration(t, 2)
	conflicts["a second generation while the first stands"] = second

	skipped := testGeneration(t, 3)
	conflicts["a generation out of turn"] = skipped

	for name, generation := range conflicts {
		if err := record(generation); !errors.As(err, new(MergeQueueGenerationConflictError)) {
			t.Fatalf("RecordGeneration(%s) = %v, want a conflict", name, err)
		}
	}

	foreign := first
	foreign.CheckRun = &MergeQueueCheckEvidence{Binding: strings.Repeat("0", 64), StartedAt: time.Now()}
	if err := record(foreign); err == nil || !strings.Contains(err.Error(), "another generation") {
		t.Fatalf("RecordGeneration(evidence naming another binding) = %v, want it refused", err)
	}

	verified := verifiedGeneration(t)
	if err := record(verified); err != nil {
		t.Fatalf("RecordGeneration(evidence added) = %v", err)
	}
	replaced := verifiedGeneration(t)
	replaced.Review.Decision = "repair"
	if err := record(replaced); !errors.As(err, new(MergeQueueGenerationConflictError)) {
		t.Fatalf("RecordGeneration(a finished review replaced) = %v, want a conflict", err)
	}
	invalidated := verified
	invalidated.Invalidated = &MergeQueueInvalidation{Reason: MergeQueueTargetMoved, ObservedTarget: strings.Repeat("e", 40), At: time.Now().UTC()}
	if err := record(invalidated); err != nil {
		t.Fatalf("RecordGeneration(invalidated) = %v", err)
	}
	if err := record(verified); !errors.As(err, new(MergeQueueGenerationConflictError)) {
		t.Fatalf("RecordGeneration(invalidation withdrawn) = %v, want a conflict", err)
	}
	if err := record(second); err != nil {
		t.Fatalf("RecordGeneration(second, after the first was invalidated) = %v", err)
	}
	if _, err := store.VerifiedGeneration(mainQueue, first.EntryID, testChecks); !errors.As(err, new(MergeQueueGateError)) {
		t.Fatalf("VerifiedGeneration() = %v; the first generation's evidence must not answer for the second", err)
	}
}

func TestAStageThatEndedWithoutAResultIsSetAsideForAnother(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	lease := workerLease(t, store, mainQueue)
	failed := verifiedGeneration(t)
	failed.Review.Decision = ""
	failed.Review.Problem = "the provider stopped"
	if err := store.RecordGeneration(lease, mainQueue, failed); err != nil {
		t.Fatalf("RecordGeneration() = %v", err)
	}
	retried := verifiedGeneration(t)
	retried.Interruptions = []MergeQueueInterruption{{Stage: MergeQueueStageReview, StartedAt: failed.Review.StartedAt, FoundAt: time.Now().UTC(), Problem: failed.Review.Problem}}
	if err := store.RecordGeneration(lease, mainQueue, retried); err != nil {
		t.Fatalf("RecordGeneration(a fresh review after one that reached no verdict) = %v", err)
	}
	retried.Interruptions = nil
	if err := store.RecordGeneration(lease, mainQueue, retried); !errors.As(err, new(MergeQueueGenerationConflictError)) {
		t.Fatalf("RecordGeneration(an interruption removed) = %v, want a conflict", err)
	}
}

func TestAGenerationSaveThatMayNotHaveLandedIsReadBack(t *testing.T) {
	t.Parallel()

	generation := testGeneration(t, 1)

	// Landed, and the sync behind it failed: the record says it is there.
	landed := newMergeQueueStore(t, t.TempDir())
	landed.saveGeneration = func(queue *repowrite.PinnedRoot, name string, encoded []byte) error {
		return errors.Join(queue.WriteFile(name, encoded, 0o600, false), errors.New("sync failed"))
	}
	if err := landed.RecordGeneration(workerLease(t, landed, mainQueue), mainQueue, generation); err != nil {
		t.Fatalf("RecordGeneration(landed) = %v, want it recorded", err)
	}
	assertGenerations(t, landed, generation)

	// Never landed: the readback confirms it, and nothing is recorded.
	lost := newMergeQueueStore(t, t.TempDir())
	lost.saveGeneration = func(*repowrite.PinnedRoot, string, []byte) error { return errors.New("disk full") }
	if err := lost.RecordGeneration(workerLease(t, lost, mainQueue), mainQueue, generation); err == nil || errors.Is(err, ErrMergeQueueGenerationSaveUncertain) {
		t.Fatalf("RecordGeneration(lost) = %v, want the loss confirmed", err)
	}
	assertGenerations(t, lost)

	// Unknown: the save and the readback both failed, which is said as such,
	// and a later read is what settles it.
	root := t.TempDir()
	uncertain := newMergeQueueStore(t, root)
	uncertain.saveGeneration = func(queue *repowrite.PinnedRoot, name string, encoded []byte) error {
		return errors.Join(queue.WriteFile(name, encoded, 0o600, false), errors.New("connection to the disk lost"))
	}
	uncertain.readGenerationBack = func(*repowrite.PinnedRoot, string) ([]byte, error) { return nil, errors.New("still lost") }
	if err := uncertain.RecordGeneration(workerLease(t, uncertain, mainQueue), mainQueue, generation); !errors.Is(err, ErrMergeQueueGenerationSaveUncertain) {
		t.Fatalf("RecordGeneration(uncertain) = %v, want it named uncertain", err)
	}
	assertGenerations(t, newMergeQueueStore(t, root), generation)
}

func TestGenerationsSurviveARestartAndAreVerifiedFromTheRecord(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newMergeQueueStore(t, root)
	lease := workerLease(t, store, mainQueue)
	if err := store.RecordGeneration(lease, mainQueue, verifiedGeneration(t)); err != nil {
		t.Fatalf("RecordGeneration() = %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	got, err := newMergeQueueStore(t, root).VerifiedGeneration(mainQueue, verifiedGeneration(t).EntryID, testChecks)
	if err != nil || got.Binding() != verifiedGeneration(t).Binding() {
		t.Fatalf("VerifiedGeneration() = %#v, %v; want the recorded generation", got, err)
	}
	if _, err := newMergeQueueStore(t, root).VerifiedGeneration(mainQueue, "mqe-"+strings.Repeat("2", 32), testChecks); err == nil {
		t.Fatal("VerifiedGeneration() found a generation for an entry nothing was built for")
	}
}

func assertGenerations(t *testing.T, store *MergeQueueStore, want ...MergeQueueGeneration) {
	t.Helper()
	got, err := store.Generations(mainQueue, testGeneration(t, 1).EntryID)
	if err != nil {
		t.Fatalf("Generations() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Generations() = %d generations, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index].Binding() != want[index].Binding() {
			t.Fatalf("generation %d is %#v, want %#v", index+1, got[index], want[index])
		}
	}
}

func workerLease(t *testing.T, store *MergeQueueStore, key MergeQueueKey) *Lease {
	t.Helper()
	lease, held, err := store.LeaseWorker(context.Background(), key)
	if err != nil || !held {
		t.Fatalf("LeaseWorker() = %t, %v", held, err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	return lease
}

func testGeneration(t *testing.T, number uint64) MergeQueueGeneration {
	t.Helper()
	return MergeQueueGeneration{
		Number:        number,
		EntryID:       "mqe-" + strings.Repeat("a", 32),
		EntryOrder:    1,
		TargetBranch:  "main",
		TargetBase:    strings.Repeat("b", 40),
		Heads:         []string{strings.Repeat("c", 40), strings.Repeat("d", 40)},
		Candidate:     strings.Repeat("c", 39) + "1",
		Content:       strings.Repeat("7", 40),
		Checks:        testChecks,
		AuthorSession: "session-author",
		CreatedAt:     time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC),
	}
}

func verifiedGeneration(t *testing.T) MergeQueueGeneration {
	t.Helper()
	generation := testGeneration(t, 1)
	binding := generation.Binding()
	started := time.Date(2026, 10, 8, 9, 1, 0, 0, time.UTC)
	finished := started.Add(time.Minute)
	generation.CheckRun = &MergeQueueCheckEvidence{
		Binding: binding, StartedAt: started, FinishedAt: &finished,
		Results: []MergeQueueCheckResult{
			{Command: "npm test", Passed: true, Status: "succeeded"},
			{Command: "./scripts/lint.sh", Passed: true, Status: "succeeded"},
		},
	}
	reviewed := finished.Add(time.Minute)
	generation.Review = &MergeQueueReviewEvidence{
		Binding: binding, StartedAt: finished, FinishedAt: &reviewed,
		Decision: "approve", SessionID: "session-reviewer", Model: "reviewer-model",
	}
	return generation
}
