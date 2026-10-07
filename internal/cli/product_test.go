package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/maintenancejob"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/shutdown"
	"github.com/mason-bryant/yoyodyne/internal/slack"
	"github.com/mason-bryant/yoyodyne/internal/supervise"
)

// productHelperVariable is how the test below tells the process `yoyo start`
// detaches to be the harness rather than to run tests. The supervisor is a
// process of its own by design — it has to outlive the verb that started it —
// so the verb is exercised by actually detaching one, from this binary, and
// stopping it again.
const productHelperVariable = "YOYODYNE_PRODUCT_TEST_COMMAND"

func TestMain(m *testing.M) {
	if os.Getenv(productHelperVariable) == "" {
		// The harness develops itself, so this suite is run from a developer's
		// own shell as often as from the harness's check -- and that shell is
		// marked as the developer's, which the verbs that record a person's
		// decision refuse. The tests exercise those verbs as a person would, so
		// the marker is cleared here; the tests that assert the refusal set it
		// again for themselves.
		os.Unsetenv(execution.AgentRoleVariable)
		// Every CLI test and detached helper inherits a private default store.
		root, err := os.MkdirTemp("", "yoyodyne-cli-state-")
		if err != nil {
			panic(err)
		}
		if err := os.Setenv("YOYODYNE_STATE_HOME", root); err != nil {
			panic(err)
		}
		code := m.Run()
		// Direct supervisor and installer tests must use their product's own
		// store, rather than leaking records into even the suite's default.
		paths, err := filepath.Glob(filepath.Join(root, "projects", "*", "state", "config-readers", "supervisor-*.json"))
		if err != nil || len(paths) != 0 {
			fmt.Fprintf(os.Stderr, "supervisor tests leaked configuration records into the default state: %v, %v\n", paths, err)
			code = 1
		}
		// The store began as a new machine home, and every command the suite ran
		// without a home of its own wrote into it. A products/ directory in it
		// would switch the whole home to the earlier layout under every store
		// reading it (home.EarlierLayout), so a writer that made one fails here.
		if _, err := os.Stat(filepath.Join(root, "products")); !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "a new machine home acquired a products/ directory, which switches it to the earlier layout: %v\n", err)
			code = 1
		}
		os.RemoveAll(root)
		os.Exit(code)
	}
	// The process answers a stop signal exactly as the real binary does, and it
	// carries a bound of its own: a helper the test failed to stop ends itself
	// rather than running on the machine after the test is over.
	ctx, stop := shutdown.Answering(context.Background(), os.Stderr)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	code := RunContext(ctx, os.Args[1:], os.Stdout, os.Stderr, "test")
	cancel()
	stop()
	os.Exit(code)
}

// A product with every part off, so starting it exercises the supervisor and
// nothing that would spawn a scheduler or ask a keychain.
const quietProductConfig = validConfig + `services:
  slack:
    enabled: false
  dashboard:
    enabled: false
  scheduler:
    enabled: false
  maintenance:
    enabled: false
`

