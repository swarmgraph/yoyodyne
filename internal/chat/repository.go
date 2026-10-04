package chat

// Reading the repository at a recorded commit, on a management role's behalf.
//
// This works even for a backend that supplies no inspection tools. As with
// the work tracker and research: it names what it wants, the
// harness performs it, records it, tells the operator, and hands back what came
// of it. Nothing here lets the role choose what runs or where a path resolves —
// every path is resolved by the harness's own Git inside the tree of one commit,
// which is what keeps a capability that reaches the repository from being a
// tool the role holds.
//
// What comes back is untrusted and is delivered as such: a file is prose
// arriving inside a prompt, so it is framed as evidence of what the repository
// holds and never as instruction. For the product manager it carries one label
// more — description of the implementation, never intent — because that is the
// role that owns intent, and a file that says how the product works must not be
// able to argue about what it is for.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repositoryread"
)

// RepositoryReader is the bounded repository-read capability a conversation
// performs on a role's behalf. It is satisfied by repositoryread.Reader.
type RepositoryReader interface {
	Read(ctx context.Context, requests []repositoryread.Request) ([]repositoryread.Result, error)
}

// maxRepositoryRounds bounds how many times one operator message may be answered
// with a repository read. Reading a file, seeing what it names, and reading that
// is the point of the capability; a conversation that spends a message reading
// its way through the tree is not, and every round is a provider turn.
const maxRepositoryRounds = 2

// RepositoryError reports that a turn carried a repository block the harness
// could not read. Like the research error it is not a broken conversation: the
// turn completed, the answer is real, and nothing was read. What is lost is
// whatever that block was trying to ask for, which is why it is never silently
// treated as a reply that asked for nothing.
type RepositoryError struct {
	Err error
}

func (e *RepositoryError) Error() string {
	return "the reply asked for a repository read the harness cannot read: " + e.Err.Error()
}

func (e *RepositoryError) Unwrap() error { return e.Err }

// repositoryFraming is how what was read is labelled for this conversation's
// role: the product manager is told it is description and never intent, and
// every other management role is told it is evidence.
func (s *Session) repositoryFraming() repositoryread.Framing {
	if s.state.Role == domain.RoleProductManager {
		return repositoryread.AsDescription
	}
	return repositoryread.AsEvidence
}

// performRepositoryReads resolves one round of paths against the recorded
// commit, records each read on the conversation, and returns what to hand back
// to the role. Nothing here fails the turn: a capability that is not wired, a
// budget that is spent, and a path that names nothing are all things the role
// has to be told about so it can say it could not read, and none of them is a
// reason to lose the reply that carried the request.
func (s *Session) performRepositoryReadsWithoutToolAudit(ctx context.Context, requests []repositoryread.Request, rounds *int) ([]repositoryread.Result, string) {
	if s.options.RepositoryReader == nil {
		return nil, "no repository read capability is wired to this conversation, so nothing was read"
	}
	if *rounds >= maxRepositoryRounds {
		return nil, fmt.Sprintf("one message reads the repository at most %d time(s), and this one has; nothing further was read", maxRepositoryRounds)
	}
	*rounds++
	results, err := s.options.RepositoryReader.Read(ctx, requests)
	if err != nil {
		return nil, singleLine(err.Error(), maxTrackerFailureBytes)
	}
	// Each read is recorded on the conversation before the role sees it, as the
	// commit, the path, and the time, so the record says what the role's advice
	// was built from. Content is deliberately not in the record: the commit and
	// the path are what a reader needs to see it, and a log that copied every
	// file read would be a second repository nobody asked for. A read the
	// harness could not record is not handed back — what was read is still on
	// the reply for the operator, and the role is told why it has nothing —
	// because content the role reasons from and the record does not mention is
	// the exact gap the record exists to close.
	for _, result := range results {
		if err := s.emit(execution.EventRepositoryRead, repositoryReadPayload(result)); err != nil {
			return results, singleLine(fmt.Sprintf("the reads were made and could not be recorded on the conversation, so nothing was handed back: %v", err), maxTrackerFailureBytes)
		}
	}
	// A capability that answered with nothing at all is reported as nothing read
	// rather than delivered as an empty section, for the reason research is.
	if len(results) == 0 {
		return nil, "the repository read returned nothing for the paths named"
	}
	return results, ""
}

// repositoryReadPayload is what one read leaves on the conversation's log.
func repositoryReadPayload(result repositoryread.Result) map[string]any {
	payload := map[string]any{
		"action":  result.Action,
		"path":    result.Path,
		"commit":  result.Commit,
		"read_at": result.ReadAt,
	}
	if result.Why != "" {
		payload["why"] = result.Why
	}
	if result.Problem != "" {
		payload["problem"] = result.Problem
		return payload
	}
	payload["size"] = result.Size
	switch result.Action {
	case repositoryread.ActionList:
		payload["entries"] = len(result.Entries)
	default:
		payload["bytes"] = len(result.Content)
	}
	if result.Truncated {
		payload["truncated"] = true
		payload["truncated_by"] = result.TruncatedBy
	}
	return payload
}

// RepositoryRound is what one round of reading the repository did, for the
// operator reading what a reply looked at. It is reported rather than put to
// them: it already happened, exactly as a tracker action did.
type RepositoryRound struct {
	// Results are what came back, one per path named, in the order they were
	// named. A path that produced nothing is here too, saying why.
	Results []repositoryread.Result `json:"results,omitempty"`
	// Problem is why this round read nothing at all: no capability, a budget
	// already spent, or a repository whose commit could not be resolved.
	Problem string `json:"problem,omitempty"`
}

// Render describes one round of reads for an operator reading what happened. It
// names what was read and at which commit rather than repeating the content: the
// content is in the reply they just read, and a page of a file under it would
// bury the answer in its own sources.
func (r RepositoryRound) Render() string {
	var rendered strings.Builder
	if r.Problem != "" {
		rendered.WriteString("[repository] nothing was read\n")
		rendered.WriteString(indent(r.Problem))
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "[repository] %d path(s) read at a recorded commit\n", len(r.Results))
	for _, result := range r.Results {
		rendered.WriteString(indent(result.Describe()))
	}
	return rendered.String()
}
