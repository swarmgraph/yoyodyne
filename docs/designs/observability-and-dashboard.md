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
      at: 2026-10-13T12:14:17.907726Z
      reason: 'yoyodyne-ifd.437.44: the dashboard is no longer described as read-only. Under the operator''s change to the v1 non-goals of 11 October (amendment-a7dfcc49), the page may take the operator''s own decisions, first approving or declining what waits on him and later further command line acts named one at a time, each recorded as the command line records it; it stays local, unhosted, and keeps no work state. Rewritten whole after the earlier copy (document-773.1) was refused by the word check for one joined word.'
    - action: amended
      by: architect
      at: 2026-10-14T05:07:05.014937Z
      reason: 'yoyodyne-ifd.414.1, for the supervised dashboard (yoyodyne-ifd.414): how the token generated for a loopback dashboard reaches a person under the supervisor, and when the supervisor takes up a dashboard on a new build. With it: the team at work replacing the live cards (yoyodyne-ifd.432.30), the backlog page and its query (yoyodyne-ifd.432.31), and the delivery page after DORA (yoyodyne-ifd.432.34), each as ruled and summarised on its item. Everything else in the design is unchanged.'
    - action: amended
      by: architect
      at: 2026-10-21T08:10:21.29383Z
      reason: 'yoyodyne-ifd.432.37 and yoyodyne-ifd.432.29. The cost entry, the honesty rule, the problem entries and the spend box now use exactly the kinds of cost the provider adapter design''s usage and cost rules define - reported, estimated, unknown, and a comparison figure at published API prices shown apart - and the unattributable kind and the line reserving estimates for later sources are removed. A new section says how usage limits across several providers and accounts are shown: one line naming the limit most likely to stop work first with a count of the rest, the full list behind it and in yoyo status --json, at or near its limit told apart in words, and which are factory problems. Everything else is unchanged.'
approvals:
    - policy: approvals.designs
      revision: 7
      by: harness
      at: 2026-10-13T12:14:17.907726Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 794, for document-794.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 8
      by: harness
      at: 2026-10-14T05:07:05.014937Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 883, for document-883.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 9
      by: harness
      at: 2026-10-21T08:10:21.29383Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 897, for document-897.1 under the automatic approval policy
---
# One observability read model, and the local dashboard that projects it and takes the operator's own decisions

## What this is for

The operator can already see what the harness does — in the conversation, in `yoyo-status`, in `yoyo cost`, in Slack threads — and each surface assembles its answer separately. Every surface added that way is another place a number can disagree with the tracker, and the operator adjudicating between surfaces is the failure this design exists to prevent. It serves the goal that the operator can see what the system does on their behalf, and the cost, attention, and legibility goals beside it: one canonical, server-side derivation of status, throughput, cost, and capacity, and surfaces that only present it. The dashboard delivered here is the first surface built on that model rather than beside it. It also lets the operator take the decisions that are already the operator's, from the same page that shows them, without becoming a second place where work state is kept.

## The read model *(the section yoyodyne-ifd.139 builds)*

A shared Go package, independent of HTTP and of any rendering, reading only the durable records that already exist — run state, normalized event logs, the tracker, conversation records, the reports pile, recorded prices — and writing nothing. The service owns derivation and attribution; surfaces own presentation and nothing else. Its queries expose:

