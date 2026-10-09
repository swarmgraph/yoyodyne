package contextbundle

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A docket with more live entries than its bounds can show with their evidence
// still names every one of them, so an unfinished publication docketed behind
// sixty stopped runs can be decided from the docket like any of them.
func TestTheDocketNamesEveryEntryItHasNoRoomToShowWhole(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 5, 19, 0, 0, 0, time.UTC)
	var entries []triage.Entry
	for index := range 60 {
		entry := docketEntry(fmt.Sprintf("run-%032x", index), fmt.Sprintf("yoyodyne-named-%d", index))
		entry.RecordedAt = now.AddDate(0, 0, index-90)
		// Long enough that the byte bound, and not only the count, cuts entries.
		entry.Blocker = strings.Repeat("the evidence as it was recorded ", 100)
		entries = append(entries, entry)
	}
	publication := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.PublicationKey("run-c2a6e04172ff84ae79268a7d2c507d43", 812),
		Class:         triage.ClassPublication,
		ProductID:     "yoyodyne",
		RunID:         "run-c2a6e04172ff84ae79268a7d2c507d43",
		WorkItemID:    "yoyodyne-ifd.434.12",
		WorkItemTitle: "Machine home",
		RecordedAt:    now.AddDate(0, 0, -4),
		Publication:   &triage.Publication{Number: 812, State: "open", ApprovedAt: now.AddDate(0, 0, -4)},
	}
	entries = append(entries, publication)

	window := TriageDocketWindow(ProductRequest{TriageDocket: entries, TriageDocketAt: now}, nil, nil)
	if got := len(window.Listed) + len(window.Unlisted); got != len(entries) {
		t.Fatalf("the window accounts for %d entries, want all %d", got, len(entries))
	}
	if len(window.Unlisted) == 0 || len(window.Cut) == 0 {
		t.Fatalf("the docket was meant to be past both bounds: %d named in one line, %d cut short", len(window.Unlisted), len(window.Cut))
	}
	if len(window.Text) > MaxTriageDocketBytes {
		t.Fatalf("the docket is %d bytes, past its bound of %d, though its one-line names fit within it", len(window.Text), MaxTriageDocketBytes)
	}
	for _, entry := range entries {
		if !strings.Contains(window.Text, "("+entry.RunID+")") {
			t.Fatalf("entry %s on %s is missing from the docket:\n%s", entry.Key, entry.WorkItemID, window.Text)
		}
	}

	// Every entry with no room for its evidence is one line naming the run, the
	// item, the kind of stoppage, who moves next, and how to read it whole.
	sawPublication := false
	for _, standing := range window.Unlisted {
		entry := standing.Entry
		line := entry.Line()
		if !strings.Contains(window.Text, line) {
			t.Fatalf("entry %s is not named in its one line %q:\n%s", entry.Key, line, window.Text)
		}
		for _, want := range []string{
			entry.RunID, entry.WorkItemID, "[" + entry.Class.Title() + "]",
			"next mover: " + entry.NextMover() + ";", "`yoyo triage show " + entry.RunID + "` shows it whole",
		} {
			if !strings.Contains(line, want) {
				t.Fatalf("the line for %s does not carry %q: %q", entry.Key, want, line)
			}
		}
		if strings.Count(line, "\n") != 1 {
			t.Fatalf("the line for %s is not one line: %q", entry.Key, line)
		}
		if entry.Class == triage.ClassPublication {
			sawPublication = true
			if !strings.Contains(line, "[unfinished publication]") || !strings.Contains(line, "Machine home") {
				t.Fatalf("the unfinished publication's line does not say what it is: %q", line)
			}
		}
	}
	if !sawPublication {
		t.Fatal("the unfinished publication was meant to be among the entries named in one line")
	}

	// Every entry whose evidence was cut short says who moves next and how to
	// read it whole, beside the heading that names its kind, item, and run.
	cut := make(map[triage.WindowPosition]bool, len(window.Cut))
	for _, at := range window.Cut {
		cut[at] = true
	}
	for _, standing := range window.Listed {
		if !cut[standing.At()] {
			continue
		}
		entry := standing.Entry
		heading := fmt.Sprintf("  [%s] %s on %s (%s)\n", entry.Class.Title(), entry.RecordedAt.UTC().Format(time.RFC3339), entry.WorkItemID, entry.RunID)
		start := strings.Index(window.Text, heading)
		if start < 0 {
			t.Fatalf("the cut entry %s lost its heading:\n%s", entry.Key, window.Text)
		}
		rest := window.Text[start:]
		end := strings.Index(rest, "]\n")
		if end < 0 || !strings.Contains(rest[:end], "Next mover: "+entry.NextMover()+". `yoyo triage show "+entry.RunID+"` shows it whole") {
			t.Fatalf("the cut entry %s does not say who moves next and how to read it whole:\n%s", entry.Key, rest[:min(len(rest), 4096)])
		}
	}
}

// A docket so long that its one-line names alone pass the byte bound lists them
// anyway, past it, and still shows one entry with its evidence: a longer docket
// costs a conversation some room, and an entry left out costs a decision.
func TestADocketWhoseNamesAlonePassItsBoundStillNamesEveryEntry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 5, 19, 0, 0, 0, time.UTC)
	var entries []triage.Entry
	for index := range 400 {
		entry := docketEntry(fmt.Sprintf("run-%032x", index), fmt.Sprintf("yoyodyne-crowded-%d", index))
		entry.WorkItemTitle = strings.Repeat("a long title ", 10)
		entry.RecordedAt = now.Add(time.Duration(index-1000) * time.Hour)
		entries = append(entries, entry)
	}
	window := TriageDocketWindow(ProductRequest{TriageDocket: entries, TriageDocketAt: now}, nil, nil)
	if len(window.Listed) != 1 || len(window.Unlisted) != len(entries)-1 {
		t.Fatalf("shown %d with evidence and %d in one line, want one and the other %d", len(window.Listed), len(window.Unlisted), len(entries)-1)
	}
	for _, entry := range entries {
		if !strings.Contains(window.Text, "("+entry.RunID+")") {
			t.Fatalf("entry %s is missing from the docket", entry.Key)
		}
	}
}
