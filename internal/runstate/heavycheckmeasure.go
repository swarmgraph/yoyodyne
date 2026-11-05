package runstate

// What each heavy check cost, kept so its limit can be set from evidence.
//
// Every launch of a heavy check (execution.heavy_checks) is written down here
// as it launches, and finished as it exits: how long it waited for its place,
// how long it ran, against what time limit, how it ended, and what the machine
// looked like as it launched — the load, the free memory, how many of each
// heavy check were running in their places, and every process running a heavy
// check's command outside any place, which is a developer running a full suite
// inside its own session, where no place is asked for. That last count is the
// part of the machine's load the places do not limit, and a limit read without
// it would be read as though the places were all there was.
//
// It is a file of its own beside the record of places, written under the same
// lock, so the record of places keeps the shape every build running beside
// this one reads, and a measurement that cannot be written never stops a
// check: it is evidence about the check, not part of running it. It keeps the
// last heavyCheckMeasurementsKept launches.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// HeavyCheckMeasurementSchemaVersion is 1 and has never changed.
const HeavyCheckMeasurementSchemaVersion = 1

const (
	heavyCheckMeasurementsName = "measurements.json"
	// heavyCheckMeasurementsKept is how many launches the measurements keep:
	// enough for weeks of one machine's heavy checks, and small enough that
	// the file stays far inside the state record size bound.
	heavyCheckMeasurementsKept = 512
	// heavyCheckOutsideKept bounds how many processes running a heavy check
	// outside any place one launch names; the count beside it is whole.
	heavyCheckOutsideKept = 8
)

// HeavyCheckOutcome is how a measured launch ended.
type HeavyCheckOutcome string

const (
	HeavyCheckPassed HeavyCheckOutcome = "passed"
	HeavyCheckFailed HeavyCheckOutcome = "failed"
	// HeavyCheckTimedOut is a check stopped by its time limit, its own or what
	// its stage had left.
	HeavyCheckTimedOut HeavyCheckOutcome = "timed_out"
	// HeavyCheckStalled is a check stopped for producing no output for longer
	// than its silence was allowed.
	HeavyCheckStalled HeavyCheckOutcome = "stalled"
	// HeavyCheckStopped is a check its own run stopped, for a reason of the
	// run's rather than the check's.
	HeavyCheckStopped HeavyCheckOutcome = "stopped"
)

// Says is the outcome in the words a person reads.
func (o HeavyCheckOutcome) Says() string {
	switch o {
	case HeavyCheckPassed:
		return "passed"
	case HeavyCheckFailed:
		return "failed"
	case HeavyCheckTimedOut:
		return "stopped by its time limit"
	case HeavyCheckStalled:
		return "stopped for going silent"
	case HeavyCheckStopped:
		return "stopped by its run"
	case "":
		return "ended without its end written down"
	}
	return string(o)
}

// HeavyCheckProcessSeen is a process found running a heavy check's command,
// as the machine's process listing showed it as a check launched.
type HeavyCheckProcessSeen struct {
	PID          int    `json:"pid"`
	ProcessGroup int    `json:"process_group"`
	Check        string `json:"check"`
}

// HeavyCheckReading is what the machine looked like as a check launched, read
// by whoever launches it: the record itself reads nothing but its own permits.
type HeavyCheckReading struct {
	// Load is the one-minute load average, where the platform says, and Cores
	// the number of cores it is read against.
	Load  *float64 `json:"load,omitempty"`
	Cores int      `json:"cores,omitempty"`
	// FreeMemory is the memory the operating system reports free, in bytes,
	// where it says: on macOS the pages it holds no data in at all, which
	// leaves out memory it would give up from its caches.
	FreeMemory *uint64 `json:"free_memory,omitempty"`
	// Listed is false where the machine's processes could not be listed, so
	// no count of suites run outside the places is known; Seen is every
	// process the listing showed running a heavy check's command.
	Listed bool                    `json:"listed,omitempty"`
	Seen   []HeavyCheckProcessSeen `json:"-"`
}

