---
id: workflow-diagrams
kind: design
title: Drawing a project's workflows on request, on the command line and the dashboard
supports:
    - v1-goals
    - configurable-workflows
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-20T19:44:42.539653Z
      reason: 'yoyodyne-y06u: new design for the workflow visualizer the operator asked for on November 19. It sets the drawing component and its text, SVG and Mermaid formats, the yoyo workflow draw command and the dashboard page with their labels, and three fixed descriptions beside the code, each with a drift test. It states the three limits as rules and lists ten build pieces in order. Written from main b25a89f9014b.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-20T19:44:42.539653Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3214, for document-3214.1 under the automatic approval policy
---
# Drawing a project's workflows on request

## What this is for

The operator asked on November 19, 2026 for the harness to draw the workflows a project has configured, like the five diagrams his assistant drew by hand that week: an overview, intake, inside the harness, the merge queue, and what happens after a change lands. Drawn by hand, they stop being true the first time the configuration or the code changes. Drawn by the harness from what it actually runs, they stay true.

This serves the goal that the harness's surfaces read clearly, and the brief's goal that the operator can see what the system does on their behalf. It builds on [configurable workflows](configurable-workflows.md), which made the delivery loop a definition a person can read; this design makes that definition, and the parts of the work that are still only Go code, something a person can see.

## The rules

These hold for everything this design describes, and each is enforced in code rather than left to habit.

1. **A fixed description describes the code and never drives it.** No code chooses a next step from a fixed description. The loader that runs workflows refuses a file marked as a description, and the loader for descriptions refuses a runnable workflow.
2. **Diagrams change nothing.** Drawing a diagram reads configuration and definitions and writes nothing: no state, no record, no file. Nothing drawn is stored; every diagram is drawn when it is asked for.
3. **Nothing here widens what a workflow definition may configure.** The runnable workflow schema is unchanged. The fields a fixed description adds are accepted only by the description loader.

## The drawing component

One component in `internal/workflow` turns a workflow into a diagram. Every surface uses it, so the command line and the dashboard can never draw the same workflow differently.

**Input.** The compiled, normalized definition the workflow package already produces, which is the same definition `yoyo workflow show --effective` prints, or a compiled fixed description. Optionally, a list of labels, each attached to one step by name. The component reads nothing else: it does not load configuration itself.

**What it works out first.** From the input it builds one plain description of the drawing: the start step; every step with its action and summary; every transition with the outcome that takes it; every end with its summary; and, for each transition, whether it goes forward, back to an earlier step, or back to the same step. Steps are ordered by their shortest distance from the start, and within one distance in the order the definition lists them. Every format below is written from this one description.

**Output, three formats:**

- **Text**, the default on a terminal, and the whole of what a terminal without graphics needs. Every distinction is a word, never a colour or a symbol alone. It looks like this for part of the delivery workflow:

```
delivery: claim a work item, develop a change, check it, and promote it
starts at: claim

step develop (action candidate.develop)
  invoke the developer in this run's worktree, and publish what the attempt produced
  run by: developer, routed per developer slot (from execution settings)
  when produced, goes to: check
  when reissued, goes back to: develop (the same step)

end delivered
  the change is on the target branch
```

  Labels appear as lines under their step, each ending with the setting it came from. An end is always written as "end", a transition to an earlier step as "goes back to", and one to the same step says so in words.

- **SVG**, a picture the harness lays out itself, with no outside program and no script library. Steps sit in columns by their distance from the start. Forward transitions are straight arrows labelled with their outcome; transitions back are drawn as curves below, labelled the same way; a transition to the same step is a labelled loop. The start has an arrow marked "start", and every end has a double border and the word "end" in it, so nothing depends on colour. The same input always gives the same bytes, so a test can compare the output exactly.
- **Mermaid**, as text a person can paste into a document that renders it. Nothing in the harness renders it.

## The command and the dashboard page

**The command:** `yoyo workflow draw [workflow id] [--format text|svg|mermaid] [--bare]`. With no id it draws `delivery`. `overview` draws the overview below. `--bare` leaves the labels out. It prints to standard output and changes nothing.

**The dashboard page:** one page listing the project's workflows, the overview first, each drawn as SVG with its text outline beneath it, so a reader without graphics loses nothing. It takes no action and offers none.

Both surfaces read the workflows and their labels through one new reading in the read model: the effective definitions, the fixed descriptions, and the labels for each, as of the configuration the running harness has loaded. Both then hand that reading to the drawing component. This keeps the rule in [surfaces project one read model](../decisions/invariants/surfaces-project-one-read-model.md).

