package runstate

import (
	"errors"
	"time"
)

// EpicWait is a run that stopped before its next call to a provider because its
// work item is in no open epic the operator named. The run keeps its claim, its
// branch and its checkout, and is picked up again when the item is added to an
// epic or epic mode is turned off.
type EpicWait struct {
	// Since is when the run stopped.
	Since time.Time `json:"since"`
}

// Validate rejects a wait that does not say when it began.
func (w EpicWait) Validate() error {
	if w.Since.IsZero() {
		return errors.New("since is required")
	}
	return nil
}
