package runstate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const laneReportConversation = "chat-0123456789abcdef0123456789abcdef"

func laneReportVersion(summary string, turn int) LaneReport {
	return LaneReport{
		ProductID: "yoyodyne",
		Agent:     "factory",
		Report: LaneReportContent{
			Summary:   summary,
			Remaining: []string{"root-cause work for the stalled checks"},
			Blockers: []LaneReportBlocker{{
				What:      "a prevention item outside the lane",
				WaitingOn: "product-manager",
				Cites:     "report-2026-09-25-0001",
			}},
		},
		Stamp: LaneReportStamp{
			Pass:           fmt.Sprintf("factory-watch#%d", turn),
			ConversationID: laneReportConversation,
			Turn:           turn,
		},
		RecordedAt: time.Date(2026, 9, 25, 10, turn, 0, 0, time.UTC),
	}
}

// admitProductManager stands in for the read model's mover check, which this
// package cannot import: it admits the one mover these tests name and refuses
// everything else, so the store is shown to ask the check it was given.
func admitProductManager(token string) error {
	if token == "product-manager" {
		return nil
	}
	return fmt.Errorf("waiting_on %q is not a mover a blocker may wait on", token)
}

func newLaneReportTestStore(t *testing.T, redact ...string) *LaneReportStore {
	t.Helper()
	store, err := NewLaneReportStore(t.TempDir(), "yoyodyne", admitProductManager, redact...)
	if err != nil {
		t.Fatalf("NewLaneReportStore() error = %v", err)
	}
	return store
}

// Three reports written in turn leave the newest as the report and all three in
// the history, oldest first, each stamped with the pass and the turn that wrote
// it.
func TestALaneReportIsRewrittenWholeAndItsVersionsKept(t *testing.T) {
	t.Parallel()

	store := newLaneReportTestStore(t)
	for turn := 1; turn <= 3; turn++ {
		written, err := store.Write(context.Background(), laneReportVersion(fmt.Sprintf("pass %d: the line is moving", turn), turn))
		if err != nil {
			t.Fatalf("Write(%d) error = %v", turn, err)
		}
		if written.Version != turn {
			t.Fatalf("Write(%d) numbered the version %d", turn, written.Version)
		}
	}

	current, found, err := store.Current("factory")
	if err != nil || !found {
		t.Fatalf("Current() = %v, %v", found, err)
	}
	if current.Version != 3 || current.Report.Summary != "pass 3: the line is moving" {
		t.Fatalf("the current report is version %d %q, want the third", current.Version, current.Report.Summary)
	}
	if current.Stamp.Pass != "factory-watch#3" || current.Stamp.Turn != 3 || current.Stamp.ConversationID != laneReportConversation {
		t.Fatalf("the current report's stamp = %+v", current.Stamp)
	}

	history, err := store.History("factory")
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("the history holds %d versions, want 3", len(history))
	}
	for index, version := range history {
		if version.Version != index+1 || version.Stamp.Turn != index+1 {
			t.Errorf("history[%d] is version %d written at turn %d", index, version.Version, version.Stamp.Turn)
		}
	}

	// Under the state root, one directory per instance, and nowhere else.
	if !strings.HasSuffix(store.ReportPath("factory"), "/projects/yoyodyne/state/program-managers/factory/report.json") {
		t.Errorf("the report is at %s", store.ReportPath("factory"))
	}
}

// The fifty-first version drops the oldest, and the numbering goes on counting.
func TestTheFiftyFirstLaneReportDropsTheOldest(t *testing.T) {
	t.Parallel()

	store := newLaneReportTestStore(t)
	for turn := 1; turn <= LaneReportHistory+1; turn++ {
		if _, err := store.Write(context.Background(), laneReportVersion(fmt.Sprintf("pass %d", turn), turn)); err != nil {
			t.Fatalf("Write(%d) error = %v", turn, err)
		}
	}
	history, err := store.History("factory")
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history) != LaneReportHistory {
		t.Fatalf("the history holds %d versions, want %d", len(history), LaneReportHistory)
	}
	if history[0].Version != 2 || history[len(history)-1].Version != LaneReportHistory+1 {
		t.Fatalf("the history runs from version %d to %d, want 2 to %d",
			history[0].Version, history[len(history)-1].Version, LaneReportHistory+1)
	}
}

