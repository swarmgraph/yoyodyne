package checkrun

// The environment checks the harness supplies, for the stop causes it
// recognises, and the holds they are attached to (docs/designs/follow-ups.md,
// "The checks").

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/followup"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

// DefaultEnvironmentTimeout bounds one environment check's command.
const DefaultEnvironmentTimeout = 30 * time.Second

// Login is a provider's own check of its login. It is satisfied by
// backend.Backend.
type Login interface {
	CheckAvailability(ctx context.Context) (backend.Availability, error)
}

// Environment runs the environment checks the harness supplies.
type Environment struct {
	// Command runs one of the harness's own commands and reports its combined
	// output; an error is the command not starting or exiting non-zero. It
	// defaults to running the command directly, with no shell.
	Command func(ctx context.Context, name string, args ...string) (string, error)
	// LookPath finds a tool on the search path. It defaults to exec.LookPath.
	LookPath func(file string) (string, error)
	// Logins is each provider's check of its login, by the provider's name.
	Logins map[string]Login
	// Timeout bounds each check; DefaultEnvironmentTimeout where not positive.
	Timeout time.Duration
}

// Evaluate runs one environment check.
func (e Environment) Evaluate(ctx context.Context, check followup.Check) Finding {
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = DefaultEnvironmentTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	target := check.Arguments[followup.ArgTarget]
	switch followup.EnvironmentCheck(check.Arguments[followup.ArgCheck]) {
	case followup.EnvironmentXcodeLicense:
		return e.xcodeLicense(ctx)
	case followup.EnvironmentToolOnPath:
		lookPath := e.LookPath
		if lookPath == nil {
			lookPath = exec.LookPath
		}
		found, err := lookPath(target)
		if err != nil {
			return Finding{Detail: target + " is not on the search path"}
		}
		return Finding{Passed: true, Detail: target + " is on the search path, at " + found}
	case followup.EnvironmentProviderLogin:
		login := e.Logins[target]
		if login == nil {
			return Finding{CouldNotRun: "the harness has no way to ask " + target + " whether it is logged in"}
		}
		availability, err := login.CheckAvailability(ctx)
		switch {
		case err != nil:
			return Finding{CouldNotRun: "asking " + target + " whether it is logged in failed: " + oneline.Fold(err.Error(), 300)}
		case !availability.Installed:
			return Finding{Detail: target + " is not installed: " + oneline.Fold(availability.Missing, 300)}
		case !availability.Authenticated:
			return Finding{Detail: target + " is not logged in"}
		}
		return Finding{Passed: true, Detail: target + " is logged in"}
	}
	return Finding{CouldNotRun: fmt.Sprintf("%q is not an environment check this build supplies", check.Arguments[followup.ArgCheck])}
}

// xcodeLicense is `xcodebuild -license check`, which exits zero once the
// license is accepted.
func (e Environment) xcodeLicense(ctx context.Context) Finding {
	command := e.Command
	if command == nil {
		command = runCommand
	}
	output, err := command(ctx, "xcodebuild", "-license", "check")
	said := oneline.Fold(strings.TrimSpace(output), 300)
	switch {
	case err == nil:
		return Finding{Passed: true, Detail: "the Xcode license is accepted (xcodebuild -license check passed)"}
	case errors.Is(err, exec.ErrNotFound):
		return Finding{CouldNotRun: "xcodebuild is not on the search path"}
	case ctx.Err() != nil:
		return Finding{CouldNotRun: "xcodebuild -license check did not finish in time"}
	case said == "":
		return Finding{Detail: "the Xcode license is not accepted (xcodebuild -license check: " + err.Error() + ")"}
	}
	return Finding{Detail: "the Xcode license is not accepted (xcodebuild -license check: " + said + ")"}
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(output), err
}

// Says is what an environment check confirms, in ordinary words.
func Says(check followup.Check) string {
	target := check.Arguments[followup.ArgTarget]
	switch followup.EnvironmentCheck(check.Arguments[followup.ArgCheck]) {
	case followup.EnvironmentXcodeLicense:
		return "the Xcode license has been accepted"
	case followup.EnvironmentToolOnPath:
		return target + " is on the search path"
	case followup.EnvironmentProviderLogin:
		return target + " is logged in again"
	}
	return "its check passes"
}

