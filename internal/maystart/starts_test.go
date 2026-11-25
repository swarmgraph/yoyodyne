package maystart

import (
	"go/ast"
	"go/token"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	insideASession    = "a step of a session somebody listed here already started"
	asksTheProgram    = "asks the provider's program about itself, its sign-in or its settings, and gives it no work"
	operatorTalks     = "the operator's own conversation with a role, which always may start"
	roleAsks          = "a question asked inside a turn that was allowed to start, which always may start"
	opensConversation = "opens a conversation for a caller listed here, and takes no turn in it"
	judgesAPiece      = "judges whether a piece joins, for an epic that is open and readable while epic mode is on and for nothing else, as piece 9 of docs/designs/epic-mode-second-part.md rules"
	writesTheRecord   = "holds the run while its record, its pull request or what it left is settled; no session, check or developer slot"
)

// starts is every place in the module that starts a provider's session or takes
// the lease on a run, beside those in launches.
var starts = map[string]launch{
	// On the closed list.
	"internal/cli/cli.go RunContext reviewBranch":                                           starting(OperatorReview),
	"internal/cli/correction.go correctorFrom Corrector{}":                                  starting(PromptedPass),
	"internal/cli/correction.go correctorFrom roleCorrection{}":                             starting(PromptedPass),
	"internal/cli/correction.go roleCorrection.Wake Send":                                   starting(PromptedPass),
	"internal/cli/correction.go roleCorrection.Wake openChat":                               starting(PromptedPass),
	"internal/cli/escalate.go developmentManagerConversation.Judge Send":                    starting(PromptedPass),
	"internal/cli/escalate.go developmentManagerConversation.Judge openChat":                starting(PromptedPass),
	"internal/cli/escalate.go escalatorFrom Escalator{}":                                    starting(PromptedPass),
	"internal/cli/escalate.go escalatorFrom developmentManagerConversation{}":               starting(PromptedPass),
	"internal/cli/forgecheck.go teamBranchUpdaterFrom branchReviewerFrom":                   starting(TeamBranchUpdate),
	"internal/cli/recurring.go recurringTrigger Trigger{}":                                  starting(RecurringPass, PromptedPass, UnmetGoalReview),
	"internal/cli/recurring.go recurringTrigger roleConversation{}":                         starting(RecurringPass, PromptedPass, UnmetGoalReview),
	"internal/cli/recurring.go roleConversation.Wake Send (2)":                              starting(RecurringPass, PromptedPass, UnmetGoalReview),
	"internal/cli/recurring.go roleConversation.Wake Send (3)":                              starting(RecurringPass, PromptedPass, UnmetGoalReview),
	"internal/cli/recurring.go roleConversation.Wake Send":                                  starting(RecurringPass, PromptedPass, UnmetGoalReview),
	"internal/cli/recurring.go roleConversation.opener conversation open":                   starting(RecurringPass, PromptedPass, UnmetGoalReview),
	"internal/cli/recurring.go roleConversation.opener prepareChat":                         starting(RecurringPass, PromptedPass, UnmetGoalReview),
	"internal/cli/review.go branchReviewerFrom BranchReviewer{}":                            starting(TeamBranchUpdate, OperatorReview),
	"internal/cli/review.go branchReviewerFrom Reviewer{}":                                  starting(TeamBranchUpdate, OperatorReview),
	"internal/cli/review.go reviewBranch branchReviewerFrom":                                starting(OperatorReview),
	"internal/cli/review.go reviewBranch reviewer.Review":                                   starting(OperatorReview),
	"internal/orchestrator/branchreview.go BranchReviewer.conduct reviewer.Review":          starting(TeamBranchUpdate, OperatorReview),
	"internal/orchestrator/checkstagecontinue.go CheckStageContinuer.Continue AdoptRun":     starting(ContinueRun),
	"internal/orchestrator/correction.go Corrector.wake Wake":                               starting(PromptedPass),
	"internal/orchestrator/documentpublication.go Pipeline.publishDocumentAttempt AdoptRun": starting(PublishDocument),
	"internal/orchestrator/escalate.go Escalator.deliver Judge":                             starting(PromptedPass),
	"internal/orchestrator/forgefeedback.go Reconciler.takeUpFeedback AdoptRun":             starting(FeedbackRepair),
	"internal/orchestrator/githubidentity.go GitHubIdentityStops.takeUpPublishing AdoptRun": starting(AfterLogin),
	"internal/orchestrator/integrationresume.go IntegrationResumer.Resume AdoptRun":         starting(FinishMerge),
	"internal/orchestrator/mergequeuecatchup.go MergeQueueRunHandback.catchUp AdoptRun":     starting(BringUpToDate),
	"internal/orchestrator/outsidefilelanding.go Pipeline.continueOutsideFile AdoptRun":     starting(BringInFile),
	"internal/orchestrator/personmerge.go Reconciler.updatePersonMerge AdoptRun":            starting(UpdateForPerson),
	"internal/orchestrator/queuedchecks.go Reconciler.ContinueUpdates AdoptRun":             starting(PickUpRun),
	"internal/orchestrator/reconcile.go Reconciler.reconcileRun AdoptRun":                   starting(UpdateQueued),
	"internal/orchestrator/reconcilewait.go Reconciler.takeUpWait AdoptRun":                 starting(PickUpRun),
	"internal/orchestrator/recurring.go Trigger.run Wake":                                   starting(RecurringPass, PromptedPass, UnmetGoalReview),
	"internal/orchestrator/redtarget.go Reconciler.resumeRedTarget AdoptRun":                starting(UpdateQueued),
	"internal/orchestrator/repaircontinue.go RepairContinuer.Continue AdoptRun":             starting(DecidedRepair),
	"internal/orchestrator/schedule.go Scheduler.Schedule startIntakeRuns":                  starting(MessagesForWork),
	"internal/orchestrator/stallcontinue.go StallContinuer.Continue AdoptRun":               starting(ContinueRun),
	"internal/orchestrator/teambranchupdate.go teamBranchRun.review ReviewCandidate":        starting(TeamBranchUpdate),

	// What starts no work.
	"internal/backend/claudecode/backend.go Backend.CheckAvailability Command{} (2)":                    because(asksTheProgram),
	"internal/backend/claudecode/backend.go Backend.CheckAvailability Command{}":                        because(asksTheProgram),
	"internal/backend/claudecode/backend.go Backend.Run Command{}":                                      because(insideASession),
	"internal/backend/claudecode/backend.go Backend.installedVersion Command{}":                         because(asksTheProgram),
	"internal/backend/claudecode/settingscheck.go Backend.CheckLaunchSettings Command{}":                because(asksTheProgram),
	"internal/backend/claudecode/settingscheck.go Backend.ask Command{}":                                because(asksTheProgram),
	"internal/backend/claudecode/subagentcheck.go Backend.CheckSubagentLaunch Command{}":                because(asksTheProgram),
	"internal/backend/codex/backend.go Backend.CheckAvailability Command{} (2)":                         because(asksTheProgram),
	"internal/backend/codex/backend.go Backend.CheckAvailability Command{}":                             because(asksTheProgram),
	"internal/backend/codex/backend.go Backend.Run Command{}":                                           because(insideASession),
	"internal/backend/codex/backend.go Backend.installedVersion Command{}":                              because(asksTheProgram),
	"internal/backend/codex/mcp.go Backend.withMCPServersOff Command{}":                                 because(asksTheProgram),
	"internal/chat/chat.go Session.speakUntil Send (2)":                                                 because(operatorTalks),
	"internal/chat/chat.go Session.speakUntil Send":                                                     because(operatorTalks),
	"internal/chat/chat.go Session.takeTurn RunRequest{}":                                               because(insideASession),
	"internal/chat/chat.go Session.takeTurn Serve":                                                      because(insideASession),
	"internal/chat/repair.go Session.carryOutRepair Judge":                                              because("judges a repair of the backlog from records, with no session"),
	"internal/chat/repositoryask.go usageLimitNoting.Run provider.Run":                                  because(insideASession),
	"internal/chat/requestsize.go requestBounded.Run provider.Run (2)":                                  because(insideASession),
	"internal/chat/requestsize.go requestBounded.Run provider.Run":                                      because(insideASession),
	"internal/cli/chat.go conversationRepositoryAsk Asker{}":                                            because(roleAsks),
	"internal/cli/chat.go converse prepareChat":                                                         because(operatorTalks),
	"internal/cli/chat.go converseWith conversation open":                                               because(operatorTalks),
	"internal/cli/chat.go openChat openChatOnModel":                                                     because(opensConversation),
	"internal/cli/chat.go openChatOnModel conversation open":                                            because(opensConversation),
	"internal/cli/chat.go openChatOnModel prepareChat":                                                  because(opensConversation),
	"internal/cli/chat.go preparedChat.open conversation Open":                                          because(opensConversation),
	"internal/cli/chat.go preparedChat.open conversationExchanges":                                      because(roleAsks),
	"internal/cli/chat.go preparedChat.open conversationRepositoryAsk":                                  because(roleAsks),
	"internal/cli/chat.go runChatMessage Send":                                                          because(operatorTalks),
	"internal/cli/dashboard.go serveDashboard Serve":                                                    because("serves the dashboard's pages"),
	"internal/cli/dashboardacts.go decideInConversation conversation open":                              because("the operator's own decision, recorded in the conversation it is about; a document it publishes asks as a document"),
	"internal/cli/dashboardacts.go decideInConversation prepareChat":                                    because("the operator's own decision, recorded in the conversation it is about; a document it publishes asks as a document"),
	"internal/cli/epicjoin.go epicJoins.judge RunRequest{}":                                             because(judgesAPiece),
	"internal/cli/epicjoin.go epicJoins.judge provider.Run":                                             because(judgesAPiece),
	"internal/epicjoin/epicjoin.go Joiner.send Judge":                                                   because(judgesAPiece),
	"internal/orchestrator/epicjoin.go Scheduler.judgeEpicPieces Judge":                                 because(judgesAPiece),
	"internal/cli/exchange.go conversationExchanges Conductor{}":                                        because(roleAsks),
	"internal/cli/exchange.go conversationExchanges exchangeVoice{}":                                    because(roleAsks),
	"internal/cli/exchange.go exchangeVoice.Answer RunRequest{}":                                        because(roleAsks),
	"internal/cli/exchange.go exchangeVoice.Answer Serve":                                               because(roleAsks),
	"internal/cli/run.go pipelineFrom Reviewer{}":                                                       because(insideARun),
	"internal/cli/run.go reviewerForEndpoint Reviewer{}":                                                because(insideARun),
	"internal/cli/run.go supervisionLoopFrom Conductor{}":                                               because("closes an exchange nobody is answering; it has no voice to answer with"),
	"internal/cli/sidestream.go askAside sideThreads":                                                   because(operatorTalks),
	"internal/cli/sidestream.go preparedChat.sideThreads sideVoice{}":                                   because(operatorTalks),
	"internal/cli/sidestream.go sideVoice.Answer RunRequest{}":                                          because(operatorTalks),
	"internal/cli/sidestream.go sideVoice.Answer Serve":                                                 because(operatorTalks),
	"internal/cli/slack.go buildSlackSink productManagerConversation{}":                                 because(operatorTalks),
	"internal/cli/slack.go productManagerConversation.Say openChat":                                     because(operatorTalks),
	"internal/cli/slack.go sayToConversation Send":                                                      because(operatorTalks),
	"internal/dashboard/fixtureserver/main.go main Serve":                                               because("serves the dashboard's pages"),
	"internal/dashboard/server.go Server.Serve Serve":                                                   because("serves the dashboard's pages"),
	"internal/modelfailover/modelfailover.go Policy.runAlternate provider.Run (2)":                      because(insideASession),
	"internal/modelfailover/modelfailover.go Policy.runAlternate provider.Run":                          because(insideASession),
	"internal/modelfailover/modelfailover.go serveWithCapacity provider.Run (2)":                        because(insideASession),
	"internal/modelfailover/modelfailover.go serveWithCapacity provider.Run (3)":                        because(insideASession),
	"internal/modelfailover/modelfailover.go serveWithCapacity provider.Run (4)":                        because(insideASession),
	"internal/modelfailover/modelfailover.go serveWithCapacity provider.Run":                            because(insideASession),
	"internal/orchestrator/checkstagecontinue.go CheckStageContinuer.NoteWaiting AdoptRun":              because(writesTheRecord),
	"internal/orchestrator/claims.go ClaimAuditor.settle AdoptRun":                                      because(writesTheRecord),
	"internal/orchestrator/closeditems.go Reconciler.actOnClosedItemPublication AdoptRun":               because(writesTheRecord),
	"internal/orchestrator/converge.go Reconciler.sweepBranch AdoptRun":                                 because(writesTheRecord),
	"internal/orchestrator/converge.go Reconciler.sweepPublication AdoptRun":                            because(writesTheRecord),
	"internal/orchestrator/converge.go Reconciler.sweepWorktree AdoptRun":                               because(writesTheRecord),
	"internal/orchestrator/documentdecision.go DocumentDecider.adopt AdoptRun":                          because(writesTheRecord),
	"internal/orchestrator/documentpublication.go Pipeline.handOverDocument AdoptRun":                   because(writesTheRecord),
	"internal/orchestrator/documentpublication.go Pipeline.recordDocumentRetry AdoptRun":                because(writesTheRecord),
	"internal/orchestrator/escalationended.go Reconciler.endEscalation AdoptRun":                        because(writesTheRecord),
	"internal/orchestrator/mergedisagreement.go Reconciler.findMergeDisagreement AdoptRun":              because(writesTheRecord),
	"internal/orchestrator/mergequeueworker.go MergeQueueWorker.ask reviewer.Review":                    because("reviews a change already in the merge queue, which carries on to landing"),
	"internal/orchestrator/personmerge.go Reconciler.refusePersonMergeUpdate AdoptRun":                  because(writesTheRecord),
	"internal/orchestrator/personmerge.go Reconciler.settlePersonMerge AdoptRun":                        because(writesTheRecord),
	"internal/orchestrator/pipeline.go activeRun.attemptDevelopment RunRequest{}":                       because(insideARun),
	"internal/orchestrator/pipeline.go activeRun.attemptDevelopment provider.Run":                       because(insideARun),
	"internal/orchestrator/pipeline.go activeRun.attemptReview reviewer.Review":                         because(insideARun),
	"internal/orchestrator/publication.go Reconciler.finishPublication AdoptRun":                        because(writesTheRecord),
	"internal/orchestrator/publication.go Reconciler.recoverPublication AdoptRun":                       because("records the pull request of a landed promotion, and asks GitHub to merge it as the run was about to"),
	"internal/orchestrator/publication.go Reconciler.refreshPublication AdoptRun":                       because(writesTheRecord),
	"internal/orchestrator/rearm.go Rearmer.repeat AdoptRun":                                            because("asks GitHub to merge an approved change, as it would have by itself; it pays for nothing and takes no slot"),
	"internal/orchestrator/reconcilefinding.go Reconciler.maintainRunFindings AdoptRun":                 because(writesTheRecord),
	"internal/orchestrator/reconcilefinding.go Reconciler.recordReconcileFinding AdoptRun":              because(writesTheRecord),
	"internal/orchestrator/recoveryrequest.go RecoveredIntegrations.Wait AdoptRun":                      because(writesTheRecord),
	"internal/orchestrator/recoveryrequest.go RecoveredIntegrations.refuse AdoptRun":                    because(writesTheRecord),
	"internal/orchestrator/repaircontinue.go RepairContinuer.DeliverNotes AdoptRun":                     because(writesTheRecord),
	"internal/orchestrator/repaircontinue.go RepairContinuer.recordDispatchedContinuation AdoptRun (2)": because(writesTheRecord),
	"internal/orchestrator/repaircontinue.go RepairContinuer.recordDispatchedContinuation AdoptRun":     because(writesTheRecord),
	"internal/orchestrator/rerun.go Rerunner.handBackPublication AdoptRun":                              because(writesTheRecord),
	"internal/orchestrator/rerun.go Rerunner.retire AdoptRun":                                           because(writesTheRecord),
	"internal/orchestrator/reviewcomparison.go activeRun.compareDocumentReview reviewer.Review":         because(insideARun),
	"internal/orchestrator/runretirement.go RunRetirer.Retire AdoptRun":                                 because(writesTheRecord),
	"internal/orchestrator/teammergesettle.go Reconciler.settleTeamMerge AdoptRun":                      because(writesTheRecord),
	"internal/orchestrator/teampullrequest.go Reconciler.adoptTeamWait AdoptRun":                        because(writesTheRecord),
	"internal/repositoryask/asker.go Asker.Ask RunRequest{}":                                            because(insideASession),
	"internal/repositoryask/asker.go Asker.Ask provider.Run":                                            because(insideASession),
	"internal/review/reviewer.go Reviewer.Review RunRequest{}":                                          because(insideASession),
	"internal/review/reviewer.go Reviewer.Review provider.Run":                                          because(insideASession),
	"internal/runrecover/recover.go Recoverer.Recover AdoptRun":                                         because("writes the development manager's request on a run; what then takes the run up asks under its own path"),
	"internal/runstate/exchange.go ExchangeStore.Hold TryLeasePath":                                     because("a lease on something other than a run"),
	"internal/runstate/homemigrate.go HomeInFlight TryLeasePath":                                        because("looks whether a run is held, and starts nothing"),
	"internal/runstate/sidestream.go SideStreamStore.Hold TryLeasePath":                                 because("a lease on something other than a run"),
	"internal/runstate/sidestream.go SideStreamStore.Open TryLeasePath":                                 because("a lease on something other than a run"),
	"internal/runstate/slotrelease.go Store.releaseSlot takeReleaseLease":                               because("gives back the slot of a run whose process is gone"),
	"internal/runstate/slotrelease.go Store.takeReleaseLease takeLease":                                 because("gives back the slot of a run whose process is gone"),
	"internal/runstate/store.go Store.AdoptRun takeLease":                                               because("the lease itself, taken for whoever called; each caller is listed"),
	"internal/runstate/store.go Store.AdoptWithin takeLease":                                            because("the lease itself, taken for whoever called; each caller is listed"),
	"internal/runstate/store.go Store.reserve takeLease":                                                because("the lease itself, taken for whoever called; each caller is listed"),
	"internal/runstate/supervision.go SupervisionStore.Lease TryLeasePath":                              because("a lease on something other than a run"),
	"internal/runstate/watch.go WatchStore.Held TryLeasePath":                                           because("a lease on something other than a run"),
	"internal/runstate/watch.go WatchStore.Lease TryLeasePath":                                          because("a lease on something other than a run"),
	"internal/slack/state.go Store.Lease TryLeasePath":                                                  because("a lease on something other than a run"),
	"internal/slack/state.go Store.PassLease TryLeasePath":                                              because("a lease on something other than a run"),
	"internal/spend/spend.go Metered.Run provider.Run":                                                  because(insideASession),
	"scripts/validationfixture/main.go holdRun TryLeasePath":                                            because("looks whether a run is held, and starts nothing"),
}

