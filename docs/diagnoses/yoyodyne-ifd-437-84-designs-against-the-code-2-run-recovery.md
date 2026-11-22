# yoyodyne-ifd.437.84: design documents against the code, group 2: run recovery and forward progress

One of nine reports comparing the design documents in `docs/designs/` with the code, made at the operator's request of 2026-11-21 for a review of every design against the current code. This one covers group 2, run recovery and forward progress.

- **Read on:** 2026-11-21, between 15:40 and 15:48 PST. Main moved from `2447eeaab` to `fe2db3d37` at 15:41, a minute into the reading; that change was 22 files, mostly the team feedback repair code in `internal/runstate`.
- **Read by:** a read-only reader in the operator's assistant's session, on Sonnet 5.5. It read code and ran nothing, tests included.
- **Checked by hand:** the operator's assistant checked three findings against the code. G2-1, G2-2 and G2-3 held. Every other finding is unchecked.
- **How to use it:** this is evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Classes:** `MATCHES`, the code does what the design says; `DIFFERS`, the code does something else; `NOT BUILT`, the design calls it future or the code is absent; `UNCLEAR`, not established.

## Summary
- Documents: 5. Claims checked: about 55 (counts are approximate). MATCHES 17, DIFFERS 4, NOT BUILT 27, UNCLEAR 7.
- Overall shape: these five are mostly a plan with a built core. Built: the run stop record (three causes), the hold-count sorting, the docket logs, `run.inspect` / `run.verify` / `run.recover` for the development manager only, worker identity, the follow-up record and check runner (used for two holds only), the lost-race limit, the raise limits. Not built: what-failed identity, the third-act refusal, run records replacing the docket logs, follow-ups for wait decisions, the harness acting for an unavailable development manager, item states, the follow-up deadlines and escalation, most of forward progress.
- Mismatches that matter most:
  - G2-1 [DIFFERS] An unrecognised stop reason: design says outside the work and no attempt charged; code decides it as about the work and charges it (open item yoyodyne-ifd.428.99.28).
  - G2-2 [DIFFERS] The provider boundary's window: three designs say thirty minutes; code still uses the two-hour window for it.
  - G2-3 [NOT BUILT] The harness acting for an unavailable development manager (`run.recover` refuses everyone but her; `execution.harness_recovery_per_hour` and `execution.idle_slot_after` exist nowhere in code or docs/configuration.md).
  - G2-4 [DIFFERS] Why a slot stands empty: forward-progress names a closed list of nine reasons, a ten-minute threshold and a filed P0 bug; code has a different five-cause list, no threshold and no bug.
  - G2-5 [NOT BUILT] Waits as follow-ups: the wait decision, parking, the brake, dependency waits and unknown-result waits do not write follow-ups; deadlines are stored but nothing acts on them.
- Consolidation proposals:
  - Mark each document's built and unbuilt parts with one status line per section, so a reader can tell the shipped rules from the plan. Today all five read as if shipped.
  - Move the shared "what failed" identity and the "third use of the same act" rule to one place (stopped-run-recovery step 1 and 3). forward-progress, fresh-factory and stopped-run-recovery each restate it in full.
  - Move the harness-acts-for-the-development-manager rule to one place (forward-progress point 4). fresh-factory "Who acts" and stopped-run-recovery step 5 restate its list and bounds.
  - Cut the ruling-by-ruling history paragraphs ("Ruled on October 27/29, November 18/19") out of stopped-run-recovery step 1 and into a short rules list.
  - Pick one name: "hold on new work" or "brake" (recoverable-and-terminal-failures and follow-ups say brake; stopped-run-recovery says hold; code says `brake_probe_overdue_after` and "hold" both).
  - Move the "persona rules, word for word" section of fresh-factory out; the rules are shipped in `internal/chat/role.go` and the persona files, so the text is a second copy that will drift.

---

## stopped-run-recovery.md — Stopped run recovery
Verdict: partly stale (steps 1 to 4 describe a plan whose slices 2 to 7 are open; step 1 and the hold list are built)
Size: 65 KB. Last changed: 2026-11-20.

