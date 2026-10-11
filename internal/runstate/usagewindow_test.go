package runstate

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func usageReading(account, window string, utilization float64, at time.Time) backend.UsageWindowReading {
	return backend.UsageWindowReading{Provider: domain.BackendClaudeCode, Account: account, Window: window, Utilization: &utilization, At: at}
}

// A newer reading of an account's window replaces the older; an older one
// arriving late replaces nothing; another account's same window is its own;
// and what was recorded is what a store opened afresh — a restart — reads.
func TestAUsageWindowReadingReplacesTheOlderOneAndSurvivesARestart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := NewUsageWindowStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if listed, err := store.List(); err != nil || len(listed) != 0 {
		t.Fatalf("an empty product lists %+v, %v", listed, err)
	}
	moment := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	if err := store.RecordUsageWindows([]backend.UsageWindowReading{
		usageReading("default", "seven_day", 0.30, moment),
		usageReading("default", "five_hour", 0.10, moment),
		usageReading("second", "seven_day", 0.70, moment),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUsageWindows([]backend.UsageWindowReading{usageReading("default", "seven_day", 0.35, moment.Add(time.Minute))}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUsageWindows([]backend.UsageWindowReading{usageReading("default", "seven_day", 0.20, moment.Add(-time.Minute))}); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewUsageWindowStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := restarted.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Fatalf("listed %d readings, want one per account and window: %+v", len(listed), listed)
	}
	byKey := map[string]backend.UsageWindowReading{}
	for _, reading := range listed {
		byKey[reading.Account+"/"+reading.Window] = reading
	}
	if week := byKey["default/seven_day"]; *week.Utilization != 0.35 || !week.At.Equal(moment.Add(time.Minute)) {
		t.Fatalf("default's week = %+v, want the newest reading", week)
	}
	if other := byKey["second/seven_day"]; *other.Utilization != 0.70 {
		t.Fatalf("second's week = %+v, want its own reading untouched", other)
	}
	if err := store.RecordUsageWindows([]backend.UsageWindowReading{{Account: "default", At: moment}}); err == nil {
		t.Fatal("a reading that names no window was recorded")
	}
}
