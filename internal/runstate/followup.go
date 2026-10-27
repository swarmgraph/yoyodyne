package runstate

// Where follow-ups are kept: one store under the product's state, one file per
// follow-up, every write made through the pinned state root and read back
// before it is reported done.
//
// The record and the rules on ending one are internal/followup's; this store
// only refuses to write a record those rules refuse. Every change to a stored
// follow-up is made under the store's lock, from a strict read of what is
// there, so two acts on one follow-up at once cannot both end it, and a record
// this build cannot read whole is refused rather than rewritten from part of it
// (docs/designs/follow-ups.md, "The decision").
//
// The store is kept on each harness's machine: a follow-up is seen only on the
// machine that recorded it, as the design says.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/followup"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

const (
	followUpsDirectory = "follow-ups"
	followUpsLock      = ".follow-ups.lock"
)

// ErrFollowUpUnknown is an identifier this product keeps no follow-up for.
var ErrFollowUpUnknown = errors.New("no such follow-up is kept")

// FollowUpStore keeps a product's follow-ups.
type FollowUpStore struct {
	stateRoot string
	anchor    string
	productID domain.ProductID
}

func NewFollowUpStore(root string, productID domain.ProductID) (*FollowUpStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the follow-ups' state root: %w", err)
	}
	return &FollowUpStore{stateRoot: stateRoot, anchor: anchor, productID: productID}, nil
}

// directory is the follow-ups' directory, relative to the state root.
func (s *FollowUpStore) directory() string {
	return filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), followUpsDirectory)
}

// Add records a new follow-up, open, under an identifier the store issues, and
// returns it as it was read back.
func (s *FollowUpStore) Add(written followup.FollowUp) (followup.FollowUp, error) {
	if written.ID != "" {
		return followup.FollowUp{}, errors.New("a new follow-up's identifier is issued by the store")
	}
	if written.State != followup.StateOpen {
		return followup.FollowUp{}, fmt.Errorf("a new follow-up is open, and this one is %q", written.State)
	}
	written.ID = followup.NewID()
	written.CreatedAt = written.CreatedAt.UTC()
	written.BackToOwnerAt = written.BackToOwnerAt.UTC()
	written.ToManagerAt = written.ToManagerAt.UTC()
	if err := written.Validate(); err != nil {
		return followup.FollowUp{}, fmt.Errorf("refused follow-up: %w", err)
	}
	var stored followup.FollowUp
	err := s.locked(func(root *repowrite.PinnedRoot) error {
		var err error
		stored, err = s.write(root, written, true)
		return err
	})
	return stored, err
}

// Pass ends an open follow-up by its check passing.
func (s *FollowUpStore) Pass(id string, at time.Time, result string) (followup.FollowUp, error) {
	return s.change(id, func(standing followup.FollowUp) (followup.FollowUp, error) {
		return standing.Pass(at, result)
	})
}

// Close ends an open follow-up by its owner closing it, whoever waits
// withdrawing it, or the operator closing it, with a reason. Whether the one
// asking is that person is the caller's to establish; what this refuses is an
// ending with no reason, or of a follow-up already ended.
func (s *FollowUpStore) Close(id string, by followup.Closer, reason string, at time.Time) (followup.FollowUp, error) {
	return s.change(id, func(standing followup.FollowUp) (followup.FollowUp, error) {
		return standing.Close(by, reason, at)
	})
}

func (s *FollowUpStore) change(id string, apply func(followup.FollowUp) (followup.FollowUp, error)) (followup.FollowUp, error) {
	if !followup.ValidID(id) {
		return followup.FollowUp{}, fmt.Errorf("%w: %q is not a follow-up identifier", ErrFollowUpUnknown, id)
	}
	var stored followup.FollowUp
	err := s.locked(func(root *repowrite.PinnedRoot) error {
		encoded, err := root.ReadFile(s.path(id))
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrFollowUpUnknown, id)
		}
		if err != nil {
			return fmt.Errorf("read follow-up %s: %w", id, err)
		}
		standing, err := s.decode(id, encoded, true)
		if err != nil {
			return err
		}
		changed, err := apply(standing)
		if err != nil {
			return fmt.Errorf("follow-up %s: %w", id, err)
		}
		stored, err = s.write(root, changed, false)
		return err
	})
	return stored, err
}

