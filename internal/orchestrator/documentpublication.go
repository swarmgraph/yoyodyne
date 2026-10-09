package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// PublishesDocuments reports whether a confirmed document could land: a
// reviewed run integrates only where the project integrates automatically.
//
// A project that keeps its intent in a companion intent repository is answered
// the same way, so a document its automatic policy confirms stays confirmed by
// that policy rather than being put to the operator: confirming a design is not
// a decision only a person can make. PublishDocument then holds it — see
// ErrCompanionPublicationNotBuilt.
func (p Pipeline) PublishesDocuments() bool { return p.automatic() }

// ErrCompanionPublicationNotBuilt is why a confirmed document of a project that
// keeps its intent in a companion intent repository is held rather than landed.
// The reviewed run that would land it there — a branch cut from that repository,
// checks that mean something for a repository holding only documents,
// independent review, and promotion into its target under the promotion lease,
// as docs/designs/machine-home.md describes — is not built yet. Running this
// project's own checks over that repository, or landing the document in the
// project's repository, would each be wrong, and putting it to the operator
// would route a routine confirmation to a person. So the document stays
// confirmed and saved in its conversation, the owning role is told once what
// holds it, and it is offered again at every later message, which is what lands
// it once that run exists.
var ErrCompanionPublicationNotBuilt = errors.New("this project keeps its intent in a companion intent repository, and the reviewed run that lands a document there is not built yet; the document stays confirmed and saved in this conversation and is tried again at each later message, and nothing about it is waiting on the operator")

