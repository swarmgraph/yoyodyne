package config

// The parts of the product, declared in one place.
//
// Slack, the dashboard, the scheduler, and the maintenance pass are parts of one
// product rather than independent small tools: the management-and-supervision
// design's supervision tree has one supervisor per product, and these are its
// children. What is declared here is which of them this product runs and, for
// the one that listens on a port, where. Nothing here starts anything — the
// supervisor command reads the section, and it is what starts and stops the
// enabled children together — so this section is the declaration and only the
// declaration, validated like every other key so a mistake is found when the
// file loads rather than on the day the product is started.
//
// It is configuration in the sense every other section is: it selects which
// registered parts run and narrows nothing else. A service enabled here holds
// exactly what that service holds when started by hand — a sink still reads
// its tokens from the store only its own launch looks at, a dashboard still
// refuses every request without its bearer token — and there is no key here
// that grants a capability, a tool, or an authority.

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Services is what a product declares about its parts. Every service is present
// whether or not it is enabled, because the set of parts is the product's shape
// rather than a list it appends to: a service missing from the file is one at
// its default rather than one the product does not have.
type Services struct {
	// Slack is the reporting sink, the `yoyo slack` process that holds this
	// product's two tokens. Enabling it as a service requires reporting to be
	// switched on under the top-level `slack` section, because a sink started
	// for a project that reports nothing has nowhere to post and refuses to run.
	Slack Service `yaml:"slack" json:"slack"`
	// Dashboard is the read-only projection of the read model served to a
	// browser. It is the one service with more to say than a switch: the port it
	// serves on, the address it binds, the hosts a request may name, and where
	// its bearer token comes from.
	Dashboard DashboardService `yaml:"dashboard" json:"dashboard"`
	// Scheduler is the watch loop — `yoyo work --watch` — which reads the queue
	// and starts what is ready, for as long as the product is running.
	Scheduler Service `yaml:"scheduler" json:"scheduler"`
	// Maintenance is the periodic pass that keeps the installation converged:
	// reconciling interrupted runs, catching the checkout up, and the rest of
	// what the interim maintenance job did by hand. It is the supervisor's own
	// pass rather than a process of its own, and its one setting is how often.
	Maintenance MaintenanceService `yaml:"maintenance" json:"maintenance"`
}

// Service is a part of the product with nothing to say but whether it runs.
type Service struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// MaintenanceService is the periodic pass's entry: whether it runs, and its
// cadence. The cadence is measured from the last pass rather than against a
// wall-clock grid, as a recurring task's is, so a machine that slept through
// the night owes nobody the passes it missed.
type MaintenanceService struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Every is how often the pass runs. It has a floor rather than a ceiling:
	// every pass runs `yoyo reconcile`, which reads the tracker and asks the
	// forge about every unsettled publication, and a cadence of seconds would be
	// that load for nothing, since what the pass settles moves on the scale of
	// runs finishing.
	Every Duration `yaml:"every" json:"every"`
	// TrackerCleanup is when the pass cleans up the work tracker's store, and
	// for how long a cleanup may hold it.
	TrackerCleanup TrackerCleanup `yaml:"tracker_cleanup" json:"tracker_cleanup"`
}

// TrackerCleanup is when the maintenance pass cleans up the work tracker's
// store in the primary checkout: once the store has grown GrownMegabytes since
// the last cleanup left it, or Every has passed since that cleanup, whichever
// comes first. A store the harness has never cleaned up is cleaned up at the
// first pass. bd never reclaims the room its own changes leave behind, and
// every tracker call opens the whole store, so a store nobody cleans up makes
// every call slower and every wait behind bd's lock longer (internal/beads,
// cleanup.go).
//
// The growth is measured from what the last cleanup left rather than as a size
// the store may not pass, because a cleanup keeps the whole history: a store
// whose history alone outgrew a fixed size would be cleaned up on every pass
// and shrink by nothing.
type TrackerCleanup struct {
	// Every is the longest the store goes without a cleanup.
	Every Duration `yaml:"every" json:"every"`
	// GrownMegabytes is how much the store may grow past what the last cleanup
	// left before the next is due.
	GrownMegabytes int `yaml:"grown_megabytes" json:"grown_megabytes"`
	// MaxHold is the longest one cleanup holds the store. Every tracker call
	// waits behind it, so it is stopped when this runs out and the next pass
	// finishes the work; it has a ceiling well inside the limit a tracker call
	// is given, so a call that arrives the moment a cleanup starts still
	// answers inside its own.
	MaxHold Duration `yaml:"max_hold" json:"max_hold"`
}

