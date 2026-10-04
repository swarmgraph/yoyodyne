package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/logread"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestChatReportsLogReadsAndAuditFailures(t *testing.T) {
	t.Parallel()
	cursor := int64(0)
	for _, test := range []struct {
		name       string
		request    logread.Request
		failAudit  int
		failTurn   bool
		refuseRead bool
		want       []string
	}{
		{name: "truncated cursor read", request: logread.Request{Record: "watch", Name: "output", Cursor: &cursor, MaxBytes: 4096}, want: []string{"log.read watch/output", "cursor 0", "byte cap 4096", "scan cap 8388608", "next cursor 128", "truncated"}},
		{name: "time window read", request: logread.Request{Record: "run", Name: "run-a", Since: time.Date(2026, 10, 4, 7, 0, 0, 0, time.UTC), Until: time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC), MaxBytes: 2048}, want: []string{"log.read run/run-a", "window 2026-10-04T07:00:00Z to 2026-10-04T08:00:00Z", "byte cap 2048"}},
		{name: "refused read", request: logread.Request{Record: "watch", Cursor: &cursor, MaxBytes: 4096}, refuseRead: true, want: []string{"log.read watch", "refused: record unavailable"}},
		{name: "second round audit failure", request: logread.Request{Record: "watch", Cursor: &cursor, MaxBytes: 4096}, failAudit: 2, want: []string{"log.read watch", "byte cap 4096", "nothing handed back: tool outcome audit failed", "audit store refused the write"}},
		{name: "failed turn after read", request: logread.Request{Record: "watch", Cursor: &cursor, MaxBytes: 4096}, failTurn: true, want: []string{"log.read watch", "byte cap 4096", "next cursor 128", "truncated"}},
		{name: "failed turn after audit failure", request: logread.Request{Record: "watch", Cursor: &cursor, MaxBytes: 4096}, failAudit: 1, failTurn: true, want: []string{"log.read watch", "nothing handed back: tool outcome audit failed", "audit store refused the write"}},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				store, err := runstate.NewConversationStore(root, "yoyodyne")
				if err != nil {
					t.Fatal(err)
				}
				payload, err := json.Marshal(map[string]any{"requests": []logread.Request{test.request}})
				if err != nil {
					t.Fatal(err)
				}
				ask := logread.Fence + "\n" + string(payload) + "\n```"
				results := []backendapi.RunResult{{SessionID: "session-1", FinalText: ask}}
				if test.failAudit == 2 {
					results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: ask})
				} else {
					results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: "The watch stopped.", IsError: test.failTurn})
				}
				provider := &sequencingBackend{results: results}
				var recording chat.Store = store
				if test.failAudit > 0 {
					recording = &logAuditFailureStore{Store: store, failAt: test.failAudit}
				}
				reader := &cliLogReader{refuse: test.refuseRead}
				session, err := chat.Open(chat.Options{
					Role: domain.RoleProgramManager, Agent: "program-manager", Backend: provider,
					Store: recording, LogReader: reader, Model: "opus", Provider: domain.BackendClaudeCode,
					AccountAlias: config.DefaultAccountAlias, Repository: filepath.Join(root, "repository"),
					ProductID: "yoyodyne", RepositoryID: "yoyodyne",
					Briefing: chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
				})
				if err != nil {
					t.Fatal(err)
				}
				var stdout, stderr bytes.Buffer
				code := runChatMessage(context.Background(), session, domain.RoleProgramManager, "Read the record.", format == "json", &stdout, &stderr)
				wantCode := 0
				if test.failTurn {
					wantCode = 1
				}
				if code != wantCode {
					t.Fatalf("exit=%d want=%d stdout=%q stderr=%q", code, wantCode, stdout.String(), stderr.String())
				}
				rendered := stdout.String()
				if format == "json" {
					var output chatOutput
					if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
						t.Fatal(err)
					}
					wantRounds := 1
					if test.failAudit == 2 {
						wantRounds = 2
					}
					if len(output.LogReads) != wantRounds || (output.Error != "") != test.failTurn {
						t.Fatalf("JSON dropped log rounds or failure: %+v", output)
					}
					rendered = ""
					for _, round := range output.LogReads {
						rendered += round.Render()
						if len(round.Requests) != 1 || round.Requests[0].MaxBytes != test.request.MaxBytes {
							t.Fatalf("JSON dropped applied bounds: %+v", round)
						}
					}
					last := output.LogReads[len(output.LogReads)-1]
					if test.failAudit > 0 && (last.Problem == "" || len(last.Results) != 0) {
						t.Fatalf("audit failure exposed evidence or lost its reason: %+v", last)
					}
					if test.failAudit == 0 && !test.refuseRead && (len(last.Results) != 1 || last.Results[0].Content != "record evidence 1") {
						t.Fatalf("JSON dropped successful evidence: %+v", last)
					}
				}
				for _, want := range test.want {
					if !strings.Contains(rendered, want) {
						t.Errorf("output is missing %q: %q", want, rendered)
					}
				}
				if strings.Contains(stdout.String(), "record evidence 2") {
					t.Fatal("CLI exposed evidence whose audit failed")
				}
				for _, request := range provider.requests {
					if strings.Contains(request.Prompt, "record evidence 2") || test.failAudit == 1 && strings.Contains(request.Prompt, "record evidence 1") {
						t.Fatal("model received evidence whose audit failed")
					}
				}
			})
		}
	}
}

type logAuditFailureStore struct {
	chat.Store
	failAt, outcomes int
}

func (s *logAuditFailureStore) AppendEvent(event execution.Event) error {
	if event.Type == execution.EventToolPerformed {
		s.outcomes++
		if s.outcomes == s.failAt {
			return errors.New("audit store refused the write")
		}
	}
	return s.Store.AppendEvent(event)
}

type cliLogReader struct {
	refuse bool
	calls  int
}

func (r *cliLogReader) Read(_ context.Context, requests []logread.Request) ([]logread.Result, error) {
	r.calls++
	if r.refuse {
		return nil, errors.New("record unavailable")
	}
	content := "record evidence 1"
	if r.calls == 2 {
		content = "record evidence 2"
	}
	return []logread.Result{{Record: requests[0].Record, Name: requests[0].Name, Content: content, Bytes: len(content), NextCursor: 128, Truncated: true}}, nil
}
