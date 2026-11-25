// Package maystart decides whether something may start under the epics the
// operator has named. It is the only code that answers that, and it is asked
// beside the hold on new work, the hold for design and every other check: a yes
// here lifts none of them.
//
// Membership is read from the record of named epics (epicscope) and the pieces
// recorded as joined (epicprogress), and never worked out from the tracker.
package maystart

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
)

// Answer is what was decided. Only May starts anything.
type Answer string

const (
	May          Answer = "may"
	MayNot       Answer = "may-not"
	CouldNotTell Answer = "could-not-tell"
)

// Decision is the answer and, unless it is May, why in plain words.
type Decision struct {
	Answer Answer
	Why    string
	// Open is the epics that have not ended, on a run refused for being in none
	// of them: each is one the operator can add its item to.
	Open []string
	// Clears is what lets the same start go ahead, in words, where it is neither
	// adding the item nor a file being mended.
	Clears string
	// NotKept is why Ask could not keep the answer in the record of refused
	// starts. The answer stands either way.
	NotKept error `json:"-"`
}

// MayStart reports a yes. Being unable to tell is not one, and nor is a decision
// nobody made.
func (d Decision) MayStart() bool { return d.Answer == May }

// Start is what is about to start and by which path. It is made only by the
// functions below, and one made by none of them cannot be judged.
type Start struct {
	path  Path
	what  what
	items []string
	role  domain.AgentRole

	document Document

	// target is the branch a team branch update is for.
	target string

	// agent, source and due are said of a role's pass only; see OfAgent and Due.
	agent  string
	source string
	due    string
}

// Document is which document a run publishes. A refusal of it is kept under its
// identifier, so the same document is one entry whichever write is refused.
type Document struct {
	ID    string
	Title string
	// WriteID is the write being published, and ConfirmedAt when it was
	// confirmed, which says which of two writes is the newer.
	WriteID     string
	ConfirmedAt time.Time
}

// Path is the way it is about to start, and empty for what is on no path.
func (s Start) Path() Path { return s.path }

type what int

const (
	workItemRun what = iota + 1
	documentRun
	rolePass
	goalReview
	messageReading
	teamBranchUpdate
	operatorConversation
	roleQuestion
)

// Run is a run for a work item: a developer run, a repair, a continuation or any
// other that does not publish a document.
func Run(path Path, item string) Start {
	return Start{path: path, what: workItemRun, items: []string{item}}
}

// DocumentRun is a run that publishes a document, with the work items the
// document answers. A path that repairs or picks up runs may start one too, and
// it is still judged by what it answers. What asks also says which document it
// is (Of), without which a refusal cannot be kept.
func DocumentRun(path Path, answers ...string) Start {
	return Start{path: path, what: documentRun, items: append([]string(nil), answers...)}
}

// Of is the start with the document it publishes named.
func (s Start) Of(document Document) Start {
	s.document = document
	return s
}

// Pass is a role's pass that nobody is talking to the role in.
func Pass(path Path, role domain.AgentRole) Start {
	return Start{path: path, what: rolePass, role: role}
}

// OfAgent is a pass of one agent of its role: a program manager instance. Every
// other role's pass names none.
func (s Start) OfAgent(agent string) Start {
	s.agent = agent
	return s
}

// Due says what the pass came due for, so that a refusal of it is counted once
// however often it is asked about: source is what can start it, and due names
// this one time that source did. A pass that says neither is counted every time.
func (s Start) Due(source, due string) Start {
	s.source, s.due = source, due
	return s
}

// GoalReview is the review of unmet goals.
func GoalReview() Start { return Start{path: UnmetGoalReview, what: goalReview} }

// MessageReading is reading outside messages for work.
func MessageReading() Start { return Start{path: MessagesForWork, what: messageReading} }

// UpdateFromTeam is bringing the target branch up to date with the team's branch.
func UpdateFromTeam(target string) Start {
	return Start{path: TeamBranchUpdate, what: teamBranchUpdate, target: target}
}

