package readmodel

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// proposedChange is one undecided proposal, as the amendment store records it.
func proposedChange() amendment.Proposal {
	return amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            "amendment-0123456789abcdef0123456789abcdef",
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         "run-9a8b7c6d5e4f30211203f4e5d6c7b8a9",
		WorkItemID:    "yoyodyne-ifd.402",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "observability-and-dashboard",
		Kind:          artifact.KindDesign,
		Owner:         domain.RoleArchitect,
		Change:        "The design should say the dashboard reads the attention entries as records rather than as sentences.",
		Why:           "A page cannot open a card on a sentence, and the read model now carries the record.",
		RaisedAt:      moment.Add(-3 * time.Hour),
	}
}

// attentionOfEveryKind is one entry per kind, built the way the reading builds
// them, with the sentence each is expected to print beside it. It is the
// fixture every test below reads, and a kind added to the vocabulary without an
// entry here fails the coverage check.
func attentionOfEveryKind(t *testing.T) map[AttentionKind]struct {
	entry Attention
	what  string
} {
	t.Helper()
	failingTask := FailingTask{
		Task:     "development-manager-sweep",
		Role:     domain.RoleDevelopmentManager,
		Cause:    runstate.PreTurnMessageRefused,
		Problem:  "scheduled pass's message is 47768 bytes, limit is 32768",
		Failures: 2,
		FirstAt:  time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC),
		RaisedAt: time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC),
		LatestAt: time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC),
	}
	brake := runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion, ProductID: "yoyodyne", HeldAt: moment.Add(-2 * time.Hour),
		HeldBy: runstate.IntakeHolderBrake,
		Brake: &runstate.IntakeBrake{
			Blocked:        []runstate.BrakeBlockedRun{{WorkItemID: "yoyodyne-ifd.300", Reason: "checks failed"}},
			CooldownEndsAt: moment.Add(time.Hour),
		},
	}
	stall := Stall{Reason: ReasonSessionIdle, Says: "a watch session is alive and has found nothing it can start", Since: moment.Add(-time.Hour)}
	pile := report.Pile{Collected: 12, Unhandled: 3, Oldest: moment.Add(-9 * 24 * time.Hour), OldestAge: 9 * 24 * time.Hour, Worst: report.SeverityWarning}
	child := runstate.SupervisedChild{Service: config.ServiceScheduler, State: runstate.ChildDegraded, Reason: "died 6 times in 10 minutes"}
	mismatch := runstate.ConfigMismatch{
		Service: "dashboard", PID: 4242, Build: "0364141b2c3d4e5f60718293a4b5c6d7e8f9001a",
		ConfigPath: "/work/yoyodyne/.yoyodyne/config.yaml", StartedAt: moment.Add(-44 * time.Hour),
		Keys: []string{"agents.developer.effort"},
	}
	owed := runstate.State{RunID: "run-owed", WorkItemID: "yoyodyne-ifd.410", Status: runstate.StatusFailed, Phase: runstate.PhaseCleaningUp}
	published := runstate.State{
		RunID: "run-queued", WorkItemID: "yoyodyne-ifd.411", Branch: "yoyodyne/item/queued",
		Integration: &runstate.Integration{TargetBranch: "main"},
		PullRequest: &runstate.PullRequest{Number: 567, URL: "https://forge.example/pr/567", MergeQueued: true},
	}
	outage := runstate.ProviderOutage{Cause: domain.ProviderUnauthenticated, Provider: domain.BackendClaudeCode, AccountAlias: "default", Since: moment.Add(-time.Hour), LastSeen: moment}
	paused := directive.Directive{ID: "directive-4f2c", Kind: directive.KindAmbiguous, Unresolved: "which branch does this land on?", ReceivedAt: moment.Add(-time.Hour)}
	capacity := CapacityHold{Holding: true, Since: moment.Add(-time.Hour), ResetsAt: moment.Add(time.Hour)}
	capacityEntry, _ := capacity.Attention()
	stallEntry, _ := stall.Waiting()

	return map[AttentionKind]struct {
		entry Attention
		what  string
	}{
		AttentionAmendment: {amendmentAttention(proposedChange()),
			"a change to observability-and-dashboard is proposed and undecided (amendment-0123456789abcdef0123456789abcdef)"},
		AttentionCarriedItem: {carriedItemAttention("yoyodyne-ifd.212", domain.ConversationWith(domain.RoleArchitect)),
			`yoyodyne-ifd.212 is admitted for "conversation:architect" rather than a developer run`},
		AttentionReports: {reportsAttention(pile),
			"3 of 12 collected report(s) are unhandled, the oldest filed 9d ago, worst warning"},
		AttentionAmendmentQueue: {amendmentQueueAttention(amendment.Queue{Proposed: 5, Undecided: 4, Oldest: moment.Add(-20 * 24 * time.Hour), OldestAge: 20 * 24 * time.Hour, Owners: []domain.AgentRole{domain.RoleArchitect}}),
			"4 of 5 proposed change(s) are undecided, the oldest raised 20d ago, against the architect's documents"},
		AttentionOwedStep: {owedStepAttention(owed),
			"cleanup of the branch and worktree for yoyodyne-ifd.410 is not finished"},
		AttentionPublication: {awaitingForgeAttention(published),
			"run run-queued promoted yoyodyne-ifd.411 into main and the forge has not published it: pull request #567 https://forge.example/pr/567"},
		AttentionDegradedService: {degradedServiceAttention(child),
			"the scheduler service is degraded: died 6 times in 10 minutes"},
		AttentionConfigMismatch: {configMismatchAttention(mismatch), mismatch.Says()},
		AttentionServiceCopies: {serviceCopiesAttention(ServiceCopies{Service: "slack", Deployed: "3d3d367a1b2c4d5e", Copies: []ServiceCopy{
			{PID: 77, Build: "3d3d367a1b2c4d5e", StartedAt: moment.Add(-time.Hour), Supervised: true},
			{PID: 99, Build: "1a2b3c4d5e6f7a8b", StartedAt: moment.Add(-2 * time.Hour), Behind: true},
		}}),
			"the slack service has 2 copies running, where one should run: pid 77 on build 3d3d367a1b2c since " + localMoment(moment.Add(-time.Hour)) +
				" (the supervisor's); pid 99 on build 1a2b3c4d5e6f, an old build, since " + localMoment(moment.Add(-2*time.Hour)) + " (not the supervisor's, left running)"},
		AttentionFailingTask: {failingTaskAttention(failingTask),
			"the recurring task development-manager-sweep has failed before its first turn 2 times in a row since 2026-08-30T09:00:00Z: the harness refused the message it composed for the pass; latest: scheduled pass's message is 47768 bytes, limit is 32768"},
		AttentionHold: {intakeHoldAttention(brake),
			"intake is held, since 2026-08-30T10:00:00Z: " + singleLine(brake.Account(), maxRefusalBytes) +
				"; tripped by a run of yoyodyne-ifd.300: checks failed"},
		AttentionDirective: {directiveAttention(paused),
			"directive directive-4f2c is unresolved: which branch does this land on?"},
		AttentionOutage: {outageAttention(outage), outage.Says()},
		AttentionStall: {stallEntry,
			"a watch session is alive and has found nothing it can start, since 2026-08-30T11:00:00Z"},
		AttentionHeldWork: {heldWorkAttention(HeldAwaitingCarryOut, 2),
			"2 admitted items await carry-out of a decision already recorded"},
		AttentionOperatorAction: {operatorActionAttention(OperatorAction{
			Key: "report:report-1", Subject: "report-1", ReportID: "report-1", WorkItemID: "yoyodyne-ifd.383",
			Needs:      "add the hook to .claude/settings.json by hand",
			RecordedIn: "the handling of report-1 recorded in chat-1",
			FoundBy:    "the Lead Product Manager, handling the report",
			Ends:       reportFindingEnds,
			Since:      moment.Add(-time.Hour),
		}),
			"report-1 needs your hand: add the hook to .claude/settings.json by hand (found by the Lead Product Manager, handling the report; recorded in the handling of report-1 recorded in chat-1, about yoyodyne-ifd.383)"},
		AttentionProductDecision: {productDecisionAttention(triage.Entry{
			RunID: "run-superseded", WorkItemID: "yoyodyne-ifd.428.34",
			ProductDecision: &triage.ProductDecision{
				Decision: triage.ProductSuperseded, SupersededBy: "yoyodyne-ifd.398",
				Reason: "398 does this work whole", DecidedBy: "the Lead Product Manager in conversation chat-1",
				RunStatus: "running", RunInFlight: true, RunReadAt: moment,
			},
		}),
			"yoyodyne-ifd.428.34 is superseded by yoyodyne-ifd.398 while run run-superseded is in flight, decided by the Lead Product Manager in conversation chat-1: 398 does this work whole"},
		AttentionHumanGate: {humanGateAttention("yoyodyne-ifd.209.7", HumanGateWait{Gate: "soak-reviewed", Statement: "the operator has judged the parity soak"}),
			`yoyodyne-ifd.209.7 waits on the gate "soak-reviewed": the operator has judged the parity soak`},
		AttentionUntracedPass: {untracedPassAttention(UntracedPass{Task: "factory-watch", Role: domain.RoleProgramManager, StartedAt: moment.Add(-time.Hour), Findings: 2, First: "reviews wait an hour for a slot"}),
			"the pass of factory-watch at 2026-08-30T11:00:00Z reported 2 finding(s) and left no trace of them: no memory written, no lane report changed, no report filed, no work admitted; the first: reviews wait an hour for a slot"},
		AttentionFactoryStall: {factoryStallAttention(FactoryStall{
			Since: moment.Add(-6 * time.Hour), LastPull: moment.Add(-6 * time.Hour), Limit: 2 * time.Hour, At: moment,
			Failures: []PassFailure{{Task: "development-manager-sweep", At: moment.Add(-time.Hour), Attempts: 6, Problem: "list work items: timed out"}},
		}),
			"no work has been pulled and no recurring pass has succeeded for 6h0m0s, past the 2h0m0s limit: work was last pulled at " + localMoment(moment.Add(-6*time.Hour)) +
				", and no recurring pass has ever succeeded; each pass's latest failure: development-manager-sweep failed 6 times since, latest at " + localMoment(moment.Add(-time.Hour)) +
				": list work items: timed out"},
		AttentionTrackerUnanswered: {trackerUnansweredAttention(runstate.TrackerListings{
			FailingSince: moment.Add(-time.Hour), Failures: 3, LatestAt: moment,
			Latest: "bd list did not answer within its 30s bound on any of 3 attempts over 1m40s",
		}),
			"the tracker has not answered a listing since 2026-08-30T11:00:00Z: 3 listing(s) failed after their retries, the latest at 2026-08-30T12:00:00Z: bd list did not answer within its 30s bound on any of 3 attempts over 1m40s"},
		AttentionUnrunCheck: {unrunCheckAttention(UnrunCheck{
			Command: "make codex-resume", Changes: 3, Since: moment.Add(-3 * time.Hour), LatestAt: moment, LatestRunID: "run-latest",
			Reason: "codex is not installed on this machine",
		}),
			"the check make codex-resume could not run on 3 changes in a row, since " + localMoment(moment.Add(-3*time.Hour)) +
				", so each of them went on without it; latest reason: codex is not installed on this machine"},
		// The capacity hold is the third switch under the hold kind; it is
		// checked with the rest below, and named here so the map is one per kind.
		"": {capacityEntry,
			"every role is held by the provider's usage limit, since 2026-08-30T11:00:00Z, until 2026-08-30T13:00:00Z"},
	}
}

