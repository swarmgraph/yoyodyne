package runstate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// PowerEvent is a transition reported by the operating system. Dark wakes
// count as wakes: the machine can run processes during them.
type PowerEvent struct {
	At     time.Time `json:"at"`
	Awake  bool      `json:"awake"`
	Source string    `json:"source"`
}

const (
	// MachineObservationInterval is the minimum time between supervisor looks.
	MachineObservationInterval = time.Minute
	// MachineObservationWindow bounds the interval consecutive looks can
	// establish. The extra minute allows the supervisor's poll after each tick,
	// processing and timer delays; longer gaps leave presence unknown.
	MachineObservationWindow = 2 * MachineObservationInterval
)

// MachineObservation records OS power history and the scheduler lease as the
// supervisor saw them. A missing observation is unknown, never proof of sleep.
type MachineObservation struct {
	At           time.Time    `json:"at"`
	Watching     bool         `json:"watching"`
	Power        []PowerEvent `json:"power,omitempty"`
	PowerProblem string       `json:"power_problem,omitempty"`
}

// RecordMachine appends under a file lock, in the supervisor's state directory
// outside the repository, with the same durability as the watch log.
func (s *SupervisionStore) RecordMachine(ctx context.Context, observation MachineObservation) error {
	if observation.At.IsZero() {
		return errors.New("a machine observation requires its time")
	}
	// The log is its own lock: it is opened through the confined writer as a
	// lock file, read-only, and what is appended goes through the checked
	// batches rather than through that descriptor.
	path := filepath.Join(s.root, "machine.jsonl")
	file, err := s.state.openLock(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := lockStateFile(ctx, file); err != nil {
		return err
	}
	defer unlockStateFile(file)
	encoded, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxEncodedStateBytes {
		return errors.New("machine observation exceeds the state record size bound")
	}
	if err := closeOffATornFragment(file, &encoded); err != nil {
		return err
	}
	return s.state.appendTo(path, encoded)
}

func (s *SupervisionStore) MachineHistory() ([]MachineObservation, error) {
	file, err := os.Open(filepath.Join(s.root, "machine.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxEncodedStateBytes)
	var observations []MachineObservation
	var problems []error
	for scanner.Scan() {
		var observation MachineObservation
		if err := json.Unmarshal(scanner.Bytes(), &observation); err != nil {
			problems = append(problems, fmt.Errorf("read machine history: %w", err))
			continue
		}
		if observation.At.IsZero() {
			problems = append(problems, errors.New("machine history carries an observation without a time"))
			continue
		}
		observations = append(observations, observation)
	}
	return observations, errors.Join(append(problems, scanner.Err())...)
}
