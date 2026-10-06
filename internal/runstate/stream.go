package runstate

// Reading the event streams the harness is writing right now.
//
// A run, a conversation, a branch review, and a side conversation each record a
// normalized event stream, and each keeps it beside its own records rather than
// among the others'. It is the same question being asked of all four — is this
// alive, what is it doing, and what did it cost — so this reads all four as one
// collection and nothing that asks it has to say which kind it meant.
//
// A side conversation is priced here beside the conversation it was opened from
// rather than folded into it: its terminals are in a log of its own, and every
// row it yields names that conversation, so what a thread nobody was watching
// spent is in every total and still says whose it was.
//
// An inter-role exchange is priced beside the streams and is the only thing
// never followed: its record is the thread itself, revised as it goes, rather than a
// stream of events, so it appears in the spend report and in no other answer.
// What two roles spent asking each other is money the harness spent, and a
// total without it is short.
//
// It is read-only in the strongest sense: no lease is taken, nothing is
// adopted, and nothing is written, so a stream another process is appending to
// is read exactly as a finished one is. That is what lets an operator watch a
// live run without the watching being an act on it.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// eventLogSuffix is what names an event log among the records beside it. The
// branch review store keeps its shared verdict log in the same directory, and
// that log is not an event stream, so the suffix rather than the extension is
// what selects one.
const eventLogSuffix = ".events.jsonl"

// StreamKind is which of the recorded collections a priced or followed thing
// belongs to. The three that record an event stream are told apart by the id
// the stream is named for, which is the same thing their records are named for.
type StreamKind string

const (
	StreamRun          StreamKind = "run"
	StreamConversation StreamKind = "conversation"
	StreamReview       StreamKind = "review"
	// StreamSide is a side conversation: a bounded thread an agent holds beside
	// its main conversation, with a log and a record of its own.
	StreamSide StreamKind = "side"
	// StreamExchange is an inter-role exchange, which has no event stream: it is
	// priced beside the other three and never listed or followed.
	StreamExchange StreamKind = "exchange"
)

// EveryStreamKind is the kinds that record a stream, which is what a query that
// names none covers: asking whether anything is alive should never have
// required saying which kind of alive was meant.
var EveryStreamKind = []StreamKind{StreamRun, StreamConversation, StreamReview, StreamSide}

// EveryPricedKind is everything the spend report covers when nothing was
// narrowed: the streams and the exchanges beside them.
var EveryPricedKind = append(append([]StreamKind{}, EveryStreamKind...), StreamExchange)

// Followable reports a kind that records an event stream.
func (k StreamKind) Followable() bool { return k != StreamExchange }

// The statuses derived for the two kinds that keep no status of their own. A
// run records its own and is reported by it; these say what an operator is
// actually asking of the other two.
const (
	// ConversationAnswering is an agent working on a turn, and
	// ConversationWaiting is between turns, with the operator holding the ball.
	ConversationAnswering = "answering"
	ConversationWaiting   = "waiting"
	// ConversationEnded is a conversation the role has since replaced, in which
	// nothing will happen again.
	ConversationEnded = "ended"
	// ReviewInProgress is a verdict still being made and ReviewFinished is one
	// that has been.
	ReviewInProgress = "reviewing"
	ReviewFinished   = "reviewed"
	// ExchangeOpen is an exchange still being conducted; a closed one reports
	// its outcome in the one word `yoyo exchange` says it in.
	ExchangeOpen = "open"
	// SideStreamOpen is a side conversation its agent is still holding; one that
	// has ended reports the outcome its record ends with.
	SideStreamOpen = "open"
	// StreamStatusUnknown is a stream whose record could not be read. It is
	// stated rather than guessed at: a run whose state file is gone is not a run
	// in some particular state.
	StreamStatusUnknown = "unknown"
)

// Stream is one recorded event stream, named and placed well enough to follow,
// list, or price without opening it again to work out which kind it is.
type Stream struct {
	ID     string     `json:"id"`
	Kind   StreamKind `json:"kind"`
	Status string     `json:"status"`
	// StartedAt is when the work behind the stream opened. A run records the
	// moment in its state file; a conversation's and a branch review's is the
	// timestamp of their first event, which is the same moment and needs no
	// state file to find. It is zero on a stream whose moment could not be read.
	StartedAt time.Time `json:"started_at,omitzero"`
	Events    int       `json:"events"`
	// Path is the event log itself, which is what following one opens.
	Path string `json:"path"`
	// Updated is when the log was last appended to. It is what "newest" means
	// here — the stream something happened in most recently, which is the one an
	// operator who named nothing meant.
	Updated time.Time `json:"updated"`
	// Conversation is the main conversation a side stream was opened beside, and
	// is empty on every other kind and on a side stream whose record could not
	// be read.
	Conversation string `json:"conversation,omitempty"`
}

// Dated reports a stream whose opening moment could be read. One that could not
// still cost money and is still followable, so it is carried rather than
// dropped.
func (s Stream) Dated() bool { return !s.StartedAt.IsZero() }

// StreamStore reads the event streams of every kind under one product, and the
// exchanges beside them. It composes the stores that own them rather than
// reaching into their directories, so a layout only one of them knows about
// stays that store's.
type StreamStore struct {
	runs          *Store
	conversations *ConversationStore
	reviews       *BranchReviewStore
	sides         *SideStreamStore
	exchanges     *ExchangeStore
}

