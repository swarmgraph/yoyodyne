package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
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
//
// One handoff may take more than one run. A run that stops for a cause that
// judged nothing about the document — a check stopped by a time limit, the
// forge or the network failing, the machine or the harness stopping — is
// followed by another run of the same stored text, up to MaxDocumentAttempts,
// at most one run per call; see runstate.State.DocumentJudged for what judges a
// document. Only a run that judged it is handed back to its owner. Where the
// attempts are spent, the last run is handed to the development manager instead.
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
	// The tracker and docket below are replaced for the run itself, which has
	// no backlog item; the real ones are kept for the two things a document run
	// does write there — the start and end of each run, on the work item its
	// document's revision names, and a run handed to the development manager.
	record := documentItemRecorder{tracker: p.Tracker, document: document}
	docket := p.Docket
	p.Docket, p.Prices = nil, nil
	p.Selection = runstate.Selection{By: runstate.SelectedByConversation, Reason: fmt.Sprintf("Conversation %s supplied document %s, written by the %s at turn %d and confirmed under %s; publish its exact content through independent review.", document.ConversationID, document.WriteID, document.Owner.Title(), document.Turn, name)}
	p.Tracker = &documentTracker{item: documentItem(document, 0)}
	if err := p.validateDispatch(); err != nil {
		return runstate.DocumentDelivery{}, err
	}
	if hold, held, err := p.operatorHold(); err != nil || held {
		if err != nil {
			return runstate.DocumentDelivery{}, err
		}
		return runstate.DocumentDelivery{RunID: document.RunID(), Detail: fmt.Sprint(hold)}, nil
	}
	attempt, previous, err := p.documentAttempt(document)
	if err != nil {
		return runstate.DocumentDelivery{}, err
	}
	if attempt == runstate.MaxDocumentAttempts {
		// Every attempt is spent, so no slot will start another.
		p.clearDocumentWait(document)
		return p.handOverDocument(ctx, docket, record, *previous)
	}
	if previous != nil {
		if _, err := p.Store.Load(document.RunIDFor(attempt)); errors.Is(err, os.ErrNotExist) {
			waiting, waited, err := p.awaitDocumentRetry(*previous)
			if err != nil || waiting != "" {
				return runstate.DocumentDelivery{RunID: previous.RunID, Retrying: waiting}, err
			}
			p.Selection.Reason += fmt.Sprintf(" Attempt %d of %d: run %s stopped without judging the document (%s); %s.", attempt+1, runstate.MaxDocumentAttempts, previous.RunID, documentStopSays(*previous), waited)
			if err := p.recordDocumentRetry(ctx, previous.RunID, func(retry *runstate.DocumentRetry) {
				retry.WaitedFor, retry.Next = waited, document.RunIDFor(attempt)
			}); err != nil {
				return runstate.DocumentDelivery{}, err
			}
		} else if err != nil {
			return runstate.DocumentDelivery{}, err
		}
	}
	p.Tracker = &documentTracker{item: documentItem(document, attempt)}
	saved, ran, err := p.publishDocumentAttempt(ctx, document, attempt, approval.Policy, record)
	var held documentHeld
	if errors.As(err, &held) {
		return runstate.DocumentDelivery{RunID: held.runID, Detail: held.summary, Retrying: fmt.Sprintf("Document %s (%s) is confirmed, and its run has not started: %s. It is tried again at the next message and does not need writing again.\n", document.WriteID, document.Candidate.Artifact.Title, held.summary)}, nil
	}
	var waiting documentWaiting
	if errors.As(err, &waiting) {
		return p.waitForSlot(ctx, document, waiting)
	}
	var inFlight documentInFlight
	if errors.As(err, &inFlight) {
		return runstate.DocumentDelivery{RunID: inFlight.runID}, nil
	}
	if !ran || !saved.Status.Terminal() {
		// A pause retains the confirmed handoff in the conversation. A store or
		// infrastructure failure must never be claimed as delivered.
		if saved.RunID == "" {
			return runstate.DocumentDelivery{}, err
		}
		return documentDelivery(saved), err
	}
	if !retryable(saved) {
		return documentDelivery(saved), nil
	}
	// The run this call made stopped without judging the document. Where its
	// checks ran out of time, how many developer runs were going when it stopped
	// is recorded, so the next attempt can wait for fewer.
	if saved.DocumentStoppedOnTime() {
		if active, err := p.Store.Incomplete(); err == nil {
			if recordErr := p.recordDocumentRetry(ctx, saved.RunID, func(retry *runstate.DocumentRetry) {
				retry.DevelopersAtStop = runstate.HoldingDeveloperSlots(active) + 1
			}); recordErr != nil {
				return runstate.DocumentDelivery{}, recordErr
			}
		}
	}
	if attempt+1 >= runstate.MaxDocumentAttempts {
		saved, err = p.Store.Load(saved.RunID)
		if err != nil {
			return runstate.DocumentDelivery{}, err
		}
		return p.handOverDocument(ctx, docket, record, saved)
	}
	return runstate.DocumentDelivery{RunID: saved.RunID, Retrying: fmt.Sprintf("Document %s (%s) is confirmed, and run %s stopped without judging it: %s. Nothing about the document was found wrong, so the harness runs the same text again (attempt %d of %d); it does not need writing again, and nothing is counted against it.\n", document.WriteID, document.Candidate.Artifact.Title, saved.RunID, documentStopSays(saved), attempt+2, runstate.MaxDocumentAttempts)}, nil
}

