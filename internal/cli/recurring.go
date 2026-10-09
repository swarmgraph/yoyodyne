package cli

// Where the schedule meets a role's conversation, and where an operator reads
// what the schedule produced.
//
// A recurring task is wired into the pull for the reason the stopped-work
// delivery beside it is: a firing is conversation turns, and the pull is where
// the harness is already deciding what to do next. It is deliberately not a
// separate daemon, a cron entry, or a launchd job. Every one of those is a second
// thing to install, a second thing to notice has died, and — worst of the three —
// a second invoker of a role, which the harness is the only thing that does.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/forgehygiene"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// recurringTrigger wires the schedule over parts that are already built, so a
// firing happens in the role's own recorded conversation and its report lands
// beside the run state everything else durable lives in.
//
// A project that schedules nothing gets no trigger at all rather than one with an
// empty schedule: the pull then does not carry the field, and nothing about a
// pass mentions a schedule that does not exist. It returns the interface rather
// than the concrete trigger so that "nothing scheduled" is a nil the pull can
// actually test — a typed nil pointer in an interface field is not nil, and the
// pass would call through it.
func recurringTrigger(parts components, configPath string, stderr io.Writer) orchestrator.ScheduleRecurring {
	instances := programManagerPasses(parts)
	if len(parts.config.RecurringTasks) == 0 && len(instances) == 0 {
		return nil
	}
	trigger := &orchestrator.Trigger{
		// The schedule as this pull read the configuration, so a cadence changed
		// under a running session takes effect at the next pull like every other
		// configured value.
		Tasks: parts.config.RecurringTasks,
		// Where a firing is claimed and paced, so one task fires once per cadence
		// however many sessions are polling.
		Claims:             parts.store.Sweeps(),
		Availability:       machineAvailability(parts),
		Reports:            parts.store.Sweeps(),
		Roles:              roleConversation{configPath: configPath, stderr: stderr},
		MissingReportLimit: parts.config.Execution.MissingReportLimit(),
		ConversationWork: sweepConversationWork{
			items: withListingRecord(chatTracker(parts.runner, parts.repository), parts.trackerListings),
		},
		Repository: parts.repository,
		// The same pause every run, turn, and delivery reads. A firing is a
		// provider invocation, so `yoyo pause` covers it exactly as it covers them.
		Holds: parts.holds,
		// And the provider answering nobody, so a firing due while it stands
		// records the wait rather than a turn that failed — and, once the probe
		// interval has passed, fires into it to find out whether it still does.
		Outages:     parts.outages,
		OutageProbe: parts.config.Execution.UsageLimitUnknownResetPause.Duration(),
		// Where a cadence that went unfired is said, as the harness's own report
		// in the pile every other report is in, so it reaches the operator the way
		// breakage does rather than only the sweep log somebody has to go and read.
		Breakage: parts.reports,
		Attribution: report.Attribution{
			ProductID:    parts.config.Product.ID,
			RepositoryID: string(parts.config.Product.RepositoryID),
			Build:        buildinfo.Commit(),
		},
		RecordFailures: func(ctx context.Context) error {
			return parts.store.Sweeps().RecordPassFailures(ctx, parts.reports,
				passFailureAttribution(parts.config), readmodel.FactoryFlowAgent(programManagerInstances(parts.config)))
		},
		PassFailures: func(role domain.AgentRole, agent string) string {
			return readmodel.RenderPassFailures(readmodel.Sources{Passes: parts.store.Sweeps(), Reports: parts.reports,
				ProgramManagers: programManagerInstances(parts.config)}, role, agent)
		},
		// The proposed changes, so a task that wakes a role owning documents puts
		// the undecided ones against them in the wake and records what the role
		// argued. The same log every run proposes into and `yoyo amendment`
		// decides from.
		Amendments: parts.amendments,
	}
	// The pile and what became of it, so a critical report is delivered to the
	// Lead Product Manager as a turn of its own the pull after it is filed, a pass
	// shown one cannot end complete while it stands unhandled, and a program
	// manager's report her passes have left standing is named as overdue.
	if parts.reports != nil {
		trigger.Pile = parts.reports
	}
	// The docket a development manager's pass carries, built by the docketer her
	// conversation builds it with and rendered by the section her conversation
	// renders, so the two never disagree about what is waiting on her.
	if parts.docket != nil {
		trigger.Docket = sweepDocket{
			docketer: docketerFrom(parts),
			items:    withListingRecord(chatTracker(parts.runner, parts.repository), parts.trackerListings),
			window:   parts.docket,
			// The standing `yoyo status` reads, so what she is shown as waiting on
			// the operator is his needs-a-human line and not a second reading of it.
			standing: func() readmodel.Standing {
				return readmodel.ReadStanding(context.Background(), standingSources(configPath))
			},
		}
	}
	// The work the tracker closed, so a pass of the Lead Product Manager's audits
	// what landed since her last pass against the standing goals. The same
	// tracker her conversation admits the corrections into.
	trigger.ClosedWork = closedWork{
		items: withListingRecord(chatTracker(parts.runner, parts.repository), parts.trackerListings),
	}
	// The program manager instances their triggers wake, with the cursor each
	// keeps over the streams it watches and the streams themselves, and whether a
	// turn is already in flight on an instance's conversation, which prevents
	// an unfinished pass being mistaken for one whose process died.
	if len(instances) > 0 {
		trigger.Instances = instances
		trigger.Cursors = parts.store.PassCursors()
		trigger.Events = passEvents{runs: parts.store, repository: parts.repository, refresh: parts.tracker().RefreshExportIfDue}
		if conversations, err := runstate.NewConversationStore(parts.stateRoot, parts.config.Product.ID); err == nil {
			trigger.Conversations = instanceConversations{store: conversations}
		} else if stderr != nil {
			fmt.Fprintf(stderr, "the program manager instances' conversations could not be opened to ask whether a turn is in flight on one, so a pass is opened and recorded as unreachable where one is: %v\n", err)
		}
	}
	// The harness's own reading of the forge on the development manager's pass,
	// through the same client the publication path opens and merges requests
	// with, so what it lists is the repository runs publish into. The tracker
	// says which work is closed and the run records say which work each request
	// was opened for. The gate is the one the publication path itself opens a
	// request under — orchestrator.Pipeline.publishes, which is exactly
	// `approvals.publishing: automatic` and no other value: the only other mode,
	// `human`, pushes nothing and opens nothing, as the approvals table in
	// docs/configuration.md says. A project under it has no requests of the
	// harness's on any forge, and a forge it may not even have is not read.
	if parts.config.Approvals.Publishing == domain.ApprovalAutomatic {
		trigger.Forge = forgehygiene.Sweeper{
			Forge: publish.GitHub{
				Runner:       parts.runner,
				Dir:          parts.repository,
				Remote:       parts.config.Execution.Remote,
				PushRemote:   parts.config.Execution.PushRemote,
				RedactValues: parts.redactValues,
			},
			Tracker: parts.tracker(),
			Runs:    parts.store,
		}
	}
	return trigger
}

