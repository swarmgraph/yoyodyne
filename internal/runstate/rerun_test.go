package runstate

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const rerunDocketKey = "stopped_run:run-0123456789abcdef0123456789abcdef"

func newRerunStore(t *testing.T) *RerunStore {
	t.Helper()
	store, err := NewRerunStore(t.TempDir(), domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	return store
}

func claimedRerun() Rerun {
	return Rerun{
		DocketKey:  rerunDocketKey,
		PriorRunID: "run-0123456789abcdef0123456789abcdef",
		WorkItemID: "yoyodyne-ifd.102.6",
		Reason:     "the development manager triaged this stoppage as a re-run: the ground moved under a correct change",
		ClaimedAt:  time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		Preserved: PreservedArtifacts{
			Branch:       "yoyodyne/task/abc",
			WorktreePath: "/state/worktrees/task",
			Disposition:  PreservedKept,
		},
	}
}

// The bound this record exists for. One docketed stoppage is run again once,
// whoever asks and however much budget the item has left.
func TestOneStoppageIsClaimedOnce(t *testing.T) {
	t.Parallel()

	store := newRerunStore(t)
	if _, err := store.Claim(context.Background(), claimedRerun()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	_, err := store.Claim(context.Background(), claimedRerun())
	if !errors.Is(err, ErrRerunTaken) {
		t.Fatalf("second Claim() error = %v, want the re-run refused", err)
	}
	var taken RerunTakenError
	if !errors.As(err, &taken) || taken.Existing.WorkItemID != "yoyodyne-ifd.102.6" {
		t.Fatalf("refusal = %#v, want it to carry the re-run that already exists", err)
	}
}

// Two processes claiming one stoppage at the same instant are one claim and one
// refusal, because the read and the write are under the stoppage's own lock.
func TestConcurrentClaimsOfOneStoppageLeaveOneRerun(t *testing.T) {
	t.Parallel()

	store := newRerunStore(t)
	var group sync.WaitGroup
	claimed := make([]error, 4)
	for index := range claimed {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, claimed[index] = store.Claim(context.Background(), claimedRerun())
		}(index)
	}
	group.Wait()
	granted := 0
	for _, err := range claimed {
		switch {
		case err == nil:
			granted++
		case errors.Is(err, ErrRerunTaken):
		default:
			t.Fatalf("Claim() error = %v, want either the claim or the refusal", err)
		}
	}
	if granted != 1 {
		t.Fatalf("claims granted = %d, want exactly one", granted)
	}
}

// What the stopped run preserved outlives the process that claimed the re-run,
// which is the whole reason it is written down rather than reported.
func TestARerunsDispositionSurvivesTheProcessThatRecordedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first, err := NewRerunStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	if _, err := first.Claim(context.Background(), claimedRerun()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	retired := time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC)
	if _, err := first.Settle(context.Background(), rerunDocketKey, "run-fedcba9876543210fedcba9876543210", PreservedArtifacts{
		Branch:       "yoyodyne/task/abc",
		WorktreePath: "/state/worktrees/task",
		Disposition:  PreservedRetired,
		RetiredAt:    &retired,
	}); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}

	later, err := NewRerunStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	recorded, found, err := later.Find(rerunDocketKey)
	if err != nil || !found {
		t.Fatalf("Find() = %t, error = %v", found, err)
	}
	if recorded.RunID != "run-fedcba9876543210fedcba9876543210" {
		t.Fatalf("run = %q, want the fresh run the re-run started", recorded.RunID)
	}
	if recorded.Preserved.Disposition != PreservedRetired || recorded.Preserved.RetiredAt == nil {
		t.Fatalf("preserved = %#v, want a dated retirement", recorded.Preserved)
	}
	if !strings.Contains(recorded.Render(), "yoyodyne/task/abc") {
		t.Fatalf("Render() = %q, want the artifacts named", recorded.Render())
	}
}

