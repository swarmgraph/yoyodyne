package teamspends_test

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/teamspends"
)

const (
	item   = "yoyodyne-ifd.7"
	runOne = "run-11112222333344445555666677778888"
	runTwo = "run-99998888777766665555444433332222"
)

var (
	epoch = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	// One repair, one re-run and one re-arm, the smallest caps there are, so a
	// second spend by anybody is the one that is refused.
	caps = runstate.TriageCaps{ReviewRounds: 10, RepairGrants: 1, Reruns: 1, MergeRearms: 1}
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	var out, errs bytes.Buffer
	command.Stdout, command.Stderr = &out, &errs
	if err := command.Run(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, errs.String())
	}
	return strings.TrimSpace(out.String())
}

type factory struct {
	name    string
	clone   string
	runs    *runstate.Store
	claims  *runstate.TeamClaimStore
	state   *runstate.SharedRecordsStore
	spender teamspends.Spender
}

// newFactories makes a bare remote and two factories cloned from it, each with
// stores and a records repository of its own, counting through the remote.
func newFactories(t *testing.T) (a, b *factory, remote string) {
	t.Helper()
	base := t.TempDir()
	remote = filepath.Join(base, "remote.git")
	git(t, base, "init", "--bare", "--quiet", remote)
	seed := filepath.Join(base, "seed")
	git(t, base, "init", "--quiet", seed)
	git(t, seed, "commit", "--allow-empty", "--quiet", "-m", "seed")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	var made []*factory
	for _, name := range []string{"factory A", "factory B"} {
		clone := filepath.Join(base, strings.ReplaceAll(name, " ", "-"))
		git(t, base, "clone", "--quiet", remote, clone)
		git(t, clone, "config", "maintenance.auto", "false")
		git(t, clone, "config", "gc.auto", "0")
		made = append(made, newFactory(t, name, clone))
	}
	return made[0], made[1], remote
}

func newFactory(t *testing.T, name, clone string) *factory {
	t.Helper()
	manager, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: clone,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Remote:         "origin",
		Timeout:        2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	root := t.TempDir()
	state, err := runstate.NewSharedRecordsStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := runstate.NewTeamClaimStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	f := &factory{name: name, clone: clone, runs: runs, claims: claims, state: state}
	f.spender = teamspends.Spender{
		Open: func(ctx context.Context) (teamspends.Remote, error) {
			return manager.RecordsRepository(ctx, state.RepositoryDirectory())
		},
		Claims:  claims,
		Lock:    state,
		Factory: func(context.Context) string { return name },
		Now:     func() time.Time { return epoch },
	}
	runs.UseSharedSpends(f.spender)
	return f
}

// hold records that this factory holds the claim on the item, and release that
// it no longer does.
func (f *factory) hold(t *testing.T) {
	t.Helper()
	if err := f.claims.Save(runstate.TeamClaim{WorkItemID: item, RunID: runOne, Factory: f.name, Commit: "0123456789abcdef", TakenAt: epoch, RenewedAt: epoch}); err != nil {
		t.Fatal(err)
	}
}

func (f *factory) release(t *testing.T) {
	t.Helper()
	claim, found, err := f.claims.Load(item)
	if err != nil || !found {
		t.Fatalf("Load() = %v, %v", found, err)
	}
	claim.ReleasedAt = &epoch
	if err := f.claims.Save(claim); err != nil {
		t.Fatal(err)
	}
}

func decided(word, run string) runstate.TriageDecision {
	return runstate.TriageDecision{
		Decision:     word,
		RunID:        run,
		Reason:       "the reasoning the development manager recorded for this decision",
		DecidedBy:    "development manager",
		Conversation: "chat-0123456789abcdef",
		Turn:         3,
	}
}

func (f *factory) grant(t *testing.T, run string) error {
	t.Helper()
	_, err := f.runs.Triage().GrantRepair(context.Background(), item, decided(runstate.TriageDecisionRepair, run), 1, epoch, caps)
	return err
}

