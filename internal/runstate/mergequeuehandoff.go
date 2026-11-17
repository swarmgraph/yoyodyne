package runstate

// The wait between one merge queue entry landing and the queue's next check
// starting, kept beside the queue so the queue's weekly figures can count it.
// The worker writes one when it starts the first check after a landing
// (orchestrator's MergeQueueWorker.recordHandoff); nothing else writes it.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

const mergeQueueHandoffsName = "handoffs.json"

// maxMergeQueueHandoffs bounds the handoffs kept for one queue: the most
// recent are kept and the oldest dropped. A busy day lands a few dozen
// changes, so this is months of them, well inside the bound on one record.
const maxMergeQueueHandoffs = 1000

// MergeQueueHandoff is one wait between an entry landing and the queue
// starting its next check.
type MergeQueueHandoff struct {
	// LandedEntry and LandedWorkItem name the entry that landed.
	LandedEntry    string `json:"landed_entry"`
	LandedWorkItem string `json:"landed_work_item,omitempty"`
	// MergedAt is when the change landed: the forge's own time for a merge it
	// made, or when the queue confirmed a landing it pushed itself.
	MergedAt time.Time `json:"merged_at"`
	// NoticedAt is when the queue confirmed the landing. The gap between
	// MergedAt and NoticedAt is the time the queue took to learn of a merge.
	NoticedAt time.Time `json:"noticed_at"`
	// NextEntry and NextWorkItem name the entry whose check started next, and
	// NextAdmittedAt when it was admitted.
	NextEntry      string    `json:"next_entry"`
	NextWorkItem   string    `json:"next_work_item,omitempty"`
	NextAdmittedAt time.Time `json:"next_admitted_at"`
	// FirstCheckAt is when that entry's check started.
	FirstCheckAt time.Time `json:"first_check_at"`
	// WaitSeconds is the time the queue had work and checked nothing: from
	// the landing, or from the next entry's admission where that came later,
	// to FirstCheckAt.
	WaitSeconds int64 `json:"wait_seconds"`
}

// Wait is the handoff's wait as a duration.
func (h MergeQueueHandoff) Wait() time.Duration { return time.Duration(h.WaitSeconds) * time.Second }

// NewMergeQueueHandoff is the handoff from a landing to the next check, with
// its wait worked out.
func NewMergeQueueHandoff(landed MergeQueueEntry, mergedAt, noticedAt time.Time, next MergeQueueEntry, firstCheckAt time.Time) MergeQueueHandoff {
	from := mergedAt
	if next.AdmittedAt.After(from) {
		from = next.AdmittedAt
	}
	wait := firstCheckAt.Sub(from)
	if wait < 0 {
		wait = 0
	}
	return MergeQueueHandoff{
		LandedEntry: landed.EntryID, LandedWorkItem: landed.WorkItemID,
		MergedAt: mergedAt.UTC(), NoticedAt: noticedAt.UTC(),
		NextEntry: next.EntryID, NextWorkItem: next.WorkItemID, NextAdmittedAt: next.AdmittedAt.UTC(),
		FirstCheckAt: firstCheckAt.UTC(),
		WaitSeconds:  int64(wait / time.Second),
	}
}

func (h MergeQueueHandoff) validate() error {
	var problems []error
	if strings.TrimSpace(h.LandedEntry) == "" || strings.TrimSpace(h.NextEntry) == "" {
		problems = append(problems, errors.New("a handoff names the entry that landed and the entry checked next"))
	}
	if h.MergedAt.IsZero() || h.NoticedAt.IsZero() || h.FirstCheckAt.IsZero() {
		problems = append(problems, errors.New("a handoff records when the change landed, when the queue noticed, and when the next check started"))
	}
	if h.WaitSeconds < 0 {
		problems = append(problems, errors.New("a handoff's wait is never negative"))
	}
	return errors.Join(problems...)
}

// RecordHandoff adds a handoff to the queue's record of them. A landing has
// one handoff, so one already recorded for the same landed entry is kept and
// this one is not added; it reports whether it added it.
func (s *MergeQueueStore) RecordHandoff(ctx context.Context, key MergeQueueKey, handoff MergeQueueHandoff) (bool, error) {
	if err := errors.Join(key.validate(), handoff.validate()); err != nil {
		return false, fmt.Errorf("merge queue handoff: %w", err)
	}
	queueRoot, unlock, err := s.lockQueue(ctx, key)
	if err != nil {
		return false, err
	}
	defer unlock()
	var handoffs []MergeQueueHandoff
	encoded, err := queueRoot.ReadFile(mergeQueueHandoffsName)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return false, fmt.Errorf("read the merge queue's handoffs for %s: %w", key.TargetBranch, err)
	default:
		unknown, err := decodeTolerating(encoded, &handoffs)
		noteUnknownFields("merge queue "+mergeQueueHandoffsName, unknown)
		if err != nil {
			return false, fmt.Errorf("decode the merge queue's handoffs for %s: %w", key.TargetBranch, err)
		}
	}
	for _, recorded := range handoffs {
		if recorded.LandedEntry == handoff.LandedEntry {
			return false, nil
		}
	}
	handoffs = append(handoffs, handoff)
	if extra := len(handoffs) - maxMergeQueueHandoffs; extra > 0 {
		handoffs = handoffs[extra:]
	}
	encoded, err = encodeRecord("merge queue handoffs", handoffs)
	if err != nil {
		return false, err
	}
	if err := queueRoot.WriteFile(mergeQueueHandoffsName, encoded, 0o600, false); err != nil {
		return false, fmt.Errorf("write the merge queue's handoffs for %s: %w", key.TargetBranch, err)
	}
	return true, queueRoot.Sync()
}

// Handoffs reports the queue's recorded handoffs, oldest first.
func (s *MergeQueueStore) Handoffs(key MergeQueueKey) ([]MergeQueueHandoff, error) {
	var handoffs []MergeQueueHandoff
	if _, err := s.readSide(key, mergeQueueHandoffsName, &handoffs); err != nil {
		return nil, err
	}
	return handoffs, nil
}