func passFailureAttribution(cfg config.Config) report.Attribution {
	return report.Attribution{ProductID: cfg.Product.ID, RepositoryID: string(cfg.Product.RepositoryID), Build: buildinfo.Commit()}
}

type sweepConversationWork struct {
	items interface {
		List(ctx context.Context, status string) ([]beads.WorkItem, error)
	}
}

func (w sweepConversationWork) Read(ctx context.Context, role domain.AgentRole) (string, error) {
	items, err := w.items.List(ctx, "")
	if err != nil {
		problem := fmt.Errorf("read the work waiting in the %s's conversation: %w", role.Title(), err)
		return contextbundle.ConversationWorkSection(nil, problem.Error()), problem
	}
	return contextbundle.ConversationWorkSection(readmodel.ConversationWork(items, role), ""), nil
}

// closedWork lists the work the tracker closed after a moment, for the Lead
// Product Manager's audit. It reads the closed listing and changes nothing.
type closedWork struct {
	items interface {
		List(ctx context.Context, status string) ([]beads.WorkItem, error)
	}
}

// Closed is the work closed after since, oldest first, at most limit of it. An
// item the tracker gives no close time for cannot be placed after any moment,
// so it is left out rather than listed on every pass.
func (w closedWork) Closed(ctx context.Context, since time.Time, limit int) (orchestrator.ClosedWork, error) {
	items, err := w.items.List(ctx, "closed")
	if err != nil {
		return orchestrator.ClosedWork{}, fmt.Errorf("list the closed work: %w", err)
	}
	var after []beads.WorkItem
	for _, item := range items {
		if item.ClosedAt.After(since) {
			after = append(after, item)
		}
	}
	slices.SortStableFunc(after, func(a, b beads.WorkItem) int {
		if c := a.ClosedAt.Compare(b.ClosedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	reading := orchestrator.ClosedWork{}
	if limit > 0 && len(after) > limit {
		reading.More = len(after) - limit
		after = after[:limit]
	}
	for _, item := range after {
		reading.Items = append(reading.Items, orchestrator.ClosedItem{
			ID:       item.ID,
			Title:    item.Title,
			ClosedAt: item.ClosedAt,
			Landed:   landedSummary(item),
		})
	}
	return reading, nil
}

// reviewSummaryLine opens the line a run's landing note carries the independent
// reviewer's account of the approved change on.
const reviewSummaryLine = "Review summary:"

// landedSummary is the account of what landed on an item: the last reviewer's
// summary its notes carry, which describes the change that was approved, and
// the reason the tracker closed it with where it carries none — the close of a
// conversation's item names the document revision it landed as.
func landedSummary(item beads.WorkItem) string {
	summary := ""
	for _, line := range strings.Split(item.Notes, "\n") {
		if rest, found := strings.CutPrefix(strings.TrimSpace(line), reviewSummaryLine); found && strings.TrimSpace(rest) != "" {
			summary = strings.TrimSpace(rest)
		}
	}
	if summary != "" {
		return summary
	}
	return strings.TrimSpace(item.CloseReason)
}

// sweepDocket is the triage docket as a scheduled pass of the development
// manager's carries it: built now, as her conversation builds it when it opens,
// and windowed and rendered by the same section, from the same walk position.
type sweepDocket struct {
	docketer interface {
		Build() (orchestrator.DocketBuild, error)
	}
	// items lists every work item the tracker holds, which is what says whose
	// entries are on closed work. Nil is a pass with no tracker to ask, and then
	// no entry is taken for dead and the window says so.
	items interface {
		List(ctx context.Context, status string) ([]beads.WorkItem, error)
	}
	// window is where the last docket window stopped, shared with her
	// conversation, so a pass resumes the walk past what she was last shown
	// wherever she was shown it. Nil starts every pass at the oldest stoppage.
	window docketWindow
	// standing reads the needs-a-human line, whose entries the operator moves
	// are carried beside the docket: her sweep is asked to check what appears to
	// wait on him and does not really need him, and the docket alone shows her
	// only what waits on her. Nil carries no such section.
	standing func() readmodel.Standing
	// now is the moment the ages are reckoned from; nil is the clock.
	now func() time.Time
}

func (d sweepDocket) StartPass(pending []triage.WindowPosition) orchestrator.RecurringDocketPass {
	return &sweepDocketPass{source: d, pending: pending}
}

type sweepDocketPass struct {
	source   sweepDocket
	shown    []triage.WindowPosition
	pending  []triage.WindowPosition
	next     contextbundle.DocketWindow
	complete bool
	known    []triage.Stoppage
	// whole and cut are the entries this pass's answered turns showed whole and
	// showed only cut, for the counts its record keeps.
	whole, cut map[triage.WindowPosition]bool
}

func (p *sweepDocketPass) Window() string {
	window, problem, complete := p.read()
	p.next = window
	p.complete = complete
	p.remember(window, complete)
	rendered := window.Text
	if rendered == "" {
		rendered = "## Triage docket\n\nNothing is on the docket: no stoppage is waiting on a decision of yours.\n"
	}
	if problem != "" {
		rendered += "\n" + problem + "\n"
	}
	d := p.source
	if d.standing == nil {
		return rendered
	}
	now := time.Now()
	if d.now != nil {
		now = d.now()
	}
	return rendered + "\n" + d.standing().RenderOperatorWaits(now)
}

func (p *sweepDocketPass) Delivered() string {
	if p.whole == nil {
		p.whole, p.cut = map[triage.WindowPosition]bool{}, map[triage.WindowPosition]bool{}
	}
	cutShort := make(map[triage.WindowPosition]bool, len(p.next.Cut))
	for _, at := range p.next.Cut {
		cutShort[at] = true
	}
	for _, standing := range p.next.Listed {
		p.shown = append(p.shown, standing.At())
		if cutShort[standing.At()] {
			p.cut[standing.At()] = true
		} else {
			p.whole[standing.At()] = true
		}
	}
	for _, standing := range p.next.Unlisted {
		p.cut[standing.At()] = true
	}
	if p.complete {
		p.pending = nil
		for _, standing := range p.next.Unlisted {
			p.pending = append(p.pending, standing.At())
		}
	}
	// Unlike a prepared message a provider refused, an answered turn advances
	// the shared conversation walk. The pass's own seen set also covers urgent,
	// decided and waited entries, which do not advance that walk.
	if p.next.Position != nil && p.source.window != nil {
		if err := p.source.window.RecordWindowPosition(*p.next.Position); err != nil {
			return fmt.Sprintf("where this docket window stopped could not be recorded, so another conversation may start from the same place: %v", err)
		}
	}
	return ""
}

func (p *sweepDocketPass) Remaining() (runstate.DocketDelivery, string) {
	window, problem, complete := p.read()
	p.remember(window, complete)
	seen := make(map[triage.WindowPosition]bool, len(p.shown))
	for _, at := range p.shown {
		seen[at] = true
	}
	delivery := runstate.DocketDelivery{Delivered: len(p.shown), Whole: len(p.whole)}
	for at := range p.cut {
		if !p.whole[at] {
			delivery.Cut++
		}
	}
	for _, standing := range p.known {
		if seen[standing.At()] {
			continue
		}
		seen[standing.At()] = true
		delivery.Undelivered = append(delivery.Undelivered, standing.At())
		if delivery.Oldest == nil || standing.Since.Before(delivery.Oldest.Position.Since) {
			delivery.Oldest = &runstate.UndeliveredDocketEntry{Position: standing.At(),
				WorkItemID: standing.Entry.WorkItemID, WorkItemTitle: standing.Entry.WorkItemTitle, RunID: standing.Entry.RunID}
		}
	}
	if !complete {
		// An unreadable or partial first window still owes every entry the
		// last pass named, even where this pass has not managed to read it.
		for _, at := range p.pending {
			if seen[at] {
				continue
			}
			delivery.Undelivered = append(delivery.Undelivered, at)
			if delivery.Oldest == nil || at.Since.Before(delivery.Oldest.Position.Since) {
				delivery.Oldest = &runstate.UndeliveredDocketEntry{Position: at}
			}
		}
	}
	p.pending = delivery.Undelivered
	return delivery, problem
}

// remember drops absent entries only on a complete reading. A partial reading
// can add or refresh evidence, but cannot prove a known unread entry was settled.
func (p *sweepDocketPass) remember(window contextbundle.DocketWindow, complete bool) {
	current := append(append([]triage.Stoppage(nil), window.Listed...), window.Unlisted...)
	if complete {
		p.known = current
		return
	}
	byPosition := make(map[triage.WindowPosition]triage.Stoppage, len(current))
	for _, standing := range current {
		byPosition[standing.At()] = standing
	}
	for i, standing := range p.known {
		if refreshed, found := byPosition[standing.At()]; found {
			p.known[i] = refreshed
			delete(byPosition, standing.At())
		}
	}
	for _, standing := range current {
		if _, found := byPosition[standing.At()]; found {
			p.known = append(p.known, standing)
		}
	}
}

// read uses the same renderer as the conversation, refreshed between turns.
// A partial build says what it could not establish beside what it did find.
func (p *sweepDocketPass) read() (contextbundle.DocketWindow, string, bool) {
	d := p.source
	built, err := d.docketer.Build()
	listed := built.Listed()
	if err != nil && len(listed) == 0 {
		window := contextbundle.TriageDocketWindow(contextbundle.ProductRequest{TriageDocketUnavailable: err.Error()}, nil, nil)
		return window, fmt.Sprintf("the live docket could not be read, so which entries remain undelivered could not be established: %v", err), false
	}
	now := time.Now()
	if d.now != nil {
		now = d.now()
	}
	request := contextbundle.ProductRequest{TriageDocket: listed, TriageDocketAt: now}
	var problems []string
	if len(listed) > 0 {
		if d.items == nil {
			request.TriageDocketItemsUnavailable = "no tracker was given to this pass"
		} else if items, listErr := d.items.List(context.Background(), ""); listErr != nil {
			request.TriageDocketItemsUnavailable = listErr.Error()
		} else {
			request.TriageDocketItems = items
		}
		if d.window != nil {
			if position, readErr := d.window.WindowPosition(); readErr != nil {
				problems = append(problems, fmt.Sprintf("where the last docket window stopped could not be read, so this one starts at the oldest stoppage: %v", readErr))
			} else {
				request.TriageDocketPosition = position
			}
		}
	}
	window := contextbundle.TriageDocketWindow(request, p.shown, p.pending)
	if err != nil {
		problems = append(problems, fmt.Sprintf("The docket could only be built in part, so there may be stoppages it does not list: %v", err))
	}
	return window, strings.Join(problems, "; "), err == nil
}

// roleConversation is a role's own conversation, reached the way an operator
// reaches it: the recorded conversation resumed, one message sent, the reply
// read. Nothing about the turn is special — it is held under the same lease,
// recorded in the same log, charged to the same account, and reads the same
// persona as the conversation an operator opens by hand.
//
// That last part is the whole of why a scheduled wakeup is safe to do at all. A
// turn that read a different personality, or held a different authority, because
// something woke it on a timer would be a second version of a role nobody
// configured.
type roleConversation struct {
	configPath string
	// stderr is where opening the conversation says what it could not read, so a
	// session that started with a warning says so where the operator running it
	// can see it.
	stderr io.Writer
	// open is how the conversation is opened for a turn, and nil for every
	// trigger but a test's: production prepares and opens the operator's own
	// conversation with the task's model, waiting for its hold within the bound.
	open func(ctx context.Context, role domain.AgentRole, agent, model string, options orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error)
	// timeout is a test's shorter bound; production shares the conversation's
	// turn bound, including the time spent waiting for its hold.
	timeout time.Duration
}

// Wake puts one message into a role's conversation and reads the account it gave
// of the pass.
//
// A conversation that could not be opened is reported as unreachable rather than
// as a failed firing, and the difference is what the caller records: nothing was
// asked, so the firing produced no account rather than a bad one. The ordinary
// reasons opening fails — the operator is mid-turn with the role, the provider is
// not signed in, no agent fills the role — are all of that kind.
//
// An answer with no sweep block is asked once for the block alone, under the
// same conversation hold. An unrecovered account fails the pass.
//
// A task that names its own model has this turn ask for it, and only this turn:
// the conversation, its account, and its failover are the role's, and the next
// message anybody else sends into it asks for the role's model again.
//
// The turn is marked as the pass's, so what it writes on the pass's behalf — a
// program manager's lane report — is stamped with the pass as well as the turn,
// and a report it carried that was refused is on the pass's record beside
// whatever else the pass has to say about itself.
func (r roleConversation) Wake(ctx context.Context, role domain.AgentRole, agent, pass, model, message string, options orchestrator.RecurringTurnOptions) (orchestrator.Turn, error) {
	bound := r.timeout
	if bound <= 0 {
		bound = chat.DefaultTurnTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	session, lease, replacement, err := r.opener()(ctx, role, agent, model, options)
	if err != nil {
		return orchestrator.Turn{}, passNotOpened(err)
	}
	defer lease.Release()
	session.ForPass(pass)

	reply, err := session.Send(ctx, message)
	// The conversation and what the turn cost are carried whichever way it went: a
	// turn that failed still happened in a conversation somebody can go and read,
	// and the provider charged for it exactly as it charges for one that answered.
	evidence := session.Evidence()
	turn := orchestrator.Turn{
		Replacement:    replacement,
		ConversationID: evidence.ConversationID,
		CostUSD:        session.TurnCostUSD(),
		Model:          servingModel(evidence),
		Effort:         evidence.Effort,
		ResolvedEffort: evidence.ResolvedEffort,
		EffortReported: evidence.EffortReported,
		// The criticals the conversation carried in of its own accord, so a pass
		// that ends complete over one of them is refused as complete.
		CriticalReports: session.CriticalReportsShown(),
		// What the turn saved into the role's memory and lane report, which a
		// turn that failed afterwards still saved: the pass's record says which
		// writes stood, and the pass run again is told not to make them twice.
		Saved: reply.Saved,
		// And the reports it filed and the work it admitted, the other two
		// traces a finding can leave: a pass that reports findings and leaves
		// none of the four is recorded as untraced.
		Wording:      reply.Wording,
		ReportsFiled: len(reply.Reports),
		Admitted:     reply.AdmittedWork(),
	}
	if err != nil {
		return turn, notWoken(err)
	}
	turn.Turns = 1
	turn.Result, turn.ResultProblem = readSweep(role, reply.Text)
	_, result, _, extractErr := sweep.Extract(reply.Text)
	if result == nil && extractErr == nil {
		turn.MissingReport = true
		if options.RetryReport {
			// Keep the session and hold: another turn must not intervene between
			// the work and the request for its account.
			turn.ReportRetried = true
			recovered, recoveryErr := session.Send(ctx, sweep.ReportRequest())
			turn.CostUSD += session.TurnCostUSD()
			turn.Saved = append(turn.Saved, recovered.Saved...)
			turn.Wording = append(turn.Wording, recovered.Wording...)
			turn.ReportsFiled += len(recovered.Reports)
			turn.Admitted = append(turn.Admitted, recovered.AdmittedWork()...)
			turn.CriticalReports = append(turn.CriticalReports, session.CriticalReportsShown()...)
			turn.Model = servingModel(session.Evidence())
			if recoveryErr != nil {
				return turn, fmt.Errorf("the request for the missing closing report failed; the preceding reply's findings remain unrecorded: %w", recoveryErr)
			}
			turn.Turns++
			turn.Result, turn.ResultProblem = readSweep(role, recovered.Text)
			turn.MissingReport = turn.Result == nil
			if turn.MissingReport {
				turn.ResultProblem = appendProblem(turn.ResultProblem, "the closing report is still missing after the pass's one request for it; the pass failed and its findings remain unrecorded")
			}
			turn.ResultProblem = appendProblem(turn.ResultProblem, recovered.LaneReport.Refusal())
		}
	}
	// A question the role put to a role it may not ask was handed back and the
	// rest of its reply carried out, so it is said beside the account and is
	// not a failed turn.
	for _, refused := range reply.RefusedAsks {
		turn.ResultProblem = appendProblem(turn.ResultProblem, refused)
	}
	if refusal := reply.LaneReport.Refusal(); refusal != "" {
		if turn.ResultProblem == "" {
			turn.ResultProblem = refusal
		} else {
			turn.ResultProblem += "; " + refusal
		}
	}
	return turn, nil
}

// servingModel is the model a turn ran on: the alternate that served it where
// the provider moved it, and the model it asked for otherwise.
func servingModel(evidence chat.Evidence) string {
	if served := strings.TrimSpace(evidence.ServedModel); served != "" {
		return served
	}
	return strings.TrimSpace(evidence.RequestedModel)
}

// readSweep reads the account a role gave of its pass out of what it answered,
// and what the record should say about it. The two are not exclusive: a reply
// that carried more than one block is read — the last block is its account —
// and the problem says so beside it, because a pass whose decisions were taken
// must not lose its record over a slip in the shape of the reply.
func readSweep(role domain.AgentRole, reply string) (*sweep.Result, string) {
	_, result, note, err := sweep.Extract(reply)
	switch {
	case err != nil:
		return nil, fmt.Sprintf("the %s answered with a sweep block the harness cannot read, so what the pass found is only in the conversation: %v", role, err)
	case result == nil:
		return nil, fmt.Sprintf("the %s answered in prose without a sweep block, so what the pass found is only in the conversation", role)
	case note != "":
		return result, fmt.Sprintf("the %s answered with more than one sweep block: %s", role, note)
	default:
		return result, ""
	}
}

// notWoken marks the failures where the turn asked the role nothing, so what is
// recorded about the firing says so rather than claiming a pass that produced
// nothing. They are the same three the stopped-work delivery names, for the same
// reasons: a provider with no capacity never put the message in front of the
// role, and neither did one nobody is logged into or nobody can reach; a pause
// placed between the claim and the turn refused it before the provider was
// reached; and a cancellation is the harness's own death rather than anything
// about the role.
//
// Unlike the delivery, none of them gives anything back. A recurring task's
// cadence is not a budget of attempts at one specific thing: the next firing
// looks at everything this one would have, so it waits for the next cadence
// rather than being retried at once against a provider that is still out of
// capacity.
func notWoken(err error) error {
	// A message the harness refused, or a turn it could not assemble, never
	// reached the provider either — and unlike the refusals below it is not
	// waited out: the next firing composes the same message and meets the same
	// refusal, so the firing is recorded as failed with its cause.
	switch {
	case errors.Is(err, chat.ErrMessageRefused):
		return &orchestrator.NotStartedError{Cause: runstate.PreTurnMessageRefused, Err: err}
	case errors.Is(err, chat.ErrTurnUnassembled):
		return &orchestrator.NotStartedError{Cause: runstate.PreTurnContextUnassembled, Err: err}
	}
	var held *chat.OperatorHoldError
	if errors.Is(err, chat.ErrProviderCapacity) || errors.Is(err, chat.ErrProviderAway) || errors.As(err, &held) || errors.Is(err, chat.ErrTurnAbandoned) {
		return fmt.Errorf("%w: %w", orchestrator.ErrRoleUnreachable, err)
	}
	return err
}

// passNotOpened is a conversation that could not be opened for the turn. It is
// always unreachable — nothing was asked — and it is a firing that failed
// before its first turn as well, unless what stopped the opening was the
// provider answering nobody, the operator's pause, or a held conversation,
// which are waits with records of their own.
func passNotOpened(err error) error {
	unreachable := fmt.Errorf("%w: %w", orchestrator.ErrRoleUnreachable, err)
	var held *chat.OperatorHoldError
	if errors.Is(err, chat.ErrProviderCapacity) || errors.Is(err, chat.ErrProviderAway) || errors.As(err, &held) || errors.Is(err, chat.ErrTurnAbandoned) || errors.Is(err, context.Canceled) || errors.Is(err, runstate.ErrConversationHeld) {
		return unreachable
	}
	return &orchestrator.NotStartedError{Cause: runstate.PreTurnConversationUnopened, Err: unreachable}
}

func (r roleConversation) opener() func(context.Context, domain.AgentRole, string, string, orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
	if r.open != nil {
		return r.open
	}
	return func(ctx context.Context, role domain.AgentRole, agent, model string, options orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
		prepared, err := prepareChat(ctx, role, agent, r.configPath, r.errors())
		if err != nil {
			return nil, nil, nil, err
		}
		if err := prepared.onModel(model); err != nil {
			return nil, nil, nil, err
		}
		// A scheduled pass waits for the hold like an operator's command, but
		// still defers a provider refusal to its next cadence instead of sleeping
		// through a usage window. Queueing and provider waiting are independent.
		hold, err := prepared.claim(ctx, true, r.errors())
		if err != nil {
			return nil, nil, nil, err
		}
		replacement, err := recurringReplacement(prepared.store, prepared.identity, options)
		if err != nil {
			return nil, nil, nil, errors.Join(err, hold.Release())
		}
		session, err := prepared.open(ctx, hold, replacement != nil, false, r.errors())
		if err != nil {
			return nil, nil, nil, errors.Join(err, hold.Release())
		}
		return session, hold, replacement, nil
	}
}

// Called under the hold: another task or the operator may already have replaced
// the conversation, which ends the old conversation's missing-report count.
func recurringReplacement(store *runstate.ConversationStore, identity runstate.ConversationIdentity, options orchestrator.RecurringTurnOptions) (*runstate.SweepConversationReplacement, error) {
	if options.FreshAfter == "" {
		return nil, nil
	}
	current, err := store.Load(identity)
	if errors.Is(err, runstate.ErrNoConversation) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if current.ConversationID != options.FreshAfter {
		return nil, nil
	}
	return &runstate.SweepConversationReplacement{Previous: current.ConversationID, Reason: options.FreshReason}, nil
}

func (r roleConversation) errors() io.Writer {
	if r.stderr == nil {
		return io.Discard
	}
	return r.stderr
}

// defaultRenderedSweeps bounds how many recorded sweeps a listing shows when
// nobody said. The most recent are kept rather than the oldest: what an operator
// usually wants from a schedule is what it has been doing lately.
//
// It is a default rather than a cap, which is the part worth stating. An hourly
// task fills twenty passes before the day is out, and the question these reports
// exist to answer — whether a week of fixes filed root-cause work or quietly
// repaired the same thing seven times — needs the week rather than the morning.
// So `--limit` widens it and `--limit 0` reads the whole log, and the listing
// says which of the two it is showing.
const defaultRenderedSweeps = 20

type sweepsOutput struct {
	Sweeps []runstate.Sweep `json:"sweeps"`
	// Unreadable are the lines of the log that would not decode. They are carried
	// beside the sweeps rather than raised as a failure for the reason the store
	// sets them aside: one torn write must not cost every report around it. They
	// are never left out, because a listing that quietly dropped records would be
	// worse than the failure it replaced.
	Unreadable []runstate.UnreadableSweep `json:"unreadable,omitempty"`
	Error      string                     `json:"error,omitempty"`
}

// readSweeps shows what the recurring tasks have produced. It is read-only: a
// sweep is written once and never revised, and nothing here fires one, retires
// one, or decides anything about what a pass found.
//
// It exists for the same reason `yoyo reports` does. A pass that runs at three in
// the morning tells nobody anything if reading it costs an interactive
// conversation with a provider behind it, and a schedule whose output is out of
// reach of anything scripted is one nobody will ever summarize.
func readSweeps(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sweeps", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	task := flags.String("task", "", "show only this recurring task's sweeps (default: all of them)")
	limit := flags.Int("limit", defaultRenderedSweeps, "show this many of the most recent sweeps, or 0 for every one recorded")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "sweeps does not accept positional arguments: name a task with --task")
		printSweepsUsage(stderr)
		return 2
	}
	if *limit < 0 {
		fmt.Fprintln(stderr, "--limit cannot be negative; use 0 to read every recorded sweep")
		return 2
	}
	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportSweepFailure(stdout, stderr, *jsonOutput, err)
	}
	// The unreadable lines come back beside the sweeps rather than instead of
	// them: a log with one torn write still holds every report around it, and
	// those are what somebody came here to read. What they cannot be is silent,
	// so they are shown either way.
	recorded, unreadable, err := parts.store.Sweeps().List()
	// A scan that failed part way through is not a listing lost. List returns what
	// it read before the failure, and those passes are exactly what somebody came
	// here for, so the failure is carried alongside them and said after them
	// rather than in place of them. The command still exits non-zero: what changes
	// is that a log which cannot be read to the end costs the reader the tail of
	// the pile instead of all of it. A failure that read nothing at all has
	// nothing to render and is reported on its own, as it always was.
	partial := err
	if partial != nil && len(recorded) == 0 && len(unreadable) == 0 {
		return reportSweepFailure(stdout, stderr, *jsonOutput, partial)
	}
	if named := strings.TrimSpace(*task); named != "" {
		filtered := make([]runstate.Sweep, 0, len(recorded))
		for _, entry := range recorded {
			if entry.Task == named {
				filtered = append(filtered, entry)
			}
		}
		recorded = filtered
	}
	// JSON is unbounded whatever --limit says, and always has been: it is what
	// something summarizing a week reads, and a bound there would be a bound on
	// what a script can see rather than on what fits a terminal.
	if *jsonOutput {
		out := sweepsOutput{Sweeps: recorded, Unreadable: unreadable}
		if partial != nil {
			out.Error = partial.Error()
		}
		if code := writeJSON(stdout, stderr, out); code != 0 {
			return code
		}
		if partial != nil {
			return 1
		}
		return 0
	}
	// A pass's account is a role's own words, and a role that named work by its
	// number alone is read here with each item's title beside it. A tracker
	// that cannot be listed costs the titles and nothing else.
	titles, _ := readmodel.ReadWorkItemTitles(context.Background(), readmodel.Sources{Tracker: parts.tracker(), TrackerTimeout: trackerCommandTimeout})
	fmt.Fprint(stdout, renderSweeps(recorded, unreadable, *limit, titles, readmodel.ReadTextTerms(parts.repository)))
	if partial != nil {
		// Said after the listing rather than before it, and on stderr, so what a
		// reader is looking at stays on stdout whole: the sweeps above are real
		// records, and what this adds is that there may be more of them than the
		// reading reached.
		fmt.Fprintf(stderr, "the sweep log could not be read to the end, so the listing above stops where the reading stopped rather than where the log does: %v\n", partial)
		return 1
	}
	return 0
}

func reportSweepFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, sweepsOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintf(stderr, "sweeps failed: %v\n", err)
	return 1
}

