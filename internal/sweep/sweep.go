// Package sweep is what a role says at the end of a recurring pass over its own
// domain: what it found, what it did about each thing, and whether one turn was
// enough.
//
// A recurring task wakes a role on a cadence and nobody is watching the turn, so
// the prose it answers with reaches an operator only if something keeps it. That
// is what this is for. The fenced block is the same channel shape every other
// structured thing an agent says already uses — a report, a proposed amendment,
// an ask — for the reason that package gives: the splitting is identical, so a
// channel added later inherits the rules rather than a copy of them.
//
// Two things in it are load-bearing beyond the record. The status is how a heavy
// pass says one turn was not enough, which is what lets the harness take another
// turn rather than have the role try to fit a morning's work inside one and
// overflow whatever bounds that turn has. And every finding carries what was
// filed for it, because a fix with nothing filed is a silent repair — the thing
// the recurring sweep exists to stop being normal — and a record that could not
// tell the two apart could not show it either way.
//
// Nothing here decides anything or authorizes anything. A role acting on what it
// found does so under the authority it already holds, through the paths it
// already acts through; this is its account of having done so, and an account
// that claimed more than the role did would be caught where every other claim is
// — against the durable records the acts themselves left.
package sweep

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/fenced"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

// Fence opens the one block a swept turn may carry its account in. It is a
// distinct language tag rather than plain JSON so a sweep result can never be
// confused with JSON a role happens to be discussing.
const Fence = "```yoyodyne-sweep"

// The bounds one turn's account is held to. A pass that found forty things in
// one turn has found something systemic, and the right answer to that is the
// summary saying so rather than forty entries nobody reads — so the cap is small
// enough to force that sentence and large enough that an ordinary busy turn fits
// inside it. The block bound is the untrusted payload the whole thing is decoded
// from.
const (
	MaxFindings     = 20
	MaxQuestions    = 5
	MaxSummaryBytes = 4 << 10
	MaxTextBytes    = 2 << 10
	MaxBlockBytes   = 32 << 10
)

// MaxRecommendations bounds how many proposed changes one turn may recommend on,
// and it is also how many the harness puts to an owning role on one firing: the
// two are one number because a batch larger than the account could carry back
// would be a batch the role cannot finish, and a bound on the account alone
// would be discovered as recommendations silently missing from the record.
//
// Ten is a pass's worth of reading. Every proposal is an argument against one of
// the role's own documents, and arguing each one back with a reason means
// reading the document it names; a firing that put forty in front of her would
// get forty one-line verdicts rather than ten considered ones.
const (
	MaxRecommendations     = 10
	MaxPassRecommendations = MaxRecommendations * MaxMergedTurns
	maxRecommendationsText = "10"
)

// The bounds a whole firing's merged account is held to, which are deliberately
// not the per-turn ones above.
//
// A firing takes several turns and folds their accounts together, so a pass that
// legitimately reported the per-turn maximum on each of four turns has four times
// that many findings — and holding the merged account to the per-turn cap would
// refuse exactly the heavy pass iteration exists for. It refused it at the worst
// possible moment, too: the record is validated as it is written, so what was
// discarded was the whole durable report of the busiest passes, which is the one
// thing the recurring sweep exists to produce.
//
// So the pass bounds are what MaxMergedTurns turns at the per-turn caps come to.
// MaxMergedTurns is stated here rather than read from the configuration because
// this package is the channel and not the schedule; a test where both are visible
// keeps it at or above the largest turn bound a task may configure.
const (
	MaxMergedTurns   = 10
	MaxPassFindings  = MaxFindings * MaxMergedTurns
	MaxPassQuestions = MaxQuestions * MaxMergedTurns
)

// maxFindingsText and maxQuestionsText are the same bounds as the contract
// states them; a test keeps the numbers a role is told equal to the ones
// enforced here.
const (
	maxFindingsText  = "20"
	maxQuestionsText = "5"
)

// Status is whether the pass finished inside the turn it was given.
type Status string

