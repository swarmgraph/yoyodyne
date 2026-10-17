package runstate

import (
	"testing"
	"time"
)

// How long a restart waits for a running check is its launch plus its
// configured limit, or the shorter limit it was given; a stage with no check
// launched, waiting for a place, or ended has none running.
func TestCheckRunningUntilIsTheChecksConfiguredLimitAtMost(t *testing.T) {
	t.Parallel()

	began := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	launched := began.Add(10 * time.Minute)
	stage := CheckStage{StartedAt: began, BoundSeconds: 36000, Command: "make race", CheckConfiguredSeconds: 2700, CheckLimitSeconds: 2700, CheckStartedAt: &launched}
	if until, running := stage.CheckRunningUntil(); !running || !until.Equal(launched.Add(45*time.Minute)) {
		t.Fatalf("CheckRunningUntil() = %s, %t; want 45 minutes after the launch", until, running)
	}
	scaled := stage
	scaled.CheckLimitSeconds = 27000
	if until, running := scaled.CheckRunningUntil(); !running || !until.Equal(launched.Add(45*time.Minute)) {
		t.Fatalf("CheckRunningUntil() under a load-scaled limit = %s, %t; want the configured 45 minutes", until, running)
	}
	shorter := stage
	shorter.CheckLimitSeconds = 600
	if until, running := shorter.CheckRunningUntil(); !running || !until.Equal(launched.Add(10*time.Minute)) {
		t.Fatalf("CheckRunningUntil() under a shorter limit = %s, %t; want the 10 minutes it was given", until, running)
	}
	unlaunched := stage
	unlaunched.CheckStartedAt = nil
	waitingForPlace := stage
	since := launched
	waitingForPlace.PermitWait = &HeavyCheckWait{Check: "make race", Since: &since}
	ended := stage
	finished := launched.Add(time.Minute)
	ended.FinishedAt = &finished
	for name, stage := range map[string]CheckStage{"not launched": unlaunched, "waiting for a place": waitingForPlace, "ended": ended} {
		if _, running := stage.CheckRunningUntil(); running {
			t.Errorf("%s: CheckRunningUntil() reports a check running", name)
		}
	}
}
