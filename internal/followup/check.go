package followup

// The closed list of checks a follow-up can wait on.
//
// A check is chosen from this list, which is kept in the harness's own code as
// every capability is: a follow-up names a check and its arguments, and never a
// command for the harness to run. Even the two checks that end in running
// something name it rather than say it: an environment check names one the
// harness supplies (EnvironmentChecks), and a command check names one the
// project declares by name in its configuration, so a role writing a follow-up
// has no way to put a command in front of the harness
// (docs/designs/follow-ups.md, "The checks").
//
// Running a check is not this package's: what each one asks of the tracker, the
// forge, the run records, or the machine belongs to the check runner, which is
// built on this list.

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// CheckName is one check on the closed list.
type CheckName string

const (
	// CheckItemClosed is an item closed. Argument: item.
	CheckItemClosed CheckName = "item-closed"
	// CheckDependenciesClosed is every item an item depends on closed.
	// Argument: item.
	CheckDependenciesClosed CheckName = "dependencies-closed"
	// CheckItemUnparked is an item no longer parked. Argument: item.
	CheckItemUnparked CheckName = "item-unparked"
	// CheckItemLanded is an item having a run that landed on the target branch.
	// Argument: item.
	CheckItemLanded CheckName = "item-landed"
	// CheckPullRequestMerged is a pull request merged. Argument: pull-request,
	// its number.
	CheckPullRequestMerged CheckName = "pull-request-merged"
	// CheckStopLeftList is a stopped run's stop having left the development
	// manager's list. Argument: run.
	CheckStopLeftList CheckName = "stop-left-list"
	// CheckRunReloaded is a run whose recovery act had an unknown result
	// reloaded, and what it holds established. Argument: run.
	CheckRunReloaded CheckName = "run-reloaded"
	// CheckReportHandled is a report's handling recorded. Argument: report.
	CheckReportHandled CheckName = "report-handled"
	// CheckExchangeClosed is an exchange between roles closed. Argument:
	// exchange.
	CheckExchangeClosed CheckName = "exchange-closed"
	// CheckProposalDecided is a proposed change decided. Argument: proposal.
	CheckProposalDecided CheckName = "proposal-decided"
	// CheckDocumentLanded is a document landed on the target branch. Argument:
	// document.
	CheckDocumentLanded CheckName = "document-landed"
	// CheckFileExists is a file existing at a path in the repository, at the
	// target branch's current commit. Argument: path, relative to the
	// repository.
	CheckFileExists CheckName = "file-exists"
	// CheckHoldReleased is a hold released. Argument: hold.
	CheckHoldReleased CheckName = "hold-released"
	// CheckProviderUsable is a provider that can be used again. Argument:
	// provider.
	CheckProviderUsable CheckName = "provider-usable"
	// CheckEnvironment is an environment check the harness supplies, named from
	// EnvironmentChecks. Arguments: check, and target where that check asks for
	// one.
	CheckEnvironment CheckName = "environment"
	// CheckCommand is a command check the project declares by name in its
	// configuration. Argument: name, the declared name and never the command.
	CheckCommand CheckName = "command"
)

// Argument names a check takes.
const (
	ArgItem        = "item"
	ArgPullRequest = "pull-request"
	ArgRun         = "run"
	ArgReport      = "report"
	ArgExchange    = "exchange"
	ArgProposal    = "proposal"
	ArgDocument    = "document"
	ArgPath        = "path"
	ArgHold        = "hold"
	ArgProvider    = "provider"
	ArgCheck       = "check"
	ArgTarget      = "target"
	ArgName        = "name"
)

// EnvironmentCheck is one environment check the harness supplies, for a stop
// cause it recognises.
type EnvironmentCheck string

const (
	// EnvironmentXcodeLicense is the Xcode license accepted. It takes no target.
	EnvironmentXcodeLicense EnvironmentCheck = "xcode-license-accepted"
	// EnvironmentToolOnPath is a tool found on the search path. Its target is
	// the tool's name.
	EnvironmentToolOnPath EnvironmentCheck = "tool-on-search-path"
	// EnvironmentProviderLogin is a provider's login still valid. Its target is
	// the provider.
	EnvironmentProviderLogin EnvironmentCheck = "provider-login-valid"
)

// EnvironmentChecks is every environment check the harness supplies.
func EnvironmentChecks() []EnvironmentCheck {
	return []EnvironmentCheck{EnvironmentXcodeLicense, EnvironmentToolOnPath, EnvironmentProviderLogin}
}

// environmentTargets says whether each environment check takes a target.
var environmentTargets = map[EnvironmentCheck]bool{
	EnvironmentXcodeLicense:  false,
	EnvironmentToolOnPath:    true,
	EnvironmentProviderLogin: true,
}

// argument is how one argument's value is read.
type argument struct {
	name     string
	required bool
	valid    func(string) error
}

// checks is the closed list: each check and the arguments it takes. A test
// holds it to Checks, so a check added to one and not the other fails.
var checks = map[CheckName][]argument{
	CheckItemClosed:         {{ArgItem, true, validReference}},
	CheckDependenciesClosed: {{ArgItem, true, validReference}},
	CheckItemUnparked:       {{ArgItem, true, validReference}},
	CheckItemLanded:         {{ArgItem, true, validReference}},
	CheckPullRequestMerged:  {{ArgPullRequest, true, validPullRequest}},
	CheckStopLeftList:       {{ArgRun, true, validReference}},
	CheckRunReloaded:        {{ArgRun, true, validReference}},
	CheckReportHandled:      {{ArgReport, true, validReference}},
	CheckExchangeClosed:     {{ArgExchange, true, validReference}},
	CheckProposalDecided:    {{ArgProposal, true, validReference}},
	CheckDocumentLanded:     {{ArgDocument, true, validReference}},
	CheckFileExists:         {{ArgPath, true, validPath}},
	CheckHoldReleased:       {{ArgHold, true, validReference}},
	CheckProviderUsable:     {{ArgProvider, true, validReference}},
	CheckEnvironment:        {{ArgCheck, true, validEnvironmentCheck}, {ArgTarget, false, validReference}},
	CheckCommand:            {{ArgName, true, validDeclaredName}},
}

