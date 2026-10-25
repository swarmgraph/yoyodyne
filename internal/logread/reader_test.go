package logread

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// The home here is laid out the current way, projects/<id>/state; the tests
// below keep the earlier products/<id> layout, which is read where it is.
func TestEveryRuledRecordAndWatchOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	product := home.ProductDirectory(root, "sample")
	reader := Reader{StateRoot: root, ProductID: "sample", RedactValues: []string{"known-secret"}}
	since := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)
	until := since.Add(time.Hour)
	for _, kind := range []string{"pass", "watch", "docket", "run", "usage-limit", "provider-outage", "intervention"} {
		request := Request{Record: kind, Since: since, Until: until, MaxBytes: MaxContentBytes}
		if kind == "run" {
			request.Name = "run-123"
		}
		path := filepath.Join(product, recordPath(request))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		text := `{"at":"2026-10-04T08:30:00Z","message":"known-secret observed","api_token":"unknown-secret"}` + "\n"
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		results, err := reader.Read(context.Background(), []Request{request})
		if err != nil || len(results) != 1 || results[0].Problem != "" || !strings.Contains(results[0].Content, "observed") {
			t.Fatalf("%s: %#v %v", kind, results, err)
		}
		if strings.Contains(results[0].Content, "known-secret") || strings.Contains(results[0].Content, "unknown-secret") {
			t.Fatalf("%s leaked: %s", kind, results[0].Content)
		}
		if results[0].NextCursor != int64(len(text)) {
			t.Fatalf("%s cursor=%d want %d", kind, results[0].NextCursor, len(text))
		}
	}
	cursor := int64(0)
	output := Request{Record: "watch", Name: "output", Cursor: &cursor, MaxBytes: 100}
	if err := os.WriteFile(filepath.Join(product, recordPath(output)), []byte("watch output known-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	results, err := reader.Read(context.Background(), []Request{output})
	if err != nil || !strings.Contains(results[0].Content, "watch output [REDACTED]") {
		t.Fatalf("watch output=%#v %v", results, err)
	}
}

func TestJSONEscapingCannotHideASecretFromRedaction(t *testing.T) {
	t.Parallel()
	secret := "quoted\"value\nsecond line"
	data, err := json.Marshal(map[string]any{"detail": secret, "values": []string{secret}, "nested": map[string]string{"password": "unknown"}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	directory := filepath.Join(root, "products/sample")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "watch.jsonl"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	cursor := int64(0)
	results, err := (Reader{StateRoot: root, ProductID: "sample", RedactValues: []string{secret}}).Read(context.Background(), []Request{{Record: "watch", Cursor: &cursor, MaxBytes: 4096}})
	if err != nil || len(results) != 1 || results[0].Problem != "" || strings.Contains(results[0].Content, "quoted") || strings.Contains(results[0].Content, "unknown") || strings.Count(results[0].Content, "[REDACTED]") != 3 {
		t.Fatalf("redacted results=%v err=%v", results, err)
	}
}

func TestBoundsWindowsCursorsAndRefusals(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	directory := filepath.Join(root, "products/sample")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	first := `{"at":"2026-10-04T08:30:00Z","text":"first secret-value"}` + "\n"
	second := `{"at":"2026-10-04T09:30:00Z","text":"second"}` + "\n"
	if err := os.WriteFile(filepath.Join(directory, "watch.jsonl"), []byte(first+second), 0600); err != nil {
		t.Fatal(err)
	}
	reader := Reader{StateRoot: root, ProductID: "sample", RedactValues: []string{"secret-value"}}
	cursor := int64(0)
	request := Request{Record: "watch", Cursor: &cursor, MaxBytes: 55}
	results, err := reader.Read(context.Background(), []Request{request})
	if err != nil || results[0].Bytes > 55 || !results[0].Truncated || strings.Contains(results[0].Content, "secret-value") {
		t.Fatalf("bounded=%#v %v", results, err)
	}
	if results[0].Size != int64(len(first+second)) {
		t.Fatalf("a cut read names a whole size of %d, want %d", results[0].Size, len(first+second))
	}
	cursor = int64(len(first))
	request.MaxBytes = 1000
	results, err = reader.Read(context.Background(), []Request{request})
	if err != nil || !strings.Contains(results[0].Content, "second") || strings.Contains(results[0].Content, "first") {
		t.Fatalf("cursor=%#v %v", results, err)
	}
	request.Cursor = nil
	request.Since = time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	request.Until = request.Since.Add(time.Hour)
	results, err = reader.Read(context.Background(), []Request{request})
	if err != nil || !strings.Contains(results[0].Content, "second") || strings.Contains(results[0].Content, "first") {
		t.Fatalf("window=%#v %v", results, err)
	}
	for _, invalid := range []Request{
		{Record: "memory", Cursor: &cursor, MaxBytes: 100},
		{Record: "conversations", Cursor: &cursor, MaxBytes: 100},
		{Record: "token", Cursor: &cursor, MaxBytes: 100},
		{Record: "run", Name: "../../escape", Cursor: &cursor, MaxBytes: 100},
		{Record: "run", Name: "/absolute", Cursor: &cursor, MaxBytes: 100},
		{Record: "watch", Cursor: &cursor, MaxBytes: MaxContentBytes + 1},
		{Record: "watch", MaxBytes: 100},
	} {
		if _, err := reader.Read(context.Background(), []Request{invalid}); err == nil {
			t.Fatalf("accepted invalid request %#v", invalid)
		}
	}
	cursor = 1
	request = Request{Record: "watch", Cursor: &cursor, MaxBytes: 100}
	results, err = reader.Read(context.Background(), []Request{request})
	if err != nil || !strings.Contains(results[0].Problem, "boundary") {
		t.Fatalf("unaligned cursor=%#v %v", results, err)
	}
	cursor = 0
	request.Record = "docket"
	results, err = reader.Read(context.Background(), []Request{request})
	if err != nil || results[0].Problem == "" {
		t.Fatalf("missing record=%#v %v", results, err)
	}
}

func TestLinksCannotReadForbiddenRecords(t *testing.T) {
	t.Parallel()
	for _, directoryLink := range []bool{false, true} {
		root := t.TempDir()
		product := filepath.Join(root, "products/sample")
		if err := os.MkdirAll(product, 0700); err != nil {
			t.Fatal(err)
		}
		secret := filepath.Join(root, "secret")
		if err := os.WriteFile(secret, []byte("forbidden\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if directoryLink {
			if err := os.Remove(product); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(root, product); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Symlink(secret, filepath.Join(product, "watch.jsonl")); err != nil {
			t.Fatal(err)
		}
		cursor := int64(0)
		results, err := (Reader{StateRoot: root, ProductID: "sample"}).Read(context.Background(), []Request{{Record: "watch", Cursor: &cursor, MaxBytes: 100}})
		if err != nil || results[0].Problem == "" || results[0].Content != "" {
			t.Fatalf("link returned %#v %v", results, err)
		}
	}
}

func TestWholeReplyAndScanBounds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	directory := filepath.Join(root, "products/sample")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", MaxContentBytes) + "\n"
	if err := os.WriteFile(filepath.Join(directory, "watch.jsonl"), []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	cursor := int64(0)
	request := Request{Record: "watch", Cursor: &cursor, MaxBytes: MaxContentBytes}
	results, err := (Reader{StateRoot: root, ProductID: "sample"}).Read(context.Background(), []Request{request, request, request})
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, result := range results {
		sum += result.Bytes
	}
	if sum != MaxBytesPerReply || !results[2].Truncated {
		t.Fatalf("reply returned %d bytes: %#v", sum, results)
	}
	if err := os.WriteFile(filepath.Join(directory, "watch.jsonl"), []byte(strings.Repeat("x", MaxScanBytes+2)), 0600); err != nil {
		t.Fatal(err)
	}
	results, err = (Reader{StateRoot: root, ProductID: "sample"}).Read(context.Background(), []Request{request})
	if err != nil || !results[0].Truncated || results[0].Content != "" || results[0].NextCursor != 0 {
		t.Fatalf("scan bound=%#v %v", results, err)
	}
}

func TestBlockRejectsUnknownFieldsAndMixedBounds(t *testing.T) {
	t.Parallel()
	for _, payload := range []string{
		`{"requests":[{"record":"watch","cursor":0,"max_bytes":100,"path":"secret"}]}`,
		`{"requests":[{"record":"watch","since":"2026-10-04T08:00:00Z","max_bytes":100}]}`,
		`{"requests":[{"record":"watch","cursor":0,"task":"health","max_bytes":100}]}`,
		`{"requests":[{"record":"pass","cursor":0,"task":"../health","max_bytes":100}]}`,
		`{"requests":[{"record":"pass","since":"2026-10-04T08:00:00Z","until":"2026-10-04T09:00:00Z","offset":10,"max_bytes":100}]}`,
		`{"requests":[{"record":"pass","cursor":0,"offset":-1,"max_bytes":100}]}`,
		`{"requests":[]} trailing`,
	} {
		if _, err := Decode(payload); err == nil {
			t.Errorf("accepted %s", payload)
		}
	}
	if _, err := Decode(`{"requests":[{"record":"pass","task":"health","cursor":40,"offset":10,"since":"2026-10-04T08:00:00Z","until":"2026-10-04T09:00:00Z","max_bytes":100}]}`); err != nil {
		t.Errorf("refused a window read carried on from a cursor and offset: %v", err)
	}
	body := `{"requests":[{"record":"watch","cursor":0,"max_bytes":100}]}`
	prose, requests, err := Extract("read it\n" + Fence + "\n" + body + "\n```")
	if err != nil || len(requests) != 1 || !strings.Contains(prose, "read it") {
		t.Fatalf("extract=%q %#v %v", prose, requests, err)
	}
	if err := Descriptor().Validate("log.read", []capability.Capability{capability.LogRead}); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(requests)
	if strings.Contains(string(encoded), "path") {
		t.Fatal("protocol accepts paths")
	}
}

// writePasses writes the pass record under the product's current layout and
// returns each line as written, newline included.
func writePasses(t *testing.T, root string, passes ...map[string]any) []string {
	t.Helper()
	path := filepath.Join(home.ProductDirectory(root, "sample"), "sweeps", "sweeps.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, pass := range passes {
		data, err := json.Marshal(pass)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(data)+"\n")
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0600); err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestAPassLongerThanOneReadIsReadInPartsToItsEnd(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// The failure output is three reads long, written in two-byte characters so
	// a byte cap falls inside one, with the secret repeated across every part.
	var output strings.Builder
	for output.Len() < 3*MaxContentBytes {
		output.WriteString("provider said known-secret é ")
	}
	since := time.Date(2026, 10, 19, 10, 10, 0, 0, time.UTC)
	lines := writePasses(t, root,
		map[string]any{"task": "technical-health-pm", "started_at": since.Add(time.Minute), "problem": "the pass failed", "failure_output": output.String(), "api_token": "unknown-secret"},
		map[string]any{"task": "technical-health-pm", "started_at": since.Add(2 * time.Minute), "problem": "the next pass"},
	)
	redactor := execution.NewRedactor("known-secret")
	whole := redactor.Redact(redactFields([]byte(lines[0]), redactor))
	reader := Reader{StateRoot: root, ProductID: "sample", RedactValues: []string{"known-secret"}}
	request := Request{Record: "pass", Since: since, Until: since.Add(40 * time.Minute), MaxBytes: MaxContentBytes}
	var assembled strings.Builder
	parts := 0
	for {
		results, err := reader.Read(context.Background(), []Request{request})
		if err != nil || len(results) != 1 || results[0].Problem != "" {
			t.Fatalf("part %d: %#v %v", parts, results, err)
		}
		result := results[0]
		if result.Part == nil {
			t.Fatalf("part %d of a record longer than one read came back without saying it is a part: %#v", parts, result)
		}
		if result.Part.Start != int64(assembled.Len()) || result.Bytes != len(result.Content) || result.Bytes > MaxContentBytes {
			t.Fatalf("part %d starts at %d with %d bytes, after %d bytes read", parts, result.Part.Start, result.Bytes, assembled.Len())
		}
		if strings.Contains(result.Content, "known-secret") || strings.Contains(result.Content, "unknown-secret") || !utf8.ValidString(result.Content) {
			t.Fatalf("part %d leaked a secret or split a character", parts)
		}
		assembled.WriteString(result.Content)
		parts++
		if result.Part.Remaining != int64(len(whole)-assembled.Len()) {
			t.Fatalf("part %d says %d bytes remain, want %d", parts, result.Part.Remaining, len(whole)-assembled.Len())
		}
		if result.Next == nil {
			t.Fatalf("part %d gives no next read", parts)
		}
		request = *result.Next
		if result.Part.Remaining == 0 {
			if result.NextCursor != int64(len(lines[0])) {
				t.Fatalf("the last part leaves the cursor at %d, want %d", result.NextCursor, len(lines[0]))
			}
			break
		}
		if result.NextCursor != 0 || request.Offset != result.Part.Start+int64(result.Bytes) {
			t.Fatalf("part %d's next read is %#v", parts, request)
		}
		if parts > 10 {
			t.Fatal("the record was never read to its end")
		}
	}
	if len(whole) <= 2*MaxContentBytes || parts < (len(whole)+MaxContentBytes-1)/MaxContentBytes || assembled.String() != whole {
		t.Fatalf("read %d parts; assembled the record as returned whole: %v", parts, assembled.String() == whole)
	}
	if request.Since != since || request.Offset != 0 {
		t.Fatalf("the read after the last part dropped its window or kept an offset: %#v", request)
	}
	results, err := reader.Read(context.Background(), []Request{request})
	if err != nil || results[0].Problem != "" || results[0].Part != nil || !strings.Contains(results[0].Content, "the next pass") || results[0].Next != nil {
		t.Fatalf("the record after the long one: %#v %v", results, err)
	}

	beyond := int64(0)
	results, err = reader.Read(context.Background(), []Request{{Record: "pass", Cursor: &beyond, Offset: int64(len(whole)), MaxBytes: 100}})
	if err != nil || !strings.Contains(results[0].Problem, "offset") || results[0].Content != "" {
		t.Fatalf("an offset past the record: %#v %v", results, err)
	}
}

func TestAPassReadNarrowsToOneTask(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	since := time.Date(2026, 10, 19, 10, 10, 0, 0, time.UTC)
	var passes []map[string]any
	for i := range 9 {
		task := []string{"technical-health-pm", "factory-flow-pm", "dm-sweep"}[i%3]
		passes = append(passes, map[string]any{"task": task, "started_at": since.Add(time.Duration(i) * time.Minute), "problem": fmt.Sprintf("pass %d of %s", i, task)})
	}
	writePasses(t, root, passes...)
	reader := Reader{StateRoot: root, ProductID: "sample"}
	request := Request{Record: "pass", Task: "technical-health-pm", Since: since, Until: since.Add(time.Hour), MaxBytes: MaxContentBytes}
	results, err := reader.Read(context.Background(), []Request{request})
	if err != nil || results[0].Problem != "" || results[0].Next != nil {
		t.Fatalf("task read=%#v %v", results, err)
	}
	for _, want := range []string{"pass 0 of", "pass 3 of", "pass 6 of"} {
		if !strings.Contains(results[0].Content, want) {
			t.Fatalf("task read lacks %q: %s", want, results[0].Content)
		}
	}
	if strings.Contains(results[0].Content, "factory-flow-pm") || strings.Contains(results[0].Content, "dm-sweep") {
		t.Fatalf("task read returned another task's passes: %s", results[0].Content)
	}

	// A cap that holds one pass stops before the next of the same task, and
	// the next read it gives carries the task and the window on.
	request.MaxBytes = len(results[0].Content)/3 + 10
	var read []string
	for range 5 {
		results, err = reader.Read(context.Background(), []Request{request})
		if err != nil || results[0].Problem != "" || results[0].Part != nil {
			t.Fatalf("narrow read=%#v %v", results, err)
		}
		read = append(read, results[0].Content)
		if results[0].Next == nil {
			break
		}
		request = *results[0].Next
		if request.Task != "technical-health-pm" || request.Since != since {
			t.Fatalf("next read dropped the task or window: %#v", request)
		}
	}
	joined := strings.Join(read, "")
	if strings.Count(joined, `"task":"technical-health-pm"`) != 3 || len(read) != 3 || strings.Contains(joined, "dm-sweep") {
		t.Fatalf("one pass at a time read %q", read)
	}
}
