// Package doctor answers one question about an installation of Yoyodyne: can
// work actually run here, and if not, what exactly would put it right.
//
// The discipline of the package is that the second half is never optional.
// Every finding that is not already healthy carries a remedy, and a remedy is a
// command somebody can paste into a shell rather than a description of a
// direction to head in. A state this package can describe but not route out of
// is a defect here rather than a fact about the state: an operator told what is
// wrong and not what to do about it is barely better off than one told nothing,
// and an agent reading the same findings is no better off at all.
//
// It is read-only about everything except its own diagnosis. Nothing here
// installs, authenticates, restarts, edits a configuration, or touches a lease
// it did not create -- what it does with the sink's lease is take it and drop it
// again, which is how it finds out whether anybody else has it. The one thing it
// creates is the state root, which every other command creates too and which
// existing is the whole of what "created" means for it.
//
// Nothing here reads a credential. Whether a secret is present is asked of the
// store that holds it in the form that answers without producing the value, and
// what a running sink says about its own tokens is a namespace name and a
// workspace id -- addressing, not secrets. A diagnostic that helpfully printed a
// token would put it in a terminal, a scrollback, and whatever collects them.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/adapters"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// SchemaVersion versions the machine-readable report. It is its own number
// because the report is consumed by things that are not this executable -- the
// setup skill reads it to decide what to repair -- and those have to be able to
// tell a report they understand from one they do not.
const SchemaVersion = 1

// Status is what one check found. Three values rather than two, because "this
// will not work" and "this works and something about it is worth knowing" ask
// different things of an operator and a report that ran them together would be
// one nobody reads twice.
//
// The line between the last two is one question and not a judgement of how much
// something matters: would this stop work running. An unauthenticated provider
// refuses every invocation, so it is a problem. A sink nobody started reports
// nothing while every run proceeds exactly as it would have, so it is a warning
// however badly the operator wants to know — reporting is an observation and
// never a gate, and a status that disagreed with that would put `yoyo doctor`'s
// exit code at odds with the rest of the harness.
type Status string

const (
	StatusOK      Status = "ok"
	StatusWarning Status = "warning"
	StatusProblem Status = "problem"
)

// worseThan orders the statuses, which is how a report takes the worst of its
// findings as its own.
func (s Status) worseThan(other Status) bool { return rank(s) > rank(other) }

func rank(s Status) int {
	switch s {
	case StatusProblem:
		return 2
	case StatusWarning:
		return 1
	default:
		return 0
	}
}

// Finding is one check and what became of it.
type Finding struct {
	// Check names what was looked at, stably, so a caller can key on it. The
	// names are fixed strings rather than prose, and the ones that are about a
	// particular thing carry it after a colon -- `provider:claude-code`.
	Check   string `json:"check"`
	Status  Status `json:"status"`
	Summary string `json:"summary"`
	// Detail carries what was actually observed: the error a tool returned, the
	// two versions that disagreed, the path that was not there. It is separate
	// from the summary so a listing stays readable and nothing is lost.
	Detail string `json:"detail,omitempty"`
	// Remedy is a command. It is present on every finding that is not ok, which
	// is the promise this package makes and which its tests hold it to. A remedy
	// with a placeholder in it is still a command: the operator fills the
	// placeholder in, and what they run afterwards is the thing that fixes it.
	Remedy string `json:"remedy,omitempty"`
}

// Report is the whole diagnosis.
type Report struct {
	SchemaVersion int `json:"schema_version"`
	// Product and Config are what was being diagnosed, and are empty when no
	// configuration was found -- which is itself the first finding.
	Product string `json:"product,omitempty"`
	Config  string `json:"config,omitempty"`
	// Status is the worst of the findings, so a caller that wants one answer has
	// one without re-deriving it.
	Status   Status    `json:"status"`
	Findings []Finding `json:"findings"`
}

// Healthy reports whether anything would stop work running here. A warning does
// not: it is something worth knowing about an installation that works.
func (r Report) Healthy() bool { return r.Status != StatusProblem }

// Counts summarizes the findings by status, which is what a one-line verdict is
// rendered from.
func (r Report) Counts() (ok, warnings, problems int) {
	for _, finding := range r.Findings {
		switch finding.Status {
		case StatusProblem:
			problems++
		case StatusWarning:
			warnings++
		default:
			ok++
		}
	}
	return ok, warnings, problems
}

// Environment is everything a diagnosis reads the machine through. Every field
// is a seam rather than a direct call so that the whole matrix of broken
// installations is reachable from a test: the states worth being complete over
// are exactly the ones that are inconvenient to arrange for real.
type Environment struct {
	// Runner runs the tools being asked about -- git, bd, claude, gh, security.
	Runner execution.ProcessRunner
	// LookPath resolves a program the way a shell would.
	LookPath func(string) (string, error)
	// Getenv reads the environment, which is where the state root and the
	// operator's own overrides come from.
	Getenv func(string) string
	// UserHomeDir is the other half of resolving the state root and the
	// namespaced environment file.
	UserHomeDir func() (string, error)
	// GOOS is the platform, which decides how a secret store is asked and which
	// install command a remedy names.
	GOOS string
	// Version is the build making the diagnosis. It is compared against the
	// build on PATH and against the build a running sink recorded, which is the
	// pair of comparisons a stale installation hides in.
	Version string
	// Build is the repository revision that build was made from, which is the
	// comparison the version alone cannot make. A harness developing itself runs
	// unreleased binaries, so every one of them answers "dev": a sink started a
	// week ago and the binary asking about it report the same version and compare
	// as identical, which is the false all-clear this is here to end.
	//
	// It is empty where the binary carries no revision of its own, and an absence
	// on either side is a comparison nobody can make rather than agreement.
	Build string
	// Load resolves the project's configuration, and returns the same error the
	// operator would see from `yoyo config validate`.
	Load func() (config.Resolved, error)
	// Locate finds the configuration Load would read, without reading it. It is
	// asked only when Load fails, and is what tells a project with no
	// configuration from one whose configuration is there and does not load:
	// `yoyo init` fixes the first and refuses the second, and `--force` would fix
	// the second by deleting the edit that broke it. Nil leaves the two
	// indistinguishable, and both are answered as the first.
	Locate func() (string, error)
	// Timeout bounds each tool invocation. Nothing here is long-running by
	// design, so a tool that hangs is itself the finding.
	Timeout time.Duration
	// Now is when the diagnosis is being made, used only for reading a recorded
	// time back as an age.
	Now func() time.Time
	// ProcessRunning answers whether a process a record names is still there,
	// which is how a running part of the product is told from one that exited.
	// Nil asks the operating system.
	ProcessRunning func(pid int) (bool, error)
}

