package contextbundle

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// Urgent entries do not move the ordinary walk position, and decided entries
// sit outside it too. A pass must still reach both without repeating its first
// slice, and an early ending must leave a useful order for the next pass.
func TestADocketPassContinuesPastUrgentEntriesWithoutAdvancingTheOrdinaryWalk(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.FixedZone("PDT", -7*60*60))
	var entries []triage.Entry
	for i := range 27 {
		entry := docketEntry(fmt.Sprintf("run-%032x", i), fmt.Sprintf("urgent-%d", i))
		entry.Class = triage.ClassEscalation
		entry.Key = triage.Key(entry.Class, entry.RunID)
		entry.RecordedAt = now.Add(time.Duration(i-100) * time.Hour)
		entries = append(entries, entry)
	}
	for i := range 3 {
		entries = append(entries, gatedDocketEntry(i, now.Add(time.Duration(i-200)*time.Hour)))
	}
	request := ProductRequest{TriageDocket: entries, TriageDocketAt: now}
	first := TriageDocketWindow(request, nil, nil)
	if len(first.Listed) != 25 || len(first.Unlisted) != 5 || first.Position != nil {
		t.Fatalf("first slice listed %d, unread %d, position %+v; want 25, 5 and no ordinary walk advance", len(first.Listed), len(first.Unlisted), first.Position)
	}
	var shown, pending []triage.WindowPosition
	for _, standing := range first.Listed {
		shown = append(shown, standing.At())
	}
	for _, standing := range first.Unlisted {
		pending = append(pending, standing.At())
	}
	// The saved order survives JSON and a new process with a different time zone.
	data, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	var restored []triage.WindowPosition
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	next := TriageDocketWindow(request, shown, restored)
	if len(next.Listed) != 5 || len(next.Unlisted) != 0 {
		t.Fatalf("second slice listed %d, unread %d; want all five remaining entries", len(next.Listed), len(next.Unlisted))
	}
	for i, standing := range next.Listed {
		if standing.Entry.Key != first.Unlisted[i].Entry.Key {
			t.Fatalf("entry %d is %s, want %s", i, standing.Entry.Key, first.Unlisted[i].Entry.Key)
		}
	}
	// On a new pass the unread entries go before those shown on the last pass,
	// even though the old urgent entries would ordinarily fill the window again.
	afterEnding := TriageDocketWindow(request, nil, restored)
	for i, standing := range next.Listed {
		if afterEnding.Listed[i].Entry.Key != standing.Entry.Key {
			t.Fatalf("the next pass skipped unread entry %s", standing.Entry.Key)
		}
	}
}
