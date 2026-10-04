// Package maintain is the product supervisor's periodic pass: what the interim
// maintenance job did by hand every ten minutes, taken by the resident on the
// cadence services.maintenance.every sets, and written down each time.
//
// The job it absorbs ran as a launchd script outside the repository, and its
// history is the reason each rule here is what it is. An operator's script
// beside it rewrote four tracker statuses with a bare `bd update --status`
// nobody could see, so this pass writes no tracker status of its own: the one
// step that touches the tracker is `yoyo reconcile`, whose every write is a
// settlement the harness records with a note. The job force-restarted the
// watch 158 times while the provider's login was expired, so nothing here
// restarts anything while the provider cannot be reached or is not logged in.
// It bounced a live watch session for a redeploy and cancelled a run, so the
// scheduler is never stopped by this pass: a session takes a build up itself,
// between the runs it hosts, on its own drain rules. And it ran with no record
// of its own, so each pass here is a sweep record beside the recurring tasks',
// with every step it took and every step it skipped saying why.
//
// The steps, in the order they are recorded:
//
//   - provider: whether the provider is answering, read from the product's
//     outage record. While it is not, the supervisor holds every restart it
//     would choose to make, and the steps below say so where it held one.
//   - reconcile: `yoyo reconcile` from the supervisor's own binary, which
//     settles what interrupted runs left behind, converges the checkout and
//     the worktrees, and takes the stall reading. It is the one slow step, so
//     it runs beside the supervisor's looks at the children rather than
//     holding them up.
//   - rebuild: what the supervisor's rebuilder last came to — it builds the
//     binary on its own thirty-second look when the branch lands something the
//     binary is made of, so a deploy has a build to take up.
//   - restart-requests: the restarts program managers asked for, each answered
//     on the request with what the supervisor did.
//   - redeploy: the deployed build taken up. The parts are moved onto it by
//     the supervisor's own looks; what this step adds is the supervisor
//     itself, which re-executes into the build once this record is written.
//   - slack: `yoyo slack ensure`, as the supervisor's own look at the sink
//     already takes it every few seconds; the step records where it stands.
package maintain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// The step names, as the sweep record carries them.
const (
	StepProvider        = "provider"
	StepReconcile       = "reconcile"
	StepRebuild         = "rebuild"
	StepRestartRequests = "restart-requests"
	StepRedeploy        = "redeploy"
	StepSlack           = "slack"
)

const (
	// ReconcileTimeout bounds `yoyo reconcile`. It asks the forge about every
	// unsettled publication and waits a dropped connection out on its own
	// backoff, so it is given long enough to do that and no longer.
	ReconcileTimeout = 15 * time.Minute
	// maxOutputBytes bounds how much of a command's output one step's detail
	// carries. The whole of it is in the supervisor's log.
	maxOutputBytes = 1 << 10
)

// Claims is where the pass records its cadence and its report: the sweep
// store every recurring task records in. It is satisfied by
// *runstate.SweepStore.
type Claims interface {
	Claim(ctx context.Context, task string, every time.Duration, now time.Time) (runstate.SweepClaim, error)
	Settle(ctx context.Context, task, problem string) (runstate.SweepClaim, error)
	Find(task string) (runstate.SweepClaim, bool, error)
	Append(recorded runstate.Sweep) error
}

// Outages is the product's record of the provider answering nobody. It is
// satisfied by *runstate.ProviderOutageStore.
type Outages interface {
	Standing() (runstate.ProviderOutage, bool, error)
}

// Requests is the log of program managers' restart requests. It is satisfied
// by *runstate.RestartRequestStore.
type Requests interface {
	Open() ([]runstate.RestartRequest, error)
	Answer(id, answer string, at time.Time) (runstate.RestartRequest, error)
}

// Rebuild is the supervisor's rebuilder as the pass reads it. It is satisfied
// by *supervise.Rebuilder.
type Rebuild interface {
	Standing() (string, bool)
}

// Host is the supervisor as the pass sees it. It is satisfied by
// *supervise.Supervisor, and every call on it is made on the supervisor's own
// goroutine.
type Host interface {
	ChildState(name config.ServiceName) (runstate.SupervisedChild, bool)
	Hosted(name config.ServiceName) string
	RunningBuild() string
	Deployed() string
	Moving() string
	TakeUp(into string) error
	RestartOnRequest(ctx context.Context, name config.ServiceName, now time.Time) string
}

