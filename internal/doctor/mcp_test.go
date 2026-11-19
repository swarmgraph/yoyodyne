package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
)

const mcpConfig = healthyConfig + `mcp:
  servers:
    mail:
      command: [mail-mcp, --stdio]
      login: [mail-mcp, login]
      tools:
        search: read
    tickets:
      url: https://mcp.example.com/
      credential: file
      tools:
        list_open: read
    calendar:
      url: https://calendar.example.com/
      credential: keychain
      tools:
        list: read
`

// A project that declares no MCP server is told nothing about MCP.
func TestAProjectWithNoMCPServerIsToldNothingAboutThem(t *testing.T) {
	t.Parallel()
	report := newWorld(t).diagnose()
	for _, finding := range report.Findings {
		if strings.HasPrefix(finding.Check, "mcp:") {
			t.Fatalf("a project with no mcp section was reported %+v", finding)
		}
	}
}

// Each server whose credential or login is missing is a warning naming the
// server and the one command that fixes it.
func TestAMissingMCPCredentialNamesTheServerAndTheCommandToRun(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	world.configuration = mcpConfig
	world.runner.reply("find-generic-password -s yoyo-mcp.yoyodyne.calendar", failed("The specified item could not be found in the keychain."))
	report := world.diagnose()

	for check, want := range map[string]struct{ summary, remedy string }{
		"mcp:mail":     {"mail", "yoyo mcp login mail"},
		"mcp:tickets":  {"tickets", "tickets.token"},
		"mcp:calendar": {"yoyo-mcp.yoyodyne.calendar", "security add-generic-password -s yoyo-mcp.yoyodyne.calendar -a yoyo -w"},
	} {
		finding, ok := findingFor(report, check)
		if !ok {
			t.Fatalf("no %s finding in %+v", check, report.Findings)
		}
		if finding.Status != StatusWarning || !strings.Contains(finding.Summary, want.summary) || !strings.Contains(finding.Remedy, want.remedy) {
			t.Errorf("%s = %s %q remedy %q; want a warning naming %q with a remedy carrying %q", check, finding.Status, finding.Summary, finding.Remedy, want.summary, want.remedy)
		}
	}
	if !report.Healthy() {
		t.Fatalf("a missing MCP credential stopped the installation being healthy: %+v", report)
	}
	for _, command := range world.runner.commands {
		if strings.Contains(strings.Join(command, " "), "yoyo-mcp") && contains(command, "-w") {
			t.Fatalf("the diagnosis asked the keychain for the credential itself: %v", command)
		}
	}
}

func TestAStoredMCPCredentialIsReportedWithoutItsValue(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	world.configuration = mcpConfig
	file := mcpcredential.File(world.stateRoot, "yoyodyne", "tickets")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("tickets-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mcpcredential.ServerDirectory(world.stateRoot, "yoyodyne", "mail"), 0o700); err != nil {
		t.Fatal(err)
	}
	report := world.diagnose()
	for _, check := range []string{"mcp:mail", "mcp:tickets", "mcp:calendar"} {
		finding, _ := findingFor(report, check)
		if finding.Status != StatusOK {
			t.Errorf("%s = %s %q, want ok", check, finding.Status, finding.Summary)
		}
		if strings.Contains(finding.Summary+finding.Detail, "tickets-secret") {
			t.Fatalf("%s carries the credential: %+v", check, finding)
		}
	}
}

const reviewThreadsConfig = mcpConfig + `review_threads:
  server: mail
  post_tool: send
  read_tool: search
  channel: C0123456789
  reviewers: [U0AAAA1111]
`

// A refused review_threads section is the configuration that does not load, and
// the finding says which setting and why, in words.
func TestARefusedReviewThreadsSectionNamesTheSetting(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	world.configuration = strings.Replace(reviewThreadsConfig, "reviewers: [U0AAAA1111]", "reviewers: []", 1)
	report := world.diagnose()
	finding, found := findingFor(report, "configuration")
	if !found || finding.Status != StatusProblem {
		t.Fatalf("configuration = %#v, want a problem: %s", finding, render(report))
	}
	if !strings.Contains(finding.Detail, "review_threads.reviewers is empty") {
		t.Fatalf("detail = %q, want the setting named", finding.Detail)
	}
}

func TestAProjectWithNoReviewThreadsSectionIsToldNothingAboutIt(t *testing.T) {
	t.Parallel()
	if _, found := findingFor(newWorld(t).diagnose(), "review_threads"); found {
		t.Fatal("a project with no review_threads section was reported one")
	}
}
