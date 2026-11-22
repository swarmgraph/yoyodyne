# yoyodyne-ifd.437.84: the design documents against the code, summary of nine reports and a list of ordering rules

## In short

- **Only 6 of the 53 design documents are current.** 28 are partly stale and 19 are mostly plans that are not built. None is superseded outright.
- **Every document is marked "status: active", so the label says nothing.** Plans and shipped behaviour are mixed in the same sections, and several documents list unbuilt things under "what is enforced in code".
- **The reports hold 82 findings where the code does something else and 76 where what the document describes is not built**, each with the document line and the code line. 55 claims could not be settled.
- **39 rules in the code decide order or choice of work, and only 5 are in the operating rules.** 11 are in the code alone, including the order among waiting written work, which is consistent with two seat picks on 2026-11-21.
- **The readers proposed about 40 consolidations.** The largest are splitting four oversized designs into "built" and "plan", folding six small designs into their neighbours, and moving incident history out.
- **This is evidence for the architect, not a ruling.** The documents are hers to change. The operator's assistant checked 30 findings by hand and 29 held.

## What was done

The operator asked on 2026-11-21 for "a thorough review of all of the design documents against the current state of the code. Make sure they're up to date, consolidate them, etc." The architect reads about 192 KB of the repository per turn and cannot read 19.6 MB of code, so the operator's assistant did the reading from its own session.

- **Mechanical check first.** Every document was checked for references to files, code names and work items that no longer exist. It found little: 17 dead paths in 10 documents, one dead code name, six unknown item ids. The drift is in what the documents say the system does.
- **Nine readers, one per group of related documents**, each on Sonnet 5.5 and read-only. For each document a reader picked the 10 to 20 most load-bearing concrete claims and classed each as matches, differs, not built or unclear. A "differs" or "not built" had to carry the document's line and the code's file and line.
- **A tenth reader listed the ordering rules in the code** and whether the operating rules record each.
- **Read between 15:40 and 15:48 PST on 2026-11-21.** Main moved from `2447eeaab` to `fe2db3d37` at 15:41, a minute into the reading; that change was 22 files, mostly the team feedback repair code in `internal/runstate`.
- **Sample check.** Three findings from each of the ten reports were checked against the code by hand: 29 of 30 held. The doubtful one is named under "What to treat with care".
- **Size of the work.** The ten readers used about 1.77 million tokens in all and took between three and eight minutes each.

The ten full reports are the files beside this one in `docs/diagnoses/` whose names begin `yoyodyne-ifd-437-84-`: nine named `designs-against-the-code-<group>-...` and one named `ordering-rules-in-the-code`.

## Verdict for each document

| Group | Document | Verdict |
|---|---|---|
| Core architecture | v1-harness-design | partly stale |
| | orchestrator-boundaries | mostly a plan (its five new packages do not exist) |
| | one-owner-for-each-guarantee | partly stale |
| | artifact-contract | partly stale |
| | fewer-lifecycle-states | mostly a plan |
| | README (the index) | current |
| Run recovery | stopped-run-recovery | partly stale |
| | fresh-factory-health-and-preserved-run-recovery | partly stale |
| | forward-progress | mostly a plan |
| | recoverable-and-terminal-failures | partly stale |
| | follow-ups | mostly a plan |
| Integration and merging | integration-through-a-merge-queue | partly stale |
| | merge-queue-pace | partly stale |
| | integrating-to-a-local-branch | partly stale (section 5 is a plan) |
| | working-alongside-other-contributors | partly stale (tracker sharing is a plan) |
| | document-landing-route | partly stale |
| | unpublished-designs-and-admitted-work | mostly a plan |
| Checks and sandbox | checks-on-ci | partly stale |
| | choosing-checks-by-change | mostly a plan |
| | heavy-check-capacity | partly stale |
| | repeated-checks | mostly a plan |
| | project-dependencies-in-runs | current |
| | repository-confined-writes | partly stale |
| Roles and management | program-manager | partly stale |
| | management-and-supervision | partly stale |
| | management-loop-protocol | mostly a plan |
| | architect-loop | mostly a plan |
| | ownership-and-the-operator | partly stale |
| | unmet-goal-review | mostly a plan |
| | needs-design-gate | current |
| | placing-an-item-first | current (still reads as a plan) |
| | one-label-per-item | mostly a plan |
| Execution and cost | claude-execution-and-account-routing | partly stale |
| | developer-sub-agents | current |
| | provider-adapters-and-endpoints | partly stale |
| | provider-usage-and-cost | partly stale (built, but says nothing is) |
| | reading-published-prices | mostly a plan |
| | reducing-what-the-factory-costs | mostly a plan (one piece built) |
| | what-each-invocation-was-sent | mostly a plan |
| What the operator sees | observability-and-dashboard | partly stale |
| | slack-reporting-design | partly stale |
| | slack-review-threads | mostly a plan |
| | workflow-diagrams | mostly a plan |
| | alpha-releases | mostly a plan |
| Machine and configuration | machine-home | partly stale |
| | portable-agent-configuration | partly stale |
| | pinned-outside-files | current |
| | project-groups | mostly a plan |
| | tracker-store-size | partly stale |
| | configurable-workflows | partly stale |
| Tools and intake | tool-interface | partly stale |
| | mcp-tools-through-the-harness | partly stale |
| | scheduled-message-intake | mostly a plan (three pieces built) |

## The mismatches that matter most

Finding ids point into the nine group reports; rule ids (R1 to R39) point into the ordering rules report.

**Stated as enforced, and not built**
- The MCP design lists "a server with a url that states no sign_in is refused at load" under what is enforced in code. No Go file mentions `sign_in` (G9-1).
- The scheduled message intake design lists its reader, start path and questions as enforced. Only pieces 1 to 3 exist, and `yoyo intake list`, `show` and `answer` do not (G9-4).
- The one-label-per-item rule is not in the code at all (G5-1).
- The heavy-check design names a setting, `heavy_checks_total`, that does not exist, and gives counts this repository does not use (G4-12, G4-14).

**The code does something else**
- The merge queue ages an entry at eight hours, and the design says four. Document batching and turn-taking are not built (G3-1, G3-2).
- A stop with an unrecognised reason is designed as outside the work and uncharged. The code treats it as about the work and charges an attempt (G2-1).
- Three designs give the provider boundary thirty minutes. The code still uses two hours (G2-2).
- A missing provider program is designed to reach the operator as `install`. The code still returns `credential` (G5-4).
- The dashboard design says a non-loopback bind is refused when the config loads. Only the dashboard's start refuses it (G7-1).
- The race check on main is designed to cover only macOS-only code. The config still runs the whole of `make race` (G1-26).
- Repository-confined writes are specified on `openat2` and `renameat2`. The code uses Go's `os.Root` and plain renames (G4-18).

**Built, and missing from the design**
- The Slack design names about a dozen event kinds and the code has many more. The warning about a service running an older build, and the hourly heartbeat, are in no design (G7 report).
- The routing design still calls the slot-pair fields and routing records future work. They are built (G6-2).

**Whole documents that are plans**
- alpha-releases, workflow-diagrams, project-groups, reading-published-prices, what-each-invocation-was-sent, unmet-goal-review, management-loop-protocol and unpublished-designs-and-admitted-work have little or no code behind them.

## Consolidation proposals

These are the readers' proposals, for the architect to take or leave.

**Split built from plan**
- integration-through-a-merge-queue (59 KB): the queue's mechanics, which are built; the order and GitHub's required checks; and a short not-built list.
- claude-execution-and-account-routing (43 KB): a short "what is built" form, with the history dropped.
- integrating-to-a-local-branch: move section 5, the batch route, to a design of its own.
- working-alongside-other-contributors: move section 7, tracker sharing, to a design of its own.
- mcp-tools-through-the-harness: move section 8, sign-in, to its own plan or mark it not built.
- repository-confined-writes: what the code does, and a clearly marked target.

**Fold together**
- merge-queue-pace into the merge queue design.
- repeated-checks into choosing-checks-by-change.
- management-loop-protocol into management-and-supervision.
- needs-design-gate, placing-an-item-first and one-label-per-item into one design on how an item is held, ordered and labelled.
- Guarantee 6 of one-owner-for-each-guarantee into fewer-lifecycle-states, leaving one sentence behind.
- The race-check ruling out of one-owner-for-each-guarantee, to sit beside choosing-checks-by-change.

**State once**
- The "what failed" identity and the third-act rule are restated in full in three recovery designs.
- The rule for the harness acting when the development manager is unavailable is restated in three.
- "Brake" and "hold on new work" name the same thing in different documents.

**Cut or move**
- v1-harness-design: the self-hosting sequence and implementation choices are history, and its git model and recovery sections restate the merge queue design.
- "Code this makes wrong", "Not read" and "Evidence" sections, which describe what the author read on a past day.
- Incident dates and ruling-by-ruling history, which belong in work items and commit messages.
- The word-for-word persona rules in the fresh-factory design, which are a second copy of the persona files.

**One change that would help every document**
- A status line per section, or per numbered piece, saying built or not built. Today all 53 read as if shipped.

## Ordering rules in the code

The tenth reader found 39 rules that decide order or choice of work.

- **11 are in the code alone.** No operating rule, guide or design states them.
- **23 are described somewhere but not in the operating rules.**
- **5 are in the operating rules.**

The ones a decider would most want to know:

- **Priority 0 jumps a labelled seat unless the seat is dedicated** (R15). This is the rule the operator asked to remove on 2026-11-21.
- **Order among written work waiting for a seat** (R3, R4). Recorded decisions, such as a decided repair, take free seats first, ordered by priority. Paused runs whose checks have cleared come after them, oldest pause first, with no regard to priority. This is code only. It is consistent with what happened on 2026-11-21, when a repair of a P1 change and then the longest-waiting change were picked ahead of a P0 change.
- **A continuation that comes back without going on is not retried for 15 minutes** (R9). Code only.
- **The merge queue's limits** (R10, R11): three rebuilds when main moves, two unreadable reviews before a hand-back, five minutes of an unreadable forge before falling back to local checks. Code only.
- **The merge queue's eight-hour age rule** (R12), where the design says four.
- **The 15-minute drain and three-hour interval between build moves** (R28). Only the three hours is a recorded ruling.

## What to treat with care

- **One sampled finding is doubtful.** The dashboard report says the design's "80 percent" mark conflicts with the `usage_window_degraded_percent` setting, default 90. These may be two different thresholds, one for display and one for routing.
- **55 claims are unclear.** Each says what was looked at. The readers also named what they did not finish, mostly the internals of overlaps between groups and a few traces through the largest files.
- **The counts are by finding, not by claim.** Matching claims are bundled several to a line, so the 93 "matches" lines stand for roughly 290 claims.
- **Nothing here was run.** The readers read code and did not execute it or its tests.
