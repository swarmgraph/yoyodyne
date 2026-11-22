# yoyodyne-ifd.437.84: second reading of the design documents against the code, group 9: tools and message intake

A second, adversarial reading of the report `yoyodyne-ifd-437-84-9.md` beside this file. It re-read every finding there that differs or is not built against the code, tried to settle the unclear ones, checked a sample of the matches, and looked for what the first reading missed. **Where the two files disagree, this one stands.**

- **Read on:** 2026-11-21, in the evening, between 18:33 and 18:43 PST, with main at `848d86190`. The code had moved since the first reading; each finding is judged against the code as it was at this reading.
- **Read by:** a read-only reader on Opus 5.5 in the operator's assistant's session. It read code and ran nothing, tests included.
- **Checked by hand:** nothing in this file was checked again by hand.
- **How to use it:** evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Second-reading classes:** `CONFIRMED`, the first finding is right as written; `CORRECTED`, its gist is right and a detail is wrong; `WRONG`, the design and the code agree or the finding misread one of them; a class in the first reading's own words where an unclear finding was settled.

Read 2026-11-21, about 18:20-18:50 PST, at main `848d86190`. The only intake landing since noon is piece 3 (`1a404d4a7`, yoyodyne-ifd.435.52, closed 13:19 PST). Nothing else in `internal/intake`, `internal/intakerules`, `internal/config/intake.go`, `internal/mcpcall`, `internal/rolecapability` or these three designs has changed since then.

## Summary
- Findings re-read: 15. CONFIRMED 8, CORRECTED 2, WRONG 5, CANNOT TELL 0. (G9-1 to G9-9, G9-11, G9-12, plus four unnumbered claims: "piece 8 containment NOT BUILT", "time format BUILT", "Hold means the intake hold is read at query time", and the time_format line in the MATCHES list.)
- Unclear findings settled: 6 of 6 (G9-9, G9-10, G9-13, G9-14, the `yoyo config show` tools claim, and the "no MCP server at launch" caveat). All six are MATCHES.
- Verdicts changed: none of the three labels change. The detail behind two of them is wrong. MCP: pieces 1-9 are built, 8 and 9 included. Intake: the time format piece is not built.
- **Do not act on these as written:**
  - G9-9: the 64 KiB share is built. `internal/review/mcpcalls.go:27` has `MaxMCPCallEvidenceBytes = 64 << 10`, inside `MaxReviewInputBytes = 768 << 10` (`internal/review/reviewer.go:28`), and the cut is declared. The design matches.
  - "Piece 8 containment NOT BUILT": wrong. `internal/backend/conformance/mcp_test.go:301` exists, landed 2026-11-13 (`152c77c73`). MCP piece 9 (yoyodyne-ifd.435.50) was closed as done on 2026-11-20.
  - "Time format piece BUILT": wrong. There is no `time_format` key anywhere in the Go code. The config decoder is strict (`internal/config/document.go:452`), so a query that writes `time_format:` fails to load. Since and until are always RFC 3339 (`internal/intake/run.go:304-305`).
  - "A Hold is passed, so the intake hold is read at query time": wrong. `Runner.Hold` is the operator's pause (`mcpcall.Hold` returns `runstate.OperatorHold`, `internal/mcpcall/path.go:414-416`; `internal/intake/run.go:87,209`). The intake hold does not stop a query, which is what the design says (lines 188, 250) and what `docs/configuration.md:8207` says.
  - G9-5: the design matches the code. The only stale text is a code comment in `internal/rolecapability/bundles.go:200,306-309`. That is a code fix, not a design revision.