// Every kind carries its kind, a mover from the vocabulary, the identifier of
// the record it is about where there is one, and the record itself — and prints
// the sentence derived from them, opening with the mover's own possessive so a
// surface counting by mover and a reader of the line agree on who has to act.
func TestEveryAttentionKindCarriesItsRecordAndDerivesItsSentence(t *testing.T) {
	t.Parallel()
	fixtures := attentionOfEveryKind(t)
	for _, kind := range AttentionKinds() {
		if _, covered := fixtures[kind]; !covered {
			t.Fatalf("kind %q has no fixture here, so its shape is unpinned", kind)
		}
	}
	for kind, fixture := range fixtures {
		entry := fixture.entry
		if kind != "" && entry.Kind != kind {
			t.Errorf("%s: kind = %q", kind, entry.Kind)
		}
		if !entry.Mover.Valid() {
			t.Errorf("%s: mover %q is outside the vocabulary", kind, entry.Mover)
		}
		if entry.Label() == "" || entry.Label() == string(entry.Kind) {
			t.Errorf("%s: no plain-words label: %q", kind, entry.Label())
		}
		if got := entry.What(); got != fixture.what {
			t.Errorf("%s: what = %q, want %q", kind, got, fixture.what)
		}
		if whose := entry.Whose(); !strings.HasPrefix(whose, entry.Mover.Possessive()+" — ") {
			t.Errorf("%s: whose = %q does not open with the mover's possessive %q", kind, whose, entry.Mover.Possessive())
		}
		// Exactly one record is carried, the one the kind names. The carried
		// item is the exception: its record is the marker and the item id, both
		// values rather than records of their own.
		carried, want := 0, 1
		value := reflect.ValueOf(entry)
		for index := 0; index < value.NumField(); index++ {
			if value.Field(index).Kind() == reflect.Pointer && !value.Field(index).IsNil() {
				carried++
			}
		}
		if entry.Kind == AttentionCarriedItem {
			want = 0
			if entry.Executor == "" || entry.WorkItemID == "" {
				t.Errorf("%s: %+v, want the marker and the item carried", kind, entry)
			}
		}
		if carried != want {
			t.Errorf("%s: %d records carried, want %d: %+v", kind, carried, want, entry)
		}
	}

	// The identifiers, one kind at a time: each names the record a surface would
	// open or act on.
	for kind, want := range map[AttentionKind]string{
		AttentionAmendment:         "amendment-0123456789abcdef0123456789abcdef",
		AttentionCarriedItem:       "yoyodyne-ifd.212",
		AttentionOwedStep:          "run-owed",
		AttentionPublication:       "run-queued",
		AttentionDegradedService:   "scheduler",
		AttentionFailingTask:       "development-manager-sweep",
		AttentionConfigMismatch:    fixtures[AttentionConfigMismatch].entry.ConfigMismatch.InstanceID(),
		AttentionServiceCopies:     "slack",
		AttentionHold:              HoldIntake,
		AttentionDirective:         "directive-4f2c",
		AttentionOutage:            string(domain.ProviderUnauthenticated),
		AttentionStall:             string(ReasonSessionIdle),
		AttentionReports:           "",
		AttentionAmendmentQueue:    "",
		AttentionHeldWork:          "",
		AttentionProductDecision:   "run-superseded",
		AttentionHumanGate:         "soak-reviewed",
		AttentionUntracedPass:      "factory-watch",
		AttentionFactoryStall:      "2026-08-30T06:00:00Z",
		AttentionTrackerUnanswered: "2026-08-30T11:00:00Z",
		AttentionUnrunCheck:        "make codex-resume",
	} {
		if got := fixtures[kind].entry.ID; got != want {
			t.Errorf("%s: id = %q, want %q", kind, got, want)
		}
	}
	if capacity := fixtures[""].entry; capacity.Kind != AttentionHold || capacity.ID != HoldCapacity || capacity.CapacityHold == nil {
		t.Errorf("capacity hold = %+v, want the hold kind, the capacity switch, and the hold carried", capacity)
	}
}

