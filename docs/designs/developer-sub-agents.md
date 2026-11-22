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
    - action: amended
      by: architect
      at: 2026-11-15T09:51:34.311772Z
      reason: 'yoyodyne-wj4l.3: carries amendment-33f44efa, approved by the architect on November 15. The instruction is chosen from the run''s main and sub-agent models: Opus main with cheaper sub-agents gets brief, bounded hand-offs with fixed short reports and diff reading; Sonnet main with Opus sub-agents consults Opus for planning and hard decisions and never hands it the writing; every other pair keeps the general four-point instruction. Every arrangement keeps reading what each sub-agent changed and the developer''s own verification. Also folds in one correction from the five corrections item (hs28, part 4): sub-agent settings are settled before a run''s first launch in a slot and again from a new slot that takes it up. Read whole at main 252e349b.'
    - action: amended
      by: architect
      at: 2026-11-22T11:10:25.94603Z
      reason: 'Review against the code, execution group: the instruction table now matches the built family reading (Haiku recognised; a sub-agent model of unrecognised family gets the general instruction), repository sub-agent definitions are refused when called, and the trial section records that Opus main with Sonnet sub-agents ended at higher cost with no quality gain, so sub-agents are off on every slot. No new build. Read whole at main d83037d02c03 with the second reading at 848d86190.'
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
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-15T09:51:34.311772Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2817, for document-2817.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-22T11:10:25.94603Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3441, for document-3441.1 under the automatic approval policy
---
# Developer runs that hand coding to sub-agents on a cheaper model

yoyodyne-t4w. A developer run today is one session on one model. The operator directed on October 14 that a run should plan on the main model and hand the writing of code to sub-agents on a cheaper model, configurably. This design says how, so that the build (yoyodyne-23o) can be implemented and reviewed against it. Revised for yoyodyne-wj4l.2: a run with sub-agents on is told to delegate, rather than left to decide whether to. Revised for yoyodyne-wj4l.3: the instruction a run receives fits the models it runs on, because the trial found that about 95% of an Opus run's cost was the main session reading its own context again, and the operator agreed on November 14 to fit the instruction to each arrangement of models. Revised for the review of every design against the code (yoyodyne-ifd.437.84), from the second reading of the execution, providers and cost designs taken against main at 848d86190: the choice of instruction now says how the built code reads model families, the containment rule says repository definitions are refused when used, and the trial section records the trial's result.

It serves the goal that spend follows the work: the model a piece of work costs is chosen for that piece of work rather than for the role. It must not break the goals that implementation stays isolated in harness-managed worktrees, that provider selection stays configurable without making safety invariants optional, that every change is reviewed by a role that did not write it, and that the operator can see what the harness spends.

It extends two designs and replaces nothing in either. [Provider adapters and execution endpoints](provider-adapters-and-endpoints.md) owns what an adapter declares; this design adds one capability to that declaration. [Execution routing](claude-execution-and-account-routing.md) owns slots, endpoint pairs, pinning, fallback and records; this design adds sub-agent settings to what a run pins and records. It adds no invariant: [configuration never grants authority](../decisions/invariants/configuration-never-grants-authority.md) and [repository writes are physically confined](../decisions/invariants/repository-writes-are-physically-confined.md) already bind every part of it.

## What is built, and where this repository stands

Everything in this design is built: the settings and their precedence, the provider check, the containment rules, the accounting, and the three instructions with the choice between them (`internal/orchestrator/subagents.go`, `internal/backend/claudecode/subagents.go`, `internal/domain/label.go`). This was read from the second reading of the code at main 848d86190, not checked again by hand.

In this repository's own configuration, no agent and no developer slot turns sub-agents on, so every developer run here runs without them and receives no sub-agent instruction. The section on the trial below says why.

## What the run does

The developer session on the run's endpoint plans the work, uses sub-agents through the provider's own sub-agent tool in the way the instruction for its arrangement of models says, reads what they did, and remains accountable for the change. The developer, not a sub-agent, is the one bound by [developer verifies before submitting](../decisions/invariants/developer-verifies-before-submitting.md): a sub-agent may run checks, but the run submits only after the developer has verified the whole result itself.

How sub-agents are used is what the run is told, not left to the developer's judgment. A run with sub-agents on receives one of the instructions below. The harness still does not split the work for it: how the change is divided into pieces is the developer's decision.

## The instructions a run with sub-agents on receives

The harness chooses one instruction from the run's main model and its sub-agent model, as recorded on the run's routing record. It reads each model's family from the configured model name, whether that name is a short alias or a full model identifier. The families it recognises are Opus, Sonnet and Haiku; any other name is a model whose family it does not recognise.

