# yoyodyne-ifd.437.84: second reading of the design documents against the code, group 2: run recovery and forward progress

A second, adversarial reading of the report `yoyodyne-ifd-437-84-2.md` beside this file. It re-read every finding there that differs or is not built against the code, tried to settle the unclear ones, checked a sample of the matches, and looked for what the first reading missed. **Where the two files disagree, this one stands.**

- **Read on:** 2026-11-21, in the evening, between 18:33 and 18:43 PST, with main at `848d86190`. The code had moved since the first reading; each finding is judged against the code as it was at this reading.
- **Read by:** a read-only reader on Opus 5.5 in the operator's assistant's session. It read code and ran nothing, tests included.
- **Checked by hand:** nothing in this file was checked again by hand.
- **How to use it:** evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Second-reading classes:** `CONFIRMED`, the first finding is right as written; `CORRECTED`, its gist is right and a detail is wrong; `WRONG`, the design and the code agree or the finding misread one of them; a class in the first reading's own words where an unclear finding was settled.

Read against main at `848d86190` on 2026-11-21. Read only: Read and grep, nothing run.

## Summary
- Findings re-read: 24 (4 DIFFERS, 20 NOT BUILT). CONFIRMED 16, CORRECTED 8, WRONG 0, CANNOT TELL 0.
- Unclear findings settled: 6 of 7 (G2-32 still unclear).
- MATCHES findings found wrong: G2-16 and G2-39 (the fifteen-minute lease bound); G2-24 and G2-50 are only partly right.
- Verdicts changed: none. Each verdict holds, but forward-progress and recoverable-and-terminal-failures each have built or unbuilt parts that the first reading put on the wrong side (below).
- Findings the architect should not act on as written:
  - G2-1: the gap is narrower than stated. A stop that records no reason is already `StopUnknown`, which is outside the work and not charged. Only a reason string this build does not recognise is charged as about the work.
  - G2-2: "three designs say thirty minutes" is wrong. Two documents say it, in three places (recoverable-and-terminal-failures 96 and 189, stopped-run-recovery 156).
  - G2-3: point 4 of forward-progress is not entirely unbuilt. Act 2, releasing a hold when its follow-up check passes, is built. The harness also already continues stalled runs and check stages on its own, without asking whether she is available.
  - G2-6a: a cross-run grouping of stops does exist under another name: the program manager's stop-cause tally. It is not the design's grouping.
  - G2-16 / G2-39 (MATCHES): wrong. `promotionQueueWait` (`internal/runstate/promotion.go:33-41`) limits how long a run waits for its turn at the lease. It does not limit waits taken while holding the lease. recoverable-and-terminal-failures 189 itself calls this an order not yet shipped.
  - Overlap note on recoverable-and-terminal-failures 92 against stopped-run-recovery 178: not a contradiction. Line 92 is about whether a failure at a boundary is retried, not about how a stop is charged.
- Most important misses:
  - A stop whose retry window ran out at the tracker-write or catch-up boundary is decided as about the work and charged (`runstop.go:81-96` with `stopcause.go:222-238`). That breaks rule 5 and recoverable-and-terminal-failures 176-177.
  - A `run.recover` continue keeps its own copy of the resume rules (`internal/orchestrator/recoveryrequest.go:61-72`) instead of calling `resumable()` (`resumable.go:50`). That breaks stopped-run-recovery rule 2.
  - The harness's own continues do not go through `run.recover`, which fresh-factory 252 ("There is one carrier") forbids.
  - Only 2 of the 16 named follow-up checks can be run (environment, and project-declared commands, of which none are declared). No status or health surface reads the follow-up store.
  - The run on the base commit (build 4) is not built: `StopBaseFailing` is defined, but no code ever sets it.