// An amendment entry carries the proposal whole: the target document and what
// sort of document it is, who proposed it and from where, the change, and why —
// none of it cut to a line, because the card that shows it and the act that
// decides it both need the thing rather than a sentence about it.
func TestAnAmendmentEntryCarriesTheProposalInFull(t *testing.T) {
	t.Parallel()
	proposal := proposedChange()
	entry := amendmentAttention(proposal)
	if entry.Amendment == nil || !reflect.DeepEqual(*entry.Amendment, proposal) {
		t.Fatalf("amendment = %+v, want the proposal carried whole", entry.Amendment)
	}
	if entry.Mover != MoverArchitect || entry.WorkItemID != "yoyodyne-ifd.402" {
		t.Fatalf("entry = %+v, want the owner as mover and the proposer's item", entry)
	}
	if !strings.HasPrefix(entry.Whose(), "the architect's — ") {
		t.Fatalf("whose = %q, want the owner named", entry.Whose())
	}
	// A document the product manager owns waits on her, in the words a reader
	// uses for her rather than the role's identifier.
	proposal.Owner = domain.RoleProductManager
	if whose := amendmentAttention(proposal).Whose(); !strings.HasPrefix(whose, "the Lead Product Manager's — ") {
		t.Fatalf("whose = %q, want the product manager named in plain words", whose)
	}

	// Through the reading itself, from the store's records.
	sources := quietSources()
	sources.Amendments = fakeAmendments{records: []amendment.Record{{Proposal: &proposal}}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 1 || standing.NeedsHuman[0].Kind != AttentionAmendment || standing.NeedsHuman[0].Amendment == nil {
		t.Fatalf("needs a human = %+v, want the one undecided proposal carried whole", standing.NeedsHuman)
	}
	if got := standing.NeedsHuman[0].Amendment; got.Change != proposal.Change || got.Why != proposal.Why || got.Artifact != proposal.Artifact {
		t.Fatalf("amendment = %+v, want the change, the reason, and the document in full", got)
	}
}

// The intake hold's mover is read off the same record its sentence is worded
// from, in every state the brake's record can be in, so the two cannot name
// different people.
func TestTheIntakeHoldsMoverMatchesTheHoldsOwnWording(t *testing.T) {
	t.Parallel()
	ended := moment
	blocked := []runstate.BrakeBlockedRun{{WorkItemID: "yoyodyne-ifd.300", Reason: "checks failed"}}
	for name, hold := range map[string]runstate.IntakeHold{
		"the operator's own": {HeldAt: moment, HeldBy: runstate.IntakeHolderOperator},
		"a brake hold from before the brake worked its own": {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake},
		"undecided":          {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, CooldownEndsAt: moment.Add(time.Hour)}},
		"released":           {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, Decision: runstate.BrakeDecisionRelease}},
		"probe decided":      {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, Decision: runstate.BrakeDecisionProbe}},
		"probing":            {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, Probe: &runstate.IntakeProbe{WorkItemID: "yoyodyne-ifd.300", StartedAt: moment}}},
		"probed and blocked": {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, CooldownEndsAt: moment.Add(time.Hour), Probe: &runstate.IntakeProbe{WorkItemID: "yoyodyne-ifd.300", StartedAt: moment, EndedAt: &ended, Blocked: true}}},
		"escalated":          {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{Blocked: blocked, Decision: runstate.BrakeDecisionEscalate}},
		// The harness's own escalation at the bound on its loop, and the one state
		// where a decision of hers stands on top of it: a hold it escalated is
		// still hers to release, and the mover has to follow the release rather
		// than the escalation, because the release is what the session acts on.
		"escalated by the harness": {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{
			Blocked: blocked, Cycles: 4, CycleBound: 4,
			Escalation: &runstate.BrakeEscalation{At: moment, Cycles: 4, Probe: "yoyodyne-ifd.300", Reason: "checks failed"},
		}},
		"escalated by the harness and released by her": {HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &runstate.IntakeBrake{
			Blocked: blocked, Cycles: 4, CycleBound: 4, Decision: runstate.BrakeDecisionRelease,
			Escalation: &runstate.BrakeEscalation{At: moment, Cycles: 4, Probe: "yoyodyne-ifd.300", Reason: "checks failed"},
		}},
	} {
		entry := intakeHoldAttention(hold)
		if entry.Whose() != hold.Whose() {
			t.Errorf("%s: whose = %q, want the hold's own %q", name, entry.Whose(), hold.Whose())
		}
		if !strings.HasPrefix(entry.Whose(), entry.Mover.Possessive()+" — ") {
			t.Errorf("%s: mover %q does not open the hold's wording %q", name, entry.Mover, entry.Whose())
		}
	}
}

