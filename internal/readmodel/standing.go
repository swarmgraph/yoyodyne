// Package readmodel is the one server-side derivation the operator surfaces
// project. Nothing here renders for a particular surface and nothing here
// writes: it reads the durable records the harness already keeps and says what
// they mean, so that a number the CLI reports and the same number a channel
// reports cannot disagree.
//
// What it answers first is the standing question — where does the harness stand
// right now — in the four lines the operator ratified:
//
//	Running        the developer runs in flight, each with item, phase, elapsed, spend
//	Working        the persona conversations with a turn in flight
//	Not startable  each admitted item nothing will pull, with the refusal that stops it
//	Needs a human  what is waiting on a person, and whose move it is
//
// The lines are a contract rather than a layout. Every one of them is printed
// on every reading, because the failure they exist to end is silence: an
// operator who sees nothing cannot tell a quiet machine from a dead one, and
// four lines that are sometimes absent are four lines nobody can rely on. A
// line with nothing in it says "nothing", and a line whose source could not be
// read says that instead — never "nothing", which would be the confident
// emptiness every report in this harness is written to avoid.
//
// There is no fifth line and no residual category. Work that is admitted, has a
// free slot, and is held back by nothing is startable and is about to be
// started, so it is named in the not-startable line's own count of the backlog
// rather than in a bucket for whatever was left over: a residual category is
// where the states nobody thought about go to be invisible.
//
// # Where the answers come from
//
// Each line reads the truest record for it and nothing else. The runs come from
// durable run state, the conversations from the conversation records and the
// advisory hold that is the only thing that actually knows a turn is in flight
// — observed rather than taken, so that reading the status never costs the
// operator the conversation it describes — the refusals from the queue's own
// account of what holds each entry back, and the attention conditions from the
// switches, directives, proposals, and unsettled runs the harness records as it
// goes. Nothing is read from a session's memory of what it has already tried:
// that is a fact about one process rather than about the product, and an item
// passed over because a watch session remembers failing at it is an item this
// says nothing about.
package readmodel

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/developerslot"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// backlogStatuses are the tracker slices the admitted work is assembled from.
// They are the queue's own, because the queue this describes has to be the queue
// that is actually pulled from.
var backlogStatuses = backlog.AdmittedStatuses()

// maxRefusalBytes bounds one refusal as this renders it. A parking reason and a
// directive are prose somebody wrote at whatever length they wanted, and this is
// a status line.
//
// It binds every surface, and the strictest requirement on it is not this one's.
// A terminal prints a refusal when somebody asks for it; the channel heartbeat
// says the same words again every hour while a state stands, which is a line
// that has to stay a line or become the thing people mute. Raising this would
// lengthen that message too, so it is raised for both surfaces or for neither.
const maxRefusalBytes = 160

// Runs is the durable run state the standing status reads. It reads and never
// adopts: what is in flight and what is unsettled are both questions about runs
// other processes own, and answering them is not acting on one.
//
// Recorded is every run, and it is read for the one question the other two
// listings cannot answer: which promotions the forge has not published. A run
// settled after a dropped merge owes no step and is not in flight, and its
// publication is still not on the remote.
//
// It is satisfied by *runstate.Store.
type Runs interface {
	Incomplete() ([]runstate.State, error)
	Outstanding() ([]runstate.State, error)
	Recorded() ([]runstate.State, error)
	Price(workItemID string) (runstate.ItemPrice, error)
}

// RunPresence is whether a process can be found behind a run in flight, asked
// without taking anything. It is an optional capability of Runs rather than a
// member of it: every store the harness wires satisfies it, and a reading over
// runs that cannot answer it reads every run as having a process, which is what
// every reading did before it could be asked. It is satisfied by
// *runstate.Store.
type RunPresence interface {
	Presence(state runstate.State, quiet time.Duration, now time.Time) (runstate.RunPresence, error)
}

// RunHolder observes a lease's process even after the run's record went
// terminal, while landing checks may still be running. Every wired store
// satisfies it; readings over fixtures may omit it.
type RunHolder interface {
	Held(runID string) (bool, error)
}

// Conversations is the durable conversation state, and the observation that
// says whether a turn is in flight. Both are needed and neither is enough: the
// record says what the conversation is and how many turns it has had, and only
// the hold says whether one is happening right now.
//
// The hold is observed rather than taken. A reading of the standing status must
// leave every conversation exactly as free as it found it, because this is
// asked of every conversation on every reading and hourly by the heartbeat, and
// a probe that acquired would be refusing the operator their own chat for the
// instant it held.
//
// It is satisfied by *runstate.ConversationStore.
type Conversations interface {
	Recorded() ([]runstate.Conversation, error)
	InFlight(runstate.ConversationIdentity) (bool, error)
}

// Tracker is the admitted work and the tracker's own account of what can be
// pulled. Readiness is asked for rather than inferred, for the reason the
// backlog states: a listing carries dependencies without carrying whether they
// are finished.
//
// It is satisfied by beads.Client.
type Tracker interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
	Ready(ctx context.Context) ([]beads.WorkItem, error)
}

// Directives is what the operator has told the harness, read for the ones
// nobody has settled. It is satisfied by *runstate.DirectiveStore.
type Directives interface {
	List() ([]directive.Directive, error)
}

// Amendments is the changes proposed to documents their proposer does not own,
// read for the ones nobody has decided. It is satisfied by
// *runstate.AmendmentStore.
type Amendments interface {
	List() ([]amendment.Record, error)
}

// OperatorHolds is the switch over everything the harness would spend on a
// provider. It is satisfied by *runstate.OperatorHoldStore.
type OperatorHolds interface {
	Held() (runstate.OperatorHold, bool, error)
}

// IntakeHolds is the switch over the work the harness chooses for itself. It is
// satisfied by *runstate.IntakeHoldStore.
type IntakeHolds interface {
	Held() (runstate.IntakeHold, bool, error)
}

// Sessions is what the sessions that choose work have said about themselves. It
// is satisfied by *runstate.WatchStore.
type Sessions interface {
	List() ([]runstate.WatchTransition, error)
}

// Reports is the pile every role files what it noticed into, and what became of
// the ones somebody has decided. It is read for one derived fact — how deep the
// pile is and how old the oldest undecided report in it is — because that is the
// only way an operator can tell a channel that is being worked through from one
// that is quietly filling up. It is satisfied by *runstate.ReportStore.
type Reports interface {
	List() ([]report.Report, error)
	Handlings() ([]report.Handling, error)
}

// ProviderOutages is the provider answering nobody, as a reading asks it. It is
// satisfied by *runstate.ProviderOutageStore.
type ProviderOutages interface {
	Standing() (runstate.ProviderOutage, bool, error)
}

// Docket is the development manager's triage docket, read for the one entry the
// attention line names: a product decision about a run in flight she has not yet
// answered. It is satisfied by *runstate.DocketStore.
type Docket interface {
	List() ([]triage.Entry, error)
}

// DivergedTargets is the product's record of the target branches the harness
// will not catch up to the remote's, as a reading asks it. It is satisfied by
// *runstate.DivergedTargetStore.
type DivergedTargets interface {
	Standing() ([]runstate.DivergedTarget, error)
}

// Gates is the human gates a person has recorded passing. It is read rather than
// inferred, and it is read from the harness's own store rather than from the
// tracker, because the tracker has no way to answer it: the only completion it
// records is an item being closed, and closure passing a gate that reserved
// somebody's step is the failure the gate exists to end.
//
// It is satisfied by *runstate.Store.
type Gates interface {
	DischargedGates() (map[string][]string, error)
}

// Sources are the durable records one standing reading is assembled from, and
// the two configured numbers it is read against. Every store is an interface so
// that this derivation can be exercised without a state directory, which is the
// only way a format nobody may break gets a fixture that holds it.
type Sources struct {
	// Repository is where the current terms register is read at render time.
	Repository    string
	Runs          Runs
	Conversations Conversations
	Tracker       Tracker
	// Stoppages is what the harness has stopped and nobody has decided about, which
	// is what separates an item held for a person from one whose dependencies have
	// all landed. It is optional, and a reading without one holds every blocked
	// item rather than releasing work whose hold it could not read; see
	// backlog.Holds for why that is the safe direction.
	Stoppages Stoppages
	// Decisions is what triage has already decided about those stoppages, which is
	// what separates an item waiting on the development manager from one waiting
	// on the harness carrying her decision out. It is optional, and a reading
	// without one reports every held item as one nobody has decided about, saying
	// so in the refusal rather than guessing the other way.
	Decisions Decisions
	// Remains is the repository, asked whether each stopped run's change is still
	// there; a hold on a preserved change is decided from that rather than from
	// the run's removal flags. It is optional, and a reading without one decides
	// from the record and says so in the hold.
	Remains       Remains
	Directives    Directives
	Amendments    Amendments
	OperatorHolds OperatorHolds
	IntakeHolds   IntakeHolds
	Sessions      Sessions
	// Gates is the human acts a person has recorded, which is what separates an
	// item waiting on a person's reserved step from one nothing is holding. It is
	// optional, and a reading without one holds every declared gate rather than
	// passing a step nobody recorded taking; readQueue says why that is the safe
	// direction and where the gap is reported.
	Gates Gates
	// Reports is the collected pile. It is optional, and a reading without one
	// says nothing about the pile rather than reporting it empty: "nobody has
	// reported anything" and "nothing was wired to read what anybody reported" are
	// opposite answers, and only one of them means there is nothing to do.
	Reports Reports
	// Docket is the triage docket, read for the Lead Product Manager's decisions
	// about runs in flight that the development manager has not answered. It is
	// optional, and a reading without one names none of them.
	Docket Docket
	// Sweeps is the recurring tasks' reports, read for the recommendations an
	// owning role recorded on the proposed changes put to it. It is optional,
	// and a reading without one names the undecided proposals and says nothing
	// about what their owners argued — which is what every surface showed
	// before the queue had a cadence.
	Sweeps Sweeps
	// UsageLimits is the provider's refusals outside a run, read with the runs
	// above — for the ones parked on a limit — and the agents below for the one
	// thing the three say together: whether the provider is holding every role
	// at once. It is optional, and a reading without one reads the hold from the
	// runs alone and says nothing about the log rather than reporting it empty —
	// a project whose every refusal went unread for five days is the reason
	// this is here.
	UsageLimits UsageLimits
	// CapacityServed is the latest moment the provider served each account and
	// model, read against the refusals above: a refusal recorded before a served
	// turn on its account and model is read as lifted, whatever reset it quoted.
	// It is optional, and a reading without one clears nothing early — every
	// refusal then stands until its quoted reset, as it did before the record
	// existed.
	CapacityServed CapacityServedRecord
	// ProviderOutages is the product's record of the provider answering nobody.
	// It is optional, and a reading without one says nothing about an outage
	// rather than reporting none — three days of a login nobody was told had
	// expired is the reason this is here.
	ProviderOutages ProviderOutages
	// DivergedTargets is the product's record of the target branches the harness
	// will not catch up to the remote's. It is optional, and a reading without
	// one says nothing about a divergence rather than reporting none.
	DivergedTargets DivergedTargets
	// Supervision is the product's supervisor: whether one is running, and what
	// it last recorded about the parts of the product. It is optional, and a
	// reading without one says nothing about the parts rather than reporting
	// them all off — a part the supervisor has left down is the one state here
	// that nothing else reports.
	Supervision Supervision
	Machine     MachineHistory
	// ConfigReaders is what each running part of the product recorded, as it
	// started, about the configuration keys its build reads, compared against
	// the file each reads now. It is optional, and a reading without it says
	// nothing about the parts' builds rather than reporting every part current.
	ConfigReaders ConfigReaders
	// ProgramManagers is every configured agent on the program manager role,
	// with its lane and its schedule, and RestartRequests is the log of their
	// requests that the supervisor restart a part. LaneReports, Passes, and
	// Exchanges are the rest of what an instance's status is derived from: its
	// report's citations are resolved against the requests, the reports, the
	// amendments, and the exchanges, and its passes are attributed to it through
	// the conversations. All are optional: a reading without the instances
	// carries none, and one without a store it needs says so rather than
	// reporting what it could not read as nothing. FirstSeen is when each
	// instance was first seen in the loaded configuration, which is what an
	// instance that has never completed a pass is measured from.
	ProgramManagers []ProgramManagerInstance
	RestartRequests RestartRequests
	LaneReports     LaneReports
	Passes          Passes
	Exchanges       Exchanges
	FirstSeen       FirstSeen
	// Agents is every configured agent, as the configuration resolved it: what
	// each asks for and what each may be served by instead. It is the other half
	// of the hold above, because a refusal holds a role only against what that
	// role is configured to ask.
	Agents []AgentEndpoint
	// UnknownResetPause is execution.usage_limit_unknown_reset_pause as the caller
	// read it: how long a refusal that named no reset stands before the harness
	// asks again, which is the same interval failover reads the same log on.
	UnknownResetPause time.Duration
	// Capacity is execution.max_concurrent_developers as the caller read it. It is
	// what turns "nothing is starting" into "there is no slot", which are opposite
	// things for an operator to do about.
	Capacity int
	// Slots is execution.developer_slots as the caller read it: what each of the
	// Capacity developer slots prefers. It is what lets the running line say
	// which slot each run occupies and name the preferred label beside each slot,
	// read off the same derivation the scheduler fills the free slots from. Empty
	// is every slot preferring nothing, and the line then reads exactly as it did
	// before slots could prefer anything.
	Slots []domain.DeveloperSlot
	// FactoryStallAfter is execution.factory_stall_after as the caller read it:
	// how long no pull and no successful recurring pass may go before the
	// factory is said to have stalled. Zero takes DefaultFactoryStallAfter.
	FactoryStallAfter time.Duration
	// TrackerListings is how the tracker's listings stand, as every listing the
	// harness's tracker client makes records it. Nil says nothing about them.
	TrackerListings TrackerListingRecord
	// TrackerTimeout bounds one tracker command, so an unresponsive tracker costs
	// this answer a line rather than hanging the surface that asked.
	TrackerTimeout time.Duration
	// Now stamps the reading. It defaults to the wall clock and is injected so a
	// test can pin an elapsed time.
	Now func() time.Time
}

