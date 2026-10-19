package orchestrator

// A developer turn can end on a reply while work it started in the background —
// a test suite, a timing run — is still running. The runner waits that work out
// to its bound after the reply and then ends it (execution.AfterReply), but the
// reply was written before the work finished, so it cannot say what the work
// found: a developer that wrote "waiting for the timing runs" handed back a
// change it had not finished checking. Taken as the attempt's answer, that turn
// goes to the checks and the reviewer, and whatever they find wrong is charged
// to a repair attempt the developer never really used. This file is what the
// develop loop asks instead: the turn is sent back once, in the same session and
// within the same attempt, saying what was left running and what became of it.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// outlivedReply reports a developer turn whose session was still running work
// of its own after it wrote its final reply. The runner only records an account
// for a process still there well after its reply, so a provider that exits a
// moment after writing its terminal is never one of these.
func outlivedReply(result backend.RunResult) (execution.AfterReply, bool) {
	if result.Process.AfterReply == nil {
		return execution.AfterReply{}, false
	}
	return *result.Process.AfterReply, true
}

// backgroundWorkEnding says, in the words a person reads, what became of the
// work a developer turn left running after its reply.
func backgroundWorkEnding(account execution.AfterReply) string {
	switch account.Outcome {
	case execution.AfterReplyEnded:
		return fmt.Sprintf("the developer's turn ended while processes it had started in the background were still running; they were still running %s after the reply, so the harness ended them",
			waitedFor(account.Bound()))
	case execution.AfterReplyExited:
		return fmt.Sprintf("the developer's turn ended while processes it had started in the background were still running; they finished on their own %s after the reply, too late for the reply to say what they found",
			waitedFor(time.Duration(account.WaitedSeconds)*time.Second))
	}
	return "the developer's turn ended while processes it had started in the background were still running"
}

// recordBackgroundWork writes into the run's event stream what the harness did
// about a turn that ended with background work still running, so the run's
// record says why the developer was asked again, or why the reply was taken as
// it stood, rather than leaving a reader to infer it from the invocation count.
// A record that cannot be written costs the stream the sentence and nothing
// else, as recordAfterReplyWaiting's does: what the harness does next is the
// same either way.
func (a *activeRun) recordBackgroundWork(text string) {
	event, err := execution.NewEvent(a.state.RunID, a.state.LastSequence+1, a.pipeline.clock().Now(), execution.EventProcessOutput, "harness", map[string]any{
		"role": domain.RoleDeveloper, "text": text,
	})
	if err == nil {
		_ = a.sink(event)
	}
}

// sendBackForBackgroundWork records that the turn is being sent back and why.
func (a *activeRun) sendBackForBackgroundWork(ctx context.Context, account execution.AfterReply) {
	a.recordBackgroundWork(backgroundWorkEnding(account) + ". The turn is sent back to the developer in the same session to finish that work and give its account; no repair attempt is spent.")
	a.observe(ctx, deliveryDevelop, "reissued")
}

// acceptDespiteBackgroundWork records that a turn sent back once for background
// work ended the same way again, and that its reply is taken as it stands: the
// harness has already ended what was left, and a third invocation asking again
// would spend the largest thing the harness buys on the same question.
func (a *activeRun) acceptDespiteBackgroundWork(account execution.AfterReply) {
	a.recordBackgroundWork(backgroundWorkEnding(account) + ". The developer had already been sent back once for this, so its reply is taken as it stands, and the checks judge the change without whatever that work would have found.")
}

// backgroundWorkPrompt is what a developer sent back for background work is
// asked. It says what was left running and what became of it, that the change
// itself is untouched, and what a turn has to do before it ends.
func backgroundWorkPrompt(invariants, scratchDirectory string, account execution.AfterReply, checks []string) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract(scratchDirectory, checks))
	prompt.WriteString("\n\n")
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString("# Your turn ended while your own background work was still running\n\n")
	fmt.Fprintf(&prompt, "You ended your last turn with processes you had started in the background still running. Here is what became of them: %s. Whatever they printed after your reply never reached you, and none of it is in your reply.\n\n", backgroundWorkEnding(account))
	prompt.WriteString("The work you already did is untouched. Your worktree holds it and this is the same session, so continue rather than starting anything over. Nothing is waiting on you to come back later: the turn you end is the end of your work. Run what you still need — the check, the timing, the measurement — in the foreground, wait for it to finish within this turn, and then reply with the account: what you changed, how you verified it, and what risk remains, plus any landing claim, report, or proposal you have to make. If something takes too long to run here, say that in the account instead of starting it in the background.\n\n")
	prompt.WriteString("This is asked once and spends no repair attempt. If your next turn also ends with background work still running, the harness ends that work and takes your reply as it stands.")
	return prompt.String()
}
