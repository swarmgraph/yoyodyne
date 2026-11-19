package orchestrator

// A confirmed document of a project that keeps its intent in a companion intent
// repository lands there, end to end, over two real Git repositories: the
// project's own, which holds code and must never be touched, and the intent
// repository, which holds the governed documents. Nothing here is stubbed but
// the agents and the order of the checks' recording; the worktrees, the
// promotion, the checks, and the run records are the real ones.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	widgetPath    = "internal/widget/widget.go"
	widgetSource  = "package widget\n\n// Spin turns the widget once; the design under review relies on it.\nfunc Spin() int { return 7 }\n"
	landedDesign  = "recovery-design"
	landedPath    = "docs/designs/recovery-design.md"
	projectCheck  = "make project-check"
	intentChecks0 = "test -f " + landedPath
	intentChecks1 = `test -n "$` + config.EnvironmentVariable + `"`
)

// recordingChecks runs every request through the real runner and keeps what it
// was asked, so a test can say which checks ran, where, and with what
// environment.
type recordingChecks struct {
	inner    CheckRunner
	mu       sync.Mutex
	requests []checks.Request
}

func (r *recordingChecks) Run(ctx context.Context, request checks.Request, sink func(execution.Event) error) ([]checks.Result, uint64, error) {
	r.mu.Lock()
	r.requests = append(r.requests, checks.Request{RunID: request.RunID, Directory: request.Directory, Commands: slices.Clone(request.Commands), Env: slices.Clone(request.Env)})
	r.mu.Unlock()
	return r.inner.Run(ctx, request, sink)
}

func (r *recordingChecks) seen() []checks.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

// companionFixture is a project repository and its companion intent repository,
// each on main with an initial commit, and a pipeline that can land in either.
type companionFixture struct {
	project, intent string
	configPath      string
	pipeline        Pipeline
	provider        *orchestratortest.Backend
	runs            *runstate.Store
	checks          *recordingChecks
}

