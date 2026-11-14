package logread

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

func longOutput(bytes int) string {
	var output strings.Builder
	for output.Len() < bytes {
		output.WriteString("provider said known-secret é ")
	}
	return output.String()
}

// A pass entry larger than one read is returned in parts, and every part says
// when the pass started, when it ended, and how it ended, so the first read
// alone tells a program manager whether the role ran.
func TestAPassReadInPartsCarriesItsSummaryOnEveryPart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	started := time.Date(2026, 10, 19, 10, 11, 0, 0, time.UTC)
	ended := started.Add(4 * time.Minute)
	writePasses(t, root,
		map[string]any{"task": "development-manager-sweep", "role": "development-manager", "started_at": started, "ended_at": ended, "turns": 2,
			"failure_output": longOutput(2 * MaxContentBytes), "problem": "a turn of the pass was not answered with known-secret"},
	)
	reader := Reader{StateRoot: root, ProductID: "sample", RedactValues: []string{"known-secret"}}
	cursor := int64(0)
	request := Request{Record: "pass", Cursor: &cursor, MaxBytes: MaxContentBytes}
	for part := 0; ; part++ {
		results, err := reader.Read(context.Background(), []Request{request})
		if err != nil || len(results) != 1 || results[0].Content == "" || results[0].Part == nil {
			t.Fatalf("part %d: %#v %v", part, results, err)
		}
		summary := results[0].Summary
		if summary == nil || summary.Task != "development-manager-sweep" || summary.Role != "development-manager" ||
			summary.StartedAt == nil || !summary.StartedAt.Equal(started) || summary.EndedAt == nil || !summary.EndedAt.Equal(ended) {
			t.Fatalf("part %d summary = %#v, want whose pass, when it started and ended", part, summary)
		}
		if !strings.HasPrefix(summary.Ended, "completed after 2 turn(s)") || summary.Incomplete {
			t.Fatalf("part %d says the pass ended %q", part, summary.Ended)
		}
		if strings.Contains(summary.Ended, "known-secret") {
			t.Fatalf("the summary leaked a secret: %q", summary.Ended)
		}
		if results[0].Part.Remaining == 0 {
			break
		}
		request = *results[0].Next
		if part > 5 {
			t.Fatal("the entry was never read to its end")
		}
	}
}

// Reads earlier in a reply that spend what one reply may return leave a later
// read of a long pass entry with no room; it says so and summarises the entry
// rather than coming back as an empty part.
func TestAPassReadWithNoRoomLeftInTheReplyIsNeverSilentlyEmpty(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	started := time.Date(2026, 10, 19, 10, 11, 0, 0, time.UTC)
	lines := writePasses(t, root,
		map[string]any{"task": "a", "started_at": started, "ended_at": started.Add(time.Minute), "turns": 1, "failure_output": longOutput(MaxContentBytes + 10)},
		map[string]any{"task": "b", "started_at": started, "ended_at": started.Add(time.Minute), "turns": 1, "failure_output": longOutput(MaxContentBytes + 10)},
		map[string]any{"task": "c", "started_at": started, "ended_at": started.Add(time.Minute), "turns": 0, "failed": true, "failure_output": longOutput(MaxContentBytes + 10)},
	)
	reader := Reader{StateRoot: root, ProductID: "sample"}
	first, second := int64(0), int64(len(lines[0]))
	third := second + int64(len(lines[1]))
	results, err := reader.Read(context.Background(), []Request{
		{Record: "pass", Cursor: &first, MaxBytes: MaxContentBytes},
		{Record: "pass", Cursor: &second, MaxBytes: MaxContentBytes},
		{Record: "pass", Cursor: &third, MaxBytes: MaxContentBytes},
	})
	if err != nil || len(results) != 3 {
		t.Fatalf("Read() = %#v, %v", results, err)
	}
	last := results[2]
	if last.Content != "" || !strings.Contains(last.Problem, "none of this entry was returned") {
		t.Fatalf("the read with no room = %#v, want it to say why nothing came back", last)
	}
	if last.Summary == nil || last.Summary.Task != "c" || !strings.HasPrefix(last.Summary.Ended, "failed after 0 turn(s)") {
		t.Fatalf("the read with no room summarised the pass as %#v", last.Summary)
	}
	if last.Next == nil || last.Next.Cursor == nil || *last.Next.Cursor != third || last.Next.Offset != 0 {
		t.Fatalf("the read with no room gives the next read %#v, want the same entry asked again", last.Next)
	}
	again, err := reader.Read(context.Background(), []Request{*last.Next})
	if err != nil || again[0].Content == "" || again[0].Problem != "" {
		t.Fatalf("asking again on its own = %#v, %v", again, err)
	}
}

// One pass entry longer than the whole of what one read scans cannot be
// returned even in parts; it is summarised from what was scanned and the next
// read starts after it, so the reader is never handed the same empty read.
func TestAPassEntryLongerThanTheScanBoundIsSummarisedAndPassed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	started := time.Date(2026, 10, 19, 10, 11, 0, 0, time.UTC)
	head := `{"task":"development-manager-sweep","role":"development-manager","started_at":"` + started.Format(time.RFC3339) + `","turns":1,"failure_output":"`
	huge := head + strings.Repeat("x", MaxScanBytes+1024) + `"}` + "\n"
	after := `{"task":"next","started_at":"` + started.Format(time.RFC3339) + `"}` + "\n"
	path := filepath.Join(home.ProductDirectory(root, "sample"), "sweeps", "sweeps.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(huge+after), 0600); err != nil {
		t.Fatal(err)
	}
	reader := Reader{StateRoot: root, ProductID: "sample"}
	cursor := int64(0)
	results, err := reader.Read(context.Background(), []Request{{Record: "pass", Cursor: &cursor, MaxBytes: MaxContentBytes}})
	if err != nil || len(results) != 1 {
		t.Fatalf("Read() = %#v, %v", results, err)
	}
	result := results[0]
	if !strings.Contains(result.Problem, "cannot be returned, even in parts") || result.Content != "" {
		t.Fatalf("an entry over the scan bound = problem %q, %d bytes", result.Problem, len(result.Content))
	}
	if result.Summary == nil || result.Summary.Task != "development-manager-sweep" || result.Summary.StartedAt == nil || !result.Summary.Incomplete {
		t.Fatalf("summary = %#v, want the pass named and its start read from what was scanned", result.Summary)
	}
	if result.NextCursor != int64(len(huge)) || result.Next == nil || *result.Next.Cursor != int64(len(huge)) {
		t.Fatalf("next cursor = %d, next = %#v, want the read after the entry", result.NextCursor, result.Next)
	}
	following, err := reader.Read(context.Background(), []Request{*result.Next})
	if err != nil || !strings.Contains(following[0].Content, `"next"`) {
		t.Fatalf("the read after it = %#v, %v", following, err)
	}
}