| Main model | Sub-agent model | Instruction |
| --- | --- | --- |
| Opus | Sonnet or Haiku | Opus main with cheaper sub-agents |
| Sonnet | Opus | Sonnet main with Opus sub-agents |
| any other pair, including any pair with a model whose family is not recognised | | the general instruction |

A run whose sub-agent model has a family the harness does not recognise receives the general instruction, even when its main model is Opus. The Opus instruction is written for a sub-agent that is cheaper than the main session, and the harness cannot tell that an unrecognised model is. The alternative of treating every model that is not Opus as cheaper was rejected for that reason: it would hand the bulk of the work to a model whose price nothing has checked. Adding a family to the recognised list is a change to this table and is the architect's.

Two points hold in every instruction, and a wording that drops either of them is a change to this design:

- Read what each sub-agent changed before relying on it.
- Verify the whole change before submitting: read the complete change and run the checks the developer's instructions name yourself. A sub-agent's account that checks passed is not the developer's verification.

### Opus main with cheaper sub-agents

The Opus session's own context is the expensive part, so this instruction keeps what enters it short. It must say:

1. Plan the change on the main model: decide what has to change and in what order.
2. Hand exploration of the code to a sub-agent that returns a short summary of what it found, not the contents of the files it read.
3. Hand the writing of code, and the loop of running checks and fixing what fails, to sub-agents, one bounded piece at a time. Each piece is handed over with a brief that names the files, the intended change, the conditions under which the piece is done, and the repository's own conventions that apply. Each sub-agent reports back in a fixed format of a few lines: what it changed, which checks it ran, and what is left. Write code directly only where handing it over would cost more than writing it, such as a change of a few lines.
4. Read what each sub-agent changed as a diff, rather than reading the changed files whole again.
5. Verify the whole change before submitting: read the complete diff and run the final checks yourself.

### Sonnet main with Opus sub-agents

Here the sub-agent is the stronger and dearer model, so it advises and the main session writes. It must say:

1. Consult an Opus sub-agent for the plan and for hard decisions, giving it the work item, the design it names and the code in question, and asking for advice, not for changes.
2. Never hand the writing of code to the Opus sub-agent: write the change yourself.
3. Verify the whole change before submitting: read the complete change and run the checks your instructions name yourself.

This instruction leaves out handing the writing to sub-agents, and that is deliberate.

### The general instruction

Every other pair receives the general instruction, which must say four things:

1. Plan the change on the main model: read the work item, the design it names and the code it touches, and decide what has to change and in what order.
2. Hand the writing of code to sub-agents, one bounded piece at a time, telling each which files to change, what the change must do, and how to check it. Write code directly only where handing it over would cost more than writing it, such as a change of a few lines.
3. Read what each sub-agent changed before handing out the next piece.
4. Verify the whole change before submitting: read the complete change and run the checks the developer's instructions name. A sub-agent's account that checks passed is not the developer's verification.

Its starting wording is:

```
Sub-agents are on for this run. Plan the change yourself: read the work item, the design it names and the code it touches, and decide what has to change and in what order. Hand the writing of code to sub-agents, one bounded piece at a time, telling each which files to change, what the change must do, and how to check it. Read what each sub-agent changed before you hand out the next piece. Write code yourself only where handing it over would cost more than writing it, such as a change of a few lines. Before you submit, verify the whole change yourself: read the complete change and run the checks your instructions name. A sub-agent's account that checks passed is not your verification.
```

The two other instructions start from the wording their build (yoyodyne-wj4l.3) carries, provided it says every point above.

### Where the instructions live, and who owns them

- Each instruction is one Markdown file in the harness's own source, beside the code that assembles a developer run's instructions. The general instruction stays in `developer-sub-agents.md`; the build names the other two files. The files are built into the program, so every project receives the same text, and a managed project's own files cannot change it.
- The harness adds the chosen file's text to a developer run's instructions only when sub-agents are on for that run, after the developer persona. A run with sub-agents off receives exactly what it would receive without this design.
- None of them is in the developer persona, which every developer run receives, and none is a Go string.
- The development manager owns the wording. A change to it is ordinary work, admitted and reviewed like any other change. A change that drops a point this design requires of an instruction, or changes which pair receives which instruction, is a change to this design and is the architect's.
- The run's record notes which instruction was given, with the revision of the program it came from, so the trial and the reviewer can tell which runs received which.

## Configuration

