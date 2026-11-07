package runstate

// What this machine keeps about merging the tracker's shared export, in team
// mode (docs/designs/working-alongside-other-contributors.md, section 5;
// internal/orchestrator/trackershare.go does the merging).
//
// Two things are kept, under the product's directory: what the machine last took
// from the shared export, which the next merge compares against, and one hold for
// each item whose two versions could not be merged. A hold lives here rather than
// on the item because the point of it is that neither version is edited: parking
// the item would write to one of the two versions being held apart.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// TrackerMergeHoldSchemaVersion is 1 and has never changed.
const TrackerMergeHoldSchemaVersion = 1

const (
	trackerShareDirectory = "tracker-share"
	trackerShareHolds     = "holds"
	trackerShareBase      = "base.jsonl"
	trackerShareDigest    = "shared-digest"
	trackerShareLock      = ".lock"
)

// TrackerVersion names one of the two versions of a held item.
type TrackerVersion string

const (
	// TrackerVersionThisMachine is the version in this machine's tracker.
	TrackerVersionThisMachine TrackerVersion = "here"
	// TrackerVersionShared is the version in the export committed on the shared
	// target branch.
	TrackerVersionShared TrackerVersion = "shared"
)

// TrackerVersionVocabulary is the versions a decision can keep.
func TrackerVersionVocabulary() []string {
	return []string{string(TrackerVersionThisMachine), string(TrackerVersionShared)}
}

// Valid reports one of the two versions.
func (v TrackerVersion) Valid() bool {
	return v == TrackerVersionThisMachine || v == TrackerVersionShared
}

// TrackerMergeDifference is one field the two versions of a held item disagree
// on, in the words each has it.
type TrackerMergeDifference struct {
	Field       string `json:"field"`
	ThisMachine string `json:"this_machine"`
	Shared      string `json:"shared"`
}

// TrackerMergeDecision is what the development manager decided about a hold. The
// next merge carries it out and ends the hold.
type TrackerMergeDecision struct {
	Keep   TrackerVersion `json:"keep"`
	Reason string         `json:"reason"`
	By     string         `json:"by"`
	At     time.Time      `json:"at"`
}

// TrackerMergeHold is one item held because its two versions cannot be merged.
type TrackerMergeHold struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	WorkItemID    string           `json:"work_item_id"`
	Title         string           `json:"title,omitempty"`
	HeldAt        time.Time        `json:"held_at"`
	// TargetBranch is the branch the shared version was read from.
	TargetBranch string `json:"target_branch"`
	// ThisMachine and Shared are the item's whole line in each export, and empty
	// where that side removed the item.
	ThisMachine          string                   `json:"this_machine,omitempty"`
	Shared               string                   `json:"shared,omitempty"`
	ThisMachineChangedAt string                   `json:"this_machine_changed_at,omitempty"`
	SharedChangedAt      string                   `json:"shared_changed_at,omitempty"`
	Differences          []TrackerMergeDifference `json:"differences"`
	// Reported says the development manager has been told, so a later check does
	// not tell her again about the same two versions.
	Reported bool                  `json:"reported,omitempty"`
	Decision *TrackerMergeDecision `json:"decision,omitempty"`
}

// SameVersions reports a hold on the same two versions as other.
func (h TrackerMergeHold) SameVersions(other TrackerMergeHold) bool {
	return h.ThisMachine == other.ThisMachine && h.Shared == other.Shared
}

// Reason is the hold in words, naming both versions, as the queue shows it and
// as the development manager is told.
func (h TrackerMergeHold) Reason() string {
	if h.Decision != nil {
		return fmt.Sprintf("two machines changed this item in ways that cannot be merged, and %s decided to keep the %s; the next forge check carries that out",
			h.Decision.By, h.Decision.Keep.Says())
	}
	return "two machines changed this item in ways that cannot be merged, so nothing starts it and neither version has been changed. " +
		h.DescribeVersions() + " The development manager decides which stands"
}

// DescribeVersions names the two versions and what each says.
func (h TrackerMergeHold) DescribeVersions() string {
	var thisMachine, shared []string
	for _, difference := range h.Differences {
		thisMachine = append(thisMachine, fmt.Sprintf("%s is %q", difference.Field, oneline.Bound(difference.ThisMachine, 160)))
		shared = append(shared, fmt.Sprintf("%s is %q", difference.Field, oneline.Bound(difference.Shared, 160)))
	}
	return fmt.Sprintf("On this machine%s, %s. In the export shared on %s%s, %s.",
		changedAtWords(h.ThisMachineChangedAt), strings.Join(thisMachine, "; "),
		h.TargetBranch, changedAtWords(h.SharedChangedAt), strings.Join(shared, "; "))
}

// Says is a version in ordinary words.
func (v TrackerVersion) Says() string {
	if v == TrackerVersionShared {
		return "version in the shared export"
	}
	return "version on this machine"
}

