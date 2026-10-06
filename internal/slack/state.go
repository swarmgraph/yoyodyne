package slack

// What the sink remembers between runs: which thread each topic opened, how far
// it has read each durable stream, which directives were said to it in a thread
// rather than at a terminal, which threads it has already told a stranger it
// does not know them in, and which operators it has asked to decide about a
// stopped line and where.
//
// All five live at the state root beside the runs, conversations, and reports
// they are read from, per product, and each is owned by the sink alone. A
// restart therefore posts into the same threads, resumes from the same place,
// still knows who asked for what, does not greet the same stranger twice, and
// does not ask the same person the same question again, which is the whole of
// what makes an outage a delay rather than a gap.
//
// Known limit, stated rather than solved: the thread map is per machine. Two
// collaborators' harnesses against one shared repository would open two threads
// per work item, because neither can see the other's map. That is team-mode
// coordination and belongs to the work that takes it on; the setup document says
// so, and until then one sink per product per workspace is the supported shape.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	// ThreadMapSchemaVersion and CursorsSchemaVersion are versioned separately
	// from run state because they describe neither a run nor a conversation, and
	// a sink is free to be upgraded without every other record moving with it.
	ThreadMapSchemaVersion   = 1
	CursorsSchemaVersion     = 1
	SteerMapSchemaVersion    = 1
	RefusalMapSchemaVersion  = 1
	DecisionMapSchemaVersion = 1
	// maxRecordBytes bounds any of them. A thread map is one line per topic, a
	// cursor set one line per stream, a steer map one line per directive somebody
	// typed into a thread, and a decision map one line per operator asked about
	// one stopped line, so anything near this bound is a file something other
	// than the sink has written.
	maxRecordBytes = 4 << 20
	// maxRememberedAsks bounds the decision map, which would otherwise grow
	// forever: every stopped state is marked with the moment it began, so a line
	// that stops and restarts all day is a new entry per operator every time, and
	// nothing else ever removes one.
	//
	// It is a count rather than an age because what it has to guarantee is that
	// the file keeps loading. A few dozen is far more than the handful of states
	// anybody can still usefully answer about, and the one that is standing now is
	// kept whatever the count says.
	maxRememberedAsks = 64
	// maxRememberedRefusals bounds the one record a person outside this project
	// can cause a line to be written to. The rest of what the sink keeps grows
	// with the work; this grows with how many threads strangers have spoken in,
	// which is not the harness's to bound by asking them to stop. Forgetting the
	// oldest costs at most one more polite refusal in a thread nobody has touched
	// in a long time, which is the cheapest thing here to be wrong about.
	maxRememberedRefusals = 256
)

// Store is the sink's own durable state for one product.
type Store struct {
	// base is the state root everything below is inside, kept because a write
	// that has to be confined is a root and a path within it rather than one
	// absolute name.
	base string
	// within is where this store's state sits under that root, in slash form.
	within  string
	root    string
	product domain.ProductID
}

// NewStore roots the sink's state under the product, beside the runs it reads.
func NewStore(root string, productID domain.ProductID) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	base := filepath.Clean(root)
	within := path.Join(home.ProductDirectoryWithin(base, string(productID)), "slack")
	return &Store{
		base:    base,
		within:  filepath.ToSlash(within),
		root:    filepath.Join(base, within),
		product: productID,
	}, nil
}

func (s *Store) Root() string { return s.root }

// SinkLog is where a supervised sink says what it is doing: the root that write
// is confined to, and the path to the file inside it. The two are returned
// together because neither is enough on its own — the containment is decided
// against the filesystem below the root — and the layout is spelled once, here,
// where the rest of this store's own paths come from.
func (s *Store) SinkLog() (root, relative string) {
	return s.base, s.within + "/" + sinkLogFile
}

// ensureRoot creates this product's sink state directory. It is the store's own
// directory — the root everything it writes is confined inside — rather than a
// write within somebody else's, which is why it is here and why nothing below
// it creates directories by name.
func (s *Store) ensureRoot() error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create the slack state directory: %w", err)
	}
	return nil
}

