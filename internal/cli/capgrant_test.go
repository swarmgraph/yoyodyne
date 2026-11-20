package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fixedPricer stands in for the run records a grant prices its item from.
type fixedPricer struct {
	price runstate.ItemPrice
	err   error
}

func (p fixedPricer) PriceFromIndex(string) (runstate.ItemPrice, error) { return p.price, p.err }

// fixedClock is a clock that always reads one time.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

var _ execution.Clock = fixedClock{}

// The command line prices the item before it records the Lead Product Manager's
// grant, so the grant carries the item's cost so far; and where the cost cannot
// be read it records nothing.
func TestTheLeadProductManagersGrantCarriesTheItemsCostSoFar(t *testing.T) {
	t.Parallel()

	runs, err := runstate.NewStore(t.TempDir(), domain.ProductID("example"))
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	const item = "yoyodyne-ifd.430.38"
	caps := runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	for crossing := 1; crossing <= runstate.MaxDelegatedCapCrossings; crossing++ {
		if _, err := runs.Triage().CrossCap(ctx, item, domain.RoleDevelopmentManager, runstate.TriageReviewRoundBudget, "narrow findings", at, caps); err != nil {
			t.Fatalf("CrossCap() %d error = %v", crossing, err)
		}
	}
	clock := fixedClock{at: at}

	refusing := conversationCapGrantor{store: runs.Triage(), prices: fixedPricer{err: errors.New("unreadable")}, caps: caps, clock: clock}
	if _, err := refusing.GrantCap(ctx, item, runstate.TriageReviewRoundBudget, "worth it"); err == nil || !strings.Contains(err.Error(), "the grant was not recorded") {
		t.Fatalf("a grant whose cost cannot be read = %v, want it refused", err)
	}
	if counters, _ := runs.Triage().Counters(item); counters.LeadGrants() != 0 {
		t.Fatalf("a refused grant recorded %d grants", counters.LeadGrants())
	}

	price := runstate.ItemPrice{WorkItemID: item, TotalUSD: 41.27, Runs: make([]runstate.RunPrice, 3), UnknownRuns: 1}
	grantor := conversationCapGrantor{store: runs.Triage(), prices: fixedPricer{price: price}, caps: caps, clock: clock}
	granted, err := grantor.GrantCap(ctx, item, runstate.TriageReviewRoundBudget, "worth it")
	if err != nil {
		t.Fatalf("GrantCap() error = %v", err)
	}
	want := runstate.TriageGrantCost{TotalUSD: 41.27, Runs: 3, UnknownRuns: 1}
	recorded, _ := granted.Counters.OverrideOf(runstate.TriageReviewRoundBudget)
	if granted.CostSoFar == nil || *granted.CostSoFar != want || recorded.CostSoFar == nil || *recorded.CostSoFar != want {
		t.Fatalf("the grant reports cost %+v and records %+v, want %+v", granted.CostSoFar, recorded.CostSoFar, want)
	}
}
