package orchestrator

// Taking up approved changes the merge queue held: the "GitHub's own merge
// queue" section of docs/designs/integration-through-a-merge-queue.md.
//
// With execution.merge_queue on, a change whose target branch requires the
// forge's own merge queue, or whose target's protection could not be read, is
// not admitted and not replayed the old way: its run stops at the promotion
// with its approval standing and the setting named (mergequeueadmission.go,
// CauseMergeQueueHeld). Nothing a person decides is needed to take it up again,
// only the condition clearing, so the harness looks for itself. A held change
// is taken up at the first reading that finds the protection readable and the
// forge's queue not required, and the changes one queue holds are taken up in
// the order they were held, which is the order they were approved: each is
// resumed at its promotion (IntegrationResumer), where it is admitted to the
// queue as any approved change is. A switch turned off while changes are held
// clears the condition too, and the resumed promotion then integrates the old
// way, replay, checks and review included, because that is what the switch
// says now.
//
// Clearing is also what ends the queue's one report of the condition: the
// queue's refusal record is removed, so a condition found again later is
// reported again, once. The harness never changes a repository setting.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// MergeQueueHeldRefusals is the queue's record of the condition that holds its
// changes. It is satisfied by *runstate.MergeQueueStore.
type MergeQueueHeldRefusals interface {
	ClearRefusal(ctx context.Context, key runstate.MergeQueueKey) error
}

// MergeQueueHeldRuns reads the run a held change was approved by. It is
// satisfied by *runstate.Store.
type MergeQueueHeldRuns interface {
	Load(runID string) (runstate.State, error)
}

// MergeQueueHeldChanges takes up the approved changes the merge queue held,
// once what held them has cleared.
type MergeQueueHeldChanges struct {
	// Docket is where a held change's stoppage is listed, as every integration
	// stop is.
	Docket   ResumeDocket
	Runs     MergeQueueHeldRuns
	Refusals MergeQueueHeldRefusals
	// Forge is asked again whether the target requires its own merge queue;
	// nil for a project with no forge that can say, whose targets nothing of
	// the forge's holds.
	Forge   publish.QueueCapabilityReader
	Harness queuemode.Harness
	// Enabled is execution.merge_queue as the configuration says now.
	Enabled bool
	// ProductID names the queue of a run that recorded no repository.
	ProductID string
	// Resume resumes one held run at its promotion. It is satisfied by
	// IntegrationResumer.Resume.
	Resume func(ctx context.Context, runID string) (IntegrationResumeResult, error)
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

func (h MergeQueueHeldChanges) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// MergeQueueHeldPass is what one look at the held changes found and did.
type MergeQueueHeldPass struct {
	// Held is how many held changes were found, and Resumed how many of them
	// were taken up.
	Held    int `json:"held"`
	Resumed int `json:"resumed"`
	// Says is the look's account, in ordinary words.
	Says string `json:"says,omitempty"`
}

type heldChange struct {
	entry triage.Entry
	run   runstate.State
}

// Take looks at every change the merge queue holds and takes up those whose
// condition has cleared. It never returns an error: what stopped it is its
// account.
func (h MergeQueueHeldChanges) Take(ctx context.Context) MergeQueueHeldPass {
	var pass MergeQueueHeldPass
	if h.Docket == nil || h.Runs == nil || h.Resume == nil {
		return pass
	}
	entries, err := h.Docket.List()
	if err != nil {
		pass.Says = fmt.Sprintf("the changes the merge queue holds could not be listed: %v", err)
		return pass
	}
	queues := map[runstate.MergeQueueKey][]heldChange{}
	var keys []runstate.MergeQueueKey
	for _, entry := range entries {
		if entry.Class != triage.ClassStoppedRun || entry.IntegrationStop == nil || entry.IntegrationStop.Cause != string(runstate.CauseMergeQueueHeld) {
			continue
		}
		run, err := h.Runs.Load(entry.RunID)
		if err != nil || run.IntegrationStop == nil || run.IntegrationStop.Cause != runstate.CauseMergeQueueHeld {
			// A run whose record no longer says it is held was taken up already,
			// or settled some other way, and its entry closes on its own terms.
			continue
		}
		key := runstate.MergeQueueKey{Repository: nonEmpty(run.RepositoryID, h.ProductID), TargetBranch: run.TargetBranch}
		if _, seen := queues[key]; !seen {
			keys = append(keys, key)
		}
		queues[key] = append(queues[key], heldChange{entry: entry, run: run})
		pass.Held++
	}
	if pass.Held == 0 {
		return pass
	}
	var says []string
	for _, key := range keys {
		held := queues[key]
		// Held in the order they were approved, which is the order they were
		// held in; the run identifier breaks a tie, so two readings agree.
		sort.SliceStable(held, func(i, j int) bool {
			a, b := held[i].run.IntegrationStop.RecordedAt, held[j].run.IntegrationStop.RecordedAt
			if !a.Equal(b) {
				return a.Before(b)
			}
			return held[i].run.RunID < held[j].run.RunID
		})
		stands, why := h.stands(ctx, key)
		if stands {
			says = append(says, fmt.Sprintf("%d change(s) into %s stay held: %s", len(held), key.TargetBranch, why))
			continue
		}
		_ = h.Refusals.ClearRefusal(context.WithoutCancel(ctx), key)
		resumed, stopped := 0, ""
		for _, change := range held {
			if ctx.Err() != nil {
				stopped = "the session working the queue stopped"
				break
			}
			result, err := h.Resume(ctx, change.run.RunID)
			switch {
			case result.Resumed:
				resumed++
			case result.IntakeHeld != nil:
				stopped = "intake is held, so nothing is taken up until it is released"
			case result.CapacityFull != nil:
				stopped = fmt.Sprintf("every developer slot is occupied (%d of %d), so the rest wait for one", result.CapacityFull.Active, result.CapacityFull.Limit)
			case err != nil:
				stopped = fmt.Sprintf("run %s could not be taken up: %v", change.run.RunID, err)
			default:
				stopped = fmt.Sprintf("run %s was not taken up", change.run.RunID)
			}
			if stopped != "" {
				break
			}
		}
		pass.Resumed += resumed
		account := fmt.Sprintf("%s: %s; took up %d of the %d change(s) held for it, in the order they were approved", key.TargetBranch, why, resumed, len(held))
		if stopped != "" {
			account += "; " + stopped
		}
		says = append(says, account)
	}
	pass.Says = strings.Join(says, ". ")
	return pass
}

// stands reports whether what holds a queue's changes still stands, and says
// what was found either way.
func (h MergeQueueHeldChanges) stands(ctx context.Context, key runstate.MergeQueueKey) (bool, string) {
	if !h.Enabled {
		return false, "execution.merge_queue is off now, so each is integrated the way changes integrate without the queue"
	}
	if h.Forge == nil {
		return false, "nothing here can say the forge requires its own merge queue any more"
	}
	observed, err := h.Forge.QueueCapabilities(ctx, key.TargetBranch)
	if err != nil {
		return true, fmt.Sprintf("the protection of %s still could not be read: %v", key.TargetBranch, err)
	}
	selection, err := queuemode.Select(key.TargetBranch, observed, h.Harness, h.now())
	if err != nil {
		return true, err.Error()
	}
	if selection.Hold != nil {
		return true, strings.TrimSpace(selection.Hold.Requirement)
	}
	return false, fmt.Sprintf("the protection of %s is readable and no longer requires the forge's own merge queue", key.TargetBranch)
}
