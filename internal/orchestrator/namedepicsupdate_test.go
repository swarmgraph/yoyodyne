package orchestrator

// Bringing the target branch up to date with the team's branch, under the epics
// the operator named.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// epicUpdate is an update that asks a product with one open epic.
type epicUpdate struct {
	*teamUpdateFixture
	epics passEpics
	root  string
}

func newEpicUpdate(t *testing.T) epicUpdate {
	t.Helper()
	u := epicUpdate{newTeamUpdateFixture(t), newPassEpics(t), t.TempDir()}
	u.asks(u.epics.ask)
	u.restart()
	return u
}

func (u *epicUpdate) asks(decide StartDecision) {
	u.pipeline.MayStart = decide
	u.updater.KeepRefused = func(target string) {
		if err := maystart.KeepUpdateOf(u.epics.root, "yoyodyne", target); err != nil {
			u.t.Error(err)
		}
	}
}

// restart reads the update's record from a store of its own again.
func (u *epicUpdate) restart() {
	u.t.Helper()
	records, err := runstate.NewTeamBranchUpdateStore(u.root, "yoyodyne")
	if err != nil {
		u.t.Fatal(err)
	}
	u.records, u.updater.Records = records, records
}

// kept is each target branch whose refused update is kept.
func (u *epicUpdate) kept() []maystart.RefusedUpdate {
	u.t.Helper()
	record, err := maystart.ReadRefusals(u.epics.root, "yoyodyne")
	if err != nil {
		u.t.Fatal(err)
	}
	return record.Updates
}

// refuses tries the update twice and wants nothing begun, what was kept left as
// it was, and one outstanding update.
func (u *epicUpdate) refuses(why string) {
	u.t.Helper()
	checks, reviews, staging, told := u.checks.calls(), u.reviewer.calls(), u.local("staging"), len(u.toldTheDevelopmentManager())
	before, found, err := u.records.Load("staging")
	if err != nil {
		u.t.Fatal(err)
	}
	git := &buildsNothing{TeamBranchGit: u.updater.Git}
	u.updater.Git = git
	defer func() { u.updater.Git = git.TeamBranchGit }()
	for range 2 {
		if outcome := u.update(); outcome.Landed != "" || outcome.Current || !strings.Contains(outcome.Waiting, why) || !strings.Contains(outcome.Waiting, "still due") {
			u.t.Fatalf("Update() = %#v, want it still due and refused because %q", outcome, why)
		}
	}
	after, foundAfter, err := u.records.Load("staging")
	if err != nil || found != foundAfter || !reflect.DeepEqual(before, after) {
		u.t.Fatalf("record = %#v, %v; want it left as %#v", after, err, before)
	}
	if u.checks.calls() != checks || u.reviewer.calls() != reviews || u.local("staging") != staging || len(u.toldTheDevelopmentManager()) != told || git.built != 0 {
		u.t.Fatalf("%d check run(s), %d review(s), %d merge(s) built; want nothing begun or told", u.checks.calls()-checks, u.reviewer.calls()-reviews, git.built)
	}
	if kept := u.kept(); len(kept) != 1 || kept[0].TargetBranch != "staging" || kept[0].Path != maystart.TeamBranchUpdate || !strings.Contains(kept[0].Why, why) {
		u.t.Fatalf("kept as refused: %+v, want one update of staging", kept)
	}
}

// buildsNothing counts the merges built.
type buildsNothing struct {
	TeamBranchGit
	built int
}

func (b *buildsNothing) BuildQueueCandidate(ctx context.Context, request gitworktree.QueueCandidateRequest) (gitworktree.QueueCandidate, error) {
	b.built++
	return b.TeamBranchGit.BuildQueueCandidate(ctx, request)
}

func (u *epicUpdate) lands() {
	u.t.Helper()
	if outcome := u.update(); outcome.Landed == "" || len(u.kept()) != 0 {
		u.t.Fatalf("Update() = %#v with %+v kept as refused, want it landed and none kept", outcome, u.kept())
	}
}

func (u *epicUpdate) teamMoves() { u.teamMerges("team.txt", "the team's work\n", "the team's change") }

const everyEpicEnded = "every epic the operator named has ended"

