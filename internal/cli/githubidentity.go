package cli

import (
	"context"
	"errors"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// gitHubIdentityProbe asks GitHub, with the identity the harness publishes
// under, whether it answers for a target branch: the remotes must take the
// harness's credential for listing that branch, and, where pull requests are
// opened automatically, the GitHub command-line tool must be logged in. A nil
// error means GitHub answered. It reads and writes nothing.
func gitHubIdentityProbe(parts components) func(ctx context.Context, target string) error {
	return func(ctx context.Context, target string) error {
		if err := parts.worktrees.VerifyRemoteAccess(ctx, target); err != nil {
			return err
		}
		if parts.config.Approvals.Publishing != domain.ApprovalAutomatic {
			return nil
		}
		forge := publish.GitHub{
			Runner: parts.runner, Dir: parts.repository,
			Remote: parts.config.Execution.Remote, PushRemote: parts.config.Execution.PushRemote,
			RedactValues: parts.redactValues,
		}
		availability, err := forge.Availability(ctx)
		if err != nil {
			return err
		}
		if !availability.Installed {
			return errors.New("the GitHub command-line tool is not installed, so it is not logged in")
		}
		if !availability.Authenticated {
			return errors.New("the GitHub command-line tool is not logged in")
		}
		return nil
	}
}

// gitHubIdentityStopsFrom is what takes up the runs that stopped only because
// the harness could not reach GitHub as itself, built over the parts a
// continuation at the checks and a resumption of an approved change use.
func gitHubIdentityStopsFrom(parts components) *orchestrator.GitHubIdentityStops {
	continuer := checkStageContinuerFrom(parts)
	resumer := integrationResumerFrom(parts)
	return &orchestrator.GitHubIdentityStops{
		Docket:    continuer.Docket,
		Redocket:  continuer.Redocket,
		Runs:      continuer.Runs,
		Intake:    continuer.Intake,
		Items:     continuer.Items,
		Worktrees: continuer.Worktrees,
		Capacity:  continuer.Capacity,
		Start:     continuer.Start,
		Answers:   gitHubIdentityProbe(parts),
		ResumeLanding: func(ctx context.Context, runID, reason string) (orchestrator.IntegrationResumeResult, error) {
			return resumer.Resume(ctx, orchestrator.IntegrationResumeRequest{Run: runID, Reason: reason})
		},
	}
}

// gitHubAnswersForCarryOut is the question a decision refused because the
// harness could not reach GitHub as itself waits on: asked of the branch the
// primary checkout is on, which is the target a run lands on.
func gitHubAnswersForCarryOut(parts components) func(ctx context.Context) error {
	probe := gitHubIdentityProbe(parts)
	return func(ctx context.Context) error {
		target, err := parts.worktrees.CurrentBranch(ctx)
		if err != nil {
			return err
		}
		return probe(ctx, target)
	}
}
