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
      at: 2026-10-07T15:08:15.816152Z
      reason: 'yoyodyne-ifd.437.14, also carrying yoyodyne-ifd.437.36 and its duplicate yoyodyne-ifd.437.43: the owning role decides proposals to its own documents, seeing the whole proposal, deciding against a named revision, one decision at a time, appended and never rewritten, with only changes to what the goals admit left to the operator; How a write reaches disk restated by governing approval setting to agree with automatic confirmation and reviewed landing (yoyodyne-ifd.433.21.1), including the human integration fallback, the goals and brief rule keyed on the setting, bounded returns to the owner, and a run that judged nothing retried unchanged. Resubmitted unchanged in substance after the forge refused the previous run''s push with its own server error.'
approvals:
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-10-07T15:08:15.816152Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 566, for document-566.1 under the automatic approval policy
---
# The artifact contract: specification shape and identity

This is the normative statement of what the harness checks of governed documents. It was previously taught only in a goals-directory index, which is ungoverned by design — indexes link and describe, and normative prose lives in governed artifacts. The configuration guide describes this contract for operators; this document defines it.

## Specification shape

A specification opens with an introduction saying what the thing is and why it exists, then states its goals under a heading whose **whole text** is `Goals`, at any level — a title merely opening with the word is a title. Each goal is one top-level list entry; its statement is that entry's opening paragraph rejoined onto one line, ending at a blank line, an unindented line, a nested entry, or the emphasized `*Supports: …*` trailer — recognized by the emphasis it opens with, written directly under the entry, indented with it, no blank line between. Content after the statement describes the goal; a heading below the `Goals` heading divides goals; the section ends at the next heading at the same level or above, or at **any** heading stating what the product will not do, wherever nested. A non-goals document states its content under a `Non-goals` heading and states no goals; index and non-goals documents are not malformed for lacking goals, and a document that should state goals and does not is still reported.

A goal entry may open with a stable identifier in square brackets — `- [traceable-chain] Maintain a traceable chain …` — lower-case letters, digits, and single hyphens. That identifier is the goal's identity: assigned once, never reused, and unchanged when the statement is reworded. An attribution resolves by identity where one is stated, and by the statement's words where none is. An identity carried by two active goals resolves to neither and is reported, because choosing between them would attribute work to a goal nobody picked.

## Identity

The file name is the id — lower-case letters, digits, hyphens — and a frontmatter id disagreeing with it is refused. Kinds: `brief`, `goals`, `non-goals`, `design`, `specification`, `decision`. Status: `draft`, `active`, `superseded`, `retired`, agreeing with the revision log, which is append-only and records at least the creation, under the role that owns the kind. Approvals are append-only and name the revision they were given for. Refused, never guessed at: no usable frontmatter, unknown fields, an id claimed by two files (both refused, each naming the other). Ungoverned by design: a `README.md` in any artifact home, and everything in the invariants directory, which carries its own scheme.

## How a write reaches disk

How a document written from a conversation reaches the repository depends on the approval setting that governs it, never on the name of its kind alone.

- **An automatic setting, with automatic integration.** The harness confirms the document under that setting and asks no one. It then opens a run that carries exactly the content the owning role wrote, permitted to change that one file and no other, with no developer rewriting it. The run is checked and independently reviewed like any change and lands through the normal integration path, so nothing is left uncommitted in the primary checkout. The landed file records who confirmed it and under which setting. A document already waiting in a conversation's store when this path is in place is confirmed the same way, by its stored identity and content, without its role writing it again. A restart between confirmation and landing neither loses the document nor lands it twice.
- **A human setting, or an automatic setting where integration is human.** The operator confirms the write. Authorize records the approval in the document's frontmatter against the revision it produced; publication is the operator's own commit, under their own identity. Until they commit, the changed document is an uncommitted change runs refuse to start over, deliberately: the artifact homes are what runs read as context, and a run based on half-landed intent is worse than a run that waits. The surface that performed the write says so at the moment of writing.
- **Documents governed by the goals or brief setting.** A revision not recorded as consistent with intent keeps the operator's confirmation whatever the setting says, and a new document of intent is never confirmed by the setting alone. This follows the governing setting, not the kind: non-goals and operating rules are governed by the goals setting, so the same rule holds for them.

A failure to publish never stops the operator's message from being delivered; it is told to the owning role, and publishing is tried again at the role's next message.

When a document's run does not land, only its owning role can mend it, because no developer worked on it. A run refused at review, failing a check, or conflicting with the target branch goes back to the owning role's conversation with the reviewer's findings, the failing check, or the conflicting paths. That role is named as the one to move it wherever the stop is shown, and its revised document opens a fresh run. Returns are bounded at three. A run that ended without judging the document, such as a check stopped by its time limit or a push the forge refused with its own error, has judged nothing: the document is retried unchanged, and its owner is not asked to rewrite it.

## Deciding a proposed change to an owned document

Roles that may not write a document propose changes to it instead. The role that owns the document decides each proposal. The operator decides only a proposal that would change what work the goals admit, as [who approves a change to intent](../decisions/who-approves-a-change-to-intent.md) states. Nobody approves on the operator's behalf, and no proposal its owner can decide ends waiting on the operator.

- **The owner sees the whole proposal before deciding.** On request and within a bound, the harness gives the owning role the complete proposal, every decision already recorded on it, the text of the revision it was written against, and a digest of that revision. A proposal shown to its owner and not yet decided is recorded as shown and undecided, which is different from one never shown. Showing a proposal is not deciding it.
- **A decision names the revision it was made against.** A decision records the proposal, the document revision its owner read, the verdict, and the reason. If the document has moved past that revision by the time the change would be published, the decision no longer applies, and the proposal goes back to its owner to be decided again against the current text.
- **Decisions on one document are taken one at a time.** Two decisions on the same document never race; the second waits until the first is recorded. Repeating the same decision on the same proposal and revision records nothing new.
- **Decisions are appended, never rewritten.** A later decision may replace an earlier one, and the record keeps both, with who made each and why. What applies is the latest decision on the latest revision.
- **An approved change is written by the owner.** Approving a proposal does not edit the document. The owning role writes the change as a revision of its own document, which reaches disk by the rules above, and that revision's reason names the proposal it carries.
