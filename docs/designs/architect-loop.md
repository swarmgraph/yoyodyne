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
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-14T10:23:39.213147Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 924, for document-924.1 under the automatic approval policy
---
# The architect loop: a windowed pass over what landed, reconciling decisions against documents first

## What this is for

The per-item roles cannot see drift that lives in the accumulation: a decision made and never landed, a pattern repeated across items that wants a shared home, a design a run quietly moved past, an invariant eroding one correct change at a time. The architect's recurring pass looks for exactly that and nothing else, and serves the chain goal directly: a decision that never reaches its document is a broken link in the chain, and nine such links cost weeks in 2026-09 before anything noticed.

## What a pass is

A recurring task on the architect's own conversation, daily or oftener as the project configures, under every rule the recurring-task design states: the same authority as a turn the operator opens, the spending pause gating it, a durable pass record, at most one firing per scheduler pull. It proposes and never fixes: revisions to its own documents are stated landing-ready and recorded by the operator; work is put to the Lead Product Manager as a report she handles; changes to product documents are amendment proposals. A pass with nothing to say files no report, per the communication rule.

## The four constraints, binding

1. **Windowed.** A pass reads what landed since the previous pass — the runs integrated, the documents revised, the amendments decided — and never the corpus. The window is the pass record's own cursor over the run records and the artifact revision logs, the two the freshness measurement already reads.
2. **Instances, not impressions.** Every finding names the concrete records it generalizes from: two files, two items, two revisions. A finding that names one instance is an observation and is left in the pass record; a finding that names none is not filed.
3. **Through the gates.** A design revision is landing text; work is a report to the Lead Product Manager naming the goal it would serve; a product-document change is an amendment proposal; an invariant is the architect's own act, stated for the operator to record. The pass admits nothing, edits nothing, and directs nothing.
4. **Silent when empty.** A pass that reconciled nothing and found nothing says so in its record and posts nothing.

## The first shipment: decided against landed

Before any drift review, a pass reconciles the architect's own decisions against the governed documents, because that gap is cheap to check and has already cost the most. Two derivations, supplied by the harness rather than reconstructed by the role:

- **Decided, not landed.** Every amendment against an architect-owned document decided as approved whose identifier appears in no later revision-log reason of that document. The pass states the landing text or names the revision that already carries it under another wording.
- **Ruled, not landed.** Every open work item carried by the architect's conversation whose notes or conversation record hold a ruling and whose owning document has no revision opening with the item's identifier, which is the convention the auto-close reads. The pass restates the ruling landing-ready or says what still blocks it.

Both are derived by the harness from the amendment log, the revision logs, and the tracker, delivered into the pass as a list, and carried in `yoyo amendment list --json` under `unlanded` so an operator can read the same answer without a turn. The pass never computes them from prose, per `surfaces-project-one-read-model`.

## The drift review, after that

Over the window and only the window: each landed run's item and the design it built against, read for an implementation decision the design does not carry; each reviewer finding that named a delivered invariant, read for a pattern across runs; each new package or file whose name repeats one that exists, read as a candidate for the entanglement question asked continuously. A finding names its instances and goes through the gate for its kind. The loop reaches the code only through the repository read at a recorded commit, bounded as every such read is.

## What this deliberately is not

A refactoring engine, a second reviewer, or a gate. Nothing a pass says stops a run, holds a merge, or reorders the backlog, and a pass is never a reason to skip the ruling a design question deserves in the conversation it was asked in.
