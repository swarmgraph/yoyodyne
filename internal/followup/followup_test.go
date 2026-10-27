package followup

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

var written = time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)

// open is a whole follow-up: the development manager waits on the brake's
// trial run landing, which holds intake.
func open() FollowUp {
	return FollowUp{
		SchemaVersion: SchemaVersion,
		ID:            NewID(),
		Waits:         ownership.MoverDevelopmentManager,
		Owner:         ownership.MoverHarness,
		About:         []Subject{{Kind: SubjectHold, ID: "intake"}, {Kind: SubjectWorkItem, ID: "yoyodyne-ifd.432.25.21"}},
		Blocks:        Blocks{Items: []string{"yoyodyne-ifd.432.25.19"}, HoldsIntake: true},
		Check:         Check{Name: CheckItemLanded, Arguments: map[string]string{ArgItem: "yoyodyne-ifd.432.25.21"}},
		BackToOwnerAt: written.Add(45 * time.Minute),
		ToManagerAt:   written.Add(4 * time.Hour),
		Origin:        Origin{Kind: OriginHarness},
		CreatedAt:     written,
		State:         StateOpen,
	}
}

func TestAWholeFollowUpIsValid(t *testing.T) {
	t.Parallel()
	if err := open().Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if !ValidID(NewID()) || NewID() == NewID() {
		t.Fatal("NewID issues identifiers that are not its own shape, or not fresh")
	}
}

