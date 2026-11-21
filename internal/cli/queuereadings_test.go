package cli

import "testing"

// The scheduler builds a pipeline for each pass, so what the merge queue last
// read from the tracker must be one value shared by every pipeline of the
// process: a pass that cannot read the tracker orders by what an earlier pass
// read.
func TestEveryPassSharesWhatTheMergeQueueLastReadFromTheTracker(t *testing.T) {
	// Not parallel: the state root the components read is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	parts, err := buildComponents(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	first := mergeQueueDriverFrom(parts).Promoter.Pipeline
	second := mergeQueueDriverFrom(parts).Promoter.Pipeline
	if first.QueueReadings == nil || first.QueueReadings != second.QueueReadings {
		t.Fatalf("pipelines hold %p and %p, want one shared record of the queue's last readings", first.QueueReadings, second.QueueReadings)
	}
}
