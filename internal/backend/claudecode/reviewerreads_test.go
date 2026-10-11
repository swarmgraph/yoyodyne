package claudecode

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A reviewer's settings give its shell the read-only Git commands and nothing
// else: the sandbox has to be running, refuses writes to the worktree, never
// runs a command unsandboxed, and approves nothing merely for being sandboxed,
// so the allow list is the whole of what runs.
func TestAReviewerIsGivenReadsAndNothingThatWrites(t *testing.T) {
	t.Parallel()

	encoded, err := reviewerSettings("/worktree under review")
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Sandbox struct {
			Enabled                  bool `json:"enabled"`
			FailIfUnavailable        bool `json:"failIfUnavailable"`
			AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands"`
			AutoAllowBashIfSandboxed bool `json:"autoAllowBashIfSandboxed"`
			Filesystem               struct {
				DenyWrite []string `json:"denyWrite"`
			} `json:"filesystem"`
		} `json:"sandbox"`
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		t.Fatalf("decode %s: %v", encoded, err)
	}
	sandbox := settings.Sandbox
	if !sandbox.Enabled || !sandbox.FailIfUnavailable || sandbox.AllowUnsandboxedCommands || sandbox.AutoAllowBashIfSandboxed {
		t.Fatalf("sandbox = %+v, want it required, never bypassed, and approving nothing by itself", sandbox)
	}
	if !slices.Contains(sandbox.Filesystem.DenyWrite, "/worktree under review") {
		t.Fatalf("denyWrite = %q, want the worktree refused for writing", sandbox.Filesystem.DenyWrite)
	}
	for _, rule := range settings.Permissions.Allow {
		command, ok := strings.CutPrefix(rule, "Bash(git ")
		command = strings.TrimSuffix(strings.TrimSuffix(command, ")"), " *")
		if !ok || !slices.Contains(reviewerGitCommands, command) {
			t.Fatalf("allow rule %q is not one of the read-only Git commands", rule)
		}
	}
	// Each command line is walked through the encoded rules the way the CLI
	// reads them: a deny rule that matches refuses it, and otherwise it runs only
	// if an allow rule matches. The refused lines are each a way an allowed
	// command would read or run something outside the repository.
	for _, command := range []string{
		"git grep -Ocat Save",
		"git grep -nOcat Save",
		"git blame --contents /etc/passwd README.md",
		"git blame --cont /etc/passwd README.md",
		"git diff -O/etc/passwd",
		"git log -O /etc/passwd -p",
		"git diff --orderfile=/etc/passwd",
		"git diff /etc/passwd /dev/null",
		"git diff '/etc/passwd' x",
		"git diff ~/.ssh/id_rsa x",
		"git diff ../../secret x",
		"git diff --no-index a b",
		"git diff --no-ind a b",
		"git log --output=x",
		"git log --out=x",
		"git show --textconv HEAD:x",
		"git cat-file --filters HEAD:x",
		"git diff --ext-diff",
		"git show $(cat /etc/passwd)",
		"git show `cat /etc/passwd`",
		"git -C /etc log",
		"cat /etc/passwd",
		"git checkout main",
	} {
		if bashRuns(settings.Permissions.Allow, settings.Permissions.Deny, command) {
			t.Errorf("the reviewer's shell would run %q", command)
		}
	}
	for _, command := range []string{
		"git log",
		"git log --oneline -5",
		"git show HEAD~1:internal/review/reads.go",
		"git diff main..HEAD -- internal/review",
		"git log -p -L 10,20:internal/review/reads.go",
		"git status --short",
		"git ls-files internal/review",
		"git merge-base main HEAD",
	} {
		if !bashRuns(settings.Permissions.Allow, settings.Permissions.Deny, command) {
			t.Errorf("the reviewer's shell would refuse %q, which only reads the repository", command)
		}
	}
	for _, write := range []string{"Edit", "Write", "NotebookEdit", "WebFetch", "WebSearch", "Task"} {
		if slices.Contains(reviewerTools, write) {
			t.Fatalf("reviewerTools = %q carries %s", reviewerTools, write)
		}
	}
}

