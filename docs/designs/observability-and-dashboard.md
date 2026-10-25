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
      at: 2026-10-21T08:20:48.61508Z
      reason: 'yoyodyne-ifd.432.37 and yoyodyne-ifd.432.29: the cost entry now uses the four kinds of cost the provider usage and cost design defines (reported, estimated, unknown, comparison only), links that design, and drops the unattributable class, with a figure tied to no work item counted under its role; a new section says how usage limits across several providers and accounts are shown on the top line, in yoyo status, and in the factory problems. Also replaces the term ''after DORA'' with ordinary words (amendment-ff231c2b) and spells color one way throughout. Rewritten from main at c5c190e8 after the earlier copy (document-897.1) was refused for linking the provider adapters design, which states no cost rule.'
    - action: amended
      by: architect
      at: 2026-10-21T09:51:13.651391Z
      reason: 'yoyodyne-ifd.432.28.1: The read model gains a section on work item references. It covers how a reference to an item in a role''s text is kept (the text as written, plus the full id and its byte range), found (one matcher that never stops early and never searches tracker titles), and shown (only the id changes, followed by the current priority, labels and title, always built from the stored text). It also covers older text, missing items, an unreadable tracker, and Slack using the same rendered text. Examples are included, and the design names the parent clause the Lead Product Manager would need to amend for short ids in older text. Everything else is unchanged from main at a5e9aca6. Resent after the word check refused one joined word.'
    - action: amended
      by: architect
      at: 2026-10-21T19:48:00.693684Z
      reason: 'Approved amendment-77180439 from the developer building how work items are shown to a person: the work item references section now passes over text in backticks, link targets, paths and names joined by underscores; treats a full id with no digit and no dot as a reference only when the tracker holds it or the harness recorded it as a work item id; and cuts a shown title at 200 bytes. The paragraph on the parent item''s wording is replaced to match the Lead Product Manager''s narrowing of 21 October. Everything else is unchanged from main at 5825a633.'
    - action: amended
      by: architect
      at: 2026-10-24T20:50:34.368986Z
      reason: 'Approved amendment-c85bf4cf from the developer building the loopback-only dashboard (yoyodyne-ifd.434.33): the web-security section now says the dashboard listens only on a loopback address and refuses any other, with allowed_hosts naming further host names for that same listener, matching the Lead Product Manager''s ruling that the dashboard stays on this machine (yoyodyne-ifd.432.40). Only this section changes; written from main at 4efea121. Resent after the word check refused one joined word.'
    - action: amended
      by: architect
      at: 2026-10-25T12:33:08.776621Z
      reason: 'Approved amendment-9f5999da from the developer measuring what each management pass and review accomplished (yoyodyne-ifd.432.47): the dashboard section gains the reading of what the spend accomplished, opened from the spend box, served at /api/accomplishment and in yoyo status --spend, rebuilt every 15 minutes. Only this section changes; written from main at e6e96049.'
    - action: amended
      by: architect
      at: 2026-10-25T14:03:08.935488Z
      reason: The read model's list of queries gains the accomplishment query that the dashboard section already describes, built every 15 minutes for a date range the operator chooses, and names it as the third reading, beside the backlog and delivery charts, that serves a range the operator chooses. This is the first of three passages that reconcile the design with its accomplishment reading for the reviewer's finding (yoyodyne-ifd.437.61); the backlog page's note and the deferred section follow. Only this section changes; the work item references subsection is copied unchanged. Written from main at ddef0b1e.
    - action: amended
      by: architect
      at: 2026-10-25T15:50:32.892911Z
      reason: The backlog page's note on what stays deferred now names three exceptions rather than two, adding the reading of what the spend accomplished, so it agrees with the read model's list of queries that landed this morning. This is the second of three passages reconciling the design with its accomplishment reading for the reviewer's finding (yoyodyne-ifd.437.61); the deferred section follows once long documents can be revised. Only this section changes. Written from main at 8a4317dd.
    - action: amended
      by: architect
      at: 2026-10-25T18:01:23.940497Z
      reason: 'Approved amendment-9f56f450: the dashboard section opens with a summary for the operator, above the top status band and at the head of yoyo status, giving five answers in order (what landed, code apart from documents; what is in progress with its newest result; what is blocked, with who it waits on and the next step, and runs that ended without landing grouped by cause; what needs the operator; what it cost), built from the standing, throughput and spend readings, with run ids and stop causes behind a detail button or flag. The section list now counts eight sections. Only this section changes; the read model''s query list gains its line in a following revision. Written from main at 0ef00fa8.'
    - action: amended
      by: architect
      at: 2026-10-25T20:20:02.180936Z
      reason: 'Approved amendment-9f56f450, second half: the read model''s list of queries gains the summary query that the dashboard section''s opening summary already describes, built from the standing, throughput and spend answers and keeping no figure of its own. Only the new list entry changes; everything else in this section, including the work item references subsection, is copied unchanged. Written from main at b877e026.'
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
      at: 2026-10-21T08:20:48.61508Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1404, for document-1404.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 10
      by: harness
      at: 2026-10-21T09:51:13.651391Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1409, for document-1409.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 11
      by: harness
      at: 2026-10-21T19:48:00.693684Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1436, for document-1436.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 12
      by: harness
      at: 2026-10-24T20:50:34.368986Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1644, for document-1644.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 13
      by: harness
      at: 2026-10-25T12:33:08.776621Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1701, for document-1701.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 14
      by: harness
      at: 2026-10-25T14:03:08.935488Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1707, for document-1707.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 15
      by: harness
      at: 2026-10-25T15:50:32.892911Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1711, for document-1711.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 16
      by: harness
      at: 2026-10-25T18:01:23.940497Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1730, for document-1730.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 17
      by: harness
      at: 2026-10-25T20:20:02.180936Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1746, for document-1746.1 under the automatic approval policy
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
- provider cost, with every figure one of the four kinds of cost that [provider usage and cost](provider-usage-and-cost.md) defines: reported, estimated, unknown, and comparison only. No total adds two kinds together: each total shows its reported subtotal, its estimated subtotal, its comparison figure apart from both, and its unknown share as a count of calls and a percentage. Unknown renders as unknown, never as zero, exactly as the cost surfaces already hold. Which work a figure belongs to is kept apart from its kind: a figure tied to no work item is counted in the total and shown under its role and conversation, never dropped, and is still one of the four kinds;
- capacity: per configured account alias, the model, active invocation count, state — healthy, waiting, usage-limited, or **capacity-blocked** — the recorded reset time, and recent capacity events. The capacity-blocked state exists as a read-model query, carried in `yoyo status --json` as `standing.capacity_blocked`, derived from the run records and the usage-limit log, naming each parked or blocked run and each refused conversation with its reset time, the time already waited, and a remedy.
- usage limits: the `usage_limits` query described under usage limits across providers and accounts below;
- what the spend accomplished: the `accomplishment` query described under what the spend accomplished below, built every 15 minutes rather than on each poll, for a date range the operator chooses. With the backlog and delivery charts it is one of the three readings that serve a range the operator chooses rather than a fixed window;
- factory problems: everything the line is not doing that the goals say it should, where nothing in the ordinary flow fixes it by itself, derived from the records above and from nothing a role writes. Each problem carries a kind from a closed set of four — **broken**, the harness failed at its own doing: a firing refused before its first turn, a listing the tracker returned undecodable, a part the supervisor left down, a page that cannot build; **stalled**, nothing moving that should: a stall record, slots idle over ready work, a recorded decision unattempted, an approved change awaiting its resume, a queued merge whose checks cannot be read; **blocked**, a decision somebody owes is unmade: stoppages awaiting the development manager, amendments awaiting their owner, rulings made and not landed, items the docket window has not shown; **degraded**, running but worse: a stage the bound stopped under load, a provider window or a capacity-blocked run, lost races replaying, a part on a stale build. A deep queue, a run in progress, and a green queued merge are not problems. Each problem has an identity — its kind, its cause, and its subject — onto which every occurrence folds, and carries: what is wrong, in a sentence; since when, and the latest occurrence; its count; its cost so far as the records hold it — runs spent on it, decisions waiting behind it, hours the line has stood, and money where the records hold it, each money figure in its kind of cost as every cost is; its mover, in the vocabulary the attention line already uses; the admitted item that fixes it, found by the item's own citation of the problem's source record, or the plain statement that none is admitted yet; and the source records it was derived from. The stop cause every stopped run records (yoyodyne-ifd.428.32) and the run-stop inventory (yoyodyne-ifd.429.28) are what classify a stopped run into a kind; until they land, a stopped run is classified by the fields it has, and one nothing classifies is a problem of kind stalled with its cause named as unclassified, which is itself a finding.
- **Who owns each entry.** The read model calls the resolver of [ownership and the operator](ownership-and-the-operator.md) once per entry and carries its owner, reason, remedy, capability, and hand; the dashboard, the Slack sink, `yoyo status`, and every conversation's context project that answer and derive no owner of their own, and the needs-a-human line prints its two heads, yours to decide and a role's decision that needs your hand, from it.
- program managers: per configured instance, its name, lane, the status derived as [program-manager](program-manager.md) specifies — blocked from an open cited request, stale from the pass records against the instance's schedule, working otherwise — its open restart requests, and its current lane report, served whole one instance at a time. Where an instance's lane report cites a problem by id, the read model joins the instance's judgment onto the problem — its remedy, its account of the cause, the count it corrected, and the item it admitted in its lane — and the instance's own status and blockers are problems of kind blocked or stalled with the instance as subject.
- the team at work and one piece of work, the `team` and `work` queries described under the team at work below;
- the backlog and the delivery measures, the `/api/backlog` and `/api/delivery` queries described under the backlog page and the delivery page below;
- the summary that opens the dashboard and `yoyo status`: the `summary` query described under the summary that opens the page below, built from the standing, throughput and spend answers this list already gives and keeping no figure of its own, so every figure in it equals the same figure where that answer is shown;
- the rendered text of everything a role wrote that names a work item, as described under work item references below.

