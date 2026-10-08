package chat

// What stops the same work being admitted twice.
//
// Both doors into the queue ask this, and they ask it of the same judgement for
// the reason the admission gate is one predicate: the product manager reaches
// the direct "create" and the proposal path, and a check one of them made and
// the other did not would be duplicates arriving through whichever asked less.
//
// What each door does with the answer is different, because what is available to
// each is different. A creation is carried out and reported, with nobody to ask,
// so a creation that looks like admitted work is refused and the match is named:
// the role that asked is told which item this already is, which is what it needs
// either to act on that item instead or to say why this is not it. A proposal is
// already on its way to somebody, so nothing is refused — the resemblance is
// written onto the proposal, which stops the harness admitting it on a goal's
// authority and puts it in front of the operator with the match named.
//
// Neither ever drops it silently, which is the whole point. A guard whose finding
// reaches nobody is a guard that turns a duplicate into a mystery.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/admission"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// alreadyAdmitted reports the admitted work a candidate looks like, or why the
// question could not be answered.
//
// An unanswered question is a refusal, not a pass. Until yoyodyne-ifd.366 it was
// the other way — the caller carried on and said the check did not run, on the
// argument that losing an admission to a tracker that was briefly unavailable is
// worse than admitting a duplicate — and on 2026-09-18 the listing timed out
// under an admission and the admission went in unchecked, against a guard that
// had cost two runs to earn. What changed the answer is that the listing is now
// retried under the recovery rule: a tracker that still would not answer after
// that is not briefly unavailable, and a creation written to it on the strength
// of a guard that never ran is the duplicate the guard exists to stop. The
// refusal carries the reason, so the role can ask again once the tracker answers
// rather than describing work as admitted that was not.
//
// The listing is returned beside the matches because a creation that names a
// closed item as distinct from it is judged against the same reading: the item
// it names need not be among the matches, and its state is what decides whether
// the distinction is recorded.
func (s *Session) alreadyAdmitted(ctx context.Context, candidate admission.Candidate) ([]admission.Match, []beads.WorkItem, error) {
	if s.options.Tracker == nil {
		return nil, nil, errors.New("no work tracker is configured, so nothing could check whether this work is already admitted")
	}
	// Every item rather than the open queue. The duplicate that costs a run is a
	// duplicate of work that has already landed — a diff against the target branch
	// arithmetically cannot contain what the target branch carries — and closed
	// work is exactly what an open-queue listing leaves out.
	admitted, err := s.options.Tracker.List(ctx, "")
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", unlistedAdmittedWork, err)
	}
	return admission.Resembling(candidate, admitted), admitted, nil
}

// maxDistinctionBytes bounds the sentence a creation gives for what separates it
// from the closed item it names. It is one sentence rather than a paragraph,
// because the paragraph is the item's description and this is written beside it.
const maxDistinctionBytes = 512

// Distinction is a creation's statement that it is separate work from one
// closed item the duplicate check would match it against: the item, and one
// sentence of what is separate. It is written onto the new item, so whoever
// reads the item later reads why it was admitted beside the one it resembles.
type Distinction struct {
	ID       string `json:"id"`
	Separate string `json:"separate"`
}

// problems checks a distinction as far as one action can: it names an item, and
// it says in one bounded line what is separate. Whether the item is closed needs
// the tracker, so it is judged where the creation is carried out.
func (d Distinction) problems() []error {
	var problems []error
	switch id := strings.TrimSpace(d.ID); {
	case id == "":
		problems = append(problems, errors.New("distinct_from requires \"id\", the closed item this work is separate from"))
	default:
		if err := beads.ValidateIssueID(id); err != nil {
			problems = append(problems, fmt.Errorf("distinct_from: %w", err))
		}
	}
	switch separate := strings.TrimSpace(d.Separate); {
	case separate == "":
		problems = append(problems, errors.New("distinct_from requires \"separate\", one sentence of what is separate about this work; a distinction nobody stated is the guard switched off"))
	case strings.ContainsAny(separate, "\r\n"):
		problems = append(problems, errors.New("distinct_from \"separate\" is one sentence and cannot span lines"))
	case len(separate) > maxDistinctionBytes:
		problems = append(problems, fmt.Errorf("distinct_from \"separate\" is %d bytes, limit is %d", len(separate), maxDistinctionBytes))
	}
	return problems
}

