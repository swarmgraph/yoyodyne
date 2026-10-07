package runstate

// What a watch session is doing, recorded where somebody who is not at its
// terminal can read it.
//
// A session that stays open until it is told to stop has one failure mode
// nothing before it had: it goes quiet, and quiet is what both a healthy idle
// session and a dead one look like. Nothing else in the harness answers that.
// A run's record says what became of a run, and a session that is choosing
// nothing has no run to say it with; the intake hold says the operator stopped
// the choosing, and says nothing about whether anything is left to obey it.
//
// So the session says it itself, in the same shape everything else here is
// said: an append-only log per product, one entry per transition rather than
// one per poll. A session idling overnight writes one line, not one a minute,
// which is what makes the log readable and what makes the absence of a line
// mean something. The states are few on purpose — choosing, idle, braked,
// resumed, and stopped — because each one is a different thing for an operator
// to do, and a state nobody would act on differently is noise in a log whose
// value is that it is short.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

const (
	// watchLeaseFile is the lock one watching session holds for as long as it
	// watches, and watchHolderFile is that session saying which one it is. They
	// sit beside the log rather than among the runs because what is singular is
	// the session, not any run it starts.
	watchLeaseFile  = ".watch.lock"
	watchHolderFile = ".watch.holder"
)

// WatchSchemaVersion is 1 and has never changed.
const WatchSchemaVersion = 1

// MaxWatchReasonBytes bounds what one transition says about itself. It is the
// bound a hold's reason takes, for the same reason: a line somebody reads in the
// morning is only useful if it says what it was about, and a bounded line says
// it.
const MaxWatchReasonBytes = 4 << 10

// MaxPassedOverReasonBytes bounds what one passed-over item says about itself
// beyond the class it was passed over in. It is far below the bound a hold's
// reason takes, because these are read several to a line rather than one at a
// time: a class naming five items with a paragraph against each is a line nobody
// reads, and the whole point of the naming is that it is read at a glance.
const MaxPassedOverReasonBytes = 256

// maxEncodedWatchBytes bounds one encoded transition, including the trailing
// newline. The writer and the reader share it, so a transition that was written
// is always one that can be read back.
const maxEncodedWatchBytes = 16 << 10

var watchSessionIDPattern = regexp.MustCompile(`^watch-[a-f0-9]{32}$`)

// WatchState is what a session is doing between one transition and the next.
type WatchState string

const (
	// WatchWatching is a session choosing work: it has read the queue and is
	// starting what the configuration leaves room for.
	WatchWatching WatchState = "watching"
	// WatchIdle is a session that started nothing at a poll and is polling again.
	// It is the ordinary state of a drained queue and the one that most needs
	// saying out loud, because it is indistinguishable from a dead process
	// otherwise — and it is also a queue with plenty in it that this session
	// cannot start, which is why the reason says what was passed over and the two
	// fields below say what was nonetheless going and who has to act.
	WatchIdle WatchState = "idle"
	// WatchBraked is a session choosing nothing because intake is held —
	// whether the operator held it or the session's own failure-storm brake did.
	// Which of the two is in the reason, because they need different things from
	// whoever reads it.
	WatchBraked WatchState = "braked"
	// WatchBlocked is a session that would choose work and cannot start any: the
	// machine itself refuses every run, for something like uncommitted changes
	// sitting in the primary checkout. It is its own state rather than idle
	// because the two are opposite facts about the same silence — idle is a queue
	// with nothing in it and is waiting on the product manager, blocked is a queue
	// with work in it and is waiting on whoever can put the machine right. Told
	// apart only by a session's own terminal, the second reads as the first, which
	// is a stall diagnosed by hand three times before this state existed.
	WatchBlocked WatchState = "blocked"
	// WatchResumed is a session choosing again after a brake lifted. It is its
	// own state rather than a second "watching" so that the lift is legible: a
	// hold that was placed and never lifted reads as one that is still in force.
	WatchResumed WatchState = "resumed"
	// WatchStopped is the session ending, and it is recorded whichever way it
	// ended. A session that stops is not news the way a session that dies is,
	// but a log that only ever said the two loudly enough to tell apart while
	// the process lived would leave the reader guessing afterwards.
	WatchStopped WatchState = "stopped"
)

func (s WatchState) Valid() bool {
	switch s {
	case WatchWatching, WatchIdle, WatchBraked, WatchBlocked, WatchResumed, WatchStopped:
		return true
	default:
		return false
	}
}

// PassedOverClass is one reason a poll left an item where it was. The set is
// closed, and every place a pull passes an item over names one of them: a class
// nobody named is an item that disappears into a count saying something else
// about it.
//
// It is recorded rather than only rendered into the reason above, and that is
// what makes the accounting readable by something other than the session that
// wrote it. On 2026-09-06 the stall alarm paged that nothing had started for an
// hour with nothing accounting for it, while the poll's own account of the same
// queue — a third of it waiting on triage decisions — sat one surface over as
// prose nothing could take apart. A surface that has to parse a sentence to
// learn the cause derives its own instead, which is two readings of one machine.
type PassedOverClass string

