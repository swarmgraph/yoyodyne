package config

import (
	"strings"
	"testing"
)

func TestMissingRecurringReportBoundLoadsWithDefaultAndOverrides(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		key  string
		want int
	}{
		"default":           {"", 3},
		"explicit":          {"execution:\n  missing_reports_before_fresh_conversation: 2\n", 2},
		"zero uses default": {"execution:\n  missing_reports_before_fresh_conversation: 0\n", 3},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := loadProject(t, projectWithSweep+test.key, nil).Config
			if got := cfg.Execution.MissingReportLimit(); got != test.want {
				t.Fatalf("bound = %d, want %d", got, test.want)
			}
		})
	}
	_, err := loadProjectError(t, projectWithSweep+"execution:\n  missing_reports_before_fresh_conversation: -1\n", nil)
	if err == nil || !strings.Contains(err.Error(), "execution.missing_reports_before_fresh_conversation cannot be negative") {
		t.Fatalf("negative bound loaded: %v", err)
	}
}
