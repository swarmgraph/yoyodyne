---
id: developer-sub-agents
kind: design
title: Developer runs that hand coding to sub-agents on a cheaper model
supports:
    - operations-goals
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-14T15:49:46.162794Z
      reason: 'yoyodyne-t4w: the design for developer runs that plan on the main model and hand coding to sub-agents on a cheaper configured model, answering the build yoyodyne-23o. Filed as its own design because the provider design has a pending revision and the execution routing design is near the size limit; it extends both.'
    - action: amended
      by: architect
      at: 2026-10-27T18:59:34.397641Z
      reason: 'yoyodyne-wj4l.2: a run with sub-agents on is now told to plan on the main model, hand the writing of code to sub-agents, and verify the whole change before submitting. The instruction is one Markdown file in the harness''s source, added only to such runs and owned by the development manager. The developer persona is unchanged, and the trial runs with the instruction on.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-14T15:49:46.162794Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 945, for document-945.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-27T18:59:34.397641Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1862, for document-1862.1 under the automatic approval policy
---
# Developer runs that hand coding to sub-agents on a cheaper model

yoyodyne-t4w. A developer run today is one session on one model. The operator directed on October 14 that a run should plan on the main model and hand the writing of code to sub-agents on a cheaper model, configurably. This design says how, so that the build (yoyodyne-23o) can be implemented and reviewed against it. Revised for yoyodyne-wj4l.2: a run with sub-agents on is told to delegate, rather than left to decide whether to.

It serves the goal that spend follows the work: the model a piece of work costs is chosen for that piece of work rather than for the role. It must not break the goals that implementation stays isolated in harness-managed worktrees, that provider selection stays configurable without making safety invariants optional, that every change is reviewed by a role that did not write it, and that the operator can see what the harness spends.

It extends two designs and replaces nothing in either. [Provider adapters and execution endpoints](provider-adapters-and-endpoints.md) owns what an adapter declares; this design adds one capability to that declaration. [Execution routing](claude-execution-and-account-routing.md) owns slots, endpoint pairs, pinning, fallback and records; this design adds sub-agent settings to what a run pins and records. It adds no invariant: [configuration never grants authority](../decisions/invariants/configuration-never-grants-authority.md) and [repository writes are physically confined](../decisions/invariants/repository-writes-are-physically-confined.md) already bind every part of it.

## What the run does

The developer session on the run's endpoint plans the work on the main model, hands the writing of code to sub-agents through the provider's own sub-agent tool, reads what they did, and remains accountable for the change. The developer, not a sub-agent, is the one bound by [developer verifies before submitting](../decisions/invariants/developer-verifies-before-submitting.md): a sub-agent may run checks, but the run submits only after the developer has verified the whole result itself.

Delegating is what the run is told to do, not left to the developer's judgment. A run with sub-agents on receives the instruction below. The harness still does not split the work for it: how the change is divided into pieces is the developer's decision.

## The instruction a run with sub-agents on receives

The instruction must say four things, and a wording that drops any of them is a change to this design:

1. Plan the change on the main model: read the work item, the design it names and the code it touches, and decide what has to change and in what order.
2. Hand the writing of code to sub-agents, one bounded piece at a time, telling each which files to change, what the change must do, and how to check it. Write code directly only where handing it over would cost more than writing it, such as a change of a few lines.
3. Read what each sub-agent changed before handing out the next piece.
4. Verify the whole change before submitting: read the complete change and run the checks the developer's instructions name. A sub-agent's account that checks passed is not the developer's verification.

The starting wording is:

```
Sub-agents are on for this run. Plan the change yourself: read the work item, the design it names and the code it touches, and decide what has to change and in what order. Hand the writing of code to sub-agents, one bounded piece at a time, telling each which files to change, what the change must do, and how to check it. Read what each sub-agent changed before you hand out the next piece. Write code yourself only where handing it over would cost more than writing it, such as a change of a few lines. Before you submit, verify the whole change yourself: read the complete change and run the checks your instructions name. A sub-agent's account that checks passed is not your verification.
```

### Where the instruction lives, and who owns it

