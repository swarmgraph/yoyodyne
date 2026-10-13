package runstate

import (
	"strings"
	"testing"
	"time"
)

// A stage waiting for a place says so rather than naming a check it is not
// running, and what it spends leaves every wait out, the one still going
// included.
func TestAStageWaitingForAPlaceSaysSoAndLeavesTheWaitOutOfItsSpend(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	stage := CheckStage{StartedAt: started, BoundSeconds: 1800, Command: "make race", PermitWait: &HeavyCheckWait{WaitedSeconds: 120}}
	stage.PermitWait.Begin("make race", started.Add(5*time.Minute))
	now := started.Add(9 * time.Minute)
	if spent := stage.SpentBy(now); spent != 3*time.Minute {
		t.Fatalf("spent = %s, want 3m: nine minutes less two waited before and four waiting now", spent)
	}
	said := stage.Describe(now)
	if !strings.Contains(said, "waiting 4m for a free place to run make race") || strings.Contains(said, ", on make race") {
		t.Fatalf("said %q, want it waiting rather than on the check", said)
	}
	if err := stage.Validate(); err != nil {
		t.Fatal(err)
	}
	stage.PermitWait.Granted(4 * time.Minute)
	if stage.PermitWait.Waiting() || stage.PermitWait.WaitedSeconds != 360 {
		t.Fatalf("after the grant = %#v", stage.PermitWait)
	}
	if said := stage.Describe(now); !strings.Contains(said, ", on make race") {
		t.Fatalf("said %q, want it on the check once granted", said)
	}
	stage.PermitWait.Check = "make race"
	if err := stage.Validate(); err == nil {
		t.Fatal("a wait naming its check and no time was accepted")
	}
}
