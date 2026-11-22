# yoyodyne-ifd.437.84: design documents against the code, group 7: what the operator sees

One of nine reports comparing the design documents in `docs/designs/` with the code, made at the operator's request of 2026-11-21 for a review of every design against the current code. This one covers group 7, what the operator sees.

- **Read on:** 2026-11-21, between 15:40 and 15:48 PST. Main moved from `2447eeaab` to `fe2db3d37` at 15:41, a minute into the reading; that change was 22 files, mostly the team feedback repair code in `internal/runstate`.
- **Read by:** a read-only reader in the operator's assistant's session, on Sonnet 5.5. It read code and ran nothing, tests included.
- **Checked by hand:** the operator's assistant checked three findings against the code. The absence of the older-build warning and the heartbeat from the Slack design held, and so did G7-17. G7-3 is doubtful: the design's 80 percent mark and the `usage_window_degraded_percent` setting (default 90) may be two different thresholds, one for display and one for routing. Every other finding is unchecked.
- **How to use it:** this is evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Classes:** `MATCHES`, the code does what the design says; `DIFFERS`, the code does something else; `NOT BUILT`, the design calls it future or the code is absent; `UNCLEAR`, not established.

## Summary
- Documents: 5. Claims checked: 41. MATCHES 17, DIFFERS 6, NOT BUILT 13, UNCLEAR 5.
- The five mismatches that matter most:
  - G7-1: the dashboard design says a non-loopback bind is refused when configuration loads; the loader accepts it (with a keychain or file token) and only the dashboard start refuses it.
  - G7-2: the design's "team at work" section and its `team` and `work` queries are not what shipped; the page still has the older "Running now" live cards.
  - G7-3: the design says usage limits have four states with an 80 percent mark that "is not a setting"; the code has a configurable `usage_window_degraded_percent` defaulting to 90 and no `usage_limits` query.
  - G7-14: alpha-releases (alpha tags, `yoyo release notes`/`reviewed`, the marked-alpha final cut) is entirely unbuilt: all seven build items are open, and `yoyo release` is already the intake-hold command.
  - G7-17: the Slack design's outbound event list is a small fraction of what the notifier now emits, and its stale-build warning (built in `internal/slack/heartbeat.go`) is not described anywhere.
- Consolidation proposals:
  - Move the "Work item references" section (about 50 lines, the largest block) out of observability-and-dashboard.md into its own design, or into `docs/`, since the Slack sink and every surface point at it.
  - Split observability-and-dashboard.md (70 KB) into: read model + factory problems, dashboard pages, web security and acting, and a short "built vs planned" table. Roughly 40 percent of it is front-matter revision history (lines 8-92).
  - Fold slack-review-threads.md's "Documents this changes" and cross-notes into the MCP design once piece 1 is the only built part; keep the rest as a plan.
  - Replace the "what is built" prose in workflow-diagrams.md and alpha-releases.md with one status line each: both are plans with nothing built.

---

## observability-and-dashboard.md — One observability read model, and the local dashboard
Verdict: partly stale
Size: 70 KB. Last changed: 2026-11-21.

### Findings
- G7-1 [DIFFERS] A non-loopback bind is refused both when the configuration loads and when the dashboard starts.
  - Document: line 381-384: "refused in two places ... When the configuration is loaded. Loading refuses the configuration"; line 398 "refused when the configuration is loaded, naming this section".
  - Code: `internal/config/services.go:199-201` comment: "A bind outside loopback loads where its token is stored, and the dashboard refuses to listen on it"; `:460-480` the loader checks only that the bind is an IP address, and refuses a non-loopback bind only when `token` is `generated` (message tells the operator to use `keychain`/`file` "or bind 127.0.0.1"). `internal/dashboard/server.go:319-325` `LoopbackBind` runs only in `ListenOn` (line 341). The loader's message does not name the design section and does suggest another address.
  - What to change in the document: say the start-up refusal is the only full refusal; the loader refuses only a non-loopback bind paired with a generated token. Or file a work item to make the loader match the design, which is the intended behaviour.
