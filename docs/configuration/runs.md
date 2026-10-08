<!--
Landed by yoyodyne-ifd.117.3, tranche 3 of the configuration.md split, with
docs/configuration.md left intact. The links below into ../configuration.md
resolve today, and each points at a section this tranche did not move:

  #recurring-tasks                    -> no row in docs/docs-map.md; it stays
                                         in configuration.md until the map
                                         gives it a home
  #a-developer-slot-that-prefers-a-label
                                      -> this guide's own section, linked back
                                         deliberately (117.4): the replay test
                                         const configurationGuide in
                                         internal/orchestrator/scheduleslots_test.go
                                         reads the developer-slot yaml out of
                                         configuration.md, which still carries
                                         the section, so the sentence under
                                         that heading names that copy rather
                                         than this one. The tranche that
                                         reduces configuration.md to an index
                                         takes the block with it and must
                                         repoint the test at this file, or the
                                         test fails on a heading that is gone.

117.3 retargeted setup.md's #what-init-proposes-for-checks to this guide when
it landed.

"The configuration index ... lists the other guides" below is a forward claim:
configuration.md becomes the index in 117.4.

Scope against docs/docs-map.md: both sections the map's disposition table
assigns this guide — Checks and Scheduling ready work — with their children.
Four of those children have no row of their own, because the table was last
reconciled on 2026-08-24 and the file has grown since: What a developer has to
have run, What a check leaves running, The environment a check runs in, and A
developer slot that prefers a label each sit under a section that has a row, so
each goes where its parent goes, and the map already names the third among the
nine headings it has never seen. Running a work item against the workflow
definition, which follows Scheduling ready work in configuration.md, has no row
and is nobody's child, so it stays there.

Extracted from the docs/configuration.md beside this file, section for section.
Most edits are to links: a relative path out of docs/configuration/ gains a ../
prefix, and a link into a section the split has moved into a guide — one of
this tranche's siblings or an earlier tranche's — is retargeted at that guide. One sentence of prose differs, and it is the
replay-test sentence under A developer slot that prefers a label: in
configuration.md it says the test reads the block "out of this document", which
is true there and false here, so this copy names docs/configuration.md as the
file the test actually reads. Diffing this guide's body against lines 1834-2527
of that file shows those link lines, that sentence, and nothing else.

Size: 758 lines against the map's 343-line budget; the sections themselves
grew after the map's counts were taken.
-->
# Configuring checks, scheduling, and what a run may spend

The gate every run passes before it reaches review or integration, the
environment those commands are given and what they may leave behind, and how
ready work is picked up and how much of it runs at once.

[The configuration index](../configuration.md) lists the other guides.

## Checks

Each entry runs through `/bin/sh -c` in the run's worktree, so shell syntax is
available. A check must be non-interactive and must exit non-zero on failure: a
failing check ends the run before any reviewer is asked and before anything can
be integrated. Checks are the project's own — the bundle supplies none — and the
list is replaced wholesale rather than merged.

```yaml
# Go
checks:
  - go test ./...
  - go vet ./...
  - gofmt -l . | (! grep .)

# TypeScript / Node
checks:
  - npm ci
  - npx tsc --noEmit
  - npm test -- --run
  - npx eslint .

# Python
checks:
  - python -m pytest -q
  - python -m ruff check .
  - python -m mypy .

# Java (Maven)
checks:
  - mvn --batch-mode --quiet verify

# Java (Gradle)
checks:
  - ./gradlew --no-daemon check
```

Note the shape of the Go formatting check. `gofmt -l` exits 0 even when it
lists unformatted files, so `gofmt -l .` on its own is not a gate: it reports a
problem and then passes. A check has to turn that output into a non-zero exit,
as above or in a Makefile target. This repository learned it the ordinary way,
by integrating an unformatted file through a green check run.

Prefer the non-interactive, non-daemon, pinned-install form of each tool. A
check that prompts, starts a watcher, or resolves dependencies differently
between runs makes the integration gate nondeterministic.

### What a developer has to have run

The checks above are what the harness runs. What a developer has to have run
itself is decided from them, and it is asked for rather than assumed: every
developer's reply records the commands it executed, and the harness refuses a
change that records none before it spends a suite on it.

Two things are asked, and only the first is universal.

- **The probe.** One execution of a declared check, or of the build step
  underneath it, made in the worktree before anything is changed. Every run is
  asked for it whatever the work turns out to be, and what it answers is whether
  commands run here rather than whether they pass. A probe the developer records
  as `refused` — the command never started — ends the run naming what refused,
  because nothing a developer does to its change fixes an environment that
  cannot spawn a process. A probe recorded as `failed` is the opposite finding:
  the environment works and something else is red, usually the commit the run
  was cut from, so the run carries on and the configured checks report the
  failure with the repair loop behind them.
- **The check run.** The developer's own record of running a check against the
  change it is handing over. This is asked only of a change the declared checks
  would actually read: a change to content nothing here checks submits on the
  probe alone. Demanding a suite run for a change the suite never reads teaches
  padding rather than verification, which is why the line is drawn rather than
  the bar raised.

