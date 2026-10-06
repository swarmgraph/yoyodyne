package cli

// Reaching the state `yoyo doctor` defines as correct, by walking the operator
// there and asking before each step.
//
// Everything setup does exists already: `bd init`, `yoyo init`, the Slack
// recipe in docs/slack/setup.md. What did not exist is anything that puts them
// in order, so a newcomer between a binary on PATH and a working installation
// had to read four documents and know which parts of them applied to them. This
// is that path as a command, and it is deliberately a wrapper: every step is
// something an operator could have typed, and setup asks before typing it.
//
// It is idempotent because it remembers nothing. Each step looks at the
// installation before it does anything, and a step that is already true is
// reported as already true rather than repeated -- which is also what makes an
// interrupted setup resumable, since a second run picks up exactly where the
// installation actually is rather than where a record claims it got to. There
// is no state file here on purpose: a record of what setup did could disagree
// with the machine, and then a resumed setup would be converging on the record.
//
// It never overwrites what is already there. A configuration that exists and
// does not load is handed to the operator rather than regenerated, and a token
// already in the keychain is left alone -- one person running several harnesses
// is the ordinary case, and the second install clobbering the first's tokens is
// exactly the failure the namespaced names exist to prevent.
//
// And it ends by asking `yoyo doctor`, which is the only thing here that decides
// whether the installation is actually working. Setup's own steps say what it
// did; the diagnosis says what is true, carries a command for everything setup
// could not do itself, and is what this command's exit status is taken from.

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/launchd"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// setupSchemaVersion versions the machine-readable report, which is its own
// number for the reason doctor's is: what reads it is not this executable.
const setupSchemaVersion = 1

// The steps, named stably so a caller keying on one -- the JSON report, a test,
// a script walking this path -- keys on a name rather than on prose.
const (
	stepTracker         = "tracker"
	stepConfiguration   = "configuration"
	stepTrackerRemote   = "tracker-remote"
	stepArtifactReadmes = "artifact-readmes"
	stepSlack           = "slack"
	stepSlackSecrets    = "slack-secrets"
	stepLaunchAgent     = "launch-agent"
)

// setupStatus is what became of one step. Four values rather than two, because
// an operator reading this needs to tell what setup did from what was already
// true from what is now theirs to finish: a step nobody has to act on and a step
// waiting on them look identical under a status that only says "not done".
type setupStatus string

const (
	// setupAlready is a step that was true before setup touched anything, which
	// is what every step reads as on a second run.
	setupAlready setupStatus = "already"
	// setupDone is a step setup carried out.
	setupDone setupStatus = "done"
	// setupSkipped is a step the operator declined, or one that was never
	// offered because nothing could be asked. Nothing was changed either way.
	setupSkipped setupStatus = "skipped"
	// setupHandedOff is a step setup cannot carry out itself. It carries the
	// command that does, which is the same promise doctor makes about a finding.
	setupHandedOff setupStatus = "handed-off"
)

// setupStep is one step and what became of it.
type setupStep struct {
	Step    string      `json:"step"`
	Status  setupStatus `json:"status"`
	Summary string      `json:"summary"`
	Detail  string      `json:"detail,omitempty"`
	// Remedy is a command, and is present on every step that leaves anything for
	// somebody to do or to decide. A step somebody still owes something on is
	// worth nothing without the thing they would run.
	Remedy string `json:"remedy,omitempty"`
}

// setupReport is the whole walk: what setup did, and what the installation is
// afterwards.
type setupReport struct {
	SchemaVersion int         `json:"schema_version"`
	Product       string      `json:"product,omitempty"`
	Config        string      `json:"config,omitempty"`
	Steps         []setupStep `json:"steps"`
	// Diagnosis is `yoyo doctor` run at the end, unabridged. Setup's steps are
	// an account of what it did and this is the account of whether it worked;
	// folding one into the other would let a walk that carried out every step
	// report success over an installation that still cannot run work.
	Diagnosis doctor.Report `json:"diagnosis"`
}

func runSetup(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("directory", ".", "project directory to set up")
	product := flags.String("product", "", "product id (default: the project directory name)")
	channel := flags.String("slack-channel", "", "Slack channel to report into, which is also how the optional Slack walk is answered without being asked")
	launchAgent := flags.Bool("launch-agent", false, "install the launch agent that starts the product with the machine, which is how that step is answered yes without being asked")
	assumeYes := flags.Bool("yes", false, "answer every question setup asks with the answer it proposes")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON, asking nothing and, without --yes, changing nothing")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "setup does not accept positional arguments: it sets up one project")
		printSetupUsage(stderr)
		return 2
	}

	// Exit 1 belongs to the diagnosis and to nothing else, which is what lets
	// anything reading this exit 1 read the report on stdout rather than work out
	// which kind of 1 it got. Being pointed at a directory that is not there is
	// the command being used wrongly, like the positional argument above it, so it
	// says so on stderr and exits the way that does.
	root, err := filepath.Abs(*directory)
	if err != nil {
		fmt.Fprintf(stderr, "resolve project directory %q: %v\n", *directory, err)
		return 2
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "%s is not a directory to set up\n", root)
		return 2
	}

	walk := &setup{
		directory:    root,
		product:      *product,
		slackChannel: strings.TrimSpace(*channel),
		launchAgent:  *launchAgent,
		version:      version,
		runner:       execution.OSProcessRunner{},
		lookPath:     exec.LookPath,
		getenv:       os.Getenv,
		homeDir:      os.UserHomeDir,
		goos:         runtime.GOOS,
		executable:   os.Executable,
		environ:      os.Environ(),
		getuid:       os.Getuid,
		stdin:        stdin,
		ask:          &questioner{reader: bufio.NewReader(stdin), out: stdout, defaults: *assumeYes},
	}
	if *jsonOutput {
		// A machine-readable report with a prompt in the middle of it is not
		// machine-readable, so under --json the questions are not asked. They are
		// declined rather than defaulted unless --yes says otherwise: asking for
		// a report is not consent to change the machine, and a caller that wants
		// the walk carried out says so.
		walk.ask.out = io.Discard
		walk.ask.closed = !*assumeYes
		walk.unattended = true
	}

	report := walk.converge(ctx)

	if *jsonOutput {
		if writeJSON(stdout, stderr, report) != 0 {
			// Nothing readable reached stdout, so this is not the diagnosis
			// exiting 1 with a report to act on. It is the command failing to do
			// what it was asked, which is what 2 says -- and what keeps anything
			// reading an exit 1 sure there is a report under it.
			return 2
		}
	} else {
		renderSetup(stdout, report)
	}
	// The exit status is the diagnosis's rather than the walk's, which is the
	// whole point of ending on one: a walk where every question was answered
	// yes still leaves an installation that cannot run work if the provider is
	// not authenticated, and setup must not report that as a success.
	if !report.Diagnosis.Healthy() {
		return 1
	}
	return 0
}

