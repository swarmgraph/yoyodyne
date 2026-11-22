# yoyodyne-ifd.437.84: the rules in the code that decide order and choice of work

A list of every rule found in the code that decides the order or choice of work, and whether the operating rules record it. It was made beside the nine reports on the design documents, after the operator found on 2026-11-21 that the rule putting priority 0 work ahead of a labelled slot's own work had never been put to him.

- **Read on:** 2026-11-21, between 15:40 and 15:48 PST. Main moved from `2447eeaab` to `fe2db3d37` at 15:41, a minute into the reading; that change was 22 files, mostly the team feedback repair code in `internal/runstate`.
- **Read by:** a read-only reader in the operator's assistant's session, on Sonnet 5.5. It read code and ran nothing, tests included.
- **Checked by hand:** the operator's assistant checked three findings against the code. R3, R9 and R10 held. Every other finding is unchecked.
- **How to use it:** this is evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Classes:** `MATCHES`, the code does what the design says; `DIFFERS`, the code does something else; `NOT BUILT`, the design calls it future or the code is absent; `UNCLEAR`, not established.

Read-only audit of the repository at main (2447eeaab). All paths are under that directory. Line numbers are from the files as read today.

## Summary

- 39 rules listed.
- Only in code: 11 (R1 to R11). Four of them (R4, R5, R6, R8) are partly described in docs; the step that matters is only in code.
- Described in docs/configuration.md, docs/work.md, docs/operations.md or a design document, but not in the operating rules: 23 (R12 to R34).
- In the operating rules (docs/product/operating-rules.md): 5 (R35 to R39). Of the 23 in the middle group, R22, R25, R28 and R29 touch an operating rule only in part (noted in each).
- Two places where code and a document disagree: R12 (merge queue age limit 8 hours in code, 4 hours in the design amended 2026-11-20) and R37 (operating rule line 97 says the reliability seat is "dedicated"; the code's `dedicated` flag is not set on that slot).
- Method: "only in code" means I grepped docs/*.md, docs/designs/*.md and docs/configuration/*.md for the rule's words or number and found nothing. A document that states the rule in other words could have been missed; each entry says what I searched for.
- Not established: who decided most of the "described" rules. Where a design document's revision log or a config comment names the decider, it is given; otherwise "decider not recorded in what I read".

How the pieces fit, in the order a pull applies them (schedule.go, `Scheduler.Schedule`): global stops (R24, R25, R26, R29), carry-outs of recorded decisions (R4, R5, R6, R34, R35), continuations of paused runs (R3), documents and outside files (R27), then the walk of the ordered queue by slot (R1, R2, R7 to R11, R13 to R19).

---

# A. Only in code

### R1: Free slots are filled in three passes: preferring slots take their own label's work first, then slots with no preference take what is left, then preferring slots that found none of their label's work fall back to the whole backlog. Inside a pass, slots go in configured order.
- Code: `internal/orchestrator/schedule.go:4072` — `passes := []struct{ labelled, preferring bool }{{true, true}, {false, false}, {false, true}}`; `internal/developerslot/slot.go:224` `Assignment.Free()` lists preferring slots first, then the rest, each in configured order.
- Can a setting change it? Only through `execution.developer_slots` (which slots prefer what, and their positions).
- In the operating rules? no
- Described elsewhere? docs/configuration.md "A developer slot that prefers a label" says a preferring slot pulls its label first and falls back; it does not say preferring slots are served before unlabelled slots, or that slot position decides ties. Searched for "configured order", "preferring slots first": no hit.
- Note: with this project's config (slot 1 reliability, slots 2 and 3 dedicated) slot 1 walks first, so it takes the top priority-0 item of any label.

### R2: Which slot a run already in flight is read as occupying decides which slots count as free, and so which preference the next pull applies: runs with a recorded slot keep it; the rest go oldest first, labelled runs into a slot preferring one of their labels, others into a no-preference slot first.
- Code: `internal/developerslot/slot.go:146` `Assign` (sort by start time then run id at line ~160; labelled-run pass and then "a slot with no preference first").
- Can a setting change it? `execution.developer_slots`, `execution.max_concurrent_developers`.
- In the operating rules? no
- Described elsewhere? Package comment in slot.go only; docs/configuration.md says status and scheduler read one derivation but not the placement order (searched "falls back", "no preference first").
- Note: an unlabelled run started earlier sits in a no-preference slot, so a labelled slot stays free for its label. A decider changing slot order changes which work gets pulled, not only what is shown.

### R3: A run paused on its dependencies or on forge checks that have since cleared is continued after the recorded decisions and before any queue walk, oldest pause first, with no regard to its item's priority or label.
- Code: `internal/orchestrator/schedule.go:3526` calls `nextContinuations`; `internal/orchestrator/schedulecontinue.go:146-152` "Oldest pause first" (sort by `UpdatedAt`, then run id); line 210 reason says "continued at the first pull with a developer slot free, ahead of any new work".
- Can a setting change it? no (the CI wait itself is `execution.ci_checks`, see R30)
- In the operating rules? partly: line 99 says work on a change a run already wrote and kept, "such as a decided repair or continuation", goes first. It does not say how continuations order against decided repairs, nor oldest-first among themselves.
- Described elsewhere? nowhere for the order among kinds (searched "oldest pause", "ahead of any new work" in work.md, configuration.md, operations.md, docs/designs/checks-on-ci.md).
- Note: kept-change recorded decisions (R4) take free slots first and continuations get what remains, so a priority-3 repair can take a slot ahead of a priority-0 continuation. Whether that is wanted is not written anywhere.

### R4: Among recorded decisions that build on a kept change, order is: those whose item carries a free dedicated slot's label first, then by item priority (highest first), then oldest decision first.
- Code: `internal/orchestrator/schedule.go:3349` `precedence`; `schedule.go:6124` sort by `DecidedAt`, then `6131-6160` re-sort kept, dedicated, priority.
- Can a setting change it? Dedicated labels come from `execution.developer_slots`.
- In the operating rules? partly: line 99 and line 98 give "kept work first" and "dedicated seat takes its kind first"; the three-level order among several kept changes is not stated.
- Described elsewhere? docs/configuration.md "A slot dedicated to its label" (order list items 1 and 2) and "Scheduling ready work" ("the highest priority first among several").
- Note: filed in section A because the three-level order (dedicated label, then priority, then oldest) is stated only in code and one configuration paragraph; the first two levels are partly described there.

### R5: What counts as "a change a run already wrote and kept" is decided by decision type and by looking in the repository: continue-checks, continue-stall, finish-merge and resume-integration always count; a repair counts if the branch or the worktree is still there; a re-run counts only if the branch is still there; if the look fails or the run is unknown, it does not count and the decision is ordered as fresh work.
- Code: `internal/orchestrator/schedule.go:6551` `keptChange` (switch at 6554-6560, repository look at 6575-6590).
- Can a setting change it? no
- In the operating rules? partly: line 99 names "a decided repair or continuation, or a merge being tried again" but not these tests.
- Described elsewhere? docs/configuration.md "Scheduling ready work", paragraph "A decided repair or re-run is fired by the same pass" (branch or worktree for a repair, branch for a re-run). The failed-look-means-fresh fallback is nowhere.
- Note: a re-run whose branch is gone is fresh work and falls under priority order, so the operating rule does not apply to it.

### R6: A decided recovery of the intake brake's own test run goes first of everything while the hold stands; the hold lets one run start at a time.
- Code: `internal/orchestrator/schedule.go:3733` (testRunRecovery before every other start) and `schedule.go:3650` `if (probing || checksProbing) && free > 1 { free = 1 }`; reason text `schedule.go:6591` `keptForTheHoldReason`.
- Can a setting change it? `execution.blocked_runs_before_intake_hold`, `brake_cooldown` (R25).
- In the operating rules? no (line 100 covers who is summoned, not this)
- Described elsewhere? docs/configuration.md "Watching instead of draining" ("The intake hold is the remote brake") covers the hold and probe; the "recovery goes ahead of every ready item" ordering I did not find stated outside code comments.
- Note: this is an ordering exception that sits above R4.

### R7: Slot counting. A start this session made that has no run on record counts as holding a slot for two minutes; after that, the oldest such starts (at most as many as there are slots) stop counting. A start fired with no slot free never counts. A run waiting on dependencies or forge checks, a run waiting at its merge step for the forge, a change waiting in the merge queue, and a harness-authored run hold no developer slot.
- Code: `internal/orchestrator/emptyslots.go:33` `countedGrace = 2 * time.Minute`; `internal/orchestrator/schedule.go:8848` `stalledStarts`, `8881` `slotlessStarts`; `internal/orchestrator/idlestarts.go:62`; `internal/runstate/state.go:6326` `HoldsDeveloperSlot`.
- Can a setting change it? `execution.max_concurrent_developers` sets the count; the rules for what holds one: no.
- In the operating rules? no
- Described elsewhere? The "holds no developer slot" parts: docs/operations.md:5823 (forge checks), docs/configuration.md "Integrating through a merge queue" (queue wait), docs/work.md:1576 (dependency pause). The two-minute grace and the bound on stalled starts: nowhere.
- Note: the capacity count decides whether work is taken at all (free < 1 means no walk).

### R8: A session never starts the same item twice unless the item changed, and in a drain never starts it twice at all; a start that attempted no work is retried at the next poll; a start stopped by a usage window is held until the window's reset.
- Code: `internal/orchestrator/schedule.go:4439` `cooling`, `4474` `forget`, `4680` `fingerprint`; `scheduleslots.go:126`.
- Can a setting change it? no
- In the operating rules? no
- Described elsewhere? docs/work.md "Letting the harness choose the work" (paragraph at line 2288: "A session does not start the same item twice unless the item has changed"). Listed here as only partly in code because the retry-at-next-poll and usage-window clauses are not in that paragraph; if a decider reads only the paragraph, it is described.
- Note: what counts as a change is the fingerprint: what it says, what it is for, priority, status, dependencies, notes (work.md).

### R9: A carry-out that came back without the run going on is not tried again for 15 minutes; one turned away only for want of a free slot is not paced.
- Code: `internal/orchestrator/schedulecontinue.go:58` `continuationRetry = 15 * time.Minute`, `62` `lostTheSlot`.
- Can a setting change it? no
- In the operating rules? no
- Described elsewhere? nowhere (searched "refused" with "continuation" in work.md, configuration.md, operations.md).
- Note: this is a limit that decides whether kept work is taken, and a decider might want it shorter given R3 and operating rule line 99.

### R10: Merge queue limits that decide whether an entry is built again or handed back: a candidate is rebuilt at most 3 times in one call because the target moved; a candidate's checkout is attempted at most 3 times; a candidate whose review reply holds no readable verdict is asked twice at most, then the entry is handed back; one pass takes at most 24 steps; a session works at most 8 queues at once; a waiting queue goes round again every minute; held changes are looked at every 5 minutes.
- Code: `internal/orchestrator/mergequeueworker.go:80` (`maxMergeQueueDriftRebuilds = 3`), `:87` (`maxMergeQueueCheckoutAttempts = 3`), `:1080` (`maxMergeQueueUnreadableReviews = 2`); `internal/orchestrator/mergequeuedriver.go:44` (24 steps), `:499` (5 minutes), `:503` (8 queues), `:512` (1 minute); `internal/orchestrator/mergequeuepromotion.go:429` (`maxMergeQueueBegins = 2`).
- Can a setting change it? no
- In the operating rules? no
- Described elsewhere? nowhere found (searched the merge queue design and configuration.md "Integrating through a merge queue" for "rebuilds", "twice", "24", "eight queues").
- Note: the design says to "apply existing finite retry and continuation budgets durably" (docs/designs/integration-through-a-merge-queue.md, "Failure, withdrawal and continuation") without naming these.

### R11: A required forge check on a run waiting for CI is read at most once a minute, and a forge that stays unreadable for 5 minutes ends the wait and sends the run to the full local checks; a session whose reading of the harness fails for 5 minutes stops (runs it started are waited out).
- Code: `internal/orchestrator/cichecks.go:589` (`ciPollEvery = time.Minute`), `:597` (`ciUnreadableFor = 5 * time.Minute`); `internal/orchestrator/schedule.go:228` (`readRetryWindow = 5 * time.Minute`), used at `:2443`.
- Can a setting change it? no for these numbers (the 60 minute wait limit is a setting, R30)
- In the operating rules? no
- Described elsewhere? docs/designs/checks-on-ci.md says an unreadable forge falls back to local checks but gives no number; the 5 minute session stop is nowhere (searched "gives up on it", "went on being unreadable").
- Note: operating rule line 95 says only a person or a bound on the work ends a run. These bounds end a wait or a session, not a run, and each is waited out first.

---

# B. Described in docs or a design, not in the operating rules

### R12: The merge queue takes its next candidate by five rules: an entry already being checked or reviewed is never displaced; then an entry waiting 8 hours (code) goes first whatever its priority; then priority 0 code and the architect's oldest document; then documents; then other code by priority; then code whose priority was never read.
- Code: `internal/orchestrator/mergequeueorder.go:29` (`mergeQueueAgeLimit = 8 * time.Hour`), `:397` `rank`, `:241` `pick`.
- Can a setting change it? no
- In the operating rules? no
- Described elsewhere? docs/designs/integration-through-a-merge-queue.md "The order the queue takes entries in" (line 137). Decider: the operator directed priority 0 code goes ahead of documents (design lines 25 and 139); the architect recorded the rest (revision log, 2026-10 to 2026-11).
- Note: CONFLICT. The design as amended 2026-11-20 (yoyodyne-ifd.429.255) says the age rule is 4 hours, documents land together in batches of at most 8, and a document candidate never lands twice in a row while ordinary code waits. The code has 8 hours, one document per candidate, and no turn-taking (`rank` has no such branch; grep for "four hours", "4 * time.Hour", "documentBatch" in internal/orchestrator found nothing). The build pieces appear not yet landed. Also the header comment of `mergequeueworker.go:5` still says the worker "takes the first entry still waiting in admission order", which is stale.

### R13: In the merge queue, an entry whose work item depends on another admitted entry's item waits for it wherever it ranks, unless every entry would wait (a cycle).
- Code: `internal/orchestrator/mergequeueorder.go:301-333` (`blocked` map, `candidates` filter).
- Can a setting change it? no
- In the operating rules? no
- Described elsewhere? docs/designs/integration-through-a-merge-queue.md "Further rules" (line 172).
- Note: with the tracker unreadable the dependencies last read are used (design amended 2026-11-21).

### R14: The ready backlog is ordered by priority (0 first), then items the Lead Product Manager placed first (earliest placement first, ended by closing, priority change or her removal), then oldest admitted first; an item with no admission time goes after those with one.
- Code: `internal/backlog/backlog.go:642` `Sort`; `internal/beads/placement.go:109` `PlacedFirst`.
- Can a setting change it? no
- In the operating rules? partly: lines 91 to 94 say what makes an item priority 0 or 1, but not that priority is the first sort key or the tie-break order.
- Described elsewhere? docs/designs/placing-an-item-first.md (architect, created 2026-10-29; on the Lead Product Manager's own promise of 2026-10-28) for placement; docs/configuration.md "Scheduling ready work" says "highest priority first". The oldest-first tie-break: code comment at backlog.go:632-641 ("The tie-break is the harness's and not a decision about the work", added after the 2026-09-29 incident).
- Note: the design says "at most three in one priority"; I did not verify that cap in code.

### R15: Priority-0 work is taken by whichever slot reaches it first, preferring or not, before any labelled work of lower priority; a slot not marked dedicated therefore never walks past priority 0 for its label.
- Code: `internal/orchestrator/scheduleslots.go:261` `outranksPreference` (`entry.Priority == 0 && !slot.Dedicated`), used at `internal/orchestrator/schedule.go:3995`.
- Can a setting change it? Yes, per slot: `execution.developer_slots[].dedicated: true`. Nothing else.
- In the operating rules? no (this is the rule that prompted the audit)
- Described elsewhere? docs/configuration.md "A developer slot that prefers a label" (line 4421, "wherever that sits in the order below priority 0") and "Scheduling ready work" rule 10 (line 4300); docs/work.md rule 10; package comment of scheduleslots.go. Decider not recorded in what I read; the comment calls priority 0 "the top of the Lead Product Manager's order".
- Note: operating rule line 91 says "a developer seat prefers" reliability work and line 97 says the seat is "dedicated to reliability". Under R15 the reliability slot (not marked dedicated) takes any priority-0 item first, reliability or not. See R37.

### R16: A slot that prefers a label, and finds none of its label's work ready, falls back to the ordinary order (it never idles on an empty label).
- Code: `internal/orchestrator/schedule.go:4072` (third pass) and `:4066-4070` (fall back after `walkNothing`); `scheduleslots.go:269-287` records the reason.
- Can a setting change it? `execution.developer_slots`
- In the operating rules? only for a dedicated seat (line 98 "the seat takes the ordinary order, so it never sits idle"); not stated for a seat that only prefers.
- Described elsewhere? docs/configuration.md lines 4414 to 4600.
- Note: none.

### R17: A ready priority-0 item that a dedicated slot walked past, with no other slot free, waits for another slot and is reported by name; it is not given to the dedicated slot.
- Code: `internal/orchestrator/scheduleslots.go:308` `waitingForAnotherSlotReason`; `schedule.go:4130-4137`.
- Can a setting change it? `dedicated` flags
- In the operating rules? partly: line 98 says a dedicated seat takes its kind "ahead of reliability work of any priority elsewhere"; it does not say priority-0 work then waits for a non-dedicated slot.
- Described elsewhere? docs/configuration.md "A slot dedicated to its label" (line 4590 area).
- Note: with this project's config only slot 1 is undedicated, so priority-0 work that is not reliability-labelled has one slot that takes it first. If slot 1 is busy, it waits even with slots 2 and 3 free and working on their labelled work.

### R18: The configuration refuses a slot marked dedicated that names no label, and refuses every slot dedicated, so priority-0 work always has a slot that takes it first.
- Code: `internal/config/config.go:1797-1799`; `internal/domain/label.go:188`.
- Can a setting change it? no (it is what the loader refuses)
- In the operating rules? no
- Described elsewhere? docs/configuration.md "A slot dedicated to its label" (last paragraph).
- Note: this is a standing guarantee for priority 0 that rests in the loader. Operating rules do not state that priority-0 work must always have a slot.

### R19: Priority-0 work is not held behind a run over less urgent work for sharing files with it; it is started, and the other run is the one likely to replay. A child of an epic a run is over still waits whatever the priority. Other racing work is sequenced behind the run in flight.
- Code: `internal/orchestrator/conflict.go:234` `outranks`; `:189` `against`.
- Can a setting change it? no
- In the operating rules? no
- Described elsewhere? docs/configuration.md "Scheduling ready work" rule 3 and its paragraph; docs/work.md.
- Note: a second place where priority 0 overrides ordinary behavior.

### R20: Work that is not ready is skipped: an unresolved directive, children carrying its execution, a conversation executor, a parked item, the needs-design label, a hold from a stopped run, a human-only step not recorded, a prerequisite the tree does not meet, the tracker not calling it ready, a run already in flight, no free slot.
- Code: `internal/orchestrator/scheduleslots.go:111-226` (`eligibility`); `internal/backlog/backlog.go:536-537` (`ready :=`).
- Can a setting change it? no (these are fixed, "thirteen things")
- In the operating rules? no
- Described elsewhere? docs/configuration.md "Scheduling ready work" and docs/work.md "Letting the harness choose the work", the same thirteen in the same order, locked by `internal/doclink/selectionrules_test.go`. needs-design: docs/designs/needs-design-gate.md. Parking: work.md (reason: a $34.38 run on 2026-08-27).
- Note: of the thirteen, only the parking and needs-design rules were recorded as a decision with an incident; I did not establish a named decider for the rest.

### R21: Admitted work whose tracker status is blocked is pulled when its own dependencies are all closed (the tracker's ready list is bypassed for blocked work); a child that every waiting parent waits on is pulled although the tracker holds it.
- Code: `internal/backlog/backlog.go:615` `startable`; `:513-516`.
- Can a setting change it? no
- In the operating rules? no
- Described elsewhere? docs/work.md "Letting the harness choose the work" (first paragraphs, with the 2026-09-04 incident).
- Note: none.

### R22: A recorded provider usage window that closes the models an item's run would ask for passes the item over; it keeps its priority and place and is read as startable at the reset or at the next paced probe. Several windows, a missing provider and a launch-settings hold likewise stop starts for the slots they cover.
- Code: `internal/orchestrator/scheduleslots.go:175-193`; `schedule.go:2840` (`providerAway`), `:2881` (`usageWindow`), `:2926` (`launchSettingsHeld`), `:5550` `windowFor`.
- Can a setting change it? `execution.usage_limit_max_pause` (6h), `usage_limit_in_process_pause` (6h), `usage_limit_unknown_reset_pause` (30m), `server_overload_pause` (90s) in .yoyodyne/config.yaml.
- In the operating rules? partly: line 95 says a run is never killed for a flaky network and only a person or a bound on the work ends a run; the usage pauses are such bounds but are not named.
- Described elsewhere? docs/configuration.md "Waiting out a provider that refuses" (line 6121). Config comment says the maximum "deliberately stops short of the seven-day one".
- Note: decider not recorded in what I read.

### R23: A run that reaches the usage-limit maximum pause (6 hours) stops waiting.
- Code: config key read at `internal/config` (`usage_limit_max_pause`); applied in pipeline `pauseForUsageLimit`, `usageLimitPause` (names in internal/orchestrator/README.md item 11).
- Can a setting change it? `execution.usage_limit_max_pause`
- In the operating rules? no
- Described elsewhere? docs/configuration.md "Waiting out a provider that refuses".
- Note: I did not read pipeline.go for the exact stop. Rule stated from the config comment and README only.

### R24: Nothing new is chosen while intake is held (by the operator or by the brake), while the operator has paused harness activity, while the provider is answering nobody, while a target branch is diverged from the remote, or while every slot's provider failed to apply its sandbox. A drain (not a watch) ends on each of these; a watch waits.
- Code: `internal/orchestrator/schedule.go:237-301` (stop reasons), `:2840-2926`, `:2967-3003` (`pull.Intake.Held`, `pull.paused`), `:3613` `pull.blocked`.
- Can a setting change it? the operator's pause and hold are commands; no setting
- In the operating rules? no (line 100 covers who the brake summons)
- Described elsewhere? docs/configuration.md "Watching instead of draining"; docs/operations.md.
- Note: none.

### R25: Failure-storm brake. Three runs blocked in a row with nothing landing between them hold intake and summon the development manager; if nobody decides within 30 minutes, one probe run starts under the hold; after 4 such cycles with no escalation the harness escalates to the Lead Product Manager and tells the operator, and probes stop until released. Environmental stops count toward nothing.
- Code: `internal/orchestrator/schedule.go:2944` trip; `schedule.go:4865` `workBrake`; defaults `internal/config/config.go:809` (3), `:820` (30m), `:827` (4).
- Can a setting change it? `execution.blocked_runs_before_intake_hold`, `brake_cooldown`, `brake_escalation_cycles`, `brake_probe_overdue_after` (45m). Zero disables or never escalates.
- In the operating rules? partly, line 100: "When the intake brake trips, it summons the development manager to decide, never the operator; the operator is told and may release it. (789c052c)". The thresholds and the automatic escalation to the Lead Product Manager are not in it.
- Described elsewhere? docs/configuration.md "Watching instead of draining" (lines 4877 to 5249).
- Note: the automatic escalation after 4 cycles ends in the operator being told directly, which rule 100 allows ("the operator is told").

### R26: At most `max_waiting_pull_requests` approved changes may wait for a person to merge; at the limit no new work is chosen from the queue, but repairs, continuations, reviews, documents, outside files and the brake's test run still start.
- Code: `internal/orchestrator/waitingpullrequests.go:29`; `internal/orchestrator/schedule.go:3768`; default `internal/config/config.go:814` (5).
- Can a setting change it? `execution.max_waiting_pull_requests` (default 5; 0 is no limit). Not set in .yoyodyne/config.yaml, so the default 5 applies if that key is read.
- In the operating rules? no
- Described elsewhere? docs/configuration.md "Pull requests waiting for a person to merge" (line 5249).
- Note: a limit nobody can read is treated as not reached (waitingpullrequests.go:36-38).

### R27: Confirmed documents landing and files brought in from outside take no developer slot: documents are counted against `execution.document_landings` (default 2), and start before the queue is walked.
- Code: `internal/runstate/state.go:6332` `HoldsDocumentLanding`; `internal/config/config.go:328` (`DefaultDocumentLandings = 2`); `internal/orchestrator/schedule.go:3144-3161`.
- Can a setting change it? `execution.document_landings` (set to 2 in .yoyodyne/config.yaml)
- In the operating rules? no
- Described elsewhere? docs/configuration.md lines 3702 to 3710 and "Scheduling ready work".
- Note: documents go ahead of the developer-slot walk by construction, not by a priority rule.

### R28: A watch session moves onto a newly installed build at most once per `build_move_interval` (3 hours; at least 1, at most 24), onto the newest build installed at that time. After the move falls due, the session drains: it pulls into free seats until the drain bound (15 minutes) is within one poll, starts nothing after, waits for checks already running at most 10 minutes past the bound, waits out promotions and document landings, and stops and preserves other runs.
- Code: `internal/orchestrator/schedule.go:9590` `buildMoveDue`, `:7525` (`redeployCheckWaitCeiling = 10 * time.Minute`), `:7580` `declinesStarts`, `:7602` `declinesDeveloperStarts`, `:2537` the decision; `internal/config/config.go:784` (15m), `:790` (3h, min 1h, max 24h).
- Can a setting change it? `execution.build_move_interval`, `execution.redeploy_drain_limit` (both set at defaults in docs/configuration.md; neither is in .yoyodyne/config.yaml). The 10 minute ceiling: no.
- In the operating rules? partly: line 96 says the parts of the product are "moved onto a new build together, by the product and not by a person". It does not say when or how long a drain lasts.
- Described elsewhere? docs/designs/management-and-supervision.md ("A move happens at most once every three hours", architect ruling yoyodyne-ifd.428.161); docs/configuration.md "Watching instead of draining" (line 5128); docs/operations.md:3912-3935 (ten minutes past the bound).
- Note: the drain bound decides whether a free slot is filled, so seats stand empty for it. The drain skipping starts is also in memory "rebuild drains the watch".

### R29: A decided repair or continuation is not carried out, and no paused run is continued, while the session is stepping down for a restart, the operator's pause is on, or intake is held.
- Code: `internal/orchestrator/schedule.go:3373-3380` (declining), `:3524-3530` (`!paused && !held && !declining`).
- Can a setting change it? no
- In the operating rules? no. Compare line 99, which says kept work is taken first "by any freed developer seat"; these stops come before it.
- Described elsewhere? partly, docs/configuration.md "Watching instead of draining"; docs/work.md.
- Note: a conflict of wording rather than behaviour. Line 99 has no exception for a drain, a pause or a hold.

### R30: Checks on CI. A run pushes its candidate after the short local checks and parks (holding no slot) until the forge has decided the required checks, or until `wait_limit` (60 minutes from the push), or until the forge is unreadable (R11); the run then continues at the first pull with a slot free.
- Code: `internal/config/cichecks.go:21` (`DefaultCIWaitLimit`), `:53`; `internal/orchestrator/cichecks.go:566-600`; `schedulecontinue.go:125-245`.
- Can a setting change it? `execution.ci_checks.enabled`, `local`, `required`, `wait_limit` (set: enabled, `required: [test, race, vet]`, 60m in .yoyodyne/config.yaml, "values decided by the development manager").
- In the operating rules? no
- Described elsewhere? docs/configuration.md "Checks on CI" (line 3832); docs/designs/checks-on-ci.md.
- Note: none.

### R31: Check time limits and heavy-check places. Each check gets `check_timeout` (45m here), a stage `check_stage_timeout` (90m here); a check stopped at its limit is continued at most twice, within three times its limit; a heavy check waits for a place (`make race` 1, `make test` 2 at once) and places go first to a landing or promotion check, then to a run continuing preserved work, then the rest, the earliest asker first within each; a place is kept free for landing checks.
- Code: `internal/orchestrator/heavychecks.go:200` `heavyCheckPriority`; `internal/runstate/heavycheck.go:266, 514, 635, 739`; `internal/runstate/checkstagecontinue.go:43` (`MaxCheckStageContinuations = 2`); `internal/config/heavychecks.go:27`.
- Can a setting change it? `execution.check_timeout`, `check_stage_timeout`, `heavy_checks`
- In the operating rules? no
- Described elsewhere? docs/configuration.md "How many of a heavy check may run at once" (line 3460) and "How long a check may take" (line 3045); docs/work.md hold paragraph on the check time limit; docs/designs/heavy-check-capacity.md. Architect ruling of 2026-10-12 for the heavy-check numbers (comment in .yoyodyne/config.yaml). Check limits are marked "a stopgap" in the config.
- Note: the place order repeats operating rule line 99 (preserved work goes before ordinary) but ranks landings above it. Rule 99 does not mention landings.

### R32: Recurring role passes: when several are due, the one that has waited longest since it fell due goes first (a task that cannot say when it fell due counts as waiting only since now); ties go critical-report delivery, then tasks, then program manager instances. At most 4 firings in flight; two firings of one role's conversation never run at once. A critical report waiting for the Lead Product Manager is delivered in place of her own task's firing.
- Code: `internal/orchestrator/recurringfirings.go:361-374` (sort), `:136-139` (kinds), `:56` (`MaxConcurrentFirings = 4`), `:310-325` (critical delivery in place of her task).
- Can a setting change it? each task's `every`, `enabled`, `max_turns` in `recurring_tasks`. Not the order or the 4.
- In the operating rules? no
- Described elsewhere? docs/configuration.md "Recurring tasks" (line 10407: "waited longest since it fell due goes next, whatever kind it is"); docs/operations.md:9033; docs/designs/configurable-workflows.md:147. The tie order and the 4 limit: not found in docs.
- Note: this project runs the development manager and Lead Product Manager sweeps every 45 minutes (config comment: operator direction of 2026-09-28, to stay inside the provider cache hour).

### R33: Per-run attempt bounds that decide whether a run is kept going or stopped: 4 repair attempts before a replan, 2 integration retries before reconciliation, 2 transient relaunches before blocking, one harness-made continuation of a silent-stream stall, one re-run per docketed stoppage, 4 review rounds per item, a repair grant of 2 rounds.
- Code: `.yoyodyne/config.yaml` (`repair_attempts_before_replan: 4`, `integration_retries_before_reconciliation: 2`, `transient_relaunches_before_blocking: 2`); `internal/runstate/stallcontinue.go:39` (`MaxHarnessStallContinuations = 1`); triage defaults in `internal/config` (triage block, `review_rounds_cap: 4`, `repair_grant_attempts: 2`).
- Can a setting change it? yes for all except the stall continuation count (code constant).
- In the operating rules? no
- Described elsewhere? docs/configuration.md "Triage thresholds" (line 8371) and "What one work item has been given"; docs/work.md.
- Note: these are what end an item's claim on slots; a decider might want them in one place. I did not verify the triage defaults in code, only in docs.

---

### R34: A decision that does not build on a kept change goes ahead of ready work at its item's priority or lower, and behind ready work of higher priority, oldest decision first.
- Code: `internal/orchestrator/schedule.go:3327-3341` `outranked`; `:6335` `outranking`; `:3946` `firePending`.
- Can a setting change it? no
- In the operating rules? no. The operating rules say only that kept work goes first (R35). This ordering is by the Lead Product Manager's order (yoyodyne-ifd.428.58; an incident of 2026-09-29 is cited in the code comment at schedule.go:3282).
- Described elsewhere? docs/configuration.md "Scheduling ready work", paragraph "A decided repair or re-run is fired by the same pass".
- Note: the complement of R35 (the operating rules give kept work first and say nothing about this).

---

# C. In the operating rules

### R35: Work on a change a run has already written and kept, such as a decided repair or continuation, or a merge being tried again, is taken first by any freed developer seat, ahead of fresh work of any priority, reliability seat and dedicated seats included.
- Code: `internal/orchestrator/schedule.go:3303-3326` (comment and `kept`), `:6551` `keptChange`; `outranking` at `:6335` is bypassed for kept work (`:3333-3341`).
- Can a setting change it? no
- In the operating rules? yes, line 99 (the operator, 2026-10-25, recorded on yoyodyne-ifd.428.126.2.1; amendment 2026-10-25T19:41).
- Described elsewhere? docs/configuration.md "Scheduling ready work" and "A slot dedicated to its label".
- Note: see R3, R5 and R29 for what the rule leaves open (how kept work orders among itself; what counts as kept; stops that come first).

### R36: A developer seat dedicated to one kind of work takes it ahead of reliability work of any priority elsewhere; with none of that work ready it takes the ordinary order.
- Code: `internal/orchestrator/scheduleslots.go:261`; `schedule.go:3988-4000`.
- Can a setting change it? `execution.developer_slots[].dedicated`
- In the operating rules? yes, line 98 (the Lead Product Manager, 2026-10-28, on the operator's direction of that day; yoyodyne-ifd.428.130).
- Described elsewhere? docs/configuration.md "A slot dedicated to its label".
- Note: .yoyodyne/config.yaml marks slots 2 (team-development) and 3 (usability, product, performance, cost) dedicated; slot 3 on the operator's direction of 2026-11-21.

### R37: Developer seats are tagged with the labels they prefer, and a reliability-labelled item at priority 0 or 1 is admitted ahead of feature work, which a seat prefers.
- Code: `internal/developerslot/slot.go:34-59`; `internal/domain/label.go:90`; `.yoyodyne/config.yaml` slot 1 `prefer: [reliability]`.
- Can a setting change it? `execution.developer_slots`
- In the operating rules? yes, lines 91 and 97 (5268040c, 2026-09-14; a43f7677 and d83b6391 for the seats).
- Described elsewhere? docs/configuration.md "A developer slot that prefers a label".
- Note: line 97 says "one seat is dedicated to reliability work", and slot 1 in this config is `prefer: [reliability]` without `dedicated: true`. In the code vocabulary it is preferring, not dedicated, so priority-0 work of any label goes ahead of its reliability work (R15). The operating rule and the code use "dedicated" differently.

### R38: When the intake brake trips, it summons the development manager to decide, never the operator; the operator is told and may release it.
- Code: `internal/orchestrator/schedule.go:4711` `brake`, `:4766` `summon`; `recurringfirings.go` handles the summons as a firing.
- Can a setting change it? thresholds, see R25
- In the operating rules? yes, line 100 (789c052c).
- Described elsewhere? docs/configuration.md "Watching instead of draining".
- Note: see R25.

### R39: A run is never killed for a flaky network or a sleeping laptop; a transport that drops or a machine that sleeps is waited out, and only a person or a bound on the work itself ends a run.
- Code: the forge wait and ci wait paths (R11, R30), `internal/runstate/state.go:6320` (ForgeWait holds no slot); the session's read retry at `internal/orchestrator/schedule.go:2443`.
- Can a setting change it? the bounds themselves (usage pauses, wait limits)
- In the operating rules? yes, line 95 (fb5a74a9)
- Described elsewhere? docs/configuration.md "Waiting out a network that dropped" (line 7116).
- Note: the bounds R11, R22, R23 and R30 are what "a bound on the work itself" means in practice; none is named in the rule.

---

## Counts and uncertain entries

- Section A has 11 entries (R1 to R11). R4, R5, R6 and R8 are partly described in docs; the step that matters (dedicated-first inside kept work, failed look counts as fresh, recovery-first under the hold, retry at the next poll) is only in code.
- Section B has 23 entries (R12 to R34). Section C has 5 (R35 to R39). Total 39.
- Entries I could not fully establish: R14 (the cap of three placements), R23 (stop at the usage maximum, not read in pipeline.go), R33 (triage default numbers read only from docs).
