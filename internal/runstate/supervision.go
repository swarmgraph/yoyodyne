package runstate

// What the product's supervisor records about itself and its children.
//
// The management-and-supervision design puts one supervisor over each product,
// with the Slack sink, the scheduler, and the dashboard as its children, and
// rules that children survive the supervisor's death: they are processes of
// their own with recorded presence, and a returning supervisor reattaches
// through those records and the lease machinery rather than killing and
// respawning. Two records make that hold. The lease under this store is what
// answers whether a supervisor is running — an advisory lock the operating
// system drops when its holder dies, so a killed supervisor leaves nothing to
// clear. The record beside it is what the supervisor most recently knew about
// its children: which it started, which it found already running, which it
// has given up restarting and why. `yoyo start` reads it to say what came up,
// `yoyo stop` reads it for the process to stop, and `yoyo status` reads it for
// the child the supervisor has left down.
//
// Nothing here is the child's own presence. Each child keeps that where it
// always has — the sink's presence record, the watch session's holder stamp —
// and the supervisor's record is its account of having looked, so a reader
// wanting to know whether a child is alive asks the child's lease and reads
// this for what the supervisor decided about it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// SupervisionSchemaVersion is 1 and has never changed.
const SupervisionSchemaVersion = 1

// MaxChildReasonBytes bounds what the record says about one child, for the
// reason every other reason here is bounded: it is read in a line by somebody
// deciding what to do about it.
const MaxChildReasonBytes = 4 << 10

const (
	// supervisorLeaseFile is the lock one supervisor holds for as long as it
	// runs, and supervisionFile is its record of the children.
	supervisorLeaseFile = ".supervisor.lock"
	supervisionFile     = "supervision.json"
	// supervisorLogFile is where a supervisor started detached says what it is
	// doing, beside its record.
	supervisorLogFile = "supervisor.log"
)

// ChildState is where one child stands, as the supervisor last saw it. It is
// the closed set every surface reads a child's state from.
type ChildState string

const (
	// ChildOff is a part the configuration does not enable. It is recorded so a
	// reader sees the product's whole shape rather than only what is running.
	ChildOff ChildState = "off"
	// ChildNotYet is a part the configuration enables whose adoption as a child
	// of the supervisor has not landed, so it is declared and not started. The
	// reason names the work that adopts it.
	ChildNotYet ChildState = "not_yet_a_child"
	// ChildRunning is a child holding its lease: started by this supervisor, or
	// found already running and reattached.
	ChildRunning ChildState = "running"
	// ChildDown is a child that is not running and is going to be started again
	// once its backoff has passed.
	ChildDown ChildState = "down"
	// ChildDegraded is a child the supervisor has stopped restarting: it failed
	// past the bounds, or it cannot be started at all, and it is left down with
	// the reason until somebody acts on it.
	ChildDegraded ChildState = "degraded"
	// ChildScheduled is a part that is a pass the supervisor takes itself on a
	// cadence rather than a process it starts: the maintenance pass. The reason
	// carries its cadence and what its last pass came to.
	ChildScheduled ChildState = "scheduled"
)

// Valid reports whether the state is one this harness names.
func (s ChildState) Valid() bool {
	switch s {
	case ChildOff, ChildNotYet, ChildRunning, ChildDown, ChildDegraded, ChildScheduled:
		return true
	}
	return false
}