const (
	// StatusComplete is a pass that is done: everything it found was dealt with
	// or written down, and there is nothing it is waiting on a further turn for.
	StatusComplete Status = "complete"
	// StatusMore is a pass with more to do than one turn holds. It is not a
	// failure and not a finding: it is the role saying so rather than trying to
	// fit the rest into a turn that will not hold it.
	StatusMore Status = "more"
)

func (s Status) Valid() bool {
	switch s {
	case StatusComplete, StatusMore:
		return true
	default:
		return false
	}
}

// Disposition is what became of one thing a pass found. The vocabulary is
// deliberately about what the role did rather than about how bad the thing is:
// a sweep is judged by whether what it found moved, and a severity word here
// would invite the account to argue its own importance instead.
type Disposition string

const (
	// DispositionFixed is a thing the role resolved itself, inside the authority
	// it already holds.
	DispositionFixed Disposition = "fixed"
	// DispositionFiled is a thing the role did not resolve and wrote down for
	// whoever owns it.
	DispositionFiled Disposition = "filed"
	// DispositionConsulted is a thing waiting on another role's ruling, which the
	// pass asked for.
	DispositionConsulted Disposition = "consulted"
	// DispositionLeft is a thing the role looked at and deliberately did nothing
	// about, with the reason in its detail. It is a real answer: a finding
	// somebody has considered and left alone is not one nobody has seen.
	DispositionLeft Disposition = "left"
)

func (d Disposition) Valid() bool {
	switch d {
	case DispositionFixed, DispositionFiled, DispositionConsulted, DispositionLeft:
		return true
	default:
		return false
	}
}

// Finding is one unresolved thing a pass found and what became of it.
type Finding struct {
	// Issue is what was found, in the words a person reads.
	Issue string `json:"issue"`
	// Disposition is what the role did about it, and Detail is how.
	Disposition Disposition `json:"disposition"`
	Detail      string      `json:"detail,omitempty"`
	// Filed names the work filed for the root cause of this finding: the
	// identifiers where the role has them, and it is empty where nothing was
	// filed. It is a field of its own rather than prose inside the detail because
	// it is the one thing a week of these reports is read for — a fix that filed
	// nothing is a repair that leaves the cause in place, and a reader must not
	// have to infer which happened from a sentence.
	Filed []string `json:"filed,omitempty"`
}

// SilentRepair reports a fix that filed nothing for its root cause. It is
// stated here rather than computed by each reader for the reason the field
// above exists: it is the question the whole record is kept to answer.
func (f Finding) SilentRepair() bool {
	return f.Disposition == DispositionFixed && len(f.Filed) == 0
}

