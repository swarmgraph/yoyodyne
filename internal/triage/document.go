package triage

import (
	"errors"
	"fmt"
	"strings"
)

// The two decisions that answer a stopped document publication. They are the
// durable record's words as well (runstate.DocumentDecisionRetry and
// runstate.DocumentDecisionRetire are these), declared here because the entry
// names them to the development manager and this package sits beneath the run
// state.
const (
	DocumentRetry  = "retry"
	DocumentRetire = "retire"
)

// Document is the confirmed document a stopped document publication was
// landing, on the stopped-run entry of that publication's run. Such a run has no
// work item behind it, so none of the decisions recorded on an item can be taken
// on it; what answers it is retrying the same text through a fresh run, or
// retiring it, and the entry says so in place of the item's budgets.
//
// NewerCopy and LandedBy are not written with the entry. They are what the
// harness's records say about the other copies of the same document when the
// docket is built for somebody to read, because the newest copy is the one
// worth retrying and a copy that landed since makes every other one moot.
type Document struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
	// Origin is where the confirmed text came from, in the words a run's record
	// says it: the owning role, its conversation, and the turn.
	Origin string `json:"origin"`
	// Stopped is why the run ended, in one line.
	Stopped string `json:"stopped"`
	// PullRequest is the request the run left on the forge, and zero where it
	// opened none.
	PullRequest    int    `json:"pull_request,omitempty"`
	PullRequestURL string `json:"pull_request_url,omitempty"`
	// NewerCopy names a later stopped run of the same document, and is empty
	// where this run is the newest.
	NewerCopy string `json:"newer_copy,omitempty"`
	// LandedBy names a run that has landed the same document since this one
	// started, and is empty where none has.
	LandedBy string `json:"landed_by,omitempty"`
}

func (d Document) validate() error {
	var problems []error
	if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.Title) == "" || strings.TrimSpace(d.Path) == "" {
		problems = append(problems, errors.New("document: id, title, and path are required"))
	}
	for name, text := range map[string]string{"origin": d.Origin, "stopped": d.Stopped, "newer_copy": d.NewerCopy, "landed_by": d.LandedBy, "title": d.Title} {
		if len(text) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("document %s is %d bytes, limit is %d", name, len(text), MaxMessageBytes))
		}
	}
	if d.PullRequest < 0 {
		problems = append(problems, errors.New("document pull_request cannot be negative"))
	}
	return errors.Join(problems...)
}

// renderDocument says which document the run was landing, what it left open,
// and the two decisions that answer it.
func (e Entry) renderDocument() string {
	d := e.Document
	if d == nil {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Document publication, with no work item behind it: %q (%s, at %s), %s\n", d.Title, d.ID, d.Path, d.Origin)
	if d.PullRequest > 0 {
		fmt.Fprintf(&rendered, "      Its pull request #%d (%s) was opened by this run and has not merged.\n", d.PullRequest, d.PullRequestURL)
	} else {
		rendered.WriteString("      It opened no pull request.\n")
	}
	if d.Stopped != "" {
		rendered.WriteString(indented("Why it stopped", d.Stopped))
	}
	switch {
	case d.LandedBy != "":
		fmt.Fprintf(&rendered, "      This document has since landed by %s, so this copy is not needed.\n", d.LandedBy)
	case d.NewerCopy != "":
		fmt.Fprintf(&rendered, "      A newer stopped copy of this document is %s.\n", d.NewerCopy)
	default:
		rendered.WriteString("      This is the newest stopped copy of this document.\n")
	}
	return rendered.String()
}

// documentNextMover is the next-mover line of a document publication's entry.
func (e Entry) documentNextMover() string {
	return fmt.Sprintf("      Next mover: you — decide it with the triage decision %q, which lands the same confirmed text through a fresh run and closes this run's pull request, or %q, which closes the pull request and records why; name run %s and no work item id.\n",
		DocumentRetry, DocumentRetire, e.RunID)
}
