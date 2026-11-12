package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// multiTracker holds several work items and records what is done to each. The
// methods a test does not drive panic through the nil WorkTracker.
type multiTracker struct {
	WorkTracker
	items   map[string]*beads.WorkItem
	closed  map[string]string
	reopens map[string]int
	notes   map[string]int
}

func newMultiTracker(ids ...string) *multiTracker {
	tracker := &multiTracker{items: map[string]*beads.WorkItem{}, closed: map[string]string{}, reopens: map[string]int{}, notes: map[string]int{}}
	for _, id := range ids {
		tracker.items[id] = &beads.WorkItem{ID: id, Status: "in_progress"}
	}
	return tracker
}

func (m *multiTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	item, ok := m.items[id]
	if !ok {
		return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
	}
	return *item, nil
}

func (m *multiTracker) RecordOutcome(_ context.Context, id, notes string) (beads.WorkItem, error) {
	m.items[id].Notes += notes + "\n"
	m.notes[id]++
	return *m.items[id], nil
}

func (m *multiTracker) Complete(_ context.Context, id, reason string) (beads.WorkItem, error) {
	m.items[id].Status = "closed"
	m.closed[id] = reason
	return *m.items[id], nil
}

func (m *multiTracker) Reopen(_ context.Context, id, reason string, parking domain.WorkItemParking) (beads.WorkItem, error) {
	m.items[id].Status = "open"
	m.items[id].Parking = parking
	m.items[id].Notes += reason + "\n"
	m.reopens[id]++
	return *m.items[id], nil
}

type fakeTeamPullRequests struct {
	requests map[int]publish.TeamPullRequest
	failures map[int]error
	reads    map[int]int
}

func (f *fakeTeamPullRequests) TeamPullRequest(_ context.Context, number int) (publish.TeamPullRequest, error) {
	if f.reads == nil {
		f.reads = map[int]int{}
	}
	f.reads[number]++
	if err := f.failures[number]; err != nil {
		return publish.TeamPullRequest{}, err
	}
	request, ok := f.requests[number]
	if !ok {
		return publish.TeamPullRequest{}, fmt.Errorf("pull request %d does not exist in this repository", number)
	}
	request.Number = number
	return request, nil
}

type fakeClosers struct {
	closer publish.Closer
	reads  int
}

func (f *fakeClosers) Closer(_ context.Context, number int) (publish.Closer, error) {
	f.reads++
	closer := f.closer
	closer.Number = number
	return closer, nil
}

// quietTeamBranch reads the team's branch as carrying none of the landing
// commits, so a check's other settlements have nothing to do.
type quietTeamBranch struct{ ReconcileWorktrees }

func (quietTeamBranch) RemoteTargetCarries(context.Context, string, ...string) (gitworktree.TargetReading, error) {
	return gitworktree.TargetReading{Exists: true}, nil
}

type teamPullRequestFixture struct {
	store    *runstate.Store
	tracker  *multiTracker
	reader   *fakeTeamPullRequests
	closers  *fakeClosers
	runs     map[string]string
	landing  map[string]string
	worktree ReconcileWorktrees
}

const (
	teamTarget   = "main"
	teamUpstream = "release"
)

// newTeamPullRequestFixture lands one real run waiting for the team's merge,
// then records one more such run for each named item, each with a landing
// commit of its own.
func newTeamPullRequestFixture(t *testing.T, items ...string) teamPullRequestFixture {
	t.Helper()
	repository, worktreeRoot, store := restartableFixture(t)
	provider := teamMergeProvider()
	first := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-first", Title: "First", Status: "open"}}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, first, provider, []string{"exit 0"}), provider)
	pipeline.Config.Execution.UpstreamBranch = teamUpstream
	outcome, err := pipeline.Run(context.Background(), first.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	base, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !base.WaitingForTeamMerge() {
		t.Fatalf("run = %#v, want it waiting for the team's merge", base.TeamMerge)
	}
	fixture := teamPullRequestFixture{
		store:    store,
		reader:   &fakeTeamPullRequests{requests: map[int]publish.TeamPullRequest{}, failures: map[int]error{}},
		closers:  &fakeClosers{closer: publish.Closer{By: "someone-on-the-team", At: time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC)}},
		runs:     map[string]string{"yoyodyne-first": base.RunID},
		landing:  map[string]string{"yoyodyne-first": base.TeamMerge.LandingCommit},
		worktree: newObserver(t, repository, worktreeRoot),
	}
	ids := append([]string{"yoyodyne-first"}, items...)
	fixture.tracker = newMultiTracker(ids...)
	for index, id := range items {
		runID, err := runstate.NewRunID()
		if err != nil {
			t.Fatalf("NewRunID() error = %v", err)
		}
		copied := base
		copied.RunID, copied.WorkItemID = runID, id
		commit := strings.Repeat(string(rune('a'+index)), 40)
		wait := *base.TeamMerge
		wait.LandingCommit = commit
		copied.TeamMerge = &wait
		integration := *base.Integration
		integration.SourceCommit, integration.TargetCommit = commit, commit
		copied.Integration = &integration
		if err := store.Create(copied); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
		fixture.runs[id], fixture.landing[id] = runID, commit
	}
	return fixture
}

