package publish

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Conditional requests.
//
// The forge answers a GET with an ETag naming the answer it gave, and answers
// the same request carrying that tag in If-None-Match with 304 Not Modified and
// no body when nothing has changed; GitHub does not count such an answer against
// the token's rate limit. The forge check asks every few minutes about pull
// requests that mostly have not moved, so it asks this way, and an unchanged
// answer costs a status line rather than the whole answer again.
//
// Only REST reads can be asked this way: GitHub's GraphQL API has no
// conditional requests, so the batched query for the state of every waiting
// pull request (States) is asked in full, once for up to MaxStatesPerQuery
// requests.

// ResponseCache keeps the forge's last answer to each read asked conditionally,
// with the tag the forge gave it. A cache that loses an answer costs one full
// read and nothing else.
type ResponseCache interface {
	Cached(key string) (etag, body string, found bool)
	Keep(key, etag, body string)
}

// conditionalAnswer is one read asked conditionally.
type conditionalAnswer struct {
	// Body is the answer, after the read's own selection, and the kept one where
	// the forge said nothing has changed.
	Body string
	// Unchanged is the forge answering 304: Body is what was kept from before.
	Unchanged bool
	// Paged is the forge saying the listing has more pages than the one read.
	// Body is then the first page alone, and nothing is kept: a later page can
	// change while the first does not, so a tag on the first says nothing about
	// the whole.
	Paged bool
}

// conditionalRead asks one GET of the forge's REST API, carrying the tag of the
// answer kept for it where there is one. A client with no ResponseCache asks in
// full, as every read did before conditional requests.
func (g GitHub) conditionalRead(ctx context.Context, endpoint, selection string) (conditionalAnswer, error) {
	options := []string{"--include"}
	if selection != "" {
		options = append(options, "--jq", selection)
	}
	key := endpoint + " | " + selection
	var keptTag, keptBody string
	var kept bool
	if g.Responses != nil {
		keptTag, keptBody, kept = g.Responses.Cached(key)
		if kept && strings.TrimSpace(keptTag) != "" {
			options = append(options, "--header", "If-None-Match: "+keptTag)
		}
	}
	result, err := g.apiQuery(ctx, "GET", endpoint, options...)
	if err != nil {
		return conditionalAnswer{}, err
	}
	status, headers, body := splitIncluded(result.Stdout)
	if status == 304 || (status == 0 && strings.Contains(result.Stderr, "HTTP 304")) {
		if !kept {
			return conditionalAnswer{}, fmt.Errorf("the forge said nothing has changed at %s, and no earlier answer is kept", endpoint)
		}
		return conditionalAnswer{Body: keptBody, Unchanged: true}, nil
	}
	if result.Status != execution.ProcessSucceeded {
		return conditionalAnswer{}, g.forgeFailure("ask the forge for "+endpoint, result)
	}
	if result.OutputTruncation != "" {
		return conditionalAnswer{}, fmt.Errorf("the forge's answer at %s was cut and is not read: %s", endpoint, result.OutputTruncation)
	}
	if status != 0 && (status < 200 || status > 299) {
		return conditionalAnswer{}, fmt.Errorf("the forge answered %d at %s", status, endpoint)
	}
	answer := conditionalAnswer{Body: body, Paged: strings.Contains(headers["link"], `rel="next"`)}
	if tag := strings.TrimSpace(headers["etag"]); g.Responses != nil && tag != "" && !answer.Paged && len(body) <= MaxKeptResponseBytes {
		g.Responses.Keep(key, tag, body)
	}
	return answer, nil
}

// MaxKeptResponseBytes bounds one answer a ResponseCache is given to keep. An
// answer larger than this is asked for in full each time.
const MaxKeptResponseBytes = 256 << 10

// splitIncluded separates what `gh api --include` printed into the status, the
// headers by lower-cased name, and the body. Output that does not begin with a
// status line is all body, with status zero.
func splitIncluded(stdout string) (int, map[string]string, string) {
	headers := map[string]string{}
	text := strings.ReplaceAll(stdout, "\r\n", "\n")
	if !strings.HasPrefix(text, "HTTP/") {
		return 0, headers, stdout
	}
	head, body, _ := strings.Cut(text, "\n\n")
	lines := strings.Split(head, "\n")
	status := 0
	if fields := strings.Fields(lines[0]); len(fields) >= 2 {
		status, _ = strconv.Atoi(fields[1])
	}
	for _, line := range lines[1:] {
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if earlier, seen := headers[name]; seen {
			value = earlier + ", " + strings.TrimSpace(value)
		}
		headers[name] = strings.TrimSpace(value)
	}
	return status, headers, body
}