// Product is the product this store's state belongs to, and so the product the
// sink built on it reports on. It is kept rather than re-derived from the path
// because it is what every speaker's name is qualified by: a product read back
// out of a directory name would be the same id spelled a second way, and two
// spellings of one thing are two things that can disagree.
func (s *Store) Product() domain.ProductID { return s.product }

// Lease makes this process the product's only sink, reporting whether it got
// it. Two sinks are not a redundancy: they hold separate thread maps, so they
// open separate threads and post everything twice.
func (s *Store) Lease() (*runstate.Lease, bool, error) {
	return runstate.TryLeasePath(filepath.Join(s.root, ".sink.lock"), "slack sink")
}

// PassLease is held by a sink for the length of one pass over the records, and
// by the product's supervisor while it restarts the sink into a deployed build.
// It is what lets the restart land between two passes rather than inside one: a
// sink stopped while a post is on its way can have the post land and its cursor
// never written, and the sink started in its place then posts it a second time.
// A sink that finds it held skips the pass rather than waiting on it, because
// the only other holder is stopping it.
func (s *Store) PassLease() (*runstate.Lease, bool, error) {
	return runstate.TryLeasePath(filepath.Join(s.root, ".pass.lock"), "slack sink pass")
}

// Running reports whether a sink is holding this product's lease, by trying to
// take it and letting it go again. Taking the lease is how the question is
// asked — the lock is advisory and the operating system drops it when its
// holder dies, so an answer of "nobody" is an answer about processes rather
// than about files somebody forgot to clean up.
//
// It is asked of this product's lease and of nothing wider, which is what makes
// the answer right on a machine running several harnesses: a sibling product's
// sink holds a different lease, so it neither answers for this product nor is
// disturbed by the asking.
func (s *Store) Running() (bool, error) {
	lease, held, err := s.Lease()
	if err != nil {
		return false, err
	}
	if !held {
		return true, nil
	}
	// Held for as long as it took to find out it was free. A sink starting a
	// moment later takes it as it always would.
	if err := lease.Release(); err != nil {
		return false, err
	}
	return false, nil
}

// Thread is one topic's thread. The channel is recorded with the timestamp
// because a thread timestamp only means anything inside the channel it was
// posted in: a project that changes channels has not moved its threads, it has
// left them behind, and the sink opens new ones rather than replying into a
// timestamp the new channel has never heard of.
type Thread struct {
	Channel  string    `json:"channel"`
	ThreadTS string    `json:"thread_ts"`
	OpenedAt time.Time `json:"opened_at"`
	// Status is the status this thread's opening message was last marked with. It
	// is remembered for one reason — so a pass that finds nothing has moved does
	// nothing — and it is deliberately not treated as evidence of which symbol is
	// on the message: a sink killed between the workspace taking a mark and this
	// being written would make it a lie, so what replaces a mark sweeps the whole
	// vocabulary rather than trusting this to name what is there.
	//
	// It is an added field rather than a new schema version because a map written
	// before there were status marks is a correct map of unmarked threads: absent
	// reads as nothing to remove, and the first status the item reaches marks it.
	// Refusing to load one would cost every existing product its threads over a
	// mark that had not been invented when they were opened.
	Status notify.Status `json:"status,omitempty"`
}

// ThreadMap is the topic-to-thread map as it is stored.
type ThreadMap struct {
	SchemaVersion int               `json:"schema_version"`
	Threads       map[string]Thread `json:"threads"`
}

// LoadThreads reads the thread map. A product that has never reported has no
// map rather than a broken one, so an absent file is an empty map.
func (s *Store) LoadThreads() (ThreadMap, error) {
	var stored ThreadMap
	found, err := readJSON(s.threadPath(), "slack thread map", &stored)
	if err != nil {
		return ThreadMap{}, err
	}
	if !found {
		return ThreadMap{SchemaVersion: ThreadMapSchemaVersion, Threads: map[string]Thread{}}, nil
	}
	if stored.SchemaVersion != ThreadMapSchemaVersion {
		return ThreadMap{}, fmt.Errorf("slack thread map schema version %d is not supported", stored.SchemaVersion)
	}
	if stored.Threads == nil {
		stored.Threads = map[string]Thread{}
	}
	return stored, nil
}

