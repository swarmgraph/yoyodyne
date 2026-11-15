package directive

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

var recordedAt = time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)

// A directive that pauses work has to say what it is waiting for, and one that
// does not must not pretend to. Both halves are what keeps a pause liftable: a
// record with nothing unresolved stops work nobody can release, and an
// operational directive carrying one would read as holding work up when it is
// already in effect.
func TestValidateHoldsEachKindToWhatItMustSay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Directive)
		wantErr string
	}{
		{name: "an operational directive needs nothing unresolved"},
		{
			name:    "an ambiguous directive must name what is unresolved",
			mutate:  func(d *Directive) { d.Kind = KindAmbiguous },
			wantErr: "unresolved is required",
		},
		{
			name: "an ambiguous directive that names it is valid",
			mutate: func(d *Directive) {
				d.Kind = KindAmbiguous
				d.Unresolved = "which of the two readings was meant"
			},
		},
		{
			name: "an artifact directive must name the artifact",
			mutate: func(d *Directive) {
				d.Kind = KindArtifact
				d.Unresolved = "whether the goal still covers this"
			},
			wantErr: "artifact is required",
		},
		{
			name:    "an operational directive may not name an artifact",
			mutate:  func(d *Directive) { d.Artifact = "docs/product/brief.md" },
			wantErr: "names an artifact",
		},
		{
			name:    "an operational directive may not carry something unresolved",
			mutate:  func(d *Directive) { d.Unresolved = "what was meant" },
			wantErr: "in effect already",
		},
		{
			name:    "a kind the harness does not know is refused",
			mutate:  func(d *Directive) { d.Kind = "urgent" },
			wantErr: "is not a directive kind",
		},
		{
			name:    "a resolution with no time is half a settlement",
			mutate:  func(d *Directive) { d.Resolution = "settled in conversation" },
			wantErr: "requires the time it was resolved",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorded := operational()
			if test.mutate != nil {
				test.mutate(&recorded)
			}
			err := recorded.Validate()
			switch {
			case test.wantErr == "" && err != nil:
				t.Fatalf("Validate() error = %v, want none", err)
			case test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)):
				t.Fatalf("Validate() error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}

// Only an unresolved directive of a pausing kind holds work up. Settling one is
// exactly what releases the work, so a resolved record must stop constraining
// anything the moment it is settled.
func TestOnlyUnresolvedArtifactAndAmbiguousDirectivesPauseWork(t *testing.T) {
	t.Parallel()

	for _, kind := range []Kind{KindOperational, KindArtifact, KindAmbiguous} {
		recorded := operational()
		recorded.Kind = kind
		if kind.Pauses() {
			recorded.Unresolved = "what was meant"
		}
		if kind == KindArtifact {
			recorded.Artifact = "docs/product/brief.md"
		}
		if got := recorded.Pauses(); got != kind.Pauses() {
			t.Fatalf("%s Pauses() = %t, want %t", kind, got, kind.Pauses())
		}
		if !kind.Pauses() {
			continue
		}
		resolved, err := recorded.Resolve("the operator answered", recordedAt.Add(time.Hour))
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if resolved.Pauses() {
			t.Fatalf("a resolved %s directive still pauses work", kind)
		}
		if _, err := resolved.Resolve("again", recordedAt.Add(2*time.Hour)); err == nil {
			t.Fatal("Resolve() on a settled directive error = nil, want a refusal")
		}
	}
}

// An operational directive is settled by being carried out, which is the only
// account there is of what became of the commonest kind of directive: it takes
// effect the moment it is recorded and has nothing to resolve, so without an
// outcome it stands open forever and whoever asked for it is never told the work
// exists.
func TestAnOperationalDirectiveIsSettledByBeingCarriedOut(t *testing.T) {
	t.Parallel()

	recorded := operational()
	if recorded.Resolved() {
		t.Fatal("a directive nobody has settled reads as settled")
	}
	carried, err := recorded.CarryOut("admitted yoyodyne-ifd.170 to the backlog: stop opening those pull requests", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("CarryOut() error = %v", err)
	}
	if !carried.Resolved() || carried.Pauses() {
		t.Fatalf("carried = %#v, want a settled directive that pauses nothing", carried)
	}
	// The outcome names the work, because a thread told its directive was acted
	// on is told nothing it can follow.
	if !strings.Contains(carried.Render(), "carried out") || !strings.Contains(carried.Render(), "yoyodyne-ifd.170") {
		t.Fatalf("Render() = %q, want it to say it was carried out and name what it became", carried.Render())
	}
	// One record, one account of what became of it.
	if _, err := carried.CarryOut("admitted again", recordedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("CarryOut() on a settled directive error = nil, want a refusal")
	}
	if _, err := recorded.CarryOut("", recordedAt.Add(time.Hour)); err == nil {
		t.Fatal("CarryOut() with no outcome error = nil, want a refusal")
	}
}

// Recording what came of an operational directive does not withdraw it. It is a
// standing instruction — "stop opening pull requests for documentation-only
// changes" is still the instruction after the item it prompted is admitted — so
// having an account of it and having lapsed are two different facts, and only
// the pausing kinds have ever ended by being settled.
//
// This is the half that matters most, because before an operational directive
// could be settled at all it was permanently unresolved and so permanently in
// force. Anything that read "still live" off the absence of a disposition would
// silently retire the operator's instruction the first time work was admitted
// for it, which is the failure this package exists to end arriving through its
// own front door.
func TestCarryingOutAnOperationalDirectiveLeavesItInForce(t *testing.T) {
	t.Parallel()

	recorded := operational()
	if !recorded.InForce() {
		t.Fatal("a recorded operational directive is not in force")
	}
	carried, err := recorded.CarryOut("admitted yoyodyne-ifd.170 to the backlog", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("CarryOut() error = %v", err)
	}
	if !carried.InForce() {
		t.Fatalf("carried = %#v, want a standing instruction still in force after it was acted on", carried)
	}
	// The two facts stay apart: it is accounted for, and it still applies.
	if !carried.Resolved() {
		t.Fatalf("carried = %#v, want the outcome recorded on it", carried)
	}
}

// A directive that pauses work is the one that ends by being settled: resolving
// it is exactly what lifts the hold, so it stops being in force at that moment.
func TestResolvingAPausingDirectiveTakesItOutOfForce(t *testing.T) {
	t.Parallel()

	pausing := operational()
	pausing.Kind = KindAmbiguous
	pausing.Unresolved = "which of the two readings was meant"
	if !pausing.InForce() {
		t.Fatal("an unresolved directive that pauses work is not in force")
	}
	resolved, err := pausing.Resolve("the second reading", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.InForce() {
		t.Fatalf("resolved = %#v, want a lifted pause to stop constraining work", resolved)
	}
}

// The two acts refuse each other's kinds. Resolving is somebody answering what
// held work up and carrying out is somebody having done what was asked, so an
// outcome written onto a pausing directive would lift its pause on the strength
// of something that never answered it.
func TestResolvingAndCarryingOutRefuseEachOthersKinds(t *testing.T) {
	t.Parallel()

	pausing := operational()
	pausing.Kind = KindAmbiguous
	pausing.Unresolved = "which of the two readings was meant"
	if _, err := pausing.CarryOut("admitted yoyodyne-ifd.170 to the backlog", recordedAt.Add(time.Hour)); err == nil {
		t.Fatal("CarryOut() on a directive that pauses work error = nil, want a refusal")
	}
	if _, err := operational().Resolve("the second reading", recordedAt.Add(time.Hour)); err == nil {
		t.Fatal("Resolve() on a directive that pauses nothing error = nil, want a refusal")
	}
}

// A reference is what somebody types to name a directive, and it is checked
// before anything is recorded against it. Whether any directive answers to it
// needs the records; this only refuses what was never going to name one.
func TestValidReferenceAcceptsAnIdentifierAndAnyPrefixOfOne(t *testing.T) {
	t.Parallel()

	full := "directive-" + strings.Repeat("0", 32)
	for _, valid := range []string{full, "directive-0", "directive-3f2a", "  " + full + "  "} {
		if !ValidReference(valid) {
			t.Fatalf("ValidReference(%q) = false, want a reference the store can look up", valid)
		}
	}
	for _, invalid := range []string{"", "directive-", "3f2a", "directive-zzzz", full + "0", "yoyodyne-ifd.170"} {
		if ValidReference(invalid) {
			t.Fatalf("ValidReference(%q) = true, want it refused", invalid)
		}
	}
}

// An unscoped directive reaches every item. It is the conservative reading and a
// deliberate one: a directive that rewrites the brief affects whatever was
// derived from the brief, and nothing here can yet say which work that is.
func TestAnUnscopedDirectiveAffectsEveryItemAndAScopedOneOnlyWhatItNames(t *testing.T) {
	t.Parallel()

	unscoped := operational()
	if !unscoped.Affects("yoyodyne-anything") {
		t.Fatal("an unscoped directive did not affect an item")
	}
	scoped := operational()
	scoped.Scope = []string{"yoyodyne-ifd.1", "yoyodyne-ifd.2"}
	if !scoped.Affects("yoyodyne-ifd.2") {
		t.Fatal("a scoped directive did not affect an item it named")
	}
	if scoped.Affects("yoyodyne-ifd.3") {
		t.Fatal("a scoped directive affected an item it did not name")
	}
}

// Pausing is the one question the run pipeline asks, so it has to select on both
// halves at once: the kind that pauses, and the item it reaches.
func TestPausingSelectsOnlyWhatHoldsOneItemUp(t *testing.T) {
	t.Parallel()

	ambiguous := operational()
	ambiguous.ID = "directive-" + strings.Repeat("a", 32)
	ambiguous.Kind = KindAmbiguous
	ambiguous.Unresolved = "what was meant"
	ambiguous.Scope = []string{"yoyodyne-ifd.1"}

	elsewhere := ambiguous
	elsewhere.ID = "directive-" + strings.Repeat("b", 32)
	elsewhere.Scope = []string{"yoyodyne-ifd.9"}

	pausing := Pausing([]Directive{operational(), ambiguous, elsewhere}, "yoyodyne-ifd.1")
	if len(pausing) != 1 || pausing[0].ID != ambiguous.ID {
		t.Fatalf("Pausing() = %#v, want only the unresolved directive scoped to the item", pausing)
	}
}

// What a paused run records about why has to name both the directive and what is
// unresolved: those are the two things somebody needs in order to lift it.
func TestSummaryNamesTheDirectiveAndWhatIsUnresolved(t *testing.T) {
	t.Parallel()

	recorded := operational()
	recorded.Kind = KindAmbiguous
	recorded.Unresolved = "which of the two readings was meant"
	summary := recorded.Summary()
	for _, wanted := range []string{recorded.ID, string(KindAmbiguous), recorded.Text, recorded.Unresolved} {
		if !strings.Contains(summary, wanted) {
			t.Fatalf("Summary() = %q, want it to mention %q", summary, wanted)
		}
	}
}

// Withdrawing is the only thing that ends an operational directive, and it ends
// it without claiming anything was done about it. Before it existed the record
// could only ever grow the set of things it said were in force: a question read
// as an instruction was in force from the moment it was written down and stayed
// there, listed as live direction and met by every run that read it, with no
// verb anywhere that could take it out.
func TestWithdrawingAnOperationalDirectiveTakesItOutOfForce(t *testing.T) {
	t.Parallel()

	recorded := operational()
	withdrawn, err := recorded.Withdraw("the operator, at a command line", "",
		"recorded in error: this was a question about a run, not an instruction", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if withdrawn.InForce() {
		t.Fatalf("withdrawn = %#v, want a withdrawn directive to stop constraining work", withdrawn)
	}
	// It was taken back rather than acted on, so nothing about it reads as a
	// disposition: an operator scanning what came of their directives must not
	// find this one among the things somebody did.
	if withdrawn.Resolved() {
		t.Fatalf("withdrawn = %#v, want withdrawing it to settle nothing", withdrawn)
	}
	if !withdrawn.Withdrawn() {
		t.Fatalf("withdrawn = %#v, want the record to say it was withdrawn", withdrawn)
	}
}

// A withdrawn directive reads as withdrawn rather than as a record somebody
// deleted. Everything it ever said is still on it, and it has gained who ended
// it and when — which is what lets a run that was held or judged while it stood
// still be explicable afterwards.
func TestAWithdrawnDirectiveKeepsWhatItSaidAndSaysWhoEndedIt(t *testing.T) {
	t.Parallel()

	carried, err := operational().CarryOut("admitted yoyodyne-ifd.170 to the backlog", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("CarryOut() error = %v", err)
	}
	withdrawn, err := carried.Withdraw("the operator, at a command line", "",
		"we open small documentation pull requests again", recordedAt.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	// A standing instruction that was acted on and later taken back is both
	// things, and the record says both: what it produced, and that it no longer
	// applies.
	if !withdrawn.Resolved() || withdrawn.InForce() {
		t.Fatalf("withdrawn = %#v, want an acted-on instruction that is no longer in force", withdrawn)
	}
	rendered := withdrawn.Render()
	for _, wanted := range []string{
		withdrawn.Text,
		"carried out",
		"yoyodyne-ifd.170",
		"withdrawn",
		"no longer applies",
		"the operator, at a command line",
		"we open small documentation pull requests again",
	} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("Render() = %q, want it to mention %q", rendered, wanted)
		}
	}
}

// A withdrawal made under a role carries the role, so a surface that answers the
// thread the directive came from can answer in that role's voice, and the listing
// says so beside who did it. One made by the operator at a terminal carries none,
// which is what the operator being nobody's persona looks like on the record.
func TestAWithdrawalCarriesTheRoleItWasMadeUnder(t *testing.T) {
	t.Parallel()

	withdrawn, err := operational().Withdraw("the operator, from conversation chat-1, after turn 3", domain.RoleProductManager,
		"recorded in error", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if withdrawn.WithdrawnRole != domain.RoleProductManager {
		t.Fatalf("withdrawn role = %q, want the role it was withdrawn under", withdrawn.WithdrawnRole)
	}
	if rendered := withdrawn.Render(); !strings.Contains(rendered, "(as the Lead Product Manager)") {
		t.Fatalf("Render() = %q, want the role named beside who withdrew it", rendered)
	}
	if _, err := operational().Withdraw("the operator, at a command line", "janitor", "recorded in error", recordedAt.Add(time.Hour)); err == nil {
		t.Fatal("Withdraw() under a role the harness does not have error = nil, want a refusal")
	}
	plain, err := operational().Withdraw("the operator, at a command line", "", "recorded in error", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if plain.WithdrawnRole != "" || strings.Contains(plain.Render(), "(as the") {
		t.Fatalf("withdrawn = %#v, want no role on a withdrawal the operator made at a terminal", plain)
	}
}

// Withdrawing a directive that pauses work lifts the pause without answering
// what it was waiting for. That is what taking back a question means: there is
// no answer to one the operator no longer means to have asked, and leaving the
// work held would be enforcing something nobody means.
func TestWithdrawingAPausingDirectiveLiftsThePauseUnanswered(t *testing.T) {
	t.Parallel()

	pausing := operational()
	pausing.Kind = KindAmbiguous
	pausing.Unresolved = "which of the two readings was meant"
	pausing.Scope = []string{"yoyodyne-ifd.1"}
	withdrawn, err := pausing.Withdraw("the operator, at a command line", "",
		"never mind: the work went the other way and the question no longer arises", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if withdrawn.Pauses() {
		t.Fatalf("withdrawn = %#v, want a withdrawn directive to hold no work", withdrawn)
	}
	if held := Pausing([]Directive{withdrawn}, "yoyodyne-ifd.1"); len(held) != 0 {
		t.Fatalf("Pausing() = %#v, want a withdrawn directive to pause nothing", held)
	}
	if withdrawn.Resolved() {
		t.Fatalf("withdrawn = %#v, want the pause lifted without an answer being claimed", withdrawn)
	}
}

// A withdrawal says who and why, once. Without who it is a directive that
// stopped applying because of nobody; without why the record keeps the
// operator's words and cannot say what happened to them; and a second one would
// overwrite the account of who ended it.
func TestWithdrawalRefusesWhatWouldLeaveTheRecordUnanswerable(t *testing.T) {
	t.Parallel()

	recorded := operational()
	if _, err := recorded.Withdraw("", "", "recorded in error", recordedAt.Add(time.Hour)); err == nil {
		t.Fatal("Withdraw() with nobody withdrawing it error = nil, want a refusal")
	}
	if _, err := recorded.Withdraw("the operator, at a command line", "", "  ", recordedAt.Add(time.Hour)); err == nil {
		t.Fatal("Withdraw() with no reason error = nil, want a refusal")
	}
	withdrawn, err := recorded.Withdraw("the operator, at a command line", "", "recorded in error", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if _, err := withdrawn.Withdraw("the operator, at a command line", "", "again", recordedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("Withdraw() on a withdrawn directive error = nil, want a refusal")
	}
	// Nothing is written onto a directive nobody means any more: an outcome on it
	// would say a withdrawn instruction was carried out.
	if _, err := withdrawn.CarryOut("admitted yoyodyne-ifd.170 to the backlog", recordedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("CarryOut() on a withdrawn directive error = nil, want a refusal")
	}
}

// A pausing directive somebody resolved has already ended, and the work it held
// has already resumed on the strength of the answer. Withdrawing it afterwards
// would be taking back something that is over.
func TestWithdrawalRefusesADirectiveThatHasAlreadyEnded(t *testing.T) {
	t.Parallel()

	pausing := operational()
	pausing.Kind = KindAmbiguous
	pausing.Unresolved = "which of the two readings was meant"
	resolved, err := pausing.Resolve("the second reading", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if _, err := resolved.Withdraw("the operator, at a command line", "", "never mind", recordedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("Withdraw() on a resolved directive error = nil, want a refusal")
	}
	if _, err := resolved.Resolve("again", recordedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("Resolve() on a resolved directive error = nil, want a refusal")
	}
}

// A withdrawal, who made it, and when it was made are one fact. Half of it
// describes a directive that stopped applying because of nobody, or at no time,
// which is not a state anything can act on or anybody can ask about.
func TestValidateHoldsAWithdrawalToWhoAndWhen(t *testing.T) {
	t.Parallel()

	withdrawnAt := recordedAt.Add(time.Hour)
	tests := []struct {
		name    string
		mutate  func(*Directive)
		wantErr string
	}{
		{
			name:    "a reason with nobody and no time is refused",
			mutate:  func(d *Directive) { d.Withdrawal = "recorded in error" },
			wantErr: "requires who withdrew it",
		},
		{
			name:    "somebody withdrawing it at no time is refused",
			mutate:  func(d *Directive) { d.WithdrawnBy = "the operator, at a command line" },
			wantErr: "requires who withdrew it",
		},
		{
			name: "a withdrawal with nobody named is refused",
			mutate: func(d *Directive) {
				d.Withdrawal = "recorded in error"
				d.WithdrawnAt = &withdrawnAt
			},
			wantErr: "withdrawn by is required",
		},
		{
			name: "a withdrawal with no reason is refused",
			mutate: func(d *Directive) {
				d.WithdrawnBy = "the operator, at a command line"
				d.WithdrawnAt = &withdrawnAt
			},
			wantErr: "withdrawal is required",
		},
		{
			name: "all three together are valid",
			mutate: func(d *Directive) {
				d.Withdrawal = "recorded in error"
				d.WithdrawnBy = "the operator, at a command line"
				d.WithdrawnAt = &withdrawnAt
			},
		},
		{
			name:    "a role with no withdrawal is refused",
			mutate:  func(d *Directive) { d.WithdrawnRole = domain.RoleProductManager },
			wantErr: "requires who withdrew it",
		},
		{
			name: "a role the harness does not have is refused",
			mutate: func(d *Directive) {
				d.Withdrawal = "recorded in error"
				d.WithdrawnBy = "the operator, from conversation chat-1, after turn 3"
				d.WithdrawnAt = &withdrawnAt
				d.WithdrawnRole = "janitor"
			},
			wantErr: "withdrawn role",
		},
		{
			name: "a role the harness has is valid beside the three",
			mutate: func(d *Directive) {
				d.Withdrawal = "recorded in error"
				d.WithdrawnBy = "the operator, from conversation chat-1, after turn 3"
				d.WithdrawnAt = &withdrawnAt
				d.WithdrawnRole = domain.RoleProductManager
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			recorded := operational()
			test.mutate(&recorded)
			err := recorded.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want none", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}

// A standing instruction written into a document is resolved into it: it stops
// applying as a directive, says what it became, who did it and why, and keeps the
// outcome it had collected while it stood.
func TestResolvingAStandingDirectiveIntoADocumentEndsIt(t *testing.T) {
	t.Parallel()

	carried, err := operational().CarryOut("admitted yoyodyne-ifd.170 to the backlog", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("CarryOut() error = %v", err)
	}
	ended, err := carried.ResolveInto("docs/product/operating-rules.md",
		"the Lead Product Manager, from conversation chat-1, after turn 956", domain.RoleProductManager,
		"written into the operating rules", recordedAt.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("ResolveInto() error = %v", err)
	}
	if ended.InForce() || !ended.ResolvedInto() {
		t.Fatalf("ended = %#v, want it out of force and resolved into the document", ended)
	}
	if ended.Resolution != "admitted yoyodyne-ifd.170 to the backlog" {
		t.Fatalf("resolution = %q, want the earlier outcome kept", ended.Resolution)
	}
	rendered := ended.Render()
	for _, want := range []string{
		"carried out " + recordedAt.Add(time.Hour).Format(time.RFC3339),
		"resolved into docs/product/operating-rules.md",
		"(as the lead product manager)",
		"written into the operating rules",
	} {
		if !strings.Contains(strings.ToLower(rendered), strings.ToLower(want)) {
			t.Errorf("Render() = %q, want it to contain %q", rendered, want)
		}
	}
	// Nothing further is written onto it, however it would have ended.
	if _, err := ended.ResolveInto("docs/elsewhere.md", "someone", "", "again", recordedAt.Add(3*time.Hour)); err == nil ||
		!strings.Contains(err.Error(), "was resolved into docs/product/operating-rules.md") {
		t.Fatalf("ResolveInto() again error = %v, want a refusal naming the first", err)
	}
	if _, err := ended.Withdraw("the operator, at a command line", "", "never mind", recordedAt.Add(3*time.Hour)); err == nil {
		t.Fatal("Withdraw() on a resolved-into directive error = nil, want a refusal")
	}
	if _, err := ended.CarryOut("more", recordedAt.Add(3*time.Hour)); err == nil {
		t.Fatal("CarryOut() on a resolved-into directive error = nil, want a refusal")
	}
}

// Resolving a pausing directive into what carries it answers the pause with the
// reason given, so every reader asking whether it was answered agrees.
func TestResolvingAPausingDirectiveIntoAnItemLiftsThePause(t *testing.T) {
	t.Parallel()

	pausing := operational()
	pausing.Kind = KindAmbiguous
	pausing.Unresolved = "which of the two readings was meant"
	pausing.Scope = []string{"yoyodyne-ifd.1"}
	ended, err := pausing.ResolveInto("yoyodyne-ifd.2", "the Lead Product Manager, from conversation chat-1, after turn 3",
		domain.RoleProductManager, "the second reading, admitted as its own item", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("ResolveInto() error = %v", err)
	}
	if !ended.Resolved() || ended.Resolution != "the second reading, admitted as its own item" || ended.Pauses() {
		t.Fatalf("ended = %#v, want it resolved with the reason and holding nothing", ended)
	}
	if _, err := ended.Resolve("again", recordedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("Resolve() after ResolveInto() error = nil, want a refusal")
	}
}

// Resolving refuses what would leave the record unanswerable, and a directive
// that already ended some other way.
func TestResolvingIntoRefusesWhatItCannotRecord(t *testing.T) {
	t.Parallel()

	recorded := operational()
	for _, bad := range []struct{ became, by, reason string }{
		{"", "someone", "why"},
		{"docs/x.md", "", "why"},
		{"docs/x.md", "someone", " "},
		{"docs/x.md\nand more", "someone", "why"},
	} {
		if _, err := recorded.ResolveInto(bad.became, bad.by, "", bad.reason, recordedAt.Add(time.Hour)); err == nil {
			t.Fatalf("ResolveInto(%q, %q, %q) error = nil, want a refusal", bad.became, bad.by, bad.reason)
		}
	}
	withdrawn, err := recorded.Withdraw("the operator, at a command line", "", "a question", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if _, err := withdrawn.ResolveInto("docs/x.md", "someone", "", "why", recordedAt.Add(2*time.Hour)); err == nil ||
		!strings.Contains(err.Error(), "was withdrawn at") {
		t.Fatalf("ResolveInto() on a withdrawn directive error = %v, want a refusal naming the withdrawal", err)
	}
	pausing := operational()
	pausing.Kind = KindAmbiguous
	pausing.Unresolved = "which"
	resolved, err := pausing.Resolve("the first", recordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if _, err := resolved.ResolveInto("docs/x.md", "someone", "", "why", recordedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("ResolveInto() on a resolved pausing directive error = nil, want a refusal")
	}
	half := operational()
	half.Became = "docs/x.md"
	if err := half.Validate(); err == nil || !strings.Contains(err.Error(), "requires who resolved it") {
		t.Fatalf("Validate() of half a resolution error = %v, want a refusal", err)
	}
}

func operational() Directive {
	return Directive{
		SchemaVersion: SchemaVersion,
		ID:            "directive-" + strings.Repeat("0", 32),
		ProductID:     "yoyodyne",
		Kind:          KindOperational,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    recordedAt,
		Text:          "stop opening pull requests for documentation-only changes",
	}
}

// Two factories that see the same Slack reply name the same directive, and the
// name is one the store accepts.
func TestASlackMessageNamesOneDirective(t *testing.T) {
	t.Parallel()

	id := ForSlackMessage("C0123", "1760000000.000100")
	if !ValidID(id) {
		t.Fatalf("ForSlackMessage() = %q, want a valid directive id", id)
	}
	if again := ForSlackMessage("C0123", "1760000000.000100"); again != id {
		t.Fatalf("ForSlackMessage() = %q then %q, want the same name", id, again)
	}
	if ForSlackMessage("C0124", "1760000000.000100") == id || ForSlackMessage("C0123", "1760000000.000101") == id {
		t.Fatal("ForSlackMessage() gave one name to different messages")
	}
	if ForSlackMessage("C01", "230") == ForSlackMessage("C0123", "0") {
		t.Fatal("ForSlackMessage() let the channel and timestamp run together")
	}
}

func TestMergeFillsWhatTheCopyLacksAndChangesNothingItSays(t *testing.T) {
	t.Parallel()

	earlier := recordedAt.Add(time.Hour)
	later := recordedAt.Add(2 * time.Hour)
	base := operational()
	other := operational()
	other.Resolution, other.ResolvedAt = "carried out", &later
	other.Withdrawal, other.WithdrawnBy, other.WithdrawnAt, other.WithdrawnRole = "no longer meant", "the operator", &later, domain.RoleProductManager
	other.Became, other.BecameReason, other.BecameBy, other.BecameRole, other.BecameAt = "docs/rules.md", "written in", "the operator", domain.RoleArchitect, &later

	merged := Merge(base, other)
	if got := strings.Join(merged.Dispositions(), ","); got != "resolved,withdrawn,became" {
		t.Fatalf("Dispositions() = %q, want all three", got)
	}
	if !SameDispositions(merged, other) || SameDispositions(merged, base) {
		t.Fatal("SameDispositions() did not tell the merged copy from the base and from the other")
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if base.ResolvedAt != nil {
		t.Fatal("Merge() changed its base argument")
	}

	// What the base already says is kept, whatever the other holds.
	base.Resolution, base.ResolvedAt = "settled here", &earlier
	kept := Merge(base, other)
	if kept.Resolution != "settled here" || !kept.ResolvedAt.Equal(earlier) {
		t.Fatalf("Merge() = %q at %v, want the base's own resolution kept", kept.Resolution, kept.ResolvedAt)
	}
	if kept.Withdrawal != "no longer meant" {
		t.Fatalf("Merge() withdrawal = %q, want the one the base lacked copied in", kept.Withdrawal)
	}
}

func TestSameDispositionsComparesTimesByInstant(t *testing.T) {
	t.Parallel()

	at := recordedAt.Add(time.Hour)
	zoned := at.In(time.FixedZone("elsewhere", 3600))
	a, b := operational(), operational()
	a.Resolution, a.ResolvedAt = "done", &at
	b.Resolution, b.ResolvedAt = "done", &zoned
	if !SameDispositions(a, b) {
		t.Fatal("SameDispositions() told one instant in two zones apart")
	}
	b.Resolution = "done differently"
	if SameDispositions(a, b) {
		t.Fatal("SameDispositions() ignored a different resolution")
	}
	if len(operational().Dispositions()) != 0 {
		t.Fatal("Dispositions() named a group an open directive lacks")
	}
}

func TestAFactoryIsOptionalAndOneLine(t *testing.T) {
	t.Parallel()

	d := operational()
	d.Factory = "the build machine"
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	d.Factory = "two\nlines"
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "factory cannot span lines") {
		t.Fatalf("Validate() error = %v, want a refusal of a factory over two lines", err)
	}
}