// Validate reports every contract violation in the finding at once.
func (f Finding) Validate() error {
	var problems []error
	switch issue := strings.TrimSpace(f.Issue); {
	case issue == "":
		problems = append(problems, errors.New("issue is required"))
	case len(issue) > MaxTextBytes:
		problems = append(problems, fmt.Errorf("issue is %d bytes, limit is %d", len(issue), MaxTextBytes))
	}
	if !f.Disposition.Valid() {
		problems = append(problems, fmt.Errorf("disposition %q must be %q, %q, %q, or %q",
			f.Disposition, DispositionFixed, DispositionFiled, DispositionConsulted, DispositionLeft))
	}
	if len(f.Detail) > MaxTextBytes {
		problems = append(problems, fmt.Errorf("detail is %d bytes, limit is %d", len(f.Detail), MaxTextBytes))
	}
	for i, filed := range f.Filed {
		if strings.TrimSpace(filed) == "" {
			problems = append(problems, fmt.Errorf("filed[%d] is blank", i))
		}
		if len(filed) > MaxTextBytes {
			problems = append(problems, fmt.Errorf("filed[%d] is %d bytes, limit is %d", i, len(filed), MaxTextBytes))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid finding: %w", err)
	}
	return nil
}

// Recommended is what an owning role recommends be done with a change somebody
// proposed to one of its documents. It is a recommendation and never a
// decision: no role records a decision on a proposal, `yoyo amendment` is the
// only thing that does, and what a pass produces is the owner's argument for
// the operator to act on.
type Recommended string

const (
	// RecommendApprove is the owner arguing for the change.
	RecommendApprove Recommended = "approve"
	// RecommendDecline is the owner arguing against it, with the reason a
	// decline has to carry.
	RecommendDecline Recommended = "decline"
	// RecommendMerge is the owner saying this proposal and another ask for one
	// change, naming the other in Into: the operator carries it out as one
	// approval and one decline, since the record has no merge of its own.
	RecommendMerge Recommended = "merge"
)

func (r Recommended) Valid() bool {
	switch r {
	case RecommendApprove, RecommendDecline, RecommendMerge:
		return true
	default:
		return false
	}
}

// Recommendation is one proposed change argued back by the role that owns the
// document it names: which proposal, what the owner recommends, and why.
type Recommendation struct {
	// Proposal is the id of the proposed amendment, as the harness put it to the
	// role. A recommendation on a proposal the role was never shown, or one the
	// operator has decided since, names nothing that is waiting and is dropped
	// from the batch wherever the batch is read.
	Proposal string      `json:"proposal"`
	Verdict  Recommended `json:"verdict"`
	// Reason is why, and it is required whichever way the verdict goes: a
	// recommendation is an argument for the operator to act on, and one with no
	// reasoning asks them to decide on an assertion.
	Reason string `json:"reason"`
	// Into is the other proposal a merge folds this one into, and is required on
	// a merge and refused on anything else.
	Into string `json:"into,omitempty"`
}

// Validate reports every contract violation in the recommendation at once.
func (r Recommendation) Validate() error {
	var problems []error
	if !amendment.ValidID(strings.TrimSpace(r.Proposal)) {
		problems = append(problems, fmt.Errorf("proposal %q is not a proposed amendment's id", r.Proposal))
	}
	if !r.Verdict.Valid() {
		problems = append(problems, fmt.Errorf("verdict %q must be %q, %q, or %q", r.Verdict, RecommendApprove, RecommendDecline, RecommendMerge))
	}
	switch reason := strings.TrimSpace(r.Reason); {
	case reason == "":
		problems = append(problems, errors.New("reason is required"))
	case len(reason) > MaxTextBytes:
		problems = append(problems, fmt.Errorf("reason is %d bytes, limit is %d", len(reason), MaxTextBytes))
	}
	into := strings.TrimSpace(r.Into)
	switch {
	case r.Verdict == RecommendMerge && !amendment.ValidID(into):
		problems = append(problems, errors.New("a merge names the proposal it folds this one into, in \"into\""))
	case r.Verdict == RecommendMerge && into == strings.TrimSpace(r.Proposal):
		problems = append(problems, errors.New("a merge names a different proposal from the one it merges"))
	case r.Verdict != RecommendMerge && into != "":
		problems = append(problems, fmt.Errorf("\"into\" belongs to a merge, and this recommends %s", r.Verdict))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid recommendation: %w", err)
	}
	return nil
}

// MaxAudits bounds how many closed items one turn may account for auditing, and
// it is also the most closed items the harness lists for one pass: a listing
// longer than the account could carry back would be one the role could not
// finish. MaxPassCorrections is how many corrections one pass may say it
// admitted; the rest of a pass's violations are deferred.
const (
	MaxAudits          = 25
	MaxPassAudits      = MaxAudits * MaxMergedTurns
	MaxPassCorrections = 3
	maxAuditsText      = "25"
	maxCorrectionsText = "three"
	maxAuditGoals      = 10
)

// AuditFinding is what auditing one closed item against the standing goals
// found.
type AuditFinding string

const (
	// AuditMet is a closed item whose landed work breaks none of the standing
	// goals checked.
	AuditMet AuditFinding = "met"
	// AuditBroken is a closed item whose landed work breaks at least one of
	// them, named in the audit's detail.
	AuditBroken AuditFinding = "broken"
)

func (f AuditFinding) Valid() bool {
	return f == AuditMet || f == AuditBroken
}

// Audit is one closed work item checked against the standing goals after it
// landed: which item, which goals were checked, what was found, and what became
// of a violation — the correction admitted or widened for it, or deferred to a
// later pass because this one had admitted its three.
type Audit struct {
	// Item is the closed work item, by identifier.
	Item string `json:"item"`
	// Goals are the standing goals the item was checked against, in the words or
	// identities the goals document gives them.
	Goals   []string     `json:"goals"`
	Finding AuditFinding `json:"finding"`
	// Detail is where the landed work breaks a goal, and is required on a
	// violation: a correction nobody can trace to what it corrects is one the
	// developer who picks it up has to rediscover.
	Detail string `json:"detail,omitempty"`
	// Correction is the work item admitted or widened to correct a violation.
	// Deferred marks a violation this pass did not correct because it had
	// already admitted its three. A violation carries exactly one of the two.
	Correction string `json:"correction,omitempty"`
	Deferred   bool   `json:"deferred,omitempty"`
}

// Validate reports every contract violation in the audit at once.
func (a Audit) Validate() error {
	var problems []error
	switch item := strings.TrimSpace(a.Item); {
	case item == "":
		problems = append(problems, errors.New("item is required"))
	case len(item) > MaxTextBytes:
		problems = append(problems, fmt.Errorf("item is %d bytes, limit is %d", len(item), MaxTextBytes))
	}
	if len(a.Goals) == 0 {
		problems = append(problems, errors.New("goals names the standing goals the item was checked against, and names none"))
	}
	if len(a.Goals) > maxAuditGoals {
		problems = append(problems, fmt.Errorf("%d goals, limit is %d", len(a.Goals), maxAuditGoals))
	}
	for i, goal := range a.Goals {
		switch trimmed := strings.TrimSpace(goal); {
		case trimmed == "":
			problems = append(problems, fmt.Errorf("goals[%d] is blank", i))
		case len(trimmed) > MaxTextBytes:
			problems = append(problems, fmt.Errorf("goals[%d] is %d bytes, limit is %d", i, len(trimmed), MaxTextBytes))
		}
	}
	if !a.Finding.Valid() {
		problems = append(problems, fmt.Errorf("finding %q must be %q or %q", a.Finding, AuditMet, AuditBroken))
	}
	if len(a.Detail) > MaxTextBytes {
		problems = append(problems, fmt.Errorf("detail is %d bytes, limit is %d", len(a.Detail), MaxTextBytes))
	}
	if len(a.Correction) > MaxTextBytes {
		problems = append(problems, fmt.Errorf("correction is %d bytes, limit is %d", len(a.Correction), MaxTextBytes))
	}
	correction := strings.TrimSpace(a.Correction)
	switch a.Finding {
	case AuditBroken:
		if strings.TrimSpace(a.Detail) == "" {
			problems = append(problems, errors.New("a broken goal says where in detail"))
		}
		if (correction == "") == !a.Deferred {
			problems = append(problems, errors.New("a broken goal names the correction admitted or widened for it, or says it is deferred, and not both"))
		}
	case AuditMet:
		if correction != "" || a.Deferred {
			problems = append(problems, errors.New("an item that breaks no goal has nothing to correct or defer"))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid audit: %w", err)
	}
	return nil
}

// Result is one turn's account of a recurring pass.
type Result struct {
	Status  Status `json:"status"`
	Summary string `json:"summary"`
	// Findings are what the pass found, and are empty on a pass that found
	// nothing — which on a healthy harness is most of them, and is the answer
	// this record most wants to be able to state plainly.
	Findings []Finding `json:"findings,omitempty"`
	// Questions are what the pass needs a person to settle. They are separate
	// from the findings because they are the only part of a report that asks for
	// anything: a report with no questions needs no attention, which is what
	// makes reading these at leisure possible at all.
	Questions []string `json:"questions,omitempty"`
	// Recommendations are what an owning role recommends on the changes
	// proposed to its documents that the harness put to it on this pass. They
	// are the batch the operator decides from, and they are empty on every pass
	// of a role that owns no documents or was put none.
	Recommendations []Recommendation `json:"recommendations,omitempty"`
	// Audits are the closed work items this pass checked against the standing
	// goals, and are empty on every pass that was handed none to check.
	Audits []Audit `json:"audits,omitempty"`
}

// Validate reports every contract violation in the result at once.
//
// It is the contract a whole firing's account is held to, so its volume bounds
// are the pass ones: this is what the durable record validates against, and a
// record is written per firing rather than per turn. What one turn may send is a
// tighter question, asked by validateTurn where a turn's block is decoded.
func (r Result) Validate() error {
	var problems []error
	if !r.Status.Valid() {
		problems = append(problems, fmt.Errorf("status %q must be %q or %q", r.Status, StatusComplete, StatusMore))
	}
	switch summary := strings.TrimSpace(r.Summary); {
	case summary == "":
		problems = append(problems, errors.New("summary is required"))
	case len(summary) > MaxSummaryBytes:
		problems = append(problems, fmt.Errorf("summary is %d bytes, limit is %d", len(summary), MaxSummaryBytes))
	}
	if len(r.Findings) > MaxPassFindings {
		problems = append(problems, fmt.Errorf("%d findings in one pass, limit is %d", len(r.Findings), MaxPassFindings))
	}
	for i, finding := range r.Findings {
		if err := finding.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("findings[%d]: %w", i, err))
		}
	}
	if len(r.Questions) > MaxPassQuestions {
		problems = append(problems, fmt.Errorf("%d questions in one pass, limit is %d", len(r.Questions), MaxPassQuestions))
	}
	for i, question := range r.Questions {
		switch trimmed := strings.TrimSpace(question); {
		case trimmed == "":
			problems = append(problems, fmt.Errorf("questions[%d] is blank", i))
		case len(trimmed) > MaxTextBytes:
			problems = append(problems, fmt.Errorf("questions[%d] is %d bytes, limit is %d", i, len(trimmed), MaxTextBytes))
		}
	}
	if len(r.Recommendations) > MaxPassRecommendations {
		problems = append(problems, fmt.Errorf("%d recommendations in one pass, limit is %d", len(r.Recommendations), MaxPassRecommendations))
	}
	for i, recommendation := range r.Recommendations {
		if err := recommendation.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("recommendations[%d]: %w", i, err))
		}
	}
	if len(r.Audits) > MaxPassAudits {
		problems = append(problems, fmt.Errorf("%d audits in one pass, limit is %d", len(r.Audits), MaxPassAudits))
	}
	for i, audit := range r.Audits {
		if err := audit.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("audits[%d]: %w", i, err))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid sweep result: %w", err)
	}
	return nil
}

