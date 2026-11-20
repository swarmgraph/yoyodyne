package ownership

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
)

// A role's decision that needs the operator's hand.
//
// Some remedies are a role's to decide, but the harness cannot yet carry them
// out for that role, so a person has to type the command. The entry stays the
// role's: naming the operator as its owner would tell him a decision is his
// when it is not, which is the defect this registry exists to end. It carries a
// Hand instead, saying what the harness cannot yet do, what he types, and the
// work item that closes the gap. The surfaces print such entries under their
// own head on the needs-a-human line and count them as the harness's missing
// capability, never as his to decide (`docs/designs/ownership-and-the-operator.md`,
// "A role's decision that needs the operator's hand").
//
// The set of gaps is closed for the reason the operator's reasons are: a gap
// nobody named is one no item is admitted to close, and a record naming one
// the harness does not know is refused rather than shown.

// HandNeed is a capability the harness does not yet have, for which a role's
// decision needs the operator's keyboard.
type HandNeed string

const (
	// HandAmendmentDecision is deciding a change proposed to a document, where
	// the role that owns it holds no capability to decide it from its own
	// conversation. Owning roles decide their own (yoyodyne-ifd.437.14), so with
	// the shipped roles no amendment carries this; it stays for a role the
	// harness has not given the decision.
	HandAmendmentDecision HandNeed = "amendment-decision"
	// HandOperationalConfiguration is applying an operational configuration
	// value a role recommends — a check, a developer model, how often a task runs —
	// which today only a person can write into .yoyodyne/config.yaml.
	HandOperationalConfiguration HandNeed = "operational-configuration"
	// HandDocumentConfirmation is confirming a document a role wrote from a
	// conversation, where the project's approval setting for its kind asks for a
	// person.
	HandDocumentConfirmation HandNeed = "document-confirmation"
	// HandCapCrossing was crossing one work item's triage cap once the
	// development manager had used every crossing she may record herself. The
	// harness now carries that out: past her five the Lead Product Manager grants
	// up to five more from her own conversation, and no role passes ten
	// (`docs/designs/stopped-run-recovery.md`). So it is no longer a gap: a new
	// record naming it is refused with that path, and one recorded before resolves
	// to the Lead Product Manager as the one deciding, with no hand.
	HandCapCrossing HandNeed = "cap-crossing"
)

// handGap is what the registry knows of one gap: what the harness cannot yet
// do, what a person types meanwhile where the record names nothing more
// exact, and the work item that closes it, empty where none is admitted yet.
type handGap struct {
	missing  string
	types    string
	closedBy string
}

var handGaps = map[HandNeed]handGap{
	HandAmendmentDecision: {
		missing:  "letting the owning role decide the change from its own conversation",
		types:    "yoyo amendment approve|decline <amendment> --reason \"<why>\"",
		closedBy: "yoyodyne-ifd.437.14",
	},
	HandOperationalConfiguration: {
		missing:  "applying operational configuration a role recommends through a reviewed run",
		types:    "the change to .yoyodyne/config.yaml the finding names, made by hand",
		closedBy: "yoyodyne-ifd.434.8",
	},
	HandDocumentConfirmation: {
		missing:  "confirming a document written from a conversation as its kind's approval setting says, without a person",
		types:    "yoyo agent chat <agent> --message \"approve <document>\"",
		closedBy: "yoyodyne-ifd.433.21",
	},
}

// HandNeeds is the whole set, in the order a refusal names it.
func HandNeeds() []HandNeed {
	return []HandNeed{HandAmendmentDecision, HandOperationalConfiguration, HandDocumentConfirmation}
}

// Valid reports whether a need is in the set.
func (n HandNeed) Valid() bool {
	_, ok := handGaps[n]
	return ok
}

// Known reports a hand a stored record may name: a gap the harness still lacks,
// or the cap crossing it has since closed. A record written before the gap closed
// is still read as the role's decision it was, and the registry resolves it to
// whoever decides it now, rather than being read as a finding naming the
// operator for no reason.
func (n HandNeed) Known() bool { return n.Valid() || n == HandCapCrossing }