### Findings
- G2-1 [DIFFERS] A stop whose reason the harness does not recognise charges nothing and is recorded as outside the work.
  - Document: lines 178-180: "A stop whose reason the harness does not recognise charges no repair attempt and no review round... So such a stop records its cause as outside the work".
  - Code: `internal/runstate/runstop.go:81-96` (`StopCauseOf`) ends with `return StopCauseWork` for any class not listed, so an unrecognised reason is about the work. `runstop.go:131` says "decided as about the work"; `internal/runstate/stopcause.go:99-102,138` (`StopSideOf` returns `StopSideWork, false`) says "an unrecognised cause is counted rather than excused". Hold counting is right (`stopcause.go:196-205` counts it). Open item yoyodyne-ifd.428.99.28 is exactly this change.
  - What to change in the document: say the charging rule is the designed target and is not built (item open), or mark lines 176-182 "as designed, not as built".
- G2-2 [DIFFERS] The provider boundary allows thirty minutes.
  - Document: line 156: "once the reissues and the thirty minutes the provider boundary allows are spent". Same claim in recoverable-and-terminal-failures lines 96 and 189.
  - Code: `internal/recovery/recovery.go:40` has one `Window = 2 * time.Hour`; `internal/orchestrator/pipeline.go:5326` and `recovery.go:491` apply it to `RetryProviderInvocation`. No shorter provider window exists.
  - What to change in the document: say the window is two hours today and the shortening is ordered but not built, or find the item that builds it (I found none open by title).
- G2-6 [NOT BUILT] "What failed" (cause, step, failing check or provider error, item) recorded on every stop, and two stops compared on those four parts.
  - Document: line 163: "**What failed** is recorded on every stop, as four things together".
  - Code: `internal/runstate/runstop.go:107-135` (`RunStop`) has reason, cause, step, candidate, kept, evidence; no what-failed field or comparison. Grep for same-failure matching finds nothing outside the designs. Planned as forward-progress build piece 5.
  - What to change in the document: mark step 1's what-failed paragraph and step 3's third-act rule as planned, depending on forward-progress piece 5.
- G2-7 [NOT BUILT] A third continue, repair or rerun on the same failure of one item is refused.
  - Document: lines 274-276 ("The same act on the same failure"), 295.
  - Code: no counter of acts per failure in `internal/runrecover/recover.go` (refusal list lines 60-70 has no such reason).
  - What to change in the document: as G2-6.
- G2-8 [NOT BUILT] The two docket logs retired in three steps; the list worked out from run records.
  - Document: lines 200-204: "Slice 2 ... writes nothing new to `docket.jsonl`".
  - Code: `internal/orchestrator/triage.go:368` and `:958`, `productdecision.go:89` still call `Docket.RecordOnce`, which appends to `docket.jsonl` (`internal/runstate/docket.go:145-171`); closures still go to `docket-closed.jsonl` (`docket.go:195-228`). Slice 2 item yoyodyne-ifd.428.99.3 is `blocked`, slice 3 (.4) open.
  - What to change in the document: the "Where stops and decisions are kept" subsection (lines 194-232) describes a future; say so, and shorten it to the rule and the order.
- G2-9 [NOT BUILT] A wait decision writes a follow-up and says whether it keeps the stop listed; the stop leaves the list only while that follow-up is open.
  - Document: lines 246-252 (step 2), 266 (step 3).
  - Code: `internal/triage/triage.go:944-985` (`Closure`) has `Decision` as a free string, `RevisitAfter`, `WaitsOn`; no follow-up id and no keeps-listed field. `followup.FollowUp.KeepsStopListed` (`internal/followup/followup.go:50`) is defined but nothing outside its own package sets or reads it. Follow-ups are written only for two holds (`internal/orchestrator/followups.go:61,95`).
  - What to change in the document: mark steps 2 and 3's follow-up rules as planned (piece 3 of follow-ups).
- G2-10 [NOT BUILT] The harness continues a run by itself while the development manager is unavailable, at most `execution.harness_recovery_per_hour` an hour.
  - Document: line 315.
  - Code: `internal/runrecover/recover.go:87-89` refuses any caller but the development manager; no `harness_recovery_per_hour` anywhere in `internal/` or `docs/configuration.md`. (The harness does continue some runs on its own through other paths, for example `internal/orchestrator/stallcontinue.go` and `checkstagecontinue.go`, which close docket entries; step 5 does not mention these.)
  - What to change in the document: mark as planned (forward-progress piece 7) and describe the continues that do exist.
