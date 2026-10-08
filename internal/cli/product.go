package cli

// `yoyo start` and `yoyo stop`: the product started once, and stopped once.
//
// Before this, Slack, the scheduler, and the dashboard were each a verb an
// operator ran and left running, and a machine that rebooted was a machine
// where each of them had to be started again by hand. The operator's direction
// of 2026-09-19 was that they are parts of one product: declared in its
// configuration, started together, stopped together. This is the verb that
// does it. `yoyo start` starts the product's supervisor, which reads the
// services section and starts every enabled part through the same lease-
// checked, repeat-safe path each part already had — `yoyo slack ensure` is
// the pattern, and the scheduler is started as `yoyo work --watch` under its
// own watch lease — and keeps each running within the supervisor's bounds.
// `yoyo stop` stops the supervisor first, so nothing restarts a part on its
// way down, and then the parts in reverse order.
//
// The supervisor is one verb rather than two: `yoyo start` detaches a
// `yoyo start --foreground` into a session of its own and returns, and the
// foreground form is the resident — what the launch agent `yoyo setup`
// installs runs at every login, and what `yoyo start` asks that agent to start
// where it is loaded. The resident also takes the periodic maintenance pass
// (internal/maintain), holds every restart it chooses to make while the
// provider cannot be reached or is not logged in, and re-executes itself into
// a build deployed over it. What the supervisor holds is exactly what it needs and
// no more: it never has a Slack token, because the sink's launch reads the
// pair out of the keychain into the sink's own environment and nowhere else,
// and every other child is started with the Slack variables taken out.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/launchd"
	"github.com/mason-bryant/yoyodyne/internal/maintain"
	"github.com/mason-bryant/yoyodyne/internal/maintenancejob"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/redeploy"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
	"github.com/mason-bryant/yoyodyne/internal/supervise"
	"github.com/mason-bryant/yoyodyne/internal/watchdog"
)

// startWait bounds how long `yoyo start` waits for the detached supervisor to
// record what it started before reporting. The supervisor writes its record on
// its first look, which is immediate, but the sink's start asks the keychain
// and a keychain that prompts can take a while; past this the verb says the
// supervisor is still bringing the parts up and where to read how they stand.
const startWait = 15 * time.Second

// schedulerLogFile is where the supervised scheduler says what it is doing,
// beside the watch log it writes its transitions to.
const schedulerLogFile = "scheduler.log"

// product is one product as the two verbs act on it: its configuration, its
// supervisor's state, and what it takes to start and stop each part. It is
// assembled from the real environment by openProduct and from fakes by the
// tests.
type product struct {
	resolved  config.Resolved
	stateRoot string
	store     *runstate.SupervisionStore
	// program is the binary the supervisor and its children are started from —
	// this one, so what comes up is the build that started it.
	program  string
	launcher slack.Launcher
	environ  []string
	goos     string
	// children assembles the parts to drive, in start order, with the enabled
	// parts nothing can start yet and the parts that are off.
	children func() ([]supervise.Child, []supervise.NotYet, []config.ServiceName, error)
	now      func() time.Time
	// machine is where the operator's maintenance launchd job is looked for
	// and retired from, and runner what the supervisor's rebuild runs Git and
	// the build with. Both are empty in a product assembled by a test that is
	// not about them, and an empty machine finds no job.
	machine maintenancejob.Machine
	runner  execution.ProcessRunner
	// running resolves the binary this process is executing, for re-executing
	// into a deployed build, and revision the build it was made from. Both are
	// nil in a product assembled by a test, which re-executes nothing and reads
	// the build this test binary carries.
	running  func() (*redeploy.Binary, error)
	revision func() string
	// agent is this product's launch agent on a machine with launchd, and nil
	// elsewhere and in a product assembled by a test.
	agent *launchd.Agent
}

func runStart(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	foreground := flags.Bool("foreground", false, "run the supervisor in this process rather than detaching it")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "start does not accept positional arguments")
		printStartUsage(stderr)
		return 2
	}
	p, err := openProduct(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "start failed: %v\n", err)
		return 1
	}
	if *foreground {
		return p.supervise(ctx, stdout, stderr)
	}
	return p.start(ctx, stdout, stderr, *jsonOutput)
}

func runStop(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("stop", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "stop does not accept positional arguments")
		printStopUsage(stderr)
		return 2
	}
	p, err := openProduct(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "stop failed: %v\n", err)
		return 1
	}
	return p.stop(ctx, stdout, stderr, *jsonOutput)
}

// openProduct assembles the product from the configuration and the machine.
func openProduct(configPath string) (*product, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, err
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return nil, err
	}
	store, err := runstate.NewSupervisionStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return nil, err
	}
	// Started from the binary that is starting it rather than from whatever
	// `yoyo` is on the PATH, for the reason the sink is: what comes up is the
	// build that noticed it was needed.
	program, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// A home that cannot be found is a job that cannot be looked for, which the
	// retirement says rather than refusing to start the product over.
	home, _ := os.UserHomeDir()
	p := &product{
		resolved:  resolved,
		stateRoot: stateRoot,
		store:     store,
		program:   program,
		launcher:  slack.DetachedLauncher{},
		environ:   os.Environ(),
		goos:      runtime.GOOS,
		now:       time.Now,
		machine: maintenancejob.Machine{
			Home:   home,
			UID:    os.Getuid(),
			GOOS:   runtime.GOOS,
			Runner: execution.OSProcessRunner{},
		},
		runner:  execution.OSProcessRunner{},
		running: redeploy.Running,
	}
	if runtime.GOOS == "darwin" {
		p.agent = launchd.AgentFor(resolved.Config.Product.ID, launchd.Controller{Runner: execution.OSProcessRunner{}, UserHomeDir: os.UserHomeDir, Getuid: os.Getuid})
	}
	p.children = p.realChildren
	return p, nil
}

