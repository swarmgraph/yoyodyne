# yoyodyne-ifd.429.12: what internal/orchestrator's test binary spends its time on

The 429.11 run reported `internal/orchestrator`'s test binary taking 651 seconds
under `make test` and 606 under `make race` at a one-minute load near 50,
against the Makefile's twenty-minute `TEST_TIMEOUT`. This item asked for the
package's tests to be split, or their slowest fixtures shared, until the binary
finishes in well under half of that figure at a load above 40. This is the
record of what the binary's time goes on, of what does not reduce it, and of
why the split cannot be one change.

All figures are from one sixteen-core machine on 2026-09-25, with other
developer runs working throughout. Every run was `-count=1`, so nothing was
served from the test cache.

## Where the time goes

The binary is not waiting on its own tests' logic. It is waiting on the Git
processes that the harness code under test starts.

| run | binary | wall | user | sys | one-minute load |
| --- | --- | --- | --- | --- | --- |
| `go test -json`, 01:26 to 01:36 | plain | 628.5s | 274s | 577s | 82 at start, 56 at end |
| every `git` counted through a `PATH` shim, to 01:45 | plain | 508.5s | 339s | 734s | about 55 |
| without the change below, beside the next row, 02:33 to 02:47 | race | 823.2s | 318s | 1458s | 60, rising to 160, 85 at end |
| with the change below, beside the row above | race | 826.7s | 317s | 1469s | the same |
| `make race`, with a second `make race` beside it, to 02:30 | race | 1016.1s | | | 82 to 146 |

(The last row is the orchestrator line out of the whole suite. The neighbouring
`make race` reported 1022.8s for the same package.)

Two things stand out. First, system time is two to four times user time.
Second, the counting run saw **53,830 Git invocations** in one pass of the
package. At a load near 50 that is roughly 10 to 27 milliseconds of kernel time
per Git process. On an idle machine it is about 3. All 826 top-level tests are
parallel, so no serial test is holding the rest up. Real-time waits are not a
factor either: an instrumented run recorded one real 1-second sleep in the
pipeline, reconciler, and scheduler waits combined.

The invocations by what they ask Git to do:

| command | count |
| --- | --- |
| `rev-parse` (5,800 of them `--git-common-dir`, 4,478 `--verify HEAD^{commit}`) | 14,093 |
| `worktree` (6,292 of them `list --porcelain`) | 7,838 |
| `diff` | 6,282 |
| `merge-base` | 3,785 |
| `log` | 3,459 |
| `status` | 3,128 |
| `ls-files --others` | 3,010 |
| `config` | 2,116 |
| everything else | about 10,000 |

Almost all of these are issued by the harness under test (`internal/gitworktree`,
the pipeline, publication) while a test drives a run through a repository. Only
a small share come from the tests building their fixtures. `init`, the fixtures'
`config` calls, and their initial `add` and `commit` come to about 3,000, which
is 5 to 6 percent.

## What does not reduce it

**Sharing the fixtures.** Building `pipelineRepository` once per binary and
copying it into each test would save the 5 to 6 percent above, at most. The
other 94 percent is the harness's own Git work, and a test cannot share that
without no longer testing it.

**Turning off Git's disk flushes.** A first microbenchmark showed a commit plus
a worktree add and remove taking 938ms with Git's default flushing and 57ms with
`core.fsync=none`. That benchmark ran under the repository's `.git` directory
while the load was moving. Repeated back to back in `$TMPDIR`, where the tests'
repositories actually live, it came out at 168, 162, 157, and 166ms, the same
either way. A `TestMain` setting `core.fsync=none` for every Git command the
binary starts made no difference to the race binary either: 826.7s against
823.2s, run side by side (the table above). It is not part of this change.

