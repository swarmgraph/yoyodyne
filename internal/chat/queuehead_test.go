package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeQueueHeads is the harness's hand for a release she decides: every release
// it was asked for, and what it answers.
type fakeQueueHeads struct {
	releases []QueueHeadRelease
	err      error
}

func (f *fakeQueueHeads) ReleaseHead(_ context.Context, release QueueHeadRelease) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.releases = append(f.releases, release)
	return "released entry 49 from the head of the merge queue for main as landed", nil
}

func queueHeadOptions(t *testing.T, heads QueueHeads, action string) Options {
	t.Helper()
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.429.148": {ID: "yoyodyne-ifd.429.148", Title: "the earlier queue fix", Status: "closed"},
	}}
	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	options := triageOptions(t, tracker, budgets, trackerReply("Releasing the head of the queue.", action))
	options.Reports = &fakeReports{}
	if heads != nil {
		options.QueueHeads = heads
	}
	return options
}

const queueLandedAction = `{"action":"queue","id":"yoyodyne-ifd.429.148","entry":"mqe-49","decision":"landed","reason":"pull request 1727 was merged by hand, so its commit is already on main and the queue has nothing to build"}`

// She releases the head of the queue as landed, and the release reaches the
// queue in her name, with her reason, for the item she named.
func TestTheDevelopmentManagerReleasesTheHeadOfTheMergeQueue(t *testing.T) {
	t.Parallel()

	heads := &fakeQueueHeads{}
	reply := triageSend(t, queueHeadOptions(t, heads, queueLandedAction))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the release applied", reply.Actions)
	}
	if len(heads.releases) != 1 {
		t.Fatalf("releases = %#v, want one", heads.releases)
	}
	release := heads.releases[0]
	if release.EntryID != "mqe-49" || release.WorkItemID != "yoyodyne-ifd.429.148" || !release.Landed ||
		!strings.Contains(release.Reason, "merged by hand") {
		t.Fatalf("release = %#v, want the entry, the item, landed, and her reason", release)
	}
	if !strings.HasPrefix(release.By, "the development manager in conversation ") {
		t.Fatalf("released by %q, want her named from the conversation's own facts", release.By)
	}
	if !strings.Contains(reply.Actions[0].Summary, "released entry 49") {
		t.Fatalf("summary = %q, want what the queue said it did", reply.Actions[0].Summary)
	}
}

// "return" hands the entry back rather than recording it as landed.
func TestAReleaseThatReturnsTheEntryIsNotRecordedAsLanded(t *testing.T) {
	t.Parallel()

	heads := &fakeQueueHeads{}
	action := strings.Replace(queueLandedAction, `"decision":"landed"`, `"decision":"return"`, 1)
	reply := triageSend(t, queueHeadOptions(t, heads, action))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(heads.releases) != 1 || heads.releases[0].Landed {
		t.Fatalf("actions = %#v, releases %#v; want one release that hands the entry back", reply.Actions, heads.releases)
	}
}

// A release is refused whole where it names no entry or an unknown decision,
// where nothing can reach the queue, and where the queue refuses it, and in
// each case nothing is said to have been done.
func TestAReleaseIsRefusedWhereItCannotBeCarriedOut(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		heads  QueueHeads
		action string
		want   string
	}{
		"no entry":      {heads: &fakeQueueHeads{}, action: strings.Replace(queueLandedAction, `"entry":"mqe-49",`, "", 1), want: `requires "entry"`},
		"bad decision":  {heads: &fakeQueueHeads{}, action: strings.Replace(queueLandedAction, `"decision":"landed"`, `"decision":"skip"`, 1), want: "not a decision"},
		"no queue":      {heads: nil, action: queueLandedAction, want: "cannot reach the merge queue"},
		"queue refuses": {heads: &fakeQueueHeads{err: errors.New("main does not hold its approved commit")}, action: queueLandedAction, want: "does not hold"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			options := queueHeadOptions(t, test.heads, test.action)
			reply, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
			said := refusalOf(reply, err) + renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
			if !strings.Contains(said, test.want) {
				t.Fatalf("Send() said %q, want it to contain %q", said, test.want)
			}
			for _, outcome := range reply.Actions {
				if outcome.Applied {
					t.Fatalf("actions = %#v, want the release refused", reply.Actions)
				}
			}
		})
	}
}
