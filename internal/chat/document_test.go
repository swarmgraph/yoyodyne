package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// documentReply is a turn that writes one document, in the shape the contract
// states. It is built here rather than pasted per test so a change to the block
// is a change in one place, exactly as it is for the role reading the contract.
func documentReply(action, id, kind, directory, body string) string {
	block := `{"documents":[{"action":"` + action + `","id":"` + id + `"`
	if kind != "" {
		block += `,"kind":"` + kind + `"`
		block += `,"title":"What v2 is for"`
	}
	if directory != "" {
		block += `,"directory":"` + directory + `"`
	}
	block += `,"body":"` + body + `","reason":"drafted with the operator"}]}`
	return "Here is the document.\n\n" + artifact.WriteFence + "\n" + block + "\n```\n"
}

// documentOptions is a conversation with an artifact store behind it, over a
// repository that has the homes a generated project has.
func documentOptions(t *testing.T, provider Backend) (Options, string) {
	t.Helper()

	options := testOptions(t, provider)
	repository := t.TempDir()
	options.Repository = repository
	// The store a generated project gets, assembled the one way production
	// assembles it, so a test can never be reading a filing no project has.
	options.Documents = artifact.StoreFor(repository, config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	})
	return options, repository
}

func TestADocumentIsRecordedAndNothingIsWrittenUntilTheOperatorApproves(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals\\n\\nShip the thing."),
	}}}
	options, repository := documentOptions(t, provider)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Write the goals up.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Writes) != 1 {
		t.Fatalf("reply carried %d documents, want 1", len(reply.Writes))
	}
	if strings.Contains(reply.Text, "documents") {
		t.Fatalf("the block was left in the prose: %q", reply.Text)
	}
	// The document is waiting on the operator and the repository is untouched.
	// That is the whole of what a reply carrying a document has changed.
	if _, err := os.Stat(filepath.Join(repository, "docs", "product", "v2-goals.md")); !os.IsNotExist(err) {
		t.Fatalf("a document was written before the operator approved it: %v", err)
	}

	written := reply.Writes[0]
	outcome, err := session.ApproveWrite(written.ID)
	if err != nil {
		t.Fatalf("ApproveWrite() error = %v", err)
	}
	if outcome.Path != "docs/product/v2-goals.md" || !outcome.Approval {
		t.Fatalf("outcome = %#v", outcome)
	}
	if waiting := session.Writes(); len(waiting) != 0 {
		t.Fatalf("a written document is still waiting: %#v", waiting)
	}
	// The write stops at the working tree, and the operator is told so as it
	// lands — in the words `yoyo artifact approve` uses, naming the checkout —
	// rather than meeting it later as a run that refuses to start.
	checkout := options.Documents.(artifact.Store).RepositoryRoot
	if want := artifact.PendingCommit(checkout, "docs/product/v2-goals.md"); outcome.PendingCommit != want ||
		!strings.Contains(outcome.Render(), want) {
		t.Fatalf("outcome says %q and renders %q, want it to say %q", outcome.PendingCommit, outcome.Render(), want)
	}

	// What landed is a document the store itself reads back: the frontmatter the
	// harness generated, the revision recorded under the role that wrote it, and
	// the operator's approval against that revision. None of it was transcribed.
	set, err := options.Documents.(artifact.Store).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	recorded, found := set.Find("v2-goals")
	if !found {
		t.Fatalf("the written document is not in the set: %#v", set)
	}
	if recorded.Kind != artifact.KindGoals || recorded.Title != "What v2 is for" {
		t.Fatalf("recorded document = %#v", recorded)
	}
	if len(recorded.Revisions) != 1 || recorded.Revisions[0].By != domain.RoleProductManager {
		t.Fatalf("revisions = %#v", recorded.Revisions)
	}
	if recorded.ApprovalState() != artifact.ApprovalApproved {
		t.Fatalf("approval state = %q", recorded.ApprovalState())
	}
	approval, _ := recorded.LatestApproval()
	if approval.By != artifact.ApproverOperator || !strings.Contains(approval.Reason, session.Evidence().ConversationID) {
		t.Fatalf("recorded approval = %#v", approval)
	}
	// The product manager is told what the operator did with its document, the
	// same way it is told about an approved proposal.
	if !strings.Contains(strings.Join(session.notices, "\n"), "docs/product/v2-goals.md") {
		t.Fatalf("the role was not told the document was written: %v", session.notices)
	}
}

