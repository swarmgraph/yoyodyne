package runstate

// Places for heavy checks: how many of one check may run at once on this
// machine, and which check is let start next.
//
// A project names the checks only so many of may run at once
// (execution.heavy_checks): the race suite, the whole test run. Every run
// asking to start one asks this record for a place first, and the record is
// one file under the state root, so every run on the machine, of every
// product, counts against the same places. It is changed only under a file
// lock beside it, and every change is decided from the record as it is read
// under that lock.
//
// A place is a permit, and a permit moves through four states, each recorded
// before the step it names:
//
//   - waiting: the run has asked and has not been let start;
//   - running: the run has been given the place and is starting, or has
//     started, the check in a process group of its own;
//   - exited: the check's main process has ended and its result is recorded;
//   - released: the place is free again.
//
// A permit is released only from exited, and only once every process the
// check started is confirmed gone; one whose processes cannot be confirmed
// gone keeps its place and says why. Running and exited permits are what a
// place is counted by.
//
// Whether a check's processes are gone is read the way a provider attempt's
// are (launch.go): every permit has a file of its own beside the record, which
// the asking process locks and the check's processes inherit. The lock goes
// only when the last process holding the file has exited, so a free lock and a
// process group with no members is a check that has stopped, and nothing about
// a process number on its own is trusted to say so. The same file says whether
// a waiting run's process is still there to be let start: one that died while
// waiting left its file unlocked, and is passed over until its run comes back
// for it.
//
// Every request for a place reads and settles the whole record first, so a
// harness restarted beside checks that outlived it grants nothing until it has
// read what is still running: a running or exited permit whose processes are
// still alive keeps its place, one whose processes are gone is released, and
// waiting permits keep the order they asked in. Nothing resets the count, and
// a run that asks again for a check it was already running waits for that
// launch to end rather than starting a second one beside it.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// HeavyCheckSchemaVersion is 1 and has never changed.
const HeavyCheckSchemaVersion = 1

// HeavyCheckDirectory is the directory under the state root the record, its
// lock, and every permit's file are kept in.
const HeavyCheckDirectory = "heavy-checks"

const (
	heavyCheckRecordName = "permits.json"
	heavyCheckLockName   = "permits.lock"
	// heavyCheckReleasedKept is how many released permits the record keeps, so
	// whoever reads it can see what ran last without the record growing with
	// every check the machine ever ran.
	heavyCheckReleasedKept = 32
	// heavyCheckAbandonedKept is how long a waiting permit whose process has
	// gone keeps its place in the order for its run to come back for it. It is
	// passed over meanwhile, so it holds nobody up.
	heavyCheckAbandonedKept = 6 * time.Hour
	// heavyCheckPoll is how often a waiting run reads the record again.
	heavyCheckPoll = time.Second
	// heavyCheckCleanupWait is how long a check that has exited is watched for
	// its last processes to go before its permit is left holding the place.
	heavyCheckCleanupWait = 2 * time.Second
	// heavyCheckReasonBytes bounds every sentence the record keeps.
	heavyCheckReasonBytes = 1024
)

// HeavyCheckState is where one permit is in its life.
type HeavyCheckState string

const (
	HeavyCheckWaiting  HeavyCheckState = "waiting"
	HeavyCheckRunning  HeavyCheckState = "running"
	HeavyCheckExited   HeavyCheckState = "exited"
	HeavyCheckReleased HeavyCheckState = "released"
)

// Holds reports a state that counts against the check's places.
func (s HeavyCheckState) Holds() bool {
	return s == HeavyCheckRunning || s == HeavyCheckExited
}

func (s HeavyCheckState) valid() bool {
	switch s {
	case HeavyCheckWaiting, HeavyCheckRunning, HeavyCheckExited, HeavyCheckReleased:
		return true
	}
	return false
}

var heavyCheckIDPattern = regexp.MustCompile(`^permit-[0-9a-f]{16}$`)

// HeavyCheckProcess is the process a running check was started as.
type HeavyCheckProcess struct {
	PID          int       `json:"pid"`
	ProcessGroup int       `json:"process_group"`
	StartedAt    time.Time `json:"started_at"`
	// Boot is the boot the machine was in when the check started, where the
	// platform says. A machine restarted since has no process of it left.
	Boot string `json:"boot,omitempty"`
	// Host is the host name the machine had then, said to whoever reads the
	// record. It is never what decides anything: a Mac is given a new host name
	// when it joins another network.
	Host string `json:"host,omitempty"`
}

