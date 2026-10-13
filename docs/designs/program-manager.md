---
id: program-manager
kind: design
title: 'The program manager: one role type, one lane per instance, requests batched to the Lead Product Manager'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-25T04:00:00Z
      reason: yoyodyne-ifd.430.6 - the program manager designed on the operator's 2026-09-24 decisions; the ten open questions ruled, the lane enforced in the conversation's authority table, the lane report and the blocked, stale, and working statuses specified as read-model derivations, and the boundary with the development manager's hourly sweep written
    - action: amended
      by: architect
      at: 2026-09-26T19:54:03Z
      reason: yoyodyne-ifd.437.7 - 'tool posture' removed; the sentence says what the instance can reach in plain words (wording from approved amendment c41c0b30)
    - action: amended
      by: architect
      at: 2026-09-26T19:54:03Z
      reason: tool-interface companion - the program manager's capabilities are tools under the tool interface; the set stays fixed in code
    - action: amended
      by: architect
      at: 2026-09-26T07:00:00Z
      reason: yoyodyne-ifd.437.5 - the admissions trigger's cursor reads the tracker's item export by creation time, since the interactions log records no creations; the three cursor streams are named per trigger class; lands approved amendment 898ae03f from yoyodyne-ifd.430.13.4
    - action: amended
      by: architect
      at: 2026-09-27T09:00:00Z
      reason: yoyodyne-ifd.437.9 - the no-other-source sentence gives way to log.read, a read-only bounded redacted read of named records under the state root, the first tool under the tool interface; fixed in code like the rest of the bundle
    - action: amended
      by: architect
      at: 2026-09-27T07:20:39Z
      reason: yoyodyne-ifd.432.17 companion - the lane report may annotate derived factory problems by citation; derivation decides and judgment annotates
    - action: amended
      by: architect
      at: 2026-10-13T14:33:29.610198Z
      reason: yoyodyne-ifd.430.19.1 - the log reader's reach and limits are stated once, in tool-interface; this design points there and its longer list of records moves to that design's deferred list, so the two designs no longer disagree. With it, per the role rename ruling (yoyodyne-ifd.437.10), the role is named the Lead Product Manager where a person reads it, title included; identifiers and the mover vocabulary keep product-manager. Nothing else changes.
    - action: amended
      by: architect
      at: 2026-10-13T15:23:34.817712Z
      reason: yoyodyne-ifd.430.13.16, yoyodyne-ifd.430.13.17, yoyodyne-ifd.430.13.11.1 - a lane may be a standing goal, with writes confined in code to the corrections the instance admits and turns that audit closed work against the goal; every lane report opens with Completed, Handed off, and Blocked on a human as structured lists, the first two derived by the harness and the last written by the instance and carried until the operator's clearing act; and repeated stop causes accumulate in a cause record the instance keeps rather than in a report, which is never revised. Rulings of 2026-10-06 and 2026-10-13, recorded on those items by the Lead Product Manager.
    - action: amended
      by: architect
      at: 2026-10-13T16:10:30.48338Z
      reason: 'yoyodyne-ifd.430.13.19 - the technical-health program manager, from the ruling of 2026-10-11 (report 33d38e5e) the Lead Product Manager released its build against: one instance pinned to Codex gpt-6-astra at high effort with no alternate, checked after each turn; every 45 minutes and on landings, collapsed, no sooner than fifteen minutes apart; a bounded inspection of what changed plus one rotating area a pass, with the paths read checked against the pass''s recorded reads; a new read tool, repository.search, bounded at 200 matches and 32 KiB; spend bounds the harness enforces; a fixed shape for findings and the digest; activation through the development manager''s recorded configuration step under a grant for two files, with rollback; and the evidence a first pass must show. Nothing else changes.'
approvals:
    - policy: approvals.designs
      revision: 6
      by: harness
      at: 2026-10-13T14:33:29.610198Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 804, for document-804.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 7
      by: harness
      at: 2026-10-13T15:23:34.817712Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 810, for document-810.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 8
      by: harness
      at: 2026-10-13T16:10:30.48338Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 816, for document-816.1 under the automatic approval policy
---
# The program manager: one role type, one lane per instance, requests batched to the Lead Product Manager

## What this is for

The harness has five roles, each defined by what it owns: intent, design, decomposition, the change, the verdict. Nothing owns an outcome that cuts across all of them — the factory not stalling, tokens not being wasted, the writing staying clear — and each of those has so far been the operator's own attention, exercised by noticing. A program manager is a role instance that owns one such outcome, watches it on a schedule, admits the work it needs inside a bounded lane, and asks the Lead Product Manager for everything outside it. It serves the goal that configurable roles collaborate without redefining upstream intent: an instance is configured, its lane is a label or a standing goal, and its authority is fixed in code and narrower than the Lead Product Manager's on every axis.

