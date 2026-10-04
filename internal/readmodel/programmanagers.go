package readmodel

// The program manager instances, as the read model carries them.
//
// `docs/designs/program-manager.md` has every surface read an instance from one
// query — the dashboard's card, `yoyo status --json` under
// `standing.program_managers`, `yoyo status`'s line per instance, the channel's
// hourly line, and the other instances' opening lines — so that the page, the
// terminal, and the channel cannot disagree about one. The query carries the
// instance's lane, its status, the blockers its lane report names that the
// record can find, the claims it cannot, where its report is, and its open
// restart requests.
//
// # The status is derived, never written
//
// Nothing an instance writes sets its own status. Blocked is read off the
// citations in its latest lane report, each resolved against the records the
// instance could have raised: a report the product manager has not handled, an
// amendment nobody has decided, an exchange still open, a restart request
// nothing has answered. Only a record of the instance's own that is still open
// makes a blocker; a citation that resolves to nothing, to another instance's
// record, or to one already decided is carried as a claim with the reason, and
// blocks nothing. That is what makes the word mean the same for every instance.
//
// Stale is read off the pass records: no completed pass within twice the
// instance's `every`. It is plain Go over durable files, with no provider call
// anywhere on the path, for the reason the stall watchdog is — a watcher that
// asks a model anything pauses with the provider's usage window, and staleness
// is the out-of-band signal that the watcher itself is not watching. The sweep
// in internal/watchdog holds both derivations to that.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RestartRequests is the durable log of program managers' restart requests,
// answered and unanswered, as the read model asks about it. It is satisfied by
// *runstate.RestartRequestStore.
type RestartRequests interface {
	List() ([]runstate.RestartRequest, error)
}

// LaneReports is every instance's current lane report, and where it is kept. It
// is satisfied by *runstate.LaneReportStore.
type LaneReports interface {
	Current(agent string) (runstate.LaneReport, bool, error)
	ReportPath(agent string) string
}

// Passes is the log of recurring-task firings, each one a pass. It is satisfied
// by *runstate.SweepStore.
type Passes interface {
	List() ([]runstate.Sweep, []runstate.UnreadableSweep, error)
}

// Exchanges is every ask one role has put to another. It is satisfied by
// *runstate.ExchangeStore.
type Exchanges interface {
	List() ([]exchange.Exchange, error)
}

// FirstSeen is when each instance was first seen in the loaded configuration.
// It is satisfied by *runstate.FirstSeenStore.
type FirstSeen interface {
	FirstSeen() (map[string]time.Time, error)
}

// ProgramManagerInstance is one configured program manager instance, as the
// configuration names it: the agent, the lane it owns, and its schedule.
type ProgramManagerInstance struct {
	Agent string
	Lane  string
	// Every is triggers.every, and zero for an instance only events wake. An
	// instance with no schedule has nothing to be stale against.
	Every time.Duration
}

// ProgramManagerStatus is the one word an instance's status is shown as.
type ProgramManagerStatus string

const (
	// ProgramManagerBlocked is an instance whose latest report names an open ask
	// of its own, to a named mover, that the record can find.
	ProgramManagerBlocked ProgramManagerStatus = "blocked"
	// ProgramManagerStale is an instance with no completed pass within twice its
	// schedule. It outranks blocked in the word shown; both are carried.
	ProgramManagerStale ProgramManagerStatus = "stale"
	// ProgramManagerWorking is neither.
	ProgramManagerWorking ProgramManagerStatus = "working"
)

// CitedRecord is which kind of record a blocker's citation resolved to.
type CitedRecord string

const (
	CitedReport         CitedRecord = "report"
	CitedAmendment      CitedRecord = "amendment"
	CitedExchange       CitedRecord = "exchange"
	CitedRestartRequest CitedRecord = "restart-request"
)

// ProgramManagerBlocker is one blocker the instance's report names whose
// citation resolved to an open record of the instance's own.
type ProgramManagerBlocker struct {
	What      string      `json:"what"`
	WaitingOn Mover       `json:"waiting_on"`
	Cites     string      `json:"cites"`
	Record    CitedRecord `json:"record"`
}