// HeavyCheckPermit is one run's place, or its request for one, for one check.
type HeavyCheckPermit struct {
	ID string `json:"id"`
	// Product, RunID, WorkItemID, and Commit say whose check this is: the
	// product and run asking, the item the run is over, and the commit the
	// check is run against where the run knows it.
	Product    string `json:"product"`
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id,omitempty"`
	Commit     string `json:"commit,omitempty"`
	// Check is the execution.heavy_checks name the place is counted under, and
	// Command the command as it is run.
	Check    string                    `json:"check"`
	Command  string                    `json:"command"`
	Priority domain.HeavyCheckPriority `json:"priority"`
	State    HeavyCheckState           `json:"state"`
	AskedAt  time.Time                 `json:"asked_at"`
	// GrantedAt is when the place was given, LaunchedAt when the check's
	// process was written down here before it could do any work, ExitedAt when
	// its main process ended, and ReleasedAt when the place was free again.
	GrantedAt  *time.Time `json:"granted_at,omitempty"`
	LaunchedAt *time.Time `json:"launched_at,omitempty"`
	ExitedAt   *time.Time `json:"exited_at,omitempty"`
	ReleasedAt *time.Time `json:"released_at,omitempty"`
	// Process is the check's process, once it is written down.
	Process *HeavyCheckProcess `json:"process,omitempty"`
	// Hold is the name of the permit's file beside the record, and HoldMark
	// what was written into it, which is what tells this file from one put at
	// the same name later.
	Hold     string `json:"hold"`
	HoldMark string `json:"hold_mark"`
	// Asker is the harness process that last asked for this permit.
	Asker string `json:"asker,omitempty"`
	// Result is how the check's main process ended.
	Result string `json:"result,omitempty"`
	// AbandonedAt is when a waiting permit was first found with nobody waiting
	// on it, and is cleared when its run comes back for it.
	AbandonedAt *time.Time `json:"abandoned_at,omitempty"`
	// Problem is why a permit that has exited, or whose harness has gone, still
	// holds its place: its processes could not be confirmed gone. It is the one
	// state of this record somebody has to look at.
	Problem string `json:"problem,omitempty"`
	// Released says why the place was given back, and ReleasedBy who gave it
	// back by hand, where somebody did.
	Released   string `json:"released,omitempty"`
	ReleasedBy string `json:"released_by,omitempty"`
}

// Waited is how long the permit waited for its place: until it was given one,
// or until now where it is still waiting.
func (p HeavyCheckPermit) Waited(now time.Time) time.Duration {
	end := now
	if p.GrantedAt != nil {
		end = *p.GrantedAt
	} else if p.ReleasedAt != nil {
		end = *p.ReleasedAt
	}
	if waited := end.Sub(p.AskedAt); waited > 0 {
		return waited
	}
	return 0
}

// Stuck reports a permit holding its place for processes nobody can confirm
// have gone.
func (p HeavyCheckPermit) Stuck() bool {
	return p.State.Holds() && p.Problem != ""
}

func (p HeavyCheckPermit) validate() error {
	var problems []error
	if !heavyCheckIDPattern.MatchString(p.ID) {
		problems = append(problems, fmt.Errorf("permit id %q is invalid", p.ID))
	}
	if strings.TrimSpace(p.Product) == "" || strings.TrimSpace(p.RunID) == "" {
		problems = append(problems, fmt.Errorf("permit %s names no product or run", p.ID))
	}
	if strings.TrimSpace(p.Check) == "" || strings.TrimSpace(p.Command) == "" {
		problems = append(problems, fmt.Errorf("permit %s names no check", p.ID))
	}
	if !p.Priority.Valid() {
		problems = append(problems, fmt.Errorf("permit %s has priority %q", p.ID, p.Priority))
	}
	if !p.State.valid() {
		problems = append(problems, fmt.Errorf("permit %s has state %q", p.ID, p.State))
	}
	if p.AskedAt.IsZero() {
		problems = append(problems, fmt.Errorf("permit %s records no time it was asked for", p.ID))
	}
	if p.Hold != p.ID+".hold" || p.HoldMark == "" {
		problems = append(problems, fmt.Errorf("permit %s names no file of its own", p.ID))
	}
	if p.State != HeavyCheckWaiting && p.GrantedAt == nil && p.State != HeavyCheckReleased {
		problems = append(problems, fmt.Errorf("permit %s is %s with no time it was granted", p.ID, p.State))
	}
	if p.State == HeavyCheckExited && p.ExitedAt == nil {
		problems = append(problems, fmt.Errorf("permit %s exited with no time it did", p.ID))
	}
	if p.State == HeavyCheckReleased && p.ReleasedAt == nil {
		problems = append(problems, fmt.Errorf("permit %s was released with no time it was", p.ID))
	}
	return errors.Join(problems...)
}

