package runstate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// RulingNoteOpening is how the note a role's document leaves on a work item it
// answers begins (the chat package's rulingNote). The words are held here, below
// both the conversation that writes the note and the recurring pass that reads
// it, so the two cannot drift apart.
const RulingNoteOpening = "Design or ruling for this item written as "

// RulingSnapshot is one work item whose newest note is the woken role's ruling
// on it, as the last pass to deal with the item saw it: the item, a digest of
// everything about it the role would read again (its text, notes, status,
// priority, labels, when it last changed, and the documents it cites), and when
// the snapshot was taken. Passes that leave the item out carry it forward
// unchanged. The task's next pass leaves the item out where its
// digest is unchanged and no report naming it was filed since
// (internal/orchestrator/standingrulings.go).
type RulingSnapshot struct {
	ID     string    `json:"id"`
	Digest string    `json:"digest"`
	At     time.Time `json:"at"`
}

// MaxSweepRulings bounds the snapshots and the standing rulings one pass
// records. Both are drawn from the work waiting in the role's conversation,
// which a pass records no more of than MaxSweepDelivered.
const MaxSweepRulings = MaxSweepDelivered

// validateRulings checks the standing rulings a pass left out and the snapshots
// it took.
func (s Sweep) validateRulings() []error {
	var problems []error
	if len(s.RulingsStanding) > MaxSweepRulings {
		problems = append(problems, fmt.Errorf("%d standing rulings left out of one pass, limit is %d", len(s.RulingsStanding), MaxSweepRulings))
	}
	for i, id := range s.RulingsStanding {
		if strings.TrimSpace(id) == "" || len(id) > MaxSweepModelBytes {
			problems = append(problems, fmt.Errorf("rulings_standing[%d] must name a work item", i))
		}
	}
	if len(s.Rulings) > MaxSweepRulings {
		problems = append(problems, fmt.Errorf("%d ruling snapshots in one pass, limit is %d", len(s.Rulings), MaxSweepRulings))
	}
	for i, ruling := range s.Rulings {
		switch {
		case strings.TrimSpace(ruling.ID) == "" || len(ruling.ID) > MaxSweepModelBytes:
			problems = append(problems, fmt.Errorf("rulings[%d] must name a work item", i))
		case strings.TrimSpace(ruling.Digest) == "" || len(ruling.Digest) > MaxSweepModelBytes:
			problems = append(problems, fmt.Errorf("rulings[%d] must carry a bounded digest", i))
		case ruling.At.IsZero():
			problems = append(problems, errors.New("a ruling snapshot names when it was taken"))
		}
	}
	return problems
}