- G7-2 [NOT BUILT] The `team` query (one row per role and slot, five states, handoffs) and the `work` query replace the live cards.
  - Document: lines 187, 328-350: "It replaces the live agent and work cards".
  - Code: `internal/dashboard/assets/shell.html:87-99` still has the `live` section "Running now" with `cards`, `slots-idle`; no `team` route in `internal/dashboard/server.go:620`; no Team type in `internal/readmodel`. The per-item route `/api/items/<id>` (server.go:828) is the nearest thing to the `work` query. The work item that designed it (yoyodyne-ifd.432.30) is closed, so this is a design that was never built, not one in progress.
  - What to change in the document: mark section "The team at work" as not built, or reduce it to what the "Running now" section shows.
- G7-3 [DIFFERS] Usage limits: four states, 80 percent "near limit", "not a setting", `usage_limits` query, one top line chosen among accounts.
  - Document: lines 182, 297-326: "80 percent used or more ... changing it is a revision of this design, not a setting".
  - Code: `internal/readmodel/usagewindows.go:32` `DefaultUsageWindowDegradedPercent = 90`; `docs/configuration.md:1061,4101` `execution.usage_window_degraded_percent` (default 90); the standing carries `usage_windows` (`internal/readmodel/standing.go:816`), not `usage_limits`. The shell has `usage-limits` list (shell.html:61). No "limit reached / near limit / fine / not recorded" states found in `usagewindows.go`.
  - What to change in the document: describe `usage_windows` as built, with the configurable degraded percent (default 90), and move the four-state, single-top-line chooser to a plan.
- G7-4 [DIFFERS] A token is "presented in a header or cookie".
  - Document: line 390: "presented in a header or cookie and never in a URL or a log".
  - Code: `internal/dashboard/server.go:656-668` "accepted from the Authorization header alone: not a query string, and not a cookie"; `:15-16` explains a cookie on 127.0.0.1 would reach every other service on the host.
  - What to change in the document: say "bearer header only; never a cookie or URL".
- G7-5 [DIFFERS] The page's eight sections are the summary, band, spend, team, pipeline, throughput, factory problems, program managers.
  - Document: line 266.
  - Code: `shell.html` sections: summary, band, spend, `live` ("Running now"), pipeline, throughput, factory, managers, plus a hidden `queues` (merge queues) and `mcp` section (shell.html:161,175); `server.go:676-682` comment says "a ninth, the merge queues". `services.dashboard.hide_summary` can drop the summary (`internal/config/services.go:233-240`).
  - What to change in the document: list the actual sections, including merge queues and MCP calls, and `hide_summary`.
- G7-6 [NOT BUILT] Dashboard acts: approve, decline, ask about it first.
  - Document: lines 400-418.
  - Code: `internal/dashboard/acts.go`, `internal/cli/dashboardacts.go` exist. I did not read them far enough to name which acts are wired, so I class this UNCLEAR rather than NOT BUILT: look at `acts.go` before editing this section.
  - What to change in the document: list acts actually wired and their grants.
- MATCHES: read model queries for backlog, delivery, accomplishment, summary, program managers; routes `/api/backlog`, `/api/delivery`, `/api/accomplishment`, `/api/summary`, `/api/spend`, `/api/throughput`, `/api/standing` (`server.go:620-648`); accomplishment rebuilt every 15 minutes (`internal/dashboard/snapshot.go:37`); four problem kinds broken/stalled/blocked/degraded (`internal/readmodel/problems.go:39-50`); `standing.capacity_blocked` in status JSON (`standing.go:854`); cited title cut at 200 bytes (`internal/readmodel/titles.go:49`); work-item matcher in `internal/workref`; `yoyo status --spend --from --to` and `--detail` (`internal/cli/status.go:154,323`); `yoyo dashboard token` (`internal/cli/dashboard.go:59`); Chart.js vendored under `internal/dashboard/assets` with a source record; shared token read-only and per-person `dashboard_token` namespace (`internal/config/operators.go:49`); pages `/backlog` and `/delivery`; `allowed_hosts` config (`services.go:219`); token file under state root (`dashboard.go:239`).
- UNCLEAR: "an answer more than 30 minutes old is shown as out of date" (only found unrelated 30-minute constants); the five team states and 5-minute working window (no team code); "stale build is a factory problem of kind degraded" (not traced); `yoyo status` prints the summary first (not traced); "no secret enters the page" (not checked).