// distinguished sets aside the item a creation named as distinct from it, or
// says why it may not. The refusal names what to do instead, for the reason a
// duplicate refusal does: a refusal that only says no is answered by asking again
// in different words.
func distinguished(matches []admission.Match, admitted []beads.WorkItem, distinction *Distinction) ([]admission.Match, admission.Match, string) {
	if distinction == nil {
		return matches, admission.Match{}, ""
	}
	remaining, distinct, err := admission.Distinguish(matches, admitted, distinction.ID)
	if err != nil {
		return matches, admission.Match{}, fmt.Sprintf("nothing was created: %s. Open work is acted on — updated or widened — and where this is genuinely separate from it, propose it so the operator decides with the match in front of them",
			singleLine(err.Error(), maxTrackerFailureBytes))
	}
	return remaining, distinct, ""
}

// distinctionNote is what a created item records about the closed item it was
// admitted beside, and is nothing on the ordinary creation that names none.
func distinctionNote(distinct admission.Match, distinction *Distinction) string {
	if distinction == nil || distinct.ID == "" {
		return ""
	}
	matched := "which the duplicate check did not match"
	if distinct.Because != "" {
		matched = "which the duplicate check matched because " + distinct.Because
	}
	return fmt.Sprintf("\n\nDistinct from %s (%s) %q, %s. What is separate: %s",
		distinct.ID, distinct.Status, singleLine(distinct.Title, maxSurveyTitleBytes), matched,
		singleLine(distinction.Separate, maxDistinctionBytes))
}

// distinctionClause is what one line about a creation says about the closed item
// it was admitted beside, folded into the summary as the report's clause is.
func distinctionClause(distinct admission.Match) string {
	if distinct.ID == "" {
		return ""
	}
	return fmt.Sprintf(", recorded as distinct from %s (%s)", distinct.ID, distinct.Status)
}

// unlistedAdmittedWork is how both doors say the duplicate check could not be
// made, so the refusal a creation carries and the question a proposal puts to
// the operator name the same gap in the same words.
const unlistedAdmittedWork = "the tracker would not list the admitted work, so nothing checked whether this is already in it and it is not admitted on a guard that never ran"

// admissionSources are the records a candidate says the work came from, as those
// records hold their identifiers rather than as anything typed them. A reference
// is resolved before it gets here, so a directive named by a prefix is matched
// against the items citing the directive rather than against the prefix.
func admissionSources(cited ...string) []string {
	var sources []string
	for _, source := range cited {
		if trimmed := strings.TrimSpace(source); trimmed != "" {
			sources = append(sources, trimmed)
		}
	}
	return sources
}

// duplicateRefusal is what a creation that looks like admitted work is told. It
// names the match, says nothing was created, and names what the role can actually
// do about it — because a refusal that only says no is one the role answers by
// asking again in different words.
//
// What it does not offer is a way to insist that open work is not a duplicate.
// Work the role believes is genuinely different from open work is proposed, and
// the operator decides with the same match in front of them. Closed work is the
// other case: the role names it in "distinct_from" with what is separate, and
// the creation is admitted with that recorded, because deciding that the build
// of a closed design is not the design is the admitter's to make.
func duplicateRefusal(verb creation, matches []admission.Match) string {
	return fmt.Sprintf("%s already looks like work the tracker holds, so nothing was created: %s. %s",
		verb.subject, admission.Describe(matches), duplicateRemedy(matches))
}

// duplicateRemedy is what to do instead. It depends on where the matched work
// got to — work still open is work to fold this into or to widen, and work that
// has closed is work already done, which is what makes admitting it again a run
// spent on a diff that cannot contain anything — and on how it was matched.
//
// A source match has a remedy the other does not, because one record genuinely
// can prompt more than one piece of work: an operator's directive routinely does,
// and the contract has always said to name it on the item that answers it. So the
// second piece of work is admitted without the citation rather than not admitted,
// and this says so. Leaving that out would turn a guard into a wall in front of
// something the role is told to do.
//
// The proposal is offered only where the match is open. A closed match that is
// genuinely separate work — the build of a design the matched item recorded, say
// — is admitted again naming it in "distinct_from", because a proposal there puts
// a decision the role's own authority covers in front of the operator as an
// approval, which is what proposal 959.1 did on 2026-09-28.
func duplicateRemedy(matches []admission.Match) string {
	// A correction that names a closed item another correction already names is
	// the same violation, so the one already admitted is widened rather than a
	// second filed beside it.
	for _, match := range matches {
		if match.Corrected == "" {
			continue
		}
		if match.Status != closedWorkItemStatus {
			return fmt.Sprintf("That correction is still open, so widen it: update %s with \"corrects\" naming the further closed items this violation covers, and name it as the correction in your audits.", match.ID)
		}
		return fmt.Sprintf("That correction has closed. Where the same violation is still there, it did not hold: admit a correction again with \"distinct_from\" naming %s and one sentence of what it missed.", match.ID)
	}
	remedy := "That work is closed, so it is already done and a run made for this one could not contain anything it does not already carry. " +
		"Say so rather than admitting it again. Where you have read it and this is genuinely separate work, admit it again with \"distinct_from\" naming that item and one sentence of what is separate, which is recorded on the new item; nothing is put to the operator for that."
	for _, match := range matches {
		if match.Status != closedWorkItemStatus {
			remedy = "Act on that item — update it, or say why this is separate work and propose it instead so the operator decides — rather than admitting this beside it."
			break
		}
	}
	for _, match := range matches {
		if match.Source != "" {
			return remedy + fmt.Sprintf(" Where %s genuinely prompted a second, separate piece of work, admit that without citing %s: the citation belongs on the one item that answers the record, and it is what this check reads.",
				match.Source, match.Source)
		}
	}
	return remedy
}