func (f teamPullRequestFixture) reconciler(t *testing.T) Reconciler {
	t.Helper()
	return Reconciler{
		Tracker:          f.tracker,
		Worktrees:        f.worktree,
		Store:            f.store,
		TeamPullRequests: f.reader,
		Closers:          f.closers,
	}
}

func (f teamPullRequestFixture) track(t *testing.T, number int, unverified bool, items ...string) (TeamPullRequestTrack, error) {
	t.Helper()
	return f.reconciler(t).TrackTeamPullRequest(context.Background(), TeamPullRequestTracking{
		Number: number, Items: items, Unverified: unverified, TargetBranch: teamTarget, UpstreamBranch: teamUpstream,
	})
}

func (f teamPullRequestFixture) load(t *testing.T, item string) runstate.State {
	t.Helper()
	state, err := f.store.Load(f.runs[item])
	if err != nil {
		t.Fatalf("Load(%s) error = %v", item, err)
	}
	return state
}

func (f teamPullRequestFixture) open(number int, commits ...string) {
	f.reader.requests[number] = publish.TeamPullRequest{URL: fmt.Sprintf("https://forge.example/pull/%d", number), State: "OPEN", BaseBranch: teamUpstream, Commits: commits}
}

func (f teamPullRequestFixture) mustTrack(t *testing.T, number int, unverified bool, items ...string) TeamPullRequestTrack {
	t.Helper()
	track, err := f.track(t, number, unverified, items...)
	if err != nil {
		t.Fatalf("track error = %v", err)
	}
	for _, item := range track.Items {
		if item.Failure != "" {
			t.Fatalf("track = %#v, want no failures", track)
		}
	}
	return track
}

