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
	// step is what is owed, where two records can ask for the same one: a question
	// and a hold of the same step are one wait.
	step string
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
// where it does not. It is an optional capability of EpicRecords: no budget is
// kept until piece 12 of the design is built.
type EpicBudgets interface {
	RefusesStart(naming epicprogress.Naming) (string, error)
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
	unfinished map[string]struct{}
	problem    string
	// conversations is read for the questions put to the operator, and asked is
	// what was read.
	conversations Conversations
	asked         []operatorQuestion
	askedErr      error
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
}

func (s *epicStandings) of(epic epicscope.Epic) EpicStanding {
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
		return EpicStanding{State: state}
	}
	for _, delivery := range entry.Deliveries {
		if !delivery.At.After(opened) {
			at := delivery.At
			standing.DeliveredBefore = &at
		}
	}
	allDone, handed, underway, refused := true, false, false, false
	for _, id := range s.view.Items(epic) {
		member, err := s.member(epic, entry, id, standing.Budget != "", map[string]bool{})
		if err != nil {
			return fail(err.Error())
		}
		questions := s.questions(id)
		allDone = allDone && member.done && len(questions) == 0
		handed = handed || member.handed
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
	delivered := EpicIntegrated
	if handed {
		delivered = EpicHandedOver
	}
	if allDone {
		wait := s.checkOwed(epic, entry, opened)
		if wait == nil && s.needsProblem != "" {
			return fail("whether a question about it is open could not be read: " + s.needsProblem)
		}
		if wait == nil {
			standing.State, standing.Budget = delivered, ""
			return standing
		}
		standing.Waits = append(standing.Waits, *wait)
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

// ended is where an epic the operator ended stands. It had been delivered where
// its delivery still counted then, every member's change was landed or handed
// over, and no question was open. Afterwards only merges move it.
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
	asked, err := s.operatorQuestions()
	if err != nil {
		return "", err
	}
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

// operatorQuestions is every question put to the operator, read once for a
// reading. A log or a line of one that cannot be read is an error, because what
// it held could have been open.
func (s *epicStandings) operatorQuestions() ([]operatorQuestion, error) {
	if s.askedRead {
		return s.asked, s.askedErr
	}
	s.askedRead = true
	s.asked, s.askedErr = readOperatorQuestions(s.conversations)
	if s.askedErr != nil {
		s.askedErr = fmt.Errorf("when the operator was asked could not be read: %w", s.askedErr)
	}
	return s.asked, s.askedErr
}

func readOperatorQuestions(conversations Conversations) ([]operatorQuestion, error) {
	events, can := conversations.(ConcernEvents)
	if !can {
		return nil, errors.New("nothing was wired to read the conversations' event logs")
	}
	recorded, err := conversations.Recorded()
	if err != nil {
		return nil, err
	}
	pending := map[string]bool{}
	for _, conversation := range recorded {
		for _, concern := range conversation.PendingConcerns {
			pending[concernWaitID(conversation.ConversationID, concern.ID)] = true
		}
	}
	logged, err := events.LoggedConversations()
	if err != nil {
		return nil, err
	}
	var asked []operatorQuestion
	for _, conversation := range logged {
		found, skipped, err := events.EventsOfType(conversation, execution.EventConcernRaised, execution.EventConcernAnswered)
		if err != nil {
			return nil, err
		}
		if len(skipped) > 0 {
			return nil, fmt.Errorf("%s of the event log of %s could not be read", count(len(skipped), "line"), conversation)
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
				return nil, fmt.Errorf("an event about a question in %s could not be read", conversation)
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
	return asked, nil
}

// doneAt is whether a member of an epic ended at a moment was done then, and
// whether its change is not merged yet. Runs started later are not looked at,
// other than one that took a pull request over.
func (s *epicStandings) doneAt(epic epicscope.Epic, entry epicprogress.Entry, id string, at time.Time, seen map[string]bool) (done, handed bool, err error) {
	runs, err := s.runsOf(id)
	if err != nil {
		return false, false, err
	}
	if s.items[id].Landing != "" && !s.items[id].ClosedAt.After(at) {
		return true, false, nil
	}
	for _, run := range runs {
		if run.StartedAt.After(at) || !run.Discharges() {
			continue
		}
		for steps := 0; run.PersonMerge != nil && run.PersonMerge.Superseded() && steps < len(runs); steps++ {
			next := slices.IndexFunc(runs, func(other runstate.State) bool { return other.RunID == run.PersonMerge.SupersededBy })
			if next < 0 {
				break
			}
			run = runs[next]
		}
		closed := closedUnmerged(run)
		switch {
		case closed != nil && !closed.After(at):
			continue
		case closed != nil, run.WaitingForPersonMerge(), run.BringingPersonMergeUpToDate(),
			run.PersonMerge != nil && run.PersonMerge.Superseded(),
			run.WaitingForTeamMerge() && run.TeamMerge.PullRequest != nil:
			done, handed = true, true
		case landed(run), approvedSummary(run):
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

// checkOwed is what the check of the whole epic still needs, and nil where it
// passed. One that did not pass is made again after a removal, and is then not a
// round of correction.
func (s *epicStandings) checkOwed(epic epicscope.Epic, entry epicprogress.Entry, opened time.Time) *EpicWait {
	removedAfter := func(check epicprogress.Check) bool {
		return slices.ContainsFunc(epic.Removed, func(removal epicscope.Removal) bool {
			return removal.RemovedAt.After(check.At) && !slices.ContainsFunc(entry.Joined, func(joined epicprogress.Joined) bool {
				return joined.At.After(check.At) && joined.At.Before(removal.RemovedAt)
			})
		})
	}
	var last *epicprogress.Check
	rounds := 0
	for _, check := range entry.Checks {
		if !check.Passed() && removedAfter(check) {
			continue
		}
		if check.Found == epicprogress.FoundNotMet {
			rounds++
		}
		if check.At.After(opened) && (check.Passed() || !slices.ContainsFunc(epic.Removed, func(r epicscope.Removal) bool { return r.RemovedAt.After(check.At) })) {
			last = &check
		}
	}
	switch {
	case last == nil:
		return &EpicWait{Owner: MoverOf(domain.RoleReviewer), Says: "every member is done and the check of the whole epic is owed; that check is not built yet"}
	case last.Passed():
		return nil
	case last.Found == epicprogress.FoundNotMet && rounds > 2:
		return &EpicWait{Owner: MoverProductManager, Says: "the check of the whole epic found something missing a third time: decide on one more round or ask about scope"}
	case last.Found == epicprogress.FoundNotMet:
		return &EpicWait{Owner: MoverDevelopmentManager, Says: "write the piece that corrects what the check of the whole epic found missing"}
	case last.Found == epicprogress.FoundPersonsStep:
		answer := humanGateAttention("", HumanGateWait{}).Ownership
		return &EpicWait{Owner: answer.Owner, Says: "the epic's description reserves a step for a person, and it has not been taken"}
	default:
		return &EpicWait{Owner: MoverProductManager, Says: "say what evidence the check of the whole epic lacked, for the development manager to have written"}
	}
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
		return epicMember{underway: true}, nil
	}
	runs, err := s.runsOf(id)
	if err != nil {
		return epicMember{}, err
	}
	for _, run := range runs {
		if run.Discharges() && (run.WaitingForPersonMerge() || run.WaitingForTeamMerge() && run.TeamMerge.PullRequest != nil) {
			return epicMember{done: true, handed: true}, nil
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
		run.Integration == nil && run.PullRequest == nil && run.PersonMerge == nil && run.Retirement == nil
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
