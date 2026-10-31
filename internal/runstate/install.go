package runstate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// InstallRun is one run of one of the project's declared install steps in one
// of this run's checkouts (docs/designs/project-dependencies-in-runs.md): what
// the step is called, the digest of the files that decide what it installs,
// when it started and how long it took, how it ended, and the end of what it
// printed, scrubbed of secrets by the check runner that ran it.
type InstallRun struct {
	// Name is the step's name as the project declared it.
	Name string `json:"name"`
	// Directory is the checkout the step ran in.
	Directory string `json:"directory"`
	// InputDigest is "sha256:" and the digest of the step's inputs as they
	// stood in that checkout when it started.
	InputDigest string    `json:"input_digest"`
	StartedAt   time.Time `json:"started_at"`
	// DurationMilliseconds is how long the step ran.
	DurationMilliseconds int64         `json:"duration_milliseconds"`
	Result               InstallResult `json:"result"`
	// ExitCode is the command's exit status, zero where it passed or was
	// stopped before it exited on its own.
	ExitCode int `json:"exit_code,omitempty"`
	// Output is the last InstallOutputLines lines the step printed, at most
	// MaxInstallOutputBytes.
	Output string `json:"output,omitempty"`
}

// InstallResult is how one run of an install step ended.
type InstallResult string

const (
	// InstallPassed is a step whose command exited successfully.
	InstallPassed InstallResult = "passed"
	// InstallFailed is a step whose command exited with a failure.
	InstallFailed InstallResult = "failed"
	// InstallTimedOut is a step stopped at its time limit.
	InstallTimedOut InstallResult = "timed_out"
	// InstallStopped is a step stopped because the run itself was stopped, or
	// whose command could not be started.
	InstallStopped InstallResult = "stopped"
)

// InstallOutputLines and MaxInstallOutputBytes bound what an InstallRun keeps
// of the step's output: its last 200 lines, and of those no more than the last
// 16 KiB.
const (
	InstallOutputLines    = 200
	MaxInstallOutputBytes = 16 << 10
)

var installDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Validate holds a recorded step run to the shapes the harness writes.
func (r InstallRun) Validate() error {
	var problems []error
	if strings.TrimSpace(r.Name) == "" {
		problems = append(problems, errors.New("name is required"))
	}
	if !installDigestPattern.MatchString(r.InputDigest) {
		problems = append(problems, fmt.Errorf("input digest %q is not a sha256 digest", r.InputDigest))
	}
	switch r.Result {
	case InstallPassed, InstallFailed, InstallTimedOut, InstallStopped:
	default:
		problems = append(problems, fmt.Errorf("result %q is not one an install step ends with", r.Result))
	}
	if r.DurationMilliseconds < 0 {
		problems = append(problems, errors.New("duration cannot be negative"))
	}
	if len(r.Output) > MaxInstallOutputBytes {
		problems = append(problems, fmt.Errorf("output is %d bytes, over the %d an install step keeps", len(r.Output), MaxInstallOutputBytes))
	}
	return errors.Join(problems...)
}

// Duration is how long the step ran.
func (r InstallRun) Duration() time.Duration {
	return time.Duration(r.DurationMilliseconds) * time.Millisecond
}

// Describe says in one sentence what became of the step.
func (r InstallRun) Describe() string {
	took := r.Duration().Round(time.Second)
	switch r.Result {
	case InstallPassed:
		return fmt.Sprintf("the install step %q finished in %s", r.Name, took)
	case InstallTimedOut:
		return fmt.Sprintf("the install step %q was stopped at its time limit after %s", r.Name, took)
	case InstallStopped:
		return fmt.Sprintf("the install step %q was stopped after %s before it finished", r.Name, took)
	default:
		return fmt.Sprintf("the install step %q failed with exit status %d after %s", r.Name, r.ExitCode, took)
	}
}

// InstallOutputTail is the part of a step's output a record keeps: its last
// InstallOutputLines lines, cut further from the front to the last
// MaxInstallOutputBytes, never in the middle of a character.
func InstallOutputTail(lines []string) string {
	if len(lines) > InstallOutputLines {
		lines = lines[len(lines)-InstallOutputLines:]
	}
	text := strings.Join(lines, "\n")
	if len(text) <= MaxInstallOutputBytes {
		return text
	}
	cut := len(text) - MaxInstallOutputBytes
	for cut < len(text) && !utf8.RuneStart(text[cut]) {
		cut++
	}
	return text[cut:]
}
