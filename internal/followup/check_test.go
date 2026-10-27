package followup

import (
	"strings"
	"testing"
)

// The closed list is the design's sixteen checks, each with what it takes, and
// the table the validator reads holds exactly those.
func TestTheClosedListIsTheDesignsChecks(t *testing.T) {
	t.Parallel()
	want := []CheckName{"item-closed", "dependencies-closed", "item-unparked", "item-landed", "pull-request-merged",
		"stop-left-list", "run-reloaded", "report-handled", "exchange-closed", "proposal-decided", "document-landed",
		"file-exists", "hold-released", "provider-usable", "environment", "command"}
	got := Checks()
	if len(got) != len(want) || len(checks) != len(want) {
		t.Fatalf("Checks() = %v and the table holds %d, want %v", got, len(checks), want)
	}
	for i := range want {
		if got[i] != want[i] || !got[i].Valid() {
			t.Errorf("check %d = %q, want %q on the list", i, got[i], want[i])
		}
	}
}

// One well-formed check of each name is accepted.
func TestEveryCheckOnTheListIsAccepted(t *testing.T) {
	t.Parallel()
	for _, c := range []Check{
		{CheckItemClosed, map[string]string{ArgItem: "yoyodyne-ifd.432.25.21"}},
		{CheckDependenciesClosed, map[string]string{ArgItem: "yoyodyne-ifd.432.25.19"}},
		{CheckItemUnparked, map[string]string{ArgItem: "yoyodyne-0bj"}},
		{CheckItemLanded, map[string]string{ArgItem: "yoyodyne-ifd.432.25.21"}},
		{CheckPullRequestMerged, map[string]string{ArgPullRequest: "1508"}},
		{CheckStopLeftList, map[string]string{ArgRun: "run-1fca3a7c"}},
		{CheckRunReloaded, map[string]string{ArgRun: "run-1fca3a7c"}},
		{CheckReportHandled, map[string]string{ArgReport: "report-66b8734b397ad14b8f1ca4f5af779cdf"}},
		{CheckExchangeClosed, map[string]string{ArgExchange: "exchange-12"}},
		{CheckProposalDecided, map[string]string{ArgProposal: "amendment-3"}},
		{CheckDocumentLanded, map[string]string{ArgDocument: "follow-ups"}},
		{CheckFileExists, map[string]string{ArgPath: "docs/designs/follow-ups.md"}},
		{CheckHoldReleased, map[string]string{ArgHold: "intake"}},
		{CheckProviderUsable, map[string]string{ArgProvider: "claude"}},
		{CheckEnvironment, map[string]string{ArgCheck: string(EnvironmentXcodeLicense)}},
		{CheckEnvironment, map[string]string{ArgCheck: string(EnvironmentToolOnPath), ArgTarget: "go"}},
		{CheckEnvironment, map[string]string{ArgCheck: string(EnvironmentProviderLogin), ArgTarget: "codex"}},
		{CheckCommand, map[string]string{ArgName: "license_check"}},
	} {
		if err := c.Validate(); err != nil {
			t.Errorf("%s %v: Validate() = %v", c.Name, c.Arguments, err)
		}
	}
}

// A follow-up names a check, never a command: not as the check's name, not as
// an argument, and not as a command check's declared name.
func TestACheckNamingACommandIsRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []Check{
		{Name: "xcodebuild -license check"},
		{Name: "/usr/bin/true"},
		{Name: "make test && echo ok"},
		{Name: "$(curl example.com)"},
		{Name: CheckCommand, Arguments: map[string]string{ArgName: "xcodebuild -license check"}},
		{Name: CheckCommand, Arguments: map[string]string{ArgName: "./scripts/check.sh"}},
	} {
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "command") {
			t.Errorf("%q %v: Validate() = %v, want a refusal saying it is a command", c.Name, c.Arguments, err)
		}
	}
	for _, c := range []Check{
		{Name: CheckItemClosed, Arguments: map[string]string{ArgItem: "x", "command": "make test"}},
		{Name: CheckEnvironment, Arguments: map[string]string{ArgCheck: "xcodebuild -license check"}},
		{Name: CheckEnvironment, Arguments: map[string]string{ArgCheck: string(EnvironmentToolOnPath), ArgTarget: "go; rm -rf ~"}},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%q %v: Validate() = nil, want a refusal", c.Name, c.Arguments)
		}
	}
}

func TestACheckWithArgumentsItCannotReadIsRefused(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]Check{
		"a name off the list":            {Name: "time-passed", Arguments: map[string]string{"after": "1h"}},
		"no name":                        {},
		"a missing argument":             {Name: CheckItemClosed},
		"an empty argument":              {Name: CheckItemClosed, Arguments: map[string]string{ArgItem: ""}},
		"a sentence for an item":         {Name: CheckItemClosed, Arguments: map[string]string{ArgItem: "the one about holds"}},
		"a pull request that is no num":  {Name: CheckPullRequestMerged, Arguments: map[string]string{ArgPullRequest: "#12"}},
		"an absolute path":               {Name: CheckFileExists, Arguments: map[string]string{ArgPath: "/etc/passwd"}},
		"a path that climbs out":         {Name: CheckFileExists, Arguments: map[string]string{ArgPath: "../outside"}},
		"a path not written cleanly":     {Name: CheckFileExists, Arguments: map[string]string{ArgPath: "docs//designs/./x.md"}},
		"an environment check unknown":   {Name: CheckEnvironment, Arguments: map[string]string{ArgCheck: "disk-free"}},
		"an environment target missing":  {Name: CheckEnvironment, Arguments: map[string]string{ArgCheck: string(EnvironmentToolOnPath)}},
		"an environment target unwanted": {Name: CheckEnvironment, Arguments: map[string]string{ArgCheck: string(EnvironmentXcodeLicense), ArgTarget: "x"}},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want a refusal", name)
		}
	}
}
