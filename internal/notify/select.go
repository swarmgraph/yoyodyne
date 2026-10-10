package notify

// What is worth saying, read from the record rather than from whatever is
// executing the work.
//
// Selection is a pure comparison of two readings of a durable record, which is
// what makes reporting an observation instead of a gate: nothing here is called
// from a run, so nothing here can slow one, fail one, or park one. A sink that
// was away comes back, re-reads, and finds exactly the crossings it missed.
//
// It reports transitions rather than states, so each one is said once however
// often the record is read. That is the same discipline the conversation's
// activity lines follow, and it is what keeps a thread a narrative rather than
// an event log scrolling sideways.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// FromRun reports what a run crossed between two readings of its durable state.
// A zero-valued before is a run this sink has not reported on yet, so everything
// its record already holds is a crossing: a sink that starts late says what
// happened rather than pretending the run began where it was first read.
//
// look is what the repository holds of a run's change, asked the way the docket,
// the pull's hold, and `yoyo status` ask it (readmodel.Looking). It is asked of
// every ending that says what survives, because a sink catching up hours late
// reads a record the repository may have moved on from. Nil is a reader without
// a repository; a wired reader uses the same check as the hold.
func FromRun(before, after runstate.State, look func(runstate.State) triage.Found) ([]Notification, error) {
	if strings.TrimSpace(after.RunID) == "" {
		return nil, nil
	}
	topic, err := WorkItem(after.WorkItemID)
	if err != nil {
		return nil, fmt.Errorf("address run %s: %w", after.RunID, err)
	}
	// The record carries what the item is called, so the thread this run opens can
	// be named in words rather than in the identifier alone. A run recorded before
	// titles were written carries none, and its topic is addressed exactly as it
	// was before there was one.
	topic = topic.WithTitle(after.WorkItemTitle)
	refs := Refs{RunID: after.RunID, WorkItemID: after.WorkItemID}
	if after.PullRequest != nil {
		refs.PullRequest = after.PullRequest.URL
	}
	at := after.UpdatedAt
	if at.IsZero() {
		at = after.StartedAt
	}
	// A different run is a different narrative, so the previous one's record is
	// no baseline for it: compared against a run that had already been promoted,
	// a fresh attempt at the same item would read as one that lost its promotion.
	started := before.RunID != after.RunID
	if started {
		before = runstate.State{}
	}

	var crossed []Notification
	sayWith := func(kind Kind, severity report.Severity, speaker Speaker, detail Detail, text string) {
		eventRefs := refs
		if kind == KindRunParked && after.DirectivePause != nil {
			eventRefs.DirectiveID = after.DirectivePause.DirectiveID
		}
		crossed = append(crossed, Notification{
			Topic:   topic,
			Speaker: speaker,
			Event: Event{
				Kind:     kind,
				At:       at,
				Severity: severity,
				Refs:     eventRefs,
				Detail:   detail,
				Text:     text,
			},
		})
	}
	say := func(kind Kind, severity report.Severity, speaker Speaker, detail Detail) {
		sayWith(kind, severity, speaker, detail, "")
	}

	// A run starting is the one crossing whose speaker varies, and it follows the
	// selector the record names: a persona must not narrate a selection it never
	// made.
	if started {
		say(KindRunStarted, report.SeverityNote, selectionSpeaker(after.Selection), startedDetail(after))
	}
	if parked(before) && !parked(after) && !after.Status.Terminal() {
		say(KindRunContinued, report.SeverityNote, Harness(), Detail{})
	}
	if !checksBehind(before) && checksBehind(after) {
		say(KindChecksPassed, report.SeverityNote, Harness(), Detail{Checks: stageSpend(after)})
	}
	// A failing check is said whenever the recorded failure changes, so a second
	// repair attempt that fails differently is news rather than silence. A repeat
	// of exactly the same failure is the same fact and is said once.
	if after.CheckFailure != nil && (before.CheckFailure == nil || *before.CheckFailure != *after.CheckFailure) {
		say(KindChecksFailed, report.SeverityWarning, Harness(), Detail{
			Command:  after.CheckFailure.Command,
			ExitCode: after.CheckFailure.ExitCode,
		})
	}
	// A refusal by the gate in front of the checks is said once per refusal, as
	// the round it bought. The record writes the refusal and the round in one
	// save, so the repair count moving under a refusal is what says a round was
	// spent on it: two readings carrying the same refusal on the same count are
	// one refusal read twice, and the same paths refused again on a later count
	// are a second round spent for the same stated cause, which under the
	// communication rule is a second thread line rather than silence. A refusal
	// that found the budget already gone spends no round and moves no count —
	// the run blocks on it, and the blocker line names the paths — so it is not
	// said here, where the developer's line would promise an attempt the run is
	// not going to make.
	//
	// The developer speaks it, and that is deliberate where the harness speaks
	// the failing check beside it. The gate is a string comparison rather than a
	// judgement, and what the line is about is the developer's own change reaching
	// outside its item and the developer taking it back out — an account the
	// developer can give of its own act, where a persona narrating a check's
	// verdict would be claiming one it never reached.
	if refusalBoughtARound(before, after) {
		say(KindPathRefused, report.SeverityWarning, Persona(domain.RoleDeveloper, ""), Detail{
			RefusedPaths: after.PathRefusal.Paths,
			OmittedPaths: after.PathRefusal.Omitted,
			Grants:       after.PathRefusal.Grants,
		})
	}
	// A verdict is keyed on the invocation that gave it rather than on the words
	// it used, because a repair loop produces several and most of them say the
	// same word. The decision alone would report the first request for repairs
	// and silently swallow every one after it, leaving a thread that reads as one
	// repair followed by an approval with the rounds between them missing. The
	// reviewer's session is cleared and re-recorded around every review, so two
	// verdicts are always two sessions; the decision is compared beside it so a
	// verdict recorded without a session is still not lost. Neither field moves
	// while a run is repairing, so the verdict standing over a repair round is
	// not said a second time.
	if verdictGiven(after) && (after.ReviewSessionID != before.ReviewSessionID || after.ReviewDecision != before.ReviewDecision) {
		verdict := KindReviewRepairs
		if after.ReviewDecision == runstate.ReviewApprove {
			verdict = KindReviewApproved
		}
		say(verdict, report.SeverityNote, Persona(domain.RoleReviewer, ""), Detail{
			Findings:  after.ReviewFindings,
			Requested: describeFindings(after.ReviewFindingDetails),
		})
	}
	// A lost race is the harness's own act as the promotion is, and it is said
	// once per race: the run's count moving is the race, recorded before the
	// replay begins. It is a note, because nothing is wrong with the change and
	// nothing waits on anybody — the replay re-earns the gate by itself.
	if after.IntegrationRetries > before.IntegrationRetries {
		say(KindRaceLost, report.SeverityNote, Harness(), Detail{
			TargetBranch: after.TargetBranch,
			Races:        after.IntegrationRetries,
		})
	}
	// A promotion is the harness's own act — no agent performs one — so the
	// harness is the speaker rather than any persona.
	if before.Integration == nil && after.Integration != nil {
		say(KindPromoted, report.SeverityNote, Harness(), Detail{
			TargetBranch: after.Integration.TargetBranch,
			Commit:       after.Integration.TargetCommit,
		})
	}
	if before.PullRequest == nil && after.PullRequest != nil {
		say(KindPublished, report.SeverityNote, Harness(), Detail{PullRequest: describePullRequest(after.PullRequest)})
	}
	if mergeQueued(after) && !mergeQueued(before) {
		say(KindMergeQueued, report.SeverityNote, Harness(), Detail{PullRequest: describePullRequest(after.PullRequest)})
	}
	if merged(after) && !merged(before) {
		say(KindMergeCompleted, report.SeverityNote, Harness(), Detail{PullRequest: describePullRequest(after.PullRequest)})
	}
	// A merge that is not going to happen, said as loudly as a park by a cause
	// nobody chose is. Nobody decided it, the change is promoted and reads as
	// landed everywhere else, and what follows it is a publication sitting on the
	// forge for as long as it takes somebody to notice — which on 2026-08-20 was
	// four promotions and most of a day.
	//
	// It is read from the moment the record holds rather than from the queued flag
	// going out: the flag is cleared by a merge that happened as well as by one
	// that did not, and a sink that had not yet seen the merge queued would have
	// no crossing to compare against at all. A drop the record stamps is a fact
	// about the run, so a sink reading the record for the first time hours later
	// says it exactly as one watching would have.
	if after.MergeDrop != nil && before.MergeDrop == nil {
		say(KindMergeDropped, report.SeverityWarning, Harness(), Detail{
			PullRequest: describePullRequest(after.PullRequest),
			Cause:       after.MergeDrop.Reason,
		})
	}
	// A merge the harness withdrew because its checks failed on the target itself
	// is said once, as the wait begins, naming the check and the item filed for
	// it. It is read from the moment the record holds, as a drop is.
	if waiting := targetRedOf(after); waiting != nil && (targetRedOf(before) == nil || !targetRedOf(before).At.Equal(waiting.At)) {
		say(KindMergeWaitingOnTarget, report.SeverityWarning, Harness(), Detail{
			PullRequest: describePullRequest(after.PullRequest),
			Cause:       waiting.Describe(),
		})
	}
	// A landing is said once its checks have ended, whichever way, and never
	// while they run: what a thread wants of it is the result, and a red one is
	// the one fact about a landed change that the run's own ending does not
	// carry. It is read from the record's own sentence rather than worded here,
	// so the channel, `yoyo status`, and the item's note say one thing about it.
	if landed(after) && !landed(before) {
		kind, severity := KindLandingGreen, report.SeverityNote
		switch {
		case after.LandingChecks.Red():
			kind, severity = KindLandingRed, report.SeverityWarning
		case after.LandingChecks.Unverified():
			kind, severity = KindLandingUnverified, report.SeverityWarning
		}
		say(kind, severity, Harness(), Detail{Landing: after.LandingChecks.Describe()})
	}
	if !parked(before) && parked(after) {
		say(KindRunParked, parkSeverity(after), Harness(), Detail{Cause: causeOf(after)})
	}
	// A run that ended without landing, said in the read model's own outcome
	// vocabulary rather than in one word over all four. A stoppage somebody has to
	// decide about is said as loudly as whoever decides it and whatever stopped it
	// warrant — see stoppageSeverity; the other three endings leave nobody a
	// decision and are said as themselves rather than as a blocker nobody
	// recorded. Both state what remains of the change, because the attempt being
	// over says nothing about whether the work is.
	//
	// There are two crossings rather than one because the two facts are written in
	// two saves on a path that matters. Reconciliation settles a run some killed
	// process already left terminal: it takes a blocker from the tracker and puts
	// it on a record whose status has not moved since the crash. Watching only for
	// the run becoming terminal, the sink would have said "failed" at the crash
	// and then nothing at all when the stoppage arrived — the ending an operator
	// most needs, silently swallowed by the fact the run was already over. So a
	// blocker appearing on a run that had already ended is itself a crossing, and
	// the stoppage line it says corrects the ending that preceded it.
	endedNow := !endedWithoutLanding(before) && endedWithoutLanding(after)
	stoppageNow := !handedToAPerson(before) && handedToAPerson(after)
	// A run whose own change merged and whose publication finished is never said
	// to have stopped, whatever blocker reaches its record afterwards: a blocker on
	// such a record is about the item rather than about this run, and a stop notice
	// naming the run that landed the work sends whoever reads it to recover
	// finished work.
	if (endedNow || stoppageNow) && !after.MergedAndComplete() {
		found := lookedAt(after, look)
		remains := Detail{Remains: after.Artifacts().Describe()}
		if found != nil {
			remains.Remains = found.DescribeRemains() + "; " + found.Describe()
		}
		// An approved change the environment stopped is the one ending here whose
		// move is neither a decision nor nothing: the harness resumes it, by a verb,
		// once the cause has cleared. The table's clauses for both kinds below say
		// otherwise of it — a decision in triage, or nothing recorded for anybody —
		// so the move is the record's own sentence instead, the one the docket entry
		// carries and the repair verb refuses in. That holds while the run's branch
		// is there; once it is gone the resume would refuse, so the line says what
		// is gone and that a re-run is the way on, as those surfaces then do.
		// A stoppage is new when it is said, so no triage decision can stand about
		// it yet and the carry-out the read model also distinguishes cannot arise.
		mover := readmodel.StoppageMover(after, found, false)
		if after.IntegrationStop != nil {
			remains.Mover = integrationMove(after, mover == readmodel.MoverHarness, found)
		}
		// A check stage its bound stopped says that load stopped it rather than the
		// change, and whose move follows — the harness's, continuing it at its checks,
		// until its continuations are spent — in the sentence the docket entry
		// carries.
		if says := after.CheckStageStopSays(); says != "" {
			whose := "the development manager's"
			if mover == readmodel.MoverHarness {
				whose = "the harness's"
			}
			remains.Mover = whose + " — " + says
		}
		// A silent-stream stall the sweep settled says the same way that the harness
		// stopped it rather than anything judging the change, and whose move follows.
		if says := after.StallStopSays(); says != "" {
			whose := "the development manager's"
			if mover == readmodel.MoverHarness {
				whose = "the harness's"
			}
			remains.Mover = whose + " — " + says
		}
		if outcome := after.Outcome(); outcome == runstate.OutcomeStopped {
			sayWith(KindBlockerRecorded, stoppageSeverity(after, mover), Harness(), remains, endingReason(after))
		} else {
			remains.Ending = string(outcome)
			// A run that died before it claimed anything is the one ending here that
			// does leave somebody a decision: it is docketed as it dies, so the fixed
			// clause's "nothing was recorded for anybody to decide" is false of exactly
			// this run and true of the others. It is derived from the record beside the
			// line rather than worded again by the sink, for the reason the stall's
			// clause is.
			if after.DiedBeforeClaiming() {
				remains.Mover = unstartedMove
			}
			if after.Escalated() {
				remains.Mover = mover.Possessive() + " — the run raised the work item as one that cannot be met as written; its docket entry is where the development manager decides what happens next."
			}
			sayWith(KindRunEnded, endingSeverity(outcome), Harness(), remains, endingReason(after))
		}
	}
	return crossed, nil
}

