package readmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A change is shown as landed only once the queue confirmed it on the target:
// an approved change waiting its turn, a candidate that passed its gate, and a
// merge asked of the target whose outcome nothing established are each shown
// as what they are, and every line names the work by identifier and title.
func TestTheQueueIsShownWithoutReadingApprovalAsALanding(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	admitted := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	admit := func(title string, head byte) runstate.MergeQueueEntry {
		runID, err := runstate.NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		entry, _, err := store.Admit(context.Background(), runstate.MergeQueueAdmission{
			Key: key, WorkItemID: "yoyodyne-ifd.9", WorkItemTitle: title, RunID: runID,
			ApprovedHead: strings.Repeat(string(head), 40), IntegrationPolicy: "automatic", Mode: runstate.MergeQueueHarness,
			ModeEvidence: runstate.MergeQueueModeEvidence{ObservedAt: admitted, Explanation: "The harness runs the queue itself."}, At: admitted,
		})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	waiting := admit("Waiting its turn", 'a')
	verified := admit("Passed its gate", 'b')
	asked := admit("Merge asked for", 'c')

	lease, held, err := store.LeaseWorker(context.Background(), key)
	if err != nil || !held {
		t.Fatalf("LeaseWorker() = %t, %v", held, err)
	}
	defer lease.Release()
	for _, entry := range []runstate.MergeQueueEntry{verified, asked} {
		generation := verifiedQueueGeneration(entry)
		if err := store.RecordGeneration(lease, key, generation); err != nil {
			t.Fatal(err)
		}
	}
	// The third entry's local move was asked for and its answer never written.
	generation := verifiedQueueGeneration(asked)
	attempt := runstate.MergeQueuePromotionAttempt{
		Number: 1, Path: runstate.MergeQueueLandLocally, Generation: generation.Number, Binding: generation.Binding(),
		TargetBase: generation.TargetBase, Candidate: generation.Candidate, IntendedAt: admitted,
	}
	attempt.Key = runstate.MergeQueuePromotionKey(asked.EntryID, attempt.Number, attempt.Path, attempt.Binding, attempt.Candidate)
	attempt.Mutations = []runstate.MergeQueueMutationRecord{{
		Mutation: runstate.MergeQueueMoveTarget, Key: attempt.MutationKey(runstate.MergeQueueMoveTarget),
		Commit: attempt.Candidate, Expected: attempt.TargetBase, RequestedAt: admitted,
	}}
	landing := runstate.NewMergeQueueLanding(asked)
	landing.Attempts = []runstate.MergeQueuePromotionAttempt{attempt}
	if err := store.RecordLanding(lease, key, landing); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRefusal(context.Background(), key, runstate.MergeQueueRefusal{
		At: admitted, WorkItemID: "yoyodyne-ifd.9", RunID: waiting.RunID,
		Requirement: "main lands changes only through the forge's merge queue.", Step: "remove the merge queue requirement from main's protection rules", PersonOnly: true,
	}); err != nil {
		t.Fatal(err)
	}

	queues, problem := ReadMergeQueues(store)
	if problem != "" || len(queues) != 1 {
		t.Fatalf("ReadMergeQueues() = %#v, %q", queues, problem)
	}
	states := map[string]QueueEntryState{}
	for _, entry := range queues[0].Entries {
		states[entry.Title] = entry.State
	}
	want := map[string]QueueEntryState{"Waiting its turn": QueueEntryWaiting, "Passed its gate": QueueEntryVerified, "Merge asked for": QueueEntryUncertain}
	for title, state := range want {
		if states[title] != state {
			t.Fatalf("%q is shown as %q, want %q (states %v)", title, states[title], state, states)
		}
	}
	rendered := Standing{MergeQueues: queues}.RenderMergeQueues()
	for _, line := range []string{"yoyodyne-ifd.9 Waiting its turn", "Passed its gate — verified, not landed yet", "uncertain", "0 finished", "not in use: main lands changes only through the forge's merge queue."} {
		if !strings.Contains(rendered, line) {
			t.Fatalf("rendered queues lack %q:\n%s", line, rendered)
		}
	}
	// What a landing asked for is said in words, never as the record's own
	// value for it.
	if !strings.Contains(rendered, "landing attempt 1 asked for moving main onto the candidate") || strings.Contains(rendered, string(runstate.MergeQueueMoveTarget)) {
		t.Fatalf("the uncertain landing is not said in ordinary words:\n%s", rendered)
	}
	if strings.Contains(rendered, "— landed") || strings.Contains(rendered, "landed on") {
		t.Fatalf("nothing has landed, and the queue says something has:\n%s", rendered)
	}
}

// verifiedQueueGeneration is a generation of an entry that passed every part
// of its gate.
func verifiedQueueGeneration(entry runstate.MergeQueueEntry) runstate.MergeQueueGeneration {
	at := time.Date(2026, 10, 9, 9, 5, 0, 0, time.UTC)
	generation := runstate.MergeQueueGeneration{
		Number: 1, EntryID: entry.EntryID, EntryOrder: entry.Order, TargetBranch: entry.TargetBranch,
		TargetBase: strings.Repeat("d", 40), Heads: []string{entry.ApprovedHead}, Candidate: strings.Repeat("e", 39) + entry.ApprovedHead[:1],
		Content: strings.Repeat("f", 40), Checks: runstate.NewMergeQueueCheckConfiguration([]string{"./check.sh"}),
		AuthorSession: "developer-session", CreatedAt: at,
	}
	binding := generation.Binding()
	generation.Paths = &runstate.MergeQueuePathEvidence{Binding: binding, CheckedAt: at, Changed: 1}
	generation.CheckRun = &runstate.MergeQueueCheckEvidence{Binding: binding, StartedAt: at, FinishedAt: &at,
		Results: []runstate.MergeQueueCheckResult{{Command: "./check.sh", Passed: true, Status: "succeeded"}}}
	generation.Review = &runstate.MergeQueueReviewEvidence{Binding: binding, StartedAt: at, FinishedAt: &at,
		Decision: "approve", SessionID: "reviewer-session", Model: "reviewer-model"}
	return generation
}

// fixtureQueues is merge queue records held in memory, keyed by entry.
type fixtureQueues struct {
	keys        []runstate.MergeQueueKey
	keysErr     error
	entries     map[string][]runstate.MergeQueueEntry
	generations map[string][]runstate.MergeQueueGeneration
	landings    map[string]runstate.MergeQueueLanding
	unreadable  map[string]error
	refusal     *runstate.MergeQueueRefusal
	pass        *runstate.MergeQueuePass
}

func (f fixtureQueues) Keys() ([]runstate.MergeQueueKey, error) { return f.keys, f.keysErr }
func (f fixtureQueues) Entries(key runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error) {
	return f.entries[key.TargetBranch], nil
}
func (f fixtureQueues) Generations(_ runstate.MergeQueueKey, entryID string) ([]runstate.MergeQueueGeneration, error) {
	return f.generations[entryID], f.unreadable[entryID]
}
func (f fixtureQueues) Landing(_ runstate.MergeQueueKey, entryID string) (runstate.MergeQueueLanding, bool, error) {
	landing, found := f.landings[entryID]
	return landing, found, nil
}
func (f fixtureQueues) Refusal(runstate.MergeQueueKey) (runstate.MergeQueueRefusal, bool, error) {
	if f.refusal == nil {
		return runstate.MergeQueueRefusal{}, false, nil
	}
	return *f.refusal, true, nil
}
func (f fixtureQueues) LastPass(runstate.MergeQueueKey) (runstate.MergeQueuePass, bool, error) {
	if f.pass == nil {
		return runstate.MergeQueuePass{}, false, nil
	}
	return *f.pass, true, nil
}

func fixtureEntry(order uint64, item, title string, mode runstate.MergeQueueMode) runstate.MergeQueueEntry {
	return runstate.MergeQueueEntry{
		EntryID: "mqe-" + strings.Repeat(string(rune('0'+order)), 32), Order: order, Repository: "yoyodyne", TargetBranch: "main",
		WorkItemID: item, WorkItemTitle: title, RunID: "run-" + strings.Repeat(string(rune('0'+order)), 32), Mode: mode,
		ApprovedHead: strings.Repeat(string(rune('0'+order)), 40),
		ModeEvidence: runstate.MergeQueueModeEvidence{Explanation: "The harness runs the queue itself."},
	}
}

// Each entry is projected with its mode, where it stands and why, in the
// order the queue works it, with its finished entries after the open ones; a
// record that cannot be read is said, not skipped; and the shared read model
// carries the queue beside everything else it reads.
func TestTheReadModelProjectsEachQueueEntryFromItsRecords(t *testing.T) {
	t.Parallel()

	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	landed := fixtureEntry(1, "yoyodyne-ifd.1", "Landed already", runstate.MergeQueueHarness)
	handed := fixtureEntry(2, "yoyodyne-ifd.2", "Handed back", runstate.MergeQueueHarness)
	waiting := fixtureEntry(3, "yoyodyne-ifd.3", "Waiting its turn", runstate.MergeQueueForge)
	torn := fixtureEntry(4, "yoyodyne-ifd.4", "Torn candidates", runstate.MergeQueueHarness)
	at := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	completed := runstate.NewMergeQueueLanding(landed)
	completed.Completion = &runstate.MergeQueueCompletion{Generation: 1, Landed: strings.Repeat("9", 40), QueueAt: at, RunAt: &at, WorkItemAt: &at}
	handedBack := runstate.NewMergeQueueLanding(handed)
	handedBack.Handback = &runstate.MergeQueueHandback{
		At: at, Class: runstate.MergeQueueCandidateDefect, Continuation: runstate.MergeQueueContinueRepair,
		Mover: "developer", Reason: "make test failed on the candidate", Generation: 1,
	}
	queues := fixtureQueues{
		keys:     []runstate.MergeQueueKey{key},
		entries:  map[string][]runstate.MergeQueueEntry{"main": {landed, handed, waiting, torn}},
		landings: map[string]runstate.MergeQueueLanding{landed.EntryID: completed, handed.EntryID: handedBack},
		unreadable: map[string]error{
			torn.EntryID: errors.New("decode generations: unexpected end of JSON input"),
		},
		pass: &runstate.MergeQueuePass{At: at, Outcome: runstate.MergeQueuePassWaiting, EntryID: waiting.EntryID, WorkItemID: waiting.WorkItemID,
			Says: "the review is not asked for while the operator's pause stands"},
	}

	standing := ReadStanding(context.Background(), Sources{MergeQueues: queues})
	if len(standing.MergeQueues) != 1 {
		t.Fatalf("ReadStanding().MergeQueues = %#v", standing.MergeQueues)
	}
	if !strings.Contains(standing.MergeQueuesProblem, "decode generations") {
		t.Fatalf("MergeQueuesProblem = %q, want the torn record said", standing.MergeQueuesProblem)
	}
	queue := standing.MergeQueues[0]
	type shown struct {
		item  string
		mode  runstate.MergeQueueMode
		state QueueEntryState
	}
	var got []shown
	for _, entry := range queue.Entries {
		got = append(got, shown{entry.WorkItemID, entry.Mode, entry.State})
	}
	want := []shown{
		{"yoyodyne-ifd.2", runstate.MergeQueueHarness, QueueEntryHandedBack},
		{"yoyodyne-ifd.3", runstate.MergeQueueForge, QueueEntryWaiting},
		{"yoyodyne-ifd.4", runstate.MergeQueueHarness, QueueEntryUncertain},
		{"yoyodyne-ifd.1", runstate.MergeQueueHarness, QueueEntryLanded},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if queue.Finished != 1 || queue.LastPass == nil || queue.LastPass.Outcome != runstate.MergeQueuePassWaiting {
		t.Fatalf("queue = %#v, want one finished and the waiting pass", queue)
	}
	rendered := standing.RenderMergeQueues()
	for _, raw := range []string{"(repair)", "harness queue", "forge queue", "developer to move"} {
		if strings.Contains(rendered, raw) {
			t.Fatalf("rendered queues carry the record's own value %q rather than words:\n%s", raw, rendered)
		}
	}
	for _, line := range []string{
		"Merge queue for main: 3 waiting to land, 1 finished",
		"last pass: waiting — the review is not asked for while the operator's pause stands",
		"yoyodyne-ifd.2 Handed back — handed back, in the harness's own queue — sent back to its run for repair; the next move is the developer's: make test failed on the candidate",
		"yoyodyne-ifd.3 Waiting its turn — waiting, in the forge's merge queue",
		"yoyodyne-ifd.1 Landed already — landed, in the harness's own queue — landed on main as 999999999999, confirmed on the target",
		"decode generations",
	} {
		if !strings.Contains(rendered, line) {
			t.Fatalf("rendered queues lack %q:\n%s", line, rendered)
		}
	}
}

// One queue that cannot be listed does not hide the others, and a product
// with no queue prints nothing at all.
func TestAnUnlistableQueueIsSaidBesideTheOthersAndNoQueuePrintsNothing(t *testing.T) {
	t.Parallel()

	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	queues := fixtureQueues{
		keys: []runstate.MergeQueueKey{key}, keysErr: errors.New("decode the merge queue release: unexpected EOF"),
		entries: map[string][]runstate.MergeQueueEntry{"main": {fixtureEntry(1, "yoyodyne-ifd.1", "Waiting", runstate.MergeQueueHarness)}},
	}
	read, problem := ReadMergeQueues(queues)
	if len(read) != 1 || !strings.Contains(problem, "decode the merge queue release") {
		t.Fatalf("ReadMergeQueues() = %#v, %q; want main read and release said", read, problem)
	}
	if rendered := ReadStanding(context.Background(), Sources{MergeQueues: fixtureQueues{}}).RenderMergeQueues(); rendered != "" {
		t.Fatalf("no queue rendered as %q, want nothing", rendered)
	}
	if rendered := ReadStanding(context.Background(), Sources{}).RenderMergeQueues(); rendered != "" {
		t.Fatalf("no queue store rendered as %q, want nothing", rendered)
	}
}
