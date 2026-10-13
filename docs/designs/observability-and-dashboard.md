---
id: observability-and-dashboard
kind: design
title: One observability read model, and the local dashboard that projects it and takes the operator's own decisions
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:29:09Z
      reason: promoted from the operator's shared-observability-and-reduced-dashboard brief under the 2026-08-22 mandate; the gating input for yoyodyne-ifd.139 and yoyodyne-ifd.141, with deviations recorded in the promotion
    - action: amended
      by: architect
      at: 2026-09-19T00:30:00Z
      reason: approved amendment 608138f6 from yoyodyne-ifd.141.1 (591e0ae5 declined as superseded by the repair round's rename) - the capacity-blocked state landed as a read-model query; the deviation-to-implement recorded at promotion is discharged
    - action: amended
      by: architect
      at: 2026-09-19T14:10:00Z
      reason: operator direction of 2026-09-19 (directive-6de828a8) - the bind address and allowed hosts become configuration with loopback defaults; a non-loopback bind is an opt-in requiring a supplied token, with Host and Origin validated against the configured set and every other rule unchanged; the read-only, no-write-path property recorded as what makes the opt-in safe, and plain-HTTP transport stated honestly
    - action: amended
      by: architect
      at: 2026-09-25T04:00:00Z
      reason: yoyodyne-ifd.430.6 companion, also landing approved amendment 7b674d9e from yoyodyne-ifd.432.9 - the read model gains the program-manager query and the page a section projecting it; the section list is restated as shipped, with spend in its own box under the status band and throughput reporting endings only
    - action: amended
      by: architect
      at: 2026-09-26T16:00:00Z
      reason: yoyodyne-ifd.432.7 - a dashboard session is bound to a person by a fourth operators-mapping namespace, dashboard_token, a keychain or file reference; two acts each behind a grant held by the person, recorded as the resolved human; what a session may never do stated
    - action: amended
      by: architect
      at: 2026-09-27T11:00:00Z
      reason: 'yoyodyne-ifd.432.17 - the factory-problems query and section designed: four kinds derived from the records and never from a role''s writing, an identity that folds repeats into a count, the entry shape, the attention line as the slice whose mover is the operator, the program manager''s judgment joined by citation, and the capacity panel folded in as degraded entries'
    - action: amended
      by: architect
      at: 2026-09-30T02:30:00Z
      reason: yoyodyne-ifd.430 - the factory problems and every read-model entry carry the owner, reason, remedy, and hand the ownership resolver returns, and the needs-a-human line prints yours to decide and a role's decision that needs your hand from it
    - action: amended
      by: architect
      at: 2026-10-13T09:19:09.624414Z
      reason: 'The dashboard is no longer read-only: the v1 non-goals were revised on October 11 to let it take the operator''s own decisions, recorded as the command line records them. This revision for yoyodyne-ifd.437.44 removes the read-only wording, lists the acts and their grants, and keeps the resolved person on every record; it carries the change proposed in amendment-a7dfcc49. The management and supervision design follows in a second revision.'
approvals:
    - policy: approvals.designs
      revision: 7
      by: harness
      at: 2026-10-13T09:19:09.624414Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 773, for document-773.1 under the automatic approval policy
---
# One observability read model, and the local dashboard that projects it and takes the operator's own decisions

## What this is for

The operator can already see what the harness does — in the conversation, in `yoyo-status`, in `yoyo cost`, in Slack threads — and each surface assembles its answer separately. Every surface added that way is another place a number can disagree with the tracker, and the operator adjudicating between surfaces is the failure this design exists to prevent. It serves the goal that the operator can see what the system does on their behalf, and the cost, attention, and legibility goals beside it: one canonical, server-side derivation of status, throughput, cost, and capacity, and surfaces that only present it. The dashboard delivered here is the first surface built on that model rather than beside it.

## The read model *(the section yoyodyne-ifd.139 builds)*

A shared Go package, independent of HTTP and of any rendering, reading only the durable records that already exist — run state, normalized event logs, the tracker, conversation records, the reports pile, recorded prices — and writing nothing. The service owns derivation and attribution; surfaces own presentation and nothing else. Its queries expose:

- source health and observation time for every answer;
- active invocations, each with role, backend, model, **account alias and configuration revision** as run records now carry them, work-item id and title, phase, and start time;
- queued work and the operator-attention conditions — blockers, unresolved directives, undecided proposals, outstanding publications;
- pipeline stage counts and recent transitions, and a bounded recent-activity feed sufficient to explain the current picture;
- integrated-work totals over explicit daily and weekly windows, counting the same events the CLI counts;
- provider cost with every figure classified: known (provider-reported), unknown, or unattributable, and an `estimated` class reserved for future sources that is never summed silently into known — unknown renders as unknown, never as zero, exactly as the cost surfaces already hold;
- capacity: per configured account alias, the model, active invocation count, state — healthy, waiting, usage-limited, or **capacity-blocked** — the recorded reset time, and recent capacity events. The capacity-blocked state exists as a read-model query, carried in `yoyo status --json` as `standing.capacity_blocked`, derived from the run records and the usage-limit log, naming each parked or blocked run and each refused conversation with its reset time, the time already waited, and a remedy.
- factory problems: everything the line is not doing that the goals say it should, where nothing in the ordinary flow fixes it by itself, derived from the records above and from nothing a role writes. Each problem carries a kind from a closed set of four — **broken**, the harness failed at its own doing: a firing refused before its first turn, a listing the tracker returned undecodable, a part the supervisor left down, a page that cannot build; **stalled**, nothing moving that should: a stall record, slots idle over ready work, a recorded decision unattempted, an approved change awaiting its resume, a queued merge whose checks cannot be read; **blocked**, a decision somebody owes is unmade: stoppages awaiting the development manager, amendments awaiting their owner, rulings made and not landed, items the docket window has not shown; **degraded**, running but worse: a stage the bound stopped under load, a provider window or a capacity-blocked run, lost races replaying, a part on a stale build. A deep queue, a run in progress, and a green queued merge are not problems. Each problem has an identity — its kind, its cause, and its subject — onto which every occurrence folds, and carries: what is wrong, in a sentence; since when, and the latest occurrence; its count; its cost so far as the records hold it — runs spent on it, decisions waiting behind it, hours the line has stood, and money where the run logs price it, each figure classified known or unknown as every cost is; its mover, in the vocabulary the attention line already uses; the admitted item that fixes it, found by the item's own citation of the problem's source record, or the plain statement that none is admitted yet; and the source records it was derived from. The stop cause every stopped run records (yoyodyne-ifd.428.32) and the run-stop inventory (yoyodyne-ifd.429.28) are what classify a stopped run into a kind; until they land, a stopped run is classified by the fields it has, and one nothing classifies is a problem of kind stalled with its cause named as unclassified, which is itself a finding.
- **Who owns each entry.** The read model calls the resolver of [ownership and the operator](ownership-and-the-operator.md) once per entry and carries its owner, reason, remedy, capability, and hand; the dashboard, the Slack sink, `yoyo status`, and every conversation's context project that answer and derive no owner of their own, and the needs-a-human line prints its two heads, yours to decide and a role's decision that needs your hand, from it.
- program managers: per configured instance, its name, lane, the status derived as [program-manager](program-manager.md) specifies — blocked from an open cited request, stale from the pass records against the instance's schedule, working otherwise — its open restart requests, and its current lane report, served whole one instance at a time. Where an instance's lane report cites a problem by id, the read model joins the instance's judgment onto the problem — its remedy, its account of the cause, the count it corrected, and the item it admitted in its lane — and the instance's own status and blockers are problems of kind blocked or stalled with the instance as subject.

The model also serves what Slack's governed behavior already needs — per-item status for thread reactions, directive lifecycle marks, item titles for thread openers — so the sink presents these derivations instead of computing them.

**Honesty is a property of the model, not a style of the page.** Every metric identifies its time window; every source carries health; every total names its unknown and unattributable portions. A stale or unreadable source degrades the answer visibly rather than narrowing it silently — the same rule the freshness line and the cost floors already follow.

**The attention line is a slice of the problems.** Every problem names its mover, and what `yoyo status`, the page, and the channel put in front of the operator as needing a human is the problems whose mover is the operator, and nothing else: an entry moved by a role or the harness is the line working, shown to him and asked of him never. Two requests stay on that line that are not problems, because the operator holds them by design: a proposal against the brief or the goals that moves fundamental intent, per [who-approves-a-change-to-intent](../decisions/who-approves-a-change-to-intent.md), and a directive awaiting his answer. Who moves a problem is never decided here: every problem carries the owner, reason, and remedy that [the ownership resolver](ownership-and-the-operator.md) returns for its kind, so the needs-a-human slice is exactly the problems whose owner is the operator for a reason on the closed list, and a problem whose remedy only needs his hand is shown under that head instead, as the harness's defect.

## The dashboard *(the sections yoyodyne-ifd.141 builds)*

A locally hosted page with seven visually distinct sections: a top status band (active agents, queued work, attention count, throughput, landed today and this week); a spend box directly under it, showing the last 24 hours rolling and the last seven local days by kind — runs, conversations, branch reviews, exchanges, side threads — with a 30-day daily listing behind it, every figure classified and every floor marked; live agent and work cards (role, item title and id, phase, provider and model, elapsed time); a compact pipeline view showing where work accumulates or blocks; throughput over the same two windows, reporting endings only in the run history's own words, with cost left to the spend box; the factory problems from the model's problems query, grouped by kind with broken first, each entry its sentence, its age and count, its cost with every unknown marked, its mover as a word, its fixing item as a link or the words `none admitted yet`, and the factory-flow program manager's remedy where its report cites the entry, with that instance's lane-report summary as the section's own summary; provider capacity is folded in as entries of kind degraded and has no section of its own; and the program managers, each instance with its name in full, its lane, its status as blocked, stale, or working, and a link opening its lane report, from the model's `program_managers` query per [program-manager](program-manager.md). Attractiveness is an acceptance requirement — hierarchy, typography, spacing, accessible color, responsive layout, useful empty, loading, and error states, a small amount of purposeful motion — and polish must never imply precision the model did not claim. Simple polling; push infrastructure is deferred unless polling proves inadequate. A degraded source gets a prominent indicator while the page stays usable. Restarting the dashboard changes no workflow state and loses no history, because the history lives in the durable records, not in the page.

The dashboard is a projection, never an engine: it owns no workflow, conversation, provider, or configuration state, and keeps no store of work state of its own. The only writes it makes are the operator's own decisions listed under *Acting from the dashboard* below, each carried out by the same code the command line runs for that act and recorded exactly as the command line records it. This is the boundary [the v1 non-goals] record as amended on 2026-10-11, and this design binds to it.

## Web security *(the repository's web-service conventions, established here)*

The bind address and the allowed hosts are configuration; the defaults are loopback and the loopback Host, and a project that says nothing gets exactly the behavior it had. An explicit non-loopback bind is an operator opt-in that keeps every other rule intact:

- **A token is required on every request**, presented in a header or cookie and never in a URL or a log; a request without one, or with a wrong one, fails closed. Under the loopback default the token is generated per start and printed where the operator can read it. **Under a non-loopback bind the token must be supplied** — from the per-project secret store or the environment, never from `.yoyodyne` — because a generated token printed to one terminal is unusable from another device; a non-loopback bind with no supplied token, or a supplied token below the minimum entropy, is refused at load.
- **Host and Origin are validated against the configured address and allowed hosts**, not hard-wired to loopback. A request naming a host outside the configured set fails closed exactly as it did before.
- The content-security policy stays restrictive and CDN-free; all user-, model-, repository-, and Slack-supplied text stays untrusted and escaped; nothing secret, credential, or private-identifier enters the read model or the page.
- **What a wider bind exposes.** The shared token reads the projection and acts on nothing. An act is open only to a session bound to a person by that person's own token, and only for an act that person holds the grant for, as below. So the opt-in widens who can read observability data, and lets a bound person take their own decisions from another device; it never lets anyone act beyond the grants they hold.
- **Transport is plain HTTP in V1.** A non-loopback bind sends the token and the page in clear over the operator's network; TLS termination is deferred, and the opt-in is for networks the operator trusts. Binding to a specific interface address is preferred over a wildcard, which reaches every interface the machine has.

## Acting from the dashboard: who a session is

The shared token reads and acts on nothing, because it resolves to nobody. A person who acts from the page binds a fourth identifier namespace in the operators mapping, `dashboard_token`, a reference to a per-person token in the keychain (`yoyo-dashboard.<product>.<operator>`) or the token file under the state root, written by `yoyo operator token <name>` and refused to agent processes. A session presenting that token is that person while the tab holds it: proof is possession, the same layer as the forge's push authentication. It reads everything the shared token reads, travels only as a bearer header under the rules above, and pays the same plain-HTTP cost.

The acts a bound session may take are the operator's own decisions, and only these:

- **Approving or declining what waits on his decision**: a proposed change to a document, a document a role wrote in a conversation that waits for his approval, and work proposed in a conversation.
- **Settling a step he owes**, which needs `direct-work`.
- **An ask** about any of the above, which puts a question to the role concerned and decides nothing.

Each act is carried out by the command-line verb's own code for the same act, and needs the grant the command line needs for it, held by the person, never by the token: deciding a proposed change needs `decide-amendments`, and one against a product document needs `own-intent` beside it. An act records the resolved human — name, the `dashboard` namespace, a digest prefix of the token — on the same record the terminal writes, in place of `by: operator`. A further act the command line offers is added to this list by a revision of this design, one feature at a time, as the v1 non-goals allow. A session may never direct work beyond those acts, never reach anything an agent could, never read more than the shared token reads, and never write anything but the records those acts write; every other method and path is refused.

## Process shape

A standalone dashboard command is the V1 shape. It reads the durable service, exposes health and readiness, and is adoptable by the later supervisor for start, stop, and observation without redesign; it remains useful afterwards for inspection and recovery, and never becomes a second control plane. Browser availability is never a prerequisite for harness operation.

## Deferred, deliberately

Deep drilldowns; rich timelines and custom historical windows; readiness and conflict visualizations; push infrastructure; multi-account pool controls; automatic lifecycle ownership by the supervisor.