// Checks is the whole closed list, in the order the design lists it.
func Checks() []CheckName {
	return []CheckName{
		CheckItemClosed,
		CheckDependenciesClosed,
		CheckItemUnparked,
		CheckItemLanded,
		CheckPullRequestMerged,
		CheckStopLeftList,
		CheckRunReloaded,
		CheckReportHandled,
		CheckExchangeClosed,
		CheckProposalDecided,
		CheckDocumentLanded,
		CheckFileExists,
		CheckHoldReleased,
		CheckProviderUsable,
		CheckEnvironment,
		CheckCommand,
	}
}

// Valid reports whether a name is on the closed list.
func (n CheckName) Valid() bool {
	_, ok := checks[n]
	return ok
}

// Check is the one check a follow-up waits on: a name from the closed list and
// the arguments that name takes.
type Check struct {
	Name      CheckName         `json:"name"`
	Arguments map[string]string `json:"arguments"`
}

// Validate refuses a check off the closed list, an argument the check does not
// take, a missing one, and a value its argument cannot read. A name that is not
// on the list is refused whatever it is; one that reads as a command is told so,
// because that is the refusal most worth saying plainly.
func (c Check) Validate() error {
	spec, ok := checks[c.Name]
	if !ok {
		if looksLikeCommand(string(c.Name)) {
			return fmt.Errorf("check %q is a command, and a follow-up names a check from the closed list, never a command; a command the project wants run is declared by name in follow_ups.command_checks and named with the %q check", c.Name, CheckCommand)
		}
		return fmt.Errorf("check %q is not on the closed list of checks (%s)", c.Name, checkList())
	}
	var problems []error
	taken := map[string]bool{}
	for _, arg := range spec {
		taken[arg.name] = true
		value, present := c.Arguments[arg.name]
		if !present {
			if arg.required {
				problems = append(problems, fmt.Errorf("check %q needs its %q argument", c.Name, arg.name))
			}
			continue
		}
		if err := arg.valid(value); err != nil {
			problems = append(problems, fmt.Errorf("check %q argument %q: %w", c.Name, arg.name, err))
		}
	}
	var extra []string
	for name := range c.Arguments {
		if !taken[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		problems = append(problems, fmt.Errorf("check %q takes no %q argument", c.Name, name))
	}
	if c.Name == CheckEnvironment {
		environment := EnvironmentCheck(c.Arguments[ArgCheck])
		if wants, known := environmentTargets[environment]; known {
			_, has := c.Arguments[ArgTarget]
			switch {
			case wants && !has:
				problems = append(problems, fmt.Errorf("environment check %q needs a target", environment))
			case !wants && has:
				problems = append(problems, fmt.Errorf("environment check %q takes no target", environment))
			}
		}
	}
	return errors.Join(problems...)
}

func checkList() string {
	names := make([]string, 0, len(checks))
	for _, name := range Checks() {
		names = append(names, string(name))
	}
	return strings.Join(names, ", ")
}

// looksLikeCommand is a name with what a command line has and a check's name
// never does: a space, a path, or a shell's punctuation.
func looksLikeCommand(name string) bool {
	return strings.ContainsAny(name, " \t/\\;|&$`<>()") || strings.HasPrefix(name, "-")
}

// MaxReferenceBytes bounds an identifier a follow-up carries.
const MaxReferenceBytes = 256

// referencePattern is an identifier a follow-up can name: a work item
// ("yoyodyne-ifd.432.25.21"), a run, a report, an exchange, a document, a
// provider. It is one token, so it can never be a command line.
var referencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@+-]*$`)

func validReference(value string) error {
	if len(value) > MaxReferenceBytes || !referencePattern.MatchString(value) {
		return fmt.Errorf("%q is not an identifier: one token of letters, digits, and . _ : @ + - of at most %d bytes", value, MaxReferenceBytes)
	}
	return nil
}

var pullRequestPattern = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

func validPullRequest(value string) error {
	if !pullRequestPattern.MatchString(value) {
		return fmt.Errorf("%q is not a pull request's number", value)
	}
	return nil
}

func validPath(value string) error {
	if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%q is not a path in the repository", value)
	}
	clean, err := repowrite.Relative(value)
	if err != nil {
		return err
	}
	if clean != value {
		return fmt.Errorf("%q is not written as the path it names, %q", value, clean)
	}
	return nil
}

func validEnvironmentCheck(value string) error {
	if _, ok := environmentTargets[EnvironmentCheck(value)]; !ok {
		names := make([]string, 0, len(environmentTargets))
		for _, known := range EnvironmentChecks() {
			names = append(names, string(known))
		}
		return fmt.Errorf("%q is not an environment check the harness supplies (%s)", value, strings.Join(names, ", "))
	}
	return nil
}

// declaredNamePattern is a command check's declared name: a configuration key,
// never a command line.
var declaredNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$`)

func validDeclaredName(value string) error {
	if len(value) > 64 || !declaredNamePattern.MatchString(value) {
		if looksLikeCommand(value) {
			return fmt.Errorf("%q is a command; a command check names the check the project declares in follow_ups.command_checks, never the command", value)
		}
		return fmt.Errorf("%q is not a declared command check's name", value)
	}
	return nil
}
