package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The decision a development manager records when the forge dropped a merge for
// a cause that has since passed.
const rearmReasoning = "the required check that held this merge was wedged on the runner and has since passed on a re-run of it; nothing about the change is in question and the request needs making again"

// The promoted commit and the merge method the run's own merge recorded. The
// method matters: what a re-arm repeats is the request the reviewer's verdict
// authorized, so it is read off the record rather than chosen by the action.
const (
	rearmedCommit = "cccccccccccccccccccccccccccccccccccccccc"
	rearmedBase   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rearmedMethod = "merge"
)

// rearmCaps are the harness defaults a re-arm decision is recorded against:
// triage acts alone once, per publication. They are spent where the decision is
// recorded rather than read by the action, which is why the Rearmer carries no
// cap of its own.
var rearmCaps = runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}

// droppedPublication is a finished run whose promotion landed locally and whose
// queued merge the forge then dropped: the state settleDroppedMerge leaves and
// the one a publication entry is docketed from.
func droppedPublication() runstate.State {
	completed := docketedNow.Add(-3 * time.Hour)
	return runstate.State{
		SchemaVersion:         runstate.StateSchemaVersion,
		RunID:                 docketedRunID,
		ProductID:             "yoyodyne",
		RepositoryID:          "yoyodyne",
		WorkItemID:            docketedItem,
		WorkItemTitle:         docketedTitle,
		Backend:               "claude-code",
		Status:                runstate.StatusFailed,
		Phase:                 runstate.PhaseIntegrating,
		StartedAt:             completed.Add(-time.Hour),
		UpdatedAt:             completed,
		CompletedAt:           &completed,
		WorktreePath:          "/state/worktrees/task",
		Branch:                "yoyodyne/task/abc",
		BaseCommit:            rearmedBase,
		TargetBranch:          "main",
		ProviderSessionID:     "session-developer",
		ProviderModel:         "opus",
		ProviderResolvedModel: "claude-opus-5",
		ReviewSessionID:       "session-reviewer",
		ReviewModel:           "opus",
		ReviewResolvedModel:   "claude-opus-5",
		ReviewDecision:        runstate.ReviewApprove,
		ReviewRounds:          1,
		Integration: &runstate.Integration{
			TargetBranch:         "main",
			SourceCommit:         rearmedCommit,
			TargetCommit:         rearmedCommit,
			PreviousTargetCommit: rearmedBase,
		},
		PullRequest: &runstate.PullRequest{
			Remote:      "origin",
			Branch:      "yoyodyne/task/abc",
			Number:      92,
			URL:         "https://forge.invalid/pull/92",
			HeadCommit:  rearmedCommit,
			State:       "OPEN",
			MergeMethod: rearmedMethod,
		},
		PublishFailure: "the forge dropped the queued merge of pull request 92",
		Blocker:        "Yoyodyne stopped this item: the forge dropped the queued merge.",
	}
}

// leasedRuns is the run store with the promotion lease watched. A re-arm is an
// integration retry against the target branch, so it has to queue where every
// promotion into that branch queues; the lease itself is the real one, and this
// records which branch it was taken for.
type leasedRuns struct {
	*runstate.Store
	promoted []string
}

func (r *leasedRuns) LeasePromotion(ctx context.Context, targetBranch string) (*runstate.Lease, error) {
	r.promoted = append(r.promoted, targetBranch)
	return r.Store.LeasePromotion(ctx, targetBranch)
}

// rearmHarness is the durable state a re-arm acts on, held together so a test
// can drive one decision without rebuilding three stores and a forge.
type rearmHarness struct {
	docket    *memoryDocket
	runs      *runstate.Store
	leases    *leasedRuns
	forge     *orchestratortest.RearmForge
	worktrees *orchestratortest.RemoteTarget
	state     runstate.State
}

func (h *rearmHarness) rearmer() Rearmer {
	return Rearmer{
		Docket:    h.docket,
		Runs:      h.leases,
		Forge:     h.forge,
		Worktrees: h.worktrees,
		Decisions: h.runs.Triage(),
		Clock:     docketClock{},
	}
}

