# yoyodyne-ifd.437.84: design documents against the code, group 8: the machine and configuration

One of nine reports comparing the design documents in `docs/designs/` with the code, made at the operator's request of 2026-11-21 for a review of every design against the current code. This one covers group 8, the machine and configuration.

- **Read on:** 2026-11-21, between 15:40 and 15:48 PST. Main moved from `2447eeaab` to `fe2db3d37` at 15:41, a minute into the reading; that change was 22 files, mostly the team feedback repair code in `internal/runstate`.
- **Read by:** a read-only reader in the operator's assistant's session, on Sonnet 5.5. It read code and ran nothing, tests included.
- **Checked by hand:** the operator's assistant checked three findings against the code. G8-1, G8-7 and G8-14 held. Every other finding is unchecked.
- **How to use it:** this is evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Classes:** `MATCHES`, the code does what the design says; `DIFFERS`, the code does something else; `NOT BUILT`, the design calls it future or the code is absent; `UNCLEAR`, not established.

## Summary
- Documents: 6. Claims checked: about 62. MATCHES 33, DIFFERS 5, NOT BUILT 21, UNCLEAR 3.
- The five mismatches that matter most:
  - G8-1: machine-home describes the supervisor moving state on its own (`moving.json`, `home-move.json`, drain limit, undo). None of it exists; only `yoyo home migrate` exists, and its warning says so.
  - G8-12: project-groups is a plan with zero code (no `group.yaml`, `yoyo group`, `groups/` directory, `project` key), yet machine-home states `groups/` rules and a refusal as if built.
  - G8-14: portable-agent-configuration describes `yoyo config materialize`, `extract`, `adopt` and a role-applied typed config write (section 6). None exist. The CLI has `validate`, `show`, `drift`, `baseline`.
  - G8-23: tracker-store-size build piece 6 (backup folder: delete copies older than 14 days, ignore check, tracker setting, warning) has no code. The rebuild (pieces 4-5) is built.
  - G8-27: configurable-workflows names `yoyo workflow validate` and `show --effective` as the inspection surface. There is no `workflow` command (`internal/cli/cli.go:38-141`); the one in workflow-diagrams is also unbuilt.
- Consolidation proposals:
  - Split machine-home: keep the built layout, binding and commands; move the supervisor-move plan (lines 398-566) and the Validation section (569-745) to their own plan document or to the work items.
  - Cut `configurable-workflows.md`'s settled-question list, recurring-task detail and Sentinel text into the documents that own them (architect-loop, program-manager, tool-interface).
  - Mark project-groups "not built" at the top and delete its "Documents this changes" section (done).
  - portable-agent-configuration: split "built" (lock, drift, baseline, layers) from "designed" (materialize, extract, adopt, section 6).
- Note on the inventory's "missing paths": `.yoyodyne/roles/` is built (`internal/config/roles.go:57` loads `roles/<name>.yaml`; activation check at `internal/config/resolve.go:106`) and `.yoyodyne/config.lock` is built (`internal/config/lock.go:41`). They are missing only because this repository's own `.yoyodyne/` holds `config.yaml`, `personas`, `remits`, `workflows`. Not drift.