// endingReason is what the record gives as the reason a run ended, or the
// absence stated as itself. Both lines above finish on it, so an empty one would
// leave a sentence trailing off a colon — and the generic absence the renderer
// falls back to is written for an agent's own words going missing, which is a
// defect, where this is usually not one.
//
// A run ending with no reason recorded is ordinary rather than broken. A
// cancellation is the plain case: the operator stopped it and owed nobody a
// sentence about why. Saying the record names no reason is the true account of
// it; implying one had gone missing would report an ordinary act as a fault in
// the record. A stoppage reconciliation settled on a run that was already
// terminal is no longer such a case: the sweep writes its own reason where the
// record gives none, and a record settled before it did still carries the
// blocker, which the read model's reason stands in where the failure is empty.
//
// The derivation is the read model's (runstate.State.Reason), so `yoyo status`
// and this line give one reason for one run, opening with the class that
// stopped it. What this adds is the channel's own phrasing around it: a round
// the environment refused is said before the failure, and an outstanding
// publication, which status prints under its own label, is the account where
// the record gives nothing else.
func endingReason(state runstate.State) string {
	reason := state.Reason()
	if reason == "" {
		reason = strings.TrimSpace(state.PublishFailure)
	}
	if reason == "" {
		reason = runstate.NoReasonSays
	}
	if state.RecordedStopClass() == runstate.StopUnknown {
		reason = string(runstate.StopUnknown) + ": " + reason
	}
	if strings.TrimSpace(state.Failure) != "" {
		return environmentallyRefused(state) + reason
	}
	return reason
}

// integrationMove is whose move follows an approved change the environment
// stopped: the harness's, by `yoyo triage resume`, in the sentence the run's
// record words for every surface — while its branch is there, by the read
// model's reading of the stoppage (readmodel.StoppageMover, over
// triage.IntegrationResumable) that the docket, the pull's hold, `yoyo status`,
// and the repair verb's refusal share. Once it is gone the move is the sentence
// those surfaces say of it instead, naming the re-run and no verb that would
// refuse. It is derived beside the fact the message states rather than worded
// again here, so the channel line and those surfaces cannot come to say
// different things about one run.
func integrationMove(state runstate.State, resumable bool, found *triage.Found) string {
	if resumable {
		return "the harness's — " + state.IntegrationStop.ResumeSays(state.RunID)
	}
	if found == nil {
		return triage.IntegrationGoneSays(state.RunID, fmt.Sprintf("branch %s removed as the run's record says, not checked", state.Branch))
	}
	return triage.IntegrationGoneSays(state.RunID, found.Describe())
}