// One verb starts the product and one stops it; a second start while it runs
// says so and does nothing; and what is started is a process of its own, found
// again by its lease and its record rather than by anything the first verb
// kept.
func TestTheProductStartsOnceStopsOnceAndASecondStartDoesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the supervisor is detached and signalled on the Unix hosts Yoyodyne supports")
	}
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	t.Setenv(productHelperVariable, "1")
	// The real verb retires the operator's maintenance launchd job from the
	// home it runs under, so this one runs under a home of its own: a job
	// found there is none, and the machine's own is never touched.
	t.Setenv("HOME", t.TempDir())
	configPath := writeConfig(t, quietProductConfig)
	store, err := runstate.NewSupervisionStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	// Whatever the test finds, the process it started does not outlive it.
	t.Cleanup(func() {
		if recorded, found, err := store.Load(); err == nil && found && recorded.PID > 0 {
			_ = syscall.Kill(recorded.PID, syscall.SIGKILL)
		}
	})

	stdout, stderr, code := runCLI(t, "start", "--config", configPath)
	if code != 0 {
		t.Fatalf("start code = %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"started the supervisor for yoyodyne as pid", "slack: off", "dashboard: off", "scheduler: off", "maintenance: off", "yoyo stop"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("start said %q, want %q in it", stdout, want)
		}
	}
	recorded, found, err := store.Load()
	if err != nil || !found {
		t.Fatalf("Load() = %t, %v after start, want the supervisor's record", found, err)
	}
	if running, err := store.Running(); err != nil || !running {
		t.Fatalf("Running() = %t, %v after start, want the detached supervisor holding its lease", running, err)
	}

	stdout, stderr, code = runCLI(t, "start", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("second start code = %d, stderr %q", code, stderr)
	}
	var second startReport
	if err := json.Unmarshal([]byte(stdout), &second); err != nil {
		t.Fatalf("second start wrote %q, want JSON: %v", stdout, err)
	}
	if second.Started || second.PID != recorded.PID || second.Recorded == nil {
		t.Fatalf("second start = %+v, want nothing started and the running supervisor named", second)
	}
	if running, err := store.Running(); err != nil || !running {
		t.Fatalf("Running() = %t, %v after a second start, want the first supervisor still there", running, err)
	}

	// The reading `yoyo status` takes is wired to the same record.
	if sources := standingSources(configPath); sources.Supervision == nil {
		t.Error("standingSources() wired nothing to read the supervisor, so `yoyo status` cannot say a part is degraded")
	}

	stdout, stderr, code = runCLI(t, "stop", "--config", configPath)
	if code != 0 {
		t.Fatalf("stop code = %d, stderr %q stdout %q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, fmt.Sprintf("supervisor: stopped pid %d", recorded.PID)) {
		t.Errorf("stop said %q, want the supervisor stopped by pid", stdout)
	}
	if running, err := store.Running(); err != nil || running {
		t.Fatalf("Running() = %t, %v after stop, want the lease let go", running, err)
	}

	stdout, _, code = runCLI(t, "stop", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "supervisor: was not running") {
		t.Errorf("stopping a stopped product = %d %q, want it said to be not running and no failure", code, stdout)
	}
}

// recordingLauncher stands in for the detached launcher: it records what it
// was asked to start and runs a supervisor in this process against the same
// store, which is what the detached one would do.
type recordingLauncher struct {
	launched []slack.Launch
	store    *runstate.SupervisionStore
	children []supervise.Child
	cancel   context.CancelFunc
	done     chan struct{}
}

func (l *recordingLauncher) Launch(spec slack.Launch) (int, error) {
	l.launched = append(l.launched, spec)
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	l.done = make(chan struct{})
	supervisor := &supervise.Supervisor{
		Records:  l.store,
		Product:  "yoyodyne",
		Children: l.children,
		NotYet:   []supervise.NotYet{{Name: config.ServiceDashboard, Reason: "its adoption is yoyodyne-ifd.414"}},
		Off:      []config.ServiceName{config.ServiceMaintenance},
		Poll:     time.Millisecond,
		PID:      4242,
	}
	go func() {
		defer close(l.done)
		_ = supervisor.Run(ctx)
	}()
	return 4242, nil
}

type startedChild struct {
	name    config.ServiceName
	running bool
}

func (c *startedChild) Name() config.ServiceName              { return c.name }
func (c *startedChild) Running(context.Context) (bool, error) { return c.running, nil }
func (c *startedChild) Ensure(context.Context) (supervise.Ensured, error) {
	c.running = true
	return supervise.Ensured{Started: true, PID: 77, Log: "/state/" + string(c.name) + ".log"}, nil
}
func (c *startedChild) Stop(context.Context) (supervise.Stopped, error) {
	c.running = false
	return supervise.Stopped{WasRunning: true, PID: 77}, nil
}