func TestADeclinedDocumentWritesNothingAndKeepsTheReason(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals"),
	}}}
	options, repository := documentOptions(t, provider)
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Write the goals up.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if err := session.DeclineWrite(reply.Writes[0].ID, "the second goal is not what I meant"); err != nil {
		t.Fatalf("DeclineWrite() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repository, "docs")); !os.IsNotExist(err) {
		t.Fatalf("a declined document reached the repository: %v", err)
	}
	if waiting := session.Writes(); len(waiting) != 0 {
		t.Fatalf("a declined document is still waiting: %#v", waiting)
	}
	// A decision is made once. Approving what was declined would be a second
	// answer quietly replacing the one somebody already acted on.
	if _, err := session.ApproveWrite(reply.Writes[0].ID); err == nil {
		t.Fatal("ApproveWrite() wrote a document that was already declined")
	}
}

func TestADocumentTheRoleMayNotWriteIsRefusedBeforeAnythingIsRecorded(t *testing.T) {
	t.Parallel()

	// The architect owns designs; the goals are the product manager's, and the
	// refusal is the ownership table rather than anything about this reply.
	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals"),
	}, {SessionID: "session-1", FinalText: "I will leave the goals to their owner."}}}
	options, repository := documentOptions(t, provider)
	options.Role = domain.RoleArchitect
	options.Agent = string(domain.RoleArchitect)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Write the goals up.")
	if err != nil || len(reply.DocumentRefusals) != 1 || !strings.Contains(reply.DocumentRefusals[0], "owns") {
		t.Fatalf("Send() = %#v, %v, want an ownership refusal returned to the role", reply, err)
	}
	if len(reply.Writes) != 0 || len(session.Writes()) != 0 {
		t.Fatalf("a refused document was recorded: %#v", session.Writes())
	}
	if _, err := os.Stat(filepath.Join(repository, "docs")); !os.IsNotExist(err) {
		t.Fatalf("a refused document reached the repository: %v", err)
	}
	// The answer is still the operator's to read: the provider answered, and the
	// turn was paid for whichever way the block went.
	if !strings.Contains(reply.Text, "Here is the document.") {
		t.Fatalf("the reply was lost with the refusal: %q", reply.Text)
	}
}

func TestADocumentFiledOutsideTheArtifactHomesIsRefusedAtTheActionLayer(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("create", "v2-goals", "goals", "internal/chat", "# Goals"),
	}, {SessionID: "session-1", FinalText: "Understood."}}}
	options, repository := documentOptions(t, provider)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Write the goals up.")
	if err != nil || len(reply.DocumentRefusals) != 1 {
		t.Fatalf("Send() = %#v, %v, want a returned refusal", reply, err)
	}
	if len(reply.Writes) != 0 || len(session.Writes()) != 0 {
		t.Fatalf("a document filed outside the homes was recorded: %#v", session.Writes())
	}
	if _, err := os.Stat(filepath.Join(repository, "internal")); !os.IsNotExist(err) {
		t.Fatalf("a refused document reached the repository: %v", err)
	}
}

// Inside an artifact home is not enough either, and this is the case the write
// contract used to steer a role into: the architect filing a design under the
// product manager's home, which loads and validates and is in the wrong role's
// directory.
func TestADocumentFiledInAnotherKindsHomeIsRefusedAtTheActionLayer(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("create", "v1-design", "design", config.DefaultSpecifications, "# Design"),
	}, {SessionID: "session-1", FinalText: "Understood."}}}
	options, repository := documentOptions(t, provider)
	options.Role = domain.RoleArchitect
	options.Agent = string(domain.RoleArchitect)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Write the design up.")
	if err != nil || len(reply.DocumentRefusals) != 1 {
		t.Fatalf("Send() = %#v, %v, want a returned refusal", reply, err)
	}
	if !strings.Contains(reply.DocumentRefusals[0], config.DefaultDesigns) {
		t.Fatalf("the refusal does not name where a design is filed: %v", err)
	}
	if len(reply.Writes) != 0 || len(session.Writes()) != 0 {
		t.Fatalf("a misfiled document was recorded: %#v", session.Writes())
	}
	if _, err := os.Stat(filepath.Join(repository, "docs")); !os.IsNotExist(err) {
		t.Fatalf("a refused document reached the repository: %v", err)
	}

	// And the contract this role is sent tells it the directory that is accepted,
	// rather than the first home the project happens to configure.
	contract := SystemPrompt(domain.RoleArchitect, testAdmission, session.artifactFiling(), "")
	if !strings.Contains(contract, `"directory":"`+config.DefaultDesigns+`"`) {
		t.Fatalf("the architect's contract does not file a design in %s", config.DefaultDesigns)
	}
	if strings.Contains(contract, `"directory":"`+config.DefaultSpecifications+`"`) {
		t.Fatalf("the architect's contract steers a document into the product manager's home")
	}
}

