---
id: reducing-what-the-factory-costs
kind: design
title: 'Reducing what the factory costs to run: reuse what does not change, skip passes with nothing to do, and match the model to the work'
supports:
    - v1-goals
    - operations-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-20T22:38:04.74506Z
      reason: 'Mason asked on November 20 for research into reducing what the factory costs, with his assistant''s measurements of Nov 6 to 20 as evidence. Orders seven changes by saving and risk: reuse of the unchanging opening of each pass, skipping passes with nothing new, a diagnosis of the development manager''s rise in sessions, cheaper developer and review models behind trials, Sonnet passes, and a read helper for conversation roles. Written from passcontext.go and freshness.go at main ed9451a8.'
    - action: amended
      by: architect
      at: 2026-11-20T23:31:58.620995Z
      reason: 'yoyodyne-ifd.435.83: adds section 8, answering Mason''s question of November 20 on what a project can configure: each setting with its default, bounds held in code and who decides it, models named by kind per provider, and the choices kept fixed and why. Sections 2, 4, 5 and 7 and the build pieces now point to it. The earlier rewrite was refused because the design already existed.'
    - action: amended
      by: architect
      at: 2026-11-21T01:50:54.242927Z
      reason: 'yoyodyne-ifd.435.83: replaces the text on main, which is document-3267.1, a rewrite the architect made in error from the first version, with document-3268.1 (identical to document-3259.1), the text Mason approved. Its own publication was handed back because both revisions replaced the whole file from the first version. Copied unchanged from the text the operator''s assistant supplied on November 20 at 5:30 PM PST; nothing else changes.'
    - action: amended
      by: architect
      at: 2026-11-21T19:37:43.475827Z
      reason: 'amendment-d26f1591: section 1 now states what landed with yoyodyne-ifd.430.89 (unchanging opening sent after the instructions by SplitUnchanging and withPassBriefing, per part fingerprints and first changed part recorded), drops the old account of briefingFirst, and says plainly the architect has not read the provider figures since. Read at 697f01f0. The decision is unchanged.'
    - action: amended
      by: architect
      at: 2026-11-22T01:25:26.368096Z
      reason: 'amendment-8afa9323, approved Nov 21: section 8 holds full_pass_every between 1 and 12 hours and puts both pass settings on program manager scheduled triggers too, with why. Resent after document-3361.1 was handed back: this text no longer claims the portable configuration design already holds these bounds, says that design still says 24 hours until its own revision, and cites the admitted build item 430.92 rather than unbuilt code. Read at 09e7a6138757.'
    - action: amended
      by: architect
      at: 2026-11-22T12:53:55.893638Z
      reason: 'Corrects the design against the second reading of the designs against the code at 848d86190: section 4 says the sub-agent trial ended and slot 2 runs Sonnet with sub-agents off; section 6 and piece 8 had no way to set a model for a program manager''s scheduled passes, so a scheduled trigger gains an optional model the development manager sets; the sizes stay in the listing of documents left out, settling a contradiction; pieces 1 and 2 say what is built. Written whole from main 60d981f81.'
    - action: amended
      by: architect
      at: 2026-11-22T18:54:50.932728Z
      reason: 'amendment-f9345507f2d46b92cd94eb3d7d92e304, approved Nov 22: section 2 now says a role''s own acts in its last pass do not make the next pass new, while what then happened to its work does, and that a landing counts only where it touches a file kind the role owns, product intent, role instructions, or an item the role owns, carries or is named on. The four hour floor stays. Read at 697f81f621a0.'
    - action: amended
      by: architect
      at: 2026-11-23T05:43:28.179635Z
      reason: 'amendment-edddc4be: resends section 8 (document-3636.1 was returned by review) as a whole revision so the rest of the design agrees with it. No setting names the four fixed high risk labels, so piece 6 and the reviewer checks no longer promise a refusal; pieces 3 and 6 say they were built with yoyodyne-ifd.435.85, leaving yoyo config show naming each kind''s model and the trial. Read at 1bfe9d75c5b6.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-20T22:38:04.74506Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3250, for document-3250.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-20T23:31:58.620995Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3267, for document-3267.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-21T01:50:54.242927Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3280, for document-3280.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-21T19:37:43.475827Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3340, for document-3340.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-22T01:25:26.368096Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3366, for document-3366.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-11-22T12:53:55.893638Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3446, for document-3446.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 6
      by: harness
      at: 2026-11-22T18:54:50.932728Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3520, for document-3520.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 7
      by: harness
      at: 2026-11-23T05:43:28.179635Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3639, for document-3639.1 under the automatic approval policy
