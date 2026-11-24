package runstate

// The record of the unmet-goal reviews (docs/designs/unmet-goal-review.md).
//
// Two things are kept, because the next review needs both. Each review is
// recorded as it starts — when, why, the goals it goes through and their
// fingerprints, the goals it found unchanged and since when, and the items the
// operator raised on purpose — and what became of it once its pass is over.
// And, for each goal, the fingerprint and start time of the last review that
// went through it and completed, which is what the next review compares
// against. A review whose pass did not complete leaves that second half alone,
// so a goal nobody actually went through is never said to be unchanged since.
//
// Recording the daily review is also what makes it once a day: the record is
// locked across every Yoyodyne process, and a second daily review for a day
// already recorded is refused, so two sessions pulling at 09:00 start one.
//
// It lives under the state root beside the other passes' bookkeeping, never in
// the repository: it is the harness's record, not anything a person edits.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// GoalReviewsSchemaVersion is the shape of the stored record.
const GoalReviewsSchemaVersion = 1

// MaxGoalReviewsKept bounds how many reviews the record keeps, newest last. The
// record is read at every pull, and each review carries its report.
const MaxGoalReviewsKept = 30

const (
	goalReviewsDirectory = "product-manager"
	goalReviewsFile      = "goal-reviews.json"
	goalReviewsLockFile  = ".goal-reviews.lock"
)

// GoalReviewDaily is the trigger of the review that runs once a day.
const GoalReviewDaily = "daily"

// What became of a review.
const (
	// GoalReviewStarted is a review whose pass has been started and has not
	// finished.
	GoalReviewStarted = "started"
	// GoalReviewCompleted is a review whose pass completed.
	GoalReviewCompleted = "completed"
	// GoalReviewNothingChanged is a review in which every goal was unchanged,
	// so no pass was started and no provider call was spent.
	GoalReviewNothingChanged = "nothing changed"
	// GoalReviewNotCompleted is a review whose pass did not complete.
	GoalReviewNotCompleted = "not completed"
)

// ErrGoalReviewRecorded is what recording a daily review for a day that
// already has one unwraps to.
var ErrGoalReviewRecorded = errors.New("a daily review is already recorded for that day")

// GoalReviewed is what the last completed review that went through a goal
// read, and when that review started.
type GoalReviewed struct {
	Fingerprint string    `json:"fingerprint"`
	ReviewedAt  time.Time `json:"reviewed_at"`
}

// GoalReviewEntry is one goal in one review.
type GoalReviewEntry struct {
	Goal        string `json:"goal"`
	Fingerprint string `json:"fingerprint"`
	// Since is, on an unchanged goal, when the review that last went through it
	// started.
	Since time.Time `json:"since,omitempty"`
}