func NewStreamStore(root string, productID domain.ProductID) (*StreamStore, error) {
	runs, err := NewStore(root, productID)
	if err != nil {
		return nil, err
	}
	conversations, err := NewConversationStore(root, productID)
	if err != nil {
		return nil, err
	}
	reviews, err := NewBranchReviewStore(root, productID)
	if err != nil {
		return nil, err
	}
	sides, err := NewSideStreamStore(root, productID)
	if err != nil {
		return nil, err
	}
	exchanges, err := NewExchangeStore(root, productID)
	if err != nil {
		return nil, err
	}
	return &StreamStore{runs: runs, conversations: conversations, reviews: reviews, sides: sides, exchanges: exchanges}, nil
}

// Root is the product's own directory, which is where every kind of stream
// lives. An empty answer names it, so "nothing is recorded" can be told apart
// from "nothing is recorded here": the state root is resolved from the
// environment, and an operator whose environment points somewhere they did not
// expect is reading a true answer to the wrong question.
func (s *StreamStore) Root() string { return filepath.Dir(s.runs.Root()) }

// StreamQuery selects which recorded streams an answer is about.
type StreamQuery struct {
	// Kinds narrows the answer to some of the kinds. Empty covers all of them,
	// and a kind that records no stream contributes nothing to a listing.
	Kinds []StreamKind
	// Match keeps only the streams whose id contains it, which is what makes a
	// unique id prefix enough to name one.
	Match string
	// Limit bounds how many are returned, newest first. Zero returns all of them.
	Limit int
}

func (q StreamQuery) kinds() []StreamKind {
	if len(q.Kinds) == 0 {
		return EveryStreamKind
	}
	var followable []StreamKind
	for _, kind := range q.Kinds {
		if kind.Followable() {
			followable = append(followable, kind)
		}
	}
	return followable
}

// List reports the selected streams, newest first. A product that has only ever
// chatted has no runs directory, one that has only ever run has no
// conversations directory, one whose branches have never been reviewed has no
// branch-reviews directory, and one whose agents have never held a side thread
// has no sidestreams directory; any one of those on its own is a product with
// streams rather than an error, so an absent directory contributes nothing.
//
// It reads the directory, not the logs: every entry is stat'd, the newest are
// chosen, and only those are described. A listing that opened every event log
// to print twenty rows would read the whole state directory — over a hundred
// runs, logs that reach megabytes — and `--follow --latest` asks for the newest
// stream every few seconds, which is what the script's `ls -t | head` avoided
// and this has to avoid too.
func (s *StreamStore) List(query StreamQuery) ([]Stream, error) {
	found, err := s.choose(query)
	if err != nil {
		return nil, err
	}
	var current map[string]ConversationIdentity
	streams := make([]Stream, 0, len(found))
	for _, entry := range found {
		if current, err = s.currentFor(entry, current); err != nil {
			return nil, err
		}
		stream, _, err := s.describe(entry, current, false)
		if err != nil {
			return nil, err
		}
		streams = append(streams, stream)
	}
	return streams, nil
}

// choose is the directory half of a listing: every selected entry stat'd,
// sorted newest first, and cut to the limit, with no log opened.
func (s *StreamStore) choose(query StreamQuery) ([]streamEntry, error) {
	var found []streamEntry
	for _, kind := range query.kinds() {
		entries, err := s.entries(kind, query.Match)
		if err != nil {
			return nil, err
		}
		found = append(found, entries...)
	}
	sort.Slice(found, func(i, j int) bool {
		if !found[i].updated.Equal(found[j].updated) {
			return found[i].updated.After(found[j].updated)
		}
		return found[i].id < found[j].id
	})
	if query.Limit > 0 && len(found) > query.Limit {
		found = found[:query.Limit]
	}
	return found, nil
}

// currentFor reads which conversations are still the roles' the first time a
// conversation is about to be described, and not before: it is one fact about
// the whole directory rather than one per stream, and a listing that chose no
// conversation never needs it.
func (s *StreamStore) currentFor(entry streamEntry, current map[string]ConversationIdentity) (map[string]ConversationIdentity, error) {
	if entry.kind != StreamConversation || current != nil {
		return current, nil
	}
	return s.currentConversations()
}

// Find is the one stream a query names, newest first so an ambiguous prefix
// lands on the one most likely meant. A query matching nothing is reported as
// nothing found rather than as a failure: which of the two it is belongs to the
// caller, who knows whether the operator named something.
func (s *StreamStore) Find(query StreamQuery) (Stream, bool, error) {
	query.Limit = 1
	streams, err := s.List(query)
	if err != nil || len(streams) == 0 {
		return Stream{}, false, err
	}
	return streams[0], true, nil
}

func (s *StreamStore) root(kind StreamKind) string {
	switch kind {
	case StreamConversation:
		return s.conversations.Root()
	case StreamReview:
		return s.reviews.Root()
	case StreamSide:
		return s.sides.Root()
	default:
		return s.runs.Root()
	}
}

// streamEntry is what a directory listing says about one stream before its log
// is opened: enough to choose the newest, and nothing that costs a read.
type streamEntry struct {
	kind    StreamKind
	id      string
	path    string
	updated time.Time
}

