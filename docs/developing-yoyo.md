# Working on yoyo itself

*For someone changing yoyo itself. Part of
[yoyo's documentation](../README.md#further-reading).*

`yoyo` is configured against its own repository, so a checkout of it is a
project like any other. From one, verify the tools, run every check, and open
the conversation:

```sh
claude auth status --json
bd where
make check
make build
./bin/yoyo config validate
./bin/yoyo chat
```

`make check` is `fmtcheck`, `test`, `race`, and `vet`, and it is the gate CI
runs. Those same four are what this project declares as its
[checks](configuration.md), and a run applies them one at a time before it will
let a change reach review or integration — so anything a run has to exercise has
to reach one of the four, and for content that is not Go that means `make test`.

`make race` takes the packages it covers as `RACE_PACKAGES`, and is the whole
module when nothing names them, which is what `make check` and a person's own
run want. A run's gate is meant to name them: the harness hands every check the
Go packages the change touches as `YOYODYNE_CHANGED_GO_PACKAGES`, and
`make race RACE_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"` in the check
list runs the race detector over those alone, with the whole suite run once per
landing from `landing_checks` instead. The unset-only default is what makes
that line safe to run outside the harness — the probe and the evidence a
developer records in its worktree, or your own hand — where the variable is
not set and the whole module is what should run. An explicitly empty
`RACE_PACKAGES` is the harness saying the change touches no Go package, and
the target says so and passes. The check
stage as a whole is bounded by `execution.check_stage_timeout`, scaled for
the machine's load;
[what a whole check stage may cost](configuration.md#what-a-whole-check-stage-may-cost)
is the arithmetic, and [where the whole suite runs](configuration.md#where-the-whole-suite-runs)
is the arrangement.

`make adoption` is the fifth thing and deliberately not one of them. It runs
[`scripts/walk-adoption.sh`](../scripts/walk-adoption.sh), which executes the
README's "Getting started" against a throwaway Python project — its own scratch
repository and state directory, removed when it exits — and asserts what each
step is documented to do. It is the gate on a change to the README's install or
getting-started sections, and it is a merge gate rather than a habit: the
`adoption` job in [`.github/workflows/ci.yml`](../.github/workflows/ci.yml)
installs `bd` — at [a pinned version this repository
owns](#the-tracker-version-ci-pins) — and runs this target on every pull
request. On a machine with no `bd` at all, the walk fetches that same pinned
release itself, from [the one home Beads has](https://github.com/gastownhall/beads)
into its scratch root, so a fresh machine and CI install the tracker from the
same place; a machine that has `bd` walks with the one it has.

The harness runs it too, for the changes that can break it, once
`.yoyodyne/config.yaml` names it under `path_checks`:

```yaml
path_checks:
  - command: make adoption
    paths: scripts/walk-adoption.paths
```

It stays out of
`check`, which every run applies to every change, because most changes touch
nothing the walk vouches for and the walk is a build and a scratch project
every time. Instead it is a [path check](configuration.md#checks-a-change-runs-only-when-it-touches-what-they-vouch-for):
[`scripts/walk-adoption.paths`](../scripts/walk-adoption.paths) is the one
place the paths it vouches for are declared — the README, the walk itself, the
Makefile and module files, and the program under `cmd` and `internal` less its
tests and test fixtures — and a run whose change touches any of them runs
`make adoption` after the four declared checks, as a fifth check whose result
the review is shown. A change touching none of them, a document elsewhere or a
test, does not pay for it, and a change that edits the list runs the walk
whatever the edited list says. With that entry in the configuration, this closes
the gap two changes fell through on 2026-09-29, pull requests 902 and 907: each changed what `yoyo status`
printed, passed every check the harness ran, and turned `main` red for
whoever merged next, because the only thing that ran the walk was the forge.
The harness's checks run outside any agent's sandbox, so the `bd` and the
network the walk may need are the machine's own, and it walks in about thirty
seconds.
It needs no provider unless you pass `WALK_PROVIDER=1`, and it names any claim
it could not exercise rather than passing over it. It does need a scratch root
outside every git repository, which `$TMPDIR` and `/tmp` are on an ordinary
machine: the throwaway project lives under that root, and `bd init` in a project
inside another repository discovers that repository's tracker remote and tries
to clone it over the network. A `TMPDIR` pointing into a repository — an agent
run's scratch directory under `.git/worktrees` is the case this came from — is
refused up front, naming the enclosing repository, rather than dying at the
tracker step with nothing in the log.
[`scripts/walk-adoption-output.md`](../scripts/walk-adoption-output.md) is the
last recorded walk, kept from the README split as a readable record of what the
walkthrough actually prints; CI is what re-runs it now, so the file is a
snapshot rather than the evidence anything rests on.

## What a checkout needs besides Go

**Node**, for the dashboard. The page is drawn by its own script, which a Go
test cannot run, so its only behavioural evidence is
`internal/dashboard/testdata/render.js` running that script under Node against
the fixtures and the result being held to the renders under
`internal/dashboard/testdata/renders`. A machine without Node produces none of
that, so `make test` **fails** there rather than skipping:

```text
--- FAIL: TestThePageRendersEverySectionInEveryState
    node is not on the PATH, so the page's script was never run and the renders
    under testdata/renders were never compared …
```

Install it — `brew install node` on macOS, your package manager elsewhere — and
`yoyo doctor` says so too, under `node`, before a check has to.

**An environment that deliberately has no Node declares it**, in
`YOYODYNE_NODE_UNAVAILABLE`, whose value says which environment that is; the
render test then skips, quoting the declaration, and the fixture-shape and route
tests hold as they always did. Nothing in the harness sets it, and nothing here
sets it today — the machine the checks run on has Node — so it is there for a
container or a sandbox somebody builds without one, and setting it is that
person saying so. Nothing else skips, because the whole point is that a machine
that simply never installed Node stops reading as a green run — which is what
`make test` printing the render test's own line, after the suite, is there to
make visible either way:

```text
--- PASS: TestThePageRendersEverySectionInEveryState (3.36s)
```

Go and Git are the rest of it, and `bd` for the tracker; `make check` and
`yoyo doctor` between them name anything missing.

## The files an AI session reads on its own

[`CLAUDE.md`](../CLAUDE.md) and [`AGENTS.md`](../AGENTS.md) are loaded by every
Claude Code and Codex session in this repository without being asked for,
developer runs included. They are the architect's: instructions to such a
session about how this repository is worked on — the checks, the tracker, where
scratch files go, what a background process must do. They state no product
intent. What the product is for and the standing rules every role works under
are in [the product home](product/README.md), which the files link, and a
change to one of those rules is proposed to the Lead Product Manager rather than
written into the session files.

The two are one file kept in two places, because each tool reads only its own
name. `make test` fails when they differ, and when either carries the section
`bd setup` writes between `BEADS INTEGRATION` or `BEADS CODEX SETUP` markers —
that section tells every session to use `bd` for all its tracking, which a
developer run cannot do, and nothing in `bd` can be told not to write it. So
running `bd setup claude` or `bd setup codex` here is followed by removing what
it wrote.

## The tracker version CI pins

The `adoption` job installs `bd` from a prebuilt upstream release at a pinned
version: `BD_VERSION` in
[`.github/workflows/ci.yml`](../.github/workflows/ci.yml), one line, used both
to build the download URL and to check that the binary which arrived is the one
that was asked for. Nothing in CI follows the upstream's release schedule, and
that is the whole point of the line: on 2026-09-05 an unpinned
`go install …@latest` moved and turned every pull request here red at a step
nobody had changed, where it read as a broken adoption walkthrough rather than
as somebody else's release.

The pin is this repository's, not the upstream's. There is nobody to ask and
nothing that moves it automatically: it moves when a person edits that line in
an ordinary pull request, reviewed like any other change, and it is allowed to
go stale — a pinned version that still behaves as the harness assumes is not a
version that needs bumping.

To bump it deliberately:

1. Edit `BD_VERSION` in the `adoption` job. It is the only place the version is
   written; the URL and the check on the installed binary both read it.
2. Install that version locally and run the checks that pin what the harness
   assumes of `bd`:

   ```sh
   go test ./internal/beads -run Conformance -count=1
   ```

   They skip where `bd` is not on `PATH`, so a green run on a machine without
   the tracker has verified nothing. `-count=1` because the test cache is keyed
   on this repository's inputs rather than on which `bd` answered, so a rerun
   after installing a different version can otherwise replay the old pass.
3. Open the change. CI runs those same checks against the pinned binary, before
   it walks the adoption path.

That third step is what a bad bump fails, and it fails naming the assumption the
new version broke rather than the walkthrough — the walkthrough never runs. A
version that is not there to download, an archive with no binary in it, or a
binary reporting some version other than the one asked for, fails earlier still,
at the install step, which names `BD_VERSION` and this section. The download is
tried five times first, so a release host or network that fails for a moment
does not turn the job red, and a download that still fails says it may have been
the network rather than the pin.

What cannot happen quietly is the pin turning back into a floating reference.
`make test` holds every workflow in this repository to installing tools at
versions it names — `@latest`, a branch tip, and the forge's moving release URL
are each reported by `internal/composition` — so replacing the version with
whatever is newest fails a declared check on the machine of whoever wrote it,
rather than on somebody else's release day.

## Before you change how a run works

[What the delivery pipeline actually guarantees](delivery-pipeline-baseline.md)
enumerates the paths a run can take and the boundaries it holds — the phases,
the pause cases, the shared repair budget and the counters beside it, the
reconciliation actions, and the terminal outcomes. It is the specification the
code never had, and it is kept true by golden traces that record what each path
actually produced, so a change in behavior shows up as a diff rather than as a
document nobody re-read.

## Where the build cache goes

The Go toolchain writes what it has compiled to `$GOCACHE`, which defaults to a
directory under your home. Every command above needs it before it compiles
anything, so an environment that does not grant writes there fails all four
checks at setup — `operation not permitted` on a path, and no mention of a cache
anywhere in the message — which reads as a broken toolchain rather than as a
directory nobody granted. An agent sandbox is exactly such an environment: a
writable worktree and `TMPDIR` do not grant a cache under the user's home.

The harness sets `GOCACHE` for every run it makes, at `.git/yoyodyne/go-build`
in the repository the run works in. It is outside the working tree so it is not
untracked content in anybody's checkout, and every
worktree of one repository shares it — so a run's own execution probe and the
checks the harness then applies to its change compile against one cache rather
than two. `internal/execution/gocache.go` names that path without creating it.
For Codex developers, the adapter creates and explicitly admits only that cache
and the assigned scratch directory through the confined filesystem writer,
on initial launch and native resume. It sets `GOCACHE` to the resolved path it
admits; it grants no write to the rest of `.git`. See
[the environment a check runs in](configuration/runs.md#the-environment-a-check-runs-in)
for the native sandbox regression and its execution requirements.

Nothing sets it for an environment the harness did not make, so redirect it
yourself in one:

```sh
export GOCACHE="${TMPDIR:-/tmp}/go-build"
```

Those grants are the provider's to give, and yoyo depends on two of them without
controlling either: a run writes its build cache inside `.git`, and a run reads
absolute paths outside its worktree — the primary checkout's tracker export
among them. A Claude Code release that tightened either would break runs with
nothing going red, so both are probed against the installed CLI by
`TestLocalConformanceARunWritesInsideItsRepositorysGitDirectory` and
`TestLocalConformanceARunReadsAnAbsolutePathOutsideItsWorktree` in
`internal/backend/claudecode`. Each spends a provider invocation, so like the
rest of that suite they are opt-in:

```sh
YOYODYNE_CLAUDE_CONFORMANCE=1 go test ./internal/backend/claudecode -run Conformance
```

Run them after upgrading the CLI. What the declared checks cover on their own is
the fixture the probes stand on — that the directory each aims at is really
outside the worktree the run works in, so a green probe means a grant rather
than a target the run held anyway.

`make build`, `make test`, `make race`, and `make vet` refuse before they spend
anything when the cache cannot be written, and the refusal names that redirect,
so it costs a message rather than a diagnosis. `GOTMPDIR` is deliberately left
alone: it defaults into `TMPDIR`, which every environment that runs these grants
already, and the Go command refuses a `GOTMPDIR` that does not exist — naming
one would add a way to fail rather than remove one.

## A test never bounds a wait in wall-clock time

A test here waits on a signal it controls — a lock the test releases, a channel
something under test sends on, a budget or a clock the test advances itself —
and never on a length of time it hopes is long enough. No `time.After` guarding
a channel read, no deadline on a loop polling for a state, no sleep that gives a
goroutine a chance to have done something.

The reason is the machine these run on. The checks are applied to more than one
change at a time — a minute-zero probe overlaps a repair round, concurrent
seats run their race suites together — so load is the ordinary case rather than
the exception, and under the race detector at a load average past twenty a
five-second bound on a shutdown, a ten-second bound on a loop reaching a state,
and a thirty-second bound on a queued lock have each been reached with the code
working. Every one of those failed a change that never touched the package, and
each cost a repair round or a triage round on it. A bound that fails on load
rather than on the change is not a gate.

What a bound bought was a failure instead of a hang when the code is wrong, and
that is bought already: `go test` fails the whole binary at its own `-timeout`
with a dump of every goroutine, which names what was waited on and where. So a
test that would have hung waits instead, and a wait that never ends is reported
by something that reads the stack rather than a clock.

That `-timeout` is the one wall-clock bound the rule cannot remove, because it
is what replaces every bound it does remove — so it is sized for the loaded
machine rather than left at Go's ten minutes, which is a figure this repository
reaches without hanging. `TEST_TIMEOUT` in the `Makefile` is the whole of it, at
twenty-eight minutes, set from what `internal/orchestrator` measured with a
second check stage running beside it: 1,439 seconds under `make race`, which
passed the twenty minutes the figure used to be, and up to 1,170 seconds under
`make test` ([the record](diagnoses/yoyodyne-ifd-429-69-orchestrator-test-limit.md)).
It stays under the thirty minutes of
[`execution.check_timeout`](configuration.md#how-long-a-check-may-take), which
is not scaled for load, so a hang is still reported by `go test` with its dump
of every goroutine before the harness ends the check. That makes twenty-eight
minutes the ceiling rather than a step: the figure is interim until the
package split (yoyodyne-ifd.429.14) lands, and a package that grows until it
needs more than this is a package to split, not a figure to raise again.

The shape that replaces a bound is one of three. Where the code under test
already says when it has got somewhere, wait on that: a claim returns its hold, a
process returns its result, and the test reads `<-done` with nothing beside it.
Where it does not say, give it a way to — a seam the harness never sets, that a
test fills with a channel or a step: the Slack sink's wait between passes, the
conversation store's word that a claim has queued, and the process runner's
total budget and its limit on how long a process may go without output are
each one of those, and each is a test driving
the thing it is about rather than polling at a millisecond and giving up at ten
seconds. And
where the claim is about promptness, read it off what happened rather than off
how long it took: a sink that was stopped before it started asked the workspace
nothing, and a descendant the group kill reached never wrote the marker it
would have written after its sleep. What remains wall-clock in those tests is
the code's own timer where it is the thing under test, which is not a bound the
test set and not one load can turn into a failure.

The same rule covers a test that launches a process and reads what it wrote,
and a suite in shell run from Go: the wait is for the process, and the working
directory is one the suite owns rather than a package directory beside a census
that will list what the shell leaves there.

One bound the rule does not reach is the harness's own on a local Git command.
It was a flat thirty seconds, and at a load average near forty that flat figure
killed `git worktree list` and `git status` in the middle of a suite that was
passing. So the figure is the idle machine's, and the manager scales it by how
far the one-minute load average exceeds the cores, per command and capped at
ten times (`internal/gitworktree`). That is the right figure for a run, and the
tests whose subject is it hold it there.

It is not a figure the other tests can run under. Under a full suite the scaled
budget has been reached by Git that was working — a replay killed at 66.8
seconds with the load at 36, a one-file checkout at 31 seconds near 50, and a
replay at the idle thirty seconds with the load at 11 to 13 on sixteen cores,
which the scaling reads as an idle machine — each in a test that passed in
seconds on its own. A lagging one-minute average does not measure what a
suite's own race binaries do to a Git command started beside them. And those
tests are about what Git and the harness do, never about how long Git took, so
the tests in `internal/gitworktree` and `internal/orchestrator` give every
manager they build a budget of their own, `testGitBudget`, ten minutes, and so
does a Git command a test runs beside one. The tracker's conformance checks in
`internal/beads` bound each `bd` command the same way, in
`conformanceTimeout`. Ten minutes catches a command that has hung, which any
figure does, and it sits under `TEST_TIMEOUT`, so the hang is reported as the
command it was. [The record](diagnoses/yoyodyne-ifd-429-11-git-budget-under-load.md)
has the reports this came from and the suite passing with the load between 34
and 54 on sixteen cores. What a suite is held to in total is still
[`execution.check_timeout`](configuration.md#how-long-a-check-may-take), which
is the operator's to set against the concurrency they run.

The rule was checked the way the failures arrived: `make race` ten times in a
row with a second `make race` looping beside it on the same tree, at one-minute
load averages from 13 to 59 on sixteen cores, twenty-one runs and no failure.
[The record](diagnoses/yoyodyne-ifd-389-race-beside-race.md) has the numbers,
and one thing worth knowing before repeating it: `make race` on an unchanged
tree is served from Go's test cache, so a repetition that is meant to execute
anything runs under `GOFLAGS=-count=1`.

The repetition was then run over `make check` itself — the whole gate rather
than its race half — and
[that record](diagnoses/yoyodyne-ifd-270-ten-checks-under-load.md) has those
numbers. `GOFLAGS=-count=1` covers it for the same reason: `make test` is
served from the cache exactly as `make race` is, so any repetition of the gate
that is meant to execute anything says so on the command line.

## What a surface may do with emphasis

This is the contract for anything that writes output an operator reads — a
command, a listing, the conversation, a run's closing lines, a message posted to
Slack, and whatever surface comes after them. It is recorded once, here, because
it was argued separately for the conversation, for the goals listing, and for the
reports listing, and three surfaces that agree by coincidence are not a
discipline. A new surface cites this section rather than deriving the rules
again.

The goal it serves is stated in [the v1 goals](product/goals/v1-goals.md#goals):
*the harness's surfaces read clearly: boundaries between topics and speakers are
visible, important findings stand out, and every distinction survives a terminal
that cannot render emphasis.* Every rule below is that sentence made checkable.

**A surface asks for a theme; it does not write an escape.**
`console.ThemeFor(out, os.Getenv)` is the whole of the question for a command,
and a conversation asks the console it opened. Both answer with a
`console.Theme` whose zero value dresses nothing, so a surface calls
`theme.Entry`, `theme.Detail`, `theme.State`, `theme.Severity`, `theme.Card`, or
`theme.Rule` unconditionally and the theme decides whether that is anything at
all. That is where a surface inherits the rest of this: no package outside
`internal/console` writes an escape into its output, and a surface reaching for
one has found something the theme should be taught rather than a licence to dress
itself.

**The words carry the meaning; the dressing only makes it findable.** Every
distinction the dressing draws is one the text already makes — a question ends in
a question mark, a group says `blocked (2)` in words, a goal that may no longer
be named is marked `[no longer active]`, a proposal says what it is proposing.
The test is mechanical rather than a matter of taste: strip every escape from the
output and it must say everything it said dressed. If stripping loses a
distinction, the distinction was never in the text and the layout is what has to
change.

**Where the words will not carry it, put a mark in them.** A report's severity is
`!!` for critical and `!` for warning, in the column before the identifier, so a
pile can be scanned down its margin and a critical report does not read like a
note on a terminal that may not be dressed at all. Reaching for a louder colour
instead is how a distinction ends up living only in the decoration.

**Emphasis is spent, not spread.** A note is dressed as nothing on purpose: a
listing where every line is coloured has no emphasis left for the line that
matters. The same reasoning is why structure is weighted rather than recoloured —
a heading, a bold run, and a listing's entries are the text's own emphasis, not a
kind of thing the harness is telling apart, so they leave the colours to mean
what they mean.

**The vocabulary is named, and the theme decides what it looks like.** A surface
names `console.StateBlocked` or `console.SeverityCritical`; it does not choose an
orange. That is what keeps blocked work the same colour in `/status`, in a run's
closing lines, and in a listing, and it is why the set is fixed rather than free
text.

**Permission is all or nothing, and it is asked rather than assumed.**
`NO_COLOR`, a `TERM` that says `dumb` or says nothing, and a stream that is not a
terminal each suppress every escape there is — the colour, the rules, the cards,
the bell, the window title — and with them anything that depends on there being a
moment at which something unprompted can be written. Somebody who asked for an
undecorated conversation asked for all of it, which is why `Theme.Permitted` is
one question rather than several.

**Machine-readable output carries none of it.** `--json` states a severity as a
field, and dressing it would be corrupting the field. The same goes for anything
else written for a program to read.

**A surface that is not a terminal holds the same contract in its own
materials.** Slack has no ANSI, so severity is said in words there — a `critical`
says "Critical" — and an ordinary fact carries no marker at all, which is the
words carrying the meaning and emphasis being spent rather than spread, in a
medium that renders emoji instead of escapes. A future dashboard inherits the
reasoning, not the escape codes.

What an operator is told they will see is written where they read it — [the
conversation on a terminal](conversation.md#what-the-conversation-looks-like-on-a-terminal),
[the goals listing](artifacts.md#goals-and-what-work-serves-them), [what a
report looks like](reporting.md#what-agents-report-and-where-it-reaches-you), and
[severity in Slack](slack/setup.md#what-it-posts). Those describe what
one surface does; this section is the rule they are each an instance of, and it
is the one a new surface has to satisfy.

## Where a surface reads the work's own state from

A surface that answers a question about the work reads the answer from the shared
read model in [`internal/readmodel`](../internal/readmodel/standing.go), and never
from a second reading of its own. That is the architect's standing ruling rather than a
per-surface preference, and it has two halves worth naming where somebody will
meet them:

- **Decision 5 of the Slack reporting design.** A question asked in a thread is
  answered by the harness from the read model, under the exception that design
  states; anything that needs a role's judgement instead routes as a turn into
  that role's durable conversation once the conversational client lands. So a
  surface may read to answer, and may not decide.
- **The `surfaces-project-one-read-model` invariant.** Every operator surface
  projects the one derivation, reimplements none of it, and keeps no state of its
  own. Two surfaces computing one answer differently is a disagreement only the
  operator can settle.

What that decided most recently: a Slack reply settling the pause on its own item
without naming it has to know which directives are holding that item, and it asks
`readmodel.Pausing` — the same reading the run pipeline enforces on. The sink's
own `Directives` interface stays the two write methods it was drawn as, because
widening the half that records directives into one that also reads them would put
a second authority over the record in the reporting process.

A surface acting on a reading says how far the thing it acted on reaches. A
directive recorded against no work item holds every item in the product, so a
thread that settles one has lifted a pause on far more than the item it is about,
and the acknowledgment says so — a reader has nothing else to tell them, because
the thread they are in is about one item.

## What `test` checks besides the code

Some of what `make test` runs is not about the Go code at all: it reads this
repository's own documents, it executes the part of the build that is shell, and
it holds every other kind of file the repository carries to something, so a
mechanical defect in any of them fails a check instead of costing a reviewer a
paragraph or waiting for the day it matters. Each one exists because a reviewer
wrote that paragraph, more than once, and because the thing being checked is one
nobody can verify by eye — a relative path resolves only against the directory
layout, a `#fragment` names a heading through a slug nothing writes down, and no
Go check has ever run a line of bash.

| What fails | Where it lives | What it means |
| --- | --- | --- |
| A link in any Markdown file here resolving to nothing — a path that is not in the repository, or a fragment naming a heading the target does not carry — or a fragment cited from Go, YAML, or shell source naming a heading the document it names does not carry | `internal/doclink` | Fix the link, or the heading it points at. Absolute URLs are not resolved: they are somebody else's to keep working, and reaching for one would put the network in a deterministic check. The exception is a URL naming this repository's own forge home, derived from go.mod's module path — the repository root carrying a fragment, which is a link into the README because that is where the forge renders it, and a file in the blob view. That spelling is not somebody else's and it is the one a document has to use when it is read outside a checkout: `.github/release-notes-preamble.md` points at `README.md#getting-started` that way, and it is appended to the notes of every release already published, which no change here can correct. Anything else under that home — a release, a pull request, an issue — is the forge's own furniture and stays unresolved. Source is read for citations because prose is not the only thing that names a heading: `docs/configuration.md#checks` is written into every `.yoyodyne/config.yaml` `yoyo init` has ever generated, on disks this repository cannot reach. A cited path is resolved in the three spellings one is written in — from the repository root, from the citing file's own directory, and inside a forge blob URL — so `../README.md#further-reading` in a script counts like `README.md#further-reading` in a Go string. A citation naming a document this repository does not have in any of the three is passed over rather than reported — a fixture written to be broken is a path too, and guessing would be worse than saying nothing — so a fixture in a test must name a document this repository has not got, which is the convention the fixtures in `internal/doclink` keep. |
| A report about this repository's own goals that names no place to open — a goals document that could not be read with no path beside it, a goal whose link upstream does not hold and that does not name the document it is written in, a wrapped goal with no file and line, an identity two active goals carry that does not say which documents state it, or a recorded brief the collected goals do not say where to find | `internal/goal` (`goal_test.go`) and `internal/cli` (`goals_repository_test.go`) | Carry the path, the artifact, or the line into the report. Nothing about what the documents themselves say fails here, which is [the decision below](#why-nothing-a-goals-document-says-reddens-this-build): a goal written across more than one physical line, a goal that has not said yet what it supports, a goal naming a brief claim the brief does not state, a goals document stating no goals, a repository with no active goal, a brief stating none, and a configured artifact home nobody has created are all reported by `yoyo goals list` and carried into `yoyo release`'s goals check instead. |
| A document under `docs/product`, `docs/designs`, or `docs/decisions`, or an invariant under `docs/decisions/invariants`, that the harness's own loader refuses to read — or a load of those homes, as the configuration names them, that reads no artifact of some kind this repository ships, or no active invariant | `internal/cli` (`governed_documents_repository_test.go`) | The loader and the documents it governs disagree, and the failure names the file. If a change to the loader or to what `Validate` accepts caused it, that change is what broke: every other artifact and invariant test reads a synthetic store, so this is the only check that sees the documents this repository actually ships. If an edit to the document caused it, fix the document. What the documents say about each other — a `supports` link that does not hold, a revision recorded under a role that does not own the document — is logged here and not failed, for [the same reason](#why-nothing-a-goals-document-says-reddens-this-build) as the goals rows, and `yoyo artifact list` reports it. |
| A coined term in a document under `docs/product`, `docs/designs`, or `docs/decisions`, or in a string literal of `internal/cli`, `internal/chat`, `internal/notify`, `internal/slack`, `internal/readmodel`, `internal/dashboard`, `internal/directive`, `internal/goal`, `internal/backend`, or `internal/config`, or in the dashboard's assets, that [the register](terms.md) does not define — or a compound word nothing accounts for anywhere a person or a role reads the harness's words — or a register entry with no definition, no place of use, or a second row for a term already listed, a replaced term's row still excusing a document that no longer carries it, or a compound listed as still to be decided that nothing writes any more | `internal/terms` | Write the ordinary word, or add the entry. The register decides and the check only reports: adding a row permits a term and removing it refuses the term again, neither of which is a change to any code; a replaced term's row may name the governed documents that still carry it while their owner amends them, and those are excused for that term and nothing else is. Frontmatter, fenced blocks, and struct tags are not read — a revision's recorded reason is what somebody decided in their own words, a fenced block is code, and a struct tag is a key. A term of more than one word is looked for however its parts are spaced, so a hyphen, a doubled space, or a line wrap between them does not get one past the check, and a row permits every spelling of its term rather than the one the row happens to write. A new compound is refused by [the rule below](#what-the-terms-check-counts-as-a-new-term); an ordinary word given a new sense has no shape a check can see, which is why the reviewer persona carries the same rule as a finding class. |
| The eight documents this repository carries to its own Lead Product Manager as [what the product ships](configuration.md#what-the-lead-product-manager-sees-besides-them-and-what-it-does-not) adding up to the ceiling on that set, one of them no longer being where the set names it, or the set no longer fitting the briefing beside the specifications and the bounded sections | `internal/contextbundle` (`product_test.go`) | Reaching the ceiling is a product decision rather than a documentation edit: the set is carried in full by the Lead Product Manager's decision, and the constant's comment says what carrying it costs at the ceiling and names the reduction (yoyodyne-ifd.117.4). It is the one row here that warns before it fails — `make test` prints the set's size after the suite, and a `WARNING:` line for the length of the margin under the ceiling, because `go test ./...` discards what a passing test says. A path that stops resolving is a document the Lead Product Manager silently stops being given, and a set that passes its ceiling and still does not fit the briefing is the specifications having outgrown the reserve, which the failure says. |
| A place the harness enforces role authority that [the authority inventory](authority-inventory.md) does not list, or a listed check whose declaration has moved or been renamed | `internal/authority` | Add the row, or correct the one that moved. The inventory is the statement of what the harness authorizes today and the ground truth the capability registry re-expresses, so an authorization site nothing lists is authority nobody wrote down. The document decides and the check only reports: adding a row lists a check and moving one to the second table excuses it, neither of which is a change to any code. What the sweep can recognize is a floor — a function that names a role and refuses, a name carrying `authoriz` or `authorit`, and the `protect`, `independen`, and `lease` boundaries — so a check outside all of those is still a reviewer's to catch. |
| A place that ends a developer run, a typed cause outside the work or stop class, or a bound's source value that [the run-stop inventory](run-stops.md) does not account for — or a listed site that moved, changed count, or disappeared | `internal/orchestrator` (`run_stops_inventory_test.go`) | Account for the new stop, its budget cost, and what remains of the change, or correct the stale row. The sweep counts ending calls and status writes per declaration, so another stop inside an already-listed function also fails; recognized syntax that does not end a run is listed separately with its reason. |
| A row of [the authority inventory](authority-inventory.md) that the role-capability registry neither expresses as a capability question nor names as a gap | `internal/rolecapability` | Answer the row: write the question a call site would ask instead of the role's name, or the reason there is not one yet. The registry is the inventory said once more in capabilities, so a check nobody re-expressed is one the conversion would silently drop — and a gap written down is the honest half of the claim, which is why an unanswered row fails and a gap does not. |
| A place in the Go sources that reads what became of a directive — `Resolved()`, or the `ResolvedAt` behind it — that the audit in `internal/directive` (`disposition_audit_test.go`) does not list, or a listed reader that has moved, gone, or changed how many times it reads | `internal/directive` | Add the row, or correct the one that moved, saying what the read means where it sits. `Resolved` is has-a-disposition and `InForce` is still-applies: a standing instruction carries an outcome the moment somebody records what came of it and goes on applying until the operator withdraws it, so a filter that asked `Resolved` to find out what still constrains work would retire that instruction as soon as its first item was admitted, silently. The audit is the list of every reader and what each one meant by reading it; the sweep is a floor rather than a fence, because it recognizes the call and the field by name, and a reader that reaches the same question another way is still a reviewer's to catch. |
| A place in the Go sources that refuses a record for carrying a field it does not know — a call to `DisallowUnknownFields`, or to the run store's `decodeStrictly` — that the audit in `internal/runstate` (`strictdecode_audit_test.go`) does not list, or a listed site that no longer refuses anything | `internal/runstate` | Decide which the new site is. A read that only says what a record holds — a listing, a dashboard panel, the Slack sink, a command that shows one record — goes through the tolerant door (`decodeTolerating`, or a store's `Read`) instead, because a strict one there stops reporting for as long as a record a newer build wrote stands. A read that precedes a write, or one whose refusal is a visible failure somebody is owed — an agent's reply held to its block, a gate that declines to proceed on a record it can only read part of — is listed with which it is and why. The sweep is a floor: strictness reached another way, such as a `Validate` refusing a value it has no word for, is still a reviewer's to catch. |
| A durable field that [the delivery-pipeline baseline](delivery-pipeline-baseline.md) states, that no recorded trace carries, and that its own not-covered section does not name | `internal/orchestrator` (`baseline_test.go`) | Record a trace that carries the field, or name it in the gap list. The document promises a parity harness that what it does not measure is written down, and that promise was broken three times running while every check was green, because nothing but a reader was holding it. The sweep is a floor: it recognizes field names, so an ordering or a refusal the document states in prose alone is still a reviewer's to catch. |
| A recorded delivery trace that no parity scenario walks, or a scenario whose transcript the trace does not evidence | `internal/orchestrator` (`parity_test.go`) | Write the transcript the trace evidences, name why no built-in definition can express that path, or fix the definition. The built-in workflow definitions are the delivery loop as data, and the only thing that makes them the pipeline rather than a plausible state machine is that every frozen path walks through them. A trace nothing walks is measured by nothing and looks exactly like one that passed. |
| This repository's own copy of the delivery definition, `.yoyodyne/workflows/delivery.yaml`, digesting to something other than the built-in it was copied from | `internal/orchestrator` (`definition_repository_test.go`) | Carry the change into both files, or, if the divergence is meant, change the test that holds them together. This project keeps its own definition like any other project does, so its runs execute the copy while the parity harness measures the built-in; editing one alone would leave the two measuring different sequences with nothing saying so. Comments are not compared — the digest is what an instance pins, and the copy carries a header of its own. |
| A governed document whose place in the chain is wrong — a `supports` entry naming nothing, an artifact reaching no brief, or a revision recorded by a role that does not own the document | `internal/cli` (`artifact_repository_test.go`) | The harness reports these and never refuses a document over one; here they fail, because a warning nobody is made to read is how one of them breaks unnoticed. |
| A claim in the release verb's own suite, [`scripts/cut-release-test.sh`](../scripts/cut-release-test.sh), that no longer holds | `internal/cli` (`release_repository_test.go`) | Read the claim it named and fix `scripts/cut-release.sh`. The verb is shell, so no other check here executes it, and its value is entirely in cuts it refuses — a refusal first exercised on the day it was needed is one nobody had. |
| A claim in the notes writer's own suite, [`scripts/release-notes-test.sh`](../scripts/release-notes-test.sh), that no longer holds | `internal/cli` (`release_repository_test.go`) | Read the claim it named and fix `scripts/release-notes.sh` or `scripts/release-body.sh`. The same argument as the row above, for the other half of the release path: what a release page publishes would otherwise first execute during a publication. |
| The release verb excusing from its clean-tree check a derived export that a run does not declare as churn the primary checkout may acquire | `internal/cli` (`release_repository_test.go`) | Either declare the path in `AllowedPrimaryChanges` as well, or take it back out of `derived_exports`. The containment is one-way on purpose: a run may come to tolerate a path the cut has no business leaving dirty under a tag, so widening the run's list alone is fine and widening the cut's alone is not. |
| A shell file a shell will not parse — every `.sh` here, the tools in `bin`, and the hooks the tracker installs | `internal/composition` | Fix the syntax. Parsing is `bash -n`, which reads a script and runs none of it, so it is safe to point at the release verb and the adoption walkthrough. It is the floor rather than the gate: shell with a suite gets executed as well. |
| A claim in the install script's own suite, [`scripts/install-test.sh`](../scripts/install-test.sh), that no longer holds — including the `dist` target and [`scripts/install.sh`](../scripts/install.sh) disagreeing about the platforms a release covers, the archive's name, what it holds, or how `checksums.txt` is written | `internal/composition` | Read the claim it named and fix `scripts/install.sh`, or the Makefile line it drifted from. The script is the README's first line and the one part of the install path that is not the binary, because it is the part that fetches the binary; the suite runs it against a fabricated release and a fabricated machine — `curl` and `uname` are stubs on `PATH`, the releases are files under a temporary root — so a platform with no release binary, a download that does not match the published checksums, a `PATH` the binary is not on, and a missing prerequisite are executed rather than asserted, with no network and no published release. The fixtures are packaged the way the installer unpacks, so they cannot disagree with it; the Makefile is the one place the two can drift, and the last block of the suite compares them directly. |
| A YAML or JSON file that does not decode | `internal/composition` | Fix the file. What each one means belongs to whatever reads it — Claude Code, Codex, the tracker, the harness — but one that nothing can parse is this repository's defect whoever owns the schema, and it is not a defect a reviewer reading a diff reliably sees. |
| A workflow that is not shaped like one — no trigger, no jobs, or a job with no runner or no steps | `internal/composition` | Fix the workflow. Decoding is not enough for these: the release workflow is triggered by a tag push, so what is wrong with it would otherwise first misbehave during a real publication. |
| A page the dashboard's script draws from the fixtures under `internal/dashboard/testdata/fixtures` differing from the render recorded under `internal/dashboard/testdata/renders`, a section of the page that reaches none of its four states in any scenario, or a fixture that is not the read model's own shape | `internal/dashboard` (`page_test.go`) | Look at the diff, and if the change to the page was meant, rerun with `-update-renders` and commit the renders with the change. The renders are the evidence a reviewer is handed for each section in each state, so they change when the page does and not otherwise. The script is run by `node`, which this check looks for on the `PATH` and fails without, naming it: a machine without Node compares no renders at all, so passing there would say nothing about the page. The one environment that skips instead is one declaring its own absence of Node in `YOYODYNE_NODE_UNAVAILABLE`, which nothing here sets — see [what a checkout needs besides Go](#what-a-checkout-needs-besides-go). Either way the fixtures' shape and the routes are held. The fixtures are decoded refusing unknown fields, so a field the read model stops carrying fails here rather than leaving the renders showing a page nothing can produce. |
| `CLAUDE.md` and `AGENTS.md` differing by a byte, either one carrying the section `bd setup` writes, or `CLAUDE.md`'s opening no longer saying it is the architect's instructions and linking the product home | `internal/composition` (`sessionfiles_test.go`) | Make the change in both files, or remove what `bd setup` wrote. See [the files an AI session reads on its own](#the-files-an-ai-session-reads-on-its-own). |
| A file no content class recognizes, a class that recognizes nothing, or a class crediting its coverage to a check the project no longer declares | `internal/composition` | Write the class, retire it, or say what covers it now. This is the audit rather than a gate: it holds what this repository is made of against what its declared checks actually exercise, so a new kind of content cannot arrive covered by nothing and unnoticed — which is how shell got here. |

Fixtures written to be malformed on purpose are not walked: anything under a
`testdata` directory is skipped, along with `.git`, `.dolt`, and `dist`.

### Why nothing a goals document says reddens this build

Two of the rows above used to fail on the contents of `docs/product`: a goal
written across more than one physical line, and a goal whose `*Supports: ...*`
trailer named a claim the brief does not state. Both are worth fixing and
neither is a defect in any code here. They are how somebody typed a document
they own, and either one can appear from an ordinary edit — rewording a claim in
the brief breaks every trailer that named the old wording, and a hard wrap is
what most editors do to a long sentence.

A build that goes red on that makes the harness the editor of the documents it
exists to serve, and it makes rewriting the document to suit the checker the
cheapest way to get back to green. That is the pressure the states beside them
were already tolerated to avoid, and holding the line halfway was the
inconsistency: the same finding was a note on a release assessment and a red
suite in every developer's `make test`.

So the checks moved to the surfaces that can afford to be right about them,
which are the ones that read a document in front of the person who owns it:

- `yoyo goals list` names each of them on stderr and carries them in `--json` —
  `goals not read`, `goal not linked to the brief`, `goal not written on one
  line`, and `goal identity stated twice`.
- `internal/conformance`'s goals check carries the same set into a release
  assessment, and grades them. A goals document nobody can read, and a
  repository with nothing to check an attribution against, are mismatches and
  refuse the cut. A broken link upstream, a wrapped goal, and an identity two
  active goals carry are notes beside it, because each goal is still stated and
  the work already naming one still resolves.
- `yoyo goals attribution` exits non-zero for a work item naming a goal no goals
  document states, and for one whose recorded goal was written over. That is the
  harm a wrapped or reworded goal actually causes, and it is judged against the
  tracker's own record rather than against anybody's prose.

What the two rows kept is the half that was never about the documents: a report
naming no file, no artifact, or no line is unusable, and that is this
repository's defect rather than the Lead Product Manager's.

### What the terms check counts as a new term

The register lists the terms somebody has already found. To refuse the next one
before it reaches a person, `internal/terms` also reads the harness's own words
wherever a person or a role reads them — every string literal holding a space in
the Go source under `cmd` and `internal`, outside tests and test data; the
personas under `internal/config/builtin` and `.yoyodyne/personas`; the guides;
and the governed documents — and picks out one shape of word mechanically: a
compound, two or more runs of letters joined by hyphens. Nothing else has a
shape a check can tell from ordinary English.

A compound is not read where it is not prose: inside a code span, a link's
target, an HTML tag or comment, a fenced block, or frontmatter; touching a
character that makes it part of a flag, a path, a file name, an address, or a
key (`--stall-after`, `run-stops.md`, `docs/run-stops.md`); between a pair of quotes,
where it is a value being named; or in a string with no space in it, which is a
key or an identifier.

What is left is ordinary English, and passes, when:

- its first part is a prefix English builds words with — `re`, `non`, `self`,
  `pre`, `co`, `un`, `mid`, `per`, `in`, and the rest listed in
  `internal/terms/compounds.go`: `re-run`, `non-zero`, `mid-turn`;
- its last part is one English builds adjectives with — `only`, `wide`,
  `facing`, `based`, `local`, `relative`, `time`, and the rest listed there — or
  ends in *-ed*: `read-only`, `operator-facing`, `hand-edited`;
- a part is a number word, or the first part is a single letter: `two-hour`,
  `e-mail` — a single letter further in is an article in a phrase, as in
  `needs-a-human`, and does not count;
- every part is capitalised, which is a name;
- it starts with the harness's own name or a tool's: `yoyodyne-report`,
  `claude-code`;
- it is the name of a Markdown document under `docs/`, which is how a design or
  a decision is cited;
- it is listed under [ordinary compounds](terms.md#ordinary-compounds) in the
  register.

Otherwise it is a term of art, and passes only where the register accounts for
it: a row in either of its tables, whose pattern matches anywhere in the
compound; a term in the vocabulary inventory, whatever its proposed decision,
while that decision is pending; or one of the compounds listed as still to be decided
in `internal/terms/inventory`, which are the ones already written the day this
half of the check began, allowed by name until somebody decides each. A
compound that passes none of these fails `make test`, naming the file and line
and the three answers: the ordinary words, a row in the register, or a place
on the ordinary list for a word nobody would have to look up. A word on the
list of compounds still to be decided that nothing writes any more fails too, so the
list only shrinks.

The inventory's list of terms is read from `internal/terms/inventory`, the same
package `go run ./scripts/vocabulary` writes
[the inventory document](vocabulary-inventory.md) from, so the check never
depends on that document having been regenerated. Regenerating it after a
decision changes the list is still what keeps the document true.

`make dist VERSION=<tag>` builds the release archives and their checksums into
`dist/`, and `make dist-verify VERSION=<tag>` does that and then unpacks the
archive for the platform it is running on and asserts the binary reports
`<tag>`. That target is the whole of what a release consists of: the release
workflow runs it for a pushed tag and publishes what it produced, and CI runs
the same target on every change with a placeholder version, so a tag push
reruns a path that is already exercised rather than executing it for the first
time when a failure would mean a botched or missing release.

## What the checks cover, and what they own up to not covering

The four declared checks are Go commands, so on their own they read none of the
shell, Markdown, workflow YAML, or Makefile this repository is also made of. A
change made entirely of those passed every gate a run applied with nothing having
exercised it, which is not a gate that was weak — it is a gate that never ran.

`internal/composition` is where that is written down. Every file the repository
carries belongs to a content class there, and every class records either the
declared checks that exercise it and what those checks do with it, or why
nothing does. "Nothing exercises this, and here is why" is a legitimate answer —
a `.png` avatar's defect is how it looks, and the tracker's `.jsonl` exports are
rewritten wholesale from a store that is authoritative elsewhere. What is not
legitimate is the class going unwritten, and that is what the audit holds: a
file no class recognizes fails, so the question gets asked rather than skipped.
So does a class crediting its coverage to a check the configuration no longer
declares, which is the same loss arriving from the other direction.

The census is git's own list — what the repository tracks, plus what it carries
untracked and is not ignoring. Both halves matter. A directory walk would find
build output and scratch files, and a gate failing on those is one people learn
to run with a flag; tracked files alone would miss a run's own new files, which
are untracked at the moment its checks run, and a run introducing a new kind of
content is the case the audit is for.

## Cutting a release

`make release VERSION=<tag>` is that build with its gate in front, so a daily
cadence costs two commands rather than a procedure once
[this tag's notes are on `main`](#every-cut-writes-its-notes) and
[carry their readiness result](#release-readiness):

```sh
make release VERSION=v0.3.0
git push origin v0.3.0
```

It gates on [this release's notes](releases/README.md), on [release
readiness](#release-readiness) and on that result being in the notes `main`
holds, walks [the documented adoption path](../scripts/walk-adoption.sh), runs
`check`, builds and verifies the archives for `<tag>`, then tags the commit
they were built from — in that order, so a red gate refuses the cut, names what
was red, and leaves nothing to undo. It also refuses a tag that is not
`vMAJOR.MINOR.PATCH` or that already exists, a dirty working tree, a checkout
that is not on `main`, and a `HEAD` that is not where `origin/main` is; where
origin is unreachable it says that last one went unchecked rather than passing
over it.

**A cut writes nothing to `main`.** The tag names the commit `origin/main`
held when the gates started, which is the commit the gates ran on; the cut
makes no commit of its own, on `main` or anywhere the tag could name, and the
push it prints is the tag alone. That is what a protected default branch
allows, and it is how the v0.5.0 cut on 2026-09-20 could not finish: the verb
then committed its own housekeeping on `main` and tagged that, and a branch
that requires a pull request refused the push — a pull request produces a
different commit, so the tag named a tree `main` would never hold, and a
second cut had fresh housekeeping it could not push either. So whether `main`
is protected is now asked of the forge before anything is built, both ways a
forge can protect a branch — the `protected` field of
`repos/{owner}/{repo}/branches/<branch>`, which any account that can read the
repository may read, and then the rulesets that apply to it — and the cut says
what that changes rather than
finding out at the push: on a protected branch the readiness result reaches
the notes only through the pull request the cut opens, and publishing is the
tag alone. On a branch that is not protected the path is the same. Where the
forge cannot be asked — `gh` not installed, or a remote it does not know — the
cut says that went unchecked and takes the same path. The branch's protection
endpoint, `repos/{owner}/{repo}/branches/<branch>/protection`, is asked only
once the branch is known to be protected, and only to say which rules apply:
it needs administrator rights and answers anybody else with the 404 it gives
an unprotected branch, so a cut that decided from it told a non-administrator
that a protected `main` was open. Where it will not answer, the cut says the
branch is protected and that its rules could not be read.

Two things the tree carries are handled differently because of that. The
tracker's own exports — `.beads/interactions.jsonl` and `.beads/issues.jsonl`
— do not count as a dirty tree and are never committed: they are derived from
a store that is authoritative elsewhere, the archives a release ships are not
built from them (the notes are drafted from one, and are committed before the
cut), a harness running beside the cut refreshes them for readers and the
walkthrough this gate runs rewrites them itself, so refusing on them would
stall most days of a daily cadence and committing them would be a commit
`main` cannot take. They are excluded from the tree check and left where they
lie; the tag is placed on the commit by hash, so what they look like on disk
changes nothing about what it names. And the readiness result reaches
`docs/releases/<tag>.md` ahead of the tag rather than under it, [through a pull
request of its own](#release-readiness), so the cut that finds it absent stops
there and the cut after the merge goes through. Between them, integration may
land more on `main`; the cut says so and tags the commit its gates ran on,
which `main` still holds, and refuses only a `main` that was rewritten
underneath it.

It stops at the tag. Publishing is the `git push`, which is the irreversible
half and what the release workflow acts on, so it stays something you do
deliberately. [`scripts/cut-release-test.sh`](../scripts/cut-release-test.sh)
executes every one of those refusals against fabricated repositories, and
drives a scratch remote whose `main` refuses every direct push through the
two-cut loop to a pushed tag, and `make test` runs it, so changing the verb is
checked by the same command as changing anything else.

## Release readiness

`make check` says the code does what its tests say. A tag says more than that:
that the system still matches what it records about itself. So the cut runs
[`yoyo conformance`](artifacts.md#asking-all-of-this-at-once-before-a-tag)
before it spends the walkthrough — the artifacts and their references, the links
the documentation makes to itself, the invariants, and every admitted work
item's attribution to a goal, with staleness surveyed alongside and refusing
nothing. A divergence refuses the tag and names every mismatch the check that
found it collected; nothing is written.

The sequence those checks run in is not in the code. It is a
[workflow definition](configuration.md#the-release-readiness-workflow) — data in
the project-owned format, selecting actions the harness registered in Go —
validated and compiled before the first check runs, and walked by the workflow
runtime one durable transition at a time. It is the first sequence this harness
runs that never existed as Go control flow, which is the point of it: the same
checks, ordered by a file a project can read and replace, and no way for that
file to make the gate perform anything but reads.

The cut asks for it as the Markdown section a release's notes carry, and there
is one invocation rather than two — a gate read one way and a notes section
written from a second run would be two results, and only one of them would be
the one that refused or did not. On a green cut that section has to be in
`docs/releases/<tag>.md` on `main` before the tag exists, between two
HTML-comment markers, replacing an earlier one rather than accumulating beside
it, and leaving everything the Lead Product Manager wrote around it alone. It gets
there the way every other change reaches `main`: a cut that finds the notes
carrying no result, or a stale one, commits the stamped notes on a branch
named `release/<tag>-readiness-<commit>` on top of the commit `origin/main`
holds — with plumbing, so nothing in the checkout is touched — pushes it, opens
the pull request for it where `gh` is installed, says so, and stops before
spending the walkthrough. Merge it and cut again; the second cut finds the
result current and goes through. A cut run again before the merge finds the
branch and says so rather than pushing over it.

"Current" is the verdict and the pinned definition, not the whole section: the
counts in a reading move with the tracker every day, and a harness running
beside the cut moves them between the merge and the next cut, so a stamp held
to the whole text would never be current and the loop would never close. What
the notes record is the reading the first cut took, one commit behind the tag;
what the tag certifies is that a second reading, on the tree it names, ended
the same way — and a mismatch on that second reading refuses the tag before
anything is stamped.

## Every cut writes its notes

A release nobody can read is a release nobody adopts, so
[`docs/releases/<tag>.md`](releases/README.md) is a gate rather than a courtesy.
The cut checks for it before it spends the walkthrough, and a tag with no notes
is the first of the two refusals that leave something behind: it drafts them
and stops. The second is the readiness stamp, committed on a branch and opened
as a pull request, which is the second `make release` in the loop below.

```sh
make release VERSION=v0.3.1        # drafts docs/releases/v0.3.1.md and refuses
$EDITOR docs/releases/v0.3.1.md    # place each item; the judgement is yours
git add docs/releases/v0.3.1.md    # the draft is a new file, so -a will not do
git commit -m "v0.3.1 release notes"
git push origin main               # or a pull request, where main is protected
make release VERSION=v0.3.1        # stamps the readiness result on a branch,
                                   # opens its pull request, and stops
                                   # ...merge it, and bring main up to date...
make release VERSION=v0.3.1        # green, and the tag names what main holds
git push origin v0.3.1
```

The `git add` is not a flourish: the drafted notes are a file git has never seen,
so `git commit -a` stages nothing and stops with "no changes added to commit".

**The push is not optional, and it is not the tag push.** The cut
refuses a `HEAD` that is not where `origin/main` is, so a notes commit that
exists only in your checkout stops the *second* `make release` rather than the
first — and it stops it before the notes gate, so the message you get names the
remote rather than the notes. This repository protects `main` against direct
pushes, so its own notes commit reaches `main` the way every other change does,
through a branch and a merged pull request; the direct push above is the short
form for a repository that permits one. Either way the cut runs once
`origin/main` carries the notes. Only a checkout whose origin is unreachable
skips this, and the cut says that went unchecked rather than passing over it.

**The second `make release` is not a retry.** The notes you committed carry no
readiness result — a draft never does — so the cut that finds them on `main`
[stamps the result into them on a branch and opens the pull request for
it](#release-readiness), and that is the second stop. It is a pull request like
any other, so it goes through the same review and checks, and the cut after
the merge is the one that tags. Nothing here is written to `main` by the cut
itself, which is what a protected `main` permits.
[`scripts/cut-release-test.sh`](../scripts/cut-release-test.sh) executes this
loop against a scratch repository with a real remote, unpushed notes and all,
and again against one whose `main` refuses every direct push.

The draft comes from the tracker rather than the commit log:
[`scripts/release-notes.sh`](../scripts/release-notes.sh) reads the work items
closed between the previous tag and this one out of the tracker's export,
`.beads/issues.jsonl`, and carries their titles, their descriptions, their
types, the goals they served, and — for an item that did not come from a person
— which persona asked for it. A commit message says what one change did; the
item behind it says what somebody wanted, which is the difference between notes
and a changelog. The export rather than `bd` is what it reads, so a draft needs
no tracker on the machine and can be made from any checkout carrying the
export. `make release-notes VERSION=<tag>` drafts one on its own, and
`bash scripts/release-notes.sh <tag> --print` shows one without writing it.

Every entry has one shape, which is the operator's:

```markdown
- **The title** (`yoyodyne-ifd.404`)
  Serves: the goal the item names
  Requested by the reviewer: the reason, in the reviewer's own words

  The item's description, paragraph by paragraph.
```

The `Requested by` line is there only for an item that did not originate with a
person, and which those are is read off the admission the item's own notes
record rather than guessed from its description: an item admitted from a role's
collected report names that role and carries the report's own words; an item
the development manager carved out of a parent names the development manager,
the parent, and the reason recorded — ``Requested by the development manager,
decomposing `yoyodyne-ifd.209`: …``. An item the Lead Product Manager admitted with
neither is the ordinary case, work the operator asked for through her, and
carries no such line, because the notes do not say which person asked and
guessing would be worse than silence. The two patterns that read those
admissions are held against the harness's own note writers by a test in
`internal/chat`, so a change to either wording fails there by name rather than
quietly dropping the line.

The releases cut before this shape existed — [`v0.3.0`](releases/v0.3.0.md),
[`v0.4.0`](releases/v0.4.0.md), and [`v0.5.0`](releases/v0.5.0.md) — carry
titles and goals only; the next draft is the first written this way.
Redrafting one of them is `bash scripts/release-notes.sh <tag> --force`, and
it is a person's commit rather than a run's: with every description in it,
v0.5.0 redrafts to some 300 KB, which is more than the review gate's patch
bound will show a reviewer and more than [the forge accepts as a release
page's body](#what-a-release-page-carries), and the draft writes over the
intro and the placement the Lead Product Manager made by hand, so both have to be
carried back into it.

Only what the tracker calls **closed** reaches the notes. An id in a commit
message says work touched that item, not that the item is done — a parent epic
is named by every child's commit, and a multi-part item by each part as it lands
— so publishing either as shipped is the one lie this is careful about. Items
dropped for not being closed are counted in the output, alongside the tokens
that looked like ids and are not in the tracker at all, so neither exclusion is
silent.

Where the draft puts each item is placed from its type, and **that placement is
a starting point rather than an answer**: which work is key functionality, which
is an enhancement, and which fix is critical enough to go up to the top is the
Lead Product Manager's judgement until the post-v1 release-manager role exists. The
three sections and their order are the operator's and are not the draft's to
change. [`scripts/release-notes-test.sh`](../scripts/release-notes-test.sh)
executes the placement rule, the section order, the entry shape — three items
side by side, one the operator asked for, one from a reviewer's report, one the
development manager decomposed — and the refusals against a fabricated
repository and a fabricated export, and `make test` runs it.

## What a release page carries

The release workflow publishes that same file as the release page's body, with
the install preamble under it, so the release page and the repository tell one
story rather than two. That is the whole of the page: the tag's notes, the
preamble, the three platforms' archives, and their checksums. The forge is
never asked for its own generated notes. Those are a changelog — every commit
since the previous tag, each saying what one change did — and the distinction
above is not a nicety: on v0.5.0 the publish step passed `--generate-notes`
beside `--notes-file`, the forge appended some six hundred commits under 84,765
characters of curated notes, and refused the whole body as longer than the
125,000 characters it accepts. The notes grow with the backlog, so that would
have refused every release from there. `internal/cli`'s release test reads the
workflow's publish step and fails on the flag by name.

The archives are built at the commit the tag names. A binary records the commit
it was built from beside the version it is stamped with, and v0.5.0's local cut
built its archives before its own housekeeping commit, so they named a commit
the tag did not and were rebuilt by hand. A cut makes no such commit any more
— [it writes nothing to `main`](#cutting-a-release), and tags the commit its
archives were built from — so a local cut's archives and its tag now name the
same commit. What ships is still the workflow's own build rather than a local
one: its checkout is the tag's own commit, and a step ahead of the build
asserts `HEAD` is what the tag resolves to rather than trusting that it is.

[`scripts/release-body.sh`](../scripts/release-body.sh) is the composition,
kept as a script rather than inline in the workflow because workflow YAML on a
tag trigger first executes during a real publication. It measures what it
composed: at 75 percent of the forge's limit it warns on stderr, where the
workflow's log shows it, and over the limit it refuses, naming the length and
the file to shorten, so a body too long is refused here with a reason rather
than by the forge with a status code. Both figures are read from the
environment, `RELEASE_BODY_LIMIT` and `RELEASE_BODY_WARN_PERCENT`, which is how
the test above drives them against notes a few hundred bytes long. That test
holds the body to being the notes and the preamble and nothing else, byte for
byte, and covers what a tag with no notes file publishes.