// setup is one walk. Every way it reads or changes the machine is a field, for
// the reason doctor's environment is: the states worth being complete over --
// a tracker that is not there, a configuration that does not load, a keychain
// with one of the two tokens in it -- are exactly the ones that are inconvenient
// to arrange for real.
type setup struct {
	directory    string
	product      string
	slackChannel string
	// launchAgent is the operator asking for the launch agent on the command
	// line, which is what answers that step yes on a walk answering itself.
	launchAgent bool
	version     string

	runner   execution.ProcessRunner
	lookPath func(string) (string, error)
	getenv   func(string) string
	homeDir  func() (string, error)
	goos     string
	// executable is the binary the launch agent is written to run, environ
	// what it is given, and getuid the launchd domain it is loaded into. A walk
	// assembled without the first offers no agent.
	executable func() (string, error)
	environ    []string
	getuid     func() int

	// stdin is the operator's own input, handed to a child process that prompts
	// for a credential itself. It is deliberately the same stream the questions
	// are read from: the keychain asks for the token on the terminal rather than
	// through this, and giving the child anything else would leave it reading a
	// stream nobody is typing into.
	stdin io.Reader
	ask   *questioner
	// unattended says nothing this walk writes reaches anybody, which is what
	// --json means. It gates the one step that hands the terminal to another
	// program: a keychain prompt on a walk nobody is watching is a command that
	// looks hung, over a credential nothing here could have explained.
	unattended bool

	steps []setupStep
}

// setupCommandTimeout bounds the commands setup runs. It is longer than the
// probes elsewhere in this package because `bd init` stands a database engine
// up for the first time in a project, which is the one thing here that is slow
// on purpose.
const setupCommandTimeout = 3 * time.Minute

func (s *setup) converge(ctx context.Context) setupReport {
	// The tracker first, because everything under it is asked of the tracker or
	// of a configuration that names where the tracker lives.
	tracker := s.ensureTracker(ctx)
	s.record(tracker)
	s.record(s.ensureConfiguration(ctx))
	// Where the tracker syncs is its own step rather than something the
	// configuration step does on its way past. `.yoyodyne/config.yaml` is a
	// committed file, so the ordinary way a machine arrives at a configured
	// project is by cloning one -- and a project whose configuration is already
	// there is exactly the case a remote configured only while writing that file
	// would never reach. As a step it is read first and converged on every run,
	// like everything else here.
	s.record(s.ensureTrackerRemote(ctx, tracker))

	// Everything below is what the project turns on, so it is only asked about
	// once there is a project configuration to read. A configuration that does
	// not load has already been handed off above, and inventing answers about
	// reporting on top of it would be inventing them about a project nothing
	// here can see.
	if resolved, err := s.load(); err == nil {
		// The indexes come before the optional tier because they are about the
		// project itself: a repository configured before these existed has homes
		// full of governed documents and nothing at the door of any of them, and
		// this is the step that closes that for an installation that already
		// works. `yoyo init` writes them for one that does not exist yet.
		s.record(s.ensureArtifactHomeIndexes(resolved))
		s.record(s.ensureSlackReporting(resolved))
		// Reloaded rather than reused: the step above may have just turned
		// reporting on, and the secrets are namespaced by a product id that only
		// the file is authoritative about.
		if reloaded, err := s.load(); err == nil && reloaded.Config.Slack.Enabled {
			s.record(s.ensureSlackSecrets(ctx, reloaded.Config.Product.ID))
		}
		// Last, because it starts the product: the agent it installs runs the
		// supervisor, which starts every part the section above enables from
		// whatever the steps above stored.
		if reloaded, err := s.load(); err == nil {
			s.record(s.ensureLaunchAgent(ctx, reloaded))
		}
	}

	report := setupReport{SchemaVersion: setupSchemaVersion, Steps: s.steps}
	report.Diagnosis = s.diagnose(ctx)
	report.Product = report.Diagnosis.Product
	report.Config = report.Diagnosis.Config
	return report
}

func (s *setup) record(step setupStep) {
	if step.Step == "" {
		return
	}
	s.steps = append(s.steps, step)
}

// load reads the configuration this walk is about. It is the file under the
// directory setup was pointed at rather than whatever `yoyo` would discover
// from the working directory: a project with no configuration of its own would
// otherwise resolve to an ancestor's, and setup would report a project it never
// touched as configured.
func (s *setup) load() (config.Resolved, error) {
	return loadActivatedConfiguration(s.configPath())
}

func (s *setup) configPath() string {
	return filepath.Join(s.directory, config.DirectoryName, config.FileName)
}

// diagnose asks doctor about the same project, through the same seams, so what
// setup says and what the operator gets from `yoyo doctor` afterwards are the
// same answers rather than two implementations of the same question.
func (s *setup) diagnose(ctx context.Context) doctor.Report {
	return doctor.Diagnose(ctx, doctor.Environment{
		Runner:      s.runner,
		LookPath:    s.lookPath,
		Getenv:      s.getenv,
		UserHomeDir: s.homeDir,
		GOOS:        s.goos,
		Version:     s.version,
		Load:        s.load,
	})
}

// repository is the directory the tracker and the checks belong to. It is the
// project directory until a configuration says otherwise, and doctor's own
// resolution afterwards, so both mean the same place by "the repository".
func (s *setup) repository() string {
	resolved, err := s.load()
	if err != nil {
		return s.directory
	}
	return doctor.RepositoryPath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
}

// ensureTracker gets `bd` answering in this project. The tracker is a required
// dependency rather than an integration -- work items, their dependencies, and
// the durable record of what agents did all live there -- so this is the one
// step with no optional reading of it.
func (s *setup) ensureTracker(ctx context.Context) setupStep {
	repository := s.repository()
	if _, err := s.lookPath("bd"); err != nil {
		return setupStep{
			Step:    stepTracker,
			Status:  setupHandedOff,
			Summary: "bd is not installed, and setup does not install tools",
			Detail:  err.Error(),
			Remedy:  beads.InstallCommand,
		}
	}
	if s.trackerAnswers(ctx, repository) {
		return setupStep{
			Step:    stepTracker,
			Status:  setupAlready,
			Summary: "the tracker already answers in this project",
			Detail:  repository,
		}
	}

	remedy := fmt.Sprintf("bd init%s", inDirectoryNote(repository))
	if !s.ask.confirm(fmt.Sprintf("Initialize the tracker in %s? Every role reads and writes it", repository), true) {
		return setupStep{
			Step:    stepTracker,
			Status:  setupSkipped,
			Summary: "the tracker was left uninitialized, so no work can be claimed here",
			Remedy:  remedy,
		}
	}
	result, err := s.run(ctx, repository, "bd", "init")
	if err != nil || result.Status != execution.ProcessSucceeded {
		return setupStep{
			Step:    stepTracker,
			Status:  setupHandedOff,
			Summary: "bd would not initialize this project",
			Detail:  describeCommandFailure(result, err),
			Remedy:  remedy,
		}
	}
	if !s.trackerAnswers(ctx, repository) {
		// `bd init` returning success and the tracker still not answering is a
		// state worth separating from a refusal: whatever has to be looked at is
		// bd's rather than setup's, and saying "done" over it would put the next
		// failure several steps away from its cause.
		return setupStep{
			Step:    stepTracker,
			Status:  setupHandedOff,
			Summary: "bd init reported success and the tracker still does not answer here",
			Remedy:  fmt.Sprintf("bd stats%s", inDirectoryNote(repository)),
		}
	}
	return setupStep{
		Step:    stepTracker,
		Status:  setupDone,
		Summary: "initialized the tracker, and it answers in this project",
		Detail:  repository,
	}
}

// trackerAnswers asks the cheapest question that proves the tracker is usable
// here rather than merely installed, which is the same question doctor asks.
func (s *setup) trackerAnswers(ctx context.Context, repository string) bool {
	result, err := s.run(ctx, repository, "bd", "stats")
	return err == nil && result.Status == execution.ProcessSucceeded
}

