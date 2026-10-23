package execution

// A process launched for a role carries the role, and what it starts inherits
// it. What is asserted here is the marker itself over a synthetic environment,
// and then a real child launched through the runner with the marker on top of
// an explicit environment, starting a shell that reads it back -- because the
// case the marker is for is an agent's shell running the yoyo binary.

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestWithAgentRoleMarksTheEnvironmentOnceAndReplacesAnInheritedMarker(t *testing.T) {
	t.Parallel()

	got := WithAgentRole([]string{"PATH=/usr/bin", AgentRoleVariable + "=developer", "HOME=/home/operator"}, domain.RoleReviewer)
	want := []string{"PATH=/usr/bin", "HOME=/home/operator", AgentRoleVariable + "=reviewer"}
	if !slices.Equal(got, want) {
		t.Fatalf("WithAgentRole() = %v, want %v", got, want)
	}
	role, launched := LaunchedForRole(got)
	if !launched || role != domain.RoleReviewer {
		t.Fatalf("LaunchedForRole() = %q, %v, want the reviewer, true", role, launched)
	}
}

func TestLaunchedForRoleReadsNothingFromAnUnmarkedOrEmptiedEnvironment(t *testing.T) {
	t.Parallel()

	if role, launched := LaunchedForRole([]string{"PATH=/usr/bin", "YOYODYNE_STATE_HOME=/state"}); launched {
		t.Fatalf("LaunchedForRole() on an unmarked environment = %q, true, want nothing", role)
	}
	// Setting the variable to nothing is how a test process, or a person at a
	// shell an agent opened, says it is nobody's agent.
	if role, launched := LaunchedForRole([]string{AgentRoleVariable + "="}); launched {
		t.Fatalf("LaunchedForRole() on an emptied marker = %q, true, want nothing", role)
	}
}

// The marker rides on the explicit environment rather than around it, so the
// allowlist carries it through under the harness's own family and a shell the
// agent starts sees it.
func TestAChildLaunchedForARoleSeesTheRoleItWasLaunchedFor(t *testing.T) {
	t.Parallel()

	result, err := OSProcessRunner{}.Run(context.Background(), Command{
		Name:    "/bin/sh",
		Args:    []string{"-c", "env"},
		Env:     WithAgentRole(ExplicitEnvironment(nil), domain.RoleDeveloper),
		Timeout: testProcessBudget,
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessSucceeded {
		t.Fatalf("Run() = %#v: %s", result.Status, result.Stderr)
	}
	var seen []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.HasPrefix(line, AgentRoleVariable+"=") {
			seen = append(seen, line)
		}
	}
	if want := []string{AgentRoleVariable + "=developer"}; !slices.Equal(seen, want) {
		t.Fatalf("the child sees %v, want exactly %v", seen, want)
	}
	// And a grandchild given the child's environment through the allowlist
	// still carries it: the marker is under the harness's own prefix on purpose.
	if role, launched := LaunchedForRole(ExplicitEnvironment(strings.Split(strings.TrimSpace(result.Stdout), "\n"))); !launched || role != domain.RoleDeveloper {
		t.Fatalf("LaunchedForRole() through the allowlist = %q, %v, want the developer, true", role, launched)
	}
}