// defaultTimeout is what each probe gets when nothing says. It is generous for
// commands that answer immediately because the ones that do not are the
// interesting case: `bd` starts a database engine on its first invocation in a
// project, and `gh auth status` reaches the network.
const defaultTimeout = 30 * time.Second

// Diagnose runs every check and returns what it found, in a fixed order: the
// tools, then the project, then the things the project turns on. The order is
// the order somebody would fix them in, because the first problem in the list is
// usually why the ones under it are problems too.
func Diagnose(ctx context.Context, env Environment) Report {
	diagnosis := &diagnosis{env: env}
	if diagnosis.env.Timeout <= 0 {
		diagnosis.env.Timeout = defaultTimeout
	}
	if diagnosis.env.Now == nil {
		diagnosis.env.Now = time.Now
	}

	report := Report{SchemaVersion: SchemaVersion}

	// The executable itself, before anything it would run: an operator whose
	// `yoyo` is not the one they think it is has been reading the wrong answers
	// from every other command too.
	installed, findings := diagnosis.checkInstallation(ctx)
	report.Findings = append(report.Findings, findings...)
	report.Findings = append(report.Findings, diagnosis.checkGit(ctx))

	resolved, err := diagnosis.load()
	if err != nil {
		report.Findings = append(report.Findings, diagnosis.unloadedConfiguration(err))
		// Everything below reads the configuration, so there is nothing further
		// to say that would not be invented. The tracker is still asked about,
		// because `yoyo init` in a project whose tracker is not there yet is the
		// next thing to fail and saying so now saves a round trip.
		report.Findings = append(report.Findings, diagnosis.checkTracker(ctx, ""))
		report.Status = worst(report.Findings)
		return report
	}
	report.Config = resolved.Path
	report.Product = string(resolved.Config.Product.ID)
	report.Findings = append(report.Findings, Finding{
		Check:   "configuration",
		Status:  StatusOK,
		Summary: fmt.Sprintf("the configuration for %s is valid", resolved.Config.Product.ID),
		Detail:  resolved.Path,
	})

	project := config.ProjectDirectory(resolved.Path)
	repository := RepositoryPath(project, resolved.Config.Product.Repository)
	report.Findings = append(report.Findings, diagnosis.checkRepository(ctx, repository))
	report.Findings = append(report.Findings, diagnosis.checkConfigurationIgnored(ctx, repository, resolved.Path))
	report.Findings = append(report.Findings, diagnosis.checkTracker(ctx, repository))
	report.Findings = append(report.Findings, diagnosis.checkStateRoot(repository))
	report.Findings = append(report.Findings, diagnosis.checkChecks(resolved, repository)...)
	report.Findings = append(report.Findings, diagnosis.checkNode(repository)...)
	report.Findings = append(report.Findings, diagnosis.checkArtifactHomes(project, repository, resolved))
	report.Findings = append(report.Findings, diagnosis.checkProviders(ctx, resolved)...)
	report.Findings = append(report.Findings, diagnosis.checkProviderAuthentication(resolved))
	report.Findings = append(report.Findings, diagnosis.checkFailover(resolved))
	report.Findings = append(report.Findings, diagnosis.checkForge(ctx, resolved, repository))
	report.Findings = append(report.Findings, diagnosis.checkSlack(ctx, resolved, installed)...)
	report.Findings = append(report.Findings, diagnosis.checkServices(ctx, resolved)...)
	report.Findings = append(report.Findings, diagnosis.checkConfigReaders(resolved)...)
	report.Findings = append(report.Findings, diagnosis.checkMaintenanceJob(ctx)...)

	report.Status = worst(report.Findings)
	return report
}

func worst(findings []Finding) Status {
	status := StatusOK
	for _, finding := range findings {
		if finding.Status.worseThan(status) {
			status = finding.Status
		}
	}
	return status
}

// diagnosis is one run of the checks. It exists so the probes can share the
// environment without threading it through every signature.
type diagnosis struct {
	env Environment
}

func (d *diagnosis) load() (config.Resolved, error) {
	if d.env.Load == nil {
		return config.Resolved{}, errors.New("no configuration loader was wired")
	}
	return d.env.Load()
}

// unloadedConfiguration is the finding for a configuration Load could not
// produce, and its remedy depends on why.
//
// No configuration at all is `yoyo init`. A configuration that is there and
// does not load is somebody's edit, and `yoyo init` refuses to write over it
// while `yoyo init --force` would delete the edit to fix a typo in it, so the
// remedy is the editor on that file -- which is what `yoyo setup` offers for
// the same state. YOYODYNE_CONFIG naming nothing readable is neither: `yoyo
// init` would write a configuration the variable still hides, so the remedy is
// to stop naming the wrong one.
func (d *diagnosis) unloadedConfiguration(loadErr error) Finding {
	missing := Finding{
		Check:   "configuration",
		Status:  StatusProblem,
		Summary: "this directory has no usable Yoyodyne configuration",
		Detail:  loadErr.Error(),
		Remedy:  "yoyo init",
	}
	if d.env.Locate == nil {
		return missing
	}
	path, err := d.env.Locate()
	if err != nil {
		if notFound := (config.NotFoundError{}); errors.As(err, &notFound) {
			return missing
		}
		if strings.TrimSpace(d.getenv(config.EnvironmentVariable)) != "" {
			return Finding{
				Check:   "configuration",
				Status:  StatusProblem,
				Summary: fmt.Sprintf("%s names no configuration that can be read", config.EnvironmentVariable),
				Detail:  err.Error(),
				Remedy:  "unset " + config.EnvironmentVariable,
			}
		}
		return missing
	}
	if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
		return missing
	}
	return Finding{
		Check:   "configuration",
		Status:  StatusProblem,
		Summary: "the configuration is there and does not load, and yoyo init will not write over it",
		Detail:  fmt.Sprintf("%s: %v", path, loadErr),
		Remedy:  fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(path)),
	}
}

func (d *diagnosis) lookPath(program string) (string, error) {
	if d.env.LookPath == nil {
		return exec.LookPath(program)
	}
	return d.env.LookPath(program)
}

func (d *diagnosis) getenv(name string) string {
	if d.env.Getenv == nil {
		return os.Getenv(name)
	}
	return d.env.Getenv(name)
}

func (d *diagnosis) homeDir() (string, error) {
	if d.env.UserHomeDir == nil {
		return os.UserHomeDir()
	}
	return d.env.UserHomeDir()
}