// HeavyCheckRecord is the whole of the record: each heavy check's number of
// places, as last asked with, and every permit not yet released together with
// the last few that were.
type HeavyCheckRecord struct {
	SchemaVersion int                `json:"schema_version"`
	Limits        map[string]int     `json:"limits,omitempty"`
	Permits       []HeavyCheckPermit `json:"permits,omitempty"`
}

// Holding is every permit holding a place, for one check or, with an empty
// name, for every check.
func (r HeavyCheckRecord) Holding(check string) []HeavyCheckPermit {
	var holding []HeavyCheckPermit
	for _, permit := range r.Permits {
		if permit.State.Holds() && (check == "" || permit.Check == check) {
			holding = append(holding, permit)
		}
	}
	return holding
}

// Waiting is every permit still waiting, in the order they are let start.
func (r HeavyCheckRecord) Waiting() []HeavyCheckPermit {
	var waiting []HeavyCheckPermit
	for _, permit := range r.Permits {
		if permit.State == HeavyCheckWaiting {
			waiting = append(waiting, permit)
		}
	}
	sort.SliceStable(waiting, func(first, second int) bool { return heavyCheckBefore(waiting[first], waiting[second]) })
	return waiting
}

// Stuck is every permit holding its place for processes nobody can confirm
// have gone.
func (r HeavyCheckRecord) Stuck() []HeavyCheckPermit {
	var stuck []HeavyCheckPermit
	for _, permit := range r.Permits {
		if permit.Stuck() {
			stuck = append(stuck, permit)
		}
	}
	return stuck
}

// Permit is the permit with this id.
func (r HeavyCheckRecord) Permit(id string) (HeavyCheckPermit, bool) {
	for _, permit := range r.Permits {
		if permit.ID == id {
			return permit, true
		}
	}
	return HeavyCheckPermit{}, false
}

func (r HeavyCheckRecord) validate() error {
	var problems []error
	if r.SchemaVersion != HeavyCheckSchemaVersion {
		problems = append(problems, fmt.Errorf("heavy check record schema version %d is not supported", r.SchemaVersion))
	}
	for check, limit := range r.Limits {
		if strings.TrimSpace(check) == "" || limit < 1 {
			problems = append(problems, fmt.Errorf("heavy check %q has %d places", check, limit))
		}
	}
	seen := map[string]bool{}
	for _, permit := range r.Permits {
		if err := permit.validate(); err != nil {
			problems = append(problems, err)
		}
		if seen[permit.ID] {
			problems = append(problems, fmt.Errorf("permit %s is recorded twice", permit.ID))
		}
		seen[permit.ID] = true
	}
	return errors.Join(problems...)
}

// heavyCheckBefore is the order waiting permits are let start in: a landing's
// first, then a preserved repair's or re-run's, then everything else, and in
// the order they asked among equals.
func heavyCheckBefore(first, second HeavyCheckPermit) bool {
	if first.Priority.Rank() != second.Priority.Rank() {
		return first.Priority.Rank() < second.Priority.Rank()
	}
	if !first.AskedAt.Equal(second.AskedAt) {
		return first.AskedAt.Before(second.AskedAt)
	}
	return first.ID < second.ID
}

// HeavyCheckAsk is what a run asks for a place with.
type HeavyCheckAsk struct {
	Product    string
	RunID      string
	WorkItemID string
	Commit     string
	// Check is the execution.heavy_checks name, and Command the command run.
	Check   string
	Command string
	// Limit is how many of Check may run at once, as the asking run's
	// configuration says. The record keeps the last one asked with.
	Limit    int
	Priority domain.HeavyCheckPriority
	// Read, where set, reads the machine as the check launches, for the
	// measurements kept beside the record (heavycheckmeasure.go). It is read
	// before the record's lock is taken, so a slow reading holds up nobody
	// else's place.
	Read func() HeavyCheckReading
}

func (a HeavyCheckAsk) validate() error {
	var problems []error
	if err := domain.ValidateIdentifier("product id", a.Product); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(a.RunID) == "" {
		problems = append(problems, errors.New("a heavy check is asked for by a run"))
	}
	if strings.TrimSpace(a.Check) == "" || strings.TrimSpace(a.Command) == "" {
		problems = append(problems, errors.New("a heavy check is asked for by its name and command"))
	}
	if a.Limit < 1 {
		problems = append(problems, fmt.Errorf("heavy check %q is asked for with %d places", a.Check, a.Limit))
	}
	if !a.Priority.Valid() {
		problems = append(problems, fmt.Errorf("heavy check %q is asked for with priority %q", a.Check, a.Priority))
	}
	return errors.Join(problems...)
}

