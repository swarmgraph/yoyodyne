package orchestrator

// Closing an earlier copy of a document once a newer one is published.
//
// Every document run publishes its own branch and opens its own pull request,
// so each rewrite of a document, and each repeat attempt at the same text, left
// the request before it open. Nothing would ever merge those: a copy the owner
// wrote again is replaced by the newer text, and an attempt that stopped is
// replaced by the attempt after it. They sat on the forge looking like pending
// work until somebody closed them by hand.
//
// So a document run, the moment its own pull request is recorded, closes the
// open request of every earlier run of the same document, with a comment naming
// the request that replaces it, and deletes the branch that request carried.
// Doing it there rather than in a sweep is what makes the newer request the one
// to name: it exists, and it is the one a reader should go to. Requests left
// open before this existed are closed the same way, by the next run of their
// document.
//
// What is closed is narrow on purpose. The other run has to be of the same
// document, onto the same target branch, and over: a run that has not ended is
// still publishing its copy. It has to have
// stopped without landing anything — a run that succeeded has either landed its
// copy or is waiting for a person to merge it, and that wait is a person's to
// end. Its text has to have been written no later than this run's, so a repeat
// attempt at an older text never closes a newer one. And its request has to be
// open as far as its record says, so a request that merged, was closed by the
// development manager's decision (documentdecision.go), or was already closed
// here is never touched again.
//
// A close that fails is not this run's failure: the request stays open, the
// development manager's list of stopped runs still shows it as an earlier copy,
// and the next run of the document tries again.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// EarlierDocumentRuns is the run store closing earlier copies needs: one
// document's runs, chosen by the run index, each run read again before its
// request is closed, and the narrow update that records the close. It is
// satisfied by *runstate.Store.
type EarlierDocumentRuns interface {
	DocumentRuns(id string) ([]runstate.State, error)
	Load(runID string) (runstate.State, error)
	Update(ctx context.Context, runID string, update runstate.RunUpdate) (runstate.State, error)
}

// EarlierDocumentClosure is what became of one earlier copy's pull request.
type EarlierDocumentClosure struct {
	RunID string `json:"run_id"`
	PublicationRetirement
}

// closeEarlierDocumentCopies is the run's half: it closes what this run's
// request replaces, where the run store can list a document's runs and the
// forge access wired can close a request at all.
func (a *activeRun) closeEarlierDocumentCopies(ctx context.Context) {
	if a.state.Document == nil || a.state.PullRequest == nil {
		return
	}
	runs, listed := a.pipeline.Store.(EarlierDocumentRuns)
	forge, closes := a.pipeline.Publisher.(SupersededPublications)
	if !listed || !closes {
		return
	}
	closeEarlierDocumentCopies(ctx, runs, forge, a.pipeline.Worktrees, a.pipeline.Factory, a.state, a.pipeline.clock().Now())
}

// closeEarlierDocumentCopies closes the open pull request of every earlier run
// of the document current publishes, and reports what it did with each.
func closeEarlierDocumentCopies(ctx context.Context, runs EarlierDocumentRuns, forge SupersededPublications, branches SupersededBranches, identities FactoryIdentities, current runstate.State, now time.Time) []EarlierDocumentClosure {
	listed, err := runs.DocumentRuns(current.Document.Candidate.Artifact.ID)
	if err != nil {
		return nil
	}
	var closed []EarlierDocumentClosure
	for _, other := range listed {
		if !earlierDocumentCopy(current, other) {
			continue
		}
		if ctx.Err() != nil {
			return closed
		}
		if closure, acted := closeEarlierDocumentCopy(ctx, runs, forge, branches, identities, current, other.RunID, now); acted {
			closed = append(closed, closure)
		}
	}
	return closed
}

