package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// Establishing, before a developer is started, that the CLI put in force what
// the developer is launched with.
//
// A developer's sandbox, its notes guard, and the settings that keep the
// operator's memory and connectors out all travel in the one JSON payload passed
// with --settings (context.go). Claude Code's help says settings that fail
// validation are silently ignored in print mode, and that is what 2.1.286 does:
// one key of the wrong type and it drops the whole payload, starts anyway, and
// reports the rejection only to whoever asks. A later CLI that rejected any part
// of the payload would therefore start every developer unconfined and unguarded
// with nothing failing.
//
// So the harness asks. The CLI answers questions about its own state over the
// control protocol of --input-format stream-json without a model being called:
// get_settings gives the merged settings it applied and every settings file it
// rejected, get_hooks_listing the hooks it will run, and get_sandbox_dialog
// whether its sandbox is actually running. The check launches the CLI exactly
// as a developer is launched — the same settings, the same flags, the same
// directory and environment — asks those three questions, closes its input, and
// judges the answers. No prompt is sent, so no provider call is made, and
// --no-session-persistence keeps it from writing a session. Against 2.1.286 it
// takes under a second.
//
// Whether a write outside the worktree is refused is established from the
// CLI's own account of its sandbox rather than by attempting one: only the
// model can ask the CLI to run a shell command, and a model call before every
// run is a cost and a dependence on what the model chooses to do. The sandbox
// answer is the CLI's runtime state — supported, enabled, with no dependency
// errors and no fallback to running unsandboxed — not a reading back of the
// settings it was handed.
//
// The flags were read from 2.1.286's help, recorded in testdata/cli-help. On
// any other version the check also asks the installed CLI for its help and
// confirms every flag this adapter passes is still named there. The settings
// keys cannot be confirmed that way, since the help names none, and the CLI
// keeps a key it does not know without complaint; what stands behind them is
// that each one's effect is read back — the sandbox running, the guard listed,
// memory and connectors off, the excluded files excluded — rather than its
// spelling.

// recordedCLIVersion is the Claude Code version the flags and settings keys
// this adapter passes were read from.
const recordedCLIVersion = "2.1.286"

// reliedFlags is every flag an invocation of this adapter passes, and the ones
// the check itself and the availability check pass. A test holds it to the
// recorded help and holds every flag Run passes to it, so a flag added to an
// invocation is a flag this check confirms on an upgraded CLI.
var reliedFlags = []string{
	"-p", "--output-format", "--verbose", "--permission-mode", "--name", "--settings",
	"--setting-sources", "--strict-mcp-config", "--disable-slash-commands", "--safe-mode",
	"--exclude-dynamic-system-prompt-sections", "--allowedTools", "--tools",
	"--append-system-prompt", "--resume", "--model", "--effort",
	"--input-format", "--no-session-persistence", "--version",
}

// settingsCheckTimeout bounds one of the check's processes. Each answers in
// well under a second; the bound is for a CLI that hangs, which reads as a
// check that could not be made rather than as one that failed.
const settingsCheckTimeout = 30 * time.Second

// guardCommand is the notes guard's command as the developer's settings name
// it, and guardMatcher the tool it stands in front of.
const (
	guardCommand = "yoyo goals guard"
	guardMatcher = "Bash"
)

// The three questions the check asks, by the request identifiers their answers
// come back under.
const (
	settingsQuestion = "settings"
	hooksQuestion    = "hooks"
	sandboxQuestion  = "sandbox"
)