const (
	PassedOverCarriedInConversation PassedOverClass = "carried in conversation"
	PassedOverParked                PassedOverClass = "parked"
	// PassedOverHeldForAPerson is the class no pull records any more. It said two
	// states at once — a stoppage nobody has decided about, and a decision nobody
	// has carried out — and on 2026-09-07 that cost days of the operator's
	// attention on a development manager who had decided every one of them. The
	// two below replaced it.
	//
	// It stays in the taxonomy because the log is append-only and validated on
	// every read: a class this no longer recognized would make every log holding
	// one unreadable, permanently, which is the same reason Restarting below is a
	// field beside a state rather than a state of its own.
	PassedOverHeldForAPerson PassedOverClass = "held for a person"
	// PassedOverAwaitingDecision is a stoppage the development manager has still
	// to decide about, and PassedOverAwaitingCarryOut one she has decided and the
	// harness has still to act on. They are separate classes because they have
	// separate next movers, and naming them apart is the whole of what tells an
	// operator whether the gap is a decision or its execution.
	PassedOverAwaitingDecision PassedOverClass = "awaiting a decision"
	PassedOverAwaitingCarryOut PassedOverClass = "awaiting carry-out of a decision"
	// PassedOverWaitingOnAPerson is an item holding a step only a person can take
	// and nobody has recorded taking. It is its own class rather than one of the
	// two above because its next mover is neither the development manager nor the
	// harness: nothing machinery does passes it, an item's closure included, and
	// the act that does is the operator's own.
	PassedOverWaitingOnAPerson    PassedOverClass = "waiting on a person"
	PassedOverWaitingOnOtherWork  PassedOverClass = "waiting on other work"
	PassedOverAlreadyTried        PassedOverClass = "already tried this session"
	PassedOverAlreadyInFlight     PassedOverClass = "already in flight"
	PassedOverCoveredByChildren   PassedOverClass = "covered by its children"
	PassedOverPausedByDirective   PassedOverClass = "paused by a directive"
	PassedOverSequencedBehindWork PassedOverClass = "sequenced behind work in flight"
	PassedOverPrerequisiteUnmet   PassedOverClass = "the tree does not meet what it asks for"
	// PassedOverLeftForAnotherSlot is an item the only free developer slots
	// passed over for their preferred label: each of them pulled work carrying
	// the label it prefers ahead of this item, and no slot with no preference was
	// free to take it. It is not a wait on anything about the item — a slot with
	// no preference takes it in the product manager's order, and a preferring
	// slot falls back to it once its label's work is exhausted — which is why it
	// is named apart from a deferral.
	PassedOverLeftForAnotherSlot PassedOverClass = "left for another developer slot"
	// PassedOverWaitingOnUsageWindow is an item whose run this session started was
	// stopped by the provider's usage window, with a reset later than the harness
	// will wait. It is not an item already tried: nothing about it failed, and the
	// session pulls it again by itself the moment the window resets. It is held
	// until then only because a run started into a window still closed is refused
	// the same way again.
	PassedOverWaitingOnUsageWindow PassedOverClass = "waiting on the provider's usage window"
)

// PassedOverClasses is the whole taxonomy, in the order a pull meets them. A
// caller that has to cover every class reads it from here rather than repeating
// the list.
func PassedOverClasses() []PassedOverClass {
	return []PassedOverClass{
		PassedOverCarriedInConversation,
		PassedOverParked,
		PassedOverHeldForAPerson,
		PassedOverAwaitingDecision,
		PassedOverAwaitingCarryOut,
		PassedOverWaitingOnAPerson,
		PassedOverWaitingOnOtherWork,
		PassedOverAlreadyTried,
		PassedOverAlreadyInFlight,
		PassedOverCoveredByChildren,
		PassedOverPausedByDirective,
		PassedOverSequencedBehindWork,
		PassedOverPrerequisiteUnmet,
		PassedOverLeftForAnotherSlot,
		PassedOverWaitingOnUsageWindow,
	}
}

func (c PassedOverClass) Valid() bool {
	for _, known := range PassedOverClasses() {
		if c == known {
			return true
		}
	}
	return false
}

// PassedOverGroup is one class of what a poll passed over: how many items fell
// into it, which of them it named, and the conversation that carries them where
// the class names one.
//
// The count is exact and the naming is bounded, which is the division every
// listing here makes: a backlog of forty deferred items is forty in the count
// and five in the names, because a line whose job is to be read at a glance is
// not a list of identifiers.
type PassedOverGroup struct {
	Class PassedOverClass  `json:"class"`
	Role  domain.AgentRole `json:"role,omitempty"`
	Count int              `json:"count"`
	Items []string         `json:"items,omitempty"`
	// Reasons is why each named item above is in this class, positionally, and
	// empty where the class says the whole of it on its own — which is every class
	// but the one below.
	//
	// "Already tried this session" is the class that does not. It is the only one
	// whose cause is a thing that already happened rather than a state anybody can
	// go and read: the item is exactly as ready as it was, nothing is recorded
	// against it, and what excluded it is an attempt this session made and remembers
	// privately. An exclusion carrying no reason is what that looks like from
	// outside, and on 2026-09-13 it is what left two items excluded for the rest of
	// a session with no surface saying why — over a queue of seventy-four.
	Reasons []string `json:"reasons,omitempty"`
}

// PassedOver is one poll's whole account of the items it left where they were,
// against the size of the queue it read them from.
//
// The queue's own size travels with the groups so that a fraction said about
// them is a fraction of one reading. A surface that took the count from here and
// the total from its own tracker read would be saying "33 of 47" about two
// different moments.
type PassedOver struct {
	Admitted int               `json:"admitted,omitempty"`
	Groups   []PassedOverGroup `json:"groups,omitempty"`
}

// Passed is how many items the account covers, which is the sum of what each
// class holds rather than how many classes there are.
func (p PassedOver) Passed() int {
	passed := 0
	for _, group := range p.Groups {
		passed += group.Count
	}
	return passed
}