// closeEarlierDocumentCopy closes one earlier run's request. The run is read
// again first, so a request somebody else closed since the listing gets no
// second comment, and the close is recorded with the narrow update that
// records a superseded request, which changes nothing a writer beside this one
// changed meanwhile.
func closeEarlierDocumentCopy(ctx context.Context, runs EarlierDocumentRuns, forge SupersededPublications, branches SupersededBranches, identities FactoryIdentities, current runstate.State, runID string, now time.Time) (EarlierDocumentClosure, bool) {
	state, err := runs.Load(runID)
	if err != nil {
		return EarlierDocumentClosure{RunID: runID, PublicationRetirement: PublicationRetirement{Failure: fmt.Errorf("read run %s to close its earlier copy of the document: %w", runID, err).Error()}}, true
	}
	if !earlierDocumentCopy(current, state) {
		return EarlierDocumentClosure{}, false
	}
	published := *state.PullRequest
	closure := EarlierDocumentClosure{RunID: runID, PublicationRetirement: PublicationRetirement{Number: published.Number, URL: published.URL, Branch: published.Branch}}
	closed, err := forge.Close(ctx, publish.CloseRequest{Head: published.Branch, Number: published.Number, Comment: replacedDocumentComment(state, current),
		Mark: forgeMark(ctx, identities, state.ProductID, current.ProductID)})
	if err != nil {
		closure.Failure = fmt.Errorf("close pull request #%d of run %s: %w", published.Number, runID, err).Error()
		return closure, true
	}
	if closed.Merged {
		// The record said unmerged and the forge says merged, so this copy reached
		// the target after all. Nothing was closed, the branch stays, and the
		// sweep that refreshes publications records the merge on the run.
		closure.Failure = fmt.Sprintf("pull request #%d of run %s is merged at the forge, so it was not closed", published.Number, runID)
		return closure, true
	}
	closure.Closed = closed.Closed
	if _, err := runs.Update(ctx, runID, runstate.PullRequestSuperseded{Number: published.Number, Vehicle: replacedDocumentVehicle(state, current), State: "CLOSED", At: now, Wrote: closeCommentID(closed)}); err != nil {
		closure.Failure = fmt.Errorf("record that pull request #%d of run %s was closed: %w", published.Number, runID, err).Error()
		return closure, true
	}
	if branches == nil {
		return closure, true
	}
	// The branch goes after the close, for the reason retirePublication gives,
	// and only from the exact commit the harness published there.
	worktree := worktreeOf(state)
	worktree.Branch = published.Branch
	if err := branches.DeleteRemoteBranch(ctx, worktree, published.HeadCommit); err != nil {
		closure.Failure = fmt.Errorf("delete the branch pull request #%d carried: %w", published.Number, err).Error()
		return closure, true
	}
	closure.BranchDeleted = true
	return closure, true
}

// earlierDocumentCopy reports other being a run whose open pull request
// current's replaces: see the top of this file for each clause.
func earlierDocumentCopy(current, other runstate.State) bool {
	if current.Document == nil || other.Document == nil || other.RunID == current.RunID {
		return false
	}
	mine, theirs := current.Document.Candidate.Artifact, other.Document.Candidate.Artifact
	if theirs.ID != mine.ID || theirs.Kind != mine.Kind || other.TargetBranch != current.TargetBranch {
		return false
	}
	if !other.Status.Terminal() || other.Status == runstate.StatusSucceeded || other.Integration != nil {
		return false
	}
	if !openDocumentPublication(other) {
		return false
	}
	return !documentWrittenAt(other).After(documentWrittenAt(current))
}

// documentWrittenAt is when the text a run publishes was written: its latest
// revision, or, for a document that records none, when the run started.
func documentWrittenAt(state runstate.State) time.Time {
	revisions := state.Document.Candidate.Artifact.Revisions
	if len(revisions) == 0 {
		return state.StartedAt
	}
	return revisions[len(revisions)-1].At
}

// sameDocumentText reports two runs publishing the same text, which is a
// repeat attempt rather than a newer revision.
func sameDocumentText(a, b runstate.State) bool {
	return a.Document.Candidate.Content == b.Document.Candidate.Content
}

// replacedDocumentVehicle is what the closed run's record keeps as what
// replaced its request.
func replacedDocumentVehicle(earlier, current runstate.State) string {
	what := "a newer revision of the document"
	if sameDocumentText(earlier, current) {
		what = "the same text of the document again"
	}
	return fmt.Sprintf("pull request #%d, opened by run %s to publish %s", current.PullRequest.Number, current.RunID, what)
}

// replacedDocumentComment is what the closed request is left carrying: which
// request replaces it, and why this one will not be merged.
func replacedDocumentComment(earlier, current runstate.State) string {
	d := earlier.Document
	replacement := fmt.Sprintf("pull request #%d", current.PullRequest.Number)
	if url := strings.TrimSpace(current.PullRequest.URL); url != "" {
		replacement += " (" + url + ")"
	}
	why := fmt.Sprintf("a newer revision of %q (%s) is being published by %s, so this earlier one will not be merged.", d.Candidate.Artifact.Title, d.Candidate.Artifact.ID, replacement)
	if sameDocumentText(earlier, current) {
		why = fmt.Sprintf("the same text of %q (%s) is being published again by %s, so this earlier attempt will not be merged.", d.Candidate.Artifact.Title, d.Candidate.Artifact.ID, replacement)
	}
	return strings.Join([]string{
		"Yoyodyne closed this pull request: " + why,
		"",
		"- This publication: run `" + earlier.RunID + "`, which stopped without landing the document",
		"- Replaced by: run `" + current.RunID + "`, " + replacement,
		"",
		"The branch this request carries is being deleted with it.",
	}, "\n")
}
