package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

var passesAt = time.Date(2026, 10, 6, 15, 40, 0, 0, time.UTC)

func answeredPass(task string, at time.Time, delivered ...runstate.DeliveredWork) runstate.Sweep {
	return runstate.Sweep{Task: task, Role: domain.RoleArchitect, StartedAt: at, Turns: 1,
		Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "done"}, Delivered: delivered}
}

func TestStatusNamesTheFirstItemAndWhetherTheLastPassTookIt(t *testing.T) {
	t.Parallel()
	recorded := []runstate.Sweep{
		answeredPass("architect-pass", passesAt,
			runstate.DeliveredWork{ID: "yoyodyne-ifd.414.1", Priority: 0, Taken: true}),
		answeredPass("architect-pass", passesAt.Add(45*time.Minute),
			runstate.DeliveredWork{ID: "yoyodyne-ifd.414.1", Priority: 0},
			runstate.DeliveredWork{ID: "yoyodyne-ab2", Priority: 0, Taken: true}),
		// A task whose passes never recorded what they were handed says nothing.
		{Task: "dm-sweep", Role: domain.RoleDevelopmentManager, StartedAt: passesAt, Turns: 1, Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "done"}},
	}
	passes := ConversationPasses(recorded)
	if len(passes) != 1 {
		t.Fatalf("passes = %+v, want the architect's task alone", passes)
	}
	said := passes[0].Says()
	for _, want := range []string{"architect-pass (architect)", localMoment(passesAt.Add(45 * time.Minute)), "was handed 2 items", "first in its order: yoyodyne-ifd.414.1 (P0), NOT TAKEN — no reason given", "it took 1 of the 2"} {
		if !strings.Contains(said, want) {
			t.Errorf("line = %q, want %q", said, want)
		}
	}
	if strings.Contains(said, "latest firing") {
		t.Errorf("line = %q, want nothing about a later firing", said)
	}

	standing := Standing{ConversationPasses: passes}
	if rendered := standing.RenderConversationPasses(); !strings.HasPrefix(rendered, "Recurring passes over conversation work (1):\n  architect-pass") {
		t.Errorf("rendered = %q", rendered)
	}
	if rendered := (Standing{}).RenderConversationPasses(); rendered != "" {
		t.Errorf("an empty standing renders %q, want nothing", rendered)
	}
}

func TestStatusSaysWhenTheLatestFiringDidNotRun(t *testing.T) {
	t.Parallel()
	recorded := []runstate.Sweep{
		answeredPass("architect-pass", passesAt,
			runstate.DeliveredWork{ID: "yoyodyne-ifd.414.1", Priority: 0, Reason: "waits on the secret store"}),
		{Task: "architect-pass", Role: domain.RoleArchitect, StartedAt: passesAt.Add(45 * time.Minute),
			Missed: &runstate.MissedPass{Trigger: runstate.PassTriggerSchedule, How: runstate.MissUnfired}, Problem: "no pass followed"},
	}
	passes := ConversationPasses(recorded)
	if len(passes) != 1 {
		t.Fatalf("passes = %+v", passes)
	}
	said := passes[0].Says()
	for _, want := range []string{"NOT TAKEN — waits on the secret store", "its latest firing, at " + localMoment(passesAt.Add(45*time.Minute)) + ", was missed"} {
		if !strings.Contains(said, want) {
			t.Errorf("line = %q, want %q", said, want)
		}
	}
}

func TestAnItemSaysWhenAPassLastConsideredIt(t *testing.T) {
	t.Parallel()
	recorded := []runstate.Sweep{
		answeredPass("architect-pass", passesAt.Add(45*time.Minute),
			runstate.DeliveredWork{ID: "yoyodyne-ab2", Priority: 0, Taken: true},
			runstate.DeliveredWork{ID: "yoyodyne-ifd.414.1", Priority: 0, Reason: "behind the addendum"}),
		answeredPass("architect-pass", passesAt,
			runstate.DeliveredWork{ID: "yoyodyne-ifd.414.1", Priority: 0, Taken: true}),
	}
	considered, found := LastConsidered(recorded, "yoyodyne-ifd.414.1")
	if !found || !considered.At.Equal(passesAt.Add(45*time.Minute)) || considered.Position != 2 || considered.Taken {
		t.Fatalf("LastConsidered = %+v, %v, want the later pass, second of two, not taken", considered, found)
	}
	said := considered.Sentence()
	for _, want := range []string{"architect's recurring pass architect-pass", "2nd of the 2 items it was handed", "did NOT take this item — behind the addendum"} {
		if !strings.Contains(said, want) {
			t.Errorf("sentence = %q, want %q", said, want)
		}
	}
	if _, found := LastConsidered(recorded, "yoyodyne-gtx"); found {
		t.Error("an item no pass was handed has no last consideration")
	}
}