// SaveThreads replaces the thread map durably.
func (s *Store) SaveThreads(threads ThreadMap) error {
	threads.SchemaVersion = ThreadMapSchemaVersion
	if threads.Threads == nil {
		threads.Threads = map[string]Thread{}
	}
	return s.write(s.threadPath(), "slack thread map", threads)
}

// Lookup reports the thread a topic already opened in this channel. The topic is
// its key, which is what the notifier puts on a message and what an inbound
// reply will be correlated back through.
func (m ThreadMap) Lookup(channel, topic string) (Thread, bool) {
	thread, found := m.Threads[topic]
	if !found || thread.Channel != channel || strings.TrimSpace(thread.ThreadTS) == "" {
		return Thread{}, false
	}
	return thread, true
}

// Record remembers the thread a topic opened.
func (m *ThreadMap) Record(topic string, thread Thread) {
	if m.Threads == nil {
		m.Threads = map[string]Thread{}
	}
	m.Threads[topic] = thread
}

// Steer is one directive somebody said into a thread: who said it, and where
// they said it. Neither is anywhere else. The durable directive record is the
// product's and is the same record whichever way a directive arrived, so a
// Slack member id and a thread key have no business in it — they are facts
// about this workspace rather than about what the operator directed.
//
// What they are for is the other half of an acknowledgment: when something
// later becomes of that directive, the person who asked for it is told where
// they asked, by name, and the message they asked in stops saying the directive
// is still open.
type Steer struct {
	// Member is the Slack member id of the human whose reply recorded it, which
	// is what a message tags to reach them.
	Member string `json:"member"`
	// Topic is the key of the thread it was said in, so what becomes of it is
	// said in the same narrative rather than in a thread nobody was reading.
	Topic string `json:"topic"`
	// Message is the timestamp of the reply that asked for it, which is the one
	// message in the channel that carries where this directive stands. It wears
	// the thinking face from the moment it arrived until the directive is
	// settled, so the mark can only move if the message that asked is remembered
	// — the acknowledgment posted under it is a different message and says a
	// different thing.
	//
	// It is an added field rather than a new schema version, for the reason a
	// thread's status is: a map written before there were receipts is a correct
	// map of directives whose asks nothing can mark, and refusing to load one
	// would lose the origin of every directive still in flight.
	Message string `json:"message,omitempty"`
	// RecordedAt is when the directive was received, kept so a reader of this
	// file can tell a live steer from one left over from months ago.
	RecordedAt time.Time `json:"recorded_at"`
	// Said marks a directive whose outcome the inbound half has already put in
	// the thread, which is exactly the one it settled itself. Without it the
	// delivery pass would say the same settlement again on its next reading: the
	// two halves post from different goroutines and remember what they have said
	// in different places, and this is the connection's half of that memory.
	Said bool `json:"said,omitempty"`
}

// SteerMap is the directive-to-reply map as it is stored.
//
// It is written by the connection alone — the goroutine that reads replies — and
// read by the delivery pass, which is the same division the thread map is held
// to the other way round. Nothing prunes it: it grows by one line per directive
// somebody typed into a thread, which is a human typing rather than a machine
// recording, and losing the origin of a directive that is still unresolved would
// lose exactly the case this exists for.
type SteerMap struct {
	SchemaVersion int              `json:"schema_version"`
	Steers        map[string]Steer `json:"steers"`
}

// LoadSteers reads the steer map. A product nobody has steered from a thread has
// no map rather than a broken one, so an absent file is an empty map.
func (s *Store) LoadSteers() (SteerMap, error) {
	var stored SteerMap
	found, err := readJSON(s.steerPath(), "slack steer map", &stored)
	if err != nil {
		return SteerMap{}, err
	}
	if !found {
		return SteerMap{SchemaVersion: SteerMapSchemaVersion, Steers: map[string]Steer{}}, nil
	}
	if stored.SchemaVersion != SteerMapSchemaVersion {
		return SteerMap{}, fmt.Errorf("slack steer map schema version %d is not supported", stored.SchemaVersion)
	}
	if stored.Steers == nil {
		stored.Steers = map[string]Steer{}
	}
	return stored, nil
}