// lookedAt asks once as an ending is said, and is nil only for a reader
// without a repository lookup.
func lookedAt(state runstate.State, look func(runstate.State) triage.Found) *triage.Found {
	if look == nil {
		return nil
	}
	found := look(state)
	return &found
}

// stoppageCause is the environmental cause the record gives for a stoppage: the
// integration stop's where an approved change was stopped on its way to the
// target, and otherwise the refusal a settled round was classified by. A cause
// recorded on a round that delivered a change anyway is not one: that round
// spent as any round does, and what stopped the run is the work.
func stoppageCause(state runstate.State) (runstate.EnvironmentalCause, bool) {
	if stop := state.IntegrationStop; stop != nil {
		return stop.Cause, true
	}
	if refused := state.Environmental; refused != nil && refused.Refused {
		return refused.Cause, true
	}
	return "", false
}

// stoppageSeverity is how loudly a stoppage is said, from who moves next and
// what stopped it. Critical is what reaches the operator wherever he is, so it
// is kept for a stoppage that is his to act on: on 2026-09-25 he was paged as
// critical for an approved change that lost its race for main twice and waited
// on the development manager to re-run it, which was ordinary.
//
// The mover is the read model's (readmodel.StoppageMover) and is not relabelled
// here. What makes a stoppage the operator's even so is its cause: one only a
// person clears on the machine — a target branch that diverged from the
// remote's, a credential the remote refused, a primary checkout carrying state
// the harness does not own. The harness's resume or her decision is still the
// next move the message names, and neither can happen until he has, so that
// cause is critical whoever moves after him.
//
// Otherwise a stoppage is one of two things. Where the environment stopped it —
// a lost race, a replay the harness killed, a tracker or forge that did not
// answer, a usage window, any refusal the settle classified — nothing was
// judged and the move is routine, so it is a note, and a note reaches the
// item's thread rather than the channel (see reachOf). Where the work stopped
// it — findings nobody repaired, checks that kept failing, paths the item never
// granted, a replay that conflicted, or anything the record does not name as
// the environment's — it is a warning: a real decision about the change, and
// the development manager's to make.
func stoppageSeverity(state runstate.State, mover readmodel.Mover) report.Severity {
	cause, environmental := stoppageCause(state)
	if mover == readmodel.MoverOperator || (environmental && cause.NeedsAPerson()) {
		return report.SeverityCritical
	}
	if environmental || state.LostItsRace() {
		return report.SeverityNote
	}
	return report.SeverityWarning
}

// endingSeverity is how loudly a run ending without a blocker is said. A
// cancellation is a note because somebody chose it and it is no verdict on the
// change; a run the harness could not carry and one it stopped on time are
// warnings, because nobody chose either and what follows both is silence that
// looks exactly like work in progress.
func endingSeverity(outcome runstate.RunOutcome) report.Severity {
	if outcome == runstate.OutcomeCancelled || outcome == runstate.OutcomeSucceeded {
		return report.SeverityNote
	}
	return report.SeverityWarning
}

// FromReport says what an agent noticed while its own work carried on. The
// speaker is the reporting role itself, because a report is the one outbound
// message that is entirely the agent's own words, and the severity is the one
// the agent chose rather than one derived from anything here.
func FromReport(reported report.Report) (Notification, error) {
	topic, err := topicForItem(reported.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address report %s: %w", reported.ID, err)
	}
	speaker := Persona(reported.Role, reported.Agent)
	// A report the harness filed itself is said in the harness's voice, which is
	// the one speaker that is not a persona; see report.HarnessReporter.
	if reported.Role == report.HarnessReporter {
		speaker = Speaker{}
	}
	return Notification{
		Topic:   topic,
		Speaker: speaker,
		Event: Event{
			Kind:     KindReportFiled,
			At:       reported.RecordedAt,
			Severity: reported.Severity,
			Refs:     Refs{RunID: reported.RunID, WorkItemID: reported.WorkItemID},
			Text:     reported.Message,
		},
	}, nil
}

// FromUsageLimit says that a provider refused the harness for want of capacity
// somewhere that is not a run. A run says the same thing by parking, and this is
// how every other process says it: the conversation turn or the review that was
// stopped, what the limit was, and when the provider said it lifts.
//
// It is a warning for the reason a park by the same cause is. Nobody chose it,
// nothing else in the record says it happened, and what follows it is hours of
// silence that look exactly like a healthy quiet queue. The speaker is the
// harness, because a provider running out of capacity is not any persona's act
// and no role should be made to narrate one.
// A refusal something else served through is the same record read the other way
// round, and is said as one: nothing stopped, so it is a note rather than a
// warning, and what it carries is which model was moved off, which one the work
// is being produced by instead, and — in the cause — why. Both reasons are said
// the same way because they are the same news to a reader: the model behind the
// work is not the one the configuration names. Silence would be the wrong answer
// to either, whatever it saved.
func FromUsageLimit(exhaustion runstate.UsageLimitExhaustion) (Notification, error) {
	topic, err := topicForItem(exhaustion.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address usage limit refusal at %s: %w", exhaustion.At.UTC().Format(time.RFC3339), err)
	}
	kind, severity := KindUsageLimitExhausted, report.SeverityWarning
	if exhaustion.Substituted() {
		kind, severity = KindModelSubstituted, report.SeverityNote
	}
	return Notification{
		Topic:   topic,
		Speaker: Harness(),
		Event: Event{
			Kind:     kind,
			At:       exhaustion.At,
			Severity: severity,
			Refs: Refs{
				WorkItemID:     exhaustion.WorkItemID,
				ConversationID: exhaustion.ConversationID,
			},
			Detail: Detail{
				Waiting: exhaustion.Waiting,
				Cause:   exhaustion.Describe(),
				// Each model qualified by the provider that was asked for it where the
				// turn crossed from one to the other, so a substitution between two
				// providers that spell one model name still reads as a substitution.
				Model:    exhaustion.DescribeModel(),
				ServedBy: exhaustion.DescribeServedBy(),
			},
		},
	}, nil
}

// FromProposal says that a role asked the owner of a document for a change to
// it. Both halves the agent wrote are carried: what should become true, and the
// case for it, which is the whole of what the owner decides on.
func FromProposal(proposal amendment.Proposal) (Notification, error) {
	topic, err := topicForItem(proposal.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address proposal %s: %w", proposal.ID, err)
	}
	text := strings.TrimSpace(proposal.Change)
	if why := strings.TrimSpace(proposal.Why); why != "" {
		text += " — " + why
	}
	return Notification{
		Topic:   topic,
		Speaker: Persona(proposal.Role, proposal.Agent),
		Event: Event{
			Kind:     KindProposalRaised,
			At:       proposal.RaisedAt,
			Severity: report.SeverityNote,
			Refs:     Refs{RunID: proposal.RunID, WorkItemID: proposal.WorkItemID},
			Detail:   Detail{Artifact: proposal.Artifact},
			Text:     text,
		},
	}, nil
}

// The operator's two switches are about the whole line rather than any one item,
// so they are addressed to the product and spoken by the harness: what an
// operator did is not any persona's account to give.
//
// What lifts either is its absence. The operator's hold records nothing about
// its lifting, so that release takes the moment it was observed; the intake
// hold's release is recorded with who lifted it, and says so where it can.

// The hold carries who placed it as well as why, and both are said: the same
// switch is placed by the operator and by the harness's own failure-storm brake,
// and a channel that named one for the other is a channel that sent somebody to
// look at the wrong state.
//
// The brake's hold carries the runs that tripped it, each with its item and
// what stopped it, so the message names what stopped the line rather than
// counting it.
func FromIntakeHold(hold runstate.IntakeHold) Notification {
	detail := Detail{Reason: hold.Account()}
	// A hold the brake is working itself is the development manager's or the
	// harness's, and the record says which; the fixed clause names the operator,
	// which is right for the operator's hold and for nothing else.
	if hold.Braked() {
		detail.Mover = hold.Whose()
		detail.Stops = strings.Join(hold.Brake.Entries(), "; ")
	}
	return productNotification(KindIntakeHeld, hold.HeldAt, detail)
}

