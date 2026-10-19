package doctor

// Whether every running part of the product can read the configuration as it
// stands now.
//
// The configuration finding above says this build reads the file. A part of
// the product started days ago runs the build it was started from, and a key
// a landing added since is one that build refuses: on 2026-09-28 the dashboard
// served nothing but the decoder's error for two hours after the effort lines
// landed, while the watch and the installed binary had been checked and read
// the file fine. Each long-running part records, as it starts, the keys its
// build reads; this compares the file against every part still running and
// names the part, its build, and the keys.
//
// The scheduler is the one part here that would stop work: a watch that
// cannot read the configuration chooses nothing. So a mismatch on it is a
// problem, and one on any other part is a warning, for the reason every
// service finding is — a sink that cannot read the file reports nothing and a
// dashboard serves an error, and no run is stopped by either.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/supervise"
)

const configReadersCheck = "config-readers"

// checkConfigReaders is one finding per running part that cannot read the
// file, or one finding saying every part that recorded what it reads can.
func (d *diagnosis) checkConfigReaders(resolved config.Resolved) []Finding {
	root, err := d.stateRootPath()
	if err != nil {
		return []Finding{{
			Check:   configReadersCheck,
			Status:  StatusWarning,
			Summary: "whether the running parts of the product can read the configuration was not checked: the state root their records are under could not be found",
			Detail:  err.Error(),
			Remedy:  "export YOYODYNE_STATE_HOME=$HOME/.local/state/yoyodyne",
		}}
	}
	store, err := runstate.NewConfigReaderStore(root, resolved.Config.Product.ID)
	if err != nil {
		return []Finding{{
			Check:   configReadersCheck,
			Status:  StatusWarning,
			Summary: "whether the running parts of the product can read the configuration was not checked",
			Detail:  err.Error(),
		}}
	}
	if d.env.ProcessRunning != nil {
		store = store.WithProcessCheck(d.env.ProcessRunning)
	}
	readers, readersErr := store.Running()
	mismatches, mismatchErr := store.Mismatches()
	var findings []Finding
	for _, mismatch := range mismatches {
		status := StatusWarning
		if mismatch.Service == string(config.ServiceScheduler) {
			status = StatusProblem
		}
		findings = append(findings, Finding{
			Check:   configReadersCheck + ":" + mismatch.Service,
			Status:  status,
			Summary: mismatch.Says(),
			Detail:  runstate.ConfigMismatchRemedy(mismatch.Service),
			Remedy:  configMismatchCommand(mismatch, supervisedDashboard(root, resolved.Config.Product.ID)),
		})
	}
	if mismatchErr != nil {
		findings = append(findings, Finding{
			Check:   configReadersCheck,
			Status:  StatusWarning,
			Summary: "whether every running part of the product can read the configuration was not fully checked",
			Detail:  mismatchErr.Error(),
		})
	}
	if len(findings) > 0 {
		return findings
	}
	if readersErr == nil && len(readers) == 0 {
		return []Finding{{
			Check:   configReadersCheck,
			Status:  StatusOK,
			Summary: "no running part of the product has recorded which configuration keys its build reads, so there is nothing to compare the file against",
			Detail:  "a part records them as it starts; one started from a build older than the record says nothing here",
		}}
	}
	described := make([]string, 0, len(readers))
	for _, reader := range readers {
		build := "a build that recorded no revision"
		if reader.Build != "" {
			build = "build " + shortRevision(reader.Build)
		}
		described = append(described, fmt.Sprintf("%s on %s", reader.Service, build))
	}
	return []Finding{{
		Check:   configReadersCheck,
		Status:  StatusOK,
		Summary: "every running part of the product reads every key in the configuration",
		Detail:  strings.Join(described, "; "),
	}}
}

const changedSettingsCheck = "changed-settings"

// checkChangedSettings is one finding per running part holding a setting from
// its start that the configuration has since changed: which part, which
// setting, and what becomes of it — the supervisor restarting it, where its
// restart stands, or why the supervisor is not the one to — in the words
// `yoyo status` says it in. A part the supervisor is about to restart is a
// note; one nothing will restart is a warning, since it goes on serving on the
// old value until somebody does.
func (d *diagnosis) checkChangedSettings(resolved config.Resolved) []Finding {
	root, err := d.stateRootPath()
	if err != nil {
		return nil
	}
	productID := resolved.Config.Product.ID
	store, err := runstate.NewConfigReaderStore(root, productID)
	if err != nil {
		return nil
	}
	if d.env.ProcessRunning != nil {
		store = store.WithProcessCheck(d.env.ProcessRunning)
	}
	drifts, driftErr := store.SettingsDrift()
	var findings []Finding
	if len(drifts) > 0 {
		var running bool
		var record runstate.Supervision
		if supervision, err := runstate.NewSupervisionStore(root, productID); err == nil {
			running, _ = supervision.Running()
			record, _, _ = supervision.Load()
		}
		started := map[int]bool{}
		if dashboards, err := runstate.NewDashboardStore(root, productID); err == nil {
			instances, _ := dashboards.StartedBySupervisor()
			for _, instance := range instances {
				started[instance.PID] = true
			}
		}
		for _, drift := range drifts {
			supervised := supervise.SupervisedCopy(drift, record, started)
			status := StatusWarning
			remedy := "yoyo stop && yoyo start"
			switch {
			case supervised && running:
				status, remedy = StatusOK, ""
			case !supervised:
				remedy = fmt.Sprintf("kill %d", drift.PID)
			}
			findings = append(findings, Finding{
				Check:   changedSettingsCheck + ":" + drift.Service,
				Status:  status,
				Summary: drift.Says(),
				Detail:  supervise.SettingsDriftWhy(drift, running, record, supervised),
				Remedy:  remedy,
			})
		}
	}
	if driftErr != nil {
		findings = append(findings, Finding{
			Check:   changedSettingsCheck,
			Status:  StatusWarning,
			Summary: "whether a setting a running part of the product holds from its start has changed was not fully checked",
			Detail:  driftErr.Error(),
		})
	}
	return findings
}

// configMismatchCommand is what restarts the part now. The watch, the sink,
// and the product's dashboard are moved onto a deployed build by the harness
// in their own time; the command is for not waiting. A dashboard somebody
// started by hand is theirs to start again.
func configMismatchCommand(mismatch runstate.ConfigMismatch, supervised int) string {
	if mismatch.Service == string(config.ServiceDashboard) && mismatch.PID != supervised {
		return fmt.Sprintf("kill %d && yoyo dashboard", mismatch.PID)
	}
	return "yoyo stop && yoyo start"
}

// supervisedDashboard is the process of the dashboard the product's supervisor
// last started, and 0 where none is recorded.
func supervisedDashboard(root string, productID domain.ProductID) int {
	store, err := runstate.NewDashboardStore(root, productID)
	if err != nil {
		return 0
	}
	start, found, err := store.Start()
	if err != nil || !found {
		return 0
	}
	return start.PID
}
