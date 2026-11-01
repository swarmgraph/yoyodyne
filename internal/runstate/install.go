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

// InstallFailureCause is whose an install step's failure is, decided from the
// digests the runs of it recorded (docs/designs/project-dependencies-in-runs.md,
// "When the step fails"). Only InstallCauseChange spends a repair round.
type InstallFailureCause string

const (
	// InstallCauseChange is a failure on inputs the run's change altered: the
	// change's to repair, handed back as a failed check is.
	InstallCauseChange InstallFailureCause = "change"
	// InstallCauseOutside is a failure on inputs unchanged from the run's start
	// that have installed on this machine before, so what failed is almost
	// always the network or the package registry. It is tried again, and held
	// for the development manager once those tries are spent.
	InstallCauseOutside InstallFailureCause = "outside"
	// InstallCauseNeverInstalled is a failure on unchanged inputs that have never
	// installed on this machine: the step or the machine is wrong rather than
	// the change, so the run is held for the development manager at once.
	InstallCauseNeverInstalled InstallFailureCause = "never-installed"
)

// SortInstallFailure says whose failed is. startingDigest is the digest of the
// step's inputs as the run's starting target had them, and installedBefore
// whether a run of the same step on this machine has ever installed the inputs
// failed ran on.
func SortInstallFailure(failed InstallRun, startingDigest string, installedBefore bool) InstallFailureCause {
	switch {
	case failed.InputDigest != startingDigest:
		return InstallCauseChange
	case installedBefore:
		return InstallCauseOutside
	default:
		return InstallCauseNeverInstalled
	}
}

// InstallRetries is how many times a step that failed for a cause outside the
// work is tried again, and InstallRetryInterval how long the harness waits
// before each try, before the run is held for the development manager.
const (
	InstallRetries       = 3
	InstallRetryInterval = 5 * time.Minute
)

// InstallWait is a run waiting to try an install step again after it failed for
// a cause outside the work. It is on the record only while the wait lasts, so
// every surface showing the run can say what it is waiting for and when it
// tries next.
type InstallWait struct {
	// Name is the step's name as the project declared it.
	Name string `json:"name"`
	// Retry is which of the InstallRetries tries the run is waiting to make,
	// counted from 1.
	Retry int `json:"retry"`
	// NextAt is when that try starts.
	NextAt time.Time `json:"next_at"`
}

// Validate holds a recorded wait to the shapes the harness writes.
func (w InstallWait) Validate() error {
	var problems []error
	if strings.TrimSpace(w.Name) == "" {
		problems = append(problems, errors.New("name is required"))
	}
	if w.Retry < 1 || w.Retry > InstallRetries {
		problems = append(problems, fmt.Errorf("retry %d is not one of the %d tries", w.Retry, InstallRetries))
	}
	if w.NextAt.IsZero() {
		problems = append(problems, errors.New("next attempt time is required"))
	}
	return errors.Join(problems...)
}

// Describe says in one sentence what the run is waiting for, naming the time of
// the next try in the operator's local time with its zone.
func (w InstallWait) Describe() string {
	return fmt.Sprintf("the install step %q failed on dependency files that have installed on this machine before, so the cause is most likely the network or the package registry; trying again at %s (try %d of %d)",
		w.Name, w.NextAt.Local().Format("15:04 MST"), w.Retry, InstallRetries)
}