func TestTrackTeamPullRequestRefusals(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a", "yoyodyne-b")
	f.open(5, f.landing["yoyodyne-a"])
	f.reader.requests[6] = publish.TeamPullRequest{State: "OPEN", BaseBranch: "other", Commits: []string{f.landing["yoyodyne-a"]}}
	f.reader.requests[7] = publish.TeamPullRequest{State: "CLOSED", BaseBranch: teamUpstream, Commits: []string{f.landing["yoyodyne-a"]}}
	f.reader.requests[8] = publish.TeamPullRequest{State: "OPEN", BaseBranch: teamUpstream, Commits: []string{f.landing["yoyodyne-a"]}, CommitsCapped: true}

	for _, test := range []struct {
		name   string
		number int
		items  []string
		want   []string
	}{
		{"base is not the team's branch", 6, []string{"yoyodyne-a"}, []string{"pull request #6 goes into other, not into the team's branch release"}},
		{"closed without merging", 7, []string{"yoyodyne-a"}, []string{"pull request #7 was closed without being merged"}},
		{"item not waiting", 5, []string{"yoyodyne-a", "yoyodyne-gone"}, []string{"yoyodyne-gone is not waiting for the team's merge"}},
		{"landing commit not among the commits", 5, []string{"yoyodyne-a", "yoyodyne-b"}, []string{"yoyodyne-b's landing commit " + strings.Repeat("b", 40) + " is not among pull request #5's commits", "--unverified"}},
		{"commits capped", 8, []string{"yoyodyne-b"}, []string{"only the first 250 commits"}},
		{"no such pull request", 99, []string{"yoyodyne-a"}, []string{"pull request #99 could not be read", "does not exist"}},
		{"no number and no items", 0, nil, []string{"a pull request number is needed", "no item is named"}},
	} {
		_, err := f.track(t, test.number, false, test.items...)
		if err == nil {
			t.Errorf("%s: error = nil, want a refusal", test.name)
			continue
		}
		for _, want := range test.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error = %q, want it to say %q", test.name, err, want)
			}
		}
	}

	// Nothing is recorded on any item when one is refused.
	for _, id := range []string{"yoyodyne-a", "yoyodyne-b"} {
		if state := f.load(t, id); state.TeamMerge.PullRequest != nil {
			t.Errorf("%s: pull request = %#v, want none recorded", id, state.TeamMerge.PullRequest)
		}
	}
	if len(f.tracker.notes) != 0 {
		t.Errorf("notes written = %v, want none", f.tracker.notes)
	}

	// A reconciler with no forge access, and a project with no team branch.
	reconciler := f.reconciler(t)
	reconciler.TeamPullRequests = nil
	_, err := reconciler.TrackTeamPullRequest(context.Background(), TeamPullRequestTracking{Number: 5, Items: []string{"yoyodyne-a"}, TargetBranch: teamTarget, UpstreamBranch: teamUpstream})
	if err == nil || !strings.Contains(err.Error(), "this command has no forge access to read pull request #5") {
		t.Errorf("no reader: error = %v", err)
	}
	for _, upstream := range []string{"", teamTarget} {
		_, err = f.reconciler(t).TrackTeamPullRequest(context.Background(), TeamPullRequestTracking{Number: 5, Items: []string{"yoyodyne-a"}, TargetBranch: teamTarget, UpstreamBranch: upstream})
		if err == nil || !strings.Contains(err.Error(), "names no team branch (execution.upstream_branch) apart from its target branch") {
			t.Errorf("upstream %q: error = %v", upstream, err)
		}
	}
}

func TestTrackTeamPullRequestRefusesAnItemLandedElsewhere(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a")
	f.open(5, f.landing["yoyodyne-a"])
	_, err := f.reconciler(t).TrackTeamPullRequest(context.Background(), TeamPullRequestTracking{
		Number: 5, Items: []string{"yoyodyne-a"}, TargetBranch: "staging", UpstreamBranch: teamUpstream,
	})
	if err == nil || !strings.Contains(err.Error(), "yoyodyne-a landed on main, not on this project's target branch staging") {
		t.Fatalf("error = %v", err)
	}
}

func TestTrackTeamPullRequestRecordsUnverifiedAndMovesAnItem(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a", "yoyodyne-b")
	f.open(5, f.landing["yoyodyne-a"])
	f.open(6, f.landing["yoyodyne-a"])

	track := f.mustTrack(t, 5, true, "yoyodyne-a", "yoyodyne-b", "yoyodyne-a")
	if track.Number != 5 || track.URL != "https://forge.example/pull/5" || len(track.Items) != 2 {
		t.Fatalf("track = %#v, want two items with duplicates collapsed", track)
	}
	if track.Items[0].Unverified || !track.Items[1].Unverified {
		t.Fatalf("items = %#v, want only the second unverified", track.Items)
	}
	b := f.load(t, "yoyodyne-b")
	if pull := b.TeamMerge.PullRequest; pull == nil || pull.Number != 5 || !pull.Unverified {
		t.Fatalf("pull request = %#v, want #5 unverified", pull)
	}
	if notes := f.tracker.items["yoyodyne-b"].Notes; !strings.Contains(notes, "Pull request #5 into release carries this item, so the item closes when the team merges it.") ||
		!strings.Contains(notes, "was not found among the pull request's commits; it was recorded as unverified at the outside tool's word.") ||
		!strings.Contains(notes, "Run: "+f.runs["yoyodyne-b"]) {
		t.Fatalf("notes = %q", notes)
	}

	// The same record again changes nothing.
	again := f.mustTrack(t, 5, true, "yoyodyne-a", "yoyodyne-b")
	if !again.Items[0].Unchanged || !again.Items[1].Unchanged || f.tracker.notes["yoyodyne-a"] != 1 {
		t.Fatalf("again = %#v, notes = %v; want unchanged and one note", again, f.tracker.notes)
	}

	// Recording an item on a second request moves it there.
	moved := f.mustTrack(t, 6, false, "yoyodyne-a")
	if moved.Items[0].MovedFrom != 5 || moved.Items[0].Unchanged {
		t.Fatalf("moved = %#v, want it moved from #5", moved.Items)
	}
	if pull := f.load(t, "yoyodyne-a").TeamMerge.PullRequest; pull == nil || pull.Number != 6 {
		t.Fatalf("pull request = %#v, want #6", pull)
	}
	if notes := f.tracker.items["yoyodyne-a"].Notes; !strings.Contains(notes, "Pull request #6 into release carries this item") || !strings.Contains(notes, "It was carried by pull request #5 before.") {
		t.Fatalf("notes = %q", notes)
	}
}

