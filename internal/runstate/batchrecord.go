package runstate

// The durable record of the harness's own batch pull request for one target
// branch (docs/designs/integrating-to-a-local-branch.md, section 5, "Ownership
// and restarts"). With execution.batch_pull_request enabled and
// execution.upstream_branch set, the harness gathers the work landed on the
// target branch into one pull request into the team's branch. This file keeps
// what that route has done and seen, written after each step, so a restart
// repeats nothing that was finished and every surface says the same thing. It
// holds the pull request, its head, its items, the approval seen, the automated
// reviewer's state, the review requests sent, and the tries at the description.
// Nothing here opens a pull request, pushes a branch, or asks the forge
// anything; the steps that do write what they did here.
//
// One record exists per product and target branch, written through the shared
// confined write primitive, in a directory of its own under the product's state.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// BatchRecordSchemaVersion is the version of the record's shape.
const BatchRecordSchemaVersion = 1

// MaxBatchRecordItems bounds the items one batch carries, so a record cannot
// grow without limit.
const MaxBatchRecordItems = 1000

// MaxBatchRecordHistory bounds the requests sent and the description tries a
// record keeps, oldest dropped first.
const MaxBatchRecordHistory = 50

const (
	batchRecordsDirectory = "batch-pull-requests"
	batchRecordName       = "batch.json"
)

// BatchItemState is where an item waiting for the team's merge stands on the
// batch route.
type BatchItemState string

const (
	// BatchUnpublished is on the target branch and in no open batch.
	BatchUnpublished BatchItemState = "unpublished"
	// BatchInOpenBatch is in the open batch pull request, not yet approved.
	BatchInOpenBatch BatchItemState = "in the open batch"
	// BatchApproved is in a batch a person has approved on its head commit.
	BatchApproved BatchItemState = "approved"
	// BatchMerged is in a batch that merged.
	BatchMerged BatchItemState = "merged"
)

// Automated reviewer states.
const (
	// BatchReviewerWaiting means the account has not yet reviewed the head.
	BatchReviewerWaiting = "waiting"
	// BatchReviewerReviewed means the account has submitted a review on the head.
	BatchReviewerReviewed = "reviewed"
	// BatchReviewerPassedOver means the review tool had not finished two hours
	// after the head was pushed, and the batch went on without it.
	BatchReviewerPassedOver = "passed over"
)

// BatchRecord is one target branch's batch pull request.
type BatchRecord struct {
	SchemaVersion  int    `json:"schema_version"`
	TargetBranch   string `json:"target_branch"`
	UpstreamBranch string `json:"upstream_branch"`
	// BatchBranch is the branch the harness keeps the batch on, never the
	// target branch itself.
	BatchBranch string `json:"batch_branch,omitempty"`

	// PullRequest is the batch pull request, nil before one opens.
	PullRequest *BatchPullRequestRef `json:"pull_request,omitempty"`
	// Head is the commit the batch branch stands at, which every push is a
	// compare and swap against.
	Head string `json:"head,omitempty"`
	// HeadPushedAt is when the batch branch was last moved.
	HeadPushedAt *time.Time `json:"head_pushed_at,omitempty"`
	// Items are the work items the batch carries.
	Items []BatchRecordItem `json:"items,omitempty"`

	// Approval is the person's approval seen on the pull request, and the commit
	// it was given on. It stands only while Head is that commit.
	Approval *BatchApproval `json:"approval,omitempty"`
	// Reviewer is the automated reviewer's state on the current head.
	Reviewer *BatchReviewer `json:"reviewer,omitempty"`
	// Mergeable is what the forge last reported of the pull request's head.
	Mergeable *BatchMergeable `json:"mergeable,omitempty"`
	// Requests are the review requests sent, one per head.
	Requests []BatchRequestSent `json:"requests,omitempty"`
	// Description is the tries at the Lead Product Manager's description.
	Description []BatchDescriptionTry `json:"description,omitempty"`

	// Merged and Closed end the batch: the forge merged it, or it was closed
	// without merging.
	Merged *BatchEnded `json:"merged,omitempty"`
	Closed *BatchEnded `json:"closed,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`
}

// BatchPullRequestRef names the pull request.
type BatchPullRequestRef struct {
	Number   int       `json:"number"`
	URL      string    `json:"url,omitempty"`
	OpenedAt time.Time `json:"opened_at"`
}