// A revision names an id rather than a kind, so the role's authority over it can
// only be settled by reading the document. It is settled before the operator is
// asked: an ownership refusal is permanent, and a document parked on one would
// hold a pending slot until somebody declined it.
func TestARevisionOfAnotherRolesDocumentIsRefusedBeforeTheOperatorIsAsked(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("revise", "v1-design", "", "", "# Design\\n\\nRevised."),
	}, {SessionID: "session-1", FinalText: "Understood."}}}
	options, repository := documentOptions(t, provider)
	store := options.Documents.(artifact.Store)
	design := artifact.Write{
		Action: artifact.WriteCreate, ID: "v1-design", Kind: artifact.KindDesign, Title: "How it is built",
		Directory: "docs/designs", Body: "# Design", Reason: "recorded with the operator",
	}
	if _, err := store.Create(domain.RoleArchitect, design.Draft(), fixedClock{}.Now()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	before, err := os.ReadFile(filepath.Join(repository, "docs", "designs", "v1-design.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	// The product manager owns the goals, not the designs.
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Narrow the second design decision.")
	if err != nil || len(reply.DocumentRefusals) != 1 {
		t.Fatalf("Send() = %#v, %v, want a returned refusal", reply, err)
	}
	if len(reply.Writes) != 0 || len(session.Writes()) != 0 {
		t.Fatalf("a revision of another role's document was recorded: %#v", session.Writes())
	}
	after, err := os.ReadFile(filepath.Join(repository, "docs", "designs", "v1-design.md"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("the design changed under a refused revision: %v", err)
	}
}

// The same refusal reached at the write rather than at the action layer — which
// is what a document that changed hands between the two would produce — decides
// the document rather than leaving it waiting on an approval that can never
// succeed.
func TestAWriteTheStoreRefusesOnOwnershipIsDecidedRatherThanLeftWaiting(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals"),
	}}}
	options, _ := documentOptions(t, provider)
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Write the goals up.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	// The document was recorded under a role that owns it, and the store is then
	// asked under one that does not.
	session.state.Role = domain.RoleArchitect

	outcome, err := session.ApproveWrite(reply.Writes[0].ID)
	if err == nil {
		t.Fatal("ApproveWrite() wrote a document the store had no authority for")
	}
	if outcome.Undecided {
		t.Fatalf("a permanent refusal left the document waiting: %#v", outcome)
	}
	if waiting := session.Writes(); len(waiting) != 0 {
		t.Fatalf("a refused document still holds a pending slot: %#v", waiting)
	}
	if rendered := outcome.Render(); !strings.Contains(rendered, "refused") ||
		!strings.Contains(rendered, "not waiting on you") {
		t.Fatalf("the operator is told to try again: %q", rendered)
	}
}

func TestAConversationWithNoArtifactStoreOffersNoWriteAndRefusesOne(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals"),
	}, {SessionID: "session-1", FinalText: "Understood."}}}
	session := openTestSession(t, testOptions(t, provider))

	// The role is not told about a mechanism nothing behind this conversation
	// could carry out, which is the same rule the tracker clause follows.
	if strings.Contains(SystemPrompt(domain.RoleProductManager, testAdmission, nil, ""), artifact.WriteFence) {
		t.Fatal("a conversation with no artifact store offered the write contract")
	}
	reply, err := session.Send(context.Background(), "Write the goals up.")
	if err != nil || len(reply.DocumentRefusals) != 1 {
		t.Fatalf("Send() = %#v, %v, want a returned refusal", reply, err)
	}
}

