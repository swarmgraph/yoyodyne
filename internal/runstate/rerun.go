package runstate

// What triage has already done about one docketed stoppage, and what the
// stoppage left behind.
//
// The per-item counters beside this bound what an item may be given across every
// run of it. They are the right bound for that question and the wrong one for
// this: a docket entry is one stoppage, and an entry re-run twice is one stoppage
// acted on twice however much budget the item happens to have left. So the entry
// is the identity here — one record per docket key, claimed before the run it
// causes is started, and refused to whoever asks second.
//
// It carries what the stopped run preserved, because that is the other half of
// what a re-run leaves lying around. A branch and a worktree held for a fresh run
// to draw on are useful; the same two after the fresh run has integrated are
// orphans nobody knows to look for, and the difference between the two is a
// disposition somebody wrote down. So this says which of them each is, and says
// why when the harness kept something it would rather have retired.
//
// Like the counters, it is one home per machine, and for the same reason: what
// the coordination of two harnesses over one repository would take belongs to the
// team-mode epic rather than here.

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
)

// RerunSchemaVersion is 1 and has never changed.
const RerunSchemaVersion = 1

// What became of the branch and the worktree the stopped run preserved. They are
// dispositions rather than flags because "still there" is two different facts: a
// worktree held on purpose for a fresh run to draw on, and one nobody decided
// anything about, are the same directory and completely different to whoever
// finds it.
const (
	// PreservedKept is the artifact the harness deliberately left where it is:
	// the fresh run has not integrated, so what the stopped run holds may still
	// be what somebody cherry-picks from.
	PreservedKept = "kept"
	// PreservedRetired is the artifact the harness removed once the fresh run
	// integrated, which is the moment what the stopped run held stopped being
	// worth keeping.
	PreservedRetired = "retired"
	// PreservedGone is the artifact that was already removed before triage
	// reached it, so there was never anything here to keep or retire.
	PreservedGone = "gone"
)

// PreservedArtifacts is what the stopped run left behind and what the harness
// has done about it. Problem is why a retirement did not happen: an artifact
// that could not be retired is kept and says so, because the one thing this must
// never do is describe as gone something a person will find on their disk.
type PreservedArtifacts struct {
	Branch       string     `json:"branch,omitempty"`
	WorktreePath string     `json:"worktree_path,omitempty"`
	Disposition  string     `json:"disposition"`
	RetiredAt    *time.Time `json:"retired_at,omitempty"`
	Problem      string     `json:"problem,omitempty"`
}

// ValidDisposition reports one of the three dispositions above.
func ValidDisposition(disposition string) bool {
	switch disposition {
	case PreservedKept, PreservedRetired, PreservedGone:
		return true
	default:
		return false
	}
}

