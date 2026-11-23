package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// The night of 2026-09-29, replayed in the records: the last run started at
// 21:51, the development manager's sweep last succeeded an hour before, and
// every sweep after that failed on the tracker listing.
func stalledNight() (time.Time, []runstate.State, []runstate.Sweep) {
	pulled := time.Date(2026, 9, 30, 4, 51, 0, 0, time.UTC)
	runs := []runstate.State{{RunID: "run-last", StartedAt: pulled}}
	passes := []runstate.Sweep{{
		Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager,
		StartedAt: pulled.Add(-time.Hour), Turns: 1, Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "nothing stopped"},
	}}
	for hour := 1; hour <= 12; hour++ {
		passes = append(passes, runstate.Sweep{
			Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager,
			StartedAt: pulled.Add(time.Duration(hour) * time.Hour),
			Problem:   "list work items: bd list timed out after 30s",
		})
	}
	passes = append(passes, runstate.Sweep{
		Task: "lead-product-manager-pass", Role: domain.RoleProductManager,
		StartedAt: pulled.Add(3 * time.Hour), NotStarted: runstate.PreTurnConversationUnopened,
	})
	return pulled, runs, passes
}

func TestAFactoryThatHasDoneNothingPastItsLimitIsStalledAndSaysWhatEachPassFailedOn(t *testing.T) {
	t.Parallel()
	pulled, runs, passes := stalledNight()
	now := pulled.Add(13 * time.Hour)

	stall, stalled := FactoryStallOf(runs, passes, false, 2*time.Hour, now)
	if !stalled {
		t.Fatalf("FactoryStallOf() = not stalled, want a stall thirteen hours past the last pull")
	}
	if !stall.Since.Equal(pulled) || !stall.LastPull.Equal(pulled) || !stall.LastPass.Equal(pulled.Add(-time.Hour)) {
		t.Fatalf("stall = %+v, want it measured from the last pull, with the earlier successful pass named", stall)
	}
	if stall.For() != 13*time.Hour {
		t.Fatalf("For() = %s, want 13h", stall.For())
	}
	if len(stall.Failures) != 2 {
		t.Fatalf("Failures = %+v, want one per task", stall.Failures)
	}
	sweepFailure := stall.Failures[0]
	if sweepFailure.Task != "development-manager-sweep" || sweepFailure.Attempts != 12 || !sweepFailure.At.Equal(pulled.Add(12*time.Hour)) {
		t.Fatalf("sweep failure = %+v, want the twelve failed sweeps with the latest dated", sweepFailure)
	}
	says := stall.Says()
	for _, want := range []string{"13h0m0s", "2h0m0s limit", "bd list timed out", "development-manager-sweep", "lead-product-manager-pass", "conversation"} {
		if !strings.Contains(says, want) {
			t.Errorf("Says() = %q, want it to carry %q", says, want)
		}
	}
	entry := factoryStallAttention(stall)
	if entry.Mover != MoverHarness || entry.Since() != pulled {
		t.Fatalf("attention = %+v, want the harness's move dated from the last pull", entry)
	}
}

func TestAFactoryInsideItsLimitIsNotStalled(t *testing.T) {
	t.Parallel()
	pulled, runs, passes := stalledNight()
	if stall, stalled := FactoryStallOf(runs, passes, false, 2*time.Hour, pulled.Add(2*time.Hour-time.Minute)); stalled {
		t.Fatalf("FactoryStallOf() = %+v, want nothing inside the limit", stall)
	}
	// A limit configured longer than the stall has run is the same answer.
	if stall, stalled := FactoryStallOf(runs, passes, false, 24*time.Hour, pulled.Add(13*time.Hour)); stalled {
		t.Fatalf("FactoryStallOf() = %+v, want nothing inside a day's limit", stall)
	}
}

func TestAPullOrASuccessfulPassEndsTheStall(t *testing.T) {
	t.Parallel()
	pulled, runs, passes := stalledNight()
	now := pulled.Add(13 * time.Hour)

	recovered := append(append([]runstate.Sweep{}, passes...), runstate.Sweep{
		Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager,
		StartedAt: now.Add(-10 * time.Minute), Turns: 2, Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "the tracker answers again"},
	})
	if stall, stalled := FactoryStallOf(runs, recovered, false, 2*time.Hour, now); stalled {
		t.Fatalf("FactoryStallOf() = %+v, want a successful pass to end the stall", stall)
	}
	pulledAgain := append(append([]runstate.State{}, runs...), runstate.State{RunID: "run-next", StartedAt: now.Add(-time.Minute)})
	if stall, stalled := FactoryStallOf(pulledAgain, passes, false, 2*time.Hour, now); stalled {
		t.Fatalf("FactoryStallOf() = %+v, want a pull to end the stall", stall)
	}
}

