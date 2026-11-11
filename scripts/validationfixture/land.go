package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	scriptedAuthorSession   = "validation-scripted-author-session"
	scriptedReviewerSession = "validation-scripted-reviewer-session"
	scriptedModel           = "validation-scripted-reviewer"
)

// landIntent lands one document in a project's companion intent repository
// through the harness's own document route: orchestrator.Pipeline.PublishDocument
// with the real worktree managers for the project's repository and the intent
// repository, the real check runner running the configured intent checks, and
// the real run store under the home. What it replaces is the provider: the
// independent reviewer is a scripted one that approves under a session and a
// model of its own, because the validation project has no provider. A landing
// proved this way shows the route, the checks, the promotion and the record;
// it does not show that a reviewer judged the document.
//
// It prints, one per line as name=value: run, status, repository, intent_head,
// intent_commit, project_commit, target_commit, review_session, author_session.
func landIntent(root, product, configPath, documentPath, contentFile string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load %s: %w", configPath, err)
	}
	if string(cfg.Product.ID) != product || !cfg.Product.HasIntentRepository() {
		return fmt.Errorf("%s is not the configuration of project %s with a companion intent repository", configPath, product)
	}
	// What the configuration init writes leaves to the person, set here the way
	// a person sets it: the designs confirmed automatically and integration
	// automatic, so a design lands without a human. The checks the project's
	// own code runs are never run for a landing in the intent repository, but a
	// pipeline requires one to be configured.
	cfg.Approvals.Designs = domain.ApprovalAutomatic
	cfg.Approvals.Integration = domain.ApprovalAutomatic
	if len(cfg.Checks) == 0 {
		cfg.Checks = []string{"true"}
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("the configuration is not valid with those settings: %w", err)
	}
	content, err := os.ReadFile(contentFile)
	if err != nil {
		return err
	}
	runner := execution.OSProcessRunner{}
	repository := cfg.Product.Repository
	worktreeRoot := home.WorktreeDirectory(root, product, string(cfg.Product.RepositoryID))
	projectWorktrees, err := gitworktree.New(gitworktree.Options{Runner: runner, RepositoryRoot: repository, WorktreeRoot: worktreeRoot})
	if err != nil {
		return fmt.Errorf("the project's worktrees: %w", err)
	}
	intentWorktrees, err := gitworktree.New(gitworktree.Options{
		Runner: runner, RepositoryRoot: cfg.Product.IntentRepository,
		WorktreeRoot: filepath.Join(filepath.Dir(worktreeRoot), filepath.Base(worktreeRoot)+"-intent"),
	})
	if err != nil {
		return fmt.Errorf("the intent repository's worktrees: %w", err)
	}
	store, err := runstate.NewStore(root, domain.ProductID(product))
	if err != nil {
		return err
	}
	directives, err := runstate.NewDirectiveStore(root, domain.ProductID(product))
	if err != nil {
		return err
	}
	holds, err := runstate.NewOperatorHoldStore(root)
	if err != nil {
		return err
	}
	intake, err := runstate.NewIntakeHoldStore(root, domain.ProductID(product))
	if err != nil {
		return err
	}
	reviewer := cfg.Agents["reviewer"]
	pipeline := orchestrator.Pipeline{
		Tracker: noTracker{}, Worktrees: projectWorktrees, IntentWorktrees: intentWorktrees,
		Store: store, Instances: store, Backend: noProvider{}, Checks: checks.Runner{Process: runner},
		Reviewer:   scriptedReviewer{model: reviewer.Model},
		Directives: directives, Holds: holds, Intake: intake,
		NewRunID: runstate.NewRunID, Repository: repository, Config: cfg, ConfigPath: configPath, StateRoot: root,
	}
	if !pipeline.PublishesDocuments() {
		return errors.New("the pipeline built here does not publish documents")
	}

	id := strings.TrimSuffix(path.Base(documentPath), ".md")
	directory := path.Dir(documentPath)
	// A design has to trace to the product's brief, which a fresh intent
	// repository does not hold, so the document is a decision record when it is
	// filed in the decisions home: the architect's account of a decision, owned
	// and confirmed as a design is, and not a link in the chain to the brief.
	kind, title := artifact.KindDesign, "Validation design "+id
	if directory == cfg.Product.Decisions {
		kind, title = artifact.KindDecision, "Validation decision "+id
	}
	candidate, confirmed, err := artifact.StoreFor(repository, cfg.Product).PrepareConfirmation(domain.RoleArchitect, artifact.Write{
		Action: artifact.WriteCreate, ID: id, Kind: kind, Title: title,
		Directory: directory, Body: string(content), Reason: "written by the validation project to land through the intent repository",
	}, artifact.Policy{Brief: domain.ApprovalHuman, Goals: domain.ApprovalHuman, Designs: domain.ApprovalAutomatic, SpecificationsHome: cfg.Product.Specifications},
		"the validation project's automatic design", time.Now())
	if err != nil {
		return fmt.Errorf("prepare the document: %w", err)
	}
	if !confirmed {
		return errors.New("the automatic policy did not confirm the document")
	}
	if candidate.Artifact.Path != documentPath {
		return fmt.Errorf("the document would land at %s, not %s", candidate.Artifact.Path, documentPath)
	}
	publication := runstate.DocumentPublication{
		Candidate: candidate, ConversationID: "chat-validation-project", WriteID: "document-1.1", Turn: 1,
		Owner: domain.RoleArchitect, AuthorSession: scriptedAuthorSession,
		AuthorBackend: string(domain.BackendClaudeCode), AuthorModel: "validation-scripted-author",
	}
	delivery, err := pipeline.PublishDocument(ctx, publication)
	if err != nil {
		return fmt.Errorf("publish the document: %w (delivery: %+v)", err, delivery)
	}
	state, err := store.Load(delivery.RunID)
	if err != nil {
		return err
	}
	head, err := intentWorktrees.TargetCommit(ctx, "main")
	if err != nil {
		if branch, berr := intentWorktrees.CurrentBranch(ctx); berr == nil {
			head, err = intentWorktrees.TargetCommit(ctx, branch)
		}
		if err != nil {
			return err
		}
	}
	target := ""
	if state.Integration != nil {
		target = state.Integration.TargetCommit
	}
	fmt.Printf("run=%s\nstatus=%s\nlanded=%t\ndetail=%s\nrepository=%s\nintent_head=%s\nintent_commit=%s\nproject_commit=%s\ntarget_commit=%s\nreview_session=%s\nauthor_session=%s\n",
		delivery.RunID, state.Status, delivery.Landed, strings.ReplaceAll(delivery.Detail, "\n", " "), state.Repository, head,
		state.IntentCommit, state.ProjectCommit, target, state.ReviewSessionID, scriptedAuthorSession)
	return nil
}