// RunningRun is one developer run in flight, as the four-line status names it.
type RunningRun struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	// Title is the work item's title as the run recorded it at its claim, and
	// empty on a run recorded before titles were carried. It is here for the
	// surface that shows a run as a card rather than a line: an id alone is a
	// lookup, and a title beside it is a glance.
	Title string `json:"title,omitempty"`
	// Labels is the tracker's labels on the item as the run recorded them at its
	// claim, and empty on a run recorded before labels were carried. They are
	// what the slot below is read from.
	Labels []string `json:"labels,omitempty"`
	// Backend, Model, and Account are what the run is spending: the provider it
	// runs on, the model it asked for (the resolved identifier where the provider
	// reported one), and the account alias it runs under. The alias is exactly
	// what lets this be said on a page — it names nothing a credential is.
	Backend domain.Backend `json:"backend,omitempty"`
	Model   string         `json:"model,omitempty"`
	// Effort is the effort level the run's developer invocations ask for, said
	// beside the model, and absent where the developer agent configured none.
	Effort  string         `json:"effort,omitempty"`
	Account string         `json:"account,omitempty"`
	Phase   runstate.Phase `json:"phase,omitempty"`
	// Stage is the phase folded onto the three parts of a run a pipeline shows,
	// derived here so no surface keeps its own list of which phase is which.
	Stage Stage `json:"stage"`
	// ResumingIntegration reports a run at its promotion again after the
	// environment stopped it there, with its approval standing. The line says
	// runstate.ResumingIntegrationSays for it in place of the bare phase, because
	// a run integrating after such a stop and one integrating for the first time
	// are the same phase and different facts.
	ResumingIntegration bool `json:"resuming_integration,omitempty"`
	// Checks is where the check stage stands, said as the record words it —
	// "checks: 14m of 30m, on make race" — and empty for a run whose current
	// attempt is not in its checks. The line says it in place of the bare phase,
	// because a run "checking" for forty minutes and a run fourteen minutes into
	// a thirty-minute stage are the same phase and different facts, and the
	// second is the one an operator watching a slow stage is reading for.
	Checks string `json:"checks,omitempty"`
	// AfterReply says the developer's session has written its final reply and
	// is still running on work it started in the background, as the record
	// words it — "reply written, waiting for background processes: 2m of 5m" —
	// and is empty otherwise. The line says it in place of the bare phase,
	// because a run "developing" whose turn is over and one whose provider is
	// still working are the same phase and different facts, and only the second
	// is the provider's.
	AfterReply string `json:"after_reply,omitempty"`
	// NoProcess says why no process can be found behind the run, and is empty
	// where one can. A run's record says "running" until something writes its
	// ending, and a process that dies writes nothing, so a run with no process is
	// still in flight, still holding its slot, and still listed here — but the line
	// says it is not running rather than printing the phase the dead process last
	// wrote. It is read without taking the run's lease, from the holder stamp the
	// lease keeps; see runstate.Store.Presence.
	NoProcess string `json:"no_process,omitempty"`
	// NoProcessRemedy is what ends such a run, which is not the same for every
	// park: runstate.DeadRunRemedy, from the sweep's own rules. It is set exactly
	// where NoProcess is.
	NoProcessRemedy string              `json:"no_process_remedy,omitempty"`
	StartedAt       time.Time           `json:"started_at"`
	Elapsed         time.Duration       `json:"elapsed"`
	CostText        string              `json:"cost_text"`
	Tokens          runstate.TokenUsage `json:"tokens"`
	CostUSD         float64             `json:"cost_usd"`
	// UnknownCost says why there is no figure rather than reporting one of zero: a
	// run whose evidence cannot be read has not cost nothing.
	UnknownCost string `json:"unknown_cost,omitempty"`
	// Slot is the developer slot this run occupies, counted from 1 as the
	// configuration counts them, and SlotPrefers what that slot prefers. Both are
	// carried only where some configured slot prefers a label — which slot a run
	// is in is only worth saying where the slots differ — and both are read off
	// the derivation the scheduler reads, so the slot this says a run holds is
	// the slot the scheduler will not fill. Zero is a run in flight beyond the
	// configured capacity, which no slot holds.
	Slot        int      `json:"developer_slot,omitempty"`
	SlotPrefers []string `json:"slot_prefers,omitempty"`
	// WaitingOn is the unfinished work a run paused on a dependency is waiting
	// for, as its record names it, and PausedSince when it last wrote its record,
	// which for such a run is when it recorded the pause. Both are carried only
	// on Standing.PausedRuns.
	WaitingOn   string    `json:"waiting_on,omitempty"`
	PausedSince time.Time `json:"paused_since,omitempty"`
}

// DeveloperSlotStanding is one configured developer slot as the standing status
// names it: its number, what it prefers, and the run in it where one is. It is
// carried only where some configured slot prefers a label, for the surfaces
// that read the model rather than its lines, and the running line renders the
// free ones under it.
type DeveloperSlotStanding = developerslot.Slot

// WorkingTurn is one persona conversation with a turn in flight. It is the fact
// no surface counted before this: a conversation is not a run, so a machine
// spending an operator's money on six persona turns reported nothing running at
// all.
type WorkingTurn struct {
	Agent string           `json:"agent"`
	Role  domain.AgentRole `json:"role"`
	// Backend and Model are what the turn is spending, as the conversation record
	// carries them; the model is the resolved identifier where the provider
	// reported one.
	Backend domain.Backend `json:"backend,omitempty"`
	Model   string         `json:"model,omitempty"`
	// Effort is the effort level the conversation's last recorded turn asked
	// for, and absent where the agent configured none.
	Effort string `json:"effort,omitempty"`
	// Turns is how many turns the record holds, which is the turn before the one
	// in flight: a turn is recorded as it completes.
	Turns int `json:"turns"`
	// Since is when the record last moved, which is the closest thing the durable
	// state has to when the turn in flight began.
	Since   time.Time     `json:"since"`
	Elapsed time.Duration `json:"elapsed"`
}

// Refused is one admitted item nothing will pull, with the refusal that stops
// it. The reason is the queue's or the harness's own account rather than a
// paraphrase, because a reason nobody can act on is the whole of what this line
// exists to replace.
type Refused struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title,omitempty"`
	Reason     string `json:"reason"`
	// Kind is which pile the refusal puts the item in, from the queue's own
	// closed vocabulary. It is carried for the surface that shows where admitted
	// work accumulates, so that surface counts the piles from the reading that
	// worded the refusals rather than from a second parse of them.
	Kind backlog.HoldKind `json:"kind"`
	// HeldSince is when an item held after a stopped run came to be held, read
	// from the record that holds it — the run's stop, the stoppage's docketing,
	// or the decision that stopped it — and nil for every other kind and for a
	// record that names no moment. It is what lets a reader tell a hold from
	// yesterday from one three weeks old, which the reason alone does not say.
	HeldSince *time.Time `json:"held_since,omitempty"`
}

// WorkItemRef names one admitted work item, by id and title, for a surface that
// lists a grouping of the queue rather than counting it. It carries no more than
// that: what an item is in full is ReadWorkItem's answer, one item at a time.
type WorkItemRef struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title,omitempty"`
}

