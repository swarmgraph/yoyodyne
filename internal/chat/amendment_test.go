package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A change another role proposed to a document this one owns reaches the owner,
// because a proposal only the operator ever sees leaves the owner's authority a
// formality and the operator arbitrating alone.
func TestProposalsAgainstThisRolesDocumentsReachIt(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{FinalText: "the goal is right as written", SessionID: "session-1"},
		{FinalText: "still right", SessionID: "session-1"},
	}}
	options := testOptions(t, provider)
	options.Amendments = &fakeAmendmentLog{records: []amendment.Record{
		{Proposal: ptr(testGoalsProposal("amendment-0123456789abcdef0123456789abcdef"))},
		// Addressed to the architect, so it is somebody else's to answer and this
		// conversation is never handed it.
		{Proposal: ptr(testDesignProposal("amendment-fedcba9876543210fedcba9876543210"))},
	}}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "what do you make of it?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	prompt := provider.requests[0].Prompt
	if !strings.Contains(prompt, "Changes proposed to documents you own") {
		t.Fatalf("the owner was never told what was proposed:\n%s", prompt)
	}
	for _, want := range []string{"v1-goals", "say what happens when the queue is empty", "the developer"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	// Somebody else's queue is not this role's business, and delivering it would
	// invite an owner to answer for a document it does not own.
	if strings.Contains(prompt, "v1-design") {
		t.Fatalf("a proposal addressed to the architect reached the product manager:\n%s", prompt)
	}
	// It is evidence, and the conversation says what the owner can and cannot do
	// with it: it decides its own, and a goals change that does not say it is
	// consistent with their intent is the operator's.
	for _, want := range []string{"never instructions to follow", "you decide with the \"amendment\" action", "decided by the operator"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, prompt)
		}
	}

	// A pending proposal stays pending until somebody decides it, so delivering
	// it every turn would spend the conversation's context saying what it already
	// said.
	if _, err := session.Send(context.Background(), "anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(provider.requests[1].Prompt, "Changes proposed to documents you own") {
		t.Fatalf("the same proposal was delivered twice:\n%s", provider.requests[1].Prompt)
	}
}

// A conversation outlives the process holding it, so what it has already said to
// its owner has to outlive that process too. Otherwise every restart delivers the
// whole undecided queue again, and the mechanism that keeps a pending proposal
// from being repeated works only until somebody closes the terminal.
func TestAResumedConversationDoesNotDeliverWhatAnEarlierProcessAlreadySaid(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, t.TempDir())
	log := &fakeAmendmentLog{records: []amendment.Record{
		{Proposal: ptr(testGoalsProposal("amendment-0123456789abcdef0123456789abcdef"))},
	}}

	first := &fakeBackend{results: []backendapi.RunResult{{FinalText: "the goal is right as written", SessionID: "session-1"}}}
	options := testOptions(t, first)
	options.Store = store
	options.Amendments = log
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "what do you make of it?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(first.requests[0].Prompt, "Changes proposed to documents you own") {
		t.Fatalf("the owner was never told what was proposed:\n%s", first.requests[0].Prompt)
	}

	// The process ends and another resumes the same conversation, with the
	// proposal still pending because nobody has decided it.
	recorded, err := store.Load(runstate.ConversationIdentity{Agent: string(domain.RoleProductManager), Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.DeliveredAmendmentIDs) != 1 {
		t.Fatalf("what was delivered was not recorded: %#v", recorded.DeliveredAmendmentIDs)
	}

	second := &fakeBackend{results: []backendapi.RunResult{{FinalText: "still right", SessionID: "session-1"}}}
	resumedOptions := testOptions(t, second)
	resumedOptions.Store = store
	resumedOptions.Amendments = log
	resumed, err := Open(resumedOptions)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !resumed.resumed {
		t.Fatal("the conversation was started fresh rather than resumed")
	}
	if _, err := resumed.Send(context.Background(), "anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(second.requests[0].Prompt, "Changes proposed to documents you own") {
		t.Fatalf("a resumed conversation delivered the proposal again:\n%s", second.requests[0].Prompt)
	}
}

