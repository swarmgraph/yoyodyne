# yoyodyne-ifd.437.84: second reading of the design documents against the code, group 7: what the operator sees

A second, adversarial reading of the report `yoyodyne-ifd-437-84-7.md` beside this file. It re-read every finding there that differs or is not built against the code, tried to settle the unclear ones, checked a sample of the matches, and looked for what the first reading missed. **Where the two files disagree, this one stands.**

- **Read on:** 2026-11-21, in the evening, between 18:33 and 18:43 PST, with main at `848d86190`. The code had moved since the first reading; each finding is judged against the code as it was at this reading.
- **Read by:** a read-only reader on Opus 5.5 in the operator's assistant's session. It read code and ran nothing, tests included.
- **Checked by hand:** nothing in this file was checked again by hand.
- **How to use it:** evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Second-reading classes:** `CONFIRMED`, the first finding is right as written; `CORRECTED`, its gist is right and a detail is wrong; `WRONG`, the design and the code agree or the finding misread one of them; a class in the first reading's own words where an unclear finding was settled.

Read on 2026-11-21 against main at `848d86190`, read-only (Read and grep only; nothing run).

## Summary
- Findings re-read: 20 (every DIFFERS and NOT BUILT, G7-21 counted as DIFFERS). CONFIRMED 12, CORRECTED 7, WRONG 1, CANNOT TELL 0.
- Unclear findings settled: 10 of 11 (only "no secret enters the page" is still unclear).
- Verdicts changed: none. Two need a note: workflow-diagrams rests on a command that does not exist (see G7-30), and slack-reporting-design's "Roles answering in threads" section describes as rejected the shape the code actually uses.
- The findings the architect should not act on as written:
  - **G7-3**: the 80 and 90 are two different thresholds, and neither one controls routing. The design's 80 percent is a display state ("near limit") that the design says is never a problem. The code's 90 percent (`execution.usage_window_degraded_percent`) decides when a window is listed as a degraded factory problem. The real conflict is that the code makes a window at 90 percent or more a problem, and the design says a near-limit window never is.
  - **G7-18**: WRONG. The design deliberately leaves the list of direct-message states to the operator guide (`slack-reporting-design.md:131`), and the guide does cover the heartbeat and the older-build warning (`docs/slack/setup.md:142`, `:650-665`). Do not add a section to the design.
  - **G7-17**: the design calls its list the *initial* set and says it can be extended (`:173`, `:175`), so the code's larger set does not contradict it. There are 75 kinds, through `envelope.go:435`, not "more than 45" through line 239.
  - **G7-32**: yoyodyne-ifd.437.82 is no longer pending in substance. `v1-harness-design.md` was already amended (`:152`, `:634-640`) and now hands the alpha case to alpha-releases.md, which does not answer it.
  - **G7-30** (classed MATCHES): WRONG. There is no `yoyo workflow` command. `--effective` is a flag of `yoyo config show` (`internal/cli/cli.go:662-666`).
- The most important things the first reader missed:
  - The Slack sink opens the Lead Product Manager's conversation itself and runs the provider turn inside the sink process (`internal/cli/slack.go:736-761`, `openChat`). The design rejects exactly that (`slack-reporting-design.md:96`, `:101`, `:125`, `:127`). Only the Lead Product Manager is reachable this way; no other role is.
  - The design says the per-person dashboard token is written by `yoyo operator token <name>` (`observability-and-dashboard.md:404`). No `operator` command exists (`cli.go:38-182`). The dashboard creates the token file itself (`internal/cli/dashboard.go:811-814`).
  - A notes file that serves several alphas cannot be found by `release-body.sh`. It opens `docs/releases/$tag.md` (`scripts/release-body.sh:68`) and has no tag pattern at all, so an alpha's page would carry only the install preamble. The design's "the tag pattern in all three scripts" (`alpha-releases.md:33`) is wrong for this script.
  - The release cut still tags the tip of origin's default branch (`internal/releasecut/releasecut.go:10-11`). The amended v1 design says to tag the commit the notes name.

