package cli

// The dashboard as a part of the product.
//
// The observability-and-dashboard design's "Under the supervisor" and "The
// generated token" are the rules this follows. The supervisor starts
// `yoyo dashboard` detached on the configured port, handing it a start identity
// it records as its own account of that start (runstate.DashboardStart). The
// dashboard records itself under that identity, publishes its generated token
// to the token file, and serves nothing but its health answer until the
// supervisor has made one request carrying that token and had it accepted.
// Only then is the start counted, and only then is every earlier dashboard the
// supervisor started stopped, whatever port it serves on, with any dashboard
// somebody started by hand on the new one's port.
//
// So a dashboard is taken up only where its start identity is the one the
// supervisor recorded and its process is the one that start launched; one
// started by hand, or one whose record says otherwise, is not this part. The
// supervisor keeps a record of every start it made until it has seen that
// start's dashboard stop (runstate.DashboardStore.Starts), so an earlier copy
// is recognised as its own by its start identity after a later start has
// replaced it — including one left on a port the configuration has since
// moved away from.
// Moving onto a deployed build is a replacement — the new one started and
// confirmed ready before the old one stops — so the port is never left with no
// dashboard ready on it. Reattaching to a dashboard that survived the
// supervisor keeps that dashboard, and with it its token; only a new
// dashboard process makes a new one. The token is read here to make the
// readiness request and for nothing else: it is never logged, recorded, or
// put in an error.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
	"github.com/mason-bryant/yoyodyne/internal/supervise"
)

// dashboardLogFile is where the supervised dashboard says what it is doing.
const dashboardLogFile = "dashboard.log"

// dashboardReadyWait bounds how long a start waits for the dashboard it
// launched to be confirmed ready. The dashboard opens the state, binds, and
// publishes its token before anything else, which is seconds; past this it is
// stopped and the start counted as failed.
const dashboardReadyWait = 60 * time.Second

// dashboardChild is the dashboard as the supervisor drives it.
type dashboardChild struct {
	store    *runstate.DashboardStore
	product  domain.ProductID
	launcher slack.Launcher
	program  string
	config   string
	dir      string
	environ  []string
	logRoot  string
	log      string
	port     int
	// bind is the address the dashboard is to listen on, which it reads from
	// the configuration itself; the child holds it to refuse an address that
	// is not loopback before a dashboard is started on one.
	bind   string
	source config.DashboardTokenSource
	// configured reads the services section as the configuration says it now,
	// which is what a start launches on; nil keeps port, bind, and source, the
	// values the supervisor read as it started.
	configured func() (config.Services, error)
	// readers is where each dashboard records the settings it started on; nil
	// opens the product's records under logRoot.
	readers *runstate.ConfigReaderStore
	tokens  dashboard.TokenStores
	// client makes the readiness request; nil uses one with a short timeout.
	client *http.Client
	// wait and poll bound the wait for readiness, and look and stop are the
	// operating system's answers; each is replaced by a test.
	wait  time.Duration
	poll  time.Duration
	look  func(pid int) (runstate.ProcessLook, error)
	stop  func(ctx context.Context, pid int, held func() (bool, error)) (supervise.Stopped, error)
	now   func() time.Time
	build func() string
}

func (dashboardChild) Name() config.ServiceName { return config.ServiceDashboard }

// current is the dashboard the supervisor last started, where it is still the
// process that start launched and still holds its lease.
func (c dashboardChild) current() (runstate.DashboardInstance, bool, error) {
	start, found, err := c.store.Start()
	if err != nil || !found {
		return runstate.DashboardInstance{}, false, err
	}
	look, found, err := c.store.Instance(start.StartID)
	if err != nil {
		return runstate.DashboardInstance{}, false, err
	}
	if !found || !look.Live || look.Instance.PID != start.PID || !look.Instance.Supervised {
		return runstate.DashboardInstance{}, false, nil
	}
	return look.Instance, true, nil
}

func (c dashboardChild) Running(context.Context) (bool, error) {
	_, running, err := c.current()
	return running, err
}

// HolderPID is the process of the dashboard the supervisor started.
func (c dashboardChild) HolderPID(context.Context) int {
	instance, running, _ := c.current()
	if !running {
		return 0
	}
	return instance.PID
}

// Build is the revision the running dashboard recorded itself as built from.
func (c dashboardChild) Build(context.Context) (string, error) {
	instance, running, err := c.current()
	if err != nil || !running {
		return "", err
	}
	return instance.Build, nil
}