## machine-home.md — The machine home
Verdict: partly stale (layout, binding and commands built; the supervisor's own move is a plan)
Size: 52 KB. Last changed: 2026-11-20 (inventory); the last revision is stamped 2026-11-21.

### Findings
- G8-1 [NOT BUILT] The supervisor moves the state itself, starting no new work, draining, giving up, undoing.
  - Document: lines 398-533: "The supervisor ... moves it, in its periodic pass"; `moving.json`; `home-move.json`; "two hours by default" drain.
  - Code: a search of all Go files for `moving.json`, `home-move.json` and "drain limit" for the move finds nothing. `internal/home/migrate.go:202` `MoveHome` is one pass that returns `Moved`/`LeftBehind`; there is no step record, no retry at 1/5/15 minutes, no undo. `internal/home/migrate.go:100` `StillEarlier` says "this build moves nothing on its own; `yoyo home migrate` moves it".
  - What to change: state at the top of "The product moves its own state" that it is not built (the validation text at lines 725-730 already waits on yoyodyne-ifd.434.29), and drop "Moving it by hand" framing that `yoyo home migrate` is the override.
- G8-2 [DIFFERS] The warning "says ... when the product will next try to move it or why it last could not".
  - Document: lines 387-389.
  - Code: `internal/home/migrate.go:100-110` — the warning names `yoyo home migrate` and says nothing about a next attempt. The script description at lines 703-708 already admits this.
  - What to change: say plainly the warning names the command today; keep the "next attempt" wording under the plan.
- G8-3 [DIFFERS] `yoyo home migrate` "refuses ... any session holds the watch lease, any conversation has a turn in flight, or the supervisor's move is under way" and "finishes a move that `home-move.json` shows was interrupted".
  - Document: lines 537-541.
  - Code: `internal/home/migrate.go:202-345` is idempotent by renames and leftovers, with no `home-move.json`; `internal/cli/home.go:239` is the command. The refusal for runs in flight is built; "supervisor's move under way" and `home-move.json` cannot exist.
  - What to change: drop the `home-move.json` and "move under way" clauses until the supervisor move is built.
- G8-4 [DIFFERS] The migration's seven steps, each "marked done" in a record.
  - Document: lines 461-489, 449-452.
  - Code: `internal/home/migrate.go:202-345` does the same moves in a different grouping and adds the machine file (`machine.yaml`) and bindings; the marker is `MovedMarkerName`. No per-step record.
  - What to change: add the machine file move and binding step to the list; remove the "marked done" claim or mark it planned.
- G8-5 [DIFFERS] `yoyo project rename` "refuses while a run is in flight or the new name is taken".
  - Document: lines 279-284.
  - Code: `internal/cli/project.go:570-581` also refuses while the project's `worktrees/` holds any worktree ("still holds N worktree(s) ... registered at those paths").
  - What to change: add the worktree refusal to the rename description and to validation check 5.
- G8-6 [NOT BUILT] Companion intent repository build is "the contributor-mode epic's (yoyodyne-ifd.78)", "decomposed once the Lead PM ... releases it".
  - Document: line 374-375.
  - Code: `internal/cli/init.go:38` has `yoyo init --external --intent`; `internal/cli/goals.go:194` and `internal/cli/artifact_check.go:89` have `--intent`; `internal/home/home.go:30` `IntentDirectoryName`. So much of this is built, and line 374 is out of date.
  - What to change: rewrite line 374 to say what is built (init, checks, landing route per check 7) and what is not.
- G8-7 [NOT BUILT] `groups/<group id>/intent/` at the top of the home, and "Groups are made only in this home": `yoyo group init` and a group-naming configuration "are refused while the state is still in the old platform home".
  - Document: lines 164-170, 193-200.
  - Code: no Go file mentions `GroupsDirectoryName`, `group init` or `group.yaml`; `internal/home/home.go` lists only accounts, `operator-hold.json`, logs, `heavy-checks`, `machine-identity.json`, `projects` (lines 270-275, 283-289).
  - What to change: mark the `groups/` entry and the refusal as planned (project-groups, not built) or delete until the build exists.
- G8-8 [DIFFERS] Wording of the migration: the document treats the migration as automatic ("a build deployed over a watching session restarts into a harness that finds its state where it left it").
  - Document: lines 379-385.
  - Code: this part matches (`internal/home/home.go:117-137` `Default` keeps the earlier home when `~/.yoyodyne` is not in use). Listed here only to separate it from G8-1; see MATCHES.
  - What to change: nothing.
- G8-9 [UNCLEAR] Validation check 6 and the script's coverage (lines 688-735). `scripts/validate-machine-home.sh` exists. I did not read it, so I cannot confirm which of its seven checks match the mapping.
- MATCHES (one line each): default home `~/.yoyodyne` with precedence env, XDG, machine file (`home.go:117,186,239`); top-of-home entries accounts, `operator-hold.json`, `machine-identity.json`, `heavy-checks`, logs (`home.go:270-290`); `projects/<id>/` with `config.yaml`, `repository.json`, `state/`, `worktrees/`, `intent/` (`home.go:283-296`); product id as a safe segment (`domain.ValidateIdentifier`, `types.go:545`); binding by git common directory (`binding.go:75`); `yoyo project bind|rename|list` (`cli/project.go:47-51`); bind `--replace` and refusal while runs in flight (`project.go:84,395-413`); `--replace` refusal for kept stopped-run worktrees (`project.go:418-446`); bind rewrites `product.repository` in a kept config (`project.go:142-190`); removal of the state-root marker on unbind (`project.go:187`); machine identity made once (`home/machineidentity.go`); move of operator hold, identity, heavy-checks, accounts, logs to top (`migrate.go:~275`); `git worktree repair` per moved worktree (`migrate.go:560`); old-home marker (`migrate.go:~330`); `YOYODYNE_STATE_HOME` keeps old home (`home.go:30`, origin handling at 239); refusal for second clone and moved clone; heavy-check record is one record at the top (`home.go:278-282`); `scripts/validate-machine-home.sh` exists.

### Missing from the document
- The machine file `machine.yaml` at `~/.yoyodyne/machine.yaml` that the state-root key is read from (`internal/home/home.go:42-46`), and the earlier config home (`YOYODYNE_CONFIG_HOME`, `XDG_CONFIG_HOME`) the migration reads (`migrate.go:34-65`). The document says the old external config home "ends" but not how the machine file resolves.
- `yoyo project list --home` and `--json` output including origin (`cli/project.go:730,775`).
- Origins named `environment:`, `default`, `earlier-default` that `yoyo doctor` and `config show --origins` print (`home.go:60-72`).
- The install-cache directory per project (`home.InstallCacheDirectoryWithin`, `home.go:351`), which belongs in "The project directory" list.

### Overlap with other documents
- Lines 196-200 vs project-groups.md lines 48 and 149, 167: both describe `groups/<group id>/intent/`. They agree. project-groups says the machine-home revision is "once this design lands"; it has landed (revision 14 here). Delete project-groups's instruction.
- Lines 439-445 (drain limit "one of the numeric limits the development manager decides under portable agent configuration") vs portable-agent-configuration.md lines 327-328: agree in wording, but the key is `execution.redeploy_drain_limit` (`internal/config/config.go:330`), which is the redeploy drain, not a move drain; the move limit has no key.
- Lines 185-190 vs `heavy-check-capacity.md` (not read): same claim, no conflict seen.

### Could be cut or moved
- The 14 revision entries in front matter (lines 8-68) carry incident history and work-item narrative; repository comment rules put that in commits.
- Validation section (lines 569-745, about 175 lines) is test planning, not design; move to the validation work item or the script header.
- "Alternatives rejected" (lines 774-810) restates reasons already given in the body (e.g. lines 514-517, 789-803).
- Lines 202-216 "One identity for the machine" tells the build piece what to check; this is work-item text.

## portable-agent-configuration.md — Portable agent configuration
Verdict: partly stale (lock, drift and baseline built; materialize, extract, adopt and role-applied values not built)
Size: 28 KB. Last changed: 2026-11-06.

### Findings
- G8-10 [MATCHES] Layers and merge: defaults, `extends` bundle, project file; `checkBundleDocument` refuses `version`/`product` in a bundle (`internal/config/builtin.go:147`); `BuiltinV1 = "builtin:v1"` (`builtin.go:19`).
- G8-11 [MATCHES] `.yoyodyne/config.lock` is a committed baseline with version, bundle, revision, digested values and is never read at load (`internal/config/lock.go:41-60`; `drift.go:251`); `yoyo config drift` sorts values into unchanged/yours/available/conflicting (`drift.go`, `cli/cli.go:180`); missing lock is a report (`drift.go:293`).
- G8-13 [DIFFERS] The lock is written by `materialize` and `init`.
  - Document: line 218: "`materialize` and `init` write `.yoyodyne/config.lock`".
  - Code: `internal/config/scaffold.go:113` (init writes it) and `internal/cli/cli.go:182` `config baseline` writes one where none exists. There is no `materialize`.
  - What to change: name `init` and `yoyo config baseline`; document `baseline`, which the design never mentions.
- G8-14 [NOT BUILT] `yoyo config materialize` and `yoyo config extract`, including `materialize --from <bundle>` and the "effective configuration does not move" test.
  - Document: lines 175-199, 270-296.
  - Code: `internal/cli/cli.go:175-184` dispatches only `validate`, `show`, `drift`, `baseline`. No Go file defines materialize or extract (the grep for "materialize" hits only unrelated text and drift comments). `docs/configuration.md` still documents the four manual steps.
  - What to change: say these two commands are designed and not built, as line 250 already says for `adopt`.
- G8-15 [NOT BUILT] `yoyo config adopt <key>`: line 250 itself says "designed but not yet built". Code agrees (not in `cli.go:175-184`).
- G8-16 [NOT BUILT] Section 6: a typed action by which the development manager applies `checks`, `developer_models`, schedule and limits through a reviewed run that edits only `config.yaml` and `config.lock`; "applied by" shown in drift and doctor; narrowing of `RACE_PACKAGES`.
  - Document: lines 307-414.
  - Code: no capability for applying configuration exists (`internal/capability/*.go` has no config or apply capability; `internal/toolcatalog/catalog.go` lists log read, run inspect/verify/recover, health observe/follow). `RACE_PACKAGES` appears only in `internal/config/heavychecks.go`.
  - What to change: mark section 6 as a plan with its work items, or move it to its own design.
- G8-17 [UNCLEAR] `yoyo config show` "gains a fourth line in its header" and `yoyo doctor` lists drift (lines 155-160). I did not open `runConfigShow` or the doctor code.
- G8-18 [UNCLEAR] "Drift is a read-model derivation" (lines 164-168, 454-456). `internal/cli/drift_test.go` and `config/drift.go` exist; I did not check `internal/readmodel` for a drift field.
- MATCHES: `config show --origins` and revision `cfg-...` (`cli/cli.go:176-178`, config revision in `config`); `builtin:v1` states no checks; role definitions load from `roles/<name>.yaml` with an activation check (`config/roles.go:57`, `resolve.go:106`) and `.yoyodyne/roles/` is refused by grants (`protectedpath/condition_test.go:716`).

### Missing from the document
- `yoyo config baseline` (record a baseline where there is none) and the lock's `LockVersion = 1` and `docs/configuration.md` text on template-supplied files compared where the baseline has no record (docs/configuration.md around lines 11105-11127).
- Where a kept configuration lives now: under `~/.yoyodyne/projects/<id>/config.yaml` (machine-home), so "committed file" wording at lines 218-228 and 355-374 needs the external case.
- The `outside_files` setting and other keys a role may not apply (line 335-345 lists the refused keys but not `outside_files`).

### Overlap with other documents
- Lines 118-136 (what a project cannot inherit) vs `docs/configuration.md` "Precedence" and "Merge and removal semantics": the design links there; no restatement beyond the summary.
- Lines 339-343 vs mcp-tools-through-the-harness.md and scheduled-message-intake.md: they point back here; agree.
- Lines 355-379 (how a value reaches the committed file) vs artifact-contract.md and the landing designs: restates the reviewed-run path; keep a pointer only.

### Could be cut or moved
- "Decided at ratification" (lines 433-456) repeats the decisions of lines 86-101 and 215-228.
- The work-item-voice intro (lines 55-58) and revision log (lines 8-32).
- Section 5 on third-party bundles (lines 259-305) describes a source that does not exist; shrink to the rule "values travel, authority does not" and drop the rest until a plugin exists.

## pinned-outside-files.md — Pinned outside files
Verdict: current
Size: 20 KB. Last changed: 2026-10-28.

### Findings
- All checked claims match. MATCHES: record suffix `.yoyodyne-source.yaml` (`internal/outsidefile/record.go:31`); default limit 5 MiB and ceiling 50 MiB (`fetch.go:18-20`); at most 5 redirects, each checked (`fetch.go:22,153-160`); retry after 10 minutes (`fetch.go:25`); `outside_files.hosts` in config (`internal/config/config.go`; this repository's `.yoyodyne/config.yaml:372-373` lists `cdn.jsdelivr.net`); both records exist beside the files (`internal/dashboard/assets/chart.umd.js.yoyodyne-source.yaml`, `chart.js-LICENSE.md.yoyodyne-source.yaml`); pinned files compared before checks (`outsidefile/compare.go`); run built in orchestrator (`orchestrator/pinnedfiles.go`, `outsidefilelanding.go`); run state record (`runstate/outsidefile.go`); fetch only from main (`pinnedfiles.go:92` lists files at a commit).
- G8-19 [UNCLEAR] Six runs per content of a record, three failed queue runs stop the record, one warning report (lines 136, 160). I did not find the count constants; I looked in `orchestrator/pinnedfiles.go` and `outsidefilelanding.go` only by name search ("six", "= 6", "Spent"). Check by reading `outsidefilelanding.go`.
- G8-20 [DIFFERS] "What this repository needs" lists the records as still to be made, and says the delivery page "is parked until it is on main".
  - Document: lines 51, 162-168.
  - Code: `internal/dashboard/assets/` already holds `chart.umd.js`, its license file and both records, plus `delivery.html`, `delivery.js`; `.yoyodyne/config.yaml:372` already lists the host.
  - What to change: delete "What this repository needs" and the "parked" sentences; they are done.

### Missing from the document
- `internal/dashboard/chartlib.go` (the dashboard's use of the pinned library) is not named.
- A limit on the size of a record (`MaxRecordBytes = 64 KiB`, `record.go:35`) and on the listing the harness reads from main (`pinnedfiles.go:31-32`: 500,000 files, 64 MiB).

### Overlap with other documents
- Lines 118, 129 vs document-landing-route.md and integration-through-a-merge-queue.md: the "own place, no developer slot" and the queue admission are stated in all three; they agree.
- Line 61 vs observability-and-dashboard.md chart-library paragraph: this says the dashboard wording "is brought into line ... at its next revision"; not checked whether that happened.

### Could be cut or moved
- Lines 55 and the revision log carry the incident history (yoyodyne-e7j8); the failure paragraph at lines 156-160 repeats the amendments' reasoning.
- "What a reviewer can check" (lines 191-204) restates "What is enforced in code" (181-189) and the body.

## project-groups.md — Project groups
Verdict: mostly a plan not built
Size: 19 KB. Last changed: 2026-11-20.

### Findings
- G8-12 [NOT BUILT] Everything in sections 1-6 and the ten first-stage build pieces.
  - Document: lines 39-48 (`group.yaml`, `yoyo group init`, `group: <id>`), 56 (the `project` metadata key), 62 (management only in the managing project), 95 (group checks).
  - Code: no Go file mentions `group.yaml`, `group init` or a groups directory (searched all `.go` files); `internal/home/home.go` has no groups entry. Work items for pieces exist (`.beads/issues.jsonl`, `yoyodyne-t5r4.7` to `.10`, `t5r4.12`), so this is admitted plan work, not code.
  - What to change: add a status line at the top saying nothing in it is built.
- G8-21 [DIFFERS] "The machine home design is revised to say so" / "The architect writes that revision once this design lands."
  - Document: lines 48, 149, 167.
  - Code/document: machine-home.md lists `groups/<group id>/intent/` at lines 164-170 (revision of 2026-11-21).
  - What to change: delete the future-tense instruction and the "Documents this changes" section.
- G8-22 [UNCLEAR] "`yoyo doctor` refuses a group whose intent repository has none [remote]"; refs `refs/yoyodyne/tracker` and `refs/yoyodyne/records` in section 7 of working-alongside-other-contributors.md (lines 52, 67). Not checked in code; group-less contributor code exists in `internal/teamrecords`, `internal/teamclaim`.
- MATCHES: v1 non-goal limits one harness to one product (documented in `docs/product/goals/v1-non-goals.md`, linked; not opened); operator pause is machine-wide (machine-home line 172-174, `home.go:272`); `docs/product/project-groups-goals.md` exists.

### Missing from the document
- None needed beyond a statement that there is no code.

### Overlap with other documents
- Lines 52, 58, 67 repeat the contributors design's reference names and claim rules; both documents agree as far as read.
- Line 73 (the pause stops every harness) vs machine-home.md lines 172-174: agree.
- Section 3 passes vs program-manager.md and management-and-supervision.md: not compared.

### Could be cut or moved
- "What a reviewer can check" (lines 133-142) repeats "What is enforced in code" (124-131).
- Section 7 (second stage, lines 109-113) and the rejected-alternative bullets are short; keep.

## tracker-store-size.md — Keeping the tracker store small
Verdict: partly stale (rebuild and limits built; backup-folder piece not built; opening figures out of date)
Size: 20 KB. Last changed: 2026-11-19.

### Findings
- G8-23 [NOT BUILT] The backup folder: delete copies older than 14 days except the newest, check the ignore rule, set the tracker's own retention to 14 days, stop and warn for the architect if anything else is in it.
  - Document: lines 149-158; build piece 6, line 177.
  - Code: no Go file under `internal/maintain`, `internal/maintenancejob` or `internal/beads` names a backup directory (only `internal/beads/writelock.go:288` skips `.backup` names). `.beads/.gitignore:61` has `backup/`, but `.beads/config.yaml:41-45` shows the tracker's backup keys still commented out.
  - What to change: mark section and piece 6 as not built, or note the item that carries it.
- G8-24 [MATCHES] The rebuild: rehearsal on a copy, 60 second bound (`internal/maintain/trackerrebuild.go:72-74` `rebuildBound`), kept copy 14 days (`keptFor`, line 69), warning after three passes held back (`heldBackWarnAfter = 3`, line 80), recorded reasons for each held-back pass (`heldByOutsideProcess`, `heldByLockWait` etc., lines 83-88), lock taken first, listing limit 10 seconds (`rebuildListingLimit`, line 66), store `StoreSize`/`RehearseRebuild`/`RebuildStore` (`trackerrebuild.go:92-96`, `beads/storerebuild.go`), record in `runstate/trackerstorerebuild.go`.
- G8-25 [DIFFERS] A failed rehearsal "files one warning for the development manager" with no stated repeat.
  - Document: lines 106-108.
  - Code: `internal/maintain/trackerrebuild.go:46-50` "is not tried again for rebuildRetry" and `rebuildRetry = 24 * time.Hour` (line 76-78): a failed rehearsal is retried after 24 hours.
  - What to change: add the 24-hour wait after a failed rehearsal.
- G8-26 [MATCHES] Export "at most once every five minutes" (`internal/beads/export.go:28` `exportRefreshInterval = 5 * time.Minute`); dashboard listings from one shared flight (`internal/beads/listingshare.go`); "left again N times" counter (`orchestrator/repeatleave.go`, `streakFor`). Whether the dashboard listing refreshes "at most once a minute" (line 80) is UNCLEAR: I found `listingshare.go` (a single-flight, not a time-based cache) and `itemReadsMaxAge = 5 * time.Minute` (`beads/itemreads.go:178`), not a one-minute figure.
- G8-31 [UNCLEAR] `bd compact`/`bd flatten` "removed" from the weekly measurement (piece 5, line 176): no `compact` or `flatten` string in non-test Go code, consistent with removal; the weekly measurement lives in `internal/maintain/trackerlimits.go` (not read).

### Missing from the document
- The 24-hour retry after a rehearsal that did not pass (G8-25).
- The live-rebuild bound `rebuildLiveBound = 3 x rebuildBound` that ends a live rebuild before it replaces anything (`trackerrebuild.go:72-75`).
- Where the kept stores go (`runstate.TrackerStoreRebuildStore.KeptDirectory`/`WorkDirectory`, `trackerrebuild.go:100-105`), though line 117 says "in the harness's state".

### Overlap with other documents
- Line 60 vs one-owner-for-each-guarantee.md and orchestrator-boundaries.md (which links here): agree on "one tracker reader and writer".
- Line 78 (notes written at most three times per reason) vs the repeat-leave rules in the development-manager text: agree.

### Could be cut or moved
- "How this was decided" (lines 62-74) and the November 19 incident paragraph (line 135): incident figures (35 MB a day, 930 MB, 1.56 GB, "October 31 ... 19:43 PDT") belong in the diagnosis at `docs/diagnoses/yoyodyne-ifd-428-126-4-19-store-stays-over-limit.md`.
- Four "Rejected" paragraphs at lines 139-147 repeat reasons given in "How this was decided".

## configurable-workflows.md — Configurable workflows
Verdict: partly stale (definitions, executor, trial and role-definition loading built; the switch, step-attempt records and the workflow command not built)
Size: 34 KB. Last changed: 2026-11-19.

### Findings
- G8-27 [NOT BUILT] "`yoyo workflow validate` and `show --effective` are the inspection surface".
  - Document: the definitions paragraph under "Definitions and validation" (line 112 of the file region read).
  - Code: `internal/cli/cli.go:38-141` has no `workflow` case; `internal/cli/conformance.go:43` takes a `--workflow` flag only. `workflow-diagrams.md:41,70` also refers to `yoyo workflow show --effective` and `yoyo workflow draw`.
  - What to change: say what inspection exists today (`yoyo conformance --workflow`) or mark the commands planned.
- G8-28 [NOT BUILT] Step-attempt record and idempotency key; settling a step whose process died (build pieces 1-2; runtime paragraph).
  - Document: "create or recover the step-attempt record and idempotency key", "Reconciliation ... adopts, safely re-executes under the same key".
  - Code: `internal/runstate/workflowinstance.go:53,83` defines `WorkflowInstance` and `WorkflowCheckpoint` only; no attempt or key type exists in `runstate`.
  - What to change: separate "runtime as built" (instance, checkpoint, pinned definition, `Start/Resume/Step/Run` at `internal/workflow/execute.go:103-225`) from "runtime as designed".
- G8-29 [MATCHES] The graph has not taken over: the delivery trial observes a run through an instance while the run record decides (`internal/orchestrator/declarative.go:86-138` `deliveryTrial`, `beginDeliveryTrial`); parity test compares built-in definitions with recorded paths (`internal/orchestrator/parity_test.go:3-74`). So "Until the switch" is current.
- G8-30 [MATCHES] `$wait` as runtime destination (`internal/workflow/definition.go:55`); loader and validator exist (`internal/workflow/loader.go`, `validate.go`, `digest.go`, `compile.go`); `.yoyodyne/workflows/` exists in this repository; role definitions under `.yoyodyne/roles/` with an activation record (`config/roles.go:57,263`; `resolve.go:106`); operators mapping `dashboard_token` and `decide-amendments` (`internal/config/scaffold.go:695-704`); passes run beside one another up to a bound (`internal/orchestrator/recurringfirings.go:56` `MaxConcurrentFirings = 4`).
- G8-32 [UNCLEAR] "The management bundles ... hold `repository.read` and `repository.list`" (lines on authority model). `internal/chat/refusedblock.go:23` names "repository read" and `internal/exchange/evidence.go:450` frames repository reads; I did not open the capability registry (`internal/capability`) to confirm the two capability names. Nine-piece list and four switch conditions (opt-in trial of 50 runs over 7 days): trial exists; no record counting runs and days found, so piece 6 is probably not built (UNCLEAR, did not search `runstate`).
- G8-33 [UNCLEAR] "New projects get materialized workflow files from `init`; existing projects keep built-in selection until they explicitly eject a copy" (settled question 6). `docs/configuration.md:5447-5448` lists `.yoyodyne/workflows/delivery.yaml` and `delivery-human-approval.yaml`; I did not read the `init` code to confirm what it writes.

### Missing from the document
- `yoyo conformance` (runs a definition against recorded behaviour, `internal/cli/conformance.go`, `internal/conformance/workflow.go`), which is the one thing that exercises a workflow from the command line.
- `workflow-diagrams.md` and `yoyo workflow draw` are not linked from the inspection surface.
- The "five roles" in the authority paragraph vs six shipped bundles (line on the program manager); the text says both, which a reader will trip on.

### Overlap with other documents
- The recurring-pass concurrency paragraph says program-manager.md's "one task per scheduler pull" is "replaced by this paragraph, and that design is revised to say so; until then this paragraph and architect-loop are the rule". Both architect-loop.md and program-manager.md link back here; whether program-manager was revised is not checked.
- The tool paragraph (actions invocable from conversation) vs tool-interface.md: agree, points there.
- Authority model vs `docs/decisions/authority-by-capability.md`: restated, not checked.
- Role definitions under `.yoyodyne/roles/` here vs machine-home.md lines 354-355 ("stays beyond any grant"): agree.

### Could be cut or moved
- The 20 revision entries (lines 8-72) and the "twenty questions, settled" list (lines ~197-223), which restate the body.
- The Sentinel and agent-memory paragraphs belong to the program-manager and memory documents; they describe things outside the workflow runtime.
- The "Whether the graph runs the pipeline" section is a dated decision plus a nine-piece build list; move the decision to `docs/decisions/` and keep the build list in work items.
