package doctor

import (
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"strings"
	"testing"
)

// Team mode is refused in plain words where the harness does not publish, and
// where the remote will not hold a reference under refs/yoyodyne/.
func TestTeamModeIsRefusedWhereItCannotRun(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = healthyConfig + teamSection
	finding, found := findingFor(world.diagnose(), "team")
	if !found || finding.Status != StatusProblem || !strings.Contains(finding.Summary, "does not publish pull requests") || !strings.Contains(finding.Summary, "team mode is refused") {
		t.Fatalf("team finding with publishing off = %#v, want team mode refused for publishing", finding)
	}

	world = newWorld(t)
	world.configuration = publishingConfig + teamSection
	world.runner.reply("--force-with-lease=refs/yoyodyne/probe/doctor:", probeRefused("[remote rejected] (deny updating a hidden ref)"))
	finding, found = findingFor(world.diagnose(), "team")
	if !found || finding.Status != StatusProblem || !strings.Contains(finding.Summary, "refs/yoyodyne/") || !strings.Contains(finding.Detail, "deny updating a hidden ref") {
		t.Fatalf("team finding with a refusing remote = %#v, want team mode refused naming the refusal", finding)
	}
}

// With team mode on and a forge that takes the probe, the check passes, and
// the probe it pushed is deleted with a lease naming its own commit.
func TestTeamModeOnAForgeThatAcceptsTheReferences(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = publishingConfig + teamSection
	world.runner.reply("commit-tree", succeeded("1111111111111111111111111111111111111111\n"))
	finding, found := findingFor(world.diagnose(), "team")
	if !found || finding.Status != StatusOK {
		t.Fatalf("team finding = %#v, want ok", finding)
	}
	deleted := false
	for _, command := range world.runner.commands {
		joined := strings.Join(command, " ")
		if strings.Contains(joined, "--force-with-lease=refs/yoyodyne/probe/doctor:1111111111111111111111111111111111111111") && strings.Contains(joined, " :refs/yoyodyne/probe/doctor") {
			deleted = true
		}
	}
	if !deleted {
		t.Errorf("the probe was not deleted with a lease naming its commit: %v", world.runner.commands)
	}
}

// With team mode off the forge is asked nothing about team mode.
func TestTeamModeOffAsksNothing(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = publishingConfig
	if finding, found := findingFor(world.diagnose(), "team"); found {
		t.Fatalf("team finding with team mode off = %#v, want none", finding)
	}
	for _, command := range world.runner.commands {
		if strings.Contains(strings.Join(command, " "), "refs/yoyodyne/") {
			t.Fatalf("team mode off pushed %v", command)
		}
	}
}

// probeRefused is what `git push --porcelain` prints, and how it exits, when
// the remote answers and refuses the probe reference.
func probeRefused(summary string) execution.ProcessResult {
	return execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stdout:   "To example\n!\t1111111111111111111111111111111111111111:refs/yoyodyne/probe/doctor\t" + summary + "\nDone\n",
		Stderr:   "error: failed to push some refs",
	}
}

// A push that never reached the remote is not reported as the forge refusing
// the reference, and the remedy is to find out why the remote cannot be
// reached rather than to change the forge.
func TestTeamModeOnAnUnreachableRemoteSaysItCouldNotCheck(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = publishingConfig + teamSection
	world.runner.reply("--force-with-lease=refs/yoyodyne/probe/doctor:", failed("ssh: Could not resolve hostname github.com: nodename nor servname provided"))
	finding, found := findingFor(world.diagnose(), "team")
	if !found || finding.Status != StatusProblem || !strings.Contains(finding.Summary, "could not be checked") || strings.Contains(finding.Summary, "Allow references") {
		t.Fatalf("team finding = %#v, want a check that could not reach the remote, not a refusal", finding)
	}
	if !strings.Contains(finding.Remedy, " fetch origin") {
		t.Errorf("remedy = %q, want one that asks the remote", finding.Remedy)
	}
}

// A probe reference an earlier check left behind is named, with the command
// that removes it.
func TestTeamModeNamesAProbeLeftByAnEarlierCheck(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = publishingConfig + teamSection
	world.runner.reply("--force-with-lease=refs/yoyodyne/probe/doctor:", probeRefused("[rejected] (stale info)"))
	finding, found := findingFor(world.diagnose(), "team")
	if !found || finding.Status != StatusProblem || !strings.Contains(finding.Summary, "left its probe reference") || !strings.Contains(finding.Remedy, ":refs/yoyodyne/probe/doctor") {
		t.Fatalf("team finding = %#v, want the leftover probe named with the command that removes it", finding)
	}
}

// Team mode is refused while the scheduler service is off, because nothing
// else renews or releases the claims.
func TestTeamModeIsRefusedWithTheSchedulerOff(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = publishingConfig + teamSection + "services:\n  scheduler:\n    enabled: false\n"
	finding, found := findingFor(world.diagnose(), "team")
	if !found || finding.Status != StatusProblem || !strings.Contains(finding.Summary, "scheduler service is off") {
		t.Fatalf("team finding = %#v, want team mode refused with the scheduler off", finding)
	}
}
