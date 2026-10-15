package testharness

import (
	"github.com/mason-bryant/yoyodyne/internal/config"
)

// DocketConfig is the configuration the harness's docket is built against here:
// the triage thresholds every other docket in these tests uses.
func DocketConfig() config.Config {
	return config.Config{Execution: config.Execution{IntegrationRetriesBeforeReconciliation: 1}, Triage: DocketedTriage}
}