// What a pass told it kept leaving an item did with it, and how many passes in
// a row left it, are said in capitals where they need noticing.
func TestStatusSaysAnItemDecidedAskedOrLeftInARow(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		item runstate.DeliveredWork
		want string
	}{
		{runstate.DeliveredWork{ID: "x-1", Taken: true, Settled: runstate.SettledDecided}, "taken: DECIDED, with the assumptions it made written on the item"},
		{runstate.DeliveredWork{ID: "x-1", Taken: true, Settled: runstate.SettledAsked}, "taken: ASKED one question about it"},
		{runstate.DeliveredWork{ID: "x-1", Reason: "spread over replies", LeftInARow: 2}, "NOT TAKEN — spread over replies (left 2 times in a row; the next pass is told to decide it or ask one question)"},
		{runstate.DeliveredWork{ID: "x-1", Reason: "spread over replies", LeftInARow: 3}, "(LEFT 3 TIMES IN A ROW, after being told to decide it or ask; the development manager is told)"},
	} {
		passes := ConversationPasses([]runstate.Sweep{answeredPass("architect-pass", passesAt, test.item)})
		if said := passes[0].Says(); !strings.Contains(said, test.want) {
			t.Errorf("line = %q, want %q", said, test.want)
		}
		considered, _ := LastConsidered([]runstate.Sweep{answeredPass("architect-pass", passesAt, test.item)}, "x-1")
		if sentence := considered.Sentence(); test.item.LeftInARow > 1 && !strings.Contains(strings.ToLower(sentence), "in a row") {
			t.Errorf("sentence = %q, want the count", sentence)
		}
	}
}

// Passes that left an item for the reason its note already gives wrote no note
// of their own, and every surface that shows the note says how many left it
// again since and when the latest did, in local time.
func TestTheRepeatsOfAPassedOverNoteAreCountedWhereverItIsShown(t *testing.T) {
	t.Parallel()
	left := runstate.DeliveredWork{ID: "x-1", Reason: "spread over replies"}
	repeat := left
	repeat.NoteRepeated = true
	recorded := []runstate.Sweep{
		// An earlier note for the same reason, before a pass that took the item,
		// is not the note standing now.
		answeredPass("architect-pass", passesAt, left),
		answeredPass("architect-pass", passesAt.Add(time.Hour), repeat),
		answeredPass("architect-pass", passesAt.Add(2*time.Hour), runstate.DeliveredWork{ID: "x-1", Taken: true}),
		answeredPass("architect-pass", passesAt.Add(3*time.Hour), left),
		answeredPass("architect-pass", passesAt.Add(4*time.Hour), repeat),
		// A pass that gave no account neither breaks the run nor adds to it.
		{Task: "architect-pass", Role: domain.RoleArchitect, StartedAt: passesAt.Add(5 * time.Hour), Turns: 1, Failed: true},
		answeredPass("architect-pass", passesAt.Add(6*time.Hour), repeat),
		answeredPass("architect-pass", passesAt.Add(7*time.Hour), repeat),
	}
	want := "(the note written on the item at " + localMoment(passesAt.Add(3*time.Hour)) +
		" gives this reason; left again 3 times since, most recently at " + localMoment(passesAt.Add(7*time.Hour)) + ")"

	passes := ConversationPasses(recorded)
	if len(passes) != 1 || passes[0].LeftAgain == nil || passes[0].LeftAgain.Times != 3 {
		t.Fatalf("passes = %+v, want the task's repeats counted", passes)
	}
	if said := passes[0].Says(); !strings.Contains(said, "NOT TAKEN — spread over replies "+want) {
		t.Errorf("status line = %q, want %q", said, want)
	}
	considered, problem := ReadLastConsidered(fakeSweeps{recorded: recorded}, "x-1")
	if problem != "" || considered == nil || considered.LeftAgain == nil {
		t.Fatalf("considered = %+v (%s), want the repeats on the item's last consideration", considered, problem)
	}
	if !strings.Contains(considered.Says, "did NOT take this item — spread over replies "+want) {
		t.Errorf("item line = %q, want %q", considered.Says, want)
	}

	// A pass that wrote its own note says nothing of repeats.
	written := ConversationPasses(recorded[:4])
	if written[0].LeftAgain != nil || strings.Contains(written[0].Says(), "left again") {
		t.Errorf("status line = %q, want no repeats after a note just written", written[0].Says())
	}
}