// HeavyCheckStore is the record of places, at the state root above every
// product's records, because a machine has one set of cores.
type HeavyCheckStore struct {
	root string
	// The rest are a test's ways in, and zero in every store the harness makes.
	now         func() time.Time
	poll        time.Duration
	cleanupWait time.Duration
	signalGroup func(int) error
	boot        func() string
}

// NewHeavyCheckStore is the record under the state root root.
func NewHeavyCheckStore(root string) (*HeavyCheckStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	return &HeavyCheckStore{root: filepath.Join(filepath.Clean(root), HeavyCheckDirectory)}, nil
}

// Root is the directory the record is kept in.
func (s *HeavyCheckStore) Root() string { return s.root }

func (s *HeavyCheckStore) clock() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func (s *HeavyCheckStore) currentBoot() string {
	if s.boot != nil {
		return s.boot()
	}
	return currentBoot()
}

// pin opens the record's directory pinned against replacement, creating it
// the first time, as every harness-owned write is
// (docs/decisions/invariants, repository-writes-are-physically-confined).
func (s *HeavyCheckStore) pin() (*repowrite.PinnedRoot, error) {
	stateRoot, anchor, err := confinedStateRoot(s.root)
	if err != nil {
		return nil, err
	}
	return pinStateRoot(stateRoot, anchor)
}

// heavyCheckSession is the record held under its lock.
type heavyCheckSession struct {
	store  *HeavyCheckStore
	root   *repowrite.PinnedRoot
	lock   *os.File
	record HeavyCheckRecord
	// alive is, for each waiting permit, whether a process is waiting on it,
	// as settle found.
	alive map[string]bool
}

// open takes the record's lock, waiting for whoever holds it, and reads the
// record.
func (s *HeavyCheckStore) open(ctx context.Context) (*heavyCheckSession, error) {
	root, err := s.pin()
	if err != nil {
		return nil, fmt.Errorf("open the record of heavy check places: %w", err)
	}
	lock, err := root.OpenLock(heavyCheckLockName, 0o600)
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("open the lock on the record of heavy check places: %w", err)
	}
	if err := lockStateFile(ctx, lock); err != nil {
		lock.Close()
		root.Close()
		return nil, fmt.Errorf("lock the record of heavy check places: %w", err)
	}
	session := &heavyCheckSession{store: s, root: root, lock: lock, alive: map[string]bool{}}
	record, err := readHeavyCheckRecord(root, true)
	if err != nil {
		session.close()
		return nil, err
	}
	session.record = record
	return session, nil
}

func (h *heavyCheckSession) close() {
	_ = releaseStateFile(h.lock)
	_ = h.root.Close()
}

// readHeavyCheckRecord reads the record, strictly where strict is true: what
// precedes a write under the lock, where a field this build does not know would
// be lost by writing the record back. Record, which only shows the record,
// reads past such a field, saying so once.
func readHeavyCheckRecord(root *repowrite.PinnedRoot, strict bool) (HeavyCheckRecord, error) {
	source, err := root.ReadFile(heavyCheckRecordName)
	if errors.Is(err, os.ErrNotExist) {
		return HeavyCheckRecord{SchemaVersion: HeavyCheckSchemaVersion}, nil
	}
	if err != nil {
		return HeavyCheckRecord{}, fmt.Errorf("read the record of heavy check places: %w", err)
	}
	if len(source) > maxEncodedStateBytes {
		return HeavyCheckRecord{}, errors.New("the record of heavy check places exceeds the state record size bound")
	}
	var record HeavyCheckRecord
	if strict {
		err = decodeStrictly(source, &record)
	} else {
		var unknown []string
		unknown, err = decodeTolerating(source, &record)
		noteUnknownFields("record of heavy check places", unknown)
	}
	if err != nil {
		return HeavyCheckRecord{}, fmt.Errorf("decode the record of heavy check places: %w", err)
	}
	if err := record.validate(); err != nil {
		return HeavyCheckRecord{}, fmt.Errorf("the record of heavy check places is invalid: %w", err)
	}
	return record, nil
}

