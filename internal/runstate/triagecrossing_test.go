package runstate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The week the delegation was argued from, replayed as the harness now takes it.
// Every one of those overrides was a development manager refused past a cap, an
// escalation, an operator granting it within minutes, and a decision recorded
// afterwards. The operator step was latency rather than judgement, so the
// crossing is his: the same refusal, the same cap crossed by one, and the same
// decision recorded — with nobody waited on and the argument on the record.
func TestTheDevelopmentManagerCrossesTheCapTheOperatorWasBeingWokenFor(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.143"
	spendRounds(t, store, item, 0, 4)

	// Where every one of those ceremonies started: the decision refused, by the
	// round budget, at the cap the project configured.
	var refusal TriageCapError
	if _, err := store.RecordRerun(ctx, item, triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), overrideCaps); !errors.As(err, &refusal) {
		t.Fatalf("RecordRerun() at 4 of 4 rounds error = %v, want a cap refusal", err)
	}
	rounds, refused := refusal.RefusedBy(TriageReviewRoundBudget)
	if !refused {
		t.Fatalf("RecordRerun() was refused by %v, want the review round budget", refusal.Budgets())
	}

	crossed := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	crossing, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageReviewRoundBudget,
		"the change was right and the ground moved under it; the rounds went on a base that has since landed", crossed, overrideCaps)
	if err != nil {
		t.Fatalf("CrossCap() error = %v", err)
	}
	// One step, which is exactly what the refusal said would permit the decision:
	// an unbounded raise is not delegated and is not what this gives.
	if crossing.Cap != rounds.Permits() || crossing.Cap != overrideCaps.ReviewRounds+1 {
		t.Fatalf("CrossCap() raised the cap to %d, want the %d the refusal said would permit it", crossing.Cap, rounds.Permits())
	}
	if crossing.Number != 1 || crossing.Bound != MaxDelegatedCapCrossings {
		t.Fatalf("CrossCap() = crossing %d of %d, want the first of %d", crossing.Number, crossing.Bound, MaxDelegatedCapCrossings)
	}

	// The reason and the role are on the item's own record, which is what makes
	// the crossing answerable after the channel message has scrolled away.
	recorded, found := crossing.Counters.OverrideOf(TriageReviewRoundBudget)
	if !found {
		t.Fatalf("counters carry no override of the review round budget: %+v", crossing.Counters.Overrides)
	}
	if recorded.CrossedBy != domain.RoleDevelopmentManager || !recorded.Delegated() {
		t.Fatalf("the recorded crossing was crossed by %q, want the development manager", recorded.CrossedBy)
	}
	if !strings.Contains(recorded.Reason, "the ground moved under it") {
		t.Fatalf("the recorded crossing carries the reason %q", recorded.Reason)
	}
	if !recorded.DecidedAt.Equal(crossed) {
		t.Fatalf("the recorded crossing is dated %s, want %s", recorded.DecidedAt, crossed)
	}
	if described := recorded.Describe(); !strings.Contains(described, "on delegated authority") {
		t.Fatalf("the crossing describes itself as %q, which does not say whose authority it was", described)
	}

	// And the decision the ceremony existed for is now recordable, by the same
	// call that was refused a moment ago.
	if _, err := store.RecordRerun(ctx, item, triageDecided(TriageDecisionRerun, decidedRunID), crossed, overrideCaps); err != nil {
		t.Fatalf("RecordRerun() past the crossing error = %v, want the decision the crossing permits", err)
	}
}

