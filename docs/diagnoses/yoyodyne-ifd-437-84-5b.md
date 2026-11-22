# yoyodyne-ifd.437.84: second reading of the design documents against the code, group 5: roles and management

A second, adversarial reading of the report `yoyodyne-ifd-437-84-5.md` beside this file. It re-read every finding there that differs or is not built against the code, tried to settle the unclear ones, checked a sample of the matches, and looked for what the first reading missed. **Where the two files disagree, this one stands.**

- **Read on:** 2026-11-21, in the evening, between 18:33 and 18:43 PST, with main at `848d86190`. The code had moved since the first reading; each finding is judged against the code as it was at this reading.
- **Read by:** a read-only reader on Opus 5.5 in the operator's assistant's session. It read code and ran nothing, tests included.
- **Checked by hand:** nothing in this file was checked again by hand.
- **How to use it:** evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Second-reading classes:** `CONFIRMED`, the first finding is right as written; `CORRECTED`, its gist is right and a detail is wrong; `WRONG`, the design and the code agree or the finding misread one of them; a class in the first reading's own words where an unclear finding was settled.

Read at main `848d86190` on 2026-11-21. Read only: Grep and Read over the repository. Nothing was built, tested or run.

## Summary
- Findings re-read: 21 (every DIFFERS and NOT BUILT). CONFIRMED 15, CORRECTED 5, WRONG 1, CANNOT TELL 0.
- Unclear findings settled: 15 of 16. G5-83 is not a claim the design makes, so there was nothing to settle.
- Verdicts changed: needs-design-gate.md, from "current" to "partly built". The architect's half of the gate is not built, and she cannot take the label off (see G5-30 and G5-31).
- The findings the architect should not act on as written:
  - G5-56: WRONG. "Take work by priority first, then age" in the configured pass orders the architect's own carried items. It does not reorder the backlog, and the architect holds no ordering capability. The real gap is G5-3.
  - G5-30 (classed MATCHES): WRONG. The architect cannot use the `label` action. It needs `work-item.mutate`, and her bundle has no tracker write at all.
  - G5-38: corrected. Three kinds the document lists have no kind in code (escalation, diverged target, intake question), and several names in the finding are wrong.
  - G5-9: corrected. Consult and clarify are already carried by `internal/exchange`, as the document itself says. What is not built is the typed request kinds beyond that, readiness, outcome summaries and profiles.
  - G5-68: corrected. The conversation lease exists (`internal/runstate/conversation.go:1149-1200`). Only the request inbox is not built.
  - G5-36: corrected. Today an idle session with ready work belongs to the harness, not the operator (`internal/ownership/registry.go:535-538`).
  - G5-65 (classed MATCHES): half wrong. No management-loop dispatcher exists anywhere.
  - "Overlap" note under ownership-and-the-operator.md, that management-and-supervision.md lines 188-190 agree with it: WRONG. They contradict it (see Contradictions).
- The most important things the first reader missed:
  - The architect holds no tracker write (`internal/rolecapability/bundles.go:105-108`). Even so, her needs-design wake prompt says "Only you take the label off" (`internal/orchestrator/needsdesignwake.go:237`), and the owner registry gives her that remedy (`internal/ownership/registry.go:551-553`). Meanwhile the Lead Product Manager and the development manager can remove `needs-design` with the plain `label` action, because nothing in `internal/chat` refers to it.
  - Who escalates to the operator: management-and-supervision.md:189 lets the Lead Product Manager escalate for "priority, material risk, budget". ownership-and-the-operator.md's closed list has seven reasons, none of those, and stopped-run-recovery.md:291 rules spending out explicitly.
  - management-and-supervision.md:220 sends a degraded service to the operator by direct message. ownership-and-the-operator.md:225 and the code (`registry.go:420-422`) make the harness its owner.
  - When an exchange runs out of rounds, code files a warning report, which goes to the Lead Product Manager's pile. The role prompts and the config comment tell roles "the operator has been told" (`internal/exchange/contract.go:61`, `internal/exchange/conduct.go:148`, `internal/config/config.go:892-896`).
  - The supervisor refuses a program manager's restart request for the scheduler (`internal/supervise/host.go:161-163`). program-manager.md:239 says any declared part.

## placing-an-item-first.md
Verdict: agree (current; built).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G5-16 | UNCLEAR | MATCHES | Closing: `internal/beads/client.go:2051-2070` (`endPlacementOnClose` appends "Placement first among priority N ended: the item closed"). Priority change: `internal/chat/tracker.go:2367-2374` (`placementEndedByPriority` note, same write). |
| G5-17 | UNCLEAR | MATCHES | `Placement.Line()` (`internal/beads/placement.go:115-128`, local zone, named) is rendered in the survey and read (`internal/chat/tracker.go:2949, 3241`), the briefings (`internal/contextbundle/product.go:1738`, `conversationwork.go:28`), the backlog listing (`internal/backlog/backlog.go:813`), and the read model the dashboard and status use (`internal/readmodel/backlogitems.go:269`). |

