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
	if !resolved.Config.Services.Scheduler.Enabled {
		return []Finding{{
			Check:  "team",
			Status: StatusProblem,
			Summary: "team mode is on, and the scheduler service is off, so team mode is refused: the scheduler is what renews this machine's claims every 15 minutes and releases them when a run ends, " +
				"so without it another factory could take over an item a run is still working, and items would stay claimed after their runs end. Turn services.scheduler.enabled on, or turn team.enabled off",
			Detail: "services.scheduler.enabled is false",
			Remedy: editConfiguration,
		}}
	}
	remote := resolved.Config.Execution.Remote
	switch probe := d.probeTeamReferences(ctx, repository, remote); {
	case probe.refused:
		return []Finding{{
			Check:  "team",
			Status: StatusProblem,
			Summary: fmt.Sprintf("team mode is on, and the remote %s would not take a reference under refs/yoyodyne/, so team mode is refused: "+
				"that is where factories claim work, and without claims two factories would do the same work. "+
				"Allow references under refs/yoyodyne/ on the forge, or turn team.enabled off", remote),
			Detail: probe.detail,
			Remedy: editConfiguration,
		}}
	case probe.leftOver:
		return []Finding{{
			Check:   "team",
			Status:  StatusProblem,
			Summary: fmt.Sprintf("team mode is on, and whether %s accepts references under refs/yoyodyne/ could not be checked: an earlier check left its probe reference %s there", remote, teamProbeRef),
			Detail:  probe.detail,
			Remedy:  fmt.Sprintf("git -C %s push %s :%s", shellQuote(repository), remote, teamProbeRef),
		}}
	case probe.detail != "":
		return []Finding{{
			Check:  "team",
			Status: StatusProblem,
			Summary: fmt.Sprintf("team mode is on, and whether %s accepts references under refs/yoyodyne/ could not be checked, because the push did not reach it; "+
				"in team mode no new work starts while the remote cannot be reached", remote),
			Detail: probe.detail,
			Remedy: fmt.Sprintf("git -C %s fetch %s", shellQuote(repository), remote),
		}}
	}
	return []Finding{{
		Check:   "team",
		Status:  StatusOK,
		Summary: fmt.Sprintf("team mode is on, the harness publishes pull requests, the scheduler renews claims, and %s accepts the references factories claim work with", remote),
	}}
}

// teamProbe is what pushing the probe reference found: nothing wrong where
// detail is empty; the remote refusing the reference; a probe left by an
// earlier check in the way; or, with neither, a push that did not reach the
// remote or could not be made.
type teamProbe struct {
	refused  bool
	leftOver bool
	detail   string
}

// probeTeamReferences pushes a reference of its own under refs/yoyodyne/ to the
// remote and deletes it again, and says what refused it, or nothing where the
// remote took both. The commit pushed is a new one of the empty tree, so the
// probe carries none of the repository's content.
func (d *diagnosis) probeTeamReferences(ctx context.Context, repository, remote string) teamProbe {
	tree, err := d.run(ctx, repository, "git", "hash-object", "-t", "tree", "-w", os.DevNull)
	if err != nil || tree.Status != execution.ProcessSucceeded {
		return teamProbe{detail: "the probe could not be written: " + describeFailure(tree, err)}
	}
	commit, err := d.run(ctx, repository, "git",
		"-c", "user.name=Yoyodyne Harness", "-c", "user.email=harness@yoyodyne.invalid",
		"commit-tree", "--no-gpg-sign", strings.TrimSpace(tree.Stdout), "-m", "yoyo doctor checking that this remote accepts references under refs/yoyodyne/")
	if err != nil || commit.Status != execution.ProcessSucceeded {
		return teamProbe{detail: "the probe could not be written: " + describeFailure(commit, err)}
	}
	probe := strings.TrimSpace(commit.Stdout)
	pushed, err := d.run(ctx, repository, "git", "push", "--porcelain", "--no-verify",
		"--force-with-lease="+teamProbeRef+":", remote, probe+":"+teamProbeRef)
	if failed := judgeProbePush("pushing", pushed, err); failed.detail != "" {
		return failed
	}
	removed, err := d.run(ctx, repository, "git", "push", "--porcelain", "--no-verify",
		"--force-with-lease="+teamProbeRef+":"+probe, remote, ":"+teamProbeRef)
	return judgeProbePush("removing", removed, err)
}

// judgeProbePush reads one probe push the way the harness reads a claim push
// (gitworktree's pushClaimRef): the line `git push --porcelain` printed for the
// reference says whether the remote answered and refused it. A refusal for
// stale info is a probe an earlier check left; any other refusal is the remote
// refusing the reference; and no line for it at all is a push that never
// reached the remote.
func judgeProbePush(doing string, result execution.ProcessResult, err error) teamProbe {
	if err == nil && result.Status == execution.ProcessSucceeded {
		return teamProbe{}
	}
	detail := doing + " " + teamProbeRef + " failed: " + describeFailure(result, err)
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 || !strings.HasSuffix(fields[1], ":"+teamProbeRef) || !strings.HasPrefix(fields[0], "!") {
			continue
		}
		detail = doing + " " + teamProbeRef + " was refused: " + strings.TrimSpace(fields[2])
		if strings.Contains(fields[2], "stale info") {
			return teamProbe{leftOver: true, detail: detail}
		}
		return teamProbe{refused: true, detail: detail}
	}
	return teamProbe{detail: detail}
}