// SupervisedChild is one part of the product as the supervisor last saw it.
type SupervisedChild struct {
	Service config.ServiceName `json:"service"`
	State   ChildState         `json:"state"`
	// PID is the process the child is running as, where the supervisor knows
	// it: the one it started, or the one the child's own presence named.
	PID int `json:"pid,omitempty"`
	// Log is where a child the supervisor started says what it is doing.
	Log string `json:"log,omitempty"`
	// Reason says why a child is off, not yet a child, down, or degraded. It is
	// empty for a child that is running.
	Reason string `json:"reason,omitempty"`
	// Reattached reports that the child was found running rather than started:
	// a supervisor that came back after dying, or a part somebody started by
	// hand before the product was.
	Reattached bool `json:"reattached,omitempty"`
	// StartedAt is when this supervisor last started the child, and zero for a
	// child it only ever reattached.
	StartedAt time.Time `json:"started_at,omitempty"`
	// DiedAt is when the supervisor last found the child gone.
	DiedAt time.Time `json:"died_at,omitempty"`
	// Starts is how many times this supervisor has started the child.
	Starts int `json:"starts,omitempty"`
	// Failures is how many times in a row the child has died within the stable
	// interval of being started, which is what the restart bound counts. A child
	// that stays up past that interval starts the count again at nothing.
	Failures int `json:"failures,omitempty"`
	// NextStartAt is when a child that is down is started again: the backoff
	// its failures earned it.
	NextStartAt time.Time `json:"next_start_at,omitempty"`
	// Build is the revision the running child was built from, as the child
	// itself last said, and BuildSince when the supervisor first found it on
	// that build — when it last moved. Build is empty where the child has said
	// nothing the supervisor can read, which is a comparison nobody can make
	// rather than a child that is current.
	Build      string    `json:"build,omitempty"`
	BuildSince time.Time `json:"build_since,omitempty"`
	// RestartingInto is the build a child is being moved onto because a binary
	// was deployed over the one it runs, for as long as that restart is under
	// way, and Redeploy says where it stands: what the child is in the middle of
	// that the restart waits out, or that the child restarts itself.
	RestartingInto string `json:"restarting_into,omitempty"`
	Redeploy       string `json:"redeploy,omitempty"`
	// Restarts is how many times the supervisor has moved the child onto a
	// deployed build, and RestartedAt when it last did. They are counted apart
	// from Failures on purpose: a restart into a deployed build is not a death,
	// and spending the restart bound on one would leave a part degraded for
	// having been deployed to five times in two minutes.
	Restarts    int       `json:"restarts,omitempty"`
	RestartedAt time.Time `json:"restarted_at,omitempty"`
}

