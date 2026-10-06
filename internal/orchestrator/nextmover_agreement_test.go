package orchestrator

// Whether a carry-out is outstanding about a stoppage is one question, and two
// things answer it: the development manager's docket, which names a next mover
// on every entry, and the read model every operator surface projects, which puts
// the same item in one of two waits on the status head, the attention line and
// the alarm. They were two rules. The docket asked the item's totals — any
// re-run not yet claimed, or any grant not yet spent — and the read model asked
// the decision standing about this run and consulted the grant only where that
// decision was a repair. An item with an unspent repair grant whose latest
// stoppage was then decided wait, re-scope or escalate read as the harness's on
// one and as the development manager's on the other: one piece of work with two
// next movers, which is a disagreement only the operator can adjudicate.
//
// The rule is triage.AwaitingCarryOut now, and both read it. This is what holds
// them together anyway: one durable ledger, both readers, and the same answer
// about the same stoppage. It lives beside the docket rather than beside the read
// model because whoever changes what an entry says about its next mover is who
// has to know the surfaces move with it.

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// nextMoverStoppages is the one stopped run both readers are asked about. The
// read model wants the escalations beside the runs; there are none, so what
// either says about the item is attributable to the triage record and to nothing
// else.
type nextMoverStoppages struct{ states []runstate.State }

func (s nextMoverStoppages) Recorded() ([]runstate.State, error) { return s.states, nil }

func (nextMoverStoppages) Escalated() ([]runstate.Escalation, error) { return nil, nil }

func TestAnItemIsNeverGivenTwoNextMoversAcrossSurfaces(t *testing.T) {
	t.Parallel()

	// A grant recorded and unspent on every case, which is the state that made the
	// two rules disagree: the read model consults it only under a repair, and the
	// docket consulted it whatever had been decided since.
	const grantedRounds, roundsSpent = 3, 2

	for _, test := range []struct {
		name string
		// decided is the decision standing about this stoppage, or empty for a
		// stoppage nobody has decided about.
		decided string
		// carryOut is what both readers must say: the harness has something left to
		// do about this stoppage.
		carryOut bool
	}{
		{name: "nobody has decided anything", decided: "", carryOut: false},
		{name: "a repair granted and not handed back", decided: runstate.TriageDecisionRepair, carryOut: true},
		{name: "a re-run nothing has claimed", decided: runstate.TriageDecisionRerun, carryOut: true},
		// The three that buy no attempt. Each leaves the harness nothing to do, and
		// each is what stood recorded over an unspent grant in the case this test
		// was written for.
		{name: "a wait decided over an unspent grant", decided: runstate.TriageDecisionWait, carryOut: false},
		{name: "a re-scope decided over an unspent grant", decided: runstate.TriageDecisionRescope, carryOut: false},
		{name: "an escalation decided over an unspent grant", decided: runstate.TriageDecisionEscalate, carryOut: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stopped := stoppedState()
			ledger := runstate.TriageCounters{
				RepairGrants:    1,
				GrantedRounds:   grantedRounds,
				CommittedRounds: grantedRounds,
				ReviewRounds:    roundsSpent,
			}
			if test.decided != "" {
				ledger.Decisions = []runstate.TriageDecision{triageDecided(test.decided, stopped.RunID)}
			}
			recorded := &recordedDecisions{
				counters: map[string]runstate.TriageCounters{docketedItem: ledger},
			}

			built, err := docketerDeciding([]runstate.State{stopped}, &memoryDocket{}, recorded, recorded).Build()
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if len(built.Entries) != 1 {
				t.Fatalf("built = %#v, want the one stoppage docketed", built)
			}
			entry := built.Entries[0]

			// The read model is asked about the same run and the same record. Nothing
			// is wired to look for the change, so the run is held on what its own
			// record says survived — which is beside the point here: what is being
			// compared is which of the two waits the hold is in.
			held, err := readmodel.HeldForAPerson(context.Background(),
				nextMoverStoppages{states: []runstate.State{stopped}}, recorded, nil)
			if err != nil {
				t.Fatalf("HeldForAPerson() error = %v", err)
			}
			reason, holding := held.Reason(docketedItem)
			if !holding {
				t.Fatalf("held = %+v, want the stopped run to hold its item", held)
			}

			docket, surfaces := entry.Counters.AwaitingCarryOut(), held.Decided(docketedItem)
			if docket != surfaces {
				t.Fatalf("the docket says a carry-out is outstanding = %t and the surfaces say %t about %s; "+
					"one stoppage with two answers is one item with two next movers\ndocket:\n%s\nsurfaces: %s",
					docket, surfaces, docketedItem, entry.Render(), reason)
			}
			if docket != test.carryOut {
				t.Fatalf("a carry-out is outstanding = %t, want %t with %q decided about the stoppage",
					docket, test.carryOut, test.decided)
			}

			// And the words each surface says follow the one answer, so the agreement
			// is readable rather than only arithmetic.
			mover, clause := "Next mover: you", "the development manager decides what happens to it"
			if test.carryOut {
				mover, clause = "Next mover: the harness", "the development manager has already decided what happens to it"
			}
			if rendered := entry.Render(); !strings.Contains(rendered, mover) {
				t.Fatalf("the entry does not name %q as the next mover:\n%s", mover, rendered)
			}
			if !strings.Contains(reason, clause) {
				t.Fatalf("hold = %q, want it to close on %q", reason, clause)
			}
		})
	}
}

