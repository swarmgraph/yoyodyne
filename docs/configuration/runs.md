# Configuring checks, scheduling, and what a run may spend

The checks every run passes before it reaches review or integration — which run
when, what they may cost, the environment they are given and what they may leave
behind — how ready work is picked up, how much of it runs at once, in which slot
and on which model, and the workflow definition a run follows.

[The configuration index](../configuration.md) lists the other guides.

## Checks

Each entry runs through `/bin/sh -c` in the run's worktree, so shell syntax is
available. A check must be non-interactive and must exit non-zero on failure: a
failing check ends the run before any reviewer is asked and before anything can
be integrated. A check that could not run at all says so instead, and is not
counted as a failure; see [A check that could not run](#a-check-that-could-not-run).
Checks are the project's own — the bundle supplies none — and the
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

On the network, a developer's sandbox permits this much and no more:

- **Claude Code.** Commands may bind a local port, accept connections on it,
  and connect to localhost, so a test that starts a server on `127.0.0.1` runs
  in the developer's own checks. The harness passes
  `sandbox.network.allowLocalBinding: true` in the settings it gives every
  developer invocation, first and resumed; nothing in the repository's
  `.claude/settings.json` is needed for it. Every other outbound connection
  still goes through the CLI's filtering proxy, as before. The bind is allowed
  on any local address rather than loopback alone, so a test should bind
  `127.0.0.1` rather than every interface, or a machine on the same network can
  connect to it while the test runs. No other role is given it.
- **Codex.** Commands may not bind a port or reach any host: the developer's
  permission profile below switches its network off. Codex allows local
  binding only through its own network proxy, which needs the profile's
  network switched on, and the sandbox rules it uses then also let commands
  send to port 53 on any host. That was read from codex-cli 0.162.0-alpha.17.2
  without a provider call; the supported version is the one named below. So a
  test that starts a server fails with `bind: operation not permitted` in a
  Codex developer's own checks, and is judged by the harness's run of the
  checks afterwards, which no developer sandbox confines.

Local binding changes nothing a developer may write: the Claude Code setting
names no path, and the Codex profile grants none.

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
The supported Codex CLI is **0.160.0**. The adapter runs every Codex role under
a Codex permission profile it defines itself on the command line, as
configuration overrides (`default_permissions` and `permissions.<name>`), and
never passes `--sandbox`: 0.160.0 lets `--sandbox` replace a selected profile
without saying so, so a run given both would run under the option rather than
its profile. The profiles extend none of the CLI's built-in ones, so what a
role may do does not move with a CLI release. These are existing Codex
settings, not new keys in Yoyodyne's configuration schema.

Each profile's name is the one in the table below followed by a random ending
drawn when the invocation starts, such as
`yoyodyne-developer-3f9c…`. That ending is what keeps configuration files out.
0.160.0 adds the entries of a `[permissions.<name>]` table in the account's
`config.toml`, or in the worktree's own `.codex/config.toml` where the account
trusts the worktree, to a profile of the same name given on the command line:
a file naming `yoyodyne-developer` made a directory outside the worktree
writable. No file written before an invocation can name its profile, so nothing
is added to it. What those files say about `sandbox_mode` or
`default_permissions` gives way to the profile the command line selects.

| | `yoyodyne-developer` | `yoyodyne-read-only` |
| --- | --- | --- |
| Read | every file on the machine | every file on the machine |
| Write | the worktree, the shared cache, this run's own scratch folder in the harness's state directory, and the system and user temporary directories | nothing |
| Kept read-only inside each writable directory | `.git`, `.agents`, `.codex`, `.aws` | — |
| Network | off | off |
| Approvals | never asked; anything the profile refuses stays refused | never asked |

That is what the CLI's `workspace-write` and `read-only` sandboxes granted
before the move, entry for entry: asked without a provider call, 0.160.0
resolves each profile to the same policy on files as those sandboxes resolved
to. The temporary directories stay writable because a project's tests write to
them, as they did under `workspace-write`. A read-only role is additionally
launched from an empty directory outside the repository, with user
configuration, execution rules, and external integrations switched off
([provider plugins](../provider-plugins.md#capability-validation)).

The adapter resolves the cache path against the harness's checkout, refuses
escaping worktree pointers and a redirected cache path, creates the cache
through the confined writer with a pinned directory handle, and sets `GOCACHE`
to the same resolved cache path. The run's scratch folder is
`scratch/<run id>/` in the harness's state directory, outside the repository
and its `.git` directory, cut by the harness before the run starts and removed
with the run's worktree; the adapter grants it only after confirming it is that
one folder, reached through no link, and refuses to start the run otherwise
([the ruling](../decisions/developer-run-scratch-outside-git.md)). It does not
grant the whole Git directory, anything else in the state directory, another
run's scratch, or an inherited cache path. A Claude Code developer is given the
same folder as its sandbox's one added write path,
`sandbox.filesystem.allowWrite`, and nothing else in the state directory.

`exec resume` takes neither `--cd` nor `--sandbox`, so the profile and `--cd`
are given to `exec`, ahead of `resume`, on every invocation. Asked without a
provider call, 0.160.0 runs a resumed session under the profile given then, in
either direction between the two profiles, rather than the one the session was
saved with. A test asks the installed CLI to resolve each profile from the
adapter's own arguments and compares the result entry for entry
(`TestTheInstalledCLIResolvesEachProfileAsDeclared`), once with no
configuration files and once with an account `config.toml` and a trusted
worktree `.codex/config.toml` that set `sandbox_mode = "danger-full-access"`,
select an unrestricted profile, and add a writable outside directory and the
network to each profile's name. Against the same files it checks that a name
without the random ending is widened, so the check cannot pass on files the CLI
never read. It skips where Codex is not installed.

A path containing a double quote is quoted correctly in the profile, but the
CLI's own macOS sandbox has failed to compile such a path into its Seatbelt
profile (`sandbox-exec: unbound variable`). The adapter does not rename such a
path, grant a broader parent, or disable the sandbox; the native regression
below uses paths with spaces and no double quotes.

The following regression launches the real CLI with a local scripted Responses
server, saves a native session, resumes it with different cache, scratch, and
worktree paths, and resumes that same session under reviewer restrictions.
It requires successful Go compilation and scratch log writes, denied writes to
unrelated and previously granted paths, and read-only restrictions after
restoring a writable session. It checks the CLI's command event or unified shell
tool result for a zero exit and a distinct proof message for each turn, and
checks the retained files. Its denied directories are temporary ones, so it
removes the developer profile's grants of the temporary directories for its own runs and
fails rather than run if the profile stops carrying them in the form it
expects. The scripted provider's final reply and a replayed
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
whether it passed. A skip never reads as a pass: where the test skips — Codex
not installed, or a developer run — the target says the check
[could not run](#a-check-that-could-not-run), giving the test's own reason, so
it spends no repair attempt and the change goes on. Only a test that ran and
failed fails the check. Signing in to Codex and its usage limits cannot stop
it, because the test talks only to a stand-in model server it starts on the
same machine. It is a path check for a change touching what
[`scripts/codex-resume.paths`](../../scripts/codex-resume.paths) lists — the
Codex adapter, its test, and what it runs in — and on every other change the
run's record names it as not run because the change touches nothing that list
covers. This repository's `.yoyodyne/config.yaml` names it with the provider
CLIs left on its search path; a project adding the same entry restarts every
running part of the product onto a build that reads `needs_provider_clis`
first, because a part that cannot read a key goes on without it, or on a build
from before parts could do that, refuses the whole file
([a setting a running build does not know](#a-setting-a-running-build-does-not-know)):

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
### The environment the harness's own Git and forge commands run in

Every Git command the harness runs itself gets that same list, and for a reason
of its own. Git runs hooks, and a hook is a program the repository supplies and
the harness executes: `git worktree add` runs `post-checkout`, a ref update runs
`reference-transaction`, and both of those live in `.git/hooks`, which every
worktree the harness cuts shares. So a Git command that inherited the harness's
environment handed whatever that environment carried to a program the harness
never wrote — the Slack tokens included, by a path the run's own built
environment says nothing about.

**The forge credential is added to the forge commands and to nothing else.**
Those are the `gh` invocations the harness makes and the Git commands that reach
a remote — the push, the fetch, `ls-remote`, and the delete of a merged branch.
They carry, on top of the list above, whichever of `GH_TOKEN`, `GITHUB_TOKEN`,
`GH_ENTERPRISE_TOKEN`, `GITHUB_ENTERPRISE_TOKEN`, `GH_HOST`, `GH_CONFIG_DIR`,
`GIT_ASKPASS`, `SSH_ASKPASS`, `GIT_SSH`, `GIT_SSH_COMMAND`, and
`GIT_TERMINAL_PROMPT` the harness's own environment holds. Every local Git
command — a diff, a ref update, a checkout, a `worktree add` — gets none of
them, so the hooks those run have no forge credential to hand out. Handing every
Git command a token so that the push would have one is exactly the arrangement
this replaces.

`SSH_AUTH_SOCK` is not on that second list and does not need to be: it is on the
standing one above, so **a project whose remote is SSH pushes through the agent
exactly as it always did** — every process the harness starts carries the socket,
and the keys stay with the agent holding them. It is worth saying because the
absence reads like an omission, and the cost of it actually being one would be
every run stopping at integration on every installation with an SSH remote.

### Which provider authentication is supported

**A provider authenticates by its own login, held in its provider home, and by
nothing else.** That is what the accounts machinery names an account by, and it
is the only authentication an invocation the harness makes receives.

A key exported in a shell — `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`,
`OPENAI_API_KEY` — reaches none of them. It reads as a credential, so the
allowlist drops it from every process the harness launches, exactly as it drops
the Slack tokens. An installation that had been authenticating that way does not
degrade: the provider refuses its next run.

Three things say so before that run happens, and none of them is a gate.
[`yoyo doctor`](../operations.md#which-provider-authentication-is-supported) reports
it under `provider-authentication`, as a warning, with the login for this
project's own provider as the remedy; `yoyo config validate` says it beside the
validity answer, on standard error, and carries the variable names under
`provider_keys` in its `--json`; and `yoyo slack` says it once when the sink
starts, because the shell that starts a sink is usually the shell the harness
was started from. All three name the variables and never their values.

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

Each check gets a time limit, and a check that reaches it is stopped:

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

**The limit scales with the machine's load by the rule the stage's bound
uses** ([the stage's bound](#what-a-whole-check-stage-may-cost)): `check_timeout` is the
figure for an idle machine, multiplied by how far the one-minute load average
is above the number of cores, up to ten times, and never above the stage's own
scaled bound. A check's limit is worked out as the check begins, and the run's
record and notes carry the configured figure, the limit in force, and the load
that scaled it.

**A check that runs out of time says how long it ran and which limit stopped it.**
Three limits can stop a check: its own, what the stage's bound had left when
it started, and the deadline of the work that started the run. Whichever
stopped it, the message gives the time the check ran, measured from when it
started, and names that limit, such as `make race ran for 2m20s from when it
started` beside `the check stage reached its 30m0s
execution.check_stage_timeout bound`. A limit is named only where the time
measured against it reached it, so a check the stage stopped after two minutes
is never said to have reached its own thirty-minute limit. This holds for every
check the harness runs: a developer run's or a document run's checks, a
landing's, and the merge queue's.

**A check stopped at its own limit is unfinished, not failed.** It neither
passed nor failed, so it earns the change nothing at the gate and hands the
developer nothing to repair; the run stops for a cause outside the work,
`check-time-limit`, keeping its branch, worktree, and developer session. The
harness then runs the checks again from the start on the same change, but load
cannot prove the change did not make the check hang, so the reruns are bounded:
on one candidate (one attempt at one commit), at most two reruns, within three
times the check's limit as it stood when it first stopped. Both bounds are
shared with the stage's own continuations and with a repair the development
manager decides, are written on the run before each rerun, and survive a
restart. A longer limit under load adds no reruns. Once either bound is spent
the run stays stopped with everything kept, the record says the check did not
finish and why is unresolved, and the stoppage is the development manager's. A
check that exits with a failure is never run again by any of this.
### Installing a project's dependencies

A project whose checks need packages a fresh worktree does not have — npm's
`node_modules`, a Python virtual environment, anything a package manager
fetches — declares the step that installs them under `install`:

```yaml
install:
  - name: npm packages
    run: npm ci --cache "$YOYODYNE_INSTALL_CACHE" --prefer-offline
    inputs: [package.json, package-lock.json]
    outputs: [node_modules]
    time_limit: 10m
```

`install` is a list, empty by default, and an empty list changes nothing. Each
entry takes these fields and no others:

| Field | Required | What it is |
| --- | --- | --- |
| `name` | yes | what the step is called wherever it is shown, such as on the status line when it fails |
| `run` | yes | the command, run through `/bin/sh -c` in the root of the checkout, as a check is |
| `inputs` | yes, at least one | the files, relative to the repository root, whose content decides what the step installs; each must be tracked by Git |
| `outputs` | yes, at least one | the folders, relative to the repository root, the step produces; each must be ignored by Git, so nothing it installs can reach a commit |
| `time_limit` | no | how long one attempt may run, written like `10m` or `1h`; 10 minutes where left out |

The configuration is refused when it is read, and the refusal names the step
and the path it is about (or, for the last two below, the line of the file),
if:

- a step has no `name`, or two steps have the same one;
- a step's `run` is empty;
- a step names no `inputs`, or no `outputs`;
- a path is empty, is absolute, or lies outside the repository root (`..`, or
  `.` for the root itself);
- an input is not tracked by Git; add it with `git add`, or name one that is;
- an output is not ignored by Git; add it to `.gitignore`;
- an output holds a file Git tracks; take it out of the index with
  `git rm --cached`;
- an output that already exists is a link to somewhere outside the repository;
- two steps name the same output;
- `time_limit` is negative;
- the repository the configuration names cannot be read by Git, so none of the
  checks above could be made; every step is refused rather than passed
  unchecked;
- `time_limit` is not a duration such as `10m`;
- an entry carries a field the table above does not list.

`yoyo doctor` shows each step and says whether this machine can start its
command.

A Python project that keeps its packages in a virtual environment declares the
same thing with its own tools, and points its checks at the environment's
Python:

```yaml
install:
  - name: Python packages
    run: python3 -m venv .venv && .venv/bin/pip install --cache-dir "$YOYODYNE_INSTALL_CACHE" -r requirements.txt
    inputs: [requirements.txt]
    outputs: [.venv]

checks:
  - .venv/bin/python -m pytest -q
```

Either example needs its output in the project's `.gitignore` (`node_modules/`,
`.venv/`) before the configuration loads. The harness adds nothing to the
command: whether npm runs the scripts a package carries when it is installed
(`--ignore-scripts`), or pip uses a lock file with hashes, is the project's to
say in `run`, because the harness knows no package manager.

A step is run the way a check is: as the harness's own user, with the
environment a check gets ([above](#the-environment-a-check-runs-in)), in a
process group ended with it, and behind a place among the machine's heavy
checks where `execution.heavy_checks` names its command. Like a check, it is
run by the harness rather than by an agent, so it reaches the network; no
developer or reviewer session is given network access by it. It alone is told
the project's package cache folder, in `YOYODYNE_INSTALL_CACHE`.

The package cache folder is kept for each project among its records in the
harness's state directory, at `projects/<id>/state/install-cache`, and created
by the harness. No developer, reviewer, or other agent session is given write
access to it or network access by any of this.

Each step runs in a run's worktree after the worktree is made and before the
developer's first turn, so the developer's first check finds what it installed.
Each run of a step is recorded on the run with the step's name, a digest of its
inputs, when it started and how long it took, how it ended, and the last 200
lines of what it printed (at most 16 KiB), with secrets removed.

After that, a step runs again in a checkout before anything else runs the
project's code there — every developer turn (each repair round, and an attempt
a continued run makes), every check stage (the merge candidate's included), the
landing checks, and the merge queue's checks of a candidate and of its base —
wherever no successful run of it is recorded in that checkout, the digest of
its inputs differs from the one its last successful run there recorded, or one
of its outputs is missing. Where none of those holds, it does not run. A
developer on a project that declares a step is told that a dependency file it
changes is installed before the checks that judge the change and before its
next repair round, not within the same turn. A change that touches a step's
input is a change to every check after the step: the checks are told the whole
module rather than the packages the change touches, and every path check runs.

A step that does not pass in a run's worktree is sorted by whose failure it
is, from the digest of its inputs:

- **The dependency files are the ones the run started from, and the same step
  has installed them on this machine before.** The cause is outside the work,
  almost always the network or the package registry. The harness waits five
  minutes and tries again, up to three times; while it waits, the status line
  and the dashboard say which step failed and the local time of the next try.
  If a try passes, the run carries on. If every try fails, the item is blocked
  for the development manager with the step's output, the branch and worktree
  are kept, and no repair attempt is spent.
- **The dependency files are the ones the run started from, and no run on this
  machine has ever installed them.** The step as the project declares it, or
  the machine, is what is wrong. The item is blocked for the development
  manager at once, with the step's output, and no repair attempt is spent.
  Before the developer starts, this is before any developer turn.
- **The run changed the dependency files.** The failure is the change's to
  repair. Before a check stage it goes back to the developer as a failed check,
  with the step's output, and spends one repair round, as any failed check
  does. Before a developer turn the turn goes ahead, because it is the change's
  chance to repair it, and the checks after it hand the failure back if it
  still stands.

The landing checks and the merge queue run a step once and do not sort its
failure. A landing whose step fails is recorded as never having run rather than
as red, and a merge queue stage whose step fails earns nothing and is run again
on a later pass.

What has installed on this machine is kept for each project beside the package
cache, at `projects/<id>/state/install-installed.jsonl`: one line for each step
name and input digest that has passed. A project whose steps passed only before
this record existed starts with an empty one, so its first failure on unchanged
files is held for the development manager rather than tried again.

`install` is the operator's to write and never a role's, because a command that
fetches from the network and runs what it fetched decides what reaches the
machine. A role may recommend a step; you write it once, when you set the
project up, beside the checks `yoyo init` proposed, and a role's attempt to
write it is refused with that reason.
[Installing a project's dependencies before a developer run](../designs/project-dependencies-in-runs.md)
is the design the rest follows.

### What a whole check stage may cost

The budget above bounds one check and says nothing about the list. Four checks
each inside a thirty-minute budget are a check stage that may run for two hours,
and on 2026-09-19 one did: a run on this repository sat in its checks for over
two hours under load, with `make race` alone past ninety minutes, holding its
developer seat and the watch session's drain for the whole of it. So the stage
has a bound of its own, beside the per-check one. A session
[draining to restart into a deployed build](#watching-instead-of-draining)
lets the check a run is on finish, up to `check_timeout` as configured, and
stops the run before its next check, independently of the stage's bound:

```yaml
execution:
  check_timeout: 30m         # per check
  check_stage_timeout: 30m   # the whole list, from the first check starting to the last ending
```

**The bound in force scales with the machine's load**, exactly as a local Git
command's budget does: `check_stage_timeout` is the figure for an idle machine,
and where the one-minute load average is above the number of cores it is
multiplied by how far above — twice the cores is twice the bound — up to ten
times the configured figure. It is the same reading and the same cap the Git
budget uses (one function, `gitworktree.ScaleForLoad`), because the two were
stopping working commands under the same load: on 2026-09-26 a stage with the
gate already narrowed to three packages was stopped in `make race` at a load
average of 40 to 55 on 16 cores. The load is read as each check begins rather
than once, because the load a stage starts under is not the load three suites
beside each other build up, and the bound only ever grows: a check already
given what the stage had left keeps it. A platform that cannot report its load
holds the stage to the configured figure.

Each check is given the smaller of its own budget and what the stage has left,
and a check the stage has nothing left for is not started. A stage that reaches
its bound **ends the run as a stoppage** — `timed_out`, no repair attempt spent,
the change preserved — and what it names is the bound, the configured figure
and the load that scaled it, the check it stopped and how long that check had
run, what the stage had spent across how many checks, and the two things that
move it: narrow the checks each run must pass to what the change touches, or raise the
bound. That is a different failure from a check reaching its own budget, and it
is reported as one, because raising `check_timeout` does nothing for a check the
stage stopped.

**A stage the scaled bound still stops is a stop from outside the work.** It is
recorded on the run as one, of cause `check-stage-bound`, naming the bound, the
load, and the check; the run keeps its branch, its worktree, and its developer
session; and it counts toward nothing — not the
[failure-storm brake](#watching-instead-of-draining), and not the item's review
rounds, repair grant, or re-run. Because a stage the bound stopped judged
nothing, the harness [continues it at its checks by
itself](../operations.md#what-a-check-stage-may-cost-and-where-the-whole-suite-runs)
— on the change the run already has, at a pull with a slot free, ahead of
fresh work at equal or lower priority, spending nothing, within the bounds
[a check stopped at its own limit](#how-long-a-check-may-take) shares with it —
rather than leaving it to a re-run that redoes the development.

**The bound is visible while the checks run, not only when it stops them.**
The run's record carries the stage — when it began, the bound in force, the
configured figure and the load reading that scaled it, and which check it is
on — so `yoyo status` says where a run in its checks stands in place of the
bare phase, with the configured figure beside a bound the load raised:

```text
Running (1 developer run):
  yoyodyne-ifd.389 (Timing-bound tests do not fail the gate under machine load) — checks: 14m of 30m, on make race, 1h02m elapsed, $4.10 so far
  yoyodyne-ifd.432.13 (…) — checks: 41m of 90m (30m configured, scaled for a one-minute load average of 48.0 on 16 cores), on make race, 1h20m elapsed, $6.75 so far
```

The same figures reach the item: the run's notes carry `Check stage: 14m0s of
the 30m0s execution.check_stage_timeout bound` above the per-check lines — with
`(the configured 30m0s scaled for a one-minute load average of …)` after the
bound where the load raised it — and what the gate was narrowed to beside it, and a stage the bound stopped says so
there in the same words `yoyo status` uses for the run. The Slack thread's
"checks passed" line says what the stage spent of its bound, for the same
reason the per-check pair is recorded on every run: a stage walking toward its
bound is visible run after run, before the run the bound stops.

The default is thirty minutes on purpose, and in minutes on purpose. It is the
per-check default rather than something above it, because the bound is what
makes the per-run gate worth narrowing: with the race suite narrowed to the
packages a change touches — [below](#where-the-whole-suite-runs) — this
repository's whole stage fits it with two runs contending, and the load scaling
is what keeps it fitting when a third suite beside them loads the machine past
its cores. A project whose
stage does not fit it is told, on the first run that reaches it, which check
the bound stopped and what moves it. Like the per-check budget it must be
positive; a stage with no bound would be every check's budget added up again.

### Where the whole suite runs

A per-run gate that runs the whole suite on every attempt spends the suite's
cost several times per change and pays it in wall clock under contention, which
is what the stage bound above then stops. The arrangement that fits inside the
bound is three parts: the per-run gate runs the expensive suite **narrowed to
what the change touches** on every attempt before review, the approved change
is checked **whole as the merge candidate** once before it is promoted, and
the whole suite can also run **once per landing** over what actually landed.

**Narrowing.** Every check is given `YOYODYNE_CHANGED_GO_PACKAGES` in its
environment: the Go packages the change touches, as the `./dir` patterns the Go
command takes, sorted and without repeats. A changed file belongs to the nearest
directory above it that holds Go source — a package's test data and embedded
files are the package's, as the Go command itself files them — and a file above
every package, a document or the Makefile, belongs to none. The variable is
`./...` where the harness cannot narrow: a change to `go.mod` or `go.sum` or the
vendor tree reaches every package, and a repository that is no Go module has
nothing to narrow within. It is empty where the change touches no Go package at
all. A check that never mentions it runs exactly as it always has; a check
written to read it runs over that and nothing else. What the narrowing cannot
see is a package that depends on a touched one, which is what the whole check
of the merge candidate below is for. The run's record says what the gate was
narrowed to, and so do the item's notes; each check's start in the run's event
log says whether it was told the whole module and why, which is what
`yoyo status --checks` and the delivery page count
([how long the heavy checks took](#how-long-the-heavy-checks-took-week-by-week)).

**The merge candidate is checked whole.** Checks that were told less than the
whole module never promote a change. Once the reviewer approves a change whose
checks were narrowed, the harness runs the same checks once more over it, in
its worktree on the machine the run is on, told
`YOYODYNE_CHANGED_GO_PACKAGES=./...`: the change as it would be promoted, which
is the merge candidate. Only that pass is evidence the promotion takes, and the
promotion refuses evidence from narrowed checks however it is reached. A
failure there is the change's, found in a package that depends on what it
touched, and is handed back to the developer as repair input exactly as a
failing check before review is, spending a repair attempt; the approval is
cleared with it, so the repaired change is checked, reviewed, and checked whole
again. A run whose integration a person approves is checked whole the same
way, once its change is approved and before its pull request is opened or its
branch handed over. A gate already told the whole module — a change to
`go.mod`, a repository that is no Go module — runs nothing more. Two kinds of
run have their checks told the whole module from the start, because nothing
later checks them whole: a confirmed document's landing, which runs the project's
[document checks](#the-document-checks) where it declares them and otherwise
the configured checks over the whole module; and a run of the harness's own
that brings in a [pinned outside file](agents.md#pinned-outside-files). Its heavy checks are let start ahead of every run's gate, as a
landing's are. The stage's record says `the whole module (the merge candidate
is checked whole before it is promoted); before review: ` and what the gate was
narrowed to, so the two passes are never read as one. Every configured check
runs again, not only the ones that read the variable, because the harness
cannot tell which checks read it.

With [integration through a merge queue](publishing.md#integrating-through-a-merge-queue)
switched on (`execution.merge_queue: true`), the run leaves that whole pass to
the queue where its change goes to the harness's own queue. The queue builds
the merge candidate from the target as it then stands, runs every configured
check over it told the whole module, and lands exactly that candidate, so a
pass here would repeat it over a candidate that is not the one that lands. So
a run with the switch on marks its narrowed evidence as leaving the whole pass
to the queue (`checks_passed.whole_left_to_queue` on the run's record), and
that evidence admits the change to the harness's own queue and promotes
nothing. Nothing about it can stop the queue:

- **The queue runs every check the run left to it.** It checks the candidate
  with the configured checks and, where they no longer name one of the checks
  the run narrowed, because they changed after the change was admitted, with
  that check too, told the whole module like the rest. The candidate lands
  only once every one of them ran on that exact candidate and passed, rather
  than failing or saying it could not run; a candidate that fails one is
  handed back to its run like any failing candidate. A
  [path check](#checks-a-change-runs-only-when-it-touches-what-they-vouch-for)
  is the exception, because it joins the run's checks for what the change
  touches, is told nothing narrower than it always runs over, and the queue
  does not run it. The item's admission note says the whole pass was left to
  the queue, and once the change lands the run's record names the queue's
  check that stood in, in `checks_passed.whole_by`: the queue entry, the
  generation, and the commit that landed.
- **Such a change goes to the harness's queue or is checked here first.**
  Where admission would choose the forge's own merge queue, which lands a
  commit this harness never checks, nothing is admitted: the run checks the
  change whole here, as with the switch off, and is then admitted in whichever
  queue admission chooses. The stage's record says why: `checked here rather
  than left to the merge queue, because` and the reason. An explicit transfer
  of such a change to the forge's queue (`yoyo queue transfer`) is refused for
  the same reason.
- **A hold checks nothing.** A change the queue would hold, or whose target's
  protection cannot be read to say, is not admitted, so nothing checks it
  whole while it waits.

A change left to the queue that is promoted the old way because the switch was
turned off since, a held one among them, is checked whole on this machine
first, as above, and its stage's record says so. A run whose integration a
person approves is checked whole here whatever the switch says, because its
change does not go through the queue.

**Landing checks.** `landing_checks` is a second list beside `checks`, run
once per landing on the target branch — after a run has integrated, closed its
item, and removed its worktree — in a detached checkout of the integrated commit
cut under the worktree root for the purpose and removed afterwards, and told
`YOYODYNE_CHANGED_GO_PACKAGES=./...` because a landing is where the whole
suite runs. It runs under a budget of its own:

```yaml
execution:
  landing_check_timeout: 2h   # per landing check; the list has no stage bound
```

The budget is the landing's rather than the gate's on purpose. What is moved
to the landing is exactly the suite the gate's stage bound cannot hold, so a
landing held to `check_stage_timeout` would be stopped on every landing of the
repository that needed it; each landing check gets `landing_check_timeout`
whole, and the list may take the sum. The default is two hours, which is what
the whole race suite took under load on 2026-09-19 with room to spare.

**What waits on a landing, and what does not.** The landing checks are run by
the process that made the landing, after the run is terminal, its item
settled, and its worktree removed — so the run's developer seat is free and
`yoyo work` can start the next run beside them, and the run reads as succeeded
everywhere while they run. What does wait is whatever waits on that process
returning from the run: `yoyo run` prints its result only once the landing has
ended, a `yoyo work` drain or a `--limit` returns only once every run it
started has landed, and the restart a deployed build causes waits out every run
the session started, landing included — up to
[`redeploy_drain_limit`](#watching-instead-of-draining), past which the
landing checks are stopped and the landing is recorded as unverified. So a
landing holds `yoyo run` and a drain or a `--limit` for up to
`landing_check_timeout` times the number of landing checks — two hours a check
by default — plus any time it spends waiting its turn behind another landing
(below), and a project that cannot afford that on a drain lowers the budget or
shortens the list; it does not hold a seat, a claim, or the queue.

**One landing at a time, per target branch.** Landings on one target branch
queue on a lease of their own, so at most one landing suite runs at a time
however many runs land back to back: with two developer seats, the most the
checks put on the machine is one landing suite beside two narrowed gates, not
two whole suites beside them. The lease is an advisory file lock beside the
branch's promotion lease in the run state directory, dropped by the operating
system when its holder dies, and it is not the promotion lease — a landing
holds nobody out of integration. A landing that finds another running records
when it began waiting before it waits, and `yoyo status` says so under the run
until it is let in. The wait is bounded by what the landing ahead may take —
`landing_check_timeout` times the number of landing checks, and a
fifteen-minute margin for its checkout — and a landing that waits that out
runs nothing and is unverified. The bound covers one landing ahead, so a third
landing queued behind two full-budget suites waits it out unverified. The landing checkout runs its checks against
the repository's shared build cache, the one under the common Git directory
every run's worktree compiles against, rather than a cold one of its own.

A landing whose checks all pass on their own exit is **green**; one where a
check fails on its own exit is **red**; one whose checks did not run to a
verdict — no checkout could be cut, a check was stopped at its budget, the
landing waited out its turn behind another, the process running them died — is
**unverified**. A stopped check judged nothing,
which is the rule the per-run gate already applies to a check it stops on
time, so a landing it happened in files nothing and says why instead. All
three are recorded on the run, said on the item's notes, and said in the run's
Slack thread, and a red or unverified landing reaches the channel because it is
the one fact about a landed change that the run's own ending does not carry.

**A red landing files its own item and blocks nothing.** The run that landed
the change succeeded on the gate it was given and was approved; a red landing
is news about the target branch, not a verdict on that run, so nothing is
reopened, failed, or blocked. What happens instead is that the harness admits a
bug at priority 0 — the front of the queue, where this project puts an
operator's order — naming the target branch, the commit, the check that failed,
and the run and item that landed it, under the goal the landed item served,
because every run after it is cut from that commit and a red target branch is
the thing to fix first. What the check printed goes in that item's notes and
nowhere else: the title and the description are fields the
[protected-path gate](artifacts.md#protected-paths-in-a-developers-change) reads grants
from, and check output is text a change can shape, so the harness keeps those
two to its own words. In the notes it is quoted, a `> ` on every line, because
the notes are read line by line for the item's `Goal served:` and for the
marker below, and a check that printed either must not be taken for it. The
item is named on the run (`filed as
yoyodyne-ifd.402`) and on the landed item's notes. A target branch that stays
red is one item rather than one per landing: a later red landing of the same
check on the same branch finds the item still open — by the
`Red-landing check:` line its notes carry — notes the later commit on it, and
files nothing (`red again on yoyodyne-ifd.402, filed by an earlier landing`).
A red landing the tracker would not take an item for is still recorded and
said as red, with the refusal beside it. This is the operator's standing order of 2026-09-19, and it
is the one place the harness admits work on its own account: it goes straight
to the tracker under either `approvals.work_items` value, which
[what reaches the queue](goals.md#what-reaches-the-queue) states as the exception it
is.

For this repository the narrowed gate is written as, with the Makefile's `test`
and `race` targets taking the packages as `CHANGED_GO_PACKAGES`:

```yaml
checks:
  - make fmtcheck
  - make test CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"
  - make race CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"
  - make vet
```

The variable says only where the changed Go files are, and this repository's
tests also read its documents, scripts, Makefile, and configuration, so
[`scripts/check-scope.sh`](../../scripts/check-scope.sh) reads the change from Git.
A named list runs those packages and only those: a package the change touches
that the variable leaves out is added to them, and a file outside every
package -- the Makefile, a script, the configuration, a document -- does not
widen either target. It cannot change what the race detector finds in the
listed packages, and `make test` runs the tests that read this checkout, which
the Makefile's `DOCUMENT_TESTS` names, beside the packages instead. Only a
change Git cannot read runs the whole module. An empty variable over a change
made only of Markdown outside every package runs the race check over nothing
and `make test` over those tests alone, each saying so; an empty variable over
any other file outside every package runs the whole module, because there are
no packages to stand in for it. A package that depends on a changed one, or a
test that reads a file outside every package and is missing from that list, is
still run by the whole check of the merge candidate before anything is
promoted. This project names no `landing_checks`: the whole
check of the merge candidate is the whole suite on this machine, and CI runs
the same four checks on every pull request as separate jobs: `test`, `race` and
`vet`, which `execution.ci_checks.required` names, and formatting in the `build`
job.

**Read the variable with the shell's unset-only default, `${…-./...}`.** The
variable is set only by the harness's check runner, and the declared checks
are run in other places too: a developer executes them in its worktree for its
minute-zero probe and its submission evidence, and a person runs them by hand.
A line that read an unset variable as "nothing to test" would print that and
pass, which is a green race check nobody ran. With the unset-only default those
runs test the whole module, while inside the harness the variable is always
set — to the packages, to `./...`, or to nothing at all for a change touching
no Go package, which the Makefile's `race` target says and passes on. A check
written directly against the Go command wants the same shape:
`set -- ${YOYODYNE_CHANGED_GO_PACKAGES-./...}; [ $# -eq 0 ] || go test -race "$@"`.

A project that names no landing checks lands exactly as it did before they
existed, and a check list that never reads the variable is a gate that runs
whole on every attempt, bounded by the stage. Each entry is a shell line like
the checks above, non-interactive and non-zero on failure, and an empty one is
refused when the configuration loads. The landing checks are run by the process
that made the landing, once its run is over: a run whose process died and whose
integration `yoyo reconcile` settled afterwards lands without them, and its
record carries no landing rather than a green one. So does a run whose merge the
forge queued rather than performed: its change is on no target branch when the
run ends, and the sweep that settles the merge later runs no landing checks. On a
[protected target](publishing.md#a-protected-target-lands-through-its-pull-request) whose
merge the forge performs at once, the checkout is of the promoted commit the
pull request carried, which is the tree the merge put on the target. A process that dies inside
the landing checks leaves a run that is over with a landing the record says is
running; `yoyo reconcile` settles that landing as unverified, saying the process
died, and removes the checkout it was running in — a live process running them
holds the run's lease and is left alone.

### How many of a heavy check may run at once

Some checks are heavy enough that two of them at once slow every run on the
machine: a race detector over the whole module, a full test run. Name those
under `execution.heavy_checks`, each with how many may run at once, and every
run of every project under the same [state root](setup.md#where-the-harness-keeps-its-state-state_root) counts
against the same places:

```yaml
execution:
  heavy_checks:
    make race: 1   # one race suite at a time on this machine
    make test: 2
```

A check is named the way the check lists write it. A configured command counts
under a name when it is the name, or begins with the name and then a space, so
`make race RACE_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"` counts under
`make race`, and `make racecar` does not; where two names match, the longer
one wins. A name given no number gets one place. A name that matches nothing in
`checks`, `landing_checks`, `document_checks`, `path_checks`, or the `run` of
an `install` step is refused when the configuration loads, and so is a number below one. Name none, which is the
default, and every check starts as soon as its stage reaches it, as it always
did.

A run whose heavy check has no free place **waits for one, keeping its
developer slot**, and the wait is on its record from the moment it begins:
`yoyo status` and the dashboard say `checks: 3m of 30m, waiting 4m for a free
place to run make race` rather than naming a check that is not running. **A
check's time starts when it launches.** Neither its own `check_timeout` nor the
stage's `check_stage_timeout` is spent while it waits, and the stage's bound is
scaled for the machine's load as the check launches rather than as it began
waiting. A check re-run after its limit stopped it asks for a place again.

Waiting checks are let start in three orders: checks for a landing or a
promotion first — the [landing checks](#where-the-whole-suite-runs), a merge
queue candidate checked locally, and a replay onto a target branch that moved;
then a run continuing work an earlier attempt preserved, a decided repair or a
re-run started from a preserved branch; then everything else. Within each, the
one that asked first starts first. A landing takes its turn on its target
branch first and its place second, never the other way round. A confirmed
document's landing takes a place only for a document check named here.

**A place is kept free for landing checks.** The order above cannot help a
landing check that arrives when every place is already running ordinary checks:
it would wait for a whole check to finish, and every approved change behind it
in the merge queue with it. So while the harness's merge queue holds an entry
that still has a heavy check to pass on its candidate, every check that is not
for a landing — ordinary checks and work continuing preserved changes — is let
start only while those checks number at most one fewer than the check's places.
The landing check always finds a place free, and nothing running is stopped to
make one. When no entry in the queue needs the check, ordinary checks use every
place. A check named with one place keeps nothing, because keeping its only
place would stop every ordinary copy of it whenever the queue held an entry; its
landing checks are still first in the line. A candidate stopped and run again
keeps the claim until it has passed the check or leaves the queue. It is read
from the queue's own record each time a place is asked for, so nothing is stored
and nothing has to be cleared when the entry lands, is handed back, or is
withdrawn. The number of places is unchanged.

The places are kept in one record under the state root, `heavy-checks/`,
changed only under a file lock beside it. A place moves through four states,
each recorded before the step it names: waiting, running (given the place and
started in a process group of its own), exited (its main process ended and its
result is recorded), and released. **A place is given back only once every
process the check started is gone**, read the way a provider's processes are
read: a file the check's processes inherit, whose lock goes only with the last
of them, and a process group with no members left. A check that leaves a
process behind keeps its place, and the record says why on that permit, until
the process goes. Every other run's check of the same name on the machine waits
behind it meanwhile, so it is an entry on the attention line in `yoyo status`
and on the dashboard, labelled *check place kept* and the development manager's
to settle, naming the check, the run and its item, and why the place is kept.
`yoyo heavy-checks` lists every place running, waiting, or kept on the machine,
and `yoyo heavy-checks release <permit> --by <name> --reason <why>` gives a kept
place back once she, or a person, has decided the leftover process is a hang;
the release is written onto the permit with who gave it back and why. A process
the harness launched for any other role is refused it.

If the record of places cannot be opened, a heavy check is not started without
its limit: it is refused, naming why, and the run stops at its checks the way it
stops for any other failure of the harness rather than of the change.

A harness that restarts reads the record before it gives out anything. A
running check whose processes are still alive keeps its place; one whose
processes are gone is released. A run that comes back and asks for a check it
was already running waits for that launch to end rather than starting a second
beside it, and a run that died while waiting keeps its place in the order — it
holds nobody up meanwhile — and takes it up again when it comes back. Nothing
resets the count.

[What limiting heavy checks costs, and what it saves](../experiments/yoyodyne-ifd-429-47-heavy-check-permits.md)
records what a place costs — about a sixth of a second per heavy check — and
what a limit of one did to wall time, CPU time, and waiting on a busy machine.
#### Setting a heavy check's number from what it cost

**Every launch of a heavy check is measured**, beside the record of places, in
`heavy-checks/measurements.json`: how long it waited for its place, how long it
ran against the time limit it launched with, and how it ended — passed, failed,
stopped by its time limit, stopped for going silent, or stopped by its run. As
it launches the harness also reads the machine: the one-minute load, the memory
the operating system reports free, how many of each heavy check hold places, and
every process whose command is one of the heavy checks but holds no place —
which is a developer running `make test` inside its own session, where no place
is asked for. Only the last 512 launches are kept. A measurement that cannot be
written never stops a check and never keeps its place: the write is tried three
times, and if it still fails the run's own record says so in plain words. A
launch whose end could not be written down carries the reason instead, and a
launch whose harness stopped before its end was written down is kept with no
end; neither is read as a pass.

`yoyo heavy-checks measurements` reads them. For each heavy check it gives how
its launches ended, how long they waited, the load they started under, how many
started beside a suite run outside every place, and, for each number of it
running at once, how long the launches ran against their time limits. For the
machine it gives the load and free memory against how many heavy checks of
every name were running. And it proposes a number for each check:

- A number of the check running at once **holds** where at least five launches
  finished at it and none was stopped by its time limit, and the longest took
  no more than three quarters of its limit, which leaves room for a slower day.
  One launch stopped by its time limit is enough to say a number does not hold.
- The number proposed is the one below the lowest number that does not hold. A
  check that does not hold even alone is told that fewer at once cannot help:
  its time limit, or the suite, is what has to change.
- Where everything measured holds, the number proposed is the one it ran under,
  and **never more**: a check limited to two has never been seen running three
  at once, so nothing measured says three would hold.
- Too few launches at the configured number propose nothing, and say so.
- A launch whose end could not be written down is listed on its own, with the
  run it belonged to and the reason the harness stored, and is left out of
  every number proposed: how long it ran is not known.

The command proposes and changes nothing. The development manager applies a
proposed number through the reviewed path a configuration change takes. Until
then the numbers above stand.

#### How long the heavy checks took, week by week

The measurements keep only the last 512 launches, which is a few days on a busy
machine, so the figures that show the effect of a change to the checks over
weeks are read from every run's event log instead, which go back as far as the
runs do. `yoyo status --checks` prints them week by week, Monday to Sunday in
the machine's time zone, which it names, and the dashboard's
[delivery page](../operations.md#the-delivery-page) shows them day by day and week
by week. For each heavy check and each period it gives:

- how many times the check ran, and how many of those passed;
- the median time of the runs that passed;
- the median wait for a free place to run it, from the check asking for its
  place to it launching;
- how many runs were told to run the whole module, with each reason the harness
  gave and how many runs gave it, and how many were told only the packages the
  change touches.

A run counts in the period it launched in; one stopped before it launched ran
no time and is not counted. A check's start event has said what it was told
since this reading was added. For an older event the run's record of its last
check stage says it where the check ran inside that stage, which covers the
merge candidate's checks, and any other older run is counted as not recorded
rather than guessed. What is counted is what the harness told the check: a
project's own check may still run the whole module for a reason of its own —
this repository's `scripts/check-scope.sh` does for a change to the Makefile
that touches no Go package — and only the check's output says so. `--json` carries the same figures.

What the measurements do not see: a suite run under a name `heavy_checks` does
not give — `make check`, which runs the race suite among the others — is not in
the count of suites outside every place, and shows only in the load. Where the
machine's processes cannot be listed, the count is left unknown for that launch
rather than counted as none, and the command says for how many. Inside a sandbox
that refuses the reading, as a developer's own session on macOS does, free
memory is left out; the harness reading it is not in one.

What the places do not cover: a suite a developer runs inside its own session is
its own, and takes no place, though the measurements count it; and a
[suite the forge runs](#where-the-whole-suite-runs) takes none either, while a
fallback to running it here does. The places count one machine's state root: two
state roots on one machine count separately.

### Checks a change runs only when it touches what they vouch for

Some checks vouch for one part of the repository and cost something whatever
they are given: a walkthrough of the documented install, say, which a change to
the documentation or to the program it documents can break, and a change to a
design note cannot. `path_checks` is a list of those, each a command and the
file in the repository listing the paths it vouches for:

```yaml
path_checks:
  - command: make getting-started
    paths: scripts/walk-adoption.paths
```

The per-run gate runs every entry in `checks` and then each path check whose
list covers a path the change touches, in the order written, under the same
stage bound, in the same environment, and to the same effect: a path check that
fails is a failing check, handed to the developer to repair like any other, and
one that passes is a check result the review is shown beside the rest. A change
that touches nothing on a path check's list does not run it. The run's record
and the item's notes say which path check the gate added and which changed
path added it, and name each one it did not run as touching nothing its list
covers, where they say what the gate was narrowed to.

The list lives beside the thing it describes rather than in this file, and a
change that edits it runs the check whatever the edited list says. Otherwise
the author of a change could narrow or empty the list and switch off the check
meant to hold that change; so a change to the list costs that change one run
of the check, and for every other change the list reads as the target branch
holds it. It takes the part of a `.gitignore`'s syntax everybody already
reads: one pattern a line, blank lines and `#` comments ignored; a pattern with
no `/` matches any component of a path, so `*_test.go` is every Go test file; a
`/` anchors a pattern at the repository root; a trailing `/` means a directory
and everything under it; and a leading `!` takes back what an earlier line
covered, the last matching line deciding. A list that cannot be read — missing,
or carrying a line that is not a pattern — runs its check rather than passing it
over, because the gate has no declaration to skip it on, and the record says so.

A path check runs with the provider CLIs hidden from its search path, like
every other check, unless it sets `needs_provider_clis: true`. That is for a
check whose whole point is how the harness drives a real provider CLI, such as
`make codex-resume`, the Codex native-resume test
([where it runs](runs.md)); it lifts the hiding for that check
alone, and gives no agent anything.

```yaml
path_checks:
  - command: make codex-resume
    paths: scripts/codex-resume.paths
    needs_provider_clis: true
```

Path checks are not landing checks and are not narrowed: a landing runs
`landing_checks` alone. An entry with no command, or with a `paths` that is
absolute or leaves the repository, is refused when the configuration loads.

### The document checks

A design, a decision record, or the goals a role writes and the harness
confirms lands through a reviewed run of its own, a document landing, with no
developer in it ([documents written in conversation](../artifacts.md)). Those runs
are counted apart from the developer slots: at most
`execution.document_landings` of them go at once, two unless you say otherwise,
and none of them takes or waits for a developer slot, so a document is never
stuck behind development work and no development run waits on a document.
Their checks still run through the same check runner as every other run's, so
whatever bounds the machine's checks bounds theirs.

```yaml
execution:
  document_landings: 2
document_checks:
  - go test ./internal/terms ./internal/doclink ./internal/artifact
```

`document_checks` is the list a document landing runs in place of `checks` and
`path_checks`: the tests that read the governed documents, rather than the
whole suite and never a race check. A project that declares none has its
document landings run `checks` and the path checks the change adds, exactly as
every other run does. A test that reads the documents and is missing from the
list is caught by `landing_checks` on the target branch, if you run the whole
suite there; add it to the list then. The record of the checks a landing
passed names the digest of the list it ran, and its promotion refuses that
record if the list in force has changed since.

Only a change whose every path lies inside the artifact homes — `product.specifications`
with its goals, `product.designs`, `product.decisions`, and
`product.invariants`, each home's `README.md` included — takes this route.
Anything else is refused from it before any check runs. Independent review and
the promotion through the pull request, and the merge queue where it is on,
are the same as for any run. With `execution.merge_queue` on, the document's
run admits it to the queue and ends, and the queue checks its candidate with
`document_checks` where you declare any, and with `checks` whole where you
declare none; a candidate the queue refuses ends the run with nothing handed
to a developer, and the document goes back to its owner as any document that
did not land does. An entry with no command is refused when the
configuration loads, and `document_landings` may not be negative.

### The checks run on main

`main_checks` is a list the supervisor runs whole on the head of the branch
runs land on, on a schedule of its own: every `execution.main_check_interval`,
four hours unless you say otherwise, and only when the branch has moved since
the last run. It is for a check you have taken off each change and want run on
what has landed instead, such as a race detector over the whole module.

```yaml
main_checks:
  - make race
execution:
  main_check_interval: 4h   # the default; at least 30m
```

Each run reads where the remote has the branch, fetches it, and checks that
commit out in a worktree of its own under the product's state directory. It
never touches your checkout and never moves a branch. Where the remote cannot
be reached it checks the local branch and says so in the run's record. Every
check runs whole there with `execution.landing_check_timeout` as its budget and
no bound on the list, and waits for a place like any other check where
`execution.heavy_checks` names it. The worktree is removed afterwards.

Every run is recorded with the commit, when it started and ended, and how each
check ended, and `yoyo status` says when the branch last passed and what the
latest run came to where it did not pass. A check that fails is run once more on
the same commit, in the same worktree, with the tests and packages its output
named failing given to it in `YOYODYNE_FAILING_TESTS`, one to a line. A check
that reads the variable runs only those; one that does not runs whole again.
This repository's `make race` reads it (`scripts/rerun-failing.sh`). Where that
second run passes, the tests fail under load rather than on the commit: the run
is recorded as passed, `yoyo status` names them, and a warning names them with
both runs' results. Such a run is not a clean run: the last clean commit that
`yoyo status` and a later failure's report name stays the last commit whose run
passed on its first try. Where it fails again, or where the failure named no test and
so is not run again, the run files a critical report naming the commit, both
runs' failing tests, the last commit the checks passed on at the first try, and the commits that
landed between them. Either way the next run waits for the branch to move. A check that could not run is recorded as
such, files a warning naming the commit and the reason (once for a stretch of
tries on one commit), and is tried again ten minutes later rather than at the
next interval; the regular schedule is unchanged. Before each run, anything an
earlier run left at its own checkout path, including a half-written one that
Git no longer lists, is removed, and the checkout itself is given ten minutes.

The schedule is kept in that record, so the supervisor restarting does not
start a run early: the next run is due an interval after the latest one
started. While a run is under way the supervisor waits for it to finish before
it moves onto a new build. A project that lists no `main_checks` runs nothing
on its branch. Both keys may be changed by the development manager.

### The checks the merge queue runs

With `execution.merge_queue` on, the harness's merge queue runs checks on the
candidate it lands. A project that says nothing has every configured check in
`checks` run whole on exactly the candidate commit. `merge_queue_checks` names
the ones the queue runs instead: each must be a command written exactly as
`checks` writes it, and each is run whole on exactly the candidate commit.
The list and its digest are recorded with the candidate's evidence, and a
candidate lands only on the list in force when it lands.

```yaml
checks:
  - make test
  - make race CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"
main_checks:
  - make race
merge_queue_checks:
  - make test
merge_queue_race_check: make race CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"
```

`merge_queue_race_check` is the one check the queue adds to the list for a
candidate that needs it. It must be a configured check and must be left off
`merge_queue_checks`, which it needs. The queue adds it when the change's
approved head has no passing result of it and the change touches Go packages.
It is run after the declared list passed and is told only the packages the
change touches, through `YOYODYNE_CHANGED_GO_PACKAGES`, so no change lands
without a race check on its own code. A change whose run recorded a passing
result over the approved head, or whose record says it touches no Go package,
is not checked by it again. Where the record cannot say, the check is added.

A change whose own run narrowed its checks leaves the whole pass to the queue,
as before. It lands only where the queue ran the declared list whole on the
landing commit, every check on it passed, and the approved head carries a
passing race result or the queue ran the narrowed race check; anything less is
refused at the landing. Leave the race check off `merge_queue_checks` only once
`main_checks` runs it on the branch, as above. The queue's checks of a confirmed document
are `document_checks`, not these.

With checks on CI on (`execution.ci_checks`, below) and the project publishing,
the queue runs neither list on a change's candidate: it runs the quick checks
`execution.ci_checks.local` names, and GitHub's required checks on the
candidate's own commit stand in for the rest, as the next section says.

### Checks on CI (`execution.ci_checks`)

`execution.ci_checks` is the switch for moving most of a run's testing to the
forge, described in `docs/designs/checks-on-ci.md`. It is off by default, and
off (or absent) a run's checks, timing and records are exactly what they were.
This setting is the switch and its validation. The GitHub adapter can push a
run's branch, open or update its draft pull request, and read each required
check's result and a failing check's output for a named commit.

With the switch on, a developer run's checks before review are only the ones
named in `local`. When they pass, the harness pushes the candidate commit,
opens or updates its draft pull request, and records the wait on the run: the
commit, the push time and the required checks. The run then holds no developer
slot. `yoyo status` and the dashboard list it as waiting for CI, with the time
waited. A watching `yoyo work` session reads the forge for that exact commit
about once a minute and continues the run when every required check has passed,
when one has ended any other way, when the forge has stayed unreadable for about
five minutes, or when `wait_limit` has passed since the push. Review starts only after every required check passed on that commit. Until the forge's results are themselves integration evidence (a later build), the full checks also run on this machine over the approved candidate before it is promoted, as they do for any change checked in part. A
restart during the wait pushes nothing and carries on with the same wait.

A required check that failed on the forge goes back to the developer as a
failed check. The harness reads the forge's log, because a developer's sandbox
has no network, and records on the run the failing checks, the failing tests
the log names, and the end of each failing check's output, with terminal colour
codes and secrets removed, in the shape a local failed check is recorded in (so
each check keeps a share of the same 8 KiB a local check's output is held to).
It spends one repair round. After the repair the short local check runs again,
the new commit is pushed, and it needs a result of its own: a result for an
earlier commit is never carried forward. If a failed check's log cannot be read,
the failure still goes back to the developer, naming the check, why its log
could not be read, and a link to it; the full checks do not run on this machine
instead, because the forge has already said the check failed. The harness also
files one warning report for the development manager for that commit, naming
each check whose log it could not read and why.

When the wait ends any other way, the run runs the full declared checks on this
machine over the same commit and goes on from their result, and the reason is
recorded on the run. That happens when a required check has not finished, or
never started, within `wait_limit`; ends cancelled, skipped, or in any state
other than passed or failed; when the forge cannot be read for about five
minutes running (a single failed read is waited out); and when the switch is
turned off during the wait. None of these spends a repair round, because the
cause is outside the change; only a check that fails does.

After that fallback the harness reads the forge once more for the same commit,
when the local checks have passed and again before the approved change is
promoted. An answer it finds is recorded on the run too, and a failure from
either the forge or this machine counts as a failure: a required check that
failed on the forge goes back to the developer as a failing check and spends a
repair round, even though the local checks passed. That failure carries the
failing tests and the end of each failing check's log, read the same way as
above, and not only a link to the forge's page. The other stages of the flow
arrive in later builds.

A push the forge refuses, or a draft pull request it will not open, blocks the
item for the development manager with the forge's own words, because merging
the change would need the same forge. The branch and worktree are kept, and no
repair round is spent.

With `execution.merge_queue` on as well, and the project publishing, the merge
queue checks each change's candidate the same way rather than running its full
checks on this machine too. The candidate runs only the checks `local` names,
in the order `checks` lists them, and, while no name in `required` is a word of
`merge_queue_race_check` (as `race` is of `make race`), that race check told only
the packages the change touches, where the change owes it. Once those pass, the
harness pushes the candidate to its candidate branch and opens the pull request
that carries it, so GitHub runs the required checks on that exact commit. The
independent review runs while GitHub's checks do; the harness reads them every
30 seconds meanwhile, and a required check that fails on the candidate stops the
review. The candidate lands only once its review approved it and every required
check passed on its own commit, read by the harness with the workflow files'
digest and with the target still at the candidate's base. A required check
still running is waited for on the queue's next pass, holding no developer slot.
A failure is the change's, and goes back to its run for a repair, only where the
same check passed at the candidate's base on GitHub; where it failed there too,
the entry waits for the target to move. A required check that ends cancelled,
skipped or any way but passed or failed, or has not finished within `wait_limit`
of the push, has the entry's next candidate run the full checks on this machine
instead, with the reason recorded and no repair spent. A confirmed document's
candidate still runs `document_checks`.

```yaml
execution:
  ci_checks:
    enabled: false
    local: [make fmtcheck, make vet]   # declared checks still run on this machine
    required: [test, race]             # forge checks that must pass on the change
    wait_limit: 1h                     # how long to wait for them after the push
```

- `local` must name checks the project declares in `checks`, written exactly as
  `checks` writes them; a name that is not one is refused when the file is read,
  whether or not the switch is on.
- `required` must not be empty while `enabled` is true.
- `enabled: true` is refused when the project's forge adapter cannot push a
  branch, open a draft pull request, and read check results and output for a
  named commit. The refusal names what is missing. The GitHub adapter, the only
  one shipped, does all four.
- `wait_limit` is 1h when not set.

`yoyo config show --origins` says which file each value came from.

### The companion intent repository's checks

A confirmed document whose home is a companion intent repository
(`intent.repository`) is landed by a run cut from that repository's target
branch, not from the project's code. That run runs `intent.checks` and never
`checks`, `document_checks`, `landing_checks`, `path_checks`, or `install`,
because those are the code repository's commands, and a repository that holds
only documents has nothing for them to build or test.

When you name no list, the run uses two commands:

```yaml
intent:
  checks:
    - yoyo artifact check --intent .
    - yoyo goals check --intent .
```

`yoyo artifact check` reads every governed document, its index, and the links
written in them, and exits non-zero on a document that is malformed.
`yoyo goals check` reads the goals the same way `yoyo goals list` does and exits
non-zero when a goals document could not be read, a goal is not linked to the
brief, or a goal is not written on one line.

Each command runs in the run's own checkout of the intent repository, with
`YOYODYNE_CONFIG` naming the project's configuration, which the harness sets.
`--intent .` points the command at that checkout, so it reads the candidate
document rather than the checkout the configuration names.

To use a list of your own, write it in the configuration kept in the project's
directory in the machine home, under `intent:` beside `repository:`:

```yaml
intent:
  repository: intent
  checks:
    - yoyo artifact check --intent .
    - yoyo goals check --intent .
    - ./scripts/spell-check.sh
```

A list you write replaces the default whole, so keep the two commands above if
you still want them. An entry with no command is refused when the configuration
loads, and so is `intent.checks` in a configuration with no `intent.repository`:
the list is what a landing into the companion intent repository runs, so it means
nothing without one. An entry may be named in `execution.heavy_checks` like any
other configured check.

### A check that could not run

A check can end a third way besides passing and failing: it could not run at
all. The tool it drives is not installed where the harness runs its checks, or
the service it talks to cannot be reached from there. Such a check has judged
nothing about the change, and a developer cannot repair a missing tool, so
treating it as a failure would spend repair attempts on a cause no change can
fix.

A check says it could not run by printing a line that begins
`yoyo check could not run:`, followed by the reason, on either stream, and
exiting non-zero:

```sh
command -v codex >/dev/null || {
  echo "yoyo check could not run: codex is not installed on this machine" >&2
  exit 1
}
```

It is a line rather than a reserved exit status because a check is usually a
make target, and `make` replaces whatever status its recipe exited with by its
own; the line reaches the harness through any wrapper. A check that exits zero
passed whatever it printed, a check stopped at its time budget was stopped on
time, and the prefix with no reason after it is not recognized, so each of
those is judged as it always was.

A check the machine refused to start could not run either, without printing
anything itself. On macOS, while the Xcode licence is unaccepted or Apple's
developer tools are missing, `make` and the tools beside it exit
before doing anything (`make` with status 69) and print the same line for every
target. A check that exits non-zero with one of those lines is recorded as could
not run, with the reason `the machine refused to start it:` followed by the
line. Only those few known lines count; any other message is judged as the
failure it looks like.

A check that could not run:

- spends no repair attempt and does not stop the change: the checks after it
  still run, and if they pass the change goes on to review and can land;
- is said on the run as could not run, naming why: in the reviewer's evidence,
  in the run's notes on the work item, under the run in the list of recent runs
  `yoyo status` prints, and in the run's record as `check_stage.could_not_run` and
  `checks_passed.could_not_run`;
- makes no landing red: a landing check that could not run is named in the
  landing's line and the rest decide whether it is green.

A check that could not run lets a change go on only beside a check that ran
and passed. When every configured check could not run, nothing verified the
change, so it is not reviewed or integrated. Instead the run stops with the
cause `no-check-ran`: the item is blocked with a note saying no check verified
the change and naming each check and why it could not run, the branch and
worktree are kept, and the stopped run is put on the development manager's list.
No repair attempt and no review round is spent on it, and it counts toward no
hold on new work. A project with a single check meets this whenever that check
cannot run.

What stops every check is usually the machine, so every other ready item would
stop the same way. The session that started the run therefore says so once and
starts no new work.

Where the harness recognises the cause, the hold waits on a follow-up
(see [the follow-ups design](../designs/follow-ups.md)). The causes it recognises
are an Xcode license nobody has accepted and a tool missing from the search
path. The follow-up carries a check the harness runs itself:
`xcodebuild -license check` passing, or the tool being found on the search path.
The watch session runs open follow-ups' checks at the top of its polls: an
environment check every 10 minutes, any other check every 5 minutes, one at a
time. Each check's result is recorded on its follow-up only when it changes, and
a check that cannot be run counts as not passed and says why. Once the check
passes, the harness closes the follow-up with the time, releases the hold,
starts work again, and says so. The message goes on the watch log `yoyo status`
prints and in the channel. The development manager, who waits on the follow-up,
is told by a report addressed to her, which is put in front of her next pass.
Nothing else re-checks the cause: no run is started to find out. The two
intervals are fixed for now. The settings the design names for them,
`follow_ups.check_interval` and `follow_ups.command_check_interval`, do not
exist yet; they come with the follow-ups' deadlines.

A provider whose login has lapsed is handled the same way. The outage waits on
a follow-up whose check asks the provider whether it is logged in, and is
cleared when that check passes. If the follow-up cannot be written, the outage
stands and the session says why; it does not ask the provider itself.

A cause the harness does not recognise gets no follow-up. For that cause the
session keeps the old behaviour: it starts no new work for the interval
`execution.usage_limit_unknown_reset_pause` gives (30 minutes by default). After
that it starts one run, however many developer places are free, to find out
whether the checks can run again, and starts nothing else until that run ends.
If the checks ran, work goes on as normal; if not, it holds again for the same
interval. The operator's own pause is never released by a follow-up.

A real failure is still a failure: a check that ran and exited non-zero without
that line is handed back to the developer exactly as before, whatever another
check said. So is a check that printed the line and whose output also names a
failing test or package (a `--- FAIL:` line, a `FAIL` package line, and the
like): one step of it could not run and another ran and failed, or the code
under test printed the line itself, and a failure wins over the line.

Every change that goes on without a check is a change nothing checked that
way, and each run says so only on itself. So once the same check has been
unable to run on several changes in a row, `yoyo status` says so on its fourth
line, under the development manager, naming the check, how many changes, and
since when. A change the check runs on clears it, and so does changing the
check's command or taking it out of `checks` and `path_checks`, because a check
is known by its command:

```yaml
execution:
  could_not_run_before_status: 3   # the default
```

A run whose checks stopped before reaching the check neither counts toward the
number nor clears it.

Claude Code reports how much of each account's usage limits is used on nearly
every invocation, and the harness records the latest reading of each limit;
the dashboard's status band and `yoyo status` show it as information. A limit
past this share used is also shown as degraded capacity among the dashboard's
factory problems, until a reading below it arrives or the limit resets. It
changes what is shown and nothing the harness does — the provider's own refusal
is still what stops work:

```yaml
execution:
  usage_window_degraded_percent: 90   # the default
```

### Naming a failed check's failing tests

When a check fails, the harness can record which tests failed and which file
each one is in. It does not know any language's test output, so it learns this
from you: `failing_tests` says how a check's output names its failing tests.
A check you declare nothing for is recorded exactly as before.

```yaml
failing_tests:
  - checks: [make test, make race]
    test: '^\s*--- FAIL: (?P<test>[^\s/]+)'
    file: '^\s+(?P<file>[^\s/:]+_test\.go):[0-9]+: '
    group: '^FAIL\s+(?P<directory>\S+)'
    strip_prefix: example.com/shop
```

Each entry is a reading, and these are its keys:

- **`checks`** names the checks it applies to, the way
  `execution.heavy_checks` names them: `make test` is every configured command
  that is `make test` or begins with `make test` and a space. Where two
  readings name the same command, the longer name wins. A name that matches no
  configured check is refused when the configuration loads.
- **`test`** is a regular expression for a line that names a failing test. Its
  `(?P<test>...)` group is the test's name. If the same line also names the
  file, a `(?P<file>...)` group says so and `file` can be left out.
- **`file`** is an expression for a line that names the file the failing test
  above it is in, by its `(?P<file>...)` group. A line it matches when no test
  is waiting for a file is ignored.
- **`group`** is an expression for a line that ends a set of failing tests and
  says which directory their files are in, by its `(?P<directory>...)` group.
  Leave it out if the file names are already complete paths.
- **`strip_prefix`** is taken off the start of each directory `group` names, so
  a name the test runner prints, such as a Go import path, becomes a directory
  in the repository. A directory equal to it is the top of the repository.

Each expression is matched against one line of the output at a time, and uses
Go's regular expression syntax. An expression that does not compile, or lacks
the group it needs, is refused when the configuration loads.

The example above is the reading for `go test`. It prints `--- FAIL: TestName`
for each failing test, with a subtest counted under its test. It then prints
lines such as `    cart_test.go:12: ...` naming the file the test failed in,
and then `FAIL<tab>example.com/shop/internal/cart` for the package. Taking the
module path off the package gives `internal/cart/cart_test.go`. A runner that
prints the test and its file on one line needs only `test`. For pytest:

```yaml
failing_tests:
  - checks: [pytest]
    test: '^FAILED (?P<file>[^:\s]+)::(?P<test>\S+)'
```

**What is recorded.** After a check that ran and exited non-zero, the run's
record lists each failing test with its file, under
`check_failure.failing_tests`. The check's failure output, which goes to the
developer, the work item's notes and the development manager's list, says the
same in one line. A merge queue candidate that is handed back over a failed
check names the failing tests in the reason it is handed back with. At most
twenty tests are listed, and the line says how many more there were.

**When the output cannot be read that way.** The reading is all or nothing. If
any failure in the output cannot be named as a test with its file, the record
says so and why, and lists no tests. Examples are a package that did not build,
a test binary that ran out of time, a test whose failure line points at a file
that is not a test file, or output that stops before the line saying where the
tests were. The check is then handled exactly as it would have been without a
reading.

**Whether the change touched them.** Where the tests were named, the run's
record also judges each one against the paths the change touched, under
`check_failure.touched_tests`. A test is touched when its own file changed, or
when any changed path is in the same directory as its file; a deleted file
counts, and so do both the old and the new path of a renamed file. The record
says whether the failure is confined to tests the change does not touch, and
why: that needs every failing test named with its file, none of them touched,
and at most five of them. Anything else is an ordinary failed check. The design
also lets a project's change-aware analyser add tests to the touched set, but
no setting declares one yet, so the path rule decides alone.

**Running those tests once more.** Where a run's check fails only in tests the
change does not touch, the harness runs those tests once more, on the same
commit, before any repair attempt is spent. It runs the check's own command
again under the check's own time limit, with the tests named in
`YOYODYNE_FAILING_TESTS`, one to a line: each test's name, then the directory
of each test's file as `./dir` (`.` for the top of the repository). It knows
nothing of your test runner, so a check narrows itself by reading the variable;
one that does not read it runs whole again. This repository's `make test`,
`make race` and `make test-macos` read it (`scripts/rerun-failing.sh`).

- The rerun is written on the run's record, under `untouched_reruns`, before it
  starts. Each check stage gets one per candidate commit: the checks before
  review, and the checks over the merge candidate before it is promoted. A
  restart of the harness finds the rerun it already spent and never grants a
  second for the same stage and commit. A new candidate commit starts again.
- If every rerun test passes, the check counts as passed, the checks after it
  run, and no repair attempt or review round is spent. The record keeps the
  first failure with its output beside the passing rerun, and the reviewer is
  shown every test that failed and then passed, with the first failure's
  output.
- If a test fails again, or the rerun is stopped by its time limit or cannot be
  started, the first failure is handled as an ordinary failed check, exactly as
  it would have been without the rerun.
- It is separate from the two runs again of a check stopped by its time limit:
  neither counts toward the other.
- The merge candidate is still checked whole before it is promoted, even where
  the checks before review already ran whole, so a test that passed only when
  run once more runs again before the change lands.

The run on the base commit after a second failure, and the dashboard's count of
tests that failed and then passed, are later work (see [the design for failures
in tests a change does not
touch](../designs/recoverable-and-terminal-failures.md#a-check-that-fails-only-in-tests-the-change-does-not-touch)).
A reading by itself only describes a failure: a check no reading names, or one
whose failing tests could not be named, fails or passes exactly as before.

`failing_tests` is a configuration key. A running part of the product on a
build from before it refuses a file that carries it, so restart the product
after upgrading and before adding it.

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
reconcile on top of the target, and the result is checked and reviewed again.
The first conflict is the run's one reconciliation attempt, which spends no
repair attempt; a later one is a repair attempt, and the run blocks only once a
budget is spent. Nothing is ever forced.

Thirteen things keep an item out of a pass, reported at two different grains. The
first ten are named against the item, because nothing else would report that
this item was passed over; the last three are facts about the pass rather than
about any one item.
[How work flows](../work.md#letting-the-harness-choose-the-work) lists the same
thirteen in the same order, and a test fails when the two lists differ:

<!-- selection-rules: the same names, in the same order, as docs/work.md; internal/doclink/selectionrules_test.go holds them together -->
1. **An unresolved directive** withholds the item until a person resolves the
   directive, and is named in the directive's own words.
2. **Unfinished children that carry its execution** withhold a container while
   any child it was broken into is queued, blocked, or claimed, and release it
   once the last of them leaves the backlog.
3. **A race with work in flight** withholds an item that shares an epic
   decomposition or files with a run in flight, and releases it at the first
   pull after that run ends. Files alone never hold a priority-0 item behind a
   run over less urgent work.
4. **A conversation executor** withholds an item whose `executor` names a
   persona conversation from every developer run; nothing clears it, and what
   moves the item is somebody opening the conversation it names.
5. **Parking** withholds an item the Lead Product Manager parked however far the
   queue drains, and only her `unpark` releases it.
6. **The needs-design label** withholds an item labelled `needs-design` from
   every start the harness chooses until the architect's design for it is
   readable, and only the architect's removal of the label releases it; a run
   already going on the item carries on, and one you name is not held.
7. **A hold** withholds an item whose stopped run left its change on a branch,
   or whose publication did not finish, or whose approved change is still in
   the merge queue, until the development manager's decision is carried out,
   the escalation is answered, `yoyo reconcile` settles the publication, or the
   queue lands the change or records handing it back on the run. A queue entry
   that was released but whose hand-back the run does not record still holds
   the item, so its blocked status is never cleared as left over and no fresh
   run starts beside the approved change.
8. **A step only a person can take** withholds an item that declares a
   `human-gate:` nobody has recorded taking, and only a person's
   `yoyo gate record <name> --for <item>` releases it; closing an item never does.
9. **A prerequisite the tree does not meet** withholds an item that pinpoints
   code the repository no longer has, or says in its own words that something
   must land first; a pinpoint releases it when the code lands, and a sentence
   when the item is amended or the dependency recorded.
10. **A label another slot prefers** withholds an item every free developer slot
    walked past for its preferred label, and the next slot with no preference to
    come free — or the preferring slot, once its label's work is exhausted —
    releases it. A priority-0 item is never walked past for a label, except by
    a slot marked dedicated to its label, which leaves it to the other slots.
11. **The tracker not calling it ready** withholds an item with unfinished
    dependencies or a status that is not open, and the tracker's own readiness
    releases it.
12. **A run already in flight for it** withholds the item while that run lasts,
    and the run ending releases it.
13. **No free developer slot** withholds everything once the slots are taken,
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
item down the order that races nothing. A priority-0 item is not held behind a
run over less urgent work for sharing files with it: it is started, and the
less urgent change is the one likely to replay. It still waits behind a run
over the epic it was broken out of. An item says which files it will change
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

A decided repair or re-run is fired by the same pass. One that builds on a
change a run already wrote and kept — a repair of a stopped run whose branch or
worktree is still there, or a re-run of a run whose branch is still there,
whatever stopped it, which starts from that change,
like the harness's own continuations and a finished merge — takes a freed
developer slot ahead of fresh work of any priority, whatever label the slot
prefers, the highest priority first among several, and its reason names the
ready work of higher priority it went ahead of. Every other decision goes ahead
of ready work at its item's priority or below and behind ready work of a higher
priority, oldest decision first. A confirmed document waiting to land takes no
developer slot at all: it is counted against `execution.document_landings`
rather than `max_concurrent_developers`, and starts as soon as a document
landing frees, whatever the developer slots hold; nothing already running is
stopped for it.
[How work flows](../work.md#letting-the-harness-choose-the-work) has the whole
rule; it changes no gate and nothing a decision costs.

A fourteenth thing deliberately keeps nothing out: an item whose goal was amended
after it was admitted is pulled exactly as it would have been, and what changed
goes into the run's recorded reason instead. See
[what a change upstream leaves stale](goals.md#what-a-change-upstream-leaves-stale) for
why staleness reports rather than decides.

`max_concurrent_developers` cannot exceed the number of developer `instances` you
configured, and the default of `1` is deliberate: raising it is a decision about
your machine, and [how long a check may take](#how-long-a-check-may-take) is the
setting that has to move with it.

More than one run means more than one process writing to the tracker, and `bd`
does not protect two writes to one item from each other: two notes appended to
one item at the same moment can leave only one of them, with both commands
reporting success. So the harness queues its writes to any one item, across
every process using the same tracker, on a lock file per item under
`yoyodyne-writes/` in the tracker's own directory (`.beads/` in an ordinary
checkout). That directory is found the way `bd` finds it, so a process reaching
the tracker from a subdirectory, a linked worktree, or a redirect queues on the
same lock. Writes to different items do not wait for each other.
A write that waits longer than the tracker command limit for the one ahead of
it fails with an error rather than going ahead unqueued. The queue covers the
harness's own writes only: a `bd update` typed by hand is not in it.
[The diagnosis](../diagnoses/yoyodyne-ifd-433-23-concurrent-writes-to-one-item.md)
has the measurements.
### A developer slot that prefers a label

Each unit of `max_concurrent_developers` is a **developer slot**: the capacity
one developer run takes. By default every slot pulls in the order you set. A
slot can instead prefer a **label** — the tracker's own labels, which the
Lead Product Manager and the development manager put on work items — and then it
pulls the ready work carrying that label first, wherever that sits in the
order below priority 0, and the rest of the backlog only when none of its
label's work is ready.
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
  reliability label the item carries. Priority 0 is the exception: a preferring
  slot takes ready priority-0 work before any of its label's work at a lower
  priority, label or none, so a preference never starts less urgent work ahead
  of the top of the order. The run's recorded reason says the item does not
  carry the slot's label and was taken ahead of that work because it is at
  priority 0. A slot marked `dedicated` is the one exception to this
  ([below](#a-slot-dedicated-to-its-label)).
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

A replay test in `internal/orchestrator` reads the block above out of this
document, loads it as a configuration, and drives the scheduler over it, so the
example is held to doing what these three points say rather than described as
doing it.

A run's record can name the slot it occupies. Where it does, that is the run's
slot and no other: a change of labels, a different start order, a restart, or a
lowered `max_concurrent_developers` never moves it, and a run whose recorded
slot lies beyond the capacity is reported beyond the slots, keeping its number.
The one move is a run taken up again — a repair, a continuation, or a run picked
up by a new worker after a restart — while another run holds its recorded slot:
it goes on in the lowest free slot instead of waiting, records that slot, the
one it moved from, when, and why, says so in its history and on its status line,
and reads its sub-agent setting from the new slot. A run that keeps its slot's
endpoint pair moves the same way and is switched to the new slot's own
endpoints — that slot's pair, or the developer's own endpoint where the slot has
none — so no slot runs another slot's pair; the earlier and the new endpoints
and the configuration revision of each are recorded with the move, its
unfinished developer operation is kept as it stood, and its next developer
attempt starts a new session on the new endpoints rather than resuming the old
one. Every path that takes a run up again hands the store the slots'
endpoints: the pipeline's own, a decided repair, a continuation after a silent
provider stream or at the checks, a resumed merge, the merge queue's attempt to
bring a change up to date, and the reconciling sweep's. While no slot is free
the run waits for any slot to free, not for its own, and its record and its
work item say every developer slot is occupied. When two runs that recorded one slot are due to be continued in one pass,
each goes on in a free slot of its own, the older first.
A run the scheduler pulled into a slot records that slot in every project, or
the first free one, in the order below, where that was taken by the time the run recorded it, and
then its selection reason names the slot it runs in. The recorded slot is also
where the run's sub-agent setting is read from
([developer sub-agents](recovery.md#developer-sub-agents-on-a-cheaper-model)). A run started with no slot named — a re-run or
fresh run a development manager's decision started, or a run started by name —
records the first free slot in the order the slots' preferences place it: a
slot preferring one of its labels, then a slot preferring nothing, then any
other. A run taken up again whose record names no slot records the first free one,
in the same order, as it is taken up. So the health check's list of what holds each slot and
the running line of `yoyo status` name the slot the record names. For a run
recorded before slots were, or one that found no number free, which slot a run is in is read off what is in flight against what the slots prefer, the same way every
time, by the scheduler and by `yoyo status` alike. A run over labelled work is in a slot that prefers its
label while one is unassigned, and everything else is in a slot with no
preference first and in a preferring slot only once those are full — which is
that slot having fallen back. Labels are read from what each run recorded at
its claim, so a run started by `yoyo run` counts against the slots exactly as a
scheduled one does. Where any slot prefers a label, the running line of
[`yoyo status`](../operations.md#where-the-harness-stands-the-four-lines) says which slot each run is in and what that slot
prefers, and names each free slot with its preference under the runs; where
none does, the line reads as it always did. [The dashboard's Running now
section](../operations.md#watching-from-a-browser-the-dashboard) names every slot whatever it prefers: each run's
slot with its preferred labels, a slot with none said to prefer no label, and
each idle slot listed under the runs. An item the only free slots walked
past for their label — an unlabelled item ranked above the reliability item
slot 1 pulled, with no other slot free — is reported by the pass as **left for
another developer slot** rather than as deferred, naming the slot and what it
pulled ahead of the item: the item waits on nothing about itself, and what
takes it is the next slot with no preference to come free, or slot 1 once its
label's work is exhausted.
#### A slot dedicated to its label

A slot that prefers a label may also be marked `dedicated: true`. A dedicated
slot is kept for its label: it takes ready work carrying the label ahead of
priority-0 work that does not carry it, where a slot that only prefers a label
takes priority-0 work first, as above. Every slot not marked dedicated keeps
that rule, so priority-0 work is still taken first, by the other slots. This
is how a slot is given to one kind of work whose items sit below priority 0 —
how teams share one repository, say — without raising those items to
priority 0, which would pull them into every slot:

```yaml
execution:
  max_concurrent_developers: 3
  developer_slots:
    - prefer: [reliability]
    - {}
    - prefer: [team-development]
      dedicated: true   # slot 3 takes team-development work ahead of priority 0 elsewhere
```

The order a freed dedicated slot takes work in is:

1. work on a change a run has already written and kept, carrying its label;
2. such work carrying any other label, or none;
3. fresh ready work carrying its label, in the Lead Product Manager's order —
   highest priority first, then, within one priority, the items she placed
   first (earliest placement first), then oldest admitted;
4. the ordinary order every other slot uses.

The first two are the operating rule that work on a change a run already wrote
goes first on every slot; a dedicated slot only puts the work on its own label
first among it. The last is why a dedicated slot never sits idle: with none of
its label's work ready it takes the ordinary order, and its run's recorded
reason says it found none of that work ready and took the ordinary order.

Where some slot is dedicated, the running line of `yoyo status` says what the
slot is doing in plain words: a run in it is either working on work carrying
its label, or working outside its label because none of its work was ready,
and a free dedicated slot is said as kept for its label. A ready priority-0
item a dedicated slot walked past, with no other slot free to take it, is
reported by the pass as waiting for one of the other developer slots, naming
the item and what the slot took instead, each by its title with its identifier
after it.

Two configurations are refused when the file is read, naming the slot and
why: a slot marked dedicated that names no label under `prefer`, and every
developer slot dedicated, which would leave no slot to take priority-0 work
first. A list shorter than `max_concurrent_developers` always leaves a slot
undedicated. `dedicated` is a new key, so every running part of the product
has to be moved onto a build that reads it before a project's file carries it.

The same slot entries may also carry a `routing` endpoint pair and an optional
`number` confirming their position. [Developer slot endpoints](#developer-slot-endpoints)
states precedence, defaults, validation and reviewer selection. A developer run pulled into a slot with a pair starts on its
primary and moves to its alternate once per operation on a usage limit, as that
section describes. Label preferences keep their existing behavior. An entry may
also carry `subagents`, which turns the developer's sub-agents on or off for
that slot and names their model
([developer sub-agents](recovery.md#developer-sub-agents-on-a-cheaper-model)).
### Developer slot endpoints

A developer slot may name an ordered pair of endpoints under
`execution.developer_slots[].routing`: a primary the slot's runs start on, and
one alternate they move to when the primary reaches its usage limit. Developer
runs use it, as [below](#what-a-developer-run-does-with-its-slots-pair); the
reviewer has a pair of its own, which reviews use as
[below](#what-a-review-does-with-the-reviewers-pair), and the four-slot mapping
in the design is a configuration somebody has to apply. Accepting or printing a
pair does not mean a run has used it: the run's own record says what it used.

```yaml
execution:
  max_concurrent_developers: 2
  developer_slots:
    - number: 1
      prefer: [reliability]
      routing:
        enabled: true
        primary: {provider: codex, model: gpt-6.1-sol, account: codex-account}
        alternate: {provider: claude-code, model: opus, account: default}
    - number: 2
      routing:
        enabled: true
        primary: {provider: claude-code, model: opus, account: default}
        alternate: {provider: codex, model: gpt-6.1-sol, account: codex-account}
accounts:
  default:
    provider: claude-code
  codex-account:
    provider: codex
agents:
  developer:
    instances: 2
```

The list still names slots in order. An optional `number` must equal the entry's
one-based position and fit within `max_concurrent_developers`; duplicates, zero,
negative numbers and reordered numbers are refused. Unlisted slots retain their
existing defaults. `prefer` continues to choose work and does not choose an
endpoint.

An explicit `routing` pair takes precedence over shared developer defaults and
`execution.developer_models` label rules. Without a pair, the first matching
label rule still wins, followed by the shared developer model. Both `primary`
and `alternate` blocks are required. The primary inherits omitted provider,
model, account, model version and effort from the developer agent. An explicit
model clears an inherited version pin; `model_version` may supply its own pin.
The alternate must name a model, inherits provider and account from the resolved
primary, and inherits effort from the developer agent rather than from the
primary's override. It may also name its own `model_version` or `effort`.

`enabled` controls fallback and defaults to false. A disabled pair still selects
its primary, retains its alternate, and validates both. A same-provider alternate
with a different supported model is allowed. Identical resolved endpoints,
unknown providers or accounts, incompatible authentication, unsupported roles
or access, and incompatible model versions or effort are refused. Validation
collects the invalid fields rather than reverting to shared defaults. Capacity
and credential availability are checked at launch, not by this resolver.

The reviewer uses its own `agents.<reviewer>.backend`, `model`, `account`,
`model_version` and `effort`, plus its own `failover` block for the alternate.
It never inherits the developer slot pair or label models. `failover.effort` is
accepted only for developer and reviewer run endpoint resolution. The alternate
retains the existing `enabled`, `provider`, `model` and `account` keys and also accepts
`effort`. For these run endpoint pairs, inherited effort is validated separately
on each endpoint; an explicit `effort: ""` passes no effort, so that endpoint's
provider uses its own configured level (for Codex, the Codex configuration; for
Claude Code, its own resolution).
An incompatible inherited level is refused rather than silently dropped, and a
reviewer alternate must support the reviewer's read-only access. Disabled
reviewer alternates are validated too.

`yoyo config show --effective --origins` prints the configured pairs and their
source layer. The run endpoint resolvers additionally return the selected
provider, adapter version, model, account alias, effective effort, field origins
and configuration revision for later persistence. They do not read authentication
files or include the path of any provider's home directory. The configuration reload API loads and
validates a complete replacement before accepting it; rejection returns an error
and preserves the last valid configuration and any previously resolved selection.
The caller serializes reload and records the error. A watching session reads
the configuration again at every pull, so a run it starts pins its pair from
the configuration that pull read, and nothing a later pull or reload reads
changes a pair a run has already pinned.

#### What a developer run does with its slot's pair

Only a slot with a `routing` pair is affected. In a project that names one for
any slot, every run records the slot it occupies; a run in a slot without a
pair records nothing more and runs exactly as it would in a project with none.

- **The pair is pinned when the run starts.** Before the run claims its work
  item, its record takes the slot, both endpoints with the configuration key
  each field came from, whether fallback is on, and the configuration's
  revision. The run starts on the primary. A repair, a reissue after a wait, a
  restart, and a later configuration change all read the pair off that record,
  so editing the slot's pair or its label rules reaches new runs and never one
  already going. A run the scheduler pulls into the slot is refused before it
  claims anything if the primary's provider is not installed or not logged in,
  as a run with no pair is for the developer's.
- **Every invocation is an attempt under a logical operation.** The first
  attempt at the item is one operation, and each repair answering a recorded
  failure is another. A reissue after a wait, a relaunch after a dropped
  connection, and a restart stay in the same operation. Each provider launch is
  a separately recorded attempt, written into the run's record before the
  provider can begin work, so a harness that dies at any point leaves a record
  it can recover from without starting a second provider beside the first.
  After a restart the harness first settles the last attempt: one that never
  began is launched again under its own identity, one that stopped without a
  result is recorded as interrupted, and one that may still be running is
  waited for, for up to two minutes, after which the run stops with the reason,
  its work kept, rather than launching beside it.
- **One switch per operation, for a usage limit only.** When the provider
  refuses the primary for its usage limit, the operation moves to the alternate
  at once, without waiting the limit out, provided `enabled` is true. Where the
  primary is already known to have reached its limit — a refusal recorded
  against that account and model that has not reset and that no served
  invocation has lifted since — the alternate serves the operation's first
  attempt, and no attempt is recorded on the primary. A login nobody has
  renewed, a provider nobody can reach, an overloaded server, a dropped
  connection, a failing check, and a reviewer's finding are answered exactly as
  on a run with no pair, and never move an operation. A same-provider alternate
  on another model switches the same way.
- **The alternate is checked before it is used.** It must still be a provider
  allowed to serve the developer, on an account the configuration declares,
  installed, logged in, and not itself known to be limited. One that fails a
  check leaves the operation on its primary, waiting the limit out as a run
  with no pair does, with the reason recorded on the operation; the switch is
  not spent, and the alternate is checked again before the next attempt.
- **Once moved, the operation stays.** Every reissue, wait, and restart of that
  operation stays on the alternate, and an alternate that is limited too is
  waited out where it is. The next operation — the next repair — starts on the
  primary again with a switch of its own. A switch spends no repair attempt and
  no relaunch, and resets neither; the usage-limit wait budget and the
  relaunch budget cover the operation across both endpoints.
- **A new endpoint starts a new session.** An attempt on the same provider,
  account and model as the run's last one resumes its session. An attempt on
  any other endpoint — the other provider, another account, or another model of
  the same provider — is given a new session and told what the run recorded:
  the run and its branch, the files the change touched, the developer's last
  summary, and the work item's context; the failure a repair answers is in its
  prompt as always. The change itself is in the worktree, committed on the
  run's branch. No session identifier or credential passes from one endpoint
  to another.

The run's record holds the pinned pair, each operation, each attempt's
endpoint, whether its process was confirmed stopped, how it ended, and which of
the run's logged events carry the usage its provider reported; the switch records what
triggered it and the attempt it came from. The model the provider reports
actually serving is in the run's event log and spend log as before. What
`yoyo status` and the dashboard say about a run's endpoint and any switch is
separate work.

#### What a review does with the reviewer's pair

When `agents.<reviewer>.failover` is enabled, reviews use the reviewer agent's
own endpoint pair: the agent's own provider, account and model first, and the
`failover` endpoint as the alternate. The developer's slot, labels and account
never choose it, and the review runs under the account of the endpoint it is
made on. A review of a run's change, and a review of a merge queue candidate,
each move to the alternate once, and only when the provider refuses the review
for a usage limit or the first endpoint is already known to be limited. The
alternate is used only if it can serve the reviewer with read-only access, is
installed and logged in, and is not itself known to be limited. Otherwise the
review waits on the first endpoint, and the reason is recorded. Once moved, the
review stays on the alternate for its retries and waits. The alternate starts a
session of its own and is never given the developer's session. A reply that is
cut off, late, or for another change is not a verdict. The run's record shows
the pair, each attempt's endpoint and how it ended; for a merge queue candidate,
the candidate's record and its review show the endpoint that served it. A
project without `failover` enabled on the reviewer is reviewed as before.

These new keys require a compatible build: `execution.developer_slots[].number`,
`execution.developer_slots[].routing` and its endpoint fields, and
`agents.*.failover.effort`. Restart running parts on that build before adding them
to a project's file. `yoyo config validate` and `yoyo doctor` name running parts
whose recorded schema cannot read the keys.

### A developer model chosen by the item's label

The slot preference above says which work a seat pulls first. This says what
that work costs to do. **Model spend follows the work rather than the role**: a
documentation item and a change to the scheduler are both developer runs, and
only one of them needs the developer's own model. For slots without an explicit
[endpoint pair](#developer-slot-endpoints), `execution.developer_models`
is how a project says so — the tracker's own labels, the ones
[a slot prefers](#a-developer-slot-that-prefers-a-label), mapped to the model a
run over such an item asks for:

```yaml
execution:
  developer_models:
    - label: docs
      model: sonnet
    - label: config
      model: sonnet
    - label: tests
      model: sonnet
```

With that block, a run over an item labelled `docs` asks for `sonnet`, and an
item carrying none of the three labels asks for the developer agent's own
`model` exactly as every run did before the mapping existed. It is the
operator's direction of 2026-09-19, taken off a seven-day reading in which
developer runs on Opus were 64% of $1,431: the largest spend line is developer
runs that do not all need the developer's model, and the label already says
which do. The block is the operator's to paste into the project's own
configuration by hand, because `.yoyodyne/` is a
[protected path](artifacts.md#protected-paths-in-a-developers-change) no run may write, so
a project whose file does not yet carry it runs every item on the developer's
configured model.

**One label per entry, and the order is the answer.** An item can carry two
labels the mapping names, and what it takes is **the first entry in the
mapping's own order** — the file's order, not the item's — so moving an entry
up the list is how a project says which of two labels wins. That is why an
entry names one label rather than a list: an entry preferring several would
make "the first match" a question about which of *that entry's* labels matched
first, which the file does not answer.

**It is read once, when the run starts, and written onto the run.** The labels
it is read against are the ones the item carried when it was pulled, which the
run already records; the model it chose and **why it chose that one** are
recorded beside them. Every developer invocation the run goes on to make — the
first attempt, each repair, and anything a later process resumes — reads the
model back off that record rather than resolving the mapping again, for the
reason [the account](agents.md#pooling-work-across-several-accounts) is read back: a run
that resolved it per invocation would move mid-flight the first time the file
was edited under it. The reason is recorded for an unmapped item too, because
an item nothing mapped and a mapping nobody read are two accounts of one model
and only the record tells them apart.

`yoyo status` names the model each running run is on as it always did, and the
[cost log](agents.md#provider-accounts) records it per invocation, so what a kind of work
costs is read off the same surfaces as before — a mapped run simply says
`sonnet` where it used to say `opus`.

**The reviewer's model is not reachable from here.** There is no key in this
block that could name it, deliberately: which tools a reviewer may use is a safety
property rather than a spend decision, and an independent verdict bought more
cheaply is the one saving that costs the gate its meaning. The
[account pool and the failover rules](recovery.md#serving-a-turn-from-a-permitted-alternate-model)
apply to a mapped run unchanged, and so does everything else — a run on a
mapped model is claimed, developed, checked, reviewed, and promoted exactly as
any run is, under the same contract and the same authority table. Configuration
selects the model and never widens what a role may do.

**What the file refuses.** A label the tracker would not carry — anything but
one identifier-shaped word, the same rule a slot's preference is held to. A
`model` that cannot name a model, held to the rule every other configured
selector is. And a label mapped twice, because the first match in the order is
what an item takes, so a second entry for one label is a mapping the operator
believes is active and that nothing will ever reach. All three are refused when
the configuration loads, before anything is claimed.

### Saying why ready work has not started

```yaml
execution:
  scheduling_wait_problem_after: 1h   # the default
```

Every pass keeps, on each ready item it did not start, on each item the tracker
does not offer because it waits on other work, and on each decided repair
or re-run it left unstarted, the reason it gave, as one value in the item's
tracker metadata that a changed reason replaces and the run starting the item
clears. `yoyo status` and the dashboard read it back
([operations](../operations.md#reading-why-the-scheduler-has-not-started-an-item)).
`scheduling_wait_problem_after` is how long one reason may stand unchanged
before the wait is shown as a problem, with whose move ends it; a wait on other
work is never shown as a problem for its age. It must not be
negative; zero takes the default. It changes no priority, reservation, budget,
or scheduling rule: it says when a wait is reported, and nothing else.

### Watching instead of draining

`yoyo work` returns when nothing more is ready. `yoyo work --watch` does not: it
waits out an interval and reads the queue again, until you stop it.

```yaml
execution:
  work_poll: 60s                       # the default
  blocked_runs_before_intake_hold: 3   # the default
  brake_cooldown: 30m                  # the default
  brake_escalation_cycles: 4           # the default: two hours at that cooldown
  brake_probe_overdue_after: 45m       # the default
  redeploy_drain_limit: 15m            # the default
  build_move_interval: 3h              # the default
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
[recurring task](agents.md#recurring-tasks) that has come due. Each of those is a turn and
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
session restart or an edit to the item. The harness's own short-lived files in the tracker's directory, its write locks
and the tickets its tracker calls hold under `.beads/yoyodyne-writes/`, are never counted as
uncommitted changes. Every way the harness takes a run up again asks about the
checkout before the run takes a developer slot: a run waiting for its pause to
end, such as a wait for GitHub's checks, and a stopped run the harness
continues or resumes, or puts back to bring its change up to date or to answer
feedback on its pull request. A refusal leaves the run as it was, holding no
slot, and it is tried again at the first poll or sweep after the checkout is
clean. Other refusals carry the condition and
the beginning of the cause separately, so bounding a long cause does not spend
the detail limit repeating the condition.

`blocked_runs_before_intake_hold` is the failure-storm brake, a different thing
from that cooldown: it is aimed at a broken machine rather than a broken item.
That many runs blocking one after another, with nothing landing between them,
holds intake — the same hold you would place — and the same poll summons the
development manager's [sweep](agents.md#recurring-tasks) out of its cadence, with the
runs that blocked and the reason each blocked in the message that wakes her.
Any run that lands clears the count, and `0` turns the brake off, leaving you as
the only thing that holds intake. Only verdicts and check failures on a change
that was present count: a stop the environment made — a dirty checkout, a
tracker or a forge that did not answer, a sandbox that would not spawn, a
target branch that has
[diverged from the forge](../operations.md#unwedging-a-target-branch-that-diverged-from-the-forge)
so the harness will not catch it up — is a verdict on nothing and counts
toward nothing, and neither does a dispatch or a run the provider turned away
because nobody is logged into it or nobody can reach it, nor any run whose
record names a stop cause outside the work — a move to a new build, a login the
provider refused, the provider ending a session after its retries, the forge's
retries running out, a merge the merge queue held for a reason not about the
change, a direct merge the forge refused because the target branch's protection
requires the forge's own merge queue (recorded as `forge-queue-required`, naming
that setting), a check stopped at its time limit or the check stage stopped at
its bound once the harness's reruns are spent, a change no configured check
could run on, an approved change that lost the race to merge as many times as a
run may (`lost-races`), the reviewer's session running out of its time before a
verdict (`review-budget`), and failing tests the change did not touch that also
fail on the base commit it was built on (`base-failing`) — or a stop you or the
development manager asked for. Those are the only stops it leaves out. A replay
that stopped on the change past `execution.integration_retries_before_reconciliation`
(`integration-budget`) and the developer's session running out of its time
(`provider-budget`) still count. A run the harness's own step failed around,
such as a state it could not save, still counts; so does a run whose process died, though the
runs one claim audit finds dead together, such as every run a machine restart
ended, count as one; so do a cancellation that records nobody as having asked
for it, any other failure to publish to the forge, and a stop cause the running
build does not recognise, which the session's log also names each time it meets
one. A paused run never
counts, even when the note saying so on its work item could not be written:
the session says the note failed instead. That is
[a wait](../operations.md#waiting-out-a-provider-nobody-can-reach) no run can end,
and a brake tripped on it prescribes a decision about a change nobody judged —
which is what happened on 2026-09-17 over an expired login, again on
2026-09-19 when two of the three stops that tripped it came from outside the
work, and
again on 2026-09-21 when all three were one diverged target.

**The brake's hold does not wait on you while the harness is still working
it.** She decides what happens to it — to
release it, to keep it and probe the line, or to escalate it to the Lead
Product Manager, which you are told of — and the
watching session acts on the decision at its next poll. `brake_cooldown` is how
long the brake waits for that decision before it decides on evidence instead:
once it has passed with nothing recorded, the session starts one probe run under
the hold, and the probe landing reopens intake while the probe blocking keeps it
held, restarts the cooldown, and summons her again with the probe's own
stoppage beside the three. Where she has decided a repair of the run the last
probe ran, or a re-run of its item, the hold's next start goes to that
recovery, ahead of every other decision and every ready item, and the recovery
counts as the probe: its landing reopens intake and its blocking keeps the
hold. Every other decided recovery waits for the hold to lift, as fresh work
does. A probe stopped from outside the work is neither a
landing nor a blocking: it spends no cycle and summons nobody, and the next
probe still waits the cooldown, as it does when the session that started a
probe went away holding it — so probes never start back to back, however often
such a stop recurs. So a
broken machine is probed once per cooldown and
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
escalates the hold to the Lead Product Manager itself: the cycle that reaches
it is not put to her again, no further probe starts, and you are sent
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

**And she is put the hold on every pass while it stands.** Every pass of the
development manager's, scheduled or summoned, carries the hold beside her
docket as a decision to make again: how long it has stood, how many developer
slots are idle under it, how long any probe run has been out, and what she
decided last, which never takes it off a later pass. She releases it or says
in her account what keeps it held. `brake_probe_overdue_after` is how long a
probe run may be out before it is put back on her list of stopped runs, naming
the run and how long it has been out, because a probe that neither lands nor
blocks leaves the line held with nobody asked about it. The default is 45
minutes; zero never puts a probe back.

So a brake hold never waits on you. Once it is escalated, by her or by the
harness at that bound, it is the Lead Product Manager's to decide — the
ownership registry's rule for the hold, under the operating rule that the brake
summons the development manager and never the operator — and you are told: the
hold is sent to you as escalated once, directly and tagged by member id, the
moment the first escalation is recorded — one message for the hold whichever of
the two escalated it, and none more if the other follows. It is not
[a finding for you](../operations.md#where-a-finding-that-needs-your-hand-goes),
and the hourly line that carries it afterwards stays in the channel.
The trip itself is sent to you directly too, once and tagged, the moment it is
recorded, naming the runs it counted and `yoyo release`: it is not yours to
move while the harness is working it, and the message says whose it is, but it
is the one hold you did not place. `yoyo release` and the conversation's
`/release` still lift any of them sooner.

The hold records which of you placed it, and everything that reports one says
so: "the harness's own brake placed it after 3 run(s) blocked in a row with
nothing landing between them, which is the configured brake at 3" rather than a
hold attributed to you — and, for the brake's, what is deciding it and when the
probe starts if nobody does. It matters because what you do about a stopped
line depends entirely on which of the two stopped it, and a brake that trips
over a hold you already placed leaves yours standing, still yours, and summons
nobody over it. The brake's hold also names the runs it counted — each with its
item and what stopped it — on the channel's message and on `yoyo status`'s
fourth line, under whoever the hold is waiting on, and a release is said once,
naming who lifted it.

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

**Beyond the three: a watching session takes up a build deployed over it.** It moves at most once every `execution.build_move_interval`, three hours unless the project sets otherwise: a build installed sooner than that after the last move waits, and the session goes on working on the build it has. Once the time is up it moves onto the newest build installed at that moment, so the builds that landed in between are never moved onto one by one. Main lands about once an hour, and moving on every landing held new work back for most of an hour each night. The time is counted from the last move however it was made, a restart by the supervisor included, and a session that has not begun a move by the drain limit and five more minutes after the supervisor first finds the move overdue while the machine is awake (twenty minutes by default, so a machine that slept through the due time never has a session stopped at its first look on waking) is moved by the supervisor ([moving onto a deployed build](../operations.md#starting-the-product-and-stopping-it)).
When that time is up and a newer `yoyo` is installed — you rebuild it, you
install it — the session drains: it restarts into what you deployed the moment it hosts no run
and no recurring pass is taking its turns — past the bound, a run it has
stopped is not one it hosts — and until then it goes on polling, pulling into
free seats, and, while it still hosts a run, firing its recurring tasks,
because the drain is about the runs it hosts and not about the scheduler's
other duties. Once it hosts no run it starts no new pass, since a pass begun
then would only hold the restart; the session that comes back takes it at its
first pull. A pass already taking its turns is waited out like a run until the
bound below; when the session restarts past the bound, every pass still taking
its turns is stopped, recorded as stopped for a restart rather than failed, and
tried again at the first pull of the session that comes back. A deploy is the whole of
the instruction; what is configured is only how often a move may happen and how
long the wait may last:

```yaml
execution:
  build_move_interval: 3h              # the default; at least 1h, at most 24h
  redeploy_drain_limit: 15m            # the default
```

The least time between moves is counted from the last move the watch log
records, so a session that came back from a move waits from that move, and a
product whose log records none moves at once.

Past that bound the session restarts anyway. While it waits past the bound for
a check that is still running — a run's, below, or a merge queue candidate's —
the other runs it hosts are left working rather than stopped, because the
restart cannot come before that check ends and a run stopped then would only
sit idle in its seat; they are stopped together the first time no check is left
to wait for, and the restart follows within seconds. Each run it still hosts at its
developer attempt, its checks, or its review is stopped where it is and preserved whole:
worktree, branch, claim, developer session, and every counter. The session that
comes back re-adopts each one at its first pull, ahead of anything new, and
continues it from the phase it was stopped at — the session that stopped it
never does, however long it goes on pulling. A run at its promotion is waited
out past the bound rather than interrupted, because it holds the target
branch's lease, for at most thirty minutes past the session's limit for winding
down; past that the supervisor restarts the session onto the new build whatever
it is still waiting out, and reconciliation against the remote settles a
promotion cut off part-way. While one is, or while a document landing run is, the restart
cannot come sooner, so the session goes on filling free developer slots and
leaves each run at its developer attempt going, and stops those once nothing of
the kind is left. A document landing run is waited out at any step, and past the
bound no new one starts: a document a recurring pass confirms then waits for
the session that comes back to land it. A run with a check running is left to
finish that check — up to
[`check_timeout`](#what-a-whole-check-stage-may-cost) as configured, or the
shorter limit the check was given, however far the machine's load has scaled
it or `check_stage_timeout`, or ten minutes past the bound, whichever comes
first — and stops before its next check, so a check that ends inside that wait
is not started over on the new build; a check still running ten minutes past
the bound is stopped and runs again on the new build. The exception is a
developer slot sitting empty meanwhile: a free one while ready work waits, or
one still held by a run stopped for the restart, with nothing else holding the
restart back. Then the run is stopped part-way through its check, and a merge
queue candidate's checks likewise, and the session restarts at its next look,
so the new build fills that slot and runs the check again; see
[Operations](../operations.md#a-session-draining-to-restart-into-a-deployed-build). A run waiting for a heavy check's place or
between checks is stopped at the bound. Every check that
passed before the stop keeps its result, so the session that comes back runs
only the checks that had not finished — and only when the change is exactly the
one they passed on; a changed change runs every check again. The work item's
notes say which checks kept their results and which ran again. A stage that has already
finished gets up to a minute's grace to record its verdict and move the run
on to its review or a repair, where the next look stops it. A run that has
already landed and is running its
[landing checks](#where-the-whole-suite-runs) is stopped too: nothing about the
run is at stake by then, and the landing is recorded as unverified and files
nothing. The bound is minutes rather than hours on purpose: on 2026-09-19 a
session waited two hours on one run's race suite under load, with its second
seat empty and two recurring passes missed — a wait the restart drain limit
caps at fifteen minutes by default, independently of load, apart from the one
check a run is on, which is waited for no longer than `check_timeout` as
configured or ten minutes past the bound, whichever comes first. What a run the bound stops loses is the
minutes its current phase had spent, and a developer attempt and a review
resume where they were. Set it longer if that trade is wrong for your project, and never to
nothing — a drain with no bound is the wait this exists to end, and the
configuration refuses it. [Operations](../operations.md#a-session-draining-to-restart-into-a-deployed-build)
says what a drain does and does not stop.

What deploys cost is one restart per `build_move_interval` at most: every build
installed within that time of the last move is taken up by the same restart,
onto the newest of them. The queue is re-read from scratch on the way back in
exactly as it is at every poll.

The bounds cross the restart reduced to what is left of them — `--budget` less
what the session has spent, `--limit` less what it has started — because a bound
carried whole would start again at every restart. A session that has reached
either one stops on the bound instead of restarting: you set that number, and
taking up a build is not you raising it. There is nothing here for a drain, which
is a command you are waiting on the return of.

**`--budget <usd>`** caps what one session spends, and everything it spends
counts against it: the runs it starts, priced from the same recorded run evidence
`yoyo cost` prices items from; the turns it takes delivering stopped work; and
the turns a [recurring task](agents.md#recurring-tasks) takes when its cadence comes due.
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
### Pull requests waiting for a person to merge

Where your merge setting leaves each pull request for a person to merge, every
approved change the harness publishes waits on that person. A harness that
went on starting new work would only pile up more of them, each built on a
target that has moved further from the rest, so there is a limit:

```yaml
execution:
  max_waiting_pull_requests: 5   # the default; 0 is no limit
```

With that many pull requests waiting, no new developer work is started. What
is already under way carries on: repair rounds the development manager
decided, reviews, runs paused on other work, documents being landed, and the
bringing up to date of the waiting pull requests themselves. The status line,
the dashboard, the channel's hourly message, and the Lead Product Manager's
sweep all open with the same sentence, so the quiet never reads as a stall:

```
Not starting new work because 5 pull requests are waiting for a person to merge (the limit is 5): #41 for (P1) Retry the merge (ex-7); ... New work starts again by itself once one of them is merged or closed.
```

Nothing needs releasing. The count is read again at every pull, so once a
check of the forge finds one of them merged or closed, the next pull starts new
work. Setting the limit is the development manager's decision, like the other
numeric limits here. A drain (`yoyo work` without `--watch`) stops when it
meets the limit, and says so.

### Waiting for the team's branch

```yaml
execution:
  upstream_branch: main   # unset by default
```

`execution.upstream_branch` names the team's branch on the same remote as the
target branch. Unset, which is the default, keeps today's behavior exactly: an
item whose change lands on the target branch is closed. With it set, a landed
item stays open, "waiting for the team to merge it into main", until a fetch of
that branch finds the item's landing commit there. That closes it with the
commit recorded. The item is never pulled again while it waits, and an item
that depends on it and lands on the same target branch may start. The fetch is
made by the forge check, every `execution.forge_check_interval`, which runs
for a project that sets this key even if it publishes nothing. `yoyo status`
and the dashboard list each waiting item with the branch it landed on and the
branch it waits to be merged into. Setting it is the development manager's
decision.

An outside tool that opens its own pull request into the team's branch for a
batch of waiting items tells the harness with
`yoyo pull-request track <number> --item <id>...`, and takes it back with
`yoyo pull-request untrack <number>`. The harness reads the pull request first
and refuses, in plain words, one that goes into another branch, one closed
without being merged, an item that is not waiting for the team's merge on this
target branch, and an item whose landing commit is not among the pull request's
commits, unless `--unverified` is given for a batch built from new commits, such
as a squash. An item is carried by one pull request at a time, so tracking it on
a second moves it. Each forge check reads every tracked pull request. One merged
closes every item it carries with the merge commit recorded, so a squash merge
closes them too. One closed without merging gives each item back to the Lead
Product Manager, parked, with a note naming who closed it and when; the change
stays on the target branch. While one is open, a waiting item whose landing
commit appears among its commits is added to it, with a note. What people say
on an open tracked pull request is read the way feedback on the harness's own
pull requests is, leaving out what this product's harness wrote, but it never
starts a run by itself: a review that requests changes, or a comment on a line,
goes on the development manager's list naming the items whose landed changes
touched that file (every item, for a review that names no file); an ordinary
comment goes on her list naming no item; and an approval is noted on each item as
information only. Where the feedback calls for a change, the development
manager records a repair of one item from that entry, naming the pull request,
the feedback and the item. It is refused, with its reason, once the item has
closed, when the entry already has a decision, when the item is not waiting for
the team's merge on that pull request, and when the item has no attempts left.
Otherwise it spends one of the item's attempts, and the next forge check starts
a new run of the item from the target branch, with the feedback given to the
developer as evidence on the item's notes. The run goes through the configured
checks and an independent review and lands on the target branch; the earlier
run stays recorded as integrated, the item stays waiting for the team's merge,
and the tracked pull request picks up the new landing commit. The team's merge
of the earlier change does not close the item while the repair's landing is
still waiting. A repair of an item waiting on a pull request the harness opened
is not built yet.
`yoyo status` and the dashboard say which pull request carries each waiting
item, or that none does yet.

The target branch is also brought up to date with the team's branch. On each
forge check the harness reads the team's branch and, where the target does not
carry its tip, builds a merge of the team's branch into the target in a checkout
of its own, runs the configured checks on exactly that commit, has an
independent reviewer read it, and lands it the way the project lands any
change: it moves the local target under its promotion turn, or, where the
project publishes, opens a pull request and has the forge merge it with a merge
commit. Where integration needs a person (`approvals.integration: human`), the
pull request waits for them, and a project that does not publish leaves the
checked and reviewed merge for a person to land, since it has no pull request to
carry their approval. Immediately before it lands, under the promotion turn, it
builds the merge again and lands only a commit whose whole content is what that
merge gives, and it lands nothing while the operator's pause or a landing hold
an outside tool placed on the target stands; the merge is kept, with its checks
and review, until the hold ends. It never rebases or forces the target, because
pull requests and other contributors point at its commits. The harness
does not resolve a conflict itself: the development manager is told once which
files conflict, which items on the target touched them, and what she can
decide, work keeps landing, and the update is tried again when either branch
moves. Checks that fail, or a reviewer who does not approve, are handled the
same way. The update belongs to no work item, so no item's repair budget is
charged.

**Deciding a conflict.** The development manager can record a decision on a
waiting conflict that names one item waiting for the team's merge on the
target, with her reason (the `team_conflict` action in her conversation). The
decision is recorded on the update itself and counted as a round; the harness
starts a run of that item at the next forge check, in a worktree cut from the
target's current head with the merge of the team's tip already begun and Git's
conflict markers in the files that conflict. That run's developer settles the
conflict and the harness commits the merge, with the target as its first parent
and the team's tip as its second. The run ends there and promotes nothing: the
update takes its merge and puts it through the same configured checks, the
same independent review, the same check on protected paths and the same
compare and swap as a merge it built itself. A merge carrying a run's
resolution is not compared with the tree the harness builds again, which
could never match a conflict; the paths that differ from both the target and the
team's tip, which are the ones the run wrote, are put through the ordinary check
on protected paths under the grants of the item the run was for, and a refusal
reaches the development manager's list naming them. A file that holds what the
team's tip holds is the team's own and is not refused. The independent reviewer
and the pull request are told that a developer resolved the conflicts, by which
run and in which files, and are asked to judge those resolutions closely. The
run records the development manager as the one who chose it, with her reason, the
conversation and the round, and it starts nothing while the operator's hold on
the work the harness chooses stands; the round is not spent in that case.

**Three rounds, kept beside the update.** The runs decided on for one update are
limited to three (the update's own record counts the rounds, and `yoyo status`
says which round a decided run is). They are counted against that limit only, and are
never charged to the item's repair attempts or its other budgets, because
another contributor's work caused the conflict. A round is counted when the
decision is recorded and given back if the conflict goes away before its run
starts; a round whose run started stays spent, whether or not it made a merge.
The count starts again from none once an update has landed. A fourth round is
refused, in words that say the three are spent; the conflict then stays on the
development manager's list for a person to settle, and the target branch keeps
taking new work. A conflict is decided once, an item that is not waiting for the
team's merge on the target is refused, and a target that moves before the merge
lands drops the merge and builds it again, which meets the conflict afresh and
needs a new decision. A project with no configured checks never
lands an update, because a merge no check ran on has earned nothing. `yoyo status`
and the dashboard show when the target was last brought up to date and
any update that is waiting.

The reasoning is in
[integrating to a local branch](../designs/integrating-to-a-local-branch.md).

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
`blocked_runs_before_intake_hold`, `max_waiting_pull_requests`, `brake_cooldown`,
`brake_escalation_cycles`, `redeploy_drain_limit`, and `build_move_interval` are re-read at every pull
too, so an interval you shorten, a brake you loosen, or a drain bound you
lengthen under a draining session takes effect at the next wait rather than at
the next restart, and a bound you tighten under a standing loop is heard at the
next probe.
### A setting a running build does not know

A change can add a setting and set it in `.yoyodyne/config.yaml` at once,
while the parts of the product still run the build from before it. Such a
part checks whether the repository holding the file has moved past the commit
its build was made from. If it has, the part treats each setting it does not
know as one added since, leaves it out, names it once in its log, and reads
the rest; `yoyo doctor` and `yoyo status` name the part, its build, and the
settings until it moves onto a build that reads them. A build at the
repository's own commit, or one that cannot place itself in the repository's
history, refuses a setting it does not know as a misspelling, as it always
has. So a misspelled setting is still refused by the current build, by every
check, and by `yoyo config validate`. A part on a build from before this
behaviour refuses the whole file until it moves onto a newer build.

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
## Running a work item against the workflow definition

The delivery loop's sequence is also written down as data, in the two built-in
workflow definitions this build ships — `delivery.yaml` for a project whose
integration the harness takes, and `delivery-human-approval.yaml` for one where a
person still approves it. **Every new run compiles the one its integration policy
binds and executes it beside the run**, which is the default and takes no
configuration at all. A project that wants its own sequence
[writes one and owns it whole](#the-definition-is-the-projects-to-own).

What "executes" means here is exact, and it is worth being plain about: the
definition resolves where the run goes next and records it. It does not perform
anything. What runs a work item is the same Go control flow it always was — the
run claims the item, invokes the developer, runs the checks, buys the verdict and
takes the promotion lease exactly as it always has — and the conversion that
moves the performing too is a later step with its own configuration.

A new run records a **workflow instance** beside its own record, standing on the
definition's first state, and every boundary the run crosses is put to the
definition: the state the run just performed, the outcome it produced, and the
transition the definition resolves from them. The doors the definition holds are
the registered delivery steps with their bodies replaced by nothing at all, so
delivery is untouched and what the instance costs is one small file write per
boundary.

### The definition is the project's to own

The built-in is the default and not the only option. A project that wants its own
sequence writes it beside its personas, under the same configuration directory,
named for the workflow it replaces:

```
.yoyodyne/workflows/delivery.yaml                  # automatic integration
.yoyodyne/workflows/delivery-human-approval.yaml   # a person still approves
```

Which of the two a run reads is the integration policy above, exactly as it is
for the built-ins: a project whose `approvals.integration` is `automatic` binds
`delivery`, and one where it is not binds `delivery-human-approval`. A project
that keeps neither file runs what this build ships, which is what every project
did before this existed and still takes no configuration at all.

**Nothing is merged between the two.** A project that writes one owns the whole
sequence from then on — the states, the transitions, the terminals — which is the
only arrangement where reading the file tells you what its runs execute. The
other side of that is the cost: a later Yoyodyne that improves the built-in does
not change a project that ejected a copy, so the copy is worth a header saying
where it came from. What it does do is say so. `yoyo config drift` compares each
copy with the built-in this build ships and lists, under `workflow`, every
transition the built-in routes that the copy does not route at all, as
*available*, with both sides; `yoyo config validate` and `yoyo doctor` say the
same in one line on standard error, naming each transition. A run of the copy
that produces such an outcome records a divergence, so offering it takes away
nothing the project chose. Every other difference — a transition the copy sends
somewhere else, a summary it words differently — is listed as *yours* and never
offered, because without a record of what the copy was made from nothing can
tell a project's edit from a later improvement. Nothing is adopted for you:
adopting one is an edit to the copy. Yoyodyne's own repository keeps one —
`.yoyodyne/workflows/delivery.yaml` here was copied from the built-in verbatim,
adopted so that this project runs the arrangement it ships, and a test holds the
two to one sequence: any difference fails it except a transition the built-in
has added and the copy has not yet adopted, which the test names.

**What a copy can change is the sequence and nothing else.** It selects among the
actions this build registered — `work-item.claim`, `candidate.develop`,
`candidate.check`, `candidate.review`, `candidate.integrate`, `run.complete` and
`run.clean-up` — and each action's authority is declared in Go, so no file can
make a run do anything the built-in could not. A state selecting an action
nothing registers, a transition to a destination that does not exist, an outcome
the step it selected never produces, a file answering to another workflow's name,
or a key the schema does not describe: each is refused, all of them are reported
together, and the file is refused whole.

The gate is not among the things a file can rearrange away. A definition that can
reach the promotion without a state that runs the checks and a state that buys an
independent verdict between the last write of the change and the promotion is
refused at compile, whatever order it puts its states in. Configuration selects
the sequence; it cannot make a guarantee optional.

**A copy that is wrong stops the run before it claims anything.** The refusal
names the file and the defect, and nothing falls back to the built-in — a run
that quietly executed a sequence nobody chose, under a name the project had
already used for something else, is the failure this location exists to prevent.
Refusing it costs nothing: no work item has been claimed, no worktree exists, and
no provider has been paid.

A run already in flight is treated differently, because by then the work is under
way and what is broken is only the watching. Such a run finishes exactly as it
would have and records a `workflow_divergence` naming the file, which is the same
thing it records when a definition is edited under an instance already running
it: an instance keeps the digest it pinned and is never migrated, so an edit — a
correct one included — stops the observation of the runs already going and
reaches the next one.

**The rollback is one key.** A project that wants the legacy path — the same
delivery with nothing observing it — writes:

```yaml
execution:
  declarative_delivery: false   # the rollback; the default is true
```

That is the whole of it. It reaches new runs only: everything already in flight
finishes on whatever it started on, in both directions, which is the section
below. `yoyo config show --effective` prints the value that applies and
`--origins` names the file it came from, so a rollback is something you can
confirm rather than assume.

Three fields on the run say what happened:

- `workflow_instance_id` names the instance, and a run carrying one is a run
  executing the definition. It is written when the run is created and never
  afterwards.
- `workflow_unobserved` is why a run has none although its project asked for
  one: the definition could not be built, or the instance could not be created.
  The run delivers as it would have and what is lost is the watching. Without
  it, such a run reads like a rolled-back one and is counted as one the
  definition agreed with.
- `workflow_divergence` is why the run stopped being observed: the definition
  sent it somewhere it did not go, refused an outcome it produced, could not be
  stepped at all, or had no outcome for the way the run ended. A run carrying one
  is a run to read before the definition is trusted with anything, which is still
  ahead of it.

  That last case is what keeps the record honest. A run can end by a route no
  definition expresses — a review that ended without a verdict and without the
  operator's stop, a `complete` that failed, a worktree that could not be cut
  before the first attempt — and none of those is observed, deliberately, because
  naming the nearest outcome would record the run ending somewhere it did not. So
  the instance is left standing where the two last agreed, and a run that reaches
  a terminal status with its instance still mid-graph records the gap itself as
  the divergence, naming the state it stopped in.

  That holds however the run reaches its terminal. A run its own process ends
  records it there; a run whose process died and is settled by `yoyo reconcile`
  has the same gap recorded by the sweep, in the same words, whether the
  settlement completes it, blocks it, or fails it. The completed case is the one
  worth naming: the work lands and the item is settled on it, so a run whose observation
  stopped halfway would otherwise read exactly like one that walked the
  definition to the end.

  **What is recorded is the gap, not the settlement.** A process that died can
  still have left its instance on a terminal — an ending the definition has an
  outcome for is stepped before the process stops writing — and such a run is
  settled carrying no divergence, on every one of those three settlements. That
  is the recorded baseline's blocked trace:
  `reconciliation-blocks-a-run-interrupted-while-developing` stops writing as the
  developer's attempt ends, the developer's ending sends its instance to
  `abandoned`, and the sweep blocks the run with nothing to record. An empty
  divergence there is the observation having finished rather than the sweep
  having missed it, which is why it is measured rather than left to be read off
  an absent field.

All three are on the run's summary, so `yoyo status <beads-id>` and its `--json`
carry them like every other fact about a run, and a divergence and an unobserved
run each get a line there. Neither is a reason a run ended: the run delivered
exactly as it would have, and what diverged or went unwatched is the observation.

Three divergences are already known and expected. The first two are
interrupted processes rather than anything about the work. A run interrupted while its
reviewer was being asked resumes at the checks rather than at the review, because
a resumed run re-earns the whole gate, and no definition has a transition from
the review back to the check; such a run records a divergence naming both. A
process killed inside integration is settled by the sweep as succeeded with its
instance still standing in `integrate`, and records the gap that leaves. (That
is a local promotion; a process killed while landing through a pull request is
settled on the forge's answer instead, as
[a protected target lands through its pull request](publishing.md#a-protected-target-lands-through-its-pull-request)
says.) Both are
left as divergences deliberately — the definition is missing a path the pipeline
takes, and an observation that quietly agreed with itself would be worth nothing.
The third is a replay conflict handed back to its developer, and only where a
project's own copy predates the transition for it: `integrate` answers
`reconciling`, which the built-in routes back to `develop`, so a run observed
through the built-in walks from the conflict to the developer and on without
diverging. A copy made before that transition existed does not route the
outcome, so a run executing it records the refusal as its divergence until the
copy adopts the line `reconciling: develop` under `integrate`; `yoyo config
drift` offers it, as the paragraph on copies above says. This repository's own
copy is one of those until a change carrying it lands.

**The default and the rollback both reach new runs only.** Whether a run is
observed is settled once, when the run is reserved, and read back off the run's
own record by every later process. So a run already in flight when you roll back
keeps being observed to its terminal, and a run started under a rollback you have
since undone carries on to its own terminal with no instance and nothing watching
it. There is no migration in either direction, which is the same rule an
in-flight instance is held to when the definition itself changes: it keeps
running the definition it was pinned to, or it stops being stepped and says so.

That is the price of the rollback, and it is worth stating plainly: writing
`declarative_delivery: false` does not stop the runs that are already going. It
decides what the next one does.
