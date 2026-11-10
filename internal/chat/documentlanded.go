package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
)

// DocumentLandings reads the repository a project's governed documents are kept in
// (its companion intent repository where it has one, its own repository otherwise).
type DocumentLandings interface {
	// OnTarget reads one repository-relative path as that repository's target branch holds it now.
	OnTarget(ctx context.Context, path string) (LandedFile, error)
	// AtCommit reads one path as the named commit of that repository holds it; an unknown commit is an error.
	AtCommit(ctx context.Context, commit, path string) (LandedFile, error)
}

// LandedFile is one path as a commit of that repository holds it.
type LandedFile struct {
	// Repository is how a person names the repository read, for example "the
	// companion intent repository at /x".
	Repository string
	// Branch is the target branch read; empty for AtCommit.
	Branch string
	// Commit is the full commit id read.
	Commit string
	// Found is false where the commit holds no regular file at the path.
	Found   bool
	Content []byte
}

// Ways a document is recorded as landed, in the event and in what the role is told.
const (
	landedAsConfirmed   = "as confirmed"
	landedInAnotherForm = "in another form"
)

// settleLandedDocuments gives back the place of every confirmed document that
// is already in the repository its documents are kept in, however it got there.
// A publisher that cannot land a document (a project with a companion intent
// repository) holds it for good, and a person may have landed it by hand; the
// held copy would otherwise refuse the operator's decision, the role's next
// revision of the document, and every new document once enough are held.
//
// A document is settled only when the target branch holds a file of the same
// identity whose text is not the text the confirmed document revises (the candidate
// keeps that text's identity, artifact.DocumentIdentity): a file still equal to
// it, or no file, is a revision that has not landed. A
// read that fails is told to the owning role once and leaves the document held.
// With no DocumentLandings configured nothing is checked.
func (s *Session) settleLandedDocuments(ctx context.Context) error {
	landings := s.options.DocumentLandings
	if landings == nil {
		return nil
	}
	changed := false
	for _, record := range s.writes {
		if record.decided || record.pending.Publication == nil {
			continue
		}
		candidate := record.pending.Publication.Candidate
		documentPath := candidate.Artifact.Path
		file, err := landings.OnTarget(ctx, documentPath)
		if err != nil {
			failure := err.Error()
			if failure != record.landingFailure {
				record.landingFailure = failure
				if err := s.carryResults(fmt.Sprintf("Document %s (%s) is confirmed and not yet published, and the harness could not read %s to see whether it landed by another route: %v. It is kept and looked for again at the next message.\n", record.pending.ID, candidate.Artifact.ID, documentPath, failure)); err != nil {
					return err
				}
			}
			continue
		}
		if !file.Found {
			continue
		}
		found := strings.TrimSpace(string(file.Content))
		var how string
		switch {
		case found == strings.TrimSpace(candidate.Content):
			how = landedAsConfirmed
		case candidate.Before != "" && artifact.DocumentIdentity(file.Content) == candidate.Before:
			continue
		case sameDocument(file.Content, candidate.Artifact.ID):
			how = landedInAnotherForm
		default:
			continue
		}
		if err := s.settleLanded(record, file, how); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return s.record()
}

// sameDocument reports whether a file is the document with this id: it names
// that id in its frontmatter, or it does not parse, in which case being at the
// document's path is its identity.
func sameDocument(content []byte, id string) bool {
	parsed, err := artifact.ParseDocument(content)
	if err != nil {
		return true
	}
	return parsed.ID == id
}

// settleLanded records a confirmed document as landed and tells its owner.
func (s *Session) settleLanded(record *writeRecord, file LandedFile, how string) error {
	candidate := record.pending.Publication.Candidate
	if err := s.emit(execution.EventDocumentLanded, map[string]any{
		"write_id":   record.pending.ID,
		"artifact":   candidate.Artifact.ID,
		"path":       candidate.Artifact.Path,
		"repository": file.Repository,
		"branch":     file.Branch,
		"commit":     file.Commit,
		"how":        how,
		"by":         "harness",
	}); err != nil {
		return fmt.Errorf("record document landing: %w", err)
	}
	record.decided = true
	var told string
	if how == landedAsConfirmed {
		told = fmt.Sprintf("Document %s (%s) is on %s of %s at commit %s, as confirmed, so it is recorded as landed and its waiting place is given back. A later change to it is written as a revision of what landed.\n",
			record.pending.ID, candidate.Artifact.ID, file.Branch, file.Repository, shortCommit(file.Commit))
	} else {
		told = fmt.Sprintf("Document %s (%s) landed by another route, in a form other than the one confirmed here: it is on %s of %s at commit %s at %s. Nothing more is published from this copy and its waiting place is given back. Read what landed before changing it; your next revision of %s is taken as a revision of what landed.\n",
			record.pending.ID, candidate.Artifact.ID, file.Branch, file.Repository, shortCommit(file.Commit), candidate.Artifact.Path, candidate.Artifact.ID)
	}
	return s.carryResults(told)
}

// undecidedWrite is the write the operator names that nothing has decided yet,
// whether it waits for a decision or is confirmed and held.
func (s *Session) undecidedWrite(writeID string) (*writeRecord, error) {
	trimmed := strings.TrimSpace(writeID)
	for _, record := range s.writes {
		if record.pending.ID != trimmed {
			continue
		}
		if record.decided {
			return nil, fmt.Errorf("document %s has already been decided", trimmed)
		}
		return record, nil
	}
	return nil, fmt.Errorf("no document %q is waiting in this conversation", writeID)
}

// MarkLanded records that the operator landed an undecided document by hand, as
// the named commit of the repository documents are kept in. It works on a
// document confirmed and held, and on one still waiting for a decision. The
// harness reads the file at the commit first and records nothing unless that
// commit holds a file of the document's identity at the document's path.
func (s *Session) MarkLanded(ctx context.Context, writeID, commit string) (string, error) {
	record, err := s.undecidedWrite(writeID)
	if err != nil {
		return "", err
	}
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return "", fmt.Errorf("name the commit the document landed as: /landed %s <commit>", record.pending.ID)
	}
	if s.options.DocumentLandings == nil {
		return "", fmt.Errorf("nothing is configured to read the repository this project's documents are kept in, so the harness cannot check that %s holds document %s; it is not recorded as landed", commit, record.pending.ID)
	}
	id := strings.TrimSpace(record.pending.Write.ID)
	var documentPath string
	publication := record.pending.Publication
	if publication != nil {
		documentPath, id = publication.Candidate.Artifact.Path, publication.Candidate.Artifact.ID
	} else {
		if s.options.Documents == nil {
			return "", fmt.Errorf("no artifact store is configured, so the harness cannot tell where document %s is filed", record.pending.ID)
		}
		documentPath, err = s.options.Documents.WritePath(record.pending.Write)
		if err != nil {
			return "", fmt.Errorf("the harness cannot tell where document %s is filed: %w", record.pending.ID, err)
		}
	}
	file, err := s.options.DocumentLandings.AtCommit(ctx, commit, documentPath)
	if err != nil {
		return "", fmt.Errorf("read commit %s for document %s: %w; it is not recorded as landed", commit, record.pending.ID, err)
	}
	if !file.Found {
		return "", fmt.Errorf("commit %s of %s holds no file at %s; document %s is not recorded as landed", commit, file.Repository, documentPath, record.pending.ID)
	}
	if parsed, err := artifact.ParseDocument(file.Content); err == nil && parsed.ID != id {
		return "", fmt.Errorf("commit %s of %s holds a different document at %s (%s, not %s); document %s is not recorded as landed", commit, file.Repository, documentPath, parsed.ID, id, record.pending.ID)
	}
	payload := map[string]any{
		"write_id":   record.pending.ID,
		"artifact":   id,
		"path":       documentPath,
		"repository": file.Repository,
		"commit":     file.Commit,
		"how":        "by the operator's hand",
		"by":         "operator",
	}
	if publication != nil {
		payload["as_confirmed"] = strings.TrimSpace(string(file.Content)) == strings.TrimSpace(publication.Candidate.Content)
	}
	if err := s.emit(execution.EventDocumentLanded, payload); err != nil {
		return "", fmt.Errorf("record document landing: %w", err)
	}
	record.decided = true
	s.noteHandStep(intervention.KindOther, nil, "", id,
		fmt.Sprintf("recorded document %s as landed by hand as commit %s", record.pending.ID, shortCommit(file.Commit)))
	if err := s.carryResults(fmt.Sprintf("The operator says document %s (%s) landed by hand as commit %s of %s, and the harness found it at %s there. Its waiting place is given back, and your next revision of %s is taken as a revision of what landed.\n",
		record.pending.ID, id, shortCommit(file.Commit), file.Repository, documentPath, id)); err != nil {
		return "", err
	}
	return fmt.Sprintf("Document %s (%s) is recorded as landed as commit %s of %s, where the harness found it at %s. It is no longer offered for publication, its waiting place is given back, and the %s has been told that its next revision of it is a revision of what landed.",
		record.pending.ID, id, shortCommit(file.Commit), file.Repository, documentPath, RoleTitle(s.state.Role)), nil
}

