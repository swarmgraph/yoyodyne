package beads

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// Squashing the live tracker store's history.
//
// The design on keeping the store small (docs/designs/tracker-store-size.md)
// lets the live store's record of past versions be squashed, and never an item
// or a note. Whether a squash is due, and which, is the maintenance pass's to
// decide from the latest measurement on a copy (internal/maintain); this is
// the squash itself, and it holds two of the design's three conditions:
//
//  1. Maintenance alone holds the store. Nothing starts while another process
//     has the store open, and, unlike the cleanup, nothing starts where who has
//     it open cannot be told: a squash cannot be undone.
//  2. The live store is copied first, whole, into a directory the caller owns,
//     refused as a measurement's copy is where the store changed while it was
//     copied or holds a symbolic link. Who has the store open is looked at
//     again once the copy is made, and the caller is then handed the copy so
//     its place is recorded before bd runs. A copy that is not recorded, or
//     whose squash does not start, is removed, so no copy is left that nothing
//     names.
//
// The squash runs on the store that was copied, pinned by BEADS_DIR, under
// limit. bd holds its exclusive lock on the store while it runs, so every
// other tracker call that arrives meanwhile waits behind it, and one whose own
// limit runs out first fails as a timeout and is its caller's to try again.
// How long the store was held is what Took records.

// StoreSquash is one squash of the live store: what stopped it starting, or
// the copy made first and what the squash came to.
type StoreSquash struct {
	// Store is the live store directory, and Stage the squash: StageOldSquashed
	// or StageAllSquashed, the measurement's names for the same commands.
	Store string `json:"store"`
	Stage string `json:"stage"`
	// Busy is the processes that had the store open, and Unknown why who had it
	// open could not be told; either stopped the squash before anything was
	// copied, or once the copy was made, in which case the copy was removed.
	Busy    []StoreHolder `json:"busy,omitempty"`
	Unknown string        `json:"unknown,omitempty"`
	// Moved is what changed in the store while it was copied, where something
	// did. The copy was removed and nothing was squashed.
	Moved string `json:"moved,omitempty"`
	// Copy is the copy of the live store made before the squash, CopiedAt when
	// it was begun, and CopyTook how long it took.
	Copy     string        `json:"copy,omitempty"`
	CopiedAt time.Time     `json:"copied_at"`
	CopyTook time.Duration `json:"copy_took,omitempty"`
	// Command is the bd command run on the live store, StartedAt when, and Took
	// how long it held the store. Said is what bd wrote, Failed why it did not
	// succeed, and Stopped a squash stopped at its limit.
	Command   []string      `json:"command,omitempty"`
	StartedAt time.Time     `json:"started_at"`
	Took      time.Duration `json:"took,omitempty"`
	Said      string        `json:"said,omitempty"`
	Failed    string        `json:"failed,omitempty"`
	Stopped   bool          `json:"stopped,omitempty"`
	// SizeBefore and SizeAfter are the store's database directory in bytes
	// either side of the squash.
	SizeBefore int64 `json:"size_before,omitempty"`
	SizeAfter  int64 `json:"size_after,omitempty"`
}

// Ran reports a squash that was started on the live store.
func (s StoreSquash) Ran() bool { return !s.StartedAt.IsZero() }

// SquashCommand is the bd command that makes stage, the same one a measurement
// runs on its copy, and false for a stage that is not a squash.
func SquashCommand(stage string) ([]string, bool) {
	if stage != StageOldSquashed && stage != StageAllSquashed {
		return nil, false
	}
	for _, measured := range measuredStages {
		if measured.name == stage {
			return append([]string(nil), measured.args...), true
		}
	}
	return nil, false
}

// SquashStore squashes the live store as stage says, as the comment at the top
// of this file says: alone, and only once a copy of it is in copies under name
// and copied has recorded it. limit bounds bd.
//
// The store being open elsewhere, or changing while it is copied, is not an
// error: nothing is squashed, the squash says why, and the caller tries again.
// A copy that cannot be made or recorded is an error, and nothing is squashed.
// A squash that ran and failed is returned with what was measured of it and
// the error.
func (c Client) SquashStore(ctx context.Context, stage string, copies *repowrite.PinnedRoot, name string, limit time.Duration, copied func(StoreSquash) error) (squash StoreSquash, err error) {
	squash.Stage = stage
	command, ok := SquashCommand(stage)
	if !ok {
		return squash, fmt.Errorf("%q is not a squash of the tracker's store", stage)
	}
	if limit <= 0 {
		return squash, errors.New("a squash of the tracker's store needs a limit longer than zero")
	}
	if copied == nil {
		return squash, errors.New("a squash of the tracker's store needs somewhere to record its copy")
	}
	store := storeDirectory(c.Dir, c.beadsDirVariable())
	if store == "" {
		return squash, fmt.Errorf("no tracker store was found from %s", describeDir(c.Dir))
	}
	squash.Store = store
	database := databaseDirectory(store)
	if squash.Busy, squash.Unknown = c.storeHolders(ctx, database); len(squash.Busy) > 0 || squash.Unknown != "" {
		return squash, nil
	}
	before, err := storeFingerprint(store)
	if err != nil {
		return squash, err
	}
	squash.CopiedAt = time.Now()
	if err := copyStore(store, copies, name); err != nil {
		return squash, errors.Join(fmt.Errorf("copy the tracker's store %s into %s before squashing it: %w", store, copies.Path(), err), copies.RemoveAll(name))
	}
	squash.CopyTook = time.Since(squash.CopiedAt)
	if c.storeCopied != nil {
		c.storeCopied()
	}
	after, err := storeFingerprint(store)
	if err != nil {
		return squash, errors.Join(err, copies.RemoveAll(name))
	}
	if moved := fingerprintChange(before, after); moved != "" {
		squash.Moved = moved
		return squash, copies.RemoveAll(name)
	}
	if squash.Busy, squash.Unknown = c.storeHolders(ctx, database); len(squash.Busy) > 0 || squash.Unknown != "" {
		return squash, copies.RemoveAll(name)
	}
	squash.Copy = filepath.Join(copies.Path(), name)
	if err := copied(squash); err != nil {
		squash.Copy = ""
		return squash, errors.Join(fmt.Errorf("the copy of the tracker's store could not be recorded, so the store was not squashed: %w", err), copies.RemoveAll(name))
	}
	if size, err := directorySize(database); err == nil {
		squash.SizeBefore = size
	}
	live := c
	live.beadsDir = store
	squash.Command = command
	squash.StartedAt = time.Now()
	said, runErr := live.runWithin(ctx, limit, command...)
	squash.Took = time.Since(squash.StartedAt)
	squash.Said = oneline.Fold(strings.TrimSpace(string(said)), maxMeasureSaid)
	if size, err := directorySize(database); err == nil {
		squash.SizeAfter = size
	}
	if runErr != nil {
		squash.Failed = oneline.Fold(runErr.Error(), maxMeasureSaid)
		squash.Stopped = timedOut(runErr) && ctx.Err() == nil
		return squash, runErr
	}
	return squash, nil
}

// storeHolders is who has the store open, and why that could not be told
// where it could not.
func (c Client) storeHolders(ctx context.Context, database string) ([]StoreHolder, string) {
	lookup, cancel := context.WithTimeout(ctx, holderLookupTimeout)
	defer cancel()
	holders, err := c.holdersFinder().Holders(lookup, database)
	if err != nil {
		return nil, oneline.Fold(err.Error(), maxMeasureSaid)
	}
	return holders, ""
}
