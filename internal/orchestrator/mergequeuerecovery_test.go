package orchestrator

// The merge queue's withdrawal and failure recovery: a queued merge is taken
// back and confirmed before the change is handed anywhere, a merge that lands
// first is completed once and never replayed, a process stopped around any
// request or record is finished from what the record and the forge show, a
// failed candidate keeps its evidence and is read for what it says, and only a
// defect of the change spends anything — out of the run's own budget, which no
// restart refills.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// withdrawingForge withdraws the fixture forge's queued merge, answering each
// call as a test says first.
type withdrawingForge struct {
	mu     sync.Mutex
	forge  *orchestratortest.Forge
	calls  int
	answer func(call int) error
}

func (w *withdrawingForge) DisableAutoMerge(_ context.Context, number int) error {
	w.mu.Lock()
	w.calls++
	call := w.calls
	w.mu.Unlock()
	if w.answer != nil {
		if err := w.answer(call); err != nil {
			return err
		}
	}
	if number != w.forge.Number {
		return fmt.Errorf("pull request %d is not the one the forge holds", number)
	}
	w.forge.DropQueuedMerge()
	return nil
}

func (w *withdrawingForge) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

// recordingRepair is the run's repair loop, recording each hand-back and
// refusing the first fail of them.
type recordingRepair struct {
	mu     sync.Mutex
	given  []runstate.MergeQueueHandback
	fail   int
	entity string
}

func (r *recordingRepair) HandBack(_ context.Context, entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail > 0 {
		r.fail--
		return errors.New("the repair loop could not be reached")
	}
	r.entity = entry.RunID
	r.given = append(r.given, handback)
	return nil
}

func (r *recordingRepair) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.given)
}

// queuedMergeFixture is a protected target whose verified candidate's merge the
// forge holds queued.
func queuedMergeFixture(t *testing.T) (*promotionFixture, *withdrawingForge, runstate.MergeQueueGeneration) {
	t.Helper()
	f := newPromotionFixture(t, landProtected)
	withdrawer := &withdrawingForge{forge: f.forge.Forge}
	f.promoter.Withdrawer = withdrawer
	f.forge.QueueMerge = true
	generation := f.verify()
	waiting, err := f.promote()
	if err != nil || waiting.Waiting == "" {
		t.Fatalf("Promote() = %#v, %v; want the merge queued with the forge", waiting, err)
	}
	return f, withdrawer, generation
}

func (f *promotionFixture) withdraw() (MergeQueuePromotion, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return f.promoter.Withdraw(ctx, queueKey, f.entry.EntryID, "the change is to be repaired")
}

func (f *promotionFixture) recover() (MergeQueueRecovery, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return f.promoter.Recover(ctx, queueKey)
}

// assertStillArmed checks a withdrawal left standing refuses every rewrite of
// the change and asked the forge for nothing new.
func (f *promotionFixture) assertStillArmed() {
	f.t.Helper()
	landing := f.landing()
	if refusal := landing.HeadRewriteRefusal(); refusal == "" {
		f.t.Fatal("an unconfirmed withdrawal lets the change's head be rewritten")
	}
	if landing.Handback != nil || landing.Completion != nil {
		f.t.Fatalf("landing = %#v, want neither a handback nor a completion while the merge may land", landing)
	}
	if len(f.forge.MergeRequests()) != 1 {
		f.t.Fatal("the merge was asked for again while it was being withdrawn")
	}
}