// Five is the delegation and the sixth is the escalation. The refusal names the
// Lead Product Manager rather than only saying no, because past the development
// manager's five a raise is hers to grant, and it no longer names a command for
// the operator to type.
func TestASixthCrossingOfOneItemIsRefusedAndNamesTheLeadProductManager(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.209.20"
	at := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)

	for crossing := 1; crossing <= MaxDelegatedCapCrossings; crossing++ {
		recorded, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageReviewRoundBudget,
			"the findings are narrow and the change is preserved", at, overrideCaps)
		if err != nil {
			t.Fatalf("CrossCap() %d error = %v", crossing, err)
		}
		if recorded.Number != crossing {
			t.Fatalf("CrossCap() %d reported crossing %d", crossing, recorded.Number)
		}
	}

	var spent TriageCrossingError
	_, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageReviewRoundBudget,
		"one more would settle it", at, overrideCaps)
	if !errors.As(err, &spent) || !errors.Is(err, ErrTriageCrossingsSpent) {
		t.Fatalf("the sixth CrossCap() error = %v, want the delegated bound refusing it", err)
	}
	if !strings.Contains(err.Error(), "Lead Product Manager") || strings.Contains(err.Error(), "yoyo triage override") {
		t.Fatalf("the sixth CrossCap() error does not name the Lead Product Manager as the one deciding, or still names the operator's command:\n%s", err)
	}
	if spent.Crossings != MaxDelegatedCapCrossings || spent.Bound != MaxDelegatedCapCrossings {
		t.Fatalf("the refusal reports %d of %d, want %d of %d", spent.Crossings, spent.Bound, MaxDelegatedCapCrossings, MaxDelegatedCapCrossings)
	}

	// Nothing was recorded, which is what makes a refusal free: the item stands at
	// the five it had, and the cap where the fifth left it.
	counters, err := store.Counters(item)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.DelegatedCrossings() != MaxDelegatedCapCrossings {
		t.Fatalf("the item carries %d crossings after the refusal, want %d", counters.DelegatedCrossings(), MaxDelegatedCapCrossings)
	}

	// And the operator's own hand still crosses it, which is the whole of what the
	// bound hands back to them.
	if _, err := store.Override(ctx, item, TriageOverride{
		Budget:    TriageReviewRoundBudget,
		Cap:       overrideCaps.ReviewRounds + MaxDelegatedCapCrossings + 1,
		DecidedBy: "mason",
		Reason:    "driven by hand until it lands",
	}, at, overrideCaps); err != nil {
		t.Fatalf("Override() past the delegated bound error = %v, want the operator's path still open", err)
	}
}

// A crossing nobody argued for is refused outright rather than recorded weakly.
// The justification is the condition the delegation was granted under: it is what
// lands on the item and what reaches the operator, so a crossing without one is
// the delegation with its condition quietly dropped.
func TestACrossingWithNoJustificationIsRefusedOutright(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.272"
	at := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)

	for _, reason := range []string{"", "   ", "\n\t "} {
		_, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageRepairGrantBudget, reason, at, overrideCaps)
		if !errors.Is(err, ErrTriageCrossingUnjustified) {
			t.Fatalf("CrossCap() with reason %q error = %v, want it refused for want of a justification", reason, err)
		}
	}
	counters, err := store.Counters(item)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if len(counters.Overrides) != 0 {
		t.Fatalf("a refused crossing recorded %+v", counters.Overrides)
	}
}