// Every field the design names is required, and each refusal says which.
func TestAFollowUpMissingAFieldIsRefused(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		change func(*FollowUp)
		says   string
	}{
		"no schema":              {func(f *FollowUp) { f.SchemaVersion = 0 }, "schema version"},
		"no id":                  {func(f *FollowUp) { f.ID = "" }, "id"},
		"an id that is a path":   {func(f *FollowUp) { f.ID = "follow-up-../../etc" }, "id"},
		"nobody waits":           {func(f *FollowUp) { f.Waits = "" }, "who waits"},
		"the harness waits":      {func(f *FollowUp) { f.Waits = ownership.MoverHarness }, "who waits"},
		"no owner":               {func(f *FollowUp) { f.Owner = "" }, "owner"},
		"an unnamed owner":       {func(f *FollowUp) { f.Owner = ownership.MoverUnnamed }, "owner"},
		"a reason on a role":     {func(f *FollowUp) { f.OwnerReason = ownership.OperatorCredential }, "operator's reason"},
		"about nothing":          {func(f *FollowUp) { f.About = nil }, "what it is about"},
		"about an unknown kind":  {func(f *FollowUp) { f.About = []Subject{{Kind: "thing", ID: "x"}} }, "cannot be about"},
		"about a sentence":       {func(f *FollowUp) { f.About = []Subject{{Kind: SubjectReport, ID: "please confirm"}} }, "not an identifier"},
		"blocks a sentence":      {func(f *FollowUp) { f.Blocks.Items = []string{"the next thing"} }, "an item it blocks"},
		"keeps no stop listed":   {func(f *FollowUp) { f.KeepsStopListed = true }, "keeps a stop listed"},
		"no check":               {func(f *FollowUp) { f.Check = Check{} }, "closed list"},
		"no first deadline":      {func(f *FollowUp) { f.BackToOwnerAt = time.Time{} }, "two deadlines"},
		"no second deadline":     {func(f *FollowUp) { f.ToManagerAt = time.Time{} }, "two deadlines"},
		"deadlines out of order": {func(f *FollowUp) { f.ToManagerAt = f.BackToOwnerAt }, "second deadline"},
		"a deadline before":      {func(f *FollowUp) { f.BackToOwnerAt = written.Add(-time.Minute) }, "before it was written"},
		"no origin":              {func(f *FollowUp) { f.Origin = Origin{} }, "origin"},
		"a role origin, no role": {func(f *FollowUp) { f.Origin = Origin{Kind: OriginRole} }, "origin names role"},
		"a harness origin, role": {func(f *FollowUp) { f.Origin = Origin{Kind: OriginHarness, Role: domain.RoleArchitect} }, "names no role"},
		"no time written":        {func(f *FollowUp) { f.CreatedAt = time.Time{} }, "when it was written"},
		"no state":               {func(f *FollowUp) { f.State = "" }, "state"},
		"open, yet passed":       {func(f *FollowUp) { f.Passed = &Passed{At: written} }, "open follow-up"},
		"passed, no time":        {func(f *FollowUp) { f.State, f.Passed = StatePassed, &Passed{} }, "when its check passed"},
		"passed, never recorded": {func(f *FollowUp) { f.State = StatePassed }, "passed follow-up"},
		"closed, no reason":      {func(f *FollowUp) { f.State, f.Closed = StateClosed, &Closed{By: ClosedByOwner, At: written} }, "reason is required"},
		"closed by nobody":       {func(f *FollowUp) { f.State, f.Closed = StateClosed, &Closed{Reason: "done", At: written} }, "closed by"},
		"closed and passed": {func(f *FollowUp) {
			f.State, f.Closed, f.Passed = StateClosed, &Closed{By: ClosedByOwner, Reason: "x", At: written}, &Passed{At: written}
		}, "nothing else ending it"},
		"closed, never recorded": {func(f *FollowUp) { f.State = StateClosed }, "who closed it"},
		"closed with no time":    {func(f *FollowUp) { f.State, f.Closed = StateClosed, &Closed{By: ClosedByOwner, Reason: "x"} }, "when it was closed"},
		"an unbounded reason": {func(f *FollowUp) {
			f.State, f.Closed = StateClosed, &Closed{By: ClosedByOwner, Reason: strings.Repeat("x", MaxTextBytes+1), At: written}
		}, "byte bound"},
		"a check off the list":    {func(f *FollowUp) { f.Check = Check{Name: "whenever"} }, "closed list"},
		"a check naming command":  {func(f *FollowUp) { f.Check = Check{Name: "xcodebuild -license check"} }, "never a command"},
		"an argument it lacks":    {func(f *FollowUp) { f.Check.Arguments = nil }, "needs its"},
		"an argument it does not": {func(f *FollowUp) { f.Check.Arguments["command"] = "make test" }, "takes no"},
	}
	for name, c := range cases {
		f := open()
		f.Check.Arguments = map[string]string{ArgItem: "yoyodyne-ifd.432.25.21"}
		c.change(&f)
		err := f.Validate()
		if err == nil {
			t.Errorf("%s: Validate() = nil, want a refusal", name)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: Validate() = %v, want it to say %q", name, err, c.says)
		}
	}
}

// A wait on a stopped run may keep the stop listed; nothing else may.
func TestOnlyAWaitOnAStopKeepsItListed(t *testing.T) {
	t.Parallel()
	f := open()
	f.About = []Subject{{Kind: SubjectStop, ID: "run-1fca3a7c87880f3016924bdd7093225c"}}
	f.KeepsStopListed = true
	f.Check = Check{Name: CheckRunReloaded, Arguments: map[string]string{ArgRun: "run-1fca3a7c87880f3016924bdd7093225c"}}
	if err := f.Validate(); err != nil {
		t.Fatalf("a wait on a stop that keeps it listed: Validate() = %v", err)
	}
}

// The operator owns a follow-up only with a reason from his closed list, and
// never one he or his assistant recorded.
func TestTheOperatorOwnsAFollowUpOnlyWithAReasonOnHisClosedList(t *testing.T) {
	t.Parallel()
	for _, reason := range []ownership.OperatorReason{"", "approval", "decision"} {
		f := open()
		f.Owner, f.OwnerReason = ownership.MoverOperator, reason
		if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "closed list") {
			t.Errorf("the operator owning it for %q: Validate() = %v, want a refusal naming the closed list", reason, err)
		}
	}
	for _, reason := range ownership.OperatorReasons() {
		f := open()
		f.Owner, f.OwnerReason = ownership.MoverOperator, reason
		if err := f.Validate(); err != nil {
			t.Errorf("the operator owning it for %q: Validate() = %v", reason, err)
		}
		for _, origin := range []OriginKind{OriginOperator, OriginAssistant} {
			f.Origin = Origin{Kind: origin}
			if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "never the operator's to own") {
				t.Errorf("recorded by %s and owned by the operator: Validate() = %v, want a refusal", origin, err)
			}
		}
	}
}

