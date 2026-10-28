package checkrun

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/followup"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var start = time.Date(2026, 10, 24, 11, 30, 0, 0, time.UTC)

func newStore(t *testing.T) *runstate.FollowUpStore {
	t.Helper()
	store, err := runstate.NewFollowUpStore(t.TempDir(), domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// license is `xcodebuild -license check` on a machine whose license is
// accepted once accepted is set, counting how often it was asked.
type license struct {
	mu       sync.Mutex
	accepted bool
	asked    int
}

func (l *license) accept() { l.mu.Lock(); l.accepted = true; l.mu.Unlock() }

func (l *license) command(_ context.Context, name string, args ...string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if name != "xcodebuild" || strings.Join(args, " ") != "-license check" {
		return "", errors.New("unexpected command " + name)
	}
	l.asked++
	if l.accepted {
		return "", nil
	}
	return "You have not agreed to the Xcode license agreements.", errors.New("exit status 69")
}

func licenseHold(t *testing.T, runner *Runner) followup.FollowUp {
	t.Helper()
	check, ok := Recognise("make test could not run: the machine refused to start it: You have not agreed to the Xcode license agreements. Please run 'sudo xcodebuild -license'")
	if !ok {
		t.Fatal("the Xcode license refusal is not recognised")
	}
	hold := HoldID(HoldChecksCouldNotRun, "run-one")
	written, err := runner.Attach(HoldFollowUp(hold, check, []followup.Subject{{Kind: followup.SubjectRun, ID: "run-one"}}, start))
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if written.Owner != ownership.MoverOperator || written.OwnerReason != ownership.OperatorCredential {
		t.Fatalf("owner %s for %q, want the operator for a credential", written.Owner, written.OwnerReason)
	}
	return written
}

// A hold for a license not accepted: its check fails and is recorded once
// however often it runs, runs only at the longer interval, and once the license
// is accepted the follow-up is closed with the time within one interval.
func TestALicenseHoldsFollowUpFailsThenPassesWithinOneInterval(t *testing.T) {
	t.Parallel()
	machine := &license{}
	store := newStore(t)
	runner := &Runner{Store: store, Environment: Environment{Command: machine.command}}
	written := licenseHold(t, runner)

	first := runner.Round(context.Background(), start)
	if first.Ran != 1 || len(first.Changed) != 1 || len(first.Passed) != 0 {
		t.Fatalf("first round = %+v, want the check run once, its failure recorded, nothing passed", first)
	}
	recorded, err := store.Load(written.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.LastResult == nil || recorded.LastResult.Passed || !strings.Contains(recorded.LastResult.Detail, "not accepted") {
		t.Fatalf("last result = %+v, want the license not accepted", recorded.LastResult)
	}
	// Polled every minute: the check is run again only at its interval, and the
	// same failure writes nothing.
	for minute := 1; minute < 10; minute++ {
		if round := runner.Round(context.Background(), start.Add(time.Duration(minute)*time.Minute)); round.Ran != 0 {
			t.Fatalf("minute %d ran %d checks, want none before the ten-minute interval", minute, round.Ran)
		}
	}
	again := runner.Round(context.Background(), start.Add(10*time.Minute))
	if again.Ran != 1 || len(again.Changed) != 0 {
		t.Fatalf("round at ten minutes = %+v, want the check run and nothing written for the same failure", again)
	}
	unchanged, _ := store.Load(written.ID)
	if !unchanged.LastResult.At.Equal(start) {
		t.Fatalf("the result's time moved to %s on an unchanged finding", unchanged.LastResult.At)
	}

	accepted := start.Add(13 * time.Minute)
	machine.accept()
	var passedAt time.Time
	for minute := 13; minute <= 30 && passedAt.IsZero(); minute++ {
		now := start.Add(time.Duration(minute) * time.Minute)
		if round := runner.Round(context.Background(), now); len(round.Passed) == 1 {
			passedAt = now
			passed := round.Passed[0]
			if passed.State != followup.StatePassed || !passed.Passed.At.Equal(now) || !strings.Contains(passed.Passed.Result, "license is accepted") {
				t.Fatalf("passed = %+v, want it closed with the time and what the check found", passed.Passed)
			}
		}
	}
	if passedAt.IsZero() || passedAt.Sub(accepted) > DefaultCommandInterval {
		t.Fatalf("passed at %s, license accepted at %s; want it within one interval", passedAt, accepted)
	}
	if after := runner.Round(context.Background(), passedAt.Add(time.Hour)); after.Ran != 0 {
		t.Fatalf("a passed follow-up's check ran again: %+v", after)
	}
}

// A check this runner cannot run counts as not passed and says why, once.
func TestACheckThatCannotRunIsNotPassedAndSaysWhy(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	runner := &Runner{Store: store}
	written := licenseHold(t, runner)
	for minute := 0; minute <= 60; minute += 5 {
		if round := runner.Round(context.Background(), start.Add(time.Duration(minute)*time.Minute)); len(round.Passed) != 0 {
			t.Fatalf("a check nothing can run passed: %+v", round)
		}
	}
	recorded, _ := store.Load(written.ID)
	if recorded.State != followup.StateOpen || recorded.LastResult == nil || recorded.LastResult.Passed ||
		!strings.Contains(recorded.LastResult.CouldNotRun, "no way to run") || !recorded.LastResult.At.Equal(start) {
		t.Fatalf("follow-up = %+v, last result %+v; want it open, not passed, saying why, recorded once", recorded, recorded.LastResult)
	}
	if said := recorded.LastResult.Says(); !strings.Contains(said, "could not be run, so it has not passed") {
		t.Fatalf("Says() = %q", said)
	}
}

// Other checks run at the shorter interval, one at a time.
type counting struct {
	mu      sync.Mutex
	running int
	most    int
	calls   int
}

func (c *counting) Evaluate(context.Context, followup.Check) Finding {
	c.mu.Lock()
	c.running++
	c.calls++
	c.most = max(c.most, c.running)
	c.mu.Unlock()
	time.Sleep(time.Millisecond)
	c.mu.Lock()
	c.running--
	c.mu.Unlock()
	return Finding{Detail: "the item is still open"}
}

func TestOtherChecksRunAtTheShorterIntervalOneAtATime(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	closed := &counting{}
	runner := &Runner{Store: store, Checks: map[followup.CheckName]Evaluator{followup.CheckItemClosed: closed}}
	for _, item := range []string{"yoyodyne-a", "yoyodyne-b", "yoyodyne-c"} {
		written := HoldFollowUp(HoldID(HoldChecksCouldNotRun, "run-"+item), followup.Check{Name: followup.CheckItemClosed, Arguments: map[string]string{followup.ArgItem: item}}, nil, start)
		if _, err := runner.Attach(written); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); runner.Round(context.Background(), start) }()
	}
	wg.Wait()
	if closed.most != 1 || closed.calls != 3 {
		t.Fatalf("most at once %d, calls %d; want one at a time and each check once per interval", closed.most, closed.calls)
	}
	if round := runner.Round(context.Background(), start.Add(4*time.Minute)); round.Ran != 0 {
		t.Fatalf("ran %d before the five-minute interval", round.Ran)
	}
	if round := runner.Round(context.Background(), start.Add(5*time.Minute)); round.Ran != 3 {
		t.Fatalf("ran %d at the five-minute interval, want 3", round.Ran)
	}
}

type login struct {
	availability backend.Availability
	err          error
}

func (l login) CheckAvailability(context.Context) (backend.Availability, error) {
	return l.availability, l.err
}

func TestTheEnvironmentChecks(t *testing.T) {
	t.Parallel()
	environment := Environment{
		LookPath: func(file string) (string, error) {
			if file == "codex" {
				return "/opt/homebrew/bin/codex", nil
			}
			return "", exec.ErrNotFound
		},
		Logins: map[string]Login{
			"claude-code": login{availability: backend.Availability{Installed: true, Authenticated: true}},
			"codex":       login{availability: backend.Availability{Installed: true}},
			"broken":      login{err: errors.New("the status command crashed")},
		},
		Command: func(context.Context, string, ...string) (string, error) { return "", exec.ErrNotFound },
	}
	cases := []struct {
		check       followup.Check
		passed      bool
		couldNotRun string
	}{
		{EnvironmentCheck(followup.EnvironmentToolOnPath, "codex"), true, ""},
		{EnvironmentCheck(followup.EnvironmentToolOnPath, "gofmt-nowhere"), false, ""},
		{EnvironmentCheck(followup.EnvironmentProviderLogin, "claude-code"), true, ""},
		{EnvironmentCheck(followup.EnvironmentProviderLogin, "codex"), false, ""},
		{EnvironmentCheck(followup.EnvironmentProviderLogin, "broken"), false, "failed"},
		{EnvironmentCheck(followup.EnvironmentProviderLogin, "nobody"), false, "no way to ask"},
		{EnvironmentCheck(followup.EnvironmentXcodeLicense, ""), false, "not on the search path"},
	}
	for _, c := range cases {
		if err := c.check.Validate(); err != nil {
			t.Fatalf("%v is not a valid check: %v", c.check, err)
		}
		found := environment.Evaluate(context.Background(), c.check)
		if found.Passed != c.passed || (c.couldNotRun == "") != (found.CouldNotRun == "") || !strings.Contains(found.CouldNotRun, c.couldNotRun) {
			t.Errorf("%v found %+v, want passed %t and could-not-run %q", c.check.Arguments, found, c.passed, c.couldNotRun)
		}
	}
}

func TestTheStopCausesTheHarnessRecognises(t *testing.T) {
	t.Parallel()
	cases := map[string]followup.Check{
		"make: You have not agreed to the Xcode license agreements.":                                                                      EnvironmentCheck(followup.EnvironmentXcodeLicense, ""),
		"Agreeing to the Xcode/iOS license requires admin privileges, please run “sudo xcodebuild -license” and then retry this command.": EnvironmentCheck(followup.EnvironmentXcodeLicense, ""),
		`exec: "golangci-lint": executable file not found in $PATH`:                                                                       EnvironmentCheck(followup.EnvironmentToolOnPath, "golangci-lint"),
		"sh: 1: shellcheck: command not found":                                                                                            EnvironmentCheck(followup.EnvironmentToolOnPath, "shellcheck"),
		"zsh: command not found: swiftlint":                                                                                               EnvironmentCheck(followup.EnvironmentToolOnPath, "swiftlint"),
	}
	for said, want := range cases {
		got, ok := Recognise(said)
		if !ok || got.Name != want.Name || got.Arguments[followup.ArgCheck] != want.Arguments[followup.ArgCheck] || got.Arguments[followup.ArgTarget] != want.Arguments[followup.ArgTarget] {
			t.Errorf("Recognise(%q) = %+v, %t; want %+v", said, got, ok, want)
		}
	}
	if _, ok := Recognise("--- FAIL: TestSomething (0.01s)"); ok {
		t.Error("a failing test was recognised as an environment cause")
	}
}

// The operator's own hold is never one the harness lifts, whatever passes.
func TestOnlyTheHarnesssOwnHoldsAreLiftedByIt(t *testing.T) {
	t.Parallel()
	for hold, may := range map[string]bool{
		HoldID(HoldChecksCouldNotRun, "run-one"): true,
		HoldID(HoldProviderLogin, "claude-code"): true,
		HoldOperator:                             false,
		"intake":                                 false,
		"operator-hold:anything":                 false,
		"something-else:run-one":                 false,
	} {
		if HarnessMayLift(hold) != may {
			t.Errorf("HarnessMayLift(%q) = %t, want %t", hold, !may, may)
		}
	}
}

func TestADeclaredCommandCheck(t *testing.T) {
	t.Parallel()
	check := followup.Check{Name: followup.CheckCommand, Arguments: map[string]string{followup.ArgName: "simulator-booted"}}
	if found := (Commands{}).Evaluate(context.Background(), check); found.Passed || !strings.Contains(found.CouldNotRun, "declares no command check") {
		t.Fatalf("an undeclared check found %+v", found)
	}
	commands := Commands{Declared: map[string]DeclaredCommand{"simulator-booted": {Command: "xcrun simctl list | grep Booted"}},
		Run: func(_ context.Context, _ string, command string) (string, error) {
			if command != "xcrun simctl list | grep Booted" {
				return "", errors.New("ran " + command)
			}
			return "", nil
		}}
	if found := commands.Evaluate(context.Background(), check); !found.Passed {
		t.Fatalf("the declared check found %+v, want it passed", found)
	}
}

type filedReports struct{ filed []report.Report }

func (r *filedReports) Append(reported report.Report) error {
	r.filed = append(r.filed, reported)
	return nil
}

// Whoever waits on a follow-up is told its check passed, by a report addressed
// to that role; nobody is told twice, and the operator is never sent a report.
func TestWhoeverWaitsIsToldTheCheckPassed(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	told := &filedReports{}
	runner := &Runner{Store: store, Tell: &Teller{Reports: told, ProductID: "yoyodyne", RepositoryID: "yoyodyne"},
		Environment: Environment{LookPath: func(string) (string, error) { return "/usr/bin/shellcheck", nil }}}
	written, err := runner.Attach(HoldFollowUp(HoldID(HoldChecksCouldNotRun, "run-one"), EnvironmentCheck(followup.EnvironmentToolOnPath, "shellcheck"), nil, start))
	if err != nil {
		t.Fatal(err)
	}
	operators := HoldFollowUp(HoldID(HoldChecksCouldNotRun, "run-two"), EnvironmentCheck(followup.EnvironmentToolOnPath, "shellcheck"), nil, start)
	operators.Waits = ownership.MoverOperator
	if _, err := runner.Attach(operators); err != nil {
		t.Fatal(err)
	}
	round := runner.Round(context.Background(), start)
	if len(round.Passed) != 2 || round.Told != 1 || len(told.filed) != 1 {
		t.Fatalf("round = %+v, filed %d; want both passed and only the development manager told", round, len(told.filed))
	}
	filed := told.filed[0]
	if filed.Addressee() != domain.RoleDevelopmentManager || filed.Role != report.HarnessReporter || !strings.Contains(filed.Message, written.ID) ||
		!strings.Contains(filed.Message, "shellcheck is on the search path") {
		t.Fatalf("report = %+v, want it addressed to the development manager naming the follow-up and what was found", filed)
	}
	runner.Round(context.Background(), start.Add(time.Hour))
	if len(told.filed) != 1 {
		t.Fatalf("filed %d reports, want the waiting role told once", len(told.filed))
	}
}
