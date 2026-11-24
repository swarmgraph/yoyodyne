package runstate

import (
	"strings"
	"testing"
	"time"
)

func TestARunWaitingForItsEpicIsInFlightAndSaysSinceWhen(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		wait   EpicWait
		status Status
		refuse string
	}{
		"waiting":       {EpicWait{Since: time.Now()}, StatusRunning, ""},
		"no time":       {EpicWait{}, StatusRunning, "epic_wait: since is required"},
		"already ended": {EpicWait{Since: time.Now()}, StatusSucceeded, "epic_wait requires a run that is still in flight"},
	} {
		err := State{Status: test.status, EpicWait: &test.wait}.Validate()
		if said := err != nil && strings.Contains(err.Error(), "epic_wait"); said != (test.refuse != "") || (said && !strings.Contains(err.Error(), test.refuse)) {
			t.Errorf("%s: Validate() = %v, want it to say %q", name, err, test.refuse)
		}
	}
}