// SaveSteers replaces the steer map durably.
func (s *Store) SaveSteers(steers SteerMap) error {
	steers.SchemaVersion = SteerMapSchemaVersion
	if steers.Steers == nil {
		steers.Steers = map[string]Steer{}
	}
	return s.write(s.steerPath(), "slack steer map", steers)
}

// Lookup reports where one directive was said, and by whom. A directive nobody
// said in a thread is not in the map, which is every directive recorded at a
// terminal.
func (m SteerMap) Lookup(directiveID string) (Steer, bool) {
	steer, found := m.Steers[directiveID]
	if !found || strings.TrimSpace(steer.Topic) == "" {
		return Steer{}, false
	}
	return steer, true
}

// Record remembers where one directive was said.
func (m *SteerMap) Record(directiveID string, steer Steer) {
	if m.Steers == nil {
		m.Steers = map[string]Steer{}
	}
	m.Steers[directiveID] = steer
}

// Refusal is one thread this sink has already told somebody it does not know
// them in: who was told, and when.
//
// It is durable for the same reason a cursor is. The rule the refusal is held to
// is one per thread and not one per process, so a sink that restarted overnight
// must not greet the same stranger again in the morning — and a mark kept only
// in memory would do exactly that, every time anything restarted the process.
type Refusal struct {
	// Member is the Slack member id this was said to, kept so a reader of this
	// file can tell one stranger's thread from another's rather than only that
	// something was said in it.
	Member string `json:"member"`
	// At is when it was said, which is what says which entry to forget when the
	// map reaches its bound.
	At time.Time `json:"at"`
}

// RefusalMap is the threads a stranger has already been answered in, as it is
// stored.
//
// It is written by the connection alone, like the steer map, and read by nothing
// else at all: what it holds is the memory of one sentence having been said, and
// nothing downstream acts on it. It is bounded rather than unbounded, which is
// the one way it differs from every other record here — the lines in it are
// caused by people outside this project rather than by the work.
type RefusalMap struct {
	SchemaVersion int                `json:"schema_version"`
	Refused       map[string]Refusal `json:"refused"`
}

// LoadRefusals reads the refusal map. A product nobody outside it has spoken to
// has no map rather than a broken one, so an absent file is an empty map.
func (s *Store) LoadRefusals() (RefusalMap, error) {
	var stored RefusalMap
	found, err := readJSON(s.refusalPath(), "slack refusal map", &stored)
	if err != nil {
		return RefusalMap{}, err
	}
	if !found {
		return RefusalMap{SchemaVersion: RefusalMapSchemaVersion, Refused: map[string]Refusal{}}, nil
	}
	if stored.SchemaVersion != RefusalMapSchemaVersion {
		return RefusalMap{}, fmt.Errorf("slack refusal map schema version %d is not supported", stored.SchemaVersion)
	}
	if stored.Refused == nil {
		stored.Refused = map[string]Refusal{}
	}
	return stored, nil
}

// SaveRefusals replaces the refusal map durably.
func (s *Store) SaveRefusals(refusals RefusalMap) error {
	refusals.SchemaVersion = RefusalMapSchemaVersion
	if refusals.Refused == nil {
		refusals.Refused = map[string]Refusal{}
	}
	return s.write(s.refusalPath(), "slack refusal map", refusals)
}

// Has reports a thread this sink has already said it in. The channel is part of
// the key for the reason it is part of a thread: a timestamp means nothing
// outside the channel it was posted in, and a project that moved channels has
// left the conversation behind rather than taken it with it.
func (m RefusalMap) Has(channel, threadTS string) bool {
	_, found := m.Refused[refusalKey(channel, threadTS)]
	return found
}