- source health and observation time for every answer;
- active invocations, each with role, backend, model, **account alias and configuration revision** as run records now carry them, work-item id and title, phase, and start time;
- queued work and the operator-attention conditions — blockers, unresolved directives, undecided proposals, outstanding publications;
- pipeline stage counts and recent transitions, and a bounded recent-activity feed sufficient to explain the current picture;
- integrated-work totals over explicit daily and weekly windows, counting the same events the CLI counts;
- provider cost with every figure in one of the kinds of cost the usage and cost rules of [the provider adapter design](provider-adapters-and-endpoints.md) define, and in no other: **reported**, the figure the provider itself reported; **estimated**, worked out by the harness from recorded usage and a recorded price of a stated version, always labelled as estimated and never summed silently into reported; and **unknown**, where neither exists, which renders as unknown and never as zero, exactly as the cost surfaces already hold. Apart from all three, a **comparison figure at published API prices** for the same usage is shown beside spend and never added into any spend total. A figure the records cannot tie to a work item is still one of these kinds, and is shown under the run or conversation that spent it;
- capacity: per configured account alias, the model, active invocation count, state — healthy, waiting, usage-limited, or **capacity-blocked** — the recorded reset time, and recent capacity events. The capacity-blocked state exists as a read-model query, carried in `yoyo status --json` as `standing.capacity_blocked`, derived from the run records and the usage-limit log, naming each parked or blocked run and each refused conversation with its reset time, the time already waited, and a remedy.
- usage limits: every usage reading a provider reports, one per account alias and limit, with the provider, the period the limit covers as the provider names it — five hours, the week, a model's own week — the share used, when it resets, and when it was read. A provider that reports no reading for an alias is returned as `not reported`, never as zero used. Each reading carries its state and its place in the order described under usage limits below.
- factory problems: everything the line is not doing that the goals say it should, where nothing in the ordinary flow fixes it by itself, derived from the records above and from nothing a role writes. Each problem carries a kind from a closed set of four — **broken**, the harness failed at its own doing: a firing refused before its first turn, a listing the tracker returned undecodable, a part the supervisor left down, a page that cannot build; **stalled**, nothing moving that should: a stall record, slots idle over ready work, a recorded decision unattempted, an approved change awaiting its resume, a queued merge whose checks cannot be read; **blocked**, a decision somebody owes is unmade: stoppages awaiting the development manager, amendments awaiting their owner, rulings made and not landed, items the docket window has not shown; **degraded**, running but worse: a stage the bound stopped under load, a provider window or a capacity-blocked run, lost races replaying, a part on a stale build. A deep queue, a run in progress, and a green queued merge are not problems. Each problem has an identity — its kind, its cause, and its subject — onto which every occurrence folds, and carries: what is wrong, in a sentence; since when, and the latest occurrence; its count; its cost so far as the records hold it — runs spent on it, decisions waiting behind it, hours the line has stood, and money where the records hold it, each figure in its kind of cost as every cost is; its mover, in the vocabulary the attention line already uses; the admitted item that fixes it, found by the item's own citation of the problem's source record, or the plain statement that none is admitted yet; and the source records it was derived from. The stop cause every stopped run records (yoyodyne-ifd.428.32) and the run-stop inventory (yoyodyne-ifd.429.28) are what classify a stopped run into a kind; until they land, a stopped run is classified by the fields it has, and one nothing classifies is a problem of kind stalled with its cause named as unclassified, which is itself a finding.
- **Who owns each entry.** The read model calls the resolver of [ownership and the operator](ownership-and-the-operator.md) once per entry and carries its owner, reason, remedy, capability, and hand; the dashboard, the Slack sink, `yoyo status`, and every conversation's context project that answer and derive no owner of their own, and the needs-a-human line prints its two heads, yours to decide and a role's decision that needs your hand, from it.
- program managers: per configured instance, its name, lane, the status derived as [program-manager](program-manager.md) specifies — blocked from an open cited request, stale from the pass records against the instance's schedule, working otherwise — its open restart requests, and its current lane report, served whole one instance at a time. Where an instance's lane report cites a problem by id, the read model joins the instance's judgment onto the problem — its remedy, its account of the cause, the count it corrected, and the item it admitted in its lane — and the instance's own status and blockers are problems of kind blocked or stalled with the instance as subject.
- the team at work and one piece of work, the `team` and `work` queries described under the team at work below;
- the backlog and the delivery measures, the `/api/backlog` and `/api/delivery` queries described under the backlog page and the delivery page below.

