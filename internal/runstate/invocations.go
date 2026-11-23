package runstate

// What each developer and reviewer invocation was sent, kept whole. The ruling
// is docs/designs/what-each-invocation-was-sent.md.
//
// Layout, under invocations/ beside the run records in the product's state
// directory:
//
//	parts/<first two hex digits>/<digest>.gz   one file for each distinct part text
//	runs/<run id>/<number>.json                one entry for each invocation
//
// A part is named by the SHA-256 of its text and stored compressed, once, so the
// contract and persona that many invocations share cost one file. An entry holds
// the settings an invocation was started with and its parts in order, each with
// its name, digest, size and where it came from, and the digest of the whole.
// Both kinds of file are published whole under their final names through the
// confined writer and never changed afterwards, so an entry is either there
// complete or not there.

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// InvocationSchemaVersion is the version of an invocation entry's shape.
const InvocationSchemaVersion = 1

// maxInvocationPartBytes bounds how much of one stored part is read back. A part
// is the harness's own text, which the provider's request limit already bounds,
// so this only keeps a damaged file from being read without end.
const maxInvocationPartBytes = 64 << 20

// InvocationPart is one stored part of one message, by name and digest.
type InvocationPart struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
	Digest string `json:"digest"`
	Size   int    `json:"size"`
}

// InvocationMessage is one message the provider was given, as its parts in order.
type InvocationMessage struct {
	Role  string           `json:"role"`
	Parts []InvocationPart `json:"parts"`
}

// InvocationEntry is the record of one invocation, written before the provider
// was started.
type InvocationEntry struct {
	SchemaVersion    int                 `json:"schema_version"`
	RunID            string              `json:"run_id"`
	Number           int                 `json:"number"`
	Role             string              `json:"role"`
	StartedAt        time.Time           `json:"started_at"`
	Backend          string              `json:"backend"`
	ProviderVersion  string              `json:"provider_version,omitempty"`
	Model            string              `json:"model,omitempty"`
	Effort           string              `json:"effort,omitempty"`
	Tools            []string            `json:"tools,omitempty"`
	Posture          string              `json:"posture,omitempty"`
	WorkingDirectory string              `json:"working_directory,omitempty"`
	Commit           string              `json:"commit,omitempty"`
	Resumed          string              `json:"resumed_session,omitempty"`
	Messages         []InvocationMessage `json:"messages"`
	WholeDigest      string              `json:"whole_digest"`
	WholeSize        int                 `json:"whole_size"`
}

// Parts is every part of every message, in the order sent.
func (e InvocationEntry) Parts() []InvocationPart {
	var parts []InvocationPart
	for _, message := range e.Messages {
		parts = append(parts, message.Parts...)
	}
	return parts
}

// SentMessageText is one message of an invocation as it was sent.
type SentMessageText struct {
	Role string
	Text string
}

// InvocationText is an invocation's entry with the exact text of each message,
// read back and checked against the digests the entry holds.
type InvocationText struct {
	Entry    InvocationEntry
	Messages []SentMessageText
}

// InvocationRecordError is a part or entry that could not be read back as it was
// written: it is missing, unreadable, or its content no longer matches its
// digest. What was lost is said, and nothing is filled in for it.
type InvocationRecordError struct {
	RunID  string
	Number int
	Detail string
}

func (e InvocationRecordError) Error() string {
	return fmt.Sprintf("invocation %d of run %s cannot be shown as it was sent: %s", e.Number, e.RunID, e.Detail)
}

// ErrNoInvocation says a run has no such invocation recorded.
var ErrNoInvocation = errors.New("no such invocation is recorded")

var invocationEntryName = regexp.MustCompile(`^([0-9]{6})\.json$`)

// InvocationStore keeps what each invocation was sent.
type InvocationStore struct {
	stateRoot string
	anchor    string
	mu        sync.Mutex
}

// Invocations is the store of what each invocation was sent, beside the run
// records.
func (s *Store) Invocations() *InvocationStore {
	stateRoot, anchor, err := confinedStateRoot(filepath.Join(filepath.Dir(s.root), "invocations"))
	if err != nil {
		// A root that cannot be resolved is reported by every use of it, as an
		// invocation that cannot be recorded.
		return &InvocationStore{stateRoot: "", anchor: ""}
	}
	return &InvocationStore{stateRoot: stateRoot, anchor: anchor}
}

