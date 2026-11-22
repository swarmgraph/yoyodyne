# yoyodyne-ifd.437.84: second reading of the design documents against the code, group 4: checks and the run sandbox

A second, adversarial reading of the report `yoyodyne-ifd-437-84-4.md` beside this file. It re-read every finding there that differs or is not built against the code, tried to settle the unclear ones, checked a sample of the matches, and looked for what the first reading missed. **Where the two files disagree, this one stands.**

- **Read on:** 2026-11-21, in the evening, between 18:33 and 18:43 PST, with main at `848d86190`. The code had moved since the first reading; each finding is judged against the code as it was at this reading.
- **Read by:** a read-only reader on Opus 5.5 in the operator's assistant's session. It read code and ran nothing, tests included.
- **Checked by hand:** nothing in this file was checked again by hand.
- **How to use it:** evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Second-reading classes:** `CONFIRMED`, the first finding is right as written; `CORRECTED`, its gist is right and a detail is wrong; `WRONG`, the design and the code agree or the finding misread one of them; a class in the first reading's own words where an unclear finding was settled.

Read against main at `848d86190` (2026-11-21), read-only. The code has moved since the first reading (`fe2db3d37`). Some `docs/configuration.md` line numbers have shifted by a few lines (3845 is now 3849, 3881 is now 3878). None of the findings depends on those numbers.

## Summary
- Findings re-read: 27. CONFIRMED 19, CORRECTED 7, WRONG 1, CANNOT TELL 0.
- Unclear findings settled: 8 of 8 (G4-6, G4-12a, G4-12b, G4-16, G4-17, G4-22, G4-26, G4-32).
- Verdicts changed: `repository-confined-writes.md`, from "partly stale" to "mostly a plan not built". The state directory claim is false for most stores, and almost none of the contract's mechanisms exist.
- Findings the architect should not act on as written:
  - G4-4: wrong. The design does not ask for read-time validation of `required` names. Line 139 rejects it explicitly, and G4-5 already covers the warning that replaces it.
  - G4-9: partly built. This repository's `runstate` guard tests are in `DOCUMENT_TESTS` (`Makefile:374`). They run beside narrowed packages whenever the change also touches a file outside every package. A pure Go change does not run them.
  - G4-10: one detail is wrong. A change that touches only the Makefile, a script or a workflow, with no Go package, runs the whole module (`scripts/check-scope.sh` header). `go.work` and a root that is not a Go module also run the whole module (`internal/checks/narrow.go:99-118`).
  - G4-31: the first reader's "no direct writes in `internal/runstate` outside tests" is wrong. 56 non-test files in `internal/runstate` call `os.CreateTemp`, `os.Rename`, `os.OpenFile` and similar directly. 51 of them never import `repowrite`. The run record store and the conversation store are among them.
  - repository-confined-writes, MATCHES "absolute symlink refused": wrong for `repowrite.Root`. `Root.resolve` follows any link whose resolved target is inside the root, absolute links included (`internal/repowrite/repowrite.go:205-219`). The shared matrix accepts either answer for that case (`writertest/writertest.go:127-133`).
  - repository-confined-writes, MATCHES "no fallback on an unsupported platform": wrong. See G4-32.
  - project-dependencies-in-runs, MATCHES "three tries": wrong. The code makes four attempts: the first plus `InstallRetries = 3` retries (`internal/orchestrator/install.go:146-178`, held when `retry == 3` with `tries: retry+1`). The design says "up to three attempts … After the third" (line 99).
- The most important things the first reader missed:
  - The run record store, conversation store, intake hold, reservation lock, slack state and roughly fifty other state stores write with plain `os` calls, against `repository-confined-writes.md:193`.
  - The slack sink's log descriptor is handed straight to the detached child (`internal/slack/launch.go:59,75-76`). Design lines 74 and 143-148 forbid exactly that.
  - `Root.Truncate` cuts a file in place (`repowrite.go:287-317`), and the memory store uses it (`runstate/memory.go:1497`). Design line 122 says "A file is never truncated in place."
  - `heavy-check-capacity.md:136` says the kept place is worked out "under the same lock". The code reads the queue's record before taking the lock (`runstate/heavycheck.go:345-347`).
  - `repeated-checks.md`: the stage it calls "CI in the merge queue" is no longer `merge_group`. This repository took the forge's queue rule off main (choosing-checks-by-change.md:152). GitHub's required checks now run on the harness's candidate branch pull request, so the design's "one true duplicate" pair no longer describes this repository.
  - `heavy-check-capacity.md:146` says a CI-mode queue candidate needs no heavy check. The code adds the narrowed `merge_queue_race_check` whenever no required check is a race check (`orchestrator/mergequeuequick.go:75-90`). That does not happen in this repository, because `race` is required.

