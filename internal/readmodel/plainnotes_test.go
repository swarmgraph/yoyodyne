package readmodel

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// The dashboard card shows an item's notes as the tracker client reads them,
// which is prose: the boundary each harness append is stored under is not
// shown to the operator.
func TestTheItemCardShowsNotesWithoutTheirStoredBoundaries(t *testing.T) {
	t.Parallel()

	stored := "Admitted by hand.\n" + beads.FrameNote("Reprioritized from priority 2 to 1 by the Lead Product Manager.")
	listing := fmt.Sprintf(`[{"id":"yoyodyne-ifd.1","title":"An item","status":"open","priority":1,"issue_type":"task","notes":%q}]`, stored)
	client := beads.Client{Runner: fixedListing{stdout: listing}, Dir: t.TempDir()}
	item, err := ReadWorkItem(context.Background(), WorkItemSources{Tracker: client, Runs: &fakeHistories{}, TrackerTimeout: time.Second, Now: func() time.Time { return moment }}, "yoyodyne-ifd.1")
	if err != nil {
		t.Fatalf("ReadWorkItem: %v", err)
	}
	if want := "Admitted by hand.\nReprioritized from priority 2 to 1 by the Lead Product Manager."; item.Notes != want {
		t.Fatalf("card notes = %q, want %q", item.Notes, want)
	}
	if strings.Contains(item.Notes, "yoyodyne-note-bytes") {
		t.Fatalf("the card shows a stored boundary: %q", item.Notes)
	}
}