// IntakeReleased says the hold was lifted, and by whom where the record names
// them. The moment is the observer's where no release was recorded — a hold
// lifted by a harness from before releases were written down — because what
// lifts a hold is its absence, and an absence has no moment of its own.
func IntakeReleased(at time.Time, release runstate.IntakeRelease, recorded bool) Notification {
	if !recorded {
		return productNotification(KindIntakeReleased, at, Detail{})
	}
	return productNotification(KindIntakeReleased, release.ReleasedAt, Detail{Reason: release.Says()})
}

// OperatorAction is one finding only the operator can act on, as the read model
// derives it: what is needed, where it is recorded, who found it, and since
// when. It is carried here rather than read from the read model because this
// package speaks and does not read; the surface that reads hands it over.
type OperatorAction struct {
	WorkItemID string
	// RunID is the stopped run the finding came from, where it came from one.
	RunID      string
	Needs      string
	RecordedIn string
	FoundBy    string
	// Ends is what ends the finding, and Mover whose move it is with that
	// ending, both worded by the read model so the attention line and this
	// message close on the same words.
	Ends  string
	Mover string
	Since time.Time
}

// FromOperatorAction says a finding that needs the operator's hand, once. It is
// addressed to the item the finding is about where there is one, so it sits in
// that item's narrative, and to the product otherwise. The harness speaks it:
// what the finding says is in Needs, in the words of whoever found it, and the
// message is the harness telling the operator it is his.
//
// It is a warning, whatever the report was filed at: something only a person
// can change is stopping something until they change it, and a note is what a
// reader scrolls past.
func FromOperatorAction(action OperatorAction) (Notification, error) {
	topic, err := topicForItem(action.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address the finding recorded in %s: %w", action.RecordedIn, err)
	}
	return Notification{
		Topic:   topic,
		Speaker: Harness(),
		Event: Event{
			Kind:     KindOperatorAction,
			At:       action.Since,
			Severity: report.SeverityWarning,
			Refs:     Refs{RunID: action.RunID, WorkItemID: action.WorkItemID},
			Detail: Detail{
				Needs:      action.Needs,
				RecordedIn: action.RecordedIn,
				FoundBy:    action.FoundBy,
				Ends:       action.Ends,
				Mover:      action.Mover,
			},
		},
	}, nil
}

// FromBrakeEscalation is the one message that says the brake's hold has been
// handed to the operator, whoever handed it. A hold the development manager
// escalated is said in the hold's own account of who decided it, as the trip
// was. A hold only the harness escalated, at its cycle bound, is said as the
// harness's escalation: the moment is the escalation's own rather than the
// hold's, because that is when the hold became a person's, and the reason is
// the hold's whole account — what tripped it, and the cycles and the last
// probe's stoppage that ended the loop. Its mover is left to the fixed clause:
// the hold's own wording of it is the same account again, and a message that
// said it twice is one the operator reads once. It is a warning: the hold now
// waits on a person, and the hourly line raises it from there as it stands.
//
// Where both stand — she escalated a hold the harness already had — her
// decision is the account, as it is on every other surface that says whose
// move the hold is. The surface that says this says it once per hold either
// way.
//
// A hold nobody has escalated is refused rather than said as if it had: a
// message telling the operator the hold is his, over a hold still being
// worked, is the false record this exists to prevent.
func FromBrakeEscalation(hold runstate.IntakeHold) (Notification, error) {
	if !hold.Braked() || !hold.Brake.Escalated() {
		return Notification{}, errors.New("address the brake's escalation: nobody has escalated this hold")
	}
	if hold.Brake.Decision == runstate.BrakeDecisionEscalate {
		return FromIntakeHold(hold), nil
	}
	notification := productNotification(KindIntakeEscalated, hold.Brake.Escalation.At, Detail{Reason: hold.Account()})
	notification.Event.Severity = report.SeverityWarning
	return notification, nil
}

// FromWatch says what a watch session changed to. It is addressed to the
// product for the same reason the holds are — a session is about the whole line
// rather than any one item — and spoken by the harness, because choosing work is
// not a role's judgement and no persona should be made to narrate it.
//
// A state nothing has a line for is refused rather than posted as something
// nobody wrote words for, which is the same refusal an unrecognized kind gets:
// a log written by a newer harness than the sink is one the sink reads past
// rather than mistranslates.
func FromWatch(transition runstate.WatchTransition) (Notification, error) {
	kind, ok := watchKinds[transition.State]
	if !ok {
		return Notification{}, fmt.Errorf("address watch session %s: %q is not a state anything says", transition.SessionID, transition.State)
	}
	// A stop the session recorded as a restart is the one stop nothing is waiting
	// on: it has waited out its runs and is being re-executed into a build
	// deployed over it. It is said as itself rather than as an ending, because the
	// two ask opposite things of whoever reads them and only one of them asks for
	// anything at all.
	if transition.Restarting {
		kind = KindWatchRedeploying
	}
	// An idle poll that could not read the store is said as a read being retried
	// rather than as a session that found nothing, for the same reason: the idle
	// line is for a queue that was read, and this one was not.
	if transition.RetryingRead() {
		kind = KindWatchReadRetrying
	}
	// A braked session is the one an operator has to do something about: the
	// line has stopped and it stays stopped until intake is released.
	severity := report.SeverityNote
	if kind == KindWatchBraked || kind == KindWatchBlocked {
		severity = report.SeverityWarning
	}
	// The runs the session could see and the conversation it is waiting on travel
	// with the reason, because whose move follows an idle poll is derived from them
	// rather than from the words: a session polling beside a run in flight is not
	// waiting on an admission, and one whose only unstarted work is a role's to
	// carry is waiting on that role.
	notification := productNotification(kind, transition.At, Detail{
		Reason:     transition.Reason,
		Running:    transition.Running,
		Executor:   string(transition.Executor),
		Unreadable: transition.Unreadable,
		// The provider refusing to serve any more work is the fourth of these, and
		// the one that reads worst without it: a poll passed over for a usage window
		// looks exactly like a poll passed over for an empty queue, so the clause
		// would send the reader to admit work the harness could not start anyway.
		ProviderWindow: transition.ProviderWindow,
		// Whose move a braked poll is, where the hold's own record says: a hold
		// the brake placed is the development manager's or the harness's until
		// she escalates it, and the fixed clause names the operator.
		Mover: transition.Mover,
	})
	notification.Event.Severity = severity
	return notification, nil
}

// watchKinds is what each recorded state is said as. It is a table rather than a
// switch because the states and the kinds are two vocabularies that have to
// agree, and a table is where a disagreement is visible.
var watchKinds = map[runstate.WatchState]Kind{
	runstate.WatchWatching: KindWatchStarted,
	runstate.WatchIdle:     KindWatchIdle,
	runstate.WatchBraked:   KindWatchBraked,
	runstate.WatchBlocked:  KindWatchBlocked,
	runstate.WatchResumed:  KindWatchResumed,
	runstate.WatchStopped:  KindWatchStopped,
}

// Line is a product's line with nothing being chosen from it: what stopped the
// choosing, when it became that way, how much admitted work the tracker reports
// as ready behind it, and how many promoted changes are waiting on the forge to
// publish them.
//
// It is the one thing selection says that is not a crossing. Everything else
// here compares two readings of a record and reports the difference, which says
// a state once and is right to: a thread is a narrative. A line that is held or
// idle over ready work is not news that happened, it is a condition that
// persists, and the reader who needs it is the one who was told once at midnight
// and has heard nothing since. So the state is said again while it stands, and
// how often is the sink's to decide — this only says it.
type Line struct {
	// Stopped is what has stopped the choosing, in the words whoever read the
	// record would use: the operator's hold, a held intake and why it was held,
	// a session that found nothing it could start, no session at all.
	Stopped string
	// Since is when the line became that way, which is what makes the message
	// worth repeating: the state does not change and its age does.
	Since time.Time
	// Ready is how much admitted work the tracker itself calls ready. It is what
	// separates a line waiting on somebody from an honestly quiet one.
	Ready int
	// Outstanding is how many promoted changes are waiting on the forge to
	// publish them. It is the other thing that makes a quiet line worth saying:
	// a merge the forge dropped is announced once as it happens, and a reader who
	// missed that message has nothing else that would ever tell them — so the
	// count is said with the line for as long as the publication is unsettled.
	Outstanding int
	// Mover is whose move follows what stopped the line, worded by the read model
	// beside the state itself rather than here, so the clause this message ends
	// on and the attention line `yoyo status` prints name one move. A line
	// stopped by the brake's hold is the development manager's or the harness's
	// while the brake works it and the operator's once she has escalated it, and
	// one fixed clause could not be right about both.
	Mover string
	// Standing is where the harness stands, in the four lines the read model
	// renders. It is said with the line because the two answer one question at
	// different grains: the sentence says the choosing has stopped and for how
	// long, and the four lines say what is running, what is working, what will not
	// start and what is waiting on somebody — which is the whole of what an
	// operator reading a stalled channel at three in the morning has to
	// reconstruct otherwise.
	Standing string
}

