package runstate

// The durable record of a review of an accumulated change. A per-work-item
// review is recorded on the run whose change it judged; a branch review has no
// such run — the work it covers was integrated by runs that are already settled
// and cleaned up — so it is recorded here, as its own append-only log, with the
// same session and model evidence a run carries. What it must never do is write
// into a run: a verdict on the accumulated change cannot retroactively decide
// anything about a run that has already ended, and a record that could reach one
// would make it look as though it had.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// maxEncodedBranchReviewBytes bounds one encoded record, including the trailing
// newline. It is comfortably above the bound on a verdict itself, so a review
// the harness accepted is always a review it can write down. The writer and the
// reader share it, so a record that was written is always one that can be read
// back.
const maxEncodedBranchReviewBytes = 256 << 10

// BranchReviewSchemaVersion is the version of the durable branch review record.
const BranchReviewSchemaVersion = 1

// branchReviewIDPattern is the shape of a branch review's identity. It carries
// its own prefix rather than a run's, for the same reason a conversation does:
// the identifier is what tells a reader which kind of thing it is looking at,
// and a branch review that borrowed a run's prefix would read as a run in every
// listing that sorts them by name.
var branchReviewIDPattern = regexp.MustCompile(`^review-[a-f0-9]{32}$`)

// NewBranchReviewID mints the identity of one branch review.
func NewBranchReviewID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate branch review id: %w", err)
	}
	return "review-" + hex.EncodeToString(bytes), nil
}

// BranchReview is one independent review of a branch against a base commit: the
// change that was judged, the verdict, and the provider identity that produced
// it. A review that reached no verdict is recorded too, under Failure: the
// evidence that a review was attempted and did not answer is worth as much as a
// verdict to whoever is deciding whether the branch has been reviewed at all.
type BranchReview struct {
	SchemaVersion int              `json:"schema_version"`
	ReviewID      string           `json:"review_id"`
	ProductID     domain.ProductID `json:"product_id"`
	RepositoryID  string           `json:"repository_id,omitempty"`
	Branch        string           `json:"branch"`
	BaseRef       string           `json:"base_ref,omitempty"`
	BaseCommit    string           `json:"base_commit"`
	HeadCommit    string           `json:"head_commit"`
	// Commits is how many of the branch's commits were described to the
	// reviewer, and CommitsOmitted how many the bounds dropped. Truncated says
	// the reviewer was shown an incomplete change, whichever half was cut. It no
	// longer decides on its own whether an approval was available: a range whose
	// test data alone outgrew the bound is approvable, and which omissions refuse
	// one is the review's own rule rather than this flag's.
	Commits        int       `json:"commits"`
	CommitsOmitted int       `json:"commits_omitted,omitempty"`
	Truncated      bool      `json:"truncated,omitempty"`
	ReviewedAt     time.Time `json:"reviewed_at"`
	SessionID      string    `json:"session_id,omitempty"`
	Model          string    `json:"model,omitempty"`
	ResolvedModel  string    `json:"resolved_model,omitempty"`
	// Effort is the effort level the review asked the provider for, and empty
	// where the reviewer agent configured none.
	Effort string `json:"effort,omitempty"`
	// ResolvedEffort is provider-reported; EffortReported is false when not reported.
	ResolvedEffort string    `json:"resolved_effort,omitempty"`
	EffortReported bool      `json:"effort_reported"`
	Decision       string    `json:"decision,omitempty"`
	Summary        string    `json:"summary,omitempty"`
	Findings       []Finding `json:"findings,omitempty"`
	Failure        string    `json:"failure,omitempty"`
	// Shadow marks a review made to measure the reviewer rather than to judge
	// the branch: the same reviewer under the same contract, run again over a
	// branch state another review already decided, so the two verdicts can be
	// held up against each other. It is recorded in this log rather than kept
	// somewhere else because it is a provider invocation that happened and cost
	// money, and it is marked because a verdict that gates nothing must never be
	// readable as one that gates something — Approved says so below.
	Shadow bool `json:"shadow,omitempty"`
}

