package ownership

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Every kind has a rule. A kind added to the vocabulary without one would
// resolve every entry of it to the Lead Product Manager as unclassified, which
// is safe but is a registry nobody finished.
func TestEveryKindHasARule(t *testing.T) {
	t.Parallel()
	for _, kind := range Kinds() {
		if !HasRule(kind) {
			t.Errorf("kind %q has no rule in the registry", kind)
		}
	}
	for kind := range registry {
		found := false
		for _, known := range Kinds() {
			found = found || kind == known
		}
		if !found {
			t.Errorf("the registry keeps a rule for %q, which is no kind in the vocabulary", kind)
		}
	}
}

// fixtureRecords is records for a rule to read: every fact on its own, the
// facts that combine in the records the read model builds, and every value of
// every vocabulary the record carries, including reasons that are not on the
// closed list and roles the harness does not recognize.
func fixtureRecords() []Record {
	roles := append([]domain.AgentRole{"", "somebody-else"}, domain.RoleProductManager, domain.RoleArchitect,
		domain.RoleDevelopmentManager, domain.RoleProgramManager, domain.RoleDeveloper, domain.RoleReviewer)
	reasons := append([]OperatorReason{"", "approval", "decision", "review"}, OperatorReasons()...)
	flags := []func(*Record){
		func(r *Record) { r.FundamentalIntent = true },
		func(r *Record) { r.HeldByOperator = true },
		func(r *Record) { r.HeldByBrake = true },
		func(r *Record) { r.Braked = true },
		func(r *Record) { r.Braked, r.BrakeReleased = true, true },
		func(r *Record) { r.Braked, r.BrakeProbing = true, true },
		func(r *Record) { r.Braked, r.BrakeEscalated = true, true },
		func(r *Record) { r.Braked, r.BrakeEscalated, r.BrakeReleased = true, true, true },
		func(r *Record) { r.Braked, r.BrakeEscalated, r.BrakeProbing = true, true, true },
		func(r *Record) { r.Braked, r.HeldByOperator = true, true },
		func(r *Record) { r.PullRequest = true },
		func(r *Record) { r.PullRequest, r.MergeQueued = true, true },
		func(r *Record) { r.PullRequest, r.MergeQueued, r.ChecksUnread = true, true, true },
		func(r *Record) { r.PullRequest, r.MergeQueued, r.ChecksRed = true, true, true },
		func(r *Record) { r.PullRequest, r.WaitingOnRedTarget = true, true },
		func(r *Record) { r.PullRequest, r.MergeDropped = true, true },
		func(r *Record) { r.PullRequest, r.Unarmed = true, true },
		func(r *Record) { r.ConversationUnopened = true },
		func(r *Record) { r.AwaitingCarryOut = true },
		func(r *Record) { r.AwaitingWork = true },
		func(r *Record) { r.Parked = true },
		func(r *Record) { r.WaitsOnWork = true },
		func(r *Record) { r.Unread = true },
		func(r *Record) { r.GateUnreadable = true },
		func(r *Record) { r.FromReport = true },
		func(r *Record) { r.FromEscalation = true },
		func(r *Record) { r.IntegrationResumable = true },
		func(r *Record) { r.HarnessContinues = true },
		func(r *Record) { r.DocumentRun = true },
		func(r *Record) {
			r.UnresolvedFindings = []string{"item yoyodyne-x is closed but has no later confirmed merged publication"}
		},
		func(r *Record) { r.UnresolvedFindings = []string{"ask the forge about pull request 1: timed out"} },
		func(r *Record) {
			r.PassFailure = &PassFailure{Mover: MoverOperator, PersonReason: PersonCredential, PersonTarget: "the provider", PersonStep: "log in"}
		},
		func(r *Record) {
			// A pass failure naming the operator for a reason nothing validated,
			// as no resolver here ever writes but a stored record could carry.
			r.PassFailure = &PassFailure{Mover: MoverOperator, PersonReason: "because"}
		},
		func(r *Record) { r.PassFailure = &PassFailure{Mover: MoverProgramManager} },
		func(r *Record) { r.PassFailure = &PassFailure{Mover: "not-a-mover"} },
		func(r *Record) { r.RecordedOwner = MoverOperator },
		func(r *Record) { r.RecordedOwner, r.RecordedByOperator = MoverOperator, true },
		func(r *Record) { r.RecordedOwner = MoverHarness },
		func(r *Record) { r.RecordedOwner = MoverDevelopmentManager },
		func(r *Record) { r.RecordedOwner = MoverUnnamed },
		func(r *Record) { r.RecordedOwner = "not-a-mover" },
	}
	holds := []HoldSwitch{"", OperatorHold, IntakeHold, CapacityHold, "something-else"}
	stalls := append([]StallReason{"", "something-nobody-named"}, StallReasons()...)
	causes := append([]domain.ProviderOutageCause{""}, domain.ProviderOutageCauses()...)

	var records []Record
	for _, role := range roles {
		for _, reason := range reasons {
			records = append(records, Record{Role: role, Reason: reason})
			for _, flag := range flags {
				record := Record{Role: role, Reason: reason}
				flag(&record)
				records = append(records, record)
			}
		}
	}
	for _, hold := range holds {
		for _, flag := range append(flags, func(*Record) {}) {
			record := Record{Hold: hold}
			flag(&record)
			records = append(records, record)
		}
	}
	for _, stall := range stalls {
		for _, cause := range causes {
			for _, flag := range append(flags, func(*Record) {}) {
				record := Record{Stall: stall, OutageCause: cause}
				flag(&record)
				records = append(records, record)
			}
		}
	}
	return records
}

