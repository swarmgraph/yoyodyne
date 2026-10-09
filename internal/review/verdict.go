// Package review holds the provider-neutral structured reviewer verdict.
// Review semantics live here rather than in a backend adapter so every
// provider is decoded and validated against the same contract.
package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
)

// MaxVerdictBytes bounds the untrusted verdict payload a reviewer may return.
const MaxVerdictBytes = 64 << 10

type Decision string

const (
	DecisionApprove Decision = "approve"
	DecisionRepair  Decision = "repair"
	// DecisionEscalate is the reviewer saying the work item cannot be met as it
	// stands, whatever change is put in front of it: the acceptance criteria
	// contradict a ruling, ask for something no change can produce, or describe
	// work that has to be replanned first. It approves nothing and asks for
	// nothing, because there is nothing a repair round could do about it.
	//
	// It is the reviewer's half of one verb the developer has too. Before it, a
	// reviewer that saw an unmeetable item had only the expensive exits: repair
	// verdicts against a wall until the item's rounds ran out, which is how
	// yoyodyne-ifd.100.1 spent three runs and six rounds against criteria a ruling
	// forbade. The verdict ends the run where it was raised and routes the item to
	// the development manager as a decision instead.
	DecisionEscalate Decision = "escalate"
)

type Severity string

const (
	SeverityBlocker Severity = "blocker"
	SeverityMajor   Severity = "major"
	SeverityMinor   Severity = "minor"
)

// Disposition is what the reviewer says about a finding beside how serious it
// is. Severity answers how bad the problem is; a disposition answers whether this
// change is the place to fix it. They are separate words because they are
// separate questions, and folding one into the other is what made a severity
// label decide the review budget: before this, a reviewer that wanted to say
// "right, but not this change's business" had only "minor" to say it with, so a
// single minor finding became a free round, and a real defect labelled minor was
// a free round as well as a small-looking one (yoyodyne-ifd.359).
//
// A finding with no disposition is an ordinary finding: something this change
// has to do. That is nearly every finding, which is why the field is optional.
type Disposition string

const (
	// DispositionOutOfScope marks a finding the reviewer stands behind and does
	// not hold this change to: it lies outside what the work item asked for, or it
	// is too trivial to be worth another round. It is independent of severity — a
	// real defect in code the item never touched is out of scope and may well be
	// major.
	DispositionOutOfScope Disposition = "out_of_scope"
)

// Approval is what an approving verdict approves. It exists because approving a
// change and discharging the work item it was made for are two different facts,
// and only the reviewer sees both the change and what it was offered as: the
// developer's claim decides the closure by default, and a reviewer that reads a
// diagnosis as a diagnosis would otherwise have nowhere to put that but its own
// prose. yoyodyne-ifd.284 is the whole of the case — approved with "offered as
// evidence rather than implementation" written in its summary, and closed on the
// developer's unwritten default anyway.
//
// It says nothing about whether the change is good. Both approvals promote the
// change; what differs is only what becomes of the item afterwards.
type Approval string

const (
	// ApprovesImplementation is the ordinary approval: the change is the work the
	// item asked for, and the item closes on it.
	ApprovesImplementation Approval = "implementation"
	// ApprovesEvidence approves a change that lands something worth keeping and is
	// not the work — a diagnosis, the conditions that have to hold first. It
	// integrates exactly as any approval does and discharges nothing.
	ApprovesEvidence Approval = "evidence"
)

// The vocabularies above as lists, which is what validates a verdict
// below: a value permitted here and a value the contract accepts are one list
// rather than two that can drift.
//
// Anything added to any of them has to be added to the durable schema that stores it,
// which keeps its own copy so a record is checked against what a record may hold
// rather than against this version of the harness. That is a real trap — the
// addition is accepted here and refused at the moment a run tries to store what
// it decided — so the two are held together by
// TestTheDurableSchemaStoresEveryVerdictTheReviewerCanProduce rather than by
// whoever adds the value remembering.
var (
	decisions  = []Decision{DecisionApprove, DecisionRepair, DecisionEscalate}
	severities = []Severity{SeverityBlocker, SeverityMajor, SeverityMinor}
	approvals  = []Approval{ApprovesImplementation, ApprovesEvidence}

	dispositions = []Disposition{DispositionOutOfScope}
)

