package orchestrator

// Giving a spent re-run claim back, on the development manager's decision.
//
// A re-run decision is carried out once: the harness claims it against the
// stoppage before the fresh run starts, and a claim counts as spent whatever
// the fresh run came to (runstate.RerunStore.Claimed). The harness gives a claim
// back by itself only where it can prove the fresh run bought nothing — refused
// before it was reserved, refused by the environment before any agent of it
// ran, or ended before it took the work item (Rerunner's withdraw paths). A
// fresh run that died after taking the item, in preparing its worktree, with
// nothing classifying the cause, is not
// provable that way, so its claim stays spent and the item's re-run decision
// reads as carried out by a run that did nothing.
//
// Whether such a claim goes back is a judgement, and it is the development
// manager's: she reads the fresh run's docket entry, which says the claim it
// spent and what spending it cost, and records "give-back-claim" or
// "leave-claim-spent" in her conversation (chat's claimdecision.go). The record
// is written onto the claim itself. What this file holds is the carry-out: the
// watching session gives back every claim she decided to give back at its next
// pull, and `yoyo triage give-back <run>` does the same now. Nothing under the
// state root is edited by hand, and the claim's own record says who gave it back,
// on whose decision, and why.
//
// Giving a claim back starts nothing. It makes her earlier re-run decision
// outstanding again, and the carry-out pass fires that re-run through every gate
// it already asks.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DecisionGiveBackClaim is the carry-out of the development manager's decision
// to give a spent re-run claim back. It is recorded on the claim rather than on
// the item's triage record, so it is never written there as a decision or a
// refusal.
const DecisionGiveBackClaim = "give-back-claim"

// ClaimGiverClaims is the re-run claims a give-back reads and writes. It is
// satisfied by *runstate.RerunStore.
type ClaimGiverClaims interface {
	SpentOn(runID string) (runstate.Rerun, bool, error)
	List() ([]runstate.Rerun, error)
	GiveBack(ctx context.Context, docketKey, by string, at time.Time) (runstate.Rerun, bool, error)
}

// ClaimGiverRuns is the run state a give-back reads to prove the fresh run the
// claim was spent on has ended. It is satisfied by *runstate.Store.
type ClaimGiverRuns interface {
	Load(runID string) (runstate.State, error)
	Incomplete() ([]runstate.State, error)
	Latest(workItemID string) (runstate.State, error)
}

// ClaimGiverNotes writes the give-back onto the work item, where anybody reading
// the item finds it. It is satisfied by beads.Client.
type ClaimGiverNotes interface {
	RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error)
}

// ClaimGiver carries out the development manager's decisions to give a spent
// re-run claim back. It decides nothing: a claim with no give-back decision
// standing is refused by the record itself.
type ClaimGiver struct {
	Claims ClaimGiverClaims
	Runs   ClaimGiverRuns
	// Notes is optional; a give-back made without it is still recorded on the
	// claim, and says it could not write the note.
	Notes ClaimGiverNotes
	Clock execution.Clock
}

// ClaimGiveBackResult is what one give-back came to.
type ClaimGiveBackResult struct {
	WorkItemID string `json:"work_item_id"`
	PriorRunID string `json:"prior_run_id"`
	// RunID is the fresh run the claim had been spent on, empty where none was
	// recorded.
	RunID     string `json:"run_id,omitempty"`
	DocketKey string `json:"docket_key"`
	// GivenBack reports this call giving the claim back; Already reports it
	// having been given back before, which is the decision already carried out.
	GivenBack bool           `json:"given_back"`
	Already   bool           `json:"already,omitempty"`
	Claim     runstate.Rerun `json:"claim"`
	// Note is what was written onto the work item, and NoteProblem why it could
	// not be: the give-back stands on the claim's record either way.
	Note        string `json:"note,omitempty"`
	NoteProblem string `json:"note_problem,omitempty"`
}

// GiveBack carries out the standing decision about the claim spent on run —
// the fresh run the claim started, or the stopped run it was taken for where no
// fresh run was recorded. by is who is carrying it out, recorded on the claim.
func (g ClaimGiver) GiveBack(ctx context.Context, run, by string) (ClaimGiveBackResult, error) {
	if g.Claims == nil || g.Runs == nil {
		return ClaimGiveBackResult{}, errors.New("giving a re-run claim back requires the claims and the run state")
	}
	run = strings.TrimSpace(run)
	if !runstate.ValidRunID(run) {
		return ClaimGiveBackResult{}, fmt.Errorf("%q is not a run identifier; a give-back names the run the spent claim started, or the stopped run it was taken for", run)
	}
	claim, found, err := g.Claims.SpentOn(run)
	if err != nil {
		return ClaimGiveBackResult{}, fmt.Errorf("read the re-run claims: %w", err)
	}
	if !found {
		return ClaimGiveBackResult{}, fmt.Errorf("no spent re-run claim names run %s, as the fresh run it started or as the stopped run it was taken for; one already given back is not spent", run)
	}
	return g.giveBack(ctx, claim, by)
}

