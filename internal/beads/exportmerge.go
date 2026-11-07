package beads

// Merging two machines' copies of the tracker's committed export, item by item
// (docs/designs/working-alongside-other-contributors.md, section 5).
//
// Beads does not do this. bd 1.1.2 has no merge command and no merge driver:
// `bd init` writes no .gitattributes entry and no merge configuration, so Git
// merges .beads/issues.jsonl line by line like any text file. Its export is
// sorted newest item first, one item to a line, so two machines changing
// neighbouring items, or each adding an item, conflict though nothing about the
// items does. Nothing brings a merged export into the store either: the
// post-merge hook does not import, and `bd import` is asked for by hand. Import
// itself keeps whichever row has the later updated_at and the local row on a
// tie, so importing the other machine's row overwrites this machine's edit
// without saying so, and refuses a file holding Git's conflict markers. Its own
// documentation says cross-machine sync is meant to go through Dolt remotes, not
// this file.
//
// So the harness merges by item and by field against what it last read from the
// shared export. A change only one side made is taken. Two changes to different
// fields of one item are combined. Appended notes, set-like lists and metadata
// keys combine by their own rules. What is left, two different values for one
// field, or one side editing an item the other removed, is reported as a
// conflict and neither row is touched.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// exportDerived names fields that say nothing either machine decided: when the
// row last moved, and counts bd works out from other rows. A difference in one
// of them is never a difference between two versions of an item.
var exportDerived = map[string]bool{
	"updated_at":       true,
	"dependency_count": true,
	"dependent_count":  true,
	"comment_count":    true,
}

// exportMoments are set by the harness or bd as a status changes. Two machines
// that agree on the status and differ on the moment have not disagreed about
// the item, so the later moment stands. Where the statuses differ, the status
// itself is the conflict.
var exportMoments = map[string]bool{"started_at": true, "closed_at": true}

// exportSets are lists whose order means nothing to the tracker. Each machine's
// additions and removals both apply, as `bd import` itself merges them.
var exportSets = map[string]bool{"labels": true, "comments": true, "dependencies": true}

// ExportMergeInput is what one merge compares.
type ExportMergeInput struct {
	// Base is what this machine last took from the shared export. HasBase is
	// false on the first merge a machine ever makes, where there is no such
	// record: an item differing then is read as the newer side having changed
	// it, Beads' own rule, because nothing can say otherwise.
	Base    []byte
	HasBase bool
	// ThisMachine is the export of this machine's store, and Shared the export
	// committed on the shared target branch.
	ThisMachine []byte
	Shared      []byte
	// Settled replaces an item's base row before the item is compared: the
	// row of the version the development manager decided against, or empty where
	// that version has no row. Only the kept version then reads as changed, so
	// it stands without either having been edited here.
	Settled map[string]string
}

// ExportFieldDifference is one field two versions of an item cannot both keep.
type ExportFieldDifference struct {
	Field       string `json:"field"`
	ThisMachine string `json:"this_machine"`
	Shared      string `json:"shared"`
}

// ExportConflict is one item whose two versions cannot be merged. The rows are
// the whole lines as each side has them, and empty where that side removed the
// item.
type ExportConflict struct {
	ID                   string                  `json:"id"`
	Title                string                  `json:"title,omitempty"`
	ThisMachine          string                  `json:"this_machine,omitempty"`
	Shared               string                  `json:"shared,omitempty"`
	ThisMachineChangedAt string                  `json:"this_machine_changed_at,omitempty"`
	SharedChangedAt      string                  `json:"shared_changed_at,omitempty"`
	Differences          []ExportFieldDifference `json:"differences"`
}

// ExportMerge is the answer to one merge.
type ExportMerge struct {
	// Import is the rows to write into this machine's store, ordered by item:
	// the shared version where only it changed, and the combined one where both
	// did. Each is later than this machine's row, so `bd import` takes it and a
	// write landing while it runs still wins.
	Import [][]byte
	// FromShared and Combined name the items in Import, by which way they got
	// there.
	FromShared []string
	Combined   []string
	// Conflicts is the items neither version of which was touched.
	Conflicts []ExportConflict

	base    map[string]string
	shared  map[string]string
	removed map[string]string
}