// For every kind, every fixture driven through its rule either names somebody
// other than the operator or names him with a reason from the closed list. The
// rule is called directly as well as through Resolve, so a rule that would name
// him without a reason fails here rather than being caught quietly at run time.
func TestNoRuleNamesTheOperatorWithoutAReasonOnTheClosedList(t *testing.T) {
	t.Parallel()
	records := fixtureRecords()
	for _, kind := range Kinds() {
		rule := registry[kind]
		if rule == nil {
			continue
		}
		for _, record := range records {
			answer, ok := rule(record)
			if ok && answer.Owner.IsOperator() && !answer.Reason.Valid() {
				t.Errorf("kind %q names the operator for %q, which is not on the closed list, on %+v", kind, answer.Reason, record)
			}
			resolved := Resolve(kind, record)
			if resolved.Owner.IsOperator() && !resolved.Reason.Valid() {
				t.Errorf("Resolve(%q) names the operator for %q on %+v", kind, resolved.Reason, record)
			}
			if !resolved.Owner.IsOperator() && resolved.Reason != "" {
				t.Errorf("Resolve(%q) carries reason %q on an answer naming %s", kind, resolved.Reason, resolved.Owner)
			}
			if !resolved.Owner.Valid() || strings.TrimSpace(resolved.Remedy) == "" {
				t.Errorf("Resolve(%q) = %+v on %+v, want a known owner and a remedy", kind, resolved, record)
			}
		}
	}
}

// The closed list is the architect's ruling of 2026-10-17: seven reasons, and
// each is one some rule names the operator for.
func TestTheClosedListIsTheSevenReasonsRuled(t *testing.T) {
	t.Parallel()
	want := []OperatorReason{"fundamental-intent", "credential", "repository-setting", "protected-file", "human-gate", "own-hold", "diverged-history"}
	got := OperatorReasons()
	if len(got) != len(want) {
		t.Fatalf("OperatorReasons() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] || !got[i].Valid() || got[i].Says() == "a reason that is not on the closed list" {
			t.Errorf("reason %d = %q, want %q said in words", i, got[i], want[i])
		}
	}
	named := map[OperatorReason]bool{}
	for _, kind := range Kinds() {
		for _, record := range fixtureRecords() {
			if answer := Resolve(kind, record); answer.Owner.IsOperator() {
				named[answer.Reason] = true
			}
		}
	}
	for _, reason := range want {
		if !named[reason] {
			t.Errorf("no rule names the operator for %q", reason)
		}
	}
}

