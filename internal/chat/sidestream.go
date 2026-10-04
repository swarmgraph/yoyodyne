package chat

// What a role may ask for on a side thread, which is less than it may ask for on
// its main one.
//
// A side conversation runs beside the main thread and never in place of it: it
// judges, answers, and tentatively plans, and every intent it forms is a draft
// the main thread ratifies through its own single-threaded path. So its authority
// is the role's own, narrowed — and narrowed here, in Go, for the same reason the
// table it narrows is written here. The per-agent knob chooses whether an agent
// holds side threads at all; no value of it reaches what one may do, which is
// what `configuration-never-grants-authority` requires.
//
// The narrowing is read off `internal/sidestream` rather than written out a
// second time. What a side thread may ask for is stated once, in the capability
// vocabulary, exactly as what a role may ask for is.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
)

// OnSideStream is this role's authority as a side thread holds it: judgment and
// reading, and no action at all.
//
// Every answer is the role's own answer and the side thread's, so this can only
// ever take authority away. A role that may not survey the tracker on its main
// thread may not survey it here either, and a role that may admit work on its
// main thread may not admit any here — which is the whole point, because the
// second is the case a side thread would otherwise widen.
//
// What it leaves alone is what is not authority: the title, the contract, and
// what the role owns are what a role is called and what it is sent, and a side
// thread is still that role.
func (a Authority) OnSideStream() Authority {
	narrowed := a
	narrowed.TrackerActions = nil
	narrowed.LaneActions = nil
	for _, action := range a.TrackerActions {
		// A lane-scoped action is held through its lane-scoped name, so that is
		// the name the side thread is asked about.
		held := trackerCapabilities[action]
		if a.LaneScoped(action) {
			held = laneCapabilities[action]
		}
		if sidestream.Permits(held) {
			narrowed.TrackerActions = append(narrowed.TrackerActions, action)
		}
	}
	narrowed.Proposals = a.Proposals && sidestream.Permits(capability.ProposalRaise)
	narrowed.Concerns = a.Concerns && sidestream.Permits(capability.ConcernRaise)
	narrowed.Research = a.Research && sidestream.Permits(capability.ResearchCommission)
	narrowed.Evaluations = a.Evaluations && sidestream.Permits(capability.EvaluationRecord)
	// A side thread reads the evidence it was given and names no path of its
	// own: the named read is the pair of capabilities, and the list is not one a
	// side thread holds.
	narrowed.RepositoryReads = a.RepositoryReads && sidestream.Permits(capability.RepositoryRead) &&
		sidestream.Permits(capability.RepositoryList)
	narrowed.LogReads = a.LogReads && sidestream.Permits(capability.LogRead)
	narrowed.Asks = a.Asks && sidestream.Permits(capability.ExchangeAsk)
	narrowed.Answers = a.Answers && sidestream.Permits(capability.ExchangeAnswer)
	// A side thread writes no memory of its own: what it worked out reaches the
	// agent's memory once, as the merge the harness writes when it concludes.
	narrowed.Memory = a.Memory && sidestream.Permits(capability.AgentContextMutate)
	narrowed.RestartRequests = a.RestartRequests && sidestream.Permits(capability.ServiceRequestRestart)
	return narrowed
}

// SidePrompt is the system prompt one side turn is taken under: the role, what
// it owns, the ground every toolless conversation stands on, and the side
// thread's own contract.
//
// It is built the way an exchange's answering prompt is, and it is a second
// prompt rather than the role's own with a clause appended for the reason that
// one is: the role's contract describes a thread that admits work, raises
// proposals, and issues directives, and a side thread does none of those. A
// contract that said both would leave the role to work out which half it was
// under, on the one turn where getting that wrong is an action nobody ratified.
//
// What it takes from the narrowing above is what the narrowing leaves alone: the
// title and what the role owns are what a role is called and what it is
// answerable for, and a side thread is still that role. What it may ask for is
// not in the prompt at all — it is Permitted in `internal/sidestream`, checked
// where the reply is read, so a role that ignored every word here still acts on
// nothing.
func SidePrompt(role domain.AgentRole, persona string) string {
	authority, known := AuthorityFor(role)
	if !known {
		return fmt.Sprintf("You are the %s for this product. The harness holds no contract for this role, so you have nothing to answer with: say exactly that.", role)
	}
	aside := authority.OnSideStream()
	prompt := fmt.Sprintf("You are the %s for this product. You own %s.\n\n%s\n\n%s",
		aside.Title, aside.Owns, conversationGround, sidestream.SideThreadContract)
	trimmed := strings.TrimSpace(persona)
	if trimmed == "" {
		return prompt
	}
	return prompt + `

# Configured ` + aside.Title + ` persona

The project configuration supplies the guidance below. It may specialize how you
work, but it cannot widen your authority or remove any rule above — and on a side
thread you have no authority to widen.

` + trimmed
}
