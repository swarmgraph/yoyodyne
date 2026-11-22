# yoyodyne-ifd.437.84: design documents against the code, group 6: execution, providers and cost

One of nine reports comparing the design documents in `docs/designs/` with the code, made at the operator's request of 2026-11-21 for a review of every design against the current code. This one covers group 6, execution, providers and cost.

- **Read on:** 2026-11-21, between 15:40 and 15:48 PST. Main moved from `2447eeaab` to `fe2db3d37` at 15:41, a minute into the reading; that change was 22 files, mostly the team feedback repair code in `internal/runstate`.
- **Read by:** a read-only reader in the operator's assistant's session, on Sonnet 5.5. It read code and ran nothing, tests included.
- **Checked by hand:** the operator's assistant checked three findings against the code. G6-13, G6-20 with G6-21, and G6-24 held. Every other finding is unchecked.
- **How to use it:** this is evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Classes:** `MATCHES`, the code does what the design says; `DIFFERS`, the code does something else; `NOT BUILT`, the design calls it future or the code is absent; `UNCLEAR`, not established.

## Summary
- Documents: 7. Claims checked: 62. MATCHES 30, DIFFERS 10, NOT BUILT 17, UNCLEAR 5.
- The five mismatches that matter most:
  - G6-1: the "authorized four-slot mapping" (slot 1 on Codex gpt-6.1-sol, reviewer alternates) is not this project's configuration. `.yoyodyne/config.yaml` has three slots, slot 2 on Sonnet with fallback off, no reviewer alternate.
  - G6-2: the routing design still says the slot-pair fields and routing records are future work. They are built in `internal/config` and `internal/runstate/routing.go`.
  - G6-13: the Codex home is described as living under `~/.yoyodyne/accounts/<account>/codex-home/`. The code keeps it inside the account's own provider home (`.yoyodyne-codex-home`).
  - G6-20 / G6-21: "Reading published prices" is entirely unbuilt: no `sources.json`, no reader, `rates.json` has `"entries": []`.
  - G6-24: only piece 2 (and piece 1 in part) of "Reducing what the factory costs" is built. Pieces 3 to 10 have no setting, record or code.
  - G6-30: "What each invocation was sent" is entirely unbuilt (no stored parts, no record, no command).
- Consolidation proposals:
  - Split `claude-execution-and-account-routing.md` (43 KB): move the whole "Durable transition", "Restart", "Legacy" and "Acceptance criteria" blocks into a short "what is built" form, and drop the history narrative in the Rejected alternatives paragraph. Keep one slot-movement statement.
  - Mark `reading-published-prices.md`, `what-each-invocation-was-sent.md` and sections 2 to 8 of `reducing-what-the-factory-costs.md` as plans (one "Not built" banner each), or move them under a plans heading, so no reader mistakes them for what exists.
  - Fold `provider-usage-and-cost.md`'s "Until the commands move onto the ledger" paragraph and "Evidence and limits" (which says no code was read) into a current "what is built" section.
  - Cut the "Evidence" paragraphs ("Read at main ... was not read") from `provider-adapters-and-endpoints.md`, `reading-published-prices.md`, `what-each-invocation-was-sent.md` and `reducing-what-the-factory-costs.md`. They record the author's reading, not the design.

Note on budget: I did not read tests. I read the config, `internal/runstate/routing.go` (headers and key types), `internal/orchestrator/subagents.go`, `internal/pricing`, `internal/usage`, `internal/spend`, `internal/cost`, `internal/chat/passcontext.go`, `internal/contextbundle/unchanging.go`, and the backends' launch code. Slot-fallback behaviour past the record types (the actual switch decision code in `pipeline.go` / `modelfailover`) was not traced line by line.

---

## claude-execution-and-account-routing.md — Execution routing: pinned invocations, developer slots, and safe provider fallback
Verdict: partly stale
Size: 43 KB. Last changed: 2026-11-21.