// An entry no rule can classify is the Lead Product Manager's to classify,
// never the operator's: a kind with no rule, a record its rule cannot read, and
// a rule that names the operator for no reason on the list.
func TestAnUnclassifiedEntryIsTheLeadProductManagersToClassify(t *testing.T) {
	want := Answer{Owner: MoverProductManager, Remedy: "classify this entry", Capability: "report handling"}
	if got := Resolve("a-kind-nobody-named", Record{}); got != want {
		t.Errorf("Resolve(unknown kind) = %+v, want %+v", got, want)
	}
	if got := Resolve(KindHold, Record{}); got != want {
		t.Errorf("Resolve(a hold naming no switch) = %+v, want %+v", got, want)
	}
	if got := Resolve(KindHold, Record{Hold: IntakeHold}); got != want {
		t.Errorf("Resolve(an intake hold nobody placed) = %+v, want %+v", got, want)
	}
	if got := Resolve(KindStall, Record{Stall: "something-nobody-named"}); got != want {
		t.Errorf("Resolve(an unnamed stall) = %+v, want %+v", got, want)
	}

	// A rule that names the operator without a reason, or with one off the
	// list, is refused by the resolver whatever the rule says.
	const careless Kind = "careless-test-kind"
	registry[careless] = func(r Record) (Answer, bool) {
		return Answer{Owner: MoverOperator, Reason: r.Reason, Remedy: "approve it"}, true
	}
	defer delete(registry, careless)
	for _, reason := range []OperatorReason{"", "approval"} {
		if got := Resolve(careless, Record{Reason: reason}); got != want {
			t.Errorf("Resolve(a rule naming the operator for %q) = %+v, want %+v", reason, got, want)
		}
	}
	if got := Resolve(careless, Record{Reason: OperatorCredential}); !got.Owner.IsOperator() || got.Reason != OperatorCredential {
		t.Errorf("Resolve(a rule naming the operator for a credential) = %+v, want the operator", got)
	}
}

// The intake brake's escalated hold is the Lead Product Manager's, with the
// operator told, whoever escalated it (the architect's ruling of 2026-10-17 and
// operating rule 789c052c). Her release is read ahead of an escalation.
func TestAnEscalatedBrakeIsTheLeadProductManagersWithTheOperatorTold(t *testing.T) {
	t.Parallel()
	for name, record := range map[string]Record{
		"escalated":         {Hold: IntakeHold, Braked: true, BrakeEscalated: true},
		"escalated probing": {Hold: IntakeHold, Braked: true, BrakeEscalated: true, BrakeProbing: true},
		"as a stall":        {Stall: StallIntakeHold, Braked: true, BrakeEscalated: true},
	} {
		kind := KindHold
		if record.Stall != "" {
			kind = KindStall
		}
		got := Resolve(kind, record)
		if got.Owner != MoverProductManager || !strings.Contains(got.Remedy, "operator is told") {
			t.Errorf("%s: Resolve = %+v, want the Lead Product Manager with the operator told", name, got)
		}
	}
	if got := Resolve(KindHold, Record{Hold: IntakeHold, Braked: true, BrakeEscalated: true, BrakeReleased: true}); got.Owner != MoverHarness {
		t.Errorf("a released escalated brake = %+v, want the harness lifting it", got)
	}
	if got := Resolve(KindHold, Record{Hold: IntakeHold, Braked: true}); got.Owner != MoverDevelopmentManager {
		t.Errorf("a brake nobody has decided = %+v, want the development manager", got)
	}
	if got := Resolve(KindHold, Record{Hold: IntakeHold, HeldByOperator: true}); !got.Owner.IsOperator() || got.Reason != OperatorOwnHold {
		t.Errorf("the operator's own intake hold = %+v, want his, for his own hold", got)
	}
}

