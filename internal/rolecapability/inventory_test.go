package rolecapability

// The registry held against the inventory it was derived from.
//
// Every row of `docs/authority-inventory.md` is one place the harness enforces
// role authority by naming a role. This table says, for each of them, the
// capability question a call site would ask instead — and where there is no such
// question, what is missing. The point of writing both down is that the second
// list is the honest one: a re-expression that quietly covered nine tenths of the
// inventory and read as though it covered all of it would be worse than one that
// covered half and said so.
//
// It is a table rather than a rule because each line is a judgement somebody made.
// A row that arrives in the inventory and is not answered here fails, so the next
// person has to make the same judgement out loud instead of the question never
// being asked.
//
// The questions below are what the converted sites now ask. Where a row's question
// is "none", the site still names what it names and the gap says why no bundle can
// express it; parity_test.go is where the converted answers are held against the
// decisions the role names used to give.

import (
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/authority"
	"github.com/mason-bryant/yoyodyne/internal/capability"
)

const repositoryRoot = "../.."

// expression is how one inventoried check reads in capabilities.
type expression struct {
	// question is what a call site would ask in place of the role name it asks
	// about today.
	question string
	// asks is every capability that question names. Each is checked against the
	// vocabulary and against the registry, so a sentence naming something nothing
	// declares or nobody holds fails here rather than reading plausibly.
	asks []capability.Capability
	// gap is what the question does not capture, and is empty where nothing is
	// lost. A row with no capabilities at all must have one: a check the vocabulary
	// cannot ask about is a gap, not a silent pass.
	gap string
}

