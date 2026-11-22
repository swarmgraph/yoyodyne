# yoyodyne-ifd.437.84: design documents against the code, group 1: core architecture

One of nine reports comparing the design documents in `docs/designs/` with the code, made at the operator's request of 2026-11-21 for a review of every design against the current code. This one covers group 1, core architecture.

- **Read on:** 2026-11-21, between 15:40 and 15:48 PST. Main moved from `2447eeaab` to `fe2db3d37` at 15:41, a minute into the reading; that change was 22 files, mostly the team feedback repair code in `internal/runstate`.
- **Read by:** a read-only reader in the operator's assistant's session, on Sonnet 5.5. It read code and ran nothing, tests included.
- **Checked by hand:** the operator's assistant checked three findings against the code. G1-12, G1-26 and G1-32 held. Every other finding is unchecked.
- **How to use it:** this is evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Classes:** `MATCHES`, the code does what the design says; `DIFFERS`, the code does something else; `NOT BUILT`, the design calls it future or the code is absent; `UNCLEAR`, not established.

Code read at the working tree of 2026-11-21 (main at 2447eeaab). All paths below are under `the repository`.

## Summary
- Documents: 6. Claims checked: 82 (some MATCHES lines bundle two or three claims). MATCHES 44, DIFFERS 14, NOT BUILT 14, UNCLEAR 10.
- The five mismatches that matter most:
  - G1-12: `orchestrator-boundaries.md` plans five new packages (`internal/continuation`, `internal/integration`, `internal/mergequeue`, `internal/documentlanding`, `internal/rolepass`). None exists. They are planned, never created and never renamed; every file it names still sits in `internal/orchestrator`.
  - G1-26: `one-owner-for-each-guarantee.md` point 4 of the race ruling says the harness runs the race detector on main over macOS-only code. `main_checks` is still `make race` (whole), and the Makefile has no such race target.
  - G1-25: its race point 3 requires the merge queue to run the full race check. `.yoyodyne/config.yaml:468-477` runs a narrowed race check on queue candidates and the whole one on main.
  - G1-32: `artifact-contract.md` says there are eight kinds including `guidance`. The code has no `guidance` kind; its eighth kind is `invariant`.
  - G1-36: `fewer-lifecycle-states.md` folds the brake, the endpoint pause, the dependency wait and the wait to land into follow-ups. Only the environment checks and the lapsed login are built; the check runner has no evaluator for the other named checks.
- Consolidation proposals:
  - Split the race-check ruling (one-owner lines 254-320) out of `one-owner-for-each-guarantee.md` into its own design beside `choosing-checks-by-change.md`. Both state "the same four cases".
  - Mark `orchestrator-boundaries.md` as a plan, or reduce it to one page: the target package map plus the continuation operations. Put the beads rule and the constructor table where the code is (`internal/orchestrator/README.md` or the one-owner guarantee 7).
  - `one-owner-for-each-guarantee.md` guarantee 6 and the whole of `fewer-lifecycle-states.md` overlap. Keep `fewer-lifecycle-states.md` as the one home, and have guarantee 6 be one sentence pointing to it.
  - Cut `v1-harness-design.md` down: remove the Self-hosting Sequence (lines 724-771) and the Implementation Choices (796-807), which are history. Move the Git Model (574-677) and Recovery (679-701), which restate `integration-through-a-merge-queue.md`, into that design.
  - Move incident-style dates, measurements and "this differs from the answer I gave" prose out of one-owner (lines 284, 300-308) into the work items.

---

## v1-harness-design.md — Yoyodyne V1 Harness Design
Verdict: partly stale
Size: 92 KB (833 lines, of which 234 are the revision log). Last changed: 2026-11-21.

### Findings
- G1-1 [DIFFERS] The Claude Code reviewer runs with no tools.
  - Document: line 584: "Claude Code refuses every tool; Codex permits repository inspection".
  - Code: `internal/backend/claudecode/backend.go:232` — `reviewerTools = []string{"Read", "Glob", "Grep", "Bash"}`; `:797` passes them with `--tools`. The management roles get none (`:213` `readOnlyTools` empty). `provider-adapters-and-endpoints.md` line 51 already says "the reviewer is given read-only tools over the run's worktree".
  - What to change in the document: say the reviewer holds four read tools in the worktree sandbox and link the adapter design instead of restating it. The 2026-10-02 revision note says the sentence was replaced, but the body still carries the old claim.
- G1-2 [DIFFERS] The backend boundary is `CheckAvailability(ctx, cfg)`, `Capabilities(ctx, cfg)`, `Start`, `Resume`.
  - Document: lines 487-494 (the Go block).
  - Code: `internal/backend/backend.go:549-559` — `CheckAvailability(ctx)`, `Capabilities()` with no arguments, and one `Run(ctx, RunRequest)`. There is no `Start` or `Resume`; resume is a field of the request (`SessionID`, `backend.go:85`).
  - What to change in the document: replace the block with the real three methods, or delete the block and point to `provider-adapters-and-endpoints.md`, which owns the adapter contract.
