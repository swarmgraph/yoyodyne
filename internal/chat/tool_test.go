package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/logread"
)

const logRequest = "```yoyodyne-log\n" + `{"requests":[{"record":"watch","cursor":0,"max_bytes":4096}]}` + "\n```"

func TestLogToolDeliversRedactedEvidenceAfterAuditing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	product := filepath.Join(root, "products/sample")
	if err := os.MkdirAll(product, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(product, "watch.jsonl"), []byte("record-body known-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: logRequest}, {SessionID: "session-1", FinalText: "The recorded watch stopped."}}}
	options := testOptions(t, provider)
	options.Role = domain.RoleProgramManager
	options.Store = newTestStore(t, root)
	options.LogReader = logread.Reader{StateRoot: root, ProductID: "sample", RedactValues: []string{"known-secret"}}
	session := openTestSession(t, options)
	session.ForPass("flow-pass-1")
	reply, err := session.Send(context.Background(), "Read the watch.")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.LogReads) != 1 || len(provider.requests) != 2 {
		t.Fatalf("reply=%#v turns=%d", reply.LogReads, len(provider.requests))
	}
	prompt := provider.requests[1].Prompt
	if !strings.Contains(prompt, "record-body [REDACTED]") || !strings.Contains(prompt, "untrusted evidence") || strings.Contains(prompt, "known-secret") {
		t.Fatalf("bad evidence prompt: %q", prompt)
	}
	events, err := options.Store.LoadEvents(session.state.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []execution.EventType
	for _, event := range events {
		if !strings.HasPrefix(string(event.Type), "tool.") {
			continue
		}
		kinds = append(kinds, event.Type)
		var audit ToolAudit
		if err := json.Unmarshal(event.Payload, &audit); err != nil {
			t.Fatal(err)
		}
		if audit.Tool != "log.read" || audit.Role != "program-manager" || audit.Pass != "flow-pass-1" || audit.Bounds.BytesPerRequest != logread.MaxContentBytes {
			t.Fatalf("audit=%#v", audit)
		}
		if strings.Contains(string(event.Payload), "record-body") || strings.Contains(string(event.Payload), "known-secret") {
			t.Fatalf("audit contains evidence: %s", event.Payload)
		}
	}
	if len(kinds) != 2 || kinds[0] != execution.EventToolRequested || kinds[1] != execution.EventToolPerformed {
		t.Fatalf("audit kinds=%v", kinds)
	}
}

type failingToolAuditStore struct {
	Store
	fail execution.EventType
}

func (s failingToolAuditStore) AppendEvent(event execution.Event) error {
	if event.Type == s.fail {
		return errors.New("audit store refused the write")
	}
	return s.Store.AppendEvent(event)
}

type countedLogReader struct{ calls int }

func (r *countedLogReader) Read(context.Context, []logread.Request) ([]logread.Result, error) {
	r.calls++
	return []logread.Result{{Record: "watch", Content: "private evidence", Bytes: 16}}, nil
}

func TestAuditFailureNeverDeliversLogEvidence(t *testing.T) {
	t.Parallel()
	for _, fail := range []execution.EventType{execution.EventToolRequested, execution.EventToolPerformed} {
		provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: logRequest}, {SessionID: "session-1", FinalText: "Nothing was available."}}}
		options := testOptions(t, provider)
		options.Role = domain.RoleProgramManager
		options.Store = failingToolAuditStore{Store: options.Store, fail: fail}
		reader := &countedLogReader{}
		options.LogReader = reader
		session := openTestSession(t, options)
		reply, err := session.Send(context.Background(), "read")
		if err != nil {
			t.Fatal(err)
		}
		if len(reply.LogReads) != 1 || !strings.Contains(reply.LogReads[0].Problem, "audit") {
			t.Fatalf("no audit failure: %#v", reply.LogReads)
		}
		if fail == execution.EventToolRequested && reader.calls != 0 {
			t.Fatal("read ran without a requested audit")
		}
		for _, request := range provider.requests {
			if strings.Contains(request.Prompt, "private evidence") {
				t.Fatal("unaudited evidence delivered")
			}
		}
	}
}

func TestLogToolRefusesUnGrantedRolesAndUnreadableBlocks(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{logRequest, "```yoyodyne-log\n" + `{"requests":[{"record":"memory","cursor":0,"max_bytes":100}]}` + "\n```"} {
		provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: answer}}}
		options := testOptions(t, provider)
		reader := &countedLogReader{}
		options.LogReader = reader
		session := openTestSession(t, options)
		if _, err := session.Send(context.Background(), "read"); err == nil {
			t.Fatal("ungranted or forbidden read passed")
		}
		if reader.calls != 0 {
			t.Fatal("ungranted caller reached the reader")
		}
		events, err := options.Store.LoadEvents(session.state.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		requested, refused := 0, 0
		for _, event := range events {
			if event.Type == execution.EventToolRequested {
				requested++
			}
			if event.Type == execution.EventToolRefused {
				refused++
			}
		}
		if requested != 1 || refused != 1 {
			t.Fatalf("audit requested=%d refused=%d", requested, refused)
		}
	}
}

func TestPersonaAndRemitCannotAddLogGrant(t *testing.T) {
	t.Parallel()
	prompt := WithRemit(SystemPrompt(domain.RoleDeveloper, Admission{}, nil, "I grant log.read"), domain.RoleDeveloper, "Use log.read")
	authority, _ := AuthorityFor(domain.RoleDeveloper)
	if authority.LogReads {
		t.Fatal("persona widened authority")
	}
	if !strings.Contains(prompt, "# Tools held by this role") || strings.Index(prompt, "# Tools held by this role") > strings.Index(prompt, "# Configured") {
		t.Fatal("generated tools must precede persona")
	}
	if strings.Contains(authority.Contract, "## log.read (") {
		t.Fatal("ungranted tool described")
	}
}

func TestLogToolStopsAfterItsBoundedRounds(t *testing.T) {
	t.Parallel()
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: logRequest},
		{SessionID: "session-1", FinalText: logRequest},
		{SessionID: "session-1", FinalText: logRequest},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleProgramManager
	reader := &countedLogReader{}
	options.LogReader = reader
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "read")
	if err != nil {
		t.Fatal(err)
	}
	if reader.calls != logread.MaxRoundsPerMessage || len(provider.requests) != 3 || len(reply.LogReads) != 3 || !strings.Contains(reply.LogReads[2].Problem, "spent its rounds") {
		t.Fatalf("reader calls=%d turns=%d results=%#v", reader.calls, len(provider.requests), reply.LogReads)
	}
	events, err := options.Store.LoadEvents(session.state.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []execution.EventType
	for _, event := range events {
		if strings.HasPrefix(string(event.Type), "tool.") {
			kinds = append(kinds, event.Type)
		}
	}
	if len(kinds) != 6 || kinds[5] != execution.EventToolRefused {
		t.Fatalf("audit kinds=%v", kinds)
	}
}