var expresses = map[string]expression{
	"conversation.authority-table": {
		question: "what does this role's bundle hold?",
		gap:      "the table carries the contract and the operator-facing title beside the authority; a bundle carries capabilities alone, so what a role is called and what it is sent stay where they are",
	},
	"conversation.authority-derived": {
		question: "which of these does the role's bundle hold, and what follows from that about the conversation?",
		asks: []capability.Capability{
			capability.WorkItemRead, capability.WorkItemMutate, capability.BacklogAdmit,
			capability.BacklogOrder, capability.WorkDecompose, capability.WorkTriage,
			capability.ProposalRaise, capability.ConcernRaise, capability.ResearchCommission,
			capability.EvaluationRecord, capability.ExchangeAsk, capability.ExchangeAnswer,
			capability.RepositoryRead, capability.RepositoryList, capability.AgentContextMutate,
			capability.WorkItemAdmit, capability.WorkItemAttribute, capability.WorkItemUpdate,
			capability.WorkItemLabel, capability.WorkItemReprioritize, capability.WorkItemPark,
			capability.WorkItemUnpark, capability.WorkItemLink, capability.WorkItemUnlink,
			capability.WorkItemReparent,
		},
		gap: "the contract and the title are still written beside the derivation: what a role is sent and what it is called are not authority anybody holds",
	},
	"conversation.role-is-known": {
		question: "is there a bundle for this role at all?",
		gap:      "the lookup is against the derived table rather than against the registry directly, because a role also needs a contract to be addressable and no bundle carries one",
	},
	"conversation.session-opens": {
		question: "is there a bundle for the role a session is being opened for?",
		gap:      "the same lookup, made a second time and through the same derived table",
	},
	"conversation.authorize-reply": {
		question: "does the role hold the capability each block in the reply asks for?",
		asks: []capability.Capability{
			capability.ProposalRaise, capability.ConcernRaise, capability.ResearchCommission,
			capability.EvaluationRecord, capability.ExchangeAsk, capability.WorkItemMutate,
			capability.RepositoryRead, capability.RepositoryList, capability.AgentContextMutate,
			capability.LaneReportWrite, capability.ServiceRequestRestart,
		},
		gap: "the repository block is refused unless the role holds both the read and the list; that the named read is the pair is a rule of the derivation rather than a capability either half states",
	},
	"conversation.tracker-action": {
		question: "does the role hold the capability the action it asked for belongs to?",
		asks: []capability.Capability{
			capability.WorkItemRead, capability.WorkItemMutate, capability.BacklogAdmit,
			capability.BacklogOrder, capability.WorkDecompose, capability.WorkTriage,
			capability.WorkItemRepairState,
			capability.WorkItemAdmit, capability.WorkItemAttribute, capability.WorkItemUpdate,
			capability.WorkItemLabel, capability.WorkItemReprioritize, capability.WorkItemPark,
			capability.WorkItemUnpark, capability.WorkItemLink, capability.WorkItemUnlink,
			capability.WorkItemReparent,
		},
		gap: "which of the sixteen named tracker actions falls under which capability is a mapping this registry does not carry; the conversion wrote it down where the actions are, as `trackerCapabilities`",
	},
	"conversation.repair-is-stale": {
		question: "none: whether a state is stale is read off the records rather than off any role",
		gap:      "holding the capability is the whole of what a bundle can say here; that the status, the link, or the attribution has actually stopped describing the records is state read at the call site, and no bundle carries state",
	},
	"conversation.repair-hold": {
		question: "none: what somebody still has to release is a fact about the work rather than an authority anybody holds",
		gap:      "a bundle says the product manager may correct stale backlog state and cannot say which items are exempt from it; the hold is the harness's own record of stopped work and the directives in force, read as the act runs",
	},
	"conversation.contract": {
		question: "none: what is sent to a role is not what the role may do",
		gap:      "a contract sent verbatim ahead of a persona is a runtime guarantee, and no bundle can say a persona may not stand in for one",
	},
	"conversation.contract.product-manager": {
		question: "what the contract states in prose is this bundle: admission, order, the product's own documents, and correcting what the records have made stale",
		asks: []capability.Capability{
			capability.BacklogAdmit, capability.BacklogOrder, capability.ArtifactProductMutate,
			capability.WorkItemRepairState,
		},
	},
	"conversation.contract.architect": {
		question: "the architect changes designs and invariants and nothing else; the refusals are the capabilities its bundle does not hold",
		asks:     []capability.Capability{capability.ArtifactDesignMutate, capability.InvariantMutate, capability.BacklogAdmit, capability.WorkDecompose},
	},
	"conversation.contract.development-manager": {
		question: "does the development manager hold decomposition and triage, and not admission, order, or the verdict?",
		asks: []capability.Capability{
			capability.WorkDecompose, capability.WorkTriage, capability.BacklogAdmit,
			capability.BacklogOrder, capability.ReviewVerdict,
		},
	},
	"conversation.contract.developer": {
		question: "the developer holds no document, no queue, and no verdict; the contract's refusals are that absence",
		asks: []capability.Capability{
			capability.ArtifactProductMutate, capability.ArtifactDesignMutate, capability.InvariantMutate,
			capability.BacklogAdmit, capability.ReviewVerdict,
		},
	},
	"conversation.contract.reviewer": {
		question: "the reviewer holds the verdict and nothing that changes a document, a queue, or a worktree",
		asks:     []capability.Capability{capability.ReviewVerdict, capability.WorktreeMutate, capability.BacklogAdmit},
	},
	"conversation.contract.program-manager": {
		question: "the program manager holds lane-scoped tracker writes and nothing that admits outside a lane, triages, or changes a document; the contract's refusals are that absence",
		asks: []capability.Capability{
			capability.WorkItemAdmit, capability.BacklogAdmit, capability.WorkTriage,
			capability.ArtifactProductMutate, capability.ArtifactDesignMutate, capability.InvariantMutate,
		},
	},
	"conversation.lane-scope": {
		question: "does the role hold this action only through its lane-scoped name?",
		asks: []capability.Capability{
			capability.WorkItemAdmit, capability.WorkItemAttribute, capability.WorkItemUpdate,
			capability.WorkItemLabel, capability.WorkItemReprioritize, capability.WorkItemPark,
			capability.WorkItemUnpark, capability.WorkItemLink, capability.WorkItemUnlink,
			capability.WorkItemReparent,
		},
		gap: "which lane an instance owns is configuration, the label it is configured with, and no bundle carries it; the block-level refusals — no lane configured, the lane label removed, a directive carried out — are shapes of what was asked rather than capabilities",
	},
	"conversation.lane-at-act": {
		question: "does the role hold this action only through its lane-scoped name?",
		asks: []capability.Capability{
			capability.WorkItemAdmit, capability.WorkItemAttribute, capability.WorkItemUpdate,
			capability.WorkItemLabel, capability.WorkItemReprioritize, capability.WorkItemPark,
			capability.WorkItemUnpark, capability.WorkItemLink, capability.WorkItemUnlink,
			capability.WorkItemReparent,
		},
		gap: "which items are inside a lane is state — the label read off the item, and off a named parent, as the action runs — and no bundle carries state",
	},
	"conversation.admission-gate": {
		question: "does the role hold admission?",
		asks:     []capability.Capability{capability.BacklogAdmit},
		gap:      "holding it is not the whole gate: the intake hold and the approved-goal requirement are state read at the call site, and no bundle carries state",
	},
	"conversation.parent-required": {
		question: "does the role hold decomposition without admission?",
		asks:     []capability.Capability{capability.WorkDecompose, capability.BacklogAdmit},
	},
	"conversation.escalation-is-reported": {
		question: "none: an escalation carrying a report is a shape of what was said",
		gap:      "what a turn must contain alongside a decision is not an authority anybody holds",
	},
	"conversation.triage-run-belongs-to-item": {
		question: "does the role hold triage?",
		asks:     []capability.Capability{capability.WorkTriage},
		gap:      "holding triage does not say which run a decision may name; that is a scope over the subject, and scopes are the next step of this workstream",
	},
	"sidestream.no-action": {
		question: "does a side thread hold the capability this block asks for?",
		asks:     []capability.Capability{capability.WorkItemRead, capability.RepositoryRead},
		gap:      "it is asked of the thread rather than of the role, which no bundle can answer: a bundle says what a role holds, and this says what any role holds less of while it is answering beside its main thread",
	},
	"conversation.side-stream-narrows": {
		question: "which of the capabilities this role holds does a side thread hold too?",
		asks:     []capability.Capability{capability.WorkItemRead, capability.RepositoryRead},
		gap:      "the intersection is the answer and neither half of it is a bundle: the role's half is one, and the side thread's is the list above, which belongs to no role",
	},
	"sidestream.side-thread-contract": {
		question: "none: what a side thread is told it may do is invocation assembly",
		gap:      "the contract describes the same narrowing the list above holds, and what enforces it is that list rather than the words a role was sent",
	},
	"sidestream.reply-carries-no-authority": {
		question: "none: this refuses a shape of reply",
		gap:      "a harness block inside a side thread's answer is refused whichever role sent it, so no bundle expresses it",
	},
	"sidestream.side-prompt": {
		question: "none: which prompt is assembled for a side turn is invocation assembly",
		gap:      "what a role is sent is outside what a role may do, the same way the conversation contract is",
	},
	"conversation.crossing-is-justified": {
		question: "none: a crossing carrying its reason and naming its cap is a shape of what was said",
		gap:      "what a decision must contain to be recorded is not an authority anybody holds, exactly as the escalation's report is not",
	},
	"triage.delegated-crossing": {
		question: "does the role hold triage?",
		asks:     []capability.Capability{capability.WorkTriage},
		gap:      "holding triage does not say how far it may move a cap or how often; a step size and a per-item bound are scopes over the subject, and scopes are the next step of this workstream",
	},
	"triage.override-is-the-operators": {
		question: "none: the operator is not a role and holds no bundle",
		gap:      "what the operator may do by hand at a terminal is outside this vocabulary entirely, which is why the refusal has to live in the store rather than in a bundle",
	},
	"triage.crossing-record": {
		question: "does the role hold triage?",
		asks:     []capability.Capability{capability.WorkTriage},
		gap:      "the same scope gap the crossing above has, made of the durable record rather than of the act",
	},
	"exchange.ask-authority": {
		question: "does the asking role hold the asking end, and the asked role the answering end?",
		asks:     []capability.Capability{capability.ExchangeAsk, capability.ExchangeAnswer},
		gap:      "that a role may not put an ask to itself is a separation rule between two parties rather than a capability either of them holds",
	},
	"exchange.asking-contract": {
		question: "is the asking role on the channel?",
		asks:     []capability.Capability{capability.ExchangeAsk},
		gap:      "an ask being judgment-only and decisionless is what an ask is; the capability says only that the harness will carry one",
	},
	"exchange.answering-contract": {
		question: "does the answering role hold the answering end?",
		asks:     []capability.Capability{capability.ExchangeAnswer},
		gap:      "that an answer carries no authority is a property of the answer rather than of the answering role's bundle",
	},
	"exchange.answer-carries-no-authority": {
		question: "none: this refuses a shape of reply",
		gap:      "a harness block inside an answer is refused whoever sent it, so no bundle expresses it",
	},
	"exchange.answering-prompt": {
		question: "none: which prompt is assembled for an answering role is invocation assembly",
		gap:      "what a role is sent is outside what a role may do, the same way the conversation contract is",
	},
	"artifact.owner": {
		question: "which capability does this kind of document belong to?",
		asks:     []capability.Capability{capability.ArtifactProductMutate, capability.ArtifactDesignMutate},
		gap:      "the map from kind to capability lives in the ownership table, which is the half of it this registry does not carry; the two names here stand in for the artifact-kind scope the design settles, and a third owner would need a third name until scopes exist",
	},
	"artifact.authorize": {
		question: "does the role hold the capability the artifact's kind belongs to?",
		asks:     []capability.Capability{capability.ArtifactProductMutate, capability.ArtifactDesignMutate},
	},
	"artifact.unauthorized-error": {
		question: "none: this is how a refusal is reported",
		gap:      "the error a refused mutation returns says nothing about who may make one",
	},
	"artifact.unauthorized-revisions": {
		question: "was each recorded revision made by a role holding the capability its document's kind belongs to?",
		asks:     []capability.Capability{capability.ArtifactProductMutate, capability.ArtifactDesignMutate},
	},
	"artifact.create": {
		question: "does the creating role hold the capability the kind belongs to?",
		asks:     []capability.Capability{capability.ArtifactProductMutate, capability.ArtifactDesignMutate},
	},
	"artifact.amend": {
		question: "does the amending role hold the capability the kind belongs to?",
		asks:     []capability.Capability{capability.ArtifactProductMutate, capability.ArtifactDesignMutate},
	},
	"artifact.write-authorize": {
		question: "does the role writing a document from its conversation hold the capability the document's kind belongs to?",
		asks:     []capability.Capability{capability.ArtifactProductMutate, capability.ArtifactDesignMutate},
	},
	"artifact.check-write": {
		question: "does the role hold the capability the kind belongs to, for a document filed in that kind's home?",
		asks:     []capability.Capability{capability.ArtifactProductMutate, capability.ArtifactDesignMutate},
		gap:      "which home a kind is filed in is the project's configuration rather than a capability, so the home half of this check is not expressed here",
	},
	"artifact.identify": {
		question: "does the role recording goal identities hold the capability a goals document belongs to?",
		asks:     []capability.Capability{capability.ArtifactProductMutate},
	},
	"artifact.supersede-or-retire": {
		question: "does the ending role hold the capability the kind belongs to?",
		asks:     []capability.Capability{capability.ArtifactProductMutate, capability.ArtifactDesignMutate},
	},
	"invariant.authorize": {
		question: "does the role hold the invariants?",
		asks:     []capability.Capability{capability.InvariantMutate},
	},
	"invariant.unauthorized-error": {
		question: "none: this is how a refusal is reported",
		gap:      "the error a refused mutation returns says nothing about who may make one",
	},
	"invariant.revision-authorized": {
		question: "does the role a revision records hold the invariants?",
		asks:     []capability.Capability{capability.InvariantMutate},
	},
	"invariant.create": {
		question: "does the creating role hold the invariants?",
		asks:     []capability.Capability{capability.InvariantMutate},
	},
	"invariant.amend": {
		question: "does the amending role hold the invariants?",
		asks:     []capability.Capability{capability.InvariantMutate},
	},
	"invariant.retire": {
		question: "does the retiring role hold the invariants?",
		asks:     []capability.Capability{capability.InvariantMutate},
	},
	"agent-context.authorize": {
		question: "does the remembering role's bundle hold its own context?",
		asks:     []capability.Capability{capability.AgentContextMutate},
	},
	"agent-context.unauthorized-error": {
		question: "the same question, asked once and returned by every refusal of it",
		asks:     []capability.Capability{capability.AgentContextMutate},
	},
	"agent-context.merge-through-the-one-door": {
		question: "does the agent whose side thread this was hold its own context?",
		asks:     []capability.Capability{capability.AgentContextMutate},
	},
	"amendment.decide-under-owner": {
		question: "does the deciding role hold the capability the proposed-against document belongs to?",
		asks:     []capability.Capability{capability.ArtifactProductMutate, capability.ArtifactDesignMutate, capability.InvariantMutate},
	},
	"amendment.operator-decides": {
		question: "the authority follows from the document, which is to say from the capability its kind belongs to",
		asks:     []capability.Capability{capability.ArtifactProductMutate, capability.ArtifactDesignMutate, capability.InvariantMutate},
		gap:      "the operator is not a role here and holds no bundle; what this refuses is being asked for an authority at all",
	},
	"protectedpath.set": {
		question: "whose documents are these? the homes of everything a developer holds no capability over",
		asks:     []capability.Capability{capability.ArtifactProductMutate, capability.ArtifactDesignMutate, capability.InvariantMutate},
		gap:      "the set is built from configured directories, and a bundle names authority rather than paths",
	},
	"protectedpath.refused": {
		question: "none: which paths a change touched is physical containment",
		gap:      "a capability question and a path check answer different things about one edit, and the path check is the one that catches an editor",
	},
	"protectedpath.grants": {
		question: "none: a grant is per work item",
		gap:      "a grant is narrower than any bundle and outlives no run; nothing about a role's standing authority expresses it",
	},
	"protectedpath.grant-marker": {
		question: "none: the token a grant is written with is syntax",
		gap:      "how a grant is spelled is not authority",
	},
	"protectedpath.grant-instruction": {
		question: "none: what a refused developer is told is wording",
		gap:      "the sentence a refusal carries is not authority",
	},
	"protectedpath.provider-paths": {
		question: "none: these are paths the provider refuses above anything this harness permits",
		gap:      "authority this harness does not hold is not authority it can confer, and no bundle can reach it",
	},
	"protectedpath.beyond-grant": {
		question: "none: this reports which grants reach provider-refused paths",
		gap:      "the same paths outside this harness's authority, read from the other end",
	},
	"protectedpath.role-definitions": {
		question: "none: no role holds authority to write a role definition, and no grant confers it",
		gap:      "an absolute refusal is the absence of a capability rather than a capability some bundle lacks; a person writes a role definition and the operator activates it",
	},
	"protectedpath.grant-problems": {
		question: "the roles asked are the ones that put work in the queue",
		asks:     []capability.Capability{capability.BacklogAdmit, capability.WorkDecompose},
		gap:      "what it refuses is a property of the item's grants rather than of the admitting role's authority",
	},
	"protectedpath.condition-problems": {
		question: "the roles asked are the ones that put work in the queue",
		asks:     []capability.Capability{capability.BacklogAdmit, capability.WorkDecompose},
		gap:      "what it refuses is a property of the item's done-conditions against the artifact homes rather than of the admitting role's authority",
	},
	"run.gate-protected-paths": {
		question: "none: the gate compares a change's paths against the set and the item's grants",
		gap:      "physical containment again, made before a check suite is spent",
	},
	"run.grant-evidence": {
		question: "none: this decides which fields of an item a grant may be read from",
		gap:      "provenance of the item's own text, which no bundle describes",
	},
	"run.refuse-provider-grant": {
		question: "none: an item granting a path no provider honours is refused before it is claimed",
		gap:      "the same authority the provider withholds, checked at the other door",
	},
	"run.refuse-ungranted-condition": {
		question: "none: an item whose done-conditions live in a document the developer may not write is refused before it is claimed",
		gap:      "the developer's want of authority over the artifact homes, read off the item's text at the other door",
	},
	"run.developer-contract": {
		question: "the developer holds its worktree and nothing upstream of it",
		asks: []capability.Capability{
			capability.WorktreeMutate, capability.ForgePublish, capability.ArtifactProductMutate,
			capability.ArtifactDesignMutate, capability.InvariantMutate, capability.BacklogAdmit,
		},
		gap: "the contract refuses the developer performing the commit and the push its bundle holds for it. A bundle says what the harness may do on a role's behalf; what the agent may do itself is tool posture, a second axis this registry does not carry and the design settles as a scope",
	},
	"review.independent-invocation": {
		question: "does the role hold the verdict?",
		asks:     []capability.Capability{capability.ReviewVerdict},
		gap:      "that the verdict comes from its own invocation, with no session to resume and no tools, is separation; the design already records separation as a runtime rule a static bundle cannot prove",
	},
	"review.contract": {
		question: "does the role hold the verdict?",
		asks:     []capability.Capability{capability.ReviewVerdict},
		gap:      "a persona only specializing the contract is a property of prompt assembly",
	},
	"review.policy": {
		question: "was the change judged by a role holding the verdict, and checked before it?",
		asks:     []capability.Capability{capability.ReviewVerdict, capability.ChecksExecute, capability.TargetBranchMutate},
		gap:      "the reviewer being a different agent from the developer is a rule about two invocations rather than about one bundle",
	},
	"run.independent-invocations": {
		question: "none: two sessions being different is separation",
		gap:      "no statement of what a role may do distinguishes one invocation of it from another",
	},
	"runstate.independent-invocations": {
		question: "none: the same separation, demanded of the durable record",
		gap:      "the evidence outliving the process is storage rather than authority",
	},
	"backend.read-only-role": {
		question: "none: which roles reason over supplied evidence is tool posture",
		gap:      "posture is the second axis; the design settles it as a typed scope on a capability, and no scope exists yet",
	},
	"backend.supported-role": {
		question: "none: whether a backend can assemble an invocation for a role is what the provider can do",
		gap:      "provider capability is not role authority, and a bundle must not become a way to declare one",
	},
	"backend.no-tools-for-read-only": {
		question: "the one role with tools is the one holding its own worktree",
		asks:     []capability.Capability{capability.WorktreeMutate},
		gap:      "the correspondence is not the rule: what tools a role gets is decided from its posture, and a bundle that happened to line up with it proves nothing",
	},
	"backend.codex-sandbox-for-role": {
		question: "the one role that edits a worktree is the one given a sandbox it can write in",
		asks:     []capability.Capability{capability.WorktreeMutate},
		gap:      "the same gap the row above it has, and for the same reason: the sandbox a role gets is decided from its tool posture, and posture is the second axis the design settles as a typed scope on a capability. A bundle that happened to line up with the mapping would prove nothing about it",
	},
	"backend.claude-code-settings-sources": {
		question: "none: which settings files the provider reads is keeping the account's own configuration out of every role, and grants no role anything",
		gap:      "it withholds from every role alike and is decided from the developer's tool posture rather than from any capability, so a bundle has nothing to say about it",
	},
	"backend.claude-code-settings-for-role": {
		question: "none: turning off the account's memory and connectors, and the instruction files above a worktree, withholds from every role and grants nothing",
		gap:      "the same as the row above: what it withholds is the same for every role, and the developer's sandbox it keeps is the posture question the Codex sandbox row already names",
	},
	"promotion.lease": {
		question: "no role holds it; the registry records it as the harness's own, with the reason",
		asks:     []capability.Capability{capability.PromotionLease},
	},
	"run.integrate-under-lease": {
		question: "no role holds either half; moving the branch without the lease is refused where the harness does it",
		asks:     []capability.Capability{capability.PromotionLease, capability.TargetBranchMutate},
	},
	"landing.lease": {
		question: "no role holds it; the harness takes it in the landing's own process",
		gap:      "no capability names it: it serializes the landing checks the harness runs after a run is over, and it moves no branch and grants no authority, so there is nothing a role could be asked whether it holds",
	},
	"run.land-under-lease": {
		question: "none: running the landing checks without the landing lease is refused where the harness runs them",
		gap:      "the same as the lease above: queueing a landing is load control over the harness's own work rather than authority any role could hold",
	},
	"converge.catch-up-under-lease": {
		question: "no role holds either half; the reconciler is the harness under the same lease",
		asks:     []capability.Capability{capability.PromotionLease, capability.TargetBranchMutate},
	},
	"capability.known": {
		question: "none: the closed vocabulary is the ground every question above stands on",
		gap:      "a name being declarable is not a question about a role",
	},
	"action.registry-closed": {
		question: "none yet: an action declares what performing it requires, and this registry says who holds what",
		gap:      "nothing joins the two. An action's requirement is not yet checked against the bundle of whichever role a run is performing it for, which is the conversion this registry exists to make possible",
	},
	"workflow.catalog-closed": {
		question: "none yet: a catalog entry's capability is checked against the vocabulary and not against any role",
		gap:      "the same join, made where a definition is validated rather than where it runs",
	},
	"workflow.compiled-under-grant": {
		question: "none yet: a grant is assembled by its caller",
		gap:      "no grant is read from a role's bundle today; making the compile-time grant the role's is the call-site conversion this registry precedes",
	},
	"workflow.performed-under-grant": {
		question: "none yet: the grant a transition is performed under is assembled by its caller, exactly as the compile-time one is",
		gap:      "the same join again, made at the state boundary rather than at the compile. What the executor holds a step against is the action's own declaration and the grant it was handed; no bundle is read, so the authority enforced there is still the caller's rather than the role's",
	},
	"cli.conversation-contract-exists": {
		question: "none: holding a contract for a role is configuration of the conversation",
		gap:      "whether the harness can address a role at all is upstream of what that role may do",
	},
	"separation.policies": {
		question: "none: a policy is a rule over the vocabulary rather than a statement of who holds what",
		gap:      "each policy names capabilities that must not meet in one place; no bundle says whether two of them meet, because a bundle is about one role and a separation rule is about two things",
	},
	"separation.operation": {
		question: "does one thing require two capabilities that must never be held at once?",
		asks: []capability.Capability{
			capability.WorktreeMutate, capability.ReviewVerdict,
			capability.TargetBranchMutate, capability.PromotionLease,
		},
		gap: "which role would perform the operation is not asked at all, which is the point: the refusal is the combination's, so it holds for a role nobody has written a bundle for yet",
	},
	"separation.topology": {
		question: "can a step that moves the target branch be reached without both the checks and a verdict since the change was last written?",
		asks: []capability.Capability{
			capability.TargetBranchMutate, capability.ChecksExecute,
			capability.ReviewVerdict, capability.WorktreeMutate,
		},
		gap: "that the steps either side of it were actually two independent invocations is evidence a run records; a topology can keep them apart and cannot prove they were two",
	},
	"separation.holders": {
		question: "does any role hold either half of the promotion?",
		asks:     []capability.Capability{capability.PromotionLease, capability.TargetBranchMutate},
	},
	"workflow.separation-at-compile": {
		question: "none: this is where the policies above are asked of a definition",
		gap:      "the projection a compiled graph is read as carries capabilities and no role at all, which is what lets one set of rules be asked of a definition, of a registry, and of a bundle",
	},
	"rolecapability.role-bundles": {
		question: "this is the answer rather than a question: each role's bundle, in Go",
		gap:      "what a bundle cannot yet carry is scope — the artifact kind, the evidence class, the tool permission set the design settles",
	},
	"rolecapability.declared-ahead": {
		question: "which capabilities does a bundle hold that no site asks for yet?",
		asks: []capability.Capability{
			capability.ReadModelRead,
			capability.ReportFile, capability.AmendmentPropose,
		},
		gap: "holding one of these grants nothing until the site that asks for it is built; each entry names what builds it",
	},
	"rolecapability.bundles-closed": {
		question: "none: these refusals are about the table rather than about any role's authority",
		gap:      "a registry refusing its own construction says nothing about what a role may do once it is built",
	},
	"rolecapability.harness-held": {
		question: "which capabilities belong to no role at all?",
		asks:     []capability.Capability{capability.PromotionLease, capability.TargetBranchMutate},
	},
}

