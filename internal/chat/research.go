package chat

// Gathering evidence from outside the repository, on the product manager's
// behalf.
//
// The role has no network and gets none. What it has is the same arrangement it
// has with the work tracker: it names what it wants, the harness performs it,
// records it, tells the operator, and hands back what came of it. That is what
// keeps a capability that reaches the outside world from being a tool the role
// holds — nothing here lets it choose what runs, where the command reaches, or
// how often, and all three of those are the operator's, in configuration.
//
// What comes back is untrusted and is delivered as such. A search result is a
// stranger's prose arriving inside a prompt, so it is framed as evidence about
// the world and never as instruction, exactly as the repository documents and
// the tracker's own text already are.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/research"
)

// Research is the bounded evidence-gathering capability a conversation performs
// on a role's behalf. It is satisfied by research.Runner.
//
// What is permitted comes from the same object that performs it, rather than
// from a second copy of the policy beside it: what the role is offered and what
// would actually be run can then never disagree.
type Research interface {
	Permitted() research.Policy
	Search(ctx context.Context, queries []research.Query) ([]research.Finding, error)
}

// maxResearchRounds bounds how many times one operator message may be answered
// with research. Asking, reading what came back, and asking a better question is
// the point of the capability; a conversation that spends a message searching
// its way around a question is not, and every round costs the operator real
// money outside this machine.
const maxResearchRounds = 2

// maxRetainedFindings bounds the research a recorded evaluation carries. It is
// comfortably more than the rounds above can retrieve, so an ordinary evaluation
// keeps everything that was gathered toward it, and it stops a long conversation
// from accumulating an unbounded pile into one record.
const maxRetainedFindings = 12

// ResearchError reports that a turn carried a research block the harness could
// not read. Like the proposal and concern errors it is not a broken
// conversation: the turn completed, the answer is real, and nothing was
// retrieved. What is lost is whatever that block was trying to ask, which is why
// it is never silently treated as a reply that asked nothing.
type ResearchError struct {
	Err error
}

func (e *ResearchError) Error() string {
	return "the Lead Product Manager asked for research the harness cannot read: " + e.Err.Error()
}

func (e *ResearchError) Unwrap() error { return e.Err }

// researchOffered reports whether this conversation can actually perform
// research: the role may ask for it, something is wired to perform it, and the
// project has permitted at least one source.
func (s *Session) researchOffered() bool {
	if !s.authority().Research || s.options.Research == nil {
		return false
	}
	return s.options.Research.Permitted().Enabled()
}

// renderResearchSources tells the role what it may ask this turn. The sources
// are delivered with the turn rather than written into the contract because
// which sources exist is the project's own state and it moves: a contract naming
// them would be immutable policy describing something configuration decides.
//
// A role that may not ask for research at all is told nothing, rather than being
// told there is nothing: those are different, and only one of them is worth the
// context.
func (s *Session) renderResearchSources() string {
	if !s.authority().Research {
		return ""
	}
	if s.options.Research == nil {
		return "# Research sources available to you\n\nThis conversation has no research capability wired to it, so the research block is refused and nothing you ask would reach anything. Say plainly that you have no evidence source available when an idea would have needed one, rather than answering from memory as though you had checked.\n\n"
	}
	return research.Offer(s.options.Research.Permitted())
}

// performResearch puts one round of questions to the sources they name and
// returns what to hand back to the role. Nothing here fails the turn: a capability
// that is not wired, a budget that is spent, and a source that would not answer
// are all things the role has to be told about so it can say it could not find
// out, and none of them is a reason to lose the reply that carried the question.
func (s *Session) performResearchWithoutToolAudit(ctx context.Context, queries []research.Query, rounds *int) ([]research.Finding, string) {
	if s.options.Research == nil {
		return nil, "no research capability is configured for this conversation, so nothing was asked"
	}
	if !s.options.Research.Permitted().Enabled() {
		return nil, "this project has configured no research sources, so nothing was asked"
	}
	if *rounds >= maxResearchRounds {
		return nil, fmt.Sprintf("one message gathers evidence at most %d time(s), and this one has; nothing further was asked", maxResearchRounds)
	}
	*rounds++
	findings, err := s.options.Research.Search(ctx, queries)
	if err != nil {
		return nil, singleLine(err.Error(), maxTrackerFailureBytes)
	}
	// A capability that answered with nothing at all is reported as nothing
	// retrieved rather than delivered as an empty section. A role that asked and
	// was handed silence concludes there was nothing to find, which is the one
	// conclusion it must never draw from a capability that misbehaved.
	if len(findings) == 0 {
		return nil, "the research sources returned nothing for the questions asked"
	}
	s.retain(findings)
	return findings, ""
}

// retain keeps what was gathered so a recorded evaluation can carry the research
// it rests on. It is drained when an evaluation is recorded, so the findings
// travel with the recommendation they were gathered for rather than with every
// later one as well.
func (s *Session) retain(findings []research.Finding) {
	s.researched = append(s.researched, findings...)
	if len(s.researched) > maxRetainedFindings {
		s.researched = s.researched[len(s.researched)-maxRetainedFindings:]
	}
}

// takeResearch returns what has been gathered since the last evaluation was
// recorded and forgets it.
func (s *Session) takeResearch() []research.Finding {
	taken := s.researched
	s.researched = nil
	return taken
}

// ResearchRound is what one round of gathering evidence did, for the operator
// reading what a reply spent their money on. It is reported rather than put to
// them: it already happened, exactly as a tracker action did.
type ResearchRound struct {
	// Findings are what came back, one per question asked, in the order they were
	// asked. A question that produced nothing usable is here too, saying so.
	Findings []research.Finding `json:"findings,omitempty"`
	// Problem is why this round retrieved nothing at all: no capability, no
	// permitted source, or a budget already spent.
	Problem string `json:"problem,omitempty"`
}

// Render describes one round of research for an operator reading what happened.
// Everything in it but the harness's own line came from outside, so each finding
// is indented under the round rather than printed at the margin.
func (r ResearchRound) Render() string {
	var rendered strings.Builder
	if r.Problem != "" {
		rendered.WriteString("[research] nothing was retrieved\n")
		rendered.WriteString(indent(r.Problem))
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "[research] %d question(s) put to the configured sources\n", len(r.Findings))
	for _, finding := range r.Findings {
		rendered.WriteString(indent(finding.Describe()))
	}
	return rendered.String()
}