// Record remembers having said it in one thread, forgetting the oldest once the
// map is at its bound.
func (m *RefusalMap) Record(channel, threadTS string, refusal Refusal) {
	if m.Refused == nil {
		m.Refused = map[string]Refusal{}
	}
	m.Refused[refusalKey(channel, threadTS)] = refusal
	for len(m.Refused) > maxRememberedRefusals {
		delete(m.Refused, m.oldest())
	}
}

// oldest is the entry to forget first: the one said longest ago, and the first
// of them by key where two were said at the same moment, so a map at its bound
// is pruned the same way twice rather than by whichever order a map was walked
// in.
func (m RefusalMap) oldest() string {
	var oldest string
	var at time.Time
	for key, refusal := range m.Refused {
		if oldest == "" || refusal.At.Before(at) || (refusal.At.Equal(at) && key < oldest) {
			oldest, at = key, refusal.At
		}
	}
	return oldest
}

// refusalKey names one thread in one channel, which is the scope the refusal is
// held to.
func refusalKey(channel, threadTS string) string {
	return channel + "/" + threadTS
}

// Decision is one ask this sink put to one operator in a direct message: which
// standing state it was about, where it was said, and what it offered.
//
// It is kept for two reasons, and the second is the one that matters. The first
// is so an operator is asked once about a state rather than every hour it
// stands. The second is that a reply arriving on the connection carries a
// channel and a thread and nothing else: this is the only thing that says that
// thread was an ask at all, which state it was about, and what the numbers in it
// meant — so without it a reply naming option 2 is a sentence nobody can read.
type Decision struct {
	// Mark is the standing state this asked about, in the same words the
	// heartbeat's cursor names it by. It is what makes one state one ask: the
	// state that replaces it is a different mark and is asked afresh, and the
	// same one standing for a week is asked once.
	Mark string `json:"mark"`
	// Member is the Slack member id this was asked of. Every operator is asked
	// separately, because a decision addressed to a room is one each of them can
	// reasonably assume somebody else is making.
	Member string `json:"member"`
	// Channel and ThreadTS are the direct message and the thread inside it, which
	// together are what an inbound reply is correlated back through.
	Channel  string `json:"channel"`
	ThreadTS string `json:"thread_ts"`
	// Stopped is what had stopped the line, kept in the words the ask used so the
	// directive a reply records says what it was answering rather than only which
	// number was typed.
	Stopped string `json:"stopped"`
	// Options are the answers that were offered, in the order they were numbered.
	// The numbering is this list's index, so a reply naming option 2 means the
	// second of these and not the second of whatever the harness would offer
	// today — an ask answered after an upgrade still means what it said.
	Options []string  `json:"options,omitempty"`
	AskedAt time.Time `json:"asked_at"`
}

// DecisionMap is every ask this sink has put to an operator, as it is stored.
//
// It is written by the delivery pass alone and read by the connection, which is
// the division the thread map is held to and the opposite of the steer map's. A
// reply is answered in the thread it arrived in and records a directive, and
// neither of those writes anything here: the ask is what the pass put out, and
// the connection reading it is reading what was said rather than revising it.
type DecisionMap struct {
	SchemaVersion int                 `json:"schema_version"`
	Decisions     map[string]Decision `json:"decisions"`
}

// LoadDecisions reads the asks. A product whose line has never stopped has been
// asked nothing rather than having a broken record, so an absent file is an
// empty map.
func (s *Store) LoadDecisions() (DecisionMap, error) {
	var stored DecisionMap
	found, err := readJSON(s.decisionPath(), "slack decision map", &stored)
	if err != nil {
		return DecisionMap{}, err
	}
	if !found {
		return DecisionMap{SchemaVersion: DecisionMapSchemaVersion, Decisions: map[string]Decision{}}, nil
	}
	if stored.SchemaVersion != DecisionMapSchemaVersion {
		return DecisionMap{}, fmt.Errorf("slack decision map schema version %d is not supported", stored.SchemaVersion)
	}
	if stored.Decisions == nil {
		stored.Decisions = map[string]Decision{}
	}
	return stored, nil
}