// Standing is where the harness stands, in the four lines and nothing else.
// Every line carries its own problem rather than a shared one, because a
// tracker that will not answer says nothing whatever about the runs in flight,
// and one failure standing for all four would lose three answers the caller
// still has.
type Standing struct {
	// FactoryProblems is the same raised pass-failure entries the fourth line
	// carries, projected for the dashboard's factory-problems section.
	FactoryProblems        []Attention `json:"factory_problems"`
	FactoryProblemsProblem string      `json:"factory_problems_problem,omitempty"`
	ObservedAt             time.Time   `json:"observed_at"`

	// Paused is the harness waiting out the provider's usage window, said above
	// the four lines rather than inside them.
	//
	// It is the one state that goes there, and it is there because the operator
	// asked for it in those terms: when the system is paused on a provider usage
	// window, the cause is the first words of any message that reaches him. A
	// reading is a message — it is what he runs when he wants to know why nothing
	// is happening — and the window said only as one item's refusal is the cause
	// three lines down and possibly tenth in a list. It is not a fifth line: the
	// four still render exactly as they did, and this is a banner above them, in
	// the shape `yoyo status --list` already puts the operator's own pause in.
	Paused string `json:"paused,omitempty"`
	// CapacityHold is the provider holding every configured role at once, as the
	// refusal log and the agents' configuration say it, and nil where it is not.
	// It is carried whole for the surfaces that read the model rather than its
	// lines: the banner above says it in one sentence, and this is the reset, the
	// count, and the models behind that sentence.
	CapacityHold *CapacityHold `json:"capacity_hold,omitempty"`
	// CapacityBlocked is what is parked or held on provider capacity, one run
	// and one conversation at a time: the hold above is every role refused at
	// once, and this is each thing the provider has stopped on its own, with
	// its deadline and what a person can do about it. It is not a fifth line
	// and is not rendered as one; it is carried for the surfaces that read the
	// model rather than its lines — the JSON a script reads, and the capacity
	// panel the dashboard design asks for — and it is always present, with each
	// half saying so where its records could not be read.
	CapacityBlocked CapacityBlocked `json:"capacity_blocked"`
	// ProviderOutage is the provider answering nobody — a login nobody has
	// renewed, an API nothing reaches — as the product's record says it, and nil
	// where it is answering. It is carried whole for the surfaces that read the
	// model rather than its lines; the banner above says it in one sentence.
	ProviderOutage *runstate.ProviderOutage `json:"provider_outage,omitempty"`
	// DivergedTargets is every target branch the product's record holds as one
	// the harness will not catch up to the remote's, carried whole for the
	// surfaces that read the model rather than its lines. While any stands a
	// watching session chooses nothing; each is on the attention line as the
	// operator's, with the recovery.
	DivergedTargets []runstate.DivergedTarget `json:"diverged_targets,omitempty"`

	Running        []RunningRun `json:"running"`
	RunningProblem string       `json:"running_problem,omitempty"`
	// PausedRuns is the runs in flight paused on work their items wait on. Such a
	// run has no process, spends nothing, and holds no developer slot
	// (runstate.State.HoldsDeveloperSlot), so it is said here rather than on the
	// running line, and the slot it gave up is counted free wherever capacity is
	// said. It keeps its claim, branch, worktree, and session, and continuing it
	// takes a slot again.
	PausedRuns []RunningRun `json:"paused_runs,omitempty"`
	// Dispatching is every dispatch a watch session started that is waiting out a
	// tracker failure before it has claimed anything, as WaitingOnTracker reads it
	// from the watch log. Such a dispatch holds a developer slot with no run
	// record, so it is on no list above, and it is said on the running line
	// because that is where a reader looks for what is holding the slots.
	// DispatchingProblem is a watch log that could not be read for them, said
	// under the line rather than in place of it: the runs above were read.
	Dispatching        []DispatchWait `json:"dispatching,omitempty"`
	DispatchingProblem string         `json:"dispatching_problem,omitempty"`
	// DeveloperSlots is every configured developer slot with the run in it, in
	// slot order, where some slot prefers a label; nil otherwise. The running
	// line names the free ones with their preference under itself, and a run's
	// own entry says which slot it is in, so this is for the surface that wants
	// the whole set rather than the lines.
	DeveloperSlots []DeveloperSlotStanding `json:"developer_slots,omitempty"`

	Working        []WorkingTurn `json:"working"`
	WorkingProblem string        `json:"working_problem,omitempty"`
	// Waiting turns have put down their conversation while no provider is
	// serving them. They are visible without counting as turns in flight.
	Waiting        []runstate.ConversationWait `json:"waiting,omitempty"`
	WaitingProblem string                      `json:"waiting_problem,omitempty"`

	NotStartable []Refused `json:"not_startable"`
	// NotStartableGroups is the not-startable items counted by what they wait
	// on, each group with its next step and whose move it is, in the order a
	// reader acts on them. NotStartableForOperator is the one sentence saying
	// whether any of it is the operator's. Both are absent where the queue could
	// not be read, as NotStartable's entries are.
	NotStartableGroups      []WaitGroup `json:"not_startable_groups,omitempty"`
	NotStartableForOperator string      `json:"not_startable_for_operator,omitempty"`
	// WaitingForSlot is the ready work waiting only for a developer slot, where
	// every slot is taken and something is ready behind them, and nil otherwise.
	// It is not refused work and is counted in Startable, never in NotStartable:
	// the harness starts the next of it as a run finishes.
	WaitingForSlot *SlotWait `json:"waiting_for_slot,omitempty"`
	// Admitted is the whole backlog this reading saw, so a short not-startable
	// list is legible: two refusals out of three admitted items and two out of
	// forty are different states of the same machine.
	Admitted int `json:"admitted"`
	// AwaitingDecision and AwaitingCarryOut are how much of the admitted work is
	// held, split by whose move it is: a stoppage the development manager has
	// still to decide about, and a decision she recorded that the harness has
	// still to act on. They are counted separately and said in the head of the
	// not-startable line, because the head is the whole of what an hourly message
	// carries and one figure covering both is what sent an operator's attention to
	// the wrong role for days.
	AwaitingDecision int `json:"awaiting_decision"`
	AwaitingCarryOut int `json:"awaiting_carry_out"`
	// CarryOutsRefused and CarryOutsUnattempted count, over the same held items,
	// the recorded decisions the harness has not carried out, by what became of
	// them: a gate refused the attempt, or no pass attempted it at all. They are
	// said beside each other because they send a reader to different places — the
	// gate, or the pass — and until yoyodyne-ifd.428.39 only the first was ever
	// written anywhere, so two re-runs nobody attempted read as nothing at all.
	CarryOutsRefused     int `json:"carry_outs_refused"`
	CarryOutsUnattempted int `json:"carry_outs_unattempted"`
	// Startable is how much of the admitted work nothing refuses: the items the
	// harness would start next, counted over the same entries the refusals are,
	// so the head of the line, the refusals under it, and this are one set of
	// items. It is zero whenever the pass-level stall stands, because a stall
	// is precisely every pullable item refused at once — except a full machine,
	// which refuses nothing: the ready items behind every slot being taken are
	// the ones started next, so they are counted here and named again under
	// WaitingForSlot. It is not printed — the
	// four lines say it by the absence of a refusal — and is carried for the
	// surface that shows the pipeline, so that surface reads the count rather
	// than subtracting one list from another.
	Startable int `json:"startable"`
	// AdmittedItems and StartableItems name the items behind Admitted and
	// Startable, each in the product manager's order, so a surface that opens a
	// grouping of the queue lists the same items the count counts rather than
	// assembling a list of its own from the lines. The refusals above already
	// name the held-back items, so with these three every item Admitted counts is
	// named exactly once, and an item a run is carrying is in AdmittedItems and
	// on the running line and nowhere else. Both are nil where the queue could
	// not be read, as NotStartable is, and empty rather than absent otherwise.
	AdmittedItems       []WorkItemRef `json:"admitted_items"`
	StartableItems      []WorkItemRef `json:"startable_items"`
	NotStartableProblem string        `json:"not_startable_problem,omitempty"`

	NeedsHuman        []Attention `json:"needs_human"`
	NeedsHumanProblem string      `json:"needs_human_problem,omitempty"`

	// Reports is how the collected pile stands. It is not a fifth line and is not
	// rendered as one: the four are a contract the operator ratified, and this is
	// carried for the surfaces that read the model rather than its lines — the
	// JSON a script or a dashboard reads, and the arithmetic behind the attention
	// entry below. Whether the pile is draining is a question about a week rather
	// than a moment, and it is answerable only if each reading says how deep the
	// pile is and how old the oldest undecided report in it was.
	Reports report.Pile `json:"reports"`
	// ReportsProblem is a pile that could not be read. It is stated rather than
	// reported as an empty pile, for the reason every other line here states its
	// own failure: a reader told nothing concludes there is nothing.
	ReportsProblem string `json:"reports_problem,omitempty"`

	// Amendments is how the queue of proposed changes stands, the pile's
	// sibling and carried for the same reason: whether the queue is draining is
	// a question about a week of readings, answerable only if each says how deep
	// it is and how old the oldest undecided proposal was. RecommendedAmendments
	// is the batch the owning roles have argued and the operator has still to
	// decide, read off the recurring passes, which is what a surface that puts
	// the batch to him reads. AmendmentsProblem is a queue that could not be
	// read, stated rather than reported as empty.
	Amendments            amendment.Queue        `json:"amendments"`
	RecommendedAmendments []RecommendedAmendment `json:"recommended_amendments"`
	AmendmentsProblem     string                 `json:"amendments_problem,omitempty"`

	// Services is the product's parts as its supervisor last recorded them, and
	// whether a supervisor is running now. It is not a fifth line: it is carried
	// for the surfaces that read the model, and the one thing in it that waits
	// on a person — a part the supervisor has left down — is on the attention
	// line with its reason. It is nil where nothing was wired to read it.
	Services        *Services `json:"services,omitempty"`
	ServicesProblem string    `json:"services_problem,omitempty"`

	// ProgramManagers is each program manager instance as its one query carries
	// it: its lane, its status, what its report is blocked on, and its open
	// restart requests. It is not a fifth line: nothing here is waiting on a
	// person, and the operator reviews an instance when he chooses. `yoyo status`
	// prints a line per instance under the four, and the channel's hourly line
	// counts the stale ones where it already posts. It is absent where no
	// instance is configured and none has asked for anything.
	ProgramManagers        []ProgramManager `json:"program_managers,omitempty"`
	ProgramManagersProblem string           `json:"program_managers_problem,omitempty"`

	// Titles is what the tracker calls every item it holds, which is what the
	// rendered lines put beside each number they carry. It is nil where the
	// tracker could not be listed, and the lines then carry the numbers as the
	// records wrote them rather than calling every one unknown.
	Titles  *WorkItemTitles `json:"-"`
	wording *TextTerms
}

// maxUndecidedReportAge is how long the oldest report nobody has decided about
// may go unanswered before the pile is something waiting on a person rather
// than something a schedule is working through.
//
// A pile is meant to drain on its own: every role files into it, the product
// manager decides about what it is shown, and a recurring task works it on a
// cadence so that neither depends on an operator being at a terminal. The
// failure this catches is that machinery not running or not keeping up, which is
// invisible in any one reading — the pile looks the same the day it stops
// draining as it did the day before — and shows only as the oldest report's age
// climbing past anything a working cadence would leave.
const maxUndecidedReportAge = 7 * 24 * time.Hour

