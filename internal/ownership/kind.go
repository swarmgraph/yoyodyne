package ownership

// Kind is what sort of record one waiting entry is about: the key the
// registry's table is kept by (registry.go). The set is closed, for the reason
// the stall's reasons are: a kind nobody named is a kind no surface can show or
// act on, and no rule can own. The read model's attention line names its
// entries by this vocabulary (readmodel.AttentionKind is an alias of it), and
// the registry adds the kinds the line does not carry as entries of their own
// but whose owner is still decided here: a stopped run.
type Kind string

const (
	// KindAmendment is a change proposed to a document its proposer does
	// not own, which nobody has decided.
	KindAmendment Kind = "amendment"
	// KindCarriedItem is an admitted work item marked for a conversation
	// rather than a developer run.
	KindCarriedItem Kind = "conversation-carried-item"
	// KindReports is the collected report pile, once its oldest undecided
	// report has waited longer than any working cadence would leave it.
	KindReports Kind = "report"
	// KindAmendmentQueue is the queue of proposed changes, once its oldest
	// undecided proposal has waited longer than any working cadence would leave
	// it: the report pile's sibling.
	KindAmendmentQueue Kind = "amendment-queue"
	// KindOwedStep is a run that ended still owing a step.
	KindOwedStep Kind = "owed-step"
	// KindPublication is a promotion the forge has not published.
	KindPublication Kind = "publication"
	// KindDegradedService is a part of the product its supervisor has
	// stopped restarting.
	KindDegradedService Kind = "degraded-service"
	// KindFailingTask is a product pass whose executions keep failing, or
	// the earlier signal from a recurring task repeatedly refused before a turn.
	KindFailingTask Kind = "failing-task"
	// KindConfigMismatch is a running part of the product whose build
	// cannot read keys the configuration it reads now carries, so every read
	// it makes of the file fails. The part is the ID.
	KindConfigMismatch Kind = "config-mismatch"
	// KindServiceCopies is more than one copy of a part the supervisor
	// hosts running at once. The part is the ID.
	KindServiceCopies Kind = "service-copies"
	// KindHold is one of the switches over what the harness does: the
	// operator's hold over everything, the intake hold over what it chooses for
	// itself, and the provider holding every role at once. The entry's ID says
	// which of the three.
	KindHold Kind = "hold"
	// KindDirective is a directive that pauses work and nobody has
	// resolved.
	KindDirective Kind = "directive"
	// KindOutage is the provider answering nobody.
	KindOutage Kind = "outage"
	// KindStall is a queue nothing is pulling from while admitted work
	// waits behind that: a session sitting idle over it, or no session at all.
	KindStall Kind = "stall"
	// KindHeldWork is the admitted work somebody has to release, counted
	// by whose move it is rather than named item by item.
	KindHeldWork Kind = "held-work"
	// KindOperatorAction is a finding only the operator can act on: a
	// report handled as needing his hand, or a stopped run the development
	// manager escalated to him. It is always named and never counted into the
	// remainder; see Named.
	KindOperatorAction Kind = "operator-action"
	// KindAddressedReport is one report nobody has handled that waits on a
	// role: one addressed to a role, whatever its severity, or a critical one
	// addressed to none, which is the Lead Product Manager's. The report's
	// identifier is the ID and the role it waits on the mover, never the
	// operator; see addressedreports.go.
	KindAddressedReport Kind = "addressed-report"
	// KindProductDecision is the Lead Product Manager's decision that an
	// item whose run is in flight is superseded, narrowed, or to be retired,
	// which the development manager has not yet answered by stopping the run or
	// letting it finish.
	KindProductDecision Kind = "product-decision"
	// KindHumanGate is an admitted work item declaring a step only a
	// person can take, which nobody has recorded taking — or a declaration of
	// one that nothing could read. The item is the WorkItemID and the gate's
	// name is the ID.
	KindHumanGate Kind = "human-gate"
	// KindUntracedPass is a role's last pass that reported findings and
	// left no trace of them outside its account. The task is the ID, and the
	// role is the mover.
	KindUntracedPass Kind = "untraced-pass"
	// KindFactoryStall is the factory having pulled no work and completed
	// no recurring pass for longer than its configured limit. The moment it last
	// did either is the ID.
	KindFactoryStall Kind = "factory-stall"
	// KindTrackerUnanswered is the tracker failing listings after their
	// retries, since the moment the first of them failed. That moment is the ID.
	KindTrackerUnanswered Kind = "tracker-unanswered"
	// KindUnrunCheck is a configured check that said it could not run on
	// several changes in a row. The check's command is the ID.
	KindUnrunCheck Kind = "check-could-not-run"
	// KindCapacityDegraded is one of an account's usage windows past the
	// configured share used, as the provider last reported it. The account and
	// window are the ID, and the wait is nobody's: the window lifts on the
	// provider's clock.
	KindCapacityDegraded Kind = "capacity-degraded"
	// KindKeptHeavyCheck is a heavy check's place among the machine's kept
	// for processes nobody can confirm have gone, which every other run's check
	// of the same name waits behind. The permit is the ID and its run's item is
	// the WorkItemID.
	KindKeptHeavyCheck Kind = "heavy-check-kept"
	// KindLaneHumanBlock is one entry a program manager's lane report lists
	// under Blocked on a human, once per item however many passes or instances
	// name it. The item is the WorkItemID and the ID, or the record it cites
	// where it names none; the ownership registry names the mover from the
	// entry's reason.
	KindLaneHumanBlock Kind = "blocked-on-a-human"
	// KindDocumentApproval is a document an owning role wrote in a
	// conversation that waits for the operator to approve or decline it. The
	// conversation and the document's identifier there are the ID.
	KindDocumentApproval Kind = "document-approval"
)

// KindStoppage is a run that stopped and left its item for somebody: the
// docket entry the development manager decides. It is not an entry on the
// attention line, which counts stoppages as held work, but the channel and the
// held-work reading ask who moves one, and the answer is the registry's.
const KindStoppage Kind = "stoppage"

// AttentionKinds is every kind the read model's attention line carries, in the
// order it lists them.
func AttentionKinds() []Kind {
	return []Kind{
		KindAmendment,
		KindCarriedItem,
		KindReports,
		KindAmendmentQueue,
		KindOwedStep,
		KindPublication,
		KindDegradedService,
		KindFailingTask,
		KindConfigMismatch,
		KindServiceCopies,
		KindHold,
		KindDirective,
		KindOutage,
		KindStall,
		KindHeldWork,
		KindOperatorAction,
		KindAddressedReport,
		KindProductDecision,
		KindHumanGate,
		KindUntracedPass,
		KindFactoryStall,
		KindTrackerUnanswered,
		KindUnrunCheck,
		KindCapacityDegraded,
		KindKeptHeavyCheck,
		KindLaneHumanBlock,
		KindDocumentApproval,
	}
}

// Kinds is every kind the registry keeps a rule for: the attention line's, and
// the stoppage.
func Kinds() []Kind {
	return append(AttentionKinds(), KindStoppage)
}

// Valid reports whether a token is one of the attention line's kinds.
func (k Kind) Valid() bool {
	for _, known := range AttentionKinds() {
		if k == known {
			return true
		}
	}
	return false
}