// What `yoyo start` says is what the supervisor recorded: each part by name,
// running as the process it was started as, not yet a child where its
// adoption has not landed, off where the section leaves it off. The
// supervisor is started with the Slack variables taken out of its environment
// and told which configuration to read.
func TestStartSaysWhatTheSupervisorRecordedAboutEachPart(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	store, err := runstate.NewSupervisionStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := config.LoadResolved(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{store: store, children: []supervise.Child{
		&startedChild{name: config.ServiceSlack},
		&startedChild{name: config.ServiceScheduler},
	}}
	p := &product{
		resolved:  resolved,
		stateRoot: stateRoot,
		store:     store,
		program:   "/opt/yoyo/bin/yoyo",
		launcher:  launcher,
		environ:   []string{"HOME=/Users/mason", slack.BotTokenVariable + "=xoxb-secret", slack.AppTokenVariable + "=xapp-secret"},
		now:       time.Now,
	}
	var stdout, stderr strings.Builder
	if code := p.start(context.Background(), &stdout, &stderr, false); code != 0 {
		t.Fatalf("start code = %d, stderr %q", code, stderr.String())
	}
	defer func() {
		launcher.cancel()
		<-launcher.done
	}()
	for _, want := range []string{
		"started the supervisor for yoyodyne as pid 4242",
		"slack: running as pid 77, logging to /state/slack.log",
		"dashboard: enabled, and not yet a child of the supervisor: its adoption is yoyodyne-ifd.414",
		"scheduler: running as pid 77",
		"maintenance: off",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("start said:\n%s\nwant %q in it", stdout.String(), want)
		}
	}
	if len(launcher.launched) != 1 {
		t.Fatalf("launched %d processes, want the one supervisor", len(launcher.launched))
	}
	launch := launcher.launched[0]
	if launch.Program != "/opt/yoyo/bin/yoyo" || strings.Join(launch.Args, " ") != "start --foreground --config "+resolved.Path {
		t.Errorf("launched %s %v, want this binary as the foreground supervisor reading this configuration", launch.Program, launch.Args)
	}
	if strings.Join(launch.Env, " ") != "HOME=/Users/mason" {
		t.Errorf("supervisor environment = %v, want the Slack variables taken out and nothing else changed", launch.Env)
	}
	if launch.LogRoot != stateRoot || !strings.HasSuffix(launch.Log, "supervisor/supervisor.log") {
		t.Errorf("supervisor log = %q under %q, want it beside the supervisor's record", launch.Log, launch.LogRoot)
	}
}

// The real parts are assembled from the services section: a part that is off
// is off, the sink and the scheduler are children, and the dashboard, whose
// adoption has not landed, is named as such with the work that adopts it.
func TestTheRealPartsFollowTheServicesSection(t *testing.T) {
	t.Parallel()

	resolved, err := config.LoadResolved(writeConfig(t, validConfig+`services:
  slack:
    enabled: false
  dashboard:
    enabled: true
  scheduler:
    enabled: true
  maintenance:
    enabled: true
`))
	if err != nil {
		t.Fatal(err)
	}
	p := &product{resolved: resolved, stateRoot: t.TempDir(), program: "/opt/yoyo/bin/yoyo", launcher: slack.DetachedLauncher{}, goos: "darwin"}
	children, notYet, off, err := p.realChildren()
	if err != nil {
		t.Fatalf("realChildren() error = %v", err)
	}
	if len(children) != 1 || children[0].Name() != config.ServiceScheduler {
		t.Errorf("children = %v, want the scheduler alone", children)
	}
	// The maintenance pass is the supervisor's own rather than a process, so it
	// is neither a child nor a part waiting to be adopted.
	if len(notYet) != 1 || notYet[0].Name != config.ServiceDashboard || !strings.Contains(notYet[0].Reason, "yoyodyne-ifd.414") {
		t.Errorf("notYet = %+v, want the dashboard alone, with its adopting work named", notYet)
	}
	if len(off) != 1 || off[0] != config.ServiceSlack {
		t.Errorf("off = %v, want slack", off)
	}

	// A scheduler that is not running is nothing to stop, and says so.
	stopped, err := children[0].Stop(context.Background())
	if err != nil || stopped.WasRunning {
		t.Errorf("Stop() of a scheduler that is not running = %+v, %v, want nothing stopped and no failure", stopped, err)
	}
}

// The sink cannot be started from a keychain a machine does not have, and that
// is the operator's to arrange rather than something to retry: the child is
// unstartable with the same words `yoyo slack ensure` uses.
func TestTheSinkIsUnstartableWhereThereIsNoKeychain(t *testing.T) {
	t.Parallel()

	store, err := slack.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	ensured, err := slackChild{store: store, goos: "linux"}.Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if !strings.Contains(ensured.Unstartable, "macOS keychain") || !strings.Contains(ensured.Unstartable, "slack.env") {
		t.Errorf("Unstartable = %q, want the keychain named and the environment file offered", ensured.Unstartable)
	}
}

