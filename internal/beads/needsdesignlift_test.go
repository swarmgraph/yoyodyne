package beads

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// Lifting the needs-design label takes it off and notes why in one write, and
// refuses an answer that still shows the label.
func TestLiftNeedsDesignRemovesTheLabelAndNotesInOneWrite(t *testing.T) {
	t.Parallel()

	const reason = "the architect's design landed as commit abc"
	runner := &fakeRunner{responses: []string{assignedItemJSON("open", reason, "", false)}}
	if _, err := (Client{Runner: runner}).LiftNeedsDesign(context.Background(), "yoyodyne-1", reason); err != nil {
		t.Fatalf("LiftNeedsDesign() error = %v", err)
	}
	if len(runner.args) == 0 {
		t.Fatal("LiftNeedsDesign() ran nothing")
	}
	want := []string{"update", "yoyodyne-1", "--append-notes=" + FrameNote(reason), "--remove-label=needs-design", "--json"}
	if !reflect.DeepEqual(runner.args[0], want) {
		t.Fatalf("bd args = %#v, want %#v", runner.args[0], want)
	}

	labelled := strings.Replace(assignedItemJSON("open", reason, "", false), `"metadata"`, `"labels":["needs-design"],"metadata"`, 1)
	kept := &fakeRunner{responses: []string{labelled}}
	if _, err := (Client{Runner: kept}).LiftNeedsDesign(context.Background(), "yoyodyne-1", reason); err == nil || !strings.Contains(err.Error(), "still carries") {
		t.Errorf("LiftNeedsDesign() with the label still on error = %v", err)
	}
	if _, err := (Client{Runner: &fakeRunner{}}).LiftNeedsDesign(context.Background(), "yoyodyne-1", " "); err == nil {
		t.Error("LiftNeedsDesign() with no reason error = nil")
	}
}
