package publish

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The reviews and comments people leave on a published pull request.
//
// The forge keeps three kinds apart: reviews (each with a verdict — approved,
// changes requested, or only commented — and a body), comments on a line of the
// change, and comments on the request as a whole. Feedback reads all three as
// the forge recorded them and says nothing about what they mean; sorting them
// is the harness's (runstate's forgefeedback.go).

// Review states as the forge's REST API names them.
const (
	ReviewApproved         = "APPROVED"
	ReviewChangesRequested = "CHANGES_REQUESTED"
	ReviewCommented        = "COMMENTED"
)

// FeedbackReview is one review of a pull request.
type FeedbackReview struct {
	ID     int64
	Author string
	// State is the review's verdict in the forge's words: ReviewApproved,
	// ReviewChangesRequested, ReviewCommented, or another the forge names, such
	// as a dismissed or pending review.
	State  string
	Body   string
	At     time.Time
	Commit string
	URL    string
}

// FeedbackComment is one comment, on a line of the change where Path names a
// file, or on the request as a whole where it does not.
type FeedbackComment struct {
	ID     int64
	Author string
	Body   string
	At     time.Time
	Path   string
	Line   int
	URL    string
}

// FeedbackReading is everything said on one pull request, and the forge
// account that opened it, which is the account the factory acts as: the
// harness opens every request it publishes, under the access the forge CLI
// holds.
type FeedbackReading struct {
	Number int
	// State is the request's state as the forge names it, such as OPEN.
	State        string
	Opener       string
	Reviews      []FeedbackReview
	LineComments []FeedbackComment
	Comments     []FeedbackComment
}

// Feedback reads the reviews, the comments on lines of the change, and the
// comments on one pull request, every page of each.
func (g GitHub) Feedback(ctx context.Context, number int) (FeedbackReading, error) {
	if number <= 0 {
		return FeedbackReading{}, fmt.Errorf("pull request number %d is not a request", number)
	}
	reading := FeedbackReading{Number: number}
	request, err := g.apiQuery(ctx, "GET", fmt.Sprintf("repos/{owner}/{repo}/pulls/%d", number),
		"--jq", "{state: .state, merged: .merged, opener: .user.login}")
	if err != nil {
		return FeedbackReading{}, fmt.Errorf("ask the forge about pull request %d: %w", number, err)
	}
	if request.Status != execution.ProcessSucceeded {
		return FeedbackReading{}, g.forgeFailure(fmt.Sprintf("ask the forge about pull request %d", number), request)
	}
	var head struct {
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		Opener string `json:"opener"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(request.Stdout)), &head); err != nil {
		return FeedbackReading{}, fmt.Errorf("decode pull request %d: %w", number, err)
	}
	reading.State = strings.ToUpper(strings.TrimSpace(head.State))
	if head.Merged {
		reading.State = "MERGED"
	}
	reading.Opener = strings.TrimSpace(head.Opener)

	type entry struct {
		ID        int64  `json:"id"`
		Author    string `json:"author"`
		State     string `json:"state"`
		Body      string `json:"body"`
		Submitted string `json:"submitted_at"`
		Created   string `json:"created_at"`
		Commit    string `json:"commit_id"`
		Path      string `json:"path"`
		Line      *int   `json:"line"`
		Original  *int   `json:"original_line"`
		URL       string `json:"html_url"`
	}
	read := func(what, endpoint, fields string) ([]entry, error) {
		result, err := g.apiQuery(ctx, "GET", endpoint, "--paginate", "--jq", ".[] | "+fields)
		if err != nil {
			return nil, fmt.Errorf("ask the forge for the %s on pull request %d: %w", what, number, err)
		}
		if result.Status != execution.ProcessSucceeded {
			return nil, g.forgeFailure(fmt.Sprintf("ask the forge for the %s on pull request %d", what, number), result)
		}
		var entries []entry
		lines := bufio.NewScanner(strings.NewReader(result.Stdout))
		lines.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for lines.Scan() {
			line := strings.TrimSpace(lines.Text())
			if line == "" {
				continue
			}
			var decoded entry
			if err := json.Unmarshal([]byte(line), &decoded); err != nil {
				return nil, fmt.Errorf("decode the %s on pull request %d: %w", what, number, err)
			}
			entries = append(entries, decoded)
		}
		if err := lines.Err(); err != nil {
			return nil, fmt.Errorf("read the %s on pull request %d: %w", what, number, err)
		}
		return entries, nil
	}
	stamp := func(values ...string) time.Time {
		for _, value := range values {
			if at, err := time.Parse(time.RFC3339, strings.TrimSpace(value)); err == nil {
				return at.UTC()
			}
		}
		return time.Time{}
	}
	page := "?per_page=100"
	reviews, err := read("reviews", fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/reviews%s", number, page),
		"{id, author: .user.login, state, body, submitted_at, commit_id, html_url}")
	if err != nil {
		return FeedbackReading{}, err
	}
	for _, review := range reviews {
		reading.Reviews = append(reading.Reviews, FeedbackReview{
			ID: review.ID, Author: strings.TrimSpace(review.Author), State: strings.ToUpper(strings.TrimSpace(review.State)),
			Body: review.Body, At: stamp(review.Submitted), Commit: strings.TrimSpace(review.Commit), URL: strings.TrimSpace(review.URL),
		})
	}
	lineComments, err := read("comments on lines", fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/comments%s", number, page),
		"{id, author: .user.login, body, created_at, path, line, original_line, html_url}")
	if err != nil {
		return FeedbackReading{}, err
	}
	for _, comment := range lineComments {
		line := 0
		switch {
		case comment.Line != nil:
			line = *comment.Line
		case comment.Original != nil:
			line = *comment.Original
		}
		reading.LineComments = append(reading.LineComments, FeedbackComment{
			ID: comment.ID, Author: strings.TrimSpace(comment.Author), Body: comment.Body, At: stamp(comment.Created),
			Path: strings.TrimSpace(comment.Path), Line: max(line, 0), URL: strings.TrimSpace(comment.URL),
		})
	}
	comments, err := read("comments", fmt.Sprintf("repos/{owner}/{repo}/issues/%d/comments%s", number, page),
		"{id, author: .user.login, body, created_at, html_url}")
	if err != nil {
		return FeedbackReading{}, err
	}
	for _, comment := range comments {
		reading.Comments = append(reading.Comments, FeedbackComment{
			ID: comment.ID, Author: strings.TrimSpace(comment.Author), Body: comment.Body, At: stamp(comment.Created),
			URL: strings.TrimSpace(comment.URL),
		})
	}
	return reading, nil
}