// WatchTransition is one moment a session changed what it was doing, and why.
// The reason is prose because what an operator does about a braked session
// depends entirely on what braked it.
type WatchTransition struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// SessionID names the session rather than the process, so two sessions
	// interleaved in one log can still be read apart.
	SessionID string     `json:"session_id"`
	State     WatchState `json:"state"`
	At        time.Time  `json:"at"`
	Reason    string     `json:"reason,omitempty"`
	// Build is the repository revision the session's binary was built from. It is
	// here rather than only on the transition that opened the session because a
	// reader arriving in the middle of a night reads the entry the session
	// happened to write last, and a field only the first entry carried would be a
	// field that answers nothing for the sessions that most need it answered.
	//
	// A session that stays open runs whatever it was started with, so a fix that
	// landed after it started is not in it until somebody restarts it — which is
	// invisible from every other thing the record says, because the session goes
	// on choosing work and the runs it starts go on looking ordinary. This is what
	// makes that measurable.
	//
	// It is empty where the binary recorded no revision, which is a comparison
	// nobody can make rather than a session that is current.
	Build string `json:"build,omitempty"`
	// Running is how many developer runs the session could see in flight when it
	// recorded this. It is on the transition because a session idle on one slot
	// while a run works on the other is the state that was read as the whole line
	// having stopped: the reason says what was passed over, and this says that the
	// harness is nonetheless moving. Zero is a session with nothing going, which is
	// the ordinary idle.
	Running int `json:"running,omitempty"`
	// Mover is whose move a braked poll is, in the words of the hold's own
	// record, where the hold carries one. It travels for the reason the executor
	// does: the clause a channel closes a braked message on used to name the
	// operator whatever held the line, and a hold the brake placed is the
	// development manager's or the harness's until she escalates it. It is
	// empty on every other transition, and on a braked one over the operator's
	// own hold, which is theirs as it always was.
	Mover string `json:"mover,omitempty"`
	// Executor is the conversation that carries the work this session passed over,
	// where the work it passed over is carried by one. It is the marker an item
	// itself is marked with, so the role named here is the role the tracker names
	// rather than one anything derived, and it is what decides whose move follows
	// an idle poll: a queue whose only unstarted work is an architect's to carry is
	// waiting on the architect, and telling the reader it waits on an admission
	// sends them to the one person who can do nothing about it.
	Executor domain.WorkItemExecutor `json:"executor,omitempty"`
	// PassedOver is what the poll left where it was, in classes rather than in
	// prose. The reason above says the same account in words for whoever is
	// reading the log; this is the same account for whatever has to answer a
	// question about it — which is how the stall alarm names the cause instead of
	// deriving its own ignorance beside a session that had already worked it out.
	//
	// It is empty on every transition but an idle one, and empty on an idle poll
	// that passed nothing over, which is a session watching a queue it emptied.
	PassedOver PassedOver `json:"passed_over,omitzero"`
	// Unreadable marks the poll that chose nothing because the harness could not
	// be read at all, which is the third state whose next move is nobody's to
	// make: a store that will not answer is not waiting on an admission, a
	// release, or a conversation, and it is read again until it answers or the
	// session gives up on it. Every other transition leaves it false.
	Unreadable bool `json:"unreadable,omitempty"`
	// ProviderWindow marks the poll that chose nothing because the provider is
	// refusing the harness for want of capacity, which is the fourth state whose
	// next move is nobody's to make. It travels for the reason the three above do:
	// nothing a person admits, releases, or opens shortens a usage window, so a
	// reader told to admit work would be told the one thing that cannot help.
	//
	// It is the fact that was missing on 2026-09-05. A session waited out a window
	// from 12:13Z to 13:43Z and recorded nothing about it, so every surface read
	// the ninety minutes as a queue nobody was pulling and one of them woke the
	// operator over it — while the pause was the whole of the accounting. Every
	// other transition leaves it false.
	//
	// It is a field beside the idle state rather than a state of its own, for the
	// reason Restarting below is: a state nothing recognizes fails this log's
	// validation permanently, and an unknown field is ignored.
	ProviderWindow bool `json:"provider_window,omitempty"`
	// ProviderWindowResetsAt is when the provider said that window lifts. It is
	// absent where the provider named no time, which is a different fact from a
	// wait of unknown length: the harness asks again rather than being told when.
	// It is what lets a surface say "until 13:43Z" rather than only "waiting".
	ProviderWindowResetsAt *time.Time `json:"provider_window_resets_at,omitempty"`
	// Restarting marks the one stop that is not an ending: the session is being
	// re-executed into a build deployed over it, having waited out every run it
	// started, and the process comes straight back watching the same queue. Every
	// other transition leaves it false.
	//
	// It is a field beside the state rather than a state of its own, and that is
	// deliberate. A state nothing recognizes fails this log's validation, so a
	// reader from before the field existed — a Slack sink or a `yoyo status` from
	// an older build, which is exactly what is running while a redeploy is
	// happening — would stop being able to read the log at all, permanently,
	// because the entry stays in it. An unknown field is ignored by the same
	// reader, so what an older one loses is the distinction and not the log.
	Restarting bool `json:"restarting,omitempty"`
	// DispatchWait is a dispatch this session started waiting out a tracker failure
	// before it has claimed anything, recorded as the wait is taken. It is the one
	// entry here that is not a transition of the session at all: the session is
	// exactly as it was, and what the entry says is that one of the dispatches it
	// started is holding a developer slot with no run record to say so on.
	//
	// It is the fact that was missing after yoyodyne-ifd.428.6. The tracker read a
	// dispatch makes before it claims an item is waited out for up to the recovery
	// window, and nothing was written while it waited, so a slot held for two hours
	// that way looked from every surface exactly like a hung process.
	//
	// An entry carrying one is a note rather than a transition, and every fold of
	// this log into what a session is doing reads past it; see Note. It is written
	// as a watching entry because that is what an older reader, which knows nothing
	// of the field, will take it for — and a session with a dispatch in flight is
	// one choosing work.
	DispatchWait *DispatchWait `json:"dispatch_wait,omitempty"`
	// WorktreeCrossing is a Git command a dispatch this session started ran again
	// because it crossed another worktree's creation or removal — a `git worktree
	// add` meeting a neighbour's cleanup, most often. Like a dispatch wait it is a
	// note about one dispatch rather than a transition of the session, and is
	// written as a watching entry for the same reason.
	//
	// It is here because the re-run is otherwise invisible: a crossing absorbed on
	// the second attempt leaves nothing behind, so a repository whose runs cross
	// each other more and more looks exactly like one where they never do, until
	// the day one takes more attempts than it is given and a run fails over a
	// neighbour's cleanup (yoyodyne-ifd.429.33).
	WorktreeCrossing *WorktreeCrossing `json:"worktree_crossing,omitempty"`
	// RecurringPass is a recurring pass this session has begun, recorded as the
	// pass starts. A session whose schedule can fire beside its poll takes its
	// passes there and goes on pulling (see WatchPass.Beside); one whose schedule
	// cannot fires them inside its poll, one at a time, and from here until its
	// next line pulls nothing: on 2026-09-29 one pass that
	// spanned the machine's sleep held the poll for three and a half hours, and
	// the log said nothing at all from its last line before the pass until the
	// morning (docs/diagnoses/yoyodyne-ifd-433-20-tracker-listing-timeouts.md).
	// This is the line that says which pass the session is in, and since when.
	//
	// It is a note rather than a transition, for the reason a dispatch wait is:
	// the session's state has not changed — it is doing what its last line said,
	// and a surface that took the pass for the session's latest word would read a
	// session idle over an empty queue as one choosing work. It is written as a
	// watching entry for the same reason too.
	RecurringPass *WatchPass `json:"recurring_pass,omitempty"`

	// Draining marks a session that has found a build deployed over it and is
	// waiting out the runs it hosts before it restarts — bounded, so that the wait
	// is minutes and never the length of a check suite under load. It travels on
	// every transition the session makes while it drains, whatever state that
	// transition is, because draining is about the runs the session hosts and
	// not about the scheduler's other duties: a draining session still polls,
	// still pulls into free seats, and still fires its recurring tasks, and a
	// reader of any of those lines is owed the drain and its bound beside them.
	//
	// It is a field beside the state rather than a state of its own, for the
	// reason Restarting above is: a reader from before the field existed —
	// which is exactly what is running while a redeploy is happening — ignores an
	// unknown field and refuses an unknown state, permanently.
	Draining *WatchDrain `json:"draining,omitempty"`
}

