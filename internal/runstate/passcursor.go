package runstate

// Where a program manager instance has read its event streams up to.
//
// An instance is woken by what happened in the product as well as by its
// schedule: a run landing, a run stopping, work being admitted. What makes that
// a wake rather than a flood is a position per stream. Everything recorded past
// the position is what the next pass is handed, and the position moves only
// once a pass has been handed it and completed — so a burst is one pass, and a
// pass that failed leaves the same events for the next one rather than losing
// them. See "Triggers and passes" in docs/designs/program-manager.md.
//
// The position is a moment rather than an offset into either stream. Both
// streams are read by when their entries say they happened, the run records by
// when each run ended and the tracker's export by when each item was created,
// and neither is an append-only file an offset would stay valid in: a run
// record is rewritten in place, and the export is regenerated whole.
//
// A moment alone would lose what arrives late. The tracker's export is written
// after the tracker's own write, and a run record is readable only once it is
// saved, so an entry can say it happened before a pass was taken and appear
// only after the pass moved the position past it. So every read reaches back
// PassLateness behind the position, and the cursor keeps what each completed
// pass carried from inside that reach, by key, so the overlap hands a pass what
// arrived late and never what a pass already carried.
//
// It lives beside the instance's lane report, under the state root, and never
// in the repository: it is the harness's bookkeeping about one instance, not
// anything a person edits.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// PassCursorSchemaVersion is the shape of a stored cursor.
const PassCursorSchemaVersion = 1

// The event streams a program manager instance reads. The set is closed for
// the reason the trigger classes are: a stream the harness does not read is a
// position nothing ever moves.
const (
	// PassStreamRuns is the run records: a run that landed its change, and a
	// run that stopped with its work item back in somebody's hands.
	PassStreamRuns = "runs"
	// PassStreamTracker is the tracker: work admitted to the backlog.
	PassStreamTracker = "tracker"
)

// PassStreams is the closed set, in the order a pass's message lists them.
var PassStreams = []string{PassStreamRuns, PassStreamTracker}

func validPassStream(stream string) bool {
	for _, known := range PassStreams {
		if stream == known {
			return true
		}
	}
	return false
}

// PassLateness is how far behind its position a stream is read again, which is
// how late an entry may appear in its stream and still be carried. The export
// lags the tracker by the moments an export takes, so this is far past any lag
// a healthy tracker shows; an entry later than this is outside what a pass
// promises to carry.
const PassLateness = 15 * time.Minute

const (
	passCursorFile     = "cursor.json"
	passCursorLockFile = ".cursor.lock"
)

// PassCursor is one instance's position in each stream it reads.
type PassCursor struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Agent         string           `json:"agent"`
	// Streams is where each stream has been read up to: every entry at or before
	// the moment named has been handed to a pass that completed, or predates the
	// instance being watched at all.
	Streams map[string]time.Time `json:"streams"`
	// WatchedFrom is when each stream began to be watched. A read reaching back
	// PassLateness never reaches before it, so what happened before the instance
	// was watched is never handed to it.
	WatchedFrom map[string]time.Time `json:"watched_from,omitempty"`
	// Carried is, for each stream, the entries completed passes carried that
	// are still inside the reach back from the position, by key, with when each
	// happened. The overlap is read against it, and an entry that falls out of
	// the reach is dropped from it.
	Carried   map[string]map[string]time.Time `json:"carried,omitempty"`
	UpdatedAt time.Time                       `json:"updated_at"`
}

// ReadFrom is where a stream is read from: PassLateness behind its position,
// and never before the stream began to be watched.
func (c PassCursor) ReadFrom(stream string) time.Time {
	from := c.Streams[stream].Add(-PassLateness)
	if watched, known := c.WatchedFrom[stream]; known && from.Before(watched) {
		return watched
	}
	return from
}

// WasCarried reports an entry a completed pass already carried.
func (c PassCursor) WasCarried(stream, key string) bool {
	_, carried := c.Carried[stream][key]
	return carried
}

