package runstate

// A target branch the harness will not catch up to the remote, recorded against
// the product.
//
// Every catch-up and every promotion is fast-forward-or-nothing, so a local
// target branch and the remote's having both moved is the one repository state
// the harness will not decide. Until yoyodyne-ifd.428.26 the only record of it
// was each run's own blocker: the brake rightly does not count the refusal
// (yoyodyne-ifd.428.2), so a watching session went on pulling item after item,
// each spending a whole development and review before stopping on the same
// divergence, until a person ran the unwedging steps in docs/operations.md.
//
// So the refusal is written here as well, once per target branch, and the
// record is what a watching session reads to choose nothing while the wedge
// stands, what `yoyo status` names on its "Needs a human" line with the recovery
// steps, and what the channel says. It is placed by the run the refusal stopped
// and lifted by the convergence sweep of `yoyo reconcile` when it finds the two
// branches converged — nobody releases it, because releasing it would not
// settle the branches, and a line that resumed before they were settled would
// only spend the next run finding the same divergence again.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// DivergedTargetsSchemaVersion is 1 and has never changed.
const DivergedTargetsSchemaVersion = 1

// MaxDivergedTargetTextBytes bounds the catch-up's own account of why it held,
// which is read in a line by somebody deciding what to do.
const MaxDivergedTargetTextBytes = 4 << 10

// DivergedTargetRecovery is where the steps that settle a divergence are, named
// by every surface that says one stands. The steps themselves are a person's,
// so what the harness says is where they are rather than a copy of them.
const DivergedTargetRecovery = "follow \"Unwedging a target branch that diverged from the forge\" in docs/operations.md, and the next `yoyo reconcile` that finds the branches converged lifts it; nothing needs releasing"

// DivergedTargets is the product's record of every target branch standing
// diverged from the remote's.
type DivergedTargets struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Targets       []DivergedTarget `json:"targets"`
}

// DivergedTarget is one target branch the harness would not catch up to the
// remote: both positions, the catch-up's own words, since when, and the run it
// most recently stopped.
type DivergedTarget struct {
	TargetBranch string `json:"target_branch"`
	// Remote is the remote the catch-up was asked of, where the refusing run
	// knew it.
	Remote       string `json:"remote,omitempty"`
	LocalCommit  string `json:"local_commit,omitempty"`
	RemoteCommit string `json:"remote_commit,omitempty"`
	// Held is the catch-up's account of why it left the local branch where it
	// is, carried as evidence rather than interpreted.
	Held string `json:"held"`
	// Since is when the divergence was first recorded, and LastSeen when a run
	// most recently met it.
	Since    time.Time `json:"since"`
	LastSeen time.Time `json:"last_seen"`
	// Refusals is how many promotions have been refused on it. After the first,
	// a watching session starts nothing into it, so a count above one is a run
	// somebody asked for by name, or one already in flight when it was found.
	Refusals int `json:"refusals"`
	// RunID and WorkItemID are the run the latest refusal stopped, which is the
	// one whose blocker carries the same account and whose approved change the
	// recovery resumes.
	RunID      string `json:"run_id,omitempty"`
	WorkItemID string `json:"work_item_id,omitempty"`
}

func (d DivergedTarget) validate() error {
	var problems []error
	if strings.TrimSpace(d.TargetBranch) == "" {
		problems = append(problems, errors.New("target branch is required"))
	}
	if strings.TrimSpace(d.Held) == "" {
		problems = append(problems, errors.New("what held the catch-up is required"))
	}
	if len(d.Held) > MaxDivergedTargetTextBytes {
		problems = append(problems, fmt.Errorf("what held the catch-up is %d bytes, which exceeds the %d byte bound", len(d.Held), MaxDivergedTargetTextBytes))
	}
	if d.Since.IsZero() {
		problems = append(problems, errors.New("since is required"))
	}
	if d.LastSeen.IsZero() {
		problems = append(problems, errors.New("last seen is required"))
	}
	if !d.Since.IsZero() && !d.LastSeen.IsZero() && d.LastSeen.Before(d.Since) {
		problems = append(problems, errors.New("last seen is before since; a divergence is not met before it was recorded"))
	}
	if d.Refusals < 1 {
		problems = append(problems, fmt.Errorf("refusals is %d, and a recorded divergence refused at least one promotion", d.Refusals))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("diverged target %s: %w", d.TargetBranch, err)
	}
	return nil
}