### The two questions left open

**"shadow" under `internal/checks`.** The only hit is `internal/checks/searchpath.go:61-62`. There, `shadow` is a local variable naming the `hiddenSearchPath`: the per-stage `PATH` copy with the provider executables (`claude`, `codex`) left off, so a check cannot find them. It has nothing to do with check selection. The only other `shadow` package, `internal/shadow`, compares one reviewer's findings against another's (`internal/shadow/shadow.go:1`). Nothing in `internal/checks`, `internal/config` or `internal/orchestrator` computes shadow selection, counts runs toward 50, or offers a selection switch. G4-7 and G4-11 stand.

**Which writers go through `internal/repowrite`.** 77 non-test files import it. Two strengths are in use:
- `Root` (path-based): it resolves the path, then writes by path with `os.MkdirAll`, `os.CreateTemp` and `os.Rename`. The package's own header says this does not hold across a swap between resolution and write (`repowrite.go:33-36`).
- `PinnedRoot`: Go's `os.Root` handles.

| Writer (design line 226-232 list, and line 193) | Through the primitive? | Evidence |
|---|---|---|
| Artifact writer | Yes, `Root` | `internal/artifact/store.go`, `internal/artifacthome/artifacthome.go`; matrix test `artifact/confinement_test.go` |
| Invariant writer | Yes, `Root` | `internal/invariant/store.go`; `invariant/confinement_test.go` |
| Initialization (`yoyo init`) | Yes, `Root` | `internal/cli/init.go`, `init_exclude.go`; `cli/confinement_test.go` |
| Document publication | Yes, `Root` | `internal/orchestrator/documentpublication.go` |
| Install cache, outside-file landing, pinned files | Yes, `Root` | `orchestrator/install.go`, `outsidefilelanding.go`, `pinnedfiles.go` |
| Heavy-check permits, merge queue records and events, launch records, dashboard, follow-ups, team branch updates, tracker store records | Yes, `PinnedRoot` (merge queue events: `Root.OpenAppend`) | `runstate/heavycheck.go:421-475`, `mergequeue*.go`, `launch.go`, `dashboard.go`, `followup.go` |
| Tracker exports, locks, turns, store rebuild | Yes, mixed `Root` and `PinnedRoot` | `internal/beads/export.go`, `storelock.go`, `writelock.go`, `turns.go`, `storerebuild.go` (`writelock_unix.go:75` only opens read-only) |
| Execution scratch directories, search-path copies | Yes | `internal/execution/scratch.go`, `internal/checks/searchpath.go` |
| MCP call record, intake record (`internal/intake`), project acts | Yes, `Root.OpenAppend` | `mcpcall/record.go:225`, `intake/record.go:440`, `cli/project.go:488` |
| Memory store | Mixed | Writes, appends and truncates go through `Root` (`memory.go:954,1396,1407,1493-1497`); archive rename `os.Rename` (`:1107`); lock `os.OpenFile` (`:1519`); directories `os.MkdirAll` (`:1393,1512`) |
| Run record store | **No** | `runstate/store.go:900-931` (`writeState`: `os.CreateTemp` + `os.Rename`), `:727` reservation lock, `:747`, `:822`, `:1364` append, `:1551-1605` |
| Conversation records | **No** (no `repowrite` import) | `runstate/conversation.go:1168-1175, 1328-1346, 1784-1808, 1884-1905, 1978-1981` |
| Intake hold (`runstate/intake.go`) | **No** | `runstate/intake.go:320-355, 450, 514-531` |
| ~48 other runstate stores (sweep, rerun, triage, escalation, intervention, supervision, usage window, capacity, release, hold, stall, landing, lease, claim, spend, …) | **No** | 56 files in `internal/runstate` with direct `os` writes; 5 of them (`memory`, `lanereport`, `passcursor`, `needsdesignlabelled`, `needsdesignwake`) import `repowrite` but take their lock files with `os.OpenFile` |
| Lock files generally | Mixed | Through: `PinnedRoot.OpenLock` (heavy checks), `Root.OpenAppend` (tracker locks). Direct: `runstate/store.go:727`, `gitworktree/primarylease.go:79`, `gitworktree/registry.go:170`, the runstate lock files above |
| Slack state | **No** | `internal/slack/state.go:120, 828-849`, `presence.go:131` |
| Logs of child processes | Opened through `Root.OpenAppend`, then the **descriptor is handed to the child** | `internal/slack/launch.go:52-76`. MCP servers use pipes read by the harness (`mcpcall/command.go:101-113`) |
| Cleanup | Mixed | Through: `beads/exportcleanup.go`, `gitworktree/unfinished.go`. Direct: `gitworktree/landing.go:141` `os.RemoveAll`, `backend/codex/backend.go:590` |
| Setup and home | Mixed | `cli/setup.go:1413-1420` (`os.WriteFile` + `os.Rename`); `home/migrate.go` uses `Root` mostly, `os.MkdirAll` at `:219,:614` |