// Decisions, Severities, Approvals, and Dispositions are those vocabularies as a caller
// outside this package reads them, each answered with a copy so nothing holding
// one can rewrite the contract.
func Decisions() []Decision { return slices.Clone(decisions) }

func Severities() []Severity { return slices.Clone(severities) }

func Approvals() []Approval { return slices.Clone(approvals) }

func Dispositions() []Disposition { return slices.Clone(dispositions) }

// Location optionally anchors a finding to a place in the reviewed change.
type Location struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
}

// Finding is one actionable observation the developer can act on.
type Finding struct {
	Severity Severity `json:"severity"`
	// Disposition is empty for a finding this change has to act on, and
	// DispositionOutOfScope for one the reviewer names without holding the change
	// to it. It is what the review budget reads; the severity is not.
	Disposition Disposition `json:"disposition,omitempty"`
	Message     string      `json:"message"`
	Location    *Location   `json:"location,omitempty"`
	// Absent names a path this finding claims is missing. The harness checks it
	// against repository evidence rather than inferring absence from a patch.
	Absent string `json:"absent,omitempty"`
}

// Verdict is the reviewer's approve-or-repair decision on one change, and — on
// an approval of one work item's change — what that approval approves.
type Verdict struct {
	Decision Decision `json:"decision"`
	// Approves is what an approval approves: the work the item asked for, or
	// evidence that does not discharge it. It is empty on a repair, which
	// approves nothing and closes nothing, and empty at branch scope, which has no
	// work item to discharge. An approval that has to carry it and does not is
	// refused where the scope is known rather than here, because this type is the
	// same at both scopes.
	Approves Approval `json:"approves,omitempty"`
	Summary  string   `json:"summary"`
	// Fixtures are the test-data files the evidence named as kept out of the
	// patch that this verdict accounts for, by the path the evidence named. A
	// change whose fixtures alone outgrow the patch bound is approvable — the
	// code is presented whole and the fixtures are listed with their size and
	// digest and delivered beside the patch — and this is what makes that
	// approval say what it covered: a verdict that names them has read the
	// listing and judged the delivery, where one that does not is an approval
	// over evidence nobody can tell it saw.
	//
	// It is empty where the evidence named no omitted fixture, which is nearly
	// every change. An approval of a change that named some and does not list
	// them is asked for once more rather than settled.
	Fixtures []string  `json:"fixtures,omitempty"`
	Findings []Finding `json:"findings,omitempty"`
}

// UndecodableVerdictError reports a reply that could not be read as a verdict at
// all: empty, past the size bound, not a single JSON document, or malformed. It
// is deliberately distinct from a verdict that was read and then refused,
// because a reply nothing can parse says nothing whatever about the change --
// that review was never made, and asking for it again costs one review where
// giving up costs the whole change. A verdict that was read and refused has
// already said what it thinks.
type UndecodableVerdictError struct {
	cause error
}

func (e UndecodableVerdictError) Error() string {
	return "decode review verdict: " + e.cause.Error()
}

func (e UndecodableVerdictError) Unwrap() error {
	return e.cause
}

func undecodable(cause error) error {
	return UndecodableVerdictError{cause: cause}
}

