package readmodel

// One thing waiting on a person, carried as the record it is about.
//
// Until yoyodyne-ifd.432.5 an attention entry was two sentences: what is
// waiting, and whose move it is. A sentence can be printed and nothing else — a
// surface that wants to show a proposed change in full, count the entries by
// who has to move, or act on one has nothing to open, nothing to group by, and
// nothing to name in the act. So an entry now carries the thing: its kind, from
// a closed vocabulary; the identifier of the record it is about; who moves
// next, from a second closed vocabulary; and the record itself, whole, where
// there is one — an amendment's target document, proposer, change, and reason
// among them.
//
// The two sentences are still what a terminal prints, and they are derived
// here from those fields rather than stored beside them. That is what makes
// the record and the line one thing: nothing can carry a sentence that says
// one thing over fields that say another, because the sentence is never
// written down. The JSON a script or a page reads carries both, the fields and
// the sentences computed from them at the moment of writing, so a reader that
// only wants the line still has it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// AttentionKind is what sort of record one attention entry is about. The set
// is closed, for the reason the stall's reasons are: a kind nobody named is a
// kind no surface can show or act on, and the entry an operator most needs is
// exactly the one nobody thought to give a name.
type AttentionKind string

const (
	// AttentionAmendment is a change proposed to a document its proposer does
	// not own, which nobody has decided.
	AttentionAmendment AttentionKind = "amendment"
	// AttentionCarriedItem is an admitted work item marked for a conversation
	// rather than a developer run.
	AttentionCarriedItem AttentionKind = "conversation-carried-item"
	// AttentionReports is the collected report pile, once its oldest undecided
	// report has waited longer than any working cadence would leave it.
	AttentionReports AttentionKind = "report"
	// AttentionAmendmentQueue is the queue of proposed changes, once its oldest
	// undecided proposal has waited longer than any working cadence would leave
	// it: the report pile's sibling.
	AttentionAmendmentQueue AttentionKind = "amendment-queue"
	// AttentionOwedStep is a run that ended still owing a step.
	AttentionOwedStep AttentionKind = "owed-step"
	// AttentionPublication is a promotion the forge has not published.
	AttentionPublication AttentionKind = "publication"
	// AttentionDegradedService is a part of the product its supervisor has
	// stopped restarting.
	AttentionDegradedService AttentionKind = "degraded-service"
	// AttentionFailingTask is a product pass whose executions keep failing, or
	// the earlier signal from a recurring task repeatedly refused before a turn.
	AttentionFailingTask AttentionKind = "failing-task"
	// AttentionConfigMismatch is a running part of the product whose build
	// cannot read keys the configuration it reads now carries, so every read
	// it makes of the file fails. The part is the ID.
	AttentionConfigMismatch AttentionKind = "config-mismatch"
	// AttentionHold is one of the switches over what the harness does: the
	// operator's hold over everything, the intake hold over what it chooses for
	// itself, and the provider holding every role at once. The entry's ID says
	// which of the three.
	AttentionHold AttentionKind = "hold"
	// AttentionDirective is a directive that pauses work and nobody has
	// resolved.
	AttentionDirective AttentionKind = "directive"
	// AttentionOutage is the provider answering nobody.
	AttentionOutage AttentionKind = "outage"
	// AttentionStall is a queue nothing is pulling from while admitted work
	// waits behind that: a session sitting idle over it, or no session at all.
	AttentionStall AttentionKind = "stall"
	// AttentionHeldWork is the admitted work somebody has to release, counted
	// by whose move it is rather than named item by item.
	AttentionHeldWork AttentionKind = "held-work"
	// AttentionOperatorAction is a finding only the operator can act on: a
	// report handled as needing his hand, a critical report nobody has handled,
	// or a stopped run the development manager escalated to him. It is always
	// named and never counted into the remainder; see Named.
	AttentionOperatorAction AttentionKind = "operator-action"
	// AttentionProductDecision is the Lead Product Manager's decision that an
	// item whose run is in flight is superseded, narrowed, or to be retired,
	// which the development manager has not yet answered by stopping the run or
	// letting it finish.
	AttentionProductDecision AttentionKind = "product-decision"
	// AttentionHumanGate is an admitted work item declaring a step only a
	// person can take, which nobody has recorded taking — or a declaration of
	// one that nothing could read. The item is the WorkItemID and the gate's
	// name is the ID.
	AttentionHumanGate AttentionKind = "human-gate"
	// AttentionUntracedPass is a role's last pass that reported findings and
	// left no trace of them outside its account. The task is the ID, and the
	// role is the mover.
	AttentionUntracedPass AttentionKind = "untraced-pass"
	// AttentionFactoryStall is the factory having pulled no work and completed
	// no recurring pass for longer than its configured limit. The moment it last
	// did either is the ID.
	AttentionFactoryStall AttentionKind = "factory-stall"
	// AttentionTrackerUnanswered is the tracker failing listings after their
	// retries, since the moment the first of them failed. That moment is the ID.
	AttentionTrackerUnanswered AttentionKind = "tracker-unanswered"
)

// AttentionKinds is the whole vocabulary, so a test that has to cover every
// kind reads it from here rather than repeating the list.
func AttentionKinds() []AttentionKind {
	return []AttentionKind{
		AttentionAmendment,
		AttentionCarriedItem,
		AttentionReports,
		AttentionAmendmentQueue,
		AttentionOwedStep,
		AttentionPublication,
		AttentionDegradedService,
		AttentionFailingTask,
		AttentionConfigMismatch,
		AttentionHold,
		AttentionDirective,
		AttentionOutage,
		AttentionStall,
		AttentionHeldWork,
		AttentionOperatorAction,
		AttentionProductDecision,
		AttentionHumanGate,
		AttentionUntracedPass,
		AttentionFactoryStall,
		AttentionTrackerUnanswered,
	}
}

// Valid reports whether a token is one of the kinds.
func (k AttentionKind) Valid() bool {
	for _, known := range AttentionKinds() {
		if k == known {
			return true
		}
	}
	return false
}

