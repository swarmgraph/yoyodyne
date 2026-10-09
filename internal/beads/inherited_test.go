package beads

import "testing"

// bd show lists the items an item links to, titles included, so the work a
// waiting parent waits on can be named by what it is rather than by its
// identifier alone.
func TestAShowReadingKeepsTheLinkedItemsTitle(t *testing.T) {
	t.Parallel()

	item, err := decodeSingleWorkItem([]byte(`[{"id":"yoyodyne-ifd.433.21","title":"Automatic document publication","status":"open",
		"dependencies":[{"id":"yoyodyne-ifd.437.14","title":"Amendment ownership design","status":"open","dependency_type":"blocks"}]}]`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(item.Dependencies) != 1 || item.Dependencies[0].Title != "Amendment ownership design" {
		t.Fatalf("dependencies = %#v, want the linked item's title kept", item.Dependencies)
	}
	block := InheritedBlock{Through: []string{item.ID}, WaitsOn: item.Dependencies,
		Titles: map[string]string{item.ID: item.Title, "yoyodyne-ifd.437.14": item.Dependencies[0].Title}}
	want := "blocked through its parent 'Automatic document publication' (yoyodyne-ifd.433.21), which waits on 'Amendment ownership design' (yoyodyne-ifd.437.14, open)"
	if got := block.Describe(); got != want {
		t.Fatalf("Describe() = %q, want %q", got, want)
	}
	// An item whose title nothing read is named by its identifier alone.
	if got := (InheritedBlock{}).Name("yoyodyne-ifd.9", "open"); got != "yoyodyne-ifd.9 (open)" {
		t.Fatalf("Name() = %q", got)
	}
}