- **Most important things missed:**
  - The MCP design (line 116) and tool-interface (line 110) say "a developer's change cannot touch `.yoyodyne/`". In the code a work item may grant `.yoyodyne/`; only `.yoyodyne/roles/` is beyond every grant (`internal/protectedpath/roles.go:8-10,21-22,71-75`; v1-harness-design.md:586). The intake design (line 164) says the opposite of the MCP design. Nothing compares the `mcp` section at base and in the change, so a granted change could rewrite `mcp`.
  - The MCP design says an agent may tighten its MCP limits (line 148). `chat.Options.MCPLimits` (`internal/chat/chat.go:441-443`) is set by no code outside tests, and no configuration key feeds it.
  - Intake piece 2 is a library that nothing calls: `internal/intakerules` has no caller outside its own package. Two other design claims have no code: the refusal at load of a rules path the Lead Product Manager's writes cannot reach (design line 277), and the rules commit recorded on each message (line 281).

## Which numbered pieces of scheduled-message-intake.md are built now

| Piece | State | Evidence |
|---|---|---|
| 1 Configuration | Built, with one part missing | `internal/config/intake.go:258-390` has every load refusal the piece names; `roleapply.go:43` covers the typed write; `cli/intake.go` covers `yoyo config show`. Missing: refusing a grant over `intake` (design 164-166, G9-12). The refusal of a rules path out of the Lead Product Manager's reach (design 277) is also absent: `IntakeRulesPathProblem`, `config/intake.go:396-411`, checks only how the path is written. |
| 2 Rules document | Library only | `internal/intakerules/read.go`, `rules.go` and `stop.go` read rules at the target branch and refuse a malformed rule or an inactive goal (yoyodyne-ifd.435.58, closed 11-20). Nothing outside the package calls it, so no query run reads rules or records their commit. The contract sentence is not in `artifact-contract.md`, and the design says so (line 377). |
| 3 Query run and record | Built | Landed 13:19 PST today. `internal/intake/run.go`, `record.go` and `answer.go`; wired in `cli/intakequeries.go` and `orchestrator/intakequeries.go`. |
| Time format (unnumbered, lines 156-160, 168) | Not built | See above. `answer.go:150-172` accepts RFC 3339 or epoch seconds whatever the query says, and keeps the fraction instead of dropping it. |
| 4 Message reader | Not built | yoyodyne-ifd.435.53 open. No `message-reader` role, no `yoyodyne-intake` block. |
| 5 Starting work | Not built | yoyodyne-ifd.435.54 open. |
| 6 Questions | Not built | yoyodyne-ifd.435.55 open. No `intake.test` or `intake.answer`. |
| 7 Surfaces | Counts only | `yoyo status` counts (`readmodel/intakequeries.go`, `cli/status.go`). No `yoyo intake list`, `show` or `answer`; no answer from Slack; no spend kind. yoyodyne-ifd.435.56 open. |
| 8 First real query | Not built | yoyodyne-ifd.435.57 open. |

## tool-interface.md
Verdict: agree (partly stale)

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G9-2 | DIFFERS | CONFIRMED | `docs/configuration.md:383` (not 384): "there is no agent `tools:` narrowing key". |
| G9-3 | DIFFERS | CONFIRMED | `internal/logread/logread.go:55,64` accepts seven kinds; `reader.go:77-78` maps `intervention` to `interventions.jsonl`. |
| G9-6 | DIFFERS | CONFIRMED | `internal/config/roles.go:34-55`. A role definition is extends plus add/remove of capability ids. The design calls a capability id the tool's grant (line 81), so "composing registered tools" is not wrong. The stale words are "when the operator's decision admits them" (line 121) and "When it opens is ...: after behavioral parity" (line 98). |
| G9-7 | DIFFERS | CORRECTED | The general audit matches the design. `ToolAudit` (`internal/execution/tool.go:11-23`) carries the redacted parameters, the descriptor bounds, requests, bytes and truncation. The one real difference is that MCP events record no arguments (`internal/chat/mcp.go:14-17`). |
| G9-8 | DIFFERS | CONFIRMED | `internal/toolcatalog/catalog.go:85,88`; line 150 is work-item history. |
| UNCLEAR config show | UNCLEAR | MATCHES | `internal/cli/cli.go:718-720,755,872-877` reports tools from the registry. |