// OperatorConversation is the operator's own conversation with a role. It is on
// no path, because it always may start.
func OperatorConversation() Start { return Start{what: operatorConversation} }

// RoleQuestion is a question one role asks another inside a turn that was
// allowed to start. The turn was what was decided, so the question is on no path.
func RoleQuestion() Start { return Start{what: roleQuestion} }

// Decide says whether the start may go ahead for the product whose record is
// under the state root.
func Decide(stateRoot string, product domain.ProductID, start Start) Decision {
	// These may start whatever the record says, so a record that cannot be read
	// does not stop the operator talking to a role about it.
	if start.what == operatorConversation || start.what == roleQuestion {
		return Decision{Answer: May}
	}
	view, err := read(stateRoot, product)
	if err != nil {
		return couldNotTell("%v", err)
	}
	return decide(view, start)
}

// read reads what a decision rests on: the record of named epics and, while epic
// mode is on, what is kept of each epic's progress.
func read(stateRoot string, product domain.ProductID) (epicprogress.View, error) {
	record, err := epicscope.Read(stateRoot, product)
	if err != nil {
		return epicprogress.View{}, fmt.Errorf("the record of the epics the operator named could not be read (%v)", err)
	}
	if !record.On() {
		return epicprogress.View{Record: record}, nil
	}
	view, err := epicprogress.Read(stateRoot, product, record)
	if err != nil {
		return epicprogress.View{}, fmt.Errorf("what is kept of the named epics' progress could not be read (%v)", err)
	}
	return view, nil
}

// decide is the answer the files that were read give.
func decide(view epicprogress.View, start Start) Decision {
	if !view.Record.On() {
		return Decision{Answer: May}
	}
	if problem := start.problem(); problem != "" {
		return couldNotTell("%s", problem)
	}
	switch start.what {
	case workItemRun:
		return decideRun(view, start.items[0])
	case documentRun:
		return decideDocument(view, start.items)
	case rolePass:
		return decidePass(view, start.role)
	case goalReview:
		return mayNot("the review of unmet goals does not start while the factory works only on the epics the operator named")
	case messageReading:
		return mayNot("outside messages are not read for work while the factory works only on the epics the operator named")
	case teamBranchUpdate:
		if openEpic(view, func(string) bool { return true }) {
			return Decision{Answer: May}
		}
		return noEpic(view)
	default:
		return couldNotTell("nothing says what is about to start")
	}
}

// problem is why a start cannot be judged: it says nothing, or the path it
// names is not on the closed list or does not start what it says.
func (s Start) problem() string {
	if s.what == 0 {
		return "nothing says what is about to start"
	}
	starts := s.path.starts()
	if starts == 0 {
		return fmt.Sprintf("%q is not one of the known ways work starts", s.path)
	}
	var fits bool
	switch s.what {
	case workItemRun:
		fits = starts == startsRun
	case documentRun:
		fits = starts == startsDocument || starts == startsRun
	case rolePass:
		fits = starts == startsPass
	case goalReview:
		fits = starts == startsGoalReview
	case messageReading:
		fits = starts == startsMessageReading
	case teamBranchUpdate:
		fits = starts == startsTeamBranchUpdate
	}
	if !fits {
		return fmt.Sprintf("what is about to start is not something %q starts", s.path)
	}
	if s.what == teamBranchUpdate && s.target == "" {
		return "nothing says which branch is about to be brought up to date"
	}
	if s.what == workItemRun && !beads.ValidIssueID(s.items[0]) {
		return fmt.Sprintf("the run is for %q, which is not a work item identifier", s.items[0])
	}
	return ""
}

func decideRun(view epicprogress.View, item string) Decision {
	record := view.Record
	if decision, held := inEpics(view, item); held {
		return decision
	}
	decision := mayNot("%s is not in an open epic the operator named", item)
	for _, epic := range record.Epics {
		if epic.Open() {
			decision.Open = append(decision.Open, epic.WorkItemID)
		}
	}
	return decision
}