// ReadStanding assembles the four lines from the durable records. It never
// fails as a whole: a source that cannot be read costs its own line and leaves
// the other three, because an operator asking where things stand is worse off
// with nothing than with the three quarters the harness could answer, as long as
// the missing quarter says so.
func ReadStanding(ctx context.Context, sources Sources) Standing {
	now := sources.now()
	standing := Standing{
		ObservedAt:   now,
		Running:      []RunningRun{},
		Working:      []WorkingTurn{},
		NotStartable: []Refused{},
		NeedsHuman:   []Attention{},
	}

	running, paused, runningProblem := readRunning(sources, now)
	standing.Running, standing.PausedRuns, standing.RunningProblem = running, paused, runningProblem
	standing.Dispatching, standing.DispatchingProblem = readDispatching(sources, now)
	// Which slot each run occupies, and which slots are free, from the reading
	// the scheduler makes. It is read only where a slot prefers a label, and
	// only where the runs could be read: a slot said to be free over runs
	// nobody could list would be the confident emptiness this refuses.
	if runningProblem == "" {
		standing.DeveloperSlots = readSlots(sources, standing.Running)
	}

	standing.Working, standing.WorkingProblem = readWorking(sources, now)
	if waits, ok := sources.Conversations.(interface {
		WaitingTurns() ([]runstate.ConversationWait, error)
	}); ok {
		var err error
		standing.Waiting, err = waits.WaitingTurns()
		if err != nil {
			standing.WaitingProblem = fmt.Sprintf("the waiting conversation turns could not be read: %v", err)
		}
	}

	// The switches and the directives are read once and used twice: they are why
	// admitted work is not being pulled, and they are themselves things waiting on
	// a person. Reading them twice would be two chances for the two lines to
	// disagree about one file.
	switches := readSwitches(sources)
	refused, waits, queue, stall, notStartableProblem := readNotStartable(ctx, sources, switches, running, now)
	standing.NotStartable = refused
	standing.Admitted = len(queue.Entries)
	standing.AwaitingDecision = waits.awaitingDecision
	standing.AwaitingCarryOut = waits.awaitingCarryOut
	standing.CarryOutsRefused = waits.carryOutsRefused
	standing.CarryOutsUnattempted = waits.carryOutsUnattempted
	standing.Startable = len(waits.startable)
	standing.StartableItems = waits.startable
	standing.AdmittedItems = waits.admitted
	standing.WaitingForSlot = waits.slotWait
	if waits.groups != nil {
		standing.NotStartableGroups = waits.groups
		standing.NotStartableForOperator = ForOperator(waits.groups)
	}
	standing.NotStartableProblem = notStartableProblem
	// The provider's usage window is read out of the same stall the refusals are
	// worded from, rather than derived a second time here: one reading of one
	// silence, said in two places, is the rule this whole package holds.
	if stall.Reason == ReasonProviderWindow || stall.Reason == ReasonProviderAway {
		standing.Paused = stall.Says
	}
	// The provider answering nobody is carried whole as well as said, and it is
	// read again here rather than only through the stall: the stall is dropped
	// where it stopped nothing, and a login that expired over an empty queue is
	// still a login the operator has to renew before anything can happen.
	if switches.providerAway {
		standing.ProviderOutage = &switches.providerOutage
		if standing.Paused == "" {
			standing.Paused = switches.providerOutage.Says()
		}
	}
	// The provider holding every role is the other pause, read from the refusal
	// log and the parked runs rather than from the session choosing work: on
	// 2026-09-08 that session was idle over items waiting on a decision, and the
	// role that would have decided was the one being refused, so the watch log
	// never said a window at all. The session's own account wins where it has
	// one, because it is the same window said with less inference; this says it
	// where nothing else does.
	hold, holdProblem := CapacityHoldOf(sources, now)
	if hold.Holding {
		standing.CapacityHold = &hold
		if standing.Paused == "" {
			standing.Paused = hold.Says()
		}
	}
	// What is parked or held on capacity one at a time is read from the same
	// records the hold and the running line are read from, and carried whole
	// rather than said: a run asleep on a reset is still on the running line,
	// and this is where a surface finds out that it is asleep.
	standing.CapacityBlocked = CapacityBlockedOf(sources, now)

	// The pile is read once and used twice: for how it stands, and for the
	// findings in it that need the operator. Two readings of one pile a moment
	// apart could disagree about whether a report is handled.
	reports, handlings, pileProblem := readPile(sources)
	standing.Reports, standing.ReportsProblem = summarizePile(reports, handlings, pileProblem, now)
	// An escalated stoppage is a finding while its item is still admitted and not
	// parked: an item the operator retired, or closed after doing what was asked,
	// and an item the Lead Product Manager parked are read from the same queue the
	// not-startable line was read from. A queue that could not be read admits
	// nothing this reading can see, and that must not read as every escalation
	// having ended: the findings are read as standing instead, which says one once
	// rather than never. The run's change being gone is asked of the repository,
	// by the same look the held work is read with.
	var items EscalatedItems
	if notStartableProblem == "" || len(queue.Entries) > 0 {
		inQueue := make(map[string]EscalatedItem, len(queue.Entries))
		for _, entry := range queue.Entries {
			inQueue[entry.ID] = EscalatedItem{Admitted: true, Parked: entry.Parking.Reason()}
		}
		items = func(id string) EscalatedItem { return inQueue[id] }
	}
	actions, actionsProblem := readOperatorActions(reports, handlings, pileProblem, sources, items, Looking(ctx, sources.Remains, nil))
	// The proposed changes are read once and used three times, as the switches
	// are: they are the queue's count and age, each undecided one is a thing
	// waiting on a person, and the batches their owners argued on a recurring
	// pass are findings for the operator beside the pile's. The recommendations
	// are read off the passes' own reports.
	amendments := readAmendments(sources, now)
	standing.Amendments = amendments.queue
	standing.RecommendedAmendments = amendments.recommended
	if standing.RecommendedAmendments == nil {
		standing.RecommendedAmendments = []RecommendedAmendment{}
	}
	standing.AmendmentsProblem = joinProblems(amendments.problem, amendments.sweepProblem)
	actions = append(actions, amendments.batchAttention()...)

	needs, needsProblem := readNeedsHuman(sources, switches, actions, amendments)
	needsProblem = joinProblems(needsProblem, actionsProblem)
	// The provider answering nobody is on the attention line whatever the queue
	// holds, because what ends it is a person: it is added here where the stall
	// did not already carry it, which is a stall over an empty queue.
	if switches.providerAway && stall.Reason != ReasonProviderAway {
		needs = append(needs, outageAttention(switches.providerOutage))
	}
	// A target branch the harness will not catch up to the remote's is on the
	// attention line whatever else is stopping the choosing, for the reason the
	// outage is: only a person settles it. Where it is what stops the choosing
	// the stall below carries the first; every other one is added here.
	standing.DivergedTargets = switches.diverged
	for index, diverged := range switches.diverged {
		if index == 0 && stall.Reason == ReasonDivergedTarget {
			continue
		}
		needs = append(needs, divergedTargetAttention(diverged))
	}
	// The hold is waiting on a person in the one way a window is not: the window
	// lifts on the provider's clock, and the configuration that let it hold every
	// role is the operator's to change.
	if attention, held := hold.Attention(); held {
		needs = append(needs, attention)
	}
	needsProblem = joinProblems(needsProblem, holdProblem)
	// A pile whose oldest undecided report has been waiting longer than any
	// working cadence would leave it is waiting on a person, whatever else is
	// running. Nothing else says so: the pile is not work, so no queue holds it,
	// and the reports themselves are filed and forgotten by the roles that filed
	// them.
	if standing.Reports.OldestAge > maxUndecidedReportAge {
		needs = append(needs, reportsAttention(standing.Reports))
	}
	// The pile's sibling: a queue of proposed changes whose oldest undecided one
	// has waited longer than any working cadence would leave it. The undecided
	// proposals are each named on the line already; what this adds is the age,
	// which a list of them is not.
	if aged, waiting := amendments.ageAttention(); waiting {
		needs = append(needs, aged)
	}
	// A stall that is holding admitted work back and is nobody else's line to
	// carry is attention in its own right. Nothing else reports it: a live session
	// choosing nothing over a ready queue is a state no record announces, and the
	// only thing that ever said it was the silence afterwards.
	if waiting, attention := stall.Waiting(); attention {
		// A divergence's stall names its reason, which is every divergence's, so
		// its entry is keyed to the branch it is about like the ones above.
		if stall.Reason == ReasonDivergedTarget && len(switches.diverged) > 0 {
			waiting.ID = divergedTargetAttentionID(switches.diverged[0])
		}
		needs = append(needs, waiting)
	}
	// A part of the product the supervisor has stopped restarting is down and
	// not coming back on its own, which is the design's degraded state reaching
	// the standing surfaces: it is said here with the reason the supervisor
	// recorded, and the record is carried whole beside the lines.
	standing.Services, standing.ServicesProblem = readServices(sources)
	standing.ProgramManagers, standing.ProgramManagersProblem = ReadProgramManagers(sources)
	needs = append(needs, standing.Services.Attention()...)
	needsProblem = joinProblems(needsProblem, standing.ServicesProblem)
	// A running part whose build cannot read a key the configuration now
	// carries shows a person the decoder's error and nothing else, so it is
	// said here naming the part, its build, and the keys.
	mismatches, mismatchProblem := readConfigMismatches(sources)
	for _, mismatch := range mismatches {
		needs = append(needs, configMismatchAttention(mismatch))
	}
	needsProblem = joinProblems(needsProblem, mismatchProblem)
	// Every product pass is raised after three failed executions. Preserve the
	// earlier pre-turn signal at two failures without listing a pass twice.
	standing.FactoryProblems, standing.FactoryProblemsProblem = ReadPassFailures(sources)
	if standing.FactoryProblems == nil {
		standing.FactoryProblems = []Attention{}
	}
	raised := map[string]bool{}
	for _, entry := range standing.FactoryProblems {
		raised[entry.ID] = true
		needs = append(needs, entry)
	}
	needsProblem = joinProblems(needsProblem, standing.FactoryProblemsProblem)
	failing, failingProblem := ReadFailingTasks(sources)
	for _, task := range failing {
		if !raised[task.Task] {
			needs = append(needs, failingTaskAttention(task))
		}
	}
	needsProblem = joinProblems(needsProblem, failingProblem)
	// A pass that reported findings and left no trace of them is the role's
	// problem to move rather than a person's, and is said with the role as its
	// mover until a pass of the same task takes a turn again.
	untraced, untracedProblem := ReadUntracedPasses(sources)
	for _, pass := range untraced {
		needs = append(needs, untracedPassAttention(pass))
	}
	needsProblem = joinProblems(needsProblem, untracedProblem)
	// The factory having pulled nothing and completed no pass for longer than
	// its limit is said here as well as reported: the report is filed once, when
	// the supervisor notices, and this is where it stands until it clears.
	factoryStall, factoryStallProblem := ReadFactoryStall(sources)
	if factoryStall != nil {
		needs = append(needs, factoryStallAttention(*factoryStall))
	}
	needsProblem = joinProblems(needsProblem, factoryStallProblem)
	// The tracker not answering listings is said here with since when, from the
	// record every listing writes, rather than learned from whichever pass or
	// docket last failed on one.
	unanswered, unansweredProblem := ReadTrackerUnanswered(sources)
	if unanswered != nil {
		needs = append(needs, trackerUnansweredAttention(*unanswered))
	}
	needsProblem = joinProblems(needsProblem, unansweredProblem)
	// Held work is on both lines for the reason handed-off work below is, and says
	// a different thing on each: the queue's line says why nothing pulls each
	// item, and this says who has to move and how many items are waiting on them.
	needs = append(needs, Held(standing.AwaitingDecision, standing.AwaitingCarryOut)...)
	// Work marked for a conversation is on both lines and says a different thing
	// on each: the queue's line says why nothing pulls it, and this says who has
	// to open the conversation. A reader looking for what waits on a person must
	// not have to read the queue to find the longest wait there is.
	needs = append(needs, HandedOff(queue)...)
	// A human gate is on both lines for the same reason and is the plainest case
	// of it: the queue says the item will not be pulled, and this says that the
	// reason is a step reserved for a person and that nothing else will ever take
	// it. An operator who only read the queue's line would be reading a wait.
	standing.NeedsHuman = append(needs, Gated(queue)...)
	// A pile that could not be read is said on the line the pile would have been
	// said on, as well as in its own field. The field is what a script reads and
	// the line is what a person reads, and a failure only the script can see is
	// one nobody sees.
	standing.NeedsHumanProblem = joinProblems(needsProblem, standing.ReportsProblem)
	// Every number a person reads here is read beside its item's title. A
	// tracker that cannot be listed costs the titles and nothing else: the lines
	// above are all still true without them.
	standing.Titles, _ = ReadWorkItemTitles(ctx, sources)
	standing.wording = ReadTextTerms(sources.Repository)
	for index := range standing.NeedsHuman {
		standing.NeedsHuman[index].titles = standing.Titles
		standing.NeedsHuman[index].wording = standing.wording
	}
	for index := range standing.ProgramManagers {
		standing.ProgramManagers[index] = citeProgramManager(standing.ProgramManagers[index], standing.Titles)
	}
	return standing
}