func (s *StreamStore) entries(kind StreamKind, match string) ([]streamEntry, error) {
	root := s.root(kind)
	listed, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s records: %w", kind, err)
	}
	var found []streamEntry
	for _, entry := range listed {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, eventLogSuffix) {
			continue
		}
		id := strings.TrimSuffix(name, eventLogSuffix)
		if match != "" && !strings.Contains(id, match) {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			// Removed between the listing and the stat: a race with cleanup rather
			// than a failure of the answer.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect event log: %w", err)
		}
		found = append(found, streamEntry{kind: kind, id: id, path: filepath.Join(root, name), updated: info.ModTime()})
	}
	return found, nil
}

// describe reads what a listing says about one stream. The log is read once for
// everything that comes out of it — how many events it holds, when the first one
// was, and for a review, whether its verdict has been made — because a listing
// asks all of that of every row it prints.
func (s *StreamStore) describe(entry streamEntry, current map[string]ConversationIdentity, priced bool) (Stream, streamScan, error) {
	stream := Stream{ID: entry.id, Kind: entry.kind, Path: entry.path, Status: StreamStatusUnknown, Updated: entry.updated}
	scanned, err := scanStreamLog(entry.path, entry.kind, priced)
	if err != nil {
		return Stream{}, streamScan{}, fmt.Errorf("%s %s: %w", entry.kind, entry.id, err)
	}
	stream.Events = scanned.events
	stream.StartedAt = scanned.first
	switch entry.kind {
	case StreamRun:
		// A run keeps its own status and its own opening moment, and both are
		// authoritative over anything the log could be read to imply.
		state, err := s.runs.Read(entry.id)
		if err == nil {
			stream.Status = string(state.Status)
			stream.StartedAt = state.StartedAt
		}
	case StreamConversation:
		stream.Status = s.conversationStatus(entry.id, current)
	case StreamReview:
		stream.Status = ReviewInProgress
		if scanned.reviewed {
			stream.Status = ReviewFinished
		}
	case StreamSide:
		s.describeSide(&stream, &scanned)
	}
	return stream, scanned, nil
}

// describeSide reads what a side stream's own record says about it: whether it
// is still held, when it opened, and the conversation it was opened beside,
// which is what its spend is attributed to. The record is authoritative over the
// log for all three, as a run's state file is. A terminal that named no role is
// the role the record says holds the thread, because nothing else writes into a
// side stream's log — the same reason a branch review's terminals are the
// reviewer's.
//
// A record that cannot be read leaves the stream unknown and unattributed rather
// than dropped: the log still says what it cost, and that money was spent.
func (s *StreamStore) describeSide(stream *Stream, scanned *streamScan) {
	recorded, err := s.sides.Load(stream.ID)
	if err != nil {
		return
	}
	stream.Status = SideStreamOpen
	if !recorded.Open() {
		stream.Status = string(recorded.Outcome)
	}
	stream.StartedAt = recorded.OpenedAt
	stream.Conversation = recorded.Conversation
	for index := range scanned.invocations {
		if scanned.invocations[index].Role == "" {
			scanned.invocations[index].Role = recorded.Role
		}
	}
}

// conversationStatus says what a conversation is doing. A conversation has no
// state file of its own: the role's record names the conversation that role is
// in now, so every other log in the directory belonged to one that has since
// been replaced and nothing will happen in it again. Whether one that is still
// the role's is being answered is asked of the observed hold — the same
// question the four lines' Working line asks, of the same store — rather than
// read off the event log, because the log cannot tell a turn in flight from a
// turn whose process was killed before it wrote a terminal, and two halves of
// one verb must not disagree about whether an agent is working. A hold that
// could not be asked about is stated as unknown rather than guessed at.
func (s *StreamStore) conversationStatus(id string, current map[string]ConversationIdentity) string {
	identity, held := current[id]
	if !held {
		return ConversationEnded
	}
	inFlight, err := s.conversations.InFlight(identity)
	if err != nil {
		return StreamStatusUnknown
	}
	if inFlight {
		return ConversationAnswering
	}
	return ConversationWaiting
}

// currentConversations is which conversations the roles are in now, and under
// which identity each is held, read as the few fields that answer it rather
// than through the whole record. Loading the records would refuse the question
// over a conversation that fails to validate — a record written by a newer
// harness, or one a half-finished write left behind — and a listing is the
// surface an operator reaches for when something is already wrong, so it must
// not be the thing that goes silent first. A record names its conversation and
// its role whether or not the rest of it is loadable, and it is filed under the
// agent's own name, which is what the hold is keyed by.
func (s *StreamStore) currentConversations() (map[string]ConversationIdentity, error) {
	entries, err := os.ReadDir(s.conversations.Root())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]ConversationIdentity{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read conversation records: %w", err)
	}
	current := make(map[string]ConversationIdentity, len(entries))
	for _, entry := range entries {
		// The leases, the event logs, and the temporary files of a save in flight
		// all live in this directory; only a file named for an agent holds a
		// conversation.
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		recorded, err := os.ReadFile(filepath.Join(s.conversations.Root(), name))
		if err != nil {
			return nil, fmt.Errorf("read conversation record %s: %w", name, err)
		}
		var read struct {
			ConversationID string           `json:"conversation_id"`
			Agent          string           `json:"agent"`
			Role           domain.AgentRole `json:"role"`
		}
		if err := json.Unmarshal(recorded, &read); err != nil || read.ConversationID == "" {
			continue
		}
		if read.Agent == "" {
			read.Agent = strings.TrimSuffix(name, ".json")
		}
		current[read.ConversationID] = ConversationIdentity{Agent: read.Agent, Role: read.Role}
	}
	return current, nil
}

