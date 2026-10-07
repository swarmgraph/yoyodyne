package orchestrator

// A provider nobody is logged into or nobody can reach, and what a run does
// about it: wait, spending nothing.
//
// The operator's directive of 2026-09-18, verbatim: "I don't want a run killed
// just because the network is flaky, the laptop is asleep, or I need to re-auth
// a session." Until this, a run whose provider invocation failed on an expired
// login or an unreachable API was read as a transient death: it spent its two
// relaunches on an answer no relaunch could change, was recorded as blocked with
// its work preserved, and went on the development manager's docket as a
// stoppage — which happened to at least two runs in the 2026-09-15..18 outage.
// A dispatch refused at the availability check failed outright, counted toward
// the intake brake, and excluded its item from the session.
//
// Under the rule here the outage is the one wait that spends nothing. No
// relaunch is counted, no repair attempt is charged, the usage-limit pause
// budget is untouched, and nothing is docketed: the run keeps its claim, its
// branch, its worktree, and its session, and asks again on an interval until
// the provider answers. The wait is durable on the run exactly as a usage-limit
// pause is — the same deadline field, the same resume path — so a process that
// dies mid-wait comes back to a run that is still waiting rather than to one
// that failed. Beside the run, the outage is recorded once for the product, so
// every surface names the wait and the operator is told what ends it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ProviderOutages is the product's record of the provider answering nobody, as
// a run reads and writes it. It is satisfied by *runstate.ProviderOutageStore.
//
// It is optional on the pipeline. A run wired without one still waits exactly
// as it would have; what is lost is the product-wide record every surface names
// the wait from, which the harness always wires and a test that does not care
// about the operator being told does not have to.
type ProviderOutages interface {
	Notice(observed runstate.ProviderOutageObservation) (runstate.ProviderOutage, error)
	Standing() (runstate.ProviderOutage, bool, error)
	Clear() (runstate.ProviderOutage, bool, error)
}

// ProviderOutageError is a dispatch the provider could not serve because nobody
// is logged into it or nobody can reach it. It is what requireBackendReady
// reports instead of a plain refusal, so a scheduler can tell a dispatch the
// provider turned away from one that failed: the first counts toward nothing,
// dockets nothing, and excludes nothing, because the item is exactly as
// startable as it was and will be started when the provider answers.
type ProviderOutageError struct {
	Cause  domain.ProviderOutageCause
	Detail string
}

func (e ProviderOutageError) Error() string {
	return fmt.Sprintf("this dispatch is waiting on %s: %s", runstate.DescribeProviderOutage(e.Cause), e.Detail)
}

// providerAway reports an attempt the provider refused because nobody is logged
// into it or nobody can reach it. Like the other refusals it takes the shape of
// a failed attempt only: an outage reported beside an answer the provider still
// gave is evidence rather than a wait.
func providerAway(result backend.RunResult, err error) (backend.ProviderOutage, bool) {
	if result.ProviderOutage == nil || (err == nil && !result.IsError) {
		return backend.ProviderOutage{}, false
	}
	return *result.ProviderOutage, true
}

// noticeProviderOutage records the outage on the product, so every surface can
// name the wait, and reports only what went wrong recording it. A pipeline with
// nowhere to record one records nothing and says nothing. The channel is where
// the refusal was read, and empty for one met somewhere other than an
// invocation.
func (p Pipeline) noticeProviderOutage(cause domain.ProviderOutageCause, channel domain.ProviderChannel, detail, waiting, alias string) error {
	if p.ProviderOutages == nil {
		return nil
	}
	if _, err := p.ProviderOutages.Notice(runstate.ProviderOutageObservation{
		Cause:        cause,
		Provider:     p.developer().Backend,
		AccountAlias: alias,
		Detail:       detail,
		Channel:      channel,
		Waiting:      waiting,
		At:           p.clock().Now(),
	}); err != nil {
		return fmt.Errorf("record that the provider is answering nobody: %w", err)
	}
	return nil
}

// noticeProviderServed records that the provider answered, which is what ends
// the outage for every surface reading it. It is asked after every attempt the
// provider served rather than only after one that was waited for, because the
// process that meets the provider answering again is rarely the one that met it
// refusing. It reports only what went wrong, and only where something was
// standing: on a healthy harness it reads one file that does not exist.
func (p Pipeline) noticeProviderServed() error {
	if p.ProviderOutages == nil {
		return nil
	}
	if _, _, err := p.ProviderOutages.Clear(); err != nil {
		return fmt.Errorf("record that the provider is answering again: %w", err)
	}
	return nil
}

