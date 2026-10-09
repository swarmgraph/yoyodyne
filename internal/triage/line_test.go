package triage

import (
	"strings"
	"testing"
	"time"
)

// The one line an entry is cut down to names the same next mover the entry
// names shown whole, whichever of them it is.
func TestAnEntrysLineNamesTheMoverItsWholeRenderingNames(t *testing.T) {
	t.Parallel()

	base := Entry{
		SchemaVersion: SchemaVersion, Class: ClassStoppedRun, ProductID: "yoyodyne",
		RunID: "run-c2a6e04172ff84ae79268a7d2c507d43", WorkItemID: "yoyodyne-ifd.434.12",
		WorkItemTitle: "Machine home", RecordedAt: time.Date(2026, 10, 1, 17, 0, 0, 0, time.UTC),
	}
	base.Key = Key(base.Class, base.RunID)
	waiting := base
	waiting.Class = ClassPublication
	waiting.Key = PublicationKey(base.RunID, 812)
	waiting.Publication = &Publication{Number: 812, WaitingOn: "yoyodyne-ifd.500"}
	unread := base
	unread.CountersProblem = "the triage record could not be read"
	unready := base
	unready.Class, unready.RunID = ClassUnreadyItem, ""
	unready.Key = UnreadyKey(unready.WorkItemID, []string{"citation"})

	for _, test := range []struct {
		name    string
		entry   Entry
		mover   string
		command string
	}{
		{"undecided", base, "you", "yoyo triage show " + base.RunID},
		{"publication waiting on the target's fix", waiting, "the harness", "yoyo triage show " + base.RunID},
		{"unreadable triage record", unread, "unknown", "yoyo triage show " + base.RunID},
		{"nothing ran", unready, "you", "yoyo triage show '" + unready.Key + "'"},
	} {
		if got := test.entry.NextMover(); got != test.mover {
			t.Fatalf("%s: NextMover() = %q, want %q", test.name, got, test.mover)
		}
		if !strings.Contains(test.entry.Render(), "Next mover: "+test.mover+" —") {
			t.Fatalf("%s: the whole entry does not name %q as next mover:\n%s", test.name, test.mover, test.entry.Render())
		}
		line := test.entry.Line()
		for _, want := range []string{"[" + test.entry.Class.Title() + "]", test.entry.WorkItemID + " — Machine home", "next mover: " + test.mover + ";", "`" + test.command + "` shows it whole"} {
			if !strings.Contains(line, want) {
				t.Fatalf("%s: Line() = %q, missing %q", test.name, line, want)
			}
		}
		if len(line) > MaxLineBytes || strings.Count(line, "\n") != 1 {
			t.Fatalf("%s: Line() is not one bounded line: %q", test.name, line)
		}
	}
}
