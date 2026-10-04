package slack

// What the sink reads, and where the reading stops.
//
// Nothing here decides what an event means or how it is said. Which crossings of
// a durable record are worth reporting, which persona's account each one is, and
// the words it is said in all belong to the notifier, and this reads that
// package rather than repeating it: a second producer of reportable events adds
// a selection function there and nothing at all here.
//
// What is left is the part that is genuinely the sink's — where the reading got
// to. The notifier's selection is a pure comparison of two readings of a record,
// so somebody has to remember the earlier reading; the notifier's other
// producers are logs, so somebody has to remember the position. That is a
// cursor, it is durable, and it is written after each message rather than at the
// end of a pass, because it is the only thing standing between a crash and
// saying everything again.

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The streams a product has. Runs and conversations take one each, named for
// the run or the conversation, because they are separate subjects rather than
// one log; the rest are one apiece.
const (
	reportStream = "reports"
	// amendmentStream is the amendment log read by record — proposals and
	// decisions alike, the decisions in silence. It replaced proposalStream, which
	// counted proposals alone, when a line that will not decode had to hold a
	// position: such a line says nothing about which of the two it was, so the
	// only count it can hold a place in is the count of lines. A cursor still
	// standing on the old stream is carried across once, by amendmentCursor, and
	// the old stream is dropped with the pass that carried it.
	amendmentStream  = "amendments"
	proposalStream   = "proposals"
	productStream    = "product"
	watchStream      = "watch"
	usageLimitStream = "usage-limits"
	heartbeatStream  = "heartbeat"
	residentStream   = "resident"
	stallStream      = "stall"
	// capacityStream is the provider holding every role, said again while it
	// stands. It is a stream of its own rather than a mark on the stall's for the
	// reason the resident is: it is true at a time the stall's own record is
	// silent, and the two say opposite things about one quiet.
	capacityStream  = "capacity"
	claimStream     = "claims"
	directiveStream = "directives"
	// providerStream is the provider answering nobody, said once when it is seen
	// and once when it answers again. It is a stream of its own for the reason
	// the capacity stream is: a state said on a record rather than a crossing of
	// one, and true at a time every other record is silent.
	providerStream = "provider"
	// operatorActionStream is what needs the operator's own hand, said to him
	// once per finding. It is a stream of its own rather than a mark on the
	// reports' because a finding is derived from a report and what became of it
	// together — a handling can make one of a report the stream has long read
	// past — and because each is said once and remembered by name rather than by
	// position.
	operatorActionStream = "operator-actions"
	// improvementStream is what the project's template offers that the project
	// has never edited. It is a stream of its own rather than a mark on the
	// product's because what it holds is one mark per improvement rather than a
	// fixed pair of facts, and because it is the one reading here that is about
	// the installation rather than about the work.
	improvementStream = "improvements"
)

func runStream(runID string) string { return "run:" + runID }

func conversationStream(conversationID string) string { return "chat:" + conversationID }

// conversationLog is how a message names a conversation's log. It names the
// role and, where the project configured more than one agent for it, the
// agent — the way a persona is named on its own messages — and never the
// conversation's identifier, which is not something anybody reading a channel
// does anything with.
func conversationLog(conversation runstate.Conversation) string {
	identity := conversation.Identity()
	named := conversation.Role.Title()
	if identity.Agent != "" && identity.Agent != string(conversation.Role) {
		named += " (" + identity.Agent + ")"
	}
	return named + "'s conversation"
}

// The prefixes the operator's two switches are marked under. A hold is marked
// with the moment it was placed, so a second hold a week later is a second thing
// to say rather than the same one.
const (
	intakeMark = "intake:"
	holdMark   = "hold:"
	// brakeEscalationMark records having told the operators that the harness
	// escalated the brake's hold to them at its cycle bound. It is marked with
	// the moment of the escalation rather than of the hold, so a hold that is
	// released and a later one escalated afresh is a second thing to say, and
	// it is forgotten with the hold's own mark when the hold lifts.
	brakeEscalationMark = "brake-escalated:"
	// brakeDecisionMark records having told the operators that the development
	// manager escalated the brake's hold to them. It is marked with the moment
	// of her decision, and forgotten with the hold's own mark.
	brakeDecisionMark = "brake-decided-escalate:"
	// outcomeMark records having said what became of one directive somebody
	// asked for in a thread. It names the directive, because a directive is
	// settled once and what settled it is said once.
	outcomeMark = "outcome:"
	// withdrawalMark records having said that one such directive was taken back.
	// It is a mark of its own rather than the outcome's because a directive can
	// be both — carried out and later withdrawn — and each is said once.
	withdrawalMark = "withdrawal:"
	// buildMark names the build a running session is standing on, so a restart
	// onto a different binary re-arms rather than inheriting the last one's clock.
	buildMark = "build:"
	// escalatedMark records having told the operators directly about the build
	// this cursor is standing on. It carries no name of its own because the build
	// is already the cursor's standing state: a different build is a different
	// cursor, and the mark goes with it.
	escalatedMark = "escalated"
	// stallMark names the stall this cursor is standing on, and said last at the
	// cursor's Said. It names the stall rather than the state for the reason the
	// build mark names a build: a second stall is a second thing to say afresh,
	// and the same one is said again on the heartbeat's clock. Before
	// yoyodyne-ifd.354 it was a delivered mark meaning said once; a cursor still
	// carrying one is read as a stall this sink has not yet said.
	stallMark = "stall:"
	// windowMark names the provider usage window this cursor has already said
	// something about, by the deadline the provider named for it. It shares the
	// stall's stream because the two are read from one silence and are never both
	// standing: a window that accounts for the quiet is precisely a quiet with no
	// stall in it.
	windowMark = "window:"
	// improvementMark names one improvement this cursor has already said. It
	// names the value the template now supplies as well as the key, because a
	// template that improves one setting twice has improved it twice: a mark that
	// held the key alone would swallow the second one for the life of the project.
	improvementMark = "improvement:"
	// findingMark names one finding for the operator this cursor has already said,
	// by the key the read model gives it. It is dropped once the finding is no
	// longer standing, so a report handled and later handled again as needing
	// him is a second finding said once more rather than swallowed by the first.
	findingMark = "finding:"
	// unrelatedMark records having said, in the sink's own log, that the build a
	// session is running belongs to a repository this sink is not pointed at. It
	// is marked for the same reason the escalation is: it is true for as long as
	// that build stands, and a log line repeated every hour about an installation
	// that is behaving is noise rather than an account.
	unrelatedMark = "unrelated"
)

// Delivery is one step of one stream: what to say, and the cursor that records
// having said it. The cursor is the whole of the delivery guarantee — the sink
// posts and then writes it, so a process killed between the two repeats one
// message rather than losing it.
//
// A delivery with nothing to say is a cursor advance on its own. It is how a run
// that is over stops being carried and how reports older than this sink are read
// past, neither of which anybody should be told about.
type Delivery struct {
	Stream       string
	Cursor       Cursor
	Notification notify.Notification
	// Mention is the Slack member id this delivery is for, empty for the ordinary
	// message that is for whoever is reading the channel. It is set on the one
	// class of message that answers a person rather than reports to a channel:
	// what became of something they asked for in a thread. Being told is the
	// whole point of it, and a thread they are not looking at is silence.
	Mention string
	// Reply is the timestamp of the message that asked for this, empty for every
	// delivery that is not an answer to one. It is the message the receipt sits
	// on: it has worn the thinking face since it arrived, and saying what became
	// of the directive is the moment that stops being true.
	Reply string
	// Direct asks for this delivery to reach the operators where they will see it
	// at three in the morning, as well as in the channel. It is set on the one
	// class of message that is about the harness itself being degraded rather than
	// about any work, because a channel is a place somebody chooses to look and a
	// degraded harness is exactly what they will not think to look for.
	//
	// It names nobody. Who the operators are is the surface's — the same member
	// ids a reply is authorized against — and a feed that named them would be a
	// reading of the durable records holding an opinion about a workspace.
	Direct bool
	// Tag asks for this delivery to name the operators by member id in the
	// channel, so the workspace notifies them. It is set on the class of message
	// that is both important and theirs to act on: a provider nobody is logged
	// into is ended by a person and nothing else, and so are a line that has
	// stopped for reasons no record names and a brake hold the development
	// manager has handed to the operator — both said again while they stand,
	// tagged each time, because a stopped line is the most serious thing this
	// surface reports and the one message it must not let go stale. Like Direct
	// it names nobody; who the operators are is the surface's.
	Tag bool
}

