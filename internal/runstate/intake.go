package runstate

// The operator's hold on what the harness starts by itself.
//
// There are two switches and they are not the same switch. The operator hold at
// the state root stops everything the harness would spend on a provider,
// including the runs already under way, which park at their next boundary
// keeping everything they have. That is the right verb for an account, a bill,
// or an afternoon away from the machine.
//
// This is the narrower one: stop choosing new work, and let what is running
// finish. It is the case an operator reaches for when something looks wrong but
// not urgent — a decomposition that is heading somewhere odd, a queue they want
// to reorder first — and it is the one that has to exist before it is needed,
// because the alternative in the moment is stopping everything and losing the
// work in flight along with it.
//
// It is product-scoped rather than machine-wide, which is the other difference:
// what a development manager may pull is a fact about one backlog, and holding
// intake on one product must not quietly stop another. It is a flag rather than
// an instruction to any particular run — the pipeline reads it where a run would
// be started for a reason other than the operator naming it, and starts nothing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// IntakeHoldSchemaVersion is 1 and has never changed.
const IntakeHoldSchemaVersion = 1

// MaxIntakeReasonBytes bounds the reason an operator gives for holding intake.
// A hold somebody comes back to in the morning is only useful if it says what it
// was for, and a bounded line is enough to say it.
const MaxIntakeReasonBytes = 4 << 10

// IntakeHolder is who placed a hold on intake. It is recorded with the hold
// rather than worked out afterwards, because the two things that place one are
// different things for a reader to do something about, and every surface that
// says intake is held has to say which it was. Inferring it is what went wrong
// before: a session that reported its own brake's hold as the operator's sent
// somebody to look for a decision nobody had made.
type IntakeHolder string

const (
	// IntakeHolderOperator is the operator stopping the choosing themselves,
	// which is what the switch exists for.
	IntakeHolderOperator IntakeHolder = "operator"
	// IntakeHolderBrake is the harness's own failure-storm brake placing the
	// operator's switch after runs kept blocking. What lifts it is the harness
	// itself — on the development manager's decision, or on a probe run that
	// lands — unless she has escalated it to the operator; see intakebrake.go.
	IntakeHolderBrake IntakeHolder = "brake"
)

// Recorded reports a holder this harness knows how to name. The empty holder is
// not one: it is a hold written before the holder was recorded, and it is read
// rather than refused — a hold nobody can read must never be treated as absent —
// while nothing pretends to know whose it is.
func (h IntakeHolder) Recorded() bool {
	switch h {
	case IntakeHolderOperator, IntakeHolderBrake:
		return true
	}
	return false
}

// IntakeHold is the recorded fact that the harness has been stopped from
// choosing new work for this product. It carries who stopped it, when, and why.
// The operator's hold carries nothing else: what lifts it is a person, so there
// is no deadline to record. The brake's carries its own record of what it does
// next, because what lifts that one is the harness.
type IntakeHold struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	HeldAt        time.Time        `json:"held_at"`
	// HeldBy is who placed it. It is absent only on a hold written before this
	// was recorded, which Says names as the absence it is rather than guessing.
	HeldBy IntakeHolder `json:"held_by,omitempty"`
	// Reason is optional, for the reason a stop's is: an operator who holds
	// intake in a hurry owes nobody an explanation.
	Reason string `json:"reason,omitempty"`
	// Brake is the brake's own record on a hold it placed: what tripped it, who
	// is deciding about it, and the probe that releases it. It is absent on the
	// operator's hold, and on a brake hold written before the brake summoned
	// anybody, which is read as a hold that waits on a person exactly as it did.
	Brake *IntakeBrake `json:"brake,omitempty"`
}

