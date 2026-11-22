# yoyodyne-ifd.437.84: design documents against the code, group 5: roles and management

One of nine reports comparing the design documents in `docs/designs/` with the code, made at the operator's request of 2026-11-21 for a review of every design against the current code. This one covers group 5, roles and management.

- **Read on:** 2026-11-21, between 15:40 and 15:48 PST. Main moved from `2447eeaab` to `fe2db3d37` at 15:41, a minute into the reading; that change was 22 files, mostly the team feedback repair code in `internal/runstate`.
- **Read by:** a read-only reader in the operator's assistant's session, on Sonnet 5.5. It read code and ran nothing, tests included.
- **Checked by hand:** the operator's assistant checked three findings against the code. G5-1, G5-3 and G5-4 held. In G5-3 the words "no `unlanded` anywhere in internal/" are too broad: the word appears in internal/chat/substrate.go for a different thing; the amendment listing has no such field, which is the finding. Every other finding is unchecked.
- **How to use it:** this is evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Classes:** `MATCHES`, the code does what the design says; `DIFFERS`, the code does something else; `NOT BUILT`, the design calls it future or the code is absent; `UNCLEAR`, not established.

## Summary
- Documents: 9. Claims checked: about 100 (10 to 12 each in the short ones, about 18 in program-manager.md and ownership-and-the-operator.md). MATCHES 70, DIFFERS 4, NOT BUILT 8 (whole features, counted once each), UNCLEAR 6. Counts are approximate; MATCHES are listed compactly.
- The five mismatches that matter most:
  1. G5-1 one-label-per-item.md: the whole rule (`labels.area`, filing refusal, `unsorted`) is not in the code. The build item (yoyodyne-ifd.433.44) is open.
  2. G5-2 unmet-goal-review.md: none of the seven build pieces exist; all seven items are open.
  3. G5-3 architect-loop.md: the windowed drift review and the harness-derived "decided, not landed" and "ruled, not landed" lists, and `yoyo amendment list --json` `unlanded`, do not exist. The configured architect pass is an inbox survey.
  4. G5-4 ownership-and-the-operator.md: the `install` reason for a missing provider program is described as done; the code still returns `credential`.
  5. G5-5 program-manager.md: a pass-on-request instance with no schedule is "stale" after a request is two hours old; the read model says an instance with no schedule is never stale.
  (Also: G5-6 standing-goal lane and G5-7 technical-health spend bounds and served-model check are not built; G5-8 the restart request "acted on by nothing until 413 lands" is stale, it is acted on.)
- Consolidation proposals:
  - Merge management-loop-protocol.md into management-and-supervision.md as one "plan" section, or mark both as "contract built, protocol not built" (see G5-9).
  - Fold needs-design-gate.md, placing-an-item-first.md and one-label-per-item.md into one "how an item is held, ordered and labelled" design; the three share the same selection path and the same build-piece style.
  - In unmet-goal-review.md, replace the sections that restate the three above with links.
  - Split program-manager.md: the technical-health instance (lines 281-339, 16 areas, spend bounds) is a remit and could live in `.yoyodyne/remits/technical-health.md`, which already holds the sixteen areas.
  - Move incident narrative out of ownership-and-the-operator.md and management-and-supervision.md (see each "Could be cut").

---

## placing-an-item-first.md — The Lead Product Manager can place an item first among work of its own priority
Verdict: current (built; the text still reads as a plan)
Size: 7 KB. Last changed: 2026-10-29.

### Findings
MATCHES (checked, no evidence block):
- G5-10 [MATCHES] Only the Lead Product Manager places or removes: `internal/chat/placement.go:11-14`, "Only BacklogOrder reaches here, which only she holds".
- G5-11 [MATCHES] Kept on the item in the tracker, not in harness state: `internal/beads/placement.go:11-19`, metadata key `yoyodyne_placed_first`.
- G5-12 [MATCHES] At most three per priority, refusal names those already placed: `internal/beads/placement.go:26` `MaxPlacedPerPriority = 3`; `internal/chat/placement.go:41-44`.
- G5-13 [MATCHES] Order is priority, then placed (earliest first), then oldest admitted: `internal/backlog/backlog.go:623-655`.
- G5-14 [MATCHES] Placement records who, when, why, priority: `internal/beads/placement.go:30-35`.
- G5-15 [MATCHES] Removal is a second action ("unplace"), refused when nothing is placed: `internal/chat/placement.go:68-77`.
- G5-16 [UNCLEAR] "Each ending is recorded on the item with what ended it" for closing and priority change. `internal/beads/placement.go:30-33` says a placement whose item moved priority "is over whatever the metadata still says", which suggests the ending is read, not written. I did not find a write for the three automatic endings.
- G5-17 [UNCLEAR] The listing line "placed first among priority 0 by the Lead Product Manager at 06:50 PDT..." in every listing. `Placement.Line()` is called (`internal/chat/placement.go:29`); I did not check every surface (briefing, survey, `yoyo status`, dashboard).