### Findings
- G6-1 [DIFFERS] The four developer slots and their pairs are "the authorized mapping" adopted by configuration.
  - Document: lines 134-145: "Adoption uses exactly these four developer slots" with slot 1 `codex / gpt-6.1-sol` and slots 2-4 `claude-code / opus`; line 225 gives reviewers an alternate.
  - Code: `.yoyodyne/config.yaml:127-155` has `max_concurrent_developers: 3`; slot 1 has only `prefer: [reliability]` (no `routing`), slot 2 has `routing: {enabled: false, primary: {provider: claude-code, model: sonnet}, alternate: {model: opus}}` plus `dedicated: true`, slot 3 has `prefer: [usability, product, performance, cost]` and `dedicated: true`. `.yoyodyne/config.yaml:536-642`: developer and reviewer are `claude-code` / `opus` with no `failover` block; the comment at line 535 says "Provider failover is disabled." `docs/configuration.md:4604-4610` already says "the four-slot mapping in the design is a configuration somebody has to apply." The model name `gpt-6.1-sol` appears only in tests and docs; the one Codex agent in the config is `technical-health-pm` on `gpt-6-astra` (`.yoyodyne/config.yaml:599`).
  - What to change in the document: say the four-slot table was the 2026-10 adoption target, that it was not applied (operator directed Opus-only on 2026-10-05, then a Sonnet trial on slot 2 on 2026-11-14), and that the table is now an example, not an authorization; or delete the table and the "Narrow adoption" section.
- G6-2 [DIFFERS] The routing fields and records are described as not yet shipped.
  - Document: line 147: "The exact serialized field names must follow the repository's configuration conventions ... The table is the required behavior, not a claim that those fields are already shipped." Line 95: "These responsibilities do not imply that either implementation or adoption has occurred." Line 187: "the following records and ordering are mandatory."
  - Code: `internal/domain/label.go:93-98` (`DeveloperSlot.Routing *EndpointPair` with `enabled`, `primary`, `alternate`; `Subagents`); `internal/runstate/routing.go:109-121` (`RunRouting` with `Slot`, `Developer`, `Reviewer`, `Reconfigurations`, `Operations`), `:255-` (`RoutedOperation` with switch allowance, `HistoryUnknown`, recovery deadline), `:457-471` (transition progress planned, source_reconciled, destination_prepared, destination_launched, outcome_recorded), `:324` (attempt state `prepared`). `docs/configuration.md:4604-4720` documents the field names and run behaviour.
  - What to change in the document: name the shipped keys (`execution.developer_slots[].routing.{enabled,primary,alternate}`, `.subagents`) and the record types, and replace "not already shipped" with what is built; leave only adoption (work item 435.14, still open) as outstanding.
- G6-3 [MATCHES] A run taken up again whose own slot is busy takes a free slot ahead of fresh work, recording the move: `internal/runstate/routing.go:130-151` (`MovedFrom`, `MovedBecause`, `SlotMoveSays`), `:652` and `:708` (the move, with a separate path for a run with an explicit pair: `:662-677` "MoveSlot moves it"). Matches lines 111-118.
- G6-4 [MATCHES] One switch per operation: `internal/runstate/routing.go:167-169` ("SwitchLimit ... The design fixes it at one") and `:255-` `SwitchAllowance`.
- G6-5 [MATCHES] A slot pair is pinned in a run snapshot with a digest and origins per field; reconfiguration is explicit and recorded: `routing.go:153-172`, `:117-119` ("Reload and resume never add one").
- G6-6 [MATCHES, compact] Explicit pair beats `execution.developer_models` label rules (`docs/configuration.md:4630-4634`); reviewer uses its own `failover` block, never the developer pair (`:4647-4652`); both pair blocks validated while disabled (`:4640-4646`); legacy `HistoryUnknown` gets no allowance (`routing.go` RoutedOperation).
- G6-7 [UNCLEAR] Acceptance criteria 6, 7 (crash-injection and two-controller races) and 9 (older binary refuses a live transition). I saw the record fields and the 2-minute wait in `docs/configuration.md` but did not read the tests or the recovery code.

### Missing from the document
- The cache lifetime and other launch settings are not routing, but the config knob `execution.usage_limit_max_pause: 6h`, `usage_limit_in_process_pause`, `usage_limit_unknown_reset_pause: 30m`, `server_overload_pause: 90s` (`.yoyodyne/config.yaml:215-226`) are the capacity-wait settings lines 173-181 talk about without naming; name them or point to `docs/configuration.md`.
- Slot `dedicated: true` (`internal/domain/label.go:86-93`) and the rule that not every slot may be dedicated; the document only mentions the reliability preference.
- `agents.developer.instances: 4` in the config (`.yoyodyne/config.yaml:555`) against three slots in use; the document says "one instance per configured slot".

