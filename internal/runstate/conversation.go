package runstate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// ConversationSchemaVersion is versioned independently of run state. A
// conversation is not a run: it has no worktree, no checks, no verdict, and
// nothing to integrate, so it is recorded in its own shape rather than squeezed
// into a schema whose invariants describe bounded work.
//
// It stays 1 because every addition since has been an optional key: a record
// written before the work item this conversation last ran was kept still
// decodes, and its absence means what it always meant, which is that this
// conversation has started nothing.
const ConversationSchemaVersion = 1

// ErrNoConversation reports that a role has no recorded conversation, so the
// caller starts one instead of resuming.
var ErrNoConversation = errors.New("role has no recorded conversation")

// ErrConversationHeld reports that another process holds the conversation. It
// is a sentinel because "somebody else is talking to this agent" and "the state
// directory could not be read" lead to opposite conclusions, and a caller that
// cannot tell them apart reports a broken state root as a conversation in
// progress.
var ErrConversationHeld = errors.New("already held by another process")

// Conversation is the durable record of one operator conversation with an
// agent. It exists so a conversation survives the process that held it: the
// provider session identifier is what a later process resumes from, and the
// requested and resolved model selectors are the evidence of what actually
// answered.
type Conversation struct {
	SchemaVersion  int              `json:"schema_version"`
	ConversationID string           `json:"conversation_id"`
	ProductID      domain.ProductID `json:"product_id"`
	RepositoryID   string           `json:"repository_id"`
	// Agent is the configured agent this conversation is with, and Role is the
	// authority it carries. They are usually the same word, because an agent is
	// conventionally named for its role, and they are recorded separately
	// because a project may configure two agents for one role: those are two
	// identities with two personas, two model selectors, and two provider
	// sessions, and a record that named only the role would have each of them
	// resuming the other's session.
	//
	// It is empty on a record written before the agent was part of the identity.
	// Such a record was necessarily written for the agent named after its role —
	// nothing else could have addressed it — so it keeps loading under that name
	// and acquires the agent the next time it is saved.
	Agent   string           `json:"agent,omitempty"`
	Role    domain.AgentRole `json:"role"`
	Backend domain.Backend   `json:"backend"`
	// ProviderSessionID is the session a later process resumes. It is empty
	// until a turn completes, and a conversation that has taken turns without one
	// is continued from its record rather than from a session.
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	// SessionSetAside is what the provider said when it refused this
	// conversation's session as too long to continue, and empty once a fresh
	// session has served a turn. It is what lets a rebuild tell the role why it
	// has no session, rather than inferring a reason from the session's absence.
	SessionSetAside string `json:"session_set_aside,omitempty"`
	// ProviderSessionBytes is how large that session has grown, as the harness
	// measures it: every prompt it sent the session and every reply it got back,
	// the system prompt aside because each request carries that anew. It is what
	// decides when the session is compacted, and ProviderSessionBudgetBytes is the
	// size at which it is, recorded beside it so a reader can see how close a
	// session is without knowing the harness's constant. Both are rewritten by
	// each completed turn, and a turn that starts a session starts the measure
	// again. Both are zero on a session recorded before the harness measured one,
	// which the next turn compacts rather than assumes small.
	ProviderSessionBytes       int `json:"provider_session_bytes,omitempty"`
	ProviderSessionBudgetBytes int `json:"provider_session_budget_bytes,omitempty"`
	// ProviderModel is the selector the conversation requested and
	// ProviderResolvedModel is what the provider reported serving it, because a
	// floating family alias makes the resolved identifier the only real record.
	ProviderModel         string `json:"provider_model,omitempty"`
	ProviderResolvedModel string `json:"provider_resolved_model,omitempty"`
	// ProviderEffort is the effort level the last completed turn asked for, and
	// empty where the agent configured none. It is rewritten by each turn as the
	// model is; what pins every turn is the cost log's line for it, which carries
	// the level too.
	ProviderEffort string `json:"provider_effort,omitempty"`
	// ProviderResolvedEffort is provider-reported; ProviderEffortReported is false when not reported.
	ProviderResolvedEffort string `json:"provider_resolved_effort,omitempty"`
	ProviderEffortReported bool   `json:"provider_effort_reported"`
	// ProviderLoaded is the settings sources, skills, plugins, connectors, and
	// instruction files the last completed turn was given beside its prompt, by name and source. It is
	// rewritten by each turn as the model is.
	ProviderLoaded *backend.Loaded `json:"provider_loaded,omitempty"`
	// AccountAlias is the provider account the turn this record last took was
	// answered on, and ConfigRevision the configuration in force while it was.
	// They sit beside the backend and the model selectors and are kept exactly as
	// those are: rewritten by each completed turn, so the record says what served
	// the conversation as it now stands. Under a pool that stops being
	// bookkeeping — the account answering this conversation is the agent's own
	// rather than the machine's default, so the alias is the only thing on the
	// record that says whose subscription is paying for it.
	//
	// What pins every turn rather than the last one is the cost log, which takes a
	// line per provider invocation carrying the account and the revision that
	// served it, and refuses a line that names either. So a conversation resumed
	// after a configuration edit or an account move still has each earlier turn's
	// attribution, on that turn's own line, and this record is not the only copy
	// of any of it.
	//
	// That is the whole of why this is one pair and an exchange's is one per
	// round. An exchange record holds its rounds, so a round is a thing already in
	// the record to pin; a conversation record holds no turns at all — it is a
	// summary every turn rewrites in place — and a per-turn list inside it would
	// be an unbounded array in a file that is rewritten on every turn, kept for a
	// fact the cost log already keeps correctly.
	//
	// Both are empty on a conversation recorded before the harness wrote them
	// down, and on one whose first turn has not completed.
	AccountAlias   string `json:"account_alias,omitempty"`
	ConfigRevision string `json:"config_revision,omitempty"`
	// Build is the repository revision the harness binary holding this
	// conversation was built from, rewritten by each completed turn exactly as the
	// pair above is. A conversation outlives the process that opened it, so what
	// this says is which harness is answering it now — and a conversation an
	// operator left open for days is one of the residents that quietly goes on
	// running a binary the harness has moved past.
	//
	// What pins every turn rather than the last one is the same cost log the
	// account and the revision are pinned by, which now carries the build on each
	// line for the same reason it carries those.
	//
	// It is empty on a conversation recorded before the harness wrote it down, on
	// one whose first turn has not completed, and where the binary carries no
	// revision of its own.
	Build string `json:"build,omitempty"`
	Turns int    `json:"turns"`
	// PendingTrackerResults is what an agent asked of the work tracker and has not
	// been told the result of yet, already rendered as the text its next turn is
	// given. It is durable for the same reason the provider session is: the agent
	// acted, the process that watched it act may be gone, and an agent that never
	// learns what its own actions did is one that will describe them wrongly.
	//
	// What it carries is the results of actions the harness carried out, and the
	// refusal of a block it would not read at all. The second is the same fact in
	// its starkest form — every action in the block is a thing the agent believes
	// it did and did not — so it travels the same way rather than in a field of its
	// own.
	PendingTrackerResults string `json:"pending_tracker_results,omitempty"`
	// ReplyCuts are the replies of this conversation's last turn that the event
	// log holds only the beginning of, and that the role has not been told about
	// yet. The log is where a role's ruling lives until it can write the document
	// it owns, so a reply cut there is a decision the record lost; the next turn
	// opens by saying which reply, how much of it survives, and where it stops,
	// so the role can restate what the record lost.
	ReplyCuts []execution.ReplyCut `json:"reply_cuts,omitempty"`
	// RefusedBlock is the tracker block this conversation had refused whole and
	// has not answered yet. It is the same fact the pending results above carry
	// into the role's next turn, kept as a record rather than as prose so that
	// something other than the next person at a terminal can act on it: the words
	// are what the role reads, and this is what says a turn is owed and whether
	// the harness has started one.
	//
	// It is cleared by the first turn whose reply the harness could read, which is
	// the role having answered the refusal one way or another. A refusal arriving
	// while one is already recorded is therefore the second in a row, and is
	// escalated rather than woken for; see TrackerRefusal.
	RefusedBlock *TrackerRefusal `json:"refused_block,omitempty"`
	// ContextGatheredAt is when the picture of the product the agent is working
	// from was assembled, and ContextCommit is the repository commit it was
	// assembled against. They are durable because the process that briefed the
	// agent is usually not the one that resumes it, and a resumed conversation
	// that cannot say how old its picture is will describe a repository as it
	// was hours ago and sound exactly as certain about it. They are empty on a
	// conversation recorded before the harness wrote them down, and on one whose
	// first turn has not completed.
	//
	// After the first turn they advance whenever the harness re-reads the
	// repository and the tracker for the conversation, before the re-read is
	// delivered: the next measurement is taken from the newest picture read rather
	// than from one a failing turn never replaced. On 2026-09-23 the development
	// manager's record stayed at one picture for over thirty hours because every
	// turn meant to deliver the next one failed. Where a re-read has not been
	// delivered yet, PendingPicture below says so and names the picture the agent
	// last received.
	ContextGatheredAt time.Time `json:"context_gathered_at,omitempty"`
	ContextCommit     string    `json:"context_commit,omitempty"`
	// ContextShippedDocumentationBytes is what the shipped documentation in that
	// picture added up to on disk. It is recorded with the picture, on every
	// pass, because the set has a ceiling at which carrying it whole is a
	// product decision again, and a size nobody wrote down is a growth nobody
	// sees until the gate on it fails. It is zero on a conversation recorded
	// before it was written down and on a project naming no documentation.
	ContextShippedDocumentationBytes int `json:"context_shipped_documentation_bytes,omitempty"`
	// PendingPicture is a re-read of the repository and the tracker that no turn
	// has delivered yet: taken by a refresh, recorded before the turn that would
	// carry it is asked, and cleared by the turn that carries it. It is durable
	// for the reason the tracker results above are, in a sharper form. A re-read
	// that lived only in the process that took it was thrown away whenever that
	// process's turn failed, and the next process read the repository and the
	// tracker again from the same old commit — which on 2026-09-20 turned one
	// stuck picture into 21 completed re-reads, every one of them discarded.
	//
	// The picture's text is not in here. It is close to a megabyte and this
	// record has a megabyte to live in altogether, so the text is written beside
	// the record and this says which picture that text is; see
	// SavePendingPictureText.
	PendingPicture *PendingPicture `json:"pending_picture,omitempty"`
	// LastRunWorkItemID is the work item of the run this conversation started
	// most recently. It is durable for the same reason the rest of this is: the
	// process that started the run is often not the one the operator comes back
	// to, and "what did that change" is a question about the run they last
	// watched rather than about whichever process was holding it. It is empty on
	// a conversation that has never started one.
	LastRunWorkItemID string `json:"last_run_work_item_id,omitempty"`
	// DeliveredAmendmentIDs are the changes proposed to this role's documents
	// that the conversation has already carried into a turn. A proposal stays
	// pending until somebody decides it, so without a record of what was already
	// said the same list would be delivered again on every turn — and, because
	// the process that delivered it is usually not the one that resumes the
	// conversation, it is durable for the same reason the provider session is.
	// It is bounded, and an id dropped from it is delivered once more rather
	// than lost, which is the right way for this to fail.
	DeliveredAmendmentIDs []string `json:"delivered_amendment_ids,omitempty"`
	// DeliveredReportIDs are the collected reports this conversation has already
	// carried into a turn. A report stays in the pile until somebody records what
	// became of it, so without this the same unhandled reports would be delivered
	// again every turn. It is durable and bounded for exactly the reasons the
	// amendment ids above are, and an id dropped from it is offered once more
	// rather than lost — which is the right way for this to fail, because the
	// failure it must never have is a report nobody is ever shown.
	DeliveredReportIDs []string `json:"delivered_report_ids,omitempty"`
	// ReportPosition is how far through the collected pile, in the order it was
	// filed, this conversation has been carried. The ids above say what it
	// remembers being shown and this says where it got to, and the pile needs both:
	// the record is bounded and a pile of hundreds outgrows it, so a delivery
	// paced by the ids alone re-offers the same worst-first handful forever and
	// never reaches what was filed behind them. It is empty on a conversation that
	// has been shown nothing, which is the beginning of the pile.
	ReportPosition report.Position `json:"report_position,omitempty"`
	// PendingProposals are the work items an agent proposed that nobody has
	// decided yet. They are durable for the reason the provider session is, and
	// the reason is sharper here than anywhere else in this record: a proposal
	// made by one `--message` invocation is decided by another, and a process
	// that could not read back what was proposed had nothing for an approval to
	// name. The operator's "y" then arrived as ordinary speech, the proposal was
	// never decided, and nothing reached the queue.
	//
	// It holds only the undecided ones. A decision is an event in the log and
	// stays there; what is kept here is the set a later process may still act on,
	// so a proposal leaves this list the moment it is approved or declined.
	PendingProposals []PendingProposal `json:"pending_proposals,omitempty"`
	// PendingConcerns are the questions an agent stopped to ask that nobody has
	// answered yet. They are durable for exactly the reason the proposals are: a
	// concern raised by one `--message` invocation is answered by another, and a
	// process that could not read back what was asked had nothing an answer
	// could name. Before this a concern lived only in the process that raised
	// it, so the one way to answer it was the interactive prompt — and an
	// operator answering from the command line with "yes" was deciding whatever
	// proposal happened to be on the table instead.
	//
	// It holds only the unanswered ones, as PendingProposals holds only the
	// undecided: the answer is an event in the log, and a concern leaves this
	// list the moment it is answered.
	PendingConcerns []PendingConcern `json:"pending_concerns,omitempty"`
	// PendingWrites are the documents an owning role wrote that the operator has
	// not decided about yet. They are durable for the reason the proposals above
	// are, and the reason is the same one sharpened again: a document written by
	// one `--message` invocation is approved by another, and a process that could
	// not read back what was written had nothing for the approval to name — so
	// the drafted document went back to being something a person transcribed by
	// hand, which is the seam this record exists to close.
	//
	// It holds only the undecided ones. The write itself is an event in the log
	// and stays there; what is kept here is what a later process may still be
	// asked to carry out.
	PendingWrites   []PendingWrite `json:"pending_writes,omitempty"`
	DocumentReturns map[string]int `json:"document_returns,omitempty"`
	// PendingNotices is the account of harness activity the agent has not been
	// told about yet, and PendingNoticesDropped says older activity was cut to
	// keep it bounded. They are durable for the same reason the tracker results
	// beside them are: the process that watched the operator act is usually not
	// the one that asks the next question, and an agent that never learns the
	// operator approved its own proposal will describe the queue wrongly.
	PendingNotices        []string  `json:"pending_notices,omitempty"`
	PendingNoticesDropped bool      `json:"pending_notices_dropped,omitempty"`
	LastSequence          uint64    `json:"last_sequence"`
	StartedAt             time.Time `json:"started_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// TrackerRefusal is one refused tracker block as the record keeps it, and what
// the harness has done about it.
//
// The refusal already reaches the role at the start of its next turn. What this
// adds is the half nothing carried: whether a turn has been started for it. A
// refusal recorded and never woken for waits on somebody typing, which is how
// both of this week's refused batches came to need the operator's assistant to
// prompt the re-issue.
//
// The two endings are deliberate. A refusal is put to the role once, because a
// turn that was taken again on each pass would be the harness asking a role that
// cannot answer, over and over, at a turn a time. A refusal that arrives while
// one is still outstanding — the woken turn refused again, or the same defect
// back — carries the reason it will not be woken for instead, and that is what
// the operator is told.
type TrackerRefusal struct {
	// Turn is the conversation turn whose block was refused, and Actions how many
	// it asked for. Actions is zero where the harness could not count them, which
	// is a payload it never decoded rather than a block that asked for nothing.
	Turn    int `json:"turn"`
	Actions int `json:"actions,omitempty"`
	// Problem is the refusal in the harness's own words, which is the same text
	// the role is given back.
	Problem   string    `json:"problem"`
	RefusedAt time.Time `json:"refused_at"`
	// WokenAt is when the harness started a turn for this refusal. Zero is a
	// refusal no turn has been put to yet.
	WokenAt time.Time `json:"woken_at,omitempty"`
	// Attempts is how many wakeups have been claimed for this refusal, and
	// LastAttemptAt when the most recent was. They are the pair that makes the
	// retry below both bounded and paced, and neither is ever given back: a
	// wakeup the provider never took — no capacity, an outage, a lapsed login —
	// returns the turn it never took and keeps its place in the count, which is
	// what stops a provider that never comes back from being retried forever. See
	// MaxRefusalWakeups.
	Attempts      int       `json:"attempts,omitempty"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	// Escalated is why the harness will not wake for this refusal and has put it
	// in front of the operator instead. Empty is the ordinary refusal.
	Escalated string `json:"escalated,omitempty"`
}

