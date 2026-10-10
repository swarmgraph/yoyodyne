---
id: artifact-contract
kind: specification
title: 'The artifact contract: specification shape and identity'
supports:
    - v1-harness-design
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:39:19Z
      reason: approved amendment c84e23a5 from yoyodyne-ifd.87 - the contract's normative home moves out of docs/product/goals/README.md, a directory index the harness skips by design, into a governed architect-owned specification, per the architect's ifd.87 decision
    - action: amended
      by: architect
      at: 2026-09-05T20:10:00Z
      reason: approved amendment 330a8eb3 from yoyodyne-ifd.280 - the 100.2 ruling recorded where developer runs can read it, a How-a-write-reaches-disk section carrying the settled publish shape; three spent runs measured the cost of it living in tracker notes
    - action: amended
      by: architect
      at: 2026-09-07T20:00:00Z
      reason: approved amendment 162f375c from yoyodyne-ifd.344 - goal identity recorded in its normative home, bracketed identifiers assigned once and never reused, attributions resolving by identity with prose fallback for entries stating none, and a duplicated identity resolving to neither and reported
    - action: amended
      by: architect
      at: 2026-09-25T04:00:00Z
      reason: approved amendment 62a9fcd1 from yoyodyne-ifd.418 - 'in force' retired from the register at the operator's objection; the sentence says 'active', which is the artifact vocabulary already
    - action: amended
      by: architect
      at: 2026-10-06T23:59:48.592022Z
      reason: 'Owning-role amendment decisions (yoyodyne-ifd.437.14): specify authenticated owner decisions, intent boundaries, serialized and recoverable decision recording, explicit operator overrides, pending-proposal delivery, historical reconciliation, and separate owner-authored publication; replace routine operator transcription with reviewed delivery while preserving document identity and history.'
approvals:
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-10-06T23:59:48.592022Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 54, for document-54.1 under the automatic approval policy
---
# The artifact contract: specification shape and identity

This is the normative statement of what the harness checks of governed documents. It was previously taught only in a goals-directory index, which is ungoverned by design — indexes link and describe, and normative prose lives in governed artifacts. The configuration guide describes this contract for operators; this document defines it.

## Specification shape

A specification opens with an introduction saying what the thing is and why it exists, then states its goals under a heading whose whole text is `Goals`, at any level — a title merely opening with the word is a title. Each goal is one top-level list entry; its statement is that entry's opening paragraph rejoined onto one line, ending at a blank line, an unindented line, a nested entry, or the emphasized `*Supports: …*` trailer — recognized by the emphasis it opens with, written directly under the entry, indented with it, no blank line between. Content after the statement describes the goal; a heading below the `Goals` heading divides goals; the section ends at the next heading at the same level or above, or at any heading stating what the product will not do, wherever nested. A non-goals document states its content under a `Non-goals` heading and states no goals; index and non-goals documents are not malformed for lacking goals, and a document that should state goals and does not is still reported.

A goal entry may open with a stable identifier in square brackets — `- [traceable-chain] Maintain a traceable chain …` — lower-case letters, digits, and single hyphens. That identifier is the goal's identity: assigned once, never reused, and unchanged when the statement is reworded. An attribution resolves by identity where one is stated, and by the statement's words where none is. An identity carried by two active goals resolves to neither and is reported, because choosing between them would attribute work to a goal nobody picked.

## Identity

The file name is the id — lower-case letters, digits, hyphens — and a frontmatter id disagreeing with it is refused. Kinds: `brief`, `goals`, `non-goals`, `design`, `specification`, `decision`. Status: `draft`, `active`, `superseded`, `retired`, agreeing with the revision log, which is append-only and records at least the creation, under the role that owns the kind. Approvals are append-only and name the revision they were given for. Refused, never guessed at: no usable frontmatter, unknown fields, an id claimed by two files (both refused, each naming the other). Ungoverned by design: a `README.md` in any artifact home, and everything in the invariants directory, which carries its own scheme.

## Owning-role amendment decisions

Owning-role amendment decisions (yoyodyne-ifd.437.14) replaces routine operator disposition of proposals with a typed action performed by the harness under the document owner's authority. This serves the goals of autonomous operation and preventing downstream roles from redefining upstream intent.

### Ownership and reserved intent

