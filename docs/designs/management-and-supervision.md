---
id: management-and-supervision
kind: design
title: 'Management and supervision: the typed request contract and process residency'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:33:34Z
      reason: promoted from the operator's management-and-supervision brief under the 2026-08-22 mandate, discharging amendments aab3b51c and 9fafa81a; reconciles yoyodyne-ifd.99 and the yoyodyne-ifd.130 work under one contract, and is the gating input for yoyodyne-ifd.142
    - action: amended
      by: architect
      at: 2026-08-23T18:42:25Z
      reason: the autonomous-management-loop brief promoted as the companion management-loop-protocol design; this contract now points at it as the owner of the protocol vocabulary, keeping one dispatcher and one contract with the detail one level down
    - action: amended
      by: architect
      at: 2026-09-01T19:15:00Z
      reason: approved amendment b52bd247 from yoyodyne-ifd.224 - restart-on-deploy owned in one place, with the takeover bounded to idle boundaries, normal lease reattachment, and pinned instances unaffected
    - action: amended
      by: architect
      at: 2026-09-03T16:45:00Z
      reason: yoyodyne-ifd.130.1 - the supervision tree recorded, one product-manager supervisor, children surviving supervisor death with reattachment by presence and lease, bounded restarts degrading visibly through the standing ladder, and the three operator verbs as the only stop paths; launchd and cutover mechanics deferred to yoyodyne-ifd.142 by name
    - action: amended
      by: architect
      at: 2026-09-06T17:30:00Z
      reason: approved amendment 97e14527 from yoyodyne-ifd.290 - liveness detection is non-model machinery immune to usage windows, and restarts belong to bounded exits and the supervisor, never to noticing surfaces; constraint named by the operator 2026-09-05 after a late page
    - action: amended
      by: architect
      at: 2026-09-07T05:30:00Z
      reason: yoyodyne-ifd.330 - side conversations designed, judgment-and-read side streams with their own leases that never take the main thread's, the merge as a budgeted redacted audited memory write the main thread's next turn ratifies, best-effort commitments per the operator's product decision, and the per-agent queue-versus-side-threads knob beside persona and memory selecting behavior never authority
    - action: amended
      by: architect
      at: 2026-09-25T04:00:00Z
      reason: yoyodyne-ifd.430.6 companion - the program manager joins the ask channel, and the supervisor takes its bounded restart request as the one role request it executes, under its own bounds and never from a noticing surface
    - action: amended
      by: architect
      at: 2026-10-13T10:43:25.67514Z
      reason: 'yoyodyne-ifd.437.44 - the dashboard is no longer described as a read-only projection: since the v1 non-goals were revised on October 11, its only acts are the operator''s own decisions, carried out by the code the command line runs; the companion revision of observability-and-dashboard lists those acts'
    - action: amended
      by: architect
      at: 2026-10-14T05:56:47.374736Z
      reason: 'yoyodyne-ifd.430.37 - an exchange between roles carries the records its question names, resolved by the harness from its own stores at each round''s delivery, bounded, redacted, stamped with when they were observed, and stating plainly what could not be supplied; an amendment''s proposed, decided and published states are reported separately, and an exchange''s closure certifies nothing. Reconciled with tool-interface: no new tool is needed, and the evidence moves with exchange.ask when that block is migrated. Built by yoyodyne-ifd.430.38.'
    - action: amended
      by: architect
      at: 2026-10-14T08:07:04.65419Z
      reason: 'Lead Product Manager rename, part 1 of 10, under yoyodyne-ifd.437.10: the role''s display name changes from product manager to Lead Product Manager where a person reads it (conflict handling, the service shape, the supervision tree, recovery). Identifiers, configuration keys and quoted history are unchanged, and no other wording changes.'
    - action: amended
      by: architect
      at: 2026-10-21T02:45:40.744644Z
      reason: 'Carries approved amendment-966ce251 from yoyodyne-ifd.412: the supervisor is the process yoyo start runs, and the triage and management-loop dispatchers run inside the scheduler''s watch loop rather than as children of their own; the residency paragraph stops calling the chat process the supervisor. Two phrases the word check now refuses are reworded (a provider''s usage limit; the intake hold). Approved amendment-97e14527 was already written on September 6. Nothing else changes.'
