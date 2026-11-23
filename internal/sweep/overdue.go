package sweep

import (
	"errors"
	"fmt"
	"strings"
)

// Act is what a Lead Product Manager's pass did about a release or a priority 0
// item it was told has waited more than seven days. The list is closed, and it
// holds no word for deferring: the operator's direction of November 7, 2026 is
// that such an item is acted on or explained, not put off again.
type Act string

const (
	ActOrdered            Act = "ordered"
	ActStarted            Act = "started"
	ActUnblocked          Act = "unblocked"
	ActParkingCorrected   Act = "parking-corrected"
	ActParkingLifted      Act = "parking-lifted"
	ActDroppedFromRelease Act = "dropped-from-release"
	ActRetired            Act = "retired"
	// ActExplained is the one act that needs no tracker change: the pass says,
	// by name, why the item still waits, and the explanation is kept with the
	// pass's account on its record.
	ActExplained Act = "explained"
)

var acts = []Act{ActOrdered, ActStarted, ActUnblocked, ActParkingCorrected, ActParkingLifted, ActDroppedFromRelease, ActRetired, ActExplained}

// Valid reports an act on the closed list.
func (a Act) Valid() bool {
	for _, known := range acts {
		if a == known {
			return true
		}
	}
	return false
}

// ChangesTheTracker reports an act that is a tracker change, which the harness
// checks against the items the pass's tracker actions changed. Only an
// explanation is not.
func (a Act) ChangesTheTracker() bool {
	return a.Valid() && a != ActExplained
}

func actList() string {
	quoted := make([]string, len(acts))
	for i, act := range acts {
		quoted[i] = fmt.Sprintf("%q", act)
	}
	return strings.Join(quoted, ", ")
}

// Overdue is what the pass did about one item it was told has waited more than
// seven days.
type Overdue struct {
	Item   string `json:"item"`
	Act    Act    `json:"act"`
	Reason string `json:"reason"`
}

// Validate reports every shape violation in the entry at once. An act that is
// not on the list is not refused here: it is read as the item deferred, and the
// harness sends the pass back naming it rather than refusing the whole account.
func (o Overdue) Validate() error {
	var problems []error
	switch item := strings.TrimSpace(o.Item); {
	case item == "":
		problems = append(problems, errors.New("item is required"))
	case len(item) > MaxTextBytes:
		problems = append(problems, fmt.Errorf("item is %d bytes, limit is %d", len(item), MaxTextBytes))
	}
	if act := strings.TrimSpace(string(o.Act)); act == "" {
		problems = append(problems, errors.New("act is required"))
	} else if len(act) > MaxTextBytes {
		problems = append(problems, fmt.Errorf("act is %d bytes, limit is %d", len(act), MaxTextBytes))
	}
	switch reason := strings.TrimSpace(o.Reason); {
	case reason == "":
		problems = append(problems, errors.New("reason says what was done and why"))
	case len(reason) > maxLeftBytes:
		problems = append(problems, fmt.Errorf("reason is %d bytes, limit is %d", len(reason), maxLeftBytes))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid overdue item: %w", err)
	}
	return nil
}

// OverdueEntry is what the account says the pass did about one overdue item,
// and false where it says nothing. A later entry stands over an earlier one.
func (r Result) OverdueEntry(item string) (Overdue, bool) {
	item = strings.TrimSpace(item)
	var entry Overdue
	found := false
	for _, overdue := range r.Overdue {
		if strings.TrimSpace(overdue.Item) == item {
			entry, found = overdue, true
		}
	}
	return entry, found
}

// OverdueContract is what a Lead Product Manager's pass handed overdue items is
// told, beside the contract above. It is separate for the reason AuditContract
// is: a pass handed none has nothing to put in the field.
func OverdueContract() string {
	return strings.Join([]string{
		`Your block also carries "overdue": one entry for every item listed above as waiting more than seven days, at most ` + maxLeftText + ` in one turn:`,
		"",
		`"overdue":[{"item":"beads-id","act":"one of the acts below","reason":"what you did and why, in a sentence or two"}]`,
		"",
		"The acts are " + actList() + `. Every act but "explained" is a change you make with a tracker action on this pass, to the item itself or to an item it waits on, and the harness checks that one of them changed: place or reprioritize it to order it, unpark it or change its parking reason, link or unlink what it waits on, take an item out of the release by changing the release item, or retire it with a reason. "explained" says, by name, why the item still waits although it is past seven days; the explanation is its "reason", and the harness keeps it with this account on the pass's record.`,
		`There is no act for deferring. An item with no entry, an act not on the list, or a tracker act no tracker action of this pass bears out reads as deferred again, and the harness sends the pass back once, naming each such item.`,
	}, "\n")
}

// OverdueSentBack is what a pass is told when it ended with items it was told
// have waited more than seven days still only deferred. Everything else the
// pass did stands; this turn is for those items.
func OverdueSentBack(items []string) string {
	return strings.Join([]string{
		fmt.Sprintf("This pass is ending with %s, waiting more than seven days, only deferred again: its account records no act on it that this pass's tracker actions bear out, and no explanation. Everything else this pass did stands and is recorded as you gave it.", strings.Join(items, ", ")),
		"",
		`For each, act on it now with a tracker action and record the act in your block's "overdue", or record "explained" with why it still waits, and give your block again. Nothing else is asked of this turn. If an item is still only deferred, that is written on the pass's record by name.`,
		"",
		OverdueContract(),
	}, "\n")
}
