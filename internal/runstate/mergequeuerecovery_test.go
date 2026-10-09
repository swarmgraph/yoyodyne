package runstate

// A queued merge's withdrawal and an entry's handback: a withdrawal is written
// before the forge is asked and settles once, an attempt whose merge was asked
// for is set aside only once its withdrawal is confirmed, nothing rewrites the
// change while one stands, and a handback is written once, never names the
// operator, and ends the entry's turn.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

func queuedAttempt(entryID string) MergeQueuePromotionAttempt {
	attempt := intendedAttempt(entryID, 1)
	attempt = requested(attempt, MergeQueuePushCandidate, MergeQueueMutationDone)
	attempt = requested(attempt, MergeQueueOpenPullRequest, MergeQueueMutationDone)
	attempt.PullRequest, attempt.PullRequestURL = 41, "https://example.invalid/pull/41"
	return requested(attempt, MergeQueueRequestMerge, MergeQueueMutationQueued)
}

func withdrawing(attempt MergeQueuePromotionAttempt) MergeQueuePromotionAttempt {
	attempt.Withdrawal = &MergeQueueWithdrawal{
		Key: attempt.MergeQueueWithdrawalKey(), PullRequest: attempt.PullRequest, Pinned: attempt.Candidate,
		Reason: "the candidate failed its checks", IntendedAt: time.Date(2026, 10, 8, 9, 3, 0, 0, time.UTC),
	}
	return attempt
}

func TestAQueuedMergeIsSetAsideOnlyOnceItsWithdrawalIsConfirmed(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	entry := admittedEntry(t, store)
	lease := workerLease(t, store, mainQueue)
	record := func(attempt MergeQueuePromotionAttempt) error {
		landing := NewMergeQueueLanding(entry)
		landing.Attempts = []MergeQueuePromotionAttempt{attempt}
		return store.RecordLanding(lease, mainQueue, landing)
	}
	landingOf := func(attempt MergeQueuePromotionAttempt) MergeQueueLanding {
		landing := NewMergeQueueLanding(entry)
		landing.Attempts = []MergeQueuePromotionAttempt{attempt}
		return landing
	}
	queued := queuedAttempt(entry.EntryID)
	if err := record(queued); err != nil {
		t.Fatalf("RecordLanding(merge queued) = %v", err)
	}
	if landingOf(queued).HeadRewriteRefusal() == "" {
		t.Fatal("a queued merge allows the change's head to be rewritten")
	}
	aside := func(attempt MergeQueuePromotionAttempt) MergeQueuePromotionAttempt {
		attempt.SetAside = &MergeQueueSetAside{At: time.Date(2026, 10, 8, 9, 5, 0, 0, time.UTC), Reason: "its merge was withdrawn"}
		return attempt
	}
	if err := record(aside(queued)); err == nil {
		t.Fatal("RecordLanding(a queued merge set aside with no withdrawal) = nil, want it refused")
	}
	intended := withdrawing(queued)
	if err := record(intended); err != nil {
		t.Fatalf("RecordLanding(withdrawal intended) = %v", err)
	}
	if refusal := landingOf(intended).HeadRewriteRefusal(); !strings.Contains(refusal, "not confirmed") {
		t.Fatalf("HeadRewriteRefusal(withdrawal unsettled) = %q, want it refused as unconfirmed", refusal)
	}
	if err := record(aside(intended)); err == nil {
		t.Fatal("RecordLanding(set aside with its withdrawal unsettled) = nil, want it refused")
	}
	rejected := intended
	rejected.Withdrawal = &MergeQueueWithdrawal{}
	*rejected.Withdrawal = *intended.Withdrawal
	rejected.Withdrawal.Answers = []MergeQueueWithdrawalAnswer{{At: time.Date(2026, 10, 8, 9, 4, 0, 0, time.UTC), Rejected: true, Detail: "the forge refused the harness's token access"}}
	if err := record(rejected); err != nil {
		t.Fatalf("RecordLanding(a rejected withdrawal) = %v", err)
	}
	if err := record(intended); !errors.As(err, new(MergeQueueLandingConflictError)) {
		t.Fatalf("RecordLanding(a withdrawal answer removed) = %v, want a conflict", err)
	}
	retargeted := rejected
	retargeted.Withdrawal = &MergeQueueWithdrawal{}
	*retargeted.Withdrawal = *rejected.Withdrawal
	retargeted.Withdrawal.Reason = "something else"
	if err := record(retargeted); !errors.As(err, new(MergeQueueLandingConflictError)) {
		t.Fatalf("RecordLanding(a withdrawal's reason rewritten) = %v, want a conflict", err)
	}
	confirmed := rejected
	confirmed.Withdrawal = &MergeQueueWithdrawal{}
	*confirmed.Withdrawal = *rejected.Withdrawal
	confirmed.Withdrawal.Settled = &MergeQueueWithdrawalSettlement{Result: MergeQueueWithdrawalConfirmed, At: time.Date(2026, 10, 8, 9, 5, 0, 0, time.UTC)}
	if err := record(aside(confirmed)); err != nil {
		t.Fatalf("RecordLanding(set aside once withdrawn) = %v", err)
	}
	if refusal := landingOf(aside(confirmed)).HeadRewriteRefusal(); refusal != "" {
		t.Fatalf("HeadRewriteRefusal(withdrawn) = %q, want the head free to rewrite", refusal)
	}
	landedInstead := confirmed
	landedInstead.Withdrawal = &MergeQueueWithdrawal{}
	*landedInstead.Withdrawal = *confirmed.Withdrawal
	landedInstead.Withdrawal.Settled = &MergeQueueWithdrawalSettlement{Result: MergeQueueWithdrawalLanded, At: time.Date(2026, 10, 8, 9, 5, 0, 0, time.UTC)}
	if err := record(aside(landedInstead)); err == nil {
		t.Fatal("RecordLanding(a settled withdrawal settled again) = nil, want it refused")
	}
}

