package beads

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func exportLine(t *testing.T, fields map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func exportOf(lines ...string) []byte {
	return []byte(strings.Join(lines, "\n") + "\n")
}

func item(id, title, status, updated string) map[string]any {
	return map[string]any{"_type": "issue", "id": id, "title": title, "status": status, "updated_at": updated, "created_at": "2026-11-01T00:00:00Z"}
}

func with(fields map[string]any, key string, value any) map[string]any {
	fields[key] = value
	return fields
}

func importedField(t *testing.T, merge ExportMerge, id, field string) string {
	t.Helper()
	for _, row := range merge.Import {
		parsed, err := parseExportRow(string(row))
		if err != nil {
			t.Fatal(err)
		}
		if parsed.id() == id {
			return exportString(parsed.fields[field])
		}
	}
	t.Fatalf("%s was not imported: %s", id, merge.FromShared)
	return ""
}

const (
	earlier = "2026-11-01T10:00:00Z"
	mine    = "2026-11-02T10:00:00Z"
	yours   = "2026-11-03T10:00:00Z"
)

// Git conflicts on these neighbouring lines, and on the two new items, though
// the items have nothing to do with each other.
func TestTwoMachinesEditingDifferentItemsAreBothKept(t *testing.T) {
	t.Parallel()

	first, second := item("t-a", "a", "open", earlier), item("t-b", "b", "open", earlier)
	base := exportOf(exportLine(t, first), exportLine(t, second))
	merge, err := MergeExports(ExportMergeInput{
		Base: base, HasBase: true,
		ThisMachine: exportOf(
			exportLine(t, item("t-new-here", "added here", "open", mine)),
			exportLine(t, item("t-a", "a edited here", "open", mine)),
			exportLine(t, second)),
		Shared: exportOf(
			exportLine(t, item("t-new-there", "added there", "open", yours)),
			exportLine(t, first),
			exportLine(t, item("t-b", "b edited there", "open", yours))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(merge.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none", merge.Conflicts)
	}
	if want := []string{"t-b", "t-new-there"}; !slices.Equal(merge.FromShared, want) {
		t.Errorf("taken from the shared export = %v, want %v", merge.FromShared, want)
	}
	if got := importedField(t, merge, "t-b", "title"); got != "b edited there" {
		t.Errorf("t-b imported with title %q", got)
	}
	// This machine's own edit stands: nothing is written over it.
	for _, row := range merge.Import {
		if strings.Contains(string(row), `"t-a"`) || strings.Contains(string(row), "added here") {
			t.Errorf("imported this machine's own item: %s", row)
		}
	}
}

// After a merge, the same two exports have nothing more to say, including about
// what this machine combined and the shared export has not caught up with.
func TestAMergeDoesNotTakeTheSameChangeTwice(t *testing.T) {
	t.Parallel()

	base := exportOf(exportLine(t, item("t-a", "a", "open", earlier)))
	shared := exportOf(exportLine(t, item("t-a", "a edited there", "open", yours)))
	ours := exportOf(exportLine(t, with(item("t-a", "a", "in_progress", mine), "assignee", "me")))
	first, err := MergeExports(ExportMergeInput{Base: base, HasBase: true, ThisMachine: ours, Shared: shared})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(first.Combined, []string{"t-a"}) {
		t.Fatalf("combined = %v, conflicts %+v", first.Combined, first.Conflicts)
	}
	second, err := MergeExports(ExportMergeInput{Base: first.Base(), HasBase: true, ThisMachine: exportOf(string(first.Import[0])), Shared: shared})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Import) != 0 || len(second.Conflicts) != 0 {
		t.Fatalf("second merge imported %s, conflicts %+v; want nothing", second.Import, second.Conflicts)
	}
}

func TestOneItemChangedTwoWaysIsAConflictNamingBothVersions(t *testing.T) {
	t.Parallel()

	base := exportOf(exportLine(t, item("t-a", "a", "open", earlier)))
	ours := exportLine(t, with(item("t-a", "a", "in_progress", mine), "notes", "note from here"))
	theirs := exportLine(t, with(item("t-a", "a", "closed", yours), "close_reason", "done there"))
	merge, err := MergeExports(ExportMergeInput{Base: base, HasBase: true, ThisMachine: exportOf(ours), Shared: exportOf(theirs)})
	if err != nil {
		t.Fatal(err)
	}
	if len(merge.Import) != 0 {
		t.Errorf("imported %s for an item that cannot be merged", merge.Import)
	}
	if len(merge.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want one", merge.Conflicts)
	}
	conflict := merge.Conflicts[0]
	if conflict.ID != "t-a" || conflict.ThisMachine != ours || conflict.Shared != theirs {
		t.Errorf("conflict = %+v, want both whole versions", conflict)
	}
	if !slices.ContainsFunc(conflict.Differences, func(d ExportFieldDifference) bool {
		return d.Field == "status" && d.ThisMachine == "in_progress" && d.Shared == "closed"
	}) {
		t.Errorf("differences = %+v, want the status named both ways", conflict.Differences)
	}
	// The conflict goes on reading as one at the next merge, rather than the
	// shared version becoming common ground.
	again, err := MergeExports(ExportMergeInput{Base: merge.Base(), HasBase: true, ThisMachine: exportOf(ours), Shared: exportOf(theirs)})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Conflicts) != 1 {
		t.Errorf("the next merge found conflicts %+v, want the same one", again.Conflicts)
	}
}

func TestChangesThatDoNotCollideOnOneItemAreCombined(t *testing.T) {
	t.Parallel()

	baseFields := with(with(item("t-a", "a", "open", earlier), "notes", "earlier note"), "labels", []string{"keep", "drop"})
	baseFields["metadata"] = map[string]any{"shared": "x"}
	base := exportOf(exportLine(t, baseFields))

	ours := with(with(item("t-a", "a edited here", "open", mine), "notes", "earlier note\nnote from here"), "labels", []string{"keep", "added-here"})
	ours["metadata"] = map[string]any{"shared": "x", "here": "1"}
	theirs := with(with(item("t-a", "a", "open", yours), "notes", "earlier note\nnote from there"), "labels", []string{"keep", "drop", "added-there"})
	theirs["metadata"] = map[string]any{"shared": "x", "there": "2"}
	theirs["description"] = "described there"

	merge, err := MergeExports(ExportMergeInput{Base: base, HasBase: true, ThisMachine: exportOf(exportLine(t, ours)), Shared: exportOf(exportLine(t, theirs))})
	if err != nil {
		t.Fatal(err)
	}
	if len(merge.Conflicts) != 0 || !slices.Equal(merge.Combined, []string{"t-a"}) {
		t.Fatalf("combined %v, conflicts %+v", merge.Combined, merge.Conflicts)
	}
	row, err := parseExportRow(string(merge.Import[0]))
	if err != nil {
		t.Fatal(err)
	}
	if got := row.title(); got != "a edited here" {
		t.Errorf("title = %q", got)
	}
	if got := exportString(row.fields["description"]); got != "described there" {
		t.Errorf("description = %q", got)
	}
	if got := exportString(row.fields["notes"]); got != "earlier note\nnote from here\nnote from there" {
		t.Errorf("notes = %q", got)
	}
	if got := canonicalExportValue(row.fields["labels"]); got != `["keep","added-here","added-there"]` {
		t.Errorf("labels = %s", got)
	}
	if got := canonicalExportValue(row.fields["metadata"]); got != `{"here":"1","shared":"x","there":"2"}` {
		t.Errorf("metadata = %s", got)
	}
}

// Notes that no longer begin with the earlier notes were rewritten, against the
// rule that they are append-only, so nothing here decides which rewriting stands.
func TestRewrittenNotesAreNotCombined(t *testing.T) {
	t.Parallel()

	base := exportOf(exportLine(t, with(item("t-a", "a", "open", earlier), "notes", "earlier")))
	merge, err := MergeExports(ExportMergeInput{
		Base: base, HasBase: true,
		ThisMachine: exportOf(exportLine(t, with(item("t-a", "a", "open", mine), "notes", "rewritten here"))),
		Shared:      exportOf(exportLine(t, with(item("t-a", "a", "open", yours), "notes", "earlier\nadded there"))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(merge.Conflicts) != 1 || merge.Conflicts[0].Differences[0].Field != "notes" {
		t.Fatalf("conflicts = %+v, want the notes", merge.Conflicts)
	}
}

// bd keeps its own row on anything but a strictly later one, whatever the clocks
// on the two machines said.
func TestAnImportedRowIsAlwaysLaterThanThisMachinesRow(t *testing.T) {
	t.Parallel()

	base := exportOf(exportLine(t, item("t-a", "a", "open", earlier)))
	merge, err := MergeExports(ExportMergeInput{
		Base: base, HasBase: true,
		ThisMachine: exportOf(exportLine(t, item("t-a", "a", "open", mine))),
		Shared:      exportOf(exportLine(t, item("t-a", "a edited there", "open", earlier))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := importedField(t, merge, "t-a", "updated_at"); got != "2026-11-02T10:00:01Z" {
		t.Errorf("updated_at = %q, want one second past this machine's", got)
	}
}

func TestARemovalMeetingAnEditIsAConflictAndAnUnopposedOneChangesNothing(t *testing.T) {
	t.Parallel()

	base := exportOf(exportLine(t, item("t-a", "a", "open", earlier)), exportLine(t, item("t-b", "b", "open", earlier)))
	merge, err := MergeExports(ExportMergeInput{
		Base: base, HasBase: true,
		ThisMachine: exportOf(exportLine(t, item("t-a", "a edited here", "open", mine)), exportLine(t, item("t-b", "b", "open", earlier))),
		Shared:      exportOf("{\"id\":\"t-c\",\"title\":\"c\"}"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(merge.Conflicts) != 1 || merge.Conflicts[0].ID != "t-a" || merge.Conflicts[0].Shared != "" {
		t.Fatalf("conflicts = %+v, want t-a edited here and removed there", merge.Conflicts)
	}
	if !strings.Contains(string(merge.Base()), `"t-b"`) {
		t.Errorf("the base forgot the removed item, which would read as added here: %s", merge.Base())
	}
	for _, row := range merge.Import {
		if strings.Contains(string(row), `"t-b"`) {
			t.Errorf("imported a removal: %s", row)
		}
	}
}

// A machine reading the shared export for the first time has no record of what
// the two last agreed on, so the newer version is read as the change.
func TestTheFirstMergeTakesTheNewerVersion(t *testing.T) {
	t.Parallel()

	merge, err := MergeExports(ExportMergeInput{
		ThisMachine: exportOf(exportLine(t, item("t-newer-here", "here", "open", yours)), exportLine(t, item("t-older-here", "here", "open", earlier))),
		Shared:      exportOf(exportLine(t, item("t-newer-here", "there", "open", earlier)), exportLine(t, item("t-older-here", "there", "open", yours))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(merge.Conflicts) != 0 || !slices.Equal(merge.FromShared, []string{"t-older-here"}) {
		t.Fatalf("taken %v, conflicts %+v", merge.FromShared, merge.Conflicts)
	}
}

// Settling replaces the earlier row with the version decided against, so the
// kept one reads as the only change.
func TestASettledItemKeepsTheChosenVersion(t *testing.T) {
	t.Parallel()

	base := exportOf(exportLine(t, item("t-a", "a", "open", earlier)))
	ours := exportLine(t, item("t-a", "a", "in_progress", mine))
	theirs := exportLine(t, item("t-a", "a", "closed", yours))

	keepShared, err := MergeExports(ExportMergeInput{Base: base, HasBase: true, ThisMachine: exportOf(ours), Shared: exportOf(theirs), Settled: map[string]string{"t-a": ours}})
	if err != nil {
		t.Fatal(err)
	}
	if len(keepShared.Conflicts) != 0 || importedField(t, keepShared, "t-a", "status") != "closed" {
		t.Errorf("keeping the shared version: %+v", keepShared)
	}
	keepHere, err := MergeExports(ExportMergeInput{Base: base, HasBase: true, ThisMachine: exportOf(ours), Shared: exportOf(theirs), Settled: map[string]string{"t-a": theirs}})
	if err != nil {
		t.Fatal(err)
	}
	if len(keepHere.Conflicts) != 0 || len(keepHere.Import) != 0 {
		t.Errorf("keeping this machine's version: %+v", keepHere)
	}
}

func TestAnExportHoldingConflictMarkersIsRefused(t *testing.T) {
	t.Parallel()

	if _, err := MergeExports(ExportMergeInput{ThisMachine: []byte("{\"id\":\"t-a\"}\n"), Shared: []byte("<<<<<<< HEAD\n{\"id\":\"t-a\"}\n")}); err == nil || !strings.Contains(err.Error(), "the shared export") {
		t.Fatalf("error = %v, want the shared export refused", err)
	}
}