// Label is the plain wording every surface uses for an attention entry.
// The machine-facing kind stays unchanged.
func (a Attention) Label() string {
	if a.Kind == AttentionOwedStep {
		if pr := a.OwedStep.queuedMerge(); pr != nil {
			if pr.Checks != nil && pr.Checks.Red() {
				return "merge stuck"
			}
			return "merge waiting"
		}
		return "run not finished"
	}
	if a.Kind == AttentionPublication {
		if p := a.Publication; p != nil && ((p.MergeDrop != nil && (p.PullRequest == nil || !p.PullRequest.MergeQueued)) || (p.PullRequest != nil && p.PullRequest.Checks != nil && p.PullRequest.Checks.Red())) {
			return "merge stuck"
		}
		return "merge waiting"
	}
	return map[AttentionKind]string{
		AttentionAmendment:         "proposed document change",
		AttentionCarriedItem:       "work in conversation",
		AttentionReports:           "reports waiting",
		AttentionAmendmentQueue:    "document changes waiting",
		AttentionDegradedService:   "service down",
		AttentionFailingTask:       "scheduled task failing",
		AttentionConfigMismatch:    "service cannot read configuration",
		AttentionHold:              "work paused",
		AttentionDirective:         "direction unresolved",
		AttentionOutage:            "provider unavailable",
		AttentionStall:             "work not starting",
		AttentionHeldWork:          "work waiting",
		AttentionOperatorAction:    "person needed",
		AttentionProductDecision:   "work decision waiting",
		AttentionHumanGate:         "person's step waiting",
		AttentionUntracedPass:      "findings not recorded",
		AttentionFactoryStall:      "nothing completing",
		AttentionTrackerUnanswered: "tracker not answering",
	}[a.Kind]
}

// The three switches an AttentionHold entry can be about, as its ID names them.
// None of the three is a record with an identifier of its own — each is one
// file under the product, present or absent — so the name of the switch is
// what identifies it.
const (
	HoldOperator = "operator"
	HoldIntake   = "intake"
	HoldCapacity = "capacity"
)

// Mover is who has to act next on one thing waiting: the operator, one of the
// harness's roles, the harness itself, the forge, or nobody. It is the
// vocabulary a surface counts the attention line by, so it is a closed set of
// tokens rather than the possessive a sentence prints — the possessive is
// derived from it, below, and is the one wording every sentence on the line
// opens with.
type Mover = ownership.Mover

const (
	MoverOperator           = ownership.MoverOperator
	MoverHarness            = ownership.MoverHarness
	MoverForge              = ownership.MoverForge
	MoverProvider           = ownership.MoverProvider
	MoverNobody             = ownership.MoverNobody
	MoverUnnamed            = ownership.MoverUnnamed
	MoverProductManager     = ownership.MoverProductManager
	MoverArchitect          = ownership.MoverArchitect
	MoverDevelopmentManager = ownership.MoverDevelopmentManager
	MoverProgramManager     = ownership.MoverProgramManager
)

func MoverOf(role domain.AgentRole) Mover { return ownership.MoverOf(role) }

func Movers() []Mover { return ownership.Movers() }

// LaneReportMovers is the part of this vocabulary a program manager's lane
// report may name as what a blocker is waiting on: the people and parts of the
// line a lane can be held up by. It is declared here, beside the vocabulary it
// narrows, so the lane report and the attention line cannot come to disagree
// about what a mover is called.
func LaneReportMovers() []Mover {
	return []Mover{
		MoverOperator,
		MoverProductManager,
		MoverDevelopmentManager,
		MoverArchitect,
		MoverHarness,
		MoverForge,
		MoverProvider,
	}
}

// CheckLaneReportMover refuses a token that is not one of LaneReportMovers. It
// is the one conversion from a lane report's stored token to this vocabulary,
// and it is what the lane report store and the lane report block are checked
// with.
func CheckLaneReportMover(token string) error {
	allowed := LaneReportMovers()
	for _, mover := range allowed {
		if Mover(token) == mover {
			return nil
		}
	}
	names := make([]string, 0, len(allowed))
	for _, mover := range allowed {
		names = append(names, string(mover))
	}
	return fmt.Errorf("waiting_on %q is not a mover a blocker may wait on; the movers are %s", token, strings.Join(names, ", "))
}

// Attention is one thing waiting on somebody: what kind of thing, which one,
// whose move it is, and the record itself where there is one. The move is half
// the fact — a thread that says something is waiting without saying who on is
// the silence this whole surface exists to end — and the record is the other
// half a surface needs to show the thing or act on it.
//
// Exactly one of the record fields is set, the one the kind names; the rest
// are absent from the JSON. What and Whose are not fields: they are the two
// sentences derived from these, and the JSON carries them computed.
type Attention struct {
	Kind AttentionKind `json:"kind"`
	// ID is the identifier of the record the entry is about: an amendment's
	// id, a directive's, a run's, a work item's, a service's name, a recurring
	// task's name, or the
	// switch an AttentionHold entry names. It is empty on the two entries that
	// are about a set rather than a record — the report pile and held work —
	// and on the stall and the outage it is the stall's reason and the
	// outage's cause, which is what identifies each of those — save a diverged
	// target's stall, keyed to its branch as `diverged-target:<branch>`, since
	// two branches can be diverged at once.
	ID    string `json:"id,omitempty"`
	Mover Mover  `json:"mover"`
	// WorkItemID is the admitted work item the entry is about, where it is
	// about one: the carried item itself, the item a run was carrying, the
	// item an amendment's proposer was working on.
	WorkItemID string `json:"work_item_id,omitempty"`

	// Amendment is the proposed change whole, on an AttentionAmendment entry:
	// the target document, its kind and owner, the proposer's role, agent,
	// run, and work item, the change, and why.
	Amendment *amendment.Proposal `json:"amendment,omitempty"`
	// Directive is the unresolved directive whole, on an AttentionDirective
	// entry.
	Directive *directive.Directive `json:"directive,omitempty"`
	// OperatorHold, IntakeHold, and CapacityHold are the switch an
	// AttentionHold entry is about, one of them set to match the ID.
	OperatorHold *runstate.OperatorHold `json:"operator_hold,omitempty"`
	IntakeHold   *runstate.IntakeHold   `json:"intake_hold,omitempty"`
	CapacityHold *CapacityHold          `json:"capacity_hold,omitempty"`
	// Outage is the provider's outage record, on an AttentionOutage entry.
	Outage *runstate.ProviderOutage `json:"outage,omitempty"`
	// Stall is the stall as the not-startable line derived it, on an
	// AttentionStall entry.
	Stall *Stall `json:"stall,omitempty"`
	// Reports is how the pile stands, on an AttentionReports entry.
	Reports *report.Pile `json:"reports,omitempty"`
	// AmendmentQueue is how the queue of proposed changes stands, on an
	// AttentionAmendmentQueue entry.
	AmendmentQueue *amendment.Queue `json:"amendment_queue,omitempty"`
	// Service is the supervisor's record of the part it left down, on an
	// AttentionDegradedService entry.
	Service *runstate.SupervisedChild `json:"service,omitempty"`
	// FailingTask is the task, its cause, and how many firings in a row, on an
	// AttentionFailingTask entry; the task is the ID.
	FailingTask *FailingTask `json:"failing_task,omitempty"`
	// ConfigMismatch is the part, its build, and the keys it cannot read, on an
	// AttentionConfigMismatch entry; the running instance is the ID.
	ConfigMismatch *runstate.ConfigMismatch `json:"config_mismatch,omitempty"`
	// OwedStep is where the run stopped, on an AttentionOwedStep entry; the
	// run is the ID and its item is WorkItemID.
	OwedStep *OwedStep `json:"owed_step,omitempty"`
	// Publication is the promotion and what the forge holds of it, on an
	// AttentionPublication entry; the run is the ID and its item is
	// WorkItemID.
	Publication *Publication `json:"publication,omitempty"`
	// HeldWork is which wait and how many items are in it, on an
	// AttentionHeldWork entry.
	HeldWork *HeldWork `json:"held_work,omitempty"`
	// Executor is the marker that hands the item to a conversation, on an
	// AttentionCarriedItem entry; the item is WorkItemID.
	Executor domain.WorkItemExecutor `json:"executor,omitempty"`
	// OperatorAction is the finding whole, on an AttentionOperatorAction entry;
	// its key is the ID.
	OperatorAction *OperatorAction `json:"operator_action,omitempty"`
	// ProductDecision is the Lead Product Manager's decision whole, on an
	// AttentionProductDecision entry; the run it is about is the ID and its item
	// is WorkItemID.
	ProductDecision *triage.ProductDecision `json:"product_decision,omitempty"`
	// HumanGate is the step the item reserves for a person, on an
	// AttentionHumanGate entry; the item is WorkItemID.
	HumanGate *HumanGateWait `json:"human_gate,omitempty"`
	// UntracedPass is the pass and the findings it left no trace of, on an
	// AttentionUntracedPass entry; the task is the ID.
	UntracedPass *UntracedPass `json:"untraced_pass,omitempty"`
	// FactoryStall is how long the factory has done nothing, its last success,
	// and what each pass failed on, on an AttentionFactoryStall entry.
	FactoryStall *FactoryStall `json:"factory_stall,omitempty"`
	// TrackerListings is how the tracker's listings stand, on an
	// AttentionTrackerUnanswered entry: since when they have failed, how many,
	// and what the latest said.
	TrackerListings *runstate.TrackerListings `json:"tracker_listings,omitempty"`

	// titles is what the tracker calls each item, set by the reading that
	// assembled the entry, so the line a person reads names every item beside
	// its title. It is not a field of the record: What and Whose are derived
	// from the fields alone, and the titled sentences are carried beside them.
	titles  *WorkItemTitles
	wording *TextTerms
}