// The four movers of an unpublished promotion are read off the record's own
// fields, in the order the sentence reads them.
func TestAPublicationEntryNamesItsMoverFromTheRecord(t *testing.T) {
	t.Parallel()
	dropped := runstate.MergeDrop{At: moment, Reason: "the remote target moved"}
	for name, want := range map[string]struct {
		state runstate.State
		mover Mover
	}{
		"unrecorded":                 {runstate.State{RunID: "run-1", Branch: "b"}, MoverHarness},
		"queued":                     {runstate.State{RunID: "run-2", PullRequest: &runstate.PullRequest{Number: 1, MergeQueued: true}}, MoverForge},
		"queued with failing checks": {runstate.State{RunID: "run-red", PullRequest: &runstate.PullRequest{Number: 700, MergeQueued: true, Checks: &runstate.PullRequestChecks{Failing: []runstate.FailingCheck{{Name: "build"}}}}}, MoverHarness},
		"re-armed after a drop":      {runstate.State{RunID: "run-3", PullRequest: &runstate.PullRequest{Number: 1, MergeQueued: true}, MergeDrop: &dropped}, MoverForge},
		"dropped":                    {runstate.State{RunID: "run-4", PullRequest: &runstate.PullRequest{Number: 1}, MergeDrop: &dropped}, MoverDevelopmentManager},
		"queued with unread checks": {runstate.State{RunID: "run-unread", PullRequest: &runstate.PullRequest{Number: 1, MergeQueued: true,
			Checks: &runstate.PullRequestChecks{ReadAt: moment, ReadError: "unexpected end of JSON input"}}}, MoverHarness},
		// A request nothing ever asked the forge to merge is the development
		// manager's to decide rather than a person's to merge by hand
		// (yoyodyne-ifd.429.31).
		"unasked": {runstate.State{RunID: "run-5", Status: runstate.StatusSucceeded, ReviewDecision: runstate.ReviewApprove,
			Integration: &runstate.Integration{TargetBranch: "main"}, PullRequest: &runstate.PullRequest{Number: 1}}, MoverDevelopmentManager},
		"unmerged with another account": {runstate.State{RunID: "run-6", PullRequest: &runstate.PullRequest{Number: 1},
			PublishFailure: "confirm the pull request merged: the forge did not answer"}, MoverOperator},
		// A merge withdrawn for its target's red check waits on the item filed for
		// it, and the harness takes it up once that closes (yoyodyne-m5p).
		"waiting on the target's red check": {redTargetState(), MoverHarness},
	} {
		entry := awaitingForgeAttention(want.state)
		if entry.Mover != want.mover {
			t.Errorf("%s: mover = %q, want %q", name, entry.Mover, want.mover)
		}
		if !strings.HasPrefix(entry.Whose(), want.mover.Possessive()+" — ") || !strings.Contains(entry.Whose(), "yoyo reconcile") {
			t.Errorf("%s: whose = %q, want %s and the sweep that settles it", name, entry.Whose(), want.mover.Possessive())
		}
		if entry.Publication == nil || (want.state.MergeDrop != nil) != (entry.Publication.MergeDrop != nil) {
			t.Errorf("%s: publication = %+v, want the drop carried exactly where the record has one", name, entry.Publication)
		}
	}
	// The wait on a red target names the check and the filed item, and says
	// nobody has anything to decide.
	waiting := awaitingForgeAttention(redTargetState())
	for _, want := range []string{"waits on main's red check", "adoption (filed as yoyodyne-red-1)"} {
		if !strings.Contains(waiting.What(), want) {
			t.Errorf("what = %q, want it to say %q", waiting.What(), want)
		}
	}
	if !strings.Contains(waiting.Whose(), "waits on yoyodyne-red-1") || !strings.Contains(waiting.Whose(), "nothing here needs a person") {
		t.Errorf("whose = %q, want the filed item named and no person asked", waiting.Whose())
	}
	// The unasked request's sentence names the two decisions on her docket, and
	// never a merge by hand.
	unasked := awaitingForgeAttention(runstate.State{RunID: "run-5", Status: runstate.StatusSucceeded, ReviewDecision: runstate.ReviewApprove,
		Integration: &runstate.Integration{TargetBranch: "main"}, PullRequest: &runstate.PullRequest{Number: 1}})
	if !unasked.Publication.Unarmed || !strings.Contains(unasked.Whose(), "a re-arm has the harness arm it") ||
		!strings.Contains(unasked.Whose(), "a re-run hands the change back") || strings.Contains(unasked.Whose(), "merge it") {
		t.Fatalf("unasked publication = %+v, whose = %q; want it marked unarmed and put to the development manager's decisions", unasked.Publication, unasked.Whose())
	}
	// One the forge closed is hers too, offered only the re-run; one handed back
	// for a fresh run is nobody's, and is off the line altogether.
	closed := awaitingForgeAttention(runstate.State{RunID: "run-7", Status: runstate.StatusSucceeded, ReviewDecision: runstate.ReviewApprove,
		Integration: &runstate.Integration{TargetBranch: "main"}, PullRequest: &runstate.PullRequest{Number: 1, State: "CLOSED"}})
	if closed.Mover != MoverDevelopmentManager || !strings.Contains(closed.Whose(), "nothing left to arm") || strings.Contains(closed.Whose(), "a re-arm") {
		t.Fatalf("closed publication mover %q, whose = %q; want her docket with the re-run alone", closed.Mover, closed.Whose())
	}
	handedBack := runstate.State{RunID: "run-8", Status: runstate.StatusSucceeded, ReviewDecision: runstate.ReviewApprove,
		Integration: &runstate.Integration{TargetBranch: "main"},
		PullRequest: &runstate.PullRequest{Number: 1, HandedBack: &runstate.PublicationHandBack{At: moment}}}
	if awaiting := AwaitingForge([]runstate.State{handedBack}); len(awaiting) != 0 {
		t.Fatalf("awaiting the forge = %+v, want a handed-back publication off the line", awaiting)
	}
	// A record with no integration leaves the target field empty and says so
	// in the sentence: a placeholder is a sentence, and a field a surface acts
	// on carries none.
	unrecorded := awaitingForgeAttention(runstate.State{RunID: "run-1", WorkItemID: "item-1", Branch: "b"})
	if unrecorded.Publication.TargetBranch != "" || !strings.Contains(unrecorded.What(), "into an unrecorded target") {
		t.Fatalf("publication = %+v, what = %q; want an empty target and the sentence saying so", unrecorded.Publication, unrecorded.What())
	}
	if encoded := string(mustMarshal(t, unrecorded)); strings.Contains(encoded, `"target_branch"`) {
		t.Fatalf("JSON = %s, want no target field where the record holds none", encoded)
	}
}

