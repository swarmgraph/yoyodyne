package mcpcall

// The record of every MCP call the harness makes
// (docs/designs/mcp-tools-through-the-harness.md, section 4).
//
// Each call is one entry. Its first line is written before the call is made and
// says who asked for what; its second is written once the call has settled and
// says what happened. Nothing is ever rewritten or removed: both lines are
// appended, through the confined writer, to a file of this process's own for
// the day, so no two processes ever write one file and nothing here needs a
// lock between them. A call refused before anything was called is one line,
// already settled.
//
// An entry whose first line is there and whose second is not is a call the
// process making it never finished — killed, or the machine stopped. Read back,
// an act call left that way is unknown, because it may have happened, and a
// read call is failed, because a read changes nothing and so there is nothing
// to be unsure about.
//
// The record stays on this machine. It holds up to maxRecordedResultBytes of
// each result, which is more than reaches an agent, and the surfaces that show
// it (build piece 7) show names, outcomes and sizes rather than content.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// RecordSchemaVersion is 1 and has never changed.
const RecordSchemaVersion = 1

// RecordDirectoryName is the directory under the product's state directory the
// record is kept in. It is beside the mcp directory rather than inside it,
// because every name inside that one is a server's.
const RecordDirectoryName = "mcp-calls"

// maxRecordedResultBytes is how much of a result the record keeps.
const maxRecordedResultBytes = 256 << 10

// maxRecordFileBytes bounds what of one file is read back.
const maxRecordFileBytes = 256 << 20

// maxRecordLineBytes bounds one line read back: a result at its bound, with
// its arguments and its JSON escaping, fits well inside it.
const maxRecordLineBytes = 4 << 20

// Phase is which of an entry's two lines a line is.
type Phase string

const (
	// PhaseAsked is the line written before the call is made.
	PhaseAsked Phase = "asked"
	// PhaseSettled is the line written once the outcome is known, and the only
	// line of a call refused before anything was called.
	PhaseSettled Phase = "settled"
)

// Outcome is how a call ended.
type Outcome string

const (
	// OutcomePerformed is a call the server answered.
	OutcomePerformed Outcome = "performed"
	// OutcomeRefused is a call the harness refused; nothing was called.
	OutcomeRefused Outcome = "refused"
	// OutcomeFailed is a call that did not happen, or a read that did not get
	// an answer, or a call the server answered by saying the tool failed.
	OutcomeFailed Outcome = "failed"
	// OutcomeUnknown is an act call the harness cannot tell happened or not.
	OutcomeUnknown Outcome = "unknown"
)

// CallerRecord is who asked, as the record keeps it.
type CallerRecord struct {
	Kind         CallerKind       `json:"kind"`
	Role         domain.AgentRole `json:"role,omitempty"`
	Agent        string           `json:"agent,omitempty"`
	Conversation string           `json:"conversation,omitempty"`
	Turn         int              `json:"turn,omitempty"`
	Pass         string           `json:"pass,omitempty"`
	Run          string           `json:"run,omitempty"`
	Attempt      int              `json:"attempt,omitempty"`
	Task         string           `json:"task,omitempty"`
}

