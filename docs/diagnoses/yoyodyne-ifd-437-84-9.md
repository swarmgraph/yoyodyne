# yoyodyne-ifd.437.84: design documents against the code, group 9: tools and message intake

One of nine reports comparing the design documents in `docs/designs/` with the code, made at the operator's request of 2026-11-21 for a review of every design against the current code. This one covers group 9, tools and message intake.

- **Read on:** 2026-11-21, between 15:40 and 15:48 PST. Main moved from `2447eeaab` to `fe2db3d37` at 15:41, a minute into the reading; that change was 22 files, mostly the team feedback repair code in `internal/runstate`.
- **Read by:** a read-only reader in the operator's assistant's session, on Sonnet 5.5. It read code and ran nothing, tests included.
- **Checked by hand:** the operator's assistant checked three findings against the code. G9-1, G9-3 and G9-4 held. Every other finding is unchecked.
- **How to use it:** this is evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Classes:** `MATCHES`, the code does what the design says; `DIFFERS`, the code does something else; `NOT BUILT`, the design calls it future or the code is absent; `UNCLEAR`, not established.

## Summary
- Documents: 3. Claims checked: 44. MATCHES 24, DIFFERS 5, NOT BUILT 12, UNCLEAR 3.
- The five mismatches that matter most:
  - G9-1 MCP design section 8 (sign_in, OAuth, Slack connector route, build pieces 10-12) has no code at all, yet "A server with a url that states no sign_in is refused at load" is listed under "What is enforced in code".
  - G9-2 tool-interface says an agent's `tools:` block (deny, bounds) narrows tools; the code has no such key and docs/configuration.md says so outright.
  - G9-3 tool-interface says the log reader's first build reads six kinds; the code reads seven (adds `intervention`), and the "development manager's list of stopped runs" is named `docket`.
  - G9-4 scheduled-message-intake: pieces 4-6 (message reader, starting work, questions) and `yoyo intake list/show/answer` are not built; only pieces 1-3 and the time format are.
  - G9-5 `internal/rolecapability/bundles.go:200,306` still says developer runs carry no MCP block, but `internal/orchestrator/mcpask.go` does it; the MCP design gives no built or not-built state for its 12 pieces.
- Consolidation proposals:
  - Split MCP section 8 (about 60 lines plus pieces 10-12, none built) into its own plan document, or mark it plainly "not built".
  - Remove the repeated "Slack: no app, user token" account from MCP section 8 and the slack-review-threads design; state it once.
  - Make tool-interface the only place for the tool list and bounds; MCP design section 2 limits should point to `internal/mcpcall/path.go` defaults rather than restate.
  - In scheduled-message-intake, move the three "Building ..." paragraphs and the nine-step revision narrative out; give one status table of pieces.

## tool-interface.md — Tools: one interface for what the harness does on a role's behalf
Verdict: partly stale
Size: 20 KB. Last changed: 2026-11-20.

### Findings
- G9-2 [DIFFERS] An agent's `tools:` block may carry `deny` and `bounds`.
  - Document: line 97: "`tools:` under an agent may carry `deny`, a list of tool ids ... and `bounds`"; line 120 slice 5 "The agent block's `tools:` narrowing key."
  - Code: `docs/configuration.md:384` says "there is no agent `tools:` narrowing key"; `internal/config/config.go` and `resolve.go` hold no deny/bounds key (only `RoleTools` add/remove in `internal/config/roles.go:42-54`, which is a role definition's, not an agent's).
  - What to change: say slice 5 is not built, and that today an agent narrows only through a role definition's `remove`.