func TestQueuedMergeAttentionNamesJobRerunsBeforeWithdrawal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		conclusion string
		reruns     int
		asked      bool
		want       string
		withdraw   bool
	}{
		{name: "cancelled job", conclusion: "cancelled", want: "asks the forge to run the jobs", withdraw: true},
		{name: "timed out job", conclusion: "timed_out", reruns: 1, want: "2 of 2 reruns", withdraw: true},
		{name: "job never started", conclusion: "startup_failure", want: "leaving the merge queued", withdraw: true},
		{name: "awaiting first rerun", conclusion: "cancelled", reruns: 1, asked: true, want: "without spending another rerun"},
		{name: "awaiting last rerun", conclusion: "timed_out", reruns: runstate.MaxCheckReruns, asked: true, want: "has not yet started the job rerun"},
		{name: "reruns spent", conclusion: "cancelled", reruns: runstate.MaxCheckReruns, want: "job rerun limit on this head is spent", withdraw: true},
		{name: "step failed", conclusion: "failure", want: "reads the failed checks and withdraws the merge", withdraw: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks := &runstate.PullRequestChecks{Failing: []runstate.FailingCheck{{Name: "build", Conclusion: tc.conclusion, CheckRun: 123}}, Reruns: tc.reruns}
			if tc.asked {
				checks.RerunChecks = []int64{123}
			}
			state := runstate.State{RunID: "run-queued", WorkItemID: "item", Status: runstate.StatusSucceeded, Phase: runstate.PhaseComplete, Integration: &runstate.Integration{TargetBranch: "main"}, PullRequest: &runstate.PullRequest{Number: 700, MergeQueued: true, Checks: checks}}
			for _, entry := range []Attention{owedStepAttention(state), awaitingForgeAttention(state)} {
				whose := entry.Whose()
				if entry.Mover != MoverHarness || !strings.Contains(entry.What(), "build") || !strings.Contains(whose, tc.want) || strings.Contains(whose, "withdraws") != tc.withdraw {
					t.Fatalf("%s: mover %s; what %q; whose %q", entry.Kind, entry.Mover, entry.What(), whose)
				}
				if tc.asked && !strings.Contains(whose, "leaves the merge queued") {
					t.Fatalf("%s must retain the queued merge: %q", entry.Kind, whose)
				}
				if checks.FailedInTheJob() && !tc.asked && tc.reruns < runstate.MaxCheckReruns && !strings.Contains(whose, "if the forge refuses") {
					t.Fatalf("%s must make withdrawal conditional on refusal: %q", entry.Kind, whose)
				}
			}
		})
	}
}

