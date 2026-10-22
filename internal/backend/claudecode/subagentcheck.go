package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// Establishing, before a developer invocation is given sub-agents, that the
// installed CLI applies what they are launched with (subagents.go).
//
// It is the launch-settings check of settingscheck.go asked of the launch a
// run with sub-agents makes: the same flags, the run's own sub-agent
// definition, the settings with the sub-agent guard among their hooks, and the
// environment that confines sub-agents. No prompt is sent, so no provider call
// is made. Beside the settings and hooks questions it asks `initialize`, whose
// answer lists the sub-agents the session has loaded with the model each runs
// on; testdata/settings-check/claude-2.1.286-subagents.jsonl is what 2.1.286
// answered.
//
// What it confirms, on whatever version is installed:
//
//   - the CLI names --agents and --forward-subagent-text in its help, on any
//     version other than the one they were read from;
//   - it loaded the run's own sub-agent, on the model the run configured;
//   - it rejected none of the settings the guard travels in;
//   - it will run the guard that refuses every other sub-agent type and any
//     sub-agent asked to run in the background.
//
// What it cannot confirm without a model call is that a sub-agent still runs
// inside the session's process and that the terminal still reports usage for
// each model; both rest on the recorded version (claudeCodeSubagents), and a
// terminal without per-model usage is recorded as an unknown split.

// subagentFlags are the flags only an invocation with sub-agents passes. They
// are confirmed here rather than among reliedFlags, because a CLI that dropped
// either one should take sub-agents away from the runs that use them and not
// stop every developer from starting.
var subagentFlags = []string{"--agents", "--forward-subagent-text"}

// initializeQuestion is the request identifier the check's question about the
// loaded sub-agents comes back under.
const initializeQuestion = "initialize"

var subagentQuestions = func() string {
	var questions strings.Builder
	for _, question := range [][2]string{
		{initializeQuestion, "initialize"},
		{settingsQuestion, "get_settings"},
		{hooksQuestion, "get_hooks_listing"},
	} {
		encoded, _ := json.Marshal(map[string]any{
			"type":       "control_request",
			"request_id": question[0],
			"request":    map[string]string{"subtype": question[1]},
		})
		questions.Write(encoded)
		questions.WriteByte('\n')
	}
	return questions.String()
}()

// subagentLaunch is what a developer invocation with sub-agents is launched
// with beside the developer's own: the sub-agent's name, its definition, the
// allow rules for the developer's tools and that sub-agent, and the settings
// with the sub-agent guard among their hooks. Run and the check both build it
// here, so the check asks about the launch a run makes.
func subagentLaunch(runID string, request backend.SubagentRequest, allowed []string, base string) (name, definition string, rules []string, settings string, err error) {
	name = subagentName(runID)
	if definition, err = subagentDefinition(name, request, allowed); err != nil {
		return "", "", nil, "", err
	}
	if settings, err = withSubagentSettings(base, name); err != nil {
		return "", "", nil, "", err
	}
	rules = append(append([]string(nil), allowed...), subagentAllowRules(name)...)
	return name, definition, rules, settings, nil
}

