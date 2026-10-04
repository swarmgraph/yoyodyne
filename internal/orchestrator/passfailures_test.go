package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestFailedRecurringRolePassesFileTheSameFindingUntilTheySucceed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	passes, _ := runstate.NewSweepStore(root, "example")
	reports, _ := runstate.NewReportStore(root, "example")
	clock := &movingRecurringClock{now: recurringNow}
	role := &wokenRole{answers: []scriptedTurn{{err: errors.New("provider failed")}, {err: errors.New("provider failed")}, {err: errors.New("provider failed")}, {err: errors.New("another failure")}, {result: complete("recovered")}}}
	trigger := Trigger{Tasks: map[string]config.RecurringTask{"architect-pass": {Role: domain.RoleArchitect, Enabled: true, Every: config.Duration(time.Hour), Prompt: "look"}}, Claims: passes, Reports: passes, Roles: role, Clock: clock,
		RecordFailures: func(ctx context.Context) error {
			return passes.RecordPassFailures(ctx, reports, report.Attribution{RepositoryID: "example"}, "factory-watch")
		}}
	for i := 0; i < 5; i++ {
		clock.now = recurringNow.Add(time.Duration(i) * time.Hour)
		if _, err := trigger.Fire(context.Background()); err != nil {
			t.Fatal(err)
		}
		filed, err := reports.List()
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if i >= 2 {
			want = 1
		}
		if len(filed) != want {
			t.Fatalf("pass %d filed %d findings, want %d", i, len(filed), want)
		}
	}
	handlings, err := reports.Handlings()
	if err != nil || len(handlings) != 1 || handlings[0].PassFailureCleared != 4 {
		t.Fatalf("clearing = %+v, %v", handlings, err)
	}
}