### Missing from the document
- The merge queues section and the MCP calls section on the page (both hidden until records exist), and `services.dashboard.hide_summary`.
- `usage_window_degraded_percent` setting and the `usage_windows` read.
- The dashboard's own readiness path used by the supervisor (`internal/cli/dashboardchild.go`) is only described as a goal.

### Overlap with other documents
- Usage-limit states and top line: also in provider-usage-and-cost.md (not in this group); not compared.
- Slack sink "shows the read model's rendered text" (line 222) and slack-reporting-design.md line 7 area: agree in spirit; slack-reporting-design does not mention the rendered-text rule at all.
- `v1-harness-design.md` links this design; the dashboard now takes acts, which that document's older text may still deny. Not checked.

### Could be cut or moved
- Lines 8-92 revision history (about 85 lines of front matter, mostly "approved amendment ..." reasons) and lines 93-163 approval entries; the harness keeps these, the architect does not need to rewrite them.
- "Alternatives rejected" and "What a reviewer can check" paragraphs under each section (about 100 lines total) could move to one appendix.
- The 12-row examples table for work item references (lines 226-240).
- Web-security "Alternatives rejected" paragraph (line 396) repeats the first three paragraphs of the section.

---

## slack-reporting-design.md — Slack reporting: events out, directives in, one thread per topic
Verdict: partly stale
Size: 30 KB. Last changed: 2026-11-20.

### Findings
- G7-17 [DIFFERS] The reportable set is "the milestones the conversation's activity line already reports", about a dozen kinds.
  - Document: line 173: "run claimed and started; checks passed or failed; the reviewer's verdict; promotion; publication, queued merge, and merge; a run parking ... a blocker recorded; a report filed; a proposal raised; an ask-exchange turn, and an exchange closing".
  - Code: `internal/notify/envelope.go:31-239` defines more than 45 kinds, among them backlog.admitted/decomposed/attributed/reprioritized, tracker.block-refused, proposed-work.approved/declined, work.handed-off/picked-up/carried-out, cap.crossed/granted, paths.refused, promotion.race-lost, merge.dropped, merge.waiting-on-target, landing.green/red/unverified, run.ended, usage-limit.exhausted, model.substituted, amendment.decided, directive.recorded/resolved/carried-out/refused/withdrawn, question.heard, intake.held/released/escalated, hold.placed.
  - What to change in the document: replace the list in line 173 with a pointer to `internal/notify/envelope.go` as the one list, and keep only the rule that decides who speaks.
- G7-18 [DIFFERS] No mention of the warning that a service is running an older build; the design says which direct-message states ship belongs to "the operator guide".
  - Document: lines 129-132 (only "Degraded" and "Advisory-once" classes, no stale-build text).
  - Code: `internal/slack/heartbeat.go:60-72` `DefaultStaleBuildThreshold = 6` landings ("deliberately not a configuration key"); `:569-632` `staleBuild`: says nothing while the supervisor is building or moving by design; warns when a landing stays unbuilt past `DefaultBuildGrace` (20 minutes, `:82`), a move is stuck, or there is no supervisor; note severity below 6 landings behind, warning at 6 or more when no supervisor record; owner and remedy come from `ownership.ResolveStaleBuild`. The notification is built by `notify.FromResident` (`heartbeat.go:565`). The heartbeat itself (`DefaultHeartbeat = time.Hour`, `:57`) is also not in the design.
  - What to change in the document: add one section for the heartbeat and the stale-build warning (when it speaks, when it stays silent, severity, who moves it), or point at the operator guide where it is.
- G7-19 [DIFFERS] The operator allow-list lives in the Slack section of `.yoyodyne`.
  - Document: lines 202, 207: "the list lives in `.yoyodyne` ... the Slack section — enabled flag, channel, the operator allow-list".
  - Code: `internal/config/slack_test.go:55-80`: the allow-list moved to the top-level `operators` mapping, and a `slack.operators` key is refused at load with a message saying where it went.
  - What to change in the document: point to the `operators` mapping and drop `operators` from the Slack section list.
