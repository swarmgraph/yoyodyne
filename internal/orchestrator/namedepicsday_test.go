package orchestrator

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
)

// epicDay is a watching session polled every ten minutes, over ready work outside
// the one open epic, a member, a program manager due every hour and a query of
// outside messages that is always due.
type epicDay struct {
	passEpics
	harness *scheduleHarness
	role    *wokenRole
	intake  *fakeIntake
	trigger Trigger
}

func newEpicDay(t *testing.T, member string) *epicDay {
	t.Helper()
	d := &epicDay{passEpics: passEpics{t: t, root: t.TempDir()}, role: &wokenRole{}}
	var err error
	if d.operator, err = epicscope.ForOperator(d.root, "yoyodyne"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.operator.Start(epicscope.Naming{Epic: namedEpic, From: "terminal", Members: []string{blockedMember}}, baseTime); err != nil {
		t.Fatal(err)
	}
	items := readyItems("yoyodyne-outside-a", "yoyodyne-outside-b")
	items = append(items, beads.WorkItem{ID: blockedMember, Title: "Member", Status: member, Priority: 3})
	d.harness = newScheduleHarness(items...)
	d.harness.poll = 10 * time.Minute
	d.intake = &fakeIntake{due: []string{"bugs"}, started: map[string]int{}, release: make(chan struct{})}
	close(d.intake.release)
	store := sweepStore(t)
	d.trigger = Trigger{Claims: store, Reports: store, Roles: d.role, Clock: harnessClock(d.harness.clock), Instances: instance(time.Hour)}
	d.wire(&d.trigger)
	return d
}

// watch polls until done, and returns the session. Every third hour another
// unrelated item becomes ready.
func (d *epicDay) watch(each func(poll int) bool) Schedule {
	d.t.Helper()
	polls := 0
	d.harness.onSleep = func(h *scheduleHarness, _ int) bool {
		polls++
		if polls%18 == 0 {
			h.mu.Lock()
			h.Items = append(h.Items, beads.WorkItem{ID: fmt.Sprintf("yoyodyne-later-%d", polls), Title: "Later", Status: "open", Priority: 0})
			h.mu.Unlock()
		}
		return each(polls)
	}
	schedule, err := Scheduler{Open: func(ctx context.Context) (Pull, error) {
		pull, err := d.harness.open(ctx)
		pull.MayStart, pull.Recurring, pull.IntakeQueries = d.ask, d.trigger, d.intake
		return pull, err
	}, Watching: true, Sleep: d.harness.sleep, besideTurns: d.harness.beside, Now: d.harness.clock}.Schedule(context.Background())
	if err != nil {
		d.t.Fatal(err)
	}
	return schedule
}

func startedItems(schedule Schedule) []string {
	var ids []string
	for _, started := range schedule.Started {
		ids = append(ids, started.WorkItemID)
	}
	return ids
}

// Over a day of pulls nothing outside the one open epic starts, also while every
// member is blocked, and no program manager is woken and no message is read.
func TestADayOfPullsStartsNothingOutsideTheOneOpenEpic(t *testing.T) {
	t.Parallel()
	for status, want := range map[string][]string{"open": {blockedMember}, "blocked": nil} {
		t.Run("member "+status, func(t *testing.T) {
			t.Parallel()
			d := newEpicDay(t, status)
			began := d.harness.clock()
			schedule := d.watch(func(int) bool { return d.harness.clock().Sub(began) < 24*time.Hour })
			if over := d.harness.clock().Sub(began); over < 24*time.Hour || d.harness.pulls < 140 {
				t.Fatalf("%d pulls over %s, want a day of them", d.harness.pulls, over)
			}
			if got := startedItems(schedule); !slices.Equal(got, want) {
				t.Fatalf("started %v, want %v", got, want)
			}
			if len(d.role.messages) != 0 || len(d.intake.started) != 0 {
				t.Fatalf("woken %d times and read %v, want neither", len(d.role.messages), d.intake.started)
			}
			for _, item := range d.harness.Items {
				if item.ID != blockedMember && item.Status != "open" {
					t.Errorf("%s is %s, want it left as it was", item.ID, item.Status)
				}
			}
		})
	}
}

// Ending the last epic starts nothing, its members' work included; turning epic
// mode off starts the work that was held out.
func TestEndingTheLastEpicStartsNothingAndTurningEpicModeOffStartsWork(t *testing.T) {
	t.Parallel()
	d := newEpicDay(t, "open")
	d.end()
	schedule := d.watch(func(poll int) bool { return poll < 30 })
	if got := startedItems(schedule); len(got) != 0 || len(d.role.messages) != 0 || len(d.intake.started) != 0 {
		t.Fatalf("with the last epic ended, started %v, woke %d, read %v", got, len(d.role.messages), d.intake.started)
	}
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect} {
		if d.would(maystart.Pass(maystart.RecurringPass, role)).MayStart() {
			t.Errorf("a pass of the %s may start with the last epic ended", role)
		}
	}
	d.off()
	schedule = d.watch(func(poll int) bool { return poll < 30 })
	got := strings.Join(startedItems(schedule), " ")
	for _, want := range []string{"yoyodyne-outside-a", "yoyodyne-outside-b", blockedMember} {
		if !strings.Contains(got, want) {
			t.Errorf("with epic mode off, started %q, want %s among them", got, want)
		}
	}
	if len(d.role.messages) == 0 || d.intake.started["bugs"] == 0 {
		t.Errorf("with epic mode off, woke %d and read %v, want both", len(d.role.messages), d.intake.started)
	}
}