// Corrections counts the distinct corrections this pass's audits name. The
// harness refuses a fourth correction as it is admitted; this is what the
// account says it did.
func (r Result) Corrections() int {
	seen := map[string]bool{}
	for _, audit := range r.Audits {
		if correction := strings.TrimSpace(audit.Correction); correction != "" {
			seen[correction] = true
		}
	}
	return len(seen)
}

// validateTurn holds one turn's account to what a turn may send, which is the
// tighter of the two contracts and the one the role is told about. Everything
// else about the account is the same question either way, so it defers to
// Validate for the rest rather than restating it.
func (r Result) validateTurn() error {
	var problems []error
	if len(r.Findings) > MaxFindings {
		problems = append(problems, fmt.Errorf("%d findings in one turn, limit is %d", len(r.Findings), MaxFindings))
	}
	if len(r.Questions) > MaxQuestions {
		problems = append(problems, fmt.Errorf("%d questions in one turn, limit is %d", len(r.Questions), MaxQuestions))
	}
	if len(r.Recommendations) > MaxRecommendations {
		problems = append(problems, fmt.Errorf("%d recommendations in one turn, limit is %d", len(r.Recommendations), MaxRecommendations))
	}
	if len(r.Audits) > MaxAudits {
		problems = append(problems, fmt.Errorf("%d audits in one turn, limit is %d", len(r.Audits), MaxAudits))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid sweep result: %w", err)
	}
	return r.Validate()
}

