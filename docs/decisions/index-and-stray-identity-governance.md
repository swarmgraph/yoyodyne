---
id: index-and-stray-identity-governance
kind: decision
title: Directory indexes are ungoverned, and stray artifact identity is reported rather than refused
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-03T16:45:00Z
      reason: recorded for yoyodyne-ifd.87.1; decided in conversation at the ifd.87 ruling
    - action: amended
      by: architect
      at: 2026-10-19T03:12:48.050629Z
      reason: 'approved amendment-c96320ba: the record now says a directory index''s ownership lines reach every role as rules, the index stays ungoverned, and the artifact contract''s table of kinds is their normative home and holds where the two disagree; it also records that the goal guidance was rehoused as Writing a goal. Sent again after the first send was refused for want of a free publishing place.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-19T03:12:48.050629Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1247, for document-1247.1 under the automatic approval policy
---
# Directory indexes are ungoverned, and stray artifact identity is reported rather than refused

**Decision.** Directory indexes are ungoverned by design: a `README.md` links and describes, and anything normative in one belongs in a governed artifact — enforced culturally by this record and mechanically by the artifact-contract specification being the shape's normative home. Artifact frontmatter found within the parent directories of the configured homes but outside every home is reported as stray, never refused; frontmatter on a `README.md` is reported as inert.

**What an index's ownership lines are.** A directory index says who owns the documents filed in its directory, who may change one, and that every other role proposes an amendment and waits rather than editing. Those lines reach every role as rules it works under, not as description, and the harness delivers them labelled that way. That does not make the index governed: it carries no identity, nothing traces to it, and nothing reports it stale. The normative home of those rules is the table of kinds in [the artifact contract](../designs/artifact-contract.md), which says which role owns each kind of document and where each is filed. Where an index and that table disagree, the table holds, and the index is the one to correct.

**Rejected.** Governing indexes: an index is derived from what it indexes, so a governed one is stale by construction and `yoyo stale` would report it forever. Scanning the whole repository for stray identity: it would walk fixtures and test data and report noise as governance. Treating an index's ownership lines as description only: roles would then have no rule telling them to propose rather than edit until they had read the artifact contract, and the lines exist so they do not have to.

**Consequences.** The goals index's checked-shape prose moved to the artifact-contract specification. Its guidance on what makes a good goal was rehoused by the Lead Product Manager as [Writing a goal](../product/goals/writing-goals.md), filed beside the goals. The bounded stray-identity scan is the reporting the harness owes.