// ensureConfiguration gives the project its own configuration, which is `yoyo
// init` and nothing else: the same scaffold, the same proposed checks read from
// what the repository already declares, the same tracker remote.
//
// It refuses to touch a configuration that is already there, in either
// direction. One that loads is this project's own and setup has no business
// rewriting it; one that does not load is somebody's work in progress, and
// regenerating it would be deleting an edit to fix a typo in it.
func (s *setup) ensureConfiguration(ctx context.Context) setupStep {
	path := s.configPath()
	if _, err := os.Stat(path); err == nil {
		if resolved, err := s.load(); err == nil {
			return setupStep{
				Step:    stepConfiguration,
				Status:  setupAlready,
				Summary: fmt.Sprintf("%s is already configured", resolved.Config.Product.ID),
				Detail:  resolved.Path,
			}
		} else {
			return setupStep{
				Step:   stepConfiguration,
				Status: setupHandedOff,
				// Said as a refusal rather than as a failure, because the file
				// being there is the reason: an operator who reads this as
				// "setup broke" would reach for --force somewhere.
				Summary: "this project has a configuration that does not load, and setup does not overwrite one",
				Detail:  err.Error(),
				Remedy:  fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(path)),
			}
		}
	}

	if !s.ask.confirm(fmt.Sprintf("Write %s its own configuration and personas? (yoyo init)", s.directory), true) {
		return setupStep{
			Step:    stepConfiguration,
			Status:  setupSkipped,
			Summary: "the project was left unconfigured, so nothing here can run work",
			Remedy:  fmt.Sprintf("yoyo init --directory %s", shellQuote(s.directory)),
		}
	}

	initialization, err := initializeProject(initializeOptions{Directory: s.directory, ProductID: s.product})
	if err != nil {
		return setupStep{
			Step:    stepConfiguration,
			Status:  setupHandedOff,
			Summary: "the configuration could not be written",
			Detail:  err.Error(),
			Remedy:  fmt.Sprintf("yoyo init --directory %s", shellQuote(s.directory)),
		}
	}
	return setupStep{
		Step:    stepConfiguration,
		Status:  setupDone,
		Summary: fmt.Sprintf("wrote %s and the personas beside it", initialization.config),
		// What the operator still owes on the checks is the one thing about a
		// fresh configuration that is not visible in the file itself.
		Detail: describeDetection(initialization.detection, initialization.config),
	}
}

// ensureTrackerRemote points the tracker at the URL it should sync through,
// which is this project's own Git remote: bd moves its data over an ordinary
// Git remote under refs of Dolt's own, so one repository and one permission
// model carry both.
//
// It never replaces a remote the tracker already holds. A project that
// deliberately syncs its tracker somewhere else -- a repository of its own,
// which bd supports with any Git URL -- must not have that decision undone by
// something that ran to set up a machine, so an `origin` that is already there
// is reported and left.
//
// Everything it does before the question is a read, and the write is behind the
// question like every other write here. That ordering is the whole of what makes
// `yoyo setup --json` a report rather than an act: asking what a machine needs
// is not consent to do it, and a step that mutated on the way to answering would
// make the report itself the change.
func (s *setup) ensureTrackerRemote(ctx context.Context, tracker setupStep) setupStep {
	if tracker.Status != setupAlready && tracker.Status != setupDone {
		// Reading or setting where the tracker syncs is asking the tracker, and
		// the step above has already said why it cannot be asked. The remedy is
		// that step's, because what has to happen first is that.
		return setupStep{
			Step:    stepTrackerRemote,
			Status:  setupHandedOff,
			Summary: "where the tracker syncs could not be read, because the tracker does not answer here yet",
			Detail:  tracker.Summary,
			Remedy:  tracker.Remedy,
		}
	}
	repository := s.repository()
	held, err := (beads.Client{Runner: s.runner, Dir: repository, Timeout: trackerCommandTimeout}).SyncRemotes(ctx)
	if err != nil {
		return setupStep{
			Step:    stepTrackerRemote,
			Status:  setupHandedOff,
			Summary: "bd would not say where the tracker syncs, so it was left as it is",
			Detail:  singleLine(err.Error()),
			Remedy:  fmt.Sprintf("bd dolt remote add %s <url>%s", trackerRemoteName, inDirectoryNote(repository)),
		}
	}
	for _, remote := range held {
		if remote.Name == trackerRemoteName {
			return setupStep{
				Step:    stepTrackerRemote,
				Status:  setupAlready,
				Summary: fmt.Sprintf("the tracker already syncs through %s: %s", remote.Name, remote.URL),
				Detail:  "setup leaves a remote the tracker already holds exactly as it is",
			}
		}
	}

	url, absence := gitRemoteURL(ctx, s.runner, repository, trackerRemoteName)
	if url == "" {
		// Nothing is wrong with this project; it has nowhere to sync to yet.
		// It is still said, and still carries the command, because a per-machine
		// backlog is discovered at the first divergence otherwise.
		return setupStep{
			Step:    stepTrackerRemote,
			Status:  setupSkipped,
			Summary: "the tracker syncs nowhere, so this machine's backlog is its own",
			Detail:  singleLine(absence),
			Remedy: fmt.Sprintf("git -C %s remote add %s <url> && %s",
				shellQuote(repository), trackerRemoteName, s.setupCommand()),
		}
	}
	uncommitted := fmt.Sprintf("bd dolt remote add %s %s%s", trackerRemoteName, url, inDirectoryNote(repository))
	if !s.ask.confirm(fmt.Sprintf("Sync the tracker through %s, so the backlog is shared rather than one per machine?", url), true) {
		return setupStep{
			Step:    stepTrackerRemote,
			Status:  setupSkipped,
			Summary: "the tracker syncs nowhere, so this machine's backlog is its own",
			Detail:  fmt.Sprintf("this project's Git remote %s is what it would ride on", url),
			Remedy:  uncommitted,
		}
	}
	// The URL that was asked about is the URL that is configured, rather than
	// one read again afterwards: a remote quietly different from the one the
	// answer was given for is not the thing that was agreed to.
	remote := configureTrackerRemote(ctx, repository, url, s.runner)
	if remote.Status != trackerRemoteConfigured {
		return setupStep{
			Step:    stepTrackerRemote,
			Status:  setupHandedOff,
			Summary: "bd would not point the tracker at this project's Git remote",
			Detail:  singleLine(remote.Reason),
			Remedy:  uncommitted,
		}
	}
	return setupStep{
		Step:    stepTrackerRemote,
		Status:  setupDone,
		Summary: fmt.Sprintf("the tracker now syncs through %s: %s", remote.Name, remote.URL),
		Detail:  "the backlog is shared over this project's own Git remote rather than one per machine",
	}
}