// SilentRepairs counts the fixes this pass filed nothing for. It is what a
// report says out loud, and what a week of them is read against.
func (r Result) SilentRepairs() int {
	silent := 0
	for _, finding := range r.Findings {
		if finding.SilentRepair() {
			silent++
		}
	}
	return silent
}

// Merge folds a later turn of the same pass into this one. The findings
// accumulate and the last turn's status and summary stand, because a pass that
// took three turns found everything all three of them found and finished the way
// the last one says it did.
//
// It bounds what it accumulates at the pass caps, and that bounding is the point
// rather than a detail: what it produces is written straight into a durable
// record that validates as it is written, so an unbounded merge is a merge that
// can make the record unwritable — and the report it would then lose is the whole
// account of the busiest pass. Bounding here keeps the earlier findings, which
// are the ones a later turn was told not to repeat, and says in the summary how
// many were dropped, because a silently shortened list reads as a pass that found
// less than it did.
func (r Result) Merge(next Result) Result {
	merged := Result{Status: next.Status, Summary: next.Summary}
	if strings.TrimSpace(merged.Summary) == "" {
		merged.Summary = r.Summary
	}
	findings := append(append([]Finding(nil), r.Findings...), next.Findings...)
	questions := append(append([]string(nil), r.Questions...), next.Questions...)
	recommendations := append(append([]Recommendation(nil), r.Recommendations...), next.Recommendations...)
	audits := append(append([]Audit(nil), r.Audits...), next.Audits...)
	dropped := droppedCounts{}
	if len(findings) > MaxPassFindings {
		dropped.findings = len(findings) - MaxPassFindings
		findings = findings[:MaxPassFindings]
	}
	if len(questions) > MaxPassQuestions {
		dropped.questions = len(questions) - MaxPassQuestions
		questions = questions[:MaxPassQuestions]
	}
	if len(recommendations) > MaxPassRecommendations {
		dropped.recommendations = len(recommendations) - MaxPassRecommendations
		recommendations = recommendations[:MaxPassRecommendations]
	}
	if len(audits) > MaxPassAudits {
		dropped.audits = len(audits) - MaxPassAudits
		audits = audits[:MaxPassAudits]
	}
	merged.Findings = findings
	merged.Questions = questions
	merged.Recommendations = recommendations
	merged.Audits = audits
	merged.Summary = noteDropped(merged.Summary, dropped)
	return merged
}