func TestAnUpdateStartsWithEpicModeOffOrAnEpicOpen(t *testing.T) {
	t.Parallel()
	for name, arrange := range map[string]func(passEpics){"off": passEpics.off, "open": func(passEpics) {}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			u := newEpicUpdate(t)
			arrange(u.epics)
			u.teamMoves()
			u.lands()
			if u.checks.calls() != 1 || u.reviewer.calls() != 1 {
				t.Fatalf("%d check run(s) and %d review(s), want one of each", u.checks.calls(), u.reviewer.calls())
			}
		})
	}
}

func TestAnUpdateWithEveryEpicEndedIsRefusedAndStartsOnceOneIsOpenOrTheModeIsOff(t *testing.T) {
	t.Parallel()
	for name, permit := range map[string]func(passEpics){
		"an epic is opened": func(e passEpics) { e.name("yoyodyne-another") },
		"epic mode is off":  passEpics.off,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			u := newEpicUpdate(t)
			u.epics.end()
			u.teamMoves()
			u.refuses(everyEpicEnded)
			// Whether it is due is worked out from the branches after a restart.
			u.restart()
			u.refuses(everyEpicEnded)
			permit(u.epics)
			u.lands()
		})
	}
}

func TestARefusedUpdateClearsWhenNoneIsDue(t *testing.T) {
	t.Parallel()
	u := newEpicUpdate(t)
	u.epics.end()
	u.teamMoves()
	u.refuses(everyEpicEnded)
	runPipelineGit(t, u.repository, "fetch", "origin", "main")
	runPipelineGit(t, u.repository, "merge", "--no-edit", "FETCH_HEAD")
	if outcome := u.update(); !outcome.Current || len(u.kept()) != 0 {
		t.Fatalf("Update() = %#v with %+v kept, want it current and nothing kept", outcome, u.kept())
	}
}