- G1-3 [DIFFERS] The CLI surface is eleven verbs, with `yoyo init` validating the repository and `yoyo work /stop` stopping a run.
  - Document: lines 707-718.
  - Code: `internal/cli/cli.go:917-972` lists about 45 commands. `init` writes configuration, personas and checks (`:922`); `setup` is the walk-through (`:921`). `yoyo stop` and `yoyo start` run the product's supervisor (`cli.go:89-94`, `product.go:146`). `/stop` is a chat command (`chat.go:1601`), not an argument of `work`. `yoyo pause` pauses spending on a provider (`pause.go:54`); lifting an intake hold is `yoyo release` (`cli.go:950`).
  - What to change in the document: replace the block with a sentence on what each surface is for and link `docs/configuration.md`; the doc itself says "exact command names may change".
- G1-4 [DIFFERS] "Beads issue `yoyodyne-fmk` tracks completion of this design."
  - Document: line 239.
  - Code: `.beads/issues.jsonl` — `yoyodyne-fmk` is closed ("Design Yoyodyne v1 self-hosting agent harness").
  - What to change in the document: rewrite the Status section as present-tense (the design is the standing account) and drop the item id.
- G1-5 [UNCLEAR] "With the switch on, the harness opens no pull request for a run branch ... needs one build piece."
  - Document: line 615.
  - Looked at: `internal/orchestrator/README.md:481` (`admitToMergeQueue` replaces `integrate` for every run) and `publish/github.go:395` (`--draft` for the candidate). I did not find a test that refuses a run-branch pull request with the queue on.
  - What to change in the document: if built, remove the "needs one build piece" paragraph; if not, name the item.
- G1-6 [UNCLEAR] The merge method is a merge commit "deliberately", recorded on the run.
  - Document: line 624.
  - Looked at: `internal/publish/github.go:1461-1472` supports rebase, merge and squash; I did not establish which one a run uses by default.
- Matches, one line each:
  - G1-7 [MATCHES] State root resolution order and the `.git/yoyodyne/state-root` marker (line 469): `internal/home/home.go` `Resolve` (variable, machine key, `XDG_STATE_HOME`, default) and `runstate/stateroot.go:66`.
  - G1-8 [MATCHES] A `state_root` in the project file is refused by name (line 469): `internal/config/document.go:525-528`.
  - G1-9 [MATCHES] Legacy `.yoyodyne.yaml` still accepted (line 453): `internal/config/discover.go:25`.
  - G1-10 [MATCHES] Five default agents come from `builtin:v1`; the program manager is a sixth role (lines 391, 545): `config/builtin/v1/bundle.yaml:217-260`, `domain/types.go:32,40`.
  - G1-11 [MATCHES] `approvals.integration`, `publishing` and `releases` default to `human` (lines 423-425, 632): `bundle.yaml:193-194`, `config/resolve.go:423`. Also two repair attempts (`resolve.go:346`), concurrency one (`:344`), and `execution.remote` `origin` (`bundle.yaml:44`).
  - Also MATCHES: developer diff refused on artifact homes with a grant read from title, description, design guidance and acceptance criteria, never notes (line 588), `internal/protectedpath/protectedpath.go:48`; the release cut tags the commit the notes name (lines 630-644), `internal/releasecut/releasecut.go:312-423`; merge-queue candidate opens as a draft (line 610), `publish/github.go:395`; the three artifact homes and the separate invariants directory (line 451), `config/config.go:48-60`; the declarative graph observes and performs nothing (line 391 link), `orchestrator/declarative.go:3-25`.

### Missing from the document
- Checks on CI: `execution.ci_checks` (`.yoyodyne/config.yaml:193-199`) changes which checks run locally and what the forge runs. Line 564 says only "runs configured deterministic checks". The design is `checks-on-ci.md`, which this document does not link.
- Capacity: `max_concurrent_developers`, one count of heavy-check permits per machine, and the usage-limit and provider-overload pauses (`config.yaml:200-232`). Line 765 says "concurrency defaults to one" and nothing about waits.
- Team mode: a team pause (`yoyo pause --team`, `cli/pause.go:47`) and the `internal/teamclaim`, `teamrecords`, `teamspends` packages. No design in this group names them.
- MCP tools reached through the harness (`internal/mcpcall`, `yoyo mcp`): not in the Agent Model section.
- Follow-ups, the brake and the intake hold: line 419 describes the operator's three stop actions but not the harness's own holds.

