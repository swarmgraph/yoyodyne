package runstate

// A developer's provider that did not put in force what a developer is
// launched with, recorded where every pull reads it.
//
// Before a developer is started the harness asks the provider whether the
// sandbox, the notes guard, and the settings that keep the operator's personal
// configuration out took effect (backend.LaunchSettingsChecker). Where they did
// not, the dispatch is refused before anything is claimed, and the refusal is
// recorded here once for the product rather than once per item: whatever made
// the provider decline the settings — a CLI upgrade, a policy on the machine —
// declines them for every item alike, so a line that went on dispatching would
// refuse its whole ready queue one item at a time and say the same thing for
// each. A watching session reads this at every pull and starts no developer
// while it stands.
//
// It is lifted by the cause going away: a check that finds the settings in
// force again, which the dispatch a pull lets through once the probe interval
// has passed makes, and a pull on a harness build other than the one that
// placed it, because a landing that changes what the adapter passes is the
// other thing that ends it. Nothing a person releases ends it, because there is
// nothing to decide: what a person does is fix the installation, and the next
// check finds that out.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// LaunchSettingsHoldSchemaVersion is 1 and has never changed.
const LaunchSettingsHoldSchemaVersion = 1

// MaxLaunchSettingsTextBytes bounds each piece of prose the hold carries.
const MaxLaunchSettingsTextBytes = 4 << 10

// LaunchSettingsHold is the recorded fact that a developer's provider did not
// put in force what a developer is launched with: which provider and version,
// on which harness build, what did not take, and since when.
type LaunchSettingsHold struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Provider is the backend the developer is invoked on, and Version that
	// provider's own answer to being asked its version.
	Provider domain.Backend `json:"provider"`
	Version  string         `json:"version"`
	// Build is the harness revision whose check found it, which is what a
	// landing that changes the adapter is told apart by. Empty is a binary that
	// recorded no revision of its own.
	Build string `json:"build,omitempty"`
	// NotInForce is each setting or flag that did not take, as the check said it.
	NotInForce []string `json:"not_in_force"`
	// Since is when it was first found, and LastSeen when a check last confirmed
	// it.
	Since    time.Time `json:"since"`
	LastSeen time.Time `json:"last_seen"`
	// Refusals is how many dispatches it has refused.
	Refusals int `json:"refusals"`
	// Waiting names what the first refusal stopped, in words.
	Waiting string `json:"waiting,omitempty"`
}