// streamScan is what one pass over an event log yields: what a listing says of
// the stream, and — when the pass was asked to price it — every provider call it
// recorded. One pass serves both so that a spend report, which has to read every
// log it covers, reads each of them once.
type streamScan struct {
	events   int
	first    time.Time
	reviewed bool
	// invocations is every priced terminal the log holds, collected only when
	// the scan was asked to price the stream: a listing never needs them, and
	// decoding every terminal's usage to print twenty rows would be a read nobody
	// asked for.
	invocations []Invocation
}

// scanStreamLog reads an event log once. A log that is gone is empty rather than
// unreadable: a stream removed between being listed and being read is a race
// with cleanup, not a failure of the answer.
func scanStreamLog(path string, kind StreamKind, priced bool) (streamScan, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return streamScan{}, nil
	}
	if err != nil {
		return streamScan{}, fmt.Errorf("open event log: %w", err)
	}
	defer file.Close()

	var scanned streamScan
	// costs turns each terminal's reported figure into what that invocation cost,
	// by the rule the ledger prices a run by. It earns most of its keep on a
	// conversation, which is one session resumed turn after turn: a log of three
	// hundred turns carries the running total three hundred times, and summing
	// those is what had this product's management conversations reading thirty
	// times what they cost.
	var costs SessionCosts
	// reviewing is the review bracket the ledger keeps for a run log recorded
	// before a terminal named its own role, kept here for the same logs and the
	// same reason. It decides nothing for a terminal that names itself.
	reviewing := false
	// session is the one the last terminal named, which a duplicate terminal
	// recorded before it named its own is priced against.
	session := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedEventBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		scanned.events++
		var read struct {
			Type      execution.EventType `json:"type"`
			Timestamp time.Time           `json:"timestamp"`
		}
		// A line that will not decode still happened, so it is counted; what it
		// cannot do is say anything about when or what.
		if err := json.Unmarshal(line, &read); err != nil {
			continue
		}
		if scanned.first.IsZero() {
			scanned.first = read.Timestamp
		}
		if read.Type == execution.EventReviewCompleted {
			scanned.reviewed = true
		}
		// The cheap test over-matches and the decoded type rejects the rest, which
		// is what keeps pricing a stream from decoding all of its own chatter.
		if !priced || !carriesSpendEvidence(line) {
			continue
		}
		var terminal pricedEvent
		if err := json.Unmarshal(line, &terminal); err != nil {
			return streamScan{}, fmt.Errorf("decode event log to price it: %w", err)
		}
		if terminal.Type == execution.EventReviewStarted {
			reviewing = true
			continue
		}
		// A second terminal to one invocation is more of that invocation's cost,
		// by the ledger's rule and for the ledger's reason.
		if terminal.duplicateTerminal() {
			cost := costs.Own(terminal.session(session), terminal.Payload.TotalCostUSD)
			if last := len(scanned.invocations) - 1; last >= 0 {
				scanned.invocations[last].CostUSD += cost
			}
			continue
		}
		if !terminal.priced() {
			continue
		}
		announced := reviewing
		reviewing = false
		session = terminal.Payload.SessionID
		scanned.invocations = append(scanned.invocations, Invocation{
			At:      terminal.Timestamp,
			Role:    invocationRole(kind, terminal, announced),
			CostUSD: costs.Own(session, terminal.Payload.TotalCostUSD),
			Usage:   terminal.tokens(),
		})
	}
	if err := scanner.Err(); err != nil {
		return streamScan{}, fmt.Errorf("read event log: %w", err)
	}
	return scanned, nil
}

// invocationRole is whose invocation a terminal ended, for a log of any kind.
// A run log is read by the ledger's rule, bracket and all. A branch review's
// terminals are the reviewer's whether or not they said so, because nothing
// else ever writes into one; a conversation's terminal that did not name its
// role is nobody's, because the log does not say which role's conversation it
// is and a guess here would be a guess about money.
func invocationRole(kind StreamKind, terminal pricedEvent, announced bool) domain.AgentRole {
	switch kind {
	case StreamRun:
		return terminal.role(announced)
	case StreamReview:
		return domain.RoleReviewer
	default:
		return domain.AgentRole(strings.TrimSpace(terminal.Payload.Role))
	}
}

// Invocation is one provider call a stream recorded: when it ended, whose it
// was, what the provider said it cost, and what it consumed. Cost is what the
// provider reported rather than an estimate, and a call that failed is one of
// these too — it was made and it was paid for, and leaving it out would
// understate every total it belonged in. The usage is the same TokenUsage the
// ledger sums, so an invocation whose terminal carried no usage object is
// counted as unmeasured there rather than as having read nothing.
type Invocation struct {
	At time.Time `json:"at,omitzero"`
	// Role is the role whose invocation this was, and empty where the terminal
	// named none.
	Role domain.AgentRole `json:"role,omitempty"`
	// CostUSD is this invocation's own cost. Where the terminal reported the
	// session's running total, as one resuming a session does, it is what that
	// total moved by rather than the total itself -- see OwnCostUSD.
	CostUSD float64    `json:"cost_usd"`
	Usage   TokenUsage `json:"usage"`
}

