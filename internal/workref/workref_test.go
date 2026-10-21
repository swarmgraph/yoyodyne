package workref

import (
	"reflect"
	"testing"
)

func exampleIndex() *Index {
	return NewIndex([]string{"ex-7", "ex-7.2", "ex-12", "ex-abc", "ex-abc.4"})
}

func TestAFullIdRunsToItsEndAndNeverStopsEarly(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		text string
		want []Reference
	}{
		{"Blocked by ex-7; see ex-7.2.", []Reference{{ID: "ex-7", Start: 11, End: 15}, {ID: "ex-7.2", Start: 21, End: 27}}},
		{"ex-7.2s are done", []Reference{{ID: "ex-7.2s", Start: 0, End: 7}}},
		{"(ex-abc), then ex-abc.4:", []Reference{{ID: "ex-abc", Start: 1, End: 7}, {ID: "ex-abc.4", Start: 15, End: 23}}},
		{"see ex-9.", []Reference{{ID: "ex-9", Start: 4, End: 8}}},
		// Not references: a word with the prefix, a path, a branch, a code span.
		{"ex-ample text", nil},
		{"docs/ex-7.md and branch/ex-7/abc", nil},
		{"run `yoyo cost ex-7`", nil},
		{"https://forge.example/ex-7", nil},
		{"kex-7 and ex-7x-1", nil},
	} {
		found, _ := exampleIndex().FindFull(test.text)
		if !reflect.DeepEqual(found, test.want) {
			t.Errorf("FindFull(%q) = %+v, want %+v", test.text, found, test.want)
		}
	}
}

func TestAShortIdIsResolvedOnlyToTheOneItemThatEndsInIt(t *testing.T) {
	t.Parallel()

	index := NewIndex([]string{"yoyodyne-ifd.434.9", "yoyodyne-ifd.434.3", "yoyodyne-ifd.12", "yoyodyne-abc.12", "yoyodyne-c02"})
	found, unresolved := index.Find("434.9, ifd.434.3, c02 and 12; waited 3.5 hours on Go 1.22")
	want := []Reference{
		{ID: "yoyodyne-ifd.434.9", Start: 0, End: 5},
		{ID: "yoyodyne-ifd.434.3", Start: 7, End: 16},
		{ID: "yoyodyne-c02", Start: 18, End: 21},
	}
	if !reflect.DeepEqual(found, want) {
		t.Errorf("Find() = %+v, want %+v", found, want)
	}
	// "12" is a plain number and never a short id; "3.5 hours" is a quantity;
	// "1.22" names nothing and is counted.
	if unresolved != 1 {
		t.Errorf("Find() unresolved = %d, want 1", unresolved)
	}
	if found, _ := index.FindFull("434.9 and c02"); len(found) != 0 {
		t.Errorf("FindFull() = %+v, want no short id resolved in older text", found)
	}
	if found, _ := NewIndex([]string{"yoyodyne-ifd.1.2", "yoyodyne-abc.1.2"}).Find("see 1.2"); len(found) != 0 {
		t.Errorf("Find() = %+v, want a short id two items end in left as written", found)
	}
}

func TestExpandReplacesOnlyTheBytesAReferenceCovers(t *testing.T) {
	t.Parallel()

	text := "Blocked on 434.9 and 434.3."
	references := []Reference{{ID: "yoyodyne-ifd.434.3", Start: 21, End: 26}, {ID: "yoyodyne-ifd.434.9", Start: 11, End: 16}}
	if got, want := Expand(text, references), "Blocked on yoyodyne-ifd.434.9 and yoyodyne-ifd.434.3."; got != want {
		t.Errorf("Expand() = %q, want %q", got, want)
	}
	// References that no longer cover their id, or fall outside the text, are
	// not trusted.
	for _, bad := range [][]Reference{
		{{ID: "yoyodyne-ifd.434.9", Start: 0, End: 7}},
		{{ID: "yoyodyne-ifd.434.9", Start: 11, End: 99}},
		{{ID: "yoyodyne-ifd.434.9", Start: -1, End: 5}},
	} {
		if got := Expand(text, bad); got != text {
			t.Errorf("Expand(%+v) = %q, want the text as written", bad, got)
		}
	}
}

func TestAnIndexWithNoListingKnowsPrefixesAndWhatACallerNames(t *testing.T) {
	t.Parallel()

	index := NewPrefixIndex("calc")
	if found, _ := index.FindFull("calc-wja is stuck; calc-wja.2 too"); !reflect.DeepEqual(found, []Reference{{ID: "calc-wja.2", Start: 19, End: 29}}) {
		t.Errorf("FindFull() = %+v, want only the id whose shape says it is one", found)
	}
	if found, _ := index.Knowing("calc-wja").FindFull("calc-wja is stuck"); !reflect.DeepEqual(found, []Reference{{ID: "calc-wja", Start: 0, End: 8}}) {
		t.Errorf("Knowing().FindFull() = %+v, want the named root id found", found)
	}
	if PrefixOf("yoyodyne-ifd.432.28") != "yoyodyne" || PrefixOf("not an id") != "" {
		t.Errorf("PrefixOf() did not split the prefix off a full id")
	}
}