func newCompanionFixture(t *testing.T) *companionFixture {
	t.Helper()
	project, _, provider, pipeline, runs := automaticFixture(t)
	// The code a design in the intent repository can cite, committed to the
	// project's main before any run reads it.
	writeCompanionFile(t, project, widgetPath, widgetSource)
	publicationGit(t, project, "add", ".")
	publicationGit(t, project, "commit", "-m", "add the widget")

	intent := filepath.Join(t.TempDir(), "intent")
	writeCompanionFile(t, intent, "docs/product/brief.md", "# Brief\n\n"+companionBriefSentence+"\n")
	initCompanionRepository(t, intent)
	t.Cleanup(func() { removeLinkedPipelineWorktrees(t, intent) })

	intentWorktrees, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: intent,
		WorktreeRoot:   filepath.Join(t.TempDir(), "intent-worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	f := &companionFixture{project: project, intent: intent, provider: provider, runs: runs}
	f.configPath = filepath.Join(t.TempDir(), "projects", "calc", config.FileName)
	pipeline.ConfigPath = f.configPath
	pipeline.Config.Intent.Repository = "intent"
	pipeline.Config.Product.IntentRepository = intent
	pipeline.Config.Intent.Checks = []string{intentChecks0, intentChecks1}
	// The project's own checks would fail if any run executed them: no such
	// make target exists. Naming them keeps the test able to tell the two sets
	// apart, which a passing "exit 0" would not.
	pipeline.Config.Checks = []string{projectCheck}
	pipeline.IntentWorktrees = intentWorktrees
	f.checks = &recordingChecks{inner: pipeline.Checks}
	pipeline.Checks = f.checks
	f.pipeline = pipeline
	return f
}

// confirm builds the publication a conversation hands over for an automatic
// design: the owner's write, confirmed under approvals.designs, read against
// the companion repository's checkout.
func (f *companionFixture) confirm(t *testing.T, write artifact.Write, writeID string, turn int) runstate.DocumentPublication {
	t.Helper()
	store := artifact.StoreFor(f.project, f.pipeline.Config.Product)
	policy := artifact.Policy{Designs: domain.ApprovalAutomatic, SpecificationsHome: f.pipeline.Config.Product.Specifications}
	candidate, automatic, err := store.PrepareConfirmation(domain.RoleArchitect, write, policy, "confirmed by harness", time.Now())
	if err != nil || !automatic {
		t.Fatalf("PrepareConfirmation() = automatic %v, %v", automatic, err)
	}
	return f.publication(candidate, writeID, turn)
}

func (f *companionFixture) publication(candidate artifact.ConfirmedDocument, writeID string, turn int) runstate.DocumentPublication {
	return runstate.DocumentPublication{
		Candidate: candidate, ConversationID: "chat-intent-landing", WriteID: writeID, Turn: turn,
		Owner: domain.RoleArchitect, AuthorSession: "author-session", AuthorBackend: string(domain.BackendClaudeCode), AuthorModel: "opus",
	}
}

func designWrite(action artifact.WriteAction, body string) artifact.Write {
	write := artifact.Write{Action: action, ID: landedDesign, Body: body, Reason: "written by the architect"}
	if action == artifact.WriteCreate {
		write.Kind, write.Title, write.Directory = artifact.KindDesign, "Recovery design", "docs/designs"
	}
	return write
}

const (
	firstBody   = "# Recovery\n\nThe widget spins once through " + widgetPath + ", and nothing else moves.\n"
	revisedBody = "# Recovery\n\nThe widget spins once through " + widgetPath + ", and the revision adds a second turn.\n"
)

// A confirmed design lands in the companion intent repository with no person
// acting, and so does a revision of it on top of the first; the project's
// repository is not touched by either.
func TestAConfirmedDesignLandsInTheCompanionIntentRepositoryAndSoDoesItsRevision(t *testing.T) {
	t.Parallel()

	f := newCompanionFixture(t)
	projectBefore := gitLine(t, f.project, "rev-parse", "main")
	intentBefore := gitLine(t, f.intent, "rev-parse", "main")

	first := f.confirm(t, designWrite(artifact.WriteCreate, firstBody), "document-1.1", 1)
	delivery, err := f.pipeline.PublishDocument(context.Background(), first)
	if err != nil || !delivery.Landed || !delivery.Settled {
		t.Fatalf("PublishDocument() = %+v, %v; want it landed and settled", delivery, err)
	}
	intentFirst := gitLine(t, f.intent, "rev-parse", "main")
	if intentFirst == intentBefore {
		t.Fatal("the intent repository's main did not move")
	}
	if got := publicationGit(t, f.intent, "show", "main:"+landedPath); got != strings.TrimSpace(first.Candidate.Content) {
		t.Fatalf("the intent repository holds different bytes than were confirmed:\n%s", got)
	}
	if gitLine(t, f.project, "rev-parse", "main") != projectBefore {
		t.Fatal("the project's main moved")
	}
	if out, err := attemptPipelineGit(f.project, "cat-file", "-e", "main:"+landedPath); err == nil {
		t.Fatalf("the project's repository holds the document: %s", out)
	}
	if status := publicationGit(t, f.intent, "status", "--porcelain"); status != "" {
		t.Fatalf("the intent repository's checkout is dirty: %s", status)
	}

	run := runByID(t, f.runs, first.RunID())
	if run.Repository != runstate.RepositoryIntent || run.Status != runstate.StatusSucceeded || run.Integration == nil {
		t.Fatalf("run = repository %q, status %s, integration %+v; want a succeeded run in the intent repository", run.Repository, run.Status, run.Integration)
	}
	if run.Integration.TargetCommit != intentFirst || run.Integration.TargetBranch != "main" {
		t.Fatalf("integration = %+v, want main at the intent repository's new head %s", run.Integration, intentFirst)
	}
	if run.IntentCommit == "" || run.ProjectCommit != projectBefore {
		t.Fatalf("IntentCommit = %q, ProjectCommit = %q; want an intent commit and the project's main %s", run.IntentCommit, run.ProjectCommit, projectBefore)
	}
	if run.PullRequest != nil || run.MergeQueue != nil {
		t.Fatalf("a landing in the intent repository was published or queued: %+v, %+v", run.PullRequest, run.MergeQueue)
	}

	// The checks that ran are the intent checks, in the intent repository's
	// worktree, told where the project's configuration is; the project's own
	// checks never ran.
	requests := f.checks.seen()
	if len(requests) != 1 {
		t.Fatalf("check requests = %+v, want one", requests)
	}
	if !slices.Equal(requests[0].Commands, f.pipeline.Config.IntentChecks()) {
		t.Fatalf("checks = %v, want exactly the intent checks %v", requests[0].Commands, f.pipeline.Config.IntentChecks())
	}
	if !slices.Contains(requests[0].Env, config.EnvironmentVariable+"="+f.configPath) {
		t.Fatalf("check environment = %v, want %s=%s", requests[0].Env, config.EnvironmentVariable, f.configPath)
	}
	if !strings.Contains(requests[0].Directory, "intent-worktrees") {
		t.Fatalf("checks ran in %s, want a worktree of the intent repository", requests[0].Directory)
	}

	// The reviewer was shown the project's code, with the file the design cites
	// read whole, as of the project's main.
	reviews := f.provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 1 || len(f.provider.RequestsForRole(domain.RoleDeveloper)) != 0 {
		t.Fatalf("reviewer requests = %d, developer requests = %d; want one review and no developer", len(reviews), len(f.provider.RequestsForRole(domain.RoleDeveloper)))
	}
	for _, want := range []string{widgetPath, "func Spin() int { return 7 }", projectBefore} {
		if !strings.Contains(reviews[0].Prompt, want) {
			t.Errorf("the reviewer's request does not carry %q of the project's code", want)
		}
	}

	// What the owning role is told, and what status reads.
	for _, want := range []string{"document-1.1", "Recovery design", "landed", landedPath, "companion intent repository", "main", intentFirst} {
		if !strings.Contains(delivery.Detail, want) {
			t.Errorf("the owning role was told %q, want it to say %q", delivery.Detail, want)
		}
	}

	// The revision: the same document, its landed copy named as what it
	// replaces, lands on top of the first.
	revision := f.confirm(t, designWrite(artifact.WriteRevise, revisedBody), "document-2.1", 2)
	if revision.Candidate.Before != artifact.DocumentIdentity([]byte(first.Candidate.Content)) {
		t.Fatalf("the revision replaces %q, want the landed copy %q", revision.Candidate.Before, artifact.DocumentIdentity([]byte(first.Candidate.Content)))
	}
	second, err := f.pipeline.PublishDocument(context.Background(), revision)
	if err != nil || !second.Landed || !second.Settled {
		t.Fatalf("PublishDocument(revision) = %+v, %v; want it landed and settled", second, err)
	}
	intentSecond := gitLine(t, f.intent, "rev-parse", "main")
	if intentSecond == intentFirst || gitLine(t, f.intent, "rev-parse", "main~1") != intentFirst {
		t.Fatalf("the revision did not land on top of the first: main %s, first %s", intentSecond, intentFirst)
	}
	if got := publicationGit(t, f.intent, "show", "main:"+landedPath); got != strings.TrimSpace(revision.Candidate.Content) || !strings.Contains(got, "second turn") {
		t.Fatalf("the intent repository does not hold the revision:\n%s", got)
	}
	if gitLine(t, f.project, "rev-parse", "main") != projectBefore {
		t.Fatal("the revision moved the project's main")
	}
	for _, want := range []string{"document-2.1", "Recovery design", "landed", "companion intent repository", intentSecond} {
		if !strings.Contains(second.Detail, want) {
			t.Errorf("the owning role was told %q of the revision, want it to say %q", second.Detail, want)
		}
	}

	// Both runs are in the store as succeeded landings, which is what status
	// reads.
	recorded, err := f.runs.Recorded()
	if err != nil || len(recorded) != 2 {
		t.Fatalf("recorded runs = %d, %v; want the two landings", len(recorded), err)
	}
	for _, state := range recorded {
		if state.Status != runstate.StatusSucceeded || state.Integration == nil || state.Repository != runstate.RepositoryIntent || !documentDelivery(state).Landed {
			t.Errorf("run %s = %s, integration %+v, repository %q; want a landing in the intent repository", state.RunID, state.Status, state.Integration, state.Repository)
		}
	}
	if latest := runByID(t, f.runs, revision.RunID()); latest.Integration.TargetCommit != intentSecond {
		t.Fatalf("the revision's integration = %+v, want %s", latest.Integration, intentSecond)
	}
}

// A document the reviewer refuses goes back to its owner, and nothing reaches
// the intent repository.
func TestARefusedDocumentGoesBackToItsOwnerAndTheIntentRepositoryIsUnchanged(t *testing.T) {
	t.Parallel()

	f := newCompanionFixture(t)
	approve := f.provider.Respond
	f.provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			return backend.RunResult{SessionID: "independent", FinalText: `{"decision":"repair","summary":"revise the document","findings":[{"severity":"blocker","message":"state the missing decision"}]}`}, nil
		}
		return approve(request)
	}
	intentBefore := gitLine(t, f.intent, "rev-parse", "main")
	projectBefore := gitLine(t, f.project, "rev-parse", "main")

	document := f.confirm(t, designWrite(artifact.WriteCreate, firstBody), "document-1.1", 1)
	delivery, err := f.pipeline.PublishDocument(context.Background(), document)
	if err != nil {
		t.Fatalf("PublishDocument() error = %v", err)
	}
	if !delivery.Settled || delivery.Landed || !delivery.Judged {
		t.Fatalf("delivery = %+v; want it settled, judged, and not landed", delivery)
	}
	for _, want := range []string{"must revise the document", "submit it again", "state the missing decision"} {
		if !strings.Contains(delivery.Detail, want) {
			t.Errorf("the owning role was told %q, want it to say %q", delivery.Detail, want)
		}
	}
	if gitLine(t, f.intent, "rev-parse", "main") != intentBefore || gitLine(t, f.project, "rev-parse", "main") != projectBefore {
		t.Fatal("a refused document moved a main")
	}
	if out, err := attemptPipelineGit(f.intent, "cat-file", "-e", "main:"+landedPath); err == nil {
		t.Fatalf("the refused document is in the intent repository: %s", out)
	}
	run := runByID(t, f.runs, document.RunID())
	if run.Repository != runstate.RepositoryIntent || run.Integration != nil || run.Status == runstate.StatusSucceeded {
		t.Fatalf("run = repository %q, status %s, integration %+v; want a refused run in the intent repository", run.Repository, run.Status, run.Integration)
	}
}

