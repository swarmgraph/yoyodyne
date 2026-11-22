---
id: project-dependencies-in-runs
kind: design
title: Installing a project's dependencies before a developer run, without widening what the run may reach
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-29T02:27:08.360027Z
      reason: 'yoyodyne-h6sh: the operator wants Yoyo to work on projects whose tests need packages a fresh checkout lacks, and a developer run cannot fetch them. The project declares an install step; the harness runs it with network access outside the developer''s session; failures are sorted by cause; the rule on developers verifying before submitting is not relaxed; six build pieces named.'
    - action: amended
      by: architect
      at: 2026-11-22T06:44:04.577606Z
      reason: 'Brought up to date against the code at main 3b1057ae4bdf: pieces 1 to 5 are built and piece 6 is not; an outside failure gets four attempts, not three; before a developer turn a failure on changed inputs lets the turn go ahead; the step runs before every developer turn, the landing checks and the merge queue checks, which run it once without sorting; the record of what installed on this machine is named. Done under the review of every design against the code.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-29T02:27:08.360027Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1923, for document-1923.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-22T06:44:04.577606Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3418, for document-3418.1 under the automatic approval policy
---
# Installing a project's dependencies before a developer run

Serves the goal that Yoyodyne supports development in any language: verification is whatever commands the project declares, and no language, build system, or test framework is built into the harness. It answers the design item for a project's declared dependencies (yoyodyne-h6sh), from the operator's direction of October 28, 2026.

It keeps three other goals whole:

- safety invariants hold whatever the configuration says;
- implementation runs in harness-managed worktrees;
- a newcomer can reach a first run from the readme alone.

The standing goals apply throughout. Every state below is shown in ordinary words, and every decision named here belongs to a role.

## What is built and what is not

Checked against the code on November 21, 2026, at main `3b1057ae4bdf`.

- Built: build pieces 1 to 5. The `install` setting and its checks when the configuration is read; running the step before the developer starts and again wherever it is due; sorting a failure by whose it is, with the waits and the hold; the record on the run; and the guides. The code is `internal/orchestrator/install.go`, `internal/checks/install.go` and `internal/runstate/install.go`.
- Not built: build piece 6, the trial on a project other than Yoyodyne.

## The problem

The operator wants Yoyodyne to work on a TypeScript plugin that uses npm and runs its tests with a test runner npm installs. Three things stop a developer run on it from testing its own change:

- A fresh worktree has no `node_modules`, because Git ignores it.
- A Claude Code developer run reaches no outside host and cannot write the npm cache in the operator's home folder.
- A Codex developer run has no network at all.

The rule on developers verifying before submitting requires every developer run to execute the declared checks before any edit, and to record the relevant checks passing on its change before review. On this project neither can pass, so every run would fail that rule. The same is true of any project whose checks need packages a package manager fetches: Python, Ruby, Rust, Go modules not yet downloaded, and so on.

## The decision

The project declares an install step. The harness runs it in the run's worktree, with network access, before the developer starts and again wherever the files that decide it have changed. The developer's own session is never given network access, the package manager's shared cache, or any new place to write. What the developer gets is a worktree where the packages are already in place, as they would be on a person's machine.

The harness learns nothing about any package manager. It runs the command the project declares, watches the files the project names, and checks for the folders the project names. The same rule serves npm, pip, Bundler, Cargo or anything else.

## What a project declares

```yaml
install:
  - name: npm packages
    run: npm ci --cache "$YOYODYNE_INSTALL_CACHE" --prefer-offline
    inputs: [package.json, package-lock.json]
    outputs: [node_modules]
    time_limit: 10m
```

- `install` is a list. It is empty by default, and an empty list changes nothing about a run.
- `run` is the command, run in the root of the worktree by the shell the declared checks are run by.
- `inputs` are the files, relative to the root, whose content decides what the step installs. Each must be tracked by Git.
- `outputs` are the folders the step produces. Each must be ignored by Git, so installed packages can never reach a commit or a diff.
- `time_limit` bounds one attempt. It defaults to 10 minutes.

