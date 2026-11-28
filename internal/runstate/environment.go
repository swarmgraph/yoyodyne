package runstate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EnvironmentCheck is one run of the check of one environment a work item's
// Environments: line names, made before a developer turn
// (docs/designs/checking-a-run-environment.md, section 5): the environment's
// name, how its check ended, where it ran, the criteria it serves, and the end
// of what it printed, scrubbed of secrets by the check runner that ran it.
// Each environment's result is its own entry, so one passing never hides
// another.
type EnvironmentCheck struct {
	// Name is the environment's name as the project declared it.
	Name string `json:"name"`
	// Result is how the check ended.
	Result EnvironmentResult `json:"result"`
	// Where is where the check ran: "developer", under the developer
	// session's restrictions, or "harness", where the declared checks run.
	Where string `json:"where"`
	// Criteria are the numbers of the item's criteria the environment serves,
	// as its Environments: line names them.
	Criteria []int `json:"criteria,omitempty"`
	// Machine says the environment needs something installed for the whole
	// machine, and HowToInstall is the instruction a person follows to install
	// it, quoted as the project wrote it and never run.
	Machine      bool      `json:"machine,omitempty"`
	HowToInstall string    `json:"how_to_install,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	// DurationMilliseconds is how long the check ran.
	DurationMilliseconds int64 `json:"duration_milliseconds"`
	// ExitCode is the check's exit status, zero where it passed or did not
	// exit on its own.
	ExitCode int `json:"exit_code,omitempty"`
	// Output is the last InstallOutputLines lines the check printed, at most
	// MaxInstallOutputBytes.
	Output string `json:"output,omitempty"`
	// Commit is the commit an environment's test ran on, the developer's
	// submitted change as the harness committed it. It is empty on a check made
	// before a developer turn, which judges the machine rather than a change.
	Commit string `json:"commit,omitempty"`
}

// EnvironmentResult is how one run of an environment's check ended.
type EnvironmentResult string

const (
	// EnvironmentPassed is a check that exited successfully: the environment
	// is usable by the run.
	EnvironmentPassed EnvironmentResult = "passed"
	// EnvironmentFailed is a check that exited with a failure, or whose
	// command could not be started.
	EnvironmentFailed EnvironmentResult = "failed"
	// EnvironmentTimedOut is a check stopped at its time limit.
	EnvironmentTimedOut EnvironmentResult = "timed_out"
)

// Validate holds a recorded check to the shapes the harness writes.
func (c EnvironmentCheck) Validate() error {
	var problems []error
	if strings.TrimSpace(c.Name) == "" {
		problems = append(problems, errors.New("name is required"))
	}
	switch c.Result {
	case EnvironmentPassed, EnvironmentFailed, EnvironmentTimedOut:
	default:
		problems = append(problems, fmt.Errorf("result %q is not one an environment's check ends with", c.Result))
	}
	switch c.Where {
	case "developer", "harness":
	default:
		problems = append(problems, fmt.Errorf("where %q is neither developer nor harness", c.Where))
	}
	if c.DurationMilliseconds < 0 {
		problems = append(problems, errors.New("duration cannot be negative"))
	}
	if c.Commit != "" && !commitPattern.MatchString(c.Commit) {
		problems = append(problems, fmt.Errorf("commit %q is not a commit", c.Commit))
	}
	if len(c.Output) > MaxInstallOutputBytes {
		problems = append(problems, fmt.Errorf("output is %d bytes, over the %d an environment's check keeps", len(c.Output), MaxInstallOutputBytes))
	}
	return errors.Join(problems...)
}

// Passed reports whether the environment is usable by the run.
func (c EnvironmentCheck) Passed() bool { return c.Result == EnvironmentPassed }

// Serves names the criteria the environment serves, such as "criteria 1, 2",
// or "criteria the item does not number" where its line named none.
func (c EnvironmentCheck) Serves() string {
	if len(c.Criteria) == 0 {
		return "criteria the item does not number"
	}
	numbers := make([]string, 0, len(c.Criteria))
	for _, number := range c.Criteria {
		numbers = append(numbers, strconv.Itoa(number))
	}
	if len(numbers) == 1 {
		return "criterion " + numbers[0]
	}
	return "criteria " + strings.Join(numbers, ", ")
}

// PlaceSays is where the check ran, in words.
func (c EnvironmentCheck) PlaceSays() string {
	if c.Where == "harness" {
		return "where the declared checks run"
	}
	return "under the developer session's restrictions"
}

// Describe says in one sentence what became of the check.
func (c EnvironmentCheck) Describe() string {
	took := (time.Duration(c.DurationMilliseconds) * time.Millisecond).Round(time.Second)
	switch c.Result {
	case EnvironmentPassed:
		return fmt.Sprintf("the check of the environment %q, for %s, passed %s in %s", c.Name, c.Serves(), c.PlaceSays(), took)
	case EnvironmentTimedOut:
		return fmt.Sprintf("the check of the environment %q, for %s, did not finish in time and was stopped at its time limit after %s %s", c.Name, c.Serves(), took, c.PlaceSays())
	default:
		return fmt.Sprintf("the check of the environment %q, for %s, failed with exit status %d after %s %s", c.Name, c.Serves(), c.ExitCode, took, c.PlaceSays())
	}
}

// DescribeTest says in one sentence what became of one run of the
// environment's test, naming the commit it ran on.
func (c EnvironmentCheck) DescribeTest() string {
	took := (time.Duration(c.DurationMilliseconds) * time.Millisecond).Round(time.Second)
	switch c.Result {
	case EnvironmentPassed:
		return fmt.Sprintf("the test of the environment %q, for %s, passed at commit %s %s in %s", c.Name, c.Serves(), c.Commit, c.PlaceSays(), took)
	case EnvironmentTimedOut:
		return fmt.Sprintf("the test of the environment %q, for %s, did not finish in time at commit %s and was stopped at its time limit after %s %s", c.Name, c.Serves(), c.Commit, took, c.PlaceSays())
	default:
		return fmt.Sprintf("the test of the environment %q, for %s, failed with exit status %d at commit %s after %s %s", c.Name, c.Serves(), c.ExitCode, c.Commit, took, c.PlaceSays())
	}
}

// Missing says what a person reading a held item or a run is told about an
// environment whose check did not pass: its name, what it blocks, and, for a
// machine environment, the instruction a person has to follow on this machine.
func (c EnvironmentCheck) Missing() string {
	said := fmt.Sprintf("%q, which %s blocks", c.Name, c.Serves())
	if c.Machine {
		if instruction := strings.TrimSpace(c.HowToInstall); instruction != "" {
			said += fmt.Sprintf("; it needs something installed for the whole machine, which a person has to install on this machine by following %q, and the harness never runs that", instruction)
		} else {
			said += "; it needs something installed for the whole machine, which a person has to install on this machine"
		}
	}
	return said
}

// EnvironmentsStillToCheck is every environment the run's latest checks found
// not usable, in the order the item names them. An environment whose check
// passes on a later round is no longer among them, because each round's
// results replace the last.
func (s State) EnvironmentsStillToCheck() []EnvironmentCheck {
	var pending []EnvironmentCheck
	for _, check := range s.Environments {
		if !check.Passed() {
			pending = append(pending, check)
		}
	}
	return pending
}

// EnvironmentsStillToCheckSays is a run's environments still to be checked in
// plain words, and empty for a run with none.
func (s State) EnvironmentsStillToCheckSays() string {
	pending := s.EnvironmentsStillToCheck()
	if len(pending) == 0 {
		return ""
	}
	missing := make([]string, 0, len(pending))
	for _, check := range pending {
		missing = append(missing, check.Missing())
	}
	return "environments still to be checked, because their checks did not pass: " + strings.Join(missing, "; ")
}

// LeavesEnvironmentsToCheck reports a run whose latest checks left any
// environment its item names still to be checked. Such a run does not
// discharge its item even where its change lands: the criteria those
// environments serve were never judged, so the item stays open for the
// development manager, who may decide a fresh run of it once she judges them
// available (docs/designs/checking-a-run-environment.md, section 7).
func (s State) LeavesEnvironmentsToCheck() bool {
	return len(s.EnvironmentsStillToCheck()) > 0
}

// EnvironmentsLeftAtLandingSays is what an item whose change landed with
// environments still to be checked is told: each such environment, the
// criteria it serves, and the commit the change landed at.
func (s State) EnvironmentsLeftAtLandingSays() string {
	pending := s.EnvironmentsStillToCheck()
	if len(pending) == 0 {
		return ""
	}
	at := "on the target branch"
	if landed := s.landedCommit(); landed != "" {
		at = "at commit " + landed
	}
	missing := make([]string, 0, len(pending))
	for _, check := range pending {
		missing = append(missing, check.Missing())
	}
	return fmt.Sprintf("Yoyodyne run %s landed its change %s while environments this item names were still to be checked, so the item stays open, parked for the development manager rather than closed: %s. "+
		"Once she judges them available she may decide a fresh run, which starts from the target branch, runs the checks and only these environments' tests at its head, and closes the item with no developer turn where they all pass.",
		s.RunID, at, strings.Join(missing, "; "))
}

// LandedEnvironmentsRecheck is what a re-run of this run is given to check, where
// its change landed with environments still to be checked, and nil otherwise.
func (s State) LandedEnvironmentsRecheck() *EnvironmentsRecheck {
	if s.Status != StatusSucceeded || s.Integration == nil || !s.LeavesEnvironmentsToCheck() {
		return nil
	}
	recheck := &EnvironmentsRecheck{RunID: s.RunID, Landed: s.landedCommit()}
	for _, check := range s.EnvironmentsStillToCheck() {
		recheck.Names = append(recheck.Names, check.Name)
	}
	return recheck
}

// landedCommit is the commit the run's recorded integration left on the target,
// and empty where it recorded none.
func (s State) landedCommit() string {
	if s.Integration == nil {
		return ""
	}
	landed := strings.TrimSpace(s.Integration.TargetCommit)
	if landed == "" {
		landed = strings.TrimSpace(s.Integration.SourceCommit)
	}
	if !commitPattern.MatchString(landed) {
		return ""
	}
	return landed
}

// EnvironmentsMissingHeld reports a run held because no environment its item
// names was usable before its developer's turn.
func (s State) EnvironmentsMissingHeld() bool {
	return s.Environmental != nil && s.Environmental.Cause == CauseEnvironmentsMissing && len(s.Environments) > 0
}

// EnvironmentsMissingSays is why the item of a run held for its missing
// environments is held, naming each environment and what it blocks, and empty
// for every other run.
func (s State) EnvironmentsMissingSays() string {
	if !s.EnvironmentsMissingHeld() {
		return ""
	}
	missing := make([]string, 0, len(s.Environments))
	for _, check := range s.Environments {
		missing = append(missing, check.Missing())
	}
	return fmt.Sprintf("run %s ended before any developer turn because none of the environments its item names was usable: %s", s.RunID, strings.Join(missing, "; "))
}

// EnvironmentsChecked replaces the run's record of its environments' checks
// with one round's results (EnvironmentCheck), so an environment that now
// passes stops being recorded as still to be checked.
type EnvironmentsChecked struct {
	Checks []EnvironmentCheck
	At     time.Time
}

func (u EnvironmentsChecked) apply(state *State) (bool, error) {
	state.Environments = append([]EnvironmentCheck(nil), u.Checks...)
	state.UpdatedAt = u.At
	return true, nil
}

// EnvironmentTestsAt is the results of the environments' tests recorded on
// commit, in the order the item names the environments. A result recorded on
// any other commit is left out, so it is never shown as evidence for a change
// it did not test (docs/designs/checking-a-run-environment.md, section 6). A
// change with no commit has no result.
func (s State) EnvironmentTestsAt(commit string) []EnvironmentCheck {
	if strings.TrimSpace(commit) == "" {
		return nil
	}
	var at []EnvironmentCheck
	for _, test := range s.EnvironmentTests {
		if test.Commit == commit {
			at = append(at, test)
		}
	}
	return at
}

// EnvironmentsTested replaces the run's record of its environments' tests with
// one round's results, each carrying the commit it ran on.
type EnvironmentsTested struct {
	Tests []EnvironmentCheck
	At    time.Time
}

func (u EnvironmentsTested) apply(state *State) (bool, error) {
	state.EnvironmentTests = append([]EnvironmentCheck(nil), u.Tests...)
	state.UpdatedAt = u.At
	return true, nil
}

// EnvironmentsConfirmedCompleted ends a re-run whose rechecked environments all
// passed their tests at the target's head, with no developer turn and nothing
// integrated: the run succeeds complete, and its item has been closed on those
// results. It changes nothing on a run that has already ended this way.
type EnvironmentsConfirmedCompleted struct {
	At time.Time
}

func (u EnvironmentsConfirmedCompleted) apply(state *State) (bool, error) {
	if state.Status == StatusSucceeded && state.Phase == PhaseComplete && state.EnvironmentsConfirmed {
		return false, nil
	}
	if state.Status.Terminal() {
		return false, fmt.Errorf("run %s has already ended %s, so it does not end again on its environments' tests", state.RunID, state.Status)
	}
	state.EnvironmentsConfirmed = true
	state.ProviderStop = ""
	state.RedeployStop = nil
	state.OperatorHeldSince = nil
	state.Status = StatusSucceeded
	state.Phase = PhaseComplete
	state.UpdatedAt = u.At
	completed := u.At
	state.CompletedAt = &completed
	return true, nil
}
