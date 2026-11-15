package beads

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// maxExportReadBytes bounds how much of the export ReadExport reads. It is far
// above this project's own export, and a read that reaches it fails rather than
// handing a reader the older half of the backlog as though it were the whole.
const maxExportReadBytes = 256 << 20

// ReadExport reads the work items holding any of the statuses given out of the
// tracker's export in a repository, ExportPath, in the export's order, decoded
// exactly as a listing is.
//
// It is for a reader that cannot open the tracker: a developer run, whose
// sandbox refuses the store's lock to every bd command, reads the copy the
// harness puts in its worktree when it makes it. That copy is as current as the
// moment the worktree was made, and nothing later; a reader that can open the
// tracker reads the tracker. A line that is not a work item, such as a record
// of another type the tracker exports beside its items, is passed over. An
// export that is missing, or that does not decode, is an error rather than an
// empty backlog, because the two lead to opposite conclusions.
func ReadExport(repository string, statuses []string) ([]WorkItem, error) {
	path := filepath.Join(repository, ExportPath)
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read the tracker's export: %w", err)
	}
	defer file.Close()

	limited := &io.LimitedReader{R: file, N: maxExportReadBytes + 1}
	decoder := json.NewDecoder(limited)
	var items []WorkItem
	for line := 1; ; line++ {
		var raw struct {
			rawWorkItem
			Type string `json:"_type"`
		}
		if err := decoder.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if limited.N <= 0 {
				return nil, fmt.Errorf("the tracker's export %s is larger than the %d bytes read of it", path, maxExportReadBytes)
			}
			return nil, fmt.Errorf("decode record %d of the tracker's export %s: %w", line, path, err)
		}
		if raw.Type != "" && raw.Type != "issue" {
			continue
		}
		if len(statuses) != 0 && !slices.Contains(statuses, raw.Status) {
			continue
		}
		item, err := convertWorkItem(raw.rawWorkItem)
		if err != nil {
			return nil, fmt.Errorf("record %d of the tracker's export %s: %w", line, path, err)
		}
		items = append(items, item)
	}
	if limited.N <= 0 {
		return nil, fmt.Errorf("the tracker's export %s is larger than the %d bytes read of it", path, maxExportReadBytes)
	}
	return items, nil
}