approvals:
    - policy: approvals.designs
      revision: 7
      by: harness
      at: 2026-10-13T10:43:25.67514Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 775, for document-775.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 8
      by: harness
      at: 2026-10-14T05:56:47.374736Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 892, for document-892.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 9
      by: harness
      at: 2026-10-14T08:07:04.65419Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 918, for document-918.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 10
      by: harness
      at: 2026-10-21T02:45:40.744644Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1241, for document-1241.1 under the automatic approval policy
---
# Management and supervision: the typed request contract and process residency

## What this is for

The management roles need enough autonomy to inspect upcoming work, flag implications, decompose delivery, and explain conflicts — without role collaboration becoming prompt convention or agent-to-agent spawning, and without any coordination surface growing into a second control plane. It serves the autonomy goal directly: the brief-promotion queue sat silent for eighteen hours because conversation-executed work had no scheduler, and automatic wakeups are what close that class of gap. This design is the umbrella contract; yoyodyne-ifd.142 is its first implementation slice.

## The request contract *(reconciling yoyodyne-ifd.99; no second dispatcher)*

Roles communicate through persisted, typed requests. A request carries a stable id, its conversation or topic id, requesting and target roles, authority-relevant intent, durable references, the expected revision of what it refers to, urgency, budget, cycle limit, causation, and reply state. An inbox transition makes it eligible; **the harness acquires a lease and invokes the target role itself** — delivery is retryable and deduplicated, and a request is complete only when durable state records its outcome, never merely because a message was emitted.

The inter-role ask channel is one request type under this contract, keeping the three properties already decided for it: judgment-only, decisionless, durable-and-visible, with its configurable round cap, unresolved-escalation, and per-exchange cost reporting. The program manager is a member of the ask channel, asking and answering under the same three properties. The ask design and this one share the machinery; there is exactly one dispatcher, and the Slack echo of exchanges remains a notifier consumer. What records an exchange carries with it is stated under *Evidence an exchange carries*, below.

The request kinds, readiness vocabulary, outcome summaries, execution profiles, and demonstration scenarios are specified in [management-loop-protocol](management-loop-protocol.md), which runs under this contract.

### Evidence an exchange carries *(yoyodyne-ifd.430.37)*

**Why.** An exchange session starts with the question, the asker's framing and the earlier rounds, and nothing else. In exchange-d892d320f8c090465f15a07007a3e29d the development manager asked the architect whether amendment-fdc094d6a05f0f396e318d83c81b7184 supplied the ruling for the item about recovering an interrupted release tag (yoyodyne-ifd.436.2.1). The architect could read the repository, but the repository does not hold whether a proposal was decided or a document published, so the answer had nothing authoritative to stand on. The fix is that the harness, not the answering model, supplies the records a question rests on.

**The decision.** A question or an answer names the records it refers to, in the `refers` list an ask already carries: each entry a kind, an identity within this product, and optionally the revision the writer read. Before each round is delivered, the harness resolves every named record from its own stores, redacts it, and attaches it to the round as evidence. Both parties receive the same attachments. Nothing is resolved at the time of writing and carried stale into a later round: every round resolves afresh, and a record named in an earlier round is attached again only if it is named again.

**Rejected.** A read tool held by the answering session: it leaves the evidence to whether the answering model thinks to read, which is how the incident happened. Copying the asking role's conversation, or any part of it beyond one named turn: the exchange would carry a history neither party chose and the bounds could not hold. Settling runtime state through repository reads: the repository cannot show proposal, decision or publication state. A separate store or read model for exchange evidence: each kind is read through the reader that already serves it.