The configuration is refused when it is read if an input is not tracked, an output is not ignored, an output lies outside the root, or two entries name the same output.

A command that fetches from the network and runs whatever it fetched decides what reaches this machine. So `install` is the operator's to set, like an account or an approval policy, and not one of the values the development manager may apply under section 6 of [portable agent configuration](portable-agent-configuration.md). A role may recommend an install step; the operator writes it once when a project is set up, as the declared checks are written by `yoyo init`. The guide for a newcomer says how. Npm's own option for skipping the scripts a package runs when it is installed is the project's choice to make in `run`; the harness does not add it, because it would be knowing one package manager.

## When the step runs

A step is due in a checkout where no successful run of it is recorded there, where the digest of its inputs differs from the one that run recorded, or where one of its outputs is missing, and nowhere else. The harness runs each due step, in the order the project declares them, before anything that runs the project's code in that checkout:

1. **Before the developer starts.** After the worktree is made and before the developer's first turn, so the developer's first check before any edit finds the packages there.
2. **Before every later developer turn,** which is every repair round and every continued attempt.
3. **Before every check stage,** the check of a merge candidate included.
4. **Before the landing checks, and before the merge queue's checks of a candidate and of its base.**

A developer who changes a dependency file therefore cannot use the new package within the same turn. The harness installs it before the checks that judge the change and before the next turn, so the change is tested with what it asks for. The developer is told this in its instructions for any project that declares an install step.

Each run of the step is recorded on the run with:

- the step's name and the checkout it ran in;
- the digest of its inputs;
- when it started and how long it took;
- its result;
- the last 200 lines of its output, at most 16 KiB, scrubbed of secrets.

A step that passes is also added to the product's record of what has installed on this machine: the step's name and the digest of its inputs. That record is what tells the second failure case below from the third.

## What the step may reach

- **Network.** The step's command is run by the harness, not by an agent, and is given network access. No developer session, reviewer session or other agent is given network access by this design.
- **The package cache.** The harness keeps one folder per project under its state directory, created through the confined write primitive like the rest of the state directory. Its path is passed to the step as `YOYODYNE_INSTALL_CACHE`. Only the step's command is told it. A developer run's sandbox is not given write access to it, and nothing here changes what a developer run may write.
- **The worktree.** The step writes its outputs in the worktree, where the developer may already write.

Otherwise the step runs as the project's declared checks run, through the same check runner: the same user, environment and limits on how many run at once. A step counts against the capacity for heavy checks only if the project declares it heavy.

## When the step fails

In a run's own worktree, what happens depends on what changed, because that says whose failure it is:

- **The run changed the inputs.** The failure belongs to the change. Before a check stage it goes back to the developer as a failed check, with its output, and spends one repair round, as any failed check does. Before a developer turn the turn goes ahead, because that turn is the change's chance to repair it; the failure is on the run's record.
- **The inputs are unchanged from the target the run started from, and the same inputs have installed successfully before on this machine.** The cause is outside the work: almost always the network or the package registry. Under the operating rule on networks, the run is never ended for it. The harness makes one attempt and then tries again up to three more times, five minutes apart, with the time of the next attempt on the run's record. After the fourth failed attempt it holds the run for the development manager with the change kept and the output named. No repair round is spent.
- **The inputs are unchanged and have never installed successfully on this machine.** The step itself, or the machine, is wrong, not the change. The run is held for the development manager at once with the output, before any developer turn is spent where it happens before the first. No repair round is spent. The development manager decides whether the project's step needs changing, which is the operator's to write, or the machine needs something only a person can supply.

A stop the operator asks for during a wait between attempts is acted on within the wait.

The landing checks and the merge queue run a due step once and do not sort its failure. A landing check comes after the run is settled, so a failure there is recorded as the landing check never having run. A merge queue stage records the failure as its problem and runs again on a later pass.

Wherever the run is shown, it says in plain words which of these happened, for example: the npm packages could not be installed because the registry could not be reached; trying again at 14:05 PDT.