// BatchRecordItem is one item in the batch, with the commit it landed as.
type BatchRecordItem struct {
	WorkItemID    string `json:"work_item_id"`
	Title         string `json:"title,omitempty"`
	LandingCommit string `json:"landing_commit"`
}

// BatchApproval is a person's approval of the pull request.
type BatchApproval struct {
	By     string    `json:"by,omitempty"`
	Commit string    `json:"commit"`
	At     time.Time `json:"at"`
}

// BatchReviewer is the automated reviewer's state for one head.
type BatchReviewer struct {
	Account string    `json:"account"`
	Head    string    `json:"head"`
	State   string    `json:"state"`
	At      time.Time `json:"at"`
}

// BatchMergeable is one forge reading of whether the pull request can merge.
type BatchMergeable struct {
	Head      string    `json:"head"`
	Mergeable bool      `json:"mergeable"`
	ReadAt    time.Time `json:"read_at"`
	// Missing says in plain words what the forge reported missing, where the
	// pull request is not mergeable.
	Missing string `json:"missing,omitempty"`
}

// BatchRequestSent is a review request for one head.
type BatchRequestSent struct {
	Head string `json:"head"`
	// Why is opened, grown, or brought up to date.
	Why      string     `json:"why"`
	SentAt   *time.Time `json:"sent_at,omitempty"`
	Failures int        `json:"failures,omitempty"`
	LastErr  string     `json:"last_error,omitempty"`
}

// BatchDescriptionTry is one try at the description for a head.
type BatchDescriptionTry struct {
	Head string    `json:"head"`
	At   time.Time `json:"at"`
	// Outcome is posted, or failed with the reason.
	Posted bool   `json:"posted"`
	Reason string `json:"reason,omitempty"`
}

// BatchEnded is when a batch merged or closed, and at which commit.
type BatchEnded struct {
	At     time.Time `json:"at"`
	Commit string    `json:"commit,omitempty"`
}

// Open reports a batch pull request that has opened and not ended.
func (b BatchRecord) Open() bool {
	return b.PullRequest != nil && b.Merged == nil && b.Closed == nil
}

// Approved reports a person's approval standing on the batch's current head.
func (b BatchRecord) Approved() bool {
	return b.Open() && b.Approval != nil && b.Head != "" && b.Approval.Commit == b.Head
}

// Carries reports whether the batch carries the item landed as the commit.
func (b BatchRecord) Carries(workItemID, landingCommit string) bool {
	for _, item := range b.Items {
		if item.WorkItemID == workItemID && item.LandingCommit == landingCommit {
			return true
		}
	}
	return false
}

// StateOf is where an item landed as the commit stands on this batch: unpublished
// where the batch does not carry it or has closed without merging, in the open
// batch, approved, or merged.
func (b BatchRecord) StateOf(workItemID, landingCommit string) BatchItemState {
	if !b.Carries(workItemID, landingCommit) {
		return BatchUnpublished
	}
	switch {
	case b.Merged != nil:
		return BatchMerged
	case b.Closed != nil:
		return BatchUnpublished
	case b.Approved():
		return BatchApproved
	case b.Open():
		return BatchInOpenBatch
	}
	return BatchUnpublished
}

