package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// repeatedStopQueues is a queue whose head the worker's passes in history
// ended on.
func repeatedStopQueues(history []runstate.MergeQueuePass) (fixtureQueues, runstate.MergeQueueEntry) {
	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	head := fixtureEntry(5, "yoyodyne-ifd.435.48", "MCP calls piece 7", runstate.MergeQueueHarness)
	behind := fixtureEntry(6, "yoyodyne-g2l4", "The GitHub-wait fix", runstate.MergeQueueHarness)
	queues := fixtureQueues{
		keys:        []runstate.MergeQueueKey{key},
		entries:     map[string][]runstate.MergeQueueEntry{"main": {head, behind}},
		generations: map[string][]runstate.MergeQueueGeneration{head.EntryID: {verifiedQueueGeneration(head)}},
		landings:    map[string]runstate.MergeQueueLanding{},
		readings:    map[string]runstate.MergeQueueForgeReading{},
		history:     history,
	}
	if len(history) > 0 {
		last := history[len(history)-1]
		queues.pass = &last
	}
	return queues, head
}

const lockedCheckout = "the candidate's checkout could not be made: a locked checkout record names a folder that is gone"

func stoppedOn(entry runstate.MergeQueueEntry, at time.Time, says string) runstate.MergeQueuePass {
	return runstate.MergeQueuePass{At: at, Outcome: runstate.MergeQueuePassStopped, EntryID: entry.EntryID, WorkItemID: entry.WorkItemID, Says: says}
}

func repeatedStopsOf(standing Standing) []Attention {
	var found []Attention
	for _, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionMergeQueueOverdue && entry.QueueOverdue != nil && entry.QueueOverdue.StoppedPasses > 0 {
			found = append(found, entry)
		}
	}
	return found
}

// A merge queue whose worker stops on the same entry for the same reason on
// three passes in a row is said on status and in the owner's summary, naming
// the entry, its item by title, the reason, since when in local time, and the
// development manager as the one to move it, though no step has run past its
// bound; two such passes are not yet a problem, and a later pass that ends any
// other way, or a stop for another reason, leaves no entry.
func TestAMergeQueueStopThatRepeatsIsTheDevelopmentManagersToMove(t *testing.T) {
	t.Parallel()
	first := time.Date(2026, 11, 12, 4, 17, 0, 0, time.UTC)
	_, head := repeatedStopQueues(nil)
	stops := []runstate.MergeQueuePass{
		{At: first.Add(-time.Minute), Outcome: runstate.MergeQueuePassWaiting, Says: "waiting"},
		stoppedOn(head, first, lockedCheckout),
		stoppedOn(head, first.Add(time.Minute), lockedCheckout),
		stoppedOn(head, first.Add(2*time.Minute), lockedCheckout),
	}
	now := first.Add(3 * time.Minute)
	read := func(history []runstate.MergeQueuePass) Standing {
		queues, _ := repeatedStopQueues(history)
		return ReadStanding(context.Background(), Sources{MergeQueues: queues, Now: func() time.Time { return now }})
	}

	standing := read(stops)
	found := repeatedStopsOf(standing)
	if len(found) != 1 || found[0].ID != head.EntryID || found[0].WorkItemID != head.WorkItemID {
		t.Fatalf("after three identical stops, the attention line carries %+v, want the stopped entry", found)
	}
	entry := found[0]
	if entry.Mover != ownership.MoverDevelopmentManager {
		t.Fatalf("mover = %q, want the development manager", entry.Mover)
	}
	if !entry.Since().Equal(first) || entry.QueueOverdue.StoppedPasses != 3 {
		t.Fatalf("since %s after %d passes, want since the first stop, %s, after 3", entry.Since(), entry.QueueOverdue.StoppedPasses, first)
	}
	for _, want := range []string{"merge queue entry 5 for main", "MCP calls piece 7", "3 passes in a row", "since " + localMoment(first), "it stops because " + lockedCheckout} {
		if !strings.Contains(entry.What(), want) {
			t.Errorf("what = %q, want it to say %q", entry.What(), want)
		}
	}
	if zone := first.Local().Format("MST"); !strings.Contains(entry.What(), zone) {
		t.Errorf("what = %q, want the time with its zone %s named", entry.What(), zone)
	}
	if overdue := standing.MergeQueues[0].Entries[0].Overdue; overdue == nil || overdue.Mover != ownership.MoverDevelopmentManager || overdue.OwnerReason != "" {
		t.Errorf("the queue's own entry carries %+v, want the development manager named on it and no reason", overdue)
	}
	if whose := entry.Whose(); !strings.HasPrefix(whose, "the development manager's") || !strings.Contains(whose, "yoyo queue hand-back "+head.EntryID) {
		t.Errorf("whose = %q, want the development manager's, with the command that moves it", whose)
	}
	if rendered := standing.RenderMergeQueues(); !strings.Contains(rendered, "PROBLEM, the development manager's to move: merge queue entry 5") {
		t.Errorf("rendered queue does not mark the repeated stop:\n%s", rendered)
	}
	summary := Summarize(SummaryInputs{Standing: &standing, Now: now})
	if !summaryCarries(summary.Blocked, "MCP calls piece 7", "the development manager") {
		t.Errorf("blocked = %+v, want the stop waiting on the development manager", summary.Blocked)
	}
	if summaryCarries(summary.Decisions, "MCP calls piece 7", "") {
		t.Errorf("decisions = %+v, want the stop kept off what only the operator can do", summary.Decisions)
	}

	// Two such passes may be passing, and are not yet a problem.
	if found := repeatedStopsOf(read(stops[:3])); len(found) != 0 {
		t.Fatalf("after two stops, the attention line carries %+v, want nothing", found)
	}
	// A later pass that got past it clears it.
	cleared := append(append([]runstate.MergeQueuePass(nil), stops...), runstate.MergeQueuePass{At: now, Outcome: runstate.MergeQueuePassLanded, Says: "landed a change"})
	if found := repeatedStopsOf(read(cleared)); len(found) != 0 {
		t.Fatalf("after a pass that landed, the attention line carries %+v, want nothing", found)
	}
	// A stop for another reason is a new stop, counted from one.
	changed := append(append([]runstate.MergeQueuePass(nil), stops...), stoppedOn(head, now, "the target could not be read"))
	if found := repeatedStopsOf(read(changed)); len(found) != 0 {
		t.Fatalf("after a stop for another reason, the attention line carries %+v, want nothing", found)
	}
}