**The run store's own `Sync` calls.** Turning every `File.Sync` in
`internal/runstate` and `internal/repowrite` into a no-op, as an experiment and
never as a change, gave a 413.9s run with the load between 47 and 78. That was
not measured side by side, so it does not show an effect. It would also be a
change to the product's durability rather than to its tests.

## Why the split is not one change

A split puts the same Git work into more binaries. It does not remove any of it.
What it buys is a binary that finishes in a fraction of the time, because each
binary carries a fraction of the work. That is what `TEST_TIMEOUT` bounds, so it
is what the item asks for. Three things stop it being one change.

- **The tests are all internal.** All 70 test files are `package orchestrator`.
  A type-checked census of every test and every package-level test declaration
  found:
  - 674 of the 826 tests reach no unexported identifier of the package, directly
    or through the helpers they call.
  - Those 674 tests account for about 87 percent of the attributable Git
    invocations.
  - The other 152 tests reach 127 distinct unexported identifiers between them.
- **The helpers are shared.** The two groups share 180 test helpers:
  `fakeTracker`, `newPipeline`, `roleBackend`, `pipelineRepository`,
  `fakeForge`, and the baseline fixtures among them. Together that is roughly
  2,700 lines. An in-package test cannot import a package that imports
  `orchestrator`, so leaving the 152 tests where they are means one of two
  things:
  - duplicating those helpers, or
  - moving the 152 tests as well and exporting what they reach. Assigning every
    test that uses a shared helper to the moved side leaves 45 tests behind and
    requires exporting 88 unexported identifiers.
- **The size is past what a review is shown.** The package's test files are
  52,680 lines and 2.36 MB. The developing guide notes that 300 KB is already
  more than the review gate's patch bound will show a reviewer. A move of even a
  third of those tests is a change nobody can review whole.

The split therefore has to be sequenced as separate work:

1. **Extract the fakes.** Move the fakes of other packages' interfaces into a
   helper package that does not import `orchestrator`: the tracker, the
   backends, the forge, and the worktree fakes. Both the in-package tests and
   the new packages can then import it without a cycle.
2. **Move tests in groups.** Move the tests that reach no internals out in
   groups, one group per change, each small enough to review. Grouping them by
   subject is natural: publication, reconciliation, the scheduler, and so on.

There is also a different lever, and it is the product's rather than the tests'.
Several of the counted invocations look like repeated questions. The common Git
directory of one repository root is asked 5,800 times
(`internal/gitworktree/registry.go`, `commonGitDirectory`, once per registry
lease). The worktree list is read 6,292 times. Answering those once per manager
or per lease would cut the tests' Git work and a real run's by the same amount.
Whether it is safe is a question about the lease. yoyodyne-ifd.429.15 answered
it and measured the result: see the section after next.

## What this does not show

The load on the machine during these measurements was mostly other runs'. It
reached 160 at one point, well past the item's figure of 40. At loads between
56 and 146 the binary took 508 to 1016 seconds, so at that load it already
passes half of `TEST_TIMEOUT`, and in the worst run it came within three
minutes of the whole of it. The per-invocation kernel cost is an average over a
run, not a measurement of any one command. The census of which tests reach
internals is a type-checked walk of identifiers. It treats a test type as
internal if it satisfies an interface with unexported methods, and it may still
miss an internal reached some way it does not look for. A compile is what
settles any one move.

## What keeping the two answers bought (yoyodyne-ifd.429.15)

yoyodyne-ifd.429.15 made `internal/gitworktree` stop asking Git again for the
two answers above when they could not have changed.

- **The common directory is asked once per manager.** It was asked once per
  lease. That meant once per Git command that walks the registrations, because
  each of those takes the shared lease, and finding the lease's file meant asking
  Git where the common directory is. The answer depends only on the repository
  root. A manager never changes the root, and no creation, removal, or prune
  touches the root's `.git`. The one case the kept answer does not survive is a
  repository replaced under the manager, so a kept directory that is no longer
  there is asked for again (`commonGitDirectory`).
