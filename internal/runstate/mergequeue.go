package runstate

// The merge queue's durable half: which approved changes wait to integrate into
// one target branch, in what order, and which process is the one that works the
// queue. docs/designs/integration-through-a-merge-queue.md is the design; this
// file is its "Durable admission and ownership" section, and
// mergequeuegeneration.go beside it keeps what the worker verified. Nothing
// here selects an entry, builds a candidate, runs a check, or moves a branch,
// and nothing in the harness admits to the queue yet.
//
// One queue exists per repository and target branch, as one record under the
// product. Admission is a short critical section under the record's lock: read
// the record, find the run's entry or assign the next order, write the record
// whole and synced, and read it back if the write could not say whether it
// landed. The order a queue has assigned is never assigned again, and an entry
// once written is never rewritten, so a reader that saw an entry sees the same
// entry later.
//
// Working the queue is a lease beside the record, for the same reason every
// lease in this package is a file lock: the operating system drops it when its
// holder exits, so a worker that died leaves no owner behind. It is deliberately
// not the promotion lease. The worker holds its lease for as long as it works
// the queue, through checks and review; the promotion lease covers one short
// step of that work, and a worker holding it throughout would hold every other
// promotion into the branch out for the length of a review.
//
// Neither a queued entry nor the worker's lease is a run, so neither holds a
// developer slot: the slot count is taken over runs alone (see
// HoldingDeveloperSlots), and nothing here creates one.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// MergeQueueSchemaVersion is 1 and has never changed.
const MergeQueueSchemaVersion = 1

// MaxMergeQueueTextBytes bounds each text an admission records. They are
// identities and one-line names, so a value past this is not one of them.
const MaxMergeQueueTextBytes = 2 << 10

const (
	mergeQueuesDirectory  = "merge-queues"
	mergeQueueRecordName  = "queue.json"
	mergeQueueRecordLock  = "queue.lock"
	mergeQueueWorkerLease = "worker.lock"
	mergeQueueWorkerLabel = "merge queue worker"
	// mergeQueueRecordWait bounds the wait for the record's lock. What holds it
	// is another process's read, one small write, and a readback, so the bound
	// is there for a holder that is wedged rather than for one that is working.
	mergeQueueRecordWait = 30 * time.Second
)

var mergeQueueEntryIDPattern = regexp.MustCompile(`^mqe-[a-f0-9]{32}$`)

// MergeQueueMode is how an admitted entry is to be integrated, chosen before
// admission and recorded with it, because draining and transfer read the mode
// an entry was admitted in rather than the one configured now.
type MergeQueueMode string

const (
	// MergeQueueHarness is the queue the harness runs itself, on any forge.
	MergeQueueHarness MergeQueueMode = "harness"
	// MergeQueueForge is the forge's own queue, chosen only where its adapter
	// enforces the same gate the harness's queue does.
	MergeQueueForge MergeQueueMode = "forge"
)

func (m MergeQueueMode) valid() bool {
	return m == MergeQueueHarness || m == MergeQueueForge
}

// MergeQueueKey names one queue: a repository and a target branch in it.
// Repository is the repository a run records itself against
// (State.RepositoryID), compared exactly.
type MergeQueueKey struct {
	Repository   string `json:"repository"`
	TargetBranch string `json:"target_branch"`
}

func (k MergeQueueKey) validate() error {
	var problems []error
	if err := mergeQueueText("repository", k.Repository, true); err != nil {
		problems = append(problems, err)
	}
	if !validLocalBranch(k.TargetBranch) || strings.TrimSpace(k.TargetBranch) != k.TargetBranch {
		problems = append(problems, fmt.Errorf("target branch %q is not a local branch name", k.TargetBranch))
	}
	return errors.Join(problems...)
}

// directory is the queue's directory under the product's merge queues. The
// key is digested rather than encoded because a repository name is anything
// at all; the readable key is in the record, which a digest collision would
// be refused by.
func (k MergeQueueKey) directory() string {
	sum := sha256.Sum256([]byte(k.Repository + "\x00" + k.TargetBranch))
	return hex.EncodeToString(sum[:16])
}