## stopped-run-recovery.md
Verdict: agree (partly stale). Addition: steps 6 to 8 are largely built through the older triage path. The items narrowed into them are closed: `.428.53`, `.428.41`, `.429.68`, `doa`, `.428.99.1`, `.428.99.10`.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G2-1 | DIFFERS | CORRECTED | `runstop.go:81-96` returns `StopCauseWork` only for a class that `stopSideListed` does not name (`stopcause.go:118-138`). A record with no reason becomes `StopUnknown` (`stopclass.go:168-176`). That class is listed outside the work (`stopcause.go:127`), so it is not charged. The harness's own failures (`StopHarness`, `StopRecording`, `StopCleanup`, `StopDeadClaim`, `StopCancelled`, `StopPublish`) are outside the work too, which matches line 179. What remains is an unrecognised reason string, which is charged and flagged `Unrecognised` (`runstop.go:129-131`). That is open item `.428.99.28`. |
| G2-2 | DIFFERS | CORRECTED | Gist holds: there is one 2-hour `Window` (`internal/recovery/recovery.go:40`), and the provider boundary uses it (`internal/orchestrator/recovery.go:491`, `pipeline.go:5326`). Detail: the thirty minutes appears in two documents, not three. |
| G2-6 | NOT BUILT | CONFIRMED | `RunStop` (`runstop.go:107-135`) has no record of what failed. Nearest relative: `StopCauseKey` (cause, side, step) at `stopcause.go:259-283`. It is the program manager's recurrence key, has no item and no failing check, and is not used to refuse anything. |
| G2-7 | NOT BUILT | CONFIRMED | Refusals are listed at `internal/runrecover/runrecover.go:256-265`. None of them refuses the same act on the same failure. |
| G2-8 | NOT BUILT | CORRECTED | `RecordOnce` is called from 8 sites, not 3: `triage.go:368,958,998,1122,1150,1179`, `productdecision.go:89`, `mergedisagreement.go:357`. Slice 2 (`.428.99.3`) is blocked; slice 3 is open. |
| G2-9 | NOT BUILT | CONFIRMED | Only the two holds call `Attach` (`orchestrator/followups.go:61,95`). Nothing sets `KeepsStopListed` outside `followup.go:334`. A wait through `run.recover` is `WaitRequest{On}` alone (`runrecover.go:91-93`): no check, no owner, no deadlines. |
| G2-10 | NOT BUILT | CONFIRMED | The role check is at `recover.go:87`. Neither `harness_recovery_per_hour` nor any unavailability test exists anywhere in the code. |
| G2-11 | NOT BUILT | CONFIRMED | `notBuilt` is at `recover.go:65-70`. `RecoverHold` remains (`runstate/recovery.go:43`). |
| G2-16 | MATCHES | WRONG | `promotionQueueWait` limits the wait *for* the promotion lease (`promotion.go:33-41`, used at `:76`). In a forge outage the lease is given back for the wait, and the wait is unbounded (`orchestrator/forgewait.go:20-36`). Other boundaries may still wait their 2-hour window while holding the lease (`forgewait.go:6-7`). |
| G2-18 | UNCLEAR | Step 5 DIFFERS in part; steps 6-8 MATCH | Step 5: one predicate list serves `Pipeline.Run` (`resumable.go:15-57`; `0rm` closed). But `RecoveryContinuable` keeps its own copy of that list plus its own phase rules (`recoveryrequest.go:61-72`). The integration resumer keeps another (`integrationresume.go:532`). Kept check results are built (`keptchecks.go:210`). Step 7: claim given back (`runstate/rerun.go:190-214,699`). Step 8: a refused merge keeps its record (`recoveryrequest.go:141-145`; `doa` closed), but the fifteen-minute bound is not built (see G2-16). |

Checked MATCHES with no problem found: G2-12, G2-13 (`stopcause.go:140-205`), G2-14 (`pipeline.go:3294`), G2-15, G2-17.

### Missed by the first reading
- **Rule 5 (146) against window stops.** A run whose window ran out at `RetryTrackerWrite` or `RetryCatchUpTarget` (`state.go:3448-3466`) stops with `StopRecoveryWindow`. `StopKeptOutOfHoldAfter` keeps that out of the hold only at the forge and provider boundaries (`stopcause.go:222-238`), so `StopCauseOf` decides it as about the work and charges it (`runstop.go:92-96`).
- **Rule 2 (143) against `recoveryrequest.go:51-72`.** A continue decision applies its own copy of the resume predicates rather than calling `resumable()`. The design says "None decides it its own way".
- **Step 8 (344) and test line 414** ("no lease is held past fifteen minutes"). Not built, as described under G2-16.

### Contradictions with other designs
- recoverable-and-terminal-failures 177 says a run stopped by any boundary's window "counts toward nothing" the brake counts. This document lists only the forge and provider windows (156) and says the hold leaves out exactly that list (180). The code follows this document.
- Line 190 and 313 say the harness continues a run itself with no decision. fresh-factory 258 says the harness acts only while the development manager is unavailable. The code continues stalls and check stages unconditionally (`orchestrator/stallcontinue.go:16-21`).
- The first reading's claimed contradiction between recoverable-and-terminal-failures 92 and line 178 is not one (see Summary).

