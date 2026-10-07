package runstate

import (
	"bufio"
	"bytes"
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

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

const (
	// Both bounds include the trailing newline written by json.Encoder. Each
	// writer and reader shares its bound so durable state is always reloadable.
	maxEncodedStateBytes = 1 << 20
	maxEncodedEventBytes = 1 << 20
)

const (
	// leaseGrace bounds how long taking a lease waits on a lock that looks held
	// before reporting that a live holder owns the run. It is deliberately far
	// shorter than any real run: a genuine holder keeps its lease for as long as
	// it works on the run, so waiting one out would mean queueing behind a
	// developer rather than refusing a duplicate.
	//
	// What this waits out instead is a lease nobody holds any more. The lock
	// belongs to the open file description, not to the descriptor, so a child
	// process forked while the lease was open shares it until that child execs
	// and its close-on-exec descriptor goes away. The harness forks Git and
	// check processes constantly, so a released lease can keep answering "held"
	// for a few milliseconds afterwards. Believing that would refuse to resume a
	// run whose process is already gone, which is exactly the lost-process-handle
	// mistake recovery exists to avoid.
	leaseGrace = 250 * time.Millisecond
	// leaseGracePoll is how often the lock is retried within that grace.
	leaseGracePoll = 5 * time.Millisecond
)

type Store struct {
	root      string
	productID domain.ProductID
	// promotionWait bounds how long LeasePromotion waits its turn. It is a field
	// only so a test can drive the bound without spending it; every store the
	// harness builds gets promotionQueueWait.
	promotionWait time.Duration
	// promotionQueued is told, once per lease, that a promotion found the
	// branch's lease held and is waiting its turn. It is nil in every store the
	// harness builds and is a test's signal, for the reason the conversation
	// store carries one.
	promotionQueued func()
	// leaseWait bounds how long taking a run's lease waits out a lock that looks
	// held. It is a field only so a test can take the grace's end out of what it
	// asserts; every store the harness builds gets leaseGrace.
	leaseWait time.Duration
	// leaseHeld is told each time taking a run's lease finds the lock held and is
	// about to wait. It is nil in every store the harness builds and is a test's
	// signal, so the test can let the lock go at a moment it knows the wait has
	// begun rather than after a length of time it hopes is inside the grace.
	leaseHeld func()
	// signalGroup asks whether a process group still has members, as
	// syscall.Kill(-group, 0) does, and is nil in every store the harness
	// builds. It is a field only so a test can give the answer a real group
	// cannot be made to give on demand, such as a permission refusal.
	signalGroup func(group int) error
}

type ExistingWorkItemError struct {
	State State
}

func (e ExistingWorkItemError) Error() string {
	return fmt.Sprintf("work item %s already has incomplete run %s", e.State.WorkItemID, e.State.RunID)
}

// ErrNoRunInFlight reports that a work item has no incomplete run to adopt, so
// the caller may reserve a fresh one.
var ErrNoRunInFlight = errors.New("work item has no run in flight")

// ErrRunHeld reports that a run's lease belongs to a live holder. Somebody is
// already acting on the run, so this process must leave it alone rather than
// decide anything about it.
var ErrRunHeld = errors.New("run is held by another process")

// Lease is exclusive ownership of something only one process may act on at a
// time: an in-flight run, held for as long as a process acts on it; a promotion
// into one target branch, held for as long as that promotion takes; or the
// account rotation, held for as long as one start takes to choose and record
// which account it spends. Only one holder exists at a time, so a run resumed by
// a second process is as singular as one a first process reserved. It is an
// advisory file lock, which the operating system releases if its holder exits
// unexpectedly: an interrupted run stays adoptable, and a crashed promotion or
// start blocks nobody, rather than any of them becoming permanently owned by a
// process that no longer exists.
type Lease struct {
	// label names what this lease owns, so a failure to release says which.
	label string
	file  *os.File
	// holder is the stamp a reader observes this lease by, on the leases that keep
	// one. It is empty on the rest: a lease nobody has to watch from outside
	// carries nothing to keep in step with the lock.
	holder string
}

// releaseStateFile drops a lock and closes the file it was taken on, in that
// order, and it is how every lock in this package is let go of.
//
// The order is the whole of it. Closing would drop the lock anyway, but it is
// the step that can fail with nothing left to try again: Go does not retry an
// interrupted close and marks the descriptor closed regardless, so a lock
// released only by closing could stay taken with no handle left to let go of it.
// And a close is not even the moment the lock ends when somebody else still has
// the descriptor: the lock belongs to the open file description, which a process
// forked while it was held shares until that child execs — and the harness forks
// Git and check processes constantly. Either way what the next acquirer is told
// is that another process holds something nobody holds, which is the one answer
// it cannot act on. Unlocking first makes a failed close a leaked descriptor at
// worst, never a lock nobody can see.
func releaseStateFile(file *os.File) error {
	return errors.Join(unlockStateFile(file), closeStateFile(file))
}

// Release drops the lease. Releasing twice is a no-op, so a caller can defer it
// unconditionally.
//
// The stamp a reader observes goes before the lock is dropped, so no reader
// sees a holder for a lease the next process may already own. A stamp that
// could not be removed is reported: what it leaves behind is this process
// described as still holding something it let go of.
func (l *Lease) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	var cleared error
	if l.holder != "" {
		if err := os.Remove(l.holder); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleared = fmt.Errorf("clear the %s holder: %w", l.label, err)
		}
		l.holder = ""
	}
	if err := errors.Join(cleared, releaseStateFile(file)); err != nil {
		return fmt.Errorf("release %s lease: %w", l.label, err)
	}
	return nil
}