// SaveDecisions replaces the asks durably.
func (s *Store) SaveDecisions(decisions DecisionMap) error {
	decisions.SchemaVersion = DecisionMapSchemaVersion
	if decisions.Decisions == nil {
		decisions.Decisions = map[string]Decision{}
	}
	return s.write(s.decisionPath(), "slack decision map", decisions)
}

// Lookup reports the ask one direct message thread carries. A thread this sink
// asked nothing in is not in the map, which is every thread somebody starts
// themselves.
func (m DecisionMap) Lookup(channel, threadTS string) (Decision, bool) {
	asked, found := m.Decisions[decisionKey(channel, threadTS)]
	if !found || strings.TrimSpace(asked.Mark) == "" {
		return Decision{}, false
	}
	return asked, true
}

// AskedOf reports one operator already having been asked about one state. It is
// what keeps a state that stands all night to one message per person: the
// heartbeat says it again in the channel every hour, and this is asked once.
func (m DecisionMap) AskedOf(mark, member string) bool {
	for _, asked := range m.Decisions {
		if asked.Mark == mark && asked.Member == member {
			return true
		}
	}
	return false
}

// Latest reports the most recent ask this sink made in one conversation, which
// is what a message typed in that conversation rather than in a thread most
// likely meant to answer. Ties are settled by the thread it was asked in, so two
// asks recorded in the same instant name the same one of themselves every time
// rather than whichever the map happened to hand back first.
func (m DecisionMap) Latest(channel string) (Decision, bool) {
	var latest Decision
	found := false
	for _, asked := range m.Decisions {
		if asked.Channel != channel || strings.TrimSpace(asked.Mark) == "" {
			continue
		}
		if found && !laterAsk(asked, latest) {
			continue
		}
		latest, found = asked, true
	}
	return latest, found
}

// laterAsk reports the more recent of two asks. The thread settles a tie, so the
// order is total and a map walked in two different orders answers the same.
func laterAsk(asked, held Decision) bool {
	if asked.AskedAt.Equal(held.AskedAt) {
		return asked.ThreadTS > held.ThreadTS
	}
	return asked.AskedAt.After(held.AskedAt)
}

// Record remembers one ask.
func (m *DecisionMap) Record(asked Decision) {
	if m.Decisions == nil {
		m.Decisions = map[string]Decision{}
	}
	m.Decisions[decisionKey(asked.Channel, asked.ThreadTS)] = asked
}

// Forget drops the oldest asks beyond what one map keeps, and reports whether it
// dropped any, so a map with nothing to forget is not rewritten.
//
// It is bounded rather than pruned to what is current, because the two things
// this map is for expire at different moments. Not asking somebody twice about
// the state they are looking at needs only the standing mark, and that is what
// is never dropped. But an ask stays answerable after its state clears — a
// decision made an hour late is still a decision, and dropping the thread it was
// made in would turn it into silence — so what is kept behind the standing one
// is a window of recent asks rather than none.
//
// A hard count is what makes it a bound. Every mark carries the moment its state
// began, so a line that stops and restarts all day is a new mark every time, and
// a map that only ever grew would eventually pass maxRecordBytes — at which
// point it stops loading, nobody is asked, and no reply is read. That failure is
// silent in both directions, which is the one this whole tier exists to avoid.
func (m *DecisionMap) Forget(standing string, keep int) bool {
	if len(m.Decisions) <= keep {
		return false
	}
	stale := make([]string, 0, len(m.Decisions))
	for key, asked := range m.Decisions {
		// The state somebody is being asked about now is never forgotten, whatever
		// the bound says: dropping it is asking them the same question again.
		if asked.Mark != standing {
			stale = append(stale, key)
		}
	}
	// Oldest first, and by key where two were asked in the same instant, so a
	// pass drops the same entries twice over rather than whichever the map
	// happened to walk first.
	sort.Slice(stale, func(i, j int) bool {
		first, second := m.Decisions[stale[i]], m.Decisions[stale[j]]
		if first.AskedAt.Equal(second.AskedAt) {
			return stale[i] < stale[j]
		}
		return first.AskedAt.Before(second.AskedAt)
	})
	dropped := false
	for _, key := range stale {
		if len(m.Decisions) <= keep {
			break
		}
		delete(m.Decisions, key)
		dropped = true
	}
	return dropped
}