---
# Reducing what the factory costs to run

## What this is for

Mason asked on November 20, 2026 for research into ways to reduce what the factory costs to run. His examples were developers using a cheaper or a stronger model as the work needs, and the Lead Product Manager and the architect using cheaper helpers for searches. He added that projects will have different needs, so good defaults and a good deal of configurability are both required. This design records what the research found and what is decided, in order of saving and risk, says for each change how a project configures it and what it gets by default, and names the build pieces.

It serves the goal that spend follows the work: the model, the context and the number of provider calls a task costs are chosen for that task. It also serves the goal that roles and provider selection stay configurable without making safety invariants optional, the goal that the operator can see what the harness spends, and the milestone measure of useful software per hour of the owner's attention, whose internal week holds the management roles to at most 40% of known spend. It must not break independent review, the traceable chain, or the rule that conversation roles hold no tools of their own.

## What it rests on, and what was not read

The figures below are from the operator's assistant, measured on November 20 from the spend record, the four roles' conversation logs, run records, the project's configuration and the roles' provider transcripts, for November 6 to November 20. The architect did not measure them again.

Read at commit ed9451a8: `internal/chat/passcontext.go`, which builds what a recurring pass starts from, and `internal/chat/freshness.go`, which builds and refreshes the picture of the product.

Not read: the Claude Code adapter's launch arguments, the briefing builder in `internal/contextbundle`, and the code that starts a pass. Each build piece reads the code it changes first.

The revision of November 22 corrects this design against the second reading of the designs against the code (`docs/diagnoses/yoyodyne-ifd-437-84-6b.md`), made at commit 848d86190 on November 21 and read by the architect at 60d981f81. The architect did not read the code again for it; each correction names the file and lines that reading cites.

The later revision of November 22 rewrites section 8, and the build pieces and reviewer checks that rest on it, from the settings code for models by kind and the cheaper start in `internal/config`, read at commit 1bfe9d75c5b6. The architect did not read the run code that moves a run to the stronger model; the settings code says where it is.

## Models by kind, not by name

This design speaks of three kinds of model: the stronger, the cheaper and the lightest. A project names, for each provider it uses, which of that provider's models is each kind. The shipped bundle names them for Claude Code as Opus, Sonnet and Haiku. A provider for which a kind is not named has no model of that kind, and a change that needs it cannot be turned on for that provider; section 8 says what happens then. The figures below name the models this repository ran on, because that is what was measured.

## Where the money goes

In the fourteen and a half days the factory spent $5,286, about $362 a day, rising to $518 and $589 on November 18 and 19. By role: development manager 22%, developer 21%, Lead Product Manager 18%, reviewer 16%, program manager 13%, architect 9%. 98% was Opus at medium effort. 78% of the spend was writing text into the provider's cache, 13% reading it back, and 9% output. For the four conversation roles, writing is 85% to 89% of their spend. About 328 items closed, about $16 each.

So the largest lever is not the model a role runs on but how much text each provider session writes before it starts work.

## 1. Reuse the opening of each pass instead of writing it again

**What was found.** Every recurring pass starts a provider session of its own, as the bounded pass context ruling of October 5 decided, built from the role's instructions, its memories, the product briefing with the product intent whole, the previous pass's account and the pass's message. In the measured window the first turn of each session wrote a median of 86,000 to 124,000 tokens and cost a median of $0.72 to $1.03. Those first turns were 40% of all spend in the window, $2,091.

**Why, as measured.** The operator's assistant measured two things on November 20:

- Inside one session the cache worked: every second turn read from the cache everything the first turn held, a median of 81% to 87% of its prompt.
- Across sessions it did not. The development manager's opening was one block of 327 to 396 KB. In 8 of 13 consecutive pairs of her passes, the opening was byte for byte the same as the one before for its first 275 to 298 KB, yet each new session read only about 31,000 tokens from the cache and wrote 112,000 to 140,000.

The provider reuses cached text only up to points it has marked, and a point is the end of a whole block of the request. The role's instructions were sent as one block and were read back. Everything else was one block whose end changed on every pass, so none of it was reused, however much of its opening was unchanged.

**What changed from one pass to the next, as measured.** Where consecutive openings first differed:

- in 7 pairs, the memories section, most often the count of memories not listed;
- in 1 pair, the heading that follows the memories;
- in 3 pairs, a byte count on a line saying a design or shipped document was left out of what a recurring turn starts from, or which document comes next in that listing;
- in 2 pairs, the list of work items, where an item was added or changed.

**The decision.** What a pass starts from is sent in two parts, ordered by how rarely each changes:

1. **The unchanging part,** sent as the separately cached block: the role's contract, persona and remit, then the product intent whole. It is built so that two passes with the same instructions and the same intent produce it byte for byte the same. It never holds:
   - a time, a count, a size or a commit;
   - the listing of documents left out of the pass, or anything about them;
   - the work items;
   - the memories, or any line about them.
2. **The changing part,** sent as the message: the listing of documents left out, by name and size, and the rest of the briefing, including the work items; then the memories, the previous pass's account and the pass's message.

The bound on what a pass starts from, a third of the provider's window, counts both parts exactly as before, and nothing in either gives way to it.

The unchanging part is written again, once, whenever the product intent or a role's instructions change. A move onto a new build changes the instructions, and the two first turns in the measured day that read almost nothing from the cache both followed one. Moves come at most once every three hours, so this is accepted, and the measurement below shows it.

**What was built.** The fix for a role's new session writing its briefing again every pass (yoyodyne-ifd.430.89) landed on November 20 and builds this decision and most of the measurement build piece 1 asks for. Read at commit 697f01f0:

- `contextbundle.SplitUnchanging` takes the opening of the briefing that stays the same while the product intent does: the recorded intent and every document of product intent whole.
- `withPassBriefing` in `internal/chat/passcontext.go` sends that opening at the end of the instructions block, behind fixed framing text that says it is evidence and not instruction. Every round of the pass carries the same block. The rest of the briefing opens the message, as before.
- `briefingFirst` keeps the briefing whole at the start of the message only so that its opening can be taken out; it is no longer how reuse is attempted.
- Each pass records the size and a fingerprint of every part of its start in order, the size of the unchanging part, and the first part that differs from the role's previous pass, or that none did.

That item's acceptance required that a session started within the cache's lifetime reads the unchanged part from the cache, and it closed with its checks passing. The architect has not read the provider's cached and fresh token figures for passes since; the weekly figures in What is measured are where that is confirmed or refuted.

**The sizes in the listing of documents left out stay.** The listing is in the changing part, so the sizes cost no reuse, and a role uses a document's size to decide whether to read it in parts. An earlier version of build piece 2 dropped them; that is no longer wanted.

**The cache's lifetime.** The unchanging part keeps the cache that lasts an hour. A role's passes come about 45 minutes apart, so the cache that lasts five minutes would have expired before the next pass could read it. Reuse across passes is the whole saving.

**What it saves.** About 200 KB of each opening is text that otherwise does not change. In the unchanging part it becomes reading at about a fortieth of the price of writing it, from the second pass after any change on. No result of any role changes: each pass sees exactly the text it saw before, in a different order.

**Alternatives rejected.** Resuming one long session across passes, which brings back the context that grows without limit the October 5 ruling removed. Shortening the briefing first, which may cost the roles evidence they use, while reuse costs them nothing. Reordering inside the one block, which the measurement shows reuses nothing. Moving to the cache that lasts five minutes, for the reason above.

## 2. Skip a scheduled pass that has nothing to do

**What was found.** Passes that asked the harness for nothing but reads, memory notes and a lane report were 44% of the development manager's, 53% of the program manager's, 45% of the architect's and 18% of the Lead Product Manager's. Each still paid to start its session: about $900 in the window.