The model also serves what Slack's governed behavior already needs — per-item status for thread reactions, directive lifecycle marks, item titles for thread openers — so the sink presents these derivations instead of computing them.

**Honesty is a property of the model, not a style of the page.** Every metric identifies its time window; every source carries health; every total names its estimated and unknown portions. A stale or unreadable source degrades the answer visibly rather than narrowing it silently — the same rule the freshness line and the cost floors already follow.

**The attention line is a slice of the problems.** Every problem names its mover, and what `yoyo status`, the page, and the channel put in front of the operator as needing a human is the problems whose mover is the operator, and nothing else: an entry moved by a role or the harness is the line working, shown to the operator and asked of the operator never. Two requests stay on that line that are not problems, because the operator holds them by design: a proposal against the brief or the goals that moves fundamental intent, per [who-approves-a-change-to-intent](../decisions/who-approves-a-change-to-intent.md), and a directive awaiting the operator's answer. Who moves a problem is never decided here: every problem carries the owner, reason, and remedy that [the ownership resolver](ownership-and-the-operator.md) returns for its kind, so the needs-a-human slice is exactly the problems whose owner is the operator for a reason on the closed list, and a problem whose remedy only needs the operator's hand is shown under that head instead, as the harness's defect.

## The dashboard *(the sections yoyodyne-ifd.141 builds)*

A locally hosted page with seven visually distinct sections: a top status band (active agents, queued work, attention count, throughput, landed today and this week, and the usage line described under usage limits below); a spend box directly under it, showing the last 24 hours rolling and the last seven local days by kind — runs, conversations, branch reviews, exchanges, side threads — with a 30-day daily listing behind it, every figure in its kind of cost, estimated figures labelled as estimated, the comparison figure at published API prices shown apart and never added in, and every floor marked; the team at work, one row per role and developer slot, as described under the team at work below; a compact pipeline view showing where work accumulates or blocks; throughput over the same two windows, reporting endings only in the run history's own words, with cost left to the spend box; the factory problems from the model's problems query, grouped by kind with broken first, each entry its sentence, its age and count, its cost with every unknown marked, its mover as a word, its fixing item as a link or the words `none admitted yet`, and the factory-flow program manager's remedy where its report cites the entry, with that instance's lane-report summary as the section's own summary; provider capacity is folded in as entries of kind degraded and has no section of its own; and the program managers, each instance with its name in full, its lane, its status as blocked, stale, or working, and a link opening its lane report, from the model's `program_managers` query per [program-manager](program-manager.md). Attractiveness is an acceptance requirement — hierarchy, typography, spacing, accessible color, responsive layout, useful empty, loading, and error states, a small amount of purposeful motion — and polish must never imply precision the model did not claim. Simple polling; push infrastructure is deferred unless polling proves inadequate. A degraded source gets a prominent indicator while the page stays usable. Restarting the dashboard changes no workflow state and loses no history, because the history lives in the durable records, not in the page.

Two further pages sit beside the main page: the backlog page and the delivery page, each reached from a link in the top status band and each linking back. They are pages of their own rather than sections, so the main page stays one screen of what is happening now.

The dashboard is a projection, never an engine: it owns no workflow, conversation, provider, or configuration state, and keeps no work state of its own. The only writes it makes are the operator's own decisions, described under acting from the dashboard below, each taken through the same code the command line uses and recorded exactly as the same act from the command line is recorded. The page adds no decision of its own and offers no act the command line does not already offer. This is the boundary [the v1 non-goals](../product/goals/v1-non-goals.md) record since the operator's change of 11 October, and this design binds to it. The team section, the backlog page, and the delivery page are reading only, and add no act.

## The team at work

The main page's live section shows the team working: whether each role and each developer slot is doing something, what, and whether it is moving. It replaces the live agent and work cards, and it is built from records the harness already writes; it adds none.