// RetryingRead reports a poll that chose nothing because the harness's store
// could not be read, and that is being read again. It is the one condition every
// surface recognizes a retried read on — the status line, the stall reason, and
// the channel's kind — so the three cannot disagree about one transition. Only
// an idle poll is a read being retried: a session that gave up on the store
// records a stop, and a stop said as a retry is a session nobody starts again.
func (t WatchTransition) RetryingRead() bool {
	return t.Unreadable && t.State == WatchIdle
}

// Note reports an entry that says something about a dispatch this session
// started rather than about the session itself, which is what every fold of the
// log into a session's state reads past. A pre-claim wait is written from the
// dispatch's own goroutine while the session goes on polling, so taking one as
// the session's latest word would report a session idle over an empty queue as
// one still choosing, for as long as nothing else it did was news.
func (t WatchTransition) Note() bool {
	return t.DispatchWait != nil || t.WorktreeCrossing != nil || t.RecurringPass != nil
}

// WatchPass is one recurring pass a watch session has begun: which task or
// program manager instance, the role it wakes, what triggered it, and when it
// began.
type WatchPass struct {
	Task    string           `json:"task"`
	Role    domain.AgentRole `json:"role,omitempty"`
	Trigger PassTrigger      `json:"trigger,omitempty"`
	At      time.Time        `json:"at"`
	// Beside marks a pass taken beside the session's poll rather than inside
	// it, which is how a session takes every pass of a schedule that can fire
	// that way: the poll goes on pulling while it runs. A pass without it was
	// taken inside the poll, which pulls nothing until it ends.
	Beside bool `json:"beside,omitempty"`
}

// Says is the pass in the words the watch log's reason carries.
func (p WatchPass) Says() string {
	said := fmt.Sprintf("taking the recurring pass of %s since %s", p.Task, p.At.UTC().Format(time.RFC3339))
	if p.Trigger != "" {
		said += fmt.Sprintf(", fired by its %s", p.Trigger)
	}
	if p.Beside {
		return said + "; it is taken beside the session's poll, which goes on pulling while it runs"
	}
	return said + "; the session fires its passes inside its poll, so it pulls nothing more until this pass ends"
}

func (p WatchPass) validate() error {
	var problems []error
	if strings.TrimSpace(p.Task) == "" {
		problems = append(problems, errors.New("a recurring pass note names the task or instance it is a pass of"))
	}
	if p.At.IsZero() {
		problems = append(problems, errors.New("a recurring pass note records when the pass began"))
	}
	return errors.Join(problems...)
}

// WorktreeCrossing is one Git command a dispatch ran again over another
// worktree's creation or removal: which item the dispatch was for, which command,
// which attempt Git refused out of how many it is given, and Git's own words.
type WorktreeCrossing struct {
	WorkItemID string    `json:"work_item_id"`
	Command    string    `json:"command"`
	Attempt    int       `json:"attempt"`
	Attempts   int       `json:"attempts"`
	At         time.Time `json:"at"`
	Refusal    string    `json:"refusal,omitempty"`
}

// Says is the crossing in the words the watch log's reason carries.
func (c WorktreeCrossing) Says() string {
	return fmt.Sprintf("the dispatch for %s ran `%s` again after attempt %d of %d crossed another worktree's creation or removal: %s",
		c.WorkItemID, c.Command, c.Attempt, c.Attempts, c.Refusal)
}

func (c WorktreeCrossing) validate() error {
	var problems []error
	if err := domain.ValidateIdentifier("worktree crossing work item id", c.WorkItemID); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(c.Command) == "" {
		problems = append(problems, errors.New("a worktree crossing names the Git command that was run again"))
	}
	if c.Attempt < 1 || c.Attempts < c.Attempt {
		problems = append(problems, fmt.Errorf("a worktree crossing on attempt %d of %d is not an attempt", c.Attempt, c.Attempts))
	}
	if c.At.IsZero() {
		problems = append(problems, errors.New("a worktree crossing records when it was run again"))
	}
	if len(c.Refusal) > MaxWatchReasonBytes {
		problems = append(problems, fmt.Errorf("a worktree crossing's refusal is %d bytes, which exceeds the %d byte bound", len(c.Refusal), MaxWatchReasonBytes))
	}
	return errors.Join(problems...)
}

