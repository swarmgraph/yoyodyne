package beads

import (
	"testing"
	"time"
)

// A closed item carries when the tracker closed it and what it was closed with,
// which is what the Lead Product Manager's audit lists closed work by; an open
// item, or a close time the decoder cannot read, carries none.
func TestAClosedItemCarriesItsCloseTimeAndReason(t *testing.T) {
	t.Parallel()

	items, err := decodeWorkItems([]byte(`[
		{"id":"example-1","title":"Closed","status":"closed","closed_at":"2026-10-08T02:39:15Z","close_reason":"merged by the forge"},
		{"id":"example-2","title":"Open","status":"open"},
		{"id":"example-3","title":"Unreadable","status":"closed","closed_at":"yesterday"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 8, 2, 39, 15, 0, time.UTC); !items[0].ClosedAt.Equal(want) || items[0].CloseReason != "merged by the forge" {
		t.Errorf("closed item = %v, %q", items[0].ClosedAt, items[0].CloseReason)
	}
	if !items[1].ClosedAt.IsZero() || items[1].CloseReason != "" || !items[2].ClosedAt.IsZero() {
		t.Errorf("items = %+v, want no close time on open work or an unreadable one", items[1:])
	}
}
