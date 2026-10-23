package artifact

// Which changes to the product's intent are the operator's, and which are
// delegated.
//
// The operator's ruling of 2026-09-26: a change to the fundamental goals of the
// system is the operator's to approve, and anything else that stays consistent with them is
// delegated to the Lead Product Manager. The test, recorded for the architect on
// yoyodyne-77x, is what the goals would admit: a change is of fundamental intent
// if the goals would afterwards admit work they refused before, or refuse work
// they admitted; anything else is a consistent rewording or a delegated decision.
//
// Before this, every revision after an approval made the document read as
// amended since it was approved, and under `approvals.work_items: automatic`
// that put every admission naming one of its goals back to the operator. So the
// first delegated rewording — the Lead Product Manager rename on
// yoyodyne-ifd.437.11 — would have routed the whole queue under the autonomy goal
// to a person for an approval the same ruling calls a bug.
//
// The ruling covers every document of the product's intent, not only the goals:
// recording rules the operator already gave, or rewording the non-goals, changes
// what the goals admit no more than rewording a goal does. So a creation as well
// as an amendment can carry the claim, and on any document the Lead Product
// Manager owns — the brief, the goals, the non-goals, and the operating rules,
// which are exactly the kinds filed in the product's specification home.
//
// Which kind of change a revision is, is a judgement, and it is the owning
// role's to make and record: nothing here reads two versions of a sentence and
// decides whether they admit the same work. What is held here is what makes the
// judgement accountable. A revision counts as delegated only where all of it is
// on the record: it creates or amends a document of the product's intent, it was
// recorded by the Lead Product Manager, it says it is consistent with intent, and
// its reason opens with the work item that directed it — so a change nobody can
// trace to the decision behind it is not one an approval stands through or is
// recorded for. Anything short of that, a revision that says it is fundamental,
// and one that says nothing at all, is the operator's to approve exactly as
// before, whatever the approval setting for its kind says: the default is the
// operator's, and it takes a recorded claim to move off it.
//
// Two changes are the operator's whatever they are marked, because the claim
// cannot be true of them. Creating the goals or the brief states goals nothing
// admitted work under before, so a creation of either kind is never delegated.
// And a revision of one that adds or removes a goal changes what the goals
// admit by definition; that needs the document's words before and after, which
// a revision log does not hold, so it is found where a revision is written
// (goalchange.go, publication.go) rather than here.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Intent is what a creation or an amendment did to what the product intends, in
// the terms of the operator's test.
type Intent string

const (
	// IntentConsistent leaves the goals admitting and refusing exactly the work
	// they did before: a rewording, or a decision the goals already delegate.
	IntentConsistent Intent = "consistent"
	// IntentFundamental changes what the goals would admit or refuse, which is
	// the operator's to approve.
	IntentFundamental Intent = "fundamental"
)

func (i Intent) Valid() bool {
	return i == IntentConsistent || i == IntentFundamental
}

// directingItemPattern is the shape of a tracker identifier a reason opens with
// — `yoyodyne-ifd.437.11 - the autonomy goal names the Lead Product Manager` —
// the same convention a conversation's landing is recognized by. It has to carry
// a digit somewhere, because every tracker identifier does and a hyphenated
// word does not: a reason opening "re-worded for clarity" or "follow-up: ..."
// names no item, and reading it as one would keep an approval standing on a
// record nobody can follow to a decision. A quote or
// bracket ahead of it is skipped because the reason is YAML, and one with a colon
// in it is written quoted. The shape is what is checked, not that the tracker
// holds the item: this package does not read the tracker, and the reason is the
// record a person follows to it.
var directingItemPattern = regexp.MustCompile(`^["'(\[]*[A-Za-z][A-Za-z0-9]*(?:-[A-Za-z0-9]+)+(?:\.[0-9]+)*(?:$|[\s:,;)\]"'])`)

// DirectingItem returns the work item identifier a revision's reason opens with,
// and whether it opens with one.
func DirectingItem(reason string) (string, bool) {
	trimmed := strings.TrimSpace(reason)
	if !directingItemPattern.MatchString(trimmed) {
		return "", false
	}
	trimmed = strings.TrimLeft(trimmed, `"'([`)
	end := strings.IndexFunc(trimmed, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || strings.ContainsRune(`:,;)]"'`, r)
	})
	if end >= 0 {
		trimmed = trimmed[:end]
	}
	if !strings.ContainsAny(trimmed, "0123456789") {
		return "", false
	}
	return trimmed, true
}

// Rewording reports whether one revision of this artifact is a change
// delegated to the Lead Product Manager — which the operator's approval stands
// through, and which is approved as hers where it is written from her
// conversation (see publication.go) — and why not when it is not. The reason is
// carried so a surface that finds a revision labelled consistent still counting
// against the approval can say what was missing from the record, rather than
// leaving the label to read as ignored.
func (a Artifact) Rewording(revision Revision) (bool, string) {
	switch {
	case revision.Action != ActionAmended && revision.Action != ActionCreated:
		return false, fmt.Sprintf("it is %s rather than a creation or an amendment", revision.Action)
	case revision.Intent == IntentFundamental:
		return false, "it is recorded as a change of fundamental intent, which is the operator's to approve"
	case revision.Intent != IntentConsistent:
		return false, "it does not say whether it is consistent with intent, and a change that says nothing is the operator's to approve"
	case !IntentKind(a.Kind):
		return false, fmt.Sprintf("it changes a %s document, and what is delegated is a change to the product's intent", a.Kind)
	case revision.Action == ActionCreated && StatesGoals(a.Kind):
		return false, createdGoalsReason(a.Kind)
	case revision.By != domain.RoleProductManager:
		return false, fmt.Sprintf("it was recorded by the %s, and a consistent change to the product's intent is the %s's to record", revision.By.Title(), domain.RoleProductManager.Title())
	}
	if _, named := DirectingItem(revision.Reason); !named {
		return false, "its reason does not open with the work item that directed it"
	}
	return true, ""
}

// createdGoalsReason is why creating a document that states goals is the
// operator's whatever it is marked: every goal it states is one the goals did
// not admit work under before, so the mark cannot be right.
func createdGoalsReason(kind Kind) string {
	what := "a goals document"
	if kind == KindBrief {
		what = "the brief, which states what the product is for"
	}
	return fmt.Sprintf("it creates %s, and every goal a new one states admits work nothing admitted before, so it is the operator's to approve whatever it is marked", what)
}