// DispatchWait is one wait a dispatch took out on a tracker failure before it
// claimed anything: which item it was dispatched for, which boundary failed,
// which retry this is, how long it waits, and the failure it is waiting out. It
// is the shape a run's own Retry takes, counted the same way, because it is the
// same wait on the same rule, recorded where a dispatch with no run can record
// it.
type DispatchWait struct {
	WorkItemID   string    `json:"work_item_id"`
	Boundary     string    `json:"boundary"`
	Attempt      int       `json:"attempt"`
	DelaySeconds int64     `json:"delay_seconds"`
	At           time.Time `json:"at"`
	Failure      string    `json:"failure,omitempty"`
}

// Until is when the wait ends and the dispatch asks the tracker again.
func (w DispatchWait) Until() time.Time {
	return w.At.Add(time.Duration(w.DelaySeconds) * time.Second)
}

func (w DispatchWait) validate() error {
	var problems []error
	if err := domain.ValidateIdentifier("dispatch wait work item id", w.WorkItemID); err != nil {
		problems = append(problems, err)
	}
	if w.Boundary == "" {
		problems = append(problems, errors.New("a dispatch wait names the boundary it is waiting out"))
	}
	if w.Attempt < 1 {
		problems = append(problems, fmt.Errorf("a dispatch wait is retry %d, which is not a retry", w.Attempt))
	}
	if w.DelaySeconds < 0 {
		problems = append(problems, fmt.Errorf("a dispatch wait of %d seconds is not a wait", w.DelaySeconds))
	}
	if w.At.IsZero() {
		problems = append(problems, errors.New("a dispatch wait records when it was taken"))
	}
	if len(w.Failure) > MaxWatchReasonBytes {
		problems = append(problems, fmt.Errorf("a dispatch wait's failure is %d bytes, which exceeds the %d byte bound", len(w.Failure), MaxWatchReasonBytes))
	}
	return errors.Join(problems...)
}

// WatchDrain is a session waiting out the runs it hosts to restart into a build
// deployed over it: since when, how long it will wait, and the moment it stops
// waiting and restarts anyway.
type WatchDrain struct {
	Since time.Time `json:"since"`
	// BoundSeconds is execution.redeploy_drain_limit as the session read it, and
	// Until is Since plus that bound: the moment past which the session restarts
	// with its hosted runs stopped and preserved rather than waited out.
	BoundSeconds int64     `json:"bound_seconds"`
	Until        time.Time `json:"until"`
	// Hosting is how many of the session's own runs it was waiting out when it
	// recorded this. Zero is a session on the point of restarting.
	Hosting int `json:"hosting,omitempty"`
	// BoundReached marks the drain having run out: the session has stopped and
	// preserved the runs it hosts, pulls nothing more into a free seat, and
	// restarts as soon as it hosts nothing — which, for a run at its promotion,
	// is when that promotion ends. A running check stage is stopped at the
	// bound too. Its recurring tasks go on firing meanwhile.
	BoundReached bool `json:"bound_reached,omitempty"`
	// Checking and ChecksUntil retain older watch records in which sessions
	// waited out check stages past the drain bound, until the stage's own bound.
	// New sessions stop running stages at the drain bound and leave both empty.
	Checking    int       `json:"checking,omitempty"`
	ChecksUntil time.Time `json:"checks_until,omitzero"`
	// Promoting is how many of the runs the session hosts were at their
	// promotion when it recorded this, past the bound, and PromotingSince is
	// when it began waiting them out. Such a run holds the target branch's lease
	// and is left to finish rather than stopped, so the restart waits on it. The
	// session says this again at intervals while the wait lasts, because a
	// promotion can outlast every other bound here and a session that wrote
	// nothing while it waited would look no different from one that died.
	Promoting      int       `json:"promoting,omitempty"`
	PromotingSince time.Time `json:"promoting_since,omitzero"`
	// PullSkipped marks a poll that declined to pull into a free seat because
	// the bound was less than one poll away — a run started then would only be
	// stopped. It is on the drain so a reader of the idle line it was said on
	// knows the seat was left on purpose rather than for want of work.
	PullSkipped bool `json:"pull_skipped,omitempty"`
}

// Bound is how long the session waits before it restarts anyway.
func (d WatchDrain) Bound() time.Duration {
	return time.Duration(d.BoundSeconds) * time.Second
}

// Says is the drain as the clause every surface prints beside the session's
// state, so `yoyo status`, the watch log, and the channel name it in one way.
func (d WatchDrain) Says() string {
	said := fmt.Sprintf("draining to restart into the build deployed over it since %s, bounded at %s (until %s)",
		d.Since.UTC().Format(time.RFC3339), d.Bound(), d.Until.UTC().Format(time.RFC3339))
	if d.BoundReached {
		if d.Checking > 0 {
			return fmt.Sprintf("%s; the bound has run out, so it is waiting out a check stage in %d run(s) it hosts until %s at the latest, the check-stage bound, and stopping and preserving every other run at a developer attempt or a review for the session that comes back; nothing more is pulled into a free seat until it does, and its recurring tasks go on firing",
				said, d.Checking, d.ChecksUntil.UTC().Format(time.RFC3339))
		}
		if d.Promoting > 0 {
			return fmt.Sprintf("%s; the bound has run out, so it has been waiting out %d run(s) it hosts at their promotion since %s, because a promotion is never stopped part-way, and restarts the moment they finish; every other run is stopped and preserved for the session that comes back, nothing more is pulled into a free seat until it does, and its recurring tasks go on firing",
				said, d.Promoting, d.PromotingSince.UTC().Format(time.RFC3339))
		}
		return said + "; the bound has run out, so the runs it hosts are stopped and preserved for the session that comes back, nothing more is pulled into a free seat until it does, and its recurring tasks go on firing"
	}
	if d.PullSkipped {
		return said + "; the bound is less than one poll away, so nothing more is pulled into a free seat and the session that comes back pulls it"
	}
	if d.Hosting > 0 {
		return fmt.Sprintf("%s, waiting out %d run(s) it hosts while still pulling into free seats and firing its recurring tasks", said, d.Hosting)
	}
	return said
}

