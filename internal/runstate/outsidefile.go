package runstate

// The run that brings a pinned outside file in, and what the harness keeps
// about each record between its attempts (docs/designs/pinned-outside-files.md).
//
// The run is the harness's own: it fetches the file a record on the target
// branch declares, and the license file the record names where that is pinned
// too and not yet on the target branch, writes them and their records' harness
// fields, and puts that through the full checks and an independent review like
// any change. It holds
// no developer slot, and at most one is in flight at a time
// (State.HoldsOutsideFileLanding), so no developer run waits on it.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/outsidefile"
)

// OutsideFileLandings is how many runs that bring a file in may be in flight at
// once: one, whatever the configuration says.
const OutsideFileLandings = 1

// MaxOutsideFileAttempts is how many runs one record, as the target branch
// holds it, is given when each stops for a cause that judged nothing about it —
// a check stopped by a time limit, the forge, the machine. Once this many have
// stopped, the record is stopped too and reported, rather than spending the
// machine's heavy checks on it without end.
const MaxOutsideFileAttempts = 3

// MaxOutsideFileQueueFailures is how many runs in a row for one record, as the
// target branch holds it, may have their change refused by the merge queue —
// the queue's combined change failing its checks or its review, or not
// applying to the target as it then stood — before the record is stopped and
// reported. Each such run ends, nothing is handed to a developer, and a later
// pull starts a fresh run from the record as the target branch then holds it.
const MaxOutsideFileQueueFailures = 3

// MaxOutsideFileRuns bounds the runs one record's content is given in all: its
// stops that judged nothing and its merge queue failures, which stop the
// record when either count is spent.
const MaxOutsideFileRuns = MaxOutsideFileAttempts + MaxOutsideFileQueueFailures

// MaxOutsideFileFieldBytes bounds each text a run that brings a file in
// records about its record: a path, an address, a version, a work item.
const MaxOutsideFileFieldBytes = 2048

// OutsideFileLanding is what a run that brings a file in is for: one record, as
// one commit of the target branch held it.
type OutsideFileLanding struct {
	// Record is the record's repository path.
	Record string `json:"record"`
	// RecordDigest is the SHA-256 of the record's content on the target branch
	// when the run was started; a record that changes is a new landing.
	RecordDigest string `json:"record_digest"`
	// TargetCommit is the target branch's commit the record was read at.
	TargetCommit string `json:"target_commit"`
	// For is the work item the record names, which the run is attributed to.
	For string `json:"for"`
	// URL and Version are the record's, kept so the run's record says what was
	// asked for without the record file.
	URL     string `json:"url"`
	Version string `json:"version"`
	// Attempt counts from zero, as RunIDFor does.
	Attempt int `json:"attempt,omitempty"`
	// Fetched is what the fetch found, once it has.
	Fetched *OutsideFileFetch `json:"fetched,omitempty"`
	// License is the license file the record names, brought in by the same run
	// where it is itself a pinned file with a record on the target branch and
	// the target branch does not hold it yet. One run brings both in, so the
	// file's checks see the license its record names. It never names a license
	// of its own.
	License *OutsideFileLanding `json:"license,omitempty"`
}

// Records is every record the landing writes: its own, and its license's
// where it brings the license in with it.
func (o OutsideFileLanding) Records() []string {
	records := []string{o.Record}
	if o.License != nil {
		records = append(records, o.License.Record)
	}
	return records
}

// Paths is every path the landing may change: each record it writes and the
// file each declares.
func (o OutsideFileLanding) Paths() []string {
	var paths []string
	for _, record := range o.Records() {
		paths = append(paths, outsidefile.PinnedPath(record), record)
	}
	return paths
}