// Supervision is the supervisor's record: which process it is, and what it
// last knew about each part of the product.
type Supervision struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// PID is the supervisor process that wrote the record. Whether it is still
	// there is the lease's answer, and this is how `yoyo stop` names what to
	// stop.
	PID int `json:"pid"`
	// Build is the revision the supervisor was built from, for the same reason
	// the sink records its own: a resident process runs what it was started
	// with while the harness moves on.
	Build string `json:"build,omitempty"`
	// Deployed is the revision of the binary on disk the parts are started
	// from, as the supervisor last read it: the build every part is moved onto.
	// It is empty where the binary carries no revision, and then nothing is
	// compared or restarted.
	Deployed  string    `json:"deployed,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// ObservedAt is when the supervisor last wrote the record.
	ObservedAt time.Time `json:"observed_at"`
	// Children is every part of the product, in the order the section declares
	// them, whether or not each is enabled.
	Children []SupervisedChild `json:"children"`
}

func (s Supervision) Validate() error {
	var problems []error
	if s.SchemaVersion != SupervisionSchemaVersion {
		problems = append(problems, fmt.Errorf("supervision schema version %d is not supported", s.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(s.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if s.PID <= 0 {
		problems = append(problems, fmt.Errorf("supervisor pid %d names no process", s.PID))
	}
	if s.StartedAt.IsZero() {
		problems = append(problems, errors.New("started at is required"))
	}
	if s.ObservedAt.IsZero() {
		problems = append(problems, errors.New("observed at is required"))
	}
	seen := make(map[config.ServiceName]struct{}, len(s.Children))
	for _, child := range s.Children {
		if child.Service == "" {
			problems = append(problems, errors.New("a child names no service"))
		}
		if _, duplicate := seen[child.Service]; duplicate {
			problems = append(problems, fmt.Errorf("child %s is recorded twice", child.Service))
		}
		seen[child.Service] = struct{}{}
		if !child.State.Valid() {
			problems = append(problems, fmt.Errorf("child %s state %q is not one this harness names", child.Service, child.State))
		}
		if len(child.Reason) > MaxChildReasonBytes {
			problems = append(problems, fmt.Errorf("child %s reason is %d bytes, which exceeds the %d byte bound", child.Service, len(child.Reason), MaxChildReasonBytes))
		}
		if len(child.Redeploy) > MaxChildReasonBytes {
			problems = append(problems, fmt.Errorf("child %s redeploy account is %d bytes, which exceeds the %d byte bound", child.Service, len(child.Redeploy), MaxChildReasonBytes))
		}
	}
	return errors.Join(problems...)
}

// Child finds one part's entry, reporting whether the record carries it.
func (s Supervision) Child(name config.ServiceName) (SupervisedChild, bool) {
	for _, child := range s.Children {
		if child.Service == name {
			return child, true
		}
	}
	return SupervisedChild{}, false
}

// Degraded lists the children the supervisor has left down, in the record's
// order. It is what the attention line reads.
func (s Supervision) Degraded() []SupervisedChild {
	var degraded []SupervisedChild
	for _, child := range s.Children {
		if child.State == ChildDegraded {
			degraded = append(degraded, child)
		}
	}
	return degraded
}

// SupervisionStore is where the supervisor's lease and record live: one
// directory under the product, because a supervisor is one per product and
// nothing about it is shared with a sibling.
//
// It writes with the same temporary-file-and-rename every state file beside it
// uses, directly rather than through the repository's confined-write primitive,
// for the reason the provider outage store gives: the state root is the
// harness's own directory outside every repository.
type SupervisionStore struct {
	// base is the state root, and within is where this store sits under it in
	// slash form, kept apart because a detached child's log is a root and a
	// path within it rather than one absolute name.
	base      string
	within    string
	root      string
	productID domain.ProductID
}

func NewSupervisionStore(root string, productID domain.ProductID) (*SupervisionStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	base := filepath.Clean(root)
	within := filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(base, string(productID))), "supervisor")
	return &SupervisionStore{
		base:      base,
		within:    filepath.ToSlash(within),
		root:      filepath.Join(base, within),
		productID: productID,
	}, nil
}

func (s *SupervisionStore) Root() string { return s.root }

// Product is the product this store belongs to.
func (s *SupervisionStore) Product() domain.ProductID { return s.productID }

// SupervisorLog is where a detached supervisor says what it is doing: the root
// the write is confined to, and the path inside it, in the shape the detached
// launcher takes them.
func (s *SupervisionStore) SupervisorLog() (root, relative string) {
	return s.base, s.within + "/" + supervisorLogFile
}

// Lease makes this process the product's only supervisor, reporting whether it
// got it. Two supervisors over one product would each restart what the other
// had just stopped, so the second is refused rather than queued.
func (s *SupervisionStore) Lease() (*Lease, bool, error) {
	return TryLeasePath(filepath.Join(s.root, supervisorLeaseFile), "product supervisor")
}

// Running reports whether a supervisor is holding this product's lease, by
// trying to take it and letting it go again — the same question the sink's
// store answers the same way, and for the same reason: the lock is dropped by
// the operating system when its holder dies, so the answer is about a process
// rather than about a file somebody forgot to clean up.
func (s *SupervisionStore) Running() (bool, error) {
	lease, held, err := s.Lease()
	if err != nil {
		return false, err
	}
	if !held {
		return true, nil
	}
	if err := lease.Release(); err != nil {
		return false, err
	}
	return false, nil
}

// Save records what the supervisor knows.
func (s *SupervisionStore) Save(recorded Supervision) error {
	recorded.SchemaVersion = SupervisionSchemaVersion
	recorded.ProductID = s.productID
	if err := recorded.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create supervisor state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".supervision-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary supervision record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary supervision record: %w", err)
	}
	if err := writeJSONFile(temporary, "supervision record", recorded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary supervision record: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path()); err != nil {
		return fmt.Errorf("replace supervision record: %w", err)
	}
	return syncDirectory(s.root)
}

// Load reads the record back, reporting whether a supervisor ever wrote one. A
// record left by a supervisor that died is returned like any other: whether it
// is still there is the lease's answer, and the two are read together.
//
// It is the tolerant door of the two in tolerantread.go, because nothing reads
// this record to write it back: the supervisor writes it whole from what it is
// running, and every reader — the dashboard's services panel, `yoyo product` —
// only says what it holds. A supervisor on a newer build than the dashboard
// reading it is the ordinary case after a landing, and refusing its record would
// blank the panel for as long as the two builds differ.
func (s *SupervisionStore) Load() (Supervision, bool, error) {
	file, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return Supervision{}, false, nil
	}
	if err != nil {
		return Supervision{}, false, fmt.Errorf("open supervision record: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return Supervision{}, false, fmt.Errorf("read supervision record: %w", err)
	}
	var recorded Supervision
	unknown, err := decodeTolerating(encoded, &recorded)
	if err != nil {
		return Supervision{}, false, fmt.Errorf("decode supervision record: %w", err)
	}
	noteUnknownFields("supervision record", unknown)
	if err := recorded.Validate(); err != nil {
		return Supervision{}, false, err
	}
	if recorded.ProductID != s.productID {
		return Supervision{}, false, fmt.Errorf("supervision record belongs to product %q, not %q", recorded.ProductID, s.productID)
	}
	return recorded, true, nil
}

func (s *SupervisionStore) path() string { return filepath.Join(s.root, supervisionFile) }

// retiredJobsFile is the log of the launchd jobs outside the product that the
// supervisor retired, one line each, beside its record.
const retiredJobsFile = "retired-jobs.jsonl"

// maxRetiredJobBytes bounds one line of that log. The line carries the job's
// whole property list, which is a page of XML and never more than this.
const maxRetiredJobBytes = 256 << 10

// RetiredJob is a launchd job outside the product that managed the product's
// parts, and what the supervisor did about it when it was installed over it.
//
// It is written because a retirement removes something the operator put on the
// machine: the record keeps what the job was — its property list whole, which
// is what `launchctl bootstrap` would need to put it back — and what was done to
// it, so the hand step it replaces is recorded rather than only gone.
type RetiredJob struct {
	// Label is the job's launchd label, and Plist where its property list was.
	Label string `json:"label"`
	Plist string `json:"plist"`
	// PlistContent is the property list as it stood before it was removed.
	PlistContent string `json:"plist_content,omitempty"`
	// Program is what the job ran.
	Program []string `json:"program,omitempty"`
	// Duplicated is what the job did that the product does, in the words the
	// doctor uses.
	Duplicated []string `json:"duplicated,omitempty"`
	// WasLoaded, Unloaded, and Removed are what was found and what was done:
	// the job booted out of launchd where it was loaded, and its property list
	// removed where it was there.
	WasLoaded bool `json:"was_loaded"`
	Unloaded  bool `json:"unloaded"`
	Removed   bool `json:"removed"`
	// Left names what the retirement deliberately did not touch — the job's own
	// script, which is the operator's file.
	Left string `json:"left,omitempty"`
	// By is the verb that retired it, and RetiredAt when.
	By        string    `json:"by"`
	RetiredAt time.Time `json:"retired_at"`
}

// RecordRetiredJob appends one retirement to the log and returns where the log
// is, so what says the job was retired can say where that is written down.
func (s *SupervisionStore) RecordRetiredJob(job RetiredJob) (string, error) {
	if job.Label == "" || job.RetiredAt.IsZero() {
		return "", errors.New("a retired job needs its label and when it was retired")
	}
	encoded, err := json.Marshal(job)
	if err != nil {
		return "", fmt.Errorf("encode retired job: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxRetiredJobBytes {
		return "", fmt.Errorf("encoded retired job is %d bytes, limit is %d", len(encoded), maxRetiredJobBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return "", fmt.Errorf("create supervisor state directory: %w", err)
	}
	path := s.RetiredJobsPath()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return "", fmt.Errorf("inspect retired job log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return "", fmt.Errorf("open retired job log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return "", fmt.Errorf("append retired job: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return "", fmt.Errorf("append retired job: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", fmt.Errorf("sync retired job log: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close retired job log: %w", err)
	}
	if created {
		if err := syncDirectory(s.root); err != nil {
			return "", err
		}
	}
	return path, nil
}

// RetiredJobs reads every retirement back, oldest first. A log that is not there
// is a product that never retired anything.
func (s *SupervisionStore) RetiredJobs() ([]RetiredJob, error) {
	encoded, err := os.ReadFile(s.RetiredJobsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read retired job log: %w", err)
	}
	var jobs []RetiredJob
	for index, line := range bytes.Split(encoded, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var job RetiredJob
		if err := json.Unmarshal(line, &job); err != nil {
			return nil, fmt.Errorf("decode retired job log line %d: %w", index+1, err)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// RetiredJobsPath is where the retirements are logged.
func (s *SupervisionStore) RetiredJobsPath() string { return filepath.Join(s.root, retiredJobsFile) }