// What has been claimed for one item is what says a decision about it has been
// acted on, so it is read back by item and counts every claim whatever became of
// the run it caused.
func TestTheClaimsOfOneItemAreReadBackFromEveryStoppage(t *testing.T) {
	t.Parallel()

	store := newRerunStore(t)
	second := claimedRerun()
	second.DocketKey = "stopped_run:run-fedcba9876543210fedcba9876543210"
	second.PriorRunID = "run-fedcba9876543210fedcba9876543210"
	second.ClaimedAt = second.ClaimedAt.Add(time.Hour)
	other := claimedRerun()
	other.DocketKey = "stopped_run:run-11112222333344445555666677778888"
	other.PriorRunID = "run-11112222333344445555666677778888"
	other.WorkItemID = "yoyodyne-ifd.119"
	for _, rerun := range []Rerun{claimedRerun(), second, other} {
		if _, err := store.Claim(context.Background(), rerun); err != nil {
			t.Fatalf("Claim() error = %v", err)
		}
	}

	claimed, err := store.Claimed("yoyodyne-ifd.102.6")
	if err != nil {
		t.Fatalf("Claimed() error = %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("Claimed() = %d re-run(s), want both stoppages of that item", len(claimed))
	}
	// Oldest first, so a reader following the record reads the item's history in
	// the order it happened.
	if claimed[0].DocketKey != rerunDocketKey || claimed[1].DocketKey != second.DocketKey {
		t.Fatalf("Claimed() order = %q, %q", claimed[0].DocketKey, claimed[1].DocketKey)
	}
	// Another item's claims are its own: nothing here bounds one item by what
	// triage did about another.
	elsewhere, err := store.Claimed("yoyodyne-ifd.119")
	if err != nil || len(elsewhere) != 1 {
		t.Fatalf("Claimed() = %d re-run(s), error = %v, want the other item's own", len(elsewhere), err)
	}
	all, err := store.List()
	if err != nil || len(all) != 3 {
		t.Fatalf("List() = %d re-run(s), error = %v, want every record", len(all), err)
	}
	// The lock file each record is guarded by sits in the same directory and is
	// not a record; a listing that read it would fail on every product that has
	// ever claimed one.
	if _, err := store.Claimed("yoyodyne-ifd.102.6"); err != nil {
		t.Fatalf("Claimed() error = %v after the locks were written", err)
	}
}

// An item nothing has been re-run about has no claims, which is the ordinary
// answer rather than a failure to look.
func TestAnItemWithNoRerunsHasNoClaims(t *testing.T) {
	t.Parallel()

	claimed, err := newRerunStore(t).Claimed("yoyodyne-ifd.102.6")
	if err != nil || len(claimed) != 0 {
		t.Fatalf("Claimed() = %#v, error = %v, want nothing", claimed, err)
	}
}

// A stoppage nothing has been recorded about is an absence rather than a
// failure to look: that is every stoppage before somebody decides about it.
func TestAnUnclaimedStoppageIsAnAbsence(t *testing.T) {
	t.Parallel()

	recorded, found, err := newRerunStore(t).Find(rerunDocketKey)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if found {
		t.Fatalf("Find() found %#v on a store nothing has claimed in", recorded)
	}
}

// Settling a stoppage nothing claimed is refused rather than creating a record:
// a disposition with no claim behind it would describe a re-run nobody took.
func TestSettlingAnUnclaimedStoppageIsRefused(t *testing.T) {
	t.Parallel()

	_, err := newRerunStore(t).Settle(context.Background(), rerunDocketKey, "run-fedcba9876543210fedcba9876543210",
		PreservedArtifacts{Disposition: PreservedGone})
	if err == nil || !strings.Contains(err.Error(), "nothing to settle") {
		t.Fatalf("Settle() error = %v, want a refusal", err)
	}
}

// A claim taken for a run that never existed is given back, so the stoppage
// keeps the re-run its decision authorized. What makes that sound is that the
// re-run really is unspent: the record carries no run, and the same decision is
// claimable again afterwards.
func TestAClaimWhoseRunNeverExistedIsGivenBack(t *testing.T) {
	t.Parallel()

	store := newRerunStore(t)
	if _, err := store.Claim(context.Background(), claimedRerun()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if err := store.Withdraw(context.Background(), rerunDocketKey); err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if recorded, found, err := store.Find(rerunDocketKey); err != nil || found {
		t.Fatalf("Find() = %#v, found = %t, error = %v, want the claim given back", recorded, found, err)
	}
	if _, err := store.Claim(context.Background(), claimedRerun()); err != nil {
		t.Fatalf("Claim() after the withdrawal error = %v, want the same decision claimable", err)
	}
}

// A claim with a run on it was acted on, whatever became of that run, so it is
// not one to give back: withdrawing it would be the second re-run of one
// stoppage that this record exists to refuse.
func TestAClaimThatStartedARunIsNotGivenBack(t *testing.T) {
	t.Parallel()

	store := newRerunStore(t)
	if _, err := store.Claim(context.Background(), claimedRerun()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := store.Settle(context.Background(), rerunDocketKey, "run-fedcba9876543210fedcba9876543210",
		PreservedArtifacts{Disposition: PreservedKept, Branch: "yoyodyne/task/abc"}); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	err := store.Withdraw(context.Background(), rerunDocketKey)
	if err == nil || !strings.Contains(err.Error(), "run-fedcba9876543210fedcba9876543210") {
		t.Fatalf("Withdraw() error = %v, want a refusal naming the run the claim started", err)
	}
	if _, found, _ := store.Find(rerunDocketKey); !found {
		t.Fatal("the claim of a stoppage that was run again was removed")
	}
}

// Withdrawing a claim nothing took leaves exactly what it would have left, so a
// caller giving one back need not first find out whether it exists.
func TestWithdrawingAnUnclaimedStoppageIsNotAnError(t *testing.T) {
	t.Parallel()

	if err := newRerunStore(t).Withdraw(context.Background(), rerunDocketKey); err != nil {
		t.Fatalf("Withdraw() error = %v, want an absence to be what it already is", err)
	}
}

// The contract each record is held to. Every one of these describes a re-run
// somebody could misread as accounted for.
func TestARerunIsValidatedAgainstWhatItHasToSay(t *testing.T) {
	t.Parallel()

	for _, invalid := range []struct {
		name   string
		change func(*Rerun)
		want   string
	}{
		{"with no reasoning", func(r *Rerun) { r.Reason = "" }, "triage decision"},
		{"with no stoppage", func(r *Rerun) { r.DocketKey = "" }, "docket key"},
		{"with no prior run", func(r *Rerun) { r.PriorRunID = "" }, "prior run"},
		{"with an unknown disposition", func(r *Rerun) { r.Preserved.Disposition = "abandoned" }, "disposition"},
		{"with an undated retirement", func(r *Rerun) { r.Preserved.Disposition = PreservedRetired }, "when it was retired"},
		{
			"with a retirement it did not make",
			func(r *Rerun) {
				retired := time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC)
				r.Preserved.RetiredAt = &retired
			},
			"was not retired",
		},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			t.Parallel()

			rerun := claimedRerun()
			rerun.SchemaVersion = RerunSchemaVersion
			rerun.ProductID = "yoyodyne"
			rerun.UpdatedAt = rerun.ClaimedAt
			invalid.change(&rerun)
			err := rerun.Validate()
			if err == nil || !strings.Contains(err.Error(), invalid.want) {
				t.Fatalf("Validate() error = %v, want one naming %q", err, invalid.want)
			}
		})
	}
}