// Busy is nothing: a request in flight is finished by the dashboard's own
// shutdown, and a replacement serves before the old one is asked to stop.
func (dashboardChild) Busy(context.Context) (string, error) { return "", nil }

// Serving is where the dashboard serves and how its health stood when it last
// recorded itself.
func (c dashboardChild) Serving(context.Context) supervise.Serving {
	instance, running, err := c.current()
	if err != nil || !running {
		return supervise.Serving{}
	}
	serving := supervise.Serving{URL: instance.URL}
	switch {
	case instance.TokenProblem != "":
		serving.Health = "not ready: " + instance.TokenProblem
	case instance.ReadyAt.IsZero():
		serving.Health = "not ready yet"
	default:
		serving.Health = "ready since " + instance.ReadyAt.Local().Format("2006-01-02 15:04 MST")
	}
	// What the running dashboard could not apply of who it accepts, as it last
	// re-read it, is said beside its health, so it is on the line `yoyo status`
	// prints for the dashboard and not only in its log.
	if len(instance.AccessProblems) > 0 {
		serving.Health += "; " + strings.Join(instance.AccessProblems, "; ")
	}
	if instance.TokenPublished {
		serving.TokenFile = c.store.TokenPath()
	}
	return serving
}

// Ensure starts a dashboard unless the one the supervisor started is running,
// and reattaches to it — and its token — where it is.
func (c dashboardChild) Ensure(ctx context.Context) (supervise.Ensured, error) {
	instance, running, err := c.current()
	if err != nil {
		return supervise.Ensured{}, err
	}
	if running {
		return supervise.Ensured{PID: instance.PID}, nil
	}
	// An address that is not loopback is one no dashboard will start on, which
	// waiting and trying again does not change.
	if err := dashboard.LoopbackBind(c.onConfiguration().address()); err != nil {
		return supervise.Ensured{Unstartable: "it cannot be started: " + err.Error()}, nil
	}
	ensured, err := c.Replace(ctx)
	var unsafe dashboardUnsafeToken
	if errors.As(err, &unsafe) {
		return supervise.Ensured{Unstartable: unsafe.Error()}, nil
	}
	return ensured, err
}

// Replace starts a dashboard from the binary, confirms it ready, and then stops
// every earlier dashboard the supervisor started, on whatever port, and one
// somebody started by hand on the new one's port. Where the new one could not bind because
// a dashboard from a build that cannot share the port holds it, that one is
// stopped first and the start made again, once.
func (c dashboardChild) Replace(ctx context.Context) (supervise.Ensured, error) {
	// Started on the configuration as it stands, so a replacement applies a
	// port, address, or token source changed since the supervisor started.
	c = c.onConfiguration()
	if err := dashboard.LoopbackBind(c.address()); err != nil {
		return supervise.Ensured{}, err
	}
	ensured, startID, err := c.launch(ctx)
	if errors.Is(err, errDashboardPortHeld) {
		stopped, stopErr := c.stopUnsharedHolders(ctx)
		if stopErr != nil {
			return supervise.Ensured{}, errors.Join(err, stopErr)
		}
		if stopped == 0 {
			return supervise.Ensured{}, fmt.Errorf("%w, and no dashboard of this product is recorded running to stop; free port %d or set services.dashboard.port", err, c.port)
		}
		ensured, startID, err = c.launch(ctx)
	}
	if err != nil {
		return supervise.Ensured{}, err
	}
	// The new dashboard is serving whatever comes of this: an earlier copy that
	// would not stop is still running, said by the supervisor, and stopped at
	// its next look at builds (StopEarlierCopies).
	ensured.Earlier, _ = c.stopOthers(ctx, startID, true)
	return ensured, nil
}

// onConfiguration is the child with the port, address, and token source the
// configuration says now. A configuration that cannot be read keeps the values
// it had, so a broken edit leaves the dashboard where it was rather than
// stopping it.
func (c dashboardChild) onConfiguration() dashboardChild {
	if c.configured == nil {
		return c
	}
	services, err := c.configured()
	if err != nil {
		return c
	}
	c.port, c.bind, c.source = services.Dashboard.Port, services.Dashboard.Bind, services.Dashboard.Token
	return c
}

// address is the bind the child holds, and the loopback default where it holds
// none, as a configuration that names none resolves to.
func (c dashboardChild) address() string {
	if strings.TrimSpace(c.bind) == "" {
		return config.DefaultDashboardBind
	}
	return c.bind
}