// FromLine says that nothing is being chosen while there is something to choose
// or something already done waiting to be published.
// It is addressed to the product and spoken by the harness for the same reason
// the holds and the watch sessions are: a line is about every item rather than
// any one of them, and what stopped it is nobody's judgement to narrate.
//
// The moment is the reading rather than the state's own start, because that is
// what it is: an account of what was true when somebody looked, whose whole
// point is that it is being looked at again.
//
// The severity is the caller's rather than derived here, for the reason the
// resident's is: how loud this is is a question about how long the line has
// stood and who is holding it, and both are the reporting surface's to hold. A
// hold somebody placed and may have to sit with is a note every hour; a hold
// that waits on a person nobody has told is a warning, and critical once it has
// stood long enough.
func FromLine(line Line, severity report.Severity, at time.Time) Notification {
	notification := productNotification(KindLineWaiting, at, Detail{
		Stopped:     strings.TrimSpace(line.Stopped),
		Since:       line.Since,
		Ready:       line.Ready,
		Outstanding: line.Outstanding,
		Mover:       strings.TrimSpace(line.Mover),
		Standing:    strings.TrimRight(line.Standing, "\n"),
	})
	notification.Event.Severity = severity
	return notification
}

// Resident is the binary a live watch session is running: the revision it was
// built from, and how many harness changes the repository has taken on since.
//
// It is the second thing here that is a state rather than a crossing, and it is
// the only one no durable record says on its own. A held line is at least
// derivable from the hold that stopped it; a session running a binary the
// harness has moved past leaves no trace anywhere — it goes on choosing work,
// and every run it starts looks exactly like a run started by a current one.
// What it actually produces is rounds spent against defects that were fixed on
// the main line hours before, which reads as agents failing rather than as a
// process nobody restarted, and on 2026-08-30 that reading cost three review
// rounds against a bug that had already been dead for a day.
type Resident struct {
	// Build is the revision the session's binary was built from, carried so a
	// reader can check the count rather than take it.
	Build string
	// Behind is how many harness changes have landed since. It is never said at
	// zero: a session running what is deployed is the ordinary state, and the
	// whole discipline here is that silence keeps meaning nothing to do.
	Behind int
}

// FromResident says that the session choosing work is running a build the
// harness has moved past. It is addressed to the product and spoken by the
// harness for the reason the line and the holds are: which binary a session is
// executing is about every item rather than any one of them, and it is nobody's
// judgement to narrate.
//
// The severity is the caller's rather than derived here, because how loud this
// is is a question about a threshold and a threshold is the reporting surface's
// to hold: the same two facts are a note worth reading beside the heartbeat and,
// far enough past that threshold, a degraded system somebody has to be told
// about directly.
func FromResident(resident Resident, severity report.Severity, at time.Time) Notification {
	notification := productNotification(KindResidentStale, at, Detail{
		Commit: strings.TrimSpace(resident.Build),
		Behind: resident.Behind,
	})
	notification.Event.Severity = severity
	return notification
}

// Stall is the harness having started nothing at all while work was ready to
// start: since when, over how much, and what the record last said about the
// thing that chooses work.
//
// It is the third state here rather than a crossing, and it is the one that is
// derived from an absence. The waiting line reads a record somebody wrote — a
// hold, a session saying it is idle — and a stale resident reads a build stamp;
// both need the process they are about to have been well enough to write
// something down. This needs nothing to be alive: what it reads is the last time
// any run started, which the runs themselves date, against a queue the tracker
// says has work in it. That is why it catches the case the other two structurally
// cannot — a scheduler that crashed writes no stop, and a wedged one goes on
// recording that it is watching.
//
// The chooser's last word rides along because it is the whole of what an
// operator has to decide between at three in the morning: a session whose last
// word was "stopped" wants starting, and one still claiming to be watching wants
// killing first.
type Stall struct {
	// Since is when anything last held a developer slot: the later of the last
	// run start and the last run end.
	Since time.Time
	// Ready is how much admitted work the tracker called ready through it.
	Ready int
	// Chooser is what the record last said about the thing that chooses work.
	Chooser string
	// Cause is the dominant thing accounting for the queue not moving, as the last
	// poll that started nothing recorded it, and Mover is whose move follows it.
	// Both are worded by the read model rather than here.
	//
	// They are the whole of what yoyodyne-ifd.324 changed, and the reason is one
	// page: on 2026-09-06 this message said nothing accounted for an hour of
	// silence while the session's own idle line, one surface over, held the
	// accounting — a third of the queue waiting on triage decisions. The
	// accounting was never missing. It was missing from the message that woke
	// somebody, because this surface derived its own answer from an absence
	// instead of reading the one the poll had written down.
	//
	// Both are empty where no poll left an account to read — a session that
	// stopped cleanly, or one that never idled — and the message then says what it
	// said before: that nothing the record holds accounts for the silence, which
	// in that case is true.
	Cause string
	Mover string
	// Standing is where the harness stands, in the four lines the read model
	// renders, said for the reason the waiting line says them: somebody reading
	// this was woken by it, and reconstructing the machine's state from one
	// sentence is what they would otherwise have to do.
	Standing string
}

// FromStall says that nothing at all has started while work was ready to start.
// It is addressed to the product and spoken by the harness for the reason the
// line and the holds are: it is about every item rather than any one of them.
//
// It is never a note, and that is the whole difference between this and the
// hourly line. A line waiting on a hold somebody placed is a state they already
// know about; a machine that has silently stopped doing anything is a degraded
// harness, which is the one class of thing this surface takes to somebody
// directly. The severity is the caller's, because it is what the caller
// escalates as the stall stands: a warning while it is young, and critical once
// it has stood long enough that nobody has acted on the warning.
func FromStall(stall Stall, severity report.Severity, at time.Time) Notification {
	notification := productNotification(KindStallNoticed, at, Detail{
		Stopped:  strings.TrimSpace(stall.Chooser),
		Since:    stall.Since,
		Ready:    stall.Ready,
		Cause:    strings.TrimSpace(stall.Cause),
		Mover:    strings.TrimSpace(stall.Mover),
		Standing: strings.TrimRight(stall.Standing, "\n"),
	})
	notification.Event.Severity = severity
	return notification
}

// ProviderWindow is the harness waiting out the provider's usage window: what
// the read model says the state is, and when the session recorded entering it.
//
// It is the answer to the stall above rather than another kind of it. Both are
// derived from the same absence — nothing has started, over work that is ready —
// and this is the one where something accounts for it. On 2026-09-05 the
// difference was ninety minutes of a provider window reported as a machine that
// had quietly died, which is a page for a non-incident and the fastest way there
// is to teach somebody to ignore the real one.
type ProviderWindow struct {
	// Says is the state as the read model words it, carried already said for the
	// reason Line's Stopped is: the same sentence is printed by `yoyo status` and
	// said here, and a second wording is a second thing that can disagree.
	//
	// It is a whole sentence rather than a clause, and every voice opens with it
	// rather than fitting it into one. That is the operator's acceptance: when the
	// system is paused on a provider usage window, the cause is the first words of
	// any message that reaches him.
	Says string
	// Since is when the session recorded itself waiting, which is what the age in
	// the message is measured from.
	Since time.Time
	// Standing is where the harness stands, in the four lines the read model
	// renders, said for the reason the stall says them: a reader who has been told
	// nothing is being chosen still wants to see what is.
	Standing string
}

// FromProviderWindow says that the harness is waiting out the provider's usage
// window. It is addressed to the product and spoken by the harness for the
// reason the line and the stall are: it is about every item rather than any one
// of them.
//
// It is a note, and that is the whole difference between this and the stall. A
// machine that has silently stopped doing anything is a degraded harness and is
// taken to somebody directly; a machine waiting out a window the provider named
// is the provider's ordinary behaviour, and a reader is owed the fact and not
// the interruption.
func FromProviderWindow(window ProviderWindow, at time.Time) Notification {
	return productNotification(KindProviderWindow, at, Detail{
		Stopped:  strings.TrimSpace(window.Says),
		Since:    window.Since,
		Standing: strings.TrimRight(window.Standing, "\n"),
	})
}