### Overlap with other documents
- Line 584 vs `provider-adapters-and-endpoints.md` line 51 on the reviewer's tools: they disagree (G1-1). The adapter design is right.
- Lines 600-626 and 693-701 vs `integration-through-a-merge-queue.md`: this document says it "summarises and does not restate", but it restates the draft pull request, the direct merge and the rebuild rule at length. Not checked for disagreement.
- Lines 413 and 125-144 of `artifact-contract.md`: agree (confirmed by a reviewed run of its own that changes only that file).
- Lines 467-477 vs `machine-home.md`: agree on the state root; the machine-home design owns the layout.
- Lines 628-644 vs `alpha-releases.md`: this document defers the alpha case to it.
- Lines 519-551 vs `portable-agent-configuration.md`: the configuration example and the merge rules are in both. Not compared line by line.

### Could be cut or moved
- Self-hosting Sequence, lines 724-771: all six milestones are done; it is history.
- Implementation Choices to Settle, lines 796-807: the schema, the state format and the merge method are settled elsewhere.
- Testing Strategy (773-781) and Security and Safety (783-794): short and probably true; keep, but they repeat the Git Model's "never route a push through an agent" (line 592 and line 792).
- The Git Model "remotes and pull requests" bullets (606-626) and the reconciliation paragraphs (693-701): move to `integration-through-a-merge-queue.md`.
- The revision log carries 234 lines of reasons, many of them narrating one change (for example the lines 132-156 reasons). These live in git history.

---

## orchestrator-boundaries.md — How internal/orchestrator is split, one admitted change at a time
Verdict: mostly a plan not built
Size: 25 KB. Last changed: 2026-10-30.

The five missing packages are planned and have not been created or renamed. Evidence: `ls internal` has none of `continuation`, `integration`, `mergequeue`, `documentlanding`, `rolepass`; every file the document assigns to them still exists in `internal/orchestrator` (checked all of `stallcontinue.go`, `checkstagecontinue.go`, `integrationresume.go`, `repaircontinue.go`, `checkoutrecovery.go`, `rerun.go`, `carryout*.go`, `publish.go`, `publication.go`, `reconcile*.go`, `stuckmerge.go`, `queuedchecks.go`, `divergedtarget.go`, `redtarget.go`, `landingstop.go`, `mergequeue*.go`, `documentpublication.go`, `documentdocket.go`, `documentdecision.go`, `conversationlanding.go`, `recurring*.go`, `passedover.go`, `repeatleave.go`, `programmanagerpass.go`, `supervision.go`). The code map `internal/orchestrator/README.md` describes them in place. The document says each package "is created by the first admitted change that moves a file into it" (line 68); none has been admitted.

### Findings
- G1-12 [NOT BUILT] Five new packages hold the split boundaries.
  - Document: line 63 "move to a new package, `internal/continuation`"; line 64 `internal/integration`, `internal/mergequeue`; line 65 `internal/documentlanding`; line 66 `internal/rolepass`.
  - Code: `internal/` lists no such directory; `internal/orchestrator/mergequeueworker.go`, `stallcontinue.go`, `documentpublication.go`, `recurring.go` are all present.
  - What to change in the document: say plainly, in the first section, that no boundary has moved and which item (if any) would move the first; or mark the document as a plan.
- G1-13 [NOT BUILT] Counting what is free moves into `internal/runstate`.
  - Document: line 62 "`slotIsFree` ... moves into `internal/runstate`".
  - Code: `internal/orchestrator/rerun.go:1089` defines `slotIsFree`; it is called from `stallcontinue.go:199`, `checkstagecontinue.go:216`, `integrationresume.go:312`, `teamfeedbackrepair.go:293`. The heavy-check places are in `orchestrator/heavychecks.go:33-52`.
  - What to change in the document: say the count is still in orchestrator, though the reservation itself (`Store.ReserveLive`, `runstate/store.go:586`) is in runstate.
- G1-14 [NOT BUILT] The eleven shared continuation operations exist once in `internal/continuation`.
  - Document: lines 102-114.
  - Code: the four paths still have their own copies (`stallcontinue.go`, `checkstagecontinue.go`, `integrationresume.go`, `repaircontinue.go`). Operation 4's reservation is built: all of them end with `ReserveLive` (`stallcontinue.go:245`, `checkstagecontinue.go:282`).
  - What to change in the document: split the section into "built" (the reservation) and "planned" (the rest).
- G1-15 [NOT BUILT] One function of the run record alone chooses the step a stopped run resumes at.
  - Document: line 72.
  - Code: `.beads/issues.jsonl` — `yoyodyne-ifd.428.99.6` is open ("Stopped-run recovery slice 5: one function decides whether and from which step ...").
  - What to change in the document: keep, but state it waits on that item.