// publication is the key this run's publication is decided and counted under.
func (h *rearmHarness) publication() string {
	return triage.PublicationKey(h.state.RunID, h.state.PullRequest.Number)
}

// decide is what the development manager's triage does when it decides a re-arm:
// it spends that publication's re-arm budget before anything acts on it.
func (h *rearmHarness) decide(t *testing.T) {
	t.Helper()
	if _, err := h.runs.Triage().RecordMergeRearm(context.Background(), h.state.WorkItemID, h.publication(),
		triageDecided(runstate.TriageDecisionRearm, h.state.RunID), docketedNow, rearmCaps); err != nil {
		t.Fatalf("RecordMergeRearm() error = %v", err)
	}
}

// reload is the run's record as it stands on disk, which is where the count of
// what has actually been repeated lives.
func (h *rearmHarness) reload(t *testing.T) runstate.State {
	t.Helper()
	state, err := h.runs.Load(h.state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

// newRearmHarness records one run whose queued merge was dropped, dockets its
// publication, and leaves the forge holding the request open with nothing unmet.
func newRearmHarness(t *testing.T) *rearmHarness {
	t.Helper()
	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	state := droppedPublication()
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := runs.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	docket := &memoryDocket{}
	harness := &rearmHarness{
		docket: docket,
		runs:   runs,
		leases: &leasedRuns{Store: runs},
		forge: &orchestratortest.RearmForge{
			Observed: publish.PullRequest{
				Number:     state.PullRequest.Number,
				URL:        state.PullRequest.URL,
				State:      "OPEN",
				HeadCommit: rearmedCommit,
			},
			// Nothing is holding the request back any more, which is what a drop
			// whose cause was transient looks like once it has passed.
			Status: "CLEAN",
			Result: publish.MergeResult{Queued: true},
		},
		worktrees: &orchestratortest.RemoteTarget{},
		state:     state,
	}
	entry, err := docketerOverStore(docket, runs, rearmConfig()).publicationEntry(state, docketedNow)
	if err != nil {
		t.Fatalf("publicationEntry() error = %v", err)
	}
	if _, err := docket.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	return harness
}

func rearmConfig() config.Config {
	return config.Config{Triage: docketedTriage}
}

// The whole of what a re-arm promises: the request it repeats is the one the
// reviewer's verdict authorized. Same pull request, the method that verdict's own
// merge recorded rather than one this action picked, pinned to the commit that
// was integrated — and nothing overridden to get there.
func TestARearmRepeatsTheIdenticalAuthorizedRequest(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	harness.decide(t)

	result, err := harness.rearmer().Rearm(context.Background(),
		RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
	if err != nil {
		t.Fatalf("Rearm() error = %v", err)
	}
	if len(harness.forge.Requested) != 1 {
		t.Fatalf("merge requests = %#v, want the one repeat", harness.forge.Requested)
	}
	repeated := harness.forge.Requested[0]
	want := publish.MergeRequest{
		Number:     harness.state.PullRequest.Number,
		HeadCommit: rearmedCommit,
		Method:     publish.MergeMethod(rearmedMethod),
	}
	if repeated != want {
		t.Fatalf("repeated request = %#v, want the identical authorized one %#v", repeated, want)
	}
	// The pre-merge check the original gate ran, made again against the same
	// promotion rather than a second rendering of the question.
	if len(harness.worktrees.Verified) != 1 || harness.worktrees.Verified[0].SourceCommit != rearmedCommit {
		t.Fatalf("remote target checks = %#v, want the promotion checked once before the merge", harness.worktrees.Verified)
	}
	if !result.Rearmed || !result.Queued || result.Rearms != 1 {
		t.Fatalf("result = %+v, want one queued re-arm recorded", result)
	}
	// A re-arm is an integration retry against the target branch, so it queues
	// where every promotion into that branch queues rather than racing one.
	if len(harness.leases.promoted) != 1 || harness.leases.promoted[0] != "main" {
		t.Fatalf("promotion leases taken = %v, want the target branch's, once", harness.leases.promoted)
	}
	// The run goes back where reconciliation settles it, and stops saying the
	// forge dropped a merge it is now holding.
	settled := harness.reload(t)
	if !settled.PullRequest.MergeQueued || settled.PullRequest.MergeRearms != 1 {
		t.Fatalf("recorded publication = %+v, want the queued merge and its one re-arm", settled.PullRequest)
	}
	if settled.PublishFailure != "" {
		t.Fatalf("the run still reports a dropped merge the forge is holding again: %q", settled.PublishFailure)
	}
	// And the run's blocker goes with it: it said the same thing, and left standing
	// it would have `yoyo status` describing a publication that needs a person while
	// the forge is holding the merge again.
	if settled.Blocker != "" {
		t.Fatalf("the run still carries the blocker for a dropped merge the forge is holding again: %q", settled.Blocker)
	}
	if !settled.Outstanding() {
		t.Fatal("the re-armed run is not outstanding, so reconciliation will never settle what the forge does next")
	}

	// A forge that merges on the spot rather than queuing leaves the run in the
	// same place, and has to: finishing a publication is the settle path's work —
	// the merge commit, the consumed branch, the local target — and this action
	// has no worktree to do any of it with.
	immediate := newRearmHarness(t)
	immediate.decide(t)
	immediate.forge.Result = publish.MergeResult{}
	merged, err := immediate.rearmer().Rearm(context.Background(),
		RearmRequest{Run: immediate.state.RunID, Reason: rearmReasoning})
	if err != nil {
		t.Fatalf("Rearm() over a forge that merged on the spot error = %v", err)
	}
	if !merged.Rearmed || merged.Queued {
		t.Fatalf("result = %+v, want the merge reported as performed rather than queued", merged)
	}
	if left := immediate.reload(t); !left.Outstanding() || left.PublishFailure != "" {
		t.Fatalf("a merge the forge performed left the run %+v, want it outstanding for the sweep that finishes it", left.PullRequest)
	}
	// The reason names the decision it verified rather than only the prose it was
	// handed, so a re-arm nobody can account for is not a thing this can produce.
	for _, want := range []string{
		"the development manager's triage decided a re-arm of publication", harness.publication(),
		// Cited to where the record says it was made, rather than asserted.
		fmt.Sprintf("in conversation %s after turn %d", decidedIn, decidedOnTurn),
		rearmReasoning[:40],
	} {
		if !strings.Contains(result.Reason, want) {
			t.Fatalf("recorded reason %q is missing %q", result.Reason, want)
		}
	}
}

// The harness does not merge past a requirement — not with administrator
// privileges and not by asking again — so a drop the forge still holds on
// something a person has to supply is refused, and refused before anything is
// spent.
func TestARearmIsRefusedWhenOnlyAPersonCanSatisfyWhatIsUnmet(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"BLOCKED", "DIRTY", "BEHIND", "DRAFT", ""} {
		t.Run("merge state "+status, func(t *testing.T) {
			t.Parallel()

			harness := newRearmHarness(t)
			harness.decide(t)
			harness.forge.Status = status

			result, err := harness.rearmer().Rearm(context.Background(),
				RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
			if err == nil || !strings.Contains(err.Error(), "only a person can satisfy") {
				t.Fatalf("Rearm() error = %v, want it refused for what a person has to supply", err)
			}
			if result.Rearmed || len(harness.forge.Requested) != 0 {
				t.Fatalf("a refused re-arm asked the forge for %#v", harness.forge.Requested)
			}
			// The merge state is read under the promotion lease rather than in front
			// of it, because the check it gates — the remote target's — is evidence a
			// promotion admitted in between would invalidate. So the lease is taken
			// and given straight back, and the refusal costs the publication nothing.
			if len(harness.leases.promoted) != 1 || harness.leases.promoted[0] != "main" {
				t.Fatalf("promotion leases taken = %v, want the target branch held while the forge was asked", harness.leases.promoted)
			}
			// Refused before anything is spent, so the publication keeps the re-arm
			// its decision bought and asking again once the requirement is met
			// carries out the same decision.
			if made := harness.reload(t).PullRequest.MergeRearms; made != 0 {
				t.Fatalf("a refused re-arm spent %d of the publication's budget", made)
			}
		})
	}
}

// A merge state nothing could be read of refuses too. This is a gate, and the
// safe answer for a gate is no.
func TestARearmIsRefusedWhenTheForgeCannotSayWhatIsUnmet(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	harness.decide(t)
	harness.forge.StatusErr = errors.New("the forge answered with an error")

	_, err := harness.rearmer().Rearm(context.Background(),
		RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
	if err == nil || !strings.Contains(err.Error(), "nothing can say the state of") {
		t.Fatalf("Rearm() error = %v, want a request nothing can state refused", err)
	}
	if len(harness.forge.Requested) != 0 {
		t.Fatalf("a refused re-arm asked the forge for %#v", harness.forge.Requested)
	}
}

// One re-arm per publication, and a second drop is an escalation rather than
// another re-arm. The decision is spent by the development manager; what says it
// has been acted on is the publication's own durable counter.
func TestARearmIsRefusedPastOnePerPublication(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	harness.decide(t)
	if _, err := harness.rearmer().Rearm(context.Background(),
		RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning}); err != nil {
		t.Fatalf("the first Rearm() error = %v", err)
	}
	// The forge dropped it again, so the run is back where it was and the
	// question is whether one decision buys a second repeat.
	dropped := harness.reload(t)
	dropped.PullRequest.MergeQueued = false
	dropped.PublishFailure = droppedMerge(*dropped.PullRequest, "open", "main", "The forge gave no reason for the drop.")
	if err := harness.runs.Save(dropped); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// The blocker a second drop leaves says what it is rather than reading like
	// the first one, because it is the thing the development manager decides on.
	for _, want := range []string{"second drop of this publication", "escalation rather than something to re-arm again"} {
		if !strings.Contains(dropped.PublishFailure, want) {
			t.Fatalf("the second drop reads as the first: %q", dropped.PublishFailure)
		}
	}

	_, err := harness.rearmer().Rearm(context.Background(),
		RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
	if err == nil || !strings.Contains(err.Error(), "escalation rather than another re-arm") {
		t.Fatalf("a second Rearm() error = %v, want it refused past the publication's one", err)
	}
	if len(harness.forge.Requested) != 1 {
		t.Fatalf("merge requests = %d, want only the first repeat", len(harness.forge.Requested))
	}
	// And a second decision is refused where it would be recorded, which is the
	// other half of the same bound.
	_, err = harness.runs.Triage().RecordMergeRearm(context.Background(),
		harness.state.WorkItemID, harness.publication(), triageDecided(runstate.TriageDecisionRearm, harness.state.RunID), docketedNow, rearmCaps)
	if !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Fatalf("a second decision error = %v, want the publication's cap refusing it", err)
	}
}

// The budget is the publication's rather than the item's, which is the defect
// this action inherited: a decision recorded about one publication authorized a
// re-arm of a later one, and a later publication's own first re-arm was refused
// for what the first had cost.
func TestARearmDecisionBelongsToThePublicationItNames(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	// Decided about some other publication of the same item, which is not a
	// decision about this one.
	if _, err := harness.runs.Triage().RecordMergeRearm(context.Background(),
		harness.state.WorkItemID, triage.PublicationKey(harness.state.RunID, 7), triageDecided(runstate.TriageDecisionRearm, harness.state.RunID), docketedNow, rearmCaps); err != nil {
		t.Fatalf("RecordMergeRearm() error = %v", err)
	}
	_, err := harness.rearmer().Rearm(context.Background(),
		RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
	if err == nil || !strings.Contains(err.Error(), "no re-arm of publication "+harness.publication()) {
		t.Fatalf("Rearm() error = %v, want no decision of this publication's found", err)
	}
	if len(harness.forge.Requested) != 0 {
		t.Fatalf("a re-arm on another publication's decision asked the forge for %#v", harness.forge.Requested)
	}
	// And spending one publication's budget leaves every other publication's
	// untouched, which is the other direction of the same defect.
	counters, err := harness.runs.Triage().Counters(harness.state.WorkItemID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.RearmsOf(harness.publication()) != 0 || counters.RearmsOf(triage.PublicationKey(harness.state.RunID, 7)) != 1 {
		t.Fatalf("counters = %+v, want the spend on the publication it was recorded against", counters.RearmedPublications)
	}
}

// A re-arm carried out on nobody's decision would ask a forge for a merge no
// recorded decision stands behind.
func TestARearmIsRefusedWithNoDecisionRecorded(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	_, err := harness.rearmer().Rearm(context.Background(),
		RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
	if err == nil || !strings.Contains(err.Error(), "triage has recorded no re-arm") {
		t.Fatalf("Rearm() error = %v, want it refused for want of a decision", err)
	}
	if len(harness.forge.Requested) != 0 {
		t.Fatalf("an undecided re-arm asked the forge for %#v", harness.forge.Requested)
	}
	// Everything answerable from the harness's own records refuses in front of
	// both leases, so the ordinary refusal holds up no promotion at all.
	if len(harness.leases.promoted) != 0 {
		t.Fatalf("a re-arm refused from the harness's own records took the promotion lease for %v", harness.leases.promoted)
	}
}

// One decision stands per stopped run, and a later one supersedes the earlier.
// A re-arm the development manager recorded and then decided to escalate
// instead leaves the publication's counter spent and the standing decision an
// escalation, and a re-arm carried out on that counter would be carried out on
// a decision nobody holds any more.
func TestARearmIsRefusedWhereTheStandingDecisionIsNoLongerARearm(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	harness.decide(t)
	if _, err := harness.runs.Triage().RecordDecision(context.Background(), harness.state.WorkItemID,
		triageDecided(runstate.TriageDecisionEscalate, harness.state.RunID), docketedNow.Add(time.Minute)); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	_, err := harness.rearmer().Rearm(context.Background(),
		RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
	if err == nil || !strings.Contains(err.Error(), `is "escalate" rather than a re-arm`) {
		t.Fatalf("Rearm() error = %v, want it refused on the superseding decision", err)
	}
	if len(harness.forge.Requested) != 0 {
		t.Fatalf("a re-arm on a superseded decision asked the forge for %#v", harness.forge.Requested)
	}
	if left := harness.reload(t); left.PullRequest.MergeRearms != 0 || left.PublishFailure == "" {
		t.Fatalf("a refused re-arm changed the run's record: %+v", left)
	}
}

// The precondition the live incident of 2026-08-19 bought: an agent re-armed a
// publication whose run was still alive, the forge merged an earlier promotion
// mid-run, and the run's republish then failed against a request it could no
// longer publish into. A run in flight for the item is the same mistake by the
// other door.
func TestARearmIsRefusedWhileTheWorkIsStillLive(t *testing.T) {
	t.Parallel()

	t.Run("the run that made the publication is not terminally recorded", func(t *testing.T) {
		t.Parallel()

		harness := newRearmHarness(t)
		harness.decide(t)
		live := harness.reload(t)
		live.Status = runstate.StatusRunning
		live.CompletedAt = nil
		live.Blocker = ""
		if err := harness.runs.Save(live); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		_, err := harness.rearmer().Rearm(context.Background(),
			RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
		if err == nil || !strings.Contains(err.Error(), "rather than ended") {
			t.Fatalf("Rearm() error = %v, want a live run's publication refused", err)
		}
		if len(harness.forge.Requested) != 0 {
			t.Fatalf("a re-arm against a live run asked the forge for %#v", harness.forge.Requested)
		}
	})

	t.Run("another run of the item is in flight", func(t *testing.T) {
		t.Parallel()

		harness := newRearmHarness(t)
		harness.decide(t)
		beside := droppedPublication()
		beside.RunID = "run-11112222333344445555666677778888"
		beside.Status = runstate.StatusRunning
		beside.Phase = runstate.PhaseDeveloping
		beside.CompletedAt = nil
		beside.Blocker = ""
		beside.Integration = nil
		beside.PullRequest = nil
		beside.PublishFailure = ""
		if err := harness.runs.Create(beside); err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		_, err := harness.rearmer().Rearm(context.Background(),
			RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
		if err == nil || !strings.Contains(err.Error(), "in flight") {
			t.Fatalf("Rearm() error = %v, want an item with a live run refused", err)
		}
		if len(harness.forge.Requested) != 0 {
			t.Fatalf("a re-arm beside a live run asked the forge for %#v", harness.forge.Requested)
		}
	})
}

// The counter is written before the request is made, which is the direction
// every triage counter fails in: a process that dies between the two has
// recorded a re-arm it did not make rather than made one it did not record.
func TestARearmRecordsItselfBeforeItAsksTheForge(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	harness.decide(t)
	harness.forge.MergeErr = errors.New("the forge could not be reached")

	_, err := harness.rearmer().Rearm(context.Background(),
		RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
	if err == nil || !strings.Contains(err.Error(), "the re-arm is spent and recorded") {
		t.Fatalf("Rearm() error = %v, want the failed repeat reported as spent", err)
	}
	if made := harness.reload(t).PullRequest.MergeRearms; made != 1 {
		t.Fatalf("the publication records %d re-arm(s) after a request that failed, want the one it spent", made)
	}
}

// A remote target that moved under the promotion fails the same pre-merge check
// the original gate made, and a re-arm past it would ask a forge to reconcile a
// movement nobody in the run saw.
func TestARearmIsRefusedWhenTheRemoteTargetNoLongerPasses(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	harness.decide(t)
	harness.worktrees.Failure = fmt.Errorf("%w: main is somewhere else", gitworktree.ErrRemoteTargetDrift)

	_, err := harness.rearmer().Rearm(context.Background(),
		RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
	if err == nil || !errors.Is(err, gitworktree.ErrRemoteTargetDrift) {
		t.Fatalf("Rearm() error = %v, want the drifted target refused", err)
	}
	if len(harness.forge.Requested) != 0 {
		t.Fatalf("a re-arm over a drifted target asked the forge for %#v", harness.forge.Requested)
	}
	if made := harness.reload(t).PullRequest.MergeRearms; made != 0 {
		t.Fatalf("a refused re-arm spent %d of the publication's budget", made)
	}
}

// What the forge merges is the request's head, so a request that moved is not
// the one the verdict authorized and its merge is not one to repeat.
func TestARearmIsRefusedWhenTheRequestIsNoLongerTheOneAuthorized(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		observed publish.PullRequest
		want     string
	}{
		{
			name:     "the head moved on the forge",
			observed: publish.PullRequest{Number: 92, State: "OPEN", HeadCommit: strings.Repeat("d", 40)},
			want:     "not the one the verdict authorized",
		},
		{
			name:     "the forge merged it after all",
			observed: publish.PullRequest{Number: 92, State: "MERGED", Merged: true, HeadCommit: rearmedCommit},
			want:     "there is no dropped merge to repeat",
		},
		{
			name:     "the forge is holding a merge again",
			observed: publish.PullRequest{Number: 92, State: "OPEN", AutoMerge: true, HeadCommit: rearmedCommit},
			want:     "nothing is dropped and there is nothing to repeat",
		},
		{
			name:     "the branch carries some other request",
			observed: publish.PullRequest{Number: 93, State: "OPEN", HeadCommit: rearmedCommit},
			want:     "not the request the verdict authorized",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := newRearmHarness(t)
			harness.decide(t)
			harness.forge.Observed = test.observed

			_, err := harness.rearmer().Rearm(context.Background(),
				RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Rearm() error = %v, want %q", err, test.want)
			}
			if len(harness.forge.Requested) != 0 {
				t.Fatalf("a refused re-arm asked the forge for %#v", harness.forge.Requested)
			}
		})
	}
}

// A re-arm records the development manager's reasoning as why the request was
// repeated, and a publication nothing docketed is a re-arm nothing bounds.
func TestARearmRefusesWhatItCannotAccountFor(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	harness.decide(t)
	if _, err := harness.rearmer().Rearm(context.Background(),
		RearmRequest{Run: harness.state.RunID}); err == nil || !strings.Contains(err.Error(), "reasoning") {
		t.Fatalf("Rearm() with no reasoning error = %v, want it refused", err)
	}

	undocketed := newRearmHarness(t)
	undocketed.docket.entries = nil
	undocketed.decide(t)
	if _, err := undocketed.rearmer().Rearm(context.Background(),
		RearmRequest{Run: undocketed.state.RunID, Reason: rearmReasoning}); err == nil ||
		!strings.Contains(err.Error(), "is on the triage docket") {
		t.Fatalf("Rearm() of an undocketed publication error = %v, want it refused", err)
	}
}
