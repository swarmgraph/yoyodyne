package beads

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// fromReport is an origin with every field set: work a report asked for, which
// a sweep admitted in answer to the operator's directive.
var fromReport = domain.WorkItemOrigin{
	Asker:      domain.AskerReport,
	AdmittedBy: domain.RoleProductManager,
	Report:     "report-e228624206399b553ab9b161b31bbc84",
	ReportedBy: domain.RoleDevelopmentManager,
	Directive:  "directive-1",
}

// An admission's origin is written in the creation's one metadata object, as one
// fact to a key, and read back; an origin bd did not store is a failure rather
// than an item that silently reads as unknown.
func TestTheOriginIsWrittenInTheAdmissionAndReadBack(t *testing.T) {
	t.Parallel()

	created := `{"id":"yoyodyne-ifd.1","title":"Read the whole item","status":"open","priority":2,"issue_type":"task","metadata":{` +
		`"yoyodyne_origin":"report","yoyodyne_origin_asked_by":"development-manager","yoyodyne_origin_on_behalf_of":"operator",` +
		`"yoyodyne_origin_admitted_by":"product-manager","yoyodyne_origin_report":"report-e228624206399b553ab9b161b31bbc84",` +
		`"yoyodyne_origin_reported_by":"development-manager","yoyodyne_origin_directive":"directive-1"}}`
	runner := &fakeRunner{responses: []string{created}}
	item, err := (Client{Runner: runner}).Create(context.Background(), NewWorkItem{
		Title: "Read the whole item", Description: "d", Type: "task", Origin: fromReport,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if item.Origin != fromReport {
		t.Fatalf("Create() origin = %#v, want %#v", item.Origin, fromReport)
	}
	var metadata string
	for _, argument := range runner.args[0] {
		if strings.HasPrefix(argument, "--metadata=") {
			metadata = argument
		}
	}
	for _, want := range []string{
		`"yoyodyne_origin":"report"`,
		`"yoyodyne_origin_asked_by":"development-manager"`,
		`"yoyodyne_origin_on_behalf_of":"operator"`,
		`"yoyodyne_origin_admitted_by":"product-manager"`,
		`"yoyodyne_origin_report":"report-e228624206399b553ab9b161b31bbc84"`,
		`"yoyodyne_origin_reported_by":"development-manager"`,
		`"yoyodyne_origin_directive":"directive-1"`,
	} {
		if !strings.Contains(metadata, want) {
			t.Fatalf("creation metadata = %q, want it to carry %s", metadata, want)
		}
	}

	unstored := &fakeRunner{responses: []string{`{"id":"yoyodyne-ifd.1","title":"Read the whole item","status":"open","priority":2,"issue_type":"task"}`}}
	if _, err := (Client{Runner: unstored}).Create(context.Background(), NewWorkItem{
		Title: "Read the whole item", Description: "d", Type: "task", Origin: fromReport,
	}); err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("Create() with an origin bd did not store error = %v, want a failure naming the origin", err)
	}

	// An origin that could not describe a real admission is refused before bd is
	// asked anything.
	refusing := &fakeRunner{}
	if _, err := (Client{Runner: refusing}).Create(context.Background(), NewWorkItem{
		Title: "t", Description: "d", Type: "task", Origin: domain.WorkItemOrigin{Asker: domain.AskerReport, AdmittedBy: domain.RoleProductManager},
	}); err == nil || len(refusing.args) != 0 {
		t.Fatalf("Create() with a report origin naming no report error = %v, calls = %d, want it refused unasked", err, len(refusing.args))
	}

	// A creation with no origin writes no origin key, which is what a
	// decomposition leaves.
	plain := &fakeRunner{responses: []string{`{"id":"yoyodyne-1","title":"t","status":"open","priority":1,"issue_type":"task"}`}}
	if _, err := (Client{Runner: plain}).Create(context.Background(), NewWorkItem{Title: "t", Description: "d", Type: "task"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, argument := range plain.args[0] {
		if strings.Contains(argument, "yoyodyne_origin") {
			t.Fatalf("a creation with no origin carried %q", argument)
		}
	}
}

// An item admitted before origins were recorded reads as unknown, and the
// listing carries the origin of every item that has one.
func TestTheListingCarriesEachItemsOrigin(t *testing.T) {
	t.Parallel()

	listed := `[{"id":"yoyodyne-1","title":"Old","status":"open","priority":1,"issue_type":"task"},
	            {"id":"yoyodyne-2","title":"New","status":"open","priority":1,"issue_type":"task",
	             "metadata":{"yoyodyne_origin":"sweep","yoyodyne_origin_asked_by":"product-manager","yoyodyne_origin_on_behalf_of":"product-manager","yoyodyne_origin_admitted_by":"product-manager"}}]`
	items, err := (Client{Runner: &fakeRunner{responses: []string{listed}}}).List(context.Background(), "open")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 2 || items[0].Origin.Known() {
		t.Fatalf("List() = %#v, want the old item's origin unknown", items)
	}
	if want := (domain.WorkItemOrigin{Asker: domain.AskerSweep, AdmittedBy: domain.RoleProductManager}); items[1].Origin != want {
		t.Fatalf("listed origin = %#v, want %#v", items[1].Origin, want)
	}
}

// The backfill's write sets an origin on an item that has none, one key at a
// time, and never rewrites one an item already records.
func TestRecordOriginSetsAnOriginOnceAndNeverRewritesOne(t *testing.T) {
	t.Parallel()

	origin := domain.WorkItemOrigin{Asker: domain.AskerOperator, AdmittedBy: domain.RoleProductManager, Directive: "directive-1"}
	runner := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-1","title":"t","status":"closed","priority":1,"issue_type":"task"}]`,
		`[{"id":"yoyodyne-1","title":"t","status":"closed","priority":1,"issue_type":"task","metadata":{"yoyodyne_origin":"operator","yoyodyne_origin_admitted_by":"product-manager","yoyodyne_origin_directive":"directive-1"}}]`,
	}}
	item, err := (Client{Runner: runner}).RecordOrigin(context.Background(), "yoyodyne-1", origin)
	if err != nil {
		t.Fatalf("RecordOrigin() error = %v", err)
	}
	if item.Origin != origin {
		t.Fatalf("RecordOrigin() origin = %#v, want %#v", item.Origin, origin)
	}
	for _, want := range []string{
		"--set-metadata=yoyodyne_origin=operator",
		"--set-metadata=yoyodyne_origin_asked_by=operator",
		"--set-metadata=yoyodyne_origin_on_behalf_of=operator",
		"--set-metadata=yoyodyne_origin_admitted_by=product-manager",
		"--set-metadata=yoyodyne_origin_directive=directive-1",
	} {
		if !slices.Contains(runner.args[1], want) {
			t.Fatalf("update = %#v, want %s", runner.args[1], want)
		}
	}

	recorded := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-1","title":"t","status":"closed","priority":1,"issue_type":"task","metadata":{"yoyodyne_origin":"sweep","yoyodyne_origin_admitted_by":"product-manager"}}]`,
	}}
	if _, err := (Client{Runner: recorded}).RecordOrigin(context.Background(), "yoyodyne-1", origin); err == nil || !strings.Contains(err.Error(), "never rewritten") {
		t.Fatalf("RecordOrigin() over a recorded origin error = %v, want it refused", err)
	}
	if len(recorded.args) != 1 {
		t.Fatalf("calls = %#v, want only the read", recorded.args)
	}
}