const (
	// DefaultTrackerCleanupInterval is a day: the cleanup of a day's changes
	// takes a second or two, and the store never carries more than a day's
	// unreclaimed room.
	DefaultTrackerCleanupInterval = 24 * time.Hour
	// MinTrackerCleanupInterval is the shortest a project may set. A cleanup
	// holds the store every other call waits on, so a cadence of minutes would
	// be waits for nothing.
	MinTrackerCleanupInterval = time.Hour
	// DefaultTrackerCleanupGrownMegabytes is how much growth brings a cleanup
	// forward. The store in the primary checkout carried some 360 MB of
	// unreclaimed room after two months without one; a day of ordinary work
	// adds a few megabytes, so this is reached only by a day far busier than
	// usual.
	DefaultTrackerCleanupGrownMegabytes = 128
	// MaxTrackerCleanupGrownMegabytes bounds the growth a project may allow, at
	// a size whose cleanup no longer fits in any hold allowed below.
	MaxTrackerCleanupGrownMegabytes = 16 << 10
	// DefaultTrackerCleanupMaxHold is how long a cleanup may hold the store.
	// Cleaning up the 885 MB store took nine seconds.
	DefaultTrackerCleanupMaxHold = 15 * time.Second
	// MaxTrackerCleanupHold is the ceiling on that hold: two thirds of the
	// thirty seconds a tracker call is given (internal/beads), so a call that
	// arrives the moment a cleanup starts has ten seconds to answer once it is
	// stopped, against the two or so an ordinary call takes.
	MaxTrackerCleanupHold = 20 * time.Second
)

// DefaultTrackerCleanup is the cleanup a project that says nothing gets.
func DefaultTrackerCleanup() TrackerCleanup {
	return TrackerCleanup{
		Every:          Duration(DefaultTrackerCleanupInterval),
		GrownMegabytes: DefaultTrackerCleanupGrownMegabytes,
		MaxHold:        Duration(DefaultTrackerCleanupMaxHold),
	}
}

const (
	// DefaultMaintenanceInterval is how often the pass runs when a project says
	// nothing. It is the cadence the interim maintenance job ran on, which was
	// long enough to settle a finished run promptly without reading the tracker
	// and the forge more than a few times an hour.
	DefaultMaintenanceInterval = 10 * time.Minute
	// MinMaintenanceInterval is the shortest cadence a project may set.
	MinMaintenanceInterval = time.Minute
	// MaintenanceTaskName is the name the pass records its passes under in the
	// sweep log, beside the recurring tasks. It is reserved: a recurring task
	// written under it would share the pass's cadence claim, so one is refused
	// when the configuration loads.
	MaintenanceTaskName = "maintenance"
)

// DashboardService is the dashboard's entry. The bind address and the allowed
// hosts are the observability-and-dashboard design's web-security conventions
// made configuration: the defaults are loopback and the loopback Host, and a
// project that says nothing gets exactly the behaviour the standalone command
// had. A bind outside loopback loads where its token is stored, and the
// dashboard refuses to listen on it (dashboard.LoopbackBind): it listens only
// on this machine.
type DashboardService struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Port is the TCP port the dashboard serves on. It is a fixed number rather
	// than one the operating system chooses, because a supervised child that
	// came up on a different port after every restart is one nobody can
	// bookmark or reach from another device.
	Port int `yaml:"port" json:"port"`
	// Bind is the address the listener binds, as an IP literal. The default is
	// the IPv4 loopback address by name rather than "localhost", which on some
	// machines resolves to the IPv6 loopback first. An address that is not
	// loopback is refused when the dashboard starts.
	Bind string `yaml:"bind" json:"bind"`
	// AllowedHosts are the hosts, beyond the bound address itself, a request may
	// name in its Host header and Origin. Empty means the bound address alone —
	// with `localhost` beside it under the loopback default, which is what the
	// standalone command already accepted. A name is written here without a
	// port; the port is the one above.
	AllowedHosts []string `yaml:"allowed_hosts" json:"allowed_hosts"`
	// Token is where the bearer token every request has to carry comes from. It
	// is a reference and never the token: the configuration is committed, and a
	// secret in it would be a secret in the repository. `generated` is the
	// default and the loopback arrangement — a token made at each start and
	// printed once where the operator can read it — and it is refused for a bind
	// outside loopback, because a token printed to one terminal is unusable from
	// another device. `keychain` and `file` name the two stores the Slack tokens
	// already use, under names that carry the product.
	Token DashboardTokenSource `yaml:"token" json:"token"`
}