func TestADocumentSurvivesTheProcessThatWroteIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-9", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals\\n\\nShip the thing."),
	}}}
	options, repository := documentOptions(t, provider)
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Write the goals up.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	writeID := reply.Writes[0].ID

	// A second process sees only what was written down. This is the case the
	// mechanism exists for: a document written by one `--message` invocation is
	// approved by another, and a document that did not survive would have to be
	// typed out again by whoever approved it.
	resumedOptions, _ := documentOptions(t, &fakeBackend{})
	resumedOptions.Store = newTestStore(t, root)
	resumedOptions.Repository = repository
	resumedOptions.Documents = options.Documents
	resumed := openTestSession(t, resumedOptions)
	waiting := resumed.Writes()
	if len(waiting) != 1 || waiting[0].ID != writeID {
		t.Fatalf("the document did not survive the process: %#v", waiting)
	}
	if !strings.Contains(waiting[0].Write.Body, "Ship the thing.") {
		t.Fatalf("the document came back without its prose: %#v", waiting[0].Write)
	}

	// And the operator's approval, sent as its own message, decides that document
	// rather than being said to the agent as speech.
	outcomes, decided, err := resumed.DecideWrites("approve " + writeID)
	if !decided || err != nil {
		t.Fatalf("DecideWrites() = %v, %v", decided, err)
	}
	if len(outcomes) != 1 || !outcomes[0].Approved || outcomes[0].Path == "" {
		t.Fatalf("outcomes = %#v", outcomes)
	}
	if _, err := os.Stat(filepath.Join(repository, "docs", "product", "v2-goals.md")); err != nil {
		t.Fatalf("the approved document is not in the repository: %v", err)
	}
	// Nothing is left listed as waiting once it has been written, in this process
	// or in the record a later one reads.
	recorded, err := newTestStore(t, root).Load(runstate.ConversationIdentity{
		Agent: string(domain.RoleProductManager), Role: domain.RoleProductManager,
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.PendingWrites) != 0 {
		t.Fatalf("a written document is still recorded as waiting: %#v", recorded.PendingWrites)
	}
}

func TestAMessageDecidesADocumentOnlyWhenItNamesOne(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals"),
	}}}
	options, _ := documentOptions(t, provider)
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Write the goals up.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// A message is not an answer to a question that was just asked: it may be
	// hours later, and reading loose prose as an approval would file a document
	// nobody meant to approve.
	for _, spoken := range []string{"yes", "y", "that reads well, thank you", "no, not yet"} {
		if _, decided, _ := session.DecideWrites(spoken); decided {
			t.Fatalf("%q was read as a decision about a document", spoken)
		}
	}
	// An answer naming a document the conversation no longer holds is refused out
	// loud rather than spent on a turn with the agent.
	if _, decided, err := session.DecideWrites("approve document-99.1"); !decided || err == nil {
		t.Fatalf("an approval naming nothing was passed on as speech: %v, %v", decided, err)
	}
	outcomes, decided, err := session.DecideWrites("decline " + reply.Writes[0].ID + " the second goal is not what I meant")
	if !decided || err != nil {
		t.Fatalf("DecideWrites() = %v, %v", decided, err)
	}
	if len(outcomes) != 1 || outcomes[0].Approved || !strings.Contains(outcomes[0].Reason, "not what I meant") {
		t.Fatalf("outcomes = %#v", outcomes)
	}
}

func TestADocumentTheHarnessAcceptsIsOneItCanAlsoRecord(t *testing.T) {
	t.Parallel()

	// The two bounds are declared in different packages — one is what a reply may
	// carry, the other is what the conversation's state file may hold — and a
	// document taken from a reply that could not then be written down would be
	// one the operator was shown and no later process could name.
	if artifact.MaxWriteBodyBytes != runstate.MaxPendingWriteBytes {
		t.Fatalf("a reply may carry %d bytes of document and the record holds %d",
			artifact.MaxWriteBodyBytes, runstate.MaxPendingWriteBytes)
	}
	if artifact.MaxWritesPerReply > runstate.MaxPendingWrites {
		t.Fatalf("a reply may carry %d documents and the record holds %d",
			artifact.MaxWritesPerReply, runstate.MaxPendingWrites)
	}
}