// Base is what the next merge compares against: the shared export as read, since
// everything this merge took from it is now common ground, with each conflict's
// own earlier row kept so it goes on reading as a conflict. A row the shared
// export removed and this machine still holds unchanged keeps its row too, so it
// is not read later as something this machine added.
//
// keepEarlier names items whose import did not land, because a write on this
// machine beat it: they keep their earlier row too, so the next merge reads the
// shared version as still to be taken rather than as already common.
func (m ExportMerge) Base(keepEarlier ...string) []byte {
	rows := maps.Clone(m.shared)
	maps.Copy(rows, m.removed)
	for _, conflict := range m.Conflicts {
		setOrRemove(rows, conflict.ID, m.base[conflict.ID])
	}
	for _, id := range keepEarlier {
		setOrRemove(rows, id, m.base[id])
	}
	var encoded bytes.Buffer
	for _, id := range slices.Sorted(maps.Keys(rows)) {
		encoded.WriteString(rows[id])
		encoded.WriteByte('\n')
	}
	return encoded.Bytes()
}

func setOrRemove(rows map[string]string, id, row string) {
	if row == "" {
		delete(rows, id)
		return
	}
	rows[id] = row
}

// MergeExports merges two copies of the export against what they last agreed
// on. A copy holding anything but complete work items, such as Git's conflict
// markers, is an error and nothing is merged.
func MergeExports(input ExportMergeInput) (ExportMerge, error) {
	ours, err := parseExportRows(input.ThisMachine)
	if err != nil {
		return ExportMerge{}, fmt.Errorf("this machine's export: %w", err)
	}
	theirs, err := parseExportRows(input.Shared)
	if err != nil {
		return ExportMerge{}, fmt.Errorf("the shared export: %w", err)
	}
	base, err := parseExportRows(input.Base)
	if err != nil {
		return ExportMerge{}, fmt.Errorf("the export last read: %w", err)
	}
	for id, row := range input.Settled {
		parsed, err := parseExportRow(row)
		if err != nil {
			return ExportMerge{}, fmt.Errorf("the version settled for %s: %w", id, err)
		}
		if parsed == nil {
			delete(base, id)
			continue
		}
		base[id] = parsed
	}

	merge := ExportMerge{base: map[string]string{}, shared: map[string]string{}, removed: map[string]string{}}
	for id, row := range base {
		merge.base[id] = row.line
	}
	for id, row := range theirs {
		merge.shared[id] = row.line
	}
	ids := map[string]bool{}
	for _, rows := range []map[string]*exportRow{ours, theirs, base} {
		for id := range rows {
			ids[id] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		o, t, b := ours[id], theirs[id], base[id]
		if !input.HasBase && b == nil && o != nil && t != nil {
			b = olderOf(o, t)
		}
		merge.mergeItem(id, b, o, t)
	}
	return merge, nil
}

func (m *ExportMerge) mergeItem(id string, b, o, t *exportRow) {
	switch {
	case sameExportRow(o, t):
		// Both sides hold the same version. Which of them moved does not matter.
	case sameExportRow(t, b):
		// Only this machine changed it, added it, or removed it, and its version
		// stands. Nothing here pushes that version anywhere.
	case sameExportRow(o, b):
		// Only the shared version changed.
		switch {
		case t == nil:
			// Removed from the shared export and unchanged here. `bd import` cannot
			// remove a row, and removing one is never this merge's to do, so the
			// row stays in this store and the base remembers it.
			if b != nil {
				m.removed[id] = b.line
			}
		default:
			m.Import = append(m.Import, t.laterThan(o))
			m.FromShared = append(m.FromShared, id)
		}
	case o == nil || t == nil:
		m.Conflicts = append(m.Conflicts, removalConflict(id, o, t))
	default:
		combined, differences := combineExportRows(b, o, t)
		if len(differences) > 0 {
			m.Conflicts = append(m.Conflicts, editConflict(id, o, t, differences))
			return
		}
		m.Import = append(m.Import, combined.laterThan(o))
		m.Combined = append(m.Combined, id)
	}
}

// combineExportRows merges two rows both changed, field by field, and names the
// fields it could not.
func combineExportRows(b, o, t *exportRow) (*exportRow, []ExportFieldDifference) {
	fields := map[string]json.RawMessage{}
	var differences []ExportFieldDifference
	names := map[string]bool{}
	for name := range o.fields {
		names[name] = true
	}
	for name := range t.fields {
		names[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		ov, tv := o.fields[name], t.fields[name]
		var bv json.RawMessage
		if b != nil {
			bv = b.fields[name]
		}
		merged, conflicted := combineExportField(name, bv, ov, tv)
		if conflicted != nil {
			differences = append(differences, conflicted...)
			continue
		}
		if !exportAbsent(merged) {
			fields[name] = merged
		}
	}
	if len(differences) > 0 {
		return nil, differences
	}
	return newExportRow(fields), nil
}

func combineExportField(name string, bv, ov, tv json.RawMessage) (json.RawMessage, []ExportFieldDifference) {
	switch {
	case name == "updated_at":
		return laterMoment(ov, tv), nil
	case exportDerived[name]:
		return ov, nil
	case sameExportValue(ov, tv), sameExportValue(tv, bv):
		return ov, nil
	case sameExportValue(ov, bv):
		return tv, nil
	case exportMoments[name]:
		return laterMoment(ov, tv), nil
	case name == "notes":
		if combined, ok := combineNotes(bv, ov, tv); ok {
			return combined, nil
		}
	case exportSets[name]:
		if combined, ok := combineSets(bv, ov, tv); ok {
			return combined, nil
		}
	case name == "metadata":
		return combineMetadata(bv, ov, tv)
	}
	return nil, []ExportFieldDifference{{Field: name, ThisMachine: readableExportValue(ov), Shared: readableExportValue(tv)}}
}

// combineNotes keeps both machines' appended notes. Notes are append-only by
// rule, so a version that does not begin with the earlier notes rewrote them,
// and that is not something to combine.
func combineNotes(bv, ov, tv json.RawMessage) (json.RawMessage, bool) {
	base, ours, theirs := exportString(bv), exportString(ov), exportString(tv)
	if !strings.HasPrefix(ours, base) || !strings.HasPrefix(theirs, base) {
		return nil, false
	}
	ourAddition := strings.TrimLeft(strings.TrimPrefix(ours, base), "\n")
	theirAddition := strings.TrimLeft(strings.TrimPrefix(theirs, base), "\n")
	combined := ours
	if theirAddition != ourAddition {
		combined = strings.TrimRight(ours, "\n") + "\n" + theirAddition
	}
	encoded, err := json.Marshal(combined)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// combineSets applies each machine's additions and removals. Elements are
// compared whole.
func combineSets(bv, ov, tv json.RawMessage) (json.RawMessage, bool) {
	base, ok1 := exportElements(bv)
	ours, ok2 := exportElements(ov)
	theirs, ok3 := exportElements(tv)
	if !ok1 || !ok2 || !ok3 {
		return nil, false
	}
	was := map[string]bool{}
	for _, element := range base {
		was[canonicalExportValue(element)] = true
	}
	inTheirs := map[string]bool{}
	for _, element := range theirs {
		inTheirs[canonicalExportValue(element)] = true
	}
	inOurs := map[string]bool{}
	combined := []json.RawMessage{}
	for _, element := range ours {
		key := canonicalExportValue(element)
		inOurs[key] = true
		if was[key] && !inTheirs[key] {
			continue
		}
		combined = append(combined, element)
	}
	for _, element := range theirs {
		key := canonicalExportValue(element)
		if !inOurs[key] && !was[key] {
			combined = append(combined, element)
		}
	}
	if len(combined) == 0 {
		return nil, true
	}
	encoded, err := json.Marshal(combined)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// combineMetadata merges metadata key by key, because the harness keeps
// unrelated facts in it, parking among them.
func combineMetadata(bv, ov, tv json.RawMessage) (json.RawMessage, []ExportFieldDifference) {
	base, ours, theirs := exportObject(bv), exportObject(ov), exportObject(tv)
	if ours == nil || theirs == nil || base == nil {
		return nil, []ExportFieldDifference{{Field: "metadata", ThisMachine: readableExportValue(ov), Shared: readableExportValue(tv)}}
	}
	keys := map[string]bool{}
	for _, object := range []map[string]json.RawMessage{ours, theirs} {
		for key := range object {
			keys[key] = true
		}
	}
	combined := map[string]json.RawMessage{}
	var differences []ExportFieldDifference
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		value, o, t, b := json.RawMessage(nil), ours[key], theirs[key], base[key]
		switch {
		case sameExportValue(o, t), sameExportValue(t, b):
			value = o
		case sameExportValue(o, b):
			value = t
		default:
			differences = append(differences, ExportFieldDifference{Field: "metadata " + key, ThisMachine: readableExportValue(o), Shared: readableExportValue(t)})
			continue
		}
		if !exportAbsent(value) {
			combined[key] = value
		}
	}
	if len(differences) > 0 {
		return nil, differences
	}
	encoded, err := json.Marshal(combined)
	if err != nil {
		return nil, []ExportFieldDifference{{Field: "metadata", ThisMachine: readableExportValue(ov), Shared: readableExportValue(tv)}}
	}
	return encoded, nil
}

func removalConflict(id string, o, t *exportRow) ExportConflict {
	conflict := ExportConflict{ID: id}
	switch {
	case o != nil:
		conflict.Title = o.title()
		conflict.ThisMachine = o.line
		conflict.ThisMachineChangedAt = o.changedAt()
		conflict.Differences = []ExportFieldDifference{{Field: "whether the item exists", ThisMachine: "edited", Shared: "removed"}}
	case t != nil:
		conflict.Title = t.title()
		conflict.Shared = t.line
		conflict.SharedChangedAt = t.changedAt()
		conflict.Differences = []ExportFieldDifference{{Field: "whether the item exists", ThisMachine: "removed", Shared: "edited"}}
	}
	return conflict
}

func editConflict(id string, o, t *exportRow, differences []ExportFieldDifference) ExportConflict {
	return ExportConflict{
		ID:                   id,
		Title:                o.title(),
		ThisMachine:          o.line,
		Shared:               t.line,
		ThisMachineChangedAt: o.changedAt(),
		SharedChangedAt:      t.changedAt(),
		Differences:          differences,
	}
}

// exportRow is one line of an export, kept as written and read by field.
type exportRow struct {
	line   string
	fields map[string]json.RawMessage
}

func newExportRow(fields map[string]json.RawMessage) *exportRow {
	encoded, err := json.Marshal(fields)
	if err != nil {
		// Every value here came out of a decoder, so it encodes.
		panic(fmt.Sprintf("encode an export row: %v", err))
	}
	return &exportRow{line: string(encoded), fields: fields}
}

func parseExportRows(data []byte) (map[string]*exportRow, error) {
	rows := map[string]*exportRow{}
	for number, line := range bytes.Split(data, []byte{'\n'}) {
		row, err := parseExportRow(string(line))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", number+1, err)
		}
		if row != nil {
			rows[row.id()] = row
		}
	}
	return rows, nil
}

// parseExportRow reads one line, and nil for a blank one.
func parseExportRow(line string) (*exportRow, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return nil, fmt.Errorf("not a complete work item: %w", err)
	}
	row := &exportRow{line: line, fields: fields}
	if row.id() == "" {
		return nil, fmt.Errorf("a work item with no id")
	}
	return row, nil
}

func (r *exportRow) id() string        { return exportString(r.fields["id"]) }
func (r *exportRow) title() string     { return exportString(r.fields["title"]) }
func (r *exportRow) changedAt() string { return exportString(r.fields["updated_at"]) }

// laterThan is this row as it is written into a store holding other: its
// updated_at is moved past other's where it is not already later, because bd
// keeps the row it has on anything else, including a tie and a machine whose
// clock runs behind.
func (r *exportRow) laterThan(other *exportRow) []byte {
	if other == nil {
		return []byte(r.line)
	}
	theirs, err1 := time.Parse(time.RFC3339, r.changedAt())
	ours, err2 := time.Parse(time.RFC3339, other.changedAt())
	if err2 != nil || (err1 == nil && theirs.After(ours)) {
		return []byte(r.line)
	}
	moved := maps.Clone(r.fields)
	encoded, err := json.Marshal(ours.Add(time.Second).UTC().Format(time.RFC3339))
	if err != nil {
		return []byte(r.line)
	}
	moved["updated_at"] = encoded
	return []byte(newExportRow(moved).line)
}

// olderOf is the row treated as the earlier version where there is no record of
// one, and nil where the two carry the same moment or a moment nothing can read.
func olderOf(a, b *exportRow) *exportRow {
	first, err1 := time.Parse(time.RFC3339, a.changedAt())
	second, err2 := time.Parse(time.RFC3339, b.changedAt())
	switch {
	case err1 != nil || err2 != nil || first.Equal(second):
		return nil
	case first.Before(second):
		return a
	default:
		return b
	}
}

func sameExportRow(a, b *exportRow) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	names := map[string]bool{}
	for name := range a.fields {
		names[name] = true
	}
	for name := range b.fields {
		names[name] = true
	}
	for name := range names {
		if !exportDerived[name] && !sameExportValue(a.fields[name], b.fields[name]) {
			return false
		}
	}
	return true
}