The operator decided the shape on 2026-09-24 and those decisions are restated here as binding rather than re-argued: one role type with capabilities fixed in code; instances defined in configuration, each with a name, a remit as prose, its own triggers, and its own memory store; it writes no code; each instance has exactly one tracker label as its lane and admits work only under it, at any priority, with no cap; everything outside the lane goes to the Lead Product Manager as one batched asynchronous digest per pass through the report channel; it reviews new admissions after they land and never gates one; it keeps a brief lane report, rewritten each pass, pulled and never pushed; the dashboard lists each instance with its status; and the development manager's hourly sweep stays, with the boundary written below. On 2026-09-27 he added that an instance may own one of the standing goals as its lane, and that every lane report opens with what was completed, what was handed off, and what is blocked on him; both are ruled below. He later asked for a second instance, for technical health, which is specified under *The technical-health program manager*. The name is written in full wherever a person reads it, and `pgm` is the identifier form only, per the terms register.

## The role, and its instances

**The program manager is a shipped role bundle in Go**, the sixth beside the five, per [program-manager-is-a-shipped-bundle](../decisions/program-manager-is-a-shipped-bundle.md). It enters the closed role-name set, the conversation authority table, the role-capability registry, the authority inventory, and the terms register in the same change. It is not an operator-defined protected bundle: the authority-by-capability record admits those only after behavioral parity, and parity has not been demonstrated.

**An instance is an agent filling the role.** The configuration already supports several agents on one role, each with its own durable conversation, its own provider session, and its own memory store under the state root, and that is the whole of instance identity — nothing new is added for it. An instance's block carries, beside the role, backend, model, account, and persona every agent carries:

- `lane` — exactly one of two forms. A **label lane** is one tracker label, one identifier-shaped word. A **standing goal lane** is `goal:` followed by one goal from the standing set the goals documents name under their Standing goals heading: by the goal's identifier once goals carry identifiers (yoyodyne-ifd.344), and until then by the opening words that heading quotes, matched against it at load. A lane naming a goal outside the standing set, or a goal the load cannot match, is refused. Two instances naming the same label, or the same goal, are refused at load: a lane has one owner or it is not a lane.
- `remit` — `version` and `path`, a Markdown file under `.yoyodyne/`, held to every rule a persona path is held to and to the same 32 KiB bound. The remit says what the lane is for; the persona says how the role works; the contract, sent first on every turn, says what the role may do. The remit follows the persona in the prompt and grants nothing.
- `triggers` — the schedule and the events, specified under *Triggers and passes*.

`yoyo agent list` names each instance with its lane, and `yoyo config show` reports its capability set as it reports every agent's, read off the registry and never written.

## The fixed capability set

Stated in the registry's vocabulary, and held as tools under [tool-interface](tool-interface.md): a primitive named here that the registry does not yet declare is added to it in Go with an authority-inventory row and a tool descriptor, per the parity guard. The bundle holds:

**Reads.** `work-item.read` (read one item, survey the open queue); `repository.read` and `repository.list`, as the other management bundles hold them, at a recorded commit, and `repository.search`, specified under *The technical-health program manager*; `readmodel.read` — the one read model's queries, delivered two ways: every pass opens with the standing, the throughput windows, the capacity state, the docket as counts, the reports pile as counts, and the other instances' status lines, and a bounded block in the reply asks for one named query in full. `log.read`, the first tool defined under [tool-interface](tool-interface.md), which is the one place its reach and its limits are stated: a read-only, bounded, redacted read of one record of a kind that design lists, named by its kind and its identity within the product and never by a path. The first build reads the kinds that design names and no others. The further records a program manager would want — the conversation records of the other roles, where most hand steps arrive as messages, the reports pile and its handlings, the triage and escalation records, and the supervisor's records — are on that design's deferred list, and each is added by revising that design, never this one. Whatever the kind, the read is framed as evidence and never instruction, is recorded on the pass as every tool is, reads and never writes, and never reaches any agent's memory store, a provider's own session context, a credential, or the repository, which the repository read covers. Slack is a projection of the same record and is never read; a program manager that wants what the channel said reads the record the channel was rendered from.

**Writes inside the lane.** `work-item.admit` under `approvals.work_items`; `work-item.attribute`, `work-item.update`, `work-item.label`, `work-item.reprioritize`, `work-item.park`, `work-item.unpark`, `work-item.link`, `work-item.unlink`, and `work-item.reparent`, each scoped to the lane as the next section enforces. Not `close` and not `retire`: closing is the harness's on a landing, and withdrawing admitted scope is the Lead Product Manager's, so both go to her as a request.

