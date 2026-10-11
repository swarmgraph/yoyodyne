package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// readingBackend is a reviewer that reads files from the worktree it was
// started in and reports each read as the adapters do: a command.started, and a
// command.completed saying how much came back. A file that cannot be read comes
// back as an error with nothing in it. Its verdict quotes what it read.
type readingBackend struct {
	files []string
	text  []string
}

func (b *readingBackend) Run(_ context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	sequence := execution.NewSequence(request.LastSequence)
	emit := func(eventType execution.EventType, payload any) error {
		event, err := execution.NewEvent(request.RunID, sequence.Next(), reviewClock{}.Now(), eventType, "fake.reviewer", payload)
		if err != nil {
			return err
		}
		return request.EventSink(event)
	}
	for _, text := range b.text {
		if err := emit(execution.EventAgentMessage, execution.ReplyPayload(text)); err != nil {
			return backendapi.RunResult{}, err
		}
	}
	var read []string
	for index, file := range b.files {
		id := "read-" + string(rune('a'+index))
		if err := emit(execution.EventCommandStarted, map[string]any{"tool_use_id": id, "tool": "Read"}); err != nil {
			return backendapi.RunResult{}, err
		}
		data, err := os.ReadFile(filepath.Join(request.WorkingDirectory, file))
		if err := emit(execution.EventCommandCompleted, map[string]any{"tool_use_id": id, "is_error": err != nil, "content_bytes": len(data)}); err != nil {
			return backendapi.RunResult{}, err
		}
		read = append(read, strings.TrimSpace(string(data)))
	}
	summary := "judged from the evidence"
	if len(read) > 0 {
		summary = "the caller reads: " + strings.Join(read, "; ")
	}
	return backendapi.RunResult{
		Backend:   domain.BackendClaudeCode,
		SessionID: "review-session",
		FinalText: `{"decision":"approve","approves":"implementation","summary":"` + summary + `"}`,
		Process:   execution.ProcessResult{Status: execution.ProcessSucceeded},
		LastEvent: sequence.Last(),
	}, nil
}

func reviewWith(t *testing.T, provider Backend, worktree string) (Result, []execution.Event, error) {
	t.Helper()
	var events []execution.Event
	request := newRequest(func(event execution.Event) error {
		events = append(events, event)
		return nil
	})
	request.WorktreePath = worktree
	result, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request)
	return result, events, err
}

// A reviewer reads a file in the worktree that nothing it was handed carried,
// and its review is judged: what it read reaches its verdict, and the record
// closes on review.completed.
func TestAReviewerReadsAFileItWasNotHanded(t *testing.T) {
	t.Parallel()

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "caller.go"), []byte("package caller // calls Store.Save\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, events, err := reviewWith(t, &readingBackend{files: []string{"caller.go"}}, worktree)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Decision != DecisionApprove || !strings.Contains(result.Verdict.Summary, "calls Store.Save") {
		t.Fatalf("Review() = %#v, want a judged approval carrying what it read", result)
	}
	if last := events[len(events)-1]; last.Type != execution.EventReviewCompleted {
		t.Fatalf("last event = %s, want review.completed", last.Type)
	}
}

// A reviewer whose reads all came back empty is refused as a judged review,
// whichever way they came back empty: calls that returned nothing, or calls
// written into the reply as text, which run nothing. The record says so in
// place of review.completed and keeps the verdict it did not count.
func TestAReviewWhoseReadsCameBackEmptyIsNotJudged(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		provider *readingBackend
		want     string
	}{
		{name: "calls that returned nothing", provider: &readingBackend{files: []string{"missing.go", "also-missing.go"}}, want: "2 read(s) that returned nothing"},
		{name: "calls written as text", provider: &readingBackend{text: []string{"Let me look.\n<invoke name=\"Read\"><parameter name=\"file_path\">caller.go</parameter></invoke>\n<invoke name=\"Bash\">"}}, want: "2 tool call(s) written into its reply as text"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, events, err := reviewWith(t, test.provider, t.TempDir())
			var unread UnreadRepositoryError
			if !errors.As(err, &unread) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Review() error = %v, want reads that came back empty naming %q", err, test.want)
			}
			if result.Decision != "" || result.Verdict.Decision != DecisionApprove {
				t.Fatalf("Review() = %#v, want the verdict kept as evidence and not resolved", result)
			}
			last := events[len(events)-1]
			if last.Type != execution.EventReviewUnread || !strings.Contains(string(last.Payload), `"answered_reads":0`) {
				t.Fatalf("last event = %s %s, want review.unread", last.Type, last.Payload)
			}
			for _, event := range events {
				if event.Type == execution.EventReviewCompleted {
					t.Fatal("a review whose reads came back empty was recorded as completed")
				}
			}
			if result.LastSequence != last.Sequence {
				t.Fatalf("LastSequence = %d, want the review.unread event's %d", result.LastSequence, last.Sequence)
			}
		})
	}
}

// A reviewer that tried no read judged from its evidence by choice, which the
// contract permits, and one read that answered is enough: neither is refused.
func TestAReviewThatReadNothingOrReadSomethingIsJudged(t *testing.T) {
	t.Parallel()

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "caller.go"), []byte("package caller\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, provider := range map[string]*readingBackend{
		"no read":           {},
		"one read answered": {files: []string{"missing.go", "caller.go"}},
	} {
		if result, _, err := reviewWith(t, provider, worktree); err != nil || result.Decision != DecisionApprove {
			t.Fatalf("%s: Review() = %#v, %v, want a judged approval", name, result, err)
		}
	}
}

// The contract tells the reviewer not to write tool calls as text, and that a
// review whose reads came back empty is not counted.
func TestTheContractSaysAToolCallWrittenAsTextRunsNothing(t *testing.T) {
	t.Parallel()

	for _, scope := range []Scope{ScopeWorkItem, ScopeBranch} {
		contract := reviewSystemPrompt(scope, "")
		for _, want := range []string{"Never write a tool call into your reply as text", "not recorded as a judged review"} {
			if !strings.Contains(contract, want) {
				t.Fatalf("%s contract is missing %q", scope, want)
			}
		}
	}
}

// Only a line that opens with a written-out tool call, outside a code block,
// counts as one: a reviewer quoting code or prose that mentions the markers has
// not tried to read anything.
func TestOnlyALineOpeningWithAToolCallCountsAsOneWrittenAsText(t *testing.T) {
	t.Parallel()

	for text, want := range map[string]int{
		"<invoke name=\"Bash\">\n<parameter name=\"command\">git log</parameter>\n</invoke>": 1,
		"  &lt;invoke name=\"Read\"&gt;\n<function_calls>":                                   2,
		"reads.go counts `<invoke name=` written in a reply":                                 0,
		"```go\n<invoke name=\"Bash\">\n```":                                                 0,
		"{\"summary\":\"the marker <invoke name= is matched at a line start\"}":              0,
	} {
		if got := writtenToolCalls(text); got != want {
			t.Errorf("writtenToolCalls(%q) = %d, want %d", text, got, want)
		}
	}
}