func sameExportValue(a, b json.RawMessage) bool {
	if exportAbsent(a) || exportAbsent(b) {
		return exportAbsent(a) && exportAbsent(b)
	}
	return canonicalExportValue(a) == canonicalExportValue(b)
}

// exportAbsent treats the forms an export leaves out as the absence they mean.
func exportAbsent(value json.RawMessage) bool {
	switch strings.TrimSpace(string(value)) {
	case "", "null", `""`, "[]", "{}":
		return true
	}
	return false
}

func canonicalExportValue(value json.RawMessage) string {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return string(value)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return string(value)
	}
	return string(encoded)
}

func exportString(value json.RawMessage) string {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return ""
	}
	return text
}

func exportElements(value json.RawMessage) ([]json.RawMessage, bool) {
	if exportAbsent(value) {
		return nil, true
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(value, &elements); err != nil {
		return nil, false
	}
	return elements, true
}

// exportObject is the object in a metadata value, empty where it has none, and
// nil where it holds anything else.
func exportObject(value json.RawMessage) map[string]json.RawMessage {
	if exportAbsent(value) {
		return map[string]json.RawMessage{}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil {
		return nil
	}
	return object
}

func laterMoment(a, b json.RawMessage) json.RawMessage {
	first, err1 := time.Parse(time.RFC3339, exportString(a))
	second, err2 := time.Parse(time.RFC3339, exportString(b))
	switch {
	case err1 != nil:
		return b
	case err2 != nil:
		return a
	case second.After(first):
		return b
	default:
		return a
	}
}

// readableExportValue is a field's value as a person reads it: a string without
// its quotes, and nothing dressed up as a value where the field is empty.
func readableExportValue(value json.RawMessage) string {
	if exportAbsent(value) {
		return "empty"
	}
	if text := exportString(value); text != "" {
		return text
	}
	return string(value)
}
