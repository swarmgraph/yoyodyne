package chat

// A done-condition no developer run may satisfy is refused where the item is
// written.
//
// Every door into the queue that carries an item's text asks the same question
// of it: does a clause of what done means name a path under one of the
// artifact homes, or a document one of those homes owns, without a grant for
// it? Three items in one week were admitted with one — a design's query list
// to mark, a design's status entry to reconcile, a ruling to record on a design
// — and each cost a run before the reviewer or the developer found the clause
// the run could not meet. The check belongs at admission, where refusing it
// costs the role a sentence and the correction is the one the reviewer would
// have named anyway: take the clause out and say the document's owner amends
// it, or carry the grant where the change behind it is already decided.
//
// The question is asked with the item's executor beside it, because the same
// clause is right on an item a conversation carries and unmeetable on one a run
// does — and because the shape of conversation work is read whether or not a
// document is named. yoyodyne-ifd.330 was admitted as "The architect designs
// side conversations" with "Done means the design is recorded in the governed
// documents" and no executor, named no document, and was handed to a developer
// run that could only report the design had already landed. An item that reads
// so and names no executor is refused with the marker as the fix.
//
// The run asks the same question of the item it is handed, before it claims it
// (orchestrator.refuseUngrantedCondition), because two of the four fields a
// grant and a condition are read from are written with the tracker's own
// command rather than through any door here, and because the queue predates
// this gate.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
)

// conditionRefusal is why an item whose done-conditions read as given will not
// be admitted, or empty where they name nothing a run may not write. The grants
// are read from the fields given, which are the ones a grant is honoured from.
func (s *Session) conditionRefusal(title, description, acceptanceCriteria string, executor domain.WorkItemExecutor, grantedFrom ...string) string {
	// An item naming an environment the project does not declare is one whose
	// run could never be given what its criteria need, so it is refused at the
	// same doors and for the same reason (checking-a-run-environment, section 4).
	if problem := config.EnvironmentsLineProblem(description, s.options.Environments); problem != "" {
		return "it names an environment no run can be given, so nothing was written: " + problem
	}
	problems := s.options.ArtifactHomes.ConditionProblems(protectedpath.Subject{
		Title:              title,
		Description:        description,
		AcceptanceCriteria: acceptanceCriteria,
		Granted:            protectedpath.Grants(grantedFrom...),
		Executor:           executor,
	})
	if len(problems) == 0 {
		return ""
	}
	return "its done-conditions name work no developer run may do, so nothing was written: " + errors.Join(problems...).Error()
}

// updateConditionRefusal judges an update that rewrites the description or the
// title, as the item will read once it lands. The item as the tracker held it
// was read as the action was aimed, so the fields the action leaves alone are
// read from there; where that read failed, the action's own words are all
// there is, and they are judged on their own rather than the update being
// waved through because the item could not be seen.
//
// An update that leaves the description and the title alone — a note, an
// executor — writes no done-condition and is judged on none. That is
// deliberate: an item admitted before this gate with such a condition in its
// criteria is exactly the item the product manager has to be able to write a
// note onto saying so, or mark with the executor that makes the condition
// right, and the run refuses to start on it either way.
func (s *Session) updateConditionRefusal(outcome *TrackerOutcome, change beads.WorkItemChange) string {
	if change.Description == "" && change.Title == "" {
		return ""
	}
	current := beads.WorkItem{}
	if outcome.target != nil {
		current = *outcome.target
	}
	title := current.Title
	if change.Title != "" {
		title = change.Title
	}
	description := current.Description
	if change.Description != "" {
		description = change.Description
	}
	// The executor the item will carry once the update lands: the one this
	// action sets, or else the one the item already has.
	executor := current.Executor
	if strings.TrimSpace(string(change.Executor)) != "" {
		executor = change.Executor
	}
	return s.conditionRefusal(title, description, current.AcceptanceCriteria, executor, title, description, current.Design, current.AcceptanceCriteria)
}

// ProposalConditionError reports that a turn proposed work whose done-conditions
// name work no developer run may do. Like ProposalGoalError it is not a broken
// conversation: the answer is real, nothing was proposed, and what is wrong is
// the one thing the operator would have been relying on — that approving the
// work approves something a run can finish. A proposal cannot name an
// executor, so work a conversation carries is admitted with "create" instead,
// which can.
type ProposalConditionError struct {
	Err error
}

func (e *ProposalConditionError) Error() string {
	return "the Lead Product Manager proposed work whose done-conditions name work no developer run may do: " + e.Err.Error()
}

func (e *ProposalConditionError) Unwrap() error { return e.Err }

// verifyProposalConditions checks every proposal's done-conditions before the
// operator is asked about any of it, for the reason the goals are checked
// there: an approval that then fails has already spent the decision it was
// asking for. A proposal carries a title and a description and can set neither
// design guidance nor acceptance criteria nor an executor, so it is judged as
// the developer run an approved proposal is admitted as.
func (s *Session) verifyProposalConditions(proposals []Proposal) error {
	var problems []error
	for _, proposal := range proposals {
		if problem := config.EnvironmentsLineProblem(proposal.Description, s.options.Environments); problem != "" {
			problems = append(problems, fmt.Errorf("%q: %s", strings.TrimSpace(proposal.Title), problem))
			continue
		}
		conditions := s.options.ArtifactHomes.ConditionProblems(protectedpath.Subject{
			Title:       proposal.Title,
			Description: proposal.Description,
			Granted:     protectedpath.Grants(proposal.Title, proposal.Description),
		})
		if len(conditions) == 0 {
			continue
		}
		problems = append(problems, fmt.Errorf("%q: %w", strings.TrimSpace(proposal.Title), errors.Join(conditions...)))
	}
	return errors.Join(problems...)
}