// CheckSubagentLaunch establishes that the installed CLI applies, in
// directory, what a developer invocation of the run with these sub-agents is
// launched with. See the top of this file.
func (b Backend) CheckSubagentLaunch(ctx context.Context, directory, runID string, subagents backend.SubagentRequest) (backend.LaunchSettingsCheck, error) {
	if b.Runner == nil {
		return backend.LaunchSettingsCheck{}, errors.New("Claude Code process runner is required")
	}
	if strings.TrimSpace(directory) == "" || strings.TrimSpace(runID) == "" {
		return backend.LaunchSettingsCheck{}, errors.New("the directory and the run a developer would be started for are required")
	}
	environment := withSubagentEnvironment(withContextEnvironment(environmentFor(b.ConfigDir)))
	versionText, err := b.ask(ctx, environment, directory, "--version")
	if err != nil {
		return backend.LaunchSettingsCheck{}, fmt.Errorf("ask Claude Code its version: %w", err)
	}
	check := backend.LaunchSettingsCheck{Version: strings.TrimSpace(versionText)}
	if version := cliVersion(check.Version); version != recordedCLIVersion {
		help, err := b.ask(ctx, environment, directory, "--help")
		if err != nil {
			return backend.LaunchSettingsCheck{}, fmt.Errorf("ask Claude Code %s for its help: %w", version, err)
		}
		for _, flag := range subagentFlags {
			if !helpNames(help, flag) {
				check.NotInForce = append(check.NotInForce, fmt.Sprintf("the flag %s is not in its help (it was read from %s)", flag, recordedCLIVersion))
			}
		}
	}

	name, definition, rules, base, err := subagentLaunch(runID, subagents, developerTools, developerSettings)
	if err != nil {
		return backend.LaunchSettingsCheck{}, err
	}
	settings, err := settingsFor(base, true, directory)
	if err != nil {
		return backend.LaunchSettingsCheck{}, err
	}
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", worktreeWriteSessionMode,
		"--no-session-persistence",
		"--settings", settings,
	}
	args = append(args, contextArgs(true)...)
	args = append(args, "--agents", definition, "--forward-subagent-text", "--allowedTools")
	args = append(args, rules...)
	result, err := b.Runner.Run(ctx, execution.Command{
		Name:    b.binary(),
		Args:    args,
		Dir:     directory,
		Env:     execution.WithGoBuildCache(environment, directory),
		Stdin:   strings.NewReader(subagentQuestions),
		Timeout: settingsCheckTimeout,
	}, nil)
	if err != nil {
		return backend.LaunchSettingsCheck{}, fmt.Errorf("ask Claude Code whether it applies what sub-agents are launched with: %w", err)
	}
	if result.Status == execution.ProcessTimedOut || result.Status == execution.ProcessCancelled || result.Status == execution.ProcessStalled {
		return backend.LaunchSettingsCheck{}, fmt.Errorf("ask Claude Code whether it applies what sub-agents are launched with: the process %s", result.Status)
	}
	answers := readAnswers(result.Stdout)
	if len(answers) == 0 {
		said := oneline.Fold(strings.TrimSpace(result.Stderr+" "+result.Stdout), 400)
		if said == "" {
			said = "it said nothing"
		}
		check.NotInForce = append(check.NotInForce, fmt.Sprintf("it answered none of the harness's questions about sub-agents (exit %d: %s)", result.ExitCode, said))
		return check, nil
	}
	var passed map[string]any
	if err := json.Unmarshal([]byte(settings), &passed); err != nil {
		return backend.LaunchSettingsCheck{}, fmt.Errorf("decode the settings a developer with sub-agents is given: %w", err)
	}
	check.NotInForce = append(check.NotInForce, judgeLoadedSubagent(answers[initializeQuestion], name, subagents.Model)...)
	check.NotInForce = append(check.NotInForce, judgeSettings(answers[settingsQuestion], passed)...)
	check.NotInForce = append(check.NotInForce, judgeSubagentGuard(answers[hooksQuestion], name)...)
	return check, nil
}

// judgeLoadedSubagent looks for the run's own sub-agent among those the
// session loaded, on the model it was defined with.
func judgeLoadedSubagent(given answer, name, model string) []string {
	what := fmt.Sprintf("the run's own sub-agent %s", name)
	if missing, failed := unanswered(given, what); failed {
		return []string{missing}
	}
	var loaded struct {
		Agents []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(given.Response, &loaded); err != nil {
		return []string{fmt.Sprintf("%s could not be established: its answer could not be read (%v)", what, err)}
	}
	for _, agent := range loaded.Agents {
		if agent.Name != name {
			continue
		}
		if strings.TrimSpace(agent.Model) != strings.TrimSpace(model) {
			return []string{fmt.Sprintf("%s is loaded on model %q, not the %q it was defined with", what, agent.Model, model)}
		}
		return nil
	}
	return []string{what + " is not among the sub-agents it loaded"}
}

// judgeSubagentGuard looks for the sub-agent guard among the hooks the CLI
// will run.
func judgeSubagentGuard(given answer, name string) []string {
	const what = "the hook that refuses every other sub-agent and any in the background"
	if missing, failed := unanswered(given, what); failed {
		return []string{missing}
	}
	var listing struct {
		Hooks []struct {
			Event       string `json:"event"`
			Matcher     string `json:"matcher"`
			CommandText string `json:"commandText"`
			Disabled    bool   `json:"disabled"`
		} `json:"hooks"`
		Policy struct {
			AllDisabled bool `json:"allDisabled"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(given.Response, &listing); err != nil {
		return []string{fmt.Sprintf("%s could not be established: its answer could not be read (%v)", what, err)}
	}
	if listing.Policy.AllDisabled {
		return []string{what + " will not run: its policy disables every hook"}
	}
	matcher, command := strings.Join(subagentToolNames, "|"), subagentGuard(name)
	for _, hook := range listing.Hooks {
		if hook.Event == "PreToolUse" && hook.Matcher == matcher && strings.TrimSpace(hook.CommandText) == command && !hook.Disabled {
			return nil
		}
	}
	return []string{what + " is not among the hooks it will run"}
}
