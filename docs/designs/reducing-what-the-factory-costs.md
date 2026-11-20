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
---
# Reducing what the factory costs to run

## What this is for

Mason asked on November 20, 2026 for research into ways to reduce what the factory costs to run. His examples were developers using Sonnet or Opus as the work needs, and the Lead Product Manager and the architect using cheaper helpers for searches. This design records what the research found and what is decided, in order of saving and risk, says which of its choices a project can configure and who decides each, and names the build pieces.

It serves the goal that spend follows the work: the model, the context and the number of provider calls a task costs are chosen for that task. It also serves the goal that the operator can see what the harness spends, and the milestone measure of useful software per hour of the owner's attention, whose internal week holds the management roles to at most 40% of known spend. It must not break independent review, the traceable chain, or the rule that conversation roles hold no tools of their own.

## What it rests on, and what was not read

The figures below are from the operator's assistant, measured on November 20 from the spend record, the four roles' conversation logs, run records and the project's configuration, for November 6 to November 20 at 14:22 PST. The architect did not measure them again.

Read at commit ed9451a8: `internal/chat/passcontext.go`, which builds what a recurring pass starts from, and `internal/chat/freshness.go`, which builds and refreshes the picture of the product.

Not read: the Claude Code adapter's launch arguments, how the provider marks what it caches, the briefing builder in `internal/contextbundle`, and the code that starts a pass. Each build piece reads the code it changes first, and piece 1 measures before anything is changed.

## Where the money goes

In the fourteen and a half days the factory spent $5,286, about $362 a day, rising to $518 and $589 on November 18 and 19. By role: development manager 22%, developer 21%, Lead Product Manager 18%, reviewer 16%, program manager 13%, architect 9%. 98% was Opus at medium effort. 78% of the spend was writing text into the provider's cache, 13% reading it back, and 9% output. For the four conversation roles, writing is 85% to 89% of their spend. About 328 items closed, about $16 each.

So the largest lever is not the model a role runs on but how much text each provider session writes before it starts work.

## 1. Reuse the opening of each pass instead of writing it again

**What was found.** Every recurring pass starts a provider session of its own, as the bounded pass context ruling of October 5 decided, built from the role's instructions, its memories, the product briefing with the product intent whole, the previous pass's account and the pass's message. The first turn of each session writes a median of 86,000 to 124,000 tokens and costs a median of $0.72 to $1.03. Those first turns were 40% of all spend in the window, $2,091. Only the first 17,000 to 38,000 tokens of a new session come from the cache, although almost every session began within an hour of the role's previous one.

The harness already tries to make the briefing reusable: `briefingFirst` in `passcontext.go` puts the briefing ahead of the memories on a fresh pass, so that a provider caching what its requests open with can serve it again. The record shows that this is not happening.

**Why, as far as can be told without measuring.** The provider reuses cached text only up to points it has marked, and a point is the end of a whole block of the request. A pass sends the role's instructions as one block, and the briefing, memories, account and message together as another, whose end differs on every pass. So the instructions are read back from the cache, which matches the 17,000 to 38,000 tokens measured, and the whole of the second block is written fresh, however much of its opening is unchanged. This is the architect's reading of the figures, not something confirmed; piece 1 confirms or overturns it before anything else is built.

**The decision.** What a pass starts from is sent in two parts, ordered by how rarely each changes:

1. **The unchanging part,** sent as the separately cached block: the role's contract, persona and remit, then the product intent whole. It is built so that two passes with the same instructions and the same intent produce it byte for byte the same. It carries no time, no count, no commit, no listing of documents and nothing else that moves when work lands.
2. **The changing part,** sent as the message: the rest of the briefing, including the listing of the role's own documents and the work items, then the memories, the previous pass's account and the pass's message.

The bound on what a pass starts from, a third of the provider's window, counts both parts exactly as today, and nothing in either gives way to it. A change to the product intent or to a role's instructions makes the next pass write the unchanging part again, once.

