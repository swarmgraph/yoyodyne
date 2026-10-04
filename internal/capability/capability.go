// Package capability is the vocabulary an action declares its authority in.
//
// The point of the vocabulary is that it is closed. A workflow definition
// selects which registered actions run and in what order; it never mints one,
// and it never widens what one is permitted to do. That only holds if the set
// of things an action can require is a list in Go rather than a string some
// definition supplies, so a capability nothing here declares is refused where
// the registry is built — before any definition has been read and long before
// anything could be dispatched.
//
// What is written down is a primitive rather than a role. Two steps that need
// the same authority say so identically, which is what will let a later
// authority model answer "what may this role do" by reading the actions it may
// select instead of reading the code inside them.
//
// The vocabulary is in two halves, and the second is why the first is no longer
// the whole of it. The delivery names below are what the pipeline's own steps
// need; the names after them are derived from the authority inventory —
// `docs/authority-inventory.md`, held to the code by `internal/authority` — which
// is the enumeration of what the harness authorizes today. Deriving them from
// the inventory rather than inventing them is the order the design records: a
// name here that answers no check the inventory lists is authority nobody
// enforces.
//
// Which role holds which of these is `internal/rolecapability`, deliberately not
// here. A capability stays a primitive: two roles that need the same authority
// name it identically, and the sentence "the architect may amend a design" is one
// package away rather than folded into the vocabulary the actions declare
// against.
package capability

import "slices"

// Capability is one primitive an action requires in order to perform its work.
type Capability string

const (
	// WorkItemRead and WorkItemMutate are the tracker: reading what an item says,
	// and claiming, annotating, blocking, or closing it. They are apart because
	// almost everything reads the item and only the ends of a run change it.
	WorkItemRead   Capability = "work-item.read"
	WorkItemMutate Capability = "work-item.mutate"
	// RepositoryRead is reading repository content — the context a developer is
	// given, the paths a change touched, the diff a reviewer is shown. It carries
	// no write of any kind, which is what makes it the capability a step that only
	// looks at a change can be held to.
	RepositoryRead Capability = "repository.read"
	// RepositoryList is being told the names one directory of the repository
	// holds, at a recorded commit. It is apart from RepositoryRead because the two
	// answer different questions and are held by different roles: every role has
	// repository content read on its behalf — the diff, the context bundle — and
	// only the roles that hold both may name a path of their own to be read, which
	// is the management conversations' repository block. A role that may be told
	// what the tree holds is the role that may ask for one thing in it, and the
	// reviewer, whose evidence is the change, holds neither in that sense. It is
	// granted by the configurable-workflows design's authority-model section.
	RepositoryList Capability = "repository.list"
	// LogRead reads bounded, redacted operational records under the state root.
	LogRead Capability = "log.read"
	// WorktreeMutate is writing inside the run's own isolated worktree and on its
	// own branch: creating it, committing what a developer left, removing it once
	// its work is somewhere else. It never reaches the branch a run promotes into,
	// which is the next capability and deliberately not this one.
	WorktreeMutate Capability = "worktree.mutate"
	// TargetBranchMutate is moving the branch a run promotes into. It is the most
	// consequential thing the harness does and it is named on its own so that an
	// action requiring it is visible as such in the registry, without anybody
	// having to read what the action does.
	TargetBranchMutate Capability = "target-branch.mutate"
	// PromotionLease is taking the lease that admits one promotion at a time into
	// a target branch. It is separate from moving the branch because the lease is
	// what makes the move safe: an action that moves the branch without it is the
	// race the lease exists to stop, and separating them is what makes that
	// legible where the actions are declared.
	PromotionLease Capability = "promotion.lease"
	// ProviderInvoke is spending a provider invocation. It is the largest thing
	// this harness spends and the one every budget, hold, and pause is about.
	ProviderInvoke Capability = "provider.invoke"
	// ChecksExecute is running the project's configured checks. It executes
	// project-supplied commands, which is why it is its own capability rather than
	// part of reading a change: it is the one place a definition can cause
	// arbitrary project code to run.
	ChecksExecute Capability = "checks.execute"
	// ForgePublish is everything that reaches the forge and the remote: pushing a
	// run branch, opening and updating a pull request, asking for a merge, and
	// deleting the branch afterwards.
	ForgePublish Capability = "forge.publish"
	// RunStateMutate is writing the harness's own durable store: the run's record,
	// its event log, and the per-work-item counters kept beside it — the review
	// rounds an item has spent, which outlive any one run and are still the
	// harness's own bookkeeping rather than the tracker's. It is deliberately not
	// WorkItemMutate: nothing under this capability reaches the work item, and a
	// step that writes a counter here has not touched what the tracker says.
	//
	// Nearly every step needs it, and it is still named: a step that records
	// nothing about itself is a step reconciliation cannot reason about, so
	// declaring it is what makes the exceptions visible.
	RunStateMutate Capability = "run-state.mutate"
)

