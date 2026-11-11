package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
)

// The program manager's row of the authority table is held to what its design
// excludes, one refusal at a time, rather than to a copy of the row: a
// capability arriving in its bundle that lets any of these through fails here.
func TestTheProgramManagerIsRefusedWhatItsDesignExcludes(t *testing.T) {
	t.Parallel()

	authority, known := AuthorityFor(domain.RoleProgramManager)
	if !known {
		t.Fatal("AuthorityFor(program-manager) reports no authority; the sixth role holds a conversation")
	}
	if authority.Title != "program manager" {
		t.Errorf("the program manager is titled %q, want the name written in full", authority.Title)
	}
	if !slices.Contains(ConversationalRoles(), domain.RoleProgramManager) {
		t.Error("the program manager is not among the roles an operator can address")
	}

	session := &Session{}
	session.state.Role = domain.RoleProgramManager
	triage := func(decision string) TrackerAction {
		action := TrackerAction{Action: actionTriage, ID: "yoyodyne-ifd.1", Run: "run-1", Decision: decision, Reason: "because"}
		if decision == "cross" {
			action.Budget = "review round"
		}
		return action
	}
	for _, refused := range []struct {
		what   string
		action TrackerAction
	}{
		{"close", TrackerAction{Action: actionClose, ID: "yoyodyne-ifd.1", Reason: "done"}},
		{"retire", TrackerAction{Action: actionRetire, ID: "yoyodyne-ifd.1", Reason: "not wanted"}},
		{"a repair of stale state", TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.1", Reason: "stale"}},
		{"handling a report", TrackerAction{Action: actionHandle, Reason: "read"}},
		{"a brake decision", TrackerAction{Action: actionBrake, Reason: "release"}},
		{"a decision to resolve a conflict with the team's branch", TrackerAction{Action: actionResolveConflict, ID: "yoyodyne-ifd.1", Reason: "its owner knows the files"}},
		{"a repair decision", triage("repair")},
		{"a re-run decision", triage("rerun")},
		{"a re-scope decision", triage("rescope")},
		{"a re-arm decision", triage("rearm")},
		{"a wait decision", triage("wait")},
		{"an escalation", triage("escalate")},
		{"a cap crossing", triage("cross")},
	} {
		if authority.MayAct(refused.action.Action) {
			t.Errorf("the program manager may ask for %q (%s)", refused.action.Action, refused.what)
		}
		err := session.authorize(parsedReply{Actions: []TrackerAction{refused.action}})
		var refusal *AuthorityError
		if !errors.As(err, &refusal) {
			t.Errorf("authorize() of %s = %v, want an authority refusal", refused.what, err)
		}
	}

	// Everything upstream of the lane is somebody else's, and none of it is
	// reachable from this conversation: no proposal, concern, research, or
	// evaluation, and no document or invariant of any kind.
	for _, flag := range []struct {
		name string
		held bool
	}{
		{"Proposals", authority.Proposals},
		{"Concerns", authority.Concerns},
		{"Research", authority.Research},
		{"Evaluations", authority.Evaluations},
		{"ParentRequired", authority.ParentRequired},
	} {
		if flag.held {
			t.Errorf("the program manager's %s = true", flag.name)
		}
	}
	for _, kind := range artifact.Kinds() {
		if err := artifact.Authorize(domain.RoleProgramManager, kind); !errors.Is(err, artifact.ErrUnauthorized) {
			t.Errorf("artifact.Authorize(program-manager, %s) = %v, want ErrUnauthorized", kind, err)
		}
	}
	if err := invariant.Authorize(domain.RoleProgramManager); !errors.Is(err, invariant.ErrUnauthorized) {
		t.Errorf("invariant.Authorize(program-manager) = %v, want ErrUnauthorized", err)
	}

	// What it does hold: the reads, the named repository read, a memory, and
	// both ends of the ask channel.
	if err := session.authorize(parsedReply{Actions: []TrackerAction{{Action: actionRead, ID: "yoyodyne-ifd.1"}, {Action: actionSurvey}}}); err != nil {
		t.Errorf("authorize() of a read and a survey = %v, want both permitted", err)
	}
	if !authority.RepositoryReads || !authority.Memory || !authority.Asks || !authority.Answers {
		t.Errorf("the program manager holds RepositoryReads=%t Memory=%t Asks=%t Answers=%t, want all four",
			authority.RepositoryReads, authority.Memory, authority.Asks, authority.Answers)
	}
}

// The program manager is on the ask channel at both ends: it may open an
// exchange, and another role may put one to it.
func TestAnExchangeRunsToAndFromTheProgramManager(t *testing.T) {
	t.Parallel()

	programManager, _ := AuthorityFor(domain.RoleProgramManager)
	developmentManager, _ := AuthorityFor(domain.RoleDevelopmentManager)
	if err := refuseUnauthorizedAsk(programManager, parsedReply{Ask: &exchange.Ask{Role: domain.RoleDevelopmentManager, Question: "is this a pattern?"}}); err != nil {
		t.Errorf("the program manager was refused an ask to the development manager: %v", err)
	}
	if err := refuseUnauthorizedAsk(developmentManager, parsedReply{Ask: &exchange.Ask{Role: domain.RoleProgramManager, Question: "what does your lane say?"}}); err != nil {
		t.Errorf("the development manager was refused an ask to the program manager: %v", err)
	}
	if err := refuseUnauthorizedAsk(programManager, parsedReply{Ask: &exchange.Ask{Role: domain.RoleProgramManager, Question: "what?"}}); err == nil {
		t.Error("the program manager was allowed to ask its own role")
	}
	if !slices.Contains(askableRoleNames(), string(domain.RoleProgramManager)) {
		t.Errorf("askableRoleNames() = %v, missing the program manager", askableRoleNames())
	}
	if !strings.Contains(programManager.Contract, exchange.AskingContract) {
		t.Error("the program manager's contract does not tell it how to ask")
	}
	if prompt := AnsweringPrompt(domain.RoleProgramManager, ""); !strings.Contains(prompt, "program manager") {
		t.Errorf("the answering prompt does not say who is answering: %q", prompt)
	}
}

// Directives, their resolutions and withdrawals, and causing a run are the rest
// of what the design excludes, and none of them is a block a role writes: a
// directive is recorded, resolved, and withdrawn by the operator's own commands,
// and a run is caused by the harness carrying out a development manager's triage
// decision. So each is held two ways. The registry must hold nothing any of them
// would be granted under, and the one door a role's reply reaches — the tracker
// block — refuses every spelling of them, including a creation that names the
// directive it carries out. A bundle change that grants one fails here.
func TestTheProgramManagerNeitherDirectsNorCausesRuns(t *testing.T) {
	t.Parallel()

	registry := rolecapability.MustDefault()
	for _, excluded := range []struct {
		what string
		held capability.Capability
	}{
		{"causing a run: a re-run or a repair is a triage decision the harness carries out", capability.WorkTriage},
		{"causing a run: writing a run's own record", capability.RunStateMutate},
		{"causing a run: writing inside a worktree", capability.WorktreeMutate},
		{"causing a run: running the project's checks", capability.ChecksExecute},
		{"causing a run: publishing to the forge", capability.ForgePublish},
		{"settling a directive by admitting or closing work outside the lane", capability.BacklogAdmit},
		{"settling a directive by rewriting any item", capability.WorkItemMutate},
		{"reordering the backlog outside the lane", capability.BacklogOrder},
		{"decomposing outside the lane", capability.WorkDecompose},
		{"repairing stale backlog state", capability.WorkItemRepairState},
		{"putting a proposal to the operator", capability.ProposalRaise},
		{"stopping to put a concern to the operator", capability.ConcernRaise},
		{"the brief and the goals", capability.ArtifactProductMutate},
		{"the designs and the decision records", capability.ArtifactDesignMutate},
		{"the invariants", capability.InvariantMutate},
		{"a verdict", capability.ReviewVerdict},
		{"the promotion lease", capability.PromotionLease},
		{"moving the target branch", capability.TargetBranchMutate},
	} {
		if registry.Holds(domain.RoleProgramManager, excluded.held) {
			t.Errorf("the program manager holds %q (%s)", excluded.held, excluded.what)
		}
	}

	authority, _ := AuthorityFor(domain.RoleProgramManager)
	// The instance has a lane, so what refuses the creation below is the
	// directive it names rather than the want of a lane.
	session := &Session{options: Options{Lane: "reliability"}}
	session.state.Role = domain.RoleProgramManager
	for _, name := range []string{"directive", "resolve", "withdraw", "carry-out", "cause", "rerun", "repair", "escalate"} {
		if authority.MayAct(name) {
			t.Errorf("the program manager may ask for %q", name)
		}
		err := session.authorize(parsedReply{Actions: []TrackerAction{{Action: name, ID: "yoyodyne-ifd.1", Reason: "why"}}})
		var refusal *AuthorityError
		if !errors.As(err, &refusal) {
			t.Errorf("authorize() of %q = %v, want an authority refusal", name, err)
		}
	}
	// The one way a role's reply touches a directive is a creation naming the
	// directive it carries out, and that creation is refused.
	carryOut := TrackerAction{Action: actionCreate, Title: "t", Description: "d", Goal: "g", Directive: "directive-1", Reason: "why"}
	if err := session.authorize(parsedReply{Actions: []TrackerAction{carryOut}}); err == nil {
		t.Error("authorize() let the program manager carry out a directive by creating work")
	}
}

// An instance's remit says what its lane is for, and it reaches the provider on
// every turn of the instance's conversation — the first and every resumed one —
// placed after the persona, which is itself after the contract. The order is the
// point: the remit is read under both, and grants nothing either can refuse.
func TestAProgramManagersRemitFollowsThePersonaOnEveryTurn(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-pm-lane", FinalText: "Nothing in the lane has stopped."},
		{SessionID: "session-pm-lane", FinalText: "Two landings since."},
	}}
	const persona = "PERSONA: work plainly and ask before assuming."
	const remit = "REMIT: the reliability lane is for everything that keeps the line from stalling."
	options := testOptions(t, provider)
	options.Role = domain.RoleProgramManager
	options.Agent = "reliability-pm"
	options.Persona = persona
	options.Remit = remit
	session := openTestSession(t, options)
	for _, message := range []string{"What is stuck in the lane?", "And since then?"} {
		if _, err := session.Send(context.Background(), message); err != nil {
			t.Fatalf("Send(%q) error = %v", message, err)
		}
	}

	if len(provider.requests) != 2 {
		t.Fatalf("provider was asked %d time(s), want one per turn", len(provider.requests))
	}
	authority, _ := AuthorityFor(domain.RoleProgramManager)
	for turn, request := range provider.requests {
		prompt := request.SystemPrompt
		contract := strings.Index(prompt, authority.Contract)
		personaAt := strings.Index(prompt, persona)
		remitAt := strings.Index(prompt, remit)
		if contract != 0 || personaAt < 0 || remitAt < 0 {
			t.Fatalf("turn %d prompt carries contract at %d, persona at %d, remit at %d; want all three with the contract first", turn+1, contract, personaAt, remitAt)
		}
		if remitAt < personaAt {
			t.Fatalf("turn %d prompt places the remit at %d, ahead of the persona at %d", turn+1, remitAt, personaAt)
		}
		if !strings.Contains(prompt, "# Configured program manager remit") {
			t.Errorf("turn %d prompt does not label the remit as configuration", turn+1)
		}
	}
	if provider.requests[1].SessionID == "" {
		t.Fatal("the second turn did not resume the session, so it is not the resumed turn this test is about")
	}

	// An agent with no remit is sent exactly what it was sent before remits existed.
	if got := WithRemit("contract", domain.RoleProgramManager, "  "); got != "contract" {
		t.Errorf("WithRemit() with no remit = %q, want the prompt unchanged", got)
	}
}
