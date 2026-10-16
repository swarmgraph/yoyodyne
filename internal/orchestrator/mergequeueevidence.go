package orchestrator

// Two pieces of a merge queue candidate's evidence beside its checks and its
// review (mergequeueworker.go is the rest).
//
// The protected-path gate is the third thing a landing needs evidence of
// (integration-requires-revision-bound-evidence), asked of exactly what would
// land: the candidate, whose merge onto the target is a revision no run's own
// gate ever saw. It is recorded on the generation, and the generation's gate
// refuses a candidate it has no answer for or that changes a path its item does
// not grant (runstate's MergeQueueGeneration.Gate).
//
// A check that fails on a candidate is not yet the change's failure. The
// harness's queue never runs the target's own checks, so a target already red
// on a check fails every candidate built on it, and reading that as the
// change's defect spends the change's repair attempts on somebody else's
// break. So the failing check is run once more over the target at the
// candidate's base. Where the base passes it, the failure is the change's;
// where the base fails it too, the failure is the target's, the item the red
// target is recorded under is found or filed as a red landing's is, and the
// entry waits on the target moving without being charged. Where the base's
// result is already known — the base is a candidate this queue verified and
// landed under the checks configured now — nothing is run again.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// checkPaths records the protected-path gate's answer about the generation's
// candidate, unless it is recorded already.
func (w MergeQueueWorker) checkPaths(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry, generation runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, error) {
	if generation.Paths != nil {
		return generation, nil
	}
	p := w.Pipeline
	item, err := p.Tracker.Show(ctx, entry.WorkItemID)
	if err != nil {
		return generation, fmt.Errorf("read what %s grants before checking the candidate's paths: %w", entry.WorkItemID, err)
	}
	changed, err := w.Candidates.CandidatePaths(ctx, generation.TargetBase, generation.Candidate)
	if err != nil {
		return generation, fmt.Errorf("list the paths generation %d's candidate changes: %w", generation.Number, err)
	}
	var exports []string
	if p.Worktrees != nil {
		exports = p.Worktrees.CurrentExports()
	}
	refused := protectedpath.Protect(p.Config, exports...).Refused(changed, protectedpath.Grants(grantEvidence(item)...))
	generation.Paths = &runstate.MergeQueuePathEvidence{
		Binding: generation.Binding(), CheckedAt: w.now(), Changed: len(changed), Refused: boundedPaths(refused),
	}
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, fmt.Errorf("record generation %d's protected-path check: %w", generation.Number, err)
	}
	return generation, nil
}

// boundedPaths keeps what a generation records of a list of paths, and says
// how many more there were in the last one kept.
func boundedPaths(paths []string) []string {
	if len(paths) <= runstate.MaxConflictedPaths {
		return paths
	}
	kept := append([]string(nil), paths[:runstate.MaxConflictedPaths-1]...)
	return append(kept, fmt.Sprintf("and %d more", len(paths)-len(kept)))
}

// failedCheck is the configured check that ran to its own end and failed on
// the generation's candidate, and empty where none did.
func failedCheck(generation runstate.MergeQueueGeneration) string {
	run := generation.CheckRun
	if run == nil || run.FinishedAt == nil || run.Problem != "" {
		return ""
	}
	for _, result := range run.Results {
		if !result.Passed && result.CouldNotRun == "" {
			return result.Command
		}
	}
	return ""
}

// checkBase records whether the target passes the failed check at the
// candidate's base: known from a landing this queue verified, or found by
// running the check over a checkout of the base.
func (w MergeQueueWorker) checkBase(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry, generation runstate.MergeQueueGeneration, configured runstate.MergeQueueCheckConfiguration, command string) (runstate.MergeQueueGeneration, error) {
	evidence := &runstate.MergeQueueBaseEvidence{Binding: generation.Binding(), Base: generation.TargetBase, Command: command, StartedAt: w.now()}
	known, err := w.knownGreen(key, generation.TargetBase, configured)
	if err != nil {
		return generation, err
	}
	if known != "" {
		finished := w.now()
		evidence.FinishedAt, evidence.Passed, evidence.KnownFrom = &finished, true, known
		generation.BaseCheck = evidence
		if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
			return generation, fmt.Errorf("record generation %d's base check: %w", generation.Number, err)
		}
		return generation, nil
	}
	if err := w.paused(runstate.MergeQueueStageBase); err != nil {
		return generation, err
	}
	hold, err := w.holdStage(lease, key, generation, runstate.MergeQueueStageBase)
	if err != nil {
		return generation, err
	}
	defer hold.Close()
	checkout, err := w.Candidates.CheckoutQueueBase(ctx, entry.EntryID, generation.TargetBase)
	if err != nil {
		return generation, fmt.Errorf("check out %s at %s to run %s on it: %w", key.TargetBranch, generation.TargetBase, command, err)
	}
	defer func() { _ = w.Candidates.RemoveQueueCandidate(context.WithoutCancel(ctx), checkout) }()
	generation.BaseCheck = evidence
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, fmt.Errorf("record that generation %d's base check began: %w", generation.Number, err)
	}
	launched := launches{worker: w, lease: lease, key: key, generation: &generation, hold: hold, stage: runstate.MergeQueueStageBase}
	results, lastSequence, runErr := w.Pipeline.Checks.Run(ctx, checks.Request{
		RunID:        generation.EventStream(),
		Directory:    checkout,
		Commands:     []string{command},
		LastSequence: generation.LastSequence,
		Env:          []string{checks.Narrowing{Whole: true, Reason: "the target is checked whole at a merge queue candidate's base"}.Env()},
		Gate:         launched.gate,
	}, w.Events)
	if lastSequence > generation.LastSequence {
		generation.LastSequence = lastSequence
	}
	recorded := *generation.BaseCheck
	finished := w.now()
	recorded.FinishedAt = &finished
	switch {
	case runErr != nil:
		recorded.Problem = boundedEvidence("the check could not be run on the base: " + runErr.Error())
	case len(results) != 1:
		recorded.Problem = "the check reported no result on the base"
	case results[0].CouldNotRun != "":
		recorded.Problem = boundedEvidence(fmt.Sprintf("%s could not run on the base: %s", command, results[0].CouldNotRun))
	default:
		switch results[0].Process.Status {
		case execution.ProcessTimedOut, execution.ProcessCancelled, execution.ProcessStalled:
			recorded.Problem = boundedEvidence(fmt.Sprintf("%s was %s on the base and judged nothing", command, results[0].Process.Status))
		default:
			recorded.Passed, recorded.ExitCode = results[0].Passed, results[0].Process.ExitCode
		}
	}
	if recorded.Settled() && !recorded.Passed {
		item, fileErr := w.fileRedBase(ctx, entry, generation, command, results)
		if fileErr == nil {
			recorded.RedItem = item
		}
	}
	generation.BaseCheck = &recorded
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, fmt.Errorf("record generation %d's base check: %w", generation.Number, err)
	}
	if runErr != nil {
		return generation, fmt.Errorf("run %s on the base of generation %d: %w", command, generation.Number, runErr)
	}
	return generation, nil
}