// UndatedDay is where spend whose moment could not be read is grouped. It still
// cost money, so it is reported rather than dropped: it sorts ahead of every
// dated day and no window excludes it, because it has no day to be outside of.
const UndatedDay = "undated"

// SpendQuery selects what a spend report covers.
type SpendQuery struct {
	// Kinds narrows the report. Empty covers every stream and the exchanges
	// beside them; naming any kind covers only what was named, so somebody who
	// asked what the runs cost is answered about the runs.
	Kinds []StreamKind
	// Match prices the one thing it names, whatever day it ran on: the window is
	// for a report that has to choose what to show, and naming something has
	// already chosen.
	Match string
	// Days is how many local days the report covers, today counting as the first
	// of them. Zero covers every day there is evidence for.
	Days int
	// Since, where it is set, has the report reckon a second time from one
	// moment rather than from a day boundary: every invocation and every
	// exchange round at or after it is counted into SpendReport.Rolling as well
	// as into its own day's row. It is how a surface that wants both — the
	// dashboard's spend box, which shows the last 24 hours beside the last
	// thirty local days — pays for one read of the logs rather than two, since
	// pricing a stream reads the whole of its log whatever window is asked for.
	Since time.Time
	// Now is the moment the window is measured back from, so a report is a
	// function of its inputs rather than of when it happened to be asked for.
	Now time.Time
}

func (q SpendQuery) kinds() []StreamKind {
	if len(q.Kinds) == 0 {
		return EveryPricedKind
	}
	return q.Kinds
}

func (q SpendQuery) covers(kind StreamKind) bool {
	for _, candidate := range q.kinds() {
		if candidate == kind {
			return true
		}
	}
	return false
}

// SpendRow is one stream's spend on one local day. A stream contributes one row
// per day it spent on rather than one row for the day it opened: a conversation
// stays open for as long as the role is in it, so one opened a fortnight ago and
// answered again this morning spends on both days, and a single row for its
// opening day would leave this morning out of what today cost.
type SpendRow struct {
	Day      string     `json:"day"`
	StreamID string     `json:"id"`
	Kind     StreamKind `json:"kind"`
	Status   string     `json:"status"`
	// Conversation is the main conversation a side stream's row was spent beside,
	// carried from the stream so the row says whose thread the money went on.
	Conversation string `json:"conversation,omitempty"`
	// At is the moment the row is shown and ordered at. On the day the work
	// opened it is when it opened, which is what that column has always said; on
	// a later day it is the first invocation of that day, because there is
	// nothing else the column could mean there.
	At    time.Time `json:"at,omitzero"`
	Calls int       `json:"calls"`
	// Usage is absent on an exchange's row rather than zero: its record keeps
	// what the provider charged and not what it used, and a zero here would be
	// added into a day's token total as though it had used none.
	Usage   *TokenUsage `json:"usage,omitempty"`
	CostUSD float64     `json:"cost_usd"`
	// Roles is the same spend split by whose invocations it was, with each
	// role's cost apportioned across what its invocations were billed for. A
	// run's row mixes the developer's session with the reviewer's one-shot
	// invocations, and those two are not cached alike: the split is what says
	// whether a role is reading the cache it writes, which the row's one share
	// cannot. An exchange's row carries none, for the reason it carries no usage.
	Roles []RoleSpend `json:"roles,omitempty"`
}

// RoleSpend is one role's part of a row or of a report: how many invocations,
// what they cost, what they used, and that cost apportioned by what it was
// billed for. The role is empty where the terminals named none.
type RoleSpend struct {
	Role    domain.AgentRole `json:"role,omitempty"`
	Calls   int              `json:"calls"`
	CostUSD float64          `json:"cost_usd"`
	Usage   TokenUsage       `json:"usage"`
	Split   CostSplit        `json:"split"`
}

func (r *RoleSpend) add(invocation Invocation) {
	r.Calls++
	r.CostUSD += invocation.CostUSD
	r.Usage.Merge(invocation.Usage)
	r.Split.Merge(invocation.Usage.Split(invocation.CostUSD))
}

func (r *RoleSpend) merge(other RoleSpend) {
	r.Calls += other.Calls
	r.CostUSD += other.CostUSD
	r.Usage.Merge(other.Usage)
	r.Split.Merge(other.Split)
}

