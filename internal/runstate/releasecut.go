package runstate

// What the harness's release cuts came to, one record per version and commit.
//
// A release the Lead Product Manager decided is cut by the supervisor's
// maintenance pass under approvals.releases: automatic (internal/releasecut).
// Pushing a tag is the one thing a cut does that is visible outside the
// machine and cannot be taken back, and it is several steps that cannot be
// made atomic: the gates, the tag, the push, and reading the remote to see the
// push landed. So the cut records its intent here before any tag exists —
// which version, which commit, what the readiness stamp said, which gates
// passed, and who cut — and every step after that is one a later pass can
// repeat from this record alone. A process killed between the tag and the push
// leaves a record saying exactly what the next pass has to finish.
//
// It also holds what a cut was refused on, so the same answer is not asked for
// again: a commit whose gates were red is not cut again until main moves, and
// preparation handed back to the Lead Product Manager is not handed back a
// second time for the same notes.
//
// It is one file under the product, beside the other records the supervisor
// writes, and it is written the way they are (see ProviderOutageStore).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// ReleaseCutSchemaVersion is 1 and has never changed.
const ReleaseCutSchemaVersion = 1

// MaxReleaseCutTextBytes bounds what one record says about itself.
const MaxReleaseCutTextBytes = 4 << 10

// maxReleaseCuts bounds how many records the file keeps. The oldest go first;
// what a later pass reads is the standing intent and the recent refusals, and
// a release history older than this is the forge's tags and the items' notes.
const maxReleaseCuts = 64

// ReleaseCutState is where one cut stands.
type ReleaseCutState string

const (
	// ReleaseCutRefused is a cut refused before any intent was recorded: a red
	// gate, or preparation the release item's run has still to land. No tag
	// was written.
	ReleaseCutRefused ReleaseCutState = "refused"
	// ReleaseCutIntent is a cut whose gates passed and whose intent is
	// recorded, with its tag not yet confirmed on the remote.
	ReleaseCutIntent ReleaseCutState = "intent"
	// ReleaseCutPushed is a cut whose tag the remote was read holding, on the
	// commit the intent names.
	ReleaseCutPushed ReleaseCutState = "pushed"
	// ReleaseCutBlocked is a cut waiting on the Lead Product Manager: a tag of
	// that version naming some other commit, or three passes that could not
	// confirm the push. Nothing was moved or deleted.
	ReleaseCutBlocked ReleaseCutState = "blocked"
	// ReleaseCutStale is an intent no tag was ever written for, whose commit or
	// readiness stamp has since moved. Its gates no longer describe what would
	// be tagged, so the next cut runs them again.
	ReleaseCutStale ReleaseCutState = "stale"
)

func (s ReleaseCutState) Valid() bool {
	switch s {
	case ReleaseCutRefused, ReleaseCutIntent, ReleaseCutPushed, ReleaseCutBlocked, ReleaseCutStale:
		return true
	}
	return false
}

