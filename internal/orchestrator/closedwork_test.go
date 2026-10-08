package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// listedClosedWork is the closed work a test hands the audit, and the moment
// each listing was asked to start after.
type listedClosedWork struct {
	reading ClosedWork
	err     error
	asked   []time.Time
}

func (l *listedClosedWork) Closed(_ context.Context, since time.Time, limit int) (ClosedWork, error) {
	l.asked = append(l.asked, since)
	if limit != closedWorkLimit {
		return ClosedWork{}, errors.New("asked for the wrong number of items")
	}
	return l.reading, l.err
}

func productManagerSweep() map[string]config.RecurringTask {
	return map[string]config.RecurringTask{"product-manager-sweep": {
		Role: domain.RoleProductManager, Every: config.Duration(time.Hour), Enabled: true, MaxTurns: 2, Prompt: "audit what closed",
	}}
}

// The next pass is handed what closed after the last listing reached, and a
// listing that stopped at its bound records where it stopped rather than the
// moment it was read, so what did not fit is handed to the pass after.
func TestTheClosedWorkListingResumesWhereTheLastCompletedPassReached(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	reached := recurringNow.Add(-5 * time.Hour)
	if err := store.Append(runstate.Sweep{SchemaVersion: 1, ProductID: "example", Task: "product-manager-sweep", Role: domain.RoleProductManager,
		StartedAt: recurringNow.Add(-3 * time.Hour), EndedAt: recurringNow.Add(-3 * time.Hour), Turns: 1,
		Result: complete("looked"), ClosedThrough: reached}); err != nil {
		t.Fatal(err)
	}
	// A failed pass after it does not move where the listing starts.
	if err := store.Append(runstate.Sweep{SchemaVersion: 1, ProductID: "example", Task: "product-manager-sweep", Role: domain.RoleProductManager,
		StartedAt: recurringNow.Add(-2 * time.Hour), EndedAt: recurringNow.Add(-2 * time.Hour), Failed: true, Turns: 1,
		Problem: "the provider went away", ClosedThrough: recurringNow.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	first, last := recurringNow.Add(-4*time.Hour), recurringNow.Add(-90*time.Minute)
	listing := &listedClosedWork{reading: ClosedWork{More: 4, Items: []ClosedItem{
		{ID: "example-1", Title: "First", ClosedAt: first, Landed: "the reviewer's account of the first"},
		{ID: "example-2", Title: "Second", ClosedAt: last},
	}}}
	role := &wokenRole{answers: []scriptedTurn{{result: complete("audited")}}}
	trigger := Trigger{Tasks: productManagerSweep(), Claims: store, Reports: store, Roles: role, ClosedWork: listing, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(listing.asked) != 1 || !listing.asked[0].Equal(reached) {
		t.Fatalf("asked = %v, want the listing to start where the last completed pass reached, %s", listing.asked, reached)
	}
	message := role.messages[0]
	for _, want := range []string{"## Work closed since your last pass", "example-1 (First)", "the reviewer's account of the first",
		"example-2 (Second)", "its record gives no account of what landed", "4 more closed after these and are handed to your next pass", `"audits"`} {
		if !strings.Contains(message, want) {
			t.Errorf("message does not carry %q:\n%s", want, message)
		}
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	pass := recorded[len(recorded)-1]
	if want := last.Add(-time.Nanosecond); !pass.ClosedThrough.Equal(want) {
		t.Errorf("closed through = %s, want just before the last item listed, %s", pass.ClosedThrough, want)
	}
}

// A listing that could not be read audits nothing, says so, and leaves the next
// pass to be handed the same range.
func TestAnUnreadableClosedWorkListingIsSaidAndHandedToTheNextPass(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	listing := &listedClosedWork{err: errors.New("the tracker did not answer")}
	role := &wokenRole{answers: []scriptedTurn{{result: complete("nothing to audit")}}}
	trigger := Trigger{Tasks: productManagerSweep(), Claims: store, Reports: store, Roles: role, ClosedWork: listing, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(role.messages[0], "could not be listed") || !strings.Contains(role.messages[0], "the tracker did not answer") {
		t.Errorf("message = %q, want the unread listing said", role.messages[0])
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %d, %v", len(recorded), err)
	}
	if want := recurringNow.Add(-closedWorkLookback); !recorded[0].ClosedThrough.Equal(want) {
		t.Errorf("closed through = %s, want where the listing started, %s", recorded[0].ClosedThrough, want)
	}
	if !strings.Contains(recorded[0].Problem, "the tracker did not answer") {
		t.Errorf("problem = %q, want the unread listing on the record", recorded[0].Problem)
	}
}

// What a program manager instance whose lane is a standing goal audited since
// the sweep's last pass is handed to the sweep, so it does not audit the same
// item against the same goal again. Only the Lead Product Manager's own tasks
// are handed closed work; a development manager's sweep is not.
func TestTheSweepIsHandedTheProgramManagersAuditsAndOtherRolesAreHandedNoClosedWork(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	if err := store.Append(runstate.Sweep{SchemaVersion: 1, ProductID: "example", Task: "writing-pm", Role: domain.RoleProgramManager, Agent: "writing-pm",
		StartedAt: recurringNow.Add(-time.Hour), EndedAt: recurringNow.Add(-time.Hour), Turns: 1,
		Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "audited my lane", Audits: []sweep.Audit{
			{Item: "example-7", Goals: []string{"the plain-language goal"}, Finding: sweep.AuditBroken, Detail: "a coined word on the dashboard", Correction: "example-9"},
			{Item: "example-8", Goals: []string{"the plain-language goal"}, Finding: sweep.AuditMet},
		}}}); err != nil {
		t.Fatal(err)
	}
	listing := &listedClosedWork{reading: ClosedWork{Items: []ClosedItem{{ID: "example-7", Title: "Dashboard words", ClosedAt: recurringNow.Add(-2 * time.Hour)}}}}
	role := &wokenRole{answers: []scriptedTurn{{result: complete("audited")}}}
	trigger := Trigger{Tasks: productManagerSweep(), Claims: store, Reports: store, Roles: role, ClosedWork: listing, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(role.messages) != 1 || len(listing.asked) != 1 {
		t.Fatalf("messages = %d, listings = %d; want one pass and one listing", len(role.messages), len(listing.asked))
	}
	for _, want := range []string{"## Audits the program managers already made",
		"example-7, audited by writing-pm against the plain-language goal: broken — a coined word on the dashboard; corrected by example-9",
		"example-8, audited by writing-pm against the plain-language goal: met"} {
		if !strings.Contains(role.messages[0], want) {
			t.Errorf("the sweep's message does not carry %q:\n%s", want, role.messages[0])
		}
	}

	other := &wokenRole{answers: []scriptedTurn{{result: complete("swept")}}}
	trigger = Trigger{Tasks: hourlyTask("sweep"), Claims: sweepStore(t), Reports: store, Roles: other, ClosedWork: listing, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(other.messages) != 1 || len(listing.asked) != 1 || strings.Contains(other.messages[0], "Work closed since your last pass") {
		t.Errorf("the development manager's sweep was handed closed work: %d listings, %v", len(listing.asked), other.messages)
	}
}