Once skipping was turned on in this repository, no pass was skipped in ten hours, as the item finding what starts each role's pass (yoyodyne-ifd.430.99) found. Two things made every pass look new to the next one: what the role had itself done in its last pass, and every landing on a busy target branch, whatever it touched. This section now says which of those count.

**The decision.** Before the harness starts a scheduled pass, it checks, with no provider call, whether anything the pass would be handed has changed since that role's last completed pass. The things checked are:

- a change landed on the target branch that touches what the role works on, as below;
- a change to a work item the role owns, carries or is named on, made by someone other than the role;
- a report, proposed change, question from another role, or message from the operator addressed to the role;
- a document of the role's that was confirmed, landed or returned;
- a deadline passed on a follow-up the role owns;
- for the development manager, a change to her list of stopped runs or to the factory's health problems; for a program manager, a change in its lane or in the health problems its lane covers.

**What the role did itself is not new.** The role already knows what its own last pass did, so none of these makes the next pass new:

- a note the role wrote on a work item;
- a report it recorded as handled, or a proposed change it decided;
- a document it sent;
- a memory it wrote, or the report a program manager keeps on its lane.

What then happened to that work is new, because the role has not seen it:

- its document confirmed, landed on the target branch, or returned to it;
- a note written by another role or by the harness on an item the role owns, carries or is named on.

The harness tells the two apart by who wrote each record, which every one of these records already says. Nothing new is stored for it.

**Which landings count.** A change landed on the target branch counts only where it does at least one of these:

- changes a file of a kind the role owns;
- changes a document of product intent in the product specification home;
- changes a role's instructions;
- changes or closes a work item the role owns, carries or is named on.

A landing that does none of these is not new to that role. A code change on an item the architect has no part in, for example, does not start an architect pass. The development manager's list of stopped runs and the factory's health problems, and a program manager's lane, are checked as their own bullet above, so a landing that changes them still counts through it.

**The floor stays.** Where nothing has changed, the pass is not started. It is recorded as skipped with the reason nothing new since the last completed pass, naming that pass, and it counts as the one instance for its window. Every role still has a full pass at least as often as the floor in section 8 says, four hours by default, whatever the check finds, so a change the check does not cover is never left longer than that. In this repository the management roles' scheduled passes run every 45 minutes, so with the default floor at most five in a row are skipped before a full pass. Passes woken by an event, rather than by the schedule, are not affected.

A check in the harness was chosen over a first look by a cheaper model, because the check costs nothing and the things it reads are already records. A cheaper first look can be weighed later for whatever the check cannot decide. Counting every landing was rejected because on this repository's target branch something lands most hours, so no pass would ever be skipped. Counting a role's own acts was rejected for the same reason: every pass that wrote a note would make the next one look new.

**What a reviewer can check for this section.** A pass whose only changes since the last completed pass are that pass's own notes, handled reports, decided proposals, sent documents, memories or lane report is skipped. A pass is started when one of its documents was confirmed, landed or returned, or another role or the harness wrote on one of its items. A landing that changes none of the four kinds of thing above does not start the pass; one that does starts it. Each has a test.

**What it saves.** Most of the cost of the passes that changed nothing, which after section 1 is smaller than $900 because each start is cheaper. The two savings overlap and are counted together.

## 3. Why the development manager's passes doubled

Her sessions rose from about 32 a day before November 10 to between 48 and 96 a day since, and her daily cost from about $40 to between $68 and $138. Her schedule alone accounts for 32. Messages into her conversation average 63 KB each, against 7 KB for the Lead Product Manager's. Nobody has traced what starts the extra sessions or what fills her messages.

This is a diagnosis for the development manager to admit and a developer to carry out, recording what starts each of her sessions over one day and what each message holds by section, with a fix named for whatever is not needed. No design change is decided here before it is known.

## 4. Developers start on the cheaper model and move up once