// run invokes one tool and reports what it said. A tool that could not be
// started at all is distinguished from one that ran and refused, because those
// are two different remedies.
func (d *diagnosis) run(ctx context.Context, directory, name string, args ...string) (execution.ProcessResult, error) {
	if d.env.Runner == nil {
		return execution.ProcessResult{}, errors.New("no process runner was wired")
	}
	return d.env.Runner.Run(ctx, execution.Command{
		Name:    name,
		Args:    args,
		Dir:     directory,
		Timeout: d.env.Timeout,
	}, nil)
}

// checkInstallation asks whether `yoyo` is on PATH and whether the one there is
// the build answering this question. It returns the version PATH would give,
// because that is the build a remedy which restarts something would produce.
func (d *diagnosis) checkInstallation(ctx context.Context) (string, []Finding) {
	path, err := d.lookPath("yoyo")
	if err != nil {
		return d.env.Version, []Finding{{
			Check:   "path",
			Status:  StatusProblem,
			Summary: "`yoyo` is not on PATH, so nothing that shells out to it will find it",
			Detail:  err.Error(),
			Remedy:  `export PATH="$PATH:$(go env GOPATH)/bin"`,
		}}
	}
	findings := []Finding{{
		Check:   "path",
		Status:  StatusOK,
		Summary: "`yoyo` is on PATH",
		Detail:  path,
	}}

	// `yoyo version` prints the bare version and nothing else, which is pinned by
	// a test in the cli package because two other things compare those bytes
	// literally. This depends on the same property, and internal/cli holds a test
	// that runs that command's real output through this comparison: a warning on
	// every healthy installation is the kind of finding operators learn to skip
	// past, which would cost this command more than the drift it catches.
	result, err := d.run(ctx, "", path, "version")
	if err != nil || result.Status != execution.ProcessSucceeded {
		findings = append(findings, Finding{
			Check:   "binary",
			Status:  StatusProblem,
			Summary: "the `yoyo` on PATH could not say which build it is",
			Detail:  describeFailure(result, err),
			Remedy:  "go install github.com/mason-bryant/yoyodyne/cmd/yoyo@latest",
		})
		return d.env.Version, findings
	}
	installed := firstLine(result.Stdout)
	if installed != d.env.Version {
		// This is not pedantry about a version string. Every other answer in this
		// report is about the machine as the running build understands it, while
		// everything an operator types afterwards runs the build on PATH, and the
		// two diverging is how an installation drifts without anybody deciding to.
		findings = append(findings, Finding{
			Check:   "binary",
			Status:  StatusWarning,
			Summary: "the `yoyo` on PATH is a different build from the one running this check",
			Detail:  fmt.Sprintf("on PATH: %s; running: %s", installed, d.env.Version),
			Remedy:  "go install github.com/mason-bryant/yoyodyne/cmd/yoyo@latest",
		})
		return installed, findings
	}
	findings = append(findings, Finding{
		Check:   "binary",
		Status:  StatusOK,
		Summary: fmt.Sprintf("the `yoyo` on PATH is this build, %s", installed),
	})
	return installed, findings
}

func (d *diagnosis) checkGit(ctx context.Context) Finding {
	if _, err := d.lookPath("git"); err != nil {
		return Finding{
			Check:   "git",
			Status:  StatusProblem,
			Summary: "git is not installed, and every run works in a Git worktree",
			Detail:  err.Error(),
			Remedy:  d.installCommand("git"),
		}
	}
	result, err := d.run(ctx, "", "git", "--version")
	if err != nil || result.Status != execution.ProcessSucceeded {
		return Finding{
			Check:   "git",
			Status:  StatusProblem,
			Summary: "git is on PATH but would not run",
			Detail:  describeFailure(result, err),
			Remedy:  d.installCommand("git"),
		}
	}
	return Finding{Check: "git", Status: StatusOK, Summary: firstLine(result.Stdout)}
}

// checkRepository asks the two things a project has to be before a run can
// branch from it: a repository, with something to branch from.
func (d *diagnosis) checkRepository(ctx context.Context, repository string) Finding {
	result, err := d.run(ctx, repository, "git", "rev-parse", "--git-dir")
	if err != nil || result.Status != execution.ProcessSucceeded {
		return Finding{
			Check:   "repository",
			Status:  StatusProblem,
			Summary: fmt.Sprintf("%s is not a Git repository", repository),
			Detail:  describeFailure(result, err),
			Remedy:  fmt.Sprintf("git -C %s init", shellQuote(repository)),
		}
	}
	head, err := d.run(ctx, repository, "git", "rev-parse", "HEAD")
	if err != nil || head.Status != execution.ProcessSucceeded {
		return Finding{
			Check:  "repository",
			Status: StatusProblem,
			// A run branches from the target branch, and a repository with no
			// commits has no branch to branch from -- which the harness discovers
			// when it makes the worktree rather than when it claims the item.
			Summary: fmt.Sprintf("%s has no commits, so there is nothing for a run to branch from", repository),
			Detail:  describeFailure(head, err),
			Remedy:  fmt.Sprintf("git -C %s commit --allow-empty -m 'initial commit'", shellQuote(repository)),
		}
	}
	return Finding{
		Check:   "repository",
		Status:  StatusOK,
		Summary: fmt.Sprintf("the repository at %s has commits to branch from", repository),
	}
}

// checkTracker asks whether bd is there and whether it answers in this project.
// Those are two failures with two remedies: an uninstalled tracker and an
// uninitialized one look identical from a channel that has gone quiet.
func (d *diagnosis) checkTracker(ctx context.Context, repository string) Finding {
	if _, err := d.lookPath("bd"); err != nil {
		return Finding{
			Check:   "tracker",
			Status:  StatusProblem,
			Summary: "bd is not installed, and every role reads and writes the tracker",
			Detail:  err.Error(),
			Remedy:  beads.InstallCommand,
		}
	}
	// `bd stats` opens the project's database and reports on it, which is the
	// cheapest question that proves the tracker is actually usable here rather
	// than merely installed.
	result, err := d.run(ctx, repository, "bd", "stats")
	if err != nil || result.Status != execution.ProcessSucceeded {
		return Finding{
			Check:   "tracker",
			Status:  StatusProblem,
			Summary: "bd is installed but could not read this project's issues",
			Detail:  describeFailure(result, err),
			Remedy:  fmt.Sprintf("bd init%s", inDirectory(repository)),
		}
	}
	return Finding{Check: "tracker", Status: StatusOK, Summary: "bd answers in this project"}
}