func TestUntrackTeamPullRequest(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a", "yoyodyne-b")
	f.open(5, f.landing["yoyodyne-a"], f.landing["yoyodyne-b"])
	f.mustTrack(t, 5, false, "yoyodyne-a", "yoyodyne-b")

	track, err := f.reconciler(t).UntrackTeamPullRequest(context.Background(), 5)
	if err != nil || len(track.Items) != 2 {
		t.Fatalf("UntrackTeamPullRequest() = %#v, %v; want two items", track, err)
	}
	for _, id := range []string{"yoyodyne-a", "yoyodyne-b"} {
		state := f.load(t, id)
		if state.TeamMerge.PullRequest != nil || !state.WaitingForTeamMerge() {
			t.Errorf("%s: wait = %#v, want it waiting and untracked", id, state.TeamMerge)
		}
		if notes := f.tracker.items[id].Notes; !strings.Contains(notes, "Pull request #5 is no longer tracked as carrying this item; it still waits for its landing commit to be found on release.") {
			t.Errorf("%s: notes = %q", id, notes)
		}
	}
	if _, err := f.reconciler(t).UntrackTeamPullRequest(context.Background(), 5); err == nil ||
		!strings.Contains(err.Error(), "no item waiting for the team's merge is tracked under pull request #5") {
		t.Fatalf("second UntrackTeamPullRequest() error = %v", err)
	}
}

func TestSettleTeamPullRequestsClosesTheItemsAMergedRequestCarries(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a", "yoyodyne-b")
	f.open(5, f.landing["yoyodyne-a"])
	// The batch was squashed, so the second item's commit is not among the
	// request's, and it was recorded unverified.
	f.mustTrack(t, 5, true, "yoyodyne-a", "yoyodyne-b")
	mergeCommit := strings.Repeat("9", 40)
	f.reader.requests[5] = publish.TeamPullRequest{URL: "https://forge.example/pull/5", State: "MERGED", Merged: true, BaseBranch: teamUpstream, MergeCommit: mergeCommit}

	reconciler := f.reconciler(t)
	settled, err := reconciler.SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 1 || settled[0].Failure != "" || settled[0].Found != TeamPullRequestMerged || settled[0].MergeCommit != mergeCommit || len(settled[0].Items) != 2 {
		t.Fatalf("SettleTeamPullRequests() = %#v, %v", settled, err)
	}
	for _, id := range []string{"yoyodyne-a", "yoyodyne-b"} {
		state := f.load(t, id)
		if state.WaitingForTeamMerge() || state.TeamMerge.FoundBy != 5 || state.TeamMerge.FoundOn != mergeCommit || !state.TeamMerge.Found() {
			t.Errorf("%s: wait = %#v, want it found by #5 on the merge commit", id, state.TeamMerge)
		}
		item := f.tracker.items[id]
		wantHeadline := "The team merged pull request #5, which carries this item, into release as " + mergeCommit + ", so the item is closed."
		if item.Status != "closed" || !strings.Contains(item.Notes, wantHeadline) ||
			!strings.Contains(item.Notes, fmt.Sprintf("It landed on main as %s. Run: %s", f.landing[id], f.runs[id])) {
			t.Errorf("%s: status %q, notes %q", id, item.Status, item.Notes)
		}
		wantReason := fmt.Sprintf("Reviewed by Yoyodyne run %s and landed on main as %s, which the team merged into release through pull request #5 as %s", f.runs[id], f.landing[id], mergeCommit)
		if f.tracker.closed[id] != wantReason {
			t.Errorf("%s: close reason = %q, want %q", id, f.tracker.closed[id], wantReason)
		}
	}

	// A second check has nothing more to do, and reads nothing.
	reads := f.reader.reads[5]
	notes := f.tracker.notes["yoyodyne-a"]
	again, err := reconciler.SettleTeamPullRequests(context.Background())
	if err != nil || again == nil || len(again) != 0 || f.reader.reads[5] != reads || f.tracker.notes["yoyodyne-a"] != notes {
		t.Fatalf("second check = %#v, %v; reads %d, notes %d", again, err, f.reader.reads[5], f.tracker.notes["yoyodyne-a"])
	}
}

