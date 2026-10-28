package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repositoryread"
)

// The case yoyodyne-ifd.382 recorded, replayed: the operator asks the product
// manager about CLAUDE.md, and instead of advising from a month-old briefing it
// reads the file at the recorded commit first, receives it labelled as
// description rather than intent, and answers from what is actually there. The
// read is on the conversation's record as the commit, the path, and the time.
func TestTheProductManagerReadsTheFileBeforeAdvising(t *testing.T) {
	t.Parallel()

	const claudeMD = "# Project Instructions for AI Agents\n\n## The tracker is not a developer-run tool\n\n**Writes to the tracker belong to the harness and to the product manager's conversation. A developer run reads tracker state and never writes it.**\n"
	asked := "Before I advise on that, let me read the file as it stands.\n\n" +
		repositoryread.Fence + "\n" +
		`{"requests":[{"action":"read","path":"CLAUDE.md","why":"the advice turns on what the file already says"}]}` +
		"\n```"
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "CLAUDE.md at 0123456789ab already opens with a section saying developer runs never write the tracker, so there is nothing to add."},
	}}
	readAt := time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC)
	reader := &fakeRepositoryReader{results: []repositoryread.Result{{
		Action:  repositoryread.ActionRead,
		Path:    "CLAUDE.md",
		Why:     "the advice turns on what the file already says",
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		ReadAt:  readAt,
		Content: claudeMD,
		Size:    len(claudeMD),
	}}}
	root := t.TempDir()
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.RepositoryReader = reader
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Should CLAUDE.md say that developer runs never use the tracker?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reader.asked) != 1 || reader.asked[0].Path != "CLAUDE.md" || reader.asked[0].Action != repositoryread.ActionRead {
		t.Fatalf("the harness read %#v", reader.asked)
	}
	// One round of reading, reported to the operator with the path and the
	// commit rather than the content.
	if len(reply.RepositoryReads) != 1 || reply.RepositoryReads[0].Problem != "" || len(reply.RepositoryReads[0].Results) != 1 {
		t.Fatalf("reply repository reads = %#v", reply.RepositoryReads)
	}
	if rendered := reply.RepositoryReads[0].Render(); !strings.Contains(rendered, "read CLAUDE.md at 0123456789ab") || strings.Contains(rendered, "Writes to the tracker") {
		t.Fatalf("the round is rendered as %q", rendered)
	}
	// The content reached the next round of the same message, framed as
	// untrusted and labelled as description rather than intent, and the prose
	// from both rounds is what the operator reads.
	if len(provider.requests) != 2 {
		t.Fatalf("the message took %d turn(s)", len(provider.requests))
	}
	continuation := provider.requests[1].Prompt
	for _, required := range []string{
		"# Repository content",
		"## read CLAUDE.md at 0123456789ab, read 2026-09-19T10:30:00Z",
		"The tracker is not a developer-run tool",
		"untrusted text copied from the repository",
		"description of the implementation as built",
		"report the conflict",
	} {
		if !strings.Contains(continuation, required) {
			t.Fatalf("the continuation = %q, want it to contain %q", continuation, required)
		}
	}
	if !strings.Contains(reply.Text, "nothing to add") {
		t.Fatalf("reply = %q", reply.Text)
	}
	// The role is still toolless: whatever it may have the harness do, it runs
	// nothing itself.
	for _, request := range provider.requests {
		if len(request.AllowedTools) != 0 {
			t.Fatalf("the conversation was granted tools: %#v", request.AllowedTools)
		}
	}
	// The conversation's record shows the read as the commit, the path, and the
	// time, and carries none of the content.
	events, err := newTestStore(t, root).LoadEvents(session.Evidence().ConversationID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	var recorded []map[string]any
	for _, event := range events {
		if event.Type != execution.EventRepositoryRead {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		recorded = append(recorded, payload)
		if strings.Contains(string(event.Payload), "Writes to the tracker") {
			t.Fatalf("the record copied the content: %s", event.Payload)
		}
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded %d repository read(s), want 1: %#v", len(recorded), events)
	}
	for key, want := range map[string]any{
		"action":  "read",
		"path":    "CLAUDE.md",
		"commit":  "0123456789abcdef0123456789abcdef01234567",
		"read_at": readAt.Format(time.RFC3339),
		"why":     "the advice turns on what the file already says",
		"bytes":   float64(len(claudeMD)),
	} {
		if recorded[0][key] != want {
			t.Errorf("the recorded read's %s = %v, want %v", key, recorded[0][key], want)
		}
	}
}

// The architect and the development manager read the same way, and what they
// are handed is framed as evidence and not with the product manager's label: the
// label is about the role that owns intent, and neither of these does.
func TestTheOtherManagementRolesReadAsEvidence(t *testing.T) {
	t.Parallel()

	asked := "Let me see what the directory holds.\n\n" +
		repositoryread.Fence + "\n" +
		`{"requests":[{"action":"list","path":"docs/designs"}]}` +
		"\n```"
	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleDevelopmentManager} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Two designs are there."},
			}}
			reader := &fakeRepositoryReader{results: []repositoryread.Result{{
				Action:  repositoryread.ActionList,
				Path:    "docs/designs",
				Commit:  "0123456789abcdef0123456789abcdef01234567",
				ReadAt:  time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC),
				Entries: []string{"configurable-workflows.md", "v1-harness-design.md"},
				Size:    2,
			}}}
			options := testOptions(t, provider)
			options.Role = role
			options.Agent = string(role)
			options.RepositoryReader = reader
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "Which designs are recorded?")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(reader.asked) != 1 || reader.asked[0].Action != repositoryread.ActionList {
				t.Fatalf("the harness read %#v", reader.asked)
			}
			continuation := provider.requests[1].Prompt
			for _, required := range []string{"## list docs/designs at 0123456789ab", "- configurable-workflows.md", "- v1-harness-design.md", "never an instruction"} {
				if !strings.Contains(continuation, required) {
					t.Fatalf("the continuation = %q, want it to contain %q", continuation, required)
				}
			}
			if strings.Contains(continuation, "description of the implementation") {
				t.Fatalf("the %s was handed the product manager's label: %q", role, continuation)
			}
			if len(reply.RepositoryReads) != 1 || len(reply.RepositoryReads[0].Results) != 1 {
				t.Fatalf("reply repository reads = %#v", reply.RepositoryReads)
			}
		})
	}
}