// save writes the record whole, keeping only the last few released permits.
func (h *heavyCheckSession) save() error {
	var kept, released []HeavyCheckPermit
	for _, permit := range h.record.Permits {
		if permit.State == HeavyCheckReleased {
			released = append(released, permit)
			continue
		}
		kept = append(kept, permit)
	}
	sort.SliceStable(released, func(first, second int) bool {
		return released[first].ReleasedAt.Before(*released[second].ReleasedAt)
	})
	if len(released) > heavyCheckReleasedKept {
		released = released[len(released)-heavyCheckReleasedKept:]
	}
	h.record.Permits = append(kept, released...)
	h.record.SchemaVersion = HeavyCheckSchemaVersion
	if err := h.record.validate(); err != nil {
		return fmt.Errorf("the record of heavy check places would be invalid: %w", err)
	}
	encoded, err := json.MarshalIndent(h.record, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxEncodedStateBytes {
		return errors.New("the record of heavy check places exceeds the state record size bound")
	}
	if err := h.root.WriteFile(heavyCheckRecordName, encoded, 0o600, false); err != nil {
		return fmt.Errorf("write the record of heavy check places: %w", err)
	}
	return h.root.Sync()
}

func (h *heavyCheckSession) find(id string) *HeavyCheckPermit {
	for index := range h.record.Permits {
		if h.record.Permits[index].ID == id {
			return &h.record.Permits[index]
		}
	}
	return nil
}

// release gives a permit's place back and removes its file.
func (h *heavyCheckSession) release(permit *HeavyCheckPermit, why string) {
	now := h.store.clock()
	permit.State = HeavyCheckReleased
	permit.ReleasedAt = &now
	permit.Released = boundedReason(why)
	permit.Problem = ""
	_ = h.root.Remove(permit.Hold)
}

// boundedReason is a reason on one line, cut on a character boundary to the
// bound the record keeps.
func boundedReason(reason string) string {
	return oneline.Bound(strings.TrimSpace(reason), heavyCheckReasonBytes)
}

// settle reads every permit not yet released against what is still alive:
// what restart recovery is, and what every request for a place does before it
// grants anything.
func (h *heavyCheckSession) settle() {
	now := h.store.clock()
	for index := range h.record.Permits {
		permit := &h.record.Permits[index]
		switch permit.State {
		case HeavyCheckReleased:
			continue
		case HeavyCheckWaiting:
			alive := h.store.held(h.root, *permit)
			h.alive[permit.ID] = alive
			switch {
			case alive:
				permit.AbandonedAt = nil
			case permit.AbandonedAt == nil:
				permit.AbandonedAt = &now
			case now.Sub(*permit.AbandonedAt) > heavyCheckAbandonedKept:
				h.release(permit, fmt.Sprintf("it waited with nobody waiting on it for %s, and its run never came back for it", heavyCheckAbandonedKept))
			}
		default:
			if permit.Process == nil {
				// Given a place and not yet written down as started: the asker
				// is still starting it while it holds the file, and nothing ran
				// if it has let go, because nothing opens a check's gate before
				// its process is written down here.
				if !h.store.held(h.root, *permit) {
					h.release(permit, "it was given a place and its harness stopped before starting it, so nothing ran")
				}
				continue
			}
			state, reason := h.store.observe(h.root, *permit)
			switch state {
			case executionStopped:
				why := "every process it started has gone"
				if permit.State == HeavyCheckRunning {
					why = "every process it started has gone, and nobody recorded how it ended"
				}
				h.release(permit, why)
			case executionAlive:
				// A check still inside the watch its own run keeps for its last
				// processes is not yet one somebody has to look at.
				if permit.State == HeavyCheckExited && now.Sub(*permit.ExitedAt) > heavyCheckCleanupWait {
					permit.Problem = boundedReason(fmt.Sprintf("it exited and %s", reason))
				}
			default:
				permit.Problem = boundedReason(reason)
			}
		}
	}
}

// grantable reports whether a waiting permit may be let start now: its check
// has a free place, and no permit before it in the order is waiting on the
// same check with somebody waiting on it.
func (h *heavyCheckSession) grantable(permit HeavyCheckPermit) bool {
	limit := h.record.Limits[permit.Check]
	if limit < 1 {
		limit = 1
	}
	holding := h.record.Holding(permit.Check)
	if len(holding) >= limit {
		return false
	}
	for _, other := range holding {
		// The same run's earlier launch of the same command, still running
		// after its harness restarted, is waited out rather than started beside.
		if other.Product == permit.Product && other.RunID == permit.RunID && other.Command == permit.Command {
			return false
		}
	}
	for _, other := range h.record.Permits {
		if other.ID == permit.ID || other.State != HeavyCheckWaiting || other.Check != permit.Check {
			continue
		}
		if h.alive[other.ID] && heavyCheckBefore(other, permit) {
			return false
		}
	}
	return true
}

// HeavyCheckGrant is a place given to one run for one launch of a check. The
// caller starts the check behind a gate holding Hold and registering through
// Register, and calls Finish once the check's process has returned.
type HeavyCheckGrant struct {
	store *HeavyCheckStore
	// Permit is the permit as granted.
	Permit HeavyCheckPermit
	hold   *os.File
	// Waited is how long the run waited for the place.
	Waited     time.Duration
	registered bool
	finished   bool
	read       func() HeavyCheckReading
	outcome    HeavyCheckOutcome
	budget     time.Duration
	// MeasureProblem is why this launch's measurement could not be written,
	// where it could not. The check ran exactly as it would have regardless.
	MeasureProblem error
}

// Hold is the permit's file, locked, for the check's process to inherit.
func (g *HeavyCheckGrant) Hold() *os.File { return g.hold }

// Acquire asks for a place for one check and waits until it is given one. A
// check with a free place and nobody before it is given one at once; any other
// waits, recorded as waiting before it waits, reading the record again every
// poll. waiting, where not nil, is told once, as the wait begins, which permit
// is waiting.
//
// A run that asks for a check it already has a running or exited permit for
// waits for that launch's processes to go before it is given another, so a run
// restarted beside a check that outlived its harness never starts a second
// beside it. A run that asks for a check it was already waiting for takes up
// the same permit, and with it the place in the order it first asked in.
//
// A wait cut short by ctx gives the request up, recorded as such, and returns
// ctx's error.
func (s *HeavyCheckStore) Acquire(ctx context.Context, ask HeavyCheckAsk, waiting func(HeavyCheckPermit)) (*HeavyCheckGrant, error) {
	if err := ask.validate(); err != nil {
		return nil, err
	}
	poll := s.poll
	if poll <= 0 {
		poll = heavyCheckPoll
	}
	var (
		id   string
		hold *os.File
		told bool
	)
	giveUp := func(cause error) error {
		if hold == nil {
			return cause
		}
		// The request is given up under a context of its own, because the one
		// that ended is why it is being given up.
		settleCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if session, err := s.open(settleCtx); err == nil {
			if permit := session.find(id); permit != nil && permit.State == HeavyCheckWaiting {
				session.release(permit, fmt.Sprintf("its run stopped waiting: %v", cause))
				_ = session.save()
			}
			session.close()
		}
		_ = releaseStateFile(hold)
		return cause
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, giveUp(err)
		}
		session, err := s.open(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, giveUp(ctx.Err())
			}
			return nil, giveUp(err)
		}
		session.settle()
		if session.record.Limits == nil {
			session.record.Limits = map[string]int{}
		}
		session.record.Limits[ask.Check] = ask.Limit
		if hold == nil {
			// The first look: take up a permit this run is already waiting on
			// for the command, or ask anew. A launch of the same command this
			// run still has running is waited out by grantable, never started
			// beside.
			for index := range session.record.Permits {
				permit := &session.record.Permits[index]
				if permit.State != HeavyCheckWaiting || permit.Product != ask.Product || permit.RunID != ask.RunID || permit.Command != ask.Command {
					continue
				}
				taken, err := s.takeHold(session.root, *permit)
				if err != nil {
					session.close()
					return nil, err
				}
				if taken == nil {
					session.close()
					return nil, fmt.Errorf("another process is already waiting for a place for run %s's %s", ask.RunID, ask.Command)
				}
				hold, id = taken, permit.ID
				permit.AbandonedAt = nil
				permit.Asker = launcherGeneration()
				permit.Priority = ask.Priority
				session.alive[permit.ID] = true
				break
			}
			if hold == nil {
				created, permit, err := s.newPermit(session, ask)
				if err != nil {
					session.close()
					return nil, err
				}
				hold, id = created, permit.ID
				session.record.Permits = append(session.record.Permits, permit)
				session.alive[permit.ID] = true
			}
		}
		permit := session.find(id)
		if permit == nil || permit.State != HeavyCheckWaiting {
			session.close()
			_ = releaseStateFile(hold)
			return nil, fmt.Errorf("the permit run %s was waiting on for %s was taken out of the record", ask.RunID, ask.Command)
		}
		if session.grantable(*permit) {
			now := s.clock()
			permit.State = HeavyCheckRunning
			permit.GrantedAt = &now
			granted := *permit
			if err := session.save(); err != nil {
				session.close()
				_ = releaseStateFile(hold)
				return nil, err
			}
			session.close()
			return &HeavyCheckGrant{store: s, Permit: granted, hold: hold, Waited: granted.Waited(now), read: ask.Read}, nil
		}
		waitingOn := permit
		reported := *waitingOn
		if err := session.save(); err != nil {
			session.close()
			return nil, giveUp(err)
		}
		session.close()
		if !told && waiting != nil {
			told = true
			waiting(reported)
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, giveUp(ctx.Err())
		case <-timer.C:
		}
	}
}