func (f *factory) counters(t *testing.T) runstate.TriageCounters {
	t.Helper()
	counters, err := f.runs.Triage().Counters(item)
	if err != nil {
		t.Fatal(err)
	}
	return counters
}

// remoteSpends is how many spend records the remote holds for the item.
func remoteSpends(t *testing.T, f *factory) int {
	t.Helper()
	ctx := context.Background()
	manager, err := f.spender.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := manager.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := manager.Paths(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for path := range paths {
		if strings.HasPrefix(path, "spends/"+item+"/") {
			n++
		}
	}
	return n
}

func TestARepairCapOfOneMetByTwoFactoriesInTurnSpendsOneRepairBetweenThem(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	a.hold(t)
	if err := a.grant(t, runOne); err != nil {
		t.Fatalf("A's grant error = %v", err)
	}
	// A's run ended and B holds the item now. B has spent nothing itself, and the
	// team has spent the one repair the cap allows.
	a.release(t)
	b.hold(t)
	if got := b.counters(t).RepairGrants; got != 0 {
		t.Fatalf("B's own count = %d before it asks, want 0", got)
	}
	err := b.grant(t, runTwo)
	var refused runstate.TriageCapError
	if !errors.As(err, &refused) {
		t.Fatalf("B's grant error = %v, want the repair grant cap", err)
	}
	if one, ok := refused.RefusedBy(runstate.TriageRepairGrantBudget); !ok || one.Spent != 1 || one.Cap != 1 {
		t.Errorf("refusal = %+v, want 1 of 1 repair grants spent", one)
	}
	if got := remoteSpends(t, b); got != 1 {
		t.Errorf("the remote holds %d spend records, want the one A's spend left", got)
	}
}

func TestAFactoryNotHoldingTheClaimSpendsNothing(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	a.hold(t)
	err := b.grant(t, runOne)
	var notHolder *runstate.NotClaimHolderError
	if !errors.As(err, &notHolder) {
		t.Fatalf("B's grant error = %v, want a refusal for not holding the claim", err)
	}
	if got := b.counters(t).RepairGrants; got != 0 {
		t.Errorf("B counted %d grants for a spend it did not make", got)
	}
	if got := remoteSpends(t, a); got != 0 {
		t.Errorf("the remote holds %d spend records, want none", got)
	}
	// A claim this machine gave up is no claim either.
	a.release(t)
	if err := a.grant(t, runOne); !errors.As(err, &notHolder) {
		t.Errorf("a released claim spent: %v", err)
	}
}

func TestASpendThatCannotBePushedIsNotMadeAndCostsNothing(t *testing.T) {
	t.Parallel()
	a, _, remote := newFactories(t)
	a.hold(t)
	git(t, a.clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	err := a.grant(t, runOne)
	if !runstate.IsSpendNotShared(err) {
		t.Fatalf("grant error = %v, want a spend not shared", err)
	}
	if got := a.counters(t); got.RepairGrants != 0 || got.CommittedRounds != 0 || len(got.Decisions) != 0 {
		t.Fatalf("counters = %+v after a spend that was not made, want nothing charged or decided", got)
	}
	git(t, a.clone, "remote", "set-url", "origin", remote)
	if err := a.grant(t, runOne); err != nil {
		t.Fatalf("grant after the remote returned error = %v", err)
	}
	if got := a.counters(t).RepairGrants; got != 1 {
		t.Errorf("RepairGrants = %d, want the one grant made", got)
	}
	if got := remoteSpends(t, a); got != 1 {
		t.Errorf("remote spends = %d, want 1", got)
	}
}

func TestATakeoverStartsFromTheSpendsTheEarlierHolderRecorded(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	bigger := caps
	bigger.RepairGrants = 2
	a.hold(t)
	if _, err := a.runs.Triage().GrantRepair(context.Background(), item, decided(runstate.TriageDecisionRepair, runOne), 1, epoch, bigger); err != nil {
		t.Fatal(err)
	}
	// A is gone. B takes the claim over and has never counted anything.
	b.hold(t)
	if _, err := b.runs.Triage().GrantRepair(context.Background(), item, decided(runstate.TriageDecisionRepair, runTwo), 1, epoch, bigger); err != nil {
		t.Fatalf("B's grant error = %v", err)
	}
	if got := b.counters(t).RepairGrants; got != 2 {
		t.Errorf("B counts %d grants, want A's and its own", got)
	}
	if _, err := b.runs.Triage().GrantRepair(context.Background(), item, decided(runstate.TriageDecisionRepair, runTwo), 1, epoch, bigger); !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Errorf("B's third grant error = %v, want the cap of two reached", err)
	}
}

func TestReRunsAndMergeRearmsAreCountedAcrossFactoriesToo(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	a.hold(t)
	ctx := context.Background()
	if _, err := a.runs.Triage().RecordRerun(ctx, item, decided(runstate.TriageDecisionRerun, runOne), epoch, caps); err != nil {
		t.Fatal(err)
	}
	if _, err := a.runs.Triage().RecordMergeRearm(ctx, item, "run-1:#4", decided(runstate.TriageDecisionRearm, runOne), epoch, caps); err != nil {
		t.Fatal(err)
	}
	b.hold(t)
	if _, err := b.runs.Triage().RecordRerun(ctx, item, decided(runstate.TriageDecisionRerun, runTwo), epoch, caps); !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Errorf("B's re-run error = %v, want the cap reached", err)
	}
	if _, err := b.runs.Triage().RecordMergeRearm(ctx, item, "run-1:#4", decided(runstate.TriageDecisionRearm, runTwo), epoch, caps); !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Errorf("B's re-arm of the same publication error = %v, want the cap reached", err)
	}
	// Another publication has its own budget.
	if _, err := b.runs.Triage().RecordMergeRearm(ctx, item, "run-2:#9", decided(runstate.TriageDecisionRearm, runTwo), epoch, caps); err != nil {
		t.Errorf("B's re-arm of another publication error = %v", err)
	}
}

