package artifact

// Which changes to product documents are the operator's, and which are delegated.
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
// Which kind of change a revision is, is a judgement, and it is the owning
// role's to make and record: nothing here reads two versions of a sentence and
// decides whether they admit the same work. What is held here is what makes the
// judgement accountable. A rewording keeps the approval standing only where
// all of it is on the record: it creates or amends a document owned by the
// Lead Product Manager, it was recorded by that role, it says it is consistent
// with intent, and its reason
// opens with the work item that directed it — so a rewording nobody can trace to
// the decision behind it is not one the approval stands through. Anything short
// of that, an amendment that says it is fundamental, and one that says nothing
// at all, is an amendment the operator is asked about exactly as before: the
// default is the operator's, and it takes a recorded claim to move off it.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Intent is what a creation or amendment did to what the document intends, in
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

// Rewording reports whether one revision of this artifact is a rewording
// delegated to the Lead Product Manager, which the operator's approval stands
// through, and why not when it is not. The reason is carried so a surface that
// finds an amendment labelled consistent still counting against the approval can
// say what was missing from the record, rather than leaving the label to read as
// ignored.
func (a Artifact) Rewording(revision Revision) (bool, string) {
	owner, owned := Owner(a.Kind)
	switch {
	case revision.Action != ActionAmended && revision.Action != ActionCreated:
		return false, fmt.Sprintf("it is %s rather than a creation or amendment", revision.Action)
	case revision.Intent == IntentFundamental:
		return false, "it is recorded as a change of fundamental intent, which is the operator's to approve"
	case revision.Intent != IntentConsistent:
		return false, "it does not say whether it is consistent with intent, and a change that says nothing is the operator's to approve"
	case !owned || owner != domain.RoleProductManager:
		return false, fmt.Sprintf("it changes a %s document, which is not owned by the %s", a.Kind, domain.RoleProductManager.Title())
	case revision.By != domain.RoleProductManager:
		return false, fmt.Sprintf("it was recorded by the %s, and a consistent change to product intent is the %s's to record", revision.By.Title(), domain.RoleProductManager.Title())
	}
	if _, named := DirectingItem(revision.Reason); !named {
		return false, "its reason does not open with the work item that directed it"
	}
	return true, ""
}

// DelegatedCreation returns the creation that records existing intent under the
// Lead Product Manager's delegated authority. It supplies the approval's basis
// without inventing an approval given by the operator.
func (a Artifact) DelegatedCreation() (Revision, bool) {
	if len(a.Revisions) == 0 || a.Revisions[0].Action != ActionCreated {
		return Revision{}, false
	}
	creation := a.Revisions[0]
	delegated, _ := a.Rewording(creation)
	return creation, delegated
}