// newPermit makes a waiting permit for ask, with its file created and locked.
func (s *HeavyCheckStore) newPermit(session *heavyCheckSession, ask HeavyCheckAsk) (*os.File, HeavyCheckPermit, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return nil, HeavyCheckPermit{}, fmt.Errorf("name a permit for %s: %w", ask.Command, err)
	}
	id := "permit-" + hex.EncodeToString(bytes)
	markBytes := make([]byte, 16)
	if _, err := rand.Read(markBytes); err != nil {
		return nil, HeavyCheckPermit{}, fmt.Errorf("mark the file of permit %s: %w", id, err)
	}
	permit := HeavyCheckPermit{
		ID: id, Product: ask.Product, RunID: ask.RunID, WorkItemID: ask.WorkItemID, Commit: ask.Commit,
		Check: ask.Check, Command: ask.Command, Priority: ask.Priority, State: HeavyCheckWaiting,
		AskedAt: s.clock(), Hold: id + ".hold", HoldMark: hex.EncodeToString(markBytes), Asker: launcherGeneration(),
	}
	if err := session.root.CreateFile(permit.Hold, []byte(permit.HoldMark), 0o600); err != nil {
		return nil, HeavyCheckPermit{}, fmt.Errorf("create the file of permit %s: %w", id, err)
	}
	hold, err := s.takeHold(session.root, permit)
	if err == nil && hold == nil {
		err = fmt.Errorf("the file of permit %s was locked by somebody else as it was made", id)
	}
	if err != nil {
		_ = session.root.Remove(permit.Hold)
		return nil, HeavyCheckPermit{}, err
	}
	return hold, permit, nil
}