// startNames are the functions that start a session, or make what starts one, or
// take a lease, and startTypes what is made to start one. A finder that reads
// names and not types has to be told of a new one; a session itself can be asked
// for only with a RunRequest, which is seen wherever it is made.
var startNames = []string{
	"AdoptRun", "takeLease", "takeReleaseLease", "TryLeasePath",
	"prepareChat", "openChat", "openChatOnModel", "sideThreads", "conversationExchanges", "conversationRepositoryAsk",
	"branchReviewerFrom", "startIntakeRuns", "Send", "Wake", "Judge", "ReviewCandidate", "Serve",
}

var startTypes = []string{
	"RunRequest", "Reviewer", "BranchReviewer", "Asker", "Conductor", "Escalator", "Corrector", "Trigger",
	"exchangeVoice", "sideVoice", "roleConversation", "developmentManagerConversation", "roleCorrection", "productManagerConversation",
}

// startsIn is each such place in a source file, named as launchesIn says. A
// command made inside a provider adapter is a process of that provider.
func startsIn(name string, source []byte) ([]string, error) {
	inAdapter := strings.HasPrefix(name, "internal/backend/")
	return usesIn(name, source, func(node ast.Node, add func(string)) {
		switch node := node.(type) {
		case *ast.Ident:
			// Named at all, so that a value made of it is seen as well as a call.
			if node.Name == "reviewBranch" {
				add("reviewBranch")
			}
		case *ast.BasicLit:
			if said, err := strconv.Unquote(node.Value); err == nil && node.Kind == token.STRING && reviewCommand.MatchString(said) {
				add(reviewProcess)
			}
		case *ast.SelectorExpr:
			chosen, on := node.Sel.Name, strings.ToLower(expressionName(node.X))
			switch {
			case slices.Contains(startNames, chosen):
				add(chosen)
			case chosen == "Run" && (strings.HasSuffix(on, "provider") || strings.HasSuffix(on, "backend")):
				add("provider.Run")
			case chosen == "Review" && (strings.Contains(on, "review") || strings.HasSuffix(on, "onmodel")):
				add("reviewer.Review")
			case chosen == "Open" && on == "chat", chosen == "open" && on == "prepared":
				add("conversation " + chosen)
			}
		case *ast.CallExpr:
			if called, ok := node.Fun.(*ast.Ident); ok && slices.Contains(startNames, called.Name) {
				add(called.Name)
			}
			switch lastName(node.Fun) {
			case "Command", "CommandContext", "append":
				if namesReview(node.Args) {
					add(reviewProcess)
				}
			}
		case *ast.CompositeLit:
			if list, ok := node.Type.(*ast.ArrayType); ok && lastName(list.Elt) == "string" && namesReview(node.Elts) {
				add(reviewProcess)
			}
			made := lastName(node.Type)
			if slices.Contains(startTypes, made) || inAdapter && made == "Command" {
				add(made + "{}")
			}
		}
	})
}