func TestAConfirmedWithdrawalSetsTheQueuedMergeAsideAndAsksForNothingMore(t *testing.T) {
	t.Parallel()

	f, withdrawer, _ := queuedMergeFixture(t)
	base := f.local("main")
	if refusal := f.landing().HeadRewriteRefusal(); refusal == "" {
		t.Fatal("a queued merge lets the change's head be rewritten")
	}
	withdrawn, err := f.withdraw()
	if err != nil || !withdrawn.Withdrawn || withdrawn.Landed || withdrawn.stopped() {
		t.Fatalf("Withdraw() = %#v, %v; want the merge withdrawn", withdrawn, err)
	}
	if f.forge.Queued || withdrawer.count() != 1 {
		t.Fatalf("forge queued %v after %d withdrawals, want one withdrawal that took the merge back", f.forge.Queued, withdrawer.count())
	}
	attempt, _ := f.landing().Current()
	if !attempt.Withdrawn() || attempt.SetAside == nil || attempt.Withdrawal.PullRequest != attempt.PullRequest || attempt.Withdrawal.Pinned != attempt.Candidate {
		t.Fatalf("attempt = %#v, want its pinned merge confirmed withdrawn and the attempt set aside", attempt)
	}
	if refusal := f.landing().HeadRewriteRefusal(); refusal != "" {
		t.Fatalf("HeadRewriteRefusal() after confirmation = %q, want none", refusal)
	}
	// A withdrawal asked for outside a recovery releases the entry: its turn
	// ends with the harness named as who moves next, and nothing asks for the
	// merge again or builds the entry again.
	released := f.landing().Handback
	if released == nil || released.Continuation != runstate.MergeQueueReleased || released.Mover != ownership.MoverHarness || released.Class != "" {
		t.Fatalf("handback = %#v, want the entry released to the harness", released)
	}
	// An entry admitted behind it is the next one promoted, not held behind it.
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	behind, _, err := f.queue.Admit(context.Background(), runstate.MergeQueueAdmission{
		Key: queueKey, WorkItemID: "yoyodyne-later", WorkItemTitle: "Later work", RunID: runID,
		ApprovedHead: f.head, IntegrationPolicy: "automatic", Mode: runstate.MergeQueueHarness, ModeEvidence: harnessModeEvidence(),
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.promote()
	if err != nil || again.Entry.EntryID != behind.EntryID || len(f.forge.MergeRequests()) != 1 || len(f.landing().Attempts) != 1 {
		t.Fatalf("Promote() after the withdrawal = %#v, %v; want the entry behind it taken up and nothing asked again", again, err)
	}
	if recovered, err := f.recover(); err != nil || recovered.Entry.EntryID == f.entry.EntryID {
		t.Fatalf("Recover() after the release = %#v, %v; want the released entry left alone", recovered, err)
	}
	// The worker moves on to the entry behind it, whose run this fixture never
	// made, so reading that run is the error it stops on.
	if verification, _ := f.work(); verification.Entry.EntryID != behind.EntryID {
		t.Fatalf("Work() after the withdrawal = %#v; want the entry behind the released one taken up", verification)
	}
	if f.local("main") != base || f.run.saveCount() != 0 {
		t.Fatal("a withdrawal moved the target or wrote to the run")
	}
	// Asked again, the withdrawal is already done and the forge is not asked.
	if repeated, err := f.withdraw(); err != nil || !repeated.Withdrawn || withdrawer.count() != 1 {
		t.Fatalf("Withdraw() again = %#v, %v after %d forge calls; want it done without asking", repeated, err, withdrawer.count())
	}
}

func TestADelayedOrAmbiguousWithdrawalIsObservedAndNeverAssumed(t *testing.T) {
	t.Parallel()

	for _, answer := range []string{"no answer", "answered and unreadable"} {
		t.Run(answer, func(t *testing.T) {
			t.Parallel()
			f, withdrawer, _ := queuedMergeFixture(t)
			withdrawer.answer = func(call int) error {
				if call == 1 && answer == "no answer" {
					return orchestratortest.ConnectionReset("disable auto-merge")
				}
				if call == 1 {
					// The forge withdraws, and then cannot be read to confirm it.
					f.forge.DropQueuedMerge()
					f.forge.StateErr = errors.New("the forge did not answer")
				}
				return nil
			}
			uncertain, err := f.withdraw()
			if err != nil || uncertain.Withdrawn || uncertain.Unresolved == "" {
				t.Fatalf("Withdraw() = %#v, %v; want it left standing as unresolved", uncertain, err)
			}
			attempt, _ := f.landing().Current()
			if attempt.Withdrawal == nil || attempt.Withdrawal.Settled != nil || len(attempt.Withdrawal.Answers) != 1 || attempt.Withdrawal.Answers[0].Rejected {
				t.Fatalf("withdrawal = %#v, want it unsettled with one answer that did not say", attempt.Withdrawal)
			}
			f.assertStillArmed()
			if recovered, err := f.recover(); err != nil || recovered.Handback != nil {
				t.Fatalf("Recover() while the withdrawal stands = %#v, %v; want nothing handed back", recovered, err)
			}
			// The forge answers later, and the next call confirms it.
			f.forge.StateErr = nil
			confirmed, err := f.withdraw()
			if err != nil || !confirmed.Withdrawn {
				t.Fatalf("Withdraw() once the forge answers = %#v, %v; want it confirmed", confirmed, err)
			}
			if attempt, _ := f.landing().Current(); attempt.Withdrawal.Settled == nil || len(attempt.Withdrawal.Answers) != 1 {
				t.Fatalf("withdrawal = %#v, want the earlier answer kept and the withdrawal settled", attempt.Withdrawal)
			}
		})
	}
}

func TestARejectedWithdrawalKeepsTheMergeArmedAndHandsNothingBack(t *testing.T) {
	t.Parallel()

	f, withdrawer, _ := queuedMergeFixture(t)
	withdrawer.answer = func(int) error {
		return fmt.Errorf("withdraw the queued merge of pull request 1: %w", publish.ErrForgeAccessRefused)
	}
	for range 2 {
		refused, err := f.withdraw()
		if err != nil || refused.Withdrawn || !strings.Contains(refused.Unresolved, "refused") {
			t.Fatalf("Withdraw() = %#v, %v; want the refusal reported", refused, err)
		}
	}
	attempt, _ := f.landing().Current()
	if w := attempt.Withdrawal; w == nil || w.Settled != nil || len(w.Answers) != 2 || !w.Answers[0].Rejected || !w.Answers[1].Rejected {
		t.Fatalf("withdrawal = %#v, want two rejections kept and nothing settled", w)
	}
	if !f.forge.Queued {
		t.Fatal("the merge stopped being queued though the forge refused to withdraw it")
	}
	f.assertStillArmed()
}

func TestALandingThatRacesTheWithdrawalIsCompletedOnceAndNeverReplayed(t *testing.T) {
	t.Parallel()

	for _, when := range []string{"before the withdrawal", "while it stood unconfirmed"} {
		t.Run(when, func(t *testing.T) {
			t.Parallel()
			f, withdrawer, generation := queuedMergeFixture(t)
			withdrawer.answer = func(call int) error {
				if call == 1 && when == "before the withdrawal" {
					// The forge merged it, and says nothing was armed to withdraw.
					f.forge.PerformQueuedMerge(t)
				}
				if call == 1 && when == "while it stood unconfirmed" {
					return orchestratortest.ConnectionReset("disable auto-merge")
				}
				return nil
			}
			first, err := f.withdraw()
			if when == "while it stood unconfirmed" {
				if err != nil || first.Unresolved == "" {
					t.Fatalf("Withdraw() = %#v, %v; want the withdrawal left standing", first, err)
				}
				f.forge.PerformQueuedMerge(t)
				calls := withdrawer.count()
				first, err = f.withdraw()
				if withdrawer.count() != calls {
					t.Fatal("the forge was asked to withdraw a merge the pull request already showed landed")
				}
			}
			if err != nil || !first.Landed || !first.Completed || first.Withdrawn {
				t.Fatalf("Withdraw() = %#v, %v; want the landing completed", first, err)
			}
			attempt, _ := f.landing().Current()
			if attempt.Withdrawal.Settled == nil || attempt.Withdrawal.Settled.Result != runstate.MergeQueueWithdrawalLanded {
				t.Fatalf("withdrawal = %#v, want it settled as landed", attempt.Withdrawal)
			}
			f.assertLandedOnce(generation)
			if again, err := f.withdraw(); err != nil || !again.Completed || f.run.saveCount() != 1 {
				t.Fatalf("Withdraw() after the landing = %#v, %v; want nothing done twice", again, err)
			}
			if recovered, err := f.recover(); err != nil || recovered.Entry.EntryID != "" {
				t.Fatalf("Recover() after the landing = %#v, %v; want nothing left to recover", recovered, err)
			}
		})
	}
}

func TestAWithdrawalStoppedAroundAnyRequestOrRecordIsFinishedFromWhatItShows(t *testing.T) {
	t.Parallel()

	// Each moment is a record write the process stops at instead of making,
	// and whether the forge had been asked by then.
	moments := map[string]struct {
		stopsAt   func(runstate.MergeQueueWithdrawal, runstate.MergeQueuePromotionAttempt) bool
		forgeDown bool
		asks      int
	}{
		"before the intent is saved": {
			stopsAt: func(w runstate.MergeQueueWithdrawal, _ runstate.MergeQueuePromotionAttempt) bool {
				return w.Settled == nil && len(w.Answers) == 0
			},
			asks: 1,
		},
		"after the intent, before the forge withdrew": {
			stopsAt: func(w runstate.MergeQueueWithdrawal, _ runstate.MergeQueuePromotionAttempt) bool {
				return len(w.Answers) > 0
			},
			forgeDown: true, asks: 2,
		},
		"after the forge withdrew, before its answer is saved": {
			stopsAt: func(w runstate.MergeQueueWithdrawal, a runstate.MergeQueuePromotionAttempt) bool {
				return w.Settled != nil && a.SetAside == nil
			},
			asks: 2,
		},
		"after the settlement, before the attempt is set aside": {
			stopsAt: func(w runstate.MergeQueueWithdrawal, a runstate.MergeQueuePromotionAttempt) bool {
				return a.SetAside != nil
			},
			asks: 1,
		},
	}
	for moment, at := range moments {
		t.Run(moment, func(t *testing.T) {
			t.Parallel()
			f, withdrawer, _ := queuedMergeFixture(t)
			stopped := false
			f.landings.fail = func(landing runstate.MergeQueueLanding) (bool, error) {
				attempt, _ := landing.Current()
				if stopped || attempt.Withdrawal == nil || !at.stopsAt(*attempt.Withdrawal, attempt) {
					return false, nil
				}
				stopped = true
				return false, errStopped
			}
			if at.forgeDown {
				withdrawer.answer = func(call int) error {
					if call == 1 {
						return orchestratortest.ConnectionReset("disable auto-merge")
					}
					return nil
				}
			}
			if _, err := f.withdraw(); !errors.Is(err, errStopped) {
				t.Fatalf("Withdraw() error = %v, want the stop", err)
			}
			if moment == "before the intent is saved" && withdrawer.count() != 0 {
				t.Fatal("the forge was asked to withdraw before the withdrawal was written down")
			}
			f.assertStillArmed()
			// The next call settles it from the record, the pull request, and the
			// forge, asking the forge again only where the record does not say it
			// answered.
			finished, err := f.withdraw()
			if err != nil || !finished.Withdrawn {
				t.Fatalf("Withdraw() after the stop = %#v, %v; want it confirmed", finished, err)
			}
			landing := f.landing()
			attempt, _ := landing.Current()
			if len(landing.Attempts) != 1 || !attempt.Withdrawn() || attempt.SetAside == nil || f.forge.Queued {
				t.Fatalf("landing = %#v, want the one attempt confirmed withdrawn and set aside", landing)
			}
			if withdrawer.count() != at.asks {
				t.Fatalf("the forge was asked to withdraw %d times, want %d", withdrawer.count(), at.asks)
			}
			if len(f.forge.MergeRequests()) != 1 || f.run.saveCount() != 0 {
				t.Fatal("finishing the withdrawal asked for a merge or wrote to the run")
			}
		})
	}
}

// failingFixture is a target whose entry's candidate failed its checks.
func failingFixture(t *testing.T, budget, spent int) (*promotionFixture, *recordingRepair) {
	t.Helper()
	f := newPromotionFixture(t, landLocally)
	f.worker.Pipeline.Config.Checks = []string{"test -f feature.txt", "test -f missing.txt"}
	f.worker.Pipeline.Config.Execution.RepairAttemptsBeforeReplan = budget
	f.run.admittedRun.state.Branch = "change"
	f.run.admittedRun.state.RepairAttempts = spent
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	verification, err := f.work()
	if err != nil || verification.Verified || verification.Refusal == "" {
		t.Fatalf("Work() = %#v, %v; want the candidate refused", verification, err)
	}
	return f, repair
}

func TestADefectiveCandidateIsHandedBackToTheSameRunWithItsEvidenceKept(t *testing.T) {
	t.Parallel()

	f, repair := failingFixture(t, 2, 1)
	failed := f.generations()[0]
	recovered, err := f.recover()
	if err != nil || recovered.Failure.Class != runstate.MergeQueueCandidateDefect || recovered.Failure.Check != "test -f missing.txt" || !recovered.Failure.Attributed {
		t.Fatalf("Recover() = %#v, %v; want the failing check read as the change's defect", recovered, err)
	}
	handback := f.landing().Handback
	if handback == nil || handback.Continuation != runstate.MergeQueueContinueRepair || handback.HandedBackAt == nil ||
		handback.Mover == ownership.MoverOperator || handback.RepairAttempts != 1 || handback.RepairBudget != 2 {
		t.Fatalf("handback = %#v, want a repair given to the run with its own budget", handback)
	}
	if handback.Generation != failed.Number || handback.Binding != failed.Binding() || handback.TargetBase != failed.TargetBase ||
		handback.Candidate != failed.Candidate || handback.Heads[0] != f.head || handback.ApprovedHead != f.head || handback.Branch != "change" {
		t.Fatalf("handback = %#v, want it to name the failed generation and the run's saved change", handback)
	}
	if repair.count() != 1 || repair.entity != f.entry.RunID {
		t.Fatalf("repair given %d times to %s, want once to run %s", repair.count(), repair.entity, f.entry.RunID)
	}
	// The evidence stays where it was earned, and the run is charged nothing
	// here: its repair loop records its own attempt.
	kept := f.generations()[0]
	if kept.CheckRun == nil || len(kept.CheckRun.Results) != len(failed.CheckRun.Results) || kept.Binding() != failed.Binding() {
		t.Fatalf("generation = %#v, want its checks kept", kept)
	}
	if f.run.saveCount() != 0 || f.run.latest().RepairAttempts != 1 {
		t.Fatal("recovery wrote to the run or changed its repair count")
	}
	// The entry's turn is over: nothing builds, promotes, or hands it back again.
	if verification, err := f.work(); err != nil || verification.Entry.EntryID != "" {
		t.Fatalf("Work() after the handback = %#v, %v; want nothing waiting", verification, err)
	}
	if promoted, err := f.promote(); err != nil || promoted.Entry.EntryID != "" {
		t.Fatalf("Promote() after the handback = %#v, %v; want nothing left", promoted, err)
	}
	if again, err := f.recover(); err != nil || again.Entry.EntryID != "" || repair.count() != 1 {
		t.Fatalf("Recover() again = %#v, %v; want nothing done twice", again, err)
	}
}

func TestAReviewersRepairVerdictIsADefectOfTheChange(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally, repairVerdict)
	f.run.admittedRun.state.Branch = "change"
	f.worker.Pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2
	if verification, err := f.work(); err != nil || verification.Verified {
		t.Fatalf("Work() = %#v, %v; want the repair verdict refused", verification, err)
	}
	recovered, err := f.recover()
	if err != nil || recovered.Failure.Class != runstate.MergeQueueCandidateDefect || !strings.Contains(recovered.Failure.Reason, `"repair"`) {
		t.Fatalf("Recover() = %#v, %v; want the reviewer's verdict read as the change's defect", recovered, err)
	}
	if handback := f.landing().Handback; handback == nil || handback.Continuation != runstate.MergeQueueContinueRepair {
		t.Fatalf("handback = %#v, want a repair", handback)
	}
}

func TestAnExhaustedBudgetStaysExhaustedAcrossRestarts(t *testing.T) {
	t.Parallel()

	f, repair := failingFixture(t, 2, 2)
	recovered, err := f.recover()
	if err != nil || recovered.Handback == nil {
		t.Fatalf("Recover() = %#v, %v; want a handback", recovered, err)
	}
	handback := f.landing().Handback
	if handback.Continuation != runstate.MergeQueueBudgetExhausted || handback.Mover != ownership.MoverDevelopmentManager ||
		handback.RepairAttempts != 2 || handback.RepairBudget != 2 || handback.HandedBackAt == nil || handback.Branch != "change" {
		t.Fatalf("handback = %#v, want the budget recorded exhausted and the run handed to the development manager with its work", handback)
	}
	if repair.count() != 1 || repair.given[0].Continuation != runstate.MergeQueueBudgetExhausted || f.run.saveCount() != 0 {
		t.Fatal("an exhausted run was not handed back once as exhausted, or recovery wrote to the run itself")
	}
	// A restarted harness reads the same record: nothing refills the budget,
	// and the decision is not made again on whatever the run says later.
	restarted := f.promoter
	restarted.Repair = &recordingRepair{}
	f.run.admittedRun.state.RepairAttempts = 0
	again, err := restarted.Recover(context.Background(), queueKey)
	if err != nil || again.Entry.EntryID != "" {
		t.Fatalf("Recover() after a restart = %#v, %v; want nothing decided again", again, err)
	}
	if after := f.landing().Handback; after.Continuation != runstate.MergeQueueBudgetExhausted || after.RepairAttempts != 2 {
		t.Fatalf("handback after a restart = %#v, want it still exhausted", after)
	}
	if verification, err := f.work(); err != nil || verification.Entry.EntryID != "" {
		t.Fatalf("Work() after exhaustion = %#v, %v; want the entry not built again", verification, err)
	}
}

func TestAHandbackStoppedBeforeTheRunIsGivenItIsGivenOnce(t *testing.T) {
	t.Parallel()

	f, repair := failingFixture(t, 2, 0)
	repair.fail = 1
	if _, err := f.recover(); err == nil {
		t.Fatal("Recover() = nil, want the unreachable repair loop reported")
	}
	decided := f.landing().Handback
	if decided == nil || decided.HandedBackAt != nil {
		t.Fatalf("handback = %#v, want the decision recorded and not yet given", decided)
	}
	recovered, err := f.recover()
	if err != nil || recovered.Handback == nil {
		t.Fatalf("Recover() after the stop = %#v, %v", recovered, err)
	}
	given := f.landing().Handback
	if given.HandedBackAt == nil || repair.count() != 1 || !given.At.Equal(decided.At) || given.Reason != decided.Reason {
		t.Fatalf("handback = %#v after %d hand-backs, want the same decision given once", given, repair.count())
	}
}

func TestOnlyADefectOfTheChangeIsChargedAndHandedBack(t *testing.T) {
	t.Parallel()

	t.Run("target drift", func(t *testing.T) {
		t.Parallel()
		f := newPromotionFixture(t, landLocally)
		f.promoter.Repair = &recordingRepair{}
		f.verify()
		moveTarget(t, f.repository, "elsewhere.txt")
		if promoted, err := f.promote(); err != nil || !promoted.Drift {
			t.Fatalf("Promote() = %#v, %v; want drift", promoted, err)
		}
		f.assertNotHandedBack(runstate.MergeQueueTargetDrift)
	})
	t.Run("infrastructure", func(t *testing.T) {
		t.Parallel()
		f := newPromotionFixture(t, landLocally)
		f.promoter.Repair = &recordingRepair{}
		f.checks.runner = brokenRunner{}
		if _, err := f.work(); err == nil {
			t.Fatal("Work() with a broken runner = nil, want it reported")
		}
		f.assertNotHandedBack(runstate.MergeQueueInfrastructureFailure)
	})
	t.Run("target failure", func(t *testing.T) {
		t.Parallel()
		f, _ := failingFixture(t, 2, 0)
		filer := &orchestratortest.RecordingFiler{Open: []beads.WorkItem{{
			ID: "yoyodyne-red-7", Status: "open", Notes: "Filed by the harness.\n" + redLandingMarker("main", "test -f missing.txt"),
		}}}
		f.worker.Pipeline.Filer = filer
		recovered := f.assertNotHandedBack(runstate.MergeQueueTargetFailure)
		if recovered.Failure.WaitsOn != "yoyodyne-red-7" || len(filer.Filed) != 0 {
			t.Fatalf("Recover() = %#v, filed %d; want it to wait on the existing item and file nothing", recovered, len(filer.Filed))
		}
	})
	t.Run("unreadable evidence", func(t *testing.T) {
		t.Parallel()
		f, _ := failingFixture(t, 2, 0)
		f.promoter.Queue = unreadableGenerations{flakyLandings: f.landings}
		f.assertNotHandedBack(runstate.MergeQueueUnreadableEvidence)
	})
}

// assertNotHandedBack checks a failure of the class given is reported, and
// charges, withdraws, and hands back nothing.
func (f *promotionFixture) assertNotHandedBack(class runstate.MergeQueueFailureClass) MergeQueueRecovery {
	f.t.Helper()
	recovered, err := f.recover()
	if err != nil || recovered.Failure.Class != class || recovered.Handback != nil || recovered.Failure.Mover == ownership.MoverOperator {
		f.t.Fatalf("Recover() = %#v, %v; want a %s handed back to nobody", recovered, err, class)
	}
	if class.Charged() {
		f.t.Fatalf("%s is charged to the change", class)
	}
	landing, _, err := f.queue.Landing(queueKey, f.entry.EntryID)
	if err != nil || landing.Handback != nil || !landing.Waiting() {
		f.t.Fatalf("landing = %#v, %v; want the entry still waiting", landing, err)
	}
	if f.run.saveCount() != 0 || f.promoter.Repair.(*recordingRepair).count() != 0 {
		f.t.Fatal("a failure that is not the change's wrote to the run or handed it back")
	}
	return recovered
}

func TestAFailureIsNotAttributedToAHeadAnAnnotationNames(t *testing.T) {
	t.Parallel()

	head, other := strings.Repeat("a", 40), strings.Repeat("b", 40)
	configured := runstate.NewMergeQueueCheckConfiguration([]string{"make test"})
	finished := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	generation := runstate.MergeQueueGeneration{
		Number: 1, EntryID: "mqe-" + strings.Repeat("c", 32), EntryOrder: 1, TargetBranch: "main",
		TargetBase: strings.Repeat("d", 40), Heads: []string{head, other}, Candidate: strings.Repeat("e", 40),
		Content: strings.Repeat("f", 40), Checks: configured, AuthorSession: "developer-session", CreatedAt: finished,
	}
	generation.CheckRun = &runstate.MergeQueueCheckEvidence{
		Binding: generation.Binding(), StartedAt: finished, FinishedAt: &finished,
		Results: []runstate.MergeQueueCheckResult{{Command: "make test", ExitCode: 2, Status: "exited"}},
	}
	failure, failed, err := ClassifyMergeQueueFailure(MergeQueueFailureEvidence{
		Generations: []runstate.MergeQueueGeneration{generation}, Configured: configured, SuspectedHeads: []string{other},
	})
	if err != nil || !failed || failure.Class != runstate.MergeQueueCandidateDefect {
		t.Fatalf("ClassifyMergeQueueFailure() = %#v, %v, %v; want a defect of the candidate", failure, failed, err)
	}
	if failure.Attributed || failure.Mover != ownership.MoverDevelopmentManager || !strings.Contains(failure.Reason, "not proof") {
		t.Fatalf("failure = %#v, want it attributed to no head and the annotation recorded as evidence only", failure)
	}

	// The same failure with a check stopped on time judged nothing.
	stopped := generation
	stopped.CheckRun = &runstate.MergeQueueCheckEvidence{
		Binding: generation.Binding(), StartedAt: finished, FinishedAt: &finished, Problem: "make test was timed-out and judged nothing",
		Results: []runstate.MergeQueueCheckResult{{Command: "make test", ExitCode: -1, Status: "timed-out"}},
	}
	if failure, _, _ := ClassifyMergeQueueFailure(MergeQueueFailureEvidence{Generations: []runstate.MergeQueueGeneration{stopped}, Configured: configured}); failure.Class != runstate.MergeQueueInfrastructureFailure {
		t.Fatalf("a check stopped on time classified %s, want infrastructure", failure.Class)
	}
	// Evidence recorded against another candidate says nothing.
	foreign := generation
	foreign.CheckRun = &runstate.MergeQueueCheckEvidence{Binding: strings.Repeat("0", 64), StartedAt: finished, FinishedAt: &finished,
		Results: []runstate.MergeQueueCheckResult{{Command: "make test", ExitCode: 2, Status: "exited"}}}
	if failure, _, _ := ClassifyMergeQueueFailure(MergeQueueFailureEvidence{Generations: []runstate.MergeQueueGeneration{foreign}, Configured: configured}); failure.Class != runstate.MergeQueueUnreadableEvidence {
		t.Fatalf("checks of another candidate classified %s, want unreadable evidence", failure.Class)
	}
}

func TestARunWithoutItsSavedChangeIsKeptForTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	f, repair := failingFixture(t, 2, 0)
	f.run.admittedRun.state.Branch = ""
	if _, err := f.recover(); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	handback := f.landing().Handback
	if handback == nil || handback.Continuation != runstate.MergeQueueMissingPrerequisite || handback.Mover != ownership.MoverDevelopmentManager || repair.count() != 0 {
		t.Fatalf("handback = %#v, want the missing branch recorded for the development manager", handback)
	}
}

