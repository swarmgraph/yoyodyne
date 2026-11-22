# yoyodyne-ifd.437.84: design documents against the code, group 4: checks and the run sandbox

One of nine reports comparing the design documents in `docs/designs/` with the code, made at the operator's request of 2026-11-21 for a review of every design against the current code. This one covers group 4, checks and the run sandbox.

- **Read on:** 2026-11-21, between 15:40 and 15:48 PST. Main moved from `2447eeaab` to `fe2db3d37` at 15:41, a minute into the reading; that change was 22 files, mostly the team feedback repair code in `internal/runstate`.
- **Read by:** a read-only reader in the operator's assistant's session, on Sonnet 5.5. It read code and ran nothing, tests included.
- **Checked by hand:** the operator's assistant checked three findings against the code. G4-12 with G4-14, and G4-18, held. G4-7 held for the analyser and the typed policy; the word "shadow" does appear in one file under internal/checks, which was not examined. Every other finding is unchecked.
- **How to use it:** this is evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Classes:** `MATCHES`, the code does what the design says; `DIFFERS`, the code does something else; `NOT BUILT`, the design calls it future or the code is absent; `UNCLEAR`, not established.

## Summary
- Documents: 6. Claims checked: about 62. MATCHES 33, DIFFERS 9, NOT BUILT 15, UNCLEAR 5.
- Read-only audit. I did not run anything. Budget was enough to finish every document, but `repository-confined-writes.md` was checked at the level of the primitive's package (`internal/repowrite`) and not writer by writer.
- Five mismatches that matter most:
  1. G4-7: `choosing-checks-by-change.md` describes one typed policy with path rules, an analyser, a shadow rollout and a switch. The code has Go-package narrowing (`internal/checks/narrow.go`, the `YOYODYNE_CHANGED_GO_PACKAGES` variable) and `path_checks`; none of the rest exists.
  2. G4-18 and G4-27: `repository-confined-writes.md` specifies `openat2`, `renameat2` swaps, `.yoyodyne-tmp-` names, a holding directory and link-count checks. `internal/repowrite` is built on Go's `os.Root` with plain renames and a different temp-file name.
  3. G4-12 and G4-14: `heavy-check-capacity.md` says `heavy_checks_total` and counts of 2/2/3 are this repository's configuration. No such setting exists; the repository sets `make race: 1`, `make test: 2`.
  4. G4-13: the same document says keys are matched "exactly". The code matches a prefix followed by a space and lets the longer name win.
  5. G4-5: `checks-on-ci.md` says a never-started required check on three runs in a row files a warning for the development manager. No code does that.
- Consolidation proposals:
  - Merge `repeated-checks.md` into `choosing-checks-by-change.md`: both rest on the same unbuilt analyser, shadow and "50 runs" rule (repeated-checks.md:64, choosing-checks-by-change.md:171).
  - `project-dependencies-in-runs.md` is built; shrink it to the decision and rejected alternatives and point to `docs/configuration/runs.md` (line 557) for the behaviour.
  - Move the four-case "what verifies the merge candidate" text (choosing-checks-by-change.md:116-154) to the merge queue design, which already owns the GitHub required-checks conditions.
  - Split `repository-confined-writes.md` into what `repowrite` does and a clearly marked target contract.

---

## checks-on-ci.md — Checks on CI: a short local check, the full suite on the forge before review, and a switch back to local checks
Verdict: partly stale
Size: 18 KB. Last changed: 2026-11-18.