// A project's runs read its companion intent at a commit, so an edit in the
// companion repository's working tree that nobody committed is never intent:
// not what the developer or the reviewer reads, and not what the run records.
func TestAnUncommittedEditInTheCompanionCheckoutIsNotReadAsIntent(t *testing.T) {
	t.Parallel()

	f := newCompanionFixture(t)
	// An ordinary run: the developer adds a file, and the project's check is a
	// passing one, since this test is about what the run reads.
	f.pipeline.Config.Checks = []string{"exit 0"}
	committed := gitLine(t, f.intent, "rev-parse", "main")
	const uncommitted = "The checkout says calc also divides, which nobody committed."
	brief := filepath.Join(f.intent, "docs", "product", "brief.md")
	text, err := os.ReadFile(brief)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, append(text, []byte("\n"+uncommitted+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if status := publicationGit(t, f.intent, "status", "--porcelain"); !strings.Contains(status, "brief.md") {
		t.Fatalf("the edit is not an uncommitted change: %q", status)
	}

	outcome, err := f.pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() = %+v, %v", outcome, err)
	}
	developer := f.provider.RequestsForRole(domain.RoleDeveloper)
	reviewing := f.provider.RequestsForRole(domain.RoleReviewer)
	if len(developer) != 1 || len(reviewing) != 1 {
		t.Fatalf("developer requests = %d, reviewer requests = %d", len(developer), len(reviewing))
	}
	for name, prompt := range map[string]string{"developer": developer[0].Prompt, "reviewer": reviewing[0].Prompt} {
		if !strings.Contains(prompt, companionBriefSentence) {
			t.Errorf("the %s's context lacks the committed brief", name)
		}
		if strings.Contains(prompt, uncommitted) {
			t.Errorf("the %s's context carries the uncommitted edit", name)
		}
	}
	if !strings.Contains(developer[0].Prompt, committed) || !strings.Contains(developer[0].Prompt, "companion intent repository") {
		t.Errorf("the developer's context does not name the intent commit %s", committed)
	}
	state := runByID(t, f.runs, pipelineRunID)
	if state.IntentCommit != committed {
		t.Fatalf("IntentCommit = %q, want the intent repository's main %s", state.IntentCommit, committed)
	}
}

// A kind whose policy asks for the operator still waits for the operator: the
// harness's own confirmation is refused where the setting is human, and nothing
// lands; the operator's approval lands it.
func TestADesignWhosePolicyAsksForTheOperatorLandsInTheIntentRepositoryOnlyOnTheirApproval(t *testing.T) {
	t.Parallel()

	f := newCompanionFixture(t)
	f.pipeline.Config.Approvals.Designs = domain.ApprovalHuman
	intentBefore := gitLine(t, f.intent, "rev-parse", "main")

	// Confirmed by the harness under a setting that has since changed, and so
	// not decided by anyone.
	confirmed := f.confirm(t, designWrite(artifact.WriteCreate, firstBody), "document-1.1", 1)
	delivery, err := f.pipeline.PublishDocument(context.Background(), confirmed)
	if !errors.Is(err, runstate.ErrDocumentNotPublishable) {
		t.Fatalf("PublishDocument() = %+v, %v; want %v", delivery, err, runstate.ErrDocumentNotPublishable)
	}
	if gitLine(t, f.intent, "rev-parse", "main") != intentBefore {
		t.Fatal("a document the operator had not decided landed")
	}
	if recorded, err := f.runs.Recorded(); err != nil || len(recorded) != 0 {
		t.Fatalf("runs = %+v, %v; want none opened for a refused document", recorded, err)
	}

	store := artifact.StoreFor(f.project, f.pipeline.Config.Product)
	candidate, err := store.PrepareApproval(domain.RoleArchitect, designWrite(artifact.WriteCreate, firstBody), "approved by the operator in conversation chat-intent-landing", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	approved := f.publication(candidate, "document-1.2", 1)
	landed, err := f.pipeline.PublishDocument(context.Background(), approved)
	if err != nil || !landed.Landed || !landed.Settled {
		t.Fatalf("PublishDocument(approved) = %+v, %v; want it landed", landed, err)
	}
	if got := publicationGit(t, f.intent, "show", "main:"+landedPath); got != strings.TrimSpace(candidate.Content) {
		t.Fatalf("the intent repository holds different bytes than the operator approved:\n%s", got)
	}
	if run := runByID(t, f.runs, approved.RunID()); run.Repository != runstate.RepositoryIntent || run.Selection == nil || !strings.Contains(run.Selection.Reason, "approved by the operator") {
		t.Fatalf("run = repository %q, selection %+v", run.Repository, run.Selection)
	}
}

// A pipeline built without access to the companion repository cannot land the
// document and says so rather than landing it anywhere else. (The conversation
// side of the same hold is TestCompanionIntentDocumentIsHeldConfirmedRatherThanPutToTheOperator.)
func TestAPipelineWithoutAccessToTheIntentRepositoryHoldsTheDocument(t *testing.T) {
	t.Parallel()

	f := newCompanionFixture(t)
	f.pipeline.IntentWorktrees = nil
	intentBefore := gitLine(t, f.intent, "rev-parse", "main")
	projectBefore := gitLine(t, f.project, "rev-parse", "main")
	document := f.confirm(t, designWrite(artifact.WriteCreate, firstBody), "document-1.1", 1)
	if _, err := f.pipeline.PublishDocument(context.Background(), document); !errors.Is(err, ErrCompanionPublicationNotBuilt) {
		t.Fatalf("PublishDocument() error = %v, want %v", err, ErrCompanionPublicationNotBuilt)
	}
	if gitLine(t, f.intent, "rev-parse", "main") != intentBefore || gitLine(t, f.project, "rev-parse", "main") != projectBefore {
		t.Fatal("a held document moved a main")
	}
	if recorded, err := f.runs.Recorded(); err != nil || len(recorded) != 0 {
		t.Fatalf("runs = %+v, %v; want none", recorded, err)
	}
}