// The names below are the rest of what the harness authorizes, one per authority
// the inventory's rows actually tell roles apart by. They are not the pipeline's:
// no registered action requires one, and what requires them instead is the
// authorization sites the inventory lists, which ask who holds one rather than
// which role they are talking to. What they are for is that a role's authority can
// be stated in capabilities at all — a bundle built only from the delivery names
// above would say nothing about the four roles that never enter a run.
//
// The granularity is the inventory's rather than a tidier one. Where a row tells
// two roles apart, there is a name for what it tells them apart by; where it does
// not, there is not one.
const (
	// BacklogAdmit is putting new work into the backlog and taking it out again —
	// admitting, closing, retiring. It is apart from WorkItemMutate because that is
	// the boundary the harness enforces most often: the development manager may
	// build structure underneath admitted work all day and may not admit any, and
	// an item admitted by anything but the product manager is work nobody chose.
	BacklogAdmit Capability = "backlog.admit"
	// BacklogOrder is what is pulled next: priority, and parking admitted work out
	// of reach without taking it out of the queue. Order is the product manager's
	// in the same breath as admission and is still its own name, because a role
	// that may reorder without admitting is a coherent thing to write down.
	BacklogOrder Capability = "backlog.order"
	// WorkItemRepairState is correcting backlog state the records have made
	// stale: a blocked status left over from a stoppage that ended, a dependency
	// on work that closed, an attribution orphaned by an amendment to the goals.
	// It is the product manager's over her own backlog, granted by the
	// configurable-workflows design's authority-model section.
	//
	// It is apart from WorkItemMutate because its subject is not what an item
	// says but whether what the tracker records about it is still true, and apart
	// from BacklogAdmit because it is state hygiene rather than a decision about
	// scope: nothing held under it closes work, retires it, or takes it out of
	// the order, and an act under it is refused where the records do not say the
	// old state is stale.
	WorkItemRepairState Capability = "work-item.repair-state"
	// WorkDecompose is creating work underneath something already admitted. It is
	// the development manager's whole tracker authority beyond annotation, and it is
	// deliberately not BacklogAdmit: decomposition underneath an admitted parent is
	// not admission, which is the distinction the parent requirement enforces today.
	WorkDecompose Capability = "work.decompose"
	// WorkTriage is recording what was decided about work that stopped moving. Its
	// subject is a stopped run rather than the item's own fields, which is why it is
	// not WorkItemMutate: a decision nobody can find is the state triage exists to
	// leave behind.
	WorkTriage Capability = "work.triage"
	// ArtifactProductMutate and ArtifactDesignMutate are authorship of the
	// canonical documents, split the way ownership is: the brief, the goals and the
	// non-goals are the product manager's, and the designs, the specifications and
	// the decision records are the architect's.
	//
	// They are two names rather than one because one name would answer the
	// ownership check wrongly. "May this role amend an artifact?" is a question with
	// no true answer — it depends on the kind — and a vocabulary that could only ask
	// it that way would let the architect through on a goals document. The design
	// settles the eventual shape as one capability with a typed artifact-kind scope;
	// until scopes exist, these two are that scope written into the names.
	ArtifactProductMutate Capability = "artifact.product.mutate"
	ArtifactDesignMutate  Capability = "artifact.design.mutate"
	// InvariantMutate is creating, amending, or retiring an architectural
	// invariant. It is the architect's and is not folded into the design documents
	// it is extracted from: an invariant binds work that never reads the design, and
	// the harness refuses a change to one from every other role by name.
	InvariantMutate Capability = "invariant.mutate"
	// ResearchCommission is having the harness gather evidence from outside this
	// machine, and EvaluationRecord is writing down a durable recommendation about
	// an idea. Both are the product manager's today. They stay apart for the reason
	// the conversation authority already keeps them apart: one reaches outside and
	// decides nothing, the other decides nothing and is authority over what the
	// product's own record says it was advised.
	ResearchCommission Capability = "research.commission"
	EvaluationRecord   Capability = "evaluation.record"
	// ProposalRaise is handing the operator work to approve, and ConcernRaise is
	// stopping to put a question to them instead. They belong to the role that
	// decides what is admitted, and they are two names because they are the two
	// flags the conversation authority carries: a role that may propose and may not
	// stop is a different role from one that may do both.
	ProposalRaise Capability = "proposal.raise"
	ConcernRaise  Capability = "concern.raise"
	// ExchangeAsk is putting a question to another role on the inter-role ask
	// channel, and ExchangeAnswer is being put one. Neither carries authority to
	// decide anything — an ask is judgment-only and an answer resolves nothing —
	// and both are still capabilities, because whether the harness will carry a
	// question from a role, or to it, is a boundary the harness enforces.
	//
	// They are two names because the program manager's design names the two ends
	// separately; every role on the channel today holds both, which is the same
	// membership the one name used to state.
	ExchangeAsk    Capability = "exchange.ask"
	ExchangeAnswer Capability = "exchange.answer"
	// ReviewVerdict is returning the judgement a change is gated on. It is the
	// reviewer's alone, and naming it is what lets "no role but the reviewer decides
	// a verdict" and "the development manager may not override one" be the same
	// question asked twice rather than two rules kept in two places.
	ReviewVerdict Capability = "review.verdict"
	// AgentContextMutate is writing an agent's own durable memory: what it knows
	// about this project, recorded as revisions that enter its later invocations.
	// The `### Agent memory` section of `docs/designs/configurable-workflows.md`
	// requires that agent-authored memory be written only through typed context
	// actions the role contract owns, and this is the authority those actions
	// declare — so a role that may not hold it may not remember anything, whatever
	// a workflow definition or a persona says.
	//
	// Reading is deliberately not a capability beside it. What an agent knows is
	// assembled into its own prompt by the runtime, which is prompt assembly and
	// stays runtime-internal; what an operator reads is the audit history, which the
	// design settles as a CLI surface and never an agent one.
	AgentContextMutate Capability = "agent-context.mutate"
)

