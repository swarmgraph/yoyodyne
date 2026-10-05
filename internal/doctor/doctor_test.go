package doctor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// TestEveryUnhealthyFindingCarriesARunnableRemedy is the promise the whole
// package exists to make, held against the matrix of states an installation is
// actually found in. A state that can be described and not routed out of is a
// defect here rather than a fact about the state, so this walks the states
// rather than sampling them: every one of them must produce a remedy, and a
// remedy must be a command rather than an instruction to go and read something.
func TestEveryUnhealthyFindingCarriesARunnableRemedy(t *testing.T) {
	t.Parallel()

	for name, broken := range brokenInstallations() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t)
			broken(world)
			report := world.diagnose()

			if report.Status == StatusOK {
				t.Fatalf("Diagnose() found nothing wrong with %q: %s", name, render(report))
			}
			for _, finding := range report.Findings {
				if finding.Status == StatusOK {
					continue
				}
				if strings.TrimSpace(finding.Remedy) == "" {
					t.Fatalf("finding %q is %s with no remedy: %s", finding.Check, finding.Status, finding.Summary)
				}
				if strings.TrimSpace(finding.Summary) == "" {
					t.Fatalf("finding %q has a remedy and says nothing it remedies", finding.Check)
				}
				// A remedy is a command. Prose telling somebody where to look is
				// exactly what this package exists instead of.
				if strings.HasSuffix(finding.Remedy, ".") || strings.Contains(finding.Remedy, " see ") {
					t.Fatalf("finding %q remedy is prose rather than a command: %q", finding.Check, finding.Remedy)
				}
			}
		})
	}
}

// brokenInstallations enumerates the states doctor has to be complete over.
// States produced by editing a working configuration are first-class here
// alongside states produced by never having installed something, because on a
// machine that has been running for months the second kind has stopped
// happening and the first kind has not.
func brokenInstallations() map[string]func(*world) {
	return map[string]func(*world){
		"yoyo is not on PATH": func(w *world) {
			w.absent("yoyo")
		},
		"the yoyo on PATH is a different build": func(w *world) {
			w.runner.reply("yoyo version", succeeded("v0.0.1"))
		},
		"the yoyo on PATH will not run": func(w *world) {
			w.runner.fail("yoyo version", errors.New("exec format error"))
		},
		"git is not installed": func(w *world) {
			w.absent("git")
		},
		"the project is not a repository": func(w *world) {
			w.runner.reply("rev-parse --git-dir", failed("not a git repository"))
		},
		"the repository has no commits": func(w *world) {
			w.runner.reply("rev-parse HEAD", failed("ambiguous argument 'HEAD'"))
		},
		"bd is not installed": func(w *world) {
			w.absent("bd")
		},
		"bd cannot read this project": func(w *world) {
			w.runner.reply("stats", failed("no beads database here"))
		},
		"there is no configuration": func(w *world) {
			w.configError = errors.New("no .yoyodyne/config.yaml was found above this directory")
		},
		"the configuration does not load": func(w *world) {
			w.configError = errors.New("checks must not be empty")
		},
		"no checks are configured": func(w *world) {
			w.configuration = strings.Replace(healthyConfig, "  - go test ./...\n", "", 1)
			w.configuration = strings.Replace(w.configuration, "checks:\n", "checks: []\n", 1)
		},
		"a configured check names a program this machine does not have": func(w *world) {
			w.configuration = strings.Replace(healthyConfig, "go test ./...", "cargo test --all", 1)
		},
		"the product ships the dashboard and node is not installed": func(w *world) {
			w.shipsDashboard()
			w.absent("node")
		},
		"an artifact home has no index at its door": func(w *world) {
			w.undocument("docs/designs")
		},
		"an artifact home's index has stopped answering": func(w *world) {
			w.rewriteIndex("docs/decisions", "# docs/decisions\n\nWhatever anybody felt like writing.\n")
		},
		"a running dashboard's build cannot read a key in the configuration": func(w *world) {
			w.configuration = effortConfig
			w.runningPart("dashboard", 4242, staleBuild, schemaWithout("agents.*.effort"))
		},
		"the provider is not installed": func(w *world) {
			w.absent("claude")
		},
		"the provider is not authenticated": func(w *world) {
			w.runner.reply("auth status --json", succeeded(`{"loggedIn":false}`))
		},
		"the project publishes and gh is not installed": func(w *world) {
			w.configuration = publishingConfig
			w.absent("gh")
		},
		"the project publishes and gh is not authenticated": func(w *world) {
			w.configuration = publishingConfig
			w.runner.reply("gh auth status", failed("You are not logged into any GitHub hosts"))
		},
		"the project publishes and has no such remote": func(w *world) {
			w.configuration = publishingConfig
			w.runner.reply("remote get-url", failed("No such remote 'origin'"))
		},
		"reporting is on and this project's secrets are not stored": func(w *world) {
			w.configuration = reportingConfig
			w.runner.reply("find-generic-password", failed("The specified item could not be found in the keychain."))
			w.sinkRunning(slack.Presence{Version: currentVersion, SecretNamespace: "yoyodyne", Channel: "C1"})
		},
		"reporting is on and this project's secrets file was made and never filled in": func(w *world) {
			w.configuration = reportingConfig
			w.secretsFile("")
			w.sinkRunning(slack.Presence{Version: currentVersion, SecretNamespace: "yoyodyne", Channel: "C1"})
		},
		"reporting is on, this is not macOS, and no environment file holds the secrets": func(w *world) {
			w.goos = "linux"
			w.configuration = reportingConfig
			w.sinkRunning(slack.Presence{Version: currentVersion, SecretNamespace: "yoyodyne", Channel: "C1"})
		},
		"reporting is on and no sink is running": func(w *world) {
			w.configuration = reportingConfig
		},
		"reporting is on and the sink that was running has died": func(w *world) {
			w.configuration = reportingConfig
			w.sinkRecorded(slack.Presence{Version: currentVersion, PID: 4242, SecretNamespace: "yoyodyne"})
		},
		"the running sink is an older build": func(w *world) {
			w.configuration = reportingConfig
			w.sinkRunning(slack.Presence{Version: "v0.0.1", PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1"})
		},
		"the running sink holds another project's secrets": func(w *world) {
			w.configuration = reportingConfig
			w.sinkRunning(slack.Presence{Version: currentVersion, PID: 4242, SecretNamespace: "sibling", Channel: "C1"})
		},
		"the running sink was started from a shell": func(w *world) {
			w.configuration = reportingConfig
			w.sinkRunning(slack.Presence{Version: currentVersion, PID: 4242, Channel: "C1"})
		},
		"slack.channel was edited on a working install": func(w *world) {
			w.configuration = strings.Replace(reportingConfig, "channel: C1", "channel: C2", 1)
			w.sinkRunning(slack.Presence{Version: currentVersion, PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1"})
		},
		"a sink is running and recorded nothing about itself": func(w *world) {
			w.configuration = reportingConfig
			w.sinkLeaseHeld()
		},
		"the running sink read another configuration": func(w *world) {
			w.configuration = reportingConfig
			w.sinkRunning(slack.Presence{
				Version:         currentVersion,
				PID:             4242,
				SecretNamespace: "yoyodyne",
				Channel:         "C1",
				Config:          "/somewhere/else/.yoyodyne/config.yaml",
			})
		},
		"the state root cannot be written": func(w *world) {
			w.stateRoot = "relative/not/absolute"
		},
		"every agent runs on one model and none names an alternate": func(w *world) {
			w.configuration = singleModelConfig
		},
		"a provider is authenticated by a key exported in the environment": func(w *world) {
			w.variables["ANTHROPIC_API_KEY"] = "sk-exported-in-a-shell-profile"
		},
		"the slack service is on and this project's secrets are not stored": func(w *world) {
			w.configuration = servicesConfig("slack:\n    enabled: true\n")
			w.runner.reply("find-generic-password", failed("The specified item could not be found in the keychain."))
			w.sinkRunning(slack.Presence{Version: currentVersion, SecretNamespace: "yoyodyne", Channel: "C1"})
		},
		"the dashboard service is on and its keychain token was never stored": func(w *world) {
			w.configuration = servicesConfig("dashboard:\n    enabled: true\n    bind: 192.168.1.20\n    token: keychain\n")
			w.runner.reply("find-generic-password -s yoyo-dashboard", failed("The specified item could not be found in the keychain."))
		},
		"the dashboard service is on with a keychain token on a machine with no keychain": func(w *world) {
			w.goos = "linux"
			w.configuration = servicesConfig("dashboard:\n    enabled: true\n    bind: 192.168.1.20\n    token: keychain\n")
		},
		"the dashboard service is on and its token file was never written": func(w *world) {
			w.configuration = servicesConfig("dashboard:\n    enabled: true\n    bind: 192.168.1.20\n    token: file\n")
		},
		"the dashboard service is on and its token file is empty": func(w *world) {
			w.configuration = servicesConfig("dashboard:\n    enabled: true\n    bind: 192.168.1.20\n    token: file\n")
			w.dashboardTokenFile("", 0o600)
		},
		"the dashboard service's token file is readable by others": func(w *world) {
			w.configuration = servicesConfig("dashboard:\n    enabled: true\n    bind: 192.168.1.20\n    token: file\n")
			w.dashboardTokenFile("0123456789abcdef0123456789abcdef", 0o644)
		},
	}
}

// The services a product declares are each reported: off, on with what they
// need in place, or on with what they need missing and the command that stores
// it. A healthy project has the scheduler and the maintenance pass on and needs
// nothing stored for either, so every service finding is ok.
func TestEveryDeclaredServiceIsReported(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	report := world.diagnose()
	for check, want := range map[string]string{
		"service:slack":       "the slack service is off",
		"service:dashboard":   "the dashboard service is off",
		"service:scheduler":   "the scheduler service is on",
		"service:maintenance": "the maintenance service is on",
	} {
		finding, found := findingFor(report, check)
		if !found {
			t.Fatalf("Diagnose() never checked %q: %s", check, render(report))
		}
		if finding.Status != StatusOK || !strings.Contains(finding.Summary, want) {
			t.Errorf("%s = %s %q, want ok and %q in it", check, finding.Status, finding.Summary, want)
		}
	}
}

// The Slack service is on and its tokens are not stored: the product would
// start a sink that refuses to run for want of them. The finding says so and
// carries the same store command the secrets check does, because both are one
// question asked of one keychain.
func TestTheSlackServiceWithoutItsTokensIsAWarningWithTheStoreCommand(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = servicesConfig("slack:\n    enabled: true\n")
	world.runner.reply("find-generic-password", failed("The specified item could not be found in the keychain."))
	report := world.diagnose()

	finding, found := findingFor(report, "service:slack")
	if !found {
		t.Fatalf("Diagnose() never checked the slack service: %s", render(report))
	}
	if finding.Status != StatusWarning {
		t.Fatalf("service:slack = %s, want a warning: a sink that cannot start stops no run", finding.Status)
	}
	if !strings.Contains(finding.Summary, "tokens are not stored") {
		t.Errorf("summary = %q, want the missing tokens named", finding.Summary)
	}
	secrets, _ := findingFor(report, "slack-secrets")
	if finding.Remedy == "" || finding.Remedy != secrets.Remedy {
		t.Errorf("remedy = %q, want the secrets check's own %q", finding.Remedy, secrets.Remedy)
	}
	if !report.Healthy() {
		t.Fatalf("Diagnose() = %s, want an installation that still runs work: %s", report.Status, render(report))
	}
}

// Node is what draws the dashboard page, so a product that ships the dashboard
// is asked whether this machine has it and every other product is asked nothing.
// The three answers are the three the render test itself gives, which is what
// keeps the diagnosis and the checks one statement rather than two.
func TestNodeIsAskedOfAProductThatShipsTheDashboard(t *testing.T) {
	t.Parallel()

	t.Run("a product that does not ship the dashboard is not asked", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.absent("node")
		report := world.diagnose()
		if finding, found := findingFor(report, "node"); found {
			t.Fatalf("node = %s %q, want nothing said about a product with no dashboard to draw: %s", finding.Status, finding.Summary, render(report))
		}
	})

	t.Run("node installed is ok", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.shipsDashboard()
		report := world.diagnose()
		finding, found := findingFor(report, "node")
		if !found || finding.Status != StatusOK {
			t.Fatalf("node = %+v, found = %t, want ok: %s", finding, found, render(report))
		}
	})

	// An environment that declares its own absence has chosen not to have Node,
	// so it is worth knowing rather than a problem — and the declaration is
	// quoted, because which environment declared it is the whole of what a
	// reader acts on.
	t.Run("a declared absence is a warning naming the declaration", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.shipsDashboard()
		world.absent("node")
		world.variables[dashboard.NodeUnavailableVariable] = "this container is built without Node"
		report := world.diagnose()
		finding, _ := findingFor(report, "node")
		if finding.Status != StatusWarning {
			t.Fatalf("node = %s %q, want a warning: a declared absence stops no run here", finding.Status, finding.Summary)
		}
		if !strings.Contains(finding.Detail, "this container is built without Node") {
			t.Errorf("detail = %q, want the declaration quoted", finding.Detail)
		}
		if !report.Healthy() {
			t.Fatalf("Diagnose() = %s, want an installation that still runs work: %s", report.Status, render(report))
		}
	})

	// Nothing declaring it is the case this whole arrangement exists for: the
	// machine simply never installed Node, the render test now fails there, and
	// the finding says so before the check does.
	t.Run("an undeclared absence is a problem naming what to install", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.shipsDashboard()
		world.absent("node")
		report := world.diagnose()
		finding, _ := findingFor(report, "node")
		if finding.Status != StatusProblem {
			t.Fatalf("node = %s %q, want a problem: the render test fails on such a machine", finding.Status, finding.Summary)
		}
		if finding.Remedy != "brew install node" {
			t.Errorf("remedy = %q, want the install command for this platform", finding.Remedy)
		}
		if !strings.Contains(finding.Detail, dashboard.NodeDocumentation) {
			t.Errorf("detail = %q, want %s named as where Node is written down", finding.Detail, dashboard.NodeDocumentation)
		}
	})
}

