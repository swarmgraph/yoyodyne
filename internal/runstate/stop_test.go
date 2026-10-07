package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// The request is written by the operator's process and read by whichever process
// is working on the run, which is the whole point: the operator does not hold
// that run's lease, so they state the fact beside it and the run reads it.
func TestAStopRequestIsReadableByTheProcessWorkingOnTheRun(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStopStore(t, root)
	runID := "run-" + repeatHex(32)
	if _, requested, err := store.StopRequested(runID); err != nil || requested {
		t.Fatalf("StopRequested() = %t, %v, want nothing asked of a fresh run", requested, err)
	}

	request := StopRequest{
		SchemaVersion: StopSchemaVersion,
		ProductID:     domain.ProductID("yoyodyne"),
		RunID:         runID,
		WorkItemID:    "yoyodyne-ifd.26",
		RequestedAt:   time.Date(2026, 8, 18, 18, 15, 0, 0, time.UTC),
		Reason:        "it is rewriting the wrong file",
	}
	if err := store.RecordStop(request); err != nil {
		t.Fatalf("RecordStop() error = %v", err)
	}
	// A separate store over the same root is what the working process is.
	read, requested, err := newStopStore(t, root).StopRequested(runID)
	if err != nil || !requested {
		t.Fatalf("StopRequested() = %t, %v, want the recorded request", requested, err)
	}
	if read.WorkItemID != request.WorkItemID || read.Reason != request.Reason {
		t.Fatalf("StopRequested() = %#v, want the request as it was made", read)
	}

	// Asking twice means the same thing the second time. An operator cannot see
	// whether the run has reached a boundary yet, so refusing them would make the
	// verb depend on timing they have no way to know about.
	if err := store.RecordStop(request); err != nil {
		t.Fatalf("second RecordStop() error = %v", err)
	}

	// The request names one run, so it must never be readable as another's: a
	// stop that leaked across runs would end work nobody asked about.
	other := "run-" + repeatHex(31) + "b"
	if _, requested, err := store.StopRequested(other); err != nil || requested {
		t.Fatalf("StopRequested() on another run = %t, %v, want nothing asked of it", requested, err)
	}
}

// A run's stop request lives beside its state in the same directory, so the
// scan that discovers runs must keep ignoring it. A record mistaken for a run
// would be a run nobody could load.
func TestAStopRequestIsNotMistakenForARun(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStopStore(t, root)
	if err := store.RecordStop(StopRequest{
		SchemaVersion: StopSchemaVersion,
		ProductID:     domain.ProductID("yoyodyne"),
		RunID:         "run-" + repeatHex(32),
		WorkItemID:    "yoyodyne-ifd.26",
		RequestedAt:   time.Date(2026, 8, 18, 18, 15, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("RecordStop() error = %v", err)
	}
	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 0 {
		t.Fatalf("Incomplete() = %#v, want the request left out of the runs", incomplete)
	}
}

// A request that cannot be read is an error rather than an absence, for the
// reason every other record here is: reported as absent it would leave a run
// carrying on that somebody has already stopped.
func TestAnUnreadableStopRequestIsNotReportedAsAbsent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStopStore(t, root)
	runID := "run-" + repeatHex(32)
	directory := filepath.Join(home.ProductDirectory(root, "yoyodyne"), "runs")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for name, content := range map[string]string{
		"not JSON at all":       "stop\n",
		"a request for nothing": `{"schema_version":1,"product_id":"yoyodyne","run_id":"` + runID + `"}`,
		"an unsupported schema": `{"schema_version":2,"product_id":"yoyodyne","run_id":"` + runID + `","work_item_id":"a","requested_at":"2026-08-18T18:15:00Z"}`,
		"a key nothing writes":  `{"schema_version":1,"product_id":"yoyodyne","run_id":"` + runID + `","work_item_id":"a","requested_at":"2026-08-18T18:15:00Z","force":true}`,
	} {
		if err := os.WriteFile(filepath.Join(directory, runID+".stop.json"), []byte(content), 0o600); err != nil {
			t.Fatalf("%s: WriteFile() error = %v", name, err)
		}
		if _, requested, err := store.StopRequested(runID); err == nil || requested {
			t.Fatalf("%s: StopRequested() = %t, %v, want a refusal to read it as absent", name, requested, err)
		}
	}
}

// A request recorded against another product would be acted on by a harness
// nobody spoke to, so the store refuses it rather than writing it.
func TestAStopRequestForAnotherProductIsRefused(t *testing.T) {
	t.Parallel()

	store := newStopStore(t, t.TempDir())
	if err := store.RecordStop(StopRequest{
		SchemaVersion: StopSchemaVersion,
		ProductID:     domain.ProductID("elsewhere"),
		RunID:         "run-" + repeatHex(32),
		WorkItemID:    "yoyodyne-ifd.26",
		RequestedAt:   time.Date(2026, 8, 18, 18, 15, 0, 0, time.UTC),
	}); err == nil {
		t.Fatal("RecordStop() accepted a request for another product")
	}
}

func newStopStore(t *testing.T, root string) *Store {
	t.Helper()
	store, err := NewStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

// repeatHex builds a run identifier's hexadecimal half, so a test can name a run
// without a record of one existing.
func repeatHex(length int) string {
	digits := make([]byte, length)
	for index := range digits {
		digits[index] = "0123456789abcdef"[index%16]
	}
	return string(digits)
}

// A stop somebody other than the operator asked for names them, and one that
// carries out a decision names the decision and who made it. An operator's own
// stop names nobody and is read as theirs, which is every request written before
// anybody else could make one.
func TestAStopRequestSaysWhoAskedForIt(t *testing.T) {
	t.Parallel()

	operator := StopRequest{
		SchemaVersion: StopSchemaVersion, ProductID: "yoyodyne", RunID: "run-0123456789abcdef0123456789abcdef",
		WorkItemID: "yoyodyne-ifd.428.34", RequestedAt: time.Now(),
	}
	if err := operator.Validate(); err != nil || operator.StoppedBy() != "the operator" {
		t.Fatalf("operator stop = %v, %q", err, operator.StoppedBy())
	}
	decided := operator
	decided.RequestedBy = "the development manager in conversation chat-0123456789abcdef"
	decided.Decision = TriageDecisionStop
	if err := decided.Validate(); err != nil || decided.StoppedBy() != decided.RequestedBy {
		t.Fatalf("decided stop = %v, %q", err, decided.StoppedBy())
	}
	unnamed := operator
	unnamed.Decision = TriageDecisionStop
	if err := unnamed.Validate(); err == nil || !strings.Contains(err.Error(), "names who decided it") {
		t.Fatalf("a decided stop naming nobody: %v", err)
	}
	other := decided
	other.Decision = TriageDecisionRerun
	if err := other.Validate(); err == nil {
		t.Fatal("a stop request carrying out a re-run was accepted")
	}
}
