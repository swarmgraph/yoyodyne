// Package maystart decides whether something may start under the epics the
// operator has named. It is the only code that answers that, and it is asked
// beside the hold on new work, the hold for design and every other check: a yes
// here lifts none of them.
//
// Membership is read from the record of named epics (epicscope) and never worked
// out from the tracker.
package maystart

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
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

// GoalReview is the review of unmet goals.
func GoalReview() Start { return Start{path: UnmetGoalReview, what: goalReview} }

// MessageReading is reading outside messages for work.
func MessageReading() Start { return Start{path: MessagesForWork, what: messageReading} }

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
	record, err := epicscope.Read(stateRoot, product)
	if err != nil {
		return couldNotTell("the record of the epics the operator named could not be read (%v)", err)
	}
	return decide(record, start)
}

// decide is the answer a record that was read gives.
func decide(record epicscope.Record, start Start) Decision {
	if !record.On() {
		return Decision{Answer: May}
	}
	if problem := start.problem(); problem != "" {
		return couldNotTell("%s", problem)
	}
	switch start.what {
	case workItemRun:
		return decideRun(record, start.items[0])
	case documentRun:
		return decideDocument(record, start.items)
	case rolePass:
		return decidePass(record, start.role)
	case goalReview:
		return mayNot("the review of unmet goals does not start while the factory works only on the epics the operator named")
	case messageReading:
		return mayNot("outside messages are not read for work while the factory works only on the epics the operator named")
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
	}
	if !fits {
		return fmt.Sprintf("what is about to start is not something %q starts", s.path)
	}
	if s.what == workItemRun && !beads.ValidIssueID(s.items[0]) {
		return fmt.Sprintf("the run is for %q, which is not a work item identifier", s.items[0])
	}
	return ""
}

func decideRun(record epicscope.Record, item string) Decision {
	if _, held := record.Holding(item); held {
		return Decision{Answer: May}
	}
	decision := mayNot("%s is not in an open epic the operator named", item)
	for _, epic := range record.Epics {
		if epic.Open() {
			decision.Open = append(decision.Open, epic.WorkItemID)
		}
	}
	return decision
}

func decideDocument(record epicscope.Record, answers []string) Decision {
	for _, item := range answers {
		if _, held := record.Holding(item); held || openEpic(record, func(epic string) bool { return epic == item }) {
			return Decision{Answer: May}
		}
	}
	if len(answers) == 0 {
		return mayNot("the document answers no work item, so it waits until epic mode is turned off or it is written again naming a member of an open epic")
	}
	return mayNot("the document answers no open epic the operator named and no work item in one")
}

func decidePass(record epicscope.Record, role domain.AgentRole) Decision {
	switch role {
	case domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect:
		if openEpic(record, func(string) bool { return true }) {
			return Decision{Answer: May}
		}
		return mayNot("every epic the operator named has ended")
	case domain.RoleProgramManager:
		return mayNot("a program manager's pass does not start while the factory works only on the epics the operator named")
	default:
		return couldNotTell("no rule covers a pass of the role %q", role)
	}
}

// openEpic looks at every entry because an epic that ended may have another.
func openEpic(record epicscope.Record, matches func(epic string) bool) bool {
	for _, epic := range record.Epics {
		if epic.Open() && matches(epic.WorkItemID) {
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
