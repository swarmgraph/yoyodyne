package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

type fakeOriginRecorder struct {
	recorded map[string]domain.WorkItemOrigin
	refuse   string
}

func (f *fakeOriginRecorder) RecordOrigin(_ context.Context, id string, origin domain.WorkItemOrigin) (beads.WorkItem, error) {
	if id == f.refuse {
		return beads.WorkItem{}, errors.New("tracker refused the write")
	}
	if f.recorded == nil {
		f.recorded = map[string]domain.WorkItemOrigin{}
	}
	f.recorded[id] = origin
	return beads.WorkItem{ID: id, Origin: origin}, nil
}

// The backfill sets an origin only where an item records none and its notes
// state one exactly; it leaves everything else as it reads, writes nothing on a
// dry run, and counts a refused write without stopping.
func TestTheOriginBackfillSetsOnlyWhatTheNotesStateExactly(t *testing.T) {
	t.Parallel()

	const admitted = "Admitted to the backlog by the Lead Product Manager in conversation chat-1, after turn 4.\n\nReason: asked."
	fromReport := admitted + "\n\nAdmitted from report report-1, filed at \"warning\" by the development manager: it broke"
	fromDirective := admitted + "\n\nIn answer to directive directive-1, received by the product-manager, which said: do it"
	items := []beads.WorkItem{
		{ID: "yoyodyne-1", Notes: fromReport},
		{ID: "yoyodyne-2", Notes: fromDirective},
		// Notes that never said who asked are left unknown.
		{ID: "yoyodyne-3", Notes: admitted},
		// An origin already recorded is never rewritten.
		{ID: "yoyodyne-4", Notes: fromReport, Origin: domain.WorkItemOrigin{Asker: domain.AskerSweep, AdmittedBy: domain.RoleProductManager}},
	}

	dry := &fakeOriginRecorder{}
	set, failures := recordOrigins(context.Background(), dry, items, true)
	if failures != 0 || len(set) != 2 || len(dry.recorded) != 0 {
		t.Fatalf("dry run set = %#v, failures = %d, recorded = %#v; want two reported and nothing written", set, failures, dry.recorded)
	}

	tracker := &fakeOriginRecorder{refuse: "yoyodyne-2"}
	set, failures = recordOrigins(context.Background(), tracker, items, false)
	if failures != 1 || len(set) != 2 || set[1].Failure == "" {
		t.Fatalf("set = %#v, failures = %d; want the refused write counted and named", set, failures)
	}
	want := domain.WorkItemOrigin{Asker: domain.AskerReport, AdmittedBy: domain.RoleProductManager, Report: "report-1", ReportedBy: domain.RoleDevelopmentManager}
	if len(tracker.recorded) != 1 || tracker.recorded["yoyodyne-1"] != want {
		t.Fatalf("recorded = %#v, want only yoyodyne-1 given %#v", tracker.recorded, want)
	}
	if set[0].AskedBy != "development-manager" || set[0].OnBehalfOf != "development-manager" {
		t.Fatalf("reported = %#v, want who asked named", set[0])
	}
}