// CapacityError is every developer slot being occupied. It carries the two
// numbers rather than only refusing, because a caller that waits for a slot
// rather than failing has to be able to say what it is waiting on — and the
// fields are named in its JSON for the same reason: a state an operator reads is
// a state a machine reads too.
type CapacityError struct {
	Limit  int `json:"limit"`
	Active int `json:"active"`
}

func (e CapacityError) Error() string {
	return fmt.Sprintf("developer capacity is full: %d active run(s), limit %d", e.Active, e.Limit)
}

func NewStore(root string, productID domain.ProductID) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &Store{
		root:          filepath.Join(home.ProductDirectory(root, string(productID)), "runs"),
		productID:     productID,
		promotionWait: promotionQueueWait,
		leaseWait:     leaseGrace,
	}, nil
}

func (s *Store) Root() string {
	return s.root
}

// Reserve atomically checks duplicate work and global developer capacity,
// creates the pending run state, and returns the lease that makes this process
// the run's only owner. The advisory reservation lock serializes this short
// check/create critical section across Yoyodyne processes and is released by
// the operating system if a process exits unexpectedly.
func (s *Store) Reserve(ctx context.Context, state State, maxConcurrent int) (*Lease, error) {
	if maxConcurrent < 1 {
		return nil, errors.New("max concurrent developers must be greater than zero")
	}
	if state.Status != StatusPending {
		return nil, fmt.Errorf("reserved run state must be pending, got %q", state.Status)
	}
	state, err := state.withRecordedTextsBounded()
	if err != nil {
		return nil, err
	}
	if err := s.validateState(state); err != nil {
		return nil, err
	}
	release, err := s.lockReservations(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	active, err := s.Incomplete()
	if err != nil {
		return nil, fmt.Errorf("discover incomplete runs while reserving: %w", err)
	}
	for _, existing := range active {
		if existing.WorkItemID == state.WorkItemID {
			return nil, ExistingWorkItemError{State: existing}
		}
	}
	// A run paused on work its item waits on is in flight for the duplicate check
	// above and holds no slot here: see State.HoldsDeveloperSlot.
	if holding := HoldingDeveloperSlots(active); holding >= maxConcurrent {
		return nil, CapacityError{Limit: maxConcurrent, Active: holding}
	}
	// The lease is taken before the state exists, so no run is ever recorded as
	// in flight without an owner holding it.
	lease, held, err := s.takeLease(ctx, state.RunID)
	if err != nil {
		return nil, err
	}
	if !held {
		return nil, fmt.Errorf("run %s is already held by another process", state.RunID)
	}
	if err := s.Create(state); err != nil {
		return nil, errors.Join(fmt.Errorf("create reserved run state: %w", err), lease.Release())
	}
	return lease, nil
}

// HoldingDeveloperSlots counts the runs among states that hold a developer slot,
// which is what the configured limit is measured against.
func HoldingDeveloperSlots(states []State) int {
	holding := 0
	for _, state := range states {
		if state.HoldsDeveloperSlot() {
			holding++
		}
	}
	return holding
}

// ReclaimSlot is how a run paused on work its item waits on takes a developer
// slot again before it continues. It is the reservation's capacity check made
// for a run that already exists: under the same lock, against the same limit,
// counting every other run that holds a slot, and it refuses with the same
// CapacityError a fresh reservation does, so a continuation waits for a slot
// exactly as a fresh pull does. On success the pause is cleared and saved in the
// same critical section, which is what makes the slot this run's rather than one
// a reservation beside it could also have counted free. A refusal changes
// nothing: the run stays paused and holds no slot.
//
// The caller holds the run's lease, as it does for every write to the run.
func (s *Store) ReclaimSlot(ctx context.Context, state State, maxConcurrent int) (State, error) {
	if maxConcurrent < 1 {
		return state, errors.New("max concurrent developers must be greater than zero")
	}
	if state.DependencyPause == nil {
		return state, nil
	}
	release, err := s.lockReservations(ctx)
	if err != nil {
		return state, err
	}
	defer release()

	active, err := s.Incomplete()
	if err != nil {
		return state, fmt.Errorf("discover incomplete runs while reclaiming a developer slot: %w", err)
	}
	holding := 0
	for _, existing := range active {
		if existing.RunID != state.RunID && existing.HoldsDeveloperSlot() {
			holding++
		}
	}
	if holding >= maxConcurrent {
		return state, CapacityError{Limit: maxConcurrent, Active: holding}
	}
	reclaimed := state
	reclaimed.DependencyPause = nil
	if err := s.Save(reclaimed); err != nil {
		return state, fmt.Errorf("clear the dependency pause: %w", err)
	}
	return reclaimed, nil
}

// Adopt takes exclusive ownership of the run already in flight for a work item,
// which is how a second process enters a run it did not start. It is the resume
// counterpart of Reserve and refuses the same thing Reserve refuses: two
// processes acting on one item at once. A run whose lease is already held
// belongs to a live holder and is reported as an existing run rather than
// picked up.
func (s *Store) Adopt(ctx context.Context, workItemID string) (State, *Lease, error) {
	if strings.TrimSpace(workItemID) == "" {
		return State{}, nil, errors.New("work item id is required")
	}
	release, err := s.lockReservations(ctx)
	if err != nil {
		return State{}, nil, err
	}
	defer release()

	active, err := s.Incomplete()
	if err != nil {
		return State{}, nil, fmt.Errorf("discover incomplete runs while adopting: %w", err)
	}
	for _, existing := range active {
		if existing.WorkItemID != workItemID {
			continue
		}
		lease, held, err := s.takeLease(ctx, existing.RunID)
		if err != nil {
			return State{}, nil, err
		}
		if !held {
			return State{}, nil, ExistingWorkItemError{State: existing}
		}
		// The state is re-read under the lease, because only now is this
		// process the one entitled to act on what it reads.
		state, err := s.Load(existing.RunID)
		if err != nil {
			return State{}, nil, errors.Join(err, lease.Release())
		}
		return state, lease, nil
	}
	return State{}, nil, ErrNoRunInFlight
}

// AdoptRun takes exclusive ownership of one recorded run by identifier. It is
// how reconciliation enters a run that no work item lookup would find: a run
// that already reached a terminal status but still owes cleanup is not in
// flight, yet it must have exactly one owner for as long as anybody acts on it.
// A run whose lease a live holder already owns is reported as held rather than
// picked up.
func (s *Store) AdoptRun(ctx context.Context, runID string) (State, *Lease, error) {
	release, err := s.lockReservations(ctx)
	if err != nil {
		return State{}, nil, err
	}
	defer release()

	lease, held, err := s.takeLease(ctx, runID)
	if err != nil {
		return State{}, nil, err
	}
	if !held {
		return State{}, nil, ErrRunHeld
	}
	// As in Adopt, the state is re-read under the lease, because only now is
	// this process the one entitled to act on what it reads.
	state, err := s.Load(runID)
	if err != nil {
		return State{}, nil, errors.Join(err, lease.Release())
	}
	return state, lease, nil
}

// lockReservations serializes the reservation and adoption critical sections
// against every other Yoyodyne process.
func (s *Store) lockReservations(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create run state directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(s.root, ".reservation.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open run reservation lock: %w", err)
	}
	if err := lockStateFile(ctx, lock); err != nil {
		lock.Close()
		return nil, fmt.Errorf("lock run reservations: %w", err)
	}
	return func() { _ = releaseStateFile(lock) }, nil
}