// PublishDocument enters the ordinary delivery gates with an already written
// candidate. Only the owning conversation can supply this handoff; there is no
// developer invocation or repair of its content inside a publication run.
func (p Pipeline) PublishDocument(ctx context.Context, document runstate.DocumentPublication) (runstate.DocumentDelivery, error) {
	if err := document.Validate(); err != nil {
		return p.refuseDocument(document, err)
	}
	policy := artifact.Policy{Brief: p.Config.Approvals.Brief, Goals: p.Config.Approvals.Goals, Designs: p.Config.Approvals.Designs, SpecificationsHome: p.Config.Product.Specifications}
	name, mode, governed := policy.SettingFor(document.Candidate.Artifact)
	approval, _ := document.Candidate.Artifact.LatestApproval()
	if !governed || mode != domain.ApprovalAutomatic || approval.Policy != name {
		return p.refuseDocument(document, errors.New("the document's automatic confirmation policy no longer applies"))
	}
	filing, err := artifact.StoreFor(p.Repository, p.Config.Product).Filing(document.Owner)
	if err != nil {
		return runstate.DocumentDelivery{}, err
	}
	filed := false
	for _, home := range filing {
		if home.Kind == document.Candidate.Artifact.Kind && strings.HasPrefix(document.Candidate.Artifact.Path, home.Directory+"/") {
			filed = true
		}
	}
	if !filed {
		return p.refuseDocument(document, errors.New("confirmed document is outside its kind's configured home"))
	}
	if !p.automatic() {
		return p.refuseDocument(document, errors.New("reviewed document publication requires automatic integration"))
	}
	if p.Config.Product.HasIntentRepository() {
		return runstate.DocumentDelivery{}, ErrCompanionPublicationNotBuilt
	}
	// These tracker operations describe this run's document, not backlog work.
	// The canonical account remains the conversation and the ordinary run store.
	item := beads.WorkItem{ID: document.RunID(), Title: "Publishing " + document.Candidate.Artifact.Title, Status: "open", Description: "Publish exactly the document confirmed in its owning conversation.\nprotected-path grant: " + document.Candidate.Artifact.Path}
	tracker := &documentTracker{item: item}
	p.Tracker, p.Docket, p.Prices = tracker, nil, nil
	p.Selection = runstate.Selection{By: runstate.SelectedByConversation, Reason: fmt.Sprintf("Conversation %s supplied document %s, written by the %s at turn %d and confirmed under %s; publish its exact content through independent review.", document.ConversationID, document.WriteID, document.Owner.Title(), document.Turn, name)}
	if err := p.validateDispatch(); err != nil {
		return runstate.DocumentDelivery{}, err
	}
	if hold, held, err := p.operatorHold(); err != nil || held {
		if err != nil {
			return runstate.DocumentDelivery{}, err
		}
		return runstate.DocumentDelivery{RunID: document.RunID(), Detail: fmt.Sprint(hold)}, nil
	}
	publishing, skipped, err := p.resolvePublishing(ctx)
	if err != nil {
		return runstate.DocumentDelivery{}, err
	}
	state, err := p.Store.Load(document.RunID())
	var lease *runstate.Lease
	if err == nil {
		// A run of it is recorded, so it is not waiting for a slot whoever
		// reserved it.
		p.clearDocumentWait(document)
		state, lease, err = p.Store.AdoptRun(ctx, document.RunID())
		if errors.Is(err, runstate.ErrRunHeld) {
			// The scheduler, or another message of the conversation, is running it
			// now. Its ending is read back at a later offer.
			return runstate.DocumentDelivery{RunID: document.RunID()}, nil
		}
		if err != nil {
			return runstate.DocumentDelivery{}, err
		}
		defer lease.Release()
		if !reflect.DeepEqual(state.Document, &document) {
			return runstate.DocumentDelivery{}, errors.New("the run already records a different document handoff")
		}
		if state.Status.Terminal() {
			return documentDelivery(state), nil
		}
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			return runstate.DocumentDelivery{}, err
		}
		if err := p.Worktrees.ValidateReady(ctx); err != nil {
			return runstate.DocumentDelivery{}, err
		}
		if outcome, held, err := p.holdIntake(item.ID); err != nil || held {
			return runstate.DocumentDelivery{RunID: document.RunID(), Detail: outcome.Summary}, err
		}
		target, err := p.Worktrees.CurrentBranch(ctx)
		if err != nil {
			return runstate.DocumentDelivery{}, err
		}
		state = runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: document.RunID(), ProductID: p.Config.Product.ID, RepositoryID: string(p.Config.Product.RepositoryID), WorkItemID: item.ID, WorkItemTitle: item.Title, Backend: p.reviewer().Backend, ConfigRevision: p.Config.Revision(), Build: p.Build, Status: runstate.StatusPending, Document: &document, TargetBranch: target}
		state, lease, err = p.reserveRun(ctx, state)
		var capacity runstate.CapacityError
		var existing runstate.ExistingWorkItemError
		switch {
		case errors.As(err, &capacity):
			return p.waitForSlot(ctx, document, capacity)
		case errors.As(err, &existing):
			// Another process reserved it between the look above and this one.
			p.clearDocumentWait(document)
			return runstate.DocumentDelivery{RunID: document.RunID()}, nil
		case err != nil:
			return runstate.DocumentDelivery{}, err
		}
		defer lease.Release()
		p.clearDocumentWait(document)
	}
	tracker.item.Status = "in_progress"
	invariants, err := p.loadInvariants()
	if err != nil {
		return runstate.DocumentDelivery{}, err
	}
	a := &activeRun{pipeline: p, state: state, item: tracker.item, claimed: true, publishing: publishing, invariants: invariants, context: fmt.Sprintf("Publish the %s's exact document %s from conversation %s, turn %d. Confirmed under %s. No developer may rewrite it.\n", document.Owner.Title(), document.Candidate.Artifact.Title, document.ConversationID, document.Turn, approval.Policy), outcome: Outcome{RunID: state.RunID, WorkItemID: state.WorkItemID, Status: state.Status, Phase: state.Phase, PublishSkipped: skipped, Summary: "Publish the owning role's confirmed document without rewriting its content.", PullRequest: state.PullRequest}}
	a.worktree = gitworktree.Worktree{RunID: state.RunID, WorkItemID: state.WorkItemID, Path: state.WorktreePath, Branch: state.Branch, BaseCommit: state.BaseCommit, TargetBranch: state.TargetBranch, HarnessCommit: state.HarnessCommit}
	a.outcome.Branch, a.outcome.WorktreePath, a.outcome.BaseCommit = state.Branch, state.WorktreePath, state.BaseCommit
	if err := a.claim(ctx); err != nil {
		return runstate.DocumentDelivery{}, err
	}
	if state.WorktreePath == "" {
		if err := a.beginDeliveryTrial(); err != nil {
			return runstate.DocumentDelivery{}, err
		}
		a.observe(ctx, deliveryClaim, "claimed")
	} else {
		a.resumeDeliveryTrial()
	}
	a.carryReviewEvidence()
	if state.Integration == nil && state.Phase == runstate.PhaseIntegrating {
		if err := a.recoverDocumentIntegration(ctx); err != nil {
			return runstate.DocumentDelivery{}, err
		}
		state = a.state
	}
	// Integration is idempotent and the worktree manager observes whether its
	// commit already reached the target after an interrupted promotion.
	if state.Integration != nil {
		a.outcome.Integration = &gitworktree.Integration{Branch: state.Branch, TargetBranch: state.Integration.TargetBranch, SourceCommit: state.Integration.SourceCommit, TargetCommit: state.Integration.TargetCommit, PreviousTargetCommit: state.Integration.PreviousTargetCommit, ThroughPullRequest: state.Integration.ThroughPullRequest}
		_, err = a.finish(ctx)
	} else {
		if state.WorktreePath == "" {
			worktree, createErr := p.Worktrees.Create(ctx, gitworktree.CreateRequest{ResumeCreation: true, RunID: state.RunID, WorkItemID: item.ID, BaseRef: state.TargetBranch, TargetBranch: state.TargetBranch})
			if createErr != nil {
				return runstate.DocumentDelivery{}, createErr
			}
			a.recordWorktree(worktree)
			a.state.Status, a.state.Phase = runstate.StatusRunning, runstate.PhaseDeveloping
			if err := p.Store.Save(a.state); err != nil {
				return runstate.DocumentDelivery{}, err
			}
		}
		if err = a.prepareDocument(ctx); err == nil {
			if state.WorktreePath == "" {
				a.observe(ctx, deliveryDevelop, "produced")
			}
			_, err = a.verifyReviewAndFinish(ctx)
		} else {
			_, err = a.fail(err, runstate.StatusFailed)
		}
	}
	saved, loadErr := p.Store.Load(state.RunID)
	if loadErr != nil {
		return runstate.DocumentDelivery{}, errors.Join(err, loadErr)
	}
	if saved.Status.Terminal() {
		return documentDelivery(saved), nil
	}
	// A pause retains the confirmed handoff in the conversation. A store or
	// infrastructure failure must never be claimed as delivered.
	return documentDelivery(saved), err
}

