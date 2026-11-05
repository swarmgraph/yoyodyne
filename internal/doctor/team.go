package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// teamProbeRef is the reference the team check pushes and deletes again to learn
// whether the remote accepts references under refs/yoyodyne/, which is where
// team mode keeps its claims.
const teamProbeRef = "refs/yoyodyne/probe/doctor"

// checkTeam asks, for a project with team mode on, the two things team mode
// cannot run without (docs/designs/working-alongside-other-contributors.md,
// section 5): that the harness publishes through pull requests, because the
// forge is where factories learn about each other, and that the shared remote
// accepts references under refs/yoyodyne/, because that is where the claims
// that keep two factories off the same item live. Either one missing refuses
// team mode, in plain words, rather than letting it run without claims.
//
// A project with team mode off is asked nothing, and the check says nothing.
func (d *diagnosis) checkTeam(ctx context.Context, resolved config.Resolved, repository string) []Finding {
	if !resolved.Config.Team.Enabled {
		return nil
	}
	editConfiguration := fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(resolved.Path))
	if !HarnessPublishes(resolved.Config.Approvals.Publishing) {
		return []Finding{{
			Check:  "team",
			Status: StatusProblem,
			Summary: "team mode is on, and the harness does not publish pull requests for this project, so team mode is refused: " +
				"another factory could not see this one's work. Set approvals.publishing to automatic, or turn team.enabled off",
			Detail: fmt.Sprintf("approvals.publishing is %s", resolved.Config.Approvals.Publishing),
			Remedy: editConfiguration,
		}}
	}
	remote := resolved.Config.Execution.Remote
	if refusal := d.probeTeamReferences(ctx, repository, remote); refusal != "" {
		return []Finding{{
			Check:  "team",
			Status: StatusProblem,
			Summary: fmt.Sprintf("team mode is on, and the remote %s would not take a reference under refs/yoyodyne/, so team mode is refused: "+
				"that is where factories claim work, and without claims two factories would do the same work. "+
				"Allow references under refs/yoyodyne/ on the forge, or turn team.enabled off", remote),
			Detail: refusal,
			Remedy: editConfiguration,
		}}
	}
	return []Finding{{
		Check:   "team",
		Status:  StatusOK,
		Summary: fmt.Sprintf("team mode is on, the harness publishes pull requests, and %s accepts the references factories claim work with", remote),
	}}
}

// probeTeamReferences pushes a reference of its own under refs/yoyodyne/ to the
// remote and deletes it again, and says what refused it, or nothing where the
// remote took both. The commit pushed is a new one of the empty tree, so the
// probe carries none of the repository's content.
func (d *diagnosis) probeTeamReferences(ctx context.Context, repository, remote string) string {
	tree, err := d.run(ctx, repository, "git", "hash-object", "-t", "tree", "-w", os.DevNull)
	if err != nil || tree.Status != execution.ProcessSucceeded {
		return "the probe could not be written: " + describeFailure(tree, err)
	}
	commit, err := d.run(ctx, repository, "git",
		"-c", "user.name=Yoyodyne Harness", "-c", "user.email=harness@yoyodyne.invalid",
		"commit-tree", "--no-gpg-sign", strings.TrimSpace(tree.Stdout), "-m", "yoyo doctor checking that this remote accepts references under refs/yoyodyne/")
	if err != nil || commit.Status != execution.ProcessSucceeded {
		return "the probe could not be written: " + describeFailure(commit, err)
	}
	probe := strings.TrimSpace(commit.Stdout)
	pushed, err := d.run(ctx, repository, "git", "push", "--porcelain", "--no-verify",
		"--force-with-lease="+teamProbeRef+":", remote, probe+":"+teamProbeRef)
	if err != nil || pushed.Status != execution.ProcessSucceeded {
		return "pushing " + teamProbeRef + " was refused: " + describeFailure(pushed, err)
	}
	removed, err := d.run(ctx, repository, "git", "push", "--porcelain", "--no-verify",
		"--force-with-lease="+teamProbeRef+":"+probe, remote, ":"+teamProbeRef)
	if err != nil || removed.Status != execution.ProcessSucceeded {
		return "removing " + teamProbeRef + " again was refused: " + describeFailure(removed, err)
	}
	return ""
}
