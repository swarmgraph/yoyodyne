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
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-20T22:38:04.74506Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3250, for document-3250.1 under the automatic approval policy
---
# Reducing what the factory costs to run

## What this is for

Mason asked on November 20, 2026 for research into ways to reduce what the factory costs to run. His examples were developers using Sonnet or Opus as the work needs, and the Lead Product Manager and the architect using cheaper helpers for searches. This design records what the research found and what is decided, in order of saving and risk, and names the build pieces.

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

Where nothing has changed, the pass is not started. It is recorded as skipped with the reason nothing new since the last completed pass, naming that pass, and it counts as the one instance for its window. Every role still has at least one full pass every four hours, whatever the check finds, so a change the check does not cover is never left longer than that. Passes woken by an event, rather than by the schedule, are not affected. The four hours is a numeric limit the development manager sets under section 6 of portable agent configuration.

A check in the harness was chosen over a first look by a cheaper model, because the check costs nothing and the things it reads are already records. A cheaper first look can be weighed later for whatever the check cannot decide.

**What it saves.** Most of the cost of the passes that changed nothing, which after piece 1 is smaller than $900 because each start is cheaper. The two savings overlap and are counted together.

## 3. Why the development manager's passes doubled

Her sessions rose from about 32 a day before November 10 to between 48 and 96 a day since, and her daily cost from about $40 to between $68 and $138. Her schedule alone accounts for 32. Messages into her conversation average 63 KB each, against 7 KB for the Lead Product Manager's. Nobody has traced what starts the extra sessions or what fills her messages.

This is a diagnosis for the development manager to admit and a developer to carry out, recording what starts each of her sessions over one day and what each message holds by section, with a fix named for whatever is not needed. No design change is decided here before it is known.

## 4. Developers start on the cheaper model and move up once

**What was found.** Opus runs cost a median of $2.42 and Sonnet runs $0.70. Repair rounds came in 30% of Opus runs and 38% of Sonnet runs, and 59% and 49% ended successfully. This is not a controlled comparison: the only Sonnet seat takes one label of work. The dearest tenth of runs took 39% of developer spend, most of them with two or more repair rounds.

**The decision.** The management loop protocol already decides that the development manager chooses among execution profiles, that a failed attempt at low cost moves once to a stronger profile with the reason and added cost recorded, and that high risk work never runs on the cheapest. That was never built. It is built narrowly now, for developer runs:

- an item the development manager marks eligible starts on Sonnet;
- after its first failed review or failed checks, the run moves once to Opus for the rest of its attempts, and the move and its cost are recorded on the run;
- priority 0 items, and work on security, migrations, concurrency or public interfaces, are never eligible;
- which items are eligible, and which models the two steps use, is the development manager's to decide, as developer models already are.

It goes in by trial. For at least 40 eligible runs, the development manager alternates eligible items between starting on Sonnet and starting on Opus, and compares cost per merged change, how often the first review approves, repair rounds and defects found after landing. The rule is kept only where cost per merged change falls and the first review approves no less often. It stops at once on a defect after landing traced to a Sonnet run that an Opus run would have caught.

[Developer sub-agents](developer-sub-agents.md) is a separate trial already decided, for runs that keep Opus to plan and hand writing to a cheaper model; it is unchanged.

## 5. Reviews of documents on the cheaper model

**What was found.** 982 reviews cost $864: code at a median $0.93 and published documents at $0.66. 49 items took five or more reviews each, 35% of all reviews.

**The decision.** Independent review requires a role that did not write the change, not a particular model. Reviews of a document publication run on Sonnet after a trial of at least 30 publications reviewed on both models, kept only where the Sonnet reviewer refuses what the Opus reviewer refuses. Code reviews stay on Opus. The reviewer's model by kind of change becomes one of the values the development manager decides under section 6 of portable agent configuration, beside developer models, because it changes how work is reviewed and not who may review. That design is revised to say so in its own revision.