func (d WatchDrain) validate() error {
	var problems []error
	if d.Since.IsZero() {
		problems = append(problems, errors.New("draining names no moment it began"))
	}
	if d.BoundSeconds <= 0 {
		problems = append(problems, fmt.Errorf("draining is bounded at %d seconds, which is no bound", d.BoundSeconds))
	}
	if d.Until.IsZero() {
		problems = append(problems, errors.New("draining names no moment it ends"))
	}
	if d.Hosting < 0 {
		problems = append(problems, fmt.Errorf("draining reports %d hosted runs, which is not a count", d.Hosting))
	}
	if d.Checking < 0 {
		problems = append(problems, fmt.Errorf("draining reports %d runs waited out at their checks, which is not a count", d.Checking))
	}
	if d.Checking > 0 && d.ChecksUntil.IsZero() {
		problems = append(problems, errors.New("draining waits out a check stage and names no moment that wait ends"))
	}
	if d.Promoting < 0 {
		problems = append(problems, fmt.Errorf("draining reports %d runs waited out at their promotion, which is not a count", d.Promoting))
	}
	if d.Promoting > 0 && d.PromotingSince.IsZero() {
		problems = append(problems, errors.New("draining waits out a promotion and names no moment that wait began"))
	}
	return errors.Join(problems...)
}