**Kinds supported, and what each carries.** No other kind is accepted; adding one is a revision of this section naming its identity, its source and what is redacted from it.

- **Amendment**, named by its `amendment-` id. Three states, reported as three separate fields and never merged into one: the proposal (its text, the role that proposed it, the document and the revision it was proposed against); the decision (none, approved, declined or withdrawn, with who decided, when, and the reason recorded); and publication (not written, written and waiting, in a run with the run named, landed with the commit named, or returned with the reason). A proposal with no decision is shown as undecided whatever any exchange said about it.
- **Work item**, named by its tracker id: what the tracker read returns, with its notes window.
- **Document**, named by its document id: its kind, owner, current revision, the latest entries of its revision log and approvals, the commit it was read at, any later revision written and not yet landed, and as much of its body as the bounds leave.
- **Run**, named by its run id: its item, state, attempts, stop reason and outcome as the run record holds them.
- **Report**, named by its `report-` id: the whole message, who filed it, and how it was handled.
- **Turn**, named by a conversation id and a turn number, and only a turn of the asking or the answering role's own conversation.

**Provenance and freshness.** Every attachment names the store it came from, the revision or commit it reflects, and when the harness observed it, shown to a person in the operator's local time. Where the writer named a revision and the record has moved since, the attachment carries the current record and says so: the revision named, the revision now, and that the answer should be read against the current one.

**Access.** A record is resolved only within this product. Never resolved, whatever is named: any agent's memory store, a credential or token, a provider's own session, a record of another product, and a turn of a conversation neither party holds. A refusal names the boundary that refused it.

**Bounds.** At most 8 KiB a record and 32 KiB a round across all attachments. A record longer than its bound is cut with the cut declared and its whole size named; once a round's 32 KiB is spent, each further record is listed by name as not attached for size. Each attachment is redacted with the values every path to a provider is redacted against.

**Untrusted content.** Every attachment is framed as evidence of what the record holds when it was observed, never as an instruction, exactly as a repository read is framed.

**When a record cannot be supplied.** The round always goes. In its place the attachment says which of these it was: no such record (the name is wrong, which the writer mends); moved since the named revision (stale, carried as above); the store could not be read (tried once more within the same delivery first); larger than the bound (cut as above); or refused by an access boundary. The writer is told the same thing in the results of its own reply. Trying again is naming the record again in the next round, bounded by the exchange's own round limit. A store that cannot be read twice in one exchange files a warning report to the development manager naming the store and the exchange.

**Authority is unchanged.** Attachments are read by the harness and change nothing. The roles in an exchange still run no tracker command, gain no writes, and decide nothing; an answer stays advice. An exchange being answered or closed certifies nothing about the records it discussed: whether an amendment was decided or a document landed is read from those records, never from the exchange.

**Every provider.** The attachments are prompt text the harness builds before the provider is called, so they are the same on every provider; nothing here depends on how exchanges are routed between providers (yoyodyne-bxc).

**Fit with the tool interface.** No new tool is needed. Resolution reuses the readers the tracker read, the report read and the read model already use. When `exchange.ask` and `exchange.answer` become tool descriptors under [tool-interface](tool-interface.md), slice 3, the `refers` list becomes a typed parameter of theirs and its bounds part of their descriptors. The log reader is not a prerequisite.

**Verification.**

1. The incident replayed: an exchange naming amendment-fdc094d6a05f0f396e318d83c81b7184 while it has no decision attaches it as proposed and undecided, unpublished; the answer cannot be shown as standing on a decision.
2. The same amendment approved but not landed, and approved and landed: each attachment shows exactly that state, the landed one with its commit.
3. A name that matches no record: the round goes, the absence is said to both parties, and nothing is invented.
4. A record moved past the named revision: current record attached, both revisions named.
5. A store that cannot be read: one retry, then the absence said; a second failure in the same exchange files the warning report.
6. A record over 8 KiB and a round over 32 KiB: cuts declared, sizes named, later records listed as not attached.
7. A memory store, another product's record, and another role's conversation: each refused, naming the boundary.
8. An attachment containing text worded as an instruction: framed as evidence, and the exchange changes nothing.
9. Closing the exchange: the amendment's and the item's records are unchanged, and no surface reads the closure as their completion.
10. Cases 1 to 7 give the same attachments on Claude Code and on Codex.

