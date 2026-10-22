package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// A proposed change decided from the dashboard is recorded exactly as
// `yoyo amendment approve` records it — the operator deciding under the
// owner's authority — and is then no longer waiting, so a second press is
// refused rather than decided twice. A decline without a reason is refused by
// the record itself, as the command line's is. Only a proposal the attention
// line names as the operator's can be found at all: a change to the goals that
// does not say it keeps their intent is, and an ordinary one, the architect's
// to move even where the architect argued it on a pass, is refused as not
// waiting on him.
func TestTheDashboardDecidesAProposedChangeAsTheCommandLineDoes(t *testing.T) {
	configPath, _ := amendmentProject(t)
	proposal := recordProposal(t, "amendment-0123456789abcdef0123456789abcdef", func(p *amendment.Proposal) {
		p.Artifact, p.Kind, p.Owner = "v1-goals", artifact.KindGoals, domain.RoleProductManager
	})
	ordinary := recordProposal(t, "amendment-fedcba9876543210fedcba9876543210")
	stateRoot, err := resolvedTestStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	runs, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	if err := runs.Sweeps().Append(runstate.Sweep{
		SchemaVersion: runstate.SweepSchemaVersion, ProductID: "yoyodyne", Task: "architect-amendments", Role: domain.RoleArchitect,
		StartedAt: at, EndedAt: at.Add(time.Minute), Turns: 1,
		Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "argued one", Recommendations: []sweep.Recommendation{
			{Proposal: ordinary.ID, Verdict: sweep.RecommendApprove, Reason: "the ordering was never settled"},
		}},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	actor := newDashboardActor(context.Background(), configPath, io.Discard)

	if _, err := actor.Find(context.Background(), readmodel.DecisionAmendment, "", ordinary.ID); !errors.Is(err, dashboard.ErrNotWaiting) {
		t.Fatalf("Find() on an ordinary proposal = %v, want ErrNotWaiting: it is the architect's to move, argued or not", err)
	}
	found, err := actor.Find(context.Background(), readmodel.DecisionAmendment, "", proposal.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if _, err := actor.Decide(context.Background(), found, false, ""); err == nil || !strings.Contains(err.Error(), "records why it was turned down") {
		t.Fatalf("a decline with no reason = %v, want the record's own refusal", err)
	}
	said, err := actor.Decide(context.Background(), found, true, "")
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if !strings.Contains(said, "approved the proposed change "+proposal.ID) || !strings.Contains(said, "the Lead Product Manager makes the change") {
		t.Fatalf("Decide() said %q", said)
	}

	store, err := runstate.NewAmendmentStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	decision, decided := amendment.DecisionOn(records, proposal.ID)
	if !decided || decision.Verdict != amendment.VerdictApproved || decision.Decider != amendment.DeciderOperator || decision.Authority != domain.RoleProductManager {
		t.Fatalf("the recorded decision = %+v (decided %v)", decision, decided)
	}
	if _, err := actor.Find(context.Background(), readmodel.DecisionAmendment, "", proposal.ID); !errors.Is(err, dashboard.ErrNotWaiting) {
		t.Fatalf("Find() after deciding = %v, want ErrNotWaiting", err)
	}
	// The command line agrees it is decided, and still lists the ordinary one.
	if stdout, _, code := runCLI(t, "amendment", "list", "--config", configPath); code != 0 || strings.Contains(stdout, proposal.ID) || !strings.Contains(stdout, ordinary.ID) {
		t.Fatalf("amendment list after the dashboard decided = %q (code %d)", stdout, code)
	}
}

// A document or proposed work is decided by the message the conversation's
// own verb reads, naming it, with a decline's reason after it.
func TestTheDashboardDecidesInTheConversationByTheMessageItsVerbReads(t *testing.T) {
	t.Parallel()
	actor := newDashboardActor(context.Background(), "unused", io.Discard)
	var sent []string
	actor.decideInConversation = func(_ context.Context, _ string, decision readmodel.OperatorDecision, message string, _ io.Writer) (string, error) {
		sent = append(sent, decision.Agent+": "+message)
		return "decided", nil
	}
	document := readmodel.OperatorDecision{Kind: readmodel.DecisionDocument, ID: "document-4.1", Role: domain.RoleProductManager, Agent: "product-manager"}
	work := readmodel.OperatorDecision{Kind: readmodel.DecisionProposal, ID: "6.1", Role: domain.RoleProductManager, Agent: "product-manager"}
	for _, each := range []struct {
		decision readmodel.OperatorDecision
		approve  bool
		reason   string
	}{{document, true, "ignored on an approval"}, {document, false, " not yet "}, {work, false, ""}} {
		if _, err := actor.Decide(context.Background(), each.decision, each.approve, each.reason); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"product-manager: approve document-4.1", "product-manager: decline document-4.1 not yet", "product-manager: decline 6.1"}
	if strings.Join(sent, "|") != strings.Join(want, "|") {
		t.Fatalf("sent %q, want %q", sent, want)
	}
}

// A document declined from the page leaves the list, because the decline is
// carried out in the conversation by the code that saves its record: the
// conversation's saved record no longer holds it as waiting, and the same
// reading the page and the attention line make no longer finds it, read as a
// later process would read it. Declining it again is refused as not waiting
// rather than recorded twice, and nothing is written to the repository.
func TestADocumentDeclinedFromThePageLeavesTheListAndTheSavedRecord(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repository := t.TempDir()
	tracker := &recordingChatTracker{created: beads.WorkItem{ID: "yoyodyne-ifd.200", Title: "Pause on a usage limit"}}
	writing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalAndDocumentReply}}
	var stdout, stderr bytes.Buffer
	wrote := openTestDocumentSession(t, root, repository, writing, tracker)
	if code := runChatMessage(context.Background(), wrote, domain.RoleProductManager, "write the goals up", false, &stdout, &stderr); code != 0 {
		t.Fatalf("runChatMessage() code = %d, stderr = %q", code, stderr.String())
	}

	store, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	sources := readmodel.Sources{Conversations: store}
	found, ok, _ := readmodel.FindOperatorDecision(sources, readmodel.DecisionDocument, "product-manager", "document-1.1")
	if !ok {
		t.Fatal("the written goals document is not on the operator's list before it is decided")
	}

	// A second process holding nothing but the record, as the dashboard is.
	deciding := &recordingChatBackend{}
	session := openTestDocumentSession(t, root, repository, deciding, tracker)
	said, err := decideInSession(context.Background(), session, found, "decline document-1.1 not what we agreed")
	if err != nil {
		t.Fatalf("decideInSession() error = %v", err)
	}
	if !strings.Contains(said, "document-1.1") {
		t.Fatalf("decideInSession() said %q", said)
	}
	if deciding.turns != 0 {
		t.Fatalf("the decline was said to the Lead Product Manager %d time(s)", deciding.turns)
	}

	if _, ok, _ := readmodel.FindOperatorDecision(sources, readmodel.DecisionDocument, "product-manager", "document-1.1"); ok {
		t.Fatal("the declined document is still on the operator's list")
	}
	recorded, err := store.Load(runstate.ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatal(err)
	}
	for _, write := range recorded.PendingWrites {
		if write.ID == "document-1.1" {
			t.Fatalf("the conversation's saved record still holds the declined document as waiting: %+v", write)
		}
	}
	if _, err := os.Stat(filepath.Join(repository, "docs", "product", "v2-goals.md")); !os.IsNotExist(err) {
		t.Fatalf("a declined document was written: %v", err)
	}

	again := openTestDocumentSession(t, root, repository, &recordingChatBackend{}, tracker)
	if _, err := decideInSession(context.Background(), again, found, "decline document-1.1 twice"); err == nil {
		t.Fatal("a second decline of the same document was not refused")
	}
}