var settingsQuestions = func() string {
	var questions strings.Builder
	for _, question := range [][2]string{
		{settingsQuestion, "get_settings"},
		{hooksQuestion, "get_hooks_listing"},
		{sandboxQuestion, "get_sandbox_dialog"},
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

// CheckLaunchSettings establishes that the installed CLI puts in force, in
// directory, what a developer is launched with there. See the top of this file.
func (b Backend) CheckLaunchSettings(ctx context.Context, directory string) (backend.LaunchSettingsCheck, error) {
	if b.Runner == nil {
		return backend.LaunchSettingsCheck{}, errors.New("Claude Code process runner is required")
	}
	if strings.TrimSpace(directory) == "" {
		return backend.LaunchSettingsCheck{}, errors.New("the directory a developer would be started in is required")
	}
	environment := withContextEnvironment(environmentFor(b.ConfigDir))
	versionText, err := b.ask(ctx, environment, directory, "--version")
	if err != nil {
		return backend.LaunchSettingsCheck{}, fmt.Errorf("ask Claude Code its version: %w", err)
	}
	check := backend.LaunchSettingsCheck{Version: strings.TrimSpace(versionText)}
	version := cliVersion(check.Version)
	if version != recordedCLIVersion {
		help, err := b.ask(ctx, environment, directory, "--help")
		if err != nil {
			return backend.LaunchSettingsCheck{}, fmt.Errorf("ask Claude Code %s for its help: %w", version, err)
		}
		for _, flag := range reliedFlags {
			if !helpNames(help, flag) {
				check.NotInForce = append(check.NotInForce, fmt.Sprintf("the flag %s is not in its help (it was read from %s)", flag, recordedCLIVersion))
			}
		}
	}

	settings, err := settingsFor(developerSettings, domain.RoleDeveloper, directory)
	if err != nil {
		return backend.LaunchSettingsCheck{}, err
	}
	result, err := b.Runner.Run(ctx, execution.Command{
		Name:    b.binary(),
		Args:    settingsCheckArgs(settings),
		Dir:     directory,
		Env:     execution.WithGoBuildCache(environment, directory),
		Stdin:   strings.NewReader(settingsQuestions),
		Timeout: settingsCheckTimeout,
	}, nil)
	if err != nil {
		return backend.LaunchSettingsCheck{}, fmt.Errorf("ask Claude Code which settings it applies: %w", err)
	}
	if result.Status == execution.ProcessTimedOut || result.Status == execution.ProcessCancelled || result.Status == execution.ProcessStalled {
		return backend.LaunchSettingsCheck{}, fmt.Errorf("ask Claude Code which settings it applies: the process %s", result.Status)
	}
	answers := readAnswers(result.Stdout)
	if len(answers) == 0 {
		said := oneline.Fold(strings.TrimSpace(result.Stderr+" "+result.Stdout), 400)
		if said == "" {
			said = "it said nothing"
		}
		check.NotInForce = append(check.NotInForce, fmt.Sprintf("it answered none of the harness's questions about its settings, sandbox, and hooks (exit %d: %s), so none of them could be established", result.ExitCode, said))
		return check, nil
	}
	var passed map[string]any
	if err := json.Unmarshal([]byte(settings), &passed); err != nil {
		return backend.LaunchSettingsCheck{}, fmt.Errorf("decode the settings a developer is given: %w", err)
	}
	check.NotInForce = append(check.NotInForce, judgeSettings(answers[settingsQuestion], passed)...)
	check.NotInForce = append(check.NotInForce, judgeGuard(answers[hooksQuestion])...)
	check.NotInForce = append(check.NotInForce, judgeSandbox(answers[sandboxQuestion])...)
	return check, nil
}

// settingsCheckArgs is the developer's launch with its questions in place of a
// prompt: the same permission mode, settings, context flags and tools, read
// from stream-json input so the control questions can be put, and with no
// session written.
func settingsCheckArgs(settings string) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", worktreeWriteSessionMode,
		"--no-session-persistence",
		"--settings", settings,
	}
	args = append(args, contextArgs(domain.RoleDeveloper)...)
	args = append(args, "--allowedTools")
	return append(args, developerTools...)
}

// ask runs the CLI with one argument and returns what it printed.
func (b Backend) ask(ctx context.Context, environment []string, directory, flag string) (string, error) {
	result, err := b.Runner.Run(ctx, execution.Command{Name: b.binary(), Args: []string{flag}, Dir: directory, Env: environment, Timeout: settingsCheckTimeout}, nil)
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("%s ended %s (exit %d): %s", flag, result.Status, result.ExitCode, oneline.Fold(strings.TrimSpace(result.Stderr), 400))
	}
	return result.Stdout, nil
}

