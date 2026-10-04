package logread

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/capability"
)

func TestEveryRuledRecordAndWatchOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	reader := Reader{StateRoot: root, ProductID: "sample", RedactValues: []string{"known-secret"}}
	since := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)
	until := since.Add(time.Hour)
	for _, kind := range []string{"pass", "watch", "docket", "run", "usage-limit", "provider-outage"} {
		request := Request{Record: kind, Since: since, Until: until, MaxBytes: MaxContentBytes}
		if kind == "run" {
			request.Name = "run-123"
		}
		path := filepath.Join(root, "products/sample", recordPath(request))
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
	if err := os.WriteFile(filepath.Join(root, "products/sample", recordPath(output)), []byte("watch output known-secret\n"), 0600); err != nil {
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
		`{"requests":[{"record":"watch","cursor":0,"max_bytes":100,"since":"2026-10-04T08:00:00Z","until":"2026-10-04T09:00:00Z"}]}`,
		`{"requests":[]} trailing`,
	} {
		if _, err := Decode(payload); err == nil {
			t.Errorf("accepted %s", payload)
		}
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