### Missing from the document
- Placing an item already placed is refused ("remove it first"), which puts it behind later placements: `internal/chat/placement.go:26-29`. The document says nothing about re-placing.
- The reason is limited to one line and a byte bound (`MaxPlacementReasonBytes`, `internal/beads/placement.go:21-23`).

### Overlap with other documents
- integration-through-a-merge-queue.md links here (inventory). unmet-goal-review.md line 66 names "placing an item first" as a way the Lead Product Manager reorders; agrees.

### Could be cut or moved
- "What this is for" (lines 24) is incident narrative with times and item ids; "Build piece" (59-66) is future tense for built work. Replace with a short "where it lives" line naming `internal/beads/placement.go` and `internal/backlog/backlog.go`.

---

## one-label-per-item.md — At least one label per work item, from a configured list
Verdict: mostly a plan not built
Size: 14 KB. Last changed: 2026-10-18.

### Findings
- G5-1 [NOT BUILT] Every item carries at least one label from a list at `labels.area` in `.yoyodyne/config.yaml`, with exactly one `unsorted: true` entry, enforced at the one filing path.
  - Document: line 61: "The list lives in `.yoyodyne/config.yaml` under `labels.area`"; line 33 names the build item yoyodyne-ifd.433.44.
  - Code: no `labels.area` key and no `unsorted` anywhere in `internal/` (searched Go, YAML, Markdown); `.yoyodyne/config.yaml` has no `labels:` section. `internal/chat/tracker.go:1287-1291` only checks each label is a valid identifier (`beads.ValidateLabel`); it does not require a listed label. `.beads/issues.jsonl`: yoyodyne-ifd.433.44 is open (release notes v0.6.0 and v0.7.0 list it as open).
  - What to change in the document: say plainly it is designed and not built, or hold it until built.
- G5-18 [NOT BUILT] Filing refusal when no listed label; unsorted-needs-a-reason; 3-day age shown on the dashboard; `yoyo init` default list of seven labels; briefing renders the list. Same evidence as G5-1: no code for any of them.
- G5-19 [DIFFERS] A slot preference naming a label not on the list is refused by `yoyo config validate`.
  - Document: line 89: "refuse a slot preference naming a label that is not on the list".
  - Code: `internal/domain/label.go:8-12` and `internal/config/lane.go:98` check only that a label is identifier-shaped (`ValidateLabel`); there is no list to check against.
  - What to change: restate as "slot preferences are checked for shape only until the list exists".
- G5-20 [MATCHES] State label `needs-design` is a closed list in code: `internal/domain/label.go` `NeedsDesignLabel`. Only that one exists, as the document says ("today only `needs-design`").
- G5-21 [MATCHES] A program manager's lane label is added by the harness on creation: `internal/chat/lane.go:139-141` `laneLabels`, and `lane.go:281`. The lane label is never removed by its owner: `lane.go:63-64`.
- G5-22 [UNCLEAR] Child items taking the parent's labels is advised in prompt text only (`internal/chat/role.go:876`: "a child carved out of a labelled parent ordinarily carries its parent's label"); the document's rule (parent's listed labels when filing names none, enforced) is stronger than the code. Classed UNCLEAR for enforcement.

### Missing from the document
- Today's actual label rules: label shape (`^[A-Za-z0-9][A-Za-z0-9._-]*$`, 64 bytes, `internal/domain/label.go`), and the `label` action with add/remove (`internal/chat/role.go:855-856`). A reader would need these until the list is built.

### Overlap with other documents
- needs-design-gate.md line 37 and one-label line 48 both state `needs-design` is a state label; they agree.
- unmet-goal-review.md line 151 (build piece 4) says to read the one-label design first and adds label `raised-by-operator`; one-label lines 39-44 allow only listed and state labels, so the new label is not covered. Disagree by omission.
- Lines 153-155 (open question of labels beyond the list for this repo) overlap with `.yoyodyne/config.yaml` slot preferences.

### Could be cut or moved
- Lines 52-57 "Alternatives rejected" and 31 (72 of 267 items) are history. Section 6 (labelling existing items, 127-133) is a one-time migration plan.

---

## needs-design-gate.md — Holding an item until its design is readable
Verdict: current (built, with one piece unchecked)
Size: 9 KB. Last changed: 2026-11-19.