**Its own records.** `agent-context.mutate` over its own memory store, budgeted and audited as every memory is; `lane-report.write` over its own lane report and nothing else; `stop-cause.record` over its own account of stop causes and nothing else, specified under *Repeated stop causes*; and, for a standing goal lane only, the audit record its turns append to, specified under *A lane that is a standing goal*.

**Speech.** `report.file` — reports at the three severities, and the digest, which is a report; `amendment.propose` against any governed document, exactly as the developer holds it, because a lane about writing quality finds stale designs and the Lead Product Manager cannot amend one either; `exchange.ask` and `exchange.answer` on the ask channel, which gains the role as a member; and `service.request-restart`, the one request to the supervisor, specified below.

**Excluded, whatever a persona or a remit says:** every tracker action on an item outside the lane; close and retire anywhere; every triage decision and cap crossing; `run.cause`, `run.repair-continue`, `publication.merge-repeat`; directives, resolutions, and withdrawals; artifact writes; gate evidence of any kind; the human gate; any repository write; any command; any change to a report already filed. It writes no code, and it may use no tools, as the other management roles may not: what it reads, the harness reads for it.

## The lane, and how it is enforced

The lane is enforced where every conversation authority is enforced, in Go at the authority table, and it is a scope on the tracker actions rather than a new mechanism. For a label lane:

- **A creation carries the lane label in the same write that admits it.** The harness adds the label whether or not the reply named it, so an admission never exists unlabelled. Other labels may be added beside it.
- **Every other tracker action is refused unless the item carries the lane label at the moment of the act**, read from the tracker as the action runs — the same at-act reading the Lead Product Manager's repair makes. A listing that has moved does not widen the lane.
- **The lane label is never removed by its owner.** A `label remove` naming the lane label is refused; taking an item out of a lane is the Lead Product Manager's or the development manager's act.
- **Links.** A lane item may be linked to wait on any item, in or out of the lane, because waiting on work is a fact about the lane item and mutates nothing outside it. The reverse link, which would make an outside item wait on the lane, is refused.
- **Reparent** requires both the item and the new parent to carry the lane label.
- **Priority is unbounded inside the lane**, as the operator decided, and a lane admission that names a priority is placed at it.

A standing goal lane is narrower, and is enforced in the same place; its rules are in the next section.

**A lane admission is an admission**, so it passes through `approvals.work_items` exactly as the Lead Product Manager's own does: under `human` the instance proposes rather than admits, and the proposal is put to the operator with the lane named; under `automatic` it admits against a goal the operator approved, with the same attribution, the same duplicate-admission guard, and the same refusal of a done-condition naming a document no run may write. A role cannot hold a wider door into the backlog than the role that owns the backlog; `configuration-never-grants-authority` rules the alternative out. Each admission's notes record the instance and the lane, and the digest lists every admission the pass made, which is how the Lead Product Manager sees them after the fact.

## A lane that is a standing goal

yoyodyne-ifd.430.13.16. The standing goals apply to every item and every role's output, and the Lead Product Manager's sweep checks closed work against them. An instance whose lane is one standing goal takes that check over for its goal: it reads work after it closes, judges it against the goal, and admits a correction where the goal was broken. It owns no label, so its writes are confined by what it admitted rather than by what an item is tagged with.

**What confines its writes**, in Go at the authority table:

- **It may admit only a correction.** An admission names, in a field the harness reads rather than in prose, one closed item that the same turn audited and found breaking the goal; it is attributed to the goal the closed item served, and it carries the lane's standing goal among the goals the change must not break. An admission naming no closed item, or one this turn did not audit, is refused. The duplicate-admission guard and `approvals.work_items` apply as to any admission.
- **The harness marks the correction as the instance's own in the same write that admits it**, recording the instance and the goal on the item, as it adds the lane label for a label lane. Every later action is refused unless the item carries that mark, read at the moment of the act.
- **On its own corrections it may add notes and change priority, and nothing else.** Labels, parking, links, and reparenting are refused, so the instance cannot reach any item it did not admit, and cannot shape the queue around its corrections.

**What its turns audit.** Each turn takes the items closed since the closing time its audit record last reached, oldest first, at most twenty, and for each reads the merged change at its merge commit and the review recorded on it, and judges them against the goal. For each item it appends one entry to its audit record: the item, the turn, whether the work holds the goal or breaks it, and, where it breaks it, where, and the correction admitted. The record lives under the state root at `products/<product>/program-managers/<agent>/audit.jsonl`, is only appended to, and is how the next turn knows where to start. A turn that fails leaves no entries, so the next turn takes the same items. An item closed with nothing merged is recorded as audited with nothing to read.

**What its lane report carries.** The three lists every report opens with, under *The lane report*, and then the audit counts: items audited this turn, how many held the goal and how many broke it, corrections admitted, and closed items not yet audited. The counts are derived by the harness from the audit record and never written by the instance.