Why some items took five or more reviews is a question for the development manager's diagnosis in section 3, which also lists those items.

## 6. Passes on the cheaper model where the work allows it

The development manager already decides the model of a recurring task under section 6. As a trial, the factory-flow program manager's scheduled passes run on Sonnet for one week, compared with the week before on how many findings it filed that a role then acted on, and on what it missed that the development manager later found. They return to Opus if fewer of its findings are acted on. The management roles' own conversation turns, and the models their agents name, are unchanged by this design.

## 7. A helper that reads for a conversation role

**What was found.** The conversation roles hold no tools, so they cannot start sub-agents; they ask the harness for reads, and what comes back fills their Opus context. The architect read four designs between 19 and 49 times each in the window, at about 5% of the architect's spend. The Lead Product Manager read the operating rules 17 times and checked for existing items before each of about 2,000 admissions.

**The decision.** A new tool, `repository.ask`, class `read`, under [the tool interface](tool-interface.md):

- a role asks one question, with optional path prefixes;
- the harness runs one Haiku session that can only read the repository at the commit the turn reads at, with no write, no network and no MCP tools, and with a time limit;
- the answer comes back as paths, line ranges and at most 4 KiB of text, framed as untrusted evidence, and its cost is recorded against the asking role;
- an answer is a pointer and not evidence: a role that rules on a passage reads that passage itself first.

It is held first by the architect, the Lead Product Manager and the program manager. Its saving is the smallest here, so it is built last. The conversation roles still hold no tools of their own: the session belongs to the harness, as research does.

## What is measured

Each week the harness records, from the spend record, the cost per closed item and per landed code change, by role and by model, and how much of each role's spend was writing to the cache, reading from it and output. The dashboard's delivery page shows the same figures. Each trial above reports into these, so a change that saves money and costs quality is seen.

## Alternatives rejected

- **Moving every role to a cheaper model at once.** Nothing yet says which work Sonnet or Haiku does well enough, and the largest cost is text written, not the model.
- **Fewer passes by a longer schedule alone.** A pass with work to do would wait; skipping only the passes with nothing new saves the same without that wait.
- **Giving conversation roles sub-agents of their own.** It would give them tools, which the safety boundaries rule out.
- **Capping spend for each item and stopping work at the cap.** It stops work without saying why it was dear; the diagnosis in section 3 and the move up in section 4 address the cause.

## Build pieces, in order, for the Lead Product Manager to admit

1. **Measure what a pass reuses.** For every pass, record the digest of each part of its start in order and the cached and fresh tokens the provider reports, and name the first part that differs from the role's previous pass. No provider call is added. Then confirm with two passes whether the separately sent block is read from the cache and the message is not.
2. **Send the unchanging part apart.** Build the unchanging part byte for byte the same across passes and send it as the separately cached block, with a test that two passes with the same instructions and intent produce identical bytes. Needs 1.
3. **Skip passes with nothing new.** The check, the skipped record, the four hour floor as a numeric limit, and a test for each thing checked.
4. **The diagnosis of the development manager's sessions, messages and repeated reviews,** with a fix named for each cause.
5. **Developers starting cheap and moving up once,** with the trial and its record.
6. **Reviewer model by kind of change** as a value under section 6, and the trial for document reviews.
7. **The factory-flow program manager on Sonnet for a week,** set by the development manager, with the comparison recorded.
8. **`repository.ask`,** its grants and its tests.
9. **The weekly figures** on cost per closed item and per landed code change.

Pieces 1, 3, 4 and 9 can start at once. Pieces 5, 6 and 7 are trials the development manager runs.

## What a reviewer can check

- After piece 2, two passes of one role with unchanged instructions and intent send identical bytes in the unchanging part, and the provider reports it read from the cache on the second.
- No pass sends less evidence than before: every part it started from is still sent.
- A skipped pass is recorded with its reason, and no role goes more than four hours without a full pass.
- A developer run that moved to Opus records why, when and what it cost.
- No trial's change is kept without the comparison its section names.