// ensureArtifactHomeIndexes gives every directory of governed documents the
// README that says what is filed there, whose it is, and whether the operator
// may edit one by hand. It is the repair half of what `yoyo doctor` reports.
//
// It writes an index that is missing and asks separately about one that is there
// and has stopped answering, because those are two different things to consent
// to. Writing a file nobody wrote loses nothing. Replacing one somebody did write
// loses their prose, which is why that question defaults to no and says what it
// would cost -- setup does not overwrite what is already there without being told
// to, and an index somebody customized is exactly the case that rule is for.
func (s *setup) ensureArtifactHomeIndexes(resolved config.Resolved) setupStep {
	repository := s.repository()
	root, err := repowrite.NewRoot(repository)
	if err != nil {
		return setupStep{
			Step:    stepArtifactReadmes,
			Status:  setupHandedOff,
			Summary: "the artifact homes could not be looked at, so nothing was written at the door of any of them",
			Detail:  err.Error(),
			Remedy:  s.setupCommand(),
		}
	}

	var missing, incomplete, unreadable []artifacthome.Status
	for _, status := range artifacthome.Inspect(root, resolved.Config) {
		switch status.State {
		case artifacthome.StateMissing:
			missing = append(missing, status)
		case artifacthome.StateIncomplete:
			incomplete = append(incomplete, status)
		case artifacthome.StateUnreadable:
			unreadable = append(unreadable, status)
		}
	}
	if len(missing) == 0 && len(incomplete) == 0 && len(unreadable) == 0 {
		return setupStep{
			Step:    stepArtifactReadmes,
			Status:  setupAlready,
			Summary: "every artifact home already says what is filed there, who owns it, and the hand-edit policy",
			Detail:  repository,
		}
	}

	var written []string
	var left []artifacthome.Status
	left = append(left, unreadable...)
	if len(missing) > 0 {
		question := fmt.Sprintf("Write %s into %s? Each says what is filed there, who owns it, and whether you may edit one by hand", countOf(len(missing), "README"), joinPaths(missing))
		if s.ask.confirm(question, true) {
			written, left = writeIndexes(root, missing, written, left)
		} else {
			left = append(left, missing...)
		}
	}
	if len(incomplete) > 0 {
		question := fmt.Sprintf("Replace %s that no longer answer all three? Whatever you wrote in them is replaced: %s", countOf(len(incomplete), "README"), joinPaths(incomplete))
		if s.ask.confirm(question, false) {
			written, left = writeIndexes(root, incomplete, written, left)
		} else {
			left = append(left, incomplete...)
		}
	}

	remedy := s.setupCommand()
	if len(left) > 0 && len(missing) == 0 && len(unreadable) == 0 {
		// Nothing was missing and nothing was unreadable, so what is left is an
		// index somebody wrote and setup was told not to replace. Running setup
		// again would ask the same question; the file itself is theirs to finish.
		remedy = "${EDITOR:-vi} " + strings.Join(resolvedPaths(repository, left), " ")
	}
	switch {
	case len(written) == 0 && len(left) > 0:
		return setupStep{
			Step:    stepArtifactReadmes,
			Status:  setupSkipped,
			Summary: fmt.Sprintf("%s left without an index that answers all three", countOf(len(left), "artifact home")),
			Detail:  artifacthome.Describe(left),
			Remedy:  remedy,
		}
	case len(left) > 0:
		summary := fmt.Sprintf("wrote %s; %s still without one that answers all three", countOf(len(written), "README"), countOf(len(left), "artifact home"))
		return setupStep{
			Step:    stepArtifactReadmes,
			Status:  setupDone,
			Summary: summary,
			Detail:  artifacthome.Describe(left),
			Remedy:  remedy,
		}
	default:
		return setupStep{
			Step:    stepArtifactReadmes,
			Status:  setupDone,
			Summary: fmt.Sprintf("wrote %s at the door of the artifact homes", countOf(len(written), "README")),
			Detail:  strings.Join(written, ", "),
		}
	}
}

// writeIndexes writes what was agreed to and keeps whatever refused to be
// written, so one home the filesystem would not take never hides the rest.
func writeIndexes(root repowrite.Root, candidates []artifacthome.Status, written []string, left []artifacthome.Status) ([]string, []artifacthome.Status) {
	for _, candidate := range candidates {
		if _, err := artifacthome.Write(root, candidate.Home); err != nil {
			candidate.State = artifacthome.StateUnreadable
			candidate.Detail = err.Error()
			left = append(left, candidate)
			continue
		}
		written = append(written, candidate.Path)
	}
	return written, left
}

func joinPaths(statuses []artifacthome.Status) string {
	return strings.Join(artifacthome.Paths(statuses), ", ")
}

// resolvedPaths names the indexes as an operator would open them, which is the
// repository-relative path joined onto the repository the remedy is about.
func resolvedPaths(repository string, statuses []artifacthome.Status) []string {
	paths := make([]string, 0, len(statuses))
	for _, status := range statuses {
		paths = append(paths, shellQuote(filepath.Join(repository, filepath.FromSlash(status.Path))))
	}
	return paths
}

// ensureSlackReporting is the optional tier, and it is optional in the sense
// that an installation without it runs work exactly as one with it does.
// Reporting is an observation and never a gate, so nothing here can leave the
// project worse off than not being asked would have.
//
// What it does not do is repeat the recipe. The app is created from the
// manifest checked into this repository and the steps in docs/slack/setup.md,
// which are one document consumed by every path to a working sink; a copy of
// them here would be a second document to keep in step with Slack's own screens.
func (s *setup) ensureSlackReporting(resolved config.Resolved) setupStep {
	settings := resolved.Config.Slack
	if settings.Enabled && strings.TrimSpace(settings.Channel) != "" {
		return setupStep{
			Step:    stepSlack,
			Status:  setupAlready,
			Summary: fmt.Sprintf("this project already reports into %s", settings.Channel),
			Detail:  resolved.Path,
		}
	}

	// Naming a channel on the command line is the operator saying they want
	// this, so it is what the two questions default to. Without one the tier is
	// declined by default, which is what a walk that cannot ask -- and every
	// walk that is answering itself -- then does with it.
	wanted := s.slackChannel != ""

	// The offer is worth a sentence before the question, because what somebody
	// is agreeing to here is a Slack app in their own workspace rather than a
	// setting. What that app is, and why each scope is asked for, is the recipe.
	s.ask.say("Reporting into Slack is optional and changes nothing about how work runs:\none thread per work item, one message per milestone, posted by a separate\nprocess that holds the two tokens. The app, its scopes, and the two tokens:\n%s", setupSlackRecipe)
	if !s.ask.confirm("Report this product's work into a Slack channel?", wanted) {
		return setupStep{
			Step:    stepSlack,
			Status:  setupSkipped,
			Summary: "Slack reporting is off, so the account of the work stays on this machine",
			Detail:  "nothing about a run changes either way; the recipe is " + setupSlackRecipe,
			Remedy:  s.setupCommand(),
		}
	}
	if !s.ask.confirm("Is that app created from the manifest and installed into your workspace?", wanted) {
		return setupStep{
			Step:   stepSlack,
			Status: setupHandedOff,
			// Deliberately not a failure and deliberately resumable: the app is
			// made on Slack's own screens, and the whole of what setup can do
			// about it is say where the steps are and pick this up again next
			// time it is run.
			Summary: "the Slack app has to exist before this project can be pointed at it",
			Detail:  setupSlackRecipe,
			Remedy:  s.setupCommand(),
		}
	}

	channel := s.ask.line("Which channel? Its id from the channel's About panel, like C0123456789", s.slackChannel)
	if strings.TrimSpace(channel) == "" {
		return setupStep{
			Step:    stepSlack,
			Status:  setupSkipped,
			Summary: "no channel was named, so reporting was left off",
			Detail:  "a project that enables reporting without naming a channel is refused when the configuration loads",
			Remedy:  s.setupCommand() + " --slack-channel <channel id>",
		}
	}
	if err := enableSlackReporting(resolved.Path, strings.TrimSpace(channel)); err != nil {
		return setupStep{
			Step:    stepSlack,
			Status:  setupHandedOff,
			Summary: "the configuration could not be told where to report",
			Detail:  err.Error(),
			Remedy:  fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(resolved.Path)),
		}
	}
	return setupStep{
		Step:    stepSlack,
		Status:  setupDone,
		Summary: fmt.Sprintf("this project now reports into %s", strings.TrimSpace(channel)),
		Detail:  resolved.Path,
	}
}