// GiveBackDecided carries out every standing decision to give a claim back,
// oldest claim first, and reports each. A claim whose give-back is refused is
// reported with why and the rest are still carried out.
func (g ClaimGiver) GiveBackDecided(ctx context.Context, by string) ([]ClaimGiveBackResult, error) {
	if g.Claims == nil || g.Runs == nil {
		return nil, errors.New("giving a re-run claim back requires the claims and the run state")
	}
	recorded, err := g.Claims.List()
	if err != nil {
		return nil, fmt.Errorf("read the re-run claims: %w", err)
	}
	var results []ClaimGiveBackResult
	var problems []error
	for _, claim := range recorded {
		if !claim.GiveBackOutstanding() {
			continue
		}
		result, err := g.giveBack(ctx, claim, by)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		results = append(results, result)
	}
	return results, errors.Join(problems...)
}

func (g ClaimGiver) giveBack(ctx context.Context, claim runstate.Rerun, by string) (ClaimGiveBackResult, error) {
	result := ClaimGiveBackResult{
		WorkItemID: claim.WorkItemID,
		PriorRunID: claim.PriorRunID,
		RunID:      claim.RunID,
		DocketKey:  claim.DocketKey,
		Claim:      claim,
	}
	if err := g.spentRunEnded(claim); err != nil {
		return result, err
	}
	given, gave, err := g.Claims.GiveBack(ctx, claim.DocketKey, by, g.now())
	if err != nil {
		return result, err
	}
	result.Claim = given
	if !gave {
		result.Already = true
		return result, nil
	}
	result.GivenBack = true
	result.Note = fmt.Sprintf(
		"Re-run claim given back: %s. The re-run decided about the stoppage of run %s is outstanding again, and the harness carries it out at its next pull under every gate a re-run already asks.",
		given.GivenBack.Says(), given.PriorRunID)
	if g.Notes == nil {
		result.NoteProblem = "no tracker was wired to write the give-back onto the item; it is recorded on the claim"
		return result, nil
	}
	if _, err := g.Notes.RecordOutcome(ctx, given.WorkItemID, result.Note); err != nil {
		result.NoteProblem = fmt.Sprintf("the give-back is recorded on the claim, and the note saying so could not be written onto %s: %v", given.WorkItemID, err)
	}
	return result, nil
}

// spentRunEnded refuses to give back a claim whose fresh run may still be
// going: a claim is given back for a run that bought nothing, and a run in
// flight may yet buy something. A claim naming no fresh run is held to no run
// of its item being in flight, because the run it started may not have been
// recorded on it yet.
func (g ClaimGiver) spentRunEnded(claim runstate.Rerun) error {
	if run := strings.TrimSpace(claim.RunID); run != "" {
		state, err := g.Runs.Load(run)
		if err != nil {
			return fmt.Errorf("read run %s, which the claim was spent on: %w", run, err)
		}
		if !state.Status.Terminal() {
			return fmt.Errorf("run %s, which the re-run claim of %s was spent on, is recorded as %s rather than ended, so its claim is not given back while it may still buy something; the decision stands and is carried out once it ends", run, claim.WorkItemID, state.Status)
		}
		// The claim goes back so the stopped work is run again. An item already run
		// again since, by some other run, would be started a second time beside it.
		latest, err := g.Runs.Latest(claim.WorkItemID)
		if err != nil {
			return fmt.Errorf("read the latest run of %s: %w", claim.WorkItemID, err)
		}
		if latest.RunID != run && latest.StartedAt.After(state.StartedAt) {
			return fmt.Errorf("%s has been run again since run %s, which the re-run claim was spent on, by run %s, so giving the claim back would start the item a second time; the decision is not carried out, and the development manager decides about run %s instead", claim.WorkItemID, run, latest.RunID, latest.RunID)
		}
		return nil
	}
	incomplete, err := g.Runs.Incomplete()
	if err != nil {
		return fmt.Errorf("read what is in flight: %w", err)
	}
	for _, state := range incomplete {
		if state.WorkItemID == claim.WorkItemID {
			return fmt.Errorf("the re-run claim of %s names no fresh run and run %s of the item is in flight, so the claim may be that run's and is not given back; the decision stands and is carried out once it ends", claim.WorkItemID, state.RunID)
		}
	}
	return nil
}

func (g ClaimGiver) now() time.Time {
	if g.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return g.Clock.Now().UTC()
}

// CarryClaimGiveBacks gives back every claim the development manager decided to
// give back, and reports each the way a carried-out decision is reported. It is
// fired on every pull, outside the developer slots, because a give-back starts
// nothing: the re-run it makes outstanding again is fired by the pull like any
// other.
func (c CarryOut) CarryClaimGiveBacks(ctx context.Context) ([]CarriedOut, error) {
	if c.ClaimGiver == nil {
		return nil, nil
	}
	results, err := c.ClaimGiver.GiveBackDecided(ctx, "the harness's watching session")
	carried := make([]CarriedOut, 0, len(results))
	for _, result := range results {
		if !result.GivenBack {
			continue
		}
		carried = append(carried, CarriedOut{
			WorkItemID:    result.WorkItemID,
			RunID:         result.RunID,
			DocketKey:     result.DocketKey,
			Decision:      DecisionGiveBackClaim,
			Carried:       true,
			Reason:        result.Note,
			RecordProblem: result.NoteProblem,
		})
	}
	return carried, err
}
