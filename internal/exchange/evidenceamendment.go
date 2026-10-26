package exchange

// An amendment as a round carries it.
//
// An amendment is three facts that come apart in practice: somebody proposed a
// change, somebody decided it, and the decided change was written into the
// document and landed. A question about one is nearly always a question about
// which of the three has happened, so they are three fields here and never one
// word, and the rendering refuses to let the later two be read off the first: a
// proposal nobody decided is undecided and unpublished, whatever any exchange
// said about it.

import (
	"fmt"
	"strings"
	"time"
)

// AmendmentDecisionState is what was decided about a proposal.
type AmendmentDecisionState string

const (
	AmendmentUndecided AmendmentDecisionState = "none"
	AmendmentApproved  AmendmentDecisionState = "approved"
	AmendmentDeclined  AmendmentDecisionState = "declined"
	AmendmentWithdrawn AmendmentDecisionState = "withdrawn"
)

// AmendmentPublicationState is how far a decided change has got into its
// document.
type AmendmentPublicationState string

const (
	AmendmentNotWritten AmendmentPublicationState = "not-written"
	AmendmentWaiting    AmendmentPublicationState = "written-and-waiting"
	AmendmentInRun      AmendmentPublicationState = "in-a-run"
	AmendmentLanded     AmendmentPublicationState = "landed"
	AmendmentReturned   AmendmentPublicationState = "returned"
)

// AmendmentProposal is the change as it was proposed.
type AmendmentProposal struct {
	Artifact string
	Kind     string
	Owner    string
	Role     string
	Agent    string
	Change   string
	Why      string
	Intent   string
	// Revision is the document's revision the proposal was proposed against,
	// and empty where the proposal did not record one.
	Revision   string
	RunID      string
	WorkItemID string
	RaisedAt   time.Time
}

// AmendmentDecision is what was decided, by whom, when, and why.
type AmendmentDecision struct {
	State        AmendmentDecisionState
	Decider      string
	Authority    string
	Reason       string
	Conversation string
	At           time.Time
}

// AmendmentPublication is how far the decided change has got into its
// document: the run carrying it, the commit it landed at, or why it came back.
//
// Matched says how the publication was found where it was not read off a
// record linking it to this amendment — today, by its document and the time
// after the approval — and is said beside it, so a reader knows the run named
// may be publishing some other revision of the same document.
type AmendmentPublication struct {
	Matched string
	State   AmendmentPublicationState
	Run     string
	Commit  string
	Reason  string
	At      time.Time
}

// AmendmentEvidence is one amendment's three states.
type AmendmentEvidence struct {
	ID          string
	Proposal    AmendmentProposal
	Decision    AmendmentDecision
	Publication AmendmentPublication
}

// settled is the evidence with the rule held: only an approved change is ever
// published, so a publication read for anything else is not this proposal's.
func (a AmendmentEvidence) settled() AmendmentEvidence {
	if a.Decision.State == "" {
		a.Decision.State = AmendmentUndecided
	}
	if a.Publication.State == "" {
		a.Publication.State = AmendmentNotWritten
	}
	if a.Decision.State != AmendmentApproved {
		a.Publication = AmendmentPublication{State: AmendmentNotWritten}
	}
	return a
}

// Revision is what this amendment's state is at, for comparing with the
// revision a writer named: it changes whenever the decision or the publication
// does.
func (a AmendmentEvidence) Revision() string {
	settled := a.settled()
	revision := "decision:" + string(settled.Decision.State) + "/publication:" + string(settled.Publication.State)
	switch {
	case settled.Publication.Commit != "":
		revision += "@" + settled.Publication.Commit
	case settled.Publication.Run != "":
		revision += "@" + settled.Publication.Run
	}
	return revision
}

