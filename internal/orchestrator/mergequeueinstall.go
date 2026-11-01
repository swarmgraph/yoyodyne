package orchestrator

// The project's declared install steps, run before the merge queue's stages
// run the project's code in a checkout: the candidate's checks, in the
// candidate's checkout, and the failing check asked of the target, in the
// base's (install.go says when a step is due). Each successful run is kept on
// the generation, without its output, because the generation is the record the
// queue keeps of what its stages did; a step that does not pass is the stage's
// problem, so the stage earns nothing and is run again on a later pass.

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// installWhereDue runs each declared step that is due in directory, in the
// order the project declares them, behind gate, and keeps each successful run
// on the generation. It returns what the stage records as its problem where a
// step did not pass, and an error where the attempt could not be recorded or
// the queue's work was stopped.
func (w MergeQueueWorker) installWhereDue(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, generation *runstate.MergeQueueGeneration, directory, before string, gate func(string) (*execution.LaunchGate, error)) (string, error) {
	p := w.Pipeline
	if p == nil || len(p.Config.Install) == 0 {
		return "", nil
	}
	cache := ""
	for _, step := range p.Config.Install {
		digest, err := installDigest(directory, step.Inputs)
		if err != nil {
			return boundedEvidence(fmt.Sprintf("the inputs of install step %q could not be read: %v", step.Name, err)), nil
		}
		due, err := installDue(generation.Install, step, directory, digest)
		if err != nil {
			return boundedEvidence(fmt.Sprintf("the outputs of install step %q could not be looked for: %v", step.Name, err)), nil
		}
		if !due {
			continue
		}
		if cache == "" {
			if cache, err = p.installCache(); err != nil {
				return boundedEvidence(err.Error()), nil
			}
		}
		sink := w.Events
		if sink == nil {
			sink = func(execution.Event) error { return nil }
		}
		record, last, runErr := runInstallStep(ctx, *p, step, digest, checks.InstallRequest{
			RunID:        generation.EventStream(),
			Directory:    directory,
			LastSequence: generation.LastSequence,
			Cache:        cache,
			// A candidate is checked for a landing, so its install is let start
			// ahead of every run's gate, as its checks are (heavychecks.go).
			Permit: checks.PermitRequest{Priority: domain.HeavyCheckForLanding},
			Gate:   gate,
		}, sink)
		if last > generation.LastSequence {
			generation.LastSequence = last
		}
		if runErr != nil && ctx.Err() != nil {
			return "", runErr
		}
		if record.Result != runstate.InstallPassed {
			return boundedEvidence(installFailure{record: record, before: before}.Error()), nil
		}
		record.Output = ""
		revised := *generation
		revised.Install = keptInstall(generation.Install, record)
		if err := w.Queue.RecordGeneration(lease, key, revised); err != nil {
			return "", fmt.Errorf("record install step %q of generation %d: %w", step.Name, generation.Number, err)
		}
		*generation = revised
	}
	return "", nil
}

// keptInstall is records with record in place of any earlier run of the same
// step in the same checkout.
func keptInstall(records []runstate.InstallRun, record runstate.InstallRun) []runstate.InstallRun {
	kept := make([]runstate.InstallRun, 0, len(records)+1)
	for _, earlier := range records {
		if earlier.Name != record.Name || earlier.Directory != record.Directory {
			kept = append(kept, earlier)
		}
	}
	return append(kept, record)
}