// refuseDocument reports a confirmation that no longer holds. Where no run was
// opened for the document, the refusal is final and its conversation stops
// holding the handoff; where one was, the run's own record governs, so the
// refusal is an ordinary failure and the handoff is kept for it.
func (p Pipeline) refuseDocument(document runstate.DocumentPublication, refusal error) (runstate.DocumentDelivery, error) {
	if _, err := p.Store.Load(document.RunID()); err == nil {
		return runstate.DocumentDelivery{RunID: document.RunID()}, fmt.Errorf("run %s already holds this document: %w", document.RunID(), refusal)
	} else if !errors.Is(err, os.ErrNotExist) {
		return runstate.DocumentDelivery{}, errors.Join(refusal, err)
	}
	// Nothing will ever publish it as it stands, so it is not waiting for a slot
	// either; its conversation meets the same refusal at its next message and
	// takes the document down the path it would take now.
	p.clearDocumentWait(document)
	return runstate.DocumentDelivery{}, fmt.Errorf("%w: %v", runstate.ErrDocumentNotPublishable, refusal)
}

// DocumentWaits is where a confirmed document waits for a developer slot when
// every slot is taken; see runstate.DocumentWait. It is an optional capability
// of the pipeline's StateStore, satisfied by *runstate.Store. A store without it
// keeps nothing waiting, and the document is offered again only at its
// conversation's next message, as it was before the scheduler could start one.
type DocumentWaits interface {
	WaitForSlot(ctx context.Context, document runstate.DocumentPublication, at time.Time) (runstate.DocumentWait, bool, error)
	ClearDocumentWait(runID string) error
}