// The names below are the program manager's, from the capability set its design
// fixes (docs/designs/program-manager.md, "The fixed capability set"). They are
// declared here because that design names them in the registry's vocabulary and
// the vocabulary did not have them: a primitive a design names and nothing
// declares is one no bundle can hold.
//
// The first ten are the tracker writes a program manager may ask for, one per
// tracker action. They are deliberately not the product manager's and the
// development manager's names — BacklogAdmit, BacklogOrder, WorkItemMutate,
// WorkDecompose — because those are unscoped, and every one of these is scoped to
// the instance's lane: a program manager holding BacklogAdmit would hold close
// and retire with it, and one holding WorkItemMutate would hold every item in the
// tracker. A name per action is what lets the lane be a scope over each of them
// rather than a second list beside them.
const (
	// WorkItemAdmit is admitting new work to the backlog inside the lane, through
	// the same approvals.work_items door the product manager's admissions pass.
	// It is not BacklogAdmit: nothing under it closes or retires anything.
	WorkItemAdmit Capability = "work-item.admit"
	// WorkItemAttribute is recording which goal a lane item serves.
	WorkItemAttribute Capability = "work-item.attribute"
	// WorkItemUpdate is rewriting a lane item's own fields and appending to its
	// notes.
	WorkItemUpdate Capability = "work-item.update"
	// WorkItemLabel is putting a label on a lane item or taking one off — never
	// the lane's own label, which only the product manager or the development
	// manager takes off.
	WorkItemLabel Capability = "work-item.label"
	// WorkItemReprioritize, WorkItemPark, and WorkItemUnpark are what is pulled
	// next, inside the lane, at any priority.
	WorkItemReprioritize Capability = "work-item.reprioritize"
	WorkItemPark         Capability = "work-item.park"
	WorkItemUnpark       Capability = "work-item.unpark"
	// WorkItemLink and WorkItemUnlink are making a lane item wait on another item,
	// and undoing it. The reverse — an outside item made to wait on the lane — is
	// a mutation of the outside item and is not covered.
	WorkItemLink   Capability = "work-item.link"
	WorkItemUnlink Capability = "work-item.unlink"
	// WorkItemReparent is moving a lane item under a lane parent.
	WorkItemReparent Capability = "work-item.reparent"
	// ReadModelRead is being handed the one read model's queries: the standing,
	// the throughput windows, the capacity state, the docket and the reports pile
	// as counts, and the other instances' status lines. It reads the record the
	// operator's surfaces are projected from, and never a surface itself.
	ReadModelRead Capability = "readmodel.read"
	// LaneReportWrite is rewriting the instance's own lane report and nothing
	// else: the brief, pulled summary of where its lane stands.
	LaneReportWrite Capability = "lane-report.write"
	// ReportFile is filing a report at one of the three severities, the pass's
	// digest to the product manager included.
	ReportFile Capability = "report.file"
	// AmendmentPropose is proposing a change to a governed document its role does
	// not own, to be decided under the owner's authority.
	AmendmentPropose Capability = "amendment.propose"
	// ServiceRequestRestart is writing one durable request that the supervisor
	// restart a part the services section declares. It restarts nothing itself:
	// the supervisor's pass is the only executor, and the harness the only
	// invoker.
	ServiceRequestRestart Capability = "service.request-restart"
)