Sampled matches (G5-12, G5-13, G5-15): all hold.

### Missed by the first reading
- Nothing material.

### Contradictions with other designs
- None found.

## one-label-per-item.md
Verdict: agree (mostly a plan not built).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G5-1 | NOT BUILT | CONFIRMED | No `labels.area` or `unsorted` in `internal/` Go. `.yoyodyne/config.yaml` has no `labels:`. yoyodyne-ifd.433.44 is open. |
| G5-18 | NOT BUILT | CONFIRMED | Same evidence. |
| G5-19 | DIFFERS | CORRECTED | Should be classed NOT BUILT, as part of G5-1: there is no list to check against. The cite is wrong: `internal/config/lane.go:98` validates a program manager's lane. Slot preferences are checked in `internal/config/config.go:1752-1785` (`developerSlotProblems`, shape via `slot.Problems()`). |
| G5-22 | UNCLEAR | NOT BUILT | Prompt advice only (`internal/chat/role.go:876`). No code copies a parent's labels on create. |

### Missed by the first reading
- The first reader's note under needs-design-gate.md says `internal/config/lane.go` reserves the `needs-design` name. It reserves the recurring task name `architect-needs-design` instead (`lane.go:193-213`). A lane or slot named `needs-design` passes validation today, which one-label line 174 says must be refused.

### Contradictions with other designs
- unmet-goal-review.md:104 adds the label `raised-by-operator`, which one-label lines 39-44 do not allow (the first reader noted this).

## needs-design-gate.md
Verdict: should be **partly built**, not current. The hold, the setting, the wake and the wait report are built. The architect's two actions are not: she cannot write the design field or remove the label, and other roles can remove it.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G5-30 | MATCHES | WRONG | `label` needs `capability.WorkItemMutate` (`internal/chat/tracker.go:339`). The architect's bundle has no tracker write: "reads the tracker and writes none of it" (`internal/rolecapability/bundles.go:105-108`). The Lead Product Manager and the development manager hold `WorkItemMutate` and can remove `needs-design`. No code in `internal/chat` refers to the label, so design line 52, "only the architect removes the label", is not enforced. |
| G5-31 | UNCLEAR | NOT BUILT | No tracker action takes a design field (`TrackerAction`, `internal/chat/tracker.go:441-505`). The architect has no tracker write (above). Build piece 3 (design line 96) is not built. |
| G5-32 | UNCLEAR | NOT BUILT | No removal path exists, so nothing records the form. |
| G5-24 | MATCHES | MATCHES, with a caveat | The registry names the architect (`registry.go:551-553`), but she holds no capability for the remedy it gives her. |

### Missed by the first reading
- Design line 52 against code: other roles can remove the label and the architect cannot (G5-30).
- The wake prompt tells the architect "Only you take the label off" (`internal/orchestrator/needsdesignwake.go:237`), which she cannot do.

### Contradictions with other designs
- ownership-and-the-operator.md's table: "held work: the owner of the group it is held in" gives this wait to the architect. Under its own "hand" rule, a remedy the role cannot perform should carry a hand, and none is listed (lines 411-430).

## unmet-goal-review.md
Verdict: agree (mostly a plan not built).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G5-2 | NOT BUILT | CONFIRMED | All seven items are open in `.beads/issues.jsonl` (428.141, 430.78, 430.79, 433.65, 430.80, 433.66, 429.171). |
| G5-33 | NOT BUILT | CONFIRMED | No `raised-by-operator` in `internal/`. |
| G5-34 | NOT BUILT | CONFIRMED | Item 429.171 is open. |
| G5-35 | NOT BUILT | CONFIRMED | Items 430.80 and 433.66 are open. |
| G5-36 | DIFFERS | CORRECTED | The "105 times" is history (line 25). The claim to correct is line 86, "no longer goes to the operator first", written as current. Today the notice is not operator-owned anyway: an idle session with ready work is the harness's (`internal/ownership/registry.go:535-538`), and `factory-stall` is the harness's (`registry.go:636-638`). What is missing is only the review that would start. |
| G5-37 | UNCLEAR | NOT BUILT | No such setting in `internal/config`. It is part of G5-2. |

### Missed by the first reading
- Nothing beyond the first reader's notes.