// MergeQueueAdmission is what a caller admits: one approved change, by the run
// that made it and the head that was approved.
type MergeQueueAdmission struct {
	Key           MergeQueueKey
	WorkItemID    string
	WorkItemTitle string
	RunID         string
	// Publication is the publication the change was approved under, such as
	// its pull request's address. It is empty for a project that does not
	// publish.
	Publication  string
	ApprovedHead string
	// IntegrationPolicy names the integration policy the change was approved
	// under, which admission records and never interprets.
	IntegrationPolicy string
	Mode              MergeQueueMode
	// At is when the change was admitted; zero means now.
	At time.Time
}

// MergeQueueEntry is one admitted change. Every field is fixed when it is
// admitted and none is revised afterwards.
type MergeQueueEntry struct {
	EntryID string `json:"entry_id"`
	// Order is the entry's place in its queue, from 1, assigned once and never
	// assigned again in that queue.
	Order             uint64           `json:"order"`
	ProductID         domain.ProductID `json:"product_id"`
	Repository        string           `json:"repository"`
	TargetBranch      string           `json:"target_branch"`
	WorkItemID        string           `json:"work_item_id"`
	WorkItemTitle     string           `json:"work_item_title"`
	RunID             string           `json:"run_id"`
	Publication       string           `json:"publication,omitempty"`
	ApprovedHead      string           `json:"approved_head"`
	IntegrationPolicy string           `json:"integration_policy"`
	Mode              MergeQueueMode   `json:"mode"`
	AdmittedAt        time.Time        `json:"admitted_at"`
	// PredecessorOrder is the order of the entry admitted immediately before
	// this one, and zero for a queue's first.
	PredecessorOrder uint64 `json:"predecessor_order"`
}

// Key is the queue the entry was admitted to.
func (e MergeQueueEntry) Key() MergeQueueKey {
	return MergeQueueKey{Repository: e.Repository, TargetBranch: e.TargetBranch}
}