## The rule on developers verifying before submitting

The rule is not relaxed and needs no change. With the step in place, the developer's first check before any edit and its record of the relevant checks passing both run against installed packages, offline, as they would on a person's machine. No exception is made for a project whose step does not work: an exception would be a change to the invariant, and the first check before any edit exists to catch a run whose toolchain cannot work at all.

## How this fits the other designs

- **Checks on CI.** The short local check and the fallback to local checks both run in a checkout the step has prepared. CI installs packages by its own workflow and is unchanged.
- **Repository confined writes.** The harness's writes to the cache folder and to the record of what has installed on this machine go through the confined write primitive. What the project's command writes is the project's command's write, as a declared check's writes are.
- **Choosing checks by change.** A change to an install step's inputs counts as a change to every check that runs after the step.
- **Portable agent configuration.** `install` is a key a role may not apply, and drift reports it like any other value.

## Alternatives rejected

- **Giving the developer run network access.** It would widen what an agent may reach on every run to serve one step that does not need an agent.
- **Giving the developer run write access to the operator's package cache.** It would let an agent change a folder outside the worktree that other programs on the machine trust.
- **Committing the packages to the repository.** It makes every project carry what its package manager exists to fetch, and fails for packages built for one machine.
- **The harness recognising lock files by name.** It would build knowledge of particular package managers into the harness, which the goal on any language rules out.
- **Excusing such projects from the rule on verifying before submitting.** It would let a run submit a change nobody ran, which is what the rule exists to prevent.
- **Copying installed folders between worktrees from a store the harness keeps.** It would be faster, but it is a second copy of what the package manager's own cache already gives. It is left for later, if measured install times call for it.
- **Three attempts on an outside failure.** The first version of this design said three. Four attempts five minutes apart cover a registry outage of about a quarter of an hour, which three do not reliably, and the configuration guide already promises four.
- **Handing a failure on changed inputs back before a developer turn.** It would charge a repair round for something the coming turn exists to fix, and tell the developer nothing the turn would not find.

## What is enforced in code

- No agent session is given network access or write access to the cache folder by this design.
- The step runs before the developer's first turn and before every later point named above where it is due.
- Outputs must be ignored by Git and inputs tracked, checked when the configuration is read.
- `install` is refused for a role's write under section 6 of portable agent configuration.
- A failure in a run's own worktree is sorted into one of the three cases above from the recorded input digests and the record of what has installed on this machine, and only the first spends a repair round, and only before a check stage.

## What a reviewer can check

- With no `install` declared, a run's steps, timing and records are unchanged.
- On a project declaring npm packages, the developer's first check before any edit finds `node_modules` and runs the tests offline.
- A run that changes `package-lock.json` gets a fresh install before its checks and before its next turn, and no install where the inputs are unchanged and the outputs present.
- An install that fails on inputs the run changed spends one repair round before a check stage, and lets the turn go ahead before a developer turn. One that fails on unchanged inputs that installed before is attempted four times and then held, spending none. One that fails on inputs that never installed is held at once.
- A configuration whose output is tracked by Git, or whose input is not, is refused when it is read, naming the path.
- A developer session cannot write the cache folder, shown by a test that tries.

## Build pieces

1. Built. The `install` key: its shape, its validation, its refusal for a role's write, and `yoyo doctor` showing it, with the step run through the check runner with network access.
2. Built. Running the step before the developer starts, with the cache folder under the state directory and the step's record on the run.
3. Built. Running the step again before developer turns, check stages, the landing checks and the merge queue's checks, wherever it is due.
4. Built. Sorting a failure into the three cases, with the waits, the hold for the development manager, and the plain wording on the status line and the dashboard.
5. Built. The newcomer's guide and the configuration guide: how to declare an install step, with one example each for npm and one other package manager.
6. Not built. A trial on a project other than Yoyodyne written in another language, such as the operator's TypeScript plugin, showing a developer run test its own change.