What belongs in the `detail` of anything but a pass is the message the command
itself printed, rather than a paraphrase of it, because a tool that refuses
often says how to stop refusing and that sentence is the whole value of the
record. The Go build cache defaults under the user's home, which a run's sandbox
does not grant. The harness points `GOCACHE` at `.git/yoyodyne/go-build` and
explicitly admits that path for Codex developers — the developer's own probe
included, as
[the environment a check runs in](#the-environment-a-check-runs-in) describes.
An environment the harness did not make is the project's own to warn about, and
this repository's `make` targets refuse with the redirect named; a developer
copying that refusal into the `detail` puts the fix in the run's record rather
than leaving the next reader to rediscover it.

Which files the checks read is a mechanical question rather than a developer's
judgement, and the answer comes from the checks themselves. This repository
keeps a ledger of what it is made of — every content class, and for each one
either the declared checks that exercise it or why nothing does — and the bar is
read off that, so a class that gains or loses coverage moves what is asked of a
developer without anything else being edited. The ledger is consulted only for a
project that declares the checks it was written against; a project with checks
of its own is asked for the record on every change, which is the stricter of the
two answers and the one that costs nothing to be wrong about.

### What a check leaves running

Every command the harness runs is the leader of a process group of its own, and
that group is killed when the command ends — whether it succeeded, failed, timed
out, or was cancelled. So a check that backgrounds something and exits leaves
nothing behind: the background work dies with the check that started it, and a
cleanup step the check only reaches on its happy path is not what the machine
depends on.

That is the point rather than an inconvenience. A check is a question about the
change, asked and answered inside the run; anything still running afterwards is
spending the operator's machine on a run that is over, and every run working
beside it pays for that. A daemon a check genuinely needs is started by the
check and stopped by it, inside the one command.

The reap reaches the group and nothing further. Work that puts itself in a
session of its own — `setsid`, a launchd job, a tool that deliberately detaches
what it starts — is outside the group by the time the command ends, and nothing
here kills it. What bounds that work is the bound it carries itself, which is
why background load a check spawns should stop on its own however the check
ends.

### The environment a check runs in

A check does not inherit the harness's environment. It is given one the harness
builds from an allowlist — the same one every provider invocation is built from
— so nothing the shell that started the harness happened to export reaches the
project's own commands. What a check sees is:

- what a program needs to run at all: `PATH`, `HOME`, `USER`, `LOGNAME`,
  `SHELL`, `TMPDIR`, `TERM`, `TZ`, `LANG`, `LANGUAGE`, and `LC_*`
- `SSH_AUTH_SOCK`, so a Git command over an SSH remote — a private module the
  checks fetch — can still ask the agent that holds the keys
- the proxy and certificate settings: `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`,
  `ALL_PROXY` and their lowercase forms, `SSL_CERT_FILE`, `SSL_CERT_DIR`, and
  `NODE_EXTRA_CA_CERTS`
- what the toolchains read: everything beginning `GO`, `XDG_*`, and Git's
  environment configuration (`GIT_CONFIG_*`), which is also where the
  maintenance fence every harness-launched process carries lives
- the harness's own `YOYODYNE_*`
- `GOCACHE`, pointed at `.git/yoyodyne/go-build` inside the repository being
  checked, replacing whatever the harness's own environment said. Every run the
  harness makes is given the same redirect, so a developer's own execution of
  the checks and the harness's run of them afterwards share one cache.

And whatever that list admits, a name that reads as a credential — anything
`yoyo` would redact from a process's output: `*TOKEN*`, `*PASSWORD*`,
`*API_KEY*`, `*_SECRET`, and the rest — is dropped. The list exists so that no
run's subprocess tree ever holds a Slack token, which
[`docs/slack/setup.md`](../slack/setup.md#where-the-tokens-go-and-what-the-harness-guarantees-about-where-they-do-not)
states as the guarantee it is; a check is a process the harness launches for a
run, so it is held to the same rule. A check whose tooling reads a variable not
on the list does not see it, and the place to set one is the command itself —
`FOO=bar make test` — where it is versioned with the project and visible to
every reviewer rather than a fact about one operator's shell.

The build cache is there because the Go toolchain's default cache is under the
user's home, which a developer run's sandbox does not grant: without the
redirect the first Go command in a run fails at setup with `operation not
permitted`, which reads as a broken toolchain. A project whose checks are not Go
is unaffected by a variable its tools never read.

`PATH` is the harness's own with the provider CLIs left off it: `claude`,
`codex`, and the executable every entry under `providers` names in their place.
A directory on the path that holds one of them is replaced, for the length of
the check stage, by a directory in the temporary directory holding a link to
everything the original holds except those executables, so the toolchain
installed beside a provider CLI is still found. The checks run where a provider
CLI usually is not installed as well — the forge's continuous integration, a
collaborator's machine — and a test that passed only because one is installed
here would fail there, after a review has been spent on it. Under this rule it
fails in the check stage instead. A test that genuinely needs a provider CLI
skips itself, saying why, where none is found. The one exception is a path
check that sets `needs_provider_clis: true`: it runs with the provider CLIs on
its search path, because what it vouches for is how the harness drives a real
one, and every other check in the same stage still has them hidden.

For a Codex developer, redirecting the environment alone does not grant a write.
The adapter uses `workspace-write` with an explicit
`sandbox_workspace_write.writable_roots` override naming only the shared cache
and the scratch directory assigned to this run. It resolves those paths against
the harness's checkout, refuses escaping worktree pointers and redirected cache
or scratch paths, creates both directories through the confined writer with a
pinned directory handle, and sets `GOCACHE` to the same resolved cache path.
It does not grant the whole Git directory, another run's scratch, or an inherited
cache path. The adapter disables approvals and sandbox network access. These
are existing Codex settings, not new keys in Yoyodyne's configuration schema.

The installed `codex-cli 0.159.2` lists `--add-dir` on `exec`, but not on
`exec resume`. The adapter passes the writable-root configuration and `--cd`
before `resume`, alongside `--sandbox`, on every invocation. These arguments
request the current run's directory policy; argument and CLI-help checks alone
do not prove that native resume replaces a saved session's permissions or cwd.
Read-only roles receive no developer directory grants.

Codex CLI 0.159.2 on macOS cannot execute this policy when a writable root
contains a double quote: its generated Seatbelt profile fails to compile with
`sandbox-exec: unbound variable`. Encoding the path correctly in the adapter's
configuration argument does not fix the CLI's profile generation. Such paths
require a CLI fix; the adapter does not rename them, grant a broader parent, or
disable the sandbox. Argument tests retain quoted paths, while the native
regression uses paths with spaces and no double quotes. The CLI's
[Seatbelt profile generator](https://github.com/openai/codex/blob/rust-v0.159.2/codex-rs/sandboxing/src/seatbelt.rs#L536-L564)
is where those protected-path expressions are constructed.

The following regression launches the real CLI with a local scripted Responses
server, saves a native session, resumes it with different cache, scratch, and
worktree paths, and resumes that same session under reviewer restrictions.
It requires successful Go compilation and scratch log writes, denied writes to
unrelated and previously granted paths, and read-only restrictions after
restoring a writable session. It checks the CLI's command event or unified shell
tool result for a zero exit and a distinct proof message for each turn, and
checks the retained files. The scripted provider's final reply and a replayed
command from an earlier turn cannot satisfy it. A unified shell command that
yields is polled until it reports completion, without a limit on the number of
polls. Only an explicit refusal before its execution environment is ready is
retried. Native invocation and legacy shell APIs receive the ten-minute
subprocess budget used by the Git and tracker conformance tests; local HTTP
requests and completion polling have no separate deadline. Other refusals
retain the CLI's tool result in the failure. It uses no provider credentials or
paid model calls:

```sh
go test ./internal/backend/codex -run TestNativeResumeReplacesSavedDirectoryGrants -count=1
```

The regression runs by default when Codex is installed; an absent CLI skips
it. Refusal to start the local server or execute the native sandbox fails the
check. A host must permit loopback listening and native sandbox execution.
An outer sandbox that refuses `sandbox-exec` with
`sandbox_apply: Operation not permitted` cannot exercise the native policy.

A developer run is such an outer sandbox on macOS, so there, and only there,
the regression skips instead of failing: where the process is marked as a
developer's (`YOYODYNE_AGENT_ROLE=developer`, below) and either Codex reported
`sandbox_apply: Operation not permitted` or the run's sandbox refused the local
listening port. The skip says which in plain words. Anywhere else — a person's
terminal, continuous integration, the harness's own checks, a reviewer — the
same refusal fails the test as it always did.

The harness's checks hide the provider CLIs, so `make test` there skips the
regression for want of Codex. `make codex-resume` is the check that runs it
with the real Codex present, outside any developer run's sandbox, and records
whether it passed: it fails when the test skips for any reason, so a machine
without Codex, or a developer run, cannot read as a pass. It is a path check
for a change touching what
[`scripts/codex-resume.paths`](../../scripts/codex-resume.paths) lists, once
`.yoyodyne/config.yaml` names it with the provider CLIs left on its search path:

```yaml
path_checks:
  - command: make codex-resume
    paths: scripts/codex-resume.paths
    needs_provider_clis: true
```

An accepted CLI launch or saved thread does not prove permitted cache writes,
scratch log writes, or confinement after resume. Successful native execution
evidence is required before claiming those conditions are met; unrestricted
filesystem access does not substitute for it.

A provider invocation is given the same list with one thing more:
`YOYODYNE_AGENT_ROLE`, naming the role the process was launched for —
`developer`, `reviewer`, and so on. It is under the harness's own prefix so the
allowlist carries it into everything the agent starts, and it is what
[the verbs that record a person's decision](../operations.md#pausing-everything-and-resuming-it)
— `yoyo pause`, `yoyo resume`, `yoyo release`, `yoyo artifact approve` — read
to refuse a shell an agent opened. A check the harness runs itself does not
carry it: a check is the project's command, launched by the harness rather
than by an agent.

### What `init` proposes for `checks`

A project does not start from the empty list unless it has to. `yoyo init` reads
what the repository already announces about its own toolchain and writes the
commands that follow into `checks`, each under a comment naming the artifact it
was derived from:

| What is there | What is proposed |
| --- | --- |
| a Makefile with a `check` target, or with `test` and no `check` | `make check` / `make test` |
| `go.mod` | `go test ./...`, `go vet ./...` |
| `package.json` with a test script and exactly one lockfile | the lockfile's install, `npm`/`yarn`/`pnpm test`, and `tsc --noEmit` where there is a `tsconfig.json` |
| `pyproject.toml`, `pytest.ini`, `setup.cfg`, or `tox.ini` naming pytest | `python3 -m pytest -q` |
| `pom.xml` | `mvn --batch-mode --quiet verify` |
| a `gradlew` wrapper | `./gradlew --no-daemon check` |

**Nothing is executed.** Detection is by artifact presence and by reading those
artifacts, because running a stranger's build to discover what it is is not a
first impression worth making, and because a command that has to run to be
proposed is one that runs before anybody has reviewed it. This is a convenience
default derived from the project's own files rather than an understanding of
toolchains in the harness: what runs is still only the shell commands this list
declares, judged by their exit codes.

**Whatever is not written into `checks` is written beside it, commented out,
under a heading that says what it wants from you.** There are three, and only the
first asks for anything:

| Heading | What it means | What you owe |
| --- | --- | --- |
| `YOU MUST CHOOSE` | detection could not tell which command is the gate, and `checks` is empty | a choice: a run is refused until there is one |
| `ALSO FOUND, AND NOT DECIDED` | the same, except `checks` was written from something else and works | nothing; the question is open, not blocking |
| `ALSO FOUND, AND NOT NEEDED` | commands detection read and decided against, because what it wrote covers them | nothing |

The distinction is the point. A demand to choose is worth reading only where a
run cannot happen until somebody does; putting it over an already-runnable file
teaches an operator to scroll past it.

Taking any of them is the same gesture: delete the leading `#` and nothing else,
and open the list above with `checks:` if it is still `checks: []`. Each carries
the reason it is where it is.

**A Makefile supersedes the language-native commands**, which is the ordinary way
into the third heading. A project with a `check` target and a `go.mod` gets
`make check`, and `go test ./...` and `go vet ./...` appear under
`ALSO FOUND, AND NOT NEEDED` rather than being added, because two gates running
the same suite is the suite run twice. Nothing about that is undecided, so
nothing about it demands a decision.

**What cannot be settled is not settled**, which is the first two headings. The
cases that reach them today are:

- Python tests with no runner named anywhere. unittest discovery over
  pytest-style tests collects nothing and exits 0, which is a gate that passes
  everything, so neither runner is written.
- A `package.json` with no lockfile beside it, or with more than one, which
  leaves how the project installs unsettled.
- A `package.json` that declares no `test` script at all, or whose only one is
  npm's `exit 1` placeholder: nothing there says how the project is tested.
- A Gradle build script with no `gradlew` wrapper to pin the version a check
  would run under.

Which of the two headings they land under depends only on whether anything else
in the project produced a `checks` list to stand on.

A repository that announces none of this keeps `checks: []` and the commented
per-language examples above, which is what it always did.

### How long a check may take

Each check gets a budget, and a check that exceeds it is killed and ends the run:

```yaml
execution:
  check_timeout: 30m   # the default; per check, not for the list
```

It is the *total* time a check may run rather than the time it may stay quiet: a
suite printing a result every second is spending it just as fast as one that has
gone silent. The `30m` default is deliberately generous, because a check stopped
at this bound is not a check that judged the change — the work may have been
passing the whole way, and killing it costs a run that had nothing wrong with it.

**Concurrency multiplies what a suite takes, so this has to scale with it.**
`max_concurrent_developers: 2` does not give each run its own machine: two suites
contend for the same cores, and each one's wall clock grows accordingly — about
twofold for this repository's own suite, and further under whatever else the
machine is doing, including the provider processes the runs themselves keep busy.
The budget is spent in wall clock, so N concurrent runs need a budget set against
what the suite takes with N of them running, not against what it takes alone.
Either raise `check_timeout` to match, or lower `max_concurrent_developers` so
the suites serialize; leaving both at values chosen independently is how a
passing suite gets killed. This is the failure that produced the setting: a flat
ten minutes, a suite past forty packages with real Git integration tests, and two
concurrent runs — the tests were passing package by package when the bound
stopped them.

Every check reports what it spent against what it was allowed, whether it passed
or not. The completion event carries `elapsed` and `timeout`, and the run's notes
on the work item carry the same pair per check, so a suite growing toward its
ceiling is visible run after run rather than only in the run the ceiling finally
stops. When one does time out, the failure names both numbers and the two
settings that move them.

A budget of `0` is refused rather than read as "unbounded": nothing else bounds a
check, so one that never returns would hold a worktree, a claim, and a run open
indefinitely.

## Scheduling ready work

`yoyo run <id>` is you naming an item. `yoyo work` is the harness choosing:

```yaml
execution:
  max_concurrent_developers: 1   # the default
```

It reads the admitted work in the order you set — highest priority first — takes
the items the tracker itself reports as ready to pull, and starts as many of them
at once as this leaves free. Each run gets a worktree and a branch of its own,
and the command returns once every run it started has ended. `--limit <n>` stops
it after that many runs; without one it drains what is ready, and
[`--watch`](#watching-instead-of-draining) keeps it open instead.

Nothing about running several at once relaxes anything. Capacity is enforced at
the reservation rather than by the scheduler, so two schedulers, or a scheduler
and a `yoyo run` beside it, share one limit rather than getting one each — a run
that loses the race for the last slot is reported as declined, not as a failure.
Integration stays serial: at most one promotion into a given target branch
happens at a time, and a change whose target moved while it was being reviewed is
replayed onto where the target went and promoted by fast-forward — or, on a
target the forge protects, replayed the same way and then landed through its
pull request rather than by a local fast-forward
([a protected target lands through its pull request](publishing.md#a-protected-target-lands-through-its-pull-request)).
A replay that conflicts is handed back to the change's own developer to
reconcile on top of the target, as a repair attempt that is checked and reviewed
again, and the run blocks only once a budget is spent. Nothing is ever forced.

Twelve things keep an item out of a pass, reported at two different grains. The
first nine are named against the item, because nothing else would report that
this item was passed over; the last three are facts about the pass rather than
about any one item.
[How work flows](../work.md#letting-the-harness-choose-the-work) lists the same
twelve in the same order, and a test fails when the two lists differ:

<!-- selection-rules: the same names, in the same order, as docs/work.md and docs/configuration.md; internal/doclink/selectionrules_test.go holds them together -->
1. **An unresolved directive** withholds the item until a person resolves the
   directive, and is named in the directive's own words.
2. **Unfinished children that carry its execution** withhold a container while
   any child it was broken into is queued, blocked, or claimed, and release it
   once the last of them leaves the backlog.
3. **A race with work in flight** withholds an item that shares an epic
   decomposition or files with a run in flight, and releases it at the first
   pull after that run ends.
4. **A conversation executor** withholds an item whose `executor` names a
   persona conversation from every developer run; nothing clears it, and what
   moves the item is somebody opening the conversation it names.
5. **Parking** withholds an item the Lead Product Manager parked however far the
   queue drains, and only her `unpark` releases it.
6. **A hold** withholds an item whose stopped run left its change on a branch,
   or whose publication did not finish, until the development manager's
   decision is carried out, the escalation is answered, or `yoyo reconcile`
   settles the publication.
7. **A step only a person can take** withholds an item that declares a
   `human-gate:` nobody has recorded taking, and only a person's
   `yoyo gate record <name> --for <item>` releases it; closing an item never does.
8. **A prerequisite the tree does not meet** withholds an item that pinpoints
   code the repository no longer has, or says in its own words that something
   must land first; a pinpoint releases it when the code lands, and a sentence
   when the item is amended or the dependency recorded.
9. **A label another slot prefers** withholds an item every free developer slot
   walked past for its preferred label, and the next slot with no preference to
   come free — or the preferring slot, once its label's work is exhausted —
   releases it.
10. **The tracker not calling it ready** withholds an item with unfinished
    dependencies or a status that is not open, and the tracker's own readiness
    releases it.
11. **A run already in flight for it** withholds the item while that run lasts,
    and the run ending releases it.
12. **No free developer slot** withholds everything once the slots are taken,
    and any run ending releases one.
<!-- /selection-rules -->

Parking is the one to know about if you watch the queue: it is how deferred
work stays admitted without being pulled, and it exists because on 2026-08-27 a
draining queue reached work a scope decision had put off, started it, and spent
$34.38 on a run nobody wanted. A priority cannot do that job — the bottom of the
order is the last thing pulled, not the thing never pulled. The conversation
executor is the same kind of marker for work a role does in conversation rather
than in a developer run.

The children rule is there because a decomposed epic and the child doing its
work are both reported as ready, and starting both buys the same change twice —
two developers over one file, the second of them guaranteed a conflict at
integration. A race is sequenced behind the run it would have raced rather than
started beside it, named with that run and what the two share — the epic one of
them was broken out of and the other is, or overlapping files. Two items merely
filed under one epic are not racing:
an epic is as often a heading as it is one piece of work broken into several,
nothing tells the two apart from the outside, and holding every child of a
heading behind whichever started first serializes the queue rather than
declining a race. That one
is a wait rather than a refusal: the conflicts are re-read at every pull from
what is actually in flight, so the item is pulled at the first pull where the run
it would have raced has ended, and the slot the hold freed is spent on the next
item down the order that races nothing. An item says which files it will change
by naming them after `conflict-surface:` on a line of its own, in its title,
description, design guidance, or acceptance criteria; an item that declares
nothing has those same fields read for the files it plainly names, and that
inference takes only a path with a separator and an extension on the end, because
a surface invented out of prose would hold unrelated work back. An item the tree
is not ready for — one that pinpoints a `file:line` or a package-qualified
symbol the repository no longer has, or that says in its own authored words that
something must land before it starts — is named with the unmet prerequisite and
routed to the [triage docket](recovery.md#triage-thresholds) rather than
to a run; [how work flows](../work.md#letting-the-harness-choose-the-work) says what
the two readings are, and what the executor, parking, and hold rules each
record. An item every free
[developer slot](#a-developer-slot-that-prefers-a-label) walked past for its
preferred label is named as left for another slot, with the slot and what it
pulled ahead of the item: it waits on nothing about itself. An item that
declares a step only a person can take — named after `human-gate:` on a line of
its own, in those same authored fields — is passed over with that step and what
records it both named, until somebody has recorded taking it with `yoyo gate
record <name> --for <item>`. That one is neither a wait nor something any run
clears: closing a work item does not pass it, which is the whole reason it
exists. The act is recorded against the item that declared the gate and passes it
there and nowhere else, so a name a later item declares again is a step somebody
still has to take. See
[a step only a person can take](../work.md#letting-the-harness-choose-the-work) for
what it replaced. The last three
rules are reported as facts about the pass — the
stop reason names which of them ended the choosing, and a pass that got as far as
reading the queue prints how many items were admitted, how many the tracker
called ready to pull, and how many slots were taken. Those are counts rather than
a list on purpose: naming every unready item would print a line per backlog entry
on every pass and bury the deferrals worth reading. A pass that stopped before
reading the queue at all — held intake, or every slot already taken — says
nothing about the backlog rather than reporting zeroes it never looked up.

A decided repair or re-run is fired by the same pass, oldest decision first, and
one about a stopped run whose branch or worktree is still there takes the first
free developer slot ahead of fresh pulls of any priority, naming in its reason
the ready work it went ahead of. Every other decision waits behind ready work of
a higher priority than its item.
[How work flows](../work.md#letting-the-harness-choose-the-work) has the whole
rule; it changes no gate and nothing a decision costs.

A thirteenth thing deliberately keeps nothing out: an item whose goal was amended
after it was admitted is pulled exactly as it would have been, and what changed
goes into the run's recorded reason instead. See
[what a change upstream leaves stale](goals.md#what-a-change-upstream-leaves-stale) for
why staleness reports rather than decides.

`max_concurrent_developers` cannot exceed the number of developer `instances` you
configured, and the default of `1` is deliberate: raising it is a decision about
your machine, and [how long a check may take](#how-long-a-check-may-take) is the
setting that has to move with it.

### A developer slot that prefers a label

Each unit of `max_concurrent_developers` is a **developer slot**: the capacity
one developer run takes. By default every slot pulls in the order you set. A
slot can instead prefer a **label** — the tracker's own labels, which the
Lead Product Manager and the development manager put on work items — and then it
pulls the ready work carrying that label first, wherever that sits in the
order, and the rest of the backlog only when none of its label's work is ready.
On 2026-09-19 the operator directed that one of Yoyodyne's own developer
[seats](../terms.md#the-register) — one running developer, as against the slot,
which is the capacity it fills — be dedicated to the `reliability` label, and
this is the block that does it, by giving the slot that seat fills a preference
for the label. The block is the operator's to paste into the project's
configuration by hand, because `.yoyodyne/` is a
[protected path](artifacts.md#protected-paths-in-a-developers-change) no run may write, so
a project whose file does not yet carry it has a reliability seat that is
directed and not yet configured:

```yaml
execution:
  max_concurrent_developers: 3
  developer_slots:
    - prefer: [reliability]   # developer slot 1 pulls reliability-labelled work first
    # slots 2 and 3 are not named, so they prefer nothing
```

The reliability label means, in the operator's words, bugs, anything that
keeps the system from stalling, and anything that keeps the system from making
mistakes. The admission practice that goes with it, from the same day: every
item admitted under the reliability directive, every bug, and every stall or
mistake fix carries the `reliability` label from admission, put on by the
Lead Product Manager's `labels` field in the same write that admits the item, so the
item never exists unlabelled. The [conversation guide](../conversation.md#backlog-state-that-has-stopped-being-true)
states the same practice where it describes the `labels` and `label` actions,
in the section on an item's tracker state.

`developer_slots` is one entry per slot, in slot order, and it may be shorter
than the capacity — the slots it does not name prefer nothing — and never
longer, because a preference for a slot the capacity does not have is one
nothing would ever act on, so a longer list is refused when the file loads. An
entry names the labels it prefers under `prefer`, any one of which on an item is
enough; an entry written as `{}` or with `prefer: []` is a slot with no
preference, which is how the first slot is left alone and the second given one.
A slot may prefer more than one label, and more than one slot may prefer the
same label. Each label is held to the rule the tracker's actions hold a label to
— one word of letters, digits, dots, underscores, and hyphens, up to 64 bytes —
and compared exactly, so `Dashboard` and `dashboard` are two labels. A list in a
later layer replaces an inherited one whole, as `checks` does.

**What a preference changes is which item a slot pulls first, and nothing
else.** The item a preferring slot starts is claimed, developed, checked,
reviewed, and promoted exactly as it would be from any slot, under the same
contract and the same authority table. Configuration selects what a slot pulls
and never widens what it may do.

Three things follow from a preference, in the order a pull applies them:

- **A preferring slot pulls its label's ready work first.** With the example
  above and a reliability-labelled bug at priority 2 under an unlabelled item
  at priority 1, slot 1 pulls the bug ahead of the unlabelled one; the run's
  recorded reason says it was pulled into developer slot 1, which prefers the
  reliability label the item carries.
- **A slot with no preference leaves labelled work to a preferring slot that is
  free to take it.** With slots 1 and 2 both free, the reliability item goes to
  slot 1 and slot 2 takes the next unlabelled item down the order. Where no
  preferring slot is free — the seat in slot 1 is working on one reliability
  item and another is ready — the label is only a preference, and slot 2 takes the
  reliability item in the order like any other. A label dedicates capacity to
  its work; it never withholds the rest of the machine from it.
- **A preferring slot never idles on an empty label.** Once none of its label's
  work is ready, slot 1 pulls from the rest of the backlog in the order like a
  slot with no preference, and its recorded reason says it fell back. The next
  reliability item admitted is pulled the next time slot 1 is free.

A replay test in `internal/orchestrator` reads that block, loads it as a
configuration, and drives the scheduler over it, so the example is held to doing
what these three points say rather than described as doing it. The copy it reads
is the one in
[`docs/configuration.md`](../configuration.md#a-developer-slot-that-prefers-a-label)
and not the one above, because the split has not reduced that document to an
index yet and both still carry the section; the two blocks are identical, and
the tranche that makes it an index points the test here.

A run's record can name the slot it occupies. Where it does, that is the run's
slot and no other: a change of labels, a different start order, a restart, or a
lowered `max_concurrent_developers` never moves it, and a run whose recorded
slot lies beyond the capacity is reported beyond the slots, keeping its number.
A run records its slot only in a project that names an endpoint pair for at
least one slot ([developer slot endpoints](../configuration.md#developer-slot-endpoints)):
it records the slot it was pulled into, or the lowest free one where that was
taken or it was started by name. In every other project, and for every run
recorded before slots were, which slot a run is in is read off what is in flight against what the slots prefer, the same way every
time, by the scheduler and by `yoyo status` alike. A run over labelled work is in a slot that prefers its
label while one is unassigned, and everything else is in a slot with no
preference first and in a preferring slot only once those are full — which is
that slot having fallen back. Labels are read from what each run recorded at
its claim, so a run started by `yoyo run` counts against the slots exactly as a
scheduled one does. Where any slot prefers a label, the running line of
[`yoyo status`](../operations.md#where-the-harness-stands-the-four-lines) says which slot each run is in and what that slot
prefers, and names each free slot with its preference under the runs; where
none does, the line reads as it always did. An item the only free slots walked
past for their label — an unlabelled item ranked above the reliability item
slot 1 pulled, with no other slot free — is reported by the pass as **left for
another developer slot** rather than as deferred, naming the slot and what it
pulled ahead of the item: the item waits on nothing about itself, and what
takes it is the next slot with no preference to come free, or slot 1 once its
label's work is exhausted.

The same slot entries may also carry a `routing` endpoint pair and an optional
`number` confirming their position. [Developer slot endpoints](../configuration.md#developer-slot-endpoints)
states precedence, defaults, validation and reviewer selection. A developer run pulled into a slot with a pair starts on its
primary and moves to its alternate once per operation on a usage limit, as that
section describes. Label preferences keep their existing behavior.

### Watching instead of draining

`yoyo work` returns when nothing more is ready. `yoyo work --watch` does not: it
waits out an interval and reads the queue again, until you stop it.

```yaml
execution:
  work_poll: 60s                       # the default
  blocked_runs_before_intake_hold: 3   # the default
  brake_cooldown: 30m                  # the default
  brake_escalation_cycles: 4           # the default: two hours at that cooldown
```

Nothing else about the pass changes, and nothing needed to. Every pull re-reads
the configuration and the intake hold, takes the queue in the order you set, and
records why it chose what it chose — so work you admit is picked up at the next
poll, a reprioritization at the next pull, and an item whose dependency landed
becomes pullable because the tracker says so. There is no change detection in it:
nothing between the readings is cached, and a run already in flight is never
preempted by any of it.

An idle session costs one local tracker read per `work_poll` and asks no provider
anything, so a queue that is empty overnight spends nothing — unless it has a
stopped run to [deliver](../work.md#letting-the-harness-choose-the-work), or a
[recurring task](../configuration.md#recurring-tasks) that has come due. Each of those is a turn and
is charged as one, so a project with an hourly task and an empty queue spends a
turn an hour rather than nothing.

**The intake hold is the remote brake.** It does not stop a watching session; it
brakes it in place — the session keeps polling, chooses nothing, and resumes
where it was when you release it. `yoyo pause`, the wider switch, parks the runs
too, and lifting it resumes them from their own records.

**Holding intake does not stop the spend above, and that is the distinction to
have in mind.** The hold stops the session *choosing work*, and the two things
that spend without choosing any are read before it: a stopped run reaches the
development manager and a due recurring task fires under a held intake exactly as
they do under a clear one. That is deliberate — a held queue is usually waiting on
one of those judgements, and withholding them would be the hold answering a
question nobody asked it — but it means an operator who holds intake to stop
spending is still charged a turn per cadence. `yoyo pause` is the switch that
stops those too.

**Four guards, because the loop no longer ends.**

**A watching session does not start the same item twice unless the item has
changed.** The case that forces this is a run that fails *before it starts* —
unreadable acceptance criteria, a context bundle that will not assemble. Nothing
is claimed and nothing is recorded, so the item is left exactly as ready as it
was: a drain tries it once and returns, and a watch with no memory would retry it
every interval forever. A provider that is not authenticated used to be one of
these and is not any more: that dispatch is
[a wait](../operations.md#waiting-out-a-provider-nobody-can-reach) the session
holds the item through rather than an attempt it remembers, so the item is
started when the login is renewed.

The rule covers every item the session has started, not only the ones that failed
that way, because the other cases that leave an item pullable with nothing
recorded — a run the intake hold or your `yoyo pause` stopped before it claimed
anything — would spin the same way. What lifts it is the item changing: what the
work says, what it is for, its priority, its status, what it depends on, and its
notes. The notes make the ordinary recovery work: a run that stops on a blocker
takes the item out of the ready queue and writes the blocker into its notes, so
when you release that item without editing anything else, the session sees an
item it has not tried and pulls it. Nothing the harness writes can clear the
cooldown of an item that stayed pullable, because it only appends to the notes of
an item it has claimed, blocked, or closed.

An item this session has already run and nothing has touched since is left alone
for the life of the session. Restarting the session, or touching the item, asks
for another attempt — and the restart it makes for itself when you deploy counts,
which is usually what you want, since a build you just installed is the likeliest
reason the attempt would go differently.

A start the environment refuses before reserving a run is eligible again after
one poll interval, without any change to the item. Uncommitted changes in the
primary checkout, a lost race for capacity, and failures to read repository
readiness, durable state, or architectural invariants leave no memory that the
work was tried and count nothing toward `blocked_runs_before_intake_hold`.
The refusing step records that the cause is outside the work. A sandbox that
will not spawn a shell is covered when that step marks its refusal that way.
A readiness read also covers an unmarked failed start while the checkout is
still refusing work.

The primary checkout's readiness is read at every pull before new work is chosen.
A watching session waits and reads again; a drain stops on the refusal. The watch
log, `yoyo status`, and the Slack heartbeat carry the cause, including
`runs cannot start: uncommitted changes in the primary checkout (<file>); commit or stash to release`.
Once you commit or stash, the next poll can start the queued work without a
session restart or an edit to the item. Other refusals carry the condition and
the beginning of the cause separately, so bounding a long cause does not spend
the detail limit repeating the condition.

`blocked_runs_before_intake_hold` is the failure-storm brake, a different thing
from that cooldown: it is aimed at a broken machine rather than a broken item.
That many runs blocking one after another, with nothing landing between them,
holds intake — the same hold you would place — and the same poll summons the
development manager's [sweep](../configuration.md#recurring-tasks) out of its cadence, with the
runs that blocked and the reason each blocked in the message that wakes her.
Any run that lands clears the count, and `0` turns the brake off, leaving you as
the only thing that holds intake. Only verdicts and check failures on a change
that was present count: a stop the environment made — a dirty checkout, a
tracker or a forge that did not answer, a sandbox that would not spawn, a
target branch that has
[diverged from the forge](../operations.md#unwedging-a-target-branch-that-diverged-from-the-forge)
so the harness will not catch it up — is a verdict on nothing and counts
toward nothing, and neither does a dispatch or a run the provider turned away
because nobody is logged into it or nobody can reach it. That is
[a wait](../operations.md#waiting-out-a-provider-nobody-can-reach) no run can end,
and a brake tripped on it prescribes a decision about a change nobody judged —
which is what happened on 2026-09-17 over an expired login, again on
2026-09-19 when two of the three stops that tripped it came from outside the
work, and
again on 2026-09-21 when all three were one diverged target.

**The brake's hold does not wait on you while the harness is still working
it.** She decides what happens to it — to
release it, to keep it and probe the line, or to escalate it to you — and the
watching session acts on the decision at its next poll. `brake_cooldown` is how
long the brake waits for that decision before it decides on evidence instead:
once it has passed with nothing recorded, the session starts one probe run under
the hold, and the probe landing reopens intake while the probe blocking keeps it
held, restarts the cooldown, and summons her again with the probe's own
stoppage beside the three. So a broken machine is probed once per cooldown and
put to her each time, and a machine that was fine is choosing again within a
cooldown of the trip whether or not anybody answered. Thirty minutes is the
default: a summoned turn is minutes, so that is several answers' worth of
slack, and a summons the provider refused costs the line half an hour rather
than the two hours the 2026-09-19 trip cost it. Zero waits for her summoned
turn and no longer.

**And the loop that makes has a bound.** On a machine that stays broken, each
blocked probe summons her again and restarts the cooldown, so the brake goes
round — one of her turns and one probe run per cooldown — and before the bound
nothing about it got louder unless she escalated it. `brake_escalation_cycles`
is how many of those summons-and-probe cycles the harness goes round before it
escalates the hold to you itself: the cycle that reaches it is not put to her
again, no further probe starts, and you are sent
[one direct message](../reporting.md#a-brake-hold-the-harness-escalates), tagged
by member id, naming the cycles spent and what stopped the last probe. It is a
count of cycles rather than a length of time because the loop is what it
bounds; what it comes to in hours is the cooldown times it, and the default of
four is two hours at the default cooldown — the same bar the heartbeat raises a
stopped line to critical at. Every summons names which cycle it is and at what
cycle the harness stops asking, so she can escalate sooner herself. A hold the
harness escalated is still hers to release if the line turns out to be fine; a
probe decision on it is refused, because the bound ended the loop. Zero never
escalates on its own, which is the loop as it stood before the bound existed.

So a brake hold waits on a person only once it is escalated, by her or by the
harness at that bound. `yoyo release` and the conversation's `/release` still
lift any of them sooner.

The hold records which of you placed it, and everything that reports one says
so: "the harness's own brake placed it after 3 run(s) blocked in a row with
nothing landing between them, which is the configured brake at 3" rather than a
hold attributed to you — and, for the brake's, what is deciding it and when the
probe starts if nobody does. It matters because what you do about a stopped
line depends entirely on which of the two stopped it, and a brake that trips
over a hold you already placed leaves yours standing, still yours, and summons
nobody over it.

And the session says what it is doing, because an idle session and a dead one are
otherwise the same silence. Each transition — watching, idle, braked, blocked, resumed,
stopped — is recorded once, where `yoyo status` prints it and the Slack sink
posts it. A session idling all night writes one line rather than one a minute. A
stop says whether it is an ending or a restart, so the one below reads as a
session coming back rather than a line waiting for you to start another.

**Beyond the three: a reading of the harness that fails does not end the
session.** The tracker is a database a reconcile and every settling run write to,
so a reading that fails is contention far more often than a store that is broken
— and a session that exited on one left the queue idle until an external job
noticed. A watching session waits and reads again, two seconds doubling to
thirty, and stops only once the readings have gone on failing for five minutes,
saying how long it tried. None of it is configured: the numbers are the harness's
and the same for every product. A drain still stops on the first one, and so does
either kind of pass on a pull that assembles and is unusable — a capacity of
zero, or a `--budget` with nothing to price it — because that is a decision about
the configuration rather than a reading that failed.

**Beyond the three: a watching session takes up a build deployed over it.** When
the `yoyo` it is running is written over — you rebuild it, you install it — the
session stops choosing, waits out every run it already started, and restarts into
what you deployed. A run in flight is never interrupted for it, and nothing is
configured: a deploy is the whole of the instruction. What that costs is one
restart per deploy, and the queue is re-read from scratch on the way back in
exactly as it is at every poll.

The bounds cross the restart reduced to what is left of them — `--budget` less
what the session has spent, `--limit` less what it has started — because a bound
carried whole would start again at every deploy. A session that has reached
either one stops on the bound instead of restarting: you set that number, and
taking up a build is not you raising it. There is nothing here for a drain, which
is a command you are waiting on the return of.

**`--budget <usd>`** caps what one session spends, and everything it spends
counts against it: the runs it starts, priced from the same recorded run evidence
`yoyo cost` prices items from; the turns it takes delivering stopped work; and
the turns a [recurring task](../configuration.md#recurring-tasks) takes when its cadence comes due.
The last two are turns the session takes rather than runs it started, and they
are counted for exactly that reason — a bound that quietly excluded what a quiet
session spends would be the cap disappearing on the nights it matters most. It is
checked between pulls, never part way: money already spent is spent, and what
stopping would lose is the work it bought.

A budget the harness cannot measure is no budget, so it fails closed at both
ends. A pass given `--budget` with no way to price itself is refused before
anything starts. A session that has started and then meets a run whose recorded
evidence will not price — the event log gone, or a record it cannot read — stops
there and says which run it was, rather than counting it as free and carrying on
inside a bound it can no longer hold. The stop is announced like every other
transition, so you find out while it matters rather than in the morning.

**The default is still the drain**, and `--until-drained` says so explicitly.
That is deliberate: watching is the shape this loop is meant to have, and turning
it on by default is a decision to make once stopped work reliably reaches
somebody, rather than a side effect of the flag existing.

What changes when you watch is what bounds the spend. A drain is bounded by the
queue emptying; a watching session is bounded by what you admit to the queue. The
backlog's order stops being a schedule and becomes the throttle.

### When a configuration change takes effect

**At the next selection.** `yoyo work` re-reads the configuration before every
pull, not once when it starts, so a capacity you raise or a priority you reorder
while it is running is picked up the next time it chooses something. That is the
same answer every other command gives — each one loads the configuration fresh —
and it is what makes reordering the backlog steer the work rather than steering
the work after a restart.

A run already in flight keeps the configuration its own pull read. Its capacity,
its check budget, and its repair budget were fixed when it was reserved, and
changing them under a running developer would mean a run judged by rules it was
never started under.

A watching session is the same answer said again: `work_poll`,
`blocked_runs_before_intake_hold`, `brake_cooldown`, and
`brake_escalation_cycles` are re-read at every pull too, so an interval you
shorten or a brake you loosen takes effect at the next wait rather than at the
next restart, and a bound you tighten under a standing loop is heard at the
next probe.

### Why each run says why it was there

Every run `yoyo work` starts records, in durable state, why that item was chosen:
where it sat in the order, how much of the queue was pullable, how much of the
machine was free, and anything upstream of it that had changed since it was
admitted. `yoyo status` and a conversation's survey both read it back.

This is not bookkeeping. Work the harness chose and cannot account for looks
exactly like work happening behind your back, and holding intake — which stops
`yoyo work` choosing anything more while what is running finishes — is worth
having only if the thing that chooses actually consults it. Both halves are
enforced rather than conventional: an item you name yourself is exempt from the
hold, because naming it is you deciding it is the exception.