// SpendReport is what the selected streams spent, one row per stream per day,
// ordered so that each day's rows are contiguous and the most recent day is
// last — which is where the eye already is when the question is what today cost.
type SpendReport struct {
	Rows []SpendRow `json:"rows"`
	// Oldest is the first local day the window covers, and is empty on a report
	// with no window. It is what lets an empty report say which of the two
	// empties it is: nothing spent at all, or nothing spent in the days asked
	// about, which a wider window would answer differently.
	Oldest string `json:"oldest_day,omitempty"`
	Days   int    `json:"days,omitempty"`
	// Streams is how many event streams were read to produce the rows, and
	// Exchanges how many exchange records were.
	Streams   int `json:"streams"`
	Exchanges int `json:"exchanges"`
	// UnreadableExchanges names the exchange records that could not be read.
	// They are counted and named rather than dropped, because a record nobody can
	// parse did not cost nothing: the exchanges beside them are still priced, and
	// every total they are missing from is a floor. `yoyo cost` answers the same
	// way for the same records, because two surfaces differing about what an
	// unknown figure is leaves the operator to settle it.
	UnreadableExchanges []string `json:"unreadable_exchanges,omitempty"`
	// UnreadableReason is why the first of them could not be read, which says
	// what kind of broken this is without enumerating every instance.
	UnreadableReason string `json:"unreadable_reason,omitempty"`
	// Rolling is the same spend reckoned from SpendQuery.Since rather than from
	// a day boundary — the window whole local days cannot express — and is nil
	// where the query named no moment. RollingWindow() is what adds it up, by
	// the one summation every total here is made by.
	Rolling []SpendRow `json:"rolling,omitempty"`
	// RollingSince is the moment Rolling is reckoned from.
	RollingSince time.Time `json:"rolling_since,omitzero"`
	// Reaches is the earliest local day any priced row of this pass falls on,
	// whatever window was asked for. It is what tells a day nothing was spent on
	// from a day no priced record goes back to: the first is nothing spent, and
	// the second is a day this store can say nothing about, and a surface that
	// showed the second as zero would be reporting a figure nobody measured. It
	// is empty where nothing dated was priced.
	Reaches string `json:"reaches,omitempty"`
}

// Empty reports a query that selected nothing at all — no stream and no
// exchange record, readable or not — which is a different answer from a window
// nothing was spent in.
func (r SpendReport) Empty() bool {
	return r.Streams == 0 && r.Exchanges == 0 && len(r.UnreadableExchanges) == 0
}

// Floor reports a total that is a lower bound, because something it should
// cover could not be read.
func (r SpendReport) Floor() bool { return len(r.UnreadableExchanges) > 0 }

// Since is the same report narrowed to the rows from one local day on, by the
// rule the report's own window applies: a row is inside from that day on, and
// an undated row is inside every window because it has no day to be outside
// of. The unreadable exchanges are carried whole, because a record nobody could
// read is missing from every window at once. It says its first day and no
// count of days, since the count was the query's and this is a later start.
func (r SpendReport) Since(day string) SpendReport {
	narrowed := r
	narrowed.Rows = nil
	narrowed.Oldest = day
	narrowed.Days = 0
	narrowed.take(r.Rows)
	return narrowed
}

// RollingWindow is the rolling rows as a report of their own, so that the one
// summation below adds them up as it adds up any other window: the same
// streams and the same unreadable exchanges, reckoned from the moment the
// query named rather than from a day boundary. A report whose query named no
// moment comes back with no rows at all, which sums to nothing spent.
func (r SpendReport) RollingWindow() SpendReport {
	narrowed := r
	narrowed.Rows = r.Rolling
	narrowed.Rolling = nil
	narrowed.Oldest = ""
	narrowed.Days = 0
	return narrowed
}

// SpendTotals is what a report's rows add up to: in all, and by kind in the
// order the kinds are priced. It is the one summation every surface that
// prints a total reads — `yoyo status --spend` and the dashboard's spend box
// alike — so two of them cannot add the same rows to different figures.
type SpendTotals struct {
	Calls   int
	CostUSD float64
	// Usage is the token usage of every row that recorded one. An exchange's
	// row records none, and adds nothing here rather than zero.
	Usage TokenUsage
	// ByKind carries only the kinds that have a row, in the order
	// EveryPricedKind prices them.
	ByKind []KindTotal
	// ByRole carries only the roles whose invocations the rows recorded, in the
	// order roleOrder ranks them, each with its cost apportioned across what its
	// invocations were billed for. It is the figure that says whether a role is
	// paying to write a cache it reads back or one it does not, which the total's
	// one cache-read share hides behind whichever role reads the most.
	ByRole []RoleSpend
}

// KindTotal is one kind's share of a report's total.
type KindTotal struct {
	Usage   TokenUsage
	Kind    StreamKind
	Calls   int
	CostUSD float64
}

// roleOrder is the order roles are reported in: the two a run is made of
// first, then the conversation roles, then anything else by name, and the
// invocations that named no role last. The order is fixed so that two reports
// over different windows put the same role on the same line.
var roleOrder = []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer, domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect}

// Totals sums the rows the report holds.
func (r SpendReport) Totals() SpendTotals {
	var totals SpendTotals
	byKind := make(map[StreamKind]*KindTotal, len(EveryPricedKind))
	byRole := make(map[domain.AgentRole]*RoleSpend)
	var unpriced []StreamKind
	for _, row := range r.Rows {
		totals.Calls += row.Calls
		totals.CostUSD += row.CostUSD
		if row.Usage == nil {
			totals.Usage.Priced += row.Calls
		}
		if row.Usage != nil {
			totals.Usage.Merge(*row.Usage)
		}
		share, seen := byKind[row.Kind]
		if !seen {
			share = &KindTotal{Kind: row.Kind}
			byKind[row.Kind] = share
			if !isPricedKind(row.Kind) {
				unpriced = append(unpriced, row.Kind)
			}
		}
		share.Calls += row.Calls
		share.CostUSD += row.CostUSD
		if row.Usage == nil {
			share.Usage.Priced += row.Calls
		}
		if row.Usage != nil {
			share.Usage.Merge(*row.Usage)
		}
		for _, spent := range row.Roles {
			part, seen := byRole[spent.Role]
			if !seen {
				part = &RoleSpend{Role: spent.Role}
				byRole[spent.Role] = part
			}
			part.merge(spent)
		}
	}
	// A kind the pricing does not name still spent, so it is not dropped: it
	// follows the priced kinds, in the order its rows came.
	for _, kind := range append(append([]StreamKind{}, EveryPricedKind...), unpriced...) {
		if share, seen := byKind[kind]; seen {
			totals.ByKind = append(totals.ByKind, *share)
		}
	}
	totals.ByRole = orderedRoles(byRole)
	return totals
}

