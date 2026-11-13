package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// waitingArchitect is the architect's wake with the wait record and the report
// pile wired beside it.
func waitingArchitect(t *testing.T) (*architectWake, *runstate.NeedsDesignLabelledStore, *filedReports) {
	t.Helper()
	w := newArchitectWake(t)
	labelled, err := runstate.NewNeedsDesignLabelledStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewNeedsDesignLabelledStore() error = %v", err)
	}
	filed := &filedReports{}
	w.trigger.NeedsDesignLabelled = labelled
	w.trigger.Breakage = filed
	w.trigger.Attribution = report.Attribution{ProductID: "example", RepositoryID: "example"}
	return w, labelled, filed
}

func priority(p int) *int { return &p }

// Every pull records since when each item has carried the label, keeps that
// time across pulls, and drops the item when the label comes off.
func TestEachPullRecordsSinceWhenAnItemHasWaitedOnDesign(t *testing.T) {
	t.Parallel()

	w, labelled, _ := waitingArchitect(t)
	w.items.items = []NeedsDesignItem{{ID: "yoyodyne-item", Title: "Item", Priority: priority(2)}}
	w.fire(t, recurringNow)
	w.fire(t, recurringNow.Add(2*time.Hour))
	record, _, err := labelled.Load()
	if err != nil {
		t.Fatal(err)
	}
	if since, ok := record.Since("yoyodyne-item"); !ok || !since.Equal(recurringNow.UTC()) {
		t.Fatalf("since = %v, %v, want the first pull that saw the label, %v", since, ok, recurringNow)
	}

	w.items.unlabel("yoyodyne-item")
	w.fire(t, recurringNow.Add(3*time.Hour))
	if record, _, _ = labelled.Load(); len(record.Items) != 0 {
		t.Fatalf("record after the label came off = %+v, want it empty", record.Items)
	}
}

// A P0 item still waiting more than a day is reported to the Lead Product
// Manager once; a lower priority, or a P0 under a day, is not reported at all.
func TestAPZeroItemWaitingMoreThanADayOnDesignIsReportedOnce(t *testing.T) {
	t.Parallel()

	w, labelled, filed := waitingArchitect(t)
	w.items.items = []NeedsDesignItem{
		{ID: "yoyodyne-top", Title: "Top", Priority: priority(0)},
		{ID: "yoyodyne-low", Title: "Low", Priority: priority(1)},
	}
	w.fire(t, recurringNow)
	w.fire(t, recurringNow.Add(23*time.Hour))
	if len(filed.filed) != 0 {
		t.Fatalf("reports under a day = %+v, want none", filed.filed)
	}
	w.fire(t, recurringNow.Add(25*time.Hour))
	w.fire(t, recurringNow.Add(49*time.Hour))
	if len(filed.filed) != 1 {
		t.Fatalf("reports = %+v, want exactly one, for the P0 item", filed.filed)
	}
	reported := filed.filed[0]
	if reported.Addressee() != domain.RoleProductManager || reported.Role != report.HarnessReporter || reported.WorkItemID != "yoyodyne-top" {
		t.Fatalf("report = %+v, want the harness's own, about yoyodyne-top, for the Lead Product Manager", reported)
	}
	if !strings.Contains(reported.Message, "since "+recurringNow.Local().Format("2006-01-02 15:04 MST")) || !strings.Contains(reported.Message, "1 day 1 hour ago") {
		t.Fatalf("message = %q, want since when and how long", reported.Message)
	}
	if record, _, _ := labelled.Load(); !record.Reported("yoyodyne-top") || record.Reported("yoyodyne-low") {
		t.Fatalf("record = %+v, want only the P0 item marked reported", record.Items)
	}

	// Taking the label off and putting it back starts a new wait, reported
	// again once it too passes a day.
	w.items.unlabel("yoyodyne-top")
	w.fire(t, recurringNow.Add(50*time.Hour))
	w.items.items = append(w.items.items, NeedsDesignItem{ID: "yoyodyne-top", Title: "Top", Priority: priority(0)})
	w.fire(t, recurringNow.Add(51*time.Hour))
	w.fire(t, recurringNow.Add(76*time.Hour))
	if len(filed.filed) != 2 {
		t.Fatalf("reports after a new wait passed a day = %d, want two", len(filed.filed))
	}
}

// The scheduler's refusal of a labelled item says since when it has waited.
func TestTheSchedulersRefusalSaysSinceWhenTheItemHasWaitedOnDesign(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	entry := backlog.Entry{ID: "item", NeedsDesign: true, NeedsDesignSince: at}
	reason, named := passedOverReason(entry)
	if !named || !strings.Contains(reason, "labelled "+domain.NeedsDesignLabel+" since "+at.Local().Format("2006-01-02 15:04 MST")+",") {
		t.Fatalf("reason = %q, want since when", reason)
	}
	entry.NeedsDesignSince = time.Time{}
	if reason, _ := passedOverReason(entry); !strings.Contains(reason, backlog.NeedsDesignHold) {
		t.Fatalf("reason without a recorded time = %q, want the sentence unchanged", reason)
	}
}