// CitedWhat is What with every work item it names shown beside its title.
func (a Attention) CitedWhat() string {
	return a.wording.Render(a.titles.Cite(a.What()))
}

// CitedWhose is Whose with every work item it names shown beside its title,
// read after What, so an item the line has already titled is not titled twice.
func (a Attention) CitedWhose() string {
	return a.wording.Render(a.titles.CiteAfter(a.CitedWhat(), a.Whose()))
}

// Named reports an entry the attention line prints by name wherever it falls
// and never counts into "and N things not named here": a finding only the
// operator can act on, a repeatedly failing product pass, and a hold the brake
// placed. A line that folds those into a remainder has told him nothing, and
// nothing telling him is the month
// one class of finding once waited and the two hours the brake once stood.
func (a Attention) Named() bool {
	switch a.Kind {
	case AttentionOperatorAction:
		return true
	case AttentionFailingTask:
		return a.FailingTask != nil && a.FailingTask.ProductPass
	case AttentionHold:
		return a.IntakeHold != nil && a.IntakeHold.HeldBy == runstate.IntakeHolderBrake
	}
	return false
}

// OwedStep carries the finished run's remaining cleanup or merge settlement,
// including the last recorded check reading. The run and item are on the entry.
type OwedStep struct {
	ReconcileFindings []runstate.ReconcileFinding `json:"reconcile_findings,omitempty"`
	Status            runstate.Status             `json:"status"`
	Phase             runstate.Phase              `json:"phase,omitempty"`
	// EndedAt is when the run ended, which is when the step began to be owed.
	// It is absent on a record that names no ending.
	EndedAt                    time.Time                  `json:"ended_at,omitzero"`
	PullRequest                *runstate.PullRequest      `json:"pull_request,omitempty"`
	MergeDrop                  *runstate.MergeDrop        `json:"merge_drop,omitempty"`
	TargetBranch               string                     `json:"target_branch,omitempty"`
	CleanupFailure             string                     `json:"cleanup_failure,omitempty"`
	LandingChecks              *runstate.LandingChecks    `json:"landing_checks,omitempty"`
	CompletionRecordingFailure string                     `json:"completion_recording_failure,omitempty"`
	ConfigComparison           *runstate.ConfigComparison `json:"config_comparison,omitempty"`
}

// queuedMerge excludes an obsolete publication without discarding the run's
// independent landing or cleanup obligations or the publication's history.
func (s *OwedStep) queuedMerge() *runstate.PullRequest {
	if s == nil || s.PullRequest == nil || !s.PullRequest.MergeQueued || s.PullRequest.Superseded != "" || s.PullRequest.HandedBack != nil {
		return nil
	}
	return s.PullRequest
}

// Publication is a promotion the forge has not published: where it was
// promoted to, the branch that carries it, the pull request the forge holds
// for it where the record holds one, and the drop where the forge dropped its
// merge. Which of the four movers it waits on is read off these.
type Publication struct {
	ReconcileFindings []runstate.ReconcileFinding `json:"reconcile_findings,omitempty"`
	// TargetBranch is empty where the record holds no integration to read it
	// from; the sentence says so rather than the field carrying a placeholder.
	TargetBranch string                `json:"target_branch,omitempty"`
	Branch       string                `json:"branch"`
	PullRequest  *runstate.PullRequest `json:"pull_request,omitempty"`
	MergeDrop    *runstate.MergeDrop   `json:"merge_drop,omitempty"`
	// Unarmed is a request nothing ever asked the forge to merge
	// (runstate.State.PublicationUnasked), which is the development manager's to
	// decide rather than a person's to merge by hand. One the forge has closed
	// is offered only the re-run.
	Unarmed bool `json:"unarmed,omitempty"`
	// EndedAt is when the run that promoted it ended, which is when the
	// publication was left outstanding: the run ends with it unsettled. It is
	// absent on a record that names no ending.
	EndedAt time.Time `json:"ended_at,omitzero"`
}

