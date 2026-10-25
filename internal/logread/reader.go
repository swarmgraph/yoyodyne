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
	"github.com/mason-bryant/yoyodyne/internal/home"
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
	case "intervention":
		return "interventions.jsonl"
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
	// The product's records directory is resolved the way every store resolves
	// it, so a home in either layout is read where its records are.
	file, err := openRecord(root, home.ProductDirectoryWithin(r.StateRoot, string(r.ProductID))+"/"+recordPath(request))
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
	result.Size = info.Size()
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
	windowed := !request.Since.IsZero()
	offset := start
	// stopped is set when the read ends before the end of what it scanned, and
	// nextOffset is where in the record at the next cursor the next read starts.
	stopped := false
	nextOffset := int64(0)
	for _, entry := range entries {
		if len(entry) == 0 {
			continue
		}
		atCursor := offset == start
		skip := false
		if windowed {
			at, err := recordTime(entry)
			if err != nil {
				return refuse(result, "record has no readable timestamp; use a cursor")
			}
			skip = at.Before(request.Since) || !at.Before(request.Until)
		}
		if !skip && request.Task != "" {
			skip = recordTask(entry) != request.Task
		}
		if skip {
			if atCursor && request.Offset > 0 {
				return refuse(result, "the record at the cursor is outside this read, so an offset into it names nothing")
			}
			offset += int64(len(entry))
			result.NextCursor = offset
			continue
		}
		// Redact the complete entry before cutting it, so no part can expose a
		// prefix of a secret, and so every part is cut from the same text.
		content := redactor.Redact(redactFields(entry, redactor))
		partStart := int64(0)
		if atCursor && request.Offset > 0 {
			if request.Offset >= int64(len(content)) {
				return refuse(result, "offset is beyond the end of the record at the cursor")
			}
			partStart = int64(characterStart(content, int(request.Offset)))
		}
		piece := content[partStart:]
		room := limit - len(result.Content)
		if len(piece) <= room {
			result.Content += piece
			offset += int64(len(entry))
			result.NextCursor = offset
			if partStart > 0 {
				// The last part of a record read in parts is returned alone.
				result.Part = &Part{Start: partStart}
				stopped = true
				break
			}
			continue
		}
		stopped = true
		result.Truncated = true
		if result.Content != "" {
			// Whole records only after the first; this one starts the next read.
			break
		}
		// One record longer than this read returns comes back in parts. Each
		// part ends at the start of a character, which is where the next begins.
		piece = piece[:characterStart(piece, room)]
		result.Content = piece
		nextOffset = partStart + int64(len(piece))
		result.Part = &Part{Start: partStart, Remaining: int64(len(content)) - nextOffset}
		break
	}
	result.Truncated = result.Truncated || scanCut
	result.Bytes = len(result.Content)
	if (stopped && result.NextCursor < result.Size) || scanCut || nextOffset > 0 {
		next := request
		cursor := result.NextCursor
		next.Cursor = &cursor
		next.Offset = nextOffset
		result.Next = &next
	}
	return result
}

func refuse(result Result, problem string) Result {
	result.Content = ""
	result.Bytes = 0
	result.Part = nil
	result.Problem = problem
	return result
}

// characterStart moves an index into value back to the start of the character
// it falls inside, so a part neither ends nor begins halfway through one.
func characterStart(value string, index int) int {
	if index >= len(value) {
		return len(value)
	}
	for index > 0 && !utf8.RuneStart(value[index]) {
		index--
	}
	return index
}

// recordTask is the recurring task a pass record names, or empty where it
// names none.
func recordTask(data []byte) string {
	var record struct {
		Task string `json:"task"`
	}
	if json.Unmarshal(data, &record) != nil {
		return ""
	}
	return record.Task
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