func (h LaunchSettingsHold) Validate() error {
	var problems []error
	if h.SchemaVersion != LaunchSettingsHoldSchemaVersion {
		problems = append(problems, fmt.Errorf("launch settings hold schema version %d is not supported", h.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(h.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(string(h.Provider)) == "" {
		problems = append(problems, errors.New("provider is required"))
	}
	if len(h.NotInForce) == 0 {
		problems = append(problems, errors.New("a launch settings hold names what did not take"))
	}
	for _, missing := range append([]string{h.Version, h.Build, h.Waiting}, h.NotInForce...) {
		if len(missing) > MaxLaunchSettingsTextBytes {
			problems = append(problems, fmt.Errorf("a launch settings hold carries %d bytes of text, which exceeds the %d byte bound", len(missing), MaxLaunchSettingsTextBytes))
		}
	}
	if h.Since.IsZero() || h.LastSeen.IsZero() {
		problems = append(problems, errors.New("since and last seen are required"))
	}
	if !h.Since.IsZero() && h.LastSeen.Before(h.Since) {
		problems = append(problems, errors.New("last seen is before since"))
	}
	if h.Refusals < 1 {
		problems = append(problems, fmt.Errorf("refusals is %d, and a recorded hold refused at least one dispatch", h.Refusals))
	}
	return errors.Join(problems...)
}

// Says is the hold in the one sentence every surface states it in: what is held,
// why, and what ends it.
func (h LaunchSettingsHold) Says() string {
	return fmt.Sprintf("No developer is started on %s: the installed CLI (%s) did not put in force what a developer is launched with: %s. "+
		"The harness checks again by itself at the first dispatch after the probe interval and on a new harness build; held since %s",
		h.Provider, h.version(), strings.Join(h.NotInForce, "; "), h.Since.Local().Format("2006-01-02 15:04 MST"))
}

// version names the provider version the hold was found on.
func (h LaunchSettingsHold) version() string {
	if version := strings.TrimSpace(h.Version); version != "" {
		return version
	}
	return "which gave no version"
}

// LaunchSettingsHoldStore is where the hold is recorded: one file under the
// product, written the way ProviderOutageStore writes its own and for the
// reason that store gives.
type LaunchSettingsHoldStore struct {
	root      string
	productID domain.ProductID
}

func NewLaunchSettingsHoldStore(root string, productID domain.ProductID) (*LaunchSettingsHoldStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &LaunchSettingsHoldStore{root: home.ProductDirectory(root, string(productID)), productID: productID}, nil
}

// LaunchSettingsObservation is one check finding the settings not in force.
type LaunchSettingsObservation struct {
	Provider   domain.Backend
	Version    string
	Build      string
	NotInForce []string
	Waiting    string
	At         time.Time
}

// Notice records a check finding the settings not in force, and reports the
// hold as it now stands and whether this observation opened it. A finding on
// the provider, version, and build of the hold already standing is the same
// hold: it keeps when it began and counts the refusal. Any other is a different
// cause and begins afresh, because it is a different thing to tell the
// operator.
//
// Two dispatches can be refused at the same moment, so the read and the write
// are made under the store's lock: the second waits for the first and finds the
// hold it opened, which is what keeps one cause to one report.
func (s *LaunchSettingsHoldStore) Notice(observed LaunchSettingsObservation) (LaunchSettingsHold, bool, error) {
	unlock, err := s.lock()
	if err != nil {
		return LaunchSettingsHold{}, false, err
	}
	defer unlock()
	at := observed.At
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()
	standing, found, err := s.Standing()
	if err != nil {
		return LaunchSettingsHold{}, false, err
	}
	notInForce := make([]string, 0, len(observed.NotInForce))
	for _, missing := range observed.NotInForce {
		notInForce = append(notInForce, boundLaunchSettingsText(missing))
	}
	recorded := LaunchSettingsHold{
		SchemaVersion: LaunchSettingsHoldSchemaVersion,
		ProductID:     s.productID,
		Provider:      observed.Provider,
		Version:       boundLaunchSettingsText(observed.Version),
		Build:         strings.TrimSpace(observed.Build),
		NotInForce:    notInForce,
		Since:         at,
		LastSeen:      at,
		Refusals:      1,
		Waiting:       boundLaunchSettingsText(observed.Waiting),
	}
	opened := !found || standing.Provider != recorded.Provider || standing.Version != recorded.Version || standing.Build != recorded.Build
	if !opened {
		recorded.Since = standing.Since
		recorded.Refusals = standing.Refusals + 1
		recorded.Waiting = standing.Waiting
		if at.Before(recorded.Since) {
			recorded.LastSeen = recorded.Since
		}
	}
	if err := recorded.Validate(); err != nil {
		return LaunchSettingsHold{}, false, err
	}
	if err := s.write(recorded); err != nil {
		return LaunchSettingsHold{}, false, err
	}
	return recorded, opened, nil
}

// Standing reports whether a hold is recorded. No record is the ordinary
// answer; a record that cannot be read is an error, never an absence.
func (s *LaunchSettingsHoldStore) Standing() (LaunchSettingsHold, bool, error) {
	file, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return LaunchSettingsHold{}, false, nil
	}
	if err != nil {
		return LaunchSettingsHold{}, false, fmt.Errorf("open launch settings hold: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var standing LaunchSettingsHold
	if err := decoder.Decode(&standing); err != nil {
		return LaunchSettingsHold{}, false, fmt.Errorf("decode launch settings hold: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return LaunchSettingsHold{}, false, fmt.Errorf("decode launch settings hold: %w", err)
	}
	if err := standing.Validate(); err != nil {
		return LaunchSettingsHold{}, false, err
	}
	if standing.ProductID != s.productID {
		return LaunchSettingsHold{}, false, fmt.Errorf("launch settings hold belongs to product %q, not %q", standing.ProductID, s.productID)
	}
	return standing, true, nil
}

// Clear lifts the hold and reports what was standing. Clearing what is not
// standing is not an error.
func (s *LaunchSettingsHoldStore) Clear() (LaunchSettingsHold, bool, error) {
	standing, found, err := s.Standing()
	if err != nil || !found {
		return LaunchSettingsHold{}, false, err
	}
	if err := os.Remove(s.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return LaunchSettingsHold{}, false, fmt.Errorf("clear launch settings hold: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return LaunchSettingsHold{}, false, err
	}
	return standing, true, nil
}

func (s *LaunchSettingsHoldStore) write(recorded LaunchSettingsHold) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create product state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".launch-settings-hold-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary launch settings hold: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary launch settings hold: %w", err)
	}
	if err := writeJSONFile(temporary, "launch settings hold", recorded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary launch settings hold: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path()); err != nil {
		return fmt.Errorf("replace launch settings hold: %w", err)
	}
	return syncDirectory(s.root)
}

// lock takes the store's exclusive lock and returns what releases it.
func (s *LaunchSettingsHoldStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create product state directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(s.root, "launch-settings-hold.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open launch settings hold lock: %w", err)
	}
	if err := lockStateFile(context.Background(), file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock launch settings hold: %w", err)
	}
	return func() {
		_ = unlockStateFile(file)
		_ = file.Close()
	}, nil
}

// path names the hold's file: one fixed name under the product.
func (s *LaunchSettingsHoldStore) path() string {
	return filepath.Join(s.root, "launch-settings-hold.json")
}

func boundLaunchSettingsText(text string) string {
	return oneline.Fold(text, MaxLaunchSettingsTextBytes-len(oneline.Marker))
}