// CapacityHold is the provider holding every configured role at once: what the
// read model says the state is, when the hold began, and whose move it is.
//
// It is the capacity half of the stall rather than another kind of window.
// The window above is a session waiting out a limit it met itself, and is
// nobody's move; this is every role refused on a known reset with nothing
// configured to fail over to, which is the state that held this product for
// five days in September 2026 while the record said so 134 times.
type CapacityHold struct {
	// Says is the state as the read model words it, carried already said for the
	// reason the window's is: the same sentence heads `yoyo status` and this
	// message, and a second wording is a second thing that can disagree.
	Says string
	// Since is when the earliest standing refusal was recorded, which is what the
	// age in the message is measured from.
	Since time.Time
	// Mover is whose move follows it, worded by the read model beside the fact.
	Mover string
	// Standing is where the harness stands, in the four lines the read model
	// renders, for the reason the stall says them: whoever this reaches was told
	// nothing is moving and still wants to see what is.
	Standing string
}

// FromCapacityHold says that the provider is holding every role. It is
// addressed to the product and spoken by the harness for the reason the window
// and the stall are: it is about every item rather than any one of them.
//
// The severity is the caller's, because it is what the caller escalates as the
// hold stands: a warning the first hour and a critical one once it has stood
// long enough that a person is the only thing that ends it early.
func FromCapacityHold(hold CapacityHold, severity report.Severity, at time.Time) Notification {
	notification := productNotification(KindCapacityHold, at, Detail{
		Stopped:  strings.TrimSpace(hold.Says),
		Since:    hold.Since,
		Mover:    strings.TrimSpace(hold.Mover),
		Standing: strings.TrimRight(hold.Standing, "\n"),
	})
	notification.Event.Severity = severity
	return notification
}

// ProviderOutage is the provider answering nobody, as the read model says it:
// what the state is, when it began, and whose move it is. It is the other half
// of the capacity hold above and is shaped the same way for the same reason —
// the same sentence heads `yoyo status` and this message.
type ProviderOutage struct {
	// Says is the state as the read model words it, carried already said.
	Says string
	// Since is when the outage was first noticed, which is what the age in the
	// message is measured from.
	Since time.Time
	// Mover is whose move ends it, worded by the read model beside the fact.
	Mover string
	// Standing is where the harness stands, in the four lines the read model
	// renders, for the reason the hold says them.
	Standing string
}

// FromProviderOutage says that the provider is answering nobody. It is
// addressed to the product and spoken by the harness for the reason the hold
// and the stall are: it is about every item rather than any one of them.
//
// It is a warning, and it is said once: the caller tags the operators and does
// not repeat it while it stands. What is degraded is the harness, and what ends
// it is a person, which is the case the reach rule takes to somebody directly.
func FromProviderOutage(outage ProviderOutage, at time.Time) Notification {
	notification := productNotification(KindProviderOutage, at, Detail{
		Stopped:  strings.TrimSpace(outage.Says),
		Since:    outage.Since,
		Mover:    strings.TrimSpace(outage.Mover),
		Standing: strings.TrimRight(outage.Standing, "\n"),
	})
	notification.Event.Severity = report.SeverityWarning
	return notification
}

// FromProviderRestored says that the provider is answering again after an
// outage. It is a note: nothing is wrong and nothing is waiting on anybody, and
// what it carries is what was restored and how long it had stood.
func FromProviderRestored(says string, since, at time.Time) Notification {
	return productNotification(KindProviderRestored, at, Detail{
		Stopped: strings.TrimSpace(says),
		Since:   since,
	})
}

// RecurringTaskFailing is a recurring task failing before its first turn, as
// the read model says it: the sentence, since when, and whose move it is.
type RecurringTaskFailing struct {
	// Says is the failure as the read model words it: the task, the cause, and
	// how many firings in a row.
	Says string
	// Since is the first of the failed firings, which the age is measured from.
	Since time.Time
	// Mover is whose move ends it, worded by the read model beside the fact.
	Mover string
}

// FromRecurringTaskFailing says that a recurring task keeps failing before its
// first turn. It is addressed to the product and spoken by the harness, like
// the outage beside it: what failed is the harness's own firing, not any work
// item. The severity is the caller's — a warning when it is first said, and
// critical once it has stood long enough that nothing is going to end it.
func FromRecurringTaskFailing(failing RecurringTaskFailing, severity report.Severity, at time.Time) Notification {
	notification := productNotification(KindRecurringTaskFailing, at, Detail{
		Stopped: strings.TrimSpace(failing.Says),
		Since:   failing.Since,
		Mover:   strings.TrimSpace(failing.Mover),
	})
	notification.Event.Severity = severity
	return notification
}

// FromReleasedClaim says that an item the tracker called in progress had nothing
// working on it, and that the harness has given it back to the queue.
//
// It is addressed to the work item rather than to the product, unlike the stall
// beside it, and the difference is what each is about: a stall is the whole line
// having stopped, and this is one item that was stuck. The thread it lands in is
// the item's own, which is where the run that died already said everything it
// said — so a reader arrives at the release with the run above it.
//
// The speaker is the harness. Nobody judged anything here: an audit found a claim
// with no run alive behind it, and no persona should be made to narrate a
// process's death as though it were their account of the work.
//
// It is a warning for the reason the stall is. A run that ended badly is already
// said in this thread and is nobody's surprise; a claim that outlived its run is
// the harness having been quietly degraded — an item nothing would ever pull
// again — and that is the one class of thing this surface takes to somebody
// directly.
func FromReleasedClaim(released runstate.ReleasedClaim) (Notification, error) {
	topic, err := topicForItem(released.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address the claim released on %s: %w", released.WorkItemID, err)
	}
	return Notification{
		Topic:   topic.WithTitle(released.WorkItemTitle),
		Speaker: Harness(),
		Event: Event{
			Kind:     KindClaimReleased,
			At:       released.ReleasedAt,
			Severity: report.SeverityWarning,
			Refs:     Refs{RunID: released.RunID, WorkItemID: released.WorkItemID},
			Detail: Detail{
				Title:   released.WorkItemTitle,
				Stopped: strings.TrimSpace(released.Because),
				Since:   released.Since,
			},
		},
	}, nil
}

// Improvement is one value the project's template has improved that this
// project has never edited: which setting it is, and what the three-way
// comparison says about it.
//
// It is the fourth state here rather than a crossing, and the only one that is
// about the installation rather than about the work. Nothing recorded it and
// nothing is waiting on it: it is true from the moment a template moves ahead of
// a project, and it stays true, silently, until somebody adopts it or edits the
// value themselves. The surfaces that could have said so are all ones an
// operator has to run, which is why a project can sit for months on a persona
// the template has since fixed.
//
// The sentence is the comparison's own rather than assembled here, because what
// makes a value an improvement is a derivation with one home, and a second
// wording of it is a second thing that can disagree with `yoyo config drift`.
type Improvement struct {
	// Setting is the configuration key, so a message can name the value without
	// taking the sentence apart.
	Setting string
	// Says is what the comparison says about it: what the template supplied when
	// this project was generated, and what it supplies now.
	Says string
}

// FromImprovement says that the project's template has improved one value this
// project never edited. It is addressed to the product and spoken by the harness
// for the reason the line and the holds are: a template is about the whole
// project rather than any one item of work, and nobody's judgement narrates it.
//
// It is a note and stays a note however long it goes unadopted. Nothing is
// degraded, nothing is waiting, and the state is one an operator is entitled to
// decline forever -- which is exactly why it is said once and never repeated.
func FromImprovement(improvement Improvement, at time.Time) Notification {
	return productNotification(KindBundleImprovement, at, Detail{
		Setting:     strings.TrimSpace(improvement.Setting),
		Improvement: strings.TrimSpace(improvement.Says),
	})
}

// Improvements is several values the project's template has improved, found on
// one reading and said together. Says is the comparison's own sentence over all
// of them -- how many, and the first few by name -- for the reason a single
// improvement's is: the wording has one home, and a surface that counted and
// named them itself would be a second one.
type Improvements struct {
	Says string
}