// OutsideFileFetch is what one fetch found, as the reviewer is shown it.
type OutsideFileFetch struct {
	FinalURL string `json:"final_url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	// Given reports that the record gave the checksum the file matched; false
	// is a file trusted on its first fetch.
	Given bool      `json:"given,omitempty"`
	At    time.Time `json:"at"`
}

// RunID is the run that makes this landing's attempt.
func (o OutsideFileLanding) RunID() string {
	return OutsideFileRunID(o.Record, o.RecordDigest, o.Attempt)
}

// OutsideFileRunID is the run that makes one attempt at bringing in the file one
// record declares, as one content of the record says it. It is derived rather
// than drawn, so a scheduler pass and a restarted one name the same run and the
// reservation refuses the second.
func OutsideFileRunID(record, digest string, attempt int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "outside file\x00%s\x00%s\x00%d", record, digest, attempt))
	return fmt.Sprintf("run-%x", sum[:16])
}

// WorkItemID is the bookkeeping item the run is recorded under. It is the
// run's own rather than the record's for item, so nothing that reads a run
// as its item's latest mistakes it for a developer's.
func (o OutsideFileLanding) WorkItemID() string {
	return OutsideFileItemPrefix + strings.TrimPrefix(o.RunID(), "run-")
}

// OutsideFileItemPrefix opens the bookkeeping item of every run that brings a
// file in, and no work item the tracker holds.
const OutsideFileItemPrefix = "outsidefile-"

// OutsideFileRunOfItem is the run a bookkeeping item names, and false for a
// work item the tracker holds.
func OutsideFileRunOfItem(id string) (string, bool) {
	rest, ok := strings.CutPrefix(id, OutsideFileItemPrefix)
	if !ok || !runIDPattern.MatchString("run-"+rest) {
		return "", false
	}
	return "run-" + rest, true
}

func (o OutsideFileLanding) Validate() error {
	var problems []string
	if !outsidefile.IsRecord(o.Record) {
		problems = append(problems, fmt.Sprintf("record %q is not a record path", o.Record))
	}
	if len(o.RecordDigest) != 64 {
		problems = append(problems, "record digest must be a SHA-256")
	}
	if !commitPattern.MatchString(o.TargetCommit) {
		problems = append(problems, "target commit must be a full commit")
	}
	if strings.TrimSpace(o.For) == "" {
		problems = append(problems, "the landing must name the work item its record is for")
	}
	if o.Attempt < 0 || o.Attempt >= MaxOutsideFileRuns {
		problems = append(problems, fmt.Sprintf("attempt %d is outside 0 to %d", o.Attempt, MaxOutsideFileRuns-1))
	}
	for name, value := range map[string]string{"record": o.Record, "for": o.For, "url": o.URL, "version": o.Version} {
		if len(value) > MaxOutsideFileFieldBytes {
			problems = append(problems, fmt.Sprintf("%s is %d bytes, more than %d", name, len(value), MaxOutsideFileFieldBytes))
		}
	}
	if f := o.Fetched; f != nil && len(f.FinalURL) > MaxOutsideFileFieldBytes {
		problems = append(problems, fmt.Sprintf("final address is %d bytes, more than %d", len(f.FinalURL), MaxOutsideFileFieldBytes))
	}
	if f := o.Fetched; f != nil && (f.FinalURL == "" || len(f.SHA256) != 64 || f.Size <= 0 || f.At.IsZero()) {
		problems = append(problems, "a fetch records its final address, checksum, size and time")
	}
	if license := o.License; license != nil {
		switch {
		case license.License != nil:
			problems = append(problems, "the license brought in with a file brings in no license of its own")
		case license.Record == o.Record:
			problems = append(problems, "the license brought in with a file is a record of its own")
		case license.TargetCommit != o.TargetCommit:
			problems = append(problems, "the license brought in with a file is read at the same target commit as the file")
		}
		if err := license.Validate(); err != nil {
			problems = append(problems, "license: "+err.Error())
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// PricedAgainst is the work item a run's cost is counted on: its own item, or,
// for a run that brings a file in, the item its record names, which is the
// item that asked for the file.
func (s State) PricedAgainst() string {
	if s.OutsideFile != nil {
		return s.OutsideFile.For
	}
	return s.WorkItemID
}

// HoldsOutsideFileLanding reports a run that brings a file in, in flight.
func (s State) HoldsOutsideFileLanding() bool {
	return s.Status.InFlight() && s.OutsideFile != nil
}

// HarnessAuthored reports a run whose change the harness wrote rather than a
// developer: a confirmed document's landing, or a run that brings a file in.
// Neither invokes a developer, so neither owes a developer's account or a
// developer session to integrate.
func (s State) HarnessAuthored() bool { return s.Document != nil || s.OutsideFile != nil }

// HoldingOutsideFileLandings counts the runs among states that bring a file in.
func HoldingOutsideFileLandings(states []State) int {
	holding := 0
	for _, state := range states {
		if state.HoldsOutsideFileLanding() {
			holding++
		}
	}
	return holding
}

// ReserveOutsideFileLanding is Reserve for a run that brings a file in: under
// the same lock and with the same duplicate check, refused only while another
// such run is in flight, and never counted against the developer slots.
func (s *Store) ReserveOutsideFileLanding(ctx context.Context, state State) (*Lease, error) {
	if state.OutsideFile == nil {
		return nil, errors.New("only a run that brings a file in is reserved as one")
	}
	return s.reserve(ctx, state, func(active []State) error {
		if holding := HoldingOutsideFileLandings(active); holding >= OutsideFileLandings {
			return CapacityError{Limit: OutsideFileLandings, Active: holding, OutsideFiles: true}
		}
		return nil
	})
}

// OutsideFileAttemptSchemaVersion is 1 and has never changed.
const OutsideFileAttemptSchemaVersion = 1

// OutsideFileAttempt is what the harness keeps about one record between the
// scheduler passes that consider it: when its last try ended without judging
// it, which attempt is next, and whether it is stopped. It is keyed by the
// record's path and holds for the record's content it names; a record whose
// content changed on the target branch starts again from nothing.
type OutsideFileAttempt struct {
	SchemaVersion int    `json:"schema_version"`
	Record        string `json:"record"`
	RecordDigest  string `json:"record_digest"`
	// Attempt is the run the next try makes, counted from zero; every run that
	// ends without bringing the file in moves it on.
	Attempt int `json:"attempt,omitempty"`
	// Unjudged counts the runs that stopped without anything judging the file:
	// a check stopped by a time limit, the forge, the machine.
	Unjudged int `json:"unjudged,omitempty"`
	// QueueFailures is why each run in a row had its change refused by the
	// merge queue, oldest first.
	QueueFailures []string `json:"queue_failures,omitempty"`
	// RetryFrom is when the last try that judged nothing about the record ended
	// — a fetch the network stopped, or a run stopped by a time limit, the
	// forge, or the machine — and Waited is what stopped it. The record is tried
	// again once outsidefile.RetryAfter has passed. A network failure is never
	// counted.
	RetryFrom *time.Time `json:"retry_from,omitempty"`
	Waited    string     `json:"waited,omitempty"`
	// Stopped is why the record is stopped: a failure about the record, which no
	// later attempt gets past while the record says what it says. It is tried
	// again once the record changes on the target branch, or, for a stop that
	// names StoppedBuild, once another build is running.
	Stopped   string     `json:"stopped,omitempty"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	// StoppedBuild is the harness build that stopped the record where the stop
	// was the merge step refusing a run for evidence another run of that build
	// would lack too. Such a stop is the harness's fault rather than the
	// record's, so it is lifted once a different build is running as well as
	// when the record changes. Empty for every other stop.
	StoppedBuild string `json:"stopped_build,omitempty"`
	// Reported says the warning report for the stop was filed, so a stop is
	// reported once.
	Reported bool `json:"reported,omitempty"`
}

