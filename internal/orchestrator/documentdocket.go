package orchestrator

// Putting stopped document publications in front of the development manager.
//
// A document run is its owning conversation's while that conversation can still
// do something with it: retry it after a stop that judged nothing, or hand it
// back to its owner to revise. Two kinds of document run are nobody's by that
// account. One is the last of a handoff's attempts, every one of which stopped
// without judging the document, which PublishDocument hands to her itself. The
// other is a run that left its pull request open on the forge and that its
// conversation is not going to land: an earlier copy of a document written again
// since, an attempt a later attempt replaced, or a copy of a document that has
// landed since by another run. Nothing closed those, so the forge's list of open
// publications filled with copies nobody would ever merge.
//
// So the docket takes in both. A document run that is still its conversation's
// latest word on the document is taken in too once it has stood stopped for the
// configured stuck-merge age, because a conversation that has not offered it
// again by then is not going to: that is the same age past which an unmerged
// publication is somebody's to decide. Each is entered as a stopped run naming
// the document and its pull request, and the two decisions that answer it are
// retrying it and retiring it (documentdecision.go).

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// documentHandoff names one handoff: its conversation and its write.
type documentHandoff struct{ conversation, write string }

// documentInConversation names one document in one conversation.
type documentInConversation struct{ conversation, artifact string }

// documentHandoffEnds is the last run of each document handoff the recorded runs
// hold, among the runs keep admits: the one with the highest attempt.
func documentHandoffEnds(recorded []runstate.State, keep func(runstate.State) bool) map[documentHandoff]runstate.State {
	last := map[documentHandoff]runstate.State{}
	for _, state := range recorded {
		if state.Document == nil || !keep(state) {
			continue
		}
		attempt := state.DocumentAttempt()
		if attempt < 0 {
			continue
		}
		key := documentHandoff{state.Document.ConversationID, state.Document.WriteID}
		if seen, ok := last[key]; !ok || seen.DocumentAttempt() < attempt {
			last[key] = state
		}
	}
	return last
}

// latestDocumentHandoffs is, of those, the latest handoff of each document in
// each conversation: the latest turn, and of one turn's handoffs the greatest
// write, which is how a retry's handoff comes after the one it retries.
func latestDocumentHandoffs(ends map[documentHandoff]runstate.State) map[documentInConversation]runstate.State {
	latest := map[documentInConversation]runstate.State{}
	for _, state := range ends {
		key := documentInConversation{state.Document.ConversationID, state.Document.Candidate.Artifact.ID}
		if seen, ok := latest[key]; !ok || state.Document.Turn > seen.Document.Turn || (state.Document.Turn == seen.Document.Turn && state.Document.WriteID > seen.Document.WriteID) {
			latest[key] = state
		}
	}
	return latest
}

// documentCopies is what every recorded document run says about the others: which
// runs are still their conversation's latest word on their document, and, for
// each document, its copies that stopped and the runs that landed it.
type documentCopies struct {
	latest  map[string]bool
	stopped map[string][]runstate.State
	landed  map[string][]runstate.State
}

func documentCopiesOf(recorded []runstate.State) documentCopies {
	copies := documentCopies{latest: map[string]bool{}, stopped: map[string][]runstate.State{}, landed: map[string][]runstate.State{}}
	every := func(runstate.State) bool { return true }
	for _, state := range latestDocumentHandoffs(documentHandoffEnds(recorded, every)) {
		copies.latest[state.RunID] = true
	}
	for _, state := range recorded {
		if state.Document == nil {
			continue
		}
		id := state.Document.Candidate.Artifact.ID
		switch {
		case state.Status == runstate.StatusSucceeded && state.Integration != nil:
			copies.landed[id] = append(copies.landed[id], state)
		case state.DocumentDecidable():
			copies.stopped[id] = append(copies.stopped[id], state)
		}
	}
	return copies
}