// Validate reports every contract violation in the cursor at once.
func (c PassCursor) Validate() error {
	var problems []error
	if c.SchemaVersion != PassCursorSchemaVersion {
		problems = append(problems, fmt.Errorf("pass cursor schema version %d is not supported", c.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("agent", c.Agent); err != nil {
		problems = append(problems, err)
	}
	streams := make([]string, 0, len(c.Streams))
	for stream := range c.Streams {
		streams = append(streams, stream)
	}
	sort.Strings(streams)
	for _, stream := range streams {
		if !validPassStream(stream) {
			problems = append(problems, fmt.Errorf("stream %q is not one a pass reads; the streams are %v", stream, PassStreams))
		}
		if c.Streams[stream].IsZero() {
			problems = append(problems, fmt.Errorf("stream %q has no position", stream))
		}
	}
	for stream, carried := range c.Carried {
		if !validPassStream(stream) {
			problems = append(problems, fmt.Errorf("carried stream %q is not one a pass reads", stream))
		}
		for key := range carried {
			if key == "" {
				problems = append(problems, fmt.Errorf("carried stream %q holds an entry with no key", stream))
			}
		}
	}
	for stream := range c.WatchedFrom {
		if !validPassStream(stream) {
			problems = append(problems, fmt.Errorf("watched stream %q is not one a pass reads", stream))
		}
	}
	if c.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated_at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid pass cursor: %w", err)
	}
	return nil
}

// PassCursorStore keeps every instance's cursor for one product.
type PassCursorStore struct {
	root      string
	productID domain.ProductID
}

// NewPassCursorStore opens the cursors kept under a state root.
func NewPassCursorStore(root string, productID domain.ProductID) (*PassCursorStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &PassCursorStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "program-managers"),
		productID: productID,
	}, nil
}

// PassCursors is the cursor store for this run store's product, reached from
// here for the reason the sweeps are.
func (s *Store) PassCursors() *PassCursorStore {
	return &PassCursorStore{
		root:      filepath.Join(filepath.Dir(s.root), "program-managers"),
		productID: s.productID,
	}
}

// Path names one instance's cursor, so a failure can say where it is.
func (s *PassCursorStore) Path(agent string) string {
	return filepath.Join(s.root, agent, passCursorFile)
}

// Load reads one instance's cursor. An instance that has never been watched is
// the ordinary answer rather than a failure; a cursor that cannot be read is
// neither, because reading past it as absent would hand the next pass either
// nothing or everything the streams have ever held.
func (s *PassCursorStore) Load(agent string) (PassCursor, bool, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return PassCursor{}, false, err
	}
	stored, err := os.ReadFile(s.Path(agent))
	if errors.Is(err, os.ErrNotExist) {
		return PassCursor{}, false, nil
	}
	if err != nil {
		return PassCursor{}, false, fmt.Errorf("read the %s pass cursor: %w", agent, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(stored)))
	decoder.DisallowUnknownFields()
	var cursor PassCursor
	if err := decoder.Decode(&cursor); err != nil {
		return PassCursor{}, false, fmt.Errorf("decode the %s pass cursor at %s: %w", agent, s.Path(agent), err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return PassCursor{}, false, fmt.Errorf("decode the %s pass cursor at %s: %w", agent, s.Path(agent), err)
	}
	if err := cursor.Validate(); err != nil {
		return PassCursor{}, false, err
	}
	if cursor.ProductID != s.productID {
		return PassCursor{}, false, fmt.Errorf("the pass cursor at %s belongs to product %q, not %q", s.Path(agent), cursor.ProductID, s.productID)
	}
	if cursor.Agent != agent {
		return PassCursor{}, false, fmt.Errorf("the pass cursor at %s belongs to agent %q, not %q", s.Path(agent), cursor.Agent, agent)
	}
	return cursor, true, nil
}