// brokenRunner is a check runner that fails before judging anything.
type brokenRunner struct{}

func (brokenRunner) Run(context.Context, checks.Request, func(execution.Event) error) ([]checks.Result, uint64, error) {
	return nil, 0, errors.New("the check runner could not start")
}

// unreadableGenerations is the queue's records with the generations record
// unreadable.
type unreadableGenerations struct {
	*flakyLandings
}

func (unreadableGenerations) Generations(runstate.MergeQueueKey, string) ([]runstate.MergeQueueGeneration, error) {
	return nil, errors.New("the generations record is truncated")
}

func TestTheRunHandbackBlocksTheSameRunOnceAndChargesNothing(t *testing.T) {
	t.Parallel()

	f, _ := failingFixture(t, 2, 1)
	// The run as the queue finds it: it ended succeeded once its change was
	// approved, and keeps its branch, checkout, and session.
	f.run.admittedRun.state.Status = runstate.StatusSucceeded
	f.run.admittedRun.state.ProductID = "yoyodyne"
	f.run.admittedRun.state.TargetBranch = "main"
	f.run.admittedRun.state.WorktreePath = f.repository
	f.run.admittedRun.state.BaseCommit = f.local("main")
	store := f.run.admittedRun.StateStore.(*runstate.Store)
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	docketer := &Docketer{Docket: docket, Runs: latestRun{f.run}, Decisions: store.Triage(), Reruns: store.Reruns()}
	handback := MergeQueueRunHandback{Store: f.run, Tracker: f.tracker, Docket: docketer}
	f.promoter.Repair = handback
	recovered, err := f.recover()
	if err != nil || recovered.Handback == nil || recovered.Handback.Continuation != runstate.MergeQueueContinueRepair {
		t.Fatalf("Recover() = %#v, %v; want a repair handed back", recovered, err)
	}
	if f.landing().Handback.HandedBackAt == nil {
		t.Fatal("the hand-back to the run was not written down")
	}
	saved := f.run.latest()
	if f.run.saveCount() != 1 || saved.RunID != f.entry.RunID || saved.Branch != "change" || saved.ProviderSessionID != "developer-session" {
		t.Fatalf("run saved %d times as %#v, want the same run with its branch and session saved once", f.run.saveCount(), saved)
	}
	if saved.CheckFailure == nil || saved.CheckFailure.Command != "test -f missing.txt" || saved.Blocker == "" {
		t.Fatalf("run = %#v, want the failing check and a blocker recorded for its repair", saved)
	}
	if saved.RepairAttempts != 1 || saved.RepairBudget(f.worker.Pipeline.Config.Execution.RepairAttemptsBeforeReplan) != 2 || saved.GrantedRepairAttempts() != 0 {
		t.Fatalf("run repair attempts %d of %d, want them as they stood", saved.RepairAttempts, saved.RepairBudget(2))
	}
	if !strings.Contains(strings.Join(f.tracker.Calls, " "), "block") {
		t.Fatalf("tracker calls %v, want the item blocked on the hand-back", f.tracker.Calls)
	}
	// The run is a stopped run now, docketed for the development manager the
	// way the repair continuation takes a stoppage up: failed, with its
	// failing check returned to its developer.
	if saved.Status != runstate.StatusFailed || !saved.HandedBack() {
		t.Fatalf("run status %s, handed back %v; want a failed run whose failure was returned for repair", saved.Status, saved.HandedBack())
	}
	if err := continuableRepair(saved, triage.Found{BranchThere: true, WorktreeThere: true}); err != nil {
		t.Fatalf("the repair continuation refuses the handed-back run: %v", err)
	}
	entries, err := docket.List()
	if err != nil || len(entries) != 1 || entries[0].RunID != f.entry.RunID || entries[0].WorkItemID != f.entry.WorkItemID {
		t.Fatalf("docket = %#v, %v; want the same run docketed once", entries, err)
	}
	// Asked again for the same candidate — a process that stopped after the
	// hand-back and before writing it down — it makes nothing twice.
	calls := len(f.tracker.Calls)
	if err := handback.HandBack(context.Background(), f.entry, *f.landing().Handback); err != nil {
		t.Fatalf("HandBack() again = %v", err)
	}
	if f.run.saveCount() != 1 || len(f.tracker.Calls) != calls {
		t.Fatal("handing the same candidate back again wrote to the run or the item again")
	}
	if again, err := docket.List(); err != nil || len(again) != 1 {
		t.Fatalf("docket after a second hand-back = %#v, %v; want one entry", again, err)
	}
}

