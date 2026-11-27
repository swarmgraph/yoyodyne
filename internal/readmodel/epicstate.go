package readmodel

// Where each epic the operator named stands, worked out from the records at each
// reading: docs/designs/epic-mode-second-part.md, piece 8.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epiccost"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// EpicState is where a named epic stands. It is empty where it could not be
// worked out.
type EpicState string

const (
	EpicCancelled       EpicState = "cancelled"
	EpicIntegrated      EpicState = "delivered-integrated"
	EpicHandedOver      EpicState = "delivered-handed-over"
	EpicWorking         EpicState = "working"
	EpicOutOfBudget     EpicState = "out-of-budget"
	EpicWaitsOnOperator EpicState = "waiting-on-the-operator"
	EpicBlockedOutside  EpicState = "blocked-outside-the-factory"
)

var epicStateSays = map[EpicState]string{
	EpicCancelled:       "Cancelled: you ended it before it was delivered",
	EpicIntegrated:      "Delivered, integrated: every member's change is on the target branch",
	EpicHandedOver:      "Delivered, handed over: every change is approved, and at least one pull request is not merged",
	EpicWorking:         "Working",
	EpicOutOfBudget:     "Out of budget",
	EpicWaitsOnOperator: "Waiting on the operator",
	EpicBlockedOutside:  "Blocked outside the factory",
}

// EpicWait is one thing a member, or the whole epic where no member is named,
// waits for, and who moves it.
type EpicWait struct {
	WorkItemID string `json:"work_item_id,omitempty"`
	Owner      Mover  `json:"owner"`
	Says       string `json:"says"`
	// CheckOwed is the check of the whole epic owed and not begun, and
	// CheckUnderway one begun and not finished. The harness makes both.
	CheckOwed     bool `json:"check_owed,omitempty"`
	CheckUnderway bool `json:"check_underway,omitempty"`
	// step is what is owed, where two records can ask for the same one: a question
	// and a hold of the same step are one wait.
	step string
}

// CheckDue reports an epic whose check of the whole epic the harness owes: one
// not begun, or one begun and not finished, which it looks up before going on.
func (s EpicStanding) CheckDue() bool {
	return slices.ContainsFunc(s.Waits, func(wait EpicWait) bool { return wait.CheckOwed || wait.CheckUnderway })
}

// outside is a wait that no role, the harness or the operator ends.
func (w EpicWait) outside() bool {
	return slices.Contains([]Mover{MoverForge, MoverProvider, MoverNobody}, w.Owner)
}

// EpicStanding is the one state of an epic and what it waits for.
type EpicStanding struct {
	State   EpicState  `json:"state,omitempty"`
	Waits   []EpicWait `json:"waits,omitempty"`
	Problem string     `json:"state_problem,omitempty"`
	// Budget is why the next start is refused by the epic's budget, where it is.
	Budget string `json:"budget,omitempty"`
	// DeliveredBefore is a delivery that no longer counts, because the epic opened
	// again afterwards.
	DeliveredBefore *time.Time `json:"delivered_before,omitempty"`
	// Cost is what this naming of the epic has cost beside its limit, and
	// CostProblem why that could not be read.
	Cost        *epiccost.Standing `json:"cost,omitempty"`
	CostProblem string             `json:"cost_problem,omitempty"`
	// epic is the epic's identifier, which the cost line names its command with.
	epic string
	// TakenOut is each item the operator took out of a delivered epic, with the
	// operator's words, shown beside the delivery.
	TakenOut []EpicTakenOut `json:"taken_out,omitempty"`
}

// EpicTakenOut is one item the operator took out and has not put back.
type EpicTakenOut struct {
	WorkItemID string    `json:"work_item_id"`
	At         time.Time `json:"at"`
	Words      string    `json:"words"`
}

// takenOut is each item the operator took out of the epic and has not put back,
// with the last removal's words.
func takenOut(epic epicscope.Epic) []EpicTakenOut {
	var out []EpicTakenOut
	for _, removal := range slices.Backward(epic.Removed) {
		if epic.TakenOut(removal.WorkItemID) && !slices.ContainsFunc(out, func(kept EpicTakenOut) bool { return kept.WorkItemID == removal.WorkItemID }) {
			out = append(out, EpicTakenOut{WorkItemID: removal.WorkItemID, At: removal.RemovedAt, Words: removal.Words})
		}
	}
	slices.Reverse(out)
	return out
}

func (s EpicStanding) delivered() bool {
	return s.State == EpicIntegrated || s.State == EpicHandedOver
}

// EndedEpic is an epic the operator ended.
type EndedEpic struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title"`
	EpicStanding
}

func (s EpicStanding) render() string {
	var rendered strings.Builder
	if s.State == "" {
		fmt.Fprintf(&rendered, "%swhere it stands could not be worked out: %s\n", partialRead, s.Problem)
	} else {
		fmt.Fprintf(&rendered, "    %s", epicStateSays[s.State])
		if s.Budget != "" {
			fmt.Fprintf(&rendered, "; the next start is refused by its budget: %s", s.Budget)
		}
		rendered.WriteString("\n")
	}
	if s.DeliveredBefore != nil {
		fmt.Fprintf(&rendered, "    It was delivered at %s and opened again since.\n", localMoment(*s.DeliveredBefore))
	}
	if s.delivered() {
		for _, out := range s.TakenOut {
			fmt.Fprintf(&rendered, "    Delivered without %s, which you took out at %s: %s\n", out.WorkItemID, localMoment(out.At), out.Words)
		}
	}
	switch {
	case s.Cost != nil:
		fmt.Fprintf(&rendered, "    %s\n", s.Cost.Says(s.epic))
	case s.CostProblem != "":
		fmt.Fprintf(&rendered, "%swhat it has cost could not be read: %s\n", partialRead, s.CostProblem)
	}
	for _, wait := range s.Waits {
		if wait.Owner == MoverNobody {
			fmt.Fprintf(&rendered, "    %s\n", wait.Says)
		} else {
			fmt.Fprintf(&rendered, "    %s: %s\n", wait.Owner.WaitingOn(), wait.Says)
		}
	}
	return rendered.String()
}

// EpicBudgets is the epic's budget refusing its next start, as why, and empty
// where it does not. It is an optional capability of EpicRecords, which a test
// gives in place of EpicCosts.
type EpicBudgets interface {
	RefusesStart(naming epicprogress.Naming) (string, error)
}

// EpicCosts is what a naming of an epic has cost beside its limit. It is an
// optional capability of EpicRecords, satisfied by the records the harness
// wires; the budget's refusal of a start is then read from it.
type EpicCosts interface {
	Cost(view epicprogress.View, epic epicscope.Epic) (epiccost.Standing, error)
}