// reviewProcess is a place that names `yoyo review` as a command to run: the verb
// among a command's arguments, or a command line opening with it.
const reviewProcess = "yoyo review process"

var reviewCommand = regexp.MustCompile(`^\s*(\S*/)?yoyo\s+review\b`)

func namesReview(arguments []ast.Expr) bool {
	return slices.ContainsFunc(arguments, func(argument ast.Expr) bool {
		literal, ok := argument.(*ast.BasicLit)
		return ok && literal.Value == `"review"`
	})
}

// onlyTheCommand is what is wrong among the places found with who uses the
// exemption of `yoyo review`: it is the command's alone, and no listing lets
// another place have it.
func onlyTheCommand(found map[string]bool) []string {
	var problems []string
	for use := range found {
		parts := strings.SplitN(use, " ", 3) // the file, the function, and what is used
		if strings.HasPrefix(parts[2], "reviewBranch") && use != "internal/cli/cli.go RunContext reviewBranch" {
			problems = append(problems, use+" uses the review that always starts, which is only the yoyo review command's; it asks under a path of its own")
		}
		if strings.HasPrefix(parts[2], reviewProcess) {
			problems = append(problems, use+" runs yoyo review, which is only a person's to run; it asks under a path of its own")
		}
	}
	return problems
}