func TestSettleTeamPullRequestsFinishesAnItemClosedBeforeTheRunWasRecorded(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a")
	f.open(5, f.landing["yoyodyne-a"])
	f.mustTrack(t, 5, false, "yoyodyne-a")
	f.tracker.items["yoyodyne-a"].Status = "closed"
	f.reader.requests[5] = publish.TeamPullRequest{State: "MERGED", Merged: true, BaseBranch: teamUpstream, MergeCommit: strings.Repeat("9", 40)}

	settled, err := f.reconciler(t).SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 1 || !settled[0].Items[0].Settled {
		t.Fatalf("SettleTeamPullRequests() = %#v, %v", settled, err)
	}
	if len(f.tracker.closed) != 0 || f.load(t, "yoyodyne-a").WaitingForTeamMerge() {
		t.Fatalf("closed again = %v, waiting = %v; want the item left and the run recorded", f.tracker.closed, f.load(t, "yoyodyne-a").WaitingForTeamMerge())
	}
}

func TestSettleTeamPullRequestsReadsAMergeCommitTheForgeDoesNotName(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a")
	f.open(5, f.landing["yoyodyne-a"])
	f.mustTrack(t, 5, false, "yoyodyne-a")
	f.reader.requests[5] = publish.TeamPullRequest{State: "MERGED", Merged: true, BaseBranch: teamUpstream}

	settled, err := f.reconciler(t).SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 1 || !strings.Contains(settled[0].Failure, "the forge names no merge commit for pull request #5") || len(settled[0].Items) != 0 {
		t.Fatalf("SettleTeamPullRequests() = %#v, %v", settled, err)
	}
	if !f.load(t, "yoyodyne-a").WaitingForTeamMerge() || f.tracker.items["yoyodyne-a"].Status != "in_progress" {
		t.Fatal("the item was settled without a merge commit")
	}
}

