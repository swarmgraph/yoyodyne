package readmodel

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// assertOperatorSliceIsTheLine holds a standing to the rule that the problems
// whose owner is the operator are exactly the needs-a-human line's entries
// whose owner is the operator.
func assertOperatorSliceIsTheLine(t *testing.T, standing Standing) {
	t.Helper()
	var line, slice []string
	for _, entry := range standing.NeedsHuman {
		if entry.Mover.IsOperator() {
			line = append(line, attentionIdentity(entry))
		}
	}
	for _, problem := range OperatorsProblems(standing.Problems) {
		slice = append(slice, attentionIdentity(problem.Entry))
	}
	slices.Sort(line)
	slices.Sort(slice)
	if !slices.Equal(line, slice) {
		t.Fatalf("the operator's problems %v are not the needs-a-human line's operator entries %v", slice, line)
	}
}

func problemOf(t *testing.T, problems []Problem, kind AttentionKind) Problem {
	t.Helper()
	for _, problem := range problems {
		if problem.Entry.Kind == kind {
			return problem
		}
	}
	t.Fatalf("no problem of attention kind %s among %+v", kind, problems)
	return Problem{}
}

// One entry of each of the four kinds: a failing pass is broken, a queue
// nothing pulls is stalled, a proposed change nobody decided is blocked, and a
// run asleep on the provider's reset is degraded. Each carries its age, count,
// cost with the money it cannot price said as not recorded, the registry's
// owner, and its sources; and the list runs broken first.
func TestEveryProblemKindIsListedWithItsAgeCountCostAndOwner(t *testing.T) {
	t.Parallel()
	fixtures := attentionOfEveryKind(t)
	resetsAt := moment.Add(time.Hour)
	standing := Standing{
		ObservedAt: moment,
		NeedsHuman: []Attention{
			fixtures[AttentionAmendment].entry,
			fixtures[AttentionStall].entry,
			fixtures[AttentionFailingTask].entry,
		},
		CapacityBlocked: CapacityBlocked{Runs: []CapacityBlockedRun{{
			RunID: "run-parked", WorkItemID: "yoyodyne-ifd.150", State: CapacityStateWaiting, RefusedBy: "an exhausted usage limit",
			Since: moment.Add(-2 * time.Hour), ResetsAt: &resetsAt, Remedy: waitingRunRemedy,
		}}},
	}
	problems := DeriveProblems(standing, nil)
	want := map[AttentionKind]ProblemKind{
		AttentionFailingTask:     ProblemBroken,
		AttentionStall:           ProblemStalled,
		AttentionAmendment:       ProblemBlocked,
		AttentionCapacityBlocked: ProblemDegraded,
	}
	if len(problems) != len(want) {
		t.Fatalf("problems = %+v, want one of each kind", problems)
	}
	for index, kind := range ProblemKinds() {
		if problems[index].Kind != kind {
			t.Fatalf("problem %d is %s, want %s: the list runs broken, stalled, blocked, degraded", index, problems[index].Kind, kind)
		}
	}
	for attention, kind := range want {
		problem := problemOf(t, problems, attention)
		if problem.Kind != kind || !problem.Kind.Valid() {
			t.Errorf("%s is a %s problem, want %s", attention, problem.Kind, kind)
		}
		if answer := ownership.Resolve(problem.Entry.Kind, problem.Entry.ownershipRecord()); problem.Entry.Mover != answer.Owner {
			t.Errorf("%s is owned by %s, want the registry's %s", attention, problem.Entry.Mover, answer.Owner)
		}
		if problem.Count < 1 || problem.Cost.Money != MoneyNotRecorded || len(problem.Sources) == 0 || problem.Identity == "" {
			t.Errorf("%s = %+v, want a count, sources, an identity, and money said as not recorded", attention, problem)
		}
	}
	pass := problemOf(t, problems, AttentionFailingTask)
	if pass.Count != 2 || pass.Cost.Runs != 2 || pass.Since == nil || pass.Since.After(moment) || pass.Cost.StoodSeconds == nil {
		t.Errorf("the failing pass = %+v, want its two failures in a row, since when, and how long it has stood", pass)
	}
	parked := problemOf(t, problems, AttentionCapacityBlocked)
	if parked.Entry.Mover != MoverHarness || parked.Since == nil || !parked.Since.Equal(moment.Add(-2*time.Hour)) || *parked.Cost.StoodSeconds != 7200 || parked.Entry.WorkItemID != "yoyodyne-ifd.150" {
		t.Errorf("the parked run = %+v, want the harness's with its age", parked)
	}
	if blocked := problemOf(t, problems, AttentionAmendment); blocked.FixedBy != "" {
		t.Errorf("the proposed change names a fixing item %q with no listing to find one in", blocked.FixedBy)
	}
}