// setupSlackRecipe is where the app, its scopes, and its two tokens are
// described. It is a pointer rather than a summary on purpose: the manifest and
// this document are what both paths to a working sink read, and a second
// telling of them here is one that goes stale the first time Slack moves a
// button.
const setupSlackRecipe = "docs/slack/setup.md, or " +
	"https://github.com/mason-bryant/yoyodyne/blob/main/docs/slack/setup.md"

// ensureSlackSecrets stores this project's own token pair, under the names that
// carry the product.
//
// On macOS the keychain does the asking. Setup runs `security` and gets out of
// the way: the token is typed at the keychain's own prompt, so it never reaches
// this process, this process's memory, an argument list `ps` would show, or a
// shell history -- which is the same discipline that keeps doctor from ever
// reading one back.
//
// Everywhere else the store is a file this project's launch sources, and what
// this step reads is the two names in it rather than its existence -- the same
// reading `yoyo doctor` makes, out of the same place, so the two surfaces cannot
// disagree about one file. Existence would be the wrong question: the documented
// way to make the file installs an empty one and then opens an editor, so an
// operator who left the editor without saving would be told the step is already
// done over a sink that refuses to start for want of the names nobody wrote.
//
// Setup still does not create the file, because the only thing that would make
// it a stored pair is the two tokens, and those are the operator's to paste in.
func (s *setup) ensureSlackSecrets(ctx context.Context, productID domain.ProductID) setupStep {
	bot, app := slack.BotSecret(productID), slack.AppSecret(productID)
	if file, found := s.namespacedEnvFile(productID); found {
		return s.slackEnvFileStep(file, productID)
	}
	if s.goos != "darwin" {
		directory, file := s.envFilePaths(productID)
		return setupStep{
			Step:    stepSlackSecrets,
			Status:  setupHandedOff,
			Summary: fmt.Sprintf("this platform has no keychain, so %s's two tokens go in a file only its own launch reads", productID),
			Detail: fmt.Sprintf("setup does not write it, because an empty file is not a stored pair and only the two tokens would make it one. It wants %s and %s as exports",
				botTokenVariable, appTokenVariable),
			Remedy: fmt.Sprintf("mkdir -p %s && install -m 600 /dev/null %s && ${EDITOR:-vi} %s", directory, file, file),
		}
	}

	missing := s.missingKeychainSecrets(ctx, bot, app)
	if len(missing) == 0 {
		return setupStep{
			Step:    stepSlackSecrets,
			Status:  setupAlready,
			Summary: fmt.Sprintf("this project's Slack secrets are already in the keychain as %s and %s", bot, app),
			// Said rather than assumed, because leaving them alone is a decision:
			// one person running several harnesses is the ordinary case, and a
			// setup that overwrote a pair it found would break the install it was
			// not being run for.
			Detail: "setup leaves a stored pair exactly as it is",
		}
	}
	remedy := keychainAddCommand(missing)
	if s.unattended {
		// The keychain would prompt on a terminal this walk is not writing to,
		// which is a command that looks hung to whoever started it and a
		// credential prompt nothing explained. The pair is left for a walk with
		// somebody behind it, which is what the remedy is.
		return setupStep{
			Step:    stepSlackSecrets,
			Status:  setupHandedOff,
			Summary: fmt.Sprintf("storing %s's Slack tokens needs somebody at the terminal the keychain prompts on", productID),
			Detail:  strings.Join(missing, " and ") + " are not stored",
			Remedy:  remedy,
		}
	}
	s.ask.say("The keychain asks for each token itself, so no token reaches this program,\nits arguments, or your shell history. Both are on the app's own pages: the bot\nuser OAuth token under OAuth & Permissions, the app-level token under Basic\nInformation.")
	if !s.ask.confirm(fmt.Sprintf("Store %s's two Slack tokens in the keychain now?", productID), true) {
		return setupStep{
			Step:    stepSlackSecrets,
			Status:  setupSkipped,
			Summary: fmt.Sprintf("this project's Slack secrets are not stored: %s", strings.Join(missing, " and ")),
			Detail:  "nothing else changes; a sink with no tokens cannot start, and every run proceeds exactly as it would have",
			Remedy:  remedy,
		}
	}
	for _, name := range missing {
		fmt.Fprintf(s.ask.out, "  the keychain will ask for the %s. %s\n", describeSecret(name, bot), setupSlackRecipe)
		result, err := s.storeKeychainSecret(ctx, name)
		if err != nil || result.Status != execution.ProcessSucceeded {
			return setupStep{
				Step:    stepSlackSecrets,
				Status:  setupHandedOff,
				Summary: fmt.Sprintf("the keychain did not take %s", name),
				Detail:  describeCommandFailure(result, err),
				Remedy:  keychainAddCommand(s.missingKeychainSecrets(ctx, bot, app)),
			}
		}
	}
	return setupStep{
		Step:    stepSlackSecrets,
		Status:  setupDone,
		Summary: fmt.Sprintf("stored %s in the keychain", strings.Join(missing, " and ")),
		Detail:  "the sink reads them from names that carry the product, so a second harness on this machine keeps its own pair",
	}
}

// storeKeychainSecret hands the terminal to the keychain. `-w` with no value is
// what makes it prompt, and the prompt is the whole point: setup is not in the
// path the token takes, so there is nothing here for it to leak.
func (s *setup) storeKeychainSecret(ctx context.Context, name string) (execution.ProcessResult, error) {
	return s.runner.Run(ctx, execution.Command{
		Name:    "security",
		Args:    []string{"add-generic-password", "-s", name, "-a", setupKeychainAccount, "-w"},
		Stdin:   s.stdin,
		Timeout: setupCommandTimeout,
	}, nil)
}

// missingKeychainSecrets names which of the two the keychain does not have. The
// query asks for the item rather than for its password, which is how a question
// about a secret is asked without producing one.
func (s *setup) missingKeychainSecrets(ctx context.Context, names ...string) []string {
	var missing []string
	for _, name := range names {
		result, err := s.run(ctx, "", "security", "find-generic-password", "-s", name, "-a", setupKeychainAccount)
		if err != nil || result.Status != execution.ProcessSucceeded {
			missing = append(missing, name)
		}
	}
	return missing
}

// setupKeychainAccount is the account the namespaced items are stored under. It
// is fixed because the distinguishing part is the service name, which carries
// the product -- the same arrangement doctor reads them back through.
const setupKeychainAccount = "yoyo"

func keychainAddCommand(missing []string) string {
	commands := make([]string, 0, len(missing))
	for _, name := range missing {
		commands = append(commands, fmt.Sprintf("security add-generic-password -s %s -a %s -w", name, setupKeychainAccount))
	}
	return strings.Join(commands, " && ")
}

// describeSecret says which of the two tokens is being asked for, in the terms
// Slack's own screens use, so an operator with two long strings in front of them
// can tell which one goes where.
func describeSecret(name, bot string) string {
	if name == bot {
		return "bot user OAuth token, which starts xoxb-"
	}
	return "app-level token, which starts xapp-"
}