**How its findings reach the Lead Product Manager's sweep.** Her sweep reads the audit records through the read model and skips an item an instance has already audited for that goal; where no instance is configured for a standing goal, her sweep audits against it as it does today. Corrections are listed in the instance's digest like every admission, and where she disagrees with one she handles it as she handles any admission she did not make.

**The factory-flow lane stays a label lane**, unchanged. Its outcome is the factory moving, which is not a goal a closed change can break, and its work is admitted under its label as before.

## What leaves the lane

**One digest per instance per pass**, and nothing per finding. The digest is a report — filed through `report.file`, carried into the Lead Product Manager's conversation in the slices she already works, decided with her `handle` action — at `note` severity, or `warning` where it carries an objection to an admission or a blocker of the instance's own. Its shape is fixed so a slice of them reads alike: the lane and the pass; the admissions the pass made in the lane; each request outside the lane as one entry with the work it recommends, the goal it would serve, the recommended priority, and why; each objection to a new admission as one entry naming the item and the concern; the counts of each repeated stop cause, with the original report each names; and the instance's open requests from earlier passes, restated by id rather than repeated. A pass with nothing to say files no digest.

**Requests to the development manager and the architect** are asks on the exchange channel — judgment-only, decisionless, durable, round-capped — because what a program manager wants from either is a judgment, never an act, and the acts it might want (a triage decision, a ruling) land through those roles' own paths.

**Review of new admissions is after the fact and never a gate.** An event-triggered pass sees the items admitted since its last pass; an instance whose remit reaches one of them may object in the digest, and may park it only where the item is in its own lane. Nothing an instance says holds an admission out of the queue.

## Repeated stop causes

yoyodyne-ifd.430.13.11.1. An instance that reports on stopped runs has to say how often one cause recurs, across passes, while a collected report is never revised once filed. The two are reconciled by keeping the count out of the report: the instance keeps an account of stop causes as its own record, the report it files about a cause is filed once and never changed, and the digest carries the count beside the report it cites.

**A cause's identity** is three things read from the run record, never chosen by the instance: the stop cause the run record prints, the class it was stopped in (the work, a bound on the work, or something outside the work), and the workflow step it stopped at. Two stops agreeing on all three are one cause; differing in any one, they are two.

**What counts.** One stop event is one stopped run at one stop time, as the stoppages trigger hands it to the pass. Each is counted once against its cause, keyed by the run and the stop time, so a pass retried after a failure, or handed the same events again, adds nothing; a run stopped again after a re-run is a new stop time and counts again.

**The writer and the record.** `stop-cause.record` is the one write, scoped to the instance's own account under the state root at `products/<product>/program-managers/<agent>/stop-causes.jsonl`, which is only appended to. It takes stop events the pass was handed, by run and stop time, and refuses any it was not; the harness derives the cause from the run record and keeps the count. It may also attach to a cause the original report filed about it and the instance's reading of the cause. A stop event later found misclassified is moved by an appended correction naming the earlier entry, never by rewriting it.

**Reports and their handling.** The first time a pass sees a cause, it files one report about it and attaches that report to the cause. Later passes file nothing new for it; the digest lists the cause, its count, and the report. Once the Lead Product Manager has handled that report, further stops of the cause count on, and a new report naming the handled one and the count since it was handled is filed when that count reaches a configured number, three by default, or at once where a stopped run's item is at priority 0. The new report then becomes the one later passes cite. A handling is never reopened or changed by a later stop.

**Checkable examples.**

- *One cause across two passes.* Pass one is handed runs A and B, both stopped at the checks step by a time limit outside the work: one cause, count two, one report filed and attached. Pass two is handed run C with the same three facts: count three, no new report, and the digest reads the count of three against the first report.
- *A replayed pass.* Pass two fails after recording run C and is run again, handed C once more: C is already counted at that stop time, so the count stays three.
- *Separate causes.* Run D is stopped at the review step in the work: a second cause, count one, with its own report, and the first cause's count is unchanged.

**What tests prove and what needs live passes.** Implementation tests prove the identity rule, counting once by run and stop time, replay adding nothing, separate causes staying separate, the refusal of a stop the pass was not handed, the second report at the configured count and at priority 0, corrections appended rather than rewritten, and that no filed report is changed. Live passes prove the rest: a real instance seeing one cause recur across passes, its digest carrying the count against the original report, and a second report filed after the Lead Product Manager handled the first.

**A change to the parent's wording, for the Lead Product Manager to decide.** Grouped stop post-mortems (yoyodyne-ifd.430.13.11) asks for one report per cause with a count accumulated across passes. Under this design the count accumulates in the instance's cause record and in each digest, and the report it cites is never revised. Its acceptance wording should say so; that wording is hers, and this design does not relax it for her.

