---
id: architect-loop
kind: design
title: 'The architect loop: a windowed pass over what landed, reconciling decisions against documents first'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-25T10:00:00Z
      reason: yoyodyne-ifd.339 - the architect's recurring pass designed on the operator's four shaping constraints; decided-versus-landed reconciliation as the first shipment, the pass windowed to landings since the last, findings naming instances, proposals through existing gates, and the one derivation the harness supplies so the pass reads rather than reconstructs
    - action: amended
      by: architect
      at: 2026-10-14T10:23:39.213147Z
      reason: 'Part of renaming the product manager role to Lead Product Manager where a person reads it (yoyodyne-ifd.437.10): two places in ''What a pass is'' and the third binding constraint. Nothing else changes; the stale ''daily'' cadence is left to its own item, yoyodyne-ifd.434.18.'
    - action: amended
      by: architect
      at: 2026-10-19T03:54:46.918009Z
      reason: 'Approved amendments 72766a9d and 2f99afd2: a pass runs in its own conversation beside the scheduler''s pull, one at a time in a conversation and several conversations at once up to a bound, replacing ''at most one firing per scheduler pull''. The schedule is the project''s, no longer ''daily''. Revisions and decisions on proposed changes are the architect''s own, confirmed under the kind''s approval policy, never recorded by the operator, which the autonomy goal requires. Invariant changes are held in the pass record until the architect can submit them. Adds what a pass starts from, as ruled on October 5 for bounded pass context (yoyodyne-ifd.430.47).'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-14T10:23:39.213147Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 924, for document-924.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-19T03:54:46.918009Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1250, for document-1250.1 under the automatic approval policy
---
# The architect loop: a windowed pass over what landed, reconciling decisions against documents first

## What this is for

The per-item roles cannot see drift that lives in the accumulation: a decision made and never landed, a pattern repeated across items that wants a shared home, a design a run quietly moved past, an invariant eroding one correct change at a time. The architect's recurring pass looks for exactly that and nothing else, and serves the chain goal directly: a decision that never reaches its document is a broken link in the chain, and nine such links cost weeks in 2026-09 before anything noticed.

## What a pass is

A recurring task on the architect's own conversation, on the schedule the project configures, under every rule the recurring-task design in [configurable-workflows](configurable-workflows.md) states: the same authority as a turn the operator opens, the spending pause gating it, and a durable pass record. A pass runs in its own conversation beside the scheduler's pull, never inside it, so a long pass holds up no developer run and no other role's pass. One conversation runs one pass at a time; passes of different conversations run at the same time, up to a bound the harness sets on how many run at once.

What a pass may change is what any architect turn may change, and nothing in it is put to the operator for approval:

- a revision of an architect document is written whole as a typed document write, confirmed under that kind's approval policy, and landed through a reviewed run that changes only that file;
- a proposed change to an architect document is decided by the architect, approved or declined with its reason, and the operator is told afterwards; an approval is followed by the revision that carries it;
- work is put to the Lead Product Manager as a report she handles;
- a change to a product document is an amendment proposal to its owner.

A pass with nothing to say files no report, per the communication rule.

## What a pass starts from

Each pass starts a fresh provider session from: the role contract, persona and remit; the architect's memory; the previous pass's account; the pass message; and the product intent whole. The architect's own documents are read when the pass needs them, at a recorded commit. What a pass starts from is bounded to a third of the provider's context, and the bound is never met by leaving out the contract, the intent, or the memory: a start that would pass it is refused before the provider is called and recorded as a missed pass naming the size. Memory holds the architect's judgment and the pass record holds the account of what the pass did. A compaction during a pass is recorded on the pass record, and a pass compacted twice is recorded as partial. Turns the operator opens are unchanged by this.

## The four constraints, binding

1. **Windowed.** A pass reads what landed since the previous pass — the runs integrated, the documents revised, the amendments decided — and never the corpus. The window is the pass record's own cursor over the run records and the artifact revision logs, the two the freshness measurement already reads.
2. **Instances, not impressions.** Every finding names the concrete records it generalizes from: two files, two items, two revisions. A finding that names one instance is an observation and is left in the pass record; a finding that names none is not filed.
3. **Through the gates.** A design revision is a typed document write; work is a report to the Lead Product Manager naming the goal it would serve; a product-document change is an amendment proposal; an invariant change is the architect's own act. Until the architect can submit an invariant change from the conversation as a typed write (the item for that, yoyodyne-0bj), the pass keeps the change landing-ready in its own record and holds it; it is not sent to the operator to approve. The pass admits nothing and directs nothing.
4. **Silent when empty.** A pass that reconciled nothing and found nothing says so in its record and posts nothing.

## The first shipment: decided against landed

Before any drift review, a pass reconciles the architect's own decisions against the governed documents, because that gap is cheap to check and has already cost the most. Two derivations, supplied by the harness rather than reconstructed by the role:

- **Decided, not landed.** Every amendment against an architect-owned document decided as approved whose identifier appears in no later revision-log reason of that document. The pass writes the revision that carries it, or names the revision that already carries it under another wording.
- **Ruled, not landed.** Every open work item carried by the architect's conversation whose notes or conversation record hold a ruling and whose owning document has no revision opening with the item's identifier, which is the convention the auto-close reads. The pass writes the revision or says what still blocks it.

Both are derived by the harness from the amendment log, the revision logs, and the tracker, delivered into the pass as a list, and carried in `yoyo amendment list --json` under `unlanded` so an operator can read the same answer without a turn. The pass never computes them from prose, per `surfaces-project-one-read-model`.

## The drift review, after that

Over the window and only the window: each landed run's item and the design it built against, read for an implementation decision the design does not carry; each reviewer finding that named a delivered invariant, read for a pattern across runs; each new package or file whose name repeats one that exists, read as a candidate for the entanglement question asked continuously. A finding names its instances and goes through the gate for its kind. The loop reaches the code only through the repository read at a recorded commit, bounded as every such read is.

## What this deliberately is not

A refactoring engine, a second reviewer, or a gate. Nothing a pass says stops a run, holds a merge, or reorders the backlog, and a pass is never a reason to skip the ruling a design question deserves in the conversation it was asked in.