// The delegation is one role's and one step's. Anything else asking for it is a
// caller reaching for the bounded path through a door that is not there, and the
// store refuses rather than recording something no reader could attribute.
func TestOnlyTheDevelopmentManagerCrossesACapAndOnlyByOneStep(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.279"
	at := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)

	// The Lead Product Manager's raises are granted through GrantCap, never through
	// the development manager's crossing.
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer, domain.RoleProductManager, ""} {
		if _, err := store.CrossCap(ctx, item, role, TriageRerunBudget, "because", at, overrideCaps); err == nil {
			t.Fatalf("CrossCap() as %q was permitted, want only the development manager", role)
		}
	}
	if _, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, "rounds", "because", at, overrideCaps); err == nil {
		t.Fatalf("CrossCap() of a budget nothing bounds was permitted")
	}

	// A delegated crossing arriving through the operator's own verb is refused
	// there too, so the bounded path cannot be taken through the unbounded one.
	if _, err := store.Override(ctx, item, TriageOverride{
		Budget:    TriageRerunBudget,
		Cap:       9,
		DecidedBy: "the development manager",
		Reason:    "because",
		CrossedBy: domain.RoleDevelopmentManager,
	}, at, overrideCaps); err == nil {
		t.Fatalf("Override() carrying a delegated crossing was permitted, want it refused")
	}

	// And a record hand-edited past the bound is refused as it is read, because
	// these are the figures the guards obey.
	crossings := make([]TriageOverride, 0, MaxDelegatedCapCrossings+1)
	for step := 1; step <= MaxDelegatedCapCrossings+1; step++ {
		crossings = append(crossings, TriageOverride{
			Budget:    TriageReviewRoundBudget,
			Cap:       overrideCaps.ReviewRounds + step,
			DecidedBy: domain.RoleDevelopmentManager.Title(),
			DecidedAt: at,
			Reason:    "because",
			CrossedBy: domain.RoleDevelopmentManager,
		})
	}
	problems := errors.Join(validateTriageOverrides(crossings)...)
	if problems == nil || !strings.Contains(problems.Error(), "delegated authority") {
		t.Fatalf("validateTriageOverrides() over %d crossings = %v, want the bound refusing the record", len(crossings), problems)
	}

	// A crossing that names a cleared budget is not a step and is refused as one.
	cleared := TriageOverride{
		Budget:    TriageReviewRoundBudget,
		Cleared:   true,
		DecidedBy: domain.RoleDevelopmentManager.Title(),
		DecidedAt: at,
		Reason:    "because",
		CrossedBy: domain.RoleDevelopmentManager,
	}
	if err := cleared.Validate(); err == nil || !strings.Contains(err.Error(), "never clears one") {
		t.Fatalf("a delegated crossing that clears a budget validated as %v", err)
	}
}

// The item the crossing has to work on is not the one level with its cap. Rounds
// are counted whatever a cap says, so an item that ran and was re-run is past its
// ceiling rather than at it — six of four is the state `yoyo status` documents —
// and a crossing that stepped from the ceiling would move it to five, meet the
// same refusal, and cost another of the five crossings and another message to the
// operator for every round of the gap.
//
// So the crossing lands on the figure the refusal itself quoted: one more than
// the item has spent. One crossing answers one refusal, whatever the arithmetic
// behind it.
func TestACrossingLandsOnTheCeilingTheRefusalSaidWouldPermitTheDecision(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.272"
	// Two past the cap of four, which is one run and a re-run of an item that keeps
	// coming back rather than anything unusual.
	spendRounds(t, store, item, 0, 6)

	var refusal TriageCapError
	if _, err := store.RecordRerun(ctx, item, triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), overrideCaps); !errors.As(err, &refusal) {
		t.Fatalf("RecordRerun() at 6 of 4 rounds error = %v, want a cap refusal", err)
	}
	rounds, refused := refusal.RefusedBy(TriageReviewRoundBudget)
	if !refused || rounds.Spent != 6 || rounds.Permits() != 7 {
		t.Fatalf("the refusal reports %+v, want 6 spent and 7 permitting it", rounds)
	}

	at := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	crossing, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageReviewRoundBudget,
		"the rounds went on a base that had moved, and the work is worth running again", at, overrideCaps)
	if err != nil {
		t.Fatalf("CrossCap() error = %v", err)
	}
	if crossing.Cap != rounds.Permits() {
		t.Fatalf("CrossCap() raised the cap to %d, want the %d the refusal said would permit the decision",
			crossing.Cap, rounds.Permits())
	}

	// And the promise the refusal makes holds: asking for the same decision again
	// records it, on one crossing rather than on three.
	if _, err := store.RecordRerun(ctx, item, triageDecided(TriageDecisionRerun, decidedRunID), at, overrideCaps); err != nil {
		t.Fatalf("RecordRerun() past the crossing error = %v, want the decision the crossing permits", err)
	}
	counters, err := store.Counters(item)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.DelegatedCrossings() != 1 {
		t.Fatalf("one refusal cost %d crossings", counters.DelegatedCrossings())
	}

	// It is still a raise and never a reach backwards: a budget whose configured
	// ceiling is already well past what the item has spent steps from the ceiling,
	// so a crossing can never lower one.
	generous := TriageCaps{ReviewRounds: 40, RepairGrants: 1, Reruns: 1, MergeRearms: 2}
	roomy, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageReviewRoundBudget,
		"driving it by hand for one more round", at, generous)
	if err != nil {
		t.Fatalf("CrossCap() under a generous configured cap error = %v", err)
	}
	if roomy.Cap != generous.ReviewRounds+1 {
		t.Fatalf("CrossCap() under a configured cap of %d raised it to %d, want one past the ceiling",
			generous.ReviewRounds, roomy.Cap)
	}
}

