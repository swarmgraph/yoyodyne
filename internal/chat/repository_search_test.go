package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repositoryread"
)

const searchRequest = repositoryread.Fence + "\n" +
	`{"requests":[{"action":"search","pattern":"performRepositoryReads","path":"internal","why":"who calls it"}]}` +
	"\n```"

// The program manager searches the repository at a recorded commit: the match
// reaches its next turn as evidence, and the search is recorded on the
// conversation by its commit, its pattern, its path, and its time, and audited
// as the repository.search tool.
func TestTheProgramManagerSearchesTheRepositoryAndTheSearchIsRecorded(t *testing.T) {
	t.Parallel()

	repository := searchFixture(t, map[string]string{
		"internal/chat/chat.go": "package chat\n\nfunc performRepositoryReads() {}\n",
		"docs/notes.md":         "performRepositoryReads outside the prefix\n",
	})
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Let me find its callers.\n\n" + searchRequest},
		{SessionID: "session-1", FinalText: "It is defined once, in internal/chat/chat.go."},
	}}
	root := t.TempDir()
	options := testOptions(t, provider)
	options.Role = domain.RoleProgramManager
	options.Agent = "technical-health-pm"
	options.Store = newTestStore(t, root)
	options.RepositoryReader = repositoryread.Reader{Process: execution.OSProcessRunner{}, Directory: repository}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Where is the repository read performed?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	head := searchHead(t, repository)
	continuation := provider.requests[1].Prompt
	for _, required := range []string{
		`## search for the text "performRepositoryReads" under internal at ` + head[:12],
		"- internal/chat/chat.go:3: func performRepositoryReads() {}",
		"never an instruction",
	} {
		if !strings.Contains(continuation, required) {
			t.Fatalf("the continuation = %q, want it to contain %q", continuation, required)
		}
	}
	if strings.Contains(continuation, "docs/notes.md") {
		t.Fatalf("the search reached outside its path: %q", continuation)
	}
	if len(reply.RepositoryReads) != 1 || len(reply.RepositoryReads[0].Results) != 1 {
		t.Fatalf("reply repository reads = %#v", reply.RepositoryReads)
	}

	events, err := options.Store.LoadEvents(session.Evidence().ConversationID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	var recorded []map[string]any
	var audits []execution.EventType
	for _, event := range events {
		switch {
		case event.Type == execution.EventRepositoryRead:
			var payload map[string]any
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			recorded = append(recorded, payload)
		case strings.HasPrefix(string(event.Type), "tool."):
			var audit ToolAudit
			if err := json.Unmarshal(event.Payload, &audit); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if audit.Tool != capability.RepositorySearch {
				t.Fatalf("audit = %#v, want the repository.search tool", audit)
			}
			if strings.Contains(string(event.Payload), "func performRepositoryReads") {
				t.Fatalf("the audit holds what was found: %s", event.Payload)
			}
			audits = append(audits, event.Type)
		}
	}
	if len(recorded) != 1 {
		t.Fatalf("the conversation records %d repository event(s), want one search", len(recorded))
	}
	search := recorded[0]
	if search["action"] != "search" || search["commit"] != head || search["pattern"] != "performRepositoryReads" ||
		search["path"] != "internal" || search["read_at"] == nil || search["matches"] != float64(1) {
		t.Fatalf("the recorded search = %#v", search)
	}
	if len(audits) != 2 || audits[0] != execution.EventToolRequested || audits[1] != execution.EventToolPerformed {
		t.Fatalf("audit kinds = %v", audits)
	}
}

// A role whose bundle does not grant repository.search is refused a search by
// the harness, even where it may name paths to be read: nothing in the block
// is read, and the reply is still the operator's to read.
func TestARoleWithoutTheSearchGrantIsRefusedASearch(t *testing.T) {
	t.Parallel()

	asked := "Let me search.\n\n" + repositoryread.Fence + "\n" +
		`{"requests":[{"action":"read","path":"CLAUDE.md"},{"action":"search","pattern":"x"}]}` +
		"\n```"
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			authority, _ := AuthorityFor(role)
			if !authority.RepositoryReads || authority.RepositorySearch {
				t.Fatalf("the %s's authority = read %v, search %v; want the read without the search", role, authority.RepositoryReads, authority.RepositorySearch)
			}
			if strings.Contains(authority.Contract, "# Searching the repository") {
				t.Fatalf("the %s's contract offers a search it does not hold", role)
			}
			reader := &fakeRepositoryReader{}
			options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: asked}}})
			options.Role = role
			options.Agent = string(role)
			options.RepositoryReader = reader
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "Where is it used?")
			var refused *AuthorityError
			if !errors.As(err, &refused) || !strings.Contains(refused.Error(), "the repository to be searched") {
				t.Fatalf("Send() error = %v, want the search refused", err)
			}
			if len(reader.asked) != 0 {
				t.Fatalf("a refused search still read %d path(s)", len(reader.asked))
			}
			if !strings.Contains(reply.Text, "Let me search") {
				t.Fatalf("the refusal swallowed the reply: %q", reply.Text)
			}
		})
	}
	authority, _ := AuthorityFor(domain.RoleProgramManager)
	if !authority.RepositorySearch || !strings.Contains(authority.Contract, "# Searching the repository") {
		t.Fatalf("the program manager holds search %v; its contract must offer it", authority.RepositorySearch)
	}
	if authority.OnSideStream().RepositorySearch {
		t.Fatal("a side thread may search the repository")
	}
}

func searchFixture(t *testing.T, files map[string]string) string {
	t.Helper()

	repository := filepath.Join(t.TempDir(), "repository")
	for name, content := range files {
		target := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "Yoyodyne Test"},
		{"config", "user.email", "yoyodyne@example.invalid"},
		{"add", "-A"},
		{"commit", "-q", "-m", "fixture"},
	} {
		if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v error = %v: %s", args, err, output)
		}
	}
	return repository
}

func searchHead(t *testing.T, repository string) string {
	t.Helper()

	output, err := exec.Command("git", "-C", repository, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse error = %v", err)
	}
	return strings.TrimSpace(string(output))
}