// epicStandings is what the states are worked out from.
type epicStandings struct {
	epicFacts
	runs       itemRuns
	recorded   map[string][]runstate.State
	groups     map[string]WaitGroup
	held       map[string]Refused
	capacity   map[string]bool
	paused     map[string]bool
	needs      []Attention
	exchanges  []exchange.Exchange
	directives []directive.Directive
	budgets    EpicBudgets
	costs      EpicCosts
	unfinished map[string]struct{}
	problem    string
	// conversations is read for the questions put to the operator, and asked is
	// what was read.
	conversations Conversations
	asked         []operatorQuestion
	askedRead     bool
	// needsProblem is why a question may be missing from needs, so that an epic is
	// not shown as delivered.
	needsProblem string
}

func newEpicStandings(facts epicFacts, sources Sources, standing Standing, needs []Attention, needsProblem, problem string, progressErr error) *epicStandings {
	s := &epicStandings{epicFacts: facts, needs: needs, needsProblem: needsProblem, conversations: sources.Conversations, problem: problem, recorded: map[string][]runstate.State{}, groups: map[string]WaitGroup{},
		held: map[string]Refused{}, capacity: map[string]bool{}, paused: map[string]bool{}, unfinished: map[string]struct{}{}}
	s.runs, _ = sources.Runs.(itemRuns)
	s.budgets, _ = sources.Epics.(EpicBudgets)
	s.costs, _ = sources.Epics.(EpicCosts)
	for _, err := range []error{progressErr, s.readExchanges(sources), s.readDirectives(sources)} {
		if err != nil {
			s.problem = joinProblems(s.problem, singleLine(err.Error(), maxEpicReasonBytes))
		}
	}
	for id := range facts.items {
		if !facts.done(id) {
			s.unfinished[id] = struct{}{}
		}
	}
	for _, group := range standing.NotStartableGroups {
		for _, item := range group.Items {
			s.groups[item.WorkItemID] = group
		}
	}
	for _, refused := range slices.Concat(standing.AlsoHeld, standing.NotStartable) {
		if refused.Kind == backlog.HeldForAPerson {
			s.held[refused.WorkItemID] = refused
		}
	}
	for _, run := range standing.CapacityBlocked.Runs {
		s.capacity[run.WorkItemID] = s.capacity[run.WorkItemID] || run.State == CapacityStateWaiting
	}
	for _, run := range standing.PausedRuns {
		s.paused[run.WorkItemID] = true
	}
	return s
}

func (s *epicStandings) readExchanges(sources Sources) error {
	if sources.Exchanges == nil {
		return nil
	}
	listed, err := sources.Exchanges.List()
	if err != nil {
		return fmt.Errorf("the questions between roles could not be read: %w", err)
	}
	s.exchanges = listed
	return nil
}

func (s *epicStandings) readDirectives(sources Sources) (err error) {
	if sources.Directives == nil {
		return nil
	}
	if s.directives, err = sources.Directives.List(); err != nil {
		return fmt.Errorf("what the operator has directed could not be read: %w", err)
	}
	return nil
}

// runsOf is an item's runs, newest first.
func (s *epicStandings) runsOf(id string) ([]runstate.State, error) {
	if known, read := s.recorded[id]; read {
		return known, nil
	}
	if s.runs == nil {
		return nil, errors.New("nothing was wired to read one item's runs")
	}
	runs, err := s.runs.Runs(id)
	if err != nil {
		return nil, fmt.Errorf("the runs of %s could not be read: %w", id, err)
	}
	runs = slices.Clone(runs)
	slices.SortStableFunc(runs, func(a, b runstate.State) int { return b.StartedAt.Compare(a.StartedAt) })
	s.recorded[id] = runs
	return runs, nil
}

// epicMember is where one item of an epic stands. One that is none of these and
// waits for nothing is held only by another item of the epic.
type epicMember struct {
	done, handed, underway, refusedByBudget bool
	waits                                   []EpicWait
	// heads is each approved head handed over for a person to merge.
	heads []string
}

// of is where an epic stands, with what it has cost.
func (s *epicStandings) of(epic epicscope.Epic) EpicStanding {
	var cost *epiccost.Standing
	var costProblem string
	if s.costs != nil && s.view.Unreadable(epicprogress.Of(epic)) == "" {
		read, err := s.costs.Cost(s.view, epic)
		if err != nil {
			costProblem = singleLine(err.Error(), maxEpicReasonBytes)
		} else if read.Budget != nil || read.Runs > 0 || read.Calls > 0 || read.CheckRuns > 0 {
			// An epic with no limit that nothing was spent on has no cost to say.
			cost = &read
		}
	}
	standing := s.stateOf(epic, cost, costProblem)
	standing.Cost, standing.CostProblem, standing.epic = cost, costProblem, epic.WorkItemID
	return standing
}