No structural check refuses new direct writes, which the design lists as deferred (line 275). The run record store does have a test that names its writers (`TestNoRunRecordWriterOutsideThisPackageIsUnlisted`), but that test is about the record, not about confinement.

## checks-on-ci.md
Verdict: agree (partly stale)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G4-1 | DIFFERS | CONFIRMED | `internal/orchestrator/cichecks.go:384-416`: `describeCIEvidence` only builds text for the reviewer (`pipeline.go:10253`). Before promotion the full local checks still run (`docs/configuration.md:3849`). Design line 110 is a rule about what counts as evidence, so the gap is that nothing outside the queue consumes the evidence (build 7). |
| G4-2 | DIFFERS | CONFIRMED | `ciFlow()` (`cichecks.go:65-77`) excludes `checkingCandidate`, harness-authored, document and outside-file runs. No replay-stage CI path; `docs/configuration.md:3878`. |
| G4-3 | NOT BUILT | CONFIRMED | Same as G4-1. |
| G4-4 | NOT BUILT | WRONG | Design line 59 tells the project how to write the names. Line 139 explicitly rejects refusing an unknown name when the configuration is read. Code agrees: `config/cichecks.go:136-140` refuses only empty names. The only enforcement the design asks for is the warning, which is G4-5. Drop G4-4. |
| G4-5 | NOT BUILT | CONFIRMED | `CheckNotStarted` (`publish/cichecks.go:55`) only leads to the fallback (`orchestrator/cichecks.go:576`). No counter or report anywhere. |

Unclear settled:
- G4-6 → MATCHES. `readmodel/standing.go:630-635` (`waiting_for_ci`, `ci_waited`), `readmodel/render.go:303-304` (status: "waiting for CI: …, waited …"), `dashboard/assets/dashboard.js:1049`.

Spot-checked MATCHES: `wait_limit` 60m (`config/cichecks.go:21`), `local` refused even with the switch off (`:117-129`), 8 KiB (`runstate/state.go:373`), poll once a minute and 5 minutes unreadable (`orchestrator/cichecks.go:589,597`), and this repository's settings (`.yoyodyne/config.yaml:192-196`) all hold. No non-match found.

### Missed by the first reading
- Line 122: the queue stage runs "the short local check, then the harness pushes". The code also runs the narrowed `merge_queue_race_check` on the candidate, where it is owed and no required check is a race check (`orchestrator/mergequeuequick.go:75-90`). The design omits this.
- Line 40 still describes "today" as three full runs per change. This repository has had `ci_checks.enabled: true` since it was configured (`.yoyodyne/config.yaml:193`), so the opening is history.

### Contradictions with other designs
- `heavy-check-capacity.md:146` says "In CI mode a merge queue candidate runs only the short local check … no place is kept". That contradicts the merge queue's CI flow as built and documented (`docs/configuration.md:3884-3887`, `mergequeuequick.go:75-90`), which can add the heavy narrowed race check. checks-on-ci.md:122 is silent on it.