// readRunning is the developer runs in flight, priced from the same recorded
// evidence `yoyo cost` reads. A run whose price cannot be read is reported as
// unpriceable rather than as free, which is the rule every cost surface here
// already holds.
//
// In flight is runstate.Status.InFlight and nothing wider: the store's listing
// already answers in those terms, and the predicate is applied here as well so
// that this count is the status's own whatever listing it was handed. The
// scheduler reads the same predicate for the slots and epics already taken,
// which is what lets its refusals be checked against this line.
//
// A run paused on work its item waits on is returned apart, as the second list:
// it holds no developer slot, so counting it as running would say a slot is
// taken that the scheduler will fill.
func readRunning(sources Sources, now time.Time) ([]RunningRun, []RunningRun, string) {
	if sources.Runs == nil {
		return nil, nil, "nothing was wired to read the runs in flight"
	}
	states, err := sources.Runs.Incomplete()
	if err != nil {
		return nil, nil, fmt.Sprintf("the runs in flight could not be read: %v", err)
	}
	running := make([]RunningRun, 0, len(states))
	var paused []RunningRun
	for _, state := range states {
		if !state.Status.InFlight() {
			continue
		}
		run := RunningRun{
			RunID:               state.RunID,
			WorkItemID:          state.WorkItemID,
			Title:               state.WorkItemTitle,
			Labels:              append([]string(nil), state.WorkItemLabels...),
			Backend:             state.Backend,
			Model:               modelOf(state.ProviderModel, state.ProviderResolvedModel),
			Effort:              strings.TrimSpace(state.ProviderEffort),
			Account:             state.AccountAlias,
			Phase:               state.Phase,
			Stage:               StageOf(state.Phase),
			ResumingIntegration: state.ResumingIntegration(),
			Checks:              checksOf(state, now),
			AfterReply:          afterReplyOf(state, now),
			StartedAt:           state.StartedAt,
			Elapsed:             now.Sub(state.StartedAt),
			// A run nothing has priced yet is stated as unpriced rather than as free.
			// It is overwritten below by whatever the ledger actually says.
			UnknownCost: "no priced invocation is recorded for it yet",
		}
		if presence, ok := sources.Runs.(RunPresence); ok {
			// A reading that could not be made says nothing: the line then reads as it
			// did before this could be asked, rather than calling a run dead on the
			// strength of a stamp nobody could read.
			if found, err := presence.Presence(state, DefaultDeadClaimThreshold, now); err == nil && !found.Found {
				run.NoProcess = found.Says
				run.NoProcessRemedy = runstate.DeadRunRemedy(state, DefaultDeadClaimThreshold)
			}
		}
		price, err := sources.Runs.Price(state.WorkItemID)
		if err != nil {
			run.UnknownCost = fmt.Sprintf("what it has spent could not be read: %v", err)
		} else {
			for _, priced := range price.Runs {
				if priced.RunID != state.RunID {
					continue
				}
				run.CostUSD, run.UnknownCost = priced.CostUSD, priced.Unknown
				run.Tokens = priced.Tokens
				run.CostText = priced.Tokens.CostText(priced.CostUSD)
				break
			}
		}
		if !state.HoldsDeveloperSlot() {
			if state.DependencyPause != nil {
				run.WaitingOn = state.DependencyPause.Summary()
			}
			run.PausedSince = state.UpdatedAt
			paused = append(paused, run)
			continue
		}
		running = append(running, run)
	}
	for _, runs := range [][]RunningRun{running, paused} {
		sort.SliceStable(runs, func(first, second int) bool {
			if !runs[first].StartedAt.Equal(runs[second].StartedAt) {
				return runs[first].StartedAt.Before(runs[second].StartedAt)
			}
			return runs[first].RunID < runs[second].RunID
		})
	}
	return running, paused, ""
}

// readDispatching is the dispatches standing in a wait on the tracker. A reading
// wired with no watch log has none to report, which is every reading from before
// a session could record one.
func readDispatching(sources Sources, now time.Time) ([]DispatchWait, string) {
	if sources.Sessions == nil {
		return nil, ""
	}
	sessions, err := sources.Sessions.List()
	if err != nil {
		return nil, fmt.Sprintf("whether a dispatch is waiting out the tracker could not be read: %v", err)
	}
	waiting := WaitingOnTracker(sessions, now)
	if len(waiting) == 0 {
		return nil, ""
	}
	return waiting, ""
}

// readSlots is which developer slot each run in flight occupies and which are
// free, read from the same derivation the scheduler fills the free slots from,
// over the labels each run recorded at its claim. It answers nothing where no
// configured slot prefers a label: which slot a run is in is only worth saying
// where the slots differ, and a project that configured no preference reads
// exactly as it did before. The runs are given their slot in place.
func readSlots(sources Sources, running []RunningRun) []DeveloperSlotStanding {
	if !developerslot.Preferring(sources.Slots) {
		return nil
	}
	inFlight := make([]developerslot.Run, 0, len(running))
	for _, run := range running {
		inFlight = append(inFlight, developerslot.Run{
			RunID:      run.RunID,
			WorkItemID: run.WorkItemID,
			Labels:     run.Labels,
			StartedAt:  run.StartedAt,
		})
	}
	assignment := developerslot.Assign(sources.Capacity, sources.Slots, inFlight)
	for _, slot := range assignment.Slots {
		if slot.Free() {
			continue
		}
		for index := range running {
			if running[index].RunID == slot.RunID {
				running[index].Slot = slot.Number
				running[index].SlotPrefers = slot.Preferred
			}
		}
	}
	return assignment.Slots
}

// checksOf is where a run's check stage stands, for a run that is in it: what
// the stage has spent of its bound and which check it is on, in the record's
// own words. A run in any other phase says nothing here, and so does a record
// written before the stage was recorded.
func checksOf(state runstate.State, now time.Time) string {
	if state.Phase != runstate.PhaseChecking || state.CheckStage == nil || !state.CheckStage.Running() {
		return ""
	}
	return state.CheckStage.Describe(now)
}

// afterReplyOf is what a run in flight says while its developer's session is
// waiting out background work after its final reply, and empty for any other
// run: a finished wait is the attempt's history rather than where it stands.
func afterReplyOf(state runstate.State, now time.Time) string {
	if state.Phase != runstate.PhaseDeveloping || state.AfterReply == nil || !state.AfterReply.Waiting() {
		return ""
	}
	return state.AfterReply.Describe(now)
}

// readWorking is the persona conversations with a turn in flight. A conversation
// nobody is holding is not listed at all: this line counts work happening now,
// and a record of every conversation the product has ever had would make the
// count mean nothing.
func readWorking(sources Sources, now time.Time) ([]WorkingTurn, string) {
	if sources.Conversations == nil {
		return nil, "nothing was wired to read the conversations"
	}
	recorded, err := sources.Conversations.Recorded()
	if err != nil {
		return nil, fmt.Sprintf("the conversations could not be read: %v", err)
	}
	working := make([]WorkingTurn, 0, len(recorded))
	var unanswered []string
	for _, conversation := range recorded {
		identity := runstate.ConversationIdentity{Agent: conversation.Agent, Role: conversation.Role}
		if identity.Agent == "" {
			identity.Agent = string(conversation.Role)
		}
		inFlight, problem := InFlight(sources.Conversations, identity)
		if problem != "" {
			unanswered = append(unanswered, fmt.Sprintf("%s (%s)", identity, problem))
			continue
		}
		if !inFlight {
			continue
		}
		working = append(working, WorkingTurn{
			Agent:   identity.Agent,
			Role:    conversation.Role,
			Backend: conversation.Backend,
			Model:   modelOf(conversation.ProviderModel, conversation.ProviderResolvedModel),
			Effort:  strings.TrimSpace(conversation.ProviderEffort),
			Turns:   conversation.Turns,
			Since:   conversation.UpdatedAt,
			Elapsed: now.Sub(conversation.UpdatedAt),
		})
	}
	sort.SliceStable(working, func(first, second int) bool {
		return working[first].Agent < working[second].Agent
	})
	if len(unanswered) > 0 {
		// The conversations that did answer are still reported. What is said beside
		// them is which ones this reading cannot speak for, because a count that
		// silently skipped them would be a count somebody trusts.
		return working, "whether a turn is in flight could not be asked of " + strings.Join(unanswered, "; ")
	}
	return working, ""
}

// Stage is the part of a run a phase belongs to, in the three words a pipeline
// shows: the developer's part, the reviewer's, and the harness's. It is owned
// here so that no surface keeps its own list of which phase is which.
type Stage string

const (
	// StageDeveloping is the developer at work, or about to be: developing,
	// checking, and a run that has not recorded a phase yet, which is one that
	// is still being set up for the developer.
	StageDeveloping Stage = "developing"
	// StageReviewing is the independent review.
	StageReviewing Stage = "reviewing"
	// StageIntegrating is everything after an approval: the promotion and what
	// settles it.
	StageIntegrating Stage = "integrating"
)

// StageOf folds a phase onto its stage. A phase this does not know is the
// harness's, because every phase before the review is named above.
func StageOf(phase runstate.Phase) Stage {
	switch phase {
	case runstate.PhaseDeveloping, runstate.PhaseChecking, "":
		return StageDeveloping
	case runstate.PhaseReviewing:
		return StageReviewing
	default:
		return StageIntegrating
	}
}

// modelOf is the model a record says an invocation ran on: the identifier the
// provider resolved the selector to where it reported one, and the selector
// itself otherwise. A selector such as "opus" floats; the resolved identifier is
// what was actually spent, so it is preferred where the record has it.
func modelOf(selector, resolved string) string {
	if strings.TrimSpace(resolved) != "" {
		return resolved
	}
	return selector
}

