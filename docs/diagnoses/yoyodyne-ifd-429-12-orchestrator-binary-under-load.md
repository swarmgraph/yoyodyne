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

## The helper package and the census (yoyodyne-ifd.429.13, landed by 429.13.1)

Step 1 above has its first half. The fakes of other packages' interfaces now
have exported copies in `internal/orchestrator/orchestratortest`:

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

The more specialized fakes have moved there too, with their scripted answers,
state, and call records as exported fields. Among them are `RecoveryCheckout`
and `ResumeOwnership` for recovery, `CheckedForge` and `PublicationAnswers` for
publication, and the conversation, re-arm, and scheduler fakes.

The in-package tests moved onto them in two changes, split by file name
because one change was too large to review:

- **First (yoyodyne-ifd.429.13.6.1).** Every test file whose name sorts before
  `publish_test.go` took its fakes from `orchestratortest`. The old in-package
  `fakeTracker`, `fakeBackend`, `fakeForge`, and `fakePricer` went, because no
  test used them any more.
- **Second (yoyodyne-ifd.429.13.6.2).** The files from `publish_test.go` on
  moved too. The last two old names, `looked` and `answeringForge`, gave way to
  `orchestratortest`'s `Survival` and `AnsweringForge`, and `fakes_test.go`,
  which held them, is gone.

The fakes of the tracker, the backends, the forge, and the worktree manager now
live only in `orchestratortest`. Nothing under `internal/orchestrator` declares a
copy of one. A test that needs one answer changed still declares a small type of
its own, but it wraps an `orchestratortest` fake or the real interface rather
than repeating one.

The census below was taken on the tree of pull request #757 (base 230c2a83),
with the same type-checked walk as 429.12. It counted 834 tests, 8 more than
429.12, because main had gained tests since. Moving the fakes changed neither
count: none of the fakes reaches an internal.

- 681 tests reach no unexported identifier of the package.
- 153 tests reach 129 distinct unexported identifiers between them.

Main has gained one test since that census: `TestEveryTurnOfAFiringIsToldItsPass`
in `recurring_test.go`. The census does not classify it.

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

## The first group moved (yoyodyne-ifd.429.14)

Step 2 has begun. The census is no longer taken by hand:
[`scripts/movetests`](../../scripts/movetests/main.go) type-checks the package's
test variant, follows every test through the helpers it calls, and prints each
test file with how many of its tests reach an unexported identifier of the
package. Given a group of files, it moves the tests that reach none into a new
package and writes the shared helpers they need into
`internal/orchestrator/testharness`.

That answers the decision the section above left to each group. A shared helper
that names something `orchestrator` declares is kept in two copies: unexported
in the package's own test files, where the tests that stay use it, and exported
in `testharness`, which the moved packages import. The first copy goes once the
last test using it has moved. One copy per moved package would have been the
alternative, and it would put the same pipeline helpers in every one of them.

### The census on 2026-10-14

Taken on commit f4a1d55c of the main branch, before the first group moved, by
`go run ./scripts/movetests`:

- 1,453 tests, up from 834 in the census above.
- 418 of them reach internals. 1,035 reach none and can move.

A plain `go test -count=1 -json ./internal/orchestrator` on the same tree took
699 seconds of wall time from 10:37 to 10:48 PDT, at a one-minute load between
9 and 16 on sixteen cores. The tests' own elapsed times add up to 8,136 seconds,
because they run in parallel. The 1,035 tests that can move account for 6,836
of them, 84 percent. The heaviest files by what their movable tests take are:

| file | movable tests | their seconds |
| --- | --- | --- |
| `pipeline_test.go` | 86 of 101 | 1,276 |
| `publish_test.go` | 31 of 39 | 799 |
| `mergequeueworker_test.go` | 15 of 15 | 377 |
| `protectedpath_test.go` | 14 of 19 | 320 |
| `reconcile_test.go` | 19 of 23 | 246 |
| `publication_test.go` | 6 of 6 | 220 |
| `publication_settle_test.go` | 7 of 8 | 210 |
| `mergequeuerecovery_test.go` | 11 of 14 | 171 |
| `queuedchecks_test.go` | 10 of 12 | 132 |

The rest are under 125 seconds each.

### What this change moved

Publication, the second-heaviest subject: the 43 tests of `publish_test.go`,
`publication_test.go`, and `publication_settle_test.go` that reach no
internals, into `internal/orchestrator/publicationtests`. Together they took
about 1,200 of the 8,136 seconds above, about 15 percent. Each test keeps its
name and its assertions; the only differences are the package qualifiers and
the capitalized names of the helpers `testharness` exports.