The model also serves what Slack's governed behavior already needs — per-item status for thread reactions, directive lifecycle marks, item titles for thread openers, and the rendered text of every work item reference — so the sink presents these derivations instead of computing them.

**Honesty is a property of the model, not a style of the page.** Every metric identifies its time window; every source carries health; every cost total shows its kinds apart and names its unknown share. A stale or unreadable source degrades the answer visibly rather than narrowing it silently — the same rule the freshness line and the cost floors already follow.

**The attention line is a slice of the problems.** Every problem names its mover, and what `yoyo status`, the page, and the channel put in front of the operator as needing a human is the problems whose mover is the operator, and nothing else: an entry moved by a role or the harness is the line working, shown to the operator and asked of the operator never. Two requests stay on that line that are not problems, because the operator holds them by design: a proposal against the brief or the goals that moves fundamental intent, per [who-approves-a-change-to-intent](../decisions/who-approves-a-change-to-intent.md), and a directive awaiting the operator's answer. Who moves a problem is never decided here: every problem carries the owner, reason, and remedy that [the ownership resolver](ownership-and-the-operator.md) returns for its kind, so the needs-a-human slice is exactly the problems whose owner is the operator for a reason on the closed list, and a problem whose remedy only needs the operator's hand is shown under that head instead, as the harness's defect.