// InFlight reports whether a process is holding one agent's conversation right
// now, and why the question could not be answered when it could not. It
// observes the hold and acquires nothing, so asking costs the conversation
// nothing: every surface asks this of every conversation, and the answer must
// never be the reason the next question is answered differently.
//
// A failure to ask is not an answer. Reporting one as in flight would say every
// agent at once was mid-turn whenever the state directory could not be opened,
// which is both wrong and the opposite of what anybody would do about it.
func InFlight(conversations Conversations, identity runstate.ConversationIdentity) (bool, string) {
	inFlight, err := conversations.InFlight(identity)
	if err != nil {
		return false, err.Error()
	}
	return inFlight, ""
}

// switches is what has stopped the harness choosing work, read once for both the
// line that says which items are not startable and the line that says who has to
// act.
type switches struct {
	operator     runstate.OperatorHold
	operatorHeld bool
	intake       runstate.IntakeHold
	intakeHeld   bool
	// providerOutage is the provider answering nobody, and providerAway whether
	// that stands. It is read with the switches because it is read the way they
	// are — one file under the product, present or absent — and said the way
	// they are: as what stops the choosing, and as something waiting on a person.
	providerOutage runstate.ProviderOutage
	providerAway   bool
	// diverged is every target branch recorded as one the harness will not
	// catch up to the remote's.
	diverged []runstate.DivergedTarget
	// pausing are the unresolved directives that stop work, in the order they were
	// recorded.
	pausing []directive.Directive
	// problems are the switches that could not be read. A switch nobody can read is
	// never reported as clear: an operator told nothing is holding the line, by a
	// reading that could not open the hold file, has been told the one thing that
	// is worse than nothing.
	problems []string
}

func readSwitches(sources Sources) switches {
	var read switches
	if sources.OperatorHolds == nil {
		read.problems = append(read.problems, "nothing was wired to read the operator's hold")
	} else if hold, held, err := sources.OperatorHolds.Held(); err != nil {
		read.problems = append(read.problems, fmt.Sprintf("the operator's hold could not be read: %v", err))
	} else {
		read.operator, read.operatorHeld = hold, held
	}
	if sources.IntakeHolds == nil {
		read.problems = append(read.problems, "nothing was wired to read the intake hold")
	} else if hold, held, err := sources.IntakeHolds.Held(); err != nil {
		read.problems = append(read.problems, fmt.Sprintf("the intake hold could not be read: %v", err))
	} else {
		read.intake, read.intakeHeld = hold, held
	}
	if sources.ProviderOutages != nil {
		if outage, standing, err := sources.ProviderOutages.Standing(); err != nil {
			read.problems = append(read.problems, fmt.Sprintf("whether the provider is answering could not be read: %v", err))
		} else {
			read.providerOutage, read.providerAway = outage, standing
		}
	}
	if sources.DivergedTargets != nil {
		if diverged, err := sources.DivergedTargets.Standing(); err != nil {
			read.problems = append(read.problems, fmt.Sprintf("whether a target branch stands diverged from the remote's could not be read: %v", err))
		} else {
			read.diverged = diverged
		}
	}
	if sources.Directives == nil {
		read.problems = append(read.problems, "nothing was wired to read the recorded directives")
	} else if recorded, err := sources.Directives.List(); err != nil {
		read.problems = append(read.problems, fmt.Sprintf("the recorded directives could not be read: %v", err))
	} else {
		for _, given := range recorded {
			if given.Pauses() {
				read.pausing = append(read.pausing, given)
			}
		}
	}
	return read
}

// readNotStartable is every admitted item nothing will pull, with the refusal
// that stops it, how much admitted work this reading saw in all, and the stall
// that is actually holding some of it back.
//
// The refusals come from four places and each is the real one. An entry the
// queue itself calls unready carries the queue's own account. An item whose
// unfinished children already carry its execution carries those children, in the
// backlog's own words, because the scheduling pass will never pull it and an
// operator told otherwise is sent to investigate a stall that is not one. An
// item an unresolved directive pauses carries that directive, because the
// pipeline refuses to commit to the work for exactly that reason. Everything
// else is pullable, and what is left is the pass-level refusal — a switch, a
// full machine, nothing choosing — which is a fact about the harness said once
// against each item it actually stops.
//
// The order the four are asked in is the scheduling pass's own, so an item
// several of them stop is refused here for the same one the pass would name.
//
// The stall it returns is the one that stopped at least one item. A stall over
// an empty queue is a state of the machine and not something waiting on
// anybody, so it is returned as no stall at all rather than as attention nobody
// asked for.
//
// The held counts it returns are counted over the same entries, in-flight work
// left out with the rest of it: they are said in the head of the line the
// refusals are listed under, so a count covering an item the line does not name
// is a head that contradicts what is printed beneath it.
func readNotStartable(ctx context.Context, sources Sources, held switches, running []RunningRun, now time.Time) ([]Refused, heldWork, backlog.Queue, Stall, string) {
	if sources.Tracker == nil {
		return nil, heldWork{}, backlog.Queue{}, Stall{}, "nothing was wired to read the admitted work"
	}
	queue, coverage, gateProblem, err := readQueue(ctx, sources)
	if err != nil {
		return nil, heldWork{}, backlog.Queue{}, Stall{}, fmt.Sprintf("the admitted work could not be read: %v", err)
	}
	// An item a run is already carrying is on the running line. Naming it here as
	// well would report the machine working as work that will not start.
	inFlight := make(map[string]struct{}, len(running))
	for _, run := range running {
		inFlight[run.WorkItemID] = struct{}{}
	}
	// Why nothing more is being chosen, worked out once and by the derivation every
	// other surface reads. It names no reason when the harness would start the next
	// pullable item, which is what makes a pullable item's absence from this line
	// mean something.
	stopped := whyNothingStarts(sources, held, len(running), now)
	refusal := stopped.Refusal()
	// What the stall could not read is said whatever the queue holds. It is a
	// gap in this reading rather than a fact about the work, so a queue with
	// nothing in it must not swallow it below. What could not be read about the
	// gates is carried the same way and for the same reason.
	problem := joinProblems(stopped.Problem, gateProblem)

	// Whether the stall actually stopped anything. A stall over an empty queue is
	// a state of the machine rather than something waiting on a person, and the
	// attention line must not be given one.
	stalled := false
	waits := heldWork{admitted: make([]WorkItemRef, 0, len(queue.Entries)), startable: []WorkItemRef{}}
	refused := make([]Refused, 0, len(queue.Entries))
	groups := newWaitGroups(stopped, held)
	// A full machine refuses nothing. Every developer slot being taken is the
	// harness working, and an item ready behind it is the next one started as a
	// run finishes — so it is counted as startable and on a line of its own, and
	// never filed as not startable. On 2026-09-27 forty-five such items were
	// counted as work "nothing will pull", and the operator asked what he was
	// meant to do about it.
	full := stopped.Reason == ReasonNoCapacity
	if full {
		refusal = ""
	}
	var slotWait *SlotWait
	for _, entry := range queue.Entries {
		waits.admitted = append(waits.admitted, WorkItemRef{WorkItemID: entry.ID, Title: entry.Title})
		if _, carried := inFlight[entry.ID]; carried {
			continue
		}
		paused := pausedBy(held.pausing, entry.ID)
		covering := coverage.Covering(entry.ID)
		switch {
		case !entry.Ready:
			waits.count(entry)
			waits.countCarryOuts(sources.Decisions, entry)
			kind := entry.HoldKind()
			held := Refused{WorkItemID: entry.ID, Title: entry.Title, Reason: entry.Hold(), Kind: kind}
			if kind == backlog.HeldForAPerson {
				held.HeldSince = entry.AwaitingSince
			}
			refused = append(refused, held)
			groups.add(entry, kind)
		case len(covering) > 0:
			// A covered item is not stalled and never will be: nothing is holding it
			// back that clearing a switch or freeing a slot would release, so the
			// pass-level refusal below must not be the one it carries.
			refused = append(refused, Refused{WorkItemID: entry.ID, Title: entry.Title,
				Reason: backlog.CoveredReason(covering), Kind: backlog.HeldCovered})
			groups.add(entry, backlog.HeldCovered)
		case paused != nil:
			refused = append(refused, Refused{WorkItemID: entry.ID, Title: entry.Title,
				Reason: fmt.Sprintf("paused for unresolved directive %s: %s",
					paused.ID, singleLine(paused.Unresolved, maxRefusalBytes)),
				Kind: backlog.HeldByDirective})
			groups.add(entry, backlog.HeldByDirective)
		case refusal != "":
			refused = append(refused, Refused{WorkItemID: entry.ID, Title: entry.Title, Reason: refusal, Kind: backlog.HeldByStall})
			groups.add(entry, backlog.HeldByStall)
			stalled = true
		default:
			// Nothing refuses it: this is the work the harness starts next, now or,
			// on a full machine, as soon as a slot frees.
			ref := WorkItemRef{WorkItemID: entry.ID, Title: entry.Title}
			waits.startable = append(waits.startable, ref)
			if full {
				if slotWait == nil {
					slotWait = &SlotWait{Slots: sources.Capacity, InFlight: len(running), Items: []WorkItemRef{}}
				}
				slotWait.Ready++
				slotWait.Items = append(slotWait.Items, ref)
			}
		}
	}
	if !stalled {
		stopped = Stall{}
	}
	oldestHoldFirst(refused)
	if slotWait != nil {
		slotWait.said()
	}
	waits.groups = groups.list()
	waits.slotWait = slotWait
	if len(held.problems) > 0 {
		problem = joinProblems(problem, strings.Join(held.problems, "; "))
	}
	return refused, waits, queue, stopped, problem
}

// oldestHoldFirst orders the items held after a stopped run by how long each has
// been held, oldest first, in the places those items already take in the list;
// every other refusal keeps its place in the product manager's order. The held
// items are the ones somebody has to act on, and a reader scanning thirty of them
// wants the one that has waited longest at the top, not wherever its priority
// put it. One whose moment is unknown goes after those whose moment is known,
// in the order it arrived.
func oldestHoldFirst(refused []Refused) {
	var places []int
	var held []Refused
	for place, item := range refused {
		if item.Kind == backlog.HeldForAPerson {
			places = append(places, place)
			held = append(held, item)
		}
	}
	sort.SliceStable(held, func(first, second int) bool {
		one, other := held[first].HeldSince, held[second].HeldSince
		switch {
		case one == nil:
			return false
		case other == nil:
			return true
		default:
			return one.Before(*other)
		}
	})
	for index, place := range places {
		refused[place] = held[index]
	}
}

// heldWork is how much of a reading's not-startable work is held, split by whose
// move it is. It is a pair rather than one figure because they are two different
// people to go to, and it is counted where the refusals are so that the head of
// that line and the entries under it describe one set of items.
type heldWork struct {
	awaitingDecision int
	awaitingCarryOut int
	// startable is the other side of the same count: the entries nothing
	// refuses, named rather than counted so the surface that lists them lists
	// the entries the count was taken over. admitted is every entry the reading
	// saw, in the same order, for the same reason.
	startable []WorkItemRef
	admitted  []WorkItemRef
	// carryOutsRefused and carryOutsUnattempted are the held items' recorded
	// decisions the harness has not carried out, by what became of them.
	carryOutsRefused     int
	carryOutsUnattempted int
	// groups is the refused items counted by what they wait on, and slotWait
	// the ready items waiting only for a developer slot.
	groups   []WaitGroup
	slotWait *SlotWait
}