func TestReviewRoundsAreCountedFromTheSharedRecords(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	ctx := context.Background()
	a.hold(t)
	if _, err := a.runs.Triage().RecordReviewRound(ctx, item, runstate.RoundKey(runOne, 0), "pid-1", epoch); err != nil {
		t.Fatal(err)
	}
	// The same round recorded again is one record.
	if _, err := a.runs.Triage().RecordReviewRound(ctx, item, runstate.RoundKey(runOne, 0), "pid-1", epoch); err != nil {
		t.Fatal(err)
	}
	b.hold(t)
	counted, err := b.runs.Triage().RecordReviewRound(ctx, item, runstate.RoundKey(runTwo, 0), "pid-2", epoch)
	if err != nil {
		t.Fatal(err)
	}
	if counted.ReviewRounds != 2 {
		t.Errorf("B counts %d rounds, want A's and its own", counted.ReviewRounds)
	}
	// A round given back is given back for the team.
	if _, round, err := b.runs.Triage().ReturnReviewRound(ctx, item, runstate.RoundKey(runTwo, 0), "pid-2", epoch); err != nil || !round.Returned {
		t.Fatalf("ReturnReviewRound() = %+v, %v", round, err)
	}
	a.release(t)
	a.hold(t)
	again, err := a.runs.Triage().RecordReviewRound(ctx, item, runstate.RoundKey(runOne, 1), "pid-1", epoch)
	if err != nil {
		t.Fatal(err)
	}
	if again.ReviewRounds != 2 {
		t.Errorf("A counts %d rounds, want its first and its second with B's given back", again.ReviewRounds)
	}
}