### Work item references

Work-item display formatting (yoyodyne-ifd.432.28) asks that a work item named to a person be shown with its current priority, labels, and title from the tracker. Roles name items by id inside their own prose. A surface that finds those ids again by searching text it has already rendered can pick the wrong id, such as one inside a title it added, and can delete the words around it; the first attempt at that item did both. This section fixes how a reference is kept, found, and shown, so every surface shows the same thing and nothing around a reference changes.

**What is stored.** When the harness records text a role wrote — a note, a report, a reply, a pass account, a lane report, a digest — it keeps the text exactly as written and, beside it, a `references` list. Each entry is the item's full id and the start and end byte of the id in the stored text, covering the id and nothing else. The list never holds a title, a priority, or labels: those are read from the tracker each time the text is shown. Nothing a role wrote is changed to make a reference.

**How a reference is found.** One matcher, the same when text is recorded and when older text is read:

- **A full id** is the project's tracker prefix and a hyphen, where the character before it is not a letter, digit, hyphen, or dot. It runs to the first character that is not a letter, digit, hyphen, or dot, and a dot it ends with is not part of it, so a sentence's full stop, a comma, a semicolon, a colon, or a closing parenthesis after it never is. A match never stops early at a shorter id: `ex-7.2s` is the id `ex-7.2s`, never `ex-7.2`. A full id is a reference whether or not the tracker holds it, except as the next point says; the tracker decides only how it is shown.
- **A full id with no digit.** A full id whose part after the prefix's hyphen holds no digit and no dot, such as `ex-style`, may be an ordinary hyphenated word rather than an item. It is a reference only when the tracker holds an item with exactly that id, in the tracker read the matcher runs against, or when the harness itself recorded it in a field that holds a work item id, such as the item a run was started for. Otherwise it is ordinary text: left as written, never shown as not in the tracker, and not counted in `unresolved`. An item whose id has no digit, such as `ex-gvm`, is still a reference wherever the tracker holds it.
- **A short id**, written without the prefix, such as `432.28` or `ifd.432.28`, is a reference only if it contains a digit, stands between the same boundaries, and exactly one item the tracker holds has an id that ends in it just after the prefix's hyphen or a dot. This is decided when the text is recorded, against the tracker read then, and the stored entry holds the full id. A short id with no digit is never matched, so an ordinary word is never taken for an item. A short id that matches no item, or more than one, is left as written and counted in the answer's `unresolved` figure.
- **Where no reference is looked for.** Four kinds of text are passed over whole, because a person reads or types them exactly as written. The first is text set in backticks, whether one pair or a fenced block. The second is the target of a link: the part of a Markdown link in parentheses, or a bare web address; the link's visible text is searched as any other text is. The third is a path: a run of characters with no space in it that holds a slash, such as a branch name or a folder name. The fourth is a name joined by underscores: the character just before or just after the id is an underscore. An id inside any of these is not a reference, is left as written, and is not counted in `unresolved`. A role that wants an item shown with its title names it outside these.
- **Text read from the tracker is never searched.** An id inside an item's title is part of the title, never a reference.