// countCarryOuts counts one held item's decisions a gate refused and ones no
// pass attempted, from the item's own triage record. A record that cannot be
// read counts nothing here: the hold's own reason already says the record could
// not be read, which is where that is stated.
func (h *heldWork) countCarryOuts(decisions Decisions, entry backlog.Entry) {
	if held, _ := entry.Awaits(); !held || decisions == nil {
		return
	}
	counters, err := decisions.Counters(entry.ID)
	if err != nil {
		return
	}
	refused, unattempted := counters.CarryOutFindings()
	h.carryOutsRefused += refused
	h.carryOutsUnattempted += unattempted
}

func (h *heldWork) count(entry backlog.Entry) {
	switch held, carryOut := entry.Awaits(); {
	case !held:
	case carryOut:
		h.awaitingCarryOut++
	default:
		h.awaitingDecision++
	}
}

// whyNothingStarts is the pass-level stall: the reason a pullable item with
// nothing wrong with it is still not being started, from the records this
// reading holds. The derivation itself is shared, because a channel and a
// terminal that worked this out separately would be two answers to the one
// question an operator asks.
func whyNothingStarts(sources Sources, held switches, running int, now time.Time) Stall {
	conditions := Conditions{
		OperatorHold:   held.operator,
		OperatorHeld:   held.operatorHeld,
		IntakeHold:     held.intake,
		IntakeHeld:     held.intakeHeld,
		ProviderOutage: held.providerOutage,
		ProviderAway:   held.providerAway,
		Diverged:       held.diverged,
		Running:        running,
		Capacity:       sources.Capacity,
		// The reading's own moment, so a provider's usage window this line reports
		// as standing is one that had not lifted when the rest of these lines were
		// read.
		Now: now,
	}
	// The watch log costs a read, so it is passed as the question rather than the
	// answer: the derivation asks for it only where the switches and the capacity
	// did not already settle what has stopped the choosing.
	if sources.Sessions != nil {
		conditions.Sessions = sources.Sessions.List
	}
	return WhyNothingStarts(conditions)
}

// Choosing is the watch sessions still alive, latest transition per session. It
// reads the log per session rather than taking its last line, because one log
// holds every session a product has had and nothing stops two running at once: a
// last entry can be one session stopping while another carries on watching, and
// a reading that took it at face value would report a line that is being pulled
// from as one nobody is pulling from.
//
// It is exported and lives here for the reason the whole package does: which
// sessions are alive is one derivation, and two surfaces folding the same log
// their own way is how they come to disagree.
func Choosing(sessions []runstate.WatchTransition) []runstate.WatchTransition {
	return alive(sessions, func(state runstate.WatchState) bool {
		switch state {
		case runstate.WatchStopped, runstate.WatchIdle, runstate.WatchBlocked:
			// A stopped session is gone; idle and blocked sessions are alive but
			// choosing nothing — the same answer to "is anything being pulled".
			return false
		default:
			return true
		}
	})
}

// Live is the watch sessions that have not stopped, latest transition per
// session, including the idle ones. It is the other reading of the same fold: a
// session polling an empty queue is not choosing anything and is very much a
// process that is running, and the two questions have opposite answers about it.
func Live(sessions []runstate.WatchTransition) []runstate.WatchTransition {
	return alive(sessions, func(state runstate.WatchState) bool {
		return state != runstate.WatchStopped
	})
}

// alive folds a watch log to the last transition of each session and keeps the
// ones a caller counts as still going, newest first. A note about one of a
// session's dispatches is not a transition of the session, so it is read past;
// see runstate.WatchTransition.Note.
func alive(sessions []runstate.WatchTransition, keep func(runstate.WatchState) bool) []runstate.WatchTransition {
	last := make(map[string]runstate.WatchTransition, len(sessions))
	for _, transition := range sessions {
		if transition.Note() {
			continue
		}
		if recorded, seen := last[transition.SessionID]; seen && recorded.At.After(transition.At) {
			continue
		}
		last[transition.SessionID] = transition
	}
	kept := make([]runstate.WatchTransition, 0, len(last))
	for _, transition := range last {
		if keep(transition.State) {
			kept = append(kept, transition)
		}
	}
	sort.SliceStable(kept, func(first, second int) bool {
		if !kept[first].At.Equal(kept[second].At) {
			return kept[first].At.After(kept[second].At)
		}
		return kept[first].SessionID < kept[second].SessionID
	})
	return kept
}

// Queue is the admitted work as the standing reading assembles it, for a surface
// that lists the queue's own facts about an item rather than the four lines —
// `yoyo gate list` reads which items are held by a step only a person can take
// from here. It is exported so that such a surface is a projection of this
// derivation rather than a parallel assembly of it: two readers each calling the
// gate reader over their own choice of tracker slices agree only for as long as
// nobody changes one of them. The problem it returns is readQueue's — what could
// not be read about the gates, where that changed the answer.
func Queue(ctx context.Context, sources Sources) (backlog.Queue, string, error) {
	if sources.Tracker == nil {
		return backlog.Queue{}, "", errors.New("nothing was wired to read the admitted work")
	}
	queue, _, problem, err := readQueue(ctx, sources)
	return queue, problem, err
}

// readQueue assembles the admitted work in the product manager's order, and the
// coverage over it, from the same five readings the scheduler makes: the
// listings that carry the order, the tracker's own account of what can be
// pulled, what the harness is holding for a person, the human gates a person
// has recorded passing, and the work that has already been pulled.
//
// The gates are the one of the four whose absence is survivable, so they are
// reported as a problem rather than as a failure. Nothing discharged means every
// declared gate holds, which overstates what is waiting on a person and never
// understates it — and an overstated gate is a line an operator can read and
// dismiss, where the other direction is work started past somebody's step.
func readQueue(ctx context.Context, sources Sources) (backlog.Queue, backlog.Coverage, string, error) {
	var admitted []beads.WorkItem
	for _, status := range backlogStatuses {
		items, err := sources.list(ctx, status)
		if err != nil {
			return backlog.Queue{}, nil, "", err
		}
		admitted = append(admitted, items...)
	}
	trackerCtx, cancel := sources.bounded(ctx)
	defer cancel()
	ready, err := sources.Tracker.Ready(trackerCtx)
	if err != nil {
		return backlog.Queue{}, nil, "", fmt.Errorf("list the work items the tracker reports as ready: %w", err)
	}
	pullable := make([]string, 0, len(ready))
	for _, item := range ready {
		pullable = append(pullable, item.ID)
	}
	var held backlog.Holds
	if sources.Stoppages != nil {
		held, err = HeldForAPerson(ctx, sources.Stoppages, sources.Decisions, sources.Remains)
		if err != nil {
			return backlog.Queue{}, nil, "", fmt.Errorf("read what the harness is holding back after stopped runs: %w", err)
		}
	}
	var discharged map[string][]string
	unread := ""
	switch {
	case sources.Gates == nil:
		unread = "nothing was wired to read them"
	default:
		passed, err := sources.Gates.DischargedGates()
		if err != nil {
			unread = err.Error()
		} else {
			discharged = passed
		}
	}
	queue := backlog.Order(admitted, pullable, held, discharged)
	// What could not be read is reported only where it changed the answer. No
	// admitted item declares a gate on most readings, and a line that announced an
	// unread source every time would be a line reporting a gap that cost nothing —
	// which is how a caveat stops being read. Where it did cost something, the
	// count is said, because that is exactly what is being overstated.
	problem := ""
	if unread != "" && queue.Gated() > 0 {
		problem = fmt.Sprintf("the human gates a person has passed could not be read (%s), so all %d gated item(s) are reported as still waiting on somebody",
			unread, queue.Gated())
	}
	// Coverage is read one status wider than the order, exactly as the scheduling
	// pass reads it. Leaving the claimed slice out would report an epic whose
	// child a run is carrying right now as pullable, which is the shape of the
	// disagreement this line exists to have none of.
	claimed, err := sources.list(ctx, backlog.StatusClaimed)
	if err != nil {
		return backlog.Queue{}, nil, "", err
	}
	return queue, backlog.Cover(queue, admitted, claimed), problem, nil
}

// readReports is how the collected pile stands. A pile that could not be read
// says so and reports no counts at all: a zero here would read as a channel
// nobody has filed into, which is the one thing a broken read of it must never
// look like.
func readPile(sources Sources) ([]report.Report, []report.Handling, string) {
	if sources.Reports == nil {
		return nil, nil, "nothing was wired to read what the roles have reported"
	}
	reports, err := sources.Reports.List()
	if err != nil {
		return nil, nil, fmt.Sprintf("the collected reports could not be read: %v", err)
	}
	handlings, err := sources.Reports.Handlings()
	if err != nil {
		// The pile is readable and what became of it is not, so every report would
		// count as undecided. That overstates the backlog in the direction that
		// sends somebody to work on something already done, so no counts are given
		// at all and the gap is named.
		return nil, nil, fmt.Sprintf("what became of the collected reports could not be read, so how many are still waiting cannot be said: %v", err)
	}
	return reports, handlings, ""
}

// summarizePile is how the pile stands, from one reading of it, or the stated
// absence where it could not be read.
func summarizePile(reports []report.Report, handlings []report.Handling, problem string, now time.Time) (report.Pile, string) {
	if problem != "" {
		return report.Pile{}, problem
	}
	return report.Summarize(reports, handlings, now), ""
}

