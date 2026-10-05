package readmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeItemTracker answers Show from what the test put in it, and records the
// deadline it was asked under.
type fakeItemTracker struct {
	items    map[string]beads.WorkItem
	fail     error
	deadline bool
}

func (f *fakeItemTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	_, f.deadline = ctx.Deadline()
	if f.fail != nil {
		return beads.WorkItem{}, f.fail
	}
	item, found := f.items[id]
	if !found {
		return beads.WorkItem{}, fmt.Errorf("%w: bd show failed: issue not found", beads.ErrNoSuchWorkItem)
	}
	return item, nil
}

// fakeHistories answers History with the summaries the test put in it, and
// records the query, so a test can hold the reading to asking for one item's
// latest run and no more.
type fakeHistories struct {
	runs  []runstate.RunSummary
	fail  error
	asked []runstate.RunQuery
}

func (f *fakeHistories) History(query runstate.RunQuery) (runstate.RunHistory, error) {
	f.asked = append(f.asked, query)
	if f.fail != nil {
		return runstate.RunHistory{}, f.fail
	}
	runs := f.runs
	if query.Limit > 0 && len(runs) > query.Limit {
		runs = runs[:query.Limit]
	}
	return runstate.RunHistory{Runs: runs, Matched: len(f.runs), Recorded: len(f.runs)}, nil
}

func fullItem() beads.WorkItem {
	return beads.WorkItem{
		ID:                 "yoyodyne-ifd.432.1",
		Title:              "Work item details are viewable from the dashboard",
		Description:        "A pop-up card per item.",
		Design:             "The card reads the shared projection.",
		AcceptanceCriteria: "Every field has a plain label.",
		Notes:              "Goal served: the surfaces read clearly.",
		Status:             "in_progress",
		Priority:           2,
		Parent:             "yoyodyne-ifd.432",
		Labels:             []string{"dashboard", "feature"},
		RelevantGoals:      []string{"Keep the work traceable.", "Use ordinary words."},
	}
}

// The item is the tracker's own fields under their own names, every one of them
// present, and the run beside it is the latest run as the run history
// summarizes it: in flight here, with how long it has been going.
func TestReadWorkItemCarriesEveryFieldAndTheRunInFlight(t *testing.T) {
	t.Parallel()
	tracker := &fakeItemTracker{items: map[string]beads.WorkItem{"yoyodyne-ifd.432.1": fullItem()}}
	histories := &fakeHistories{runs: []runstate.RunSummary{{
		RunID: "run-new", WorkItemID: "yoyodyne-ifd.432.1", Status: runstate.StatusRunning, Outcome: runstate.RunOutcome(runstate.StatusRunning),
		Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-12 * time.Minute), Branch: "yoyodyne/yoyodyne-ifd-432-1/abc", WorktreePath: "/tmp/wt", CostUSD: 3.41,
	}, {
		RunID: "run-old", WorkItemID: "yoyodyne-ifd.432.1", Status: runstate.StatusFailed, Outcome: runstate.OutcomeFailed, StartedAt: moment.Add(-time.Hour),
	}}}
	item, err := ReadWorkItem(context.Background(), WorkItemSources{Tracker: tracker, Runs: histories, TrackerTimeout: time.Second, Now: func() time.Time { return moment }}, "yoyodyne-ifd.432.1")
	if err != nil {
		t.Fatalf("ReadWorkItem: %v", err)
	}
	want := fullItem()
	if item.ID != want.ID || item.Title != want.Title || item.Status != want.Status || item.Priority != want.Priority || item.Parent != want.Parent ||
		item.Description != want.Description || item.Design != want.Design || item.AcceptanceCriteria != want.AcceptanceCriteria || item.Notes != want.Notes ||
		strings.Join(item.RelevantGoals, ";") != strings.Join(want.RelevantGoals, ";") ||
		strings.Join(item.Labels, ",") != "dashboard,feature" || !item.ObservedAt.Equal(moment) {
		t.Fatalf("item = %+v, want every field of %+v", item, want)
	}
	if !tracker.deadline {
		t.Fatal("the tracker was asked with no deadline")
	}
	if len(histories.asked) != 1 || histories.asked[0].WorkItemID != "yoyodyne-ifd.432.1" || histories.asked[0].Limit != 1 || histories.asked[0].FailedOnly {
		t.Fatalf("the histories were asked %+v, want one item's latest run", histories.asked)
	}
	run := item.Run
	if run == nil || item.RunProblem != "" {
		t.Fatalf("run = %+v, problem %q", run, item.RunProblem)
	}
	if run.RunID != "run-new" || !run.InFlight || run.Preserved || run.Remains != "" || run.Elapsed != 12*time.Minute || run.Phase != runstate.PhaseDeveloping || run.CostUSD != 3.41 || run.Branch == "" {
		t.Fatalf("the run in flight reads as %+v", run)
	}
}