// DashboardTokenSource is where the dashboard's bearer token is read from.
type DashboardTokenSource string

const (
	// DashboardTokenGenerated is a token made at each start and printed once,
	// which is what a loopback dashboard has always had.
	DashboardTokenGenerated DashboardTokenSource = "generated"
	// DashboardTokenKeychain is the macOS keychain, under an item named for the
	// product beside the two Slack tokens.
	DashboardTokenKeychain DashboardTokenSource = "keychain"
	// DashboardTokenFile is a file under the state root, named for the product,
	// which is the store a machine with no keychain has.
	DashboardTokenFile DashboardTokenSource = "file"
)

// DashboardTokenSources lists the sources a project may name, in the order a
// refusal names them.
var DashboardTokenSources = []DashboardTokenSource{DashboardTokenGenerated, DashboardTokenKeychain, DashboardTokenFile}

// Valid reports whether the source is one the harness has.
func (s DashboardTokenSource) Valid() bool {
	for _, known := range DashboardTokenSources {
		if s == known {
			return true
		}
	}
	return false
}

// ServiceName names one part of the product, which is how the services are
// listed and how a refusal names the one that was mistyped.
type ServiceName string

const (
	ServiceSlack       ServiceName = "slack"
	ServiceDashboard   ServiceName = "dashboard"
	ServiceScheduler   ServiceName = "scheduler"
	ServiceMaintenance ServiceName = "maintenance"
)

// ServiceNames is every service a product has, in the order the section is
// written and reported.
var ServiceNames = []ServiceName{ServiceSlack, ServiceDashboard, ServiceScheduler, ServiceMaintenance}

const (
	// DefaultDashboardPort is the port a dashboard serves on when nothing names
	// another. It is the one the operations guide has used as its example of a
	// port worth bookmarking since the standalone command existed.
	DefaultDashboardPort = 8765
	// DefaultDashboardBind is the loopback address, by number rather than by the
	// name "localhost", for the reason the standalone command binds it that way.
	DefaultDashboardBind = "127.0.0.1"
	// MaxAllowedHostBytes bounds one allowed host at what a host name may be.
	MaxAllowedHostBytes = 253
)

// DefaultServices is the shape a project that writes nothing gets, and what a
// configuration built by hand states to be validated as a loaded one is. The
// two services that need nothing outside the file — the scheduler and the
// maintenance pass, which are the harness's own loop and its self-maintenance
// — are on, because a product started with neither is a product that starts
// nothing. The two that need something outside it are off: Slack needs a
// workspace, an app, and two stored tokens, and the dashboard needs a port
// somebody means to open; each is switched on by the operator who arranged
// that, as reporting itself is.
func DefaultServices() Services {
	return Services{
		Slack: Service{Enabled: false},
		Dashboard: DashboardService{
			Enabled: false,
			Port:    DefaultDashboardPort,
			Bind:    DefaultDashboardBind,
			// Empty and not nil, so the effective configuration reports the
			// bound address alone as `[]` rather than as a value that is missing.
			AllowedHosts: []string{},
			Token:        DashboardTokenGenerated,
		},
		Scheduler:   Service{Enabled: true},
		Maintenance: MaintenanceService{Enabled: true, Every: Duration(DefaultMaintenanceInterval), TrackerCleanup: DefaultTrackerCleanup()},
	}
}

// Enabled reports whether the named service is switched on. A name the product
// has no service for is off, which is the answer that starts nothing.
func (s Services) Enabled(name ServiceName) bool {
	switch name {
	case ServiceSlack:
		return s.Slack.Enabled
	case ServiceDashboard:
		return s.Dashboard.Enabled
	case ServiceScheduler:
		return s.Scheduler.Enabled
	case ServiceMaintenance:
		return s.Maintenance.Enabled
	}
	return false
}