### Contradictions with other designs
- Section 4 says the operator hears only closed-list steps, which agrees with ownership-and-the-operator.md. management-and-supervision.md:189 lets him be asked about priority and budget (see below).

## ownership-and-the-operator.md
Verdict: agree (partly stale).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G5-4 | DIFFERS | CONFIRMED | `internal/ownership/registry.go:505-510` returns `OperatorCredential` for `ProviderProgramMissing`. `humanblock.go:20-49` has seven reasons, none of them `install`. |
| G5-38 | DIFFERS | CORRECTED | The gist holds: code has many more kinds (`kind.go:15-161`, `emptyslot.go:13`, `stalebuild.go:12`). The other direction also differs: the document lists escalation, diverged target and intake question as kinds, and code has no kind for any of them (escalation is an operator-action origin, diverged target a stall reason). Names in the finding are wrong: the waiting document is `document-approval`, the lane block is `blocked-on-a-human`, the empty slot is `empty-developer-slot`, and `capacity-blocked` was left out. The rule map is `registry.go:186-220`. |
| G5-39 | DIFFERS | CONFIRMED | `registry.go:454-490`. |
| G5-48 | UNCLEAR | MATCHES | `internal/ownership/confinement_test.go:20` `TestTheOperatorsMoverIsNamedOnlyInTheOwnershipPackage` walks the module. |
| G5-49 | UNCLEAR | MATCHES | `runstate.MaxLeadCapGrants` (`internal/orchestrator/schedule.go:6534`). The words "up to five more and never past ten" are in `internal/triage/triage.go:2737` and `internal/ownership/hand.go:133`. |

Sampled matches (G5-40, G5-43, G5-44, G5-46): hold.

### Missed by the first reading
- Table line 225 ("degraded service, failing task") against `registry.go:420-422`: a degraded service is always the harness's, with the remedy "yoyo stop and yoyo start", a command only a person runs.
- Table line 230 ("stoppage: the development manager") against `registry.go:563-571`: a resumable run, or one awaiting carry-out, is the harness's, and a document run belongs to the role that owns the document.

### Contradictions with other designs
- management-and-supervision.md:189 lets the Lead Product Manager escalate to the operator for "approved intent, priority, material risk, budget, or an authority boundary". This document (lines 331-369 and 474-479) allows only the seven closed reasons, and says a missing capability "is never an ask of the operator". stopped-run-recovery.md:291 agrees with this document that spending is not on the list. The first reader recorded these as agreeing.
- management-and-supervision.md:220 sends a repeatedly failing child to the operator by direct message. This document's line 225 gives degraded services to the harness or the supervisor, and the operator only for a credential.
- follow-ups.md (lines 72-73, 159-166, 188) agrees with this document on wait ownership: the operator owns a wait only with a closed reason, and his own follow-ups go to the Lead Product Manager (`registry.go:716-731`). No contradiction.
- stopped-run-recovery.md:282-285 agrees with lines 427-435 on raises.

## architect-loop.md
Verdict: agree (pass machinery built; the review itself not).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G5-3 | NOT BUILT | CONFIRMED | `internal/cli/amendment.go:37-44` has only `proposals`, `decided` and `error`. `.yoyodyne/config.yaml:331-363` is a survey of carried items and proposals. |
| G5-56 | DIFFERS | WRONG | "Take work by priority first, then age" (`config.yaml:354`) orders which of her own carried items she rules on first. Design line 97 is about the backlog, and the architect holds no `BacklogOrder` or admit capability (`bundles.go:104-134`), so "admits nothing and directs nothing" holds. The real mismatch is line 55, "looks for exactly that and nothing else", against a pass that surveys carried work, which is G5-3. |
| G5-54 | UNCLEAR | MATCHES | `internal/chat/passcontext.go:3-26` lists the contract, persona, remit, memories, briefing with intent whole, previous account and message. Nothing gives way to the bound. |
| G5-55 | UNCLEAR | MATCHES | `orchestrator.MaxConcurrentFirings = 4` (`internal/orchestrator/recurringfirings.go:56`) is a harness constant, as the design says. |

### Missed by the first reading
- A second architect trigger (the needs-design wake) runs under the reserved task name `architect-needs-design` (`internal/config/lane.go:197`). The first reader noted it.

### Contradictions with other designs
- None beyond the first reader's notes.