- G1-16 [DIFFERS] Run storage takes only types and constants from `internal/beads`; six named files; a check in `make test` holds it.
  - Document: lines 80-88.
  - Code: `internal/runstate/trackerstoresquash.go` does not exist (the nearby files are `trackerstorerebuild.go`, `trackercalllog.go`, `trackerlisting.go`, `trackermerge.go`). Ten runstate files import `internal/beads` (the six plus `document.go`, `emptyslots.go`, `state.go`, `trackercalllog.go`, `trackerstorerebuild.go`). Several call functions, which the rule forbids: `beads.ValidIssueID` at `runstate/emptyslots.go:212,239`, `document.go:270`, `state.go:4781`; `beads.SayBytes` at `trackerstorecleanup.go:173`. I found no such check in `internal/composition` or the runstate audit tests. `internal/beads` does not import runstate (the other half of the rule holds).
  - What to change in the document: correct the file list, and either record the calls above as the list of exceptions the check will start with, or drop the claim that a check exists.
- G1-17 [DIFFERS] `buildComponents` "opens twenty-one stores" and the forge client "is built twice".
  - Document: line 152.
  - Code: `internal/cli/run.go:385` `buildComponents` now makes 24 store constructors (`runstate.New...Store`); `publish.GitHub{` is built three times in the same file (`:725`, `:1003`, `:1639`).
  - What to change in the document: update the numbers, or remove them and name the rule only.
- G1-18 [DIFFERS] The tracker client "that records how its listings end" is built in `components.tracker` and `withListingRecord`.
  - Document: line 168.
  - Code: `withListingRecord` does not exist. The client takes a `Listings ListingRecorder` field (`internal/beads/client.go:528,624-626`). Listing sharing is in `beads/listingshare.go`.
  - What to change in the document: name the real field and constructor.
- G1-19 [NOT BUILT] The roots, the worktree managers and the forge client get constructors in `internal/home`, `internal/gitworktree`, `internal/publish`.
  - Document: lines 166-175.
  - Code: `resolveRoots` (`internal/cli/run.go:239`), `primaryCheckout` (`:608`) and `standingRemains` (`:369`) are still in the command line package. `internal/home` has the state root resolver and binding but not the three roots.
  - What to change in the document: mark each row built or not.
- G1-20 [DIFFERS] `pipeline.go` is "about 9,700 lines" and `schedule.go` "about 6,300".
  - Document: line 51.
  - Code: `wc -l` gives 12,489 and 9,592.
  - What to change in the document: drop the line counts (they date the document in two weeks); the point is the package's own code map.
- Matches, one line each: G1-21 [MATCHES] the declarative graph steps beside a run and acts on nothing (`orchestrator/declarative.go:3-25`, line 138); G1-22 [MATCHES] making a run live takes the slot in the same act under the reservation lock (`runstate/store.go:573-612`, line 107); G1-23 [MATCHES] `internal/maintain` exists and holds the store measure, cleanup and rebuild (lines 85, 88); G1-24 [MATCHES] every file the document names for a boundary exists today in `internal/orchestrator` (lines 61-66).
- Not checked: that `internal/continuation` would import nothing from orchestrator (line 190; no such package); the machine-home build's use of the constructors (line 182; UNCLEAR, not looked at).

### Missing from the document
- The code map `internal/orchestrator/README.md` now carries the mergequeue, forgererun, lostracerepair, forgewait, teambranchupdate files in detail. This document's file lists for "integration" omit `forgewait.go`, `forgererun.go`, `lostracerepair.go`, `teambranchupdate.go`, `mergequeuecheckrerun.go` and `mergequeueforgewait.go` — the code that is the most active part of the package.
- Nothing says who decides to start the first move. No item in `.beads/issues.jsonl` was found to be admitted for it (not searched exhaustively).

### Overlap with other documents
- Lines 107 (operation 4) and `one-owner-for-each-guarantee.md` lines 177 and 422 say the same thing about reservation; they agree.
- Lines 85 and 88 and `tracker-store-size.md` and one-owner guarantee 7 (line 225): all three say that calling the tracker is the client's and deciding about its store is maintenance's. They agree.
- Lines 102-114 and `stopped-run-recovery.md`: the ordering rules (ask everything that can refuse first, close the entry last) are in both. Not compared.
- Line 140 and `configurable-workflows.md` line 165 (item 428.123): agree that the graph binds to the run record.

### Could be cut or moved
- The five "Rejected" bullets (lines 92-96) and the "Alternatives rejected" list (199-206) repeat lines 51-55.
- The shared constructors section (150-182) is a different subject (the command line package) from the orchestrator split; it could be its own short design or live in the cli package code map.
- "What a reviewer can check" (188-197): half of it is untestable until the packages exist.

---