// Validate checks the record names its branches and holds well-formed commits,
// and keeps no more than it is bounded to.
func (b BatchRecord) Validate() error {
	var problems []error
	if b.SchemaVersion != BatchRecordSchemaVersion {
		problems = append(problems, fmt.Errorf("batch record schema version %d is not supported", b.SchemaVersion))
	}
	if !validLocalBranch(b.TargetBranch) {
		problems = append(problems, fmt.Errorf("batch record target_branch %q is not a local branch name", b.TargetBranch))
	}
	if !validLocalBranch(b.UpstreamBranch) {
		problems = append(problems, fmt.Errorf("batch record upstream_branch %q is not a local branch name", b.UpstreamBranch))
	}
	if b.BatchBranch != "" && !validLocalBranch(b.BatchBranch) {
		problems = append(problems, fmt.Errorf("batch record batch_branch %q is not a local branch name", b.BatchBranch))
	}
	if b.Head != "" && !commitPattern.MatchString(b.Head) {
		problems = append(problems, errors.New("batch record head is invalid"))
	}
	if b.PullRequest != nil && b.PullRequest.Number <= 0 {
		problems = append(problems, errors.New("batch record pull_request number must be positive"))
	}
	if b.PullRequest != nil && b.Head == "" {
		problems = append(problems, errors.New("batch record pull_request requires a head"))
	}
	if len(b.Items) > MaxBatchRecordItems {
		problems = append(problems, fmt.Errorf("batch record carries %d items, which exceeds the %d kept", len(b.Items), MaxBatchRecordItems))
	}
	for _, item := range b.Items {
		if strings.TrimSpace(item.WorkItemID) == "" || !commitPattern.MatchString(item.LandingCommit) {
			problems = append(problems, fmt.Errorf("batch record item %q needs an identifier and a valid landing_commit", item.WorkItemID))
		}
	}
	if b.Approval != nil && !commitPattern.MatchString(b.Approval.Commit) {
		problems = append(problems, errors.New("batch record approval commit is invalid"))
	}
	if r := b.Reviewer; r != nil {
		switch r.State {
		case BatchReviewerWaiting, BatchReviewerReviewed, BatchReviewerPassedOver:
		default:
			problems = append(problems, fmt.Errorf("batch record reviewer state %q is not %s, %s or %s", r.State, BatchReviewerWaiting, BatchReviewerReviewed, BatchReviewerPassedOver))
		}
		if strings.TrimSpace(r.Account) == "" || !commitPattern.MatchString(r.Head) {
			problems = append(problems, errors.New("batch record reviewer needs an account and a valid head"))
		}
	}
	if b.Mergeable != nil && !commitPattern.MatchString(b.Mergeable.Head) {
		problems = append(problems, errors.New("batch record mergeable head is invalid"))
	}
	if len(b.Requests) > MaxBatchRecordHistory || len(b.Description) > MaxBatchRecordHistory {
		problems = append(problems, fmt.Errorf("batch record keeps more than %d requests or description tries", MaxBatchRecordHistory))
	}
	for _, request := range b.Requests {
		if !commitPattern.MatchString(request.Head) || request.Failures < 0 {
			problems = append(problems, errors.New("batch record request needs a valid head and no negative failures"))
		}
	}
	for _, try := range b.Description {
		if !commitPattern.MatchString(try.Head) {
			problems = append(problems, errors.New("batch record description try needs a valid head"))
		}
	}
	if b.Merged != nil && b.Closed != nil {
		problems = append(problems, errors.New("batch record cannot be both merged and closed"))
	}
	if (b.Merged != nil || b.Closed != nil) && b.PullRequest == nil {
		problems = append(problems, errors.New("batch record merged or closed requires a pull request"))
	}
	if b.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("batch record updated_at is required"))
	}
	return errors.Join(problems...)
}

// BatchRecordStore keeps the batch records of a product's target branches.
type BatchRecordStore struct {
	stateRoot, anchor string
	productID         domain.ProductID
}

// NewBatchRecordStore opens the store under the state root.
func NewBatchRecordStore(root string, productID domain.ProductID) (*BatchRecordStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the batch record state root: %w", err)
	}
	return &BatchRecordStore{stateRoot: stateRoot, anchor: anchor, productID: productID}, nil
}

func (s *BatchRecordStore) directory() string {
	return filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), batchRecordsDirectory)
}

// batchRecordDirectory digests the branch for the reason teamBranchDirectory
// does: a name like release/1.2 is not one directory name.
func batchRecordDirectory(targetBranch string) string {
	sum := sha256.Sum256([]byte("batch\x00" + targetBranch))
	return hex.EncodeToString(sum[:16])
}

func (s *BatchRecordStore) open(targetBranch string) (*repowrite.PinnedRoot, error) {
	if !validLocalBranch(targetBranch) {
		return nil, fmt.Errorf("target branch %q is not a local branch name", targetBranch)
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, fmt.Errorf("pin the batch record state root: %w", err)
	}
	defer root.Close()
	directory := path.Join(filepath.ToSlash(s.directory()), batchRecordDirectory(targetBranch))
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create the batch record directory for %s: %w", targetBranch, err)
	}
	pinned, err := root.OpenDirectory(directory)
	if err != nil {
		return nil, fmt.Errorf("open the batch record directory for %s: %w", targetBranch, err)
	}
	return pinned, nil
}

