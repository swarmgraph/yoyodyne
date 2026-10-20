package readmodel

// The tracker not answering listings, said once, with since when.
//
// A listing that fails is said where it failed: a problem on a pass, a line on
// a docket, a refusal in a conversation. On 2026-09-29 the development manager
// reported `bd list` timing out and nothing said since when, or whether it was
// still happening, without reading every one of those places. The harness's
// tracker client now writes how each listing ends to one record, and this is
// that record on the attention line: the harness's move, because the listings
// are the harness's and it retries them, and nothing about a contended store
// is a person's to settle while it lasts.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TrackerListingRecord is how the tracker's listings stand. It is satisfied by
// *runstate.TrackerListingStore.
type TrackerListingRecord interface {
	Read() (runstate.TrackerListings, error)
}

// ReadTrackerUnanswered reads whether the tracker is failing listings now. A
// reading with no record wired says nothing; a record that cannot be read says
// so rather than reporting the tracker answering.
func ReadTrackerUnanswered(sources Sources) (*runstate.TrackerListings, string) {
	if sources.TrackerListings == nil {
		return nil, ""
	}
	record, err := sources.TrackerListings.Read()
	if err != nil {
		return nil, fmt.Sprintf("whether the tracker is answering listings could not be read: %v", err)
	}
	if !record.Failing() {
		return nil, ""
	}
	return &record, ""
}

// trackerUnansweredAttention is listings failing, as the attention line
// carries it. The moment they began failing is the ID, since a later outage
// after an answer is a different one.
func trackerUnansweredAttention(record runstate.TrackerListings) Attention {
	return resolved(Attention{
		Kind:            AttentionTrackerUnanswered,
		ID:              record.FailingSince.UTC().Format(time.RFC3339),
		TrackerListings: &record,
	})
}