- G7-20 [DIFFERS] Slack reply grammar: `resolve <directive-id> <how>`.
  - Document: line 191.
  - Code: `internal/slack/inbound.go:927-948`: `resolve <how it was settled>` is the ordinary form; a leading word that is a valid directive reference is taken as the id, otherwise the thread's own wait is settled.
  - What to change in the document: say the id is optional.
- G7-21 [NOT BUILT -> built] Design says the sink verb is "to be settled at implementation; I propose `yoyo slack`" and that 68.4 inbound is "designed now".
  - Document: lines 88, 185.
  - Code: `internal/cli/cli.go:127` `case "slack"`; `internal/slack/inbound.go` exists; yoyodyne-ifd.68.2/.3/.4 closed (yoyodyne-ifd.68.25 is blocked). So the status section (lines 76-78) describes built work as pending.
  - What to change in the document: state the verb is `yoyo slack`, inbound is built, and 68.25 is the only waiting item.
- G7-22 [UNCLEAR] "Roles answering in threads: what has to be built first" (lines 103-127).
  - Document: lines 116-121.
  - Code: yoyodyne-ifd.68.25 is blocked in `.beads/issues.jsonl`; I did not trace whether a thread question now reaches the architect's conversation. `internal/slack/conversation.go` exists. Check this before editing the section.
- MATCHES: Socket Mode (`internal/slack/socketmode.go`); thread map durable per product, owned by the sink (`internal/slack/state.go:182-233`, `ThreadMap`); the sink never in an agent environment (not retraced; code exists, see test names); directive kinds and in-thread acknowledgments recorded/resolved/carried out/refused (`envelope.go:212-216` has the four plus withdrawn); severities from reports (`internal/report`); tokens `SLACK_BOT_TOKEN`/`SLACK_APP_TOKEN` in the sink environment (`internal/slack/envfile.go:27`); DMs to operators (`internal/slack/dm.go`).
- UNCLEAR: "cursor initialises at now for a stream with none" (`internal/slack/catchup.go`, not read); "body over the size limit is truncated with a marker" (not found).

### Missing from the document
- The heartbeat, stale-build warning (G7-18), and the capacity, stall, provider-outage, improvements, interventions and claims feeds in `internal/slack/` (`capacity.go`, `stall.go`, `provideroutage.go`, `improvements.go`, `interventions.go`, `claims.go`): a reader cannot learn what the channel carries at night from this document.
- The `Resident` notification and who-moves wording from `ownership.ResolveStaleBuild`.
- Steer map and stranger refusal state files (`state.go:265-381`).

### Overlap with other documents
- observability-and-dashboard.md line 192 says the model serves "per-item status for thread reactions, directive lifecycle marks, item titles for thread openers"; slack-reporting-design.md never mentions reactions or lifecycle marks. Not in conflict, but only one side is written.
- slack-reporting-design.md line 202 operator list vs `scheduled-message-intake.md` (not read in full): both refer to who may act; the top-level `operators` mapping is the one source.
- Direct-message classes (lines 129-132) and the dashboard's attention line (observability line 196): both use the ownership resolver; consistent.

### Could be cut or moved
- The "Status" section's epic and child-item narrative (lines 76-78) and the "What each child builds" list (lines 210-214), now history.
- The "Already built, according to the development manager's inspections at commits 4ffa688948c6 and e3d93b7cdde7" paragraph (line 116): incident history, not current behaviour.

---

## slack-review-threads.md — Slack review threads for pull requests a person merges
Verdict: mostly a plan not built (piece 1, the configuration, is built)
Size: 16 KB. Last changed: 2026-11-15.

### Findings
- G7-23 [MATCHES] `review_threads` configuration: fields `server`, `post_tool`, `read_tool`, `channel`, `reviewers`, `check_every`; default 15 minutes, minimum 5 minutes, maximum 24 hours (`internal/config/reviewthreads.go:22-62`); the typed write refuses the section (header comment, same file).
- G7-24 [NOT BUILT] The harness posts the fixed request, keeps the thread on the publication record, reads replies, files a note-severity report to the Lead Product Manager, posts the closing line.
  - Document: lines 63-104, 160-166 (build pieces 2 to 4).
  - Code: absent. `review-threads` / `review_threads` appear only in `internal/config/*` and `internal/doctor/mcp.go`; nothing in `internal/orchestrator`, `internal/runstate`, `internal/mcpcall` or `internal/readmodel` references it. The Slack route piece it waits on (yoyodyne-ifd.435.60) is still open.
  - What to change in the document: put a one-line status at the top: "piece 1 built; pieces 2 to 5 not built".
