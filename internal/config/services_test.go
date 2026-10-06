package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// Every part of the product is present whether or not a project mentions it,
// at a harness default with an origin that says so: the section is the
// product's shape rather than something a project opts into, and `config show
// --origins` has to be able to name where each value came from.
func TestAProjectThatSaysNothingAboutServicesGetsEveryServiceAtItsDefault(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig, nil)
	want := Services{
		Slack: Service{Enabled: false},
		Dashboard: DashboardService{
			Enabled:      false,
			Port:         DefaultDashboardPort,
			Bind:         DefaultDashboardBind,
			AllowedHosts: []string{},
			Token:        DashboardTokenGenerated,
		},
		Scheduler:   Service{Enabled: true},
		Maintenance: MaintenanceService{Enabled: true, Every: Duration(DefaultMaintenanceInterval)},
	}
	if !reflect.DeepEqual(resolved.Config.Services, want) {
		t.Fatalf("services = %+v, want the defaults %+v", resolved.Config.Services, want)
	}
	for _, key := range []string{
		"services.slack.enabled",
		"services.dashboard.enabled",
		"services.dashboard.port",
		"services.dashboard.bind",
		"services.dashboard.allowed_hosts",
		"services.dashboard.token",
		"services.scheduler.enabled",
		"services.maintenance.enabled",
		"services.maintenance.every",
	} {
		if origin := resolved.Origins[key]; origin != OriginDefault {
			t.Errorf("origin[%q] = %q, want %q", key, origin, OriginDefault)
		}
	}
	if enabled := resolved.Config.Services.EnabledServices(); !reflect.DeepEqual(enabled, []ServiceName{ServiceScheduler, ServiceMaintenance}) {
		t.Errorf("enabled services = %v, want the scheduler and the maintenance pass", enabled)
	}
}

// A section a project writes overrides field by field, and the origin of each
// value it wrote is the file, with the values it left alone still the harness's.
func TestServicesAreReadFieldByFieldWithTheirOrigins(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`slack:
  enabled: true
  channel: C0123456789
services:
  slack:
    enabled: true
  dashboard:
    enabled: true
    bind: 192.168.1.20
    allowed_hosts: [yoyo.local, 192.168.1.20]
    token: keychain
  scheduler:
    enabled: false
`, nil)
	services := resolved.Config.Services
	if !services.Slack.Enabled || !services.Dashboard.Enabled || services.Scheduler.Enabled || !services.Maintenance.Enabled {
		t.Fatalf("services = %+v, want slack and the dashboard on, the scheduler off, and the maintenance pass left at its default", services)
	}
	if services.Dashboard.Bind != "192.168.1.20" || services.Dashboard.Token != DashboardTokenKeychain {
		t.Errorf("dashboard = %+v, want the bind and token the file wrote", services.Dashboard)
	}
	if services.Dashboard.Port != DefaultDashboardPort {
		t.Errorf("dashboard port = %d, want the default %d kept under a layer that did not restate it", services.Dashboard.Port, DefaultDashboardPort)
	}
	if !reflect.DeepEqual(services.Dashboard.AllowedHosts, []string{"yoyo.local", "192.168.1.20"}) {
		t.Errorf("allowed hosts = %v, want the file's list", services.Dashboard.AllowedHosts)
	}
	if services.Dashboard.Loopback() {
		t.Errorf("Loopback() = true for %s", services.Dashboard.Bind)
	}
	for key, want := range map[string]string{
		"services.slack.enabled":           resolved.Path,
		"services.dashboard.enabled":       resolved.Path,
		"services.dashboard.bind":          resolved.Path,
		"services.dashboard.allowed_hosts": resolved.Path,
		"services.dashboard.token":         resolved.Path,
		"services.scheduler.enabled":       resolved.Path,
		"services.dashboard.port":          OriginDefault,
		"services.maintenance.enabled":     OriginDefault,
	} {
		if origin := resolved.Origins[key]; origin != want {
			t.Errorf("origin[%q] = %q, want %q", key, origin, want)
		}
	}
	if enabled := services.EnabledServices(); !reflect.DeepEqual(enabled, []ServiceName{ServiceSlack, ServiceDashboard, ServiceMaintenance}) {
		t.Errorf("enabled services = %v", enabled)
	}
}