func (s *epicStandings) stateOf(epic epicscope.Epic, cost *epiccost.Standing, costProblem string) EpicStanding {
	var standing EpicStanding
	naming := epicprogress.Of(epic)
	entry, _ := s.view.Entry(naming)
	fail := func(why string) EpicStanding { return EpicStanding{Problem: singleLine(why, maxEpicReasonBytes)} }
	if why := s.view.Unreadable(naming); why != "" {
		return fail("its progress cannot be read: " + why)
	}
	if s.problem != "" {
		return fail(s.problem)
	}
	if epic.Open() && s.budgets != nil {
		why, err := s.budgets.RefusesStart(naming)
		if err != nil {
			return fail(fmt.Sprintf("its budget could not be read: %v", err))
		}
		standing.Budget = why
	} else if epic.Open() && s.costs != nil && epic.Budget != nil {
		if cost == nil {
			return fail("what it has cost, beside its budget, could not be read: " + costProblem)
		}
		standing.Budget = cost.Why()
	}
	if epic.Ended != nil && len(entry.Deliveries) == 0 {
		return EpicStanding{State: EpicCancelled}
	}
	opened, err := s.openedAgain(epic, entry)
	if err != nil {
		return fail(err.Error())
	}
	if epic.Ended != nil {
		state, err := s.ended(epic, entry, opened)
		if err != nil {
			return fail(err.Error())
		}
		ended := EpicStanding{State: state}
		if ended.delivered() {
			ended.TakenOut = takenOut(epic)
		}
		return ended
	}
	for _, delivery := range entry.Deliveries {
		if !delivery.At.After(opened) {
			at := delivery.At
			standing.DeliveredBefore = &at
		}
	}
	allDone, handed, underway, refused := true, false, false, false
	var heads []string
	for _, id := range s.view.Items(epic) {
		member, err := s.member(epic, entry, id, standing.Budget != "", map[string]bool{})
		if err != nil {
			return fail(err.Error())
		}
		questions := s.questions(id)
		allDone = allDone && member.done && len(questions) == 0
		handed = handed || member.handed
		heads = append(heads, member.heads...)
		underway = underway || member.underway
		refused = refused || member.refusedByBudget
		if !member.done {
			member.waits = append(member.waits, s.otherHolds(id)...)
		}
		for _, wait := range questions {
			wait.WorkItemID = id
			standing.Waits = append(standing.Waits, wait)
		}
		for _, wait := range member.waits {
			wait.WorkItemID = id
			if !slices.ContainsFunc(standing.Waits, func(other EpicWait) bool {
				return other.WorkItemID == id && other.Owner == wait.Owner && (other.Says == wait.Says || wait.step != "" && other.step == wait.step)
			}) {
				standing.Waits = append(standing.Waits, wait)
			}
		}
	}
	if waits := s.piecesAsked(epic, entry); len(waits) > 0 {
		allDone = false
		standing.Waits = append(standing.Waits, waits...)
	}
	if waits := commentsUnknown(entry); len(waits) > 0 {
		allDone = false
		standing.Waits = append(standing.Waits, waits...)
	}
	delivered := EpicIntegrated
	if handed {
		delivered = EpicHandedOver
	}
	if allDone {
		waits := s.checkOwed(epic, entry, opened, heads)
		if len(waits) == 0 && s.needsProblem != "" {
			return fail("whether a question about it is open could not be read: " + s.needsProblem)
		}
		if len(waits) == 0 {
			standing.State, standing.Budget = delivered, ""
			standing.TakenOut = takenOut(epic)
			return standing
		}
		standing.Waits = append(standing.Waits, waits...)
	}
	if len(standing.Waits) == 0 && !underway && !refused {
		standing.Waits = append(standing.Waits, EpicWait{Owner: MoverDevelopmentManager, Says: "its members wait only for each other, so none can start: decide what to change"})
	}
	working := underway || slices.ContainsFunc(standing.Waits, func(wait EpicWait) bool { return !wait.outside() && !wait.Owner.IsOperator() })
	switch {
	case working:
		standing.State = EpicWorking
	case refused:
		standing.State = EpicOutOfBudget
	case slices.ContainsFunc(standing.Waits, EpicWait.outside):
		// What also waits on the operator is named with it.
		standing.State = EpicBlockedOutside
	default:
		standing.State = EpicWaitsOnOperator
	}
	if standing.State != EpicOutOfBudget {
		standing.Budget = ""
	}
	return standing
}

// ended is where an epic the operator ended stood when it was ended, which nothing
// recorded later changes. It had been delivered where
// its delivery still counted then, every member's change was landed or handed
// over, and no question was open.
func (s *epicStandings) ended(epic epicscope.Epic, entry epicprogress.Entry, opened time.Time) (EpicState, error) {
	at := epic.Ended.At
	counts := func(recorded time.Time) bool { return recorded.After(opened) && !recorded.After(at) }
	if !slices.ContainsFunc(entry.Checks, func(check epicprogress.Check) bool { return check.Passed() && counts(check.At) }) ||
		!slices.ContainsFunc(entry.Deliveries, func(delivery epicprogress.Delivery) bool { return counts(delivery.At) }) {
		return EpicCancelled, nil
	}
	state := EpicIntegrated
	for _, id := range s.view.Items(epic) {
		done, handed, err := s.doneAt(epic, entry, id, at, map[string]bool{})
		if err != nil {
			return "", err
		}
		if !done || s.questionOpenAt(id, at) {
			return EpicCancelled, nil
		}
		if handed {
			state = EpicHandedOver
		}
	}
	var delivered time.Time
	for _, delivery := range entry.Deliveries {
		if counts(delivery.At) && delivery.At.After(delivered) {
			delivered = delivery.At
		}
	}
	// The logs are long, so they are read only for what is otherwise delivered.
	asked := s.operatorQuestions()
	for _, id := range s.view.Items(epic) {
		if slices.ContainsFunc(asked, func(question operatorQuestion) bool {
			return namesItem(id, question.texts...) && question.openAt(at, delivered)
		}) {
			return EpicCancelled, nil
		}
	}
	return state, nil
}

// ConcernEvents is when each question to the operator was raised and answered,
// which the saved conversation does not hold. It is an optional capability of
// Conversations, satisfied by *runstate.ConversationStore.
type ConcernEvents interface {
	// LoggedConversations is every conversation with a log, those replaced since
	// included.
	LoggedConversations() ([]string, error)
	EventsOfType(conversationID string, types ...execution.EventType) ([]execution.Event, []runstate.SkippedLine, error)
}

// operatorQuestion is one question a role put to the operator.
type operatorQuestion struct {
	texts    []string
	raised   time.Time
	answered *time.Time
	// pending is that the saved conversation still holds it.
	pending bool
}

// openAt reports a question open at a moment. One that was never answered and
// that no saved conversation holds was dropped at a time nothing records. An epic
// is delivered only with none open, so one raised before the delivery was dropped
// by then.
func (q operatorQuestion) openAt(at, delivered time.Time) bool {
	if q.raised.After(at) {
		return false
	}
	if q.answered != nil {
		return q.answered.After(at)
	}
	return q.pending || q.raised.After(delivered)
}

// operatorQuestions is every question put to the operator whose time the logs
// give, read once for a reading. What cannot be read is left out, because nothing
// shows that it stood when an epic was ended.
func (s *epicStandings) operatorQuestions() []operatorQuestion {
	if !s.askedRead {
		s.asked, s.askedRead = readOperatorQuestions(s.conversations), true
	}
	return s.asked
}

func readOperatorQuestions(conversations Conversations) (asked []operatorQuestion) {
	events, can := conversations.(ConcernEvents)
	if !can {
		return nil
	}
	pending := map[string]bool{}
	recorded, _ := conversations.Recorded()
	for _, conversation := range recorded {
		for _, concern := range conversation.PendingConcerns {
			pending[concernWaitID(conversation.ConversationID, concern.ID)] = true
		}
	}
	logged, _ := events.LoggedConversations()
	for _, conversation := range logged {
		found, _, err := events.EventsOfType(conversation, execution.EventConcernRaised, execution.EventConcernAnswered)
		if err != nil {
			continue
		}
		at := map[string]int{}
		for _, event := range found {
			// chat.PendingConcern, which this package cannot import.
			var concern struct {
				ID      string `json:"id"`
				Concern struct {
					Subject  string `json:"subject"`
					Detail   string `json:"detail"`
					Question string `json:"question"`
				} `json:"concern"`
			}
			if err := json.Unmarshal(event.Payload, &concern); err != nil || concern.ID == "" {
				continue
			}
			index, known := at[concern.ID]
			switch {
			case event.Type == execution.EventConcernRaised:
				at[concern.ID] = len(asked)
				asked = append(asked, operatorQuestion{texts: []string{concern.Concern.Subject, concern.Concern.Detail, concern.Concern.Question}, raised: event.Timestamp,
					pending: pending[concernWaitID(conversation, concern.ID)]})
			case known && asked[index].answered == nil:
				answered := event.Timestamp
				asked[index].answered = &answered
			}
		}
	}
	return asked
}