// Pass is the supervisor's periodic pass over one product.
type Pass struct {
	Product domain.ProductID
	// Every is the cadence, from the services section.
	Every time.Duration
	// Program is the binary the supervisor is running, which is what
	// `reconcile` is run from, Config the configuration file it reads, and
	// Repository where it runs.
	Program    string
	Config     string
	Repository string
	// Environ is what reconcile is given, with the Slack variables already
	// taken out by whoever started the supervisor.
	Environ []string

	Claims   Claims
	Outages  Outages
	Requests Requests
	// Rebuild is the supervisor's rebuilder, and nil where the binary is not
	// built from this checkout; NoRebuild then says why.
	Rebuild   Rebuild
	NoRebuild string
	Host      Host
	Runner    execution.ProcessRunner

	Now func() time.Time
	Log func(format string, args ...any)
	// RecordFailures files or clears the shared finding after recording a pass.
	RecordFailures func(context.Context) error

	// nextDue is when the cadence next fires, kept so a supervisor looking every
	// few seconds does not open the claim each time.
	nextDue time.Time
	// running is the pass under way: when it started, and where its reconcile
	// reports once it is done.
	running *underway
	last    *runstate.Sweep
}

type underway struct {
	started    time.Time
	firing     int
	reconciled chan runstate.SweepStep
}

// Name is how the pass is named in what the supervisor says, and Service the
// part of the product it is recorded as.
func (p *Pass) Name() string { return config.MaintenanceTaskName }

func (p *Pass) Service() config.ServiceName { return config.ServiceMaintenance }

// Describe is the pass's line in the supervisor's record: its cadence, and
// what the last pass came to.
func (p *Pass) Describe() string {
	said := fmt.Sprintf("every %s", p.every())
	if p.last != nil {
		said += fmt.Sprintf("; last pass at %s (%s)", p.last.StartedAt.UTC().Format(time.RFC3339), summarize(p.last.Steps))
	}
	if p.running != nil {
		said += fmt.Sprintf("; a pass has been under way since %s", p.running.started.UTC().Format(time.RFC3339))
	} else if !p.nextDue.IsZero() {
		said += fmt.Sprintf("; next at %s", p.nextDue.UTC().Format(time.RFC3339))
	}
	return said
}

// Look is the pass as a resident of the supervisor. On every tick it finishes
// a pass whose reconcile has come back, and begins one when the cadence has
// come round. It never returns an error: a pass that failed is a fact in the
// record, and the next pass looks at everything this one would have.
func (p *Pass) Look(ctx context.Context, now time.Time) {
	if p.running != nil {
		select {
		case reconciled := <-p.running.reconciled:
			p.finish(ctx, reconciled)
		default:
		}
		return
	}
	if !p.due(now) {
		return
	}
	claimed, err := p.Claims.Claim(ctx, config.MaintenanceTaskName, p.every(), now)
	if err != nil {
		var notDue runstate.SweepNotDueError
		if errors.As(err, &notDue) {
			p.nextDue = notDue.NextDue
			return
		}
		// Tried again at the next cadence rather than every tick, for the reason
		// a failed firing moves a recurring task's clock.
		p.nextDue = now.Add(p.every())
		p.log("the maintenance pass could not be claimed, so it was not taken: %v", err)
		return
	}
	p.nextDue = claimed.NextDue(p.every())
	p.running = &underway{started: now, firing: claimed.Firings, reconciled: make(chan runstate.SweepStep, 1)}
	p.log("maintenance pass %d starting", claimed.Firings)
	reconciled := p.running.reconciled
	go func() { reconciled <- p.reconcile(ctx) }()
}

// Wait finishes the pass under way, blocking until its reconcile is back. It
// is for a test, and for nothing that holds the supervisor's looks.
func (p *Pass) Wait(ctx context.Context) {
	if p.running == nil {
		return
	}
	select {
	case reconciled := <-p.running.reconciled:
		p.finish(ctx, reconciled)
	case <-ctx.Done():
	}
}

// Last is the most recent pass this process recorded.
func (p *Pass) Last() (runstate.Sweep, bool) {
	if p.last == nil {
		return runstate.Sweep{}, false
	}
	return *p.last, true
}

