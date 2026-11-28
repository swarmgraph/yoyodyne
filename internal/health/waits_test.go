package health

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// showingTracker is a tracker that also shows single items, as beads.Client
// does, by one id or several at once. With silent set it answers no show until
// the call's own bound ends it, as a store held by another command does.
type showingTracker struct {
	trackerOf
	shown  map[string]beads.WorkItem
	silent bool
}

func (t showingTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	items, err := t.ShowMany(ctx, []string{id})
	if err != nil {
		return beads.WorkItem{}, err
	}
	item, found := items[id]
	if !found {
		return beads.WorkItem{}, errors.New("no issue found: " + id)
	}
	return item, nil
}

func (t showingTracker) ShowMany(ctx context.Context, ids []string) (map[string]beads.WorkItem, error) {
	if t.silent {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	items := map[string]beads.WorkItem{}
	for _, id := range ids {
		if item, found := t.shown[id]; found {
			items[id] = item
		}
	}
	return items, nil
}

// waitDecisions is the triage record of one item: the development manager
// decided to wait on named work about its stopped run.
type waitDecisions map[string]runstate.TriageCounters

func (d waitDecisions) Counters(workItemID string) (runstate.TriageCounters, error) {
	return d[workItemID], nil
}

// The factory health check reads a stoppage the development manager decided
// to wait on named work for through the same tracker reading the other
// surfaces use: once that work has closed, it says so, when, and that her
// decision is owed; while it is open, the wait stands; and a tracker that does
// not answer within its time limit is said to have not answered, naming the
// work, with nothing about what was or was not connected to it. The
// yoyodyne-ifd.435.20 shape is the one tested.
func TestTheHealthCheckSaysWhetherTheWorkAStoppageWaitsOnHasClosed(t *testing.T) {
	t.Parallel()

	const item, waitsOn = "yoyodyne-ifd.435.20", "yoyodyne-ifd.435.20.1"
	closedAt := time.Date(2026, 10, 17, 18, 30, 0, 0, time.UTC)
	stoppedAt := observedAt.Add(-48 * time.Hour)
	stopped := run("run-a55ad7d7", item)
	stopped.Status, stopped.CompletedAt, stopped.UpdatedAt = runstate.StatusFailed, &stoppedAt, stoppedAt
	stopped.Branch, stopped.WorktreePath = "yoyodyne/"+item+"/a55ad7d7", "/state/worktrees/run-a55ad7d7"
	stopped.Blocker = "Yoyodyne stopped this item: a configured check still failed after every permitted attempt."
	decisions := waitDecisions{item: {WorkItemID: item, Decisions: []runstate.TriageDecision{
		{Decision: runstate.TriageDecisionWait, RunID: stopped.RunID, DecidedAt: stoppedAt.Add(time.Hour), WaitsOn: waitsOn},
	}}}
	listed := trackerOf{"blocked": {{ID: item, Title: "the loopback sandbox", Status: "blocked"}}}

	// The limit bounds every tracker command the reading makes, the listing that
	// finds the held item included, and the listing is answered on another
	// goroutine (readmodel's sharedTracker). A limit the listing can miss on a
	// loaded machine leaves the held item unlisted and the observation empty, so
	// a tracker that answers gets a limit nothing waits out, and the one that does
	// not answer gets one long enough for the listing beside it to finish.
	const answers, silentFor = time.Minute, 2 * time.Second
	needs := func(t *testing.T, tracker readmodel.Tracker, limit time.Duration) string {
		t.Helper()
		observer, _ := whole(t)
		services := observer.standing(context.Background()).Services
		sources := readmodel.Sources{Runs: fakeRuns{}, Stoppages: stoppedRuns{stopped}, Decisions: decisions, Tracker: tracker,
			TrackerTimeout: limit, Capacity: 2, Now: func() time.Time { return observedAt }}
		observer.Standing = func(ctx context.Context) readmodel.Standing {
			standing := readmodel.ReadStanding(ctx, sources)
			standing.Services = services
			return standing
		}
		observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)
		held := find(t, observation, "yoyodyne/run-a55ad7d7/held-after-stoppage")
		if strings.Contains(held.Needs, "wired to read the tracker") {
			t.Fatalf("held stoppage needs %q, want nothing said about wiring the tracker", held.Needs)
		}
		return held.Needs
	}

	t.Run("closed", func(t *testing.T) {
		t.Parallel()
		said := needs(t, showingTracker{trackerOf: listed, shown: map[string]beads.WorkItem{
			waitsOn: {ID: waitsOn, Status: "closed", ClosedAt: closedAt},
		}}, answers)
		for _, want := range []string{
			waitsOn + ", the work the development manager was waiting on, closed " + closedAt.Local().Format("2006-01-02 15:04 MST"),
			"that wait is over and the stoppage needs her decision",
		} {
			if !strings.Contains(said, want) {
				t.Fatalf("held stoppage needs %q, want it to say %q", said, want)
			}
		}
		if strings.Contains(said, "could not be read") || strings.Contains(said, "decided to wait on") {
			t.Fatalf("held stoppage needs %q, want it saying the wait is over rather than that it may stand", said)
		}
	})
	t.Run("open", func(t *testing.T) {
		t.Parallel()
		said := needs(t, showingTracker{trackerOf: listed, shown: map[string]beads.WorkItem{
			waitsOn: {ID: waitsOn, Status: "in_progress"},
		}}, answers)
		if !strings.Contains(said, "decided to wait on "+waitsOn) || !strings.Contains(said, "once it is closed") ||
			strings.Contains(said, "could not be read") {
			t.Fatalf("held stoppage needs %q, want the wait standing, as the other surfaces say it", said)
		}
	})
	t.Run("tracker does not answer", func(t *testing.T) {
		t.Parallel()
		said := needs(t, showingTracker{trackerOf: listed, silent: true}, silentFor)
		for _, want := range []string{"whether " + waitsOn + " is closed could not be read", "the tracker did not answer in time",
			"cannot say whether that wait still stands"} {
			if !strings.Contains(said, want) {
				t.Fatalf("held stoppage needs %q, want it to say %q", said, want)
			}
		}
	})
}
