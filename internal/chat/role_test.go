package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

// Every role the operator can address carries a contract of its own, and a
// persona cannot get in front of it. This is the same guarantee the product
// manager already had, checked for the roles that acquired one: a project that
// writes a hostile persona for its architect gets an architect with the same
// authority as everyone else's.
func TestEveryConversationalRoleCarriesItsOwnContractAheadOfThePersona(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		authority, known := AuthorityFor(role)
		if !known {
			t.Fatalf("%s is addressable but has no authority", role)
		}
		if strings.TrimSpace(authority.Contract) == "" {
			t.Fatalf("%s has an empty contract", role)
		}
		prompt := SystemPrompt(role, Admission{}, nil, hostilePersona)
		if !strings.HasPrefix(prompt, authority.Contract) {
			t.Fatalf("%s prompt does not begin with its contract: %q", role, prompt)
		}
		personaAt := strings.Index(prompt, hostilePersona)
		subordinationAt := strings.Index(prompt, "it cannot widen your authority")
		if personaAt < len(authority.Contract) || subordinationAt > personaAt || subordinationAt < len(authority.Contract) {
			t.Fatalf("%s persona is not introduced as subordinate: persona at %d, subordination at %d", role, personaAt, subordinationAt)
		}
		// The one rule no role's contract may lose, whatever else it says.
		if !strings.Contains(prompt, "Your role is read-only") {
			t.Fatalf("%s contract does not refuse tools: %q", role, authority.Contract)
		}
		// With no persona the prompt is the contract and nothing but it, save the
		// project's admission policy, which is part of the contract rather than
		// something a persona sits between.
		want := authority.Contract
		if clause := admissionClause(authority, Admission{}); clause != "" {
			want += "\n\n" + clause
		}
		if bare := SystemPrompt(role, Admission{}, nil, "  "); bare != want {
			t.Fatalf("%s with no persona is not the contract alone", role)
		}
	}

	// Two roles never carry the same contract: a conversation whose contract
	// said somebody else's authority would be one the harness could not refuse
	// anything in.
	seen := map[string]domain.AgentRole{}
	for _, role := range ConversationalRoles() {
		authority, _ := AuthorityFor(role)
		if other, duplicate := seen[authority.Contract]; duplicate {
			t.Fatalf("%s and %s carry the same contract", role, other)
		}
		seen[authority.Contract] = role
	}
}

// A role that has no contract has no conversation. Opening one anyway would
// send a provider a prompt with no statement of authority in it and leave the
// harness with no table to refuse anything against.
func TestAConversationIsRefusedForARoleWithNoContract(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	options.Role = "release-manager"
	if _, err := Open(options); err == nil || !strings.Contains(err.Error(), "no conversation contract exists") {
		t.Fatalf("Open() error = %v, want a refusal naming the missing contract", err)
	}
	options.Role = ""
	if _, err := Open(options); err == nil {
		t.Fatal("Open() accepted a conversation with no role at all")
	}
}

