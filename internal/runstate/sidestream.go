package runstate

// Where side conversations live: in the same operating-system state root as runs,
// conversations, and exchanges, product-scoped and beside all three rather than
// inside any of them.
//
// Beside the conversations rather than among them is the placement that matters.
// A side stream is its own thread with its own lease, and a conversation store
// keyed by agent has exactly one record and one lease per agent — so a stream
// kept there would either be a second record fighting for that agent's name or a
// second holder of the lease the main thread serializes on. Its own directory is
// what makes "the main thread's lease is untouched by it" a fact about where the
// files are rather than a rule somebody has to keep.
//
// The transcripts cannot meet either, and that is enforced twice over. A side
// stream's log is named for a `side-` identifier in this directory, and a
// conversation's for a `chat-` identifier in that one; each store validates the
// identifier before it is a path, so an event handed to the wrong store is
// refused rather than appended to a log that then holds two threads.
//
// It is a file per stream rather than an append-only log, because a stream is
// revised as it goes: written when it opens, again as each turn lands, and once
// more when it concludes. The revision is a temporary file and a rename, as every
// other revised record here is, so a process that dies mid-write leaves the
// previous state rather than a truncated file nothing can read.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
)

// ErrNoSideStream reports an identifier that names nothing recorded.
var ErrNoSideStream = sidestream.ErrNoStream

// SideStreamStore is one product's side conversations.
type SideStreamStore struct {
	root      string
	productID domain.ProductID
}

func NewSideStreamStore(root string, productID domain.ProductID) (*SideStreamStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &SideStreamStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "sidestreams"),
		productID: productID,
	}, nil
}

func (s *SideStreamStore) Root() string { return s.root }

// Open records a stream that is being opened, and refuses one the agent has no
// room for.
//
// The bound is passed rather than read here for the reason an exchange's round
// cap is passed: how many side threads an agent may hold at once is the project's
// configuration, and a store that read it would be a second place the number
// lives. What this decides is the part that has to be decided under the records
// rather than above them — whether the agent is already at its bound, and whether
// this identifier is already taken.
//
// Counting what an agent holds and adding to it is one decision, so it is taken
// under a lease of the agent's own. Two processes opening at once would otherwise
// each count the same open streams and each open, and the bound would hold for
// neither. That lease is over opening rather than over any conversation: it is
// dropped before the stream is carried, and it is not the stream's lease and not
// the main thread's.
//
// A stream already recorded is refused rather than replaced: opening is a first
// write, and the way a stream is revised afterwards is Save.
func (s *SideStreamStore) Open(stream sidestream.Stream, bound int) error {
	if err := s.validate(stream); err != nil {
		return err
	}
	if bound < 1 {
		return fmt.Errorf("side stream bound is %d; an agent configured for side threads is allowed at least one", bound)
	}
	if !stream.Open() {
		return fmt.Errorf("side stream %s is opened with the outcome %q already on it", stream.ID, stream.Outcome)
	}
	admitting, taken, err := TryLeasePath(
		filepath.Join(s.root, "leases", stream.Agent+".opening.lease"),
		"opening a side stream for "+stream.Agent)
	if err != nil {
		return err
	}
	if !taken {
		return fmt.Errorf("another process is opening a side stream for %s", stream.Agent)
	}
	return errors.Join(s.admit(stream, bound), admitting.Release())
}

// admit is Open's decision, made with the agent's opening lease held.
func (s *SideStreamStore) admit(stream sidestream.Stream, bound int) error {
	path, err := s.path(stream.ID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("side stream %s is already recorded", stream.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("look for side stream %s: %w", stream.ID, err)
	}
	held, err := s.OpenFor(stream.Agent)
	if err != nil {
		return err
	}
	if len(held) >= bound {
		return fmt.Errorf("%s is %w: %d of %d", stream.Agent, sidestream.ErrTooManyStreams, len(held), bound)
	}
	return s.Save(stream)
}

// Save makes one side stream durable, whether it is being opened, taken another
// turn, or concluded. It is a replacement rather than an append because the
// record is one thread rather than a stream of events about one: what a reader
// wants is the thread as it now stands, and what makes that safe is that only one
// turn of one stream is ever in flight — which is Hold's guarantee below rather
// than a convention.
func (s *SideStreamStore) Save(stream sidestream.Stream) error {
	if err := s.validate(stream); err != nil {
		return err
	}
	path, err := s.path(stream.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create side stream directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".sidestream-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary side stream: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary side stream: %w", err)
	}
	if err := writeJSONFile(temporary, "side stream", stream); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary side stream: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace side stream: %w", err)
	}
	return syncDirectory(s.root)
}

// Load reads one side stream by its full identifier.
func (s *SideStreamStore) Load(id string) (sidestream.Stream, error) {
	path, err := s.path(id)
	if err != nil {
		return sidestream.Stream{}, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return sidestream.Stream{}, fmt.Errorf("%w: %s", ErrNoSideStream, id)
	}
	if err != nil {
		return sidestream.Stream{}, fmt.Errorf("open side stream: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var loaded sidestream.Stream
	if err := decoder.Decode(&loaded); err != nil {
		return sidestream.Stream{}, fmt.Errorf("decode side stream %s: %w", id, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return sidestream.Stream{}, fmt.Errorf("decode side stream %s: %w", id, err)
	}
	if loaded.ID != id {
		return sidestream.Stream{}, fmt.Errorf("side stream file %s holds side stream %s", id, loaded.ID)
	}
	if err := s.validate(loaded); err != nil {
		return sidestream.Stream{}, err
	}
	return loaded, nil
}

// Records names every side stream recorded for this product, by identifier and in
// directory order. It answers nil for a product whose agents have never held one,
// which is how a directory that does not exist yet is told from one that holds no
// streams.
func (s *SideStreamStore) Records() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read side stream directory: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(entry.Name(), ".json"))
	}
	return ids, nil
}