func (e MergeQueueEntry) validate() error {
	var problems []error
	if !mergeQueueEntryIDPattern.MatchString(e.EntryID) {
		problems = append(problems, fmt.Errorf("entry id %q is not a merge queue entry id", e.EntryID))
	}
	if e.Order == 0 {
		problems = append(problems, errors.New("order starts at 1"))
	}
	if e.PredecessorOrder >= e.Order {
		problems = append(problems, fmt.Errorf("predecessor order %d is not before order %d", e.PredecessorOrder, e.Order))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := e.Key().validate(); err != nil {
		problems = append(problems, err)
	}
	problems = append(problems, mergeQueueContents(e.WorkItemID, e.WorkItemTitle, e.RunID, e.Publication, e.ApprovedHead, e.IntegrationPolicy, e.Mode)...)
	if e.AdmittedAt.IsZero() {
		problems = append(problems, errors.New("admission time is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("merge queue entry %d: %w", e.Order, err)
	}
	return nil
}

// sameContents reports whether an entry records exactly what an admission
// asks for. The identity, order, predecessor, and time are the queue's, not
// the caller's, so a retry is the same admission whatever they are.
func (e MergeQueueEntry) sameContents(other MergeQueueEntry) bool {
	return e.ProductID == other.ProductID && e.Repository == other.Repository && e.TargetBranch == other.TargetBranch &&
		e.WorkItemID == other.WorkItemID && e.WorkItemTitle == other.WorkItemTitle && e.RunID == other.RunID &&
		e.Publication == other.Publication && e.ApprovedHead == other.ApprovedHead &&
		e.IntegrationPolicy == other.IntegrationPolicy && e.Mode == other.Mode
}

// MergeQueue is one queue's record: every entry admitted to it, in order, and
// the order the next admission is assigned.
type MergeQueue struct {
	SchemaVersion int               `json:"schema_version"`
	ProductID     domain.ProductID  `json:"product_id"`
	Repository    string            `json:"repository"`
	TargetBranch  string            `json:"target_branch"`
	NextOrder     uint64            `json:"next_order"`
	Entries       []MergeQueueEntry `json:"entries"`
}

func (q MergeQueue) validate(productID domain.ProductID, key MergeQueueKey) error {
	var problems []error
	if q.SchemaVersion != MergeQueueSchemaVersion {
		problems = append(problems, fmt.Errorf("merge queue schema version %d is not supported", q.SchemaVersion))
	}
	if q.ProductID != productID {
		problems = append(problems, fmt.Errorf("merge queue belongs to product %q, not %q", q.ProductID, productID))
	}
	if q.Repository != key.Repository || q.TargetBranch != key.TargetBranch {
		problems = append(problems, fmt.Errorf("merge queue record is for %s on %q, not %s on %q", q.TargetBranch, q.Repository, key.TargetBranch, key.Repository))
	}
	var previous uint64
	ids := make(map[string]bool, len(q.Entries))
	runs := make(map[string]bool, len(q.Entries))
	for _, entry := range q.Entries {
		if err := entry.validate(); err != nil {
			problems = append(problems, err)
			continue
		}
		if entry.ProductID != q.ProductID || entry.Key() != key {
			problems = append(problems, fmt.Errorf("merge queue entry %d belongs to another queue", entry.Order))
		}
		if entry.Order <= previous || entry.PredecessorOrder != previous {
			problems = append(problems, fmt.Errorf("merge queue entry %d is out of order after %d", entry.Order, previous))
		}
		if ids[entry.EntryID] {
			problems = append(problems, fmt.Errorf("merge queue entry id %s is recorded twice", entry.EntryID))
		}
		if runs[entry.RunID] {
			problems = append(problems, fmt.Errorf("run %s is admitted twice", entry.RunID))
		}
		ids[entry.EntryID], runs[entry.RunID] = true, true
		previous = entry.Order
	}
	if q.NextOrder <= previous || q.NextOrder == 0 {
		problems = append(problems, fmt.Errorf("next order %d is not after the last order assigned, %d", q.NextOrder, previous))
	}
	return errors.Join(problems...)
}

// MergeQueueConflictError is an admission refused because the queue already
// holds the run with different contents. An admitted entry is never rewritten,
// so the earlier entry stands and the caller is told what it records.
type MergeQueueConflictError struct {
	Admitted MergeQueueEntry
}

func (e MergeQueueConflictError) Error() string {
	return fmt.Sprintf("run %s is already admitted to the merge queue for %s as entry %d at head %s, with different contents; an admitted entry is never rewritten",
		e.Admitted.RunID, e.Admitted.TargetBranch, e.Admitted.Order, e.Admitted.ApprovedHead)
}

// ErrMergeQueueSaveUncertain is an admission whose write failed in a way that
// does not say whether it landed, and whose readback could not settle it
// either. Retrying the admission is safe, because it reads the record before
// it writes anything, and is the only way to learn which it was.
var ErrMergeQueueSaveUncertain = errors.New("the merge queue admission may or may not have been saved, and reading it back did not say")

// MergeQueueAdmitter is what admits approved changes to their queue and reads
// a queue back. Later slices of the merge queue are given this rather than the
// store, so a caller that admits cannot reach the worker's lease.
type MergeQueueAdmitter interface {
	Admit(ctx context.Context, admission MergeQueueAdmission) (MergeQueueEntry, bool, error)
	Entries(key MergeQueueKey) ([]MergeQueueEntry, error)
}

// MergeQueueWorkerLeaser is what takes the one lease that makes a process the
// worker for a queue.
type MergeQueueWorkerLeaser interface {
	LeaseWorker(ctx context.Context, key MergeQueueKey) (*Lease, bool, error)
}

var (
	_ MergeQueueAdmitter     = (*MergeQueueStore)(nil)
	_ MergeQueueWorkerLeaser = (*MergeQueueStore)(nil)
)

// MergeQueueStore keeps a product's merge queues, one directory per queue
// under the product, written through the pinned state root so a replaced
// directory cannot redirect a write.
type MergeQueueStore struct {
	stateRoot string
	anchor    string
	productID domain.ProductID
	// save writes a queue's record. It is a field only so a test can make a
	// write fail before or after it lands; every store the harness builds
	// writes through writeQueue.
	save func(queue *repowrite.PinnedRoot, encoded []byte) error
	// readback reads a queue's record after a save that failed. It is a field
	// for the same reason; every store the harness builds reads through
	// readQueue.
	readback func(queue *repowrite.PinnedRoot) ([]byte, error)
	// saveGeneration and readGenerationBack are the same two seams for an
	// entry's generations record; nil is the ordinary write and read.
	saveGeneration     func(queue *repowrite.PinnedRoot, name string, encoded []byte) error
	readGenerationBack func(queue *repowrite.PinnedRoot, name string) ([]byte, error)
	// leaseWait is the grace a worker lease that looks held is waited out for,
	// as a run's is; see leaseGrace.
	leaseWait time.Duration
}

func NewMergeQueueStore(root string, productID domain.ProductID) (*MergeQueueStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the merge queue state root: %w", err)
	}
	return &MergeQueueStore{
		stateRoot: stateRoot, anchor: anchor, productID: productID,
		save: writeQueue, readback: readQueue, leaseWait: leaseGrace,
	}, nil
}

// Root is the directory every queue of the product is kept under.
func (s *MergeQueueStore) Root() string {
	return filepath.Join(s.stateRoot, s.directory())
}

func (s *MergeQueueStore) directory() string {
	return filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), mergeQueuesDirectory)
}

