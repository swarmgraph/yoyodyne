package execution

// The environment a run's process is given is built from an allowlist, and the
// Slack token pair is what the allowlist exists to keep out. What is asserted
// here is the two halves of that: the list itself, over a synthetic parent, and
// then a real child launched through the runner with the pair exported in this
// process -- which is exactly the shell-profile case the guarantee is for -- not
// seeing them.

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestExplicitEnvironmentCarriesOnlyWhatTheAllowlistAdmits(t *testing.T) {
	t.Parallel()

	got := ExplicitEnvironment([]string{
		"PATH=/usr/bin",
		"HOME=/home/operator",
		"SLACK_BOT_TOKEN=xoxb-secret",
		"SLACK_APP_TOKEN=xapp-secret",
		"GOCACHE=/somewhere",
		"GIT_CONFIG_COUNT=1",
		"LC_ALL=C",
		"XDG_CONFIG_HOME=/home/operator/.config",
		"YOYODYNE_STATE_HOME=/state",
		"AWS_REGION=us-east-1",
		"SHLVL=3",
		"MALFORMED",
	})
	want := []string{
		"PATH=/usr/bin",
		"HOME=/home/operator",
		"GOCACHE=/somewhere",
		"GIT_CONFIG_COUNT=1",
		"LC_ALL=C",
		"XDG_CONFIG_HOME=/home/operator/.config",
		"YOYODYNE_STATE_HOME=/state",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ExplicitEnvironment() = %v, want %v", got, want)
	}
}

// A provider's own variables are carried under the prefixes its backend names,
// and nothing else's are: what is a provider's own is that backend's to say.
func TestExplicitEnvironmentCarriesTheProvidersOwnFamiliesWhenNamed(t *testing.T) {
	t.Parallel()

	parent := []string{"PATH=/usr/bin", "CLAUDE_CONFIG_DIR=/homes/two", "ANTHROPIC_BASE_URL=https://proxy.example", "CODEX_HOME=/homes/codex"}
	if got := ExplicitEnvironment(parent, "CLAUDE_", "ANTHROPIC_"); !slices.Equal(got, parent[:3]) {
		t.Fatalf("ExplicitEnvironment() = %v, want %v", got, parent[:3])
	}
	if got := ExplicitEnvironment(parent); !slices.Equal(got, parent[:1]) {
		t.Fatalf("ExplicitEnvironment() with no provider = %v, want %v", got, parent[:1])
	}
}

// The credential rule is on top of the allowlist rather than instead of it: a
// name the list admits is still dropped when it reads as a credential, which is
// what keeps a provider's API key out under the provider's own prefix and what
// would keep the token pair out even if the list ever admitted the family.
func TestExplicitEnvironmentDropsACredentialTheAllowlistWouldAdmit(t *testing.T) {
	t.Parallel()

	got := ExplicitEnvironment([]string{
		"ANTHROPIC_API_KEY=sk-secret",
		"CLAUDE_CODE_OAUTH_TOKEN=oauth-secret",
		"ANTHROPIC_MODEL=opus",
		"GOOGLE_APPLICATION_CREDENTIALS=/keys/service.json",
		"GOFLAGS=-mod=mod",
		"YOYODYNE_CLIENT_SECRET=harness-secret",
	}, "CLAUDE_", "ANTHROPIC_")
	want := []string{"ANTHROPIC_MODEL=opus", "GOFLAGS=-mod=mod"}
	if !slices.Equal(got, want) {
		t.Fatalf("ExplicitEnvironment() = %v, want %v", got, want)
	}
	for _, entry := range got {
		name, _, _ := strings.Cut(entry, "=")
		if sensitiveEnvironmentName(name) {
			t.Fatalf("an explicit environment carries %q, which SensitiveEnvironmentValues recognizes", name)
		}
	}
}