// citedReport is the collected report an admission says the work came from, and
// is the zero report where it names none, which is most admissions.
//
// It is looked up rather than taken as typed, for the reason a directive is: an
// item whose record names a report nobody filed says where the work came from and
// says something untrue, and the guard above decides from exactly these citations
// — so a citation nothing checked is a guard that silently stops working.
func (s *Session) citedReport(named string) (report.Report, error) {
	reported := strings.TrimSpace(named)
	if reported == "" {
		return report.Report{}, nil
	}
	if s.options.Reports == nil {
		return report.Report{}, errNoReports
	}
	reports, err := s.options.Reports.List()
	if err != nil {
		return report.Report{}, fmt.Errorf("read the collected reports: %w", err)
	}
	for _, collected := range reports {
		if collected.ID == reported {
			return collected, nil
		}
	}
	return report.Report{}, fmt.Errorf("no report in the pile is %s; name a report exactly as it was listed to you", reported)
}

// reportNote is what an item admitted from a report records about it, and is
// nothing at all on the ordinary admission that cites none. It carries the
// reporter's own words as well as the identifier, for the reason a directive note
// does: an item naming a record somebody has to go and open says less than one
// that says what was reported.
func reportNote(cited report.Report) string {
	if cited.ID == "" {
		return ""
	}
	return fmt.Sprintf("\n\nAdmitted from report %s, filed at %q by the %s: %s",
		cited.ID, cited.Severity, RoleTitle(cited.Role), singleLine(cited.Message, maxTrackerFailureBytes))
}

// citedClause is what one line about an admission says about the report it was
// admitted from. It is folded into the summary rather than rendered separately,
// exactly as the directive's clause is.
func citedClause(cited report.Report) string {
	if cited.ID == "" {
		return ""
	}
	return ", admitted from report " + cited.ID
}

// resemblingProposals judges each proposal against the work the tracker already
// holds, once for the whole turn. The listing is taken once rather than per
// proposal for the reason the placement check looks its references up together: a
// turn proposing three items asks the tracker one question, and every proposal in
// it is judged against the same answer.
//
// A proposal that cannot be judged is put to the operator rather than admitted.
// Nothing is refused, for the reason a resemblance refuses nothing — the proposal
// is already on its way to somebody — but a proposal the goals would otherwise
// have admitted unasked is admitted on the strength of this check as much as of
// the goal, and a listing the recovery rule could not get an answer from is a
// check that did not run. So the gap is written where a match would be, which
// stops the admission and puts the proposal in front of the operator with the
// reason named: what the failed check costs is their decision, and what
// admitting would have cost is the duplicate the guard exists to stop.
func (s *Session) resemblingProposals(ctx context.Context, proposals []Proposal) []string {
	resembling := make([]string, len(proposals))
	if len(proposals) == 0 || s.options.Tracker == nil {
		return resembling
	}
	admitted, err := s.options.Tracker.List(ctx, "")
	if err != nil {
		unchecked := unlistedAdmittedWork + ": " + singleLine(err.Error(), maxTrackerFailureBytes)
		for i := range resembling {
			resembling[i] = unchecked
		}
		return resembling
	}
	for i, proposal := range proposals {
		matches := admission.Resembling(admission.Candidate{
			Title:  strings.TrimSpace(proposal.Title),
			Parent: strings.TrimSpace(proposal.Parent),
		}, admitted)
		if len(matches) == 0 {
			continue
		}
		resembling[i] = "it looks like work already admitted: " + admission.Describe(matches)
	}
	return resembling
}