var (
	releaseVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	releaseCommitPattern  = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	// releaseItemPattern is the shape the tracker gives an item's id, which
	// carries dots a configured identifier may not.
	releaseItemPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// ValidReleaseVersion reports whether a version is a release tag: vMAJOR.MINOR.PATCH
// and nothing else, as scripts/cut-release.sh holds it.
func ValidReleaseVersion(version string) bool {
	return releaseVersionPattern.MatchString(version)
}

// ReleaseCut is one cut of one version at one commit.
type ReleaseCut struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Version       string           `json:"version"`
	// WorkItemID is the release item the Lead Product Manager decided it in.
	WorkItemID string `json:"work_item_id"`
	// Commit is what origin's default branch held when the cut started, which
	// is what the gates ran on and what the tag names.
	Commit string          `json:"commit"`
	State  ReleaseCutState `json:"state"`
	// ReadinessVerdict and ReadinessDefinition are what the notes' readiness
	// stamp records, which the cut's gate found matching a fresh reading of
	// Commit; NotesDigest is the Git object name of the notes file at Commit,
	// so a stamp that changed afterwards is seen to have.
	ReadinessVerdict    string `json:"readiness_verdict,omitempty"`
	ReadinessDefinition string `json:"readiness_definition,omitempty"`
	NotesDigest         string `json:"notes_digest,omitempty"`
	// Gates names the gates that passed, in the order they ran.
	Gates []string `json:"gates,omitempty"`
	// CutBy says which process cut: the pass, its firing, and the machine.
	CutBy string `json:"cut_by,omitempty"`
	// Preparation marks a refusal over the release's notes or their stamp,
	// which hands the release back to the Lead Product Manager rather than
	// waiting for main to move.
	Preparation bool `json:"preparation,omitempty"`
	// Detail is what the latest step said: the refusal, the remote's answer,
	// what the record is waiting on.
	Detail string `json:"detail,omitempty"`
	// UnconfirmedTurns counts the passes that tried to publish the tag and
	// could not read it back from the remote. The third blocks the cut.
	UnconfirmedTurns int `json:"unconfirmed_turns,omitempty"`
	// Noted and Reported say the release item's note and the channel's report
	// for this record's outcome were written, so neither is written twice and
	// one that failed is written by a later pass.
	Noted      bool      `json:"noted,omitempty"`
	Reported   bool      `json:"reported,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Key names the record: one version at one commit.
func (c ReleaseCut) Key() string { return c.Version + "@" + c.Commit }

func (c ReleaseCut) Validate() error {
	var problems []error
	if c.SchemaVersion != ReleaseCutSchemaVersion {
		problems = append(problems, fmt.Errorf("release cut schema version %d is not supported", c.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !ValidReleaseVersion(c.Version) {
		problems = append(problems, fmt.Errorf("release cut version %q is not vMAJOR.MINOR.PATCH", c.Version))
	}
	if !releaseItemPattern.MatchString(c.WorkItemID) {
		problems = append(problems, fmt.Errorf("release cut work item %q is not a work item id", c.WorkItemID))
	}
	if !releaseCommitPattern.MatchString(c.Commit) {
		problems = append(problems, fmt.Errorf("release cut commit %q is not a whole object name", c.Commit))
	}
	if !c.State.Valid() {
		problems = append(problems, fmt.Errorf("release cut state %q is not one this harness names", c.State))
	}
	if c.State != ReleaseCutRefused && c.State != ReleaseCutStale && c.NotesDigest == "" {
		problems = append(problems, errors.New("a release cut past its gates records the notes it checked"))
	}
	if c.UnconfirmedTurns < 0 {
		problems = append(problems, errors.New("unconfirmed turns is negative"))
	}
	if len(c.Detail) > MaxReleaseCutTextBytes || len(c.CutBy) > MaxReleaseCutTextBytes {
		problems = append(problems, fmt.Errorf("a release cut says at most %d bytes in each of its texts", MaxReleaseCutTextBytes))
	}
	if c.RecordedAt.IsZero() || c.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at and updated_at are required"))
	}
	return errors.Join(problems...)
}

// BoundReleaseCutText folds prose to a line inside a record's bound.
func BoundReleaseCutText(text string) string {
	return oneline.Fold(text, MaxReleaseCutTextBytes-len(oneline.Marker))
}

type releaseCutFile struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Cuts          []ReleaseCut     `json:"cuts"`
}

// ReleaseCutStore is where the cuts are recorded.
type ReleaseCutStore struct {
	// state is the state root every write this store makes is confined to.
	state     stateRoot
	root      string
	productID domain.ProductID
}

func NewReleaseCutStore(root string, productID domain.ProductID) (*ReleaseCutStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	state, err := newStateRoot(root)
	if err != nil {
		return nil, err
	}
	return &ReleaseCutStore{root: home.ProductDirectory(root, string(productID)), state: state, productID: productID}, nil
}

// Path names the file, so a refusal can say where the record is.
func (s *ReleaseCutStore) Path() string { return filepath.Join(s.root, "release-cuts.json") }

// List reports every recorded cut, oldest first. No file is no cuts.
func (s *ReleaseCutStore) List() ([]ReleaseCut, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open release cuts: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var recorded releaseCutFile
	if err := decoder.Decode(&recorded); err != nil {
		return nil, fmt.Errorf("decode release cuts: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode release cuts: %w", err)
	}
	if recorded.SchemaVersion != ReleaseCutSchemaVersion {
		return nil, fmt.Errorf("release cuts schema version %d is not supported", recorded.SchemaVersion)
	}
	if recorded.ProductID != s.productID {
		return nil, fmt.Errorf("release cuts belong to product %q, not %q", recorded.ProductID, s.productID)
	}
	for _, cut := range recorded.Cuts {
		if err := cut.Validate(); err != nil {
			return nil, fmt.Errorf("release cut %s: %w", cut.Key(), err)
		}
	}
	return recorded.Cuts, nil
}

// Put records a cut, replacing the record of the same version and commit.
func (s *ReleaseCutStore) Put(cut ReleaseCut) error {
	if cut.ProductID != s.productID {
		return fmt.Errorf("release cut product %q does not match store product %q", cut.ProductID, s.productID)
	}
	if err := cut.Validate(); err != nil {
		return err
	}
	cuts, err := s.List()
	if err != nil {
		return err
	}
	replaced := false
	for index := range cuts {
		if cuts[index].Key() == cut.Key() {
			cuts[index] = cut
			replaced = true
		}
	}
	if !replaced {
		cuts = append(cuts, cut)
	}
	sort.SliceStable(cuts, func(i, j int) bool { return cuts[i].RecordedAt.Before(cuts[j].RecordedAt) })
	if len(cuts) > maxReleaseCuts {
		cuts = keepStanding(cuts)
	}
	return s.write(releaseCutFile{SchemaVersion: ReleaseCutSchemaVersion, ProductID: s.productID, Cuts: cuts})
}

// keepStanding drops the oldest settled records until the file is within its
// bound. A standing intent is never dropped: it is what a later pass finishes.
func keepStanding(cuts []ReleaseCut) []ReleaseCut {
	excess := len(cuts) - maxReleaseCuts
	kept := make([]ReleaseCut, 0, len(cuts))
	for _, cut := range cuts {
		if excess > 0 && cut.State != ReleaseCutIntent {
			excess--
			continue
		}
		kept = append(kept, cut)
	}
	return kept
}

func (s *ReleaseCutStore) write(recorded releaseCutFile) error {
	return replaceJSONFile(s.state, s.Path(), "release cuts", recorded)
}