**One row per worker.** The read model's `team` query returns one row for each role conversation the harness runs — the Lead Product Manager, the architect, the development manager, and each program manager instance — one for each configured developer slot, and one for each review in progress. A row carries: who it is, by role and slot; the work item it holds, by title and id, or none; the phase of that work as the run record names it; when that phase began; the latest recorded activity, in a few words taken from the event log, and its time; and how fresh the reading is.

**Five states, decided from the records.** Every row is in exactly one:

- **working**: an invocation is recorded running and its event log has recorded activity in the last five minutes;
- **waiting**: it is held on something that will arrive without anybody deciding — a provider's reset, a check in progress, a free slot, a queued merge — and the row names what it waits on and, where recorded, until when;
- **blocked**: it is held on a decision somebody owes; the row links to the factory problems entry for it and shows the owner that entry carries, choosing none of its own;
- **idle**: nothing is assigned and nothing is running;
- **stale**: the records support none of the four — a running invocation's last recorded activity is older than five minutes, or a source the row needs is unhealthy.

A reading the model is unsure of is stale, never working. Motion on the page is shown only for a row whose event log has recorded something since the last poll.

**Activity is not progress.** A row shows recorded activity — turns, tool use, files touched — apart from verified progress — checks passed, a review approved, a change merged. They are separate columns and never combined into one figure, so a busy run that has verified nothing reads as busy and not as nearly done.

**Handoffs.** Below the rows, the last day's handoffs from the records: a run sent to review, a review sent back for repair, a stopped run put on the development manager's list, a decision carried out, a change merged. Each names both sides, the item, and the time in the operator's local zone with the zone named.

**One piece of work.** The `work` query, opened from any row or handoff, shows one work item in four parts: what was requested — the item's description and the goal it serves; what changed — the commits and files its run produced; why it was accepted — the checks that passed on the reviewed revision and the reviewer's verdict; and what is uncertain — checks that could not run, findings the review left, cost not reported, sources missing. A part with nothing recorded says `not recorded` rather than being left out.

**What it leaves to the factory problems.** The team section says who is doing what; the factory problems section says what is wrong and who moves it. A blocked row links to its problem and never restates it.

## The backlog page

A page of its own, reached from the top status band, that charts the backlog over time, so the operator can see whether work arrives faster than it lands.

**The query.** The read model serves `/api/backlog`: for every work item, open or closed, its id, filed time, closed time or none, priority, labels, kind, who does the work — a developer run, the architect, or a named manager conversation — whether it is ready now or waiting and on what, who filed it where the admission records it, and its parent where it was split from an existing item. Times come from the item's own record, never from its dependency records, whose timestamps are not trusted until the tracker's time zone defect is fixed. An origin that was not recorded is returned as `not recorded`, never guessed. The answer carries the tracker's read time and health as every answer does.

**Parts split from an item.** A part split from an existing item is counted under its parent by default: splitting one item into five is not four new filings. The page shows splits as a series of their own beside the filed series, and one control counts them as filings instead, so the operator can see both readings.

**The page.** Show filed, open, or closed; split by priority, who does the work, label, ready or waiting, or who filed it; time scale hourly, daily, or weekly. Every window is in the operator's local zone and the zone is named on the page. The page works at phone width and in dark mode, and every series is told apart by more than color.

**The chart library.** The page draws with `Chart.js` at one exact version, vendored into `internal/dashboard/assets` and served by the dashboard itself, which keeps the content-security policy at `script-src 'self'` with no CDN. Beside the library a file records its version, where it came from, its checksum, and its MIT license text; changing the version is a reviewed change like any other. No other script from outside this project is admitted by this ruling: another needs a revision of this design naming it.

**The deferral.** Rich timelines stay deferred in general. The backlog chart and the delivery charts are the two exceptions, each because the operator asked for it by name.

## The delivery page