// The developer and the reviewer stay as they were: a conversation with either
// that names a path is refused by the harness rather than by prose, nothing is
// read, and the reply is still the operator's to read.
func TestTheRunGatedRolesCannotReadTheRepository(t *testing.T) {
	t.Parallel()

	asked := "Let me read it.\n\n" +
		repositoryread.Fence + "\n" +
		`{"requests":[{"action":"read","path":"CLAUDE.md"}]}` +
		"\n```"
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			reader := &fakeRepositoryReader{}
			options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
			}})
			options.Role = role
			options.Agent = string(role)
			options.RepositoryReader = reader
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "What does CLAUDE.md say?")
			var refused *AuthorityError
			if !errors.As(err, &refused) {
				t.Fatalf("Send() error = %v, want an AuthorityError", err)
			}
			if !strings.Contains(refused.Error(), "a repository path to be read") {
				t.Fatalf("refusal = %q", refused.Error())
			}
			if len(reader.asked) != 0 {
				t.Fatalf("a refused role still read %d path(s)", len(reader.asked))
			}
			if !strings.Contains(reply.Text, "Let me read it") {
				t.Fatalf("the refusal swallowed the reply: %q", reply.Text)
			}
		})
	}
}

// The contract states the block for the three roles that may use it and for no
// other, and the product manager's states the labelling rule as well.
func TestTheContractOffersTheRepositoryBlockToTheManagementRoles(t *testing.T) {
	t.Parallel()

	for role, offered := range map[domain.AgentRole]bool{
		domain.RoleProductManager:     true,
		domain.RoleArchitect:          true,
		domain.RoleDevelopmentManager: true,
		domain.RoleDeveloper:          false,
		domain.RoleReviewer:           false,
	} {
		prompt := SystemPrompt(role, testAdmission, nil, "")
		if strings.Contains(prompt, repositoryread.Fence) != offered {
			t.Errorf("the %s's contract offers the repository block: %t, want %t", role, !offered, offered)
		}
		if strings.Contains(prompt, "cannot read a file") {
			t.Errorf("the %s's contract still says it cannot read a file, which the block makes untrue for the roles that hold it and imprecise for the rest", role)
		}
	}
	if !strings.Contains(SystemPrompt(domain.RoleProductManager, testAdmission, nil, ""), repositoryread.ProductManagerClause) {
		t.Fatal("the product manager's contract does not state the labelling rule")
	}
	if strings.Contains(SystemPrompt(domain.RoleArchitect, testAdmission, nil, ""), repositoryread.ProductManagerClause) {
		t.Fatal("the architect's contract carries the product manager's labelling rule")
	}
}