### Findings
MATCHES:
- G5-23 [MATCHES] Label name `needs-design` held from developer selection like a parked item: `internal/domain/label.go:62-65`; `internal/backlog/backlog.go:317-320` `NeedsDesign`.
- G5-24 [MATCHES] Architect owns the wait in the owner table: `internal/ownership/registry.go:551-553`, remedy "make the design each item waits on readable and remove the needs-design label".
- G5-25 [MATCHES] The 8 KiB design-field rule is told to the architect: `internal/orchestrator/needsdesignwake.go:237`.
- G5-26 [MATCHES] Wake travels the background path, queued while busy; daily backstop: `internal/orchestrator/needsdesignwake.go:9-45`, `NeedsDesignBackstop = 24 * time.Hour` (line 65); task name `NeedsDesignWakeTask`.
- G5-27 [MATCHES] Wait shown with its time in local time and age, P0 first: `internal/readmodel/designwait.go:8-10, 50`.
- G5-28 [MATCHES] P0 reported to the Lead Product Manager once after a day: `internal/orchestrator/needsdesignwait.go:31-33` `NeedsDesignReportAfter = 24 * time.Hour`; `internal/runstate/needsdesignlabelled.go:4, 51`.
- G5-29 [MATCHES] Developer raises needs-design through the run outcome: `internal/runstate/needsdesignlabelled.go`, wake comment "a developer's raise that the harness labels on the run's behalf" (`needsdesignwake.go:21-22`).
- G5-30 [MATCHES] The label can be set by a role via the `label` action (`internal/chat/role.go:855-856`); the architect removes it with the same action.
- G5-31 [UNCLEAR] Architect's two actions on items she does not own (write the design field; remove only this label). `internal/chat/tracker.go:3266` shows the design field is read; I did not find the write path or the scoping to `needs-design`. Looked at `internal/chat/tracker.go`, `role.go`.
- G5-32 [UNCLEAR] "Removal records which of the two forms the design took". Not found.

### Missing from the document
- The wake is driven by the label as the tracker holds it at every pull, so every path that adds the label wakes her, and an item woken for is recorded once (`internal/orchestrator/needsdesignwake.go:12-26`). The document says only "setting the label wakes".
- `needs-design` is reserved: configuration cannot reuse the name (see one-label-per-item.md line 48, planned) and `internal/config/lane.go` carries a reference; the document does not say where the name is validated.

### Overlap with other documents
- unmet-goal-review.md line 126 uses this hold for items resting on an unchecked outside assumption (not built). Agrees.
- ownership-and-the-operator.md does not list the "design wait" as its own kind; it falls under held work (`registry.go:551`). Not stated in either document.

### Could be cut or moved
- "The three practices from the case of the listing that timed out" (84-88) and the case in line 33 are incident history. The "Builds" list (90-99) is done work and can be replaced by pointers.

---

## unmet-goal-review.md — Reviewing unmet goals
Verdict: mostly a plan not built
Size: 19 KB. Last changed: 2026-11-10.

### Findings
- G5-2 [NOT BUILT] A review that starts when developers have had nothing to start for 30 minutes and a goal is unmet, and every day at 09:00 local time.
  - Document: line 41: "The review runs when developer slots have sat with nothing to start for 30 minutes ... and every day at 09:00".
  - Code: no idle-stretch counter, no goal fingerprint, no daily review in `internal/orchestrator/` or `internal/config/`; `.yoyodyne/config.yaml` has no such task. The seven pieces are open: yoyodyne-ifd.428.141 (1), ifd.430.78 (2), ifd.430.79 (3), ifd.433.65 (4), ifd.430.80 (5), ifd.433.66 (6), ifd.429.171 (7). Only the design item yoyodyne-mis9 is closed.
  - What to change: add a one-line status "designed, none of seven pieces built" at the top.
- G5-33 [NOT BUILT] The `raised-by-operator` label and a harness action that records an item the operator raised, from his own words.
  - Document: line 104: "it puts the label `raised-by-operator` on the item".
  - Code: the string `raised-by-operator` appears nowhere in `internal/`; item yoyodyne-ifd.433.65 is open.
- G5-34 [NOT BUILT] "Assumptions about outside systems" section in designs and the hold on build items resting on one (section 7). Nothing in `internal/`; item yoyodyne-ifd.429.171 open.
- G5-35 [NOT BUILT] Status and dashboard standing section for raised items (yoyodyne-ifd.430.80 open) and the 3-week question (yoyodyne-ifd.433.66 open).
- G5-36 [DIFFERS] "The notice that developers have nothing to start ... went only to the operator, 105 times" and is replaced by the review (line 86).
  - Code: the notice still exists as the idle line and the factory-stall kind: `internal/orchestrator/schedule.go:441-461`, `internal/ownership/kind.go` `KindFactoryStall` owner rule `registry.go:636`. Not replaced.
  - What to change: state this is the target, not today.