// The registry's rule for the follow-up kind returns the owner the follow-up
// recorded, and the operator only as the record names him with a reason.
func TestTheRegistryReturnsTheRecordedOwner(t *testing.T) {
	t.Parallel()
	f := open()
	f.Owner = ownership.MoverArchitect
	if got := ownership.Resolve(ownership.KindFollowUp, f.Ownership()); got.Owner != ownership.MoverArchitect || got.Reason != "" {
		t.Errorf("Resolve = %+v, want the architect it recorded", got)
	}
	f.Owner, f.OwnerReason = ownership.MoverOperator, ownership.OperatorCredential
	if got := ownership.Resolve(ownership.KindFollowUp, f.Ownership()); !got.Owner.IsOperator() || got.Reason != ownership.OperatorCredential {
		t.Errorf("Resolve = %+v, want the operator for a credential", got)
	}
	f.Origin = Origin{Kind: OriginAssistant}
	if got := ownership.Resolve(ownership.KindFollowUp, f.Ownership()); got.Owner != ownership.MoverProductManager {
		t.Errorf("Resolve of one his assistant recorded naming him = %+v, want the Lead Product Manager", got)
	}
}

func TestAPassingCheckEndsAnOpenFollowUp(t *testing.T) {
	t.Parallel()
	passed, err := open().Pass(written.Add(time.Hour), "the item has a run that landed")
	if err != nil {
		t.Fatal(err)
	}
	if passed.State != StatePassed || passed.Passed == nil || !passed.Passed.At.Equal(written.Add(time.Hour)) {
		t.Fatalf("Pass = %+v, want it passed at the time given", passed)
	}
	if _, err := passed.Pass(written.Add(2*time.Hour), ""); !errors.Is(err, ErrNotOpen) {
		t.Errorf("passing a passed follow-up = %v, want ErrNotOpen", err)
	}
	if _, err := passed.Close(ClosedByOperator, "override", written); !errors.Is(err, ErrNotOpen) {
		t.Errorf("closing a passed follow-up = %v, want ErrNotOpen", err)
	}
	if _, err := open().Pass(time.Time{}, ""); err == nil {
		t.Error("a pass with no time was accepted")
	}
}

// Each of the three ways somebody ends a follow-up takes a reason, and one
// without a reason is refused.
func TestACloseOrWithdrawalWithoutAReasonIsRefused(t *testing.T) {
	t.Parallel()
	for _, by := range Closers() {
		for _, reason := range []string{"", "   ", "\n"} {
			if _, err := open().Close(by, reason, written); err == nil || !strings.Contains(err.Error(), "reason is required") {
				t.Errorf("closed by %s with reason %q: %v, want a refusal for the missing reason", by, reason, err)
			}
		}
		closed, err := open().Close(by, "no longer needed: the item it waited on was retired", written.Add(time.Minute))
		if err != nil {
			t.Fatalf("closed by %s: %v", by, err)
		}
		if closed.State != StateClosed || closed.Closed.By != by || closed.Closed.Reason == "" {
			t.Errorf("closed by %s = %+v, want closed with who and why", by, closed)
		}
		if _, err := closed.Close(by, "again", written); !errors.Is(err, ErrNotOpen) {
			t.Errorf("closing a closed follow-up = %v, want ErrNotOpen", err)
		}
	}
	for _, by := range []Closer{"", "reader", "harness"} {
		if _, err := open().Close(by, "read it", written); err == nil {
			t.Errorf("closed by %q was accepted; only the owner, whoever waits, and the operator close one", by)
		}
	}
}