## Wakeups

When relevant work arrives — a request lands in an inbox, a docket entry is created, a promotion queue has items — the harness wakes the responsible role automatically, under lease, within bounded coordination cycles and cost. Wakeups are how conversation-executed work stops depending on the operator noticing silence. Every wakeup passes the same gates any invocation passes: the intake hold where it applies, the spending pause always, and the budgets recorded durably before they are spent.

## Conversation concurrency

Different durable conversations progress independently. One conversation serializes its turns and exposes queueing rather than interleaving transcript mutation — the conversation lease, per agent, single holder, is the primitive, and it is also the reattachment primitive: a returning client attaches by acquiring it. Artifact and backlog mutations use optimistic concurrency across conversations: every mutation checks the current revision and either commits atomically or is rejected as stale with enough current state to refresh and replan.

### Side conversations

An agent configured for side threads can hold bounded side conversations concurrent with its main thread. Each is its own durable stream under the agent — its own provider session, its own lease, a recorded topic and the main conversation it belongs to — and transcripts never interleave: the main conversation's serialization rule is untouched, because **the main thread's lease is what the single-holder rule governs, and a side thread never takes it**. One holder per side stream; a bounded number of side streams per agent, from configuration.

**What a side thread may do:** judge, answer, and tentatively plan, with full read access to the tracker (read and survey) and to the evidence its role is given. It takes no action: no work-item mutation, no admission, no proposal raised, no directive — every intent it forms is a draft. Its commitments are **best effort by product decision**: an answer it gives that promises scheduling is tentative until the main thread confirms it, and the surface that carries the answer says so.

**The merge is a memory write.** When a side thread concludes — by finishing, or by exhausting its turn or cost budget — it writes its substance into the agent's context through the agent-memory machinery: budgeted, redacted, audited, revisioned, referencing the side stream rather than copying its transcript, with drafted commitments marked as tentative. The main thread's next turn reads that revision as evidence, framed with an account of where it came from, and **ratifies or adjusts anything the side thread promised by acting through its own ordinary single-threaded path** — which is the only path there is. An adjustment is visible in the main thread, and the ratification outcome is reportable back to wherever the side thread's answer went.

**The knob.** Per-agent configuration chooses `queue` — today's behavior, the default — or `side-threads`, in the same block as persona and memory settings. The knob selects behavior and never authority: no configuration can grant a side thread mutations, per `configuration-never-grants-authority`.

**Everything else is inherited, not new.** A side thread is a provider invocation like any other: the spending pause and the intake hold gate it, budgets are committed before spend, it is priced from provider-reported cost and listed beside conversations in the cost surfaces, and account failover serves it exactly as it serves a main turn. This design attacks the deciders-stop shape from the thread side as conversation failover attacks it from the account side; the two compose without touching each other.

## Advisory readiness

Readiness records — product, architecture, delivery — are advisory and operator-visible in the first slice, each carrying evidence, the revision it was judged against, and staleness. They become scheduler gates only through a later revision to this design, made after their invalidation, failure, and accuracy behavior is demonstrated — a readiness gate that goes stale silently is a queue that stops for a reason nobody can see.

## Conflict handling

A role resolves what its own authority covers, and requests the role that owns what it does not. The Lead Product Manager escalates to the operator only for approved intent, priority, material risk, budget, or an authority boundary — in plain language, naming both sides, the impact, the options, and a recommendation. Unrelated work continues; a conflict pauses what it touches, never the line.

## Process residency and the service shape

