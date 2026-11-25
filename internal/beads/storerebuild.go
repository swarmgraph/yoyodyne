package beads

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// Rebuilding the tracker's store from its own export.
//
// The design on keeping the store small (docs/designs/tracker-store-size.md)
// brings the store back under its limit by building a fresh one from the old
// one's export, because no bd command gives back the disk space of its history
// (docs/diagnoses/yoyodyne-ifd-428-126-4-19-store-stays-over-limit.md). Items,
// notes, dependencies, labels and comments are carried across; what is dropped
// is the store's record of past versions. Whether a rebuild is due is the
// maintenance pass's to decide (internal/maintain); this is the rebuild.
//
// The build is the same on a copy and on the live store: take a full export,
// make an empty store in a directory of its own, import the export into it,
// take bd's cleanup so the new store is as small as it can be, take a second
// export, and compare the two record by record. Everything except the last
// two moves happens away from the live store.
//
// RehearseRebuild does that on a copy of the live store, and says how long
// the build took, which is what the live rebuild holds the store for. It holds
// the store lock exclusive (storelock.go) only while it copies the store, so
// the copy is not written to by the harness's own calls halfway through; the
// build itself runs on the copy with the lock released. Another process having
// the live store open never stops it, since it works on a copy.
//
// RebuildStore takes the store lock exclusive before anything else, so every
// other bd call the harness makes waits behind it rather than stopping it, and
// only then looks for processes that still have the database open. With the
// lock held, none of the harness's own calls can be running, so a process
// found then is one outside the harness, such as a person's bd at a terminal,
// and it alone stops the rebuild, by name. Otherwise the build runs, and with
// the two exports found equal, the live database is replaced: the old
// database is moved aside as the kept copy, whose place is recorded first, and
// the new one is moved into its place. A failure at either move puts the old
// database back, so the store is never left without one.
//
// Only the database directory is replaced. The store's project files, which
// carry the project's identity, and the lock files stay where they are.

// StageRebuilt names the rebuild where a record says which operation it is.
const StageRebuilt = "rebuilt from its export"

// The steps of a rebuild, as a failure or a difference names them.
const (
	RebuildExport  = "exporting the store"
	RebuildBuild   = "building a new store from the export"
	RebuildCompare = "comparing the new store's export with the first"
	RebuildMove    = "moving the new store into place"
)

const (
	rebuildFreshName  = "fresh"
	rebuildExportName = "export.jsonl"
)

// StoreRebuild is one rebuild of the store, on a copy or on the live store.
type StoreRebuild struct {
	// Store is the live store directory.
	Store string `json:"store"`
	// Busy is the processes outside the harness that had the store open once the
	// live rebuild held the store lock, and Unknown why who had it open could not
	// be told; either stopped the live rebuild before anything was built or
	// moved. Neither is looked for by a rehearsal.
	Busy    []StoreHolder `json:"busy,omitempty"`
	Unknown string        `json:"unknown,omitempty"`
	// Waited says the store lock could not be taken, because the tracker calls
	// already running did not finish within the client's bound; nothing was
	// copied, built or moved.
	Waited string `json:"waited,omitempty"`
	// Moved is what changed in the store while it was copied, for a rehearsal.
	Moved string `json:"moved,omitempty"`
	// StartedAt is when the rebuild began. CopyTook is how long the copy took, for
	// a rehearsal. Took is how long the build took: its export, new store,
	// second export and comparison. HeldFor is how long the live store's lock was
	// held, and zero for a rehearsal.
	StartedAt time.Time     `json:"started_at"`
	CopyTook  time.Duration `json:"copy_took,omitempty"`
	Took      time.Duration `json:"took,omitempty"`
	HeldFor   time.Duration `json:"held_for,omitempty"`
	// Records is how many records the export held. SizeBefore and SizeAfter are
	// the database directory in bytes before, and the new one's after.
	Records    int   `json:"records,omitempty"`
	SizeBefore int64 `json:"size_before,omitempty"`
	SizeAfter  int64 `json:"size_after,omitempty"`
	// Step is the step that failed or found a difference, and Failed why it
	// failed. Differing is how many records the two exports differed in, and
	// Missing, Added and Changed name the first of them.
	Step      string   `json:"step,omitempty"`
	Failed    string   `json:"failed,omitempty"`
	Differing int      `json:"differing,omitempty"`
	Missing   []string `json:"missing,omitempty"`
	Added     []string `json:"added,omitempty"`
	Changed   []string `json:"changed,omitempty"`
	// Kept is where the old database was moved to, and KeptAt when its place was
	// recorded. Swapped says the new database is in the live store's place, and
	// Restored that the old one was put back after the move failed.
	Kept     string    `json:"kept,omitempty"`
	KeptAt   time.Time `json:"kept_at"`
	Swapped  bool      `json:"swapped,omitempty"`
	Restored bool      `json:"restored,omitempty"`
	// ServerKept is why the tracker's server could not be stopped, so nothing was
	// built or moved.
	ServerKept string `json:"server_kept,omitempty"`
	// ServerDown is why the tracker's server, stopped for the rebuild, could not
	// be started again; the supervisor tries again at its next look.
	ServerDown string `json:"server_down,omitempty"`
}

