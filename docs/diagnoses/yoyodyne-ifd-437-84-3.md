# yoyodyne-ifd.437.84: design documents against the code, group 3: integration and merging

One of nine reports comparing the design documents in `docs/designs/` with the code, made at the operator's request of 2026-11-21 for a review of every design against the current code. This one covers group 3, integration and merging.

- **Read on:** 2026-11-21, between 15:40 and 15:48 PST. Main moved from `2447eeaab` to `fe2db3d37` at 15:41, a minute into the reading; that change was 22 files, mostly the team feedback repair code in `internal/runstate`.
- **Read by:** a read-only reader in the operator's assistant's session, on Sonnet 5.5. It read code and ran nothing, tests included.
- **Checked by hand:** the operator's assistant checked three findings against the code. G3-1, G3-4 with G3-5, and G3-7 held. Every other finding is unchecked.
- **How to use it:** this is evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Classes:** `MATCHES`, the code does what the design says; `DIFFERS`, the code does something else; `NOT BUILT`, the design calls it future or the code is absent; `UNCLEAR`, not established.

## Summary
- Documents: 6. Claims checked: 52. MATCHES 24, DIFFERS 6, NOT BUILT 14, UNCLEAR 8.
- The five mismatches that matter most:
  - G3-1 and G3-2: the queue's order is the older five-rule order (eight-hour age rule, documents ahead of all non-priority-0 code, the architect's oldest document beside priority 0). The document's four-hour rule, document batching and turn-taking are not built (items yoyodyne-ifd.429.266 and .267 are open).
  - G3-4 and G3-5: the draft-pull-request, mark-ready and direct-merge rules are not built. The code still runs `gh pr merge --auto` (items .233 and .243 open).
  - G3-6: `yoyo doctor` has no check for missing required checks or a missing up-to-date requirement on the target branch.
  - G3-7: the queue picks the document check set by the entry's recorded kind, not by what the candidate changes. The document says the reverse and lists the recorded-kind way as rejected.
  - G3-29 and G3-32: the batch pull request route (section 5 of integrating-to-a-local-branch.md, items 7 to 14) is a plan; `execution.batch_pull_request` is not in the code. Tracker sharing through `refs/yoyodyne/tracker` is not built either.
- Consolidation proposals:
  - Split integration-through-a-merge-queue.md (59 KB, 463 lines) into: the queue's guarantees and mechanics (built), the order, GitHub's required checks and the trial, and a short "not yet built" list. The built part and the plan are now mixed in every section.
  - Fold merge-queue-pace.md into the queue design. It states decisions the queue design already restates (side by side, check ahead, declared list). Keep only its race-check-on-main ruling, and point that to one-owner-for-each-guarantee.md.
  - Cut the "Code this makes wrong" and "Not read" sections from document-landing-route.md and merge-queue-pace.md. They describe a repository state that no longer exists.
  - Move working-alongside-other-contributors.md section 7 (tracker sharing) to its own design. It is the largest unbuilt part, and project-groups.md also depends on it.
  - Move integrating-to-a-local-branch.md section 5 (batch route, about 150 lines, all unbuilt) to a design of its own, so sections 1 to 4 (built) read as current.
  - Mark unpublished-designs-and-admitted-work.md as a plan not built, or merge it into the document-landing and publishing design.

---

## integration-through-a-merge-queue.md — Integration through a merge queue
Verdict: partly stale (the base mechanics match; the order section, the draft/direct-merge rules and the trial text do not)
Size: 59 KB. Last changed: 2026-11-21.

### Findings
- G3-1 [DIFFERS] The age rule is four hours.
  - Document: line 143: "An entry waiting four hours or more since admission goes first"; also lines 139, 170, 189.
  - Code: `internal/orchestrator/mergequeueorder.go:29` `mergeQueueAgeLimit = 8 * time.Hour`; `:34` the rule text is "waited eight hours or more"; `:403-418` applies it.
  - What to change in the document: say eight hours is built, and four hours is waiting on item yoyodyne-ifd.429.266 (open).
