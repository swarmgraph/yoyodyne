package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
	"github.com/mason-bryant/yoyodyne/internal/supervise"
)

// fakeDashboards stands in for the dashboard processes the child launches: each
// launch registers a dashboard under the start identity it was handed and
// publishes its token, as `yoyo dashboard --start-id` does, and the readiness
// request is answered by whichever of them the request's token belongs to.
type fakeDashboards struct {
	t      *testing.T
	store  *runstate.DashboardStore
	mu     sync.Mutex
	next   int
	alive  map[int]*runstate.Lease
	tokens map[string]string // start id → token
	events []string
	// tokenProblem makes the next launch record a token it could not publish,
	// and answer makes the readiness request answer as another dashboard.
	tokenProblem string
	answerAs     string
	supplied     string
	launches     []slack.Launch
	// readers, where set, is where each dashboard records itself as reading the
	// configuration, as `yoyo dashboard` does, and refuse is the processes that
	// do not stop when asked.
	readers *runstate.ConfigReaderStore
	refuse  map[int]bool
	// configPath, where set, is the configuration each dashboard records itself
	// as reading, with the port and token source it started on, as `yoyo
	// dashboard` records them.
	configPath string
}

func newFakeDashboards(t *testing.T, store *runstate.DashboardStore) *fakeDashboards {
	fakes := &fakeDashboards{t: t, store: store, next: 7000, alive: map[int]*runstate.Lease{}, tokens: map[string]string{}}
	// The leases stand for running dashboards, so they are held until the test
	// ends: a fake nothing refers to any more is collected, and collecting a
	// lease's file closes it and lets its lock go, which reads as the dashboard
	// having stopped in the middle of the test.
	t.Cleanup(func() {
		fakes.mu.Lock()
		defer fakes.mu.Unlock()
		for _, lease := range fakes.alive {
			_ = lease.Release()
		}
	})
	return fakes
}

func (f *fakeDashboards) Launch(spec slack.Launch) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launches = append(f.launches, spec)
	startID := spec.Args[slices.Index(spec.Args, "--start-id")+1]
	port, err := strconv.Atoi(spec.Args[slices.Index(spec.Args, "--port")+1])
	if err != nil {
		f.t.Fatal(err)
	}
	f.next++
	pid := f.next
	instance := runstate.DashboardInstance{StartID: startID, PID: pid, Build: "deployed-build", StartedAt: time.Now().UTC(),
		URL: "http://" + net.JoinHostPort(f.bindFor(), strconv.Itoa(port)) + "/", Port: port, ReadyURL: "http://127.0.0.1:45999/", Supervised: true}
	switch {
	case f.tokenProblem != "":
		instance.TokenProblem = f.tokenProblem
	case f.supplied == "":
		token := "generated-token-" + startID
		if _, err := f.store.PublishToken(token); err != nil {
			f.t.Fatal(err)
		}
		instance.TokenPublished = true
		f.tokens[startID] = token
	default:
		f.tokens[startID] = f.supplied
	}
	lease, err := f.store.Register(instance)
	if err != nil {
		f.t.Fatal(err)
	}
	f.alive[pid] = lease
	f.recordReader(instance)
	f.events = append(f.events, "launch "+startID)
	return pid, nil
}

// bindFor is the address a dashboard launched now listens on: the one the
// configuration names where the fakes read one, and loopback otherwise.
func (f *fakeDashboards) bindFor() string {
	if f.configPath == "" {
		return config.DefaultDashboardBind
	}
	resolved, err := config.LoadResolved(f.configPath)
	if err != nil {
		f.t.Fatal(err)
	}
	return resolved.Config.Services.Dashboard.Bind
}

// recordReader records a dashboard as reading the configuration, which is the
// record every reading of the copies of a part counts.
func (f *fakeDashboards) recordReader(instance runstate.DashboardInstance) {
	if f.readers == nil {
		return
	}
	reader := runstate.ConfigReader{Service: string(config.ServiceDashboard), PID: instance.PID, Build: instance.Build,
		ConfigPath: "/repo/.yoyodyne/config.yaml", StartedAt: instance.StartedAt, Keys: []string{"product"}}
	if f.configPath != "" {
		reader.ConfigPath = f.configPath
		// The token source and the address are what the configuration says as
		// the dashboard starts, which is where `yoyo dashboard` reads them from.
		resolved, err := config.LoadResolved(f.configPath)
		if err != nil {
			f.t.Fatal(err)
		}
		reader.Settings = map[string]string{config.SettingDashboardToken: string(resolved.Config.Services.Dashboard.Token)}
		if instance.Supervised {
			reader.Settings[config.SettingDashboardPort] = strconv.Itoa(instance.Port)
			reader.Settings[config.SettingDashboardBind] = resolved.Config.Services.Dashboard.Bind
		}
	}
	if err := f.readers.Record(reader); err != nil {
		f.t.Fatal(err)
	}
}