// ProgramManagerClaim is one blocker the report names that the record does not
// bear out, and why. It is shown and blocks nothing.
type ProgramManagerClaim struct {
	What      string `json:"what"`
	WaitingOn Mover  `json:"waiting_on"`
	Cites     string `json:"cites"`
	SaidCites string `json:"said_cites,omitempty"`
	Reason    string `json:"reason"`
}

// ProgramManagerMiss is one missed pass of an instance, as its sweep record
// says it.
type ProgramManagerMiss struct {
	// Trigger is what owed the pass and How whether no pass followed it or the
	// pass was cancelled before it completed, in the sweep record's own words.
	Trigger runstate.PassTrigger `json:"trigger"`
	How     runstate.MissKind    `json:"how"`
	// At is when the pass fell due, or was taken where it was cancelled, and
	// RecordedAt when the harness recorded it missed.
	At         time.Time `json:"at"`
	RecordedAt time.Time `json:"recorded_at"`
	// Says is the record's account, the cause among it where one is known.
	Says string `json:"says"`
	// WaitingOn is whose move ends it: the harness's, whose next pass does.
	WaitingOn Mover `json:"waiting_on"`
}

// sentence is the miss as the instance's line says it.
func (m ProgramManagerMiss) sentence() string {
	what := "missed its " + m.Trigger.Describe()
	if m.How == runstate.MissCancelled {
		what = "its " + m.Trigger.Describe() + " was cancelled before it completed"
	}
	return fmt.Sprintf("%s at %s — the %s's — %s", what, m.At.UTC().Format(time.RFC3339), m.WaitingOn, m.Says)
}

// ProgramManager is one program manager instance as the read model carries it.
type ProgramManager struct {
	// Agent is the instance: the configured agent's name.
	Agent string `json:"agent"`
	// Lane is the tracker label the instance owns, and empty for an instance the
	// configuration no longer names or names with no lane.
	Lane string `json:"lane,omitempty"`
	// Status is the one word shown. Stale and Blocked are both carried, because
	// stale outranks blocked in the word and the card shows both.
	Status  ProgramManagerStatus `json:"status"`
	Stale   bool                 `json:"stale"`
	Blocked bool                 `json:"blocked"`
	// StaleSays is why the instance is stale, in a sentence, and empty where it
	// is not.
	StaleSays string `json:"stale_says,omitempty"`
	// LastCompletedPassAt is when the instance's latest completed pass ended,
	// and absent where none has.
	LastCompletedPassAt *time.Time `json:"last_completed_pass_at,omitempty"`
	// MissedPass is the latest pass the instance's triggers owed it that the
	// sweep log records as missed since its last completed pass — one no pull
	// took, or one cancelled before it completed — and absent where none is.
	// It is the harness's to clear, by taking the next pass, and it does not
	// change the word: a missed pass is why an instance goes on to read stale,
	// and is said beside whatever word it reads.
	MissedPass *ProgramManagerMiss `json:"missed_pass,omitempty"`
	// Blockers are the report's blockers that resolved to an open record of the
	// instance's own, and Claims the ones that did not, each with its reason.
	// Both are empty rather than absent.
	Blockers []ProgramManagerBlocker `json:"blockers"`
	Claims   []ProgramManagerClaim   `json:"claims"`
	// ReportPath is where the instance's current report is kept, and
	// ReportWrittenAt when it was written, absent where it has written none.
	ReportPath      string     `json:"report_path,omitempty"`
	ReportWrittenAt *time.Time `json:"report_written_at,omitempty"`
	// RestartRequests is every request the instance made that nothing has
	// answered, oldest first, carried whole in the store's own type. It is empty
	// rather than absent for an instance with none.
	RestartRequests []runstate.RestartRequest `json:"restart_requests"`
}