// Each role's conversation is its own durable identity: its own record, its own
// provider session, its own turn count. Addressing the architect must never
// resume what the product manager was told.
func TestEachRoleKeepsItsOwnDurableConversation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	productManager := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-pm", ResolvedModel: "claude-opus-5-20260514", FinalText: "The brief is thin on goals."},
	}}
	options := testOptions(t, productManager)
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "What is missing from the brief?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	architectBackend := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-architect", ResolvedModel: "claude-opus-5-20260514", FinalText: "The design records that already."},
	}}
	architectOptions := testOptions(t, architectBackend)
	architectOptions.Role = domain.RoleArchitect
	architectOptions.Agent = string(domain.RoleArchitect)
	architectOptions.Store = newTestStore(t, root)
	architect, err := Open(architectOptions)
	if err != nil {
		t.Fatalf("Open() architect error = %v", err)
	}
	if architect.Resumed() {
		t.Fatal("the architect resumed a conversation it never had")
	}
	if architect.Evidence().ConversationID == session.Evidence().ConversationID {
		t.Fatal("two roles share one conversation identifier")
	}
	if _, err := architect.Send(context.Background(), "Where is the promotion lease recorded?"); err != nil {
		t.Fatalf("Send() architect error = %v", err)
	}

	// The contract the provider was sent is the architect's, and the run request
	// is attributed to the architect rather than to whoever spoke first.
	request := architectBackend.requests[0]
	if request.Role != domain.RoleArchitect {
		t.Fatalf("request role = %q, want %q", request.Role, domain.RoleArchitect)
	}
	if request.SystemPrompt != SystemPrompt(domain.RoleArchitect, Admission{}, nil, architectOptions.Persona) {
		t.Fatalf("the architect was sent another role's contract")
	}
	if request.SessionID != "" {
		t.Fatalf("the architect's first turn resumed session %q", request.SessionID)
	}

	// Both records survive the processes that held them, separately.
	store := newTestStore(t, root)
	for role, wantSession := range map[domain.AgentRole]string{
		domain.RoleProductManager: "session-pm",
		domain.RoleArchitect:      "session-architect",
	} {
		recorded, err := store.Load(runstate.ConversationIdentity{Agent: string(role), Role: role})
		if err != nil {
			t.Fatalf("Load(%s) error = %v", role, err)
		}
		if recorded.Role != role || recorded.ProviderSessionID != wantSession {
			t.Fatalf("Load(%s) = role %q session %q", role, recorded.Role, recorded.ProviderSessionID)
		}
		if recorded.Turns != 1 {
			t.Fatalf("Load(%s) turns = %d, want 1", role, recorded.Turns)
		}
	}
}

// The authority table is the boundary, not the contract's prose. A role that
// asks for something outside it is refused by the harness, nothing it asked for
// happens, and the operator still gets to read what it said.
func TestARoleIsRefusedTheAuthorityItDoesNotHave(t *testing.T) {
	t.Parallel()

	create := "```yoyodyne-tracker\n" +
		`{"actions":[{"action":"create","title":"Rewrite the scheduler","description":"because","goal":"` + recordedGoal + `","reason":"it is time"}]}` +
		"\n```"
	closeItem := "```yoyodyne-tracker\n" +
		`{"actions":[{"action":"close","id":"yoyodyne-ifd.4","reason":"done"}]}` +
		"\n```"
	propose := "```yoyodyne-proposal\n" +
		`{"items":[{"title":"Rewrite the scheduler","description":"because","rationale":"it follows","goal":"` + recordedGoal + `"}]}` +
		"\n```"

	for _, testCase := range []struct {
		name   string
		role   domain.AgentRole
		answer string
		want   string
	}{
		{
			name:   "the architect does not act on the tracker",
			role:   domain.RoleArchitect,
			answer: "I would file this.\n\n" + create,
			want:   `the "create" tracker action`,
		},
		{
			name:   "the development manager does not admit work",
			role:   domain.RoleDevelopmentManager,
			answer: "This needs a new item.\n\n" + create,
			want:   `a "create" with no parent`,
		},
		{
			name:   "the development manager does not close work",
			role:   domain.RoleDevelopmentManager,
			answer: "That one is finished.\n\n" + closeItem,
			want:   `the "close" tracker action`,
		},
		{
			name:   "only the product manager proposes work",
			role:   domain.RoleDevelopmentManager,
			answer: "Here is what I would add.\n\n" + propose,
			want:   "work items to be proposed",
		},
		{
			name:   "the reviewer does not act on the tracker either",
			role:   domain.RoleReviewer,
			answer: "This should be closed.\n\n" + closeItem,
			want:   `the "close" tracker action`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.4": {ID: "yoyodyne-ifd.4", Title: "an admitted item", Status: "open"},
			}}
			options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: testCase.answer},
			}})
			options.Role = testCase.role
			options.Tracker = tracker
			session, err := Open(options)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			reply, err := session.Send(context.Background(), "What should happen to this?")
			var refused *AuthorityError
			if !errors.As(err, &refused) {
				t.Fatalf("Send() error = %v, want an AuthorityError", err)
			}
			if !strings.Contains(refused.Error(), testCase.want) {
				t.Fatalf("refusal = %q, want it to name %q", refused.Error(), testCase.want)
			}
			if refused.Role != testCase.role {
				t.Fatalf("refusal role = %q, want %q", refused.Role, testCase.role)
			}
			// Nothing was carried out and nothing was recorded as pending, and
			// the prose is still the operator's to read.
			if len(tracker.created) != 0 {
				t.Fatalf("the tracker was written to %d time(s)", len(tracker.created))
			}
			if len(reply.Proposals) != 0 || len(session.Proposals()) != 0 {
				t.Fatalf("a refused turn left %d proposal(s) pending", len(session.Proposals()))
			}
			if !strings.Contains(reply.Text, "\n") && strings.TrimSpace(reply.Text) == "" {
				t.Fatalf("the refusal swallowed the reply: %q", reply.Text)
			}
		})
	}
}