// Provider capacity is shown among the problems and nowhere of its own: every
// run and conversation held on it, the hold over every role, and a usage window
// past its threshold are each a degraded problem, and none is the operator's.
func TestProviderCapacityIsADegradedKindOfProblem(t *testing.T) {
	t.Parallel()
	hold := CapacityHold{Holding: true, Since: moment.Add(-time.Hour), ResetsAt: moment.Add(time.Hour), Refusals: 4, ParkedRuns: 2}
	holdEntry, _ := hold.Attention()
	standing := Standing{
		ObservedAt:      moment,
		NeedsHuman:      []Attention{holdEntry},
		FactoryProblems: []Attention{degradedCapacityAttention(degradedFixture(moment))},
		CapacityBlocked: CapacityBlocked{
			Runs: []CapacityBlockedRun{
				{RunID: "run-stopped", WorkItemID: "yoyodyne-ifd.201", State: CapacityStateBlocked, RefusedBy: "a transient server overload", Since: moment.Add(-3 * time.Hour), Remedy: blockedRunRemedy},
				{RunID: "run-window", WorkItemID: "yoyodyne-ifd.200", State: CapacityStateBlocked, RefusedBy: "an exhausted usage limit", Since: moment.Add(-time.Hour), Remedy: windowRunRemedy},
			},
			Conversations: []CapacityBlockedConversation{{ConversationID: "chat-0123456789", State: CapacityStateBlocked, Waiting: "the architect's conversation chat-0123456789", RefusedBy: "an exhausted usage limit", Since: moment.Add(-30 * time.Minute), Refusals: 3, Remedy: refusedTurnRemedy}},
		},
	}
	problems := DeriveProblems(standing, nil)
	if len(problems) != 5 {
		t.Fatalf("problems = %+v, want the hold, the window, two runs, and the conversation", problems)
	}
	owners := map[string]Mover{}
	for _, problem := range problems {
		if problem.Kind != ProblemDegraded || problem.OperatorsMove() {
			t.Errorf("%s is %s owned by %s, want degraded and not the operator's", problem.Identity, problem.Kind, problem.Entry.Mover)
		}
		owners[problem.Entry.ID] = problem.Entry.Mover
	}
	for id, want := range map[string]Mover{"run-stopped": MoverDevelopmentManager, "run-window": MoverHarness, "chat-0123456789": MoverNobody, HoldCapacity: MoverHarness} {
		if owners[id] != want {
			t.Errorf("%s is owned by %s, want %s", id, owners[id], want)
		}
	}
	turn := problemOf(t, problems, AttentionCapacityBlocked)
	for _, problem := range problems {
		if problem.Entry.ID == "chat-0123456789" {
			turn = problem
		}
	}
	if turn.Count != 3 || !strings.Contains(turn.Entry.What(), "3 turn(s) refused") {
		t.Errorf("the refused conversation = %+v, want its three refused turns counted", turn)
	}
}

