package runstate

// What the checks run on the head of the target branch came to, one record per
// run (internal/maincheck).
//
// The supervisor runs the project's main_checks whole on the branch's head
// every execution.main_check_interval, whenever the branch has moved since the
// last run. Each run is recorded here with the commit it ran on, when it
// started and ended, and how each check ended, so `yoyo status` can say when
// the branch last passed and a failure's report can name the last commit that
// did. The record is also what keeps the schedule across a restart of the
// supervisor: the next run is due an interval after the latest one started,
// not an interval after the supervisor came up.
//
// It is one file under the product, written whole through the shared write
// primitive. Only the supervisor writes it; every surface reads it.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// MainCheckRunSchemaVersion is 1 and has never changed.
const MainCheckRunSchemaVersion = 1

// MaxMainCheckTextBytes bounds what one record says in each of its texts.
const MaxMainCheckTextBytes = 4 << 10

// maxMainCheckRuns bounds how many runs the file keeps, oldest dropped first:
// at one run every four hours this is about ten days, and what a later run
// reads is the latest run and the latest one that passed.
const maxMainCheckRuns = 64

// maxMainCheckFailingNames bounds how many failing tests one check records.
const maxMainCheckFailingNames = 20

// MainCheckOutcome is how one run, or one check in it, ended.
type MainCheckOutcome string

const (
	// MainCheckPassed is every check passing.
	MainCheckPassed MainCheckOutcome = "passed"
	// MainCheckFailed is a check that ran and failed.
	MainCheckFailed MainCheckOutcome = "failed"
	// MainCheckCouldNotRun is a run that never got as far as a check's result:
	// the branch's head could not be read, its checkout could not be made, or
	// every check that did not pass said it could not run. It says nothing
	// about the commit, so the next run tries the same commit again.
	MainCheckCouldNotRun MainCheckOutcome = "could-not-run"
)

func (o MainCheckOutcome) Valid() bool {
	switch o {
	case MainCheckPassed, MainCheckFailed, MainCheckCouldNotRun:
		return true
	}
	return false
}

// Words is the outcome as a person reads it.
func (o MainCheckOutcome) Words() string {
	switch o {
	case MainCheckPassed:
		return "passed"
	case MainCheckFailed:
		return "failed"
	case MainCheckCouldNotRun:
		return "could not run"
	}
	return string(o)
}

var mainCheckCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// MainCheckResult is how one check of a run ended.
type MainCheckResult struct {
	Command string           `json:"command"`
	Outcome MainCheckOutcome `json:"outcome"`
	// Took is how long the check ran.
	Took time.Duration `json:"took"`
	// FailingTests are the tests or packages a failed check's output named
	// failing, up to maxMainCheckFailingNames.
	FailingTests []string `json:"failing_tests,omitempty"`
	// Detail is the reason a check could not run, or how a failed one ended
	// where its output named no failing test.
	Detail string `json:"detail,omitempty"`
}

