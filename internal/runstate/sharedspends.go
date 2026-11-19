package runstate

// What an item's budgets cost across every factory of a team, in team mode
// (docs/designs/working-alongside-other-contributors.md, section 8).
//
// The triage record (triage.go) counts what this machine spent on an item. With
// team mode on, every spend is also a record in the shared records reference,
// and the count a spend is held to is the count of those records across every
// factory, so a repair cap of one is a cap of one for the team. This file is the
// seam: runstate names what a spend is and what refuses one, and
// internal/teamspends makes the records and the pushes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SharedSpend is one spend of an item's budget, as the shared records name it.
type SharedSpend struct {
	// Budget is the budget spent: TriageRepairGrantBudget, TriageRerunBudget,
	// TriageMergeRearmBudget or TriageReviewRoundBudget.
	Budget string
	// Run is the run the spend was made for.
	Run string
	// Key says which part of the budget was spent where the budget is counted
	// finer than by item: the publication a merge re-arm repeats, and the
	// developer attempt a review round judged.
	Key string
	// Return says the record gives back a review round the run was charged and
	// the environment refused, since a record is never removed. It is not a
	// spend, so the claim need not be held to make it, and nothing refuses it.
	Return bool
}

// SharedSpendCounts is what an item has spent across every factory.
type SharedSpendCounts struct {
	RepairGrants int
	Reruns       int
	// ReviewRounds is the rounds counted less the rounds given back.
	ReviewRounds int
	// Rearms is the merge re-arms spent, by the publication each repeated.
	Rearms map[string]int
}

// SharedSpends is the team's shared record of what an item has spent. It is
// satisfied by teamspends.Spender and is nil with team mode off.
type SharedSpends interface {
	// Spend counts the item's spends across every factory, asks within whether the
	// spend is permitted at that count, and only if it is writes the spend record
	// and pushes it. It returns once the record is on the shared remote, and a
	// spend that returns an error was not made. within may be called again with a
	// newer count when another factory pushed first, and its error is returned
	// unchanged. A spend that could not be pushed returns *SpendNotSharedError,
	// and one made by a factory not holding the item's claim returns
	// *NotClaimHolderError.
	Spend(ctx context.Context, workItemID string, spend SharedSpend, within func(SharedSpendCounts) error) error
}

// SpendNotSharedError is a spend that could not be pushed to the shared
// remote. It is not a failure of the work: the spend is not made and nothing is
// charged, and the caller waits and asks again.
type SpendNotSharedError struct {
	WorkItemID string
	Budget     string
	Problem    string
}

func (e *SpendNotSharedError) Error() string {
	return fmt.Sprintf("the %s of %s was not made, because it could not be shared with the team's other factories: %s; nothing was charged and it will be asked again",
		e.Budget, e.WorkItemID, e.Problem)
}

// NotClaimHolderError is a spend asked by a factory that does not hold the
// item's claim. Only the holder spends an item's budgets.
type NotClaimHolderError struct {
	WorkItemID string
	Budget     string
}

func (e *NotClaimHolderError) Error() string {
	return fmt.Sprintf("the %s of %s was not made, because this factory does not hold the claim on it and only the factory holding the claim spends an item's budgets",
		e.Budget, e.WorkItemID)
}

// IsSpendNotShared reports a spend that waits on the shared remote.
func IsSpendNotShared(err error) bool {
	var notShared *SpendNotSharedError
	return errors.As(err, &notShared)
}

// UseSharedSpends makes every triage store this store hands out count and
// record its spends through the team's shared records. It is set once, where the
// project has team mode on, and a store that was never given it counts on this
// machine alone.
func (s *Store) UseSharedSpends(spends SharedSpends) { s.sharedSpends = spends }

// adopt raises this record to what the whole team has spent, so a counter here
// never says less than the shared records do: a spend another factory made, or
// this factory's own before a takeover, is counted exactly as one made here.
func (c *TriageCounters) adopt(shared SharedSpendCounts) {
	c.RepairGrants = max(c.RepairGrants, shared.RepairGrants)
	c.Reruns = max(c.Reruns, shared.Reruns)
	c.ReviewRounds = max(c.ReviewRounds, shared.ReviewRounds)
	for publication, spent := range shared.Rearms {
		if have := c.RearmedPublications[publication]; spent > have {
			if c.RearmedPublications == nil {
				c.RearmedPublications = make(map[string]int, len(shared.Rearms))
			}
			c.RearmedPublications[publication] = spent
			c.MergeRearms += spent - have
		}
	}
}

// spendShared asks check whether a spend is permitted, against the record raised
// to the team's count when team mode is on, and where it is records the spend on
// the shared remote before returning. check reads the record and changes
// nothing, because it is asked again when another factory pushed first.
func (s *TriageStore) spendShared(ctx context.Context, workItemID string, counters *TriageCounters, spend SharedSpend, check func() error) error {
	if s.shared == nil {
		if check == nil {
			return nil
		}
		return check()
	}
	return s.shared.Spend(ctx, workItemID, spend, func(counts SharedSpendCounts) error {
		counters.adopt(counts)
		if check == nil {
			return nil
		}
		return check()
	})
}

// All reads the record of every item this machine has any.
func (s *TriageStore) All() ([]TriageCounters, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list triage counters: %w", err)
	}
	var all []TriageCounters
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		file, err := os.ReadFile(filepath.Join(s.root, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read triage counters: %w", err)
		}
		var named struct {
			WorkItemID string `json:"work_item_id"`
		}
		if err := json.Unmarshal(file, &named); err != nil || strings.TrimSpace(named.WorkItemID) == "" {
			return nil, fmt.Errorf("triage counters %s do not name a work item", entry.Name())
		}
		counters, err := s.load(named.WorkItemID)
		if err != nil {
			return nil, err
		}
		all = append(all, counters)
	}
	return all, nil
}

// ---- joining ----

const spendsJoinedFile = "shared-spends-joined.json"

// SpendsJoined is when this machine pushed the spends it had counted before it
// joined team mode.
type SpendsJoined struct {
	SchemaVersion int       `json:"schema_version"`
	At            time.Time `json:"at"`
}

// SpendsJoined reports when this machine pushed its earlier spends, and false
// where it has not.
func (s *SharedRecordsStore) SpendsJoined() (time.Time, bool, error) {
	encoded, err := os.ReadFile(filepath.Join(s.stateRoot, s.productDirectory(), spendsJoinedFile))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	var joined SpendsJoined
	// It is read only to say whether it was written, so a field a newer build added
	// is no reason to push the spends again.
	if _, err := decodeTolerating(encoded, &joined); err != nil {
		return time.Time{}, false, fmt.Errorf("decode the record of joining team spends: %w", err)
	}
	return joined.At, true, nil
}

// MarkSpendsJoined records that this machine pushed its earlier spends, so it
// does so once.
func (s *SharedRecordsStore) MarkSpendsJoined(at time.Time) error {
	encoded, err := encodeRecord("spends joined", SpendsJoined{SchemaVersion: 1, At: at.UTC()})
	if err != nil {
		return err
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return fmt.Errorf("pin the shared records' state root: %w", err)
	}
	defer root.Close()
	if err := root.Unchanged(); err != nil {
		return err
	}
	if err := root.WriteFile(filepath.Join(s.productDirectory(), spendsJoinedFile), encoded, 0o600, false); err != nil {
		return fmt.Errorf("record that this machine joined the team's spends: %w", err)
	}
	return nil
}