// knownGreen says how the target is known to pass every configured check at
// base, and is empty where it is not known: base is the candidate an entry of
// this queue landed, verified under the checks configured now.
func (w MergeQueueWorker) knownGreen(key runstate.MergeQueueKey, base string, configured runstate.MergeQueueCheckConfiguration) (string, error) {
	entries, err := w.Queue.Entries(key)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		landing, found, err := w.Queue.Landing(key, entry.EntryID)
		if err != nil {
			return "", err
		}
		completion := landing.Completion
		if !found || completion == nil || completion.Landed != base || completion.Generation == 0 {
			continue
		}
		generations, err := w.Queue.Generations(key, entry.EntryID)
		if err != nil {
			return "", err
		}
		if int(completion.Generation) > len(generations) {
			continue
		}
		landed := generations[completion.Generation-1]
		if landed.Candidate == base && landed.Checks.Same(configured) {
			return fmt.Sprintf("%s is the candidate entry %d landed, which passed every check configured now", base, entry.Order), nil
		}
	}
	return "", nil
}

// fileRedBase finds the unfinished item that records the target red on a
// check, or files one the way a red landing files its own: at the front of the
// queue, under the goal the queued item serves, with what the check printed in
// its notes.
func (w MergeQueueWorker) fileRedBase(ctx context.Context, entry runstate.MergeQueueEntry, generation runstate.MergeQueueGeneration, command string, results []checks.Result) (string, error) {
	p := w.Pipeline
	if p.Filer == nil {
		return "", errors.New("nothing is wired to file a work item")
	}
	marker := redLandingMarker(generation.TargetBranch, command)
	existing, err := openItemMarked(ctx, p.Filer, marker)
	if err != nil {
		return "", err
	}
	base := shortCommit(generation.TargetBase)
	if existing != "" {
		note := fmt.Sprintf("Red again at %s on %s: %s fails there, met by the merge queue candidate for %s (%s), which waits on this item.", base, generation.TargetBranch, command, entry.WorkItemID, entry.WorkItemTitle)
		_, err := p.Tracker.RecordOutcome(ctx, existing, note)
		return existing, err
	}
	description := fmt.Sprintf("%s fails on %s itself at %s. The merge queue candidate for %s (%s) failed it, and the same check run on %s at the candidate's base failed too, so the failure is the target's rather than the change's.\n\n"+
		"The queued change waits on this item and is charged nothing for it; the queue checks it again once %s has moved. Reproduce with `%s` at %s. What the check said is in this item's notes.",
		command, generation.TargetBranch, base, entry.WorkItemID, entry.WorkItemTitle, generation.TargetBranch, generation.TargetBranch, command, generation.TargetBase)
	notes := fmt.Sprintf("Filed by the harness for %s red on %s at %s, met by the merge queue entry for %s (%s), as a red landing files its own item.\n%s",
		command, generation.TargetBranch, base, entry.WorkItemID, entry.RunID, marker)
	if item, err := p.Tracker.Show(ctx, entry.WorkItemID); err == nil {
		if statement, named := goal.NamedIn(item.Notes); named {
			notes += "\n\n" + goal.Note(statement)
		}
	}
	if len(results) == 1 {
		if output := strings.TrimSpace(boundedCheckOutput(results[0])); output != "" {
			notes += "\n\nWhat the check said (bounded):\n\n" + quotedOutput(output)
		}
	}
	priority := 0
	created, err := p.Filer.Create(ctx, beads.NewWorkItem{
		Title:       fmt.Sprintf("Red target %s at %s: %s fails, met by the merge queue entry for %s", generation.TargetBranch, base, command, entry.WorkItemID),
		Description: description,
		Type:        "bug",
		Notes:       notes,
		Priority:    &priority,
		Origin:      domain.WorkItemOrigin{Asker: domain.AskerHarness},
	})
	if err != nil {
		return "", err
	}
	return created.ID, nil
}