// For reports whether the attempt describes the record's content now.
func (a OutsideFileAttempt) For(digest string) bool { return a.RecordDigest == digest }

// Waiting reports a record whose last try ended too recently to try again.
func (a OutsideFileAttempt) Waiting(now time.Time) bool {
	return a.RetryFrom != nil && now.Sub(*a.RetryFrom) < outsidefile.RetryAfter
}

func (a OutsideFileAttempt) Validate() error {
	var problems []string
	if a.SchemaVersion != OutsideFileAttemptSchemaVersion {
		problems = append(problems, fmt.Sprintf("schema version %d is not supported", a.SchemaVersion))
	}
	if !outsidefile.IsRecord(a.Record) {
		problems = append(problems, fmt.Sprintf("record %q is not a record path", a.Record))
	}
	if len(a.RecordDigest) != 64 {
		problems = append(problems, "record digest must be a SHA-256")
	}
	if a.Attempt < 0 || a.Attempt > MaxOutsideFileRuns {
		problems = append(problems, fmt.Sprintf("attempt %d is outside 0 to %d", a.Attempt, MaxOutsideFileRuns))
	}
	if len(a.StoppedBuild) > MaxOutsideFileFieldBytes || (a.StoppedBuild != "" && a.Stopped == "") {
		problems = append(problems, "a build is recorded only beside the stop it made, and is at most a field's length")
	}
	if a.Unjudged < 0 || a.Unjudged > MaxOutsideFileAttempts || len(a.QueueFailures) > MaxOutsideFileQueueFailures {
		problems = append(problems, "more runs are counted than one record is given")
	}
	if len(problems) > 0 {
		return fmt.Errorf("what is kept about a pinned record: %s", strings.Join(problems, "; "))
	}
	return nil
}