// RefusedSettings is why the configuration as it stands now is one no
// dashboard can start on, and empty where one can: an address that is not
// loopback. The supervisor then leaves the running dashboard serving on what
// it started with rather than replacing it with one that refuses to start.
func (c dashboardChild) RefusedSettings(context.Context) string {
	if err := dashboard.LoopbackBind(c.onConfiguration().address()); err != nil {
		return err.Error()
	}
	return ""
}

// ChangedSettings is each setting the dashboard the supervisor runs holds from
// its start whose value in the configuration is now different, from the
// settings that dashboard recorded as it started. A dashboard that recorded none
// is compared with nothing.
func (c dashboardChild) ChangedSettings(context.Context) ([]runstate.SettingChange, error) {
	if c.configured == nil {
		return nil, nil
	}
	instance, running, err := c.current()
	if err != nil || !running {
		return nil, err
	}
	readers := c.readers
	if readers == nil {
		if readers, err = runstate.NewConfigReaderStore(c.logRoot, c.product); err != nil {
			return nil, err
		}
	}
	// Another part's record that could not be read says nothing about this
	// dashboard's, so it stops the comparison only where this one is not found.
	processes, readErr := readers.Running()
	var started map[string]string
	for _, reader := range processes {
		if reader.Service == string(config.ServiceDashboard) && reader.PID == instance.PID {
			started = reader.Settings
		}
	}
	if len(started) == 0 {
		return nil, readErr
	}
	services, err := c.configured()
	if err != nil {
		return nil, fmt.Errorf("read the configuration: %w", err)
	}
	return runstate.ChangedSettings(started, services.StartSettings(config.ServiceDashboard)), nil
}

// StopEarlierCopies stops every dashboard the supervisor started before the one
// it runs now, whatever port each serves on. It stops nothing while the one it
// runs now is not running, since an earlier copy may then be the only one
// serving and the start that follows stops it once it is ready.
func (c dashboardChild) StopEarlierCopies(ctx context.Context) ([]supervise.EarlierCopy, error) {
	instance, running, err := c.current()
	if err != nil || !running {
		return nil, err
	}
	return c.stopOthers(ctx, instance.StartID, false)
}

// errDashboardPortHeld is a dashboard that exited before it was ready because
// its port was held by a process that did not share it.
var errDashboardPortHeld = errors.New("the dashboard could not bind its port, which another process holds and does not share")

func (c dashboardChild) launch(ctx context.Context) (supervise.Ensured, string, error) {
	startID, err := runstate.NewDashboardStartID()
	if err != nil {
		return supervise.Ensured{}, "", err
	}
	pid, err := c.launcher.Launch(slack.Launch{
		Program: c.program,
		Args:    []string{"dashboard", "--config", c.config, "--port", strconv.Itoa(c.port), "--start-id", startID},
		Dir:     c.dir,
		Env:     slack.WithoutSecrets(c.environ),
		LogRoot: c.logRoot,
		Log:     c.log,
	})
	if err != nil {
		return supervise.Ensured{}, "", err
	}
	build := ""
	if c.build != nil {
		build = c.build()
	}
	if err := c.store.RecordStart(runstate.DashboardStart{StartID: startID, PID: pid, Build: build, Port: c.port, StartedAt: c.clock().UTC()}); err != nil {
		c.abandon(ctx, pid)
		return supervise.Ensured{}, "", fmt.Errorf("record the dashboard's start: %w", err)
	}
	if err := c.waitReady(ctx, startID, pid); err != nil {
		c.abandon(ctx, pid)
		return supervise.Ensured{}, "", err
	}
	return supervise.Ensured{Started: true, PID: pid, Log: filepath.Join(c.logRoot, filepath.FromSlash(c.log))}, startID, nil
}

// waitReady waits for the dashboard to record itself and makes the readiness
// request to its own readiness port, carrying its token, until the dashboard
// that answers is the one this start launched.
func (c dashboardChild) waitReady(ctx context.Context, startID string, pid int) error {
	deadline := c.clock().Add(c.waitBound())
	var last string
	for {
		look, found, err := c.store.Instance(startID)
		switch {
		case err != nil:
			last = err.Error()
		case found && look.Instance.TokenProblem != "":
			// A token that cannot be published safely is the file's owner's to
			// settle, not something a retry changes.
			return dashboardUnsafeToken{problem: look.Instance.TokenProblem}
		case found && look.Instance.ReadyURL != "":
			confirmed, why := c.confirm(ctx, look.Instance)
			if confirmed {
				return nil
			}
			last = why
		case !found:
			if exited, held := c.exited(pid); exited {
				if held {
					return fmt.Errorf("%w (port %d); its log is %s", errDashboardPortHeld, c.port, c.logPath())
				}
				return fmt.Errorf("the dashboard exited before it was ready; its log is %s", c.logPath())
			}
			last = "it has not recorded itself yet"
		}
		if !c.clock().Before(deadline) {
			return fmt.Errorf("the dashboard was not confirmed ready within %s (%s); its log is %s", c.waitBound(), last, c.logPath())
		}
		if !sleepContext(ctx, c.pollEvery()) {
			return ctx.Err()
		}
	}
}