func (t WatchTransition) Validate() error {
	var problems []error
	if t.SchemaVersion != WatchSchemaVersion {
		problems = append(problems, fmt.Errorf("watch transition schema version %d is not supported", t.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(t.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !watchSessionIDPattern.MatchString(t.SessionID) {
		problems = append(problems, errors.New("session_id is invalid"))
	}
	if !t.State.Valid() {
		problems = append(problems, fmt.Errorf("watch state %q is not one a session takes", t.State))
	}
	if t.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if len(t.Reason) > MaxWatchReasonBytes {
		problems = append(problems, fmt.Errorf("watch transition reason is %d bytes, which exceeds the %d byte bound", len(t.Reason), MaxWatchReasonBytes))
	}
	// A session recorded before the build was written down carries none, and that
	// is not a malformed entry: what it costs is the one comparison, which is
	// reported as unmakeable where it is read.
	if t.Build != "" && !buildPattern.MatchString(t.Build) {
		problems = append(problems, fmt.Errorf("watch transition build %q is not a revision", t.Build))
	}
	// A negative count of runs is not a session that saw fewer than none of them,
	// it is a caller with a bug, and a surface that printed it would be saying
	// something about the machine that nothing observed.
	if t.Running < 0 {
		problems = append(problems, fmt.Errorf("watch transition reports %d runs in flight, which is not a count", t.Running))
	}
	// A marker the harness does not recognize names a role nothing can address,
	// and the whole reason this field is here is to name somebody a reader can go
	// to. An empty marker is ordinary: it is a session whose idleness no
	// conversation accounts for.
	if t.Executor != "" && !t.Executor.Valid() {
		problems = append(problems, fmt.Errorf("watch transition executor %q is not a marker an item is carried by", t.Executor))
	}
	// Only an idle poll passes anything over. A watching session is starting what
	// it read, and a braked or stopped one never got as far as the queue, so an
	// account of what was left behind on one of those is a reading no pull made.
	if len(t.PassedOver.Groups) > 0 && t.State != WatchIdle {
		problems = append(problems, fmt.Errorf("a %s transition cannot say what a poll passed over, which is a thing only an idle one does", t.State))
	}
	for _, group := range t.PassedOver.Groups {
		if !group.Class.Valid() {
			problems = append(problems, fmt.Errorf("watch transition passes items over as %q, which is not a class a pull leaves an item in", group.Class))
		}
		// A group naming more items than it counts is an account that contradicts
		// itself, and the count is what every fraction said about it is drawn from.
		if group.Count < 1 || group.Count < len(group.Items) {
			problems = append(problems, fmt.Errorf("watch transition counts %d items as %q while naming %d of them", group.Count, group.Class, len(group.Items)))
		}
		// A marker the harness does not recognize names a role nothing can address,
		// for the reason the executor above may not: the whole use of the role here
		// is to name somebody a reader can go to.
		if group.Role != "" && !group.Role.Valid() {
			problems = append(problems, fmt.Errorf("watch transition passes items over to %q, which is not a role anything addresses", group.Role))
		}
		// The reasons are positional, so more of them than there are names is an
		// account nothing can read back: the reader cannot tell which item the extra
		// one belongs to, and a reason attached to the wrong item is worse than none.
		if len(group.Reasons) > len(group.Items) {
			problems = append(problems, fmt.Errorf("watch transition gives %d reason(s) for the %d item(s) it names as %q", len(group.Reasons), len(group.Items), group.Class))
		}
		for index, reason := range group.Reasons {
			if len(reason) > MaxPassedOverReasonBytes {
				problems = append(problems, fmt.Errorf("watch transition reason for %q item %d is %d bytes, which exceeds the %d byte bound", group.Class, index, len(reason), MaxPassedOverReasonBytes))
			}
		}
	}
	if t.PassedOver.Admitted < 0 {
		problems = append(problems, fmt.Errorf("watch transition read a queue of %d admitted items, which is not a count", t.PassedOver.Admitted))
	}
	// Only an idle poll can be one made inside a provider's window. A session
	// marked as waiting out a window while it is watching, braked, or stopped
	// would have every surface accounting for a silence that something else is
	// causing, which is the alarm this field exists to keep honest rather than to
	// turn off.
	if t.ProviderWindow && t.State != WatchIdle {
		problems = append(problems, fmt.Errorf("a %s transition cannot be a poll made inside the provider's usage window, which is a thing only an idle one is", t.State))
	}
	// A reset time on a transition that is not waiting out a window is a moment
	// nothing is waiting for, and a surface reading it would say the harness is
	// held until a time nobody is holding it to.
	if t.ProviderWindowResetsAt != nil {
		if !t.ProviderWindow {
			problems = append(problems, errors.New("a watch transition names when the provider's usage window lifts without saying it is waiting out one"))
		}
		if t.ProviderWindowResetsAt.IsZero() {
			problems = append(problems, errors.New("the provider's usage window is present and names no moment; a provider that named none records none"))
		}
	}
	// Only a stop can be a restart. A session marked as coming back while it is
	// still watching, idle, or braked would have every reader saying a restart is
	// under way that nothing is going to make.
	if t.Restarting && t.State != WatchStopped {
		problems = append(problems, fmt.Errorf("a %s transition cannot be a restart, which is a thing only a stop is", t.State))
	}
	// A dispatch wait is written only as a watching entry, which is what an older
	// reader will take it for; on any other state it would tell that reader the
	// session had braked, idled, or stopped when it had done nothing of the kind.
	if t.DispatchWait != nil {
		if t.State != WatchWatching {
			problems = append(problems, fmt.Errorf("a %s entry cannot carry a dispatch's wait, which is written only as a watching one", t.State))
		}
		if err := t.DispatchWait.validate(); err != nil {
			problems = append(problems, err)
		}
	}
	// A crossing is written only as a watching entry, for the dispatch wait's
	// reason, and one entry is one note: an entry carrying both would be read as
	// whichever a surface happened to look for.
	if t.WorktreeCrossing != nil {
		if t.State != WatchWatching {
			problems = append(problems, fmt.Errorf("a %s entry cannot carry a worktree crossing, which is written only as a watching one", t.State))
		}
		if t.DispatchWait != nil {
			problems = append(problems, errors.New("an entry carries a dispatch wait and a worktree crossing, and a note is one or the other"))
		}
		if err := t.WorktreeCrossing.validate(); err != nil {
			problems = append(problems, err)
		}
	}
	// A pass note is written only as a watching entry, and is one note on its own,
	// for the reasons the two above are.
	if t.RecurringPass != nil {
		if t.State != WatchWatching {
			problems = append(problems, fmt.Errorf("a %s entry cannot carry a recurring pass, which is written only as a watching one", t.State))
		}
		if t.DispatchWait != nil || t.WorktreeCrossing != nil {
			problems = append(problems, errors.New("an entry carries a recurring pass beside another note, and a note is one thing"))
		}
		if err := t.RecurringPass.validate(); err != nil {
			problems = append(problems, err)
		}
	}
	// A drain that cannot say when it began or when it gives up is a drain no
	// surface can name the bound of, which is the whole of what the field is for.
	if t.Draining != nil {
		if err := t.Draining.validate(); err != nil {
			problems = append(problems, fmt.Errorf("draining: %w", err))
		}
	}
	return errors.Join(problems...)
}

// NewWatchSessionID names one session of watching.
func NewWatchSessionID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate watch session id: %w", err)
	}
	return "watch-" + hex.EncodeToString(bytes), nil
}

// WatchStore is where a session's transitions are collected, in the same
// operating-system state root as the runs and the reports and beside them
// rather than among them. It is one append-only log per product, because what
// is being watched is one product's queue and because the transitions outlive
// the process that made them: a session that died is read from the entry it
// never wrote after.
type WatchStore struct {
	root      string
	productID domain.ProductID
}

func NewWatchStore(root string, productID domain.ProductID) (*WatchStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &WatchStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *WatchStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the account of the
// session actually is.
func (s *WatchStore) Path() string { return filepath.Join(s.root, "watch.jsonl") }

// Lease makes this process the product's only watching session, reporting
// whether it got it.
//
// Two sessions watching one product are not two workers. They read one queue and
// choose from it independently, and a run is not in the run store until it
// reserves — several steps after the item was chosen — so both can pick the same
// item inside that window, and the occupied-item bookkeeping each keeps is its
// own. Two of them briefly coexisted while the 2026-09-05 wedge was being
// cleared, which is what this refuses.
//
// The session stamps itself beside the lock as it takes it, under the lock, so a
// session refused here can say which one has it rather than only that somebody
// does. A lease that cannot be stamped is dropped rather than kept: a session
// nothing can name is exactly what the refusal exists to stop somebody meeting.
func (s *WatchStore) Lease(sessionID string) (*Lease, bool, error) {
	if !watchSessionIDPattern.MatchString(sessionID) {
		return nil, false, fmt.Errorf("watch session id %q is invalid", sessionID)
	}
	lease, held, err := TryLeasePath(filepath.Join(s.root, watchLeaseFile), "watch session")
	if err != nil || !held {
		return nil, held, err
	}
	holder := filepath.Join(s.root, watchHolderFile)
	if err := s.stampHolder(holder, sessionID); err != nil {
		return nil, false, errors.Join(err, lease.Release())
	}
	lease.holder = holder
	return lease, true, nil
}

// Held reports whether a session is holding this product's watch, by trying to
// take the lease and letting it go again. It is how the product's supervisor
// asks whether its scheduler child is alive, and it is the same question the
// sink's store answers about the sink the same way: the lock is dropped by the
// operating system when its holder dies, so the answer is about a process. It
// stamps nothing, because it is a question rather than a session.
func (s *WatchStore) Held() (bool, error) {
	lease, held, err := TryLeasePath(filepath.Join(s.root, watchLeaseFile), "watch session")
	if err != nil {
		return false, err
	}
	if !held {
		return true, nil
	}
	if err := lease.Release(); err != nil {
		return false, err
	}
	return false, nil
}

// WatchHolder is the session holding this product's watch, as it stamped itself
// when it took the lease. It carries the session identifier the log and `yoyo
// status` also carry, so a refusal and every other surface name one session
// rather than two, and the process, which is what somebody stops when stopping
// it is what they want.
type WatchHolder struct {
	SessionID string    `json:"session_id"`
	PID       int       `json:"pid"`
	HeldAt    time.Time `json:"held_at"`
	// Build is the revision the holding process was built from. A session that
	// re-executes itself into a deployed build takes the lease again as the new
	// build and stamps it again, so this is what the supervisor reads to know the
	// scheduler has moved. It is empty for a holder from a build older than the
	// field, or one that carries no revision.
	Build string `json:"build,omitempty"`
}

// Holder is the session that stamped itself as watching this product, reported
// as an absence where none has rather than as a failure. It says nothing about
// whether that session is still alive — the lease is what decides that, and this
// is only how the holder of one is named — so it is read after a lease was
// refused rather than instead of trying to take one.
//
// It is the tolerant door of the two in tolerantread.go. The stamp is written
// whole by the session that holds the watch and never read to be written back,
// and its readers — the supervisor naming the scheduler it adopts, the sentence
// that refuses a second watch — are exactly the ones that meet a session on a
// newer build than their own.
func (s *WatchStore) Holder() (WatchHolder, bool, error) {
	file, err := os.Open(filepath.Join(s.root, watchHolderFile))
	if errors.Is(err, os.ErrNotExist) {
		return WatchHolder{}, false, nil
	}
	if err != nil {
		return WatchHolder{}, false, fmt.Errorf("open the watch holder: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return WatchHolder{}, false, fmt.Errorf("read the watch holder: %w", err)
	}
	var holder WatchHolder
	unknown, err := decodeTolerating(encoded, &holder)
	if err != nil {
		return WatchHolder{}, false, fmt.Errorf("decode the watch holder: %w", err)
	}
	noteUnknownFields("watch holder", unknown)
	if holder.PID <= 0 {
		return WatchHolder{}, false, errors.New("the watch holder names no process")
	}
	return holder, true, nil
}

// stampHolder writes this process's stamp for the watch it now holds. It is
// replaced by rename rather than written in place, so a reader sees the whole of
// one stamp or none of it and never half of one.
func (s *WatchStore) stampHolder(path, sessionID string) error {
	temporary, err := os.CreateTemp(s.root, ".watch-holder-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary watch holder: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary watch holder: %w", err)
	}
	holder := WatchHolder{SessionID: sessionID, PID: os.Getpid(), HeldAt: time.Now().UTC(), Build: buildinfo.Commit()}
	if err := writeJSONFile(temporary, "watch holder", holder); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary watch holder: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace watch holder: %w", err)
	}
	return syncDirectory(s.root)
}

// Record appends one transition. It is an append rather than a rewrite for the
// reason every other log here is: a transition is written once and never
// revised, and two sessions watching one product must not overwrite each
// other's account.
func (s *WatchStore) Record(transition WatchTransition) error {
	if err := s.validate(transition); err != nil {
		return err
	}
	encoded, err := encodeWatchTransition(transition)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedWatchBytes {
		return fmt.Errorf("encoded watch transition is %d bytes, limit is %d", len(encoded), maxEncodedWatchBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create watch directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect watch log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open watch log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append watch transition: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append watch transition: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync watch log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close watch log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every recorded transition in the order it happened. A log that
// does not exist yet is a product nobody has watched, which is not a failure to
// read. A line that will not decode is: a session nobody can read must not be
// reported as one in whatever state the readable lines happen to end on. The
// sink reads past such a line by position with Scan, and says so.
func (s *WatchStore) List() ([]WatchTransition, error) {
	transitions, skipped, err := s.Scan()
	if err != nil {
		return nil, err
	}
	if err := firstSkipped("watch log", skipped); err != nil {
		return nil, err
	}
	return transitions, nil
}

// Scan returns every transition that decoded, in the order it happened, and
// beside them the lines that would not, each at the position it holds among the
// records. It is the read a positional cursor is kept against, so one bad line
// costs the reader that line and nothing behind it.
func (s *WatchStore) Scan() ([]WatchTransition, []SkippedLine, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open watch log: %w", err)
	}
	defer file.Close()

	var transitions []WatchTransition
	skipped, err := scanLog(file, maxEncodedWatchBytes, func(line []byte) error {
		decoded, err := decodeWatchTransition(line)
		if err != nil {
			return err
		}
		if err := s.validate(decoded); err != nil {
			return err
		}
		transitions = append(transitions, decoded)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read watch log: %w", err)
	}
	return transitions, skipped, nil
}

// Latest is the last transition recorded, which is where a session got to. A
// product nobody has watched has none, which is reported as an absence rather
// than as a session in some default state: never having watched and having
// stopped watching are different facts. A note about a dispatch is not where the
// session got to, so it is read past.
func (s *WatchStore) Latest() (WatchTransition, bool, error) {
	transitions, err := s.List()
	if err != nil {
		return WatchTransition{}, false, err
	}
	for index := len(transitions) - 1; index >= 0; index-- {
		if !transitions[index].Note() {
			return transitions[index], true, nil
		}
	}
	return WatchTransition{}, false, nil
}

func decodeWatchTransition(data []byte) (WatchTransition, error) {
	var decoded WatchTransition
	if err := json.Unmarshal(data, &decoded); err != nil {
		return WatchTransition{}, err
	}
	if err := decoded.Validate(); err != nil {
		return WatchTransition{}, err
	}
	return decoded, nil
}

func encodeWatchTransition(transition WatchTransition) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(transition); err != nil {
		return nil, fmt.Errorf("encode watch transition: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *WatchStore) validate(transition WatchTransition) error {
	if transition.ProductID != s.productID {
		return fmt.Errorf("watch transition product %q does not match store product %q", transition.ProductID, s.productID)
	}
	return transition.Validate()
}
