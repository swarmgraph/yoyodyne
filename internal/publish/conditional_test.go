package publish

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// memoryResponses is a ResponseCache held in memory.
type memoryResponses struct {
	tags   map[string]string
	bodies map[string]string
}

func (m *memoryResponses) Cached(key string) (string, string, bool) {
	tag, found := m.tags[key]
	return tag, m.bodies[key], found
}

func (m *memoryResponses) Keep(key, etag, body string) {
	if m.tags == nil {
		m.tags, m.bodies = map[string]string{}, map[string]string{}
	}
	m.tags[key], m.bodies[key] = etag, body
}

// included is what `gh api --include` prints for a 200: the status line, the
// headers, a blank line, and the body.
func included(etag, body string, extra ...string) execution.ProcessResult {
	headers := []string{"HTTP/2.0 200 OK", "Content-Type: application/json", "Etag: " + etag}
	headers = append(headers, extra...)
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: strings.Join(headers, "\r\n") + "\r\n\r\n" + body}
}

// notModified is what it prints for a 304: the status line and headers, no
// body, and gh's own refusal on standard error with a failed exit.
func notModified(etag string) execution.ProcessResult {
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1,
		Stdout: "HTTP/2.0 304 Not Modified\r\nEtag: " + etag + "\r\n\r\n", Stderr: "gh: HTTP 304"}
}

func conditionalFeedbackRunner() *scriptedRunner {
	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pulls/7 --include", included(`W/"pr-1"`, `{"state":"open","merged":false,"opener":"factory-bot"}`+"\n"))
	runner.reply("pulls/7/reviews", included(`W/"reviews-1"`,
		`{"id":11,"author":"ada","state":"CHANGES_REQUESTED","body":"Please rename the flag.","submitted_at":"2026-11-02T10:00:00Z","commit_id":"abc","html_url":"https://example.invalid/r/11"}`+"\n"))
	runner.reply("pulls/7/comments", included(`W/"lines-1"`, ""))
	runner.reply("issues/7/comments", included(`W/"comments-1"`, ""))
	return runner
}

// The first conditional reading is asked in full and kept with the forge's
// tags; the next one carries them, and where the forge says nothing has
// changed the kept answer is the reading, with nothing fetched again in full.
func TestAnUnchangedAnswerIsNotFetchedAgainInFull(t *testing.T) {
	t.Parallel()
	runner := conditionalFeedbackRunner()
	kept := &memoryResponses{}
	forge := GitHub{Runner: runner, Binary: "gh", GitBinary: "git", Responses: kept}

	first, err := forge.Feedback(context.Background(), 7)
	if err != nil {
		t.Fatalf("Feedback() error = %v", err)
	}
	if first.Unchanged || first.State != "OPEN" || len(first.Reviews) != 1 || first.Reviews[0].Author != "ada" {
		t.Fatalf("first reading = %+v, want the request read in full", first)
	}
	if len(kept.tags) != 4 {
		t.Fatalf("kept %d answers, want all four reads kept with their tags", len(kept.tags))
	}
	for _, command := range runner.commands {
		if joined := strings.Join(command, " "); strings.Contains(joined, "If-None-Match") || strings.Contains(joined, "--paginate") {
			t.Fatalf("first reading asked %v, want a plain read carrying no tag and no paging", command)
		}
	}

	runner.reply("pulls/7 --include", notModified(`W/"pr-1"`))
	runner.reply("pulls/7/reviews", notModified(`W/"reviews-1"`))
	runner.reply("pulls/7/comments", notModified(`W/"lines-1"`))
	runner.reply("issues/7/comments", notModified(`W/"comments-1"`))
	asked := len(runner.commands)
	second, err := forge.Feedback(context.Background(), 7)
	if err != nil {
		t.Fatalf("second Feedback() error = %v", err)
	}
	if !second.Unchanged || second.State != "OPEN" || len(second.Reviews) != 1 || second.Reviews[0].Body != "Please rename the flag." {
		t.Fatalf("second reading = %+v, want the kept answer, said unchanged", second)
	}
	for _, command := range runner.commands[asked:] {
		joined := strings.Join(command, " ")
		if strings.Contains(joined, "remote get-url") {
			continue
		}
		if !strings.Contains(joined, "If-None-Match: W/\"") || strings.Contains(joined, "--paginate") {
			t.Fatalf("second reading asked %q, want every read conditional on its kept tag", joined)
		}
	}
}

