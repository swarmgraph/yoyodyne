package beads

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// Measuring the tracker's store on a copy of it.
//
// The store grows past what the nightly cleanup takes back, and a listing of
// open work has come to run past its limit with nothing else holding the
// store. Whether a cleanup alone is enough, whether a squash of old history is
// needed, or whether the current data is itself too big are three answers the
// figures below give, and none of them can be had by trying the remedies on
// the live store: a squash cannot be undone, and every call waits behind it.
// So a measurement copies the store and does everything to the copy:
//
//  1. It copies the store directory while no other process has the store open,
//     and keeps the copy only where every file in the store has the same size
//     and modification time after the copy as before it, so the copy is one
//     state of the store rather than a mixture of two. A store holding a
//     symbolic link is not copied at all, since bd run on the copy could write
//     through the link into the live store.
//  2. On the copy as it is, it measures the database directory's size, times a
//     listing of open items, a listing of ready items, and a read of one item,
//     and takes a full export.
//  3. It then runs, one after another on the same copy, the cleanup the
//     maintenance pass runs (`bd gc --skip-decay --force`), a squash of the
//     history older than thirty days (`bd compact --days 30 --force`), and a
//     squash of all history (`bd flatten --force`), and after each takes the
//     same size, the same three timed reads, and another full export, compared
//     record by record with the export before it.
//
// The sizes between the stages are the store's breakdown: what the cleanup
// took back is leftover data no history reaches, what the squash of old
// history took back is that history, what the full squash took back is the
// recent history, and what is left is the current data. The last stage is not
// a remedy anybody has proposed; it is there because its size and timings are
// the current data alone, which is the third answer.
//
// Every bd call is made with the copy's directory as its working directory and
// BEADS_DIR naming the copy, in place of any BEADS_DIR the supervisor
// inherited, so none of them finds the live store. The copy is removed when
// the measurement ends, and one an earlier measurement left behind is removed
// before the next is made.

// The stages of a measurement, in the order they run.
const (
	StageAsItIs        = "as it is"
	StageCleanedUp     = "after the cleanup"
	StageOldSquashed   = "after squashing history older than 30 days"
	StageAllSquashed   = "after squashing all history"
	ReadOpenListing    = "listing open items"
	ReadReadyListing   = "listing ready items"
	ReadOneItem        = "reading one item"
	squashOlderDays    = "30"
	measuredCopyName   = "copy"
	maxMeasureSaid     = 1 << 10
	maxComparedNamed   = 100
	measureCallLimit   = 20 * time.Minute
	unidentifiedPrefix = "a record with no id, "
)

// measuredStages is what makes each stage after the first: the bd command run
// over the copy.
var measuredStages = []struct {
	name string
	args []string
}{
	{StageCleanedUp, []string{"gc", "--skip-decay", "--force"}},
	{StageOldSquashed, []string{"compact", "--days", squashOlderDays, "--force", "--json"}},
	{StageAllSquashed, []string{"flatten", "--force", "--json"}},
}

// StoreMeasurement is one measurement of the tracker's store on a copy of it.
type StoreMeasurement struct {
	// Store is the live store directory that was copied.
	Store string `json:"store"`
	// Busy is the processes that had the store open, where they stopped the copy
	// being made. Nothing was copied or measured.
	Busy []StoreHolder `json:"busy,omitempty"`
	// Moved is what changed in the store while it was copied, where something
	// did. The copy was discarded and nothing was measured.
	Moved string `json:"moved,omitempty"`
	// StartedAt is when the copy began, CopyTook how long it took, and Took how
	// long the whole measurement took, the copy included.
	StartedAt time.Time     `json:"started_at"`
	CopyTook  time.Duration `json:"copy_took,omitempty"`
	Took      time.Duration `json:"took,omitempty"`
	// Stages is each state of the copy measured, in order. A stage whose command
	// failed is the last.
	Stages []StoreStage `json:"stages,omitempty"`
}

// StoreStage is one state of the copy: the command that made it, and what was
// measured of the copy afterwards.
type StoreStage struct {
	Name string `json:"name"`
	// Command is the bd command run over the copy to make this stage, and empty
	// for the copy as it is; Took is how long it ran, Said what it wrote, and
	// Failed why it did not succeed, in which case nothing was measured after it.
	Command []string      `json:"command,omitempty"`
	Took    time.Duration `json:"took,omitempty"`
	Said    string        `json:"said,omitempty"`
	Failed  string        `json:"failed,omitempty"`
	// Size is the copy's database directory in bytes after the command.
	Size  int64       `json:"size,omitempty"`
	Reads []StoreRead `json:"reads,omitempty"`
	// Export is the full export taken after the command.
	Export StoreExport `json:"export"`
}