## choosing-checks-by-change.md
Verdict: agree (mostly a plan not built)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G4-7 | NOT BUILT | CONFIRMED | `internal/checks/narrow.go:99` is the only selection. The "shadow" hit is unrelated (see above). |
| G4-8 | NOT BUILT | CONFIRMED | `narrow.go:93-96`. |
| G4-9 | DIFFERS | CORRECTED | No rule ties *any changed Go file* to `internal/runstate`. But `runstate`'s guard tests, including `TestNoRunRecordWriterOutsideThisPackageIsUnlisted`, are in `DOCUMENT_TESTS` (`Makefile:374`). They run beside the narrowed packages when the change also touches a file outside every package (`Makefile:94-126`). The first reader's "not a runstate rule" understates this. A Go-only change still skips them until the whole-module check on the merge candidate. |
| G4-10 | DIFFERS | CORRECTED | The gist holds: the Makefile, workflows or check configuration do not force a whole run when Go packages are also touched. Corrections: (a) a change touching only non-Go files outside every package, other than Markdown and tracker exports, runs the whole module (`scripts/check-scope.sh` header, "Where it touches any other file…"); (b) `go.work`, `go.work.sum` and a root that is not a Go module also widen (`narrow.go:100-112`). |
| G4-11 | NOT BUILT | CONFIRMED | `searchpath.go:61` is a PATH helper; `internal/shadow` is reviewer comparison. No selection switch exists. |

Unclear settled:
- G4-12a → DIFFERS. No setting in `internal/config` declares the two forge race conditions. Whether the forge's queue is used is decided by `internal/queuemode` from what the adapter can establish (`publish/queuecapability.go:23-41`: eight requirements, none of them "race detector" or "full suite"). The design's "the project declares both" (line 141) is not how it is built.
- G4-12b → MATCHES. `DefaultMainCheckInterval = 4h` (`internal/config/config.go:771`); `.yoyodyne/config.yaml:465-466` `main_checks: [make race]`.

Spot-checked MATCHES: `whole_by` (`runstate/state.go:1194-1198`), `merge_queue_checks` and `merge_queue_race_check` (`.yoyodyne/config.yaml:473-477`). Both hold.

### Missed by the first reading
- Line 80 ("No other surface filters checks on its own") and line 112: `path_checks` is a second, separate filter (`.yoyodyne/config.yaml:493-497`), and so is `scripts/race-skip.sh`. The first reader noted both as missing context, but they also contradict line 80 outright.
- Line 118: the queue's own run in CI mode can include the narrowed race check (`mergequeuequick.go:75-90`), not "only the quick checks GitHub does not run".

### Contradictions with other designs
- Line 80 against `one-owner-for-each-guarantee.md:371`. The guarantees design lists "path checks" as an existing, separate check path still to be moved onto the runner, which acknowledges the second filter this design says must not exist.
- Line 80 and `repeated-checks.md:98` against what is built: the getting-started walk is already narrowed by its own path filter. See G4-22.

## heavy-check-capacity.md
Verdict: agree (partly stale)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G4-12 | NOT BUILT | CONFIRMED | No `heavy_checks_total` in any `.go` file or user guide; `grantable` (`runstate/heavycheck.go:626-675`) counts per check. |
| G4-13 | DIFFERS | CORRECTED | Right, but the code is `internal/config/heavychecks.go:31-53` (`HeavyCheck` and `heavyCheckMatches`), not 42-49. Validation matches keys against checks, landing, document, main, intent and path checks and install steps (`:58-96`), wider than "the project's checks". |
| G4-14 | DIFFERS | CONFIRMED | `.yoyodyne/config.yaml:241-243`. Design line 198 itself defers the 2/3 setting until the total is built, so only lines 66-76 overstate it. |
| G4-15 | DIFFERS | CONFIRMED | `heavycheck.go:1-12`: own file under the state root, file lock beside it. |
| (total half of kept place) | NOT BUILT | CONFIRMED | Follows from G4-12. |

Unclear settled:
- G4-16 → NOT BUILT. Problems list only *kept* permits (`readmodel/heavychecks.go:59-73`, `record.Stuck()`). No code compares a wait against one hour or the check's limit. `Waited()` is used only for display and measurement (`checks/runner.go:571`, `readmodel/heavychecklimits.go:180`).
- G4-17 → MATCHES. `MaxCheckStageContinuations = 2` and `CheckRerunAllowanceFactor = 3` (`internal/runstate/checkstagecontinue.go:43,49`), used in `orchestrator/pipeline.go:7719-7728`. They live in `runstate` and `orchestrator`, not in `internal/checks` as "the checks plan" implies.

Spot-checked MATCHES: three grant orders (`heavyCheckBefore`, `heavycheck.go:318`), no place kept at count one (`:626-649`), and the 6-hour abandoned-wait release (`:84,:582`) hold.