func decodeBatchRecord(encoded []byte) (BatchRecord, error) {
	var record BatchRecord
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return BatchRecord{}, err
	}
	if decoder.More() {
		return BatchRecord{}, errors.New("trailing data after the record")
	}
	if err := record.Validate(); err != nil {
		return BatchRecord{}, err
	}
	return record, nil
}

func readBatchRecord(pinned *repowrite.PinnedRoot) (BatchRecord, error) {
	encoded, err := pinned.ReadFile(batchRecordName)
	if err != nil {
		return BatchRecord{}, err
	}
	if len(encoded) > maxEncodedStateBytes {
		return BatchRecord{}, fmt.Errorf("the record is %d bytes, which exceeds the %d byte bound", len(encoded), maxEncodedStateBytes)
	}
	return decodeBatchRecord(encoded)
}

// Load reads a target branch's batch record, and reports false where none was
// written.
func (s *BatchRecordStore) Load(targetBranch string) (BatchRecord, bool, error) {
	pinned, err := s.open(targetBranch)
	if err != nil {
		return BatchRecord{}, false, err
	}
	defer pinned.Close()
	record, err := readBatchRecord(pinned)
	if errors.Is(err, fs.ErrNotExist) {
		return BatchRecord{}, false, nil
	}
	if err != nil {
		return BatchRecord{}, false, fmt.Errorf("read the batch record for %s: %w", targetBranch, err)
	}
	if record.TargetBranch != targetBranch {
		return BatchRecord{}, false, fmt.Errorf("the batch record kept for %s is for %q", targetBranch, record.TargetBranch)
	}
	return record, true, nil
}

// Save validates the record and replaces it whole and synced. A step of the
// batch route calls it after each thing it does, so what was finished is never
// repeated.
func (s *BatchRecordStore) Save(record BatchRecord) error {
	record.SchemaVersion = BatchRecordSchemaVersion
	note := truncatedNote(MaxRecordedTextBytes)
	requests := make([]BatchRequestSent, len(record.Requests))
	for i, request := range record.Requests {
		request.LastErr = boundRecordedText(request.LastErr, MaxRecordedTextBytes, note)
		requests[i] = request
	}
	record.Requests = requests
	tries := make([]BatchDescriptionTry, len(record.Description))
	for i, try := range record.Description {
		try.Reason = boundRecordedText(try.Reason, MaxRecordedTextBytes, note)
		tries[i] = try
	}
	record.Description = tries
	if record.Mergeable != nil {
		mergeable := *record.Mergeable
		mergeable.Missing = boundRecordedText(mergeable.Missing, MaxRecordedTextBytes, note)
		record.Mergeable = &mergeable
	}
	if err := record.Validate(); err != nil {
		return fmt.Errorf("batch record for %s: %w", record.TargetBranch, err)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the batch record for %s: %w", record.TargetBranch, err)
	}
	encoded = append(encoded, '\n')
	pinned, err := s.open(record.TargetBranch)
	if err != nil {
		return err
	}
	defer pinned.Close()
	if err := pinned.WriteFile(batchRecordName, encoded, 0o600, false); err != nil {
		return fmt.Errorf("write the batch record for %s: %w", record.TargetBranch, err)
	}
	if err := pinned.Sync(); err != nil {
		return fmt.Errorf("sync the batch record for %s: %w", record.TargetBranch, err)
	}
	return nil
}

// All reads every batch record the product has, by target branch.
func (s *BatchRecordStore) All() ([]BatchRecord, error) {
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, fmt.Errorf("pin the batch record state root: %w", err)
	}
	defer root.Close()
	directory := path.Join(filepath.ToSlash(s.directory()))
	entries, err := root.ReadDirectory(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list the batch records: %w", err)
	}
	var records []BatchRecord
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pinned, err := root.OpenDirectory(path.Join(directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("open the batch record directory %s: %w", entry.Name(), err)
		}
		record, err := readBatchRecord(pinned)
		pinned.Close()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read the batch record in %s: %w", entry.Name(), err)
		}
		if batchRecordDirectory(record.TargetBranch) != entry.Name() {
			return nil, fmt.Errorf("the batch record in %s is for %q", entry.Name(), record.TargetBranch)
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].TargetBranch < records[j].TargetBranch })
	return records, nil
}