// A changed answer is read and kept in place of the old one, with its new tag,
// and the reading is not said unchanged.
func TestAChangedAnswerIsReadAndKeptInPlaceOfTheOld(t *testing.T) {
	t.Parallel()
	runner := conditionalFeedbackRunner()
	kept := &memoryResponses{}
	forge := GitHub{Runner: runner, Binary: "gh", GitBinary: "git", Responses: kept}
	if _, err := forge.Feedback(context.Background(), 7); err != nil {
		t.Fatalf("Feedback() error = %v", err)
	}

	runner.reply("pulls/7 --include", notModified(`W/"pr-1"`))
	runner.reply("pulls/7/comments", notModified(`W/"lines-1"`))
	runner.reply("issues/7/comments", included(`W/"comments-2"`,
		`{"id":31,"author":"linus","body":"Why this approach?","created_at":"2026-11-02T12:00:00Z","html_url":"https://example.invalid/c/31"}`+"\n"))
	runner.reply("pulls/7/reviews", notModified(`W/"reviews-1"`))
	reading, err := forge.Feedback(context.Background(), 7)
	if err != nil {
		t.Fatalf("second Feedback() error = %v", err)
	}
	if reading.Unchanged || len(reading.Comments) != 1 || reading.Comments[0].Author != "linus" || len(reading.Reviews) != 1 {
		t.Fatalf("reading = %+v, want the new comment read beside the kept review", reading)
	}
	var tag string
	for key, kept := range kept.tags {
		if strings.HasPrefix(key, "repos/{owner}/{repo}/issues/7/comments") {
			tag = kept
		}
	}
	if tag != `W/"comments-2"` {
		t.Fatalf("kept tag = %q, want the changed answer's own", tag)
	}
}

// A listing with more than one page is read again in full, every page, and
// not kept: a later page can change while the first does not.
func TestAListingOfMoreThanOnePageIsReadInFullAndNotKept(t *testing.T) {
	t.Parallel()
	runner := conditionalFeedbackRunner()
	delete(runner.replies, "pulls/7/reviews")
	runner.reply("reviews?per_page=100 --include", included(`W/"reviews-1"`, `{"id":11,"author":"ada","state":"APPROVED","body":"","submitted_at":"2026-11-02T10:00:00Z"}`+"\n",
		`Link: <https://api.github.com/repositories/1/pulls/7/reviews?page=2>; rel="next"`))
	runner.reply("reviews?per_page=100 --paginate", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: strings.Join([]string{
		`{"id":11,"author":"ada","state":"APPROVED","body":"","submitted_at":"2026-11-02T10:00:00Z"}`,
		`{"id":12,"author":"grace","state":"CHANGES_REQUESTED","body":"On page two.","submitted_at":"2026-11-02T11:00:00Z"}`,
	}, "\n") + "\n"})
	kept := &memoryResponses{}
	reading, err := GitHub{Runner: runner, Binary: "gh", GitBinary: "git", Responses: kept}.Feedback(context.Background(), 7)
	if err != nil {
		t.Fatalf("Feedback() error = %v", err)
	}
	if len(reading.Reviews) != 2 || reading.Reviews[1].Author != "grace" {
		t.Fatalf("reviews = %+v, want both pages", reading.Reviews)
	}
	for key := range kept.tags {
		if strings.Contains(key, "/reviews") {
			t.Fatalf("kept %q, want a listing of more than one page left unkept", key)
		}
	}
}

// A forge that cannot be reached is a failed read, and nothing kept is
// replaced.
func TestAnUnreachableForgeIsAFailedConditionalReadAndKeepsWhatWasKept(t *testing.T) {
	t.Parallel()
	runner := conditionalFeedbackRunner()
	kept := &memoryResponses{}
	forge := GitHub{Runner: runner, Binary: "gh", GitBinary: "git", Responses: kept}
	if _, err := forge.Feedback(context.Background(), 7); err != nil {
		t.Fatalf("Feedback() error = %v", err)
	}
	before := map[string]string{}
	for key, tag := range kept.tags {
		before[key] = tag
	}
	runner.reply("pulls/7 --include", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1,
		Stderr: `Get "https://api.github.com/repos/acme/thing/pulls/7": dial tcp: lookup api.github.com: no such host`})
	if _, err := forge.Feedback(context.Background(), 7); err == nil || !strings.Contains(err.Error(), "no such host") {
		t.Fatalf("Feedback() error = %v, want the forge's own failure", err)
	}
	for key, tag := range before {
		if kept.tags[key] != tag {
			t.Fatalf("kept %q = %q, want %q left as it was", key, kept.tags[key], tag)
		}
	}
}

// How far the target has moved past a head is asked conditionally too, and an
// unchanged comparison is the kept distance.
func TestBehindIsAskedConditionally(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("compare/"+head+"...main", included(`W/"cmp-1"`, `{"ahead_by":3}`+"\n"))
	kept := &memoryResponses{}
	forge := GitHub{Runner: runner, Binary: "gh", GitBinary: "git", Responses: kept}
	if behind, err := forge.Behind(context.Background(), head, "main"); err != nil || behind != 3 {
		t.Fatalf("Behind() = %d, %v, want 3", behind, err)
	}
	runner.reply("compare/"+head+"...main", notModified(`W/"cmp-1"`))
	if behind, err := forge.Behind(context.Background(), head, "main"); err != nil || behind != 3 {
		t.Fatalf("second Behind() = %d, %v, want the kept 3", behind, err)
	}
	last := strings.Join(runner.commands[len(runner.commands)-1], " ")
	if !strings.Contains(last, `If-None-Match: W/"cmp-1"`) {
		t.Fatalf("second comparison asked %q, want it conditional on the kept tag", last)
	}
}