// Decode decodes a validated verdict from a bounded JSON document and names
// every field the closed schema does not define.
//
// Strictness is kept where it protects the contract and dropped where it only
// punishes. The document must be one JSON object within the size bound, and
// every field the schema does name keeps exactly its validation: an unknown
// decision, an unknown severity, or a missing required field is still refused.
// An unknown *extra* field is not a corrupted verdict, it is a verbose one --
// verdicts are model-generated and a model asked for structured output will
// occasionally embellish the schema -- so the extras are named for the caller to
// record as evidence of drift and the verdict is decoded without them.
//
// A verdict written inside one Markdown code fence is read as the bare verdict
// it encloses, for the same reason: the fence is formatting around the document
// rather than a different document, and the contract's ban on it is about how
// the reply should look, not about what it decides. Only a fence around the
// whole reply is lifted — prose beside a fenced verdict is still not a single
// JSON document — so nothing this accepts was ambiguous about what the
// reviewer decided.
func Decode(data []byte) (Verdict, []string, error) {
	if len(data) == 0 {
		return Verdict{}, nil, undecodable(errors.New("input is empty"))
	}
	if len(data) > MaxVerdictBytes {
		return Verdict{}, nil, undecodable(fmt.Errorf("input is %d bytes, limit is %d", len(data), MaxVerdictBytes))
	}
	data = unfence(data)

	decoder := json.NewDecoder(bytes.NewReader(data))
	var verdict Verdict
	if err := decoder.Decode(&verdict); err != nil {
		return Verdict{}, nil, undecodable(err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Verdict{}, nil, undecodable(errors.New("unexpected trailing content after the verdict"))
	}
	// The drift is reported whatever becomes of the verdict beside it: a reviewer
	// that embellished the schema did so whether or not what it decided survives
	// validation.
	unknown := unknownFields(data)
	if err := verdict.Validate(); err != nil {
		return Verdict{}, unknown, err
	}
	return verdict, unknown, nil
}

// unfence returns what one code fence around the whole reply encloses, and the
// reply unchanged where it is not exactly that. The opening line is three or
// more backticks followed by nothing or by "json", in any case, and the closing
// line is backticks alone; anything outside the pair leaves the reply as it
// was, so the decoder refuses it as it always has.
func unfence(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	opening, body, found := bytes.Cut(trimmed, []byte("\n"))
	if !found {
		return data
	}
	fence := opening[:len(opening)-len(bytes.TrimLeft(opening, "`"))]
	if len(fence) < 3 {
		return data
	}
	if info := strings.TrimSpace(string(opening[len(fence):])); info != "" && !strings.EqualFold(info, "json") {
		return data
	}
	body = bytes.TrimRight(body, " \t\r\n")
	closing := bytes.LastIndexByte(body, '\n')
	last := body[closing+1:]
	if !bytes.Equal(bytes.TrimSpace(last), fence) {
		return data
	}
	if closing < 0 {
		return nil
	}
	enclosed := body[:closing]
	// A fence nested inside the body would make "the whole reply is one fenced
	// block" a guess about where the block ends, so it is not lifted.
	if bytes.Contains(enclosed, fence) {
		return data
	}
	return enclosed
}

// The closed schema, named once so the decoder and the drift walk below cannot
// disagree about what the contract defines.
var (
	verdictFields  = []string{"decision", "approves", "summary", "fixtures", "findings"}
	findingFields  = []string{"severity", "disposition", "message", "location", "absent"}
	locationFields = []string{"file", "line"}
)

// unknownFields names every field the closed schema does not define, in the
// order a reader would come across them: the verdict's own, then each finding's,
// then that finding's location's. It is deliberately tolerant of anything it
// cannot re-read, because the typed decode above has already settled whether the
// document is a verdict at all; this walk only says what else was in it.
func unknownFields(data []byte) []string {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil
	}
	unknown := unknownKeys(document, "", verdictFields)
	var findings []json.RawMessage
	if err := json.Unmarshal(document["findings"], &findings); err != nil {
		return unknown
	}
	for index, raw := range findings {
		var finding map[string]json.RawMessage
		if err := json.Unmarshal(raw, &finding); err != nil {
			continue
		}
		prefix := fmt.Sprintf("findings[%d].", index)
		unknown = append(unknown, unknownKeys(finding, prefix, findingFields)...)
		var location map[string]json.RawMessage
		if err := json.Unmarshal(finding["location"], &location); err != nil {
			continue
		}
		unknown = append(unknown, unknownKeys(location, prefix+"location.", locationFields)...)
	}
	return unknown
}

// unknownKeys names the fields of one object that the schema does not define.
// They are sorted because Go's map iteration is not ordered and the recorded
// evidence has to read the same way every time it is produced.
func unknownKeys(fields map[string]json.RawMessage, prefix string, known []string) []string {
	var unknown []string
	for name := range fields {
		if slices.Contains(known, name) {
			continue
		}
		unknown = append(unknown, prefix+name)
	}
	slices.Sort(unknown)
	return unknown
}