**What was found.** Opus runs cost a median of $2.42 and Sonnet runs $0.70. Repair rounds came in 30% of Opus runs and 38% of Sonnet runs, and 59% and 49% ended successfully. This is not a controlled comparison: the only Sonnet seat takes one label of work. The dearest tenth of runs took 39% of developer spend, most of them with two or more repair rounds.

**The decision.** The management loop protocol already decides that the development manager chooses among execution profiles, that a failed attempt at low cost moves once to a stronger profile with the reason and added cost recorded, and that high risk work never runs on the cheapest. That was never built. It is built narrowly now, for developer runs:

- an eligible item starts on the cheaper model;
- after its first failed review or failed checks, the run moves once to the stronger model for the rest of its attempts, and the move and its cost are recorded on the run;
- work of a high risk kind is never eligible: security, migrations, concurrency and public interfaces, as the protocol rules;
- which other items are eligible is set as section 8 says.

In this repository it goes in by trial. For at least 40 eligible runs, the development manager alternates eligible items between starting on the cheaper model and starting on the stronger, and compares cost per merged change, how often the first review approves, repair rounds and defects found after landing. It is kept only where cost per merged change falls and the first review approves no less often. It stops at once on a defect after landing traced to a run on the cheaper model that one on the stronger would have caught.

[Developer sub-agents](developer-sub-agents.md) describes runs that keep the stronger model to plan and hand writing to sub-agents on a cheaper one. Its trial in this repository, Opus planning with Sonnet sub-agents, cost more without a gain in quality, as this repository's configuration records (`.yoyodyne/config.yaml`, lines 136 to 140, at 848d86190), and sub-agents are now off on every developer slot. Slot 2 instead runs Sonnet as its main model with sub-agents off. That is an early look at the question this section's trial answers, not a replacement for it, because slot 2 takes only one label of work and is not compared with the stronger model on the same kind of items.

## 5. Reviews of documents on the cheaper model

**What was found.** 982 reviews cost $864: code at a median $0.93 and published documents at $0.66. 49 items took five or more reviews each, 35% of all reviews.

**The decision.** Independent review requires a role that did not write the change, not a particular model. The reviewer's model is chosen by kind of change, documents or code, as section 8 says. In this repository, reviews of a document publication move to the cheaper model after a trial of at least 30 publications reviewed on both, kept only where the cheaper reviewer refuses what the stronger refuses. Code reviews stay on the stronger model.

Why some items took five or more reviews is a question for the development manager's diagnosis in section 3, which also lists those items.

## 6. Passes on the cheaper model where the work allows it

A recurring task's model is already a setting. A program manager's scheduled passes are not recurring tasks: they come from the triggers on its agent, and in the code a trigger has only how often and on what (`internal/config/lane.go`, lines 62 to 69, at 848d86190). Short of the agent's own model, which stays the operator's, there is nowhere to name a model for those passes.

So a scheduled trigger of a program manager instance gains an optional `model`, naming a kind, that applies only to the passes that trigger starts, exactly as a recurring task's model applies only to its own passes. Where a trigger names none, its passes use the agent's model, as today. [The program manager design](program-manager.md) already says a pass asks for the trigger's model where the trigger names one; this makes that true in the code. Letting the development manager change the agent's model instead was rejected: it would also change the instance's conversation turns, and it would take a decision that is the operator's.

As a trial in this repository, the factory-flow program manager's scheduled passes run on the cheaper model for one week, set by the development manager on its scheduled trigger, compared with the week before on how many findings it filed that a role then acted on, and on what it missed that the development manager later found. They return to the stronger model if fewer of its findings are acted on. The management roles' own conversation turns, and the models their agents name, are unchanged by this design.

## 7. A helper that reads for a conversation role

**What was found.** The conversation roles hold no tools, so they cannot start sub-agents; they ask the harness for reads, and what comes back fills their context. The architect read four designs between 19 and 49 times each in the window, at about 5% of the architect's spend. The Lead Product Manager read the operating rules 17 times and checked for existing items before each of about 2,000 admissions.

**The decision.** A new tool, `repository.ask`, class `read`, under [the tool interface](tool-interface.md):

