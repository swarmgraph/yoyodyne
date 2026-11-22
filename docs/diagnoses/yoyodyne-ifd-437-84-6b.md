# yoyodyne-ifd.437.84: second reading of the design documents against the code, group 6: execution, providers and cost

A second, adversarial reading of the report `yoyodyne-ifd-437-84-6.md` beside this file. It re-read every finding there that differs or is not built against the code, tried to settle the unclear ones, checked a sample of the matches, and looked for what the first reading missed. **Where the two files disagree, this one stands.**

- **Read on:** 2026-11-21, in the evening, between 18:33 and 18:43 PST, with main at `848d86190`. The code had moved since the first reading; each finding is judged against the code as it was at this reading.
- **Read by:** a read-only reader on Opus 5.5 in the operator's assistant's session. It read code and ran nothing, tests included.
- **Checked by hand:** nothing in this file was checked again by hand.
- **How to use it:** evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Second-reading classes:** `CONFIRMED`, the first finding is right as written; `CORRECTED`, its gist is right and a detail is wrong; `WRONG`, the design and the code agree or the finding misread one of them; a class in the first reading's own words where an unclear finding was settled.

Read on 2026-11-21 against main at `848d86190` (merged 18:14 PST). Read only; nothing was run. `reducing-what-the-factory-costs.md` was re-read as it is now (revision 4, approved 2026-11-22T01:25Z, 5:25 PM PST; 270 lines). Every line number the first report gives for that file is stale, so this reading gives the current ones.

## Summary
- Findings re-read: 14 (the 9 `DIFFERS` and 5 `NOT BUILT` ids). CONFIRMED 10, CORRECTED 3, WRONG 1, CANNOT TELL 0. The build-pieces table for `reducing-what-the-factory-costs.md` was also re-checked (see that section).
- Unclear findings settled: 8 of 9 (G6-7, G6-12b, G6-12c, G6-16c, G6-19b, G6-19c, G6-29b, G6-31; G6-29 is still unclear).
- Verdicts changed: `developer-sub-agents.md`, from "current" to "partly stale".
- Findings the architect should not act on as written:
  - G6-26 (WRONG): design line 103 and the code agree. The cached block is the role's instructions, then fixed framing, then the unchanging part of the briefing (`internal/chat/passcontext.go:260-265`). That is design line 103's "contract, persona and remit, then the product intent". Lines 114-119 of the revised document describe exactly this.
  - G6-28 (CORRECTED): "the only Sonnet seat takes one label of work" (line 158) is true: slot 2 is Sonnet and dedicated to team-development (`.yoyodyne/config.yaml:141-146`). Only line 169, which calls the sub-agent trial "already decided … unchanged", is stale.
  - G6-14 (CORRECTED): the problem is bigger than the package name. The Claude Code adapter decodes cost itself (`internal/backend/claudecode/parser.go:103`, `:959`, `:1101-1106`; `claudecode/subagents.go:279`). So "no adapter decodes a cost event itself" (lines 49 and 57) is false as well.
  - G6-18 (CORRECTED): the design text is at line 38, not 15. In that design a "call" means one harness invocation: the paths it lists are invocations, and section 50-57 works out per-call amounts from a session's running totals. This needs a wording fix, not a change of behaviour.