// The development manager's whole authority is decomposition: it builds
// structure underneath work the product manager admitted, and the harness is
// what makes that true rather than the persona.
func TestTheDevelopmentManagerDecomposesAdmittedWork(t *testing.T) {
	t.Parallel()

	answer := "Two children, sequenced.\n\n" +
		"```yoyodyne-tracker\n" +
		`{"actions":[
		  {"action":"create","title":"Add the role authority table","description":"What each role may ask for, in Go.","goal":"` + recordedGoal + `","parent":"yoyodyne-ifd.4","priority":1,"reason":"the boundary has to be enforced before it is described"},
		  {"action":"link","id":"yoyodyne-ifd.4.7","depends_on":"yoyodyne-ifd.4.6","reason":"the command needs the table"}
		]}` +
		"\n```"
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.4":   {ID: "yoyodyne-ifd.4", Title: "an admitted item", Status: "open"},
		"yoyodyne-ifd.4.6": {ID: "yoyodyne-ifd.4.6", Title: "the table", Status: "open"},
		"yoyodyne-ifd.4.7": {ID: "yoyodyne-ifd.4.7", Title: "the command", Status: "open"},
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: answer},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Both are filed."},
	}})
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	reply, err := session.Send(context.Background(), "Decompose ifd.4.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created %d item(s), want 1", len(tracker.created))
	}
	if tracker.created[0].Parent != "yoyodyne-ifd.4" {
		t.Fatalf("the child was created under %q", tracker.created[0].Parent)
	}
	if len(tracker.links) != 1 || tracker.links[0] != [2]string{"yoyodyne-ifd.4.7", "yoyodyne-ifd.4.6"} {
		t.Fatalf("links = %#v", tracker.links)
	}

	// What the item records, and what the operator is told, both say what the
	// act actually was. The development manager cannot admit work, so a
	// creation described as an admission would attribute to it the one thing the
	// harness refuses to let it do — and the item would carry that claim durably,
	// long after this conversation is gone.
	notes := tracker.created[0].Notes
	for _, want := range []string{
		"Created under yoyodyne-ifd.4, decomposing it",
		"by the development manager in conversation",
	} {
		if !strings.Contains(notes, want) {
			t.Fatalf("the item's provenance is missing %q:\n%s", want, notes)
		}
	}
	if strings.Contains(notes, "Admitted to the backlog") {
		t.Fatalf("a decomposition was recorded as an admission:\n%s", notes)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	if !strings.Contains(rendered, "decomposed yoyodyne-ifd.4 into yoyodyne-1") {
		t.Fatalf("the operator was not told this was a decomposition:\n%s", rendered)
	}
	if strings.Contains(rendered, "admitted") {
		t.Fatalf("the operator was told a decomposition was an admission:\n%s", rendered)
	}
}