// Silent reports a delivery selection had nothing to say about, which advances a
// cursor and posts nothing.
func (d Delivery) Silent() bool { return d.Notification.Silent() }

// Posts reports a delivery worth putting somewhere a person reads. It is the
// predicate the pass actually wants, and it is wider than Silent by one case: an
// event whose reach is the durable record it came from is a real milestone that
// belongs in that record and nowhere else, so it advances its cursor exactly as
// a silent one does.
//
// A delivery that answers one person by name is the exception, and it posts
// whatever its reach says. Being told is the whole point of it: somebody typed a
// reply and is waiting to hear what came of it, and a posting policy about how
// much of the channel a milestone is worth has nothing to say about a message
// that is a person's answer.
func (d Delivery) Posts() bool {
	if strings.TrimSpace(d.Mention) != "" {
		return !d.Silent()
	}
	return d.Notification.Posts()
}

// Batch is one pass over the durable records: what is ready to post, which
// streams still exist so cursors for the rest can be dropped, and what each
// topic is doing right now.
type Batch struct {
	Deliveries []Delivery
	Streams    map[string]struct{}
	// Statuses is each topic's current status, keyed exactly as a thread is. It
	// is a reading rather than a history, and that is why it is beside the
	// deliveries instead of among them: a delivery is a transition said once, and
	// a status is what is true at the end of this pass however many transitions
	// got there — including none, which is the case a message could never carry.
	// A run that finishes with nothing left to say still has to stop reading as
	// working.
	Statuses map[string]notify.Status
	// Partial says this pass read past a record it could not read at all, so the
	// streams above are not the whole of the product's: the stream that record
	// would have named may be missing from them, and a cursor dropped on their
	// account is one that stream loses for good. Every delivery is still posted
	// and every cursor it moves still moves; only the forgetting waits for a pass
	// that read everything.
	Partial bool

	// Asking is the decision the operators are owed about a line that has stopped,
	// nil on every pass over a line that is moving. It is beside the deliveries
	// rather than among them because a delivery is something said into the
	// channel, and this is a question put to named people in a direct message —
	// same reading of the same records, different audience and a different act.
	//
	// Whether each of them has already been asked is the sink's to know, so this
	// is produced while the state stands rather than once: the reading cannot
	// remember who was told, and a feed that tried to would be keeping the record
	// of a message it does not send.
	Asking *Ask
}

// Feed is where the sink's messages come from. It is polled rather than
// subscribed to because the records it reads are files written by other
// processes, and because a sink that has been away has to catch up from its
// cursors either way.
type Feed interface {
	Poll(ctx context.Context, cursors Cursors) (Batch, error)
}

// HarnessFeed reads the product's own durable records: what became of its runs,
// what its conversations did to the backlog, what its agents reported and
// proposed while their work carried on, and the operator's two switches over the
// whole line.
type HarnessFeed struct {
	Runs *runstate.Store
	// Conversations is where the backlog moving is read from. It is optional
	// only in the sense that a feed assembled without it reports everything else:
	// a product whose queue changes invisibly is the thing this exists to
	// prevent, and every sink the harness builds is given one.
	Conversations *runstate.ConversationStore
	Reports       *runstate.ReportStore
	// Decisions is what triage has decided about each item's stoppages, read for
	// one thing: a stopped run the development manager escalated to the operator,
	// which is a finding that needs his hand. It is optional, and a feed
	// assembled without one names no escalated stoppage — which is silence
	// exactly where a person was handed something, so every sink the harness
	// builds is given one. It is satisfied by *runstate.TriageStore.
	Decisions readmodel.Decisions
	Proposals *runstate.AmendmentStore
	Intake    *runstate.IntakeHoldStore
	Holds     *runstate.OperatorHoldStore
	// Watch is where a watch session says what it is doing. It is optional in the
	// same sense the conversations are: a feed assembled without one reports
	// everything else, and what is lost is the one thing nothing else in the
	// record says — that the session choosing work is alive and idle rather than
	// dead.
	Watch *runstate.WatchStore
	// UsageLimits is where a provider refusing the harness outside a run is read
	// from: a conversation turn, an independent review. It is optional in the
	// same sense the two above are, and what a feed assembled without one loses
	// is the only account there is of those refusals — a run says its own by
	// parking, and nothing else says anything at all.
	UsageLimits *runstate.UsageLimitStore
	// Outages is the product's record of the provider answering nobody — a login
	// nobody has renewed, an API nothing reaches. It is optional in the same sense
	// the three above are, and what a feed assembled without one loses is the one
	// message the 2026-09-17 outage needed: the operator told the moment it
	// happened, and told when it ended.
	Outages *runstate.ProviderOutageStore
	// Divergences is the product's record of the target branches the harness
	// will not catch up to the remote's. It is optional in the same sense, and a
	// feed assembled without one never says the line is standing still on one.
	Divergences *runstate.DivergedTargetStore
	// Backlog is how much admitted work the tracker calls ready, and it is read
	// for one purpose: telling a line that is waiting on somebody from one that is
	// honestly quiet. It is optional, and a feed assembled without one says
	// everything else and never says the line is waiting — which is silence over a
	// held queue, so every sink the harness builds is given one.
	Backlog Backlog
	// Directives is the product's durable directive record, read for one thing
	// only: what became of a directive somebody asked for in a thread. It is
	// optional, and a feed assembled without one says nothing about any
	// directive — which is silence exactly where somebody is waiting for an
	// answer, so every sink the harness builds is given one.
	//
	// Nothing here writes it. Recording a directive is the inbound half's and
	// resolving one is the operator's; this is a reading of what they did.
	Directives *runstate.DirectiveStore
	// Steers is the sink's own memory of which directives were said into a
	// thread, by whom, and in which message. It is what separates a directive to
	// answer somebody about from one typed at a terminal, which has no thread and
	// nobody to tag, and it is read here and written only by the connection.
	Steers *Store
	// Deployments is how far the product's repository has moved past the build a
	// live watch session is running, read for one thing: telling a session that is
	// executing what is deployed from one that is executing a binary from before
	// the fixes it is about to spend rounds rediscovering. It is optional, and a
	// feed assembled without one says everything else and never says a session is
	// stale — which is the silence this exists to end, so every sink the harness
	// builds is given one.
	Deployments Deployments
	// StaleBuildThreshold is how far behind a session has to be before the
	// operators are told directly rather than in the channel alone. Zero takes
	// DefaultStaleBuildThreshold.
	StaleBuildThreshold int
	// CapacityEscalation is how long the provider may hold every role before the
	// hold is said as critical and taken to the operators with every repetition
	// rather than the first. Zero takes DefaultCapacityEscalation.
	CapacityEscalation time.Duration
	// StallEscalation is how long a line may stand stopped on a person — a stall
	// the record holds, or a brake hold that waits on the operator — before it
	// is said as critical rather than as a warning. Zero takes
	// DefaultStallEscalation.
	StallEscalation time.Duration
	// FailingTaskEscalation is how long a recurring task may stand failing
	// before its first turn before it is said again as critical. Zero takes
	// DefaultFailingTaskEscalation.
	FailingTaskEscalation time.Duration
	// Stalls is the durable record of this product having gone quiet — nothing
	// started, over work the tracker calls ready, with nothing accounting for it.
	// It is read here and never written: what notices and records a stall is
	// internal/watchdog, run from machinery that is always running, because Slack
	// reporting is opt-in and a watchdog that only ran where somebody had turned it
	// on left the products least able to notice with no history at all.
	//
	// It is optional, and a feed assembled without one says everything else and
	// never says this product has gone quiet — which is the seven and a half hours
	// that asked for this, so every sink the harness builds is given one.
	Stalls *runstate.StallStore
	// Claims is the durable record of the claims the harness gave back because
	// nothing was working on them. Unlike the stalls above it, this feed only reads
	// it: the audit that writes it runs in the watch loop, because giving a claim
	// back is directing work and this surface does not direct work.
	//
	// It is optional, and a feed assembled without one says everything else and
	// never mentions a claim the harness unstuck — which is a second run for an
	// item with nothing accounting for the first, so every sink the harness builds
	// is given one.
	Claims ReleasedClaims
	// Improvements is what the project's template supplies now against what it
	// supplied when this project was generated, read for the one class of value
	// that is an offer: improved by the template and never edited here. It is
	// optional, and a feed assembled without one says everything else and never
	// mentions the template — which is the silence this was added to end, because
	// every other surface that says it is one somebody has to run.
	//
	// Nothing here decides what an improvement is or how it is worded. That is the
	// configuration's three-way comparison, which `yoyo config drift` prints from
	// too; this reads it and says one of them at a time.
	Improvements Improvements
	// Standing is where the harness stands, as the read model derives it: the same
	// four lines `yoyo status` prints, said with the heartbeat so a channel and a
	// terminal answer one question one way. It is optional, and a feed assembled
	// without one says the heartbeat exactly as it did before the lines existed —
	// with the message itself saying the lines could not be read here, rather than
	// leaving them out and reading as a harness with nothing in any of them.
	Standing *readmodel.Sources
	// Heartbeat is how often a line that is choosing nothing over ready work says
	// so again. Zero takes DefaultHeartbeat. It is a cadence rather than a switch:
	// there is deliberately no way to turn it off, because what it would buy is
	// silence that means waiting-on-you, which is the state it exists to end.
	Heartbeat time.Duration
	// Now is read for the moment a hold was seen to have lifted, and for the age a
	// heartbeat says a state has stood. Both are things no record holds — what
	// lifts a hold is its absence, and what makes a state worth saying again is how
	// long ago it began — so it is injected and a test can say when they were.
	Now func() time.Time
	// Log is where a record that cannot be addressed at all is said out loud
	// before it is read past. It is the sink's own log, and it is never given a
	// token to print.
	Log func(format string, args ...any)

	// unread is each record the feed is reading past because it could not be
	// read, with what refused it, so each is said once while it stands rather
	// than once a pass. A record that reads again is forgotten, so it is said
	// afresh if it breaks again later.
	unread map[string]string
}