// Advance moves the named streams of one instance's cursor to the positions
// given, records what the pass carried from each, and leaves every other stream
// where it was. A position is never moved backwards: two sessions settling
// passes over one instance must not hand the later of them events the earlier
// already carried. A stream positioned for the first time begins to be watched
// at that position.
func (s *PassCursorStore) Advance(ctx context.Context, agent string, positions map[string]time.Time, carried map[string]map[string]time.Time, now time.Time) (PassCursor, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return PassCursor{}, err
	}
	release, err := s.lock(ctx, agent)
	if err != nil {
		return PassCursor{}, err
	}
	defer release()

	cursor, found, err := s.Load(agent)
	if err != nil {
		return PassCursor{}, err
	}
	if !found {
		cursor = PassCursor{Agent: agent, Streams: map[string]time.Time{}}
	}
	if cursor.Streams == nil {
		cursor.Streams = map[string]time.Time{}
	}
	if cursor.WatchedFrom == nil {
		cursor.WatchedFrom = map[string]time.Time{}
	}
	if cursor.Carried == nil {
		cursor.Carried = map[string]map[string]time.Time{}
	}
	for stream, at := range positions {
		if !validPassStream(stream) {
			return PassCursor{}, fmt.Errorf("stream %q is not one a pass reads; the streams are %v", stream, PassStreams)
		}
		at = at.UTC()
		current, kept := cursor.Streams[stream]
		if !kept {
			cursor.WatchedFrom[stream] = at
		}
		if !kept || at.After(current) {
			cursor.Streams[stream] = at
		}
	}
	for stream, keys := range carried {
		if !validPassStream(stream) {
			return PassCursor{}, fmt.Errorf("stream %q is not one a pass reads; the streams are %v", stream, PassStreams)
		}
		for key, at := range keys {
			if key == "" {
				return PassCursor{}, fmt.Errorf("an entry of the %s stream was carried with no key", stream)
			}
			if cursor.Carried[stream] == nil {
				cursor.Carried[stream] = map[string]time.Time{}
			}
			cursor.Carried[stream][key] = at.UTC()
		}
	}
	// What has fallen out of the reach back from the position can no longer be
	// read again, so it no longer needs remembering.
	for stream, keys := range cursor.Carried {
		reach := cursor.ReadFrom(stream)
		for key, at := range keys {
			if at.Before(reach) {
				delete(keys, key)
			}
		}
		if len(keys) == 0 {
			delete(cursor.Carried, stream)
		}
	}
	if len(cursor.WatchedFrom) == 0 {
		cursor.WatchedFrom = nil
	}
	if len(cursor.Carried) == 0 {
		cursor.Carried = nil
	}
	at := now
	if at.IsZero() {
		at = time.Now()
	}
	cursor.SchemaVersion = PassCursorSchemaVersion
	cursor.ProductID = s.productID
	cursor.UpdatedAt = at.UTC()
	if err := cursor.Validate(); err != nil {
		return PassCursor{}, err
	}
	encoded, err := json.MarshalIndent(cursor, "", "  ")
	if err != nil {
		return PassCursor{}, fmt.Errorf("encode the %s pass cursor: %w", agent, err)
	}
	confined, err := repowrite.NewRoot(filepath.Dir(s.root))
	if err != nil {
		return PassCursor{}, fmt.Errorf("resolve the product's state directory: %w", err)
	}
	if _, err := confined.WriteFile(filepath.Join(filepath.Base(s.root), agent, passCursorFile), append(encoded, '\n')); err != nil {
		return PassCursor{}, fmt.Errorf("record the %s pass cursor: %w", agent, err)
	}
	return cursor, nil
}

// lock serializes the read-modify-write of one instance's cursor across every
// Yoyodyne process.
func (s *PassCursorStore) lock(ctx context.Context, agent string) (func(), error) {
	directory := filepath.Join(s.root, agent)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create the %s pass cursor directory: %w", agent, err)
	}
	file, err := os.OpenFile(filepath.Join(directory, passCursorLockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the %s pass cursor lock: %w", agent, err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the %s pass cursor: %w", agent, err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}
