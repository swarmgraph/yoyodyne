---
id: slack-reporting-design
kind: design
title: 'Slack reporting: events out, directives in, one thread per topic'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-19T14:55:09Z
      reason: commissioned as yoyodyne-ifd.68.1, the design child of the Slack reporting epic; decided in conversation with the operator on 2026-08-19 and transcribed under the architect's role verbatim from conversation chat-11558d32
    - action: amended
      by: architect
      at: 2026-08-19T19:08:06Z
      reason: approved amendment-c255fbec from the developer on yoyodyne-ifd.68.2 - the design was explicit about topics and silent about speakers; the attribution rule is recorded ahead of the table so new event kinds are attributed by it, and run-started follows the recorded selector rather than being fixed to the development manager
    - action: amended
      by: architect
      at: 2026-08-19T19:08:06Z
      reason: approved amendment-7b47ac9b from the developer on yoyodyne-ifd.68.3's first run - the design specified catch-up from cursors and not the no-cursor case, and the two readings diverge sharply; the non-replaying reading is now the design's own, with the stream-beginning refinement so the cutoff is when reporting was enabled rather than when each message is posted
    - action: amended
      by: architect
      at: 2026-09-01T19:15:00Z
      reason: approved amendments fb92f7a7, 3b9126c6, and e3d3c8e9 from yoyodyne-ifd.68.25 - the prose ruling answers which prose posts, its attribution, every role's voice, and @role answering, with decision 1 unamended; and decision 2's token claim now names both mechanisms, enforced by construction for agents and stated honestly for the harness process, with setup moved off shell export
    - action: amended
      by: architect
      at: 2026-09-03T16:45:00Z
      reason: yoyodyne-ifd.68.25.1 - the operator's direction that agents appear to speak in threads with their own identity, formalized over the fb92f7a7 ruling; the durable-record source rule and the reporter's no-provider rule are restated unchanged as what makes the proxy safe
    - action: amended
      by: architect
      at: 2026-09-03T16:45:00Z
      reason: approved amendments 2a89bb4b and fb58a54c (3cb3ca45 declined as duplicate) - the direct-message tier's admitting rule is two named classes, degraded and advisory-once, with the shipped states listed and the bundle-improvement notice admitted as the first advisory-once member with durable dedup; rules yoyodyne-ifd.241.1 and 241.3
    - action: amended
      by: architect
      at: 2026-09-07T00:30:00Z
      reason: yoyodyne-ifd.313 - the operator-approved communication rule recorded as binding on every communicator, final text from the item with the operator's three amendments applied verbatim; governs the recurring-loop reports from their first posts, with yoyodyne-ifd.314's reach rule implementing placement beneath it
    - action: amended
      by: architect
      at: 2026-09-25T04:00:00Z
      reason: approved amendment 8eb60865 from yoyodyne-ifd.441 - the degraded class stops enumerating shipped states, which had gone stale at two while five shipped; the admitting rule is what the design owns
    - action: amended
      by: architect
      at: 2026-09-25T04:00:00Z
      reason: approved amendment ee80baf9 from yoyodyne-ifd.363 - the 2026-09-07 entry's reason reworded from a gendered pronoun to 'the operator's', no other word of it changed and nothing in the prose changed; shipped documents say nothing about the operator's pronouns
    - action: amended
      by: architect
      at: 2026-10-14T11:55:10.336664Z
      reason: 'Part 7 of renaming the product manager role to the Lead Product Manager where a person reads it: the inbound vocabulary''s default receiver of a plain thread reply now names the Lead Product Manager. Nothing else in the design changes.'
approvals:
    - policy: approvals.designs
      revision: 9
      by: harness
      at: 2026-10-14T11:55:10.336664Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 929, for document-929.1 under the automatic approval policy
---
# Slack reporting: events out, directives in, one thread per topic

## Status

This design serves epic yoyodyne-ifd.68 and is the deliverable of yoyodyne-ifd.68.1. Its implementation children are yoyodyne-ifd.68.2 (notifier and voice), yoyodyne-ifd.68.3 (sink, manifest, setup), and yoyodyne-ifd.68.4 (two-way steering, deferred in implementation and first-class here). Each child names below the sections it builds.

## What this is for

Work the harness runs on its own is acceptable only while it is visible, and today visibility means a terminal an operator is sitting at. This design puts the same account of the work into a Slack workspace: one message per reportable event, in the reporting persona's own voice, one thread per topic, and — when yoyodyne-ifd.68.4 lands — a thread reply reaching the persona as a durable directive. It serves the autonomy goal (visibility is what makes unattended work tolerable, and thread replies are the override clause reaching a new channel), the legibility goal (one thread per topic is boundaries between topics; one voice per speaker is boundaries between speakers), and, for the inbound half, the directives goal verbatim: durable, discoverable, and enforceable regardless of which agent received them.