- Sub-agents are off unless configured on. Off keeps the behaviour of a run without this design exactly, including the developer's allowed tools and instructions.
- The setting belongs to the agent definition: on or off, the sub-agent model, and the sub-agent effort. The model defaults to `sonnet`. The effort is unset by default, so the provider's own default applies.
- A developer slot may override the agent's setting for that slot: on or off and the model. Precedence is the slot, then the agent, then off. This sits beside the slot's endpoint pair and follows the same rule: an explicit slot setting wins. It is built; no slot in this repository's configuration uses it.
- The sub-agent model is a model on the same provider and account as the endpoint serving the run. It is validated for each endpoint in the slot's pair, as effort already is, and an option is never forwarded to an endpoint that cannot interpret it.
- An explicitly set effort that the provider cannot apply to a sub-agent is refused at configuration load, never dropped silently.
- Exact field names follow the repository's configuration conventions and appear in the effective configuration output with their origin.
- The settings that apply to a run, and the instruction they choose, are settled before the run's first launch in a slot and written to the run's routing record. A run taken up again in a different slot settles them again from that slot before its first launch there, and the record keeps both. A configuration reload changes them for new runs only.
- Turning sub-agents on by default, or changing the default model, is a change to developer models and is decided by the development manager under [portable agent configuration](portable-agent-configuration.md), after a trial as below.

## Providers

An adapter declares whether it offers sub-agents, and for each adapter version, three facts the harness checks with a probe before relying on them: sub-agents run inside the invocation's own process and sandbox; their tool use and messages are streamed on the invocation's event stream; and usage is reported for each model, or only as a total.

- An endpoint whose adapter does not offer sub-agents, or fails any of those three checks, serves the run without them, and the run receives no sub-agent instruction. The run record and status say so in plain words, for example "sub-agents off for this run: Codex does not offer them".
- Configuration load warns once, naming every endpoint the agent could use that will run without sub-agents.
- Configuration load refuses the setting only where no endpoint the agent or slot could use offers sub-agents, because the setting could then never take effect.
- A run that falls back from its primary to its alternate under the execution routing design takes the sub-agent setting with it where the alternate offers sub-agents, and receives the instruction for the alternate's models. Where the alternate does not offer them, the run goes on without sub-agents and without an instruction, and says so. Falling back never depends on sub-agents.

Claude Code offers sub-agents today. Codex does not.

## Containment

Each of these is enforced in code and shown by a test. None is left to the model's instructions.

- The harness writes the only sub-agent definition a run can use: it is given on the provider's command line when the run starts. A sub-agent definition from the repository, the user's home or the provider's own configuration may be visible to the provider, but a call that asks for any sub-agent other than the harness's own is refused by the harness when it is made, so no such definition is ever used. Configuration that the repository could otherwise supply would let repository content grant tools.
- A sub-agent's tools are a subset of the run's own allowed tools, minus the sub-agent tool itself. A sub-agent cannot start a sub-agent.
- A sub-agent cannot run in the background and cannot outlive the call that started it.
- A sub-agent works in the run's worktree under the run's sandbox, with the run's write confinement. A sub-agent that the provider would run anywhere else, such as a separate machine or a separate session the harness did not launch, makes the adapter version ineligible.
- The developer's allowed tools gain the sub-agent tool only when sub-agents are on for that run.

Refusing a foreign definition when it is called, rather than keeping it from being loaded at all, is enough: what this rule protects is which tools a sub-agent holds, and a definition that can never run grants none.

The instruction that an Opus sub-agent advises and does not write is an instruction, not a containment rule: the Opus sub-agent holds the same tools as any other sub-agent, and the developer is accountable for what it changes.

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

- The reviewer is unchanged: one independent reviewer, the same evidence, the same bar. The reviewer is shown, with the run record, which models did which work and which instruction the run received, and judges the change, not who wrote which line.
- Checks are unchanged.
- Reviewers do not use sub-agents under this design.

## The trial, and what it found

The rule for a trial is unchanged. Before sub-agents are turned on by default, the development manager turns them on for one developer slot with a Claude Code primary and runs it until at least ten changes from that slot have merged. Every run in the trial receives the instruction for its arrangement. Where more than one arrangement is tried, each is judged on its own changes and the results are not pooled. The trial compares them with changes merged by the other Claude Code slots over the same period on:

- provider cost per merged change, with unknown shares counted and shown;
- how often the first review approved the change;
- repair rounds per change;
- elapsed time per run;
- runs ended for producing no output;
- for the trial slot alone, the share of each run's usage spent on the sub-agent model, so the result shows whether the work was used as the instruction asks.

Sub-agents are turned on by default only if cost per merged change falls and the first review approves no less often. The development manager records the result and the decision for each arrangement tried. Where an arrangement's runs used the sub-agent model differently from what its instruction asks, the development manager says so in the result, and a change to that instruction's wording is tried before the arrangement is judged.