// A dashboard bound outside loopback names where its token is stored, and the
// diagnosis looks there: a keychain item that carries the product, or a file
// under the state root. Stored is ok and says where; missing is a warning with
// the command that stores it; and neither ever reads the token.
func TestTheDashboardServiceIsCheckedAgainstTheStoreItsEntryNames(t *testing.T) {
	t.Parallel()

	t.Run("a keychain token that is stored", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.configuration = servicesConfig("dashboard:\n    enabled: true\n    bind: 192.168.1.20\n    token: keychain\n")
		report := world.diagnose()
		finding, _ := findingFor(report, "service:dashboard")
		if finding.Status != StatusOK || !strings.Contains(finding.Summary, "yoyo-dashboard.yoyodyne") || !strings.Contains(finding.Summary, "192.168.1.20:8765") {
			t.Fatalf("service:dashboard = %s %q, want ok naming the item and the address", finding.Status, finding.Summary)
		}
		for _, command := range world.runner.commands {
			if strings.Contains(strings.Join(command, " "), "yoyo-dashboard") && contains(command, "-w") {
				t.Fatalf("the diagnosis asked the keychain for the token itself: %v", command)
			}
		}
	})
	t.Run("a keychain token that is missing", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.configuration = servicesConfig("dashboard:\n    enabled: true\n    bind: 192.168.1.20\n    token: keychain\n")
		world.runner.reply("find-generic-password -s yoyo-dashboard", failed("The specified item could not be found in the keychain."))
		report := world.diagnose()
		finding, _ := findingFor(report, "service:dashboard")
		if finding.Status != StatusWarning {
			t.Fatalf("service:dashboard = %s, want a warning", finding.Status)
		}
		if want := "security add-generic-password -s yoyo-dashboard.yoyodyne -a yoyo -w"; finding.Remedy != want {
			t.Errorf("remedy = %q, want %q", finding.Remedy, want)
		}
	})
	t.Run("a file token that is stored", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.configuration = servicesConfig("dashboard:\n    enabled: true\n    bind: 192.168.1.20\n    token: file\n")
		file := world.dashboardTokenFile("0123456789abcdef0123456789abcdef", 0o600)
		report := world.diagnose()
		finding, _ := findingFor(report, "service:dashboard")
		if finding.Status != StatusOK || finding.Detail != file {
			t.Fatalf("service:dashboard = %s %q (%q), want ok naming %s", finding.Status, finding.Summary, finding.Detail, file)
		}
	})
	t.Run("a file token that is missing", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.configuration = servicesConfig("dashboard:\n    enabled: true\n    bind: 192.168.1.20\n    token: file\n")
		report := world.diagnose()
		finding, _ := findingFor(report, "service:dashboard")
		if finding.Status != StatusWarning || !strings.Contains(finding.Remedy, "dashboard.token") {
			t.Fatalf("service:dashboard = %s with remedy %q, want a warning whose remedy writes the file", finding.Status, finding.Remedy)
		}
	})
	t.Run("a loopback dashboard with a generated token needs nothing stored", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.configuration = servicesConfig("dashboard:\n    enabled: true\n")
		report := world.diagnose()
		finding, _ := findingFor(report, "service:dashboard")
		if finding.Status != StatusOK || !strings.Contains(finding.Summary, "generated at each start") {
			t.Fatalf("service:dashboard = %s %q, want ok with the generated token named", finding.Status, finding.Summary)
		}
	})
}