// startReport is what `yoyo start` says: whether it started a supervisor or
// found one, and what the supervisor recorded about the parts.
type startReport struct {
	Product domain.ProductID `json:"product"`
	// Started reports that this invocation started the supervisor. False is a
	// product that was already running, which is the ordinary answer to a
	// second start and not a failure.
	Started bool   `json:"started"`
	PID     int    `json:"pid,omitempty"`
	Log     string `json:"log,omitempty"`
	// Recorded is the supervisor's record where one was read in time, and
	// Pending says why it was not.
	Recorded *runstate.Supervision `json:"recorded,omitempty"`
	Pending  string                `json:"pending,omitempty"`
	// Through names the launch agent the supervisor was started through, where
	// it was, rather than detached by this command.
	Through string `json:"through,omitempty"`
	// Retired says the operator's maintenance launchd job was retired as the
	// supervisor was installed, and RetireProblem why it could not be.
	Retired       string `json:"retired,omitempty"`
	RetireProblem string `json:"retire_problem,omitempty"`
}

// start starts the supervisor detached, unless one is running, and reports
// what the supervisor then recorded about the parts.
func (p *product) start(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool) int {
	productID := p.resolved.Config.Product.ID
	// Installing the supervisor retires the job that managed the parts before
	// it, whether or not a supervisor is already running: a second manager of
	// the same processes is what `yoyo doctor` sends the operator here to end.
	retired, retireProblem := p.retireMaintenanceJob(ctx, "yoyo start")
	running, err := p.store.Running()
	if err != nil {
		fmt.Fprintf(stderr, "start failed: %v\n", err)
		return 1
	}
	if running {
		// A second start while one is running says so and does nothing. What
		// it says is what the running supervisor recorded, because that is what
		// the person typing it wanted to know.
		report := startReport{Product: productID, Started: false, Retired: retired, RetireProblem: retireProblem}
		if recorded, found, err := p.store.Load(); err != nil {
			report.Pending = fmt.Sprintf("its record could not be read: %v", err)
		} else if found {
			report.Recorded = &recorded
			report.PID = recorded.PID
		} else {
			report.Pending = "it has not recorded the parts yet"
		}
		return p.reportStart(stdout, stderr, jsonOutput, report)
	}
	logRoot, logPath := p.store.SupervisorLog()
	// Where the launch agent `yoyo setup` installs is loaded for this checkout,
	// the supervisor is started through it rather than beside it: launchd then
	// restarts it if it dies, and the machine has one way the product is
	// launched rather than two.
	if started, handled := p.startThroughAgent(ctx, stdout, stderr, jsonOutput, logRoot, logPath, retired, retireProblem); handled {
		return started
	}
	pid, err := p.launcher.Launch(slack.Launch{
		Program: p.program,
		Args:    []string{"start", "--foreground", "--config", p.resolved.Path},
		Dir:     config.ProjectDirectory(p.resolved.Path),
		// The Slack variables are taken out whatever the shell had exported:
		// the supervisor never holds a token, and the sink's launch puts this
		// product's own pair into the sink's environment and nowhere else.
		Env:     slack.WithoutSecrets(p.environ),
		LogRoot: logRoot,
		Log:     logPath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "start failed: start the supervisor for %s: %v\n", productID, err)
		return 1
	}
	report := startReport{
		Product:       productID,
		Started:       true,
		PID:           pid,
		Log:           filepath.Join(logRoot, filepath.FromSlash(logPath)),
		Retired:       retired,
		RetireProblem: retireProblem,
	}
	// The supervisor records the parts on its first look. Waiting for that is
	// what lets this verb say what came up rather than only that something was
	// started to find out.
	recorded, found := p.awaitRecord(ctx, pid)
	if found {
		report.Recorded = &recorded
	} else {
		report.Pending = "the supervisor is still bringing the parts up; `yoyo status` says how they stand, and its log says what it is doing"
	}
	return p.reportStart(stdout, stderr, jsonOutput, report)
}

