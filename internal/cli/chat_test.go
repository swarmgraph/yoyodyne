package cli

// What `yoyo chat --message` does with what it is given. The conversation half
// of this is tested where the conversation lives; what is here is the command
// line's own behavior: which of the two paths a message takes, and what each
// path writes to stdout, to stderr, and to a document a script reads.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The defect this work item exists for: `yoyo chat --message "/reports"` used to
// be said to the product manager, who cannot carry out a command, so it bought
// a confused answer with a turn the operator paid for. The provider here has no
// turn to give, which is what makes "nothing was said to it" an assertion
// rather than a claim.
func TestASlashMessageIsCarriedOutByTheHarnessRatherThanSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	provider := &recordingChatBackend{}
	session := newTestChatSession(t, provider, collectedTestReport(t))

	var stdout, stderr bytes.Buffer
	if code := runChatMessage(context.Background(), session, domain.RoleProductManager, "/reports", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "bd lint could not run") {
		t.Fatalf("stdout = %q, want the collected pile", stdout.String())
	}
	if provider.turns != 0 {
		t.Fatalf("the product manager was asked %d time(s) to carry out a command", provider.turns)
	}
}

// A script reads the command's output from a field of its own. It is not the
// reply, because nothing replied: putting it there would make a harness listing
// indistinguishable from something the product manager said.
func TestASlashMessageIsReportedAsHarnessOutputRatherThanAsAReply(t *testing.T) {
	t.Parallel()

	provider := &recordingChatBackend{}
	session := newTestChatSession(t, provider, collectedTestReport(t))

	var stdout, stderr bytes.Buffer
	if code := runChatMessage(context.Background(), session, domain.RoleProductManager, "/reports", true, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}
	var decoded chatOutput
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout.String())
	}
	if !strings.Contains(decoded.Harness, "bd lint could not run") {
		t.Fatalf("harness = %q, want the collected pile", decoded.Harness)
	}
	if decoded.Reply != "" || decoded.Error != "" {
		t.Fatalf("output = %#v, want nothing to read as something the product manager said", decoded)
	}
	if decoded.Evidence == nil || decoded.Evidence.ConversationID == "" {
		t.Fatalf("evidence = %#v, want the conversation the command was carried out in", decoded.Evidence)
	}
	if provider.turns != 0 {
		t.Fatalf("the product manager was asked %d time(s) to carry out a command", provider.turns)
	}
}

// The commands that only mean something inside a conversation are refused here
// rather than half carried out by a process that is about to exit, and the
// refusal says what does the same job from a command line.
func TestAConversationOnlyCommandIsRefusedInASingleMessage(t *testing.T) {
	t.Parallel()

	provider := &recordingChatBackend{}
	session := newTestChatSession(t, provider)

	var stdout, stderr bytes.Buffer
	code := runChatMessage(context.Background(), session, domain.RoleProductManager, "/work yoyodyne-ifd.70", false, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runChatMessage() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing to have been carried out", stdout.String())
	}
	if !strings.Contains(stderr.String(), "yoyo run <beads-id>") {
		t.Fatalf("stderr = %q, want it to name what runs a work item from a command line", stderr.String())
	}
	if provider.turns != 0 {
		t.Fatalf("a refused command was said to the product manager %d time(s)", provider.turns)
	}
}

// Everything that is not a command is still said to the product manager, which
// is the other half of the rule: the interception has to be a slash and not a
// mood.
func TestAMessageThatIsNotACommandIsStillSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	provider := &recordingChatBackend{
		result: backendapi.RunResult{SessionID: "session-1", FinalText: "The backlog holds four items."},
	}
	session := newTestChatSession(t, provider)

	var stdout, stderr bytes.Buffer
	if code := runChatMessage(context.Background(), session, domain.RoleProductManager, "what is in the backlog?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}
	if provider.turns != 1 {
		t.Fatalf("the product manager was asked %d time(s), want exactly one turn", provider.turns)
	}
	if !strings.Contains(stdout.String(), "The backlog holds four items.") {
		t.Fatalf("stdout = %q, want the answer", stdout.String())
	}
}