// takeHold locks a permit's file and returns it, or nil where somebody else
// has it locked. A file that is missing or carries another mark is refused.
func (s *HeavyCheckStore) takeHold(root *repowrite.PinnedRoot, permit HeavyCheckPermit) (*os.File, error) {
	file, err := openRegular(root, permit.Hold)
	if err != nil {
		return nil, fmt.Errorf("open the file of permit %s: %w", permit.ID, err)
	}
	if mark, err := readHoldMark(file); err != nil || mark != permit.HoldMark {
		file.Close()
		return nil, fmt.Errorf("the file of permit %s has been replaced", permit.ID)
	}
	taken, err := tryLockStateFile(file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the file of permit %s: %w", permit.ID, err)
	}
	if !taken {
		file.Close()
		return nil, nil
	}
	return file, nil
}

// held reports whether some process holds a permit's file: a waiting run, or
// the processes of a check. A file nobody can open or that has been replaced
// is not held by anything this permit started.
func (s *HeavyCheckStore) held(root *repowrite.PinnedRoot, permit HeavyCheckPermit) bool {
	file, err := s.takeHold(root, permit)
	if err != nil {
		return false
	}
	if file == nil {
		return true
	}
	_ = releaseStateFile(file)
	return false
}

// Register writes the check's process down as started, before its gate opens
// and before it does any work. A failure leaves the gate shut, so a check that
// could not be written down never ran.
func (g *HeavyCheckGrant) Register(process execution.StartedProcess) error {
	var reading HeavyCheckReading
	if g.read != nil {
		reading = g.read()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := g.store.open(ctx)
	if err != nil {
		return err
	}
	defer session.close()
	permit := session.find(g.Permit.ID)
	if permit == nil || permit.State != HeavyCheckRunning || permit.Process != nil {
		return fmt.Errorf("permit %s is no longer this launch's to start", g.Permit.ID)
	}
	host, _ := os.Hostname()
	now := g.store.clock()
	permit.Process = &HeavyCheckProcess{
		PID: process.PID, ProcessGroup: process.ProcessGroup, StartedAt: process.StartedAt.UTC(),
		Boot: g.store.currentBoot(), Host: strings.TrimSpace(host),
	}
	permit.LaunchedAt = &now
	if err := session.save(); err != nil {
		return err
	}
	g.registered = true
	g.Permit = *permit
	if err := session.measureLaunch(*permit, reading); err != nil {
		g.MeasureProblem = err
	}
	return nil
}

// Finish records how the check's main process ended and gives the place back
// once every process it started is confirmed gone, watching for them a short
// while first. A check whose processes are still there after that keeps its
// place, with the reason on the permit, and is released by the next request
// for a place that finds them gone. A launch that never started gives its
// place straight back.
func (g *HeavyCheckGrant) Finish(result string) (HeavyCheckPermit, error) {
	if g.finished {
		return g.Permit, nil
	}
	g.finished = true
	// The process runner closes this copy once the check holds it; a check
	// that never started leaves it to be closed here. It is closed and never
	// unlocked: the lock belongs to the open file the check's processes share
	// with this copy, so unlocking it would let go of theirs too.
	_ = g.hold.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wait := g.store.cleanupWait
	if wait <= 0 {
		wait = heavyCheckCleanupWait
	}
	deadline := time.Now().Add(wait)
	for first := true; ; first = false {
		session, err := g.store.open(ctx)
		if err != nil {
			return g.Permit, err
		}
		permit := session.find(g.Permit.ID)
		if permit == nil || permit.State == HeavyCheckReleased {
			if permit != nil {
				g.Permit = *permit
			}
			session.close()
			return g.Permit, nil
		}
		if first && g.registered {
			now := g.store.clock()
			permit.State = HeavyCheckExited
			permit.ExitedAt = &now
			permit.Result = boundedReason(result)
			if err := session.measureExit(*permit, g.outcome, g.budget); err != nil && g.MeasureProblem == nil {
				g.MeasureProblem = err
			}
		}
		// The reason a place is kept is written only once the watch is over, so
		// a check whose last process takes a moment to go is never shown as one
		// somebody has to look at.
		last := time.Now().After(deadline)
		if !g.registered {
			if !g.store.held(session.root, *permit) {
				session.release(permit, "its launch never started, so nothing ran")
			}
		} else {
			state, reason := g.store.observe(session.root, *permit)
			if state == executionStopped {
				session.release(permit, "it exited and every process it started has gone")
			} else if last {
				permit.Problem = boundedReason(fmt.Sprintf("it exited and %s", reason))
			}
		}
		saved := session.save()
		g.Permit = *permit
		session.close()
		if saved != nil {
			return g.Permit, saved
		}
		if g.Permit.State == HeavyCheckReleased || last {
			return g.Permit, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Record is the record as it stands, read without its lock and without
// settling anything: what a surface shows. The record is replaced whole, so
// a reader never sees half of one.
func (s *HeavyCheckStore) Record() (HeavyCheckRecord, error) {
	root, err := s.pin()
	if err != nil {
		return HeavyCheckRecord{}, err
	}
	defer root.Close()
	return readHeavyCheckRecord(root, false)
}

// Settle reads the whole record against what is still alive and records what
// it found, granting nothing: restart recovery on its own, for a process that
// starts before any run asks for a place.
func (s *HeavyCheckStore) Settle(ctx context.Context) (HeavyCheckRecord, error) {
	session, err := s.open(ctx)
	if err != nil {
		return HeavyCheckRecord{}, err
	}
	defer session.close()
	session.settle()
	if err := session.save(); err != nil {
		return HeavyCheckRecord{}, err
	}
	return session.record, nil
}

// ReleaseByHand gives back the place a permit holds for processes nobody
// could confirm have gone, on the word of whoever releases it, recorded with
// their name and reason. A waiting permit holds no place and is not released
// this way.
func (s *HeavyCheckStore) ReleaseByHand(ctx context.Context, id, by, reason string) (HeavyCheckPermit, error) {
	if strings.TrimSpace(by) == "" || strings.TrimSpace(reason) == "" {
		return HeavyCheckPermit{}, errors.New("a place released by hand names who released it and why")
	}
	session, err := s.open(ctx)
	if err != nil {
		return HeavyCheckPermit{}, err
	}
	defer session.close()
	permit := session.find(id)
	switch {
	case permit == nil:
		return HeavyCheckPermit{}, fmt.Errorf("no permit %s is recorded", id)
	case !permit.State.Holds():
		return HeavyCheckPermit{}, fmt.Errorf("permit %s is %s and holds no place", id, permit.State)
	}
	session.release(permit, "released by hand: "+reason)
	permit.ReleasedBy = boundedReason(by)
	if err := session.save(); err != nil {
		return HeavyCheckPermit{}, err
	}
	return *permit, nil
}