// Asking first sends the question to the conversation of the role that raised
// the decision, or the Lead Product Manager's where that role has none here,
// as one message naming what it is about, and decides nothing.
func TestAskingFirstReachesTheRoleThatRaisedItAndDecidesNothing(t *testing.T) {
	t.Parallel()
	configPath := writeConfig(t, validConfig)
	actor := newDashboardActor(context.Background(), configPath, io.Discard)
	var mu sync.Mutex
	asked := make(chan string, 2)
	actor.converse = func(_ context.Context, role domain.AgentRole, request conversationRequest, _ io.Reader, _, _ io.Writer) int {
		mu.Lock()
		defer mu.Unlock()
		asked <- string(role) + "|" + request.agentName + "|" + request.message
		return 0
	}
	actor.decideInConversation = func(context.Context, string, readmodel.OperatorDecision, string, io.Writer) (string, error) {
		t.Fatal("asking decided something")
		return "", nil
	}

	change := readmodel.OperatorDecision{Kind: readmodel.DecisionAmendment, ID: "amendment-0123456789abcdef0123456789abcdef", Label: "proposed document change", Subject: "v1-design", AskRole: domain.RoleDeveloper, AskAgent: "developer"}
	said, err := actor.Ask(context.Background(), change, "which section?")
	if err != nil {
		t.Fatal(err)
	}
	got := waitFor(t, asked)
	if got != "developer|developer|Before deciding proposed document change amendment-0123456789abcdef0123456789abcdef (v1-design), the operator asks from the dashboard: which section?" {
		t.Fatalf("asked %q", got)
	}
	if !strings.Contains(said, "asked the developer") || !strings.Contains(said, "yoyo agent chat developer") || !strings.Contains(said, "nothing was decided") {
		t.Fatalf("Ask() said %q", said)
	}

	nobody := change
	nobody.AskRole, nobody.AskAgent = domain.RoleArchitect, "architect"
	said, err = actor.Ask(context.Background(), nobody, "why?")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitFor(t, asked); !strings.HasPrefix(got, "product-manager||") {
		t.Fatalf("asked %q, want the Lead Product Manager", got)
	}
	if !strings.Contains(said, "the Lead Product Manager was asked instead") || !strings.Contains(said, "yoyo chat") {
		t.Fatalf("Ask() said %q", said)
	}
}