// takeLease tries to become the owner of one run, reporting whether it did. The
// lease file outlives the run: it is the name the lock is taken on, and removing
// it while another process holds it would let a third take a lock on a file
// nobody else can see.
func (s *Store) takeLease(ctx context.Context, runID string) (*Lease, bool, error) {
	path, err := s.leasePath(runID)
	if err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open run lease: %w", err)
	}
	held, err := acquireLease(ctx, file, s.leaseWait, s.leaseHeld)
	if err != nil {
		file.Close()
		return nil, false, fmt.Errorf("lock run %s: %w", runID, err)
	}
	if !held {
		file.Close()
		return nil, false, nil
	}
	lease := &Lease{label: "run", file: file}
	// The holder is stamped under the lock, so a surface that only reads can say
	// whether a process is behind the run without taking the lease to find out.
	// A lease that cannot be stamped is refused rather than taken: what it would
	// buy is a run every surface reports as having no process while one works on
	// it.
	holder, err := s.holderPath(runID)
	if err != nil {
		return nil, false, errors.Join(err, lease.Release())
	}
	if err := s.stampRunHolder(holder); err != nil {
		return nil, false, errors.Join(err, lease.Release())
	}
	lease.holder = holder
	return lease, true, nil
}

// acquireLease takes the exclusive lock on an open lease file, retrying within
// the grace before it concludes that a live holder owns the run. A lock that is
// still held after the grace belongs to somebody, so the caller is told so
// rather than made to wait for them. waiting, where it is set, is told each time
// an attempt finds the lock held.
func acquireLease(ctx context.Context, file *os.File, grace time.Duration, waiting func()) (bool, error) {
	deadline := time.Now().Add(grace)
	for {
		held, err := tryLockStateFile(file)
		if err != nil || held {
			return held, err
		}
		if waiting != nil {
			waiting()
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(leaseGracePoll):
		}
	}
}

