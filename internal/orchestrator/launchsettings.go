package orchestrator

// A developer is started only on a provider that has been seen to apply
// what a developer is launched with.
//
// The developer's sandbox, its notes guard, and the settings that keep the
// operator's personal configuration out are passed to the provider, and a
// provider can decline them without failing: Claude Code ignores a settings
// payload it rejects and starts anyway. So before a developer is started — on a
// fresh dispatch and on a resumed run alike, before anything is claimed or
// relaunched — the provider is asked whether they took
// (backend.LaunchSettingsChecker). Where they did not, the dispatch is refused
// with the provider's version and what did not take, nothing is charged, and
// the refusal is recorded once for the product (runstate.LaunchSettingsHold).
// The first refusal files one report; every pull reads the record and starts
// no developer while it stands, so the ready queue is not refused one item at
// a time. What lifts it is a check finding the settings active — which a pull
// lets one dispatch make once the probe interval has passed since the last —
// or a pull on a harness build other than the one that placed it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// LaunchSettingsHolds is the product's record of a developer's provider that
// did not apply its launch settings, as a run writes and lifts it. It is
// satisfied by *runstate.LaunchSettingsHoldStore.
//
// It is optional. A pipeline wired without one still refuses every dispatch the
// check fails, and what is lost is the record a watching session stops pulling
// on, and with it the one report: each refusal then files its own.
type LaunchSettingsHolds interface {
	Notice(observed runstate.LaunchSettingsObservation) (runstate.LaunchSettingsHold, bool, error)
	Standing() (runstate.LaunchSettingsHold, bool, error)
	Clear() (runstate.LaunchSettingsHold, bool, error)
}

// ScheduleLaunchSettings is the same record as a watching session reads and
// lifts it.
type ScheduleLaunchSettings interface {
	Standing() (runstate.LaunchSettingsHold, bool, error)
	Clear() (runstate.LaunchSettingsHold, bool, error)
}

// defaultLaunchSettingsProbe is how long a hold stands before a pull lets a
// dispatch check again, where the pull names no probe interval of its own.
const defaultLaunchSettingsProbe = 30 * time.Minute

// LaunchSettingsError is a dispatch refused because the developer's provider
// did not apply what a developer is launched with. Like a provider
// nobody can reach, it counts toward nothing, dockets nothing, and excludes
// nothing: the item is exactly as startable as it was, and is started once the
// provider is seen to apply the settings.
type LaunchSettingsError struct {
	Hold runstate.LaunchSettingsHold
}

func (e LaunchSettingsError) Error() string {
	return e.Hold.Says()
}

// requireLaunchSettings refuses a dispatch whose developer would start without
// the settings it is launched with. A provider whose adapter has nothing to
// establish passes. A check that could not be made at all refuses this
// dispatch as the machine's and holds nothing, because it says nothing about
// the settings; the next poll asks again. A check that passes lifts a hold
// still standing, which is how a person's repair of the installation, or a
// dispatch the operator named, ends one.
func (p Pipeline) requireLaunchSettings(ctx context.Context, waiting string, provider backend.Backend, named domain.Backend) error {
	checker, checks := provider.(backend.LaunchSettingsChecker)
	if !checks {
		return nil
	}
	check, err := checker.CheckLaunchSettings(ctx, p.Repository)
	if err != nil {
		return fmt.Errorf("check what the developer's provider applies: %w",
			refusedByEnvironment("whether the developer's provider applies its sandbox and guard could not be checked", err))
	}
	if check.InForce() {
		// A hold that could not be lifted here is lifted by the next check that
		// passes; this dispatch is not refused over the record of one.
		// Only a hold on this provider is lifted: a check on one provider says
		// nothing about another's.
		if p.LaunchSettings != nil {
			if standing, found, err := p.LaunchSettings.Standing(); err == nil && found && standing.Provider == named {
				_, _, _ = p.LaunchSettings.Clear()
			}
		}
		return nil
	}
	now := p.clock().Now()
	observed := runstate.LaunchSettingsObservation{
		Provider:   named,
		Version:    check.Version,
		Build:      p.Build,
		NotInForce: check.NotInForce,
		Waiting:    waiting,
		At:         now,
	}
	hold := runstate.LaunchSettingsHold{
		SchemaVersion: runstate.LaunchSettingsHoldSchemaVersion, ProductID: p.Config.Product.ID,
		Provider: named, Version: check.Version, Build: p.Build, NotInForce: check.NotInForce,
		Since: now, LastSeen: now, Refusals: 1, Waiting: waiting,
	}
	opened := true
	var problem error
	if p.LaunchSettings != nil {
		recorded, fresh, err := p.LaunchSettings.Notice(observed)
		if err != nil {
			problem = fmt.Errorf("record that developers on %s are held: %w", named, err)
		} else {
			hold, opened = recorded, fresh
		}
	}
	if opened {
		if err := p.reportLaunchSettingsHold(hold); err != nil {
			problem = errors.Join(problem, err)
		}
	}
	refused := LaunchSettingsError{Hold: hold}
	if problem != nil {
		return errors.Join(refused, problem)
	}
	return refused
}