// The store belongs to one product, and the run store reaches its own without
// being told the state root a second time.
func TestTheRerunStoreIsTheRunStoresOwnProduct(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, err := NewStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	beside, err := NewRerunStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	if runs.Reruns().Root() != beside.Root() {
		t.Fatalf("Reruns().Root() = %q, want %q", runs.Reruns().Root(), beside.Root())
	}
	if _, err := runs.Reruns().Claim(context.Background(), claimedRerun()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, found, err := beside.Find(rerunDocketKey); err != nil || !found {
		t.Fatalf("Find() = %t, error = %v, want the claim the run store's own record took", found, err)
	}
}

func giveBackDecision() RerunClaimDecision {
	return RerunClaimDecision{
		Decision:     RerunClaimGiveBack,
		Reason:       "the fresh run died at its tracker claim and bought nothing",
		DecidedBy:    "development manager",
		Conversation: "chat-0123456789abcdef",
		Turn:         7,
		DecidedAt:    time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC),
	}
}

// A claim spent on a fresh run is given back only on the development manager's
// decision, and once given back it is no longer counted as spent, so the decision
// that took it may be carried out again. The record keeps who gave it back and
// why, through the claim taken again after it.
func TestASpentClaimIsGivenBackOnlyOnTheDevelopmentManagersDecision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newRerunStore(t)
	if _, err := store.Claim(ctx, claimedRerun()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	const fresh = "run-fedcba9876543210fedcba9876543210"
	if _, err := store.Settle(ctx, rerunDocketKey, fresh, PreservedArtifacts{Disposition: PreservedKept}); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if err := store.Withdraw(ctx, rerunDocketKey); err == nil || !strings.Contains(err.Error(), "yoyo triage give-back") {
		t.Fatalf("Withdraw() error = %v, want the harness's own give-back refused naming the decision that applies", err)
	}
	if _, _, err := store.GiveBack(ctx, rerunDocketKey, "the harness's watching session", time.Now()); err == nil {
		t.Fatal("GiveBack() with no decision standing gave the claim back")
	}
	spent, found, err := store.SpentOn(fresh)
	if err != nil || !found || spent.DocketKey != rerunDocketKey {
		t.Fatalf("SpentOn(%s) = %#v, %v, %v, want the claim spent on it", fresh, spent, found, err)
	}
	if _, err := store.DecideClaim(ctx, rerunDocketKey, giveBackDecision()); err != nil {
		t.Fatalf("DecideClaim() error = %v", err)
	}
	if claimed, err := store.Claimed("yoyodyne-ifd.102.6"); err != nil || len(claimed) != 1 {
		t.Fatalf("Claimed() = %d, %v before the give-back is carried out, want the claim still spent", len(claimed), err)
	}

	at := time.Date(2026, 8, 20, 9, 5, 0, 0, time.UTC)
	given, gave, err := store.GiveBack(ctx, rerunDocketKey, "the harness's watching session", at)
	if err != nil || !gave {
		t.Fatalf("GiveBack() = %v, %v, want it given back", gave, err)
	}
	if given.GivenBack == nil || given.GivenBack.By != "the harness's watching session" || given.GivenBack.RunID != fresh ||
		given.GivenBack.Decision.Reason != giveBackDecision().Reason || given.ClaimDecision != nil {
		t.Fatalf("given back = %#v, want who carried it out, the run it was spent on, and the decision with its reason", given.GivenBack)
	}
	if says := given.GivenBack.Says(); !strings.Contains(says, "chat-0123456789abcdef") || !strings.Contains(says, "bought nothing") {
		t.Fatalf("Says() = %q, want the decision cited with its reason", says)
	}
	if _, again, err := store.GiveBack(ctx, rerunDocketKey, "someone", at); err != nil || again {
		t.Fatalf("second GiveBack() = %v, %v, want the decision already carried out", again, err)
	}
	if claimed, err := store.Claimed("yoyodyne-ifd.102.6"); err != nil || len(claimed) != 0 {
		t.Fatalf("Claimed() = %d, %v, want a claim given back not counted", len(claimed), err)
	}
	if _, found, _ := store.SpentOn(fresh); found {
		t.Fatal("SpentOn() still finds a claim given back")
	}
	if _, err := store.DecideClaim(ctx, rerunDocketKey, giveBackDecision()); err == nil {
		t.Fatal("DecideClaim() decided again about a claim already given back")
	}

	// The stoppage may be claimed again, and the earlier give-back stays on it.
	again := claimedRerun()
	again.ClaimedAt = at.Add(time.Minute)
	if _, err := store.Claim(ctx, again); err != nil {
		t.Fatalf("Claim() after the give-back error = %v", err)
	}
	reclaimed, _, err := store.Find(rerunDocketKey)
	if err != nil || reclaimed.GivenBack != nil || len(reclaimed.EarlierGivenBack) != 1 || reclaimed.EarlierGivenBack[0].By != "the harness's watching session" {
		t.Fatalf("re-claimed record = %#v, %v, want the earlier give-back kept", reclaimed, err)
	}
	// Withdrawing that claim, which started nothing, restores the given-back
	// record rather than erasing it.
	if err := store.Withdraw(ctx, rerunDocketKey); err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	restored, found, err := store.Find(rerunDocketKey)
	if err != nil || !found || restored.GivenBack == nil || restored.GivenBack.RunID != fresh || len(restored.EarlierGivenBack) != 0 {
		t.Fatalf("withdrawn record = %#v, %v, %v, want the give-back it replaced", restored, found, err)
	}
}

// Leaving a claim spent records that she looked, and gives nothing back.
func TestAClaimLeftSpentStaysSpent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newRerunStore(t)
	if _, err := store.Claim(ctx, claimedRerun()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	decision := giveBackDecision()
	decision.Decision = RerunClaimLeaveSpent
	decided, err := store.DecideClaim(ctx, rerunDocketKey, decision)
	if err != nil || decided.ClaimDecision == nil || decided.GiveBackOutstanding() {
		t.Fatalf("DecideClaim() = %#v, %v, want the decision recorded and nothing to give back", decided.ClaimDecision, err)
	}
	if _, _, err := store.GiveBack(ctx, rerunDocketKey, "someone", time.Now()); err == nil || !strings.Contains(err.Error(), RerunClaimLeaveSpent) {
		t.Fatalf("GiveBack() error = %v, want it refused naming the decision standing", err)
	}
	if claimed, _ := store.Claimed("yoyodyne-ifd.102.6"); len(claimed) != 1 {
		t.Fatalf("Claimed() = %d, want the claim still spent", len(claimed))
	}
}