### Findings
- MATCHES (setting `execution.ci_checks` with `enabled`, `local`, `required`, `wait_limit`): `internal/config/cichecks.go:25-41`.
- MATCHES (`enabled` false by default; `wait_limit` default 60m): `cichecks.go:19`. `docs/configuration.md:3920` says "1h", which is the same.
- MATCHES (a `local` entry that matches no command in `checks` is refused, even with the switch off): `cichecks.go:116-124`.
- MATCHES (`required` must not be empty when enabled; refused for a forge adapter that cannot do the four things): `cichecks.go:136-145`. GitHub is the only adapter.
- MATCHES (short local check, then push and draft pull request, then wait, no slot held): `internal/orchestrator/cichecks.go:98-186`; `docs/configuration.md:3842`.
- MATCHES (CI failure recorded as a failed check, 8 KiB in total, shared equally): `orchestrator/cichecks.go:318-346` uses `runstate.MaxCheckOutputBytes` (8 KiB, `runstate/state.go:373`). `publish/cichecks.go:31-33` still reads 200 lines and up to 16 KiB per check from the forge, then the orchestrator cuts to the share.
- MATCHES (cancelled, skipped, never started, unreadable forge, or `wait_limit` each fall back to full local checks with no repair spent): `orchestrator/cichecks.go:569-587`, `:597` (forge unreadable for 5 minutes decides the wait).
- MATCHES (a later CI result for the same commit is recorded, and a failure from either counts): `orchestrator/cichecks.go:459-510` (`lateCIResult`).
- MATCHES (restart keeps the wait and pushes nothing new): `orchestrator/cichecks.go:219`, `:417-428`; `docs/configuration.md:3847`.
- MATCHES (this repository's `local` is fmtcheck, vet, build, test-macos; `required` names only jobs in `ci.yml`): `.yoyodyne/config.yaml:192-196`; `.github/workflows/ci.yml` jobs `test`, `race`, `vet`.
- MATCHES (a refused push blocks the item for the development manager): `orchestrator/cichecks.go:187-217`.
- MATCHES (merge-queue candidate asks a cancelled or skipped check to run once more): `orchestrator/mergequeuecheckrerun.go`.
- G4-1 [DIFFERS] The doc says CI results are typed evidence for integration in every flow; the user guide says the forge's results are not yet integration evidence outside the merge queue.
  - Document: line 110: "A CI result counts as evidence only when the harness's own action reads it from the forge for the exact candidate commit … and records the digest of the workflow file at that commit."
  - Code: `docs/configuration.md:3845-3846` — "Until the forge's results are themselves integration evidence (a later build), the full checks also run on this machine over the approved candidate before it is promoted." In code, outside the queue the digest is recorded (`orchestrator/cichecks.go:370`) and shown to the reviewer as text (`orchestrator/pipeline.go:10253`); the promotion path does not consume it. The queue path does (`orchestrator/mergequeueforgechecks.go`).
  - What to change in the document: say the queue path uses typed evidence and the pre-review flow only shows it to the reviewer, or finish build 7 and say so.
- G4-2 [DIFFERS] The doc's "How this fits" says a replay with the queue off gets the short check and a CI wait; the user guide says the other stages "arrive in later builds".
  - Document: line 122: "A replayed revision with the queue off gets the short local check, then the harness pushes it and waits…"
  - Code: `docs/configuration.md:3881` — "The other stages of the flow arrive in later builds." `ciFlow()` (`orchestrator/cichecks.go:65`) limits the flow to a developer's change before review. I did not find replay-stage code.
  - What to change in the document: mark build 6 (replay stage) as not built, or confirm and change the guide.
- G4-3 [NOT BUILT, in the code] Build 7, CI results as typed evidence in the reviewer's evidence, is only text for the reviewer (see G4-1).
- G4-4 [NOT BUILT, in the code] Required check names "must be the forge's own names".
  - Document: line 59. Nothing validates this at read time, which the doc itself says (line 139). Only the warning (G4-5) would catch it, and that is not built.
- G4-5 [NOT BUILT, in the code] Warning report after three runs in a row where a required check never started.
  - Document: line 100: "When the same required check has never started on three runs in a row, the harness files a warning report for the development manager".
  - Code: searched `internal/orchestrator`, `internal/publish`, `internal/runstate` for a counter or report; none. The work item that built the doc change (`yoyodyne-ifd.437.70`) corrected the doc, and the build 9 listed at line 153 has no item in `.beads/issues.jsonl` that I could find. Code only records the reason on each run (`ciWaitDecided`, `orchestrator/cichecks.go:577`).
  - What to change in the document: mark build 9 as not built, with the item id once filed.
- UNCLEAR G4-6: the dashboard shows "waiting for CI, with the time waited" (line 69). `docs/configuration.md:3843` says yes; I did not read the dashboard code.

### Missing from the document
- The poll rule: a watching `yoyo work` session reads the forge about once a minute (`orchestrator/cichecks.go:589`) and treats five minutes of unreadable forge as the end of the wait (`:597`). The doc only says "the forge cannot be read".
- After the fallback, the harness reads the forge once more when the local checks pass and again before promotion (`docs/configuration.md:3869-3875`).
- With the merge queue on, the race check is told only the touched packages while no `required` name is a word of `merge_queue_race_check` (`docs/configuration.md:3893-3895`). The doc defers this to the queue design; check that the queue design says it.
- Reading the CI log can itself fail; that counts as the forge being unreadable, not a failure of the change (`docs/configuration.md:3860`).

### Overlap with other documents
- With `integration-through-a-merge-queue.md` (not in my group): checks-on-ci.md:122 and :133 restate the second-review-beside-the-wait rule. Agree in intent; I did not compare line by line.
- With `choosing-checks-by-change.md:118` and `:125`: both say the queue's own run keeps only the quick checks and "CI is never narrowed". They agree.
- With `heavy-check-capacity.md:177`: both say the CI-mode candidate takes a permit only for a heavy short check. They agree.

### Could be cut or moved
- The three paragraphs of revision history in the front matter (lines 8-36) hold the build-8 and 11-18 narrative; the body text already holds the result.
- "Alternatives rejected" is fine; the "16 KiB each" item (line 138) records an old number and can go.

---

## choosing-checks-by-change.md — Choosing checks by what a change touches, through one shared policy
Verdict: mostly a plan not built (the merge-candidate cases are the part that is closer to built)
Size: 25 KB. Last changed: 2026-11-20.

### Findings
- G4-7 [NOT BUILT, in the code] "One typed policy in the checks package decides every check for a run", with the inputs base and candidate, changed paths, path rules and an analyser.
  - Document: line 73: "One typed policy in the checks package decides every check for a run."
  - Code: `internal/checks/narrow.go:99` `NarrowGoPackages(root, changed)` only maps changed files to the nearest Go package directory and returns `./...` for `go.mod`/`go.sum`/`vendor`. It sets `YOYODYNE_CHANGED_GO_PACKAGES` (`narrow.go:17`). The Makefile (lines 185-230) and `scripts/check-scope.sh` read it. There is no policy type, no analyser, and no per-check "whole, narrowed or skipped" decision. `docs/configuration.md:4181` says "no setting declares [an analyser] yet".
  - What to change in the document: describe the built mechanism (the variable, `check-scope.sh`, `race-skip.sh`, `path_checks`) and move the policy, analyser and path rules into a short "not built" section.
- G4-8 [NOT BUILT, in the code] The Go analyser: dependent packages, their tests, test data, for macOS and Linux.
  - Document: lines 86-92. Code: `narrow.go:93-96` says "What it cannot see is a package that depends on a touched one — that is what the landing check over the whole module is for." No analyser exists.
- G4-9 [DIFFERS] "Any changed Go file selects the tests of `internal/runstate`" in this repository.
  - Document: line 96 and lines 203, 215. Code: no mention of `runstate` in `internal/checks/narrow.go` or `scripts/check-scope.sh`; a change to `internal/orchestrator` alone narrows `make test` to that package plus the document tests (`check-scope.sh` header). `Makefile` has `DOCUMENT_TESTS` for tests that read files outside a package, not a runstate rule. Item `yoyodyne-ifd.437.72` is open for the check of six designs against the code.
  - What to change in the document: delete the claim, or file the work to add it.
- G4-10 [DIFFERS] The "whole suite runs when" list (lines 100-106).
  - Document: lines 102-106: "the Makefile, a CI workflow or the check configuration changed; a changed path is claimed by no path rule …; the base commit is not an ancestor".
  - Code: `narrow.go:99-130` widens only for module files and `vendor/`. A change to the Makefile or a workflow leaves the race check as it was and runs `make test` with the document tests (`scripts/check-scope.sh` header, "Neither check counts the tracker's exports"). Where Git cannot read the change, the whole module runs.
  - What to change in the document: use the built list: module files, vendor, Git unreadable.
- G4-11 [NOT BUILT, in the code] Rollout: shadow selection, "50 shadow runs with no miss", a switch and a miss report (lines 168-172, builds 4 and 5).
  - Code: searched `internal/checks`, `internal/config` for a shadow or selection switch; the only "shadow" hit is an unrelated search-path helper (`internal/checks/searchpath.go:61`). Narrowing is on wherever a check reads the variable.
- MATCHES (formatting and vet run whole; narrowing applies to the checks before review; the merge candidate is checked whole once more over `./...`): `Makefile:196-203`, `scripts/check-scope.sh` header.
- MATCHES (`merge_queue_checks` and `merge_queue_race_check` exist as settings): `.yoyodyne/config.yaml:473-477`; `docs/configuration.md:307-308`.
- MATCHES (the record names the queue run in `checks_passed.whole_by`): `internal/runstate/state.go:1198`, with `WholeLeftToQueue` beside it.
- MATCHES (case 2 with GitHub's required checks as the full check when `ci_checks` is on): `orchestrator/mergequeueforgechecks.go`, `mergequeuequick.go`.
- UNCLEAR G4-12a: case 3, the forge's own merge queue, with "the project declares both [conditions]" (lines 137-141). I did not find a setting that declares them; I searched `internal/config` for `forge` queue conditions only briefly.
- UNCLEAR G4-12b: the "scheduled full race check on main every four hours" (line 136, 154). `.yoyodyne/config.yaml` has `main_checks` (`docs/configuration.md:306`); I did not confirm the four-hour interval.

### Missing from the document
- `path_checks` exists (`docs/configuration.md:3645-3716`; this repository uses it for `make adoption` and `make codex-resume`). It is the nearest built thing to the doc's "path rules", and the doc never mentions it.
- The untouched-test rerun (`internal/checks/untouchedrerun.go`; `docs/configuration.md:4189`): a failure confined to tests the change does not touch is rerun once on the same commit before any repair is spent. It is a selection-adjacent rule and nowhere here.
- `make race` skips a change that is only Markdown outside every Go directory (`scripts/race-skip.sh`).
- A change to an install step's input makes every check run whole (`docs/configuration/runs.md`, after line 620). The doc `project-dependencies-in-runs.md` says it too.

### Overlap with other documents
- With `repeated-checks.md:64`: both define the same "50 runs with no miss" rollout. They agree; neither is built.
- With `checks-on-ci.md:125`: "Narrowing applies to local checks before review" matches (checks-on-ci.md:125 and this line 114).
- With `merge-queue-pace.md` (not in my group): case 2's cost paragraph (line 136) restates its one-landing-an-hour argument.

### Could be cut or moved
- Lines 116-154 (the four merge-candidate cases and the case 2 history) belong with the merge queue design.
- The revision narrative at lines 69 (three sentences of dates) duplicates the front matter.
- "Alternatives rejected" entries at lines 192-197 repeat the cases above.

---

## heavy-check-capacity.md — Capacity for heavy checks: one count per machine home, durable waiting, and recovery
Verdict: partly stale
Size: 22 KB. Last changed: 2026-11-16.

### Findings
- G4-12 [NOT BUILT, in the code] `execution.heavy_checks_total` and the cap across all heavy checks.
  - Document: line 64: "`execution.heavy_checks_total` is how many heavy checks, of any kinds together, may run at once on this machine home."
  - Code: no hit for `heavy_checks_total` in any `.go` file or in `docs/configuration.md`; `internal/runstate/heavycheck.go` `grantable` (line 626) counts per check only. Items `yoyodyne-ifd.429.227` (build) and `.429.228` (this repository's setting) are in `.beads/issues.jsonl`.
  - What to change in the document: say the total is not built, or drop it from the settings section.
- G4-13 [DIFFERS] Keys must match a declared check "character for character".
  - Document: line 62: "Each key is a check command exactly as the project declares it"; line 66: "each key must match a check the project declares, character for character".
  - Code: `internal/config/heavychecks.go:42-49` (`heavyCheckMatches`) — a command counts under a name when it equals the name or begins with it followed by whitespace; the longer name wins. `docs/configuration.md:3473-3477`: `make race RACE_PACKAGES=…` counts under `make race`. A name given no number gets one place (`:3478`).
  - What to change in the document: describe the prefix match, which is what lets `make race CHANGED_GO_PACKAGES=…` be heavy.
- G4-14 [DIFFERS] This repository's configuration is `make race` 2, `make test` 2, total 3.
  - Document: lines 68-74 and the last build line (198).
  - Code: `.yoyodyne/config.yaml:241-243` — `make race: 1`, `make test: 2`; no total.
  - What to change in the document: state the current values and say the 2/2/3 numbers wait on the total build.
- G4-15 [DIFFERS] Permits live in the shared reservation store under the reservation lock.
  - Document: lines 82-84 say they are "counted in the same reservation store as developer slots". Line 88 hedges: "Until permits move into the reservation… one record per machine home … file lock beside it".
  - Code: `internal/runstate/heavycheck.go:1-20` (own record under the state root, file lock beside it, one file per permit for process-group liveness). The move has not happened.
  - What to change in the document: put the built arrangement first and the planned move second.
- MATCHES (four states waiting, running, exited, released; released only once the process group is empty): `heavycheck.go:10-30`.
- MATCHES (a run waiting keeps its developer slot; time limit starts at launch): `docs/configuration.md:3478-3490`.
- MATCHES (three orders: landing or promotion, then preserved-work continuations, then the rest; first asked first): `docs/configuration.md:3492-3497`; `heavyCheckBefore` at `heavycheck.go:318`.
- MATCHES (a place is kept for the landing check while the queue holds an entry; none where the count is one; landing group granted the full count): `heavycheck.go:626-662` (`grantable`, `keeping`). Item `yoyodyne-ifd.429.193`.
- MATCHES (restart: running or exited permits with live processes keep their place, gone ones are released, waiting ones keep order): `heavycheck.go:567-612`, header lines 36-42.
- NOT BUILT, total half of the kept place (line 168, "where the total is one, no place is kept in the total"): follows from G4-12; `grantable` has no total.
- UNCLEAR G4-16: "A wait longer than the greater of one hour and the check's time limit is listed under problems" (line 115). I found `Stuck()` (`heavycheck.go:198`, a permit holding a place for processes nobody can confirm gone) and a six-hour `heavyCheckAbandonedKept` (`:84`), but not a one-hour wait rule. I did not read `internal/cli/status*.go` closely.
- UNCLEAR G4-17: reruns of a check stopped by its limit, "at most two reruns within three times its limit" (line 143). I did not find the numbers in `internal/checks/timelimit.go`.

### Missing from the document
- The measurement side is built: `yoyo heavy-checks measurements` proposes a number for each check (`docs/configuration/runs.md`, after line 690). The doc calls it a future build (yoyodyne-ifd.429.48, still open).
- An install step is run behind a place where `heavy_checks` names its command (`docs/configuration/runs.md`, "A step is run the way a check is"). The doc's list of what takes permits (lines 175-177) leaves this out.
- `ReleaseByHand` (`heavycheck.go:1064`) lets an operator release a stuck permit; the doc has no manual release.

### Overlap with other documents
- With `checks-on-ci.md:124` and `:127`: agree (CI full suite takes no permit; fallback takes permits).
- With `choosing-checks-by-change.md:170` and `:181`: "shadow runs take a permit" — agree, but shadow runs are not built.
- With `one-owner-for-each-guarantee.md` (not in my group): guarantee 3 and migration items 6, 16, 20 are cited; I did not check their status.

### Could be cut or moved
- The "Builds" list (lines 190-198) restates the sections above and names items that have landed or not; replace with one status line each.
- Four of the "Alternatives rejected" items (semaphore, memory count, short names, own lock) restate points made in the body.

---

## repeated-checks.md — Repeated checks: when two runs are the same run, and the first one removed
Verdict: mostly a plan not built
Size: 12 KB. Last changed: 2026-11-20.

### Findings
- G4-19 [NOT BUILT, in the code] The five-part equivalence record (commit, definition digest, toolchain, platform, environment) and keeping a passed result for a later commit.
  - Document: lines 22-37. Code: no equivalence or kept-result type in `internal/checks` (files: `runner.go`, `failure.go`, `touched.go`, `untouchedrerun.go`, `narrow.go`, `permits.go`, …). No setting for it in `docs/configuration.md`.
- G4-20 [NOT BUILT, in the code] The first removal: skip the CI run on push to main when the queue passed the same commit.
  - Document: lines 91-97: "the CI run on the push to main is skipped when, and only when, the merge queue's run passed the same commit…"; one-week shadow.
  - Code: `.github/workflows/ci.yml:3-10` still triggers on `push: branches: [main]` with no skip condition (no `if:` lines in the file). No shadow record found.
  - What to change in the document: keep as a plan and say nothing is switched on; the doc already says "No live check is removed by this design", which is true.
- G4-21 [DIFFERS] The inventory at "c95f6af84aaa" (lines 66-73) names stages and where they run.
  - Document: line 66 table. Code: with `ci_checks` on (this repository, `.yoyodyne/config.yaml:192`) the checks before review are the short local check, and the full suite runs on the forge before review. The table's "harness's checks before review: macOS, this machine, the candidate commit" is now only the short set. The doc's own "How this fits" says so (line 111) but the inventory was not redone.
  - What to change in the document: update the table for the current stages, or drop it and point to `checks-on-ci.md`.
- UNCLEAR G4-22: "Release packaging and the `getting-started` check … are not touched" (line 97). The README-walk rename to `getting-started` is in the Makefile `.PHONY` line (`Makefile:58`), so the name matches; I did not check that narrowing is absent.
- MATCHES (the push run on main is the one true duplicate; PR run and queue run are different commits): `ci.yml` triggers `pull_request`, `merge_group`, `push`.
- MATCHES (decisions belong to `internal/checks` under guarantee 9): by design; consistent with `heavycheck` and runner placement.
- Line 122: "The code of `internal/checks` … were not read." This sentence is itself the admission that the doc was not checked against code.

### Missing from the document
- The built duplicate-avoidance: one rerun of tests the change does not touch, on the same commit, before a repair is spent (`internal/checks/untouchedrerun.go`; `docs/configuration.md:4189-4215`; `scripts/rerun-failing.sh`). It is the only built "same run again" rule here and the doc has none of it.
- `make race` on main reruns only the failing tests (`Makefile:236-244`).
- The harness checks the approved merge candidate once over `./...` before promotion (`Makefile:196-203`); the doc's pair "before review and before promotion" does not name this.

### Overlap with other documents
- With `choosing-checks-by-change.md:171` and `:172`: same 50-run and shadow rollout. Agree.
- With `checks-on-ci.md:126`: both say the local full run and CI on the pull request become one run. Agree.
- With `choosing-checks-by-change.md:179`: both say narrowing release packaging and the getting-started check will use the shared policy. Agree.

### Could be cut or moved
- The inventory table and "What each pair is" (lines 64-90) read as a one-off audit; move to a decision record.
- The "Evidence" section (lines 120-122) is history.
- Everything after "No live check is removed" can be merged into `choosing-checks-by-change.md` as one section on keeping results.

---

## project-dependencies-in-runs.md — Installing a project's dependencies before a developer run
Verdict: current (built; a few sentences are stale)
Size: 13 KB. Last changed: 2026-10-28.

### Findings
- MATCHES (`install` list with `name`, `run`, `inputs`, `outputs`, `time_limit`; empty by default; time limit 10 minutes): `internal/config/install.go:38`, `docs/configuration/runs.md:557-585`.
- MATCHES (config refused for untracked input, unignored output, output outside root, duplicate output): `docs/configuration/runs.md:587-606`.
- MATCHES (`YOYODYNE_INSTALL_CACHE`; cache folder under the state directory): `internal/checks/install.go:27`; `runs.md` says `projects/<id>/state/install-cache`.
- MATCHES (step runs before the developer's first turn, and before check stages and repair rounds when the input digest differs or an output is missing): `internal/orchestrator/install.go:17-33`; `runs.md:631-640`.
- MATCHES (three failure cases; five-minute waits, three tries; no repair spent): `internal/runstate/install.go:162-163` (`InstallRetries = 3`, `InstallRetryInterval = 5 * time.Minute`); `orchestrator/install.go:177`.
- MATCHES (record: name, digest of inputs, start, duration, result, last 200 lines at most 16 KiB): `runstate/install.go:56-57`.
- MATCHES (`install` is the operator's, refused for a role's write; `yoyo doctor` shows it): `runs.md` last paragraph and line 598.
- G4-23 [DIFFERS] "Until the first four build pieces land, a project whose checks need packages … will fail that rule on every run."
  - Document: line 108. Code: pieces 1 to 4 are built (`internal/orchestrator/install.go`, `mergequeueinstall.go`, `runstate/install.go`). The paragraph and the "Work on such a project should not be started" advice are stale.
  - What to change in the document: delete the paragraph; say the build pieces landed.
- G4-24 [DIFFERS] "How the harness runs a declared check today was not read for this design; the first build piece states it" (line 92).
  - Code: `docs/configuration/runs.md` ("A step is run the way a check is… in a process group ended with it, and behind a place among the machine's heavy checks") states it.
  - What to change in the document: replace with the stated behaviour.
- G4-25 [DIFFERS] When the step runs: the doc lists "before the developer starts", "before each check stage, repair round, and check of a merge candidate".
  - Code: `runs.md:631-640` adds the landing checks and the merge queue's checks of a candidate and of its base, and every continued attempt. Failure handling differs there: the landing checks and queue run a step once and do not sort its failure; a queue stage whose step fails is run again on a later pass.
  - What to change in the document: add these two stages and their different failure handling.
- NOT BUILT (build pieces 5 and 6): the guide examples for npm and one other manager exist (`runs.md:559-620`); piece 6, a trial on a TypeScript project, is not shown in the code. Unclear which; mark UNCLEAR G4-26.

### Missing from the document
- A repair turn on changed inputs goes ahead rather than blocking: "Before a developer turn the turn goes ahead… the checks after it hand the failure back" (`runs.md:663-665`).
- The record of what has installed on this machine is a file, `projects/<id>/state/install-installed.jsonl`, and a project whose steps passed before it existed starts empty (`runs.md:677-681`).
- A change to a step's input makes the checks run whole and every path check run (`runs.md:640-642`).

### Overlap with other documents
- With `checks-on-ci.md:77` area: both say the checkout a step has prepared is used by the short check and the fallback. Agree.
- With `mcp-tools-through-the-harness.md` (outside my group) the inventory shows a link to this doc; not compared.
- With `docs/configuration/runs.md:557-700`: the user guide restates nearly all of this doc. They agree.

### Could be cut or moved
- "The problem" (lines 30-36), the build-pieces list (lines 129-152) and "What is enforced in code" / "What a reviewer can check" repeat the body once built. Keep decision, rejected alternatives and reasons.

---

## repository-confined-writes.md — Repository-confined writes: physical containment for every owned writer
Verdict: partly stale (the contract describes a stronger primitive than the one built)
Size: 20 KB. Last changed: 2026-11-19.

Note on scope: the document is about writes the harness itself makes (artifact, invariant, initialization writers, state stores, locks, logs). It says nothing about where a developer run's own sandbox may write; the rule that a run writes only to its worktree and to `.git` is in `CLAUDE.md` and the operating rules, not here. See "Missing".

### Findings
- G4-18 [DIFFERS] The primitive's mechanism.
  - Document: lines 81-82: "Linux … `openat2` with `RESOLVE_BENEATH` … macOS … `openat` and `O_NOFOLLOW` … At most 40 links are followed"; lines 107 and 115: `renameat2` with `RENAME_NOREPLACE` / `RENAME_EXCHANGE`, `renameatx_np` with `RENAME_EXCL` / `RENAME_SWAP`.
  - Code: `internal/repowrite/pinned.go:14-47` — the pinned root is Go's `os.Root` (`os.OpenRoot`), which does its own beneath-the-root lookup. `pinned.go:239-250` replaces a file with `root.Rename(temporary, target)`, a plain rename; no exchange or no-replace flag, no after-change undo. No `openat2`, `renameat2` or `renameatx_np` anywhere in `internal/repowrite`.
  - What to change in the document: describe the built primitive (`os.Root` handles, pinned directories, temp-then-rename) and keep the syscall-level contract as a target.
- G4-27 [DIFFERS] Temporary and holding names.
  - Document: line 101 "`.yoyodyne-tmp-`"; line 154 "holding directory, `.yoyodyne-removing`".
  - Code: `internal/repowrite/repowrite.go:63` `temporaryPattern = ".yoyo-write-*.tmp"`. No `.yoyodyne-removing` in the package. (The inventory flagged both as not found in the code.)
  - What to change in the document: use the real name, or delete the removal-holding scheme (`RemoveFile` at `repowrite.go:492` is a plain removal).
- G4-28 [NOT BUILT, in the code] Link-count checks (a file with more than one link is refused) and "the held directory is proven still beneath the root on both sides of every change".
  - Document: lines 58, 127-130. Code: no `Nlink` or link-count check in `internal/repowrite`. `PinnedRoot.Unchanged()` (`pinned.go:57-68`) is called out in its own comment as "a refusal after a replacement, not the confinement mechanism"; it is not run before and after every change.
- G4-29 [NOT BUILT, in the code] Appends "in batches of at most 64 KiB with checks before and after", and the pipe-copying of a child's output.
  - Document: lines 125-148. Code: `repowrite.go:250` `OpenAppend` returns an `*os.File` for the caller to write; `append_unix.go:14` sets `O_NOFOLLOW`. No batching or pipe reader in the package.
- G4-30 [NOT BUILT, in the code] The conformance suite (a shared table of filesystem shapes run against each writer) and test hooks guarded by `testing.Testing()`.
  - Document: lines 195-232. Code: `internal/repowrite/pinned_test.go`, `repowrite_test.go`, and `readertest`/`writertest` directories exist, which look like a shared suite; I did not read them. The only `testing.Testing()` I found is in `internal/runstate/stateroot.go:409`, not in the primitive. Class UNCLEAR for the suite, NOT BUILT for the hook guard in `repowrite`.
- G4-31 [DIFFERS] "Every store in `internal/runstate`, the memory store, the conversation records and the lock files write through the primitive."
  - Document: line 165. Code: `internal/runstate/heavycheck.go:421` and `:442-475` use `repowrite.PinnedRoot` and `OpenLock`; 77 non-test files import `repowrite`. I did not search every writer. Class: MATCHES for runstate's heavy-check store; UNCLEAR for the others. Searched `os.WriteFile` and `os.Create` in `internal/runstate` and found none outside tests.
- MATCHES (absolute symlink target refused; relative one inside the root followed): `repowrite.go:182-250` (`resolve`, `contains`, `EscapeError`) — I read the header comment only (lines 1-45).
- MATCHES (no fallback to ordinary writes on an unsupported platform): `pinned.go:24-26` returns an error for `js` and `plan9`.
- UNCLEAR G4-32: the supported-platforms list (Linux 5.6+, macOS 11+, ext4/XFS/Btrfs/tmpfs/APFS, lines 130-150); `pinned.go:24` refuses only `js` and `plan9`, so the filesystem list is not enforced there.
- The "hold" on other v1 code (lines 247-255) is history; `yoyodyne-xdj` is open in `.beads/issues.jsonl` but I did not read its state.

### Missing from the document
- `Move` across two roots, `RenameDirectory`, `MakeDirectory`, `RemoveDirectory` (`repowrite.go:420-577`) — the built operation set differs from the doc's "creating, replacing, appending, removing".
- `Root` (path-based `Resolve`) alongside `PinnedRoot`: the package header (`repowrite.go:34-44`) says `Resolve` alone does not defend against a swap between resolution and write; writers that need that must use `PinnedRoot`. The doc has one primitive; the code has two with different strength.
- How the harness confines a developer run's own writes (the run sandbox): not here. A reader looking for "where a run may write" will not find it in this doc.

### Overlap with other documents
- With `pinned-outside-files.md` and `what-each-invocation-was-sent.md` (not in my group): both link here. Not compared.
- With `CLAUDE.md` ("the run may write only to its own worktree and to `.git`"): complementary, not stated in this doc.

### Could be cut or moved
- "When other v1 code may be built" (lines 247-255) and "Deferred" (257-277) are a status of work in October; check against `yoyodyne-xdj` and either remove or turn into a short open-items list.
- The first section's list of what the "August 21 security analysis" found is history; keep one sentence.