- The instruction is one Markdown file, `developer-sub-agents.md`, in the harness's own source, beside the code that assembles a developer run's instructions. The build item names the package. The file is built into the program, so every project receives the same text, and a managed project's own files cannot change it.
- The harness adds the file's text to a developer run's instructions only when sub-agents are on for that run, after the developer persona. A run with sub-agents off receives exactly what it receives today.
- It is not in the developer persona, which every developer run receives, and not in a Go string.
- The development manager owns the wording. A change to it is ordinary work, admitted and reviewed like any other change. A change that drops one of the four points above is a change to this design and is the architect's.
- The run's record notes that the instruction was given, with the revision of the program it came from, so the trial and the reviewer can tell which runs received it.

## Configuration

- Sub-agents are off unless configured on. Off keeps today's behaviour exactly, including the developer's allowed tools and instructions.
- The setting belongs to the agent definition: on or off, the sub-agent model, and the sub-agent effort. The model defaults to `sonnet`. The effort is unset by default, so the provider's own default applies.
- A developer slot may override the agent's setting for that slot: on or off and the model. Precedence is the slot, then the agent, then off. This sits beside the slot's endpoint pair and follows the same rule: an explicit slot setting wins.
- The sub-agent model is a model on the same provider and account as the endpoint serving the run. It is validated for each endpoint in the slot's pair, as effort already is, and an option is never forwarded to an endpoint that cannot interpret it.
- An explicitly set effort that the provider cannot apply to a sub-agent is refused at configuration load, never dropped silently.
- Exact field names follow the repository's configuration conventions and appear in the effective configuration output with their origin.
- The settings that apply to a run are part of the run's routing record, written before the first launch. A configuration reload changes them for new runs only.
- Turning sub-agents on by default, or changing the default model, is a change to developer models and is decided by the development manager under [portable agent configuration](portable-agent-configuration.md), after the trial below.

## Providers

An adapter declares whether it offers sub-agents, and for each adapter version, three facts the harness checks with a probe before relying on them: sub-agents run inside the invocation's own process and sandbox; their tool use and messages are streamed on the invocation's event stream; and usage is reported for each model, or only as a total.

- An endpoint whose adapter does not offer sub-agents, or fails any of those three checks, serves the run without them, and the run does not receive the instruction. The run record and status say so in plain words, for example "sub-agents off for this run: Codex does not offer them".
- Configuration load warns once, naming every endpoint the agent could use that will run without sub-agents.
- Configuration load refuses the setting only where no endpoint the agent or slot could use offers sub-agents, because the setting could then never take effect.
- A run that falls back from its primary to its alternate under the execution routing design takes the sub-agent setting with it where the alternate offers sub-agents, and runs without them, saying so and without the instruction, where it does not. Falling back never depends on sub-agents.

Claude Code offers sub-agents today. Codex does not.

## Containment

Each of these is enforced in code and shown by a test. None is left to the model's instructions.

- The harness writes the only sub-agent definition a run can use. Sub-agent definitions from the repository, the user's home or the provider's own configuration are not loaded. Configuration that the repository could otherwise supply would let repository content grant tools.
- A sub-agent's tools are a subset of the run's own allowed tools, minus the sub-agent tool itself. A sub-agent cannot start a sub-agent.
- A sub-agent cannot run in the background and cannot outlive the call that started it.
- A sub-agent works in the run's worktree under the run's sandbox, with the run's write confinement. A sub-agent that the provider would run anywhere else, such as a separate machine or a separate session the harness did not launch, makes the adapter version ineligible.
- The developer's allowed tools gain the sub-agent tool only when sub-agents are on for that run.

## Accounting

- Sub-agent usage is the run's usage. It counts in the run's cost, the work item's cost, the account's usage limits and every spend limit, exactly as the developer session's usage does.
- Usage is recorded for each model on the run. Where the provider reports only a total, the total is recorded as reported and the split between models is recorded as unknown. It is never estimated and shown as reported.
- Each sub-agent call is recorded on the invocation: when it started and ended, the model it ran on, a short description of the task as the developer gave it, how many tools it used, and its usage where reported.
- Status and the dashboard show, for a run, which models did its work and what each cost, with unknown shown as unknown.
- A provider usage limit reached on the sub-agent model alone does not move the run to its alternate endpoint. The developer session continues the work itself, and the run records that sub-agents became unavailable and why. A usage limit on the run's own model is handled by the existing fallback rules.

## Lifetime and time limits