func TestAWithdrawalIsWrittenOnlyForAMergeTheForgeHoldsQueued(t *testing.T) {
	t.Parallel()

	entryID := "mqe-" + strings.Repeat("a", 32)
	queued := queuedAttempt(entryID)
	notAsked := withdrawing(queued)
	notAsked.Mutations = notAsked.Mutations[:2]
	if err := notAsked.validate(entryID); err == nil {
		t.Fatal("a withdrawal of a merge never asked for validated")
	}
	moved := intendedAttempt(entryID, 1)
	moved.Path = MergeQueueLandLocallyThenPullRequest
	moved.Key = MergeQueuePromotionKey(entryID, 1, moved.Path, moved.Binding, moved.Candidate)
	moved = requested(moved, MergeQueueMoveTarget, MergeQueueMutationDone)
	moved = requested(moved, MergeQueuePushCandidate, MergeQueueMutationDone)
	moved = requested(moved, MergeQueueOpenPullRequest, MergeQueueMutationDone)
	moved.PullRequest = 41
	moved = requested(moved, MergeQueueRequestMerge, MergeQueueMutationQueued)
	if err := withdrawing(moved).validate(entryID); err == nil || !strings.Contains(err.Error(), "moved the local target") {
		t.Fatalf("a withdrawal after the local target moved validated as %v, want it refused", err)
	}
	other := withdrawing(queued)
	other.Withdrawal.Pinned = strings.Repeat("e", 40)
	if err := other.validate(entryID); err == nil {
		t.Fatal("a withdrawal pinned to another commit validated")
	}
}

func repairHandback() *MergeQueueHandback {
	return &MergeQueueHandback{
		At: time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), Class: MergeQueueCandidateDefect,
		Continuation: MergeQueueContinueRepair, Mover: ownership.MoverOf(domain.RoleDeveloper), Reason: "make test failed on the candidate",
		Generation: 1, Binding: strings.Repeat("b", 64), TargetBase: strings.Repeat("c", 40), Heads: []string{strings.Repeat("f", 40)},
		Candidate: strings.Repeat("d", 40), Branch: "yoyodyne/item/run", ApprovedHead: strings.Repeat("f", 40),
		RepairAttempts: 1, RepairBudget: 2,
	}
}

