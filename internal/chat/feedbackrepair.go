package chat

// The development manager's decision to repair an item whose run has ended, from
// feedback on a pull request into the team's branch that the harness tracks, or
// on a pull request the harness opened for a person to merge
// (docs/designs/integrating-to-a-local-branch.md, section 2).
//
// It is not one of the decisions about a stopped run: the run that landed the
// item ended as integrated, so there is no stoppage to name. It names the list
// entry it answers instead, the pull request and the piece of feedback the report
// on her list quotes, and the one item to repair. The harness refuses it, in
// plain words, once the item has closed, when the entry already has a decision,
// when the item is not waiting for a merge of that pull request, and
// when the item has no attempts left; it records the decision on the run that
// landed the item and spends one of the item's attempts. A later pass of the
// harness starts the new run. Nothing here starts a run.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// decisionRepairFromFeedback is the word that records the decision. It is this
// package's rather than the durable triage vocabulary's, for the reason the
// crossing is: it settles no stopped run and writes no decision on the item's
// triage record.
const decisionRepairFromFeedback = "repair-from-feedback"

// FeedbackRepairRequest is one decision as the conversation records it. Who
// decided and where are the conversation's own facts, never the reply's.
type FeedbackRepairRequest struct {
	PullRequest  int
	FeedbackID   string
	WorkItemID   string
	Reason       string
	DecidedBy    string
	Conversation string
	Turn         int
}

// FeedbackRepairDecided is what recording it came to.
type FeedbackRepairDecided struct {
	// RunID is the run that landed the item, which holds the decision.
	RunID string
}

// FeedbackRepairs records the decision. It is satisfied in the harness by an
// adapter over orchestrator.TeamFeedbackRepairer, and wired into the development
// manager's conversation and no other.
type FeedbackRepairs interface {
	Decide(ctx context.Context, request FeedbackRepairRequest) (FeedbackRepairDecided, error)
}

func (a TrackerAction) feedbackRepairProblems() []error {
	var problems []error
	if a.PullRequest <= 0 {
		problems = append(problems, fmt.Errorf("%s requires \"pull_request\", the number of the tracked pull request the feedback is on, as the report on your list names it", decisionRepairFromFeedback))
	}
	if strings.TrimSpace(a.Feedback) == "" {
		problems = append(problems, fmt.Errorf("%s requires \"feedback\", the identifier of the piece of feedback, such as line-comment-21, as the report on your list names it", decisionRepairFromFeedback))
	}
	if strings.TrimSpace(a.ID) == "" {
		problems = append(problems, fmt.Errorf("%s requires \"id\", the one item to repair", decisionRepairFromFeedback))
	}
	if strings.TrimSpace(a.Run) != "" {
		problems = append(problems, fmt.Errorf("%s names no \"run\": the run that landed the item ended as integrated, and what the decision answers is the list entry", decisionRepairFromFeedback))
	}
	if strings.TrimSpace(a.Reason) == "" {
		problems = append(problems, fmt.Errorf("%s requires \"reason\", the reasoning it is recorded with", decisionRepairFromFeedback))
	}
	if strings.TrimSpace(a.Budget) != "" || strings.TrimSpace(a.SupersededBy) != "" || strings.TrimSpace(a.WaitsOn) != "" || strings.TrimSpace(a.To) != "" || strings.TrimSpace(string(a.OperatorReason)) != "" || strings.TrimSpace(string(a.Hand)) != "" {
		problems = append(problems, fmt.Errorf("%s takes only \"id\", \"pull_request\", \"feedback\" and \"reason\"", decisionRepairFromFeedback))
	}
	return problems
}

func (s *Session) carryOutFeedbackRepair(ctx context.Context, outcome *TrackerOutcome) {
	action := outcome.Action
	id := strings.TrimSpace(action.ID)
	if s.options.FeedbackRepairs == nil {
		outcome.refused(errors.New("this conversation cannot record a repair from feedback on a tracked pull request, so nothing was decided"))
		return
	}
	decided, err := s.options.FeedbackRepairs.Decide(ctx, FeedbackRepairRequest{
		PullRequest: action.PullRequest, FeedbackID: strings.TrimSpace(action.Feedback), WorkItemID: id, Reason: action.Reason,
		DecidedBy: RoleTitle(s.state.Role), Conversation: s.state.ConversationID, Turn: s.state.Turns,
	})
	if err != nil {
		outcome.refused(err)
		return
	}
	note := s.trackerProvenance(fmt.Sprintf("%s, answering %s on pull request #%d into the team's branch", triageVerbs[decisionRepairFromFeedback], strings.TrimSpace(action.Feedback), action.PullRequest), action.Reason)
	if _, err := s.options.Tracker.Update(ctx, id, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		s.settleTrackerNote(ctx, outcome, id, note, "the decision recorded on the item")
		return
	}
	outcome.applied("decided to repair %s from %s on pull request #%d, recorded on run %s and one of the item's attempts spent; a new run of it starts at the harness's next pass, and the item keeps waiting for the merge of the pull request",
		id, strings.TrimSpace(action.Feedback), action.PullRequest, decided.RunID)
}
