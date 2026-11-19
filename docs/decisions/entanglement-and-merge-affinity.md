---
id: entanglement-and-merge-affinity
kind: decision
title: Central code stays shared, its oversized files are split, and work touching the same files is ordered
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-06T17:30:00Z
      reason: operator design question of 2026-09-06 after a week of integration races and renderer collisions; ruled in conversation
    - action: amended
      by: architect
      at: 2026-11-19T21:55:55.512559Z
      reason: 'Rewording under the item replacing the coined vocabulary in the governed documents (yoyodyne-ifd.437.21): seams, gated, spine, monoliths, merge affinity, the record''s own hyphenated compounds, and the short form DM are replaced with ordinary words, title included. The decision, what it rejected, and its consequences are unchanged.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-19T21:55:55.512559Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3138, for document-3138.1 under the automatic approval policy
---
# Central code stays shared, its oversized files are split, and work touching the same files is ordered

**Decision.** The places where the harness concentrates on purpose (one read model, one pipeline, one governed vocabulary) are needed and stay. That each of them grew into a few very large files was an accident, and those files are split along boundaries that are already governed. The orchestrator's central pipeline file is broken up only through the action registry that the conversion to configurable workflows introduces, and in no other way. The rendering code is split into one file per section, behind the read model's boundary. Where work still collides after the split, it is ordered rather than restructured: the development manager records the files each item is expected to change, links items that overlap with a dependency so they run one after another, lets items that do not overlap run at the same time, and schedules work that touches the whole pipeline file for quiet hours. The scheduler reads that ordering for itself only once the readiness check that advises on it has been shown to work.

**Rejected.** Splitting the read model or the pipeline into a copy for each large piece of work: that builds surfaces that disagree with each other from the start. Breaking up the central pipeline file some other way ahead of the action registry: that leaves two competing ways of splitting one file. Treating two merges racing for the same target branch as code that is too tangled: the race is changes landing one at a time as designed, and the answer is ordering the work and making collisions less frequent, not new package boundaries.

**Consequences.** Large pieces of work that change the central pipeline file come after the milestone that wraps the pipeline's steps in actions, where the queue allows. Splitting the renderer is small, cheap work the development manager can break down now. The development manager's practice of recording expected files and dependencies starts now, and it becomes automatic only at the same point where the readiness check does.