// EnabledServices lists the services switched on, in the section's own order.
func (s Services) EnabledServices() []ServiceName {
	var enabled []ServiceName
	for _, name := range ServiceNames {
		if s.Enabled(name) {
			enabled = append(enabled, name)
		}
	}
	return enabled
}

// The settings a part reads once, as it starts, and holds for as long as it
// runs, by their key in the configuration. A change to one reaches the part only
// when it is started again, which is what the supervisor does when it finds one
// changed (internal/supervise/settings.go). What a running dashboard re-reads
// for itself — who it accepts: the people bound under operators, their tokens,
// its own stored token, and services.dashboard.allowed_hosts — is not among
// them, because a change to any of those is applied without a restart.
const (
	SettingDashboardPort  = "services.dashboard.port"
	SettingDashboardBind  = "services.dashboard.bind"
	SettingDashboardToken = "services.dashboard.token"
)

// StartSettings is each setting the named part records as held from its start,
// with its value here as the part would read it. The dashboard binds its port
// and address and reads where its token comes from once, and records all three;
// one started by hand records only its token source, because it serves on the
// port it was given and binds loopback. The sink and the scheduler record none
// yet, so a change to a setting either holds from its start is not compared
// with anything.
func (s Services) StartSettings(name ServiceName) map[string]string {
	switch name {
	case ServiceDashboard:
		return map[string]string{
			SettingDashboardPort:  strconv.Itoa(s.Dashboard.Port),
			SettingDashboardBind:  strings.TrimSpace(s.Dashboard.Bind),
			SettingDashboardToken: string(s.Dashboard.Token),
		}
	}
	return nil
}

// Loopback reports whether the dashboard's bind address is a loopback address,
// which is the line between the default arrangement and the opt-in. An address
// that does not parse is not loopback; it is refused separately.
func (d DashboardService) Loopback() bool {
	ip := net.ParseIP(strings.TrimSpace(d.Bind))
	return ip != nil && ip.IsLoopback()
}