// write puts one follow-up on the disk, under the lock the caller holds, and
// reads it back: a write is reported done only once the bytes read back are the
// bytes written and decode to a follow-up the rules accept.
func (s *FollowUpStore) write(root *repowrite.PinnedRoot, recorded followup.FollowUp, create bool) (followup.FollowUp, error) {
	encoded, err := encodeRecord("follow-up", recorded)
	if err != nil {
		return followup.FollowUp{}, err
	}
	if len(encoded) > maxEncodedStateBytes {
		return followup.FollowUp{}, fmt.Errorf("encoded follow-up is %d bytes, limit is %d", len(encoded), maxEncodedStateBytes)
	}
	path := s.path(recorded.ID)
	if create {
		err = root.CreateFile(path, encoded, 0o600)
	} else {
		err = root.WriteFile(path, encoded, 0o600, false)
	}
	if err != nil {
		return followup.FollowUp{}, fmt.Errorf("record follow-up %s: %w", recorded.ID, err)
	}
	if err := root.Unchanged(); err != nil {
		return followup.FollowUp{}, err
	}
	readBack, err := root.ReadFile(path)
	if err != nil {
		return followup.FollowUp{}, fmt.Errorf("read back follow-up %s: %w", recorded.ID, err)
	}
	if !bytes.Equal(readBack, encoded) {
		return followup.FollowUp{}, fmt.Errorf("follow-up %s read back differently from what was written", recorded.ID)
	}
	return s.decode(recorded.ID, readBack, true)
}

// Load reads one follow-up.
func (s *FollowUpStore) Load(id string) (followup.FollowUp, error) {
	if !followup.ValidID(id) {
		return followup.FollowUp{}, fmt.Errorf("%w: %q is not a follow-up identifier", ErrFollowUpUnknown, id)
	}
	encoded, err := s.read(id)
	if errors.Is(err, fs.ErrNotExist) {
		return followup.FollowUp{}, fmt.Errorf("%w: %s", ErrFollowUpUnknown, id)
	}
	if err != nil {
		return followup.FollowUp{}, err
	}
	return s.decode(id, encoded, false)
}

// List reads every follow-up the product keeps, oldest first. A record that
// cannot be read is named in the error and the rest are still returned, so one
// bad file never hides every other wait.
func (s *FollowUpStore) List() ([]followup.FollowUp, error) {
	entries, err := os.ReadDir(filepath.Join(s.stateRoot, s.directory()))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list follow-ups: %w", err)
	}
	var kept []followup.FollowUp
	var problems []error
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || entry.IsDir() || !followup.ValidID(id) {
			continue
		}
		encoded, err := s.read(id)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err == nil {
			var standing followup.FollowUp
			if standing, err = s.decode(id, encoded, false); err == nil {
				kept = append(kept, standing)
				continue
			}
		}
		problems = append(problems, err)
	}
	sort.Slice(kept, func(i, j int) bool {
		if !kept[i].CreatedAt.Equal(kept[j].CreatedAt) {
			return kept[i].CreatedAt.Before(kept[j].CreatedAt)
		}
		return kept[i].ID < kept[j].ID
	})
	return kept, errors.Join(problems...)
}

func (s *FollowUpStore) read(id string) ([]byte, error) {
	file, err := os.Open(filepath.Join(s.stateRoot, s.path(id)))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read follow-up %s: %w", id, err)
	}
	if len(encoded) > maxEncodedStateBytes {
		return nil, fmt.Errorf("follow-up %s exceeds %d bytes", id, maxEncodedStateBytes)
	}
	return encoded, nil
}

// decode reads one stored follow-up. It is strict for the reads that precede a
// write, so a field this build does not know is never lost by writing the
// record back without it, and tolerant for Load and List, which only say what
// is kept. Either way the record must be one the rules accept, under the name
// it is kept by.
func (s *FollowUpStore) decode(id string, encoded []byte, strict bool) (followup.FollowUp, error) {
	var standing followup.FollowUp
	if strict {
		if err := decodeStrictly(encoded, &standing); err != nil {
			return followup.FollowUp{}, fmt.Errorf("decode follow-up %s: %w", id, err)
		}
	} else {
		unknown, err := decodeTolerating(encoded, &standing)
		if err != nil {
			return followup.FollowUp{}, fmt.Errorf("decode follow-up %s: %w", id, err)
		}
		noteUnknownFields("follow-up", unknown)
	}
	if standing.ID != id {
		return followup.FollowUp{}, fmt.Errorf("follow-up %s records the identifier %q", id, standing.ID)
	}
	if err := standing.Validate(); err != nil {
		return followup.FollowUp{}, fmt.Errorf("follow-up %s: %w", id, err)
	}
	return standing, nil
}

// locked runs one change with the state root pinned and the store's lock held.
func (s *FollowUpStore) locked(change func(*repowrite.PinnedRoot) error) error {
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return fmt.Errorf("pin the follow-ups' state root: %w", err)
	}
	defer root.Close()
	directory := s.directory()
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return fmt.Errorf("create the follow-ups' directory: %w", err)
	}
	lock, err := root.OpenLock(filepath.Join(directory, followUpsLock), 0o600)
	if err != nil {
		return fmt.Errorf("open the follow-ups' lock: %w", err)
	}
	defer lock.Close()
	if err := lockStateFile(context.Background(), lock); err != nil {
		return fmt.Errorf("lock the follow-ups: %w", err)
	}
	defer unlockStateFile(lock)
	return change(root)
}

// path names one follow-up's file, relative to the state root. The identifier
// is one the store issued and checked, so it never names anything but a file
// in the follow-ups' directory.
func (s *FollowUpStore) path(id string) string {
	return filepath.Join(s.directory(), id+".json")
}