// HeavyCheckMeasurement is one launch of one heavy check.
type HeavyCheckMeasurement struct {
	Permit     string                    `json:"permit"`
	Product    string                    `json:"product"`
	RunID      string                    `json:"run_id"`
	WorkItemID string                    `json:"work_item_id,omitempty"`
	Check      string                    `json:"check"`
	Command    string                    `json:"command"`
	Priority   domain.HeavyCheckPriority `json:"priority"`
	// Limit is how many of Check could run at once as it launched.
	Limit      int        `json:"limit"`
	AskedAt    time.Time  `json:"asked_at"`
	GrantedAt  time.Time  `json:"granted_at"`
	LaunchedAt time.Time  `json:"launched_at"`
	ExitedAt   *time.Time `json:"exited_at,omitempty"`
	// BudgetMS is the time limit the check launched with, in milliseconds:
	// its own, or what its stage had left where that was less.
	BudgetMS int64             `json:"budget_ms,omitempty"`
	Outcome  HeavyCheckOutcome `json:"outcome,omitempty"`
	// Running is how many of each heavy check held a place on the machine as
	// it launched, this launch included.
	Running map[string]int `json:"running"`
	// Machine is the load and free memory as it launched.
	Machine HeavyCheckReading `json:"machine"`
	// Outside counts the processes running a heavy check's command outside
	// any place as it launched, and OutsideSeen names the first few. It is
	// meaningful only where Machine.Listed is true.
	Outside     int                     `json:"outside,omitempty"`
	OutsideSeen []HeavyCheckProcessSeen `json:"outside_seen,omitempty"`
}

// Waited is how long the launch waited for its place.
func (m HeavyCheckMeasurement) Waited() time.Duration {
	if waited := m.GrantedAt.Sub(m.AskedAt); waited > 0 {
		return waited
	}
	return 0
}

// Ran is how long the check ran, from launch to its main process ending; ok
// is false for a launch whose end was never written down.
func (m HeavyCheckMeasurement) Ran() (time.Duration, bool) {
	if m.ExitedAt == nil {
		return 0, false
	}
	if ran := m.ExitedAt.Sub(m.LaunchedAt); ran > 0 {
		return ran, true
	}
	return 0, true
}

// Budget is the time limit the check launched with, and zero where none was
// written down.
func (m HeavyCheckMeasurement) Budget() time.Duration {
	return time.Duration(m.BudgetMS) * time.Millisecond
}

func (m HeavyCheckMeasurement) validate() error {
	var problems []error
	if !heavyCheckIDPattern.MatchString(m.Permit) {
		problems = append(problems, fmt.Errorf("measurement names permit %q", m.Permit))
	}
	if strings.TrimSpace(m.Check) == "" || m.LaunchedAt.IsZero() {
		problems = append(problems, fmt.Errorf("measurement of permit %s names no check or launch", m.Permit))
	}
	return errors.Join(problems...)
}

// HeavyCheckMeasurements is the whole file: the last launches, oldest first.
type HeavyCheckMeasurements struct {
	SchemaVersion int                     `json:"schema_version"`
	Measurements  []HeavyCheckMeasurement `json:"measurements,omitempty"`
}