// The stoppage that is in neither of the two waits, and which both readers
// therefore have to answer the same way out of band: an approved change the
// environment stopped short of its promotion. Nobody decided anything about it
// and nobody has to — the reviewer already decided — so the docket names the
// harness and the verb that resumes it, and the hold the pull reads has to say
// the same rather than sending the operator to a development manager the docket
// tells she owes nothing here.
func TestAnApprovedChangeTheEnvironmentStoppedNamesTheHarnessOnBothSurfaces(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	// What the pipeline records on such a run: the approval standing with its
	// reviewer session, nothing promoted, and the environmental cause of the stop.
	// The blocker goes, because a run that fails inside its own process hands
	// nobody one.
	stopped.Blocker = ""
	stopped.Failure = "bd show failed with status timed_out and exit code -1: "
	stopped.ReviewDecision = runstate.ReviewApprove
	stopped.ReviewSessionID = "f4c1a0de-review"
	stopped.CheckFailure = nil
	stopped.ReviewFindings, stopped.ReviewFindingDetails = 0, nil
	stopped.IntegrationStop = &runstate.IntegrationStop{
		Cause:      runstate.CauseTransportFailure,
		Detail:     stopped.Failure,
		Phase:      runstate.PhaseReviewing,
		RecordedAt: stopped.UpdatedAt,
	}
	recorded := &recordedDecisions{counters: map[string]runstate.TriageCounters{docketedItem: {}}}

	// Docketed as the run ends rather than by the scan that walks the recorded
	// history, which is where a death that preserved its change reaches the
	// development manager at all: the scan deliberately re-derives only the
	// blockers, so that months of settled failures are not docketed in one build.
	docket := &memoryDocket{}
	docketer := docketerDeciding([]runstate.State{stopped}, docket, recorded, recorded)
	if _, err := docketer.RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	if len(docket.entries) != 1 {
		t.Fatalf("docket = %#v, want the one stoppage docketed", docket.entries)
	}
	rendered := docket.entries[0].Render()
	if !strings.Contains(rendered, "Next mover: the harness") || !strings.Contains(rendered, "yoyo triage resume") {
		t.Fatalf("the docket entry does not name the harness and the resume:\n%s", rendered)
	}

	held, err := readmodel.HeldForAPerson(context.Background(),
		nextMoverStoppages{states: []runstate.State{stopped}}, recorded, nil)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	reason, holding := held.Reason(docketedItem)
	if !holding {
		t.Fatalf("held = %+v, want the stopped run to hold its item", held)
	}
	if !held.Decided(docketedItem) {
		t.Fatalf("hold = %q, want the surfaces to name the harness as the docket does", reason)
	}
	if !strings.Contains(reason, "`yoyo triage resume`") {
		t.Fatalf("hold = %q, want it to close on the verb that resumes the promotion", reason)
	}
}