// Poll reads every stream and reports what the cursors say has not been posted.
//
// Everything the product recorded before the cursors' watermark is history and
// is read past: a channel turned on today does not want a month of finished work
// arriving at once. The watermark is one durable moment for the whole product
// rather than this process's start time, which is what makes an outage a delay:
// a record filed while the sink was down is after the watermark whatever the
// cursor for its stream happens to say, so it is posted when the sink returns
// rather than mistaken for history somebody has already read.
func (f *HarnessFeed) Poll(ctx context.Context, cursors Cursors) (Batch, error) {
	batch := Batch{Streams: map[string]struct{}{
		reportStream:    {},
		amendmentStream: {},
		productStream:   {},
	}}
	since := cursors.Since
	// What this pass reads past, so what stopped standing can be forgotten once
	// the pass is over.
	unread := map[string]bool{}

	// A run record this build cannot read is read past rather than failing the
	// pass: the sink reports on each run independently, so one record nobody can
	// read is a reason to say nothing about that run and not about every other
	// stream as well. Its stream is kept among the pass's streams, so its cursor
	// is where it was when the record reads again.
	states, unreadable, err := f.Runs.RecordedReadable()
	if err != nil {
		return Batch{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	for _, record := range unreadable {
		batch.Streams[runStream(record.Record)] = struct{}{}
		batch.Partial = true
		f.readPast(unread, "run "+record.Record, record.Err)
	}
	// What is still in flight is counted from the same reading the crossings are
	// selected from, rather than asked for a second time: a run is in flight or it
	// is not, and two readings of the same files a moment apart could disagree
	// about it.
	// What is waiting on the forge is counted from that same reading, for the same
	// reason and by the read model's derivation rather than by a reading of the
	// publication fields taken here: it is the same derivation the attention line
	// of `yoyo status` lists, so a count said here is a count that line names.
	inFlight, awaitingForge := 0, len(readmodel.AwaitingForge(states))
	// Every ending's preservation, and whether an approved change can still be
	// resumed, is asked of the repository by the look the docket and the hold
	// take. A feed without one says that nothing looked.
	var remains readmodel.Remains
	if f.Standing != nil {
		remains = f.Standing.Remains
	}
	look := readmodel.Looking(ctx, remains, f.Now)
	for _, state := range states {
		if err := ctx.Err(); err != nil {
			return Batch{}, err
		}
		if !state.Status.Terminal() {
			inFlight++
		}
		stream := runStream(state.RunID)
		batch.Streams[stream] = struct{}{}
		deliveries, err := f.runDeliveries(state, cursors.Streams[stream], since, look)
		if err != nil {
			return Batch{}, err
		}
		batch.Deliveries = append(batch.Deliveries, deliveries...)
	}
	// What each item is doing is read from the same reading its crossings were
	// selected from, for the reason the in-flight count is: a status derived from
	// a second reading a moment later could contradict the messages posted beside
	// it in the same pass.
	batch.Statuses = itemStatuses(states, since)

	conversed, err := f.conversationDeliveries(ctx, cursors, &batch, unread)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, conversed...)

	filed, tornReports, err := f.Reports.Scan()
	if err != nil {
		return Batch{}, fmt.Errorf("read the collected reports: %w", err)
	}
	reported, err := f.logDeliveries(reportStream, "reports", cursors.Streams[reportStream], len(filed), tornReports, since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromReport(filed[index])
			return filed[index].RecordedAt, notification, err
		})
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, reported...)

	// What in the pile needs the operator's own hand, from the same reading of
	// the reports the stream above was read from and from what became of them.
	// It is read here rather than as the reports are because a finding is not a
	// report: a report is said where it is filed, and a finding stands from the
	// moment a handling makes one until its recorded ending. Product-pass
	// findings are read with their sweeps and end when the affected pass succeeds.
	// The amendment log is read ahead of the findings because the batches the
	// owners argued are findings too, and are read from it.
	records, tornRecords, err := f.Proposals.Scan()
	if err != nil {
		return Batch{}, fmt.Errorf("read the proposed changes: %w", err)
	}
	findings, err := f.operatorActionDeliveries(cursors.Streams[operatorActionStream], filed, states, records, cursors.Since, batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, findings...)

	// The amendment log advances by record rather than by proposal, so that a
	// line which will not decode — which says nothing about whether it was a
	// proposal or a decision — holds a position the proposals behind it are
	// counted past. A decision is read past in silence, exactly as a conversation
	// event that is not a milestone is.
	raised, err := f.logDeliveries(amendmentStream, "proposals", f.amendmentCursor(cursors, records, tornRecords), len(records), tornRecords, since,
		func(index int) (time.Time, notify.Notification, error) {
			proposal := records[index].Proposal
			if proposal == nil {
				return time.Time{}, notify.Notification{}, nil
			}
			notification, err := notify.FromProposal(*proposal)
			return proposal.RaisedAt, notification, err
		})
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, raised...)

	// The watch log is read once and used twice: for what each session said it was
	// doing, and for whether any session is still doing it. Reading it twice would
	// let one pass post a session stopping and then derive a line from a log that
	// had moved underneath it.
	sessions, tornSessions, err := f.sessions()
	if err != nil {
		return Batch{}, err
	}
	watched, err := f.watchDeliveries(cursors, batch.Streams, sessions, tornSessions)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, watched...)

	refused, err := f.usageLimitDeliveries(cursors, batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, refused...)

	outcomes, err := f.directiveDeliveries(cursors, batch.Streams, unread)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, outcomes...)

	// The operator's two switches are read once and used twice: for the messages
	// that say they were placed and lifted, and for what has stopped the line when
	// the heartbeat below asks. Reading them apart would let one pass post a hold
	// and derive a line nothing is holding.
	//
	// A hold that cannot be read is never taken for an absent one, so nothing
	// derived from the switches is said on this pass — the holds themselves, the
	// heartbeat, and the provider's outage — and every stream that does not turn
	// on them carries on.
	held, err := f.switches()
	switched := err == nil
	if switched {
		batch.Deliveries = append(batch.Deliveries, f.holdDeliveries(cursors.Streams[productStream], held)...)
	} else {
		batch.Partial = true
		f.readPast(unread, "the operator's holds", err)
	}

	// What is ready to pull is asked at most once a pass, however many of this
	// pass's readings want it. One does today — the waiting line, which gates
	// itself to one reading a heartbeat — and it is still asked through this
	// rather than directly, so a second reading that wants the number costs
	// nothing and cannot disagree with the first about one queue.
	ready := f.readyOnce()

	if switched {
		beat, asking, err := f.heartbeatDeliveries(ctx, cursors.Streams[heartbeatStream], held, sessions, inFlight, awaitingForge, ready, batch.Streams)
		if err != nil {
			return Batch{}, err
		}
		batch.Deliveries = append(batch.Deliveries, beat...)
		batch.Asking = asking
	}

	// How old the binary choosing work is, from the same reading of the watch log
	// and the same reading of the runs. Both are records that stamp the build that
	// wrote them, and neither is read a second time here: a resident derived from
	// one reading and runs derived from another could disagree about which binary
	// is dispatching, which is the one question this stream turns on.
	//
	// It is a separate stream from the heartbeat above because the two are true at
	// opposite times: the line is said when nothing is being chosen, and a session
	// running an old binary is at its most expensive while it is busy.
	resident, err := f.residentDeliveries(ctx, cursors.Streams[residentStream], sessions, states, batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, resident...)

	// Whether anything is happening at all, as the product's own stall record
	// holds it — plus the provider's usage window from the same reading of the
	// watch log this pass already made, which is the one quiet stretch that has an
	// answer and no record.
	//
	// It is a separate stream from the heartbeat above for the reason the resident
	// is: the heartbeat says a state something wrote down about itself, and this
	// says the absence of anything having been written down at all — which is the
	// state the heartbeat structurally cannot see, because the thing that feeds it
	// is what has died.
	stalled, err := f.stallDeliveries(ctx, cursors, sessions, batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, stalled...)

	// Whether the provider is holding every role at once, from the refusal log
	// and the agents' configuration rather than from anything a session wrote
	// about itself. It is beside the stall rather than part of it because the two
	// are true at the same time and say opposite things: the stall says nothing
	// accounts for the quiet, and this says exactly what does and until when.
	holding, err := f.capacityDeliveries(ctx, cursors.Streams[capacityStream], batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, holding...)

	// Whether the provider is answering anybody at all, from the same reading of
	// the record the switches were read with. It is beside the capacity hold
	// rather than part of it because the two are opposite waits: the hold lifts
	// on the provider's clock, and this lifts when a person logs in or the
	// network returns.
	if switched {
		batch.Deliveries = append(batch.Deliveries, f.outageDeliveries(ctx, cursors.Streams[providerStream], held, batch.Streams)...)
	}
	// A recurring task whose firings keep failing before their first turn, from
	// the sweep log the standing reads it from, so the channel says it exactly
	// when the attention line lists it.
	batch.Deliveries = append(batch.Deliveries, f.failingTaskDeliveries(cursors.Streams[failingTaskStream], batch.Streams)...)
	// The claims the harness gave back, said beside the stall above because the two
	// answer one question from opposite ends: that one asks whether anything has
	// started, and this one asks whether what the tracker calls started actually
	// is. Neither can see the other's case — an item under a dead claim has left
	// the ready queue, so the stall reading is structurally silent about it.
	unstuck, err := f.claimDeliveries(cursors.Streams[claimStream], since, batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, unstuck...)

	// What the project's template offers that this project never edited. It is
	// last because it is the one reading here that is not about the work at all:
	// nothing has happened, nothing is degraded, and nothing is waiting on
	// anybody. It is said because a project that materialized from a template and
	// then never ran the command that would tell it is a project that goes on
	// running a persona the template has since fixed.
	improved, err := f.improvementDeliveries(ctx, cursors.Streams[improvementStream], batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, improved...)
	f.forgetReadable(unread)
	return batch, nil
}