// startThroughAgent kickstarts the product's launch agent where it is loaded
// and runs this checkout's configuration, and reports whether it did. An agent
// installed for another checkout of the same product is left alone: starting
// it would start somebody else's supervisor under this command's name.
func (p *product) startThroughAgent(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool, logRoot, logPath, retired, retireProblem string) (int, bool) {
	if p.agent == nil {
		return 0, false
	}
	runs, err := p.agent.Runs(p.resolved.Path)
	if err != nil || !runs {
		return 0, false
	}
	loaded, err := p.agent.Loaded(ctx)
	if err != nil || !loaded {
		return 0, false
	}
	asked := p.now()
	if err := p.agent.Kickstart(ctx); err != nil {
		fmt.Fprintf(stderr, "start failed: start the supervisor for %s through its launch agent %s: %v\n", p.resolved.Config.Product.ID, p.agent.Label, err)
		return 1, true
	}
	report := startReport{
		Product:       p.resolved.Config.Product.ID,
		Started:       true,
		Through:       p.agent.Label,
		Log:           filepath.Join(logRoot, filepath.FromSlash(logPath)),
		Retired:       retired,
		RetireProblem: retireProblem,
	}
	recorded, found := p.awaitRecordSince(ctx, asked)
	if found {
		report.Recorded = &recorded
		report.PID = recorded.PID
	} else {
		report.Pending = fmt.Sprintf("the launch agent %s was asked to start the supervisor and it has not recorded the parts yet; `yoyo status` says how they stand, and its log says what it is doing", p.agent.Label)
	}
	return p.reportStart(stdout, stderr, jsonOutput, report), true
}

// awaitRecordSince waits, bounded, for a supervisor started at or after a
// moment to write its record: one started by launchd, whose process this
// command never learns.
func (p *product) awaitRecordSince(ctx context.Context, since time.Time) (runstate.Supervision, bool) {
	deadline := p.now().Add(startWait)
	for {
		recorded, found, err := p.store.Load()
		if err == nil && found && !recorded.StartedAt.Before(since.Add(-time.Second)) {
			return recorded, true
		}
		if !p.now().Before(deadline) || ctx.Err() != nil {
			return runstate.Supervision{}, false
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return runstate.Supervision{}, false
		}
	}
}

// awaitRecord waits, bounded, for the supervisor just started to write its
// record. A record an earlier supervisor left is not the one being waited
// for, which is why it is matched on the process.
func (p *product) awaitRecord(ctx context.Context, pid int) (runstate.Supervision, bool) {
	deadline := p.now().Add(startWait)
	for {
		recorded, found, err := p.store.Load()
		if err == nil && found && recorded.PID == pid {
			return recorded, true
		}
		if !p.now().Before(deadline) || ctx.Err() != nil {
			return runstate.Supervision{}, false
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return runstate.Supervision{}, false
		}
	}
}

func (p *product) reportStart(stdout, stderr io.Writer, jsonOutput bool, report startReport) int {
	if jsonOutput {
		return writeJSON(stdout, stderr, report)
	}
	switch {
	case report.Started && report.Through != "" && report.PID > 0:
		fmt.Fprintf(stdout, "started the supervisor for %s through its launch agent %s, as pid %d, logging to %s\n", report.Product, report.Through, report.PID, report.Log)
	case report.Started && report.Through != "":
		fmt.Fprintf(stdout, "asked the launch agent %s to start the supervisor for %s, logging to %s\n", report.Through, report.Product, report.Log)
	case report.Started:
		fmt.Fprintf(stdout, "started the supervisor for %s as pid %d, logging to %s\n", report.Product, report.PID, report.Log)
	case report.PID > 0:
		fmt.Fprintf(stdout, "the product %s is already running: its supervisor is pid %d; nothing was started\n", report.Product, report.PID)
	default:
		fmt.Fprintf(stdout, "the product %s is already running; nothing was started\n", report.Product)
	}
	if report.Recorded != nil {
		for _, child := range report.Recorded.Children {
			fmt.Fprintf(stdout, "  %s\n", supervise.DescribeChild(child))
		}
	}
	if report.Pending != "" {
		fmt.Fprintf(stdout, "  %s\n", report.Pending)
	}
	if report.Retired != "" {
		fmt.Fprintf(stdout, "%s\n", report.Retired)
	}
	if report.RetireProblem != "" {
		fmt.Fprintf(stderr, "%s\n", report.RetireProblem)
	}
	fmt.Fprintf(stdout, "stop it with `yoyo stop`; `yoyo status` says how each part stands\n")
	return 0
}