- **The worktree list is kept for the length of one exclusive lease.** While the
  harness holds the exclusive lease, no other harness writes the registrations.
  The only changes the kept list could miss are the holder's own. The list is
  dropped:
  - before and after every command the holder runs that walks the registrations,
    except the listing itself (an add, a removal, a prune, a checkout, a switch,
    a rebase, a branch);
  - when the holder clears an unfinished registration itself;
  - when the lease is released.

  A listing Git gave while a change was running beside it is used once and never
  kept. Outside an exclusive lease, nothing is kept (`registryState`).

Counted the same way as the table above, with a `PATH` shim, one pass of
`go test -count=1 ./internal/orchestrator` each. The two runs used the commit
before the change and the change itself, side by side on 2026-09-25 from about
08:05, with the one-minute load between 65 and 105.

| run | all `git` | `rev-parse --git-common-dir` | `worktree list --porcelain` | wall |
| --- | --- | --- | --- | --- |
| before | 53,992 | 5,815 | 6,310 | 702.9s |
| after | 48,684 | 545 | 6,310 | 663.2s |

The change removed 5,308 Git processes, 9.8 percent of the package's total.
Nearly all of that saving is the common directory. The 545 that remain are about
one per manager, because the tests build a manager per repository.

The worktree list did not move. An exclusive lease is held for one creation,
removal, restore, or prune, and each of those lists at most once between its
own changes. Nothing inside a lease lists twice, so keeping the list there
saves nothing in this suite. It is still the lease-safe place to keep one, and
it is what keeps a later caller that lists twice under a lease from paying for
it. The 6,310 listings come from reads outside any exclusive lease: `Inspect`,
`Observe`, `Survives`, `VerifyOwnedHead`, and the target checkout lookup, each
holding the shared lease for its one command. Keeping a list across those needs
a different argument, because any other harness may be writing between them. It
is not part of this change.

The wall times are one pass each at a load that moved throughout. They are not
evidence of a time saving by themselves. The saving the counts support is about
a tenth of the package's Git processes, at the kernel cost per process the first
table describes.

## The helper package and the census (yoyodyne-ifd.429.13)

Step 1 above is complete. The shared fakes of other packages' interfaces now
live only in `internal/orchestrator/orchestratortest`:

- `Tracker`, the work tracker
- `Backend`, the provider, with `RoleBackend` and `WithVerification`
- `Forge`, the forge, with `ConnectionReset`
- `Pricer`, the pricing ledger
- `PartialWorktreeManager`, a worktree manager whose worktree was never finished

Their fields and methods are exported, because a package outside `orchestrator`
could not set them otherwise. The package imports nothing from `orchestrator`,
directly or through any package it reaches, and
`TestThePackageImportsNothingFromTheOrchestrator` fails if it ever does. A test
package of its own checks that each fake still satisfies the interface
`orchestrator` asks of it.

Every in-package test uses these shared fakes from `orchestratortest`. Removing
the in-package copies (yoyodyne-ifd.429.13.6) deletes `inpackagefakes_test.go` and
`fakeforge_test.go`, along with their accessors in `fakeaccess_test.go`.
`fakeTracker`, `fakeBackend`, `fakeForge`, `fakePricer`,
`partialWorktreeManager`, `roleBackend`, `withVerification`, and
`connectionReset` are no longer declared in `orchestrator`, and no aliases keep
the old names working. The shared fixture interfaces in `fakeaccess_test.go`
remain, with their compile-time checks against the helper package's fakes.

The census below was taken on the tree of pull request #757 (base 230c2a83),
with the same type-checked walk as 429.12. It counted 834 tests, 8 more than
429.12, because main had gained tests since. Moving the fakes changed neither
count: none of the fakes reaches an internal.

- 681 tests reach no unexported identifier of the package.
- 153 tests reach 129 distinct unexported identifiers between them.