// slackEnvFileStep says what the environment file that is there actually holds.
// It is handed off rather than skipped when a name is missing, because what is
// left is somebody editing one file, and the step carries the command that opens
// it. No value is read out of the file and none reaches the step: this report is
// printed, and `--json` puts it somewhere that keeps it.
func (s *setup) slackEnvFileStep(file string, productID domain.ProductID) setupStep {
	contents, err := os.ReadFile(file)
	if err != nil {
		return setupStep{
			Step:    stepSlackSecrets,
			Status:  setupHandedOff,
			Summary: "this project's Slack secrets file could not be read, so whether the secrets are stored is unknown",
			Detail:  err.Error(),
			Remedy:  fmt.Sprintf("ls -l %s", shellQuote(file)),
		}
	}
	if missing := slack.UnassignedTokenVariables(contents); len(missing) > 0 {
		return setupStep{
			Step:    stepSlackSecrets,
			Status:  setupHandedOff,
			Summary: fmt.Sprintf("this project's Slack secrets file assigns no %s", strings.Join(missing, " and no ")),
			Detail:  fmt.Sprintf("%s is there, and a sink launched from it refuses to start for want of the names it does not assign", file),
			Remedy:  fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(file)),
		}
	}
	return setupStep{
		Step:    stepSlackSecrets,
		Status:  setupAlready,
		Summary: fmt.Sprintf("this project's Slack secrets are already stored for %s", productID),
		Detail:  file,
	}
}

func (s *setup) namespacedEnvFile(productID domain.ProductID) (string, bool) {
	path, err := s.namespacedEnvFilePath(productID)
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		return path, false
	}
	return path, true
}

// namespacedEnvFilePath is the documented path and only that, for the reason
// doctor reads only that one: a check reading a directory the document never
// named would tell an operator who followed it exactly that their secrets are
// not stored.
func (s *setup) namespacedEnvFilePath(productID domain.ProductID) (string, error) {
	home, err := s.homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "yoyo", string(productID), "slack.env"), nil
}

func (s *setup) envFilePaths(productID domain.ProductID) (directory, file string) {
	if path, err := s.namespacedEnvFilePath(productID); err == nil {
		return shellQuote(filepath.Dir(path)), shellQuote(path)
	}
	base := `"$HOME/.config/yoyo/` + string(productID)
	return base + `"`, base + `/slack.env"`
}

// ensureLaunchAgent installs the per-user launchd agent that starts the
// product's supervisor with the machine and starts it again if it dies. It is
// the one step here that leaves something running: once it is loaded, the
// product is up, and `yoyo start` is never typed on this machine again.
//
// The agent is a property list this walk renders whole, so it is compared whole
// apart from its PATH: one on disk that reads as this would write it is already
// installed, and one that reads differently — an older binary path, a moved
// checkout — is replaced with the question asked, because a job pointing at a
// binary that is not there starts nothing with the machine. A PATH-only
// difference is not a replacement (launchd.Same says why). The job is loaded
// where it is not, and reloaded where the file changed, which restarts the
// supervisor and leaves its children running to be reattached.
func (s *setup) ensureLaunchAgent(ctx context.Context, resolved config.Resolved) setupStep {
	if s.executable == nil {
		return setupStep{}
	}
	productID := resolved.Config.Product.ID
	if s.goos != "darwin" {
		return setupStep{
			Step:    stepLaunchAgent,
			Status:  setupHandedOff,
			Summary: fmt.Sprintf("this platform has no launchd, so nothing starts %s with the machine; start it by hand after each restart", productID),
			Detail:  "a systemd user unit is the equivalent on Linux, and this harness does not write one",
			Remedy:  "yoyo start",
		}
	}
	agent := launchd.AgentFor(productID, launchd.Controller{Runner: s.runner, UserHomeDir: s.homeDir, Getuid: s.getuid})
	program, err := s.executable()
	if err == nil && agent.Path == "" {
		err = errors.New("the home directory could not be resolved")
	}
	if err != nil {
		return setupStep{Step: stepLaunchAgent, Status: setupHandedOff, Summary: "the launch agent could not be described, so it was not written", Detail: err.Error(), Remedy: "yoyo start"}
	}
	// Resolved as every verb resolves it, so the agent's log is the one
	// `yoyo start` names.
	root, err := productStateRootFrom(resolved, s.getenv, s.homeDir, s.goos)
	if err != nil {
		return setupStep{Step: stepLaunchAgent, Status: setupHandedOff, Summary: "where the harness keeps its state could not be resolved, so the launch agent was not written", Detail: err.Error(), Remedy: "yoyo start"}
	}
	store, err := runstate.NewSupervisionStore(root, productID)
	if err != nil {
		return setupStep{Step: stepLaunchAgent, Status: setupHandedOff, Summary: "where the supervisor logs could not be resolved, so the launch agent was not written", Detail: err.Error(), Remedy: "yoyo start"}
	}
	logRoot, logPath := store.SupervisorLog()
	wanted := launchd.Render(launchd.Spec{
		Label:            agent.Label,
		Program:          program,
		Args:             []string{"start", "--foreground", "--config", resolved.Path},
		WorkingDirectory: config.ProjectDirectory(resolved.Path),
		Log:              filepath.Join(logRoot, filepath.FromSlash(logPath)),
		Environment:      launchd.Environment(s.environ),
	})
	installed, found, err := agent.Installed()
	if err != nil {
		return setupStep{Step: stepLaunchAgent, Status: setupHandedOff, Summary: "the launch agent on this machine could not be read", Detail: err.Error(), Remedy: "ls -l " + shellQuote(agent.Path)}
	}
	current := found && launchd.Same(installed, wanted)
	loaded, err := agent.Loaded(ctx)
	if err != nil {
		return setupStep{Step: stepLaunchAgent, Status: setupHandedOff, Summary: "whether the launch agent is loaded could not be read", Detail: err.Error(), Remedy: agent.BootstrapCommand()}
	}
	uninstall := "it is removed with: " + agent.UninstallCommand()
	if current && loaded {
		return setupStep{
			Step:    stepLaunchAgent,
			Status:  setupAlready,
			Summary: fmt.Sprintf("%s already starts with the machine: the launch agent %s runs its supervisor", productID, agent.Label),
			Detail:  agent.Path + "; " + uninstall,
		}
	}

	question := fmt.Sprintf("Install the launch agent %s, so %s starts with the machine and its supervisor is restarted if it dies?", agent.Label, productID)
	switch {
	case current:
		question = fmt.Sprintf("Load the launch agent %s, which is written and not loaded, so %s starts with the machine?", agent.Label, productID)
	case found:
		question = fmt.Sprintf("Replace the launch agent %s, which no longer reads as this binary and configuration would write it, and reload it?", agent.Label)
	}
	// Proposed yes to a person, because a product that starts with the machine
	// is what this walk is for. A walk answering itself takes it only where
	// --launch-agent asked: that walk is a script or an agent session, and a
	// resident that starts with the machine is a change to the machine somebody
	// should have asked for by name, not one a scratch walk leaves behind.
	byDefault := true
	if s.ask.defaults {
		byDefault = s.launchAgent
	}
	s.ask.say("The launch agent runs `yoyo start --foreground` from %s at every login and\nstarts it again if it dies, so %s and every part its services section\nenables come up with the machine and nothing is typed by hand.", program, productID)
	if !s.ask.confirm(question, byDefault) {
		return setupStep{
			Step:    stepLaunchAgent,
			Status:  setupSkipped,
			Summary: fmt.Sprintf("%s does not start with the machine; `yoyo start` starts it by hand", productID),
			Detail:  "the launch agent would run `yoyo start --foreground` from " + program,
			Remedy:  s.setupCommand() + " --launch-agent",
		}
	}
	if !current {
		if err := agent.Install(wanted); err != nil {
			return setupStep{Step: stepLaunchAgent, Status: setupHandedOff, Summary: "the launch agent could not be written", Detail: err.Error(), Remedy: s.setupCommand()}
		}
	}
	if loaded {
		// A loaded job reads the file it was loaded from, so a changed file is
		// taken up by unloading and loading again. The supervisor it ran exits
		// and its children survive it, to be reattached by the one that starts.
		if err := agent.Bootout(ctx); err != nil {
			return setupStep{Step: stepLaunchAgent, Status: setupHandedOff, Summary: "the launch agent was rewritten and could not be unloaded to take the change up", Detail: err.Error(), Remedy: agent.UninstallCommand() + " && " + s.setupCommand()}
		}
	}
	if err := agent.Bootstrap(ctx); err != nil {
		return setupStep{Step: stepLaunchAgent, Status: setupHandedOff, Summary: "the launch agent is written and could not be loaded", Detail: err.Error(), Remedy: agent.BootstrapCommand()}
	}
	detail := fmt.Sprintf("%s runs `yoyo start --foreground` from %s, now and at every login; %s", agent.Path, program, uninstall)
	if running, err := store.Running(); err == nil && running && !loaded {
		detail += "; a supervisor started by hand is running, so the agent's is refused and exits until that one stops, and from the next login the agent's is the one"
	}
	return setupStep{
		Step:    stepLaunchAgent,
		Status:  setupDone,
		Summary: fmt.Sprintf("%s now starts with the machine: the launch agent %s runs its supervisor", productID, agent.Label),
		Detail:  detail,
	}
}

