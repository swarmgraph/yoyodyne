package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// cappedTracker runs the real tracker adapter against a listing process with
// bd's row defaults and stderr hints. The schedule harness supplies the store
// and runs, so no real tracker is opened by these tests.
type cappedTracker struct {
	harness   *scheduleHarness
	cutVerb   string
	cutStatus string
	byteCut   bool
}

func (r cappedTracker) Run(ctx context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	verb := command.Args[0]
	status := ""
	for _, arg := range command.Args {
		if strings.HasPrefix(arg, "--status=") {
			status = strings.TrimPrefix(arg, "--status=")
		}
	}
	var items []beads.WorkItem
	var err error
	switch verb {
	case "ready":
		items, err = r.harness.Ready(ctx)
	case "list":
		items, err = r.harness.List(ctx, status)
	default:
		return execution.ProcessResult{}, fmt.Errorf("unexpected tracker command: %v", command.Args)
	}
	if err != nil {
		return execution.ProcessResult{}, err
	}
	cap := 50
	if verb == "ready" {
		cap = 100
	}
	forced := verb == r.cutVerb && (verb == "ready" || status == r.cutStatus)
	if forced {
		cap = 2
	}
	whole := len(items)
	cut := (forced || !slices.Contains(command.Args, "--limit=0")) && whole > cap
	if cut {
		items = items[:cap]
	}
	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		rows = append(rows, map[string]any{"id": item.ID, "title": item.Title, "status": item.Status, "priority": item.Priority})
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return execution.ProcessResult{}, err
	}
	result := execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(encoded)}
	if cut {
		if r.byteCut {
			result.Stdout = strings.TrimSuffix(result.Stdout, "]") + `,{"id":"unfinished`
			result.OutputTruncation = "output cut at the bound"
		} else {
			result.Stderr = fmt.Sprintf("Showing %d of %d %s issues. Use --limit 0 for all.\n", len(items), whole, verb)
		}
	}
	return result, nil
}

func TestSchedulerReadsAndOffersMoreThanTheTrackerDefaultInTheSameOrder(t *testing.T) {
	t.Parallel()
	ids := make([]string, 120)
	for index := range ids {
		ids[index] = fmt.Sprintf("yoyodyne-%03d", index)
	}
	items := readyItems(ids...)
	// Equal-priority rows retain the tracker's order, even when it is the
	// reverse of their identifiers.
	slices.Reverse(items)
	slices.Reverse(ids)
	harness := newScheduleHarness(items...)
	tracker := beads.Client{Runner: cappedTracker{harness: harness}}
	schedule, err := (Scheduler{Open: func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Tracker = tracker
		return pull, err
	}}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule(): %v", err)
	}
	if len(schedule.Started) == 0 || !strings.Contains(schedule.Started[0].Reason, "one of the 120 the tracker reports as ready") {
		t.Fatalf("the first pull did not offer the whole ready list: %s", schedule.Render())
	}
	if got := harness.pullOrder(); !slices.Equal(got, ids) {
		t.Fatalf("offered and pulled %v, want all 120 in order: %s", got, schedule.Render())
	}
}

func TestCutTrackerListingsAreOnTheSchedulingRecordAndWaitingWork(t *testing.T) {
	for _, test := range []struct {
		verb     string
		status   string
		byteCut  bool
		watching bool
	}{
		{verb: "ready"},
		{verb: "list", status: "open"},
		{verb: "list", status: "blocked"},
		{verb: "list", status: "in_progress"},
		{verb: "ready", byteCut: true},
		{verb: "list", status: "open", byteCut: true},
		{verb: "ready", watching: true},
		{verb: "list", status: "open", watching: true},
	} {
		t.Run(fmt.Sprintf("%s-%s-bytes-%t-watch-%t", test.verb, test.status, test.byteCut, test.watching), func(t *testing.T) {
			t.Parallel()
			items := readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")
			if test.status != "" {
				for index := range items {
					items[index].Status = test.status
				}
			}
			harness := newScheduleHarness(items...)
			tracker := beads.Client{Runner: cappedTracker{harness: harness, cutVerb: test.verb, cutStatus: test.status, byteCut: test.byteCut}}
			harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 2 }
			sessions := &recordedSessions{}
			schedule, err := (Scheduler{Sessions: sessions, Watching: test.watching, Sleep: harness.sleep, Now: harness.clock, Open: func(ctx context.Context) (Pull, error) {
				pull, err := harness.open(ctx)
				pull.Tracker = tracker
				return pull, err
			}}).Schedule(context.Background())
			if len(schedule.Started) != 0 || (!test.watching && (err == nil || schedule.Stopped != ScheduleUnreadable)) || (test.watching && (err != nil || schedule.Stopped != ScheduleCancelled)) {
				t.Fatalf("cut list treated as whole: schedule=%+v, err=%v", schedule, err)
			}
			passRecord := schedule.ReadFailure
			if test.watching {
				passRecord = schedule.ReadProblem
				var said []string
				for _, transition := range sessions.recorded() {
					if transition.unreadable {
						said = append(said, transition.reason)
					}
				}
				for _, want := range []string{"cut", "read 2 complete work item(s)", "work may be missing"} {
					if !strings.Contains(strings.Join(said, "\n"), want) {
						t.Fatalf("watch record lacks %q: %v", want, said)
					}
				}
			}
			sources := readmodel.Sources{Tracker: tracker}
			standing := readmodel.ReadStanding(context.Background(), sources)
			_, _, queueErr := readmodel.Queue(context.Background(), sources)
			if queueErr == nil {
				t.Fatal("the queue listing accepted a cut list")
			}
			encoded, err := json.Marshal(standing)
			if err != nil {
				t.Fatal(err)
			}
			for _, evidence := range []string{passRecord, schedule.Render(), standing.NotStartableProblem, standing.Render(), standing.RenderBrief(), string(encoded), queueErr.Error()} {
				for _, want := range []string{"cut", "read 2 complete work item(s)", "work may be missing"} {
					if !strings.Contains(evidence, want) {
						t.Fatalf("record or waiting-work surface lacks %q: %s", want, evidence)
					}
				}
			}
			if len(standing.NotStartable) != 0 || len(standing.NotStartableGroups) != 0 {
				t.Fatalf("a cut list was used to explain individual waiting items: %+v", standing)
			}
		})
	}
}