func TestAnUpdateWhoseEpicRecordCannotBeReadIsRefusedBeforeAnythingStarts(t *testing.T) {
	t.Parallel()
	u := newEpicUpdate(t)
	if err := os.WriteFile(filepath.Join(home.ProductDirectory(u.epics.root, "yoyodyne"), "epic-scope.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.teamMoves()
	u.refuses("the record of the epics the operator named could not be read")
}

// A refusal leaves a conflict run that waits, its decision and the update found
// to conflict as they were, though a branch has moved under them.
func TestARefusedUpdateLeavesAWaitingRunAndAPendingUpdateAsTheyWere(t *testing.T) {
	t.Parallel()
	f, epics, runID := waitingConflict(t)
	u := epicUpdate{f, passEpics{t: t, root: epics.root, operator: epics.operator}, t.TempDir()}
	u.asks(epics.decide)
	u.epics.end()
	u.teamMerges("app.txt", "one\nteam three\nthree\n", "the team changes it again")
	u.refuses(everyEpicEnded)
	if state := waitsForEpic(t, f.waiting.StateStore.(*runstate.Store), runID); state.RunID != runID {
		t.Fatalf("run = %s", state.RunID)
	}
	if record := u.record(); record.Decision == nil || record.Decision.RunID != runID || record.Pending == nil || record.Pending.Conflict == nil {
		t.Fatalf("record = %#v, want the run, its decision and the conflict kept", record)
	}
	u.epics.off()
	u.update()
	if state, err := f.pipeline.Store.Load(runID); err != nil || !state.Status.Terminal() || len(u.kept()) != 0 {
		t.Fatalf("run = %s, %v; want the admitted update to set it aside", state.Status, err)
	}
}

// A conflict already found goes on being said, with nothing asked.
func TestAnUpdateFoundToConflictStillSaysSoWithEveryEpicEnded(t *testing.T) {
	t.Parallel()
	f, _, _ := conflictFixture(t)
	u := epicUpdate{f, newPassEpics(t), t.TempDir()}
	u.asks(u.epics.ask)
	u.epics.end()
	if outcome := u.update(); len(outcome.Conflict) != 1 || len(u.kept()) != 0 {
		t.Fatalf("Update() = %#v with %+v kept, want the conflict said and nothing refused", outcome, u.kept())
	}
}

// A run that resolves a conflict asks under its own path, and the checks and
// review of the merge it left are the update's.
func TestTheMergeARunLeftIsNotCheckedWithEveryEpicEnded(t *testing.T) {
	t.Parallel()
	f, _, _ := conflictFixture(t)
	u := epicUpdate{f, newPassEpics(t), t.TempDir()}
	f.developer = writeIn("", "app.txt", "one\nstaging and team two\nthree\n")
	u.asks(func(start maystart.Start) maystart.Decision {
		if start.Path() == maystart.TeamBranchUpdate {
			return u.epics.ask(start)
		}
		return maystart.Decision{Answer: maystart.May}
	})
	u.epics.end()
	if _, err := u.decide(); err != nil {
		t.Fatal(err)
	}
	if outcome := u.update(); outcome.Landed != "" || u.record().Pending.ResolvedBy == "" || u.checks.calls()+u.reviewer.calls() != 0 {
		t.Fatalf("Update() = %#v, want the run's merge kept and neither checked nor reviewed", outcome)
	}
	u.refuses(everyEpicEnded)
	u.epics.name("yoyodyne-another")
	u.lands()
}

func TestAnInterruptedReviewAsksBeforeItBeginsAgain(t *testing.T) {
	t.Parallel()
	u := newEpicUpdate(t)
	u.teamMoves()
	u.reviewer.instead = func(call int) (review.Result, error, bool) {
		return review.Result{}, errors.New("the process ended"), call == 1
	}
	if _, err := u.updater.Update(context.Background()); err == nil || u.record().Pending.ChecksFinishedAt == nil {
		t.Fatalf("Update() error = %v, want the review interrupted after the checks", err)
	}
	u.restart()
	u.epics.end()
	u.refuses(everyEpicEnded)
	u.epics.name("yoyodyne-another")
	u.lands()
	if u.checks.calls() != 1 || u.reviewer.calls() != 2 {
		t.Fatalf("%d check run(s) and %d review(s), want the review alone begun again", u.checks.calls(), u.reviewer.calls())
	}
}

func TestAnUpdateWhosePullRequestIsOpenCarriesOnWithEveryEpicEnded(t *testing.T) {
	t.Parallel()
	u := newEpicUpdate(t)
	u.forge = &orchestratortest.Forge{Remote: u.remote}
	*u.pipeline = publishing(*u.pipeline, u.forge)
	u.pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	u.teamMoves()
	u.update()
	u.epics.end()
	u.restart()
	if outcome := u.update(); !strings.Contains(outcome.Waiting, "waits for a person to merge it") {
		t.Fatalf("Update() = %#v, want the pull request followed", outcome)
	}
	if err := u.forge.MergeIntoRemote("staging", u.record().Pending.Candidate); err != nil {
		t.Fatal(err)
	}
	u.forge.Merged = true
	u.lands()
}

// approvedUpdate is an update whose merge is checked and approved and waits to
// land, with every epic ended since.
func approvedUpdate(t *testing.T) (epicUpdate, func()) {
	t.Helper()
	u := newEpicUpdate(t)
	holds, err := runstate.NewLandingHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	u.pipeline.LandingHolds = holds
	if _, err := holds.Place("staging", "a script is rewriting staging", time.Hour, u.pipeline.clock().Now()); err != nil {
		t.Fatal(err)
	}
	u.teamMoves()
	if outcome := u.update(); outcome.Landed != "" || !u.record().Pending.Approved() {
		t.Fatalf("Update() = %#v, want an approved merge waiting to land", outcome)
	}
	u.epics.end()
	u.restart()
	return u, func() {
		if _, _, err := holds.Release("staging"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnApprovedMergeStillHeldLandsWithEveryEpicEnded(t *testing.T) {
	t.Parallel()
	u, release := approvedUpdate(t)
	release()
	u.lands()
	if u.checks.calls() != 1 || u.reviewer.calls() != 1 {
		t.Fatalf("%d check run(s) and %d review(s), want neither repeated", u.checks.calls(), u.reviewer.calls())
	}
}

// goneMerge is Git that no longer holds the merge built first.
type goneMerge struct {
	TeamBranchGit
	gone string
}

func (g goneMerge) HoldsCommit(ctx context.Context, commit string) (bool, error) {
	if commit == g.gone {
		return false, nil
	}
	return g.TeamBranchGit.HoldsCommit(ctx, commit)
}

func TestAnApprovedMergeNoLongerHeldAsksBeforeItIsBuiltAgain(t *testing.T) {
	t.Parallel()
	u, release := approvedUpdate(t)
	release()
	u.updater.Git = goneMerge{u.updater.Git, u.record().Pending.Candidate}
	u.refuses(everyEpicEnded)
	u.epics.name("yoyodyne-another")
	u.lands()
	if u.checks.calls() != 2 || u.reviewer.calls() != 2 {
		t.Fatalf("%d check run(s) and %d review(s), want the merge built again checked and reviewed", u.checks.calls(), u.reviewer.calls())
	}
}
