package runstate

import (
	"context"
	"strings"
	"testing"
	"time"
)

var trackedAt = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func trackedRequest(number int) TeamPullRequest {
	return TeamPullRequest{Number: number, URL: "https://example.invalid/pr", TrackedAt: trackedAt}
}

func TestTeamPullRequestValidate(t *testing.T) {
	t.Parallel()
	if err := trackedRequest(4).Validate(); err != nil {
		t.Fatalf("Validate() = %v, want valid", err)
	}
	for name, mutate := range map[string]func(*TeamPullRequest){
		"no number":         func(p *TeamPullRequest) { p.Number = 0 },
		"no time":           func(p *TeamPullRequest) { p.TrackedAt = time.Time{} },
		"unverified, found": func(p *TeamPullRequest) { p.Unverified, p.Found = true, true },
	} {
		p := trackedRequest(4)
		mutate(&p)
		if p.Validate() == nil {
			t.Errorf("%s: Validate() = nil, want it refused", name)
		}
	}
}

func TestTeamMergeValidatesPullRequestAndReturn(t *testing.T) {
	t.Parallel()
	wait := teamMergeWait(trackedAt)
	pr := trackedRequest(4)
	wait.PullRequest = &pr
	if err := wait.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	later := trackedAt.Add(time.Hour)
	returned := TeamMergeReturned{Number: 4, ClosedBy: "ada", ClosedAt: later, At: later}
	for name, mutate := range map[string]func(*TeamMerge){
		"bad pull request":   func(w *TeamMerge) { bad := trackedRequest(0); w.PullRequest = &bad },
		"found_by, no found": func(w *TeamMerge) { w.FoundBy = 4 },
		"returned no number": func(w *TeamMerge) { r := returned; r.Number = 0; w.Returned = &r },
		"returned no closed": func(w *TeamMerge) { r := returned; r.ClosedAt = time.Time{}; w.Returned = &r },
		"returned no at":     func(w *TeamMerge) { r := returned; r.At = time.Time{}; w.Returned = &r },
		"returned and found": func(w *TeamMerge) { w.Returned = &returned; w.FoundAt, w.FoundOn = &later, strings.Repeat("c", 40) },
	} {
		invalid := wait
		mutate(&invalid)
		if invalid.Validate() == nil {
			t.Errorf("%s: Validate() = nil, want it refused", name)
		}
	}
	wait.Returned = &returned
	if err := wait.Validate(); err != nil || wait.Waiting() || wait.Found() {
		t.Fatalf("returned wait = %v, waiting %t, want valid and not waiting", err, wait.Waiting())
	}
}