// MaxRefusalWakeups bounds how many turns the harness starts for one refusal
// before it stops trying.
//
// It counts wakeups claimed rather than turns taken, so it bounds the retry a
// provider window earns as well as the turns a role actually answered. A wakeup
// the provider refused for want of capacity gives back the turn it never took —
// so the refusal is still owed one, and the window has not silently spent the
// only one it had — but not its place in this count, which is what makes a window
// that never clears end in abandonment rather than in a wakeup every quarter of
// an hour for ever.
//
// The same count bounds the other ending that puts nothing in front of the role:
// a provider answering nobody, because it is down or because the account's login
// has lapsed. Unlike a window that one does not clear on its own schedule, which
// is why it is bounded rather than ridden out for as long as it lasts.
//
// Three is what "briefly out of capacity" is worth riding out: paced by the delay
// below, it spans half an hour from the first attempt. A refusal that exhausts it
// still has the harness's own words at the top of its conversation, so what it
// loses is the harness starting the turn — which is stated where the attempts run
// out rather than left to be inferred from the quiet: the give-back that spends
// the last attempt marks the refusal Escalated and records it as unresolved, so
// the operator is told. See WithdrawRefusalWakeup.
const MaxRefusalWakeups = 3

// RefusalWakeupRetryDelay is how long a wakeup that reached nobody is left alone
// before it is made again.
//
// The bound above is worth nothing without it. Whatever drives the wakeup decides
// how often it looks, and the loop that does today looks once per pull — which on
// a watching session is once a minute — so three attempts counted and not paced
// would be three attempts inside three minutes, and a refusal abandoned in the
// time a provider took to come back. Paced, it costs one refused call a quarter
// of an hour and leaves the passes between it for the queue.
//
// It is measured from the last attempt rather than the first, for the reason the
// escalation's is: a wakeup that failed, waited, and failed again waits again
// rather than being abandoned on a clock that started before anybody knew there
// was a problem.
const RefusalWakeupRetryDelay = 15 * time.Minute

