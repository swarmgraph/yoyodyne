package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
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
	f.next++
	pid := f.next
	instance := runstate.DashboardInstance{StartID: startID, PID: pid, Build: "deployed-build", StartedAt: time.Now().UTC(),
		URL: "http://127.0.0.1:8765/", Port: 8765, ReadyURL: "http://127.0.0.1:45999/", Supervised: true}
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
	f.events = append(f.events, "launch "+startID)
	return pid, nil
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
