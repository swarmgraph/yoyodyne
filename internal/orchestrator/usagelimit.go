package orchestrator

// A provider refusal met outside a run, written down where the sink reads it.
//
// A run parks: the limit, the deadline, and the item that is waiting all go into
// the run's own durable state, and the record it leaves is what an operator who
// was asleep reads in the morning. Everything else the harness points at a
// provider has no run to park. A branch review is the one here — it has no run
// store, no worktree, and no integration by design — so an exhausted limit
// stopped it at somebody's terminal and left the product's record saying
// nothing.
//
// This is the other half of that. It is not a pause: nothing here waits, retries,
// or spends a budget, because the caller is an operator who asked for a review
// and is owed the refusal now. What it does is leave the trace.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// UsageLimitRecorder is where a provider's refusal is collected. It is satisfied
// by runstate.UsageLimitStore.
type UsageLimitRecorder interface {
	Record(exhaustion runstate.UsageLimitExhaustion) error
}

// recordUsageLimit writes down one refusal and reports only what went wrong
// writing it. A caller with nothing to record to, or nothing to record, does
// neither and says nothing.
func recordUsageLimit(to UsageLimitRecorder, productID domain.ProductID, at time.Time, waiting string, limit *backend.UsageLimit) error {
	return recordUsageLimitOn(to, productID, at, waiting, limit, "", "")
}

// recordUsageLimitOn is recordUsageLimit for a refusal whose account and model
// are known, so the record reads back as a refusal of exactly that endpoint.
func recordUsageLimitOn(to UsageLimitRecorder, productID domain.ProductID, at time.Time, waiting string, limit *backend.UsageLimit, account, model string) error {
	if to == nil || limit == nil {
		return nil
	}
	exhaustion := runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     productID,
		At:            at,
		Waiting:       waiting,
		Kind:          limit.Kind,
		AccountAlias:  account,
		Model:         model,
	}
	if !limit.ResetsAt.IsZero() {
		resetsAt := limit.ResetsAt.UTC()
		exhaustion.ResetsAt = &resetsAt
	}
	if err := to.Record(exhaustion); err != nil {
		return fmt.Errorf("record the provider's refusal: %w", err)
	}
	return nil
}
