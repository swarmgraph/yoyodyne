# yoyodyne-ifd.437.84: second reading of the design documents against the code, group 3: integration and merging

A second, adversarial reading of the report `yoyodyne-ifd-437-84-3.md` beside this file. It re-read every finding there that differs or is not built against the code, tried to settle the unclear ones, checked a sample of the matches, and looked for what the first reading missed. **Where the two files disagree, this one stands.**

- **Read on:** 2026-11-21, in the evening, between 18:33 and 18:43 PST, with main at `848d86190`. The code had moved since the first reading; each finding is judged against the code as it was at this reading.
- **Read by:** a read-only reader on Opus 5.5 in the operator's assistant's session. It read code and ran nothing, tests included.
- **Checked by hand:** nothing in this file was checked again by hand.
- **How to use it:** evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Second-reading classes:** `CONFIRMED`, the first finding is right as written; `CORRECTED`, its gist is right and a detail is wrong; `WRONG`, the design and the code agree or the finding misread one of them; a class in the first reading's own words where an unclear finding was settled.

Read against main at `848d86190` (2026-11-21, evening PST). Read only; nothing run.

## Summary
- Findings re-read (DIFFERS and NOT BUILT): 20. CONFIRMED 18, CORRECTED 2, WRONG 0, CANNOT TELL 0.
- Unclear findings settled: 11 of 11, two of them only in part (G3-10, G3-52). Results: MATCHES 5 (G3-11, G3-30, G3-35, G3-48, G3-49), NOT BUILT 4 (G3-9, G3-23, G3-24, G3-31), DIFFERS in part 1 (G3-10), NOT BUILT in part 1 (G3-52).
- Verdicts changed: none.
- Findings the architect should not act on as written:
  - G3-7: the code line is `mergequeueadmission.go:86-87`, not 85-88. In practice the gap is narrow, because `gateDocument` (`documentpublication.go:1576-1593`) lets a document run change only its one path, and only inside the artifact homes. The design still describes a path test that the code does not run.
  - G3-10 (first reading's note): `mergequeuecheckrerun.go` covers more than a forge check that never started. It covers `cancelled`, `timed_out` and `startup_failure` (`:20-28`).
  - G3-20c: pieces 1 to 7 are not all built. Piece 2's display half is missing (see "Missed" below).
  - "Missing from the document", hand-back: the hand-back is `MergeQueueRunHandback.HandBack` / `.Report` at `mergequeuerecovery.go:1041, 1201`. There is no `MergeQueueRepair.HandBack` at `:94-108`.
  - G3-35: the code is right, but the 50 is `MaxStatesPerQuery` at `internal/publish/github.go:759`. `conditional.go:21-24` only mentions it in a comment.
- Most important misses:
  - `yoyo status` and the dashboard list queue entries in admission (place) order, not in the order the queue chooses, and they never show the rule that chose an entry. The rule is written only on the generation record (design lines 181, 197, 451).
  - A local queue check that ends without a verdict (timed out, cancelled or stalled) is run again on every pass. Nothing is charged, there is no limit, and the entry is never withdrawn. Design line 217 says a second ending withdraws the entry.
  - merge-queue-pace's rule that a test failing then passing twice in seven days files a critical report is not built. Every second-try pass is a warning (`internal/maincheck/maincheck.go:489-515`).

## What the merge queue's order is in the code now

`internal/orchestrator/mergequeueorder.go` at `848d86190`. This file last changed at `c680ad457` (06:48 PST today). The four-hour rule (yoyodyne-ifd.429.266) is open at priority 0; it was raised to 0 at 17:42 PST today. Document batching (.267) is open. Neither is on main.

`pick` (`:241-315`) reads every waiting entry's item in one batch (`readEntryItems`, `:339-360`). Where the tracker cannot be read, it uses the last priority it read. It then drops any entry whose item depends on another waiting entry's item, unless every entry is held that way (`:301-311`). `rank` (`:397-458`) then takes the first rule that applies:

0. Only one entry waiting: that entry (`:242-244`).
1. Any entry whose newest candidate has begun its checks or review (`:398-402`, `entryInProgress` `:227-237`).
2. Any entry waiting **8 hours** or more since admission, oldest first (`:29`, `:403-417`).
3. Two kinds of entry go together into one group: the architect's oldest document (the first in admission order whose paths are all in architect-owned homes), and code whose priority was read as 0. Within the group, the earliest placement goes first, otherwise the earliest admission (`:418-434`).
4. Any other document (all paths in the artifact homes), first admitted. Placement is not consulted (`:435-439`).
5. Code with a readable priority, lowest number first, then by placement, then by admission (`:440-456`).
6. The rest, first admitted (`:457`).

Whether an entry is a document is decided by its paths against its base (`documentKind`, `:199-214`), never by its recorded kind.

## integration-through-a-merge-queue.md
Verdict: agree (partly stale).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G3-1 | DIFFERS | CONFIRMED | `mergequeueorder.go:29` is 8h; rule applied at `:403-417` (not 418). Design `:143`. |
| G3-2 | DIFFERS | CONFIRMED | Urgent group `:418-434`; documents before all other code `:435-439`; no last-landing test. |
| G3-3 | NOT BUILT | CONFIRMED | `pick` returns one entry (`:241-315`); the only batch is the tracker read (`:339-360`). |
| G3-4 | NOT BUILT | CONFIRMED | Candidate pull request opened by `Publisher.Ensure` (`mergequeuepromotion.go:779`), not a draft; no mark-ready call anywhere. |
| G3-5 | NOT BUILT | CONFIRMED | `publish/github.go:522-528` tries `--auto` first; `mergequeuepromotion.go:857-871` settles Queued. |
| G3-6 | NOT BUILT | CONFIRMED | No protection or required-check check in `internal/doctor/*.go`. |
| G3-7 | DIFFERS | CORRECTED | `mergequeueadmission.go:86-87` (recorded kind: `WorkItemID == RunID`). Effect is narrow: `gateDocument` (`documentpublication.go:1579-1584`) restricts a document run to one path inside the homes. |
| G3-8 | DIFFERS | CONFIRMED | `.yoyodyne/config.yaml:192-196` `ci_checks.enabled: true`; .429.76.8 closed 2026-11-18. |
| G3-9 | UNCLEAR | NOT BUILT | No daily median or longest wait by kind in `internal/readmodel`, `internal/cli` or `runstate/mergequeuerecords.go`. |
| G3-10 | UNCLEAR | DIFFERS in part | Three paths. (a) Local queue check timed out, cancelled or stalled: Problem set (`mergequeueworker.go:892-901`), classed as infrastructure, run again with nothing charged (`mergequeuerecovery.go:211-214`, `mergequeueworker.go:745-749`). No count and no withdrawal: **DIFFERS** from line 217. (b) Forge required check before landing: one rerun, then the full local checks (`mergequeuequick.go:109-119`), which matches line 296. (c) Forge check on a held merge, `cancelled`, `timed_out` or `startup_failure`: rerun once, and a second ending withdraws the entry (`mergequeuecheckrerun.go:20-44`, `mergequeuedriver.go:339-365`), which matches line 217. |
| G3-11 | UNCLEAR | MATCHES | `keptQueueChecks` (`mergequeueworker.go:958-991`) keeps results only where the interruption's Binding equals the generation's; `Binding` covers Candidate and Checks.Digest (`runstate/mergequeuegeneration.go:272-287`). |

### Missed by the first reading
- Design `:181`, `:197`, `:451` (piece 2): status and the dashboard should show the waiting entries in the queue's order, with the rule. `readmodel/mergequeues.go:287` orders them by `MergeQueueWorkOrder`, which is place and then admission (`runstate/mergequeue.go:237-248`). `RenderMergeQueues` (`:464-485`) prints no rule. `MergeQueueSelection` is written only to the generation (`mergequeueworker.go:276`), and no surface reads it.
- Design `:217`: a local queue check that keeps timing out loops without limit (see G3-10a). Nothing withdraws the entry or names the check in a report.
- Design `:176` versus code: the in-progress rule outranks the age rule (`rank` `:398-402` comes before `:403`). That agrees with the "further rules", but the design's numbered list never says that the in-progress rule comes first.

### Contradictions with other designs
- Line `:283` (no checking ahead on the pull request path) against merge-queue-pace.md `:124` and `:161` (not established). The code sides with this design (`mergequeueahead.go:29-35`).
- Lines `:153-164` (documents batched) against merge-queue-pace.md `:132-140` (combining changes rejected) and `:185` ("Until each is revised, this design decides where they differ"). This design was revised after pace, so pace's precedence clause now points the wrong way.
- Line `:217` (a second machine ending withdraws the entry) and line `:296` (after a rerun, fall back to the full local checks) both cover a cancelled check, with different outcomes. checks-on-ci.md `:98` and `:164` agree with `:296`.

## merge-queue-pace.md
Verdict: agree (partly stale).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G3-21 | DIFFERS | CONFIRMED | `mergequeueahead.go:29-35` settles it: nothing is checked ahead on the pull request path. |
| G3-22 | DIFFERS | CONFIRMED | `config.yaml:465-466` is whole `make race`; one-owner-for-each-guarantee.md `:301` narrows race point 4 to the macOS-only packages. Pace `:63-70` is stale on both counts. |
| G3-23 | UNCLEAR | NOT BUILT | `maincheck.go:442-475` reruns the failing tests once; `reportUnderLoad` `:489-515` is always a warning. No seven-day repeat count in `internal/maincheck` or `runstate/maincheck.go`. |
| G3-24 | UNCLEAR | NOT BUILT | No weekly queue figures (candidates started, landed, thrown away by checking ahead) in `readmodel` or `cli`; `checktimes.go` has none. |

### Missed by the first reading
- Pace `:72` and `:156` (the narrowed race check is added where a head has none). This repository has checks on CI on, so `mergeQueueChecks` takes `quickQueueChecks` (`mergequeueadmission.go:89-90`) before the declared list. Because the target requires `race` (`config.yaml:195`), no queue race check is added (`mergequeuequick.go:94-107`). The first reading noted the replacement but not this consequence.

### Contradictions with other designs
- See above: `:124` against the queue design `:283`; `:132-140` and `:185` against the queue design `:153-164`; `:63-70` against one-owner-for-each-guarantee.md `:301`.

## integrating-to-a-local-branch.md
Verdict: agree (sections 1 to 4 current, section 5 not built).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G3-29 | NOT BUILT | CONFIRMED | `batch_pull_request` appears only in this design and the report. |
| G3-30 | UNCLEAR | MATCHES | `pullrequestupdate.go:1-15`: the run starts from the kept change lifted onto the target (`runstate.Lift`) and replaces the branch from the harness's last head through `ReplaceBranch` (`gitworktree/remote.go:378`, `--force-with-lease`). The refusal entry for a foreign commit was not traced. |
| G3-31 | UNCLEAR | NOT BUILT | A team-branch update conflict is recorded and reported (`teambranchupdate.go:553-567`), but there is no decision kind bound to it and no three-round limit for it. `TeamBranchUpdateConflict` is used only in `runstate/teambranchupdate.go` and `orchestrator/teambranchupdate.go`. The three-round limit that does exist, `MaxUpToDateConflicts = 3` (`runstate/personmerge.go:240`), covers bringing a person-merge pull request up to date. |

### Missed by the first reading
- Design `:173`: the decision bound to the conflict entry, the new run, and its own three-round limit are not built (G3-31). The section reads as built.

### Contradictions with other designs
- `:146` against working-alongside-other-contributors.md `:148`, which says the repair starts "from the head of the pull request's kept branch". The code follows `:146`.

## working-alongside-other-contributors.md
Verdict: agree.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G3-32 | NOT BUILT | CONFIRMED | No `refs/yoyodyne/tracker` in `internal`. The comments in `beads/exportmerge.go:4` and `runstate/trackermerge.go:4` still cite "section 5"; the tracker export is section 7 (`:221`, `:248`). |
| G3-33 | DIFFERS | CORRECTED | Correct that item 13 is done (`docs/team-mode-coordination.md:1-12`). The stale lines are `:352` (item 13) and `:364`. Line `:107` ("This design replaces the earlier draft") is still true. |
| G3-34 | DIFFERS | CONFIRMED | `:148` against integrating-to-a-local-branch.md `:146`; the code (`pullrequestupdate.go:6-9`) follows the latter. |
| G3-35 | UNCLEAR | MATCHES | `MaxStatesPerQuery = 50` at `publish/github.go:759`. The design's file reference (`conditional.go`) is only a comment that points there (`:21-24`). |

### Missed by the first reading
None beyond the above.

### Contradictions with other designs
- G3-34, as above.

## document-landing-route.md
Verdict: agree.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G3-42 | DIFFERS | CONFIRMED | `runstate/conversation.go:685`; `artifact/write.go:392` (8), withdraw `:369-373`; `pipeline.go:724` `ReserveDocumentLanding`. |
| G3-48 | UNCLEAR | MATCHES | `Makefile:326-338` `DOCCHECK_TESTS` lists the five packages plus named tests in eight others, as "any other test that reads a governed document" allows. |
| G3-49 | UNCLEAR | MATCHES | `internal/chat/waitingdocuments.go:265-300` (`answerUnanswered`, `fileUnansweredDefect`) files a warning report. |

### Missed by the first reading
- Rule 3 (`:32`) puts the document check choice on "document"; the queue chooses it by recorded kind (G3-7). The code also gives the architect's oldest document a place beside priority 0 code (`mergequeueorder.go:418-434`), which the integration design `:151` and `:202` reject. This design is silent on order, so the clash is between the integration design and the code.

### Contradictions with other designs
None found beyond the overlap the first reading listed.

## unpublished-designs-and-admitted-work.md
Verdict: agree (mostly a plan not built).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G3-50 | NOT BUILT | CONFIRMED | No overlap reader; "unpublished" in `internal` is used only for promotions. Item .78.10 open. |
| G3-51 | NOT BUILT | CONFIRMED | No agreement action in `internal/action` or `internal/artifact`. |
| G3-52 | UNCLEAR | NOT BUILT (in part) | `PendingWrite` keeps `WrittenAt` (`runstate/conversation.go:516`) but has no field for items named or documents revised (`:509-545`). |

### Missed by the first reading
None.

### Contradictions with other designs
None found.