// inEpics is the answer for an item that is in an open epic, and false for one
// that is in none.
func inEpics(view epicprogress.View, item string) (Decision, bool) {
	switch holders := view.Holders(item); len(holders) {
	case 0:
		return Decision{}, false
	case 1:
		if why := view.Unreadable(epicprogress.Of(holders[0])); why != "" {
			return progressUnreadable(holders[0].WorkItemID, why), true
		}
		return Decision{Answer: May}, true
	default:
		var ids, ends []string
		for _, epic := range holders {
			ids = append(ids, epic.WorkItemID)
			ends = append(ends, "`yoyo epic end "+epic.WorkItemID+"`")
		}
		decision := mayNot("%s is in more than one open epic (%s), and may be in one only", item, strings.Join(ids, " and "))
		decision.Clears = "the operator ends all but one of those epics: " + strings.Join(ends, " or ")
		return decision, true
	}
}

// progressUnreadable is a start in an epic whose own progress cannot be read. Other open
// epics carry on.
func progressUnreadable(epic, why string) Decision {
	decision := couldNotTell("the progress of epic %s cannot be read: %s", epic, why)
	decision.Clears = fmt.Sprintf("the operator runs `yoyo epic end %s` and then `yoyo epic start %s`, which begins it again", epic, epic)
	return decision
}

// noEpic is why nothing starts for the whole of the named epics: each has ended,
// or the ones that have not cannot be read.
func noEpic(view epicprogress.View) Decision {
	for _, epic := range view.Record.OpenEpics() {
		if why := view.Unreadable(epicprogress.Of(epic)); why != "" {
			return progressUnreadable(epic.WorkItemID, why)
		}
	}
	return mayNot("every epic the operator named has ended")
}

func decideDocument(view epicprogress.View, answers []string) Decision {
	var refused *Decision
	for _, item := range answers {
		decision, held := inEpics(view, item)
		if held && decision.MayStart() || openEpic(view, func(epic string) bool { return epic == item }) {
			return Decision{Answer: May}
		}
		for _, epic := range view.Record.OpenEpics() {
			if why := view.Unreadable(epicprogress.Of(epic)); why != "" && epic.WorkItemID == item {
				decision, held = progressUnreadable(item, why), true
			}
		}
		if held && refused == nil {
			refused = &decision
		}
	}
	if refused != nil {
		return *refused
	}
	if len(answers) == 0 {
		return mayNot("the document answers no work item, so it waits until epic mode is turned off or it is written again naming a member of an open epic")
	}
	return mayNot("the document answers no open epic the operator named and no work item in one")
}

func decidePass(view epicprogress.View, role domain.AgentRole) Decision {
	switch role {
	case domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect:
		if openEpic(view, func(string) bool { return true }) {
			return Decision{Answer: May}
		}
		return noEpic(view)
	case domain.RoleProgramManager:
		return mayNot("a program manager's pass does not start while the factory works only on the epics the operator named")
	default:
		return couldNotTell("no rule covers a pass of the role %q", role)
	}
}

// openEpic looks at every entry because an epic that ended may have another. One
// whose progress cannot be read lets nothing start.
func openEpic(view epicprogress.View, matches func(epic string) bool) bool {
	for _, epic := range view.Record.OpenEpics() {
		if view.Unreadable(epicprogress.Of(epic)) == "" && matches(epic.WorkItemID) {
			return true
		}
	}
	return false
}

func mayNot(format string, args ...any) Decision {
	return Decision{Answer: MayNot, Why: fmt.Sprintf(format, args...)}
}

func couldNotTell(format string, args ...any) Decision {
	return Decision{Answer: CouldNotTell, Why: fmt.Sprintf(format, args...)}
}