// readPast says that one record could not be read and is being read past, once
// for as long as it stands with the same reason. Saying it every pass would be a
// line every few seconds for as long as the record stands — the live sink logged
// seven thousand of them over five records — which is how the one line that
// matters becomes the one an operator filters out.
func (f *HarnessFeed) readPast(unread map[string]bool, record string, err error) {
	unread[record] = true
	reason := err.Error()
	if said, found := f.unread[record]; found && said == reason {
		return
	}
	if f.unread == nil {
		f.unread = map[string]string{}
	}
	f.unread[record] = reason
	f.say("%s could not be read, so nothing is reported about it until it can be; every other stream is reported as usual: %v", record, err)
}

// forgetReadable forgets every record this pass read that an earlier pass could
// not, so a record that breaks again later is said again rather than taken for
// the one already said.
func (f *HarnessFeed) forgetReadable(unread map[string]bool) {
	for record := range f.unread {
		if !unread[record] {
			delete(f.unread, record)
		}
	}
}

// readyOnce answers what is ready to pull, asking the tracker at most once
// however often the returned function is called. `bd` is a process the sink
// spawns, so a pass that wanted the number twice would pay for it twice; the
// answer cannot change inside one pass anyway, and two readings a moment apart
// that disagreed would be two accounts of one queue.
func (f *HarnessFeed) readyOnce() func(context.Context) (int, error) {
	var (
		asked  bool
		count  int
		failed error
	)
	return func(ctx context.Context) (int, error) {
		if !asked {
			asked = true
			count, failed = f.Backlog.Ready(ctx)
		}
		return count, failed
	}
}

// itemStatuses reads what each work item is doing out of the runs recorded for
// it. An item is marked from its latest run, because a status answers what is
// happening to the item now: an earlier attempt that failed is not what somebody
// scanning the channel needs, and the thread below the mark still holds it.
//
// A run that was over before the watermark marks nothing. It is history the
// channel was never told about, and a thread opened today by something else —
// a report, the backlog moving — must not acquire a status from a run nobody
// here has said a word about.
func itemStatuses(states []runstate.State, since time.Time) map[string]notify.Status {
	latest := map[string]runstate.State{}
	for _, state := range states {
		if predates(since, completion(state)) {
			continue
		}
		if held, found := latest[state.WorkItemID]; found && !later(state, held) {
			continue
		}
		latest[state.WorkItemID] = state
	}
	statuses := make(map[string]notify.Status, len(latest))
	for item, state := range latest {
		topic, err := notify.WorkItem(item)
		if err != nil {
			// A run nothing can be addressed to is already said once and read past
			// where its crossings are selected; marking is not the place to say it
			// a second time on every pass.
			continue
		}
		statuses[topic.Key()] = notify.StatusOfRun(state)
	}
	return statuses
}

// later reports the more recent of two runs on one item. The start is what
// orders them — a second attempt begins after the first, whatever either goes on
// to do — and the last update settles the tie a repaired record could otherwise
// leave.
func later(state, held runstate.State) bool {
	if !state.StartedAt.Equal(held.StartedAt) {
		return state.StartedAt.After(held.StartedAt)
	}
	return state.UpdatedAt.After(held.UpdatedAt)
}