// supervise is the foreground form: this process is the supervisor, until it
// is asked to stop. It is what `yoyo start` detaches, and what the launch agent
// runs.
func (p *product) supervise(ctx context.Context, stdout, stderr io.Writer) int {
	children, notYet, off, err := p.children()
	if err != nil {
		fmt.Fprintf(stderr, "start failed: %v\n", err)
		return 1
	}
	log := func(format string, args ...any) {
		fmt.Fprintf(stdout, "%s %s\n", p.now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
	}
	// Resolved before anything else happens, because it is the only moment the
	// file is known to be the build this process is: a deploy can write over it
	// at any time after. A platform that cannot replace a process in place has
	// none, and a build taken up there is taken up by exiting.
	self, selfErr := p.selfBinary()
	// The resident form retires the job too, so a supervisor launchd starts at
	// login ends the second manager without anybody typing `yoyo start`.
	if retired, problem := p.retireMaintenanceJob(ctx, "the supervisor"); retired != "" || problem != "" {
		log("%s", firstNonEmptyString(retired, problem))
	}
	if err := writeConfigReaderAt(p.resolved, runstate.ConfigReaderSupervisor, p.stateRoot); err != nil {
		fmt.Fprintf(stderr, "the supervisor could not record which configuration keys its build reads: %v\n", err)
	}
	hold, err := p.restartHold()
	if err != nil {
		fmt.Fprintf(stderr, "start failed: %v\n", err)
		return 1
	}
	copies, err := runstate.NewConfigReaderStore(p.stateRoot, p.resolved.Config.Product.ID)
	if err != nil {
		fmt.Fprintf(stderr, "start failed: %v\n", err)
		return 1
	}
	supervisor := &supervise.Supervisor{
		Records:     p.store,
		Product:     p.resolved.Config.Product.ID,
		Children:    children,
		NotYet:      notYet,
		Off:         off,
		Now:         p.now,
		Log:         log,
		PID:         os.Getpid(),
		Build:       p.build(),
		RestartHold: hold,
		PowerHistory: func(ctx context.Context) ([]runstate.PowerEvent, error) {
			return supervise.ReadPowerHistory(ctx, p.runner)
		},
		// The children are started from the binary that started the supervisor,
		// so that file is the build each of them is moved onto when it is
		// deployed over.
		Binary: &supervise.DeployedBinary{Path: p.program},
		// Every copy of a part records itself here as it starts, so this is where
		// the supervisor finds a copy it did not collect or did not start.
		Copies: copies,
	}
	residents, pass, err := p.residents(supervisor, log)
	if err != nil {
		fmt.Fprintf(stderr, "start failed: %v\n", err)
		return 1
	}
	supervisor.Residents = residents
	if pass != nil {
		supervisor.Scheduled = []supervise.Scheduled{pass}
	}
	err = supervisor.Run(ctx)
	var takeUp *supervise.TakeUpError
	switch {
	case errors.Is(err, supervise.ErrAlreadyRunning):
		// Exits cleanly: a refusal is the ordinary answer to a second supervisor,
		// and a launchd job that read it as a failure would start another every
		// few seconds for as long as the first one ran.
		fmt.Fprintf(stderr, "start refused: %v; a second start while one is running does nothing\n", err)
		return 0
	case errors.As(err, &takeUp):
		if self == nil {
			// Exits non-zero so the launch agent, which restarts the supervisor on
			// an unsuccessful exit, starts it again from the deployed build.
			fmt.Fprintf(stderr, "the supervisor stopped to take up the deployed build %s and cannot re-execute itself here (%v); the launch agent starts it again from that build, and without one `yoyo start` does\n", takeUp.Into, selfErr)
			return 1
		}
		log("re-executing %s to take up the deployed build", self.Path())
		if err := self.Take(self.Args()); err != nil {
			fmt.Fprintf(stderr, "start failed: the supervisor let its lease go to take up the deployed build and could not re-execute: %v; the launch agent starts it again, and without one `yoyo start` does\n", err)
		}
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "start failed: %v\n", err)
		return 1
	}
	return 0
}

// selfBinary is the binary this process is running, for re-executing into a
// deployed build. A product assembled by a test has none.
func (p *product) selfBinary() (*redeploy.Binary, error) {
	if p.running == nil {
		return nil, errors.New("this supervisor was assembled with no binary to re-execute")
	}
	return p.running()
}

// build is the revision this supervisor is running.
func (p *product) build() string {
	if p.revision != nil {
		return p.revision()
	}
	return buildinfo.Commit()
}

// restartHold is the provider guard the supervisor reads before every restart
// it chooses to make: the product's outage record, standing while the provider
// cannot be reached or is not logged in. A record that cannot be read holds too,
// because the guard is only worth anything if it fails closed.
func (p *product) restartHold() (func() string, error) {
	outages, err := runstate.NewProviderOutageStore(p.stateRoot, p.resolved.Config.Product.ID)
	if err != nil {
		return nil, err
	}
	return func() string {
		outage, standing, err := outages.Standing()
		if err != nil {
			return fmt.Sprintf("whether the provider is answering could not be read (%v)", err)
		}
		if standing {
			return outage.Says()
		}
		return ""
	}, nil
}

// stopReport is what `yoyo stop` did, in order: the supervisor, then each
// part.
type stopReport struct {
	Product    domain.ProductID      `json:"product"`
	Supervisor supervise.Stopped     `json:"supervisor"`
	Problem    string                `json:"problem,omitempty"`
	Children   []supervise.ChildStop `json:"children"`
}