- The most important things the first reader missed:
  - In `execution.developer_slots[].routing`, `enabled` switches fallback only. A pair with `enabled: false` still pins the run to its primary: slot 2 runs Sonnet that way (`internal/config/slotrouting.go:98`, `internal/runstate/routing.go:527`, `internal/orchestrator/developerrouting.go:1045`). A rewrite that names the keys has to say this.
  - Switching and crash recovery are built and tested. The switch decision is in `developerrouting.go:1031-1111`, the launch gate and recovery in `internal/runstate/launch.go:1-30` and `:457-592`, and the crash, race and newer-version tests are in `launch_unix_test.go:368-1034` and `routing_test.go:224-634`. G6-7 is settled as MATCHES.
  - Codex runs read the account's own `config.toml`, with only the instruction keys taken out (`internal/backend/codex/context.go:131-148`). That contradicts provider-adapters lines 51 and 57 ("No adapter reads configuration from … the account's own provider home").
  - Reducing section 6 and piece 8 (put the factory-flow program manager's scheduled passes on the cheaper model, through a recurring task's model) have no mechanism. That PM's passes come from `triggers`, which have no model key (`internal/config/lane.go:62-69`). The only other way is the agent's `model`, and line 224 says that stays the operator's.
  - `provider-usage-and-cost.md` line 52 says only the shared code turns running totals into per-call amounts. The Claude Code parser applies its own session-increment rule (`parser.go:75-81`, `:1101-1106`).

## claude-execution-and-account-routing.md
Verdict: agree (partly stale). The routing machinery is built. What is stale is the adoption text: the four-slot table (lines 134-147), line 95, and the "Narrow adoption" section.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G6-1 | DIFFERS | CONFIRMED | `.yoyodyne/config.yaml:124` (`max_concurrent_developers: 3`) and `:135-157` (the slots; the report says 127-155). Two small corrections: `gpt-6.1-sol` also appears in non-test code, at `internal/backend/effort.go:62` (an effort table only, not a slot). And the config comment at `:535`, "Every role runs on Claude Opus", is itself stale: slot 2 runs Sonnet (`:145`) and technical-health-pm runs Codex (`:598-599`). |
| G6-2 | DIFFERS | CONFIRMED | `internal/domain/label.go:93-121`, `internal/runstate/routing.go:109-121`, `:234-276`. When the rewrite names the keys, it must say that `routing.enabled` is fallback-on/off, not pair-on/off (see Missed). |

UNCLEAR settled:

| Finding | First reading | Second reading | Evidence |
|---|---|---|---|
| G6-7 | UNCLEAR | MATCHES | **AC6 (crash injection):** `internal/runstate/launch_unix_test.go:368` (killed before launch), `:398` (after spawn, before acknowledgement), `:434` (between marking and opening the gate), `:454` (running provider adopted), `:485` (after completion, before the outcome is recorded), `:508` (surviving child), `:555` (reused PID), `:1034` (earlier boot); `routing_test.go:462` (restart at each persisted transition). **AC7 (races):** `launch_unix_test.go:900` (two competing recoveries launch once), `:937` (two launches of one attempt); `routing_test.go:370` (idempotent, conflicting identity refused), `:415` (competing transitions leave exactly one). **AC9:** `routing.go:35-43` and `:76` (`RoutingVersion = 2`, `ErrUnsupportedRouting`); `routing_test.go:634` (a newer version is neither resumed nor replaced). **Switch decision:** `developerrouting.go:1031-1111` allows one switch, only on a classified usage limit (`:979-1006`) or a primary already known to be limited (`:792-800`). The alternate is checked before use (`:1052`). It refuses when fallback is off, when the switch is spent, or when the operation is already on its alternate (`:1045-1050`). A switch whose source may still be running waits for reconciliation (`:776-782`; `launch.go:457-592`). I did not find a test that injects a crash at the usage-persistence point of AC6. |

MATCHES spot-checked, not matches:
- G6-3: the behaviour matches, but the citations are off. `MoveSlot` is at `routing.go:638` and `MoveSlotRepinned` at `:669`, not 652/708/662-677. The decision itself is `internal/runstate/store.go:319-372` (`takeUpSlot`: the lowest free number, a dedicated slot included; repinned for an explicit pair).