Beside the backlog page, sharing its chart library and layout, a page of the factory's delivery measures after DORA. Each is served by `/api/delivery` as daily and weekly figures in the operator's local zone, says which records it was read from, and shows `not recorded` where its source holds nothing, never zero. Durations are shown as the median and the 90th percentile.

- **Deployments.** A deployment is a change merged to main. Merges per day and per week, from the forge and the run records.
- **Lead time.** From the item's filing to its merge, broken into queued (filing to first run), developing, checks, review and repair rounds, and merging, from the run records. Stage times are never taken from dependency records.
- **Change failure rate.** The share of merged changes followed by any of: a revert of the change; main failing its checks on the merge commit; or a defect filed within 48 hours that cites the change by item or pull request.
- **Recovery time.** How long the factory itself stays broken: from a problem's first occurrence to the record that cleared it, for factory problems of kind broken or stalled, read from the same source records as the factory problems.
- **Rework rate.** The share of merges whose item is a bug rather than a feature, with the share of merges whose item has no kind shown beside it rather than dropped.
- **Hand steps.** Interventions by the operator per merged change, from the recorded hand steps. Steps taken by hand outside the harness are not recorded, so the figure is shown as at least the recorded count, and says so.
- **First pass.** The share of merged changes whose first check run and first review both passed.
- **Tokens per merged change**, by model, from the normalized usage records, with changes whose usage was not recorded counted and shown apart.

## Usage limits across providers and accounts

With one provider and one account, the top band can show the week's share used and the model's own week beside it. With several providers and several accounts, a list of percentages tells the operator nothing at a glance. This section says what the top band's usage line names, where the rest are read, and how a limit nearly reached is told apart from one that is not, for the page, `yoyo status`, and the factory problems. It is settled before a second provider is live so the line never becomes a list.

**Three states, decided by the read model.** Every reading is in exactly one: **at its limit**, where the provider has refused work for that period or the share used is 100%; **near its limit**, at 80% used or more; and **clear** otherwise. The 80% mark is fixed by this design; changing it is a revision of this design. A reading that is `not reported` has no state and is counted apart.

**What the usage line names.** One reading: the limit most likely to stop work first. The order is every reading at its limit, earliest reset first; then every reading near its limit, highest share used first, earliest reset breaking a tie; then every clear reading, ordered the same way. The line shows the first reading in that order as provider, account alias, period, share used, its state in words, and when it resets in the operator's local time with the zone named — for example `Claude Code, main account, week: 84% used, near its limit, resets Thu 09:00 PDT` — followed by how many other readings there are, how many of those are near or at their limit, and how many are not reported, as `and 5 more, 1 near its limit, 2 not reported`. Where every reading is clear the line still names the first, so the operator always sees the nearest limit. Only readings of endpoints a configured role can be routed to are ordered for the line; an endpoint no role can use appears in the list below and never on the line. A reading that is not reported is never placed on the line ahead of one that is reported, and is never shown as 0% used.

**Where the rest are read.** On the page, the usage line opens the full list: one row per reading in the same order, each with every field above and the age of the reading. `yoyo status` prints the same line; `yoyo status --json` carries every reading in the same order with its state. No surface computes an order or a state of its own: each projects what the read model returns.

**Told apart without colour.** The state is a word on every reading wherever it is shown, so the distinction survives a terminal that cannot render emphasis. Colour, where the page uses it, repeats the word and never replaces it.

**Old readings.** A reading more than an hour old shows its age beside it in words, such as `read 3 hours ago`, and keeps its place in the order: an old reading near its limit is still the nearest limit the harness knows of.

**What the factory problems show.** A reading at its limit while work waits on it is a problem of kind degraded, a provider window, as above. A reading near its limit is not a problem: nothing is wrong yet, so it is shown on the usage line and in the list only. A provider serving work whose readings are all `not reported` is a problem of kind degraded, naming the provider and saying its usage is not reported, so a limit the harness cannot see is never mistaken for one that is far away.