// checkStateRoot asks whether the harness can keep its durable records, where
// they are, which layer put them there, and whether the checkout's state-root
// marker agrees. The marker is read first and only read — recording one is for
// the processes that open the root — so a diagnosis never settles which root a
// product uses. Where the marker agrees, or there is none yet, the root is
// created if it is missing, which is what every other command does with it and
// what makes "missing" not a failure. Where it disagrees nothing is created: the
// directory this shell resolved is not where the product's state is, and an
// empty one left there would be a second root nobody meant.
func (d *diagnosis) checkStateRoot(repository string) Finding {
	resolved, err := runstate.ResolveRoot(d.getenv, d.homeDir, d.env.GOOS)
	if err != nil {
		return Finding{
			Check:   "state",
			Status:  StatusProblem,
			Summary: "the state root could not be resolved, so nothing can be recorded",
			Detail:  err.Error(),
			Remedy:  "export YOYODYNE_STATE_HOME=$HOME/.local/state/yoyodyne",
		}
	}
	root := resolved.Path
	marker, markerErr := runstate.ReadRootMarker(repository)
	if markerErr == nil && !marker.Agrees(root) && marker.RootGone() {
		// A marker naming a root that is gone splits nothing, so what it costs
		// is a refusal of every command over a record nobody needs: the remedy
		// is the one command that replaces it.
		return Finding{
			Check:  "state",
			Status: StatusProblem,
			Summary: fmt.Sprintf("this checkout's state-root marker names %s, which no longer exists, so every command from here refuses to start",
				marker.Recorded),
			Detail: fmt.Sprintf("recorded in %s %s; this shell resolves %s, from %s, and nothing is split because there is no state at the recorded root",
				marker.Path, marker.WrittenBy(), root, resolved.Origin),
			Remedy: runstate.RebindCommand,
		}
	}
	if markerErr == nil && !marker.Agrees(root) {
		return Finding{
			Check:  "state",
			Status: StatusProblem,
			Summary: fmt.Sprintf("this shell resolves the state root %s, from %s, and this checkout's state is kept at %s, so every command from here refuses to start",
				root, resolved.Origin, marker.Recorded),
			Detail: fmt.Sprintf("recorded in %s %s; one product's state is never split across two roots", marker.Path, marker.WrittenBy()),
			Remedy: fmt.Sprintf("unset %s, or correct state_root in the machine configuration, so the root resolves to %s; to move the state instead: yoyo stop, move the directory, change the setting, then %s",
				runstate.StateHomeVariable, shellQuote(marker.Recorded), runstate.RebindCommand),
		}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Finding{
			Check:   "state",
			Status:  StatusProblem,
			Summary: "the state root could not be created, so nothing can be recorded",
			Detail:  err.Error(),
			Remedy:  fmt.Sprintf("mkdir -p %s && chmod 700 %s", shellQuote(root), shellQuote(root)),
		}
	}
	summary := fmt.Sprintf("the durable records live in %s, from %s", root, resolved.Origin)
	switch {
	case markerErr != nil:
		return Finding{
			Check:   "state",
			Status:  StatusWarning,
			Summary: summary + ", and whether this checkout's state-root marker agrees could not be read",
			Detail:  markerErr.Error(),
		}
	case marker.Path == "":
		return Finding{Check: "state", Status: StatusOK, Summary: summary + "; the repository is not a Git checkout, so no marker is kept"}
	case marker.Recorded == "":
		return Finding{Check: "state", Status: StatusOK, Summary: summary + "; no process has recorded a root in " + marker.Path + " yet"}
	default:
		return Finding{Check: "state", Status: StatusOK, Summary: summary + "; the marker in " + marker.Path + " agrees"}
	}
}

// stateRootPath is the root the other checks look for what they need under,
// resolved the one way every process resolves it. Those checks only read, so
// they take the path without the marker, which the state check reports on.
func (d *diagnosis) stateRootPath() (string, error) {
	resolved, err := runstate.ResolveRoot(d.getenv, d.homeDir, d.env.GOOS)
	return resolved.Path, err
}

// checkChecks asks whether the project has deterministic checks and whether this
// machine could run them. A configuration that names a command nothing here can
// resolve fails every run at the same point, after the model has been paid for.
func (d *diagnosis) checkChecks(resolved config.Resolved, repository string) []Finding {
	if len(resolved.Config.Checks) == 0 {
		return []Finding{{
			Check:   "checks",
			Status:  StatusProblem,
			Summary: "no deterministic checks are configured, so nothing verifies a change before it is reviewed",
			Detail:  "checks is empty in " + resolved.Path,
			Remedy:  fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(resolved.Path)),
		}}
	}
	var missing []string
	for _, command := range resolved.Config.Checks {
		program := programOf(command)
		if program == "" || shellBuiltin(program) {
			continue
		}
		if strings.ContainsRune(program, filepath.Separator) {
			// A check runs in the run's worktree rather than wherever this was
			// typed, so a relative script is resolved against the repository. The
			// alternative reports a perfectly good check as missing whenever
			// somebody runs this from a subdirectory.
			resolvedProgram := program
			if !filepath.IsAbs(resolvedProgram) {
				resolvedProgram = filepath.Join(repository, resolvedProgram)
			}
			if info, err := os.Stat(resolvedProgram); err != nil || info.IsDir() {
				missing = append(missing, program)
			}
			continue
		}
		if _, err := d.lookPath(program); err != nil {
			missing = append(missing, program)
		}
	}
	if len(missing) > 0 {
		return []Finding{{
			Check:   "checks",
			Status:  StatusProblem,
			Summary: fmt.Sprintf("the configured checks need %s, which this machine cannot run", strings.Join(missing, ", ")),
			Detail:  fmt.Sprintf("checks in %s: %s", resolved.Path, strings.Join(resolved.Config.Checks, "; ")),
			// The remedy is the configuration rather than an install command
			// because nothing here knows how this machine installs an arbitrary
			// program, and a check this machine cannot run is as validly fixed by
			// changing the check as by installing the tool.
			Remedy: fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(resolved.Path)),
		}}
	}
	return []Finding{{
		Check:   "checks",
		Status:  StatusOK,
		Summary: fmt.Sprintf("%s configured, and every command resolves here", countOf(len(resolved.Config.Checks), "check")),
		Detail:  strings.Join(resolved.Config.Checks, "; "),
	}}
}