## management-loop-protocol.md
Verdict: agree (mostly a plan not built).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G5-9 | NOT BUILT | CORRECTED | Line 46 itself says consult and clarify go over the existing ask channel, and that is built (`internal/exchange`). Not built: escalate as a typed request kind, readiness judgments and dispositions, and the five profiles (no hits outside the terms inventory). The only model routing is by label (`internal/config/developermodels.go:71`). |
| G5-57 | MATCHES | MATCHES, with a caveat | The mechanism is a warning report (`internal/exchange/conduct.go:574-602`), which is the Lead Product Manager's pile (`registry.go:327-328`), as line 46 says. But code tells the roles the operator is told: `exchange/contract.go:61`, `conduct.go:148`, `chat/exchange.go:331`, `config/config.go:892-896`. |
| G5-58 | MATCHES/UNCLEAR | NOT BUILT | No outcome-summary code in `internal/`. |
| G5-59 | UNCLEAR | NOT BUILT | No readiness-judgment records exist. |

### Missed by the first reading
- The wording of the exchange-exhaustion message (G5-57).

### Contradictions with other designs
- Line 50 defines "escalate" as asking the Lead Product Manager to obtain an operator decision "because delegated authority or policy is insufficient". ownership-and-the-operator.md:474-479 says a missing capability is a defect she admits, "never an ask of the operator", and that only closed-list reasons reach him.

## management-and-supervision.md
Verdict: agree (partly stale).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G5-67 | DIFFERS | CONFIRMED | ifd.413 is closed. `internal/maintain/maintain.go:1185-1210` answers restart requests. Nothing in maintain or supervise reads program-manager staleness. |
| G5-68 | NOT BUILT | CORRECTED | The request inbox is not built. The conversation lease is built: a per-agent lock with a holder stamp and queueing (`internal/runstate/conversation.go:1149-1200`). |
| G5-69 | NOT BUILT | CONFIRMED | `internal/readiness/readiness.go:1-40` is a different thing. |
| G5-70 | UNCLEAR | DIFFERS | Line 193 says the supervisor holds the Slack tokens and builds child environments by allowlist. In code the supervisor never has a token (`internal/cli/product.go:25-28`): the sink reads them from the keychain, and every other child gets the inherited environment with the three Slack variables removed (`internal/slack/supervise.go:285-301`), which is a denylist. |
| G5-65 | MATCHES | half WRONG | The triage dispatcher is a step of the watch loop, but no management-loop dispatcher exists anywhere (see G5-9). Line 220 states one as present. |

### Missed by the first reading
- Line 193 against the code (G5-70).
- Line 220's direct message to the operator for a degraded child disagrees with the code's harness owner (`registry.go:420-422`) as well as with ownership-and-the-operator.md.

### Contradictions with other designs
- Line 189 against ownership-and-the-operator.md's closed list and stopped-run-recovery.md:291 (above).
- Line 220 against ownership-and-the-operator.md:225 (above).

## program-manager.md
Verdict: agree (partly stale).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G5-5 | DIFFERS | CONFIRMED | `internal/readmodel/programmanagers.go:590-592`. |
| G5-6 | NOT BUILT | CONFIRMED | A colon fails `labelPattern` (`internal/domain/label.go:25`). There is no `audit.jsonl`. The prompt text for a goal lane exists (`internal/chat/lane.go:287`) but nothing can configure one. |
| G5-7 | NOT BUILT | CONFIRMED | Detail: the served model is recorded on the pass (`internal/cli/recurring.go:858-865`), but nothing fails a pass on it. |
| G5-8 | DIFFERS | CONFIRMED | Lines 239, 263 and 361. `internal/cli/product.go:776-800` hands requests to `maintain.Pass`. |
| G5-81 | UNCLEAR | MATCHES | Admissions read the tracker stream (`internal/orchestrator/programmanagerpass.go:24-40, 170-176`), with a lateness reach for the export. |
| G5-82 | UNCLEAR | DIFFERS | `internal/supervise/supervise.go:46-53`: five rapid failures in a row, a rapid failure being a death within two minutes of a start, with backoff doubling to 30 s. That is not "five restarts in two minutes" (line 239). |
| G5-83 | UNCLEAR | no claim | The design does not state the instance's schedule. `config.yaml:582-588` has `every: 45m`, `on: [admissions, stoppages]`. |

Sampled matches (G5-74, G5-75, G5-76, G5-78, G5-73): all hold at the cited constants.

### Missed by the first reading
- Line 239 says the supervisor carries out a restart request for any part the `services` section declares, as it would a death. The scheduler's request is refused instead: "left running, because stopping it cancels the runs it hosts" (`internal/supervise/host.go:161-163`). A degraded child is also refused (`host.go:156-157`).
- Line 263 says the supervisor "restarts the scheduler if the scheduler is what died" after reading staleness. Nothing reads staleness (G5-67).

### Contradictions with other designs
- Line 239 against management-and-supervision.md:222. Both say "once 413 lands", and both are stale in the same way, so they agree with each other and not with the code.