// doneAt is whether a member of an epic ended at a moment was done then, and
// whether its change was not merged yet. Runs started later are not looked at, and
// what was recorded of the others later is not either.
func (s *epicStandings) doneAt(epic epicscope.Epic, entry epicprogress.Entry, id string, at time.Time, seen map[string]bool) (done, handed bool, err error) {
	runs, err := s.runsOf(id)
	if err != nil {
		return false, false, err
	}
	if s.items[id].Landing != "" && !s.items[id].ClosedAt.After(at) {
		return true, false, nil
	}
	runs = slices.DeleteFunc(slices.Clone(runs), func(run runstate.State) bool { return run.StartedAt.After(at) })
	for _, run := range runs {
		if !run.Discharges() {
			continue
		}
		for steps := 0; run.PersonMerge != nil && run.PersonMerge.Superseded() && !run.PersonMerge.SupersededAt.After(at) && steps < len(runs); steps++ {
			next := slices.IndexFunc(runs, func(other runstate.State) bool { return other.RunID == run.PersonMerge.SupersededBy })
			if next < 0 {
				break
			}
			run = runs[next]
		}
		// What a retirement recorded later ended was still so at the moment.
		standing := run.Retirement != nil && run.Retirement.At.After(at) && run.Retirement.PriorStatus == runstate.StatusSucceeded
		closed := closedUnmerged(run)
		switch {
		case closed != nil && !closed.After(at):
			continue
		case run.TeamMerge != nil && run.TeamMerge.PullRequest == nil && (run.TeamMerge.FoundAt == nil || run.TeamMerge.FoundAt.After(at)):
			continue
		case closed != nil, run.WaitingForPersonMerge(), run.BringingPersonMergeUpToDate(),
			run.PersonMerge != nil && (run.PersonMerge.Superseded() || run.PersonMerge.Merged() && run.PersonMerge.MergedAt.After(at)),
			standing && run.PersonMerge != nil && run.PersonMerge.Waiting() && run.Integration == nil && run.PullRequest != nil,
			run.TeamMerge != nil && (run.TeamMerge.FoundAt == nil || run.TeamMerge.FoundAt.After(at)):
			done, handed = true, true
		case landed(run), approvedSummary(run),
			standing && run.Integration != nil && (run.PullRequest == nil || run.PullRequest.Merged):
			return true, false, nil
		}
	}
	if done {
		return done, handed, nil
	}
	pieces := 0
	for _, piece := range s.pieces(epic, entry, id, runs, seen) {
		pieceDone, pieceHanded, err := s.doneAt(epic, entry, piece, at, seen)
		if err != nil || !pieceDone {
			return false, false, err
		}
		pieces++
		handed = handed || pieceHanded
	}
	return pieces > 0, handed, nil
}

// questionOpenAt reports a question between roles, or a directive, that names an
// item and was not settled at a moment.
func (s *epicStandings) questionOpenAt(id string, at time.Time) bool {
	open := func(since time.Time, settled ...*time.Time) bool {
		return !since.After(at) && !slices.ContainsFunc(settled, func(end *time.Time) bool { return end != nil && !end.After(at) })
	}
	return slices.ContainsFunc(s.exchanges, func(asked exchange.Exchange) bool {
		return open(asked.OpenedAt, asked.ClosedAt) && namesItem(id, questionsOf(asked)...)
	}) || slices.ContainsFunc(s.directives, func(said directive.Directive) bool {
		return said.Kind.Pauses() && len(said.Scope) > 0 && said.Affects(id) && open(said.ReceivedAt, said.ResolvedAt, said.WithdrawnAt, said.BecameAt)
	})
}

// openedAgain is when an epic was last opened to more work: a member was added or
// joined, or a person closed one of its pull requests unmerged while it was in
// the epic. A check or a delivery from before then no longer counts.
func (s *epicStandings) openedAgain(epic epicscope.Epic, entry epicprogress.Entry) (time.Time, error) {
	var latest time.Time
	until := func(at, end time.Time) {
		if at.After(latest) && (end.IsZero() || !at.After(end)) {
			latest = at
		}
	}
	var ended time.Time
	if epic.Ended != nil {
		ended = epic.Ended.At
	}
	in := map[string]time.Time{}
	for _, removal := range epic.Removed {
		in[removal.WorkItemID] = removal.RemovedAt
		if removal.AddedAt != nil {
			until(*removal.AddedAt, ended)
		}
	}
	for _, id := range s.view.Items(epic) {
		in[id] = ended
	}
	for _, member := range epic.Members {
		until(member.AddedAt, ended)
	}
	for _, joined := range entry.Joined {
		until(joined.At, ended)
	}
	for id, left := range in {
		runs, err := s.runsOf(id)
		if err != nil {
			return time.Time{}, err
		}
		for _, run := range runs {
			if closed := closedUnmerged(run); closed != nil && closed.After(epic.NamedAt) {
				until(*closed, left)
			}
		}
	}
	return latest, nil
}

// closedUnmerged is when a person closed a run's pull request without merging it.
func closedUnmerged(run runstate.State) *time.Time {
	if run.PersonMerge != nil && run.PersonMerge.Returned() {
		return run.PersonMerge.ClosedAt
	}
	if run.TeamMerge != nil && run.TeamMerge.Returned != nil {
		return &run.TeamMerge.Returned.ClosedAt
	}
	return nil
}

