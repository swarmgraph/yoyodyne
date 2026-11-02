package readmodel

// Which models the provider's refusals hold closed at one moment, and until
// when, for whatever is about to send one invocation to one of them.
//
// A refusal closes its model until the reset the provider named. One that named
// none closes it too: sending fresh work into it at every pull is how one
// exhausted endpoint became a refused dispatch every few minutes. Capacity is
// re-established there only by a paced probe — one bounded invocation once the
// configured probe interval has passed since the later of the refusal and the
// last probe — and the probes are recorded (runstate.CapacityProbe), so the
// pacing survives a restart. A run parked on such a refusal probes the window
// itself on that same interval, so while one is parked nothing else is sent to
// probe beside it.
//
// Where the provider names the limit as one over the whole account rather than
// one model family — five_hour, seven_day — a refusal of one model closes every
// model of that provider the caller's endpoints name, because the window is
// shared by all of them (backend.AccountWideLimit). A turn served on any of
// those models after the refusal is the provider saying the shared window is
// open.
//
// The development dispatch and the recurring role turns both read this, so a
// role's turn and a developer run asking for the same model are held on one
// reading of one record.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// CapacityProbes is the durable record of the probes sent into windows refused
// with no reset time. It is satisfied by *runstate.CapacityProbeStore.
type CapacityProbes interface {
	List() ([]runstate.CapacityProbe, error)
}

// CapacityProbeRecorder is the same record, written as a probe is sent.
type CapacityProbeRecorder interface {
	CapacityProbes
	Record(probe runstate.CapacityProbe) error
}

// CapacityWindow is one model held closed on one account.
type CapacityWindow struct {
	Model string
	// Account is the account the refusal named, and empty where it named none,
	// which is read as every account.
	Account string
	// Until is the reset the provider named, or — where it named none — the next
	// probe time.
	Until time.Time
	// Kind is the provider's own name for the limit, and empty where it named
	// none.
	Kind string
	// NoReset is a refusal that named no reset time, whose Until is the harness's
	// next probe rather than anything the provider said.
	NoReset bool
	// Prober names a run parked on this refusal that probes it itself, and is
	// empty where none is. While one is, the window is closed whatever Until says.
	Prober string
	// Scope is every model one probe answers for: the refused model alone, or each
	// model under an account-wide limit. Sorted.
	Scope []string
}

// CapacityVerdict is what a window says of sending one invocation now.
type CapacityVerdict int

const (
	// CapacityOpen is no window standing over the models asked for, or one of
	// them served.
	CapacityOpen CapacityVerdict = iota
	// CapacityClosed is every model asked for held closed: the invocation is not
	// sent, and waits until the window's Until.
	CapacityClosed
	// CapacityProbeDue is a window refused with no reset whose next probe time
	// has come: one invocation may be sent as the probe, and whoever sends it
	// records it so nothing else is sent before the next interval.
	CapacityProbeDue
)

// CapacityWindows is every window standing at one reading.
type CapacityWindows struct {
	byModel  map[string][]CapacityWindow
	interval time.Duration
	now      time.Time
}

// CapacityWindowReading is what the windows are read from.
type CapacityWindowReading struct {
	// Refusals is every refusal the caller reads: the usage-limit log, the runs
	// parked on a limit, and the runs a limit ended.
	Refusals []runstate.UsageLimitExhaustion
	// Runs is the runs in flight, read for the ones parked on a refusal with no
	// reset, which probe it themselves.
	Runs []runstate.State
	// Probes is the probe record.
	Probes []runstate.CapacityProbe
	// Endpoints is every endpoint the caller may send to, which is where an
	// account-wide window finds the other models it closes.
	Endpoints []AgentEndpoint
	// Evidence lifts a refusal the provider has since served through.
	Evidence CapacityEvidence
	// Interval is execution.usage_limit_unknown_reset_pause: how long after a
	// refusal with no reset, or after the last probe of it, the next probe is due.
	// Zero reads no refusal without a reset as closing anything.
	Interval time.Duration
}