## observability-and-dashboard.md
Verdict: agree (partly stale)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G7-1 | DIFFERS | CORRECTED | The gist holds: the loader refuses a non-loopback bind only when paired with `token: generated` (`internal/config/services.go:479-482`). The bind check itself is at `:466-472`, not 460-480. The loader's message for a bad bind even suggests "the address of the interface another device reaches this machine on" (`:471`). The start-up refusal also breaks the design (`:385`): it names no section and suggests an address, "set it to 127.0.0.1, or another loopback address" (`internal/dashboard/server.go:319-325`). It runs in `ListenOn` (`:341`) and `ListenSharedOn` (`:365`). The design rejects keeping a setting that allows a non-loopback bind (`:396`), and the code still keeps it (`services.go:194-200`). |
| G7-2 | NOT BUILT | CONFIRMED | `shell.html:87-99` "Running now". No team or work route (`server.go:612-650`). `internal/config/team.go` is unrelated. The design item yoyodyne-ifd.432.30 is closed, and no build item exists in `.beads/issues.jsonl`. |
| G7-3 | DIFFERS | CORRECTED | **Two different thresholds; neither is used for routing.** The design's 80 percent (`:306-310`) marks the "near limit" display state, and `:322`/`:326` say a near limit is never a factory problem. The code's 90 percent (`internal/config/config.go:552-556`, `:801-804`; `readmodel/usagewindows.go:32`) is "past which the dashboard's factory problems say the window is degraded capacity. It changes what is shown and nothing the harness does". A window at or over it (`usagewindows.go:132`) becomes a degraded problem (`:250-268`; `standing.go:1489`; `problems.go:116`). Nothing outside the read model, status and Slack reads usage windows (only `runstate/usagewindow.go`, `backend/`, and `cli/provider.go`, `slack.go` and `status.go` reference them), so no routing threshold exists. The four states, the `usage_limits` query and the top-line chooser are not built; `usage_windows` is what ships (`standing.go:816`). The thing to fix: the design forbids exactly what the code does, listing a high but unrefused window as a problem. |
| G7-4 | DIFFERS | CONFIRMED | `server.go:656-668`; header `:13-20`. |
| G7-5 | DIFFERS | CONFIRMED | `shell.html` sections `live`, `queues` (hidden), `mcp` (hidden). The `server.go:676-682` comment counts eight plus the merge queues and omits MCP. `hide_summary` is at `services.go:233-240`. |
| G7-14 | NOT BUILT | (see alpha-releases) | |

Unclear claims, settled:
- G7-6, dashboard acts: **DIFFERS (mostly built)**. Approve, decline and ask are wired at `/api/acts/decide` and `/api/acts/ask` (`internal/dashboard/acts.go:49-50`, `:263-315`) for three kinds: amendment, document and proposal (`readmodel/decisions.go:53-55`). Grants: own-intent for documents and proposals; decide-amendments for amendments, plus own-intent where the Lead Product Manager owns the document (`acts.go:353-370`). Two departures from the design. Settling an owed step under `direct-work` (`:412`) is not offered. The grant for a document is fixed in code as own-intent rather than read from the grant the command-line approve checks (`:412`). Binding a person who lacks own-intent is refused at load (`config/operators.go:376-384`), which matches `:406`.
- "An answer more than 30 minutes old is shown as out of date" (`:295`): **MATCHES approximately**. A snapshot is stale once its age passes 2 × (interval + build time) (`dashboard/snapshot.go:66-71`, `:195`). With the 15-minute interval (`:37`) that is a little over 30 minutes, not exactly 30.
- The five team states and the 5-minute window: **NOT BUILT** (no team code; see G7-2).
- "A part on a stale build is a factory problem of kind degraded" (`:182`): **DIFFERS**. The read model has no stale-build problem. A part whose build cannot read the configuration is a *broken* problem (`readmodel/problems.go:110-113`, `AttentionConfigMismatch`). The "behind what has landed" warning exists only in the Slack heartbeat (`internal/slack/heartbeat.go:582-633`).
- `yoyo status` prints the summary first: **MATCHES** (`internal/cli/status.go:434-440`, `:493-498`).
- "No secret enters the page": **STILL UNCLEAR** (not traced field by field).

Matches re-checked that are not matches: none among the ones checked (`titles.go:49`, `snapshot.go:37`, `standing.go:854`, `operators.go:49`, `status.go:153-155` all hold).

### Missed by the first reading
- `:404` "written by `yoyo operator token <name>`": there is no `operator` command (`internal/cli/cli.go:38-182`). The dashboard creates `dashboard.<name>.token` itself where it is missing (`internal/cli/dashboard.go:811-814`).
- `:390` "The token is generated each start": a shared token may also come from the keychain or a file (`services.go:224-231`). The design reads as if `generated` were the only source.
- `:322`/`:326` against the code: a window at 90 percent or more that the provider has not refused is a degraded problem in code (G7-3). This is the actual design/code conflict behind G7-3.

### Contradictions with other designs
- `observability-and-dashboard.md:322` says a near limit is never a problem; the configuration reference (`docs/configuration.md:1061`, `:4101`) documents the 90 percent degraded threshold that makes one. That is a guide, not a design, but the architect should settle it in one place.
- `observability-and-dashboard.md:222` says the Slack sink "shows the read model's rendered text"; `slack-reporting-design.md:98` says harness facts are rendered by per-role voice templates. They agree only if the templates count as read-model output. Neither document says so.