- G5-37 [UNCLEAR] "The 30 minutes ... is one of the numeric limits the development manager decides under portable agent configuration". No such setting in `internal/config/`.

### Missing from the document
- How this differs from the Lead Product Manager's existing `product-manager-sweep` in `.yoyodyne/config.yaml:276-321`, which already reads across goals, designs and admitted work every 45 minutes and checks closed work against the standing goals. The document's "Each pass looks at its own inbox" is not what the configured sweep prompt says.
- The standing-goal correction path already built (`internal/chat/correction.go`: priority 0, at most three per pass, `corrects` field).

### Overlap with other documents
- Section 3 "what counts as met" vs operating rules/goals: not compared.
- Line 66 names placing-an-item-first, line 126 names needs-design-gate; both agree with those documents.
- Lines 53-60 architect first vs architect-loop.md line 97 "Nothing a pass says ... reorders the backlog": compatible, but the architect pass also being woken with a "review's reason" is not in architect-loop.md.

### Could be cut or moved
- "What this is for" paragraph 1 (25) and the "Alternatives rejected" list (134-144) are incident history. Lines 35-37 ("what it rests on and what was not read") is process narrative.

---

## ownership-and-the-operator.md — Who owns what, and when it reaches the operator
Verdict: partly stale
Size: 38 KB. Last changed: 2026-11-21.

### Findings
- G5-4 [DIFFERS] A missing provider program resolves to the operator with reason `install`, never `credential`.
  - Document: lines 374-379: "the rule for an outage returns the operator with the reason `install` ... It no longer returns `credential`"; closed list line 340 names `install`; table line 227.
  - Code: `internal/ownership/registry.go:505-510` returns `operator(OperatorCredential, "install the provider's program again, or set providers.<provider>.binary ...")` for `ProviderProgramMissing`. `internal/ownership/humanblock.go:24-36, 59-65` lists seven reasons and no `install`.
  - What to change: say `install` is designed (yoyodyne-la2d) and not built, or hold the text until built.
- G5-38 [DIFFERS] The registry's kinds "are the ones the read model already names" and the list (lines 175-180).
  - Code: `internal/ownership/kind.go` holds many more: `config-mismatch`, `service-copies`, `addressed-report`, `product-decision`, `untraced-pass`, `factory-stall`, `tracker-unanswered`, `forge-certificate`, `check-could-not-run`, `capacity-degraded`, `heavy-check-kept`, `lane-human-block`, `empty-slot`, `stale-build`, `unreadable-run`; and `registry.go` rules at lines 296-716.
  - What to change: add them, or state the list is illustrative and point at `kind.go`.
- G5-39 [DIFFERS] Table row for `hold` says the brake hold goes to the development manager and then the Lead Product Manager; it omits the probe step and the release path.
  - Code: `internal/ownership/registry.go:454-490` has `BrakeReleased`, `BrakeProbing`, `BrakeEscalated` with harness owners for released and probing. Minor; not a conflict.
MATCHES:
- G5-40 [MATCHES] Amendment: owner decides; operator only for fundamental intent: `registry.go:296-305`.
- G5-41 [MATCHES] Hand present when the owner role cannot decide: `registry.go:299-302` `withHand(... HandAmendmentDecision ...)`; waiting document hand `registry.go:703-708` `HandDocumentConfirmation`.
- G5-42 [MATCHES] Closed list of seven reasons in code, closed set in `internal/ownership`: `humanblock.go:24-36` (`fundamental-intent`, `credential`, `repository-setting`, `protected-file`, `human-gate`, `own-hold`, `diverged-history`).
- G5-43 [MATCHES] Follow-up owner rule: operator only with a reason; an operator's own follow-up goes to the Lead Product Manager: `registry.go:716-735`.
- G5-44 [MATCHES] Stale build owners by cause: `internal/ownership/stalebuild.go:20-70` (move-waiting, move-due, move-stuck, not-built, uncommitted, unsupervised) matching table line 229.
- G5-45 [MATCHES] Intake question owner `questions: person`: `internal/config/intake.go:112-113`.
- G5-46 [MATCHES] Escalated brake goes to the Lead Product Manager with the operator told: `registry.go:476-478`.
- G5-47 [MATCHES] Default owner is the Lead Product Manager and unowned entries are defects: `registry.go` rules return `Answer{}, false` to classify (e.g. `registry.go:476, 716-731`).
- G5-48 [UNCLEAR] "A test over the module's non-test Go files fails on any other use of the operator mover" (line 501). I saw `internal/ownership/confinement_test.go` exists; did not read it.
- G5-49 [UNCLEAR] Raising attempts: Lead Product Manager grants up to five more, capped at ten (lines 427-435). Not found as a constant in `internal/chat/triage.go`.

