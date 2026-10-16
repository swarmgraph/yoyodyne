package orchestrator

// A repair decided on a run that gave back its developer slot after losing the
// race to land.
//
// Such a run stopped approved, with its checks passed and its change kept on
// its branch, and with a recorded base the target branch has since left behind.
// What a repair of it is for is bringing that change up to date with the target
// and landing it. Re-entering it at its developer, on the base it stopped on,
// does not do that: the checks and the review that follow are spent on a change
// the promotion then refuses at once, because the target moved hours earlier.
// So the repair puts the run back at its promotion instead, exactly as a repair
// of a dropped merge does (replaysDroppedMerge): the promotion finds the target
// moved, replays the change onto it, and the whole gate is earned again on the
// replayed change before it lands. See
// docs/diagnoses/yoyodyne-gle-repair-left-the-change-on-its-old-base.md.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// replaysLostRace reports a repair decided on a run that stopped for losing the
// race to land, whose approval still stands and whose change the repository
// still holds, so what the repair carries out is the replay rather than a
// developer attempt. A missing checkout is restored from the branch first, on
// the same conditions any repair restores one (continuableRepair).
func replaysLostRace(prior runstate.State, found triage.Found) bool {
	if !prior.LostItsRace() || !prior.ApprovedAwaitingIntegration() || continuableStall(prior) || prior.StoppedAtStageBound() {
		return false
	}
	if strings.TrimSpace(prior.ProviderSessionID) == "" || prior.ResumptionsLeft() == 0 {
		return false
	}
	if prior.WorktreePath == "" || prior.Branch == "" || prior.BaseCommit == "" || prior.TargetBranch == "" {
		return false
	}
	if found.Unknown || !found.BranchThere {
		return false
	}
	if found.WorktreeThere {
		return true
	}
	return found.Looked() && prior.HarnessCommit != "" && prior.PreservedWorkRef == "" && prior.ArtifactsRetiredBy == ""
}

// putBackAfterLostRace is the record of a run that lost its race made live again
// at its promotion. The races it lost are cleared with the stop they caused:
// they bounded how long that stint held a developer slot, the stint ended when
// it gave the slot back, and this is a new one. Left standing, the first move of
// the target after the replay would stop the run again as though it had lost
// five more.
func putBackAfterLostRace(continued *runstate.State, prior runstate.State, reason string, now time.Time) {
	putBackAtPromotion(continued, prior, reason, now)
	continued.IntegrationRetries = 0
}

// lostRaceReplayReason is what the run and the item record as why a repair put
// a run that lost its race back at its promotion rather than invoking its
// developer.
func lostRaceReplayReason(entry triage.Entry, granted repairGrant, prior runstate.State) string {
	reason := fmt.Sprintf(
		"Triaged: the development manager's triage decided a repair of run %s, %s, whose approved change lost the race to land on %s and gave back its developer slot with its work kept. The harness put the run back at its promotion to bring the kept change up to date with %s before anything else, check it and have it reviewed again, and land it; the races it lost before are not counted against it, and a conflict goes to the developer under a grant of %d further repair attempt(s) recorded against %s's durable triage budget. The reasoning that decision was recorded with: ",
		entry.RunID, granted.decision.Cite(), prior.TargetBranch, prior.TargetBranch, granted.attempts, entry.WorkItemID)
	room := max(runstate.MaxSelectionReasonBytes-len(reason), 0)
	return singleLine(reason+singleLine(strings.TrimSpace(granted.decision.Reason), room), runstate.MaxSelectionReasonBytes)
}
