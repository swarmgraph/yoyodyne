package runstate

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// mergeQueueStreamPattern is the event stream a generation's checks and review
// write to (MergeQueueGeneration.EventStream).
var mergeQueueStreamPattern = regexp.MustCompile(`^mqe-[a-f0-9]{32}-generation-[0-9]+$`)

// AppendEvent appends one event to a generation's own event stream: what its
// checks, its base check, and its review emitted. The stream is the
// generation's rather than the run's, because the run that made the change
// ended when the change was admitted and its own log ended with it; it is kept
// under the product's merge queues, one file per generation.
func (s *MergeQueueStore) AppendEvent(event execution.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if !mergeQueueStreamPattern.MatchString(event.RunID) {
		return fmt.Errorf("%q is not a merge queue generation's event stream", event.RunID)
	}
	encoded, err := encodeEvent(event)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedEventBytes {
		return StopError{Class: StopEventBound, Cause: fmt.Errorf("encoded event is %d bytes, limit is %d", len(encoded), maxEncodedEventBytes)}
	}
	root, err := repowrite.NewRoot(s.stateRoot)
	if err != nil {
		return fmt.Errorf("resolve the merge queue state root: %w", err)
	}
	// Appended in checked batches through the confined writer, which syncs the
	// stream and stops at a moved directory or a link from outside.
	if err := root.Append(path.Join(filepath.ToSlash(s.directory()), "events", event.RunID+".jsonl"), encoded, 0o600, 0o700); err != nil {
		return fmt.Errorf("append to the event stream %s: %w", event.RunID, err)
	}
	return nil
}
