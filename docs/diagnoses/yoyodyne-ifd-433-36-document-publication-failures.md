# yoyodyne-ifd.433.36: why architect document publications failed make test or ended cancelled

Architect document publications failed in two ways that nobody had explained.
Seven pull requests failed `make test`, and about ten publication runs were
recorded as cancelled. This diagnosis gives the cause of each, says whether the
fix for the starting message's time limit (yoyodyne-trt) held, and says which
checks and which build the publication that failed at 03:22 PDT on October 13
ran on.

All times are Pacific (PDT). Everything here was read from the harness's own
records on October 13: the run records and their event logs under the
product's `runs` directory, the scheduler's log, and the architect's
conversation log (`chat-a06587022b9caf04cbbb20d8f6fe8c13`). Build and commit
ancestry was read with `git merge-base --is-ancestor`.

## The answer

- **All seven `make test` failures were faults in the documents' wording, not
  flaky tests and not out-of-date bases.** Every one failed
  `TestThisRepositoryCoinsNoCompoundNothingDefines` in `internal/terms`: the
  document used a hyphenated word that `docs/terms.md` does not define. One
  also failed `TestThisRepositoryOwnCoinedTermsAreRegistered`. All seven were
  written before the check that refuses such words when a role writes a
  document (`internal/documentcheck`, yoyodyne-7my) merged at 20:35 PDT on
  October 12 in pull request 1212.
- **Nine of the ten cancelled runs were stopped by a restart onto a newly
  deployed build.** The watch session hosting each run waits 15 minutes for its
  runs to finish before restarting; the full `make test` took 20 to 30 minutes,
  so a run whose checks were going when a build was deployed was stopped. Each
  record names the restart.
