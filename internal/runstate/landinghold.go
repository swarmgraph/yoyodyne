package runstate

// The hold an outside tool places on landing on one target branch.
//
// A project's own script may need the harness to land nothing on a branch for a
// while: a release being cut from it, a migration that must not see new commits.
// It places this hold through `yoyo landing hold`, and the harness lands nothing
// on that branch while the hold stands; `yoyo landing release` ends it sooner.
//
// Every hold carries its own expiry, Until, and is never longer than
// MaxLandingHold. A script that dies without releasing leaves a record nothing
// lifts, and the expiry is what keeps that record from stopping landing for
// good: a hold past its Until is read as no hold at all.
//
// One file is kept per branch, so holds on different branches never touch each
// other. A hold that cannot be read is an error, never "no hold", because
// landing through a hold nobody can read would defeat what the script asked.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// LandingHoldSchemaVersion is 1 and has never changed.
const LandingHoldSchemaVersion = 1

// MaxLandingHold is the longest a landing hold may last.
const MaxLandingHold = 4 * time.Hour

const (
	landingHoldFilePrefix = "landing-hold-"
	landingHoldFileSuffix = ".json"
	// maxLandingHoldReasonBytes bounds the reason a status line carries.
	maxLandingHoldReasonBytes = 1024
)

// LandingHold is the hold standing on landing on one target branch.
type LandingHold struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	TargetBranch  string           `json:"target_branch"`
	// Reason is why landing stopped, in the placing tool's words.
	Reason string `json:"reason"`
	// SetAt is when the hold was placed, and Until when it ends on its own if
	// nothing releases it first.
	SetAt time.Time `json:"set_at"`
	Until time.Time `json:"until"`
}

// Standing reports a hold that has not yet reached its expiry at now.
func (h LandingHold) Standing(now time.Time) bool { return now.Before(h.Until) }

// Says is the hold in a sentence, for the status line and for a refusal to land.
func (h LandingHold) Says() string {
	const layout = "2006-01-02 15:04 MST"
	return fmt.Sprintf("landing on %s is held: %s; set at %s, and it ends on its own at %s unless `yoyo landing release %s` ends it sooner",
		h.TargetBranch, h.Reason, h.SetAt.Local().Format(layout), h.Until.Local().Format(layout), h.TargetBranch)
}

// ValidateLandingHoldDuration refuses, in plain words, a missing duration, a
// negative one, and one past MaxLandingHold.
func ValidateLandingHoldDuration(d time.Duration) error {
	switch {
	case d == 0:
		return errors.New("a landing hold needs a duration: say how long it lasts with --for, up to 4 hours, such as --for 30m")
	case d < 0:
		return fmt.Errorf("a landing hold's duration must be more than zero, and %s is not", d)
	case d > MaxLandingHold:
		return fmt.Errorf("a landing hold may last at most 4 hours, so a script that stops before releasing it cannot stop landing for good; --for %s asks for longer", d)
	}
	return nil
}

// landingHoldFile is the file one branch's hold is kept in, with the branch
// encoded as branchLockName encodes it, so `release/1.2` and `release-1.2` do
// not share a hold. A name that was shortened to a digest cannot be read back
// to the branch, which is why the record carries TargetBranch itself.
func landingHoldFile(branch string) string {
	name := strings.TrimSuffix(branchLockName(landingHoldFilePrefix, branch), ".lock")
	return name + landingHoldFileSuffix
}

// LandingHoldStore is where the holds are kept: one file per branch under the
// product, written by `yoyo landing` and read by every pass that lands.
type LandingHoldStore struct {
	root, anchor string
	productID    domain.ProductID
}

func NewLandingHoldStore(root string, productID domain.ProductID) (*LandingHoldStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	root, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, err
	}
	return &LandingHoldStore{root: home.ProductDirectory(root, string(productID)), anchor: anchor, productID: productID}, nil
}

// Path names the file a branch's hold is kept in, so a failure can say where.
func (s *LandingHoldStore) Path(target string) string {
	return filepath.Join(s.root, landingHoldFile(target))
}

// Place sets the hold on target for duration from now, replacing any earlier
// hold on that branch.
func (s *LandingHoldStore) Place(target, reason string, duration time.Duration, now time.Time) (LandingHold, error) {
	target = strings.TrimSpace(target)
	if !validLocalBranch(target) {
		return LandingHold{}, fmt.Errorf("%q is not a local branch name a landing hold can be placed on", target)
	}
	reason = oneline.Fold(strings.TrimSpace(reason), maxLandingHoldReasonBytes)
	if reason == "" {
		return LandingHold{}, errors.New("a landing hold needs a reason, so whoever reads the status line knows why landing stopped")
	}
	if err := ValidateLandingHoldDuration(duration); err != nil {
		return LandingHold{}, err
	}
	hold := LandingHold{
		SchemaVersion: LandingHoldSchemaVersion,
		ProductID:     s.productID,
		TargetBranch:  target,
		Reason:        reason,
		SetAt:         now.UTC(),
		Until:         now.Add(duration).UTC(),
	}
	encoded, err := json.Marshal(hold)
	if err != nil {
		return LandingHold{}, err
	}
	root, err := pinStateRoot(s.root, s.anchor)
	if err != nil {
		return LandingHold{}, err
	}
	defer root.Close()
	if err := root.WriteFile(landingHoldFile(target), append(encoded, '\n'), 0o600, false); err != nil {
		return LandingHold{}, fmt.Errorf("place the landing hold on %s: %w", target, err)
	}
	return hold, nil
}

