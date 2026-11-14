# yoyodyne-ifd.429.75: the two document-run tests that failed intermittently

Two tests in `internal/orchestrator` were reported as failing now and then, on
changes that could not have affected them. Both failures came from test code
that had already been replaced by the time this item ran, and the tests as they
stand on main wait on the events they check. This records the cause of each and
what replaced it, so the next intermittent failure in these tests is not taken
for the same one.

## The document-wait restart test

`TestADocumentThatFoundEveryLandingTakenWaitsThroughARestartAndLandsWhenOneFrees`
(`documentwait_test.go`) failed on GitHub's run of the checks for pull request
1203, the checks-on-CI design, whose checks had passed locally at 09:34 PDT on
October 12, 2026. It said the waiting design and the ready item started in the
wrong order.

**Cause.** The test then asserted the order in which the two runs reached the
test's fakes. The scheduler chooses the waiting document first and the ready
item second, and then starts each run in a goroutine of its own, so which of the
two reaches its fake first is up to the Go scheduler, not the code under test.
On a busy machine the ready item sometimes arrived first.

**What replaced it.** The publication-deadline fix (yoyodyne-trt, commit
`cd4bc6ab`, 11:50 PDT the same day) changed the assertion to read the order the
scheduler chose from its own record of what it started, `schedule.Started`,
which it appends to as it chooses, before any run begins, and to require
separately that each run reached its fake exactly once. Pull request 1203 was
built on a base from before that commit, which is why it still met the old
assertion.

## The stop-request deadline test

`TestAStopRequestStillEndsADocumentRunPastItsMessageDeadline`
(`documentdeadline_test.go`) failed once in a full race run on a machine at load
14 to 17 on 16 cores, during the run on yoyodyne-ifd.430.24, which ended at
12:23 PDT on October 21, 2026. The run succeeded when the test wanted it
stopped.

**Cause.** The document's check was then `touch started; sleep 2`. The test
waited for the `started` file and then recorded the stop request, and the run
noticed the stop at its next step after the check. If the test's goroutine took
longer than two seconds to see the file and record the stop, the check had
already finished and the run went on to land, with no stop yet recorded.

**What replaced it.** The governed-document change (yoyodyne-ifd.174, commit
`dd16c6d7`, 20:12 PDT on October 21) made the check wait for a second file,
`started.released`, that the test writes only after it has recorded the stop.
The stop is therefore always on record before the check can end. The check also
ends by itself after fifty seconds, inside the run's one-minute check limit, so
a test that never acts still lets it finish; that bound is the work's own end,
not something the outcome races against.

## Verified on this item

Both tests passed 20 times out of 20 under the race detector, on their own and
again beside two concurrent race-detector runs of every scheduling and document
test in the package, with the machine's load average between 13 and 36. No test
or assertion was changed.
