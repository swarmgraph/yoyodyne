package supervise

import (
	"context"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// settingsChild is a part that holds settings from its start: it starts on
// whatever the configuration says at that moment, and says which of those the
// configuration has changed since.
type settingsChild struct {
	*deployableChild
	settingsMu sync.Mutex
	configured map[string]string
	started    map[string]string
}

func newSettingsChild(name config.ServiceName, binary *binaryOnDisk, events *[]string, configured map[string]string) *settingsChild {
	return &settingsChild{deployableChild: newDeployable(name, binary, events), configured: configured}
}

func (s *settingsChild) Ensure(ctx context.Context) (Ensured, error) {
	ensured, err := s.deployableChild.Ensure(ctx)
	if err == nil && ensured.Started {
		s.settingsMu.Lock()
		s.started = maps.Clone(s.configured)
		s.settingsMu.Unlock()
	}
	return ensured, err
}

func (s *settingsChild) ChangedSettings(context.Context) ([]runstate.SettingChange, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if !s.isRunning() {
		return nil, nil
	}
	return runstate.ChangedSettings(s.started, s.configured), nil
}

func (s *settingsChild) configure(key, value string) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	s.configured[key] = value
}

// A setting a part holds from its start that changes in the configuration,
// with no build deployed, has the part restarted on the new value at the next
// look at settings, recorded as a restart to apply that setting and not as a
// death, and a part whose settings are as it started is left alone.
func TestAChangedSettingRestartsThePartWithNoBuildDeployed(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	var events []string
	part := newSettingsChild(config.ServiceSlack, binary, &events, map[string]string{"services.slack.example": "one"})
	supervisor := deploySupervisor(t, store, clock, binary, part)

	supervisor.Tick(context.Background())
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if part.startCount() != 1 || part.stopCount() != 0 {
		t.Fatalf("starts = %d stops = %d, want a part whose settings are as it started left alone", part.startCount(), part.stopCount())
	}

	part.configure("services.slack.example", "two")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if got, want := strings.Join(events, ","), "slack start,slack stop,slack start"; got != want {
		t.Fatalf("events = %s, want %s: the part stopped and started again on the new value", got, want)
	}
	got := child(t, loaded(t, store), config.ServiceSlack)
	if got.State != runstate.ChildRunning || got.Failures != 0 || !got.DiedAt.IsZero() || got.Restarts != 0 {
		t.Errorf("slack = %+v, want it running with nothing counted as a death or as a deploy", got)
	}
	if got.SettingsRestarts != 1 || !got.SettingsRestartedAt.Equal(clock.Now()) || got.SettingsRestarting || got.Settings != "" {
		t.Errorf("slack = %+v, want one restart to apply a setting, at this look, with nothing outstanding", got)
	}
	if !strings.Contains(got.SettingsApplied, "services.slack.example is two in the configuration and it started with one") {
		t.Errorf("settings applied = %q, want the setting, its new value, and the one it started with", got.SettingsApplied)
	}
	if said := DescribeChild(got); !strings.Contains(said, "restarted by the supervisor to apply a changed setting once") || !strings.Contains(said, "services.slack.example is two") {
		t.Errorf("DescribeChild = %q, want the restart and the setting it applied said", said)
	}

	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if part.startCount() != 2 {
		t.Errorf("starts = %d, want no further restart once the part is on the new value", part.startCount())
	}
}