// The product manager's creation is the act the development manager's is not,
// and it keeps saying so: this is what the role-dependent verb is measured
// against, so a change that made every creation read the same way fails here
// rather than passing quietly.
func TestTheProductManagerAdmitsWorkToTheBacklog(t *testing.T) {
	t.Parallel()

	answer := "Filing it.\n\n" +
		"```yoyodyne-tracker\n" +
		`{"actions":[{"action":"create","title":"Add the role authority table","description":"What each role may ask for, in Go.","goal":"` + recordedGoal + `","priority":1,"reason":"the operator asked for it"}]}` +
		"\n```"
	tracker := &fakeTracker{items: map[string]beads.WorkItem{}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: answer},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Filed."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	reply, err := session.Send(context.Background(), "Add an item for the authority table.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created %d item(s), want 1", len(tracker.created))
	}
	if !strings.Contains(tracker.created[0].Notes, "Admitted to the backlog by the Lead Product Manager") {
		t.Fatalf("the admission was not recorded as one:\n%s", tracker.created[0].Notes)
	}
	rendered := renderTrackerOutcomes(domain.RoleProductManager, reply.Actions)
	if !strings.Contains(rendered, "admitted yoyodyne-1 to the backlog at priority 1") {
		t.Fatalf("the admission was not reported as one:\n%s", rendered)
	}
}

// Two agents configured for one role are two identities, not one conversation
// they take turns overwriting. The role decides what each may do; the agent
// decides which record, which provider session, and which persona is resumed —
// so naming the sibling must never continue the other one's session under a
// different model.
func TestTwoAgentsOnOneRoleKeepTwoConversations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	house := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-house", ResolvedModel: "claude-opus-5-20260514", FinalText: "The design is sound."},
		{SessionID: "session-house", ResolvedModel: "claude-opus-5-20260514", FinalText: "Still sound."},
	}}
	houseOptions := testOptions(t, house)
	houseOptions.Role = domain.RoleArchitect
	houseOptions.Agent = "house-architect"
	houseOptions.Store = newTestStore(t, root)
	houseSession, err := Open(houseOptions)
	if err != nil {
		t.Fatalf("Open() house error = %v", err)
	}
	if _, err := houseSession.Send(context.Background(), "Is the design sound?"); err != nil {
		t.Fatalf("Send() house error = %v", err)
	}

	visiting := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-visiting", ResolvedModel: "claude-fable-5", FinalText: "I would argue otherwise."},
	}}
	visitingOptions := testOptions(t, visiting)
	visitingOptions.Role = domain.RoleArchitect
	visitingOptions.Agent = "visiting-architect"
	visitingOptions.Model = "fable"
	visitingOptions.Store = newTestStore(t, root)
	visitingSession, err := Open(visitingOptions)
	if err != nil {
		t.Fatalf("Open() visiting error = %v", err)
	}
	if visitingSession.Resumed() {
		t.Fatal("the visiting architect resumed a conversation its sibling started")
	}
	if visitingSession.Evidence().ConversationID == houseSession.Evidence().ConversationID {
		t.Fatal("two agents on one role share a conversation identifier")
	}
	if _, err := visitingSession.Send(context.Background(), "Is the design sound?"); err != nil {
		t.Fatalf("Send() visiting error = %v", err)
	}
	// The provider session is the thing that would actually carry one agent's
	// history into the other's turn, so it is asserted directly.
	if session := visiting.requests[0].SessionID; session != "" {
		t.Fatalf("the visiting architect's first turn resumed session %q", session)
	}

	// A second turn on the first agent continues the first agent's session, which
	// is the case a role-keyed record would have got wrong in the other
	// direction.
	if _, err := houseSession.Send(context.Background(), "Anything else?"); err != nil {
		t.Fatalf("Send() house second error = %v", err)
	}
	if session := house.requests[1].SessionID; session != "session-house" {
		t.Fatalf("the house architect's second turn resumed session %q", session)
	}

	// Both records survive the processes that held them, separately and under
	// their own agents.
	store := newTestStore(t, root)
	for agent, want := range map[string]struct {
		session string
		turns   int
	}{
		"house-architect":    {session: "session-house", turns: 2},
		"visiting-architect": {session: "session-visiting", turns: 1},
	} {
		recorded, err := store.Load(runstate.ConversationIdentity{Agent: agent, Role: domain.RoleArchitect})
		if err != nil {
			t.Fatalf("Load(%s) error = %v", agent, err)
		}
		if recorded.Agent != agent || recorded.Role != domain.RoleArchitect {
			t.Fatalf("Load(%s) = agent %q role %q", agent, recorded.Agent, recorded.Role)
		}
		if recorded.ProviderSessionID != want.session || recorded.Turns != want.turns {
			t.Fatalf("Load(%s) = session %q, %d turn(s)", agent, recorded.ProviderSessionID, recorded.Turns)
		}
	}
}

