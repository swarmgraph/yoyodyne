package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// ExportPath is the passive snapshot a run reads, never the tracker itself.
const ExportPath = ".beads/issues.jsonl"

const exportRefreshInterval = time.Minute

// ErrExportBusy reports an export that never started because another export
// held the lock past one call's bound. It answers nothing about the store, as a
// `bd` killed at its bound does not, so a caller that waits out a timed-out
// export waits this out the same way.
var ErrExportBusy = errors.New("another tracker export held the export lock past the bound of one tracker call")

// Raw export bytes must survive the process runner's line splitting and secret
// redaction unchanged, but retaining them is still bounded.
type boundedExportOutput struct{ buffer bytes.Buffer }

func (b *boundedExportOutput) Len() int       { return b.buffer.Len() }
func (b *boundedExportOutput) Bytes() []byte  { return b.buffer.Bytes() }
func (b *boundedExportOutput) String() string { return b.buffer.String() }

func (b *boundedExportOutput) Write(data []byte) (int, error) {
	if b.Len()+len(data) > maxBDOutputBytes {
		return 0, fmt.Errorf("tracker export output exceeds %d bytes", maxBDOutputBytes)
	}
	return b.buffer.Write(data)
}

// withoutAutoExport keeps the full JSONL rewrite off ordinary harness writes,
// even where a project's bd configuration or the parent environment enables it.
// bd binds export.auto to BD_EXPORT_AUTO, with environment ahead of the file.
func withoutAutoExport(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "BD_EXPORT_AUTO=") {
			result = append(result, entry)
		}
	}
	return append(result, "BD_EXPORT_AUTO=false")
}

// RefreshExport takes a fresh snapshot for a worktree. The expensive export is
// its own read, rather than part of every mutation's exclusive store lock. bd
// writes to stdout; publication happens only after it exits successfully and
// through the confined writer, so a killed export leaves the previous copy whole.
//
// Fresh means begun after this was asked for, not necessarily begun by this
// call: an export that queued behind another which began after it was asked
// for is handed that one's snapshot, which already holds every write finished
// before it asked (exportStamp). Several worktrees made at once then hold the
// tracker's store for one export rather than one each.
func (c Client) RefreshExport(ctx context.Context) error {
	return c.refreshExport(ctx, 0)
}

// RefreshExportIfDue keeps admission triggers and maintenance's passive copy
// current without exporting once per reader or program manager instance.
func (c Client) RefreshExportIfDue(ctx context.Context) error {
	return c.refreshExport(ctx, exportRefreshInterval)
}

func (c Client) refreshExport(ctx context.Context, interval time.Duration) error {
	asked := time.Now()
	if c.Dir == "" || storeDirectory(c.Dir, os.Getenv("BEADS_DIR")) == "" {
		return nil
	}
	declared, err := repowrite.NewRoot(c.Dir)
	if err != nil {
		return err
	}
	root, err := repowrite.OpenPinnedRoot(declared.Path())
	if err != nil {
		return err
	}
	defer root.Close()
	beads, err := root.OpenDirectory(".beads")
	if err != nil {
		return err
	}
	defer beads.Close()
	file, err := beads.OpenLock("yoyodyne-export.lock", 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	waiting, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	if err := lockWriteFile(waiting, file); err != nil {
		if ctx.Err() == nil && waiting.Err() != nil {
			return fmt.Errorf("%w: %w", ErrExportBusy, err)
		}
		return fmt.Errorf("lock the tracker export: %w", err)
	}
	defer unlockWriteFile(file)
	// Checked after queueing, so concurrent readers share the snapshot the first
	// made rather than each paying for another full export.
	info, err := beads.Lstat("issues.jsonl")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if interval > 0 && err == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) < interval {
		return nil
	}
	if err == nil && readExportStamp(beads).covers(info, asked) {
		return nil
	}
	started := time.Now()
	data, err := c.run(ctx, "export")
	if err != nil {
		return err
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var item struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(line, &item); err != nil || item.ID == "" {
			return errors.New("bd export did not answer with complete JSONL work items, so the previous snapshot is kept")
		}
	}
	if err := beads.Unchanged(); err != nil {
		return err
	}
	if err := beads.WriteFile("issues.jsonl", data, 0o644, false); err != nil {
		return err
	}
	// The stamp only lets a later export be skipped, so one that cannot be
	// written costs that export and nothing else.
	if info, err := beads.Lstat("issues.jsonl"); err == nil {
		writeExportStamp(beads, exportStamp{StartedAt: started, ModifiedAt: info.ModTime(), Size: info.Size()})
	}
	return nil
}

// exportStampName is where the stamp of the published snapshot is kept, beside
// the export lock and written only by whoever holds it. It ends in .lock so the
// ignore file bd writes into .beads, which keeps every *.lock out of Git, keeps
// it out too; the lock itself is not written to, because a lock is opened
// read-only so that a lock file with another hard link is never changed.
const exportStampName = "yoyodyne-export-stamp.lock"

// exportStamp is when the published snapshot's export began. It names the
// snapshot it describes by modification time and size, because the snapshot is
// a tracked file something other than an export can replace — a checkout, a
// merge — and a stamp describing a file that has since been replaced says
// nothing about the one there now.
type exportStamp struct {
	StartedAt  time.Time `json:"started_at"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
}

// covers reports a stamp describing the snapshot published now, from an export
// that began no earlier than asked.
func (s exportStamp) covers(published os.FileInfo, asked time.Time) bool {
	return !s.StartedAt.IsZero() && !s.StartedAt.Before(asked) && published.Mode().IsRegular() &&
		published.ModTime().Equal(s.ModifiedAt) && published.Size() == s.Size
}

// readExportStamp is the kept stamp, and the zero stamp, which covers nothing,
// where there is none or it cannot be read.
func readExportStamp(beads *repowrite.PinnedRoot) exportStamp {
	encoded, err := beads.ReadFile(exportStampName)
	if err != nil {
		return exportStamp{}
	}
	var stamp exportStamp
	if json.Unmarshal(encoded, &stamp) != nil {
		return exportStamp{}
	}
	return stamp
}

func writeExportStamp(beads *repowrite.PinnedRoot, stamp exportStamp) {
	encoded, err := json.Marshal(stamp)
	if err != nil {
		return
	}
	_ = beads.WriteFile(exportStampName, encoded, 0o600, false)
}