// TestOriginMetadataConformance checks against a real bd what the fakes above
// can only restate: that a creation's origin keys are stored and given back by
// the creation, a read, and a listing, and that the backfill's one-key-at-a-time
// spelling stores the same thing.
func TestOriginMetadataConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	created, err := client.Create(ctx, NewWorkItem{Title: "Admitted from a report", Description: "d", Type: "task", Origin: fromReport})
	if err != nil {
		t.Fatalf("Create() with an origin error = %v", err)
	}
	if created.Origin != fromReport {
		t.Fatalf("Create() origin = %#v, want bd to echo %#v", created.Origin, fromReport)
	}
	shown, err := client.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if shown.Origin != fromReport {
		t.Fatalf("Show() origin = %#v, want %#v", shown.Origin, fromReport)
	}

	old, err := client.Create(ctx, NewWorkItem{Title: "Admitted before origins", Description: "d", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	listed, err := client.List(ctx, "open")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, item := range listed {
		switch item.ID {
		case created.ID:
			if item.Origin != fromReport {
				t.Fatalf("listed origin = %#v, want %#v", item.Origin, fromReport)
			}
		case old.ID:
			if item.Origin.Known() {
				t.Fatalf("listed origin of an item admitted without one = %#v, want unknown", item.Origin)
			}
		}
	}

	backfilled := domain.WorkItemOrigin{Asker: domain.AskerOperator, AdmittedBy: domain.RoleProductManager, Directive: "directive-2"}
	if _, err := client.RecordOrigin(ctx, old.ID, backfilled); err != nil {
		t.Fatalf("RecordOrigin() error = %v", err)
	}
	reread, err := client.Show(ctx, old.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if reread.Origin != backfilled {
		t.Fatalf("Show() origin after the backfill = %#v, want %#v", reread.Origin, backfilled)
	}
}