// decisionKey is one ask's identity: the thread it was said in, qualified by the
// conversation, for the reason a refusal's is — a thread timestamp only means
// anything inside the conversation it was posted in.
func decisionKey(channel, threadTS string) string {
	return channel + "/" + threadTS
}

// Cursor is how far the sink has read one durable stream, and the streams are
// read four ways because they are four shapes.
//
// A log advances by position. The operator's switches are a fixed pair of facts
// about one subject, and what has been said about them is recorded by name,
// because nothing records a hold being lifted — the lift is knowable only by
// having said the hold. A run is neither: what is worth saying about it is the
// difference between two readings of its record, so the reading already reported
// is kept and the next pass is a comparison against it. And the line's own state
// is none of the three: it is not a record at all but a condition derived from
// several, said again while it stands rather than once when it began, so what is
// kept is which state is standing and when it was last said.
type Cursor struct {
	Position  uint64   `json:"position,omitempty"`
	Delivered []string `json:"delivered,omitempty"`
	// Reported is the run state everything already said was said about. The
	// crossings between it and the record's current reading are what has not been
	// said yet, so it is advanced only once the whole of a crossing has been
	// posted: advancing it earlier would lose the rest of it to a crash.
	Reported *runstate.State `json:"reported,omitempty"`
	// Closed marks a run that is over and owes nothing, whose reported reading is
	// dropped because nothing more can cross. Without it the sink would carry a
	// copy of every run a product ever made.
	Closed bool `json:"closed,omitempty"`
	// Standing and Said are the fourth shape, and they are the only one that is a
	// state rather than a history: what the line is currently stopped by, and when
	// the clock on saying it again was last set — which is the moment the state was
	// first seen as well as the moment it was last said, because the first sighting
	// is what starts the interval rather than something to announce.
	//
	// A state that clears forgets both, so the next one to stand is armed afresh
	// rather than inheriting an interval that has already run out.
	Standing string    `json:"standing,omitempty"`
	Said     time.Time `json:"said,omitempty"`
}

// Has reports whether one named milestone has already been posted.
func (c Cursor) Has(mark string) bool {
	for _, delivered := range c.Delivered {
		if delivered == mark {
			return true
		}
	}
	return false
}

// Marked reports the first delivered mark under a prefix, which is how a hold
// that was said is found again when what lifted it is only its absence.
func (c Cursor) Marked(prefix string) (string, bool) {
	for _, delivered := range c.Delivered {
		if strings.HasPrefix(delivered, prefix) {
			return delivered, true
		}
	}
	return "", false
}

// With returns the cursor including one further milestone. It is a value
// rather than a mutation so an advance is only durable once it is written.
func (c Cursor) With(mark string) Cursor {
	if c.Has(mark) {
		return c
	}
	advanced := c
	advanced.Delivered = append(append([]string(nil), c.Delivered...), mark)
	sort.Strings(advanced.Delivered)
	return advanced
}

// Without returns the cursor with one milestone forgotten. What it forgets is a
// pair that has been said in full — a hold placed and the same hold lifted —
// because keeping it would grow the cursor by a line for every afternoon
// somebody was away from the machine.
func (c Cursor) Without(mark string) Cursor {
	dropped := c
	dropped.Delivered = nil
	for _, delivered := range c.Delivered {
		if delivered != mark {
			dropped.Delivered = append(dropped.Delivered, delivered)
		}
	}
	return dropped
}