// readNeedsHuman is everything waiting on a person, with whose move it is.
//
// What is here and what is not is the whole of the line's value. A switch
// somebody placed, a directive nobody settled, a proposal nobody decided, a run
// that owes a step, held work, and work marked for a conversation are all
// waiting on somebody named and will wait forever without them. A parked item is
// not: parking is a decision already taken, and listing it would tell an
// operator to act on something somebody deliberately settled.
//
// Held work is the one entry here whose mover can be the harness rather than a
// person, and it is on this line for exactly that reason: an operator scanning
// for what is waiting on him has to be able to see which of it is not.
//
// A finding only the operator can act on is named ahead of the undecided
// proposals and is never folded into the remainder: the brake's hold, with the
// runs that tripped it, and each report-derived finding by name. Those are the
// entries whose wait was measured in weeks before they were named here.
func readNeedsHuman(sources Sources, held switches, actions []Attention, amendments amendmentReading) ([]Attention, string) {
	attention := make([]Attention, 0, 4+len(actions))
	if held.operatorHeld {
		attention = append(attention, operatorHoldAttention(held.operator))
	}
	if held.intakeHeld {
		// Who placed it is on the record and is said with it: the same switch is
		// placed by the operator and by the harness's own failure-storm brake,
		// and an operator told this hold is theirs when the brake placed it goes
		// looking for a decision they never made. Whose move it is comes from
		// the same record: the operator's hold is theirs, and the brake's is the
		// development manager's while she decides, the harness's while a probe
		// runs, and the operator's only once she has escalated it — with the
		// probe named where one is in flight, so the line says what is being
		// tried rather than only that something is.
		attention = append(attention, intakeHoldAttention(held.intake))
	}
	for _, paused := range held.pausing {
		attention = append(attention, directiveAttention(paused))
	}
	attention = append(attention, actions...)
	problem := strings.Join(held.problems, "; ")

	// The proposed changes, read once above: each undecided one, after the
	// batches their owners argued, which are among the findings above.
	// A queue that could not be read is said here as well as in its own field,
	// for the reason the pile's failure is: the field is what a script reads and
	// the line is what a person reads.
	problem = joinProblems(problem, joinProblems(amendments.problem, amendments.sweepProblem))
	attention = append(attention, amendments.attention()...)

	decisions, decisionsProblem := readProductDecisions(sources)
	attention = append(attention, decisions...)
	problem = joinProblems(problem, decisionsProblem)

	if sources.Runs == nil {
		problem = joinProblems(problem, "nothing was wired to read the runs that owe a step")
		return attention, problem
	}
	// A run that owes a step and a promotion the forge has not published are two
	// entries where one run is both — a merge the forge still has queued — because
	// they are two different waits: the step is the harness's to take, and the
	// publication is whoever's the entry names.
	if outstanding, err := sources.Runs.Outstanding(); err != nil {
		problem = joinProblems(problem, fmt.Sprintf("the runs that owe a step could not be read: %v", err))
	} else {
		for _, state := range outstanding {
			if !state.Status.Terminal() {
				continue
			}
			if presence, ok := sources.Runs.(RunHolder); ok {
				held, err := presence.Held(state.RunID)
				if err != nil {
					problem = joinProblems(problem, fmt.Sprintf("the process holding run %s could not be read: %v", state.RunID, err))
					continue
				}
				if held {
					continue
				}
			}
			if state.PullRequest != nil && (state.PullRequest.Superseded != "" || state.PullRequest.HandedBack != nil) {
				// The obsolete publication owes no merge, but it may still owe
				// landing settlement or local cleanup. Ask the same outstanding
				// predicate with only that merge obligation removed.
				remaining := state
				pr := *state.PullRequest
				pr.MergeQueued = false
				remaining.PullRequest = &pr
				if !remaining.Outstanding() {
					continue
				}
			}
			attention = append(attention, owedStepAttention(state))
		}
	}
	// What is awaiting the forge is read by the record's own predicate, over every
	// recorded run, because it is the same reading the channel's heartbeat counts:
	// a count said hourly in a channel and a line absent from the terminal is the
	// disagreement this whole package exists to prevent.
	if recorded, err := sources.Runs.Recorded(); err != nil {
		problem = joinProblems(problem, fmt.Sprintf("the promotions awaiting the forge could not be read: %v", err))
	} else {
		for _, state := range recorded {
			if len(state.ReconcileFindings) > 0 && (!state.Status.Terminal() || !state.Outstanding()) && !state.AwaitingForge() {
				attention = append(attention, ReconcileFindingAttention(state))
			}
		}
		for _, state := range AwaitingForge(recorded) {
			attention = append(attention, awaitingForgeAttention(state))
		}
	}
	return attention, problem
}

// readProductDecisions is every product decision about a run still in flight
// that the development manager has not answered, read off the docket she decides
// from. A run that has ended is left out: there is nothing left to stop, and the
// next docket build settles its entry. Where the runs cannot be read, every
// unanswered decision is named rather than none.
func readProductDecisions(sources Sources) ([]Attention, string) {
	if sources.Docket == nil {
		return nil, ""
	}
	entries, err := sources.Docket.List()
	if err != nil {
		return nil, fmt.Sprintf("the product decisions about runs in flight could not be read: %v", err)
	}
	var inFlight map[string]bool
	if sources.Runs != nil {
		if running, err := sources.Runs.Incomplete(); err == nil {
			inFlight = make(map[string]bool, len(running))
			for _, state := range running {
				inFlight[state.RunID] = true
			}
		}
	}
	now := sources.now()
	var attention []Attention
	for _, entry := range entries {
		if entry.Class != triage.ClassProductDecision || entry.ProductDecision == nil || !entry.Undecided(now) {
			continue
		}
		if inFlight != nil && !inFlight[entry.RunID] {
			continue
		}
		attention = append(attention, productDecisionAttention(entry))
	}
	return attention, ""
}

// Held is the admitted work somebody has to release, as attention rather than as
// queue entries: how many items wait on the development manager's decision and
// how many wait on the harness carrying out decisions she has already recorded.
//
// The two are separate entries because they are separate people, and that is the
// whole of what this exists for. Held work was named on the attention line only
// through the queue's own refusals, one per item and all of them wording one
// state, so an operator counting them read every held item as a decision
// somebody owed. On 2026-09-07 that was thirty-three items and none of them: the
// development manager had decided each one, and the gap was the harness never
// carrying them out.
//
// Counts rather than identifiers, for the reason the attention line bounds
// everything else it carries: what this line answers is who has to move, and a
// reader who wants the items has the not-startable line above it, which names
// them with the reason against each.
func Held(awaitingDecision, awaitingCarryOut int) []Attention {
	attention := make([]Attention, 0, 2)
	if awaitingDecision > 0 {
		attention = append(attention, heldWorkAttention(HeldAwaitingDecision, awaitingDecision))
	}
	if awaitingCarryOut > 0 {
		attention = append(attention, heldWorkAttention(HeldAwaitingCarryOut, awaitingCarryOut))
	}
	return attention
}

// awaits agrees the verb with the count, because a line that says "1 admitted
// item await" is one a reader stops trusting the arithmetic of.
func awaits(count int) string {
	if count == 1 {
		return "awaits"
	}
	return "await"
}

// HandedOff is the admitted work no run will ever carry, as attention rather
// than as a queue entry: the item is done in the conversation it names, and the
// wait between the handoff and somebody opening that conversation is the longest
// silence any of this has.
//
// It is derived from the same queue the not-startable line reads, and it is a
// separate function because the two lines say different things about the same
// item: one says why nothing pulls it, and this says who has to move.
func HandedOff(queue backlog.Queue) []Attention {
	attention := make([]Attention, 0, len(queue.Entries))
	for _, entry := range queue.Entries {
		if entry.Executor.DeveloperRun() {
			continue
		}
		attention = append(attention, carriedItemAttention(entry.ID, entry.Executor))
	}
	return attention
}

// Pausing is every directive holding one work item up, for a surface that has to
// name them rather than count them: a thread settling what it is waiting on has
// to know whether that is one thing or several before it settles anything.
//
// It is here rather than in the surface that asks, and it reads through the same
// source the four lines do, because which directives hold an item is the reading
// the run pipeline enforces on: a channel that worked it out its own way could
// lift a pause the pipeline still holds. Nothing about it revises the record —
// this says what is in force, and settling one stays with the acts that settle
// directives. That division is the architect's standing ruling rather than this
// package's preference; docs/developing-yoyo.md records which ruling and what it
// said, under "Where a surface reads the work's own state from".
//
// A directive that named no scope holds every item, so it is here too. That is
// the pipeline's own reading and the honest one: it is what stops this item, and
// settling it from here settles it wherever else it was stopping work. A surface
// acting on this says so where it acts — a settlement that reached past the item
// somebody was looking at is the one thing they could not have known from the
// thread they were in.
func Pausing(sources Sources, workItemID string) ([]directive.Directive, error) {
	if sources.Directives == nil {
		return nil, errors.New("nothing was wired to read the recorded directives")
	}
	recorded, err := sources.Directives.List()
	if err != nil {
		return nil, fmt.Errorf("the recorded directives could not be read: %w", err)
	}
	pausing := directive.Pausing(recorded, workItemID)
	directive.Sort(pausing)
	return pausing, nil
}

// Gated is the admitted work held by a step only a person can take, as
// attention rather than as a queue entry: the item is not waiting on anything
// the harness will ever finish, and until somebody records the act it will sit
// exactly where it is.
//
// It is derived from the same queue the not-startable line reads, and it is a
// separate function for the reason HandedOff beside it is: the two lines say
// different things about one item, and one of them has to say whose move it is.
func Gated(queue backlog.Queue) []Attention {
	attention := make([]Attention, 0, len(queue.Entries))
	for _, entry := range queue.Entries {
		if !entry.HumanGates.Holds() {
			continue
		}
		for _, gate := range entry.HumanGates.Gates {
			attention = append(attention, humanGateAttention(entry.ID, HumanGateWait{Gate: gate.Name, Statement: gate.Statement}))
		}
		// A declaration nothing could read holds the item exactly as a gate does,
		// and is on this line for the same reason: it will wait forever without a
		// person. What it waits for is different, so what it says is different —
		// there is no name to record an act against, and the author has to correct
		// the line.
		for _, unreadable := range entry.HumanGates.Unreadable {
			attention = append(attention, humanGateAttention(entry.ID, HumanGateWait{Unreadable: unreadable}))
		}
	}
	return attention
}

// humanGateAttention is one step an item reserves for a person, as the
// attention line carries it. It is the operator's move, because recording the
// act is his; an unreadable declaration is his to see and its author's to
// correct, and the sentence says so.
func humanGateAttention(workItemID string, wait HumanGateWait) Attention {
	return Attention{Kind: AttentionHumanGate, ID: wait.Gate, Mover: MoverOperator, WorkItemID: workItemID, HumanGate: &wait}
}

// pausedBy is the unresolved directive that stops one item, or nothing.
func pausedBy(pausing []directive.Directive, workItemID string) *directive.Directive {
	for index := range pausing {
		if pausing[index].Affects(workItemID) {
			return &pausing[index]
		}
	}
	return nil
}

func (s Sources) list(ctx context.Context, status string) ([]beads.WorkItem, error) {
	trackerCtx, cancel := s.bounded(ctx)
	defer cancel()
	items, err := s.Tracker.List(trackerCtx, status)
	if err != nil {
		return nil, fmt.Errorf("list %s work items: %w", status, err)
	}
	return items, nil
}

// bounded gives one tracker command its own deadline, so a tracker that will not
// answer costs this reading a line rather than hanging whatever asked for it. A
// caller that configured no bound gets the context it passed in.
func (s Sources) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.TrackerTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, s.TrackerTimeout)
}

func (s Sources) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// joinProblems puts two accounts of what could not be read on one line, and
// keeps whichever of them there is when there is only one.
func joinProblems(first, second string) string {
	switch {
	case strings.TrimSpace(first) == "":
		return second
	case strings.TrimSpace(second) == "":
		return first
	default:
		return first + "; " + second
	}
}

// singleLine folds prose somebody wrote into the one line a status can carry,
// and says where it was cut. The fold and the cut are oneline's, on a rune
// boundary, because a line truncated mid-rune is not text; the mark is this
// package's own ellipsis, which every status line already ends a cut with.
func singleLine(text string, limit int) string {
	bounded := oneline.Bound(text, limit)
	if len(bounded) == len(strings.Join(strings.Fields(text), " ")) {
		return bounded
	}
	return bounded + "…"
}