**What a reviewer can check.** With readings at 100%, 90% and 40%, the line names the 100% reading. With two readings at 85%, it names the one that resets first. A reading not reported never appears on the line while another is reported, and is never shown as 0%. Every state appears as its word in plain text output. An endpoint no role can be routed to never reaches the line. `yoyo status` and the page show the same reading first.

## Web security *(the repository's web-service conventions, established here)*

The bind address and the allowed hosts are configuration; the defaults are loopback and the loopback Host, and a project that says nothing gets exactly the behavior it had. An explicit non-loopback bind is an operator opt-in that keeps every other rule intact:

- **A token is required on every request**, presented in a header or cookie and never in a URL or a log; a request without one, or with a wrong one, fails closed. Under the loopback default the token is generated per start and reaches the operator as described under the generated token below. **Under a non-loopback bind the token must be supplied** — from the per-project secret store or the environment, never from `.yoyodyne` — because a generated token printed to one terminal is unusable from another device; a non-loopback bind with no supplied token, or a supplied token below the minimum entropy, is refused at load.
- **Host and Origin are validated against the configured address and allowed hosts**, not hard-wired to loopback. A request naming a host outside the configured set fails closed exactly as it did before.
- The content-security policy stays restrictive and CDN-free; the vendored chart library, served by the dashboard itself, keeps it so. All user-, model-, repository-, and Slack-supplied text stays untrusted and escaped; nothing secret, credential, or private-identifier enters the read model or the page.
- **A wider bind widens who can read, never who can act.** The shared token reads and acts on nothing. An act needs the operator's own bound token, described below, and the grant the act requires, at any bind address; a request to act that carries only the shared token is refused.
- **Transport is plain HTTP in V1.** A non-loopback bind sends the token and the page in clear over the operator's network; TLS termination is deferred, and the opt-in is for networks the operator trusts. Binding to a specific interface address is preferred over a wildcard, which reaches every interface the machine has.

## Acting from the dashboard

### Who a session is

The shared token reads and acts on nothing, because it resolves to nobody. A person who acts from the page binds a fourth identifier namespace in the operators mapping, `dashboard_token`, a reference to a per-person token in the keychain (`yoyo-dashboard.<product>.<operator>`) or the token file under the state root, written by `yoyo operator token <name>` and refused to agent processes. A session presenting that token is that person while the tab holds it: proof is possession, the same layer as the forge's push authentication. It reads everything the shared token reads, travels only as a bearer header under the rules above, and pays the same plain-HTTP cost.

For now only the operator may be bound in the `dashboard_token` namespace. Binding any other person there is refused at load, naming this section, until a revision of this design and of the v1 non-goals allows it.

### What the operator may decide from the page

The first act is deciding what waits on the operator. The page lists every entry the needs-a-human line shows under yours to decide — documents waiting for the operator's approval, proposed changes waiting on the operator, and the other decisions the ownership resolver names the operator for — one entry at a time, each with what it is, which role put it there, what approving and what declining would do, and the item it serves where it names one. Each entry offers three controls: approve, decline, and ask about it first. Approve and decline are one click each and take the same act, with the same checks and the same record, as the command line's own approve and decline for that kind of entry. Asking opens a question about that entry to the role that put it there, through the same conversation the terminal reaches, and takes no act; the entry stays waiting until the operator approves or declines it.

The acts already designed here stay, each behind a grant held by the person, never by the token: settling an owed step needs `direct-work`; deciding an amendment needs `decide-amendments`, and an amendment against a product document needs `own-intent` beside it. Approving or declining a document needs the grant the command line's approve command checks for that document's kind, and nothing less.

Further acts the command line offers are added one at a time. Each arrives by a revision of this design that names the act, the grant it needs, and the record it writes; until a revision names an act, the page refuses it.

### What an act records, and what a session may never do