// HumanGateWait is one step an admitted item reserves for a person: the gate's
// name and what the person has to do, as the item's author declared them — or,
// where nothing could read the declaration, why not. Exactly one of the two
// shapes is set.
type HumanGateWait struct {
	Gate       string `json:"gate,omitempty"`
	Statement  string `json:"statement,omitempty"`
	Unreadable string `json:"unreadable,omitempty"`
}

// HeldWait is which of the two waits held work is in.
type HeldWait string

const (
	// HeldAwaitingDecision is a stoppage the development manager has still to
	// decide about.
	HeldAwaitingDecision HeldWait = "decision"
	// HeldAwaitingCarryOut is a decision she recorded that the harness has
	// still to act on.
	HeldAwaitingCarryOut HeldWait = "carry-out"
)

// HeldWork is how many admitted items are in one of the two waits.
type HeldWork struct {
	Awaiting HeldWait `json:"awaiting"`
	Count    int      `json:"count"`
}

// What is the thing waiting, as the terminal prints it: derived from the
// entry's record, never stored.
func (a Attention) What() string {
	switch a.Kind {
	case AttentionHold:
		switch {
		case a.OperatorHold != nil:
			return fmt.Sprintf("all harness activity is held, since %s",
				a.OperatorHold.HeldAt.UTC().Format(time.RFC3339))
		case a.IntakeHold != nil:
			what := fmt.Sprintf("intake is held, since %s: %s",
				a.IntakeHold.HeldAt.UTC().Format(time.RFC3339), singleLine(intakeClause(*a.IntakeHold), maxRefusalBytes))
			// The brake's hold names the runs that tripped it, each with its item
			// and what stopped it: on 2026-09-19 the line said only that intake
			// was held, and finding which three runs had stopped it was the
			// operator's to do.
			if a.IntakeHold.Brake != nil && len(a.IntakeHold.Brake.Blocked) > 0 {
				what += "; tripped by " + singleLine(strings.Join(a.IntakeHold.Brake.Entries(), "; "), maxBrakeEntriesBytes)
			}
			return what
		case a.CapacityHold != nil:
			what := "every role is held by the provider's usage window, since " + a.CapacityHold.Since.UTC().Format(time.RFC3339)
			if !a.CapacityHold.ResetsAt.IsZero() {
				what += ", until " + a.CapacityHold.ResetsAt.UTC().Format(time.RFC3339)
			}
			return what
		}
	case AttentionDirective:
		if a.Directive != nil {
			return fmt.Sprintf("directive %s is unresolved: %s",
				a.Directive.ID, singleLine(a.Directive.Unresolved, maxRefusalBytes))
		}
	case AttentionAmendment:
		if a.Amendment != nil {
			return fmt.Sprintf("a change to %s is proposed and undecided (%s)", a.Amendment.Artifact, a.Amendment.ID)
		}
	case AttentionOwedStep:
		if step := a.OwedStep; step != nil {
			if len(step.ReconcileFindings) > 0 {
				return reconcileFindingWhat(a.WorkItemID, step.ReconcileFindings)
			}
			if pr := step.queuedMerge(); pr != nil {
				what := fmt.Sprintf("merge of pull request %d for %s is queued", pr.Number, a.WorkItemID)
				if pr.Merged {
					what = fmt.Sprintf("merge of pull request %d for %s needs confirmation", pr.Number, a.WorkItemID)
				}
				if pr.Checks != nil {
					what += "; " + pr.Checks.Describe(step.TargetBranch)
				}
				return what
			}
			// A dropped merge is a separate publication decision; this entry
			// describes only the run's remaining cleanup or completion.
			if step.LandingChecks != nil && !step.LandingChecks.Finished() {
				return fmt.Sprintf("landing checks for %s ended without a recorded result; their checkout needs cleanup", a.WorkItemID)
			}
			if c := step.ConfigComparison; c != nil && c.Pending {
				what := fmt.Sprintf("the configuration comparison for %s has not reached its work item", a.WorkItemID)
				if c.DeliveryFailure != "" {
					what += ": " + c.DeliveryFailure
				}
				return what
			}
			if step.Phase != runstate.PhaseCleaningUp && step.Phase != runstate.PhaseComplete {
				return fmt.Sprintf("completion of %s is not recorded; its work item needs settlement and its branch and worktree need cleanup", a.WorkItemID)
			}
			if step.CompletionRecordingFailure != "" {
				return fmt.Sprintf("completion of %s could not be recorded: %s", a.WorkItemID, step.CompletionRecordingFailure)
			}
			what := fmt.Sprintf("cleanup of the branch and worktree for %s is not finished", a.WorkItemID)
			if step.CleanupFailure != "" {
				what += ": " + step.CleanupFailure
			}
			return what
		}
	case AttentionPublication:
		if a.Publication != nil {
			if len(a.Publication.ReconcileFindings) > 0 {
				return reconcileFindingWhat(a.WorkItemID, a.Publication.ReconcileFindings)
			}
			target := a.Publication.TargetBranch
			if target == "" {
				target = "an unrecorded target"
			}
			if a.Publication.PullRequest == nil {
				return fmt.Sprintf("run %s promoted %s into %s and its record holds no pull request for branch %s, so nothing has asked the forge to merge it",
					a.ID, a.WorkItemID, target, a.Publication.Branch)
			}
			if a.Publication.MergeDrop != nil && !a.Publication.PullRequest.MergeQueued {
				return fmt.Sprintf("merge of pull request %d for %s was dropped by the forge: %s", a.Publication.PullRequest.Number, a.WorkItemID, a.Publication.MergeDrop.Reason)
			}
			what := fmt.Sprintf("run %s promoted %s into %s and the forge has not published it: pull request #%d %s",
				a.ID, a.WorkItemID, target, a.Publication.PullRequest.Number, a.Publication.PullRequest.URL)
			// The checks the last sweep read ride on the line, because a merge the
			// forge is holding says nothing about whether it will land.
			if checks := a.Publication.PullRequest.Checks; checks != nil {
				what += "; " + checks.Describe(a.Publication.TargetBranch)
			}
			if waiting := a.Publication.PullRequest.TargetRed; waiting != nil && !a.Publication.PullRequest.MergeQueued {
				what += "; it " + waiting.Describe()
			}
			return what
		}
	case AttentionOutage:
		if a.Outage != nil {
			return a.Outage.Says()
		}
	case AttentionReports:
		if a.Reports != nil {
			return a.Reports.Describe()
		}
	case AttentionAmendmentQueue:
		if a.AmendmentQueue != nil {
			return a.AmendmentQueue.Describe()
		}
	case AttentionStall:
		if a.Stall != nil {
			what := a.Stall.Says
			// The provider answering nobody, a diverged target, and a session
			// draining past its bound already say since when in their own
			// sentences; the two session states do not, and how
			// long a queue has been unpulled is half of what makes it worth acting
			// on.
			if a.Stall.Reason != ReasonProviderAway && a.Stall.Reason != ReasonDivergedTarget && a.Stall.Reason != ReasonDrainOverrun && !a.Stall.Since.IsZero() {
				what += ", since " + a.Stall.Since.UTC().Format(time.RFC3339)
			}
			return what
		}
	case AttentionDegradedService:
		if a.Service != nil {
			return fmt.Sprintf("the %s service is degraded: %s", a.Service.Service, singleLine(a.Service.Reason, maxRefusalBytes))
		}
	case AttentionConfigMismatch:
		if a.ConfigMismatch != nil {
			return a.ConfigMismatch.Says()
		}
	case AttentionFailingTask:
		if a.FailingTask != nil {
			return a.FailingTask.Says()
		}
	case AttentionHeldWork:
		if a.HeldWork != nil {
			counted := count(a.HeldWork.Count, "admitted item")
			if a.HeldWork.Awaiting == HeldAwaitingCarryOut {
				return fmt.Sprintf("%s %s carry-out of a decision already recorded", counted, awaits(a.HeldWork.Count))
			}
			return fmt.Sprintf("%s %s the development manager's decision", counted, awaits(a.HeldWork.Count))
		}
	case AttentionCarriedItem:
		return fmt.Sprintf("%s is admitted for %q rather than a developer run", a.WorkItemID, a.Executor)
	case AttentionOperatorAction:
		if a.OperatorAction != nil {
			return a.OperatorAction.Says()
		}
	case AttentionHumanGate:
		if a.HumanGate != nil {
			if a.HumanGate.Unreadable != "" {
				return fmt.Sprintf("%s declares a step only a person can take that nothing could read: %s",
					a.WorkItemID, singleLine(a.HumanGate.Unreadable, maxRefusalBytes))
			}
			return fmt.Sprintf("%s waits on the gate %q: %s", a.WorkItemID, a.HumanGate.Gate,
				singleLine(a.HumanGate.Statement, maxRefusalBytes))
		}
	case AttentionProductDecision:
		if a.ProductDecision != nil {
			return fmt.Sprintf("%s while run %s is in flight, decided by %s: %s",
				a.ProductDecision.Says(a.WorkItemID), a.ID, a.ProductDecision.DecidedBy, singleLine(a.ProductDecision.Reason, maxRefusalBytes))
		}
	case AttentionUntracedPass:
		if a.UntracedPass != nil {
			return a.UntracedPass.Says()
		}
	case AttentionFactoryStall:
		if a.FactoryStall != nil {
			return a.FactoryStall.Says()
		}
	case AttentionTrackerUnanswered:
		if a.TrackerListings != nil {
			// What the latest listing said is cut to a line; the record carries it
			// whole.
			said := *a.TrackerListings
			said.Latest = singleLine(said.Latest, maxRefusalBytes)
			return said.Says()
		}
	}
	// An entry whose record is missing is still said rather than printed
	// blank: a blank line on the attention line is the confident emptiness
	// this package refuses everywhere else.
	return strings.TrimSpace(string(a.Kind)+" "+a.ID) + " is waiting and its record was not carried"
}