## one-owner-for-each-guarantee.md — Guarantees the whole system relies on: one owning component and one way in for each
Verdict: partly stale
Size: 51 KB. Last changed: 2026-11-20.

### Findings
- G1-26 [NOT BUILT] The race run on main is "narrowed to the code built only on macOS".
  - Document: lines 301, 308 "since November 20, 2026 narrowed"; lines 312-316 list it as a build piece.
  - Code: `.yoyodyne/config.yaml:465-466` — `main_checks: - make race`; the comment above it says "The whole race check runs on main". `Makefile:282` has `test-macos` but no race target over `MACOS_TEST_PACKAGES` (`Makefile:280`). `failing_tests` (`config.yaml:~455`) lists `make test, make race, make test-macos`, not a new target.
  - What to change in the document: write point 4 so a reader can tell the ruling (decided) from the behaviour (still the whole `make race`); move the tense of line 301 to "will run".
- G1-25 [DIFFERS] The harness's own merge queue runs the full race check on the candidate.
  - Document: lines 296, 299 "the queue ran the project's configured checks whole over the candidate, the full race check among them".
  - Code: `.yoyodyne/config.yaml:468-477` — `merge_queue_checks` omits the race check; `merge_queue_race_check: make race CHANGED_GO_PACKAGES=...` runs it only on the packages the change touches; "The whole race check runs on main instead". `config/config.go:136-142` (`MergeQueueRaceCheck`).
  - What to change in the document: point 3 case 2 is not what this repository does; say which design (yoyodyne-ifd.429.132, `merge-queue-pace.md`) now rules it, and reconcile.
- G1-27 [DIFFERS] The migration order and "items waiting on design" are still to do.
  - Document: lines 356-376 (migration items 1-5, 7, 9, 11-13) and 399-410.
  - Code: `.beads/issues.jsonl` — `yoyodyne-ifd.428.116`, `.117`, `.118`, `.119`, `.121`, `.86`, `.123`, `yoyodyne-ifd.433.45`, `.432.46` are closed. In code: the stale-save refusal (`runstate/store.go:882` `checkRevision`), `ReserveLive`, `budget.Permission` (`chat/compact.go:92`), the run index (`runstate/runindex.go`).
  - What to change in the document: mark each migration item built or open; remove the "items waiting on design" table or say they closed.
- G1-28 [NOT BUILT] One function moves a run between states; one plan decides which checks run; permits share the developer slot reservation.
  - Document: lines 161 (guarantee 2), 260 (guarantee 9 plan), 179 (permits).
  - Code: `yoyodyne-ifd.428.99.6` open; `internal/checks/` has `narrow.go`, `runner.go`, `permits.go`, `timelimit.go` and no plan; `runstate/heavycheck.go:76` keeps its own `permits.lock`.
  - What to change in the document: same as G1-27; these are the open pieces.
- G1-29 [DIFFERS] "Making a run live takes the reservation as an argument, so a start without one does not compile"; likewise a paid turn.
  - Document: lines 171, 193, 342.
  - Code: the run store refuses at run time (`runstate/store.go:857-858` `ErrMadeLiveWithoutASlot`; save is refused unless the caller is `ReserveLive`). The paid turn is by type (`budget.Permission`, `internal/budget/budget.go:84-89`).
  - What to change in the document: say the slot is a run-time refusal plus a package audit, not a type.
- G1-30 [NOT BUILT] The recovery budget's decisions and in-place retries move onto `internal/budget`.
  - Document: lines 194-198.
  - Code: `internal/budget` exists (142 lines, `budget.go`), decides message turns only; it is imported only by `internal/chat` and `internal/config/timelimits.go`.
  - What to change in the document: line 183 "created by the first item that uses it" is done; say the second and third kinds are not.
- G1-31 [DIFFERS] "The tracker is reached through one client ... the only code that runs the tracker's command"; the listing sharing "moves into the client".
  - Document: lines 227-235, 346.
  - Code: sharing is in the client (`internal/beads/listingshare.go:44-110`), but `internal/orchestrator/cyclelistings.go` also remains. `internal/doctor/doctor.go:528` and `internal/cli/setup.go:408,441` run `bd stats` and `bd init` directly. I found no `make test` check for it.
  - What to change in the document: list those exceptions, or move them into the client.