func (s *Store) Create(state State) error {
	// Every free-text field is cut to its bound on the way in rather than the
	// record refused for it; see boundRecordedTexts.
	state, err := state.withRecordedTextsBounded()
	if err != nil {
		return err
	}
	if err := s.validateState(state); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create run state directory: %w", err)
	}
	path, err := s.statePath(state.RunID)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("run %s already exists", state.RunID)
		}
		return fmt.Errorf("create run state: %w", err)
	}
	if err := writeJSONFile(file, "run state", state); err != nil {
		return cleanupFailedCreate(file, path, err)
	}
	if err := file.Close(); err != nil {
		return cleanupFailedCreate(nil, path, fmt.Errorf("close run state: %w", err))
	}
	if err := syncDirectory(s.root); err != nil {
		return cleanupFailedCreate(nil, path, err)
	}
	return nil
}

// Save replaces a run's whole record. Under the run's write lock it compares
// the routing the caller holds with the routing stored, and refuses the write
// if they differ: routing changes only through UpdateRouting, so a difference
// is either a copy read before a routing change or an attempt to change
// routing here, and writing either would lose or forge it. Every other field is
// the caller's, as it always was.
func (s *Store) Save(state State) error {
	state, err := state.withRecordedTextsBounded()
	if err != nil {
		return err
	}
	if err := s.validateState(state); err != nil {
		return err
	}
	unlock, err := s.lockRunWrites(context.Background(), state.RunID)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.checkRoutingWrite(state); err != nil {
		return err
	}
	return s.writeState(state)
}