func TestTheOperatorIsShownTheDocumentAndItIsWrittenOnTheirYes(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals\\n\\nShip the thing."),
	}}}
	options, repository := documentOptions(t, provider)
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(),
		testConsole(strings.NewReader("write the goals up\ny\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	// The document itself is on the screen before the question, because a
	// document approved unread is the transcription problem with an extra step.
	for _, required := range []string{
		"Ship the thing.",
		"create v2-goals (goals) in docs/product?",
		"records your approval in it",
		"docs/product/v2-goals.md",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("the transcript does not say %q:\n%s", required, transcript)
		}
	}
	if _, err := os.Stat(filepath.Join(repository, "docs", "product", "v2-goals.md")); err != nil {
		t.Fatalf("the approved document is not in the repository: %v", err)
	}
}

func TestAnAnswerNobodyCanBeSureOfDeclinesTheDocument(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1", ResolvedModel: "claude-opus-5",
		FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals"),
	}}}
	options, repository := documentOptions(t, provider)
	session := openTestSession(t, options)

	var out strings.Builder
	// The fail-closed rule the proposals already follow, applied where it matters
	// most: this is the prompt whose answer writes a file.
	if err := session.Converse(context.Background(),
		testConsole(strings.NewReader("write the goals up\nlet me think about it\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if !strings.Contains(out.String(), "let me think about it") {
		t.Fatalf("the answer was not kept as the reason:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(repository, "docs")); !os.IsNotExist(err) {
		t.Fatalf("an unclear answer wrote a document: %v", err)
	}
}

func TestARevisionReplacesTheDocumentAndRecordsAFurtherApproval(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "s", ResolvedModel: "claude-opus-5",
			FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals\\n\\nShip the thing.")},
		{SessionID: "s", ResolvedModel: "claude-opus-5",
			FinalText: documentReply("revise", "v2-goals", "", "", "# Goals\\n\\nShip it twice.")},
	}}
	options, _ := documentOptions(t, provider)
	session := openTestSession(t, options)

	first, err := session.Send(context.Background(), "Write the goals up.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if _, err := session.ApproveWrite(first.Writes[0].ID); err != nil {
		t.Fatalf("ApproveWrite() error = %v", err)
	}
	second, err := session.Send(context.Background(), "Narrow the second goal.")
	if err != nil {
		t.Fatalf("Send() second error = %v", err)
	}
	if _, err := session.ApproveWrite(second.Writes[0].ID); err != nil {
		t.Fatalf("ApproveWrite() second error = %v", err)
	}

	set, err := options.Documents.(artifact.Store).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	recorded, _ := set.Find("v2-goals")
	if len(recorded.Revisions) != 2 || recorded.Revisions[1].Action != artifact.ActionAmended {
		t.Fatalf("revisions = %#v", recorded.Revisions)
	}
	// The title the creation gave it is still there: a revision carries the
	// document, and what it does not mention it does not replace with nothing.
	if recorded.Title != "What v2 is for" {
		t.Fatalf("the revision lost the title: %#v", recorded)
	}
	if recorded.ApprovalState() != artifact.ApprovalApproved || len(recorded.Approvals) != 2 {
		t.Fatalf("approvals = %#v", recorded.Approvals)
	}
}

func TestARepeatedDocumentRefusalWaitsDurablyForTheNextTurn(t *testing.T) {
	t.Parallel()
	answer := documentReply("create", "v2-goals", "goals", "internal/chat", "# Goals")
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: answer},
		{SessionID: "session-1", FinalText: answer},
		{SessionID: "session-1", FinalText: "I will correct the directory."},
	}}
	options, _ := documentOptions(t, provider)
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Write the goals.")
	if err != nil || len(reply.DocumentRefusals) != 2 || len(provider.requests) != 2 {
		t.Fatalf("Send() = %#v, %v; requests = %d", reply, err, len(provider.requests))
	}
	state, err := options.Store.Load(runstate.ConversationIdentity{Agent: options.Agent, Role: options.Role})
	if err != nil || !strings.Contains(state.PendingTrackerResults, "Document submission refused") {
		t.Fatalf("durable results = %#v, %v", state, err)
	}
	if _, err := session.Send(context.Background(), "Continue."); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(provider.requests[2].Prompt, "Document submission refused") {
		t.Fatal("the next turn did not receive the repeated refusal")
	}
}
