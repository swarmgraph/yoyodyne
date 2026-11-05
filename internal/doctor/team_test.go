package doctor

import (
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
	world.runner.reply("--force-with-lease=refs/yoyodyne/probe/doctor:", failed("! [remote rejected] refs/yoyodyne/probe/doctor (deny updating a hidden ref)"))
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