// ResumableDocuments reads one conversation's document runs from the run store
// and answers two things about them. Judged is, for each document, how many of
// its handoffs ended on a run that judged it, which is the only kind of return
// MaxDocumentReturns counts. Resumable is, for each document, its latest handoff
// where that handoff's last run stopped without judging it and was not handed
// to the development manager: a document a build that returned every stop to
// its owner may have stopped publishing over nothing, and which PublishDocument
// continues from its stored text.
func (p Pipeline) ResumableDocuments(conversationID string) ([]runstate.DocumentPublication, map[string]int, error) {
	recorded, err := p.Store.Recorded()
	if err != nil {
		return nil, nil, err
	}
	// The last run of each handoff is the one with the highest attempt.
	last := map[string]runstate.State{}
	for _, state := range recorded {
		if state.Document == nil || state.Document.ConversationID != conversationID {
			continue
		}
		attempt := state.DocumentAttempt()
		if attempt < 0 {
			continue
		}
		if seen, ok := last[state.Document.WriteID]; !ok || seen.DocumentAttempt() < attempt {
			last[state.Document.WriteID] = state
		}
	}
	judged := map[string]int{}
	latest := map[string]runstate.State{}
	for _, state := range last {
		id := state.Document.Candidate.Artifact.ID
		if state.Status.Terminal() && state.Status != runstate.StatusSucceeded && state.DocumentJudged() {
			judged[id]++
		}
		if seen, ok := latest[id]; !ok || state.Document.Turn > seen.Document.Turn || (state.Document.Turn == seen.Document.Turn && state.Document.WriteID > seen.Document.WriteID) {
			latest[id] = state
		}
	}
	var resumable []runstate.DocumentPublication
	for _, state := range latest {
		if retryable(state) && !documentHandedOver(state) {
			resumable = append(resumable, *state.Document)
		}
	}
	slices.SortFunc(resumable, func(a, b runstate.DocumentPublication) int { return strings.Compare(a.WriteID, b.WriteID) })
	return resumable, judged, nil
}

// documentHeld is an attempt the intake hold kept from starting, carrying the
// hold's own reason so the owning conversation is told why.
type documentHeld struct {
	runID, summary string
}

func (h documentHeld) Error() string { return h.summary }

// documentWaiting is an attempt every developer slot being taken kept from
// starting; PublishDocument records the document as waiting for one.
type documentWaiting struct {
	runID    string
	attempt  int
	capacity runstate.CapacityError
}

func (w documentWaiting) Error() string { return w.capacity.Error() }

// documentInFlight is an attempt whose run another process holds: the
// scheduler, or another message of the conversation, is running it now, and its
// ending is read back at a later offer.
type documentInFlight struct {
	runID string
}

func (f documentInFlight) Error() string {
	return "run " + f.runID + " is already publishing this document"
}

// documentItem is the bookkeeping item one attempt's run is recorded under.
func documentItem(document runstate.DocumentPublication, attempt int) beads.WorkItem {
	return beads.WorkItem{ID: document.RunIDFor(attempt), Title: "Publishing " + document.Candidate.Artifact.Title, Status: "open", Description: "Publish exactly the document confirmed in its owning conversation.\nprotected-path grant: " + document.Candidate.Artifact.Path}
}