## fresh-factory-health-and-preserved-run-recovery.md
Verdict: agree (partly stale).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G2-19 | NOT BUILT | CONFIRMED | `Request` (`runrecover.go:96-108`) has no field naming who acts, and `recover.go:87` refuses every caller but the development manager. |
| G2-20 | NOT BUILT | CORRECTED | Mostly right. Item states, cross-item failure, role unavailability and tracker drift are all absent (`health.go:207-246`). But "the latest pull's reason" exists in reduced form: one empty-slot cause per pull, not one per ready item (`observe.go:438-453`). |
| G2-21 | UNCLEAR | DIFFERS (small) | Built. Each unreadable run is a factory problem of kind `unreadable-run` owned by the development manager (`readmodel/unreadableruns.go:49-50`, `ownership/registry.go:214`), and health reports it as `ClassFactoryProblem` (`observe.go:433`). It names the file and the error, not the size or last-change time (`unreadableruns.go:31-35`). |
| G2-22 | NOT BUILT | CONFIRMED | No `idle_slot_after`. Nothing files a bug. |
| G2-23 | NOT BUILT | CONFIRMED | `recover.go:65-70`. The document says the same. |
| G2-24 | MATCHES | Partly | The list matches, with the extra "unresolved hold recorded" (`runinspect.go:61-75`). But "wait recorded" carries no kind: line 235 requires a request to name which kind of wait it expects, and nothing in `runinspect.go` does. |
| G2-26 | MATCHES | MATCHES | `health.MaxAge = 15m` (`health.go:57`), checked at `recover.go:174`. |
| G2-29 | UNCLEAR | MATCHES | `health.follow` is `FollowDescriptor` (`health.go:192-197`), granted at `chat/role.go:268`. |

### Missed by the first reading
- **"There is one carrier" (252).** The harness's own continues (`stallcontinue.go`, `checkstagecontinue.go`) close docket entries without going through `run.recover`.
- **The wait action (243).** Through `run.recover`, a wait can only be a dependency wait (`runrecover.go:91-93`). It has no owner, check, deadlines, or choice of whether the stop stays listed.
- **"overdue follow-ups" (163).** Health never reads the follow-up store. The only follow-up text it shows is a recovery hold's free-text field (`observe.go:291`).

### Contradictions with other designs
- "Who acts" (258) against stopped-run-recovery 190 and 313: see above.
- 266 says releasing a hold is not a `run.recover` call. forward-progress 217 says each of the harness's five acts "is already on the recovery tool's closed list".

## forward-progress.md
Verdict: agree (mostly a plan). But the report's "no piece 1 to 8 is built" understates what exists: act 2, unreadable records not stopping a listing (`6d1` and `d56` closed), and slot release for the CI, forge-outage and merge-queue waits.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G2-3 | NOT BUILT | CORRECTED | The unavailability test, acts 1, 3 and 5 under it, and the bounds are absent. Act 2 is built (`orchestrator/followups.go:1-25`, item `432.25.19` closed). The harness's own stall continue already obeys "one act per stop" (`runstate/stallcontinue.go:39`) without any unavailability test. |
| G2-4 | DIFFERS | CORRECTED | The cause set is five plus "not known" (`ownership/emptyslot.go:20-46`); correct. "No threshold" is too strong: there is a 5-minute wait before the forge-checks cause is raised (`readmodel/emptyslots.go:119`) and a 2-minute grace for a slot still counted (`orchestrator/emptyslots.go:33`). There is no general ten-minute threshold and no bug is filed. |
| G2-6a | NOT BUILT | CORRECTED | The design's grouping (four parts, three items in 24 hours, blocks the harness's own acts) is absent. A related grouping exists: the program manager tallies stops across runs by cause, side and step, and reports again after `DefaultStopCauseRecurrence = 3` (`stopcause.go:50,259-283`). |
| G2-30 | NOT BUILT | CONFIRMED | No state computation anywhere: no "waiting for capacity" and no "no recorded way forward". |
| G2-31 | UNCLEAR | DIFFERS in part | A run waiting on CI checks, on a forge outage, or in the merge queue holds no slot (`runstate/state.go:6320-6342`). A run at the landing step itself still holds its slot, and resuming a stopped landing waits for a free slot (`githubidentity.go:196`). |
| G2-32 | UNCLEAR | STILL UNCLEAR | A run of this session ending wakes the pull at once (`schedule.go:2353-2378`). I found no wake on a hold being released, an item becoming ready, or a provider becoming usable. I did not trace the poll interval's default. |
| G2-33 | MATCHES | MATCHES | `config.go:612,831`. |