// waitForSlot records a document whose run every developer slot being taken
// refused, so the scheduler starts it in the next slot that frees. Its
// conversation keeps the handoff and is told once that it waits; nothing about
// it is a failure.
func (p Pipeline) waitForSlot(ctx context.Context, document runstate.DocumentPublication, capacity runstate.CapacityError) (runstate.DocumentDelivery, error) {
	delivery := runstate.DocumentDelivery{RunID: document.RunID(), WaitingForSlot: &capacity}
	waits, ok := p.Store.(DocumentWaits)
	if !ok {
		delivery.Detail = fmt.Sprintf("Document %s (%s) is confirmed and waiting for a developer slot (%s). It is tried again at the next message in this conversation; it does not need writing again.\n", document.WriteID, document.Candidate.Artifact.Title, capacity.Error())
		return delivery, nil
	}
	if _, waiting, err := waits.WaitForSlot(ctx, document, p.clock().Now()); err != nil {
		return runstate.DocumentDelivery{}, fmt.Errorf("record document %s as waiting for a developer slot: %w", document.WriteID, err)
	} else if !waiting {
		// Another process reserved its run under the same lock just now.
		return runstate.DocumentDelivery{RunID: document.RunID()}, nil
	}
	delivery.Detail = fmt.Sprintf("Document %s (%s) is confirmed and waiting for a developer slot (%s). The harness starts its reviewed run in the next slot that frees, ahead of any new development run, without waiting for a message here; it does not need writing again.\n", document.WriteID, document.Candidate.Artifact.Title, capacity.Error())
	return delivery, nil
}

// clearDocumentWait takes a document off the slot wait. It is called where a
// run of the document is recorded, or where none ever can be, and a wait it
// could not clear decides nothing: the next offer of it finds the run recorded
// or the confirmation lapsed and clears it then.
func (p Pipeline) clearDocumentWait(document runstate.DocumentPublication) {
	if waits, ok := p.Store.(DocumentWaits); ok {
		_ = waits.ClearDocumentWait(document.RunID())
	}
}

// PublishWaitingDocument is the scheduler starting a document that waited for a
// developer slot: the same publication its conversation would offer, through
// PublishDocument, answered as the outcome of a run the scheduler hosted. A
// document that lost the slot to another run is answered with the capacity
// error, which the scheduler records as declined rather than failed.
func (p Pipeline) PublishWaitingDocument(ctx context.Context, document runstate.DocumentPublication) (Outcome, error) {
	delivery, err := p.PublishDocument(ctx, document)
	// The run is named only where one is recorded: a document that went on
	// waiting held no slot, and a schedule naming a run would say one freed.
	outcome := Outcome{WorkItemID: document.RunID(), Summary: strings.TrimSpace(delivery.Detail)}
	if state, loadErr := p.Store.Load(document.RunID()); loadErr == nil {
		outcome.RunID, outcome.Status, outcome.Phase = state.RunID, state.Status, state.Phase
	}
	if err == nil && delivery.WaitingForSlot != nil {
		return outcome, *delivery.WaitingForSlot
	}
	return outcome, err
}

// A process can disappear after the target moved but before its record was
// saved. Observe the existing promotion under the ordinary promotion lease;
// never replay an already landed document into an empty change or promote it
// again. The same revision-bound gates still have to support its record.
func (a *activeRun) recoverDocumentIntegration(ctx context.Context) error {
	lease, err := a.pipeline.Store.LeasePromotion(ctx, a.state.TargetBranch)
	if err != nil {
		return err
	}
	defer lease.Release()
	observed, err := a.pipeline.Worktrees.Observe(ctx, a.worktree)
	if err != nil || !observed.BranchIntegrated {
		return err
	}
	if observed.BranchCommit != a.state.HarnessCommit {
		return errors.New("the target contains a different document revision than this run recorded")
	}
	if err := a.validateIndependentReview(); err != nil {
		return err
	}
	if err := a.integrationEarned(ctx); err != nil {
		return err
	}
	a.state.Integration = &runstate.Integration{TargetBranch: a.state.TargetBranch, SourceCommit: observed.BranchCommit, TargetCommit: observed.BranchCommit, PreviousTargetCommit: a.state.BaseCommit}
	a.outcome.Integration = &gitworktree.Integration{Branch: a.state.Branch, TargetBranch: a.state.TargetBranch, SourceCommit: observed.BranchCommit, TargetCommit: observed.BranchCommit, PreviousTargetCommit: a.state.BaseCommit}
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return err
	}
	if err := a.publishIntegration(ctx); err != nil {
		return err
	}
	return a.pipeline.Store.Save(a.state)
}