Resolve document identity and kind through the canonical artifact store and ownership table. The architect owns designs, specifications, and decision records. The Lead Product Manager owns the brief, goals, and non-goals. Other roles propose changes and do not decide them merely because they raised, routed, or implemented the work. Authority is checked in trusted code; a persona, configuration field, request parameter, or protected-path grant cannot confer it.

The owner decides proposals within approved intent. A change that would admit work the goals previously refused, or refuse work they previously admitted, is a fundamental intent change: the Lead Product Manager drafts it and the operator decides it. Adding, removing, or changing the scope of a goal crosses that boundary. Consistent rewording does not. An architect must not approve a design proposal that quietly changes this product boundary; it remains unresolved pending the intent decision and subsequent owner reconciliation.

The operator retains direct intervention and override on any document. That retained power is not a routine approval stage and must not be exercised by an assistant claiming to act on the operator's behalf.

### Typed decision request

Register one amendment-decision operation usable by an authorized owner conversation and by authenticated human entry points. A request names the proposal, approve or decline verdict, a reason, an operation identifier, the expected pending state, and the document revision and content digest against which the judgment was made. Reasons are required for both new owner verdicts and are bounded by the existing decision-reason limit. At most ten requests may be carried in one conversation reply; each receives its own durable outcome.

The harness derives the actor, role, product, repository, conversation, turn or invocation, and authority-policy revision from trusted execution context. The request cannot supply or override those identities. Resolve the proposal and current document again when executing the request. Refuse cross-product references, a non-owner actor, changed ownership, ambiguous document identity, stale document evidence, or a proposal already decided by a different operation. A refusal records why and leaves the proposal undecided by this request.

Reuse the existing approved and declined verdicts and the distinction between authority and actual decider. An owner action records `owner` as decider and the resolved owning role as authority. Human action records the authenticated person and `operator` as decider. Neither path impersonates the other.

A proposal lacking an original revision remains readable. Record that original basis as unknown, and require the owner to read the current document before deciding it. Never invent historical evidence. The decision records the proposal identity and digest, current document identity, path, kind, revision and digest, verdict, reason, actual actor, exercised authority, operation identity, and recording time together. Decision evidence must not depend solely on conversation memory or an ephemeral process.

### Serialization and uncertain writes

All entry points use the same trusted operation and storage discipline, including CLI, conversation, dashboard where authorized, and reconciliation. Serialize the complete read, authorization check, expected-state comparison, append, and durable acknowledgment for a product's amendment log. Per-conversation locking alone is insufficient because different conversations and human commands can decide the same proposal.

Preserve append-only history. The first ordinary decision remains effective; a racing different decision receives a conflict naming it. Return success only for the effective decision the operation actually recorded or recovered. Retrying an operation identifier with identical content returns its prior result; reusing it with different content is refused. An ordinary repeated approve or decline is not an override.

Persist operation identity and decision evidence in the same durable record or transaction. After an uncertain append, sync, or acknowledgment, re-read under serialization and reconcile that operation before writing again. A complete matching record may be adopted after durability is established. A partial or malformed record blocks authoritative decision processing and produces a visible storage-recovery condition; do not skip it to decide from an incomplete history or silently erase it. A diagnostic scan may show readable records but cannot authorize decisions across the damaged history.

Version the record extensions and update all effective-decision consumers together. Legacy records retain their original attribution and first-decision meaning. An incompatible reader or writer must refuse authoritative operation on an unsupported record version rather than ignore an override or new decision evidence.

### Explicit operator override

An override is a separate append-only operation, available only through an authenticated operator action. It names the proposal, expected effective decision identity or digest, replacement verdict, current document basis, and reason. Serialize it with ordinary decisions. A stale expected decision is refused, so two interventions cannot silently overwrite one another.

Keep the original decision and every override visible. Effective-decision readers follow the validated override chain; they do not reinterpret a second ordinary decision as an override. The record says who intervened and which decision was replaced.

Before a governed revision is accepted for publication, recheck its effective authorization and document basis through the shared operation. Serialize the authorization check with the durable publication admission; do not hold the amendment lock across remote waits. An override invalidates publication that has not yet been admitted. If publication is already admitted or has landed, record its actual state and route corrective revision work to the owner. An override is not a claim that a repository change was undone.

### Delivery, reporting, and historical recommendations

Delivery marks describe context already sent, not decisions made. Failed turns, compaction, and conversation replacement must not strand undecided proposals. Provide a bounded owner-scoped pending listing and full proposal read through the registered tools; retain counts of omitted entries. Previously delivered pending proposals remain discoverable and can be redelivered for an owner decision. A remembered recommendation cannot suppress an item from the undecided queue indefinitely.