// latestRun is the docket's view of the fixture's one run as it stands.
type latestRun struct{ run *recordingRun }

func (l latestRun) Recorded() ([]runstate.State, error) {
	return []runstate.State{l.run.latest()}, nil
}

func TestAWithdrawalConfirmedByAPromotionIsReleasedByTheNextRecovery(t *testing.T) {
	t.Parallel()

	f, withdrawer, _ := queuedMergeFixture(t)
	withdrawer.answer = func(call int) error {
		if call == 1 {
			return orchestratortest.ConnectionReset("disable auto-merge")
		}
		return nil
	}
	if standing, err := f.withdraw(); err != nil || standing.Unresolved == "" {
		t.Fatalf("Withdraw() = %#v, %v; want the withdrawal left standing", standing, err)
	}
	// The promotion finishes the standing withdrawal, and cannot tell what it
	// was for, so it records nothing past the confirmation.
	confirmed, err := f.promote()
	if err != nil || !confirmed.Withdrawn || f.landing().Handback != nil {
		t.Fatalf("Promote() = %#v, %v; want the withdrawal confirmed and nothing decided", confirmed, err)
	}
	// The verified candidate has not failed, so recovery releases the entry
	// rather than leaving it at the head of the queue.
	recovered, err := f.recover()
	released := f.landing().Handback
	if err != nil || recovered.Handback == nil || released == nil || released.Continuation != runstate.MergeQueueReleased || released.Mover != ownership.MoverHarness {
		t.Fatalf("Recover() = %#v, %v, handback %#v; want the entry released to the harness", recovered, err, released)
	}
	if after, err := f.promote(); err != nil || after.Entry.EntryID != "" || len(f.forge.MergeRequests()) != 1 {
		t.Fatalf("Promote() after the release = %#v, %v; want nothing left and nothing asked again", after, err)
	}
}