func (a *activeRun) prepareDocument(ctx context.Context) error {
	// Only the harness works in this checkout. An interruption immediately
	// after its commit may leave HEAD ahead of the saved checkpoint. Adopt that
	// commit only after proving it is clean, owned, and exactly the confirmed
	// single file; checks and independent review still follow this checkpoint.
	if a.state.Phase == runstate.PhaseDeveloping {
		observed, err := a.pipeline.Worktrees.Observe(ctx, a.worktree)
		if err != nil {
			return err
		}
		expected := a.worktree.HarnessCommit
		if expected == "" {
			expected = a.worktree.BaseCommit
		}
		if observed.WorktreeHead != expected {
			if observed.WorktreeDirty || observed.WorktreeHead != observed.BranchCommit {
				return errors.New("the interrupted document commit is not a clean owned branch")
			}
			a.worktree.HarnessCommit = observed.WorktreeHead
			if _, err := a.gateProtectedPaths(ctx); err != nil {
				return err
			}
			a.recordHarnessCommit(observed.WorktreeHead)
			if err := a.pipeline.Store.Save(a.state); err != nil {
				return err
			}
		}
	}
	candidate := a.state.Document.Candidate
	current, err := os.ReadFile(filepath.Join(a.worktree.Path, candidate.Artifact.Path))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if string(current) != candidate.Content {
		if (err == nil && artifact.DocumentIdentity(current) != candidate.Before) || (errors.Is(err, os.ErrNotExist) && candidate.Before != "") {
			a.state.ReplayConflict = &runstate.ReplayConflict{Paths: []string{candidate.Artifact.Path}, TargetBranch: a.worktree.TargetBranch, Detail: "the target document changed after confirmation"}
			return fmt.Errorf("document conflicts with target branch at %s", candidate.Artifact.Path)
		}
		root, err := repowrite.NewRoot(a.worktree.Path)
		if err != nil {
			return err
		}
		if _, err := root.WriteFile(candidate.Artifact.Path, []byte(candidate.Content)); err != nil {
			return err
		}
	}
	if err := a.commitAttempt(ctx); err != nil {
		return err
	}
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return err
	}
	summary, err := a.pipeline.Worktrees.SummarizeChanges(ctx, a.worktree)
	if err != nil {
		return err
	}
	a.recordChanges(summary)
	return a.publishAttempt(ctx)
}

func documentDelivery(state runstate.State) runstate.DocumentDelivery {
	d := state.Document
	result := runstate.DocumentDelivery{RunID: state.RunID, Settled: state.Status.Terminal(), Landed: state.Status == runstate.StatusSucceeded && state.Integration != nil && (state.PullRequest == nil || !state.PullRequest.MergeQueued)}
	if !result.Settled {
		return result
	}
	if state.PullRequest != nil && state.PullRequest.MergeQueued {
		result.Settled = false
		return result
	}
	if result.Landed {
		result.Detail = fmt.Sprintf("Document %s (%s) landed through reviewed run %s at %s.\n", d.WriteID, d.Candidate.Artifact.Title, state.RunID, d.Candidate.Artifact.Path)
	} else {
		var detail strings.Builder
		fmt.Fprintf(&detail, "Document %s (%s) did not land in run %s. The %s must revise the document in this conversation and submit it again; that opens a fresh reviewed run.\n%s\n", d.WriteID, d.Candidate.Artifact.Title, state.RunID, d.Owner.Title(), state.Failure)
		if state.CheckFailure != nil {
			fmt.Fprintf(&detail, "Failing check: %s\n%s\n", state.CheckFailure.Command, state.CheckFailure.Output)
		}
		for _, finding := range state.ReviewFindingDetails {
			fmt.Fprintf(&detail, "Review finding: %s\n", finding.Message)
		}
		if state.ReplayConflict != nil {
			fmt.Fprintf(&detail, "Conflicting paths: %s\n", strings.Join(state.ReplayConflict.Paths, ", "))
		}
		result.Detail = detail.String()
	}
	return result
}

// documentTracker implements the pipeline's bookkeeping for a document run.
// It writes no tracker item: confirmation is already admitted by policy.
type documentTracker struct{ item beads.WorkItem }

