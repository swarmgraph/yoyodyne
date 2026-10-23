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
	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
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

// LandsApprovedDocuments reports whether a document the operator approves can
// go through a reviewed run, rather than being written into the checkout: the
// project keeps its intent in its own repository and has an independent
// reviewer configured. Where the project integrates automatically the run lands
// it; where it does not, the run checks and reviews it and leaves it on its
// branch for the operator to merge, as every change there is (reviewDocument).
//
// A companion intent repository is answered no, unlike in PublishesDocuments,
// because holding a person's approval until that run is built would keep the
// document out of the repository indefinitely, where the write into the
// companion repository holds no run up. A project with no reviewer to run is
// answered no as well, because the run could only stop for want of one.
func (p Pipeline) LandsApprovedDocuments() bool {
	return !p.Config.Product.HasIntentRepository() && p.validateReviewPolicy() == nil
}

// PublishStandingApproval lands the operator's approval of a document as it now
// stands, given with `yoyo artifact approve`, through a reviewed run that
// changes only that file, rather than writing it into the checkout. The
// document's text is what its owner last recorded; the run adds the approval,
// and the handoff is the owning role's, so the same ownership, filing, checks,
// and independent review hold as for a document that role wrote. Where
// LandsApprovedDocuments says no run can carry it, the caller writes the
// approval into the working tree instead.
//
// Each approval is its own handoff, named by the document, the revision
// approved, and the file it would land, so giving the approval again after a
// run that did not land it starts a fresh run rather than colliding with it.
func (p Pipeline) PublishStandingApproval(ctx context.Context, id, reason string, now time.Time) (runstate.DocumentPublication, runstate.DocumentDelivery, error) {
	candidate, err := artifact.StoreFor(p.Repository, p.Config.Product).PrepareStandingApproval(id, reason, now)
	if err != nil {
		return runstate.DocumentPublication{}, runstate.DocumentDelivery{}, err
	}
	owner, owned := artifact.Owner(candidate.Artifact.Kind)
	if !owned {
		return runstate.DocumentPublication{}, runstate.DocumentDelivery{}, fmt.Errorf("no role owns a %s artifact, so no run can land an approval of %s", candidate.Artifact.Kind, candidate.Artifact.ID)
	}
	publication := runstate.DocumentPublication{
		Candidate: candidate,
		Owner:     owner,
		WriteID:   fmt.Sprintf("%s%s.%d-%s", runstate.StandingApprovalPrefix, candidate.Artifact.ID, len(candidate.Artifact.Revisions)-1, artifact.DocumentIdentity([]byte(candidate.Content))[:12]),
	}
	delivery, err := p.PublishDocument(ctx, publication)
	return publication, delivery, err
}

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
// candidate. Only the owning conversation, or the operator's approval of a
// document as it stands (PublishStandingApproval), can supply this handoff;
// there is no developer invocation or repair of its content inside a
// publication run.
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
	// A document the operator approved answers to no approval setting: a person
	// decided it, and only the harness's own confirmation is bounded by the
	// setting it was given under. Nor does one the Lead Product Manager approved
	// as a change consistent with intent, which is a decision the operator
	// delegated to her; the candidate's own validation holds her approval to the
	// revision that claim is on.
	confirmed := "approved by the operator"
	if document.Candidate.ProductManagerApproved() {
		confirmed = "approved by the " + domain.RoleProductManager.Title() + " as consistent with intent"
	}
	if !document.Candidate.Decided() {
		policy := artifact.Policy{Brief: p.Config.Approvals.Brief, Goals: p.Config.Approvals.Goals, Designs: p.Config.Approvals.Designs, SpecificationsHome: p.Config.Product.Specifications}
		name, mode, governed := policy.SettingFor(document.Candidate.Artifact)
		approval, _ := document.Candidate.Artifact.LatestApproval()
		if !governed || mode != domain.ApprovalAutomatic || approval.Policy != name {
			return p.refuseDocument(document, errors.New("the document's automatic confirmation policy no longer applies"))
		}
		confirmed = "confirmed under " + name
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
	if err := documentRouteRefusal(p.Config, []string{document.Candidate.Artifact.Path}); err != nil {
		return p.refuseDocument(document, err)
	}
	// A document the operator or the Lead Product Manager approved goes through
	// a reviewed run whatever the integration setting, and is left for the
	// operator to merge where it is not automatic; the harness's own confirmation
	// lands only where the run can.
	if !p.automatic() && !document.Candidate.Decided() {
		return p.refuseDocument(document, errors.New("reviewed document publication requires automatic integration"))
	}
	if !p.automatic() {
		if err := p.validateReviewPolicy(); err != nil {
			return p.refuseDocument(document, err)
		}
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
	p.Selection = runstate.Selection{By: runstate.SelectedByConversation, Reason: fmt.Sprintf("Document %s was supplied, %s, and %s; publish its exact content through independent review.", document.WriteID, document.Origin(), confirmed)}
	if retried := document.Retried; retried != nil {
		p.Selection.Reason += " " + oneline.Fold(fmt.Sprintf("It retries run %s, as decided by %s at %s: %s", retried.FromRun, retried.DecidedBy, retried.DecidedAt.Local().Format("2006-01-02 15:04 MST"), retried.Reason), 1024)
	}
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
			waited := documentRetryWaited(*previous)
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
	saved, ran, err := p.publishDocumentAttempt(ctx, document, attempt, confirmed, record)
	var held documentHeld
	if errors.As(err, &held) {
		return runstate.DocumentDelivery{RunID: held.runID, Detail: held.summary, Retrying: fmt.Sprintf("Document %s (%s) is confirmed, and its run has not started: %s. %s\n", document.WriteID, document.Candidate.Artifact.Title, held.summary, triedAgain(document))}, nil
	}
	var waiting documentWaiting
	if errors.As(err, &waiting) {
		return p.waitForSlot(ctx, document, waiting)
	}
	var inFlight documentInFlight
	if errors.As(err, &inFlight) {
		return runstate.DocumentDelivery{RunID: inFlight.runID}, nil
	}
	var restarting documentHeldForRestart
	if errors.As(err, &restarting) {
		return runstate.DocumentDelivery{RunID: restarting.runID, Retrying: fmt.Sprintf("Document %s (%s) is confirmed, and its run has not started: %s. It does not need writing again.\n", document.WriteID, document.Candidate.Artifact.Title, restarting.Error())}, nil
	}
	if !ran || !saved.Status.Terminal() {
		// A pause retains the confirmed handoff in the conversation. A store or
		// infrastructure failure must never be claimed as delivered.
		if saved.RunID == "" {
			return runstate.DocumentDelivery{}, err
		}
		return documentDelivery(saved), err
	}
	if saved.DocumentConflict != nil {
		// Nothing starts this text again, so it is not waiting for a slot either.
		p.clearDocumentWait(document)
		return p.handOverDocument(ctx, docket, record, saved)
	}
	if !retryable(saved) {
		return documentDelivery(saved), nil
	}
	if attempt+1 >= runstate.MaxDocumentAttempts {
		saved, err = p.Store.Load(saved.RunID)
		if err != nil {
			return runstate.DocumentDelivery{}, err
		}
		return p.handOverDocument(ctx, docket, record, saved)
	}
	if document.StandingApproval() {
		return runstate.DocumentDelivery{RunID: saved.RunID, Retrying: fmt.Sprintf("Document %s (%s) is confirmed, and run %s stopped without judging it: %s. Nothing about the document was found wrong. %s\n", document.WriteID, document.Candidate.Artifact.Title, saved.RunID, documentStopSays(saved), triedAgain(document))}, nil
	}
	if document.Retried != nil {
		// A retry the development manager decided has no conversation holding it to
		// offer it again, so its next attempt is put where the scheduler starts it.
		if waits, ok := p.Store.(DocumentWaits); ok {
			if _, _, err := waits.WaitForSlot(ctx, document, attempt+1, p.clock().Now()); err != nil {
				return runstate.DocumentDelivery{RunID: saved.RunID}, fmt.Errorf("record the next attempt at retried document %s for the scheduler: %w", document.WriteID, err)
			}
		}
		return runstate.DocumentDelivery{RunID: saved.RunID, Retrying: fmt.Sprintf("Document %s (%s), retried by the development manager, stopped in run %s without being judged: %s. The harness starts attempt %d of %d itself as a document landing frees.\n", document.WriteID, document.Candidate.Artifact.Title, saved.RunID, documentStopSays(saved), attempt+2, runstate.MaxDocumentAttempts)}, nil
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
	last := documentHandoffEnds(recorded, func(state runstate.State) bool { return state.Document.ConversationID == conversationID })
	judged := map[string]int{}
	for _, state := range last {
		if state.Status.Terminal() && state.Status != runstate.StatusSucceeded && state.DocumentJudged() {
			judged[state.Document.Candidate.Artifact.ID]++
		}
	}
	var resumable []runstate.DocumentPublication
	for _, state := range latestDocumentHandoffs(last) {
		// A run the development manager decided is hers: a retry already put the
		// same text into a handoff of its own, and a retirement withdrew it.
		if retryable(state) && !documentHandedOver(state) && state.DocumentDecision == nil {
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

// documentWaiting is an attempt every document landing being taken kept from
// starting; PublishDocument records the document as waiting for one to free.
type documentWaiting struct {
	runID    string
	attempt  int
	capacity runstate.CapacityError
}

func (w documentWaiting) Error() string { return w.capacity.Error() }

// documentHeldForRestart is an attempt a watch session about to restart onto a
// new build kept from starting, because a run it started now would be one the
// restart has to wait for. The document is left waiting where the scheduler
// starts it, so the session that comes back lands it without a message from
// its conversation.
type documentHeldForRestart struct {
	runID string
}

func (h documentHeldForRestart) Error() string {
	return "the watch session running this pass is about to restart onto a new build, so the run publishing this document starts in the session that comes back"
}

// holdDocumentForRestart leaves a document whose attempt a restart kept from
// starting waiting for a document landing, which the next session's first
// pull starts (scheduledocuments.go). A store that keeps nothing waiting
// leaves it to its conversation's next message instead.
func (p Pipeline) holdDocumentForRestart(ctx context.Context, document runstate.DocumentPublication, attempt int) error {
	held := documentHeldForRestart{runID: document.RunIDFor(attempt)}
	waits, ok := p.Store.(DocumentWaits)
	if !ok {
		return held
	}
	if _, _, err := waits.WaitForSlot(ctx, document, attempt, p.clock().Now()); err != nil {
		return fmt.Errorf("record document %s as waiting for the session that comes back after a restart: %w", document.WriteID, err)
	}
	return held
}

// documentInFlight is an attempt whose run another process holds: the
// scheduler, or another message of the conversation, is running it now, and its
// ending is read back at a later offer.
type documentInFlight struct {
	runID string
}

func (f documentInFlight) Error() string {
	return "run " + f.runID + " is already publishing this document"
}

// triedAgain says what starts a document's run again after one that did not
// start or finish. A conversation offers its document again at its next
// message; an approval given with `yoyo artifact approve` has no conversation
// to do that, so it is given again.
func triedAgain(document runstate.DocumentPublication) string {
	if document.StandingApproval() {
		return "Nothing was written to the checkout; give the approval again with yoyo artifact approve to start a fresh run."
	}
	return "It is tried again at the next message in its conversation and does not need writing again."
}

// documentItem is the bookkeeping item one attempt's run is recorded under.
func documentItem(document runstate.DocumentPublication, attempt int) beads.WorkItem {
	return beads.WorkItem{ID: document.RunIDFor(attempt), Title: "Publishing " + document.Candidate.Artifact.Title, Status: "open", Description: "Publish exactly the document confirmed in its owning conversation.\nprotected-path grant: " + document.Candidate.Artifact.Path}
}

// retryable reports a document run that ended without landing and without
// judging its document: the one ending that is followed by another attempt. A
// run that found the target's copy of its document changed is not one, because
// another run of the same text meets the same copy; it goes to the development
// manager instead (see handOverDocument).
func retryable(state runstate.State) bool {
	return state.Status.Terminal() && state.Status != runstate.StatusSucceeded && !state.DocumentJudged() && state.DocumentConflict == nil
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

// documentRetryWaited says what the attempt after a stopped run waited for
// before it started. It waits for nothing but a free document landing, which
// ReserveDocumentLanding counts against execution.document_landings when the
// attempt is reserved, and never for developer runs, even after a check time
// limit: a document run holds no developer slot, and developer runs are nearly
// always going, so a retry that waited for fewer of them could wait
// indefinitely.
func documentRetryWaited(previous runstate.State) string {
	if previous.DocumentStoppedOnTime() {
		return "started once a document landing was free after its checks ran out of time; how many developer runs are going is not waited on"
	}
	return "started once a document landing was free, because nothing about the stop depends on how busy the machine is"
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
// manager once every attempt stopped without judging it, or at once where the
// run found the target's copy of the document changed after the text was
// written, which no further attempt can get past. The confirmed text stays on
// each run's record, so whatever she decides starts from it rather than from
// the owner writing it again; the owner is told so, and nothing is counted
// against the document.
func (p Pipeline) handOverDocument(ctx context.Context, docket *Docketer, record documentItemRecorder, last runstate.State) (runstate.DocumentDelivery, error) {
	d := last.Document
	delivery := documentDelivery(last)
	delivery.HandedOver = true
	why := fmt.Sprintf("%d runs in a row stopped without judging it, the last being run %s (%s)", runstate.MaxDocumentAttempts, last.RunID, documentStopSays(last))
	blocker := fmt.Sprintf("Publishing document %s (%s) from conversation %s stopped %d times without anything judging the document; the last stop: %s. The confirmed text is on this run's record.", d.WriteID, d.Candidate.Artifact.Title, d.ConversationID, runstate.MaxDocumentAttempts, documentStopSays(last))
	noted := fmt.Sprintf("%d runs stopped without anything judging the document, the last being run %s", runstate.MaxDocumentAttempts, last.RunID)
	if conflict := last.DocumentConflict; conflict != nil {
		why = fmt.Sprintf("run %s stopped because %s, so the harness does not start it again", last.RunID, conflict.Says())
		blocker = fmt.Sprintf("Publishing document %s (%s) from conversation %s stopped because %s. The confirmed text is on this run's record; retrying it lands nothing while that copy stands, and retiring it is the other decision.", d.WriteID, d.Candidate.Artifact.Title, d.ConversationID, conflict.Says())
		noted = fmt.Sprintf("run %s found the copy of %s on %s changed after the text was written", last.RunID, conflict.Path, conflict.TargetBranch)
	}
	delivery.Detail = fmt.Sprintf("Document %s (%s) did not land: %s. It is handed to the development manager as a stopped run, with the confirmed text kept on the run's record; the %s does not need to write it again, and nothing is counted against it.\n", d.WriteID, d.Candidate.Artifact.Title, why, d.Owner.Title())
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
		state.Blocker = blocker
	}
	if err := p.Store.Save(state); err != nil {
		return runstate.DocumentDelivery{}, err
	}
	if docket != nil {
		if _, err := docket.RecordStoppedRun(state); err != nil {
			return runstate.DocumentDelivery{}, fmt.Errorf("hand the stopped document run %s to the development manager: %w", state.RunID, err)
		}
	}
	record.note(ctx, fmt.Sprintf("Yoyodyne handed the publishing of document %s (%s) to the development manager: %s. The confirmed text is kept on that run's record.", d.WriteID, d.Candidate.Artifact.Title, noted))
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
func (p Pipeline) publishDocumentAttempt(ctx context.Context, document runstate.DocumentPublication, attempt int, confirmed string, record documentItemRecorder) (runstate.State, bool, error) {
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
	// A run inside a recurring pass of a watch session is counted while it
	// goes, so the session's restart onto a new build waits for it rather than
	// stopping it; past the session's drain bound no new one starts, and the
	// document waits for the session that comes back (passPublications).
	publications := passPublicationsOf(ctx)
	if !publications.begin(err == nil) {
		return runstate.State{}, false, p.holdDocumentForRestart(ctx, document, attempt)
	}
	defer publications.end()
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
		record.note(ctx, fmt.Sprintf("Yoyodyne started run %s to publish document %s (%s), %s; attempt %d of at most %d.", runID, document.WriteID, document.Candidate.Artifact.Title, document.Origin(), attempt+1, runstate.MaxDocumentAttempts))
	}
	tracker.item.Status = "in_progress"
	invariants, err := p.loadInvariants()
	if err != nil {
		return runstate.State{}, false, err
	}
	a := &activeRun{pipeline: p, state: state, item: tracker.item, claimed: true, publishing: publishing, invariants: invariants, context: fmt.Sprintf("Publish the %s's exact document %s, %s, %s. No developer may rewrite it.\n", document.Owner.Title(), document.Candidate.Artifact.Title, document.Origin(), confirmed), outcome: Outcome{RunID: state.RunID, WorkItemID: state.WorkItemID, Status: state.Status, Phase: state.Phase, PublishSkipped: skipped, Summary: "Publish the owning role's confirmed document without rewriting its content.", PullRequest: state.PullRequest}}
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

// DocumentWaits is where a confirmed document waits for a document landing to
// free when every one is taken; see runstate.DocumentWait. It is an optional capability
// of the pipeline's StateStore, satisfied by *runstate.Store. A store without it
// keeps nothing waiting, and the document is offered again only at its
// conversation's next message, as it was before the scheduler could start one.
type DocumentWaits interface {
	WaitForSlot(ctx context.Context, document runstate.DocumentPublication, attempt int, at time.Time) (runstate.DocumentWait, bool, error)
	ClearDocumentWait(runID string) error
}

// waitForSlot records a document whose attempt every document landing being
// taken refused, so the scheduler starts it as soon as one frees. Its
// conversation keeps the handoff and is told once that it waits; nothing about
// it is a failure.
func (p Pipeline) waitForSlot(ctx context.Context, document runstate.DocumentPublication, refused documentWaiting) (runstate.DocumentDelivery, error) {
	capacity := refused.capacity
	delivery := runstate.DocumentDelivery{RunID: refused.runID, WaitingForSlot: &capacity}
	waits, ok := p.Store.(DocumentWaits)
	if !ok {
		delivery.Detail = fmt.Sprintf("Document %s (%s) is confirmed and waiting for a document landing to free (%s). %s\n", document.WriteID, document.Candidate.Artifact.Title, capacity.Error(), triedAgain(document))
		return delivery, nil
	}
	if _, waiting, err := waits.WaitForSlot(ctx, document, refused.attempt, p.clock().Now()); err != nil {
		return runstate.DocumentDelivery{}, fmt.Errorf("record document %s as waiting for a document landing: %w", document.WriteID, err)
	} else if !waiting {
		// Another process reserved its run under the same lock just now.
		return runstate.DocumentDelivery{RunID: refused.runID}, nil
	}
	delivery.Detail = fmt.Sprintf("Document %s (%s) is confirmed and waiting for a document landing to free (%s). The harness starts its reviewed run as soon as one does, without waiting for a message here and without taking a developer slot; it does not need writing again.\n", document.WriteID, document.Candidate.Artifact.Title, capacity.Error())
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
// document landing to free: the same publication its conversation would offer,
// through PublishDocument, answered as the outcome of a run the scheduler
// hosted. A document that lost the landing to another run is answered with the
// capacity error, which the scheduler records as declined rather than failed.
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
			conflict := a.documentConflict(ctx)
			a.state.DocumentConflict = &conflict
			return errors.New(conflict.Says())
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

// maxDocumentHistory bounds how many of a document's changes on the target
// branch are read back for the copy its text was written against, and
// maxDocumentHistoryBytes how large a copy is read. A copy older than that is
// recorded as not found rather than searched for without end.
const (
	maxDocumentHistory      = 50
	maxDocumentHistoryBytes = 8 << 20
)

// documentConflict is the record of a document run whose target copy is not
// the copy its text was written against. The commit that made that copy is
// looked for among the document's recent changes on the target branch; a
// history that cannot be read leaves it unnamed rather than losing the stop.
func (a *activeRun) documentConflict(ctx context.Context) runstate.DocumentConflict {
	candidate := a.state.Document.Candidate
	conflict := runstate.DocumentConflict{Path: candidate.Artifact.Path, TargetBranch: a.worktree.TargetBranch, TargetCommit: a.worktree.BaseCommit, NewDocument: candidate.Before == "", RecordedAt: a.pipeline.clock().Now().UTC()}
	if conflict.NewDocument {
		return conflict
	}
	commits, err := a.pipeline.Worktrees.CommitsChanging(ctx, a.worktree.BaseCommit, candidate.Artifact.Path, maxDocumentHistory)
	if err != nil {
		return conflict
	}
	for _, commit := range commits {
		copied, err := a.pipeline.Worktrees.FileAtCommit(ctx, commit, candidate.Artifact.Path, maxDocumentHistoryBytes)
		if err == nil && copied.Content != nil && artifact.DocumentIdentity(copied.Content) == candidate.Before {
			conflict.WrittenAgainst = commit
			break
		}
	}
	return conflict
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
	switch {
	case result.Landed:
		result.Detail = fmt.Sprintf("Document %s (%s) landed through reviewed run %s at %s.\n", d.WriteID, d.Candidate.Artifact.Title, state.RunID, d.Candidate.Artifact.Path)
	case state.Status == runstate.StatusSucceeded:
		// A run under an integration setting that is not automatic: the document
		// passed and is waiting for the operator's merge, as every change there is.
		where := "on branch " + state.Branch
		if state.PullRequest != nil && state.PullRequest.URL != "" {
			where += " and pull request " + state.PullRequest.URL
		}
		result.Detail = fmt.Sprintf("Document %s (%s) passed its checks and the independent review in run %s and is %s, for the operator to merge, because this project's integration is not automatic. Nothing is waiting in the checkout.\n", d.WriteID, d.Candidate.Artifact.Title, state.RunID, where)
	default:
		var detail strings.Builder
		next := fmt.Sprintf("The %s must revise the document in this conversation and submit it again; that opens a fresh reviewed run.", d.Owner.Title())
		if d.StandingApproval() {
			next = "Only the operator's approval of it as it stands was being landed; nothing reached the checkout, and once what stopped it is answered the approval is given again with yoyo artifact approve."
		}
		fmt.Fprintf(&detail, "Document %s (%s) did not land in run %s. %s\n%s\n", d.WriteID, d.Candidate.Artifact.Title, state.RunID, next, state.Failure)
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
	bounded, cancel := trackerRecordContext(ctx)
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
		automatic := a.pipeline.automatic()
		verdict, err := a.reviewChange(ctx)
		// The definition a project that does not integrate automatically binds has
		// no review step, so the review is not put to it.
		if automatic {
			a.observeReviewEnded(ctx, verdict, err, budgetSpent)
		}
		if err != nil {
			return a.stop(ctx, err)
		}
		if verdict != review.DecisionApprove {
			what := "the document"
			if a.state.OutsideFile != nil {
				what = "the outside file"
			}
			return a.fail(stoppedBy(runstate.StopReview, errors.New("the independent reviewer refused "+what+": "+a.state.ReviewSummary)), runstate.StatusFailed)
		}
		// Where integration is not automatic the reviewed document is left on its
		// branch, and on its pull request where the project publishes, for the
		// operator to merge, exactly as every other change there is.
		if !automatic {
			return a.finish(ctx)
		}
		outcome, replayed, err := a.promoteApproved(ctx)
		if !replayed {
			return outcome, err
		}
	}
}

func (a *activeRun) validateIndependentReview() error {
	if a.state.OutsideFile != nil {
		if a.outcome.ReviewSessionID == "" || a.outcome.ReviewModel == "" {
			return errors.New("bringing a file in requires a recorded independent reviewer invocation")
		}
		return nil
	}
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
	if a.state.HarnessAuthored() {
		return nil
	} // No developer ran anything to record; the configured checks still run.
	return a.gateSelfVerification(ctx)
}

// documentRouteRefusal is why a change may not take the document landing route,
// or nil where it may: only a change whose every path lies inside one of the
// artifact homes the configuration names — the product specifications with
// their goals, the designs, the decisions, and the invariants, each home's
// directory index included — lands as a document. Anything else goes the
// ordinary way, as a developer's work item.
func documentRouteRefusal(cfg config.Config, changed []string) error {
	homes := artifacthome.Homes(cfg)
	var outside []string
	for _, changedPath := range changed {
		inside := slices.ContainsFunc(homes, func(home artifacthome.Home) bool {
			return strings.HasPrefix(changedPath, home.Directory+"/")
		})
		if !inside {
			outside = append(outside, changedPath)
		}
	}
	if len(changed) == 0 || len(outside) > 0 {
		directories := make([]string, 0, len(homes))
		for _, home := range homes {
			directories = append(directories, home.Directory)
		}
		return fmt.Errorf("only a change wholly inside the artifact homes (%s) lands as a document; %v is not", strings.Join(directories, ", "), outside)
	}
	return nil
}

// documentChecks is the list a confirmed document's landing runs in place of
// the configured checks and path checks, and whether the project declared one.
// A project that declared none has its document landings run the configured
// checks, exactly as every other run does.
func (a *activeRun) documentChecks() ([]string, bool) {
	if a.state.Document == nil || len(a.pipeline.Config.DocumentChecks) == 0 {
		return nil, false
	}
	return append([]string(nil), a.pipeline.Config.DocumentChecks...), true
}

// documentCheckSet is the digest of the list of checks a confirmed document's
// landing runs over changed, as verify chooses it: the document checks where the
// project declared any, and the configured checks with the path checks the
// change adds where it declared none.
func (a *activeRun) documentCheckSet(changed []string) string {
	commands, declared := a.documentChecks()
	if !declared {
		commands, _ = withPathChecks(a.pipeline.Config.Checks, pathChecksFor(a.worktree.Path, a.pipeline.Config.PathChecks, changed))
	}
	return config.CheckSetDigest(commands)
}

func (a *activeRun) gateDocument(ctx context.Context, changed []string) error {
	candidate := a.state.Document.Candidate
	// Settled before any check runs, by the gate every round passes first.
	if err := documentRouteRefusal(a.pipeline.Config, changed); err != nil {
		return err
	}
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

// A document run, and a run that brings a file in, use the same publication
// recovery as other runs, with the durable run record taking the place of
// backlog bookkeeping.
func (r Reconciler) forDocument(state runstate.State) Reconciler {
	if state.HarnessAuthored() {
		r.Tracker = &documentTracker{item: beads.WorkItem{ID: state.WorkItemID, Title: state.WorkItemTitle, Status: "in_progress"}}
		r.Docket = nil
	}
	return r
}