// due reports whether the cadence has come round. It reads the durable claim
// once and then keeps the answer.
func (p *Pass) due(now time.Time) bool {
	if p.nextDue.IsZero() {
		claimed, found, err := p.Claims.Find(config.MaintenanceTaskName)
		if err != nil || !found {
			// A claim that cannot be read is answered by claiming, which refuses
			// on its own reading; a claim nobody made is a pass that is due.
			return true
		}
		p.nextDue = claimed.NextDue(p.every())
	}
	return !now.Before(p.nextDue)
}

// finish takes the steps that read and change the supervisor's account of its
// children, on its own goroutine, and records the pass.
func (p *Pass) finish(ctx context.Context, reconciled runstate.SweepStep) {
	started := p.running.started
	p.running = nil
	recorded := runstate.Sweep{
		SchemaVersion: runstate.SweepSchemaVersion,
		ProductID:     p.Product,
		Task:          config.MaintenanceTaskName,
		StartedAt:     started.UTC(),
	}
	recorded.Steps = append(recorded.Steps, p.provider())
	recorded.Steps = append(recorded.Steps, reconciled)
	recorded.Steps = append(recorded.Steps, p.rebuild())
	recorded.Steps = append(recorded.Steps, p.restartRequests(ctx))
	redeploy, takeUp := p.redeploy()
	recorded.Steps = append(recorded.Steps, redeploy)
	recorded.Steps = append(recorded.Steps, p.slack())
	for index := range recorded.Steps {
		recorded.Steps[index].Detail = bounded(recorded.Steps[index].Detail)
		step := recorded.Steps[index]
		p.log("maintenance: %s: %s, %s", step.Name, step.Outcome, step.Detail)
	}

	recorded.EndedAt = p.now().UTC()
	recorded.Result = &sweep.Result{Status: sweep.StatusComplete, Summary: summarize(recorded.Steps)}
	recorded.Problem = bounded(failures(recorded.Steps))
	if err := p.Claims.Append(recorded); err != nil {
		p.log("the maintenance pass could not be recorded, so `yoyo sweeps` will not show it: %v", err)
	}
	if _, err := p.Claims.Settle(ctx, config.MaintenanceTaskName, recorded.Problem); err != nil {
		p.log("the maintenance pass's claim could not be settled: %v", err)
	}
	if p.RecordFailures != nil {
		if err := p.RecordFailures(ctx); err != nil {
			p.log("the product pass failure finding could not be recorded: %v", err)
		}
	}
	p.last = &recorded
	// Last, once the record that says so is written: the supervisor lets its
	// lease go at the end of this tick and re-executes into the build.
	if takeUp != "" {
		if err := p.Host.TakeUp(takeUp); err != nil {
			p.log("the deployed build %s was not taken up: %v", takeUp, err)
		}
	}
}

// provider reads whether the provider is answering. The hold itself is the
// supervisor's, read from the same record wherever it would restart anything;
// this step is what puts the reading on the pass.
func (p *Pass) provider() runstate.SweepStep {
	step := runstate.SweepStep{Name: StepProvider}
	if p.Outages == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no outage record is wired, so whether the provider is answering is not read"
		return step
	}
	outage, standing, err := p.Outages.Standing()
	switch {
	case err != nil:
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("whether the provider is answering could not be read: %v", err)
	case standing:
		step.Outcome, step.Detail = runstate.StepRan, outage.Says()+"; nothing is restarted while it stands"
	default:
		step.Outcome, step.Detail = runstate.StepRan, "answering"
	}
	return step
}

// reconcile runs `yoyo reconcile` from the running binary. It is a subprocess
// rather than a call because the verb is the whole of the settlement — runs,
// publications, convergence, the stall reading — and a second copy of its
// wiring here would be a second reconcile to keep in step with the first. It
// runs off the supervisor's goroutine and touches nothing of the supervisor's.
func (p *Pass) reconcile(ctx context.Context) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepReconcile}
	if p.Runner == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no process runner is wired, so nothing can be run"
		return step
	}
	result, err := p.Runner.Run(ctx, execution.Command{
		Name:    p.Program,
		Args:    []string{"reconcile", "--config", p.Config},
		Dir:     p.Repository,
		Env:     p.Environ,
		Timeout: ReconcileTimeout,
	}, nil)
	switch {
	case err != nil:
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("yoyo reconcile could not be run: %v", err)
	case result.Status != execution.ProcessSucceeded:
		step.Outcome, step.Detail = runstate.StepFailed, "yoyo reconcile "+describeResult(result)
	default:
		step.Outcome, step.Detail = runstate.StepRan, "yoyo reconcile "+describeResult(result)
	}
	return step
}