### Missing from the document
- Kinds the registry has that a reader would otherwise not find (see G5-38).
- `internal/ownership/ladder.go` and `emptyslot.go`, `passfailure.go`, `stalebuild.go`, `queuemode.go`: rules the document does not mention.
- The `stall` and `outage` rules take a cause value (`ProviderUnreachable`, `ProviderProgramMissing`, login) that the table summarizes in one row.

### Overlap with other documents
- follow-ups.md also describes "a wait is shown once, with one owner" (line 197-208 here). I did not read follow-ups.md for agreement (outside this group).
- stopped-run-recovery.md is cited for the raise limit (line 431); not checked.
- management-and-supervision.md lines 188-190 (escalation only for named reasons) vs lines 458-478 here; they agree on "the Lead Product Manager is top rung".

### Could be cut or moved
- Lines 160-170 "The problem this ends", 354-356 history of renamed reasons, and 370-389 (the `install` case) are incident history and a design-in-the-middle for something not built. Lines 545-573 "Build" lists are done work.

---

## architect-loop.md — The architect loop: a windowed pass over what landed
Verdict: mostly a plan not built (pass machinery built; the review itself not)
Size: 11 KB. Last changed: 2026-11-19.

### Findings
- G5-3 [NOT BUILT] The pass reads only what landed since the previous pass (a cursor over run records and revision logs) and starts with "decided, not landed" and "ruled, not landed" lists supplied by the harness, also in `yoyo amendment list --json` under `unlanded`.
  - Document: lines 77, 84-89: "carried in `yoyo amendment list --json` under `unlanded`".
  - Code: `internal/cli/amendment.go:39-44` JSON has only `proposals` and `decided`; no `unlanded` anywhere in `internal/`. `.yoyodyne/config.yaml:331-360` `architect-pass` is a 45-minute inbox survey ("Survey the work items carried in your conversation ... and the undecided proposals"); no windowed drift review. The architect item ifd.339 is closed (design only).
  - What to change: say the pass today is the inbox survey and the review is not built.
- G5-50 [MATCHES] A pass is a recurring task with a durable record, in its own conversation, one at a time per conversation, several at once: `internal/orchestrator/recurringfirings.go:5, 478` (one goroutine per firing).
- G5-51 [MATCHES] A pass starts bounded to a third of the provider's window and a larger start is refused: `internal/orchestrator/recurring.go:1505`; `internal/chat/passcontext.go:21, 99`.
- G5-52 [MATCHES] A pass compacted twice is recorded partial: `internal/orchestrator/recurring.go:310`; test `recurring_test.go:333`.
- G5-53 [MATCHES] Invariant changes via typed document write; `yoyo invariant retire` exists: `internal/cli/invariant.go:45`.
- G5-54 [UNCLEAR] "Each pass starts a fresh provider session from the role contract, persona, remit, memory, previous pass's account, pass message, product intent whole" (line 73). Not verified beyond the size bound.
- G5-55 [UNCLEAR] The bound on how many conversations' passes run at once ("a bound the harness sets"): no setting found in `internal/config/`; item yoyodyne-ifd.428.75.1 (reconcile scheduled-pass concurrency) is open.
- G5-56 [DIFFERS] "Nothing a pass says ... reorders the backlog" (line 97) and "The pass admits nothing and directs nothing" (79), but the configured pass prompt says "Take work by priority first, then age" for the items carried in her conversation, and she rules on work items she carries.
  - Code: `.yoyodyne/config.yaml:341-352`.
  - What to change: separate "the drift review" (reports only) from "the survey of carried work".

### Missing from the document
- The configured pass: 45 minutes, `max_turns: 8`, prompt about carried items and proposals (`.yoyodyne/config.yaml:331-360`). The document describes only the unbuilt drift review.
- The needs-design wake and backstop (`internal/orchestrator/needsdesignwake.go`) is a second architect trigger, not mentioned.

### Overlap with other documents
- configurable-workflows.md revision at its line 64 and program-manager.md line 52 state the same concurrency sentence; all three agree.
- management-and-supervision.md / ownership-and-the-operator.md: the architect "decides proposed changes ... and tells the operator afterwards" (here line 65, ownership 246-252); agree.