### Missed by the first reading
1. **What `enabled` means.** `EndpointPair.Enabled` is recorded as `FallbackEnabled` (`internal/config/slotrouting.go:98`, `routing.go:527`). A slot with `routing` but `enabled: false` is still `Explicit` and launches its primary (`developerrouting.go:207-219`, `:244-261`). The design (lines 126-132, 147) never says a pair can pin without fallback, and that is exactly how slot 2's Sonnet trial is configured (`.yoyodyne/config.yaml:141-146`).
2. **A run moved to a slot with no pair.** Line 116 covers a pinned run moved to "the new slot's own pair". In code, when the new slot has no explicit pair, the run is pinned to the developer's own endpoint (`developerrouting.go:221-241`, `slotSnapshot`). So a pinned run can end up holding a snapshot with `Explicit` false. The design does not say this.
3. **The merge queue's review also switches endpoints** (`internal/orchestrator/mergequeueworker.go:1324`, `moveQueueReview` with `SwitchUsageLimit`). The design names only the developer and the run reviewer (lines 159, 221-229).

### Contradictions with other designs
- None new beyond the first reading's overlaps. Line 225 (reviewer alternates) and `docs/configuration.md:4604-4612` already agree that the mapping is unapplied configuration.

## developer-sub-agents.md
Verdict: should be **partly stale**. The mechanism is built and contained, but the trial section (lines 168-179) is written as future, and the configuration has already ended one arrangement and turned sub-agents off everywhere.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G6-9 | DIFFERS | CONFIRMED | `internal/orchestrator/subagents.go:78-87`. Opus main with a sub-agent model carrying no family name gets the general instruction, while the design (line 59) gives it the Opus-main instruction. The code also recognises Haiku as a family (`:93`); the design's line 55 names only Opus, Sonnet and "another". |

UNCLEAR settled:

| Finding | First reading | Second reading | Evidence |
|---|---|---|---|
| G6-12b | UNCLEAR | MATCHES | `internal/backend/claudecode/subagents.go:13-34`: the only sub-agent is one defined on the command line with `--agents`; a hook refuses every other type; tools are the run's minus Agent/Task; `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH=1` and `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` (`:52-57`); `--forward-subagent-text` puts sub-agent output on the stream (`backend.go:787-788`). One nuance: definitions from the repository are refused when used, not left unloaded (design line 139). |
| G6-12c | UNCLEAR | DIFFERS | `.yoyodyne/config.yaml:136-140`: the Opus-main/Sonnet-sub-agent trial (yoyodyne-wj4l) "cost more without a quality gain", and slot 2 now trials Sonnet main with sub-agents off. No `subagents` key appears anywhere in the config, so sub-agents are off on every slot. Design lines 170-179 still describe the trial as future and record no result. |

### Missed by the first reading
1. Line 117 says precedence is "slot, then agent, then off" and that slot settings sit "beside the slot's endpoint pair". That is built (`internal/domain/label.go:95-105`), but no slot in the shipped configuration uses it. Worth one line saying where the shipped config stands.
(The other gaps are covered by G6-9 and G6-12c.)

### Contradictions with other designs
- Lines 168-179 (trial still to run) against `reducing-what-the-factory-costs.md:169` ("a separate trial already decided … it is unchanged"). Both are stale against `.yoyodyne/config.yaml:136-140`.