## Design decisions

**1. The source is the durable record, never the running process.** Runs, conversations, and branch reviews already persist normalized event streams that `yoyo-status` follows, and reportable milestones are already read from durable run state by the conversation's activity lines. The notifier reads exactly those records. Rejected: in-process hooks calling Slack from orchestration code. The recorded event is the account a person reading afterwards would get, so posting from it means Slack says what the record says; and a reporting channel wired into the run's critical path is one whose outage can touch a run, which the next decision forbids.

**2. The sink is one separate, long-running process.** A single operator-started process (verb to be settled at implementation; I propose `yoyo slack`) holds the Socket Mode connection, tails the durable streams from per-stream cursors, owns the thread map, and posts. Rejected: posting from each run process. Runs are ephemeral and concurrent; N processes posting means N racing writers to one thread map and a token in every run's environment. The credential boundary is two mechanisms, and each holds a different half. The reporter is a separate process, and every agent invocation receives a named, allowlist-constructed environment with the token pair removed — so no agent process ever holds a Slack token, by construction. The harness's own processes are only as clean as the shell that launched them: an operator who exports the pair into that shell gives it to the harness, whose agent children still never see it. Setup therefore teaches the per-project secret store rather than shell export, and the residue is a trusted process holding a secret, not an agent holding one.

**3. Reporting is observation, never a gate.** Slack being down, slow, or misconfigured changes nothing about any run: no wait, no failure, no park. The sink catches up from its cursors when it returns, so messages arrive late rather than never. At-least-once delivery is accepted: a crash between a post and its cursor advance may duplicate a message, and a rare duplicate is chosen over a lost one because the durable record, not Slack, is authoritative.

A sink starting with no cursor for a stream initializes that cursor at now and reports forward: history is never replayed into Slack, because a product with hundreds of recorded runs would otherwise open hundreds of threads the moment reporting is switched on. The cutoff is when reporting was switched on, not when a message happens to be posted — a stream that begins after the sink is running is reported from its own beginning, so a run started under a live sink is reported whole even if the sink falls behind, and only the pre-Slack past stays where it already is, in the durable records `yoyo-status` reads.

**4. Socket Mode, confirmed.** The harness runs on operators' machines behind NAT; the Events API would demand a public HTTPS endpoint per operator, which fails the near-one-click setup requirement outright. And Socket Mode is bidirectional on one connection, which is what makes 68.4 a feature flag rather than a transport redesign: the websocket that posts is the websocket replies arrive on. Rejected: incoming webhooks — outbound-only, so two-way would mean a second transport later, which is precisely the rework this design exists to prevent.

**5. Persona voice is deterministic; agent-authored text is verbatim.** The channel carries agent speech, and each persona appears to speak with its own display identity and avatar, the reporter posting as its proxy. Two rules make that safe and are unchanged in substance: **no prose reaches Slack that is not first held in a durable record** — the verbatim kinds, and recorded conversation turns routed through the durable interaction service — and **the reporter never invokes a provider**, because the process holding the tokens must never be the process running an agent. Harness facts remain deterministically rendered; a persona's voice is its recorded words, never a generation made for the channel. Two kinds of outbound message, and the split is load-bearing:

- **Harness facts** — a run starting, checks passing, a verdict, a promotion — are rendered from the event by per-role voice templates shipped alongside the personas (68.2's deliverable), with each persona given its own Slack display name and avatar on the message. No provider invocation per event. Rejected: voicing events through a model. It prices visibility at a model call per event, makes the channel nondeterministic, and puts a model between a fact and its reporting — a model paraphrasing "checks failed" is a model that can misstate it.
- **Agent-authored text** — a report, a proposal's summary, an ask-exchange turn — is posted as the agent wrote it, attributed to its persona, already redacted (decision 7). This is where the genuine voice lives, at no added cost and no added risk: it is text the agent already said, in a channel that already carries agent text to operators.

**What prose the channel carries.** Decision 1 stands: the source is the durable record, and the reporter additionally never invokes a provider, because the process that holds the tokens must never be the process that runs an agent. What widens is what gets durably recorded. Prose posts only when a durable record holds it: the verbatim kinds above, and a role's recorded conversation turns when routed through the durable interaction service. It is attributed to the persona whose record it is, with the display identity every speaker already has. Every role gets a voice through exactly this mechanism — its durable conversation and records; no record, no voice. An @role question in a thread is answered today by the harness from the read model, per the stated exception; once the conversational client lands, gated on the durable service, it routes as a turn into that role's durable conversation through the service, and the recorded reply posts in-thread.

**What may arrive as a direct message.** A direct message is admitted only by naming its class, and a state fitting neither class does not DM — that is the gate a developer must not widen silently, stated here rather than in `heartbeat.go`:

- **Degraded** — the system is stopped, stale, or choosing nothing over ready work: something only a human fixes. Which states ship in this class is the operator guide's to list, because the list moves with every state admitted; what does not move is the admitting rule, and a state that fits neither class does not DM.
- **Advisory-once** — a fact addressed to a person that speaks exactly once per fact, deduplicated durably, never repeated, never urgent in presentation. The bundle-improvement notice is its first member: one DM per newly-available improvement, the dedup key recorded durably so restarts cannot re-announce.

**6. One vocabulary, two directions.** Outbound events and inbound directives are two halves of one contract, correlated by topic (section: *Addressing*). The inbound half creates **no new directive machinery**: a thread reply is a new *receiver* for the existing directive record — same kinds, same pause semantics, same resolution — so nothing about the channel can weaken directive governance, because the channel does not have its own governance to weaken.

**7. Nothing unredacted can reach Slack, by construction.** Events are redacted before persistence (the design's existing security rule), and the sink reads only persisted records. A message body over Slack's size limit is truncated with a marker naming the durable record that holds the whole; nothing is ever split into a flood of messages to fit.

## The outbound vocabulary

Every outbound message is one envelope:

| Field | Meaning |
| --- | --- |
| `kind` | What happened — the reportable event types below. |
| `topic` | The thread key: `work-item:<beads-id>`, `exchange:<id>`, or `product` (section: *Addressing*). |
| `speaker` | The role and configured agent whose account this is, or `harness` for what no persona did. |
| `severity` | `critical`, `warning`, `note` — the reports vocabulary, and rendered so critical cannot be mistaken for a note; the words carry the meaning, decoration only adds to it. |
| `body` | The rendered or verbatim text. |
| `refs` | Correlation ids: run, conversation or exchange, work item, directive, pull request. |

### Who speaks

Who speaks is decided here rather than per implementation child, because one voice per speaker is the epic's legibility goal, and an attribution decided three times is three chances to voice one event differently.

The rule comes first and the table applies it, so a new event kind is attributed by the rule rather than by extending the table from imagination: **a persona speaks for its own judgments and its own authored text; the harness speaks for what it performs.**

| Event | Speaker |
| --- | --- |
| run claimed and started | the recorded selector: the development manager when it chose the item, the harness when the operator named it |
| checks passed or failed | harness |
| the reviewer's verdict | reviewer |
| the developer's attempt summary | developer, verbatim |
| promotion; publication; queued merge; merge | harness |
| a run parking or continuing (usage limit, overload, operator pause, unresolved directive) | harness |
| blocker recorded | harness |
| report filed | the role that filed it, verbatim |
| proposal raised | the role that raised it, verbatim |
| ask-exchange turn | the role whose turn it is, verbatim |
| an exchange closing, including unresolved at its round cap | harness |

Run-started deserves its line of reasoning, because it is the one place the speaker varies. The event carries the recorded selection reason, so the speaker follows the recorded selector: the development manager when it chose, and the harness when the operator named the item — the operator is not a persona, and a persona must not narrate selections it never made.

The initial reportable set is deliberately the milestones the conversation's activity line already reports, plus what has no terminal surface at night: run claimed and started — **carrying the recorded selection reason**, so the fact the `selected-work-passes-intake-and-records-why` invariant makes durable is also the fact an operator sees; checks passed or failed; the reviewer's verdict; promotion; publication, queued merge, and merge; a run parking (usage limit, overload, operator pause, unresolved directive) and continuing; a blocker recorded; a report filed, at its severity; a proposal raised; an ask-exchange turn, and an exchange closing — including closing unresolved at its round cap, which escalates to the operator and is exactly the kind of message this channel exists to carry. Each transition is said once; a thread is a narrative, not an event log scrolling sideways.

The set is versioned in the envelope and extensible: a new producer adds kinds without the sink, the threading, or the inbound half changing. The **notifier interface (68.2) takes `(topic, speaker, event)` and assumes nothing about the producer** — runs are the first producer, conversations and branch reviews are the same shape, and ifd.99's ask exchanges are a recorded second consumer arriving later with `exchange:` topics and per-exchange cost beside its rounds.

## Addressing: one thread per topic

The primary topic is the work item: the first event for `work-item:<id>` opens its thread in the configured channel with a header message naming the item, and every later event for that topic is a reply in it. An ask exchange that concerns a work item posts into that item's thread, because the epic's rule is one thread per *topic* and the item is the topic; an exchange with no item gets its own thread. Product-level events with no topic — pause placed and lifted, intake held and released — post to the channel top level, unthreaded, because they are about the whole line and burying them in any one item's thread would misfile them.

The topic-to-`thread_ts` map is durable at the state root, per product, owned by the sink alone, so a restart posts into the same threads. **Known limit, stated rather than solved:** the map is per machine, so two collaborators' harnesses against one shared repository would open two threads per item. That is team-mode coordination and belongs to yoyodyne-ifd.82.2; this design keeps to one sink per product per workspace and says so in its setup documentation.

## The inbound vocabulary (built by 68.4; designed now)

A message in a topic's thread maps onto the existing directive surface and nothing else:

- A plain reply records an **operational directive**, scoped to the thread's work item, `received-by` the persona it @-mentions or the Lead Product Manager by default.
- The pausing kinds are **stated, never inferred**, exactly as the CLI requires: a reply opening `ambiguous:` or `artifact: <name>` records that kind, and must state what is unresolved or be refused — the same rule, because a pause nobody can name a reason for is a pause nobody can lift, and a classifier guessing which sentences pause work is the failure the CLI already refused.
- `resolve <directive-id> <how>` resolves, with the existing unique-prefix rule.

Every inbound message gets an in-thread acknowledgment that is itself an outbound event: the directive as recorded with its id, or the refusal with its reason. A directive recorded from a thread is indistinguishable, downstream, from one recorded by `yoyo directive` — same record, same pause gate, same discoverability — which is what "enforceable regardless of which agent received it" costs and all it costs.

**Authorization is allow-listed and defaults closed.** Inbound messages act only when their Slack user id is in the configured operator list; the list lives in `.yoyodyne` because user ids are identity, not secrets. An unlisted user's reply is acknowledged as not acted on — visibly, because a channel that silently ignores some people looks broken rather than closed. The default list is empty, so shipping 68.4 changes nothing for any workspace until an operator names themselves. Rejected for now: operator *commands* (`/stop`, `/hold`) from Slack — directives move intent and commands move the machine, and widening the machine's control surface to a chat workspace is a separate decision; the inbound transport already exists, so adding it later needs no redesign, which is the standard this design is held to.

## Configuration and credentials

- `SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN` in the sink's environment only. Never in `.yoyodyne`, never in Beads, never in any prompt or context bundle — and structurally never in a run process (decision 2).
- In `.yoyodyne`: the Slack section — enabled flag, channel, the operator allow-list — validated like everything else, with a project that enables Slack but lacks a channel refused at load, before anything runs.
- The checked-in app manifest (68.3) declares the least scopes the design needs: posting, per-message display-identity override, Socket Mode connectivity, and reading thread replies in the configured channel. Setup is: create the app from the manifest, install, export two tokens, start the sink. The setup document is held to the readme's newcomer standard.

## What each child builds

- **68.2** — the envelope, the notifier interface `(topic, speaker, event)`, the reportable-event selection from the durable streams, the voice templates and display identities, severity rendering. Sections: *decisions 1, 5*, *outbound vocabulary*.
- **68.3** — the sink process, Socket Mode connection, cursors and catch-up, the thread map, the manifest and setup docs. Sections: *decisions 2, 3, 4, 7*, *addressing*, *configuration*.
- **68.4** — the inbound mapping, authorization, acknowledgments, wired to the existing directive record. Section: *inbound vocabulary*. Its precondition is the epic's, not a technical one: one-way has proven itself in production threads.

## Invariants and existing constraints

`one-promotion-per-target-branch` is untouched: this design performs no Git operation and no promotion. `selected-work-passes-intake-and-records-why` binds nothing here — this design schedules nothing and claims nothing — but the run-started message deliberately surfaces the selection reason that invariant makes durable. No agent acquires a credential or a new capability anywhere in this design; every post and every directive recording is the harness's own act.

## The communication rule

Every message the harness or an agent sends to a person follows four rules, whether the text comes from a template or a model:

1. **Write for an executive.** Brief, to the point, focused on what requires action, a decision, or represents a serious and unsolvable problem. One plain sentence of what; one of why it matters.
2. **Post on importance or on an ask; otherwise do not post.** Severity is importance, not actionability: important breakage posts even without an ask. Brief periodic status is fine — "*{item title}* has merged" is ok; the prohibition is walls and useless text, not short factual posts. Routine narration goes to the durable record, with an item's milestones in its own thread.
3. **Never bare id lists.** Aggregate with meaning — "31 items wait on triage decisions; the architect holds 8 rulings; next mover: the architect" — naming an item title-first only when the reader must act on it.
4. **Every message answers "why is the reader reading this" or is not sent** — with a generous reading of why: "so the operator knows a feature merged and can test it" is a legitimate answer. The test kills pointless text, not useful awareness.