// droppedCounts is what the pass bounds cut from a merged account.
type droppedCounts struct {
	findings, questions, recommendations, audits int
}

func (d droppedCounts) any() bool {
	return d.findings > 0 || d.questions > 0 || d.recommendations > 0 || d.audits > 0
}

// noteDropped says in the summary what the pass bounds cut, and keeps the summary
// inside its own bound while doing it. A note that pushed the summary past what
// the record accepts would lose the report it exists to preserve.
func noteDropped(summary string, dropped droppedCounts) string {
	if !dropped.any() {
		return summary
	}
	note := fmt.Sprintf("(This pass reached its bound of %d findings and %d questions; %d finding(s) and %d question(s) from its later turns are not listed.)",
		MaxPassFindings, MaxPassQuestions, dropped.findings, dropped.questions)
	if dropped.recommendations > 0 {
		note = fmt.Sprintf("(This pass reached its bound of %d findings, %d questions, and %d recommendations; %d finding(s), %d question(s), and %d recommendation(s) from its later turns are not listed.)",
			MaxPassFindings, MaxPassQuestions, MaxPassRecommendations, dropped.findings, dropped.questions, dropped.recommendations)
	}
	if dropped.audits > 0 {
		note += fmt.Sprintf(" (It also reached its bound of %d audits; %d audit(s) from its later turns are not listed.)", MaxPassAudits, dropped.audits)
	}
	joined := strings.TrimSpace(summary)
	if joined != "" {
		joined += " "
	}
	joined += note
	if len(joined) <= MaxSummaryBytes {
		return joined
	}
	// The note is what a reader most needs of the two, so it is the part kept
	// whole: the summary is cut back far enough to leave room for it.
	// The cut falls on a rune boundary: the record is read back through JSON,
	// which turns each byte of a broken rune into three, and a summary that grew
	// past its bound that way is a record refused on read.
	room := MaxSummaryBytes - len(note) - 2
	if room <= 0 {
		return oneline.Bound(note, MaxSummaryBytes)
	}
	cut := min(len(summary), room)
	for cut > 0 && cut < len(summary) && !utf8.RuneStart(summary[cut]) {
		cut--
	}
	return strings.TrimSpace(summary[:cut]) + " " + note
}

