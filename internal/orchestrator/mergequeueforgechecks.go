package orchestrator

// GitHub's required checks as the merge queue's full check
// (docs/designs/integration-through-a-merge-queue.md). Where the project has
// switched on checks on CI, the harness reads the forge's required checks for
// the queue candidate's own commit and records the reading on the generation
// with the digest of the workflow files they ran from and the target the
// candidate was built on. runstate.MergeQueueGeneration.CIChecksCount is the
// one place that says whether that reading counts, and only the full local run
// it would stand in for may be skipped on it: the independent review, the
// protected-path check and the quick local checks are the generation's gate
// either way.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// readCIChecks reads the forge's required checks for the generation's
// candidate commit and records the reading on the generation. It records
// nothing, and returns the generation as it was, where checks on CI is off or
// no forge is wired. A forge that cannot be read, or that answers for another
// commit, is an error and records no reading: a check state nobody could read
// is not a passing one.
func (w MergeQueueWorker) readCIChecks(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, error) {
	p := w.Pipeline
	ci := p.Config.Execution.CIChecks
	if !ci.Enabled || p.CIForge == nil {
		return generation, nil
	}
	reading, err := p.CIForge.RequiredChecks(ctx, generation.Candidate, ci.Required)
	if err != nil {
		return generation, fmt.Errorf("read the forge's required checks on candidate %s: %w", generation.Candidate, err)
	}
	if reading.Commit != generation.Candidate {
		return generation, fmt.Errorf("the forge's reading is for %s, not the candidate %s", reading.Commit, generation.Candidate)
	}
	evidence := &runstate.MergeQueueCIEvidence{
		Binding: generation.Binding(), Commit: reading.Commit, Base: generation.TargetBase,
		Required: append([]string(nil), ci.Required...),
	}
	passed := reading.AllPassed()
	for _, check := range reading.Checks {
		// A result the forge gave for a commit other than the candidate is
		// recorded as it was given, so it can never count.
		evidence.Checks = append(evidence.Checks, runstate.CICheckEvidence{Name: check.Name, Result: string(check.Result), Commit: check.Commit})
	}
	if passed {
		if evidence.WorkflowDigest, err = p.CIForge.WorkflowDigest(ctx, generation.Candidate); err != nil {
			return generation, fmt.Errorf("read the digest of the workflow files the checks on %s ran from: %w", generation.Candidate, err)
		}
	}
	// The target is read after the forge, so a move while the forge was being
	// read shows in the reading rather than hiding behind it.
	if evidence.TargetAtRead, err = w.Candidates.TargetCommit(ctx, generation.TargetBranch); err != nil {
		return generation, fmt.Errorf("read %s after the forge's checks on %s: %w", generation.TargetBranch, generation.Candidate, err)
	}
	evidence.ReadAt = w.now()
	generation.CIChecks = evidence
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, fmt.Errorf("record the forge's checks on generation %d's candidate: %w", generation.Number, err)
	}
	return generation, nil
}

// ciChecksCount asks, at the moment of a merge, whether the forge's recorded
// reading still counts as the generation's full check: the workflow files are
// read at the candidate again and the target is read afresh, and the answer is
// runstate's. The reason it gives when it does not is what a record of the
// full local run that follows says it ran for.
func (w MergeQueueWorker) ciChecksCount(ctx context.Context, generation runstate.MergeQueueGeneration) (bool, string, error) {
	p := w.Pipeline
	ci := p.Config.Execution.CIChecks
	if !ci.Enabled || p.CIForge == nil {
		return false, "checks on the forge are not switched on", nil
	}
	if generation.CIChecks == nil {
		return false, "the forge's checks were never read for the candidate", nil
	}
	digest, err := p.CIForge.WorkflowDigest(ctx, generation.Candidate)
	if err != nil {
		return false, "", fmt.Errorf("read the digest of the workflow files at %s: %w", generation.Candidate, err)
	}
	target, err := w.Candidates.TargetCommit(ctx, generation.TargetBranch)
	if err != nil {
		return false, "", fmt.Errorf("read %s before counting the forge's checks: %w", generation.TargetBranch, err)
	}
	counts, why := generation.CIChecksCount(ci.Required, digest, strings.TrimSpace(target))
	return counts, why, nil
}