// A repeated stop whose pass recorded, from the error it stopped on, a
// credential only a person holds is the operator's, with that reason said, and
// is among what only he can do.
func TestARepeatedMergeQueueStopOnARefusedCredentialIsTheOperators(t *testing.T) {
	t.Parallel()
	first := time.Date(2026, 11, 12, 4, 17, 0, 0, time.UTC)
	_, head := repeatedStopQueues(nil)
	var history []runstate.MergeQueuePass
	for index := range 3 {
		pass := stoppedOn(head, first.Add(time.Duration(index)*time.Minute), "push the candidate: the forge refused the harness's token access")
		pass.PersonOnly = ownership.PersonCredential
		history = append(history, pass)
	}
	queues, _ := repeatedStopQueues(history)
	now := first.Add(3 * time.Minute)
	standing := ReadStanding(context.Background(), Sources{MergeQueues: queues, Now: func() time.Time { return now }})
	found := repeatedStopsOf(standing)
	if len(found) != 1 || !found[0].Mover.IsOperator() || found[0].Ownership.Reason != ownership.OperatorCredential {
		t.Fatalf("the attention line carries %+v, want the stop as the operator's for a credential", found)
	}
	if overdue := standing.MergeQueues[0].Entries[0].Overdue; overdue == nil || !overdue.Mover.IsOperator() || overdue.OwnerReason != ownership.OperatorCredential.Says() || strings.Contains(overdue.WaitingOn, "handing it back") {
		t.Errorf("the queue's own entry carries %+v, want the operator named on it for a credential", overdue)
	}
	if rendered := standing.RenderMergeQueues(); !strings.Contains(rendered, "PROBLEM, "+ownership.MoverOperator.Possessive()+" to move: ") {
		t.Errorf("rendered queue does not name the operator:\n%s", rendered)
	}
	if whose := found[0].Whose(); !strings.Contains(whose, ownership.OperatorCredential.Says()) {
		t.Errorf("whose = %q, want the reason said", whose)
	}
	summary := Summarize(SummaryInputs{Standing: &standing, Now: now})
	if !summaryCarries(summary.Decisions, "MCP calls piece 7", "you") {
		t.Errorf("decisions = %+v, want the stop among what only the operator can do", summary.Decisions)
	}
}

// summaryCarries reports an answer carrying a line about the title, waiting on
// waitingOn where that is not empty.
func summaryCarries(answer SummaryAnswer, title, waitingOn string) bool {
	for _, entry := range answer.Entries {
		if !strings.Contains(strings.Join(entry.Detail, "\n"), title) {
			continue
		}
		if waitingOn == "" || strings.HasPrefix(entry.WaitingOn, waitingOn) {
			return true
		}
	}
	return false
}