// stop stops the supervisor and then every part in reverse start order. The
// supervisor goes first so nothing restarts a part as it goes down, and the
// parts are stopped whether or not a supervisor was running: a supervisor
// that died left them running, and this is what stops them.
func (p *product) stop(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool) int {
	productID := p.resolved.Config.Product.ID
	report := stopReport{Product: productID, Children: []supervise.ChildStop{}}
	log := func(format string, args ...any) {
		if !jsonOutput {
			fmt.Fprintf(stdout, format+"\n", args...)
		}
	}
	code := 0
	running, err := p.store.Running()
	switch {
	case err != nil:
		report.Problem = fmt.Sprintf("whether a supervisor is running could not be read: %v", err)
		code = 1
	case running:
		recorded, found, err := p.store.Load()
		switch {
		case err != nil:
			report.Problem = fmt.Sprintf("a supervisor is running and its record could not be read, so it could not be named to stop it: %v", err)
			code = 1
		case !found:
			report.Problem = "a supervisor is running and has not recorded which process it is, so it could not be stopped"
			code = 1
		default:
			log("stopping the supervisor, pid %d", recorded.PID)
			stopped, err := supervise.StopHolder(ctx, recorded.PID, p.store.Running)
			report.Supervisor = stopped
			if err != nil {
				report.Problem = fmt.Sprintf("stop the supervisor: %v", err)
				code = 1
			}
		}
	}
	children, _, _, err := p.children()
	if err != nil {
		report.Problem = joinProblem(report.Problem, fmt.Sprintf("the parts could not be assembled to stop them: %v", err))
		code = 1
	} else {
		stops, err := supervise.StopAll(ctx, children, log)
		report.Children = stops
		if err != nil {
			code = 1
		}
	}
	if jsonOutput {
		if written := writeJSON(stdout, stderr, report); written != 0 {
			return written
		}
		return code
	}
	switch {
	case report.Supervisor.WasRunning && report.Supervisor.Detail != "":
		fmt.Fprintf(stdout, "supervisor: asked pid %d to stop; %s\n", report.Supervisor.PID, report.Supervisor.Detail)
	case report.Supervisor.WasRunning:
		fmt.Fprintf(stdout, "supervisor: stopped pid %d\n", report.Supervisor.PID)
	case report.Problem == "":
		fmt.Fprintln(stdout, "supervisor: was not running")
	}
	for _, stop := range report.Children {
		fmt.Fprintf(stdout, "%s\n", stop.Describe())
	}
	if report.Problem != "" {
		fmt.Fprintf(stderr, "stop: %s\n", report.Problem)
	}
	return code
}

// retireMaintenanceJob retires the operator's maintenance launchd job, where
// this machine has it, and records that it did. It returns what to say about a
// retirement made, or why one could not be made; both are empty where there was
// no job. A job that could not be retired never stops the product starting: the
// supervisor still runs, and `yoyo doctor` goes on naming the job.
func (p *product) retireMaintenanceJob(ctx context.Context, by string) (said, problem string) {
	retirement, err := p.machine.Retire(ctx)
	if err != nil {
		return "", fmt.Sprintf("the operator's maintenance job %s could not be retired, so it and the supervisor both manage the product's parts until it is: %v; `yoyo doctor` names it", maintenancejob.Label, err)
	}
	if !retirement.Acted() {
		if retirement.Left != "" && retirement.Found.Loaded {
			return "", fmt.Sprintf("the maintenance job was not retired: %s", retirement.Left)
		}
		return "", ""
	}
	said = retirement.Describe()
	path, err := p.store.RecordRetiredJob(runstate.RetiredJob{
		Label:        maintenancejob.Label,
		Plist:        retirement.Found.Plist,
		PlistContent: retirement.Found.Content,
		Program:      retirement.Found.Program,
		Duplicated:   maintenancejob.DutyNames(retirement.Found.Duplicates),
		WasLoaded:    retirement.Found.Loaded,
		Unloaded:     retirement.Unloaded,
		Removed:      retirement.Removed,
		Left:         retirement.Left,
		By:           by,
		RetiredAt:    p.now().UTC(),
	})
	if err != nil {
		return said, fmt.Sprintf("the retirement of %s could not be recorded: %v", maintenancejob.Label, err)
	}
	if retirement.Left != "" {
		said += "; " + retirement.Left
	}
	return said + "; recorded in " + path, ""
}

// residents is the work the supervisor hosts beside its children: rebuilding
// the product's binary when its branch lands, for a product whose binary is
// built from its own checkout, the factory stall check, and the periodic
// maintenance pass where the services section enables it, which is returned as well so the supervisor
// records it as a part.
func (p *product) residents(host *supervise.Supervisor, log func(format string, args ...any)) ([]supervise.Resident, *maintain.Pass, error) {
	var residents []supervise.Resident
	repository := doctor.RepositoryPath(config.ProjectDirectory(p.resolved.Path), p.resolved.Config.Product.Repository)
	var rebuilder *supervise.Rebuilder
	noRebuild := "the supervisor has no process runner, so nothing is built"
	if p.runner != nil {
		if built, ok := supervise.NewRebuilder(repository, p.program, p.runner, slack.WithoutSecrets(p.environ), log); ok {
			rebuilder = built
			residents = append(residents, rebuilder)
		} else {
			noRebuild = fmt.Sprintf("the binary %s is not built from this checkout, so there is nothing here to rebuild; a release is installed by hand", p.program)
		}
	}
	if watch, ok := p.factoryWatch(log); ok {
		residents = append(residents, watch)
	}
	maintenance := p.resolved.Config.Services.Maintenance
	if !maintenance.Enabled {
		return residents, nil, nil
	}
	productID := p.resolved.Config.Product.ID
	sweeps, err := runstate.NewSweepStore(p.stateRoot, productID)
	if err != nil {
		return nil, nil, err
	}
	outages, err := runstate.NewProviderOutageStore(p.stateRoot, productID)
	if err != nil {
		return nil, nil, err
	}
	requests, err := runstate.NewRestartRequestStore(p.stateRoot, productID)
	if err != nil {
		return nil, nil, err
	}
	reports, err := runstate.NewReportStore(p.stateRoot, productID)
	if err != nil {
		return nil, nil, err
	}
	pass := &maintain.Pass{
		Product:    productID,
		Every:      maintenance.Every.Duration(),
		Program:    p.program,
		Config:     p.resolved.Path,
		Repository: repository,
		Environ:    slack.WithoutSecrets(p.environ),
		Claims:     sweeps,
		Outages:    outages,
		Requests:   requests,
		NoRebuild:  noRebuild,
		Host:       host,
		Runner:     p.runner,
		Now:        p.now,
		Log:        log,
		RecordFailures: func(ctx context.Context) error {
			return sweeps.RecordPassFailures(ctx, reports, passFailureAttribution(p.resolved.Config),
				readmodel.FactoryFlowAgent(programManagerInstances(p.resolved.Config)))
		},
	}
	// Assigned only where there is one: an interface holding a nil pointer is
	// not nil, and the pass reads a nil rebuilder as the reason above.
	if rebuilder != nil {
		pass.Rebuild = rebuilder
	}
	return append(residents, pass), pass, nil
}

