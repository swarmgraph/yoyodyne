package runstate

// What a written document is tied to, kept on its record.
//
// A document that waits to be published can disagree with a work item admitted
// while it waits (docs/designs/unpublished-designs-and-admitted-work.md). Finding
// that takes only what the record says: when the document was written, which
// work items it names, and which documents it revises. The first is
// PendingWrite.WrittenAt. The other two are kept on the record when the document
// is written (Names and Revises), and a record written before they were kept is
// given them from what it already holds when it is read back
// (PendingWrite.WithRelations), so no pass has to tell the two apart.

import (
	"regexp"
	"slices"
	"strings"
)

// DocumentRef is one document a written document revises: its identifier, and
// the repository-relative file it is read from where the record knows it. The
// path is empty on a record whose file was never recorded and which holds no
// copy of the document to take it from.
type DocumentRef struct {
	ID   string `json:"id"`
	Path string `json:"path,omitempty"`
}

// MaxNamedItems bounds the work items one document's record keeps as named.
const MaxNamedItems = 40

// maxNamedItemBytes bounds one named work item identifier.
const maxNamedItemBytes = 128

// itemShape finds text shaped like a tracker identifier: a prefix, a hyphen, and
// a suffix that is dotted digits after a short word or a hash. It is deliberately
// loose, and what it finds are candidates: whoever compares them with the items
// the tracker holds drops what is not one, and an identifier missed here would
// be a document that disagrees with an item and is never listed.
var itemShape = regexp.MustCompile(`\b[a-z][a-z0-9]*-[a-z0-9]+(?:\.[0-9]+)*`)

// itemSuffix reports whether what follows the hyphen looks like a tracker's
// suffix rather than a hyphenated word: it holds a digit, or is at most four
// letters long, as the tracker's hash suffixes are.
func itemSuffix(suffix string) bool {
	if len(suffix) <= 4 {
		return true
	}
	return strings.ContainsAny(suffix, "0123456789")
}

// NamedItems is every work item identifier the text names, in order of first
// appearance and without repeats.
func NamedItems(text string) []string {
	var items []string
	for _, found := range itemShape.FindAllString(text, -1) {
		_, suffix, _ := strings.Cut(found, "-")
		// The suffix up to its first dot is the part that has to look like one.
		head, _, _ := strings.Cut(suffix, ".")
		if !itemSuffix(head) || slices.Contains(items, found) {
			continue
		}
		items = append(items, found)
	}
	return items
}

// namedItems is the work items this document names: the items it answers and the
// one its hold waits on, then any named by their identifier in its reason or
// body.
func (w PendingWrite) namedItems() []string {
	var named []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > maxNamedItemBytes || len(named) >= MaxNamedItems || slices.Contains(named, id) {
			return
		}
		named = append(named, id)
	}
	for _, id := range w.Answers {
		add(id)
	}
	if w.Hold != nil {
		add(w.Hold.UntilClosed)
	}
	for _, id := range NamedItems(w.Reason) {
		add(id)
	}
	for _, id := range NamedItems(w.Body) {
		add(id)
	}
	return named
}

// revisedDocuments is the document a revision replaces, with the file it is read
// from where the record holds one: the copy confirmed for publication names it.
// A creation revises nothing.
func (w PendingWrite) revisedDocuments() []DocumentRef {
	id := strings.TrimSpace(w.Artifact)
	if w.Action != "revise" || id == "" {
		return nil
	}
	ref := DocumentRef{ID: id}
	if w.Publication != nil && w.Publication.Candidate.Artifact.ID == id {
		ref.Path = w.Publication.Candidate.Artifact.Path
	}
	return []DocumentRef{ref}
}

// WithRelations is the record with what it names and what it revises filled in
// from what it holds, where it kept neither. A path already kept is never
// replaced, and one the record did not know is filled from the confirmed copy
// where there is one. It is what a record written before these were kept reads
// back as, so a document is described the same whenever it was written.
func (w PendingWrite) WithRelations() PendingWrite {
	if len(w.Names) == 0 {
		w.Names = w.namedItems()
	}
	if len(w.Revises) == 0 {
		w.Revises = w.revisedDocuments()
		return w
	}
	revises := slices.Clone(w.Revises)
	for i := range revises {
		if revises[i].Path == "" {
			for _, found := range w.revisedDocuments() {
				if found.ID == revises[i].ID {
					revises[i].Path = found.Path
				}
			}
		}
	}
	w.Revises = revises
	return w
}

// RecordRevisedPath is the record with the file the document it revises is read
// from, for a writer that knows it. It changes nothing on a creation.
func (w PendingWrite) RecordRevisedPath(path string) PendingWrite {
	w = w.WithRelations()
	if len(w.Revises) == 1 && w.Revises[0].Path == "" {
		w.Revises = []DocumentRef{{ID: w.Revises[0].ID, Path: strings.TrimSpace(path)}}
	}
	return w
}
