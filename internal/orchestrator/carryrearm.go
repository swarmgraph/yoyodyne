package orchestrator

// Re-arming a publication's merge on the development manager's decision, with
// nobody typing a verb.
//
// One publication is re-armed on no decision at all: a merge the reconciling
// sweep withdrew because its checks failed on the target itself (redtarget.go).
// It waits on the items filed for that check, and once every one of them is
// closed the harness arms it again here, on a head level with its target whose
// checks now pass, spending no re-arm (yoyodyne-m5p).
//
// Two publications reach her docket as ones a re-arm can finish: a promoted,
// approved run whose record holds its pull request and says nothing ever asked
// the forge to merge it, and one whose queued merge the forge dropped. She
// decides either: a re-arm, or a re-run. A re-run is fired by the ordinary
// carry-out, because it is a run and takes a developer slot. A re-arm is not a
// run. It is one merge request, made by the Rearmer under the target branch's
// promotion lease, and it holds no slot and leaves no run to wait out — so it is
// fired here, on its own path, once per pull, rather than dressed up as a started
// run the pass would then account for as one.
//
// Until yoyodyne-ifd.429.31 nothing fired the first, and until yoyodyne-ifd.428.46
// nothing fired the second: each waited for somebody to type `yoyo triage rearm`.
// What is fired is exactly what the verb would make — the same action, reached
// by the same run — so the gates are the Rearmer's own: the forge's merge state,
// a head behind its target and a failing check on a first arming, the pre-merge
// check on the remote target, the decision standing and not yet carried out. The
// sweep offers a publication only where the Rearmer's own reading of the record
// says it can repeat one, so what the watch attempts and what the verb would
// accept cannot come apart.
//
// Every attempt that does not fire is written onto the item's triage record
// exactly as a refused re-run's is, where the development manager reads it. A
// refusal by the Rearmer is paced the same way. The operator's pause and the
// intake hold stop it as they stop every other carry-out: the decision is written
// onto the item as waiting on that switch, once for each time it closes, and the
// first pull after it opens attempts the decision. A re-arm held back by a run of
// its item in flight is handed to RecordUnattempted with the others, so a decision
// no pass attempts for a poll interval is said on the item too.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// CarryRearms fires every re-arm decision standing about a publication the
// harness has not acted on — a merge nothing ever asked the forge for, or one the
// forge dropped — and reports what became of each attempt. intakeHeld is the
// pull's own reading of the intake hold, which a carry-out honours as every other
// one does.
func (c CarryOut) CarryRearms(ctx context.Context, intakeHeld bool) ([]CarriedOut, error) {
	if c.Rearmer == nil {
		return nil, nil
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	hold, paused, err := c.paused()
	if err != nil {
		return nil, err
	}
	shut := rearmSwitch{}
	switch {
	case paused:
		shut = rearmSwitch{
			gate:    runstate.TriageGateSpendingPause,
			refusal: fmt.Sprintf("the operator has paused the harness, since %s", hold.HeldAt.UTC().Format(time.RFC3339)),
			clears:  "`yoyo resume` lifting the pause; nothing was asked of the forge, so the publication keeps its re-arm",
		}
	case intakeHeld:
		shut = rearmSwitch{
			gate:    runstate.TriageGateIntakeHold,
			refusal: "the operator has held what the harness chooses",
			clears:  "`yoyo release` lifting the hold; nothing was asked of the forge, so the publication keeps its re-arm",
		}
	}
	entries, err := c.Docket.List()
	if err != nil {
		return nil, fmt.Errorf("read the triage docket: %w", err)
	}
	inFlight, err := c.itemsInFlight()
	if err != nil {
		return nil, err
	}
	tasks, _, err := c.readRearms(ctx, entries, inFlight, onceRecorded(c.Runs), c.now(), shut.gate)
	carried := make([]CarriedOut, 0, len(tasks))
	for _, task := range tasks {
		if ctx.Err() != nil {
			break
		}
		if shut.gate != "" {
			carried = append(carried, c.stopped(ctx, task, CarriedOut{
				WorkItemID: task.WorkItemID,
				RunID:      task.RunID,
				DocketKey:  task.DocketKey,
				Decision:   task.Decision,
			}, shut.gate, true, shut.refusal, shut.clears))
			continue
		}
		carried = append(carried, c.carryRearm(ctx, task))
	}
	return carried, err
}

// rearmSwitch is a gate shut for every re-arm at once — the operator's pause or
// the intake hold — as this pull reads it, with what it says and what opens it.
// The zero value is both switches open.
type rearmSwitch struct {
	gate    string
	refusal string
	clears  string
}

// readRearms is every re-arm decision the sweep above would attempt, and every
// one standing that it would not, with why.
//
// A publication is a candidate where it is docketed as one and the Rearmer's own
// reading of its run's record — rearmablePublication and publicationIsSettled —
// says it has a merge to repeat or to make: that is a publication nothing asked
// the forge to merge and one the forge dropped alike, and it excludes a merge
// the forge still has queued and one it merged. The decision standing about that
// run has to be a re-arm, and the publication's budget has to carry one the
// harness has not made.
//
// A permanent refusal leaves the decision alone until it changes. A temporary
// refusal still cooling is left to its pacing and said nowhere new, because the
// refusal already on the item says it. A waiting record made since the decision, on the switch
// that is shut now, is not written again every poll the switch stands. And a run
// of the item in flight holds the decision back, which is returned as held so
// RecordUnattempted writes it onto the item once it has stood a poll interval.
func (c CarryOut) readRearms(ctx context.Context, entries []triage.Entry, inFlight map[string]string, history func() ([]runstate.State, error), now time.Time, shut string) ([]CarryOutTask, []heldDecision, error) {
	candidates := make(map[string]bool)
	for _, entry := range entries {
		if entry.Class == triage.ClassPublication && entry.RunID != "" && entry.WorkItemID != "" {
			candidates[entry.RunID] = true
		}
	}
	if len(candidates) == 0 {
		return nil, nil, nil
	}
	recorded, err := history()
	if err != nil {
		return nil, nil, fmt.Errorf("read the recorded runs to find the publications a re-arm could finish: %w", err)
	}
	var tasks []CarryOutTask
	var held []heldDecision
	var problems []error
	for _, state := range recorded {
		if !candidates[state.RunID] || !unmergedPublication(state) {
			continue
		}
		delete(candidates, state.RunID)
		counters, err := c.Decisions.Counters(state.WorkItemID)
		if err != nil {
			problems = append(problems, fmt.Errorf("read what triage has decided about %s: %w", state.WorkItemID, err))
			continue
		}
		key := triage.PublicationKey(state.RunID, state.PullRequest.Number)
		var task CarryOutTask
		if state.WaitingOnRedTarget() {
			if !rearmable(state) {
				continue
			}
			// A merge withdrawn for its target's red check is the harness's to arm,
			// with nobody deciding: it is attempted once every item it waits on is
			// closed, and until then it is waiting rather than refused.
			answered, err := c.targetRedAnswered(ctx, *state.PullRequest.TargetRed)
			if err != nil {
				problems = append(problems, err)
				continue
			}
			if !answered {
				continue
			}
			task = CarryOutTask{
				WorkItemID: state.WorkItemID,
				RunID:      state.RunID,
				DocketKey:  key,
				Decision:   runstate.TriageDecisionRearm,
				Reason:     fmt.Sprintf("every item the merge of pull request %d waited on for %s's red check is closed", state.PullRequest.Number, state.PullRequest.TargetRed.TargetBranch),
				DecidedAt:  state.PullRequest.TargetRed.At,
				Harness:    true,
			}
		} else {
			decision, found := counters.DecisionOf(state.RunID)
			if !found || decision.Decision != runstate.TriageDecisionRearm {
				continue
			}
			if counters.RearmsOf(key) <= state.PullRequest.MergeRearms {
				continue
			}
			task = CarryOutTask{
				WorkItemID: state.WorkItemID,
				RunID:      state.RunID,
				DocketKey:  key,
				Decision:   decision.Decision,
				Reason:     decision.Reason,
				DecidedAt:  decision.DecidedAt,
			}
		}
		if running, busy := inFlight[state.WorkItemID]; busy {
			held = append(held, heldDecision{
				task:   task,
				gate:   runstate.TriageGateWorkItem,
				why:    fmt.Sprintf("run %s of %s is in flight, and a re-arm of an item something is already running is not attempted until that run ends", running, state.WorkItemID),
				clears: fmt.Sprintf("run %s ending, which needs nobody; the pass after it attempts the re-arm", running),
			})
			continue
		}
		if standing, recorded := counters.CarryOutOf(state.RunID); recorded && !standing.RefusedAt.Before(task.DecidedAt) {
			if standing.BlocksDecision(task.Decision, task.DecidedAt, now) {
				continue
			}
			if shut != "" && standing.Waiting && standing.Gate == shut {
				continue
			}
		}
		tasks = append(tasks, task)
	}
	return tasks, held, errors.Join(problems...)
}

// targetRedAnswered reports every item a merge withdrawn for its target's red
// check waits on being closed. A carry-out wired with no tracker reads it as
// answered and leaves the gate to the Rearmer, which refuses and says why.
func (c CarryOut) targetRedAnswered(ctx context.Context, waiting runstate.TargetRed) (bool, error) {
	if c.Items == nil {
		return true, nil
	}
	ids := waiting.WaitingOn()
	statuses, err := itemStatuses(ctx, c.Items, ids)
	if err != nil {
		return false, fmt.Errorf("read whether %s, filed for a red check on %s, is closed: %w", statusReadID(err), waiting.TargetBranch, err)
	}
	for _, id := range ids {
		if !strings.EqualFold(statuses[id], "closed") {
			return false, nil
		}
	}
	return true, nil
}

// rearmable reports a run whose publication the Rearmer could repeat or make the
// merge of, as its record stands: promoted, approved, over, and neither merged
// nor queued at the forge. It is the Rearmer's own two readings rather than a
// third, so a publication the watch offers is one the verb would take.
func rearmable(state runstate.State) bool {
	published, _, err := rearmablePublication(state)
	return err == nil && publicationIsSettled(state, published) == nil
}

// unmergedPublication reports a run whose publication a re-arm decision could
// stand about and nothing has finished: it ended, it published a request, and
// the forge has neither merged it nor holds a merge of it queued. It is wider
// than rearmable on purpose. A decision about a publication whose record cannot
// describe the merge a re-arm makes — a run that stopped before it promoted is
// the case — is still a decision, and it is offered so that the Rearmer refuses
// it aloud onto the item rather than the watch passing it over in silence,
// which is what the re-arm of the supervisor's periodic pass (yoyodyne-ifd.413)
// met for a day (yoyodyne-edi).
func unmergedPublication(state runstate.State) bool {
	if state.PullRequest == nil || !state.Status.Terminal() {
		return false
	}
	return !state.PullRequest.Merged && !state.PullRequest.MergeQueued
}

// carryRearm makes the one merge request a decision authorizes, and writes a
// refusal onto the item where the development manager reads it.
func (c CarryOut) carryRearm(ctx context.Context, task CarryOutTask) CarriedOut {
	carried := CarriedOut{
		WorkItemID: task.WorkItemID,
		RunID:      task.RunID,
		DocketKey:  task.DocketKey,
		Decision:   task.Decision,
	}
	result, err := c.Rearmer.Rearm(ctx, RearmRequest{Run: task.RunID, Reason: task.Reason})
	if err != nil || !result.Rearmed {
		carried.Cause = carryOutCause(err)
		clears := "what the refusal names: a head brought level with its target, checks that pass, a requirement on the forge met, or a forge that can be read again — nothing was spent where the refusal says so, and the decision is attempted again once the refusal has cooled; a re-run is the other decision, and hands the change back for a fresh run"
		if carried.Cause != "" {
			clears = "the development manager recording a new decision about this publication, or escalating the inconsistent record"
		}
		var unrearmable UnrearmablePublicationError
		if errors.As(err, &unrearmable) {
			clears = fmt.Sprintf("nothing on the forge: the record of run %s cannot describe the merge a re-arm makes, so no later attempt makes it and none brings pull request %d's head up to date. The decision that applies is a re-run, recorded in place of this re-arm, which hands the change back for a fresh run from the target branch — the fallback a re-arm decision names for a head it cannot bring up to date",
				unrearmable.RunID, unrearmable.Number)
		}
		return c.stopped(ctx, task, carried, runstate.TriageGateHarness, false, refusalText(err), clears)
	}
	carried.Carried = true
	carried.Reason = result.Reason
	carried.RecordProblem = result.RecordProblem
	// A finding an earlier refused attempt left is taken back now the merge is
	// asked for: left standing, the docket would go on saying this decision is not
	// happening while the forge holds its merge.
	if !task.Harness {
		if problem := c.clearFinding(ctx, task); problem != "" {
			carried.RecordProblem = joinProblem(carried.RecordProblem, problem)
		}
	}
	return carried
}

// clearFinding takes back the carry-out finding standing about one decision,
// and reports what it could not do. Only a finding that is there is cleared, so
// the first attempt, which left none, writes nothing.
func (c CarryOut) clearFinding(ctx context.Context, task CarryOutTask) string {
	counters, err := c.Decisions.Counters(task.WorkItemID)
	if err != nil {
		return fmt.Sprintf("the re-arm was made and whether an earlier refusal of it stands on %s's triage record could not be read, so the docket may still say this decision is not happening: %v", task.WorkItemID, err)
	}
	if _, standing := counters.CarryOutOf(task.RunID); !standing {
		return ""
	}
	return clearCarryOutFinding(ctx, c.Decisions, task.WorkItemID, task.RunID, c.now())
}
