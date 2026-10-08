package runstate

// The operator's hold on everything the harness would spend on a provider.
//
// A run waits out a provider that refuses it, and the operator can release that
// wait early. This is the other direction: the provider would serve the work and
// the operator does not want it served yet — to conserve tokens, or for any
// other reason of their own. Without it the only way to stop the harness is to
// kill processes, which leaves cancelled runs with claimed items and work
// nobody can continue, so the one verb an operator reaches for in a hurry is the
// one that costs them the most.
//
// It is a flag rather than an instruction to any particular run. Every
// provider-call boundary reads it — a developer attempt, a reviewer invocation,
// a conversation turn — and parks or refuses instead of spending, which is the
// same waiting machinery an exhausted usage limit uses with the operator as its
// reset rather than a clock. What it cannot do is interrupt a generation already
// streaming: the flag is read before a call, so a call already made finishes.
//
// It lives at the state root rather than under a product, because what makes an
// operator pause is theirs rather than a product's: an account, a bill, an
// afternoon away from the machine. One switch means one file, and every product
// on the machine reads it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// OperatorHoldSchemaVersion is 1 and has never changed.
const OperatorHoldSchemaVersion = 1

// PauseOperatorHold is the pause kind an operator hold is accounted under. It is
// deliberately not one of the provider refusals: those are the provider
// declining to serve work, bounded by what the operator configured a run may
// spend waiting, while this is the operator declining to have it served and
// bounded by nothing but them. A ledger that reported the two as one would say a
// run spent six hours refused by a provider that never refused it.
const PauseOperatorHold = "operator_hold"

// OperatorHold is the recorded fact that the operator has paused all harness
// activity. It carries when they did and nothing else: what lifts it is them,
// so there is no deadline to record and nothing to work out from one.
type OperatorHold struct {
	SchemaVersion int       `json:"schema_version"`
	HeldAt        time.Time `json:"held_at"`
}

func (h OperatorHold) Validate() error {
	var problems []error
	if h.SchemaVersion != OperatorHoldSchemaVersion {
		problems = append(problems, fmt.Errorf("operator's pause schema version %d is not supported", h.SchemaVersion))
	}
	if h.HeldAt.IsZero() {
		problems = append(problems, errors.New("held at is required"))
	}
	return errors.Join(problems...)
}

// OperatorHoldStore is where the hold is recorded: one file at the state root,
// above the per-product records, because the switch is one switch.
type OperatorHoldStore struct {
	root string
}

func NewOperatorHoldStore(root string) (*OperatorHoldStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	return &OperatorHoldStore{root: filepath.Clean(root)}, nil
}

func (s *OperatorHoldStore) Root() string { return s.root }

// Hold records the operator's hold. Holding what is already held is deliberately
// not an error and deliberately does not restamp it: an operator who pauses
// twice means the same thing the second time, and the time the hold began is
// what says how long the harness has been quiet.
func (s *OperatorHoldStore) Hold(at time.Time) (OperatorHold, error) {
	if existing, held, err := s.Held(); err != nil || held {
		return existing, err
	}
	recorded := OperatorHold{SchemaVersion: OperatorHoldSchemaVersion, HeldAt: at.UTC()}
	if err := recorded.Validate(); err != nil {
		return OperatorHold{}, err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return OperatorHold{}, fmt.Errorf("create state root: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".operator-hold-*.tmp")
	if err != nil {
		return OperatorHold{}, fmt.Errorf("create the temporary record of the operator's pause: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return OperatorHold{}, fmt.Errorf("secure the temporary record of the operator's pause: %w", err)
	}
	if err := writeJSONFile(temporary, "operator's pause", recorded); err != nil {
		temporary.Close()
		return OperatorHold{}, err
	}
	if err := temporary.Close(); err != nil {
		return OperatorHold{}, fmt.Errorf("close the temporary record of the operator's pause: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path()); err != nil {
		return OperatorHold{}, fmt.Errorf("replace the record of the operator's pause: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return OperatorHold{}, err
	}
	return recorded, nil
}

// Held reports whether the operator is holding harness activity. No record is
// the ordinary answer and means the harness may spend, which is why it is
// reported as an absence rather than as a failure to look. A record that cannot
// be read is neither: it is reported as an error, because a hold nobody can read
// must never be spent through as though it were absent.
func (s *OperatorHoldStore) Held() (OperatorHold, bool, error) {
	file, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return OperatorHold{}, false, nil
	}
	if err != nil {
		return OperatorHold{}, false, fmt.Errorf("open the record of the operator's pause: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var held OperatorHold
	if err := decoder.Decode(&held); err != nil {
		return OperatorHold{}, false, fmt.Errorf("decode the record of the operator's pause: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return OperatorHold{}, false, fmt.Errorf("decode the record of the operator's pause: %w", err)
	}
	if err := held.Validate(); err != nil {
		return OperatorHold{}, false, err
	}
	return held, true, nil
}

// Release lifts the hold and reports what was lifted. Releasing what is not held
// is not an error for the same reason holding twice is not: the operator means
// the harness to be running, and it is.
func (s *OperatorHoldStore) Release() (OperatorHold, bool, error) {
	held, found, err := s.Held()
	if err != nil {
		return OperatorHold{}, false, err
	}
	if !found {
		return OperatorHold{}, false, nil
	}
	if err := os.Remove(s.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return OperatorHold{}, false, fmt.Errorf("release the operator's pause: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return OperatorHold{}, false, err
	}
	return held, true, nil
}

// path names the hold's file. It is one fixed name at the state root: nothing
// about it is derived from anything a caller supplies.
func (s *OperatorHoldStore) path() string {
	return filepath.Join(s.root, "operator-hold.json")
}