// FromImprovements says that the project's template has improved several values
// this project never edited, in one message rather than one each. It is what
// bounds the advisory-once class to one message a pass: the first reading on a
// project several revisions behind is the one that finds a dozen at once, and a
// dozen direct messages is a burst aimed at the channel that reaches a person
// as a notification. It is addressed and pitched exactly as one improvement is,
// and it is a note for the same reason.
func FromImprovements(improvements Improvements, at time.Time) Notification {
	return productNotification(KindBundleImprovements, at, Detail{
		Improvement: strings.TrimSpace(improvements.Says),
	})
}

// Accumulation is what one topic gathered while nothing was posting its events:
// how many there were, the first and last of them, and the most attention any
// one of them asked for.
//
// It is the one thing here that is not read from a record at all. Everything
// else compares two readings and reports the difference; this reports what a
// surface did with the difference when it was too large to say one message at a
// time, which is a fact about the reporting rather than about the work. It is
// still said in this package, because what a message says is decided here
// whatever ends up carrying it.
type Accumulation struct {
	// Topic is the thread the accumulation belongs to, which is what makes a
	// digest one message per thread rather than one message for a whole backlog.
	Topic Topic
	// Events is how many were collapsed into this one message.
	Events int
	// Since and At are the first and last of them. The span between them is what
	// tells a reader whether they are looking at an afternoon or a fortnight.
	Since time.Time
	At    time.Time
	// Severity is the most attention any of the collapsed events asked for, so a
	// digest standing for a warning is marked as one. A digest is not allowed to
	// be quieter than the loudest thing inside it.
	Severity report.Severity
}

// FromAccumulation says that a topic gathered more than a channel can carry, and
// where the whole of it is.
//
// It is spoken by the harness rather than by any of the personas whose events
// were collapsed, for the reason a thread's opening message is: deciding not to
// repeat four hundred messages is not anybody's account of the work, and a
// persona made to say it would be claiming a judgement it never made.
func FromAccumulation(gathered Accumulation) Notification {
	severity := gathered.Severity
	if !severity.Valid() {
		severity = report.SeverityNote
	}
	refs := Refs{}
	if gathered.Topic.Kind == TopicWorkItem {
		refs.WorkItemID = gathered.Topic.ID
	}
	return Notification{
		Topic:   gathered.Topic,
		Speaker: Harness(),
		Event: Event{
			Kind:     KindCatchUpDigest,
			At:       gathered.At,
			Severity: severity,
			Refs:     refs,
			Detail:   Detail{Accumulated: gathered.Events, Since: gathered.Since},
		},
	}
}

// FromSkippedLine says that one line of a durable log could not be read and
// was read past. It is addressed to the product and spoken by the harness for
// the reason the holds are: a torn write in the reports log is about every item
// rather than any one of them, and no persona's judgement is in a decoder
// refusing a line.
//
// It is a warning: whatever the line recorded is not going to be said, which
// is something already lost, and the file is a person's to look at. It is
// dated by when the sink read it rather than by anything the line says, because
// a line that will not decode names no moment — which is also why it is never
// read past as history, since absence of a date is not evidence of age.
func FromSkippedLine(log string, skipped runstate.SkippedLine, at time.Time) Notification {
	notification := productNotification(KindLogLineSkipped, at, Detail{
		Log:    strings.TrimSpace(log),
		Line:   skipped.Line,
		Offset: skipped.Offset,
		Cause:  strings.TrimSpace(skipped.Problem),
	})
	notification.Event.Severity = report.SeverityWarning
	return notification
}

func FromOperatorHold(hold runstate.OperatorHold) Notification {
	return productNotification(KindHoldPlaced, hold.HeldAt, Detail{})
}

func HoldLifted(at time.Time) Notification {
	return productNotification(KindHoldLifted, at, Detail{})
}

// KindExchangeTurn and KindExchangeClosed are selected in conversation.go rather
// than here, because an ask exchange is something a conversation did and this
// file reads runs. That is where the prediction this file used to carry came
// out: the envelope, the addressing, and every persona's line for both kinds
// were written before the channel existed, and the channel arriving cost one
// selection function and nothing in the sink, the threading, or the envelope.

func productNotification(kind Kind, at time.Time, detail Detail) Notification {
	return Notification{
		Topic:   Product(),
		Speaker: Harness(),
		Event: Event{
			Kind:     kind,
			At:       at,
			Severity: report.SeverityNote,
			Detail:   detail,
		},
	}
}

// topicForItem addresses something to the item it concerns, or to the product
// where it concerns none: a conversation has no assigned work rather than
// unknown work, and burying what it said in some item's thread would misfile it.
func topicForItem(workItemID string) (Topic, error) {
	if strings.TrimSpace(workItemID) == "" {
		return Product(), nil
	}
	return WorkItem(workItemID)
}

// selectionSpeaker is whose account the start of a run is. The development
// manager speaks where its triage chose the item, because that choice is its own
// judgment; everything else is the harness, which is the same rule the whole
// table follows. The operator is not a persona and the scheduler is not a role,
// so neither has a voice to be spoken in, and a run whose selection nothing
// recorded has nobody to attribute at all — the harness's flat sentence is the
// honest account of it rather than a persona claiming a choice the record cannot
// show it made.
func selectionSpeaker(selection *runstate.Selection) Speaker {
	if selection == nil || strings.TrimSpace(selection.By) != runstate.SelectedByDevelopmentManager {
		return Harness()
	}
	return Persona(domain.RoleDevelopmentManager, "")
}

// selectionDetail carries the recorded account of why the harness is running
// this item. A run recorded before selections existed carries none, and the
// absence is stated rather than rendered as a blank — an unaccounted run is
// exactly what recording the reason exists to make visible.
func selectionDetail(selection *runstate.Selection) Detail {
	if selection == nil {
		return Detail{}
	}
	return Detail{SelectedBy: selection.By, SelectionReason: selection.Reason}
}

// startedDetail is what a run's opening message says about it: why it is
// running, and what it is running as. The account and the configuration are said
// once, where the thread opens, rather than on every crossing after it — they
// are fixed for the life of a run, and a fact repeated on every message is one
// nobody reads on the message where it changed.
func startedDetail(after runstate.State) Detail {
	detail := selectionDetail(after.Selection)
	detail.Account = after.AccountAlias
	detail.Configuration = after.ConfigRevision
	return detail
}

// checksBehind reports a run with the deterministic checks behind it: the record
// carries no failing check and the run has moved past running them. Both halves
// are needed. A run in a later phase with a failing check recorded is one the
// checks handed back to the developer, and a run that has not reached reviewing
// has not been past the gate yet whatever else its record says. It is the rule
// the conversation's own milestones read the record by, stated here rather than
// imported so this stays independent of how a run is executed.
func checksBehind(state runstate.State) bool {
	if state.CheckFailure != nil {
		return false
	}
	switch state.Phase {
	case runstate.PhaseReviewing, runstate.PhaseIntegrating, runstate.PhaseCompleting, runstate.PhaseCleaningUp, runstate.PhaseComplete:
		return true
	default:
		return false
	}
}

// refusalBoughtARound reports a later reading carrying the protected-path
// gate's refusal — the change in the worktree touches a path the item did not
// grant — that the record wrote beside the repair round it bought. The count
// moving is the whole of the test: the record clears a refusal at the next
// gate that passes and writes the next one with the round it hands back, so a
// refusal under a moved count is one the developer is taking back out, whether
// it is the earlier reading's paths again or different ones. A refusal read
// with the count unmoved is either the same save read twice or one the budget
// was already gone for, and neither is this line's to say.
//
// A run that stopped is not said either, whatever the count did. That is the
// reading a sink starting late takes of a run that blocked on its refusal: the
// count moved on the rounds before it, the refusal on the record is the one
// that spent none, and the blocker line beside it is what names the paths.
func refusalBoughtARound(before, after runstate.State) bool {
	if after.PathRefusal == nil || after.RepairAttempts <= before.RepairAttempts {
		return false
	}
	return !handedToAPerson(after)
}

// stageSpend is what the check stage spent of its bound, for a record that
// carries one, in the words the message says it in. A record written before
// the stage was recorded carries none, and says so through the placeholder's
// own absence rather than as a spend of nothing.
func stageSpend(state runstate.State) string {
	stage := state.CheckStage
	if stage == nil || stage.Running() {
		return ""
	}
	return fmt.Sprintf("%s of the %s bound", stage.Elapsed().Round(time.Second), stage.Bound())
}