// reportLaunchSettingsHold files the harness's one report of a hold, when it
// opens. It is critical because a developer that started anyway would have run
// unconfined or unguarded, and because the line has stopped starting developers
// until the cause is gone.
func (p Pipeline) reportLaunchSettingsHold(hold runstate.LaunchSettingsHold) error {
	if p.Reports == nil {
		return nil
	}
	message := fmt.Sprintf("%s. "+
		"No developer runs on %s while it stands, because one started now would have no sandbox or no guard, and developer slots on other providers carry on; a person has to look at what changed in the installed CLI or the machine's policy, or a change to the adapter has to land.",
		oneline.Fold(strings.TrimSuffix(hold.Says(), "."), report.MaxMessageBytes*3/4), hold.Provider)
	collected, err := report.Collect([]report.Entry{{Severity: report.SeverityCritical, Message: message}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        "launch-settings@" + hold.Since.UTC().Format(time.RFC3339),
		Build:        p.Build,
		ProductID:    p.Config.Product.ID,
		RepositoryID: string(p.Config.Product.RepositoryID),
	}, p.clock().Now())
	if err != nil {
		return fmt.Errorf("report that developers on %s are held: %w", hold.Provider, err)
	}
	for _, reported := range collected {
		if err := p.Reports.Append(reported); err != nil {
			return fmt.Errorf("report that developers on %s are held: %w", hold.Provider, err)
		}
	}
	return nil
}

// launchSettingsHeld reads whether developers stand held on their provider's
// launch settings, and reports the hold where one does. A pull wired without
// the record reads none, and a record that cannot be read is said on the
// schedule and read past, for the reason an unreadable outage is: the
// dispatch it lets through makes the check itself.
//
// A hold placed by another harness build is lifted here, because a landing
// that changed what the adapter passes or how it checks is one of the two
// things that end it. And a hold last confirmed a probe interval ago lets this
// pull through: the dispatch it makes is the check, which lifts the hold if the
// settings are active again and confirms it, filing nothing more, if not.
func (s Scheduler) launchSettingsHeld(schedule *Schedule, pull Pull) (runstate.LaunchSettingsHold, bool) {
	schedule.LaunchSettingsHold = nil
	if pull.LaunchSettings == nil {
		return runstate.LaunchSettingsHold{}, false
	}
	hold, standing, err := pull.LaunchSettings.Standing()
	if err != nil {
		schedule.LaunchSettingsProblem = fmt.Sprintf("whether developers are held on their provider's launch settings could not be read, so the pull was made as though they were not: %v", err)
		return runstate.LaunchSettingsHold{}, false
	}
	if !standing {
		return runstate.LaunchSettingsHold{}, false
	}
	if build := strings.TrimSpace(pull.Build); build != "" && hold.Build != "" && build != hold.Build {
		if _, _, err := pull.LaunchSettings.Clear(); err != nil {
			schedule.LaunchSettingsProblem = fmt.Sprintf("the hold placed by build %s could not be lifted for build %s: %v", hold.Build, build, err)
		}
		return runstate.LaunchSettingsHold{}, false
	}
	probe := pull.OutageProbe
	if probe <= 0 {
		probe = defaultLaunchSettingsProbe
	}
	if !s.now().Before(hold.LastSeen.Add(probe)) {
		return runstate.LaunchSettingsHold{}, false
	}
	schedule.LaunchSettingsHold = &hold
	return hold, true
}

// slotProvider is the provider a dispatch into a developer slot first invokes:
// the primary of the slot's own endpoint pair where it has an enabled one that
// names a provider, and the configured developer's otherwise. It is the same
// choice dispatchBackend makes once the dispatch is under way.
func (p Pull) slotProvider(number int) domain.Backend {
	if number >= 1 && number <= len(p.Slots) {
		if routing := p.Slots[number-1].Routing; routing != nil && routing.Enabled && routing.Primary != nil && routing.Primary.Provider != "" {
			return routing.Primary.Provider
		}
	}
	return p.DeveloperProvider
}

// slotOn reports a slot whose dispatch would start on provider. A slot whose
// provider this pull cannot say is read as being on it, so a hold is never
// dispatched through for want of knowing.
func (p Pull) slotOn(number int, provider domain.Backend) bool {
	on := p.slotProvider(number)
	return on == "" || on == provider
}

// everySlotOn reports a pull none of whose developer slots dispatches onto
// anything but provider, which is when a hold on provider stops the pull whole.
func (p Pull) everySlotOn(provider domain.Backend) bool {
	slots := max(p.Capacity, len(p.Slots), 1)
	for number := 1; number <= slots; number++ {
		if !p.slotOn(number, provider) {
			return false
		}
	}
	return true
}