// AwaitingWakeup reports a refusal the harness still owes a turn: no turn has
// been put to the role, it is not one the operator has been handed instead, its
// attempts are not spent, and the last one that reached nobody is old enough to
// be worth making again.
func (r TrackerRefusal) AwaitingWakeup(now time.Time) bool {
	switch {
	case r.Escalated != "" || !r.WokenAt.IsZero():
		return false
	case r.Attempts >= MaxRefusalWakeups:
		return false
	case r.Attempts == 0 && r.LastAttemptAt.IsZero():
		return true
	default:
		return !now.Before(r.LastAttemptAt.Add(RefusalWakeupRetryDelay))
	}
}

// MaxTrackerRefusalProblemBytes bounds the refusal a conversation record carries.
// The whole of what the role is given back is bounded separately by the pending
// results it travels in; this is the copy the record keeps, and it is held well
// below the state file's own limit for the reason every other bound here is.
const MaxTrackerRefusalProblemBytes = 4 << 10

// ErrNoRefusalAwaitingWakeup reports a conversation with no refused tracker
// block owed a turn: none recorded, one already woken for, or one the operator
// has been handed. It is a sentinel because a pass walking past a conversation
// somebody else just woke is the record doing its job rather than a failure.
var ErrNoRefusalAwaitingWakeup = errors.New("the conversation has no refused tracker block awaiting a wakeup")