- G3-2 [DIFFERS] Rule 2 is priority 0 code only, with "no document beside priority 0 code", and rule 3 alternates documents with ordinary code.
  - Document: lines 144-145, 151: "no document entry has a place beside priority 0 code"; "a document candidate never lands twice in a row while ordinary code waits".
  - Code: `mergequeueorder.go:35` `MergeQueueRuleUrgent = "priority 0 code, or the architect's oldest document"`; `:420-431` puts the oldest architect document into the urgent group; `:432-436` takes any document entry before all other code; there is no "last landing was a document" test anywhere in `rank`.
  - What to change in the document: describe the order as built (age, then priority 0 code plus the architect's oldest document, then documents, then code by priority, then unreadable) and mark the rest as plan.
- G3-3 [NOT BUILT] Waiting documents land together in one candidate of at most eight, split on failure.
  - Document: lines 153-164 and piece 8 (line 457).
  - Code: absent. `pick` (`mergequeueorder.go:257-312`) returns one entry; the only `batch` in the queue code is the tracker read (`:317-365`). Item yoyodyne-ifd.429.267 is open.
  - What to change in the document: move the whole subsection to a "not built" part.
- G3-4 [NOT BUILT] A candidate's pull request opens as a draft; the harness marks it ready and merges in one step; it returns to draft before a push and on withdrawal; a merge the harness did not mark ready files a critical report.
  - Document: lines 231-243; line 243 "Build work: one piece".
  - Code: no mark-ready or return-to-draft call exists in `internal/publish` or `internal/orchestrator`. `EnsureDraft` (`internal/publish/cichecks.go:178-183`) is the checks-on-CI path and leaves an open request "in whatever draft state it is in". Item yoyodyne-ifd.429.233 is open.
  - What to change in the document: mark as plan; say what the CI path does today.
- G3-5 [NOT BUILT] The merge is asked for directly, never by merge when ready; a moved main withdraws every pushed candidate and rebuilds it with no repair charged; GitHub's "not up to date" refusal counts as a moved main.
  - Document: lines 257-269.
  - Code: `internal/publish/github.go:522` always tries `gh pr merge ... --auto` first (`MergeResult{Queued: true}`), and `internal/orchestrator/mergequeuepromotion.go:857-871` settles a queued result as `MergeQueueMutationQueued`. There is no per-pass sweep of pushed candidates against main. Item yoyodyne-ifd.429.243 is open (and .229, a conflict check on every waiting entry when main moves).
  - What to change in the document: mark as plan; say that today the lease read before the merge and the post-landing confirmation (`mergequeueconfirm.go`) are the guards.
- G3-6 [NOT BUILT] `yoyo doctor` reports a target branch that does not require the configured checks or an up-to-date branch, with the switch on or off.
  - Document: lines 262, 269, 398, 444.
  - Code: no merge-queue, protection or required-checks check in `internal/doctor/*.go` (`team.go`, `trackerremote.go` and the others name none). `publish/github.go:1047` reads rulesets only to decide whether the branch is protected.
  - What to change in the document: mark the doctor reports as plan, or say which item builds them.
- G3-7 [DIFFERS] The check set follows what the candidate changes, never the entry's recorded kind.
  - Document: lines 210-213: "An entry recorded as a document whose candidate changes any other path is checked like every other entry"; line 223: "letting the entry's recorded kind choose the checks" is rejected.
  - Code: `mergequeueadmission.go:85-88`: `entry.WorkItemID == entry.RunID && ... len(p.Config.DocumentChecks) > 0` returns the document checks. This is the recorded kind (a document run), not a path test. (The order, by contrast, does read paths: `mergequeueorder.go:195-214`.)
  - What to change in the document: either say the route's own refusal before admission (`documentRouteRefusal`, `documentpublication.go:1520`) is what keeps code out, or file the code gap with the architect.
- G3-8 [DIFFERS, status of a plan] The trial on this repository is described as not yet started.
  - Document: lines 317-366 ("The trial starts with the landing of the change that turns the setting on").
  - Code: `.yoyodyne/config.yaml:192-197` has `ci_checks: enabled: true`, `local: [make fmtcheck, make vet, make build, make test-macos]`, `required: [test, race, vet]`, `wait_limit: 60m`. Item yoyodyne-ifd.429.76.8 is closed. `merge_queue: true` at `:178`.
  - What to change in the document: say the switch is on in this repository and give the start time; move "the order of the switch" to history.
- G3-9 [UNCLEAR] Piece 10: daily median and longest wait per kind of entry, shown on `yoyo status` and the dashboard. I did not find a reader for it in `internal/readmodel`; I did not search every surface.
- G3-10 [UNCLEAR] A check ended by the machine running it is run once more, and a second ending withdraws the entry with a report (lines 217, 425). I found no such rule in `mergequeueworker.go`, `queuedchecks.go` or `internal/checks`; the check-rerun code in `mergequeuecheckrerun.go` is for a forge check that never started.
- G3-11 [UNCLEAR] After a restart, passed checks are kept only where commit and check-set digest are both recorded (lines 219, 426). `mergequeuerestart_test.go` exists; I did not read it.
- MATCHES (one line each):
  - G3-12 `execution.merge_queue` switch; this repository sets it (`config.yaml:178`).
  - G3-13 Priority is not recorded at admission; read each choice (`mergequeueorder.go:5-7, 262-291`).
  - G3-14 A tracker that cannot be read orders by remembered priority and placement; entries never read go last in admission order (`QueueReadings`, `:55-100`; `:296-311`), and the choice records it (`TrackerUnread`, `:113-124`).
  - G3-15 Placement within priority 0 and within each priority, earliest placement first (`placedFirst`, `:466-478`).
  - G3-16 In-progress candidates are not stopped for a higher entry; a dependent entry waits for its blocker (`:164-199`, `:293-303`).
  - G3-17 Document versus code is decided by paths (`documentKind`, `:195-214`; `changeIsDocument`, `documentpublication.go:1499`).
  - G3-18 The queue checks the candidate whole (`mergequeueworker.go:838` `Narrowing{Whole: true}`), with a declared list (`merge_queue_checks`, `config.yaml:473-477`; `mergequeuedeclared.go`).
  - G3-19 No candidate is checked ahead where landing goes through a pull request (`mergequeueahead.go:30-42`).
  - G3-20a Post-landing confirmation of the first parent and files, with a critical report (`mergequeueconfirm.go:23`).
  - G3-20b The queue holds waiting changes when protection requires GitHub's merge queue or cannot be read (`internal/queuemode/queuemode.go:304`).
  - G3-20c Pieces 1 to 7 of "Pieces of work" are built: their items (.185, .186, .187, .76.7) are closed.

### Missing from the document
- The hand-back mechanics. The code hands a defective candidate back to the same run with the failure (`MergeQueueRepair.HandBack`, `mergequeuerecovery.go:94-108`), and reports it to a role only when no single run can take it up (`Report`, `:103-108`). The document says only "reuse integration continuation" (line 406) and never names who receives a hand-back.
- A person's merge of a candidate and its follow-up (`personmerge.go`, `personmergeupdate.go`), and the stuck-merge handling (`stuckmerge.go`).
- Entries that carry pinned outside files (`mergequeueoutside.go`, `outsidefilequeue.go`); the document only links to pinned-outside-files.md.
- Where the queue's selection records are stored and what `yoyo status` prints; the document says "shown" without a field list.

### Overlap with other documents
- Lines 208-213 and 144 repeat the check-selection rule of document-landing-route.md rule 3 (lines 32). They agree in words; the code does not (G3-7).
- Lines 281-283 ("Checking one entry ahead") repeat merge-queue-pace.md decision 3 (lines 114-124). They disagree: the queue design says no checking ahead on the pull request path; the pace design (line 124) says this was not established.
- Lines 166-167 point to integrating-to-a-local-branch.md and agree with it.
- Lines 287-304 repeat checks-on-ci.md. Not compared (other group).

### Could be cut or moved
- Lines 137-139 and 168-170: the November 20 incident figures (7.9 hours, 22 minutes); the history belongs in the item.
- Lines 306-315 "Why this way out of the slow queue" and the rejected-alternatives paragraphs (lines 202, 223, 241, 264, 304, 388): about 60 lines of reasoning that restate decisions made elsewhere.
- Line 463 "Evidence and publication limits": says which files were read at a named commit and "does not claim implementation". Remove; it is wrong for the built parts.
- The "Pieces of work" list (446-459) duplicates the "Build work" lines inside each section.

---

## merge-queue-pace.md — How the harness's merge queue lands more than one change an hour
Verdict: partly stale
Size: 17 KB. Last changed: 2026-11-19.

### Findings
- G3-21 [DIFFERS] The queue design has not decided whether a candidate lands as its own commit through the pull request path.
  - Document: lines 124, 161: "This design does not establish whether a candidate landed through the forge's pull request path lands as exactly its own commit. The build piece reads the landing code first."
  - Code: `internal/orchestrator/mergequeueahead.go:30-42`: the forge's merge-commit method puts a merge commit above the candidate, so "the worker checks nothing ahead and says why" on that path.
  - What to change in the document: state the answer and the limit (checking ahead helps only a project that lands locally).
- G3-22 [DIFFERS, between documents] The run on main every four hours is the whole `make race`.
  - Document: lines 62-70, 104.
  - Code: `.yoyodyne/config.yaml:465-466` `main_checks: - make race` (whole). The closing note of yoyodyne-ifd.429.254 records a ruling on 2026-11-20 in one-owner-for-each-guarantee.md that point 4 narrows to the macOS-only packages, "because GitHub's required race job already runs the whole make race". The code has not changed yet, and this document is not updated.
  - What to change in the document: point race point 4 to one-owner-for-each-guarantee.md instead of restating it.
- G3-23 [UNCLEAR] "The same test failing and then passing on main twice within seven days files the critical report after all" (line 67). `internal/maincheck/maincheck.go` reruns failing tests once and reports at warning (lines 18-22, 424-440, 489-499), but I found no seven-day repeat count there; `runstate/maincheck.go:39` keeps a history of about ten days. Not confirmed either way.
- G3-24 [UNCLEAR] Piece 5, weekly figures for candidates started, landed and thrown away by checking ahead (line 163). Not found in `internal/readmodel/checktimes.go`; not searched further.
- MATCHES:
  - G3-25 The queue runs the declared list whole on the exact commit (`mergequeueworker.go:838`), and the race check is added where the approved head has none (`merge_queue_race_check`, `config.yaml:477`; `raceOwed`, `mergequeuedeclared.go:83`).
  - G3-26 Checks and review run side by side (`mergequeueworker.go:152, 236`).
  - G3-27 The next candidate is built on the current one, one deep, rebuilt without charge when the target is not its base (`mergequeueahead.go:1-40`).
  - G3-28 On main, a failing check's tests are run once more on the same commit and a pass the second time is reported at warning, with the last clean commit unmoved (`maincheck.go:18-22, 424-440, 489-499`).
  - Note: the document says this repository declares "`make test`" whole; `config.yaml:475` writes it with `CHANGED_GO_PACKAGES`, but the queue passes `./...` (`mergequeueworker.go:838`), so the document is right in effect.

### Missing from the document
- The configuration names: `merge_queue_checks` and `merge_queue_race_check`. The document says "the project declares" but never gives the keys.
- With checks on CI on, the whole declared list is replaced by the quick checks plus GitHub's required checks (`mergeQueueChecks`, `mergequeueadmission.go:84-99`); this supersedes decision 1 for this repository today. The document does not say so.

### Overlap with other documents
- Lines 106-124 repeat integration-through-a-merge-queue.md lines 215 and 281-283. Agree except G3-21.
- Lines 144 repeats document-landing-route.md rule 3. Agree.
- Lines 94-104 (case 2) repeat choosing-checks-by-change.md (other group); not compared.

### Could be cut or moved
- Lines 40-50 and 196-213: operator's assistant figures "not yet checked", and the "Read at main 06f384c0 / Not read" list, which is false now (the queue code was read by the build items).
- Lines 132-140 (combining changes, rejected): superseded by the document-batch ruling in the queue design.
- Lines 176-185 "Designs this changes" says "Until each is revised, this design decides where they differ"; the queue design is now revised, so cut.

---

## integrating-to-a-local-branch.md — Integrating to a local branch and handing landed work to the team
Verdict: partly stale (sections 1 to 4 describe built code; section 5 is a plan not built)
Size: 49 KB. Last changed: 2026-11-21.

### Findings
- G3-29 [NOT BUILT] The harness's own batch pull request route: `execution.batch_pull_request` with six settings, branch `yoyodyne/batch/<target>`, review request by command or address, merge on approval.
  - Document: lines 215-352, build items 7 to 14 (lines 424-431).
  - Code: no `batch_pull_request` or `BatchPullRequest` in `internal/` (a search of `internal` and `docs` finds it only in this document). Items yoyodyne-ifd.429.127.3 to .10 are open.
  - What to change in the document: move section 5 to its own design, marked as not built; remove the "The seven asks" table, which tracks status by build item number.
- G3-30 [UNCLEAR] A repair of an item waiting on a harness-opened pull request starts from the kept branch's change applied onto the target head, and replaces the kept branch's head by compare and swap (line 146, build item 3, line 420). The only force-with-lease push I found is `internal/publish/cichecks.go:141` (checks on CI). Item yoyodyne-ifd.429.127.11 is open and the amendment landed on 2026-11-21, so the code probably lags; not confirmed.
- G3-31 [UNCLEAR] "Three rounds for the update", counted apart from the item's attempts (line 173). `internal/orchestrator/personmergeupdate.go:92` mentions rounds for bringing a pull request up to date; I did not find the same limit for the team-branch update in `teambranchupdate.go`.
- MATCHES:
  - `execution.upstream_branch` unset by default (`config.go:425`).
  - `yoyo pull-request track/untrack` with `--unverified` (`cli/pullrequest.go:64-96`).
  - `yoyo landing hold <target> --for --reason` and `release`; four-hour cap (`runstate/landinghold.go:38` `MaxLandingHold = 4 * time.Hour`; `cli/landing.go`).
  - The tracked-pull-request, feedback and team-branch update code exists (`orchestrator/teampullrequest.go`, `teampullrequestfeedback.go`, `teamfeedbackrepair.go`, `teambranchupdate.go`, `teammerge*.go`); item 3 and others closed.
  - Tracked pull requests are not counted against `max_waiting_pull_requests` (not checked in code; accepted from the item list).

### Missing from the document
- Whether the configuration reference (`docs/configuration.md`) names `upstream_branch`; the document should link to it for the built part.

### Overlap with other documents
- Line 146 versus working-alongside-other-contributors.md line 148: DISAGREE. Line 148 there says the new run starts "from the head of the pull request's kept branch"; line 146 here says it starts from the kept branch's change applied onto the target branch's current head, and rejects starting from the old head (line 159). Working-alongside has not been updated for the November 21 amendment.
- Lines 82-86 and 270-271 repeat the working-alongside sections 1 to 3. They point there and agree.
- Line 166 of integration-through-a-merge-queue.md says the batch route is separate from the document batching of the queue. Agrees.

### Could be cut or moved
- Lines 217 (the user's 720-line shell job, November 8), 362-371 (alternatives, partly duplicating the "Rejected" lists inside sections 2, 3, 5), and the "The seven asks" table (340-350).
- Reviewer-check list (390-414) repeats "What is enforced in code" (373-388) almost line for line; keep one.

---

## working-alongside-other-contributors.md — Working alongside other contributors
Verdict: partly stale (sections 1 to 6 and claims, records and budgets are built; tracker sharing through a reference is not)
Size: 48 KB. Last changed: 2026-11-20.

### Findings
- G3-32 [NOT BUILT] The tracker's export travels through `refs/yoyodyne/tracker`, written as Git objects with a lease; the merge reads that reference, not the target branch.
  - Document: lines 223, 248-264, build item 15 (line 354).
  - Code: no `refs/yoyodyne/tracker` constant. The references the code names are claims, pause, records, fetched, building and preserved (`internal/gitworktree/recordsref.go:38`, `claimrefs.go`, `teampause.go`). The item-by-item merge itself is built (`internal/beads/exportmerge.go`, `runstate/trackermerge.go`), but its comment still points at "section 5" of this design (the design's section is 7).
  - What to change in the document: split "merge built, reference not built"; the group design `project-groups.md` item yoyodyne-t5r4.4 (open) waits on this.
- G3-33 [DIFFERS] This document replaces the earlier draft, `docs/team-mode-coordination.md`, through build item 13.
  - Document: lines 107, 352, 364: "The file is outside the architect's folders, so a developer run changes it."
  - Code: `docs/team-mode-coordination.md` now holds a short replacement note ("Distributed coordination for team mode: replaced"), so item 13 is done.
  - What to change in the document: drop build item 13 and the closing bullet.
- G3-34 [DIFFERS, between documents] An ended run's repair starts "from the head of the pull request's kept branch" (line 148). See the overlap note with integrating-to-a-local-branch.md line 146.
- G3-35 [UNCLEAR] Forge state read for up to 50 pull requests a GraphQL query (line 213, "`internal/publish/conditional.go`"). The file exists; I did not read the batch size.
- MATCHES:
  - G3-36 Claim reference `refs/yoyodyne/claims/<item id>`, renewed every 15 minutes, expiring after two hours (`internal/teamclaim/teamclaim.go:31, 44`).
  - G3-37 Team pause reference `refs/yoyodyne/pause` (`gitworktree/teampause.go`, `teamclaim/pause.go`).
  - G3-38 `execution.max_waiting_pull_requests` default 5 (`config.go:814`; `docs/configuration.md:5258`).
  - G3-39 Forge check every five minutes by default, a limit of its own (`config.go:764` `DefaultForgeCheckInterval`).
  - G3-40 The hidden mark `<!-- yoyodyne: <identity> -->` (`internal/factory/mark.go:20`).
  - G3-41 Records reference `refs/yoyodyne/records` (`gitworktree/recordsref.go:38`) and the packages `teamrecords`, `teamspends` for records and spends.

### Missing from the document
- The `internal/teamclaim`, `teamrecords`, `teamspends` packages and `internal/factory/mark.go` are not named, so a reader cannot find the code from the design.
- The person-merge path the harness takes (`orchestrator/personmerge.go`, `personmergeupdate.go`) is described only as behaviours.

### Overlap with other documents
- Lines 148 and 153 versus integrating-to-a-local-branch.md lines 146 and 165-173: see G3-34 (disagree on where the repair run starts; agree on the three-round limit).
- Lines 201 and 306-307 versus integration-through-a-merge-queue.md lines 225-227 (promotion by compare and swap, "never force the target"). Agree.
- Section 5 (claims, team pause) overlaps machine-home.md and project-groups.md (other groups); not compared.

### Could be cut or moved
- Lines 125-135 and 289-292: the one-person-owner-account reasoning appears three times (lines 125, 142-144, 290).
- Lines 205 (approvals from the command line) and 288 (Dolt remotes): outside this design's subject.
- Lines 358-364 "Documents this amends": most items are done; keep one line.

---

## document-landing-route.md — A route of its own for landing confirmed documents
Verdict: partly stale (the rules are built; the "Code this makes wrong" section describes code that was already changed)
Size: 5 KB. Last changed: 2026-10-13.

### Findings
- G3-42 [DIFFERS] "Code this makes wrong" names four defects that no longer exist.
  - Document: lines 44-49: "`MaxPendingWrites = 2` in `internal/runstate/conversation.go`"; "`publishDocumentAttempt` reserving developer capacity"; "The missing withdraw action."
  - Code: `internal/runstate/conversation.go:685` `MaxPendingWrites = artifact.MaxWaitingDocuments`, and `internal/artifact/write.go:392` `MaxWaitingDocuments = 8`; the withdraw request exists (`artifact/write.go:368-369`); `pipeline.go:722-724` reserves with `ReserveDocumentLanding` against `DocumentLandingLimit()`.
  - What to change in the document: delete the section.
- MATCHES:
  - G3-43 `execution.document_landings` default 2 (`config.go:328`), separate from developer slots (`pipeline.go:722-724`; `schedule documents.go:7`).
  - G3-44 Qualifying paths are the artifact homes and their indexes; others are refused from the route (`documentRouteRefusal`, `documentpublication.go:1520`).
  - G3-45 `document_checks` is declared and a project with none runs its full checks (`config.go:113-119`; `config.yaml:490-491` `make doccheck`; `mergequeueadmission.go:86`).
  - G3-46 At most eight waiting documents per role, with a typed withdraw (see G3-42 code).
  - G3-47 Three runs without a verdict stop publication: run notes read "attempt 1 of at most 3".
- G3-48 [UNCLEAR] Rule 3 names the document checks as `internal/terms`, `scripts/vocabulary`, `internal/doclink`, `internal/artifact`, `internal/conformance`. The configured check is `make doccheck`; the test list is the Makefile's target, which I did not read.
- G3-49 [UNCLEAR] "Every write is answered by the end of the writing role's next pass; an unanswered write is a harness defect" (line 38). I did not find the report.

### Missing from the document
- That the landing no longer ends in the run: the confirmed document is now an entry in the merge queue (`mergequeueadmission.go:86` picks its checks; `mergequeueorder.go` orders it) and, as the queue design says, documents are ordered and, once built, batched there. Rule 4 says only "through the merge queue when it is switched on".
- The architect's oldest document gets a place beside priority 0 code (`mergequeueorder.go:420-431`).

### Overlap with other documents
- Rule 3 (line 32) versus integration-through-a-merge-queue.md lines 208-213 and merge-queue-pace.md line 144. Agree in words; see G3-7 for the code.
- The reviewer's check "merges through the pull request" (line 57) versus integration lines 227-241: the pull request is now a candidate pull request opened by the queue, not by the document run.

### Could be cut or moved
- Lines 24 and 26 (incident story of October 11, the 25 to 30 minute landing); the "Code this makes wrong" section (44-49).

---

## unpublished-designs-and-admitted-work.md — A design not yet published is checked against the admitted work it touches
Verdict: mostly a plan not built
Size: 9 KB. Last changed: 2026-11-20.

### Findings
- G3-50 [NOT BUILT] The harness works out which unpublished documents overlap an open item (by item identifier in the body or reason, or by a cited document) and lists it in the architect's and the Lead Product Manager's passes until answered.
  - Document: lines 42-57, 106-110 (build pieces 1 to 4).
  - Code: no overlap list or "agreement" action found (searches for the agreement-note wording and for an overlap reader in `internal/` return nothing). Item yoyodyne-ifd.78.10 is open and carries the build.
  - What to change in the document: the opening already says it is a ruling; add "not built" to the status or merge it into the publishing design.
- G3-51 [NOT BUILT] An action the architect takes that has the harness write an agreement note on an item (line 110: "Today the architect can read items but write nothing on them").
  - Document: lines 73, 110.
  - Code: absent in `internal/artifact`, `internal/action`; item .78.10 open.
- G3-52 [UNCLEAR] "Every written document's record keeps the time it was written, the items it names, and the documents it revises" (lines 95-96). I could not find fields for items named or documents revised in `internal/runstate`'s document publication records. Piece 1 says it still has to be built, so this is the future state, not a claim about today.

### Missing from the document
- Where in the pass briefings the list goes (no file or section named), and the record format for "answered".

### Overlap with other documents
- Lines 106-110 overlap document-landing-route.md rule 5 (waiting documents per role) and its status words ("waiting", "confirmed", "refused"); no conflict.
- Line 22 refers to a companion intent repository, the subject of project-groups.md (other group); not compared.

### Could be cut or moved
- Lines 24-26 (the November 9 incident on another project): belongs in the item (yoyodyne-ifd.78.8).
- The "What is enforced in code" list (92-96) repeats sections 3 and 5; the "What a reviewer can check" list (98-104) repeats build piece 4.