func TestAHandbackEndsTheEntrysTurnAndIsWrittenOnce(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	entry := admittedEntry(t, store)
	lease := workerLease(t, store, mainQueue)
	queued := queuedAttempt(entry.EntryID)
	handedBack := NewMergeQueueLanding(entry)
	handedBack.Attempts = []MergeQueuePromotionAttempt{queued}
	handedBack.Handback = repairHandback()
	if err := store.RecordLanding(lease, mainQueue, handedBack); err == nil {
		t.Fatal("RecordLanding(handed back with its merge still queued) = nil, want it refused")
	}
	landing := NewMergeQueueLanding(entry)
	landing.Handback = repairHandback()
	if err := store.RecordLanding(lease, mainQueue, landing); err != nil {
		t.Fatalf("RecordLanding(handback) = %v", err)
	}
	if landing.Waiting() {
		t.Fatal("an entry handed back still waits in the queue")
	}
	given := landing
	given.Handback = repairHandback()
	at := time.Date(2026, 10, 8, 10, 1, 0, 0, time.UTC)
	given.Handback.HandedBackAt = &at
	if err := store.RecordLanding(lease, mainQueue, given); err != nil {
		t.Fatalf("RecordLanding(handed to the run) = %v", err)
	}
	for name, revise := range map[string]func(*MergeQueueHandback){
		"more attempts left": func(h *MergeQueueHandback) { h.RepairBudget = 5 },
		"exhausted after all": func(h *MergeQueueHandback) {
			h.Continuation, h.RepairAttempts = MergeQueueBudgetExhausted, 2
		},
		"taken back from the run": func(h *MergeQueueHandback) { h.HandedBackAt = nil },
	} {
		revised := given
		revised.Handback = repairHandback()
		revised.Handback.HandedBackAt = &at
		revise(revised.Handback)
		if err := store.RecordLanding(lease, mainQueue, revised); err == nil {
			t.Errorf("RecordLanding(%s) = nil, want it refused", name)
		}
	}
	withdrawn := given
	withdrawn.Handback = nil
	if err := store.RecordLanding(lease, mainQueue, withdrawn); !errors.As(err, new(MergeQueueLandingConflictError)) {
		t.Fatalf("RecordLanding(handback removed) = %v, want a conflict", err)
	}
}

func TestAHandbackNeverNamesTheOperatorAndReadsTheBudgetAsItStood(t *testing.T) {
	t.Parallel()

	for name, revise := range map[string]func(*MergeQueueHandback){
		"the operator as mover":        func(h *MergeQueueHandback) { h.Mover = ownership.MoverOperator },
		"drift handed back":            func(h *MergeQueueHandback) { h.Class = MergeQueueTargetDrift },
		"infrastructure handed back":   func(h *MergeQueueHandback) { h.Class = MergeQueueInfrastructureFailure },
		"a repair with nothing left":   func(h *MergeQueueHandback) { h.RepairAttempts = 2 },
		"exhausted with attempts left": func(h *MergeQueueHandback) { h.Continuation = MergeQueueBudgetExhausted },
		"an unattributed failure handed to a run": func(h *MergeQueueHandback) {
			h.Continuation = MergeQueueUnattributed
			at := time.Now()
			h.HandedBackAt = &at
		},
		"no failed generation": func(h *MergeQueueHandback) { h.Generation = 0 },
	} {
		handback := repairHandback()
		revise(handback)
		if problems := handback.validate(); len(problems) == 0 {
			t.Errorf("a handback with %s validated", name)
		}
	}
	if problems := repairHandback().validate(); len(problems) != 0 {
		t.Fatalf("a whole handback = %v", problems)
	}
	released := &MergeQueueHandback{
		At: time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), Continuation: MergeQueueReleased, Mover: ownership.MoverHarness,
		Reason: "its queued merge was withdrawn to rewrite its head", ApprovedHead: strings.Repeat("f", 40),
	}
	if problems := released.validate(); len(problems) != 0 {
		t.Fatalf("a whole release = %v", problems)
	}
	for name, revise := range map[string]func(*MergeQueueHandback){
		"a failure class":    func(h *MergeQueueHandback) { h.Class = MergeQueueCandidateDefect },
		"a repair budget":    func(h *MergeQueueHandback) { h.RepairBudget = 2 },
		"handed to its run":  func(h *MergeQueueHandback) { at := time.Now(); h.HandedBackAt = &at },
		"the operator moves": func(h *MergeQueueHandback) { h.Mover = ownership.MoverOperator },
	} {
		revised := *released
		revise(&revised)
		if problems := revised.validate(); len(problems) == 0 {
			t.Errorf("a release with %s validated", name)
		}
	}
}
