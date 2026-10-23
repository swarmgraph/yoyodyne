# yoyodyne-ifd.270: `make check` repeated under load

The item's done conditions are that every test it names waits on the condition
it asserts rather than on a length of time, that ten consecutive full `make
check` runs pass under load, that the repetition runs with `GOFLAGS=-count=1`
so a cached pass never stands in for an executed one, and that the run says
whether the sink's live `F_FULLFSYNC` is worth a separate look. This is the
record of the last three, and of the one wall-clock bound the sweep behind the
first had left behind it. That first condition was already met when this run
started, and the section below says how.

## What was already true

Every test the item names had been converted by
[yoyodyne-ifd.389](yoyodyne-ifd-389-race-beside-race.md), whose rule is stated
in [`docs/developing-yoyo.md`](../developing-yoyo.md#a-test-never-bounds-a-wait-in-wall-clock-time):

- `internal/slack`'s `waitFor` is gone. `TestARefusalOnlyAPersonCanClearIsSaidOnceAndThenWaitedOut`
  and `TestReportingSaysSoWhenItStartsWorkingAgain` drive the sink one pass at a
  time through `passStepper`, so what they assert is how many passes there
  were rather than how long they took, and the sixteen-second sink-state wait
  went with the helper.
- `TestStoppingTheSinkStopsIt` reads promptness off what the sink did — a sink
  stopped before it started asks the workspace nothing and records no presence
  — instead of allowing five seconds for it.
- `internal/execution`'s timeout and stall tests hold the runner's own budget
  and idle bound through a seam, and `TestOSProcessRunnerLeavesAChattyProcessAlone`
  reads the claim off the bound's resets rather than off a chatty helper
  outliving two seconds.
- The rebases are bounded by `Manager.localTimeout`, which scales the idle
  thirty seconds by how far the one-minute load average exceeds the cores,
  capped at ten times, per command. `TestManagerIntegrateAdmitsOnePromotionAndReplaysTheLoser`
  and `TestSchedulerRunsSeveralEligibleItemsAtOnceInWorktreesOfTheirOwn` run
  their Git through that manager, so the budget that killed them at a flat
  thirty seconds now moves with the machine.

What was left of the item was therefore its verification, which 389 did for
`make race` and not for `make check`, and its question about `F_FULLFSYNC`.

## What ran

Ten `make check` runs in sequence over one worktree, from a script in the run's
own scratch directory, each recording its exit code and the one-, five-, and
fifteen-minute load averages at its start. A second suite looped beside them
for the first part of the sequence; the rest of it ran against the load the
machine was already carrying, which on this machine is several other developer
runs and their provider processes rather than an idle baseline. Both loops
carry their own deadline, computed when the script starts, so the load stops on
its own whatever becomes of what started it.

Every run was made with `GOFLAGS=-count=1`. `make test` and `make race` on an
unchanged tree are served from Go's test cache, and a cached pass executes
nothing and evidences nothing: 389's first attempt at its own repetition served
nine of ten runs from the cache in about a minute each before it was noticed.
The same flag is why this took the time it did.

## What the first attempt reported, before the fix

The run's own minute-zero probe — one `make check` on the unchanged tree, while
a second suite ran beside it — failed, and not on any bound a test had set:

```
panic: test timed out after 10m0s
	running tests:
		TestAProviderInvocationTheMachineNeverStartedIsRefusedEnvironmentally (0s)
		... sixteen tests, each 0s to 38s
FAIL	github.com/mason-bryant/yoyodyne/internal/orchestrator	600.624s
```

Ten minutes is `go test`'s own default `-timeout`, the bound the rule in
`docs/developing-yoyo.md` relies on to turn a hang into a failure that names
what it waited on. It is also a wall-clock bound, and at a one-minute load
average of 85 on sixteen cores `internal/orchestrator`'s race binary reached it
with hundreds of its 761 tests still queued on the parallel limit — a suite that
was working, failed by the machine. The same package had taken 371 seconds in
the `make test` half of the same probe, so the margin was already thin without
the race detector.

So the bound was sized for the loaded machine rather than left at the figure a
quiet one gets: `TEST_TIMEOUT` in the `Makefile`, twenty minutes, on both `test`
and `race`. That is the only code change this item made; everything below ran
against it.

## What it reported

| run | exit | started | ended | load at start (1, 5, 15 min) |
| --- | --- | --- | --- | --- |
| 1 | 0 | 10:35:02 | 10:55:54 | 11.10, 55.30, 50.68 |
| 2 | 0 | 10:55:54 | 11:17:16 | 42.60, 50.04, 49.37 |
| 3 | 0 | 11:17:16 | 11:29:40 | 58.68, 61.92, 57.18 |
| 4 | 0 | 11:29:40 | 11:43:31 | 22.06, 37.52, 48.77 |
| 5 | 0 | 11:43:31 | 11:59:11 | 38.86, 42.34, 43.26 |
| 6 | 0 | 11:59:11 | 12:11:31 | 26.92, 35.96, 39.75 |
| 7 | 0 | 12:11:31 | 12:19:14 | 19.83, 32.99, 36.87 |
| 8 | 0 | 12:19:14 | 12:31:28 | 21.12, 26.20, 31.47 |
| 9 | 0 | 12:31:28 | 12:38:52 | 25.15, 31.07, 31.85 |
| 10 | 0 | 12:38:52 | 12:45:35 | 14.65, 21.46, 26.56 |

Ten of ten passed. The neighbour's three `make race` runs over the first
thirty-five minutes exited 0 as well. No log of the thirteen suites carries a
`FAIL` line, a `panic:` line, or a `(cached)` line.

The figure that was raised is the one to read these against.
`internal/orchestrator`'s binary is the longest in the suite, and over those
thirteen suites its race half took 614.7s, 711.7s, 281.7s, 534.7s, 538.0s,
298.4s, 253.9s, 404.6s, 219.5s and 205.7s, with the three neighbour runs at
715.2s, 576.3s and 708.7s. Four of those are past the ten minutes that failed
the probe, and the longest is 11m55s — so this sequence did not merely pass
with the bound raised, it needed the raise four times over, and still ran at
two thirds of the new figure at its worst.

## Is `F_FULLFSYNC` on the sink's live path worth a separate look

No — and the measurement rather than the assertion. `Store.write` replaces each
record through a temporary file it `Sync`s before the rename, and on darwin
`os.File.Sync` is `F_FULLFSYNC`, a barrier the device waits on rather than a
write the kernel may still be holding. Measured on this machine's repository
volume at a one-minute load average of 67 on sixteen cores, one record of the
size the sink writes cost 480ms with the sync and 78ms without it: at that load
the barrier is most of a write, and on a quiet machine it is a small fraction of
a much smaller number.

What the barrier lands on is what decides it. An ordinary pass writes nothing at
all — cursors are saved when the watermark is first taken or a stream goes away,
threads when a thread is opened or a status moves — and a pass that does write
does so against a fifteen-second poll interval and, per delivery, a one-second
pace that exists because that is what Slack accepts. Half a second of barrier
behind a second of deliberate pacing is not a cost anybody is waiting on;
reporting is not a gate on anything, and nothing blocks on a pass.

The two writes on a path somebody does wait on are the presence record written
at startup and cleared on the way out, and each is one record. Both are already
behind the context read that `TestStoppingTheSinkStopsIt` asserts, which is what
makes a stop prompt: a sink stopped before it started writes no presence at all.

The test-time store needs no plain-fsync seam either. What made the cost visible
was the sink tests polling at a millisecond against a wall-clock bound — a loop
fsyncing as fast as it could, against a clock that load could win — and those
are gone: no test in `internal/slack` now bounds anything on how long a write
takes, so the fsyncs a test spends are a handful per test rather than thousands.

If reporting ever does look slow on a busy machine, the place to look first is
`SaveCursors` inside the delivery loop, which writes once per delivery, and not
the pass or the store's durability. That durability is what the barrier buys:
the thread map is the difference between reopening one thread and losing every
thread the channel has.

## What this does and does not show

It shows that the whole gate, and not only its race half, survives the
concurrency this machine carries: ten `make check` runs in sequence over three
hours and ten minutes, at one-minute load averages from 14 to 59 on sixteen
cores, with a second suite beside the first three of them, and nothing executed
from the test cache. It also shows that the bound the rule leans on had itself
become a bound on the machine, and by how much: four of thirteen runs of the
longest package went past the figure that had been failing suites.

It does not show that no wall-clock bound is left in the suite. Three that
nothing here converted, none of which failed in these runs:
`TestAdoptWaitsOutALeaseNobodyHoldsAnyMore` in `internal/runstate`, which closes
a phantom lock after a fifth of `leaseGrace` and expects `Adopt` to still be
waiting — named the same way in 389's record and still standing;
`TestAProcessThatStopsIsNotExitedBehindIt` in `internal/shutdown`, which asks a
twenty-millisecond grace not to have fired before the test's next line; and
`TestDashboardPrintsItsURLAndTokenAndStopsWhenAsked` in `internal/cli`, which
allows five seconds for a listener to print its token and ten for the command to
stop. Each is named in this run's summary as work to admit rather than converted
here. All three were converted since, by
[yoyodyne-ifd.429.3](yoyodyne-ifd-429-3-second-tranche-under-load.md).

One thing about repeating this is worth knowing in advance. The sequence took
three hours and ten minutes, and a developer run's provider invocation is
bounded at four; this one had already spent half an hour before the loop
started, and finished with the margin it had left rather than with any to
spare. A repetition of this size does not reliably fit inside one run, and
whoever asks for the next one should expect to run the loop beside the harness
rather than inside it.

## Sub-second budgets, swept

`TestALandingRunsUnderItsOwnBudgetAndAStoppedCheckLeavesItUnverified` gave
its gate check, `true`, fifty milliseconds and failed under `-race`, and
yoyodyne-ifd.422 raised the figure to ten seconds. yoyodyne-ifd.429.24 then
swept every `_test.go` file for a duration under a second — `time.Millisecond`,
`time.Microsecond`, `time.Nanosecond`, and a literal such as `50ms` — along with
the second-scale budgets in the packages that run commands, and the shell
suites under `scripts` and `bin`, which carry none. The sweep was made twice:
once by the run that stopped on 2026-09-26 against the tree at `30dc3a2b`, and
again over everything `main` gained between that commit and `197f9f82`, which
the run that finished the item started from.

A budget under a second is only a load failure where the test needs it **not**
to be reached. Where running out is the outcome asserted, load makes that
outcome more certain rather than wrong, so the question each hit was asked is
which of the two it is.

### Converted

| Test | What it gave, and to what | What it does now |
| --- | --- | --- |
| `TestPingsAloneKeepAQuietConnectionAlive` (`internal/slack`) | a 500ms idle bound on a real read, with pings 100ms apart that had to arrive inside it | Reads the deadlines the connection was given off a recording connection, `heldDeadlines`: one per frame, each a whole bound past the read. A bound set once per call records one, and fails. `TestSilencePastTheDeadlineEndsTheRead` is still the real timer running out. |
| `TestHangingUpTellsThePeerTheConnectionIsOver` (`internal/slack`) | the code's own 250ms `closeCourtesyTimeout`, on a close frame the test needs its peer to read | The write bound is held rather than armed, so the frame waits for a peer that is slow to be scheduled. What is asserted is that the frame is sent. |
| `TestTheKeyboardIsNegotiatedAndHandedBack` (`internal/console`) | 200ms for a terminal reply the test writes from a goroutine | A terminal that answers is waited for: its identity reply ends the negotiation. The case whose terminal answers nothing keeps 200ms, because running out is what it asserts. |
| `TestTypingAheadOfTheNegotiationIsKept` (`internal/console`) | two seconds, the same shape over a second | Waited for, as above. |
| `TestSuspendingHandsTheTerminalOverAndTakesItBack` and `TestSuspendingDuringAChoiceTakesTheListDownAndPutsItBack` (`internal/console`) | the code's own 250ms `keyboardReplyTimeout`, spent by the resumed negotiation while a polling goroutine writes the reply | The terminal carries the bound as `keyboardWait`, a field the harness leaves at `keyboardReplyTimeout`, and these tests set it past the binary's own `-timeout`. |
| `TestALandingRunsUnderItsOwnBudgetAndAStoppedCheckLeavesItUnverified` (`internal/orchestrator`) | the ten seconds 422 gave the gate's `true`, and a check that the landing's `sleep 30` was stopped inside those ten, which is a kill and a reap load can stretch | The gate is given ten minutes. A landing run under the gate's bounds would see `sleep 30` finish and pass, so a check stopped at its bound is already the landing's own budget at work, and the elapsed-time check went. |
| `TestAPullAttemptsEveryDecisionItHasASlotForAndSaysWhyItPassedTheRest` (`internal/orchestrator`, new since `30dc3a2b`) | a loop polling every 5ms for the first pull's account, given up on after ten seconds | Waits on two signals: the schedule harness tells a channel when a pull hands over what it passed over, and each carry says it has arrived before it blocks. |
| `TestAFreedSlotIsRefilledAtTheNextPollWhileAnotherRunIsStillGoing` and `TestASlotAnotherProcessFreesIsRefilledAtTheNextPollWhileTheSessionIsFull` (`internal/orchestrator`, new) | five seconds for a held run to be released, after which it ended on its own and failed the assertion that it was still going | The held run waits on its release alone. The behaviour these guard against never releases it, and the binary's `-timeout` reports that hang. |
| `TestTheStandingIsASnapshotServedWithItsAgeWhateverTheBuilderIsDoing` (`internal/dashboard`, new) | ten seconds for a request to be answered while the test holds the builder | The request is made directly. One that waited on the held builder would hang, and the `-timeout` names where. |
| `TestTheSupervisorInstalledOverTheMaintenanceJobRetiresItAndCarriesTheRebuild` (`internal/cli`, new) | a thirty-second context on a supervisor the test stops itself once the rebuild has run | A context the test cancels, and nothing else. |
| `awaitLanding` in `internal/orchestrator` and `waitFor` in `internal/supervise` | a poll for a state given up on after thirty and five seconds | The polls carry no deadline. Both were named for admission by the first sweep and are converted here, because the fix is to delete the deadline. |

### Left as they are: the bound is what the test asserts

- `internal/orchestrator`: `TestPipelineReportsElapsedAndBudgetWhenACheckTimesOut`
  (100ms on `sleep 30`); `TestALandingRunsUnderItsOwnBudgetAndAStoppedCheckLeavesItUnverified`
  (the landing's one second on `sleep 30`; its gate is in the table above);
  `TestALandingThatWaitsOutItsBoundRunsNothingAndIsUnverified` (50ms on a held
  landing lease); and `witnessedWorktrees.leaseHeld` in the promotion tests
  (100ms). That probe either finds the lease held and is meant to give up, or
  finds it free and takes it on the first `flock`, which is tried before the
  deadline is read.
- `internal/runstate`: `TestPromotionLeaseWaitIsBounded`,
  `TestPromotionLeaseDiesWithItsHolder`,
  `TestALandingsWaitIsBoundedAndTellsTheBoundFromACancellation`,
  `TestAClaimGivesUpWhenItsContextIsDone`, and
  `TestTakingAConversationBackGivesUpWhenItsContextIsDone`, each on a lease or a
  claim somebody holds. `TestALandingHoldsNeitherThePromotionLeaseNorAnotherBranchsLanding`
  gives 50ms to leases nobody holds, and the first `flock` takes them before the
  bound is consulted, so load cannot reach it.
- `internal/execution`: `TestOSProcessRunnerRealBudgetEndsAProcessThatOutlivesIt`
  and `TestOSProcessRunnerRealIdleBoundStopsASilentProcess`, 50ms of the
  runner's own timer on a helper that sleeps. That timer is the thing under test.
- `internal/gitworktree`: `TestAWorktreeCheckoutKilledByItsBudgetIsSaidInOneSentence`
  and `TestACountKilledByItsBudgetRefusesTheCreationAsAKilledCheckout`, a
  nanosecond on the one command they are about.
- `internal/cli`: `TestATakeoverThatDoesNotHappenIsGivenUpOnWithinItsDeadline`
  (50ms on a wedged re-execution). `internal/chat`: `TestStoppingReportsARunThatHasNotGivenUpYet`
  and `TestAnUnstoppableRunIsReportedRatherThanDescribedAsStopped` (a stop grace
  of 1ms and 10ms on runs that do not stop). `internal/shutdown`:
  `TestAProcessThatDoesNotStopWithinItsGraceExits` (1ms).
- `internal/slack`: the three conversation tests that set `steering.deadline`
  to 20ms, each on a conversation that does not answer in time, and
  `TestSilencePastTheDeadlineEndsTheRead` (50ms on a silent peer).
- `internal/dashboard/fixtureserver`: `TestEveryScenarioLoadsAndAnswersAsNamed`
  gives each read 50ms. The scenarios that answer never read the context, and
  the pending one is meant to run it out.
- `internal/console`: `TestBracketingIsAskedForAndHandedBack` resumes with
  nobody answering the negotiation, so its 250ms runs out as it would on a
  terminal that says nothing, and the test writes nothing until the region is
  drawn again.

### Not budgets

Poll intervals and pacing that make a test quicker and never decide its result:
`progressInterval` in `internal/chat`, `Poll` in `internal/supervise` and
`internal/cli`, `look` in `internal/cli`'s stream test, the read-back interval in
`internal/beads` (a sleep the test replaces), the marker poll in
`internal/gitworktree`'s integration runner, the helpers' own sleeps in
`internal/execution` and `internal/console`, and the arithmetic in
`internal/gitworktree`'s load test and `internal/runstate`'s timestamp
precision. The two 250ms windows in `internal/gitworktree`'s lease tests and
the three-second window in `internal/execution`'s group-kill test assert that
something did **not** happen, which a slow machine can make uninformative but
cannot make fail.

### The repetition

The converted tests were run ten times in sequence under `-race` with
`-count=1`, so nothing was served from the test cache, all six packages they
live in per run. Ten of ten exited 0, between 23:27 and 23:33 PDT on
2026-09-27, at one-minute load averages from 40 to 50 on sixteen cores. The
whole of `make test` then ran once with `GOFLAGS=-count=1` and exited 0,
between 23:39 and 23:51 PDT, with the one-minute load between 34 and 63; and
`make race` over those six packages, `-count=1` again, exited 0 between 23:51
and 00:02 PDT, with the load between 40 and 53.

The ten full `make check` runs that make up this record's own repetition were
not repeated: the section above says that sequence takes over three hours, and
the run that first took this item stopped inside it. The targeted ten are the
repetition for the tests this item changed; the one uncached `make test`, and
the race detector over the packages the change touches, are the evidence that
nothing else moved.

### Found, outside this sweep

Budgets of thirty seconds on real processes whose running out would fail a
test that is working, which is the same class at a figure no recorded failure
has reached yet: `internal/beads` `client_test.go` (a stub `bd` script), and in
`internal/execution` `process_test.go` (two), `agentrole_test.go`,
`environment_test.go`, `process_group_unix_test.go`, and
`gitmaintenance_test.go` (two). Git reached sixty-six seconds under a loaded
suite, so thirty is within reach. They are named for admission rather than
converted, because the item is the sub-second budgets and each has to be
checked for whether its budget is what it is about. The seven-second budgets
in `internal/gitworktree`'s load tests are the scaled Git budget under test,
and `internal/research`'s five seconds is a figure in a message from a fake
process.

**Done, by yoyodyne-ifd.429.36.** Each of the nine was checked, and in none of
them is the budget what the test is about: each asserts what a process saw,
printed, or left behind. A tenth of the same class was added to them:
`TestAChangeDeletingAFileLargerThanTheBoundIsApprovable` in `internal/review`,
whose one-file checkout ran past the Git manager's load-scaled thirty seconds
under a full suite and passed in under a second on its own. All ten were
converted as `internal/gitworktree` and `internal/orchestrator` were by
[yoyodyne-ifd.429.11](yoyodyne-ifd-429-11-git-budget-under-load.md): a budget
of ten minutes, which catches only a process that has hung and sits under the
Makefile's `TEST_TIMEOUT`, so the hang is still reported as the command it was.
That is `testProcessBudget` in `internal/execution`, `conformanceTimeout` in
`internal/beads`, and `testGitBudget` in `internal/review`.