## Triggers and passes

A pass is a recurring-task firing, and every rule the recurring-task design states holds unchanged: the intake hold does not stop it, the spending pause does, budgets are committed before spend, it skips when a turn is in flight on the instance's conversation, at most one task fires per scheduler pull, and every pass ends in a durable record `yoyo sweeps` reads. An instance's `triggers` block names its schedule as `every` and its events as `on`, from a closed set: `landings` (a run integrated or a merge confirmed), `admissions` (items created), and `stoppages` (a docket entry opened). A trigger class decides when the instance gets an opportunity to judge, never what it concludes.

**A burst wakes an instance once.** Each instance keeps a durable cursor per event stream, one stream per trigger class: the run records for `landings`, the tracker's item export (`.beads/issues.jsonl`) read by each item's recorded creation time for `admissions`, and the docket records for `stoppages`. The tracker's interactions log is not among them: Beads writes an entry there for a status, priority, or assignee change and none for an item's creation, so an admissions trigger reading it would never fire. The export is a dump the tracker refreshes rather than its live store, so an admission reaches the trigger when the export next carries it; the schedule's `every` bounds how late that can be. An event past the cursor arms one wake; the wake is taken at the next pull only once the stream has been quiet for a settle window of two minutes, or the schedule is due, whichever comes first; and the pass is handed everything between the cursor and the moment it was taken, after which the cursor moves. A Lead Product Manager admitting thirty items in one turn produces one pass carrying thirty admissions. A pass that fails leaves the cursor where it was, so the next pass carries the same events rather than losing them, and the failure is on the pass record as it is for any recurring task.

**Models.** A pass asks for the task's `model` where the trigger block names one and the agent's `model` otherwise; an operator's turn in the instance's conversation asks for the agent's. That is the mechanism yoyodyne-ifd.420 shipped and it adds no key.

**Restarts.** `service.request-restart` writes one durable request naming a part the `services` section declares. The supervisor's periodic pass is the only executor: it treats a requested restart as it treats a death, under its own backoff and its five-in-two-minutes bound, refuses one while the provider-outage record stands, and records what it did on the request. At most one open request per part per instance; a second is refused while the first is unanswered. Until yoyodyne-ifd.413 lands, a request is recorded, shown on the instance's report and in the standing, and acted on by nothing, and the pass is told so in the result. No program manager restarts anything itself, kills anything, or invokes anything: noticing surfaces do not restart processes, and the harness is the only invoker.

## The lane report

Each instance owns one report: an executive summary of the lane's progress, what remains, and its blockers. It is **rewritten whole on every pass** by one `yoyodyne-lane-report` block in the reply, carrying `summary`, `remaining` as a list, and `blockers` as a list of entries each naming `what`, `waiting_on` (a mover from the read model's own vocabulary: product-manager, development-manager, architect, harness, forge, provider), and `cites` (the id of the request, report, amendment, or exchange the instance already raised about it). A blocker waiting on the operator is not written there: it belongs under Blocked on a human, below, and a blocker naming the operator as its mover is refused. A report may also carry `problems`, a list of entries each naming the id of a factory problem the read model derives, per [observability-and-dashboard](observability-and-dashboard.md), with the instance's `remedy`, its `cause` as it reads it, and the `fixes` item it admitted in its lane; the read model joins each onto the problem it cites, and an entry citing no derived problem is shown as a claim, as an unresolved blocker is. Nothing an instance writes creates, removes, or reclassifies a problem. The whole report is bounded at 16 KiB, redacted before it is written, and refused whole where it is malformed, with the previous report left standing and the refusal on the pass record.

**Every report opens with three lists**, ahead of the summary, by yoyodyne-ifd.430.13.17 and the operator's direction of 2026-09-27. Each entry is structured, never prose: the item's identifier and title, and the further field each list names. The summary stays prose beneath them.

- **Completed** — what the instance finished in the last 24 hours: items it admitted, or items in its lane, that closed in that time, and its own requests that were decided in that time. Derived by the harness from the instance's recorded acts and the tracker; the instance does not write it.
- **Handed off** — what the instance passed to another agent in the last 24 hours: an admission the scheduler will give a developer, a request in its digest to the Lead Product Manager, an ask to the development manager or the architect, an amendment proposed to a document's owner. Each entry names the agent it went to, and never a person. Derived by the harness from the same recorded acts.
- **Blocked on a human** — only what needs the operator himself, under his rule of 2026-09-26: a decision a role could make is a defect to report, never an entry here. Written by the instance as `blocked_on_a_human`, a list of entries each naming the item, what the operator must do, its reason from the closed list of reasons the waiting registry keeps (yoyodyne-ifd.432.25), the waiting record it cites, and since when. An entry with no reason from that list, or citing no waiting record, is refused and named on the pass record, and the rest of the report stands. Work handed to a step only a person can take, such as reading a log only he can open, is listed here as well as under Handed off, and the instance files it as a defect.

