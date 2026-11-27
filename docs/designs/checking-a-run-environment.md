---
id: checking-a-run-environment
kind: design
title: Checking before implementation that a run can reach the environments its work item's criteria need
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-26T23:00:02.114784Z
      reason: 'The Lead Product Manager widened the environment check item (yoyodyne-ifd.429.314) at about 15:05 PST on November 26 to cover prerequisites installed for the whole machine and tools the developer''s sandbox refuses. This replaces document-4169.1: a person installs a machine prerequisite from a stated instruction the harness never runs, and an environment may run its check and test where the declared checks run. Nothing widens the developer''s sandbox.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-26T23:00:02.114784Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4173, for document-4173.1 under the automatic approval policy
---
# Checking a run's environment before implementation starts

Serves the goal that Yoyodyne supports development in any language: verification is whatever commands the project declares, and no language, build system or test framework is built into the harness. It answers the item asking that the harness check, before implementation starts, that the environment a work item's criteria need is available to the run (yoyodyne-ifd.429.314).

It keeps four other goals whole:

- implementation runs in harness-managed worktrees;
- safety invariants hold whatever the configuration says;
- every change is reviewed by a role that did not write it;
- the owner's attention is spent only where it is needed.

It builds on [installing a project's dependencies before a developer run](project-dependencies-in-runs.md) and changes nothing that design says.

## What happened

The operator reported this on November 26, 2026, from a run on another project on this machine. The item's criteria needed a real test client on two client platforms. The run built the change, and its own checks passed. Partway through implementation it found it had no test client: installing the client library from a public package registry was refused by the run's network sandbox, as designed.

The reviewer approved the evidence only, so the item was rightly left open, but only after a whole run's cost. One platform was later checked by hand, and the other is still unchecked.

The operator then raised two more cases of the same gap:

- **Prerequisites that install outside the repository.** Phone simulators for iOS and Android, an Android system image, and a test browser downloaded into the user's caches install for the whole machine. An install step may only produce ignored folders inside the checkout, so none can provide them.
- **Tools that are installed but refused at run time.** A developer run measured on September 19, 2026 showed headless Chrome stopping when it tried to create the socket file it makes on start, because the developer's sandbox refuses every socket file. Whether a simulator, which talks to the system's simulator service, can run inside that sandbox at all has not been measured.

The gap is before implementation, not at review.

## Decision

### 1. A project declares its environments

```yaml
environments:
  - name: android client
    needs: [client library]
    check: scripts/ready-android.sh
    test: scripts/integration-android.sh
    time_limit: 15m
  - name: web ui
    machine: true
    how_to_install: "npx playwright install chromium"
    where: harness
    check: scripts/ready-browser.sh
    test: scripts/integration-web.sh
```

- `environments` is a list, empty by default. An empty list changes nothing about any run.
- `name` is how an item refers to the environment.
- `needs` names install steps, by their names in `install`, that must have succeeded in the checkout before the check runs. This is the only way the harness fetches anything for an environment, and the install step keeps every rule its own design gives it.
- `machine`, false by default, says the environment needs something installed for the whole machine rather than in the checkout. Section 2 says what follows.
- `how_to_install` is the plain instruction a person follows to install a `machine` prerequisite. The harness shows it as written and never runs it.
- `where` is `developer` by default, or `harness`. Section 3 says what each means.
- `check` is a short command that exits zero when the environment is usable. It must run the tool, not only find it: a browser check opens a page, and a simulator check boots a device and runs one command on it. It changes no state outside the folders section 6 gives it.
- `test` is the integration test that judges the criteria this environment serves.
- `time_limit` bounds one run of `check` or `test`. It defaults to 15 minutes.

The configuration is refused when it is read in any of these cases:

- two entries share a name;
- `needs` names an install step the project does not declare;
- `how_to_install` is set without `machine: true`;
- `where` is neither `developer` nor `harness`.

Like `install`, `environments` is the operator's to set and is refused for a role's write under section 6 of [portable agent configuration](portable-agent-configuration.md). These commands decide what runs on the machine, and a role may recommend one in a report.

### 2. A prerequisite installed for the whole machine is installed by a person

The harness never installs a `machine` prerequisite, and nothing in a run does either. An unattended factory changing software for the whole machine would reach outside every boundary the harness keeps for its own writes. That is a real safety boundary, and it is the reason a person installs it, not a missing tool.

The readiness check confirms it exactly as any other environment, by running the tool. Where the check fails for a `machine` environment, the development manager's notice and the item's note quote `how_to_install` and say that a person has to run it on this machine.

The development manager raises it under the reason [who owns what](ownership-and-the-operator.md) gives for a program that needs installing. That reason is not yet built. Until it is, her report to the Lead Product Manager says in plain words what to install and why.

### 3. Where the check and the test run

- **`where: developer`.** The check and the test run under exactly the restrictions the developer session's own commands run under, with no network and no place to write beyond what that session has. This is what makes a passing check true for the developer: the developer can run the same test itself before submitting.
- **`where: harness`.** The check and the test run where the project's declared checks run, outside the developer's sandbox, through the same runner and under its limits on how many run at once. The developer cannot run them. The developer is told the environment exists and which criteria it serves, and is given each result after submitting.

`where: harness` is for a tool the developer's sandbox refuses, such as a browser that needs a socket file, or a simulator that needs the system's simulator service.

Neither choice widens the developer's sandbox. A `harness` environment gets no more network or write access than the declared checks already run with. Build piece 1 establishes what those restrictions are.

I chose this over reporting each such criterion as one the developer cannot verify. Reporting alone would leave the item open until a person checked it by hand, which spends the owner's attention on routine verification.

### 4. An item names the environments its criteria need

An item names them on one line of its description, beginning `Environments:`, with the criteria each serves, for example:

`Environments: android client (criteria 1, 2); ios client (criterion 3)`

- The Lead Product Manager writes the line when she admits the item, and the development manager when she splits one.
- The harness reads the line when it reserves the run.
- Admission is refused, naming the line, where an item names an environment the project does not declare.
- An item without the line needs no environment, and its run is unchanged.

A description line was chosen over a label because labels name areas of work, and over a separate field because the tracker has none the harness could rely on for every project.

### 5. The check runs before the first developer turn

After the worktree is made and its due install steps have run, and before the developer's first turn, the harness runs each named environment's `check`, in the place section 3 gives it.

A `developer` check is never given the network access an install step has. That would answer a different question: whether the environment exists on the machine, not whether the run can use it.

Each environment's result is recorded on the run apart from the others:

- its name;
- passed, failed or did not finish in time;
- where it ran;
- the criteria it serves;
- the last 200 lines of its output, at most 16 KiB, with secrets removed.

The outcome follows from those results:

- **Every named environment passes.** The run goes on as today.
- **None passes.** The run ends before any developer turn, with no repair attempt and no developer attempt spent. The item is held for the development manager, naming each missing environment, the criteria it blocks, the check's output, and for a `machine` environment the instruction from section 2. She decides what follows:
  - Where the project's declaration is wrong, that is the operator's to write, and she raises it in a report.
  - Where a person has to install something, she raises it as section 2 says.
  - Otherwise she parks the item or has it split.
- **Some pass.** The run goes on. Each environment that failed is recorded as still to be checked, with its criteria, and is shown so on the run, on yoyo status, in the reviewer's evidence and in the item's notes. One environment passing never hides another.

A resumed or continued run runs the checks again before its next developer turn, because a machine can change between turns. An environment that now passes stops being recorded as still to be checked.

### 6. Each available environment's test is recorded against the commit it tested

After the developer submits, and before review, the harness runs each passing environment's `test` at the submitted head, in the place section 3 gives it. Each result is recorded with the commit it ran on.

- A failure goes back to the developer as a failed check, with the test's output, and spends one repair round, as any failed check does. For a `harness` environment this is the only way the developer learns the result.
- The reviewer is given each environment's result with its commit and where it ran, and the list of environments still to be checked. The reviewer judges the change on that evidence.
- A result on a commit other than the one that lands is never shown as evidence for the landed one, as [integration requires revision bound evidence](../decisions/invariants/integration-requires-revision-bound-evidence.md) already holds.

Each run of `check` or `test` is given an empty folder of its own under the run's scratch space, as [developer run scratch outside Git](../decisions/developer-run-scratch-outside-git.md) places it, through `YOYODYNE_TEST_SCRATCH`. The folder is removed afterwards, so a test that changes state works on data that is thrown away.

A disposable server, a booted simulator device or a browser profile is the project's test to start and stop inside that folder. The harness starts none.

### 7. A change that lands with environments still to be checked leaves its item open

When such a change lands, the item is not closed. The harness writes a note naming each environment still to be checked, its criteria, and the landed commit, and the item goes to the development manager's list.

She may decide a fresh run of the item once she judges the environment available, for example after a person has installed a `machine` prerequisite. That run:

- starts from main;
- runs the checks;
- runs only the pending environments' tests, at main's head;
- records each result against that commit.

Where all pass, the item closes with no developer turn. Where a test fails, the run goes on as an ordinary run to repair it, from that main.

### 8. Nothing widens

This design gives no developer session, reviewer session or check any network access, package cache or place to write that it did not already have:

- The only network access stays the install step's, outside every agent session, exactly as its design gives it.
- A `harness` environment runs with what the declared checks already run with, and no more.
- Nothing installs software for the whole machine.

The rule that [a developer verifies before submitting](../decisions/invariants/developer-verifies-before-submitting.md) is unchanged. The developer runs every check it can run, including each `developer` environment's test. A `harness` environment's test is a check the developer cannot run, and like the merge candidate's checks it is the harness's to run after submitting.

## Alternatives rejected

- **Checking at review.** That is where the gap was found, after a whole run's cost. Approving evidence only already keeps such an item open.
- **Running a `developer` check with the install step's network access.** It would say the environment exists on the machine, not that the run can use it, which is the question the operator's run got wrong.
- **A check that only finds the tool.** Headless Chrome was installed and still could not start, so only running it answers the question.
- **The harness installing machine prerequisites, through a declared command run outside the sandbox.** An unattended factory would change software for the whole machine, outside every boundary it keeps for its own writes.
- **Widening the developer's sandbox for socket files or the simulator service.** It would weaken a safety boundary for every run to serve some items.
- **Reporting a criterion the sandbox refuses as one the developer cannot verify, and stopping there.** The item would wait on a person for routine verification.
- **Holding the run when any environment is missing.** Work that one environment can judge would wait on another nobody can supply yet, and the slot would sit idle.
- **Closing the item on landing and admitting a follow up for each pending environment.** The criteria belong to the item, and closing it says they were met.
- **The harness reading criteria from prose.** It cannot tell which criterion needs which environment, so the item says so on one fixed line.
- **The harness starting servers, simulators or browsers itself.** That would build knowledge of particular tools into the harness.

## What is enforced in code

- A `developer` check or test runs under the developer session's own restrictions, never with an install step's network access.
- A `harness` check or test runs through the declared checks' runner, with no more access than those checks have.
- No command in `how_to_install` is ever run by the harness.
- A run whose named environments all fail ends before any developer turn, spending no attempt.
- Each environment's result is recorded and shown apart from every other.
- An item with an environment still to be checked is not closed on landing.
- Admission refuses an item naming an undeclared environment.
- `environments` is refused for a role's write.

## What a reviewer can check

- With no `environments` declared, or no `Environments:` line on the item, a run is unchanged.
- With a named environment's install step absent and network access denied, the check fails, the run ends before any developer turn, and the item is held naming the environment and the criteria it blocks. No developer attempt is recorded.
- With a `machine` environment missing, the held item's note quotes its `how_to_install` text, and a test shows the harness never ran it.
- With the environment available under the same restrictions, a fresh run runs its test, and the result is recorded with the commit it tested.
- A `harness` environment's check and test run through the declared checks' runner, and the developer session is given their results only after submitting.
- With two environments named and one available, the run goes on, and the other's criteria are shown as still to be checked after the first passes and after landing; the item stays open.
- A fresh run decided for an item that landed with pending environments runs only their tests at main's head, and closes the item with no developer turn when they pass.
- A test shows that a `developer` check cannot reach the network where the developer session cannot.

## Build pieces, in order

Each is sized to [the size of a work item](../decisions/work-item-size.md). The Lead Product Manager owns admission and order; this design creates no items.

1. **The setting and the line.** The `environments` setting with all its fields, its validation, its refusal for a role's write, and yoyo doctor showing it; reading the `Environments:` line and refusing admission for an undeclared name. This piece first reads the code that runs the declared checks and records, in the piece's notes, what restrictions it runs commands under, and whether it can run a command under the developer session's restrictions. If it cannot, building that is its first part.
2. **The check before the first turn.** Running each check in the place its `where` gives, recording each result apart, ending the run with nothing spent when none passes, holding the item for the development manager with the `how_to_install` text where it applies, and the plain wording on status and the dashboard. Needs piece 1.
3. **The tests at the submitted head.** Running each passing environment's test before review, the scratch folder for each run of a check or test, sending a failure back as a failed check, and giving the reviewer each result with its commit and where it ran. Needs piece 2.
4. **Landing with environments still to be checked.** Leaving the item open with its note, putting it on the development manager's list, and the fresh run that runs only the pending tests at main's head and closes the item when they pass. Needs piece 3.
5. **The guide.** The setting, the line, the two places a check can run, and machine prerequisites described in the runs guide, with one example for a browser test and one for a simulator. Needs piece 4.