// bashRuns reports whether a command line runs under the permission rules
// given, read as the CLI reads them: `*` matches anything, `Bash(git log *)`
// matches the command and anything after a space, a matching deny rule refuses,
// and a line no allow rule matches is asked about and so refused.
func bashRuns(allow, deny []string, command string) bool {
	matches := func(rules []string) bool {
		for _, rule := range rules {
			pattern, ok := strings.CutPrefix(rule, "Bash(")
			if !ok {
				continue
			}
			pattern = strings.TrimSuffix(pattern, ")")
			expression := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
			if regexp.MustCompile(expression).MatchString(command) {
				return true
			}
		}
		return false
	}
	return !matches(deny) && matches(allow)
}

// readingCLI stands in for Claude Code at the one point this adapter decides:
// it reads a file the reviewer asks for only if the invocation was launched
// with the Read tool and the file is inside the directory the invocation was
// started in, which is the rule the CLI applies to a session that cannot ask
// for permission. It answers with the stream the CLI writes for one read and a
// verdict.
type readingCLI struct {
	file    string
	command execution.Command
}

func (c *readingCLI) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	c.command = command
	if command.Stdin != nil {
		_, _ = io.ReadAll(command.Stdin)
	}
	tools := ""
	if index := slices.Index(command.Args, "--tools"); index >= 0 && index+1 < len(command.Args) {
		tools = command.Args[index+1]
	}
	path := filepath.Join(command.Dir, c.file)
	content := ""
	isError := true
	if slices.Contains(strings.Split(tools, ","), "Read") {
		if data, err := os.ReadFile(path); err == nil {
			content, isError = string(data), false
		}
	}
	use, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_use", "id": "read-1", "name": "Read", "input": map[string]string{"file_path": path}},
	}}})
	result, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": "read-1", "is_error": isError, "content": content},
	}}})
	verdict, _ := json.Marshal(map[string]any{"decision": "approve", "approves": "implementation", "summary": "the caller reads " + strings.TrimSpace(content)})
	final, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "session_id": "session-1", "is_error": false, "result": string(verdict)})
	stdout := string(use) + "\n" + string(result) + "\n" + string(final) + "\n"
	for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		observer(execution.Output{Stream: execution.StreamStdout, Text: line})
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: stdout}, nil
}

// A reviewer session reads a file in the worktree that its prompt never
// carried, and what it read reaches its verdict. Before the reviewer had tools
// this read could not happen: the same session was launched with none, wrote
// the call out as text, and judged without it.
func TestAReviewerSessionReadsAFileItWasNotHanded(t *testing.T) {
	t.Parallel()

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "caller.go"), []byte("package caller // calls Store.Save\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cli := &readingCLI{file: "caller.go"}
	var events []execution.Event
	result, err := (Backend{Runner: cli, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleReviewer, WorkingDirectory: worktree,
		Prompt:    "review the change to Store.Save; the patch is all you are handed",
		EventSink: func(event execution.Event) error { events = append(events, event); return nil },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.FinalText, "calls Store.Save") {
		t.Fatalf("verdict = %q, want it to carry what the reviewer read from caller.go", result.FinalText)
	}
	if cli.command.Dir != worktree {
		t.Fatalf("the reviewer was started in %q, want the worktree %q", cli.command.Dir, worktree)
	}
	answered := false
	for _, event := range events {
		if event.Type != execution.EventCommandCompleted {
			continue
		}
		var payload struct {
			IsError      bool `json:"is_error"`
			ContentBytes int  `json:"content_bytes"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		answered = answered || (!payload.IsError && payload.ContentBytes > 0)
	}
	if !answered {
		t.Fatalf("no read the reviewer made returned anything: %#v", events)
	}
}