// List returns every recorded side stream. One record it cannot read fails the
// whole listing, for the reason the exchange listing does: this answers "what is
// this agent holding beside its main thread", and an answer quietly missing a
// thread is worse than no answer.
func (s *SideStreamStore) List() ([]sidestream.Stream, error) {
	ids, err := s.Records()
	if err != nil {
		return nil, err
	}
	if ids == nil {
		return nil, nil
	}
	streams := make([]sidestream.Stream, 0, len(ids))
	for _, id := range ids {
		loaded, err := s.Load(id)
		if err != nil {
			return nil, err
		}
		streams = append(streams, loaded)
	}
	return streams, nil
}

// OpenFor is the side streams one agent is holding right now. It is what the
// per-agent bound is counted against, and it is separated from List so that a
// caller asking whether there is room does not have to know how a stream says it
// is finished.
func (s *SideStreamStore) OpenFor(agent string) ([]sidestream.Stream, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return nil, err
	}
	recorded, err := s.List()
	if err != nil {
		return nil, err
	}
	var held []sidestream.Stream
	for _, stream := range recorded {
		if stream.Agent == agent && stream.Open() {
			held = append(held, stream)
		}
	}
	return held, nil
}

// Hold takes the exclusive lease on one side stream without waiting, reporting
// whether it got it.
//
// It is one holder per side stream, and it is deliberately not the conversation
// lease. The single-holder rule the design states governs the main thread, and a
// side thread never takes it: this lease is on its own file, under its own
// identifier, in a directory the conversation store does not read — so a stream
// being held says nothing about whether the main thread is, and holding the main
// thread stops nothing here.
//
// It is also how a restart tells an interrupted turn from one being taken. A
// stream on disk part way through looks identical either way; the lease is the
// difference, because the operating system drops it when its holder exits. Asking
// whether somebody holds it and taking it afterwards would leave a window between
// the two, so taking it is the question.
//
// The lease lives in a directory beside the records rather than among them, which
// the enumeration skips: a lease is about who is working on a stream and not part
// of what the stream says. It answers the lease as the interface the sidestream
// package states, because that package cannot import this one.
func (s *SideStreamStore) Hold(id string) (sidestream.Release, bool, error) {
	if !sidestream.ValidID(id) {
		return nil, false, fmt.Errorf("side stream id %q is invalid", id)
	}
	lease, taken, err := TryLeasePath(filepath.Join(s.root, "leases", id+".lease"), "side stream "+id)
	if err != nil || !taken {
		// A typed nil handed back as an interface is not nil, and a caller checking
		// what it got would find a lease it does not have.
		return nil, taken, err
	}
	return lease, true, nil
}

// AppendEvent persists one normalized event from a side stream. The log is named
// for the stream, and an event naming anything else is refused: a conversation's
// event written here would be the interleaved transcript the design forbids, and
// it is a failure rather than a log with two threads in it.
func (s *SideStreamStore) AppendEvent(event execution.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	path, err := s.eventPath(event.RunID)
	if err != nil {
		return err
	}
	encoded, err := encodeEvent(event)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedEventBytes {
		return fmt.Errorf("encoded event is %d bytes, limit is %d", len(encoded), maxEncodedEventBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create side stream directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open side stream event log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append side stream event: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append side stream event: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync side stream event log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close side stream event log: %w", err)
	}
	return nil
}

// LoadEvents returns one side stream's normalized events in the order they were
// recorded. An event belonging to another thread fails the read rather than being
// returned as part of this one.
func (s *SideStreamStore) LoadEvents(id string) ([]execution.Event, error) {
	path, err := s.eventPath(id)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open side stream event log: %w", err)
	}
	defer file.Close()

	var events []execution.Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedEventBytes)
	for scanner.Scan() {
		event, err := execution.DecodeEvent(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("decode side stream event log for %s: %w", id, err)
		}
		if event.RunID != id {
			return nil, fmt.Errorf("decode side stream event log for %s: event belongs to %s", id, event.RunID)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read side stream event log: %w", err)
	}
	return events, nil
}

func (s *SideStreamStore) validate(stream sidestream.Stream) error {
	if stream.ProductID != s.productID {
		return fmt.Errorf("side stream product %q does not match store product %q", stream.ProductID, s.productID)
	}
	return stream.Validate()
}

// path names one side stream's file. The identifier is checked against its own
// pattern first, so nothing that came from outside can name a path — and a
// conversation identifier is not that pattern, which is what keeps the two kinds
// of record out of each other's directories.
func (s *SideStreamStore) path(id string) (string, error) {
	if !sidestream.ValidID(id) {
		return "", fmt.Errorf("side stream id %q is invalid", id)
	}
	return filepath.Join(s.root, id+".json"), nil
}

func (s *SideStreamStore) eventPath(id string) (string, error) {
	if !sidestream.ValidID(id) {
		return "", fmt.Errorf("side stream id %q is invalid", id)
	}
	return filepath.Join(s.root, id+".events.jsonl"), nil
}