**How it is shown.** The read model renders each text from the stored text and its references, and never from an earlier rendering. In text a role wrote, each reference's bytes are replaced with the full id, followed by the item's current priority, labels, and title in parentheses: `ex-7 (P1, reliability: Retry the merge)`, or `ex-7.2 (P2: Name the stop)` for an item with no labels. Every other byte of the text is kept as it was, including a title the role wrote in its own words beside the id. A list the harness builds itself from tracker items, with no text of a role's in it, shows each item in the parent item's form: `(P1, reliability) Retry the merge (ex-7)`.

In both forms a title is cut to its first 200 bytes, at the end of a whole character, and ends with `…` where it was cut. The tracker's own title is not changed, and a surface that makes the id a link leads to the whole title.

A rendering depends on nothing but the stored text, its references, and one tracker answer. Rendering twice with the same tracker answer gives the same bytes; a later rendering with a newer answer changes only the parentheses after each reference.

**Older text.** Text recorded before references were kept has no list. The read model runs the part of the matcher that finds full ids on it each time it reads it, with the same places passed over and the same rule for a full id with no digit, stores the result nowhere, and never rewrites the note, report, or reply it came from: notes are never rewritten. A reference found this way is shown exactly as one found when its text was recorded, and the answer marks it `found_when_read` so a reviewer can tell the two apart. A short id in older text is never resolved, because whether it named exactly one item when it was written cannot be known now: it is left as written and counted in `unresolved`.

**Freshness, missing items, and an unreadable tracker.** Every answer renders against one tracker read, the one whose time and health the answer already carries, and no surface renders with a title kept from an earlier read. An id the tracker does not hold renders as `ex-9 (not in the tracker)`. When the tracker could not be read, every reference in the answer renders as `ex-7 (tracker unreadable at 09:40 PDT)`, the time being the answer's observation time in the operator's local zone with the zone named, and the tracker is shown as an unhealthy source as any other is. In neither case is a reference dropped or the text around it removed.

**Every surface takes the rendered text.** The dashboard, `yoyo status`, every conversation's context, the digests and lane reports shown to a person, and the Slack sink show the read model's rendered text and search no text of their own. A surface may make the id a link using the `references` list, and changes no words in doing so. The Slack sink posts the rendered text as it stands when the message is posted, and has no formatter of its own; a posted message is not edited when the tracker later changes, and a later message is rendered afresh.