Spot-checked MATCHES (log.read bounds 6/48 KiB/96 KiB at `repositoryread.go:80-92`; search bounds 200/32 KiB/200 bytes/10 s at `search.go:33-39`): all hold.

### Missed by the first reading
- Line 110: "a developer's change cannot touch `.yoyodyne/`" is false. A grant can reach it (`internal/protectedpath/roles.go:21-22`; v1-harness-design.md:586).
- Line 140 says any request may carry an offset, and one without starts at the beginning. In the code an offset needs a cursor (`logread.go:73-75`), and every read needs a cursor or a time window of at most 24 hours (`logread.go:76-83`). The design states neither rule.
- Line 142 says log.read never reads another agent's conversation records. The `intervention` kind reads the operator's hand steps and messages (`docs/configuration.md:387`), which line 169 lists as deferred ("conversation records of other roles, where most hand steps arrive as messages").

### Contradictions with other designs
- tool-interface.md:110 and mcp-tools-through-the-harness.md:116 ("cannot touch `.yoyodyne/`") against scheduled-message-intake.md:164 and v1-harness-design.md:586 (an item can grant `.yoyodyne/` and `.yoyodyne/config.yaml`).
- tool-interface.md:97, mcp-tools-through-the-harness.md:148 and portable-agent-configuration.md:350 ("`tools: deny` in that agent's block") all rely on an agent `tools:` key. No such key exists, and `docs/configuration.md:383` says so. This is not a disagreement between designs; three designs rest on one unbuilt key.

## mcp-tools-through-the-harness.md
Verdict: agree in substance. It should read "sections 1-7 built (pieces 1-9, the containment test and first real server included); section 8 and pieces 10-12 a plan not built". The first reading counted pieces 8 and 9 as not built.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G9-1 | NOT BUILT | CONFIRMED | No `sign_in`, `redirect_port` or PKCE in any Go file. Lines 110, 359 and 381 claim it is enforced. yoyodyne-ifd.435.60 is `blocked`. |
| G9-5 | DIFFERS | CORRECTED | The design matches the code: `DeveloperCaller` is used at `internal/orchestrator/mcpask.go:76` and `reviewedMCPCalls` at `:47`. What is stale is the comment in `internal/rolecapability/bundles.go:200` and the `declaredAhead` entry at `:306-309`. Fix the code, and add a status line to the design. |
| G9-9 | DIFFERS | WRONG | `internal/review/mcpcalls.go:24-27` has 64 KiB as a share of `MaxReviewInputBytes` (768 KiB, `reviewer.go:28`). The cut is declared (`mcpcalls.go:269-297`). MATCHES. |
| "Piece 8 NOT BUILT" | NOT BUILT | WRONG | `internal/backend/conformance/mcp_test.go:301` launches every role on every built-in adapter with servers configured, credentials stored, and `ENABLE_CLAUDEAI_MCP_SERVERS` set (`:306`), and checks the arguments, environment, directory and home. `:691` covers the critical report. yoyodyne-ifd.435.39 closed 11-11. |
| "Piece 9 NOT BUILT" | NOT BUILT | WRONG | yoyodyne-ifd.435.50 closed 2026-11-20: one read from a conversation and one from a developer run, both recorded. |
| G9-10 | UNCLEAR | MATCHES | The record keeps `ConfigRevision` and `ListingDigest` (`internal/mcpcall/record.go:127-131`). `noteListing` (`path.go:898-931`) files one `SeverityNote` for the development manager when the digest differs from the one at the server's latest recorded call. It is noted once, because the batch records the new digest. |
| caveat "no MCP server at launch" | MATCHES w/ caveat | MATCHES | The containment test above. |

Spot-checked MATCHES: limits (`path.go:84-98`), redirects at most 5 (`config/mcp.go:159`, `mcpcredential.go:232-244`), 500-byte description (`describe.go:37`). All hold. One correction: plain `yoyo mcp calls` is not refused to an agent; only `--entry` and `--json` are (`internal/cli/mcp.go:231`).