// renderSweeps writes the pile most recent first, because what a reader wants
// from a schedule is usually what it has been doing lately.
//
// A limit of zero shows everything, and a listing that is showing part of the
// pile says so and says how to see the rest — the bound is what fits a terminal
// rather than what the log holds, and a reader who cannot tell the difference is
// a reader who thinks the schedule started yesterday.
func renderSweeps(recorded []runstate.Sweep, unreadable []runstate.UnreadableSweep, limit int, titles *readmodel.WorkItemTitles, wording ...*readmodel.TextTerms) string {
	var words *readmodel.TextTerms
	if len(wording) > 0 {
		words = wording[0]
	}
	var rendered strings.Builder
	// Said first, and said whether or not there is anything else to show: a log
	// that has lost a record is the thing a reader most needs to know before they
	// draw a conclusion from what is left.
	for _, torn := range unreadable {
		fmt.Fprintf(&rendered, "line %d of the sweep log could not be read, so one pass is missing from what follows: %s\n", torn.Line, torn.Problem)
	}
	if len(unreadable) > 0 {
		rendered.WriteString("\n")
	}
	if len(recorded) == 0 {
		rendered.WriteString("no recurring task has recorded a sweep yet\n")
		return rendered.String()
	}
	shown := recorded
	if limit > 0 && len(shown) > limit {
		shown = shown[len(shown)-limit:]
		fmt.Fprintf(&rendered, "%d sweep(s) recorded; the most recent %d follow. --limit reads further back, --limit 0 reads all of them\n\n",
			len(recorded), len(shown))
	}
	for index := len(shown) - 1; index >= 0; index-- {
		// Each pass is cited on its own, so an item two passes both name is
		// titled in each of them rather than only in the newer.
		rendered.WriteString(words.Render(titles.Cite(renderSweep(shown[index]))))
		if index > 0 {
			rendered.WriteString("\n")
		}
	}
	return rendered.String()
}