// Admit admits one approved change to its queue, and reports the entry the
// queue holds for it and whether this call is what admitted it.
//
// Admitting a run the queue already holds at the same head with the same
// contents is not a second admission: it reports the entry already there,
// with its order, and assigns nothing. A run held with any other contents —
// another head among them — is refused with MergeQueueConflictError, because
// an admitted entry is never rewritten.
//
// A write that fails is read back before Admit answers. The entry found there
// is admitted; none found means nothing was, and the error says why; and a
// readback that fails as well is ErrMergeQueueSaveUncertain, which a retry
// settles.
func (s *MergeQueueStore) Admit(ctx context.Context, admission MergeQueueAdmission) (MergeQueueEntry, bool, error) {
	at := admission.At
	if at.IsZero() {
		at = time.Now()
	}
	asked := MergeQueueEntry{
		ProductID: s.productID, Repository: admission.Key.Repository, TargetBranch: admission.Key.TargetBranch,
		WorkItemID: admission.WorkItemID, WorkItemTitle: admission.WorkItemTitle, RunID: admission.RunID,
		Publication: admission.Publication, ApprovedHead: admission.ApprovedHead,
		IntegrationPolicy: admission.IntegrationPolicy, Mode: admission.Mode, AdmittedAt: at.UTC(),
	}
	if err := errors.Join(admission.Key.validate(), errors.Join(mergeQueueContents(asked.WorkItemID, asked.WorkItemTitle, asked.RunID, asked.Publication, asked.ApprovedHead, asked.IntegrationPolicy, asked.Mode)...)); err != nil {
		return MergeQueueEntry{}, false, fmt.Errorf("merge queue admission: %w", err)
	}
	queueRoot, unlock, err := s.lockQueue(ctx, admission.Key)
	if err != nil {
		return MergeQueueEntry{}, false, err
	}
	defer unlock()

	queue, err := s.load(queueRoot, admission.Key, true)
	if err != nil {
		return MergeQueueEntry{}, false, err
	}
	if existing, found := queue.entryForRun(asked.RunID); found {
		if !existing.sameContents(asked) {
			return MergeQueueEntry{}, false, MergeQueueConflictError{Admitted: existing}
		}
		return existing, false, nil
	}
	id, err := newMergeQueueEntryID()
	if err != nil {
		return MergeQueueEntry{}, false, err
	}
	asked.EntryID = id
	asked.Order = queue.NextOrder
	if count := len(queue.Entries); count > 0 {
		asked.PredecessorOrder = queue.Entries[count-1].Order
	}
	queue.Entries = append(queue.Entries, asked)
	queue.NextOrder = asked.Order + 1
	if err := queue.validate(s.productID, admission.Key); err != nil {
		return MergeQueueEntry{}, false, err
	}
	encoded, err := encodeRecord("merge queue", queue)
	if err != nil {
		return MergeQueueEntry{}, false, err
	}
	if len(encoded) > maxEncodedStateBytes {
		return MergeQueueEntry{}, false, StopError{Class: StopStateBound, Cause: fmt.Errorf("the merge queue for %s would be %d bytes, limit is %d", admission.Key.TargetBranch, len(encoded), maxEncodedStateBytes)}
	}
	saveErr := s.save(queueRoot, encoded)
	if saveErr == nil {
		return asked, true, nil
	}
	// Whether a failed write landed is what the record says, and nothing else
	// does: a rename can succeed and the directory sync behind it fail.
	landed, readErr := s.readBack(queueRoot, admission.Key)
	if readErr != nil {
		return MergeQueueEntry{}, false, fmt.Errorf("%w: save: %w; readback: %w", ErrMergeQueueSaveUncertain, saveErr, readErr)
	}
	if entry, found := landed.entryForRun(asked.RunID); found && entry.EntryID == asked.EntryID {
		return entry, true, nil
	}
	return MergeQueueEntry{}, false, fmt.Errorf("save the merge queue for %s: %w; reading it back found the admission was not saved", admission.Key.TargetBranch, saveErr)
}