// DropWrite stops offering an undecided document, confirmed and held ones
// included, and gives its waiting place back. The reason is required: it is
// recorded and told to the owning role, which would otherwise not know why a
// document it wrote is gone.
func (s *Session) DropWrite(writeID, reason string) (string, error) {
	record, err := s.undecidedWrite(writeID)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return "", fmt.Errorf("say why document %s is dropped: /drop %s <reason>; the reason is recorded and told to the %s", record.pending.ID, record.pending.ID, RoleTitle(s.state.Role))
	}
	if len(trimmed) > MaxOperatorMessageBytes {
		return "", fmt.Errorf("drop reason is %d bytes, limit is %d", len(trimmed), MaxOperatorMessageBytes)
	}
	summary := record.pending.recordedSummary()
	summary["reason"] = trimmed
	if err := s.emit(execution.EventDocumentDropped, summary); err != nil {
		return "", fmt.Errorf("record document drop: %w", err)
	}
	record.decided = true
	id := strings.TrimSpace(record.pending.Write.ID)
	s.noteHandStep(intervention.KindDecline, nil, "", id, fmt.Sprintf("dropped document %s", record.pending.ID))
	if err := s.carryResults(fmt.Sprintf("The operator dropped document %s (%s) because: %s. It is no longer offered and its waiting place is given back.\n", record.pending.ID, id, trimmed)); err != nil {
		return "", err
	}
	return fmt.Sprintf("Document %s (%s) is dropped because: %s. It is no longer offered, its waiting place is given back, and the %s has been told why.", record.pending.ID, id, trimmed, RoleTitle(s.state.Role)), nil
}