// checkOwed is what the check of the whole epic still needs, and nothing where it
// passed. One that did not pass is made again after a removal, and is then not a
// round of correction; so is one that passed and is made again because a change
// handed over was repaired since, on the new approved heads (Entry.Repaired). Each step the check found the description reserves for a
// person waits on the operator beside whatever else the check owes, so a
// question to the Lead Product Manager or a correction never hides it.
func (s *epicStandings) checkOwed(epic epicscope.Epic, entry epicprogress.Entry, opened time.Time, heads []string) []EpicWait {
	last := s.lastCheck(epic, entry, opened)
	if repaired := last != nil && last.Passed() && entry.Repaired(epic, heads); last == nil || repaired {
		done, again := "every member is done and", ""
		if repaired {
			done, again = "a change handed over was repaired after the check of the whole epic passed, so", " again, on the new approved heads, before the epic is delivered again"
		}
		if id, underway := entry.CheckUnderway(); underway {
			return []EpicWait{{Owner: MoverOf(domain.RoleReviewer), Says: fmt.Sprintf("%s the check of the whole epic is under way%s, as %s", done, again, id), CheckUnderway: true}}
		}
		return []EpicWait{{Owner: MoverOf(domain.RoleReviewer), Says: done + " the check of the whole epic is owed" + again, CheckOwed: true}}
	}
	if last.Passed() {
		return nil
	}
	waits := personsSteps(*last)
	if rest := checkRest(epic, entry, *last); rest != nil {
		waits = append(waits, *rest)
	}
	return waits
}

// personsSteps is a wait on the operator for each step a check found the
// description reserves for a person, naming the step and what the check said of
// it. A check recorded without its answers that found only such a step still
// waits on him, for a step it cannot name.
func personsSteps(check epicprogress.Check) []EpicWait {
	owner := humanGateAttention("", HumanGateWait{}).Ownership.Owner
	const says = "the epic's description reserves a step for a person, and it has not been taken"
	var waits []EpicWait
	for _, step := range epicprogress.PersonsSteps(check) {
		waits = append(waits, EpicWait{Owner: owner, Says: fmt.Sprintf("%s: %s (%s)", says, singleLine(step.Asks, maxEpicReasonBytes), singleLine(step.Says, maxEpicReasonBytes))})
	}
	if len(waits) == 0 && check.Found == epicprogress.FoundPersonsStep {
		waits = append(waits, EpicWait{Owner: owner, Says: says})
	}
	return waits
}

// lastCheck is the check of the whole epic that counts, and nil where none has
// been made since the epic last opened to more work.
func (s *epicStandings) lastCheck(epic epicscope.Epic, entry epicprogress.Entry, opened time.Time) *epicprogress.Check {
	var last *epicprogress.Check
	for _, check := range entry.Checks {
		if entry.Superseded(epic, check) {
			continue
		}
		if check.At.After(opened) && (check.Passed() || !slices.ContainsFunc(epic.Removed, func(r epicscope.Removal) bool { return r.RemovedAt.After(check.At) })) {
			last = &check
		}
	}
	return last
}

// checkRest is what a check that did not pass owes besides the steps reserved for
// a person, and nil where it owes nothing else.
func checkRest(epic epicscope.Epic, entry epicprogress.Entry, last epicprogress.Check) *EpicWait {
	switch {
	case last.Owed != nil:
		return correctionOwed(entry, last)
	case last.Found == epicprogress.FoundNotMet && entry.Rounds(epic) <= epicprogress.MaxRounds:
		return &EpicWait{Owner: MoverDevelopmentManager, Says: "write the piece that corrects what the check of the whole epic found missing"}
	case last.Found == epicprogress.FoundNotMet:
		return &EpicWait{Owner: MoverProductManager, Says: "the check of the whole epic found something missing a third time, after two rounds of correction, so no correction starts by itself: decide on one more round or ask about scope"}
	case last.Found == epicprogress.FoundPersonsStep:
		return nil
	default:
		return evidenceAsked(last)
	}
}

// evidenceAsked is who owes the next step of a check that could not tell and owes
// no correction yet. The Lead Product Manager answers the question the harness
// files her by handling it, and until her handling is recorded the step is hers.
// A handling that recorded that only the operator can act makes the epic wait on
// him, as the ownership registry resolves it.
func evidenceAsked(check epicprogress.Check) *EpicWait {
	asked := check.Asked
	switch {
	case asked == nil:
		return &EpicWait{Owner: MoverProductManager, Says: "say what evidence the check of the whole epic lacked, for the development manager to have written; the harness is filing her the question"}
	case asked.Operator != "":
		answer := resolved(Attention{Kind: AttentionOperatorAction, OperatorAction: &OperatorAction{Needs: asked.Says, Reason: asked.Operator, ReportID: asked.Report}}).Ownership
		return &EpicWait{Owner: answer.Owner, Says: fmt.Sprintf("the check %s of the whole epic could not tell, and the answer to %s says only the operator can act (%s): %s", check.ID, asked.Report, asked.Operator, singleLine(asked.Says, maxEpicReasonBytes))}
	}
	return &EpicWait{Owner: MoverProductManager, Says: fmt.Sprintf("say what evidence the check of the whole epic lacked, for the development manager to have written, by handling report %s", asked.Report)}
}

// correctionOwed is who owes the next step of the correction a check owes: the
// development manager writes the piece, a reviewer session judges whether it
// joins, and a piece judged anything but inside is a question about scope.
func correctionOwed(entry epicprogress.Entry, check epicprogress.Check) *EpicWait {
	owed := check.Owed
	if owed.Piece == "" {
		return &EpicWait{Owner: MoverDevelopmentManager, Says: fmt.Sprintf("write the piece that corrects the check %s of the whole epic, naming that check: %s", check.ID, owed.Missing)}
	}
	if piece, asked := entry.Asked(owed.Piece); asked && piece.Outcome != nil && piece.Outcome.Answer != epicprogress.AnswerInside {
		return &EpicWait{Owner: MoverProductManager, Says: fmt.Sprintf("the correction owed by the check %s waits on the question about scope its piece %s is", check.ID, owed.Piece)}
	}
	return &EpicWait{Owner: MoverOf(domain.RoleReviewer), Says: fmt.Sprintf("judge whether the correcting piece %s joins the epic", owed.Piece)}
}