// Both verbs are in the command list and the help, because they are what an
// operator reaches for first.
func TestStartAndStopAreListedAmongTheCommands(t *testing.T) {
	t.Parallel()

	var usage strings.Builder
	printUsage(&usage)
	for _, want := range []string{"  start ", "  stop "} {
		if !strings.Contains(usage.String(), want) {
			t.Errorf("usage does not list %q", strings.TrimSpace(want))
		}
	}
	var start, stop strings.Builder
	printStartUsage(&start)
	printStopUsage(&stop)
	for _, want := range []string{"yoyo slack ensure", "yoyo work --watch", "degraded", "--foreground", "second start"} {
		if !strings.Contains(start.String(), want) {
			t.Errorf("start usage does not say %q", want)
		}
	}
	for _, want := range []string{"reverse", "cancels the runs", "yoyo pause"} {
		if !strings.Contains(stop.String(), want) {
			t.Errorf("stop usage does not say %q", want)
		}
	}
}

// machineRunner is the machine the supervisor is installed on: launchd holding
// the operator's maintenance job until it is booted out, and a checkout whose
// branch has landed something the product's binary is made of. It records the
// build it is asked for, and ends the supervisor once it has one.
type machineRunner struct {
	mu         sync.Mutex
	loaded     bool
	loadedFrom string
	asked      []string
	builds     []execution.Command
	built      func()
}

func (m *machineRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	joined := strings.Join(append([]string{command.Name}, command.Args...), " ")
	m.asked = append(m.asked, joined)
	succeeded := execution.ProcessResult{Status: execution.ProcessSucceeded}
	switch {
	case joined == "launchctl print gui/501/com.yoyodyne.maintenance":
		if !m.loaded {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 113}, nil
		}
		succeeded.Stdout = "gui/501/com.yoyodyne.maintenance = {\n\tpath = " + m.loadedFrom + "\n}\n"
		return succeeded, nil
	case joined == "launchctl bootout gui/501/com.yoyodyne.maintenance":
		m.loaded = false
		return succeeded, nil
	case command.Name == "make":
		m.builds = append(m.builds, command)
		if m.built != nil {
			m.built()
		}
		return succeeded, nil
	case strings.Contains(joined, "symbolic-ref"):
		succeeded.Stdout = "main\n"
		return succeeded, nil
	case strings.Contains(joined, "rev-parse"):
		succeeded.Stdout = "b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0\n"
		return succeeded, nil
	case strings.Contains(joined, " status "):
		return succeeded, nil
	}
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "not a command this machine answers"}, nil
}