// alivePID is the process check the configuration readers' store asks.
func (f *fakeDashboards) alivePID(pid int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, alive := f.alive[pid]
	return alive, nil
}

// handStarted registers a dashboard somebody started by hand on the port.
func (f *fakeDashboards) handStarted(port int) runstate.DashboardInstance {
	f.mu.Lock()
	defer f.mu.Unlock()
	startID, _ := runstate.NewDashboardStartID()
	f.next++
	instance := runstate.DashboardInstance{StartID: startID, PID: f.next, StartedAt: time.Now().UTC(), URL: "http://127.0.0.1:8765/", Port: port}
	lease, err := f.store.Register(instance)
	if err != nil {
		f.t.Fatal(err)
	}
	f.alive[instance.PID] = lease
	f.recordReader(instance)
	return instance
}

func (f *fakeDashboards) look(pid int) (runstate.ProcessLook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, alive := f.alive[pid]
	return runstate.ProcessLook{Exists: alive}, nil
}

func (f *fakeDashboards) stop(_ context.Context, pid int, _ func() (bool, error)) (supervise.Stopped, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	lease, alive := f.alive[pid]
	if !alive {
		return supervise.Stopped{}, nil
	}
	if f.refuse[pid] {
		f.events = append(f.events, "refused "+strconv.Itoa(pid))
		return supervise.Stopped{WasRunning: true, PID: pid}, errors.New("it was still holding its lease when the wait ran out")
	}
	_ = lease.Release()
	delete(f.alive, pid)
	f.events = append(f.events, "stop "+strconv.Itoa(pid))
	return supervise.Stopped{WasRunning: true, PID: pid}, nil
}

