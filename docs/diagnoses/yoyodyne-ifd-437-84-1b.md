# yoyodyne-ifd.437.84: second reading of the design documents against the code, group 1: core architecture

A second, adversarial reading of the report `yoyodyne-ifd-437-84-1.md` beside this file. It re-read every finding there that differs or is not built against the code, tried to settle the unclear ones, checked a sample of the matches, and looked for what the first reading missed. **Where the two files disagree, this one stands.**

- **Read on:** 2026-11-21, in the evening, between 18:33 and 18:43 PST, with main at `848d86190`. The code had moved since the first reading; each finding is judged against the code as it was at this reading.
- **Read by:** a read-only reader on Opus 5.5 in the operator's assistant's session. It read code and ran nothing, tests included.
- **Checked by hand:** nothing in this file was checked again by hand.
- **How to use it:** evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Second-reading classes:** `CONFIRMED`, the first finding is right as written; `CORRECTED`, its gist is right and a detail is wrong; `WRONG`, the design and the code agree or the finding misread one of them; a class in the first reading's own words where an unclear finding was settled.

Read against the working tree at main `848d86190` (2026-11-21 18:14 PST). Read-only; nothing was run except read-only shell searches and `git log`.

## Summary
- Findings re-read (every DIFFERS and NOT BUILT): 26. CONFIRMED 20, CORRECTED 6, WRONG 0, CANNOT TELL 0.
  (The report's header says 14 NOT BUILT; I count 12 tagged NOT BUILT plus 14 DIFFERS.)
- Unclear findings settled: 5 of 7 (G1-5, G1-6, G1-33, G1-34, G1-35 settled; G1-37 and G1-42 still unclear; the reader half of G1-39 is also still unclear).
- Verdicts changed: none.
- The findings the architect should not act on as written:
  - G1-5 (UNCLEAR): settle it as NOT BUILT. With the merge queue on, the developer phase still pushes the run branch and opens its pull request. This also makes `v1-harness-design.md` line 564 wrong (see Missed).
  - G1-13: the list of callers is incomplete. `slotIsFree` is also called at `repaircontinue.go:616` and `githubidentity.go:249`, and the `teamfeedbackrepair.go` call is at `:368`, not `:293`.
  - G1-16: `trackerstoresquash.go` was not misnamed in the design. It was replaced by `trackerstorerebuild.go` in `eddc1a139`, after the design's dated reading at be20dec3.
  - G1-17 and G1-20: the design gives these counts "at ed48d235" and "at 348e9a43". They were right then, so they are dated rather than wrong. The current numbers are 24 store constructors, the forge client built 3 times in `run.go` (16 times across `internal/cli`), and 12,489 and 9,592 lines.
  - G1-27: not every migration item is closed. Item 3 waits on the open `yoyodyne-ifd.428.99.6`, items 2 and 4 are not built, and item 13's `make test` check does not exist.
  - G1-36: the runner also evaluates `command` checks, not only `environment` ones.
  - The fewer-lifecycle MATCHES line "recovery decisions ... hold ... (lines 169-181)" is wrong. The design's table (lines 171-179) has no hold. That is the very mismatch G1-38 reports.
  - The artifact-contract MATCHES line "marked consistent ... confirmed without the operator" is only partly true. The code still sends some consistent-marked writes to the operator (see Missed).
- The most important things the first reader missed:
  - `v1-harness-design.md:564` says that with the merge queue on, a run "pushes nothing and opens no pull request for the run branch". The code does both (`pipeline.go:5177` to `publish.go:164-219`). Nothing on that path reads `execution.merge_queue`, and this repository has `merge_queue: true` and `publishing: automatic` (`.yoyodyne/config.yaml:178,385`).
  - Race case 2 in `one-owner-for-each-guarantee.md:296` requires the queue to run the full race check. `choosing-checks-by-change.md:121-136` accepts a narrowed race check, and `merge-queue-pace.md:54-63` leaves the race check out of the queue. All three claim the same four cases.
  - Race point 4 in `one-owner-for-each-guarantee.md:301` narrows the run on main to macOS-only code. `choosing-checks-by-change.md:136` and `merge-queue-pace.md:63,74` still rely on a whole race run on main every four hours to catch combined races after landing, and the code still runs the whole `make race` there.
  - `artifact/publication.go:106-121,138-148` (`ConsistentMarkOverruled`/`goalsChanged`) sends a consistent-marked write to the operator anyway when it creates the goals or the brief, adds or removes a goal, or revises an unapproved revision. `artifact-contract.md:132` says a consistent mark is confirmed "whatever the kind's approval setting".
  - `orchestrator-boundaries.md` counts four continuation paths and three copies of `closeEntry` (lines 102, 112). The code has at least six paths that take a slot: `githubidentity.go` and `teamfeedbackrepair.go` are new. It also has four `closeEntry` copies (`checkstagecontinue.go:387`, `githubidentity.go:308`, `integrationresume.go:664`, `stallcontinue.go:324`).

## v1-harness-design.md
Verdict: agree (partly stale)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G1-1 | DIFFERS | CONFIRMED | Design line 584 says "Claude Code refuses every tool". `claudecode/backend.go:232` gives the reviewer `Read, Glob, Grep, Bash`, passed at `:797`. |
| G1-2 | DIFFERS | CONFIRMED | `backend/backend.go:549-558` has `CheckAvailability(ctx)`, `Capabilities()` and `Run(ctx, RunRequest)`. Resume is `RunRequest.SessionID` (`:85`). |
| G1-3 | DIFFERS | CONFIRMED | `cli.go:917-970` prints the usage, about 50 commands. `start`/`stop` are at `cli.go:91-94`. `pause` pauses spending and `release` lifts intake. |
| G1-4 | DIFFERS | CONFIRMED | `.beads/issues.jsonl`: `yoyodyne-fmk` is closed. |
| G1-5 | UNCLEAR | NOT BUILT | The developer phase calls `publishAttempt` under automatic integration (`pipeline.go:5170-5177`). That calls `Publisher.Ensure` and opens the run-branch pull request (`publish.go:212-219`). Nothing on that path reads `execution.merge_queue`. The only queue predicate, `admitsToMergeQueue` (`mergequeueadmission.go:106-117`), is used at promotion (`pipeline.go:4189`). No test refuses a run-branch pull request. The "needs one build piece" paragraph (line 615) is accurate. The bullet at line 606 and step 5 at line 564 are not. |
| G1-6 | UNCLEAR | MATCHES | `orchestrator/publish.go:387` has `const mergeMethod = publish.MergeCommit`, recorded through `recordMergeMethod` (`publish.go:969`). |

Matches spot-checked (state root order `home.go:239-265`, `state_root` refused at `document.go:524-528`, legacy file at `discover.go:25`, five agents at `bundle.yaml:209-245`, approval defaults at `bundle.yaml:193-194`, repairs 2 and concurrency 1 at `resolve.go:344-346`). All hold.

### Missed by the first reading
- Line 564 (step 5) says that with the merge queue on, the harness "pushes nothing and opens no pull request for the run branch". The code pushes and opens one (`pipeline.go:5177`, `publish.go:164-219`), as in G1-5. The document also contradicts itself: line 615 says this still "needs one build piece".
- Line 451 calls the invariants directory "kept in their own directory" beside the decision home. In the code it is a subdirectory of the decisions home (`config/config.go:48` `DefaultInvariants = "docs/decisions/invariants"`). That is consistent but easy to misread. Minor.

### Contradictions with other designs
- Line 584 against `provider-adapters-and-endpoints.md:51` ("the reviewer is given read-only tools"). The adapter design is right.
- Line 564 against line 615 of the same document, as above.

## orchestrator-boundaries.md
Verdict: agree (mostly a plan not built)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G1-12 | NOT BUILT | CONFIRMED | `ls internal` has no `continuation`, `integration`, `mergequeue`, `documentlanding` or `rolepass`. Every file named at lines 61-66 is still in `internal/orchestrator`. |
| G1-13 | NOT BUILT | CORRECTED | The gist is right: `slotIsFree` is still at `orchestrator/rerun.go:1089`. Its callers are `stallcontinue.go:199`, `checkstagecontinue.go:216`, `integrationresume.go:312`, `repaircontinue.go:616`, `githubidentity.go:249`, `teamfeedbackrepair.go:368` (not :293) and `rerun.go:491`. |
| G1-14 | NOT BUILT | CONFIRMED | The copies remain. `integrationresume.go:410` still has its own `restoreWorktree` (operation 5). `ReserveLive` is used at `stallcontinue.go:245` and `checkstagecontinue.go:282`. |
| G1-15 | NOT BUILT | CONFIRMED | `yoyodyne-ifd.428.99.6` is open. |
| G1-16 | DIFFERS | CORRECTED | The design lists its files "at be20dec3". `trackerstoresquash.go` existed then and was replaced by `trackerstorerebuild.go` in `eddc1a139`. The rest holds: 10 runstate files import `internal/beads`, and functions are called at `document.go:270`, `emptyslots.go:212,239`, `state.go:4781` and `trackerstorecleanup.go:173`. There is no check in `internal/composition` or any runstate audit test. `internal/beads` imports no runstate. |
| G1-17 | DIFFERS | CORRECTED | The design dates the counts ("At ed48d235"). Now `buildComponents` (`cli/run.go:385-567`) has 24 `runstate.New*` calls. `publish.GitHub{` appears 3 times in `run.go` (`:725` pipelineFrom, `:1003` reconcilerFrom, `:1639` mergeQueueDriverFrom) and 16 times across `internal/cli`. |
| G1-18 | DIFFERS | CONFIRMED | There is no `withListingRecord`. The client has a `Listings ListingRecorder` field (`beads/client.go:528,626`). |
| G1-19 | NOT BUILT | CONFIRMED | `resolveRoots` is at `cli/run.go:239`, `standingRemains` at `:369` and `primaryCheckout` at `:608`. `internal/home` exports no roots constructor. |
| G1-20 | DIFFERS | CORRECTED | The design attributes the counts to the code map at 348e9a43, so it is dated rather than wrong. Now `wc -l` gives 12,489 and 9,592. |

The G1-21 to G1-24 matches were spot-checked. `declarative.go:1-30` still performs nothing, and every named file exists. They hold.

### Missed by the first reading
- Lines 102, 112 and 147 say there are four continuation paths and "three copies of `closeEntry`". There are now four copies (`checkstagecontinue.go:387`, `githubidentity.go:308`, `integrationresume.go:664`, `stallcontinue.go:324`). At least two more paths take a slot and hand back to the docket (`githubidentity.go:249`, `teamfeedbackrepair.go:368`), and the design assigns neither to a boundary.
- Line 62 says counting what is free lives in `slotIsFree` and `heavychecks.go`. The design never names `internal/developerslot`, whose package comment says it derives "which slots are free to pull" (`developerslot/slot.go:1-2`) and which the scheduler uses (`schedule.go`, `scheduleslots.go`).

### Contradictions with other designs
- Line 62 against `one-owner-for-each-guarantee.md:175`. One-owner allows that `internal/developerslot` may already count what is free and moves that count into the reservation. Orchestrator-boundaries places the count only in `slotIsFree` and moves it to runstate. In the code it exists in all three places.

## one-owner-for-each-guarantee.md
Verdict: agree (partly stale). The race-check section is now the most unreliable part.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G1-26 | NOT BUILT | CONFIRMED | `.yoyodyne/config.yaml:465-466` has `main_checks: - make race`. The `Makefile` has no race target over `MACOS_TEST_PACKAGES` (`:280`, `test-macos` at `:282`). `failing_tests` (`config.yaml:453`) does not list a new target. |
| G1-25 | DIFFERS | CONFIRMED | `config.yaml:473-477` omits the race check from `merge_queue_checks`, and `merge_queue_race_check` runs it narrowed. Line 299 says "until [429.132] lands ... the second case needs the queue's own run to include the full race check". `yoyodyne-ifd.429.132` is closed and `merge-queue-pace.md` has landed, but this point was never revised. |
| G1-27 | DIFFERS | CORRECTED | The items behind migration items 1, 5, 7, 9, 11, 12 and 13 (428.116, .117, .118, .121, .119, 433.45, 432.46) and 428.86 (item 8) are closed. Item 3 waits on the open `428.99.6`. Item 2 (other whole saves onto narrow updates) and item 4 (stale-save refusal in the other stores) were not shown built. Item 13's "check that nothing else runs the tracker's command" does not exist (G1-31). The "items waiting on design" table at lines 399-410 is entirely closed items. |
| G1-28 | NOT BUILT | CONFIRMED | `internal/checks` has no plan type. `runstate/heavycheck.go:76` keeps `permits.lock`. `428.99.6` is open. |
| G1-29 | DIFFERS | CONFIRMED | The slot is refused at run time: `runstate/store.go:858,886` `ErrMadeLiveWithoutASlot`. The paid turn is enforced by type: `budget.Permission` (`budget/budget.go:77-89`). |
| G1-30 | NOT BUILT | CONFIRMED | `budget/budget.go` handles message limits only. It is imported by `chat/compact.go`, `chat/chat.go` and `config/timelimits.go`. |
| G1-31 | DIFFERS | CONFIRMED | `doctor.go:528` runs `bd stats`, and `cli/setup.go:408,441` run `bd init` and `bd stats`. `orchestrator/cyclelistings.go` still exists. There is no `make test` check. |
| G1-33 | UNCLEAR | DIFFERS (partly) | The priced ending is kept: `claudecode/pricedending_test.go:18`, `codex/pricedending_test.go:25`. But the Claude adapter decodes cost itself (`claudecode/parser.go:103,958`, `total_cost_usd`), against "No package outside `internal/cost` decodes a cost event". No such check exists. |
| G1-34 | UNCLEAR | DIFFERS (partly built) | Present: guarantee 1 (`runrecordwriters_audit_test.go`), guarantee 3's race test (`runstate/reservelive_test.go:76`), guarantee 4's test (`chat/messagebudget_test.go:43`) and guarantee 8 (`boundedreads_audit_test.go`). Absent: the checks for guarantees 2, 5 (decode), 7 and 9. Guarantee 3's "by its type" is not true (G1-29). |
| G1-35 | UNCLEAR | NOT BUILT | `internal/maincheck/maincheck.go` (789 lines) has the single rerun and the warning (header `:18-23`) and no seven-day count. Nothing in it mentions days. |

The matches were spot-checked: `whole_by` at `state.go:1198`, four hours at `config.go:771`, 90m at `config.yaml:232`, `g9g` open. They hold.

### Missed by the first reading
- Line 301 says the run on main "runs as one of the harness's recurring tasks, whose schedule the development manager sets under section 6". It is the supervisor's own run on `execution.main_check_interval` (`maincheck/maincheck.go:1-3`, `config/config.go:771`), not a recurring task.
- Line 175 says the first build item moves `internal/developerslot`'s free-slot count into the reservation. `428.117` is closed, but `developerslot` still derives the free slots the scheduler pulls from (`developerslot/slot.go:1-2`, used in `schedule.go` and `scheduleslots.go`). There are also two more ways to take a slot: `ReclaimSlot` (`store.go:529`) and `RoutingStore.ClaimSlot` (`routingstore.go:83`).
- The owner of point 4's behaviour, `internal/maincheck`, is not named. The first reader already noted this; it is repeated here because it matters for a rewrite.

### Contradictions with other designs
- Race point 3 case 2 (line 296) requires the queue to run "the full race check among them" whole. `choosing-checks-by-change.md:121-136` (revised for amendment-4ae0c6e2) accepts the approved head's full race result or the narrowed `merge_queue_race_check`. `merge-queue-pace.md:54-63` leaves the race check off the queue's list. Both one-owner (line 296) and choosing-checks (line 118) say "the same four cases". The code follows the other two designs.
- Race point 4 (line 301) narrows the run on main to macOS-only packages. `choosing-checks-by-change.md:136` ("The scheduled full race check on main catches it after landing, within four hours") and `merge-queue-pace.md:63,74,104` both rest on a whole race run on main. The code still runs the whole `make race` (G1-26).
- `merge-queue-pace.md:60` says the race check's new places "are already ruled under guarantee 9". Guarantee 9 point 3 (line 299) still requires the full race check in the queue.

## artifact-contract.md
Verdict: agree (partly stale)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G1-32 | DIFFERS | CONFIRMED | `artifact/artifact.go:62-76` has seven file kinds plus `KindInvariant`, and no guidance. `docs/product/goals/writing-goals.md:3` is still `kind: non-goals`. |
| G1-37 | UNCLEAR | STILL UNCLEAR | I read `artifact/publication.go:95-148,300-335` and found no branch for "no reviewer or a non-integrated intent repository". I did not trace the write path further. |

Matches that are not matches:
- "Three cases ... marked consistent: confirmed without the operator" (line 132) is only partly built. `publication.go:106-121` (`ConsistentMarkOverruled`) and `:138-148` (`goalsChanged`) send a consistent-marked write to the operator anyway when it creates the goals or the brief, adds or removes a goal, or revises a document not approved as it stood. `markOverruled` (`:126-130`) also refuses one whose record falls short of `Rewording`. The user docs say so (`docs/artifacts.md:114,463`, `docs/configuration/artifacts.md:528,600`). The contract does not.

### Missed by the first reading
- The consistent-mark overrule above. It is a material gap in point 3.
- `docs/decisions/who-approves-a-change-to-intent.md:26` says "the artifact contract gains `reworded`". The contract never mentions it. The code's concept is `Rewording`/`DelegatedAt` (`artifact/approval.go:238-305`).
- The revision action `identified` (`artifact.go:145`) is skipped by the delegation check (`approval.go:271`). The contract's Identity section does not name it.

### Contradictions with other designs
- Line 132 against `docs/decisions/who-approves-a-change-to-intent.md:21`. The decision's test, "would the goals ... admit work they refused", is what the code applies through `goalsChanged`, which treats adding or removing a goal as fundamental by definition. The contract states no such rule.

## fewer-lifecycle-states.md
Verdict: agree (mostly a plan not built)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G1-36 | NOT BUILT | CORRECTED | The gist holds, with corrections. `checkrun/runner.go:206-219` evaluates `environment` and `command` (the hold re-check command, 432.25.19) and nothing else. `cli/schedule.go:599-611` sets no `Checks` map. Only two acts write a follow-up: the machine-checks hold and the provider login outage (`orchestrator/followups.go:61,95`, through `Runner.Attach`). The endpoint pause's paced probe is still a scheduler schedule (`schedule.go:5540-5586`), against line 146. |
| G1-38 | DIFFERS | CONFIRMED | `runstate/recovery.go:43` `RecoverHold`, `runinspect.go:71`, `health.go:219`. The code comment at `recovery.go:31` still says this is stopped-run-recovery's step-3 list, which that design says has no hold (`stopped-run-recovery.md:409`). |
| G1-39 | NOT BUILT | CONFIRMED (item status); readers STILL UNCLEAR | `yoyodyne-819` is closed and `428.106` is open. I did not read the point-3 readers. |
| G1-40 | NOT BUILT | CONFIRMED | `docs/decisions/invariants/` has 11 invariants and none about follow-ups. `yoyodyne-0bj` is closed. |
| G1-41 | NOT BUILT | CONFIRMED | No test cross-checks `RecoveryActions()` (`recovery.go:54`) against the expected states in `runinspect`. I found none of the four tests. |
| G1-42 | UNCLEAR | STILL UNCLEAR | The brake is still `readmodel/heldline.go`. I did not walk the rest of the inventory. |

Matches that are not matches:
- "[MATCHES] the recovery decisions ... give the item back, hold, ask ..., plus the release act (lines 169-181)" is wrong. The design's table (lines 171-179) drops the hold, and the code keeps it. This is the same mismatch as G1-38, recorded as a match.

### Missed by the first reading
- Line 146 says the endpoint pause has "not a schedule in the scheduler". The paced probe lives in `orchestrator/schedule.go:5540-5586` and `scheduleslots.go:176`.
- Build piece 4 (line 301) depends on "slice 3 of stopped run recovery". `stopped-run-recovery.md:409` already states the list has no hold, so that design reads as built while the code (`recovery.go:43`) still has it.

### Contradictions with other designs
- None between designs: `stopped-run-recovery.md:409`, `fresh-factory-health-and-preserved-run-recovery.md:248` and `forward-progress.md:265` were revised to agree with it. All four disagree with the code (G1-38).

## README.md
Verdict: agree (current)

The G1-43 to G1-45 matches were re-checked: `artifacthome.go:42-53` markers, `doctor.go:829-844`. They hold. One correction to the report's note: `docs/designs/` holds 53 `.md` files, so 52 designs besides the README, not 51.

### Missed by the first reading
- None of consequence.

### Contradictions with other designs
- None.

## Not reached
- G1-37, G1-42 and the reader half of G1-39 remain unclear, as above.
- Fewer than five MATCHES per document were re-checked for `fewer-lifecycle-states.md` and `README.md`. Those documents carry fewer specific claims.