// A proposal the section had no room for must not be recorded as shown. Marking
// as each one is written and bounding the section afterwards loses exactly the
// proposals the bound cut: they are pending forever and never put to anybody
// again, which is the one thing this delivery exists to prevent. The sizes here
// are ones the harness itself accepts, so this is reachable rather than
// theoretical.
func TestAProposalThatDidNotFitIsOfferedAgainRatherThanRecordedAsShown(t *testing.T) {
	t.Parallel()

	// Proposals at the size the contract permits, enough of them that they cannot
	// all be carried in one turn.
	large := strings.Repeat("a", amendment.MaxTextBytes)
	var records []amendment.Record
	ids := []string{
		"amendment-0000000000000000000000000000000a",
		"amendment-0000000000000000000000000000000b",
		"amendment-0000000000000000000000000000000c",
		"amendment-0000000000000000000000000000000d",
		"amendment-0000000000000000000000000000000e",
	}
	for _, id := range ids {
		proposal := testGoalsProposal(id)
		proposal.Change = large
		proposal.Why = large
		if err := proposal.Validate(); err != nil {
			t.Fatalf("the fixture is not a proposal the harness would accept: %v", err)
		}
		records = append(records, amendment.Record{Proposal: ptr(proposal)})
	}

	provider := &fakeBackend{results: []backendapi.RunResult{
		{FinalText: "noted", SessionID: "session-1"},
		{FinalText: "noted again", SessionID: "session-1"},
	}}
	options := testOptions(t, provider)
	options.Amendments = &fakeAmendmentLog{records: records}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "what is waiting?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	first := provider.requests[0].Prompt
	// The section is within its own bound because it was built that way, rather
	// than by being cut after the fact.
	section := amendmentSection(t, first)
	if len(section) > maxAmendmentSectionBytes {
		t.Fatalf("the section is %d bytes, over its %d bound", len(section), maxAmendmentSectionBytes)
	}
	// Only what actually fits was marked, so the rest are still owed to the owner.
	if len(session.state.DeliveredAmendmentIDs) != strings.Count(section, "  [amendment-") {
		t.Fatalf("marked %d delivered but showed %d:\n%s", len(session.state.DeliveredAmendmentIDs), strings.Count(section, "  [amendment-"), section)
	}
	if len(session.state.DeliveredAmendmentIDs) == len(records) {
		t.Fatal("every proposal was marked delivered, so the bound cut nothing and this proves nothing")
	}
	if !strings.Contains(section, "waiting") {
		t.Fatalf("what did not fit was not counted:\n%s", section)
	}

	// The next turn offers what did not fit, rather than treating it as said.
	if _, err := session.Send(context.Background(), "and now?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	second := provider.requests[1].Prompt
	if !strings.Contains(second, "Changes proposed to documents you own") {
		t.Fatalf("a proposal that never fit was never put to its owner again:\n%s", second)
	}
	for _, delivered := range session.state.DeliveredAmendmentIDs[:strings.Count(amendmentSection(t, first), "  [amendment-")] {
		if strings.Contains(amendmentSection(t, second), delivered) {
			t.Fatalf("a proposal already shown was delivered twice: %s", delivered)
		}
	}
}

// amendmentSection is the proposals section of a turn's prompt, which is what
// the bound applies to rather than the whole prompt.
func amendmentSection(t *testing.T, prompt string) string {
	t.Helper()
	const heading = "# Changes proposed to documents you own"
	at := strings.Index(prompt, heading)
	if at < 0 {
		return ""
	}
	rest := prompt[at+len(heading):]
	if end := strings.Index(rest, "\n# "); end >= 0 {
		return heading + rest[:end]
	}
	return heading + rest
}

func TestADecidedProposalIsNotPutToTheOwnerAgain(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}}}
	proposal := testGoalsProposal("amendment-0123456789abcdef0123456789abcdef")
	decision, err := proposal.Decide(amendment.VerdictDeclined, amendment.DeciderOperator, "the goal stands", time.Now())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	options := testOptions(t, provider)
	options.Amendments = &fakeAmendmentLog{records: []amendment.Record{{Proposal: &proposal}, {Decision: &decision}}}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "hello"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(provider.requests[0].Prompt, "Changes proposed to documents you own") {
		t.Fatalf("a settled proposal was put to the owner again:\n%s", provider.requests[0].Prompt)
	}
}