// rebuild is what the supervisor's rebuilder last came to.
func (p *Pass) rebuild() runstate.SweepStep {
	step := runstate.SweepStep{Name: StepRebuild}
	if p.Rebuild == nil {
		reason := p.NoRebuild
		if reason == "" {
			reason = "the supervisor has no rebuilder"
		}
		step.Outcome, step.Detail = runstate.StepSkipped, reason
		return step
	}
	standing, failed := p.Rebuild.Standing()
	switch {
	case standing == "":
		step.Outcome, step.Detail = runstate.StepSkipped, "the rebuilder has not looked at the checkout yet"
	case failed:
		step.Outcome, step.Detail = runstate.StepFailed, standing
	default:
		step.Outcome, step.Detail = runstate.StepRan, standing
	}
	return step
}

// restartRequests answers every open request a program manager made, each
// with what the supervisor did about it.
func (p *Pass) restartRequests(ctx context.Context) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepRestartRequests}
	if p.Requests == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no restart request log is wired"
		return step
	}
	open, err := p.Requests.Open()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the restart requests could not be read: %v", err)
		return step
	}
	if len(open) == 0 {
		step.Outcome, step.Detail = runstate.StepSkipped, "no program manager has an open restart request"
		return step
	}
	var said []string
	var failed bool
	for _, request := range open {
		answer := p.Host.RestartOnRequest(ctx, request.Part, p.now())
		if _, err := p.Requests.Answer(request.ID, answer, p.now()); err != nil {
			failed = true
			said = append(said, fmt.Sprintf("%s (%s asked for the %s): %s, and the answer could not be recorded: %v", request.ID, request.Agent, request.Part, answer, err))
			continue
		}
		said = append(said, fmt.Sprintf("%s (%s asked for the %s): %s", request.ID, request.Agent, request.Part, answer))
	}
	step.Outcome, step.Detail = runstate.StepRan, strings.Join(said, "; ")
	if failed {
		step.Outcome = runstate.StepFailed
	}
	return step
}

// redeploy is the deployed build taken up. The parts are moved onto it by the
// supervisor's own looks, one at a time and never in the middle of what one is
// doing; the scheduler takes it up itself between the runs it hosts; and the
// supervisor, which nothing else moves, re-executes into it once this pass is
// recorded. It returns the build to take up, and nothing where there is none.
func (p *Pass) redeploy() (runstate.SweepStep, string) {
	step := runstate.SweepStep{Name: StepRedeploy}
	deployed, running := p.Host.Deployed(), p.Host.RunningBuild()
	var parts []string
	for _, name := range config.ServiceNames {
		state, known := p.Host.ChildState(name)
		if known && state.Redeploy != "" {
			parts = append(parts, fmt.Sprintf("the %s is %s", name, state.Redeploy))
		}
	}
	switch {
	case deployed == "":
		step.Outcome, step.Detail = runstate.StepSkipped, "which build the binary on disk is could not be read, so nothing is taken up"
		return step, ""
	case running == "":
		step.Outcome, step.Detail = runstate.StepSkipped, "the supervisor's own build carries no revision, so whether it is behind the binary on disk cannot be told"
		return step, ""
	case running == deployed && len(parts) == 0:
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("every part is on the deployed build %s; nothing to take up", short(deployed))
		return step, ""
	case running == deployed:
		step.Outcome, step.Detail = runstate.StepRan, strings.Join(parts, "; ")
		return step, ""
	}
	behind := fmt.Sprintf("the supervisor is on build %s, behind the deployed %s", short(running), short(deployed))
	if held := p.hold(); held != "" {
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("%s, and nothing is restarted while %s", behind, held)
		return step, ""
	}
	if moving := p.Host.Moving(); moving != "" {
		parts = append([]string{fmt.Sprintf("%s, and it is taken up at a later pass, once the %s has settled", behind, moving)}, parts...)
		step.Outcome, step.Detail = runstate.StepSkipped, strings.Join(parts, "; ")
		return step, ""
	}
	parts = append([]string{behind + "; it re-executes into the deployed build once this pass is recorded, leaving every part running to be reattached"}, parts...)
	step.Outcome, step.Detail = runstate.StepRan, strings.Join(parts, "; ")
	return step, deployed
}