**A Blocked on a human entry is carried forward by the harness**, not by the instance, from pass to pass until the operator's act that clears it is recorded on the waiting record it cites. It is never dropped for age, and a later report that leaves it out does not remove it. Each entry also appears on the list of what waits on the operator, once, joined with the waiting record it cites, so the dashboard and `yoyo status` show it where he already looks.

It lives under the state root at `products/<product>/program-managers/<agent>/report.json`, beside a bounded history of the last fifty versions, each stamped with the pass and the conversation turn that wrote it. The history is kept for reading back and is not the report. It is never in the repository, which is public, and never in the memory store, which is append-only judgment rather than a surface. It is a distinct record from the pass record: the pass record says what a firing did, the report says where the lane stands.

## Status: blocked, stale, working

Status is a **read-model derivation**, computed once server-side and projected by the dashboard, `yoyo status --json` under `standing.program_managers`, and the channel's hourly line where it already posts, per `surfaces-project-one-read-model`. Nothing a program manager writes sets its own status.

- **Blocked** — the latest report names at least one blocker whose `cites` the read model resolves to a record of the instance's own that is still open: a report the Lead Product Manager has not handled, an amendment undecided, an exchange unresolved, a restart request unanswered; or the report carries a Blocked on a human entry still open. A blocker whose citation resolves to nothing, to another instance's record, or to a record already decided is not a blocker; it is shown on the card as a claim with the reason, and the instance is not blocked by it. This is what makes the flag mean the same thing for every instance: blocked is an open ask of the instance's own, addressed to a named mover, that the record can find.
- **Stale** — no pass has completed within twice the instance's schedule, measured from the last completed pass record by plain Go with no provider call on the path, per the liveness constraint in [management-and-supervision](management-and-supervision.md). A pass the provider refused, a turn the size backstop rejected, and a scheduler that died all look the same here, and that is the point: it is the out-of-band signal that the watcher is not watching, visible without anybody being pushed. An instance with no completed pass at all is stale from twice its schedule after activation.
- **Working** — otherwise.

Stale outranks blocked in the one word shown, and the card shows both. Neither is a direct message: the operator reviews when he chooses. A stale instance is counted on the channel's hourly line only where that line already posts for another reason, and the supervisor's pass, once 413 lands, reads staleness beside the scheduler's health and restarts the scheduler if the scheduler is what died — never the instance, which is a conversation and not a process.

## Instances see each other

Every pass opens with a line per other instance: name, lane, status, and its open requests by id. Any lane report is readable in full through `readmodel.read`. An instance never writes another's report, store, or lane; work it believes belongs in another lane goes to the Lead Product Manager in the digest, named for that lane, and a judgment it wants from another instance is an ask, round-capped and visible like every ask.

## The boundary with the development manager's hourly sweep

The sweep (yoyodyne-ifd.283) is the standing eye over the machinery, and it keeps everything it has: the docket and every triage decision, dead claims, stuck and dropped publications, forge hygiene, cap crossings, and root-cause work filed for each fix. A program manager whose remit is the factory takes none of that ground:

| The development manager's sweep | The program manager's pass |
| --- | --- |
| a stoppage, a docket entry, a claim, a publication — anything the read model already says is waiting on a named mover | what nobody is assigned: the pattern across passes — the same cause stopping runs twice, throughput falling with no hold to explain it, fixes landing without root-cause work behind them |
| decides, crosses, repairs, re-runs, re-arms | admits root-cause and prevention work in its lane, objects in the digest, asks the development manager for a judgment |
| files root-cause work for each fix it makes | checks that filing happened and names it in the report where it did not |

Two rules hold the line. A program manager never records or requests a triage decision about a docket entry, whose next mover is already named. And before it admits an item it checks the sweep's own filings, through the duplicate-admission guard and by reading the sweep records, so one stoppage never produces two items. Where the two still meet on one fact, the sweep acts and the program manager reports, which is the division the operator drew for forge hygiene and is the division here.

## The technical-health program manager

yoyodyne-ifd.430.13.19. The operator asked for a second instance, `technical-health-pm`, whose lane is the label `technical-health`: it looks for evidence of technical debt and of running cost in the harness's own code, and admits bounded work to remove what it proves. It is an instance of this role like any other, with the shared persona and every rule above; what follows is what it adds. `factory-flow-pm`, its configuration, its memory, and its reports are unchanged by it.