// retryable reports a document run that ended without landing and without
// judging its document: the one ending that is followed by another attempt.
func retryable(state runstate.State) bool {
	return state.Status.Terminal() && state.Status != runstate.StatusSucceeded && !state.DocumentJudged()
}

// documentAttempt finds where a handoff's runs stand: the attempt to start or
// continue now, and the run before it that stopped without judging the
// document. An attempt equal to MaxDocumentAttempts means every attempt
// stopped that way, and previous is the last of them.
func (p Pipeline) documentAttempt(document runstate.DocumentPublication) (int, *runstate.State, error) {
	var previous *runstate.State
	for attempt := 0; attempt < runstate.MaxDocumentAttempts; attempt++ {
		state, err := p.Store.Load(document.RunIDFor(attempt))
		if errors.Is(err, os.ErrNotExist) {
			return attempt, previous, nil
		}
		if err != nil {
			return 0, nil, err
		}
		if !retryable(state) {
			return attempt, previous, nil
		}
		previous = &state
	}
	return runstate.MaxDocumentAttempts, previous, nil
}

// awaitDocumentRetry decides whether the attempt after a stopped run may start
// now. After a check time limit it may not while as many developer runs are
// going as were when that run stopped: the same suite under the same load is
// stopped the same way, and a retry spent on that is an attempt lost. It
// answers what the conversation is told while it waits, or, once it may start,
// what it waited for.
func (p Pipeline) awaitDocumentRetry(previous runstate.State) (waiting, waited string, err error) {
	at := 0
	if previous.DocumentRetry != nil {
		at = previous.DocumentRetry.DevelopersAtStop
	}
	if !previous.DocumentStoppedOnTime() {
		return "", "started at once, because nothing about the stop depends on how busy the machine is", nil
	}
	if at <= 1 {
		return "", "started at once after its checks ran out of time, because no other developer run was going when they did, so there was no load to wait out", nil
	}
	active, err := p.Store.Incomplete()
	if err != nil {
		return "", "", fmt.Errorf("count the developer runs a document retry waits on: %w", err)
	}
	now := runstate.HoldingDeveloperSlots(active)
	if now+1 >= at {
		d := previous.Document
		return fmt.Sprintf("Document %s (%s) is confirmed, and run %s ran out of check time while %d developer runs were going. The next attempt waits until fewer are going than that; %d are going now, with this one it would be %d. It is checked again at the next message, and the document does not need writing again.\n", d.WriteID, d.Candidate.Artifact.Title, previous.RunID, at, now, now+1), "", nil
	}
	return "", fmt.Sprintf("started once fewer developer runs were going: %d when its checks ran out of time, %d with this attempt", at, now+1), nil
}

// recordDocumentRetry writes what one document run says about the attempt
// after it, under that run's lease.
func (p Pipeline) recordDocumentRetry(ctx context.Context, runID string, change func(*runstate.DocumentRetry)) error {
	state, lease, err := p.Store.AdoptRun(ctx, runID)
	if err != nil {
		return err
	}
	defer lease.Release()
	if state.DocumentRetry == nil {
		state.DocumentRetry = &runstate.DocumentRetry{}
	}
	change(state.DocumentRetry)
	return p.Store.Save(state)
}