// member is where one item stands.
func (s *epicStandings) member(epic epicscope.Epic, entry epicprogress.Entry, id string, budgetRefuses bool, seen map[string]bool) (epicMember, error) {
	waits := func(owner Mover, format string, args ...any) (epicMember, error) {
		return epicMember{waits: []EpicWait{s.wait(id, owner, "", format, args...)}}, nil
	}
	stopped := func(owner Mover) (epicMember, error) {
		return epicMember{waits: []EpicWait{s.stoppage(id, owner)}}, nil
	}
	if s.running[id] {
		if s.capacity[id] {
			return waits(MoverProvider, "has a run that waits for the provider")
		}
		// A repair answering a change request on its pull request is named, so a
		// handed over epic shows what it is Working on. Runs that cannot be read
		// leave it named only as under way.
		runs, _ := s.runsOf(id)
		if at := slices.IndexFunc(runs, func(run runstate.State) bool { return run.Status == runstate.StatusRunning && run.ForgeFeedback != nil }); at >= 0 {
			return epicMember{underway: true, waits: []EpicWait{s.wait(id, MoverHarness, "", "has a repair under way answering the changes asked for on pull request #%d", runs[at].ForgeFeedback.PullRequest)}}, nil
		}
		return epicMember{underway: true}, nil
	}
	runs, err := s.runsOf(id)
	if err != nil {
		return epicMember{}, err
	}
	for _, run := range runs {
		if run.Discharges() && (run.WaitingForPersonMerge() || run.WaitingForTeamMerge() && run.TeamMerge.PullRequest != nil) {
			handed := epicMember{done: true, handed: true}
			if run.WaitingForPersonMerge() {
				handed.heads = []string{run.PullRequest.HeadCommit}
			}
			return handed, nil
		}
	}
	returned := len(runs) > 0 && closedUnmerged(runs[0]) != nil
	if s.done(id) || s.gone(id) {
		if s.items[id].Landing != "" || slices.ContainsFunc(runs, func(run runstate.State) bool { return landed(run) || approvedSummary(run) }) {
			return epicMember{done: true}, nil
		}
		if slices.ContainsFunc(runs, runstate.State.AwaitingForge) {
			return waits(MoverForge, "has a change the forge has not merged yet")
		}
		return s.replaced(epic, entry, id, returned, budgetRefuses, seen)
	}
	group, grouped := s.groups[id]
	if s.ready[id] {
		stalled := grouped && group.Kind == backlog.HeldByStall
		switch {
		case budgetRefuses:
			return epicMember{refusedByBudget: true}, nil
		case s.view.TextWait(s.items[id]) != "":
			// A piece whose text no judgment found inside starts nothing, and the
			// step it waits on is named with the pieces asked about (piecesAsked).
			return epicMember{}, nil
		case stalled && (group.Mover.IsOperator() || EpicWait{Owner: group.Mover}.outside()):
			return waits(group.Mover, "is ready, and nothing starts: %s", group.WaitsOn)
		}
		return epicMember{underway: true}, nil
	}
	if refused, held := s.held[id]; held && (refused.MergeQueued || refused.AwaitingNamedEpics) {
		return epicMember{underway: true}, nil
	} else if held && refused.AwaitingUnparking != "" {
		return epicMember{waits: []EpicWait{s.wait(id, MoverProductManager, "parking", "has a decision that waits for it to be taken out of parking")}}, nil
	}
	if grouped {
		if returned && group.Kind == backlog.HeldParked {
			return s.replaced(epic, entry, id, true, budgetRefuses, seen)
		}
		switch {
		case group.Kind == backlog.HeldForAPerson && group.Awaiting == HeldAwaitingDecision:
			return stopped(group.Mover)
		case group.Kind == backlog.HeldForAPerson && group.Awaiting == HeldAwaitingWork:
			return waits(MoverNobody, "waits for work the development manager decided to wait for")
		case group.Kind == backlog.HeldWaitingOn || group.Kind == backlog.HeldInherited || group.Kind == backlog.HeldCovered:
			return s.waitsOnItems(epic, id), nil
		case group.Mover == MoverHarness:
			return epicMember{underway: true}, nil
		}
		return epicMember{waits: []EpicWait{s.wait(id, group.Mover, epicHoldSteps[group.Kind], "%s", epicHoldSays[group.Kind])}}, nil
	}
	switch {
	case s.paused[id]:
		return s.waitsOnItems(epic, id), nil
	case slices.ContainsFunc(runs, runstate.State.AwaitingForge):
		return waits(MoverForge, "has a change the forge has not merged yet")
	case slices.ContainsFunc(runs, runstate.State.WaitingForTeamMerge):
		return waits(MoverNobody, "has landed and waits for the team to merge it")
	case slices.ContainsFunc(runs, runstate.State.WaitingForPersonMerge):
		return waits(MoverNobody, "waits for a person to merge its pull request, which does not finish it")
	}
	if _, held := s.held[id]; held {
		return stopped(MoverDevelopmentManager)
	}
	return waits(MoverDevelopmentManager, "is neither running, ready nor held: find out why")
}

// epicHoldSays is what each other kind of hold leaves to do.
var epicHoldSays = map[backlog.HoldKind]string{
	backlog.HeldForAGate:       "needs a step only a person can take",
	backlog.HeldParked:         "is parked: decide what becomes of it",
	backlog.HeldForDesign:      "waits for its design to be written",
	backlog.HeldByDirective:    "is paused by a directive that is not settled",
	backlog.HeldByConversation: "is to be carried out in that role's conversation",
	backlog.HeldUnread:         "is not offered by the tracker: find out why",
}

// epicHoldSteps is the step each kind of hold that can also be told of another
// way is.
var epicHoldSteps = map[backlog.HoldKind]string{
	backlog.HeldForAGate:    "person",
	backlog.HeldParked:      "parking",
	backlog.HeldForDesign:   "design",
	backlog.HeldByDirective: "directive",
}

func (s *epicStandings) wait(id string, owner Mover, step, format string, args ...any) EpicWait {
	return EpicWait{Owner: owner, step: step, Says: s.name(id, "") + " " + fmt.Sprintf(format, args...)}
}

func (s *epicStandings) stoppage(id string, owner Mover) EpicWait {
	return s.wait(id, s.stoppageOwner(id, owner), "stoppage", "has a stopped run to decide about")
}

// otherHolds is each hold on a member beside the one reason the queue gives for
// not offering it.
func (s *epicStandings) otherHolds(id string) []EpicWait {
	group, grouped := s.groups[id]
	if !grouped || s.running[id] || s.ready[id] {
		return nil
	}
	var waits []EpicWait
	refused, held := s.held[id]
	if held && group.Kind != backlog.HeldForAPerson && !refused.MergeQueued && !refused.AwaitingNamedEpics && refused.AwaitingUnparking == "" {
		waits = append(waits, s.stoppage(id, MoverDevelopmentManager))
	}
	item := s.items[id]
	// One parked because its pull request was closed is said as that.
	runs := s.recorded[id]
	if item.Parking.Parked() && (len(runs) == 0 || closedUnmerged(runs[0]) == nil) {
		waits = append(waits, s.wait(id, MoverProductManager, "parking", "%s", epicHoldSays[backlog.HeldParked]))
	}
	if slices.Contains(item.Labels, domain.NeedsDesignLabel) {
		waits = append(waits, s.wait(id, MoverArchitect, "design", "%s", epicHoldSays[backlog.HeldForDesign]))
	}
	return waits
}

// landed reports a run whose change is on its target branch and is the item's work.
func landed(run runstate.State) bool {
	return run.Discharges() && (run.RecordedAsMerged() || run.PersonMerge != nil && run.PersonMerge.Merged())
}