// renderSweep writes one pass: what it was, what it found, and what it needs.
// The questions come first among the findings for the reason the report exists —
// a report with no questions asks for nothing, and an operator reading these at
// leisure has to be able to see that at a glance.
func renderSweep(recorded runstate.Sweep) string {
	var rendered strings.Builder
	if recorded.HarnessPass() {
		return renderHarnessPass(recorded)
	}
	fmt.Fprintf(&rendered, "%s  %s (%s), %d turn(s)",
		recorded.StartedAt.UTC().Format(time.RFC3339), recorded.Task, recorded.Role, recorded.Turns)
	if recorded.CostUSD > 0 {
		fmt.Fprintf(&rendered, ", $%.4f", recorded.CostUSD)
	}
	if recorded.Model != "" {
		fmt.Fprintf(&rendered, ", on %s", recorded.Model)
	}
	rendered.WriteString("\n")
	// How long the pass stood due before it was taken, so a pass that waited on
	// another's reads as one that waited rather than as one that was on time.
	if waited, known := recorded.Waited(); known && recorded.Missed == nil {
		fmt.Fprintf(&rendered, "  fell due at %s and waited %s before it was taken\n",
			recorded.DueAt.UTC().Format(time.RFC3339), waited.Round(time.Second))
	}
	// A summoned pass is said as one before anything it found: it is the pass
	// that ran because the line stopped, and a reader scanning the log for why
	// the hourly cadence has an extra entry in it is owed the answer first.
	if recorded.Summoned != "" {
		fmt.Fprintf(&rendered, "  summoned ahead of its schedule by %s\n", recorded.Summoned)
	}
	// A firing that never reached its first turn is said as the failed firing it
	// is, ahead of anything else: it is not a pass that found less, and it is not
	// partial, and the next firing will meet the same refusal.
	if recorded.NotStarted != "" {
		fmt.Fprintf(&rendered, "  FAILED FIRING: it failed before its first turn — %s\n", recorded.NotStarted.Describe())
	}
	if recorded.Failed && recorded.NotStarted == "" {
		rendered.WriteString("  FAILED PASS: it did not complete\n")
	}
	if recorded.ReportRetried {
		rendered.WriteString("  requested the missing closing report once on this pass\n")
	}
	if replacement := recorded.ConversationReplacement; replacement != nil {
		fmt.Fprintf(&rendered, "  replaced conversation %s with %s: %s\n", replacement.Previous, recorded.ConversationID, replacement.Reason)
	}
	// A missed pass is said as one, naming the trigger that owed it, so a gap
	// reads as a pass owed rather than as a quiet one.
	if recorded.Missed != nil {
		how := "no pass followed it"
		if recorded.Missed.How == runstate.MissCancelled {
			how = "cancelled before it completed"
		} else if recorded.Missed.How == runstate.MissConversationHeld {
			how = "its wait for a held conversation ended before its first turn"
		}
		fmt.Fprintf(&rendered, "  MISSED PASS: its %s — %s\n", recorded.Missed.Trigger.Describe(), how)
	}
	// A program manager's pass says what it was handed, so a burst that woke the
	// instance once reads as one pass carrying the burst.
	if carried := describeCarried(recorded.Events); carried != "" {
		fmt.Fprintf(&rendered, "  carried %s since its last pass\n", carried)
	}
	// A development manager's pass says how much of the docket it showed whole,
	// so a pass that only saw most of it in one line reads as one that did.
	if recorded.Docket != nil {
		if shown := recorded.Docket.Shown(); shown != "" {
			fmt.Fprintf(&rendered, "  %s\n", shown)
		}
	}
	if recorded.Result == nil {
		fmt.Fprintf(&rendered, "  no account of this pass was recorded: %s\n", nonEmptySweepProblem(recorded.Problem))
		return rendered.String()
	}
	for _, question := range recorded.Result.Questions {
		fmt.Fprintf(&rendered, "  ? %s\n", question)
	}
	if summary := strings.TrimSpace(recorded.Result.Summary); summary != "" {
		fmt.Fprintf(&rendered, "  %s\n", summary)
	}
	for _, finding := range recorded.Result.Findings {
		fmt.Fprintf(&rendered, "  - [%s] %s\n", finding.Disposition, finding.Issue)
		if detail := strings.TrimSpace(finding.Detail); detail != "" {
			fmt.Fprintf(&rendered, "      %s\n", detail)
		}
		if len(finding.Filed) > 0 {
			fmt.Fprintf(&rendered, "      filed: %s\n", strings.Join(finding.Filed, ", "))
		}
		if finding.SilentRepair() {
			fmt.Fprintf(&rendered, "      fixed with nothing filed for the root cause\n")
		}
	}
	// A pass whose findings left nothing outside its account says so under
	// them, because what it found is lost to the role at its next compaction.
	if recorded.Untraced {
		rendered.WriteString("  UNTRACED: it left no trace of these findings — no memory written, no lane report changed, no report filed, no work admitted; its next pass is told which they were\n")
	}
	// What the role recommended on the changes proposed to its own documents,
	// after the findings: it is the batch `yoyo amendment` decides from, and the
	// verdict is the role's argument rather than anything settled.
	for _, recommendation := range recorded.Result.Recommendations {
		verdict := string(recommendation.Verdict)
		if recommendation.Verdict == sweep.RecommendMerge && strings.TrimSpace(recommendation.Into) != "" {
			verdict += " into " + strings.TrimSpace(recommendation.Into)
		}
		fmt.Fprintf(&rendered, "  > recommends %s for %s\n", verdict, recommendation.Proposal)
		if reason := strings.TrimSpace(recommendation.Reason); reason != "" {
			fmt.Fprintf(&rendered, "      %s\n", reason)
		}
	}
	// What the pass found auditing closed work against the standing goals: every
	// item it checked, which goals, and what became of each violation.
	if len(recorded.Result.Audits) > 0 {
		fmt.Fprintf(&rendered, "  audited %d closed item(s) against the standing goals\n", len(recorded.Result.Audits))
	}
	for _, audit := range recorded.Result.Audits {
		fmt.Fprintf(&rendered, "  * %s %s, checked against %s\n", strings.ToUpper(string(audit.Finding)), audit.Item, strings.Join(audit.Goals, "; "))
		if detail := strings.TrimSpace(audit.Detail); detail != "" {
			fmt.Fprintf(&rendered, "      %s\n", detail)
		}
		switch {
		case strings.TrimSpace(audit.Correction) != "":
			fmt.Fprintf(&rendered, "      corrected by %s\n", strings.TrimSpace(audit.Correction))
		case audit.Deferred:
			rendered.WriteString("      deferred: this pass had admitted its corrections, so a later pass takes it\n")
		}
	}
	if recorded.Problem != "" {
		fmt.Fprintf(&rendered, "  %s\n", recorded.Problem)
	}
	return rendered.String()
}