func (r HeavyCheckMeasurements) validate() error {
	var problems []error
	if r.SchemaVersion != HeavyCheckMeasurementSchemaVersion {
		problems = append(problems, fmt.Errorf("heavy check measurements schema version %d is not supported", r.SchemaVersion))
	}
	for _, measurement := range r.Measurements {
		if err := measurement.validate(); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// readHeavyCheckMeasurements reads the measurements, strictly where they are
// read to be written back, as the record of places is read.
func readHeavyCheckMeasurements(root *repowrite.PinnedRoot, strict bool) (HeavyCheckMeasurements, error) {
	source, err := root.ReadFile(heavyCheckMeasurementsName)
	if errors.Is(err, os.ErrNotExist) {
		return HeavyCheckMeasurements{SchemaVersion: HeavyCheckMeasurementSchemaVersion}, nil
	}
	if err != nil {
		return HeavyCheckMeasurements{}, fmt.Errorf("read the heavy check measurements: %w", err)
	}
	if len(source) > maxEncodedStateBytes {
		return HeavyCheckMeasurements{}, errors.New("the heavy check measurements exceed the state record size bound")
	}
	var record HeavyCheckMeasurements
	if strict {
		err = decodeStrictly(source, &record)
	} else {
		var unknown []string
		unknown, err = decodeTolerating(source, &record)
		noteUnknownFields("heavy check measurements", unknown)
	}
	if err != nil {
		return HeavyCheckMeasurements{}, fmt.Errorf("decode the heavy check measurements: %w", err)
	}
	if err := record.validate(); err != nil {
		return HeavyCheckMeasurements{}, fmt.Errorf("the heavy check measurements are invalid: %w", err)
	}
	return record, nil
}

// measure changes the measurement of one permit, or adds it where change is
// given none, and writes the file back keeping the last few. It is called under
// the record of places' lock. A failure is returned for the caller to set
// aside: no check waits on its own measurement.
func (h *heavyCheckSession) measure(permit string, change func(*HeavyCheckMeasurement) bool) error {
	record, err := readHeavyCheckMeasurements(h.root, true)
	if err != nil {
		return err
	}
	var found *HeavyCheckMeasurement
	for index := range record.Measurements {
		if record.Measurements[index].Permit == permit {
			found = &record.Measurements[index]
		}
	}
	if found == nil {
		record.Measurements = append(record.Measurements, HeavyCheckMeasurement{Permit: permit})
		found = &record.Measurements[len(record.Measurements)-1]
	}
	if !change(found) {
		return nil
	}
	if len(record.Measurements) > heavyCheckMeasurementsKept {
		record.Measurements = record.Measurements[len(record.Measurements)-heavyCheckMeasurementsKept:]
	}
	record.SchemaVersion = HeavyCheckMeasurementSchemaVersion
	if err := record.validate(); err != nil {
		return fmt.Errorf("the heavy check measurements would be invalid: %w", err)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxEncodedStateBytes {
		return errors.New("the heavy check measurements exceed the state record size bound")
	}
	return h.root.WriteFile(heavyCheckMeasurementsName, encoded, 0o600, false)
}

// measureLaunch writes a launch down as it starts: the permit as granted, the
// machine as the launcher read it, and every place held, this one included.
// A process the listing showed in the process group of any check holding a
// place is that check's own; every other one running a heavy check's command
// is a suite run outside the places.
func (h *heavyCheckSession) measureLaunch(permit HeavyCheckPermit, reading HeavyCheckReading) error {
	running := map[string]int{}
	groups := map[int]bool{}
	for _, holding := range h.record.Holding("") {
		running[holding.Check]++
		if holding.Process != nil {
			groups[holding.Process.ProcessGroup] = true
		}
	}
	var outside []HeavyCheckProcessSeen
	for _, seen := range reading.Seen {
		if !groups[seen.ProcessGroup] {
			outside = append(outside, seen)
		}
	}
	sort.SliceStable(outside, func(first, second int) bool { return outside[first].PID < outside[second].PID })
	count := len(outside)
	if len(outside) > heavyCheckOutsideKept {
		outside = outside[:heavyCheckOutsideKept]
	}
	reading.Seen = nil
	return h.measure(permit.ID, func(m *HeavyCheckMeasurement) bool {
		*m = HeavyCheckMeasurement{
			Permit: permit.ID, Product: permit.Product, RunID: permit.RunID, WorkItemID: permit.WorkItemID,
			Check: permit.Check, Command: boundedReason(permit.Command), Priority: permit.Priority,
			Limit: h.record.Limits[permit.Check], AskedAt: permit.AskedAt, GrantedAt: *permit.GrantedAt,
			LaunchedAt: *permit.LaunchedAt, Running: running, Machine: reading,
		}
		if reading.Listed {
			m.Outside, m.OutsideSeen = count, outside
		}
		return true
	})
}

// measureExit finishes a launch's measurement as its main process ends.
func (h *heavyCheckSession) measureExit(permit HeavyCheckPermit, outcome HeavyCheckOutcome, budget time.Duration) error {
	return h.measure(permit.ID, func(m *HeavyCheckMeasurement) bool {
		if m.LaunchedAt.IsZero() || m.ExitedAt != nil {
			// A launch never written down as started has nothing to finish,
			// and one already finished keeps what it was finished with.
			return false
		}
		m.ExitedAt = permit.ExitedAt
		m.Outcome = outcome
		if budget > 0 {
			m.BudgetMS = budget.Milliseconds()
		}
		return true
	})
}

// Measurements is the measurements as they stand, read without the lock: what
// a surface shows. The file is replaced whole, so a reader never sees half.
func (s *HeavyCheckStore) Measurements() (HeavyCheckMeasurements, error) {
	root, err := s.pin()
	if err != nil {
		return HeavyCheckMeasurements{}, err
	}
	defer root.Close()
	return readHeavyCheckMeasurements(root, false)
}

// Measure says how the launch this grant is for ended, before Finish writes
// its end down: outcome as the check runner judged it, and the time limit it
// launched with.
func (g *HeavyCheckGrant) Measure(outcome HeavyCheckOutcome, budget time.Duration) {
	g.outcome, g.budget = outcome, budget
}
