package runstate

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func teamMergeWait(since time.Time) TeamMerge {
	return TeamMerge{TargetBranch: "staging", UpstreamBranch: "main", LandingCommit: strings.Repeat("b", 40), Since: since}
}

// waitingForTeamRun is a succeeded, integrated run waiting for the team's merge.
func waitingForTeamRun(t *testing.T) State {
	t.Helper()
	state := testState(t, StatusSucceeded)
	state.WorktreePath = "/state/worktree"
	state.Branch = "yoyodyne/task/01234567"
	state.BaseCommit = strings.Repeat("a", 40)
	state.TargetBranch = "staging"
	state.UpstreamBranch = "main"
	state.Phase = PhaseIntegrating
	state.ProviderSessionID = "developer-session"
	state.ProviderModel = "opus"
	state.ReviewSessionID = "reviewer-session"
	state.ReviewModel = "opus"
	state.ReviewDecision = ReviewApprove
	state.Integration = &Integration{
		TargetBranch:         "staging",
		SourceCommit:         strings.Repeat("b", 40),
		TargetCommit:         strings.Repeat("b", 40),
		PreviousTargetCommit: strings.Repeat("a", 40),
	}
	wait := teamMergeWait(state.StartedAt)
	state.TeamMerge = &wait
	return state
}

func TestTeamMergePredicateAndValidate(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)
	later := since.Add(time.Hour)
	wait := teamMergeWait(since)
	if err := wait.Validate(); err != nil || !wait.Waiting() || wait.Found() {
		t.Fatalf("wait = %v, waiting %t, want a valid open wait", err, wait.Waiting())
	}
	if got, want := wait.Says(), "landed on staging, waiting for the team to merge it into main"; got != want {
		t.Errorf("Says() = %q, want %q", got, want)
	}
	found := wait
	found.FoundAt, found.FoundOn = &later, strings.Repeat("c", 40)
	if err := found.Validate(); err != nil || !found.Found() || found.Waiting() {
		t.Fatalf("found = %v, want a valid ended wait", err)
	}
	for name, mutate := range map[string]func(*TeamMerge){
		"no target":      func(w *TeamMerge) { w.TargetBranch = "" },
		"no upstream":    func(w *TeamMerge) { w.UpstreamBranch = "" },
		"bad landing":    func(w *TeamMerge) { w.LandingCommit = "nope" },
		"no since":       func(w *TeamMerge) { w.Since = time.Time{} },
		"bad found_on":   func(w *TeamMerge) { w.FoundAt, w.FoundOn = &later, "nope" },
		"found_on alone": func(w *TeamMerge) { w.FoundOn = strings.Repeat("c", 40) },
		"found_at alone": func(w *TeamMerge) { w.FoundAt = &later },
	} {
		invalid := wait
		mutate(&invalid)
		if invalid.Validate() == nil {
			t.Errorf("%s: Validate() = nil, want it refused", name)
		}
	}

	run := waitingForTeamRun(t)
	if !run.WaitingForTeamMerge() {
		t.Fatal("a succeeded integrated run with an open wait does not read as waiting")
	}
	for name, mutate := range map[string]func(*State){
		"found":          func(s *State) { s.TeamMerge = &found },
		"retired":        func(s *State) { s.Retirement = &RunRetirement{} },
		"not succeeded":  func(s *State) { s.Status = StatusRunning },
		"no integration": func(s *State) { s.Integration = nil },
		"no wait":        func(s *State) { s.TeamMerge = nil },
	} {
		other := run
		mutate(&other)
		if other.WaitingForTeamMerge() {
			t.Errorf("%s: still reads as waiting", name)
		}
	}
}

func TestTeamMergeFoundRecordsThroughTheStoreAndIsListed(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	run := waitingForTeamRun(t)
	if err := store.Create(run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	loaded := load(t, store, run.RunID)
	if !reflect.DeepEqual(loaded.TeamMerge, run.TeamMerge) || loaded.UpstreamBranch != "main" {
		t.Fatalf("round trip lost the wait: %+v upstream %q", loaded.TeamMerge, loaded.UpstreamBranch)
	}
	waiting, err := store.WaitingForTeamMerge()
	if err != nil || len(waiting) != 1 || waiting[0].RunID != run.RunID {
		t.Fatalf("WaitingForTeamMerge() = %v, %v, want the run", waiting, err)
	}

	ctx := context.Background()
	if _, err := store.Update(ctx, run.RunID, TeamMergeFound{LandingCommit: strings.Repeat("d", 40), FoundOn: strings.Repeat("c", 40), At: updateAt}); err == nil {
		t.Fatal("a mismatched landing commit was recorded")
	}
	tip := strings.Repeat("c", 40)
	written, err := store.Update(ctx, run.RunID, TeamMergeFound{LandingCommit: run.TeamMerge.LandingCommit, FoundOn: tip, At: updateAt})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if written.TeamMerge.FoundAt == nil || !written.TeamMerge.FoundAt.Equal(updateAt) || written.TeamMerge.FoundOn != tip || written.WaitingForTeamMerge() {
		t.Fatalf("Update() wrote %+v", written.TeamMerge)
	}
	if _, err := store.Update(ctx, run.RunID, TeamMergeFound{LandingCommit: run.TeamMerge.LandingCommit, FoundOn: tip, At: updateAt}); err == nil {
		t.Fatal("finding was recorded twice")
	}
	waiting, err = store.WaitingForTeamMerge()
	if err != nil || len(waiting) != 0 {
		t.Fatalf("WaitingForTeamMerge() after found = %v, %v, want none", waiting, err)
	}
}

func TestWaitingForTeamMergeOfKeepsOnePerItemOldestFirst(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	mk := func(id, run, title string, since time.Time) State {
		state := waitingForTeamRun(t)
		state.WorkItemID, state.RunID, state.WorkItemTitle = id, run, title
		wait := teamMergeWait(since)
		state.TeamMerge = &wait
		return state
	}
	ended := mk("yoyodyne-z", "r-z", "", base)
	done := base
	ended.TeamMerge.FoundAt, ended.TeamMerge.FoundOn = &done, strings.Repeat("c", 40)
	items := WaitingForTeamMergeOf([]State{
		mk("yoyodyne-b", "r1", "Beta", base.Add(time.Hour)),
		mk("yoyodyne-b", "r2", "Beta", base.Add(2*time.Hour)),
		mk("yoyodyne-a", "r3", "", base.Add(time.Hour)),
		mk("yoyodyne-c", "r4", "Gamma", base),
		ended,
	})
	var ids, runs []string
	for _, item := range items {
		ids = append(ids, item.WorkItemID)
		runs = append(runs, item.RunID)
	}
	if got, want := strings.Join(ids, ","), "yoyodyne-c,yoyodyne-a,yoyodyne-b"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if runs[2] != "r2" {
		t.Errorf("the latest wait did not win: %v", runs)
	}
	if got, want := items[0].Says(), "Gamma (yoyodyne-c): landed on staging as bbbbbbbbbbbb, waiting for the team to merge it into main; no pull request carries it yet"; got != want {
		t.Errorf("Says() = %q, want %q", got, want)
	}
	if got, want := items[1].Says(), "yoyodyne-a: landed on staging as bbbbbbbbbbbb, waiting for the team to merge it into main; no pull request carries it yet"; got != want {
		t.Errorf("Says() = %q, want %q", got, want)
	}
}