### Missed by the first reading
- Line 136 says the kept place is worked out "from the merge queue's own record each time it grants a permit, under the same lock". The code reads the queue's needs "on every poll, before the record's lock is taken" (`runstate/heavycheck.go:345-347`; wired at `cli/run.go:684`).
- Line 115, the long-wait problem: not built (G4-16).
- Line 109 speaks of process-group emptiness. The code decides "gone" from a per-permit lock file the check's processes inherit (`heavycheck.go:28-35`). The effect is the same, but the design should name the mechanism.

### Contradictions with other designs
- Line 146 against `docs/configuration.md:3884-3887` and the merge queue CI flow (see checks-on-ci above).
- Lines 55 and 82 state permits are in the shared reservation as fact. `one-owner-for-each-guarantee.md:179` says "Until item 6 lands, permits keep their own lock", and line 88 of this design says the same. Lines 55 and 82 should be hedged.

## repeated-checks.md
Verdict: agree (mostly a plan not built)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G4-19 | NOT BUILT | CORRECTED | Right, but the design's equivalence and kept-result text is at lines 48-64, not 22-37 (22-37 is front matter). |
| G4-20 | NOT BUILT | CONFIRMED | `.github/workflows/ci.yml:3-10`; no `if:` in the file. |
| G4-21 | DIFFERS | CONFIRMED | `.yoyodyne/config.yaml:192-196`; the table at lines 70-77 predates checks on CI and the harness's queue. |

Unclear settled:
- G4-22 → DIFFERS. The getting-started walk *is* already narrowed locally, by its own path filter: `path_checks: - command: make adoption, paths: scripts/walk-adoption.paths` (`.yoyodyne/config.yaml:493-495`; `Makefile:405-413`, where `adoption` is the old name for `getting-started`). Line 98's "not a filter of its own" is contradicted by what runs. On CI the `getting-started` job runs on every pull request (`ci.yml:147`).

### Missed by the first reading
- Lines 76 and 85: "CI in the merge queue" is GitHub's `merge_group` run, and the push run duplicates it. This repository lands through the harness's own queue, with the forge's queue rule off main (`choosing-checks-by-change.md:152`). With checks on CI, the queue pushes each candidate to a candidate branch with its own pull request, where the required checks run (`docs/configuration.md:3888-3891`). The pair the first removal targets therefore needs restating as candidate pull request against push to main.
- Line 98: see G4-22.

### Contradictions with other designs
- Line 98 against what `one-owner-for-each-guarantee.md:371` treats as an existing path (path checks). See choosing-checks-by-change above.
- Line 74 ("harness's checks before promotion … macOS, this machine") against `choosing-checks-by-change.md:118`: with checks on CI, the queue's local run keeps only the quick checks, and GitHub runs the rest.

## project-dependencies-in-runs.md
Verdict: agree (current, a few sentences stale). Add the retry count to the stale list.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G4-23 | DIFFERS | CONFIRMED | `orchestrator/install.go`, `runstate/install.go` built; line 108 stale. |
| G4-24 | DIFFERS | CONFIRMED | `docs/configuration/runs.md` ("A step is run the way a check is", about line 627). |
| G4-25 | DIFFERS | CONFIRMED | `runs.md`, "After that, a step runs again …" and "The landing checks and the merge queue run a step once …". |

Unclear settled:
- G4-26 → piece 5 MATCHES (npm and pip examples in `docs/configuration/runs.md` around 600-620). Piece 6 NOT BUILT: the outside-project trial item "runs the first outside-project trial on Mason's Obsidian plugin" (`yoyodyne-f07p`) is open in `.beads/issues.jsonl`.

MATCHES that are not: "three failure cases; five-minute waits, three tries". The code makes four attempts, the first plus three retries (`orchestrator/install.go:146-178`; `runstate/install.go:158-163` says "tried again"). The design says "up to three attempts … After the third" (line 99). The user guide's "tries again, up to three times" agrees with the code, not the design.

### Missed by the first reading
- Line 99: attempt count, as above.
- Line 98: before a *developer turn*, a failure on inputs the run changed does not go back as a failed check; the turn goes ahead (`runs.md`, the third bullet under "sorted by whose failure it is"). The first reader listed this as missing, but it also contradicts line 98, which says it goes back as a failed check and spends a repair round in every case.

