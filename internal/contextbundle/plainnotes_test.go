package contextbundle

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

type showAnswer struct{ stdout string }

func (s showAnswer) Run(context.Context, execution.Command, execution.OutputObserver) (execution.ProcessResult, error) {
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: s.stdout}, nil
}

// The work-item context a developer or reviewer is given carries the notes as
// the tracker client reads them, which is prose: the boundary each harness
// append is stored under is not shown to a role.
func TestTheWorkItemContextShowsNotesWithoutTheirStoredBoundaries(t *testing.T) {
	t.Parallel()

	stored := "Admitted by hand.\n" + beads.FrameNote("Noted by the development manager.\n\nReason: keep the change small.")
	answer := fmt.Sprintf(`[{"id":"yoyodyne-1","title":"Implement feature","status":"in_progress","priority":1,"issue_type":"task","notes":%q}]`, stored)
	item, err := (beads.Client{Runner: showAnswer{stdout: answer}, Dir: t.TempDir()}).Show(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	bundle, err := Assemble(Request{RepositoryRoot: t.TempDir(), WorkItem: item})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if strings.Contains(bundle.Text, "yoyodyne-note-bytes") {
		t.Fatalf("the work-item context shows a stored boundary:\n%s", bundle.Text)
	}
	if !strings.Contains(bundle.Text, "Admitted by hand.\nNoted by the development manager.\n\nReason: keep the change small.") {
		t.Fatalf("the work-item context lost the notes' prose:\n%s", bundle.Text)
	}
}