- G2-11 [NOT BUILT] Repair, rerun, give-back and ask the Lead Product Manager are decisions carried through `run.recover`.
  - Document: lines 260-268, 391.
  - Code: `internal/runrecover/recover.go:63-69` (`notBuilt`) refuses all four, naming yoyodyne-ifd.428.99. `RecoverHold` ("record an unresolved hold") still exists (`internal/runstate/recovery.go:43`), which the document says is gone.
  - What to change in the document: say these four go through the older triage path until slices 3 and 4 land.
- G2-12 [MATCHES] Three causes (work, outside, uncertain): `internal/runstate/runstop.go:32-46`.
- G2-13 [MATCHES] The hold on new work counts a dead process, an unattributed cancellation, the harness's own failures, and publishing failures other than forge retries running out or a refused merge: `internal/runstate/stopcause.go:140-205`.
- G2-14 [MATCHES] Lost races have a fixed limit of five, separate from `execution.integration_retries_before_reconciliation`: `internal/orchestrator/pipeline.go:3294`.
- G2-15 [MATCHES] The development manager's five raises, the Lead Product Manager's five more, no raise past ten: `internal/runstate/triageoverride.go:133`, `internal/chat/chat.go:5766`, `internal/ownership/hand.go:133`.
- G2-16 [MATCHES] Waits taken while holding the promotion lease are bounded to fifteen minutes: `internal/runstate/promotion.go:41`.
- G2-17 [MATCHES] `docket.jsonl`, `docket-closed.jsonl` and `docket-window.json` exist per product as described: `internal/runstate/docket.go:64-75`.
- G2-18 [UNCLEAR] Steps 5 to 8 (resume function, rerun refusal rules, finishing a merge). Slices 5, 6 and 7 (items .6, .7, .8) are open; I did not trace `rerun.go` or `integrationresume.go` in detail. Related closed items (.1, .10, .20) suggest parts are built.