// TestAHealthyInstallationSaysSo is the other half of the promise. An operator
// who has fixed everything has to be told that in so many words, because an
// empty list of complaints and a diagnosis that never ran read the same.
func TestAHealthyInstallationSaysSo(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	report := world.diagnose()

	if report.Status != StatusOK || !report.Healthy() {
		t.Fatalf("Diagnose() = %s, want a healthy installation: %s", report.Status, render(report))
	}
	if report.Product != "yoyodyne" || report.Config == "" {
		t.Fatalf("Diagnose() product = %q, config = %q", report.Product, report.Config)
	}
	if _, warnings, problems := report.Counts(); warnings != 0 || problems != 0 {
		t.Fatalf("Diagnose() = %d warnings, %d problems: %s", warnings, problems, render(report))
	}
	for _, want := range []string{"path", "binary", "git", "repository", "tracker", "state", "checks", "artifact-readmes", "provider:claude-code", "failover", "forge", "slack", "service:slack", "service:dashboard", "service:scheduler", "service:maintenance"} {
		if finding, found := findingFor(report, want); !found {
			t.Fatalf("Diagnose() never checked %q: %s", want, render(report))
		} else if finding.Status != StatusOK {
			t.Fatalf("check %q = %s on a healthy installation", want, finding.Status)
		}
	}
}

// A project whose every agent asks one model and names no alternate is the
// condition that made a five-day stoppage out of one provider window. It is a
// warning and never a problem: every run proceeds exactly as it would have,
// right up to the window closing.
func TestEveryAgentOnOneModelWithNoAlternateIsAWarning(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = singleModelConfig
	report := world.diagnose()

	finding, found := findingFor(report, "failover")
	if !found {
		t.Fatalf("Diagnose() never checked failover: %s", render(report))
	}
	if finding.Status != StatusWarning {
		t.Fatalf("failover = %s, want a warning: a single model stops no work until its window closes", finding.Status)
	}
	if !report.Healthy() {
		t.Fatalf("Diagnose() = %s, want an installation that still runs work: %s", report.Status, render(report))
	}
	for _, want := range []string{"one model", "opus on claude-code", "none names an alternate"} {
		if !strings.Contains(finding.Summary, want) {
			t.Fatalf("summary = %q, want %q in it", finding.Summary, want)
		}
	}
	for _, want := range []string{"stops every role", "failover.enabled", "failover.model"} {
		if !strings.Contains(finding.Detail, want) {
			t.Fatalf("detail = %q, want %q in it", finding.Detail, want)
		}
	}
	if !strings.Contains(finding.Remedy, "config.yaml") {
		t.Fatalf("remedy = %q, want the configuration named as what to edit", finding.Remedy)
	}
}

// The same project with one alternate named is not the condition, and the
// healthy line says how far the cover goes rather than only that it is fine.
func TestAnAlternateOnAnyAgentIsNotTheSingleModelCondition(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = strings.Replace(singleModelConfig,
		"  reviewer:\n", "    failover:\n      enabled: true\n      model: sonnet\n  reviewer:\n", 1)
	report := world.diagnose()

	finding, found := findingFor(report, "failover")
	if !found {
		t.Fatalf("Diagnose() never checked failover: %s", render(report))
	}
	if finding.Status != StatusOK {
		t.Fatalf("failover = %s, want ok: one agent fails over, so the window does not hold every role", finding.Status)
	}
	if !strings.Contains(finding.Summary, "1 of them names an alternate") {
		t.Fatalf("summary = %q, want the cover counted", finding.Summary)
	}
}

// An undocumented artifact home is a warning and never a problem: nothing about
// it stops a run, refuses an invocation, or fails a check. What it costs is paid
// by the next person to open the directory, which is what a warning is for.
func TestAnUndocumentedArtifactHomeIsAWarningWithTheFileNamed(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.undocument("docs/product/goals")
	report := world.diagnose()

	finding, found := findingFor(report, "artifact-readmes")
	if !found {
		t.Fatalf("Diagnose() never checked the artifact homes: %s", render(report))
	}
	if finding.Status != StatusWarning {
		t.Fatalf("artifact-readmes = %s, want a warning: an undocumented directory stops no work", finding.Status)
	}
	if !report.Healthy() {
		t.Fatalf("Diagnose() = %s, want an installation that still runs work: %s", report.Status, render(report))
	}
	if !strings.Contains(finding.Detail, "docs/product/goals/README.md") {
		t.Fatalf("detail = %q, want the index that is not there named", finding.Detail)
	}
	if !strings.Contains(finding.Remedy, "yoyo setup") {
		t.Fatalf("remedy = %q, want the command that writes it", finding.Remedy)
	}
	// Only the one that is missing is named: a report that listed every home
	// would be one nobody reads to the end of.
	if strings.Contains(finding.Detail, "docs/designs/README.md") {
		t.Fatalf("detail = %q, want the homes that are documented left out of it", finding.Detail)
	}
}