// Whose is whose move it is and what settles it, as the terminal prints it. It
// opens with the mover's possessive on every kind, so a surface counting the
// line by Mover and a reader of the sentence agree on who has to act; a test
// holds every kind to that.
func (a Attention) Whose() string {
	switch a.Kind {
	case AttentionHold:
		switch {
		case a.OperatorHold != nil:
			return ReasonOperatorHold.Whose()
		case a.IntakeHold != nil:
			// The hold's own record words it, because the same switch is placed
			// by the operator and by the brake, and only the record says which.
			return a.IntakeHold.Whose()
		case a.CapacityHold != nil:
			return a.CapacityHold.Whose()
		}
	case AttentionDirective:
		return a.Mover.Possessive() + " — the work it affects waits until `yoyo directive resolve` settles it"
	case AttentionAmendment:
		return a.Mover.Possessive() + " — nothing reaches the document until they or the operator decide it"
	case AttentionOwedStep:
		if step := a.OwedStep; step != nil {
			if len(step.ReconcileFindings) > 0 {
				return reconcileFindingWhose(a.Mover, step.ReconcileFindings)
			}
			if pr := step.queuedMerge(); pr != nil {
				if pr.Checks != nil && pr.Checks.Red() {
					checks := pr.Checks
					if checks.AwaitingRerun() {
						return a.Mover.Possessive() + " — the forge has not yet started the job rerun the harness requested; `yoyo reconcile` leaves the merge queued and reads the jobs again, without spending another rerun"
					}
					if checks.FailedInTheJob() && checks.Reruns < runstate.MaxCheckReruns {
						return fmt.Sprintf("%s — `yoyo reconcile` asks the forge to run the jobs it cancelled, timed out, or could not start again (%d of %d reruns on this head), leaving the merge queued; if the forge refuses, the harness withdraws the merge to update its head or return it for repair", a.Mover.Possessive(), checks.Reruns+1, runstate.MaxCheckReruns)
					}
					if checks.FailedInTheJob() {
						return a.Mover.Possessive() + " — the job rerun limit on this head is spent; `yoyo reconcile` withdraws the merge to update a head behind its target or return the change to the development manager"
					}
					return a.Mover.Possessive() + " — `yoyo reconcile` reads the failed checks and withdraws the merge to update its head, wait for a target fix, or return the change for repair"
				}
				return a.Mover.Possessive() + " — `yoyo reconcile` confirms the forge's merge and finishes the run's cleanup once it lands"
			}
			if step.LandingChecks != nil && !step.LandingChecks.Finished() {
				return a.Mover.Possessive() + " — `yoyo reconcile` records the interrupted landing as unverified and removes its checkout"
			}
			if step.ConfigComparison != nil && step.ConfigComparison.Pending {
				return a.Mover.Possessive() + " — `yoyo reconcile` delivers the saved configuration comparison to the work item and finishes any remaining cleanup"
			}
			if step.Phase != runstate.PhaseCleaningUp && step.Phase != runstate.PhaseComplete || step.CompletionRecordingFailure != "" {
				return a.Mover.Possessive() + " — `yoyo reconcile` settles the work item, finishes cleanup, and records completion"
			}
			return a.Mover.Possessive() + " — `yoyo reconcile` finishes the run's cleanup and records it"
		}
	case AttentionPublication:
		if a.Publication != nil {
			if len(a.Publication.ReconcileFindings) > 0 {
				return reconcileFindingWhose(a.Mover, a.Publication.ReconcileFindings)
			}
			// Four cases, and all four are settled by the same sweep once the forge
			// records the merge; each says so,
			// because that is what stops a reader going looking for a lever that
			// is not there.
			switch {
			case a.Publication.PullRequest == nil:
				return a.Mover.Possessive() + " — `yoyo reconcile` looks the request up on the forge by that branch, records it, and arms its merge; a forge that holds none is said on every sweep"
			case a.Publication.PullRequest.MergeQueued && a.Publication.PullRequest.Checks != nil && a.Publication.PullRequest.Checks.ReadError != "":
				return a.Mover.Possessive() + " — the next `yoyo reconcile` sweep reads the checks again; the merge stays queued while its check state is unread"
			case a.Publication.PullRequest.MergeQueued:
				if a.Publication.PullRequest.Checks != nil && a.Publication.PullRequest.Checks.Red() {
					return (Attention{Kind: AttentionOwedStep, Mover: a.Mover, OwedStep: &OwedStep{PullRequest: a.Publication.PullRequest}}).Whose()
				}
				return a.Mover.Possessive() + " — it merges once the base branch's requirements are met, and `yoyo reconcile` settles the run when it does"
			case a.Publication.PullRequest.TargetRed != nil:
				return a.Mover.Possessive() + " — the checks fail on the target itself rather than on this change, so the failure is filed as the target's and the merge waits on " + strings.Join(a.Publication.PullRequest.TargetRed.WaitingOn(), ", ") + "; once that closes the watch re-arms it on a level head that passes, or `yoyo reconcile` brings a head the fix left behind up to date, and nothing here needs a person"
			case a.Publication.MergeDrop != nil:
				return a.Mover.Possessive() + " — the forge dropped the merge: " + a.Publication.MergeDrop.Reason + "; she decides a repair, re-run, or re-arm, which the harness carries out; `yoyo reconcile` settles it once the forge records the merge"
			case a.Publication.Unarmed && a.Publication.PullRequest.Closed():
				return a.Mover.Possessive() + " — nothing ever asked the forge to merge the request and the forge has closed it, so there is nothing left to arm: it is on her docket, and a re-run hands the change back for a fresh run"
			case a.Publication.Unarmed:
				return a.Mover.Possessive() + " — nothing ever asked the forge to merge the request, and it is on her docket: a re-arm has the harness arm it under the same landing checks the run's merge makes, a re-run hands the change back for a fresh run, and `yoyo reconcile` settles it once the forge records the merge"
			default:
				return a.Mover.Possessive() + " — the request is on the forge unmerged; merge it, or leave it, and `yoyo reconcile` settles it once the forge records the merge"
			}
		}
	case AttentionOutage:
		return ReasonProviderAway.Whose()
	case AttentionReports:
		return a.Mover.Possessive() + " — reports are decided in conversation, and a pile this old says the schedule that works it is not keeping up"
	case AttentionAmendmentQueue:
		return a.Mover.Possessive() + " — proposals are decided with `yoyo amendment`, and a queue this old says the recurring task that argues them is not keeping up, or none is enabled"
	case AttentionStall:
		if a.Stall != nil {
			return a.Stall.Reason.Whose()
		}
	case AttentionDegradedService:
		return a.Mover.Possessive() + " — the supervisor has stopped restarting it; fix the cause, then `yoyo stop` and `yoyo start` bring it back, or start the part by hand and the supervisor takes it back"
	case AttentionConfigMismatch:
		if a.ConfigMismatch != nil {
			return a.Mover.Possessive() + " — " + runstate.ConfigMismatchRemedy(a.ConfigMismatch.Service) + "; the entry clears once the part runs a build that reads every key"
		}
	case AttentionFailingTask:
		if a.FailingTask != nil && a.FailingTask.Ownership != nil {
			return a.Mover.Possessive() + " — " + runstate.PassFailureOwnersSays(*a.FailingTask.Ownership) + "; the affected pass succeeding clears this finding"
		}
		if a.Mover == MoverOperator {
			return a.Mover.Possessive() + " — nothing is asked of the role until its conversation opens; fix what stops it opening, and the first firing that takes a turn clears this"
		}
		return a.Mover.Possessive() + " — the harness refuses what it composed for the pass, which is a defect in the harness rather than anything waiting it out will end; every firing meets the same refusal until the harness is fixed, and the first firing that takes a turn clears this"
	case AttentionHeldWork:
		if a.HeldWork != nil && a.HeldWork.Awaiting == HeldAwaitingCarryOut {
			return a.Mover.Possessive() + " — the decision is made, and what is outstanding is the harness acting on it"
		}
		return a.Mover.Possessive() + " — nothing pulls a stopped item until she decides what happens to it"
	case AttentionCarriedItem:
		return a.Mover.Possessive() + " — in conversation; no run will ever be started for it"
	case AttentionOperatorAction:
		if a.OperatorAction != nil {
			return a.Mover.Possessive() + " — only a person can act on this; " + a.OperatorAction.Ends
		}
	case AttentionHumanGate:
		if a.HumanGate != nil {
			if a.HumanGate.Unreadable != "" {
				return a.Mover.Possessive() + " — no act records this one; the item's author has to correct the declaration on it before anything pulls it"
			}
			return fmt.Sprintf("%s — nothing machinery does passes it, closing an item included; `yoyo gate record %s --for %s` is the act",
				a.Mover.Possessive(), a.HumanGate.Gate, a.WorkItemID)
		}
	case AttentionProductDecision:
		return a.Mover.Possessive() + " — it is on her docket: \"stop\" stops the run with its change preserved and \"proceed\" lets it finish, and the run goes on until she records one"
	case AttentionUntracedPass:
		return a.Mover.Possessive() + " — its next pass is told which findings they were, and a pass of the task that takes a turn clears this; nothing here needs a person"
	case AttentionFactoryStall:
		return a.Mover.Possessive() + " — every pass it attempts is failing, so no role is looking at anything; the critical report filed when it began names the failures, and the first pull or successful pass clears this and files the recovery"
	case AttentionTrackerUnanswered:
		return a.Mover.Possessive() + " — each listing its thirty-second bound killed is asked again, twice, before it is given up on, a pass carries on with what it could read and names what it could not, and the first listing that answers clears this"
	}
	return a.Mover.Possessive() + " — the entry's record was not carried, so what settles it cannot be said"
}