The first addition after that census was `TestEveryTurnOfAFiringIsToldItsPass`
in `recurring_test.go`. This historical census does not classify it or later
additions. Removing the in-package fakes adds or removes no tests and changes
none of their assertions; the counts below still describe the tree of pull
request #757.

### By file

A group is one or more test files, so the census is given per file. Files fall
into three kinds.

**No test reaches internals.** These 30 files hold 177 tests and can move whole:

`baseline_test.go` (4), `brake_test.go` (12), `branchreview_test.go` (8), `codex_test.go` (2), `conflict_test.go` (10), `correction_test.go` (10), `coverage_agreement_test.go` (1), `developermodel_test.go` (2), `directive_test.go` (8), `environmental_test.go` (9), `escalate_test.go` (25), `escalation_test.go` (6), `handback_test.go` (5), `invariant_test.go` (3), `language_agnostic_test.go` (3), `nextmover_agreement_test.go` (3), `outcome_test.go` (6), `phasespend_test.go` (2), `preservedsurfaces_test.go` (4), `promotion_test.go` (2), `protectedtarget_test.go` (7), `publication_test.go` (6), `repairdispatch_test.go` (6), `reviewbase_test.go` (1), `scheduleslots_test.go` (5), `stop_test.go` (6), `stoppage_test.go` (1), `supervision_test.go` (10), `triagecaps_test.go` (6), `usagewindow_test.go` (4)

**Internals are reached by a minority.** These 28 files hold 580 tests. Only the
85 named below reach internals, and those stay behind while the rest of their
file moves:

| file | tests | reaching internals | the tests that do |
| --- | --- | --- | --- |
| `account_test.go` | 5 | 1 | `TestSimultaneousStartsAreServedByDistinctAccounts` |
| `amendments_test.go` | 18 | 8 | `TestAChangeWithNoContentWordsIsComparedLiterally`, `TestALongAmendmentRefusalIsFoldedToWhatTheStateWillTake`, `TestARefusalIsNotShownToARoleThatDidNotEarnIt`, `TestCarriedAmendmentRefusalsStopAtTheBound`, `TestOneArgumentSpelledTwoWaysIsRecognisedAndTwoArgumentsAreNot`, `TestOneChangeAskedOfTwoDocumentsIsTwoArguments`, `TestTheDeveloperContractSaysHowToProposeAChange`, `TestTheRecordedRefusalOutlivesTheCarriedOne` |
| `carryout_test.go` | 30 | 2 | `TestEveryGateIsNamedFromTheRefusalsSentinel`, `TestOnlyThePausesShutForEveryDecisionAreTheWaitingKind` |
| `claims_test.go` | 18 | 1 | `TestAnAuditorWithoutAClockUsesTheWallClock` |
| `converge_test.go` | 14 | 5 | `TestConvergeRetiresSettledCheckoutsPastTheTail`, `TestSweepableWorktreesHoldsBackTheMostRecentSettledRuns`, `TestSweepingRecordsACheckoutSomethingElseAlreadyRemoved`, `TestSweepingRecordsTheBranchItDeleted`, `TestSweepingRetiresACheckoutAndPreservesTheWorkInIt` |
| `conversationlanding_test.go` | 5 | 1 | `TestARevisionReasonOpensWithAnIdentifierAsAWholeWord` |
| `hold_test.go` | 6 | 1 | `TestARunParksAtItsNextProviderCallAndCarriesOnWhenTheHoldLifts` |
| `integrationresume_test.go` | 18 | 5 | `TestADivergedTargetAndARefusedKeyAreIntegrationStops`, `TestAKilledReplayIsAnIntegrationStopAndAConflictIsNot`, `TestAResumptionMakesTheStoppedRunLiveAtItsPromotionChargingNothing`, `TestAnApprovedChangeWhoseWorktreeWasRetiredIsRestoredAndResumed`, `TestOnlyARunResumedOnPurposeIsPickedUpAtItsPromotion` |
| `landing_test.go` | 22 | 7 | `TestADependencyTheTrackerRefusesParksTheItemRatherThanFailingTheSettlement`, `TestDecidingWhereAnUndischargedItemGoesReportsATrackerItCouldNotRead`, `TestSettlingAnItemLeftWaitingKeepsTheParkingItAlreadyCarried`, `TestSettlingAnUndischargedItemTwiceMakesItWaitOnce`, `TestTheClosureDerivationReadsTheSameRecordEverywhere`, `TestTheParkingDefaultRecordsTheReasonItSuperseded`, `TestTheParkingReasonStaysInsideWhatTheTrackerHolds` |
| `pipeline_test.go` | 91 | 12 | `TestBoundedTailKeepsTheEndOfTheOutputOnARuneBoundary`, `TestDeveloperPromptKeepsTheHarnessContractAboveAnyPersona`, `TestFailureNoteDescribesEachArtifactFromWhatWasSettledAboutIt`, `TestPipelineBoundsTheFailingCheckOutputItHandsBack`, `TestPipelineRefusesToActOnARunAnotherInvocationHolds`, `TestRunPausesForAnExhaustedUsageLimitAndResumesWhenItResets`, `TestRunPausesForAnOverloadedReviewAndAsksAgain`, `TestRunPausesWhenTheReviewerHitsAnExhaustedUsageLimit`, `TestSingleLineKeepsCommitSubjectsBoundedAndValid`, `TestValidateClaimedItemRejectsIdentityStatusAndBlockerChanges`, `TestValidateReviewPolicyGatesOnTheRoleHoldingTheVerdict`, `TestValidateReviewPolicyRefusesAReviewerNothingCanLaunch` |
| `protectedpath_test.go` | 14 | 2 | `TestAGrantInTheItemsNotesDoesNotAdmitAPath`, `TestTheDeveloperContractNamesEveryPathBeyondAGrant` |
| `provideroutage_test.go` | 7 | 2 | `TestRunWaitsOutALoginRefusedOnStderrBeforeAnyEnvelope`, `TestRunWaitsOutAProviderNobodyCanReachSpendingNothing` |
| `publication_settle_test.go` | 5 | 1 | `TestReconcileFinishesAMergeThatLandedAmongOthers` |
| `publish_test.go` | 37 | 8 | `TestPipelinePublishingNamesWhicheverRemoteIsMissing`, `TestPipelineStopsBeforePromotingIntoADivergedRemoteTarget`, `TestPipelineStopsWaitingForAMergeThatNeverArrives`, `TestPipelineStopsWhenTheRemoteTargetDivergesAfterThePromotion`, `TestPullRequestBodyCarriesGovernedTextAndComputedFacts`, `TestPullRequestBodyOmitsWhatItHasNothingToSay`, `TestPullRequestBodyStaysInsideWhatAForgeAccepts`, `TestPullRequestTitleCutsAtAWordBoundary` |
| `reconcile_test.go` | 23 | 4 | `TestARepairContinuesAFirstAttemptStalledAtItsChecksAtTheChecks`, `TestARepairContinuesAFirstAttemptStalledInItsReviewAtTheReview`, `TestAStallAtTheReviewIsHeldToTheRepairsContentCheck`, `TestAVanishedProcessThatDeliveredNothingReturnsItsGrant` |
| `reconcilewait_test.go` | 5 | 1 | `TestAContinuationThePipelineRefusesIsReportedAsAFailure` |
| `recovery_test.go` | 12 | 1 | `TestRecordedRetriesAreValidatedAndBounded` |
| `recurring_test.go` | 28 | 1 | `TestABoundedProblemIsCutOnARuneBoundary` |
| `repaircontinue_test.go` | 27 | 1 | `TestARepairSupersedesTheBlockerOnBothTheRunAndTheItem` |
| `reports_test.go` | 3 | 1 | `TestTheDeveloperContractSaysWhatMeritsAReport` |
| `rerun_test.go` | 39 | 1 | `TestSupersedingAStaleClaimAsksAgainWhetherARunHoldsIt` |
| `schedule_test.go` | 92 | 11 | `TestADeliveryFailureIsSaidByEveryPassThatMeetsIt`, `TestAFailedFiringIsAProblemThatStillCosts`, `TestAFiredTaskReachesTheScheduleWithItsCost`, `TestAPassDoesNotEraseWhatItSaidAboutStoppedWork`, `TestAPassNamesEachEndingRatherThanCallingEveryStoppageAFailure`, `TestAPassWithNothingDueSaysNothingAboutTheSchedule`, `TestAPullWithoutATriggerIsUnchanged`, `TestAScheduleThatCouldNotBeFiredIsSaidRatherThanStoppingThePass`, `TestAnExclusionSaysWhatBecameOfTheStart`, `TestRepeatedDeliveryFailuresAreOneProblemOnThePass`, `TestWhatADeliveryCostCountsAgainstTheSessionsBudget` |
| `selfcheck_test.go` | 9 | 1 | `TestABoundedSelfCheckFieldIsCutOnARuneBoundary` |
| `spend_test.go` | 4 | 1 | `TestADeveloperAttemptAfterTheFirstIsChargedToRepair` |
| `trackerread_test.go` | 3 | 1 | `TestAPreClaimTrackerWaitIsReadableFromTheSurfacesWhileItStands` |
| `triage_test.go` | 38 | 2 | `TestAPublicationDocketedUnderTheOlderKeyIsNotDocketedAgain`, `TestARunThatDiedBeforeItClaimedItsItemIsDocketed` |
| `triageoverride_test.go` | 4 | 2 | `TestARerunReasonNamesTheCrossedCapsAndStaysInsideTheSelectionBound`, `TestARerunReasonSaysNothingAboutOverridesOnAnItemThatHasNone` |
| `unaccounted_test.go` | 3 | 1 | `TestAReplyThatAccountsForNothingIsToldApartFromOneThatDoes` |