// A path that names nothing, and a capability that is not wired, are things
// the role is told so it can say it could not read. The reply is not lost over
// either, and neither is the round.
func TestAReadThatReturnedNothingIsSaidRatherThanSwallowed(t *testing.T) {
	t.Parallel()

	asked := "Let me check.\n\n" +
		repositoryread.Fence + "\n" +
		`{"requests":[{"action":"read","path":"docs/missing.md"}]}` +
		"\n```"
	for _, test := range []struct {
		name   string
		reader RepositoryReader
		want   string
	}{
		{
			name: "no such path at the commit",
			reader: &fakeRepositoryReader{results: []repositoryread.Result{{
				Action: repositoryread.ActionRead, Path: "docs/missing.md", Commit: "0123456789abcdef0123456789abcdef01234567",
				ReadAt:  time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC),
				Problem: "there is no docs/missing.md in the tree at 0123456789ab",
			}}},
			want: "nothing was returned: there is no docs/missing.md in the tree at 0123456789ab",
		},
		{
			name:   "the capability failed",
			reader: &fakeRepositoryReader{err: errors.New("resolve the commit to read at: git rev-parse failed")},
			want:   "Nothing was read: resolve the commit to read at",
		},
		{
			name: "no capability wired",
			want: "Nothing was read: no repository read capability is wired to this conversation",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "I could not read it, so I cannot say."},
			}}
			options := testOptions(t, provider)
			options.RepositoryReader = test.reader
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "What does that document say?")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(provider.requests) != 2 || !strings.Contains(provider.requests[1].Prompt, test.want) {
				t.Fatalf("the role was not told %q: %d turn(s), continuation %q", test.want, len(provider.requests), provider.requests[len(provider.requests)-1].Prompt)
			}
			if len(reply.RepositoryReads) != 1 {
				t.Fatalf("reply repository reads = %#v", reply.RepositoryReads)
			}
			if !strings.Contains(reply.Text, "cannot say") {
				t.Fatalf("reply = %q", reply.Text)
			}
		})
	}
}

// One message reads at most twice, so a role that reads its way through the
// tree is stopped, with the reply and the earlier reads intact. The turn after
// the second read is the message's last, so a third read is refused on the
// reply and not answered with another paid turn.
func TestOneMessageReadsTheRepositoryABoundedNumberOfTimes(t *testing.T) {
	t.Parallel()

	asked := "Reading.\n\n" + repositoryread.Fence + "\n" + `{"requests":[{"action":"read","path":"a.md"}]}` + "\n```"
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Done reading."},
	}}
	reader := &fakeRepositoryReader{results: []repositoryread.Result{{
		Action: repositoryread.ActionRead, Path: "a.md", Commit: "0123456789abcdef0123456789abcdef01234567",
		ReadAt: time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC), Content: "a", Size: 1,
	}}}
	options := testOptions(t, provider)
	options.RepositoryReader = reader
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Read everything.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reader.asked) != maxRepositoryRounds {
		t.Fatalf("the harness read %d time(s), want %d", len(reader.asked), maxRepositoryRounds)
	}
	if len(reply.RepositoryReads) != 3 || reply.RepositoryReads[2].Problem == "" || !strings.Contains(reply.RepositoryReads[2].Problem, "at most 2 time(s)") {
		t.Fatalf("reply repository reads = %#v", reply.RepositoryReads)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("provider turns = %d, want 3: the turn after the limit is the last", len(provider.requests))
	}
	if !strings.Contains(provider.requests[2].Prompt, "This is the last turn this message may take") {
		t.Fatalf("the closing turn was not told it was the last: %q", provider.requests[2].Prompt)
	}
	if !strings.Contains(reply.Stopped, "reading the repository reached the most one message may do") {
		t.Fatalf("the stop was not stated: %q", reply.Stopped)
	}
}