// Entries reports every entry admitted to a queue, in order. A queue nothing
// has been admitted to has none; a record that cannot be read is an error,
// never an empty queue.
func (s *MergeQueueStore) Entries(key MergeQueueKey) ([]MergeQueueEntry, error) {
	if err := key.validate(); err != nil {
		return nil, fmt.Errorf("merge queue: %w", err)
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, fmt.Errorf("pin the merge queue state root: %w", err)
	}
	defer root.Close()
	queueRoot, err := root.OpenDirectory(path.Join(filepath.ToSlash(s.directory()), key.directory()))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the merge queue for %s: %w", key.TargetBranch, err)
	}
	defer queueRoot.Close()
	queue, err := s.load(queueRoot, key, false)
	if err != nil {
		return nil, err
	}
	return queue.Entries, nil
}

// LeaseWorker takes the lease that makes this process the one worker for a
// queue, without waiting behind a live holder: what the lease guards is
// ownership of something singular, so a second worker is told the queue has
// one rather than queued to become it. A lock that looks held is waited out
// for the grace a run's lease is, for the reason leaseGrace gives.
//
// The lease is the queue's alone. It is not the target's promotion lease, and
// holding it reserves no developer slot.
func (s *MergeQueueStore) LeaseWorker(ctx context.Context, key MergeQueueKey) (*Lease, bool, error) {
	if err := key.validate(); err != nil {
		return nil, false, fmt.Errorf("merge queue worker: %w", err)
	}
	queueRoot, err := s.openQueue(key)
	if err != nil {
		return nil, false, err
	}
	defer queueRoot.Close()
	file, err := queueRoot.OpenLock(mergeQueueWorkerLease, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open the merge queue worker lease for %s: %w", key.TargetBranch, err)
	}
	held, err := acquireLease(ctx, file, s.leaseWait, nil)
	if err != nil {
		file.Close()
		return nil, false, fmt.Errorf("lock the merge queue worker lease for %s: %w", key.TargetBranch, err)
	}
	if !held {
		file.Close()
		return nil, false, nil
	}
	return &Lease{label: mergeQueueWorkerLabel, file: file, scope: key.directory()}, true, nil
}

// openQueue pins the queue's directory, creating it on the way.
func (s *MergeQueueStore) openQueue(key MergeQueueKey) (*repowrite.PinnedRoot, error) {
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, fmt.Errorf("pin the merge queue state root: %w", err)
	}
	defer root.Close()
	directory := path.Join(filepath.ToSlash(s.directory()), key.directory())
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create the merge queue directory for %s: %w", key.TargetBranch, err)
	}
	queueRoot, err := root.OpenDirectory(directory)
	if err != nil {
		return nil, fmt.Errorf("open the merge queue for %s: %w", key.TargetBranch, err)
	}
	return queueRoot, nil
}