**The cache's lifetime.** The unchanging part keeps the cache that lasts an hour. A role's passes come about 45 minutes apart, so the cache that lasts five minutes would have expired before the next pass could read it. Reuse across passes is the whole saving.

**What it saves.** If the unchanging part is most of what a pass now writes at its start, most of the $2,091 becomes reading at about a fortieth of the price. How much is unchanging is what piece 1 measures. No result of any role changes: each pass sees exactly the text it saw before, in a different order.

**Alternatives rejected.** Resuming one long session across passes, which brings back the context that grows without limit the October 5 ruling removed. Shortening the briefing first, which may cost the roles evidence they use, while reuse costs them nothing. Moving to the cache that lasts five minutes, for the reason above.

## 2. Skip a scheduled pass that has nothing to do

**What was found.** Passes that asked the harness for nothing but reads, memory notes and a lane report were 44% of the development manager's, 53% of the program manager's, 45% of the architect's and 18% of the Lead Product Manager's. Each still paid to start its session: about $900 in the window.

**The decision.** Before the harness starts a scheduled pass, it checks, with no provider call, whether anything the pass would be handed has changed since that role's last completed pass. The things checked are:

- a change landed on the target branch;
- a change to a work item the role owns, carries or is named on;
- a report, proposed change, question from another role, or message from the operator addressed to the role;
- a document of the role's that was confirmed, sent, landed or returned;
- a deadline passed on a follow-up the role owns;
- for the development manager, a change to her list of stopped runs or to the factory's health problems; for a program manager, a change in its lane or in the health problems its lane covers.

Where nothing has changed, the pass is not started. It is recorded as skipped with the reason nothing new since the last completed pass, naming that pass, and it counts as the one instance for its window. Every role still has a full pass at least every four hours by default, whatever the check finds, so a change the check does not cover is never left longer than that. Passes woken by an event, rather than by the schedule, are not affected. Whether skipping is on, and the longest time between full passes, are settings in section 8.

A check in the harness was chosen over a first look by a cheaper model, because the check costs nothing and the things it reads are already records. A cheaper first look can be weighed later for whatever the check cannot decide.

**What it saves.** Most of the cost of the passes that changed nothing, which after piece 1 is smaller than $900 because each start is cheaper. The two savings overlap and are counted together.

## 3. Why the development manager's passes doubled

Her sessions rose from about 32 a day before November 10 to between 48 and 96 a day since, and her daily cost from about $40 to between $68 and $138. Her schedule alone accounts for 32. Messages into her conversation average 63 KB each, against 7 KB for the Lead Product Manager's. Nobody has traced what starts the extra sessions or what fills her messages.

This is a diagnosis for the development manager to admit and a developer to carry out, recording what starts each of her sessions over one day and what each message holds by section, with a fix named for whatever is not needed. No design change is decided here before it is known.

## 4. Developers start on the cheaper model and move up once

**What was found.** Opus runs cost a median of $2.42 and Sonnet runs $0.70. Repair rounds came in 30% of Opus runs and 38% of Sonnet runs, and 59% and 49% ended successfully. This is not a controlled comparison: the only Sonnet seat takes one label of work. The dearest tenth of runs took 39% of developer spend, most of them with two or more repair rounds.

**The decision.** The management loop protocol already decides that the development manager chooses among execution profiles, that a failed attempt at low cost moves once to a stronger profile with the reason and added cost recorded, and that high risk work never runs on the cheapest. That was never built. It is built narrowly now, for developer runs:

- an item the development manager marks eligible starts on the cheaper model;
- after its first failed review or failed checks, the run moves once to the stronger model for the rest of its attempts, and the move and its cost are recorded on the run;
- priority 0 items, and work on security, migrations, concurrency or public interfaces, are never eligible;
- which items are eligible is the development manager's to decide, and which models the two steps use comes from the settings in section 8.

It goes in by trial. For at least 40 eligible runs, the development manager alternates eligible items between starting on the cheaper model and starting on the stronger one, and compares cost per merged change, how often the first review approves, repair rounds and defects found after landing. The rule is kept only where cost per merged change falls and the first review approves no less often. It stops at once on a defect after landing traced to a run on the cheaper model that a run on the stronger one would have caught.