// TestAStaleSinkIsNamedAsStale covers the outage this check was written for. A
// sink that keeps running while the binary under it moves on posts what its own
// build knew how to post and silently drops everything added since, and the
// channel reads as a quiet week rather than as a broken installation.
func TestAStaleSinkIsNamedAsStale(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.sinkRunning(slack.Presence{Version: "v0.0.1", PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1"})
	report := world.diagnose()

	finding, found := findingFor(report, "slack-sink-version")
	if !found {
		t.Fatalf("Diagnose() never compared the sink's build: %s", render(report))
	}
	if finding.Status != StatusWarning {
		t.Fatalf("slack-sink-version = %s, want a warning: %s", finding.Status, finding.Summary)
	}
	// Both builds have to be named. "Restart the sink" over an unnamed drift is
	// advice; the two versions side by side are the evidence.
	if !strings.Contains(finding.Detail, "v0.0.1") || !strings.Contains(finding.Detail, currentVersion) {
		t.Fatalf("slack-sink-version detail = %q, want both builds named", finding.Detail)
	}
	// The remedy stops the sink by the pid it recorded. A pattern that matched
	// its command line would match a sibling project's sink on the same machine.
	if !strings.Contains(finding.Remedy, "kill 4242") {
		t.Fatalf("slack-sink-version remedy = %q, want the recorded pid stopped", finding.Remedy)
	}
	if !strings.Contains(finding.Remedy, "yoyo slack") {
		t.Fatalf("slack-sink-version remedy = %q, want a sink started again", finding.Remedy)
	}
}

// The same outage on the installation it actually happened on. A harness
// developing itself runs nothing but unreleased binaries, so a sink started a
// week ago and the binary asking about it report one version and compare as
// identical — a clean report over exactly the drift being looked for. Two
// revisions are two places in one history, and they settle it.
func TestASinkOnAnotherRevisionOfOneVersionIsNamedAsStale(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.build = currentBuild
	world.sinkRunning(slack.Presence{
		Version: currentVersion, Build: staleBuild, PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1",
	})
	report := world.diagnose()

	finding, found := findingFor(report, "slack-sink-version")
	if !found {
		t.Fatalf("Diagnose() never compared the sink's build: %s", render(report))
	}
	if finding.Status != StatusWarning {
		t.Fatalf("slack-sink-version = %s, want a warning: %s", finding.Status, finding.Summary)
	}
	// Both revisions have to be named, for the reason both versions are: "restart
	// the sink" over an unnamed drift is advice, and the two side by side are the
	// evidence somebody checks.
	if !strings.Contains(finding.Detail, staleBuild[:12]) || !strings.Contains(finding.Detail, currentBuild[:12]) {
		t.Fatalf("slack-sink-version detail = %q, want both revisions named", finding.Detail)
	}
	if !strings.Contains(finding.Remedy, "kill 4242") {
		t.Fatalf("slack-sink-version remedy = %q, want the recorded pid stopped", finding.Remedy)
	}

	// A sink on the same revision is the ordinary healthy state and says so.
	agreeing := newWorld(t)
	agreeing.configuration = reportingConfig
	agreeing.build = currentBuild
	agreeing.sinkRunning(slack.Presence{
		Version: currentVersion, Build: currentBuild, PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1",
	})
	healthy, found := findingFor(agreeing.diagnose(), "slack-sink-version")
	if !found || healthy.Status != StatusOK {
		t.Fatalf("slack-sink-version = %#v, want a sink on the deployed revision reported healthy", healthy)
	}

	// And a pair with no revisions to compare says the comparison was not made,
	// rather than reporting the versions agreeing as if it had been. On an install
	// where every build answers one version, that agreement is not evidence.
	unstamped := newWorld(t)
	unstamped.configuration = reportingConfig
	unstamped.sinkRunning(slack.Presence{
		Version: currentVersion, PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1",
	})
	unchecked, found := findingFor(unstamped.diagnose(), "slack-sink-version")
	if !found || unchecked.Status != StatusOK {
		t.Fatalf("slack-sink-version = %#v, want a version that agrees reported healthy", unchecked)
	}
	if !strings.Contains(unchecked.Summary, "no pair of revisions") {
		t.Fatalf("slack-sink-version summary = %q, want it to say the revisions could not be compared", unchecked.Summary)
	}
}

// The version on PATH is not always readable, and the revisions still are. A
// binary that answers the version question with nothing leaves the drift check
// above with nothing to compare, so the finding must not go on to describe the
// sink's own version as the installed one — which would put the sink on both
// sides of a comparison it is one half of, and claim an agreement nobody checked.
func TestASinkIsNotComparedAgainstAnInstalledVersionNobodyCouldRead(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.build = currentBuild
	world.runner.reply("yoyo version", succeeded(""))
	world.sinkRunning(slack.Presence{
		Version: currentVersion, Build: staleBuild, PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1",
	})

	finding, found := findingFor(world.diagnose(), "slack-sink-version")
	if !found {
		t.Fatalf("Diagnose() never compared the sink's build")
	}
	// The revisions differ, which is the whole of what could be compared, so it is
	// still the warning — said in the words that are true of it.
	if finding.Status != StatusWarning {
		t.Fatalf("slack-sink-version = %s, want a warning: %s", finding.Status, finding.Summary)
	}
	if strings.Contains(finding.Summary, "report one version") {
		t.Fatalf("slack-sink-version summary = %q, want it not to claim a version comparison nobody made", finding.Summary)
	}
	// The detail names the sink's version once, on the sink's side. Reporting it
	// on both is what made the line read as two builds of one version.
	if strings.Count(finding.Detail, currentVersion) != 1 {
		t.Fatalf("slack-sink-version detail = %q, want the sink's version on the sink's side alone", finding.Detail)
	}
	if !strings.Contains(finding.Detail, staleBuild[:12]) || !strings.Contains(finding.Detail, currentBuild[:12]) {
		t.Fatalf("slack-sink-version detail = %q, want both revisions named", finding.Detail)
	}

	// And with no revisions either, nothing whatever was compared, which the
	// finding says rather than reporting an installed version there is none of.
	blind := newWorld(t)
	blind.configuration = reportingConfig
	blind.runner.reply("yoyo version", succeeded(""))
	blind.sinkRunning(slack.Presence{
		Version: currentVersion, PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1",
	})
	quiet, found := findingFor(blind.diagnose(), "slack-sink-version")
	if !found || quiet.Status != StatusOK {
		t.Fatalf("slack-sink-version = %#v, want a comparison nobody could make reported as one", quiet)
	}
	if strings.Contains(quiet.Summary, "the installed version") {
		t.Fatalf("slack-sink-version summary = %q, want it not to name an installed version nobody read", quiet.Summary)
	}
}

// TestSecretsAreCheckedForThisInstanceRatherThanForAnyToken is the
// multi-harness case. "A Slack token exists somewhere on this machine" passes
// for every project on it and is right for at most one, so what is asked for is
// this product's own pair under names that carry the product.
func TestSecretsAreCheckedForThisInstanceRatherThanForAnyToken(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	// The sibling's pair is in the keychain and this project's is not, which is
	// exactly the state a generic check calls healthy.
	world.runner.reply("find-generic-password", failed("The specified item could not be found in the keychain."))
	world.runner.reply("yoyo-slack-bot.sibling", succeeded("password has been found"))
	world.runner.reply("yoyo-slack-app.sibling", succeeded("password has been found"))
	report := world.diagnose()

	finding, found := findingFor(report, "slack-secrets")
	if !found || finding.Status != StatusWarning {
		t.Fatalf("slack-secrets = %#v, want this project's missing pair reported: %s", finding, render(report))
	}
	for _, want := range []string{"yoyo-slack-bot.yoyodyne", "yoyo-slack-app.yoyodyne"} {
		if !strings.Contains(finding.Remedy, want) {
			t.Fatalf("slack-secrets remedy = %q, want the namespaced name %q", finding.Remedy, want)
		}
	}
	// Storing a token by pasting it onto a command line puts it in a shell
	// history. The keychain prompts for it instead, which `-w` with no value is.
	if strings.Contains(finding.Remedy, "xoxb") || strings.Contains(finding.Remedy, "-w '") {
		t.Fatalf("slack-secrets remedy = %q, want the value prompted for rather than written", finding.Remedy)
	}
}

// storedTokens is a filled environment file, written the way the setup document
// says to write one: two `export` lines, one of them quoted, because both are
// what an operator's own file looks like. Neither value is a token; what they
// are for is being findable in a report that should never contain them.
const storedTokens = "export SLACK_BOT_TOKEN=\"xoxb-not-a-real-token\"\nexport SLACK_APP_TOKEN=xapp-not-a-real-token\n"

// TestAnEmptySecretsFileIsNotSecretsStored is the state the file check used to
// certify. `yoyo doctor`'s own remedy for missing secrets installs an empty file
// and opens an editor on it, so an operator who leaves that editor without
// saving has exactly this: a file of the right name, in the right place, with
// the right mode, and no tokens in it. Told the secrets are stored, the next
// thing they meet is a sink refusing to start for want of the names this said
// nothing about.
func TestAnEmptySecretsFileIsNotSecretsStored(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	// The keychain answers for everything, which is what makes this about the
	// file: with the file there, the keychain is not what the sink will read.
	file := world.secretsFile("")
	report := world.diagnose()

	finding, found := findingFor(report, "slack-secrets")
	if !found || finding.Status != StatusWarning {
		t.Fatalf("slack-secrets = %#v, want the empty file reported: %s", finding, render(report))
	}
	// The names are the whole of the diagnosis: they are what the sink says it
	// wants when it dies, so a report that has them is one somebody can act on.
	for _, want := range []string{slack.BotTokenVariable, slack.AppTokenVariable} {
		if !strings.Contains(finding.Summary, want) {
			t.Fatalf("slack-secrets summary = %q, want %q named", finding.Summary, want)
		}
	}
	if !strings.Contains(finding.Detail, file) {
		t.Fatalf("slack-secrets detail = %q, want the file that is missing them named", finding.Detail)
	}
}

// TestASecretsFileMissingOneTokenNamesOnlyThatOne holds the report to what is
// actually wrong. Half a pair is the ordinary shape of an interrupted setup, and
// a diagnosis that named both would send somebody back to a line they already
// wrote.
func TestASecretsFileMissingOneTokenNamesOnlyThatOne(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	// The second line is there and assigns nothing, which is what the file looks
	// like when the second token was pasted somewhere else.
	world.secretsFile("export SLACK_BOT_TOKEN=xoxb-not-a-real-token\nexport SLACK_APP_TOKEN=\n")
	report := world.diagnose()

	finding, found := findingFor(report, "slack-secrets")
	if !found || finding.Status != StatusWarning {
		t.Fatalf("slack-secrets = %#v, want the half-filled file reported: %s", finding, render(report))
	}
	if !strings.Contains(finding.Summary, slack.AppTokenVariable) {
		t.Fatalf("slack-secrets summary = %q, want the one that is missing named", finding.Summary)
	}
	if strings.Contains(finding.Summary, slack.BotTokenVariable) {
		t.Fatalf("slack-secrets summary = %q, want the one that is stored left out of it", finding.Summary)
	}
	if strings.Contains(render(report), "xoxb-not-a-real-token") {
		t.Fatalf("a stored token was printed: %s", render(report))
	}
}

// TestAFilledSecretsFilePassesWithoutPrintingAToken is the other half: the file
// an operator finished is healthy, and reading it to find that out must not put
// what is in it anywhere. This output goes to a terminal, a scrollback, and
// whatever collects them, which is the same reason the keychain is asked for its
// items rather than for its passwords.
func TestAFilledSecretsFilePassesWithoutPrintingAToken(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.secretsFile(storedTokens)
	report := world.diagnose()

	finding, found := findingFor(report, "slack-secrets")
	if !found || finding.Status != StatusOK {
		t.Fatalf("slack-secrets = %#v, want a filled file to pass: %s", finding, render(report))
	}
	for _, secret := range []string{"xoxb-not-a-real-token", "xapp-not-a-real-token"} {
		if strings.Contains(render(report), secret) {
			t.Fatalf("a stored token was printed: %s", render(report))
		}
	}
}

// TestSecretsReadableByOthersAreStillReported keeps the check the token check
// was put in front of. Whose tokens they are is asked first because a file with
// no tokens in it has no permissions worth complaining about, and a file that
// does have them is still every other user's to read.
func TestSecretsReadableByOthersAreStillReported(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	file := world.secretsFile(storedTokens)
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	report := world.diagnose()

	finding, found := findingFor(report, "slack-secrets")
	if !found || finding.Status != StatusWarning {
		t.Fatalf("slack-secrets = %#v, want the mode reported: %s", finding, render(report))
	}
	if !strings.Contains(finding.Remedy, "chmod 600") {
		t.Fatalf("slack-secrets remedy = %q, want the mode fixed", finding.Remedy)
	}
}

// TestReportingNeverStopsWorkRunning holds doctor's exit status to what the rest
// of the harness already promises: reporting is an observation and never a gate,
// so no state of it may make an installation that runs work report that it
// cannot. It is asserted over every reporting state at once rather than one at a
// time, because the rule is about the class and one case reclassified later
// would slip past a per-case assertion.
func TestReportingNeverStopsWorkRunning(t *testing.T) {
	t.Parallel()

	for name, broken := range brokenInstallations() {
		if !strings.HasPrefix(name, "reporting is on") && !strings.HasPrefix(name, "the running sink") &&
			!strings.HasPrefix(name, "slack.channel") && !strings.HasPrefix(name, "a sink is running") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t)
			broken(world)
			report := world.diagnose()

			if !report.Healthy() {
				t.Fatalf("a stopped or misdirected sink made this installation report that it cannot run work: %s", render(report))
			}
			// Healthy is not the same as silent. Every one of these still has to
			// be named, with the command that ends it -- what is being held here
			// is the exit status, not the reporting.
			named := false
			for _, finding := range report.Findings {
				if strings.HasPrefix(finding.Check, "slack") && finding.Status == StatusWarning {
					named = true
					if strings.TrimSpace(finding.Remedy) == "" {
						t.Fatalf("%s is warned about with no remedy", finding.Check)
					}
				}
			}
			if !named {
				t.Fatalf("nothing was said about reporting at all: %s", render(report))
			}
		})
	}
}