// The merge re-arm is bounded per publication and a crossing names no
// publication, so what it steps from is the publication that has had the most —
// which is the one that refused, whenever any did. Measured from the item's
// total instead, an item with three publications each re-armed once would be
// crossed to four, a ceiling nothing asked for; measured from the wrong
// publication it would land below the refusal and refuse the same decision again.
func TestACrossingOfTheRearmCapStepsFromThePublicationThatRefused(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.143"
	// Three publications, one of them re-armed to its cap of two and the others
	// once: the item total is four, the most any publication has had is two.
	for _, spend := range []struct {
		publication string
		run         string
		rearms      int
	}{
		{"publication:run-a#7", "run-aaaa2222333344445555666677778888", 2},
		{"publication:run-b#8", "run-bbbb2222333344445555666677778888", 1},
		{"publication:run-c#9", "run-cccc2222333344445555666677778888", 1},
	} {
		for range spend.rearms {
			if _, err := store.RecordMergeRearm(ctx, item, spend.publication, triageDecided(TriageDecisionRearm, spend.run), time.Now(), overrideCaps); err != nil {
				t.Fatalf("RecordMergeRearm(%s) error = %v", spend.publication, err)
			}
		}
	}
	var refusal TriageCapError
	if _, err := store.RecordMergeRearm(ctx, item, "publication:run-a#7", triageDecided(TriageDecisionRearm, "run-aaaa2222333344445555666677778888"), time.Now(), overrideCaps); !errors.As(err, &refusal) {
		t.Fatalf("RecordMergeRearm() at 2 of 2 error = %v, want a cap refusal", err)
	}
	rearms, refused := refusal.RefusedBy(TriageMergeRearmBudget)
	if !refused {
		t.Fatalf("RecordMergeRearm() was refused by %v, want the merge re-arm budget", refusal.Budgets())
	}

	crossing, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageMergeRearmBudget,
		"the forge dropped it for a check that never finished, twice", time.Now(), overrideCaps)
	if err != nil {
		t.Fatalf("CrossCap() error = %v", err)
	}
	if crossing.Cap != rearms.Permits() || crossing.Cap != overrideCaps.MergeRearms+1 {
		t.Fatalf("CrossCap() raised the re-arm cap to %d, want the %d the refusal said would permit it", crossing.Cap, rearms.Permits())
	}
	// The publication that refused is permitted one more, and the others are held
	// to the same ceiling rather than to the item's total.
	if _, err := store.RecordMergeRearm(ctx, item, "publication:run-a#7", triageDecided(TriageDecisionRearm, "run-aaaa2222333344445555666677778888"), time.Now(), overrideCaps); err != nil {
		t.Fatalf("RecordMergeRearm() past the crossing error = %v, want the re-arm the crossing permits", err)
	}
	if _, err := store.RecordMergeRearm(ctx, item, "publication:run-a#7", triageDecided(TriageDecisionRearm, "run-aaaa2222333344445555666677778888"), time.Now(), overrideCaps); !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("RecordMergeRearm() a second time past the crossing error = %v, want the crossed cap refusing it", err)
	}
}

