package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// QueueEvent is one thing the forge recorded about a pull request's queued
// merge: the request entering or leaving the merge queue, or its auto-merge
// being turned on or off. Reason is the forge's own words for a removal or a
// disabling, and is empty where the forge gave none.
type QueueEvent struct {
	Kind   string    `json:"kind"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
}

// Describe says the event in words, with the forge's reason quoted where it
// gave one.
func (e QueueEvent) Describe() string {
	what := e.Kind
	switch e.Kind {
	case "AddedToMergeQueueEvent":
		what = "added to the merge queue"
	case "RemovedFromMergeQueueEvent":
		what = "removed from the merge queue"
	case "AutoMergeEnabledEvent":
		what = "auto-merge turned on"
	case "AutoMergeDisabledEvent":
		what = "auto-merge turned off"
	}
	if !e.At.IsZero() {
		what += " at " + e.At.Local().Format("2006-01-02 15:04 MST")
	}
	if e.Reason != "" {
		what += fmt.Sprintf(" (the forge's reason: %q)", e.Reason)
	} else if e.Kind == "RemovedFromMergeQueueEvent" || e.Kind == "AutoMergeDisabledEvent" {
		what += " (no reason given)"
	}
	return what
}

// QueueRemoval is the forge's account of a queued merge it stopped holding: the
// request's merge state now, and the queue and auto-merge events on its
// timeline, oldest first. It is read only to say why a merge was dropped, so it
// grants nothing and asks the forge to do nothing.
type QueueRemoval struct {
	Number int `json:"number"`
	// MergeStatus is the forge's merge state for the request (BLOCKED, DIRTY and
	// the rest), and MergeStatusError says why it could not be read.
	MergeStatus      string       `json:"merge_status,omitempty"`
	MergeStatusError string       `json:"merge_status_error,omitempty"`
	Events           []QueueEvent `json:"events,omitempty"`
}

// Requirement states in words the requirement of the base branch the forge's
// merge state reports unmet, and is empty where it reports none.
func (q QueueRemoval) Requirement() string { return mergeRequirement(q.MergeStatus) }

// Reason is the forge's own words for the last removal or disabling on the
// request's timeline, and is empty where the forge gave none.
func (q QueueRemoval) Reason() string {
	for index := len(q.Events) - 1; index >= 0; index-- {
		event := q.Events[index]
		if event.Kind != "RemovedFromMergeQueueEvent" && event.Kind != "AutoMergeDisabledEvent" {
			continue
		}
		return event.Reason
	}
	return ""
}

// queueEventsQuery reads the queue and auto-merge events on a pull request's
// timeline. The listing verbs carry none of them, so it is asked of the forge's
// GraphQL API, scoped as mergeQueueQuery is.
const queueEventsQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) {
    timelineItems(last: 20, itemTypes: [ADDED_TO_MERGE_QUEUE_EVENT, REMOVED_FROM_MERGE_QUEUE_EVENT, AUTO_MERGE_ENABLED_EVENT, AUTO_MERGE_DISABLED_EVENT]) {
      nodes {
        __typename
        ... on AddedToMergeQueueEvent { createdAt }
        ... on RemovedFromMergeQueueEvent { createdAt reason }
        ... on AutoMergeEnabledEvent { createdAt }
        ... on AutoMergeDisabledEvent { createdAt reason reasonCode }
      }
    }
  } }
}`

// QueueRemoval reads the forge's account of why it stopped holding a pull
// request's queued merge. The timeline is what fails it: a merge state that
// could not be read is recorded on the answer beside the events, because the
// events are the forge's own words and the merge state is a reading of now.
func (g GitHub) QueueRemoval(ctx context.Context, number int) (QueueRemoval, error) {
	if number <= 0 {
		return QueueRemoval{}, fmt.Errorf("pull request number %d is not a request", number)
	}
	stdout, err := g.graphql(ctx, fmt.Sprintf("ask the forge for the merge queue events of pull request %d", number),
		"-f", "query="+queueEventsQuery,
		"-F", "owner={owner}",
		"-F", "name={repo}",
		"-F", "number="+strconv.Itoa(number))
	if err != nil {
		return QueueRemoval{}, err
	}
	var answered struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					TimelineItems struct {
						Nodes []struct {
							Typename   string    `json:"__typename"`
							CreatedAt  time.Time `json:"createdAt"`
							Reason     string    `json:"reason"`
							ReasonCode string    `json:"reasonCode"`
						} `json:"nodes"`
					} `json:"timelineItems"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &answered); err != nil {
		return QueueRemoval{}, fmt.Errorf("decode the merge queue events of pull request %d: %w", number, err)
	}
	pull := answered.Data.Repository.PullRequest
	if pull == nil {
		return QueueRemoval{}, fmt.Errorf("the forge's answer names no pull request %d", number)
	}
	removal := QueueRemoval{Number: number}
	for _, node := range pull.TimelineItems.Nodes {
		reason := g.redact(strings.TrimSpace(node.Reason))
		switch code := strings.TrimSpace(node.ReasonCode); {
		case code == "" || strings.Contains(reason, code):
		case reason == "":
			reason = code
		default:
			reason += " (" + code + ")"
		}
		removal.Events = append(removal.Events, QueueEvent{Kind: strings.TrimSpace(node.Typename), At: node.CreatedAt, Reason: firstLine(reason)})
	}
	sort.SliceStable(removal.Events, func(i, j int) bool { return removal.Events[i].At.Before(removal.Events[j].At) })
	status, err := g.MergeState(ctx, number)
	if err != nil {
		removal.MergeStatusError = err.Error()
	} else {
		removal.MergeStatus = status
	}
	return removal, nil
}