// attentionFields is the entry's fields without its methods, so the wire shape
// can embed them without inheriting the JSON methods it is implementing.
type attentionFields Attention

// attentionWire is the entry as the JSON carries it: the fields, and the two
// sentences computed from them.
type attentionWire struct {
	attentionFields
	Label string `json:"label"`
	What  string `json:"what"`
	Whose string `json:"whose"`
	// SaidWhat and SaidWhose are the two sentences as a person reads them, with
	// each work item beside its title, and absent where that changes nothing.
	// They are what the dashboard shows; What and Whose stay the derivation, so
	// a document read back is still held to its fields.
	SaidWhat  string `json:"said_what,omitempty"`
	SaidWhose string `json:"said_whose,omitempty"`
}

// MarshalJSON writes the fields and, beside them, the two sentences derived
// from them at this moment. A reader that only wants the line has it; a
// reader that wants the record has that; and neither can be handed one that
// disagrees with the other, because the sentences are never taken from
// anywhere but the fields.
func (a Attention) MarshalJSON() ([]byte, error) {
	wire := attentionWire{attentionFields: attentionFields(a), Label: a.Label(), What: a.What(), Whose: a.Whose()}
	if said := a.CitedWhat(); said != wire.What {
		wire.SaidWhat = said
	}
	if said := a.CitedWhose(); said != wire.Whose {
		wire.SaidWhose = said
	}
	return json.Marshal(wire)
}