### Could be cut or moved
- Line 55 ("nine such links cost weeks in 2026-09") is incident history. Lines 61-67 duplicate "Who decides a proposed change" in ownership-and-the-operator.md 240-276.

---

## management-loop-protocol.md — The management-loop protocol
Verdict: mostly a plan not built
Size: 9 KB. Last changed: 2026-11-19.

### Findings
- G5-9 [NOT BUILT] Three request kinds (consult, clarify, escalate) sent as persisted typed requests; three readiness judgments with four architecture dispositions (clear, design-needed, cross-cutting, investigation-needed); five named execution profiles chosen by the development manager.
  - Document: lines 50-58, 72: "five named profiles — mechanical-low-risk, standard, high-judgment, architecture-sensitive, specialist".
  - Code: none of those names exist in `internal/config` or `internal/domain`; the only hits for the profile names are `internal/terms/inventory/inventory.go` (a word list). `internal/readiness/readiness.go:1-40` is a different thing (a pre-dispatch check of what one item points at in the tree; "Nothing here refuses anything"). No management-loop dispatcher in `internal/orchestrator` or `internal/supervise` (grep found only the terms inventory). The existing exchange channel is `internal/exchange` with a round cap (`internal/config/config.go:892-896` `MaxRounds`). The routing condition item yoyodyne-ifd.83 is closed but its content is cost by phase, not routing.
  - What to change: mark the whole document as "specification for a later slice"; it already says "advisory first" and "enforced only after evidence exists".
- G5-57 [MATCHES] Exchange rounds are limited and a thread that hits the limit is reported: `internal/config/config.go:892-896`.
- G5-58 [MATCHES] Outcome summaries by role (line 64): not verified; classed UNCLEAR.
- G5-59 [UNCLEAR] "Material divergence reopens the applicable readiness judgment". No readiness-judgment records exist, so nothing to reopen.

### Missing from the document
- That the first slice that exists (yoyodyne-ifd.142, closed, "advisory management-loop slice: restart, concurrency, and staleness proven") built only exchange, side threads and restart behavior; the document does not say which parts are real.

### Overlap with other documents
- Lines 46 and 50 restate management-and-supervision.md lines 110-114; agree. Line 68 ("refines the conflict-handling section") vs management-and-supervision.md 187-189; agree.
- Role boundaries (line 76) restate the authority tables in `internal/chat/role.go`; no conflict found.

### Could be cut or moved
- Line 78-79 "demonstration bar" is a test plan for an unbuilt loop. Lines 46 and 50 repeat the contract; link instead.

---

## management-and-supervision.md — Management and supervision: the typed request contract and process residency
Verdict: partly stale
Size: 31 KB. Last changed: 2026-11-19.

### Findings
MATCHES:
- G5-60 [MATCHES] Exchange evidence kinds amendment, work item, document, run, report, turn: `internal/exchange/evidence.go:47-52`.
- G5-61 [MATCHES] Evidence bounds 8 KiB a record, 32 KiB a round: `internal/exchange/evidence.go:73-74`.
- G5-62 [MATCHES] Build move interval: default 3 hours, refused under 1 hour or over 24 hours: `internal/config/config.go:790-792`, `config.go:347-350`; setting `execution.build_move_interval` (`resolve.go:537`).
- G5-63 [MATCHES] Side-threads knob `queue` (default) or `side-threads`: `internal/config/sidethreads.go:38-45`; a side thread's turn limit `side_thread_turn` (`timelimits.go:40, 94`).
- G5-64 [MATCHES] Supervisor with children and restart with backoff, left degraded after repeated failure: `internal/supervise/supervise.go:6-14, 38-55` (`MaxRapidFailures = 5`, backoff to 30 s).
- G5-65 [MATCHES] Triage and management-loop dispatchers are inside the watch loop, not children (line 220): consistent with the supervise children list; no separate dispatcher process found.
- G5-66 [MATCHES] Operator stopping verbs; supervisor executes a program manager's restart request in its periodic pass: `internal/chat/restart.go:3-12`; `internal/cli/product.go:776-800` passes `Requests` to `maintain.Pass`.
- G5-67 [DIFFERS] "The supervisor takes one request from a role ... staleness is a non-model derivation the supervisor reads once yoyodyne-ifd.413 lands" (line 222).
  - Code/records: yoyodyne-ifd.413 is closed and the supervisor is built (`internal/supervise/`); the restart request is acted on (`internal/cli/product.go:776-800`). Whether the supervisor reads staleness is not found in `internal/supervise` or `internal/maintain`.
  - What to change: drop "once yoyodyne-ifd.413 lands"; say what the supervisor reads today.