// CheckStored is Check for a record already written, which may name the cap
// crossing recorded before the harness carried that raise out itself. A record
// that was valid when written stays readable; what refuses a new one is Check,
// where a role asks.
func (n HandNeed) CheckStored() error {
	if n.Known() {
		return nil
	}
	return n.Check()
}

// Check refuses a need that is not in the set, naming the set.
func (n HandNeed) Check() error {
	if n.Valid() {
		return nil
	}
	if n == HandCapCrossing {
		return errors.New(capRaiseIsTheLeads)
	}
	names := make([]string, 0, len(HandNeeds()))
	for _, known := range HandNeeds() {
		names = append(names, string(known))
	}
	return fmt.Errorf("hand %q is not a capability the harness is known to lack; the ones it lacks are %s, and anything else a role cannot do is a defect to admit", n, strings.Join(names, ", "))
}

// capRaiseIsTheLeads is what a record naming the retired cap-crossing hand is
// told: the raise is a role's decision the harness now carries out itself.
const capRaiseIsTheLeads = `a raise of an item's cap past the development manager's own five needs no hand any more: it is the Lead Product Manager's to grant, up to five more for the item, with the "grant" action from her conversation, and the harness carries it out and tells the operator afterwards. Escalate it to her with "to": "product-manager" instead; ten raises in all is the limit no role may pass`

// Missing is what the harness cannot yet do, in words.
func (n HandNeed) Missing() string {
	return handGaps[n].missing
}

// ClosedBy is the work item that closes the gap, and empty where none is
// admitted yet.
func (n HandNeed) ClosedBy() string {
	return handGaps[n].closedBy
}

// Hand is the operator's hand on a role's decision: the person whose keyboard
// it needs, the capability the harness lacks, what he types, and the work item
// whose landing ends the need.
type Hand struct {
	// Who is the person the hand is, always the operator. It names him as a
	// keyboard rather than as an owner: the entry's owner is the role.
	Who Mover `json:"who"`
	// Need is which gap it is, from the closed set.
	Need HandNeed `json:"need"`
	// Missing is the capability the harness lacks, in words.
	Missing string `json:"missing"`
	// Types is what the operator types to carry the role's decision out.
	Types string `json:"types"`
	// ClosedBy is the work item that ends the need, absent where none is
	// admitted yet.
	ClosedBy string `json:"closed_by,omitempty"`
}

// handFor is the hand one gap needs, with the record's own command where it
// names one.
func handFor(need HandNeed, command string) *Hand {
	gap := handGaps[need]
	types := strings.TrimSpace(command)
	if types == "" {
		types = gap.types
	}
	return &Hand{Who: MoverOperator, Need: need, Missing: gap.missing, Types: types, ClosedBy: gap.closedBy}
}

// Says is the hand as the line under its head prints it, after the entry:
// whose decision it is, what to type, and what ends the need.
func (h Hand) Says(decidedBy Mover) string {
	ends := "no work item is admitted yet to give the harness that capability, which is the harness's defect to fix"
	if h.ClosedBy != "" {
		ends = "the need ends once " + h.ClosedBy + " lands"
	}
	return fmt.Sprintf("%s decision, which needs your hand because the harness has no way yet of %s: type `%s`; %s",
		decidedBy.Possessive(), h.Missing, h.Types, ends)
}

// Equal reports whether two answers say the same thing, the hand compared by
// what it holds rather than where it is kept.
func (a Answer) Equal(b Answer) bool {
	if a.Owner != b.Owner || a.Reason != b.Reason || a.Remedy != b.Remedy || a.Capability != b.Capability {
		return false
	}
	if (a.Hand == nil) != (b.Hand == nil) {
		return false
	}
	return a.Hand == nil || *a.Hand == *b.Hand
}

// IsZero reports an answer nobody gave.
func (a Answer) IsZero() bool { return a.Equal(Answer{}) }

// NeedsHand reports an answer that is a role's decision needing the operator's
// hand.
func (a Answer) NeedsHand() bool { return a.Hand != nil && !a.Owner.IsOperator() }

// decidesAmendments reports whether a role holds the capability to decide a
// change proposed to its own documents, in the shipped role bundles.
func decidesAmendments(role domain.AgentRole) bool {
	return rolecapability.MustDefault().Holds(role, capability.AmendmentDecide)
}