- G7-25 [NOT BUILT] `yoyo status` and the dashboard show each pull request with a thread, reply counts, and the thread link (line 92).
  - Code: no status or read-model code mentions review threads (same search as G7-24).
- G7-26 [NOT BUILT] A reply becomes a repair round (section 8, piece 5). Waits on yoyodyne-ifd.429.120, which the document says is the dependency.
- UNCLEAR: "Watching the pull request itself is built (yoyodyne-ifd.429.100)" (line 24; not traced).
- MATCHES: refusal rules for missing server, wrong tool class, empty reviewers, out-of-range interval at load (`reviewthreads.go:70+`, `internal/config/reviewthreads_test.go`); `yoyo doctor` check exists (`internal/doctor/mcp.go`).

### Missing from the document
- Nothing missing about the built part. The design says it did not read the code that opens a pull request; whoever builds piece 2 must reread it.

### Overlap with other documents
- mcp-tools-through-the-harness.md sections 4, 7 and 8 (named at lines 52, 123, 172): this document says it asks the MCP design to carry two changes "in that design's next revision"; I did not check whether it did. Agreement unknown.
- scheduled-message-intake.md: line 124 states intake "uses a message reader"; not compared.

### Could be cut or moved
- "Alternatives rejected" (lines 151-158) repeats what sections 3 to 7 already say.
- "What it rests on, and what was not read" (lines 30-34): history of how the design was written.

---

## workflow-diagrams.md — Drawing a project's workflows on request
Verdict: mostly a plan not built
Size: 12 KB. Last changed: 2026-11-20.

### Findings
- G7-27 [NOT BUILT] `yoyo workflow draw [id] [--format text|svg|mermaid] [--bare]`.
  - Document: line 70.
  - Code: `internal/workflow` has only `compile.go`, `definition.go`, `digest.go`, `execute.go`, `loader.go`, `validate.go`; no drawing code. No `draw` or Mermaid match in `internal/cli` or `internal/workflow`. Work item yoyodyne-y06u is open.
- G7-28 [NOT BUILT] Three fixed descriptions `intake.workflow.yaml`, `queue.workflow.yaml`, `landed.workflow.yaml` with `describes: code`, `code:`, `next:`, `follows:`.
  - Document: lines 85-105.
  - Code: no `*.workflow.yaml` file under `internal/`; `internal/workflow/loader.go` has no description loader.
- G7-29 [NOT BUILT] A dashboard page of workflows drawn as SVG with text outline (line 72) and a read-model workflows reading (line 74): nothing in `internal/dashboard` or `internal/readmodel` (page list: `server.go:612-616` has `/`, `/backlog`, `/delivery`).
- G7-30 [MATCHES] `yoyo workflow show --effective` prints the compiled definition the design builds on (line 41): flag found in `internal/cli` tests (`role_test.go`, `intake_test.go`); the surfaces-project-one-read-model rule is cited correctly. Only checked by flag existence.

### Missing from the document
- Nothing the code does in this area, because the area is not built. The design should say it is a plan and name which package holds the code for each description once the build piece reads it (it admits this at line 89).

### Overlap with other documents
- configurable-workflows.md (not in this group): the document defers conversion of intake, queue and landing to that design's conversion order (line 115). I did not check that order.
- observability-and-dashboard.md line 266: the dashboard section list does not mention a workflows page; consistent only because neither is built.

### Could be cut or moved
- The text-format example (lines 47-61) is useful; the "Alternatives rejected" list (lines 111-117) restates the rules at lines 31-35.

---

## alpha-releases.md — Releases go out first as alphas
Verdict: mostly a plan not built
Size: 11 KB. Last changed: 2026-11-20.

### Findings
- G7-14 [NOT BUILT] Alpha tags `vX.Y.Z-alpha.N`; all three scripts accept exactly one `-alpha.N` suffix; previous release skips alpha tags; alpha pages are prereleases.
  - Document: lines 28-35, 102: "The tag pattern in all three scripts widens to accept ...".
  - Code: `scripts/cut-release.sh:100` `tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'` (final tags only); `grep alpha scripts/*.sh .github/workflows/*.yml` finds nothing; `.github/workflows/release.yml` has no prerelease flag. Work item yoyodyne-ifd.436.17 is open.