// checkNode asks whether this machine can run the dashboard page's script,
// which is Node and nothing else. The page is drawn by a script a Go test cannot
// run, so its only behavioural evidence is the render comparison that script
// feeds, and a machine without Node produces none of it.
//
// It is asked only of a product that ships the dashboard, which is what carrying
// the render script means: Node is this product's development dependency rather
// than a tool every project wants reported. A repository without the script is
// told nothing, rather than told its Node is fine or that it is missing one it
// has no use for.
//
// The three answers are the three the render test itself gives, deliberately, so
// that what the doctor says and what a run's checks do are one statement: Node
// present is fine; Node absent in an environment that declares its own absence
// is worth knowing and stops nothing; Node absent with nothing declaring it is a
// problem, because the check that would have caught it is the check that now
// fails.
func (d *diagnosis) checkNode(repository string) []Finding {
	const check = "node"
	script := filepath.Join(repository, filepath.FromSlash(dashboard.RenderScript))
	if info, err := os.Stat(script); err != nil || info.IsDir() {
		return nil
	}
	if path, err := d.lookPath(dashboard.NodeProgram); err == nil {
		return []Finding{{
			Check:   check,
			Status:  StatusOK,
			Summary: "node is installed, so the dashboard page's renders are compared rather than skipped",
			Detail:  path,
		}}
	}
	if declared := strings.TrimSpace(d.getenv(dashboard.NodeUnavailableVariable)); declared != "" {
		return []Finding{{
			Check:   check,
			Status:  StatusWarning,
			Summary: fmt.Sprintf("node is not installed and %s declares this environment deliberately has none, so the dashboard page's renders are not compared here", dashboard.NodeUnavailableVariable),
			Detail:  fmt.Sprintf("%s=%s; %s needs it", dashboard.NodeUnavailableVariable, declared, dashboard.RenderScript),
			Remedy:  d.installCommand(dashboard.NodeProgram),
		}}
	}
	return []Finding{{
		Check:   check,
		Status:  StatusProblem,
		Summary: "node is not installed, so the dashboard page's renders cannot be compared and its render test fails",
		Detail:  fmt.Sprintf("%s runs under node; %s names it as a development dependency", dashboard.RenderScript, dashboard.NodeDocumentation),
		Remedy:  d.installCommand(dashboard.NodeProgram),
	}}
}

// checkArtifactHomes asks whether every directory of governed documents says at
// its door what is filed there, whose it is, and whether the operator may edit
// one by hand. `yoyo init` writes those indexes; this is what notices a project
// configured before they existed, a home that moved, and one somebody deleted.
//
// It is a warning rather than a problem, and the line is this package's own: an
// undocumented directory stops no run, refuses no invocation, and leaves an
// installation that works. What it costs is paid by the next person to open the
// directory, which is exactly the cost a warning is for.
func (d *diagnosis) checkArtifactHomes(project, repository string, resolved config.Resolved) Finding {
	const check = "artifact-readmes"
	root, err := repowrite.NewRoot(repository)
	if err != nil {
		return Finding{
			Check:   check,
			Status:  StatusWarning,
			Summary: "the artifact homes could not be looked at, so nothing here says whether they are documented",
			Detail:  err.Error(),
			Remedy:  "yoyo setup" + inDirectory(project),
		}
	}
	statuses := artifacthome.Inspect(root, resolved.Config)
	if described := artifacthome.Describe(statuses); described != "" {
		summary := fmt.Sprintf("%s of %d without an index saying what is filed there, who owns it, and whether you may edit it by hand", countOf(unwritten(statuses), "artifact home"), len(statuses))
		return Finding{
			Check:   check,
			Status:  StatusWarning,
			Summary: summary,
			Detail:  described,
			Remedy:  "yoyo setup" + inDirectory(project),
		}
	}
	return Finding{
		Check:   check,
		Status:  StatusOK,
		Summary: fmt.Sprintf("every one of the %s says what is filed there, who owns it, and the hand-edit policy", countOf(len(statuses), "artifact home")),
		Detail:  strings.Join(artifacthome.Paths(statuses), "; "),
	}
}

func unwritten(statuses []artifacthome.Status) int {
	count := 0
	for _, status := range statuses {
		if !status.Written() {
			count++
		}
	}
	return count
}

// checkProviders asks, for each backend the project's agents actually name,
// whether it is installed and authenticated. Both are the provider's own to
// hold: the harness reports on them and manages neither.
func (d *diagnosis) checkProviders(ctx context.Context, resolved config.Resolved) []Finding {
	seen := map[domain.Backend]struct{}{}
	var backends []domain.Backend
	for _, agent := range resolved.Config.Agents {
		if _, already := seen[agent.Backend]; already {
			continue
		}
		seen[agent.Backend] = struct{}{}
		backends = append(backends, agent.Backend)
	}
	sort.Slice(backends, func(i, j int) bool { return backends[i] < backends[j] })

	// What this build can do with a backend is what the project's registry says
	// about it, so a provider the project declared is diagnosed as the adapter and
	// executable it actually runs on rather than as a name nothing recognizes.
	// A configuration whose providers will not resolve has already been reported
	// where it was loaded, and every backend then answers as one nothing runs.
	registry, _ := resolved.Config.ProviderRegistry()

	findings := make([]Finding, 0, len(backends))
	for _, named := range backends {
		descriptor, _ := registry.Lookup(named)
		findings = append(findings, d.checkProvider(ctx, named, descriptor, resolved.Path))
	}
	return append(findings, d.checkAccounts(ctx, resolved, registry)...)
}

// checkAccounts asks each configured account whether it is signed in.
//
// It asks only where a project pools, and that is safe for one reason and not
// the reason it looks like: a project with one account authenticates where the
// machine does whatever that account is called — Config.Endpoint says so, and
// this reads its answer rather than restating it — so the provider check above
// has already asked the only question there is. Reporting it a second time under
// a second name would be a diagnosis that looked longer without saying more.
//
// Under a pool each alias but the default has a provider home of its own, and a
// home nobody has signed in to is exactly the state this exists to catch: the
// harness would otherwise discover it as a run refused by a provider, after an
// item had been claimed and a worktree cut for it. Declaring a second account is
// what moves a lone `work` alias into a home of its own, which is why that move
// is diagnosed here the moment it happens. The remedy is the login for that
// home, which is the command `bin/yoyo-account` runs and the one the README
// states.
//
// Each account is asked about its own provider, which is the provider it names
// and — where it names none — the one the developer runs on if that provider's
// homes are the shape a pooled home is, and Claude Code otherwise. So a project
// that declared a fork of Claude Code is diagnosed with the binary its runs will
// really use, a project that declared nothing is diagnosed with `claude` exactly
// as before, and a pool holding a Codex account is asked by `codex` whether that
// account is signed in rather than by whichever provider the developer happens
// to be on.
func (d *diagnosis) checkAccounts(ctx context.Context, resolved config.Resolved, registry *backend.Registry) []Finding {
	if !resolved.Config.Pooled() {
		return nil
	}
	root, err := d.stateRootPath()
	if err != nil {
		// The state root has already been reported as unresolvable by its own
		// check, and every account's home is under it, so there is nothing further
		// to say that would not be the same finding a second time.
		return nil
	}
	aliases := resolved.Config.AccountAliases()
	findings := make([]Finding, 0, len(aliases))
	for _, alias := range aliases {
		check := "account:" + alias
		provider, descriptor := accountProvider(resolved.Config, registry, alias)
		// Where the account authenticates is asked of the configuration rather
		// than derived here, so a diagnosis can never probe one home while the
		// harness invokes in another — which is the one failure this check could
		// have that nothing downstream would catch. An alias taken from the
		// configuration's own listing always resolves; the refusal is carried
		// anyway, because the alternative is diagnosing a home nobody chose.
		endpoint, err := resolved.Config.Endpoint(root, alias)
		if err != nil {
			findings = append(findings, Finding{
				Check:   check,
				Status:  StatusProblem,
				Summary: fmt.Sprintf("account %q could not be resolved to a provider home", alias),
				Detail:  err.Error(),
				Remedy:  fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(resolved.Path)),
			})
			continue
		}
		findings = append(findings, d.checkAccount(ctx, resolved.Config, provider, descriptor, endpoint, resolved.Path))
	}
	return findings
}