// RoundTrip answers the readiness request as the dashboard whose token it
// carries.
func (f *fakeDashboards) RoundTrip(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	presented := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	for startID, token := range f.tokens {
		if token != presented || request.URL.Path != "/api/ready" {
			continue
		}
		answer := startID
		if f.answerAs != "" {
			answer = f.answerAs
		}
		f.events = append(f.events, "ready "+startID)
		body, _ := json.Marshal(dashboard.Health{Ready: true, StartID: answer})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
	}
	return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{"error":"no"}`)), Header: http.Header{}}, nil
}

func (f *fakeDashboards) said() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

type storedKeychain struct{ token string }

func (k storedKeychain) Secret(context.Context, string) (string, error) { return k.token, nil }

func newDashboardChild(t *testing.T) (dashboardChild, *fakeDashboards) {
	t.Helper()
	stateRoot := t.TempDir()
	store, err := runstate.NewDashboardStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	fakes := newFakeDashboards(t, store)
	return dashboardChild{
		store:    store,
		product:  "yoyodyne",
		launcher: fakes,
		program:  "/opt/yoyo/bin/yoyo",
		config:   "/repo/.yoyodyne/config.yaml",
		dir:      "/repo",
		environ:  []string{"HOME=/Users/mason", slack.BotTokenVariable + "=xoxb-secret"},
		logRoot:  stateRoot,
		log:      "projects/yoyodyne/state/dashboard.log",
		port:     8765,
		source:   config.DashboardTokenGenerated,
		tokens:   dashboard.TokenStores{Keychain: storedKeychain{token: "the-stored-token"}, Platform: "darwin", StateRoot: stateRoot},
		client:   &http.Client{Transport: fakes},
		wait:     300 * time.Millisecond,
		poll:     5 * time.Millisecond,
		look:     fakes.look,
		stop:     fakes.stop,
		build:    func() string { return "deployed-build" },
	}, fakes
}

// The supervisor starts the dashboard on the configured port, with a start
// identity it records as its own, and counts it started only once a request
// carrying the published token was accepted by that dashboard. Asked again,
// it reattaches to the running one and its token rather than starting a new
// one.
func TestTheDashboardIsStartedOnTheConfiguredPortAndConfirmedWithItsToken(t *testing.T) {
	t.Parallel()
	child, fakes := newDashboardChild(t)

	ensured, err := child.Ensure(context.Background())
	if err != nil || !ensured.Started || ensured.PID != 7001 {
		t.Fatalf("Ensure() = %+v, %v, want the dashboard started", ensured, err)
	}
	launch := fakes.launches[0]
	if launch.Program != "/opt/yoyo/bin/yoyo" || !strings.HasPrefix(strings.Join(launch.Args, " "), "dashboard --config /repo/.yoyodyne/config.yaml --port 8765 --start-id dashboard-") {
		t.Errorf("launched %s %v, want this binary as `yoyo dashboard` on the configured port with a start identity", launch.Program, launch.Args)
	}
	if strings.Join(launch.Env, " ") != "HOME=/Users/mason" {
		t.Errorf("dashboard environment = %v, want the Slack variables taken out", launch.Env)
	}
	start, found, err := child.store.Start()
	if err != nil || !found || start.PID != 7001 || start.Port != 8765 || start.Build != "deployed-build" {
		t.Errorf("the supervisor's record of the start = %+v, %v, %v", start, found, err)
	}
	events := fakes.said()
	if len(events) != 2 || !strings.HasPrefix(events[0], "launch ") || !strings.HasPrefix(events[1], "ready ") {
		t.Errorf("events = %v, want the launch and then the readiness request", events)
	}
	if running, err := child.Running(context.Background()); !running || err != nil {
		t.Errorf("Running() = %v, %v", running, err)
	}
	serving := child.Serving(context.Background())
	if serving.URL != "http://127.0.0.1:8765/" || serving.TokenFile != child.store.TokenPath() {
		t.Errorf("Serving() = %+v, want the URL and the token's file", serving)
	}
	token, _ := child.store.ReadToken()

	again, err := child.Ensure(context.Background())
	if err != nil || again.Started || again.PID != 7001 || len(fakes.launches) != 1 {
		t.Errorf("Ensure() over the running dashboard = %+v, %v (%d launches), want it reattached", again, err, len(fakes.launches))
	}
	if kept, _ := child.store.ReadToken(); kept != token {
		t.Error("reattaching changed the token")
	}

	stopped, err := child.Stop(context.Background())
	if err != nil || !stopped.WasRunning || stopped.PID != 7001 {
		t.Errorf("Stop() = %+v, %v", stopped, err)
	}
	if running, _ := child.Running(context.Background()); running {
		t.Error("the dashboard is still running after Stop()")
	}
}

// Moving onto a deployed build starts the new dashboard and confirms it before
// the old one is stopped, and a dashboard somebody started by hand on the same
// port is stopped with it; one on another port is left alone.
func TestADashboardIsReplacedOnlyOnceItsReplacementIsReady(t *testing.T) {
	t.Parallel()
	child, fakes := newDashboardChild(t)
	if _, err := child.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	byHand := fakes.handStarted(8765)
	elsewhere := fakes.handStarted(9999)

	replaced, err := child.Replace(context.Background())
	if err != nil || !replaced.Started || replaced.PID == 7001 {
		t.Fatalf("Replace() = %+v, %v", replaced, err)
	}
	events := fakes.said()[2:]
	readyAt := slices.IndexFunc(events, func(event string) bool { return strings.HasPrefix(event, "ready ") })
	for _, stopped := range []string{"stop 7001", "stop " + strconv.Itoa(byHand.PID)} {
		at := slices.Index(events, stopped)
		if at < 0 || at < readyAt {
			t.Errorf("events = %v, want %q after the replacement was confirmed ready", events, stopped)
		}
	}
	if slices.Contains(events, "stop "+strconv.Itoa(elsewhere.PID)) {
		t.Errorf("events = %v, the dashboard on another port was stopped", events)
	}
	if instance, running, err := child.current(); err != nil || !running || instance.PID != replaced.PID {
		t.Errorf("current() = %+v, %v, %v, want the replacement", instance, running, err)
	}
}

// A dashboard whose token could not be published is left down at once with
// the reason, and the process launched is stopped; one that the readiness
// request finds is not the dashboard started is stopped and the start counted
// as failed, and neither failure carries the token.
func TestADashboardThatCannotBeConfirmedIsNotTakenUp(t *testing.T) {
	t.Parallel()

	child, fakes := newDashboardChild(t)
	fakes.tokenProblem = "the token file /state/dashboard/token is a symbolic link, which is refused rather than followed"
	ensured, err := child.Ensure(context.Background())
	if err != nil || ensured.Started || !strings.Contains(ensured.Unstartable, "symbolic link") {
		t.Errorf("Ensure() with an unpublishable token = %+v, %v, want it left down with the reason", ensured, err)
	}
	if len(fakes.alive) != 0 {
		t.Errorf("the dashboard that could not be confirmed is still running: %v", fakes.alive)
	}

	child, fakes = newDashboardChild(t)
	fakes.answerAs = "dashboard-ffffffffffffffff"
	_, err = child.Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not confirmed ready") || !strings.Contains(err.Error(), "not the one started") {
		t.Fatalf("Ensure() answered by another dashboard = %v, want the start refused", err)
	}
	for _, token := range fakes.tokens {
		if strings.Contains(err.Error(), token) {
			t.Errorf("the refusal carried the token: %v", err)
		}
	}
	if len(fakes.alive) != 0 {
		t.Errorf("the dashboard that could not be confirmed is still running: %v", fakes.alive)
	}
	if running, _ := child.Running(context.Background()); running {
		t.Error("a dashboard never confirmed is taken up as running")
	}
}

// A token the configuration supplies is the one the readiness request carries,
// and nothing is written to the token file for it.
func TestASuppliedTokenConfirmsTheDashboardAndIsNeverPublished(t *testing.T) {
	t.Parallel()
	child, fakes := newDashboardChild(t)
	child.source = config.DashboardTokenKeychain
	fakes.supplied = "the-stored-token"

	if _, err := child.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() with a keychain token = %v", err)
	}
	if _, err := child.store.ReadToken(); !errors.Is(err, runstate.ErrNoDashboardToken) {
		t.Errorf("ReadToken() = %v, want no token file for a supplied token", err)
	}
	if serving := child.Serving(context.Background()); serving.TokenFile != "" {
		t.Errorf("Serving() named a token file %q for a supplied token", serving.TokenFile)
	}
}

// recordedSupervision is the supervisor's record as the read model asks about
// it, naming the dashboard the supervisor runs now.
type recordedSupervision struct{ dashboard int }

func (recordedSupervision) Running() (bool, error) { return true, nil }

func (r recordedSupervision) Load() (runstate.Supervision, bool, error) {
	return runstate.Supervision{PID: 4242, Children: []runstate.SupervisedChild{
		{Service: config.ServiceDashboard, State: runstate.ChildRunning, PID: r.dashboard},
	}}, true, nil
}

// copiesProblems is every entry the reading raises about more than one copy of
// a part, on the attention line and among the factory problems.
func copiesProblems(standing readmodel.Standing) []readmodel.Attention {
	var raised []readmodel.Attention
	for _, entry := range append(slices.Clone(standing.NeedsHuman), standing.FactoryProblems...) {
		if entry.Kind == readmodel.AttentionServiceCopies {
			raised = append(raised, entry)
		}
	}
	return raised
}

func dashboardReaders(t *testing.T, child dashboardChild, fakes *fakeDashboards) {
	t.Helper()
	readers, err := runstate.NewConfigReaderStore(child.logRoot, child.product)
	if err != nil {
		t.Fatal(err)
	}
	fakes.readers = readers.WithProcessCheck(fakes.alivePID)
}

func readCopies(child dashboardChild, fakes *fakeDashboards, supervised int) readmodel.Standing {
	return readmodel.ReadStanding(context.Background(), readmodel.Sources{
		Supervision:   recordedSupervision{dashboard: supervised},
		ConfigReaders: fakes.readers,
		Dashboards:    child.store,
	})
}

// The supervisor knows a dashboard it started by the start record it kept and
// not by the port it serves on: handing the dashboard to a build configured for
// another port stops the copy it started on the old port once the new one is
// ready, says that it did, forgets that start, and leaves the problems list
// naming no stray copy.
func TestHandingTheDashboardToABuildOnAnotherPortStopsTheCopyOnTheOldPort(t *testing.T) {
	t.Parallel()
	child, fakes := newDashboardChild(t)
	dashboardReaders(t, child, fakes)
	first, err := child.Ensure(context.Background())
	if err != nil || !first.Started {
		t.Fatalf("Ensure() = %+v, %v", first, err)
	}

	child.port = 8080
	replaced, err := child.Replace(context.Background())
	if err != nil || !replaced.Started || replaced.PID == first.PID {
		t.Fatalf("Replace() onto port 8080 = %+v, %v", replaced, err)
	}
	if launch := fakes.launches[1]; !slices.Contains(launch.Args, "8080") {
		t.Errorf("the replacement was launched with %v, want port 8080", launch.Args)
	}
	events := fakes.said()[2:]
	readyAt := slices.IndexFunc(events, func(event string) bool { return strings.HasPrefix(event, "ready ") })
	if at := slices.Index(events, "stop "+strconv.Itoa(first.PID)); at < 0 || at < readyAt {
		t.Errorf("events = %v, want the copy on port 8765 stopped after the replacement was confirmed ready", events)
	}
	if len(replaced.Earlier) != 1 || replaced.Earlier[0].PID != first.PID || replaced.Earlier[0].Problem != "" ||
		!strings.Contains(replaced.Earlier[0].Said, "port 8765") {
		t.Errorf("Replace() said it stopped %+v, want the copy on port 8765, stopped", replaced.Earlier)
	}
	if starts, err := child.store.Starts(); err != nil || len(starts) != 1 || starts[0].PID != replaced.PID {
		t.Errorf("Starts() = %+v, %v, want only the replacement's start kept", starts, err)
	}

	standing := readCopies(child, fakes, replaced.PID)
	if raised := copiesProblems(standing); len(raised) != 0 {
		t.Errorf("the problems list raised %+v after the handover, want no stray copy", raised)
	}
	for _, copies := range standing.Services.Copies {
		if copies.Service == string(config.ServiceDashboard) && copies.Count() != 1 {
			t.Errorf("dashboard copies = %+v, want the replacement alone", copies)
		}
	}
}

// A dashboard the supervisor did not start is never stopped for being on
// another port, by a handover or by the supervisor's later looks, and the
// problems list goes on reporting it as not the supervisor's, naming its
// process and what stops it.
func TestADashboardTheSupervisorDidNotStartIsReportedAndLeftRunning(t *testing.T) {
	t.Parallel()
	child, fakes := newDashboardChild(t)
	dashboardReaders(t, child, fakes)
	if _, err := child.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	stranger := fakes.handStarted(9999)

	child.port = 8080
	replaced, err := child.Replace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if later, err := child.StopEarlierCopies(context.Background()); err != nil || len(later) != 0 {
		t.Errorf("StopEarlierCopies() = %+v, %v, want nothing to stop", later, err)
	}
	if slices.Contains(fakes.said(), "stop "+strconv.Itoa(stranger.PID)) {
		t.Errorf("events = %v, the dashboard the supervisor did not start was stopped", fakes.said())
	}
	for _, earlier := range replaced.Earlier {
		if earlier.PID == stranger.PID {
			t.Errorf("Replace() said %+v as the supervisor's earlier copy", earlier)
		}
	}

	raised := copiesProblems(readCopies(child, fakes, replaced.PID))
	if len(raised) == 0 {
		t.Fatal("the problems list raised nothing, want the dashboard the supervisor did not start")
	}
	pid := strconv.Itoa(stranger.PID)
	for _, entry := range raised {
		what := entry.What()
		for _, want := range []string{"pid " + pid, "not started by the supervisor, so it is left running; `kill " + pid + "` stops it"} {
			if !strings.Contains(what, want) {
				t.Errorf("what = %q, want %q in it", what, want)
			}
		}
	}
}

// An earlier copy that would not stop at the handover is still the
// supervisor's: the problems list says so rather than calling it a stranger,
// its start is kept, and the supervisor's next look stops it.
func TestAnEarlierDashboardThatWouldNotStopIsStoppedAtTheNextLook(t *testing.T) {
	t.Parallel()
	child, fakes := newDashboardChild(t)
	dashboardReaders(t, child, fakes)
	first, err := child.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fakes.refuse = map[int]bool{first.PID: true}

	child.port = 8080
	replaced, err := child.Replace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced.Earlier) != 1 || replaced.Earlier[0].Problem == "" {
		t.Errorf("Replace() said %+v, want the copy that would not stop with why", replaced.Earlier)
	}
	raised := copiesProblems(readCopies(child, fakes, replaced.PID))
	if len(raised) == 0 || !strings.Contains(raised[0].What(), "an earlier copy the supervisor started") || strings.Contains(raised[0].What(), "not started by the supervisor") {
		t.Errorf("the problems list raised %+v, want the copy said as an earlier one of the supervisor's", raised)
	}

	fakes.mu.Lock()
	fakes.refuse = nil
	fakes.mu.Unlock()
	later, err := child.StopEarlierCopies(context.Background())
	if err != nil || len(later) != 1 || later[0].PID != first.PID || later[0].Problem != "" {
		t.Errorf("StopEarlierCopies() = %+v, %v, want the copy on port 8765 stopped", later, err)
	}
	if raised := copiesProblems(readCopies(child, fakes, replaced.PID)); len(raised) != 0 {
		t.Errorf("the problems list raised %+v once the earlier copy stopped, want nothing", raised)
	}
	if starts, _ := child.store.Starts(); len(starts) != 1 {
		t.Errorf("Starts() = %+v, want the earlier start forgotten", starts)
	}
}

// dashboardOnPort is a project configuration that runs the dashboard on port.
func dashboardOnPort(port int) string {
	return `version: 1
extends: builtin:v1
product:
  id: yoyodyne
  repository: .
services:
  dashboard:
    enabled: true
    port: ` + strconv.Itoa(port) + `
`
}

// Changing services.dashboard.port in the configuration, with no new build
// deployed, has the supervisor itself replace the dashboard it runs with one on
// the new port at its next look at settings, ready before the copy on the old
// port is stopped, and record that it did and which setting changed. Until it
// does, `yoyo status` says which part, which setting, and what becomes of it.
func TestChangingTheDashboardPortWithoutANewBuildMovesTheDashboardOntoIt(t *testing.T) {
	t.Parallel()
	child, fakes := newDashboardChild(t)
	dashboardReaders(t, child, fakes)
	path := writeProjectConfig(t, dashboardOnPort(8765))
	child.config = path
	child.configured = func() (config.Services, error) {
		resolved, err := config.LoadResolved(path)
		return resolved.Config.Services, err
	}
	fakes.configPath = path
	child.readers = fakes.readers

	supervision, err := runstate.NewSupervisionStore(child.logRoot, child.product)
	if err != nil {
		t.Fatal(err)
	}
	lease, held, err := supervision.Lease()
	if err != nil || !held {
		t.Fatalf("Lease() = %v, %v", held, err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	supervisor := &supervise.Supervisor{
		Records:  supervision,
		Product:  child.product,
		Children: []supervise.Child{child},
		Now:      func() time.Time { return now },
		Log:      t.Logf,
		PID:      4242,
		Build:    "deployed-build",
	}
	supervisor.Tick(context.Background())
	first, running, err := child.current()
	if err != nil || !running || first.Port != 8765 {
		t.Fatalf("current() = %+v, %v, %v, want the dashboard serving on port 8765", first, running, err)
	}

	if err := os.WriteFile(path, []byte(dashboardOnPort(9123)), 0o600); err != nil {
		t.Fatal(err)
	}
	standing := readmodel.ReadStanding(context.Background(), readmodel.Sources{Supervision: supervision, ConfigReaders: fakes.readers, Dashboards: child.store})
	if standing.Services == nil || len(standing.Services.Settings) != 1 {
		t.Fatalf("services = %+v, want the dashboard named as holding a changed setting", standing.Services)
	}
	said := standing.RenderServices()
	for _, want := range []string{"the dashboard service, running as pid " + strconv.Itoa(first.PID), "services.dashboard.port is 9123 in the configuration and it started with 8765", "the supervisor restarts it on the new value at its next look"} {
		if !strings.Contains(said, want) {
			t.Errorf("RenderServices() = %q, want %q", said, want)
		}
	}

	now = now.Add(supervise.DeployEvery)
	supervisor.Tick(context.Background())
	moved, running, err := child.current()
	if err != nil || !running || moved.Port != 9123 || moved.PID == first.PID {
		t.Fatalf("current() = %+v, %v, %v, want a new dashboard serving on port 9123", moved, running, err)
	}
	if launch := fakes.launches[len(fakes.launches)-1]; !slices.Contains(launch.Args, "9123") {
		t.Errorf("the replacement was launched with %v, want port 9123", launch.Args)
	}
	events := fakes.said()
	readyAt := slices.Index(events, "ready "+moved.StartID)
	if at := slices.Index(events, "stop "+strconv.Itoa(first.PID)); at < 0 || at < readyAt {
		t.Errorf("events = %v, want the copy on port 8765 stopped after the one on 9123 was confirmed ready", events)
	}
	if alive, _ := fakes.alivePID(first.PID); alive {
		t.Errorf("the dashboard on port 8765, pid %d, is still running", first.PID)
	}
	recorded, _, err := supervision.Load()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := recorded.Child(config.ServiceDashboard)
	if got.PID != moved.PID || got.SettingsRestarts != 1 || got.Failures != 0 || got.Restarts != 0 ||
		!strings.Contains(got.SettingsApplied, "services.dashboard.port is 9123 in the configuration and it started with 8765") {
		t.Errorf("the supervisor's record of the dashboard = %+v, want one restart to apply the changed port, recorded with the setting", got)
	}
	standing = readmodel.ReadStanding(context.Background(), readmodel.Sources{Supervision: supervision, ConfigReaders: fakes.readers, Dashboards: child.store})
	if len(standing.Services.Settings) != 0 {
		t.Errorf("services settings = %+v, want nothing once the dashboard is on the new port", standing.Services.Settings)
	}
}

// dashboardBound is a project configuration that runs the dashboard on port
// 8765 at bind, under the token stored in the keychain, which a bind outside
// loopback needs to load at all.
func dashboardBound(bind string) string {
	return `version: 1
extends: builtin:v1
product:
  id: yoyodyne
  repository: .
services:
  dashboard:
    enabled: true
    port: 8765
    bind: "` + bind + `"
    token: keychain
`
}

// Changing services.dashboard.bind to another loopback address, with no new
// build deployed, has the supervisor replace the dashboard with one listening
// there, ready before the old one stops, and record the setting it applied. A
// bind that is not loopback cannot be applied: no dashboard is started on it,
// the one running goes on serving on what it started with, and `yoyo status`
// says which setting and why.
func TestChangingTheDashboardBindMovesItOrSaysWhyItCannot(t *testing.T) {
	t.Parallel()
	child, fakes := newDashboardChild(t)
	dashboardReaders(t, child, fakes)
	path := writeProjectConfig(t, dashboardBound("127.0.0.1"))
	child.config = path
	child.source = config.DashboardTokenKeychain
	fakes.supplied = "the-stored-token"
	child.configured = func() (config.Services, error) {
		resolved, err := config.LoadResolved(path)
		return resolved.Config.Services, err
	}
	fakes.configPath = path
	child.readers = fakes.readers

	supervision, err := runstate.NewSupervisionStore(child.logRoot, child.product)
	if err != nil {
		t.Fatal(err)
	}
	lease, held, err := supervision.Lease()
	if err != nil || !held {
		t.Fatalf("Lease() = %v, %v", held, err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	supervisor := &supervise.Supervisor{
		Records:  supervision,
		Product:  child.product,
		Children: []supervise.Child{child},
		Now:      func() time.Time { return now },
		Log:      t.Logf,
		PID:      4242,
		Build:    "deployed-build",
	}
	supervisor.Tick(context.Background())
	first, running, err := child.current()
	if err != nil || !running || first.URL != "http://127.0.0.1:8765/" {
		t.Fatalf("current() = %+v, %v, %v, want the dashboard serving at 127.0.0.1", first, running, err)
	}

	if err := os.WriteFile(path, []byte(dashboardBound("::1")), 0o600); err != nil {
		t.Fatal(err)
	}
	now = now.Add(supervise.DeployEvery)
	supervisor.Tick(context.Background())
	moved, running, err := child.current()
	if err != nil || !running || moved.PID == first.PID || moved.URL != "http://[::1]:8765/" {
		t.Fatalf("current() = %+v, %v, %v, want a new dashboard listening at ::1", moved, running, err)
	}
	if alive, _ := fakes.alivePID(first.PID); alive {
		t.Errorf("the dashboard at 127.0.0.1, pid %d, is still running", first.PID)
	}
	recorded, _, err := supervision.Load()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := recorded.Child(config.ServiceDashboard)
	if got.SettingsRestarts != 1 || got.Failures != 0 || !strings.Contains(got.SettingsApplied, "services.dashboard.bind is ::1 in the configuration and it started with 127.0.0.1") {
		t.Errorf("the supervisor's record of the dashboard = %+v, want one restart applying the changed bind", got)
	}

	if err := os.WriteFile(path, []byte(dashboardBound("192.168.1.20")), 0o600); err != nil {
		t.Fatal(err)
	}
	launches := len(fakes.launches)
	now = now.Add(supervise.DeployEvery)
	supervisor.Tick(context.Background())
	kept, running, err := child.current()
	if err != nil || !running || kept.PID != moved.PID || len(fakes.launches) != launches {
		t.Fatalf("current() = %+v, %v, %v after %d launches, want the dashboard at ::1 left serving and none started", kept, running, err, len(fakes.launches)-launches)
	}
	recorded, _, err = supervision.Load()
	if err != nil {
		t.Fatal(err)
	}
	got, _ = recorded.Child(config.ServiceDashboard)
	if !got.SettingsRefused || got.Failures != 0 {
		t.Errorf("the supervisor's record of the dashboard = %+v, want the change recorded as one it cannot apply", got)
	}
	standing := readmodel.ReadStanding(context.Background(), readmodel.Sources{Supervision: supervision, ConfigReaders: fakes.readers, Dashboards: child.store})
	said := standing.RenderServices()
	for _, want := range []string{
		"services.dashboard.bind is 192.168.1.20 in the configuration and it started with ::1",
		"the change cannot be applied, so it is not restarted and goes on serving on what it started with",
		`services.dashboard.bind "192.168.1.20" is not a loopback address, and the dashboard listens only on this machine`,
	} {
		if !strings.Contains(said, want) {
			t.Errorf("RenderServices() = %q, want %q", said, want)
		}
	}
}

// A dashboard the supervisor would start on an address that is not loopback
// is not started at all: the part is left down at once with the reason, rather
// than started into a refusal five times.
func TestADashboardIsNotStartedOnABindOutsideLoopback(t *testing.T) {
	t.Parallel()
	child, fakes := newDashboardChild(t)
	path := writeProjectConfig(t, dashboardBound("192.168.1.20"))
	child.configured = func() (config.Services, error) {
		resolved, err := config.LoadResolved(path)
		return resolved.Config.Services, err
	}
	ensured, err := child.Ensure(context.Background())
	if err != nil || !strings.Contains(ensured.Unstartable, "is not a loopback address, and the dashboard listens only on this machine") || len(fakes.launches) != 0 {
		t.Fatalf("Ensure() = %+v, %v after %d launches, want it unstartable with the reason and nothing launched", ensured, err, len(fakes.launches))
	}
}

// What the running dashboard could not apply of who it accepts, as it last
// recorded it, is said on the dashboard's line under Services, beside its
// health, in the dashboard's own words.
func TestWhatTheDashboardCouldNotApplyIsOnItsStatusLine(t *testing.T) {
	t.Parallel()
	child, _ := newDashboardChild(t)
	supervision, err := runstate.NewSupervisionStore(child.logRoot, child.product)
	if err != nil {
		t.Fatal(err)
	}
	lease, held, err := supervision.Lease()
	if err != nil || !held {
		t.Fatalf("Lease() = %v, %v", held, err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	supervisor := &supervise.Supervisor{Records: supervision, Product: child.product, Children: []supervise.Child{child},
		Now: time.Now, Log: t.Logf, PID: 4242, Build: "deployed-build"}
	supervisor.Tick(context.Background())
	instance, running, err := child.current()
	if err != nil || !running {
		t.Fatalf("current() = %v, %v", running, err)
	}
	problem := "mason cannot approve, decline, or ask from the page: the dashboard's token could not be read: the file /state/dashboard.mason.token is not there; store it with: mkdir -p /state"
	instance.AccessProblems = []string{problem}
	if err := child.store.Update(instance); err != nil {
		t.Fatal(err)
	}
	supervisor.Tick(context.Background())
	standing := readmodel.ReadStanding(context.Background(), readmodel.Sources{Supervision: supervision, Dashboards: child.store})
	if said := standing.RenderServices(); !strings.Contains(said, "dashboard: running") || !strings.Contains(said, problem) {
		t.Fatalf("RenderServices() = %q, want the dashboard's line to carry %q", said, problem)
	}
}
