package runstate

// A first silent-stream stall, and the harness continuing it itself.
//
// The harness stops a provider invocation that has written nothing for longer
// than it allows, or one still working when its total budget runs out, and
// leaves the run in flight to be continued. Nothing
// continues it, so half an hour later the reconciling sweep settles it as a run
// whose process vanished and dockets it. Until yoyodyne-a0s that entry then
// waited on the development manager deciding a repair — a decision about a stop
// the harness made itself, that judged nothing, and whose continuation costs no
// review round and no repair attempt. Two runs waited that way in two days
// (yoyodyne-ifd.428.44 and yoyodyne-ifd.430.13.8).
//
// So the harness continues such a stall itself, once per run: at the next pull
// with a developer slot free, in the same worktree and developer session, at
// the phase it stalled in, with no decision recorded and no budget spent. A
// second stall of the same run is the development manager's, as every stall
// was before. This is what the run's record says about that — whether the
// stoppage is one, whether the harness still continues it, and the sentence
// every surface says it in.
//
// A session the harness stopped because its total budget ran out is continued
// the same way and counted against the same bound. Both stops are the harness's
// own clock rather than anything judging the change, and the recovery design
// says either "owes a continuation rather than a retry"
// (docs/designs/recoverable-and-terminal-failures.md).

import (
	"fmt"
	"strings"
	"time"
)

// MaxHarnessStallContinuations bounds how many times the harness continues one
// run's silent-stream stall itself. It is one on purpose: a run whose provider
// goes silent again after the harness carried it on once is a run somebody
// should look at, not one another try settles.
const MaxHarnessStallContinuations = 1

// HandedBack reports a run holding a failure that was returned to its
// developer: a replay conflict, refused paths, owed execution evidence, a
// failing check, or the reviewer's findings. It is the repair input a
// continuation hands back, and a run carrying one is inside its repair loop.
func (s State) HandedBack() bool {
	owesVerification := s.Verification != nil && len(s.Verification.Owed) > 0
	return s.ReplayConflict != nil || s.PathRefusal != nil || owesVerification || s.CheckFailure != nil || len(s.ReviewFindingDetails) > 0
}

// SettledSilentStreamStall reports a run the sweep settled while it was parked
// on the harness's own stop of its provider: a silent stream, or a session
// whose total budget ran out (HarnessStopSays says which). This includes a
// repair already underway: the stop returned no new failure, so continuing that
// same attempt preserves its repair input and the budget it already consumed.
func (s State) SettledSilentStreamStall() bool {
	if !s.Status.Terminal() || s.Integration != nil || s.IntegrationStop != nil {
		return false
	}
	if s.Environmental == nil || s.Environmental.Cause != CauseProcessVanished || s.HarnessStopSays() == "" {
		return false
	}
	switch s.Phase {
	case PhaseDeveloping, PhaseChecking, PhaseReviewing:
		return true
	default:
		return false
	}
}

// HarnessStopSays is which of the harness's own two stops ended the AI session,
// in the words every surface says it in, and is empty for a run the harness did
// not stop either way.
func (s State) HarnessStopSays() string {
	if s.Environmental == nil {
		return ""
	}
	switch s.Environmental.ProviderStop {
	case ProviderStopStalled:
		return "produced no output for longer than the harness allows"
	case ProviderStopBudgetExhausted:
		return "was still working when its total budget ran out"
	default:
		return ""
	}
}

// HarnessStallContinuations is how many times the harness has continued this
// run's stall itself, with nobody deciding it.
func (s State) HarnessStallContinuations() int {
	count := 0
	for _, continuation := range s.RepairContinuations {
		if continuation.ByHarness {
			count++
		}
	}
	return count
}

// HarnessContinuesStall reports a settled silent-stream stall the harness will
// continue itself: its branch, worktree, and developer session are still there,
// the harness has not already continued this run MaxHarnessStallContinuations
// times, and no earlier continuation was refused for something only a person
// can settle. Whether it may go now — a free slot, the operator's switches — is
// the moment's to answer rather than the record's.
func (s State) HarnessContinuesStall() bool {
	if !s.SettledSilentStreamStall() {
		return false
	}
	if s.WorktreePath == "" || s.Branch == "" || s.BaseCommit == "" || s.TargetBranch == "" {
		return false
	}
	if s.WorktreeRemoved || s.BranchRemoved || strings.TrimSpace(s.ProviderSessionID) == "" {
		return false
	}
	if strings.TrimSpace(s.StallContinuationRefused) != "" {
		return false
	}
	return s.HarnessStallContinuations() < MaxHarnessStallContinuations
}

// StallStopSays is what every surface says about a settled silent-stream stall:
// that the harness stopped it rather than anything judging the change, and what
// happens next. It is empty for every other run.
func (s State) StallStopSays() string {
	if !s.SettledSilentStreamStall() {
		return ""
	}
	stopped := "the AI session running this run " + s.HarnessStopSays() + ", so the harness stopped it: the cause was outside the work, so the stop judged nothing and preserved the change and any earlier repair input"
	if readopted := s.ReadoptedSays(); readopted != "" {
		stopped += "; " + readopted
	}
	if s.HarnessContinuesStall() {
		return fmt.Sprintf("%s; the harness continues it itself, in the same worktree and developer session at the %s phase it stalled in, at the next pull with a developer slot free — no decision is needed and no review round, repair grant, or re-run is spent (continuation %d of %d)",
			stopped, s.Phase, s.HarnessStallContinuations()+1, MaxHarnessStallContinuations)
	}
	if refused := strings.TrimSpace(s.StallContinuationRefused); refused != "" {
		return fmt.Sprintf("%s; the harness's continuation of it was refused — %s — so what happens to it next is the development manager's decision", stopped, refused)
	}
	if s.HarnessStallContinuations() >= MaxHarnessStallContinuations {
		return fmt.Sprintf("%s; this is its second stall: the harness already continued it once by itself, which is its bound, so its continuation is spent and what happens to it next is the development manager's decision", stopped)
	}
	return stopped + "; its branch, worktree, or developer session is gone, so the harness cannot continue it, and what happens to it next is the development manager's decision"
}

// ReadoptedSays names the redeploy stop this run was re-adopted from, where it
// was. A stall at the phase the run was re-adopted at began in the session that
// re-adoption resumed, which is worth knowing before deciding anything about
// it. A stall at a later phase — re-adopted at its developer attempt, stalled in
// its review — was in a different invocation, and is said only to have
// followed the re-adoption.
func (s State) ReadoptedSays() string {
	if s.Readopted == nil {
		return ""
	}
	readopted := fmt.Sprintf("the run had been stopped for a redeploy at its %s phase at %s and re-adopted by the session that came back",
		s.Readopted.Phase, s.Readopted.At.UTC().Format(time.RFC3339))
	if s.Readopted.Phase == s.Phase {
		return readopted + ", so this stall began in the session that re-adoption resumed"
	}
	return fmt.Sprintf("%s, before it went on to the %s phase it stalled in", readopted, s.Phase)
}