// accountProvider is the provider one pooled account is diagnosed as, and the
// description this build holds of it.
//
// The account's own answer comes first, because a provider home holds one
// provider's authentication and the account is the only thing that knows whose:
// asking a Codex home whether `claude` is signed in there reports every account
// as broken, and asking a Claude Code home whether `codex` is reports every one
// of them as fine. An account that names nothing keeps the answer the diagnosis
// has always given — the developer's provider where it runs on the pooled home's
// own adapter, and the built-in Claude Code description otherwise.
func accountProvider(cfg config.Config, registry *backend.Registry, alias string) (domain.Backend, backend.Descriptor) {
	named := cfg.Accounts[alias].Provider
	if strings.TrimSpace(string(named)) == "" {
		descriptor := poolDescriptor(cfg, registry)
		return descriptor.ID, descriptor
	}
	descriptor, known := registry.Lookup(named)
	if !known {
		// A provider the project does not name is refused where the configuration
		// is read. Nothing here is in a position to say more about it, and the
		// descriptor's own emptiness is what the finding then reports.
		return named, backend.Descriptor{ID: named}
	}
	return named, descriptor
}

// poolDescriptor is the provider an account that names none is asked about: the
// one the developer agent names, because that is the invocation a rotated
// account serves. A project whose developer names a provider that does not run
// on the Claude Code adapter — or names none at all — falls back to the built-in
// Claude Code description.
//
// The gate is the Claude Code adapter rather than any adapter this build ships,
// and it is the same statement config.AccountProvider makes about an unstated
// account: a pooled home nobody named a provider for is a Claude Code home,
// because that is what `bin/yoyo-account` and the login this file hands back
// have made every one of them. A developer on another adapter whose accounts are
// really that provider's says so on the accounts, and is then diagnosed through
// that provider's own adapter.
func poolDescriptor(cfg config.Config, registry *backend.Registry) backend.Descriptor {
	builtIn, _ := registry.Lookup(domain.BackendClaudeCode)
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if cfg.Agents[name].Role != domain.RoleDeveloper {
			continue
		}
		descriptor, known := registry.Lookup(cfg.Agents[name].Backend)
		if known && descriptor.Adapter == domain.BackendClaudeCode {
			return descriptor
		}
		break
	}
	return builtIn
}

func (d *diagnosis) checkAccount(ctx context.Context, cfg config.Config, named domain.Backend, descriptor backend.Descriptor, endpoint config.AccountEndpoint, configPath string) Finding {
	alias := endpoint.Alias
	check := "account:" + alias
	membership := cfg.Accounts[alias].Membership()
	directory := endpoint.Directory
	login := AccountLoginCommand(descriptor.Adapter, directory, descriptor.Binary)
	binary := descriptor.Binary
	if binary == "" {
		binary = providerBinary(descriptor.Adapter)
	}
	// The account is asked through the adapter its own provider runs on, so what
	// this reports about a home is what an invocation made there would meet. An
	// account naming a provider nothing in this build can launch is said to be
	// exactly that rather than asked with somebody else's executable, which would
	// report every such account as unauthenticated.
	provider, built := adapters.For(descriptor, named, d.env.Runner, directory, d.lookPath)
	if !built {
		return Finding{
			Check:   check,
			Status:  StatusProblem,
			Summary: fmt.Sprintf("account %q is provider %q's, which this build has no adapter for, so nothing here can say whether it is signed in", alias, named),
			Detail:  accountHomeDetail(directory),
			Remedy:  fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(configPath)),
		}
	}
	availability, err := provider.CheckAvailability(ctx)
	switch {
	case err != nil:
		return Finding{
			Check:   check,
			Status:  StatusProblem,
			Summary: fmt.Sprintf("%s would not say whether account %q is authenticated", binary, alias),
			Detail:  err.Error(),
			Remedy:  login,
		}
	case !availability.Installed:
		// The provider check above has already said the executable is not there,
		// and saying it once per alias would bury that under the accounts it
		// stopped this from answering. What is added here is which account went
		// unasked.
		remedy := providerInstallCommand(descriptor.Adapter)
		if descriptor.Binary != "" {
			remedy = fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(configPath))
		}
		return Finding{
			Check:   check,
			Status:  StatusProblem,
			Summary: fmt.Sprintf("account %q could not be asked, because %s did not run", alias, binary),
			Detail:  strings.TrimSpace(accountHomeDetail(directory) + " " + availability.Missing),
			Remedy:  remedy,
		}
	case !availability.Authenticated:
		return Finding{
			Check:   check,
			Status:  StatusProblem,
			Summary: fmt.Sprintf("account %q is provider %q's, is in the %s pool, and is not authenticated, so every run the pool sends there would be refused", alias, named, membership),
			Detail:  accountHomeDetail(directory),
			Remedy:  login,
		}
	}
	return Finding{
		Check:   check,
		Status:  StatusOK,
		Summary: fmt.Sprintf("account %q is authenticated with provider %q and is in the %s pool", alias, named, membership),
		Detail:  strings.TrimSpace(accountHomeDetail(directory) + " " + availability.AuthMethod),
	}
}