// Rerun is the durable record of one docketed stoppage being run again.
// It is written before the fresh run is started, so a process that dies between
// the two has recorded a re-run nobody took rather than taken one nobody
// recorded — the same direction every triage counter fails in.
type Rerun struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// DocketKey names the triage entry this settles, which is what makes the
	// record one per stoppage rather than one per item or one per run.
	DocketKey  string `json:"docket_key"`
	PriorRunID string `json:"prior_run_id"`
	WorkItemID string `json:"work_item_id"`
	// Reason is what the fresh run records as why it exists: the development
	// manager's triage decision and the reasoning it gave. It is kept here as
	// well as on the run because the run is one of many and this is the record of
	// the decision that caused it.
	Reason    string    `json:"reason"`
	ClaimedAt time.Time `json:"claimed_at"`
	// RunID is the fresh run, recorded once there is one. It is absent on a claim
	// whose run never got as far as being reserved, which is exactly the case a
	// reader needs to be able to tell from a re-run that ran.
	RunID     string             `json:"run_id,omitempty"`
	Preserved PreservedArtifacts `json:"preserved"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// Validate reports every contract violation in the record at once.
func (r Rerun) Validate() error {
	var problems []error
	if r.SchemaVersion != RerunSchemaVersion {
		problems = append(problems, fmt.Errorf("triage re-run schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(r.DocketKey) == "" {
		problems = append(problems, errors.New("docket key is required"))
	}
	if strings.TrimSpace(r.PriorRunID) == "" {
		problems = append(problems, errors.New("prior run id is required"))
	}
	if strings.TrimSpace(r.WorkItemID) == "" {
		problems = append(problems, errors.New("work item id is required"))
	}
	// The reason is required for the same reason the run's own selection requires
	// one: a re-run nothing accounts for is exactly what recording this makes
	// visible, and a blank reason would look like a record of something.
	if strings.TrimSpace(r.Reason) == "" {
		problems = append(problems, errors.New("the triage decision this was run again on is required"))
	}
	if len(r.Reason) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("triage re-run reason is %d bytes, which exceeds the %d byte bound", len(r.Reason), MaxSelectionReasonBytes))
	}
	if r.ClaimedAt.IsZero() {
		problems = append(problems, errors.New("claimed at is required"))
	}
	if r.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated at is required"))
	}
	if !ValidDisposition(r.Preserved.Disposition) {
		problems = append(problems, fmt.Errorf("preserved disposition %q must be %q, %q, or %q",
			r.Preserved.Disposition, PreservedKept, PreservedRetired, PreservedGone))
	}
	// A retirement that happened has a moment, and one that did not must not
	// carry one: the timestamp is the evidence the removal was made rather than
	// intended.
	if r.Preserved.Disposition == PreservedRetired && r.Preserved.RetiredAt == nil {
		problems = append(problems, errors.New("a retired artifact records when it was retired"))
	}
	if r.Preserved.Disposition != PreservedRetired && r.Preserved.RetiredAt != nil {
		problems = append(problems, fmt.Errorf("an artifact recorded as %q was not retired, so it has no retirement to date", r.Preserved.Disposition))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid triage re-run: %w", err)
	}
	return nil
}

// Render describes one re-run for whoever is reading it: what was run again, on
// what decision, and what became of what the stopped run preserved.
func (r Rerun) Render() string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "re-ran %s on the stopped work of run %s\n", r.WorkItemID, r.PriorRunID)
	if r.RunID != "" {
		fmt.Fprintf(&rendered, "  fresh run: %s\n", r.RunID)
	}
	fmt.Fprintf(&rendered, "  decided: %s\n", strings.TrimSpace(r.Reason))
	rendered.WriteString(r.Preserved.Render())
	return rendered.String()
}

// Render says what became of the stopped run's artifacts, and never says
// nothing: an artifact whose disposition a reader cannot see is the orphan this
// record exists to prevent.
func (p PreservedArtifacts) Render() string {
	if p.Branch == "" && p.WorktreePath == "" {
		return "  the stopped run preserved no branch or worktree\n"
	}
	var rendered strings.Builder
	state := p.Disposition
	if p.Disposition == PreservedKept {
		state = "kept until the fresh run integrates"
	}
	if p.Branch != "" {
		fmt.Fprintf(&rendered, "  preserved branch (%s): %s\n", state, p.Branch)
	}
	if p.WorktreePath != "" {
		fmt.Fprintf(&rendered, "  preserved worktree (%s): %s\n", state, p.WorktreePath)
	}
	if p.Problem != "" {
		fmt.Fprintf(&rendered, "  not retired: %s\n", p.Problem)
	}
	return rendered.String()
}

// ErrRerunTaken is what a second re-run of one docketed stoppage unwraps
// to, so a caller can tell "this entry has already been run again" from a store
// that could not be read without matching on the words of either.
var ErrRerunTaken = errors.New("this triage entry has already been re-run")

// RerunTakenError names the re-run that already exists. It carries the
// record rather than only refusing, because what the asker needs is what the
// first re-run became: a fresh run that is still going is a different situation
// from one that ran and stopped again.
type RerunTakenError struct {
	Existing Rerun
}

func (e RerunTakenError) Error() string {
	run := e.Existing.RunID
	if strings.TrimSpace(run) == "" {
		return fmt.Sprintf("the stoppage of run %s was already re-run at %s, and no fresh run was recorded for it; triage re-runs a docketed stoppage once",
			e.Existing.PriorRunID, e.Existing.ClaimedAt.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("the stoppage of run %s was already re-run as %s; triage re-runs a docketed stoppage once",
		e.Existing.PriorRunID, run)
}

func (e RerunTakenError) Unwrap() error { return ErrRerunTaken }

// RerunStore is where the re-runs live: one directory under the product,
// beside the counters that bound them, and one file per docketed stoppage inside
// it. A file each rather than one shared record is what keeps two re-runs of two
// stoppages out of each other's way.
type RerunStore struct {
	root      string
	productID domain.ProductID
}

func NewRerunStore(root string, productID domain.ProductID) (*RerunStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &RerunStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "reruns"),
		productID: productID,
	}, nil
}

// Reruns is the re-run record for this run store's product, reached from here
// for the reason the counters are: whoever can read what became of an item's
// runs can read what triage has already done about one of them, without being
// told the state root a second time.
func (s *Store) Reruns() *RerunStore {
	return &RerunStore{
		root:      filepath.Join(filepath.Dir(s.root), "reruns"),
		productID: s.productID,
	}
}

func (s *RerunStore) Root() string { return s.root }

// Claim records that this docketed stoppage is being run again, and refuses a
// stoppage that already has a re-run. It is written before the fresh run is
// started, which is what makes the refusal mean anything: a claim taken after
// the run would leave the window where two of them start at once open.
func (s *RerunStore) Claim(ctx context.Context, rerun Rerun) (Rerun, error) {
	key := strings.TrimSpace(rerun.DocketKey)
	if key == "" {
		return Rerun{}, errors.New("a docket entry is required to record its re-run")
	}
	claimed := rerun
	claimed.SchemaVersion = RerunSchemaVersion
	claimed.ProductID = s.productID
	claimed.DocketKey = key
	if claimed.ClaimedAt.IsZero() {
		claimed.ClaimedAt = time.Now().UTC()
	}
	claimed.ClaimedAt = claimed.ClaimedAt.UTC()
	claimed.UpdatedAt = claimed.ClaimedAt
	if claimed.Preserved.Disposition == "" {
		// Nothing has been decided about the artifacts yet, and what the harness
		// does about them is decided by what the fresh run comes to. Until then
		// they are kept, which is what the record has to say for anybody who finds
		// them in the meantime.
		claimed.Preserved.Disposition = PreservedKept
	}
	if err := claimed.Validate(); err != nil {
		return Rerun{}, err
	}

	release, err := s.lock(ctx, key)
	if err != nil {
		return Rerun{}, err
	}
	defer release()

	existing, found, err := s.load(key)
	if err != nil {
		return Rerun{}, err
	}
	if found {
		return Rerun{}, RerunTakenError{Existing: existing}
	}
	if err := s.save(key, claimed); err != nil {
		return Rerun{}, err
	}
	return claimed, nil
}

// Settle records what the claimed re-run came to: the fresh run it started, and
// what the harness did about what the stopped run preserved. It is a separate
// write from the claim because the two are separated by the whole of a run — the
// fresh run's identifier does not exist when the claim is taken, and what becomes
// of the preserved artifacts is not known until it ends.
func (s *RerunStore) Settle(ctx context.Context, docketKey, runID string, preserved PreservedArtifacts) (Rerun, error) {
	key := strings.TrimSpace(docketKey)
	if key == "" {
		return Rerun{}, errors.New("a docket entry is required to settle its re-run")
	}
	if !ValidDisposition(preserved.Disposition) {
		return Rerun{}, fmt.Errorf("preserved disposition %q must be %q, %q, or %q",
			preserved.Disposition, PreservedKept, PreservedRetired, PreservedGone)
	}

	release, err := s.lock(ctx, key)
	if err != nil {
		return Rerun{}, err
	}
	defer release()

	settled, found, err := s.load(key)
	if err != nil {
		return Rerun{}, err
	}
	if !found {
		return Rerun{}, fmt.Errorf("no re-run of the stoppage keyed %s is recorded, so there is nothing to settle", key)
	}
	if run := strings.TrimSpace(runID); run != "" {
		settled.RunID = run
	}
	settled.Preserved = preserved
	settled.UpdatedAt = time.Now().UTC()
	if err := s.save(key, settled); err != nil {
		return Rerun{}, err
	}
	return settled, nil
}

// Withdraw gives back the claim on one docketed stoppage, so the stoppage keeps
// the re-run a decision authorized rather than having spent it on nothing.
//
// It is not a general undo, and everything about it is narrow on purpose: a
// claim is spent by being taken, and the only claim it is sound to give back is
// one whose fresh run provably never existed. What leaves that behind is the
// pipeline answering before it reserved a run — a refused developer slot, a pause
// the operator's hold or an unresolved directive or unfinished work the item
// waits on stopped it with, or any other refusal it made from the item's state,
// the repository's, or the provider's. All of them stop at or before the
// reservation, which is where the run's record, the work item's claim and every
// agent are on the far side of: there is a claim here and demonstrably nothing it
// caused. A record carrying a run is therefore refused rather than removed:
// whatever became of that run, something happened on this claim, and a second
// re-run of it is the thing the claim exists to prevent.
//
// One case is on the far side of the reservation and belongs here all the same:
// a fresh run the environment refused before any agent of it ran. That run's
// record exists and says what stopped it, but the claim bought nothing — no
// developer was invoked and no change was delivered — and the run's own settle
// is what proves it rather than this store taking anybody's word. The same
// refusal is what keeps it sound: the fresh run's identifier is written here by
// Settle, which a run refused that way never reaches, so the record this finds
// still carries no run and the check below is still the whole of the condition.
//
// A stoppage nothing has claimed is already what this would leave behind, so
// withdrawing one is not an error.
func (s *RerunStore) Withdraw(ctx context.Context, docketKey string) error {
	key := strings.TrimSpace(docketKey)
	if key == "" {
		return errors.New("a docket entry is required to withdraw its re-run")
	}

	release, err := s.lock(ctx, key)
	if err != nil {
		return err
	}
	defer release()

	claimed, found, err := s.load(key)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if run := strings.TrimSpace(claimed.RunID); run != "" {
		return fmt.Errorf("the re-run of the stoppage keyed %s started run %s, so its claim was acted on and is not one to give back", key, run)
	}
	if err := os.Remove(s.path(key)); err != nil {
		return fmt.Errorf("withdraw the triage re-run of %s: %w", key, err)
	}
	return syncDirectory(s.root)
}

// Find reports the re-run of one docketed stoppage. A stoppage nothing has been
// recorded about is the ordinary answer rather than a failure to look, and a
// record that cannot be read is neither: it is an error, because a claim nobody
// can read must never be claimed through as though it were absent.
func (s *RerunStore) Find(docketKey string) (Rerun, bool, error) {
	key := strings.TrimSpace(docketKey)
	if key == "" {
		return Rerun{}, false, errors.New("a docket entry is required to read its re-run")
	}
	return s.load(key)
}

// Claimed reports the re-runs this product has already taken of one work item's
// stoppages, oldest claim first. It is what says a decision has been acted on: a
// decision authorizes one re-run, so an item with as many claims as decisions has
// had everything triage decided about it carried out, and a further stoppage of
// it needs a further decision rather than the same one a second time.
//
// A claim counts whatever became of the run it caused. That is the direction this
// record fails in everywhere else: a re-run claimed and then not run is an
// attempt nobody took rather than one nobody counted, and treating it as unspent
// is what would let one decision start two runs.
func (s *RerunStore) Claimed(workItemID string) ([]Rerun, error) {
	id := strings.TrimSpace(workItemID)
	if id == "" {
		return nil, errors.New("a work item is required to read the re-runs taken of its stoppages")
	}
	recorded, err := s.List()
	if err != nil {
		return nil, err
	}
	var claimed []Rerun
	for _, rerun := range recorded {
		if rerun.WorkItemID == id {
			claimed = append(claimed, rerun)
		}
	}
	return claimed, nil
}

// List reports every re-run recorded for this product, oldest claim first. A
// product nothing has been re-run in lists nothing, which is not a failure to
// look; a record that cannot be read is one, because a claim nobody can read
// must never be counted as absent.
func (s *RerunStore) List() ([]Rerun, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the triage re-runs: %w", err)
	}
	var claimed []Rerun
	for _, entry := range entries {
		// The directory also holds each record's lock file and the temporary files
		// a replacement is written through. Only the records are read: a lock is
		// named for the record it guards and would otherwise be decoded as one.
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		recorded, err := s.read(filepath.Join(s.root, name))
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, recorded)
	}
	sort.Slice(claimed, func(first, second int) bool {
		return claimed[first].ClaimedAt.Before(claimed[second].ClaimedAt)
	})
	return claimed, nil
}

func (s *RerunStore) load(docketKey string) (Rerun, bool, error) {
	recorded, err := s.read(s.path(docketKey))
	if errors.Is(err, os.ErrNotExist) {
		return Rerun{}, false, nil
	}
	if err != nil {
		return Rerun{}, false, err
	}
	// The file is named for a digest of the docket key rather than for the key
	// itself, so this is what catches two keys that were ever to land on one name:
	// the record says which stoppage it is about, and one that is not this
	// stoppage's is refused rather than treated as its claim.
	if recorded.DocketKey != docketKey {
		return Rerun{}, false, fmt.Errorf("triage re-run at %s belongs to docket entry %q, not %q", s.path(docketKey), recorded.DocketKey, docketKey)
	}
	return recorded, true, nil
}

// read decodes one record. A file that is not there is reported as itself, so a
// caller asking about one stoppage can tell an absent claim from an unreadable
// one; everything else is an error, because a claim nobody can read must never
// be read as absent.
func (s *RerunStore) read(path string) (Rerun, error) {
	file, err := os.Open(path)
	if err != nil {
		return Rerun{}, fmt.Errorf("open triage re-run: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var recorded Rerun
	if err := decoder.Decode(&recorded); err != nil {
		return Rerun{}, fmt.Errorf("decode triage re-run at %s: %w", path, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Rerun{}, fmt.Errorf("decode triage re-run at %s: %w", path, err)
	}
	if err := recorded.Validate(); err != nil {
		return Rerun{}, err
	}
	if recorded.ProductID != s.productID {
		return Rerun{}, fmt.Errorf("triage re-run belongs to product %q, not %q", recorded.ProductID, s.productID)
	}
	return recorded, nil
}

// save replaces one stoppage's re-run durably, as a temporary file and a rename,
// so a process that dies mid-write leaves the previous record rather than a
// truncated file nothing can read.
func (s *RerunStore) save(docketKey string, rerun Rerun) error {
	rerun.SchemaVersion = RerunSchemaVersion
	rerun.ProductID = s.productID
	rerun.DocketKey = docketKey
	if err := rerun.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create triage re-run directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".rerun-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary triage re-run: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary triage re-run: %w", err)
	}
	if err := writeJSONFile(temporary, "triage re-run", rerun); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary triage re-run: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path(docketKey)); err != nil {
		return fmt.Errorf("replace triage re-run: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the read-modify-write on one stoppage's record across every
// Yoyodyne process, and the lock file outlives the write for the reason the
// counters' does: removing it while another process held it would let a third
// take a lock on a file nobody else can see.
func (s *RerunStore) lock(ctx context.Context, docketKey string) (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create triage re-run directory: %w", err)
	}
	file, err := os.OpenFile(s.path(docketKey)+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open triage re-run lock: %w", err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the triage re-run of %s: %w", docketKey, err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

// path names one stoppage's record. The name is built the same way a counter
// file's is — a bounded readable rendering with a digest of the exact key after
// it — because a docket key is no more a file name than a tracker identifier is.
func (s *RerunStore) path(docketKey string) string {
	return filepath.Join(s.root, triageKey(docketKey)+".json")
}