// renderHarnessPass writes one pass of the supervisor's own maintenance: it
// woke no role and spent nothing, so the header says whose pass it was, and
// each step follows in the order it was taken with what became of it. A step
// that was skipped or failed is written in capitals, because a pass that did
// less than it was meant to is what a reader of these is looking for.
func renderHarnessPass(recorded runstate.Sweep) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "%s  %s (the supervisor's own pass, no role woken)\n",
		recorded.StartedAt.UTC().Format(time.RFC3339), recorded.Task)
	if recorded.Result != nil {
		if summary := strings.TrimSpace(recorded.Result.Summary); summary != "" {
			fmt.Fprintf(&rendered, "  %s\n", summary)
		}
	}
	for _, step := range recorded.Steps {
		outcome := string(step.Outcome)
		if step.Outcome != runstate.StepRan {
			outcome = strings.ToUpper(outcome)
		}
		fmt.Fprintf(&rendered, "  - %s: %s", step.Name, outcome)
		if detail := strings.TrimSpace(step.Detail); detail != "" {
			fmt.Fprintf(&rendered, ", %s", detail)
		}
		rendered.WriteString("\n")
	}
	return rendered.String()
}

// describeCarried says how many events of each class a pass was handed, in the
// order the classes are defined, and nothing for a pass handed none.
func describeCarried(events map[string]int) string {
	var parts []string
	for _, class := range config.TriggerEvents {
		count := events[string(class)]
		switch {
		case count == 1:
			parts = append(parts, "1 "+strings.TrimSuffix(string(class), "s"))
		case count > 1:
			parts = append(parts, fmt.Sprintf("%d %s", count, class))
		}
	}
	return strings.Join(parts, ", ")
}