**Model and effort, pinned.** Its block names backend `codex`, model `gpt-6-astra`, and effort `high`, and no alternate model, endpoint, or provider. These hold for its recurring passes, for an operator's turn in its conversation, and for a resumed session alike. The harness checks each turn's record after the call: a turn whose served model is not `gpt-6-astra`, or whose recorded effort is not high, is recorded as a failed pass, and nothing in its reply is acted on. Where the endpoint is exhausted or the model unavailable, the pass waits for the endpoint's reset as any work does and is recorded as skipped with that reason; it never runs on another model. Where the provider does not report the effort it served, the record says so rather than assuming it, and the first pass names that as the limit of what it could verify.

**Codex effort comes first.** The configuration load must accept `effort: high` for a Codex agent and pass it on every invocation, including a resumed one, before this block can be written; on a build that refuses it, the block is refused at load and the instance does not exist. That support extends the existing effort setting (yoyodyne-ifd.435.5) and adds no second mechanism.

**Schedule.** `triggers.every: 45m`, as the operator decided, and `on: [landings]`, collapsed into one pass by the cursor and settle window above. A landing wake is also not taken sooner than fifteen minutes after the instance's last pass started, so a burst of merges cannot run it several times an hour on top of the schedule. The operator's reason for 45 minutes is keeping the provider's cache warm; nothing here promises the provider keeps it that long.

**What one pass inspects.** Two parts, both at one recorded commit, the target branch's head when the pass starts:

- *What changed.* The changes merged since the commit its last pass inspected, reading at most thirty changed files; what it could not reach is named as not inspected and carried to the next pass.
- *One area in depth.* The next area in a fixed rotation its remit lists, one area a pass, so the cycle covers all of them in turn. The first remit lists nine: module boundaries and dependency direction, including cycles; duplicate sources of truth and duplicate policy or state transitions; adapters that diverge and infrastructure built twice; dead paths and compatibility layers with no plan to remove them; configuration and abstraction nothing needs; the cost of checks and tests; running cost in CPU, memory, disk, and process use, including repeated subprocesses and validation; model, context, and token cost; and concurrency and how long resources are held.

The commit, the area, and the paths it read are written in the lane report as `inspected`, and the harness checks each path against the reads the pass actually recorded: a path the pass did not read is shown as a claim, not as inspected. Missing evidence is never written up as clean code: an area the pass could not reach is reported as not inspected, with what it lacked.

**Reading the code.** Beside `repository.read` and `repository.list`, the bundle gains `repository.search`: one literal string or regular expression searched across the files Git tracks at the recorded commit, optionally under one path prefix, returning each match's path, line number, and line, each line cut at 200 bytes, at most 200 matches and 32 KiB in all, with any cut declared. It reads nothing Git does not track and nothing outside the repository, is framed as evidence and never instruction, and is recorded on the pass like every tool. It is a tool under [tool-interface](tool-interface.md), which lists it when that design is next revised; the bounds here are the ones it is built to. With it a pass can follow a function from its callers to its tests across packages, which reading named files alone cannot.

**Spend bounds**, enforced by the harness and not left to the instance's judgment. They are numeric limits the development manager may change through the configuration route [the portable agent configuration design](portable-agent-configuration.md) gives her:

- at most sixty repository reads, lists, and searches in one pass; past that, each further one is refused and the pass is told so;
- at most five million provider tokens a day, counted from the usage the provider reports, in the operator's local day. A pass whose usage the provider did not report counts as two hundred thousand. Once the day's bound is reached, passes are skipped until the next day and each skip is recorded with the reason;
- a pass does not start while any ready priority 0 item is waiting for capacity on the provider account the instance uses; the skip is recorded, so an audit never holds back reliability work.

**What a finding carries.** Each one names the commit and paths inspected; the consequence observed; the cause, or plainly that the cause is not known; the simplest bounded remedy; the role that owns it; how acceptance is checked, with a measured baseline wherever the finding is about cost; and a priority that matches the harm. A finding that the cost of a change outweighs its benefit, with no change recommended, is a valid result. Duplication is a finding only where the two copies do the same job; two pieces of code that look alike but serve different responsibilities are not.

**What it does with one.** Before admitting, it searches the open and recently closed work, through the duplicate admission guard and a survey, and groups findings by cause. A cause already owned by an existing item goes in its digest as evidence for that item, named by title and id, because noting on an item outside its lane is not its act. A new cause is admitted under `technical-health`, carrying the reliability label as well where it fixes a defect, by the operating rule. It admits at most three items a pass, and holds the lane to ten admitted items not yet started; past either bound, findings are listed in the digest as deferred with the reason. It follows its own earlier admissions on later passes and, once one lands, checks the measured outcome against the acceptance it stated, and says so in the next digest. It admits no speculative framework, no broad rewrite, no change for appearance or line count, no optimization without a measurement, and no deletion of a test that checks something real.