### Missing from the document
- The harness already continues runs without a decision at a stall and at the check stage (`internal/orchestrator/stallcontinue.go`, `checkstagecontinue.go`, `integrationresume.go`), each closing its docket entry. Step 5 describes only the future automatic continue.
- Ownership of "which entries settle themselves": open items .31 (settle an older run's entry once a later run of the item succeeded) and .32 (stop retrying a refused decision) are gaps in the same area and not named.
- `StopSide` (work, bound, outside) in code is a fourth axis beside the design's three causes; a reader of the code needs the mapping (`internal/runstate/stopcause.go:60-138`).
- The reader needs to know `run.recover` is open to the development manager only today.
- The program manager's stop-cause recurrence record (`internal/runstate/stopcause.go`, `stop-causes.jsonl`) sits on the same stop records and is not mentioned.

### Overlap with other documents
- Step 1 "what failed" (163) and step 3 (274-276) repeat forward-progress lines 170-186 almost word for word. They agree.
- Step 5 (315) repeats forward-progress "The bounds" (244-250) and fresh-factory "Who acts" (256-269). They agree, but the bound list is stated three times.
- Step 3's list of decisions (260-268) repeats fresh-factory's `run.recover` list (~line 235-247). They agree, and both say the closed lists must stay equal.
- Unrecognised-cause handling: stopped-run-recovery 178 says outside the work, no charge; recoverable-and-terminal-failures line 92 says an unrecognised failure "is treated as an answer and reported" (an answer goes to the party whose answer it is, which is the developer). These two disagree on who gets it. Pick one.
- Step 1's check-time-limit rule (156) restates recoverable-and-terminal-failures "The checks and the review"; agree.

### Could be cut or moved
- Frontmatter revisions: eleven long `reason` paragraphs (lines 9-60) and thirteen approval blocks (61-126) are history; move out.
- Lines 159-161 and 176-182 (the October 27, October 29 and November 18 rulings) read as incident narrative; fold into the rule they changed.
- Lines 194-232 (docket logs retired): large, mostly describes unbuilt slices; reduce to the rule, the order, and "must never".
- Lines 348-373 ("Open items in this area") list item ids and report ids; keep only items still open.
- The "Alternatives rejected" at 184-188, 224-230 and 416-428 are long; keep one line each.

---

## fresh-factory-health-and-preserved-run-recovery.md — Fresh factory health and preserved-run recovery
Verdict: partly stale (the lifecycle and `run.*` tools are built; forward-progress additions and the harness as actor are not)
Size: 60 KB. Last changed: 2026-11-20.

### Findings
- G2-19 [NOT BUILT] The harness acts through `run.recover` while the development manager is unavailable, for four acts, within bounds.
  - Document: lines 256-269 ("Who acts"): "A `run.recover` request names who is acting: the development manager, or the harness."
  - Code: `internal/runrecover/recover.go:87-89` refuses any role but the development manager; `Request` has no who-acts field to the best I could see (`runrecover.go`). No unavailability test exists (grep for development manager unavailable finds only a string in `internal/runstate/checkstagecontinue.go:306`).
  - What to change in the document: mark "Who acts" and slice-list notes as planned, depending on forward-progress pieces 6 and 7.
- G2-20 [NOT BUILT] `health.observe` reports item states, an item in no state, the same failure on three or more items, role unavailability, and tracker-copy drift.
  - Document: lines 160-171 (the bullet list under "Fresh health delivery").
  - Code: `internal/health/health.go:207-246` lists the classifications; none of these exists. Only the empty-slot report (`ClassEmptySlot`, `observe.go:438-453`) is there.
  - What to change in the document: split the bullets into built and planned.
- G2-21 [UNCLEAR] A run record that cannot be parsed is reported on every observation as a development manager problem naming file, size, time and error (lines 175-181, ruled November 15). I found no such classification in `internal/health/*.go`; `observe.go:655-660` handles unreadable pass records only. The run listing may report it elsewhere (items yoyodyne-6d1, yoyodyne-d56); I did not trace it.
- G2-22 [NOT BUILT] `execution.idle_slot_after` and the filed P0 bug for a slot empty beside ready work (line 170).
  - Code: no `idle_slot_after` in `internal/` or `docs/configuration.md`; `internal/readmodel/emptyslots.go:40` reports the empty slot as a problem with its age, and nothing files a bug. See G2-4.
- G2-23 [NOT BUILT] Repair, rerun, give-back, ask via `run.recover` (lines 238-246, slice 3 line 380). Document says plainly these are refused as not yet built: `internal/runrecover/recover.go:63-69`. Matches the document's own account.
- G2-24 [MATCHES] `run.inspect`, `run.verify` and `run.recover` exist with the closed lists; `run.verify` states match except it still has "unresolved hold recorded" (`internal/runinspect/runinspect.go:62-71`), which the document says is folded into the wait. The document says this holds until piece 4 of fewer-lifecycle-states (line 248).
- G2-25 [MATCHES] Worker identity uses the program start record (`internal/runstate/program.go`, `worker.go`).
- G2-26 [MATCHES] Observations older than fifteen minutes are refused: `internal/runrecover/runrecover.go:233` and checks in `recover.go`.
- G2-27 [MATCHES] The shared rule and the development manager's rules are in `internal/chat/role.go` and `.yoyodyne/personas/development-manager.md` (also `internal/config/builtin/v1/personas/development-manager.md`).
- G2-28 [MATCHES] The release action exists for a finished run (`internal/runstate/recovery.go:45`).
- G2-29 [UNCLEAR] `health.follow` (line ~232): I did not find where it is registered; not checked beyond `internal/health`.

### Missing from the document
- Frontmatter title is "Fresh factory health and preserved-run recovery" but the file's first heading is "# Purpose and scope", so the inventory and any index read the title as "Purpose and scope".
- Health classifications that exist in code but are not named: held after its run stopped, kept checkout, factory problem (`internal/health/health.go:207-246`).
- The addendum's slice list does not say which of slices 1 to 6 are built; slices 1, 2 and 3 (partial) are.

### Overlap with other documents
- "Who acts" (256-269) duplicates forward-progress point 4 (203-260); they agree on the five acts but this one lists four and says the fifth (release a hold) is the follow-up runner's. forward-progress lists five including "Record a wait that keeps the stop listed", and "Release what a finished run holds". Counts differ (forward-progress "five acts" vs this "these four"). Align.
- The worker-liveness rule is also cited by forward-progress ("by the one rule for identifying a worker", line ~92); agree.
- Unknown result puts a wait keeping the stop listed: here 248-250, stopped-run-recovery step 4 (303), follow-ups 99. All agree.

### Could be cut or moved
- "The rules, word for word" (lines ~336-360): copy of persona text now in code; replace with a pointer.
- "Evidence and limits of this addendum" (last 12 lines): revision history.
- Outcome scenarios (a) to (n): a test list; keep those not covered by tests, otherwise cut.
- Frontmatter revisions and approvals (lines 1-100) are history.

---

## forward-progress.md — Forward progress
Verdict: mostly a plan not built
Size: 32 KB. Last changed: 2026-11-19.

### Findings
- G2-3 [NOT BUILT] The harness acts for an unavailable development manager.
  - Document: lines 203-250: "While the development manager is unavailable, the harness takes these acts by fixed rules" and "`execution.harness_recovery_per_hour`".
  - Code: none of the unavailability test, the harness-as-actor request, nor the setting exists (`internal/runrecover/recover.go:87-89`; no hit for the setting in `internal/`, `docs/configuration.md`, or `docs/designs/portable-agent-configuration.md` section 6, which the document names as where she sets it).
  - What to change in the document: mark point 4 as not built; say piece 7 depends on pieces 5 and 6, both unbuilt.
- G2-4 [DIFFERS] Why ready work was not started; empty slot beside ready work is a defect after ten minutes and files a P0 bug.
  - Document: lines 136-156: closed list of nine reasons recorded at each pull, "execution.idle_slot_after", "the harness files a reliability bug at priority 0".
  - Code: `internal/ownership/emptyslot.go:15-39` has a different closed set (hold, rule keeping work out, slot still counted as taken, no eligible item, runs waiting on GitHub's checks, not known); `internal/runstate/emptyslots.go` records one account per poll, not per ready item; `internal/readmodel/emptyslots.go:40` reports with age and no threshold; no bug is filed (`internal/health/observe.go:438-453` adds an anomaly only).
  - What to change in the document: describe the five-plus-unknown causes that exist and say what remains (per-item reasons, threshold, bug).
- G2-30 [NOT BUILT] Every admitted item has one of five states, computed from records; an item in none is a problem (lines 90-130, 140). No state computation exists in `internal/readmodel` or `internal/health` (no hits for item state or "no recorded way forward").
- G2-6a [NOT BUILT] What failed, the third-act refusal, and the failure across items shown once (lines 170-196). See G2-6, G2-7; no cross-item grouping in `internal/readmodel`.
- G2-31 [UNCLEAR] A run waiting only to land holds no developer slot (lines 162-164, piece 4). `internal/orchestrator/githubidentity.go:196` says "run waits at landing: every developer slot is occupied", which suggests landing still depends on slot capacity; I did not read `schedule.go` further. Piece 4 itself says the code may already do it.
- G2-32 [UNCLEAR] The pull within one minute of a slot, permit, hold or provider freeing (lines 124-134). Not traced in `schedule.go`.
- G2-33 [MATCHES] `execution.brake_probe_overdue_after` exists, default 45 minutes: `internal/config/config.go:612`, `resolve.go:487`.
- G2-34 [MATCHES] Environment holds (license, search path, lapsed login) re-check through a follow-up the harness runs itself: `internal/orchestrator/followups.go:1-40`, `docs/configuration.md:4037-4057`.
- G2-35 [MATCHES] The empty-slot account is read by the health check and `yoyo status`: `internal/health/observe.go:438`, `internal/readmodel/emptyslots.go`.

### Missing from the document
- The empty-slot cause set that is built (G2-4), and the owner mapping in `internal/ownership/emptyslot.go:60-80`.
- The document says "Its revision ... has landed" for stopped-run-recovery and fresh-factory; it does not say that the build pieces are unstarted (no piece 1 to 8 is built that I could find, except the event-driven hold re-check, which is follow-ups piece 2).
- `yoyo pause` and "operator's pause" are cited; fine, but the code's `yoyo pause` was not checked.

### Overlap with other documents
- Point 3 "What failed" and "same act" restate stopped-run-recovery step 1 and 3 (agree; see above).
- Point 4 restates fresh-factory "Who acts" (agree on content, differ on count of acts: five here, four there).
- The states in point 1 repeat the inventory in fewer-lifecycle-states ("What an admitted item can be in", line 63); the two should be one.
- The deadlines (one hour for a hold emptying every slot; four-hour second deadline) restate follow-ups' table (line ~140-147). Agree.
- "Carrying out needs no model" (195) repeats management-and-supervision.

### Could be cut or moved
- "What it rests on, and what was not read" (lines 31-50) and "What each existing piece becomes" (262-290): bookkeeping about other designs.
- The opening incident list (lines 5-16).
- "Alternatives rejected" can drop to one line each.
- Consider splitting into "slots fill" (points 2) and "recovery ends" (point 3 and 4); point 1's states belong in fewer-lifecycle-states.

---

## recoverable-and-terminal-failures.md — Recoverable and terminal failures
Verdict: partly stale (the rules are mostly built; the provider window change and `internal/budget` migration are orders not yet shipped)
Size: 29 KB. Last changed: 2026-11-19.

### Findings
- G2-2 (see above) [DIFFERS] Provider boundary thirty-minute window (lines 96 and 189). Code uses two hours.
- G2-36 [DIFFERS] Naming: this document calls the same thing "the brake", "failure-storm brake" and "trip the brake" (lines 148, 177 and the section after); stopped-run-recovery and fresh-factory call it "the hold on new work". Code config is `brake_probe_overdue_after` and the Go code says both ("hold on new work" in `stopcause.go:140-205`). One word is needed; the terms register should rule.
- G2-37 [MATCHES] Fibonacci backoff capped at thirty minutes, two-hour window for the forge, tracker and others: `internal/recovery/recovery.go:33-60`.
- G2-38 [MATCHES] A failure confined to untouched tests is one with at most five failing tests, all untouched: `internal/checks/touched.go:35,172`. Exactly one rerun: `internal/checks/untouchedrerun.go:37` (`MaxUntouchedReruns = 1`). Files `failingtests.go`, `narrow.go`, `touched.go`, `untouchedrerun.go` exist for builds 1 to 3.
- G2-39 [MATCHES] Lease-held waits bounded to fifteen minutes: `internal/runstate/promotion.go:41`.
- G2-40 [MATCHES] `execution.transient_relaunches_before_blocking` exists: `internal/config/config.go:403`.
- G2-41 [MATCHES] The hold-count rules (unrecognised cause counts; harness failures count): `internal/runstate/stopcause.go:140-205`.
- G2-42 [UNCLEAR] "Where each decision is made": retries decided only by `internal/budget` (lines ~160-170). `internal/budget/budget.go` exists with `Decide`, and 13 call sites outside the package use `budget.` symbols, but the document says migration is item-by-item and last in order (open item yoyodyne-ifd.428.126). I did not establish which boundaries have moved.
- G2-43 [UNCLEAR] The two reruns of a check stopped by its time limit within three times its limit (lines ~104-112): `internal/checks/timelimit.go` and the check-stage continue code exist; not read in detail.
- G2-44 [NOT BUILT] Builds 4 to 6 for untouched tests (base-commit run, read model count of tests that failed then passed with the dashboard entry, CI rerun) (lines ~195-203). I found the base-failing stop class (`StopBaseFailing`, `internal/runstate/stopclass.go`) but did not find a read model count of flaky tests.

### Missing from the document
- The code's stop-class list (`StopBaseFailing`, `StopForgeQueueRequired`, `CauseMergeQueueHeld`, `StopRecoveryWindow`) that carries these rules; a reader needs the list.
- That a provider boundary's retry limit also exists as `TransientRelaunches` in the run record.
- The unrecognised-failure rule says "treated as an answer and reported"; it does not say the hold on new work counts it (the later section does). Join the two.

### Overlap with other documents
- Check time-limit bounds restated in stopped-run-recovery 156 and fresh-factory (last paragraph of acceptance). Agree.
- The lost-race limit paragraph (line ~173) restates stopped-run-recovery step 1 lines 156 and 159. Agree; one should point to the other.
- "Where each decision is made" restates one-owner-for-each-guarantee.
- Line 92 vs stopped-run-recovery 178 on unrecognised causes: see G2-1 overlap note.

### Could be cut or moved
- "What this orders" (lines 187-203) is a build list with open work-item ids; move to the tracker.
- The untouched-tests section (~lines 106-160) is long and mostly built; keep the rule, cut the alternatives and the ruling narrative.
- Frontmatter revision text (lines 1-45).

---

## follow-ups.md — Follow-ups
Verdict: mostly a plan not built (record, closed check list and runner built for two holds; deadlines, escalation, commands, most creators not built)
Size: 28 KB. Last changed: 2026-10-26.

### Findings
- G2-5 [NOT BUILT] Every act that leaves something waiting writes a follow-up in the same act, refused otherwise.
  - Document: lines 85-105: parking, wait decision, unknown recovery result, brake trip, endpoint pause, dependency wait, wait to land, ask to operator, reports, open asks.
  - Code: follow-ups are written only by `internal/orchestrator/followups.go:61,95` (machine-check hold and lapsed provider login). Triage waits are `Closure` records (`internal/triage/triage.go:944-985`). `internal/runstate/followup.go` is the store.
  - What to change in the document: add a built/not built column to the list under "What creates a follow-up".
- G2-45 [NOT BUILT] Deadlines and lapses: past the first deadline the entry returns to its owner's list, past the second goes to the manager, and a bug is filed (lines 140-170).
  - Code: `BackToOwnerAt`, `ToManagerAt` are written (`internal/followup/checkrun/environment.go:224-225`) and stored, but no code reads them outside the record (grep finds no consumer).
  - What to change in the document: mark piece 4 not built.
- G2-46 [NOT BUILT] `follow_ups.check_interval`, `command_check_interval`, `deadlines`, `person_reminder_interval`, `command_checks` settings.
  - Code: none in `internal/config/config.go`; `internal/followup/checkrun/runner.go:29-31` and `docs/configuration.md:4052` both say they do not exist yet. Intervals are constants (5 and 10 minutes), matching the defaults the document gives.
- G2-47 [NOT BUILT] `yoyo follow-up add | list | close` (lines 184-190). No such command in `internal/cli`.
- G2-48 [NOT BUILT] The person-only step reaches the operator at once and repeats every 30 minutes (lines 172-180). Item yoyodyne-ifd.432.25.18 is open.
- G2-49 [NOT BUILT] Bringing in today's waits once at first start (lines 192-205); no such migration found.
- G2-50 [MATCHES] The closed check list: all 16 named checks exist as `CheckName` values (`internal/followup/check.go:33-76`), and a check cannot carry a command (`check.go:194,308`).
- G2-51 [MATCHES] The record fields: waits, owner, about, blocks, keeps-stop-listed, check, two deadlines, origin, state, passed, closed: `internal/followup/followup.go:30-75`.
- G2-52 [MATCHES] The registry has a `follow-up` kind: `internal/ownership/kind.go:161`, `registry.go:717`.
- G2-53 [MATCHES] The harness runs the checks every five minutes and environment or command checks every ten, one at a time, recording results only on change: `internal/followup/checkrun/runner.go:30-45`; `docs/configuration.md:4041-4047`.
- G2-54 [MATCHES] The hold re-check (yoyodyne-ifd.432.25.19) is built and nothing else re-checks a hold's cause: `internal/orchestrator/followups.go:1-25`.

### Missing from the document
- Which follow-up creators exist today: only the machine-check hold and lapsed provider login. A reader cannot tell this from the text.
- `docs/configuration.md` already carries a section on follow-ups (line 4037) that is more accurate than this design about what is built.
- The runner's `tell.go` (telling whoever waits) and `commands.go` (declared command checks) exist; not mentioned.

### Overlap with other documents
- "A wait decided on a stopped run" and "A recovery act whose result is unknown" (lines ~88-92) restate stopped-run-recovery steps 2-4 and fresh-factory unknown-result paragraph. Agree.
- Deadlines table (lines 140-147) is restated in forward-progress (empty slot, line 150) and referred to by stopped-run-recovery. Agree.
- Escalation ladder (lines ~160-166) duplicates ownership-and-the-operator "The escalation ladder"; follow-ups links to it, which is right.
- The "keeps a stop listed" concept appears in follow-ups (95), stopped-run-recovery steps 2 and 3, fresh-factory, forward-progress, fewer-lifecycle-states point 2. Five places define it; stopped-run-recovery step 2 should own it.

### Could be cut or moved
- The opening incident list (lines 3-16) and the pointer to the diagnosis; link only.
- "Designs this changes" (lines ~207-220): all already landed per the text itself; cut.
- "Alternatives rejected" to one line each.
- "Bringing in the waits that exist today" can wait until piece 7 is being built.