// Passed reports a build whose two exports held every record the same.
func (r StoreRebuild) Passed() bool {
	return r.Failed == "" && r.Step == "" && r.Records > 0 && r.Differing == 0
}

// FirstDifference names the first record that differs, for a warning.
func (r StoreRebuild) FirstDifference() string {
	for _, named := range [][]string{r.Changed, r.Missing, r.Added} {
		if len(named) > 0 {
			return named[0]
		}
	}
	return ""
}

// builtStore is a new store made from an export, away from the store it was
// made from.
type builtStore struct {
	database string
	rebuild  StoreRebuild
}

// RehearseRebuild copies the live store into work and builds a new store from
// the copy, as the comment at the top of this file says. It changes nothing in
// the live store. The copy and everything made from it are removed before this
// returns.
//
// The store lock not being had in time, or the store changing while it was
// copied, which only a process outside the harness can do while the lock is
// held, is not an error: nothing is built, the rebuild says why, and the
// caller records it.
func (c Client) RehearseRebuild(ctx context.Context, work *repowrite.PinnedRoot) (rebuild StoreRebuild, err error) {
	store := storeDirectory(c.Dir, c.beadsDirVariable())
	if store == "" {
		return rebuild, fmt.Errorf("no tracker store was found from %s", describeDir(c.Dir))
	}
	rebuild.Store = store
	if err := work.RemoveAll(measuredCopyName); err != nil {
		return rebuild, fmt.Errorf("remove the copy of the tracker's store an earlier rebuild left in %s: %w", work.Path(), err)
	}
	if err := work.RemoveAll(rebuildFreshName); err != nil {
		return rebuild, fmt.Errorf("remove the store an earlier rebuild left in %s: %w", work.Path(), err)
	}
	defer func() {
		err = errors.Join(err, work.RemoveAll(measuredCopyName), work.RemoveAll(rebuildFreshName), work.RemoveAll(rebuildExportName))
	}()
	usable, err := c.copyUnderLock(ctx, store, work, &rebuild)
	if err != nil || !usable {
		return rebuild, err
	}
	project := filepath.Join(work.Path(), measuredCopyName)
	copied := c.pinnedTo(project, filepath.Join(project, ".beads"))
	built, err := copied.buildFromExport(ctx, work)
	built.rebuild.Store, built.rebuild.StartedAt = store, rebuild.StartedAt
	built.rebuild.CopyTook, built.rebuild.SizeBefore = rebuild.CopyTook, rebuild.SizeBefore
	return built.rebuild, err
}