func TestASingleMessageReportsTheSaveBeforeCompaction(t *testing.T) {
	t.Parallel()
	remember := "I saved a conclusion.\n\n```yoyodyne-memory\n" +
		`{"memories":[{"action":"remember","memory":"slow-checks","text":"The race check needs eleven minutes."}]}` + "\n```"
	for _, test := range []struct {
		name          string
		save          string
		writes        int
		nothingToSave bool
		saveFailed    bool
		answerFailed  bool
		status        string
	}{
		{name: "recorded memory", save: remember, writes: 1, status: "recorded 1 memory write(s)"},
		{name: "nothing to save", save: "Nothing to save.", nothingToSave: true, status: "had nothing to save"},
		{name: "failed save", save: "All done.", saveFailed: true, status: "did not finish:"},
		{name: "recorded memory before a failed answer", save: remember, writes: 1, answerFailed: true, status: "recorded 1 memory write(s)"},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				store, err := runstate.NewConversationStore(root, "yoyodyne")
				if err != nil {
					t.Fatal(err)
				}
				memories, err := runstate.NewMemoryStore(root, "yoyodyne")
				if err != nil {
					t.Fatal(err)
				}
				provider := &sequencingBackend{results: []backendapi.RunResult{
					{Backend: domain.BackendClaudeCode, SessionID: "old", FinalText: "First answer."},
					{Backend: domain.BackendClaudeCode, SessionID: "old", FinalText: test.save},
					{Backend: domain.BackendClaudeCode, SessionID: "new", FinalText: "The waiting answer.", IsError: test.answerFailed},
				}}
				session, err := chat.Open(chat.Options{
					Role: domain.RoleProductManager, Agent: "product-manager",
					Backend: provider, Store: store, Memories: memories,
					Model: "opus", Provider: domain.BackendClaudeCode, AccountAlias: config.DefaultAccountAlias,
					Repository: filepath.Join(root, "repository"), ProductID: "yoyodyne", RepositoryID: "yoyodyne",
					Briefing:           chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
					SessionBudgetBytes: 1,
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := session.Send(context.Background(), "First message."); err != nil {
					t.Fatal(err)
				}
				var stdout, stderr bytes.Buffer
				code := runChatMessage(context.Background(), session, domain.RoleProductManager, "Waiting message.", format == "json", &stdout, &stderr)
				failed := test.saveFailed || test.answerFailed
				if (code != 0) != failed {
					t.Fatalf("runChatMessage() code = %d, want failure %v; stderr = %q", code, failed, stderr.String())
				}
				events, err := store.LoadEvents(session.Evidence().ConversationID)
				if err != nil {
					t.Fatal(err)
				}
				var recorded []chat.CompactionSave
				for _, event := range events {
					if event.Type == execution.EventSessionMemorySaved || event.Type == execution.EventSessionMemorySaveFailed {
						var save chat.CompactionSave
						if err := json.Unmarshal(event.Payload, &save); err != nil {
							t.Fatal(err)
						}
						recorded = append(recorded, save)
					}
				}
				if len(recorded) != 1 || recorded[0].SessionID != "old" || recorded[0].Turn != 2 ||
					recorded[0].MemoriesRecorded != test.writes || recorded[0].NothingToSave != test.nothingToSave ||
					(recorded[0].Failure != "") != test.saveFailed {
					t.Fatalf("recorded save = %+v, want the turn's outcome", recorded)
				}
				if format == "json" {
					var decoded struct {
						Reply           string                `json:"reply"`
						Error           string                `json:"error"`
						CompactionSaves []chat.CompactionSave `json:"compaction_saves"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
						t.Fatalf("Unmarshal() error = %v over %q", err, stdout.String())
					}
					if len(decoded.CompactionSaves) != 1 || decoded.CompactionSaves[0] != recorded[0] {
						t.Fatalf("JSON saves = %+v, want the recorded save %+v", decoded.CompactionSaves, recorded)
					}
					if (decoded.Error != "") != failed || (!failed && !strings.Contains(decoded.Reply, "The waiting answer.")) {
						t.Fatalf("JSON reply or failure missing: %+v", decoded)
					}
				} else {
					if strings.Count(stdout.String(), "[session] ") != 1 || !strings.Contains(stdout.String(), test.status) {
						t.Fatalf("stdout = %q, want one save outcome saying %q", stdout.String(), test.status)
					}
					if test.saveFailed && !strings.Contains(stdout.String(), recorded[0].Failure) {
						t.Fatalf("stdout = %q, want the recorded save failure %q", stdout.String(), recorded[0].Failure)
					}
					if !failed && !strings.Contains(stdout.String(), "The waiting answer.") {
						t.Fatalf("stdout = %q, want the waiting answer", stdout.String())
					}
				}
				if strings.Contains(stdout.String(), "I saved a conclusion.") || strings.Contains(stdout.String(), "yoyodyne-memory") {
					t.Fatalf("the internal save reply leaked into the output: %q", stdout.String())
				}
			})
		}
	}
}

// The defect this work item exists for: an operator approving a proposal with
// `yoyo chat --message "y"` had their approval said to the product manager as
// ordinary speech. It has no way to create the item under this project's gate,
// so the approval was spent, nothing reached the queue, and nothing said so.
//
// The approval is taken here by a second conversation reading the first one's
// record, because that is what every `--message` approval actually is: the
// process that carried the proposal exited before the operator answered it.
func TestAnApprovalSentAsASingleMessageCreatesTheProposedWorkItem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{created: beads.WorkItem{ID: "yoyodyne-ifd.200", Title: "Pause on a usage limit"}}
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}

	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what should we do about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}
	if len(tracker.creations) != 0 {
		t.Fatalf("%d item(s) were created before anybody approved anything", len(tracker.creations))
	}
	if !strings.Contains(stdout.String(), "approve 1.1") {
		t.Fatalf("stdout = %q, want it to say how the proposal is decided", stdout.String())
	}

	// A second process. Nothing of the first one survives but the record, which
	// is exactly the situation the approval failed in.
	deciding := &recordingChatBackend{}
	resumed := openTestChatSession(t, root, deciding, tracker)
	if !resumed.Resumed() {
		t.Fatalf("the second conversation did not resume the recorded one")
	}
	var decided, aside bytes.Buffer
	if code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "y", false, &decided, &aside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, aside.String())
	}
	if deciding.turns != 0 {
		t.Fatalf("the approval was said to the product manager %d time(s)", deciding.turns)
	}
	if len(tracker.creations) != 1 {
		t.Fatalf("%d item(s) were created, want the one the operator approved", len(tracker.creations))
	}
	if tracker.creations[0].Title != "Pause on a usage limit" {
		t.Fatalf("created %#v, want the proposed work", tracker.creations[0])
	}
	if !strings.Contains(decided.String(), "created yoyodyne-ifd.200") {
		t.Fatalf("stdout = %q, want the item the approval created", decided.String())
	}
	if len(resumed.Proposals()) != 0 {
		t.Fatalf("%d proposal(s) are still awaiting a decision after being decided", len(resumed.Proposals()))
	}

	// And a third process reads the same record: a decided proposal is decided
	// for everybody, so the same approval sent twice does not create the item
	// twice.
	again := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: "It is already in the queue."}}
	third := openTestChatSession(t, root, again, tracker)
	if len(third.Proposals()) != 0 {
		t.Fatalf("a decided proposal came back as pending: %#v", third.Proposals())
	}
	var repeated, repeatedAside bytes.Buffer
	if code := runChatMessage(context.Background(), third, domain.RoleProductManager, "y", false, &repeated, &repeatedAside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, repeatedAside.String())
	}
	if len(tracker.creations) != 1 {
		t.Fatalf("%d item(s) were created, want the approval to have been spent once", len(tracker.creations))
	}
}

// A document approved as a single message is the path both the README and
// `docs/artifacts.md` tell an operator to use, and it runs through a dispatch
// where the proposal decider is asked first and the two grammars share their
// verbs. What keeps them apart is the `document-` prefix, so this exercises the
// dispatch itself with a proposal pending at the same time: an approval that
// names a document must not be consumed by the proposal decider, refused by it,
// or said to the product manager as speech.
func TestADocumentApprovedAsASingleMessageIsWrittenAndLeavesTheProposalAlone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repository := t.TempDir()
	tracker := &recordingChatTracker{created: beads.WorkItem{ID: "yoyodyne-ifd.200", Title: "Pause on a usage limit"}}
	writing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalAndDocumentReply}}

	var stdout, stderr bytes.Buffer
	wrote := openTestDocumentSession(t, root, repository, writing, tracker)
	if code := runChatMessage(context.Background(), wrote, domain.RoleProductManager, "write the goals up and say what else is needed", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}
	if len(wrote.Proposals()) != 1 || len(wrote.Writes()) != 1 {
		t.Fatalf("%d proposal(s) and %d document(s) are waiting, want one of each", len(wrote.Proposals()), len(wrote.Writes()))
	}
	if !strings.Contains(stdout.String(), "approve document-1.1") {
		t.Fatalf("stdout = %q, want it to say how the document is decided", stdout.String())
	}
	document := filepath.Join(repository, "docs", "product", "v2-goals.md")
	if _, err := os.Stat(document); !os.IsNotExist(err) {
		t.Fatalf("the document was written before anybody approved it: %v", err)
	}

	// A second process, holding nothing but the record — which is the situation
	// the operator actually approves in.
	deciding := &recordingChatBackend{}
	resumed := openTestDocumentSession(t, root, repository, deciding, tracker)
	var decided, aside bytes.Buffer
	if code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "approve document-1.1", false, &decided, &aside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, aside.String())
	}
	if deciding.turns != 0 {
		t.Fatalf("the approval was said to the product manager %d time(s)", deciding.turns)
	}
	if _, err := os.Stat(document); err != nil {
		t.Fatalf("the approved document is not in the repository: %v", err)
	}
	if !strings.Contains(decided.String(), "wrote v2-goals") {
		t.Fatalf("stdout = %q, want what the approval wrote", decided.String())
	}
	// The proposal beside it was never the operator's subject, and the proposal
	// decider — which runs first — neither took the answer nor failed on it.
	if len(tracker.creations) != 0 {
		t.Fatalf("approving a document created %d work item(s)", len(tracker.creations))
	}
	if len(resumed.Proposals()) != 1 {
		t.Fatalf("%d proposal(s) pending, want the undecided one left where it was", len(resumed.Proposals()))
	}
	if len(resumed.Writes()) != 0 {
		t.Fatalf("%d document(s) still waiting after being written", len(resumed.Writes()))
	}

	// And the same dispatch the other way round: with a document decided and the
	// proposal still open, a proposal approval still reaches the proposal decider.
	third := openTestDocumentSession(t, root, repository, &recordingChatBackend{}, tracker)
	var created, unused bytes.Buffer
	if code := runChatMessage(context.Background(), third, domain.RoleProductManager, "approve 1.1", false, &created, &unused); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, unused.String())
	}
	if len(tracker.creations) != 1 {
		t.Fatalf("%d item(s) were created, want the proposal the operator approved", len(tracker.creations))
	}
}

// A decline sent as a single message decides the proposal too, and keeps the
// operator's own words as the reason. Nothing is created either way, which is
// why this is the half of the rule that must not silently do nothing: a
// proposal nobody decided is one that comes back, and a declined one does not.
func TestADeclineSentAsASingleMessageDecidesTheProposal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	deciding := &recordingChatBackend{}
	resumed := openTestChatSession(t, root, deciding, tracker)
	var decided, aside bytes.Buffer
	if code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "decline 1.1 we already handle this", true, &decided, &aside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, aside.String())
	}
	var decoded chatOutput
	if err := json.Unmarshal(decided.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, decided.String())
	}
	if len(decoded.Decisions) != 1 || decoded.Decisions[0].Approved {
		t.Fatalf("decisions = %#v, want the proposal declined", decoded.Decisions)
	}
	if !strings.Contains(decoded.Decisions[0].Reason, "we already handle this") {
		t.Fatalf("reason = %q, want the operator's own words", decoded.Decisions[0].Reason)
	}
	if len(decoded.Pending) != 0 {
		t.Fatalf("pending = %#v, want nothing still waiting", decoded.Pending)
	}
	if len(tracker.creations) != 0 {
		t.Fatalf("a decline created %d item(s)", len(tracker.creations))
	}
	if deciding.turns != 0 {
		t.Fatalf("the decline was said to the product manager %d time(s)", deciding.turns)
	}
}

// The other half of the rule. A prompt may read anything it cannot understand
// as a decline, because it asked; a single message was never asked anything, so
// speech is speech and the proposal stays on the table.
func TestAMessageThatIsNotADecisionIsStillSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	answering := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: "Two of them are about capacity."}}
	resumed := openTestChatSession(t, root, answering, tracker)
	var said, aside bytes.Buffer
	if code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "what else is open?", false, &said, &aside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, aside.String())
	}
	if answering.turns != 1 {
		t.Fatalf("the product manager was asked %d time(s), want the message said to it", answering.turns)
	}
	if len(resumed.Proposals()) != 1 {
		t.Fatalf("%d proposal(s) are pending, want the undecided one left exactly where it was", len(resumed.Proposals()))
	}
	if len(tracker.creations) != 0 {
		t.Fatalf("speech created %d item(s)", len(tracker.creations))
	}
}

// A reply that merely opens with a decision word is speech, and speech reaches
// the product manager. At a prompt the operator has just been asked, so whatever
// follows their verb is about the question; here the proposal may be hours and
// several messages old and they are usually talking. Turning down work they
// never mentioned, and never saying their message to anybody either, would be
// this item's own defect pointing the other way.
func TestAConversationalReplyOpeningWithADecisionWordIsSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	answering := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: "The resolver is yoyodyne-ifd.108."}}
	resumed := openTestChatSession(t, root, answering, tracker)
	var said, aside bytes.Buffer
	if code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "no, let's look at the resolver instead", false, &said, &aside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, aside.String())
	}
	if answering.turns != 1 {
		t.Fatalf("the product manager was asked %d time(s), want the message said to it", answering.turns)
	}
	if !strings.Contains(said.String(), "The resolver is yoyodyne-ifd.108.") {
		t.Fatalf("stdout = %q, want the answer", said.String())
	}
	if len(resumed.Proposals()) != 1 {
		t.Fatalf("%d proposal(s) pending, want the one nobody decided left where it was", len(resumed.Proposals()))
	}
	if len(tracker.creations) != 0 {
		t.Fatalf("speech created %d item(s)", len(tracker.creations))
	}
}

// An approval naming a proposal this conversation no longer holds says so. The
// failure this guards is the quiet one: an identifier that has expired, or that
// was mistyped, being passed on to the product manager as a sentence to
// interpret, which is how an approval goes missing without anybody being told.
func TestADecisionNamingAProposalThatIsNotThereSaysSoRatherThanBeingSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	deciding := &recordingChatBackend{}
	resumed := openTestChatSession(t, root, deciding, tracker)
	var decided, aside bytes.Buffer
	code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "approve 9.9", false, &decided, &aside)
	if code != 1 {
		t.Fatalf("runChatMessage() code = %d, want 1; stderr = %q", code, aside.String())
	}
	if !strings.Contains(aside.String(), "9.9") {
		t.Fatalf("stderr = %q, want it to name what could not be decided", aside.String())
	}
	if deciding.turns != 0 {
		t.Fatalf("a decision the harness could not carry out was said to the product manager %d time(s)", deciding.turns)
	}
	if len(tracker.creations) != 0 {
		t.Fatalf("a refused decision created %d item(s)", len(tracker.creations))
	}
	if len(resumed.Proposals()) != 1 {
		t.Fatalf("%d proposal(s) are pending, want the undecided one still waiting", len(resumed.Proposals()))
	}
}

// The same holds when nothing is awaiting a decision at all, which is the case
// it most needs to hold in: an operator approving a proposal that a second
// process decided while they were away is exactly the operator whose approval
// went missing before. Their message names a proposal, so it is answered here
// rather than bought as a turn from a product manager who cannot act on it.
func TestAnApprovalOfAnAlreadyDecidedProposalIsRefusedRatherThanSaidToTheProductManager(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{created: beads.WorkItem{ID: "yoyodyne-ifd.200"}}
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	// Somebody else decides it, and the operator does not see that happen.
	deciding := openTestChatSession(t, root, &recordingChatBackend{}, tracker)
	var swallowed, quiet bytes.Buffer
	if code := runChatMessage(context.Background(), deciding, domain.RoleProductManager, "y", false, &swallowed, &quiet); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, quiet.String())
	}

	late := &recordingChatBackend{}
	stale := openTestChatSession(t, root, late, tracker)
	var out, aside bytes.Buffer
	code := runChatMessage(context.Background(), stale, domain.RoleProductManager, "approve 1.1", false, &out, &aside)
	if code != 1 {
		t.Fatalf("runChatMessage() code = %d, want 1; stderr = %q", code, aside.String())
	}
	if late.turns != 0 {
		t.Fatalf("an approval of a decided proposal was said to the product manager %d time(s)", late.turns)
	}
	if !strings.Contains(aside.String(), "1.1") {
		t.Fatalf("stderr = %q, want it to name the proposal that is no longer awaiting a decision", aside.String())
	}
	if len(tracker.creations) != 1 {
		t.Fatalf("%d item(s) were created, want the approval spent exactly once", len(tracker.creations))
	}
}

// An approval the tracker will not carry out reports that nothing was created
// and that the proposal is still waiting, which is what tells the operator to
// try again rather than that the item exists.
func TestAnApprovalTheTrackerRefusesIsReportedAsCreatingNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	var stdout, stderr bytes.Buffer
	proposed := openTestChatSession(t, root, proposing, &recordingChatTracker{})
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	unreachable := &recordingChatTracker{createErr: errors.New("bd is unreachable")}
	resumed := openTestChatSession(t, root, &recordingChatBackend{}, unreachable)
	var decided, aside bytes.Buffer
	if code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "y", true, &decided, &aside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, aside.String())
	}
	var decoded chatOutput
	if err := json.Unmarshal(decided.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, decided.String())
	}
	if len(decoded.Decisions) != 1 || decoded.Decisions[0].WorkItemID != "" || !decoded.Decisions[0].Undecided {
		t.Fatalf("decisions = %#v, want an approval that created nothing", decoded.Decisions)
	}
	if !strings.Contains(decoded.Decisions[0].Problem, "bd is unreachable") {
		t.Fatalf("problem = %q, want what the tracker said", decoded.Decisions[0].Problem)
	}
	// And it is still on the table, so a script reading this knows what to ask
	// for again rather than having to work out whether the item exists.
	if len(decoded.Pending) != 1 || decoded.Pending[0].ID != "1.1" {
		t.Fatalf("pending = %#v, want the refused proposal still awaiting a decision", decoded.Pending)
	}
}

// An ordinary turn reports everything still awaiting a decision, not only what
// it just proposed. The text output has always listed the whole of it, because a
// decision arrives as its own message and what the operator has to be able to
// name is everything waiting on them; a JSON document that reported less would
// hide the earlier proposals from the reader least able to go looking — a script,
// which has nothing but this document.
func TestAnOrdinaryMessageReportsEverythingStillAwaitingADecision(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}
	var stdout, stderr bytes.Buffer
	first := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	proposed := openTestChatSession(t, root, first, tracker)
	if code := runChatMessage(context.Background(), proposed, domain.RoleProductManager, "what about usage limits?", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	// A second turn that proposes nothing at all. The proposal from the first is
	// still waiting, and this document is the only place a script would see it.
	quiet := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: "Nothing further for now."}}
	resumed := openTestChatSession(t, root, quiet, tracker)
	var out, aside bytes.Buffer
	if code := runChatMessage(context.Background(), resumed, domain.RoleProductManager, "anything else?", true, &out, &aside); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, aside.String())
	}
	var decoded chatOutput
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, out.String())
	}
	if len(decoded.Proposals) != 0 {
		t.Fatalf("proposals = %#v, want nothing proposed by this turn", decoded.Proposals)
	}
	if len(decoded.Pending) != 1 || decoded.Pending[0].ID != "1.1" {
		t.Fatalf("pending = %#v, want the earlier proposal a script would decide next", decoded.Pending)
	}
	if decoded.Pending[0].Proposal.Title != "Pause on a usage limit" {
		t.Fatalf("pending = %#v, want what the operator was shown", decoded.Pending[0].Proposal)
	}
}

// A command that recorded something and then failed to report it recorded it
// all the same, so what it printed is written before the failure rather than
// lost behind it — and the failure is still the command's, which is what the
// exit code says.
func TestAFailedCommandStillWritesWhatItDid(t *testing.T) {
	t.Parallel()

	evidence := chat.Evidence{ConversationID: "chat-0123456789abcdef0123456789abcdef"}
	rendered := "recorded your direction on yoyodyne-ifd.70; the next attempt at it reads it.\n"
	failure := errors.New("record direction on yoyodyne-ifd.70 where the work is tracked: bd is unreachable")

	var stdout, stderr bytes.Buffer
	if code := reportChatCommand(&stdout, &stderr, false, evidence, rendered, failure); code != 1 {
		t.Fatalf("reportChatCommand() code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "recorded your direction") {
		t.Fatalf("stdout = %q, want what the command did", stdout.String())
	}
	if !strings.Contains(stderr.String(), "bd is unreachable") {
		t.Fatalf("stderr = %q, want the failure", stderr.String())
	}

	// The document says both too, and a caller reading it can tell a command that
	// half-succeeded from one that did nothing.
	stdout.Reset()
	stderr.Reset()
	if code := reportChatCommand(&stdout, &stderr, true, evidence, rendered, failure); code != 1 {
		t.Fatalf("reportChatCommand() code = %d, want 1", code)
	}
	var decoded chatOutput
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout.String())
	}
	if !strings.Contains(decoded.Harness, "recorded your direction") || !strings.Contains(decoded.Error, "bd is unreachable") {
		t.Fatalf("output = %#v, want both what it did and what failed", decoded)
	}
}

// newTestChatSession builds a conversation over a real conversation store and a
// provider that has to be asked before it answers, so what is tested is the
// command line's own behavior rather than a stand-in for the conversation.
// The conversation counterpart of the answering voice's pooled case: under a
// pool there is no configuration-wide account, so what serves a conversation is
// the account its own agent is configured for. This drives the same decision
// `openChat` makes and carries it the whole way to the durable record and to the
// turn's cost line, so what is asserted is what a pooled project actually
// records rather than an alias a test picked.
func TestAPooledConversationRecordsTheAccountItsAgentAnswersOn(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	cfg := answeringConfig()
	cfg.Accounts = map[string]config.Account{"personal": {}, "research": {}}
	// One agent names its account and the other names none. Neither may end up on
	// the machine's default, because under a pool there is no such account to
	// fall back to.
	architect := cfg.Agents["architect"]
	architect.Account = "research"
	cfg.Agents["architect"] = architect

	for _, test := range []struct {
		agent string
		want  string
	}{
		{agent: "architect", want: "research"},
		{agent: "product-manager", want: "personal"},
	} {
		t.Run(test.agent, func(t *testing.T) {
			t.Parallel()

			account, err := conversationAccount(cfg, stateRoot, test.agent)
			if err != nil {
				t.Fatalf("conversationAccount() error = %v", err)
			}
			if account.Alias != test.want {
				t.Fatalf("account = %+v, want the agent's own account %q", account, test.want)
			}
			// The provider home follows the same account, so the login the turn runs
			// under and the alias the record names are one decision rather than two.
			if account.Directory != config.AccountConfigDirectory(stateRoot, test.want) {
				t.Fatalf("account = %+v, want the provider home of %q", account, test.want)
			}
		})
	}

	// And what that resolution lands on: the conversation record and the line the
	// turn puts in the cost log both name the answering agent's own account and
	// the configuration that was in force.
	account, err := conversationAccount(cfg, stateRoot, "architect")
	if err != nil {
		t.Fatalf("conversationAccount() error = %v", err)
	}
	root := t.TempDir()
	store, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	log := &recordingSpendLog{}
	provider := &recordingChatBackend{result: backendapi.RunResult{
		SessionID:     "session-1",
		ResolvedModel: "claude-opus-5",
		FinalText:     "More than the ordering assumes.",
		CostUSD:       0.25,
		CostReported:  true,
	}}
	session, err := chat.Open(chat.Options{
		Role:         domain.RoleArchitect,
		Agent:        "architect",
		Backend:      provider,
		Store:        store,
		Spend:        log,
		Model:        "opus-architect",
		Provider:     domain.BackendClaudeCode,
		Repository:   filepath.Join(root, "repository"),
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
		// The two lines `openChat` writes from the same resolution.
		AccountAlias:   account.Alias,
		ConfigRevision: cfg.Revision(),
	})
	if err != nil {
		t.Fatalf("chat.Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "What does this goal cost?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	recorded, err := store.Load(runstate.ConversationIdentity{Agent: "architect", Role: domain.RoleArchitect})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.AccountAlias != "research" || recorded.ConfigRevision != cfg.Revision() {
		t.Fatalf("recorded conversation = %#v, want the architect's own account and the live configuration", recorded)
	}
	if len(log.lines) != 1 {
		t.Fatalf("recorded %d cost line(s), want one for the turn: %#v", len(log.lines), log.lines)
	}
	if log.lines[0].AccountAlias != "research" || log.lines[0].ConfigRevision != cfg.Revision() {
		t.Fatalf("cost line = %#v, want the same account and configuration the record names", log.lines[0])
	}
}

func newTestChatSession(t *testing.T, provider chat.Backend, reports ...report.Report) *chat.Session {
	t.Helper()

	root := t.TempDir()
	store, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	collected, err := runstate.NewReportStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	for _, reported := range reports {
		if err := collected.Append(reported); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	session, err := chat.Open(chat.Options{
		Role:         domain.RoleProductManager,
		Agent:        "product-manager",
		Backend:      provider,
		Store:        store,
		Reports:      collected,
		Model:        "opus",
		Provider:     domain.BackendClaudeCode,
		AccountAlias: config.DefaultAccountAlias,
		Repository:   filepath.Join(root, "repository"),
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("chat.Open() error = %v", err)
	}
	return session
}

// proposalReply is one answer that proposes a single work item and creates
// nothing, which is what this project's gate makes of every proposal: the
// operator decides it.
const proposalReply = `We could wait out the limit rather than failing the run.

` + "```yoyodyne-proposal" + `
{"items":[{"title":"Pause on a usage limit","description":"Wait for the window and resume.","rationale":"Capacity is not failure.","goal":"Run development nearly autonomously."}]}
` + "```" + `
`

// proposalAndDocumentReply is one answer that both proposes work and writes a
// document, which is what puts the two decision grammars on the table at once.
const proposalAndDocumentReply = proposalReply + `
Here is the goals document as we agreed it.

` + "```yoyodyne-artifact" + `
{"documents":[{"action":"create","id":"v2-goals","kind":"goals","title":"What v2 is for","directory":"docs/product","body":"# Goals\n\nRun development nearly autonomously.","reason":"drafted with the operator"}]}
` + "```" + `
`

func TestConsistentRulesWrittenInConversationAreListedAsApproved(t *testing.T) {
	t.Parallel()
	configPath := writeConfig(t, validConfig)
	provider := &recordingChatBackend{result: backendapi.RunResult{
		SessionID: "session-1", FinalText: "The rules are recorded.\n\n" + artifact.WriteFence + `
{"documents":[{"action":"create","id":"operating-rules","kind":"rules","title":"Operating rules","directory":"docs/product","body":"## Rules\n\n- Notes are append-only.","intent":"consistent","reason":"yoyodyne-ifd.433.19 - records rules the operator already gave"}]}
` + "```\n",
	}}
	session := openTestDocumentSession(t, t.TempDir(), filepath.Dir(configPath), provider, nil)
	var written, problems bytes.Buffer
	if code := runChatMessage(context.Background(), session, domain.RoleProductManager, "Record the operating rules.", false, &written, &problems); code != 0 ||
		len(session.Writes()) != 0 || !strings.Contains(written.String(), "delegated authority, consistent with intent") ||
		strings.Contains(written.String(), "your approval recorded") {
		t.Fatalf("conversation write: code %d, stdout %q, stderr %q", code, written.String(), problems.String())
	}
	stdout, stderr, code := runCLI(t, "artifact", "list", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "approved as it stands") ||
		!strings.Contains(stdout, "recorded by the Lead Product Manager") ||
		strings.Contains(stdout, "yours to approve") || strings.Contains(stdout, "given by the operator") {
		t.Fatalf("rules listing: code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// openTestDocumentSession is openTestChatSession with an artifact store behind
// it, over a repository the caller owns so a test can look at what was written.
func openTestDocumentSession(t *testing.T, root, repository string, provider chat.Backend, tracker chat.Tracker) *chat.Session {
	t.Helper()

	store, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	session, err := chat.Open(chat.Options{
		Role:    domain.RoleProductManager,
		Agent:   "product-manager",
		Backend: provider,
		Store:   store,
		Tracker: tracker,
		// The same assembly `yoyo chat` builds, so the dispatch is exercised over
		// the store an operator actually has.
		Documents: artifactStore(repository, config.Product{
			Specifications: config.DefaultSpecifications,
			Designs:        config.DefaultDesigns,
			Decisions:      config.DefaultDecisions,
			Invariants:     config.DefaultInvariants,
		}),
		Model:        "opus",
		Provider:     domain.BackendClaudeCode,
		AccountAlias: config.DefaultAccountAlias,
		Repository:   repository,
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("chat.Open() error = %v", err)
	}
	return session
}

// openTestChatSession builds a conversation over a state root the caller owns,
// so two of them are two processes talking to one recorded conversation — which
// is what a proposal made by one `--message` and decided by the next actually
// is.
func openTestChatSession(t *testing.T, root string, provider chat.Backend, tracker chat.Tracker) *chat.Session {
	t.Helper()

	store, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	session, err := chat.Open(chat.Options{
		Role:         domain.RoleProductManager,
		Agent:        "product-manager",
		Backend:      provider,
		Store:        store,
		Tracker:      tracker,
		Model:        "opus",
		Provider:     domain.BackendClaudeCode,
		AccountAlias: config.DefaultAccountAlias,
		Repository:   filepath.Join(root, "repository"),
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("chat.Open() error = %v", err)
	}
	return session
}

// recordingChatTracker is a work tracker that remembers what it was asked to
// create. An item it never recorded is what proves nothing reached the queue.
type recordingChatTracker struct {
	created   beads.WorkItem
	creations []beads.NewWorkItem
	// createErr is a tracker that will not create what it was asked to, which is
	// what makes "nothing was created and the proposal is still waiting" an
	// assertion rather than a claim.
	createErr error
}

func (t *recordingChatTracker) Show(context.Context, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("this test asked the tracker for an item it does not hold")
}

func (t *recordingChatTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	return nil, nil
}

func (t *recordingChatTracker) Create(_ context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	if t.createErr != nil {
		return beads.WorkItem{}, t.createErr
	}
	t.creations = append(t.creations, item)
	created := t.created
	if created.Title == "" {
		created.Title = item.Title
	}
	return created, nil
}

func (t *recordingChatTracker) Update(context.Context, string, beads.WorkItemChange) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("this test did not expect an update")
}

func (t *recordingChatTracker) Block(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("this test did not expect a block")
}

func (t *recordingChatTracker) Unblock(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("this test did not expect a status to be cleared")
}

func (t *recordingChatTracker) AddBlocker(context.Context, string, string) error    { return nil }
func (t *recordingChatTracker) RemoveBlocker(context.Context, string, string) error { return nil }

func (t *recordingChatTracker) Complete(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("this test did not expect a completion")
}

func collectedTestReport(t *testing.T) report.Report {
	t.Helper()

	return report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            "report-0123456789abcdef0123456789abcdef",
		Role:          "developer",
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.70",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityCritical,
		Message:       "bd lint could not run in its sandbox, so nothing linted the item",
		RecordedAt:    time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC),
	}
}

// recordingChatBackend counts what the product manager was asked. A turn it was
// never given is what proves a command never reached it, so the zero value
// answers nothing at all.
type recordingChatBackend struct {
	result backendapi.RunResult
	turns  int
}

func (b *recordingChatBackend) Run(_ context.Context, _ backendapi.RunRequest) (backendapi.RunResult, error) {
	b.turns++
	if b.result.SessionID == "" {
		return backendapi.RunResult{}, errors.New("the product manager was asked something this test did not expect")
	}
	result := b.result
	result.Backend = domain.BackendClaudeCode
	return result, nil
}

// A crossing this conversation cannot make leaves it with no alternate at all,
// rather than with the alternate's model and none of the rest of it.
//
// The two halves are one answer, and splitting them is the failure the whole
// arrangement exists to prevent: the alternate's model belongs to the other
// provider, so handing it over on its own asks the endpoint whose window just
// closed for a selector it has never heard of — at exactly the moment the
// fallback was supposed to save the turn.
func TestAnUnresolvedCrossingLeavesTheConversationWithNoAlternateAtAll(t *testing.T) {
	cfg := config.Config{
		Accounts: map[string]config.Account{"default": {Provider: domain.BackendClaudeCode}},
		Agents: map[string]config.AgentConfig{
			"developer": {
				Role:    domain.RoleDeveloper,
				Backend: domain.BackendClaudeCode,
				Model:   "fable",
				Failover: config.Failover{
					Enabled:  true,
					Model:    "gpt-5-codex",
					Provider: domain.BackendCodex,
					// An account no longer declared — an alias edited out from under a
					// harness that is already running, which is the ordinary way this
					// resolution comes to fail after the file has loaded.
					Account: "retired-codex-account",
				},
			},
		},
	}
	var stderr strings.Builder
	// No runner: neither path here builds an adapter, and one supplied would
	// only hide that.
	alternate := conversationFailover(cfg, t.TempDir(), "developer", nil, &stderr)
	if alternate.model != "" {
		t.Fatalf("alternate model = %q, want none: %q belongs to a provider this conversation cannot reach",
			alternate.model, alternate.model)
	}
	if alternate.endpoint.Provider != "" || alternate.backend != nil || alternate.configDir != "" {
		t.Fatalf("alternate = %#v, want nothing where the crossing could not be resolved", alternate)
	}
	if !strings.Contains(stderr.String(), "cannot fail over at all") {
		t.Fatalf("warning = %q, want it to say the conversation has no failover rather than a partial one", stderr.String())
	}
}

// The same resolution answers the ordinary case, so an agent whose alternate
// stays on its own provider still gets one and needs no endpoint to reach it.
func TestAWithinProviderAlternateNeedsNoEndpointToResolve(t *testing.T) {
	cfg := config.Config{
		Accounts: map[string]config.Account{"default": {Provider: domain.BackendClaudeCode}},
		Agents: map[string]config.AgentConfig{
			"development-manager": {
				Role:     domain.RoleDevelopmentManager,
				Backend:  domain.BackendClaudeCode,
				Model:    "fable",
				Failover: config.Failover{Enabled: true, Model: "opus"},
			},
		},
	}
	var stderr strings.Builder
	alternate := conversationFailover(cfg, t.TempDir(), "development-manager", nil, &stderr)
	if alternate.model != "opus" {
		t.Fatalf("alternate model = %q, want the alternate on the agent's own provider", alternate.model)
	}
	if alternate.endpoint.Provider != "" || alternate.backend != nil {
		t.Fatalf("alternate = %#v, want no crossing wired for a substitution that does not leave the provider", alternate)
	}
	if stderr.String() != "" {
		t.Fatalf("warning = %q, want none: nothing failed to resolve", stderr.String())
	}
}

// The operator's own `yoyo chat` — interactive or --message — waits out a
// provider with no capacity under the bounds a run waits under, read from the
// same configuration. A turn the harness takes for itself is given no bounds at
// all: a stopped run delivered to the development manager, a recurring firing,
// and a correction each already pace themselves on the refusal, and one that
// slept through the window would hold the scheduler that took it for hours.
func TestOnlyTheOperatorsOwnConversationWaitsOutAUsageLimit(t *testing.T) {
	t.Parallel()

	var cfg config.Config
	cfg.Execution.UsageLimitMaxPause = config.Duration(6 * time.Hour)
	cfg.Execution.UsageLimitInProcessPause = config.Duration(time.Hour)

	attended := usageLimitPause(cfg, true)
	if attended.Maximum != 6*time.Hour || attended.InProcess != time.Hour {
		t.Fatalf("attended pause = %+v, want the run's own bounds", attended)
	}
	if background := usageLimitPause(cfg, false); background != (chat.UsageLimitPause{}) {
		t.Fatalf("background pause = %+v, want a turn the harness takes for itself to wait for nothing", background)
	}
}
