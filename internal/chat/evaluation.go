package chat

// Recording what the product manager made of an idea the operator brought it.
//
// An evaluation is advice and the harness treats it as nothing else: recording
// one admits no work, changes no document, and approves nothing. Everything it
// might lead to already has a path with an approval on it — work through the
// proposal the operator decides, a change to a document through that document's
// owner — and none of those paths runs through here. That is deliberate, and it
// is the whole reason the capability that gathers the evidence is separate from
// the authority that acts on it: research that could turn an idea into approved
// work would be a way to approve work by asking a model to look something up.
//
// What it does do is outlive the conversation. The reasoning, the sources, and
// what was still unknown are written down where somebody can find them when the
// question comes back, which is the one thing prose in a chat window cannot do.

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/evaluation"
)

// Evaluations is where a recorded evaluation is kept. It is satisfied by
// runstate.EvaluationStore.
type Evaluations interface {
	Append(recorded evaluation.Evaluation) error
}

// EvaluationError reports that a turn carried an evaluation block the harness
// could not read or could not keep. Like the errors beside it the conversation
// is intact and nothing was changed by it — an evaluation changes nothing by
// design — and what is lost is the record of the reasoning, which is exactly
// what the channel exists to keep.
type EvaluationError struct {
	Err error
}

func (e *EvaluationError) Error() string {
	return "the Lead Product Manager recorded an evaluation the harness cannot keep: " + e.Err.Error()
}

func (e *EvaluationError) Unwrap() error { return e.Err }

// recordEvaluation writes one evaluation durably, with the research the harness
// actually performed toward it attached. The findings come from the harness's
// own record of what it retrieved and when, rather than from the citations the
// product manager wrote: the citations say what it says it read, and these say
// what was actually fetched, and a record that could not tell the two apart
// would not be worth keeping.
func (s *Session) recordEvaluationWithoutToolAudit(entry evaluation.Entry) (*evaluation.Evaluation, error) {
	if s.options.Evaluations == nil {
		return nil, fmt.Errorf("no evaluation record is configured for this conversation, so the %s recommendation was not kept", recommendationName(entry))
	}
	recorded, err := evaluation.Record(entry, evaluation.Attribution{
		Role:           s.state.Role,
		Agent:          s.options.Agent,
		ConversationID: s.state.ConversationID,
		Turn:           s.state.Turns,
		ProductID:      s.options.ProductID,
		RepositoryID:   s.options.RepositoryID,
	}, s.takeResearch(), s.options.clock().Now())
	if err != nil {
		return nil, err
	}
	if err := s.options.Evaluations.Append(recorded); err != nil {
		return nil, err
	}
	return &recorded, nil
}

// recommendationName names what an entry advises, for a failure that has to say
// what was lost. It is a helper rather than a bare field read so a failure never
// prints an empty string where the recommendation should be.
func recommendationName(entry evaluation.Entry) string {
	if entry.Recommendation == "" {
		return "unstated"
	}
	return string(entry.Recommendation)
}