// Recorder is the recorder for one invocation of one run in one role. Each call
// to RecordInvocation on it records a new invocation of that run, at the time
// now says, with commit the commit the working directory was at.
func (s *InvocationStore) Recorder(runID string, role domain.AgentRole, commit string, now func() time.Time) backend.InvocationRecorder {
	if now == nil {
		now = time.Now
	}
	return invocationRecorder{store: s, runID: runID, role: role, commit: commit, now: now}
}

type invocationRecorder struct {
	store  *InvocationStore
	runID  string
	role   domain.AgentRole
	commit string
	now    func() time.Time
}

func (r invocationRecorder) RecordInvocation(sent backend.SentInvocation) error {
	_, err := r.store.Record(r.runID, r.role, r.commit, r.now().UTC(), sent)
	return err
}

func digestOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// wholeDigest is the digest of every message in order, each framed with its role
// and length so that moving text from one message to the next changes it.
func wholeDigest(messages []SentMessageText) string {
	hash := sha256.New()
	for _, message := range messages {
		fmt.Fprintf(hash, "%s\n%d\n", message.Role, len(message.Text))
		io.WriteString(hash, message.Text)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func partFile(digest string) string {
	return path.Join("parts", digest[:2], digest+".gz")
}

func (s *InvocationStore) runDirectory(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errors.New("run id is invalid")
	}
	return path.Join("runs", runID), nil
}

// Record keeps what one invocation is about to be sent, and returns its entry.
// Every part is written before the entry, so an entry that exists names parts
// that do. A part already stored under its digest is not written again.
func (s *InvocationStore) Record(runID string, role domain.AgentRole, commit string, startedAt time.Time, sent backend.SentInvocation) (InvocationEntry, error) {
	if s.stateRoot == "" {
		return InvocationEntry{}, errors.New("the invocation store's directory cannot be resolved")
	}
	directory, err := s.runDirectory(runID)
	if err != nil {
		return InvocationEntry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return InvocationEntry{}, err
	}
	defer root.Close()

	entry := InvocationEntry{
		SchemaVersion:    InvocationSchemaVersion,
		RunID:            runID,
		Role:             string(role),
		StartedAt:        startedAt,
		Backend:          sent.Backend,
		ProviderVersion:  sent.ProviderVersion,
		Model:            sent.Model,
		Effort:           sent.Effort,
		Tools:            sent.Tools,
		Posture:          sent.Posture,
		WorkingDirectory: sent.WorkingDirectory,
		Commit:           commit,
		Resumed:          sent.Resumed,
	}
	var whole []SentMessageText
	for _, message := range sent.Messages {
		recorded := InvocationMessage{Role: message.Role}
		for _, part := range message.Parts {
			digest := digestOf(part.Text)
			if err := writePart(root, digest, part.Text); err != nil {
				return InvocationEntry{}, fmt.Errorf("keep the part %q: %w", part.Name, err)
			}
			recorded.Parts = append(recorded.Parts, InvocationPart{Name: part.Name, Origin: part.Origin, Digest: digest, Size: len(part.Text)})
		}
		entry.Messages = append(entry.Messages, recorded)
		whole = append(whole, SentMessageText{Role: message.Role, Text: message.Text()})
	}
	entry.WholeDigest = wholeDigest(whole)
	for _, message := range whole {
		entry.WholeSize += len(message.Text)
	}

	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return InvocationEntry{}, err
	}
	// The number is the next free one in the run's directory. A name taken
	// between the listing and the write means another process recorded for the
	// same run, so the next number is tried.
	for attempt := 0; attempt < 8; attempt++ {
		number, err := nextInvocationNumber(root, directory)
		if err != nil {
			return InvocationEntry{}, err
		}
		entry.Number = number
		encoded, err := json.MarshalIndent(entry, "", "  ")
		if err != nil {
			return InvocationEntry{}, err
		}
		err = root.CreateFile(path.Join(directory, fmt.Sprintf("%06d.json", number)), append(encoded, '\n'), 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return InvocationEntry{}, err
		}
		return entry, nil
	}
	return InvocationEntry{}, errors.New("another process kept recording invocations for this run while this one was written")
}

func nextInvocationNumber(root interface {
	ReadDirectory(string) ([]fs.DirEntry, error)
}, directory string) (int, error) {
	entries, err := root.ReadDirectory(directory)
	if err != nil {
		return 0, err
	}
	highest := 0
	for _, entry := range entries {
		if match := invocationEntryName.FindStringSubmatch(entry.Name()); match != nil {
			number, _ := strconv.Atoi(match[1])
			if number > highest {
				highest = number
			}
		}
	}
	return highest + 1, nil
}