func (h IntakeHold) Validate() error {
	var problems []error
	if h.SchemaVersion != IntakeHoldSchemaVersion {
		problems = append(problems, fmt.Errorf("intake hold schema version %d is not supported", h.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(h.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if h.HeldAt.IsZero() {
		problems = append(problems, errors.New("held at is required"))
	}
	// An unrecorded holder is a hold from before this was written down and is
	// read as one. A holder that is recorded and is not one this harness places
	// is a record nothing here can speak for, and reading it as an absence would
	// put the guessing back.
	if h.HeldBy != "" && !h.HeldBy.Recorded() {
		problems = append(problems, fmt.Errorf("intake hold holder %q is not one this harness records", h.HeldBy))
	}
	if len(h.Reason) > MaxIntakeReasonBytes {
		problems = append(problems, fmt.Errorf("intake hold reason is %d bytes, which exceeds the %d byte bound", len(h.Reason), MaxIntakeReasonBytes))
	}
	// The brake's record belongs to the brake's hold and to no other: a record
	// of the development manager deciding about the operator's switch would be a
	// decision about a hold she does not hold.
	if h.Brake != nil {
		if h.HeldBy != IntakeHolderBrake {
			problems = append(problems, errors.New("a brake record is carried only by a hold the brake placed"))
		}
		if err := h.Brake.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("invalid brake record: %w", err))
		}
	}
	return errors.Join(problems...)
}

// Braked reports a hold the brake placed and is working: one carrying the
// brake's own record of who is deciding and what happens next. A brake hold
// written before that record existed is not one, and waits on a person as it
// always did.
func (h IntakeHold) Braked() bool {
	return h.HeldBy == IntakeHolderBrake && h.Brake != nil
}

// Probing reports the brake's own record naming this item as the probe it has
// in flight. It is the one thing that lets a harness selection through a held
// intake, and it is asked of the record rather than of the selection: a run
// that said it was the probe would be a hold any caller could talk its way
// past, and a record the brake wrote under its own lock is not.
func (h IntakeHold) Probing(workItemID string) bool {
	return h.Braked() && h.Brake.Probing() && h.Brake.Probe.WorkItemID == strings.TrimSpace(workItemID)
}

// WaitsOnAPerson reports a hold nothing but a person lifts: the operator's own,
// a brake hold from before the brake worked its own holds, and a brake hold
// escalated to the operator — by the development manager, or by the harness
// once its summons-and-probe loop has gone round the configured number of
// times. Every other brake hold is the harness's to lift.
func (h IntakeHold) WaitsOnAPerson() bool {
	return !h.Braked() || h.Brake.Escalated()
}

// Whose is whose move the hold is, worded once here for every surface that
// puts a held intake on an attention line. The operator's hold is theirs, and a
// brake hold is whoever its own record says.
func (h IntakeHold) Whose() string {
	if h.Braked() {
		return h.Brake.Whose()
	}
	return "the operator's — nothing new is chosen until `yoyo release` lifts it"
}

// Standing is what happens to the hold next, as the clause that follows Says in
// a banner or a session's account. A hold that waits on a person says so, and a
// brake hold says what the harness does about it.
func (h IntakeHold) Standing() string {
	if h.Braked() {
		return h.Brake.Standing()
	}
	return "it stays held until somebody releases it"
}

// Account is Says with, for a hold the brake is working itself, what the
// harness does about it next. The operator's hold says only who placed it and
// why, because what lifts it is the command every surface already names beside
// it, and a brake hold says what is deciding it, since that is the one thing a
// reader of a stopped line was missing.
func (h IntakeHold) Account() string {
	if !h.Braked() {
		return h.Says()
	}
	return h.Says() + ", and " + h.Standing()
}

// Says is the one clause every surface prints about a hold in force: who placed
// it and what caused it, composed once here rather than assembled by whichever
// format string is doing the printing.
//
// It is one clause rather than a whole sentence because every surface that
// prints it already frames it — a banner, a headline, a persona's line — and
// what they were all missing was whose hold it is and why. Three of those
// frames stacked, each introducing the next with its own colon, is what an
// operator read instead, and the innermost one was the only one that named the
// actual holder.
//
// The connective differs by holder because the causes do: a brake counts runs
// that blocked, and an operator says what looked wrong.
func (h IntakeHold) Says() string {
	cause := strings.TrimSpace(h.Reason)
	switch h.HeldBy {
	case IntakeHolderBrake:
		if cause == "" {
			return "the harness's own brake placed it after runs kept blocking"
		}
		return "the harness's own brake placed it after " + cause
	case IntakeHolderOperator:
		if cause == "" {
			return "the operator placed it and gave no reason"
		}
		return "the operator placed it — " + cause
	}
	if cause == "" {
		return "the record does not say who placed it or why"
	}
	return "the record does not say who placed it — " + cause
}

// IntakeHoldStore is where the hold is recorded: one file under the product,
// because the queue it holds belongs to one.
type IntakeHoldStore struct {
	root      string
	productID domain.ProductID
}

func NewIntakeHoldStore(root string, productID domain.ProductID) (*IntakeHoldStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &IntakeHoldStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *IntakeHoldStore) Root() string { return s.root }

// Hold records a hold on intake, and who is placing it. Holding what is already
// held is deliberately not an error and deliberately does not restamp it: an
// operator who holds twice means the same thing the second time, and when the
// hold began is what says how long the harness has been choosing nothing. A
// second reason and a second holder are dropped for the same reason the time is
// kept — the hold in force is the one that was placed, and rewriting either would
// rewrite the account of why nothing has started since. That is also what makes
// the brake tripping over the operator's own hold report the operator, which is
// the truth about who stopped the line.
func (s *IntakeHoldStore) Hold(holder IntakeHolder, reason string, at time.Time) (IntakeHold, error) {
	if !holder.Recorded() {
		return IntakeHold{}, fmt.Errorf("intake hold holder %q is not one this harness records", holder)
	}
	release, err := s.lock()
	if err != nil {
		return IntakeHold{}, err
	}
	defer release()
	if existing, held, err := s.Held(); err != nil || held {
		return existing, err
	}
	recorded := IntakeHold{
		SchemaVersion: IntakeHoldSchemaVersion,
		ProductID:     s.productID,
		HeldAt:        at.UTC(),
		HeldBy:        holder,
		Reason:        strings.TrimSpace(reason),
	}
	if err := s.write(recorded); err != nil {
		return IntakeHold{}, err
	}
	return recorded, nil
}

// write replaces the hold's file with the record given, whole and durable:
// written to a temporary file beside it and renamed over it, so no reader ever
// sees half a hold.
func (s *IntakeHoldStore) write(recorded IntakeHold) error {
	if err := recorded.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create product state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".intake-hold-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary intake hold: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary intake hold: %w", err)
	}
	if err := writeJSONFile(temporary, "intake hold", recorded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary intake hold: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path()); err != nil {
		return fmt.Errorf("replace intake hold: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the writers of the hold. Placing a hold was a single rename
// and needed none; the brake's record is revised in place by the watching
// session and by the development manager's conversation, which may be two
// processes, and a read-modify-write with nothing between them is a decision
// one of them loses.
func (s *IntakeHoldStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create product state directory: %w", err)
	}
	file, err := os.OpenFile(s.path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open intake hold lock: %w", err)
	}
	// Bounded, because what this waits on is another process's one small write:
	// a wait that outlasts that is a lock somebody died holding, and a hold that
	// cannot be written should fail where it is rather than hang a poll loop.
	ctx, cancel := context.WithTimeout(context.Background(), intakeLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the intake hold: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

// intakeLockWait bounds the wait for the hold's lock. The writes it serializes
// are one small file each, so anything longer is a holder that is not coming
// back.
const intakeLockWait = 5 * time.Second

// Held reports whether intake is held, by whoever placed it. No record is the ordinary
// answer and means the harness may choose work, which is why it is reported as an
// absence rather than as a failure to look. A record that cannot be read is
// neither: it is an error, because a hold nobody can read must never be started
// through as though it were absent.
func (s *IntakeHoldStore) Held() (IntakeHold, bool, error) {
	file, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return IntakeHold{}, false, nil
	}
	if err != nil {
		return IntakeHold{}, false, fmt.Errorf("open intake hold: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var held IntakeHold
	if err := decoder.Decode(&held); err != nil {
		return IntakeHold{}, false, fmt.Errorf("decode intake hold: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return IntakeHold{}, false, fmt.Errorf("decode intake hold: %w", err)
	}
	if err := held.Validate(); err != nil {
		return IntakeHold{}, false, err
	}
	if held.ProductID != s.productID {
		return IntakeHold{}, false, fmt.Errorf("intake hold belongs to product %q, not %q", held.ProductID, s.productID)
	}
	return held, true, nil
}

// Release lifts the hold and reports what was lifted, recording nobody as the
// one who lifted it. It is ReleaseBy for a caller with nobody to name.
func (s *IntakeHoldStore) Release() (IntakeHold, bool, error) {
	return s.ReleaseBy("", time.Now())
}

// ReleaseBy lifts the hold and reports what was lifted, recording who lifted it
// beside the absence. Releasing what is not held is not an error for the same
// reason holding twice is not: the operator means the harness to be choosing
// work, and it is — and nothing is recorded, because nothing was lifted.
func (s *IntakeHoldStore) ReleaseBy(by string, at time.Time) (IntakeHold, bool, error) {
	release, err := s.lock()
	if err != nil {
		return IntakeHold{}, false, err
	}
	defer release()
	held, found, err := s.Held()
	if err != nil {
		return IntakeHold{}, false, err
	}
	if !found {
		return IntakeHold{}, false, nil
	}
	return s.lift(held, by, at)
}

// lift removes the hold in force, having first recorded who lifted it. The
// record is written before the hold is removed, so a process killed between
// the two leaves a hold that is still standing and a release that names it;
// the next release overwrites the record, and a reader matching the record to
// the hold it lifted finds the hold still there and reads past it. The caller
// holds the store's lock.
func (s *IntakeHoldStore) lift(held IntakeHold, by string, at time.Time) (IntakeHold, bool, error) {
	record := IntakeRelease{
		SchemaVersion: IntakeReleaseSchemaVersion,
		Hold:          held,
		ReleasedAt:    at.UTC(),
		ReleasedBy:    oneline.Fold(by, MaxIntakeReleasedByBytes-len(oneline.Marker)),
	}
	if err := s.writeRelease(record); err != nil {
		return IntakeHold{}, false, err
	}
	if err := os.Remove(s.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return IntakeHold{}, false, fmt.Errorf("release intake hold: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return IntakeHold{}, false, err
	}
	return held, true, nil
}

// IntakeReleaseSchemaVersion is 1 and has never changed.
const IntakeReleaseSchemaVersion = 1

// MaxIntakeReleasedByBytes bounds who a release names. It is a line: a terminal,
// a conversation and its turn, or the harness and what moved it.
const MaxIntakeReleasedByBytes = 1 << 10

// IntakeRelease is the record of the last hold being lifted: what was lifted,
// when, and by whom. It exists because a hold is a file and its release is that
// file's absence, and an absence says nothing about who made it — which is what
// the channel has to say when a hold it announced is lifted. One record rather
// than a log, because what it answers is who lifted the hold that was just
// standing, and the hold before that is history the channel already said.
type IntakeRelease struct {
	SchemaVersion int        `json:"schema_version"`
	Hold          IntakeHold `json:"hold"`
	ReleasedAt    time.Time  `json:"released_at"`
	// ReleasedBy is who lifted it, in the words the surface that lifted it
	// recorded. It is absent on a release nothing named, which is read rather
	// than refused.
	ReleasedBy string `json:"released_by,omitempty"`
}

func (r IntakeRelease) Validate() error {
	var problems []error
	if r.SchemaVersion != IntakeReleaseSchemaVersion {
		problems = append(problems, fmt.Errorf("intake release schema version %d is not supported", r.SchemaVersion))
	}
	if err := r.Hold.Validate(); err != nil {
		problems = append(problems, fmt.Errorf("released hold: %w", err))
	}
	if r.ReleasedAt.IsZero() {
		problems = append(problems, errors.New("released at is required"))
	}
	if len(r.ReleasedBy) > MaxIntakeReleasedByBytes {
		problems = append(problems, fmt.Errorf("released by is %d bytes, which exceeds the %d byte bound", len(r.ReleasedBy), MaxIntakeReleasedByBytes))
	}
	return errors.Join(problems...)
}

// Says is who lifted the hold, as a clause: what the surface recorded, or the
// stated absence.
func (r IntakeRelease) Says() string {
	if by := strings.TrimSpace(r.ReleasedBy); by != "" {
		return "released by " + by
	}
	return "released by somebody the record does not name"
}

// writeRelease replaces the release record, whole and durable, as write does
// the hold's.
func (s *IntakeHoldStore) writeRelease(record IntakeRelease) error {
	if err := record.Validate(); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.root, ".intake-release-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary intake release: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary intake release: %w", err)
	}
	if err := writeJSONFile(temporary, "intake release", record); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary intake release: %w", err)
	}
	if err := os.Rename(temporaryPath, s.releasePath()); err != nil {
		return fmt.Errorf("replace intake release: %w", err)
	}
	return syncDirectory(s.root)
}

// LastRelease is the record of the last hold lifted, or nothing where none has
// ever been. A record that cannot be read is an error rather than an absence,
// for the reason a hold's is: a reader would otherwise say nobody lifted a hold
// somebody did.
func (s *IntakeHoldStore) LastRelease() (IntakeRelease, bool, error) {
	file, err := os.Open(s.releasePath())
	if errors.Is(err, os.ErrNotExist) {
		return IntakeRelease{}, false, nil
	}
	if err != nil {
		return IntakeRelease{}, false, fmt.Errorf("open intake release: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return IntakeRelease{}, false, fmt.Errorf("read intake release: %w", err)
	}
	// The record is only ever read to say who lifted a hold, so a field this
	// build does not know is read past rather than refused.
	var release IntakeRelease
	if _, err := decodeTolerating(encoded, &release); err != nil {
		return IntakeRelease{}, false, fmt.Errorf("decode intake release: %w", err)
	}
	if err := release.Validate(); err != nil {
		return IntakeRelease{}, false, err
	}
	if release.Hold.ProductID != s.productID {
		return IntakeRelease{}, false, fmt.Errorf("intake release belongs to product %q, not %q", release.Hold.ProductID, s.productID)
	}
	return release, true, nil
}

// releasePath names the record of the last release, beside the hold it lifted.
func (s *IntakeHoldStore) releasePath() string {
	return filepath.Join(s.root, "intake-release.json")
}

// path names the hold's file. It is one fixed name under the product: nothing
// about it is derived from anything a caller supplies.
func (s *IntakeHoldStore) path() string {
	return filepath.Join(s.root, "intake-hold.json")
}