// TestEveryInventoriedCheckIsExpressedOrNamedAsAGap is the completeness claim
// this registry is reviewed on. It reads the inventory where it lives rather than
// from a copy, because a copy is exactly what cannot carry the row that arrived.
func TestEveryInventoriedCheckIsExpressedOrNamedAsAGap(t *testing.T) {
	t.Parallel()

	entries, _, err := authority.Inventory(repositoryRoot)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the inventory lists no checks; this test is reading the wrong thing")
	}
	registry := mustBuild(t)

	listed := map[string]bool{}
	for _, entry := range entries {
		listed[entry.Check] = true
		expressed, answered := expresses[entry.Check]
		if !answered {
			t.Errorf("%s lists %q and nothing here says how it reads in capabilities; add the question it becomes, or the reason there is not one",
				authority.InventoryPath, entry.Check)
			continue
		}
		if strings.TrimSpace(expressed.question) == "" {
			t.Errorf("%q is answered with no question at all", entry.Check)
		}
		if len(expressed.asks) == 0 && strings.TrimSpace(expressed.gap) == "" {
			t.Errorf("%q names no capability and no gap; a check the vocabulary cannot ask about is a gap rather than a silent pass", entry.Check)
		}
		for _, asked := range expressed.asks {
			if !asked.Known() {
				t.Errorf("%q asks about %q, which this repository does not declare", entry.Check, asked)
				continue
			}
			_, harness := registry.HarnessHolds(asked)
			if len(registry.RolesHolding(asked)) == 0 && !harness {
				t.Errorf("%q asks about %q, which nothing holds", entry.Check, asked)
			}
		}
	}
	for check := range expresses {
		if !listed[check] {
			t.Errorf("%q is answered here and %s lists no such check; remove the answer or correct the name",
				check, authority.InventoryPath)
		}
	}
}