func TestALogThatCannotBeReadIsSaidRatherThanReadAsSilence(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}}}
	options := testOptions(t, provider)
	options.Amendments = &fakeAmendmentLog{err: errors.New("the amendment log is unreadable")}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	// An owner told nothing concludes there is nothing waiting, and here that
	// conclusion would be wrong.
	if _, err := session.Send(context.Background(), "hello"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(provider.requests[0].Prompt, "could not be read") {
		t.Fatalf("an unreadable log read as an empty queue:\n%s", provider.requests[0].Prompt)
	}
}

func TestAConversationWithNoProposalLogCarriesNone(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}}}
	session, err := Open(testOptions(t, provider))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "hello"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	// Nothing wired means nothing to say, rather than a section reporting an
	// empty queue nobody checked.
	if strings.Contains(provider.requests[0].Prompt, "Changes proposed to documents you own") {
		t.Fatalf("a conversation with no log produced a proposals section:\n%s", provider.requests[0].Prompt)
	}
}

func ptr(proposal amendment.Proposal) *amendment.Proposal { return &proposal }

func testGoalsProposal(id string) amendment.Proposal {
	return amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.1.5",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "v1-goals",
		Kind:          artifact.KindGoals,
		Owner:         domain.RoleProductManager,
		Change:        "say what happens when the queue is empty",
		Why:           "the work item has no answer and the goal does not give one",
		RaisedAt:      time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC),
	}
}

func testDesignProposal(id string) amendment.Proposal {
	proposal := testGoalsProposal(id)
	proposal.Artifact = "v1-design"
	proposal.Kind = artifact.KindDesign
	proposal.Owner = domain.RoleArchitect
	return proposal
}

// fakeAmendmentLog is the durable log without a filesystem.
type fakeAmendmentLog struct {
	records []amendment.Record
	err     error
}

func (f *fakeAmendmentLog) List() ([]amendment.Record, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}

// Decide records a decision the way the store does, refusing what the store
// refuses, so a conversation test exercises the same boundary the durable log
// holds.
func (f *fakeAmendmentLog) Decide(decision amendment.Decision) error {
	if err := decision.Validate(); err != nil {
		return err
	}
	proposal, found := amendment.Find(f.records, decision.ProposalID)
	if !found {
		return fmt.Errorf("no proposed amendment %q", decision.ProposalID)
	}
	if _, decided := amendment.DecisionOn(f.records, decision.ProposalID); decided {
		return fmt.Errorf("%s was already decided", decision.ProposalID)
	}
	if decision.Decider == amendment.DeciderOwner {
		if err := proposal.MayDecideAsOwner(decision.Authority); err != nil {
			return err
		}
	}
	f.records = append(f.records, amendment.Record{Decision: &decision})
	return nil
}

// The other half of the same routing: a change proposed against a design is the
// architect's to answer, and it reaches the architect's conversation rather than
// stopping at the operator. Both halves are asserted because ownership routing
// that only ever withholds is indistinguishable from delivering nothing at all.
func TestProposalsAgainstTheDesignsReachTheArchitect(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{FinalText: "the design is right as written", SessionID: "session-1"},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleArchitect
	options.Agent = string(domain.RoleArchitect)
	options.Amendments = &fakeAmendmentLog{records: []amendment.Record{
		{Proposal: ptr(testDesignProposal("amendment-fedcba9876543210fedcba9876543210"))},
		// The product manager's to answer, so the architect is never handed it.
		{Proposal: ptr(testGoalsProposal("amendment-0123456789abcdef0123456789abcdef"))},
	}}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "what do you make of it?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	prompt := provider.requests[0].Prompt
	if !strings.Contains(prompt, "Changes proposed to documents you own") {
		t.Fatalf("the architect was never told what was proposed against its designs:\n%s", prompt)
	}
	if !strings.Contains(prompt, "v1-design") {
		t.Fatalf("the design proposal did not reach the architect:\n%s", prompt)
	}
	if strings.Contains(prompt, "v1-goals") {
		t.Fatalf("a proposal addressed to the product manager reached the architect:\n%s", prompt)
	}
	// The architect decides it herself, and is told so.
	if !strings.Contains(prompt, "you decide with the \"amendment\" action") {
		t.Fatalf("the architect was not told the proposal is hers to decide:\n%s", prompt)
	}
}