// CapacityServedRecorder is where a served invocation is written down as the
// evidence that a usage window it names has lifted. It is satisfied by
// *runstate.CapacityServedStore.
type CapacityServedRecorder interface {
	Record(served runstate.CapacityServed) error
}

// servedCleanly reports an invocation the provider genuinely served — see
// backend.RunResult.ServedCleanly — that also returned no error to the caller.
// An invocation that ended IsError, whose process did not succeed, or that
// reported any refusal is not recorded as served: none of them is evidence a
// window is open, and a block cleared on one would be cleared on a guess.
func servedCleanly(result backend.RunResult, err error) bool {
	return err == nil && result.ServedCleanly()
}

// noticeCapacityServed records that the provider served this account and
// model, which every reading of the provider's refusals takes as the window
// having lifted for every refusal of that account and model recorded before it.
// Like the outage it is asked after every served invocation, and it reports
// only what went wrong: a run the provider served is not failed over a record.
func (p Pipeline) noticeCapacityServed(account, model, what string) error {
	if p.CapacityServed == nil || strings.TrimSpace(model) == "" {
		return nil
	}
	if err := p.CapacityServed.Record(runstate.CapacityServed{
		AccountAlias: account,
		Model:        model,
		At:           p.clock().Now(),
		What:         what,
	}); err != nil {
		return fmt.Errorf("record that the provider served %s: %w", model, err)
	}
	return nil
}

// pauseForProviderOutage records that the provider is answering nobody and
// waits for it to answer again. It is the usage-limit pause with every budget
// taken out: the deadline it records is the next probe rather than a reset the
// provider named, the wait charges nothing to the pause budget and never blocks
// on it, and there is no in-process bound — a run interrupted by a login the
// operator renews in an hour is one that has been waiting an hour, not one the
// harness gave up on.
//
// The relaunch and repair counters are untouched by construction: this is
// reached instead of recordRelaunch, and nothing here reads or writes either.
func (a *activeRun) pauseForProviderOutage(ctx context.Context, outage backend.ProviderOutage) error {
	p := a.pipeline
	waiting := fmt.Sprintf("run %s of %s", a.state.RunID, a.state.WorkItemID)
	if err := p.noticeProviderOutage(outage.Cause, outage.Channel, outage.Detail, waiting, a.state.AccountAlias); err != nil {
		// The wait is taken whether or not it could be recorded: the record is what
		// tells the operator, and losing it is worse than losing a run, but not so
		// much worse that the run should be failed over it. It is said on the
		// outcome instead, where the summary reads it.
		a.outcome.ProviderOutageProblem = err.Error()
	}
	// An outage is the provider's state rather than the account's, so nothing
	// names a limit here, and a kind left over from an earlier pause is cleared
	// for the reason an overload clears it.
	a.state.UsageLimitKind = ""
	a.outcome.UsageLimitKind = ""
	a.state.PauseCause = runstate.PauseCauseForOutage(outage.Cause)
	a.outcome.PauseCause = a.state.PauseCause
	// Which channel the refusal was read on is recorded beside the cause. It
	// changes nothing about the wait; it is what lets whoever reads the run
	// afterwards tell a terminal the provider wrote from a process that died
	// before writing one, which is the shape the dialect used to miss.
	a.state.ProviderOutageChannel = outage.Channel
	a.outcome.ProviderOutageChannel = outage.Channel
	// The deadline is the next probe, made durable before the wait starts so a
	// process that dies mid-wait comes back to a run that is still waiting, and
	// resumes it through the same path a usage-limit pause resumes through.
	resetsAt := p.clock().Now().Add(a.outageProbe()).UTC()
	a.state.UsageLimitResetsAt = &resetsAt
	// The probe is the harness's own deadline, and nothing about an outage names
	// a model, so the record says both rather than carrying a limit's over.
	a.state.UsageLimitResetUnknown = true
	a.state.UsageLimitModel = ""
	a.state.UpdatedAt = p.clock().Now()
	a.pausedAt = p.clock().Now()
	a.recordPauseStart()
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("record provider outage pause: %w", err)
	}
	return a.awaitProviderOutage(ctx)
}