func nonEmptySweepProblem(problem string) string {
	if trimmed := strings.TrimSpace(problem); trimmed != "" {
		return trimmed
	}
	return "nothing was recorded about what stopped it"
}

func printSweepsUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo sweeps [options]

What the recurring tasks found on their own schedule. A recurring task wakes a
role every so often to look at its own domain -- the development manager over
work that has stopped moving, say -- and nobody is watching those turns, so each
firing ends in a durable report. This is where they are read.

Each pass leads with the questions it could not settle itself, because that is
the only part of a report that asks for anything: a report with no questions
needs no attention. Below them come the pass's summary and what it found, each
finding with what the role did about it -- fixed, filed, consulted, or left --
and the work it filed for the root cause. A fix that filed nothing is named as
one, which is the whole of what a run of these reports is read for.

A finding has to leave a trace outside the account on the same pass: a memory
written, a lane report changed, a report filed, or work admitted. A pass whose
findings left none is marked UNTRACED; its task's next pass is told which
findings they were, and until then "yoyo status" shows it waiting on the role.

Each pass's header names the model its turns ran on: the task's own where it
names one with "model", the role's configured model where it does not, and the
alternate where the provider moved the turn. "yoyo status --spend" sums the
same records by task and model.

On a development manager's pass some findings are the harness's own: the open
pull requests the forge is holding for work that is closed, or for a branch the
target already carries. Each is stated once, as left, and closing it is
somebody's decision rather than the harness's.

