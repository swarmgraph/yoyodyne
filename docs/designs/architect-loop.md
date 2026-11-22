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
    - action: amended
      by: architect
      at: 2026-11-19T10:05:19.554207Z
      reason: 'Carries approved amendment-0d2fe158: an architect item counts as landed only when each document it names by path or identifier has a revision opening with its identifier, now that the closing fix (yoyodyne-ifd.437.47) has landed; records that an invariant revision closes no item. Answers reviewer report-e7be3aa6: invariant changes are now sent as typed writes (yoyodyne-0bj landed Nov 5), so the pass no longer holds them. amendment-34e202fb was already carried.'
    - action: amended
      by: architect
      at: 2026-11-19T16:34:42.199687Z
      reason: 'Replaces the retired word for a required check in four places with ordinary words, under the operator''s November 8 rule, as part of replacing retired words in the architect''s documents (yoyodyne-ifd.437.21): the spending pause holding a pass back, the third constraint now ''Through the usual routes'', a finding going through the route for its kind, and ''a check a change must pass''. Nothing else changes.'
    - action: amended
      by: architect
      at: 2026-11-22T21:24:30.446026Z
      reason: 'Design review against the code at main 7120a8560: the list of approved changes not yet written into their documents, and the unlanded field, are not built, so the section now says so and says how the architect covers the gap until they are; the closing of ruled items is built and is described as built. Build work reported to the Lead Product Manager.'
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
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-19T10:05:19.554207Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3066, for document-3066.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-19T16:34:42.199687Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3100, for document-3100.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-11-22T21:24:30.446026Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3575, for document-3575.1 under the automatic approval policy
---
# The architect loop: a windowed pass over what landed, reconciling decisions against documents first

## What this is for

The per-item roles cannot see drift that lives in the accumulation: a decision made and never landed, a pattern repeated across items that wants a shared home, a design a run quietly moved past, an invariant eroding one correct change at a time. The architect's recurring pass looks for exactly that and nothing else, and serves the chain goal directly: a decision that never reaches its document is a broken link in the chain, and nine such links cost weeks in 2026-09 before anything noticed.

## What a pass is

A recurring task on the architect's own conversation, on the schedule the project configures, under every rule the recurring-task design in [configurable-workflows](configurable-workflows.md) states: the same authority as a turn the operator opens, the spending pause holding it back like any other task, and a durable pass record. A pass runs in its own conversation beside the scheduler's pull, never inside it, so a long pass holds up no developer run and no other role's pass. One conversation runs one pass at a time; passes of different conversations run at the same time, up to a bound the harness sets on how many run at once.

What a pass may change is what any architect turn may change, and nothing in it is put to the operator for approval:

- a revision of an architect document is written whole as a typed document write, confirmed under that kind's approval policy, and landed through a reviewed run that changes only that file;
- a new invariant, or a change to one, is written the same way, as a typed document write of kind `invariant`, confirmed under the same approval setting as the architect's designs and landed through its own reviewed run; retiring one is still `yoyo invariant retire`;
- a proposed change to an architect document is decided by the architect, approved or declined with its reason, and the operator is told afterwards; an approval is followed by the revision that carries it;
- work is put to the Lead Product Manager as a report she handles;
- a change to a product document is an amendment proposal to its owner.

A pass with nothing to say files no report, per the communication rule.

## What a pass starts from

Each pass starts a fresh provider session from: the role contract, persona and remit; the architect's memory; the previous pass's account; the pass message; and the product intent whole. The architect's own documents are read when the pass needs them, at a recorded commit. What a pass starts from is bounded to a third of the provider's context, and the bound is never met by leaving out the contract, the intent, or the memory: a start that would pass it is refused before the provider is called and recorded as a missed pass naming the size. Memory holds the architect's judgment and the pass record holds the account of what the pass did. A compaction during a pass is recorded on the pass record, and a pass compacted twice is recorded as partial. Turns the operator opens are unchanged by this.

## The four constraints, binding

1. **Windowed.** A pass reads what landed since the previous pass — the runs integrated, the documents revised, the amendments decided — and never the corpus. The window is the pass record's own cursor over the run records and the artifact revision logs, the two the freshness measurement already reads.
2. **Instances, not impressions.** Every finding names the concrete records it generalizes from: two files, two items, two revisions. A finding that names one instance is an observation and is left in the pass record; a finding that names none is not filed.
3. **Through the usual routes.** A design revision is a typed document write; work is a report to the Lead Product Manager naming the goal it would serve; a product-document change is an amendment proposal; an invariant change is the architect's own act, sent as a typed document write in the same pass that decides it, never held in the pass record and never sent to the operator to approve. The pass admits nothing and directs nothing.
4. **Silent when empty.** A pass that reconciled nothing and found nothing says so in its record and posts nothing.

## The first shipment: decided against landed

Before any drift review, a pass reconciles the architect's own decisions against the governed documents, because that gap is cheap to check and has already cost the most. Two derivations are meant to be supplied by the harness rather than reconstructed by the role. Checked against the code at main 7120a8560, one is built and one is not.

- **Ruled, not landed: built.** Every open work item carried by the architect's conversation whose notes or conversation record hold a ruling, and for which a document the item names, by path or identifier, has no revision whose reason opens with the item's identifier. An item counts as landed, and the harness closes it, only when each document it names has such a revision. A revision of a document the item does not name, whatever its reason says, is written onto the item's notes as related and leaves the item open. An item that names no document closes on its first such revision, with a note saying no named document was checked. An invariant revision closes no item, because the closing reads design and decision files and not the invariants directory: the pass names such an item to the Lead Product Manager to close once the invariant lands. What is built hands the pass every open item its conversation carries, not only the ruled ones; for each it writes the revision or says what still blocks it.
- **Decided, not landed: not yet built.** Every amendment against an architect document decided as approved whose identifier appears in no later reason in that document's revision log. The pass writes the revision that carries it, or names the revision that already carries it under another wording. At main 7120a8560 the amendment log is read only for undecided proposals and the owner's recommendations on them, `yoyo amendment list --json` carries proposals and their decisions and no `unlanded` field, and no pass is handed this list. Until it is built, the architect keeps each approved change it still owes in its own memory, by the amendment's full identifier, and writes it when the document it names can carry it. That memory is the gap this derivation exists to close, and building it is work put to the Lead Product Manager.

Once built, both are derived by the harness from the amendment log, the revision logs, and the tracker, delivered into the pass as a list, and carried in `yoyo amendment list --json` under `unlanded` so an operator can read the same answer without a turn. The pass never computes them from prose, per `surfaces-project-one-read-model`.

## The drift review, after that

Over the window and only the window: each landed run's item and the design it built against, read for an implementation decision the design does not carry; each reviewer finding that named a delivered invariant, read for a pattern across runs; each new package or file whose name repeats one that exists, read as a candidate for the entanglement question asked continuously. A finding names its instances and goes through the route for its kind. The loop reaches the code only through the repository read at a recorded commit, bounded as every such read is.

## What this deliberately is not

A refactoring engine, a second reviewer, or a check a change must pass. Nothing a pass says stops a run, holds a merge, or reorders the backlog, and a pass is never a reason to skip the ruling a design question deserves in the conversation it was asked in.
