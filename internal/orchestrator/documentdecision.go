package orchestrator

// Carrying out the development manager's decision about a stopped document
// publication.
//
// A document run has no work item, so the decisions recorded on an item's
// triage record do not reach it; the two that answer it are recorded on the run
// itself (runstate.DocumentDecision). Retrying puts the same confirmed text into
// a handoff of its own and writes it down as waiting for a document landing, so
// the scheduler starts its run as one frees, under the same intake hold, checks,
// and independent review as any document landing; the old run's pull request is
// closed, since the fresh run opens one of its own. Retiring closes the pull
// request and lands nothing. Either way the closed request carries a comment
// saying who decided what and why, and the run's record says the same.
//
// Starting the fresh run is the scheduler's, not this file's: nothing here
// invokes a role or reserves a run.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DocumentDecisionRuns is the run store a document decision reads and writes:
// the stopped run, under its lease, and the wait its retry is started from. It
// is satisfied by *runstate.Store.
type DocumentDecisionRuns interface {
	Load(runID string) (runstate.State, error)
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	Save(state runstate.State) error
	WaitForSlot(ctx context.Context, document runstate.DocumentPublication, attempt int, at time.Time) (runstate.DocumentWait, bool, error)
}

// DocumentDecider carries out a retry or a retirement of a stopped document
// run. Publications closes the run's pull request and is required wherever the
// run left one open; Branches deletes the branch that request carried, and a
// decider without it leaves the branch for the convergence sweep or a person.
type DocumentDecider struct {
	Runs         DocumentDecisionRuns
	Publications SupersededPublications
	Branches     SupersededBranches
	// Copies reads the document as the run's target branch now holds it, which
	// is what a retry is refused on where that copy is a later confirmed
	// revision. A decider without it does not ask.
	Copies DocumentCopies
	Clock  execution.Clock
}

// DocumentCopies reads one file as a commit or branch holds it. It is
// satisfied by gitworktree.Manager.
type DocumentCopies interface {
	FileAtCommit(ctx context.Context, commit, path string, maxBytes int64) (gitworktree.FileAt, error)
}

// DocumentRun reports whether the named run is a document publication, which
// is what retry and retire answer and every other triage decision does not.
func (d DocumentDecider) DocumentRun(_ context.Context, runID string) (bool, error) {
	state, err := d.Runs.Load(runID)
	if err != nil {
		return false, err
	}
	return state.Document != nil, nil
}

// RetryDocument lands a stopped document run's confirmed text again through a
// fresh run, and reports what it did in a sentence for the role that decided.
func (d DocumentDecider) RetryDocument(ctx context.Context, runID string, decision runstate.DocumentDecision) (string, error) {
	decision.Decision = runstate.DocumentDecisionRetry
	state, lease, said, err := d.adopt(ctx, runID, decision.Decision)
	if err != nil || said != "" {
		return said, err
	}
	defer lease.Release()
	if refusal, err := d.laterRevision(ctx, state); err != nil {
		return "", fmt.Errorf("read %s as %s holds it, which a retry is checked against, so nothing was recorded: %w", state.Document.Candidate.Artifact.Path, state.TargetBranch, err)
	} else if refusal != "" {
		return "", errors.New(refusal)
	}
	decision.DecidedAt = d.now()
	fresh := state.Document.RetryOf(state.RunID, runstate.DocumentRetried{FromRun: state.RunID, Reason: decision.Reason, DecidedBy: decision.DecidedBy, DecidedAt: decision.DecidedAt})
	if err := fresh.Validate(); err != nil {
		return "", fmt.Errorf("the document run %s carries cannot be handed to a fresh run: %w", state.RunID, err)
	}
	decision.RetriedAs = fresh.RunID()
	// The wait is written before the decision, so a decision on the record always
	// has a fresh run behind it; one recorded again after a save that failed names
	// the same handoff, which is already waiting or already running.
	if _, _, err := d.Runs.WaitForSlot(ctx, fresh, 0, decision.DecidedAt); err != nil {
		return "", fmt.Errorf("put the retry of run %s where the scheduler starts it: %w", state.RunID, err)
	}
	d.closeDecided(ctx, &state, &decision, retriedComment(state, decision))
	if err := d.record(state, decision); err != nil {
		return "", err
	}
	return fmt.Sprintf("document %s (%q) waits for a document landing as handoff %s, first published by run %s, which the scheduler starts as one frees; %s",
		state.Document.Candidate.Artifact.ID, state.Document.Candidate.Artifact.Title, fresh.WriteID, decision.RetriedAs, publicationClause(state, decision)), nil
}