func TestSettleTeamPullRequestsGivesBackTheItemsOfAClosedRequest(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a", "yoyodyne-b")
	f.open(5, f.landing["yoyodyne-a"], f.landing["yoyodyne-b"])
	f.mustTrack(t, 5, false, "yoyodyne-a", "yoyodyne-b")
	f.reader.requests[5] = publish.TeamPullRequest{State: "CLOSED", BaseBranch: teamUpstream}

	reconciler := f.reconciler(t)
	reconciler.Closers = nil
	settled, err := reconciler.SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 1 || !strings.Contains(settled[0].Failure, "has no forge access to read who closed it") {
		t.Fatalf("without closers: %#v, %v", settled, err)
	}
	if f.tracker.reopens["yoyodyne-a"] != 0 || !f.load(t, "yoyodyne-a").WaitingForTeamMerge() {
		t.Fatal("an item was given back without knowing who closed the request")
	}

	reconciler = f.reconciler(t)
	settled, err = reconciler.SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 1 || settled[0].Failure != "" || settled[0].Found != TeamPullRequestClosed || settled[0].ClosedBy != "someone-on-the-team" || settled[0].ClosedAt == nil || len(settled[0].Items) != 2 {
		t.Fatalf("SettleTeamPullRequests() = %#v, %v", settled, err)
	}
	if f.closers.reads != 1 {
		t.Fatalf("closer read %d times, want once for the request", f.closers.reads)
	}
	who := "someone-on-the-team at " + personMergeLocalTime(f.closers.closer.At)
	for _, id := range []string{"yoyodyne-a", "yoyodyne-b"} {
		item := f.tracker.items[id]
		if item.Status != "open" || f.tracker.reopens[id] != 1 {
			t.Errorf("%s: status %q, reopened %d times; want open once", id, item.Status, f.tracker.reopens[id])
		}
		if reason := item.Parking.Reason(); !strings.Contains(reason, "Pull request #5 into release was closed without merging by "+who) || len(reason) > domain.MaxWorkItemParkingBytes {
			t.Errorf("%s: parking = %q", id, reason)
		}
		for _, want := range []string{
			"Pull request #5 into release, which carried this item, was closed without being merged, by " + who + ".",
			"Run: " + f.runs[id],
			fmt.Sprintf("The change stays on main as %s, and nothing was undone.", f.landing[id]),
			"The item goes back to the Lead Product Manager",
		} {
			if !strings.Contains(item.Notes, want) {
				t.Errorf("%s: notes = %q, want %q", id, item.Notes, want)
			}
		}
		state := f.load(t, id)
		if state.WaitingForTeamMerge() || state.TeamMerge.Returned == nil || state.TeamMerge.Returned.Number != 5 || state.TeamMerge.Returned.ClosedBy != "someone-on-the-team" {
			t.Errorf("%s: wait = %#v, want it returned", id, state.TeamMerge)
		}
		if !itemSettled(state, item.Status) {
			t.Errorf("%s: itemSettled = false, want a given-back item settled", id)
		}
		if itemSettled(state, "in_progress") {
			t.Errorf("%s: itemSettled = true on a claimed item", id)
		}
	}

	again, err := reconciler.SettleTeamPullRequests(context.Background())
	if err != nil || len(again) != 0 || f.tracker.reopens["yoyodyne-a"] != 1 {
		t.Fatalf("second check = %#v, %v; reopened %d", again, err, f.tracker.reopens["yoyodyne-a"])
	}
}

func TestSettleTeamPullRequestsLeavesAGivenBackItemAloneOnRetry(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a")
	f.open(5, f.landing["yoyodyne-a"])
	f.mustTrack(t, 5, false, "yoyodyne-a")
	f.reader.requests[5] = publish.TeamPullRequest{State: "CLOSED", BaseBranch: teamUpstream}

	// An earlier attempt gave the item back and failed before recording the run.
	state := f.load(t, "yoyodyne-a")
	note, parking := teamPullRequestClosedNotes(state, 5, f.closers.closer)
	item := f.tracker.items["yoyodyne-a"]
	item.Status, item.Parking, item.Notes = "open", parking, note+"\n"

	settled, err := f.reconciler(t).SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 1 || !settled[0].Items[0].Settled {
		t.Fatalf("SettleTeamPullRequests() = %#v, %v", settled, err)
	}
	if f.tracker.reopens["yoyodyne-a"] != 0 || f.load(t, "yoyodyne-a").TeamMerge.Returned == nil {
		t.Fatalf("reopened %d times, returned = %v; want the item left and the run recorded", f.tracker.reopens["yoyodyne-a"], f.load(t, "yoyodyne-a").TeamMerge.Returned)
	}
}