// GoalReview is one review.
type GoalReview struct {
	Trigger string `json:"trigger"`
	// Day is the operator's local day a daily review is for.
	Day       string    `json:"day,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// GoneThrough is the goals the review's pass goes through: every goal
	// changed since a review last went through it.
	GoneThrough []GoalReviewEntry `json:"gone_through,omitempty"`
	// Unchanged is the goals said in one line and not gone through again.
	Unchanged []GoalReviewEntry `json:"unchanged,omitempty"`
	// Raised is the open items carrying the label the operator's raising puts
	// on an item.
	Raised []string `json:"raised,omitempty"`
	// Report is what the review said, as its pass was handed it or, where no
	// pass was needed, as it stands.
	Report     string    `json:"report,omitempty"`
	Outcome    string    `json:"outcome"`
	Problem    string    `json:"problem,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// GoalReviews is the record.
type GoalReviews struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Goals is, by goal reference, the last completed review that went through
	// each goal.
	Goals map[string]GoalReviewed `json:"goals,omitempty"`
	// Reviews is the most recent reviews, oldest first.
	Reviews   []GoalReview `json:"reviews,omitempty"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// Daily is the daily review recorded for a day, if any.
func (r GoalReviews) Daily(day string) (GoalReview, bool) {
	for index := len(r.Reviews) - 1; index >= 0; index-- {
		if review := r.Reviews[index]; review.Trigger == GoalReviewDaily && review.Day == day {
			return review, true
		}
	}
	return GoalReview{}, false
}

// Last is the most recent review, if any.
func (r GoalReviews) Last() (GoalReview, bool) {
	if len(r.Reviews) == 0 {
		return GoalReview{}, false
	}
	return r.Reviews[len(r.Reviews)-1], true
}

// Validate reports every contract violation in the record at once.
func (r GoalReviews) Validate() error {
	var problems []error
	if r.SchemaVersion != GoalReviewsSchemaVersion {
		problems = append(problems, fmt.Errorf("goal review record schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	for goal, reviewed := range r.Goals {
		if strings.TrimSpace(goal) == "" || reviewed.Fingerprint == "" || reviewed.ReviewedAt.IsZero() {
			problems = append(problems, fmt.Errorf("goal %q must be recorded with a fingerprint and the time of the review that went through it", goal))
		}
	}
	for index, review := range r.Reviews {
		if review.Trigger == "" || review.StartedAt.IsZero() || review.Outcome == "" {
			problems = append(problems, fmt.Errorf("reviews[%d] must say why it ran, when it started, and what became of it", index))
		}
		if review.Trigger == GoalReviewDaily && review.Day == "" {
			problems = append(problems, fmt.Errorf("reviews[%d] is a daily review with no day", index))
		}
	}
	if len(r.Reviews) > MaxGoalReviewsKept {
		problems = append(problems, fmt.Errorf("%d reviews recorded, limit is %d", len(r.Reviews), MaxGoalReviewsKept))
	}
	if r.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated_at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid goal review record: %w", err)
	}
	return nil
}

// GoalReviewStore keeps the record for one product.
type GoalReviewStore struct {
	root      string
	productID domain.ProductID
}

// NewGoalReviewStore opens the record kept under a state root.
func NewGoalReviewStore(root string, productID domain.ProductID) (*GoalReviewStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &GoalReviewStore{root: home.ProductDirectory(root, string(productID)), productID: productID}, nil
}

// GoalReviews is the record for this run store's product.
func (s *Store) GoalReviews() *GoalReviewStore {
	return &GoalReviewStore{root: filepath.Dir(s.root), productID: s.productID}
}

// Path names the record, so a failure can say where it is.
func (s *GoalReviewStore) Path() string {
	return filepath.Join(s.root, goalReviewsDirectory, goalReviewsFile)
}

// Load reads the record. No record is the ordinary answer before the first
// review; one that cannot be read is a failure, because reading past it as
// empty would go through every goal again and could start a second review on a
// day that has one.
func (s *GoalReviewStore) Load() (GoalReviews, bool, error) {
	stored, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return GoalReviews{}, false, nil
	}
	if err != nil {
		return GoalReviews{}, false, fmt.Errorf("read the goal review record: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(stored)))
	decoder.DisallowUnknownFields()
	var record GoalReviews
	if err := decoder.Decode(&record); err != nil {
		return GoalReviews{}, false, fmt.Errorf("decode the goal review record at %s: %w", s.Path(), err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return GoalReviews{}, false, fmt.Errorf("decode the goal review record at %s: %w", s.Path(), err)
	}
	if err := record.Validate(); err != nil {
		return GoalReviews{}, false, err
	}
	if record.ProductID != s.productID {
		return GoalReviews{}, false, fmt.Errorf("the goal review record at %s belongs to product %q, not %q", s.Path(), record.ProductID, s.productID)
	}
	return record, true, nil
}

// Begin records a review as it starts. A daily review for a day that already
// has one is refused with ErrGoalReviewRecorded. A review with no goal to go
// through needs no pass, so it is recorded as finished, with nothing changed.
// Goals no longer read are dropped from the per-goal record.
func (s *GoalReviewStore) Begin(ctx context.Context, review GoalReview, goals []string) (GoalReviews, error) {
	return s.update(ctx, review.StartedAt, func(record *GoalReviews) error {
		if review.Trigger == GoalReviewDaily {
			if _, found := record.Daily(review.Day); found {
				return fmt.Errorf("%w: %s", ErrGoalReviewRecorded, review.Day)
			}
		}
		review.StartedAt = review.StartedAt.UTC()
		review.Outcome = GoalReviewStarted
		if len(review.GoneThrough) == 0 {
			review.Outcome = GoalReviewNothingChanged
			review.FinishedAt = review.StartedAt
		}
		record.Reviews = append(record.Reviews, review)
		if extra := len(record.Reviews) - MaxGoalReviewsKept; extra > 0 {
			record.Reviews = append([]GoalReview(nil), record.Reviews[extra:]...)
		}
		current := make(map[string]bool, len(goals))
		for _, goal := range goals {
			current[goal] = true
		}
		for goal := range record.Goals {
			if !current[goal] {
				delete(record.Goals, goal)
			}
		}
		return nil
	})
}

// Finish records what became of the review that started at startedAt. A
// review whose pass completed records each goal it went through as gone
// through at its start, with the fingerprint it read; one that did not records
// why, and leaves the goals as they were.
func (s *GoalReviewStore) Finish(ctx context.Context, startedAt time.Time, completed bool, problem string, at time.Time) (GoalReviews, error) {
	return s.update(ctx, at, func(record *GoalReviews) error {
		for index := len(record.Reviews) - 1; index >= 0; index-- {
			review := &record.Reviews[index]
			if !review.StartedAt.Equal(startedAt.UTC()) {
				continue
			}
			review.FinishedAt = at.UTC()
			if !completed {
				review.Outcome = GoalReviewNotCompleted
				review.Problem = boundedSweepText(problem)
				return nil
			}
			review.Outcome = GoalReviewCompleted
			if record.Goals == nil {
				record.Goals = map[string]GoalReviewed{}
			}
			for _, entry := range review.GoneThrough {
				record.Goals[entry.Goal] = GoalReviewed{Fingerprint: entry.Fingerprint, ReviewedAt: review.StartedAt}
			}
			return nil
		}
		return fmt.Errorf("no review that started at %s is recorded", startedAt.UTC().Format(time.RFC3339))
	})
}

func (s *GoalReviewStore) update(ctx context.Context, at time.Time, change func(*GoalReviews) error) (GoalReviews, error) {
	release, err := s.lock(ctx)
	if err != nil {
		return GoalReviews{}, err
	}
	defer release()

	record, _, err := s.Load()
	if err != nil {
		return GoalReviews{}, err
	}
	if err := change(&record); err != nil {
		return GoalReviews{}, err
	}
	if len(record.Goals) == 0 {
		record.Goals = nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	record.SchemaVersion = GoalReviewsSchemaVersion
	record.ProductID = s.productID
	record.UpdatedAt = at.UTC()
	if err := record.Validate(); err != nil {
		return GoalReviews{}, err
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return GoalReviews{}, fmt.Errorf("encode the goal review record: %w", err)
	}
	confined, err := repowrite.NewRoot(s.root)
	if err != nil {
		return GoalReviews{}, fmt.Errorf("resolve the product's state directory: %w", err)
	}
	if _, err := confined.WriteFile(filepath.Join(goalReviewsDirectory, goalReviewsFile), append(encoded, '\n')); err != nil {
		return GoalReviews{}, fmt.Errorf("record the goal review: %w", err)
	}
	return record, nil
}

// lock serializes the read-modify-write of the record across every Yoyodyne
// process.
func (s *GoalReviewStore) lock(ctx context.Context) (func(), error) {
	directory := filepath.Join(s.root, goalReviewsDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create the goal review record's directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(directory, goalReviewsLockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the goal review record's lock: %w", err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the goal review record: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}