// sessions reads what the watch sessions did, in the order they did it, and
// beside it the lines of the log that would not decode. A product nobody has
// watched has no log rather than a broken one, and a feed assembled without a
// watch store reads none.
func (f *HarnessFeed) sessions() ([]runstate.WatchTransition, []runstate.SkippedLine, error) {
	if f.Watch == nil {
		return nil, nil, nil
	}
	transitions, skipped, err := f.Watch.Scan()
	if err != nil {
		return nil, nil, fmt.Errorf("read what the watch sessions did: %w", err)
	}
	return transitions, skipped, nil
}

// switches reads the operator's two holds. Neither absence is a failure: not
// holding anything is the ordinary state of both, and a record that cannot be
// read is an error rather than an absence, because a hold nobody can read must
// never be treated as one that was never placed.
func (f *HarnessFeed) switches() (switches, error) {
	intake, intakeHeld, err := f.Intake.Held()
	if err != nil {
		return switches{}, fmt.Errorf("read the intake hold: %w", err)
	}
	operator, operatorHeld, err := f.Holds.Held()
	if err != nil {
		return switches{}, fmt.Errorf("read the operator hold: %w", err)
	}
	read := switches{
		intake:       intake,
		intakeHeld:   intakeHeld,
		operator:     operator,
		operatorHeld: operatorHeld,
	}
	if f.Outages != nil {
		outage, away, err := f.Outages.Standing()
		if err != nil {
			return switches{}, fmt.Errorf("read whether the provider is answering: %w", err)
		}
		read.outage, read.away = outage, away
	}
	if f.Divergences != nil {
		diverged, err := f.Divergences.Standing()
		if err != nil {
			return switches{}, fmt.Errorf("read whether a target branch stands diverged: %w", err)
		}
		read.diverged = diverged
	}
	return read, nil
}

// amendmentCursor is where the amendment log is read from. It is the cursor on
// the amendment stream where this sink has one, and where it has not — a sink
// that last ran a build reading the log by proposal — it is the old stream's
// count of proposals carried across to the record after the last of them, so
// nothing already said is said again and nothing since is lost. The old stream
// is not among the pass's streams, so it is dropped once the pass that carried
// it has read everything.
func (f *HarnessFeed) amendmentCursor(cursors Cursors, records []amendment.Record, skipped []runstate.SkippedLine) Cursor {
	if cursor, found := cursors.Streams[amendmentStream]; found {
		return cursor
	}
	old, found := cursors.Streams[proposalStream]
	if !found || old.Position == 0 {
		return Cursor{}
	}
	// Proposals are counted in file order, with the lines that would not decode
	// holding their positions. The old build failed the whole pass on such a
	// line, so nothing it counted is on the far side of one.
	seen, torn := uint64(0), 0
	for index := 0; index < len(records)+len(skipped); index++ {
		if torn < len(skipped) && skipped[torn].Position == index {
			torn++
			continue
		}
		if records[index-torn].Proposal != nil {
			seen++
			if seen == old.Position {
				return Cursor{Position: uint64(index + 1)}
			}
		}
	}
	return Cursor{Position: uint64(len(records) + len(skipped))}
}

// watchDeliveries says what the sessions that choose work have been doing. It is
// an append-only log like the reports pile and advances by position, and it is a
// stream of its own rather than part of the product's marks because it is a
// history rather than a switch that is on or off: a session that idled all night
// and one that stopped at midnight are both things somebody reads afterwards.
func (f *HarnessFeed) watchDeliveries(cursors Cursors, streams map[string]struct{}, transitions []runstate.WatchTransition, skipped []runstate.SkippedLine) ([]Delivery, error) {
	if f.Watch == nil {
		return nil, nil
	}
	streams[watchStream] = struct{}{}
	return f.logDeliveries(watchStream, "watch", cursors.Streams[watchStream], len(transitions), skipped, cursors.Since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromWatch(transitions[index])
			return transitions[index].At, notification, err
		})
}

// usageLimitDeliveries says where a provider refused the harness outside a run.
// It is an append-only log like the watch transitions and advances by position,
// and it is a stream of its own rather than part of any run's: the processes
// that meet a refusal have no run between them, which is the whole reason the
// log exists.
func (f *HarnessFeed) usageLimitDeliveries(cursors Cursors, streams map[string]struct{}) ([]Delivery, error) {
	if f.UsageLimits == nil {
		return nil, nil
	}
	streams[usageLimitStream] = struct{}{}
	exhaustions, skipped, err := f.UsageLimits.Scan()
	if err != nil {
		return nil, fmt.Errorf("read what the provider refused: %w", err)
	}
	return f.logDeliveries(usageLimitStream, "usage-limits", cursors.Streams[usageLimitStream], len(exhaustions), skipped, cursors.Since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromUsageLimit(exhaustions[index])
			return exhaustions[index].At, notification, err
		})
}

// directiveDeliveries says what became of the directives somebody asked for in
// a thread, in the thread they asked in and addressed to them by name.
//
// It is the other half of the acknowledgment the inbound half posts. That one
// says what was recorded, which is a receipt rather than an answer: the person
// who typed it is waiting to hear what came of it, and until now the only place
// that was said was a terminal they are not at. So the record is read for the
// one thing it holds about an outcome — the directive having been settled, and
// what settled it — and that is said where they asked. It is also the moment
// their own message stops wearing the thinking face, which is why the reply that
// asked travels with the delivery.
//
// Both settlements are read here, and the commonest one by far is a directive
// that paused nothing being carried out. Only pausing kinds could be settled at
// all until the harness could record what came of an operational one, so a plain
// reply — which is what most replies are — reached this loop, was recorded,
// and then stood open forever with its thread never told what became of it.
//
// Only directives said into a thread are read, because only those have a thread
// to answer in and somebody to answer. One recorded at a terminal is not in the
// steer map, and reporting on it here would be this feed announcing every
// directive the product has ever had.
//
// A settlement the connection made itself is left alone. It has already been
// said in the thread, by the reply that made it, and the two halves post from
// different goroutines and cannot share a memory of what they have said.
//
// A withdrawal is read here as well, and it is the one thing said from this
// stream that is not a settlement. Withdrawing is deliberately not a
// disposition, so a thread-recorded directive the operator took back was never
// answered by the settlement reading: its reply wore the thinking face forever,
// in a thread that had been told the directive was heard and was never told it
// was taken back. It is said once, in the voice of whoever took it back, and it
// moves the mark exactly as a settlement does — there is an answer to read.
// Nothing the connection does withdraws a directive, so nothing here defers to
// it. A directive can be both carried out and later withdrawn, and each is said
// once under a mark of its own.
//
// What was settled before the watermark is history, exactly as it is on every
// stream that reads a record. The per-directive mark alone would not hold that
// line: the marks live in the cursors, the steer map does not, and the setup
// document tells an operator starting a channel over to delete one and keep the
// other — which without this would answer them, by name, for every directive
// they ever steered and settled. A flood of mentions about work that is long
// over is the same trust erosion as silence, from the other side.
//
// A directive record this build cannot read is read past with the rest of the
// stream, said once, and the stream is taken up where its cursor stands once the
// records read again: the stream advances by mark rather than by position, so
// nothing is skipped by waiting, and every other stream carries on meanwhile.
func (f *HarnessFeed) directiveDeliveries(cursors Cursors, streams map[string]struct{}, unread map[string]bool) ([]Delivery, error) {
	if f.Directives == nil || f.Steers == nil {
		return nil, nil
	}
	streams[directiveStream] = struct{}{}
	cursor := cursors.Streams[directiveStream]
	steers, err := f.Steers.LoadSteers()
	if err != nil {
		return nil, fmt.Errorf("read which directives were said in a thread: %w", err)
	}
	if len(steers.Steers) == 0 {
		// A product nobody has steered from a thread has nothing here to answer,
		// which is every product until somebody replies to one.
		return nil, nil
	}
	recorded, err := f.Directives.List()
	if err != nil {
		f.readPast(unread, "the directive records", err)
		return nil, nil
	}
	var deliveries []Delivery
	advanced := cursor
	// answer says one thing that became of a directive in the thread it was asked
	// in, under one mark, and reports whether it was said now. What was settled or
	// withdrawn before the watermark is read past on age rather than marked,
	// because a mark is what a cursor reset just threw away and this has to hold
	// without one.
	answer := func(directed directive.Directive, steer Steer, mark string, at time.Time, said func(notify.Topic) notify.Notification) {
		if predates(cursors.Since, at) || advanced.Has(mark) {
			return
		}
		topic, err := notify.ParseTopic(steer.Topic)
		if err != nil {
			// The thread it was said in cannot be addressed, so there is nowhere to
			// say this. It is said here once and read past rather than retried on
			// every pass for as long as the sink runs.
			f.say("directive %s is remembered against %q, which names no thread, so what became of it was not said there: %v", directed.ID, steer.Topic, err)
			advanced = advanced.With(mark)
			deliveries = append(deliveries, Delivery{Stream: directiveStream, Cursor: advanced})
			return
		}
		advanced = advanced.With(mark)
		deliveries = append(deliveries, Delivery{
			Stream:       directiveStream,
			Cursor:       advanced,
			Mention:      steer.Member,
			Reply:        steer.Message,
			Notification: said(topic),
		})
	}
	for _, directed := range recorded {
		steer, found := steers.Lookup(directed.ID)
		if !found {
			continue
		}
		if directed.Resolved() && !steer.Said {
			answer(directed, steer, outcomeMark+directed.ID, *directed.ResolvedAt, func(topic notify.Topic) notify.Notification {
				// How it was settled decides how it is said. A directive that paused
				// work is reported as resolved and a directive that paused nothing as
				// carried out, because the reader of the second one was never waiting
				// for work to resume — they were waiting to hear what came of what they
				// asked for.
				return acknowledged(topic, settledKind(directed), directed, *directed.ResolvedAt)
			})
		}
		if directed.Withdrawn() {
			answer(directed, steer, withdrawalMark+directed.ID, *directed.WithdrawnAt, func(topic notify.Topic) notify.Notification {
				return withdrawn(topic, directed)
			})
		}
	}
	return deliveries, nil
}