// The case the guarantee is for: the token pair exported where every process
// the harness starts would inherit it. A child launched through the runner with
// an explicit environment does not see them, and does see what it needs to run.
func TestAChildGivenAnExplicitEnvironmentDoesNotSeeTheSlackTokens(t *testing.T) {
	// t.Setenv is this process's environment, so this cannot run in parallel.
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-exported-in-the-parent")
	t.Setenv("SLACK_APP_TOKEN", "xapp-exported-in-the-parent")

	result, err := OSProcessRunner{}.Run(context.Background(), Command{
		Name:    "/bin/sh",
		Args:    []string{"-c", "env"},
		Env:     ExplicitEnvironment(nil),
		Timeout: testProcessBudget,
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessSucceeded {
		t.Fatalf("Run() = %#v: %s", result.Status, result.Stderr)
	}
	seen := make(map[string]string)
	for _, line := range strings.Split(result.Stdout, "\n") {
		if name, value, named := strings.Cut(line, "="); named {
			seen[name] = value
		}
	}
	for _, name := range []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN"} {
		if value, present := seen[name]; present {
			t.Errorf("the child sees %s=%q, which was exported in the parent and must not reach it", name, value)
		}
	}
	for _, name := range []string{"PATH", "HOME"} {
		if _, present := seen[name]; !present {
			t.Errorf("the child does not see %s, without which it cannot run at all", name)
		}
	}
	// And the fence still arrives on top of it: an explicit environment is the
	// base the runner adds to, not a way around what every process carries.
	if seen[gitConfigCountVariable] == "" {
		t.Errorf("the child carries no Git maintenance fence: %v", result.Stdout)
	}
}

// A forge command is the one family given a credential, and it is given the
// allowlist underneath it rather than instead of it. Nothing that is not on the
// forge's own short list comes with it.
func TestForgeEnvironmentAddsTheForgeCredentialToTheAllowlistAndNothingElse(t *testing.T) {
	t.Parallel()

	got := ForgeEnvironment([]string{
		"PATH=/usr/bin",
		"HOME=/home/operator",
		"SLACK_BOT_TOKEN=xoxb-secret",
		"ANTHROPIC_API_KEY=sk-secret",
		"GH_TOKEN=ghp-secret",
		"GH_HOST=github.example.invalid",
		"GITLAB_TOKEN=glpat-secret",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
	})
	want := []string{
		"PATH=/usr/bin",
		"HOME=/home/operator",
		"GH_TOKEN=ghp-secret",
		"GH_HOST=github.example.invalid",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ForgeEnvironment() = %v, want %v", got, want)
	}
}

// The agent socket is what an SSH remote authenticates through, and it is on
// the standing allowlist rather than on the forge's own list — so it reaches a
// remote-reaching Git command the way it reaches everything else the harness
// starts. It is asserted rather than left to be read off two lists, because the
// cost of it having been dropped is every run stopping at integration on any
// installation whose remote is SSH, and a local test remote never notices.
func TestTheAgentSocketReachesAForgeCommandAndEveryOtherBuiltEnvironment(t *testing.T) {
	t.Parallel()

	parent := []string{
		"PATH=/usr/bin",
		"SSH_AUTH_SOCK=/private/tmp/agent.sock",
		"GH_TOKEN=ghp-secret",
		"SLACK_BOT_TOKEN=xoxb-secret",
	}
	for name, built := range map[string][]string{
		"ForgeEnvironment":    ForgeEnvironment(parent),
		"GitEnvironment":      GitEnvironment(parent),
		"ExplicitEnvironment": ExplicitEnvironment(parent),
	} {
		if !slices.Contains(built, "SSH_AUTH_SOCK=/private/tmp/agent.sock") {
			t.Errorf("%s() = %v, want the agent socket carried", name, built)
		}
	}
}

// One name reaches a process once. Which of two entries of a name a process
// reads is the operating system's to decide, and a credential that depended on
// that would be one nobody could reason about.
func TestForgeEnvironmentNamesEachVariableOnce(t *testing.T) {
	t.Parallel()

	seen := map[string]int{}
	for _, entry := range ForgeEnvironment([]string{
		"PATH=/usr/bin",
		"GH_TOKEN=ghp-secret",
		"GH_CONFIG_DIR=/home/operator/.config/gh",
		"XDG_CONFIG_HOME=/home/operator/.config",
	}) {
		name, _, _ := strings.Cut(entry, "=")
		seen[name]++
	}
	for name, count := range seen {
		if count != 1 {
			t.Errorf("%s appears %d times in a forge environment", name, count)
		}
	}
}

// A Git command the harness runs is given exactly what an agent invocation is,
// because a hook the repository supplies is a program the harness executes.
func TestGitEnvironmentIsTheSameAllowlistAnInvocationGets(t *testing.T) {
	t.Parallel()

	parent := []string{"PATH=/usr/bin", "SLACK_BOT_TOKEN=xoxb-secret", "GH_TOKEN=ghp-secret"}
	if got, want := GitEnvironment(parent), ExplicitEnvironment(parent); !slices.Equal(got, want) {
		t.Fatalf("GitEnvironment() = %v, want %v", got, want)
	}
	if got := GitEnvironment(parent); !slices.Equal(got, []string{"PATH=/usr/bin"}) {
		t.Fatalf("GitEnvironment() = %v, want the allowlist alone", got)
	}
}

// The keys an installation may have been authenticating a provider with are
// named in one place, so the surfaces that warn about them cannot disagree
// about which they are.
func TestProviderKeysInEnvironmentNamesTheKeysAndOnlyTheKeys(t *testing.T) {
	t.Parallel()

	got := ProviderKeysInEnvironment([]string{
		"OPENAI_API_KEY=sk-openai",
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=sk-anthropic",
		"CLAUDE_CODE_OAUTH_TOKEN=   ",
		"GH_TOKEN=ghp-secret",
	})
	// In the order the list states rather than the order the environment
	// happened to carry, so two surfaces say one sentence.
	want := []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"}
	if !slices.Equal(got, want) {
		t.Fatalf("ProviderKeysInEnvironment() = %v, want %v", got, want)
	}
	// Every one of them is a name the environment builder drops, which is what
	// makes the warning true.
	for _, name := range ProviderKeyNames {
		if !sensitiveEnvironmentName(name) {
			t.Errorf("%s is named as a provider key and is not dropped from a built environment", name)
		}
	}
	if len(ProviderKeysInEnvironment(nil)) > len(ProviderKeyNames) {
		t.Error("ProviderKeysInEnvironment(nil) reported more keys than there are names")
	}
}