## slack-reporting-design.md
Verdict: agree (partly stale). "Roles answering in threads" (`:103-127`) is the most stale part.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G7-17 | DIFFERS | CORRECTED | 75 `Kind` constants, `internal/notify/envelope.go:31-435` (for example `catch-up.digest` at `:428` and `log.line-skipped` at `:435`). The design calls its list "the initial reportable set" and says the set is "versioned … and extensible" (`:173`, `:175`), so the list is incomplete rather than contradicted. The proposed fix (point at `envelope.go`) still stands. |
| G7-18 | DIFFERS | WRONG | `:131` deliberately puts the list of shipped direct-message states in the operator guide. The guide covers the older-build warning (`docs/slack/setup.md:142-144`, pointing at `docs/reporting.md`) and the heartbeat (`:650-665`, `:707`). The code details in the finding are right (`heartbeat.go:58`, `:72`, `:82`, `:565`, `:582`, `:633`), but nothing is missing from the design. |
| G7-19 | DIFFERS | CONFIRMED | The refusal is in the code at `internal/config/document.go:494` ("slack.operators has moved to the top-level operators mapping"), not only in the test. |
| G7-20 | DIFFERS | CONFIRMED | `internal/slack/inbound.go:931-945`; the hint at `:589`. |
| G7-21 | NOT BUILT → built | CONFIRMED | `cli.go:127`. Items 68.2, 68.3 and 68.4 are closed and 68.25 is blocked (`.beads/issues.jsonl`). |

Unclear claims, settled:
- G7-22, roles answering in threads: **DIFFERS**. Built: a thread question reaches the *Lead Product Manager's* conversation only (`internal/slack/conversation.go:3-60`). The sink process opens that conversation itself and runs the turn (`internal/cli/slack.go:709-761`, `openChat(ctx, domain.RoleProductManager, …)`). The design rejects "the sink starting the role itself" (`:125`) and says "the reporter never invokes a provider" (`:96`, `:101`). The agent child still gets an environment without the tokens. The second "not built" prerequisite (`:121`) appears built: token-stripping tests exist for both providers (`internal/backend/claudecode/backend_test.go`, `internal/backend/codex/backend_test.go`, `internal/execution/environment_test.go` all name `SLACK_BOT_TOKEN`). Routing to roles other than the Lead Product Manager is not built.
- Cursor starts at now (`:92`): **MATCHES in substance**. A once-written durable watermark, `Since` (`internal/slack/state.go:739-750`), marks when a sink was first pointed at the product. Difference: the first pass reports milestones already in flight (`state.go:752-757`), which `:92` ("history is never replayed") does not mention.
- A body over the size limit is truncated with a marker: **MATCHES** (`internal/slack/sink.go:1344-1354`, marker naming `refs.Record()`).

Matches re-checked that are not matches:
- The four in-thread acknowledgment kinds (`:193-198`): the code has five, adding `directive.withdrawn` (`envelope.go:212-216`). The first reading listed this under MATCHES.

### Missed by the first reading
- The Lead Product Manager's path is built in the shape the design rejects (see G7-22): `:125` against `internal/cli/slack.go:736-761`.
- `:202` says authorization is the operator list alone. Talking to the Lead Product Manager from Slack also requires the `direct-work` grant and a bound Slack member id (`internal/slack/conversation.go:22-26`).
- `:116` "Already built, according to the development manager's inspections …" is history, and it is outrun by `conversation.go`.

### Contradictions with other designs
- `slack-reporting-design.md:96`/`:125` (the sink never runs an agent or a provider) against `pm-single-pane` and chat-client designs: not checked. Against the code, see above.

## slack-review-threads.md
Verdict: agree (mostly a plan; piece 1 built)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G7-24 | NOT BUILT | CORRECTED | Nothing references review threads outside the configuration (`internal/config/*`), the doctor (`internal/doctor/*`) and the configuration display (`internal/cli/cli.go:765`). Corrections: the Slack route yoyodyne-ifd.435.60 is **blocked**, not open. The build items exist and are open: pieces 2 to 5 are yoyodyne-ifd.429.215 to 429.218, and piece 1 (429.214) is closed. |
| G7-25 | NOT BUILT | CONFIRMED | Same search. |
| G7-26 | NOT BUILT | CORRECTED | The dependency yoyodyne-ifd.429.120 is now **closed**, so `:28` and `:112` ("once … is built") are stale. Piece 5 (429.218) is open and waits only on piece 3. |

Unclear claims, settled:
- "Watching the pull request itself is built (yoyodyne-ifd.429.100)" (`:24`): **MATCHES**. 429.100 is closed; `internal/orchestrator/teampullrequestfeedback.go` and `internal/publish/feedback.go` exist.

Matches re-checked: G7-23 holds (`internal/config/reviewthreads.go:45-52`).

### Missed by the first reading
- Nothing more of weight.