// writeState durably replaces a run record that exists. Its callers hold the
// run's write lock and have already validated the record.
func (s *Store) writeState(state State) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create run state directory: %w", err)
	}
	path, err := s.statePath(state.RunID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("load existing run state before save: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary run state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary run state: %w", err)
	}
	if err := writeJSONFile(temporary, "run state", state); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary run state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace run state: %w", err)
	}
	return syncDirectory(s.root)
}

// Load reads one run record for a caller that is about to act on it, which is
// the strict door: a field this build does not know refuses the record rather
// than being stepped over, because the caller is going to save the record back
// and a field stepped over here is a field lost there. See tolerantread.go for
// the other door and why the listings go through it.
func (s *Store) Load(runID string) (State, error) {
	return s.load(runID, false)
}

// Read reads one run record for a reader that will not write it back — a
// listing, or a surface that fetches one run by id to say something about it —
// which is the tolerant door: a field this build does not know is stepped over
// and named rather than refusing the whole record. Nothing decided from it is
// written back — every caller that acts re-reads through Load, under the run's
// lease — so the field this steps over is one nothing here could have saved
// anyway.
func (s *Store) Read(runID string) (State, error) {
	return s.load(runID, true)
}

func (s *Store) load(runID string, tolerateUnknownFields bool) (State, error) {
	path, err := s.statePath(runID)
	if err != nil {
		return State{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return State{}, fmt.Errorf("open run state: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return State{}, fmt.Errorf("stat run state: %w", err)
	}
	if info.Size() > maxEncodedStateBytes {
		return State{}, fmt.Errorf("run state %s is %d bytes, limit is %d", runID, info.Size(), maxEncodedStateBytes)
	}
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return State{}, fmt.Errorf("read run state %s: %w", runID, err)
	}
	var state State
	if tolerateUnknownFields {
		unknown, err := decodeTolerating(encoded, &state)
		if err != nil {
			return State{}, fmt.Errorf("decode run state %s: %w", runID, err)
		}
		noteUnknownFields("run record", unknown)
	} else if err := decodeStrictly(encoded, &state); err != nil {
		return State{}, fmt.Errorf("decode run state %s: %w", runID, err)
	} else if state.Routing != nil && state.Routing.Version > RoutingVersion {
		// A caller of the strict door is about to act on the run, and cannot act
		// on routing it does not understand.
		return State{}, fmt.Errorf("%w: run %s has routing version %d and this build reads %d", ErrUnsupportedRouting, runID, state.Routing.Version, RoutingVersion)
	}
	if state.RunID != runID {
		return State{}, fmt.Errorf("run state file %s belongs to run %s", runID, state.RunID)
	}
	// A record written before one of the schema's text bounds existed can hold a
	// field longer than Validate now accepts. It is cut here rather than refused
	// below, because a bound the harness added is not a reason for a run somebody
	// recorded to stop being readable — and the loader is walked by every scan, so
	// refusing one old file is a whole history nobody can list.
	state.boundRecordedTexts()
	if err := s.validateState(state); err != nil {
		return State{}, err
	}
	return state, nil
}

// Incomplete lists every run still in flight, in the one sense Status.InFlight
// gives that word. It is what the status surface counts as running and what the
// scheduler reads for the slots and epics already taken, so the predicate is the
// status's own rather than this listing's.
func (s *Store) Incomplete() ([]State, error) {
	return s.scan("incomplete", func(state State) bool { return state.Status.InFlight() })
}

// Outstanding lists every recorded run that still owes a step. That is a
// superset of the incomplete runs: a run can be durably terminal and still owe
// the cleanup its integration scheduled, which is exactly what an interrupted
// process leaves behind between integrating and cleaning up.
func (s *Store) Outstanding() ([]State, error) {
	return s.scan("outstanding", func(state State) bool { return state.Outstanding() })
}

// Recorded lists every run the harness holds for this product, whatever became
// of them. It is for a reader that has to notice change rather than act on it —
// the reporting sink is the first — and it decides nothing about what it reads:
// a run a live process owns is listed exactly as a finished one is.
func (s *Store) Recorded() ([]State, error) {
	return s.scan("recorded", func(State) bool { return true })
}

// Runs lists every run recorded for one work item, most recently started first,
// whatever became of each. It is how somebody asks what the harness has done to
// a piece of work without holding, adopting, or otherwise deciding anything
// about any of it: reading a record is not acting on it, so a run a live process
// owns is as readable here as one that finished months ago.
//
// The order is the point rather than a convenience. What is true of a work
// item's change now is decided by the most recent run that says anything about
// it, and that is not always the most recent run — a re-run that produced
// nothing says nothing, and what stands is what the run before it left. A caller
// walking the item's history has to walk it in one agreed direction, and runs
// are identified by a random string, so a caller sorting them itself would be a
// second definition of "most recent" that could come to disagree with this one.
// Runs started at the same instant are ordered by identifier, so two readings of
// one directory never come back in different orders.
func (s *Store) Runs(workItemID string) ([]State, error) {
	id := strings.TrimSpace(workItemID)
	if id == "" {
		return nil, errors.New("a work item is required to find its runs")
	}
	states, err := s.scan("recorded", func(state State) bool { return state.WorkItemID == id })
	if err != nil {
		return nil, err
	}
	NewestFirst(states)
	return states, nil
}

// NewestFirst puts runs in the order Runs lists them in, in place: most recently
// started first, and by identifier where two started at the same instant. It is
// exported so that a reader holding every recorded run walks one item's history
// in the one agreed direction rather than a second definition of "most recent".
func NewestFirst(states []State) {
	sort.SliceStable(states, func(i, j int) bool {
		if states[i].StartedAt.Equal(states[j].StartedAt) {
			return states[i].RunID < states[j].RunID
		}
		return states[i].StartedAt.After(states[j].StartedAt)
	})
}

// Unlanded is the run holding one item's change that never reached the
// integration target, from that item's runs listed newest first, and whether
// there is one at all. It is the substrate a child carved out of the item is
// written against.
//
// It walks the runs rather than reading the newest, because the newest run is
// not always the run that says where the change is. A recorded integration is
// the promotion itself and ends the walk: the change is on the target branch,
// and nothing an earlier run did is missing any more. A run still going has not
// failed to land anything yet, so the walk carries on past it. A terminal run
// with no recorded change and no commit produced nothing, so it says nothing
// about where the work is, and the walk carries on past that too. What is left
// is a terminal run that made a change and did not promote it, which is the
// answer. An item whose every run falls through has nothing off the target
// branch.
func Unlanded(newestFirst []State) (State, bool) {
	for _, state := range newestFirst {
		if state.Integration != nil {
			return State{}, false
		}
		if !state.Status.Terminal() || (state.Changes == nil && state.HarnessCommit == "") {
			continue
		}
		return state, true
	}
	return State{}, false
}

// Latest reports the most recently started run recorded for one work item,
// whatever became of it. It is the first of the runs above, and is stated as its
// own question because most callers have exactly that question: what did the
// harness last do to this work.
func (s *Store) Latest(workItemID string) (State, error) {
	states, err := s.Runs(workItemID)
	if err != nil {
		return State{}, err
	}
	if len(states) == 0 {
		return State{}, fmt.Errorf("%w: %s", ErrNoRecordedRun, strings.TrimSpace(workItemID))
	}
	return states[0], nil
}

// ReviewRounds counts the review rounds one work item has accumulated across
// every run ever made for it. It is read from the records rather than carried
// on the item because that is what makes it a total: a run is one attempt at
// the item, and what triage measures against its configured cap is how many
// times the work has been round with a reviewer whichever run did the going.
//
// Reading records decides nothing about them, so a run a live process owns is
// counted here exactly as a finished one is — a cap read while the current run
// is still arguing with its reviewer should include that argument.
func (s *Store) ReviewRounds(workItemID string) (int, error) {
	id := strings.TrimSpace(workItemID)
	if id == "" {
		return 0, errors.New("a work item is required to count its review rounds")
	}
	states, err := s.scan("recorded", func(state State) bool { return state.WorkItemID == id })
	if err != nil {
		return 0, err
	}
	rounds := 0
	for _, state := range states {
		rounds += state.ReviewRounds
	}
	return rounds, nil
}

// ErrNoRecordedRun reports a work item the harness has never run, which is a
// plain answer rather than a failure to look.
var ErrNoRecordedRun = errors.New("no run of this work item is recorded")

// Unreadable is one record a listing read past because it would not decode: the
// record it is, named as its file is, and what refused it.
type Unreadable struct {
	Record string
	Err    error
}

// RecordedReadable lists every run Recorded does, except that a record which
// will not decode is read past and returned beside the listing rather than
// refusing all of it. It is for a reader that reports on every run independently
// — the reporting sink — where one run nobody can read is a reason to say
// nothing about that run, not about every other run as well: the sink once held
// every stream's cursor still for as long as five records a newer build wrote
// stood in the directory. A caller that decides anything across the whole set
// wants Recorded, whose refusal says the set is incomplete.
func (s *Store) RecordedReadable() ([]State, []Unreadable, error) {
	return s.scanning("recorded", func(State) bool { return true }, true)
}

func (s *Store) scan(label string, keep func(State) bool) ([]State, error) {
	states, _, err := s.scanning(label, keep, false)
	return states, err
}

func (s *Store) scanning(label string, keep func(State) bool, readPast bool) ([]State, []Unreadable, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read run state directory: %w", err)
	}
	states := make([]State, 0)
	var unreadable []Unreadable
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		runID := strings.TrimSuffix(entry.Name(), ".json")
		// Records that belong to a run rather than being one live beside it in
		// this directory — the operator's release of a usage-limit wait is one —
		// and only a file named for a run holds a run.
		if !runIDPattern.MatchString(runID) {
			continue
		}
		state, err := s.Read(runID)
		if err != nil && readPast {
			unreadable = append(unreadable, Unreadable{Record: runID, Err: err})
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("discover %s runs: %w", label, err)
		}
		if keep(state) {
			states = append(states, state)
		}
	}
	sort.Slice(states, func(i, j int) bool {
		return states[i].RunID < states[j].RunID
	})
	return states, unreadable, nil
}

