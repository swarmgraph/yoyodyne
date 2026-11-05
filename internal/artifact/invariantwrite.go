package artifact

// An invariant written from the architect's conversation.
//
// An invariant is the architect's like her designs and decision records, and it
// reaches the repository the way they do: a typed write in her reply, refused
// before anybody is asked where it may not land, confirmed under the approval
// setting that governs her documents or put to the operator, and carried to the
// target branch by a reviewed run. What differs is the file. An invariant keeps
// its own directory's scheme (internal/invariant): the file name is its id, the
// frontmatter records its title, status, origin, scope and revisions and nothing
// else, and the body is exactly the `## Must hold` and `## Why` sections. So the
// write is checked, prepared and rendered by that package, and what this file
// adds is the route — the same checks, candidates and approvals the rest of this
// package gives a document — around it.
//
// The approval a candidate carries is kept on the candidate, which is the
// publication run's durable record, rather than in the file: an invariant's
// frontmatter has no approvals, and adding them would change the scheme every
// reader of the directory holds it to.

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
)

// invariantStore is the invariants directory this store writes an invariant
// through: the directory an invariant is filed in, in the same repository.
func (s Store) invariantStore() (invariant.Store, bool) {
	home, filed := s.homeFor(KindInvariant)
	if !filed {
		return invariant.Store{}, false
	}
	return invariant.Store{RepositoryRoot: s.RepositoryRoot, Directory: home}, true
}

// findInvariant reads the invariant an id names, where this store knows where
// invariants are filed and one is recorded under it.
func (s Store) findInvariant(id string) (invariant.Invariant, bool, error) {
	store, filed := s.invariantStore()
	if !filed || domain.ValidateIdentifier("id", strings.TrimSpace(id)) != nil {
		return invariant.Invariant{}, false, nil
	}
	return store.Find(strings.TrimSpace(id))
}

// writesInvariant reports whether a write is to an invariant: a creation of the
// kind, or a revision naming an id no artifact answers to and an invariant does.
func (s Store) writesInvariant(write Write) (bool, error) {
	if write.Action == WriteCreate {
		return write.Kind == KindInvariant, nil
	}
	set, err := s.Load()
	if err != nil {
		return false, err
	}
	if _, isArtifact := set.Find(strings.TrimSpace(write.ID)); isArtifact {
		return false, nil
	}
	_, found, err := s.findInvariant(write.ID)
	return found, err
}

// prepareInvariant checks everything about an invariant write and returns the
// invariant it would record, writing nothing. It refuses what the invariants
// directory's scheme refuses, and what a document write may carry that an
// invariant has no place for.
func (s Store) prepareInvariant(role domain.AgentRole, write Write, now time.Time) (invariant.Invariant, error) {
	if err := Authorize(role, KindInvariant); err != nil {
		return invariant.Invariant{}, err
	}
	store, filed := s.invariantStore()
	if !filed {
		return invariant.Invariant{}, errors.New("this project does not say where an invariant is filed, so there is nowhere to write one")
	}
	id := strings.TrimSpace(write.ID)
	if strings.TrimSpace(write.Section) != "" {
		return invariant.Invariant{}, fmt.Errorf("invariant %q is revised whole; section is refused on an invariant", id)
	}
	if len(write.Supports) > 0 {
		return invariant.Invariant{}, fmt.Errorf("supports is refused on invariant %q; what established it is established_by", id)
	}
	statement, rationale, err := invariantBody(write.Body)
	if err != nil {
		return invariant.Invariant{}, fmt.Errorf("invariant %q: %w", id, err)
	}
	if write.Action == WriteCreate {
		if err := s.checkInvariantDirectory(store, write.Directory); err != nil {
			return invariant.Invariant{}, err
		}
		set, err := s.Load()
		if err != nil {
			return invariant.Invariant{}, err
		}
		if found, exists := set.Find(id); exists {
			return invariant.Invariant{}, fmt.Errorf("artifact %q already exists at %s, and one id names one document; the invariant needs an id of its own", id, found.Path)
		}
		draft := invariant.Draft{ID: id, Title: strings.TrimSpace(write.Title), Statement: statement, Rationale: rationale, EstablishedBy: write.EstablishedBy, Reason: strings.TrimSpace(write.Reason)}
		if write.Scope != nil {
			draft.Scope = *write.Scope
		}
		return store.PrepareCreate(role, draft, now)
	}
	existing, found, err := store.Find(id)
	if err != nil {
		return invariant.Invariant{}, err
	}
	if !found {
		return invariant.Invariant{}, fmt.Errorf("no invariant %q is recorded; one that does not exist yet is created rather than revised", id)
	}
	amendment := invariant.Amendment{Statement: &statement, Rationale: &rationale, Reason: strings.TrimSpace(write.Reason)}
	if title := strings.TrimSpace(write.Title); title != "" {
		amendment.Title = &title
	}
	if len(write.EstablishedBy) > 0 {
		established := write.EstablishedBy
		amendment.EstablishedBy = &established
	}
	if write.Scope != nil {
		amendment.Scope = write.Scope
	}
	amended, err := store.PrepareAmend(role, id, amendment, now)
	if err != nil {
		return invariant.Invariant{}, err
	}
	// A revision that leaves every field as it was is a recorded change with
	// nothing in it, and a reviewed run carrying only a new revision line.
	if amended.Title == existing.Title && amended.Statement == existing.Statement && amended.Rationale == existing.Rationale &&
		slices.Equal(amended.EstablishedBy, existing.EstablishedBy) && slices.Equal(amended.Scope, existing.Scope) {
		return invariant.Invariant{}, fmt.Errorf("revising invariant %q changes nothing it says; an amendment names what is changing", id)
	}
	return amended, nil
}

