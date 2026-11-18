package readmodel

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
)

// yoyo status lists a held item with the whole of its reason, however long:
// a decided re-run waiting on a slot is said through to what clears the wait,
// never cut off part-way through a word as the health check once cut it.
func TestStatusShowsADecidedRerunsWholeWaitReason(t *testing.T) {
	t.Parallel()

	wait := `the "rerun" the development manager decided about the stoppage of run run-273b7c4d85397b33f32706ac59590fee is waiting on developer capacity: every developer slot is occupied: 3 active, limit 3. What clears it: a developer slot freeing, which needs nobody; nothing was claimed, so the stoppage keeps its re-run`
	reason := "run run-273b7c4d85397b33f32706ac59590fee stopped on it and its change is preserved (branch checked and there), so a fresh run would start over on top of work that is still there; " +
		awaitingCarryOutClause + "; problem: the same reason has stood for more than 1h0m0s; first passed over at 2026-11-15 08:28 PST; current reason since 2026-11-15 09:23 PST: " + wait
	standing := Standing{Admitted: 1, NotStartable: []Refused{
		{WorkItemID: "yoyodyne-7xx", Kind: backlog.HeldForAPerson, RunID: "run-273b7c4d85397b33f32706ac59590fee", Reason: reason},
	}}
	if rendered := standing.renderNotStartable(); !strings.Contains(rendered, reason) {
		t.Fatalf("yoyo status reads:\n%s\nwant the held item's whole reason, ending %q", rendered, wait[len(wait)-60:])
	}
}