// ReadProgramManagers is every program manager instance: each one the
// configuration names, and each one with an open request on record whether or
// not it is still configured — a request outlives an edit to the configuration,
// and one that is still open is still somebody's to answer. The problem names
// every record that could not be read; the instances are still listed, and
// what could not be read is not reported as none.
func ReadProgramManagers(sources Sources) ([]ProgramManager, string) {
	instances := map[string]ProgramManagerInstance{}
	for _, instance := range sources.ProgramManagers {
		instances[instance.Agent] = instance
	}
	records := readCitable(sources)
	for _, request := range records.restartRequests {
		if _, known := instances[request.Agent]; !known && request.Open() {
			instances[request.Agent] = ProgramManagerInstance{Agent: request.Agent}
		}
	}
	if len(instances) == 0 {
		return nil, strings.Join(records.problems, "; ")
	}
	passes := readCompletedPasses(sources)
	problems := append(append([]string{}, records.problems...), passes.problems...)

	agents := make([]string, 0, len(instances))
	for agent := range instances {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	now := sources.now()
	wording := ReadTextTerms(sources.Repository)
	derived := make([]ProgramManager, 0, len(agents))
	for _, agent := range agents {
		instance, problem := deriveProgramManager(sources, instances[agent], records, passes, now)
		if problem != "" {
			problems = append(problems, problem)
		}
		derived = append(derived, wordingProgramManager(instance, wording))
	}
	return derived, strings.Join(problems, "; ")
}

// ProgramManagerOf is one instance's query: what ReadProgramManagers carries
// for it, and whether the read model knows the instance at all.
func ProgramManagerOf(sources Sources, agent string) (ProgramManager, bool, string) {
	instances, problem := ReadProgramManagers(sources)
	for _, instance := range instances {
		if instance.Agent == agent {
			return instance, true, problem
		}
	}
	return ProgramManager{}, false, problem
}

// ErrNoSuchProgramManager is the read model knowing no instance by the name
// asked for: none is configured under it, and none has a request open. A
// surface tells it apart from a record that could not be read.
var ErrNoSuchProgramManager = errors.New("no program manager instance is recorded under that name")

// ValidProgramManagerName reports whether a name has the shape an agent's name
// has. A surface checks it before it asks for an instance by a name it was
// handed, and refuses one that does not without reflecting it.
func ValidProgramManagerName(agent string) bool {
	return domain.ValidateIdentifier("agent", agent) == nil
}

// LaneReportText is the part of an instance's current lane report that only
// the report says: its summary, what remains, and which version wrote it. What
// the report's blockers come to is the instance's own derivation, carried
// beside it as Blockers and Claims, so it is not said twice here.
type LaneReportText struct {
	Summary   string   `json:"summary"`
	Remaining []string `json:"remaining"`
	Version   int      `json:"version"`
	// Pass is the pass that wrote it, and empty where an operator's own turn in
	// the instance's conversation did.
	Pass           string    `json:"pass,omitempty"`
	ConversationID string    `json:"conversation_id"`
	Turn           int       `json:"turn"`
	WrittenAt      time.Time `json:"written_at"`
}

// ProgramManagerReport is the per-instance query the dashboard opens a report
// from: the instance as the standing carries it, and its current report whole.
type ProgramManagerReport struct {
	ObservedAt time.Time      `json:"observed_at"`
	Instance   ProgramManager `json:"instance"`
	// Report is absent where the instance has written none, or where its report
	// could not be read, which Problem then says.
	Report *LaneReportText `json:"report,omitempty"`
	// Problem names every record behind the instance that could not be read. The
	// instance is still carried; what could not be read is not reported as none.
	Problem string `json:"problem,omitempty"`
}

// ReadProgramManagerReport is one instance's query: what the standing carries
// for it, and the report its blockers were read from. The two are read from one
// version: a report rewritten between the derivation and the read of its text
// is read again, so the summary and the blockers beside it are one pass's.
func ReadProgramManagerReport(sources Sources, agent string) (ProgramManagerReport, error) {
	if !ValidProgramManagerName(agent) {
		return ProgramManagerReport{}, ErrNoSuchProgramManager
	}
	var answer ProgramManagerReport
	for attempt := 0; attempt < 2; attempt++ {
		instance, known, problem := ProgramManagerOf(sources, agent)
		if !known {
			if problem != "" {
				return ProgramManagerReport{}, fmt.Errorf("%w, and what could say so could not all be read: %s", ErrNoSuchProgramManager, problem)
			}
			return ProgramManagerReport{}, ErrNoSuchProgramManager
		}
		answer = ProgramManagerReport{ObservedAt: sources.now(), Instance: instance, Problem: problem}
		if sources.LaneReports == nil || instance.ReportWrittenAt == nil {
			return answer, nil
		}
		current, written, err := sources.LaneReports.Current(agent)
		if err != nil || !written {
			// The derivation has already said the report could not be read, or
			// the report has gone since; either way there is no text to carry.
			return answer, nil
		}
		if !current.RecordedAt.Equal(*instance.ReportWrittenAt) {
			continue
		}
		// The report is the role's own words, and a role that named work by its
		// number alone is read here with each item's title beside it: the card is
		// the surface a person reads it on.
		titles, _ := ReadWorkItemTitles(context.Background(), sources)
		answer.Instance = citeProgramManager(answer.Instance, titles)
		wording := ReadTextTerms(sources.Repository)
		remaining := make([]string, 0, len(current.Report.Remaining))
		for _, entry := range current.Report.Remaining {
			remaining = append(remaining, wording.Render(titles.Cite(entry)))
		}
		answer.Report = &LaneReportText{
			Summary:        wording.Render(titles.Cite(current.Report.Summary)),
			Remaining:      remaining,
			Version:        current.Version,
			Pass:           current.Stamp.Pass,
			ConversationID: current.Stamp.ConversationID,
			Turn:           current.Stamp.Turn,
			WrittenAt:      current.RecordedAt,
		}
		return answer, nil
	}
	answer.Problem = joinProblems(answer.Problem, fmt.Sprintf("the %s lane report was rewritten twice while it was being read, so its text is not shown beside blockers from another version; ask again", agent))
	return answer, nil
}

// citeProgramManager is an instance with every work item its report's
// blockers name shown beside its title.
func citeProgramManager(instance ProgramManager, titles *WorkItemTitles) ProgramManager {
	blockers := make([]ProgramManagerBlocker, 0, len(instance.Blockers))
	for _, blocker := range instance.Blockers {
		blocker.What = titles.Cite(blocker.What)
		blockers = append(blockers, blocker)
	}
	claims := make([]ProgramManagerClaim, 0, len(instance.Claims))
	for _, claim := range instance.Claims {
		claim.What = titles.Cite(claim.What)
		claim.Reason = titles.Cite(claim.Reason)
		claim.SaidCites = ""
		if said := titles.Cite(claim.Cites); said != claim.Cites {
			claim.SaidCites = said
		}
		claims = append(claims, claim)
	}
	instance.Blockers, instance.Claims = blockers, claims
	requests := make([]runstate.RestartRequest, len(instance.RestartRequests))
	copy(requests, instance.RestartRequests)
	for index := range requests {
		requests[index].Reason = titles.Cite(requests[index].Reason)
	}
	instance.RestartRequests = requests
	return instance
}

func deriveProgramManager(sources Sources, instance ProgramManagerInstance, records citable, passes completedPasses, now time.Time) (ProgramManager, string) {
	derived := ProgramManager{
		Agent:           instance.Agent,
		Lane:            instance.Lane,
		Blockers:        []ProgramManagerBlocker{},
		Claims:          []ProgramManagerClaim{},
		RestartRequests: []runstate.RestartRequest{},
	}
	for _, request := range records.restartRequests {
		if request.Agent == instance.Agent && request.Open() {
			derived.RestartRequests = append(derived.RestartRequests, request)
		}
	}

	var problem string
	if sources.LaneReports != nil {
		derived.ReportPath = sources.LaneReports.ReportPath(instance.Agent)
		current, written, err := sources.LaneReports.Current(instance.Agent)
		switch {
		case err != nil:
			problem = fmt.Sprintf("the %s lane report could not be read, so what it is blocked on cannot be said: %v", instance.Agent, err)
		case written:
			at := current.RecordedAt
			derived.ReportWrittenAt = &at
			for _, blocker := range current.Report.Blockers {
				resolved, reason := records.resolve(instance.Agent, blocker.Cites)
				if reason != "" {
					derived.Claims = append(derived.Claims, ProgramManagerClaim{
						What: blocker.What, WaitingOn: Mover(blocker.WaitingOn), Cites: blocker.Cites, Reason: reason,
					})
					continue
				}
				derived.Blockers = append(derived.Blockers, ProgramManagerBlocker{
					What: blocker.What, WaitingOn: Mover(blocker.WaitingOn), Cites: blocker.Cites, Record: resolved,
				})
			}
		}
	}
	derived.Blocked = len(derived.Blockers) > 0

	last, completed := passes.last[instance.Agent]
	if completed {
		derived.LastCompletedPassAt = &last
	}
	if missed, found := passes.missed[instance.Agent]; found && (!completed || !last.After(missed.At)) {
		derived.MissedPass = &missed
	}
	derived.Stale, derived.StaleSays = staleness(instance, last, completed, passes.activated[instance.Agent], passes.readable, now)

	switch {
	case derived.Stale:
		derived.Status = ProgramManagerStale
	case derived.Blocked:
		derived.Status = ProgramManagerBlocked
	default:
		derived.Status = ProgramManagerWorking
	}
	return derived, problem
}

// staleness is whether an instance has gone without a completed pass for twice
// its schedule, measured from its last completed pass, or from when it was
// activated where none has completed. Activation is the earliest durable trace
// of the instance: the moment the harness first saw it in the loaded
// configuration, or its first conversation where that is earlier — an instance
// configured before the first-seen record existed has only its conversation to
// go on. So an instance the scheduler has never woken is still measured from
// the load that first carried it, and reads stale twice its schedule after,
// which is the dead scheduler the reading exists to catch.
//
// A pass log that could not be read decides nothing: an instance called stale
// over a file nobody could open would be a confident answer about nothing, and
// the problem says so instead.
func staleness(instance ProgramManagerInstance, last time.Time, completed bool, activated activation, readable bool, now time.Time) (bool, string) {
	if instance.Every <= 0 || !readable {
		return false, ""
	}
	bound := 2 * instance.Every
	if completed {
		if now.Sub(last) > bound {
			return true, fmt.Sprintf("no pass has completed since %s, and its schedule is every %s",
				last.UTC().Format(time.RFC3339), instance.Every)
		}
		return false, ""
	}
	if activated.at.IsZero() || now.Sub(activated.at) <= bound {
		return false, ""
	}
	how := "first woken"
	if activated.seen {
		how = "first seen in the configuration"
	}
	return true, fmt.Sprintf("no pass has ever completed, and it was %s at %s on a schedule of every %s",
		how, activated.at.UTC().Format(time.RFC3339), instance.Every)
}

// activation is the earliest trace of an instance, and whether it is the
// configuration's first-seen record rather than a conversation.
type activation struct {
	at   time.Time
	seen bool
}

// completedPasses is every instance's last completed pass and first trace.
type completedPasses struct {
	last map[string]time.Time
	// missed is each instance's latest missed pass. A miss is read by the task
	// it was recorded under, which is the instance's name, because a pass that
	// was never taken happened in no conversation.
	missed    map[string]ProgramManagerMiss
	activated map[string]activation
	// readable is whether both the pass log and the conversations were read, so
	// that the absence of a pass means none was recorded.
	readable bool
	problems []string
}

// readCompletedPasses attributes each pass to the instance whose conversation
// it happened in, and keeps the latest that completed. A completed pass is one
// that ended in an account: a pass the provider refused, a turn the size
// backstop rejected, and a pass that answered without the block all end with
// none, and all look the same here, which is the point.
func readCompletedPasses(sources Sources) completedPasses {
	passes := completedPasses{last: map[string]time.Time{}, missed: map[string]ProgramManagerMiss{}, activated: map[string]activation{}, readable: true}
	if sources.FirstSeen != nil {
		seen, err := sources.FirstSeen.FirstSeen()
		if err != nil {
			passes.problems = append(passes.problems, fmt.Sprintf("when the program managers were first seen in the configuration could not be read, so one that has never been woken is not called stale: %v", err))
		}
		for agent, at := range seen {
			passes.activated[agent] = activation{at: at, seen: true}
		}
	}
	if sources.Passes == nil || sources.Conversations == nil {
		passes.readable = false
		passes.problems = append(passes.problems, "nothing was wired to read the program managers' passes, so none of them is called stale")
		return passes
	}
	conversations, err := sources.Conversations.Recorded()
	if err != nil {
		passes.readable = false
		passes.problems = append(passes.problems, fmt.Sprintf("the conversations could not be read, so no program manager's passes can be attributed to it and none is called stale: %v", err))
		return passes
	}
	agentOf := make(map[string]string, len(conversations))
	for _, conversation := range conversations {
		agent := conversation.Agent
		if agent == "" {
			agent = string(conversation.Role)
		}
		agentOf[conversation.ConversationID] = agent
		if first, seen := passes.activated[agent]; !seen || conversation.StartedAt.Before(first.at) {
			passes.activated[agent] = activation{at: conversation.StartedAt}
		}
	}
	recorded, unreadable, err := sources.Passes.List()
	if err != nil {
		passes.readable = false
		passes.problems = append(passes.problems, fmt.Sprintf("the pass records could not be read, so no program manager is called stale: %v", err))
		return passes
	}
	if len(unreadable) > 0 {
		passes.problems = append(passes.problems, fmt.Sprintf("%d pass record(s) could not be read, so a program manager's last completed pass may be later than is said", len(unreadable)))
	}
	for _, pass := range recorded {
		if pass.Missed != nil && pass.Role == domain.RoleProgramManager {
			if earlier, seen := passes.missed[pass.Task]; !seen || pass.StartedAt.After(earlier.At) {
				passes.missed[pass.Task] = ProgramManagerMiss{
					Trigger:    pass.Missed.Trigger,
					How:        pass.Missed.How,
					At:         pass.StartedAt,
					RecordedAt: pass.EndedAt,
					Says:       pass.Problem,
					WaitingOn:  MoverHarness,
				}
			}
			continue
		}
		agent, known := agentOf[pass.ConversationID]
		if !known || pass.Result == nil {
			continue
		}
		if last, seen := passes.last[agent]; !seen || pass.EndedAt.After(last) {
			passes.last[agent] = pass.EndedAt
		}
	}
	return passes
}

// citable is every record a lane report's blocker may cite, read once for all
// the instances.
type citable struct {
	restartRequests []runstate.RestartRequest
	reports         map[string]report.Report
	handled         map[string]report.Handling
	amendments      []amendment.Record
	exchanges       map[string]exchange.Exchange
	// unread names each kind of record that could not be read, so a citation
	// that resolves to none of the rest is not said to resolve to nothing.
	unread   []string
	problems []string
}

func readCitable(sources Sources) citable {
	records := citable{reports: map[string]report.Report{}, exchanges: map[string]exchange.Exchange{}}
	unreadable := func(kind, problem string) {
		records.unread = append(records.unread, kind)
		records.problems = append(records.problems, problem)
	}
	if sources.RestartRequests != nil {
		requests, err := sources.RestartRequests.List()
		if err != nil {
			unreadable("restart requests", fmt.Sprintf("the program managers' restart requests could not be read: %v", err))
		}
		records.restartRequests = requests
	}
	if sources.Reports != nil {
		filed, err := sources.Reports.List()
		if err != nil {
			unreadable("reports", fmt.Sprintf("the collected reports could not be read, so a program manager's blocker citing one cannot be resolved: %v", err))
		}
		for _, entry := range filed {
			records.reports[entry.ID] = entry
		}
		if err == nil {
			handlings, err := sources.Reports.Handlings()
			if err != nil {
				records.reports = map[string]report.Report{}
				unreadable("reports", fmt.Sprintf("what became of the collected reports could not be read, so a program manager's blocker citing one cannot be resolved: %v", err))
			}
			records.handled = report.Handled(handlings)
		}
	}
	if sources.Amendments != nil {
		proposals, err := sources.Amendments.List()
		if err != nil {
			unreadable("amendments", fmt.Sprintf("the proposed amendments could not be read, so a program manager's blocker citing one cannot be resolved: %v", err))
		}
		records.amendments = proposals
	}
	if sources.Exchanges != nil {
		asked, err := sources.Exchanges.List()
		if err != nil {
			unreadable("exchanges", fmt.Sprintf("the exchanges could not be read, so a program manager's blocker citing one cannot be resolved: %v", err))
		}
		for _, entry := range asked {
			records.exchanges[entry.ID] = entry
		}
	}
	return records
}

// resolve is what one citation names: the kind of open record of the agent's
// own it resolves to, or the reason it is a claim rather than a blocker.
func (c citable) resolve(agent, cites string) (CitedRecord, string) {
	for _, request := range c.restartRequests {
		if request.ID != cites {
			continue
		}
		if request.Agent != agent {
			return "", fmt.Sprintf("it cites restart request %s, which %s made rather than this instance", cites, request.Agent)
		}
		if !request.Open() {
			return "", fmt.Sprintf("it cites restart request %s, which was answered at %s", cites, request.AnsweredAt.UTC().Format(time.RFC3339))
		}
		return CitedRestartRequest, ""
	}
	if filed, found := c.reports[cites]; found {
		if filed.Agent != agent {
			return "", fmt.Sprintf("it cites report %s, which %s filed rather than this instance", cites, nameOrNobody(filed.Agent))
		}
		if handling, handled := c.handled[cites]; handled {
			return "", fmt.Sprintf("it cites report %s, which the %s handled at %s", cites, handling.Role.Title(), handling.RecordedAt.UTC().Format(time.RFC3339))
		}
		return CitedReport, ""
	}
	if proposal, found := amendment.Find(c.amendments, cites); found {
		if proposal.Agent != agent {
			return "", fmt.Sprintf("it cites amendment %s, which %s proposed rather than this instance", cites, nameOrNobody(proposal.Agent))
		}
		if decision, decided := amendment.DecisionOn(c.amendments, cites); decided {
			return "", fmt.Sprintf("it cites amendment %s, which was decided (%s) at %s", cites, decision.Verdict, decision.DecidedAt.UTC().Format(time.RFC3339))
		}
		return CitedAmendment, ""
	}
	if asked, found := c.exchanges[cites]; found {
		if asked.Asker.Agent != agent {
			return "", fmt.Sprintf("it cites exchange %s, which %s asked rather than this instance", cites, nameOrNobody(asked.Asker.Agent))
		}
		if !asked.Open() {
			return "", fmt.Sprintf("it cites exchange %s, which closed %s", cites, asked.Outcome)
		}
		return CitedExchange, ""
	}
	if len(c.unread) > 0 {
		return "", fmt.Sprintf("it cites %s, which no record that could be read holds; the %s could not be read", cites, strings.Join(c.unread, " and the "))
	}
	return "", fmt.Sprintf("it cites %s, which is no request, report, amendment, or exchange on record", cites)
}

func nameOrNobody(agent string) string {
	if strings.TrimSpace(agent) == "" {
		return "an agent the record does not name"
	}
	return agent
}

// StaleProgramManagers is the instances whose status is stale, by name.
func (s Standing) StaleProgramManagers() []string {
	var stale []string
	for _, instance := range s.ProgramManagers {
		if instance.Stale {
			stale = append(stale, instance.Agent)
		}
	}
	return stale
}