### Contradictions with other designs
- `slack-review-threads.md:123`, `:172` say the MCP design's section 7 names this design as the exception to "read tools only" for a scheduled caller, and its section 4 names the thread reads as a call record that keeps no result. The MCP design does not do either: `mcp-tools-through-the-harness.md:238` still says "A scheduled caller may hold `read` tools only, until a design says otherwise", and `:203` lists "Two exceptions", both from intake. That design has no mention of review threads at all.

## workflow-diagrams.md
Verdict: agree (mostly a plan not built). Note: the command it builds on does not exist.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G7-27 | NOT BUILT | CONFIRMED | `internal/workflow` has no drawing code; nothing in `internal/` mentions Mermaid; yoyodyne-y06u is open. |
| G7-28 | NOT BUILT | CONFIRMED | No `*.workflow.yaml` anywhere in the repository. |
| G7-29 | NOT BUILT | CONFIRMED | Pages are `/`, `/backlog` and `/delivery` only (`server.go:612-617`). |

Matches re-checked that are not matches:
- **G7-30: WRONG.** There is no `yoyo workflow` command (`internal/cli/cli.go:38-182`), so `yoyo workflow show --effective` (`:41`) does not exist. `--effective` belongs to `yoyo config show` (`cli.go:662-666`) and prints configuration, not a compiled workflow definition. The first reader found the flag by name only.

### Missed by the first reading
- `:41`: the input is described as "the same definition `yoyo workflow show --effective` prints". No such command exists, so build piece 2 (`:124`, `yoyo workflow draw`) also has to add the `workflow` verb.

### Contradictions with other designs
- `configurable-workflows.md:109` names "`yoyo workflow validate` and `show --effective`" as the inspection surface. The two designs agree with each other, and both disagree with the code (no `workflow` verb).

## alpha-releases.md
Verdict: agree (mostly a plan not built)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G7-14 | NOT BUILT | CORRECTED | Not built: `scripts/cut-release.sh:100` and `scripts/release-notes.sh:82` accept final tags only; no script or workflow mentions alpha. Correction: `release-body.sh` has **no** tag pattern (it takes the tag as given and opens `docs/releases/$tag.md`, `:68`), so "all three scripts" (`:33`, `:102`) is wrong for that script. yoyodyne-ifd.436.17 is open. |
| G7-15 | NOT BUILT | CONFIRMED | `cli.go:103-104` sends `release` to `releaseIntake` (`internal/cli/release.go:45`); yoyodyne-ifd.436.19 is open. |
| G7-16 | NOT BUILT | CONFIRMED | `internal/releasecut/releasecut.go:10-11`; yoyodyne-ifd.436.18, 436.20, 436.21, 436.22 and 436.23 are open. |
| G7-32 | DIFFERS | CORRECTED | The gist holds against `docs/releases/README.md:6-7`, `:29-41`. Correction: yoyodyne-ifd.437.82 has in substance already landed in the design. `v1-harness-design.md:152` (amended 2026-11-21) and `:634-640` say a release is tagged "on the commit the notes name, not on the tip of `main`", and "Where a release had alphas, the alpha releases design says which commit its notes name". alpha-releases.md does not say that: it says the final tag goes on the marked alpha's commit (`:98`, `:106`) and never which commit the notes name. The tracker copy still shows 437.82 as open. |

Unclear claims, settled:
- v0.6.0 "never refreshed" (`:22`): **MATCHES as history**. `docs/releases/README.md:23-27` confirms the tag was never cut; it is history and could be cut from the design.

Matches re-checked that are not matches:
- G7-31's citations: the operator's pause is at `releasecut.go:36` and `:164-167`, not `:55-58` (imports). The refusal of stale notes is at `:13-16`. The claim itself holds.

### Missed by the first reading
- `:39` (one notes file per version, `vX.Y.Z.md`) against `scripts/release-body.sh:68` (`$tag.md`): an alpha tag's page would find no notes and publish the preamble alone (`release-body.sh:21-23`). Build piece 1 (`:102`) does not mention this lookup.
- `:106` (the final tag goes on the marked alpha's commit) against the code today: `releasecut.go:10-11` cuts at whatever origin's default branch holds, with no named commit. Neither the v1 ruling nor this design is built.
- `:50`, `:104` `yoyo release notes` and `yoyo release reviewed`: `yoyo release` already lifts the intake hold and takes positional arguments (`release.go:45-50`). The build must choose subcommands that cannot be mistaken for a release target.

### Contradictions with other designs
- `v1-harness-design.md:640` hands alpha-releases.md the question of which commit the notes name; `alpha-releases.md:39`, `:98`, `:106` answer a different question (which commit is tagged). The two need one sentence that joins them.
- `v1-harness-design.md:634` (tag the commit the notes name) against `internal/releasecut/releasecut.go:10-11` (tag origin's default-branch tip): this is code against a design outside the group, but it bears on alpha-releases build piece 5.
