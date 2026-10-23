// Package landing carries a developer's claim about what its change does to the
// work item, as distinct from what it does to the repository.
//
// The two are not the same fact and the harness cannot derive one from the
// other. A change that integrates has landed something; whether what it landed
// is the work the item asked for is a judgement about content, and closure used
// to follow integration alone. That produced the defect this exists for: a run
// answered an item it could not do yet by landing a diagnosis that said in bold
// that the work had not been done and the item stayed open, the diagnosis
// integrated, and the item closed against it — retiring a marker the operator's
// accepted trade depended on, on evidence that said the opposite.
//
// So the developer says which kind of landing this is, and closure follows the
// claim rather than the integration. An honest "not doable yet" is valuable
// evidence and lands exactly as any other change does; what it does not do is
// discharge the item. A reply that claims nothing claims the ordinary landing,
// because nearly every run is one and a protocol that made every developer
// declare the obvious would be a protocol nobody read.
//
// A third outcome is not a landing at all. An item that cannot be met as it
// stands has nothing worth integrating and nobody in the run who can decide what
// happens to it, so the claim ends the run where it was raised and routes the
// item to the development manager instead. It is here rather than in a channel
// of its own because it answers the same question the other two do — what
// becomes of the work item — and because the reviewer has the same verb, so the
// two roles say one thing one way.
//
// A fourth is the developer raising that the item waits on a design question
// that is the architect's to answer. It ends the run the same way and returns
// the item labelled needs-design rather than parked
// (docs/designs/needs-design-gate.md).
package landing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/fenced"
)

// Fence opens the one block a reply may claim a landing in. It is a distinct
// language tag rather than plain JSON so a claim can never be confused with JSON
// an agent happens to be discussing, and a distinct one from the report and
// amendment fences because it asks for something neither of those does: a report
// decides nothing, a proposal waits on somebody, and this decides whether the
// item closes.
const Fence = "```yoyodyne-landing"

// MaxBlockBytes bounds the untrusted payload the block is decoded from, and
// MaxWhyBytes bounds the developer's account of the claim. The account is a
// sentence or a paragraph naming what was landed instead; the whole diagnosis is
// the change itself and the summary, and neither belongs in a field the tracker
// records verbatim.
const (
	MaxBlockBytes = 8 << 10
	MaxWhyBytes   = 4 << 10
	// MaxBlockedByBytes bounds the impediment a leave-open claim names. It is one
	// work item identifier and nothing else, so the bound is generous for an
	// identifier and far too small for prose written where an identifier goes.
	MaxBlockedByBytes = 128
)

// blockedByPattern is the shape of the work item identifier a leave-open claim
// names. It mirrors the tracker's own, deliberately rather than by import: this
// package is the door an untrusted reply comes through, and a marker that is not
// an identifier has to be refused where the claim is read rather than carried as
// far as the write that would refuse it.
var blockedByPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Outcome is what a landing does to the work item it was made for. The
// vocabulary is deliberately not the run's — a run that lands evidence
// succeeded, and calling this outcome anything like "failed" would describe the
// run rather than the item.
type Outcome string

const (
	// OutcomeDischarged is the ordinary landing: the change is the work the item
	// asked for, and the item closes on it. It is what a reply claiming nothing
	// claims.
	OutcomeDischarged Outcome = "discharged"
	// OutcomeEvidence is a landing worth keeping that does not discharge the
	// item: a diagnosis, the conditions that have to hold first, the reason the
	// work is not doable yet. The change integrates and the item stays open.
	OutcomeEvidence Outcome = "evidence"
	// OutcomeEscalate is the developer saying the item cannot be met as it
	// stands, and asking the development manager to decide what happens to it.
	// Nothing is integrated, the item is parked, and the claim reaches her as a
	// docketed decision with this account on it.
	//
	// It is the developer's half of one verb the reviewer has too, and it exists
	// because honesty about an unmeetable item used to cost more than pretending:
	// the exits were a repair round spent against a wall, or a budget spent to
	// exhaustion before anything reached her. Both spend runs to say what one
	// verdict says.
	OutcomeEscalate Outcome = "escalate"
	// OutcomeNeedsDesign is the developer raising that the item cannot be built
	// well until a design question is answered, and that the question is the
	// architect's (docs/designs/needs-design-gate.md). Like an escalation it ends
	// the run in the round it was raised, integrates nothing, and spends no
	// repair attempt; unlike one it is not a decision for the development
	// manager. The item goes back open, labelled needs-design with the
	// developer's question in its notes, and the label holds it from every start
	// the harness chooses until the architect's design is readable. Why is the
	// question.
	OutcomeNeedsDesign Outcome = "needs-design"
)