- A sub-agent is part of the invocation that started it. Stopping, timing out or interrupting the invocation ends its sub-agents with it, and the existing execution identity rules, which already cover tool processes that could outlive their parent, cover sub-agents.
- A restarted or reconstructed run never resumes a sub-agent. It starts from the durable record as the execution routing design says, and the record shows any sub-agent call that was in progress as interrupted.
- Every event a sub-agent produces on the invocation's stream counts as output for the limit that ends a run after five minutes without output. A healthy run whose sub-agent is working is therefore not ended. The limit is not lengthened for sub-agents, and an adapter version that does not stream sub-agent events is not allowed sub-agents, as above.
- Time a sub-agent spends counts in the run's own time limits, unchanged.

## Review and checks

- The reviewer is unchanged: one independent reviewer, the same evidence, the same bar. The reviewer is shown, with the run record, which models did which work, and judges the change, not who wrote which line.
- Checks are unchanged.
- Reviewers do not use sub-agents under this design.

## The trial before turning it on by default

After the build and the instruction file land, the development manager turns sub-agents on for one developer slot with a Claude Code primary and runs it until at least ten changes from that slot have merged. Every run in the trial receives the instruction. The trial compares them with changes merged by the other Claude Code slots over the same period on:

- provider cost per merged change, with unknown shares counted and shown;
- how often the first review approved the change;
- repair rounds per change;
- elapsed time per run;
- runs ended for producing no output;
- for the trial slot alone, the share of each run's usage spent on the sub-agent model, so the result shows whether the work was actually handed over.

Sub-agents are turned on by default only if cost per merged change falls and the first review approves no less often. The development manager records the result and the decision. Where the trial's runs spent little on the sub-agent model, the development manager says so in the result, and a change to the instruction's wording is tried before the trial is judged.

## Build pieces

1. The sub-agent build (yoyodyne-23o) as described in the sections above.
2. The instruction file `developer-sub-agents.md` with the starting wording above, built into the program, added to a developer run's instructions only when sub-agents are on for that run, and noted on the run's record. This piece may be admitted as part of the sub-agent build where that is still open, or as its own item. It changes no persona and needs no grant beyond an ordinary developer run's.

## Alternatives rejected

- **Leaving delegation to the developer's judgment.** The trial would then measure whether the main model chooses to hand work over, not what handing it over costs, and the operator's direction was that the main model plans and a cheaper one writes the code.
- **Putting the instruction in the developer persona.** Every developer run receives the persona, including runs with sub-agents off and runs on providers that do not offer them.
- **Putting the instruction in a Go string.** The wording is text a person should read and review as text.
- **A copy of the instruction in each project's `.yoyodyne/personas/`.** Every project would need its own copy, and the copies would drift from the text the trial measured.
- **The harness splitting a run into a planning invocation and separate coding invocations.** Two invocations would need to own one worktree, with a handover protocol between them. The provider's own sub-agent tool already keeps one owner and one sandbox.
- **Choosing the model by work label.** That already exists. It picks one model for a whole run and cannot let the main model plan while a cheaper one writes code.
- **Using sub-agent definitions found in the repository.** Repository content would then decide which tools an agent holds, which configuration may never do.
- **Lengthening the five-minute limit while a sub-agent works.** A silent sub-agent is the same stall the limit exists to end, and lengthening the limit would hide it.
- **Refusing the setting wherever any endpoint lacks sub-agents.** That would block the configured slot pairs that mix providers. Running without sub-agents and saying so keeps routing intact and still never fails silently.

## How a reviewer can tell the build matches

1. With sub-agents off, the developer's allowed tools, instructions and run records are unchanged from before.
2. With them on, the sub-agent tool is allowed; a sub-agent's tools are a subset of the run's, without the sub-agent tool; repository and user sub-agent definitions are ignored; background sub-agents are refused.
3. A sub-agent's writes are confined to the run's worktree, shown by a test that attempts a write outside it.
4. Usage is recorded for each model, unknown splits are unknown, and each sub-agent call is on the record.
5. Stopping, timing out and restarting a run end its sub-agents and never resume one.
6. Sub-agent events count as output for the five-minute limit.
7. A Codex endpoint runs without sub-agents and without the instruction, and says so; configuration with no endpoint that offers them is refused with a plain message.
8. The configuration guide documents the settings, their defaults and their precedence.
9. The instruction is read from `developer-sub-agents.md` built into the program, carries the four points above, is given only to runs with sub-agents on, and is noted on the run's record; the developer persona is unchanged.
