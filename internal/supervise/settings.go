package supervise

// Applying a changed setting to the part that holds it.
//
// Some settings a part reads once, as it starts, and holds for as long as it
// runs: the dashboard binds services.dashboard.port and never looks at it again.
// A change to one in the configuration reaches the part only when it is started
// again, and a deploy is not what changes it, so moving parts onto a deployed
// build never applied it and the operator stopped the part by hand.
//
// So on its look at builds the supervisor asks each child that holds settings
// whether any has changed under it, and where one has restarts the child under
// the rules a deploy is held to (deploy.go): one part at a time, not while a
// restart is held, not in the middle of what the part is doing, and never a
// part that restarts itself, since stopping the scheduler cancels the runs it
// hosts. A child that starts its replacement first is replaced, so the new copy
// is ready before the old one stops. Each restart is recorded with the settings
// it applied, apart from the failures the restart bound counts.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// HoldsSettings is a child that holds settings from its start. ChangedSettings
// is each one whose value in the configuration now differs from the value the
// running child started with, and nothing where none does or the child is not
// running.
type HoldsSettings interface {
	ChangedSettings(ctx context.Context) ([]runstate.SettingChange, error)
}

// settings is one running child against the configuration: a restart begun
// where a setting it holds has changed and nothing stops it, and otherwise the
// record saying which setting and why it waits.
func (s *Supervisor) settings(ctx context.Context, child Child, state *runstate.SupervisedChild, now time.Time) {
	holds, ok := child.(HoldsSettings)
	if !ok || !s.deployLook || state.SettingsRestarting {
		return
	}
	if state.RestartingInto != "" {
		// A part being moved onto a deployed build is started from the
		// configuration as it stands, so the move applies the setting too.
		return
	}
	changes, err := holds.ChangedSettings(ctx)
	if err != nil {
		state.Settings = fmt.Sprintf("whether a setting it holds from its start has changed could not be read, so it is not restarted for one this look: %v", err)
		return
	}
	if len(changes) == 0 {
		state.Settings = ""
		return
	}
	changed := runstate.DescribeSettingChanges(changes)
	if self, ok := child.(RestartsItself); ok {
		state.Settings = fmt.Sprintf("%s; the supervisor does not restart it, because stopping it cancels the runs it hosts; it starts on the new value when it next restarts itself (%s), and `yoyo stop` then `yoyo start` restarts it now", changed, self.RestartsItself())
		return
	}
	if ahead := s.restarting(child); ahead != "" {
		state.Settings = fmt.Sprintf("%s; restarted on the new value once the %s service has moved, one part at a time", changed, ahead)
		return
	}
	if held := s.restartHold(); held != "" {
		state.Settings = fmt.Sprintf("%s; not restarted while %s", changed, held)
		return
	}
	if deployable, ok := child.(Deployable); ok {
		busy, err := deployable.Busy(ctx)
		if err != nil {
			state.Settings = fmt.Sprintf("%s; not restarted, because whether it is in the middle of anything could not be read: %v", changed, err)
			return
		}
		if busy != "" {
			state.Settings = fmt.Sprintf("%s; restarted on the new value once it finishes %s", changed, busy)
			return
		}
	}
	s.restartForSettings(ctx, child, state, now, changed)
}