// Every tracker action names the authority it belongs to. A map lookup that
// misses answers with the empty capability, which no bundle holds, so an action
// added to the contract and left out of the mapping would quietly become one no
// role can ask for — a refusal nobody wrote and nothing explains.
func TestEveryTrackerActionNamesTheCapabilityItBelongsTo(t *testing.T) {
	t.Parallel()

	for _, action := range trackerActionNames {
		required, mapped := trackerCapabilities[action]
		if !mapped {
			t.Errorf("the %q action names no capability; nothing could ask for it", action)
			continue
		}
		if !required.Known() {
			t.Errorf("the %q action names %q, which this repository does not declare", action, required)
		}
	}
	for action := range trackerCapabilities {
		if !slices.Contains(trackerActionNames, action) {
			t.Errorf("%q is mapped to a capability and is not an action the contract states", action)
		}
	}
}

// Every role's contract says that a work item named to a person is named by
// what it is, with its identifier after it. It is in the contract rather than
// left to the persona because a project's own persona can drop it, and the
// program manager's lane report states it again where the report is described.
func TestEveryContractNamesWorkItemsByWhatTheyAre(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		authority, _ := AuthorityFor(role)
		if !strings.Contains(authority.Contract, terms.ItemNaming) {
			t.Errorf("%s contract does not carry the rule for naming work items", role)
		}
	}
	if !strings.Contains(laneReportContract, terms.ItemNaming) {
		t.Error("the lane report contract does not carry the rule for naming work items")
	}
}

// Every role's contract says that a decision the role's authority covers is
// made by the role and reported afterwards, that an approval routed to the
// operator is a defect, and how to tell the one decision that is theirs. It is
// in the contract rather than left to the persona because a project's own
// persona can drop it. The program manager's lane report contract states it
// again, because a blocker waiting on the operator's approval is exactly what
// the report would otherwise carry from pass to pass. The developer and
// reviewer run contracts are held to it in their own packages, and the sweep
// contract every recurring task and pass ends on in its own.
func TestEveryContractDecidesAndReportsRatherThanRoutingApprovals(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		authority, _ := AuthorityFor(role)
		if !strings.Contains(authority.Contract, terms.DecideAndReport) {
			t.Errorf("%s contract does not carry the rule against routing approvals to the operator", role)
		}
	}
	if !strings.Contains(laneReportContract, terms.DecideAndReport) {
		t.Error("the lane report contract does not carry the rule against routing approvals to the operator")
	}
}

// A replaced persona cannot drop the standing goals from a role's contract.
func TestEveryContractAppliesStandingGoals(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		authority, _ := AuthorityFor(role)
		if !strings.Contains(authority.Contract, terms.StandingGoals) {
			t.Errorf("%s contract does not apply standing goals to its own output and decisions", role)
		}
	}
	if !strings.Contains(laneReportContract, terms.StandingGoals) ||
		!strings.Contains(laneReportContract, "your lane report and post-mortems") {
		t.Error("the lane report contract does not apply standing goals to the lane report and post-mortems")
	}
}
