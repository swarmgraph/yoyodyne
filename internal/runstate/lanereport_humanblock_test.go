package runstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

func humanBlock(item, cites string, since time.Time) LaneReportHumanBlock {
	return LaneReportHumanBlock{Item: item, What: "renew the forge token", Reason: ownership.OperatorCredential, Credential: &ownership.Credential{Needs: ownership.CredentialSecret, What: "the forge token"}, Cites: cites, Since: since}
}

// waitingRecords stands in for the read model's resolution of a citation: each
// record named is open or cleared, and anything else is no record at all.
type waitingRecords map[string]WaitingRecordState

func (w waitingRecords) check(cites string) (WaitingRecordState, error) {
	if cites == "report-unreadable" {
		return WaitingRecordUnknown, errors.New("the reports could not be read")
	}
	return w[cites], nil
}

// An entry the next pass leaves out is carried forward until the record it
// cites says the operator's act is recorded, however many passes leave it out
// and however old it is; an entry written again keeps the earliest moment.
func TestABlockedOnAHumanEntryIsCarriedUntilTheOperatorsActClearsIt(t *testing.T) {
	t.Parallel()

	store := newLaneReportTestStore(t)
	records := waitingRecords{"report-a": WaitingRecordOpen, "report-b": WaitingRecordOpen}
	store.UseWaitingRecords(records.check)
	weekAgo := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)

	first := laneReportVersion("first", 1)
	first.Report.BlockedOnAHuman = []LaneReportHumanBlock{humanBlock("yoyodyne-ifd.9", "report-a", weekAgo), humanBlock("", "report-b", weekAgo)}
	if _, err := store.Write(context.Background(), first); err != nil {
		t.Fatalf("Write(first) error = %v", err)
	}
	for turn := 2; turn <= 4; turn++ {
		next := laneReportVersion("later", turn)
		if turn == 3 {
			next.Report.BlockedOnAHuman = []LaneReportHumanBlock{humanBlock("yoyodyne-ifd.9", "report-a", weekAgo.Add(48*time.Hour))}
		}
		written, err := store.Write(context.Background(), next)
		if err != nil {
			t.Fatalf("Write(%d) error = %v", turn, err)
		}
		if len(written.Report.BlockedOnAHuman) != 2 {
			t.Fatalf("version %d carries %+v, want both entries carried", turn, written.Report.BlockedOnAHuman)
		}
		for _, entry := range written.Report.BlockedOnAHuman {
			if !entry.Since.Equal(weekAgo) {
				t.Fatalf("version %d moved the moment an entry began: %+v", turn, entry)
			}
		}
	}

	records["report-a"] = WaitingRecordCleared
	cleared, err := store.Write(context.Background(), laneReportVersion("cleared", 5))
	if err != nil {
		t.Fatalf("Write(cleared) error = %v", err)
	}
	if len(cleared.Report.BlockedOnAHuman) != 1 || cleared.Report.BlockedOnAHuman[0].Cites != "report-b" || !cleared.Report.BlockedOnAHuman[0].Carried {
		t.Fatalf("after the operator's act on report-a the report carries %+v, want only the question citing report-b, carried", cleared.Report.BlockedOnAHuman)
	}
	current, _, err := store.Current("factory")
	if err != nil || len(current.Report.BlockedOnAHuman) != 1 {
		t.Fatalf("the stored report carries %+v, %v", current.Report.BlockedOnAHuman, err)
	}
}