// laterRevision is why a retry of the stopped run is refused, or empty where it
// is not: the run's target branch already carries a revision of the same
// document that was confirmed and written after the text the run carries. A
// fresh run of that text would find the same changed copy and stop the same
// way, so the retry is refused before any run is started, naming the revision
// that stands. A copy that is absent, unreadable as a document, or not a later
// confirmed revision refuses nothing; a copy that cannot be read at all is an
// error, so no retry is recorded on an answer nobody got.
func (d DocumentDecider) laterRevision(ctx context.Context, state runstate.State) (string, error) {
	candidate := state.Document.Candidate
	if d.Copies == nil || strings.TrimSpace(state.TargetBranch) == "" || len(candidate.Artifact.Revisions) == 0 {
		return "", nil
	}
	copied, err := d.Copies.FileAtCommit(ctx, state.TargetBranch, candidate.Artifact.Path, maxDocumentHistoryBytes)
	if errors.Is(err, gitworktree.ErrNotAtCommit) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if copied.Content == nil {
		return "", fmt.Errorf("the copy is %d bytes, more than the %d read", copied.Size, maxDocumentHistoryBytes)
	}
	if artifact.DocumentIdentity(copied.Content) == artifact.DocumentIdentity([]byte(candidate.Content)) {
		return "", nil
	}
	standing, err := artifact.ParseDocument(copied.Content)
	if err != nil || standing.ID != candidate.Artifact.ID {
		return "", nil
	}
	approval, approved := standing.LatestApproval()
	if !approved || approval.Revision < 0 || approval.Revision >= len(standing.Revisions) {
		return "", nil
	}
	confirmed := standing.Revisions[approval.Revision]
	written := candidate.Artifact.Revisions[len(candidate.Artifact.Revisions)-1]
	if !confirmed.At.After(written.At) {
		return "", nil
	}
	when := func(at time.Time) string { return at.Local().Format("2006-01-02 15:04 MST") }
	return fmt.Sprintf("run %s is not retried: %s already carries a later confirmed revision of %s (%s), revision %d, written by the %s at %s (%s) and confirmed by the %s at %s, after the text this run carries was written at %s. A fresh run of that text would stop on the same changed copy, so nothing was recorded; retiring the run is the decision that fits",
		state.RunID, state.TargetBranch, candidate.Artifact.Title, candidate.Artifact.ID, approval.Revision, confirmed.By.Title(), when(confirmed.At), oneline.Fold(confirmed.Reason, 300), approval.By, when(approval.At), when(written.At)), nil
}

// RetireDocument closes a stopped document run's pull request and records why.
// A request that cannot be closed records nothing, so the run stays on the
// docket to be decided again.
func (d DocumentDecider) RetireDocument(ctx context.Context, runID string, decision runstate.DocumentDecision) (string, error) {
	decision.Decision = runstate.DocumentDecisionRetire
	state, lease, said, err := d.adopt(ctx, runID, decision.Decision)
	if err != nil || said != "" {
		return said, err
	}
	defer lease.Release()
	decision.DecidedAt = d.now()
	d.closeDecided(ctx, &state, &decision, retiredComment(state, decision))
	if openDocumentPublication(state) {
		return "", fmt.Errorf("pull request #%d of run %s was not closed, so the retirement was not recorded: %s", state.PullRequest.Number, state.RunID, decision.Problem)
	}
	if err := d.record(state, decision); err != nil {
		return "", err
	}
	return fmt.Sprintf("document %s (%q) is not landed by run %s; %s", state.Document.Candidate.Artifact.ID, state.Document.Candidate.Artifact.Title, state.RunID, publicationClause(state, decision)), nil
}

// adopt takes the stopped run's lease once it is a document run the decision
// may be made on. A run the same decision already settled is answered with a
// sentence and no lease, since recording it twice means the same thing.
func (d DocumentDecider) adopt(ctx context.Context, runID, decision string) (runstate.State, *runstate.Lease, string, error) {
	if d.Runs == nil {
		return runstate.State{}, nil, "", errors.New("no run store is wired to carry out a decision about a document publication")
	}
	state, lease, err := d.Runs.AdoptRun(ctx, runID)
	if errors.Is(err, runstate.ErrRunHeld) {
		return runstate.State{}, nil, "", fmt.Errorf("run %s is being worked on by another process now, so it has not stopped; nothing was recorded", runID)
	}
	if err != nil {
		return runstate.State{}, nil, "", err
	}
	refuse := func(format string, args ...any) (runstate.State, *runstate.Lease, string, error) {
		lease.Release()
		return runstate.State{}, nil, "", fmt.Errorf(format, args...)
	}
	switch {
	case state.Document == nil:
		return refuse("run %s is a run of work item %s, not a document publication; %q and %q answer only a document publication, which has no work item", runID, state.WorkItemID, runstate.DocumentDecisionRetry, runstate.DocumentDecisionRetire)
	case state.DocumentDecision != nil && state.DocumentDecision.Decision == decision:
		lease.Release()
		return runstate.State{}, nil, fmt.Sprintf("run %s was already decided %q at %s, so nothing more was done", runID, decision, state.DocumentDecision.DecidedAt.Local().Format("2006-01-02 15:04 MST")), nil
	case state.DocumentDecision != nil:
		return refuse("run %s was already decided %q at %s, which stands; nothing was recorded", runID, state.DocumentDecision.Decision, state.DocumentDecision.DecidedAt.Local().Format("2006-01-02 15:04 MST"))
	case !state.Status.Terminal():
		return refuse("run %s has not stopped (it is %s), so it is not a stopped publication to decide; nothing was recorded", runID, state.Status)
	case state.Status == runstate.StatusSucceeded:
		return refuse("run %s succeeded, so its document is landed or waiting for its merge; nothing was recorded", runID)
	}
	return state, lease, "", nil
}