func (t *documentTracker) Show(context.Context, string) (beads.WorkItem, error) { return t.item, nil }
func (t *documentTracker) Claim(context.Context, string) (beads.WorkItem, *beads.StaleBlockClear, error) {
	t.item.Status = "in_progress"
	return t.item, nil, nil
}
func (t *documentTracker) RecordOutcome(context.Context, string, string) (beads.WorkItem, error) {
	return t.item, nil
}
func (t *documentTracker) Block(context.Context, string, string) (beads.WorkItem, error) {
	t.item.Status = "blocked"
	return t.item, nil
}
func (t *documentTracker) Release(context.Context, string, string) (beads.WorkItem, error) {
	t.item.Status = "open"
	return t.item, nil
}
func (t *documentTracker) Complete(context.Context, string, string) (beads.WorkItem, error) {
	t.item.Status = "closed"
	return t.item, nil
}
func (t *documentTracker) Reopen(context.Context, string, string, domain.WorkItemParking) (beads.WorkItem, error) {
	return t.item, nil
}
func (t *documentTracker) AddBlocker(context.Context, string, string) error {
	return errors.New("a document publication cannot add a backlog dependency")
}

func (a *activeRun) reviewDocument(ctx context.Context) (Outcome, error) {
	for {
		if err := a.holdForDirective(); err != nil {
			return a.stop(ctx, err)
		}
		if err := a.verify(ctx); err != nil {
			var failing checkFailure
			if errors.As(err, &failing) {
				a.recordCheckFailure(failing.result)
			}
			return a.stop(ctx, err)
		}
		a.observe(ctx, deliveryCheck, "passed")
		verdict, err := a.reviewChange(ctx)
		a.observeReviewEnded(ctx, verdict, err, budgetSpent)
		if err != nil {
			return a.stop(ctx, err)
		}
		if verdict != review.DecisionApprove {
			return a.fail(stoppedBy(runstate.StopReview, errors.New("the independent reviewer refused the document: "+a.state.ReviewSummary)), runstate.StatusFailed)
		}
		outcome, replayed, err := a.promoteApproved(ctx)
		if !replayed {
			return outcome, err
		}
	}
}

func (a *activeRun) validateIndependentReview() error {
	if a.state.Document == nil {
		return validateIndependentInvocations(a.outcome)
	}
	document := a.state.Document
	if a.pipeline.reviewer().Role == document.Owner {
		return errors.New("the document owner cannot review its own publication")
	}
	if a.outcome.ReviewSessionID == "" || a.outcome.ReviewModel == "" {
		return errors.New("document integration requires a recorded independent reviewer invocation")
	}
	if document.AuthorSession != "" && document.AuthorSession == a.outcome.ReviewSessionID && document.AuthorBackend == string(a.pipeline.reviewer().Backend) && document.AuthorAccount == a.state.AccountAlias {
		return errors.New("the reviewer reused the document author's provider session")
	}
	return nil
}

func (a *activeRun) gateCandidateVerification(ctx context.Context) error {
	if a.state.Document != nil {
		return nil
	} // The owning conversation has no execution tools; configured checks still run.
	return a.gateSelfVerification(ctx)
}

func (a *activeRun) gateDocument(ctx context.Context, changed []string) error {
	candidate := a.state.Document.Candidate
	if len(changed) != 1 || changed[0] != candidate.Artifact.Path {
		return fmt.Errorf("document publication may change exactly %s; changed paths: %v", candidate.Artifact.Path, changed)
	}
	current, err := os.ReadFile(filepath.Join(a.worktree.Path, candidate.Artifact.Path))
	if err != nil {
		return err
	}
	if string(current) != candidate.Content {
		return errors.New("document publication no longer holds the confirmed content")
	}
	return nil
}

// A document run uses the same publication recovery as other runs, with its
// durable run record taking the place of backlog bookkeeping.
func (r Reconciler) forDocument(state runstate.State) Reconciler {
	if state.Document != nil {
		r.Tracker = &documentTracker{item: beads.WorkItem{ID: state.WorkItemID, Title: state.WorkItemTitle, Status: "in_progress"}}
		r.Docket = nil
	}
	return r
}