// Neither a pause nor a product whose recurring tasks are simply off is a
// stall: the first is a stop somebody placed, and the second is attempting
// nothing that could fail.
func TestAPauseAndAFactoryAttemptingNothingAreNotStalls(t *testing.T) {
	t.Parallel()
	pulled, runs, passes := stalledNight()
	if _, stalled := FactoryStallOf(runs, passes, true, 2*time.Hour, pulled.Add(13*time.Hour)); stalled {
		t.Fatalf("FactoryStallOf() under the operator's pause = stalled, want a stop rather than a stall")
	}
	quiet := []runstate.Sweep{passes[0]}
	if _, stalled := FactoryStallOf(runs, quiet, false, 2*time.Hour, pulled.Add(13*time.Hour)); stalled {
		t.Fatalf("FactoryStallOf() with no pass attempted since = stalled, want a rest")
	}
}

// The harness's own passes wake no role, so a resting product whose log holds
// only them is not stalled, whether they worked or failed; beside a role's
// failing passes they are neither counted nor hide the stall.
func TestTheHarnesssOwnPassesAreNeitherAStallNorHideOne(t *testing.T) {
	t.Parallel()
	pulled, runs, passes := stalledNight()
	now := pulled.Add(13 * time.Hour)
	var own []runstate.Sweep
	for hour := 1; hour <= 12; hour++ {
		at := pulled.Add(time.Duration(hour) * time.Hour)
		own = append(own, runstate.Sweep{
			Task: "build-cache", StartedAt: at, EndedAt: at.Add(time.Second),
			Steps:  []runstate.SweepStep{{Name: "build-cache", Outcome: runstate.StepRan, Detail: "within its limit"}},
			Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "1 step(s) ran, 0 skipped, 0 failed"},
		}, runstate.Sweep{
			Task: "maintenance", StartedAt: at, EndedAt: at.Add(time.Second), Problem: "reconcile: refused",
			Steps: []runstate.SweepStep{{Name: "reconcile", Outcome: runstate.StepFailed, Detail: "refused"}},
		})
	}
	for name, given := range map[string][]runstate.State{"after a pull": runs, "where nothing was ever pulled": nil} {
		if stall, stalled := FactoryStallOf(given, own, false, 2*time.Hour, now); stalled {
			t.Fatalf("FactoryStallOf() %s = %+v, want a rest", name, stall)
		}
	}
	stall, stalled := FactoryStallOf(runs, append(own, passes...), false, 2*time.Hour, now)
	if !stalled || !stall.Since.Equal(pulled) || len(stall.Failures) != 2 {
		t.Fatalf("FactoryStallOf() beside failing sweeps = %+v, %t, want the stall with only the two roles' passes", stall, stalled)
	}
}

// A pass whose turn failed, one missed, and one that answered without an
// account are none of them a success.
func TestOnlyAPassThatAnsweredWithAnAccountSucceeded(t *testing.T) {
	t.Parallel()
	answered := runstate.Sweep{Turns: 1, Result: &sweep.Result{Status: sweep.StatusComplete}}
	if !PassSucceeded(answered) {
		t.Fatalf("PassSucceeded(%+v) = false", answered)
	}
	for name, pass := range map[string]runstate.Sweep{
		"no turn":    {Result: &sweep.Result{}},
		"no account": {Turns: 1, Problem: "the reply carried no account"},
		"failed":     {Turns: 1, Result: &sweep.Result{}, Failed: true},
		"missed":     {Turns: 1, Result: &sweep.Result{}, Missed: &runstate.MissedPass{}},
	} {
		if PassSucceeded(pass) {
			t.Errorf("%s: PassSucceeded = true", name)
		}
	}
}

// The stall is on the attention line `yoyo status` prints, as the harness's
// move, for as long as it stands.
func TestAStandingFactoryStallIsOnTheAttentionLine(t *testing.T) {
	t.Parallel()
	pulled, runs, passes := stalledNight()
	sources := quietSources()
	sources.Now = func() time.Time { return pulled.Add(13 * time.Hour) }
	sources.Runs = fakeRuns{recorded: runs, prices: map[string]runstate.ItemPrice{}}
	sources.Passes = fakePasses{passes: passes}
	sources.FactoryStallAfter = 2 * time.Hour

	standing := ReadStanding(context.Background(), sources)
	var found *Attention
	for index, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionFactoryStall {
			found = &standing.NeedsHuman[index]
		}
	}
	if found == nil {
		t.Fatalf("NeedsHuman = %+v, want the factory stall on it", standing.NeedsHuman)
	}
	if found.Mover != MoverHarness || !strings.Contains(found.What(), "no recurring pass has succeeded for 13h0m0s") {
		t.Fatalf("entry = %s / %s, want the harness's move with how long", found.What(), found.Whose())
	}
	if !strings.Contains(standing.Render(), "no work has been pulled") {
		t.Fatalf("Render() = %q, want the stall printed", standing.Render())
	}

	sources.FactoryStallAfter = 24 * time.Hour
	for _, entry := range ReadStanding(context.Background(), sources).NeedsHuman {
		if entry.Kind == AttentionFactoryStall {
			t.Fatalf("NeedsHuman carries %+v inside a day's limit", entry)
		}
	}
}