// approvedSummary reports a run that ended complete with no change to land, whose
// summary the reviewer approved as the item's work.
func approvedSummary(run runstate.State) bool {
	return run.Status == runstate.StatusSucceeded && run.ReviewDecision == runstate.ReviewApprove && run.Discharges() &&
		run.Integration == nil && run.PullRequest == nil && run.PersonMerge == nil && run.Retirement == nil &&
		(run.Changes == nil || run.Changes.Files == "")
}

// replaced is a member that was closed with nothing landed. It counts as done
// once every piece written in its place is, and is otherwise decided by the
// Lead Product Manager.
func (s *epicStandings) replaced(epic epicscope.Epic, entry epicprogress.Entry, id string, returned, budgetRefuses bool, seen map[string]bool) (epicMember, error) {
	runs, err := s.runsOf(id)
	if err != nil {
		return epicMember{}, err
	}
	whole, pieces := epicMember{done: true}, 0
	for _, piece := range s.pieces(epic, entry, id, runs, seen) {
		stands, err := s.member(epic, entry, piece, budgetRefuses, seen)
		if err != nil {
			return epicMember{}, err
		}
		pieces++
		whole.done, whole.handed = whole.done && stands.done, whole.handed || stands.handed
		whole.heads = append(whole.heads, stands.heads...)
	}
	if pieces > 0 {
		// What the pieces wait for is said of them.
		return whole, nil
	}
	why := "was closed without its change having landed"
	if returned {
		why = "had its pull request closed unmerged by a person"
	}
	return epicMember{waits: []EpicWait{{Owner: MoverProductManager, Says: fmt.Sprintf(
		"%s %s: admit it again, have it replaced, or ask the operator to take it out (yoyo epic remove %s %s --words <text>)", s.name(id, ""), why, epic.WorkItemID, id)}}}, nil
}

// pieces is each piece in the epic that stands for a member: one that came from
// it, and, where a person closed its pull request, that joined after that. Each
// member is taken once.
func (s *epicStandings) pieces(epic epicscope.Epic, entry epicprogress.Entry, id string, runs []runstate.State, seen map[string]bool) []string {
	seen[id] = true
	var since time.Time
	if len(runs) > 0 {
		if closed := closedUnmerged(runs[0]); closed != nil {
			since = *closed
		}
	}
	items := s.view.Items(epic)
	var pieces []string
	for _, piece := range entry.Joined {
		if piece.From == id && !seen[piece.WorkItemID] && slices.Contains(items, piece.WorkItemID) && piece.At.After(since) {
			pieces = append(pieces, piece.WorkItemID)
		}
	}
	return pieces
}

// piecesAsked is each piece that may join and has not: one still to be judged, and
// one that is a question about scope. A piece that is closed, is in an open
// epic, or joined and was taken out asks nothing more.
func (s *epicStandings) piecesAsked(epic epicscope.Epic, entry epicprogress.Entry) []EpicWait {
	var waits []EpicWait
	for _, piece := range entry.Pieces {
		id := piece.WorkItemID
		if s.done(id) || s.gone(id) || epic.TakenOut(id) {
			continue
		}
		// A piece that joined starts no developer on a text no judgment found inside.
		if item, read := s.items[id]; read && entry.Has(id) && !epic.Has(id) {
			if why := s.view.TextWait(item); why != "" {
				owner := MoverOf(domain.RoleReviewer)
				if answer, judged := piece.JudgedAs(epicprogress.TextDigest(item)); judged && answer.Asks() {
					owner = MoverProductManager
				}
				says := "starts no developer: " + why
				if answered, said, ok := s.scopeStep(epic, piece); ok {
					owner, says = answered, "starts no developer: it "+said
				}
				waits = append(waits, EpicWait{WorkItemID: id, Owner: owner, Says: fmt.Sprintf("%s, made from %s, %s", s.name(id, ""), s.name(piece.From, ""), says)})
			}
		}
		if s.inside(id) {
			continue
		}
		owner, says := MoverOf(domain.RoleReviewer), "has not been judged yet"
		reserved, _ := entry.Reservation(piece.Judgment)
		switch {
		case piece.Outcome != nil:
			owner, says = MoverProductManager, "is a question about scope and does not start: "+scopeQuestion(*piece.Outcome)
			if answered, said, ok := s.scopeStep(epic, piece); ok {
				owner, says = answered, "does not start: it "+said
			}
		case reserved.Result != nil && reserved.Result.Unknown:
			owner, says = MoverDevelopmentManager, "was sent to be judged and has no known answer ("+singleLine(reserved.Result.Says, maxEpicReasonBytes)+"): decide what becomes of it"
		}
		from := s.name(piece.From, "")
		if check, correcting := entry.Correcting(id); correcting && piece.From == epicprogress.FromCheck(check.ID) {
			from = piece.From + " of the whole epic"
		}
		waits = append(waits, EpicWait{WorkItemID: id, Owner: owner, Says: fmt.Sprintf("%s, made from %s, %s", s.name(id, ""), from, says)})
	}
	return waits
}

// commentsUnknown is each review comment whose judgment was sent and has no known
// answer, which the development manager decides about. Its change request starts
// no repair meanwhile.
func commentsUnknown(entry epicprogress.Entry) []EpicWait {
	var waits []EpicWait
	for _, comment := range entry.Comments {
		if reserved, _ := entry.Reservation(comment.Judgment); comment.Outcome == nil && reserved.Result != nil && reserved.Result.Unknown {
			waits = append(waits, EpicWait{WorkItemID: comment.Member, Owner: MoverDevelopmentManager, Says: fmt.Sprintf(
				"the change request %s on pull request #%d of %s was sent to be judged against the epic and has no known answer (%s), so no repair answers it: decide what becomes of it",
				comment.ID, comment.PullRequest, comment.Member, singleLine(reserved.Result.Says, maxEpicReasonBytes))})
		}
	}
	return waits
}