// AccountLoginCommand is what signs one account in, with the provider whose home
// it is. The default alias authenticates where the machine already does, so its
// command is the provider's own; every other alias names the home it is signing
// in to, and creates it, because an alias that has never been signed in is
// exactly the case this is a remedy for and its home does not exist yet.
//
// Which variable names that home is the adapter's own, read from the adapter
// rather than spelled here: handing an operator `CLAUDE_CONFIG_DIR` for a Codex
// account would sign them in where the invocation will not look, which is the
// same mistake as pointing the invocation at the wrong home and costs a login to
// find out.
//
// It is exported because it is the answer to a question two surfaces ask: this
// diagnosis reports an account that is not authenticated, and a conversation
// refuses to open on one. An operator who met the same condition twice and was
// handed two different commands would have to work out which of them was the
// real one, so there is one command and both read it from here.
// An optional binary selects the configured executable, quoted as one shell
// argument; omitting it keeps the adapter's ordinary PATH command.
func AccountLoginCommand(adapter domain.Backend, directory string, binary ...string) string {
	login := providerLoginCommand(adapter)
	if len(binary) != 0 && strings.TrimSpace(binary[0]) != "" {
		switch adapter {
		case domain.BackendCodex:
			login = shellQuote(binary[0]) + " login"
		case domain.BackendClaudeCode:
			login = shellQuote(binary[0]) + " auth login"
		}
	}
	variable, named := adapters.HomeVariable(adapter)
	if strings.TrimSpace(directory) == "" || !named {
		return login
	}
	return fmt.Sprintf("mkdir -p %s && %s=%s %s",
		shellQuote(directory), variable, shellQuote(directory), login)
}

// accountHomeDetail says where an account authenticates, which is the one thing
// about it a reader cannot derive from the alias.
func accountHomeDetail(directory string) string {
	if strings.TrimSpace(directory) == "" {
		return "signed in where this machine is"
	}
	return directory
}

func (d *diagnosis) checkProvider(ctx context.Context, named domain.Backend, descriptor backend.Descriptor, configPath string) Finding {
	check := "provider:" + string(named)
	// A declared provider names the executable its adapter runs; a built-in uses
	// the one this build knows for it.
	binary := descriptor.Binary
	if binary == "" {
		binary = providerBinary(descriptor.Adapter)
	}
	if !descriptor.Runnable() || binary == "" {
		return Finding{
			Check:   check,
			Status:  StatusProblem,
			Summary: fmt.Sprintf("the agents name backend %q, which this build has no adapter for", named),
			// The remedy is the configuration for the reason the checks probe's
			// is: nothing can be installed that would make this build grow an
			// adapter, so what has to change is the backend the agents name — or,
			// for a provider the project declared, the adapter it says runs it.
			Remedy: fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(configPath)),
		}
	}
	// The provider is asked about its own authentication through the same adapter
	// that would run it, so a project that declared a provider is diagnosed by
	// whatever reads that provider's answers rather than by whatever this
	// function happens to know. A descriptor that named no adapter this build
	// ships was answered above, so nothing reaches the refusal below; it stands
	// for the day this build grows an adapter with no availability check of its
	// own, because reporting a provider nothing could ask as healthy is the one
	// answer that would be worse than saying so.
	provider, built := adapters.For(descriptor, named, d.env.Runner, "", d.lookPath)
	if !built {
		return Finding{
			Check:   check,
			Status:  StatusWarning,
			Summary: fmt.Sprintf("%s is installed, and this build has no adapter that can ask whether it is authenticated", binary),
			Detail:  "an unauthenticated provider would refuse every agent invocation, so this is worth confirming by hand",
			Remedy:  providerLoginCommand(descriptor.Adapter),
		}
	}
	availability, err := provider.CheckAvailability(ctx)
	if err != nil {
		return Finding{
			Check:   check,
			Status:  StatusProblem,
			Summary: fmt.Sprintf("%s would not say whether it is authenticated", binary),
			Detail:  err.Error(),
			Remedy:  AccountLoginCommand(descriptor.Adapter, "", descriptor.Binary),
		}
	}
	finding := providerFinding(check, binary, descriptor.Adapter, availability)
	if !availability.Installed {
		finding.Detail = availability.Missing
		if descriptor.Binary != "" {
			finding.Remedy = fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(configPath))
		}
	} else if !availability.Authenticated {
		finding.Remedy = AccountLoginCommand(descriptor.Adapter, "", descriptor.Binary)
	}
	return finding
}

func providerFinding(check, binary string, named domain.Backend, availability backend.Availability) Finding {
	switch {
	case !availability.Installed:
		return Finding{
			Check:   check,
			Status:  StatusProblem,
			Summary: fmt.Sprintf("%s cannot run in this environment", binary),
			Remedy:  providerInstallCommand(named),
		}
	case !availability.Authenticated:
		return Finding{
			Check:   check,
			Status:  StatusProblem,
			Summary: fmt.Sprintf("%s is installed but not authenticated, so every agent invocation would be refused", binary),
			Detail:  availability.Version,
			Remedy:  providerLoginCommand(named),
		}
	}
	return Finding{
		Check:   check,
		Status:  StatusOK,
		Summary: fmt.Sprintf("%s is installed and authenticated", binary),
		Detail:  strings.TrimSpace(availability.Version + " " + availability.AuthMethod),
	}
}

