# internal/chat — code map

Read this before paging through `chat.go` (about 4,500 lines) or `tracker.go`
(about 2,800). It names symbols, not line numbers; find them with
`rg -n 'func \(s \*Session\) takeTurn' internal/chat`.

## What the package does

It runs a conversation between a person (or a recurring pass) and one
management role: the Lead Product Manager, the development manager, the
architect, a program manager. A conversation is not a run: no worktree, no
checks, no reviewer. It shares the backend boundary, the event stream and a
durable record (`runstate.Conversation`, through the `Store` interface), so a
later process continues where the last one stopped.

One message from the person becomes one or more provider turns. The role's
answer is split into prose and typed blocks (tracker actions, proposals,
concerns, document writes, memory writes, research and repository reads,
reports, an ask to another role). The harness checks each block against the
role's authority, carries out what is allowed, and feeds the results back to
the role in a follow-up turn until the role stops asking.

The same `Session.Send` serves an interactive console (`yoyo chat`), a single
message from the command line, and every recurring role pass
(`roleConversation.Wake` in `internal/cli/recurring.go`).

## Map of chat.go, in file order

1. **Package comment and imports.**
2. **Interfaces a session is built from.** `Backend`, `Store`, `Hold` (this
   process's claim on the conversation), `Tracker`.
3. **`Options`** — everything one conversation is opened with: `Role`,
   `Backend`, `Store`, `Hold`, `Tracker`, `Documents`, `Memories`, `Reports`,
   `UsageLimits`, `ProviderOutages`, `Model`, `Effort`, the failover fields,
   `Persona`, `Remit`, `Lane`, `Timeout`, `SessionBudgetBytes`, `Fresh`.
4. **`Session`** — one open conversation: `options`, `state` (the durable
   record), `pass` (set by `ForPass` for a recurring pass), pending
   proposals, concerns, writes, carried results, spend, effort evidence.
   Also `proposalRecord`, `concernRecord`, and `activeRun` (a run started
   with `/work` from the console).
5. **`Evidence`, `Reply`** — what a turn reports back. `Reply.AdmittedWork`.
6. **Opening and the hold.** `Open`, `adopt`, `reload`, `releaseHold`,
   `retakeHold`, `Resumed`, `TurnCostUSD`, `Evidence`, `requestedModel`,
   `servedByAlternate`.
7. **`Send`** — one message, start to finish (see below).
8. **`takeTurn`** — one provider invocation with its waits, failover,
   compaction and recording (see below). `recordOperatorMessage`.
9. **Splitting a reply.** `parsedReply`, `splitReply`, `collectReply`,
   `appendProblem`, `appendProse`, `carryResults`.
10. **Proposals and concerns.** `Proposals`, `Concerns`, `Answer`,
    `recordConcerns`, `Approve`, `createFromProposal`, `Reject`,
    `recordProposals`, `proposalGate`, `admit`, `admissible`, `admitOne`,
    `emit` (appends a harness event to the log).
11. **The console loop.** `Converse` → `converse` (one line in, one answer
    out; lines starting with `/` go to `command` in `steer.go`), `speak`
    (calls `Send` with progress on screen), `reportSpend`, `awaitOperator`
    (puts the conversation down at the prompt), `takeConversationBack`,
    `AwaitConversation`, `ask`, `choose`, `await`.
12. **Printing what a turn did.** `reportTrackerActions`, `reportResearch`,
    `reportRepositoryReads`, `reportPicture`, `reportEvaluation`,
    `reportAdmitted`.
13. **Deciding in the console.** `raise` / `answerTo` (concerns), `decide`,
    `undecidedCards`, `applyDecisions`, `decideOne`.
14. **Deciding from one message.** `Decided`, `Decide`, `answerFromMessage`,
    `refuseUnnamed`, `pendingCards`.
15. **The prompt and the record.** `turnPrompt` (product context on the
    first turn only; harness notices and carried results every turn),
    `renderNotices`, `notice`, `record` (saves the conversation),
    `undecidedProposals`, `unansweredConcerns`.
16. **`Options` helpers.** `providers`, `endpoint`, `validate`, `identity`,
    `sleep`, `askRounds`, `refreshAfterLandings`, `timeout`, `stopGrace`.
17. **Role contracts.** `relevantGoalsClause`, `productManagerContract`
    (long Go string constants).
18. **Effort evidence.** `invocationEffort`, `lastEffort`,
    `lastReportedEffort`, `lastEffortWasReported`.

## Questions developer runs keep asking

### Where is the turn loop?
`Session.Send`. It validates the message, measures the picture
(`measurePicture`, `freshness.go`), builds the prompt with `turnPrompt`, then
loops: `takeTurn` → `splitReply` → `collectReply` → `authorize` (`role.go`)
→ `refuseWrites` / `recordWrites` (`document.go`) → `recordConcerns` →
`verifyProposalGoals` / `verifyProposalConditions` /
`verifyProposalReferences` → `recordProposals` → `admit` →
`performTrackerActions` (`tracker.go`, at most `maxTrackerRounds`) →
`performResearch` → `performRepositoryReads` → `recordEvaluation` →
`performMemoryWrites` → `writeLaneReport` → `performRestartRequest` →
`conductAsk` (`exchange.go`). Whatever came back is carried into the next
turn's prompt; the loop ends when a round asks for nothing more.

### What does one provider turn do?
`takeTurn`: checks `heldByOperator` (`hold.go`); builds the system prompt
with `SystemPrompt` + `WithRemit` (`role.go`) and the user prompt with
`renderMemories`; records the operator's message; checks `compactionDue`
and, if due, `saveBeforeCompaction` then `compact` (`compact.go`); builds the
`backend.RunRequest` (`resumableSession`, `invocationEffort`,
`options.timeout`); picks the provider through `failoverPolicy` /
`meteredFailover` (`usagelimit.go`) and `rebuildForOwnEndpoint`
(`rebuild.go`). `requestBounded` (`requestsize.go`) measures the selected
endpoint's actual prompt, shortens replayed history to leave a margin below its
input limit, then fits the briefing (`fitBriefing`, over
`contextbundle.FitProductContext`, whose `GiveWayOrder` is the order sections
give way), refuses with `TurnTooLarge` where what never gives way is too large,
and retries a size refusal once, including the memory-save turn.
Capacity waits retain the effective prompt and spent size retry; an intervening
turn rebuilds from the latest record with the reduced history allowance.
A failed reconstruction returns its error and keeps the event position after
any recorded session replacement. It then invokes in a loop that
handles other refusals (below), and
finally `measureSession` and `record`.

### What happens when the provider refuses for a usage limit or outage?
Inside `takeTurn`'s loop: `noteUsageLimit` then `waitOutUsageLimit`
(`usagelimit.go`), or `noteProviderOutage` then `waitOutProviderOutage`
(`providerwait.go`). Both wait through `waitForProvider`, which calls
`releaseHold` before sleeping and `retakeHold` + `reload` after, so another
process (a recurring pass, the console) can use the conversation during the
wait. `Options.waitsOutUsageLimits` decides whether to wait at all;
`UsageLimitPause` bounds it. A turn on another model or account goes through
`rebuildForAlternate` / `replaceSession` (`rebuild.go`).

### What happens when a recurring pass finds the conversation in use?
The hold is taken when the pass opens the session (`roleConversation.opener`
in `internal/cli/recurring.go`). If another process holds it,
`runstate.ErrConversationHeld` comes back, and `Trigger.run` in
`internal/orchestrator/recurring.go` records a miss
(`runstate.MissConversationHeld`) instead of a failure. A recurring pass is
marked with `ForPass` (`lanereport.go`); `Send` uses `s.pass` to pick the
message size limit.

### How does a pass that forgot its closing report get asked again?
`roleConversation.Wake` in `internal/cli/recurring.go`: if the reply has no
report block it sends `sweep.ReportRequest()` once (`RetryReport` /
`ReportRetried`). Repeated pass failures are written into the next pass's
message by `Trigger.PassFailures`; who they go to is
`ownership.ResolvePassFailure`.

### Where are compaction and memory?
`compact.go`: `compactionDue`, `saveBeforeCompaction` (one turn for the
role to save memories first, built by `memorySaveRequest` and
`compactionSavePrompt`), `compact`, `measureSession`, `sessionBudget`.
`memory.go`: `extractMemoryWrites`, `performMemoryWrites`,
`applyMemoryWrite`, `renderMemories`, `keepsMemory`.

### How is a document write proposed, refused, approved?
`document.go`. A reply's writes are checked by `refuseWrites`; a refusal is
rendered by `renderDocumentRefusal` and handed back to the role with
`carryResults` in `Send`. Accepted writes become `PendingWrite` through
`recordWrites`. `documentpublication.go` confirms automatic-policy writes,
saves the complete
candidate, and calls the ordinary delivery pipeline before any prompt. Its
`PublishDocuments` also resumes saved handoffs and carries failures to the owner,
with three returns allowed per document per conversation. It confirms nothing
where the publisher cannot integrate automatically, puts a saved handoff the
publisher refuses outright back to the operator, and keeps one whose run could
not start for the next message; none of these stops the operator's message. Other policies retain
the person's decision with `ApproveWrite`, `DeclineWrite`,
`DecideWrites` or, in the console, `decideWrites`. `artifactFiling` says
which homes the role may write.

### What may a role do, and where is that enforced?
`role.go`: `Authority`, `AuthorityFor`, `buildAuthorities` (from
`internal/rolecapability`), `Session.authorize`, `SystemPrompt`.
Per-action checks: `TrackerAction.Validate` and `applyTrackerAction` /
`carryOutTrackerAction` (`tracker.go`); lanes in `lane.go`
(`refuseOutsideLane`); asks to other roles in `exchange.go`
(`refuseUnauthorizedAsk`); development manager decisions in `triage.go`
(`carryOutTriage`) and `triagestop.go`.

### Which model, effort and account does a turn use?
`Options.Model`, `Options.Effort`, `Options.AccountAlias`; the request gets
`invocationEffort`. What was actually served is read back by `Evidence`,
`lastEffort`, `servedByAlternate`. Failover: `failoverPolicy`,
`meteredFailover`, `servingEndpoint`, `alternateSession`; when it moves a turn
(capacity, a pinned version the provider has not got, or a configured
provider's executable that cannot run) is `internal/modelfailover`.

### Where do tracker results reach the role's next turn?
`carryResults` stores them on the record (`PendingTrackerResults`) so a
later process can deliver them; `turnPrompt` includes them; `takeTurn`
removes them from the record once a turn has delivered them.

### How are a work item's full text and its runs shown to a role?
`readActionTarget` and `renderWorkItemEvidence` (`tracker.go`);
`inherited.go` adds the block the item is under through a waiting parent, and
`itemruns.go` the runs made for the item.

## Sibling files

| File | What it holds |
|---|---|
| `tracker.go` | `TrackerAction`, `extractTrackerActions`, `performTrackerActions`, `applyTrackerAction`, `carryOutTrackerAction`, `TrackerOutcome`, `renderTrackerResults`, refused-block hand-back (`recordRefusedTrackerBlock`) |
| `role.go` | role authority, `SystemPrompt`, `WithRemit` |
| `document.go`, `documentpublication.go` | document writes, policy confirmation, durable publication and returns to the owner |
| `compact.go`, `memory.go` | session compaction and the role's own memory |
| `usagelimit.go`, `providerwait.go`, `provideroutage.go`, `hold.go` | provider refusals, waits that put the conversation down, the operator hold |
| `rebuild.go` | continuing on a provider that never held the session |
| `requestsize.go` | measuring the selected endpoint's input, shortening history and then the briefing before sending, and retrying a size refusal once |
| `steer.go`, `work.go`, `milestone.go` | console slash commands: `/work`, `/stop`, surveys (`SurveyWork`), `StartWork`, `StopWork` |
| `triage.go`, `triagestop.go`, `repair.go` | development manager decisions on stopped runs |
| `proposal.go`, `admission.go`, `resemblance.go`, `condition.go`, `concern.go`, `decision.go`, `withdraw.go` | proposals, admission without asking, duplicates, concerns, batch decisions |
| `origin.go` | reading an admission's origin back out of an older item's notes, for `yoyo goals origins`; the origin an admission records is `creationOrigin` in `admission.go` |
| `report.go`, `reportcoverage.go`, `lanereport.go` | reports roles file and read; program manager lane reports; `ForPass` |
| `exchange.go` | one role asking another (`conductAsk`) |
| `freshness.go`, `refresh.go` | how old the role's picture of the product is, and refreshing it |
| `research.go`, `repository.go` | outside research and repository reads at a commit |
| `stream.go`, `activity.go`, `wording.go`, `replycut.go` | showing a reply while it arrives; plain-words findings; cut replies |
| `spend.go` | what a turn spends, attributed |

## Tests

`chat_test.go` covers `Send`/`Open`; most topics have their own
`<topic>_test.go` beside the file above. Shared helpers such as
`testOptions` are in the test files; find them with `rg -n 'func testOptions'`.