- a role asks one question, with optional path prefixes;
- the harness runs one session on the helper's model that can only read the repository at the commit the turn reads at, with no write, no network and no MCP tools, and with a time limit;
- the answer comes back as paths, line ranges and text no longer than the answer limit, framed as untrusted evidence, and its cost is recorded against the asking role;
- an answer is a pointer and not evidence: a role that rules on a passage reads that passage itself first.

Its saving is the smallest here, so it is built last. The conversation roles still hold no tools of their own: the session belongs to the harness, as research does.

## 8. Settings, defaults and who may change them

Every change above has a setting, a default and an owner, unless this section says there is deliberately none. The default is what the shipped bundle gives a project that states nothing. A project in its own file is the project's configuration, changed by its operator or by a reviewed change. The development manager's path is section 6 of [portable agent configuration](portable-agent-configuration.md): a typed write, reviewed, recorded as hers. That write is not built yet: it is the item letting a role change a setting it owns through one checked write (yoyodyne-ifd.434.54). Until it lands, every value below changes only by a reviewed change to the configuration file, whoever the table names as deciding it. The defaults are today's behaviour except where a change trades nothing away.

| Change | Setting | Default for a new project | Who may change it | Built |
| --- | --- | --- | --- | --- |
| Models by kind | `execution.model_kinds`, for each provider: `stronger`, `cheaper`, `lightest`, each a model name | for Claude Code: `opus`, `sonnet`, `haiku`; Codex and any provider a project declares name none until the project names them | the development manager | yes, with yoyodyne-ifd.435.85, except that `yoyo config show` does not yet name each kind's model |
| Reusing the opening of each pass (section 1) | none | always on | nobody | yes, with yoyodyne-ifd.430.89 |
| Skipping a scheduled pass with nothing new (section 2) | `skip_when_unchanged` on each recurring task, and on each scheduled trigger of a program manager instance | on for every scheduled pass of a management role | the development manager | no; admitted as yoyodyne-ifd.430.92 |
| The floor on full passes (section 2) | `full_pass_every` on each recurring task, and on each scheduled trigger of a program manager instance | 4 hours; allowed between 1 hour and 12 hours | the development manager | no; admitted as yoyodyne-ifd.430.92 |
| Developers starting on the cheaper model (section 4) | `execution.developer_cheaper_first`: `enabled`, `labels`, `priorities`, `trial_runs` | off; when on, every label is eligible, priorities 1 to 4 are eligible, and the trial is 40 runs | the development manager | yes, with yoyodyne-ifd.435.85 |
| High risk labels never on the cheaper model (section 4) | `execution.high_risk_labels`: labels a project adds to the four fixed ones | none added; the four fixed labels `security`, `migration`, `concurrency` and `public-interface` are held in code | the development manager may add labels; nobody can remove the four, because no setting names them | yes, with yoyodyne-ifd.435.85 |
| Reviewer's model by kind of change (section 5) | `reviewer_models`: `documents` and `code`, each a kind | both `stronger` | the development manager | no |
| A recurring task's model (section 6) | the task's existing `model`, which may come to name a kind | unchanged | the development manager, as today | naming a model, yes; naming a kind, no |
| The model of a program manager's scheduled passes (section 6) | a new optional `model` on each scheduled trigger of a program manager instance, naming a model or a kind | none, so the passes use the agent's model, as today | the development manager | no |
| The reading helper (section 7) | `repository_ask`: its model kind, answer limit and time limit | on once built; `lightest`; 4 KiB, allowed between 1 KiB and 16 KiB; 60 seconds | the development manager; a project may take the tool from an agent with `tools: deny` | no |
| Weekly cost figures | none | always kept | nobody | no |

**Bounds held in code for models by kind and the cheaper start.** Each is refused when the configuration loads, with the key named:

- `execution.model_kinds` may name only Claude Code, Codex or a provider the project declares, and each model it names must be a model name the harness accepts.
- `priorities` takes 1 to 4, each named once. Naming priority 0, or a priority the tracker does not have, is refused.
- `trial_runs` takes 40 to 1000. Left out, it is 40. Fewer than 40 runs compare too little to decide anything on.
- A label in `labels` or in `high_risk_labels` must be one the tracker would carry, named once. A label in `labels` that is also a high risk label is refused, because it would make nothing eligible.