// factoryWatch is the supervisor's reading of whether the factory has stalled:
// no work pulled and no recurring pass succeeding for longer than the
// configured limit. It is hosted here rather than in the watch because the
// watch's passes are the ones a stall means are failing. A store that cannot be
// built leaves it out, and says so.
func (p *product) factoryWatch(log func(format string, args ...any)) (*watchdog.FactoryWatch, bool) {
	cfg := p.resolved.Config
	runs, err := runstate.NewStore(p.stateRoot, cfg.Product.ID)
	if err != nil {
		log("the factory stall check is not running: %v", err)
		return nil, false
	}
	passes, err := runstate.NewSweepStore(p.stateRoot, cfg.Product.ID)
	if err != nil {
		log("the factory stall check is not running: %v", err)
		return nil, false
	}
	holds, err := runstate.NewOperatorHoldStore(p.stateRoot)
	if err != nil {
		log("the factory stall check is not running: %v", err)
		return nil, false
	}
	stalls, err := runstate.NewFactoryStallStore(p.stateRoot, cfg.Product.ID)
	if err != nil {
		log("the factory stall check is not running: %v", err)
		return nil, false
	}
	reports, err := runstate.NewReportStore(p.stateRoot, cfg.Product.ID)
	if err != nil {
		log("the factory stall check is not running: %v", err)
		return nil, false
	}
	return &watchdog.FactoryWatch{
		Runs:    runs,
		Passes:  passes,
		Holds:   holds,
		Stalls:  stalls,
		Reports: reports,
		Attribution: report.Attribution{
			ProductID:    cfg.Product.ID,
			RepositoryID: string(cfg.Product.RepositoryID),
			Build:        buildinfo.Commit(),
		},
		Limit: cfg.Execution.FactoryStallAfter.Duration(),
		Log:   log,
	}, true
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func joinProblem(first, second string) string {
	if first == "" {
		return second
	}
	return first + "; " + second
}

// realChildren assembles the parts from the services section: the ones this
// harness can start, in start order — the sink first so it reports the rest
// coming up, then the scheduler — and the enabled ones it cannot start yet,
// each naming the work that adopts it.
func (p *product) realChildren() ([]supervise.Child, []supervise.NotYet, []config.ServiceName, error) {
	services := p.resolved.Config.Services
	productID := p.resolved.Config.Product.ID
	var children []supervise.Child
	var notYet []supervise.NotYet
	var off []config.ServiceName
	for _, name := range config.ServiceNames {
		if !services.Enabled(name) {
			off = append(off, name)
			continue
		}
		switch name {
		case config.ServiceSlack:
			store, err := slack.NewStore(p.stateRoot, productID)
			if err != nil {
				return nil, nil, nil, err
			}
			conversations, err := runstate.NewConversationStore(p.stateRoot, productID)
			if err != nil {
				return nil, nil, nil, err
			}
			children = append(children, slackChild{
				store:         store,
				conversations: conversations,
				goos:          p.goos,
				supervisor: slack.Supervisor{
					Store:    store,
					Secrets:  slack.Keychain{Runner: execution.OSProcessRunner{}},
					Launcher: p.launcher,
					Program:  p.program,
					Config:   p.resolved.Path,
					Dir:      config.ProjectDirectory(p.resolved.Path),
					Environ:  p.environ,
				},
			})
		case config.ServiceScheduler:
			watch, err := runstate.NewWatchStore(p.stateRoot, productID)
			if err != nil {
				return nil, nil, nil, err
			}
			children = append(children, schedulerChild{
				watch:      watch,
				launcher:   p.launcher,
				program:    p.program,
				configPath: p.resolved.Path,
				dir:        config.ProjectDirectory(p.resolved.Path),
				environ:    p.environ,
				logRoot:    p.stateRoot,
				log:        home.ProductDirectoryWithin(p.stateRoot, string(productID)) + "/" + schedulerLogFile,
			})
		case config.ServiceDashboard:
			// The dashboard's adoption as a child is yoyodyne-ifd.414; until it
			// lands the command is started by hand, and the product says so
			// rather than starting a part it does not know how to.
			// The child that adoption adds has to be a supervise.Deployable —
			// saying which build it runs and which request it is serving — or no
			// deploy reaches it; TestEveryPartTheSupervisorStartsIsMovedOntoADeployedBuild
			// fails for a child that is not.
			notYet = append(notYet, supervise.NotYet{
				Name:   name,
				Reason: "its adoption is yoyodyne-ifd.414; until that lands, start it with `yoyo dashboard`",
			})
		case config.ServiceMaintenance:
			// The periodic pass is the supervisor's own rather than a process it
			// starts, so it is no child: supervise assembles it beside the
			// rebuilder, and the record shows it as a scheduled part.
		}
	}
	return children, notYet, off, nil
}

// slackChild is the sink as a part of the product. Starting it is exactly
// `yoyo slack ensure`: lease-checked, this product's own stored pair into the
// sink's environment and nowhere else, and nothing done when one is running.
type slackChild struct {
	store *slack.Store
	// conversations is where the sink's product manager turns hold their
	// conversation, which is what a restart into a deployed build waits out.
	conversations *runstate.ConversationStore
	supervisor    slack.Supervisor
	goos          string
}

func (slackChild) Name() config.ServiceName { return config.ServiceSlack }

func (c slackChild) Running(context.Context) (bool, error) { return c.store.Running() }

func (c slackChild) Ensure(ctx context.Context) (supervise.Ensured, error) {
	if c.goos != "darwin" {
		// The keychain is macOS's, as `yoyo slack ensure` says in the same
		// words; a machine without it has the sink started from its
		// environment file, which is nothing the supervisor can do for it.
		return supervise.Ensured{Unstartable: fmt.Sprintf("its tokens are read from the macOS keychain and this machine is %s; start the sink from the environment file instead: (set -a; . ~/.config/yoyo/%s/slack.env; %s=%s exec yoyo slack)",
			c.goos, c.store.Product(), slack.SecretNamespaceVariable, c.store.Product())}, nil
	}
	supervision, err := c.supervisor.Ensure(ctx)
	if err != nil {
		return supervise.Ensured{}, err
	}
	switch supervision.Outcome {
	case slack.OutcomeStarted:
		return supervise.Ensured{Started: true, PID: supervision.PID, Log: supervision.Log}, nil
	case slack.OutcomeRunning:
		return supervise.Ensured{PID: c.presencePID()}, nil
	case slack.OutcomeSecretsUnavailable:
		return supervise.Ensured{Unstartable: fmt.Sprintf("the keychain would not produce its tokens, %s: %s; `yoyo doctor` says what to store and the command that stores it",
			strings.Join(supervision.Secrets, " and "), supervision.Detail)}, nil
	case slack.OutcomeReportingOff:
		return supervise.Ensured{Unstartable: "reporting is off: " + supervision.Detail}, nil
	}
	return supervise.Ensured{}, fmt.Errorf("the sink's supervision answered %q, which is not an outcome this harness names", supervision.Outcome)
}

// presencePID is the process a running sink recorded itself as, or nothing
// where the record is absent or unreadable — the lease already answered that
// it is running, and a pid is a convenience for the reader.
func (c slackChild) presencePID() int {
	presence, found, err := c.store.LoadPresence()
	if err != nil || !found {
		return 0
	}
	return presence.PID
}

// HolderPID is the process the running sink recorded itself as.
func (c slackChild) HolderPID(context.Context) int { return c.presencePID() }

// Build is the revision the running sink recorded itself as built from.
func (c slackChild) Build(context.Context) (string, error) {
	presence, found, err := c.store.LoadPresence()
	if err != nil || !found {
		return "", err
	}
	return presence.Build, nil
}

// Busy is a product manager turn the sink is answering in a thread, which a
// restart would cut off in the middle. A pass over the records is the other
// thing a restart waits out, and HoldBetweenPasses is how.
func (c slackChild) Busy(context.Context) (string, error) {
	pid := c.presencePID()
	if pid <= 0 || c.conversations == nil {
		return "", nil
	}
	held, err := c.conversations.HeldBy(pid)
	if err != nil || len(held) == 0 {
		return "", err
	}
	return fmt.Sprintf("the turn it is answering in the %s conversation", strings.Join(held, " and ")), nil
}

// HoldBetweenPasses takes the sink's pass lease, which the sink holds for each
// pass over the records, so the sink starts no pass while the supervisor stops
// it. A lease the sink is holding is a pass under way, which the restart waits
// for: a sink stopped while a post is on its way can have the post land with its
// cursor never written, and the sink started in its place posts it again.
func (c slackChild) HoldBetweenPasses(context.Context) (func(), string, error) {
	lease, held, err := c.store.PassLease()
	if err != nil {
		return nil, "", err
	}
	if !held {
		return nil, "the pass over the records it is making", nil
	}
	return func() { _ = lease.Release() }, "", nil
}

func (c slackChild) Stop(ctx context.Context) (supervise.Stopped, error) {
	running, err := c.store.Running()
	if err != nil {
		return supervise.Stopped{}, err
	}
	if !running {
		return supervise.Stopped{}, nil
	}
	pid := c.presencePID()
	if pid <= 0 {
		return supervise.Stopped{WasRunning: true}, errors.New("a sink is running and its presence record names no process, so it could not be stopped")
	}
	return supervise.StopHolder(ctx, pid, c.store.Running)
}

// schedulerChild is the watch loop as a part of the product: `yoyo work
// --watch`, started detached under its own watch lease, with the Slack
// variables taken out of its environment.
type schedulerChild struct {
	watch      *runstate.WatchStore
	launcher   slack.Launcher
	program    string
	configPath string
	dir        string
	environ    []string
	logRoot    string
	log        string
}

func (schedulerChild) Name() config.ServiceName { return config.ServiceScheduler }

func (c schedulerChild) Running(context.Context) (bool, error) { return c.watch.Held() }

func (c schedulerChild) Ensure(context.Context) (supervise.Ensured, error) {
	held, err := c.watch.Held()
	if err != nil {
		return supervise.Ensured{}, err
	}
	if held {
		return supervise.Ensured{PID: c.holderPID()}, nil
	}
	pid, err := c.launcher.Launch(slack.Launch{
		Program: c.program,
		Args:    []string{"work", "--watch", "--config", c.configPath},
		Dir:     c.dir,
		Env:     slack.WithoutSecrets(c.environ),
		LogRoot: c.logRoot,
		Log:     c.log,
	})
	if err != nil {
		return supervise.Ensured{}, err
	}
	return supervise.Ensured{Started: true, PID: pid, Log: filepath.Join(c.logRoot, filepath.FromSlash(c.log))}, nil
}

// HolderPID is the process the watching session stamped itself as.
func (c schedulerChild) HolderPID(context.Context) int { return c.holderPID() }

func (c schedulerChild) holderPID() int {
	holder, found, err := c.watch.Holder()
	if err != nil || !found {
		return 0
	}
	return holder.PID
}

// Build is the revision the watching session stamped itself as built from,
// and, for a session from a build older than that stamp, the revision its
// latest transition carries.
func (c schedulerChild) Build(context.Context) (string, error) {
	holder, found, err := c.watch.Holder()
	if err != nil || !found {
		return "", err
	}
	if holder.Build != "" {
		return holder.Build, nil
	}
	latest, found, err := c.watch.Latest()
	if err != nil || !found || latest.SessionID != holder.SessionID {
		return "", err
	}
	return latest.Build, nil
}

// Busy is never asked of the scheduler, which restarts itself; it is here so
// the scheduler is a part whose build the supervisor reads.
func (schedulerChild) Busy(context.Context) (string, error) { return "", nil }

// RestartsItself is the watch's own redeploy: it notices the binary it was
// started from replaced, stops pulling, waits out the runs it hosts, and
// re-executes in place. Stopping it instead would cancel those runs.
func (schedulerChild) RestartsItself() string {
	return "the watch restarts itself into it between runs, waiting out the runs it hosts under execution.redeploy_drain_limit"
}

func (c schedulerChild) Stop(ctx context.Context) (supervise.Stopped, error) {
	held, err := c.watch.Held()
	if err != nil {
		return supervise.Stopped{}, err
	}
	if !held {
		return supervise.Stopped{}, nil
	}
	pid := c.holderPID()
	if pid <= 0 {
		return supervise.Stopped{WasRunning: true}, errors.New("a session is watching and did not record which process it is, so it could not be stopped")
	}
	return supervise.StopHolder(ctx, pid, c.watch.Held)
}

func printStartUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo start [--config <path>] [--foreground] [--json]

Start the product: its supervisor, and through it every part the configuration's
services section enables. A person starts the product once with this rather than
each part by hand.

The supervisor reads the services section and starts each enabled part the way
that part is started alone -- the Slack sink exactly as `+"`yoyo slack ensure`"+`
starts it, from this product's own stored tokens; the scheduler as
`+"`yoyo work --watch`"+` under its own watch lease -- so a part started here holds
exactly what it holds started by hand. It then keeps each running: a part that
dies is started again with backoff, up to five times within two minutes of a
start, and past that it is left down and shown as degraded by `+"`yoyo status`"+`
with the reason. The supervisor's own death leaves the parts running, and the
next `+"`yoyo start`"+` finds them by their leases and takes them back rather than
starting them again.

A second start while the product is running says so and does nothing. A part the
section enables that is not yet a child of the supervisor -- the dashboard -- is
reported as such, with the work that adopts it.

The maintenance pass is the supervisor's own, run as often as
services.maintenance.every says: `+"`yoyo reconcile`"+`, the restarts program managers
asked for, and taking up a build deployed over the supervisor, each pass recorded
where `+"`yoyo sweeps`"+` reads it. Nothing is restarted by choice -- a part moved onto
a deployed build, a requested restart, the supervisor re-executing itself --
while the provider cannot be reached or is not logged in.

Where the launch agent `+"`yoyo setup`"+` installs is loaded for this checkout, the
supervisor is started through it, so launchd restarts it if it dies.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --foreground      be the supervisor in this process rather than detaching one;
                    what the launch agent runs
  --json            emit machine-readable JSON`)
}

func printStopUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo stop [--config <path>] [--json]

Stop the product: the supervisor first, so nothing restarts a part on its way
down, and then every part in the reverse of the order they were started in. A
part that is not running is reported so, and the parts are stopped whether or
not a supervisor was running -- a supervisor that died left them running, and
this is what stops them.

Stopping the scheduler cancels the runs it is hosting, as stopping a watch
session always has; a run cancelled that way is settled by `+"`yoyo reconcile`"+`.
When what you want is for the runs to keep what they have and carry on later,
`+"`yoyo pause`"+` is the verb, and the product stays up.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