### Missed by the first reading
- **Point 4 framing (217)** says the harness acts only while she is unavailable. The code continues stalls and check stages without asking (`stallcontinue.go:16-21`).
- **Point 3, "One bad record does not stop recovery"**, is built (`readmodel/unreadableruns.go:10-18`). The report implies nothing in point 3 is.

### Contradictions with other designs
- 217 ("Each is already on the recovery tool's closed list") against fresh-factory 266.
- The states (90-118) duplicate fewer-lifecycle-states 63-73, which the first reading noted. fewer-lifecycle-states 111 agrees on "five acts".

## recoverable-and-terminal-failures.md
Verdict: agree (partly stale). Three orders are not shipped, not two: the provider window, the fifteen-minute lease bound (its own line 189 says so), and untouched-tests builds 4 to 6.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G2-2 | DIFFERS | CORRECTED | As above. |
| G2-36 | DIFFERS | CONFIRMED | "Brake" appears at 148, 169 and 177-180. The other documents say "hold on new work". |
| G2-39 | MATCHES | WRONG | Should be NOT BUILT; see G2-16. |
| G2-42 | UNCLEAR | NOT BUILT | `internal/budget` decides only management-conversation turns (`budget.go:1-12`). Its callers are `chat/chat.go` and `chat/compact.go`. No boundary retry, repair, rerun or harness continue uses it. The document's own clause "until a boundary has moved, its own code decides" covers this. |
| G2-43 | UNCLEAR | MATCHES | `runstate/checkstagecontinue.go:43` (`MaxCheckStageContinuations = 2`) and `:49` (`CheckRerunAllowanceFactor = 3`). |
| G2-44 | NOT BUILT | CORRECTED | Build 4 (the run on the base commit) is not built either: `StopBaseFailing` exists only in `runstate/stopclass.go` and `stopcause.go`, and no code in `internal/orchestrator` sets it. A passing rerun is recorded on the run (`runstate/untouchedrerun.go:98-103`), but no read model counts them. |

Checked MATCHES with no problem found: G2-37, G2-38 (`checks/touched.go:35`, `untouchedrerun.go:37`), G2-40, G2-41.

### Missed by the first reading
- **176-177 against `stopcause.go:222-238`.** A window-spent stop at the tracker-write or catch-up boundary is counted toward the brake and charged (see stopped-run-recovery).
- **181, the lease bound.** Not built, and the forge-outage path does something different (`forgewait.go:20-36`).

### Contradictions with other designs
- 177 against stopped-run-recovery 156 and 180, as above.

## follow-ups.md
Verdict: agree (mostly a plan).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G2-5 | NOT BUILT | CONFIRMED | `Attach` has two callers: `orchestrator/followups.go:61,95`. |
| G2-45 | NOT BUILT | CONFIRMED | The deadline fields are written at `checkrun/environment.go:224-225`, as fixed 1 hour and 4 hours (`:197-198`). Nothing reads them. |
| G2-46 | NOT BUILT | CONFIRMED | `checkrun/runner.go:26-34` says the settings are not read yet. |
| G2-47 | NOT BUILT | CONFIRMED | No `follow-up` command in `internal/cli`. |
| G2-48 | NOT BUILT | CONFIRMED | No 30-minute reminder found in `internal/notify` or `internal/slack`. Item `432.25.18` is open. |
| G2-49 | NOT BUILT | CONFIRMED | No migration found. |
| G2-50 | MATCHES | Partly | All 16 names exist. But only environment and command checks have an evaluator (`cli/schedule.go:599-602`, `checkrun/runner.go:207-219`), and `Commands.Declared` is never filled. Every other check records "this build of the harness has no way to run the … check yet". |

Checked MATCHES with no problem found: G2-51, G2-53, G2-54.

### Missed by the first reading
- **14 of the 16 checks cannot run** (G2-50 above).
- **84 ("shown once, as its own kind, with the follow-up's check, deadlines and age beside it").** Nothing in `internal/readmodel` or `internal/health` reads the follow-up store.

### Contradictions with other designs
- 113-114 (the brake writes a follow-up with first deadline `brake_probe_overdue_after`) agrees with fewer-lifecycle-states 144. Neither is built. No contradiction found beyond those listed above.
