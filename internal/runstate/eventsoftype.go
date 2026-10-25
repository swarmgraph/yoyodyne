package runstate

// Reading only the events of some types from a log that holds every type.
//
// A reader that asks what a conversation did to the tracker, or how each review
// of a run came out, wants a handful of events out of a log that is mostly the
// provider's own output: the busiest conversation's log is tens of megabytes,
// and decoding every line of it to keep the few that matter is most of the cost
// of the answer. So a line is looked at for its type before it is decoded, and
// one that cannot be of a wanted type is passed over without being read as JSON
// at all. A line that names a wanted type and still will not decode is reported
// beside the events that did, by position, as every tolerant read here reports
// one; a line of another type is never decoded, so it is never reported.

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// EventsOfType returns one run's events of the named types, in the order they
// were recorded, and the lines naming one of those types that would not decode.
// A run with no event log has none.
func (s *Store) EventsOfType(runID string, types ...execution.EventType) ([]execution.Event, []SkippedLine, error) {
	path, err := s.eventPath(runID)
	if err != nil {
		return nil, nil, err
	}
	return eventsOfType(path, runID, types)
}

// EventsOfType returns one conversation's events of the named types, in the
// order they were recorded, and the lines naming one of those types that would
// not decode. A conversation with no event log has none.
func (s *ConversationStore) EventsOfType(conversationID string, types ...execution.EventType) ([]execution.Event, []SkippedLine, error) {
	path, err := s.eventPathForConversation(conversationID)
	if err != nil {
		return nil, nil, err
	}
	return eventsOfType(path, conversationID, types)
}

func eventsOfType(path, streamID string, types []execution.EventType) ([]execution.Event, []SkippedLine, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open event log of %s: %w", streamID, err)
	}
	defer file.Close()

	// The marker is the type as the encoder writes it (encodeEvent): a key and
	// a quoted value with nothing between them. A payload quoting the same text
	// passes this look and is then refused by the type check after decoding, so
	// the look only ever saves work and never decides.
	markers := make([][]byte, 0, len(types))
	wanted := make(map[execution.EventType]bool, len(types))
	for _, eventType := range types {
		markers = append(markers, []byte(`"type":"`+string(eventType)+`"`))
		wanted[eventType] = true
	}
	var events []execution.Event
	skipped, err := scanLog(file, maxEncodedEventBytes, func(line []byte) error {
		named := false
		for _, marker := range markers {
			if bytes.Contains(line, marker) {
				named = true
				break
			}
		}
		if !named {
			return nil
		}
		event, err := execution.DecodeEvent(line)
		if err != nil {
			return err
		}
		if event.RunID != streamID {
			return fmt.Errorf("event belongs to %s", event.RunID)
		}
		if wanted[event.Type] {
			events = append(events, event)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read event log of %s: %w", streamID, err)
	}
	return events, skipped, nil
}