**Its digest** keeps the shape every digest has, and opens with: the highest impact findings, at most five; the commit, the area, and how many paths were inspected; what evidence was missing; and the work admitted, covered by an existing item, or deferred, each with its reason and measurements. Every work item it names is named by title, then id.

**Its boundaries.** Designs are the architect's: a design the code has outgrown is an amendment it proposes, never a competing ruling. Running work and recovering it are the development manager's. Order across lanes is the Lead Product Manager's. A finding about stopped runs, throughput, or the queue belongs to `factory-flow-pm`, and goes to it as an ask. It adds no approval step to any change.

**Activation**, by the development manager, through the recorded configuration step [the portable agent configuration design](portable-agent-configuration.md) gives her, under a grant naming exactly two files: the project's configuration file, at the path the configuration load reads, and the remit at `.yoyodyne/remits/technical-health.md`. Its order is fixed: the build accepting Codex effort is deployed first; `yoyo config show` on the deployed build reports the instance with its backend, model, effort high, no alternate, `every` 45m, and landings; the scheduler's next listing shows the task at that schedule; then the first pass runs. Rolling back is removing the block by the same step; its memory, reports, and lane items stay.

**What proves it works.** Scheduling alone does not. The instance is working once its first completed pass records the requested and served provider, model, and effort, or names plainly the effort the provider did not report; shows at least one `repository.search` and one `repository.read` at a recorded commit; writes a lane report with its `inspected` entry; and files a digest or records that it had nothing to say. A first pass missing any of these leaves the activation incomplete, and the development manager reports it as such.

## The dashboard section

A section listing the active program managers: each row its name in full, its lane, its status as a word and a badge, the counts of its three opening lists, and its report as a link that opens the report on a card — the three lists first, each as a list with a line reading none where it is empty, then summary, remaining, blockers with each blocker's mover and citation, the claims that did not resolve, when the report was written, and the open restart requests. `yoyo status` shows the three lists first in the same order, and every surface has a form in plain text that keeps each entry on its own line, so the lists read as lists in a terminal that renders no emphasis. It is served from `standing.program_managers` and from `/api/program-managers/<agent>` for the report, both read-model queries, which serve the three lists ahead of the rest, so the page, `yoyo status`, and the channel cannot disagree about an instance. The section is admitted under yoyodyne-ifd.432 as yoyodyne-ifd.432.11 and builds to [observability-and-dashboard](observability-and-dashboard.md), which this design revises to name the query.

## Deferred, deliberately

- Conversion of the pass into a workflow definition, which arrives with the management-request conversion under configurable-workflows and changes nothing here.
- Two instances sharing a lane, or a lane defined by anything but one label or one standing goal.
- A standing goal lane auditing work closed before the instance was first activated; its first turn starts from its activation time, and earlier work stays with the Lead Product Manager's sweep.
- A program manager reaching the forge, the network, or research sources: its evidence is the durable record and the repository at a recorded commit.
- A push of any status change: the operator asked for a pulled report and gets one.
- The supervisor acting on an instance's staleness beyond restarting a dead scheduler; what else it might do waits for the first stale instance to teach it.
- Any record kind `log.read` does not yet read: the list, and how a kind is added, are in [tool-interface](tool-interface.md).

## The ten questions, ruled

1. The capability set is the bundle above; the read surface is the tracker, the repository at a recorded commit, read and searched, the read model, the other instances' reports, and the records `log.read` reaches as [tool-interface](tool-interface.md) states them, and never Slack.
2. The lane is a scope on the tracker actions in the conversation's authority table: the label applied on every creation, every other action refused unless the item already carries it, the label never removed by its owner, and admission under `approvals.work_items` as the Lead Product Manager's is. A standing goal lane is scoped to the corrections it admitted, as ruled above.
3. A check run is not a capability; the instance admits a diagnosis-class item in its lane.
4. A restart is one bounded durable request the supervisor's pass executes; recorded and inert until yoyodyne-ifd.413 lands.
5. Event triggers collapse through a per-instance cursor and a two-minute settle window into one pass carrying everything since the last.
6. The backstop is the stale status, derived by non-model machinery and served by the read model; the supervisor reads it once it exists.
7. Instances see each other's status, open requests, and reports, mutate nothing of each other's, and may ask each other.
8. A pass runs on the task's model and an operator's turn on the agent's, as yoyodyne-ifd.420 shipped; no new key.
9. The lane report is a rewritten bounded document under the state root with a bounded history, written by one typed action, never in the repository or the memory store, and it opens with Completed, Handed off, and Blocked on a human.
10. Blocked is an open, citable request of the instance's own to a named mover that the read model finds undecided, or an open Blocked on a human entry; stale is no completed pass within twice the schedule; working is neither.