// Validate rejects a record that could not describe a review that happened.
func (b BranchReview) Validate() error {
	var problems []error
	if b.SchemaVersion != BranchReviewSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", BranchReviewSchemaVersion))
	}
	if !branchReviewIDPattern.MatchString(b.ReviewID) {
		problems = append(problems, errors.New("review id is invalid"))
	}
	if err := domain.ValidateIdentifier("product id", string(b.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !validLocalBranch(b.Branch) {
		problems = append(problems, errors.New("branch must be a local branch name"))
	}
	if !commitPattern.MatchString(b.BaseCommit) {
		problems = append(problems, errors.New("base commit is invalid"))
	}
	if !commitPattern.MatchString(b.HeadCommit) {
		problems = append(problems, errors.New("head commit is invalid"))
	}
	if b.BaseCommit == b.HeadCommit {
		problems = append(problems, errors.New("base and head commits are the same, which is no accumulated change"))
	}
	if b.Commits < 1 {
		problems = append(problems, errors.New("a reviewed branch carries at least one commit"))
	}
	if b.CommitsOmitted < 0 {
		problems = append(problems, errors.New("omitted commit count cannot be negative"))
	}
	if b.ReviewedAt.IsZero() {
		problems = append(problems, errors.New("reviewed at is required"))
	}
	switch b.Decision {
	case ReviewApprove, ReviewRepair:
		if strings.TrimSpace(b.Summary) == "" {
			problems = append(problems, errors.New("a decided review requires a summary"))
		}
		if b.Decision == ReviewRepair && len(b.Findings) == 0 {
			problems = append(problems, errors.New("a repair verdict requires at least one finding"))
		}
	case "":
		// A review with no decision is one that did not answer, and it has to say
		// what stopped it: an empty record either way would be indistinguishable
		// from a branch nobody reviewed.
		if strings.TrimSpace(b.Failure) == "" {
			problems = append(problems, errors.New("a review with no decision must record what stopped it"))
		}
	default:
		problems = append(problems, fmt.Errorf("decision %q must be %q or %q", b.Decision, ReviewApprove, ReviewRepair))
	}
	for i, finding := range b.Findings {
		if err := finding.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("findings[%d]: %w", i, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid branch review: %w", errors.Join(problems...))
	}
	return nil
}

// Approved reports the one thing a reader of this record acts on: whether an
// independent reviewer approved the accumulated change. Everything else — a
// repair verdict, a review that failed, a review of a change too large to show
// in full, a shadow review that was never asked to gate anything — is not an
// approval, and each of them says so through the same answer, so nothing
// downstream has to enumerate the ways a review can fall short.
func (b BranchReview) Approved() bool {
	return !b.Shadow && b.Decision == ReviewApprove
}

// BranchReviewStore is where branch reviews are collected: their own directory
// beside the runs and the conversations rather than among them, for the same
// reason each of those has one — a branch review outlives every run whose work
// it judged, and belongs to no one of them. It holds one append-only log of
// verdicts, never revised once written, and one event log per review beside it.
type BranchReviewStore struct {
	root      string
	productID domain.ProductID
}

func NewBranchReviewStore(root string, productID domain.ProductID) (*BranchReviewStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &BranchReviewStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID), "branch-reviews"),
		productID: productID,
	}, nil
}