// An entry with no reason from the closed list, citing no waiting record, or
// citing one already cleared is refused on its own, and the rest of the report
// is written; a record that could not be read refuses nothing.
func TestABadBlockedOnAHumanEntryIsRefusedAndTheReportStands(t *testing.T) {
	t.Parallel()

	store := newLaneReportTestStore(t)
	store.UseWaitingRecords(waitingRecords{"report-a": WaitingRecordOpen, "report-done": WaitingRecordCleared}.check)
	at := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	version := laneReportVersion("the lane is moving", 1)
	approval := humanBlock("yoyodyne-ifd.2", "report-a", at)
	approval.Reason = "approval"
	version.Report.BlockedOnAHuman = []LaneReportHumanBlock{
		humanBlock("yoyodyne-ifd.1", "report-a", at),
		approval,
		humanBlock("yoyodyne-ifd.3", "report-nowhere", at),
		humanBlock("yoyodyne-ifd.4", "report-done", at),
		humanBlock("yoyodyne-ifd.5", "report-unreadable", at),
	}
	written, err := store.Write(context.Background(), version)
	if err != nil {
		t.Fatalf("Write() error = %v, want the report written without the bad entries", err)
	}
	var kept []string
	for _, entry := range written.Report.BlockedOnAHuman {
		kept = append(kept, entry.Item)
	}
	if strings.Join(kept, ",") != "yoyodyne-ifd.1,yoyodyne-ifd.5" {
		t.Fatalf("kept %v, want the good entry and the one whose record could not be read", kept)
	}
	refused := strings.Join(written.Report.Refused, "\n")
	for _, want := range []string{`reason "approval" is not one only the operator can act on`, "report-nowhere, which is no report", "report-done, on which the act that clears it is already recorded"} {
		if !strings.Contains(refused, want) {
			t.Errorf("the refusals do not say %q:\n%s", want, refused)
		}
	}
	if written.Report.Summary != "the lane is moving" {
		t.Fatalf("the rest of the report was not written: %+v", written.Report)
	}
}

// An entry under "credential" stored before an entry had to say which secret or
// account it needs still reads, and is carried forward by the next write, which
// succeeds; a turn that writes such an entry now has it refused on its own.
func TestACredentialEntryStoredBeforeItHadToSayWhichStillReadsAndIsCarried(t *testing.T) {
	t.Parallel()

	store := newLaneReportTestStore(t)
	store.UseWaitingRecords(waitingRecords{"report-a": WaitingRecordOpen, "report-b": WaitingRecordOpen}.check)
	weekAgo := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	first := laneReportVersion("first", 1)
	first.Report.BlockedOnAHuman = []LaneReportHumanBlock{humanBlock("yoyodyne-ifd.9", "report-a", weekAgo)}
	if _, err := store.Write(context.Background(), first); err != nil {
		t.Fatalf("Write(first) error = %v", err)
	}
	// Make the stored version what an older build wrote: no credential field.
	for _, path := range []string{store.ReportPath("factory"), filepath.Join(store.Root(), "factory", "history.jsonl")} {
		stored, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		bare := strings.ReplaceAll(string(stored), `,"credential":{"needs":"secret","what":"the forge token"}`, "")
		if bare == string(stored) {
			t.Fatalf("%s carries no credential field to take out:\n%s", path, stored)
		}
		if err := os.WriteFile(path, []byte(bare), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if history, err := store.History("factory"); err != nil || len(history) != 1 || history[0].Report.BlockedOnAHuman[0].Credential != nil {
		t.Fatalf("History() = %+v, %v; want the stored version read with its bare entry", history, err)
	}

	second := laneReportVersion("second", 2)
	fresh := humanBlock("yoyodyne-ifd.10", "report-b", weekAgo)
	fresh.Credential = nil
	second.Report.BlockedOnAHuman = []LaneReportHumanBlock{fresh}
	written, err := store.Write(context.Background(), second)
	if err != nil {
		t.Fatalf("Write(second) error = %v, want it written with the earlier entry carried", err)
	}
	if len(written.Report.BlockedOnAHuman) != 1 || written.Report.BlockedOnAHuman[0].Item != "yoyodyne-ifd.9" || !written.Report.BlockedOnAHuman[0].Carried {
		t.Fatalf("entries = %+v, want only the earlier entry, carried", written.Report.BlockedOnAHuman)
	}
	if refused := strings.Join(written.Report.Refused, "\n"); !strings.Contains(refused, "cannot tell a secret or account only a person holds") {
		t.Fatalf("refused %q, want the newly written bare entry refused", refused)
	}
	if history, err := store.History("factory"); err != nil || len(history) != 2 {
		t.Fatalf("History() = %d versions, %v; want both", len(history), err)
	}
}