### Missed by the first reading
- Line 116: ".yoyodyne cannot be touched" is false (see tool-interface). There is also no comparison of the `mcp` section at base and in the change, unlike what the intake design asks for `intake`.
- Line 148, "An agent's `tools:` block may tighten these": not wired. `Caller.Tightened` is fed only by `chat.Options.MCPLimits` (`internal/chat/mcp.go:148,167`; `chat.go:441-443`), which no code outside tests sets.
- Line 192, "one entry for each call": the code appends an asked line and a settled line to per-process daily files under `mcp-calls/` (`record.go`). The first reader listed this under Missing; it deserves a fix in the design.

### Contradictions with other designs
- Line 116 against scheduled-message-intake.md:164 and v1-harness-design.md:586 (above).

## scheduled-message-intake.md
Verdict: agree ("mostly a plan not built"). What is built is piece 1 without the grant comparison or the rules-path reach check, piece 2 as a library nothing calls, and piece 3. The time format piece is not built.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G9-4 | NOT BUILT | CONFIRMED | Pieces 4-6 and the `yoyo intake` commands are absent. The first reading's status list is wrong on the time format; see the table above. |
| G9-11 | DIFFERS | CONFIRMED | `internal/cli/intake.go` holds only `describeIntakeQueries`. |
| G9-12 | NOT BUILT | CONFIRMED | No `intake` comparison in `internal/protectedpath` or `internal/orchestrator`. The role-definition diff gate (`protectedpath/roles.go`) shows the pattern it would follow. |
| "Time format BUILT" | BUILT | WRONG | No `time_format` key exists, and the strict decoder (`config/document.go:452`) refuses one. Times are RFC 3339 only (`run.go:25-26,304-305`; `docs/configuration.md:8215-8216`). `sent_at` takes either format whatever is configured, and keeps the fraction (`answer.go:164-170`). The only tracker item is the architect's ruling, yoyodyne-ifd.437.79; no build item exists. |
| "Hold read at query time" (Missing) | — | WRONG | It is the operator's pause (`run.go:87,209`; `mcpcall/path.go:414-416`). The intake hold does not stop queries, as the design says. |
| G9-13 | UNCLEAR | MATCHES | `NextWindow` is written after every call that settles: narrowed (`run.go:269`), fit (`:285`), skipped (`:341-342`). It is read back at `record.go:336,341`, and a run starts from it (`run.go:232`). Six halvings take an hour below a minute, so the seventh call is at one minute. Not in the design: windows are capped at 7 days (`run.go:63`), and a query with no saved size starts at its `every` (`:233-235`). |
| G9-14 | UNCLEAR | MATCHES | The first run only records `switched_on` (`run.go:216-225`), and `ReadUpTo` is set to that moment (`record.go:306-311`). |
| Edited message (line 204) | not listed | MATCHES | `run.go:369-376` writes a `returned` line with the new digest, applied as `LatestDigest` (`record.go:368-370`), and the message is not read again. |

### Missed by the first reading
- Line 277: a rules path the Lead Product Manager's writes cannot reach is not refused at load. `config/intake.go:396-411` checks only the path's form.
- Line 281: the run reads no rules at the target branch and records no rules commit per message. `internal/intakerules` has no caller (`grep intakerules` outside the package finds only a comment, `config/intake.go:86`).
- Line 170: "The schedule is a recurring task under configurable workflows". The code runs queries in goroutines beside the session's pulls (`internal/orchestrator/intakequeries.go:46-62`), with an in-process lock (`run.go:97-100,167-172`), not as a recurring task.

### Contradictions with other designs
- Line 164 (a grant can reach `.yoyodyne/config.yaml` or `.yoyodyne/`) against mcp-tools-through-the-harness.md:116 and tool-interface.md:110 ("cannot touch `.yoyodyne/`").
- Line 378 says it was not checked whether portable agent configuration names `intake`. It does (`portable-agent-configuration.md:365`). That is a stale line, not a contradiction.