### Overlap with other documents
- Lines 91 and 93 versus `provider-adapters-and-endpoints.md` line 47: both say this document and that one split Claude-specific and provider-general rules. They agree, but this document no longer holds anything Claude-specific; the title still says "Claude execution" in the file name (`claude-execution-and-account-routing`). Consider renaming the file.
- Sub-agent settling at slot move: lines 115-116 and `developer-sub-agents.md` lines 121 and 211 (item 11). They agree.
- Conversation account failover paragraph (line 253) versus `provider-adapters-and-endpoints.md` line 53 (pools and affinity): same claim, no conflict.

### Could be cut or moved
- Lines 122: the dated incident narrative ("on November 20 it left runs recorded on slot 2 waiting over two hours", "left a free slot idle for over two hours on October 31"). CLAUDE.md says incident history belongs in the commit message or work item.
- Lines 267-268, 255-267 ("Narrow adoption after implementation"): a one-off procedure for the development manager. Move to the work item or the operating guide.
- Lines 297-301 ("Delivery boundary") restates who owns what.
- Revision-log amendments at 2026-11-01 and 2026-11-21 (frontmatter) are the only place the slot-move history lives; fine, but the body repeats them.

---

## developer-sub-agents.md — Developer runs that hand coding to sub-agents on a cheaper model
Verdict: current (one small difference)
Size: 23 KB. Last changed: 2026-11-15.

### Findings
- G6-9 [DIFFERS] The instruction choice for Opus main.
  - Document: lines 57-61: "Opus | any model that is not Opus | Opus main with cheaper sub-agents"; "any other pair | | the general instruction".
  - Code: `internal/orchestrator/subagents.go:78-87`: `main == "opus" && (sub == "sonnet" || sub == "haiku")` gets the Opus file; a sub-agent model whose name carries no family (for example a full identifier that does not contain "sonnet" or "haiku") gets the general instruction.
  - What to change in the document: say "Sonnet or Haiku" instead of "any model that is not Opus".