**How an item is judged eligible to start on the cheaper model.** The first of these that applies decides, and its reason is recorded on the run:

1. The setting is off: not eligible.
2. The item's priority was never recorded: it is treated as priority 0, so it is not eligible.
3. The item is priority 0: never eligible.
4. The item carries a high risk label, fixed or added: never eligible.
5. `priorities` names some priorities and not the item's: not eligible.
6. `labels` names some labels: eligible only if the item carries one of them.
7. Otherwise: eligible.

An item that `execution.developer_models` sends to a model by one of its labels keeps that model and does not start on the cheaper one: that mapping is a choice somebody made for that work, and starting it cheaper would undo it.

Why the floor stops at 12 hours, and why these settings reach a program manager's triggers:

- **12 hours, not 24.** The floor is what catches a change the skip check does not cover. A role left a whole day without one full pass could miss such a change for a working day, and the milestone's internal week allows no hour with ready work waiting and nobody moving it. Twelve hours still lets a project with little to do skip most of its passes.
- **Program manager triggers.** A program manager instance is scheduled by its triggers, not by a recurring task, and it had the largest share of passes with nothing new, 53%. Section 2 says every role keeps its floor, so leaving the triggers out would leave the program manager out by accident, and section 6 needs the trigger's model for the same reason. The development manager decides only these three keys on a trigger, the two pass settings and its model; the rest of an instance's settings stay the operator's.

Why some things have no setting:

- **Reusing the opening of each pass.** It sends each role the same text in a different order and changes nothing a role sees, so turning it off would only make the same work cost more.
- **Which things the skip check reads.** The list is held in code for each role. A list a project could edit could hide a change from the role that has to act on it. A project chooses only whether passes are skipped and how long the floor is.
- **The four fixed high risk labels.** The management loop protocol rules that high risk work never runs on the cheapest model. The four labels are held in code and no setting names them, so there is no configuration that removes one; that is how the rule holds, rather than by a refusal. A project can only add labels to the list.
- **Priority 0.** The same protocol keeps the most urgent work off the cheaper model, so priority 0 is never eligible and naming it is refused.
- **Which roles hold the reading helper.** Grants are held in the role bundles in Go and configuration never grants a tool, as the invariant on configuration says; a project can only take the helper away from an agent.
- **Which model each management role runs on.** It stays what it is today, the operator's, in each agent's configuration. A model on a recurring task or on a scheduled trigger changes only the passes it starts, never the agent's own model or its conversation turns. Nothing in this design changes the agent's model.

A change that needs a model of a kind its provider has not named cannot be turned on. Built today: turning on `execution.developer_cheaper_first` while a developer agent runs on a provider that names no cheaper model is refused when the configuration loads, naming the provider and the agent. So in a project that has not named Codex's kinds, the cheaper start cannot be turned on while any developer runs on Codex. Each later piece adds the same refusal for its own change, naming the provider, the change and the missing kind: a kind in `reviewer_models`, a task's or a trigger's model naming a kind, and the reading helper. The reading helper is off for a provider until its lightest kind is named.

The trials in sections 4 to 6 decide what this repository turns on. They do not change the shipped defaults. If a trial shows a change helps without costing quality, changing the shipped default is a later decision recorded in its own revision of this design.

Section 6 of portable agent configuration is where the development manager's path for these values is recorded. Its row for the two pass settings still allows `full_pass_every` up to 24 hours and names recurring tasks only, it has no row for a trigger's model, and its row for high risk labels still speaks of never removing the four fixed kinds rather than of their having no setting. Its next revision brings those rows into line with this section, and until it lands, this section decides. The admitted build of skipping passes with nothing new (yoyodyne-ifd.430.92) carries the 12 hour bound and the program manager triggers.

## What is measured

Each week the harness records, from the spend record, the cost per closed item and per landed code change, by role and by model, and how much of each role's spend was writing to the cache, reading from it and output. The dashboard's delivery page shows the same figures. Each trial above reports into these, so a change that saves money and costs quality is seen.