// The supervisor installed over the operator's loaded maintenance job retires
// it — booted out of launchd, its property list removed, what it was recorded —
// and carries the one duty of it the product still needs: the target branch
// having landed, the supervisor rebuilds the product's binary in its checkout.
func TestTheSupervisorInstalledOverTheMaintenanceJobRetiresItAndCarriesTheRebuild(t *testing.T) {
	t.Parallel()

	// The checkout the product's binary is built from, with the job installed
	// under a home of its own.
	configPath := writeConfig(t, quietProductConfig)
	checkout := config.ProjectDirectory(configPath)
	for _, directory := range []string{filepath.Join(checkout, "cmd", "yoyo"), filepath.Join(checkout, "bin")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(checkout, "Makefile"), []byte("build:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", maintenancejob.Label+".plist")
	script := filepath.Join(home, ".local", "yoyodyne", "yoyodyne-maintenance.sh")
	for path, content := range map[string]string{
		plist:  "<plist version=\"1.0\"><dict><key>Label</key><string>" + maintenancejob.Label + "</string><key>ProgramArguments</key><array><string>" + script + "</string></array></dict></plist>\n",
		script: "#!/bin/bash\nmake build\nnohup bin/yoyo work --watch &\nbin/yoyo slack ensure\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resolved, err := config.LoadResolved(configPath)
	if err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	store, err := runstate.NewSupervisionStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	machine := &machineRunner{loaded: true, loadedFrom: plist, built: cancel}
	p := &product{
		resolved:  resolved,
		stateRoot: stateRoot,
		store:     store,
		program:   filepath.Join(checkout, "bin", "yoyo"),
		environ:   []string{"HOME=" + home, slack.BotTokenVariable + "=xoxb-secret"},
		goos:      "darwin",
		now:       time.Now,
		machine:   maintenancejob.Machine{Home: home, UID: 501, GOOS: "darwin", Runner: machine},
		runner:    machine,
		children: func() ([]supervise.Child, []supervise.NotYet, []config.ServiceName, error) {
			return nil, nil, config.ServiceNames, nil
		},
	}

	t.Cleanup(func() {
		readers, err := runstate.NewConfigReaderStore(stateRoot, resolved.Config.Product.ID)
		if err != nil {
			t.Fatal(err)
		}
		live, err := readers.Running()
		if err != nil || len(live) != 1 || live[0].ConfigPath != resolved.Path {
			t.Errorf("supervisor did not record its actual configuration in its own store: %+v, %v", live, err)
		}
	})

	var stdout, stderr strings.Builder
	if code := p.supervise(ctx, &stdout, &stderr); code != 0 {
		t.Fatalf("supervise code = %d, stderr %q", code, stderr.String())
	}

	// The job is gone: unloaded, its property list removed, its script left.
	machine.mu.Lock()
	loaded, builds := machine.loaded, machine.builds
	machine.mu.Unlock()
	if loaded {
		t.Error("the maintenance job is still loaded after the supervisor was installed")
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Errorf("the job's property list is still installed: %v", err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Errorf("the job's script, the operator's file, was not left: %v", err)
	}
	if !strings.Contains(stdout.String(), "retired the operator's maintenance job com.yoyodyne.maintenance") {
		t.Errorf("the supervisor said:\n%s\nwant the retirement said", stdout.String())
	}
	retired, err := store.RetiredJobs()
	if err != nil || len(retired) != 1 {
		t.Fatalf("RetiredJobs() = %+v, %v, want the one retirement recorded", retired, err)
	}
	record := retired[0]
	if !record.WasLoaded || !record.Unloaded || !record.Removed || record.By != "the supervisor" || !strings.Contains(record.PlistContent, script) {
		t.Errorf("recorded %+v, want the job unloaded and removed by the supervisor, with its property list kept", record)
	}
	if !slices.Equal(record.Duplicated, []string{"scheduler", "slack", "rebuild"}) {
		t.Errorf("recorded duplicates %v, want what its script ran", record.Duplicated)
	}

	// And the rebuild is the supervisor's: the branch has landed, and the
	// product's binary is built again in its checkout, without a Slack token.
	if len(builds) != 1 {
		t.Fatalf("built %d times, want the supervisor to rebuild the binary once", len(builds))
	}
	build := builds[0]
	if build.Dir != checkout || strings.Join(build.Args, " ") != "build BINARY="+filepath.Join(checkout, "bin", "yoyo") {
		t.Errorf("build = make %v in %s, want the product's binary built in its checkout", build.Args, build.Dir)
	}
	if slices.ContainsFunc(build.Env, func(entry string) bool { return strings.HasPrefix(entry, slack.BotTokenVariable) }) {
		t.Errorf("the build ran with a Slack token in its environment: %v", build.Env)
	}

	// Installing again finds nothing to retire.
	if said, problem := p.retireMaintenanceJob(context.Background(), "yoyo start"); said != "" || problem != "" {
		t.Errorf("a second retirement = %q, %q, want nothing to do", said, problem)
	}
}

// The real children say which build they run from their own records, and the
// sink says it is busy while it holds a conversation for a turn, which is what
// a restart into a deployed build waits out.
func TestTheRealChildrenSayWhichBuildTheyRunAndWhatTheyAreDoing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := slack.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("slack.NewStore() error = %v", err)
	}
	conversations, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	if err := store.SavePresence(slack.Presence{PID: os.Getpid(), Build: "1111111111111111"}); err != nil {
		t.Fatalf("SavePresence() error = %v", err)
	}
	sink := slackChild{store: store, conversations: conversations}
	if build, err := sink.Build(context.Background()); err != nil || build != "1111111111111111" {
		t.Fatalf("sink Build() = %q, %v, want the build its presence records", build, err)
	}
	if busy, err := sink.Busy(context.Background()); err != nil || busy != "" {
		t.Fatalf("sink Busy() = %q, %v, want nothing while it holds no conversation", busy, err)
	}
	held, err := conversations.Hold(runstate.ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer held.Release()
	if busy, err := sink.Busy(context.Background()); err != nil || !strings.Contains(busy, "product-manager conversation") {
		t.Fatalf("sink Busy() = %q, %v, want the turn it is answering named", busy, err)
	}

	watch, err := runstate.NewWatchStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	lease, taken, err := watch.Lease("watch-0123456789abcdef0123456789abcdef")
	if err != nil || !taken {
		t.Fatalf("Lease() = %t, %v, want the watch taken", taken, err)
	}
	defer lease.Release()
	scheduler := schedulerChild{watch: watch}
	if build, err := scheduler.Build(context.Background()); err != nil || build != buildinfo.Commit() {
		t.Fatalf("scheduler Build() = %q, %v, want the build the holder stamped, %q", build, err, buildinfo.Commit())
	}
	var restartsItself supervise.Child = scheduler
	if _, ok := restartsItself.(supervise.RestartsItself); !ok {
		t.Fatal("the scheduler does not say it restarts itself, so the supervisor would stop it and cancel its runs")
	}
	if _, ok := restartsItself.(supervise.Deployable); !ok {
		t.Fatal("the scheduler is not a part whose build the supervisor reads")
	}
	var sinkChild supervise.Child = sink
	if _, ok := sinkChild.(supervise.RestartsItself); ok {
		t.Fatal("the sink says it restarts itself, so nothing would move it onto a deployed build")
	}
}

// Every part the supervisor starts has to say which build it runs, or a deploy
// never reaches it — which is how the dashboard served a day and a half from a
// stale build. So a part adopted as a child (the dashboard's adoption,
// yoyodyne-ifd.414, first among them) cannot land without it: this fails for any
// child that does not.
func TestEveryPartTheSupervisorStartsIsMovedOntoADeployedBuild(t *testing.T) {
	t.Parallel()

	resolved, err := config.LoadResolved(writeConfig(t, validConfig+`slack:
  enabled: true
  channel: C0123456789
services:
  slack:
    enabled: true
  dashboard:
    enabled: true
  scheduler:
    enabled: true
  maintenance:
    enabled: true
`))
	if err != nil {
		t.Fatal(err)
	}
	p := &product{resolved: resolved, stateRoot: t.TempDir(), program: "/opt/yoyo/bin/yoyo", launcher: slack.DetachedLauncher{}, goos: "darwin"}
	children, _, _, err := p.realChildren()
	if err != nil {
		t.Fatalf("realChildren() error = %v", err)
	}
	if len(children) == 0 {
		t.Fatal("realChildren() started nothing with every part enabled")
	}
	for _, child := range children {
		if _, ok := child.(supervise.Deployable); !ok {
			t.Errorf("the %s service is not a supervise.Deployable: it does not say which build it runs or what it is in the middle of, so no deploy would ever move it", child.Name())
		}
		_, self := child.(supervise.RestartsItself)
		_, passes := child.(supervise.Passes)
		if child.Name() == config.ServiceSlack && !passes {
			t.Error("the slack service cannot be held between passes, so a restart into a deployed build could land in the middle of one")
		}
		if child.Name() == config.ServiceScheduler && !self {
			t.Error("the scheduler does not say it restarts itself, so the supervisor would stop it and cancel its runs")
		}
	}
}

// A foreground supervisor refused because another holds the product's lease
// exits cleanly: the launch agent restarts the supervisor only on an
// unsuccessful exit, and a refusal read as one would start another every few
// seconds for as long as the first one ran.
func TestAForegroundSupervisorRefusedByAnotherExitsCleanly(t *testing.T) {
	t.Parallel()

	resolved, err := config.LoadResolved(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	store, err := runstate.NewSupervisionStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, held, err := store.Lease()
	if err != nil || !held {
		t.Fatalf("Lease() = %t, %v, want the lease for the first supervisor", held, err)
	}
	defer lease.Release()
	p := &product{
		resolved:  resolved,
		stateRoot: stateRoot,
		store:     store,
		program:   "/opt/yoyo/bin/yoyo",
		goos:      "darwin",
		now:       time.Now,
		children: func() ([]supervise.Child, []supervise.NotYet, []config.ServiceName, error) {
			return nil, nil, config.ServiceNames, nil
		},
	}
	t.Cleanup(func() {
		readers, err := runstate.NewConfigReaderStore(stateRoot, resolved.Config.Product.ID)
		if err != nil {
			t.Fatal(err)
		}
		live, err := readers.Running()
		if err != nil || len(live) != 1 || live[0].ConfigPath != resolved.Path {
			t.Errorf("supervisor did not record its actual configuration in its own store: %+v, %v", live, err)
		}
	})

	var stdout, stderr strings.Builder
	if code := p.supervise(context.Background(), &stdout, &stderr); code != 0 {
		t.Errorf("supervise code = %d, want 0 for a refusal", code)
	}
	if !strings.Contains(stderr.String(), "start refused") {
		t.Errorf("stderr = %q, want the refusal said", stderr.String())
	}
}
