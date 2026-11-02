package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Closer is who closed a pull request without merging it, and when, as the
// forge recorded it on the request's timeline. By is the forge account's login,
// and is empty where the forge recorded the closing without naming who did it —
// an account since deleted is the ordinary way.
type Closer struct {
	Number int       `json:"number"`
	By     string    `json:"by,omitempty"`
	At     time.Time `json:"at"`
}

// closerQuery reads the last closing event on a pull request's timeline. The
// listing verbs say a request is closed and nothing about who closed it, so it
// is asked of the forge's GraphQL API, scoped as mergeQueueQuery is.
const closerQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) {
    closedAt
    timelineItems(last: 1, itemTypes: [CLOSED_EVENT]) {
      nodes { ... on ClosedEvent { createdAt actor { login } } }
    }
  } }
}`

// Closer reads who closed a pull request and when. It only reads: nothing
// about the request changes. A request the forge reports with no closing at all
// is an error rather than an empty answer, because the caller asks only about a
// request it was told is closed, and an answer that says otherwise is not one it
// can write down.
func (g GitHub) Closer(ctx context.Context, number int) (Closer, error) {
	if number <= 0 {
		return Closer{}, fmt.Errorf("pull request number %d is not a request", number)
	}
	stdout, err := g.graphql(ctx, fmt.Sprintf("ask the forge who closed pull request %d", number),
		"-f", "query="+closerQuery,
		"-F", "owner={owner}",
		"-F", "name={repo}",
		"-F", "number="+strconv.Itoa(number))
	if err != nil {
		return Closer{}, err
	}
	var answered struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					ClosedAt      *time.Time `json:"closedAt"`
					TimelineItems struct {
						Nodes []struct {
							CreatedAt time.Time `json:"createdAt"`
							Actor     *struct {
								Login string `json:"login"`
							} `json:"actor"`
						} `json:"nodes"`
					} `json:"timelineItems"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &answered); err != nil {
		return Closer{}, fmt.Errorf("decode who closed pull request %d: %w", number, err)
	}
	pull := answered.Data.Repository.PullRequest
	if pull == nil {
		return Closer{}, fmt.Errorf("the forge's answer names no pull request %d", number)
	}
	closure := Closer{Number: number}
	if nodes := pull.TimelineItems.Nodes; len(nodes) > 0 {
		last := nodes[len(nodes)-1]
		closure.At = last.CreatedAt
		if last.Actor != nil {
			closure.By = g.redact(strings.TrimSpace(last.Actor.Login))
		}
	}
	if closure.At.IsZero() && pull.ClosedAt != nil {
		closure.At = *pull.ClosedAt
	}
	if closure.At.IsZero() {
		return Closer{}, fmt.Errorf("the forge reports no closing of pull request %d", number)
	}
	return closure, nil
}