func writePart(root interface {
	MakeDirectory(string, fs.FileMode) error
	Exists(string) (bool, error)
	CreateFile(string, []byte, fs.FileMode) error
}, digest, text string) error {
	name := partFile(digest)
	if found, err := root.Exists(name); err != nil {
		return err
	} else if found {
		return nil
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(writer, text); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := root.MakeDirectory(path.Dir(name), 0o700); err != nil {
		return err
	}
	if err := root.CreateFile(name, compressed.Bytes(), 0o600); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// List is every invocation recorded for a run, in the order they started. A run
// with none returns an empty list.
func (s *InvocationStore) List(runID string) ([]InvocationEntry, error) {
	if s.stateRoot == "" {
		return nil, errors.New("the invocation store's directory cannot be resolved")
	}
	directory, err := s.runDirectory(runID)
	if err != nil {
		return nil, err
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	entries, err := root.ReadDirectory(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var listed []InvocationEntry
	for _, file := range entries {
		match := invocationEntryName.FindStringSubmatch(file.Name())
		if match == nil {
			continue
		}
		number, _ := strconv.Atoi(match[1])
		entry, err := readInvocationEntry(root, runID, number)
		if err != nil {
			return listed, err
		}
		listed = append(listed, entry)
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].Number < listed[j].Number })
	return listed, nil
}

type invocationReader interface {
	ReadFile(string) ([]byte, error)
}

func readInvocationEntry(root invocationReader, runID string, number int) (InvocationEntry, error) {
	content, err := root.ReadFile(path.Join("runs", runID, fmt.Sprintf("%06d.json", number)))
	if errors.Is(err, fs.ErrNotExist) {
		return InvocationEntry{}, ErrNoInvocation
	}
	if err != nil {
		return InvocationEntry{}, InvocationRecordError{RunID: runID, Number: number, Detail: "its entry cannot be read: " + err.Error()}
	}
	var entry InvocationEntry
	if err := json.Unmarshal(content, &entry); err != nil {
		return InvocationEntry{}, InvocationRecordError{RunID: runID, Number: number, Detail: "its entry cannot be decoded: " + err.Error()}
	}
	if entry.SchemaVersion != InvocationSchemaVersion {
		return InvocationEntry{}, InvocationRecordError{RunID: runID, Number: number, Detail: fmt.Sprintf("its entry is of version %d, and this build reads version %d", entry.SchemaVersion, InvocationSchemaVersion)}
	}
	return entry, nil
}

// Text reads one invocation back with the exact text of each message. Every
// part's content is checked against its digest and the whole against the
// entry's, and a part that is missing or does not match is reported as that,
// never filled in.
func (s *InvocationStore) Text(runID string, number int) (InvocationText, error) {
	if s.stateRoot == "" {
		return InvocationText{}, errors.New("the invocation store's directory cannot be resolved")
	}
	if _, err := s.runDirectory(runID); err != nil {
		return InvocationText{}, err
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return InvocationText{}, err
	}
	defer root.Close()
	entry, err := readInvocationEntry(root, runID, number)
	if err != nil {
		return InvocationText{}, err
	}
	fail := func(format string, args ...any) (InvocationText, error) {
		return InvocationText{}, InvocationRecordError{RunID: runID, Number: number, Detail: fmt.Sprintf(format, args...)}
	}
	result := InvocationText{Entry: entry}
	for _, message := range entry.Messages {
		var text strings.Builder
		for _, part := range message.Parts {
			if len(part.Digest) != sha256.Size*2 {
				return fail("the part %q names a digest that is not a SHA-256", part.Name)
			}
			content, err := readPart(root, part.Digest)
			if errors.Is(err, fs.ErrNotExist) {
				return fail("the part %q (%s) is missing from the store", part.Name, part.Digest[:12])
			}
			if err != nil {
				return fail("the part %q (%s) cannot be read: %v", part.Name, part.Digest[:12], err)
			}
			if digestOf(content) != part.Digest {
				return fail("the part %q (%s) no longer matches its digest", part.Name, part.Digest[:12])
			}
			text.WriteString(content)
		}
		result.Messages = append(result.Messages, SentMessageText{Role: message.Role, Text: text.String()})
	}
	if wholeDigest(result.Messages) != entry.WholeDigest {
		return fail("the parts joined do not match the digest of the whole text")
	}
	return result, nil
}

func readPart(root invocationReader, digest string) (string, error) {
	compressed, err := root.ReadFile(partFile(digest))
	if err != nil {
		return "", err
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", err
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, maxInvocationPartBytes+1))
	if err != nil {
		return "", err
	}
	if len(content) > maxInvocationPartBytes {
		return "", errors.New("it is larger than any part the harness sends")
	}
	return string(content), nil
}