// Text is the amendment as a round carries it, the three states under three
// headings.
func (a AmendmentEvidence) Text() string {
	settled := a.settled()
	proposal := settled.Proposal
	var rendered strings.Builder
	rendered.WriteString("Proposal:\n")
	fmt.Fprintf(&rendered, "  proposed by: the %s", orUnnamed(proposal.Role))
	if proposal.Agent != "" {
		fmt.Fprintf(&rendered, " (agent %s)", proposal.Agent)
	}
	fmt.Fprintf(&rendered, ", %s\n", LocalTime(proposal.RaisedAt))
	fmt.Fprintf(&rendered, "  document: %s (a %s, owned by the %s)\n", proposal.Artifact, orUnnamed(proposal.Kind), orUnnamed(proposal.Owner))
	if proposal.Revision != "" {
		fmt.Fprintf(&rendered, "  proposed against revision: %s\n", proposal.Revision)
	} else {
		rendered.WriteString("  proposed against revision: not recorded with the proposal\n")
	}
	if proposal.Intent != "" {
		fmt.Fprintf(&rendered, "  intent, as the proposer said: %s\n", proposal.Intent)
	}
	if proposal.WorkItemID != "" || proposal.RunID != "" {
		fmt.Fprintf(&rendered, "  raised in: run %s", orUnnamed(proposal.RunID))
		if proposal.WorkItemID != "" {
			fmt.Fprintf(&rendered, ", for work item %s", proposal.WorkItemID)
		}
		rendered.WriteString("\n")
	}
	fmt.Fprintf(&rendered, "  change: %s\n", strings.TrimSpace(proposal.Change))
	fmt.Fprintf(&rendered, "  why: %s\n", strings.TrimSpace(proposal.Why))

	rendered.WriteString("\nDecision:\n")
	decision := settled.Decision
	switch decision.State {
	case AmendmentUndecided:
		rendered.WriteString("  none: nothing has decided this proposal, so it is undecided\n")
	default:
		fmt.Fprintf(&rendered, "  %s by the %s", decision.State, orUnnamed(decision.Decider))
		if decision.Authority != "" && decision.Authority != decision.Decider {
			fmt.Fprintf(&rendered, " under the %s's authority", decision.Authority)
		}
		fmt.Fprintf(&rendered, ", %s\n", LocalTime(decision.At))
		if decision.Conversation != "" {
			fmt.Fprintf(&rendered, "  decided in: conversation %s\n", decision.Conversation)
		}
		if reason := strings.TrimSpace(decision.Reason); reason != "" {
			fmt.Fprintf(&rendered, "  reason: %s\n", reason)
		} else {
			rendered.WriteString("  reason: none recorded\n")
		}
	}

	rendered.WriteString("\nPublication:\n")
	publication := settled.Publication
	switch publication.State {
	case AmendmentNotWritten:
		switch decision.State {
		case AmendmentApproved:
			rendered.WriteString("  not written: the change is approved and no revision of the document carrying it has been written\n")
		case AmendmentUndecided:
			rendered.WriteString("  not written: nothing has been published, because nothing has been decided\n")
		default:
			fmt.Fprintf(&rendered, "  not written: the proposal was %s, so it is not to be published\n", decision.State)
		}
	case AmendmentWaiting:
		rendered.WriteString("  written and waiting: a revision of the document carrying it is written and no run has taken it yet\n")
	case AmendmentInRun:
		fmt.Fprintf(&rendered, "  in a run: run %s is publishing it and has not finished\n", orUnnamed(publication.Run))
	case AmendmentLanded:
		fmt.Fprintf(&rendered, "  landed at commit %s", orUnnamed(publication.Commit))
		if publication.Run != "" {
			fmt.Fprintf(&rendered, ", by run %s", publication.Run)
		}
		if !publication.At.IsZero() {
			fmt.Fprintf(&rendered, ", %s", LocalTime(publication.At))
		}
		rendered.WriteString("\n")
	case AmendmentReturned:
		fmt.Fprintf(&rendered, "  returned: run %s did not land it", orUnnamed(publication.Run))
		if reason := strings.TrimSpace(publication.Reason); reason != "" {
			fmt.Fprintf(&rendered, ", because %s", reason)
		}
		rendered.WriteString("\n")
	default:
		fmt.Fprintf(&rendered, "  %s\n", publication.State)
	}
	if matched := strings.TrimSpace(publication.Matched); matched != "" && publication.State != AmendmentNotWritten {
		fmt.Fprintf(&rendered, "  how this was found: %s\n", matched)
	}
	return rendered.String()
}

// Record is the amendment as a source returns it.
func (a AmendmentEvidence) Record(source string) EvidenceRecord {
	return EvidenceRecord{Source: source, Revision: a.Revision(), Text: a.Text()}
}
