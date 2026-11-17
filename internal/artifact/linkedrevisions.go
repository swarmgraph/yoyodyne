package artifact

// Which documents a confirmed document answers to have changed since it was
// confirmed.
//
// A confirmed document waits for its publishing run, and what it says was
// checked against the documents it links to as they stood when it was
// confirmed. Where one of those was revised while it waited, the text is
// already known to be out of date, and a run and a review spent on it only
// find that out. Nothing here refuses a document: it names what moved, and the
// caller sends the document back to its author.

import (
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// LinkedRevision is one document a confirmed document links to or supports,
// revised after the confirmation.
type LinkedRevision struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Title     string    `json:"title,omitempty"`
	RevisedAt time.Time `json:"revised_at"`
}

// ConfirmedAt is when the document was confirmed: its latest approval, or its
// latest revision where it carries no approval.
func (a Artifact) ConfirmedAt() time.Time {
	if approval, ok := a.LatestApproval(); ok {
		return approval.At
	}
	if len(a.Revisions) > 0 {
		return a.Revisions[len(a.Revisions)-1].At
	}
	return time.Time{}
}

// markdownLink finds the target of an inline Markdown link.
var markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// LinkedRevisionsSince lists, ordered by identity, the documents the candidate
// names as ones it supports or links to in its body that were last revised
// after the candidate was confirmed. A link is to a document when it is a
// relative path to a Markdown file the repository's artifact set holds.
func (s Store) LinkedRevisionsSince(candidate ConfirmedDocument) ([]LinkedRevision, error) {
	set, err := s.Load()
	if err != nil {
		return nil, err
	}
	linked := map[string]bool{}
	for _, id := range candidate.Artifact.Supports {
		linked[strings.TrimSpace(id)] = true
	}
	byPath := map[string]string{}
	for _, other := range set.Artifacts {
		byPath[other.Path] = other.ID
	}
	_, body, _ := strings.Cut(strings.TrimPrefix(candidate.Content, "---\n"), "\n---\n")
	for _, match := range markdownLink.FindAllStringSubmatch(body, -1) {
		target, _, _ := strings.Cut(match[1], "#")
		if !strings.HasSuffix(target, ".md") || strings.Contains(target, "://") {
			continue
		}
		if id, ok := byPath[path.Join(path.Dir(candidate.Artifact.Path), target)]; ok {
			linked[id] = true
		}
	}
	confirmed := candidate.Artifact.ConfirmedAt()
	var revised []LinkedRevision
	for _, other := range set.Artifacts {
		if other.ID == candidate.Artifact.ID || !linked[other.ID] || len(other.Revisions) == 0 {
			continue
		}
		if last := other.Revisions[len(other.Revisions)-1].At; last.After(confirmed) {
			revised = append(revised, LinkedRevision{ID: other.ID, Path: other.Path, Title: other.Title, RevisedAt: last})
		}
	}
	sort.Slice(revised, func(i, j int) bool { return revised[i].ID < revised[j].ID })
	return revised, nil
}