// Validate reports every contract violation in the verdict at once.
func (v Verdict) Validate() error {
	var problems []error
	if !v.Decision.Valid() {
		problems = append(problems, fmt.Errorf("decision %q must be %q, %q or %q", v.Decision, DecisionApprove, DecisionRepair, DecisionEscalate))
	}
	if strings.TrimSpace(v.Summary) == "" {
		problems = append(problems, errors.New("summary is required"))
	}
	if v.Decision == DecisionRepair && len(v.Findings) == 0 {
		problems = append(problems, errors.New("repair requires at least one finding"))
	}
	// An escalation is not held to a finding, and deliberately. A finding is what
	// the change has to do before it is approved, and this verdict's whole content
	// is that no change to this change would help — so what it owes is the summary
	// every verdict owes, which is what the development manager reads and the whole
	// of what she decides from.
	// The vocabulary is closed like the two above it, and for the same reason: what
	// this field decides is whether a work item closes, so a word nothing
	// recognizes must be refused where the verdict is read rather than guessed at
	// by the settlement. Its absence is not refused here — a repair carries none,
	// and a branch review has no item to discharge — and where an approval must
	// carry one that is enforced against the scope, which this type does not know.
	if v.Approves != "" && !v.Approves.Valid() {
		problems = append(problems, fmt.Errorf("approves %q must be %q or %q", v.Approves, ApprovesImplementation, ApprovesEvidence))
	}
	// A repair or an escalation that says what it approves is not refused for it.
	// Neither approves anything or closes anything, so the field decides nothing
	// there and is never recorded; refusing a verbose verdict would only cost the
	// change a review, which is the trade the decoder above already made about a
	// field the schema does not name at all.
	for i, finding := range v.Findings {
		if err := finding.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("findings[%d]: %w", i, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid review verdict: %w", errors.Join(problems...))
	}
	return nil
}

// TrivialResidue reports findings whose whole content is one observation the
// reviewer disposed of as out of scope.
//
// It is the reviewer's vocabulary answering a question about budgets, which is
// why it lives here rather than where the budgets are: the disposition is a word
// this package defines, and a caller comparing strings of its own would be
// keeping a private copy of this contract.
//
// It reads the disposition and never the severity. The reviewer is asked for
// the disposition as the answer to exactly this question — does the change have
// to do this — where a severity is its estimate of how bad something is, and a
// budget decided by that estimate made a real defect labelled minor cost nothing
// (yoyodyne-ifd.359). A single minor finding with no disposition is therefore
// charged like any other repair.
//
// One, rather than any number of them. A repair whose residue is a single note
// the reviewer has said is not this change's work is the reviewer saying the work
// is right and naming one thing beside it — the end of the argument with a note
// attached, which is why the item is not charged a round for it. Two notes is a
// list, and a list is the reviewer still arguing. Where the line sits is a
// judgement rather than a law, and this is where it is written down.
func TrivialResidue(findings []Finding) bool {
	return len(findings) == 1 && findings[0].Disposition == DispositionOutOfScope
}

// Resolve returns the decision a valid verdict actually supports, rejecting one
// that contradicts its own findings. Approval cannot carry a blocker or major
// finding, because a change that still needs that work is not approved
// whatever the reviewer labelled it. Repair stays authoritative for any valid
// finding, including a purely minor one, and so does an escalation: a reviewer
// that named what is wrong with the change and then said the item cannot be met
// at all has said both, and the finding does not contradict the second.
func (v Verdict) Resolve() (Decision, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	if v.Decision == DecisionRepair || v.Decision == DecisionEscalate {
		return v.Decision, nil
	}
	var actionable []string
	for _, finding := range v.Findings {
		if finding.Severity == SeverityBlocker || finding.Severity == SeverityMajor {
			actionable = append(actionable, string(finding.Severity))
		}
	}
	if len(actionable) > 0 {
		return "", fmt.Errorf("contradictory review verdict: approve carries %d %s finding(s) that require repair", len(actionable), strings.Join(uniqueStrings(actionable), " and "))
	}
	return DecisionApprove, nil
}

// Validate reports every contract violation in the finding at once.
func (f Finding) Validate() error {
	var problems []error
	if !f.Severity.Valid() {
		problems = append(problems, fmt.Errorf("severity %q must be %q, %q, or %q", f.Severity, SeverityBlocker, SeverityMajor, SeverityMinor))
	}
	// Closed for the reason the severity is: the disposition decides whether a
	// round is charged, so a word nothing recognizes is refused here rather than
	// read by the budget as either answer.
	if f.Disposition != "" && !f.Disposition.Valid() {
		problems = append(problems, fmt.Errorf("disposition %q must be %q or omitted", f.Disposition, DispositionOutOfScope))
	}
	if strings.TrimSpace(f.Message) == "" {
		problems = append(problems, errors.New("message is required"))
	}
	if f.Location != nil {
		if err := f.Location.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("location: %w", err))
		}
	}
	if f.Absent != "" {
		if _, err := canonicalAbsentPath(f.Absent); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

func canonicalAbsentPath(claimed string) (string, error) {
	// Spaces can be part of a Git filename; cleaning separators and dot segments
	// must not turn a claim about that file into a claim about a different path.
	clean := path.Clean(claimed)
	if strings.TrimSpace(claimed) == "" || path.IsAbs(claimed) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsAny(claimed, "\\\x00") {
		return "", fmt.Errorf("absent path %q must be inside the repository", claimed)
	}
	return clean, nil
}

// Validate rejects locations that cannot point at a place in the change.
func (l Location) Validate() error {
	var problems []error
	if strings.TrimSpace(l.File) == "" {
		problems = append(problems, errors.New("file is required"))
	}
	if l.Line < 0 {
		problems = append(problems, fmt.Errorf("line %d cannot be negative", l.Line))
	}
	return errors.Join(problems...)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func (d Decision) Valid() bool {
	return slices.Contains(decisions, d)
}

func (s Severity) Valid() bool {
	return slices.Contains(severities, s)
}

func (a Approval) Valid() bool {
	return slices.Contains(approvals, a)
}

func (d Disposition) Valid() bool {
	return slices.Contains(dispositions, d)
}

// Discharges reports whether an approval closes the work item it was made for.
// It is stated as "everything except evidence discharges" for the reason the
// developer's claim is: the answer that closes an item must never be reached by
// a value nobody recognized, and an unrecognized one is refused before it is
// stored.
func (a Approval) Discharges() bool { return a != ApprovesEvidence }

// IncompleteApprovalError reports an approval of one work item's change that did
// not say what it approves. The verdict was read and is not wrong about the
// change — this is the reviewer answering everything except the one question
// that decides whether the item closes — so it is distinct from a reply nothing
// could decode and from a verdict that contradicts itself, and the caller asks
// once more rather than deciding the closure from an answer it does not have.
type IncompleteApprovalError struct{}

func (IncompleteApprovalError) Error() string {
	return "the reviewer approved without saying whether it approves the implementation or evidence"
}

// UnaccountedFixturesError reports an approval of a change whose test data the
// patch bound kept out, where the verdict did not say which of those fixtures it
// accounted for. It is the third refusal that asks for the verdict again rather
// than ending the run, and for the same reason the two above it do: the reviewer
// answered about the change and left out the one thing that says what the
// approval covered, and a built, checked, approved change is not worth losing
// over a list the reviewer can write in one more turn.
//
// It exists because narrowing the refusal on omissions — a change whose fixtures
// alone outgrow the bound is approvable — takes away the crude guarantee that an
// approval covered everything. What replaces it is the reviewer saying so.
type UnaccountedFixturesError struct {
	// Fixtures are the omitted fixtures the verdict did not name, in the order
	// the evidence listed them.
	Fixtures []string
}

func (e UnaccountedFixturesError) Error() string {
	return fmt.Sprintf("the reviewer approved a change whose test data the patch bound kept out without saying it accounted for %s",
		strings.Join(e.Fixtures, ", "))
}

// MisplacedEscalationError reports an escalation raised by a review that has no
// work item to raise it about. The verdict was read and the reviewer may well be
// right about the change; what it has no answer for is where the escalation
// would go — a branch review judges commits whose items it was never given, and
// the decision this verb routes is a decision about one item.
//
// The contract does not offer the word at that scope, so this is a reviewer that
// went outside it rather than one the harness asked an impossible question. It is
// its own error because it is a different fact from a verdict nothing could
// decode and from one that contradicts itself.
type MisplacedEscalationError struct{}

func (MisplacedEscalationError) Error() string {
	return "the reviewer escalated a review that has no work item to escalate"
}
