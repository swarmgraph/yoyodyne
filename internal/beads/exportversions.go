package beads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// exportedVersionsName is where the versions this machine has exported are kept,
// beside the tracker store rather than in any one checkout, because every checkout
// of one store exports from it. It ends in .lock for the reason exportStampName
// does; a separate lock, exportedVersionsLockName, is what queues its writers.
const (
	exportedVersionsName     = "yoyodyne-exported-versions.lock"
	exportedVersionsLockName = "yoyodyne-exported-versions-queue.lock"
)

// maxExportedVersions bounds what is kept for one item. A commit carrying the
// export lands long before an item moves this many more times between exports, and
// a version forgotten costs one hold the development manager settles.
const maxExportedVersions = 32

// ExportedVersions is, for each item, the versions this machine's exports have
// carried. Only an export can be committed, so a shared row among them came from
// this machine, or is identical to one that did (MergeExports). Each version is a
// digest of what the row says, leaving out what exportDerived leaves out.
type ExportedVersions map[string][]string

// ExportedVersionsOf is the record one export of rows leaves.
func ExportedVersionsOf(exported []byte) (ExportedVersions, error) {
	rows, err := parseExportRows(exported)
	if err != nil {
		return nil, err
	}
	versions := ExportedVersions{}
	for id, row := range rows {
		versions[id] = []string{row.digest()}
	}
	return versions, nil
}

func (v ExportedVersions) holds(row *exportRow) bool {
	return slices.Contains(v[row.id()], row.digest())
}

// digest identifies what a row says, whichever machine wrote it out.
func (r *exportRow) digest() string {
	said := map[string]string{}
	for name, value := range r.fields {
		if !exportDerived[name] && !exportAbsent(value) {
			said[name] = canonicalExportValue(value)
		}
	}
	encoded, err := json.Marshal(said)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:16])
}

// ExportedVersions reads what this machine has exported. A record that cannot be
// read answers with nothing: that costs a hold on an item that moved on, which the
// development manager settles, and no merge is worth stopping for it.
func (c Client) ExportedVersions() ExportedVersions {
	if c.Dir == "" {
		return nil
	}
	store := storeDirectory(c.Dir, c.beadsDirVariable())
	if store == "" {
		return nil
	}
	encoded, err := os.ReadFile(filepath.Join(store, exportedVersionsName))
	if err != nil {
		return nil
	}
	var versions ExportedVersions
	if json.Unmarshal(encoded, &versions) != nil {
		return nil
	}
	return versions
}

// recordExportedVersions adds what one published export carried. It is kept only to
// tell this machine's past from another machine's edit, so like the export stamp
// one that cannot be written costs that and nothing else.
func (c Client) recordExportedVersions(ctx context.Context, store string, exported []byte) {
	rows, err := parseExportRows(exported)
	if err != nil {
		return
	}
	root, err := repowrite.OpenPinnedRoot(store)
	if err != nil {
		return
	}
	defer root.Close()
	lock, err := root.OpenLock(exportedVersionsLockName, 0o600)
	if err != nil {
		return
	}
	defer lock.Close()
	waiting, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	if err := lockWriteFile(waiting, lock); err != nil {
		return
	}
	defer unlockWriteFile(lock)
	versions := c.ExportedVersions()
	if versions == nil {
		versions = ExportedVersions{}
	}
	changed := false
	for _, id := range slices.Sorted(maps.Keys(rows)) {
		digest := rows[id].digest()
		if slices.Contains(versions[id], digest) {
			continue
		}
		kept := append(versions[id], digest)
		if len(kept) > maxExportedVersions {
			kept = kept[len(kept)-maxExportedVersions:]
		}
		versions[id] = kept
		changed = true
	}
	if !changed {
		return
	}
	encoded, err := json.Marshal(versions)
	if err != nil {
		return
	}
	_ = root.WriteFile(exportedVersionsName, encoded, 0o600, false)
}