// runDeliveries says what one run's record crossed since the reading already
// reported, and advances that reading only once the whole crossing has been
// posted. A crash halfway therefore repeats what it had already said rather than
// losing what it had not, which is the trade the design takes deliberately: the
// durable record is authoritative and this is a view of it.
func (f *HarnessFeed) runDeliveries(state runstate.State, cursor Cursor, since time.Time, look readmodel.Look) ([]Delivery, error) {
	if cursor.Closed {
		return nil, nil
	}
	stream := runStream(state.RunID)
	var before runstate.State
	if cursor.Reported != nil {
		before = *cursor.Reported
	} else if len(cursor.Delivered) == 0 && predates(since, completion(state)) {
		// A run that was over before the watermark is history nobody turned
		// reporting on to read, so its cursor closes without a word. A run that
		// both started and finished while the sink was down is not that: it
		// finished after the watermark, so it is caught up on in full.
		//
		// A run this sink has already said something about is never history
		// whatever its dates say. Nothing backdates a completion today, so the
		// two cannot disagree, but if one ever did, a thread that stopped
		// mid-narrative is a worse answer than a run reported to its end.
		return []Delivery{{Stream: stream, Cursor: Cursor{Closed: true}}}, nil
	}

	crossed, err := notify.FromRun(before, state, look)
	if err != nil {
		// A run nothing can be addressed to is one nothing about it will ever be
		// postable to, whatever it goes on to do. Failing the pass would hold up
		// every other stream forever over one record, so it is said once and its
		// cursor closes.
		f.say("run %s could not be addressed and nothing will be reported about it: %v", state.RunID, err)
		return []Delivery{{Stream: stream, Cursor: Cursor{Closed: true}}}, nil
	}
	pending := make([]Delivery, 0, len(crossed))
	advanced := cursor
	for _, notification := range crossed {
		mark := markOf(notification.Event)
		if advanced.Has(mark) {
			continue
		}
		advanced = advanced.With(mark)
		pending = append(pending, Delivery{Stream: stream, Cursor: advanced, Notification: notification})
	}

	if len(pending) == 0 {
		// A run that is over and owes nothing has nothing left to cross, so the
		// reading it was being compared against is dropped rather than carried
		// for as long as the product exists.
		if settled(state) {
			return []Delivery{{Stream: stream, Cursor: Cursor{Closed: true}}}, nil
		}
		return nil, nil
	}
	// The last message of a crossing is what makes the crossing said, so it is
	// the one that moves the reading on. The marks it made redundant go with it:
	// they only ever existed to stop a crash repeating what was said against the
	// reading they were recorded under.
	settledState := state
	pending[len(pending)-1].Cursor = Cursor{Reported: &settledState}
	return pending, nil
}

// conversationDeliveries says what each of the product's conversations did to
// the backlog since the reading already reported. A conversation's log is an
// append-only log like the reports pile, so it advances by position: what it
// holds is mostly the turn itself — provider messages, tools, the reply as it
// was written — and the milestones are the few records among them where the
// queue actually moved. Everything else advances the position and says nothing.
//
// A conversation record or log this build cannot read is read past, for the
// reason an unreadable run is. A record that will not decode cannot say which
// conversation it was, so its stream cannot be kept by name and the batch is
// marked partial instead, which keeps every cursor.
func (f *HarnessFeed) conversationDeliveries(ctx context.Context, cursors Cursors, batch *Batch, unread map[string]bool) ([]Delivery, error) {
	if f.Conversations == nil {
		return nil, nil
	}
	conversations, unreadable, err := f.Conversations.RecordedReadable()
	if err != nil {
		return nil, fmt.Errorf("read the recorded conversations: %w", err)
	}
	for _, record := range unreadable {
		batch.Partial = true
		f.readPast(unread, "the conversation record "+record.Record, record.Err)
	}
	var deliveries []Delivery
	for _, conversation := range conversations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		stream := conversationStream(conversation.ConversationID)
		batch.Streams[stream] = struct{}{}
		events, skipped, err := f.Conversations.ScanEvents(conversation.ConversationID)
		if err != nil {
			f.readPast(unread, "the log of conversation "+conversation.ConversationID, err)
			continue
		}
		said, err := f.logDeliveries(stream, conversationLog(conversation), cursors.Streams[stream], len(events), skipped, cursors.Since,
			func(index int) (time.Time, notify.Notification, error) {
				notification, err := notify.FromConversation(conversation, events, index)
				return events[index].Timestamp, notification, err
			})
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, said...)
	}
	return deliveries, nil
}

