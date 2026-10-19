package runstate

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// EscalationEnding is the record that the development manager's escalation of a
// run's stoppage to the operator has ended, and what ended it: the item parked,
// retired, or closed, the run's branch and worktree both gone, or the escalation
// giving no reason from the operator's closed list, which hands it back to her.
type EscalationEnding struct {
	At time.Time `json:"at"`
	// Why is what ended it, in the words the work item was told.
	Why string `json:"why"`
}

// Validate reports every contract violation in the ending at once.
func (e EscalationEnding) Validate() error {
	var problems []error
	if e.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	switch why := strings.TrimSpace(e.Why); {
	case why == "":
		problems = append(problems, errors.New("why is required: an ending nobody can read the cause of is the silence this record replaces"))
	case len(e.Why) > MaxBlockerBytes:
		problems = append(problems, fmt.Errorf("why is %d bytes, limit is %d", len(e.Why), MaxBlockerBytes))
	}
	return errors.Join(problems...)
}

// BoundEscalationEnding folds a reason onto one line and cuts it to what the
// record carries, never mid-character. What ended an escalation quotes a parking
// reason somebody wrote at whatever length they wanted, and an ending refused for
// its length is an escalation left on the operator's line.
func BoundEscalationEnding(why string) string {
	return oneline.Fold(why, MaxBlockerBytes-len(oneline.Marker))
}