// closeDecided closes the run's pull request where it is still open, with the
// comment naming the decision, and deletes the branch it carried. What it could
// not do is written onto the decision rather than failing it: a retry has
// already started its fresh run, and a retirement checks for itself whether the
// request closed.
func (d DocumentDecider) closeDecided(ctx context.Context, state *runstate.State, decision *runstate.DocumentDecision, comment string) {
	if !openDocumentPublication(*state) {
		return
	}
	published := *state.PullRequest
	if d.Publications == nil {
		decision.Problem = fmt.Sprintf("nothing is wired to close pull request #%d", published.Number)
		return
	}
	closure, err := d.Publications.Close(ctx, publish.CloseRequest{Head: published.Branch, Number: published.Number, Comment: comment})
	if err != nil {
		decision.Problem = fmt.Sprintf("closing pull request #%d failed: %v", published.Number, err)
		return
	}
	if closure.Merged {
		decision.Problem = fmt.Sprintf("pull request #%d is merged at the forge, so it was not closed", published.Number)
		published.Merged = true
		state.PullRequest = &published
		return
	}
	published.State = "CLOSED"
	state.PullRequest = &published
	decision.PullRequestClosed = true
	if d.Branches == nil {
		return
	}
	worktree := worktreeOf(*state)
	worktree.Branch = published.Branch
	if err := d.Branches.DeleteRemoteBranch(ctx, worktree, published.HeadCommit); err != nil {
		decision.Problem = fmt.Sprintf("the branch pull request #%d carried was not deleted: %v", published.Number, err)
	}
}

func (d DocumentDecider) record(state runstate.State, decision runstate.DocumentDecision) error {
	state.DocumentDecision = &decision
	state.UpdatedAt = d.now()
	if err := d.Runs.Save(state); err != nil {
		return fmt.Errorf("record the %q decision on run %s: %w", decision.Decision, state.RunID, err)
	}
	return nil
}

func (d DocumentDecider) now() time.Time {
	if d.Clock == nil {
		return time.Now().UTC()
	}
	return d.Clock.Now().UTC()
}

// publicationClause says what the decision did with the run's pull request.
func publicationClause(state runstate.State, decision runstate.DocumentDecision) string {
	switch {
	case decision.PullRequestClosed && decision.Problem == "":
		return fmt.Sprintf("its pull request #%d is closed with the reason, and the branch it carried is deleted", state.PullRequest.Number)
	case decision.PullRequestClosed:
		return fmt.Sprintf("its pull request #%d is closed with the reason; %s", state.PullRequest.Number, decision.Problem)
	case decision.Problem != "":
		return decision.Problem
	default:
		return "it left no pull request open"
	}
}

func retiredComment(state runstate.State, decision runstate.DocumentDecision) string {
	d := state.Document
	return strings.Join([]string{
		fmt.Sprintf("Yoyodyne closed this pull request: %s retired this publication of %q (%s), so it will not be merged.", decision.DecidedBy, d.Candidate.Artifact.Title, d.Candidate.Artifact.ID),
		"",
		"Reason: " + strings.TrimSpace(decision.Reason),
		"",
		"- This publication: run `" + state.RunID + "`, which stopped without landing the document",
		"- The document was " + d.Origin(),
	}, "\n")
}

func retriedComment(state runstate.State, decision runstate.DocumentDecision) string {
	d := state.Document
	return strings.Join([]string{
		fmt.Sprintf("Yoyodyne closed this pull request: %s decided to land %q (%s) again through a fresh run, which opens a pull request of its own.", decision.DecidedBy, d.Candidate.Artifact.Title, d.Candidate.Artifact.ID),
		"",
		"Reason: " + strings.TrimSpace(decision.Reason),
		"",
		"- This publication: run `" + state.RunID + "`, which stopped without landing the document",
		"- The fresh landing: first run `" + decision.RetriedAs + "`, with the same confirmed text",
	}, "\n")
}