// A run that ended is said in the run history's words: its outcome, and what
// remains of its change, so a preserved change is the same "work preserved"
// `yoyo status` prints; and a run that ended with nothing preserved is still
// carried, saying so, rather than the card showing no run at all.
func TestReadWorkItemSaysWhatTheLatestRunCameTo(t *testing.T) {
	t.Parallel()
	tracker := &fakeItemTracker{items: map[string]beads.WorkItem{"yoyodyne-ifd.1": {ID: "yoyodyne-ifd.1", Title: "t"}}}
	completed := moment.Add(-time.Hour)
	preserved := &fakeHistories{runs: []runstate.RunSummary{{
		RunID: "run-stopped", Status: runstate.StatusFailed, Outcome: runstate.OutcomeStopped, Phase: runstate.PhaseReviewing,
		StartedAt: moment.Add(-2 * time.Hour), CompletedAt: &completed, Branch: "yoyodyne/x", WorktreePath: "/tmp/x", ProviderSessionID: "sess-1",
		Failure: "the reviewer asked for repair", StopClass: runstate.StopReview, UnknownCost: "its event log is gone",
	}}}
	item, err := ReadWorkItem(context.Background(), WorkItemSources{Tracker: tracker, Runs: preserved, Now: func() time.Time { return moment }}, "yoyodyne-ifd.1")
	if err != nil {
		t.Fatal(err)
	}
	run := item.Run
	if run == nil || run.InFlight || !run.Preserved || run.Remains != "work preserved" || run.Outcome != runstate.OutcomeStopped || run.Failure != "the reviewer asked for repair" ||
		run.UnknownCost != "its event log is gone" || run.CompletedAt == nil || run.Elapsed != 0 || run.ProviderSessionID != "sess-1" {
		t.Fatalf("a stopped run with its change preserved reads as %+v", run)
	}
	// The reason is the one `yoyo status` prints: the gate that stopped the run
	// first, then the run's own words.
	if run.StopClass != runstate.StopReview || run.Reason != "review: the reviewer asked for repair" {
		t.Fatalf("the stopped run's reason reads as %q under class %q", run.Reason, run.StopClass)
	}

	removed := &fakeHistories{runs: []runstate.RunSummary{{
		RunID: "run-landed", Status: runstate.StatusSucceeded, Outcome: runstate.OutcomeSucceeded, Branch: "yoyodyne/y", BranchRemoved: true, WorktreePath: "/tmp/y", WorktreeRemoved: true, Integrated: true,
	}}}
	item, err = ReadWorkItem(context.Background(), WorkItemSources{Tracker: tracker, Runs: removed}, "yoyodyne-ifd.1")
	if err != nil {
		t.Fatal(err)
	}
	if item.Run == nil || item.Run.InFlight || item.Run.Preserved || item.Run.Remains != "work removed" || item.Run.Outcome != runstate.OutcomeSucceeded {
		t.Fatalf("a landed run reads as %+v", item.Run)
	}

	never := &fakeHistories{}
	item, err = ReadWorkItem(context.Background(), WorkItemSources{Tracker: tracker, Runs: never}, "yoyodyne-ifd.1")
	if err != nil || item.Run != nil || item.RunProblem != "" {
		t.Fatalf("an item never run reads as run %+v, problem %q, err %v", item.Run, item.RunProblem, err)
	}
}

// The tracker's answer and the run records' are refused apart. An id the
// tracker holds nothing under is ErrNoSuchWorkItem, a tracker that could not be
// read is any other error, and the run records failing costs the run and not
// the item — said in the answer, never as an item nothing ever touched.
func TestReadWorkItemRefusesWhatItCannotReadAndSaysWhatItCould(t *testing.T) {
	t.Parallel()
	tracker := &fakeItemTracker{items: map[string]beads.WorkItem{"yoyodyne-ifd.1": {ID: "yoyodyne-ifd.1", Title: "t"}}}

	if _, err := ReadWorkItem(context.Background(), WorkItemSources{Tracker: tracker, Runs: &fakeHistories{}}, "yoyodyne-ifd.9"); !errors.Is(err, ErrNoSuchWorkItem) {
		t.Fatalf("a missing item = %v, want ErrNoSuchWorkItem", err)
	}
	down := &fakeItemTracker{fail: errors.New("bd show failed: LOCK: operation not permitted")}
	if _, err := ReadWorkItem(context.Background(), WorkItemSources{Tracker: down, Runs: &fakeHistories{}}, "yoyodyne-ifd.1"); err == nil || errors.Is(err, ErrNoSuchWorkItem) || !strings.Contains(err.Error(), "operation not permitted") {
		t.Fatalf("an unreadable tracker = %v, want its own failure", err)
	}
	if _, err := ReadWorkItem(context.Background(), WorkItemSources{}, "yoyodyne-ifd.1"); err == nil || !strings.Contains(err.Error(), "nothing was wired") {
		t.Fatalf("no tracker = %v, want a stated wiring gap", err)
	}

	item, err := ReadWorkItem(context.Background(), WorkItemSources{Tracker: tracker, Runs: &fakeHistories{fail: errors.New("scan recorded: input/output error")}}, "yoyodyne-ifd.1")
	if err != nil || item.Run != nil || !strings.Contains(item.RunProblem, "input/output error") {
		t.Fatalf("unreadable runs = %+v / %q / %v, want the item with the run's problem stated", item.Run, item.RunProblem, err)
	}
	item, err = ReadWorkItem(context.Background(), WorkItemSources{Tracker: tracker}, "yoyodyne-ifd.1")
	if err != nil || item.Run != nil || !strings.Contains(item.RunProblem, "nothing was wired") {
		t.Fatalf("no runs wired = %+v / %q / %v", item.Run, item.RunProblem, err)
	}
	// A run store that could not be opened is said as that, with the reason,
	// rather than as a wiring gap.
	item, err = ReadWorkItem(context.Background(), WorkItemSources{Tracker: tracker, RunsProblem: "the state root could not be resolved: $HOME is not set"}, "yoyodyne-ifd.1")
	if err != nil || item.Run != nil || item.RunProblem != "the runs could not be opened: the state root could not be resolved: $HOME is not set" || strings.Contains(item.RunProblem, "wired") {
		t.Fatalf("an unopened run store = %+v / %q / %v", item.Run, item.RunProblem, err)
	}
	// An item carrying no labels still answers with a list, so a card shows a
	// plain "none" rather than wondering whether the field was read.
	if item.Labels == nil {
		t.Fatal("an item with no labels carries no list")
	}
}