// expressionName is the last name of an expression, through a call to its function.
func expressionName(expression ast.Expr) string {
	if call, ok := expression.(*ast.CallExpr); ok {
		expression = call.Fun
	}
	return lastName(expression)
}

// TestEverySessionAndEveryLeaseOnARunIsOnTheClosedList fails on a place that
// starts a provider's session or takes a lease on a run and is neither on the
// closed list nor said to start no work, with why. A place that starts work and
// is on no path is put on the closed list unwired, which refuses `yoyo epic
// start` naming it, and never among those that start no work.
func TestEverySessionAndEveryLeaseOnARunIsOnTheClosedList(t *testing.T) {
	t.Parallel()
	found := usesInModule(t, startsIn)
	for _, problem := range append(unlisted(found, starts), onlyTheCommand(found)...) {
		t.Error(problem)
	}
	listed := map[Path]bool{}
	for _, places := range []map[string]launch{launches, starts} {
		for _, place := range places {
			for _, path := range place.paths {
				listed[path] = true
			}
		}
	}
	for _, entry := range Paths() {
		if !listed[entry.Path] {
			t.Errorf("no place in the code is listed as starting %q", entry.Path)
		}
	}
}

// The exemption of `yoyo review` is the command's alone: another use of its
// function, or the command run as a process, fails whatever is listed.
func TestOnlyTheCommandUsesTheReviewThatAlwaysStarts(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`func f() { reviewBranch(ctx, args, out, out) }`,
		`func f() { run := reviewBranch; run(ctx, args, out, out) }`,
		`func f() { run(map[string]command{"look": reviewBranch}) }`,
		`func f() { reviewBranch(ctx, a, out, out); reviewBranch(ctx, b, out, out) }`,
		`func f() { runner.Run(ctx, execution.Command{Name: yoyo, Args: []string{"review", "--base", base}}) }`,
		`func f() { cli.Run(ctx, []string{"review"}, out, out) }`,
		`func f() { exec.CommandContext(ctx, yoyo, "review", "--base", base) }`,
		`func f() { exec.Command(yoyo, "review") }`,
		`func f() { args = append(args, "review") }`,
		`func f() { shell(ctx, "./bin/yoyo review --base main") }`,
		`const line = "yoyo review --base main"`,
	} {
		uses, err := startsIn("internal/cli/other.go", []byte("package planted\n"+source+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		listed := map[string]launch{}
		for _, use := range uses {
			found[use], listed[use] = true, starting(OperatorReview)
		}
		if len(onlyTheCommand(found)) == 0 || len(unlisted(found, nil)) == 0 {
			t.Errorf("%s: passes; found %q", source, uses)
		}
		if len(unlisted(found, listed)) != 0 {
			t.Errorf("%s: %q", source, unlisted(found, listed))
		}
	}
	for _, entry := range Paths() {
		if entry.AlwaysStarts != (entry.Path == OperatorReview) {
			t.Errorf("%s always starts: %t; only yoyo review does", entry.Path, entry.AlwaysStarts)
		}
	}
}

func TestTheSweepSeesEachSessionAndLease(t *testing.T) {
	t.Parallel()
	for source, want := range map[string]string{
		`func f() { r.Store.AdoptRun(ctx, id) }`:                                     "x.go f AdoptRun",
		`func f() { take := store.AdoptRun; _ = take }`:                              "x.go f AdoptRun",
		`func f() { runstate.TryLeasePath(path, "a run") }`:                          "x.go f TryLeasePath",
		`func (s *Store) f() { s.takeLease(ctx, id) }`:                               "x.go Store.f takeLease",
		`func f() { openChat(ctx, role) }`:                                           "x.go f openChat",
		`func f() { session.Send(ctx, said) }`:                                       "x.go f Send",
		`func f() { parts.provider.Run(ctx, request) }`:                              "x.go f provider.Run",
		`func f() { go func() { x.Run(ctx, backend.RunRequest{}) }() }`:              "x.go f RunRequest{}",
		`func f() { modelfailover.Serve(ctx, p, request, policy) }`:                  "x.go f Serve",
		`func f() { branchReviewerFrom(parts, "").ReviewCandidate(ctx, candidate) }`: "x.go f ReviewCandidate; x.go f branchReviewerFrom",
		`var judge = func() { e.Manager.Judge(ctx, run) }`:                           "x.go  Judge",
		`func f() { command.Run(); other.Review() }`:                                 "",
	} {
		uses, err := startsIn("x.go", []byte("package planted\n"+source+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(uses)
		if strings.Join(uses, "; ") != want {
			t.Errorf("%s: found %q, want %q", source, uses, want)
		}
		for _, use := range uses {
			if len(unlisted(map[string]bool{use: true}, nil)) != 1 {
				t.Errorf("%s: a place that is not listed passes", source)
			}
		}
	}
	adapter, err := startsIn("internal/backend/other/backend.go", []byte("package other\nfunc f() { r.Run(ctx, execution.Command{}) }\n"))
	if err != nil || len(adapter) != 1 {
		t.Errorf("a provider's process started in an adapter: %q, %v", adapter, err)
	}
}