// slack is where the sink stands: `yoyo slack ensure`, which the supervisor's
// own look at the sink already takes every few seconds — lease-checked, from
// this product's own stored tokens, nothing done while one is running.
func (p *Pass) slack() runstate.SweepStep {
	step := runstate.SweepStep{Name: StepSlack}
	sink, known := p.Host.ChildState(config.ServiceSlack)
	if !known {
		step.Outcome, step.Detail = runstate.StepSkipped, "the sink is "+p.Host.Hosted(config.ServiceSlack)
		return step
	}
	switch sink.State {
	case runstate.ChildRunning:
		detail := "running"
		if sink.PID > 0 {
			detail = fmt.Sprintf("running as pid %d", sink.PID)
		}
		step.Outcome, step.Detail = runstate.StepRan, detail+"; the supervisor's own look keeps it up, which is `yoyo slack ensure` taken every few seconds"
	case runstate.ChildDown:
		step.Outcome, step.Detail = runstate.StepRan, "down, and the supervisor is starting it again: "+sink.Reason
	case runstate.ChildDegraded:
		step.Outcome, step.Detail = runstate.StepFailed, "degraded, and the supervisor has stopped restarting it: "+sink.Reason
	default:
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("%s: %s", sink.State, sink.Reason)
	}
	return step
}

// hold is the provider guard as the redeploy step says it; the supervisor's
// TakeUp refuses on the same reading.
func (p *Pass) hold() string {
	if p.Outages == nil {
		return ""
	}
	outage, standing, err := p.Outages.Standing()
	if err != nil {
		return fmt.Sprintf("whether the provider is answering could not be read (%v)", err)
	}
	if standing {
		return outage.Says()
	}
	return ""
}

func (p *Pass) every() time.Duration {
	if p.Every > 0 {
		return p.Every
	}
	return config.DefaultMaintenanceInterval
}

func (p *Pass) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pass) log(format string, args ...any) {
	if p.Log != nil {
		p.Log(format, args...)
	}
}

// summarize is the pass in a sentence: how many steps ran, were skipped, and
// failed.
func summarize(steps []runstate.SweepStep) string {
	ran, skipped, failed := 0, 0, 0
	for _, step := range steps {
		switch step.Outcome {
		case runstate.StepRan:
			ran++
		case runstate.StepSkipped:
			skipped++
		case runstate.StepFailed:
			failed++
		}
	}
	return fmt.Sprintf("%d step(s) ran, %d skipped, %d failed", ran, skipped, failed)
}

// failures is what went wrong, for the record's problem and the claim's, and
// empty on a pass in which nothing did.
func failures(steps []runstate.SweepStep) string {
	var said []string
	for _, step := range steps {
		if step.Outcome == runstate.StepFailed {
			said = append(said, step.Name+": "+step.Detail)
		}
	}
	return strings.Join(said, "; ")
}

// describeResult is a command's ending in a line: its status and the tail of
// what it said, bounded.
func describeResult(result execution.ProcessResult) string {
	output := strings.TrimSpace(result.Stdout)
	said := fmt.Sprintf("exited %d", result.ExitCode)
	if result.Status != execution.ProcessSucceeded {
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			output = stderr
		}
		said = fmt.Sprintf("ended %s, exit %d", result.Status, result.ExitCode)
	}
	if output == "" {
		return said
	}
	return said + ": " + tail(output, maxOutputBytes)
}

func short(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

// tail folds output onto one line and keeps its end, which is where a command
// says how it came out, never cutting mid-character.
func tail(text string, limit int) string {
	folded := strings.Join(strings.Fields(text), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := folded[len(folded)-limit:]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[1:]
	}
	return "…" + strings.TrimSpace(cut)
}

// bounded keeps a detail inside what the record accepts.
func bounded(text string) string {
	if len(text) <= runstate.MaxSweepTextBytes {
		return text
	}
	return oneline.Fold(text, runstate.MaxSweepTextBytes-len(oneline.Marker))
}