// The problems whose owner is the operator are the needs-a-human line's: an
// entry the line carries is a problem, and an entry beside the line that is
// the operator's is put on the line rather than shown only among the problems.
func TestTheOperatorsProblemsAreTheNeedsAHumanLine(t *testing.T) {
	t.Parallel()
	fixtures := attentionOfEveryKind(t)
	var operators []Attention
	for _, kind := range AttentionKinds() {
		if entry := fixtures[kind].entry; entry.Mover.IsOperator() {
			operators = append(operators, entry)
		}
	}
	if len(operators) < 2 {
		t.Fatalf("want two fixtures the operator owns, found %d", len(operators))
	}
	mine, notOnTheLine := operators[0], operators[1]
	standing := Standing{
		ObservedAt:      moment,
		NeedsHuman:      []Attention{mine, fixtures[AttentionStall].entry, fixtures[AttentionCapacityDegraded].entry},
		FactoryProblems: []Attention{notOnTheLine, fixtures[AttentionCapacityDegraded].entry},
	}
	standing.Problems = DeriveProblems(standing, nil)
	standing.reconcileOperatorSlice()
	assertOperatorSliceIsTheLine(t, standing)
	if len(OperatorsProblems(standing.Problems)) < 2 {
		t.Fatalf("the operator's problems = %+v, want both of his entries", OperatorsProblems(standing.Problems))
	}
	for _, problem := range standing.Problems {
		if !problem.OperatorsMove() && problem.Entry.Mover == "" {
			t.Errorf("%s has no owner", problem.Identity)
		}
	}
}

// An entry reached from two lines is one problem, said once and counted once;
// a merge the forge has queued with its checks passing is no problem; and the
// fixing item is the open item whose own text cites the problem's record, the
// most urgent first, never a closed one or the item the problem is about.
func TestAProblemIsSaidOnceAndNamesTheItemThatCitesIt(t *testing.T) {
	t.Parallel()
	fixtures := attentionOfEveryKind(t)
	action := fixtures[AttentionAmendment].entry
	green := awaitingForgeAttention(runstate.State{
		RunID: "run-green", WorkItemID: "yoyodyne-ifd.412", Branch: "yoyodyne/item/green",
		Integration: &runstate.Integration{TargetBranch: "main"},
		PullRequest: &runstate.PullRequest{Number: 568, URL: "https://forge.example/pr/568", MergeQueued: true, Checks: &runstate.PullRequestChecks{}},
	})
	standing := Standing{
		ObservedAt:      moment,
		NeedsHuman:      []Attention{action, green},
		FactoryProblems: []Attention{action},
	}
	items := []beads.WorkItem{
		{ID: "yoyodyne-closed", Status: "closed", Priority: 0, Notes: "fixes amendment-0123456789abcdef0123456789abcdef"},
		{ID: "yoyodyne-later", Status: "open", Priority: 3, Description: "also amendment-0123456789abcdef0123456789abcdef"},
		{ID: "yoyodyne-fix", Status: "open", Priority: 1, Notes: "Answers amendment-0123456789abcdef0123456789abcdef."},
	}
	if action.WorkItemID != "" {
		items = append(items, beads.WorkItem{ID: action.WorkItemID, Status: "open", Priority: 0, Notes: "amendment-0123456789abcdef0123456789abcdef"})
	}
	problems := DeriveProblems(standing, items)
	if len(problems) != 1 {
		t.Fatalf("problems = %+v, want the finding once and the green merge left out", problems)
	}
	if problems[0].Count != 1 {
		t.Errorf("the finding reached from two lines counts %d, want 1", problems[0].Count)
	}
	if problems[0].FixedBy != "yoyodyne-fix" {
		t.Errorf("fixed by %q, want the most urgent open item citing the proposed change", problems[0].FixedBy)
	}
	encoded, err := json.Marshal(problems[0])
	if err != nil {
		t.Fatal(err)
	}
	var back Problem
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("a problem does not read back: %v\n%s", err, encoded)
	}
	if back.Identity != problems[0].Identity || back.Entry.Mover != problems[0].Entry.Mover {
		t.Errorf("read back %+v from %s", back, encoded)
	}
}
