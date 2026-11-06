package cli

import (
	"fmt"
	"io"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// checkTimesOutput is `yoyo status --checks --json`: the reading, or why the
// records could not be opened at all.
type checkTimesOutput struct {
	CheckTimes *readmodel.CheckTimesReading `json:"check_times,omitempty"`
	Error      string                       `json:"error,omitempty"`
}

// reportCheckTimes prints how long the heavy checks took, week by week in the
// operator's local zone (operatorZone), read by readmodel.ReadCheckTimes from
// the run records the recorded mode reads; the delivery page reads the same
// launches into its own periods (readmodel.ReadDelivery).
func reportCheckTimes(configPath string, jsonOutput bool, stdout, stderr io.Writer) int {
	failed := func(err error) int {
		if jsonOutput {
			if code := writeJSON(stdout, stderr, checkTimesOutput{Error: err.Error()}); code != 0 {
				return code
			}
			return 1
		}
		fmt.Fprintf(stderr, "status failed: %v\n", err)
		return 1
	}
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return failed(err)
	}
	store, _, err := recordedRunStore(configPath)
	if err != nil {
		return failed(err)
	}
	reading := readmodel.ReadCheckTimes(readmodel.CheckTimesSources{
		Runs:        store,
		HeavyChecks: resolved.Config.Execution.HeavyChecks,
		Zone:        operatorZone(),
	})
	if jsonOutput {
		return writeJSON(stdout, stderr, checkTimesOutput{CheckTimes: &reading})
	}
	fmt.Fprint(stdout, reading.Render())
	return 0
}