// Release removes the hold on target and reports what was there, even if it had
// already expired, and whether a record was found. Releasing none is not an
// error.
func (s *LandingHoldStore) Release(target string) (LandingHold, bool, error) {
	target = strings.TrimSpace(target)
	if !validLocalBranch(target) {
		return LandingHold{}, false, fmt.Errorf("%q is not a local branch name", target)
	}
	hold, found, err := s.read(target)
	if err != nil {
		// An unreadable record is still removed: releasing is how a person
		// clears one that cannot be read.
		if _, statErr := os.Stat(s.Path(target)); statErr != nil {
			return LandingHold{}, false, err
		}
		hold, found = LandingHold{TargetBranch: target}, true
	}
	if !found {
		return LandingHold{}, false, nil
	}
	root, err := pinStateRoot(s.root, s.anchor)
	if err != nil {
		return LandingHold{}, false, err
	}
	defer root.Close()
	if err := root.Remove(landingHoldFile(target)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return LandingHold{}, false, fmt.Errorf("release the landing hold on %s: %w", target, err)
	}
	return hold, true, nil
}

// Standing reports the hold on target where one is set and has not reached its
// end at now. No file is the ordinary answer, and an expired hold is reported
// with false. A record that cannot be read is an error, because landing must
// not go through a hold nobody can read.
func (s *LandingHoldStore) Standing(target string, now time.Time) (LandingHold, bool, error) {
	if s == nil {
		return LandingHold{}, false, nil
	}
	hold, found, err := s.read(strings.TrimSpace(target))
	if err != nil || !found {
		return LandingHold{}, false, err
	}
	return hold, hold.Standing(now), nil
}

// List is every hold standing at now, sorted by target branch. Expired records
// are skipped and left in place. An unreadable record is an error.
func (s *LandingHoldStore) List(now time.Time) ([]LandingHold, error) {
	if s == nil {
		return nil, nil
	}
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list the landing holds: %w", err)
	}
	var holds []LandingHold
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, landingHoldFilePrefix) || !strings.HasSuffix(name, landingHoldFileSuffix) {
			continue
		}
		hold, err := s.readFile(filepath.Join(s.root, name), name)
		if err != nil {
			return nil, err
		}
		if hold.Standing(now) {
			holds = append(holds, hold)
		}
	}
	sort.Slice(holds, func(i, j int) bool { return holds[i].TargetBranch < holds[j].TargetBranch })
	return holds, nil
}

// read loads the record for target; found is false when there is none.
func (s *LandingHoldStore) read(target string) (LandingHold, bool, error) {
	name := landingHoldFile(target)
	hold, err := s.readFile(filepath.Join(s.root, name), name)
	if errors.Is(err, fs.ErrNotExist) {
		return LandingHold{}, false, nil
	}
	if err != nil {
		return LandingHold{}, false, err
	}
	if hold.TargetBranch != target {
		return LandingHold{}, false, fmt.Errorf("the landing hold %s is for branch %q, not %q", filepath.Join(s.root, name), hold.TargetBranch, target)
	}
	return hold, true, nil
}

// readFile decodes one record and checks it is this product's, at a version
// this build reads, and kept under the name its branch gives it.
func (s *LandingHoldStore) readFile(path, name string) (LandingHold, error) {
	encoded, err := readCleanupFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return LandingHold{}, err
		}
		return LandingHold{}, fmt.Errorf("read the landing hold %s: %w", path, err)
	}
	var hold LandingHold
	if _, err := decodeTolerating(encoded, &hold); err != nil {
		return LandingHold{}, fmt.Errorf("decode the landing hold %s: %w", path, err)
	}
	if hold.SchemaVersion != LandingHoldSchemaVersion {
		return LandingHold{}, fmt.Errorf("the landing hold %s has schema version %d, which is not supported", path, hold.SchemaVersion)
	}
	if hold.ProductID != s.productID {
		return LandingHold{}, fmt.Errorf("the landing hold %s belongs to product %q, not %q", path, hold.ProductID, s.productID)
	}
	if !validLocalBranch(hold.TargetBranch) || landingHoldFile(hold.TargetBranch) != name {
		return LandingHold{}, fmt.Errorf("the landing hold %s names branch %q, which does not belong in that file", path, hold.TargetBranch)
	}
	return hold, nil
}