func TestAMachineJoiningPushesItsEarlierSpendsOnce(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	ctx := context.Background()
	// A spent before team mode was on for it: its store is not counting through
	// the remote.
	earlier, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := earlier.Triage().GrantRepair(ctx, item, decided(runstate.TriageDecisionRepair, runOne), 1, epoch, caps); err != nil {
		t.Fatal(err)
	}
	items, err := earlier.Triage().All()
	if err != nil || len(items) != 1 {
		t.Fatalf("All() = %v, %v", items, err)
	}
	joined, err := a.spender.Join(ctx, items)
	if err != nil || joined.Pushed != 1 {
		t.Fatalf("Join() = %+v, %v, want the one spend pushed", joined, err)
	}
	again, err := a.spender.Join(ctx, items)
	if err != nil || again.Pushed != 0 {
		t.Fatalf("second Join() = %+v, %v, want nothing pushed", again, err)
	}
	if got := remoteSpends(t, a); got != 1 {
		t.Errorf("remote spends = %d, want 1", got)
	}
	// B now meets the cap A's earlier spend used.
	b.hold(t)
	if err := b.grant(t, runTwo); !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Errorf("B's grant error = %v, want the cap reached by what A spent before joining", err)
	}
	if _, done, err := a.state.SpendsJoined(); err != nil || done {
		t.Fatalf("SpendsJoined() = %v, %v before it was marked", done, err)
	}
	if err := a.state.MarkSpendsJoined(epoch); err != nil {
		t.Fatal(err)
	}
	if at, done, err := a.state.SpendsJoined(); err != nil || !done || !at.Equal(epoch) {
		t.Errorf("SpendsJoined() = %v, %v, %v", at, done, err)
	}
}

// A machine that spends through the shared records, and takes in another
// factory's spend, before its join runs pushes only what it counted before it
// began: its live counters hold records that are already on the remote.
func TestJoiningPushesWhatWasKeptAndNotWhatSpendingRaisedTheCountersTo(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	ctx := context.Background()
	bigger := caps
	bigger.RepairGrants = 5
	// B counted one repair grant before team mode, in a store that did not count
	// through the remote, and keeps it before it spends through the remote.
	before, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := before.Triage().GrantRepair(ctx, item, decided(runstate.TriageDecisionRepair, runOne), 1, epoch, bigger); err != nil {
		t.Fatal(err)
	}
	if err := b.state.KeepSpendsBefore(ctx, before.Triage().All, epoch); err != nil {
		t.Fatal(err)
	}
	// A spends one; B then spends one of its own, which raises B's counters to
	// the team's count.
	a.hold(t)
	if _, err := a.runs.Triage().GrantRepair(ctx, item, decided(runstate.TriageDecisionRepair, runOne), 1, epoch, bigger); err != nil {
		t.Fatal(err)
	}
	b.hold(t)
	if _, err := b.runs.Triage().GrantRepair(ctx, item, decided(runstate.TriageDecisionRepair, runTwo), 1, epoch, bigger); err != nil {
		t.Fatal(err)
	}
	if got := remoteSpends(t, b); got != 2 {
		t.Fatalf("remote spends = %d before joining, want A's and B's", got)
	}
	// Keeping again changes nothing, as a second process starting would.
	if err := b.state.KeepSpendsBefore(ctx, b.runs.Triage().All, epoch); err != nil {
		t.Fatal(err)
	}
	joined, err := b.spender.JoinKept(ctx, b.state)
	if err != nil || joined.Pushed != 1 {
		t.Fatalf("JoinKept() = %+v, %v, want only the one spend counted before joining", joined, err)
	}
	if got := remoteSpends(t, b); got != 3 {
		t.Errorf("remote spends = %d after joining, want 3", got)
	}
	if again, err := b.spender.JoinKept(ctx, b.state); err != nil || again.Pushed != 0 {
		t.Errorf("second JoinKept() = %+v, %v, want nothing", again, err)
	}
}