// confirm makes the readiness request. It reports why it was not accepted in
// words that never carry the token.
func (c dashboardChild) confirm(ctx context.Context, instance runstate.DashboardInstance) (bool, string) {
	token, err := c.token(ctx)
	if err != nil {
		return false, err.Error()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(instance.ReadyURL, "/")+"/api/ready", nil)
	if err != nil {
		return false, err.Error()
	}
	request.Header.Set("Authorization", "Bearer "+token)
	client := c.client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		said := err.Error()
		if token != "" {
			said = strings.ReplaceAll(said, token, "[token]")
		}
		return false, "the readiness request could not be made: " + said
	}
	defer response.Body.Close()
	var health dashboard.Health
	_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&health)
	if response.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("the readiness request was answered %d", response.StatusCode)
	}
	if !health.Ready || health.StartID != instance.StartID {
		return false, "the readiness request was answered by a dashboard that is not the one started"
	}
	return true, ""
}

// token is what the readiness request carries: the generated one from the file
// the dashboard published, or the one the configuration names from its store.
func (c dashboardChild) token(ctx context.Context) (string, error) {
	if c.source == config.DashboardTokenGenerated {
		return c.store.ReadToken()
	}
	supplied, err := c.tokens.Read(ctx, c.source, c.product)
	if err != nil {
		return "", err
	}
	return supplied.Value, nil
}

// stopOthers stops every running dashboard the supervisor started but the one
// to keep, recognised by its start identity and process and never by its port,
// and, where samePort is set, every other dashboard of this product on the
// configured port, which is one somebody started by hand there. It returns the
// supervisor's earlier copies and what stopping each came to, and forgets the
// start of each that has stopped.
func (c dashboardChild) stopOthers(ctx context.Context, keep string, samePort bool) ([]supervise.EarlierCopy, error) {
	var problems []error
	starts, err := c.store.Starts()
	if err != nil {
		problems = append(problems, err)
	}
	launched := make(map[string]int, len(starts))
	for _, start := range starts {
		launched[start.StartID] = start.PID
	}
	looks, lookErr := c.store.Instances()
	if lookErr != nil {
		problems = append(problems, lookErr)
	}
	stillRunning := map[string]bool{}
	var earlier []supervise.EarlierCopy
	for _, look := range looks {
		instance := look.Instance
		if instance.StartID == keep || !look.Live {
			continue
		}
		pid, recorded := launched[instance.StartID]
		ours := recorded && pid == instance.PID
		if !ours && (!samePort || instance.Port != c.port) {
			continue
		}
		startID := instance.StartID
		stopped, err := c.stopper()(ctx, instance.PID, func() (bool, error) {
			found, ok, err := c.store.Instance(startID)
			return ok && found.Live, err
		})
		if err != nil || stopped.Detail != "" {
			stillRunning[startID] = true
		}
		if err != nil {
			problems = append(problems, err)
		}
		if !ours {
			continue
		}
		said := supervise.EarlierCopy{PID: instance.PID, Said: describeDashboardCopy(instance), Stopped: stopped}
		if err != nil {
			said.Problem = err.Error()
		}
		earlier = append(earlier, said)
	}
	// A start whose dashboard is not running is one nothing will ever need to
	// recognise again; one whose dashboard would not stop is kept, so the next
	// look still knows it for the supervisor's. Where the dashboards' records
	// could not all be read, nothing is forgotten, since an unread one may be
	// running.
	for startID := range launched {
		if lookErr != nil || startID == keep || stillRunning[startID] {
			continue
		}
		if err := c.store.ForgetStart(startID); err != nil {
			problems = append(problems, err)
		}
	}
	return earlier, errors.Join(problems...)
}