func TestTeamPullRequestUpdatesThroughTheStore(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	run := waitingForTeamRun(t)
	if err := store.Create(run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	ctx := context.Background()
	landing := run.TeamMerge.LandingCommit
	at := updateAt

	if _, err := store.Update(ctx, run.RunID, TeamPullRequestTracked{LandingCommit: strings.Repeat("d", 40), PullRequest: trackedRequest(4), At: at}); err == nil {
		t.Fatal("a mismatched landing commit was tracked")
	}
	if _, err := store.Update(ctx, run.RunID, TeamPullRequestTracked{LandingCommit: landing, PullRequest: TeamPullRequest{Number: 4}, At: at}); err == nil {
		t.Fatal("an invalid pull request was tracked")
	}
	written, err := store.Update(ctx, run.RunID, TeamPullRequestTracked{LandingCommit: landing, PullRequest: trackedRequest(4), At: at})
	if err != nil || written.TeamMerge.PullRequest == nil || written.TeamMerge.PullRequest.Number != 4 {
		t.Fatalf("track = %+v, %v", written.TeamMerge, err)
	}
	if load(t, store, run.RunID).TeamMerge.PullRequest.Number != 4 {
		t.Fatal("the tracked pull request did not round trip")
	}
	// The same request again changes nothing.
	again := trackedRequest(4)
	again.TrackedAt = trackedAt.Add(time.Hour)
	if changed, err := (TeamPullRequestTracked{LandingCommit: landing, PullRequest: again, At: at}).apply(&written); err != nil || changed {
		t.Fatalf("tracking the same request again = %t, %v, want no change", changed, err)
	}
	// An unverified record of the same number does change it.
	unverified := trackedRequest(4)
	unverified.Unverified = true
	if changed, err := (TeamPullRequestTracked{LandingCommit: landing, PullRequest: unverified, At: at}).apply(&written); err != nil || !changed || !written.TeamMerge.PullRequest.Unverified {
		t.Fatalf("tracking unverified = %t, %v", changed, err)
	}
	// Moving to another request replaces it.
	written, err = store.Update(ctx, run.RunID, TeamPullRequestTracked{LandingCommit: landing, PullRequest: trackedRequest(5), At: at})
	if err != nil || written.TeamMerge.PullRequest.Number != 5 {
		t.Fatalf("move = %+v, %v", written.TeamMerge, err)
	}

	// Untracking a different number changes nothing; the right one clears it.
	written, err = store.Update(ctx, run.RunID, TeamPullRequestUntracked{Number: 4, At: at})
	if err != nil || written.TeamMerge.PullRequest == nil {
		t.Fatalf("untrack other = %+v, %v", written.TeamMerge, err)
	}
	written, err = store.Update(ctx, run.RunID, TeamPullRequestUntracked{Number: 5, At: at})
	if err != nil || written.TeamMerge.PullRequest != nil {
		t.Fatalf("untrack = %+v, %v", written.TeamMerge, err)
	}

	// Closing and finding need a tracked request carrying that number.
	if _, err := store.Update(ctx, run.RunID, TeamPullRequestClosed{LandingCommit: landing, Number: 5, ClosedAt: at, At: at}); err == nil {
		t.Fatal("a closing was recorded with no pull request tracked")
	}
	if _, err := store.Update(ctx, run.RunID, TeamMergeFound{LandingCommit: landing, FoundOn: strings.Repeat("c", 40), PullRequest: 5, At: at}); err == nil {
		t.Fatal("a merge was recorded with no pull request tracked")
	}
	if _, err := store.Update(ctx, run.RunID, TeamPullRequestTracked{LandingCommit: landing, PullRequest: trackedRequest(6), At: at}); err != nil {
		t.Fatalf("track 6: %v", err)
	}
	if _, err := store.Update(ctx, run.RunID, TeamPullRequestClosed{LandingCommit: landing, Number: 5, ClosedAt: at, At: at}); err == nil {
		t.Fatal("a closing of another number was recorded")
	}
	if _, err := store.Update(ctx, run.RunID, TeamMergeFound{LandingCommit: landing, FoundOn: strings.Repeat("c", 40), PullRequest: 5, At: at}); err == nil {
		t.Fatal("a merge of another number was recorded")
	}
	closed, err := store.Update(ctx, run.RunID, TeamPullRequestClosed{LandingCommit: landing, Number: 6, ClosedBy: "ada", ClosedAt: at, At: at})
	if err != nil || closed.TeamMerge.Returned == nil || closed.TeamMerge.Returned.ClosedBy != "ada" || closed.WaitingForTeamMerge() || closed.TeamMerge.Waiting() {
		t.Fatalf("close = %+v, %v", closed.TeamMerge, err)
	}
	waiting, err := store.WaitingForTeamMerge()
	if err != nil || len(waiting) != 0 {
		t.Fatalf("WaitingForTeamMerge() after return = %v, %v, want none", waiting, err)
	}
	for name, update := range map[string]RunUpdate{
		"track": TeamPullRequestTracked{LandingCommit: landing, PullRequest: trackedRequest(7), At: at},
		"close": TeamPullRequestClosed{LandingCommit: landing, Number: 6, ClosedAt: at, At: at},
		"found": TeamMergeFound{LandingCommit: landing, FoundOn: strings.Repeat("c", 40), At: at},
	} {
		if _, err := store.Update(ctx, run.RunID, update); err == nil {
			t.Errorf("%s was recorded on a returned item", name)
		}
	}
}

func TestTeamMergeFoundByATrackedPullRequest(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	run := waitingForTeamRun(t)
	if err := store.Create(run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	ctx := context.Background()
	landing := run.TeamMerge.LandingCommit
	if _, err := store.Update(ctx, run.RunID, TeamPullRequestTracked{LandingCommit: landing, PullRequest: trackedRequest(4), At: updateAt}); err != nil {
		t.Fatalf("track: %v", err)
	}
	merge := strings.Repeat("e", 40)
	written, err := store.Update(ctx, run.RunID, TeamMergeFound{LandingCommit: landing, FoundOn: merge, PullRequest: 4, At: updateAt})
	if err != nil || written.TeamMerge.FoundBy != 4 || written.TeamMerge.FoundOn != merge || written.TeamMerge.PullRequest.Number != 4 {
		t.Fatalf("found = %+v, %v", written.TeamMerge, err)
	}
	if got := load(t, store, run.RunID).TeamMerge; got.FoundBy != 4 || !got.Found() {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestWaitingForTeamMergeItemSaysWhichPullRequestCarriesIt(t *testing.T) {
	t.Parallel()
	state := waitingForTeamRun(t)
	state.WorkItemID, state.WorkItemTitle = "ex-1", "Title"
	base := "Title (ex-1): landed on staging as bbbbbbbbbbbb, waiting for the team to merge it into main"
	items := WaitingForTeamMergeOf([]State{state})
	if got, want := items[0].Says(), base+"; no pull request carries it yet"; got != want {
		t.Errorf("Says() = %q, want %q", got, want)
	}
	pr := trackedRequest(4)
	state.TeamMerge.PullRequest = &pr
	items = WaitingForTeamMergeOf([]State{state})
	if got, want := items[0].Says(), base+"; pull request #4 carries it"; got != want || items[0].PullRequest != 4 || items[0].PullRequestURL != pr.URL {
		t.Errorf("Says() = %q (%+v), want %q", got, items[0], want)
	}
	pr.Unverified = true
	items = WaitingForTeamMergeOf([]State{state})
	if got, want := items[0].Says(), base+"; pull request #4 carries it (its landing commit was not found among the pull request's commits)"; got != want || !items[0].Unverified {
		t.Errorf("Says() = %q, want %q", got, want)
	}
}