// logDeliveries reads an append-only log from where the cursor left it. A record
// selection had nothing to say about is read past exactly as one that predates
// the watermark is: the position still moves, so a log that is mostly not
// milestones is not re-read from its beginning on every pass. What was filed
// before the watermark is read past in one silent advance, because a
// channel turned on today does not want a year of history arriving at once.
// Nothing after the watermark is ever read past on age: the watermark is a fixed
// moment rather than this process's start, so a record filed while the sink was
// down is still news when it comes back, which is the difference between an
// outage that delays messages and one that loses them.
//
// The position counts the log's lines rather than the records that decoded. A
// line the log's own reader could not decode — a torn write, or a schema this
// build does not know — holds a position exactly as a record does, so a cursor
// standing past it stays pointed at the same record whether or not the line is
// ever read; the records are handed to at by their own index, and the skipped
// lines, in file order, say which positions are theirs. Each one is said once,
// in the sink's own log and as one channel line, as the cursor crosses it, and
// then read past like anything else: one torn write costs one message rather
// than every message behind it for as long as the line stands. It is never read
// past on age, because a line that will not decode names no moment, and absence
// of a date is not evidence of age.
func (f *HarnessFeed) logDeliveries(stream, log string, cursor Cursor, records int, skipped []runstate.SkippedLine, since time.Time, at func(int) (time.Time, notify.Notification, error)) ([]Delivery, error) {
	var deliveries []Delivery
	readPast := cursor.Position
	// The skipped lines before the cursor were said on an earlier pass, and they
	// are counted off here so that a record's index is its position less the
	// skipped lines ahead of it.
	torn := 0
	for torn < len(skipped) && uint64(skipped[torn].Position) < cursor.Position {
		torn++
	}
	for index := int(cursor.Position); index < records+len(skipped); index++ {
		position := uint64(index + 1)
		if torn < len(skipped) && skipped[torn].Position == index {
			line := skipped[torn]
			torn++
			f.say("line %d of the %s log (byte %d) could not be decoded and was read past, keeping its position: %s", line.Line, stream, line.Offset, line.Problem)
			deliveries = append(deliveries, Delivery{
				Stream:       stream,
				Cursor:       Cursor{Position: position},
				Notification: notify.FromSkippedLine(log, line, f.now()),
			})
			continue
		}
		recordedAt, notification, err := at(index - torn)
		if err != nil {
			// One record nobody can address must not hold up every record behind
			// it for as long as the process runs, so it is said once and read
			// past rather than retried forever.
			f.say("a record on the %s log could not be addressed and was skipped: %v", stream, err)
			readPast = position
			continue
		}
		// A record that posts nowhere is read past exactly as one selection had
		// nothing to say about, and for the same reason: the position still has to
		// move. It is what keeps a watch log of a thousand polls from being one
		// cursor write per poll for the life of the sink.
		if !notification.Posts() || predates(since, recordedAt) {
			readPast = position
			continue
		}
		deliveries = append(deliveries, Delivery{
			Stream:       stream,
			Cursor:       Cursor{Position: position},
			Notification: notification,
		})
	}
	// Anything read past after the last message has to move the cursor on its
	// own, or it is read past again on every pass for as long as the sink runs.
	reached := cursor.Position
	if len(deliveries) > 0 {
		reached = deliveries[len(deliveries)-1].Cursor.Position
	}
	if readPast > reached {
		deliveries = append(deliveries, Delivery{Stream: stream, Cursor: Cursor{Position: readPast}})
	}
	return deliveries, nil
}

// holdDeliveries says the operator's two switches. Each is said when it is
// placed and again when it is lifted, and the lift is the awkward half: what
// says a hold has lifted is the hold's absence against a mark saying it was
// once there. The pair is forgotten once both have been said, so the product's
// cursor does not grow a line for every afternoon somebody was away.
//
// A hold the brake placed is the one hold the operator did not place, so its
// trip is said to him directly and tagged by member id as well as to the
// channel, once, the moment it is first read: it names the runs it counted
// with what stopped each, and the verb that lifts it. On 2026-09-19 the brake
// tripped at 17:56Z, the channel got a note nobody was reading, and the line
// stood for two hours. While the development manager and the harness work the
// hold it is not his to move, and the message's next-move clause says whose
// it is; he can still lift it sooner. The moment it becomes his — her
// escalation, or the harness's at the bound on its loop — is said to him
// directly once more, unless the trip was first read already escalated and
// said that in the same message. His own hold is said to the channel alone,
// because he placed it.
//
// A release names who lifted it where the store recorded one, which it does
// for every release made since releases were written down; the record is
// matched to the hold that was marked, so a release of some later hold is not
// read as the ending of this one.
func (f *HarnessFeed) holdDeliveries(cursor Cursor, read switches) []Delivery {
	var deliveries []Delivery
	advanced := cursor

	intake, held := read.intake, read.intakeHeld
	if held {
		saidDirectly := false
		if mark := intakeMark + stamp(intake.HeldAt); !advanced.Has(mark) {
			advanced = advanced.With(mark)
			saidDirectly = intake.HeldBy == runstate.IntakeHolderBrake
			deliveries = append(deliveries, Delivery{
				Stream:       productStream,
				Cursor:       advanced,
				Direct:       saidDirectly,
				Tag:          saidDirectly,
				Notification: notify.FromIntakeHold(intake),
			})
		}
		// The brake's hold handed to the operator by the development manager. The
		// trip was said to the channel when it happened, asking nobody for
		// anything; her escalation is the moment it became his, and it is said
		// to him directly, once, in the hold's own account of who decided it.
		if intake.Braked() && intake.Brake.Decision == runstate.BrakeDecisionEscalate && intake.Brake.DecidedAt != nil {
			if mark := brakeDecisionMark + stamp(*intake.Brake.DecidedAt); !advanced.Has(mark) {
				advanced = advanced.With(mark)
				// A hold first read already escalated was said to him directly
				// just above, in the same account; it is marked, not said twice.
				if saidDirectly {
					deliveries = append(deliveries, Delivery{Stream: productStream, Cursor: advanced})
				} else {
					deliveries = append(deliveries, Delivery{
						Stream:       productStream,
						Cursor:       advanced,
						Direct:       true,
						Tag:          true,
						Notification: notify.FromIntakeHold(intake),
					})
				}
			}
		}
		// The brake's hold handed to the operators by the harness, at the bound on
		// its summons-and-probe loop. It is said once, when the record first shows
		// it, and it is the one message about a brake hold that goes to them
		// directly and by name: the hold asked nobody for anything while the
		// harness was working it, and this is the moment it became theirs — after
		// a loop that on a broken machine would otherwise have gone round all
		// night, spending a turn and a run per cooldown, with nothing here getting
		// louder than the hourly note.
		if intake.Braked() && intake.Brake.EscalatedByHarness() {
			if mark := brakeEscalationMark + stamp(intake.Brake.Escalation.At); !advanced.Has(mark) {
				if notification, err := notify.FromIntakeEscalation(intake); err == nil {
					advanced = advanced.With(mark)
					deliveries = append(deliveries, Delivery{
						Stream:       productStream,
						Cursor:       advanced,
						Direct:       true,
						Tag:          true,
						Notification: notification,
					})
				}
			}
		}
	} else {
		// The escalation is forgotten with the hold, and said nowhere: the release
		// is what says the hold lifted, whichever way it was standing.
		// The two marks are named apart because the second reading shadows the
		// first's `said`: an escalation mark forgotten with no intake mark beside
		// it still has to move the reading on, and a branch testing the inner
		// answer would never run.
		escalated, escalationSaid := advanced.Marked(brakeEscalationMark)
		if escalationSaid {
			advanced = advanced.Without(escalated)
		}
		if decided, decisionSaid := advanced.Marked(brakeDecisionMark); decisionSaid {
			advanced = advanced.Without(decided)
			escalationSaid = true
		}
		if mark, said := advanced.Marked(intakeMark); said {
			advanced = advanced.Without(mark)
			release, recorded := f.releaseOf(strings.TrimPrefix(mark, intakeMark))
			deliveries = append(deliveries, Delivery{
				Stream:       productStream,
				Cursor:       advanced,
				Notification: notify.IntakeReleased(f.now(), release, recorded),
			})
		} else if escalationSaid {
			deliveries = append(deliveries, Delivery{Stream: productStream, Cursor: advanced})
		}
	}

	operator, held := read.operator, read.operatorHeld
	if held {
		if mark := holdMark + stamp(operator.HeldAt); !advanced.Has(mark) {
			advanced = advanced.With(mark)
			deliveries = append(deliveries, Delivery{
				Stream:       productStream,
				Cursor:       advanced,
				Notification: notify.FromOperatorHold(operator),
			})
		}
	} else if mark, said := advanced.Marked(holdMark); said {
		advanced = advanced.Without(mark)
		deliveries = append(deliveries, Delivery{
			Stream:       productStream,
			Cursor:       advanced,
			Notification: notify.HoldLifted(f.now()),
		})
	}
	return deliveries
}

// releaseOf is the recorded release of the hold placed at one moment, or
// nothing: a release the store never recorded, a record that cannot be read,
// or a record of some other hold's release. A record that cannot be read costs
// the message the name and not the message, and is said in the sink's own log.
func (f *HarnessFeed) releaseOf(heldAt string) (runstate.IntakeRelease, bool) {
	if f.Intake == nil {
		return runstate.IntakeRelease{}, false
	}
	release, recorded, err := f.Intake.LastRelease()
	if err != nil {
		f.say("who released the hold on intake could not be read, so the release is said without a name: %v", err)
		return runstate.IntakeRelease{}, false
	}
	if !recorded || stamp(release.Hold.HeldAt) != heldAt {
		return runstate.IntakeRelease{}, false
	}
	return release, true
}