// A report that is malformed — past its bound, missing a field, or waiting on
// somebody outside the vocabulary — is refused whole, and the report before it
// is exactly as it was.
func TestAMalformedLaneReportIsRefusedWholeAndThePreviousOneStands(t *testing.T) {
	t.Parallel()

	store := newLaneReportTestStore(t)
	if _, err := store.Write(context.Background(), laneReportVersion("the standing report", 1)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	before, err := os.ReadFile(store.ReportPath("factory"))
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]func(*LaneReport){
		"past the bound": func(v *LaneReport) { v.Report.Summary = strings.Repeat("x", MaxLaneReportBytes) },
		"no summary":     func(v *LaneReport) { v.Report.Summary = " " },
		"no remaining":   func(v *LaneReport) { v.Report.Remaining = nil },
		"no blockers":    func(v *LaneReport) { v.Report.Blockers = nil },
		"no what":        func(v *LaneReport) { v.Report.Blockers[0].What = "" },
		"no citation":    func(v *LaneReport) { v.Report.Blockers[0].Cites = "" },
		"unknown mover":  func(v *LaneReport) { v.Report.Blockers[0].WaitingOn = "nobody" },
		"a developer":    func(v *LaneReport) { v.Report.Blockers[0].WaitingOn = "developer" },
		"no turn":        func(v *LaneReport) { v.Stamp.Turn = 0 },
	}
	for name, spoil := range cases {
		version := laneReportVersion("a report that must not land", 2)
		spoil(&version)
		if _, err := store.Write(context.Background(), version); err == nil {
			t.Errorf("%s: Write() accepted the report", name)
		}
	}

	after, err := os.ReadFile(store.ReportPath("factory"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("a refused report changed the standing one:\nbefore %s\nafter  %s", before, after)
	}
	history, err := store.History("factory")
	if err != nil || len(history) != 1 {
		t.Fatalf("History() = %d versions, %v; want the one that landed", len(history), err)
	}
}

// What the role wrote is redacted with the store's values before it is measured
// or stored, so a secret in the summary never reaches the disk.
func TestALaneReportIsRedactedBeforeItIsWritten(t *testing.T) {
	t.Parallel()

	store := newLaneReportTestStore(t, "sk-live-secret")
	version := laneReportVersion("the token sk-live-secret leaked into a check log", 1)
	version.Report.Remaining = []string{"rotate sk-live-secret"}
	written, err := store.Write(context.Background(), version)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if strings.Contains(written.Report.Summary, "sk-live-secret") || !strings.Contains(written.Report.Summary, "[REDACTED]") {
		t.Errorf("the returned report was not redacted: %q", written.Report.Summary)
	}
	for _, name := range []string{"report.json", "history.jsonl"} {
		stored, err := os.ReadFile(store.Root() + "/factory/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(stored), "sk-live-secret") {
			t.Errorf("%s holds the secret:\n%s", name, stored)
		}
	}
}

// An instance that never reported has no report, which is not a failure.
func TestAnInstanceWithNoLaneReportHasNone(t *testing.T) {
	t.Parallel()

	store := newLaneReportTestStore(t)
	if _, found, err := store.Current("factory"); found || err != nil {
		t.Fatalf("Current() = %v, %v; want none", found, err)
	}
	if history, err := store.History("factory"); len(history) != 0 || err != nil {
		t.Fatalf("History() = %v, %v; want none", history, err)
	}
}

// A store is never built without the read model's check on movers, because one
// that could not say which movers a blocker may wait on would write any token.
func TestALaneReportStoreRequiresAMoverCheck(t *testing.T) {
	t.Parallel()

	if _, err := NewLaneReportStore(t.TempDir(), "yoyodyne", nil); err == nil {
		t.Fatal("NewLaneReportStore() built a store with no mover check")
	}
}