**Internals are reached by most.** In these 12 files, 68 of 77 tests reach
internals. They stay:

`actions_test.go` (13 of 14), `completion_recording_test.go` (2 of 2), `declarative_test.go` (10 of 13), `definition_repository_test.go` (2 of 3), `dependency_test.go` (3 of 3), `doctor_agreement_test.go` (1 of 1), `durablevocabulary_test.go` (2 of 3), `lostpublication_test.go` (13 of 15), `parity_test.go` (5 of 5), `projectdefinition_test.go` (4 of 5), `protectedrearm_test.go` (1 of 1), `rearm_test.go` (12 of 12)

### What the moved tests still share with the ones that stay

The fakes were the first shared helpers, not the last. Counting the package-level
test declarations the 681 tests reach:

- 380 are reached by those tests alone, and can move with them.
- 304 are reached by the 153 as well.

The 304 shared helpers split two ways:

- **228 name nothing in `orchestrator`'s own code.** Examples are the fixed
  clocks, `pipelineRepository`, the Git helpers, and the verdict constants. They
  can join `orchestratortest` the way the fakes did.
- **76 name something `orchestrator` declares,** exported or not. Examples are
  `newPipeline`, `newSharedPipeline`, `newPublishingPipeline`,
  `newScheduleHarness`, `hookedWorktrees`, and the baseline fixtures. None of
  them can go into `orchestratortest`. A helper package that imports
  `orchestrator` would serve the tests that move out, but the in-package tests
  cannot import it. So each of these is either kept in two copies or taken out
  of the tests that stay behind. That is the decision each group's move has to
  make.

Moved tests also have to go into a directory of their own. A `package
orchestrator_test` file in `internal/orchestrator` compiles into the same test
binary as the package's own tests, so it would split nothing.