// A name that is not one of the four is refused, and the refusal names the
// four rather than the Go type the decoder would have named.
func TestAnUnknownServiceNameIsRefusedAndTheServicesAreNamed(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`services:
  scheduler:
    enabled: true
  watchdog:
    enabled: true
`, nil)
	if err == nil {
		t.Fatal("LoadResolved() = nil, want a service the product does not have refused")
	}
	for _, want := range []string{`"watchdog"`, "not a service the product has", `"slack", "dashboard", "scheduler", "maintenance"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want %q in it", err, want)
		}
	}
	if strings.Contains(err.Error(), "servicesDocument") {
		t.Errorf("error = %q, want the services named rather than the Go type", err)
	}
}

// Everything the section can get wrong is refused at load, whether or not the
// service is on, with the reason on the refusal. A bind outside loopback with a
// generated token is the one that matters most: the opt-in exists so another
// device can reach the page, and a token printed to this terminal is exactly
// what that device cannot read.
func TestServicesAreValidatedLikeEveryOtherKey(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		section string
		want    string
	}{
		"a port below the range": {
			section: "services:\n  dashboard:\n    port: 0\n",
			want:    "services.dashboard.port 0 must be between 1 and 65535",
		},
		"a port above the range": {
			section: "services:\n  dashboard:\n    port: 65536\n",
			want:    "services.dashboard.port 65536 must be between 1 and 65535",
		},
		"a bind that is not an address": {
			section: "services:\n  dashboard:\n    bind: localhost\n",
			want:    `services.dashboard.bind "localhost" must be an IP address`,
		},
		"an empty bind": {
			section: "services:\n  dashboard:\n    bind: \"\"\n",
			want:    "services.dashboard.bind is required",
		},
		"a bind outside loopback with a generated token": {
			section: "services:\n  dashboard:\n    bind: 192.168.1.20\n",
			want:    `services.dashboard binds 192.168.1.20, which is not a loopback address, and its token is "generated"`,
		},
		"a wildcard bind with a generated token": {
			section: "services:\n  dashboard:\n    bind: 0.0.0.0\n",
			want:    "services.dashboard binds 0.0.0.0, which is not a loopback address",
		},
		"a token source the harness does not have": {
			section: "services:\n  dashboard:\n    token: vault\n",
			want:    `services.dashboard.token "vault" is not a token source the harness has; the sources are "generated", "keychain", "file"`,
		},
		"an allowed host with a port on it": {
			section: "services:\n  dashboard:\n    allowed_hosts: [\"yoyo.local:8765\"]\n",
			want:    `services.dashboard.allowed_hosts "yoyo.local:8765" must be a host name or an IP address`,
		},
		"an allowed host with a scheme": {
			section: "services:\n  dashboard:\n    allowed_hosts: [\"http://yoyo.local\"]\n",
			want:    `services.dashboard.allowed_hosts "http://yoyo.local" must be a host name`,
		},
		"an empty allowed host": {
			section: "services:\n  dashboard:\n    allowed_hosts: [\"\"]\n",
			want:    "services.dashboard.allowed_hosts entry 0 is empty",
		},
		"an allowed host named twice": {
			section: "services:\n  dashboard:\n    allowed_hosts: [yoyo.local, YOYO.local]\n",
			want:    `services.dashboard.allowed_hosts names "YOYO.local" twice`,
		},
		"the slack service over reporting that is off": {
			section: "services:\n  slack:\n    enabled: true\n",
			want:    "services.slack is enabled and slack reporting is off",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := loadProjectError(t, minimalProjectConfig+tc.section, nil)
			if err == nil {
				t.Fatalf("LoadResolved() = nil, want %q refused", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want %q in it", err, tc.want)
			}
		})
	}
}

// The opt-in loads once its condition is met: a bind outside loopback with a
// stored token named, and the hosts another device would name. The wildcard is
// accepted too -- the design prefers an interface address and refuses nothing.
func TestANonLoopbackBindLoadsOnceATokenStoreIsNamed(t *testing.T) {
	t.Parallel()

	for name, section := range map[string]string{
		"an interface address with a keychain token": "services:\n  dashboard:\n    enabled: true\n    bind: 192.168.1.20\n    token: keychain\n    allowed_hosts: [yoyo.local]\n",
		"a wildcard with a file token":               "services:\n  dashboard:\n    enabled: true\n    bind: 0.0.0.0\n    token: file\n",
		"the IPv6 loopback with a generated token":   "services:\n  dashboard:\n    enabled: true\n    bind: \"::1\"\n",
		"a loopback bind with a stored token anyway": "services:\n  dashboard:\n    bind: 127.0.0.1\n    token: file\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := loadProjectError(t, minimalProjectConfig+section, nil); err != nil {
				t.Fatalf("LoadResolved() error = %v, want the section to load", err)
			}
		})
	}
}

// A layer that writes a service says only what it writes. The bundle states no
// services, so a project extending it and switching the dashboard on keeps the
// harness's port and bind under it rather than losing them.
func TestAServiceOverrideKeepsWhatItDidNotRestate(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`services:
  dashboard:
    enabled: true
`, nil)
	dashboard := resolved.Config.Services.Dashboard
	if !dashboard.Enabled || dashboard.Port != DefaultDashboardPort || dashboard.Bind != DefaultDashboardBind || dashboard.Token != DashboardTokenGenerated {
		t.Fatalf("dashboard = %+v, want it switched on with every other value at its default", dashboard)
	}
	if !dashboard.Loopback() {
		t.Errorf("Loopback() = false for the default bind")
	}
}

// The maintenance pass's cadence is read from the file with its origin, and a
// cadence under a minute is refused naming the floor: every pass reads the
// tracker and asks the forge, so a cadence of seconds is load.
func TestTheMaintenanceCadenceIsReadAndHasAFloor(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`services:
  maintenance:
    enabled: true
    every: 30m
`, nil)
	if got := resolved.Config.Services.Maintenance.Every.Duration(); got != 30*time.Minute {
		t.Errorf("services.maintenance.every = %s, want 30m", got)
	}
	if origin := resolved.Origins["services.maintenance.every"]; origin == OriginDefault {
		t.Errorf("origin of services.maintenance.every = %q, want the project file's", origin)
	}

	_, err := loadProjectError(t, minimalProjectConfig+`services:
  maintenance:
    every: 30s
`, nil)
	if err == nil || !strings.Contains(err.Error(), "services.maintenance.every is 30s") || !strings.Contains(err.Error(), "the shortest interval allowed is 1m") {
		t.Errorf("a 30s cadence loaded as %v, want it refused naming the floor", err)
	}
}

// A recurring task named for the maintenance pass would share its cadence
// claim in the sweep log, so it is refused.
func TestARecurringTaskMayNotTakeTheMaintenancePassesName(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`recurring_tasks:
  maintenance:
    role: development-manager
    every: 1h
    enabled: true
    prompt: |
      Sweep.
`, nil)
	if err == nil || !strings.Contains(err.Error(), `recurring task "maintenance" is named for the product's maintenance pass`) {
		t.Errorf("a recurring task named maintenance loaded as %v, want it refused", err)
	}
}
