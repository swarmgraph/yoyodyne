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
      at: 2026-10-13T19:07:04.606849Z
      reason: 'yoyodyne-ifd.437.26: the contract lists the rules kind, which serves the user-directives goal, and adds a guidance kind for advice that states nothing to check against, both owned by the Lead Product Manager. The section on how a write reaches disk is replaced with the route a document takes now: written by its owner, checked for undefined words and broken links, confirmed by its kind''s approval policy, landed through a reviewed run changing only that file, and returned to its writer when that run fails, with the operator''s own commit kept only where a project cannot land a document through a reviewed run. It also says what the harness enforces. This covers the contract half of yoyodyne-ifd.437.36.'
approvals:
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-10-13T19:07:04.606849Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 842, for document-842.1 under the automatic approval policy
---
# The artifact contract: specification shape and identity

This is the normative statement of what the harness checks of governed documents, and of how a document written from a conversation reaches the repository. It was previously taught only in a goals-directory index, which is ungoverned by design — indexes link and describe, and normative prose lives in governed artifacts. The configuration guide describes this contract for operators; this document defines it.

## Specification shape

A specification opens with an introduction saying what the thing is and why it exists, then states its goals under a heading whose **whole text** is `Goals`, at any level — a title merely opening with the word is a title. Each goal is one top-level list entry; its statement is that entry's opening paragraph rejoined onto one line, ending at a blank line, an unindented line, a nested entry, or the emphasized `*Supports: …*` trailer — recognized by the emphasis it opens with, written directly under the entry, indented with it, no blank line between. Content after the statement describes the goal; a heading below the `Goals` heading divides goals; the section ends at the next heading at the same level or above, or at **any** heading stating what the product will not do, wherever nested. A non-goals document states its content under a `Non-goals` heading and states no goals; index, non-goals, rules and guidance documents are not malformed for lacking goals, and a document that should state goals and does not is still reported.

A goal entry may open with a stable identifier in square brackets — `- [traceable-chain] Maintain a traceable chain …` — lower-case letters, digits, and single hyphens. That identifier is the goal's identity: assigned once, never reused, and unchanged when the statement is reworded. An attribution resolves by identity where one is stated, and by the statement's words where none is. An identity carried by two active goals resolves to neither and is reported, because choosing between them would attribute work to a goal nobody picked.

## Identity

The file name is the id — lower-case letters, digits, hyphens — and a frontmatter id disagreeing with it is refused. Status: `draft`, `active`, `superseded`, `retired`, agreeing with the revision log, which is append-only and records at least the creation, under the role that owns the kind. Approvals are append-only and name the revision they were given for. Refused, never guessed at: no usable frontmatter, unknown fields, an id claimed by two files (both refused, each naming the other). Ungoverned by design: a `README.md` in any artifact home, and everything in the invariants directory, which carries its own scheme.

## Kinds

The kind decides who owns a document. There are eight.

| Kind | Owner | What it states |
| --- | --- | --- |
| `brief` | Lead Product Manager | What the product is, who it is for, and what finished means. |
| `goals` | Lead Product Manager | Goals under a `Goals` heading, each supporting the brief. |
| `non-goals` | Lead Product Manager | What a version deliberately does not do, under a `Non-goals` heading. |
| `rules` | Lead Product Manager | Standing rules the operator gave as directives, under a `Rules` heading. |
| `guidance` | Lead Product Manager | Advice for whoever writes or reads the product documents; nothing to check against. |
| `design` | architect | How what the goals ask for gets built. |
| `specification` | architect | A normative statement a design rests on, such as this one. |
| `decision` | architect | How something was decided, what was decided against, and what that cost. |

The Lead Product Manager's role key in configuration and revision logs is `product-manager`; the name a person reads is Lead Product Manager.

**Rules** serve the goal that user directives are durable, discoverable, and enforceable whichever agent received them. Each rule is one top-level list entry under a heading whose whole text is `Rules`, and names the directive it came from. A rules document states no goals and is not reported for it. Every role reads it as product intent and applies it to its own work.

**Guidance** serves the goal of a traceable chain: advice that shapes how goals are written still carries an identity and a revision log, so a change to it can be followed. A guidance document states no goals, non-goals, or rules, and is not reported for lacking any heading. The goals directory's *Writing a goal* is guidance; it was filed as `non-goals` only because this contract had no kind for it.

## How a document reaches the repository

1. **Written by its owner.** A role writes a document only of a kind it owns, whole, as a typed write from its conversation. The harness generates the frontmatter and records the revision under the owning role. Any other role proposes an amendment to the owner and waits.
2. **Checked before it is recorded.** The body is checked for a coined word or joined word the register of terms does not define, and for a link to a file or heading that is not there. Either refuses the write back to the writer in the same message, naming each one and its line; nothing is recorded.
3. **Confirmed as its kind's approval policy says.** Where the policy is automatic, the harness confirms the revision without asking the operator and records the policy that confirmed it. Otherwise the operator approves it with the harness's approve command, recorded against that revision; an approval is never recorded by editing a document by hand. A revision of the brief or goals needs the operator's approval unless the Lead Product Manager records it as consistent with intent, with the directing work item opening its reason.
4. **Landed through a run of its own.** A confirmed or approved document lands through a run that changes exactly that file and nothing else. The project's declared checks for documents run on it, or every configured check where it declares none, and an independent reviewer judges it before it is integrated the way any change is, through the pull request and the merge queue where those are enabled. No developer rewrites it.
5. **Returned to its writer when it does not land.** A run whose checks fail, whose reviewer refuses it, or whose change conflicts with the target branch returns the findings to the conversation that wrote the document. Revising it there opens a fresh run. After three returned runs for one document in one conversation, automatic publication of it stops and the writer revises its plan. A run stopped by something outside the work, such as the machine being busy, is kept and tried again and is not a return.
6. **Bounded while waiting.** Each role has a bounded number of documents waiting to land, set by the harness; a write past that bound is refused with the reason, and nothing is recorded.

This route leaves no uncommitted document in the primary checkout.

One exception remains. A project that keeps its product documents in a repository the harness does not integrate into, or that has no reviewer configured, cannot land a document through a reviewed run. There the write stops at the operator's working tree, the approval is recorded in the document's frontmatter against the revision it produced, and publication is the operator's own commit under their own identity. Until they commit, runs refuse to start over the uncommitted document, because the artifact homes are what runs read as context and a run based on intent that has not landed is worse than a run that waits. The surface that performed the write says so when it writes.

## What the harness enforces

Enforced in code, whatever a configuration or a role's instructions say:

- the kind list above, and the owner of each kind; a write of a kind the writer does not own is refused;
- the word and link checks, before anything is recorded;
- confirmation by the kind's approval policy, and the operator's approval for the brief and goals unless consistency is recorded with its directing item;
- that a landing run changes only the one document;
- the bound on documents waiting to land, and the stop after three returned runs;
- that runs refuse to start over an uncommitted document in an artifact home.

Left to the roles: what a document says, and whether a revision of the brief or goals is consistent with intent, which the Lead Product Manager decides and records.