Successful owner decisions are reported to the operator afterwards with the document, proposal, verdict, deciding role, reason, and publication state. Surfaces say awaiting owner decision or awaiting owner revision where appropriate. They name the operator only for reserved intent or an act only a person can perform, using the ownership registry. Notification failure does not roll back a durable decision; retry delivery by decision identity.

Historical recommendations are not silently converted into decisions. For the eight unrecorded architect recommendations identified by Owning-role amendment decisions (yoyodyne-ifd.437.14), recover the full proposal and source recommendation, inspect the current document and any existing decision, and then record the owner's present disposition through the same operation. Preserve references to the earlier recommendation without backdating the new decision. Missing, truncated, ambiguous, or superseded source material is reported individually. Existing operator decisions retain their true actor and are not relabeled as owner decisions.

### Decision and publication are separate

Approval changes the proposal's disposition only. It writes no proposed prose into the artifact and does not establish that the design has changed. The document owner composes the resulting revision, naming the decision and reasoning. Decline records the reason and authorizes no revision. An approved proposal remains visible as awaiting an owner revision until a corresponding revision exists, and then as awaiting publication until landing is confirmed.

Reject automatic insertion of proposal text: it would let a downstream proposer author an upstream document through a differently named path. Reject a routine human confirmation of an owner decision: it would preserve the operator dependency this design removes. Independent review of the resulting repository change remains required and is distinct from deciding the proposal.

## How a write reaches the repository

The target publication path for owner-authored documents is a recorded owner revision delivered through an isolated, independently reviewed run. It does not commit, branch, or reset the operator's primary checkout. The owner decides the content; the harness prepares the exact document and its append-only metadata; the delivery run cannot rewrite that content on the owner's behalf. A review finding requiring a semantic change returns to the owner for a new revision.

An owner-authored revision consistent with approved intent records its actor, authority, intent classification, and reason. It does not invent an operator approval or require the operator to approve the delegated decision again. Preserve existing approval history. Fundamental intent changes retain their required operator decision, linked to the exact content authorized. Unknown intent classification is refused for autonomous publication until the owning role resolves it; it is not silently classified as consistent.

The publication record identifies the document, expected predecessor revision and digest, proposed content digest, supporting decision where applicable, authority evidence, delivery operation, review, candidate, and confirmed landing commit. Concurrent document changes require owner reconciliation, not a blind overwrite. Retry and restart adopt existing delivery evidence under the same identity rather than creating duplicate publication work. Apply the existing protected-path and revision-bound integration rules.

Earlier typed writes that stopped in the operator's working tree must be reconciled as existing records. Preserve their contents and history; do not replace an unknown outcome with another draft or claim that absence from HEAD proves the write failed. Report preparation, working-tree write where one exists, reviewed publication, and confirmed landing as separate facts.

Automatic publication of owner-authored documents (yoyodyne-ifd.433.21.1) supplies this delivery mechanism. This contract defines required behavior and does not assert that it is deployed. Until the supported operation exists, report the concrete delivery obstacle against its existing owner rather than describing publication as completed or routinely assigning a manual commit to the operator.

A work item whose deliverable is a governed ruling remains open until the required revision and its attribution are present in the repository. An amendment decision alone does not close that work or release downstream implementation.

## Acceptance

Verify owner approval and decline through a conversation, refusal of forged roles and non-owner requests, the reserved fundamental-intent path, stale document rejection, and legacy unknown-basis handling. Exercise a CLI decision racing an owner decision, identical-operation retries, conflicting reuse, and crashes before and after append, sync, and acknowledgment. Confirm that only one ordinary effective decision stands and every caller reports the durable result accurately.

Verify explicit operator overrides racing decisions and publication admission, preserved history, incompatible-reader refusal, and malformed-log refusal. Exercise a failed turn after proposal delivery, compaction, and a new conversation: undecided proposals must remain discoverable. Reconcile historical recommendations without fabricated identities, timestamps, or approvals.

Finally, verify that approval writes no artifact, that the owner authors the revision, that independent review and candidate-bound checks govern publication, that a stale predecessor cannot be overwritten, and that interrupted delivery is recovered without a duplicate write or false landing claim. Operator-facing reports must distinguish decided, revised, and landed in ordinary language.