// ReadCapacityWindows reads the windows standing at now.
func ReadCapacityWindows(reading CapacityWindowReading, now time.Time) CapacityWindows {
	windows := CapacityWindows{byModel: map[string][]CapacityWindow{}, interval: reading.Interval, now: now}
	for _, refusal := range reading.Refusals {
		if refusal.Substituted() && refusal.Reason() != runstate.SubstitutedForCapacity {
			continue
		}
		model := strings.TrimSpace(refusal.Model)
		if model == "" {
			continue
		}
		scope := windowScope(refusal, reading.Endpoints)
		if lifted(reading.Evidence, refusal, scope) {
			continue
		}
		window := CapacityWindow{Account: strings.TrimSpace(refusal.AccountAlias), Kind: strings.TrimSpace(refusal.Kind), Scope: scope}
		switch {
		case refusal.ResetsAt != nil && refusal.ResetsAt.After(refusal.At):
			if !refusal.ResetsAt.After(now) {
				continue
			}
			window.Until = refusal.ResetsAt.UTC()
		case reading.Interval > 0:
			window.NoReset = true
			window.Until = lastProbe(reading.Probes, window.Account, scope, refusal.At).Add(reading.Interval).UTC()
		default:
			continue
		}
		windows.add(window)
	}
	for _, run := range reading.Runs {
		window, probing := selfProbing(run, reading.Endpoints)
		if !probing || lifted(reading.Evidence, runstate.UsageLimitExhaustion{At: pausedSince(run), Model: window.Model, AccountAlias: window.Account}, window.Scope) {
			continue
		}
		windows.add(window)
	}
	return windows
}

func (w CapacityWindows) add(window CapacityWindow) {
	for _, model := range window.Scope {
		each := window
		each.Model = model
		w.byModel[model] = append(w.byModel[model], each)
	}
}

// Empty reports a reading that holds nothing closed and has no probe due.
func (w CapacityWindows) Empty() bool { return len(w.byModel) == 0 }

// CapacityAsk is one model an invocation asks for, on the account it asks on.
// An account of "" is a caller that does not know which account it sends on,
// and every window of the model is read as its.
type CapacityAsk struct {
	Account string
	Model   string
}

// Ask says what the windows make of sending one invocation that asks for each
// of asks in order — its own model, then the alternate it fails over to. It is
// open where any of them is, because a model still served takes the turn.
// Otherwise a probe due on any of them is that probe, and the window returned is
// the one the probe answers for. Otherwise the invocation waits, and the window
// returned is the one that reopens first.
func (w CapacityWindows) Ask(asks ...CapacityAsk) (CapacityWindow, CapacityVerdict) {
	var due, closed *CapacityWindow
	for _, ask := range asks {
		model := strings.TrimSpace(ask.Model)
		if model == "" {
			continue
		}
		window, verdict := w.model(strings.TrimSpace(ask.Account), model)
		switch verdict {
		case CapacityOpen:
			return CapacityWindow{}, CapacityOpen
		case CapacityProbeDue:
			if due == nil {
				due = &window
			}
		case CapacityClosed:
			if closed == nil || window.Until.Before(closed.Until) {
				closed = &window
			}
		}
	}
	switch {
	case due != nil:
		return *due, CapacityProbeDue
	case closed != nil:
		return *closed, CapacityClosed
	}
	return CapacityWindow{}, CapacityOpen
}

// model is one model's verdict: closed while any window over it is, a probe due
// where only windows whose probe has come stand, and open otherwise.
func (w CapacityWindows) model(account, model string) (CapacityWindow, CapacityVerdict) {
	var latest, due *CapacityWindow
	for index := range w.byModel[model] {
		window := w.byModel[model][index]
		if account != "" && window.Account != "" && window.Account != account {
			continue
		}
		if window.Prober != "" || window.Until.After(w.now) {
			if latest == nil || window.Until.After(latest.Until) {
				latest = &window
			}
			continue
		}
		if window.NoReset && due == nil {
			due = &window
		}
	}
	switch {
	case latest != nil:
		return *latest, CapacityClosed
	case due != nil:
		return *due, CapacityProbeDue
	}
	return CapacityWindow{}, CapacityOpen
}

// Probed is the reading after a probe of window was sent at: every window with
// no reset over a model in its scope waits a full interval from then, so the
// rest of the pull that sent it sends nothing more into the same window.
func (w CapacityWindows) Probed(window CapacityWindow, at time.Time) {
	next := at.Add(w.interval).UTC()
	for _, model := range window.Scope {
		for index := range w.byModel[model] {
			each := &w.byModel[model][index]
			if !each.NoReset || (window.Account != "" && each.Account != "" && each.Account != window.Account) {
				continue
			}
			if next.After(each.Until) {
				each.Until = next
			}
		}
	}
}

// Probes is the records a probe of window sent at says: one per model in its
// scope, on its account.
func (window CapacityWindow) Probes(at time.Time, what string) []runstate.CapacityProbe {
	probes := make([]runstate.CapacityProbe, 0, len(window.Scope))
	for _, model := range window.Scope {
		probes = append(probes, runstate.CapacityProbe{AccountAlias: window.Account, Model: model, At: at.UTC(), What: what})
	}
	return probes
}