// TestEveryCapabilityAnsweringAnInventoriedCheckIsUsed reads the table the other
// way. A capability no inventoried check needs is one this vocabulary invented
// rather than derived, which is the order the design settles: derived from the
// inventory, not ahead of it.
func TestEveryCapabilityAnsweringAnInventoriedCheckIsUsed(t *testing.T) {
	t.Parallel()

	asked := map[capability.Capability]bool{}
	for _, expressed := range expresses {
		for _, capable := range expressed.asks {
			asked[capable] = true
		}
	}
	// The delivery capabilities the pipeline's own actions require are exempt: they
	// were declared for the action registry, they are what a run spends, and the
	// inventory enumerates authorization sites rather than the operations behind
	// them. Everything else here has to answer a row.
	pipeline := []capability.Capability{
		capability.WorkItemRead, capability.WorkItemMutate, capability.RepositoryRead,
		capability.WorktreeMutate, capability.ProviderInvoke, capability.ChecksExecute,
		capability.ForgePublish, capability.RunStateMutate,
	}
	for _, declared := range capability.All() {
		if asked[declared] || slices.Contains(pipeline, declared) {
			continue
		}
		t.Errorf("%q answers no check the inventory lists; the vocabulary is derived from the inventory rather than invented beside it", declared)
	}
}