func changedAtWords(moment string) string {
	at, err := time.Parse(time.RFC3339, moment)
	if err != nil {
		return ""
	}
	return ", changed " + at.Local().Format("January 2 15:04 MST")
}

func (h TrackerMergeHold) validate() error {
	var problems []error
	if h.SchemaVersion != TrackerMergeHoldSchemaVersion {
		problems = append(problems, fmt.Errorf("tracker merge hold schema version %d is not supported", h.SchemaVersion))
	}
	if err := trackerMergeHoldName(h.WorkItemID); err != nil {
		problems = append(problems, err)
	}
	if h.HeldAt.IsZero() {
		problems = append(problems, errors.New("held_at is required"))
	}
	if len(h.Differences) == 0 {
		problems = append(problems, errors.New("a hold names what the two versions disagree on"))
	}
	return errors.Join(problems...)
}

func trackerMergeHoldName(workItemID string) error {
	if strings.TrimSpace(workItemID) == "" || strings.ContainsAny(workItemID, `/\`) || strings.HasPrefix(workItemID, ".") {
		return fmt.Errorf("work item %q cannot name a tracker merge hold", workItemID)
	}
	return nil
}

// ErrNoTrackerMergeHold is a decision about an item nothing holds.
var ErrNoTrackerMergeHold = errors.New("no merge hold stands on this item")

// ErrCannotKeepRemoval is a decision to keep the shared version of an item the
// shared export has removed. `bd import` cannot remove an item from the tracker,
// so a decision the next check could not carry out is refused rather than
// reported as made, and the hold stands.
var ErrCannotKeepRemoval = errors.New("the shared export no longer has this item, and the harness cannot remove an item from the tracker, so the shared version cannot be kept; keep this machine's version with the decision here, and if the item should not go on, ask the Lead Product Manager to retire it")

// TrackerMergeStore keeps one product's record of merging the shared export.
type TrackerMergeStore struct {
	stateRoot string
	anchor    string
	productID domain.ProductID
}

// NewTrackerMergeStore opens the record under one state root.
func NewTrackerMergeStore(root string, productID domain.ProductID) (*TrackerMergeStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the tracker merge record's state root: %w", err)
	}
	return &TrackerMergeStore{stateRoot: stateRoot, anchor: anchor, productID: productID}, nil
}

func (s *TrackerMergeStore) directory() string {
	return filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), trackerShareDirectory)
}

func (s *TrackerMergeStore) absolute(elements ...string) string {
	return filepath.Join(append([]string{s.stateRoot, s.directory()}, elements...)...)
}

// Lock holds the record against every other writer on this machine until the
// returned function is called. It is held only around writes to the holds, never
// around a tracker call or a fetch.
func (s *TrackerMergeStore) Lock(ctx context.Context) (func(), error) {
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, fmt.Errorf("pin the tracker merge record's state root: %w", err)
	}
	defer root.Close()
	if err := root.MakeDirectory(s.directory(), 0o700); err != nil {
		return nil, err
	}
	lock, err := root.OpenLock(filepath.Join(s.directory(), trackerShareLock), 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockStateFile(ctx, lock); err != nil {
		closeStateFile(lock)
		return nil, fmt.Errorf("wait for the tracker merge record: %w", err)
	}
	return func() { unlockStateFile(lock); closeStateFile(lock) }, nil
}

func (s *TrackerMergeStore) write(relative string, content []byte) error {
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return fmt.Errorf("pin the tracker merge record's state root: %w", err)
	}
	defer root.Close()
	if err := root.Unchanged(); err != nil {
		return err
	}
	return root.WriteFile(filepath.Join(s.directory(), relative), content, 0o600, false)
}

// Base is what this machine last took from the shared export, and false where it
// has never read it.
func (s *TrackerMergeStore) Base() ([]byte, bool, error) {
	encoded, err := os.ReadFile(s.absolute(trackerShareBase))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}

// SaveBase replaces what the next merge compares against, and remembers which
// shared export it came from.
func (s *TrackerMergeStore) SaveBase(base, shared []byte) error {
	if err := s.write(trackerShareBase, base); err != nil {
		return fmt.Errorf("record what was read from the shared export: %w", err)
	}
	if err := s.write(trackerShareDigest, []byte(trackerExportDigest(shared))); err != nil {
		return fmt.Errorf("record which shared export was read: %w", err)
	}
	return nil
}

// AlreadyRead reports a shared export the last merge read, where there is
// nothing new in it to take.
func (s *TrackerMergeStore) AlreadyRead(shared []byte) bool {
	recorded, err := os.ReadFile(s.absolute(trackerShareDigest))
	return err == nil && string(recorded) == trackerExportDigest(shared)
}

func trackerExportDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// SaveHold replaces one item's hold. The caller holds Lock.
func (s *TrackerMergeStore) SaveHold(hold TrackerMergeHold) error {
	hold.SchemaVersion = TrackerMergeHoldSchemaVersion
	hold.ProductID = s.productID
	if err := hold.validate(); err != nil {
		return err
	}
	encoded, err := encodeRecord("tracker merge hold", hold)
	if err != nil {
		return err
	}
	if err := s.write(filepath.Join(trackerShareHolds, hold.WorkItemID+".json"), encoded); err != nil {
		return fmt.Errorf("record the merge hold on %s: %w", hold.WorkItemID, err)
	}
	return nil
}

// EndHold removes one item's hold. The caller holds Lock.
func (s *TrackerMergeStore) EndHold(workItemID string) error {
	if err := trackerMergeHoldName(workItemID); err != nil {
		return err
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return fmt.Errorf("pin the tracker merge record's state root: %w", err)
	}
	defer root.Close()
	if err := root.Remove(filepath.Join(s.directory(), trackerShareHolds, workItemID+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("end the merge hold on %s: %w", workItemID, err)
	}
	return nil
}

// Hold reads one item's hold, and false where nothing holds it.
func (s *TrackerMergeStore) Hold(workItemID string) (TrackerMergeHold, bool, error) {
	if err := trackerMergeHoldName(workItemID); err != nil {
		return TrackerMergeHold{}, false, err
	}
	return readTrackerMergeHold(s.absolute(trackerShareHolds, workItemID+".json"))
}

// Holds reads every hold standing, by item.
func (s *TrackerMergeStore) Holds() ([]TrackerMergeHold, error) {
	return readTrackerMergeHolds(s.absolute(trackerShareHolds))
}

// DecideHold records which version the development manager decided to keep. It
// edits neither: the next merge carries it out.
func (s *TrackerMergeStore) DecideHold(ctx context.Context, workItemID string, keep TrackerVersion, reason, by string, at time.Time) (TrackerMergeHold, error) {
	if !keep.Valid() {
		return TrackerMergeHold{}, fmt.Errorf("%q is not a version; the versions are %s", keep, strings.Join(TrackerVersionVocabulary(), ", "))
	}
	release, err := s.Lock(ctx)
	if err != nil {
		return TrackerMergeHold{}, err
	}
	defer release()
	hold, found, err := s.Hold(workItemID)
	if err != nil {
		return TrackerMergeHold{}, err
	}
	if !found {
		return TrackerMergeHold{}, fmt.Errorf("%w: %s", ErrNoTrackerMergeHold, workItemID)
	}
	if keep == TrackerVersionShared && hold.Shared == "" {
		return TrackerMergeHold{}, fmt.Errorf("%w: %s", ErrCannotKeepRemoval, workItemID)
	}
	hold.Decision = &TrackerMergeDecision{Keep: keep, Reason: strings.TrimSpace(reason), By: by, At: at.UTC()}
	if err := s.SaveHold(hold); err != nil {
		return TrackerMergeHold{}, err
	}
	return hold, nil
}

// EndAllHolds removes every hold and names the items, for a project that has
// turned team mode off: no check will ever read those two versions again, so a
// hold left standing would keep its item from starting with nothing to end it.
// What was last read stays, so turning team mode on again finds the same items
// differ and holds them again.
func (s *TrackerMergeStore) EndAllHolds(ctx context.Context) ([]string, error) {
	holds, err := s.Holds()
	if err != nil {
		return nil, err
	}
	if len(holds) == 0 {
		return nil, nil
	}
	release, err := s.Lock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	var ended []string
	for _, hold := range holds {
		if err := s.EndHold(hold.WorkItemID); err != nil {
			return ended, err
		}
		ended = append(ended, hold.WorkItemID)
	}
	return ended, nil
}

// TrackerMergeHolds is every merge hold standing for this store's product, which
// is what keeps a held item out of what the harness starts (readmodel). It is
// read here so that everything already holding a run store can read it.
func (s *Store) TrackerMergeHolds() ([]TrackerMergeHold, error) {
	return readTrackerMergeHolds(filepath.Join(filepath.Dir(s.root), trackerShareDirectory, trackerShareHolds))
}

func readTrackerMergeHolds(directory string) ([]TrackerMergeHold, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var holds []TrackerMergeHold
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		hold, found, err := readTrackerMergeHold(filepath.Join(directory, name))
		if err != nil {
			return nil, err
		}
		if found {
			holds = append(holds, hold)
		}
	}
	slices.SortFunc(holds, func(a, b TrackerMergeHold) int { return strings.Compare(a.WorkItemID, b.WorkItemID) })
	return holds, nil
}

func readTrackerMergeHold(path string) (TrackerMergeHold, bool, error) {
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return TrackerMergeHold{}, false, nil
	}
	if err != nil {
		return TrackerMergeHold{}, false, err
	}
	var hold TrackerMergeHold
	if err := decodeStrictly(encoded, &hold); err != nil {
		return TrackerMergeHold{}, false, fmt.Errorf("decode the merge hold at %s: %w", path, err)
	}
	return hold, true, nil
}