- G6-10 [MATCHES] Three instruction files built into the program, chosen from main and sub-agent models by family, whether alias or full name: `internal/orchestrator/subagents.go:60-96` (`go:embed` of `developer-sub-agents.md`, `-opus-main.md`, `-sonnet-main.md`; `modelFamily` matches by substring).
- G6-11 [MATCHES] Sub-agent default model `sonnet`, slot override of `enabled` and `model`, agent-level `effort`: `internal/config/subagents.go:21-36`, `internal/domain/label.go:116-121` (`SlotSubagents{Enabled, Model}`).
- G6-12a [MATCHES, compact] Settings settled before first launch and again after a slot move (`internal/orchestrator/subagents.go:5-12`, `settleSubagents`, `movedSinceSubagentsSettled`); a provider that does not offer them serves the run without, with a plain-words record (`subagentsUnavailable`, `:245`); sub-agent-only usage limit does not move the run to its alternate (`:13-20`, `backend.SubagentsOnlyLimit`, `noteSubagentLimit`); sub-agent calls recorded per invocation (`backend.SubagentCall`, `internal/runstate/subagents.go:158`).
- G6-12b [UNCLEAR] Containment rules (lines 139-145: only the harness's sub-agent definition, tools subset, no background) and the 5-minute silence limit counting sub-agent events. I saw `subagentLaunch` in `internal/backend/claudecode/backend.go:761` but did not read it or its tests.
- G6-12c [UNCLEAR] The trial (lines 168-179). `.yoyodyne/config.yaml:136-146` shows the Opus-main/Sonnet-sub-agent trial was ended (cost more) and slot 2 now trials Sonnet main with sub-agents off. The document does not record either result.

### Missing from the document
- The shipped project configuration: sub-agents are not on for any slot now (`.yoyodyne/config.yaml:139-146`, "with sub-agents off"). The document describes the trial as future ("After the build and the instruction files land").
- The trial outcome the config comment states: Opus main with Sonnet sub-agents (yoyodyne-wj4l) "cost more without a quality gain".

### Overlap with other documents
- Lines 45 and 116-118 versus `claude-execution-and-account-routing.md` lines 115-116: agree.
- `reducing-what-the-factory-costs.md` section 4 and its last paragraph of section 4 say this trial is "already decided, unchanged"; it is no longer in progress. Update both together.

### Could be cut or moved
- Lines 41 (history of the revisions) and the "Build pieces" section (lines 181-185): the build is done; replace with one "built" line.
- The starting wording block (lines 99-101) duplicates `internal/orchestrator/developer-sub-agents.md`; point to the file instead.

---

## provider-adapters-and-endpoints.md — Provider adapters and execution endpoints
Verdict: partly stale
Size: 9 KB. Last changed: 2026-10-26.

### Findings
- G6-13 [DIFFERS] The Codex per-run home location.
  - Document: line 51: "The home the harness prepares for each Codex run lives under the machine home, at `~/.yoyodyne/accounts/<account>/codex-home/`, never inside `~/.codex`"; line 57: "No file the harness writes for a Codex run is under `~/.codex`."
  - Code: `internal/backend/codex/context.go:64-83` (`providerHome`: the account's configured dir, else `CODEX_HOME`, else `~/.codex`), `:124-130` and `:150-` (`prepareProviderHome` makes `.yoyodyne-codex-home` inside that home). `docs/provider-plugins.md:525` documents the same. Work item yoyodyne-rmo9 (open, p3) is the build that would move it.
  - What to change in the document: state what the code does and mark the machine-home location as the target, tied to yoyodyne-rmo9.
- G6-14 [DIFFERS] Which package decodes cost events.
  - Document: line 49: "`internal/cost`, which alone decodes cost events"; line 57 repeats it.
  - Code: `internal/cost/costevent/costevent.go:1-30`: "Package costevent is the one reader of the events in an event log that say what a provider invocation cost". `internal/cost/cost.go` is the work-item pricer. The package sits under `internal/cost` so the run store can import it.
  - What to change in the document: name `internal/cost/costevent`.
- G6-15 [MATCHES] Every `Run` returns the best result together with any error: `internal/backend/backend.go:549-559`.
- G6-16 [MATCHES] Claude Code reviewer gets read-only tools over the worktree (`Read`, `Glob`, `Grep`, git-only `Bash`) and management roles get none: `internal/backend/claudecode/backend.go:189-232` (`readOnlyTools` is empty "and no caller can add to it"; `reviewerTools`). Note: the reviewer also gets limited `Bash` for git commands; the document says only "read-only tools".
- G6-16b [MATCHES, compact] Pools with `active` and `reserved` accounts and round-robin: `internal/config/accounts.go:50-107`. Codex read-only sandbox with explicit launch flags and native reads not confined to the repository: `internal/backend/codex/backend.go:277-285`. One usage record per invocation (not per provider call): see G6-24.
- G6-16c [UNCLEAR] Role eligibility refused at config load "from declared capabilities, never provider name" (line 51); I saw `Capabilities()` on the interface but did not trace the load check.

### Missing from the document
- Reviewer cache lifetime is set to 5 minutes and management roles to 1 hour (`internal/backend/claudecode/backend.go:385-430`); a launch setting a reader of "which model and how" would need.
- The `--exclude-dynamic-system-prompt-sections` flag and the conformance test directory `internal/backend/conformance`.

### Overlap with other documents
- Lines 49 and 55 versus `provider-usage-and-cost.md`: this document links it and says it states no counting rule; agrees, but line 55 says "one usage record per provider call" and the code writes one per invocation (G6-24).
- Line 47 and `claude-execution-and-account-routing.md` line 91 split ownership of fallback; agree.

### Could be cut or moved
- Lines 58-59 ("Evidence"), which list what was and was not read at a past commit and say the adapters' code "was not read". Remove.
- The frontmatter revision reasons repeat the body.

---

## provider-usage-and-cost.md — Provider usage and cost
Verdict: partly stale (mostly built; text still says nothing is)
Size: 14 KB. Last changed: 2026-10-23.

### Findings
- G6-17a [MATCHES] Five counters, each measured, measured zero or absent, never stored as zero: `internal/usage/usage.go:27-90` (`Count{Tokens, Measured}`, `FreshInput`, `CachedInput`, `CacheWrite`, `Output`, `Reasoning`).
- G6-17b [MATCHES] Four billing modes `api_key`, `subscription`, `credits`, `not_recorded`: `internal/usage/usage.go:203-222`.
- G6-17c [MATCHES] Four kinds of cost (reported, estimated, comparison, unknown) from one pricing service; later estimates appended with a `Revises` pointer; `yoyo cost --reprice`: `internal/pricing/pricing.go:23-45`, `internal/runstate/spendcost.go:1-60`, `internal/cli/cost.go:29-35` and `:141-150`. Old lines are read with absent counters (spendcost.go header).
- G6-18 [DIFFERS] "One usage record per provider call".
  - Document: line 15: "Every call the harness makes to a provider writes exactly one usage record".
  - Code: `internal/spend/spend.go:127`: "Every invocation it serves appends exactly one line". An invocation (including its reissues) is the unit; each carries `NewInvocationID` (`internal/runstate/spendcost.go:25`). A reissue is a separate invocation, so the two agree unless one invocation makes several provider requests.
  - What to change in the document: say "one line per provider invocation", and say how a session's several requests are counted.
- G6-19 [NOT BUILT] Rate data. Document lines 57-60: rate entries "kept in the repository beside the pricing service". Code: `internal/pricing/rates.json` is `{"version": "2026-10-23.1", "entries": []}` and `internal/pricing/README.md` "No entries yet". The service exists and is tested; there are no prices, so every call without a provider-reported cost is unknown. What to change: say plainly the file is empty and point to `reading-published-prices.md`.
- G6-19b [UNCLEAR] "Until the commands move onto the ledger" (lines 98-104): `yoyo cost` now prints `CostKinds` from the ledger beside item prices (`internal/cli/cost.go:29-35`), but I did not confirm whether the old event-log price columns are still printed. Check `internal/cli/cost.go` and `status` before rewriting that paragraph.
- G6-19c [UNCLEAR] Recorded Codex streams: line 127 cites yoyodyne-ifd.435.7 (open). Recorded streams exist at `internal/backend/testdata/streams` and `internal/backend/codex/testdata`; I did not check whether they cover every grid case.

### Missing from the document
- What each ledger line actually carries now: `models` (per-model usage), `reported_total_usd`, `unknown` reason, `account_alias`, `config_revision`, `schema_version` (`internal/runstate/spend.go:125-180`). Useful to a reader of per-role and per-model spend; sub-agent per-model split depends on it (see `developer-sub-agents.md` line 150).
- `yoyo cost --reprice`.
- Spend rows are grouped by stream kind, not by the four cost kinds (`internal/runstate/stream.go:677-870`); the document's "four kinds" and the dashboard/`yoyo status` "stream kind" totals are two different splits.

### Overlap with other documents
- `reading-published-prices.md` lines 5 and 135-142 repeat the cost kinds and the "subscription is comparison only" rule. Agree.
- `provider-adapters-and-endpoints.md` line 49 repeats the "adapter hands events to the shared code" rule. Agree.
- `observability-and-dashboard.md` (group elsewhere) is named at line 94 as "revised to match"; I did not check it.

### Could be cut or moved
- Lines 10-11 ("The rules here were first written on October 13 ... dropped from the forge's merge queue") and 117-131 ("Evidence and limits", "No code was read"): history, not design.

---

## reading-published-prices.md — Reading the providers' published prices
Verdict: mostly a plan not built
Size: 13 KB. Last changed: 2026-10-27.

### Findings
- G6-20 [NOT BUILT, code absent] `internal/pricing/sources.json` and the page list.
  - Document: line 28: "The pages are listed in `internal/pricing/sources.json`".
  - Code: `internal/pricing/` holds `pricing.go`, `rates.go`, `rates.json`, `README.md`, `pricing_test.go` only. No `sources.json`, no `testdata`.
  - What to change in the document: add a "Status: not built" banner naming work item yoyodyne-ifd.435.34 (open).
- G6-21 [NOT BUILT, code absent] The reading code, page fetch-and-keep, placing kept pages in a developer's checkout, the weekly recurring task, the warning reports. Searches for `sources.json`, a price reader, and a price-reading recurring task in `internal/` and `.yoyodyne/config.yaml` found nothing. The recurring tasks in the config (`.yoyodyne/config.yaml:253-365`) have no price-reading task.
- G6-22 [MATCHES] The pinned-outside-file fetcher the design reuses exists: `internal/outsidefile/fetch.go` (https only, `MaxRedirects = 5`, `CeilingBytes = 50 << 20`, default 5 MiB); and `outside_files.hosts` is the one host list (`.yoyodyne/config.yaml:372-374`, currently only `cdn.jsdelivr.net`, so the two pricing hosts would also need adding).
- G6-23 [DIFFERS] The document says the fetcher may not be on main ("Whether the pinned files fetcher is on main was not checked", line 150; build piece 1 "waits on it where that is not yet on main"). It is on main: `internal/outsidefile/`.
  - What to change in the document: drop the caveat and the wait.
- G6-23b [MATCHES] Subscription calls are shown for comparison only: `internal/pricing/README.md` ("a subscription call is compared at the `api_key` entry ... never as money spent").

### Missing from the document
- The one-line fact that `rates.json` is the only source and is empty; so today no Codex call is ever an estimate.

### Overlap with other documents
- Lines 5, 135-142 versus `provider-usage-and-cost.md` lines 40-47 (the four kinds): same statement, agree.
- Lines 20-28 versus `pinned-outside-files.md` (not read): rules said to be reused; not checked.

### Could be cut or moved
- The "Evidence and limits" paragraph (line 148-150) and the "Alternatives rejected" list can move to the work item once built.

---

## reducing-what-the-factory-costs.md — Reducing what the factory costs to run
Verdict: mostly a plan not built (piece 1 built)
Size: 28 KB. Last changed: 2026-11-21.

Which numbered pieces are built (the document's "Build pieces", lines 227-245):

| Piece | Built? | Evidence |
| --- | --- | --- |
| 1. Record where each opening stops being reused | Partly | `internal/chat/passcontext.go:70-82` (`PassStart.UnchangingBytes`, `FirstChangedPart`, `SameAsPrevious`) and `:328-329` (per-part fingerprints compared with the previous pass). The cached/fresh provider tokens per pass were not found beside it. |
| 2. Send the unchanging part apart | Built | `internal/contextbundle/unchanging.go:26-44` (`SplitUnchanging`), `internal/chat/passcontext.go:238` and `:305` (put at the end of the instructions block). |
| 3. Models by kind (`model_kinds`) | Not built | no `model_kinds` anywhere in `internal/`, `docs/`, or `.yoyodyne/`. |
| 4. Skip passes with nothing new | Not built | no `skip_when_unchanged` or `full_pass_every`; the "skipped" in `internal/runstate/sweep.go:769` is a step outcome inside a sweep. |
| 5. Diagnosis of the development manager's sessions | Unknown | no code to find; a diagnosis would be in `docs/diagnoses/`, not checked. |
| 6. Developers start cheaper, move up once | Not built | no `developer_cheaper_first`, `high_risk_labels`. |
| 7. Reviewer model by kind | Not built | no `reviewer_models`. |
| 8. Factory-flow PM on the cheaper model | Not done | `.yoyodyne/config.yaml:565-570` still `model: opus` for `factory-flow-pm`. |
| 9. `repository.ask` | Not built | no such tool in `internal/toolcatalog/catalog.go`; only `repository.read`, `.list`, `.search`. |
| 10. Weekly cost figures | Not built | no weekly cost-per-item report found. |

### Findings
- G6-24 [NOT BUILT, code absent] Pieces 3, 4, 6, 7, 8, 9 and the settings table in section 8 (lines 189-205): none of `model_kinds`, `skip_when_unchanged`, `full_pass_every`, `developer_cheaper_first`, `high_risk_labels`, `reviewer_models`, `repository_ask` exists. Document: line 185 "The defaults are today's behaviour except where a change trades nothing away." Change: add a banner "Sections 2-8 and pieces 3-10 are a plan".
- G6-25 [MATCHES] The unchanging part never holds work items, the listing of omitted documents or memories: `internal/contextbundle/unchanging.go:15-24` (opening is only "Product context" and "Recorded product intent" and intent documents/indexes; everything else is in the rest).
- G6-26 [DIFFERS] Content of the unchanging part.
  - Document: line 71: "the role's contract, persona and remit, then the product intent whole".
  - Code: `internal/contextbundle/unchanging.go:10-25` splits only the briefing's opening (product context, recorded intent, intent documents, directory indexes); the contract, persona and remit are the instructions block that it is appended to (`internal/chat/passcontext.go:238`, "ends the instructions block").
  - What to change in the document: say the instructions block is the role's contract, persona and remit, and the unchanging part is appended to its end.
- G6-27 [MATCHES] The cache lifetime for management roles is one hour, set explicitly: `internal/backend/claudecode/backend.go:393-430` (`conversationCacheLifetime = "1h"`, comment refers to `passcontext.go`). The document's "keeps the cache that lasts an hour" holds.
- G6-28 [DIFFERS] Section 4's "the only Sonnet seat takes one label" and "That was never built".
  - Document: line 143-ish ("This is not a controlled comparison: the only Sonnet seat takes one label of work").
  - Code: `.yoyodyne/config.yaml:136-146` slot 2 is now Sonnet-main with sub-agents off, dedicated to `team-development`. Slot selection by model exists (`routing`) and the "move up once after a failure" is not built. The section also says the Opus-with-Sonnet-sub-agents trial is "already decided, unchanged"; the config says it ended (cost more, no quality gain).
  - What to change: update the model facts and trial status.
- G6-29 [UNCLEAR] The measured figures in "Where the money goes" ($5,286 over 14.5 days, 78% cache writes, etc.). They come from the operator's assistant's measurement and were not re-measured by the architect; I did not check them.
- G6-29b [UNCLEAR] Whether the reviewer's 5-minute cache setting conflicts with the document's advice (below).

### Missing from the document
- The reviewer's invocations are already set to the 5-minute cache lifetime and the `--exclude-dynamic-system-prompt-sections` flag (`internal/backend/claudecode/backend.go:385-405`, citing `docs/diagnoses/yoyodyne-ifd-424-one-shot-cache-reads.md`): a cost decision in this area the document does not mention.
- Slot-level model choice already exists (`routing`, `execution.developer_models`), so section 4's "developers start on the cheaper model" has a partial mechanism through slot pairs; the document says nothing is built.
- The recurring-pass cadences were already lowered to 45 minutes for cost (`.yoyodyne/config.yaml:253-300`).

### Overlap with other documents
- Section 4's last paragraph versus `developer-sub-agents.md` lines 168-179 (the trial): this one calls it "already decided"; the other calls it a future trial. Neither records the November 14 result. Update both.
- Section 6 (PM model trial) and section 8 versus `portable-agent-configuration.md` section 6 (named at line 189): this document lists settings it says that document will gain. The other document was not read here; confirm they agree.

### Could be cut or moved
- Lines 16-28 (the pre-fix measurement narrative: pairs, bytes, "in 8 of 13 consecutive pairs") are evidence, not design; keep one sentence.
- Section 1's "What was built" paragraph (lines 74-82) becomes a "Built" note once it names current code.

---

## what-each-invocation-was-sent.md — What each invocation was sent
Verdict: mostly a plan not built
Size: 8 KB. Last changed: 2026-10-27.

### Findings
- G6-30 [NOT BUILT, code absent] The stored-parts record, the content-digest store, the per-invocation entry and the command that prints it.
  - Document: line 53: "The run record gains one entry for each invocation ... the digest of the whole text"; line 51: "A command beside the existing run commands lists a run's invocations".
  - Code: searches of `internal/` for a prompt/parts/digest store, a "sent text" record, a per-invocation parts entry, and in `internal/runinspect` and `internal/cli` for a command showing what was sent found none. `internal/backend/backend.go:549-559` (`Backend.Run(ctx, request RunRequest)`) takes an ordinary request, not a recorded invocation, so "an invocation that was not recorded does not compile" is not true. Work item yoyodyne-ifd.428.126.1.2 is open.
  - What to change in the document: add a "not built" banner naming that work item.
- G6-31 [UNCLEAR] The document's list of what counts as sent. I did not read the code that assembles developer and reviewer instructions (`internal/orchestrator/pipeline.go`), which the document itself says the build must read first.

### Missing from the document
- The harness now adds a sub-agent instruction file to a developer run's prompt (`internal/orchestrator/subagents.go:107-120`, `withSubagentInstruction`) and a settled sub-agent setting on the routing record; the "parts" list (line 33-ish: contract, persona, invariants, work item, notes, findings, change) does not name it.
- The management roles' recurring-pass start already records sizes and fingerprints of each part (`internal/chat/passcontext.go:70-82`), a partial neighbour of this record for management turns, which the document puts out of scope.

### Overlap with other documents
- `reducing-what-the-factory-costs.md` piece 1 (digest of each part of a pass's start) and this document's per-part digests describe similar records with different owners and stores. They do not conflict; say which one owns pass-start records.
- Lines 51-52 repeat the guarantee 1 and guarantee 8 statements in `one-owner-for-each-guarantee.md`; not re-read.

### Could be cut or moved
- "What it rests on, and what was not read" (lines 15-33) and "Invariant" (none) sections.