// Record is one line of the record. A settled line carries everything its
// asked line did, so an entry reads whole from its last line.
type Record struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Entry identifies the call; both of its lines carry it.
	Entry string `json:"entry"`
	Phase Phase  `json:"phase"`
	// Scope is whose ids the call's id is one of: the conversation and agent,
	// the run, or the recurring task.
	Scope  string       `json:"scope"`
	Caller CallerRecord `json:"caller"`
	// PID is the process that made the call.
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`

	Server string              `json:"server,omitempty"`
	Tool   string              `json:"tool"`
	Class  config.MCPToolClass `json:"class,omitempty"`
	// ConfigRevision is the configuration the call was checked against.
	ConfigRevision string `json:"config_revision,omitempty"`
	// ListingDigest is the digest of the server's tool listing in the batch
	// the call was made in.
	ListingDigest string `json:"listing_digest,omitempty"`
	// Arguments are the arguments with secrets removed; a call refused for
	// holding a secret keeps none.
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// ArgumentsDigest is what a repeat is compared with.
	ArgumentsDigest string `json:"arguments_digest,omitempty"`
	ID              string `json:"id,omitempty"`
	Why             string `json:"why,omitempty"`

	Outcome Outcome       `json:"outcome,omitempty"`
	Refusal RefusalReason `json:"refusal,omitempty"`
	// Reason says, in words, why a call was refused or failed, or what is not
	// known about it.
	Reason string `json:"reason,omitempty"`
	// Tries is how many times the call was put to the server: two where a read
	// was tried once more.
	Tries int `json:"tries,omitempty"`

	// Result is the result as the call path rendered it, with secrets removed
	// and binary content dropped, cut at maxRecordedResultBytes.
	Result       string `json:"result,omitempty"`
	ResultBytes  int    `json:"result_bytes,omitempty"`
	ResultDigest string `json:"result_digest,omitempty"`
	// ResultCut says the record kept only part of the result.
	ResultCut bool `json:"result_cut,omitempty"`
	// Dropped is each item of content dropped because it was not text or JSON.
	Dropped []Dropped `json:"dropped,omitempty"`
	// ToolError is the server saying the tool itself failed.
	ToolError bool `json:"tool_error,omitempty"`
}

// Dropped is one item of a result that was not text or JSON.
type Dropped struct {
	Type  string `json:"type"`
	Bytes int    `json:"bytes"`
}

// Records is where one product's calls are kept and read back.
type Records struct {
	stateRoot string
	productID domain.ProductID
	pid       int
	now       func() time.Time

	mu sync.Mutex
}

// NewRecords keeps the record for one product under a state root, which must
// exist.
func NewRecords(stateRoot string, productID domain.ProductID) (*Records, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(stateRoot) {
		return nil, errors.New("the state root the MCP call record is kept under must be an absolute path")
	}
	return &Records{stateRoot: filepath.Clean(stateRoot), productID: productID, pid: os.Getpid(), now: time.Now}, nil
}

// Directory is where the record is kept, so a failure can say where.
func (r *Records) Directory() string {
	return filepath.Join(home.ProductDirectory(r.stateRoot, string(r.productID)), RecordDirectoryName)
}

func (r *Records) append(record Record) error {
	record.SchemaVersion = RecordSchemaVersion
	record.ProductID = r.productID
	record.PID = r.pid
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	root, err := repowrite.NewRoot(r.stateRoot)
	if err != nil {
		return fmt.Errorf("the state directory for the MCP call record: %w", err)
	}
	name := r.now().UTC().Format("20060102") + "-" + strconv.Itoa(r.pid) + ".jsonl"
	relative := path.Join(home.ProductDirectoryWithin(r.stateRoot, string(r.productID)), RecordDirectoryName, name)
	r.mu.Lock()
	defer r.mu.Unlock()
	file, err := root.OpenAppend(relative, 0o600, 0o700)
	if err != nil {
		return fmt.Errorf("open the MCP call record: %w", err)
	}
	_, writeErr := file.Write(append(encoded, '\n'))
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		return fmt.Errorf("write the MCP call record: %w", err)
	}
	return nil
}

var recordFile = regexp.MustCompile(`^\d{8}-\d+\.jsonl$`)

// Read returns every call recorded for the product, one entry each, in the
// order they started, and how many lines could not be read. An entry whose
// call was never settled reads as unknown for an act and failed for a read
// (unsettled).
func (r *Records) Read() ([]Record, int, error) {
	directory := r.Directory()
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read the MCP call record in %s: %w", directory, err)
	}
	latest := map[string]Record{}
	var order []string
	unreadable := 0
	for _, entry := range entries {
		if !recordFile.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		data, err := readBounded(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, 0, fmt.Errorf("read the MCP call record %s: %w", entry.Name(), err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 0, 64<<10), maxRecordLineBytes)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var record Record
			if err := json.Unmarshal(line, &record); err != nil || record.SchemaVersion != RecordSchemaVersion || record.ProductID != r.productID || record.Entry == "" {
				// A line a killed process left half written, or one written by a
				// schema this build does not know.
				unreadable++
				continue
			}
			previous, seen := latest[record.Entry]
			if !seen {
				order = append(order, record.Entry)
			}
			if !seen || previous.Phase != PhaseSettled {
				latest[record.Entry] = record
			}
		}
		if scanner.Err() != nil {
			unreadable++
		}
	}
	records := make([]Record, 0, len(order))
	for _, entry := range order {
		records = append(records, unsettled(latest[entry]))
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].StartedAt.Before(records[j].StartedAt) })
	return records, unreadable, nil
}

// unsettled is an entry as it reads back when its call never settled.
func unsettled(record Record) Record {
	if record.Phase == PhaseSettled {
		return record
	}
	if record.Class == config.MCPAct {
		record.Outcome = OutcomeUnknown
		record.Reason = "the harness stopped before this act call settled, so whether it happened is not known; check before asking again"
		return record
	}
	record.Outcome = OutcomeFailed
	record.Reason = "the harness stopped before this read call settled; a read changes nothing, so it can be asked again"
	return record
}

func readBounded(file string) ([]byte, error) {
	handle, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	return io.ReadAll(io.LimitReader(handle, maxRecordFileBytes))
}