// awaitProviderOutage serves the probe already in durable state and reissues
// the attempt when it is due. It is the whole of a resumed outage wait and the
// tail of a fresh one, exactly as awaitRecordedUsageLimit is for a limit, and it
// differs from that wait in the two ways the directive asks for: nothing is
// charged for the time, and nothing but the provider answering ends it.
//
// The probe is the reissued attempt itself for a provider nobody can reach —
// nothing cheaper says whether the network is back — and for a login it is the
// same, because the availability check reads the provider's local record of
// being signed in and a reissue is what actually finds out. The operator's
// release of the wait is honored for the reason it is honored on a limit: it
// says the deadline went stale, and the cost of being wrong is one refused
// request.
func (a *activeRun) awaitProviderOutage(ctx context.Context) error {
	deadline := a.state.UsageLimitResetsAt.UTC()
	a.outcome.PauseCause = a.state.PauseCause
	a.outcome.UsageLimitResetsAt = &deadline
	released, err := a.releasedByOperator()
	if err != nil {
		return err
	}
	if !released {
		if err := a.waitForProbe(ctx, deadline); err != nil {
			return err
		}
	}
	return a.clearUsageLimitPause()
}

// outageProbe is how long a run waits between asking a provider that is
// answering nobody. It is the interval a limit that named no reset is asked on,
// because that is the one interval the configuration already states for "ask
// again rather than being told when", and a second knob for the same question
// would be two answers to it.
func (a *activeRun) outageProbe() time.Duration {
	probe := a.pipeline.Config.Execution.UsageLimitUnknownResetPause.Duration()
	if probe <= 0 {
		probe = 30 * time.Minute
	}
	return probe
}

// requireBackendReady refuses a dispatch the provider could not serve. It is
// asked after every question this repository answers on its own — the operator's
// hold, the work item, the state of the primary checkout — and deliberately so:
// those refusals hold whatever is installed on the machine, and this one holds
// only where the developer's provider is not. Asked first it replaces all of
// them, so a newcomer who has not committed their adoption is told the provider
// is missing rather than which files are dirty. Nothing between the hold and here
// reserves a run, claims an item, or cuts a worktree, so asking late costs
// nothing.
//
// The refusal names the backend the run's developer is invoked on — the one
// the run recorded, which a fresh run records from the configured developer —
// rather than one provider for all of them. A run on Codex whose CLI is missing
// has to say so about Codex: sending the operator to install or log into the
// other provider is a remedy for a machine that is not the one in front of
// them. Which command puts it right is `yoyo doctor`'s to name, because that is
// the surface that knows how each provider is installed and logged into.
//
// A provider that is installed and not logged in is a wait rather than a
// refusal. It is recorded on the product so every surface names it, and it is
// reported as a ProviderOutageError so the scheduler counts it toward nothing:
// the item is exactly as startable as it was, and the brake that trips on runs
// blocking must never trip on a login. A provider that is logged in clears any
// outage of that kind still standing, because this check is the cheapest
// evidence there is that the login was renewed.
func (p Pipeline) requireBackendReady(ctx context.Context, workItemID string, provider backend.Backend, named domain.Backend) error {
	availability, err := provider.CheckAvailability(ctx)
	if err != nil {
		return err
	}
	if !availability.Installed {
		return fmt.Errorf("%s; `yoyo doctor` names what to install", availability.NotInstalled(named))
	}
	if !availability.Authenticated {
		detail := fmt.Sprintf("the %s backend is not authenticated; `yoyo doctor` names the login that fixes it (auth method: %s)",
			named, availability.AuthMethod)
		refused := ProviderOutageError{Cause: domain.ProviderUnauthenticated, Detail: detail}
		if recordErr := p.noticeProviderOutage(domain.ProviderUnauthenticated, "", detail, "the dispatch of "+workItemID, ""); recordErr != nil {
			return errors.Join(refused, recordErr)
		}
		return refused
	}
	return p.clearOutageOnLogin()
}

// clearOutageOnLogin lifts a standing unauthenticated outage on the evidence of
// the provider reporting itself logged in. An unreachable one is left standing:
// the availability check reads a local record and says nothing about the
// network, so what lifts that is an attempt the provider actually served.
func (p Pipeline) clearOutageOnLogin() error {
	if p.ProviderOutages == nil {
		return nil
	}
	standing, found, err := p.ProviderOutages.Standing()
	if err != nil {
		return fmt.Errorf("read whether the provider is answering: %w", err)
	}
	if !found || standing.Cause != domain.ProviderUnauthenticated {
		return nil
	}
	return p.noticeProviderServed()
}