// TestAChannelEditedOnAWorkingInstallIsNamed covers the config-edit
// case the completeness bar makes first-class. `slack.channel` is one line
// anybody can change on an install that is working, and changing it does nothing
// to the process already running: the sink keeps posting where it connected, the
// channel the project now names stays empty, and no error is raised by either.
func TestAChannelEditedOnAWorkingInstallIsNamed(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = strings.Replace(reportingConfig, "channel: C1", "channel: C2", 1)
	world.sinkRunning(slack.Presence{Version: currentVersion, PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1"})
	report := world.diagnose()

	finding, found := findingFor(report, "slack-sink-channel")
	if !found || finding.Status != StatusWarning {
		t.Fatalf("slack-sink-channel = %#v, want a warning: %s", finding, render(report))
	}
	if !strings.Contains(finding.Summary, "C1") || !strings.Contains(finding.Summary, "C2") {
		t.Fatalf("slack-sink-channel summary = %q, want both channels named", finding.Summary)
	}
	if !strings.Contains(finding.Remedy, "kill 4242") || !strings.Contains(finding.Remedy, "yoyo slack") {
		t.Fatalf("slack-sink-channel remedy = %q, want the running sink restarted", finding.Remedy)
	}
	// A sink whose channel still agrees is the control: this must not fire on
	// every reporting project, or it is a finding operators learn to skip past.
	agreeing := newWorld(t)
	agreeing.configuration = reportingConfig
	agreeing.sinkRunning(slack.Presence{Version: currentVersion, PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1"})
	if _, found := findingFor(agreeing.diagnose(), "slack-sink-channel"); found {
		t.Fatal("slack-sink-channel fired on a sink posting into the configured channel")
	}
}

// TestASinkThatSaidNothingCanStillBeStopped keeps a live sink out of the one
// state doctor cannot route out of. A second sink is refused while the first
// holds the lease, so "start one" is not a remedy on its own, and a sink that
// recorded no pid has to be found through the lease it is holding.
func TestASinkThatSaidNothingCanStillBeStopped(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.sinkLeaseHeld()
	report := world.diagnose()

	finding, found := findingFor(report, "slack-sink")
	if !found || finding.Status != StatusWarning {
		t.Fatalf("slack-sink = %#v, want a running sink that said nothing: %s", finding, render(report))
	}
	if !strings.HasPrefix(finding.Remedy, "kill ") {
		t.Fatalf("slack-sink remedy = %q, want the sink holding the lease stopped first", finding.Remedy)
	}
	if !strings.Contains(finding.Remedy, ".sink.lock") {
		t.Fatalf("slack-sink remedy = %q, want this product's own lease named", finding.Remedy)
	}
	if !strings.Contains(finding.Remedy, "yoyo slack") {
		t.Fatalf("slack-sink remedy = %q, want a sink started again afterwards", finding.Remedy)
	}
}

// TestAMachineWithNoKeychainIsStillRoutedOut keeps the completeness promise off
// macOS. A platform whose secret store this build cannot ask is not a state
// doctor gets to leave somebody in: the route out is the namespaced environment
// file, and the remedy has to name it.
func TestAMachineWithNoKeychainIsStillRoutedOut(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.goos = "linux"
	world.configuration = reportingConfig
	report := world.diagnose()

	secrets, found := findingFor(report, "slack-secrets")
	if !found || secrets.Status != StatusWarning {
		t.Fatalf("slack-secrets = %#v, want a warning: %s", secrets, render(report))
	}
	wanted := filepath.Join("yoyo", "yoyodyne", "slack.env")
	if !strings.Contains(secrets.Remedy, wanted) {
		t.Fatalf("slack-secrets remedy = %q, want the namespaced environment file %q", secrets.Remedy, wanted)
	}
	sink, found := findingFor(report, "slack-sink")
	if !found || !strings.Contains(sink.Remedy, wanted) {
		t.Fatalf("slack-sink remedy = %q, want the sink started from that file", sink.Remedy)
	}
	// The keychain is macOS's, so a machine without one must not be told to use
	// it -- a remedy that cannot run is worse than the problem it answers.
	if strings.Contains(secrets.Remedy, "security ") || strings.Contains(sink.Remedy, "security ") {
		t.Fatalf("a machine with no keychain was given a keychain remedy: %q / %q", secrets.Remedy, sink.Remedy)
	}
}

// TestASinkOnAnotherProjectsSecretsIsNamed is the failure the namespaced
// contract exists to make visible: a sink that connected, authenticated, and is
// posting this product's work through a sibling project's Slack app.
func TestASinkOnAnotherProjectsSecretsIsNamed(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.sinkRunning(slack.Presence{
		Version:         currentVersion,
		PID:             4242,
		SecretNamespace: "sibling",
		Channel:         "C1",
		Team:            "Sibling Workspace",
	})
	report := world.diagnose()

	finding, found := findingFor(report, "slack-sink-secrets")
	if !found || finding.Status != StatusWarning {
		t.Fatalf("slack-sink-secrets = %#v, want a warning: %s", finding, render(report))
	}
	if !strings.Contains(finding.Summary, "sibling") || !strings.Contains(finding.Summary, "yoyodyne") {
		t.Fatalf("slack-sink-secrets summary = %q, want both products named", finding.Summary)
	}
	// The workspace the tokens actually authenticated into is the one fact here
	// that came from the tokens rather than from what somebody claimed.
	if !strings.Contains(finding.Detail, "Sibling Workspace") {
		t.Fatalf("slack-sink-secrets detail = %q, want the workspace named", finding.Detail)
	}
	if !strings.Contains(finding.Remedy, "YOYO_SLACK_SECRET_NAMESPACE=yoyodyne") {
		t.Fatalf("slack-sink-secrets remedy = %q, want this project's secrets used", finding.Remedy)
	}
}

// TestNothingReadsATokenholds the credential boundary. This is a diagnostic
// whose output reaches a terminal, a scrollback, and whatever collects them, so
// nothing it runs may ask a store to produce a secret.
func TestNothingReadsAToken(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.sinkRunning(slack.Presence{Version: currentVersion, PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1"})
	world.diagnose()

	for _, command := range world.runner.commands {
		joined := strings.Join(command, " ")
		if !strings.Contains(joined, "find-generic-password") {
			continue
		}
		if contains(command, "-w") {
			t.Fatalf("the diagnosis asked the keychain for a password: %q", joined)
		}
	}
}

// TestADeadSinkIsNotMistakenForALiveOne separates the two questions the record
// and the lease answer. The record survives a process that was killed, so a
// sink's own file saying it is there is never taken as evidence that it is.
func TestADeadSinkIsNotMistakenForALiveOne(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.sinkRecorded(slack.Presence{Version: currentVersion, PID: 4242, Channel: "C1", StartedAt: time.Now().Add(-3 * time.Hour)})
	report := world.diagnose()

	finding, found := findingFor(report, "slack-sink")
	if !found || finding.Status != StatusWarning {
		t.Fatalf("slack-sink = %#v, want a stopped sink reported: %s", finding, render(report))
	}
	if !strings.Contains(finding.Detail, "4242") {
		t.Fatalf("slack-sink detail = %q, want the sink that died described", finding.Detail)
	}
}

// TestReportingOffIsHealthyRatherThanMissing keeps an opt-in observation from
// reading as a broken component. A project that reports nowhere is a project
// that decided to, and doctor reporting that as a defect would make the check
// one operators learn to ignore.
func TestReportingOffIsHealthyRatherThanMissing(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	report := world.diagnose()

	if finding, found := findingFor(report, "slack"); !found || finding.Status != StatusOK {
		t.Fatalf("slack = %#v, want reporting-off to be healthy", finding)
	}
	for _, absent := range []string{"slack-secrets", "slack-sink", "slack-sink-version", "slack-sink-secrets"} {
		if _, found := findingFor(report, absent); found {
			t.Fatalf("Diagnose() checked %q for a project that reports nowhere", absent)
		}
	}
}

// TestAForgeIsOnlyAskedAboutWhenTheHarnessPublishes holds the same line for the
// other opt-in. Everything stays on the machine until a project says otherwise,
// and a missing `gh` is then not a defect.
//
// The setting this turns on is worth being exact about, because `human` reads
// like an approval gate and is not one. `approvals.integration: human` does mean
// a person authorizes a promotion the harness still performs; `approvals.
// publishing: human` means nothing is pushed at all -- the design's publishing
// matrix gives both of its rows as "purely local", the field's own documentation
// says it "leaves pushing and pull requests to the operator", and
// Pipeline.resolvePublishing returns before the publisher is consulted.
// TestDoctorAgreesWithThePipelineAboutWhoPublishes, over in the orchestrator,
// is what keeps this reading and that behavior from drifting apart.
func TestAForgeIsOnlyAskedAboutWhenTheHarnessPublishes(t *testing.T) {
	t.Parallel()

	if !strings.Contains(healthyConfig, "publishing: human") {
		t.Fatal("this test is about a project on publishing: human and the fixture is not one")
	}
	world := newWorld(t)
	world.absent("gh")
	report := world.diagnose()

	finding, found := findingFor(report, "forge")
	if !found || finding.Status != StatusOK {
		t.Fatalf("forge = %#v, want a project the harness never publishes for to need no forge", finding)
	}
	// The summary is a claim about the harness, not about the operator: somebody
	// on `human` may push and open pull requests themselves, and doctor has no
	// business calling that project one that "does not publish".
	if strings.Contains(finding.Summary, "this project does not publish") {
		t.Fatalf("forge summary = %q, want a claim about what the harness does", finding.Summary)
	}
	if !report.Healthy() {
		t.Fatalf("Diagnose() = %s, want healthy: %s", report.Status, render(report))
	}
}

// TestAMissingConfigurationStillReportsTheToolsAroundIt keeps the first failure
// from swallowing the rest. Somebody in the wrong directory, or in a project
// that has never been initialized, is usually about to hit the tracker next.
func TestAMissingConfigurationStillReportsTheToolsAroundIt(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configError = errors.New("no .yoyodyne/config.yaml was found above this directory")
	report := world.diagnose()

	finding, found := findingFor(report, "configuration")
	if !found || finding.Status != StatusProblem || finding.Remedy != "yoyo init" {
		t.Fatalf("configuration = %#v, want `yoyo init`", finding)
	}
	for _, want := range []string{"path", "git", "tracker"} {
		if _, found := findingFor(report, want); !found {
			t.Fatalf("Diagnose() stopped before %q: %s", want, render(report))
		}
	}
}

// TestAnUnresolvableCheckIsFoundBeforeARunSpendsAnything is the case that costs
// most to discover late: a check naming a program this machine does not have
// fails every run at the same point, after the provider has already been paid.
func TestAnUnresolvableCheckIsFoundBeforeARunSpendsAnything(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = strings.Replace(healthyConfig, "go test ./...", "cargo test --all", 1)
	report := world.diagnose()

	finding, found := findingFor(report, "checks")
	if !found || finding.Status != StatusProblem {
		t.Fatalf("checks = %#v, want the missing program reported", finding)
	}
	if !strings.Contains(finding.Summary, "cargo") {
		t.Fatalf("checks summary = %q, want the program named", finding.Summary)
	}
}

// TestAShellPrefixOnACheckIsNotMistakenForAMissingProgram keeps the
// resolvability probe from inventing failures. A check line is a shell command,
// and the first word of one is not always a program.
func TestAShellPrefixOnACheckIsNotMistakenForAMissingProgram(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"CGO_ENABLED=0 go test ./...", "cd internal && go build ./...", "  go vet ./..."} {
		world := newWorld(t)
		world.configuration = strings.Replace(healthyConfig, "go test ./...", command, 1)
		report := world.diagnose()
		if finding, _ := findingFor(report, "checks"); finding.Status != StatusOK {
			t.Fatalf("checks for %q = %s: %s", command, finding.Status, finding.Summary)
		}
	}
}

// TestABackendThisBuildCannotExecuteIsRoutedOutOfTheConfiguration covers the one
// provider state no configuration can reach through the loader, which refuses a
// backend outside the vocabulary: a backend the vocabulary has grown and this
// build has no adapter for. Nothing can be installed that would give this build
// one, so the only thing an operator can act on is the file naming it, and a
// second diagnostic would leave the installation exactly as broken.
func TestABackendThisBuildCannotExecuteIsRoutedOutOfTheConfiguration(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	diagnosis := &diagnosis{env: Environment{Runner: world.runner, LookPath: world.lookPath}}
	configPath := filepath.Join(world.project, ".yoyodyne", "config.yaml")
	// Nothing describes it, which is what a backend no adapter can launch looks
	// like once the registry has been asked: no adapter, and so no executable.
	finding := diagnosis.checkProvider(context.Background(), "some-future-backend", backend.Descriptor{}, configPath)

	if finding.Status != StatusProblem {
		t.Fatalf("checkProvider() = %s, want a backend nothing can execute reported as a problem", finding.Status)
	}
	if !strings.Contains(finding.Remedy, configPath) {
		t.Fatalf("remedy = %q, want the file that has to change", finding.Remedy)
	}
	// A remedy that only asks the same question again is the failure mode this
	// package exists instead of, whether or not it happens to be a command.
	if strings.Contains(finding.Remedy, "yoyo config show") {
		t.Fatalf("remedy = %q, want a route out rather than a second diagnostic", finding.Remedy)
	}
}

// A provider the project declared is diagnosed as what it actually runs on: the
// executable its declaration named, on the adapter that launches it. Reporting
// it as a backend this build has no adapter for would send the operator to their
// configuration for something an install would have fixed.
func TestADeclaredProviderIsDiagnosedByTheExecutableItRuns(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	diagnosis := &diagnosis{env: Environment{Runner: world.runner, LookPath: world.lookPath}}
	declared := backend.Descriptor{
		ID:      "my-harness",
		Adapter: domain.BackendClaudeCode,
		Binary:  "my-harness",
	}
	finding := diagnosis.checkProvider(context.Background(), "my-harness", declared,
		filepath.Join(world.project, ".yoyodyne", "config.yaml"))

	if finding.Status != StatusProblem {
		t.Fatalf("checkProvider() = %s, want the missing executable reported", finding.Status)
	}
	if !strings.Contains(finding.Summary, "my-harness is not installed") {
		t.Fatalf("summary = %q, want the executable the declaration named", finding.Summary)
	}
	// The remedy is an install rather than the configuration, because this is a
	// provider the build can run and the operator has not installed.
	if !strings.Contains(finding.Remedy, "install") {
		t.Fatalf("remedy = %q, want the install that fixes it", finding.Remedy)
	}
}

// world is one installation, assembled broken or healthy. Everything a
// diagnosis reads is arranged here, because the states worth being complete
// over are exactly the ones nobody can arrange on a real machine on demand.
type world struct {
	t             *testing.T
	runner        *scriptedRunner
	missing       map[string]bool
	configuration string
	configError   error
	stateRoot     string
	project       string
	goos          string
	// build is the revision the diagnosing binary reports, which is what a sink's
	// own revision is checked against. A world that names none is the ordinary
	// released install, where the version is the whole of the comparison.
	build  string
	leases []*leaseHold
	// variables are what this machine's environment says beyond the state root,
	// which is how an environment that declares its own missing tools is
	// arranged.
	variables map[string]string
	// alive is the processes this machine has, by pid, for the records the
	// running parts of the product wrote about themselves.
	alive map[int]bool
}

const currentVersion = "v1.2.3"

// currentBuild and staleBuild are two revisions of one history: what the binary
// making the diagnosis was built from, and what a sink started a week earlier
// was. On a harness developing itself both of them answer the same version, so
// these are the only thing that can tell them apart.
const (
	currentBuild = "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900"
	staleBuild   = "0364141b2c3d4e5f60718293a4b5c6d7e8f9001a"
)

func newWorld(t *testing.T) *world {
	t.Helper()
	missing := map[string]bool{}
	w := &world{
		t:             t,
		runner:        &scriptedRunner{missing: missing},
		missing:       missing,
		configuration: healthyConfig,
		stateRoot:     t.TempDir(),
		project:       t.TempDir(),
		goos:          "darwin",
		variables:     map[string]string{},
		alive:         map[int]bool{},
	}
	// A machine where everything answers. Each broken installation is this with
	// exactly one thing changed, so what a test arranges is what it is about.
	w.runner.reply("yoyo version", succeeded(currentVersion))
	w.runner.reply("git --version", succeeded("git version 2.48.1"))
	w.runner.reply("rev-parse --git-dir", succeeded(".git"))
	w.runner.reply("rev-parse HEAD", succeeded("0123456789abcdef0123456789abcdef01234567"))
	w.runner.reply("stats", succeeded("open: 3"))
	w.runner.reply("claude --version", succeeded("2.0.0 (Claude Code)"))
	w.runner.reply("auth status --json", succeeded(`{"loggedIn":true,"authMethod":"subscription"}`))
	w.runner.reply("gh --version", succeeded("gh version 2.60.0"))
	w.runner.reply("gh auth status", succeeded("Logged in to github.com"))
	w.runner.reply("remote get-url", succeeded("git@github.com:example/thing.git"))
	w.runner.reply("find-generic-password", succeeded("keychain item"))
	// No operator's launchd job manages the product's parts beside the
	// supervisor on a machine where everything answers.
	w.runner.reply("launchctl print", failed("Could not find service \"com.yoyodyne.maintenance\" in domain for user gui: 501"))
	// A project `yoyo init` configured has an index at the door of every artifact
	// home, so a machine where everything answers has them too.
	w.documentArtifactHomes()
	t.Cleanup(func() {
		for _, held := range w.leases {
			held.release()
		}
	})
	return w
}

func (w *world) absent(program string) { w.missing[program] = true }

// documentArtifactHomes writes the index every artifact home gets, which is what
// `yoyo init` leaves behind and what a project configured before these existed
// does not have.
func (w *world) documentArtifactHomes() {
	w.t.Helper()
	root, err := repowrite.NewRoot(w.project)
	if err != nil {
		w.t.Fatalf("NewRoot() error = %v", err)
	}
	resolved, err := w.load()
	if err != nil {
		w.t.Fatalf("load() error = %v", err)
	}
	for _, home := range artifacthome.Homes(resolved.Config) {
		if _, err := artifacthome.Write(root, home); err != nil {
			w.t.Fatalf("Write(%s) error = %v", home.Path(), err)
		}
	}
}

// undocument deletes one home's index, which is the state a project configured
// before these existed is actually in.
func (w *world) undocument(directory string) {
	w.t.Helper()
	path := filepath.Join(w.project, filepath.FromSlash(directory), artifacthome.FileName)
	if err := os.Remove(path); err != nil {
		w.t.Fatalf("Remove() error = %v", err)
	}
}

// rewriteIndex replaces one home's index with prose that no longer answers the
// three questions, which is the other way a home stops being documented.
func (w *world) rewriteIndex(directory, content string) {
	w.t.Helper()
	path := filepath.Join(w.project, filepath.FromSlash(directory), artifacthome.FileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		w.t.Fatalf("WriteFile() error = %v", err)
	}
}

// secretsFile writes this project's environment file, which is the other place
// the pair is kept and the only one on a machine with no keychain. The contents
// are the test's, because what is in it -- both tokens, one, or nothing at all
// -- is the whole of what the states here differ by.
func (w *world) secretsFile(contents string) string {
	w.t.Helper()
	directory := filepath.Join(w.project, ".config", "yoyo", "yoyodyne")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		w.t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(directory, "slack.env")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		w.t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// dashboardTokenFile writes the file a file-sourced dashboard token is read
// from, under the product's state directory, with the mode the test is about.
func (w *world) dashboardTokenFile(contents string, mode os.FileMode) string {
	w.t.Helper()
	path := dashboard.TokenFile(w.stateRoot, "yoyodyne")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		w.t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		w.t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// sinkRecorded leaves the record a sink writes, without a process behind it.
func (w *world) sinkRecorded(presence slack.Presence) {
	w.t.Helper()
	store, err := slack.NewStore(w.stateRoot, "yoyodyne")
	if err != nil {
		w.t.Fatalf("NewStore() error = %v", err)
	}
	if presence.StartedAt.IsZero() {
		presence.StartedAt = time.Now().Add(-time.Hour)
	}
	if err := store.SavePresence(presence); err != nil {
		w.t.Fatalf("SavePresence() error = %v", err)
	}
}

// sinkRunning leaves that record and holds the lease, which together are what a
// live sink looks like from outside.
func (w *world) sinkRunning(presence slack.Presence) {
	w.t.Helper()
	w.sinkRecorded(presence)
	w.sinkLeaseHeld()
}

// sinkLeaseHeld holds the lease and records nothing, which is a sink from before
// there was a record to leave -- alive, and saying nothing about itself.
func (w *world) sinkLeaseHeld() {
	w.t.Helper()
	store, err := slack.NewStore(w.stateRoot, "yoyodyne")
	if err != nil {
		w.t.Fatalf("NewStore() error = %v", err)
	}
	lease, held, err := store.Lease()
	if err != nil || !held {
		w.t.Fatalf("Lease() = %t, %v, want the lease a running sink holds", held, err)
	}
	w.leases = append(w.leases, &leaseHold{release: func() { _ = lease.Release() }})
}

type leaseHold struct{ release func() }

func (w *world) diagnose() Report {
	w.t.Helper()
	return Diagnose(context.Background(), Environment{
		Runner:      w.runner,
		LookPath:    w.lookPath,
		Getenv:      w.getenv,
		UserHomeDir: func() (string, error) { return w.project, nil },
		GOOS:        w.goos,
		Version:     currentVersion,
		Build:       w.build,
		Load:        w.load,
		Now:         time.Now,
		ProcessRunning: func(pid int) (bool, error) {
			return w.alive[pid], nil
		},
	})
}

// runningPart records a part of the product as running, as pid, on build,
// reading this world's configuration with the keys given.
func (w *world) runningPart(service string, pid int, build string, keys []string) {
	w.t.Helper()
	store, err := runstate.NewConfigReaderStore(w.stateRoot, "yoyodyne")
	if err != nil {
		w.t.Fatalf("NewConfigReaderStore() error = %v", err)
	}
	err = store.Record(runstate.ConfigReader{
		Service:    service,
		PID:        pid,
		Build:      build,
		ConfigPath: filepath.Join(w.project, config.DirectoryName, "config.yaml"),
		StartedAt:  time.Date(2026, 9, 26, 23, 36, 0, 0, time.UTC),
		Keys:       keys,
	})
	if err != nil {
		w.t.Fatalf("Record() error = %v", err)
	}
	w.alive[pid] = true
}

// schemaWithout is this build's configuration keys less some, which is how a
// build from before those keys existed is arranged.
func schemaWithout(removed ...string) []string {
	return slices.DeleteFunc(config.SchemaKeys(), func(key string) bool { return slices.Contains(removed, key) })
}

// effortConfig is the healthy configuration carrying the key that reached the
// main checkout on 2026-09-28 ahead of the dashboard's build.
var effortConfig = strings.Replace(healthyConfig, "    model: opus\n", "    model: opus\n    effort: medium\n", 1)

// lookPath resolves only what this machine has. The set is enumerated rather
// than inverted so that a configuration naming a program nobody installed --
// the ordinary way a checks list goes wrong -- is a state a test can arrange by
// writing the configuration alone.
func (w *world) lookPath(program string) (string, error) {
	installed := map[string]bool{"yoyo": true, "git": true, "bd": true, "claude": true, "gh": true, "go": true, "security": true, "node": true}
	if !installed[program] || w.missing[program] {
		return "", errors.New("exec: \"" + program + "\": executable file not found in $PATH")
	}
	return filepath.Join("/usr/local/bin", program), nil
}

func (w *world) getenv(name string) string {
	switch name {
	case "YOYODYNE_STATE_HOME":
		return w.stateRoot
	default:
		return w.variables[name]
	}
}

// shipsDashboard puts the dashboard page's render script in the repository,
// which is what says a product ships the dashboard and is therefore asked about
// Node. The contents are irrelevant to the check, which asks whether the script
// is there rather than what it does.
func (w *world) shipsDashboard() {
	w.t.Helper()
	script := filepath.Join(w.project, filepath.FromSlash(dashboard.RenderScript))
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		w.t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(script, []byte("// the page's own renderer\n"), 0o644); err != nil {
		w.t.Fatalf("WriteFile() error = %v", err)
	}
}

func (w *world) load() (config.Resolved, error) {
	if w.configError != nil {
		return config.Resolved{}, w.configError
	}
	directory := filepath.Join(w.project, config.DirectoryName)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return config.Resolved{}, err
	}
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte(w.configuration), 0o644); err != nil {
		return config.Resolved{}, err
	}
	return config.LoadResolved(path)
}

func findingFor(report Report, check string) (Finding, bool) {
	for _, finding := range report.Findings {
		if finding.Check == check {
			return finding, true
		}
	}
	return Finding{}, false
}

// render is what a failing test prints, so a failure says what the whole
// diagnosis found rather than only the field that was compared.
func render(report Report) string {
	var built strings.Builder
	built.WriteString("\n")
	for _, finding := range report.Findings {
		built.WriteString(string(finding.Status) + "\t" + finding.Check + "\t" + finding.Summary + "\n")
		if finding.Remedy != "" {
			built.WriteString("\t\tfix: " + finding.Remedy + "\n")
		}
	}
	return built.String()
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// scriptedRunner answers the tools a diagnosis asks about, keyed on any
// substring of the command it would have run.
type scriptedRunner struct {
	commands [][]string
	// invocations are the same commands whole, kept beside the flattened ones
	// because some of what the harness decides is carried in the environment
	// rather than in the arguments -- which provider home an account is asked
	// about, for one -- and a flattened command line cannot show it.
	invocations []execution.Command
	replies     []scriptedReply
	failures    map[string]error
	// missing is shared with the world, so a program this machine does not have
	// refuses to start here exactly as it would refuse to start for real: the
	// adapters that ask a tool about itself distinguish "would not run" from
	// "ran and refused", and a test that could not produce the first would never
	// reach the remedy for it.
	missing map[string]bool
}

type scriptedReply struct {
	match string
	// env is an environment entry the command must also carry for this reply to
	// be the one. It is empty for a reply that does not care, which is every
	// reply about a machine with one provider home: two accounts are asked the
	// same question and are told apart only by the home it is asked in.
	env    string
	result execution.ProcessResult
}

// reply registers an answer. Later registrations are matched first, so a case
// can override one of the healthy defaults by naming the same command.
func (r *scriptedRunner) reply(match string, result execution.ProcessResult) {
	r.replies = append([]scriptedReply{{match: match, result: result}}, r.replies...)
}

// replyInEnv registers an answer for one command asked with one environment
// entry present, which is how a test says that this provider home answers
// differently from that one.
func (r *scriptedRunner) replyInEnv(match, env string, result execution.ProcessResult) {
	r.replies = append([]scriptedReply{{match: match, env: env, result: result}}, r.replies...)
}

func (r *scriptedRunner) fail(match string, err error) {
	if r.failures == nil {
		r.failures = map[string]error{}
	}
	r.failures[match] = err
}

func (r *scriptedRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	invocation := append([]string{command.Name}, command.Args...)
	r.commands = append(r.commands, invocation)
	r.invocations = append(r.invocations, command)
	joined := strings.Join(invocation, " ")
	if r.missing[filepath.Base(command.Name)] {
		return execution.ProcessResult{}, exec.ErrNotFound
	}
	for match, err := range r.failures {
		if strings.Contains(joined, match) {
			return execution.ProcessResult{}, err
		}
	}
	for _, reply := range r.replies {
		if !strings.Contains(joined, reply.match) {
			continue
		}
		if reply.env != "" && !slices.Contains(command.Env, reply.env) {
			continue
		}
		return reply.result, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

func succeeded(stdout string) execution.ProcessResult {
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: stdout}
}

func failed(stderr string) execution.ProcessResult {
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: stderr}
}

const healthyConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
  publishing: human
checks:
  - go test ./...
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    failover:
      enabled: true
      model: sonnet
`

// singleModelConfig is the configuration that held this product for five days
// in September 2026: every agent on one model, and nothing to fail over to.
const singleModelConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
  publishing: human
checks:
  - go test ./...
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
  reviewer:
    role: reviewer
    backend: claude-code
    model: opus
`

const publishingConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
  publishing: automatic
checks:
  - go test ./...
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    failover:
      enabled: true
      model: sonnet
`

const reportingConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
  publishing: human
checks:
  - go test ./...
slack:
  enabled: true
  channel: C1
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    failover:
      enabled: true
      model: sonnet
`

// servicesConfig is the reporting configuration with one service's entry
// written under `services`, which is how each service state is arranged: the
// entry is the whole of what the test is about, and reporting is on so that a
// Slack service is a declaration the configuration accepts.
func servicesConfig(entry string) string {
	return reportingConfig + "services:\n  " + entry
}

// The incident this finding exists for: the effort lines reached the main
// checkout while the dashboard ran a build from two days before them, and
// every read it made of the configuration failed. The doctor names the part,
// its build, and the key, and says a scheduler in the same state stops work.
func TestDoctorNamesTheRunningPartWhoseBuildCannotReadTheConfiguration(t *testing.T) {
	t.Parallel()

	t.Run("a merged key the dashboard cannot read", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.configuration = strings.Replace(healthyConfig, "    model: opus\n", "    <<: {model: opus, effort: medium}\n", 1)
		world.runningPart("dashboard", 4242, staleBuild, schemaWithout("agents.*.effort"))
		report := world.diagnose()
		finding, found := findingFor(report, "config-readers:dashboard")
		if !found || finding.Status != StatusWarning {
			t.Fatalf("no warning for the dashboard's merged key: %s", render(report))
		}
		for _, want := range []string{"the dashboard service", "build " + staleBuild[:12], "agents.developer.effort"} {
			if !strings.Contains(finding.Summary, want) {
				t.Errorf("summary %q does not name %q", finding.Summary, want)
			}
		}
	})

	t.Run("a dashboard on an older build", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.configuration = effortConfig
		world.runningPart("dashboard", 4242, staleBuild, schemaWithout("agents.*.effort"))
		// A second live dashboard must not conceal the older first instance.
		world.runningPart("dashboard", 4243, currentBuild, config.SchemaKeys())
		// The scheduler runs a build that reads the key, and a sink that is on the
		// older build has exited, so neither is named.
		world.runningPart("scheduler", 4343, currentBuild, config.SchemaKeys())
		world.runningPart("slack", 4444, staleBuild, schemaWithout("agents.*.effort"))
		world.alive[4444] = false

		report := world.diagnose()
		finding, found := findingFor(report, "config-readers:dashboard")
		if !found {
			t.Fatalf("no config-readers:dashboard finding: %s", render(report))
		}
		if finding.Status != StatusWarning {
			t.Fatalf("config-readers:dashboard = %s, want a warning", finding.Status)
		}
		for _, want := range []string{"the dashboard service", "build " + staleBuild[:12], "pid 4242", "agents.developer.effort"} {
			if !strings.Contains(finding.Summary, want) {
				t.Errorf("summary %q does not name %q", finding.Summary, want)
			}
		}
		if finding.Remedy != "kill 4242 && yoyo dashboard" {
			t.Errorf("remedy = %q, want the dashboard restarted", finding.Remedy)
		}
		for _, check := range []string{"config-readers:scheduler", "config-readers:slack"} {
			if other, named := findingFor(report, check); named {
				t.Errorf("%s = %q, want nothing said of a part that reads the key or is not running", check, other.Summary)
			}
		}
	})
	t.Run("a scheduler on an older build stops work", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.configuration = effortConfig
		world.runningPart("scheduler", 4343, staleBuild, schemaWithout("agents.*.effort"))
		report := world.diagnose()
		finding, _ := findingFor(report, "config-readers:scheduler")
		if finding.Status != StatusProblem || finding.Remedy != "yoyo stop && yoyo start" {
			t.Fatalf("config-readers:scheduler = %s with remedy %q, want a problem restarting the product", finding.Status, finding.Remedy)
		}
	})
	t.Run("every running part reads the file", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.configuration = effortConfig
		world.runningPart("dashboard", 4242, currentBuild, config.SchemaKeys())
		report := world.diagnose()
		finding, _ := findingFor(report, "config-readers")
		if finding.Status != StatusOK || !strings.Contains(finding.Detail, "dashboard on build "+currentBuild[:12]) {
			t.Fatalf("config-readers = %s %q (%q), want ok naming the dashboard's build", finding.Status, finding.Summary, finding.Detail)
		}
	})
}

func TestDoctorReportsAStaleSupervisorWithoutChangingItsRecords(t *testing.T) {
	w := newWorld(t)
	store, err := runstate.NewConfigReaderStore(w.stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	reader := runstate.ConfigReader{Service: runstate.ConfigReaderSupervisor, PID: 4242, ConfigPath: filepath.Join(w.stateRoot, "gone.yaml"), StartedAt: time.Now(), Keys: config.SchemaKeys()}
	if err := store.Record(reader); err != nil {
		t.Fatal(err)
	}
	w.alive[reader.PID] = true
	directory := filepath.Join(w.stateRoot, "products", "yoyodyne", "config-readers")
	path := filepath.Join(directory, reader.InstanceID()+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(directory, ".records.lock")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	report := w.diagnose()
	found := false
	for _, finding := range report.Findings {
		if finding.Check == configReadersCheck && strings.Contains(finding.Detail, "stale configuration reader record") {
			found = true
			for _, want := range []string{path, "diagnostic comparison left the record unchanged", "next landing configuration comparison"} {
				if !strings.Contains(finding.Detail, want) {
					t.Errorf("doctor diagnostic lacks %q: %s", want, finding.Detail)
				}
			}
		}
	}
	if !found {
		t.Fatal("doctor did not report stale record")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("doctor changed record: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("doctor created lock: %v", err)
	}
}