// UnmarshalJSON reads the fields back and refuses an entry outside the shape:
// a kind or a mover that is not in its vocabulary, an unknown field, or a
// sentence that disagrees with the fields. The sentences are derived, so a
// document may leave them out; one that carries them is held to the
// derivation, because a hand-written fixture whose line says one thing over
// fields that say another is the disagreement this shape exists to make
// impossible. The vocabularies are held for the same reason: an entry whose
// mover nobody named would print as nobody's move in particular, which is the
// silence the line exists to end. Nothing in the harness stores a standing
// document and reads it back — the only readers are the dashboard's fixtures
// and a script reading `yoyo status --json` — so the refusal costs no stored
// record its readability when a sentence is reworded.
func (a *Attention) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire attentionWire
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	decoded := Attention(wire.attentionFields)
	if wire.Label != "" && wire.Label != decoded.Label() {
		return fmt.Errorf("attention entry %s: label %q disagrees with its record", decoded.ID, wire.Label)
	}
	if !decoded.Kind.Valid() {
		return fmt.Errorf("attention entry %s: its kind %q is not one of %v", decoded.ID, decoded.Kind, AttentionKinds())
	}
	if !decoded.Mover.Valid() {
		return fmt.Errorf("attention entry %s %s: its mover %q is not one of %v", decoded.Kind, decoded.ID, decoded.Mover, Movers())
	}
	if strings.TrimSpace(wire.What) != "" && wire.What != decoded.What() {
		return fmt.Errorf("attention entry %s %s: its what %q disagrees with its record, which says %q", decoded.Kind, decoded.ID, wire.What, decoded.What())
	}
	if strings.TrimSpace(wire.Whose) != "" && wire.Whose != decoded.Whose() {
		return fmt.Errorf("attention entry %s %s: its whose %q disagrees with its record, which says %q", decoded.Kind, decoded.ID, wire.Whose, decoded.Whose())
	}
	*a = decoded
	return nil
}

// operatorHoldAttention is the operator's hold as the attention line carries it.
func operatorHoldAttention(hold runstate.OperatorHold) Attention {
	return Attention{Kind: AttentionHold, ID: HoldOperator, Mover: MoverOperator, OperatorHold: &hold}
}

// intakeHoldAttention is the intake hold as the attention line carries it,
// with whose move it is read off the hold's own record: the operator's for a
// hold they placed, and for one the brake placed the development manager's
// while she decides, the harness's while a probe runs or a decision waits to
// be carried out, and the operator's only once it is escalated — by her, or by
// the harness at the bound on its summons-and-probe loop. The record's own
// Whose words the same cases in the same order, and a test holds the two
// together: her release is read ahead of an escalation, because the two can
// stand together on a hold the harness escalated and her release is what the
// session acts on.
func intakeHoldAttention(hold runstate.IntakeHold) Attention {
	mover := MoverOperator
	if hold.Braked() {
		switch {
		case hold.Brake.Decision == runstate.BrakeDecisionRelease:
			mover = MoverHarness
		case hold.Brake.Escalated():
			mover = MoverOperator
		case hold.Brake.Decision == runstate.BrakeDecisionProbe, hold.Brake.Probing():
			mover = MoverHarness
		default:
			mover = MoverDevelopmentManager
		}
	}
	return Attention{Kind: AttentionHold, ID: HoldIntake, Mover: mover, IntakeHold: &hold}
}

// directiveAttention is an unresolved directive as the attention line carries
// it.
func directiveAttention(paused directive.Directive) Attention {
	return Attention{Kind: AttentionDirective, ID: paused.ID, Mover: MoverOperator, Directive: &paused}
}

// amendmentAttention is an undecided proposal as the attention line carries
// it: whose move it is is the document's owner, and the proposal is carried
// whole so a surface can show what was proposed and why, and act on it by id.
func amendmentAttention(proposal amendment.Proposal) Attention {
	return Attention{
		Kind:       AttentionAmendment,
		ID:         proposal.ID,
		Mover:      MoverOf(proposal.Owner),
		WorkItemID: proposal.WorkItemID,
		Amendment:  &proposal,
	}
}

// owedStepAttention is a run that ended still owing a step, as the attention
// line carries it.
func owedStepAttention(state runstate.State) Attention {
	step := &OwedStep{ReconcileFindings: state.ReconcileFindings, Status: state.Status, Phase: state.Phase, EndedAt: runEnded(state), PullRequest: state.PullRequest, MergeDrop: state.MergeDrop, CleanupFailure: state.CleanupFailure, LandingChecks: state.LandingChecks, CompletionRecordingFailure: state.CompletionRecordingFailure, ConfigComparison: state.ConfigComparison}
	if state.Integration != nil {
		step.TargetBranch = state.Integration.TargetBranch
	}
	return Attention{
		Kind:       AttentionOwedStep,
		ID:         state.RunID,
		Mover:      reconcileFindingMover(state.ReconcileFindings),
		WorkItemID: state.WorkItemID,
		OwedStep:   step,
	}
}

// runEnded is when a finished run ended, and zero on a record that names no
// ending.
func runEnded(state runstate.State) time.Time {
	if state.CompletedAt == nil {
		return time.Time{}
	}
	return *state.CompletedAt
}

