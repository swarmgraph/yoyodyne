---
id: authority-by-capability
kind: decision
title: Authority is enforced as capabilities in Go; role names become data only after parity
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-31T15:57:48Z
      reason: recorded while promoting the configurable-workflows brief; refines the fixed-roles ruling the harness design records
    - action: amended
      by: architect
      at: 2026-10-12T08:08:43.803447Z
      reason: 'amended for the owning role deciding amendments (yoyodyne-ifd.437.14): deciding an amendment is a capability primitive held by the architect and Lead Product Manager bundles over the documents each owns; the person''s dashboard grant is unchanged and nothing becomes configurable'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-12T08:08:43.803447Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 703, for document-703.1 under the automatic approval policy
---
# Authority is enforced as capabilities in Go; role names become data only after parity

**Status:** Accepted. Refines the fixed-roles ruling (recorded via amendment e43b8198) rather than reversing it.

## Context

The fixed-roles ruling held that authority a project could declare is authority a project could widen. It was right, and it also makes every specialist - an observer, an assessor - a harness code change, which does not scale to other people's projects.

**Decision.** Authority semantics stay in trusted Go as capability primitives plus runtime separation policy. Ordinary configuration still cannot touch authority. What becomes configurable — protected, operator-activated, digest-pinned, and only after authorization-by-capability reaches behavioral parity with the five shipped roles — is the *composition* of known primitives into named bundles.

**Rejected.** Roles fixed as Go constants forever: blocks specialists behind code changes and invites bespoke orchestration per specialist, which is the pattern this whole design retires. Authority in ordinary configuration or personas: rejected before, still rejected, and now enforced by invariant. A plugin API for new primitives: rejected — a primitive's semantics must be implemented and reviewed in-tree, because somebody must be accountable for what a capability *means*.

**Consequences.** The parity sequence in the configurable-workflows design is binding: the authority inventory and capability registry land before anything is configurable, authorization call sites convert from role names to capability-and-scope checks before operator-defined bundles load, and the closed role-name type is removed last. Durable records created under the original five names keep compatibility decoding. The harness design's fixed-roles statement gains the refinement rather than being erased.

**Amended (yoyodyne-ifd.437.14).** Deciding an amendment is a capability primitive, `amendment.decide`, implemented in Go and held by the architect and Lead Product Manager bundles over the documents each role owns: the architect for designs, specifications, decision records, and invariants, the Lead Product Manager for documents filed in the product specification home. A proposal against the brief or a goals document that changes intent, or does not say whether it does, stays the operator's, per who-approves-a-change-to-intent. The person's `decide-amendments` grant from the dashboard design stays a property of the person, and the operator may override any decision. This adds a primitive to the shipped bundles; it makes nothing configurable, and the parity sequence above is unchanged.
