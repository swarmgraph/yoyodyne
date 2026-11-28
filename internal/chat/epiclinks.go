package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// EpicLinks says which open epic an item is in, so that a role's link that would
// make work in a named epic wait on, or hold as a child, open work outside it is
// refused when it is written. Nothing outside the named epics starts without the
// operator, so such a link leaves the member waiting until he adds the item
// (docs/designs/epic-mode-second-part.md, "An item outside the epic that could
// block a member", point 4). It is satisfied by *epicprogress.Store; a
// conversation without one checks no link.
type EpicLinks interface {
	// EpicHolding is the open epic the item is in while epic mode is on, its own
	// item included, and whether another item is in that same epic. It answers no
	// epic while epic mode is off or where the item is in none.
	EpicHolding(item string) (epic string, inside func(string) bool, err error)
}

// outsideEpicRefusal is why holder may not be made to wait on linked, or to hold
// it as a child, and empty where it may. An empty linked is a new item created
// under holder, which is outside every epic until it joins. The harness never
// refuses the operator's own commands or a write a person makes to the tracker;
// this is asked only of a role's tracker action.
func (s *Session) outsideEpicRefusal(ctx context.Context, holder, linked string, child bool) string {
	if s.options.EpicLinks == nil || strings.TrimSpace(holder) == "" {
		return ""
	}
	epic, inside, err := s.options.EpicLinks.EpicHolding(holder)
	if err != nil {
		return fmt.Sprintf("refused, and nothing was changed: whether %s is in a named epic, and so whether this link would leave it waiting on work outside it, could not be read: %s",
			holder, singleLine(err.Error(), maxTrackerFailureBytes))
	}
	if epic == "" {
		return ""
	}
	if linked == "" {
		return fmt.Sprintf("refused, and nothing was changed: %s is in epic %s, which the operator named, and a new item under it would be open work outside that epic that it holds as a child. "+
			"Only the development manager's split of a member, or a correction the epic owes, is created there and judged for joining; nothing else outside the named epics starts. "+
			"Write the work as its own item, not under %s, or ask the operator whether to bring it into the epic once it exists", holder, epic, holder)
	}
	if inside(linked) {
		return ""
	}
	item, err := s.options.Tracker.Show(ctx, linked)
	if err != nil {
		return fmt.Sprintf("refused, and nothing was changed: %s is in epic %s, and whether %s is still open could not be read: %s",
			holder, epic, linked, singleLine(err.Error(), maxTrackerFailureBytes))
	}
	if strings.TrimSpace(item.Status) == "closed" {
		return ""
	}
	how := "make it wait on " + linked
	if child {
		how = "put " + linked + " under it as a child"
	}
	return fmt.Sprintf("refused, and nothing was changed: %s is in epic %s, which the operator named, and this would %s, while %s is open and outside that epic. "+
		"Nothing outside the named epics starts, so %s would wait until the operator brings %s in, which he does with `yoyo epic add %s %s`. "+
		"Rework %s so it does not need %s, or ask for it to be added", holder, epic, how, linked, holder, linked, epic, linked, holder, linked)
}

// createsJoiningPiece says a creation is one of the acts whose piece is judged
// for joining a named epic in the same act: the development manager writing the
// correction a check of the epic owes, or splitting a member or a piece that
// joined. The joining code judges a split only of those, so a creation under the
// epic's own item is not one, and is refused as any other item created under
// work in a named epic is.
func (s *Session) createsJoiningPiece(action TrackerAction) bool {
	if strings.TrimSpace(action.EpicCheck) != "" {
		return true
	}
	if s.state.Role != domain.RoleDevelopmentManager || !s.authority().ParentRequired {
		return false
	}
	parent := action.parent()
	if s.options.EpicLinks == nil || parent == "" {
		return true
	}
	// Where the epic cannot be read this answers no, and the refusal that follows
	// says the epic could not be read.
	epic, _, err := s.options.EpicLinks.EpicHolding(parent)
	return err == nil && epic != "" && epic != parent
}

// ProposalEpicError reports that a turn proposed work placed under an item in a
// named epic while epic mode is on. As for the other refused proposals, the turn
// completed and nothing was created or put to anybody: the refusal goes back to
// the role, which places the work elsewhere.
type ProposalEpicError struct {
	Err error
}

func (e *ProposalEpicError) Error() string {
	return "the Lead Product Manager proposed work under an item in a named epic: " + e.Err.Error()
}

func (e *ProposalEpicError) Unwrap() error { return e.Err }

// verifyProposalEpics refuses a proposal whose parent is in a named epic, before
// it is recorded, so it is neither admitted without the operator nor put to him.
// A proposal is none of the acts whose piece is judged for joining, so the item it
// would create is outside the epic and held as a child of work in it.
func (s *Session) verifyProposalEpics(ctx context.Context, proposals []Proposal) error {
	var problems []error
	for _, proposal := range proposals {
		if refusal := s.outsideEpicRefusal(ctx, strings.TrimSpace(proposal.Parent), "", true); refusal != "" {
			problems = append(problems, fmt.Errorf("%q: %s", strings.TrimSpace(proposal.Title), refusal))
		}
	}
	return errors.Join(problems...)
}

// ProposalRefusedError is a decided proposal the harness refused to carry out
// because of a named epic. The proposal is decided by it, so it waits on nobody.
type ProposalRefusedError struct {
	ProposalID string
	Why        string
}

func (e *ProposalRefusedError) Error() string {
	return fmt.Sprintf("proposal %s was refused, and nothing was created: %s", e.ProposalID, e.Why)
}

// refuseProposal records on the proposal that it was refused, before it stops
// awaiting a decision, and tells the conversation, which is where the role that
// made it reads what became of it.
func (s *Session) refuseProposal(record *proposalRecord, why string) error {
	if err := s.emit(execution.EventProposalRefused, rejection{PendingProposal: record.pending, Reason: why}); err != nil {
		return fmt.Errorf("record the refusal of proposal %s: %w", record.pending.ID, err)
	}
	record.decided = true
	refused := &ProposalRefusedError{ProposalID: record.pending.ID, Why: why}
	// The notice is kept with the record, so a later process still delivers it.
	s.notice("the harness refused to carry out proposal %s (%s) and created nothing: %s", record.pending.ID, record.pending.Proposal.Title, why)
	if err := s.record(); err != nil {
		return errors.Join(refused, fmt.Errorf("record the conversation after refusing proposal %s: %w", record.pending.ID, err))
	}
	return refused
}
