package logread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Reader is scoped by trusted wiring, never by the role's request.
type Reader struct {
	StateRoot    string
	ProductID    domain.ProductID
	RedactValues []string
}

func (r Reader) Read(ctx context.Context, requests []Request) ([]Result, error) {
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(r.StateRoot) {
		return nil, errors.New("log reader needs an absolute state root")
	}
	if len(requests) < 1 || len(requests) > MaxRequestsPerReply {
		return nil, errors.New("log request count exceeds the descriptor bounds")
	}
	for _, request := range requests {
		if err := request.Validate(); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(r.StateRoot)
	if err != nil {
		return nil, errors.New("the log reader could not open the state root")
	}
	defer root.Close()
	redactor := execution.NewRedactor(r.RedactValues...)
	remaining := MaxBytesPerReply
	results := make([]Result, 0, len(requests))
	for _, request := range requests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result := r.read(root, request, redactor, remaining)
		remaining -= result.Bytes
		results = append(results, result)
	}
	return results, nil
}

func recordPath(request Request) string {
	switch request.Record {
	case "pass":
		return "sweeps/sweeps.jsonl"
	case "watch":
		if request.Name == "output" {
			return "scheduler.log"
		}
		return "watch.jsonl"
	case "docket":
		return "docket.jsonl"
	case "usage-limit":
		return "usage-limits.jsonl"
	case "provider-outage":
		return "provider-outage.json"
	case "run":
		return "runs/" + request.Name + ".json"
	}
	return ""
}

// openRecord pins each directory, refuses every symlink, and verifies that the
// regular file opened is the one inspected. os.Root also confines a replacement
// racing the open. The request cannot choose any component of this path.
func openRecord(root *os.Root, relative string) (*os.File, error) {
	components := strings.Split(relative, "/")
	parent := root
	for _, component := range components[:len(components)-1] {
		info, err := parent.Lstat(component)
		if err != nil || !info.IsDir() {
			if parent != root {
				parent.Close()
			}
			return nil, errors.New("record directory is missing or is a link")
		}
		child, err := parent.OpenRoot(component)
		if parent != root {
			parent.Close()
		}
		if err != nil {
			return nil, errors.New("record directory could not be opened")
		}
		opened, err := child.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			child.Close()
			return nil, errors.New("record directory changed while opening it")
		}
		parent = child
	}
	if parent != root {
		defer parent.Close()
	}
	name := components[len(components)-1]
	info, err := parent.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("named record is missing or is not a regular file")
	}
	file, err := parent.Open(name)
	if err != nil {
		return nil, errors.New("named record could not be opened")
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		file.Close()
		return nil, errors.New("named record changed while opening it")
	}
	return file, nil
}

func (r Reader) read(root *os.Root, request Request, redactor execution.Redactor, remaining int) Result {
	result := Result{Record: request.Record, Name: request.Name}
	file, err := openRecord(root, "products/"+string(r.ProductID)+"/"+recordPath(request))
	if err != nil {
		result.Problem = err.Error()
		return result
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		result.Problem = "record size could not be read"
		return result
	}
	start := int64(0)
	if request.Cursor != nil {
		start = *request.Cursor
	}
	result.NextCursor = start
	if start > info.Size() {
		result.Problem = "cursor is beyond the record"
		return result
	}
	single := request.Record == "run" || request.Record == "provider-outage"
	if start != 0 && start != info.Size() {
		if single {
			result.Problem = "cursor must be zero or the end of a single JSON record"
			return result
		}
		var previous [1]byte
		if _, err := file.ReadAt(previous[:], start-1); err != nil || previous[0] != '\n' {
			result.Problem = "cursor is not at a record boundary"
			return result
		}
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		result.Problem = "record cursor could not be read"
		return result
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxScanBytes+1))
	if err != nil {
		result.Problem = "named record could not be read"
		return result
	}
	scanCut := len(data) > MaxScanBytes
	if scanCut {
		end := MaxScanBytes
		for end > 0 && !utf8.RuneStart(data[end]) {
			end--
		}
		data = data[:end]
	}
	if single && scanCut {
		result.Truncated = true
		result.Problem = "single record exceeds the 8 MiB scan bound"
		return result
	}
	entries := [][]byte{data}
	if !single {
		// An incomplete final entry is not exposed: it may be a concurrent append
		// or a secret cut by the scan bound. The next cursor repeats it.
		last := bytes.LastIndexByte(data, '\n')
		if last != len(data)-1 {
			scanCut = true
			end := last + 1
			for end > 0 && end < len(data) && !utf8.RuneStart(data[end]) {
				end--
			}
			data = data[:end]
		}
		entries = bytes.SplitAfter(data, []byte{'\n'})
	}
	limit := min(request.MaxBytes, remaining)
	offset := start
	for _, entry := range entries {
		if len(entry) == 0 {
			continue
		}
		if request.Cursor == nil {
			at, err := recordTime(entry)
			if err != nil {
				result.Content = ""
				result.Bytes = 0
				result.Problem = "record has no readable timestamp; use a cursor"
				return result
			}
			if at.Before(request.Since) || !at.Before(request.Until) {
				offset += int64(len(entry))
				result.NextCursor = offset
				continue
			}
		}
		content := redactor.Redact(redactFields(entry, redactor))
		room := limit - len(result.Content)
		if len(content) > room {
			result.Truncated = true
			// Redact the complete entry before cutting it so a cut cannot expose a
			// prefix of a secret. A partial entry is repeated on the next request.
			content = cut(content, room)
			result.Content += content
			break
		}
		result.Content += content
		offset += int64(len(entry))
		result.NextCursor = offset
	}
	result.Truncated = result.Truncated || scanCut
	result.Bytes = len(result.Content)
	return result
}

func recordTime(data []byte) (time.Time, error) {
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		return time.Time{}, err
	}
	for _, key := range []string{"at", "updated_at", "last_seen", "started_at", "recorded_at", "since"} {
		if raw, ok := record[key]; ok {
			var at time.Time
			if err := json.Unmarshal(raw, &at); err == nil && !at.IsZero() {
				return at, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("record has no timestamp")
}

func cut(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// redactFields removes conventionally sensitive values from structured records,
// even when this machine does not know the value. Plain scheduler output still
// uses the shared provider-facing redactor.
func redactFields(data []byte, redactor execution.Redactor) string {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return string(data)
	}
	var walk func(any) any
	walk = func(value any) any {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				if len(execution.SensitiveEnvironmentValues([]string{key + "=sensitive"})) > 0 {
					value[key] = "[REDACTED]"
				} else {
					value[key] = walk(child)
				}
			}
		case []any:
			for index, child := range value {
				value[index] = walk(child)
			}
		case string:
			return redactor.Redact(value)
		}
		return value
	}
	value = walk(value)
	encoded, _ := json.Marshal(value)
	if bytes.HasSuffix(data, []byte{'\n'}) {
		encoded = append(encoded, '\n')
	}
	return string(encoded)
}