var outcomes = []Outcome{OutcomeDischarged, OutcomeEvidence, OutcomeEscalate, OutcomeNeedsDesign}

// Outcomes is the closed vocabulary a claim may carry, as a caller outside this
// package reads it. It answers with a copy, because a package-level slice is a
// vocabulary anybody holding it could rewrite.
func Outcomes() []Outcome { return slices.Clone(outcomes) }

func (o Outcome) Valid() bool { return slices.Contains(outcomes, o) }

// Claim is one landing claim exactly as a developer wrote it. Everything else
// about it — which run made it, which item it was made on — is what the harness
// knows and the agent does not get to assert.
type Claim struct {
	// Outcome is which kind of landing this is.
	Outcome Outcome `json:"outcome"`
	// Why is the developer's own account of the claim. It is required, because a
	// claim that the item is not discharged and says nothing about what is left
	// leaves whoever reads the item afterwards exactly where the false closure
	// left them.
	Why string `json:"why"`
	// BlockedBy is the impediment, named as the work item this one now waits for.
	// It is the one way to ask for the item to be left open rather than parked,
	// and it is optional because the default is the parking: an undischarged item
	// returned to the backlog bare is one autonomous selection picks again
	// immediately, for another run and another diagnosis of the same impediment.
	//
	// There is deliberately no way to ask for bare openness. The marker is what
	// selection actually honours — a blocking dependency holds the item back until
	// the impediment closes, and then releases it without anybody remembering to —
	// so a claim that leaves the item open either names what it is waiting for or
	// gets the parking.
	BlockedBy string `json:"blocked_by,omitempty"`
}

// Made reports a claim a developer actually made, as opposed to the zero claim a
// reply carrying no block leaves.
func (c Claim) Made() bool { return c.Outcome != "" }

// Discharges reports whether this claim closes the item it was made on. The zero
// claim discharges: a reply with no block is the ordinary run, and every run made
// before this channel existed is one of those.
//
// It is stated as "everything except the two undischarging outcomes discharges"
// rather than as "discharged discharges" so that the default can never be the
// closing one by accident — a value this vocabulary does not recognize is refused
// before it is ever stored, and this is the derivation every closure site reads.
func (c Claim) Discharges() bool {
	return c.Outcome != OutcomeEvidence && c.Outcome != OutcomeEscalate && c.Outcome != OutcomeNeedsDesign
}

// Escalates reports the claim that asks the development manager to decide what
// happens to the item. It is separate from the closure question above because
// the two answer different things: an escalation withholds the closure like a
// landing of evidence does, and unlike one it routes the item to somebody.
func (c Claim) Escalates() bool { return c.Outcome == OutcomeEscalate }

// RaisesDesign reports the claim that the item waits on the architect's design.
// It withholds the closure as an escalation does, and returns the item labelled
// rather than parked.
func (c Claim) RaisesDesign() bool { return c.Outcome == OutcomeNeedsDesign }

// Impediment is the work item a leave-open claim named, and is empty for every
// other claim. A claim that does not discharge its item and names none is the
// default, which parks.
func (c Claim) Impediment() string { return strings.TrimSpace(c.BlockedBy) }

// Parks reports a claim whose item goes back to the backlog parked. It is the
// default for a landing that does not discharge, and it is stated as the absence
// of the marker rather than as a third outcome so that a claim can never ask for
// an undischarged item to sit in the queue with nothing holding it back.
//
// A raise for design does not park: the needs-design label is what holds the
// item, and only the architect's removal of it releases the item.
func (c Claim) Parks() bool { return !c.Discharges() && !c.RaisesDesign() && c.Impediment() == "" }