// orderedRoles lays the roles out in roleOrder, then any role the order does
// not name alphabetically, then the unnamed role last.
func orderedRoles(byRole map[domain.AgentRole]*RoleSpend) []RoleSpend {
	ordered := make([]RoleSpend, 0, len(byRole))
	placed := make(map[domain.AgentRole]bool, len(byRole))
	for _, role := range roleOrder {
		if part, seen := byRole[role]; seen {
			ordered = append(ordered, *part)
			placed[role] = true
		}
	}
	var others []domain.AgentRole
	for role := range byRole {
		if !placed[role] && role != "" {
			others = append(others, role)
		}
	}
	sort.Slice(others, func(i, j int) bool { return others[i] < others[j] })
	for _, role := range others {
		ordered = append(ordered, *byRole[role])
	}
	if part, seen := byRole[""]; seen {
		ordered = append(ordered, *part)
	}
	return ordered
}

func isPricedKind(kind StreamKind) bool {
	for _, priced := range EveryPricedKind {
		if priced == kind {
			return true
		}
	}
	return false
}

// Spend prices the selected streams by the local-timezone day the money was
// spent on, because what an operator budgets against is what today cost and the
// day they mean is the one their own clock is keeping.
//
// Every selected stream is read whatever day it opened on, because the window is
// about when the money was spent and a stream that opened before it can still
// have spent inside it. Only the rows a stream yields are held against the
// window.
func (s *StreamStore) Spend(query SpendQuery) (SpendReport, error) {
	found, err := s.choose(StreamQuery{Kinds: query.kinds(), Match: query.Match})
	if err != nil {
		return SpendReport{}, err
	}
	report := SpendReport{Streams: len(found), RollingSince: query.Since}
	if query.Days > 0 {
		report.Days = query.Days
		report.Oldest = oldestLocalDay(query.Now, query.Days)
	}
	var current map[string]ConversationIdentity
	for _, entry := range found {
		if current, err = s.currentFor(entry, current); err != nil {
			return SpendReport{}, err
		}
		// One pass over each log: what the row says about the stream and every
		// terminal it holds come out of the same read.
		stream, scanned, err := s.describe(entry, current, true)
		if err != nil {
			return SpendReport{}, err
		}
		rows := spendByDay(stream, scanned.invocations)
		report.reaching(rows)
		report.take(rows)
		report.roll(query.Since, func() []SpendRow {
			return spendByDay(stream, invocationsFrom(scanned.invocations, query.Since))
		})
	}
	if query.covers(StreamExchange) {
		if err := s.spendOnExchanges(query.Match, query.Since, &report); err != nil {
			return SpendReport{}, err
		}
	}
	byMoment := func(rows []SpendRow) func(int, int) bool {
		return func(i, j int) bool {
			if !rows[i].At.Equal(rows[j].At) {
				return rows[i].At.Before(rows[j].At)
			}
			return rows[i].StreamID < rows[j].StreamID
		}
	}
	sort.Slice(report.Rows, byMoment(report.Rows))
	sort.Slice(report.Rolling, byMoment(report.Rolling))
	return report, nil
}

// roll keeps the rows a rolling window holds, where the query named a moment to
// reckon one from. The rows are built only then, so a report nobody asked a
// rolling window of pays nothing for the one it does not get.
func (r *SpendReport) roll(since time.Time, rows func() []SpendRow) {
	if since.IsZero() {
		return
	}
	r.Rolling = append(r.Rolling, rows()...)
}

// reaching records how far back the evidence goes, from every row the pass
// produced rather than from the rows one window kept. A row with no day says
// nothing about reach, having none.
func (r *SpendReport) reaching(rows []SpendRow) {
	for _, row := range rows {
		if row.Day == UndatedDay {
			continue
		}
		if r.Reaches == "" || row.Day < r.Reaches {
			r.Reaches = row.Day
		}
	}
}

// invocationsFrom is the invocations a rolling window holds: the ones made at
// or after the moment it is reckoned from, and the ones whose moment could not
// be read, which have no moment to be outside a window by — the rule the day
// windows already apply to an undated row.
func invocationsFrom(invocations []Invocation, since time.Time) []Invocation {
	inside := make([]Invocation, 0, len(invocations))
	for _, invocation := range invocations {
		if invocation.At.IsZero() || !invocation.At.Before(since) {
			inside = append(inside, invocation)
		}
	}
	return inside
}

// take keeps the rows inside the window. An undated row has no day to be
// outside of, so no window excludes it.
func (r *SpendReport) take(rows []SpendRow) {
	for _, row := range rows {
		if r.Oldest != "" && row.Day != UndatedDay && row.Day < r.Oldest {
			continue
		}
		r.Rows = append(r.Rows, row)
	}
}