func (s *Store) AppendEvent(event execution.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if _, err := s.Load(event.RunID); err != nil {
		return fmt.Errorf("load run before appending event: %w", err)
	}
	encoded, err := encodeEvent(event)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedEventBytes {
		return StopError{Class: StopEventBound, Cause: fmt.Errorf("encoded event is %d bytes, limit is %d", len(encoded), maxEncodedEventBytes)}
	}
	path, err := s.eventPath(event.RunID)
	if err != nil {
		return err
	}
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect event log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append event: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append event: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync event log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close event log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

func (s *Store) LoadEvents(runID string) ([]execution.Event, error) {
	path, err := s.eventPath(runID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	defer file.Close()

	var events []execution.Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedEventBytes)
	for scanner.Scan() {
		event, err := execution.DecodeEvent(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("decode event log for %s: %w", runID, err)
		}
		if event.RunID != runID {
			return nil, fmt.Errorf("decode event log for %s: event belongs to run %s", runID, event.RunID)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read event log: %w", err)
	}
	return events, nil
}

func encodeEvent(event execution.Event) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(event); err != nil {
		return nil, fmt.Errorf("encode event: %w", err)
	}
	return buffer.Bytes(), nil
}

// RefusedStateError is a run state this store will not take: one the durable
// schema rejects, or one belonging to another product.
//
// It is distinguished from every other reason a save can fail because it is the
// only one that will still be true next time. A store that was unavailable is
// retried by whoever resumes the run, and what is on disk meanwhile is an
// interrupted run, which is what it is. A state the schema refuses is refused for
// as long as the process holds it: no later write closes the gap, so the record
// stays behind what the process believes and every reader after that process
// exits believes the record. A caller that gets this back has to end its run over
// what is actually stored rather than leave it looking resumable.
//
// It carries the same words the refusal itself does, because the field it names
// is the whole of what a caller has to act on.
type RefusedStateError struct {
	Problem error
}

func (e RefusedStateError) Error() string { return e.Problem.Error() }

func (e RefusedStateError) Unwrap() error { return e.Problem }

func (s *Store) validateState(state State) error {
	if state.ProductID != s.productID {
		return RefusedStateError{Problem: fmt.Errorf("run product %q does not match store product %q", state.ProductID, s.productID)}
	}
	if err := state.Validate(); err != nil {
		return RefusedStateError{Problem: err}
	}
	return nil
}

func (s *Store) statePath(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errors.New("run id is invalid")
	}
	return filepath.Join(s.root, runID+".json"), nil
}