## provider-adapters-and-endpoints.md
Verdict: agree (partly stale), and staler than the first reading says. See G6-14 and Missed 1.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G6-13 | DIFFERS | CONFIRMED | `internal/backend/codex/context.go:69-86` (`providerHome`) and `:127-148` (`madeHomeName = ".yoyodyne-codex-home"` inside the account's home). The report's 64-83 and 124-130 are close. |
| G6-14 | DIFFERS | CORRECTED | The gist is right: the reader is `internal/cost/costevent` (`costevent.go:1-21`). But the Claude Code adapter also decodes cost events itself. It reads `total_cost_usd` and per-model `modelUsage` cost off the stream (`internal/backend/claudecode/parser.go:103-106`, `:959`), applies its own increment rule to duplicate terminals (`:1101-1106`), and decodes sub-agent `costUSD` (`claudecode/subagents.go:279`, `:298`). So line 57, "No adapter decodes a cost event; only `internal/cost` does", is false, not just misnamed. |

UNCLEAR settled:

| Finding | First reading | Second reading | Evidence |
|---|---|---|---|
| G6-16c | UNCLEAR | MATCHES | `internal/backend/endpoint.go:217-253` (`Serves`/`ServesAt` decide from the provider descriptor's declared roles and postures, "deliberately the same question configuration validation asks"); `internal/config/endpoints.go:124` checks a failover alternate with `EligibleFor`. |

MATCHES spot-checked, not matches:
- G6-16: the reviewer also holds `Bash`, limited to read-only git commands (`claudecode/backend.go:204-232`). The design's "read-only tools" is true but leaves this out; the first reader noted it.

### Missed by the first reading
1. **The Codex run reads the account's own provider configuration.** `prepareProviderHome` uses the account's home as it is when it holds no instruction file and no instruction key. Otherwise it links every entry and copies `config.toml` minus the instruction keys (`codex/context.go:127-148`, `withoutInstructionKeys` `:93-125`). Every other `config.toml` setting the person keeps applies to the run. Design line 51 ("keeps configuration discovery away … from the account's own provider home") and line 57 say otherwise.
2. Line 55 ("one usage record per provider call") has the same wording issue as G6-18. The code writes one ledger line per invocation (`internal/spend/spend.go:126-129`).

### Contradictions with other designs
- Line 51 cites `machine-home.md` for the Codex home. `machine-home.md:171` lists only `accounts/` and says nothing of a `codex-home`, so the location rests on this document alone.

## provider-usage-and-cost.md
Verdict: agree (partly stale: mostly built; the rate data is empty).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G6-18 | DIFFERS | CORRECTED | The design text is at line 38 (and line 134), not line 15. `internal/spend/spend.go:126-129` writes one line per invocation. The design's own paths are invocations, and its per-call rules (lines 52-57) treat a resumed session's running total as one call's reading. So "call" already means invocation, and a wording fix is enough. |
| G6-19 | NOT BUILT | CONFIRMED | `internal/pricing/rates.json` is `{"version": "2026-10-23.1", "entries": []}`. Design lines 76-80 (the report says 57-60). |

UNCLEAR settled:

| Finding | First reading | Second reading | Evidence |
|---|---|---|---|
| G6-19b | UNCLEAR | MATCHES | The paragraph at lines 92-98 (the report says 98-104) is still current. `yoyo cost` prints item prices read from the event logs (`internal/cli/cost.go:87-91`, `:129`), then a separately labelled "by kind of cost … in the spend ledger" line (`:178-195`). `yoyo status` does the same (`internal/cli/status.go:691-696`). |
| G6-19c | UNCLEAR | DIFFERS | Only two Codex recordings exist, `internal/backend/codex/testdata/streams/codex-cli-0.159.2/{no-provider-reached,reply-and-turn-completed}.jsonl`. The first carries no usage (per its README). The grid cases at lines 111-120 (resumed totals, reset, repeated final, cut-off, failure with no usage, threshold, billing modes) have no Codex recording. `internal/backend/testdata/streams`, which the first report cites, does not exist. yoyodyne-ifd.435.7 is open. |

### Missed by the first reading
1. **Running totals are not handled only by the shared code.** Line 52 says the shared code "alone turns readings into per-call amounts". The Claude Code parser keeps `reportedCostUSD`/`duplicateCostUSD` and adds increments for repeated terminals itself (`internal/backend/claudecode/parser.go:75-81`, `:1101-1106`). This also breaks line 126 (pricing and cost logic inside adapters was rejected).
2. **A stale cross-reference.** Line 90 says `observability-and-dashboard.md` "still calls estimates reserved for future sources … is revised to match". It has been revised (`observability-and-dashboard.md:180`, revision for yoyodyne-ifd.432.37). Drop the clause.
3. The "Evidence and limits" paragraph is at line 145, not lines 117-131 as the report says.

### Contradictions with other designs
- Line 90 against `observability-and-dashboard.md:180`; see Missed 2.

## reading-published-prices.md
Verdict: agree (mostly a plan not built).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G6-20 | NOT BUILT | CONFIRMED | `internal/pricing/` holds `pricing.go`, `rates.go`, `rates.json`, `README.md`, `pricing_test.go`. `sources.json` is named at design line 41 (the report says 28). |
| G6-21 | NOT BUILT | CONFIRMED | No `sources.json` in `internal/` or `.yoyodyne/`. No recurring task mentions prices (`grep -i price .yoyodyne/config.yaml` finds only a comment at line 9). |
| G6-23 | DIFFERS | CONFIRMED | `internal/outsidefile/fetch.go:18-22` (`DefaultMaxBytes 5<<20`, `CeilingBytes 50<<20`, `MaxRedirects 5`). The caveat is at design line 151 and the wait at piece 1, line 118. `outside_files.hosts` lists only `cdn.jsdelivr.net` (`.yoyodyne/config.yaml:372-373`). |

### Missed by the first reading
None of weight.

### Contradictions with other designs
None found.

## reducing-what-the-factory-costs.md
Verdict: agree (mostly a plan not built). Section 1 is built, except the per-pass cached and fresh token figures (piece 1 in part). Sections 2-8 and the weekly figures are not built. The first report contradicts itself here: the summary says piece 2 is built and the heading says "(piece 1 built)". It should be piece 2 built and piece 1 in part.

The current document is laid out as follows. Sections 1-8 run from line 83 to line 230, the settings table is at lines 200-211, the build pieces at lines 245-258, and "What a reviewer can check" at lines 260-270.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G6-24 | NOT BUILT | CONFIRMED | No `model_kinds`, `skip_when_unchanged`, `full_pass_every`, `developer_cheaper_first`, `high_risk_labels`, `reviewer_models` or `repository_ask` in `internal/`, `docs/configuration.md` or `.yoyodyne/` (the only hit is unrelated text in `internal/readmodel/holds_test.go`). The settings table is now at lines 200-211, and "defaults are today's behaviour" is at line 198. For piece 9, cite `internal/capability/capability.go:47-64` (`repository.read`, `.list`, `.search`). The report's `internal/toolcatalog/catalog.go` is not where capabilities are defined. Skipping passes is admitted as yoyodyne-ifd.430.92, with no code yet. |
| G6-26 | DIFFERS | WRONG | Design line 103: the unchanging part is "the role's contract, persona and remit, then the product intent whole", sent as the separately cached block. Code: `withPassBriefing` sends `instructions + passBriefingFraming + passBriefing` as the system block (`internal/chat/passcontext.go:260-265`), and `SplitUnchanging` supplies the intent part (`internal/contextbundle/unchanging.go:26-42`). That is the same block in the same order. The revised lines 114-119 describe the code accurately. There are two small additions, both harmless and fixed text: the block also carries the "# Product context" opening (`unchanging.go:20-23`) and a framing paragraph (`passcontext.go:245-252`). |
| G6-28 | DIFFERS | CORRECTED | Line 158, "the only Sonnet seat takes one label of work", matches the config: slot 2 is Sonnet, prefers `team-development` and is dedicated (`.yoyodyne/config.yaml:141-146`). Line 160, "That was never built", is about escalating between execution profiles, which is indeed not built. The only stale statement is line 169: the sub-agent trial "already decided … unchanged". The config says the Opus-main/Sonnet-sub-agent arrangement ended (`.yoyodyne/config.yaml:138-140`). |

Build pieces table, re-checked:
- Piece 1 is partly built: confirmed. `PassStart` (`passcontext.go:70-82`) records `UnchangingBytes`, `FirstChangedPart` and `SameAsPrevious`, and per-part fingerprints are kept in `PassStartParts` (`:322-324`). There are no cached or fresh provider token figures per pass.
- Piece 2 is built except "Drop sizes from the listing" (line 248). The sizes are still sent (`unchanging.go:15-16`, "a role's own documents left out with their sizes"). Line 123 now calls the drop optional; see Missed 2.
- Piece 8: `factory-flow-pm` is still `model: opus` (`.yoyodyne/config.yaml:565-569`), and there is nowhere to set a model for its passes; see Missed 1.

UNCLEAR settled:

| Finding | First reading | Second reading | Evidence |
|---|---|---|---|
| G6-29 | UNCLEAR | STILL UNCLEAR | The figures (lines 79, 85-90, 133, 152, 158, 173, 185) come from the operator's assistant's measurement of the spend record and transcripts, which sit outside the repository. I did not reproduce them. |
| G6-29b | UNCLEAR | MATCHES (no conflict) | The 5-minute lifetime is the reviewer's alone (`internal/backend/claudecode/backend.go:409-412`). Management roles get `1h` (`:414-430`), which is what line 125 requires. |

### Missed by the first reading
1. **Section 6 and piece 8 have no mechanism, and the document contradicts itself.** Line 181 says "The model of each recurring task is already a setting" and puts the factory-flow program manager's scheduled passes on the cheaper model. Those passes come from the agent's `triggers` (`.yoyodyne/config.yaml:582-589`). `config.Triggers` has only `Every` and `On`, with no model (`internal/config/lane.go:62-69`), while `RecurringTask.Model` exists only for recurring tasks (`internal/config/recurring.go:96-102`). That leaves the agent's `model`, which line 224 says "stays … the operator's". So piece 8, "set by the development manager" (line 254), cannot be carried out under this document's own rules.
2. **The document contradicts itself on dropping sizes.** Line 123 says dropping sizes from the listing of documents left out "is a tidying step and not a condition of this section". But piece 2 (line 248) says "Drop sizes", line 108 says the listing is "by name", and line 264 tests for "the sizes dropped".
3. **The floor default is misattributed.** Section 2 (line 144) and the settings table (line 205) give a four-hour floor, but the scheduled passes it would floor already run every 45 minutes (`.yoyodyne/config.yaml:254-290`, `:588`). Also, line 230 states the 12-hour bound as carried by yoyodyne-ifd.430.92, whose title still says "a full pass at least every four hours". That is consistent as a default, but the bound has not been checked against the item's text.

### Contradictions with other designs
- Lines 205 and 216 (`full_pass_every` from 1 to 12 hours, also on program manager triggers) against `portable-agent-configuration.md:342` (1 to 24 hours, recurring tasks only). The document admits this itself (line 230).
- Line 181 (a recurring task's model) and line 216 (program manager triggers) against `program-manager.md:237`. That design says "A pass asks for the task's `model` where the trigger block names one", but the trigger block has no model key in code (`internal/config/lane.go:62-69`). So `program-manager.md` is also wrong against the code.
- Line 169 against `developer-sub-agents.md:170-179`; see that document.

## what-each-invocation-was-sent.md
Verdict: agree (mostly a plan not built).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G6-30 | NOT BUILT | CONFIRMED | `Backend.Run(ctx, request RunRequest)` (`internal/backend/backend.go:549-559`). There is no parts store or per-invocation text record. The nearest existing records are `InvocationAttempt.LaunchDigest` and `Inputs` (`internal/runstate/routing.go:349-352`), which exist only for routed runs and hold a digest and a reference string, not text. |

UNCLEAR settled:

| Finding | First reading | Second reading | Evidence |
|---|---|---|---|
| G6-31 | UNCLEAR | DIFFERS | A developer prompt is put together in at least three places: `internal/orchestrator/actions.go:107`, `pipeline.go:1708` and `:2543`, all through `developerPrompt` (`pipeline.go:11047-11055`). It carries parts the design's list (line 80) does not name: the scratch directory and checks inside the contract, an install section (`withInstallSection`), a lift section (`liftSection`), the sub-agent instruction (`subagents.go:107-116`), and a reconstruction preamble on a switched endpoint (`developerrouting.go:1138`). Line 41 anticipates finding such parts. |

### Missed by the first reading
None beyond G6-31 and the report's own points.

### Contradictions with other designs
None found.