// describeDashboardCopy is one dashboard in words: its process, the port it
// serves on, its build, and the start it was.
func describeDashboardCopy(instance runstate.DashboardInstance) string {
	build := "on a build that recorded no revision"
	if instance.Build != "" {
		build = "on build " + shortBuild(instance.Build)
	}
	return fmt.Sprintf("pid %d serving on port %d %s, started %s as %s",
		instance.PID, instance.Port, build, instance.StartedAt.Local().Format("2006-01-02 15:04 MST"), instance.StartID)
}

// stopUnsharedHolders stops the dashboards of this product that recorded
// themselves as reading the configuration and recorded no instance — dashboards
// from a build before the dashboard shared its port — after a start found the
// port held. It reports how many it asked to stop.
func (c dashboardChild) stopUnsharedHolders(ctx context.Context) (int, error) {
	readers, err := runstate.NewConfigReaderStore(c.logRoot, c.product)
	if err != nil {
		return 0, err
	}
	running, err := readers.Running()
	if err != nil {
		return 0, err
	}
	looks, _ := c.store.Instances()
	recorded := map[int]bool{}
	for _, look := range looks {
		recorded[look.Instance.PID] = true
	}
	stopped := 0
	var problems []error
	for _, reader := range running {
		if reader.Service != string(config.ServiceDashboard) || recorded[reader.PID] || reader.PID == os.Getpid() {
			continue
		}
		pid := reader.PID
		if _, err := c.stopper()(ctx, pid, func() (bool, error) {
			look, err := c.lookup(pid)
			return err == nil && look.Running() && look.Is(reader.StartedAt), err
		}); err != nil {
			problems = append(problems, err)
			continue
		}
		stopped++
	}
	return stopped, errors.Join(problems...)
}

// Stop stops the dashboard the supervisor started. A dashboard somebody
// started by hand on another port is theirs and is left running.
func (c dashboardChild) Stop(ctx context.Context) (supervise.Stopped, error) {
	instance, running, err := c.current()
	if err != nil {
		return supervise.Stopped{}, err
	}
	if !running {
		return supervise.Stopped{}, nil
	}
	return c.stopper()(ctx, instance.PID, func() (bool, error) {
		look, found, err := c.store.Instance(instance.StartID)
		return found && look.Live, err
	})
}

// abandon stops a dashboard this start launched that did not become ready.
func (c dashboardChild) abandon(ctx context.Context, pid int) {
	look, err := c.lookup(pid)
	if err != nil || !look.Running() {
		return
	}
	_, _ = c.stopper()(ctx, pid, func() (bool, error) {
		look, err := c.lookup(pid)
		return err == nil && look.Running(), err
	})
}

// exited reports whether the launched process has exited, and whether its log
// says the port was held.
func (c dashboardChild) exited(pid int) (bool, bool) {
	look, err := c.lookup(pid)
	if err != nil || look.Running() {
		return false, false
	}
	tail, _ := readTail(c.logPath(), 8<<10)
	return true, strings.Contains(tail, "address already in use")
}

func (c dashboardChild) logPath() string {
	return filepath.Join(c.logRoot, filepath.FromSlash(c.log))
}

func (c dashboardChild) lookup(pid int) (runstate.ProcessLook, error) {
	if c.look != nil {
		return c.look(pid)
	}
	return runstate.LookProcess(pid)
}

func (c dashboardChild) stopper() func(ctx context.Context, pid int, held func() (bool, error)) (supervise.Stopped, error) {
	if c.stop != nil {
		return c.stop
	}
	return supervise.StopHolder
}

func (c dashboardChild) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c dashboardChild) waitBound() time.Duration {
	if c.wait > 0 {
		return c.wait
	}
	return dashboardReadyWait
}

func (c dashboardChild) pollEvery() time.Duration {
	if c.poll > 0 {
		return c.poll
	}
	return 250 * time.Millisecond
}

// dashboardUnsafeToken is a dashboard whose generated token could not be
// written safely, which the supervisor leaves down at once with the reason.
type dashboardUnsafeToken struct{ problem string }

func (e dashboardUnsafeToken) Error() string {
	return "its token could not be published safely, so it serves nothing and is left down: " + e.problem
}

// readTail is the end of a file, for finding what a process that exited said
// last.
func readTail(path string, limit int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > limit {
		if _, err := file.Seek(info.Size()-limit, io.SeekStart); err != nil {
			return "", err
		}
	}
	tail, err := io.ReadAll(io.LimitReader(file, limit))
	return string(tail), err
}

func sleepContext(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