func (s *Store) eventPath(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errors.New("run id is invalid")
	}
	return filepath.Join(s.root, runID+".events.jsonl"), nil
}

func (s *Store) leasePath(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errors.New("run id is invalid")
	}
	return filepath.Join(s.root, runID+".lease"), nil
}

// releasePath names where an operator's release of one run's usage-limit wait
// is recorded. It sits beside the run's own state rather than inside it because
// the waiting process holds that state's lease and the operator does not.
func (s *Store) releasePath(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errors.New("run id is invalid")
	}
	return filepath.Join(s.root, runID+".release.json"), nil
}

// writeJSONFile durably writes one record. The label names what is being
// written, because runs and conversations share this path and an operator
// reading a failure has to know which record could not be stored.
func writeJSONFile(file *os.File, label string, value any) error {
	encoded, err := encodeRecord(label, value)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedStateBytes {
		return StopError{Class: StopStateBound, Cause: fmt.Errorf("encoded %s is %d bytes, limit is %d", label, len(encoded), maxEncodedStateBytes)}
	}
	written, err := file.Write(encoded)
	if err != nil {
		return fmt.Errorf("write %s: %w", label, err)
	}
	if written != len(encoded) {
		return fmt.Errorf("write %s: %w", label, io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", label, err)
	}
	return nil
}

// encodeRecord is the bytes one record is stored as. It is apart from the write
// so that a caller can ask what a record would take before it produces one, and
// so that the answer is the same bytes the write would put on the disk rather
// than an estimate of them.
func encodeRecord(label string, value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode %s: %w", label, err)
	}
	return buffer.Bytes(), nil
}