func TestSettleTeamPullRequestsAddsTheItemsAnOpenRequestCarries(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a", "yoyodyne-b", "yoyodyne-c")
	f.open(5, f.landing["yoyodyne-a"], f.landing["yoyodyne-b"], f.landing["yoyodyne-c"])
	f.open(6, f.landing["yoyodyne-c"])
	f.mustTrack(t, 5, false, "yoyodyne-a")
	// Item c is carried by request #6 already, though request #5 has its commit.
	f.mustTrack(t, 6, false, "yoyodyne-c")

	settled, err := f.reconciler(t).SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 2 || settled[0].Number != 5 || settled[1].Number != 6 {
		t.Fatalf("SettleTeamPullRequests() = %#v, %v; want requests 5 and 6 in order", settled, err)
	}
	if settled[0].Found != TeamPullRequestOpen || len(settled[0].Items) != 1 || !settled[0].Items[0].Added || settled[0].Items[0].WorkItemID != "yoyodyne-b" {
		t.Fatalf("request 5 = %#v, want only item b added", settled[0])
	}
	b := f.load(t, "yoyodyne-b")
	if pull := b.TeamMerge.PullRequest; pull == nil || pull.Number != 5 || !pull.Found || pull.Unverified || !b.WaitingForTeamMerge() {
		t.Fatalf("item b: wait = %#v, want it found in #5 and still waiting", b.TeamMerge)
	}
	if notes := f.tracker.items["yoyodyne-b"].Notes; !strings.Contains(notes, fmt.Sprintf("A forge check found this item's landing commit %s among the commits of pull request #5 into release, so the pull request is now tracked as carrying it.", f.landing["yoyodyne-b"])) {
		t.Fatalf("notes = %q", notes)
	}
	if pull := f.load(t, "yoyodyne-c").TeamMerge.PullRequest; pull == nil || pull.Number != 6 {
		t.Fatalf("item c: pull request = %#v, want it left on #6", pull)
	}
	if f.tracker.notes["yoyodyne-c"] != 1 {
		t.Fatalf("item c was noted %d times, want only when it was tracked", f.tracker.notes["yoyodyne-c"])
	}
	// The run the pipeline landed is not in any request's commits.
	if f.load(t, "yoyodyne-first").TeamMerge.PullRequest != nil {
		t.Fatal("an item whose landing commit is in no request was added")
	}
}

func TestSettleTeamPullRequestsSaysWhatItCannotRead(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a", "yoyodyne-b")
	f.open(5, f.landing["yoyodyne-a"])
	f.open(6, f.landing["yoyodyne-b"])
	f.mustTrack(t, 5, false, "yoyodyne-a")
	f.mustTrack(t, 6, false, "yoyodyne-b")
	f.reader.failures[5] = errors.New("the forge is down")
	f.reader.requests[6] = publish.TeamPullRequest{State: "MERGED", Merged: true, BaseBranch: teamUpstream, MergeCommit: strings.Repeat("9", 40)}

	settled, err := f.reconciler(t).SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 2 || !strings.Contains(settled[0].Failure, "read pull request #5: the forge is down") || settled[0].Found != "" || !settled[1].Items[0].Settled {
		t.Fatalf("SettleTeamPullRequests() = %#v, %v; want #5 unread and #6 settled", settled, err)
	}

	reconciler := f.reconciler(t)
	reconciler.TeamPullRequests = nil
	if _, err := reconciler.SettleTeamPullRequests(context.Background()); err == nil {
		t.Fatal("error = nil, want one for a check that cannot read a tracked request")
	}
	// With nothing tracked a check without forge access has nothing to say.
	f.reader.failures = nil
	untracked := newTeamPullRequestFixture(t)
	reconciler = untracked.reconciler(t)
	reconciler.TeamPullRequests = nil
	if settled, err := reconciler.SettleTeamPullRequests(context.Background()); err != nil || settled == nil || len(settled) != 0 {
		t.Fatalf("SettleTeamPullRequests() = %#v, %v; want an empty list", settled, err)
	}
}

func TestCheckForgeIncludesTeamPullRequests(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a")
	f.open(5, f.landing["yoyodyne-a"])
	f.mustTrack(t, 5, false, "yoyodyne-a")
	f.reader.requests[5] = publish.TeamPullRequest{State: "MERGED", Merged: true, BaseBranch: teamUpstream, MergeCommit: strings.Repeat("9", 40)}
	f.worktree = quietTeamBranch{f.worktree}

	check, err := f.reconciler(t).CheckForge(context.Background())
	if err != nil {
		t.Fatalf("CheckForge() error = %v", err)
	}
	if len(check.TeamPullRequests) != 1 || !check.TeamPullRequests[0].Items[0].Settled || check.Failures() != 0 {
		t.Fatalf("check = %#v, want request 5 settled with no failures", check)
	}
	if f.tracker.items["yoyodyne-a"].Status != "closed" {
		t.Fatalf("item status = %q, want closed", f.tracker.items["yoyodyne-a"].Status)
	}

	failing := ForgeCheck{TeamPullRequests: []TeamPullRequestSettlement{{Failure: "x", Items: []TeamPullRequestItem{{Failure: "y"}, {}}}}}
	if failing.Failures() != 2 {
		t.Fatalf("Failures() = %d, want 2", failing.Failures())
	}
}
