package runinspect

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeInvocations struct{ entries []runstate.InvocationEntry }

func (f fakeInvocations) List(string) ([]runstate.InvocationEntry, error) { return f.entries, nil }

// An inspection lists what each invocation was sent by part, size and origin,
// and carries none of the text.
func TestInspectListsTheInvocationsAndTheirPartsWithoutAnyText(t *testing.T) {
	t.Parallel()
	state := runningState()
	entries := []runstate.InvocationEntry{{
		RunID: state.RunID, Number: 1, Role: "developer", StartedAt: observed, Backend: "claude-code", Model: "claude-test", WholeSize: 30,
		Messages: []runstate.InvocationMessage{{Role: "user", Parts: []runstate.InvocationPart{
			{Name: "developer contract", Origin: "the harness build abc", Digest: strings.Repeat("a", 64), Size: 10},
			{Name: "work item and its notes", Origin: "tracker item yoyodyne-test, read at 2026-10-15T09:00:00Z", Digest: strings.Repeat("b", 64), Size: 20},
		}}},
	}}
	inspector := Inspector{Runs: fakeRuns{state: state}, Now: func() time.Time { return observed }, Invocations: fakeInvocations{entries: entries}}
	inspection := inspector.Inspect(context.Background(), state.RunID)
	if len(inspection.Invocations) != 1 || len(inspection.Invocations[0].Parts) != 2 {
		t.Fatalf("invocations = %+v", inspection.Invocations)
	}
	part := inspection.Invocations[0].Parts[1]
	if part.Name != "work item and its notes" || part.Bytes != 20 || !strings.HasPrefix(part.Origin, "tracker item yoyodyne-test") {
		t.Fatalf("part = %+v", part)
	}
	encoded, err := json.Marshal(inspection.Invocations)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"digest", strings.Repeat("a", 64), `"text"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("the list carries %q, which is not part of a list without text: %s", forbidden, encoded)
		}
	}
}