An act records the resolved human — name, the `dashboard` namespace, a digest prefix of the token — on the same record the terminal writes, in place of `by: operator`, so an act taken from the page and the same act taken from the terminal differ only in where they were taken. A session may never direct work beyond the acts named here, never reach anything an agent could, never read more than the shared token reads, and never write anything but the records of those acts; every other method and path is refused.

## Process shape

A standalone dashboard command is the V1 shape. It reads the durable service, exposes health and readiness, and is adopted by the supervisor for start, stop, and observation as described below; it remains useful afterwards for inspection and recovery, and never becomes a second control plane. Its acts are the operator's own decisions, taken through the same code and recorded as the command line records them, which is why taking them from the page does not make it one. Browser availability is never a prerequisite for harness operation.

### Under the supervisor

The dashboard is one part of the product: started, stopped, restarted, and moved onto a new build with the rest of it by the supervisor, as the operating rules require. Because a dashboard left on an old build has read records a newer build wrote and refused them, the supervisor takes up a dashboard only when its build identity matches the product's build and its start identity matches the supervisor's own record of that start. Moving onto a new build starts the new dashboard, waits for it to be ready as defined below, and only then stops the old one. A dashboard whose build differs from the product's is a factory problem of kind degraded, a part on a stale build, until it is replaced.

### The generated token

This governs only the token generated for a loopback bind. A token supplied for a non-loopback bind, and every person's `dashboard_token`, are never written here and are unchanged by it.

- **Where.** The file `dashboard/token` under the product's state root; never a worktree, `.yoyodyne`, a run record, a log, or anything the harness hands an agent.
- **Permissions.** The `dashboard` directory is created with mode `0700` and the file with mode `0600`, both owned by the user the dashboard runs as.
- **Unsafe files.** The directory and the file are opened without following a symbolic link. An existing directory or file that is a link, is not of the expected type, is owned by another user, or is more widely readable than these modes is refused, never repaired in place.
- **Writing.** The token is written to a new file created exclusively in the same directory, flushed to disk, renamed over `token`, and the directory flushed, so a reader sees the old token or the new one and never part of one.
- **Before ready.** The dashboard reports ready only after the file is published and the supervisor has made one request authenticated with that token and had it accepted. Until then it serves nothing but its health answer.
- **Rotation.** A new token is generated only when a new dashboard process starts. The supervisor reattaching to a dashboard that survived keeps its token.
- **Cleanup.** A clean stop removes the file. A start replaces any file left by a stop that was not clean.
- **When it cannot be done safely.** If the file cannot be written as above, the dashboard does not become ready, serves nothing, and is a factory problem of kind broken naming the path and the reason. It never falls back to printing the token in a log, in status, or anywhere else.
- **Reading it.** `yoyo dashboard token` prints it for a person and is refused to agent processes, as `yoyo operator token` is. `yoyo status` names the file and that command, never the value.
- **A dashboard started by hand.** A dashboard a person starts from a terminal prints its token to that terminal, which is that person's own, and also publishes the file. A dashboard the supervisor starts prints none, because its output goes to logs.
- **Alternatives rejected.** Showing the value in `yoyo status` was rejected because status output reaches conversations and agents. Posting it to Slack was rejected because a channel is read by others and keeps the message after the token has rotated.

**What a reviewer can check.** No log, status output, run record, conversation, channel post, or URL contains the token value. A symbolic link, a wrong owner, or wider permissions at the path stop the dashboard becoming ready and raise a factory problem of kind broken. Readiness is never reported before the file exists and an authenticated request has succeeded. Restarting the supervisor while the dashboard keeps running keeps the token; replacing the dashboard changes it. A build change replaces the dashboard with no moment at which no dashboard is ready. A configured token never appears in the file.

## Deferred, deliberately

Deep drilldowns; rich timelines and custom historical windows, beyond the backlog and delivery charts above; readiness and conflict visualizations; push infrastructure; multi-account pool controls; acts beyond those named under acting from the dashboard, each to be added by its own revision.