// restartForSettings restarts a child so it starts on the configuration as it
// stands: replaced where it starts its replacement first, and otherwise stopped
// between two passes and started again, as a deploy restarts it.
func (s *Supervisor) restartForSettings(ctx context.Context, child Child, state *runstate.SupervisedChild, now time.Time, changed string) {
	if replacer, ok := child.(Replaces); ok {
		s.log("the %s service holds a setting that has changed (%s); starting its replacement on the new value, and stopping it once the replacement is ready", child.Name(), changed)
		ensured, err := replacer.Replace(ctx)
		if err != nil {
			state.Settings = fmt.Sprintf("%s; its replacement on the new value could not be made ready, so it is left serving and the replacement is tried again at the next look: %v", changed, err)
			s.log("the %s service %s", child.Name(), state.Settings)
			return
		}
		s.sayEarlier(child.Name(), ensured.Earlier)
		state.State = runstate.ChildRunning
		state.Reason = ""
		state.PID = ensured.PID
		if ensured.Log != "" {
			state.Log = ensured.Log
		}
		state.Reattached = false
		state.StartedAt = now
		state.Starts++
		s.serving(ctx, child, state)
		s.settingsApplied(child, state, now, changed, "was replaced by one started on the new value, ready before the old one was stopped")
		return
	}
	release := func() {}
	if between, ok := child.(Passes); ok {
		held, pass, err := between.HoldBetweenPasses(ctx)
		switch {
		case err != nil:
			state.Settings = fmt.Sprintf("%s; not restarted, because it could not be held between two passes: %v", changed, err)
			return
		case pass != "":
			state.Settings = fmt.Sprintf("%s; restarted on the new value once it finishes %s", changed, pass)
			return
		}
		release = held
	}
	s.log("the %s service holds a setting that has changed (%s); restarting it so it starts on the new value", child.Name(), changed)
	state.SettingsRestarting = true
	if s.applying == nil {
		s.applying = map[config.ServiceName]string{}
	}
	s.applying[child.Name()] = changed
	state.Settings = changed + "; restarting it so it starts on the new value"
	stopped, err := child.Stop(ctx)
	release()
	if err != nil {
		state.SettingsRestarting = false
		state.Settings = fmt.Sprintf("%s; it could not be stopped to restart it, and is tried again at the next look: %v", changed, err)
		s.log("the %s service %s", child.Name(), state.Settings)
		return
	}
	if stopped.Detail != "" {
		state.Settings = fmt.Sprintf("%s; asked to stop so it starts on the new value; %s, and it is started again once it has let go of its lease", changed, stopped.Detail)
		return
	}
	s.start(ctx, child, state, now)
}

// startedOnSettings records a child started again after it was stopped to
// apply a changed setting.
func (s *Supervisor) startedOnSettings(child Child, state *runstate.SupervisedChild, now time.Time) {
	changed := s.applying[child.Name()]
	delete(s.applying, child.Name())
	s.settingsApplied(child, state, now, changed, "was started again on the new value")
}

// SupervisedCopy reports whether a part's running copy is the one the
// supervisor started: for the dashboard, by the start records the supervisor
// keeps of the dashboards it started (startedDashboards, by process), and for
// any other part by the process the supervisor's record names.
func SupervisedCopy(drift runstate.SettingsDrift, record runstate.Supervision, startedDashboards map[int]bool) bool {
	if drift.Service == string(config.ServiceDashboard) {
		return startedDashboards[drift.PID]
	}
	for _, child := range record.Children {
		if string(child.Service) == drift.Service && child.PID == drift.PID {
			return true
		}
	}
	return false
}

// SettingsDriftWhy is what becomes of a running part whose settings changed
// under it, in the words `yoyo doctor` and `yoyo status` both say it: where the
// supervisor runs that copy, where its restart stands as the supervisor last
// recorded it; and otherwise why the supervisor is not the one to restart it.
// supervised is whether the copy is the one the supervisor started.
func SettingsDriftWhy(drift runstate.SettingsDrift, supervisorRunning bool, record runstate.Supervision, supervised bool) string {
	if !supervised {
		return fmt.Sprintf("it was not started by the supervisor, so the supervisor leaves it running and does not restart it; stopping it (`kill %d`) and starting it again by hand is what starts it on the new value", drift.PID)
	}
	if !supervisorRunning {
		return "no supervisor is running to restart it; `yoyo start` starts one, which replaces it with a copy on the new value at its first look at settings"
	}
	for _, child := range record.Children {
		if string(child.Service) != drift.Service || child.PID != drift.PID {
			continue
		}
		if child.Settings != "" {
			return "the supervisor's account of it: " + child.Settings
		}
	}
	return fmt.Sprintf("the supervisor restarts it on the new value at its next look at settings, at most %s from now", DeployEvery)
}

// settingsApplied records a restart made to apply a changed setting, apart
// from the failures the restart bound counts.
func (s *Supervisor) settingsApplied(child Child, state *runstate.SupervisedChild, now time.Time, changed, how string) {
	state.SettingsRestarting = false
	state.SettingsRestarts++
	state.SettingsRestartedAt = now
	state.SettingsApplied = changed
	state.Settings = ""
	s.log("the %s service %s (%s): restart %d to apply a changed setting, a restart rather than a death, so its restart bound is untouched", child.Name(), how, changed, state.SettingsRestarts)
}
