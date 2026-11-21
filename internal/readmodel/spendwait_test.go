package readmodel

import (
	"strings"
	"testing"
	"time"
)

// A run waiting for the shared remote to take its review round is said under its
// running line, in plain words, with how long it has waited and how many tries.
func TestARunWaitingForTheSharedRemoteSaysSo(t *testing.T) {
	t.Parallel()
	standing := Standing{Running: []RunningRun{{
		RunID: "run-a", WorkItemID: "yoyodyne-ifd.1", Elapsed: time.Hour,
		WaitingForRemote: "the shared remote has not taken this run's review round: waiting since 2026-11-19 10:00:00 PST, 4 tries so far, asking again every minute",
		RemoteWaited:     4 * time.Minute, RemoteTries: 4,
	}}}
	rendered := standing.renderRunning()
	for _, want := range []string{"waiting for the shared remote", "4 tries so far", "waiting since 2026-11-19 10:00:00 PST"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the running line reads:\n%s\nwant it to say %q", rendered, want)
		}
	}
	plain := Standing{Running: []RunningRun{{RunID: "run-b", WorkItemID: "yoyodyne-ifd.2"}}}
	if strings.Contains(plain.renderRunning(), "shared remote") {
		t.Errorf("a run that waits on nothing is said to wait for the remote")
	}
}