// handOverDocument puts the last of a document's runs to the development
// manager once every attempt stopped without judging it. The confirmed text
// stays on each run's record, so whatever she decides starts from it rather
// than from the owner writing it again; the owner is told so, and nothing is
// counted against the document.
func (p Pipeline) handOverDocument(ctx context.Context, docket *Docketer, record documentItemRecorder, last runstate.State) (runstate.DocumentDelivery, error) {
	d := last.Document
	delivery := documentDelivery(last)
	delivery.HandedOver = true
	delivery.Detail = fmt.Sprintf("Document %s (%s) did not land: %d runs in a row stopped without judging it, the last being run %s (%s). It is handed to the development manager as a stopped run, with the confirmed text kept on the run's record; the %s does not need to write it again, and nothing is counted against it.\n", d.WriteID, d.Candidate.Artifact.Title, runstate.MaxDocumentAttempts, last.RunID, documentStopSays(last), d.Owner.Title())
	if last.DocumentRetry != nil && last.DocumentRetry.HandedOver {
		return delivery, nil
	}
	state, lease, err := p.Store.AdoptRun(ctx, last.RunID)
	if err != nil {
		return runstate.DocumentDelivery{}, err
	}
	defer lease.Release()
	if state.DocumentRetry == nil {
		state.DocumentRetry = &runstate.DocumentRetry{}
	}
	state.DocumentRetry.HandedOver = true
	if strings.TrimSpace(state.Blocker) == "" {
		state.Blocker = fmt.Sprintf("Publishing document %s (%s) from conversation %s stopped %d times without anything judging the document; the last stop: %s. The confirmed text is on this run's record.", d.WriteID, d.Candidate.Artifact.Title, d.ConversationID, runstate.MaxDocumentAttempts, documentStopSays(state))
	}
	if err := p.Store.Save(state); err != nil {
		return runstate.DocumentDelivery{}, err
	}
	if docket != nil {
		if _, err := docket.RecordStoppedRun(state); err != nil {
			return runstate.DocumentDelivery{}, fmt.Errorf("hand the stopped document run %s to the development manager: %w", state.RunID, err)
		}
	}
	record.note(ctx, fmt.Sprintf("Yoyodyne handed the publishing of document %s (%s) to the development manager: %d runs stopped without anything judging the document, the last being run %s. The confirmed text is kept on that run's record.", d.WriteID, d.Candidate.Artifact.Title, runstate.MaxDocumentAttempts, state.RunID))
	return delivery, nil
}

// documentStopSays is a stopped document run's reason in one line, never empty.
func documentStopSays(state runstate.State) string {
	if said := strings.TrimSpace(runstate.StopReason(state.RecordedStopClass(), state.Failure)); said != "" {
		return oneline.Fold(said, 600)
	}
	return fmt.Sprintf("it ended %s at its %s step and recorded no reason", state.Status, nonEmpty(string(state.Phase), "unrecorded"))
}