// StoreRead is one timed read of the copy.
type StoreRead struct {
	Name    string        `json:"name"`
	Command []string      `json:"command"`
	Took    time.Duration `json:"took"`
	// Items is how many work items it answered with.
	Items  int    `json:"items"`
	Failed string `json:"failed,omitempty"`
}

// StoreExport is one full export of the copy, and how it compares with the
// export of the stage before.
type StoreExport struct {
	// Records is how many records the export held, and Failed why it could not
	// be taken or read.
	Records int    `json:"records"`
	Failed  string `json:"failed,omitempty"`
	// Compared says this export was compared with the one before it. Missing is
	// the records that one held and this does not, Added those this holds and it
	// did not, and Changed those both hold with different contents, each named
	// by its id, the first hundred kept.
	Compared bool     `json:"compared,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	Added    []string `json:"added,omitempty"`
	Changed  []string `json:"changed,omitempty"`
	// Differing is how many records differed in all, Missing, Added and Changed
	// together, however many of them are named.
	Differing int `json:"differing,omitempty"`
}

// Unchanged reports an export compared with the one before it and found to
// hold every record, every item and note in it, exactly as that one did.
func (e StoreExport) Unchanged() bool { return e.Compared && e.Failed == "" && e.Differing == 0 }

// Measured reports a measurement whose copy was made and measured at least as
// it is.
func (m StoreMeasurement) Measured() bool { return len(m.Stages) > 0 }

// Stage is the stage named, where the measurement reached it.
func (m StoreMeasurement) Stage(name string) (StoreStage, bool) {
	for _, stage := range m.Stages {
		if stage.Name == name {
			return stage, true
		}
	}
	return StoreStage{}, false
}

// Read is the timed read named.
func (s StoreStage) Read(name string) (StoreRead, bool) {
	for _, read := range s.Reads {
		if read.Name == name {
			return read, true
		}
	}
	return StoreRead{}, false
}

// MeasureStore copies the store this client's bd uses into work and measures
// the copy, as the comment at the top of this file says. work is a directory
// the caller owns, which nothing else writes; the copy is made inside it and
// removed before this returns. The live store is only ever read.
//
// Another process having the store open, or the store changing while it was
// copied, is not an error: nothing is measured, the measurement says why, and
// the next one tries again. A stage whose command fails ends the measurement
// with that stage recorded as failed, and a read or an export that fails is
// recorded as failed and the measurement goes on.
func (c Client) MeasureStore(ctx context.Context, work *repowrite.PinnedRoot) (measurement StoreMeasurement, err error) {
	store := storeDirectory(c.Dir, c.beadsDirVariable())
	if store == "" {
		return measurement, fmt.Errorf("no tracker store was found from %s", describeDir(c.Dir))
	}
	measurement.Store = store
	if err := work.RemoveAll(measuredCopyName); err != nil {
		return measurement, fmt.Errorf("remove the copy of the tracker's store an earlier measurement left in %s: %w", work.Path(), err)
	}
	lookup, cancel := context.WithTimeout(ctx, holderLookupTimeout)
	holders, holdersErr := c.holdersFinder().Holders(lookup, databaseDirectory(store))
	cancel()
	if holdersErr == nil && len(holders) > 0 {
		measurement.Busy = holders
		return measurement, nil
	}
	before, err := storeFingerprint(store)
	if err != nil {
		return measurement, err
	}
	measurement.StartedAt = time.Now()
	defer func() { measurement.Took = time.Since(measurement.StartedAt) }()
	// Removed whatever this comes to, so a copy outlives no measurement but one
	// whose process was killed, and that one is removed by the next.
	defer work.RemoveAll(measuredCopyName)
	if err := copyStore(store, work); err != nil {
		return measurement, fmt.Errorf("copy the tracker's store %s into %s: %w", store, work.Path(), err)
	}
	measurement.CopyTook = time.Since(measurement.StartedAt)
	if c.storeCopied != nil {
		c.storeCopied()
	}
	after, err := storeFingerprint(store)
	if err != nil {
		return measurement, err
	}
	if moved := fingerprintChange(before, after); moved != "" {
		measurement.Moved = moved
		return measurement, nil
	}
	project := filepath.Join(work.Path(), measuredCopyName)
	copied := Client{
		Runner:     c.Runner,
		Binary:     c.Binary,
		Dir:        project,
		Timeout:    measureCallLimit,
		Holders:    c.Holders,
		WriterZone: c.WriterZone,
		beadsDir:   filepath.Join(project, ".beads"),
	}
	database := databaseDirectory(copied.beadsDir)

	first := copied.measureStage(ctx, StoreStage{Name: StageAsItIs}, database, "", nil)
	measurement.Stages = append(measurement.Stages, first.stage)
	previous := first
	for _, next := range measuredStages {
		if ctx.Err() != nil {
			return measurement, ctx.Err()
		}
		stage := StoreStage{Name: next.name, Command: next.args}
		started := time.Now()
		said, runErr := copied.runWithin(ctx, measureCallLimit, next.args...)
		stage.Took = time.Since(started)
		stage.Said = oneline.Fold(strings.TrimSpace(string(said)), maxMeasureSaid)
		if runErr != nil {
			stage.Failed = oneline.Fold(runErr.Error(), maxMeasureSaid)
			if size, err := directorySize(database); err == nil {
				stage.Size = size
			}
			measurement.Stages = append(measurement.Stages, stage)
			return measurement, nil
		}
		measured := copied.measureStage(ctx, stage, database, previous.item, previous.records)
		measurement.Stages = append(measurement.Stages, measured.stage)
		previous = measured
	}
	return measurement, nil
}

// measuredStage is a stage measured, with what the next stage is compared
// against: the item its single read reads, and its export's records.
type measuredStage struct {
	stage   StoreStage
	item    string
	records map[string]string
}

// measureStage measures the copy in the state stage left it: its size, the
// three timed reads, and a full export compared with the one before, where
// there is one. item is the work item the single read reads, and empty for
// the first stage, which picks one from its listing of open items.
func (c Client) measureStage(ctx context.Context, stage StoreStage, database, item string, before map[string]string) measuredStage {
	if size, err := directorySize(database); err == nil {
		stage.Size = size
	} else if stage.Failed == "" {
		stage.Failed = fmt.Sprintf("the copy's size could not be measured: %v", err)
	}
	open := c.timedListing(ctx, ReadOpenListing, "list", []string{"--status=open"})
	stage.Reads = append(stage.Reads, open.read)
	if item == "" && len(open.items) > 0 {
		item = open.items[0].ID
	}
	ready := c.timedListing(ctx, ReadReadyListing, "ready", nil)
	stage.Reads = append(stage.Reads, ready.read)
	if item == "" && len(ready.items) > 0 {
		item = ready.items[0].ID
	}
	stage.Reads = append(stage.Reads, c.timedShow(ctx, item))
	records, export := c.measuredExport(ctx)
	if before != nil && export.Failed == "" {
		export = compareExports(before, records, export)
	}
	stage.Export = export
	return measuredStage{stage: stage, item: item, records: records}
}

type timedListing struct {
	read  StoreRead
	items []WorkItem
}

func (c Client) timedListing(ctx context.Context, name, verb string, filter []string) timedListing {
	read := StoreRead{Name: name, Command: append([]string{verb, "--json", unboundedListing}, filter...)}
	started := time.Now()
	items, err := c.workListing(ctx, verb, filter)
	read.Took = time.Since(started)
	if err != nil {
		read.Failed = oneline.Fold(err.Error(), maxMeasureSaid)
		return timedListing{read: read}
	}
	read.Items = len(items)
	return timedListing{read: read, items: items}
}

func (c Client) timedShow(ctx context.Context, id string) StoreRead {
	read := StoreRead{Name: ReadOneItem, Command: []string{"show", id, "--json"}}
	if id == "" {
		read.Command = []string{"show"}
		read.Failed = "neither listing named an item to read"
		return read
	}
	started := time.Now()
	_, err := c.showOnce(ctx, id)
	read.Took = time.Since(started)
	if err != nil {
		read.Failed = oneline.Fold(err.Error(), maxMeasureSaid)
		return read
	}
	read.Items = 1
	return read
}

// measuredExport is a full export of the copy, every record bd holds included,
// read as each record's canonical form keyed by its id.
func (c Client) measuredExport(ctx context.Context) (map[string]string, StoreExport) {
	data, err := c.runWithin(ctx, measureCallLimit, "export", "--all")
	if err != nil && refusedFlag(err, "--all") {
		data, err = c.runWithin(ctx, measureCallLimit, "export")
	}
	if err != nil {
		return nil, StoreExport{Failed: oneline.Fold(err.Error(), maxMeasureSaid)}
	}
	records, err := exportRecords(data)
	if err != nil {
		return nil, StoreExport{Failed: oneline.Fold(err.Error(), maxMeasureSaid)}
	}
	return records, StoreExport{Records: len(records)}
}

// exportRecords reads an export, one JSON record a line, into each record's
// canonical form — its keys in order, its numbers as written — keyed by its
// id. A record with no id is keyed by its contents, so it is either present in
// both exports or reported missing from one and added to the other.
func exportRecords(data []byte) (map[string]string, error) {
	records := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1<<20), maxBDOutputBytes)
	for line := 1; scanner.Scan(); line++ {
		text := bytes.TrimSpace(scanner.Bytes())
		if len(text) == 0 {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(text))
		decoder.UseNumber()
		var record any
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("the export's line %d is not a JSON record: %w", line, err)
		}
		canonical, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		key := ""
		if object, ok := record.(map[string]any); ok {
			key, _ = object["id"].(string)
		}
		if key == "" {
			sum := sha256.Sum256(canonical)
			key = unidentifiedPrefix + hex.EncodeToString(sum[:6])
		}
		if _, twice := records[key]; twice {
			return nil, fmt.Errorf("the export holds %s twice", key)
		}
		records[key] = string(canonical)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the export: %w", err)
	}
	return records, nil
}

func compareExports(before, after map[string]string, export StoreExport) StoreExport {
	export.Compared = true
	var missing, added, changed []string
	for key, was := range before {
		now, found := after[key]
		switch {
		case !found:
			missing = append(missing, key)
		case now != was:
			changed = append(changed, key)
		}
	}
	for key := range after {
		if _, found := before[key]; !found {
			added = append(added, key)
		}
	}
	export.Differing = len(missing) + len(added) + len(changed)
	export.Missing, export.Added, export.Changed = named(missing), named(added), named(changed)
	return export
}

func named(keys []string) []string {
	sort.Strings(keys)
	if len(keys) > maxComparedNamed {
		keys = keys[:maxComparedNamed]
	}
	return keys
}

// storeFile is what a fingerprint keeps of one file: enough to tell that it
// was written to between two looks.
type storeFile struct {
	size     int64
	modified time.Time
}

// storeFingerprint is every regular file under the store with its size and
// modification time. A symbolic link anywhere under it is refused, because bd
// run on a copy that carried it could write through it into the live store.
func storeFingerprint(store string) (map[string]storeFile, error) {
	files := map[string]storeFile{}
	err := filepath.WalkDir(store, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(store, path)
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("the tracker's store holds a symbolic link, %s, so it is not copied: bd run on the copy could write through it into the live store", relative)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files[relative] = storeFile{size: info.Size(), modified: info.ModTime()}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("look over the tracker's store %s: %w", store, err)
	}
	return files, nil
}

// fingerprintChange says what differs between two fingerprints of the store,
// and is empty where nothing does.
func fingerprintChange(before, after map[string]storeFile) string {
	var changed []string
	for name, was := range before {
		now, found := after[name]
		switch {
		case !found:
			changed = append(changed, name+" was removed")
		case now.size != was.size || !now.modified.Equal(was.modified):
			changed = append(changed, name+" was written to")
		}
	}
	for name := range after {
		if _, found := before[name]; !found {
			changed = append(changed, name+" was created")
		}
	}
	if len(changed) == 0 {
		return ""
	}
	sort.Strings(changed)
	said := strings.Join(changed[:min(len(changed), maxHoldersSaid)], ", ")
	if len(changed) > maxHoldersSaid {
		said += fmt.Sprintf(" and %d more", len(changed)-maxHoldersSaid)
	}
	return "another process wrote to the store while it was copied: " + said
}

// copyStore copies the store directory into work as a project directory of its
// own, copy/.beads, every directory and regular file in it. Anything else — a
// socket a running bd left — is not part of the store's data and is left out.
func copyStore(store string, work *repowrite.PinnedRoot) error {
	project, err := work.CreateDirectory(measuredCopyName)
	if err != nil {
		return err
	}
	defer project.Close()
	return filepath.WalkDir(store, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(store, path)
		if err != nil {
			return err
		}
		target := filepath.ToSlash(filepath.Join(".beads", relative))
		switch {
		case entry.IsDir():
			return project.MakeDirectory(target, 0o700)
		case entry.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symbolic link", relative)
		case !entry.Type().IsRegular():
			return nil
		}
		return copyStoreFile(path, project, target)
	})
}

func copyStoreFile(path string, project *repowrite.PinnedRoot, target string) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := project.FileWriter(target, 0o600, true)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	return errors.Join(copyErr, destination.Close())
}