// docketable reports a document run that is the development manager's to
// decide: see the top of this file.
func (c documentCopies) docketable(state runstate.State, now time.Time, stuckMergeAge time.Duration) bool {
	if !state.DocumentDecidable() {
		return false
	}
	if documentHandedOver(state) {
		return true
	}
	if !openDocumentPublication(state) {
		return false
	}
	if !c.latest[state.RunID] || c.landedSince(state) != nil {
		return true
	}
	return stuckMergeAge > 0 && now.Sub(stoppedAt(state)) >= stuckMergeAge
}

// openDocumentPublication reports a document run whose pull request is still
// open as far as its record says.
func openDocumentPublication(state runstate.State) bool {
	published := state.PullRequest
	return published != nil && published.Number > 0 && !published.Merged && stillOpen(*published) &&
		published.Superseded == "" && published.HandedBack == nil
}

// landedSince is the latest run that landed the same document after this one
// started, or nil where none has.
func (c documentCopies) landedSince(state runstate.State) *runstate.State {
	var by *runstate.State
	for _, landed := range c.landed[state.Document.Candidate.Artifact.ID] {
		if landed.RunID == state.RunID || !landedAfter(landed, state.StartedAt) {
			continue
		}
		if by == nil || laterLanding(landed, *by) {
			landed := landed
			by = &landed
		}
	}
	return by
}

// newerCopy is the newest stopped copy of the same document, where it is not
// this run.
func (c documentCopies) newerCopy(state runstate.State) *runstate.State {
	newest := state
	for _, other := range c.stopped[state.Document.Candidate.Artifact.ID] {
		if other.StartedAt.After(newest.StartedAt) || (other.StartedAt.Equal(newest.StartedAt) && other.RunID > newest.RunID) {
			newest = other
		}
	}
	if newest.RunID == state.RunID {
		return nil
	}
	return &newest
}

// document is what a docket entry says about the document a run was landing,
// with what the other copies say joined on.
func (c documentCopies) document(state runstate.State) *triage.Document {
	d := state.Document
	document := &triage.Document{
		ID:      d.Candidate.Artifact.ID,
		Title:   singleLine(d.Candidate.Artifact.Title, triage.MaxMessageBytes),
		Path:    d.Candidate.Artifact.Path,
		Origin:  singleLine(d.Origin(), triage.MaxMessageBytes),
		Stopped: oneline.Fold(documentStopSays(state), triage.MaxMessageBytes),
	}
	if state.PullRequest != nil {
		document.PullRequest, document.PullRequestURL = state.PullRequest.Number, state.PullRequest.URL
	}
	if landed := c.landedSince(state); landed != nil {
		document.LandedBy = "run " + landed.RunID
		if landed.PullRequest != nil && landed.PullRequest.Number > 0 {
			document.LandedBy += fmt.Sprintf(" (pull request #%d)", landed.PullRequest.Number)
		}
	}
	if newer := c.newerCopy(state); newer != nil {
		document.NewerCopy = "run " + newer.RunID
		if newer.PullRequest != nil && newer.PullRequest.Number > 0 {
			document.NewerCopy += fmt.Sprintf(" (pull request #%d)", newer.PullRequest.Number)
		}
	}
	return document
}

// documentEntry is the stopped-run entry of a document run the development
// manager decides about.
func (d Docketer) documentEntry(state runstate.State, now time.Time, copies documentCopies) (triage.Entry, error) {
	entry, err := d.stoppedRunEntryCarrying(state, now, d.look(state), "")
	if err != nil {
		return triage.Entry{}, err
	}
	entry.Document = copies.document(state)
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket the stopped document run %s: %w", state.RunID, err)
	}
	return entry, nil
}

// joinDocuments puts what the recorded runs now say about each document onto
// the entries of document runs, including those docketed before entries named
// their document: which copy is newest, and whether one has landed since.
func joinDocuments(entries []triage.Entry, byID map[string]runstate.State, copies documentCopies) {
	for index := range entries {
		joinDocument(&entries[index], byID, copies)
		for earlier := range entries[index].Earlier {
			joinDocument(&entries[index].Earlier[earlier], byID, copies)
		}
	}
}

func joinDocument(entry *triage.Entry, byID map[string]runstate.State, copies documentCopies) {
	state, known := byID[entry.RunID]
	if !known || state.Document == nil {
		return
	}
	entry.Document = copies.document(state)
}
