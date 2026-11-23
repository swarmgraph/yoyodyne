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
	readings    map[string]runstate.MergeQueueForgeReading
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

func (f fixtureQueues) ForgeReading(_ runstate.MergeQueueKey, entryID string) (runstate.MergeQueueForgeReading, bool, error) {
	reading, found := f.readings[entryID]
	return reading, found, nil
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

// An entry the development manager set aside is shown as set aside, with who
// set it aside and why, and as finished with; one whose run is not on her list
// yet says so.
func TestASetAsideEntryIsShownWithWhoSetItAsideAndWhy(t *testing.T) {
	t.Parallel()

	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	aside := fixtureEntry(1, "yoyodyne-fo4v", "Stuck at the head", runstate.MergeQueueHarness)
	listing := fixtureEntry(2, "yoyodyne-onxb", "Set aside, not listed yet", runstate.MergeQueueHarness)
	at := time.Date(2026, 11, 11, 9, 36, 0, 0, time.UTC)
	setAside := func(entry runstate.MergeQueueEntry, docketed *time.Time) runstate.MergeQueueLanding {
		landing := runstate.NewMergeQueueLanding(entry)
		landing.Handback = &runstate.MergeQueueHandback{
			At: at, Continuation: runstate.MergeQueueReleased, Mover: "development-manager",
			Reason: "set aside from the head of the queue", ApprovedHead: entry.ApprovedHead,
			SetAside: "its run's record refuses every hand-back", SetAsideAt: &at,
			SetAsideBy: "the development manager in conversation chat-1, turn 4", DocketedAt: docketed,
		}
		return landing
	}
	queues := fixtureQueues{
		keys:     []runstate.MergeQueueKey{key},
		entries:  map[string][]runstate.MergeQueueEntry{"main": {aside, listing}},
		landings: map[string]runstate.MergeQueueLanding{aside.EntryID: setAside(aside, &at), listing.EntryID: setAside(listing, nil)},
	}
	standing := ReadStanding(context.Background(), Sources{MergeQueues: queues})
	if len(standing.MergeQueues) != 1 || len(standing.MergeQueues[0].Entries) != 2 {
		t.Fatalf("ReadStanding().MergeQueues = %#v", standing.MergeQueues)
	}
	queue := standing.MergeQueues[0]
	if queue.Open != 0 || queue.Finished != 2 {
		t.Fatalf("queue open %d, finished %d; want a set-aside entry finished with", queue.Open, queue.Finished)
	}
	shown := queue.Entries[0]
	if shown.State != QueueEntrySetAside || shown.SetAsideBy != "the development manager in conversation chat-1, turn 4" ||
		shown.SetAsideWhy != "its run's record refuses every hand-back" {
		t.Fatalf("entry = %#v, want it set aside with who and why", shown)
	}
	rendered := standing.RenderMergeQueues()
	for _, line := range []string{
		"yoyodyne-fo4v Stuck at the head — set aside, in the harness's own queue — set aside by the development manager in conversation chat-1, turn 4: its run's record refuses every hand-back; nothing was written to run " + aside.RunID + ", which keeps its approved commit 111111111111 and its approval, and is on the development manager's list of stopped runs",
		"yoyodyne-onxb Set aside, not listed yet — set aside, in the harness's own queue — set aside by",
		"is not on the development manager's list of stopped runs yet; the queue's next pass puts it there",
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

// An entry whose merge GitHub holds is shown with what GitHub last said the
// merge was waiting on, and with the time of that reading; a reading that
// failed says GitHub could not be asked, and why, rather than a reason.
func TestAMergeGitHubHoldsIsShownWithWhatGitHubLastSaidItWasWaitingOn(t *testing.T) {
	t.Parallel()

	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	at := time.Date(2026, 11, 13, 14, 4, 0, 0, time.UTC)
	held := func(order uint64, item string, pullRequest int) (runstate.MergeQueueEntry, runstate.MergeQueueLanding) {
		entry := fixtureEntry(order, item, "Held by GitHub", runstate.MergeQueueHarness)
		landing := runstate.NewMergeQueueLanding(entry)
		landing.Attempts = []runstate.MergeQueuePromotionAttempt{{
			Number: 1, Path: runstate.MergeQueueLandThroughPullRequest, Generation: 1, PullRequest: pullRequest, IntendedAt: at,
			Mutations: []runstate.MergeQueueMutationRecord{{
				Mutation: runstate.MergeQueueRequestMerge, RequestedAt: at,
				Settled: &runstate.MergeQueueSettlement{Result: runstate.MergeQueueMutationQueued, At: at},
			}},
		}}
		return entry, landing
	}
	pending, pendingLanding := held(1, "yoyodyne-ifd.1", 1828)
	unread, unreadLanding := held(2, "yoyodyne-ifd.2", 1829)
	queues := fixtureQueues{
		keys:     []runstate.MergeQueueKey{key},
		entries:  map[string][]runstate.MergeQueueEntry{"main": {pending, unread}},
		landings: map[string]runstate.MergeQueueLanding{pending.EntryID: pendingLanding, unread.EntryID: unreadLanding},
		readings: map[string]runstate.MergeQueueForgeReading{
			pending.EntryID: {PullRequest: 1828, At: at, WaitingOn: `the required check "build" has not finished (GitHub's own words: merge state BLOCKED)`},
			unread.EntryID:  {PullRequest: 1829, At: at, Unread: "x509: certificate signed by unknown authority"},
		},
	}
	rendered := ReadStanding(context.Background(), Sources{MergeQueues: queues}).RenderMergeQueues()
	for _, line := range []string{
		"held by the forge, not landed yet",
		`GitHub said the merge of pull request 1828 was waiting on: the required check "build" has not finished`,
		at.Local().Format("2006-01-02 15:04 MST"),
		"GitHub could not be asked about pull request 1829",
		"x509: certificate signed by unknown authority",
	} {
		if !strings.Contains(rendered, line) {
			t.Fatalf("rendered queues lack %q:\n%s", line, rendered)
		}
	}

	// A reading of an earlier pull request is not said of this one.
	queues.readings[pending.EntryID] = runstate.MergeQueueForgeReading{PullRequest: 1700, At: at, WaitingOn: "an old answer"}
	if rendered := ReadStanding(context.Background(), Sources{MergeQueues: queues}).RenderMergeQueues(); strings.Contains(rendered, "an old answer") {
		t.Fatalf("a reading of another pull request was shown:\n%s", rendered)
	}
}

// The waiting entries are shown in the order the queue's rules last chose, as
// its last pass recorded it, with the rule that put the first one first; an
// entry the choice did not rank follows in the order it is worked, and a pass
// that recorded no choice leaves admission order.
func TestTheQueueIsShownInTheOrderItsRulesChoseWithTheFirstEntrysRule(t *testing.T) {
	t.Parallel()

	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	doc := fixtureEntry(1, "yoyodyne-ifd.1", "A design", runstate.MergeQueueHarness)
	code := fixtureEntry(2, "yoyodyne-ifd.2", "Ordinary code", runstate.MergeQueueHarness)
	late := fixtureEntry(3, "yoyodyne-ifd.3", "Joined since", runstate.MergeQueueHarness)
	pass := &runstate.MergeQueuePass{
		At: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC), Outcome: runstate.MergeQueuePassWaiting, Says: "waiting",
		Order: &runstate.MergeQueueOrder{Entries: []string{code.EntryID, doc.EntryID}, Rule: "code, by priority", Why: "the last change the queue landed was a document"},
	}
	queues := fixtureQueues{
		keys:    []runstate.MergeQueueKey{key},
		entries: map[string][]runstate.MergeQueueEntry{"main": {doc, code, late}},
		pass:    pass,
	}
	standing := ReadStanding(context.Background(), Sources{MergeQueues: queues})
	var order []string
	for _, entry := range standing.MergeQueues[0].Entries {
		order = append(order, entry.WorkItemID)
	}
	if got := strings.Join(order, ","); got != "yoyodyne-ifd.2,yoyodyne-ifd.1,yoyodyne-ifd.3" {
		t.Fatalf("entries shown in %s, want the chosen order and then the entry that joined since", got)
	}
	if queue := standing.MergeQueues[0]; queue.FirstEntry != code.EntryID || queue.FirstRule != "code, by priority" {
		t.Fatalf("first = %q by %q, want the code entry by its rule", queue.FirstEntry, queue.FirstRule)
	}
	rendered := standing.RenderMergeQueues()
	if !strings.Contains(rendered, "first in the queue's order (code, by priority): the last change the queue landed was a document") {
		t.Fatalf("rendered queues do not say the first entry's rule:\n%s", rendered)
	}

	queues.pass = &runstate.MergeQueuePass{At: pass.At, Outcome: runstate.MergeQueuePassWaiting, Says: "waiting"}
	standing = ReadStanding(context.Background(), Sources{MergeQueues: queues})
	if first := standing.MergeQueues[0].Entries[0].WorkItemID; first != "yoyodyne-ifd.1" || standing.MergeQueues[0].FirstRule != "" {
		t.Fatalf("with no choice recorded, first shown is %s by %q, want admission order and no rule", first, standing.MergeQueues[0].FirstRule)
	}
}

// A queue whose last pass is waiting for GitHub says so in its label, and its
// line says what it was doing and when it next tries, in local time with the
// zone named.
func TestAQueueWaitingForGitHubSaysSoAndWhenItNextTries(t *testing.T) {
	t.Parallel()

	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	entry := fixtureEntry(1, "yoyodyne-ifd.1", "Waiting for GitHub", runstate.MergeQueueHarness)
	since := time.Date(2026, 11, 13, 14, 4, 0, 0, time.UTC)
	wait := runstate.MergeQueueGitHubWait{EntryID: entry.EntryID, Wait: runstate.ForgeWait{
		Step: "asking GitHub to merge pull request 1828", Since: since, Tries: 3, NextTryAt: since.Add(3 * time.Second),
		Failure: "Connection reset by peer",
	}}
	pass := runstate.MergeQueuePass{At: since, Outcome: runstate.MergeQueuePassWaiting, EntryID: entry.EntryID, Says: wait.Says(), GitHubWait: &wait}
	queues := fixtureQueues{
		keys:    []runstate.MergeQueueKey{key},
		entries: map[string][]runstate.MergeQueueEntry{"main": {entry}},
		pass:    &pass,
	}
	rendered := ReadStanding(context.Background(), Sources{MergeQueues: queues}).RenderMergeQueues()
	for _, line := range []string{
		"last pass: waiting for GitHub to be reachable again — the merge queue is waiting for GitHub to be reachable again",
		"asking GitHub to merge pull request 1828",
		"next try at " + since.Add(3*time.Second).Local().Format("2006-01-02 15:04 MST"),
	} {
		if !strings.Contains(rendered, line) {
			t.Fatalf("rendered queues lack %q:\n%s", line, rendered)
		}
	}
}