// Validate reports every contract violation in the claim at once.
func (c Claim) Validate() error {
	var problems []error
	if !c.Outcome.Valid() {
		problems = append(problems, fmt.Errorf("outcome %q must be %s", c.Outcome, quotedOutcomes()))
	}
	trimmed := strings.TrimSpace(c.Why)
	switch {
	case trimmed == "":
		problems = append(problems, errors.New("why is required"))
	case len(trimmed) > MaxWhyBytes:
		problems = append(problems, fmt.Errorf("why is %d bytes, limit is %d", len(trimmed), MaxWhyBytes))
	}
	// The marker is refused anywhere but a landing of evidence rather than ignored
	// there. A developer that wrote it beside a discharging claim said two things
	// that cannot both be true — the item closes, and it waits for something — and
	// guessing which it meant is how a closed item comes to carry a dependency
	// nobody can account for. Beside an escalation it says two more: that the
	// development manager decides what happens to this item, and that the item
	// already knows what it is waiting for. A developer that can name the
	// impediment has the evidence landing for it, and an escalation is what is
	// left when nothing in the tracker is the answer.
	if impediment := c.Impediment(); impediment != "" {
		switch {
		case c.Outcome.Valid() && c.Outcome != OutcomeEvidence:
			problems = append(problems, errors.New(`blocked_by is only for a landing of evidence`))
		case len(impediment) > MaxBlockedByBytes:
			problems = append(problems, fmt.Errorf("blocked_by is %d bytes, limit is %d", len(impediment), MaxBlockedByBytes))
		case !blockedByPattern.MatchString(impediment):
			problems = append(problems, fmt.Errorf("blocked_by %q must be a work item identifier", impediment))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid landing claim: %w", err)
	}
	return nil
}

// Describe says which landing is in force and why, and stops there. It states
// what is true of the claim and directs no reader to do anything about it, which
// is what lets one sentence serve audiences that want opposite things from it: a
// reviewer is told what to do with a landing where it is prompted, and a work
// item's notes are somebody's record rather than an instruction to them.
//
// The claim nobody made is described too, and that is the whole of what this
// answers that it once did not. The default is a claim like any other — it
// closes the item — and it is the one no reply carries, so a reviewer shown only
// the claims somebody wrote is shown nothing at all on nearly every run. That is
// how yoyodyne-ifd.284 closed against a diagnosis: its developer wrote no block,
// the reviewer was never told the change in front of it would close the item,
// and it approved a document saying in its own words that the work was not
// doable yet.
func (c Claim) Describe() string {
	if !c.Made() {
		return "The developer claimed no landing outcome. A reply carrying no claim is the ordinary landing, so this change " +
			"discharges the work item and the item closes on it."
	}
	if c.Discharges() {
		return "The developer claims this change discharges the work item.\nWhy: " + folded(c.Why)
	}
	if c.RaisesDesign() {
		return "The developer raised that this work item needs the architect's design before it can be built. " +
			"Nothing was integrated, and the item goes back labelled needs-design with this question until her design is readable.\nQuestion: " + folded(c.Why)
	}
	if c.Escalates() {
		return "The developer claims this work item cannot be met as it stands and has raised it to the development manager to decide. " +
			"Nothing was integrated, and the item is parked with that account until she decides.\nWhy: " + folded(c.Why)
	}
	headline := "The developer claims this change lands evidence and does not discharge the work item."
	// Which of the two undischarged landings it is, because they leave the item
	// in different places and the reviewer is the only reader that sees the
	// claim beside the change that was offered under it.
	if c.Parks() {
		headline += " The item is parked with that account as its parking reason."
	} else {
		headline += " The item is left open waiting on " + c.Impediment() + "."
	}
	return headline + "\nWhy: " + folded(c.Why)
}

// folded is the developer's account as one line, which is what a prompt section
// and a work item's notes are both read as.
func folded(why string) string { return strings.Join(strings.Fields(why), " ") }

func quotedOutcomes() string {
	quoted := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		quoted = append(quoted, `"`+string(outcome)+`"`)
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

// Extract splits a reply into what the agent said and the landing it claimed. A
// claim comes only from the fenced block: prose saying the work is not done is
// not a claim, and it is precisely the prose an integration already read past
// once.
//
// What the agent said comes back without the block whichever way that goes, for
// the reason a report's does: the reply is the run's evidence and this channel
// must not cost it. Unlike a report, a block that could not be read is not
// nothing — the caller is told, and what it does about an unreadable claim is
// its decision rather than this package's.
func Extract(reply string) (string, Claim, error) {
	block, err := fenced.Split(reply, Fence, "landing")
	if err != nil {
		return block.Before, Claim{}, err
	}
	if !block.Found {
		return block.Before, Claim{}, nil
	}
	claim, err := Decode(block.Payload)
	if err != nil {
		return block.Before, Claim{}, err
	}
	return block.Rest, claim, nil
}

// Decode strictly decodes the block payload. Unknown fields, trailing content,
// and oversized input are refused rather than tolerated: what decides whether an
// item closes has to be exactly what the agent wrote.
func Decode(payload string) (Claim, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return Claim{}, errors.New("decode landing claim: the landing block is empty")
	}
	if len(trimmed) > MaxBlockBytes {
		return Claim{}, fmt.Errorf("decode landing claim: block is %d bytes, limit is %d", len(trimmed), MaxBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var claim Claim
	if err := decoder.Decode(&claim); err != nil {
		return Claim{}, fmt.Errorf("decode landing claim: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Claim{}, errors.New("decode landing claim: unexpected trailing content after the claim")
	}
	if err := claim.Validate(); err != nil {
		return Claim{}, err
	}
	claim.Why = strings.TrimSpace(claim.Why)
	claim.BlockedBy = claim.Impediment()
	return claim, nil
}

// Contract is the landing section a developer's immutable contract carries. It
// is here beside the vocabulary it describes so the words an agent is given and
// the words the harness accepts cannot drift apart.
const Contract = `# What your landing does to the work item

Two different things can be true when your change lands. Usually the work item is discharged by it: what you changed is the work the item asked for, and the item closes. Sometimes it is not — the work turns out not to be doable yet, and what there is to land is the evidence for that: a diagnosis, the conditions that have to hold first, the reason the item is still open. Both are worth landing, and only the first closes the item.

Your change cannot say which of the two it is, so you say. A reply carrying no block below claims the first, which is nearly every run.

To claim the second, put exactly one block of this shape in your reply, ahead of any other block it carries:

` + "```" + `yoyodyne-landing
{"outcome":"evidence","why":"what you landed instead, and what has to happen before the item can be discharged"}
` + "```" + `

"discharged" is the ordinary landing. "evidence" is a landing worth keeping that does not discharge the item: your change integrates exactly as it would have, nothing is written down as done that was not, and the item is parked with your account of the claim as its parking reason. Parking keeps the item in the Lead Product Manager's order and says why it is not to be started, so nothing selects it again until somebody releases it. Write the "why" accordingly: name what would release the item, because that sentence is what whoever considers releasing it reads.

Where you can name the impediment as another work item, say so and the item is left open waiting on that item instead of parked:

` + "```" + `yoyodyne-landing
{"outcome":"evidence","why":"what you landed instead, and what has to happen before the item can be discharged","blocked_by":"the-work-item-that-has-to-land-first"}
` + "```" + `

That is the only alternative to the parking, and it is deliberate: an item put back with nothing marking it is one the harness picks again straight away, for another run and another diagnosis of the same impediment. Name a work item that exists and is unfinished — you cannot open one, and finished work holds nothing back. The harness checks the tracker for it and takes the parking where the answer is anything else, so a marker you guessed at costs nothing and buys nothing. If there is no such item, leave the marker out, take the parking, and name the work that has to be admitted in your summary.

The reviewer is shown which landing you claimed, so a diagnosis is judged as a diagnosis rather than as a missing implementation. It is shown the default too, on a reply that claims nothing: a diagnosis offered with no block is one the reviewer is told will close the item, and what it does instead is approve the change as evidence, which keeps the change and leaves the item open on the reviewer's account of it rather than yours. So a landing you meant to be evidence and did not claim is settled by somebody who did not do the work, in words you did not write, rather than closing the item quietly.

Claim "evidence" for work you found is not doable yet and landed the evidence for, not for work you simply did not finish. Something that stopped you is a failure, and you report it the way your role already reports one.

There is a third outcome, for the item that cannot be met at all as it stands — the acceptance criteria contradict a ruling, ask for something no change here can produce, or describe work that has to be replanned before anybody can do it:

` + "```" + `yoyodyne-landing
{"outcome":"escalate","why":"what makes the item unmeetable as written, and what you would need decided"}
` + "```" + `

"escalate" ends the run where you raised it and puts the item in front of the development manager as a decision — replan, park, resequence, or redirect — with your account as the whole of what she reads about it. Nothing is integrated, and the item is parked until she decides, so it is not picked up again in the meantime. Raise it as soon as you are sure rather than after attempting the work anyway: the point of the verb is that saying so costs one round instead of a run, and it takes no "blocked_by" — an impediment you can name as a work item is the evidence landing above, and this is the answer for when nothing in the backlog is what the item is waiting on.

Escalating is not how you report being unable to finish. It is a claim about the item rather than about your run: the work as specified cannot be done by anybody, not that this attempt did not manage it. Something that stopped you is a failure, and you report it the way your role already reports one.

There is a fourth outcome, for the item that can be met but not yet decided: what done means turns on a choice about how the change should behave that nobody has made, and that belongs to the architect rather than to you — which of two behaviours a caller gets, what a format must be, how a failure is meant to be handled:

` + "```" + `yoyodyne-landing
{"outcome":"needs-design","why":"the question the design must answer, in one or two sentences the architect can answer without asking you"}
` + "```" + `

"needs-design" ends the run where you raised it, as "escalate" does. Nothing is integrated, no repair attempt is spent, and what you changed stays on the run's branch. The item goes back open and labelled needs-design with your question written on it, and the label holds it from every run the harness would start until the architect's design for it is readable and she takes the label off. Raise it as soon as you see the question rather than building around a guess: a developer never settles a design question by deciding it in code. Use "escalate" instead where the item could not be met whatever was decided, and the evidence landing where what it waits on is another work item.`