// The stop causes the harness recognises in what a check that could not run
// said. Each is matched only where it is certain; anything else gets no
// environment check, and its hold no follow-up from here.
var (
	xcodeLicenseRefusals = []string{
		"You have not agreed to the Xcode license agreements",
		"Agreeing to the Xcode/iOS license requires admin privileges",
	}
	toolNotFound = []*regexp.Regexp{
		regexp.MustCompile(`exec: "([A-Za-z0-9][A-Za-z0-9._+-]*)": executable file not found`),
		regexp.MustCompile(`command not found: ([A-Za-z0-9][A-Za-z0-9._+-]*)`),
		regexp.MustCompile(`(?:^|[\s:])([A-Za-z0-9][A-Za-z0-9._+-]*): command not found`),
	}
)

// Recognise is the environment check for a stop cause, from what the stop
// said, where the cause is one the harness recognises.
func Recognise(said string) (followup.Check, bool) {
	for _, refusal := range xcodeLicenseRefusals {
		if strings.Contains(said, refusal) {
			return EnvironmentCheck(followup.EnvironmentXcodeLicense, ""), true
		}
	}
	for _, pattern := range toolNotFound {
		if match := pattern.FindStringSubmatch(said); match != nil {
			return EnvironmentCheck(followup.EnvironmentToolOnPath, match[1]), true
		}
	}
	return followup.Check{}, false
}

// EnvironmentCheck is the check naming one environment check, with its target
// where it takes one.
func EnvironmentCheck(which followup.EnvironmentCheck, target string) followup.Check {
	arguments := map[string]string{followup.ArgCheck: string(which)}
	if target != "" {
		arguments[followup.ArgTarget] = target
	}
	return followup.Check{Name: followup.CheckEnvironment, Arguments: arguments}
}

// The holds the harness places itself for a cause an environment check can
// confirm, named as a follow-up names a hold. Each identifier is the kind and
// what it is about, so two holds of one kind are told apart.
const (
	// HoldChecksCouldNotRun is a watch session holding new work because no
	// configured check could run on a run's change. It is followed by the run.
	HoldChecksCouldNotRun = "checks-could-not-run"
	// HoldProviderLogin is the provider's login having lapsed, which holds every
	// dispatch to it. It is followed by the provider.
	HoldProviderLogin = "provider-login"
	// HoldOperator is the operator's own pause, which only he lifts.
	HoldOperator = "operator-hold"
)

// HoldID names one hold.
func HoldID(kind, about string) string { return kind + ":" + about }

// HarnessMayLift reports a hold the harness may release when a follow-up about
// it passes: one it placed itself for a cause a check confirms. A hold the
// operator placed is never one, nor is a hold this build does not name.
func HarnessMayLift(hold string) bool {
	kind, _, _ := strings.Cut(hold, ":")
	return kind == HoldChecksCouldNotRun || kind == HoldProviderLogin
}

// Deadlines for a follow-up the harness writes for its own hold. Such a hold
// stops new work, so it counts as priority 0 (docs/designs/follow-ups.md,
// "Deadlines"); the configuration keys for them are not read yet.
const (
	holdBackToOwnerAfter = time.Hour
	holdToManagerAfter   = 4 * time.Hour
)

// HoldFollowUp is the follow-up a hold the harness placed writes for a cause
// an environment check confirms: the development manager waits, every
// developer slot it holds is empty, and its owner is whoever can clear the
// cause. Accepting a license or logging a provider in needs the machine
// owner's password or account, which is the operator's credential; a tool
// missing from the search path is the development manager's to see to.
func HoldFollowUp(hold string, check followup.Check, about []followup.Subject, now time.Time) followup.FollowUp {
	owner, reason := ownership.MoverDevelopmentManager, ownership.OperatorReason("")
	switch followup.EnvironmentCheck(check.Arguments[followup.ArgCheck]) {
	case followup.EnvironmentXcodeLicense, followup.EnvironmentProviderLogin:
		owner, reason = ownership.MoverOperator, ownership.OperatorCredential
	}
	return followup.FollowUp{
		SchemaVersion: followup.SchemaVersion,
		Waits:         ownership.MoverDevelopmentManager,
		Owner:         owner,
		OwnerReason:   reason,
		About:         append([]followup.Subject{{Kind: followup.SubjectHold, ID: hold}}, about...),
		Blocks:        followup.Blocks{EmptiesEveryDeveloperSlot: true},
		Check:         check,
		BackToOwnerAt: now.Add(holdBackToOwnerAfter),
		ToManagerAt:   now.Add(holdToManagerAfter),
		Origin:        followup.Origin{Kind: followup.OriginHarness},
		CreatedAt:     now,
		State:         followup.StateOpen,
	}
}