Nine tests stay in the package, because they reach `pullRequestBody`,
`pullRequestTitle`, `resolvePublishing`, `environmentalStop`,
`mergeConfirmationDelays`, or `settledPublicationDecision`. One more,
`TestPipelineClosesTheItemWhenTheRemoteSweptThePromotionIn`, stays although it
could move. Moving it made Git render the test after it in `publish_test.go` as
removed and added again, so the file's diff was no longer removals alone and
the change no longer fitted the review bound. It moves with the next group that
touches that file.

The review bound decides how big a group can be. A change's whole patch is
held to 256 KB, and a new file to 64 KB. Lines taken out of a file cost little,
because a diff that only removes is described rather than shown once the bound
is reached. New files cost their whole size, and this group's new files come to
about 200 KB: 167 KB of tests and helpers, and 32 KB of the move tool. The
moved `publish_test.go` was 83 KB, so its queued-merge and settled-merge tests
are in `publish_queue_test.go`.

### What the move did to the binaries

Measured as the developing guide records it: `GOFLAGS=-count=1 make race`, with
a second one beside it on the same tree, started at 10:54 PDT on 2026-10-14.
The tree was the change itself, committed as fae36536 at 11:04 PDT; nothing
committed to it after that touched either package's tests.
The one-minute load was near 10 for the first twenty minutes, then rose with
other runs on the machine to between 120 and 268, and stayed above 100 to the
end.

| package | first `make race` | second `make race` |
| --- | --- | --- |
| `internal/orchestrator` | stopped at the 43-minute `TEST_TIMEOUT`, 2,584s | stopped at the 43-minute `TEST_TIMEOUT`, 2,581s |
| `internal/orchestrator/publicationtests` | passed, 267s | passed, 270s |

A second pair, started at 11:04 PDT with the load already past 100 for most of
it, ended the same way: `internal/orchestrator` stopped at the timeout in both
(2,584s each), and `publicationtests` passed in 433s and 468s.

So the moved tests now finish in under eight minutes in a binary of their own,
at a load far above the item's 40. The package's own binary still does not
finish at that load: one group takes about 15 percent of its work away, and at
a load past 100 it needs more than that. The item's measure, no binary past half
of `TEST_TIMEOUT` at a load above 40, is for the end of the move rather than
for this first group, and it is not met yet.

### The second group: the first half of `pipeline_test.go` (yoyodyne-ifd.429.14.1)

On commit 7a3ec0c8, and again on c4b7e92d, `go run ./scripts/movetests`
counted 101 tests in `pipeline_test.go`, and 86 of them could move. This change
moved 43 of them into `internal/orchestrator/pipelinetests`. They cover
claiming the item, what a run records about itself, the developer's checks,
the reviewer's verdict, the repair loop, and the relaunch of an attempt the
provider killed. Each keeps its name and its assertions.

The 43 are the first 43 in the file, with four exchanged for four from later in
the file:

- `TestPipelineNamesADirtyCheckoutBeforeAskingWhetherTheProviderIsInstalled`,
  `TestPipelineRefusesAutomaticIntegrationThatIsNotGatedByAReviewer`,
  `TestPipelinePreservesFailedWorkAndRecordsFailure`, and
  `TestPipelineReportsElapsedAndBudgetWhenACheckTimesOut` stay.
- `TestPipelineReportsOutstandingCleanupWithoutRecastingASucceededRun`,
  `TestPipelineKeepsCompletionOrderingWhenPersistenceOrTheTrackerFails`,
  `TestRunRelaunchesAfterATransientProviderDeath`, and
  `TestRunRelaunchesATransientlyKilledReview` move in their place.

The reason is the one the first group met. With the first 43 exactly, Git drew
`pipeline_test.go`'s diff with added lines, because it paired tests that stay
with tests that moved, and the file's diff would then have been shown whole.
The first two exchanges were enough on 7a3ec0c8; on c4b7e92d, where three of
the moved tests had changed since, Git drew 271 added lines with them alone,
and the second two were the smallest further exchange that made the diff
removals alone again. The new tests come to 125 KB in four files under the
64 KB bound for a new file: `pipeline_test.go`, `review_test.go`,
`repair_test.go`, and `relaunch_test.go`. The helpers they added to
`testharness` come to about 9 KB.