- G9-3 [DIFFERS] The first build of `log.read` reads six kinds.
  - Document: lines 131-138: six kinds listed, "and no others".
  - Code: `internal/logread/logread.go:63` accepts pass, watch, docket, run, usage-limit, provider-outage and `intervention` (`internal/logread/reader.go:77`, `interventions.jsonl`, the operator's hand steps and messages). Also "the development manager's list of stopped runs" is the `docket` record.
  - What to change: add `intervention` as a seventh kind (and say what is redacted from it) or remove it; name `docket` as the code does. Line 169 lists "conversation records of other roles" as deferred, which `intervention` partly overlaps.
- G9-6 [DIFFERS] Protected role definitions "compose registered tools" by a named bundle and widen a bundle.
  - Document: line 98: "A named bundle under `.yoyodyne/roles/`, composing registered tools, inert until ... activation pins its digest"; line 121 "Protected role definitions, when the operator's decision admits them."
  - Code: built and stated as built: `internal/config/roles.go:25-110` loads `roles/<name>.yaml` with `extends` plus `tools: add/remove` of capabilities (`RoleTools`), digest recorded, inert until activation (`internal/runstate/roleactivation_test.go`, `internal/cli/role_test.go`).
  - What to change: mark slice 6 as built, describe the actual file shape (extends, add, remove of capabilities, not tools), and say "when the operator's decision admits them" is answered.
- G9-7 [DIFFERS] Audit events name "the redacted parameters, the bounds spent".
  - Document: line 88: "naming the tool, the redacted parameters, the bounds spent, and never the content".
  - Code: `internal/execution/tool.go:11-23` `ToolAudit` carries id, tool, role, turn, round, pass, parameters, bounds (the descriptor's bounds), requests, bytes spent, truncated. For MCP, `internal/chat/mcp.go:216-231` puts only block, tool name, id, outcome and refusal in parameters, never the arguments (file header lines 14-17 say so).
  - What to change: say the bounds recorded are the descriptor's with requests and bytes spent, and that the MCP tools record no arguments.
- G9-8 [DIFFERS] Slice 1 "the first new tool, the log reader" and the repository.search "lands as its own slice".
  - Document: lines 116, 123, 150.
  - Code: both built (`internal/toolcatalog/catalog.go:84-91`, `internal/logread`, `internal/repositoryread/search.go`). Line 150 still says its grant "discharges yoyodyne-ifd.437.9 and builds yoyodyne-ifd.430.13.13", both history. Also `repository.search` is held only by the program manager bundle (`internal/rolecapability/bundles.go:249`), as stated.
  - What to change: record slices 1 to 4 as built and drop the work-item sentence.
- MATCHES: registry is one action registry with a `Tool` descriptor (class read/speak/act, block, parameters, scope, bounds) (`internal/toolcatalog/catalog.go:34-44`; `internal/action`).
- MATCHES: three classes read/speak/act (`ToolRead`, `ToolSpeak`, `ToolAct` in catalog).
- MATCHES: grants are by role bundle in Go, with capability id as grant (`internal/rolecapability/bundles.go`; `toolcatalog.Granted` at catalog.go ~line 118).
- MATCHES: the contract tools section is generated from descriptors (`toolcatalog.Contract`, catalog.go ~line 150; MCP tools described per agent instead).
- MATCHES: audit events `tool.requested`, `tool.performed`, `tool.refused` on the conversation (`internal/execution/event.go:46-48`; `internal/chat/tool.go:69-97`), request audit failing prevents the act.
- MATCHES: conformance of every capability to a tool descriptor or a named Excluded entry (`toolcatalog.Excluded`, catalog.go ~line 108).
- MATCHES: speak and act migrations (report.file, amendment.propose, exchange.ask, agent-context.mutate, lane-report.write, service.request-restart all in catalog).
- MATCHES: log.read bounds: 6 records per reply, 48 KiB one record, 96 KiB per reply, 2 rounds (`internal/logread/logread.go:21-24` reuse `repositoryread` constants); parts with offset and remaining (`reader.go:217-310`); pass-record `task` narrowing (`logread.go:65`).
- MATCHES: log.read held by the program manager and development manager, same grant (`bundles.go:153, 250`).
- MATCHES: repository.search bounds: 200 matches, 32 KiB, 200-byte lines, 10 s (`internal/repositoryread/search.go:33-40`).
- MATCHES: MCP descriptors `mcp.read`/`mcp.act` held only via Go bundles (`capability.go:331`, `bundles.go:97,98,132,133,171,172,201,280`); typed write refuses `mcp` and `mcp_tools` (`internal/config/roleapply.go:42`); class mismatch refused at load (`internal/config/mcp.go:99-105` role table).
- UNCLEAR: "`yoyo config show` reports each agent's tools" (line 96); docs/configuration.md:376-383 says it does, I did not read the command.

### Missing from the document
- The tool catalog also has run-level and health tools not mentioned: `run.inspect`, `run.verify`, `run.recover`, `health.observe`, `health.follow` (catalog.go), plus `read-model.read` with a one-query-per-reply rule. The design's migration list names only the older reads.
- `RunTools` (catalog): a delivery run carries out only report and amendment blocks; a bundle's other tools are not described to a run.
- Generic rounds rule: the handlers for repository/log/readmodel enforce their own round bounds (`internal/chat/tool.go:78-83`), so the descriptor's bound is not the only one.

### Overlap with other documents
- Lines 110: the MCP configuration and who may set it repeat mcp-tools-through-the-harness.md lines 116-118 and 190. They agree.
- Line 98 on protected role definitions repeats configurable-workflows and portable-agent-configuration; not compared here.

### Could be cut or moved
- Revision-log reason text in the front matter is history; the "Read whole at main ..." sentences belong in commit messages.
- Line 150 work-item sentence.

## mcp-tools-through-the-harness.md — MCP tools through the harness
Verdict: partly stale (sections 1-7 built; section 8 and pieces 10-12 not built)
Size: 41 KB. Last changed: 2026-11-15.

### Findings
- G9-1 [NOT BUILT] Signing in to a remote server, `sign_in: token|oauth|connector`, `client_id`, `scopes`, `redirect_port`, PKCE listener, renewal, Slack connector route.
  - Document: lines 99, 109-110, 242-298, 342-344, 359-363, 381-387: "A server with a `url` that states no `sign_in` is refused at load", listed as enforced in code.
  - Code: absent. `internal/config/mcp.go:35-61` `MCPServer` has command, url, login, credential, credential_env, tools only. No match for `sign_in`, `oauth`, `pkce`, `client_id`, `redirect_port` in any Go file (`oauth` appears only in unrelated config files). `internal/cli/mcp.go:32-34` has only `login` (runs the declared command) and `calls`. Work items yoyodyne-ifd.435.59 and .435.60 exist in `.beads/issues.jsonl`.
  - What to change: mark section 8, pieces 10-12 and the six "enforced"/"checkable" lines about sign-in and Slack as a plan not built, or move them to their own document.
- G9-5 [DIFFERS] Developer runs: the document is silent on status, the bundle comment says not built.
  - Document: lines 164-176 (section 3) and piece 5 (line 337).
  - Code: built in `internal/orchestrator/mcpask.go:1-80` (`DeveloperCaller` at line 76; wired at `pipeline.go:621-626, 2798, 4888, 5057`) and reviewer evidence `reviewedMCPCalls` (`mcpask.go:42`, `pipeline.go:10272`). But `internal/rolecapability/bundles.go:200` and `:306-309` (`declaredAhead`) still say "a developer run carries out no MCP block".
  - What to change: add a built-state table for pieces 1-9; report to the code owner that the bundles.go comment is stale.
- G9-9 [DIFFERS] The reviewer's evidence "has its own share of the fixed 768 KiB review limit, at most 64 KiB".
  - Document: line 224.
  - Code: `review.MCPCallEvidence` exists (`pipeline.go:10272`) but I did not find the 64 KiB or 768 KiB constants in the orchestrator; class UNCLEAR rather than DIFFERS on the numbers. Looked at `grep "64 <<"` in orchestrator, only an unrelated `maxPinnedListingBytes`.
  - What to change: confirm in `internal/review` and cite the constant.
- G9-10 [UNCLEAR] The record holds "the configuration revision, and the listing's digest" and a digest change "files one note for the development manager" (lines 162, 196).
  - Looked at `internal/mcpcall/record.go:100-346` and `path.go:935` (digest handling); did not read far enough to confirm the note is filed.
- MATCHES: package `internal/mcpcall` is the only code that speaks MCP, with a test (`internal/mcpcall/onlypackage_test.go`).
- MATCHES: limits 5 calls a reply, 4 rounds a message, 32 KiB a call, 64 KiB a reply, 16 KiB arguments, 60 s (`internal/mcpcall/path.go:84-97`); developer 8 rounds, 20 calls (line 96); a scheduled caller has no round bound (comment line 66-68); `Tighten` only lowers (lines 105-122).
- MATCHES: three callers conversation, developer, scheduled (`path.go:50-58`, `ConversationCaller`, `DeveloperCaller`, `ScheduledCaller`).
- MATCHES: record is appended in two lines, asked before the call and settled after; outcomes performed, refused, failed, unknown; 256 KiB of result kept (`internal/mcpcall/record.go:5-62, 79-90`), act left without outcome reads as unknown, read as failed.
- MATCHES: `yoyo mcp calls` and `yoyo mcp login` refused to an agent shell (`internal/cli/mcp.go:72, 231`).
- MATCHES: credential stores only `keychain` or `file`, never a path (`internal/config/mcp.go:68-79`); `credential_env` for commands (line 44).
- MATCHES: role table: developer read only, reviewer and message reader none, program manager read only, other three both (`internal/config/mcp.go:92-98`; reviewer absent from table). Note: the config table does not list the message reader or reviewer, absent means neither.
- MATCHES: conversation tool events are `tool.requested` then `tool.performed` or `tool.refused` under `mcp.read`/`mcp.act` chosen by class (`internal/chat/mcp.go:14-17, 216-262`).
- MATCHES: `mcp` and `mcp_tools` refused in a role's write (`internal/config/roleapply.go:42`).
- MATCHES: redirects policy exists (`mcpcredential.CheckRedirect`, `internal/mcpcall/remote.go:61`); the "at most five" count not checked.
- MATCHES: the intake exception, a query's call records no result (`record.go` header: "except for an intake query's calls" and `IntakeQueryCaller`).
- NOT BUILT: piece 8 containment test needing yoyodyne-ifd.435.39 and piece 9 first real server (yoyodyne-ifd.435.50 open in issues.jsonl). Not checked beyond the issue list.
- MATCHES with caveat: "Every adapter launches every role with no MCP server" (line 232), `docs/configuration.md:6990` says no MCP server, no connector; I did not read the containment test.

### Missing from the document
- `docs/mcp.md` exists as the operator's setup guide; the design says the guides "gain the setup" (line 329) without saying it is built.
- Read model counts for MCP calls (`internal/readmodel/mcpcalls.go`, named in record.go header) are not described in the design beyond "counts".
- `refusedToAgentProcess` also guards `yoyo mcp calls --entry` and `--json`, which print content (`internal/cli/mcp.go:231`); the design says only that login is refused.
- The record is kept in `mcp-calls/` beside `mcp/` under the state directory, one file per process per day (`record.go:11-16, 52-55`), not "one entry" in a single file.

### Overlap with other documents
- Section 7 (lines 236-240) and scheduled-message-intake.md section 1 (line 154): both say a scheduled caller holds read tools only; agree.
- Lines 203-206 repeat scheduled-message-intake.md lines 239 and 304-308 (query call records keep no result; link and 300-byte line only by direct message). Agree after the November 7 and 21 revisions. Keep one owner.
- Line 285-298 (Slack, no app) and slack-review-threads.md; same facts, one document should own it.
- Line 110-118 and tool-interface.md line 110; agree.

### Could be cut or moved
- Lines 73-79 "What it rests on, and what was not read" (reads at commit hashes, "No code was read"): history, and now untrue since code exists.
- Lines 310-322 "Alternatives rejected" and 300-308 risks are useful but long; section 8 alternatives belong with section 8 if it is split out.
- "What is enforced in code" (348-365) and "What a reviewer can check" (367-387) restate sections 1-8; keep one.

## scheduled-message-intake.md — Scheduled message intake
Verdict: mostly a plan not built (its first three pieces and the time format are built; reading, starting, questions and the operator commands are not)
Size: 50 KB. Last changed: 2026-11-21.

### Which numbered pieces are built
- Piece 1 configuration: BUILT. `internal/config/intake.go` has queries with tool, arguments, since/until arguments, `time_format`, more_field, items, fields, senders, `anyone`, reader (only `message-reader`, line 337), rules, questions (`person` at line 113), limits with the design's defaults 20/100/5/5/4096 (lines 136-140), team-mode refusal (line 263-266), `intake` refused in a role's write (`roleapply.go:43`). The refusal of a grant over `intake` (line 164-166) was NOT found in `internal/protectedpath` or the orchestrator; see G9-12.
- Piece 2 rules document: BUILT. `internal/intakerules/read.go`, `rules.go`, `stop.go` (reading at the target branch, malformed rule refused, stop-and-report on a missing path). The contract sentence not checked.
- Piece 3 query run and record: BUILT. `internal/intake/run.go`, `record.go` (Slot, Due, Run; 10-minute `Settle`, one-minute `MinimumWindow`, `KindNarrowed`, `NextWindow`, skipped windows, failure kinds, sender check `listed` at run.go:410, report filing). Wired in `internal/orchestrator/intakequeries.go:46` and `internal/cli/intakequeries.go:52`; counts on `yoyo status` (`internal/cli/status.go:872`).
- Time format piece: BUILT (`config/intake.go`, `intake/answer.go` reads `unix_seconds` with a decimal part).
- Piece 4 message reader: NOT BUILT. No `message-reader` role in `internal/rolecapability`, no `yoyodyne-intake` block, no reader launch (grep for `yoyodyne-intake`, `RoleMessageReader` finds only `config/intake.go:125-126, 197, 337` which names the reader string).
- Piece 5 starting work: NOT BUILT. No admission from a rule, no record-number search, no text added to run evidence from intake (`branchreview.go:305` only passes `Intake.RulesPaths()` for intent assembly).
- Piece 6 questions: NOT BUILT. No `intake.test` or `intake.answer` actions (grep finds none).
- Piece 7 surfaces: PARTLY. `yoyo status` counts exist; `yoyo intake list/show/answer` do not (`internal/cli/intake.go` is only a config describer, 34 lines, no subcommands); Slack answering and spend kind not found.
- Piece 8 first real query: NOT BUILT; yoyodyne-n2t1 and yoyodyne-r3xa open per inventory.

### Findings
- G9-4 [NOT BUILT] The reader, the start path, questions and the `yoyo intake` commands.
  - Document: lines 126, 210-233, 243-255, 285-317; "What is enforced in code" lines 400-407 lists the reader launched with no tools, goal/priority from the rule, no admission while held, question reasons.
  - Code: nothing for these. Records stop at the decision-free state: `internal/intake/record.go:84-103` has kinds for reads, narrowing, skipping, failures; `internal/cli/intake.go` has no `list`, `show` or `answer`.
  - What to change: add a status line per piece; the "enforced in code" list should say it applies from piece 4 on.
- G9-11 [DIFFERS] "`yoyo intake list` and `yoyo intake show <number>` read them" and `yoyo intake answer`.
  - Document: lines 308, 310, 328.
  - Code: `internal/cli/intake.go` is 34 lines and holds only `describeIntakeQueries`; no command dispatch; `internal/cli` has `intakequeries.go` for running due queries only.
  - What to change: say these commands are planned.
- G9-12 [NOT BUILT] A work item's grant of `.yoyodyne/config.yaml` never reaches the `intake` section; the harness compares it at base and in the change.
  - Document: lines 164-166 (the newest revision, 2026-11-21).
  - Code: I looked for an `intake` comparison in `internal/protectedpath/*.go` and `internal/orchestrator/*.go` and found none; `internal/config/roleapply.go:43` only covers the typed write. Class: not found; the document itself says it waits on a piece.
  - What to change: say plainly that this comparison is not yet built.
- G9-13 [UNCLEAR] "Seven calls narrow an hour to a minute ... the query keeps the size of the window it will ask for next after every call" (line 174).
  - Looked at `intake/record.go:243` (`NextWindow`) and `run.go:59-63`; field exists, I did not trace every write after a call.
- G9-14 [UNCLEAR] "Where the first run reads only messages newer than the moment the query was first switched on" (line 178). Not traced in `run.go`.
- MATCHES: default `every` and minimum five minutes, one instance per window skipped while previous runs (`run.go:128-158` Slot and Due, not read in full).
- MATCHES: default limits and `text_bytes` (config/intake.go:136-140).
- MATCHES: team mode refused at load (`config/intake.go:263-266`).
- MATCHES: `intake` refused from a role's write (`roleapply.go:43`).
- MATCHES: sender check before any provider call and an unlisted sender's text not kept (`run.go:357-420`, `listed`).
- MATCHES: a query's call record holds no result (`record.go` header, `IntakeQueryCaller`).
- MATCHES: reader is the only value accepted (`config/intake.go:337`).
- MATCHES: `time_format` with `rfc3339` default and `unix_seconds`, decimal accepted.
- MATCHES: a query's tool must be a declared read tool (config validation; `Needs`).

### Missing from the document
- `internal/orchestrator/intakequeries.go` runs due queries inside the scheduler; the design says "a recurring task" and "no query run holds a developer slot" but does not say where it runs.
- `yoyo status` counts and `internal/intake` failure kinds, and how a query run's report is filed (`run.go:446-485`), are in code and only summarised in section 9.
- A `Hold` is passed to the runner (`cli/intakequeries.go:52`), so the intake hold is read at query time; the design says the hold applies to admission only.

### Overlap with other documents
- Lines 203-206 vs mcp-tools-through-the-harness.md 203-206: same exception; agree.
- Lines 289-310 questions to the Lead Product Manager and `human-gate` vs ownership-and-the-operator.md (the table row); not checked here; the design says the change is already there.
- Line 154 and mcp design 238: same rule; agree.
- Line 194 and 304-308 repeat the link-and-line exception three times (sections 2, 7, and MCP design). Say it once.

### Could be cut or moved
- Revision log in front matter (nine entries of 2026-11-07 to 2026-11-21) is history; the "Building ..." paragraphs (lines 166-168) and "Documents this changes" (371-378) are work-order text, now partly done.
- Lines 118-122 "What was not read".
- "What is enforced in code" (393-410) and "What a reviewer can check" (412-429) restate the sections; keep one, and mark which pieces make each true.
