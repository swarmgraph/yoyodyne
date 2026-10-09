package runstate

// A confirmed document waiting for a developer slot.
//
// A document's reviewed run takes a developer slot like any run. Where every
// slot is taken when its conversation offers it, nothing is reserved, and the
// document used to be offered again only at the next message in that
// conversation — so a design that unblocks other work waited for as long as the
// conversation stayed quiet, however many slots freed meanwhile. So the
// publication is written down here as waiting, beside the runs it waits behind,
// and the scheduler starts it in the next slot that frees, ahead of any new
// development run; the conversation still offers it at its next message, and
// whichever of the two reserves first runs it.
//
// A wait is one file per document, named for the run the document's
// publication is always given (DocumentPublication.RunID), so the same document
// is never waiting twice and never runs twice: a reservation refuses a second
// run of one document under the same lock that writes the wait, and the wait is
// cleared once a run of it is recorded.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DocumentWaitSchemaVersion is 1 and has never changed.
const DocumentWaitSchemaVersion = 1

// DocumentWait is one confirmed document whose reviewed run could not be
// reserved because every developer slot was taken. It carries the publication
// whole, so the scheduler can start it without the owning conversation, and
// when it began waiting, which orders the waits and says how long one has
// waited.
type DocumentWait struct {
	SchemaVersion int                 `json:"schema_version"`
	Document      DocumentPublication `json:"document"`
	Since         time.Time           `json:"since"`
}

// RunID is the run the document is published by.
func (w DocumentWait) RunID() string { return w.Document.RunID() }

func (w DocumentWait) Validate() error {
	var problems []error
	if w.SchemaVersion != DocumentWaitSchemaVersion {
		problems = append(problems, fmt.Errorf("document wait schema version %d is not supported", w.SchemaVersion))
	}
	if w.Since.IsZero() {
		problems = append(problems, errors.New("document wait must say when it began"))
	}
	if err := w.Document.Validate(); err != nil {
		problems = append(problems, fmt.Errorf("document publication: %w", err))
	}
	return errors.Join(problems...)
}

// WaitForSlot records a document as waiting for a developer slot, and reports
// whether it is waiting. A document already waiting keeps the moment it began.
// One whose run is already recorded is not waiting at all — another process
// reserved it — so nothing is written and false is returned. The check and the
// write are made under the reservation lock, which is what keeps a wait from
// being written after the reservation that would have cleared it.
func (s *Store) WaitForSlot(ctx context.Context, document DocumentPublication, at time.Time) (DocumentWait, bool, error) {
	wait := DocumentWait{SchemaVersion: DocumentWaitSchemaVersion, Document: document, Since: at.UTC()}
	if err := wait.Validate(); err != nil {
		return DocumentWait{}, false, err
	}
	release, err := s.lockReservations(ctx)
	if err != nil {
		return DocumentWait{}, false, err
	}
	defer release()
	runPath, err := s.statePath(wait.RunID())
	if err != nil {
		return DocumentWait{}, false, err
	}
	if _, err := os.Stat(runPath); err == nil {
		return DocumentWait{}, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return DocumentWait{}, false, fmt.Errorf("read whether document run %s is recorded: %w", wait.RunID(), err)
	}
	existing, found, err := s.documentWait(wait.RunID())
	if err != nil {
		return DocumentWait{}, false, err
	}
	if found {
		return existing, true, nil
	}
	directory := s.documentWaitDirectory()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return DocumentWait{}, false, fmt.Errorf("create the document wait directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".document-wait-*.tmp")
	if err != nil {
		return DocumentWait{}, false, fmt.Errorf("create the temporary record of a document waiting for a slot: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return DocumentWait{}, false, fmt.Errorf("secure the temporary record of a document waiting for a slot: %w", err)
	}
	if err := writeJSONFile(temporary, "document wait", wait); err != nil {
		temporary.Close()
		return DocumentWait{}, false, err
	}
	if err := temporary.Close(); err != nil {
		return DocumentWait{}, false, fmt.Errorf("close the temporary record of a document waiting for a slot: %w", err)
	}
	if err := os.Rename(temporaryPath, s.documentWaitPath(wait.RunID())); err != nil {
		return DocumentWait{}, false, fmt.Errorf("record the document waiting for a slot: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		return DocumentWait{}, false, err
	}
	return wait, true, nil
}

// DocumentWaits lists the documents waiting for a developer slot, longest
// waiting first, which is the order the scheduler starts them in. No directory
// is no document waiting. A record that cannot be read is an error rather than
// skipped, because a wait nobody can read is a document nothing would start.
func (s *Store) DocumentWaits() ([]DocumentWait, error) {
	entries, err := os.ReadDir(s.documentWaitDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the documents waiting for a slot: %w", err)
	}
	var waits []DocumentWait
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		runID := strings.TrimSuffix(entry.Name(), ".json")
		if !runIDPattern.MatchString(runID) {
			continue
		}
		wait, found, err := s.documentWait(runID)
		if err != nil {
			return nil, err
		}
		if found {
			waits = append(waits, wait)
		}
	}
	sort.SliceStable(waits, func(i, j int) bool {
		if !waits[i].Since.Equal(waits[j].Since) {
			return waits[i].Since.Before(waits[j].Since)
		}
		return waits[i].RunID() < waits[j].RunID()
	})
	return waits, nil
}

// ClearDocumentWait removes a document's wait, once a run of it is recorded or
// once it can no longer be published at all. Clearing what is not waiting is
// not an error: either way nothing is waiting.
func (s *Store) ClearDocumentWait(runID string) error {
	if !runIDPattern.MatchString(runID) {
		return fmt.Errorf("document wait names an invalid run %q", runID)
	}
	if err := os.Remove(s.documentWaitPath(runID)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("clear the document waiting for a slot: %w", err)
	}
	return syncDirectory(s.documentWaitDirectory())
}

func (s *Store) documentWait(runID string) (DocumentWait, bool, error) {
	file, err := os.Open(s.documentWaitPath(runID))
	if errors.Is(err, os.ErrNotExist) {
		return DocumentWait{}, false, nil
	}
	if err != nil {
		return DocumentWait{}, false, fmt.Errorf("open the document waiting for a slot as %s: %w", runID, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var wait DocumentWait
	if err := decoder.Decode(&wait); err != nil {
		return DocumentWait{}, false, fmt.Errorf("decode the document waiting for a slot as %s: %w", runID, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return DocumentWait{}, false, fmt.Errorf("decode the document waiting for a slot as %s: %w", runID, err)
	}
	if err := wait.Validate(); err != nil {
		return DocumentWait{}, false, fmt.Errorf("the document waiting for a slot as %s: %w", runID, err)
	}
	if wait.RunID() != runID {
		return DocumentWait{}, false, fmt.Errorf("the document waiting for a slot as %s is published by run %s", runID, wait.RunID())
	}
	return wait, true, nil
}

// The waits live in a directory of their own beside the run records, which a
// scan of the runs steps over because it reads only files.
func (s *Store) documentWaitDirectory() string {
	return filepath.Join(s.root, "document-waits")
}

func (s *Store) documentWaitPath(runID string) string {
	return filepath.Join(s.documentWaitDirectory(), runID+".json")
}