Timed side by side from 08:22 PDT on 2026-10-15, with
`go test -count=1 -json`, at a one-minute load between 12 and 31, on
7a3ec0c8 and on this group as it then stood (the first two exchanges only;
the group was not timed again on c4b7e92d):

| tree | `internal/orchestrator` | `internal/orchestrator/pipelinetests` |
| --- | --- | --- |
| before, 7a3ec0c8 | passed, 1,173s | |
| after, this change | passed, 1,142s | passed, 96s |

The 43 tests' own elapsed times add up to 683 seconds, but they run in
parallel with everything else in the package. Moving them out takes about 30
seconds off the package's wall time at this load. 43 movable tests remain in
`pipeline_test.go`.

### The third group: protection and landing (yoyodyne-ifd.429.14.2)

On commit f2592b91, `go run ./scripts/movetests` counted 80 tests in the six
files of item 3 in the plan below, and 62 of them could move. This change moved
all 62 into `internal/orchestrator/landingtests`. They cover the paths a change
may not touch, a protected target reached through its pull request, a target
whose checks are red, the checks a landing runs, the line a stopped landing
leaves, and what becomes of the work item once the change lands. Each keeps its
name and its assertions.

| file | tests | moved | stay |
| --- | --- | --- | --- |
| `protectedpath_test.go` | 19 | 14 | 5 |
| `protectedtarget_test.go` | 7 | 7 | 0 |
| `redtarget_test.go` | 12 | 8 | 4 |
| `landingstop_test.go` | 10 | 9 | 1 |
| `landingchecks_test.go` | 10 | 9 | 1 |
| `landing_test.go` | 22 | 15 | 7 |

The 18 that stay reach `developerContract`, `grantEvidence`, `redTargetMarker`,
`redLandingMarker`, `updatingQueuedHead`, `mentionsPath`, `shortCommit`,
`renderLandingStop`, `renderFailureNotes`, `settleUndischarged`,
`arrangeUndischarged`, `undischargedParking`, or `itemSettled`. No exchange was
needed: Git draws every one of the six files' diffs as removals alone. The new
tests come to 171 KB in six files, the largest 46 KB, and the helpers they added
to `testharness` to about 15 KB.

The move tool now renames, in the comments of what it moves, a helper or member
it exports: a name that mixes cases wherever a comment uses it, a lower-case
one only where a comment quotes it as code, and any of them as the first word of
its own comment. It used to rename only top-level names, and any lower-case one
of eight letters or more wherever it appeared. That turned the ordinary words
"publishing" and "automatic" into the helpers `Publishing` and `Automatic` in
eleven comment lines of the first two groups, which read as they did before
the move again.

Timed side by side from 06:08 PDT on 2026-10-18, with
`go test -count=1 -json`, at a one-minute load between 7 and 16:

| tree | `internal/orchestrator` | `internal/orchestrator/landingtests` |
| --- | --- | --- |
| before, f2592b91 | passed, 1,057s | |
| after, this change | passed, 1,065s | passed, 89s |

The 62 tests' own elapsed times add up to 934 seconds, but at this load the
package's wall time did not move: the two runs are within nine seconds of each
other, the after run the slower, and its first minute and a half was shared
with the new package's binary. One pass each at a load that moved is not
evidence either way. What the move does show is the new binary passing in about
a minute and a half, beside the package rather than inside it.

### What is left

About 950 tests that can move, in groups of the size above. By subject and by
what they cost, a plan the development manager can take or change:

1. The other 43 movable tests in `pipeline_test.go`, in one change.
2. The merge queue: `mergequeueworker_test.go`, `mergequeuerecovery_test.go`,
   `mergequeuepromotion_test.go`, and `queuedchecks_test.go`.
3. Protection and landing: `protectedpath_test.go`, `protectedtarget_test.go`,
   `redtarget_test.go`, `landingstop_test.go`, `landingchecks_test.go`, and
   `landing_test.go`. Moved by the third group above.
4. Reconciliation and recovery: `reconcile_test.go`, `recovery_test.go`,
   `converge_test.go`, `runretirement_test.go`, `supersession_test.go`, and
   `integrationresume_test.go`.
5. `schedule_test.go`'s 103 movable tests, in two changes, and `rerun_test.go`
   and `triage_test.go` beside them.
6. The rest, a few files a change.

The 418 tests that reach internals take about 1,300 of the 8,136 seconds, so
once everything movable has moved the package's own binary carries about a
sixth of what it carries today.