// lockQueue pins a queue's directory and takes its record lock, which is held
// only for one admission's read, write, and readback.
func (s *MergeQueueStore) lockQueue(ctx context.Context, key MergeQueueKey) (*repowrite.PinnedRoot, func(), error) {
	queueRoot, err := s.openQueue(key)
	if err != nil {
		return nil, nil, err
	}
	file, err := queueRoot.OpenLock(mergeQueueRecordLock, 0o600)
	if err != nil {
		queueRoot.Close()
		return nil, nil, fmt.Errorf("open the merge queue lock for %s: %w", key.TargetBranch, err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, mergeQueueRecordWait)
	defer cancel()
	if err := lockStateFile(waitCtx, file); err != nil {
		file.Close()
		queueRoot.Close()
		return nil, nil, fmt.Errorf("lock the merge queue for %s: %w", key.TargetBranch, err)
	}
	return queueRoot, func() {
		_ = releaseStateFile(file)
		queueRoot.Close()
	}, nil
}

// load reads a queue's record, or an empty queue where none was written. The
// strict read is the one an admission makes before it writes the record back;
// the tolerant one is for a reader.
func (s *MergeQueueStore) load(queueRoot *repowrite.PinnedRoot, key MergeQueueKey, strict bool) (MergeQueue, error) {
	encoded, err := readQueue(queueRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return MergeQueue{SchemaVersion: MergeQueueSchemaVersion, ProductID: s.productID, Repository: key.Repository, TargetBranch: key.TargetBranch, NextOrder: 1}, nil
	}
	if err != nil {
		return MergeQueue{}, fmt.Errorf("read the merge queue for %s: %w", key.TargetBranch, err)
	}
	return s.decode(encoded, key, strict)
}

// readBack reads the record after a failed save, through the store's readback.
func (s *MergeQueueStore) readBack(queueRoot *repowrite.PinnedRoot, key MergeQueueKey) (MergeQueue, error) {
	encoded, err := s.readback(queueRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return MergeQueue{}, nil
	}
	if err != nil {
		return MergeQueue{}, err
	}
	return s.decode(encoded, key, true)
}

func (s *MergeQueueStore) decode(encoded []byte, key MergeQueueKey, strict bool) (MergeQueue, error) {
	var queue MergeQueue
	var err error
	if strict {
		err = decodeStrictly(encoded, &queue)
	} else {
		var unknown []string
		unknown, err = decodeTolerating(encoded, &queue)
		noteUnknownFields("merge queue", unknown)
	}
	if err != nil {
		return MergeQueue{}, fmt.Errorf("decode the merge queue for %s: %w", key.TargetBranch, err)
	}
	if err := queue.validate(s.productID, key); err != nil {
		return MergeQueue{}, fmt.Errorf("the merge queue for %s: %w", key.TargetBranch, err)
	}
	return queue, nil
}

func (q MergeQueue) entryForRun(runID string) (MergeQueueEntry, bool) {
	for _, entry := range q.Entries {
		if entry.RunID == runID {
			return entry, true
		}
	}
	return MergeQueueEntry{}, false
}

// writeQueue replaces a queue's record whole and synced, then syncs the
// directory, so a record a reader sees is one a crash keeps.
func writeQueue(queueRoot *repowrite.PinnedRoot, encoded []byte) error {
	if err := queueRoot.WriteFile(mergeQueueRecordName, encoded, 0o600, false); err != nil {
		return err
	}
	return queueRoot.Sync()
}

func readQueue(queueRoot *repowrite.PinnedRoot) ([]byte, error) {
	encoded, err := queueRoot.ReadFile(mergeQueueRecordName)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxEncodedStateBytes {
		return nil, fmt.Errorf("the merge queue record is %d bytes, which exceeds the %d byte bound", len(encoded), maxEncodedStateBytes)
	}
	return encoded, nil
}

func newMergeQueueEntryID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate a merge queue entry id: %w", err)
	}
	return "mqe-" + hex.EncodeToString(id[:]), nil
}

// mergeQueueContents checks what an admission records beyond its queue.
func mergeQueueContents(workItemID, workItemTitle, runID, publication, approvedHead, integrationPolicy string, mode MergeQueueMode) []error {
	var problems []error
	for _, text := range []struct {
		field, value string
		required     bool
	}{
		{"work item id", workItemID, true},
		{"work item title", workItemTitle, true},
		{"publication", publication, false},
		{"integration policy", integrationPolicy, true},
	} {
		if err := mergeQueueText(text.field, text.value, text.required); err != nil {
			problems = append(problems, err)
		}
	}
	if !ValidRunID(runID) {
		problems = append(problems, fmt.Errorf("run id %q is not a run id", runID))
	}
	if !commitPattern.MatchString(approvedHead) {
		problems = append(problems, fmt.Errorf("approved head %q is not a full commit id", approvedHead))
	}
	if !mode.valid() {
		problems = append(problems, fmt.Errorf("merge queue mode %q is neither %q nor %q", mode, MergeQueueHarness, MergeQueueForge))
	}
	return problems
}

// mergeQueueText refuses a text an entry could not record as it was given: one
// with surrounding space, a line break or other control character, or more than
// the bound. It refuses rather than trims, because an entry's contents are what
// a retry is compared against.
func mergeQueueText(field, value string, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s %q has surrounding space", field, value)
	}
	if len(value) > MaxMergeQueueTextBytes {
		return fmt.Errorf("%s is %d bytes, which exceeds the %d byte bound", field, len(value), MaxMergeQueueTextBytes)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%s %q contains a control character", field, value)
	}
	return nil
}