- Matches, one line each: [MATCHES] a whole save carries a revision and is refused when stale, and writers are audited (line 151), `runstate/runrecordwriters_audit_test.go:3-20`, `store.go:882`; [MATCHES] durable run index with unreadable records skipped (line 244), `runstate/runindex.go`, `runindex_unreadable_test.go`; [MATCHES] the stage limit is 90m and returns to 60m only after scoped race checks land (line 261), `config.yaml:232`, `yoyodyne-g9g` open; [MATCHES] the whole-by record `checks_passed.whole_by` (line 296), `runstate/state.go:1198`; [MATCHES] the main check runs every four hours when main moved (line 301), `config/config.go:771`; [MATCHES] a failure on main reruns only the failing tests once, warns if they then pass, files a critical report otherwise (lines 302-306), `internal/maincheck/maincheck.go:20-21,432-503`; [MATCHES] the untouched-test rerun exists (line 272), `internal/checks/untouchedrerun.go:16-33` (item `yoyodyne-ifd.429.87` is still open, so only part is built).
- G1-33 [UNCLEAR] Only `internal/cost` decodes a cost event; each adapter returns a priced ending with malformed output (lines 210-211). Looked at: `backend/backend.go:552-558` says the adapter result is "the best the adapter knows beside any error"; I did not check cost decoding outside `internal/cost`.
- G1-34 [UNCLEAR] The `make test` checks in the table (lines 338-348). I found the guarantee 1 audit (`runrecordwriters_audit_test.go`) and a guarantee 8 audit (`runstate/boundedreads_audit_test.go`); for guarantees 2, 4, 7 and 9 I found none in `internal/composition` or `internal/runstate`.
- G1-35 [UNCLEAR] The seven-day rule (a test that fails then passes twice within seven days files a critical report, line 305). `maincheck.go` has the single rerun and the warning; I did not find the seven-day count.
- Not done: guarantee 5's evidence record and guarantee 6's pieces, which are `fewer-lifecycle-states.md`'s.

### Missing from the document
- Where the code for guarantee 3's reservation lives (`runstate/store.go` `ReserveLive`, `ReclaimSlot`, `lockReservations`) and `routingstore.go` `ClaimSlot`, which also takes a slot (`runstate/routingstore.go:83`): the document speaks of "the one reservation" but the code has at least three entry points.
- `internal/maincheck` (the main-branch checks) is the owner of the point 4 behaviour and is not named.
- The invariant (lines 412-418) is still "to be recorded once yoyodyne-0bj is built". `yoyodyne-0bj` is closed and `docs/decisions/invariants/` holds no such invariant (12 files; none says one owning component).

### Overlap with other documents
- Lines 294-299 and `choosing-checks-by-change.md`: both state "the same four cases" for who verifies the full race check at the merge candidate. They are meant to agree; the config in G1-25 does not follow either.
- Lines 213-223 (guarantee 6) and `fewer-lifecycle-states.md` lines 131-218: the document says "Nothing here changes those designs". They agree, and guarantee 6 adds nothing.
- Lines 177 and 422 and `orchestrator-boundaries.md` line 107: agree.
- Line 428 and `heavy-check-capacity.md`: says permits move to the shared reservation; that design is "unchanged" apart from that.
- Lines 272-273 and `repeated-checks.md`/`recoverable-and-terminal-failures.md`: reruns appear in all three. Not compared.