// Cursors is every stream's position as it is stored, and the moment reading
// this product began.
type Cursors struct {
	SchemaVersion int               `json:"schema_version"`
	Streams       map[string]Cursor `json:"streams"`
	// Since is the watermark: the moment somebody first pointed a sink at this
	// product. What the product recorded before it is history nobody turned
	// reporting on to read, and a channel opened today would rather not have a
	// month of finished work arriving at once.
	//
	// It is durable and it is written once, which is the whole of its value. A
	// watermark taken freshly on every process start would move forward past
	// every outage, so a report filed while the sink was down would be older than
	// the restart and be read past as history — which is the one record an
	// operator most needs when they come back. Fixed once, downtime is a gap the
	// sink reads across rather than a gap in what it says.
	Since time.Time `json:"since,omitempty"`
}

// LoadCursors reads the cursors. A sink that has never run has read nothing,
// which is not the same as having read everything: the first pass therefore
// reports the milestones a product already has in flight. That is deliberate — a
// sink started for the first time in the middle of a working day should say what
// is going on, not stay silent until the next run starts — and the setup document
// says so, because it is the one surprise in an otherwise quiet channel.
func (s *Store) LoadCursors() (Cursors, error) {
	var stored Cursors
	found, err := readJSON(s.cursorPath(), "slack cursors", &stored)
	if err != nil {
		return Cursors{}, err
	}
	if !found {
		return Cursors{SchemaVersion: CursorsSchemaVersion, Streams: map[string]Cursor{}}, nil
	}
	if stored.SchemaVersion != CursorsSchemaVersion {
		return Cursors{}, fmt.Errorf("slack cursor schema version %d is not supported", stored.SchemaVersion)
	}
	if stored.Streams == nil {
		stored.Streams = map[string]Cursor{}
	}
	return stored, nil
}

// SaveCursors replaces the cursors durably. It is written after each post
// rather than at the end of a pass, because the cursor is what stops a message
// being sent twice and a pass that posted ten messages and then died would
// otherwise repeat all ten.
func (s *Store) SaveCursors(cursors Cursors) error {
	cursors.SchemaVersion = CursorsSchemaVersion
	if cursors.Streams == nil {
		cursors.Streams = map[string]Cursor{}
	}
	return s.write(s.cursorPath(), "slack cursors", cursors)
}

// Keep drops the cursors of streams that no longer exist, so a machine that has
// run for months does not carry a cursor for every run it ever made. It reports
// whether it dropped anything, so a sink that has nothing to forget writes
// nothing: the ordinary pass finds no news, and a state file rewritten every few
// seconds for years is churn nobody asked for.
func (c *Cursors) Keep(streams map[string]struct{}) bool {
	dropped := false
	for name := range c.Streams {
		if _, live := streams[name]; !live {
			delete(c.Streams, name)
			dropped = true
		}
	}
	return dropped
}

func (s *Store) threadPath() string { return filepath.Join(s.root, "threads.json") }
func (s *Store) cursorPath() string { return filepath.Join(s.root, "cursors.json") }
func (s *Store) steerPath() string  { return filepath.Join(s.root, "steers.json") }

func (s *Store) refusalPath() string  { return filepath.Join(s.root, "refusals.json") }
func (s *Store) decisionPath() string { return filepath.Join(s.root, "decisions.json") }

// write replaces one record atomically: a sink killed mid-write leaves the
// previous file rather than half of the new one, which for the thread map is the
// difference between reopening one thread and losing every thread it holds.
func (s *Store) write(path, label string, value any) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", label, err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxRecordBytes {
		return fmt.Errorf("encoded %s is %d bytes, limit is %d", label, len(encoded), maxRecordBytes)
	}
	temporary, err := os.CreateTemp(s.root, ".slack-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", label, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary %s: %w", label, err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return fmt.Errorf("write %s: %w", label, err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync %s: %w", label, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary %s: %w", label, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", label, err)
	}
	return nil
}

// readJSON reads one record, reporting whether it was there at all.
func readJSON(path, label string, value any) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open %s: %w", label, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxRecordBytes))
	if err := decoder.Decode(value); err != nil {
		return false, fmt.Errorf("decode %s: %w", label, err)
	}
	return true, nil
}