On a pass of a role that owns documents -- the architect over the designs and
decisions, the product manager over the brief and goals -- the harness puts the
undecided changes other roles proposed to those documents in front of it,
oldest first and at most ten a pass, and the account carries what it recommended
on each: approve, decline, or merge with another, with the reason. Those are
shown after the findings. They are recommendations and never decisions: "yoyo
amendment approve" and "yoyo amendment decline" record each one, under the
owner's authority. Each pass's batch is said to you once, directly, as one
decision list, and "yoyo status" names it until every proposal in it is decided.

Four outcomes look alike and are not: a pass that found nothing shows its own
summary and no findings, which on a healthy harness is most of them; a pass that
produced no account says so and names what stopped it; a pass stopped by its
turn bound is recorded as partial, so it is never mistaken for a finished one;
and a firing that failed before its first turn -- a message the harness
refused, a conversation that would not open, a turn that would not assemble --
is marked FAILED FIRING with its cause. A task that fails that way twice in a
row is also on "yoyo status"'s needs-a-human line and said in the channel, and
the first firing that takes a turn ends that pre-turn signal; a product pass
finding raised after three failures clears only when the pass next carries out
its work. A pass that carried out its actions and says more is waiting is
partial, not failed, and ends a run of failures; so is one whose reply wrote its
report block more than once, which is read by the last.