// invariantBody reads a written invariant's body as the two sections an
// invariant file carries, and refuses text outside them: the file is rendered
// from those two sections alone, so anything else the role wrote would be
// dropped without anybody deciding to drop it.
func invariantBody(body string) (statement, rationale string, err error) {
	statement, rationale, err = invariant.ParseBody(body)
	if err != nil {
		return "", "", fmt.Errorf("the body %w", err)
	}
	headings := 0
	for index, line := range strings.Split(strings.TrimSpace(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if index == 0 && !strings.HasPrefix(trimmed, "## ") {
			return "", "", errors.New("the body opens with text before its `## Must hold` section; an invariant's body is that section and `## Why`, and nothing else")
		}
		if strings.HasPrefix(trimmed, "## ") {
			headings++
		}
	}
	if headings != 2 {
		return "", "", fmt.Errorf("the body has %d level-two sections; an invariant's body is exactly `## Must hold` and `## Why`", headings)
	}
	return statement, rationale, nil
}

// checkInvariantDirectory refuses a new invariant filed anywhere but the
// invariants directory itself: every invariant is a file directly in it, and
// one a level down is reported rather than read.
func (s Store) checkInvariantDirectory(store invariant.Store, directory string) error {
	target, err := s.invariantDirectory(directory)
	if err != nil {
		return err
	}
	expected, err := store.ResolvedDirectory()
	if err != nil {
		return err
	}
	if target != expected {
		return fmt.Errorf("artifact directory %q is not where an invariant is filed, which is %s itself", directory, expected)
	}
	return nil
}

// invariantDirectory is a directory named for an invariant, cleaned as the
// invariants store cleans its own and kept inside the repository. It is not
// required to be inside an artifact home, which the invariants directory
// usually is only to be excluded from.
func (s Store) invariantDirectory(directory string) (string, error) {
	resolved, err := resolveDirectories("invariants directory", []string{directory})
	if err != nil {
		return "", err
	}
	return resolved[0], nil
}

// invariantWritePath is the file an invariant write lands in.
func (s Store) invariantWritePath(write Write) (string, error) {
	id := strings.TrimSpace(write.ID)
	if write.Action != WriteCreate {
		found, exists, err := s.findInvariant(id)
		if err != nil {
			return "", err
		}
		if !exists {
			return "", fmt.Errorf("no invariant %q is recorded", id)
		}
		return found.Path, nil
	}
	store, filed := s.invariantStore()
	if !filed {
		return "", errors.New("this project does not say where an invariant is filed")
	}
	directory, err := store.ResolvedDirectory()
	if err != nil {
		return "", err
	}
	return path.Join(directory, id+".md"), nil
}

// RecordInvariant writes an invariant into the checkout, for the operator's
// approval where no reviewed run can land a document (see the conversation's
// approvedDocumentRoute). It reports whether the write was an invariant at all,
// so a caller writes any other document the way it always has.
func (s Store) RecordInvariant(role domain.AgentRole, write Write, now time.Time) (Artifact, bool, error) {
	isInvariant, err := s.writesInvariant(write)
	if err != nil || !isInvariant {
		return Artifact{}, isInvariant, err
	}
	store, filed := s.invariantStore()
	if !filed {
		return Artifact{}, true, errors.New("this project does not say where an invariant is filed, so there is nowhere to write one")
	}
	prepared, err := s.prepareInvariant(role, write, now)
	if err != nil {
		return Artifact{}, true, err
	}
	var recorded invariant.Invariant
	if write.Action == WriteCreate {
		recorded, err = store.Create(role, invariant.Draft{ID: prepared.ID, Title: prepared.Title, Statement: prepared.Statement, Rationale: prepared.Rationale, EstablishedBy: prepared.EstablishedBy, Scope: prepared.Scope, Reason: prepared.Revisions[len(prepared.Revisions)-1].Reason}, now)
	} else {
		recorded, err = store.Amend(role, prepared.ID, invariant.Amendment{Title: &prepared.Title, Statement: &prepared.Statement, Rationale: &prepared.Rationale, EstablishedBy: &prepared.EstablishedBy, Scope: &prepared.Scope, Reason: prepared.Revisions[len(prepared.Revisions)-1].Reason}, now)
	}
	if err != nil {
		return Artifact{}, true, err
	}
	return invariantArtifact(recorded), true, nil
}

// invariantArtifact is an invariant as the rest of this package names a
// document: its id, title, file, lifecycle and revisions, under KindInvariant.
// It is what a publication run, its record, and its owner are told the document
// is; the file itself stays in the invariants directory's form.
func invariantArtifact(recorded invariant.Invariant) Artifact {
	revisions := make([]Revision, 0, len(recorded.Revisions))
	for _, revision := range recorded.Revisions {
		revisions = append(revisions, Revision{Action: Action(revision.Action), By: revision.By, At: revision.At, Reason: revision.Reason})
	}
	status := StatusActive
	if !recorded.Active() {
		status = StatusRetired
	}
	return Artifact{ID: recorded.ID, Kind: KindInvariant, Title: recorded.Title, Status: status, Revisions: revisions, Path: recorded.Path}
}

// invariantCandidate is the file a prepared invariant would be, with the
// approval that confirms it kept on the candidate, bound to the file it
// replaces.
func (s Store) invariantCandidate(recorded invariant.Invariant, approval Approval) (ConfirmedDocument, error) {
	content, err := invariant.Render(recorded)
	if err != nil {
		return ConfirmedDocument{}, err
	}
	if len(content) > invariant.MaxFileBytes {
		return ConfirmedDocument{}, fmt.Errorf("invariant %q renders to %d bytes, limit is %d", recorded.ID, len(content), invariant.MaxFileBytes)
	}
	identity := invariantArtifact(recorded)
	approval.Revision = len(identity.Revisions) - 1
	identity.Approvals = []Approval{approval}
	confirmed := ConfirmedDocument{Artifact: identity, Content: content}
	prior, err := os.ReadFile(filepath.Join(s.RepositoryRoot, recorded.Path))
	if err == nil {
		confirmed.Before = DocumentIdentity(prior)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ConfirmedDocument{}, err
	}
	if err := confirmed.Validate(); err != nil {
		return ConfirmedDocument{}, err
	}
	return confirmed, nil
}

// invariantContentProblem says why a candidate's content is not the invariant
// its identity names, or is nil where it is.
func (d ConfirmedDocument) invariantContentProblem() error {
	if len(d.Content) > invariant.MaxFileBytes {
		return errors.New("confirmed content exceeds the invariant file limit")
	}
	parsed, err := invariant.Parse(d.Content)
	if err != nil {
		return err
	}
	parsed.Path = d.Artifact.Path
	if err := parsed.Validate(); err != nil {
		return err
	}
	expected := invariantArtifact(parsed)
	expected.Approvals = d.Artifact.Approvals
	if !reflect.DeepEqual(expected, d.Artifact) {
		return errors.New("confirmed content and invariant identity disagree")
	}
	return nil
}