- G5-68 [NOT BUILT] "Persisted typed requests ... the harness acquires a lease and invokes the target role itself", "an inbox transition makes it eligible" (line 110), conversation lease per agent as reattachment primitive (169).
  - Code: no inbox or request-lease store; `internal/exchange` delivers rounds, and no conversation-lease type was found under `internal/chat` (the "lease" word appears only in `internal/backlogrepair`, `landing`, `notify`, `queuemode`, `exchange`). Not conclusive; classed NOT BUILT for the request inbox, UNCLEAR for the conversation lease.
  - What to change: mark inbox and lease as the slice that is built only in part (yoyodyne-ifd.142).
- G5-69 [NOT BUILT] Advisory readiness records (product, architecture, delivery) visible to the operator (lines 183-185). No such record type exists (see G5-9).
- G5-70 [UNCLEAR] "The supervising process holds the Slack tokens and constructs every child's environment by allowlist": `slack.WithoutSecrets` is passed to maintain (`internal/cli/product.go:797`); did not trace the child environment construction.

### Missing from the document
- `internal/maintain` (the supervisor's periodic pass) and what it does besides restarts.
- Config-mismatch and service-copies checks in `internal/supervise/copies.go` and ownership kinds `config-mismatch`, `service-copies`.
- Which of "first slice" (lines 230-232) was built and which was deferred.

### Overlap with other documents
- management-loop-protocol.md (lines 46, 50) restates the request kinds; agrees. program-manager.md line 239 (restart: five in two minutes, one per part) vs this doc 220-222: agree.
- configurable-workflows.md mentions the same watch-session move (not read).

### Could be cut or moved
- Lines 197-218: the November 18 incident narrative (five moves, 58 minutes) and the "Build pieces" and "A reviewer can check" lists for built work. Lines 116-161 "Evidence an exchange carries" is a full sub-design with 11 verification cases and could be its own document.

---

## program-manager.md — The program manager: one role type, one lane per instance, requests batched
Verdict: partly stale
Size: 61 KB. Last changed: 2026-11-21.

### Findings
- G5-5 [DIFFERS] An instance with no schedule is stale only when a request has gone two hours without a completed pass.
  - Document: line 260: "it is stale only when a request has gone two hours from being made without a completed pass"; also line 367 (ruling 10); cjw build list line 339 "the stale rule for an instance with no schedule".
  - Code: `internal/readmodel/programmanagers.go:590-592` `if instance.Every <= 0 || !readable { return false, "" }`, and `programmanagers.go:564` uses `staleness` alone. No request-age rule.
  - What to change: state that a pass-on-request instance is never stale today, or build the rule.
- G5-6 [NOT BUILT] A lane may be a standing goal (`goal:` form), with an audit record `audit.jsonl`, twenty closed items per turn.
  - Document: lines 139, 174-190: "`goal:` followed by one goal from the standing set".
  - Code: `internal/config/lane.go:98` validates the lane as a label with `domain.ValidateLabel` (the colon fails the pattern at `internal/domain/label.go:21`); no `audit.jsonl` path anywhere in `internal/`. The design item yoyodyne-ifd.430.13.16 is closed (design only) and I found no build item. What does exist is a correction path for the Lead Product Manager's sweep (`internal/chat/correction.go`), and the lane prompt mentions "corrects" for a goal lane (`internal/chat/lane.go:287`): priority 0, at most three per pass, which differs from the document's attribution-based wording.
  - What to change: mark section "A lane that is a standing goal" as not built, and reconcile its limits with `correction.go`.
- G5-7 [NOT BUILT] Technical-health spend bounds and served-model verification: sixty reads per pass, five million tokens a day (200,000 where unreported), no start while a ready P0 waits on the account, and a turn whose served model is not `gpt-6-astra` recorded as a failed pass (lines 285, 321-325).
  - Code: no such constants in `internal/chat`, `internal/runstate`, `internal/config`, `internal/orchestrator`. `internal/backend/effort.go:61` knows `gpt-6-astra` levels only. Work item yoyodyne-ifd.430.13.20 ("Deliver technical-health-pm with verified Astra-only high effort...") is open.
  - What to change: say the instance is configured and the checks are not built.
- G5-8 [DIFFERS] "Until yoyodyne-ifd.413 lands, a request is recorded ... and acted on by nothing" (lines 239, 361, 263).
  - Code: `internal/cli/product.go:776-800` hands the restart requests to `maintain.Pass`; `internal/chat/restart.go:3-12` says the supervisor's pass acts on them; yoyodyne-ifd.413 is closed.
  - What to change: remove the "until 413 lands" clauses.
MATCHES:
- G5-71 [MATCHES] Role `program-manager` is a shipped role: `internal/domain/types.go:32, 47`.
- G5-72 [MATCHES] Instance keys lane, remit, triggers; lane label enforced on creation and never removed: `internal/config/config.go:1187-1191`, `internal/config/lane.go:85-110`, `internal/chat/lane.go:139-141, 281-286`.
- G5-73 [MATCHES] Trigger events `landings`, `admissions`, `stoppages`: `internal/config/lane.go:39-43`; two-minute settle window `PassSettleWindow = 2 * time.Minute`: `internal/orchestrator/programmanagerpass.go:101`.
- G5-74 [MATCHES] Pass request: `requests.jsonl`, at most ten focus paths, `yoyo agent pass --focus`: `internal/runstate/passrequest.go:50-52, 72`; `internal/cli/agent.go:169`; `internal/cli/agentpass_test.go:26`; block `yoyodyne-pass-request` `internal/chat/passrequest.go:25`.
- G5-75 [MATCHES] Lane report 16 KiB, history of fifty, block `yoyodyne-lane-report`: `internal/runstate/lanereport.go:52, 56`; `internal/chat/lanereport.go:37`.
- G5-76 [MATCHES] Stop-cause record `stop-causes.jsonl`, default recurrence three: `internal/runstate/stopcause.go:56, 53`.
- G5-77 [MATCHES] Stale for scheduled instances is twice the schedule: `internal/readmodel/programmanagers.go:590-596`.
- G5-78 [MATCHES] `repository.search` bounds 200 bytes a line, 200 matches, 32 KiB: `internal/repositoryread/search.go:33-38`.
- G5-79 [MATCHES] `technical-health-pm` is Codex `gpt-6-astra` effort `high`, lane `technical-health`, no triggers; `factory-flow-pm` has lane `factory-flow`: `.yoyodyne/config.yaml:571-608`. The remit lists sixteen areas: `.yoyodyne/remits/technical-health.md` (16 numbered lines).
- G5-80 [MATCHES] Dashboard section and route `/api/program-managers/<agent>`, `standing.program_managers`: `internal/cli/dashboard.go:394, 790`; `internal/readmodel/programmanagers.go:7`.
- G5-81 [UNCLEAR] Triggers cursor for `admissions` reads `.beads/issues.jsonl` by creation time (line 228). Not traced.
- G5-82 [UNCLEAR] "The supervisor's bound of five restarts in two minutes" (line 239): `internal/supervise/supervise.go:46-50` holds `MaxRapidFailures = 5` and `StableAfter = 2 * time.Minute`; close but the document's wording ("five restarts in two minutes") is not the same rule (five rapid failures in a row, a start counting as stable after two minutes).
- G5-83 [UNCLEAR] Standing check of `factory-flow-pm` trigger: the config runs `every: 45m`, `on: [admissions, stoppages]` (`.yoyodyne/config.yaml:582-588`); the document never states the instance's schedule (it describes the design only).

### Missing from the document
- The live instance settings: 45-minute schedule, `time_limits` turn 45m message 60m (`.yoyodyne/config.yaml:574-588`), and the reasoning about prompt cache.
- Missed pass is recorded and shown (`ProgramManagerMiss`, `internal/readmodel/programmanagers.go:560-563`); the document says nothing about "missed pass" status (items yoyodyne-ifd.430.13.15 closed, .12 open).
- Corrections admitted at priority 0, at most three per pass: `internal/chat/correction.go`.

### Overlap with other documents
- program-manager.md line 52 and configurable-workflows.md line 64 and architect-loop.md line 59 all state the concurrency sentence; they agree.
- Lines 149 and 319 depend on tool-interface.md for `log.read` and `repository.search`; line 319 says tool-interface "lists it when that design is next revised"; the code has it built.
- Lines 224 and 233-235 (passes and requests) vs management-and-supervision.md 165: agree.
- Lines 271-279 (boundary with the development manager's sweep) vs ownership-and-the-operator.md 230 "stoppage: the development manager": agree.

### Could be cut or moved
- Lines 345-367 ("Deferred" and "The ten questions, ruled") restate sections above; remove the ten-questions list.
- Lines 281-339, the technical-health instance (16 areas, spend bounds, activation steps), is about one configured instance and belongs in its remit and its work item.
- Lines 202-220 "Repeated stop causes" is a sub-design with checkable examples; it could be its own document.
- Lines 131 and 289 repeat dates and operator decisions.