// The kinds the design's table names resolve as the table says, on the records
// that table describes.
func TestTheRulesFollowTheDesignsTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		kind   Kind
		record Record
		owner  Mover
		reason OperatorReason
	}{
		{"an amendment is its document owner's", KindAmendment, Record{Role: domain.RoleArchitect}, MoverArchitect, ""},
		{"a fundamental amendment is the operator's", KindAmendment, Record{Role: domain.RoleProductManager, FundamentalIntent: true}, MoverOperator, OperatorFundamentalIntent},
		{"a directive is the Lead Product Manager's", KindDirective, Record{}, MoverProductManager, ""},
		{"a report pile is the Lead Product Manager's", KindReports, Record{}, MoverProductManager, ""},
		{"an owed step is the harness's", KindOwedStep, Record{}, MoverHarness, ""},
		{"an unmerged request nothing explains is the development manager's", KindPublication, Record{PullRequest: true}, MoverDevelopmentManager, ""},
		{"a queued merge is the forge's", KindPublication, Record{PullRequest: true, MergeQueued: true}, MoverForge, ""},
		{"a degraded service is the harness's", KindDegradedService, Record{}, MoverHarness, ""},
		{"a conversation that will not open is the harness's", KindFailingTask, Record{ConversationUnopened: true}, MoverHarness, ""},
		{"the operator's hold is his", KindHold, Record{Hold: OperatorHold}, MoverOperator, OperatorOwnHold},
		{"the capacity hold is the harness's", KindHold, Record{Hold: CapacityHold}, MoverHarness, ""},
		{"an unreachable provider is nobody's", KindOutage, Record{OutageCause: domain.ProviderUnreachable}, MoverNobody, ""},
		{"an expired login is the operator's", KindOutage, Record{OutageCause: domain.ProviderUnauthenticated}, MoverOperator, OperatorCredential},
		{"an idle session is the harness's", KindStall, Record{Stall: StallSessionIdle}, MoverHarness, ""},
		{"no watch session is the harness's", KindStall, Record{Stall: StallNoWatchSession}, MoverHarness, ""},
		{"a diverged target is the operator's", KindStall, Record{Stall: StallDivergedTarget}, MoverOperator, OperatorDivergedHistory},
		{"a stoppage is the development manager's", KindStoppage, Record{}, MoverDevelopmentManager, ""},
		{"a first silent-stream stall is the harness's", KindStoppage, Record{HarnessContinues: true}, MoverHarness, ""},
		{"an escalation without a reason goes back to the development manager", KindOperatorAction, Record{FromEscalation: true}, MoverDevelopmentManager, ""},
		{"a handling without a reason goes back to the Lead Product Manager", KindOperatorAction, Record{FromReport: true}, MoverProductManager, ""},
		{"a handling without a reason goes back to the role that handled it", KindOperatorAction, Record{FromReport: true, Role: domain.RoleArchitect}, MoverArchitect, ""},
		{"a finding with a credential is the operator's", KindOperatorAction, Record{FromReport: true, Reason: OperatorCredential}, MoverOperator, OperatorCredential},
		{"a human gate is the operator's", KindHumanGate, Record{}, MoverOperator, OperatorHumanGate},
		{"a carried item is the role its marker names", KindCarriedItem, Record{Role: domain.RoleArchitect}, MoverArchitect, ""},
		{"a carried item naming no role is the unnamed role's", KindCarriedItem, Record{}, MoverUnnamed, ""},
		{"a lane block off the list is the Lead Product Manager's", KindLaneHumanBlock, Record{Reason: "approval"}, MoverProductManager, ""},
		{"a held group parked is the Lead Product Manager's", KindHeldWork, Record{Parked: true}, MoverProductManager, ""},
		{"a follow-up is the owner it recorded", KindFollowUp, Record{RecordedOwner: MoverArchitect}, MoverArchitect, ""},
		{"a follow-up the harness owns is the harness's", KindFollowUp, Record{RecordedOwner: MoverHarness}, MoverHarness, ""},
		{"a follow-up the operator owns for a credential is his", KindFollowUp, Record{RecordedOwner: MoverOperator, Reason: OperatorCredential}, MoverOperator, OperatorCredential},
		{"a follow-up recorded as the operator's for no reason is the Lead Product Manager's to classify", KindFollowUp, Record{RecordedOwner: MoverOperator, Reason: "approval"}, MoverProductManager, ""},
		{"a follow-up the operator recorded is never his", KindFollowUp, Record{RecordedOwner: MoverOperator, Reason: OperatorOwnHold, RecordedByOperator: true}, MoverProductManager, ""},
		{"a follow-up recording nobody the registry names is the Lead Product Manager's to classify", KindFollowUp, Record{RecordedOwner: "somebody"}, MoverProductManager, ""},
	}
	for _, c := range cases {
		got := Resolve(c.kind, c.record)
		if got.Owner != c.owner || got.Reason != c.reason {
			t.Errorf("%s: Resolve = %+v, want owner %s reason %q", c.name, got, c.owner, c.reason)
		}
	}
}
