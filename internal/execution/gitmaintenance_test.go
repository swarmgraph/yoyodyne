package execution

// Git's automatic maintenance, fenced out of what the harness launches.
//
// The fence exists for the commands the harness never composes: an agent's Git
// command, or a project's build tooling, inside a worktree that shares the
// repository's common Git directory. What is asserted here is that a process
// this runner starts really does see the two settings — against a repository
// whose own config asks for maintenance, because a fence that loses to the
// repository's config is no fence in the repository it is for — and the two
// things about the environment that are easy to get silently wrong: that
// configuration somebody else put there survives, and that applying the fence
// twice is the same as applying it once.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestAProcessTheRunnerStartsCannotTriggerGitMaintenance(t *testing.T) {
	t.Parallel()

	repository := maintainedRepository(t)
	for _, setting := range []struct{ name, want string }{
		{name: "gc.auto", want: "0"},
		{name: "maintenance.auto", want: "false"},
	} {
		// Read through the runner rather than by hand: what is being asserted is
		// what a process the harness launched sees, and the repository's own
		// config says the opposite of it.
		if value := gitConfigThroughRunner(t, repository, setting.name); value != setting.want {
			t.Errorf("%s = %q in a launched process, want the fence's %q rather than the repository's own", setting.name, value, setting.want)
		}
	}
}

// The fence is inherited, which is the whole of why it is in the environment
// rather than in the command line: the Git command that loses a run is one the
// harness never composed, and it is a grandchild of what the harness launched.
func TestTheFenceReachesAProcessTheLaunchedProcessStartsItself(t *testing.T) {
	t.Parallel()

	repository := maintainedRepository(t)
	result, err := OSProcessRunner{}.Run(context.Background(), Command{
		Name:    "/bin/sh",
		Args:    []string{"-c", "git -C " + repository + " config --get gc.auto"},
		Env:     unfencedEnvironment(),
		Timeout: testProcessBudget,
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(result.Stdout); got != "0" {
		t.Fatalf("gc.auto = %q two processes down, want %q: %s", got, "0", result.Stderr)
	}
}

// A process given only the fence has no PATH and no HOME and runs nothing.
func TestTheFenceIsAddedToTheEnvironmentRatherThanReplacingIt(t *testing.T) {
	t.Parallel()

	environment := WithGitMaintenanceFence(nil)
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); configuresGit(name) {
			continue
		}
		if !slices.Contains(environment, entry) {
			t.Fatalf("environment dropped %q", entry)
		}
	}
}

// The fence states two settings and takes no view of any other, so configuration
// somebody else put in the environment is still applied — after which the fence
// has the last word on its own two.
func TestConfigurationTheEnvironmentAlreadyCarriedSurvivesTheFence(t *testing.T) {
	t.Parallel()

	environment := WithGitMaintenanceFence([]string{
		"PATH=/usr/bin",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=user.name",
		"GIT_CONFIG_VALUE_0=Somebody Else",
	})
	if !slices.Contains(environment, "PATH=/usr/bin") {
		t.Fatalf("environment = %v, want the rest of it carried", environment)
	}
	assertGitConfig(t, environment, []gitSetting{
		{key: "user.name", value: "Somebody Else"},
		{key: "gc.auto", value: "0"},
		{key: "maintenance.auto", value: "false"},
	})
}

// A harness process launched by a harness process is ordinary, and the fence has
// to survive it unchanged: a pair added per generation is an environment that
// grows with the call depth and reads as though somebody meant it.
func TestApplyingTheFenceTwiceIsTheSameAsApplyingItOnce(t *testing.T) {
	t.Parallel()

	once := WithGitMaintenanceFence([]string{"PATH=/usr/bin"})
	if twice := WithGitMaintenanceFence(once); !slices.Equal(twice, once) {
		t.Fatalf("the fence applied twice = %v, want %v", twice, once)
	}
}

// An environment asking for maintenance is exactly the case the fence is for,
// whoever set it and whatever case they wrote the setting's name in.
func TestAnInheritedRequestForMaintenanceIsReplaced(t *testing.T) {
	t.Parallel()

	environment := WithGitMaintenanceFence([]string{
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=GC.Auto",
		"GIT_CONFIG_VALUE_0=6700",
		"GIT_CONFIG_KEY_1=maintenance.auto",
		"GIT_CONFIG_VALUE_1=true",
	})
	assertGitConfig(t, environment, []gitSetting{
		{key: "gc.auto", value: "0"},
		{key: "maintenance.auto", value: "false"},
	})
}