- G7-15 [NOT BUILT] The harness redraws next release notes after each landing into the state directory; `yoyo release notes` prints them; the dashboard delivery page shows them.
  - Document: lines 44-51, 104.
  - Code: `internal/cli/cli.go:103` routes `yoyo release` to `releaseIntake`, whose job is lifting the intake hold (`internal/cli/release.go:45`, `release_test.go:19`). No `notes` subcommand. Work item yoyodyne-ifd.436.19 open. Note: this document uses the word "release" for a command that already means "lift the hold"; the build must pick another verb or a subcommand split.
- G7-16 [NOT BUILT] The "Try these by hand" list, the three fixed notes (`On the release review list:`, `Off the release review list:`, `To try by hand:`), `yoyo release reviewed vX.Y.Z alpha N`, and the dashboard button; the final tag cut at the marked alpha's commit, holding no slot.
  - Document: lines 57-98, 102-107.
  - Code: `internal/releasecut/releasecut.go:1-60` cuts a decided release at the commit origin's default branch holds, with no operator mark; no `reviewed` verb found; `human-gate` reason is not used by it. Items yoyodyne-ifd.436.18 to .23 are all open.
- G7-31 [MATCHES] Who may cut a release: the harness's cut under `approvals.releases` (`internal/releasecut/releasecut.go:1-5`; `docs/configuration.md:1065`: `approvals.releases` default `human`, `automatic` has the supervisor's maintenance pass cut). The cut refuses stale or missing notes (releasecut.go:19-22) and pauses with the operator's pause (releasecut.go:55-58). The document's statement that the final tag is cut "as decided releases are cut today, under the same setting" holds.
- G7-32 [DIFFERS] "`docs/releases/vX.Y.Z.md`, named for the final tag. Its alphas share it." and "the committed file ... is committed with the tag".
  - Document: lines 22, 39, 53.
  - Code: `docs/releases/README.md` records that `v0.7.0.md` lands after, not at, the commit its tag names, and that whoever cuts the tag adds the notes to the page by hand with `scripts/release-body.sh`. The alpha design's step "the final tag names the commit of the alpha he tried" is consistent, but it silently changes the tag-names-the-notes-commit rule that README states and yoyodyne-ifd.437.82 (open) is amending in v1-harness-design.md.
  - What to change in the document: add a sentence on how the notes commit and the tagged commit relate under this design; link ifd.437.82.
- MATCHES: three scripts exist (`scripts/release-notes.sh`, `cut-release.sh`, `release-body.sh`); readiness section markers in the notes (`cut-release.sh`); 125,000-character release page limit is the README's (`docs/releases/README.md`, line 14).
- UNCLEAR: the claim "v0.6.0 was drafted on September 23 and never refreshed" (line 22); README confirms v0.6.0 was never tagged and v0.7.0 covers it, so the history is right but "never refreshed" is only partly the cause.

### Missing from the document
- That `docs/releases/README.md` already has an exception rule for a release whose notes land after its tag (v0.7.0); the design should say whether alphas remove that exception.
- How a cut handles `docs/releases/vX.Y.Z.md` with the stamped readiness section when several alphas are cut: each alpha would restamp it; the design says only that the stamp "stays as today".
- The build pieces name `yoyo release`-prefixed commands without noting the existing `yoyo release` intake lift.

### Overlap with other documents
- v1-harness-design.md links this document (inventory) and holds the release tag ruling (the releasecut.go header cites it as "The release tag"); I did not read that section. The pending item yoyodyne-ifd.437.82 says it must be amended to "a release is tagged at the commit its notes name, not at main's tip"; this design says the final tag is at the marked alpha's commit. The two must be reconciled.
- operating-rules / configuration.md line 5612: `approvals.releases` description; agrees.

### Could be cut or moved
- The first paragraph's story about v0.6.0 and the November 9 request (line 22) is history; the rest of the document is a clean plan with seven build pieces.
- Section 8 (the releases guide's changes) restates build piece 7; keep one.