### Could be cut or moved
- The "What it rests on, and what was not read" section (103-129) is a log of one reading at one commit.
- Race check ruling (lines 280-320, about 40 lines of the document's 455): it is a project-specific ruling with measurements (72%, 80 of 249, 4.2 runs, 19 to 32 minutes, 16.6 load) and a first-person line ("This differs from the answer I gave in exchange 9bb52ecf", line 300). Move to its own design or to the work items.
- "Alternatives rejected" lines 445-446 are about the race ruling, not the guarantees.

---

## artifact-contract.md — The artifact contract: specification shape and identity
Verdict: partly stale
Size: 20 KB. Last changed: 2026-10-21.

### Findings
- G1-32 [DIFFERS] There are eight kinds, including `guidance`, owned by the Lead Product Manager.
  - Document: line 106 "There are eight"; lines 114 and 123 the `guidance` row and its paragraph.
  - Code: `internal/artifact/artifact.go:62-76` — the kinds are `brief`, `goals`, `non-goals`, `rules`, `design`, `specification`, `decision`, plus `invariant`. There is no `KindGuidance` anywhere (`grep -i guidance internal/artifact` finds nothing). `docs/` has no `kind: guidance` file; `docs/product/goals/writing-goals.md:3` is still `kind: non-goals`, the exact case the document says needed the new kind.
  - What to change in the document: replace `guidance` with `invariant` (written through the typed action, but with its own directory and no frontmatter kind, `artifact.go:68-75`), or say `guidance` is a plan; and fix "The goals directory's Writing a goal is guidance".
- G1-37 [UNCLEAR] A project with no reviewer or an intent repository the harness does not integrate into: the write stops at the working tree, and a marked-consistent document is still put to the operator (line 144). Looked at `internal/artifact/publication.go:173-311`; did not trace this branch.
- Matches, one line each: [MATCHES] kind owners: Lead Product Manager owns brief, goals, non-goals, rules; architect owns design, specification, decision (`artifact/ownership.go:74-76`); [MATCHES] the three cases for a Lead Product Manager document: marked consistent (confirmed without the operator), marked fundamental, and unmarked wait for the operator (`artifact/approval.go:57-62,384`, `artifact/publication.go:45,173,311`, `artifact.go:311` `IntentConsistent`/`IntentFundamental`); [MATCHES] `rules` is governed by `approvals.goals` (`approval.go:413`); [MATCHES] a document is returned to its writer at most three times, then publication stops (line 138), `runstate/document.go:79` `MaxDocumentReturns = 3`, `chat/documentpublication.go:329`; [MATCHES] a run stopped from outside the work is tried at most three times in all (line 138), `runstate/document.go:86` `MaxDocumentAttempts = 3`; [MATCHES] the bound on documents waiting to land (line 140), `runstate/conversation.go:685` `MaxPendingWrites = artifact.MaxWaitingDocuments`; [MATCHES] goal identifiers in square brackets, assigned once (line 98), `internal/goal/goal.go:18,123`; [MATCHES] statuses `draft`, `superseded` and the rest (line 102), `artifact.go:103-110`; [MATCHES] a `README.md` in any artifact home is ungoverned (line 102), `artifacthome/artifacthome.go:42`; [MATCHES] the decision it links, `docs/decisions/who-approves-a-change-to-intent.md`, exists.

### Missing from the document
- The invariant kind (G1-32): the code accepts `invariant` as a kind in the typed write path and nothing in the contract says so.
- The setting each kind is governed by (`approvals.brief`, `approvals.goals` for goals, non-goals and rules, `approvals.designs` for design, specification, decision and invariant, `artifact/approval.go:372-417`) is not named; the contract says "the kind's approval policy" without saying which one.
- A document filed in the specifications home is governed by `approvals.goals` whatever its kind (`approval.go:363-371` `SettingFor`). The contract does not mention this.

### Overlap with other documents
- Lines 125-144 and `document-landing-route.md` and `v1-harness-design.md` line 413: the route is in all three. The contract is the normative one; the others summarise. They agree on the points I read.
- Line 98 and `docs/product/goals/writing-goals.md`: the identifier rule appears in both. Not compared.

### Could be cut or moved
- The 15 lines of "Revisions" reasons narrating each amendment (lines 8-52) are history.
- Lines 136 and 144: the "earlier design let an unmarked ... document follow its kind's approval setting; that was dropped" and "That is a known gap against point 3, not the design" sentences are history and an issue note.

---

## fewer-lifecycle-states.md — Fewer lifecycle states: one record for every wait and hold, one meaning for each recovery decision, one writer for each store
Verdict: mostly a plan not built
Size: 22 KB. Last changed: 2026-10-24.

### Findings
- G1-36 [NOT BUILT] Every wait and hold is one follow-up with a named check; the brake, the endpoint pause, the dependency wait and the wait to land write theirs.
  - Document: lines 133-148 and 213-218; build pieces 1, 3, 6.
  - Code: `internal/followup/check.go:30-76` names the checks (`item-landed`, `dependencies-closed`, `stop-left-list`, `provider-usable`, `item-unparked`), but `internal/followup/checkrun/runner.go:206-218` evaluates only `environment` and `command`, and `internal/cli/schedule.go:599-611` builds the runner with no `Checks` map. For any other check the runner answers "this build of the harness has no way to run the %q check yet". The brake is still `readmodel/heldline.go:14-95` (`HeldLineOf`, `ProbeOverdue`); I found no code that writes a follow-up for it or for the dependency wait or the wait to land.
  - What to change in the document: split the folding order into "built" (environment checks, the lapsed login) and "planned".
- G1-38 [DIFFERS] The unresolved hold is gone from the decision list.
  - Document: lines 164-181 (and "Designs this changes", line 262).
  - Code: `internal/runstate/recovery.go:43` still has `RecoverHold RecoveryAction = "record an unresolved hold"`; `internal/runinspect/runinspect.go:71,77` still has `ExpectUnresolvedHoldRecorded`; `internal/health/health.go:219` still classes "unresolved recovery hold". The field the folding needs is built: `internal/followup/followup.go:48-50` `KeepsStopListed`.
  - What to change in the document: mark step 4 as partly built (the field) and partly not (the decision list).
- G1-39 [NOT BUILT] Tracker progress fields are display only; each reader moved off them; the small run-state records folded.
  - Document: lines 183-207; pieces 7 and 8.
  - Code: `yoyodyne-819` is closed (the clear on give-back); `yoyodyne-ifd.428.106` (lock waits in `internal/runstate`) is open. I did not read the readers named in point 3, so the reader claim is UNCLEAR.
- G1-40 [NOT BUILT] The invariant "Nothing waits outside the follow-up store" is to be recorded once `yoyodyne-0bj` is built.
  - Document: lines 254-258.
  - Code: `yoyodyne-0bj` is closed; `docs/decisions/invariants/` has no such invariant.
  - What to change in the document: the invariant is either recorded (then say so) or this is still a proposal waiting on the architect.
- G1-41 [NOT BUILT] Four tests named in "What is enforced in code" (lines 280-283).
  - Document: lines 280-283.
  - Code: not found under `internal/followup`, `internal/runstate`, `internal/composition`.
- Matches, one line each: [MATCHES] the environment hold's re-check is the check runner's (lines 145, 225), `internal/orchestrator/followups.go:61-95`, `followup/checkrun/environment.go`, `runner.go:194-211`; [MATCHES] a lapsed login writes its follow-up and the session does not probe itself (line 146), `orchestrator/followups.go:75-100`; [MATCHES] `execution.brake_probe_overdue_after` exists, default 45 minutes (line 144), `config/config.go:612,831`; [MATCHES] the recovery decisions continue, repair, rerun, finish the merge, wait, give the item back, hold, ask the Lead Product Manager, plus the release act (lines 169-181), `runstate/recovery.go:37-45`; [MATCHES] a follow-up carries whether it keeps a stop listed (line 271), `followup/followup.go:50,334`.
- G1-42 [UNCLEAR] The inventory of holds and stores (lines 94-129). I did not read the scheduler's brake, the pause for each endpoint or the run state package's small records, which the document itself says it did not read (line 52).

### Missing from the document
- The document says it did not read the scheduler, the run state package, the readiness check or the read model (line 50-55). It was never revised after those were read; one revision, one day.
- The 2026-10-24 "this week" fixes are now in the code (items `yoyodyne-ifd.432.25.19`, `435.10`, `435.21` are closed) and the folding order does not say which landed folded and which landed on their own.
- Heavy-check permits (`runstate/heavycheck.go`), a wait with its own lock and recovery, are not in the inventory of waits (line 83 mentions "a permit for a heavy check" only as a wait inside a run).

### Overlap with other documents
- Lines 131-218 and `follow-ups.md` (the follow-up design) and `forward-progress.md`: this document names itself a revision to both ("Designs this changes", lines 260-276). I did not read the others; the document says their revisions "follow this one".
- Lines 133-148 and `one-owner-for-each-guarantee.md` guarantee 6 (lines 213-223): agree; the second repeats the first.
- Line 164 table and `stopped-run-recovery.md` step 3: the decision list is in both; this document says the other must lose the unresolved hold.

### Could be cut or moved
- "What it rests on, and what was not read" (lines 40-59) and the "this week" framing (lines 24-30) are history.
- "Alternatives rejected" (285-292) repeats reasons already in lines 131-218.

---

## README.md — docs/designs
Verdict: current
Size: 1 KB. Last changed: 2026-08-23.

### Findings
- G1-43 [MATCHES] The index states purpose, owner and hand-edit rules, and says `yoyo amendment list` shows what is waiting: the text is generated by `internal/artifacthome/artifacthome.go:42-53,216-229` (markers `**Purpose.**`, `**Owner.**`, `**Editing by hand.**`); `yoyo amendment list` exists (`internal/cli/amendment.go:53`); `yoyo agent chat` exists (`internal/cli/agent.go:163`); `yoyo stale` exists (`cli.go:71`).
- G1-44 [MATCHES] "`yoyo init` writes it and `yoyo doctor` reports it missing": `internal/cli/init.go:665-669` writes missing homes' indexes, `cli/setup.go:633-657` offers the same, `internal/doctor/doctor.go` reports it.
- G1-45 [MATCHES] It carries no identity frontmatter and artifact governance skips it: `artifacthome/artifacthome.go:42`, and the contract line 102.
- It does not list the 51 designs or say which are plans and which are built. It is a generated template and checked by marker only (`artifacthome.go:45-53`), so adding a table is allowed (an operator-added paragraph does not break it), but the table would need a different owner than the template.

### Missing from the document
- A status per design (built, partly built, plan). Every design says `status: active`, so a reader cannot tell. The audit's result for this group: `orchestrator-boundaries.md` and `fewer-lifecycle-states.md` are mostly plans; `one-owner-for-each-guarantee.md` and `v1-harness-design.md` are partly stale; `artifact-contract.md` is partly stale; this README is current.

### Overlap with other documents
- None; the index's hand-edit paragraph repeats the same paragraph in the other artifact homes' indexes (`docs/product/README.md`, `docs/decisions/README.md`), by design.

### Could be cut or moved
- Nothing. Keep as is.