// checkForge asks about the forge only when the harness would actually use one.
// A project it never publishes for needs no `gh` and no remote, and reporting a
// missing one as a problem would be reporting a decision as a defect.
func (d *diagnosis) checkForge(ctx context.Context, resolved config.Resolved, repository string) Finding {
	if !HarnessPublishes(resolved.Config.Approvals.Publishing) {
		return Finding{
			Check:  "forge",
			Status: StatusOK,
			// Said as a fact about the harness rather than about the project,
			// because they are not the same claim: under `human` the operator may
			// well push and open pull requests themselves, and whether they keep a
			// forge CLI for that is theirs rather than something to diagnose.
			Summary: "the harness publishes nothing for this project, so it needs no forge access",
			Detail:  fmt.Sprintf("approvals.publishing is %s", resolved.Config.Approvals.Publishing),
		}
	}
	remote := resolved.Config.Execution.Remote
	if result, err := d.run(ctx, repository, "git", "remote", "get-url", remote); err != nil || result.Status != execution.ProcessSucceeded {
		return Finding{
			Check:   "forge",
			Status:  StatusProblem,
			Summary: fmt.Sprintf("this project publishes to the Git remote %q, which this repository does not have", remote),
			Detail:  describeFailure(result, err),
			Remedy:  fmt.Sprintf("git -C %s remote add %s <url>", shellQuote(repository), remote),
		}
	}
	// A project publishing from a fork needs the fork too, and the run that finds
	// it missing is a run that has already been claimed and worked. Asking here is
	// what turns that into a sentence somebody reads before starting.
	pushRemote := resolved.Config.Execution.PushRemote
	if pushRemote != "" && pushRemote != remote {
		if result, err := d.run(ctx, repository, "git", "remote", "get-url", pushRemote); err != nil || result.Status != execution.ProcessSucceeded {
			return Finding{
				Check:   "forge",
				Status:  StatusProblem,
				Summary: fmt.Sprintf("this project pushes run branches to the Git remote %q, which this repository does not have", pushRemote),
				Detail:  describeFailure(result, err),
				Remedy:  fmt.Sprintf("git -C %s remote add %s <url-of-your-fork>", shellQuote(repository), pushRemote),
			}
		}
	}
	availability, err := (publish.GitHub{Runner: d.env.Runner, Dir: repository, Remote: remote, PushRemote: pushRemote}).Availability(ctx)
	if err != nil {
		return Finding{
			Check:   "forge",
			Status:  StatusProblem,
			Summary: "gh would not say whether it is authenticated",
			Detail:  err.Error(),
			Remedy:  "gh auth login",
		}
	}
	switch {
	case !availability.Installed:
		return Finding{
			Check:   "forge",
			Status:  StatusProblem,
			Summary: "this project publishes, and gh is not installed",
			Remedy:  d.installCommand("gh"),
		}
	case !availability.Authenticated:
		return Finding{
			Check:   "forge",
			Status:  StatusProblem,
			Summary: "gh is installed but not authenticated, so no pull request can be opened",
			Detail:  availability.Version,
			Remedy:  "gh auth login",
		}
	}
	if pushRemote != "" && pushRemote != remote {
		return Finding{
			Check:   "forge",
			Status:  StatusOK,
			Summary: fmt.Sprintf("gh is authenticated, run branches go to %s, and pull requests are opened against %s", pushRemote, remote),
			Detail:  availability.Version,
		}
	}
	return Finding{
		Check:   "forge",
		Status:  StatusOK,
		Summary: fmt.Sprintf("gh is authenticated and %s is where this project publishes", remote),
		Detail:  availability.Version,
	}
}

// HarnessPublishes decides whether a run would ever reach the forge, and it has
// to agree with orchestrator.Pipeline.publishes(), which is the predicate that
// actually decides it. It is exported for one reason: so the pipeline's own test
// can hold the two together, because a diagnosis gated differently from the
// behavior it diagnoses reports a healthy installation as broken or a broken one
// as healthy, and neither failure would show up in this package's tests.
//
// `human` is the off switch here rather than an approval gate, which is the one
// thing about this setting worth being explicit about: it does not mean "a
// person authorizes the push the harness then makes", the way `integration:
// human` means a person authorizes a promotion. The design's publishing matrix
// gives both `human` publishing rows as "purely local -- nothing is pushed", the
// setting's own documentation says it "leaves pushing and pull requests to the
// operator", and resolvePublishing returns before the publisher is consulted at
// all. So a project on `human` reaches no forge, and requiring `gh` for it would
// fail an installation that is complete.
func HarnessPublishes(publishing domain.ApprovalMode) bool {
	return publishing == domain.ApprovalAutomatic
}

// RepositoryPath resolves what a configuration means by its repository: the
// project directory unless the configuration points somewhere else, and that
// path made absolute where it does. It is exported because `yoyo setup` has to
// initialize the tracker in the same directory this diagnosis then asks the
// tracker about -- two answers to "which repository" would be a setup that put
// one right and a diagnosis that reported the other as broken.
func RepositoryPath(project, repository string) string {
	if strings.TrimSpace(repository) == "" {
		return project
	}
	if filepath.IsAbs(repository) {
		return filepath.Clean(repository)
	}
	return filepath.Clean(filepath.Join(project, repository))
}

// providerBinary maps a configured backend onto the command that executes it.
// An unmapped backend is not defaulted: guessing a binary name would report a
// provider that does not exist as one that is merely not installed.
func providerBinary(named domain.Backend) string {
	return adapters.Binary(backend.Descriptor{Adapter: named})
}

func providerInstallCommand(named domain.Backend) string {
	switch named {
	case domain.BackendCodex:
		return "npm install -g @openai/codex"
	default:
		return "npm install -g @anthropic-ai/claude-code"
	}
}

func providerLoginCommand(named domain.Backend) string {
	switch named {
	case domain.BackendCodex:
		return "codex login"
	default:
		return "claude auth login"
	}
}

// installCommand names how this platform installs one of the ordinary tools.
// It is a guess about the operator's package manager, which is why it is only
// used for tools whose install is otherwise unsayable; a guess that is wrong is
// still a command that names what to install.
func (d *diagnosis) installCommand(tool string) string {
	if d.env.GOOS == "darwin" {
		return "brew install " + tool
	}
	return "sudo apt-get install -y " + tool
}

// inDirectory renders the `-C`-style suffix for a command that has to be run
// somewhere in particular, and nothing when it does not.
func inDirectory(directory string) string {
	if strings.TrimSpace(directory) == "" {
		return ""
	}
	return "   # in " + directory
}

// programOf finds the command a check line would actually execute: the first
// word, past any leading environment assignments. It is deliberately simple --
// this is a resolvability probe rather than a shell -- and anything it cannot
// read confidently it declines to judge.
func programOf(command string) string {
	for _, field := range strings.Fields(command) {
		if strings.ContainsRune(field, '=') && !strings.ContainsRune(field, '/') {
			continue
		}
		return strings.Trim(field, `"'`)
	}
	return ""
}

// shellBuiltin is the small set a check line plausibly starts with that no PATH
// lookup would ever find. A builtin is not evidence of anything, so it is passed
// over rather than reported as missing.
func shellBuiltin(program string) bool {
	switch program {
	case "cd", "echo", "export", "set", "test", "[", "true", "false", "eval", "exec", "if", "for", "while":
		return true
	}
	return false
}

// shellQuote makes a path safe to paste into a remedy. Remedies are commands, so
// a path with a space in it has to survive being one.
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n\"'\\$`*?[]{}()|&;<>#~!") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// describeFailure says what a tool did, whichever way it failed: refusing to
// start and running and refusing are different facts and the remedy differs
// between them, so neither is folded into the other.
func describeFailure(result execution.ProcessResult, err error) string {
	if err != nil {
		return err.Error()
	}
	if message := firstLine(firstNonEmpty(result.Stderr, result.Stdout)); message != "" {
		return fmt.Sprintf("%s (exit %d)", message, result.ExitCode)
	}
	return fmt.Sprintf("%s (exit %d)", result.Status, result.ExitCode)
}

func firstLine(text string) string {
	trimmed := strings.TrimSpace(text)
	if index := strings.IndexRune(trimmed, '\n'); index >= 0 {
		return strings.TrimSpace(trimmed[:index])
	}
	return trimmed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func countOf(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}