// publishDocumentAttempt starts or continues the run that makes one attempt,
// reporting the record it left and whether a run was entered at all.
func (p Pipeline) publishDocumentAttempt(ctx context.Context, document runstate.DocumentPublication, attempt int, policy string, record documentItemRecorder) (runstate.State, bool, error) {
	tracker := p.Tracker.(*documentTracker)
	item := tracker.item
	publishing, skipped, err := p.resolvePublishing(ctx)
	if err != nil {
		return runstate.State{}, false, err
	}
	runID := document.RunIDFor(attempt)
	state, err := p.Store.Load(runID)
	var lease *runstate.Lease
	started := false
	if err == nil {
		// A run of this attempt is recorded, so it is not waiting for a slot
		// whoever reserved it.
		p.clearDocumentWait(document)
		state, lease, err = p.Store.AdoptRun(ctx, runID)
		if errors.Is(err, runstate.ErrRunHeld) {
			// The scheduler, or another message of the conversation, is running it
			// now. Its ending is read back at a later offer.
			return runstate.State{}, false, documentInFlight{runID: runID}
		}
		if err != nil {
			return runstate.State{}, false, err
		}
		defer lease.Release()
		if !reflect.DeepEqual(state.Document, &document) {
			return runstate.State{}, false, errors.New("the run already records a different document handoff")
		}
		if state.Status.Terminal() {
			return state, true, nil
		}
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			return runstate.State{}, false, err
		}
		if err := p.Worktrees.ValidateReady(ctx); err != nil {
			return runstate.State{}, false, err
		}
		if outcome, held, err := p.holdIntake(item.ID); err != nil || held {
			if err != nil {
				return runstate.State{}, false, err
			}
			return runstate.State{}, false, documentHeld{runID: runID, summary: nonEmpty(strings.TrimSpace(outcome.Summary), "intake is held, and the hold gave no reason")}
		}
		target, err := p.Worktrees.CurrentBranch(ctx)
		if err != nil {
			return runstate.State{}, false, err
		}
		state = runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: p.Config.Product.ID, RepositoryID: string(p.Config.Product.RepositoryID), WorkItemID: item.ID, WorkItemTitle: item.Title, Backend: p.reviewer().Backend, ConfigRevision: p.Config.Revision(), Build: p.Build, Status: runstate.StatusPending, Document: &document, TargetBranch: target}
		state, lease, err = p.reserveRun(ctx, state)
		var capacity runstate.CapacityError
		var existing runstate.ExistingWorkItemError
		switch {
		case errors.As(err, &capacity):
			return runstate.State{}, false, documentWaiting{runID: runID, attempt: attempt, capacity: capacity}
		case errors.As(err, &existing):
			// Another process reserved it between the look above and this one.
			p.clearDocumentWait(document)
			return runstate.State{}, false, documentInFlight{runID: runID}
		case err != nil:
			return runstate.State{}, false, err
		}
		defer lease.Release()
		p.clearDocumentWait(document)
		started = true
		record.note(ctx, fmt.Sprintf("Yoyodyne started run %s to publish document %s (%s), written by the %s in conversation %s at turn %d; attempt %d of at most %d.", runID, document.WriteID, document.Candidate.Artifact.Title, document.Owner.Title(), document.ConversationID, document.Turn, attempt+1, runstate.MaxDocumentAttempts))
	}
	tracker.item.Status = "in_progress"
	invariants, err := p.loadInvariants()
	if err != nil {
		return runstate.State{}, false, err
	}
	a := &activeRun{pipeline: p, state: state, item: tracker.item, claimed: true, publishing: publishing, invariants: invariants, context: fmt.Sprintf("Publish the %s's exact document %s from conversation %s, turn %d. Confirmed under %s. No developer may rewrite it.\n", document.Owner.Title(), document.Candidate.Artifact.Title, document.ConversationID, document.Turn, policy), outcome: Outcome{RunID: state.RunID, WorkItemID: state.WorkItemID, Status: state.Status, Phase: state.Phase, PublishSkipped: skipped, Summary: "Publish the owning role's confirmed document without rewriting its content.", PullRequest: state.PullRequest}}
	a.worktree = gitworktree.Worktree{RunID: state.RunID, WorkItemID: state.WorkItemID, Path: state.WorktreePath, Branch: state.Branch, BaseCommit: state.BaseCommit, TargetBranch: state.TargetBranch, HarnessCommit: state.HarnessCommit}
	a.outcome.Branch, a.outcome.WorktreePath, a.outcome.BaseCommit = state.Branch, state.WorktreePath, state.BaseCommit
	if err := a.claim(ctx); err != nil {
		return runstate.State{}, false, err
	}
	if state.WorktreePath == "" {
		if err := a.beginDeliveryTrial(); err != nil {
			return runstate.State{}, false, err
		}
		a.observe(ctx, deliveryClaim, "claimed")
	} else {
		a.resumeDeliveryTrial()
	}
	a.carryReviewEvidence()
	if state.Integration == nil && state.Phase == runstate.PhaseIntegrating {
		if err := a.recoverDocumentIntegration(ctx); err != nil {
			return runstate.State{}, false, err
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
			// A checkout that cannot be cut ends this attempt like any other stop
			// that judged nothing, rather than leaving a pending run behind it.
			worktree, createErr := p.Worktrees.Create(ctx, gitworktree.CreateRequest{ResumeCreation: true, RunID: state.RunID, WorkItemID: item.ID, BaseRef: state.TargetBranch, TargetBranch: state.TargetBranch})
			if createErr != nil {
				_, err = a.fail(fmt.Errorf("cut the checkout for the document run: %w", createErr), runstate.StatusFailed)
				return p.settledDocumentAttempt(ctx, state.RunID, started, record, err)
			}
			a.recordWorktree(worktree)
			a.state.Status, a.state.Phase = runstate.StatusRunning, runstate.PhaseDeveloping
			if err := p.Store.Save(a.state); err != nil {
				return runstate.State{}, false, err
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
	return p.settledDocumentAttempt(ctx, state.RunID, true, record, err)
}

// settledDocumentAttempt reads back what an attempt left, and writes its end
// on the work item the document names where it ended.
func (p Pipeline) settledDocumentAttempt(ctx context.Context, runID string, ran bool, record documentItemRecorder, err error) (runstate.State, bool, error) {
	saved, loadErr := p.Store.Load(runID)
	if loadErr != nil {
		return runstate.State{}, ran, errors.Join(err, loadErr)
	}
	if !saved.Status.Terminal() {
		return saved, ran, err
	}
	d := saved.Document
	switch {
	case saved.Status == runstate.StatusSucceeded && saved.Integration != nil:
		record.note(ctx, fmt.Sprintf("Yoyodyne's run %s published document %s (%s) at %s.", saved.RunID, d.WriteID, d.Candidate.Artifact.Title, d.Candidate.Artifact.Path))
	case saved.DocumentJudged():
		record.note(ctx, fmt.Sprintf("Yoyodyne's run %s did not publish document %s (%s), and handed it back to the %s to revise: %s", saved.RunID, d.WriteID, d.Candidate.Artifact.Title, d.Owner.Title(), documentStopSays(saved)))
	default:
		record.note(ctx, fmt.Sprintf("Yoyodyne's run %s stopped publishing document %s (%s) without judging it: %s", saved.RunID, d.WriteID, d.Candidate.Artifact.Title, documentStopSays(saved)))
	}
	// A run that ended is settled whatever error came with ending it; the
	// record says what happened.
	return saved, ran, nil
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
	WaitForSlot(ctx context.Context, document runstate.DocumentPublication, attempt int, at time.Time) (runstate.DocumentWait, bool, error)
	ClearDocumentWait(runID string) error
}

// waitForSlot records a document whose attempt every developer slot being
// taken refused, so the scheduler starts it in the next slot that frees. Its
// conversation keeps the handoff and is told once that it waits; nothing about
// it is a failure.
func (p Pipeline) waitForSlot(ctx context.Context, document runstate.DocumentPublication, refused documentWaiting) (runstate.DocumentDelivery, error) {
	capacity := refused.capacity
	delivery := runstate.DocumentDelivery{RunID: refused.runID, WaitingForSlot: &capacity}
	waits, ok := p.Store.(DocumentWaits)
	if !ok {
		delivery.Detail = fmt.Sprintf("Document %s (%s) is confirmed and waiting for a developer slot (%s). It is tried again at the next message in this conversation; it does not need writing again.\n", document.WriteID, document.Candidate.Artifact.Title, capacity.Error())
		return delivery, nil
	}
	if _, waiting, err := waits.WaitForSlot(ctx, document, refused.attempt, p.clock().Now()); err != nil {
		return runstate.DocumentDelivery{}, fmt.Errorf("record document %s as waiting for a developer slot: %w", document.WriteID, err)
	} else if !waiting {
		// Another process reserved its run under the same lock just now.
		return runstate.DocumentDelivery{RunID: refused.runID}, nil
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
	// The run is the attempt this call made, which a document whose earlier run
	// stopped without judging it gives a run of its own.
	runID := nonEmpty(delivery.RunID, document.RunID())
	outcome := Outcome{WorkItemID: runID, Summary: strings.TrimSpace(nonEmpty(delivery.Detail, delivery.Retrying))}
	if state, loadErr := p.Store.Load(runID); loadErr == nil {
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
	result.Judged = !result.Landed && state.DocumentJudged()
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

// documentItemRecorder writes a document run's start and end onto the work item
// the document's latest revision names, so a reader of that item sees its
// document being published without finding the run. The item is named by the
// revision reason's first word, which is how an owning role records the item a
// revision was written for ("yoyodyne-ifd.433.11: ..."); a first word the
// tracker holds no item under names nothing, and nothing is written.
//
// The writes are bounded and never stop the run: the run's own record is the
// account, and a tracker too busy to take a note costs the note.
type documentItemRecorder struct {
	tracker  WorkTracker
	document runstate.DocumentPublication
}

// documentWorkItem is the work item a document's latest revision names, or
// empty where it names none.
func documentWorkItem(document artifact.ConfirmedDocument) string {
	revisions := document.Artifact.Revisions
	if len(revisions) == 0 {
		return ""
	}
	fields := strings.Fields(revisions[len(revisions)-1].Reason)
	if len(fields) == 0 {
		return ""
	}
	id := strings.TrimRight(fields[0], ":,;")
	if !strings.Contains(id, "-") || !beads.ValidIssueID(id) {
		return ""
	}
	return id
}

func (r documentItemRecorder) note(ctx context.Context, note string) {
	id := documentWorkItem(r.document.Candidate)
	if r.tracker == nil || id == "" {
		return
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := r.tracker.Show(bounded, id); err != nil {
		return
	}
	_, _ = r.tracker.RecordOutcome(bounded, id, note)
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
