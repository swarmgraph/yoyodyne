package config

import (
	"strings"
	"testing"
)

func TestCouldNotRunBeforeStatusLoadsWithDefaultAndOverrides(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		key  string
		want int
	}{
		"default":  {"", 3},
		"explicit": {"execution:\n  could_not_run_before_status: 5\n", 5},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := loadProject(t, projectWithSweep+test.key, nil).Config
			if got := cfg.Execution.CouldNotRunBeforeStatus; got != test.want {
				t.Fatalf("could_not_run_before_status = %d, want %d", got, test.want)
			}
		})
	}
	_, err := loadProjectError(t, projectWithSweep+"execution:\n  could_not_run_before_status: -1\n", nil)
	if err == nil || !strings.Contains(err.Error(), "execution.could_not_run_before_status cannot be negative") {
		t.Fatalf("negative count loaded: %v", err)
	}
}