// declared is every capability this repository has, in the order above. It is
// the list Known answers from, so adding a constant without adding it here
// leaves the constant unusable rather than silently half-declared — which the
// package's own test is what catches.
var declared = []Capability{
	WorkItemRead,
	WorkItemMutate,
	RepositoryRead,
	RepositoryList,
	LogRead,
	WorktreeMutate,
	TargetBranchMutate,
	PromotionLease,
	ProviderInvoke,
	ChecksExecute,
	ForgePublish,
	RunStateMutate,
	BacklogAdmit,
	BacklogOrder,
	WorkItemRepairState,
	WorkDecompose,
	WorkTriage,
	ArtifactProductMutate,
	ArtifactDesignMutate,
	InvariantMutate,
	ResearchCommission,
	EvaluationRecord,
	ProposalRaise,
	ConcernRaise,
	ExchangeAsk,
	ExchangeAnswer,
	ReviewVerdict,
	AgentContextMutate,
	WorkItemAdmit,
	WorkItemAttribute,
	WorkItemUpdate,
	WorkItemLabel,
	WorkItemReprioritize,
	WorkItemPark,
	WorkItemUnpark,
	WorkItemLink,
	WorkItemUnlink,
	WorkItemReparent,
	ReadModelRead,
	LaneReportWrite,
	ReportFile,
	AmendmentPropose,
	ServiceRequestRestart,
}

// All is every capability this repository declares, in declaration order.
func All() []Capability {
	return slices.Clone(declared)
}

// Known reports whether this is a capability the repository declares. Anything
// else is refused where a registry is built, which is the whole of what stops a
// name arriving from outside trusted code.
func (c Capability) Known() bool {
	return slices.Contains(declared, c)
}

func (c Capability) String() string {
	return string(c)
}