// createJSONFile durably writes one record that must not exist yet, and
// replaceJSONFile durably replaces one that does. They are the two doors every
// record in this package that is not a log goes through, written here rather
// than once per record so that what "durable" means — written whole, put on the
// disk, and named by a rename that a reader either sees or does not — is one
// answer this package gives rather than each writer's own.
//
// Both take the directory as well as the path, because both create it and both
// sync it: a record whose name is in a directory the operating system has agreed
// to write later is a record a crash loses along with the name.
func createJSONFile(directory, path, label string, value any) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create the directory for %s: %w", label, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create %s: %s already exists", label, filepath.Base(path))
		}
		return fmt.Errorf("create %s: %w", label, err)
	}
	if err := writeJSONFile(file, label, value); err != nil {
		return cleanupFailedCreate(file, path, err)
	}
	if err := file.Close(); err != nil {
		return cleanupFailedCreate(nil, path, fmt.Errorf("close %s: %w", label, err))
	}
	if err := syncDirectory(directory); err != nil {
		return cleanupFailedCreate(nil, path, err)
	}
	return nil
}

func replaceJSONFile(directory, path, label string, value any) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create the directory for %s: %w", label, err)
	}
	temporary, err := os.CreateTemp(directory, ".record-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", label, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("secure temporary %s: %w", label, err), temporary.Close())
	}
	if err := writeJSONFile(temporary, label, value); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary %s: %w", label, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", label, err)
	}
	return syncDirectory(directory)
}

func cleanupFailedCreate(file *os.File, path string, cause error) error {
	var cleanupErrors []error
	if file != nil {
		if err := file.Close(); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("close failed run state: %w", err))
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove failed run state: %w", err))
	}
	if len(cleanupErrors) == 0 {
		return cause
	}
	return errors.Join(cause, errors.Join(cleanupErrors...))
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not supported")
		}
		return err
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open state directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	return nil
}