// operatorActionDeliveries says each finding that needs the operator's own
// hand, once, to him directly and tagged by member id.
//
// The findings are the read model's derivation over the pile and what became
// of it, so what this says to him and what `yoyo status` names under what
// needs a human are one list. Each is marked by its key once said and is never
// said again while it stands: the status line carries it, and a message
// repeated about something he has been told is the nagging that gets a channel
// muted. A finding that ends drops its mark, so the cursor holds only what is
// standing.
//
// A finding from before the watermark is marked and not said, as every other
// record filed before the channel was turned on is: what the handling of a
// month-old report says today is said today, because the handling is today's,
// and the finding's moment is the record that made it.
//
// A stopped run the development manager escalated to the operator is read from
// the same reading of the runs the crossings were selected from, and from what
// triage decided about them. This surface cannot cheaply ask the tracker which
// items are still admitted or parked, nor the repository whether the run's
// change is still there, so it reads as ended only the escalations the
// reconcile sweep has recorded as ended on the run, and every other one as
// standing: what that costs is a mark kept until the next sweep for an item
// that was parked or left the backlog, and what it buys is a finding said once
// rather than never.
//
// The batch an owning role argued on a recurring pass is a finding too, one per
// pass, read from the amendment log and the passes' own reports: it is the one
// decision list the operator answers with `yoyo amendment`, so it reaches him
// here, once, rather than only on the status line.
func (f *HarnessFeed) operatorActionDeliveries(cursor Cursor, filed []report.Report, states []runstate.State, proposals []amendment.Record, since time.Time, streams map[string]struct{}) ([]Delivery, error) {
	streams[operatorActionStream] = struct{}{}
	handlings, err := f.Reports.Handlings()
	if err != nil {
		return nil, fmt.Errorf("read what became of the collected reports: %w", err)
	}
	actions := readmodel.OperatorActions(filed, handlings)
	passFailuresRead := true
	if f.Runs != nil {
		passActions, problem := readmodel.PassFailureOperatorActions(readmodel.Sources{Passes: f.Runs.Sweeps(), Reports: f.Reports})
		if problem != "" {
			// An unreadable log does not establish that a finding ended. Keep
			// its mark while continuing to deliver unrelated findings.
			f.say("product pass findings for the operator could not be read and were not said this pass: %s", problem)
			passFailuresRead = false
		}
		actions = append(actions, passActions...)
	}
	escalated, problem := readmodel.EscalatedOperatorActions(states, f.Decisions, nil, nil)
	if problem != "" {
		// A triage record that cannot be read costs that item's finding this
		// pass and is said here; the pass carries on, and the finding is read
		// again next time.
		f.say("an escalated stoppage could not be read and was not said this pass: %s", problem)
	}
	actions = append(actions, escalated...)
	batches, batchesRead := f.amendmentBatches(proposals)
	actions = append(actions, batches...)
	standing := make(map[string]struct{}, len(actions))
	advanced := cursor
	var deliveries []Delivery
	for _, action := range actions {
		mark := findingMark + action.Key
		standing[mark] = struct{}{}
		if advanced.Has(mark) {
			continue
		}
		advanced = advanced.With(mark)
		if predates(since, action.Since) {
			deliveries = append(deliveries, Delivery{Stream: operatorActionStream, Cursor: advanced})
			continue
		}
		notification, err := notify.FromOperatorAction(notify.OperatorAction{
			WorkItemID: action.WorkItemID,
			RunID:      action.RunID,
			Needs:      action.Needs,
			RecordedIn: action.RecordedIn,
			FoundBy:    action.FoundBy,
			Ends:       action.Ends,
			Mover:      action.Whose(),
			Since:      action.Since,
		})
		if err != nil {
			// A finding nothing can be addressed to is said here once and read
			// past, for the reason every other unaddressable record is: one record
			// must not hold up every finding behind it for as long as the sink runs.
			f.say("a finding for the operator could not be addressed and was not said: %v", err)
			deliveries = append(deliveries, Delivery{Stream: operatorActionStream, Cursor: advanced})
			continue
		}
		deliveries = append(deliveries, Delivery{
			Stream:       operatorActionStream,
			Cursor:       advanced,
			Direct:       true,
			Tag:          true,
			Notification: notification,
		})
	}
	// Marks for findings no longer standing are dropped, in one silent advance,
	// so the cursor holds what is standing and a finding made again of the same
	// report is said again.
	// The ended marks are collected before any is dropped: Without builds the
	// next cursor from this one's marks, and dropping while ranging over them
	// would read a list the drop had already changed.
	var ended []string
	for _, mark := range advanced.Delivered {
		if _, still := standing[mark]; !still && strings.HasPrefix(mark, findingMark) {
			// A batch whose pass could not be read this time is not known to have
			// ended, so its mark is kept rather than dropped and said again later.
			if !batchesRead && strings.HasPrefix(mark, findingMark+readmodel.AmendmentBatchKeyPrefix) {
				continue
			}
			if !passFailuresRead && strings.HasPrefix(mark, findingMark+readmodel.PassFailureKeyPrefix) {
				continue
			}
			ended = append(ended, mark)
		}
	}
	for _, mark := range ended {
		advanced = advanced.Without(mark)
	}
	if len(ended) > 0 {
		deliveries = append(deliveries, Delivery{Stream: operatorActionStream, Cursor: advanced})
	}
	return deliveries, nil
}

// amendmentBatches is the owners' argued batches as findings, and whether the
// passes were read whole. A sweep log that could not be read to the end yields
// what it did read, and is said here rather than failing the poll: a finding a
// pass could not be read for is said on a later poll.
func (f *HarnessFeed) amendmentBatches(proposals []amendment.Record) ([]readmodel.OperatorAction, bool) {
	if f.Runs == nil {
		return nil, true
	}
	sweeps, _, err := f.Runs.Sweeps().List()
	if err != nil {
		f.say("the recurring passes could not be read to the end, so an owner's batch of recommendations may not be said this pass: %v", err)
	}
	return readmodel.AmendmentBatchActions(readmodel.RecommendedAmendments(sweeps, proposals)), err == nil
}

func (f *HarnessFeed) say(format string, args ...any) {
	if f.Log != nil {
		f.Log(format, args...)
	}
}

func (f *HarnessFeed) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now().UTC()
}

// predates reports what the product recorded before the watermark. A record with
// no moment on it is never history, because absence of a date is not evidence of
// age; nor is anything at all when there is no watermark, which is what a caller
// asking for the whole record leaves behind.
func predates(since, at time.Time) bool {
	return !since.IsZero() && !at.IsZero() && at.Before(since)
}

// markOf names one crossing so that having posted it survives a crash. The kind
// alone would not do it: a check that fails, is repaired, and fails differently
// has crossed the same kind twice with two different things to say, and a mark
// that could not tell those apart would swallow the second. The moment is left
// out because every crossing of one reading carries the same one, and a mark
// that moved with it would match nothing.
func markOf(event notify.Event) string {
	digest := fnv.New64a()
	fmt.Fprintf(digest, "%v\x00%v\x00%s", event.Severity, event.Detail, event.Text)
	return string(event.Kind) + ":" + strconv.FormatUint(digest.Sum64(), 36)
}

// settled reports a run that is over and owes nothing further: terminal, with no
// publication still to be watched and no integration left half done.
func settled(state runstate.State) bool {
	return state.Status.Terminal() && !state.Outstanding()
}

// completion is when a run stopped, for deciding whether it is history. A run
// that has not stopped has no completion and is never history.
func completion(state runstate.State) time.Time {
	if state.CompletedAt == nil {
		return time.Time{}
	}
	return *state.CompletedAt
}

func stamp(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }
