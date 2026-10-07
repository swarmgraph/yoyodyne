# internal/orchestrator — code map

Read this before paging through `pipeline.go` (about 9,700 lines) or
`schedule.go` (about 6,300). It names symbols, not line numbers; find them
with `rg -n 'func \(a \*activeRun\) verify\b' internal/orchestrator`.

## What the package does

It runs one work item from claim to closure, and decides what runs next.

- `pipeline.go` is one run: claim the item, cut a worktree, invoke the
  developer, put the change through path checks, self-verification and the
  configured checks, get an independent review, hand failures back to the
  developer within a budget, promote the approved change onto the target
  branch, record the outcome on the item, and clean up. Every way a run can
  stop or pause is decided here too.
- `schedule.go` is the watch loop: which ready items to start, how many at
  once, when to fire recurring role passes, when to stop starting work.
- The other files are the things that act on runs that already stopped:
  continuing them, re-running them, docketing them for the development
  manager, reconciling what they left behind, and the recurring role passes.

The durable run record is `runstate.State` (in `internal/runstate`); the
pipeline reads and writes it through the `StateStore` interface. Most "what
field holds X" questions are answered in `internal/runstate/state.go`.

## Map of pipeline.go, in file order

1. **Interfaces the pipeline is built from.** `WorkTracker`, `Pricer`,
   `WorktreeManager` (includes `ContentIdentity`), `PullRequests`,
   `ChangeReviewer`, `StateStore`, `CheckRunner`, `LandingCheckouts`,
   `WorkFiler`, `Directives`, `OperatorHolds`, `IntakeHolds`.
2. **`Pipeline` struct** — every dependency and setting a run uses
   (`Config`, `Tracker`, `Store`, `Worktrees`, `Reviewer`, `Docket`, ...).
   Account choice: `AccountChooser`, `chooseAccount`, `reserveRun`
   (records model, effort and account on the new run), `accountFor`.
3. **Result types.** `Preservation` (what is left of a failed run's branch and
   worktree), `Outcome` (what `Run` returns; mirrors much of `runstate.State`),
   `ExistingRunError`, `EnvironmentRefusedError` / `refusedByEnvironment`.
4. **Starting a run.** `Pipeline.Run` — the whole start sequence, in order:
   `validateDispatch`, `operatorHold`, `resolvePublishing`, `readWorkItem`,
   `pausingDirectives`, `blockingDependencies`, `Store.Adopt` (resume an
   in-flight run via `resumeRun`, or refuse with `ExistingRunError`),
   `holdIntake`, `refuseSubstitutedHandback`, `validateReadyItem`,
   `refuseUngrantedCondition`, `loadInvariants`, `Worktrees.ValidateReady`,
   `requireBackendReady`, `reserveRun`, `beginDeliveryTrial`, `claim`,
   `Worktrees.Create`, `liftPreserved`, `prepareScratch`, then `develop` and
   `verifyReviewAndFinish`.
5. **Continuing a run.** `Pipeline.Continue` (re-enter one named run;
   `ContinuationMismatchError`), `reclaimSlot`, `resumeRun` (picks the step
   to resume at from the record), `resumedDeveloperPrompt`, and the predicates
   that read a record: `handedBackRepair`, `owesVerification`,
   `resumesAnExistingChange`, `continuableStall`, `stallResumesPastTheAttempt`,
   `owedARepair`, `refuseSubstitutedHandback`.
6. **`activeRun` struct** — one run in progress (state, outcome, worktree,
   context). Invariant delivery: `deliveredInvariants`, `reviewedInvariants`,
   `workItemEvidence`, `grantEvidence`, `refuseProviderGrant`.
7. **The gate.** `verifyReviewAndFinish` → `repairLoop` → `promoteApproved` →
   `integrate` → `finish`. Promotion races: `contendedIntegration`,
   `prepareIntegrationRetry`, `recordRebase`, `continueOnRebaseConflict`,
   `moveOntoTargetForRepair`, `blockOnRebaseConflict`, `blockOnDivergedTarget`,
   `blockOnPromotedDivergence`. Budget: `repairBudget`, `repair`,
   `chargeReplayStop`, `blockOnChargedReplay`.
8. **Repair inputs and budget-spent stops.** `recordCheckFailure`,
   `recordPathRefusal`, `blockOnUnresolvedFindings`, `blockOnFailingCheck`,
   `blockOnRefusedPaths`, `verifyHandback`, `blockOnMissingPreservedChange`,
   `block` (writes the blocker on the item).
9. **Environment-caused stops.** `recordEnvironmentalRefusal`,
   `settleEnvironmentalRound`, `roundDelivered`, `environmentalCauseOf`,
   `integrationStopCauseOf`, `recordIntegrationStop`,
   `refuseDispatchEnvironmentally`, `stepCauseOf`, `withFailedRecord`.
10. **Developer invocation.** `develop` (one attempt plus relaunches and
    waits), `attemptDevelopment` (the metered backend call),
    `recordDevelopment`, `carrySession`, `mayRelaunch`, `diedTransiently`,
    `recordRelaunch`, `blockOnSpentRelaunchBudget`, `account`,
    `developerModel`, `developerEffort`.
11. **Provider refusals and pauses.** `refusedForUsageLimit`,
    `refusedForServerOverload`, `providerStopReason`, `providerStop`,
    `recordProviderStop`, `stoppedProviderIsResumable`, `pauseForUsageLimit`,
    `pauseForServerOverload`, `awaitRecordedUsageLimit`, `waitForProbe`,
    `releasedByOperator`, `blockOnUsageLimit`, `stopOnUsageWindow`,
    `usageWindowStop`, `usageLimitPause`, `pausedForUsageLimit`.
12. **Directive, dependency, tracker and operator holds.** For each:
    `holdFor…`, `record…Pause`, `clear…Pause`, a pause error type, and a
    `pausedFor…` predicate — `directivePause`, `dependencyPause`,
    `trackerPause`, `operatorHoldPause`. Also `operatorHold`, `holdWorkItem`,
    `holdIntake`, `stopRequested` / `operatorStop`, `holdForOperator`.
13. **Checks.** `verify` (path gate, self-verification gate, configured
    checks), `integrationEarned` (reads the record, compares
    `ContentIdentity`), `closeCheckStage`, `scaleCheckStage`,
    `checkStageTimeout`, `landingCheckTimeout`, `gateProtectedPaths`,
    `pathRefusal`, `checkFailure`.
14. **Promotion and landing.** `integrate`, `finish`, `runLandingChecks`,
    `leaseLanding`, `fileRedLanding`, `nameUnreadingParts`.
15. **Endings.** `complete` (outcome notes, close, price, terminal record),
    `cleanUp`, `stop` (routes a stop to a pause or an ending), `escalate`,
    `pause`, `pauseForProviderStop`, `pauseForDirective`,
    `pauseForDependency`, `pauseForTracker`, `pauseForOperatorHold`, `fail`
    (terminal failure, failure notes, docket entry),
    `recordEndingAfterRefusedSave`, `recordPrice`.
16. **Bookkeeping.** `sink` (events), `recordWorktree`, `liftPreserved`,
    `prepareScratch`, `verifyPreservation`, `recordChanges`,
    `recordHarnessCommit`, `reportCompletionRecordingFailure`,
    `reportOutstandingCleanup`.
17. **Review.** `reviewChange` (waits and relaunches around one review),
    `attemptReview` (builds `review.Request`, calls `Reviewer.Review`,
    records the verdict), `recordReviewVerdict`, `reviewedContext`,
    `developerSummaryForReview`, `clearReviewEvidence`, `carryReviewEvidence`,
    `durableFindings` / `reportedFindings`, and the resume predicates
    `resumableRepair`, `continuedAtCheckStage`, `resumableIntegration`.
18. **Stop classes.** `phaseError`, `classifiedStop`, `stoppedBy`,
    `classifyStop`, `recordedStopIn`, `stopRequestClass`, `failureStatus`.
19. **Validation and agent lookup.** `validate`, `validateReviewPolicy`,
    `validateIndependentInvocations`, `developer`, `reviewer`,
    `agentForRole`, `runsOnCompiledAdapter`, `validateWorkItem`,
    `blockingDependencies`.
20. **Prompts.** `developerContract` / `developerContractTemplate`,
    `developerPrompt`, `repairPrompt`, `pathRefusalRepairPrompt`,
    `checkRepairPrompt`, `replayConflictRepairPrompt`, `accountPrompt`,
    `boundedCheckOutput`.
21. **Notes written on the work item.** `renderOutcomeNotes` (success),
    `renderFailureNotes` + `renderPreservationNotes` (failure), one
    `render…Notes` per blocker and pause, `renderReviewNotes`,
    `renderCheckNotes`, `renderCleanupNotes`, `integrationMessage`.

## Questions developer runs keep asking

### Where does a run move from checks to review, and back to the developer?
`repairLoop`. Each round: `holdForDirective`, `holdForDependency`, then
`verify`. A `pathRefusal`, `missingVerification` (in `selfcheck.go`) or
`checkFailure` goes back to the developer through `repair` with the matching
prompt; any other `verify` error ends the run. When `verify` passes,
`reviewChange` runs. Approve returns to `verifyReviewAndFinish`; escalate
returns `escalationRaised`; anything else is handed back with `repairPrompt`
until `repairBudget` is spent, then `blockOnUnresolvedFindings`. Without
automatic integration (`automatic()` false) there is no loop: `verify` once,
then `finish`.

### What does the reviewer receive, and where is the verdict recorded?
`attemptReview` builds the `review.Request`: diff from
`Worktrees.UnifiedChanges`, context from `reviewedContext`, invariants from
`reviewedInvariants`, the developer's summary from
`developerSummaryForReview`, account from `account`. It saves the verdict,
model and effort (`ReviewEffort`) on the state before returning.
`recordReviewVerdict` charges the item's triage counters.
`validateIndependentInvocations` refuses promotion if developer and reviewer
sessions are not distinct. The reviewer itself is in `internal/review`.

### Which backend, model, effort and account does an invocation use?
Chosen once, in `reserveRun` (`state.ProviderEffort` comes from
`Config.InvocationEffort` in `internal/config/effort.go`), then read back off
the record by `developerBackendFor` (`recordedbackend.go`), `developerModel`,
`developerEffort` and `account`. A run recorded on a backend other than the
configured developer's runs on `Pipeline.RecordedBackends`, or is refused with
`RecordedBackendError` before any provider call. The backend
side of effort is `backend.Descriptor.InvocationEffort`
(`internal/backend/effort.go`).

### How is a stopped run classified, and what does the item say?
`stop` decides first whether the error is a pause (usage limit, provider stop,
directive, dependency, tracker, operator hold, redeploy drain — see
`redeploydrain.go`) or an ending (usage window, escalation, operator stop).
Everything else goes to `fail`, which records any environmental cause, sets
the stop class with `classifyStop`, writes `renderFailureNotes` to the item
via `Tracker.RecordOutcome`, and dockets the run with
`Docket.RecordStoppedRun` or `Docket.RecordUnstartedRun` (`triage.go`). Stop
class names are in `internal/runstate/stopclass.go`. A site marks its own
class with `stoppedBy(runstate.Stop…, err)`.

### Why is a stop counted as the environment's and not the work's?
`environmentalCauseOf` / `integrationStopCauseOf` map errors to
`runstate.EnvironmentalCause`; `recordEnvironmentalRefusal` writes it;
`settleEnvironmentalRound` decides, at the end, whether the round is given
back. `EnvironmentRefusedError` is the same idea before anything was claimed.

### What happens when the provider dies, stalls or refuses?
In `develop` (and `reviewChange` for review): a transient death
(`diedTransiently`) is relaunched while `mayRelaunch`, counted by
`recordRelaunch`, and ends in `blockOnSpentRelaunchBudget`. A harness timeout
(idle or budget) becomes `providerStop` via `recordProviderStop`; the first
silent stall is continued by `StallContinuer` in `stallcontinue.go`. Usage
limits and overloads wait in `pauseForUsageLimit` / `pauseForServerOverload`;
a reset past the maximum wait ends the run through `stopOnUsageWindow`. Login
and outage refusals: `provideroutage.go`.

### Which in-flight runs does a new dispatch pick up?
`Pipeline.Run` calls `Store.Adopt`; the long condition after it lists every
resumable shape (`pausedForUsageLimit`, `pausedForDirective`, ...,
`stoppedForRedeployIsResumable`, `continuedAtCheckStage`, `resumableRepair`).
Anything else is `ExistingRunError`. `resumeRun` then picks the step.
`Pipeline.Continue` is the narrower door used when the development manager
decided a repair (`repaircontinue.go`) or a check-stage continuation
(`checkstagecontinue.go`).

### Where are the content fingerprint and "checks passed" bound together?
`verify` records `ChecksPassed` with `Worktrees.ContentIdentity`;
`integrationEarned` compares it again before promotion
(`ErrIntegrationUnearned`). The fingerprint itself is
`gitworktree.Manager.ContentIdentity` (`internal/gitworktree/identity.go`).

### Where is the change committed, pushed, merged, and the item closed?
`commitAttempt` and `publishAttempt` (`publish.go`, called from `develop`);
`integrate` (promotion lease, fast-forward, `publishIntegration`,
`awaitMerge`); `finish` → `complete` (`Tracker.RecordOutcome`,
`Tracker.Complete`, `closeDocketWithItem`) → `cleanUp`. What the developer
claims about closure is read in `landing.go` (`arrangeUndischarged`).
Landing checks after the run: `runLandingChecks`, `fileRedLanding`.

### What is the developer told?
`developerPrompt` = `developerContract` + persona + invariants + context
bundle. Repair rounds use the `…RepairPrompt` functions; a resumed run
rebuilds its prompt with `resumedDeveloperPrompt`. Relevant goals and the
context bundle come from `internal/contextbundle`.

## Sibling files these questions lead to

| File | What it holds |
|---|---|
| `schedule.go` | `Scheduler.Schedule` (one watch poll), `Pull`, `Starter`, `Scheduler.host` (starts a run), `Scheduler.fire` (recurring passes), `Scheduler.brake`, passed-over reasons (`passedOverReason`), redeploy drain bookkeeping (`redeployDrain`) |
| `recurring.go` | `Trigger` — recurring role passes: `Cadence`, `Fire`, `Summon`, `run` (turn loop of one pass, `MissConversationHeld`), `Missed`, `earlierPasses`, `PassFailures` |
| `programmanagerpass.go` | program manager pass wake-up and cursor (`readWake`, `advance`) |
| `triage.go` | `Docketer`: `RecordStoppedRun`, `RecordUnstartedRun`, `RecordEscalation`, `SettleClosedItem`, `Build` |
| `rerun.go`, `repaircontinue.go`, `carryout.go`, `carryrearm.go`, `rearm.go` | carrying out the development manager's decisions on docketed runs |
| `stallcontinue.go`, `checkstagecontinue.go`, `integrationresume.go` | the harness's own continuations of a stopped run |
| `recordedbackend.go` | `DeveloperBackends`, `developerBackendFor`, `RecordedBackendError`: a run's developer on the backend the run recorded; `erasedSession`: a session a failed attempt erased, read back from the event log |
| `redeploydrain.go` | `RedeployDrain`, `drainedForRedeploy`, `pauseForRedeploy` |
| `runretirement.go` | `RunRetirer.Retire` — retire a run whose item closed on a confirmed merge |
| `documentpublication.go` | `PublishDocument`, exact-file gate, independent review without a developer, returns to the owning conversation |
| `publish.go`, `publication.go` | pushing branches, pull requests, merge waits, re-asking the forge later |
| `reconcile.go`, `reconcilewait.go`, `reconcilefinding.go` | settling what stopped runs left on disk and on the forge |
| `selfcheck.go` | `gateSelfVerification`, `missingVerification`, `verificationRepairPrompt` |
| `declarative.go`, `workflow.go` | the delivery definition stepped beside a run (`observe`, `beginDeliveryTrial`) |
| `landing.go` | developer's closure claims, `arrangeUndischarged` |
| `recovery.go` | `readWorkItem` and tracker read retries |
| `provideroutage.go`, `usagelimit.go` | `requireBackendReady`, provider-away handling |
| `supervision.go` | the management loop's own pass over role exchanges |

## Tests

`pipeline_test.go` and `schedule_test.go` mirror the two big files. Shared
fakes are in `orchestratortest/`. Prefer `rg -n 'func Test.*<Topic>'` over
reading the test files from the top.