// allowedHostPattern is the shape of a host name: labels of letters, digits,
// and hyphens, joined by dots. An IP literal is accepted beside it. What it
// refuses is anything that is not a host — a scheme, a path, a port — because a
// request's Host header is compared against these exactly, and an entry with a
// port in it would match nothing the browser sends.
var allowedHostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$`)

// problems reports everything wrong with the services section at once. Every
// value is checked whether or not its service is enabled, for the reason the
// Slack section is: a typo found now is one an operator fixes now rather than
// on the day the service is switched on.
func (s Services) problems(reporting Slack) []string {
	var problems []string
	// A sink started for a project whose reporting is off reads a stream and then
	// discovers it has nowhere to post, which the Slack section already refuses
	// at the sink. Refusing the declaration here says so before anything is
	// started, and names both ways out.
	if s.Slack.Enabled && !reporting.Enabled {
		problems = append(problems, "services.slack is enabled and slack reporting is off; a sink started for a project that reports nothing has nowhere to post, so set slack.enabled and slack.channel, or set services.slack.enabled to false")
	}
	problems = append(problems, s.Dashboard.problems()...)
	return append(problems, s.Maintenance.problems()...)
}

func (m MaintenanceService) problems() []string {
	var problems []string
	if m.Every.Duration() < MinMaintenanceInterval {
		problems = append(problems, fmt.Sprintf("services.maintenance.every is %s, and the shortest interval allowed is %s: every pass reads the tracker and asks the forge about every unsettled publication, so a shorter interval is load rather than maintenance",
			m.Every, Duration(MinMaintenanceInterval)))
	}
	return append(problems, m.TrackerCleanup.problems()...)
}

func (t TrackerCleanup) problems() []string {
	var problems []string
	if t.Every.Duration() < MinTrackerCleanupInterval {
		problems = append(problems, fmt.Sprintf("services.maintenance.tracker_cleanup.every is %s, and the shortest interval allowed is %s: a cleanup holds the tracker's store every other tracker call waits on, so a shorter interval is waiting for nothing",
			t.Every, Duration(MinTrackerCleanupInterval)))
	}
	if t.GrownMegabytes < 1 || t.GrownMegabytes > MaxTrackerCleanupGrownMegabytes {
		problems = append(problems, fmt.Sprintf("services.maintenance.tracker_cleanup.grown_megabytes is %d, and must be between 1 and %d",
			t.GrownMegabytes, MaxTrackerCleanupGrownMegabytes))
	}
	if hold := t.MaxHold.Duration(); hold < time.Second || hold > MaxTrackerCleanupHold {
		problems = append(problems, fmt.Sprintf("services.maintenance.tracker_cleanup.max_hold is %s, and must be between 1s and %s: every tracker call waits while a cleanup holds the store, and a hold longer than that leaves a call that arrives as it starts too little of its own thirty seconds to answer in",
			t.MaxHold, Duration(MaxTrackerCleanupHold)))
	}
	return problems
}

func (d DashboardService) problems() []string {
	var problems []string
	if d.Port < 1 || d.Port > 65535 {
		problems = append(problems, fmt.Sprintf("services.dashboard.port %d must be between 1 and 65535", d.Port))
	}
	bind := strings.TrimSpace(d.Bind)
	ip := net.ParseIP(bind)
	switch {
	case bind == "":
		problems = append(problems, fmt.Sprintf("services.dashboard.bind is required; %s is the loopback default", DefaultDashboardBind))
	case ip == nil:
		problems = append(problems, fmt.Sprintf("services.dashboard.bind %q must be an IP address, such as %s for loopback or the address of the interface another device reaches this machine on", d.Bind, DefaultDashboardBind))
	}
	if !d.Token.Valid() {
		problems = append(problems, fmt.Sprintf("services.dashboard.token %q is not a token source the harness has; the sources are %s", d.Token, describeTokenSources()))
	}
	// The opt-in and its condition. A generated token is printed to the terminal
	// the dashboard started in, which is the one place a browser on another
	// device cannot read, so a bind that another device can reach has to name
	// where its token is stored instead. Refused at load, with the reason: a
	// dashboard that came up on the network and printed its token to nobody
	// would be an opt-in that silently failed to be one.
	if ip != nil && !ip.IsLoopback() && d.Token == DashboardTokenGenerated {
		problems = append(problems, fmt.Sprintf("services.dashboard binds %s, which is not a loopback address, and its token is %q; a token generated at each start is printed to one terminal and unusable from another device, so set services.dashboard.token to %q or %q, or bind %s",
			bind, DashboardTokenGenerated, DashboardTokenKeychain, DashboardTokenFile, DefaultDashboardBind))
	}
	seen := make(map[string]struct{}, len(d.AllowedHosts))
	for index, host := range d.AllowedHosts {
		trimmed := strings.TrimSpace(host)
		switch {
		case trimmed == "":
			problems = append(problems, fmt.Sprintf("services.dashboard.allowed_hosts entry %d is empty", index))
			continue
		case len(trimmed) > MaxAllowedHostBytes:
			problems = append(problems, fmt.Sprintf("services.dashboard.allowed_hosts entry %d is %d bytes, limit is %d", index, len(trimmed), MaxAllowedHostBytes))
			continue
		case net.ParseIP(trimmed) == nil && !allowedHostPattern.MatchString(trimmed):
			problems = append(problems, fmt.Sprintf("services.dashboard.allowed_hosts %q must be a host name or an IP address, without a scheme, a port, or a path", host))
			continue
		}
		folded := strings.ToLower(trimmed)
		if _, duplicate := seen[folded]; duplicate {
			problems = append(problems, fmt.Sprintf("services.dashboard.allowed_hosts names %q twice", host))
			continue
		}
		seen[folded] = struct{}{}
	}
	return problems
}

// describeTokenSources lists the sources the way a refusal reads them back.
func describeTokenSources() string {
	named := make([]string, 0, len(DashboardTokenSources))
	for _, source := range DashboardTokenSources {
		named = append(named, fmt.Sprintf("%q", source))
	}
	return strings.Join(named, ", ")
}

// describeServiceNames lists the services, for a refusal of a name that is not
// one of them.
func describeServiceNames() string {
	named := make([]string, 0, len(ServiceNames))
	for _, name := range ServiceNames {
		named = append(named, fmt.Sprintf("%q", name))
	}
	return strings.Join(named, ", ")
}

// knownService reports whether a name is one of the four.
func knownService(name string) bool {
	for _, service := range ServiceNames {
		if name == string(service) {
			return true
		}
	}
	return false
}