func TestUnreadQueuedChecksAndOwedStepsWaitOnTheHarness(t *testing.T) {
	t.Parallel()
	state := runstate.State{RunID: "run-unread", WorkItemID: "task", PullRequest: &runstate.PullRequest{Number: 732, MergeQueued: true,
		Checks: &runstate.PullRequestChecks{ReadAt: moment, ReadError: "unexpected end of JSON input"}}}
	entry := awaitingForgeAttention(state)
	if entry.Mover != MoverHarness || !strings.Contains(entry.What(), "checks unread: unexpected end of JSON input") || !strings.Contains(entry.Whose(), "next `yoyo reconcile` sweep") {
		t.Fatalf("unread publication = %+v; %s; %s", entry, entry.What(), entry.Whose())
	}
	if owed := owedStepAttention(state); owed.Mover != MoverHarness {
		t.Fatalf("owed step mover = %s", owed.Mover)
	}
}

// The JSON carries the fields and, beside them, the two sentences computed
// from the fields; reading it back yields the same entry, and a document whose
// sentence disagrees with its fields is refused rather than believed.
func TestAttentionJSONCarriesTheRecordAndTheDerivedSentences(t *testing.T) {
	t.Parallel()
	for kind, fixture := range attentionOfEveryKind(t) {
		encoded, err := json.Marshal(fixture.entry)
		if err != nil {
			t.Fatalf("%s: marshal: %v", kind, err)
		}
		var wire map[string]any
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if wire["label"] != fixture.entry.Label() || wire["what"] != fixture.entry.What() || wire["whose"] != fixture.entry.Whose() || wire["kind"] != string(fixture.entry.Kind) || wire["mover"] != string(fixture.entry.Mover) {
			t.Errorf("%s: JSON = %s, want the kind, the mover, and both sentences", kind, encoded)
		}
		var decoded Attention
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("%s: unmarshal: %v", kind, err)
		}
		if decoded.What() != fixture.entry.What() || decoded.Whose() != fixture.entry.Whose() || decoded.Kind != fixture.entry.Kind || decoded.ID != fixture.entry.ID {
			t.Errorf("%s: round trip = %+v, want %+v", kind, decoded, fixture.entry)
		}
	}

	// A sentence the fields do not derive is refused; a document that leaves
	// the sentences out is read from its fields alone.
	entry := directiveAttention(directive.Directive{ID: "directive-1", Unresolved: "which?"})
	var decoded Attention
	if err := json.Unmarshal([]byte(`{"kind":"directive","id":"directive-1","mover":"operator","directive":{"schema_version":0,"id":"directive-1","product_id":"","kind":"","received_by":"","received_at":"0001-01-01T00:00:00Z","text":"","unresolved":"which?"}}`), &decoded); err != nil {
		t.Fatalf("a document without the sentences: %v", err)
	}
	if decoded.What() != entry.What() {
		t.Fatalf("what = %q, want %q derived from the fields", decoded.What(), entry.What())
	}
	disagreeing := strings.Replace(string(mustMarshal(t, entry)), `"what":"directive directive-1 is unresolved: which?"`, `"what":"directive-2 is unresolved: which?"`, 1)
	if err := json.Unmarshal([]byte(disagreeing), &decoded); err == nil || !strings.Contains(err.Error(), "disagrees with its record") {
		t.Fatalf("a sentence disagreeing with its fields was accepted: %v", err)
	}
	wrongLabel := strings.Replace(string(mustMarshal(t, entry)), `"label":"direction unresolved"`, `"label":"merge stuck"`, 1)
	if err := json.Unmarshal([]byte(wrongLabel), &decoded); err == nil {
		t.Fatal("a label disagreeing with its record was accepted")
	}
	if err := json.Unmarshal([]byte(`{"kind":"directive","mover":"operator","surprise":1}`), &decoded); err == nil {
		t.Fatal("a field the model does not carry was accepted")
	}
	// A kind or a mover outside its vocabulary is refused rather than printed
	// as nobody's move in particular.
	for name, document := range map[string]string{
		"an unknown kind":  `{"kind":"surprise","id":"x","mover":"operator"}`,
		"a missing kind":   `{"id":"x","mover":"operator"}`,
		"an unknown mover": `{"kind":"directive","id":"x","mover":"somebody"}`,
		"a missing mover":  `{"kind":"directive","id":"x"}`,
	} {
		if err := json.Unmarshal([]byte(document), &decoded); err == nil || !strings.Contains(err.Error(), "is not one of") {
			t.Errorf("%s was accepted: %v", name, err)
		}
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// The mover vocabulary is closed and every member has a possessive a sentence
// can open with.
func TestEveryMoverHasAPossessive(t *testing.T) {
	t.Parallel()
	for _, mover := range Movers() {
		if possessive := mover.Possessive(); !strings.HasSuffix(possessive, "'s") && mover != MoverUnnamed {
			t.Errorf("%q: possessive = %q", mover, possessive)
		}
	}
	if MoverOf(domain.RoleDevelopmentManager) != MoverDevelopmentManager || MoverOf("") != MoverUnnamed || MoverOf("nobody-in-particular") != MoverUnnamed {
		t.Fatal("MoverOf does not fold roles onto the vocabulary")
	}
	if carriedItemAttention("item-1", domain.WorkItemExecutor("conversation")).Whose() != "the role it names — in conversation; no run will ever be started for it" {
		t.Fatal("a bare conversation marker does not name the unnamed role")
	}
}

// A lane report's blocker names what it waits on in this vocabulary, narrowed:
// every mover it may name is one the vocabulary has, the check admits exactly
// those, and it refuses the movers a lane cannot wait on.
func TestEveryMoverALaneReportMayNameIsAMover(t *testing.T) {
	t.Parallel()
	for _, mover := range LaneReportMovers() {
		if !mover.Valid() {
			t.Errorf("a lane report may wait on %q, which is not a mover the read model has", mover)
		}
		if err := CheckLaneReportMover(string(mover)); err != nil {
			t.Errorf("CheckLaneReportMover(%q) = %v, want it admitted", mover, err)
		}
	}
	for _, refused := range []string{string(MoverNobody), string(MoverUnnamed), "developer", "reviewer", "the-weather", ""} {
		if err := CheckLaneReportMover(refused); err == nil {
			t.Errorf("CheckLaneReportMover(%q) admitted it", refused)
		}
	}
}

// A queued merge's line carries the checks the last sweep read, so the forge
// holding a merge is never said without whether it can land.
func TestAQueuedPublicationLineCarriesItsChecks(t *testing.T) {
	t.Parallel()
	published := runstate.State{
		RunID: "run-queued", WorkItemID: "yoyodyne-ifd.437.4", Branch: "yoyodyne/item/queued",
		Integration: &runstate.Integration{TargetBranch: "main"},
		PullRequest: &runstate.PullRequest{Number: 713, URL: "https://forge.example/pr/713", MergeQueued: true,
			Checks: &runstate.PullRequestChecks{
				HeadCommit: "1111111111111111111111111111111111111111",
				ReadAt:     moment,
				Failing:    []runstate.FailingCheck{{Name: "go test", Paths: []string{"internal/other/other_test.go"}}},
				BehindBy:   31,
			}},
	}
	what := awaitingForgeAttention(published).What()
	for _, want := range []string{"pull request #713", "checks failing: go test (on internal/other/other_test.go, which this change does not touch)", "31 commit(s) behind main"} {
		if !strings.Contains(what, want) {
			t.Errorf("line %q does not say %q", what, want)
		}
	}
}

// redTargetState is a publication whose merge the sweep withdrew for main's red
// adoption check, waiting on the item filed for it.
func redTargetState() runstate.State {
	return runstate.State{RunID: "run-9", WorkItemID: "item-9", Status: runstate.StatusSucceeded, ReviewDecision: runstate.ReviewApprove,
		Integration: &runstate.Integration{TargetBranch: "main"},
		PullRequest: &runstate.PullRequest{Number: 863, TargetRed: &runstate.TargetRed{At: moment, TargetBranch: "main",
			Checks: []runstate.TargetRedCheck{{Name: "adoption", WorkItem: "yoyodyne-red-1"}}}},
		PublishFailure: "the forge's checks fail on main itself"}
}

// A part running a stale build is the harness's move whichever part it is, as
// the work item asks; the dashboard, which nothing restarts yet, says in its
// whose sentence what brings it back.
func TestAConfigMismatchIsTheHarnesssMove(t *testing.T) {
	t.Parallel()
	for service, want := range map[string]Mover{
		"scheduler":  MoverHarness,
		"slack":      MoverHarness,
		"dashboard":  MoverHarness,
		"supervisor": MoverHarness,
	} {
		entry := configMismatchAttention(runstate.ConfigMismatch{Service: service, PID: 7, ConfigPath: "/c.yaml", Keys: []string{"agents.developer.effort"}})
		if entry.Mover != want {
			t.Errorf("%s: mover = %q, want %q", service, entry.Mover, want)
		}
		if !strings.Contains(entry.What(), "agents.developer.effort") {
			t.Errorf("%s: what = %q, want the key named", service, entry.What())
		}
	}
	dashboard := configMismatchAttention(runstate.ConfigMismatch{Service: "dashboard", PID: 7, ConfigPath: "/c.yaml", Keys: []string{"x"}})
	if !strings.Contains(dashboard.Whose(), "`yoyo dashboard`") {
		t.Errorf("dashboard whose = %q, want the restart named", dashboard.Whose())
	}
}

func TestUndeliveredConfigurationFindingWaitsOnTheHarness(t *testing.T) {
	t.Parallel()
	state := runstate.State{
		RunID: "run-landed", WorkItemID: "yoyodyne-task", Status: runstate.StatusSucceeded, Phase: runstate.PhaseComplete,
		Integration:      &runstate.Integration{TargetBranch: "main"},
		ConfigComparison: &runstate.ConfigComparison{Pending: true, DeliveryFailure: "configuration finding refused"},
	}
	entry := owedStepAttention(state)
	if !state.Outstanding() || entry.Mover != MoverHarness || entry.OwedStep.ConfigComparison != state.ConfigComparison {
		t.Fatalf("missing delivery obligation: %+v", entry)
	}
	if !strings.Contains(entry.What(), "configuration comparison") || !strings.Contains(entry.What(), "configuration finding refused") || !strings.Contains(entry.Whose(), "delivers the saved configuration comparison") {
		t.Fatalf("delivery misdescribed: %s — %s", entry.What(), entry.Whose())
	}
}

func TestConfigurationFindingsIdentifyEachRunningInstance(t *testing.T) {
	t.Parallel()
	first := runstate.ConfigMismatch{Service: "dashboard", PID: 7, StartedAt: moment}
	second := first
	second.PID = 8
	if configMismatchAttention(first).ID == configMismatchAttention(second).ID {
		t.Fatal("two live dashboards open the same attention record")
	}
}
