package orchestrator

// Checks that finished before a restart onto a new build keep their results.
//
// A watch session restarting onto a newly deployed build waits for the runs it
// hosts for at most execution.redeploy_drain_limit, and then stops any run still
// going (redeploydrain.go). A run stopped at its checks is owed the gate from the
// checks, and a check stage longer than that wait would otherwise start over
// after every deploy and never finish. So every check that passes is added to
// the run's record as it passes (runstate.KeptChecks), bound to the candidate it
// passed over, and the stage that picks the run back up runs only the checks
// that had not passed — and only when the candidate is exactly the one they
// passed over. Any other candidate, and any other way into the checks, runs
// every check again.
//
// A check that said it could not run is not kept: it judged nothing, and what
// stopped it may be gone on the new build.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// checkKeeping is one check stage's use of the kept results: which configured
// checks it takes from the record rather than running, and the record it adds
// each newly passed check to.
type checkKeeping struct {
	// kept are the results reused from before the restart, in the order the
	// stage lists them.
	kept []checks.Result
	// commands and providerCLIs are what the stage still runs, with the
	// provider-CLI marks moved to the positions the remaining checks hold.
	commands     []string
	providerCLIs map[int]bool
}

// keepChecks settles which of the stage's checks run. It always starts a fresh
// record bound to the candidate as it stands now, carrying forward only what a
// pick-up after a restart may reuse. A candidate that cannot be named is given
// no record, so nothing this stage passes is kept, and every check runs.
func (a *activeRun) keepChecks(ctx context.Context, commands []string, providerCLIs map[int]bool) checkKeeping {
	owed := a.keptChecksOwed
	a.keptChecksOwed = false
	prior := a.state.KeptChecks
	a.state.KeptChecks = nil
	keeping := checkKeeping{commands: commands, providerCLIs: providerCLIs}
	content, err := a.pipeline.Worktrees.ContentIdentity(ctx, a.worktree)
	if err != nil {
		if owed {
			a.noteKeptChecks(ctx, keeping, "the change could not be named to compare it with the one the checks passed on before the restart, so no result was kept")
		}
		return keeping
	}
	record := &runstate.KeptChecks{Content: content, Attempt: a.state.RepairAttempts, Commit: a.state.HarnessCommit}
	a.state.KeptChecks = record
	if !owed {
		return keeping
	}
	if prior == nil || !prior.Binds(content, a.state.RepairAttempts, a.state.HarnessCommit) {
		why := "no check had finished before the restart"
		if prior != nil && len(prior.Checks) > 0 {
			why = "the change is not the one the checks passed on before the restart, so no result was kept"
		} else if a.stoppedChecks() != nil {
			// What the stop recorded says check by check why each runs.
			why = ""
		}
		a.noteKeptChecks(ctx, keeping, why)
		return keeping
	}
	redactor := execution.NewRedactor(a.pipeline.RedactValues...)
	passed := make(map[string]runstate.KeptCheck, len(prior.Checks))
	for _, check := range prior.Checks {
		passed[check.Command] = check
	}
	keeping.commands, keeping.providerCLIs = nil, nil
	for index, command := range commands {
		if check, ok := passed[redactor.Redact(command)]; ok {
			record.Checks = append(record.Checks, check)
			keeping.kept = append(keeping.kept, resultOfKept(check))
			continue
		}
		if providerCLIs[index] {
			if keeping.providerCLIs == nil {
				keeping.providerCLIs = map[int]bool{}
			}
			keeping.providerCLIs[len(keeping.commands)] = true
		}
		keeping.commands = append(keeping.commands, command)
	}
	why := ""
	if len(keeping.kept) == 0 && a.stoppedChecks() == nil {
		why = "no check had finished before the restart"
	}
	a.noteKeptChecks(ctx, keeping, why)
	return keeping
}

// stoppedChecks is what the restart that stopped this run at its checks did to
// them, where the stop recorded it (runstate.RedeployStopChecks).
func (a *activeRun) stoppedChecks() *runstate.RedeployStopChecks {
	if a.state.Readopted == nil {
		return nil
	}
	return a.state.Readopted.Checks
}

// keptCommands names the checks the stage reused rather than ran.
func (k checkKeeping) keptCommands() []string {
	commands := make([]string, 0, len(k.kept))
	for _, check := range k.kept {
		commands = append(commands, check.Command)
	}
	return commands
}