- **The tenth, pull request 1218, was stopped by the same kind of restart, and
  its record does not say so.** It ran inside the architect's own scheduled
  pass rather than as a run the session hosts directly, and the session stopped
  that pass with no reason attached. This change fixes that; see
  [the tenth cancelled run](#the-tenth-cancelled-run-pull-request-1218).
- **The deadline fix held.** The first publication after it merged was stopped
  by a restart before its checks finished, but its retry ran the full checks
  for over 30 minutes and merged.
- **The 03:22 PDT publication on October 13 ran the full checks, started by
  build 68888e37.** It started eight minutes before the configuration's list of
  document checks merged, so its configuration had no such list.

## 1. The seven pull requests

| Pull request | Run | Started | Document | What `internal/terms` refused |
|---|---|---|---|---|
| 1082 | run-5a511db9 | Oct 7 07:18 | artifact-contract | owning-role, amendment-decision, decision-reason, authority-policy, expected-state, storage-recovery, effective-decision, fundamental-intent, unknown-basis, identical-operation, incompatible-reader, malformed-log |
| 1113 | run-c59b1fcb | Oct 8 13:32 | stopped-run-recovery | fresh-health, handed-back, decided-recovery, recovery-contract; and "usage window", a coined term with no entry |
| 1168 | run-65618c86 | Oct 11 03:48 | configurable-workflows | execution-profile |
| 1189 | run-ec17bbb1 | Oct 12 02:53 | recoverable-and-terminal-failures | load-scaling |
| 1191 | run-75f2c4e2 | Oct 12 03:41 | checks-on-ci | heavy-check |
| 1192 | run-b47c7414 | Oct 12 04:11 | document-landing-route | heavy-check |
| 1199 | run-a1962d45 | Oct 12 06:54 | needs-design-gate | needs-design, listing-timeout |

Every run's base commit was its build's own commit, so none was out of date,
and every failure names a line of the document being published. Fed the exact
documents published by pull requests 1191 and 1199, the write-time check
(`documentcheck.Check`, run against each base commit's tree) refuses the same
words.

## 2. The cancelled runs

| Pull request | Run | Started | Stopped | Document | What stopped it |
|---|---|---|---|---|---|
| 1150 | run-2d4641dc | Oct 10 02:35 | 03:07 | stopped-run-recovery | restart onto a new build |
| 1151 | run-0eca69fa | Oct 10 02:40 | 03:07 | integration-through-a-merge-queue | restart onto a new build |
| 1170 | run-c7364583 | Oct 11 05:21 | 05:46 | configurable-workflows | restart onto a new build |
| 1172 | run-d3b9ea29 | Oct 11 06:31 | 07:08 | configurable-workflows | restart onto a new build |
| 1173 | run-a1a35cba | Oct 11 07:49 | 08:19 | configurable-workflows | restart onto a new build |
| 1188 | run-cbffaa18 | Oct 12 02:18 | 02:53 | authority-by-capability | restart onto a new build |
| 1190 | run-cfd4b347 | Oct 12 03:40 | 04:11 | authority-by-capability | restart onto a new build |
| 1197 | run-38406e04 | Oct 12 05:57 | 06:12 | needs-design-gate | restart onto a new build |
| 1202 | run-32ee66c0 | Oct 12 09:00 | 09:34 | recoverable-and-terminal-failures | restart onto a new build |
| 1218 | run-1f08478a | Oct 13 00:41 | 00:51 | document-landing-route | restart onto a new build; the record does not say so |

The first nine records read "the watch session hosting this run stopped it to
restart into the build deployed over it, after draining for its bound of
15m0s". Each was stopped during `make test`.

This no longer threatens document publications. Since 03:43 PDT on October 13
they run only the configured document checks (`document_checks` in
`.yoyodyne/config.yaml`), which took between 3 and 31 seconds on every
publication recorded that day.

### The tenth cancelled run: pull request 1218

A build was deployed at 00:36 PDT on October 13, and the session on the old
build (ed91e35b) began its 15-minute wait to restart. At 00:41 the architect's
scheduled pass, still running in that session, published a retry of
document-landing-route as run-1f08478a. At 00:51 the wait ran out. The session
stopped its hosted runs with the restart as the reason, and stopped the
architect's pass too, but with a plain cancellation. The publication inherited
the pass's cancellation, so its record says only "make test was cancelled after
9m27s because the run it belonged to was stopped".

**Fixed here.** The session now stops its scheduled passes with the restart as
the reason, as it already did for its runs (`internal/orchestrator`,
`recurringFirings.cancel` and its two callers in `schedule.go`). A publication
inside a pass reads that reason, so it is ended the way the nine above were:
cancelled, with the restart named, and tried again. The test is
`TestAFiringStoppedForARedeployCarriesTheRestartToWhatRunsInsideIt`.

**Not fixed here.** The pass started a new publication five minutes into the
session's wait to restart, which left it ten minutes to run a 30-minute check.
Whether a session waiting to restart should start new publications is
separate work.

## 3. Whether the deadline fix held

The fix for the starting message's time limit merged on October 12 (pull
request 1201, yoyodyne-trt): at 14:40 PDT by its merge commit, 14:53 PDT by the
work item. Before it, three publications
that day (pull requests 1206, 1207 and 1209, runs on builds 99fdabb4 and
d19d1b7b, which do not contain the fix) were stopped after about 14 minutes
when the message that started them reached its deadline.

- The first publication opened after it was pull request 1218, run-1f08478a,
  at 00:41 PDT on October 13 on build ed91e35b, which contains the fix. **It
  did not run its checks to completion:** the restart described above stopped
  it after 9 minutes 27 seconds of `make test`.
- Its retry, pull request 1219, run-8a7c2586, started at 01:27 PDT on build
  68888e37. It ran `make fmtcheck`, `make test` for 30 minutes 35 seconds,
  `make race` and `make vet`, all passing. It was approved and merged. That is
  well past the point where the deadline used to stop publications, so the fix
  holds.

## 4. The publication that failed at 03:22 PDT on October 13

This was run-1e6c369b, publishing observability-and-dashboard as pull request
1220.

- **It ran the full checks, not the document checks.** `make test` ran for 24
  minutes 58 seconds and failed `TestThisRepositoryCoinsNoCompoundNothingDefines`
  on "command-line".
- **Build 68888e37 started it.** That is the build in its run record, and the
  architect's conversation reported the same build from 02:15 PDT.
- **Why the full checks.** It started at 02:57 PDT. The configuration's list of
  document checks merged at 03:05 PDT (pull request 1217, yoyodyne-fp3.1.1,
  which changed only `.yoyodyne/config.yaml`). The run was pinned to the
  configuration it started with, cfg-0432ab560bb6, which had no such list. The
  next publication, at 03:43 PDT on the same build, was pinned to
  cfg-39ec48637f06 and ran only the document checks, so the build could read
  the setting. The dashboard's older build (dd70b621) did not take part: the
  dashboard does not start publications.

The same day's 04:29 PDT failure was run-03c01254, heavy-check-capacity, pull
request 1223. It ran only the document checks, for 9 seconds, and failed on
"measured-limits" and "starving".

### Why these two got past the write-time check

Both documents were written before the architect's conversation had the
check. Document 773.1 (observability-and-dashboard) was written at 17:52 PDT
on October 12, before the check merged. Document 779.1 (heavy-check-capacity)
was written at 20:45 PDT, while the conversation was still on build acae66a8,
which does not contain the check; it reached build b56ba64c at about 21:40.
Both waited and were confirmed later, and nothing checked them again.

**Fixed here.** The harness now runs the same check again just before it
confirms a waiting document automatically (`confirmDocument` in
`internal/chat/documentpublication.go`). If the check refuses it, no run is
opened, and the document goes back to its author with each refused word and
its line. The test is
`TestAWaitingDocumentTheDocumentChecksRefuseGoesBackToItsOwnerWithNoRun`.

## Other causes found, and where they go

- **A flaky test.** The checks-on-ci design (run-2a0fffa7, pull request 1203)
  passed every local check at 09:34 PDT on October 12. On GitHub,
  `TestADocumentThatFoundEveryLandingTakenWaitsThroughARestartAndLandsWhenOneFrees`
  (`internal/orchestrator/documentwait_test.go:478`) failed, saying the waiting
  design and the ready item started in the wrong order. A document cannot
  affect that test. The harness took the failure as the change's own because
  main passed, withdrew the merge, and the pull request has been open since.
  Nothing open covers fixing this test; it is named for the flaky-test fixes.
  Waiting merges that GitHub fails are yoyodyne-1wp.
- **A document that removes the last use of a listed word.** Run-470a7c17
  (tool-interface, pull request 1228, 07:32 PDT October 13) failed because the
  revision removed the last use of "evidence-not-instruction", which is still
  on the terms list of words waiting to be dealt with
  (`internal/terms/inventory`). The write-time check cannot see that, because it
  reads one document, not the whole repository. This is yoyodyne-ifd.437.45.
- **The merge-queue restart tests** (yoyodyne-ifd.429.74) did not cause any of
  the failures above.