// Past the development manager's five, the Lead Product Manager grants up to five
// more, each the same size as one of hers, recorded as her decision with her
// reason; the harness carries it out with nobody typing anything. Ten in all is
// the limit no role may pass, and the refusal says so.
func TestTheLeadProductManagerGrantsUpToFiveRaisesPastTheDevelopmentManagersFive(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.430.38"
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

	// Before the development manager's five are used, a raise is still hers.
	if _, err := store.GrantCap(ctx, item, TriageReviewRoundBudget, "worth more than the work behind it", at, overrideCaps); !errors.Is(err, ErrTriageGrantEarly) {
		t.Fatalf("GrantCap() with none of the development manager's raises used error = %v, want it refused as hers", err)
	}
	for crossing := 1; crossing <= MaxDelegatedCapCrossings; crossing++ {
		if _, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageReviewRoundBudget, "narrow findings", at, overrideCaps); err != nil {
			t.Fatalf("CrossCap() %d error = %v", crossing, err)
		}
	}

	var last TriageCrossing
	for grant := 1; grant <= MaxLeadCapGrants; grant++ {
		granted, err := store.GrantCap(ctx, item, TriageReviewRoundBudget, "the item is worth more than the work waiting behind it", at, overrideCaps)
		if err != nil {
			t.Fatalf("GrantCap() %d error = %v", grant, err)
		}
		if granted.Number != grant || granted.Bound != MaxLeadCapGrants {
			t.Fatalf("GrantCap() %d reported grant %d of %d", grant, granted.Number, granted.Bound)
		}
		// The same size as one of the development manager's: one past what stands.
		if last.Cap != 0 && granted.Cap != last.Cap+1 {
			t.Fatalf("GrantCap() %d raised the cap to %d, want one step past %d", grant, granted.Cap, last.Cap)
		}
		last = granted
	}
	recorded, _ := last.Counters.OverrideOf(TriageReviewRoundBudget)
	if recorded.CrossedBy != domain.RoleProductManager || !recorded.Granted() || recorded.Reason == "" {
		t.Fatalf("the last grant is recorded as %+v, want the Lead Product Manager's decision with her reason", recorded)
	}
	if described := recorded.Describe(); !strings.Contains(described, "granted by the Lead Product Manager") {
		t.Fatalf("the grant describes itself as %q", described)
	}
	if last.Counters.RoleRaises() != MaxRoleCapRaises || last.Counters.LeadGrants() != MaxLeadCapGrants || last.Counters.DelegatedCrossings() != MaxDelegatedCapCrossings {
		t.Fatalf("after ten raises the counts are %d in all, %d granted, %d crossed",
			last.Counters.RoleRaises(), last.Counters.LeadGrants(), last.Counters.DelegatedCrossings())
	}

	// The eleventh is refused whichever role asks, and the refusal says ten is the
	// limit no role may pass.
	for name, raise := range map[string]func() error{
		"GrantCap": func() error {
			_, err := store.GrantCap(ctx, item, TriageReviewRoundBudget, "one more", at, overrideCaps)
			return err
		},
		"CrossCap": func() error {
			_, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageReviewRoundBudget, "one more", at, overrideCaps)
			return err
		},
	} {
		err := raise()
		if !errors.Is(err, ErrTriageRaiseLimit) || !strings.Contains(err.Error(), "ten is the limit no role may pass") {
			t.Fatalf("an eleventh raise through %s error = %v, want the limit of ten refusing it", name, err)
		}
	}
	counters, err := store.Counters(item)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.RoleRaises() != MaxRoleCapRaises {
		t.Fatalf("a refused raise left %d raises recorded, want %d", counters.RoleRaises(), MaxRoleCapRaises)
	}

	// A grant carrying no reason is refused like a crossing carrying none.
	if _, err := newTriageStore(t).GrantCap(ctx, item, TriageReviewRoundBudget, " ", at, overrideCaps); !errors.Is(err, ErrTriageCrossingUnjustified) {
		t.Fatalf("GrantCap() with no reason error = %v, want it refused", err)
	}

	// A record hand-edited past her five is refused as it is read.
	grants := make([]TriageOverride, 0, MaxLeadCapGrants+1)
	for step := 1; step <= MaxLeadCapGrants+1; step++ {
		grants = append(grants, TriageOverride{
			Budget:    TriageReviewRoundBudget,
			Cap:       overrideCaps.ReviewRounds + step,
			DecidedBy: domain.RoleProductManager.Title(),
			DecidedAt: at,
			Reason:    "because",
			CrossedBy: domain.RoleProductManager,
		})
	}
	if problems := errors.Join(validateTriageOverrides(grants)...); problems == nil || !strings.Contains(problems.Error(), "granted by the Lead Product Manager") {
		t.Fatalf("validateTriageOverrides() over %d grants = %v, want her bound refusing the record", len(grants), problems)
	}
}