// OutsideFileAttempt reads what is kept about one record, reporting whether
// anything is.
func (s *Store) OutsideFileAttempt(record string) (OutsideFileAttempt, bool, error) {
	file, err := os.Open(s.outsideFileAttemptPath(record))
	if errors.Is(err, os.ErrNotExist) {
		return OutsideFileAttempt{}, false, nil
	}
	if err != nil {
		return OutsideFileAttempt{}, false, fmt.Errorf("open what is kept about the record %s: %w", record, err)
	}
	defer file.Close()
	var attempt OutsideFileAttempt
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&attempt); err != nil {
		return OutsideFileAttempt{}, false, fmt.Errorf("read what is kept about the record %s: %w", record, err)
	}
	if err := attempt.Validate(); err != nil {
		return OutsideFileAttempt{}, false, err
	}
	if attempt.Record != record {
		return OutsideFileAttempt{}, false, fmt.Errorf("what is kept for the record %s names %s", record, attempt.Record)
	}
	return attempt, true, nil
}

// SaveOutsideFileAttempt writes what is kept about one record, replacing what
// was kept before.
func (s *Store) SaveOutsideFileAttempt(attempt OutsideFileAttempt) error {
	attempt.SchemaVersion = OutsideFileAttemptSchemaVersion
	attempt.Waited = boundRecordedText(attempt.Waited, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	attempt.Stopped = boundRecordedText(attempt.Stopped, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	for i := range attempt.QueueFailures {
		attempt.QueueFailures[i] = boundRecordedText(attempt.QueueFailures[i], MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	}
	if err := attempt.Validate(); err != nil {
		return err
	}
	directory := s.outsideFileAttemptDirectory()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create directory of what is kept about pinned records: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".outsidefile-*.tmp")
	if err != nil {
		return fmt.Errorf("create the temporary record of what is kept about a pinned record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure the temporary record of what is kept about a pinned record: %w", err)
	}
	if err := writeJSONFile(temporary, "pinned record attempt", attempt); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close the temporary record of what is kept about a pinned record: %w", err)
	}
	if err := os.Rename(temporaryPath, s.outsideFileAttemptPath(attempt.Record)); err != nil {
		return fmt.Errorf("record what is kept about a pinned record: %w", err)
	}
	return syncDirectory(directory)
}

func (s *Store) outsideFileAttemptDirectory() string {
	return filepath.Join(s.root, "outsidefiles")
}

// outsideFileAttemptPath names a record's file by a digest of its path, so a
// path of any shape is one plain file name.
func (s *Store) outsideFileAttemptPath(record string) string {
	sum := sha256.Sum256([]byte(record))
	return filepath.Join(s.outsideFileAttemptDirectory(), fmt.Sprintf("%x.json", sum[:16]))
}
