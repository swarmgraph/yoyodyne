# Experiment: what narrowing make test and make race costs, and what it saves

Work item: yoyodyne-ifd.429.46, scoped local race checks. Measured on the
operator's Mac on October 24 2026 between 12:59 and 16:40 PDT, while the
machine was running other work: the one-minute load average was between 9 and
40 throughout, so every figure here is one measurement under load rather than
a benchmark.

## How it was measured

Each command was run in this item's worktree under `/usr/bin/time -p`, which
gives wall time and the user and system time of everything the command
started. CPU below is user plus system time. It reads low for tests that
mostly wait on timers, as `internal/checks` does, so the small figures say
little and the large ones are the comparison. Every run was uncached
(`-count=1`, or a first run), except where a row says otherwise.

"Before" is what this project's checks ran on every attempt until this change:
`make test` and `make race` over the whole module. "After" has two parts: the
checks before review, narrowed to the packages the change touches, on every
attempt; and one pass over the approved merge candidate, told the whole
module, before it is promoted.

## Figures

The whole suite, which is "before" for every case and the merge candidate's
pass after:

| Check | Wall | CPU | Packages |
| --- | --- | --- | --- |
| `make test` | 1,940 s (32 min) | 2,329 s | 96 with tests |
| `make race` | 2,013 s (34 min) | 4,079 s | 99 with tests |

`internal/orchestrator` alone took 1,915 s of the `make test` run and 1,812 s
of the `make race` run, so a change touching it gains little from narrowing.

The checks before review, after this change:

| Case | `make test` | `make race` |
| --- | --- | --- |
| Narrow: one package, `./internal/checks` | 21 s wall, 7 s CPU, 1 package, through `make` | 14 s wall, 2 s CPU, 1 package, through `make` |
| Broad: six packages (`runstate`, `config`, `composition`, `checks`, `cli`, `readmodel`) | 527 s wall, 130 s CPU, 6 packages | 106 s wall, 168 s CPU, 6 packages |
| Concurrent: the narrow and the broad race started together | — | narrow 10 s; broad 266 s wall, 185 s CPU, 2.5 times as long as alone |
| Markdown outside every package, nothing else | the whole suite: the tests read the documents | 0.2 s: runs over nothing and says why |
| A script, the Makefile, or the configuration | the whole suite | the whole suite |
| Touches `internal/orchestrator` | about the whole suite | about the whole suite |
| A developer's own run of the checks, where the variable is not set | the whole suite, as before | the whole suite, as before |

The broad and concurrent rows were run as `go test` over the same packages
rather than through `make`; for the narrow case `make` added about two
seconds. The merge candidate's pass adds the whole suite once per approval,
with `make race` still skipped for a change that is only Markdown outside
every package, exactly as it was before.

This project names no landing checks, so nothing ran at landing either before
or after.

## What the figures say

For a change approved on its first attempt, the checks now cost more than
before: the narrowed checks, then the whole suite once. For a change that is
repaired or sent back by its reviewer, each attempt after the first no longer
pays for the whole suite, and the developer and the reviewer are told the
result in seconds or minutes rather than half an hour. A change that touches a
document, a script, the Makefile, or the configuration runs the whole
`make test` both before review and on the merge candidate, because the
narrowing cannot tell which test reads which file.

Whether this raises the number of changes landed in a day depends on how many
runs are repaired or sent back, which the run records over the following week
will show.
