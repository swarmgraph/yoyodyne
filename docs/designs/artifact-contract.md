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
    - action: amended
      by: architect
      at: 2026-10-15T16:54:26.434698Z
      reason: 'Answers the reviewer''s report on the change for stopped document publications (yoyodyne-4u4): point 5 said a run that conflicts with the target branch goes back to its writer, which that change replaces. Point 5 now covers failing checks and a refusing reviewer, and says a run stopped by something outside the work goes to the development manager once its retries are spent; a new point 6 says a run stopped because the target branch''s copy of the document changed is held for the development manager, never restarted by the harness, and a retry is refused once a later confirmed revision is on the target branch. The enforced list gains that rule.'
    - action: amended
      by: architect
      at: 2026-10-15T18:27:59.222309Z
      reason: 'Answers the reviewer''s refusal on pull request #1295 (report-4b5da912), whose finding still applies to the copy that later landed through pull request #1296. Points 5 and 6 now say what the publication code does, read at 1ca86a02. Only a copy found changed when the run''s checkout is cut is held for the development manager. A failing check, a refusing reviewer, a conflict found while landing, and a file a document run may not write each return to the writer, and runs stopped for reasons outside the work are tried at most three times before the development manager decides. The enforced list names both rules.'
    - action: amended
      by: architect
      at: 2026-10-19T05:27:08.569382Z
      reason: 'Approved amendments 8f47877a and 4154b71c, rewritten after the reviewer refused document-1253.1 for listing as enforced three rules the approval code does not yet carry out. Point 3 and the enforced list now say only what the code does today: confirmation recorded under the harness, and the brief and goals confirmed without the operator only on a revision marked consistent under an automatic setting. The three cases for every document the Lead Product Manager owns are kept as design in their own section, marked not yet built, naming the harness change asked of the development manager on October 18.'
    - action: amended
      by: architect
      at: 2026-10-19T07:43:52.708307Z
      reason: Answers the development manager's warning (report-0a2ba84a). The section kept as design now names the build work it waits on, the work on approving the product documents (yoyodyne-ifd.433.19), instead of describing it as the change asked for on October 18. Nothing else changes; the enforced list already claims only what the approval code does.
    - action: amended
      by: architect
      at: 2026-10-21T15:54:25.305554Z
      reason: Approved amendment e3732c89, proposed by the developer building the approval of product documents. The section kept as design, still marked not yet built, now says an unmarked document of any kind in the product home waits for the operator whatever its approval setting, and that the Lead Product Manager's approval is refused on any revision other than the one she marked. This matches that build's done-means, so its reviewer reads a design that agrees with it. Point 3 and the enforced list are unchanged until the build lands.
    - action: amended
      by: architect
      at: 2026-10-21T18:16:10.468958Z
      reason: 'The approval of product documents (yoyodyne-ifd.433.19) landed on main at d6321148 on October 21. As the previous revision said it would, the three cases for every document the Lead Product Manager owns move into point 3 and the enforced list, and the section that described them as not yet built is retired. The exception for projects with no reviewer or a separate intent repository now states the gap the build''s reviewer named: a document marked consistent is still put to the operator there.'
    - action: amended
      by: architect
      at: 2026-10-23T16:03:02.533094Z
      reason: Answers the reviewer's warning on the build making goals changes wait for the operator (report-a96fc34b). Point 3 now says that creating a goals document or the brief, and a revision of either that adds or removes a statement under its Goals heading, always wait for the operator whatever their mark, and that the reviewer of a run landing either kind tests a consistent mark against what the goals admit and refuse. The sentence saying a consistent mark never adds an approval for the operator is corrected to match. The enforced list is unchanged and says these exceptions are not yet enforced until that build (yoyodyne-ifd.433.51) lands on main.
approvals:
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-10-13T19:07:04.606849Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 842, for document-842.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-10-15T16:54:26.434698Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1023, for document-1023.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 6
      by: harness
      at: 2026-10-15T18:27:59.222309Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1030, for document-1030.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 7
      by: harness
      at: 2026-10-19T05:27:08.569382Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1257, for document-1257.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 8
      by: harness
      at: 2026-10-19T07:43:52.708307Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1264, for document-1264.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 9
      by: harness
      at: 2026-10-21T15:54:25.305554Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1425, for document-1425.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 10
      by: harness
      at: 2026-10-21T18:16:10.468958Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1432, for document-1432.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 11
      by: harness
      at: 2026-10-23T16:03:02.533094Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1565, for document-1565.1 under the automatic approval policy
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
3. **Confirmed as its kind's approval policy says.** An approval is never recorded by editing a document by hand.
   - **The architect's documents** — design, specification and decision. Where the kind's policy is automatic, the harness confirms the revision without asking the operator and records, under its own name, the policy that confirmed it. Otherwise the operator approves it with the harness's approve command, recorded against that revision.
   - **The Lead Product Manager's documents** — brief, goals, non-goals, rules and guidance, whether she creates the document or revises it. Each is confirmed by one of three cases, and no approval setting confirms one on its own:
     - *Marked consistent with intent,* with its directing work item opening the reason. The harness confirms it without asking the operator, whatever the kind's approval setting, under the authority the operator gave her on September 26. The approval is recorded as hers, naming her and the directing item, never as the operator's, and it is refused where it would be attached to any revision other than the one she marked. Two changes are never confirmed this way, whatever their mark: creating a goals document or the brief, and a revision of either that adds a statement under its `Goals` heading or removes one. Adding or removing a goal always changes what the goals admit or refuse, so each of these waits for the operator's approval, and the harness says why: that the document is new, or which statement was added or removed. A revision of either that only rewords a goal, or changes nothing under its `Goals` heading, is still confirmed under her mark.
     - *Marked as a change of fundamental intent.* It always waits for the operator's approval, whatever the kind's approval setting.
     - *Not marked.* It waits for the operator's approval, whatever its kind and whatever its kind's approval setting.

   The test for which mark applies is the one in [who approves a change to intent](../decisions/who-approves-a-change-to-intent.md): a change that would make the goals admit work they refused, or refuse work they admitted, is fundamental, and any other change is consistent. A document that is not marked waits for the operator because only the Lead Product Manager can say that a change leaves intent where it was. Where she has not said so, the harness has no grounds to treat the change as consistent, and the operator decides. This adds no routine approval for him: any change she marks consistent is still confirmed without him, except the two above, which change what the goals admit or refuse and so are never consistent whatever they are marked. An earlier design let an unmarked non-goals, rules or guidance document follow its kind's approval setting; that was dropped because it let a setting stand in for her judgment.

   The reviewer of the run that lands a goals document or the brief checks a consistent mark against what the goals admit and refuse, and refuses the change where the mark is wrong, naming what the change would admit or refuse that the goals did not.
