package ownership

// The one place that decides who owns a waiting entry.
//
// Every surface used to work out on its own who moves an entry, and a case its
// rules did not cover fell to the operator. This is a table keyed by entry
// kind, one rule per kind, following the table in the ownership design
// (`docs/designs/ownership-and-the-operator.md`, "The rule for each kind"). The
// read model resolves each entry here once, as it builds the standing, and
// carries the answer on the entry; `yoyo status`, the dashboard, and the Slack
// sink print the answer it carries and derive no owner of their own.
//
// The operator is an answer only a rule here gives, and only with a reason on
// the closed list (OperatorReasons). Resolve refuses a rule's answer that names
// him without one, and an entry no rule can classify is the Lead Product
// Manager's to classify, never his.

import (
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Answer is the registry's answer for one waiting entry.
type Answer struct {
	// Owner is who moves the entry next.
	Owner Mover `json:"owner"`
	// Reason is why the owner is the operator, from the closed list, and empty
	// whenever the owner is anybody else.
	Reason OperatorReason `json:"reason,omitempty"`
	// Remedy is the one act that ends the entry, in the words a surface prints.
	Remedy string `json:"remedy"`
	// Capability is the command, registered action, or tool the remedy needs,
	// and empty where the entry ends by itself.
	Capability string `json:"capability,omitempty"`
}

// ReasonSays is why the entry is the operator's, in words, and empty where it
// is not his.
func (a Answer) ReasonSays() string {
	if !a.Owner.IsOperator() {
		return ""
	}
	return a.Reason.Says()
}

// RemedyUnclassified is the remedy of an entry no rule could classify.
const RemedyUnclassified = "classify this entry"

// Record is what a rule reads of one entry's record: the facts its kind is
// classified by. A caller fills the facts its kind's rule reads and leaves the
// rest zero. Every field is a fact read off the entry's own record, never an
// owner: the owner is the rule's to give.
type Record struct {
	// Role is the role the record names: the owner of the document an amendment
	// or the oldest queued proposal is against, the role a carried item's marker
	// names, the role a report is addressed to, the role whose pass left no
	// trace, or the owner of a document a stopped run was writing.
	Role domain.AgentRole
	// FundamentalIntent is an amendment, or the oldest of the queue, recorded as
	// a change of fundamental intent or as one that does not say which kind it
	// is (amendment.Proposal.ReservedForOperator).
	FundamentalIntent bool
	// Reason is the closed-list reason the record itself carries: a report
	// handling's, a development manager's escalation's, or a lane report entry's.
	Reason OperatorReason

	// Hold says which switch a hold is: OperatorHold, IntakeHold, or
	// CapacityHold. The intake hold's facts follow: placed by the operator, or by
	// the brake, and where the brake has it, what was decided about it.
	Hold           HoldSwitch
	HeldByOperator bool
	HeldByBrake    bool
	Braked         bool
	BrakeReleased  bool
	BrakeProbing   bool
	BrakeEscalated bool

	// Stall is the stall's reason, on a stall or a held group that is one.
	Stall StallReason
	// OutageCause is why the provider answers nobody, on an outage or a stall
	// that is one.
	OutageCause domain.ProviderOutageCause

	// UnresolvedFindings is what reconcile could not settle, in its own words, on
	// an owed step or a publication.
	UnresolvedFindings []string
	// The publication's facts: whether its record holds a pull request, whether
	// the forge has the merge queued, whether the checks are unread or red, and
	// whether it waits on a red target, was dropped by the forge, or was never
	// asked to merge.
	PullRequest        bool
	MergeQueued        bool
	ChecksUnread       bool
	ChecksRed          bool
	WaitingOnRedTarget bool
	MergeDropped       bool
	Unarmed            bool

	// PassFailure is the factory's ownership of a repeatedly failing product
	// pass, where one is recorded (ResolvePassFailureForRole), and
	// ConversationUnopened a recurring task whose conversation will not open.
	PassFailure          *PassFailure
	ConversationUnopened bool

	// Held work: awaiting the harness carrying out a recorded decision, or
	// admitted work the development manager decided to wait on, and otherwise
	// her decision. The groups of not-startable work add what else an item can be
	// held behind: a parking, other work landing, or a reason nothing could read.
	AwaitingCarryOut bool
	AwaitingWork     bool
	Parked           bool
	WaitsOnWork      bool
	Unread           bool

	// GateUnreadable is a human gate whose declaration nothing could read.
	GateUnreadable bool
	// FromReport is a finding raised by a report's handling, and FromEscalation
	// one raised by the development manager escalating a stopped run.
	FromReport     bool
	FromEscalation bool

	// A stopped run's facts: an approved change the environment stopped that the
	// harness can resume, and a check stage or a first silent-stream stall the
	// harness continues by itself.
	IntegrationResumable bool
	HarnessContinues     bool
	// DocumentRun is a stopped run that was writing a document; Role is the
	// document's owner.
	DocumentRun bool
}

// HoldSwitch is which of the three switches a hold entry is about.
type HoldSwitch string

const (
	OperatorHold HoldSwitch = "operator"
	IntakeHold   HoldSwitch = "intake"
	CapacityHold HoldSwitch = "capacity"
)

// Rule is the registry's rule for one kind: it reads the entry's record and
// answers, or reports that it cannot classify it.
type Rule func(Record) (Answer, bool)

// registry is the table: one rule per kind. A test fails on a kind in Kinds
// with no rule here.
var registry = map[Kind]Rule{
	KindAmendment:         amendmentRule,
	KindCarriedItem:       carriedItemRule,
	KindReports:           reportsRule,
	KindAmendmentQueue:    amendmentQueueRule,
	KindOwedStep:          owedStepRule,
	KindPublication:       publicationRule,
	KindDegradedService:   degradedServiceRule,
	KindFailingTask:       failingTaskRule,
	KindConfigMismatch:    configMismatchRule,
	KindServiceCopies:     serviceCopiesRule,
	KindHold:              holdRule,
	KindDirective:         directiveRule,
	KindOutage:            outageRule,
	KindStall:             stallRule,
	KindHeldWork:          heldWorkRule,
	KindOperatorAction:    operatorActionRule,
	KindAddressedReport:   addressedReportRule,
	KindProductDecision:   productDecisionRule,
	KindHumanGate:         humanGateRule,
	KindUntracedPass:      untracedPassRule,
	KindFactoryStall:      factoryStallRule,
	KindTrackerUnanswered: trackerUnansweredRule,
	KindUnrunCheck:        unrunCheckRule,
	KindCapacityDegraded:  capacityDegradedRule,
	KindKeptHeavyCheck:    keptHeavyCheckRule,
	KindLaneHumanBlock:    laneHumanBlockRule,
	KindDocumentApproval:  documentApprovalRule,
	KindStoppage:          stoppageRule,
}

// HasRule reports whether the registry keeps a rule for a kind.
func HasRule(kind Kind) bool {
	_, ok := registry[kind]
	return ok
}

// Resolve is the owner, reason, remedy, and capability of one entry. An entry
// whose kind has no rule, or whose rule cannot classify its record, is the Lead
// Product Manager's with the remedy "classify this entry": an unowned entry is
// a defect in the registry, and she files it. A rule's answer naming the
// operator without a reason from the closed list is treated the same way,
// because naming him for any other reason is the defect this registry exists
// to end; a reason on an answer naming anybody else is dropped.
func Resolve(kind Kind, record Record) Answer {
	rule, ok := registry[kind]
	if !ok {
		return unclassified()
	}
	answer, ok := rule(record)
	if !ok || !answer.Owner.Valid() {
		return unclassified()
	}
	if answer.Owner.IsOperator() {
		if !answer.Reason.Valid() {
			return unclassified()
		}
		return answer
	}
	answer.Reason = ""
	return answer
}

func unclassified() Answer {
	return Answer{Owner: MoverProductManager, Remedy: RemedyUnclassified, Capability: "report handling"}
}

// operator is the one constructor of an answer naming the operator, so every
// rule that names him gives a reason.
func operator(reason OperatorReason, remedy, capability string) (Answer, bool) {
	return Answer{Owner: MoverOperator, Reason: reason, Remedy: remedy, Capability: capability}, true
}

func owned(owner Mover, remedy, capability string) (Answer, bool) {
	return Answer{Owner: owner, Remedy: remedy, Capability: capability}, true
}

// roleOwned is a role named on the record, and no answer where the record names
// none the harness recognizes.
func roleOwned(role domain.AgentRole, remedy, capability string) (Answer, bool) {
	if !role.Valid() {
		return Answer{}, false
	}
	return owned(MoverOf(role), remedy, capability)
}

func amendmentRule(r Record) (Answer, bool) {
	if r.FundamentalIntent {
		return operator(OperatorFundamentalIntent, "approve or decline the change", "yoyo amendment approve|decline")
	}
	return roleOwned(r.Role, "decide the proposed change from its conversation", "amendment action")
}

func amendmentQueueRule(r Record) (Answer, bool) {
	if r.FundamentalIntent {
		return operator(OperatorFundamentalIntent, "decide the oldest proposed change", "yoyo amendment approve|decline")
	}
	return roleOwned(r.Role, "work the queue of proposed changes from its conversation", "amendment action")
}

func carriedItemRule(r Record) (Answer, bool) {
	if !r.Role.Valid() {
		// A marker naming no role the harness recognizes is still that marker's;
		// the unnamed mover is what a surface counts it under.
		return owned(MoverUnnamed, "do the work in the conversation the marker names", "conversation")
	}
	return roleOwned(r.Role, "do the work in its conversation", "conversation")
}

func reportsRule(Record) (Answer, bool) {
	return owned(MoverProductManager, "handle the oldest undecided reports", "report handling")
}

func addressedReportRule(r Record) (Answer, bool) {
	return roleOwned(r.Role, "handle the report", "report handling")
}

// reconcileNeedsDevelopmentManager is a finding reconcile cannot settle without
// the development manager deciding what becomes of a run's work: a closed item
// with no confirmed merged publication, or a merged branch whose deletion would
// lose commits somebody may want.
func reconcileNeedsDevelopmentManager(findings []string) bool {
	for _, problem := range findings {
		if strings.Contains(problem, "closed but has no later confirmed merged publication") {
			return true
		}
		if strings.Contains(problem, "delete the merged remote branch") && strings.Contains(problem, "want the published commit") {
			return true
		}
	}
	return false
}

func owedStepRule(r Record) (Answer, bool) {
	if reconcileNeedsDevelopmentManager(r.UnresolvedFindings) {
		return owned(MoverDevelopmentManager, "decide what becomes of the run's preserved work", "triage decision")
	}
	return owned(MoverHarness, "settle the run", "yoyo reconcile")
}

func publicationRule(r Record) (Answer, bool) {
	if len(r.UnresolvedFindings) > 0 {
		return owedStepRule(r)
	}
	switch {
	case !r.PullRequest:
		return owned(MoverHarness, "look the request up on the forge and arm its merge", "yoyo reconcile")
	case r.MergeQueued && r.ChecksUnread:
		return owned(MoverHarness, "read the checks again", "yoyo reconcile")
	case r.MergeQueued && r.ChecksRed:
		return owned(MoverHarness, "rerun or withdraw the failing merge", "yoyo reconcile")
	case r.MergeQueued:
		return owned(MoverForge, "merge once the base branch's requirements are met", "")
	case r.WaitingOnRedTarget:
		return owned(MoverHarness, "re-arm the merge once the target's fix lands", "yoyo reconcile")
	case r.MergeDropped, r.Unarmed:
		return owned(MoverDevelopmentManager, "decide a repair, re-run, or re-arm", "triage decision")
	}
	// A request on the forge that nothing is merging and nothing explains is
	// hers to decide what happens to, as a dropped one is; only a forge refusing
	// the harness's token is a person's, and the record does not say that.
	return owned(MoverDevelopmentManager, "decide a re-arm or a re-run", "triage decision")
}

func degradedServiceRule(Record) (Answer, bool) {
	return owned(MoverHarness, "bring the part back once its cause is fixed", "yoyo stop and yoyo start")
}

func failingTaskRule(r Record) (Answer, bool) {
	if pf := r.PassFailure; pf != nil {
		if reason := OperatorReason(pf.PersonReason); pf.Mover.IsOperator() && reason.Valid() {
			return operator(reason, pf.PersonStep, "the recorded person-only step")
		}
		// A stored ownership naming the operator for a reason off the list, or
		// naming nobody the registry knows, is the role that resolves the pass's
		// failures, as ResolvePassFailureForRole names it.
		if pf.Mover.Valid() && !pf.Mover.IsOperator() {
			return owned(pf.Mover, "find and fix why the pass keeps failing", "triage decision")
		}
		if pf.Resolver.Valid() {
			return roleOwned(pf.Resolver, "find and fix why the pass keeps failing", "triage decision")
		}
		return owned(MoverDevelopmentManager, "find and fix why the pass keeps failing", "triage decision")
	}
	if r.ConversationUnopened {
		return owned(MoverHarness, "fix what stops the conversation opening", "harness fix")
	}
	return owned(MoverHarness, "fix the harness's refusal of what it composed", "harness fix")
}

func configMismatchRule(Record) (Answer, bool) {
	return owned(MoverHarness, "restart the part on a build that reads the configuration", "yoyo restart")
}

func serviceCopiesRule(Record) (Answer, bool) {
	return owned(MoverDevelopmentManager, "find and stop what started the extra copy", "")
}

func holdRule(r Record) (Answer, bool) {
	switch r.Hold {
	case OperatorHold:
		return operator(OperatorOwnHold, "lift the hold", "yoyo resume")
	case CapacityHold:
		return owned(MoverHarness, "resume the roles when the provider's usage limit resets", "")
	case IntakeHold:
		if !r.Braked {
			if r.HeldByOperator {
				return operator(OperatorOwnHold, "release intake", "yoyo release")
			}
			if r.HeldByBrake {
				// A brake hold from before the brake kept a record of its own.
				return owned(MoverDevelopmentManager, "decide whether intake reopens", "brake decision")
			}
			// A hold whose record says nobody placed it is nobody's to lift
			// by right; it is an entry for her to classify.
			return Answer{}, false
		}
		// Her release is read ahead of an escalation, because the two can stand
		// together on a hold the harness escalated and her release is what the
		// session acts on.
		switch {
		case r.BrakeReleased:
			return owned(MoverHarness, "lift the hold the development manager released", "watch session")
		case r.BrakeEscalated:
			// The architect's ruling of 2026-10-17 and the operating rule that the
			// brake never summons the operator: an escalated brake is the Lead
			// Product Manager's, and the operator is told and may release it.
			return owned(MoverProductManager, "decide whether intake reopens; the operator is told and may release it", "yoyo release")
		case r.BrakeProbing:
			return owned(MoverHarness, "run the probe the development manager decided on", "watch session")
		}
		return owned(MoverDevelopmentManager, "decide what happens to the brake's hold", "brake decision")
	}
	return Answer{}, false
}

func directiveRule(Record) (Answer, bool) {
	// She ends it, or carries it into a document or an item; where settling it
	// would change what the goals admit, what reaches the operator is her drafted
	// change to the goals as an amendment, which that rule names him for.
	return owned(MoverProductManager, "end the directive, or carry it into a document or a work item", "yoyo directive resolve")
}

func outageRule(r Record) (Answer, bool) {
	// A record that names no cause is read as the login, as the outage's own
	// sentence reads it (runstate.ProviderOutage.Says).
	if r.OutageCause == domain.ProviderUnreachable {
		return owned(MoverNobody, "nothing: the harness resumes once the provider answers", "")
	}
	return operator(OperatorCredential, "log in to the provider again", "the provider's login")
}

func stallRule(r Record) (Answer, bool) {
	switch r.Stall {
	case StallOperatorHold:
		return holdRule(Record{Hold: OperatorHold})
	case StallIntakeHold:
		held := r
		held.Hold = IntakeHold
		return holdRule(held)
	case StallProviderAway:
		return outageRule(r)
	case StallDivergedTarget:
		return operator(OperatorDivergedHistory, "say which history is right, following \"Unwedging a target branch that diverged from the forge\" in docs/operations.md", "rights on the protected branch")
	case StallNoCapacity, StallProviderWindow, StallTrackerWait, StallRedeploying, StallUnwatched:
		return owned(MoverNobody, "nothing: it clears by itself", "")
	case StallStoreUnreadable:
		return owned(MoverHarness, "read the queue again until it answers", "watch session")
	case StallSessionBlocked:
		return owned(MoverHarness, "retry the runs once the refusing condition clears", "watch session")
	case StallDrainOverrun:
		return owned(MoverHarness, "restart the watch session into the deployed build", "watch session")
	case StallSessionIdle, StallNoWatchSession:
		// The parts of the product are started and restarted by the product, not
		// by a person (operating rule 6de828a8).
		return owned(MoverHarness, "start a watch session to pull the queue", "yoyo start")
	}
	return Answer{}, false
}

func heldWorkRule(r Record) (Answer, bool) {
	switch {
	case r.AwaitingCarryOut:
		return owned(MoverHarness, "carry out the decision already recorded", "watch session")
	case r.AwaitingWork:
		return owned(MoverHarness, "put the stoppage back on the development manager's docket once the work it waits on closes", "watch session")
	case r.Parked:
		return owned(MoverProductManager, "release each item once what it was parked for is settled", "tracker")
	case r.WaitsOnWork:
		return owned(MoverHarness, "pull each item once the work it waits on lands", "watch session")
	case r.Unread:
		return owned(MoverNobody, "nothing here can say; the refusal beside each item says what could not be read", "")
	}
	return owned(MoverDevelopmentManager, "decide what becomes of each stopped run", "triage decision")
}

func stoppageRule(r Record) (Answer, bool) {
	switch {
	case r.DocumentRun:
		return roleOwned(r.Role, "decide what becomes of the document run", "conversation")
	case r.IntegrationResumable, r.AwaitingCarryOut, r.HarnessContinues:
		return owned(MoverHarness, "continue the run", "watch session")
	}
	return owned(MoverDevelopmentManager, "decide what becomes of the stopped run", "triage decision")
}

func operatorActionRule(r Record) (Answer, bool) {
	if r.Reason.Valid() {
		return operator(r.Reason, "do what the finding asks", "the step the finding names")
	}
	switch {
	case r.FromEscalation:
		// An escalation naming the operator for no reason on the list goes back
		// to the development manager who decided it.
		return owned(MoverDevelopmentManager, "decide the stopped run again", "triage decision")
	case r.FromReport:
		return owned(MoverProductManager, "handle the report again", "report handling")
	}
	return Answer{}, false
}

func productDecisionRule(Record) (Answer, bool) {
	return owned(MoverDevelopmentManager, "stop the run or let it finish", "triage decision")
}

func humanGateRule(r Record) (Answer, bool) {
	if r.GateUnreadable {
		// No act records a declaration nothing could read; the item's text is the
		// Lead Product Manager's to correct.
		return owned(MoverProductManager, "correct the item's declaration of the gate", "tracker")
	}
	return operator(OperatorHumanGate, "take the step and record it", "yoyo gate record")
}

func untracedPassRule(r Record) (Answer, bool) {
	return roleOwned(r.Role, "record the pass's findings at its next pass", "")
}

func factoryStallRule(Record) (Answer, bool) {
	return owned(MoverHarness, "get a pass or a pull to succeed", "")
}

func trackerUnansweredRule(Record) (Answer, bool) {
	return owned(MoverHarness, "ask the tracker again until it answers", "")
}

func unrunCheckRule(Record) (Answer, bool) {
	return owned(MoverDevelopmentManager, "give the check what it lacks, or change the check", "")
}

func capacityDegradedRule(Record) (Answer, bool) {
	return owned(MoverNobody, "nothing: the limit lifts on the provider's clock", "")
}

func keptHeavyCheckRule(Record) (Answer, bool) {
	return owned(MoverDevelopmentManager, "give the place back if it is a hang", "yoyo heavy-checks release")
}

func laneHumanBlockRule(r Record) (Answer, bool) {
	if r.Reason.Valid() {
		return operator(r.Reason, "take the step the program manager's report names", "the step the entry cites")
	}
	return owned(MoverProductManager, "correct the program manager's report, which names the operator for no reason on the list", "report handling")
}

// documentApprovalRule is a document waiting for the operator's approval. One
// stating the product's intent is his for a change of fundamental intent: the
// Lead Product Manager approves a consistent rewording of her own, so one
// still waiting is a change she could not approve. Any other is waiting on him
// because the project's approval setting for its kind asks for a person, which
// is no reason on his closed list, so it stays its owning role's, as the
// ownership design's "A role's decision that needs the operator's hand" says,
// and the remedy names his act.
func documentApprovalRule(r Record) (Answer, bool) {
	if r.FundamentalIntent {
		return operator(OperatorFundamentalIntent, "approve or decline the document", "yoyo agent chat approve|decline")
	}
	return roleOwned(r.Role, "have the operator approve or decline the document, as the project's approval setting for its kind asks", "yoyo agent chat approve|decline")
}