// The same stop once its branch is gone. The resume restores the checkout from
// the branch and promotes the reviewed commit on it, so with the branch deleted
// it refuses, and neither reader may send anybody to it: the docket names what
// the hold names, from the same look and the same rule, and both say what is
// gone and that a re-run is the way on.
func TestAnApprovedChangeWhoseBranchIsGoneIsNeverSentToTheResume(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		survival gitworktree.Survival
		// decided is a re-run decision standing about the stoppage.
		decided bool
		// held is whether the pull holds the item at all; mover is the next mover
		// the docket names and clause the words the hold closes on.
		held   bool
		mover  string
		clause string
	}{
		{
			name:  "branch and worktree deleted, nothing decided",
			held:  false,
			mover: "Next mover: the next pull",
		},
		{
			name:     "branch deleted with the worktree left, nothing decided",
			survival: gitworktree.Survival{WorktreePresent: true},
			held:     true,
			mover:    "Next mover: you",
			clause:   "the development manager decides what happens to it",
		},
		{
			name:    "branch and worktree deleted with a re-run decided",
			decided: true,
			held:    true,
			mover:   "Next mover: the harness",
			clause:  "the development manager has already decided what happens to it",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stopped := stoppedState()
			stopped.Blocker = ""
			stopped.Failure = "bd show failed with status timed_out and exit code -1: "
			stopped.ReviewDecision = runstate.ReviewApprove
			stopped.ReviewSessionID = "f4c1a0de-review"
			stopped.CheckFailure = nil
			stopped.ReviewFindings, stopped.ReviewFindingDetails = 0, nil
			stopped.IntegrationStop = &runstate.IntegrationStop{
				Cause:      runstate.CauseTransportFailure,
				Detail:     stopped.Failure,
				Phase:      runstate.PhaseReviewing,
				RecordedAt: stopped.UpdatedAt,
			}
			ledger := runstate.TriageCounters{}
			if test.decided {
				ledger = runstate.TriageCounters{
					RepairGrants:    1,
					GrantedRounds:   3,
					CommittedRounds: 3,
					ReviewRounds:    2,
					Decisions:       []runstate.TriageDecision{triageDecided(runstate.TriageDecisionRerun, stopped.RunID)},
				}
			}
			recorded := &recordedDecisions{counters: map[string]runstate.TriageCounters{docketedItem: ledger}}
			// Docketed as the run ends, with the branch and the checkout both there,
			// which is the only way a death reaches the docket at all; then the branch
			// is deleted, and the docket built for the development manager looks again.
			repository := &orchestratortest.Survival{Survival: gitworktree.Survival{BranchExists: true, WorktreePresent: true}}

			docket := &memoryDocket{}
			docketer := docketerDeciding([]runstate.State{stopped}, docket, recorded, recorded)
			docketer.Remains = repository
			if _, err := docketer.RecordStoppedRun(stopped); err != nil {
				t.Fatalf("RecordStoppedRun() error = %v", err)
			}
			if len(docket.entries) != 1 {
				t.Fatalf("docket = %#v, want the one stoppage docketed", docket.entries)
			}
			if before := docket.entries[0].Render(); !strings.Contains(before, "yoyo triage resume") {
				t.Fatalf("with its branch there the entry does not name the resume:\n%s", before)
			}
			repository.Survival = test.survival

			built, err := docketer.Build()
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if len(built.Entries) != 1 {
				t.Fatalf("built = %#v, want the one stoppage on the docket", built)
			}
			rendered := built.Entries[0].Render()
			if strings.Contains(rendered, "triage resume") {
				t.Fatalf("the docket entry sends somebody to a resume that refuses once the branch is gone:\n%s", rendered)
			}
			for _, want := range []string{test.mover, "branch is gone", "a re-run is the way on", "checked and NOT there"} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("the docket entry does not say %q:\n%s", want, rendered)
				}
			}

			held, err := readmodel.HeldForAPerson(context.Background(),
				nextMoverStoppages{states: []runstate.State{stopped}}, recorded, repository)
			if err != nil {
				t.Fatalf("HeldForAPerson() error = %v", err)
			}
			reason, holding := held.Reason(docketedItem)
			if holding != test.held {
				t.Fatalf("held = %t (%q), want %t", holding, reason, test.held)
			}
			if !holding {
				return
			}
			if strings.Contains(reason, "triage resume") {
				t.Fatalf("hold = %q, want no resume named once the branch is gone", reason)
			}
			if !strings.Contains(reason, "a re-run is the way on") || !strings.Contains(reason, test.clause) {
				t.Fatalf("hold = %q, want it to name the re-run and close on %q", reason, test.clause)
			}
			if held.Decided(docketedItem) != strings.HasPrefix(test.mover, "Next mover: the harness") {
				t.Fatalf("the docket names %q and the hold says decided = %t; one stoppage, two next movers\ndocket:\n%s\nhold: %s",
					test.mover, held.Decided(docketedItem), rendered, reason)
			}
		})
	}
}