// A changed setting is applied under the rules a deploy is held to: not while
// a restart is held, not in the middle of what the part is doing, and never to
// a part that restarts itself, since stopping the scheduler cancels the runs it
// hosts. Each time the record says which setting and why it waits.
func TestAChangedSettingWaitsWhereADeployWouldAndNeverStopsTheScheduler(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	part := newSettingsChild(config.ServiceSlack, binary, nil, map[string]string{"services.slack.example": "one"})
	watch := &settingsChild{deployableChild: newDeployable(config.ServiceScheduler, binary, nil), configured: map[string]string{"services.scheduler.example": "one"}}
	scheduler := struct {
		*settingsChild
		selfRestarts
	}{watch, selfRestarts{}}
	supervisor := deploySupervisor(t, store, clock, binary, part, scheduler)
	held := "the provider cannot be reached"
	supervisor.RestartHold = func() string { return held }
	supervisor.Tick(context.Background())

	part.configure("services.slack.example", "two")
	watch.configure("services.scheduler.example", "two")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if part.stopCount() != 0 {
		t.Fatalf("stops = %d, want nothing restarted while a restart is held", part.stopCount())
	}
	recorded := loaded(t, store)
	if got := child(t, recorded, config.ServiceSlack).Settings; !strings.Contains(got, "services.slack.example is two in the configuration and it started with one") || !strings.Contains(got, "not restarted while the provider cannot be reached") {
		t.Errorf("slack settings = %q, want the setting and the hold named", got)
	}
	if got := child(t, recorded, config.ServiceScheduler).Settings; !strings.Contains(got, "services.scheduler.example is two") || !strings.Contains(got, "stopping it cancels the runs it hosts") {
		t.Errorf("scheduler settings = %q, want the setting and why it is never stopped for one", got)
	}

	held = ""
	part.setBusy("a product manager turn in thread 1712")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if part.stopCount() != 0 {
		t.Fatalf("stops = %d, want a part in the middle of something left running", part.stopCount())
	}
	if got := child(t, loaded(t, store), config.ServiceSlack).Settings; !strings.Contains(got, "once it finishes a product manager turn in thread 1712") {
		t.Errorf("slack settings = %q, want what it waits out named", got)
	}

	part.setBusy("")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if part.stopCount() != 1 || part.startCount() != 2 {
		t.Errorf("stops = %d starts = %d, want the part restarted once nothing stops it", part.stopCount(), part.startCount())
	}
	if watch.stopCount() != 0 {
		t.Errorf("the scheduler was stopped %d times for a changed setting, want never", watch.stopCount())
	}
}

// selfRestarts makes a fake child one that takes a deployed build up itself.
type selfRestarts struct{}

func (selfRestarts) RestartsItself() string { return "it restarts itself into it between runs" }

// A part on the new value says nothing about settings, and the reason the
// supervisor gives for a part whose settings changed is said in the words both
// `yoyo doctor` and `yoyo status` use: a copy it did not start is left running
// and named with what stops it, and a copy it started with no supervisor
// running waits on `yoyo start`.
func TestSettingsDriftWhySaysWhoRestartsThePart(t *testing.T) {
	t.Parallel()
	drift := runstate.SettingsDrift{Service: "dashboard", PID: 7001, Changes: []runstate.SettingChange{{Key: "services.dashboard.port", Started: "8765", Configured: "9000"}}}
	record := runstate.Supervision{Children: []runstate.SupervisedChild{{Service: config.ServiceDashboard, PID: 7001}}}

	if got := SettingsDriftWhy(drift, true, record, false); !strings.Contains(got, "not started by the supervisor") || !strings.Contains(got, "`kill 7001`") {
		t.Errorf("a copy the supervisor did not start: %q", got)
	}
	if got := SettingsDriftWhy(drift, false, record, true); !strings.Contains(got, "no supervisor is running") || !strings.Contains(got, "`yoyo start`") {
		t.Errorf("no supervisor running: %q", got)
	}
	if got := SettingsDriftWhy(drift, true, record, true); !strings.Contains(got, "the supervisor restarts it on the new value at its next look") {
		t.Errorf("the supervisor's copy: %q", got)
	}
	record.Children[0].Settings = "services.dashboard.port is 9000 in the configuration and it started with 8765; not restarted while the provider cannot be reached"
	if got := SettingsDriftWhy(drift, true, record, true); !strings.Contains(got, "not restarted while the provider cannot be reached") {
		t.Errorf("the supervisor's copy, held: %q", got)
	}
	if !SupervisedCopy(drift, record, map[int]bool{7001: true}) || SupervisedCopy(drift, record, map[int]bool{}) {
		t.Error("a dashboard is the supervisor's by the starts it recorded, not by the process its record names")
	}
}