One arrangement has been tried: Opus as the main model with Sonnet sub-agents, on slot 2, under the trial for yoyodyne-wj4l. Its result, as this repository's configuration records it in a comment beside slot 2, is that it cost more without a gain in quality. That arrangement was ended, sub-agents are off on every slot, and sub-agents are not on by default. The comment is the only record of the result this revision read; a fuller record with the figures above, where the development manager keeps one, stands over it.

The Sonnet main with Opus sub-agents arrangement and the general instruction have not been tried. Trying either is the development manager's decision under the rule above and needs no change to this design. Slot 2 now trials Sonnet as its main model with sub-agents off; that is a choice of the slot's model, decided as any slot's model is, and is not a trial under this design. Which model a slot runs as its main or sub-agent model is that slot's configuration.

## Build pieces

All three are built.

1. The sub-agent build (yoyodyne-23o) as described in the sections above.
2. The instruction file `developer-sub-agents.md` with the starting wording above, built into the program, added to a developer run's instructions only when sub-agents are on for that run, and noted on the run's record.
3. The two instructions for named arrangements, and the choice between all three from the run's main and sub-agent models (yoyodyne-wj4l.3), with tests showing each arrangement receives its own instruction and the run's record names which one it received. It changes no persona and needs no grant beyond an ordinary developer run's.

This revision asks for no new build: each place where the design and the code differed is settled by the design describing the code.

## Alternatives rejected

- **Leaving delegation to the developer's judgment.** The trial would then measure whether the main model chooses to hand work over, not what handing it over costs, and the operator's direction was that the main model plans and a cheaper one writes the code.
- **One instruction for every arrangement of models.** The trial showed that an Opus main session's cost is mostly its own context read again, so for that pair what matters is keeping exploration and repeated check output out of the main session. For a Sonnet main session with an Opus sub-agent, handing the writing to Opus would send the bulk of the work to the dearer model, the opposite of what spend following the work asks.
- **Giving the Opus instruction to any sub-agent model that is not Opus.** A model whose family the harness does not recognise may not be cheaper, and the Opus instruction only fits a cheaper sub-agent.
- **Choosing the instruction by slot rather than by models.** The models a run uses are what decide which instruction fits, and a run that falls back or moves to another slot changes its models; the slot is only where those models are configured.
- **Putting the instructions in the developer persona.** Every developer run receives the persona, including runs with sub-agents off and runs on providers that do not offer them.
- **Putting the instructions in Go strings.** The wording is text a person should read and review as text.
- **A copy of the instructions in each project's `.yoyodyne/personas/`.** Every project would need its own copy, and the copies would drift from the text the trial measured.
- **The harness splitting a run into a planning invocation and separate coding invocations.** Two invocations would need to own one worktree, with a handover protocol between them. The provider's own sub-agent tool already keeps one owner and one sandbox.
- **Choosing the model by work label.** That already exists. It picks one model for a whole run and cannot let the main model plan while a cheaper one writes code.
- **Using sub-agent definitions found in the repository.** Repository content would then decide which tools an agent holds, which configuration may never do.
- **Lengthening the five-minute limit while a sub-agent works.** A silent sub-agent is the same stall the limit exists to end, and lengthening the limit would hide it.
- **Refusing the setting wherever any endpoint lacks sub-agents.** That would block the configured slot pairs that mix providers. Running without sub-agents and saying so keeps routing intact and still never fails silently.

## How a reviewer can tell the build matches

1. With sub-agents off, the developer's allowed tools, instructions and run records are the same as a run without this design.
2. With them on, the sub-agent tool is allowed; a sub-agent's tools are a subset of the run's, without the sub-agent tool; a call for any sub-agent other than the harness's own, including one defined in the repository or the user's home, is refused; background sub-agents are refused.
3. A sub-agent's writes are confined to the run's worktree, shown by a test that attempts a write outside it.
4. Usage is recorded for each model, unknown splits are unknown, and each sub-agent call is on the record.
5. Stopping, timing out and restarting a run end its sub-agents and never resume one.
6. Sub-agent events count as output for the five-minute limit.
7. A Codex endpoint runs without sub-agents and without an instruction, and says so; configuration with no endpoint that offers them is refused with a plain message.
8. The configuration guide documents the settings, their defaults and their precedence.
9. Each instruction is read from a Markdown file built into the program and carries the points this design requires of it; the general instruction is `developer-sub-agents.md`; an instruction is given only to runs with sub-agents on, and the run's record names which one; the developer persona is unchanged.
10. The instruction is chosen from the run's main and sub-agent models as the table above says, whether the models are named by alias or by full identifier; Haiku is recognised as a family; a sub-agent model whose family is not recognised gets the general instruction; and a test shows each of the three arrangements receiving its own instruction.
11. A run taken up again in a different slot settles its sub-agent settings from that slot before its first launch there, and its record keeps both settings.