// MainCheckRun is one run of the main checks on one commit.
type MainCheckRun struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Branch is the target branch the run read, and Commit the head it ran on.
	// Commit is empty only on a run that could not read the head.
	Branch    string            `json:"branch"`
	Commit    string            `json:"commit,omitempty"`
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"ended_at"`
	Outcome   MainCheckOutcome  `json:"outcome"`
	Checks    []MainCheckResult `json:"checks,omitempty"`
	// Detail is what the run as a whole said: why it could not run, or where
	// the head was read from when the remote could not be reached.
	Detail string `json:"detail,omitempty"`
	// ReportID is the report a failed run filed, where it filed one.
	ReportID string `json:"report_id,omitempty"`
}

func (r MainCheckRun) Validate() error {
	var problems []error
	if r.SchemaVersion != MainCheckRunSchemaVersion {
		problems = append(problems, fmt.Errorf("main check run schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if r.Branch == "" {
		problems = append(problems, errors.New("a main check run names the branch it read"))
	}
	if r.Commit != "" && !mainCheckCommitPattern.MatchString(r.Commit) {
		problems = append(problems, fmt.Errorf("main check commit %q is not a whole object name", r.Commit))
	}
	if r.Commit == "" && r.Outcome != MainCheckCouldNotRun {
		problems = append(problems, errors.New("a main check run that ran names the commit it ran on"))
	}
	if !r.Outcome.Valid() {
		problems = append(problems, fmt.Errorf("main check outcome %q is not one this harness names", r.Outcome))
	}
	if r.StartedAt.IsZero() || r.EndedAt.IsZero() {
		problems = append(problems, errors.New("started_at and ended_at are required"))
	}
	if len(r.Detail) > MaxMainCheckTextBytes {
		problems = append(problems, fmt.Errorf("a main check run says at most %d bytes in its detail", MaxMainCheckTextBytes))
	}
	for _, check := range r.Checks {
		if !check.Outcome.Valid() {
			problems = append(problems, fmt.Errorf("main check %q outcome %q is not one this harness names", check.Command, check.Outcome))
		}
		if len(check.Detail) > MaxMainCheckTextBytes || len(check.Command) > MaxMainCheckTextBytes {
			problems = append(problems, fmt.Errorf("main check %q says more than %d bytes", check.Command, MaxMainCheckTextBytes))
		}
		if len(check.FailingTests) > maxMainCheckFailingNames {
			problems = append(problems, fmt.Errorf("main check %q names more than %d failing tests", check.Command, maxMainCheckFailingNames))
		}
	}
	return errors.Join(problems...)
}

// BoundMainCheckText folds prose to a line inside a record's bound.
func BoundMainCheckText(text string) string {
	return oneline.Fold(text, MaxMainCheckTextBytes-len(oneline.Marker))
}

// BoundMainCheckFailingTests keeps the first names a record may hold.
func BoundMainCheckFailingTests(names []string) []string {
	if len(names) > maxMainCheckFailingNames {
		names = names[:maxMainCheckFailingNames]
	}
	return append([]string(nil), names...)
}

type mainCheckFile struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Runs          []MainCheckRun   `json:"runs"`
}

// MainCheckStore is where the runs are recorded.
type MainCheckStore struct {
	stateRoot string
	anchor    string
	productID domain.ProductID
}

func NewMainCheckStore(root string, productID domain.ProductID) (*MainCheckStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the main check runs' state root: %w", err)
	}
	return &MainCheckStore{stateRoot: stateRoot, anchor: anchor, productID: productID}, nil
}

const mainCheckFileName = "main-checks.json"

func (s *MainCheckStore) relative() string {
	return filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), mainCheckFileName)
}

// Path names the file, so a reading that fails can say where the record is.
func (s *MainCheckStore) Path() string { return filepath.Join(s.stateRoot, s.relative()) }

// List reports every recorded run, oldest first. No file is no runs.
func (s *MainCheckStore) List() ([]MainCheckRun, error) {
	encoded, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read main check runs: %w", err)
	}
	if len(encoded) > maxEncodedStateBytes {
		return nil, fmt.Errorf("main check runs are %d bytes, limit is %d", len(encoded), maxEncodedStateBytes)
	}
	var recorded mainCheckFile
	if _, err := decodeTolerating(encoded, &recorded); err != nil {
		return nil, fmt.Errorf("decode main check runs: %w", err)
	}
	if recorded.SchemaVersion != MainCheckRunSchemaVersion {
		return nil, fmt.Errorf("main check runs schema version %d is not supported", recorded.SchemaVersion)
	}
	if recorded.ProductID != s.productID {
		return nil, fmt.Errorf("main check runs belong to product %q, not %q", recorded.ProductID, s.productID)
	}
	for _, run := range recorded.Runs {
		if err := run.Validate(); err != nil {
			return nil, fmt.Errorf("main check run at %s: %w", run.StartedAt.UTC().Format(time.RFC3339), err)
		}
	}
	return recorded.Runs, nil
}

// Append records a run, dropping the oldest past the file's bound.
func (s *MainCheckStore) Append(run MainCheckRun) error {
	if run.ProductID != s.productID {
		return fmt.Errorf("main check run product %q does not match store product %q", run.ProductID, s.productID)
	}
	if err := run.Validate(); err != nil {
		return err
	}
	runs, err := s.List()
	if err != nil {
		return err
	}
	runs = append(runs, run)
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].StartedAt.Before(runs[j].StartedAt) })
	if len(runs) > maxMainCheckRuns {
		runs = runs[len(runs)-maxMainCheckRuns:]
	}
	encoded, err := encodeRecord("main check runs", mainCheckFile{SchemaVersion: MainCheckRunSchemaVersion, ProductID: s.productID, Runs: runs})
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedStateBytes {
		return fmt.Errorf("encoded main check runs are %d bytes, limit is %d", len(encoded), maxEncodedStateBytes)
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return fmt.Errorf("pin the main check runs' state root: %w", err)
	}
	defer root.Close()
	if err := root.MakeDirectory(filepath.Dir(s.relative()), 0o700); err != nil {
		return fmt.Errorf("create the product's state directory: %w", err)
	}
	if err := root.WriteFile(s.relative(), encoded, 0o600, false); err != nil {
		return fmt.Errorf("record main check run: %w", err)
	}
	return root.Unchanged()
}

// MainCheckStanding is what the record says about the branch now: the latest
// run, the latest run that passed, and the latest run that ended with a result
// about its commit.
type MainCheckStanding struct {
	Latest       *MainCheckRun
	LatestPassed *MainCheckRun
	LatestResult *MainCheckRun
}

// ReadMainCheckStanding picks the three runs out of a list oldest first.
func ReadMainCheckStanding(runs []MainCheckRun) MainCheckStanding {
	var standing MainCheckStanding
	for index := len(runs) - 1; index >= 0; index-- {
		run := runs[index]
		if standing.Latest == nil {
			standing.Latest = &run
		}
		if standing.LatestResult == nil && run.Outcome != MainCheckCouldNotRun {
			standing.LatestResult = &run
		}
		if standing.LatestPassed == nil && run.Outcome == MainCheckPassed {
			standing.LatestPassed = &run
		}
		if standing.Latest != nil && standing.LatestResult != nil && standing.LatestPassed != nil {
			break
		}
	}
	return standing
}