## Alternatives rejected

- **Moving every role to a cheaper model at once.** Nothing yet says which work a cheaper model does well enough, and the largest cost is text written, not the model.
- **Naming models by one provider's names in settings.** A project on another provider could not say which of its models is the cheaper; kinds a project maps keep every setting meaningful on any provider.
- **Turning the changes that could cost quality on by default.** A new project would carry this repository's trade before any trial on its own work.
- **Fewer passes by a longer schedule alone.** A pass with work to do would wait; skipping only the passes with nothing new saves the same without that wait.
- **Giving conversation roles sub-agents of their own.** It would give them tools, which the safety boundaries rule out.
- **Capping spend for each item and stopping work at the cap.** It stops work without saying why it was dear; the diagnosis in section 3 and the move up in section 4 address the cause.

## Build pieces, in order, for the Lead Product Manager to admit

1. **Record where each opening stops being reused.** Built in part with yoyodyne-ifd.430.89: each pass records the size and fingerprint of each part of its start in order and the first part that differs from the role's previous pass. What remains is recording, beside them, the cached and fresh tokens the provider reports for each pass. No provider call is added.
2. **Send the unchanging part apart.** Built with yoyodyne-ifd.430.89, as section 1 says. Nothing remains; the sizes stay in the listing of documents left out.
3. **Models by kind.** Built with yoyodyne-ifd.435.85: `execution.model_kinds` with its defaults, and the refusal at load where the cheaper start is on for a developer whose provider names no cheaper model. What remains is `yoyo config show` naming each kind's model for each provider. Each later piece that uses a kind adds its own refusal at load, as section 8 says.
4. **Skip passes with nothing new.** The check, the skipped record, `skip_when_unchanged` and `full_pass_every` with their defaults and bounds, and a test for each thing checked.
5. **The diagnosis of the development manager's sessions, messages and repeated reviews,** with a fix named for each cause.
6. **Developers starting on the cheaper model and moving up once.** Built with yoyodyne-ifd.435.85: `execution.developer_cheaper_first` with its bounds, `execution.high_risk_labels` adding to the four fixed labels held in code, the eligibility order in section 8, and, as the settings code describes, the move up and the alternation of eligible runs between starting cheaper and starting stronger. What remains is the trial itself and its record, which the development manager runs once she turns the setting on.
7. **Reviewer model by kind of change,** with `reviewer_models`, its refusal at load where a provider lacks the kind, and the trial for document reviews.
8. **The factory-flow program manager on the cheaper model for a week.** First the optional `model` on a program manager's scheduled trigger, naming a model or a kind, with the refusal at load where its provider lacks that kind and a test that a trigger's model changes only the passes it starts; then the development manager sets it, and the comparison is recorded.
9. **`repository.ask`,** with its setting, grants and tests.
10. **The weekly figures** on cost per closed item and per landed code change.

What remains of pieces 1 and 3, and pieces 4, 5, 7, 8, 9 and 10, can start at once, since the models by kind they rest on are built. Pieces 6, 7 and 8 carry trials the development manager runs.

## What a reviewer can check

- Two passes of one role with unchanged instructions and intent send identical bytes in the unchanging part, and the provider reports it read from the cache on the second.
- The unchanging part holds no time, count, size, commit, listing of documents left out, work item or memory.
- No pass sends less evidence than before: every part it started from is still sent, the sizes in the listing of documents left out included.
- A project that states none of the settings in section 8 gets exactly the defaults that section names.
- A configuration that turns on a change needing a model kind its provider has not named is refused at load, naming the provider and the change, and the kind where more than one could be meant.
- No setting can remove one of the four fixed high risk labels: they are held in code, and an item carrying one never starts on the cheaper model, whatever the configuration says.
- An item whose priority is 0, or was never recorded, never starts on the cheaper model.
- A skipped pass is recorded with its reason, and no role goes longer than its floor without a full pass.
- A model set on a program manager's scheduled trigger changes only the passes that trigger starts; the agent's model and its conversation turns are unchanged.
- A developer run that moved to the stronger model records why, when and what it cost.
- No trial's change is kept without the comparison its section names.