4. **Landed through a run of its own.** A confirmed or approved document lands through a run that changes exactly that file and nothing else. The project's declared checks for documents run on it, or every configured check where it declares none, and an independent reviewer judges it before it is integrated the way any change is, through the pull request and the merge queue where those are enabled. No developer rewrites it.
5. **Returned to its writer when the document fails.** The findings go back to the conversation that wrote the document when its run's checks fail, when its reviewer refuses it, or when its change conflicts with the target branch while it is being landed onto a target branch that has moved since the run began; a conflict names the conflicting paths. A document that would change a file a document run may not write is refused and returned the same way. Revising it there opens a fresh run. After three returned runs for one document in one conversation, automatic publication of it stops and the writer revises its plan. A run stopped by something outside the work, such as the machine being busy, is not a return: the same text is tried again, up to three runs in all, and if the last of those stops the same way it is held for the development manager to decide.
6. **Held for the development manager when the target branch's copy changed before the run began.** When a run's checkout is cut from the target branch, the harness compares the document there with the copy its text was written against. If they differ, the run stops before anything is committed, the harness does not start it again on its own, and it is held for the development manager; its record names the document, the commit the text was written against, and the commit the target branch has now. She either retries it or retires it. A retry starts a fresh run of the same text and closes the earlier run's pull request; it is refused before any run starts when the target branch already carries a later confirmed revision of the same document, and the refusal names that revision and when it was confirmed. Retiring closes the run's pull request and lands nothing. Whatever the writer still has to say is a fresh revision written from the target branch's copy. Only this check holds a run for the development manager: a conflict found later, while the change is being landed, is a return under point 5.
7. **Bounded while waiting.** Each role has a bounded number of documents waiting to land, set by the harness; a write past that bound is refused with the reason, and nothing is recorded.

This route leaves no uncommitted document in the primary checkout.

One exception remains. A project that keeps its product documents in a repository the harness does not integrate into, or that has no reviewer configured, cannot land a document through a reviewed run. There the write stops at the operator's working tree, the approval is recorded in the document's frontmatter against the revision it produced, and publication is the operator's own commit under their own identity. Until they commit, runs refuse to start over the uncommitted document, because the artifact homes are what runs read as context and a run based on intent that has not landed is worse than a run that waits. The surface that performed the write says so when it writes. In such a project today, a document the Lead Product Manager marked consistent is still put to the operator for approval. That is a known gap against point 3, not the design: point 3 applies there too, and only the publishing step is the operator's.

## What the harness enforces

Enforced in code, whatever a configuration or a role's instructions say:

- the kind list above, and the owner of each kind; a write of a kind the writer does not own is refused;
- the word and link checks, before anything is recorded;
- for the architect's documents, confirmation by the kind's approval policy, recorded under the harness with the policy that confirmed it;
- for every document the Lead Product Manager owns, created or revised, the three cases in point 3: confirmed without the operator only when she marked it consistent with intent with its directing item opening the reason, that approval recorded as hers naming her and the item and refused on any other revision; and held for the operator, whatever the kind's approval setting, when it is marked fundamental or not marked;
- that a landing run changes only the one document;
- that a failing check, a refusing reviewer, a conflict found while landing, and a file a document run may not write each return the document to its writer, a conflict naming its paths;
- the bound on documents waiting to land, and the stop after three returned runs;
- that a run stopped by something outside the work is tried again with the same text at most three times in all before it is held for the development manager;
- that a run whose document's copy on the target branch changed before its checkout was cut stops before anything is committed and is not started again by the harness, and that a retry of it is refused when the target branch already carries a later confirmed revision of the same document;
- that runs refuse to start over an uncommitted document in an artifact home.

Not yet enforced: the two exceptions under *Marked consistent with intent* in point 3, a new goals document or brief and a revision of either that adds or removes a goal, and the reviewer's check of a consistent mark on those kinds. The work making those changes wait for the operator (yoyodyne-ifd.433.51) builds them; until it lands on main, a consistent mark on them is still confirmed without the operator, and this list moves them in when it does.

Left to the roles: what a document says, and whether a revision is consistent with intent, which the Lead Product Manager decides and records.