// A count Git would refuse every command over is replaced rather than built on,
// so a broken environment reaching the harness is not a broken environment
// leaving it.
func TestACountTheEnvironmentCannotBeReadWithIsReplaced(t *testing.T) {
	t.Parallel()

	for _, count := range []string{"", "   ", "two", "-1"} {
		environment := WithGitMaintenanceFence([]string{
			"GIT_CONFIG_COUNT=" + count,
			"GIT_CONFIG_KEY_0=user.name",
			"GIT_CONFIG_VALUE_0=Somebody Else",
		})
		assertGitConfig(t, environment, []gitSetting{
			{key: "gc.auto", value: "0"},
			{key: "maintenance.auto", value: "false"},
		})
	}
}

// A numbered pair above the count is one Git ignores. Carrying it would turn
// something already inert into configuration the fence handed on.
func TestANumberedPairTheCountDoesNotAdmitIsNotCarried(t *testing.T) {
	t.Parallel()

	environment := WithGitMaintenanceFence([]string{
		"GIT_CONFIG_COUNT=0",
		"GIT_CONFIG_KEY_0=core.editor",
		"GIT_CONFIG_VALUE_0=nothing Git was going to read",
	})
	assertGitConfig(t, environment, []gitSetting{
		{key: "gc.auto", value: "0"},
		{key: "maintenance.auto", value: "false"},
	})
	for _, entry := range environment {
		if strings.HasPrefix(entry, "GIT_CONFIG_VALUE_") && strings.Contains(entry, "nothing Git was going to read") {
			t.Fatalf("environment = %v, want the ignored pair gone rather than promoted", environment)
		}
	}
}

// assertGitConfig reads the numbered pairs back out of an environment the way
// Git would — the count, and the pairs below it in order — and compares them to
// what the caller expects a command to be about to apply.
func assertGitConfig(t *testing.T, environment []string, want []gitSetting) {
	t.Helper()

	entries := map[string]string{}
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		entries[name] = value
	}
	if got := entries[gitConfigCountVariable]; got != strconv.Itoa(len(want)) {
		t.Fatalf("%s = %q, want %d: %v", gitConfigCountVariable, got, len(want), environment)
	}
	for index, setting := range want {
		key := entries[gitConfigKeyVariable+strconv.Itoa(index)]
		value := entries[gitConfigValueVariable+strconv.Itoa(index)]
		if key != setting.key || value != setting.value {
			t.Fatalf("pair %d = %q=%q, want %q=%q: %v", index, key, value, setting.key, setting.value, environment)
		}
	}
}

// gitConfigThroughRunner asks a repository for one setting from inside a process
// the runner started, which is the only place the fence exists.
func gitConfigThroughRunner(t *testing.T, repository, setting string) string {
	t.Helper()

	result, err := OSProcessRunner{}.Run(context.Background(), Command{
		Name:    "git",
		Args:    []string{"-C", repository, "config", "--get", setting},
		Env:     unfencedEnvironment(),
		Timeout: testProcessBudget,
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessSucceeded {
		t.Fatalf("git config --get %s = %v: %s", setting, result.Status, result.Stderr)
	}
	return strings.TrimSpace(result.Stdout)
}

// unfencedEnvironment is this process's environment with no Git configuration in
// it, for a process whose fence has to come from the runner. A test the harness
// itself launched already carries the fence, and a child inheriting that would
// pass these tests with the runner fencing nothing.
func unfencedEnvironment() []string {
	var environment []string
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); !configuresGit(name) {
			environment = append(environment, entry)
		}
	}
	return environment
}

// maintainedRepository is a repository whose own config asks for the automatic
// maintenance the fence refuses. Every other repository in these tests turns it
// off; this one turns it on, because a fence is only worth asserting where
// something is asking for the thing it stops.
func maintainedRepository(t *testing.T) string {
	t.Helper()

	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "gc.auto", "6700"},
		{"config", "maintenance.auto", "true"},
	} {
		command := exec.Command("git", append([]string{"-C", repository}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v error = %v: %s", args, err, output)
		}
	}
	return repository
}