### Contradictions with other designs
- Line 89 and line 113 rely on `repository-confined-writes.md`. The cache folder does go through `Root` (`orchestrator/install.go`), but the install record of what has installed on this machine (`install-installed.jsonl`) should be checked against the confined-writes claims before either is revised.

## repository-confined-writes.md
Verdict: should be **mostly a plan not built**. Built: a path-resolving `Root` and an `os.Root`-based `PinnedRoot`, used by the document writers and about half the stores. Not built: `openat2`/`openat` walks, held-directory checks before and after a change, exchange or no-replace renames, link counts, batched appends, pipe copying, the removal holding directory, the platform and filesystem list, hooks, and the claim that every state store writes through the primitive.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G4-18 | DIFFERS | CONFIRMED | `internal/repowrite/pinned.go:14-48` (`os.OpenRoot`); `pinned.go:239-250` plain `Rename`; no `openat2`, `renameat`, `RENAME_` in the package. |
| G4-27 | DIFFERS | CONFIRMED | `repowrite.go:63` `.yoyo-write-*.tmp`; `RemoveFile` is `os.Remove` (`repowrite.go:492-511`). |
| G4-28 | NOT BUILT | CORRECTED | Right, but the design lines are 51, 69, 127, 135 and 168, not "58, 127-130". No `Nlink` in the package. `Unchanged()` is at `pinned.go:57-68`. |
| G4-29 | NOT BUILT | CONFIRMED | `Root.OpenAppend` returns `*os.File` (`repowrite.go:250-282`); flags at `append_unix.go:14`. |
| G4-30 | NOT BUILT / UNCLEAR | CORRECTED | A shared matrix exists: `internal/repowrite/writertest/writertest.go` (7 static topologies, `cases()` at :108), run by artifact, invariant, cli init, gitworktree, slack and two runstate tests. None of the design's race, late-link, cross-device, lock-link, removal-swap or unsupported-filesystem rows exist, there are no hooks, and there is no `testing.Testing()` guard in `repowrite`. Partly built. |
| G4-31 | DIFFERS (MATCHES/UNCLEAR inside) | CORRECTED | DIFFERS, more broadly than the first reader said. The run record store, conversation store, intake hold, slack state and ~48 more stores write with plain `os` calls. See the writer table above. The design is at line 193, not 165. |

Unclear settled:
- G4-32 → DIFFERS. There is no kernel-version or filesystem check. `OpenPinnedRoot` refuses only `js` and `plan9` (`pinned.go:24-26`); `Root` has no platform check; `append_other.go` gives other platforms appends without `O_NOFOLLOW`. The design's supported list is at lines 172-177, not 130-150.

MATCHES that are not:
- "absolute symlink target refused; relative one inside the root followed": `Root.resolve` uses `filepath.EvalSymlinks` and accepts any resolved target inside the root, absolute ones included (`repowrite.go:205-219`). The matrix expects either outcome for a link to elsewhere in the repository (`writertest.go:127-133`). Design line 86 requires refusal. Also `PinnedRoot.OpenDirectory` refuses every symlinked directory (`pinned.go:76-78`), stricter than line 84.
- "no fallback to ordinary writes on an unsupported platform": see G4-32.

### Missed by the first reading
- Line 193: most state stores do not use the primitive (`runstate/store.go:900-931`, `runstate/conversation.go:1784-1808`, `runstate/intake.go:320-340`).
- Lines 74 and 143-148: a child is handed the log descriptor (`internal/slack/launch.go:52-76`).
- Line 122 ("A file is never truncated in place") and line 68 ("Checking and changing are not separate steps"): `Root.Truncate` cuts in place (`repowrite.go:287-317`, used at `runstate/memory.go:1497`). Every `Root` write resolves first and then acts by path (`repowrite.go:320-357`), which the package header concedes (`:33-36`).

### Contradictions with other designs
- `orchestrator-boundaries.md:95` also says the product stores are "written through the confined writer". It shares line 193's error, so both need the same correction.
- `follow-ups.md:240`, `mcp-tools-through-the-harness.md:192`, `scheduled-message-intake.md:196` and `reading-published-prices.md:65` each promise writes "through the confined writer". The follow-up, MCP-call and intake *records* do go through it (`runstate/followup.go`, `mcpcall/record.go:225`, `intake/record.go:440`), so they agree. The runstate intake *hold* (`runstate/intake.go`) does not.