[Developer sub-agents](developer-sub-agents.md) is a separate trial already decided, for runs that keep Opus to plan and hand writing to a cheaper model; it is unchanged.

## 5. Reviews of documents on the cheaper model

**What was found.** 982 reviews cost $864: code at a median $0.93 and published documents at $0.66. 49 items took five or more reviews each, 35% of all reviews.

**The decision.** Independent review requires a role that did not write the change, not a particular model. Reviews of a document publication run on the cheaper model after a trial of at least 30 publications reviewed on both models, kept only where the cheaper reviewer refuses what the stronger reviewer refuses. Code reviews stay on the stronger model. The reviewer's model by kind of change is a setting in section 8, because it changes how work is reviewed and not who may review.

Why some items took five or more reviews is a question for the development manager's diagnosis in section 3, which also lists those items.

## 6. Passes on the cheaper model where the work allows it

The development manager already decides the model of a recurring task under section 6 of [portable agent configuration](portable-agent-configuration.md). As a trial, the factory-flow program manager's scheduled passes run on Sonnet for one week, compared with the week before on how many findings it filed that a role then acted on, and on what it missed that the development manager later found. They return to Opus if fewer of its findings are acted on. The management roles' own conversation turns, and the models their agents name, are unchanged by this design.

## 7. A helper that reads for a conversation role

**What was found.** The conversation roles hold no tools, so they cannot start sub-agents; they ask the harness for reads, and what comes back fills their Opus context. The architect read four designs between 19 and 49 times each in the window, at about 5% of the architect's spend. The Lead Product Manager read the operating rules 17 times and checked for existing items before each of about 2,000 admissions.

**The decision.** A new tool, `repository.ask`, class `read`, under [the tool interface](tool-interface.md):

- a role asks one question, with optional path prefixes;
- the harness runs one session on the lightest model that can only read the repository at the commit the turn reads at, with no write, no network and no MCP tools, and with a time limit;
- the answer comes back as paths, line ranges and a bounded amount of text, framed as untrusted evidence, and its cost is recorded against the asking role;
- an answer is a pointer and not evidence: a role that rules on a passage reads that passage itself first.

It is held first by the architect, the Lead Product Manager and the program manager. Its saving is the smallest here, so it is built last. The conversation roles still hold no tools of their own: the session belongs to the harness, as research does.

## 8. What can be configured, and who decides

Mason asked which of these choices a project can change. Every setting below lives with the values the development manager decides under section 6 of [portable agent configuration](portable-agent-configuration.md), which says how such a value is set and recorded; that design is revised in its own revision to name them. Each limit has bounds held in code, and a value outside them is refused when the configuration loads, as for every other limit there.

| Setting | What it decides | Default | Bounds held in code | Who decides |
| --- | --- | --- | --- | --- |
| `model_kinds` | For each provider, which model is the stronger, the cheaper and the lightest | Claude Code: Opus, Sonnet and Haiku. Codex: none named | A named model must be one the provider's adapter accepts | Development manager |
| `developer_cheaper_first` | Whether eligible developer runs start on the cheaper model and move to the stronger once (section 4) | Off until the trial keeps it | None | Development manager |
| `high_risk_labels` | Labels that make an item never eligible to start on the cheaper model, beside the fixed kinds | None | Adds only; cannot remove a fixed kind | Development manager |
| `reviewer_models` | The model a review uses for code and for a document publication (section 5) | The stronger model for both | Code reviews cannot be set below the stronger model | Development manager |
| `skip_when_unchanged` | Whether a scheduled pass with nothing new is skipped (section 2) | On, once piece 3 lands | None | Development manager |
| `full_pass_every` | The longest a role goes without a full pass while skipping is on | 4 hours | At least 1 hour, at most 12 hours | Development manager |
| `repository_ask` | The read helper's time limit and the size of its answer (section 7) | 5 minutes and 4 KiB | At most 15 minutes and 8 KiB | Development manager |