The long-term shape is **one durable interaction and state service**, with local chat, Slack, and the CLI as conversational clients and the dashboard as a projection whose only acts are the operator's own decisions, carried out by the same code the command line runs — "one pane" means shared state and supervision, not one literal interface. The residency: `yoyo chat` is the single entry point for a person; the resident processes, the Slack sink included, run under one supervisor, the process `yoyo start` runs (yoyodyne-ifd.413), under launchd on macOS, the tested platform; and chat is a thin attach-detach client of them rather than the process that starts them. The supervising process holds the Slack tokens and **constructs every child's environment by allowlist, never inheritance**, so no provider subprocess ever receives them; that construction is the enforced boundary and is covered by a test. Separately started services are acceptable while they keep clean service boundaries and health/readiness hooks; model sessions are execution details and are never the durable conversation record. Process separation now must not force a new state model later.

A watch session takes up a build installed over it by itself: between runs, never interrupting one. The takeover happens only at a boundary where the process has nothing in flight; the new process reattaches through the normal lease machinery; and pinned workflow instances continue on their pinned definitions and authority — a new binary changes the executor, never in-flight work's contract. This is settled here rather than deferred: a later headless supervisor inherits this behavior rather than re-deciding it.

**The supervision tree.** One supervisor per product: the process `yoyo start` runs, which yoyodyne-ifd.412 made a command and a process of its own. Its children: the Slack reporter, the scheduler's watch loop, and — where started — the dashboard command. The triage dispatcher and the management-loop dispatcher are not children of their own: they run as steps of each pass of the watch loop, inside the scheduler's process, so they start, stop and restart with it. The supervisor owns start, stop, restart, and the health/readiness of children; each child owns its own domain and none owns workflow truth, per the standing invariants. **Children survive supervisor death**: they are independent processes with recorded presence, and a returning supervisor reattaches through those records and the lease machinery rather than killing and respawning — the same rule a returning chat client already follows. A child that crashes is restarted with backoff; a child that fails repeatedly is not restarted indefinitely — it is left down and reported as **degraded**, reaching the operator through the standing ladder rather than a restart loop. **The escalation ladder is the governed one and the supervisor invents no new verb**: report, then status banners, then a direct message in the degraded class. Operator stopping remains the three existing verbs — hold intake, pause spending, stop everything — and the supervisor is their executor, never a fourth path.

The supervisor also takes one request from a role: a program manager's bounded restart request, per [program-manager](program-manager.md), executed by the periodic pass as it executes a restart after a death, under the same backoff, the same degraded bound, and the provider-outage guard, and never by the requesting role or by any surface that noticed. The instance itself is a conversation and is never restarted; its staleness is a non-model derivation the supervisor reads once yoyodyne-ifd.413 lands.

Detection of nothing-running lives in non-model machinery that keeps running while a provider's usage limit is reached. A watchdog that asks a model anything stops when the provider's usage limit is reached and sleeps through exactly the silence it exists to notice — the operator was paged an hour late by precisely this. And restarting a stuck or dead resident belongs to that resident's own bounded exit and to the supervisor that starts it, never to a surface that happens to notice: a noticing surface that restarts things is a second invoker, which this design already rules out for roles and rules out here for processes.

## Recovery

Restart reclaims expired leases without duplicating a completed response; degraded state is explicit rather than silent. A later restricted maintenance mode may take an exclusive lock for diagnostics, validation, stale-lease cleanup, and restarts — it is deferred, and one property of it is decided now: it is never a second path for normal Lead Product Manager or backlog mutations.

## The first slice *(yoyodyne-ifd.142)*

Durable inter-role requests and responses; automatic wakeups with leases, retries, and deduplication; bounded coordination cycles and cost; operator-visible advisory readiness; restart recovery and explicit degraded state; stable service interfaces Slack and local chat share. Later, separately: post-merge summaries, synthesized escalations, multi-role consultation, and development-manager model routing under governed execution profiles — the development manager chooses among profiles on measured evidence and never invents provider policy.