**What each diagram is labelled with.** Every label comes from the effective configuration or from the harness's own list of steps, never from text written for the diagram, and each names the setting it came from:

- **Which role acts at a step.** This comes from the action's registered description of the authority it needs and the role bundle that holds that authority. For a developer or reviewer step, it also names the backend and model each is routed to.
- **Which checks run.** On a step whose action runs the project's checks: the name of each check the project declares.
- **Who approves a landing.** On the step that promotes a change: how the project lands changes (directly, through the harness's merge queue, to a local branch, or merged by a person), and whether a person's approval is required.
- **On the overview:** who admits work at intake, and the approval setting for each kind of document.

No label carries a credential, an account identifier or a token. A setting the harness cannot read is labelled as not read, never left out silently.

## The three fixed descriptions

Intake, the merge queue, and what happens after a change lands are steps in Go code, not definitions. Each gets a fixed description, a file written in the same shape as a workflow definition so the same component can draw it.

**Where they live.** Each file sits in the Go package that holds the code it describes, named `intake.workflow.yaml`, `queue.workflow.yaml` and `landed.workflow.yaml`, and is built into the binary with that package. Which package holds each is settled by the build piece that writes it, after reading that code. This design does not name the packages, because they were not read when it was written.

**Their shape.** The same states, transitions and ends as a runnable definition, with these differences, which only the description loader accepts:

- `describes: code` at the top, which marks the file as a description;
- on each state, `code:`, naming the Go function that performs that step;
- on an end, `next:`, naming the workflow it hands work on to, where it hands work on;
- at the top, `follows:`, naming the workflow, and the step or end within it, that this one starts from.

The delivery definition is not changed. The overview is drawn from these `follows` and `next` links alone: one box per workflow, joined where one hands work to another.

**How each drift test finds the code and the description disagreeing.** Each description has its own test, in its own package, in two halves.

1. **Names.** Every `code:` names a function that exists in the package. The outcomes a description lists for a step must match the set of outcomes the code declares for that step, in both directions: an outcome the code returns that the description lacks fails, and one the description lists that the code never returns fails. The code declares a step's outcomes as one typed list beside the function, which is the only change this asks of that code.
2. **Paths.** While the package's existing tests run, each step records the step it ran and the outcome it returned through one recording call that decides nothing. Every recorded move from one step to the next must be a transition the description allows. This is how the delivery definition is already held to the paths the pipeline records.

A failure names the description, the step and the outcome, and says which side lacks it. Changing the code without changing its description, or the other way round, fails `make test`.

## Cost, restarts and what anyone can see

Drawing calls no AI provider and costs nothing but local reading. It holds no lease and keeps nothing, so a restart affects nothing: the next request draws again from what is loaded. The drift tests run in `make test` with the packages they cover, adding only the recording calls to tests that already run.

## Alternatives rejected

- **Graphviz.** Every machine would have to install a program before a diagram could be drawn, which the readme alone would then have to explain.
- **Drawing in the dashboard with a script library.** That would be a second drawing source beside the text outline, and the two could disagree. One layout in Go writes every format.
- **Converting intake, the merge queue and what happens after landing into workflows that actually run.** That is the conversion order the configurable workflows design already sets, under its own conditions. A description that only describes the code is the cheap, honest step now, and it is the starting point for converting later.
- **Diagrams written by hand into the documentation.** They go out of date, which is the problem the operator asked to end.
- **Storing drawn diagrams.** A stored drawing goes out of date the same way.

## What to build

In this order, for the Lead Product Manager to admit and the development manager to split further if needed:

1. The drawing component in `internal/workflow`, with the plain description of the drawing and the text, SVG and Mermaid formats. Tests compare its output for this project's delivery definition exactly.
2. `yoyo workflow draw` for runnable workflows, without labels.
3. The read model's workflows reading with labels, and the command drawing from it.
4. The description loader and its fields, with both loaders refusing each other's files, and a test of each refusal.
5. The merge queue's description and its drift test.
6. The intake description and its drift test.
7. The description of what happens after a change lands, and its drift test.
8. The overview, drawn from the descriptions' links.
9. The dashboard page.
10. The operations guide's description of `yoyo workflow draw` and the dashboard page.

## What a reviewer can check

- Drawing any workflow writes nothing anywhere.
- The loader that runs workflows refuses a file with `describes: code`, and no code chooses a step from a description.
- The text format carries every distinction the SVG does, in words.
- Every label names the setting it came from and carries no credential.
- Changing a described step's outcomes, or the paths between its steps, without changing its description fails `make test`, naming the step.
- The command line and the dashboard draw from one reading of the read model.