Where a provider names no model of a kind, a choice that needs that kind does nothing for that provider, and its runs keep the model already configured for them. With the Codex defaults above, sections 4, 5 and 7 change nothing on Codex until its kinds are named.

Some choices are deliberately not settings:

- **Reuse of the unchanging part of each pass (section 1).** It changes no role's result, so there is nothing a project gains by turning it off. The cache's lifetime of one hour is fixed for the reason given in section 1.
- **What the skip check reads (section 2).** A project that narrowed the list could skip passes that have work waiting. The list is changed only by revising this design.
- **The fixed kinds that are never eligible.** Priority 0 items and work on security, migrations, concurrency or public interfaces stay on the stronger model whatever the configuration says; a project may add labels to them and never take one away.
- **Who holds `repository.ask`.** Which roles hold a tool is trusted code, and configuration never grants authority. A project can narrow a role's tools as it can today, never widen them.

Each setting is built in the build piece that uses it, with its default, its bounds and a test that a value outside them is refused.

## What is measured

Each week the harness records, from the spend record, the cost per closed item and per landed code change, by role and by model, and how much of each role's spend was writing to the cache, reading from it and output. The dashboard's delivery page shows the same figures. Each trial above reports into these, so a change that saves money and costs quality is seen.

## Alternatives rejected

- **Moving every role to a cheaper model at once.** Nothing yet says which work Sonnet or Haiku does well enough, and the largest cost is text written, not the model.
- **Fewer passes by a longer schedule alone.** A pass with work to do would wait; skipping only the passes with nothing new saves the same without that wait.
- **Giving conversation roles sub-agents of their own.** It would give them tools, which the safety boundaries rule out.
- **Capping spend for each item and stopping work at the cap.** It stops work without saying why it was dear; the diagnosis in section 3 and the move up in section 4 address the cause.
- **Naming models directly in each setting instead of by kind.** A project moving to a new model would then change every setting; naming the kinds once per provider keeps that to one place.

## Build pieces, in order, for the Lead Product Manager to admit

1. **Measure what a pass reuses.** For every pass, record the digest of each part of its start in order and the cached and fresh tokens the provider reports, and name the first part that differs from the role's previous pass. No provider call is added. Then confirm with two passes whether the separately sent block is read from the cache and the message is not.
2. **Send the unchanging part apart.** Build the unchanging part byte for byte the same across passes and send it as the separately cached block, with a test that two passes with the same instructions and intent produce identical bytes. Needs 1.
3. **Skip passes with nothing new.** The check, the skipped record, `skip_when_unchanged` and `full_pass_every` with their bounds, and a test for each thing checked.
4. **The diagnosis of the development manager's sessions, messages and repeated reviews,** with a fix named for each cause.
5. **Developers starting cheap and moving up once,** with `model_kinds`, `developer_cheaper_first` and `high_risk_labels`, the trial and its record.
6. **Reviewer model by kind of change,** with `reviewer_models`, and the trial for document reviews. Needs the `model_kinds` setting from 5.
7. **The factory-flow program manager on Sonnet for a week,** set by the development manager, with the comparison recorded.
8. **`repository.ask`,** with its `repository_ask` setting, its grants and its tests.
9. **The weekly figures** on cost per closed item and per landed code change.

Pieces 1, 3, 4 and 9 can start at once. Pieces 5, 6 and 7 are trials the development manager runs.

## What a reviewer can check

- After piece 2, two passes of one role with unchanged instructions and intent send identical bytes in the unchanging part, and the provider reports it read from the cache on the second.
- No pass sends less evidence than before: every part it started from is still sent.
- A skipped pass is recorded with its reason, and no role goes longer than `full_pass_every` without a full pass.
- A developer run that moved to the stronger model records why, when and what it cost.
- Every setting in section 8 has its default and bounds, a value outside them is refused at load, and no setting lets a project remove a fixed kind, put code reviews below the stronger model, or grant `repository.ask` to a role.
- No trial's change is kept without the comparison its section names.