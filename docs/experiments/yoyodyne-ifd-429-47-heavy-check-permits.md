# Experiment: what limiting heavy checks costs, and what it saves

Work item: yoyodyne-ifd.429.47, heavy-check permits. Measured on the
operator's Mac on October 12 2026 at 23:15 PDT and October 13 2026 between
02:59 and 03:05 PDT, while the machine was running other work.

**Status: measured once each way, under load; not a basis for the numbers yet.**
These figures say what the permits themselves cost and show the shape of what
a limit does, but they were taken with the machine's one-minute load average
between 12 and 21, from other runs and this run's own test suites. The numbers
`execution.heavy_checks` should carry are the business of measured limits
(yoyodyne-ifd.429.48), which needs figures from a machine doing nothing else.

## How it was measured

`TestMeasureHeavyCheckPermits` in
`internal/orchestrator/heavychecks_measure_unix_test.go` runs only when
`YOYODYNE_MEASURE_HEAVY_CHECKS` names a directory, and writes its table there:

```sh
YOYODYNE_MEASURE_HEAVY_CHECKS="$somewhere" \
  go test ./internal/orchestrator/ -run TestMeasureHeavyCheckPermits -count=1 -v -timeout 25m
```

Each case starts several runs at once through the real check runner, the real
process runner, and the real record of places, once with no limit and once
with one place, and reads:

- **wall**: from the first run starting to the last ending;
- **CPU**: user and system time of every process the case started, their
  children included, read from the test process's own resource usage, so the
  test is run on its own;
- **sum of check time**: each check's time from launch to exit, added up;
- **waits**: how long each run waited for its place.

The cases: a shell loop doing a fixed amount of arithmetic, four at once, for a
check that is not Go; `go test ./internal/domain`, two at once, for a narrow Go
check; and `go test` over three packages, two at once, for a broad one. Each Go
run has a build cache of its own, so a warm cache is not what is measured.

## Figures, October 13 02:59–03:05 PDT (load average 12 to 21)

| case | places | wall | CPU (user+system) | sum of check time | longest wait | sum of waits |
| --- | --- | --- | --- | --- | --- | --- |
| shell loop, not Go, 4 at once | no limit | 8.3s | 31.55s | 32.65s | 0s | 0s |
| shell loop, not Go, 4 at once | 1 | 38.13s | 31.15s | 33.33s | 26.6s | 54.28s |
| one Go package (narrow), 2 at once | no limit | 45.42s | 34.67s | 1m30.71s | 0s | 0s |
| one Go package (narrow), 2 at once | 1 | 1m42.21s | 32.37s | 1m40.74s | 50.15s | 50.22s |
| three Go packages (broad), 2 at once | no limit | 58.21s | 1m0.73s | 1m56.17s | 0s | 0s |
| three Go packages (broad), 2 at once | 1 | 1m0.23s | 53.73s | 59.31s | 31.8s | 31.84s |

One launch of `exit 0`, mean of 50: 9.6 ms with no place asked for, and 163 ms
asking for a place, launching behind its gate, and giving the place back.

An earlier run on October 12 at 23:15 PDT, before CPU time was measured, gave
the same shape: four shell loops took 8.2 s with no limit and 35.3 s with one
place; two narrow Go runs 12.4 s and 18.2 s; two broad ones 9.4 s and 18.7 s;
and one place cost about 118 ms.

## What the figures say

**What a place costs is about a sixth of a second per heavy check**, beside a
check that takes minutes. It was not broken down further; what it is made of
is most likely the record being written and flushed to disk three times — when
the place is given, when the process is written down before it does any work,
and when it ends and the place is given back — each a file and its directory,
on a disk shared with everything else the machine was doing. Nothing of it is
spent while a check runs.

**A limit spends the same CPU and moves time from running to waiting.** In
every case the CPU the checks used was within about twelve percent with or
without the limit: the work is the same work. What changed is where the time
went. With one place, each check ran closer to its own time alone — the broad
Go case's checks took 59 s together instead of 1 m 56 s, because two of them no
longer contended for the same cores — and the second run waited for the first.

**On a machine this busy, the limit did not shorten the whole.** The wall time
of each case was the same or longer with one place: the machine was already
saturated by other work, so running two checks at once rather than one after
the other cost them little against each other, and the waiting was added on
top. The case for the limit is the one it was admitted for: a check's own time
no longer grows with how many others run beside it, so a check's time limit
stops being spent on contention, and everything else on the machine has the
cores the heavy checks no longer take all at once. These figures do not show
the machine-wide effect, because they measure only the checks they start.

## What remains

- Measure on a machine doing nothing else, and with the real suites
  (`make race`, `make test`) rather than stand-ins, before setting the numbers
  under measured limits (yoyodyne-ifd.429.48). The harness now measures every
  real launch of a heavy check as it runs — its wait, its time against its
  limit, how it ended, and the machine's load, free memory, places held, and
  suites run outside every place as it launched — and
  `yoyo heavy-checks measurements` proposes each check's number from them
  ([setting a heavy check's number from what it cost](../configuration.md#setting-a-heavy-checks-number-from-what-it-cost)).
  It measures only a project that names its heavy checks, so the figures start
  once `execution.heavy_checks` is set.
- Measure the machine-wide effect: what the other runs beside a limited suite
  gain, which is what the limit is for and what these cases cannot see. The
  machine's load against how many heavy checks were running is the part of
  that the launches' measurements carry.

## Test coverage of the change

From the permit tests in each package, by statement:

| file | covered |
| --- | --- |
| `internal/runstate/heavycheck.go` | 76.9% of 424 |
| `internal/runstate/heavycheck_unix.go` | 75.0% of 24 |
| `internal/orchestrator/heavychecks.go` | 93.1% of 29 |
| `internal/config/heavychecks.go` | 89.7% of 39 |
| `internal/checks/permits.go` | 88.9% of 9 |
| `internal/readmodel/heavychecks.go` | 61.9% of 21 |

What is not covered is mostly the paths taken when the record cannot be read,
written, or locked, which the tests reach only for an unreadable record.