A product pass that fails three times in a row also files one finding in the
report pile. The factory-flow program manager watches and must answer it in
her next pass and existing digest; the development manager resolves the cause.
Without a factory-flow instance, the development manager watches too. The same
finding appears in status and the dashboard's Factory problems section, with
its failure count kept current, each line leading with what went wrong in
ordinary words. Only that pass carrying out its work clears it, recording
the total failures in the report handling log. A report handling naming a
person-only remedy names the operator with the exact step.

A missed pass is marked MISSED PASS with the trigger that owed it: a schedule
or an instance's events that no pull took for a whole interval, or a pass
cancelled before it completed because the session carrying it stopped.

A program manager instance's passes are listed under the instance's name, and
a pass its events woke -- landings, admissions, stoppages since its last pass
-- says how many of each it carried under its header.

A pass the intake brake summoned ahead of its schedule says so under its header,
naming what tripped the brake. It is the development manager's sweep fired the
moment the line stopped, with the blocked runs in front of her, and it counts as
a firing: the schedule runs on from it.

The twenty most recent passes are shown by default, which for an hourly task is
under a day. "--limit 200" reads further back and "--limit 0" reads every pass
recorded, which is what the question these reports exist for actually needs: a
week of them, read together, is how filed root-cause work is told from the same
thing quietly repaired seven times. "--json" is never bounded and always carries
the whole log.

A line of the log that will not decode -- a write a crash interrupted -- is named
and set aside rather than failing the listing, so one torn record never costs the
reports around it.

It is read-only. A sweep is written once and never revised, and nothing here
fires one, retires one, or decides anything about what a pass found. Which tasks
run, how often, and what they are told is configuration; see the recurring tasks
section of docs/configuration.md.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --task <name>     show only this recurring task's sweeps
  --limit <n>       show this many of the most recent passes (default 20, 0 for all)
  --json            emit machine-readable JSON`)
}