// cliVersion is the version number out of the CLI's answer to --version, which
// 2.1.286 gives as "2.1.286 (Claude Code)".
func cliVersion(answer string) string {
	fields := strings.Fields(answer)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// helpNames reports a flag the help lists as an option: the flag as a whole
// word, at the start of an option line or after the comma that separates its
// spellings.
func helpNames(help, flag string) bool {
	return regexp.MustCompile(`(?m)^\s*(?:\S+,\s+)*` + regexp.QuoteMeta(flag) + `(?:[\s,]|$)`).MatchString(help)
}

// answer is one control response: the question it answers, and either what the
// CLI said or the error it gave instead.
type answer struct {
	Subtype  string          `json:"subtype"`
	ID       string          `json:"request_id"`
	Response json.RawMessage `json:"response"`
	Error    string          `json:"error"`
}

// readAnswers takes every control response out of what the CLI printed, keyed
// by the question it answers. Anything else in the stream — a system event, a
// line that is not JSON — is not an answer and is passed over.
func readAnswers(stdout string) map[string]answer {
	answers := map[string]answer{}
	for _, line := range strings.Split(stdout, "\n") {
		var envelope struct {
			Type     string `json:"type"`
			Response answer `json:"response"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &envelope) != nil || envelope.Type != "control_response" {
			continue
		}
		answers[envelope.Response.ID] = envelope.Response
	}
	return answers
}

// unanswered names a question the CLI did not answer, or answered with an
// error.
func unanswered(given answer, what string) (string, bool) {
	switch {
	case given.ID == "":
		return fmt.Sprintf("%s could not be established: it did not answer when asked", what), true
	case given.Subtype != "success":
		return fmt.Sprintf("%s could not be established: it answered %q", what, oneline.Fold(given.Error, 400)), true
	}
	return "", false
}

// judgeSettings reads back the settings the CLI applied against those passed.
// A settings file it rejected is said first and in its own words, because it
// is the cause of everything else that did not take. Then each key passed is
// looked for in the merged settings with the value it was passed, except
// claudeMdExcludes, which other sources may add to, so each pattern passed has
// to be among those merged. The hooks are left to judgeGuard, which reads the
// hooks the CLI will actually run.
func judgeSettings(given answer, passed map[string]any) []string {
	if missing, failed := unanswered(given, "which settings it applied"); failed {
		return []string{missing}
	}
	var applied struct {
		Effective map[string]any `json:"effective"`
		Errors    []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(given.Response, &applied); err != nil {
		return []string{fmt.Sprintf("which settings it applied could not be established: its answer could not be read (%v)", err)}
	}
	var missing []string
	for _, rejected := range applied.Errors {
		missing = append(missing, fmt.Sprintf("it rejected the setting %s (%s), which makes it ignore the whole settings payload the sandbox and the guard travel in",
			rejected.Path, oneline.Fold(rejected.Message, 200)))
	}
	keys := make([]string, 0, len(passed))
	for key := range passed {
		if key != "hooks" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		if !appliedAsPassed(key, passed[key], applied.Effective[key]) {
			missing = append(missing, fmt.Sprintf("the setting %s is not applied as passed (%s)", key, describeSetting(key)))
		}
	}
	return missing
}

// appliedAsPassed reports a passed setting the merged settings carry as
// passed. claudeMdExcludes is the one list another source may extend, so it is
// held to containing every pattern passed rather than to equality.
func appliedAsPassed(key string, passed, applied any) bool {
	if key != "claudeMdExcludes" {
		return reflect.DeepEqual(passed, applied)
	}
	wanted, _ := passed.([]any)
	have, _ := applied.([]any)
	for _, pattern := range wanted {
		if !slices.Contains(have, pattern) {
			return false
		}
	}
	return true
}

// describeSetting says what a settings key is for, so the line naming it says
// what a developer started without it would be missing.
func describeSetting(key string) string {
	switch key {
	case "sandbox":
		return "the sandbox that confines a developer's shell to its worktree"
	case "autoMemoryEnabled":
		return "keeps the operator's memory out"
	case "disableClaudeAiConnectors":
		return "keeps the operator's claude.ai connectors out"
	case "claudeMdExcludes":
		return "keeps instruction files above the worktree out"
	default:
		return "passed to every developer"
	}
}

// judgeGuard looks for the notes guard among the hooks the CLI will run.
func judgeGuard(given answer) []string {
	what := fmt.Sprintf("the notes guard (`%s` before every %s command)", guardCommand, guardMatcher)
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
	for _, hook := range listing.Hooks {
		if hook.Event == "PreToolUse" && hook.Matcher == guardMatcher && strings.TrimSpace(hook.CommandText) == guardCommand && !hook.Disabled {
			return nil
		}
	}
	return []string{what + " is not among the hooks it will run"}
}

// judgeSandbox reads whether the sandbox is running: supported here, enabled,
// with nothing it depends on missing, and not falling back to running a
// command unconfined.
func judgeSandbox(given answer) []string {
	const what = "the sandbox that confines a developer's shell to its worktree"
	if missing, failed := unanswered(given, what); failed {
		return []string{missing}
	}
	var sandbox struct {
		Supported           bool `json:"supported"`
		Enabled             bool `json:"enabled"`
		UnsandboxedFallback bool `json:"unsandboxed_fallback"`
		Dependencies        struct {
			Errors []string `json:"errors"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(given.Response, &sandbox); err != nil {
		return []string{fmt.Sprintf("%s could not be established: its answer could not be read (%v)", what, err)}
	}
	var why []string
	if !sandbox.Supported {
		why = append(why, "not supported on this machine")
	}
	if !sandbox.Enabled {
		why = append(why, "not enabled")
	}
	if sandbox.UnsandboxedFallback {
		why = append(why, "would run a command unconfined when it cannot sandbox it")
	}
	if len(sandbox.Dependencies.Errors) > 0 {
		why = append(why, "missing what it depends on: "+oneline.Fold(strings.Join(sandbox.Dependencies.Errors, "; "), 300))
	}
	if len(why) == 0 {
		return nil
	}
	return []string{what + " is not running: it is " + strings.Join(why, ", ")}
}