// Says is what a person is told about work this window holds: which models, the
// limit, and when it is next tried — the provider's reset where it named one,
// and the next probe where it did not.
func (window CapacityWindow) Says(models string) string {
	limit := "a usage limit"
	if window.Kind != "" {
		limit = "the " + window.Kind + " usage limit"
	}
	when := window.Until.UTC().Format(time.RFC3339)
	switch {
	case window.Prober != "":
		return fmt.Sprintf("%s holds %s closed with no reset time named, and %s is asking the provider again itself, next at %s", limit, models, window.Prober, when)
	case window.NoReset:
		return fmt.Sprintf("%s holds %s closed with no reset time named, so the provider is asked again by one probe, next at %s", limit, models, when)
	}
	return fmt.Sprintf("%s holds %s closed until %s", limit, models, when)
}

// windowScope is every model a refusal closes: its own, and under an
// account-wide limit every model of the same provider the endpoints name.
func windowScope(refusal runstate.UsageLimitExhaustion, endpoints []AgentEndpoint) []string {
	model := strings.TrimSpace(refusal.Model)
	scope := map[string]bool{model: true}
	if backend.AccountWideLimit(refusal.Kind) {
		provider := refusal.Provider
		if strings.TrimSpace(string(provider)) == "" {
			provider = providerOf(model, endpoints)
		}
		if strings.TrimSpace(string(provider)) != "" {
			for _, endpoint := range endpoints {
				if endpoint.Provider == provider && strings.TrimSpace(endpoint.Model) != "" {
					scope[strings.TrimSpace(endpoint.Model)] = true
				}
				alternateProvider := endpoint.AlternateProvider
				if alternateProvider == "" {
					alternateProvider = endpoint.Provider
				}
				if alternateProvider == provider && strings.TrimSpace(endpoint.Alternate) != "" {
					scope[strings.TrimSpace(endpoint.Alternate)] = true
				}
			}
		}
	}
	return sortedKeys(toSet(scope))
}

// providerOf is the provider an endpoint asks model of, and empty where none of
// them names it.
func providerOf(model string, endpoints []AgentEndpoint) domain.Backend {
	for _, endpoint := range endpoints {
		if strings.TrimSpace(endpoint.Model) == model {
			return endpoint.Provider
		}
		if strings.TrimSpace(endpoint.Alternate) == model {
			if endpoint.AlternateProvider != "" {
				return endpoint.AlternateProvider
			}
			return endpoint.Provider
		}
	}
	return ""
}

func toSet(models map[string]bool) map[string]struct{} {
	set := make(map[string]struct{}, len(models))
	for model := range models {
		set[model] = struct{}{}
	}
	return set
}

// lifted reports a refusal the provider has since served through on any model
// its window covers.
func lifted(evidence CapacityEvidence, refusal runstate.UsageLimitExhaustion, scope []string) bool {
	for _, model := range scope {
		each := refusal
		each.Model = model
		if evidence.Lifted(each) {
			return true
		}
	}
	return false
}

// lastProbe is the later of since and the latest probe of any model in scope
// on account after it.
func lastProbe(probes []runstate.CapacityProbe, account string, scope []string, since time.Time) time.Time {
	latest := since
	for _, probe := range probes {
		for _, model := range scope {
			if probe.Paces(account, model) && probe.At.After(latest) {
				latest = probe.At
			}
		}
	}
	return latest
}

// selfProbing is a run in flight parked on a refusal that named no reset, as
// the window it holds: the run asks the provider again itself at the deadline it
// recorded, so nothing else is sent to ask beside it.
func selfProbing(run runstate.State, endpoints []AgentEndpoint) (CapacityWindow, bool) {
	if !run.Status.InFlight() || !run.UsageLimitResetUnknown || run.UsageLimitResetsAt == nil {
		return CapacityWindow{}, false
	}
	if run.PauseCause != runstate.PauseUsageLimit && run.PauseCause != "" {
		return CapacityWindow{}, false
	}
	model := strings.TrimSpace(run.UsageLimitModel)
	if model == "" && run.Phase == runstate.PhaseDeveloping {
		model = strings.TrimSpace(run.ProviderModel)
	}
	if model == "" {
		return CapacityWindow{}, false
	}
	refusal := runstate.UsageLimitExhaustion{Model: model, Kind: run.UsageLimitKind}
	return CapacityWindow{
		Model:   model,
		Account: strings.TrimSpace(run.AccountAlias),
		Until:   run.UsageLimitResetsAt.UTC(),
		Kind:    strings.TrimSpace(run.UsageLimitKind),
		NoReset: true,
		Prober:  fmt.Sprintf("run %s of %s", run.RunID, run.WorkItemID),
		Scope:   windowScope(refusal, endpoints),
	}, true
}

func pausedSince(run runstate.State) time.Time {
	if run.UsageLimitPausedSince != nil {
		return run.UsageLimitPausedSince.UTC()
	}
	return run.UpdatedAt.UTC()
}
