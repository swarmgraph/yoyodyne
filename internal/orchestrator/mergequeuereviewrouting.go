package orchestrator

// A merge queue candidate's review on the reviewer's endpoint pair, under the
// same rules as a run's own review (reviewerrouting.go).
//
// The candidate's review is one logical operation, bound to the generation: a
// generation is one exact candidate commit, and a new candidate is a new
// generation that starts from the primary again. Where the operation stands on
// the pair is kept on the generation (runstate.MergeQueueReviewRouting), so a
// wait, a worker that is replaced, and a restart all find it again. It moves from
// its primary to its alternate at most once, only for a classified usage limit
// or a primary already known to be limited, and only to an alternate that can
// serve the reviewer's read-only role now; once moved it stays there. Each
// attempt, with the endpoint it was made on, is on the generation's record: the
// ones set aside as interruptions, and the review evidence of the last.
//
// The pair is the run's own pinned reviewer pair where the run recorded one, and
// the reviewer agent's configured pair otherwise. A project whose reviewer has no
// fallback never reaches any of this.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// queueReviewPair is the reviewer pair the candidate's review is routed on, and
// false for a review that is not routed.
func (w MergeQueueWorker) queueReviewPair(run runstate.State, at time.Time) (runstate.RoutingSnapshot, bool, error) {
	if run.Routing != nil && run.Routing.Reviewer != nil {
		return *run.Routing.Reviewer, true, nil
	}
	pair, routed, err := w.Pipeline.resolvedReviewerPair()
	if err != nil || !routed {
		return runstate.RoutingSnapshot{}, false, err
	}
	return runstate.RoutingSnapshotOf(pair, runstate.RoutingClaimed, at), true, nil
}

// reviewEndpoint is the endpoint a routing selects on its pair.
func reviewEndpoint(snapshot runstate.RoutingSnapshot, routing runstate.MergeQueueReviewRouting) runstate.RoutedEndpoint {
	if routing.Selected == runstate.EndpointAlternate && snapshot.Alternate != nil {
		return *snapshot.Alternate
	}
	return snapshot.Primary
}

// moveQueueReview moves the candidate's review to its alternate and records it,
// and says why it did not where it cannot: fallback is off, the switch is spent
// or already made, or the alternate cannot serve the reviewer now.
func (w MergeQueueWorker) moveQueueReview(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, generation *runstate.MergeQueueGeneration, snapshot runstate.RoutingSnapshot, trigger runstate.SwitchTrigger, evidence string) (bool, string, error) {
	routing := runstate.MergeQueueReviewRouting{Selected: runstate.EndpointPrimary}
	if generation.ReviewRouting != nil {
		routing = *generation.ReviewRouting
	}
	switch {
	case snapshot.Alternate == nil || !snapshot.FallbackEnabled:
		return false, "the reviewer's pair has fallback switched off", nil
	case routing.Selected != runstate.EndpointPrimary:
		return false, "this review has already moved to its alternate, and it does not move back", nil
	}
	if problem := w.Pipeline.endpointProblemFor(ctx, *snapshot.Alternate, domain.RoleReviewer); problem != "" {
		return false, fmt.Sprintf("the alternate, %s, cannot serve it: %s", describeEndpoint(*snapshot.Alternate), problem), nil
	}
	now := w.now()
	routing.Selected, routing.SwitchedAt, routing.Trigger = runstate.EndpointAlternate, &now, trigger
	routing.Evidence, routing.Waiting = boundedEvidence(evidence), ""
	revised := *generation
	revised.ReviewRouting = &routing
	if err := w.Queue.RecordGeneration(lease, key, revised); err != nil {
		return false, "", fmt.Errorf("record that generation %d's review moved to its alternate: %w", generation.Number, err)
	}
	*generation = revised
	return true, "", nil
}

// chooseQueueReviewEndpoint reads where the candidate's review stands on its
// pair, passes over a primary already known to be limited, and answers the
// endpoint to review on. Where the endpoint it ends on is known to be limited it
// answers a wait, saying why the other could not serve.
func (w MergeQueueWorker) chooseQueueReviewEndpoint(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, generation *runstate.MergeQueueGeneration, snapshot runstate.RoutingSnapshot) (runstate.RoutedEndpoint, error) {
	p := w.Pipeline
	if generation.ReviewRouting == nil {
		revised := *generation
		revised.ReviewRouting = &runstate.MergeQueueReviewRouting{Selected: runstate.EndpointPrimary}
		if err := w.Queue.RecordGeneration(lease, key, revised); err != nil {
			return runstate.RoutedEndpoint{}, fmt.Errorf("record the endpoint pair generation %d's review is made on: %w", generation.Number, err)
		}
		*generation = revised
	}
	endpoint := reviewEndpoint(snapshot, *generation.ReviewRouting)
	limit, limited := p.knownLimit(endpoint)
	if !limited {
		return endpoint, nil
	}
	known := fmt.Sprintf("%s was already known to have reached its usage limit: %s", describeEndpoint(endpoint), limit.Says)
	if generation.ReviewRouting.Selected == runstate.EndpointPrimary {
		moved, why, err := w.moveQueueReview(ctx, lease, key, generation, snapshot, runstate.SwitchPrimaryLimited, known)
		if err != nil {
			return runstate.RoutedEndpoint{}, err
		}
		if moved {
			return reviewEndpoint(snapshot, *generation.ReviewRouting), nil
		}
		known += ", and it did not move: " + why
	}
	if limit.ResetsAt != nil {
		known += fmt.Sprintf("; the limit resets at %s", limit.ResetsAt.Local().Format("2006-01-02 15:04 MST"))
	}
	return runstate.RoutedEndpoint{}, mergeQueueWait{reason: "the reviewer's endpoint is out of capacity: " + known}
}

// waitedQueueReview records why the candidate's review is waiting on the
// endpoint it has selected.
func (w MergeQueueWorker) waitedQueueReview(lease *runstate.Lease, key runstate.MergeQueueKey, generation *runstate.MergeQueueGeneration, why string) error {
	if generation.ReviewRouting == nil || generation.ReviewRouting.Waiting == boundedEvidence(why) {
		return nil
	}
	revised := *generation
	routing := *generation.ReviewRouting
	routing.Waiting = boundedEvidence(why)
	revised.ReviewRouting = &routing
	if err := w.Queue.RecordGeneration(lease, key, revised); err != nil {
		return fmt.Errorf("record why generation %d's review waits: %w", generation.Number, err)
	}
	*generation = revised
	return nil
}