// Extract splits a reply into what the role said and the account it gave of its
// pass. The account comes only from the fenced block: prose describing what was
// found is not an account, and a block the contract does not accept is refused
// rather than half-read.
//
// A reply carrying no block is not a failure here. It is a role that answered in
// prose, which is a thing a person reading the conversation can still make sense
// of; what it costs is the structure, and the caller says so rather than losing
// the turn over it.
//
// A reply carrying more than one block is not a failure either, and this is
// where the sweep differs from the other channels, which refuse a second block.
// The contract is one block, at the end of the answer. A role that wrote one on
// each round of a reply — the harness hands back what a round asked for, and the
// answer goes on after it — has not failed: its work was done, and a pass whose
// decisions were taken must not lose its record over the shape of the reply. So
// the last block is the account — a role that wrote "more" and then "complete"
// settled on the second — and the note says the reply carried more than one, for
// the record to state beside the account rather than in place of it. It is a
// note and never a failure.
func Extract(reply string) (prose string, result *Result, note string, err error) {
	block, count, err := fenced.SplitLast(reply, Fence, "sweep")
	if err != nil {
		return block.Before, nil, "", err
	}
	if !block.Found {
		return block.Before, nil, "", nil
	}
	result, err = Decode(block.Payload)
	if err != nil {
		return block.Before, nil, "", err
	}
	if count > 1 {
		note = fmt.Sprintf("the reply carried %d sweep blocks, and the last of them is the account recorded; extra blocks are not a failure", count)
	}
	return block.Rest, result, note, nil
}

// Decode strictly decodes the block payload. Unknown fields, trailing content,
// and oversized input are refused rather than tolerated: what is written into a
// durable report has to be exactly what the role wrote.
//
// This is a validator of what an agent just said rather than a reader of a
// durable record, so the reasoning that made the run-record listings tolerant
// does not reach it. There is no older build on the other side of this — the
// block was written seconds ago, by a role this build told what to write — and a
// key this build does not know is a role that answered something other than what
// it was asked. The refusal reaches the caller as an error naming the block, and
// the pass fails visibly rather than recording a sweep with part of the answer
// in it.
func Decode(payload string) (*Result, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("decode sweep: the sweep block is empty")
	}
	if len(trimmed) > MaxBlockBytes {
		return nil, fmt.Errorf("decode sweep: block is %d bytes, limit is %d", len(trimmed), MaxBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var decoded Result
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode sweep: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode sweep: unexpected trailing content after the sweep result")
	}
	// Held to the per-turn contract, which is the tighter of the two and the one
	// the role was told: this is one turn's block, whatever a whole pass may
	// accumulate across several of them.
	if err := decoded.validateTurn(); err != nil {
		return nil, err
	}
	return &decoded, nil
}

// Contract is what a role is told about this block when the harness wakes it.
// It is here beside the rules rather than beside the message that carries it,
// so the bounds a role is given and the bounds enforced on what it sends back
// are one statement.
func Contract() string {
	return strings.Join([]string{
		"Write this block once, at the very end of your answer, and nothing after it. Where your answer asks for something the harness carries out and hands back — tracker actions, memory writes, reads, research, a question to another role — your answer goes on after the results, so leave the block out of that part and write it only when you ask for nothing more. If you do write it more than once, the last one is your account:",
		"",
		Fence,
		`{"status":"complete|more","summary":"what this pass found, in a sentence or two","findings":[{"issue":"what you found","disposition":"fixed|filed|consulted|left","detail":"what you did and why","filed":["work you filed for its root cause"]}],"questions":["what only a person can settle"]}`,
		"```",
		"",
		`"complete" is a pass with nothing left to do. "more" is a pass with more than this turn holds — say it rather than rushing the rest, and the harness gives you another turn.`,
		`A pass that found nothing carries no findings and says so in the summary; that is the ordinary result and it is worth stating plainly.`,
		`Every fix carries the work you filed for its root cause in "filed". A fix that files nothing is a silent repair, and the report says so.`,
		`A finding must leave a trace outside this block on the same pass: a memory written, your lane report changed where you keep one, a report filed, or work admitted. This block and the conversation are lost to you at its next compaction. A pass whose findings leave none of those is recorded as untraced, shown as a problem that is yours to move, and your next pass is told which findings they were.`,
		"At most " + maxFindingsText + " findings and " + maxQuestionsText + " questions in one turn: a pass that found more than that has found something systemic, and the summary is where that is said.",
		terms.StandingGoals,
		"The summary, the findings, and the questions are read by a person. " + terms.ItemNaming,
		terms.PersonWriting,
		"A question is for what only the operator can decide or do, never for an approval. " + terms.DecideAndReport,
	}, "\n")
}