// outageAttention is the provider answering nobody, as the attention line
// carries it.
func outageAttention(outage runstate.ProviderOutage) Attention {
	return Attention{Kind: AttentionOutage, ID: string(outage.Cause), Mover: MoverOperator, Outage: &outage}
}

// reportsAttention is a pile whose oldest undecided report has waited too
// long, as the attention line carries it.
func reportsAttention(pile report.Pile) Attention {
	return Attention{Kind: AttentionReports, Mover: MoverProductManager, Reports: &pile}
}

// amendmentQueueAttention is a queue of proposed changes whose oldest
// undecided one has waited too long, as the attention line carries it. It is
// the operator's move whoever owns the documents: only `yoyo amendment` decides
// a proposal, and only a person enables the task that argues them.
func amendmentQueueAttention(queue amendment.Queue) Attention {
	return Attention{Kind: AttentionAmendmentQueue, Mover: MoverOperator, AmendmentQueue: &queue}
}

// degradedServiceAttention is a part the supervisor has left down, as the
// attention line carries it.
func degradedServiceAttention(child runstate.SupervisedChild) Attention {
	return Attention{Kind: AttentionDegradedService, ID: string(child.Service), Mover: MoverOperator, Service: &child}
}

// configMismatchAttention is a running part whose build cannot read the
// configuration, as the attention line carries it. It is the harness's move
// whichever part it is: a part running a stale build is the harness's own
// state, and under the operator's rule of 2026-09-26 only a change to the
// fundamental goals is his. The whose sentence says what brings each part
// onto a build that reads the file, the restart included where nothing does
// it yet.
func configMismatchAttention(mismatch runstate.ConfigMismatch) Attention {
	return Attention{Kind: AttentionConfigMismatch, ID: mismatch.InstanceID(), Mover: MoverHarness, ConfigMismatch: &mismatch}
}

// heldWorkAttention is one of the two waits held work is in, with how many
// items are in it: the development manager's where a decision is owed, and the
// harness's where one is recorded and not yet carried out.
func heldWorkAttention(awaiting HeldWait, items int) Attention {
	mover := MoverDevelopmentManager
	if awaiting == HeldAwaitingCarryOut {
		mover = MoverHarness
	}
	return Attention{Kind: AttentionHeldWork, Mover: mover, HeldWork: &HeldWork{Awaiting: awaiting, Count: items}}
}

// productDecisionAttention is a product decision about a run in flight nobody
// has answered, as the attention line carries it.
func productDecisionAttention(entry triage.Entry) Attention {
	decided := *entry.ProductDecision
	return Attention{
		Kind:            AttentionProductDecision,
		ID:              entry.RunID,
		Mover:           MoverDevelopmentManager,
		WorkItemID:      entry.WorkItemID,
		ProductDecision: &decided,
	}
}

// carriedItemAttention is an admitted item marked for a conversation, as the
// attention line carries it: whose move it is is the role the marker names,
// and the unnamed mover where it names none the harness recognizes.
func carriedItemAttention(workItemID string, executor domain.WorkItemExecutor) Attention {
	return Attention{
		Kind:       AttentionCarriedItem,
		ID:         workItemID,
		Mover:      MoverOf(executor.Role()),
		WorkItemID: workItemID,
		Executor:   executor,
	}
}

// operatorActionAttention is a finding only the operator can act on, as the
// attention line carries it.
func operatorActionAttention(action OperatorAction) Attention {
	return Attention{
		Kind:           AttentionOperatorAction,
		ID:             action.Key,
		Mover:          MoverOperator,
		WorkItemID:     action.WorkItemID,
		OperatorAction: &action,
	}
}

// maxBrakeEntriesBytes bounds the runs a brake's hold names on the attention
// line. Three stops with a line each fit; a storm longer than that is cut, and
// the hold's own record carries the whole.
const maxBrakeEntriesBytes = 1 << 10

// ReconcileFindingAttention uses the ordinary settlement attention surface for
// a refusal, including one found before the run itself could be settled.
func ReconcileFindingAttention(state runstate.State) Attention {
	attention := owedStepAttention(state)
	attention.titles = NewWorkItemTitles([]beads.WorkItem{{ID: state.WorkItemID, Title: state.WorkItemTitle}})
	return attention
}

func reconcileFindingWhat(item string, findings []runstate.ReconcileFinding) string {
	problems := make([]string, 0, len(findings))
	for _, finding := range findings {
		if !finding.Resolved {
			problems = append(problems, finding.Problem)
		}
	}
	if len(problems) == 0 {
		return fmt.Sprintf("settlement of %s finished; its finding still needs delivery or clearing", item)
	}
	what := fmt.Sprintf("reconcile could not settle %s: %s", item, strings.Join(problems, "; "))
	if len(problems) < len(findings) {
		what += "; findings from completed settlements still need delivery or clearing"
	}
	return what
}

func reconcileFindingWhose(mover Mover, findings []runstate.ReconcileFinding) string {
	if !slices.ContainsFunc(findings, func(f runstate.ReconcileFinding) bool { return !f.Resolved }) {
		return mover.Possessive() + " — the next `yoyo reconcile` delivers any pending finding note and clears the resolved finding"
	}
	problem := reconcileFindingWhat("", findings)
	remedy := "the next `yoyo reconcile` retries this item's settlement once the named refusal is resolved"
	if mover == MoverDevelopmentManager {
		if strings.Contains(problem, "closed but has no later confirmed merged publication") {
			remedy = "decide what becomes of the closed item's preserved run; closed status alone does not authorize retirement or reopening"
		} else {
			remedy = "decide how to preserve any branch work outside the target before removing the leftover remote branch; the next `yoyo reconcile` retries the deletion and clears the outstanding publication"
		}
	} else if strings.Contains(problem, "delete the merged remote branch") {
		remedy += "; restore access to the remote so the consumed branch can be removed"
	} else if strings.Contains(problem, "forge") {
		remedy += "; restore forge access so its answer can be read"
	}
	return mover.Possessive() + " — " + remedy
}

func reconcileFindingMover(findings []runstate.ReconcileFinding) Mover {
	for _, finding := range findings {
		if !finding.Resolved && strings.Contains(finding.Problem, "closed but has no later confirmed merged publication") {
			return MoverDevelopmentManager
		}
		if !finding.Resolved && strings.Contains(finding.Problem, "delete the merged remote branch") && strings.Contains(finding.Problem, "want the published commit") {
			return MoverDevelopmentManager
		}
	}
	return MoverHarness
}