// copyUnderLock copies the store into work while holding the store lock
// exclusive, and says whether the copy is one to build from: false, with the
// rebuild saying why, where the lock was not had in time or the store changed
// while it was copied.
func (c Client) copyUnderLock(ctx context.Context, store string, work *repowrite.PinnedRoot, rebuild *StoreRebuild) (bool, error) {
	unlock, err := c.lockStore(ctx, false)
	if waited, ok := lockWaited(err); ok {
		rebuild.Waited = waited
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer unlock()
	before, err := storeFingerprint(store)
	if err != nil {
		return false, err
	}
	if size, err := directorySize(databaseDirectory(store)); err == nil {
		rebuild.SizeBefore = size
	}
	rebuild.StartedAt = time.Now()
	if err := copyStore(store, work, measuredCopyName); err != nil {
		return false, fmt.Errorf("copy the tracker's store %s into %s: %w", store, work.Path(), err)
	}
	rebuild.CopyTook = time.Since(rebuild.StartedAt)
	if c.storeCopied != nil {
		c.storeCopied()
	}
	after, err := storeFingerprint(store)
	if err != nil {
		return false, err
	}
	if moved := fingerprintChange(before, after); moved != "" {
		rebuild.Moved = moved
		return false, nil
	}
	return true, nil
}

// lockWaited is the store lock not had in time, in words, where err is that.
func lockWaited(err error) (string, bool) {
	var busy storeBusy
	if errors.As(err, &busy) {
		return oneline.Fold(busy.Error(), maxMeasureSaid), true
	}
	return "", false
}

// RebuildStore rebuilds the live store as the comment at the top of this file
// says. work is a directory the caller owns, where the new store is built; kept
// is where the old database is kept, under name, and recorded is called with the
// rebuild, its kept copy's place named, before the old database is moved there.
// The two directories have to be on the volume the store is on, since a move
// that would have to copy is refused rather than made.
//
// Where the store is reached through its server, the server is stopped once no
// outside process is found and started again before the lock is let go, so the
// time it was stopped is part of HeldFor.
//
// The store lock not being had in time, or a process outside the harness having
// the store open once it is held, is not an error: nothing is built or moved,
// the rebuild says why and names the process, and the caller records it. A rebuild whose exports differ
// leaves the live store as it was. A rebuild that failed at a move has put the
// old database back, or, where it could not, says so in the error.
func (c Client) RebuildStore(ctx context.Context, work, kept *repowrite.PinnedRoot, name string, recorded func(StoreRebuild) error) (rebuild StoreRebuild, err error) {
	if recorded == nil {
		return rebuild, errors.New("a rebuild of the tracker's store needs somewhere to record its kept copy")
	}
	store := storeDirectory(c.Dir, c.beadsDirVariable())
	if store == "" {
		return rebuild, fmt.Errorf("no tracker store was found from %s", describeDir(c.Dir))
	}
	rebuild.Store = store
	database := databaseDirectory(store)
	if err := work.RemoveAll(rebuildFreshName); err != nil {
		return rebuild, fmt.Errorf("remove the store an earlier rebuild left in %s: %w", work.Path(), err)
	}
	defer func() {
		err = errors.Join(err, work.RemoveAll(rebuildFreshName), work.RemoveAll(rebuildExportName))
	}()
	unlock, err := c.lockStore(ctx, false)
	if waited, ok := lockWaited(err); ok {
		rebuild.Waited = waited
		return rebuild, nil
	}
	if err != nil {
		return rebuild, err
	}
	held := time.Now()
	defer func() {
		unlock()
		rebuild.HeldFor = time.Since(held)
	}()
	// Looked for only now: with the lock held, every bd call the harness makes
	// is waiting behind it, so whoever still has the database open is outside
	// the harness, and moving the database away under it would lose its write.
	if rebuild.Busy, rebuild.Unknown = c.storeHolders(ctx, database); len(rebuild.Busy) > 0 || rebuild.Unknown != "" {
		return rebuild, nil
	}
	if c.ServerSwitch != nil {
		// Started again before the lock is let go, whichever way this ends, and
		// whatever has become of the caller by then; but never after a stop that
		// left the server's process running.
		stopped, err := c.ServerSwitch.StopHeld(ctx, "the store is being rebuilt")
		if stopped {
			defer func() {
				if err := c.ServerSwitch.StartHeld(context.WithoutCancel(ctx)); err != nil {
					rebuild.ServerDown = oneline.Fold(err.Error(), maxMeasureSaid)
				}
			}()
		}
		if err != nil {
			rebuild.ServerKept = oneline.Fold(err.Error(), maxMeasureSaid)
			return rebuild, nil
		}
	}
	rebuild.StartedAt = held
	if size, err := directorySize(database); err == nil {
		rebuild.SizeBefore = size
	}
	locked := c
	locked.storeLocked = true
	built, err := locked.buildFromExport(ctx, work)
	built.rebuild.Store, built.rebuild.StartedAt, built.rebuild.SizeBefore = store, held, rebuild.SizeBefore
	rebuild = built.rebuild
	if err != nil || !rebuild.Passed() {
		return rebuild, err
	}
	return swapDatabases(store, database, work, built.database, kept, name, rebuild, recorded)
}

// swapDatabases moves the old database into kept, recording it first, and the
// new one into its place, and puts the old one back if the second move fails.
func swapDatabases(store, database string, work *repowrite.PinnedRoot, fresh string, kept *repowrite.PinnedRoot, name string, rebuild StoreRebuild, recorded func(StoreRebuild) error) (StoreRebuild, error) {
	storeRoot, err := repowrite.NewRoot(store)
	if err != nil {
		return failedAt(rebuild, RebuildMove, fmt.Errorf("confine moves to %s: %w", store, err))
	}
	keptRoot, err := repowrite.NewRoot(kept.Path())
	if err != nil {
		return failedAt(rebuild, RebuildMove, fmt.Errorf("confine moves to %s: %w", kept.Path(), err))
	}
	workRoot, err := repowrite.NewRoot(work.Path())
	if err != nil {
		return failedAt(rebuild, RebuildMove, fmt.Errorf("confine moves to %s: %w", work.Path(), err))
	}
	base := filepath.Base(database)
	freshRelative, err := filepath.Rel(work.Path(), fresh)
	if err != nil {
		return failedAt(rebuild, RebuildMove, err)
	}
	rebuild.Kept, rebuild.KeptAt = filepath.Join(kept.Path(), name), time.Now()
	if err := recorded(rebuild); err != nil {
		rebuild.Kept = ""
		return failedAt(rebuild, RebuildMove, fmt.Errorf("the old store's place could not be recorded, so the store was not replaced: %w", err))
	}
	if _, err := repowrite.Move(storeRoot, base, keptRoot, name, 0o700); err != nil {
		rebuild.Kept = ""
		return failedAt(rebuild, RebuildMove, fmt.Errorf("the old store could not be moved aside, so the store was not replaced: %w", err))
	}
	if _, err := repowrite.Move(workRoot, freshRelative, storeRoot, base, 0o700); err != nil {
		if _, back := repowrite.Move(keptRoot, name, storeRoot, base, 0o700); back != nil {
			rebuild.Failed = oneline.Fold(fmt.Sprintf("the new store could not be moved into place (%v) and the old one could not be put back from %s: %v", err, rebuild.Kept, back), maxMeasureSaid)
			rebuild.Step = RebuildMove
			return rebuild, fmt.Errorf("the tracker's store is missing: %s", rebuild.Failed)
		}
		rebuild.Restored, rebuild.Kept = true, ""
		return failedAt(rebuild, RebuildMove, fmt.Errorf("the new store could not be moved into place, so the old one was put back: %w", err))
	}
	rebuild.Swapped = true
	if size, err := directorySize(database); err == nil {
		rebuild.SizeAfter = size
	}
	return rebuild, nil
}

func failedAt(rebuild StoreRebuild, step string, err error) (StoreRebuild, error) {
	rebuild.Step = step
	rebuild.Failed = oneline.Fold(err.Error(), maxMeasureSaid)
	return rebuild, err
}

// pinnedTo is a client over a copy of the store: the same bd, run in dir with
// BEADS_DIR naming beads, and no listing or call logs of the live store's.
func (c Client) pinnedTo(dir, beads string) Client {
	return Client{
		Runner:      c.Runner,
		Binary:      c.Binary,
		Dir:         dir,
		Timeout:     measureCallLimit,
		Holders:     c.Holders,
		WriterZone:  c.WriterZone,
		beadsDir:    beads,
		storeLocked: true,
	}
}

// buildFromExport takes a full export of the store this client is pinned to,
// builds a new store from it in work, takes the new store's export and compares
// the two. The build's own failure is the rebuild's Failed, and the error is
// returned with it, so a caller that wants only the verdict reads the rebuild.
// A step that fails or finds a difference stops the build there.
func (c Client) buildFromExport(ctx context.Context, work *repowrite.PinnedRoot) (built builtStore, err error) {
	started := time.Now()
	defer func() { built.rebuild.Took = time.Since(started) }()
	fail := func(step string, err error) (builtStore, error) {
		built.rebuild.Step, built.rebuild.Failed = step, oneline.Fold(err.Error(), maxMeasureSaid)
		return built, err
	}
	data, err := c.runWithin(ctx, measureCallLimit, "export", "--all")
	if err != nil && refusedFlag(err, "--all") {
		data, err = c.runWithin(ctx, measureCallLimit, "export")
	}
	if err != nil {
		return fail(RebuildExport, err)
	}
	before, err := exportRecords(data)
	if err != nil {
		return fail(RebuildExport, err)
	}
	built.rebuild.Records = len(before)
	if len(before) == 0 {
		return fail(RebuildExport, errors.New("the export held no records, so there is nothing to build a store from"))
	}
	prefix, err := issuePrefix(before)
	if err != nil {
		return fail(RebuildExport, err)
	}
	if err := work.WriteFile(rebuildExportName, data, 0o600, false); err != nil {
		return fail(RebuildBuild, fmt.Errorf("keep the export in %s: %w", work.Path(), err))
	}
	project, err := work.CreateDirectory(rebuildFreshName)
	if err != nil {
		return fail(RebuildBuild, fmt.Errorf("make the directory for the new store in %s: %w", work.Path(), err))
	}
	// The new store carries the old one's project identity, which every write
	// checks against the database's: bd keeps the identity it finds in the
	// project file when it makes a store, so the file is written first.
	identity, err := os.ReadFile(filepath.Join(storeDirectory(c.Dir, c.beadsDirVariable()), "metadata.json"))
	if err == nil {
		err = project.MakeDirectory(".beads", 0o700)
	}
	if err == nil {
		err = project.WriteFile(".beads/metadata.json", identity, 0o600, true)
	}
	_ = project.Close()
	if err != nil {
		return fail(RebuildBuild, fmt.Errorf("carry the store's project identity into the new store: %w", err))
	}
	freshProject := filepath.Join(work.Path(), rebuildFreshName)
	fresh := c.pinnedTo(freshProject, filepath.Join(freshProject, ".beads"))
	exportFile := filepath.Join(work.Path(), rebuildExportName)
	for _, command := range [][]string{
		{"init", "--prefix", prefix, "--non-interactive", "--skip-hooks", "--skip-agents"},
		{"import", exportFile},
		{"gc", "--skip-decay", "--force"},
	} {
		if _, err := fresh.runWithin(ctx, measureCallLimit, command...); err != nil {
			return fail(RebuildBuild, fmt.Errorf("bd %s: %w", command[0], err))
		}
	}
	built.database = databaseDirectory(fresh.beadsDir)
	if size, err := directorySize(built.database); err == nil {
		built.rebuild.SizeAfter = size
	}
	records, export := fresh.measuredExport(ctx)
	if export.Failed != "" {
		return fail(RebuildCompare, errors.New(export.Failed))
	}
	export = compareExports(before, records, export)
	built.rebuild.Differing = export.Differing
	built.rebuild.Missing, built.rebuild.Added, built.rebuild.Changed = export.Missing, export.Added, export.Changed
	if export.Differing > 0 {
		built.rebuild.Step = RebuildCompare
	}
	return built, nil
}

// issuePrefix is the prefix every record's id shares, the text before the first
// hyphen, which the new store is made with so its ids are the old store's.
func issuePrefix(records map[string]string) (string, error) {
	prefix := ""
	for key := range records {
		if strings.HasPrefix(key, unidentifiedPrefix) {
			continue
		}
		head, _, found := strings.Cut(key, "-")
		if !found || head == "" {
			return "", fmt.Errorf("the record %q has no prefix before a hyphen, so the new store's prefix cannot be told", key)
		}
		if prefix != "" && head != prefix {
			return "", fmt.Errorf("the records carry more than one prefix (%q and %q), so the new store's prefix cannot be told", prefix, head)
		}
		prefix = head
	}
	if prefix == "" {
		return "", errors.New("no record has an id, so the new store's prefix cannot be told")
	}
	if !issueIDPattern.MatchString(prefix) {
		return "", fmt.Errorf("the prefix %q is not one the tracker accepts", prefix)
	}
	return prefix, nil
}

// storeHolders is who other than this process and the tracker's server has the
// store open, and why
// that could not be told where it could not.
func (c Client) storeHolders(ctx context.Context, database string) ([]StoreHolder, string) {
	lookup, cancel := context.WithTimeout(ctx, holderLookupTimeout)
	defer cancel()
	holders, err := c.holdersFinder().Holders(lookup, database)
	if err != nil {
		return nil, oneline.Fold(err.Error(), maxMeasureSaid)
	}
	// This process never opens the database itself, since every call is a bd of
	// its own, so it is not counted where a finder names it.
	others := holders[:0]
	for _, holder := range c.withoutServer(holders) {
		if holder.PID != os.Getpid() {
			others = append(others, holder)
		}
	}
	return others, ""
}