func (s *BranchReviewStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the recorded reviews
// actually are.
func (s *BranchReviewStore) Path() string { return filepath.Join(s.root, "reviews.jsonl") }

// Append records one branch review durably.
func (s *BranchReviewStore) Append(reviewed BranchReview) error {
	if err := s.validate(reviewed); err != nil {
		return err
	}
	encoded, err := encodeBranchReview(reviewed)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedBranchReviewBytes {
		return fmt.Errorf("encoded branch review is %d bytes, limit is %d", len(encoded), maxEncodedBranchReviewBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create branch review directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect branch review log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open branch review log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append branch review: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append branch review: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync branch review log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close branch review log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every recorded branch review in the order it was recorded. A log
// that does not exist yet is a product whose branches have never been reviewed,
// which is not a failure to read.
func (s *BranchReviewStore) List() ([]BranchReview, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open branch review log: %w", err)
	}
	defer file.Close()

	var reviews []BranchReview
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedBranchReviewBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		decoded, err := decodeBranchReview([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("decode branch review log: %w", err)
		}
		if err := s.validate(decoded); err != nil {
			return nil, fmt.Errorf("decode branch review log: %w", err)
		}
		reviews = append(reviews, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read branch review log: %w", err)
	}
	return reviews, nil
}

// AppendEvent persists one normalized event from a branch review. A branch
// review is a provider invocation the harness makes, so it records the same
// event stream every other one does: without it the review could not be
// followed while it ran, and what the provider reported it cost would be
// unrecoverable afterwards, because the event log is the only place that figure
// is ever written down.
//
// The log is named for the review rather than for a run, and it lives in this
// store's own directory, so nothing here can write into a run's event stream.
func (s *BranchReviewStore) AppendEvent(event execution.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	path, err := s.eventPath(event.RunID)
	if err != nil {
		return err
	}
	encoded, err := encodeEvent(event)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedEventBytes {
		return fmt.Errorf("encoded event is %d bytes, limit is %d", len(encoded), maxEncodedEventBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create branch review directory: %w", err)
	}
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect branch review event log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open branch review event log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append branch review event: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append branch review event: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync branch review event log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close branch review event log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// LoadEvents reads one branch review's recorded event stream. A review that
// never wrote one has no events rather than an unreadable log.
func (s *BranchReviewStore) LoadEvents(reviewID string) ([]execution.Event, error) {
	path, err := s.eventPath(reviewID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open branch review event log: %w", err)
	}
	defer file.Close()

	var events []execution.Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedEventBytes)
	for scanner.Scan() {
		event, err := execution.DecodeEvent(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("decode branch review event log for %s: %w", reviewID, err)
		}
		if event.RunID != reviewID {
			return nil, fmt.Errorf("decode branch review event log for %s: event belongs to %s", reviewID, event.RunID)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read branch review event log: %w", err)
	}
	return events, nil
}

// ReviewPrice is what one branch review cost, as its own event log reports it.
// It is the same evidence a run is priced from — the provider's own figure on
// the event that ended the invocation — read out of the log this store keeps
// rather than a run's, because a branch review belongs to no run.
type ReviewPrice struct {
	ReviewID    string  `json:"review_id"`
	Invocations int     `json:"invocations,omitempty"`
	CostUSD     float64 `json:"cost_usd"`
	// Unknown says why this review could not be priced, and is empty on one that
	// was. A review carrying it did not cost nothing: nothing survives to say
	// what it cost, which is a different fact and is stated as itself.
	Unknown string `json:"unknown,omitempty"`
}

// Known reports a review the recorded evidence could actually price.
func (p ReviewPrice) Known() bool { return p.Unknown == "" }

// Price reports what one branch review cost. It never fails for want of a log:
// an event stream that is gone is what an unknown price is, and reporting that
// as an error would lose the reviews beside it that can be priced.
func (s *BranchReviewStore) Price(reviewID string) ReviewPrice {
	price := ReviewPrice{ReviewID: reviewID}
	path, err := s.eventPath(reviewID)
	if err != nil {
		price.Unknown = err.Error()
		return price
	}
	cost, invocations, err := scanEventCost(path)
	if err != nil {
		price.Unknown = err.Error()
		return price
	}
	// Every branch review is one provider invocation by construction, so a log
	// with none in it is a log that lost them rather than a review that spent
	// nothing.
	if invocations == 0 {
		price.Unknown = "the review's event log holds no invocation to price"
		return price
	}
	price.CostUSD = cost
	price.Invocations = invocations
	return price
}

func (s *BranchReviewStore) eventPath(reviewID string) (string, error) {
	if !branchReviewIDPattern.MatchString(reviewID) {
		return "", errors.New("branch review id is invalid")
	}
	return filepath.Join(s.root, reviewID+".events.jsonl"), nil
}

func decodeBranchReview(data []byte) (BranchReview, error) {
	var decoded BranchReview
	if err := json.Unmarshal(data, &decoded); err != nil {
		return BranchReview{}, err
	}
	return decoded, nil
}

func encodeBranchReview(reviewed BranchReview) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(reviewed); err != nil {
		return nil, fmt.Errorf("encode branch review: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *BranchReviewStore) validate(reviewed BranchReview) error {
	if reviewed.ProductID != s.productID {
		return fmt.Errorf("branch review product %q does not match store product %q", reviewed.ProductID, s.productID)
	}
	return reviewed.Validate()
}