func (d DivergedTargets) Validate() error {
	var problems []error
	if d.SchemaVersion != DivergedTargetsSchemaVersion {
		problems = append(problems, fmt.Errorf("diverged targets schema version %d is not supported", d.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(d.ProductID)); err != nil {
		problems = append(problems, err)
	}
	seen := make(map[string]bool, len(d.Targets))
	for _, target := range d.Targets {
		if err := target.validate(); err != nil {
			problems = append(problems, err)
		}
		if seen[target.TargetBranch] {
			problems = append(problems, fmt.Errorf("target branch %s is recorded twice", target.TargetBranch))
		}
		seen[target.TargetBranch] = true
	}
	return errors.Join(problems...)
}

// Says is the divergence as the one clause every surface states it in: which
// branch, the catch-up's own account naming both positions, and how many runs
// it has stopped since when. It carries no remedy, because the surfaces write
// DivergedTargetRecovery beside it.
func (d DivergedTarget) Says() string {
	return fmt.Sprintf("the target branch %s will not catch up to the remote's, so the harness chooses no work for it: %s; %s refused since %s",
		d.TargetBranch, d.Held, count(d.Refusals, "promotion"), d.Since.UTC().Format(time.RFC3339))
}

// Mark names the divergence durably, so a surface that says it once can tell
// it from a later one on the same branch.
func (d DivergedTarget) Mark() string {
	return "diverged:" + d.TargetBranch + ":" + d.Since.UTC().Format(time.RFC3339)
}

// DivergedTargetObservation is one promotion refused because its target branch
// would not catch up to the remote's.
type DivergedTargetObservation struct {
	TargetBranch string
	Remote       string
	LocalCommit  string
	RemoteCommit string
	Held         string
	RunID        string
	WorkItemID   string
	At           time.Time
}

// DivergedTargetStore is where the divergences are recorded: one file under the
// product, beside the switches, because the target branches a product's runs
// promote into are a fact about the product.
//
// It writes with the temporary-file-and-rename every store beside it uses, under
// a lock because two processes revise it — a run placing a divergence and a
// sweep lifting one — and a read-modify-write with nothing between them is a
// divergence one of them loses.
type DivergedTargetStore struct {
	root      string
	productID domain.ProductID
}

func NewDivergedTargetStore(root string, productID domain.ProductID) (*DivergedTargetStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &DivergedTargetStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *DivergedTargetStore) Root() string { return s.root }

// Notice records that a promotion was refused on a diverged target, and reports
// the divergence as it now stands. A second refusal on the same branch is the
// same divergence: it keeps when it began, counts the refusal, and takes the
// latest positions and the latest run, because the positions are what the
// recovery reads and the run is the one it resumes.
func (s *DivergedTargetStore) Notice(observed DivergedTargetObservation) (DivergedTarget, error) {
	at := observed.At
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()
	unlock, err := s.lock()
	if err != nil {
		return DivergedTarget{}, err
	}
	defer unlock()
	targets, err := s.Standing()
	if err != nil {
		return DivergedTarget{}, err
	}
	recorded := DivergedTarget{
		TargetBranch: strings.TrimSpace(observed.TargetBranch),
		Remote:       strings.TrimSpace(observed.Remote),
		LocalCommit:  strings.TrimSpace(observed.LocalCommit),
		RemoteCommit: strings.TrimSpace(observed.RemoteCommit),
		Held:         oneline.Fold(observed.Held, MaxDivergedTargetTextBytes-len(oneline.Marker)),
		Since:        at,
		LastSeen:     at,
		Refusals:     1,
		RunID:        observed.RunID,
		WorkItemID:   observed.WorkItemID,
	}
	kept := make([]DivergedTarget, 0, len(targets)+1)
	for _, standing := range targets {
		if standing.TargetBranch != recorded.TargetBranch {
			kept = append(kept, standing)
			continue
		}
		recorded.Since = standing.Since
		recorded.Refusals = standing.Refusals + 1
		// A clock that went backwards between two processes must not record a
		// divergence met before it was recorded.
		if at.Before(recorded.Since) {
			recorded.LastSeen = recorded.Since
		}
		if recorded.Remote == "" {
			recorded.Remote = standing.Remote
		}
	}
	kept = append(kept, recorded)
	if err := s.write(kept); err != nil {
		return DivergedTarget{}, err
	}
	return recorded, nil
}

// Standing reports every target branch recorded as diverged, ordered by branch.
// No record is the ordinary answer and means no divergence stands. A record
// that cannot be read is an error, because a divergence nobody can read must
// never be pulled through as though it were absent.
func (s *DivergedTargetStore) Standing() ([]DivergedTarget, error) {
	file, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open diverged targets: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var recorded DivergedTargets
	if err := decoder.Decode(&recorded); err != nil {
		return nil, fmt.Errorf("decode diverged targets: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode diverged targets: %w", err)
	}
	if err := recorded.Validate(); err != nil {
		return nil, err
	}
	if recorded.ProductID != s.productID {
		return nil, fmt.Errorf("diverged targets belong to product %q, not %q", recorded.ProductID, s.productID)
	}
	return recorded.Targets, nil
}

// Clear records that a target branch has converged with the remote's, and
// reports the divergence that was standing on it. Clearing a branch with none
// standing is not an error: the branches are converged, which is what the
// caller found.
func (s *DivergedTargetStore) Clear(targetBranch string) (DivergedTarget, bool, error) {
	unlock, err := s.lock()
	if err != nil {
		return DivergedTarget{}, false, err
	}
	defer unlock()
	targets, err := s.Standing()
	if err != nil {
		return DivergedTarget{}, false, err
	}
	var cleared DivergedTarget
	found := false
	kept := make([]DivergedTarget, 0, len(targets))
	for _, standing := range targets {
		if standing.TargetBranch == targetBranch {
			cleared, found = standing, true
			continue
		}
		kept = append(kept, standing)
	}
	if !found {
		return DivergedTarget{}, false, nil
	}
	if len(kept) == 0 {
		if err := os.Remove(s.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return DivergedTarget{}, false, fmt.Errorf("clear diverged targets: %w", err)
		}
		if err := syncDirectory(s.root); err != nil {
			return DivergedTarget{}, false, err
		}
		return cleared, true, nil
	}
	if err := s.write(kept); err != nil {
		return DivergedTarget{}, false, err
	}
	return cleared, true, nil
}

func (s *DivergedTargetStore) write(targets []DivergedTarget) error {
	sort.Slice(targets, func(left, right int) bool { return targets[left].TargetBranch < targets[right].TargetBranch })
	recorded := DivergedTargets{SchemaVersion: DivergedTargetsSchemaVersion, ProductID: s.productID, Targets: targets}
	if err := recorded.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create product state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".diverged-targets-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary diverged targets: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary diverged targets: %w", err)
	}
	if err := writeJSONFile(temporary, "diverged targets", recorded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary diverged targets: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path()); err != nil {
		return fmt.Errorf("replace diverged targets: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the writers of the record, bounded for the reason the intake
// hold's lock is: what it waits on is another process's one small write.
func (s *DivergedTargetStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create product state directory: %w", err)
	}
	file, err := os.OpenFile(s.path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open diverged targets lock: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), intakeLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the diverged targets: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

// path names the record's file. It is one fixed name under the product.
func (s *DivergedTargetStore) path() string {
	return filepath.Join(s.root, "diverged-targets.json")
}