**Examples.** For a project whose tracker prefix is `ex`, where the tracker holds `ex-7` (P1, labelled reliability, titled Retry the merge), `ex-7.2` (P2, no labels, titled Name the stop), `ex-12` (P0, labelled bug, titled Fix ex-7's timeout), and `ex-gvm` (P2, no labels, titled Name the slot), and holds no `ex-9`, `ex-7.2s`, or `ex-style`:

| Case | Stored text | Shown |
|---|---|---|
| Semicolon and full stop | `Blocked by ex-7; see ex-7.2.` | `Blocked by ex-7 (P1, reliability: Retry the merge); see ex-7.2 (P2: Name the stop).` |
| Lowercase letters running on | `ex-7.2s are done, then ex-7 lands` | `ex-7.2s (not in the tracker) are done, then ex-7 (P1, reliability: Retry the merge) lands` |
| An id inside a title | `Waiting on ex-12.` | `Waiting on ex-12 (P0, bug: Fix ex-7's timeout).` The `ex-7` in the title is not a reference, and no later rendering finds it. |
| Several references, and a role's own title | `Retry the merge (ex-7), then ex-7.2 and ex-12.` | `Retry the merge (ex-7 (P1, reliability: Retry the merge)), then ex-7.2 (P2: Name the stop) and ex-12 (P0, bug: Fix ex-7's timeout).` |
| A renamed item | `Retrying ex-7 now.` | Before: `Retrying ex-7 (P1, reliability: Retry the merge) now.` After `ex-7` is retitled Retry the merge after a refusal and labelled bug as well: `Retrying ex-7 (P1, bug, reliability: Retry the merge after a refusal) now.` |
| Not in the tracker | `See ex-9.` | `See ex-9 (not in the tracker).` |
| Tracker unreadable | `Blocked by ex-7.` | `Blocked by ex-7 (tracker unreadable at 09:40 PDT).` |
| Older text with a short id | `Waiting on 7.2.` recorded before references were kept | `Waiting on 7.2.`, unchanged, and counted in `unresolved` |
| Rendering again | The fourth row's stored text, rendered twice with the same tracker answer | The fourth row's output both times, byte for byte; the second rendering starts from the stored text, so it never adds a second set of parentheses. |
| A branch name | `Kept on branch work/ex-7/c4bed934.` | Unchanged: the id is part of a path. Counted nowhere. |
| A command to type | Run `yoyo run ex-7` again. | Unchanged: the id is inside backticks. Counted nowhere. |
| A name joined by underscores | `Set ex-7_timeout lower.` | Unchanged. Counted nowhere. |
| A hyphenated word, and an id with no digit | `An ex-style check before ex-gvm.` | `An ex-style check before ex-gvm (P2: Name the slot).` `ex-style` has no digit and the tracker does not hold it, so it is ordinary text. |

An item whose title runs past 200 bytes is shown with its first 200 bytes, ending at a whole character, followed by `…`.

**The parent item's wording.** On 21 October the Lead Product Manager narrowed the parent item to every work item reference the harness can resolve, with a short id in text recorded before references were kept left as written and counted. This section meets that wording for every full id, and for every short id in text recorded since references were kept. The places passed over above, and a full id with no digit that the tracker does not hold, are not references at all, so leaving them as written meets it too.

**Alternatives rejected.** Writing the title into the stored text when it is recorded, which goes stale and would need notes rewritten to correct. Searching the rendered text again when it is shown later, which is how the first attempt picked an id inside a title and removed prose. Marking references inside the text with brackets or other characters, which changes what the role wrote and shows those characters on every surface that does not interpret them. Resolving short ids in older text against today's tracker, which can name an item that did not exist when the text was written. Treating every prefixed run of characters as a reference whatever surrounds it, which shows branch names, commands meant to be typed, and ordinary hyphenated words as items not in the tracker, against the plain-language goal. Leaving titles uncut, which lets one long title push the rest of a line off a narrow screen or a Slack message.

**What a reviewer can check.** Each example above gives the output shown. No byte outside a reference differs between the stored text and its rendering. Nothing renders from an earlier rendering. No note, report, or reply is rewritten. A missing item and an unreadable tracker render as shown above, never as a blank or a removed reference. An id in backticks, in a path, in a link's target, or joined by underscores is shown unchanged and counted nowhere. A full id with no digit that the tracker does not hold is shown unchanged and counted nowhere. No title shown runs past 200 bytes plus the `…` that marks the cut. The Slack sink holds no formatter of its own.

## The dashboard *(the sections yoyodyne-ifd.141 builds)*

**The summary that opens the page.** The page opens with a short summary for the operator, above the top status band, and `yoyo status` opens with the same summary, printed from the same answer. It answers five questions, always in this order, each in a sentence or a short list:

1. **What landed.** Changes merged in the last 24 hours and in the last seven local days, with code changes counted apart from document publications, so publishing documents never stands in for delivering software.
2. **What is in progress.** Each run in progress, with its work item and the newest result it has recorded: a check passed or failed, a review returned, a merge queued.
3. **What is blocked.** Each blocked entry, with who it waits on and the next step, exactly as its factory problems entry carries them; and the runs that ended without landing in the same two windows, grouped by why they stopped, each group with its count.
4. **What needs you.** The needs-a-human line, exactly as the attention line slices it from the factory problems. Where nothing needs the operator, the summary says so in those words.
5. **What it cost.** The spend over the same two windows, every figure in its kind of cost, kinds never added together, and the unknown share counted, as the spend box shows it.

The read model builds the summary from answers it already gives: the standing that `yoyo status --json` carries, the throughput reading, and the spend reading. It keeps no figure of its own, so every figure in the summary equals the same figure in the section below it. It names each work item by its title with its id, as the work item references section above renders them, and keeps run ids and stop causes behind a detail button on the page and a `--detail` flag on `yoyo status`, so the first thing the operator reads is in ordinary words. Every time it shows is in the operator's local zone with the zone named, and a source that could not be read is named in the summary as every answer names it.

**Alternatives rejected.** A page of its own for the summary, which hides it from the first look at the dashboard. A summary each surface composes for itself, which lets the page and `yoyo status` disagree about the same records. Run ids and stop causes in the summary itself, which asks the reader to look up words a person should not need. Counting document publications with code changes, which lets publishing documents look like delivered software.

**What a reviewer can check.** The summary is the first thing on the page and in `yoyo status`, and gives the five answers in the order above. For the same answer, the page and `yoyo status` show the same summary. Every figure in it equals the same figure in the section below. Code changes and document publications are counted apart. No run id or stop cause appears without the detail button or `--detail`. Where nothing needs the operator, the summary says so.

A locally hosted page with eight visually distinct sections: the summary described above; a top status band (active agents, queued work, attention count, throughput, landed today and this week, and the one-line usage reading described under usage limits across providers and accounts below); a spend box directly under it, showing the last 24 hours rolling and the last seven local days by kind — runs, conversations, branch reviews, exchanges, side threads — with a 30-day daily listing behind it, every figure in its kind of cost and every floor marked, and a link opening what that spend accomplished, described below; the team at work, one row per role and developer slot, as described under the team at work below; a compact pipeline view showing where work accumulates or blocks; throughput over the same two windows, reporting endings only in the run history's own words, with cost left to the spend box; the factory problems from the model's problems query, grouped by kind with broken first, each entry its sentence, its age and count, its cost with every unknown marked, its mover as a word, its fixing item as a link or the words `none admitted yet`, and the factory-flow program manager's remedy where its report cites the entry, with that instance's lane-report summary as the section's own summary; provider capacity is folded in as entries of kind degraded and has no section of its own; and the program managers, each instance with its name in full, its lane, its status as blocked, stale, or working, and a link opening its lane report, from the model's `program_managers` query per [program-manager](program-manager.md). Attractiveness is an acceptance requirement — hierarchy, typography, spacing, accessible color, responsive layout, useful empty, loading, and error states, a small amount of purposeful motion — and polish must never imply precision the model did not claim. Simple polling; push infrastructure is deferred unless polling proves inadequate. A degraded source gets a prominent indicator while the page stays usable. Restarting the dashboard changes no workflow state and loses no history, because the history lives in the durable records, not in the page.

Two further pages sit beside the main page: the backlog page and the delivery page, each reached from a link in the top status band and each linking back. They are pages of their own rather than sections, so the main page stays one screen of what is happening now.

The dashboard is a projection, never an engine: it owns no workflow, conversation, provider, or configuration state, and keeps no work state of its own. The only writes it makes are the operator's own decisions, described under acting from the dashboard below, each taken through the same code the command line uses and recorded exactly as the same act from the command line is recorded. The page adds no decision of its own and offers no act the command line does not already offer. This is the boundary [the v1 non-goals](../product/goals/v1-non-goals.md) record since the operator's change of 11 October, and this design binds to it. The summary, the team section, the usage limits panel, the backlog page, the delivery page, and the reading of what the spend accomplished are reading only, and add no act.

### What the spend accomplished

The spend box shows what the harness spent; this reading shows what that spend led to, so the operator can see which spend moved work and which did not. It is the read model's `accomplishment` query, served at `/api/accomplishment` and opened from the spend box, and `yoyo status --spend` prints the same figures from the same answer. It reads the run records, the pass and conversation records, the tracker, and the recorded costs, writes nothing, and adds no record.

**How often it is built.** The read model builds this answer every 15 minutes rather than on each poll, because it reads every pass and review in its window and its figures change slowly. Every answer names the time it was built and the health of each source it read, as every answer does. An answer more than 30 minutes old is shown as out of date, never as current.

**Each management pass and conversation turn, by what it led to.** Every turn of the Lead Product Manager, the architect, the development manager, and each program manager instance is in exactly one of four classes, tested in this order:

1. **repeated an earlier one**: an earlier turn of the same role had the same inputs and the same outcome;
2. **unblocked delivery**: it admitted, released, decided, or repaired work that then moved on — a run started, a check passed, a review returned, or a change merged;
3. **changed nothing**: the tracker and the harness's records stand after it exactly as they stood before;
4. **changed something, no work has moved yet**: every other turn.

The fourth class exists so the shares always add up to the whole: a turn that wrote something and has not yet moved work is counted, never dropped. The class is worked out from the records and never from what a role says about its own turn. For any date range, the answer gives, per role, its spend, its number of turns, and the share in each class.

**Each review, by whether it prevented a defect.** A review that returned a finding, followed by a revision from the developer that a later review accepted, counts as a defect prevented. A review that approved the change as it came counts as approved unchanged. A review that returned a finding with no later revision is shown apart, as not yet fixed.

**Each completed item, by its full cost.** A completed item is one closed with its change merged. Its cost is in four parts that never count the same figure twice: development, its first developer attempt; review, its first review; retries, every later developer or review attempt, including runs started again after a stopped run; and management, its share of the management turns that named it, as the work item references section above records names. A turn that names several items is split equally among them. A turn that names none is counted under its role and conversation, never dropped, as every figure tied to no work item is. The four parts add up to the item's total.

**How it is shown.** Totals by role and by week, and the cost per completed item, with every week in the operator's local zone and the zone named. Every money figure is one of the four kinds of cost that [provider usage and cost](provider-usage-and-cost.md) defines, kinds are never added together, and the unknown share is counted, exactly as the spend box shows them. The week of October 17 to 24 is the first week shown, read from records that already exist.

**Alternatives rejected.** Building the answer on every poll, which reads every pass in the window each few seconds for figures that change slowly. Letting a role mark its own turn as useful, which turns the measure into what roles say about themselves. Three classes only, which leaves a turn that changed records but has moved no work in none of them, so the shares would not add up. Giving a turn that names several items its whole cost on each, which counts the same money more than once.

**What a reviewer can check.** Every turn in the range is in exactly one class, and the shares add up to 100 percent. A review counts as preventing a defect only where a later revision followed its finding. No item's cost counts a figure twice, and its four parts add up to its total. No total adds two kinds of cost. Every answer names when it was built, and one more than 30 minutes old is shown as out of date. The page and `yoyo status --spend` show the same figures for the same answer.

## Usage limits across providers and accounts

The top status band shows how much of the providers' usage limits is used, so the operator can see whether work is about to be held. With one account that is a short reading. With several providers and several accounts a list of percentages stops saying anything, so the read model chooses one reading for the top line and keeps the rest one click away.

**What is read.** The read model's `usage_limits` query returns one entry for each limit each configured account reports: the provider; the account alias; the limit as the provider names its period, such as a five-hour limit, a weekly limit, or a weekly limit for one model; the share used, as a measured percentage or `not recorded`; the time it resets, in the operator's local zone with the zone named; the time the reading was taken; and the runs and conversations held on it. It reads only what the adapters and the usage-limit log already record, and adds no record.

**Four states, decided by the read model.** Each limit is in exactly one:

- **limit reached**: the provider refused work because of it, or reports it fully used;
- **near limit**: 80 percent used or more;
- **fine**: under 80 percent used;
- **not recorded**: the provider reported no share, or the latest reading is more than an hour old.

The state is always printed as its words beside the figure, so the difference survives a terminal that shows no color; color is a second signal and never the only one. The 80 percent mark is one number every surface shares; changing it is a revision of this design, not a setting.

**What the top line names.** While one account is configured, the top line shows that account's limits as it does today: the week's share used, with the model's own weekly limit beside it where the provider reports one. Once a second account is configured, the top line names one limit, the one most likely to hold work, chosen in this order:

1. a reached limit that is holding work: the one holding the most runs and conversations, and among those the one that resets latest;
2. otherwise, the near limit with the largest share used;
3. otherwise, the limit with the largest share used.

Any remaining tie goes to the account alias in alphabetical order, so the same records always give the same line. The line names the provider, the account, the limit, the share used, the state, the reset time, and how many further limits there are, for example: `Codex, account b: weekly limit 86% used, near limit, resets 14:00 PDT — 5 more limits`.

**Where the rest are read.** Selecting the top line opens the usage limits panel: one row for each account, grouped by provider, each listing its limits with the share used, the state, the reset time, and the age of the reading, with reached limits first. `yoyo status` prints the same top line and, under it, one line for each limit that is near or reached; `yoyo status --json` carries the whole list. Every surface shows the read model's choice and order and works out none of its own.

**What reaches the factory problems.** A reached limit that is holding work is already a problem of kind degraded, as the capacity entries above are, and its entry names the limit and its reset time. A near limit is not a problem and asks nobody anything. An account with work assigned whose limits read `not recorded` is a problem of kind degraded, because the line cannot tell when that work will be held. Nothing here is put to the operator: a reached limit is waited out, and moving work to another account is the routing [provider adapters and execution endpoints](provider-adapters-and-endpoints.md) already does.

**Alternatives rejected.** Every percentage on the top line, which the operator said on 30 September stops being useful once there are several providers. An average across accounts, which hides the one limit that is holding work. A mark set separately by each surface, which would let two surfaces disagree about the same limit.

**What a reviewer can check.** The top line is the same on the page and in `yoyo status` for the same records. Each state appears as its words, not only as a color. A limit with no share reported reads `not recorded`, never 0%. A near limit never appears in the factory problems or on the needs-a-human line. Every reset time names its zone.

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

**The deferral.** Rich timelines stay deferred in general. The backlog chart, the delivery charts, and the reading of what the spend accomplished are the three exceptions: the first two because the operator asked for them by name, the third because measuring what each management pass and review accomplished (yoyodyne-ifd.432.47) asks for its figures over any date range the operator chooses.

## The delivery page

Beside the backlog page, sharing its chart library and layout, a page of the factory's delivery measures, modeled on the measures software teams commonly use for how often and how safely they ship. Each is served by `/api/delivery` as daily and weekly figures in the operator's local zone, says which records it was read from, and shows `not recorded` where its source holds nothing, never zero. Durations are shown as the median and the 90th percentile.

- **Deployments.** A deployment is a change merged to main. Merges per day and per week, from the forge and the run records.
- **Lead time.** From the item's filing to its merge, broken into queued (filing to first run), developing, checks, review and repair rounds, and merging, from the run records. Stage times are never taken from dependency records.
- **Change failure rate.** The share of merged changes followed by any of: a revert of the change; main failing its checks on the merge commit; or a defect filed within 48 hours that cites the change by item or pull request.
- **Recovery time.** How long the factory itself stays broken: from a problem's first occurrence to the record that cleared it, for factory problems of kind broken or stalled, read from the same source records as the factory problems.
- **Rework rate.** The share of merges whose item is a bug rather than a feature, with the share of merges whose item has no kind shown beside it rather than dropped.
- **Hand steps.** Interventions by the operator per merged change, from the recorded hand steps. Steps taken by hand outside the harness are not recorded, so the figure is shown as at least the recorded count, and says so.
- **First pass.** The share of merged changes whose first check run and first review both passed.
- **Tokens per merged change**, by model, from the normalized usage records, with changes whose usage was not recorded counted and shown apart.

## Web security *(the repository's web-service conventions, established here)*

The dashboard listens only on a loopback address, such as 127.0.0.1 or ::1. The address is configuration, and the default is loopback. A configured address that is not loopback, including a wildcard that reaches every interface, is refused when the configuration is loaded, and the refusal names this section. The page takes the operator's own decisions, and [the v1 non-goals](../product/goals/v1-non-goals.md) keep it local and not hosted. The Lead Product Manager ruled that it therefore stays on this machine (yoyodyne-ifd.432.40). Reaching it from another device is not offered in v1.

- **A token is required on every request**, presented in a header or cookie and never in a URL or a log. A request without one, or with a wrong one, fails closed. The token is generated each start and reaches the operator as described under the generated token below.
- **Host and Origin are validated against the loopback address and `allowed_hosts`.** `allowed_hosts` names further host names under which the same loopback listener is reached, such as a name the machine maps to 127.0.0.1. It never changes the address the dashboard listens on. A request naming a host outside that set fails closed.
- The content-security policy stays restrictive and loads nothing from a CDN; the vendored chart library, served by the dashboard itself, keeps it so. All user-, model-, repository-, and Slack-supplied text stays untrusted and escaped; nothing secret, credential, or private-identifier enters the read model or the page.
- **The shared token reads and acts on nothing.** An act needs the operator's own bound token, described below, and the grant the act requires. A request to act that carries only the shared token is refused.
- **Transport is plain HTTP on the loopback interface.** Nothing the page sends leaves the machine, so no encryption is added in v1.

**Alternatives rejected.** Keeping the setting, added on 19 September, that let the operator bind an address other machines can reach. A page that takes the operator's decisions, reachable over plain HTTP from a network, would send his token in clear and amount to the hosted control plane the non-goals exclude. Making it safe would need encryption and a way to prove who is connecting from another machine, which v1 does not take on. A configurable list of trusted networks has the same problem.

**What a reviewer can check.** A configured address that is not loopback is refused at load, naming this section. `allowed_hosts` changes which Host and Origin values are accepted and never the listening address. A request naming a host outside the set fails closed. A request to act that carries only the shared token is refused.

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
