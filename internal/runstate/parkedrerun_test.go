package runstate

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Only a note carrying the parking's own reason says who placed it: a note about
// an earlier parking, or prose that mentions one, names nobody.
func TestParkedByNamesOnlyWhoPlacedThisParking(t *testing.T) {
	t.Parallel()

	const note = "Parked by the Lead Product Manager in conversation chat-1, after turn 4.\n\nReason: held for the route"
	for _, test := range []struct{ parking, notes, want string }{
		{"held for the route", "Admitted.\n\n" + note + "\n\nlater prose: Parked by the weather, after turn 1.", "the Lead Product Manager in conversation chat-1"},
		{"Pull request #4 was closed without merging", note, "somebody the item's notes do not name"},
		{"held for the route", "", "somebody the item's notes do not name"},
		{"yoyodyne run run-0123456789abcdef0123456789abcdef landed evidence", note, "run run-0123456789abcdef0123456789abcdef"},
	} {
		item := beads.WorkItem{Notes: test.notes, Parking: domain.WorkItemParking(test.parking)}
		if got := ParkedBy(item); got != test.want {
			t.Errorf("ParkedBy(%q) = %q, want %q", test.parking, got, test.want)
		}
	}
}