// ReportRequest recovers an account without asking the role to do its work again.
func ReportRequest() string {
	return strings.Join([]string{
		"Your previous reply omitted its closing report block. This is the only request for it on this pass. Reply with the block alone, accounting for that reply's findings and actions already taken. Do not repeat any action or perform new work; all earlier writes, reports, admissions and costs remain recorded.",
		Fence,
		`{"status":"complete|more","summary":"what the preceding reply found","findings":[{"issue":"what you found","disposition":"fixed|filed|consulted|left","detail":"what you already did and why","filed":["work already filed"]}],"questions":["what only a person can settle"],"recommendations":[{"proposal":"amendment-id already considered","verdict":"approve|decline|merge","reason":"why","into":"amendment-id (a merge only)"}],"audits":[{"item":"closed beads-id already audited","goals":["standing goal checked"],"finding":"met|broken","detail":"where it breaks the goal","correction":"beads-id admitted or widened","deferred":false}]}`,
		"```",
		"Omit empty lists. Include recommendations only for proposals already considered in the preceding reply, and audits only for closed items it already audited.",
	}, "\n")
}

// RecommendationContract is what an owning role is told, beside the contract
// above, when the harness has put proposed changes to its documents in front of
// it. It is a separate paragraph rather than part of Contract because most
// recurring passes are put no proposals, and a role told about a field it has
// nothing to put in it is a role that fills it with something.
//
// The wording holds the line the amendment record holds: the role recommends
// and the operator decides. A block that said "approved" would be read by
// somebody as a decision, and the whole reason the decision is recorded from
// the command line and by nobody else is that a role must not be able to
// settle an argument about its own document by having the last word in it.
func RecommendationContract() string {
	return strings.Join([]string{
		`Your block also carries "recommendations": one entry for every proposed change put to you on this pass, and none for a proposal you were not shown:`,
		"",
		`"recommendations":[{"proposal":"amendment-id","verdict":"approve|decline|merge","reason":"why, in a sentence or two","into":"amendment-id (a merge only: the proposal this one folds into)"}]`,
		"",
		`These are recommendations and not decisions. Nothing you say here changes a document or settles a proposal: the operator reads the batch and records each decision with "yoyo amendment approve" or "yoyo amendment decline", under your authority. "approve" argues for the change, "decline" argues against it, and "merge" says two proposals ask for one change and names the other; every one carries the reason, because the operator acts on the argument rather than the verdict.`,
		"At most " + maxRecommendationsText + " recommendations in one turn, which is the most the harness puts to you on one pass.",
	}, "\n")
}

// AuditContract is what a pass handed closed work to audit is told, beside the
// contract above. It is separate for the reason RecommendationContract is: most
// passes are handed no closed work, and a role told about a field it has
// nothing to put in fills it with something.
func AuditContract() string {
	return strings.Join([]string{
		`Your block also carries "audits": one entry for every closed item you checked on this pass, at most ` + maxAuditsText + ` in one turn:`,
		"",
		`"audits":[{"item":"beads-id","goals":["each standing goal you checked it against"],"finding":"met|broken","detail":"where the landed work breaks the goal (broken only)","correction":"the beads-id admitted or widened to correct it","deferred":true}]`,
		"",
		`A "broken" finding says where in "detail", and carries exactly one of "correction" — the correction you admitted at priority 0 naming the closed item in "corrects", or the open correction you widened to it — or "deferred": true. One pass admits at most ` + maxCorrectionsText + ` corrections: take the violations shared by the most closed items first, widen an open correction rather than filing a second, and mark the rest deferred so the next pass takes them. A "met" finding carries neither.`,
	}, "\n")
}