// setupCommand is how this walk is resumed. It names the directory only when
// setup was pointed somewhere other than here, so the ordinary case gets the
// command an operator would actually type.
func (s *setup) setupCommand() string {
	working, err := os.Getwd()
	if err == nil && working == s.directory {
		return "yoyo setup"
	}
	return fmt.Sprintf("yoyo setup --directory %s", shellQuote(s.directory))
}

func (s *setup) run(ctx context.Context, directory, name string, args ...string) (execution.ProcessResult, error) {
	return s.runner.Run(ctx, execution.Command{
		Name:    name,
		Args:    args,
		Dir:     directory,
		Timeout: setupCommandTimeout,
	}, nil)
}

// questioner is the operator, as far as this walk is concerned. Every question
// it asks is answerable with a keystroke, because a setup that needed prose
// would be a setup somebody abandons halfway.
type questioner struct {
	reader *bufio.Reader
	out    io.Writer
	// defaults answers every question with the answer setup proposes and asks
	// nothing, which is what --yes turns on.
	defaults bool
	// closed is set once there is nothing left to read. From then on every
	// question is declined rather than defaulted: an answer nobody gave is not
	// consent, so a setup whose input ran out changes nothing further and says
	// what is left in its report.
	closed bool
}

// say puts context in front of a question. It is written where the questions
// are written and suppressed where they are suppressed, so a walk that is
// answering itself does not narrate at a machine.
func (q *questioner) say(format string, args ...any) {
	if q.defaults || q.closed {
		return
	}
	fmt.Fprintf(q.out, "\n"+format+"\n", args...)
}

func (q *questioner) confirm(question string, byDefault bool) bool {
	if q.defaults {
		return byDefault
	}
	if q.closed {
		return false
	}
	hint := "[y/N]"
	if byDefault {
		hint = "[Y/n]"
	}
	for {
		fmt.Fprintf(q.out, "%s %s ", question, hint)
		answer, err := q.reader.ReadString('\n')
		if err != nil && strings.TrimSpace(answer) == "" {
			// The newline closes the prompt line nobody answered, so a
			// transcript does not end mid-line.
			fmt.Fprintln(q.out)
			q.closed = true
			return false
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "":
			return byDefault
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		fmt.Fprintln(q.out, "  answer y or n.")
	}
}

func (q *questioner) line(question, byDefault string) string {
	if q.defaults {
		return byDefault
	}
	if q.closed {
		return ""
	}
	if byDefault != "" {
		fmt.Fprintf(q.out, "%s [%s] ", question, byDefault)
	} else {
		fmt.Fprintf(q.out, "%s ", question)
	}
	answer, err := q.reader.ReadString('\n')
	if err != nil && strings.TrimSpace(answer) == "" {
		fmt.Fprintln(q.out)
		q.closed = true
		return ""
	}
	if strings.TrimSpace(answer) == "" {
		return byDefault
	}
	return strings.TrimSpace(answer)
}

// renderSetup prints what the walk did and then what the installation is, in
// that order and separated, because they answer different questions: the steps
// are what setup was responsible for, and the diagnosis is whether work can run
// here at all.
func renderSetup(stdout io.Writer, report setupReport) {
	fmt.Fprintln(stdout)
	for _, step := range report.Steps {
		fmt.Fprintf(stdout, "%-11s %-14s %s\n", step.Status, step.Step, step.Summary)
		if step.Detail != "" {
			fmt.Fprintf(stdout, "%-11s %-14s %s\n", "", "", step.Detail)
		}
		if step.Remedy != "" {
			fmt.Fprintf(stdout, "%-11s %-14s next: %s\n", "", "", step.Remedy)
		}
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "then yoyo doctor, which is what decides whether this installation works:")
	fmt.Fprintln(stdout)
	renderDiagnosis(stdout, report.Diagnosis, false)
	if report.Diagnosis.Healthy() {
		return
	}
	// The last line is the one somebody acts on, so a setup that left work
	// undone says what to do rather than ending on a list to scroll back
	// through.
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "fix what is marked `problem` above, then run yoyo setup again -- it picks up where this left off.")
}

// enableSlackReporting turns reporting on in a configuration that already
// exists, by editing that file rather than regenerating it. A generated
// configuration is the project's own the moment it is written -- edited,
// commented, committed -- so the only safe way to add three lines to one is to
// add three lines to it.
//
// Nothing is written until the result has been loaded and validated, which is
// what makes an unusual file safe rather than merely unlikely: a `slack` key
// this cannot read leaves the file exactly as it was, and the step that called
// this hands the operator their own editor.
func enableSlackReporting(path, channel string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	rendered := renderSlackSection(string(content), channel)

	// Written beside the real file so the personas resolve from the same
	// .yoyodyne directory they will resolve from afterwards, which is the only
	// place validating this proves anything.
	temporary := filepath.Join(filepath.Dir(path), "."+config.FileName+".setup")
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(temporary, []byte(rendered), mode); err != nil {
		return err
	}
	defer os.Remove(temporary)
	if _, err := loadActivatedConfiguration(temporary); err != nil {
		return fmt.Errorf("the configuration this would write does not load: %w", err)
	}
	return os.Rename(temporary, path)
}