// scopeStep is whose step a piece waits on once the Lead Product Manager has
// answered the question about scope its latest judgment put to her, and what it
// says; ok is false until she has. Where her handling recorded that only the
// operator can act, the epic can be delivered without the piece and only he can
// take it out, so the step is his, as the ownership registry resolves it.
// Otherwise the development manager rewrites the piece, and an edit seen after
// her answer waits to be judged.
func (s *epicStandings) scopeStep(epic epicscope.Epic, piece epicprogress.Piece) (owner Mover, says string, ok bool) {
	answer := piece.Answered
	if piece.Outcome == nil || !piece.Outcome.Answer.Asks() || answer == nil {
		return "", "", false
	}
	if answer.Operator != "" {
		owner = resolved(Attention{Kind: AttentionOperatorAction, OperatorAction: &OperatorAction{Needs: answer.Says, Reason: answer.Operator, ReportID: piece.Question}}).Ownership.Owner
		return owner, fmt.Sprintf("was judged not to be asked for by the epic, and the Lead Product Manager's answer to %s is that the epic can be delivered without it (%s); only the operator can take it out, with `yoyo epic remove %s %s --words ...`",
			piece.Question, singleLine(answer.Says, maxEpicReasonBytes), epic.WorkItemID, piece.WorkItemID), true
	}
	if item, read := s.items[piece.WorkItemID]; read {
		digest := epicprogress.TextDigest(item)
		if _, judged := piece.JudgedAs(digest); !judged {
			if at, seen := piece.SeenAt(digest); !seen || !at.Before(answer.At) {
				return MoverOf(domain.RoleReviewer), fmt.Sprintf("was rewritten after the Lead Product Manager answered %s, and its new text waits to be judged", piece.Question), true
			}
		}
	}
	return MoverDevelopmentManager, fmt.Sprintf("was judged not to be asked for by the epic, and the Lead Product Manager answered %s: rewrite it to fit, and the first edit after her answer is judged again", piece.Question), true
}

func scopeQuestion(outcome epicprogress.Outcome) string {
	why := singleLine(outcome.Why, maxEpicReasonBytes)
	switch outcome.Answer {
	case epicprogress.AnswerOutside:
		return "it asks for more than the epic's description (" + why + ")"
	case epicprogress.AnswerCannotTell:
		return "whether the epic's description asks for it cannot be told (" + why + ")"
	default:
		return why
	}
}

// waitsOnItems is a member held by other items: those outside the epic, on it or
// on an item it was broken out of, and nothing where they are all inside.
func (s *epicStandings) waitsOnItems(epic epicscope.Epic, id string) epicMember {
	inside := s.view.Items(epic)
	var member epicMember
	wait := func(on string) {
		if on != epic.WorkItemID && !slices.Contains(inside, on) && !s.done(on) {
			member.waits = append(member.waits, EpicWait{Owner: MoverNobody, Says: fmt.Sprintf("%s waits for %s, which is not in this epic.", s.name(id, ""), s.name(on, ""))})
		}
	}
	seen := map[string]bool{}
	for above := id; above != "" && !seen[above]; above = s.items[above].DecomposedFrom() {
		seen[above] = true
		for _, on := range s.items[above].WaitingOn(s.unfinished) {
			wait(on)
		}
	}
	for child, item := range s.items {
		if item.DecomposedFrom() == id {
			wait(child)
		}
	}
	slices.SortFunc(member.waits, func(a, b EpicWait) int { return strings.Compare(a.Says, b.Says) })
	return member
}

// stoppageOwner is who decides about a stopped run: whoever the attention line
// names for it, where it was passed on, and otherwise the owner given.
func (s *epicStandings) stoppageOwner(id string, otherwise Mover) Mover {
	for _, entry := range s.needs {
		if entry.WorkItemID == id && (entry.Kind == AttentionProductDecision || entry.Kind == AttentionOperatorAction && entry.OperatorAction != nil && entry.OperatorAction.RunID != "") {
			return entry.Ownership.Owner
		}
	}
	return otherwise
}

// questions is each unanswered question that names an item: one put to the
// operator, a directive, a decision, or a question between roles.
func (s *epicStandings) questions(id string) []EpicWait {
	var waits []EpicWait
	ask := func(owner Mover, step, what string) {
		waits = append(waits, s.wait(id, owner, step, "%s", what))
	}
	for _, entry := range s.needs {
		switch {
		case entry.Kind == AttentionConcern && entry.Concern != nil && namesItem(id, entry.Concern.Subject, entry.Concern.Detail, entry.Concern.Question):
			ask(entry.Ownership.Owner, "", "has a question with no answer: "+singleLine(entry.Concern.Question, maxRefusalBytes))
		case entry.Kind == AttentionDirective && entry.Directive != nil && len(entry.Directive.Scope) > 0 && entry.Directive.Affects(id):
			ask(entry.Ownership.Owner, "directive", "has a directive that is not settled: "+singleLine(entry.Directive.Unresolved, maxRefusalBytes))
		case entry.Kind == AttentionProductDecision && entry.WorkItemID == id:
			ask(entry.Ownership.Owner, "stoppage", "has a decision to make about its stopped run "+entry.ID)
		case entry.Kind == AttentionHumanGate && entry.HumanGate != nil && entry.WorkItemID == id:
			ask(entry.Ownership.Owner, "person", epicHoldSays[backlog.HeldForAGate]+": "+singleLine(entry.HumanGate.Statement+entry.HumanGate.Unreadable, maxRefusalBytes))
		}
	}
	for _, asked := range s.exchanges {
		if !asked.Open() || !namesItem(id, questionsOf(asked)...) {
			continue
		}
		owes := asked.Answerer.Role
		if last := len(asked.Rounds) - 1; last >= 0 && asked.Rounds[last].Answer != "" {
			owes = asked.Asker.Role
		}
		ask(MoverOf(owes), "", "has a question between roles that is not settled: "+singleLine(asked.Question, maxRefusalBytes))
	}
	return waits
}

// questionsOf is every question an exchange between roles asked.
func questionsOf(asked exchange.Exchange) []string {
	said := []string{asked.Question}
	for _, round := range asked.Rounds {
		said = append(said, round.Question)
	}
	return said
}

// namesItem reports a text that names the item, and not one its identifier only
// begins.
func namesItem(id string, texts ...string) bool {
	whole := regexp.MustCompile(`(^|[^\w.-])` + regexp.QuoteMeta(id) + `($|[^\w.-]|\.($|[^\w]))`)
	return slices.ContainsFunc(texts, whole.MatchString)
}

// suggestions counts the unfinished items outside the open epics that were
// admitted from a report filed during one of them.
func (f epicFacts) suggestions(sources Sources) (int, string) {
	reports, handlings, problem := readPile(sources)
	if problem != "" {
		return 0, problem
	}
	during, admitted := map[string]bool{}, map[string]bool{}
	for _, reported := range reports {
		during[reported.ID] = reported.DuringEpic != "" && f.isOpenEpic(reported.DuringEpic)
	}
	for _, handling := range handlings {
		for _, request := range handling.Requests {
			admitted[request.Admitted] = admitted[request.Admitted] || during[handling.ReportID]
		}
	}
	counted := 0
	for id, item := range f.items {
		if (during[item.Origin.Report] || admitted[id]) && !f.done(id) && !f.inside(id) && !f.isOpenEpic(id) {
			counted++
		}
	}
	return counted, ""
}