// keepPassed adds a check that has just passed to the run's record and saves
// it, so a restart that stops the stage after it does not cost it. The save is
// best effort for the reason the stage's own record is: a store that refuses it
// costs the next restart this one check, and costs the stage nothing.
func (a *activeRun) keepPassed(result checks.Result) {
	if a.state.KeptChecks == nil || !result.Passed {
		return
	}
	a.state.KeptChecks.Checks = append(a.state.KeptChecks.Checks, runstate.KeptCheck{
		Command:        result.Command,
		Status:         result.Process.Status,
		ExitCode:       result.Process.ExitCode,
		StartedAt:      result.Process.StartedAt,
		FinishedAt:     result.Process.FinishedAt,
		Stdout:         keptTail(result.Process.Stdout, runstate.MaxKeptCheckOutputBytes),
		Stderr:         keptTail(result.Process.Stderr, runstate.MaxKeptCheckOutputBytes),
		TimeoutSeconds: int64(result.Timeout / time.Second),
	})
	a.state.UpdatedAt = a.pipeline.clock().Now()
	_ = a.pipeline.Store.Save(a.state)
}

// resultOfKept is a kept check as the result the reviewer and the notes read.
func resultOfKept(check runstate.KeptCheck) checks.Result {
	return checks.Result{
		Command: check.Command,
		Process: execution.ProcessResult{
			Status:     check.Status,
			ExitCode:   check.ExitCode,
			StartedAt:  check.StartedAt,
			FinishedAt: check.FinishedAt,
			Stdout:     check.Stdout,
			Stderr:     check.Stderr,
		},
		Passed:  true,
		Timeout: time.Duration(check.TimeoutSeconds) * time.Second,
	}
}

// keptTail is the last bytes of a check's output, at most bound of them, cut at a
// line where one falls inside the bound and never inside a character.
func keptTail(output string, bound int) string {
	if len(output) <= bound {
		return output
	}
	start := len(output) - bound
	for start < len(output) && !utf8.RuneStart(output[start]) {
		start++
	}
	if newline := strings.IndexByte(output[start:], '\n'); newline >= 0 && start+newline < len(output)-1 {
		start += newline + 1
	}
	return output[start:]
}

// noteKeptChecks says on the work item which checks kept their results across
// the restart and which run again, with why none was kept where that is so. It
// is best effort: the stage's own record and the outcome notes say the same,
// so a note the tracker refuses costs the stage nothing.
func (a *activeRun) noteKeptChecks(ctx context.Context, keeping checkKeeping, why string) {
	if !a.claimed {
		return
	}
	redactor := execution.NewRedactor(a.pipeline.RedactValues...)
	again := make([]string, 0, len(keeping.commands))
	for _, command := range keeping.commands {
		again = append(again, redactor.Redact(command))
	}
	_, _ = a.pipeline.Tracker.RecordOutcome(ctx, a.state.WorkItemID, renderKeptChecksNotes(a.state.RunID, keeping.keptCommands(), again, why, a.stoppedChecks()))
}

// renderKeptChecksNotes is the note a run picked back up at its checks after a
// restart onto a new build leaves on its item. Where the stop recorded what it
// did to the checks, the note says which check the restart waited for and
// sorts the checks still to run into one cut short, any that said they could
// not run, and those that had not started, because a reader told only that
// checks "run again" reads a check the restart never started as one it threw
// away.
func renderKeptChecksNotes(runID string, kept, again []string, why string, stopped *runstate.RedeployStopChecks) string {
	lines := []string{"Yoyodyne picked this run back up at its checks after the watch session restarted onto a new build."}
	if len(kept) > 0 {
		lines = append(lines, "Kept their results, because they had already passed on this same change before the restart: "+strings.Join(kept, ", ")+".")
	}
	if stopped != nil && stopped.WaitedFor != "" && why == "" {
		lines = append(lines, fmt.Sprintf("The restart waited for %s, which was running when the new build arrived, to finish rather than start it over.", stopped.WaitedFor))
	}
	switch {
	case len(again) == 0:
		lines = append(lines, "Every check had already passed, so none runs again.")
	case why != "":
		lines = append(lines, fmt.Sprintf("Running every check again, because %s: %s.", why, strings.Join(again, ", ")))
	case stopped == nil:
		lines = append(lines, "Running now, because they had not passed before the restart: "+strings.Join(again, ", ")+".")
	default:
		var cut, couldNot, unstarted []string
		for _, command := range again {
			switch {
			case oneline.Fold(command, 512) == stopped.CutShort:
				cut = append(cut, command)
			case slices.Contains(stopped.CouldNotRun, oneline.Fold(command, 512)):
				couldNot = append(couldNot, command)
			default:
				unstarted = append(unstarted, command)
			}
		}
		if len(cut) > 0 {
			lines = append(lines, "Running again from its start, because it was still running when the run was stopped: "+strings.Join(cut, ", ")+".")
		}
		if len(couldNot) > 0 {
			lines = append(lines, "Running again, because they said they could not run before the restart: "+strings.Join(couldNot, ", ")+".")
		}
		if len(unstarted) > 0 {
			lines = append(lines, "Not started before the restart, so running now for the first time on this change: "+strings.Join(unstarted, ", ")+".")
		}
	}
	lines = append(lines, "Run: "+runID)
	return strings.Join(lines, "\n")
}