// PendingProposal is one proposed work item as the record keeps it: what was
// proposed, which turn proposed it, and why the harness did not simply admit
// it. The conversation it belongs to is the record it sits in, so it is not
// repeated here.
//
// It is declared in this package rather than shared with the conversation code
// that builds it, because that code already depends on this one and the
// dependency may not run both ways. The field names are the ones the proposal
// contract uses, so what is written here reads as what was proposed.
type PendingProposal struct {
	ID            string   `json:"id"`
	Turn          int      `json:"turn"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Rationale     string   `json:"rationale"`
	Goal          string   `json:"goal"`
	RelevantGoals []string `json:"relevant_goals,omitempty"`
	Parent        string   `json:"parent,omitempty"`
	// Dependencies name the items the proposed work waits for.
	Dependencies []string `json:"dependencies,omitempty"`
	// Class is the kind of work the proposal claims to be, where a project treats
	// a kind of work differently at admission. It is kept because it decides
	// whether the operator is asked at all, so a proposal that came back without
	// it would be a different proposal from the one that was made.
	Class string `json:"class,omitempty"`
	// Kind is whether the proposed work is a bug fix or a feature. It is empty on
	// a proposal recorded before kinds were required, and the item approved from
	// one is then created untyped, where the survey names it.
	Kind string `json:"kind,omitempty"`
	// Asking is what kept this proposal out of a queue it would otherwise have
	// gone into, worked out when it was proposed rather than when it is decided.
	Asking string `json:"asking,omitempty"`
	// Lane is the lane label a program manager's creation was proposed in, kept
	// because an approval creates the item in that lane and a proposal that came
	// back without it would be admitted outside the lane it was made for.
	Lane string `json:"lane,omitempty"`
	// Asker is who asked for the work, as the item is to record it if it is
	// admitted: the operator, where the proposal was made in a conversation they
	// were speaking in, or the role's own pass. It is absent from a proposal
	// recorded before origins were, whose item then records none.
	Asker string `json:"asker,omitempty"`
}

// PendingWrite is one document an owning role wrote, as the record keeps it:
// what would be done to which document, the document itself, and which turn
// wrote it. The conversation it belongs to is the record it sits in, so it is
// not repeated here.
//
// It is declared in this package rather than shared with the conversation code
// that builds it, for the reason the pending proposal beside it is: that code
// already depends on this one and the dependency may not run both ways. The
// field names are the ones the write contract uses, so what is written here
// reads as what the role wrote.
type PendingWrite struct {
	Publication *DocumentPublication `json:"publication,omitempty"`
	Intent      string               `json:"intent,omitempty"`
	ID          string               `json:"id"`
	Turn        int                  `json:"turn"`
	// Action is "create" or "revise", kept as text because this record says what
	// was waiting rather than deciding what is legal; what is legal is the
	// artifact package's, and it judges this again when the write is carried out.
	Action    string   `json:"action"`
	Artifact  string   `json:"artifact"`
	Kind      string   `json:"kind,omitempty"`
	Title     string   `json:"title,omitempty"`
	Supports  []string `json:"supports,omitempty"`
	Directory string   `json:"directory,omitempty"`
	Body      string   `json:"body"`
	Reason    string   `json:"reason"`
}

// MaxPendingWrites bounds the undecided documents one conversation carries, and
// MaxPendingWriteBytes bounds one of them. A document is the largest thing this
// record holds — a whole Markdown file, not a description of one — so the count
// is small where the proposals' is twenty: a conversation with two documents
// waiting on the operator has already asked them for more than anybody answers
// in one sitting, and the state file has to stay well inside its own limit.
//
// The byte bound matches what the write contract accepts, so a document the
// harness took from a reply is never one it then cannot write down.
const (
	MaxPendingWrites     = 2
	MaxPendingWriteBytes = 64 << 10
)

// MaxPendingProposals bounds the undecided proposals one conversation carries.
// A proposal carries the whole of what an operator decides from — its
// description and its rationale — so unlike the amendment identifiers above it
// is a bound on something large: twenty of them at their own maximum size is
// comfortably inside what a state file may hold, while a hundred would put a
// conversation in the state where every save of it fails.
//
// It is well above any conversation anybody holds. One reply proposes at most
// ten items, and a second reply proposing ten more with none of the first
// decided is already a conversation nobody is reading.
const MaxPendingProposals = 20

// PendingConcern is one raised concern as the record keeps it: what the agent
// would not propose, which turn stopped to ask, and the answers it offered.
// Like PendingProposal it is declared here rather than shared with the
// conversation code that builds it, because that code depends on this package
// and the dependency may not run both ways; the field names are the concern
// contract's, so what is written here reads as what was asked.
type PendingConcern struct {
	ID       string `json:"id"`
	Turn     int    `json:"turn"`
	Kind     string `json:"kind"`
	Subject  string `json:"subject"`
	Goal     string `json:"goal,omitempty"`
	Detail   string `json:"detail"`
	Question string `json:"question"`
	// Options are the answers the question can be answered by, where the agent
	// enumerated any. They are kept because an answer sent from the command line
	// may pick one by its number, exactly as the prompt lets it.
	Options []string `json:"options,omitempty"`
}

// MaxPendingConcerns bounds the unanswered concerns one conversation carries.
// A reply raises at most five, so ten is two whole turns' worth of questions
// nobody has answered — a conversation that far past its own questions has
// moved on from the earliest of them, which is what is dropped. Each one is
// small beside a proposal, so the bound is about the record staying a list an
// operator can be asked to answer from rather than about the state file.
const MaxPendingConcerns = 10

// PendingPicture is one re-read of the repository and the tracker that nothing
// has delivered yet, as the record keeps it: when it was gathered, the commit it
// was gathered against, what had moved when it was taken, and who took it. Like
// PendingProposal it is declared in this package rather than shared with the
// conversation code that builds it, because that code already depends on this
// one and the dependency may not run both ways.
//
// What it is for is the process that was not there. A refresh is taken by one
// process and may be delivered by another — the turn that would have carried it
// can fail, and the next thing to say something to the agent is usually a fresh
// process — so everything the delivery needs is here rather than in the memory
// of whoever read the repository.
type PendingPicture struct {
	// GatheredAt is when the picture was taken and Commit the repository commit
	// it was taken against. The record's own picture is advanced to them when the
	// picture is read, so the conversation is recorded as measured from the
	// picture that was read rather than from one the delivering process measured
	// for itself.
	GatheredAt time.Time `json:"gathered_at"`
	Commit     string    `json:"commit,omitempty"`
	// ShippedDocumentationBytes is what the shipped documentation in this picture
	// adds up to, kept with it for the same reason and adopted with it.
	ShippedDocumentationBytes int `json:"shipped_documentation_bytes,omitempty"`
	// Replaces is when the picture the agent last received was gathered, and
	// ReplacesCommit the commit it was gathered against. The delivery tells the
	// role how far apart the two are, and a process that never held the old
	// picture has no other way to say. They are also what the record's picture
	// goes back to where this one's text is lost before it is delivered, because
	// the agent never received it.
	Replaces       time.Time `json:"replaces,omitempty"`
	ReplacesCommit string    `json:"replaces_commit,omitempty"`
	// What had moved when this picture was taken, which is what the role is told
	// it is reconciling. It is kept rather than measured again at delivery: the
	// two readings would be taken moments apart, and the second one would describe
	// a drift this picture never had.
	Commits           int    `json:"commits,omitempty"`
	TrackerChanges    int    `json:"tracker_changes,omitempty"`
	RepositoryProblem string `json:"repository_problem,omitempty"`
	TrackerProblem    string `json:"tracker_problem,omitempty"`
	// Trigger says who took it — the operator asking, or the harness because the
	// picture had fallen past the threshold — and Threshold is the number it had
	// fallen past. The role is framed differently by each, so a process that was
	// not there frames it the way the process that took it would have.
	Trigger   string `json:"trigger"`
	Threshold int    `json:"threshold,omitempty"`
}

// MaxPendingPictureBytes bounds the undelivered picture's text kept beside a
// conversation's record. It sits above what an assembled product context may be,
// because a picture larger than that is one no turn could carry anyway, and it
// is stated here rather than taken from the package that assembles the context:
// this package is below that one and the dependency may not run the other way.
// TestThePendingPictureBoundSitsAboveTheBundleItKeeps refuses any drift apart.
const MaxPendingPictureBytes = 3 << 20

// MaxPendingNotices bounds the account of harness activity one conversation
// carries forward. It matches the bound the conversation itself keeps, so what
// is written is always what was going to be delivered.
const MaxPendingNotices = 20

// MaxDeliveredAmendmentIDs bounds that record. It is far above any plausible
// backlog of undecided proposals, and it exists so a conversation's state file
// cannot grow without limit on a queue nobody works through.
const MaxDeliveredAmendmentIDs = 256

// MaxDeliveredReportIDs bounds the record of reports already carried into a
// turn, for the reason the amendment bound exists: a conversation's state file
// cannot be allowed to grow without limit on a pile nobody works through.
const MaxDeliveredReportIDs = 256

// MaxPendingTrackerResultBytes bounds the results a conversation may carry
// forward. The record has to stay reloadable, so what waits inside it is bounded
// well below the state file's own limit rather than growing with the tracker.
const MaxPendingTrackerResultBytes = 64 << 10

var conversationIDPattern = regexp.MustCompile(`^chat-[a-f0-9]{32}$`)

func NewConversationID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate conversation id: %w", err)
	}
	return "chat-" + hex.EncodeToString(bytes), nil
}

func (c Conversation) Validate() error {
	var problems []error
	if c.SchemaVersion != ConversationSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", ConversationSchemaVersion))
	}
	if !conversationIDPattern.MatchString(c.ConversationID) {
		problems = append(problems, errors.New("conversation_id is invalid"))
	}
	if err := domain.ValidateIdentifier("product id", string(c.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("repository id", c.RepositoryID); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("role", string(c.Role)); err != nil {
		problems = append(problems, err)
	}
	// The agent is optional only because records predate it; one that names an
	// agent must name a usable one, because the name is also a path.
	if c.Agent != "" {
		if err := domain.ValidateIdentifier("agent", c.Agent); err != nil {
			problems = append(problems, err)
		}
	}
	if !c.Backend.Valid() {
		problems = append(problems, errors.New("backend is invalid"))
	}
	if c.Turns < 0 {
		problems = append(problems, errors.New("turns cannot be negative"))
	}
	// A completed turn always knows which selector it asked for, so a recorded
	// turn without one would leave the conversation unauditable.
	if c.Turns > 0 && c.ProviderModel == "" {
		problems = append(problems, errors.New("a recorded turn requires the requested model selector"))
	}
	// The account, the configuration, and the build are absent from every record
	// written before they were carried, so what is checked is the shape of one
	// that is there rather than that it is there at all: a conversation recorded
	// by an older build must still load, and a record naming an account, a
	// configuration, or a revision nothing could have produced says less than one
	// naming none of them, because it reads as evidence.
	if c.AccountAlias != "" && !accountAliasPattern.MatchString(c.AccountAlias) {
		problems = append(problems, errors.New("account_alias is not an account alias"))
	}
	if c.ConfigRevision != "" && !configRevisionPattern.MatchString(c.ConfigRevision) {
		problems = append(problems, errors.New("config_revision is not a configuration revision"))
	}
	if c.Build != "" && !buildPattern.MatchString(c.Build) {
		problems = append(problems, errors.New("build is not a revision"))
	}
	if len(c.PendingTrackerResults) > MaxPendingTrackerResultBytes {
		problems = append(problems, fmt.Errorf("pending tracker results are %d bytes, limit is %d",
			len(c.PendingTrackerResults), MaxPendingTrackerResultBytes))
	}
	// A recorded refusal that says nothing about what was wrong is one nothing can
	// act on: the wakeup carries the harness's own words, and a record without
	// them would wake a role to correct something nobody stated.
	if c.RefusedBlock != nil {
		if strings.TrimSpace(c.RefusedBlock.Problem) == "" {
			problems = append(problems, errors.New("a recorded tracker refusal must carry what was wrong with the block"))
		}
		if len(c.RefusedBlock.Problem) > MaxTrackerRefusalProblemBytes {
			problems = append(problems, fmt.Errorf("the recorded tracker refusal is %d bytes, limit is %d",
				len(c.RefusedBlock.Problem), MaxTrackerRefusalProblemBytes))
		}
		if c.RefusedBlock.RefusedAt.IsZero() {
			problems = append(problems, errors.New("a recorded tracker refusal must say when it was refused"))
		}
	}
	if len(c.DeliveredAmendmentIDs) > MaxDeliveredAmendmentIDs {
		problems = append(problems, fmt.Errorf("%d delivered amendment ids are recorded, limit is %d",
			len(c.DeliveredAmendmentIDs), MaxDeliveredAmendmentIDs))
	}
	if len(c.DeliveredReportIDs) > MaxDeliveredReportIDs {
		problems = append(problems, fmt.Errorf("%d delivered report ids are recorded, limit is %d",
			len(c.DeliveredReportIDs), MaxDeliveredReportIDs))
	}
	if len(c.PendingProposals) > MaxPendingProposals {
		problems = append(problems, fmt.Errorf("%d undecided proposals are recorded, limit is %d",
			len(c.PendingProposals), MaxPendingProposals))
	}
	// An undecided proposal nobody can name is one nobody can decide, so the
	// identifier an approval has to say is required rather than merely usual.
	for i, proposal := range c.PendingProposals {
		if strings.TrimSpace(proposal.ID) == "" {
			problems = append(problems, fmt.Errorf("pending_proposals[%d] has no id", i))
		}
	}
	if len(c.PendingConcerns) > MaxPendingConcerns {
		problems = append(problems, fmt.Errorf("%d unanswered concerns are recorded, limit is %d",
			len(c.PendingConcerns), MaxPendingConcerns))
	}
	// The same for a question: one nobody can name is one nobody can answer.
	for i, concern := range c.PendingConcerns {
		if strings.TrimSpace(concern.ID) == "" {
			problems = append(problems, fmt.Errorf("pending_concerns[%d] has no id", i))
		}
	}
	if len(c.PendingWrites) > MaxPendingWrites {
		problems = append(problems, fmt.Errorf("%d undecided documents are recorded, limit is %d",
			len(c.PendingWrites), MaxPendingWrites))
	}
	for i, write := range c.PendingWrites {
		if write.Publication != nil {
			if err := write.Publication.Validate(); err != nil {
				problems = append(problems, fmt.Errorf("pending_writes[%d]: %w", i, err))
			}
			if write.Publication.WriteID != write.ID || write.Publication.ConversationID != c.ConversationID || write.Publication.Owner != c.Role || write.Publication.Candidate.Artifact.ID != write.Artifact || write.Publication.Turn != write.Turn {
				problems = append(problems, fmt.Errorf("pending_writes[%d] publication belongs to another document or conversation", i))
			}
		}
		// An undecided document nobody can name is one nobody can approve, and one
		// with nothing under it is an approval that would write an empty file.
		if strings.TrimSpace(write.ID) == "" {
			problems = append(problems, fmt.Errorf("pending_writes[%d] has no id", i))
		}
		if strings.TrimSpace(write.Body) == "" {
			problems = append(problems, fmt.Errorf("pending_writes[%d] carries no document", i))
		}
		if len(write.Body) > MaxPendingWriteBytes {
			problems = append(problems, fmt.Errorf("pending_writes[%d] is %d bytes, limit is %d",
				i, len(write.Body), MaxPendingWriteBytes))
		}
	}
	for id, returns := range c.DocumentReturns {
		if returns < 0 || returns > MaxDocumentReturns || strings.TrimSpace(id) == "" {
			problems = append(problems, fmt.Errorf("invalid count of returned runs for document %q", id))
		}
	}
	if len(c.PendingNotices) > MaxPendingNotices {
		problems = append(problems, fmt.Errorf("%d pending notices are recorded, limit is %d",
			len(c.PendingNotices), MaxPendingNotices))
	}
	// A picture is deliberately allowed to predate the conversation that carries
	// it: it is assembled before the record exists, and a refresh moves it
	// forward afterwards. What it may not do is claim to be from the future,
	// which would make every comparison against it read as fresher than it is.
	if !c.ContextGatheredAt.IsZero() && !c.UpdatedAt.IsZero() && c.ContextGatheredAt.After(c.UpdatedAt) {
		problems = append(problems, errors.New("context_gathered_at cannot be after updated_at"))
	}
	// A picture waiting to be delivered has to say when it was read and what read
	// it: the first is what is adopted when it lands, and the second is how the
	// role is told about it. A record carrying neither names a re-read nothing
	// could deliver, which is worse than carrying no pending picture at all.
	if c.PendingPicture != nil {
		if c.PendingPicture.GatheredAt.IsZero() {
			problems = append(problems, errors.New("a pending picture must say when it was gathered"))
		}
		if strings.TrimSpace(c.PendingPicture.Trigger) == "" {
			problems = append(problems, errors.New("a pending picture must say what took it"))
		}
	}
	if c.StartedAt.IsZero() || c.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("started_at and updated_at are required"))
	}
	if c.UpdatedAt.Before(c.StartedAt) {
		problems = append(problems, errors.New("updated_at cannot be before started_at"))
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid conversation state: %w", errors.Join(problems...))
	}
	return nil
}

// ConversationIdentity names one durable conversation: the agent that holds it
// and the role whose authority it carries. Both are needed and neither is
// enough. The agent decides which record this is, because two agents filling one
// role are two identities with two provider sessions; the role decides what the
// conversation may do, and is checked against the record so an agent whose
// configured role changed cannot silently carry on under its old authority.
type ConversationIdentity struct {
	Agent string
	Role  domain.AgentRole
}

func (i ConversationIdentity) String() string {
	if i.Agent == "" || i.Agent == string(i.Role) {
		return string(i.Role)
	}
	return i.Agent + " (" + string(i.Role) + ")"
}

// validate keeps an identity usable as a path before it is one.
func (i ConversationIdentity) validate() error {
	if err := domain.ValidateIdentifier("agent", i.Agent); err != nil {
		return err
	}
	return domain.ValidateIdentifier("role", string(i.Role))
}

// ConversationStore keeps conversations in the same operating-system state root
// as runs, beside them rather than among them. A conversation is stored under
// the agent it belongs to, so a restarted process finds the one conversation it
// should resume without searching for it. An agent named for its role — which is
// what every generated configuration produces — stores it exactly where the
// role-keyed layout this replaced put it, so no existing conversation moves.
type ConversationStore struct {
	root      string
	anchor    string
	productID domain.ProductID
	// queued is told, once per take, that a claim found the conversation held
	// and is waiting its turn. It is nil in every store the harness builds and
	// is a test's signal, so a test about queueing waits on the claim having
	// tried rather than on a length of time it hopes was long enough.
	queued func()
}

func NewConversationStore(root string, productID domain.ProductID) (*ConversationStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the conversation state root: %w", err)
	}
	return &ConversationStore{
		root:      filepath.Join(home.ProductDirectory(stateRoot, string(productID)), "conversations"),
		anchor:    anchor,
		productID: productID,
	}, nil
}

func (s *ConversationStore) Root() string {
	return s.root
}

// Hold takes exclusive ownership of an agent's conversation for as long as this
// process is talking to it. Two processes resuming one provider session would
// interleave their turns and overwrite each other's record of them. It is an
// advisory file lock, so a conversation whose holder exited unexpectedly is
// immediately available again. It is per agent rather than per role because two
// agents on one role hold two conversations, and a lease that stopped one of
// them while the other talked would be serializing sessions that never meet.
func (s *ConversationStore) Hold(identity ConversationIdentity) (*Lease, error) {
	return s.take(context.Background(), identity, refuseHeldConversation)
}

// waitingForConversation and refuseHeldConversation are what take does when
// somebody else is mid-turn: queue behind them, or say so and give up.
const (
	waitingForConversation = true
	refuseHeldConversation = false
)

// take acquires the lock on an agent's conversation and stamps this process as
// its holder. It is the one path onto that lock, so the stamp every surface
// reads is written wherever the lock is taken rather than only where it was
// first taken — a turn taken back at the prompt is as visible as the first one.
func (s *ConversationStore) take(ctx context.Context, identity ConversationIdentity, wait bool) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create conversation directory: %w", err)
	}
	path, err := s.leaseFile(identity)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open conversation lease: %w", err)
	}
	if wait {
		var holder conversationHolder
		var contended bool
		queued := func() {
			contended = true
			path, _ := s.holderFile(identity)
			holder, _ = s.readHolder(path, identity)
			if s.queued != nil {
				s.queued()
			}
		}
		if err := queueForStateFile(ctx, file, queued); err != nil {
			file.Close()
			if !contended || (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) {
				return nil, fmt.Errorf("take up the %s conversation: %w", identity, err)
			}
			path, _ := s.holderFile(identity)
			if latest, readErr := s.readHolder(path, identity); readErr == nil {
				holder = latest
			}
			return nil, &ConversationHeldError{Identity: identity, PID: holder.PID, HeldAt: holder.HeldAt, Cause: err}
		}
	} else {
		held, err := tryLockStateFile(file)
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("lock %s conversation: %w", identity, err)
		}
		if !held {
			file.Close()
			path, _ := s.holderFile(identity)
			holder, _ := s.readHolder(path, identity)
			return nil, &ConversationHeldError{Identity: identity, PID: holder.PID, HeldAt: holder.HeldAt}
		}
	}
	// The label names what is owned, so a release that failed says which
	// conversation is still held rather than leaving the caller to guess.
	lease := &Lease{label: fmt.Sprintf("%s conversation", identity), file: file}
	holder, err := s.holderFile(identity)
	if err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	// The stamp is written under the lock, so what a reader sees was written by
	// the process that actually owns the conversation. A hold that cannot be
	// stamped is refused rather than taken: what it would otherwise buy is a turn
	// that runs while every surface reports the machine idle, which is the one
	// answer the standing status exists to prevent.
	if err := s.stampHolder(holder); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	lease.holder = holder
	return lease, nil
}

// InFlight reports whether a process is holding an agent's conversation right
// now, and takes nothing to answer it. Taking the lease and dropping it again
// was the mechanism this replaces: for the instant it lasts it is
// indistinguishable from a second conversation, so a chat or a sink asking for
// its own conversation during a status reading was told another process had it
// — and the four-line status and the hourly heartbeat now ask often enough to
// hit that instant.
//
// What it reads instead is the stamp the holder wrote, which is checked against
// the process named in it rather than trusted. A holder killed outright leaves
// its stamp behind, and the answer has to match the operating system's, which
// dropped that holder's lock as it died.
//
// Two things it cannot see are worth stating. A stamp whose process identifier
// has been reused by an unrelated process reads as held until the next hold
// rewrites it, which is a conversation reported busy rather than a conversation
// anybody is locked out of. And a holder from a build older than the stamp
// wrote none, so its turn reads as free until it ends.
func (s *ConversationStore) InFlight(identity ConversationIdentity) (bool, error) {
	path, err := s.holderFile(identity)
	if err != nil {
		return false, err
	}
	holder, err := s.readHolder(path, identity)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	running, err := processIsRunning(holder.PID)
	if err != nil {
		return false, fmt.Errorf("ask whether the %s conversation's holder is running: %w", identity, err)
	}
	return running, nil
}

// HeldBy names the agents whose conversations one process is holding right now,
// in name order, read from the stamps each holder wrote and taking nothing. It
// is what the supervisor asks before it restarts a part into a deployed build:
// a Slack sink answering the product manager holds that conversation for the
// turn, and a restart then would cut the turn off in the middle.
//
// A stamp that cannot be read is passed over rather than failing the answer: a
// stamp is replaced by rename, so an unreadable one is a file nobody wrote as a
// holder, and the question is only ever about the one process named.
func (s *ConversationStore) HeldBy(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, nil
	}
	paths, err := filepath.Glob(filepath.Join(s.root, "*.holder"))
	if err != nil {
		return nil, fmt.Errorf("list conversation holders: %w", err)
	}
	var agents []string
	for _, path := range paths {
		agent := strings.TrimSuffix(filepath.Base(path), ".holder")
		holder, err := s.readHolder(path, ConversationIdentity{Agent: agent})
		if err != nil || holder.PID != pid {
			continue
		}
		agents = append(agents, agent)
	}
	if len(agents) == 0 {
		return nil, nil
	}
	running, err := processIsRunning(pid)
	if err != nil {
		return nil, fmt.Errorf("ask whether pid %d is running: %w", pid, err)
	}
	if !running {
		return nil, nil
	}
	sort.Strings(agents)
	return agents, nil
}

// conversationHolder is what a process holding a conversation writes down beside
// the lease so the hold can be observed without being taken.
type conversationHolder struct {
	// PID is the process that took the hold. It is what makes the stamp
	// self-correcting: a holder that exits without releasing leaves the file
	// behind, and a reader that finds no such process reports what the operating
	// system already decided when it dropped the lock.
	PID int `json:"pid"`
	// HeldAt is when the hold was taken. Nothing decides from it — how long a turn
	// has been going is read from the conversation record — and it is written so
	// that a state directory somebody is reading by hand says when.
	HeldAt time.Time `json:"held_at"`
}

// stampHolder writes this process's stamp for a conversation it now holds. It is
// replaced by rename rather than written in place, so a reader sees the whole of
// one stamp or none of it and never half of one.
func (s *ConversationStore) stampHolder(path string) error {
	temporary, err := os.CreateTemp(s.root, ".holder-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary conversation holder: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary conversation holder: %w", err)
	}
	holder := conversationHolder{PID: os.Getpid(), HeldAt: time.Now().UTC()}
	if err := writeJSONFile(temporary, "conversation holder", holder); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary conversation holder: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace conversation holder: %w", err)
	}
	return syncDirectory(s.root)
}

// readHolder is one stamp as it sits on disk. A file that is not there is
// reported as ErrNotExist for the caller to read as an unheld conversation; a
// file that is there and will not decode is a failure to answer, because a
// reader that guessed at it would be inventing whether somebody is mid-turn.
//
// That is why this is the strict door and stays there while the listings beside
// it move to the tolerant one. A stamp carrying something this build does not
// know is a stamp a different build wrote, and what the field might say is
// exactly what this has to decide from; the caller reports the refusal rather
// than being handed an answer about who is talking to the agent.
func (s *ConversationStore) readHolder(path string, identity ConversationIdentity) (conversationHolder, error) {
	file, err := os.Open(path)
	if err != nil {
		return conversationHolder{}, fmt.Errorf("open the %s conversation holder: %w", identity, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var holder conversationHolder
	if err := decoder.Decode(&holder); err != nil {
		return conversationHolder{}, fmt.Errorf("decode the %s conversation holder: %w", identity, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return conversationHolder{}, fmt.Errorf("decode the %s conversation holder: %w", identity, err)
	}
	if holder.PID <= 0 {
		return conversationHolder{}, fmt.Errorf("the %s conversation holder names no process", identity)
	}
	return holder, nil
}

// ConversationHold is a claim on an agent's conversation that its holder may
// put down and take up again. The plain lease above is right for a process that
// owns a conversation from end to end, and wrong for the operator's console:
// that process spends nearly all of its life waiting at a prompt, and while it
// waits nobody is talking to the agent at all. Holding throughout made an idle
// window the reason the harness and the operator's own assistant could not
// reach the product manager until the operator closed it.
//
// What has to be exclusive is a turn rather than a session — two processes
// resuming one provider session would interleave their turns and overwrite each
// other's record of them — so that is the span this is held for. It is the same
// advisory file lock underneath, so a holder that exits unexpectedly still
// leaves the conversation immediately available.
type ConversationHold struct {
	store    *ConversationStore
	identity ConversationIdentity
	// lease is nil exactly while the conversation is put down.
	lease *Lease
}

// Claim takes an agent's conversation and returns the hold that can put it down
// and take it up again. It queues behind whoever is mid-turn rather than
// refusing: what is exclusive is a turn, and a turn is something that ends on
// its own, so a caller told to go away would be told to come back for something
// that was over by the time it was said. That is also what the supervision
// design settles for conversation concurrency — one conversation serializes its
// turns and exposes queueing rather than interleaving. A caller that would
// rather not wait cancels its context, and a caller that wants the question
// answered without taking anything asks InFlight.
func (s *ConversationStore) Claim(ctx context.Context, identity ConversationIdentity) (*ConversationHold, error) {
	lease, err := s.take(ctx, identity, waitingForConversation)
	if err != nil {
		return nil, err
	}
	return &ConversationHold{store: s, identity: identity, lease: lease}, nil
}

// TryClaim is Claim for a caller that has something better to do than wait. It
// refuses with ErrConversationHeld while somebody is mid-turn, which is what a
// background delivery wants: nothing has been asked of the agent yet, so the
// attempt is given back and a later pass makes it rather than the delivery
// holding its lease and its budget open for the length of somebody else's turn.
func (s *ConversationStore) TryClaim(identity ConversationIdentity) (*ConversationHold, error) {
	lease, err := s.Hold(identity)
	if err != nil {
		return nil, err
	}
	return &ConversationHold{store: s, identity: identity, lease: lease}, nil
}

// Release puts the conversation down. Releasing one that is already down is a
// no-op, and it has to be: the process that owns a conversation defers this for
// the life of the command while the conversation is put down and taken up many
// times underneath, so the deferred release routinely runs against a hold that
// is already down — every conversation that ends at the prompt is one.
//
// The absent lease is answered here rather than left to the lease's own
// nil-receiver guard. That guard makes this safe today, and a hold that depends
// on how a type it merely refers to handles being nil is one sentence away from
// not being safe tomorrow.
func (h *ConversationHold) Release() error {
	if h == nil || h.lease == nil {
		return nil
	}
	lease := h.lease
	h.lease = nil
	return lease.Release()
}

// Held reports whether this process currently has the conversation.
func (h *ConversationHold) Held() bool {
	return h != nil && h.lease != nil
}

// Retake takes the conversation up again, waiting for whoever has it rather
// than refusing — the same queueing the first claim does, for the same reason:
// the operator has already typed, there is nothing else for them to do with
// what they said, and another process's turn ends on its own. Taking up a
// conversation this process already has is a no-op.
func (h *ConversationHold) Retake(ctx context.Context) error {
	if h == nil {
		return errors.New("there is no conversation hold to take up")
	}
	if h.lease != nil {
		return nil
	}
	lease, err := h.store.take(ctx, h.identity, waitingForConversation)
	if err != nil {
		return err
	}
	h.lease = lease
	return nil
}

// Load returns the conversation recorded for an agent, reporting
// ErrNoConversation when there is none to resume. It is the strict door: the
// agent resuming the conversation writes the record back at the end of its turn.
func (s *ConversationStore) Load(identity ConversationIdentity) (Conversation, error) {
	return s.load(identity, false)
}

// Read returns the conversation recorded for an agent to a reader that will not
// write it back — a listing of the agents, a question about which conversation
// is being held — and is the tolerant door: a field this build does not know is
// stepped over and named once rather than refusing the whole record, for the
// reason Recorded does. Anything that resumes the conversation goes through
// Load.
func (s *ConversationStore) Read(identity ConversationIdentity) (Conversation, error) {
	return s.load(identity, true)
}

func (s *ConversationStore) load(identity ConversationIdentity, tolerateUnknownFields bool) (Conversation, error) {
	role := identity.Role
	path, err := s.statePathFor(identity)
	if err != nil {
		return Conversation{}, err
	}
	conversation, err := s.decode(path, string(role), tolerateUnknownFields)
	if err != nil {
		return Conversation{}, err
	}
	if conversation.Role != role {
		return Conversation{}, fmt.Errorf("conversation state for %s belongs to role %s", identity, conversation.Role)
	}
	// A record that names its agent must be the agent that was asked for. It
	// cannot be another one under the default layout, where the file is named for
	// the agent, and it is checked anyway: a record whose agent and file disagree
	// is a record somebody moved, and resuming it would put one agent's session
	// behind another's persona.
	if conversation.Agent != "" && conversation.Agent != identity.Agent {
		return Conversation{}, fmt.Errorf("conversation state for %s belongs to agent %s", identity, conversation.Agent)
	}
	return conversation, nil
}

// decode is one conversation record as it sits on disk, checked as far as the
// record itself can be checked. Who it belongs to is the caller's question: an
// agent resuming its own conversation and a reader listing every conversation
// there is ask it differently, and neither can answer it from the file alone.
// The label names the record in a failure, because a reader that cannot say
// which file would not decode has been told nothing useful.
//
// Strictly is the door for an agent resuming its conversation, which writes the
// record back at the end of its turn: a field stepped over on the way in is a
// field lost on the way out. Tolerating is the door for Read and Recorded, whose
// readers notice and never write back, and a reader that refused the whole
// record would report nothing at all about it.
func (s *ConversationStore) decode(path, label string, tolerateUnknownFields bool) (Conversation, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Conversation{}, ErrNoConversation
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("open conversation state: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Conversation{}, fmt.Errorf("stat conversation state: %w", err)
	}
	if info.Size() > maxEncodedStateBytes {
		return Conversation{}, fmt.Errorf("conversation state for %s is %d bytes, limit is %d", label, info.Size(), maxEncodedStateBytes)
	}
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return Conversation{}, fmt.Errorf("read conversation state for %s: %w", label, err)
	}
	var conversation Conversation
	if tolerateUnknownFields {
		unknown, err := decodeTolerating(encoded, &conversation)
		if err != nil {
			return Conversation{}, fmt.Errorf("decode conversation state for %s: %w", label, err)
		}
		noteUnknownFields("conversation record", unknown)
	} else if err := decodeStrictly(encoded, &conversation); err != nil {
		return Conversation{}, fmt.Errorf("decode conversation state for %s: %w", label, err)
	}
	if err := s.validateConversation(conversation); err != nil {
		return Conversation{}, err
	}
	return conversation, nil
}

// Recorded lists the conversation this product holds a record of for each of
// its agents. It is for a reader that has to notice change rather than act on
// it — the reporting sink is the first — and it decides nothing about what it
// reads: a conversation a live process is holding is listed exactly as an idle
// one is.
//
// What it lists is one conversation per agent, because that is what the records
// name. An agent whose conversation was replaced keeps the replaced one's event
// log on disk, and nothing here points at it any more: a reader that was away
// while a conversation was replaced misses whatever it had not already read of
// it. That is the same trade the watermark makes — the durable records are
// authoritative and this is a view of them — and it is stated rather than hidden
// because a gap somebody does not know about is worse than one they do.
func (s *ConversationStore) Recorded() ([]Conversation, error) {
	conversations, _, err := s.recorded(false)
	return conversations, err
}

// RecordedReadable lists what Recorded does, reading past a record that will
// not decode and returning it beside the listing, for the reason
// Store.RecordedReadable does. The record is named by its file, because a
// record that will not decode cannot say which conversation it was.
func (s *ConversationStore) RecordedReadable() ([]Conversation, []Unreadable, error) {
	return s.recorded(true)
}

func (s *ConversationStore) recorded(readPast bool) ([]Conversation, []Unreadable, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read conversation directory: %w", err)
	}
	var unreadable []Unreadable
	conversations := make([]Conversation, 0, len(entries))
	for _, entry := range entries {
		// The leases, the event logs, and the temporary files of a save in flight
		// all live in this directory; only a file named for an agent holds a
		// conversation.
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		conversation, err := s.decode(filepath.Join(s.root, entry.Name()), entry.Name(), true)
		if err != nil && readPast {
			unreadable = append(unreadable, Unreadable{Record: entry.Name(), Err: err})
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("discover recorded conversations: %w", err)
		}
		conversations = append(conversations, conversation)
	}
	sort.Slice(conversations, func(i, j int) bool {
		return conversations[i].ConversationID < conversations[j].ConversationID
	})
	return conversations, unreadable, nil
}

// ClaimRefusalWakeup marks a conversation's outstanding tracker refusal as one
// the harness has started a turn for, and returns the refusal it claimed.
//
// It is the claim rather than the wakeup, and the order is the order the
// guarantee needs: the record says a turn was started before one is, so a
// process that dies between the two has recorded a wakeup nobody made rather
// than made one nobody recorded. The second is what would fire again on the next
// pass, and again on the one after, which is the looping the refusal is escalated
// rather than repeated to avoid.
//
// It is taken under the conversation's own lease, so the claim and the turn that
// follows it cannot be interleaved with somebody else's turn writing the record
// back without it. A conversation somebody is mid-turn with refuses with
// ErrConversationHeld rather than waiting: nothing has been asked of the role
// yet, so a later pass makes the wakeup rather than this one holding a lease open
// for the length of another turn — and a role that is mid-turn is one whose
// refusal may be about to be answered anyway.
func (s *ConversationStore) ClaimRefusalWakeup(identity ConversationIdentity, at time.Time) (TrackerRefusal, error) {
	hold, err := s.TryClaim(identity)
	if err != nil {
		return TrackerRefusal{}, err
	}
	defer hold.Release()
	// Read again under the lease. What was listed is what the record said before
	// the claim, and the answer that matters is what it says now.
	conversation, err := s.Load(identity)
	if err != nil {
		return TrackerRefusal{}, err
	}
	if conversation.RefusedBlock == nil || !conversation.RefusedBlock.AwaitingWakeup(at) {
		return TrackerRefusal{}, fmt.Errorf("%w: %s", ErrNoRefusalAwaitingWakeup, identity)
	}
	claimed := *conversation.RefusedBlock
	claimed.WokenAt = at.UTC()
	claimed.LastAttemptAt = at.UTC()
	claimed.Attempts++
	conversation.RefusedBlock = &claimed
	// The record moved, so it says when — but only forward. A claim taken against
	// a clock behind the conversation's own last turn must not rewrite the record
	// as older than the turn that wrote it.
	if claimed.WokenAt.After(conversation.UpdatedAt) {
		conversation.UpdatedAt = claimed.WokenAt
	}
	if err := s.Save(conversation); err != nil {
		return TrackerRefusal{}, err
	}
	return claimed, nil
}

// WithdrawRefusalWakeup gives back the turn a wakeup never took, so the refusal
// is owed one again and a later pass makes it.
//
// What comes back is the turn and not the attempt. That distinction is the whole
// of the bound: the attempt stands and counts against MaxRefusalWakeups, and the
// moment of it stands and paces the next one, so a provider that goes on refusing
// is tried a fixed number of times over a known span and then left. Giving the
// attempt back as well would make the bound unreachable — the count would never
// pass one — and turn "retried while the window lasts" into "retried forever",
// which is the loop this whole record exists to avoid.
//
// It is spent on the endings where no model ever saw the message: the provider
// refusing the turn for want of capacity, and the provider answering nobody at
// all — an outage, or an account whose login has lapsed. Every other failure
// keeps its turn spent, because a turn that may have reached the role is one this
// cannot claim did not. why is what stopped the wakeup, in the caller's words.
//
// The give-back that would leave the refusal with no attempts left does not give
// the turn back. It hands the refusal to the operator instead, recording as
// Escalated why the harness stopped and writing the same news onto the
// conversation's own log as an unresolved refusal, so the exhaustion is said
// where every other unanswered refusal is said rather than inferred from a
// wakeup that quietly never comes. A refusal left owed a turn with none to spend
// on it would read as waiting on the harness when it is waiting on a person.
//
// It returns the refusal as the record now holds it, so the caller can say
// whether the wakeup will be made again or the operator has it.
//
// The turn it names is checked against the record, because the refusal it was
// claimed for may have been answered and replaced while the failed wakeup was
// being reported: giving back a wakeup for a refusal that no longer exists would
// re-open one somebody has already dealt with. A conversation with nothing to
// give back is not a failure — the refusal moved on under it, which is the record
// saying the wakeup is no longer owed — and it returns the zero refusal.
func (s *ConversationStore) WithdrawRefusalWakeup(ctx context.Context, identity ConversationIdentity, turn int, why string) (TrackerRefusal, error) {
	hold, err := s.Claim(ctx, identity)
	if err != nil {
		return TrackerRefusal{}, err
	}
	defer hold.Release()
	conversation, err := s.Load(identity)
	if err != nil {
		return TrackerRefusal{}, err
	}
	if conversation.RefusedBlock == nil ||
		conversation.RefusedBlock.Turn != turn ||
		conversation.RefusedBlock.WokenAt.IsZero() ||
		conversation.RefusedBlock.Escalated != "" {
		return TrackerRefusal{}, nil
	}
	given := *conversation.RefusedBlock
	given.WokenAt = time.Time{}
	if given.Attempts >= MaxRefusalWakeups {
		given.Escalated = exhaustedRefusalWakeups(given)
		if err := s.appendExhaustedRefusal(&conversation, given, why); err != nil {
			return TrackerRefusal{}, err
		}
	}
	conversation.RefusedBlock = &given
	if err := s.Save(conversation); err != nil {
		return TrackerRefusal{}, err
	}
	return given, nil
}

// exhaustedRefusalWakeups is why a refusal whose every wakeup reached nobody is
// the operator's now, in the words the record keeps.
func exhaustedRefusalWakeups(refusal TrackerRefusal) string {
	return fmt.Sprintf("the harness tried %d times to wake this conversation to correct the refusal of turn %d and the provider never took the turn",
		refusal.Attempts, refusal.Turn)
}

// appendExhaustedRefusal writes the exhaustion onto the conversation's own log as
// an unresolved refusal, which is the record every surface already says an
// unanswered refusal from. It is stamped with the last attempt, which is the
// moment the harness stopped trying.
//
// The payload is the one the conversation writes for the other two unresolved
// endings, with the attempts beside it: nothing was refused again and the harness
// never reached the role, and a reader told those apart from a role that answered
// badly is a reader who knows the fix is signing in rather than rewording.
func (s *ConversationStore) appendExhaustedRefusal(conversation *Conversation, refusal TrackerRefusal, why string) error {
	event, err := execution.NewEvent(conversation.ConversationID, conversation.LastSequence+1, refusal.LastAttemptAt,
		execution.EventTrackerRefusalUnresolved, "harness.correction", map[string]any{
			"turn":          refusal.Turn,
			"role":          string(conversation.Role),
			"actions":       refusal.Actions,
			"problem":       refusal.Problem,
			"woken":         false,
			"refused_again": false,
			"attempts":      refusal.Attempts,
			"never_taken":   boundRecordedText(why, MaxTrackerRefusalProblemBytes, truncatedNote(MaxTrackerRefusalProblemBytes)),
		})
	if err != nil {
		return err
	}
	if err := s.AppendEvent(event); err != nil {
		return fmt.Errorf("record that the wakeups for the refusal of turn %d are spent: %w", refusal.Turn, err)
	}
	conversation.LastSequence = event.Sequence
	return nil
}

// Save replaces a role's conversation record atomically. Unlike a run, a
// conversation is created and updated through the same call: every turn
// rewrites the same record, and the first turn is not a special case.
func (s *ConversationStore) Save(conversation Conversation) error {
	if err := s.validateConversation(conversation); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create conversation directory: %w", err)
	}
	path, err := s.statePathFor(conversation.Identity())
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.root, ".conversation-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary conversation state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary conversation state: %w", err)
	}
	if err := writeJSONFile(temporary, "conversation state", conversation); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary conversation state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace conversation state: %w", err)
	}
	return syncDirectory(s.root)
}

// SavePendingPictureText writes the text of the picture a refresh has taken and
// no turn has delivered, beside the conversation it belongs to. It is replaced
// by rename like every other record here, so a reader sees the whole of one
// picture or none of it.
//
// It is a file of its own rather than a field because of its size: a record has
// a megabyte to live in and the picture is nearly that by itself, so a record
// carrying it would be one the store refused to save — which is the conversation
// unable to record anything at all, in exchange for a re-read.
//
// The record beside it says which picture this is, and that record is what
// decides whether it is read: text left behind by a conversation nothing is
// pending for is never delivered, and is replaced by the next refresh.
func (s *ConversationStore) SavePendingPictureText(identity ConversationIdentity, text string) error {
	if len(text) > MaxPendingPictureBytes {
		return fmt.Errorf("the picture waiting for the %s is %d bytes, limit is %d", identity, len(text), MaxPendingPictureBytes)
	}
	path, err := s.pendingPictureFile(identity)
	if err != nil {
		return err
	}
	return s.writePictureText(path, text)
}

// SaveDeliveredPictureText writes the text of the picture the agent last
// received, beside the conversation it belongs to. It is what a later refresh is
// compared against so that the turn delivering it carries what moved rather than
// the whole picture again: on 2026-09-23 the development manager's session held
// some twenty whole pictures of about a megabyte each and every failing turn
// added another, until the session no longer fit in a request. It is bounded and
// replaced exactly as the pending picture's text is.
func (s *ConversationStore) SaveDeliveredPictureText(identity ConversationIdentity, text string) error {
	if len(text) > MaxPendingPictureBytes {
		return fmt.Errorf("the picture delivered to the %s is %d bytes, limit is %d", identity, len(text), MaxPendingPictureBytes)
	}
	path, err := s.deliveredPictureFile(identity)
	if err != nil {
		return err
	}
	return s.writePictureText(path, text)
}

// DeliveredPictureText is the text of the picture the agent last received. A
// conversation with none kept — one recorded before it was, or one whose first
// turn has not landed — has nothing to compare a refresh against, which is the
// ordinary case for such a conversation and not a failure.
func (s *ConversationStore) DeliveredPictureText(identity ConversationIdentity) (string, error) {
	path, err := s.deliveredPictureFile(identity)
	if err != nil {
		return "", err
	}
	return readPictureText(path, "the picture delivered to the "+identity.String())
}

// ClearDeliveredPictureText removes the text of the picture a conversation last
// delivered, which a new conversation for the same agent has never received.
func (s *ConversationStore) ClearDeliveredPictureText(identity ConversationIdentity) error {
	path, err := s.deliveredPictureFile(identity)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the picture delivered to the %s: %w", identity, err)
	}
	return nil
}

// writePictureText replaces one picture's text by rename, so a reader sees the
// whole of one picture or none of it.
func (s *ConversationStore) writePictureText(path, text string) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create conversation directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".picture-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary pending picture: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("secure temporary pending picture: %w", err), temporary.Close())
	}
	if _, err := temporary.WriteString(text); err != nil {
		return errors.Join(fmt.Errorf("write pending picture: %w", err), temporary.Close())
	}
	if err := temporary.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync pending picture: %w", err), temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary pending picture: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace pending picture: %w", err)
	}
	return syncDirectory(s.root)
}

// PendingPictureText is the text of the picture waiting to be delivered to an
// agent. A conversation with none recorded has nothing waiting, which is the
// ordinary case and not a failure.
func (s *ConversationStore) PendingPictureText(identity ConversationIdentity) (string, error) {
	path, err := s.pendingPictureFile(identity)
	if err != nil {
		return "", err
	}
	return readPictureText(path, "the picture waiting for the "+identity.String())
}

// readPictureText reads one picture's text back, bounded as it was written. A
// picture that is not there reads as none.
func readPictureText(path, what string) (string, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open %s: %w", what, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", what, err)
	}
	if info.Size() > MaxPendingPictureBytes {
		return "", fmt.Errorf("%s is %d bytes, limit is %d", what, info.Size(), MaxPendingPictureBytes)
	}
	text, err := io.ReadAll(io.LimitReader(file, MaxPendingPictureBytes))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", what, err)
	}
	return string(text), nil
}

// ClearPendingPictureText removes the text of a picture that has been delivered
// or abandoned. A conversation with none is already clear.
func (s *ConversationStore) ClearPendingPictureText(identity ConversationIdentity) error {
	path, err := s.pendingPictureFile(identity)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the picture waiting for the %s: %w", identity, err)
	}
	return nil
}

// AppendEvent persists one normalized event from a conversation. The log is
// named for the conversation rather than the role, so starting a new
// conversation never appends to the record of the one it replaced.
func (s *ConversationStore) AppendEvent(event execution.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	path, err := s.eventPathForConversation(event.RunID)
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
		return fmt.Errorf("create conversation directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open conversation event log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append conversation event: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append conversation event: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync conversation event log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close conversation event log: %w", err)
	}
	return nil
}

// LoadEvents returns one conversation's normalized events in the order they
// were recorded. A line that will not decode fails the read: what is rebuilt
// from this log is what a provider is told the conversation has said, and a
// rebuild over a listing that quietly dropped a turn would be a conversation
// told to itself with a hole in it. The sink reads past such a line by position
// with ScanEvents, and says so.
func (s *ConversationStore) LoadEvents(conversationID string) ([]execution.Event, error) {
	events, skipped, err := s.ScanEvents(conversationID)
	if err != nil {
		return nil, err
	}
	if err := firstSkipped("conversation event log for "+conversationID, skipped); err != nil {
		return nil, err
	}
	return events, nil
}

// ScanEvents returns every event of one conversation that decoded, in the order
// it was recorded, and beside them the lines that would not, each at the
// position it holds among the records. It is the read a positional cursor is
// kept against, so one bad line costs the reader that line and nothing behind
// it.
func (s *ConversationStore) ScanEvents(conversationID string) ([]execution.Event, []SkippedLine, error) {
	path, err := s.eventPathForConversation(conversationID)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open conversation event log: %w", err)
	}
	defer file.Close()

	var events []execution.Event
	skipped, err := scanLog(file, maxEncodedEventBytes, func(line []byte) error {
		event, err := execution.DecodeEvent(line)
		if err != nil {
			return err
		}
		if event.RunID != conversationID {
			return fmt.Errorf("event belongs to %s", event.RunID)
		}
		events = append(events, event)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read conversation event log: %w", err)
	}
	return events, skipped, nil
}

// LoggedConversations names every conversation this store holds an event log
// for, in order. It is wider than the records: a conversation replaced by --new
// leaves its log behind with nothing pointing at it, and an audit of what the
// logs say has to read those too.
func (s *ConversationStore) LoggedConversations() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list conversation event logs: %w", err)
	}
	var ids []string
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".events.jsonl")
		if !ok || entry.IsDir() || !conversationIDPattern.MatchString(id) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// identity is where a record belongs. A record written before the agent was part
// of the identity has none, and it belongs where it has always been: under the
// agent named for its role, which is the only agent that could have written it.
// Identity names the conversation this record is: the agent that holds it and
// the role whose authority it carries. It is exported because a reader that
// listed the records — the wakeup a refused tracker block is owed is the first —
// has the record and needs the identity every other call here is keyed by.
//
// A record written before the agent was part of the identity names none, and such
// a record was necessarily written for the agent named after its role, because
// nothing else could have addressed it.
func (c Conversation) Identity() ConversationIdentity {
	agent := c.Agent
	if agent == "" {
		agent = string(c.Role)
	}
	return ConversationIdentity{Agent: agent, Role: c.Role}
}

func (s *ConversationStore) validateConversation(conversation Conversation) error {
	if conversation.ProductID != s.productID {
		return fmt.Errorf("conversation product %q does not match store product %q", conversation.ProductID, s.productID)
	}
	return conversation.Validate()
}

// statePathFor names the one file an agent's conversation lives in. The agent is
// validated as an identifier before it reaches a path, so a configured agent
// name can never escape the conversation directory.
func (s *ConversationStore) statePathFor(identity ConversationIdentity) (string, error) {
	if err := identity.validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.root, identity.Agent+".json"), nil
}

func (s *ConversationStore) eventPathForConversation(conversationID string) (string, error) {
	if !conversationIDPattern.MatchString(conversationID) {
		return "", errors.New("conversation id is invalid")
	}
	return filepath.Join(s.root, conversationID+".events.jsonl"), nil
}

func (s *ConversationStore) leaseFile(identity ConversationIdentity) (string, error) {
	if err := identity.validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.root, identity.Agent+".lease"), nil
}

// holderFile names the stamp beside an agent's lease. It is not a `.json` file,
// so what Recorded lists stays the conversations themselves.
func (s *ConversationStore) holderFile(identity ConversationIdentity) (string, error) {
	if err := identity.validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.root, identity.Agent+".holder"), nil
}

// pendingPictureFile names the undelivered picture's text beside an agent's
// record. Like the holder above it is not a `.json` file, so what Recorded lists
// stays the conversations themselves.
func (s *ConversationStore) pendingPictureFile(identity ConversationIdentity) (string, error) {
	if err := identity.validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.root, identity.Agent+".picture"), nil
}

func (s *ConversationStore) deliveredPictureFile(identity ConversationIdentity) (string, error) {
	if err := identity.validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.root, identity.Agent+".delivered"), nil
}