func waitFor(t *testing.T, asked chan string) string {
	t.Helper()
	select {
	case got := <-asked:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("nothing was asked")
		return ""
	}
}

// Each person who named a dashboard token of their own is bound from the store
// named, and one whose token is not there is left unbound with the command
// that stores it; the token itself is never printed.
func TestTheDashboardBindsEachPersonFromTheirOwnStore(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	stores := dashboard.TokenStores{Platform: "linux", StateRoot: stateRoot}
	token := strings.Repeat("ab", 32)
	file := dashboard.PersonTokenFile(stateRoot, "yoyodyne", "mason")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := dashboard.New("yoyodyne", stubDashboardReader{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Product: config.Product{ID: "yoyodyne"}, Operators: map[string]config.Operator{
		"mason":  {DashboardToken: config.DashboardTokenFile, Grants: []config.Grant{config.GrantOwnIntent}},
		"jordan": {DashboardToken: config.DashboardTokenFile},
	}}
	lines := strings.Join(bindDashboardPeople(context.Background(), server, cfg, stores), "\n")
	if !strings.Contains(lines, "mason approves, declines, and asks from the page with their own token, read from the file "+file) {
		t.Fatalf("lines = %q", lines)
	}
	if !strings.Contains(lines, "jordan cannot approve, decline, or ask from the page") || !strings.Contains(lines, "dashboard.jordan.token") {
		t.Fatalf("lines = %q", lines)
	}
	if strings.Contains(lines, token) {
		t.Fatalf("the token was printed: %q", lines)
	}
	var none bytes.Buffer
	none.WriteString(strings.Join(bindDashboardPeople(context.Background(), server, config.Config{}, stores), "\n"))
	if !strings.Contains(none.String(), "reads only") {
		t.Fatalf("with nobody bound: %q", none.String())
	}
}

// stubDashboardReader answers the readings binding asks nothing of; the ones it
// does not define are never called here.
type stubDashboardReader struct{ dashboard.Reader }

func (stubDashboardReader) Standing(context.Context) (readmodel.Standing, error) {
	return readmodel.Standing{}, nil
}
func (stubDashboardReader) Throughput(context.Context) (readmodel.Throughput, error) {
	return readmodel.Throughput{}, nil
}
func (stubDashboardReader) Spend(context.Context) (readmodel.Spend, error) {
	return readmodel.Spend{}, nil
}
func (stubDashboardReader) WorkItem(context.Context, string) (readmodel.WorkItem, error) {
	return readmodel.WorkItem{}, nil
}
func (stubDashboardReader) ProgramManagerReport(context.Context, string) (readmodel.ProgramManagerReport, error) {
	return readmodel.ProgramManagerReport{}, nil
}