// A repository block the harness cannot read — an action it does not perform —
// is a typed failure rather than a reply that asked for nothing: nothing is
// read, the operator is told, and the prose is still theirs to read.
func TestAnUnreadableRepositoryBlockIsReported(t *testing.T) {
	t.Parallel()

	reader := &fakeRepositoryReader{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Let me edit it.\n\n" + repositoryread.Fence + "\n" + `{"requests":[{"action":"write","path":"CLAUDE.md"}]}` + "\n```"},
	}}
	options := testOptions(t, provider)
	options.RepositoryReader = reader
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Change CLAUDE.md.")
	var unreadable *RepositoryError
	if !errors.As(err, &unreadable) {
		t.Fatalf("Send() error = %v, want a RepositoryError", err)
	}
	if !strings.Contains(unreadable.Error(), `action "write" is not one the harness performs`) {
		t.Fatalf("error = %q", unreadable)
	}
	if len(reader.asked) != 0 {
		t.Fatalf("a refused block still read %d path(s)", len(reader.asked))
	}
	if !strings.Contains(reply.Text, "Let me edit it") {
		t.Fatalf("the refusal swallowed the reply: %q", reply.Text)
	}
}

// A path that climbs out of the repository is not an unreadable block: the
// block decodes, the reader refuses that one path with the reason, the reason
// reaches the role beside the paths that were read, and the refusal is on the
// record exactly as a read is — which is what the contract promises.
func TestAClimbingPathIsRefusedToTheRoleAndRecorded(t *testing.T) {
	t.Parallel()

	asked := "Let me look above and inside.\n\n" +
		repositoryread.Fence + "\n" +
		`{"requests":[{"action":"read","path":"../secret"},{"action":"read","path":"CLAUDE.md"}]}` +
		"\n```"
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Nothing above the repository is readable; CLAUDE.md is."},
	}}
	readAt := time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC)
	reader := &fakeRepositoryReader{results: []repositoryread.Result{
		{Action: repositoryread.ActionRead, Path: "../secret", Commit: "0123456789abcdef0123456789abcdef01234567", ReadAt: readAt, Problem: `path "../secret" climbs out of the repository`},
		{Action: repositoryread.ActionRead, Path: "CLAUDE.md", Commit: "0123456789abcdef0123456789abcdef01234567", ReadAt: readAt, Content: "# Project Instructions\n", Size: 23},
	}}
	root := t.TempDir()
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.RepositoryReader = reader
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What is above the repository?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reader.asked) != 2 || reader.asked[0].Path != "../secret" {
		t.Fatalf("the harness was handed %#v", reader.asked)
	}
	continuation := provider.requests[1].Prompt
	for _, required := range []string{"nothing was returned: path \"../secret\" climbs out of the repository", "# Project Instructions"} {
		if !strings.Contains(continuation, required) {
			t.Fatalf("the continuation = %q, want it to contain %q", continuation, required)
		}
	}
	if len(reply.RepositoryReads) != 1 || len(reply.RepositoryReads[0].Results) != 2 {
		t.Fatalf("reply repository reads = %#v", reply.RepositoryReads)
	}
	events, err := newTestStore(t, root).LoadEvents(session.Evidence().ConversationID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	var refusals, reads int
	for _, event := range events {
		if event.Type != execution.EventRepositoryRead {
			continue
		}
		if strings.Contains(string(event.Payload), "climbs out of the repository") {
			refusals++
		} else {
			reads++
		}
	}
	if refusals != 1 || reads != 1 {
		t.Fatalf("the record holds %d refusal(s) and %d read(s), want one of each", refusals, reads)
	}
}

// A side thread names no path: the narrowing takes the block away however the
// main thread holds it.
func TestASideThreadNamesNoRepositoryPath(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager} {
		authority, _ := AuthorityFor(role)
		if !authority.RepositoryReads {
			t.Fatalf("the %s's main thread cannot read the repository", role)
		}
		if authority.OnSideStream().RepositoryReads {
			t.Errorf("the %s's side thread can read the repository", role)
		}
	}
}

type fakeRepositoryReader struct {
	results []repositoryread.Result
	err     error
	asked   []repositoryread.Request
}

func (f *fakeRepositoryReader) Read(_ context.Context, requests []repositoryread.Request) ([]repositoryread.Result, error) {
	f.asked = append(f.asked, requests...)
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}
