# yoyodyne-ifd.429.69: the orchestrator package's test time limit, measured

Two approved changes were sent back at their checks on 2026-10-06 because
`internal/orchestrator` reached the Makefile's twenty-minute `TEST_TIMEOUT`
with its tests waiting on the parallel limit rather than failing. This item
sets that limit from what the package actually takes under the load the
harness runs at, inside the limit the harness gives a check, until the package
split (yoyodyne-ifd.429.14) makes the package smaller. This is the record of the
measurement, taken on 2026-10-07 against `66b4740a`.

## What ran

Two check stages over one worktree, started twenty seconds apart from one
script in the run's scratch directory. Each stage was this project's declared
checks as the harness runs them, `make test` and then `make race`, both over
the whole module, with `GOFLAGS=-count=1` so nothing was served from Go's test
cache and with `TEST_TIMEOUT=60m` so the package's own time was measured rather
than cut off at the limit being set. The machine has sixteen cores and was
carrying other developer runs and their provider processes throughout. The
one-minute load average was sampled every thirty seconds.

## What it reported

| stage | check | check's wall time | `internal/orchestrator` | one-minute load over the check (low, median, high) |
| --- | --- | --- | --- | --- |
| A | `make test` | 974 s | 870.6 s | 6.4, 10.2, 16.4 |
| B | `make test` | 952 s | 868.6 s | 6.4, 10.2, 16.4 |
| A | `make race` | 1,491 s | 1,439.1 s | 8.9, 30.1, 64.2 |
| B | `make race` | 1,495 s | 1,437.2 s | 8.9, 30.1, 64.2 |

The orchestrator package passed every time. The next slowest packages under
`make race` were `internal/cli` at 499 s, `internal/gitworktree` at 359 s, and
`internal/beads` at 332 s.

Each check also reported two failures that are this measurement's sandbox and
not the tests: `TestNativeResumeReplacesSavedDirectoryGrants` in
`internal/backend/codex` and
`TestGitHubChecksReadsALargeComparisonBeforeRetainingOnlyItsDistance` in
`internal/publish` each open a local port, and the developer run's sandbox
refuses that (`bind: operation not permitted`). The harness runs its checks
outside that sandbox.

The `make test` half ran at a lighter load than the harness reported on
2026-10-06, when the same package took about 1,170 seconds at a one-minute load
of 15 to 23. That figure is taken as the `make test` worst case here, beside the
measured `make race` figure.

## The figure, and why it cannot be higher

The worst case is the race binary: 1,439 seconds, about twenty-four minutes,
which is already past the twenty minutes the limit was. `TEST_TIMEOUT` is now
twenty-eight minutes, four minutes (about seventeen percent) over that and eight
and a half over the worst `make test`.

The ceiling is `execution.check_timeout`, which gives each check thirty minutes
and is not scaled for load. `make race` spent about a minute compiling before the
package started (1,491 s for the check against 1,439 s for the package), so a
hung test under a twenty-eight-minute limit is reported by `go test`, with every
goroutine's stack, at about twenty-nine minutes into the check, before the
harness would end the check at thirty with nothing to read. The check stage is
wider again: this project's `check_stage_timeout` is sixty minutes before any
load scaling, and the two checks here took about forty-one minutes between them.

A genuinely stuck test still fails the check: it now fails at twenty-eight
minutes rather than twenty. No test is skipped, shortened, or changed.

## What that does and does not show

It shows that the package fits under the new limit at the load the harness runs
at, with a second check stage beside it, with a few minutes to spare. It does
not show that the margin holds at any load: the race binary at twenty-four
minutes is already within six minutes of the check's own thirty, and the
package grows as work lands. The figure is interim until the package split
(yoyodyne-ifd.429.14), and nothing here can raise it further without the
check's limit rising first, which is each check's limit scaling with load
(yoyodyne-ifd.429.41).