// scriptedReviewer stands where the provider-backed reviewer does and approves
// whatever it is given, under a session and a model that are not the author's.
type scriptedReviewer struct{ model string }

func (r scriptedReviewer) Review(context.Context, review.Request) (review.Result, error) {
	verdict := review.Verdict{Decision: review.DecisionApprove, Approves: review.ApprovesImplementation, Summary: "scripted approval from the validation project; no provider reviewed this change"}
	return review.Result{
		Verdict: verdict, Decision: review.DecisionApprove,
		RequestedModel: r.model, ResolvedModel: scriptedModel, SessionID: scriptedReviewerSession,
	}, nil
}

// noProvider is the developer's backend, which a document run never invokes.
type noProvider struct{}

func (noProvider) CheckAvailability(context.Context) (backend.Availability, error) {
	return backend.Availability{}, errors.New("the validation project has no provider")
}
func (noProvider) Capabilities() backend.Capabilities { return backend.Capabilities{} }
func (noProvider) Run(context.Context, backend.RunRequest) (backend.RunResult, error) {
	return backend.RunResult{}, errors.New("the validation project has no provider")
}

// noTracker is the work tracker a document run reads and writes only for a
// work item its document names; the document landed here names none.
type noTracker struct{}

var errNoTracker = errors.New("the validation project has no tracker")

func (noTracker) Show(context.Context, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errNoTracker
}
func (noTracker) Claim(context.Context, string) (beads.WorkItem, *beads.StaleBlockClear, error) {
	return beads.WorkItem{}, nil, errNoTracker
}
func (noTracker) RecordOutcome(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errNoTracker
}
func (noTracker) Block(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errNoTracker
}
func (noTracker) Release(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errNoTracker
}
func (noTracker) Complete(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errNoTracker
}
func (noTracker) Reopen(context.Context, string, string, domain.WorkItemParking) (beads.WorkItem, error) {
	return beads.WorkItem{}, errNoTracker
}
func (noTracker) AddBlocker(context.Context, string, string) error { return errNoTracker }