// renderSlackSection puts `enabled` and `channel` into the file's own slack
// block, or writes the block when there is none. It edits lines rather than
// re-encoding the document because everything else in a generated configuration
// is comments explaining what a value is for, and a round trip through a YAML
// encoder would throw all of them away.
func renderSlackSection(content, channel string) string {
	lines := strings.Split(content, "\n")
	start := -1
	for index, line := range lines {
		if strings.TrimRight(line, " \t") == "slack:" {
			start = index
			break
		}
	}
	if start < 0 {
		return writeSlackSection(lines, channel)
	}

	// The block is everything indented under the key, up to the next line at the
	// left margin. Blank lines inside it belong to it, which is what keeps a
	// spaced-out block from being cut in half.
	end := start + 1
	for end < len(lines) {
		line := lines[end]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			end++
			continue
		}
		break
	}
	edited := append([]string{}, lines[:start+1]...)
	body := setSlackKey(lines[start+1:end], "enabled", "true")
	body = setSlackKey(body, "channel", channel)
	edited = append(edited, body...)
	return strings.Join(append(edited, lines[end:]...), "\n")
}

// writeSlackSection writes the block into a file that has none live. Where
// "yoyo init" scaffolded its commented example, the block goes exactly there
// and the example goes away: a file carrying an enabled block and, above it, an
// instruction to enable one is a file arguing with itself. Anywhere else it is
// appended, which is all this ever did.
func writeSlackSection(lines []string, channel string) string {
	block := []string{
		`# Reporting into Slack, written by "yoyo setup". One thread per work item and`,
		`# one message per milestone, posted by a separate "yoyo slack" process that`,
		`# holds this project's two tokens; nothing about a run waits on it. The recipe`,
		`# is docs/slack/setup.md in the yoyodyne repository.`,
		"slack:",
		"  enabled: true",
		"  channel: " + channel,
	}
	start, end, found := commentedSlackExample(lines)
	if !found {
		return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n\n" + strings.Join(block, "\n") + "\n"
	}
	edited := append([]string{}, lines[:start]...)
	edited = append(edited, block...)
	return strings.Join(append(edited, lines[end:]...), "\n")
}

// commentedSlackExample finds the commented slack block a generated
// configuration carries, together with the paragraph above it that says to
// uncomment it, and reports the half-open range they occupy. A file whose
// commented block is not the scaffold's own shape is left alone: it is
// somebody's own comment, and this is not the place to decide it was a mistake.
func commentedSlackExample(lines []string) (int, int, bool) {
	key := -1
	for index, line := range lines {
		if commented, ok := uncomment(line); ok && strings.TrimSpace(commented) == "slack:" {
			key = index
			break
		}
	}
	if key < 0 {
		return 0, 0, false
	}
	// The paragraph explaining the block is whatever comment lines run above it,
	// which the blank line the scaffold puts before the section terminates.
	start := key
	for start > 0 {
		if _, ok := uncomment(lines[start-1]); !ok {
			break
		}
		start--
	}
	// The block itself is the commented lines indented under the key, which is
	// what separates them from the next paragraph of prose.
	end := key + 1
	for end < len(lines) {
		commented, ok := uncomment(lines[end])
		if !ok || !strings.HasPrefix(commented, "  ") {
			break
		}
		end++
	}
	return start, end, true
}

// uncomment strips one leading "#" from a comment line, keeping whatever
// followed it, and reports false for a line that is not a comment.
func uncomment(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	return strings.TrimPrefix(trimmed, "#"), true
}

// setSlackKey replaces a key inside the block, or writes it at the top of the
// block when it is not there. A commented-out key is left commented and the
// real one written above it: the comment is what the operator wrote, and this
// is not the place to decide it was a mistake.
func setSlackKey(body []string, key, value string) []string {
	for index, line := range body {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, key+":") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		body[index] = fmt.Sprintf("%s%s: %s", indent, key, value)
		return body
	}
	return append([]string{fmt.Sprintf("  %s: %s", key, value)}, body...)
}

// inDirectoryNote renders the "run this over there" note a remedy needs when
// the project is not where the operator is standing.
func inDirectoryNote(directory string) string {
	if strings.TrimSpace(directory) == "" {
		return ""
	}
	if working, err := os.Getwd(); err == nil && working == directory {
		return ""
	}
	return "   # in " + directory
}

// shellQuote makes a path safe to paste into a remedy, which is what every
// remedy here is meant to be.
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n\"'\\$`*?[]{}()|&;<>#~!") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func describeCommandFailure(result execution.ProcessResult, err error) string {
	if err != nil {
		return err.Error()
	}
	if message := singleLine(firstNonEmpty(result.Stderr, result.Stdout)); message != "" {
		return fmt.Sprintf("%s (exit %d)", message, result.ExitCode)
	}
	return fmt.Sprintf("%s (exit %d)", result.Status, result.ExitCode)
}

func printSetupUsage(writer io.Writer) {
	fmt.Fprintln(writer, strings.TrimSpace(`
Usage: yoyo setup [options]

Walk this project from a binary on PATH to an installation that can run work,
asking before each step. It initializes the tracker, writes the project its own
configuration and personas with its checks proposed from what the repository
already declares, points the tracker at this project's Git remote so the backlog
is not one per machine, writes the index every artifact home gets -- what is
filed there, who owns it, and whether you may edit one by hand -- and then offers
reporting into Slack, which is optional, because an installation without it runs
work exactly as one with it does.

Nothing here is remembered between runs, which is what makes it safe to run
again: every step looks at the installation first, a step that is already true
is reported as already true, and an interrupted setup is resumed by running it
again. Nothing already there is overwritten. A configuration that does not load
is handed back with the command to edit it, a sync remote the tracker already
holds is left pointing where it points, an artifact home's index that is there
and has stopped answering is a separate question that defaults to no, and a Slack
token already in the keychain is left alone -- one person running several
harnesses is the ordinary case, and the names carry the product so a second
install keeps its own pair.

The tokens are typed at the keychain's own prompt, so no token reaches this
program, its arguments, or your shell history -- which is also why `+"`--json`"+` leaves
that one step to a walk somebody is watching, rather than prompting at a
terminal it is not writing to.

It ends by running `+"`yoyo doctor`"+`, which is what decides whether the installation
actually works: every step setup could not carry out itself is left as a finding
with the command that fixes it, and this command's exit status is the
diagnosis's. It exits 1 when something would stop work running, and 0 otherwise.
Exit 1 is that diagnosis and never anything else, so it always comes with the
report. Anything it could not do -- a flag it does not have, a directory that is
not there, a report it could not write -- exits 2 instead, says why on standard
error, and reports nothing.

It ends, on macOS, by offering to install the launch agent that starts the
product with the machine: a per-user launchd job whose program is the supervisor
verb, `+"`yoyo start --foreground`"+`, from this binary, which then brings up every part
the services section enables and starts it again if it dies. A walk answering
itself with `+"`--yes`"+` installs it only where `+"`--launch-agent`"+` asked for it.

Options:
  --directory <path>        project directory to set up (default: here)
  --product <id>            product id (default: the project directory name)
  --slack-channel <id>      the channel reporting goes into, which also answers
                            the optional Slack walk without being asked
  --launch-agent            install the launch agent that starts the product
                            with the machine, which also answers that step yes
                            on a walk answering itself
  --yes                     answer every question setup asks with the answer it
                            proposes. The keychain's own prompt is not one of
                            them: it still waits for each token to be typed
  --json                    emit machine-readable JSON. On its own it asks
                            nothing and changes nothing, reporting what is
                            already true and what is still to be done; with
                            --yes it carries the walk out and reports it,
                            leaving the keychain step to a walk with somebody
                            behind it`))
}
