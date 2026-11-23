package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// providerStandIn stands in for the provider's program: it keeps what it was
// given, the system prompt argument and the standard input, and ends each turn.
type providerStandIn struct {
	system []string
	input  []string
}

func (p *providerStandIn) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if len(command.Args) == 1 && command.Args[0] == "--version" {
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "2.1.286 (Claude Code)\n"}, nil
	}
	system := ""
	for index, argument := range command.Args {
		if argument == "--append-system-prompt" && index+1 < len(command.Args) {
			system = command.Args[index+1]
		}
	}
	data, _ := io.ReadAll(command.Stdin)
	p.system = append(p.system, system)
	p.input = append(p.input, string(data))
	stream := `{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done"}` + "\n"
	for _, line := range strings.Split(strings.TrimSuffix(stream, "\n"), "\n") {
		observer(execution.Output{Stream: execution.StreamStdout, Text: line, Timestamp: time.Now()})
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: stream}, nil
}

// textOfMessage takes message number n out of what `yoyo invocations <run> <n>`
// printed, by the byte count its header gives, so the text is taken whatever it
// contains.
func textOfMessage(t *testing.T, shown string, n int) string {
	t.Helper()
	header := fmt.Sprintf("----- message %d of ", n)
	start := strings.Index(shown, header)
	if start < 0 {
		t.Fatalf("no message %d in:\n%s", n, shown)
	}
	line := shown[start : start+strings.Index(shown[start:], "-----\n")+len("-----\n")]
	size, err := strconv.Atoi(strings.Fields(line[strings.LastIndex(line, ", ")+2:])[0])
	if err != nil {
		t.Fatalf("no size in %q", line)
	}
	text := shown[start+len(line):]
	if len(text) < size {
		t.Fatalf("message %d is shorter than its header says", n)
	}
	if !strings.HasPrefix(text[size:], fmt.Sprintf("\n----- end of message %d -----\n", n)) {
		t.Fatalf("message %d does not end where its header says", n)
	}
	return text[:size]
}

// What the command shows for each invocation is what the provider's program was
// given, for a developer, a reviewer's first asking, and a reviewer's second
// asking in a resumed session.
func TestTheCommandShowsExactlyWhatTheProviderWasGiven(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runs, err := runstate.NewStore(root, "product")
	if err != nil {
		t.Fatal(err)
	}
	log := runs.Invocations()
	const runID = "run-0123456789abcdef0123456789abcdef"
	stand := &providerStandIn{}
	provider := claudecode.Backend{Runner: stand, ConfigDir: t.TempDir()}
	tricky := "a first message with ----- markers -----\nand a line break, ünïcode, and a trailing newline\n"
	scratch, err := execution.PrepareScratchDirectory(t.TempDir(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		role    domain.AgentRole
		prompt  string
		system  string
		session string
	}{
		{domain.RoleDeveloper, tricky, "", ""},
		{domain.RoleReviewer, "the change under review", "the reviewer's contract and persona", ""},
		{domain.RoleReviewer, "your last reply could not be read; answer again", "the reviewer's contract and persona", "session-1"},
		{domain.RoleDeveloper, "repair: the findings, as they stood then", "", "session-1"},
	} {
		request := backend.RunRequest{
			RunID: runID, Role: call.role, ScratchDirectory: scratch, WorkingDirectory: t.TempDir(), Prompt: call.prompt, SystemPrompt: call.system,
			SessionID: call.session, Model: "claude-test",
			Recorder: log.Recorder(runID, call.role, "abc123", time.Now),
		}
		if call.role == domain.RoleReviewer {
			request.AllowedTools = []string{}
		}
		if _, err := provider.Run(context.Background(), request); err != nil {
			t.Fatalf("%s: %v", call.role, err)
		}
	}
	if len(stand.input) != 4 {
		t.Fatalf("the provider was started %d times, want 4", len(stand.input))
	}
	var listed bytes.Buffer
	if code := writeInvocations(log, runID, 0, &listed, io.Discard); code != 0 || !strings.Contains(listed.String(), "4 invocation(s) recorded") {
		t.Fatalf("list (exit %d):\n%s", code, listed.String())
	}
	for index := range stand.input {
		var shown, problems bytes.Buffer
		if code := writeInvocations(log, runID, index+1, &shown, &problems); code != 0 {
			t.Fatalf("invocation %d (exit %d): %s", index+1, code, problems.String())
		}
		next := 1
		if stand.system[index] != "" {
			if got := textOfMessage(t, shown.String(), next); got != stand.system[index] {
				t.Fatalf("invocation %d: shown system text %q, the provider was given %q", index+1, got, stand.system[index])
			}
			next++
		}
		if got := textOfMessage(t, shown.String(), next); got != stand.input[index] {
			t.Fatalf("invocation %d: shown message %q, the provider was given %q", index+1, got, stand.input[index])
		}
		if !strings.Contains(shown.String(), "provider's own program adds by itself") {
			t.Fatalf("invocation %d does not say what is not in it:\n%s", index+1, shown.String())
		}
	}
}

// A number the run does not have, and a damaged record, are said and exit
// non-zero; text is never invented.
func TestTheCommandSaysWhatItCannotShow(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runs, err := runstate.NewStore(root, "product")
	if err != nil {
		t.Fatal(err)
	}
	var out, problems bytes.Buffer
	if code := writeInvocations(runs.Invocations(), "run-0123456789abcdef0123456789abcdef", 2, &out, &problems); code != 1 || out.Len() != 0 || !strings.Contains(problems.String(), "no such invocation") {
		t.Fatalf("exit %d, out %q, problems %q", code, out.String(), problems.String())
	}
}

// Every provider call the harness makes for a developer or a reviewer of a run
// is wired to keep what it is sent: a pipeline or a reviewer built without that
// would start invocations nothing recorded.
func TestTheProductionWiringKeepsWhatEveryDeveloperAndReviewerInvocationIsSent(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(filepath.Join("..", "..", config.DirectoryName, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	store, err := runstate.NewStore(t.TempDir(), cfg.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	parts := components{config: cfg, store: store}
	pipeline := pipelineFrom(parts)
	if pipeline.Invocations == nil {
		t.Fatal("the pipeline keeps nothing of what its developers are sent")
	}
	if reviewer, ok := pipeline.Reviewer.(review.Reviewer); !ok || reviewer.Invocations == nil {
		t.Fatalf("the pipeline's reviewer keeps nothing of what it is sent: %#v", pipeline.Reviewer)
	}
	for _, provider := range []domain.Backend{domain.BackendClaudeCode, domain.BackendCodex} {
		made, ok := reviewerForEndpoint(parts)(runstate.RoutedEndpoint{Provider: provider, Model: "m"})
		if !ok {
			continue
		}
		if reviewer, isReviewer := made.(review.Reviewer); !isReviewer || reviewer.Invocations == nil {
			t.Fatalf("the reviewer made for %s keeps nothing of what it is sent", provider)
		}
	}
}