// spendOnExchanges prices the exchanges the way `yoyo cost` does: one record at
// a time, so that one which cannot be read costs the report that one exchange
// rather than the whole report, and is counted on the way past rather than
// dropped. A round counts on the day it was answered exactly as an invocation
// counts on the day it was made, so a thread that ran over two days contributes
// a row to each of them as a conversation does.
func (s *StreamStore) spendOnExchanges(match string, since time.Time, report *SpendReport) error {
	ids, err := s.exchanges.Records()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if match != "" && !strings.Contains(id, match) {
			continue
		}
		recorded, err := s.exchanges.Read(id)
		if err != nil {
			report.UnreadableExchanges = append(report.UnreadableExchanges, id)
			if report.UnreadableReason == "" {
				report.UnreadableReason = err.Error()
			}
			continue
		}
		report.Exchanges++
		status := ExchangeOpen
		if !recorded.Open() {
			status = string(recorded.Outcome)
		}
		grouped := roundsByDay(id, status, recorded.Answerer.Role, recorded.Rounds, time.Time{})
		report.reaching(grouped)
		report.take(grouped)
		report.roll(since, func() []SpendRow { return roundsByDay(id, status, recorded.Answerer.Role, recorded.Rounds, since) })
	}
	return nil
}

// roundsByDay groups an exchange's rounds into one row per local day it spent
// on, keeping only the rounds at or after `since` where a moment is given — an
// undated round being inside every window, as an undated invocation is.
func roundsByDay(id, status string, role domain.AgentRole, rounds []exchange.Round, since time.Time) []SpendRow {
	var order []string
	rows := make(map[string]*SpendRow, len(rounds))
	for _, round := range rounds {
		moment := round.AskedAt
		if round.AnsweredAt != nil {
			moment = *round.AnsweredAt
		}
		if !since.IsZero() && !moment.IsZero() && moment.Before(since) {
			continue
		}
		day := UndatedDay
		if !moment.IsZero() {
			day = LocalDay(moment)
		}
		row, seen := rows[day]
		if !seen {
			row = &SpendRow{Day: day, StreamID: id, Kind: StreamExchange, Status: status, At: moment}
			rows[day] = row
			order = append(order, day)
		}
		row.Calls++
		row.CostUSD += round.CostUSD
		usage := exchangeTokens(round)
		if row.Usage == nil && (round.CostReported != nil || len(round.Usage) > 0) {
			row.Usage = &TokenUsage{Priced: row.Calls - 1}
		}
		if row.Usage != nil {
			row.Usage.Merge(usage)
		}
		if len(row.Roles) == 0 {
			row.Roles = []RoleSpend{{Role: role}}
		}
		row.Roles[0].add(Invocation{CostUSD: round.CostUSD, Usage: usage})
	}
	grouped := make([]SpendRow, 0, len(order))
	for _, day := range order {
		grouped = append(grouped, *rows[day])
	}
	return grouped
}

func spendByDay(stream Stream, invocations []Invocation) []SpendRow {
	openedOn := ""
	if stream.Dated() {
		openedOn = LocalDay(stream.StartedAt)
	}
	var order []string
	rows := make(map[string]*SpendRow, len(invocations))
	for _, invocation := range invocations {
		day := UndatedDay
		if !invocation.At.IsZero() {
			day = LocalDay(invocation.At)
		}
		row, seen := rows[day]
		if !seen {
			row = &SpendRow{Day: day, StreamID: stream.ID, Kind: stream.Kind, Status: stream.Status, Conversation: stream.Conversation, At: invocation.At, Usage: &TokenUsage{}}
			if day == openedOn {
				row.At = stream.StartedAt
			}
			rows[day] = row
			order = append(order, day)
		}
		row.Calls++
		row.CostUSD += invocation.CostUSD
		row.Usage.Merge(invocation.Usage)
		row.addToRole(invocation)
	}
	grouped := make([]SpendRow, 0, len(order))
	for _, day := range order {
		grouped = append(grouped, *rows[day])
	}
	return grouped
}

// addToRole puts one invocation on its role's part of the row, opening the
// part on the role's first invocation. The parts are kept in the order the
// roles first spent, which the report's totals reorder into roleOrder.
func (r *SpendRow) addToRole(invocation Invocation) {
	for index := range r.Roles {
		if r.Roles[index].Role == invocation.Role {
			r.Roles[index].add(invocation)
			return
		}
	}
	part := RoleSpend{Role: invocation.Role}
	part.add(invocation)
	r.Roles = append(r.Roles, part)
}

// LocalDay is the day a moment falls on in the timezone the operator's day
// happens in, which is the only timezone a spend report is legible in.
func LocalDay(moment time.Time) string {
	return moment.Local().Format("2006-01-02")
}

// oldestLocalDay is the first local day a report of that many days covers, today
// counting as the first of them, so 7 is today and the six before it. It steps
// back by calendar days rather than by multiples of twenty-four hours, so a
// daylight-saving shift cannot move the answer by a day.
func oldestLocalDay(now time.Time, days int) string {
	local := now.Local()
	return time.Date(local.Year(), local.Month(), local.Day()-(days-1), 12, 0, 0, 0, local.Location()).
		Format("2006-01-02")
}