// landed reports a record whose landing checks have ended, one way or another.
func landed(state runstate.State) bool {
	return state.LandingChecks != nil && state.LandingChecks.Finished()
}

// verdictGiven reports a record that holds a reviewer's verdict at all. The
// record passes through no verdict twice per round — it is cleared before each
// review runs and written again when that review answers — so the absence is a
// real state of a run rather than only the state of one that has never been
// reviewed.
func verdictGiven(state runstate.State) bool {
	return state.ReviewDecision != ""
}

// parked reports a run stopped short of finishing with an instruction to resume:
// a provider that refused it, the operator holding everything, a directive
// nobody has resolved, work the item was made to wait on, or a tracker that
// would not answer the read a gate boundary makes. All five keep the run's claim
// and its worktree, which is what makes a park different from a failure.
func parked(state runstate.State) bool {
	return state.UsageLimitResetsAt != nil || state.OperatorHeldSince != nil ||
		state.DirectivePause != nil || state.DependencyPause != nil ||
		state.TrackerPause != nil
}

// causeOf names what a parked run is waiting on, as the object of "waiting on".
// A directive is named first because it is the only one nothing but a person can
// clear, and the deadline is said with a provider refusal because "until when"
// is the part an operator plans around.
func causeOf(state runstate.State) string {
	if pause := state.DirectivePause; pause != nil {
		waiting := "an unresolved directive (" + pause.Kind + ")"
		if unresolved := strings.TrimSpace(pause.Unresolved); unresolved != "" {
			waiting += ": " + unresolved
		}
		return waiting
	}
	if pause := state.DependencyPause; pause != nil {
		return "unfinished work this item depends on: " + pause.Summary()
	}
	if pause := state.TrackerPause; pause != nil {
		return "a tracker that would not answer: " + pause.Summary()
	}
	if state.OperatorHeldSince != nil {
		return runstate.DescribePause(runstate.PauseOperatorHold, "")
	}
	waiting := runstate.DescribePause(state.PauseCause, state.UsageLimitKind)
	if state.UsageLimitResetsAt != nil {
		waiting += ", until " + state.UsageLimitResetsAt.UTC().Format(time.RFC3339)
	}
	return waiting
}

// parkSeverity is how loudly a park is said, and it follows the same precedence
// causeOf names the cause by, so the weight of a message and the words in it can
// never describe two different pauses.
//
// An exhausted usage limit is a warning and the other causes are notes. That is
// not a judgement about which is worse: it is what an unattended reader can do
// about each. A directive, a dependency link, and an operator hold are waiting on
// a decision somebody already made — the person reading the channel placed the
// first and the last, and the middle one is a development manager's own link; an
// exhausted limit is hours in which nothing will happen for a reason nobody
// chose, and it must not weigh the same as checks passing. A transient overload
// lifts in seconds and stays a note for exactly that reason.
//
// A tracker that went unanswered for a whole recovery window is a warning on the
// same reasoning as the limit: nobody chose it, the store is the one every role
// writes through, and a run parked on it is work that will not move until
// somebody looks at the machine.
func parkSeverity(state runstate.State) report.Severity {
	if state.DirectivePause != nil || state.DependencyPause != nil {
		return report.SeverityNote
	}
	if state.TrackerPause != nil {
		return report.SeverityWarning
	}
	if state.OperatorHeldSince != nil {
		return report.SeverityNote
	}
	if state.PauseCause == runstate.PauseServerOverload {
		return report.SeverityNote
	}
	return report.SeverityWarning
}

// An escalation or an integration policy can complete a run without landing
// code. They name their stop cause too, as the throughput reading does.
func endedWithoutLanding(state runstate.State) bool {
	return state.Status.Terminal() && (state.Outcome() != runstate.OutcomeSucceeded || state.Integration == nil)
}

// handedToAPerson reports a run whose ending is a stoppage somebody has to
// decide about, which is the read model's own reading of it rather than a second
// one taken here. It is asked of both readings rather than only of the later
// one, because the blocker can reach a record that is already terminal: the
// stoppage is then news even though the ending is not.
//
// It asks nothing of the status, because the outcome already answers everything
// the status could: the read model only ever says "stopped" of a terminal record
// carrying a blocker. Reading the status as well is what silenced this line on
// the record it matters most on — a run whose promotion a sweep contradicted
// keeps the "succeeded" it wrote for itself, so a status test here dropped the
// critical line for a stoppage a person already owns.
func handedToAPerson(state runstate.State) bool {
	return state.Outcome() == runstate.OutcomeStopped
}

// environmentallyRefused says a round the environment refused before the words
// the run stopped on, where that is what happened. It goes first because it
// changes how the rest reads: a thread carrying only the failure reads as an
// item that has just spent another round toward its cap, and where the round was
// refused the opposite is true.
//
// What it says is the record's own sentence rather than a second reading of the
// same flags. The accounting has five states and one of them — a return the
// settle decided on and could not write — is the one a thread must not get
// wrong, so it is derived once, in the record, and phrased around here.
//
// It is silent on every ordinary stoppage, and on a run that recorded a cause
// and delivered a change anyway, because that round spent as any round does.
func environmentallyRefused(state runstate.State) string {
	refused := state.Environmental
	if refused == nil || !refused.Refused {
		return ""
	}
	return refused.Describe() + " — "
}

// targetRedOf is the wait on its target's red check a record holds, or nil.
func targetRedOf(state runstate.State) *runstate.TargetRed {
	if state.PullRequest == nil {
		return nil
	}
	return state.PullRequest.TargetRed
}

func mergeQueued(state runstate.State) bool {
	return state.PullRequest != nil && state.PullRequest.MergeQueued
}

func merged(state runstate.State) bool {
	return state.PullRequest != nil && state.PullRequest.Merged
}

// describePullRequest names a published request the way somebody would quote
// one: its number where the record has one, and its URL, which is what a reader
// actually follows.
func describePullRequest(published *runstate.PullRequest) string {
	if published == nil {
		return ""
	}
	if published.Number > 0 && strings.TrimSpace(published.URL) != "" {
		return fmt.Sprintf("#%d (%s)", published.Number, published.URL)
	}
	if published.Number > 0 {
		return fmt.Sprintf("#%d", published.Number)
	}
	return strings.TrimSpace(published.URL)
}

// describeFindings says what a repair round asked for, one entry per finding and
// in the order the reviewer raised them. It is the count's missing half: a
// reader given "3 findings" cannot tell a correction to a document from a
// problem with the design, and both are what a repair round is usually made of.
//
// Each entry is the finding's first sentence rather than the whole of it. The
// whole is in the record the message points at, and what a channel is read for
// is which change is being asked for rather than the argument behind it. The
// severity comes first because it is the one word that ranks the change, and
// the place the record names comes last because it is where somebody looks
// rather than part of what was said. A finding the record located gives its
// line beside its file; one that named a file alone says the file.
//
// A record that counted findings without keeping them yields nothing, which the
// message says as itself rather than as a reviewer who asked for nothing.
func describeFindings(findings []runstate.Finding) []string {
	if len(findings) == 0 {
		return nil
	}
	described := make([]string, 0, len(findings))
	for _, finding := range findings {
		said := firstSentence(finding.Message)
		if severity := strings.TrimSpace(finding.Severity); severity != "" {
			said = severity + ": " + said
		}
		if file := strings.TrimSpace(finding.File); file != "" {
			at := file
			if finding.Line > 0 {
				at = fmt.Sprintf("%s:%d", file, finding.Line)
			}
			// The place a finding names belongs inside the sentence rather than
			// after it, so it goes before the full stop the reviewer wrote. A
			// finding that ended on no stop is left ending on none.
			if strings.HasSuffix(said, ".") {
				said = strings.TrimSuffix(said, ".") + " (" + at + ")."
			} else {
				said += " (" + at + ")"
			}
		}
		described = append(described, said)
	}
	return described
}

// firstSentence is as much of what somebody wrote as stands on its own: up to
// the first full stop that ends a sentence, or the first line break, whichever
// comes first. A full stop inside a word is not one — a finding naming
// README.md is naming a file rather than finishing — so it is the stop followed
// by a space that ends the sentence.
func firstSentence(written string) string {
	trimmed := strings.TrimSpace(written)
	if line, _, found := strings.Cut(trimmed, "\n"); found {
		trimmed = strings.TrimSpace(line)
	}
	if at := strings.Index(trimmed, ". "); at >= 0 {
		return trimmed[:at+1]
	}
	return trimmed
}
