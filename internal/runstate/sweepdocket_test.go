package runstate

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

func TestTheOldestUndeliveredEntryNamesTheWorkItem(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		id          string
		title       string
		description string
	}{
		{"titled", "yoyodyne-ifd.430.40.26", "Stopped change 26", "Stopped change 26 (yoyodyne-ifd.430.40.26)"},
		{"untitled", "yoyodyne-ifd.430.40.26", "", "a work item with no recorded title (yoyodyne-ifd.430.40.26)"},
		{"blank title", "yoyodyne-ifd.430.40.26", " \n\t ", "a work item with no recorded title (yoyodyne-ifd.430.40.26)"},
		{"unreadable item", "", "", "an entry whose work item could not be read"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			position := triage.WindowPosition{Key: triage.Key(triage.ClassStoppedRun, "run-26"), Since: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
			delivery := DocketDelivery{
				Delivered:   25,
				Undelivered: []triage.WindowPosition{position},
				Oldest:      &UndeliveredDocketEntry{Position: position, WorkItemID: test.id, WorkItemTitle: test.title},
			}
			if err := delivery.Validate(); err != nil {
				t.Fatal(err)
			}
			if got := delivery.Says(); !strings.Contains(got, "the oldest is "+test.description+", docket entry ") {
				t.Fatalf("Says() = %q, want the oldest entry named as %q", got, test.description)
			}
		})
	}
}
