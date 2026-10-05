package beads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

func TestClientWorkItemLifecycle(t *testing.T) {
	t.Parallel()

	responses := []string{
		workItemJSON("open", ""),
		workItemJSON("in_progress", ""),
		workItemJSON("in_progress", "checks passed"),
		`{"issue_id":"yoyodyne-1","depends_on_id":"yoyodyne-blocker","status":"added"}`,
		workItemJSON("closed", "checks passed"),
	}
	runner := &fakeRunner{responses: responses}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}

	item, err := client.Show(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if item.ID != "yoyodyne-1" || len(item.Dependencies) != 1 || item.Dependencies[0].ID != "yoyodyne-parent" || item.Dependencies[0].Status != "closed" {
		t.Fatalf("Show() = %#v", item)
	}
	if _, _, err := client.Claim(context.Background(), item.ID); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := client.RecordOutcome(context.Background(), item.ID, "checks passed"); err != nil {
		t.Fatalf("RecordOutcome() error = %v", err)
	}
	if err := client.AddBlocker(context.Background(), item.ID, "yoyodyne-blocker"); err != nil {
		t.Fatalf("AddBlocker() error = %v", err)
	}
	if _, err := client.Complete(context.Background(), item.ID, "done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	wantArgs := [][]string{
		{"show", "yoyodyne-1", "--json"},
		{"update", "yoyodyne-1", "--claim", "--json"},
		{"update", "yoyodyne-1", "--append-notes=" + FrameNote("checks passed"), "--json"},
		{"dep", "add", "yoyodyne-1", "yoyodyne-blocker", "--json"},
		{"close", "yoyodyne-1", "--reason=done", "--json"},
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}
}

func TestClientBlocksAnItemAndVerifiesTheStatusItApplied(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{responses: []string{workItemJSON("blocked", "unresolved review findings")}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	item, err := client.Block(context.Background(), "yoyodyne-1", "unresolved review findings")
	if err != nil {
		t.Fatalf("Block() error = %v", err)
	}
	if item.Status != "blocked" || item.Notes != "unresolved review findings" {
		t.Fatalf("Block() = %#v", item)
	}
	wantArgs := [][]string{{"update", "yoyodyne-1", "--status=blocked", "--append-notes=" + FrameNote("unresolved review findings"), "--json"}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	// A blocker that was not actually applied must not read as recorded.
	unapplied := &fakeRunner{responses: []string{workItemJSON("in_progress", "unresolved review findings")}}
	if _, err := (Client{Runner: unapplied}).Block(context.Background(), "yoyodyne-1", "findings"); err == nil || !strings.Contains(err.Error(), "want blocked") {
		t.Fatalf("Block() unapplied error = %v", err)
	}
	if _, err := (Client{Runner: &fakeRunner{}}).Block(context.Background(), "yoyodyne-1", " "); err == nil {
		t.Fatal("Block() empty reason error = nil")
	}
}

// Clearing a status is the other end of blocking one, and it is verified the
// same way: an item still reading blocked afterwards is unclaimable, with a note
// on it saying it was released.
func TestClientClearsABlockedStatusAndVerifiesTheStatusItApplied(t *testing.T) {
	t.Parallel()

	note := "the run that blocked it ended and nothing unfinished is behind it"
	runner := &fakeRunner{responses: []string{workItemJSON("open", note)}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	item, err := client.Unblock(context.Background(), "yoyodyne-1", note)
	if err != nil {
		t.Fatalf("Unblock() error = %v", err)
	}
	if item.Status != "open" || item.Notes != note {
		t.Fatalf("Unblock() = %#v", item)
	}
	wantArgs := [][]string{{"update", "yoyodyne-1", "--status=open", "--append-notes=" + FrameNote(note), "--json"}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	unapplied := &fakeRunner{responses: []string{workItemJSON("blocked", note)}}
	if _, err := (Client{Runner: unapplied}).Unblock(context.Background(), "yoyodyne-1", note); err == nil ||
		!strings.Contains(err.Error(), "want open") {
		t.Fatalf("Unblock() unapplied error = %v", err)
	}
	// A status cleared with nothing recorded about why is a change nobody can
	// account for afterwards, which is the whole of what makes this reviewable.
	if _, err := (Client{Runner: &fakeRunner{}}).Unblock(context.Background(), "yoyodyne-1", " "); err == nil {
		t.Fatal("Unblock() empty note error = nil")
	}
}

// An item a run integrated a change for and did not discharge goes back to the
// backlog carrying why, and under whatever parking the caller decided. The status
// is read back for the reason a blocker's is: an item left claimed by a run that
// has ended is work nothing can start and nothing is watching. The parking is
// read back because an item returned unparked when a parking was asked for is one
// the next pull selects again for another run of the same diagnosis.
func TestClientReopensAnItemAndVerifiesTheStatusAndParkingItApplied(t *testing.T) {
	t.Parallel()

	reason := "run-1 landed evidence and did not discharge this item"
	parking := domain.WorkItemParking("parked by run-1, which found the design it needs has not landed")
	runner := &fakeRunner{responses: []string{reopenedItemJSON("open", reason, string(parking))}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	item, err := client.Reopen(context.Background(), "yoyodyne-1", reason, parking)
	if err != nil {
		t.Fatalf("Reopen() error = %v", err)
	}
	if item.Status != "open" || item.Notes != reason || item.Parking != parking {
		t.Fatalf("Reopen() = %#v", item)
	}
	// The parking travels in the same invocation as the status. Between two of
	// them the item is open and unparked, which is exactly when a watch session
	// polling the queue pulls it.
	wantArgs := [][]string{{"update", "yoyodyne-1", "--status=open", "--append-notes=" + FrameNote(reason),
		"--set-metadata=yoyodyne_parked=" + string(parking), "--json"}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	unapplied := &fakeRunner{responses: []string{reopenedItemJSON("in_progress", reason, string(parking))}}
	if _, err := (Client{Runner: unapplied}).Reopen(context.Background(), "yoyodyne-1", reason, parking); err == nil ||
		!strings.Contains(err.Error(), "want open") {
		t.Fatalf("Reopen() unapplied error = %v", err)
	}
	// A parking that did not take is the failure this call exists to catch, and
	// the item looks perfectly ordinary afterwards.
	unparked := &fakeRunner{responses: []string{reopenedItemJSON("open", reason, "")}}
	if _, err := (Client{Runner: unparked}).Reopen(context.Background(), "yoyodyne-1", reason, parking); err == nil ||
		!strings.Contains(err.Error(), "after being reopened") {
		t.Fatalf("Reopen() unparked error = %v", err)
	}
	// And so is a parking left behind by a caller that asked for none, because the
	// item is then held back by a decision that is over.
	stillParked := &fakeRunner{responses: []string{reopenedItemJSON("open", reason, string(parking))}}
	if _, err := (Client{Runner: stillParked}).Reopen(context.Background(), "yoyodyne-1", reason, ""); err == nil ||
		!strings.Contains(err.Error(), "still parked") {
		t.Fatalf("Reopen() still-parked error = %v", err)
	}
	// An item put back with no reason reads afterwards as work somebody walked
	// away from, which is the state this call exists to avoid.
	if _, err := (Client{Runner: &fakeRunner{}}).Reopen(context.Background(), "yoyodyne-1", " ", parking); err == nil {
		t.Fatal("Reopen() empty reason error = nil")
	}
	// A parking reason the tracker could not hold as one value on one line is
	// refused before the write, the same way an update's is.
	if _, err := (Client{Runner: &fakeRunner{}}).Reopen(context.Background(), "yoyodyne-1", reason,
		domain.WorkItemParking("first line\nsecond line")); err == nil {
		t.Fatal("Reopen() multi-line parking error = nil")
	}
}

// reopenedItemJSON is one work item as bd reports it after a reopen: the status
// and the parking metadata the call reads back.
func reopenedItemJSON(status, notes, parking string) string {
	return fmt.Sprintf(`[{
  "id":"yoyodyne-1",
  "title":"Implement feature",
  "notes":%q,
  "status":%q,
  "priority":1,
  "issue_type":"task",
  "metadata":{"yoyodyne_parked":%q}
}]`, notes, status, parking)
}

func TestClientReleasesAClaimAndVerifiesTheStatusItApplied(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{responses: []string{workItemJSON("open", "the harness gave this item back")}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	item, err := client.Release(context.Background(), "yoyodyne-1", "the harness gave this item back")
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if item.Status != "open" || item.Notes != "the harness gave this item back" {
		t.Fatalf("Release() = %#v, want the item back in the queue", item)
	}
	wantArgs := [][]string{{"update", "yoyodyne-1", "--status=open", "--append-notes=" + FrameNote("the harness gave this item back"), "--json"}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	// A release that did not take leaves work nothing will ever pull, which is the
	// direction that goes unnoticed, so it is a failure rather than a report of
	// success.
	unapplied := &fakeRunner{responses: []string{workItemJSON("in_progress", "given back")}}
	if _, err := (Client{Runner: unapplied}).Release(context.Background(), "yoyodyne-1", "given back"); err == nil || !strings.Contains(err.Error(), "want open") {
		t.Fatalf("Release() unapplied error = %v", err)
	}
	if _, err := (Client{Runner: &fakeRunner{}}).Release(context.Background(), "yoyodyne-1", " "); err == nil {
		t.Fatal("Release() empty reason error = nil")
	}
}

func TestClientAppliesOnlyTheEditItWasGiven(t *testing.T) {
	t.Parallel()

	priority := 0
	parent := "yoyodyne-ifd.12"
	detached := ""
	runner := &fakeRunner{responses: []string{
		// The notes bd answers with carry what was appended, which is what makes the
		// confirmation this update reports a confirmation of anything.
		`[{"id":"yoyodyne-1","title":"Readable conversations","description":"Say who is speaking.",` +
			`"status":"open","priority":2,"issue_type":"task",` +
			`"notes":"Admitted long ago.\n\nRenamed by the product manager."}]`,
		`[{"id":"yoyodyne-1","title":"Implement feature","status":"open","priority":0,"issue_type":"task"}]`,
		workItemJSON("open", ""),
		`{"issue_id":"yoyodyne-1","depends_on_id":"yoyodyne-blocker","status":"removed"}`,
	}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}

	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{
		Title:       "Readable conversations",
		Description: "Say who is speaking.",
		AppendNotes: "Renamed by the product manager.",
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{Priority: &priority, Parent: &parent}); err != nil {
		t.Fatalf("Update() priority error = %v", err)
	}
	// An empty parent detaches the item, which is a different request from
	// saying nothing about the parent at all.
	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{Parent: &detached}); err != nil {
		t.Fatalf("Update() detach error = %v", err)
	}
	if err := client.RemoveBlocker(context.Background(), "yoyodyne-1", "yoyodyne-blocker"); err != nil {
		t.Fatalf("RemoveBlocker() error = %v", err)
	}

	wantArgs := [][]string{
		{"update", "yoyodyne-1", "--title=Readable conversations", "--description=Say who is speaking.", "--append-notes=" + FrameNote("Renamed by the product manager."), "--json"},
		{"update", "yoyodyne-1", "--priority=0", "--parent=yoyodyne-ifd.12", "--json"},
		{"update", "yoyodyne-1", "--parent=", "--json"},
		{"dep", "remove", "yoyodyne-1", "yoyodyne-blocker", "--json"},
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	// An edit bd did not actually apply must not read as applied.
	unapplied := &fakeRunner{responses: []string{`[{"id":"yoyodyne-1","title":"Something else","status":"open","priority":2,"issue_type":"task"}]`}}
	if _, err := (Client{Runner: unapplied}).Update(context.Background(), "yoyodyne-1", WorkItemChange{Title: "Readable conversations"}); err == nil ||
		!strings.Contains(err.Error(), "want \"Readable conversations\"") {
		t.Fatalf("Update() unapplied title error = %v", err)
	}
	unmoved := &fakeRunner{responses: []string{`[{"id":"yoyodyne-1","title":"t","status":"open","priority":3,"issue_type":"task"}]`}}
	if _, err := (Client{Runner: unmoved}).Update(context.Background(), "yoyodyne-1", WorkItemChange{Priority: &priority}); err == nil ||
		!strings.Contains(err.Error(), "want 0") {
		t.Fatalf("Update() unapplied priority error = %v", err)
	}
	// A dependency the tracker did not report removing is still a dependency.
	unremoved := &fakeRunner{responses: []string{`{"issue_id":"yoyodyne-1","depends_on_id":"yoyodyne-blocker","status":"added"}`}}
	if err := (Client{Runner: unremoved}).RemoveBlocker(context.Background(), "yoyodyne-1", "yoyodyne-blocker"); err == nil ||
		!strings.Contains(err.Error(), "unexpected bd dependency response") {
		t.Fatalf("RemoveBlocker() unapplied error = %v", err)
	}
}

func TestClientRefusesAnEditItCannotApply(t *testing.T) {
	t.Parallel()

	tooLow, tooHigh := -1, MaxPriority+1
	badParent := "../etc"
	for _, test := range []struct {
		name   string
		id     string
		change WorkItemChange
		want   string
	}{
		{name: "invented id", id: "../etc", change: WorkItemChange{Title: "t"}, want: "invalid Beads issue id"},
		{name: "nothing to change", id: "yoyodyne-1", want: "must change something"},
		{name: "priority below the scale", id: "yoyodyne-1", change: WorkItemChange{Priority: &tooLow}, want: "outside 0.."},
		{name: "priority above the scale", id: "yoyodyne-1", change: WorkItemChange{Priority: &tooHigh}, want: "outside 0.."},
		{name: "invented parent", id: "yoyodyne-1", change: WorkItemChange{Parent: &badParent}, want: "invalid parent"},
		{name: "title spanning lines", id: "yoyodyne-1", change: WorkItemChange{Title: "t\n--force"}, want: "cannot span lines"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{}
			if _, err := (Client{Runner: runner}).Update(context.Background(), test.id, test.change); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Update() error = %v, want it to contain %q", err, test.want)
			}
			if len(runner.args) != 0 {
				t.Fatalf("a refused edit still ran bd %#v", runner.args)
			}
		})
	}
}

func TestClientListsWorkItemsWithoutChangingAnything(t *testing.T) {
	t.Parallel()

	// Notes ride along on a listing: every attribution-reporting path reads
	// them off List results, so a List that dropped them would report the
	// whole queue as naming no goal. The real bd emits notes in list --json;
	// this pins that the client keeps them.
	listed := `[{"id":"yoyodyne-1","title":"First","status":"open","priority":1,"issue_type":"task","notes":"Goal: maintain the traceable chain."},
	            {"id":"yoyodyne-2","title":"Second","status":"open","priority":2,"issue_type":"feature"}]`
	runner := &fakeRunner{responses: []string{listed, `[]`}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}

	items, err := client.List(context.Background(), "open")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 2 || items[0].ID != "yoyodyne-1" || items[1].IssueType != "feature" {
		t.Fatalf("List() = %#v", items)
	}
	if items[0].Notes != "Goal: maintain the traceable chain." {
		t.Fatalf("List() dropped notes: %#v", items[0])
	}
	empty, err := client.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List() unfiltered error = %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("List() unfiltered = %#v", empty)
	}
	// The cap is lifted on every listing: bd's default is a page of fifty, and a
	// reading that decides over the whole set cannot be handed a page of it. An
	// unfiltered listing asks for closed work too, which bd leaves out unasked.
	wantArgs := [][]string{
		{"list", "--json", "--limit=0", "--status=open"},
		{"list", "--json", "--limit=0", "--all"},
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	// A status is a filter, never an argument smuggled onto the command line.
	if _, err := (Client{Runner: &fakeRunner{}}).List(context.Background(), "--dangerous"); err == nil {
		t.Fatal("List() invalid status error = nil")
	}
}

// A bd release without --limit refuses every listing for the flag, before it
// opens the store, and a harness whose every listing fails makes no runs. So
// the listing is asked again without the flag, and only for that refusal: this
// puts the client over a bd that is a real process and rejects the flag the way
// cobra does, and checks that the rows come back, that the flag was tried
// first, and that a bd failing for any other reason is not asked twice.
func TestClientListsWithoutTheLimitFlagWhereBDRefusesIt(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	calls := filepath.Join(directory, "calls")
	bd := filepath.Join(directory, "bd")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + calls + "\n" +
		"for arg in \"$@\"; do\n" +
		"  case \"$arg\" in\n" +
		"    --status=blocked) echo 'Error: failed to open database: locked' >&2; exit 1 ;;\n" +
		"  esac\n" +
		"done\n" +
		"for arg in \"$@\"; do\n" +
		"  case \"$arg\" in\n" +
		"    --limit=*) echo 'Error: unknown flag: --limit' >&2; exit 1 ;;\n" +
		"  esac\n" +
		"done\n" +
		"echo '[{\"id\":\"yoyodyne-1\",\"title\":\"First\",\"status\":\"open\",\"priority\":1,\"issue_type\":\"task\"}]'\n"
	if err := os.WriteFile(bd, []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile(bd) error = %v", err)
	}
	client := Client{Runner: execution.OSProcessRunner{}, Binary: bd, Dir: directory, Timeout: 30 * time.Second}

	items, err := client.List(context.Background(), "open")
	if err != nil {
		t.Fatalf("List() over a bd without --limit error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "yoyodyne-1" {
		t.Fatalf("List() over a bd without --limit = %#v, want the one row it holds", items)
	}
	if _, err := client.List(context.Background(), "blocked"); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("List() over a bd refusing for the store error = %v, want the store's refusal", err)
	}
	recorded, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("ReadFile(calls) error = %v", err)
	}
	want := "list --json --limit=0 --status=open\n" +
		"list --json --status=open\n" +
		"list --json --limit=0 --status=blocked\n"
	if string(recorded) != want {
		t.Fatalf("bd was invoked as:\n%swant:\n%s", recorded, want)
	}
}

func TestClientReadsWhenAnItemWasAdmittedAndSaysNothingWhenItCannot(t *testing.T) {
	t.Parallel()

	// When an item was admitted is what says which wording the work was pulled
	// under, so a listing that dropped it would report every item as one nothing
	// upstream can be compared against. The real bd emits it as RFC 3339.
	listed := `[{"id":"yoyodyne-1","title":"First","status":"open","priority":1,"issue_type":"task","created_at":"2026-08-17T03:55:26Z"},
	            {"id":"yoyodyne-2","title":"Second","status":"open","priority":2,"issue_type":"task"},
	            {"id":"yoyodyne-3","title":"Third","status":"open","priority":2,"issue_type":"task","created_at":"last Tuesday"}]`
	client := Client{Runner: &fakeRunner{responses: []string{listed}}, Binary: "bd-test", Dir: "/repo"}

	items, err := client.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("List() = %#v", items)
	}
	if want := time.Date(2026, 8, 17, 3, 55, 26, 0, time.UTC); !items[0].CreatedAt.Equal(want) {
		t.Fatalf("CreatedAt = %v, want %v", items[0].CreatedAt, want)
	}
	// A tracker that says nothing, and one that says something this cannot read,
	// both leave the admission time unknown. Neither costs the read: the item is
	// still the item, and whatever needs the time reports that it does not have it.
	if !items[1].CreatedAt.IsZero() || !items[2].CreatedAt.IsZero() {
		t.Fatalf("List() = %#v, want an unknown admission time rather than a guessed one", items[1:])
	}
}

// What can be pulled is a question the tracker answers from its own dependency
// graph. The payload below is what bd actually returns, dependency shape and
// all: the relation is recorded with no completion state on it, which is exactly
// why readiness is asked for rather than worked out from a listing.
func TestClientAsksTheTrackerWhatIsReadyRatherThanWorkingItOut(t *testing.T) {
	t.Parallel()

	ready := `[{"id":"bdprobe-uxm","title":"Waiting item","description":"d","status":"open","priority":0,
	            "issue_type":"task","owner":"someone@example.com",
	            "dependencies":[{"issue_id":"bdprobe-uxm","depends_on_id":"bdprobe-3kw","type":"blocks",
	                             "created_by":"Someone","metadata":"{}"}],
	            "dependency_count":1,"dependent_count":0,"comment_count":0}]`
	runner := &fakeRunner{responses: []string{ready}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}

	items, err := client.Ready(context.Background())
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "bdprobe-uxm" {
		t.Fatalf("Ready() = %#v", items)
	}
	// The dependency decodes from the spelling bd uses, and carries no status,
	// which is the fact the backlog is built around.
	if len(items[0].Dependencies) != 1 {
		t.Fatalf("dependencies = %#v", items[0].Dependencies)
	}
	dependency := items[0].Dependencies[0]
	if dependency.ID != "bdprobe-3kw" || dependency.Type != "blocks" || dependency.Status != "" {
		t.Fatalf("dependency = %#v", dependency)
	}
	if !reflect.DeepEqual(runner.args, [][]string{{"ready", "--json"}}) {
		t.Fatalf("bd args = %#v", runner.args)
	}
}

// Decomposition can reach this reading as an edge and nothing else, and the
// reading that matters is the one that finds it there. The payload is that case
// rather than the whole of what bd answers — no parent field anywhere in it, and
// the parent named by a parent-child edge attributed to the child itself — which
// is how the tracker's own export states parentage. What a real listing states
// is pinned by the capture in TestACapturedListingStatesParentageAsAnEdgeAttributedToTheChild.
func TestClientReadsDecompositionStatedAsAnEdgeRatherThanAField(t *testing.T) {
	t.Parallel()

	child := `[{"id":"yoyodyne-ifd.121.2","title":"Execute the README split","description":"d","status":"open",
	            "priority":1,"issue_type":"task",
	            "dependencies":[{"issue_id":"yoyodyne-ifd.121.2","depends_on_id":"yoyodyne-ifd.121",
	                             "type":"parent-child","metadata":"{}"},
	                            {"issue_id":"yoyodyne-ifd.121.2","depends_on_id":"yoyodyne-ifd.121.1",
	                             "type":"blocks","metadata":"{}"}]}]`
	runner := &fakeRunner{responses: []string{child}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}

	items, err := client.Ready(context.Background())
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Ready() = %#v", items)
	}
	item := items[0]
	// The field really is absent: this is the case a reader of it alone gets
	// wrong, rather than a store that states parentage both ways.
	if item.Parent != "" {
		t.Fatalf("parent field = %q, want the payload's own answer of none", item.Parent)
	}
	if got := item.DecomposedFrom(); got != "yoyodyne-ifd.121" {
		t.Fatalf("DecomposedFrom() = %q, want the parent the edge names", got)
	}
	// Which item an edge belongs to is what tells a parent from a child, so it
	// has to survive decoding to be checkable at all.
	if item.Dependencies[0].IssueID != "yoyodyne-ifd.121.2" {
		t.Fatalf("dependency = %#v, want the item the tracker attributes it to", item.Dependencies[0])
	}
}

func TestWorkItemDecomposedFrom(t *testing.T) {
	t.Parallel()

	edge := func(issue, parent, kind string) Dependency {
		return Dependency{IssueID: issue, ID: parent, Type: kind}
	}
	for _, test := range []struct {
		name string
		item WorkItem
		want string
	}{
		{
			name: "stated as a field",
			item: WorkItem{ID: "yoyodyne-ifd.121.2", Parent: "yoyodyne-ifd.121"},
			want: "yoyodyne-ifd.121",
		},
		{
			name: "stated as an edge",
			item: WorkItem{ID: "yoyodyne-ifd.121.2", Dependencies: []Dependency{
				edge("yoyodyne-ifd.121.2", "yoyodyne-ifd.121", "parent-child")}},
			want: "yoyodyne-ifd.121",
		},
		{
			// The tracker answering directly beats the edge, so a store that
			// restates one relationship both ways cannot report two parents.
			name: "stated both ways",
			item: WorkItem{ID: "yoyodyne-ifd.121.2", Parent: "yoyodyne-ifd.121", Dependencies: []Dependency{
				edge("yoyodyne-ifd.121.2", "yoyodyne-ifd.121", "parent-child")}},
			want: "yoyodyne-ifd.121",
		},
		{
			name: "an edge the tracker did not attribute",
			item: WorkItem{ID: "yoyodyne-ifd.121.2", Dependencies: []Dependency{
				edge("", "yoyodyne-ifd.121", "parent-child")}},
			want: "yoyodyne-ifd.121",
		},
		{
			// A listing that carried the epic's children beside it must not read
			// as the epic having been broken out of one of them.
			name: "an edge belonging to another item",
			item: WorkItem{ID: "yoyodyne-ifd.121", Dependencies: []Dependency{
				edge("yoyodyne-ifd.121.2", "yoyodyne-ifd.121", "parent-child")}},
			want: "",
		},
		{
			name: "a blocker is not a parent",
			item: WorkItem{ID: "yoyodyne-ifd.121.2", Dependencies: []Dependency{
				edge("yoyodyne-ifd.121.2", "yoyodyne-ifd.121.1", "blocks")}},
			want: "",
		},
		{
			name: "work nothing was broken out of",
			item: WorkItem{ID: "yoyodyne-ifd.256"},
			want: "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.item.DecomposedFrom(); got != test.want {
				t.Fatalf("DecomposedFrom() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClientCreatesAWorkItemAndReportsTheIdentifierItGot(t *testing.T) {
	t.Parallel()

	// Creation answers with the one item it made rather than with a list.
	created := `{"id":"yoyodyne-9","title":"Pause on a usage limit","description":"Wait and resume.",
	             "notes":"Proposed in conversation chat-1","status":"open","priority":2,"issue_type":"task"}`
	runner := &fakeRunner{responses: []string{created}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	item, err := client.Create(context.Background(), NewWorkItem{
		Title:       "Pause on a usage limit",
		Description: "Wait and resume.",
		Type:        "task",
		Notes:       "Proposed in conversation chat-1",
		Parent:      "yoyodyne-ifd.12",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if item.ID != "yoyodyne-9" || item.Title != "Pause on a usage limit" {
		t.Fatalf("Create() = %#v", item)
	}
	wantArgs := [][]string{{
		"create",
		"--title=Pause on a usage limit",
		"--description=Wait and resume.",
		"--type=task",
		"--notes=Proposed in conversation chat-1",
		"--parent=yoyodyne-ifd.12",
		"--json",
	}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	// Work is admitted at a place in the backlog's order, and the highest place is
	// zero, so an unstated priority and the top of the queue cannot be the same
	// request. A creation that says nothing about it asks bd for its default.
	top := 0
	placed := &fakeRunner{responses: []string{created}}
	if _, err := (Client{Runner: placed}).Create(context.Background(), NewWorkItem{
		Title: "Pause on a usage limit", Description: "Wait and resume.", Type: "task", Priority: &top,
	}); err != nil {
		t.Fatalf("Create() with a priority error = %v", err)
	}
	wantPlaced := [][]string{{
		"create",
		"--title=Pause on a usage limit",
		"--description=Wait and resume.",
		"--type=task",
		"--priority=0",
		"--json",
	}}
	if !reflect.DeepEqual(placed.args, wantPlaced) {
		t.Fatalf("bd args = %#v, want %#v", placed.args, wantPlaced)
	}

	// An item created as something other than what was asked for is a failure:
	// the caller approved the item it described, not whatever bd produced.
	mismatched := &fakeRunner{responses: []string{`{"id":"yoyodyne-9","title":"Something else","issue_type":"task"}`}}
	if _, err := (Client{Runner: mismatched}).Create(context.Background(), NewWorkItem{Title: "Pause", Description: "d", Type: "task"}); err == nil ||
		!strings.Contains(err.Error(), "want \"Pause\"") {
		t.Fatalf("Create() mismatched title error = %v", err)
	}
	// A creation without an identifier is not a creation anyone can refer to.
	anonymous := &fakeRunner{responses: []string{`{"title":"Pause"}`}}
	if _, err := (Client{Runner: anonymous}).Create(context.Background(), NewWorkItem{Title: "Pause", Description: "d", Type: "task"}); err == nil ||
		!strings.Contains(err.Error(), "invalid Beads issue id") {
		t.Fatalf("Create() anonymous error = %v", err)
	}
}

func TestClientRefusesToCreateAnUnusableWorkItem(t *testing.T) {
	t.Parallel()

	outsideScale := MaxPriority + 1
	for _, test := range []struct {
		name string
		item NewWorkItem
		want string
	}{
		{name: "no title", item: NewWorkItem{Description: "d", Type: "task"}, want: "title is required"},
		{name: "no description", item: NewWorkItem{Title: "t", Type: "task"}, want: "description is required"},
		{name: "no type", item: NewWorkItem{Title: "t", Description: "d"}, want: "invalid Beads issue type"},
		{name: "smuggled type", item: NewWorkItem{Title: "t", Description: "d", Type: "task --force"}, want: "invalid Beads issue type"},
		{name: "invented parent", item: NewWorkItem{Title: "t", Description: "d", Type: "task", Parent: "../etc"}, want: "invalid parent"},
		{name: "priority outside the scale", item: NewWorkItem{Title: "t", Description: "d", Type: "task", Priority: &outsideScale}, want: "outside 0..4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{}
			if _, err := (Client{Runner: runner}).Create(context.Background(), test.item); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Create() error = %v, want it to contain %q", err, test.want)
			}
			if len(runner.args) != 0 {
				t.Fatalf("a refused item still ran bd %#v", runner.args)
			}
		})
	}
}

func TestClientRejectsInvalidIDsAndEmptyUpdates(t *testing.T) {
	t.Parallel()

	client := Client{Runner: &fakeRunner{}}
	if _, err := client.Show(context.Background(), "../escape"); err == nil {
		t.Fatal("Show() error = nil")
	}
	if _, err := client.RecordOutcome(context.Background(), "yoyodyne-1", " "); err == nil {
		t.Fatal("RecordOutcome() error = nil")
	}
	if err := client.AddBlocker(context.Background(), "yoyodyne-1", "bad/id"); err == nil {
		t.Fatal("AddBlocker() error = nil")
	}
	if _, err := client.Complete(context.Background(), "yoyodyne-1", ""); err == nil {
		t.Fatal("Complete() error = nil")
	}
}

func TestClientReportsProcessAndMalformedJSONErrors(t *testing.T) {
	t.Parallel()

	failed := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 2, Stderr: "blocked\n"}}}
	if _, err := (Client{Runner: failed}).Show(context.Background(), "yoyodyne-1"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("Show() failure error = %v", err)
	}

	malformed := &fakeRunner{responses: []string{"{"}}
	if _, err := (Client{Runner: malformed}).Show(context.Background(), "yoyodyne-1"); err == nil || !strings.Contains(err.Error(), "decode bd work item") {
		t.Fatalf("Show() malformed error = %v", err)
	}
}

// An id bd holds nothing under is told apart from every other refusal, by bd's
// own line for it — `Issue <id> not found` — and only where bd itself wrote it:
// a refusal that happens to say "not found" about something else, and a runner
// that could not start bd, are each a tracker that did not answer.
func TestShowTellsAMissingItemFromATrackerThatCouldNotBeRead(t *testing.T) {
	t.Parallel()

	missing := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "Issue yoyodyne-9 not found\n", Stdout: `{"error":"no issues found matching the provided IDs"}`}}}
	_, err := (Client{Runner: missing}).Show(context.Background(), "yoyodyne-9")
	if !errors.Is(err, ErrNoSuchWorkItem) || !strings.Contains(err.Error(), "Issue yoyodyne-9 not found") {
		t.Fatalf("Show() of a missing item = %v, want ErrNoSuchWorkItem carrying bd's refusal", err)
	}

	for _, stderr := range []string{
		"failed to open database: LOCK: operation not permitted\n",
		"Error: database file not found: .beads/beads.db\n",
		"Issue yoyodyne-8 not found\n",
	} {
		down := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stderr: stderr}}}
		if _, err := (Client{Runner: down}).Show(context.Background(), "yoyodyne-9"); err == nil || errors.Is(err, ErrNoSuchWorkItem) {
			t.Fatalf("Show() refused with %q = %v, want a failure that is not ErrNoSuchWorkItem", stderr, err)
		}
	}

	_, err = (Client{Runner: failingRunner{errors.New(`exec: "bd": executable file not found in $PATH`)}}).Show(context.Background(), "yoyodyne-9")
	if err == nil || errors.Is(err, ErrNoSuchWorkItem) {
		t.Fatalf("Show() with no bd = %v, want a failure that is not ErrNoSuchWorkItem", err)
	}
	if !ValidIssueID("yoyodyne-ifd.432.1") || ValidIssueID("../escape") || ValidIssueID("") {
		t.Fatal("ValidIssueID does not hold the tracker's own shape")
	}
}

// failingRunner is a runner that cannot start bd at all.
type failingRunner struct{ err error }

func (f failingRunner) Run(context.Context, execution.Command, execution.OutputObserver) (execution.ProcessResult, error) {
	return execution.ProcessResult{}, f.err
}

// The tracker is where a work item's price lives, so what is written has to be
// exactly what was priced and has to be verified against what bd echoes back.
func TestClientRecordsAndReadsBackAWorkItemPrice(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{responses: []string{costJSON(27.93, 2, 1)}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	item, err := client.RecordCost(context.Background(), "yoyodyne-1", Cost{TotalUSD: 27.93, Runs: 2, UnknownRuns: 1})
	if err != nil {
		t.Fatalf("RecordCost() error = %v", err)
	}
	if item.Cost == nil || item.Cost.TotalUSD != 27.93 || item.Cost.Runs != 2 || item.Cost.UnknownRuns != 1 {
		t.Fatalf("RecordCost() = %#v", item.Cost)
	}
	if item.Cost.Complete() {
		t.Fatal("a price with an unpriced run behind it must not read as complete")
	}
	wantArgs := [][]string{{
		"update", "yoyodyne-1",
		"--set-metadata=yoyodyne_cost_usd=27.930000",
		"--set-metadata=yoyodyne_cost_runs=2",
		"--set-metadata=yoyodyne_cost_unknown_runs=1",
		"--json",
	}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	// A price bd did not actually store must not read as recorded: an item that
	// looks priced and is not is how a ledger silently goes wrong.
	unstored := &fakeRunner{responses: []string{costJSON(3.5, 2, 1)}}
	if _, err := (Client{Runner: unstored}).RecordCost(context.Background(), "yoyodyne-1", Cost{TotalUSD: 27.93, Runs: 2, UnknownRuns: 1}); err == nil ||
		!strings.Contains(err.Error(), "after being priced") {
		t.Fatalf("RecordCost() unstored error = %v", err)
	}
	unpriced := &fakeRunner{responses: []string{workItemJSON("closed", "")}}
	if _, err := (Client{Runner: unpriced}).RecordCost(context.Background(), "yoyodyne-1", Cost{TotalUSD: 1, Runs: 1}); err == nil ||
		!strings.Contains(err.Error(), "carries no cost") {
		t.Fatalf("RecordCost() unpriced error = %v", err)
	}
}

func TestClientRefusesPricesItCannotMean(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		cost Cost
		want string
	}{
		{name: "no runs", cost: Cost{TotalUSD: 1}, want: "at least one run"},
		{name: "negative", cost: Cost{TotalUSD: -1, Runs: 1}, want: "cannot be negative"},
		{name: "not a number", cost: Cost{TotalUSD: math.NaN(), Runs: 1}, want: "not a number"},
		{name: "more unknown than run", cost: Cost{TotalUSD: 1, Runs: 1, UnknownRuns: 2}, want: "cannot be unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{}
			if _, err := (Client{Runner: runner}).RecordCost(context.Background(), "yoyodyne-1", test.cost); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("RecordCost() error = %v, want it to contain %q", err, test.want)
			}
			if len(runner.args) != 0 {
				t.Fatalf("a refused price still ran bd %#v", runner.args)
			}
		})
	}
	if _, err := (Client{Runner: &fakeRunner{}}).RecordCost(context.Background(), "../escape", Cost{TotalUSD: 1, Runs: 1}); err == nil {
		t.Fatal("RecordCost() invalid id error = nil")
	}
}

// Metadata the harness did not write, or wrote only half of, is not a price. An
// item with a total and nothing saying what it covers is reported as unpriced
// rather than as cheap.
func TestClientReadsOnlyACompleteRecordedPrice(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		metadata string
	}{
		{name: "no metadata", metadata: ""},
		{name: "somebody else's metadata", metadata: `,"metadata":{"team":"platform"}`},
		{name: "total without the runs it covers", metadata: `,"metadata":{"yoyodyne_cost_usd":12.5}`},
		{name: "a total that is not a number", metadata: `,"metadata":{"yoyodyne_cost_usd":"free","yoyodyne_cost_runs":1}`},
		{name: "a price no run could have produced", metadata: `,"metadata":{"yoyodyne_cost_usd":12.5,"yoyodyne_cost_runs":0}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{responses: []string{
				fmt.Sprintf(`[{"id":"yoyodyne-1","title":"t","status":"closed","priority":1,"issue_type":"task"%s}]`, test.metadata),
			}}
			item, err := (Client{Runner: runner}).Show(context.Background(), "yoyodyne-1")
			if err != nil {
				t.Fatalf("Show() error = %v", err)
			}
			if item.Cost != nil {
				t.Fatalf("Show() cost = %#v, want none", item.Cost)
			}
		})
	}

	// The unknown count is absent from an item all of whose runs were priced,
	// because bd stores no key it was never given.
	runner := &fakeRunner{responses: []string{`[{"id":"yoyodyne-1","title":"t","status":"closed","priority":1,"issue_type":"task","metadata":{"yoyodyne_cost_usd":12.5,"yoyodyne_cost_runs":2}}]`}}
	item, err := (Client{Runner: runner}).Show(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if item.Cost == nil || !item.Cost.Complete() || item.Cost.TotalUSD != 12.5 || item.Cost.Runs != 2 {
		t.Fatalf("Show() cost = %#v", item.Cost)
	}
}

// The attribution lives in an item's notes, and notes are what a careless
// writer replaces wholesale. So a write that puts a goal into them also tells
// the tracker, in metadata the same write cannot reach, which goal was written
// here — and a read gives that back. Without it an item whose goal was destroyed
// is indistinguishable from one that never had a goal, which is the one state
// the audit deliberately does not fail; without the words, it is distinguishable
// and still unrecoverable.
//
// The two spellings are bd's, not a choice: it takes an item's whole metadata as
// JSON when the item is created and one key at a time when it is updated. Both
// were run against a real bd, which stores a `--set-metadata=key=value` split at
// the first `=` and returns the rest verbatim; a replace-style
// `bd update --notes=` was confirmed to leave the metadata standing.
func TestAWrittenGoalIsWitnessedWhereReplacingTheNotesCannotReachIt(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	created := `{"id":"yoyodyne-9","title":"Triage docket","description":"Stopped work reaches the development manager.",
	             "status":"open","priority":1,"issue_type":"task","metadata":{"yoyodyne_goal_recorded":"` + autonomy + `"}}`
	runner := &fakeRunner{responses: []string{created}}
	item, err := (Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}).Create(context.Background(), NewWorkItem{
		Title:       "Triage docket",
		Description: "Stopped work reaches the development manager.",
		Type:        "task",
		Notes:       "Created under yoyodyne-ifd.102, decomposing it.\n\n" + goal.Note(autonomy),
		Parent:      "yoyodyne-ifd.102",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !slices.Contains(runner.args[0], `--metadata={"yoyodyne_goal_recorded":"`+autonomy+`"}`) {
		t.Fatalf("the creation carried no witness: %#v", runner.args[0])
	}
	// The words, not a flag: what a destroyed attribution is put back from.
	if item.GoalWitness != (goal.Witness{Recorded: true, Statement: autonomy}) {
		t.Fatalf("Create() = %#v, want the goal witnessed", item.GoalWitness)
	}

	// An item that acquires its goal later is witnessed by the same write that
	// appends it, so an attribution made after the fact is no less protected than
	// one made at creation.
	attribution := "Attributed to a goal.\n\n" + goal.Note(autonomy)
	attributed := &fakeRunner{responses: []string{fmt.Sprintf(
		`[{"id":"yoyodyne-4","title":"t","status":"open","priority":1,"issue_type":"task","notes":%q,`+
			`"metadata":{"yoyodyne_goal_recorded":%q}}]`, attribution, autonomy)}}
	if _, err := (Client{Runner: attributed}).Update(context.Background(), "yoyodyne-4", WorkItemChange{
		AppendNotes: attribution,
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !slices.Contains(attributed.args[0], "--set-metadata=yoyodyne_goal_recorded="+autonomy) {
		t.Fatalf("the attribution carried no witness: %#v", attributed.args[0])
	}

	// A statement longer than a goals document may state is witnessed without its
	// words rather than stored cut in half: half a goal is not the goal, and it
	// would be put back as though it were.
	long := strings.Repeat("a", goal.MaxStatementBytes+1)
	oversized := &fakeRunner{responses: []string{fmt.Sprintf(
		`[{"id":"yoyodyne-4","title":"t","status":"open","priority":1,"issue_type":"task","notes":%q,`+
			`"metadata":{"yoyodyne_goal_recorded":1}}]`, goal.Note(long))}}
	witnessed, err := (Client{Runner: oversized}).Update(context.Background(), "yoyodyne-4", WorkItemChange{AppendNotes: goal.Note(long)})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !slices.Contains(oversized.args[0], "--set-metadata=yoyodyne_goal_recorded=1") {
		t.Fatalf("an oversized goal was stored rather than witnessed bare: %#v", oversized.args[0])
	}
	if witnessed.GoalWitness != (goal.Witness{Recorded: true}) {
		t.Fatalf("Update() = %#v, want a witness carrying no words", witnessed.GoalWitness)
	}

	// A write that records no goal witnesses none. The witness says a goal was
	// written, and an item that got a note about anything else must not read
	// afterwards as one whose attribution was destroyed.
	plain := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-4","title":"t","status":"open","priority":1,"issue_type":"task",` +
			`"notes":"Noted: the reviewer asked for evidence."}]`}}
	updated, err := (Client{Runner: plain}).Update(context.Background(), "yoyodyne-4", WorkItemChange{AppendNotes: "Noted: the reviewer asked for evidence."})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	for _, argument := range plain.args[0] {
		if strings.HasPrefix(argument, "--set-metadata=yoyodyne_goal_recorded=") {
			t.Fatalf("a note carrying no goal witnessed one: %#v", plain.args[0])
		}
	}
	if updated.GoalWitness.Recorded {
		t.Fatalf("Update() = %#v, want no witness", updated.GoalWitness)
	}
}

// A goal already recorded in an item's own notes can be witnessed after the
// fact. It is what covers work attributed before the witness existed, which is
// otherwise protected by nothing at all, and it decides nothing: the statement
// is the item's own, read off it and copied where replacing the notes cannot
// reach it.
func TestAGoalAlreadyRecordedOnAnItemCanBeWitnessedAfterTheFact(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	runner := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.102.2","title":"Triage docket","status":"open","priority":1,"issue_type":"task",` +
			`"notes":"Goal served: ` + autonomy + `","metadata":{"yoyodyne_goal_recorded":"` + autonomy + `"}}]`,
	}}
	item, err := (Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}).RecordGoalWitness(context.Background(), "yoyodyne-ifd.102.2", autonomy)
	if err != nil {
		t.Fatalf("RecordGoalWitness() error = %v", err)
	}
	want := []string{"update", "yoyodyne-ifd.102.2", "--set-metadata=yoyodyne_goal_recorded=" + autonomy, "--json"}
	if !reflect.DeepEqual(runner.args[0], want) {
		t.Fatalf("bd args = %#v, want %#v", runner.args[0], want)
	}
	if item.GoalWitness.Statement != autonomy {
		t.Fatalf("RecordGoalWitness() = %#v", item.GoalWitness)
	}

	// A witness bd did not actually store is a failure rather than a reported
	// success: an item believed covered and not covered is worse than one known
	// to be uncovered.
	unstored := &fakeRunner{responses: []string{`[{"id":"yoyodyne-4","title":"t","status":"open","priority":1,"issue_type":"task"}]`}}
	if _, err := (Client{Runner: unstored}).RecordGoalWitness(context.Background(), "yoyodyne-4", autonomy); err == nil {
		t.Fatal("RecordGoalWitness() accepted a witness the tracker did not store")
	}
	// And there is no goal to witness on an item recording none, so asking is a
	// mistake rather than a bare marker written over work nobody has attributed.
	if _, err := (Client{Runner: &fakeRunner{}}).RecordGoalWitness(context.Background(), "yoyodyne-4", "  "); err == nil {
		t.Fatal("RecordGoalWitness() accepted an empty goal")
	}
}

// The witness is read as "the tracker holds this key", because the harness
// writes it two ways and bd stores what it is given. A stricter reading would
// turn the tracker's own coercion into a destroyed attribution reported as an
// item nobody has attributed yet, which is the failure being guarded against.
func TestTheGoalWitnessIsReadHoweverTheTrackerStoredIt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		stored string
		want   goal.Witness
	}{
		{`{"yoyodyne_goal_recorded":"Run development nearly autonomously."}`, goal.Witness{Recorded: true, Statement: "Run development nearly autonomously."}},
		{`{"yoyodyne_goal_recorded":1}`, goal.Witness{Recorded: true}},
		{`{"yoyodyne_goal_recorded":"1"}`, goal.Witness{Recorded: true}},
		{`{"yoyodyne_goal_recorded":true}`, goal.Witness{Recorded: true}},
		{`{"yoyodyne_goal_recorded":0}`, goal.Witness{}},
		{`{"yoyodyne_goal_recorded":""}`, goal.Witness{}},
		{`{"yoyodyne_goal_recorded":null}`, goal.Witness{}},
		{`{"team":"platform"}`, goal.Witness{}},
		{`{}`, goal.Witness{}},
	} {
		t.Run(test.stored, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{responses: []string{
				`[{"id":"yoyodyne-1","title":"t","status":"open","priority":1,"issue_type":"task","metadata":` + test.stored + `}]`,
			}}
			item, err := (Client{Runner: runner}).Show(context.Background(), "yoyodyne-1")
			if err != nil {
				t.Fatalf("Show() error = %v", err)
			}
			if item.GoalWitness != test.want {
				t.Fatalf("Show() witness = %#v, want %#v", item.GoalWitness, test.want)
			}
		})
	}
}

// What carries a work item is written where replacing its notes cannot reach
// it, and for a sharper reason than the goal witness beside it: this one is read
// by selection, so a marker the next recorded outcome could overwrite would stop
// working exactly when a run wrote on the item. The two spellings are the same
// two the witness uses, which were run against a real bd.
func TestTheExecutorIsWrittenWhereSelectionCanReadItAndTheNotesCannot(t *testing.T) {
	t.Parallel()

	created := `{"id":"yoyodyne-ifd.138","title":"Promote the brief","description":"The architect promotes it.",
	             "status":"open","priority":1,"issue_type":"task","metadata":{"yoyodyne_executor":"conversation:architect"}}`
	runner := &fakeRunner{responses: []string{created}}
	item, err := (Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}).Create(context.Background(), NewWorkItem{
		Title:       "Promote the brief",
		Description: "The architect promotes it.",
		Type:        "task",
		Executor:    domain.ConversationWith(domain.RoleArchitect),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !slices.Contains(runner.args[0], `--metadata={"yoyodyne_executor":"conversation:architect"}`) {
		t.Fatalf("the creation carried no executor: %#v", runner.args[0])
	}
	if item.Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("Create() executor = %q, want the marker read back", item.Executor)
	}

	// An item admitted before the marker existed acquires one by an update, which
	// is how the queue that provoked this gets marked at all.
	marked := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.138","title":"t","status":"open","priority":1,"issue_type":"task","metadata":{"yoyodyne_executor":"conversation:architect"}}]`,
	}}
	if _, err := (Client{Runner: marked}).Update(context.Background(), "yoyodyne-ifd.138", WorkItemChange{
		Executor: domain.ConversationWith(domain.RoleArchitect),
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !slices.Contains(marked.args[0], "--set-metadata=yoyodyne_executor=conversation:architect") {
		t.Fatalf("the update carried no executor: %#v", marked.args[0])
	}

	// A marker bd did not actually store is a failure rather than a reported
	// success, on both doors: what rests on it is that nothing selects the item
	// afterwards, and a caller told it was marked would believe the item covered
	// by exactly the guard it is not covered by. Admission is the sharper of the
	// two, because the item is in the queue and pullable the moment the call
	// returns.
	unstored := &fakeRunner{responses: []string{`[{"id":"yoyodyne-ifd.138","title":"t","status":"open","priority":1,"issue_type":"task"}]`}}
	if _, err := (Client{Runner: unstored}).Update(context.Background(), "yoyodyne-ifd.138", WorkItemChange{
		Executor: domain.ConversationWith(domain.RoleArchitect),
	}); err == nil {
		t.Fatal("Update() with an executor bd did not store = nil error, want a failure")
	}
	unmarked := &fakeRunner{responses: []string{`{"id":"yoyodyne-ifd.138","title":"Promote the brief","status":"open","priority":1,"issue_type":"task"}`}}
	if _, err := (Client{Runner: unmarked}).Create(context.Background(), NewWorkItem{
		Title: "Promote the brief", Description: "d", Type: "task", Executor: domain.ConversationWith(domain.RoleArchitect),
	}); err == nil {
		t.Fatal("Create() with an executor bd did not store = nil error, want a failure")
	}

	// A creation that says nothing about an executor writes no metadata at all,
	// so ordinary work is unaffected by any of this.
	ordinary := &fakeRunner{responses: []string{`{"id":"yoyodyne-1","title":"Implement feature","status":"open","priority":1,"issue_type":"task"}`}}
	plain, err := (Client{Runner: ordinary}).Create(context.Background(), NewWorkItem{
		Title: "Implement feature", Description: "d", Type: "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, argument := range ordinary.args[0] {
		if strings.HasPrefix(argument, "--metadata=") {
			t.Fatalf("an ordinary creation carried %q", argument)
		}
	}
	if !plain.Executor.DeveloperRun() {
		t.Fatalf("Create() executor = %q, want ordinary work to be a developer run", plain.Executor)
	}
}

// An executor the harness does not recognize is refused where it is written and
// carried where it is read, and the asymmetry is deliberate. Refusing the write
// is what keeps the case rare; reading it as work no run may take is what makes
// a typo cost nothing worse than an item nobody pulls, rather than the run this
// whole marker exists to save.
func TestAnUnrecognizedExecutorIsRefusedOnAWriteAndSurvivesARead(t *testing.T) {
	t.Parallel()

	client := Client{Runner: &fakeRunner{}}
	if _, err := client.Create(context.Background(), NewWorkItem{
		Title: "t", Description: "d", Type: "task", Executor: "architect",
	}); err == nil || !strings.Contains(err.Error(), "executor") {
		t.Fatalf("Create() with an unknown executor error = %v, want it refused by name", err)
	}
	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{Executor: "architect"}); err == nil ||
		!strings.Contains(err.Error(), "executor") {
		t.Fatalf("Update() with an unknown executor error = %v, want it refused by name", err)
	}

	stored := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-1","title":"t","status":"open","priority":1,"issue_type":"task","metadata":{"yoyodyne_executor":"architect"}}]`,
	}}
	item, err := (Client{Runner: stored}).Show(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if item.Executor.DeveloperRun() {
		t.Fatalf("Show() executor = %q, want a marker nobody recognizes still to mean not-a-developer-run", item.Executor)
	}
}

// A parking is written where selection can read it, for the reason the executor
// beside it is: the convention it replaces lived in a priority and in whoever
// remembered setting it, and a queue that drains reads neither. Releasing it
// writes the same key with nothing in it, so both directions are one shape.
func TestAParkingIsWrittenWhereSelectionCanReadItAndReleasedTheSameWay(t *testing.T) {
	t.Parallel()

	const reason = "off the critical path by the scope decision"
	created := `{"id":"yoyodyne-ifd.6","title":"The thin Codex backend","description":"d",
	             "status":"open","priority":4,"issue_type":"task","metadata":{"yoyodyne_parked":"` + reason + `"}}`
	runner := &fakeRunner{responses: []string{created}}
	item, err := (Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}).Create(context.Background(), NewWorkItem{
		Title: "The thin Codex backend", Description: "d", Type: "task", Parking: reason,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !slices.Contains(runner.args[0], `--metadata={"yoyodyne_parked":"`+reason+`"}`) {
		t.Fatalf("the creation carried no parking: %#v", runner.args[0])
	}
	if !item.Parking.Parked() || item.Parking.Reason() != reason {
		t.Fatalf("Create() parking = %q, want the reason read back", item.Parking)
	}

	// The queue is older than the marker, so work already admitted acquires one by
	// an update. That is how the set parked by convention gets parked in fact.
	parked := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.6","title":"t","status":"open","priority":4,"issue_type":"task","metadata":{"yoyodyne_parked":"` + reason + `"}}]`,
	}}
	parking := domain.WorkItemParking(reason)
	if _, err := (Client{Runner: parked}).Update(context.Background(), "yoyodyne-ifd.6", WorkItemChange{Parking: &parking}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !slices.Contains(parked.args[0], "--set-metadata=yoyodyne_parked="+reason) {
		t.Fatalf("the update carried no parking: %#v", parked.args[0])
	}

	// Releasing sets the same key to nothing, and an item whose key is empty reads
	// exactly like one nobody ever parked.
	released := domain.WorkItemParking("")
	freeing := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.6","title":"t","status":"open","priority":4,"issue_type":"task","metadata":{"yoyodyne_parked":""}}]`,
	}}
	back, err := (Client{Runner: freeing}).Update(context.Background(), "yoyodyne-ifd.6", WorkItemChange{Parking: &released})
	if err != nil {
		t.Fatalf("Update() releasing error = %v", err)
	}
	if !slices.Contains(freeing.args[0], "--set-metadata=yoyodyne_parked=") {
		t.Fatalf("the release carried no parking: %#v", freeing.args[0])
	}
	if back.Parking.Parked() {
		t.Fatalf("Update() parking = %q after a release, want it unparked", back.Parking)
	}

	// A creation that says nothing about parking writes no metadata at all, so
	// ordinary work is unaffected.
	ordinary := &fakeRunner{responses: []string{`{"id":"yoyodyne-1","title":"t","status":"open","priority":1,"issue_type":"task"}`}}
	plain, err := (Client{Runner: ordinary}).Create(context.Background(), NewWorkItem{Title: "t", Description: "d", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if plain.Parking.Parked() {
		t.Fatalf("Create() parking = %q, want ordinary work unparked", plain.Parking)
	}
}

// Both directions are verified against what bd echoed back, because both have
// something resting on them. A parking that did not take leaves work the
// operator was told is parked sitting pullable in a queue that drains; a release
// that did not take leaves work nobody can start and nothing saying why.
func TestAParkingBdDidNotStoreIsAFailureInBothDirections(t *testing.T) {
	t.Parallel()

	parking := domain.WorkItemParking("deferred by the scope decision")
	unstored := &fakeRunner{responses: []string{`[{"id":"yoyodyne-ifd.6","title":"t","status":"open","priority":4,"issue_type":"task"}]`}}
	if _, err := (Client{Runner: unstored}).Update(context.Background(), "yoyodyne-ifd.6", WorkItemChange{Parking: &parking}); err == nil {
		t.Fatal("Update() with a parking bd did not store = nil error, want a failure")
	}
	unmarked := &fakeRunner{responses: []string{`{"id":"yoyodyne-ifd.6","title":"t","status":"open","priority":4,"issue_type":"task"}`}}
	if _, err := (Client{Runner: unmarked}).Create(context.Background(), NewWorkItem{
		Title: "t", Description: "d", Type: "task", Parking: parking,
	}); err == nil {
		t.Fatal("Create() with a parking bd did not store = nil error, want a failure")
	}

	released := domain.WorkItemParking("")
	stuck := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.6","title":"t","status":"open","priority":4,"issue_type":"task","metadata":{"yoyodyne_parked":"deferred by the scope decision"}}]`,
	}}
	_, err := (Client{Runner: stuck}).Update(context.Background(), "yoyodyne-ifd.6", WorkItemChange{Parking: &released})
	if err == nil || !strings.Contains(err.Error(), "still parked") {
		t.Fatalf("Update() releasing work bd left parked = %v, want it reported as still parked", err)
	}

	// A reason the tracker could not hold as one value on one line is refused
	// before anything is written, rather than stored as half a decision.
	client := Client{Runner: &fakeRunner{}}
	wrapped := domain.WorkItemParking("deferred\nuntil team mode is scoped")
	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{Parking: &wrapped}); err == nil ||
		!strings.Contains(err.Error(), "cannot span lines") {
		t.Fatalf("Update() with a multi-line parking = %v, want it refused", err)
	}
	long := domain.WorkItemParking(strings.Repeat("a", domain.MaxWorkItemParkingBytes+1))
	if _, err := client.Create(context.Background(), NewWorkItem{
		Title: "t", Description: "d", Type: "task", Parking: long,
	}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Create() with an oversized parking = %v, want it refused", err)
	}
}

// A label is written with bd's own label flags rather than as harness metadata,
// one flag per label, and read back from bd's own field — on a creation, where
// the labels ride in the same write as the admission, and on an update in each
// direction.
func TestLabelsAreWrittenWithBdsOwnFlagsAndReadBack(t *testing.T) {
	t.Parallel()

	created := `{"id":"yoyodyne-ifd.419","title":"t","description":"d","status":"open","priority":2,"issue_type":"task","labels":["bug","reliability"]}`
	runner := &fakeRunner{responses: []string{created}}
	item, err := (Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}).Create(context.Background(), NewWorkItem{
		Title: "t", Description: "d", Type: "task", Labels: []string{"reliability", "bug"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, flag := range []string{"--labels=reliability", "--labels=bug"} {
		if !slices.Contains(runner.args[0], flag) {
			t.Fatalf("the creation did not carry %s: %#v", flag, runner.args[0])
		}
	}
	if !item.HasLabel("reliability") || !item.HasLabel("bug") {
		t.Fatalf("Create() labels = %v, want both read back", item.Labels)
	}

	adding := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.419","title":"t","status":"open","priority":2,"issue_type":"task","labels":["reliability"]}]`,
	}}
	labelled, err := (Client{Runner: adding}).Update(context.Background(), "yoyodyne-ifd.419", WorkItemChange{AddLabels: []string{"reliability"}})
	if err != nil {
		t.Fatalf("Update() adding a label error = %v", err)
	}
	if !slices.Contains(adding.args[0], "--add-label=reliability") || !labelled.HasLabel("reliability") {
		t.Fatalf("the update did not add the label and read it back: %#v, %v", adding.args[0], labelled.Labels)
	}

	removing := &fakeRunner{responses: []string{
		`[{"id":"yoyodyne-ifd.419","title":"t","status":"open","priority":2,"issue_type":"task"}]`,
	}}
	bare, err := (Client{Runner: removing}).Update(context.Background(), "yoyodyne-ifd.419", WorkItemChange{RemoveLabels: []string{"reliability"}})
	if err != nil {
		t.Fatalf("Update() removing a label error = %v", err)
	}
	if !slices.Contains(removing.args[0], "--remove-label=reliability") || bare.HasLabel("reliability") {
		t.Fatalf("the update did not remove the label: %#v, %v", removing.args[0], bare.Labels)
	}
}

// A label bd did not store is a failure in both directions, for the reason a
// parking is: what rests on a label is whatever filters on it, and an item told
// it was labelled and not is one that filter never sees.
func TestALabelBdDidNotStoreIsAFailureInBothDirections(t *testing.T) {
	t.Parallel()

	unlabelled := &fakeRunner{responses: []string{`{"id":"yoyodyne-ifd.419","title":"t","status":"open","priority":2,"issue_type":"task"}`}}
	if _, err := (Client{Runner: unlabelled}).Create(context.Background(), NewWorkItem{
		Title: "t", Description: "d", Type: "task", Labels: []string{"reliability"},
	}); err == nil || !strings.Contains(err.Error(), "without the label(s) reliability") {
		t.Fatalf("Create() with a label bd did not store = %v, want a failure naming the label", err)
	}
	unstored := &fakeRunner{responses: []string{`[{"id":"yoyodyne-ifd.419","title":"t","status":"open","priority":2,"issue_type":"task"}]`}}
	if _, err := (Client{Runner: unstored}).Update(context.Background(), "yoyodyne-ifd.419", WorkItemChange{AddLabels: []string{"reliability"}}); err == nil ||
		!strings.Contains(err.Error(), "does not carry the label(s) reliability") {
		t.Fatalf("Update() adding a label bd did not store = %v, want a failure", err)
	}
	stuck := &fakeRunner{responses: []string{`[{"id":"yoyodyne-ifd.419","title":"t","status":"open","priority":2,"issue_type":"task","labels":["reliability"]}]`}}
	if _, err := (Client{Runner: stuck}).Update(context.Background(), "yoyodyne-ifd.419", WorkItemChange{RemoveLabels: []string{"reliability"}}); err == nil ||
		!strings.Contains(err.Error(), "still carries the label(s) reliability") {
		t.Fatalf("Update() removing a label bd kept = %v, want a failure", err)
	}

	// A label that is not an identifier is refused before anything is written.
	// bd would store it, which is exactly why the refusal is here.
	client := Client{Runner: &fakeRunner{}}
	for _, label := range []string{"", "  ", "fix this week", "-leading", strings.Repeat("a", MaxLabelBytes+1)} {
		if _, err := client.Create(context.Background(), NewWorkItem{Title: "t", Description: "d", Type: "task", Labels: []string{label}}); err == nil {
			t.Fatalf("Create() with the label %q was accepted", label)
		}
		if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{AddLabels: []string{label}}); err == nil {
			t.Fatalf("Update() adding the label %q was accepted", label)
		}
	}
	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{AddLabels: []string{"reliability"}, RemoveLabels: []string{"reliability"}}); err == nil ||
		!strings.Contains(err.Error(), "both added and removed") {
		t.Fatalf("Update() adding and removing one label = %v, want it refused", err)
	}
	if _, err := client.Update(context.Background(), "yoyodyne-1", WorkItemChange{AddLabels: []string{"reliability", "reliability"}}); err == nil ||
		!strings.Contains(err.Error(), "named twice") {
		t.Fatalf("Update() naming one label twice = %v, want it refused", err)
	}
	// An update that only labels is an update: it changes something.
	if err := (WorkItemChange{RemoveLabels: []string{"reliability"}}).validate(); err != nil {
		t.Fatalf("an update that only removes a label was refused as empty: %v", err)
	}
}

// An update that only releases a parking is an update: it changes something, and
// refusing it as empty would leave parked work with no way back into the queue.
func TestReleasingAParkingIsAChangeAnUpdateAccepts(t *testing.T) {
	t.Parallel()

	released := domain.WorkItemParking("")
	if err := (WorkItemChange{Parking: &released}).validate(); err != nil {
		t.Fatalf("a release was refused as an empty update: %v", err)
	}
	if err := (WorkItemChange{}).validate(); err == nil {
		t.Fatal("an update changing nothing was accepted")
	}
}

// A note reported as written has to be on the item. The confirmation is what the
// product manager tells the operator a decision was recorded, and a write path
// that reports success without checking makes every one of those untrustworthy —
// which is the whole of yoyodyne-ifd.336.
func TestClientRefusesANoteItCannotFindOnTheItem(t *testing.T) {
	t.Parallel()

	const note = "FORGE HYGIENE joins the sweep's findings, operator-directed."

	// bd answered the update, and neither its answer nor the item itself carries
	// the note. That is a false confirmation, and the caller is told so.
	lost := &fakeRunner{responses: []string{
		workItemJSON("open", "Admitted to the backlog by the product manager."),
		workItemJSON("open", "Admitted to the backlog by the product manager."),
	}}
	_, err := (Client{Runner: lost}).Update(context.Background(), "yoyodyne-1", WorkItemChange{AppendNotes: note})
	if err == nil || !strings.Contains(err.Error(), "does not carry the note") {
		t.Fatalf("Update() with a lost note error = %v, want the false confirmation refused", err)
	}
	// It asked the tracker again before concluding anything: the update's own
	// answer is not the only thing a loss is judged on.
	wantArgs := [][]string{
		{"update", "yoyodyne-1", "--append-notes=" + FrameNote(note), "--json"},
		{"show", "yoyodyne-1", "--json"},
	}
	if !reflect.DeepEqual(lost.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", lost.args, wantArgs)
	}

	// The other direction, and the one that must not be reported as a failure: the
	// write landed and bd's answer to it did not say so. A durable write reported
	// as lost is the mirror of this defect, not a fix for it.
	echoed := &fakeRunner{responses: []string{
		workItemJSON("open", "Admitted to the backlog by the product manager."),
		workItemJSON("open", "Admitted to the backlog by the product manager.\n\n"+note),
	}}
	item, err := (Client{Runner: echoed}).Update(context.Background(), "yoyodyne-1", WorkItemChange{AppendNotes: note})
	if err != nil {
		t.Fatalf("Update() with a durable note error = %v, want the read-back to settle it", err)
	}
	if !strings.Contains(item.Notes, note) {
		t.Fatalf("Update() = %#v, want the item as the tracker actually holds it", item)
	}

	// A read-back that cannot run says which of the two it is. An append nobody
	// could confirm is not an append that failed, and a caller about to tell
	// somebody the note is recorded must not be told that it is.
	unreadable := &fakeRunner{
		results: []execution.ProcessResult{
			{Status: execution.ProcessSucceeded, Stdout: workItemJSON("open", "Admitted to the backlog by the product manager.")},
			{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "failed to open database"},
		},
	}
	if _, err := (Client{Runner: unreadable}).Update(context.Background(), "yoyodyne-1", WorkItemChange{AppendNotes: note}); err == nil ||
		!strings.Contains(err.Error(), "reading it back") {
		t.Fatalf("Update() with an unreadable item error = %v, want the append reported as unconfirmed", err)
	}

	// The ordinary case costs nothing beyond the write: bd's own answer carries
	// the note, so nothing is read back.
	confirmed := &fakeRunner{responses: []string{workItemJSON("open", "Admitted long ago.\n\n"+note)}}
	if _, err := (Client{Runner: confirmed}).Update(context.Background(), "yoyodyne-1", WorkItemChange{AppendNotes: note}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(confirmed.args) != 1 {
		t.Fatalf("bd args = %#v, want the write alone", confirmed.args)
	}

	// The description is confirmed the same way, and is where a scope addition
	// belongs: an item's standing text says what the work is, and a replacement
	// reported as applied and not stored leaves everybody reading the old scope.
	const replaced = "The sweep reads the forge as well as the tracker."
	unwritten := &fakeRunner{responses: []string{workItemJSON("open", ""), workItemJSON("open", "")}}
	if _, err := (Client{Runner: unwritten}).Update(context.Background(), "yoyodyne-1", WorkItemChange{Description: replaced}); err == nil ||
		!strings.Contains(err.Error(), "does not carry the description") {
		t.Fatalf("Update() with a lost description error = %v, want the false confirmation refused", err)
	}
}

// Every path that appends to an item's notes confirms them, not only the edit the
// product manager makes: an outcome, a blocker's reason, and the account a run
// leaves when it reopens an item are all things somebody is told were recorded.
func TestClientConfirmsEveryPathThatAppendsNotes(t *testing.T) {
	t.Parallel()

	const note = "the checks failed after every permitted attempt"
	stale := "Admitted to the backlog by the product manager."

	if _, err := (Client{Runner: &fakeRunner{responses: []string{
		workItemJSON("in_progress", stale), workItemJSON("in_progress", stale),
	}}}).RecordOutcome(context.Background(), "yoyodyne-1", note); err == nil ||
		!strings.Contains(err.Error(), "does not carry the note") {
		t.Fatalf("RecordOutcome() with a lost note error = %v, want it refused", err)
	}
	if _, err := (Client{Runner: &fakeRunner{responses: []string{
		workItemJSON("blocked", stale), workItemJSON("blocked", stale),
	}}}).Block(context.Background(), "yoyodyne-1", note); err == nil ||
		!strings.Contains(err.Error(), "does not carry the note") {
		t.Fatalf("Block() with a lost note error = %v, want it refused", err)
	}
	parking := domain.WorkItemParking("parked by run-1, which found the design it needs has not landed")
	if _, err := (Client{Runner: &fakeRunner{responses: []string{
		reopenedItemJSON("open", stale, string(parking)), reopenedItemJSON("open", stale, string(parking)),
	}}}).Reopen(context.Background(), "yoyodyne-1", note, parking); err == nil ||
		!strings.Contains(err.Error(), "does not carry the note") {
		t.Fatalf("Reopen() with a lost note error = %v, want it refused", err)
	}
}

// What the confirmation compares as equal, and what it does not. A tracker is
// free to settle line endings and trailing space; a note it stored cut short is
// the loss this exists to find.
func TestTextCarriedIgnoresOnlyWhatATrackerMayRewrite(t *testing.T) {
	t.Parallel()

	const note = "Scope addition, operator-directed:\nthe sweep reads the forge as well as the tracker."
	if !textCarried("Admitted long ago.\r\n\r\nScope addition, operator-directed:   \r\nthe sweep reads the forge as well as the tracker.", note) {
		t.Fatal("a note stored with the tracker's own line endings read as missing")
	}
	if textCarried("Admitted long ago.\n\nScope addition, operator-directed:", note) {
		t.Fatal("a note stored cut short read as recorded")
	}
	if textCarried("Admitted long ago.\n\nScope addition, operator directed:\nthe sweep reads the forge as well as the tracker.", note) {
		t.Fatal("a note stored with words changed read as recorded")
	}
}

// Whether a note is the last thing an item's notes say, which is what a retry
// asks before appending it again: the same text earlier in the notes is an older
// write, and an empty note is never the end of anything.
func TestNotesEndWithOnlyTheLastThingAppended(t *testing.T) {
	t.Parallel()

	const note = "Scope addition, operator-directed:\nthe sweep reads the forge as well as the tracker."
	if !NotesEndWith("Admitted long ago.\r\n\r\nScope addition, operator-directed:   \r\nthe sweep reads the forge as well as the tracker.\n", note) {
		t.Fatal("a note stored last, with the tracker's own line endings, read as not appended")
	}
	if NotesEndWith("Admitted long ago.\n\n"+note+"\n\nA later note.", note) {
		t.Fatal("a note followed by a later one read as the last thing appended")
	}
	if NotesEndWith("Admitted long ago.", "  ") {
		t.Fatal("an empty note read as appended")
	}
}

type fakeRunner struct {
	responses []string
	results   []execution.ProcessResult
	// failures answers the calls at these indexes, past the leading results,
	// with a failure rather than a response: a bd that refuses a command in the
	// middle of a sequence.
	failures map[int]execution.ProcessResult
	args     [][]string
	commands []execution.Command
}

func (f *fakeRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	f.args = append(f.args, append([]string(nil), command.Args...))
	f.commands = append(f.commands, command)
	index := len(f.args) - 1
	if index < len(f.results) {
		return f.results[index], nil
	}
	if failure, failed := f.failures[index]; failed {
		return failure, nil
	}
	if index >= len(f.responses) {
		return execution.ProcessResult{}, fmt.Errorf("unexpected command %v", command.Args)
	}
	if command.RawStdout != nil {
		_, err := io.WriteString(command.RawStdout, f.responses[index])
		return execution.ProcessResult{Status: execution.ProcessSucceeded, ExitCode: 0}, err
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: f.responses[index]}, nil
}

// costJSON is what bd echoes back after a price is stored: the item, with the
// price among whatever else the project keeps in its metadata.
func costJSON(total float64, runs, unknown int) string {
	return fmt.Sprintf(`[{
  "id":"yoyodyne-1",
  "title":"Implement feature",
  "status":"closed",
  "priority":1,
  "issue_type":"task",
  "metadata":{"team":"platform","yoyodyne_cost_usd":%v,"yoyodyne_cost_runs":%d,"yoyodyne_cost_unknown_runs":%d}
}]`, total, runs, unknown)
}

func workItemJSON(status, notes string) string {
	return fmt.Sprintf(`[{
  "id":"yoyodyne-1",
  "title":"Implement feature",
  "description":"Use docs/v1-harness-design.md",
  "design":"Bounded change",
  "acceptance_criteria":"Tests pass",
  "notes":%q,
  "status":%q,
  "priority":1,
  "issue_type":"task",
  "dependencies":[{"id":"yoyodyne-parent","dependency_type":"parent-child","status":"closed"}]
}]`, notes, status)
}

// The disagreement yoyodyne-ifd.338 was admitted for. The backlog computes a
// blocked item's readiness from what it actually waits on, because the status
// field is written when work stops and never rewritten when what stopped it
// clears; bd's claim gate reads that status and nothing else. So from the moment
// yoyodyne-ifd.277 released the stale-status items, every one of them was
// selectable and unclaimable at once — yoyodyne-ifd.285 was dispatched
// twenty-nine times in twenty hours and died here each time.
//
// The claim re-reads the item under the refusal, finds nothing unfinished
// waiting, corrects the status, reads the correction back, and takes the item
// on the read that returned open.
func TestClientClaimsPastAStaleBlockedStatus(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		results: []execution.ProcessResult{{
			Status:   execution.ProcessFailed,
			ExitCode: 1,
			Stderr:   "Error claiming yoyodyne-1: issue not claimable: status blocked",
		}},
		responses: []string{
			"",
			blockedItemJSON(nil),
			`[]`,
			blockedItemJSON(nil),
			workItemJSON("open", ""),
			workItemJSON("open", ""),
			workItemJSON("in_progress", ""),
			"",
		},
	}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo", readBack: unsleepingReadBack(t)}
	item, cleared, err := client.Claim(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if item.Status != "in_progress" {
		t.Fatalf("Claim() status = %q, want in_progress", item.Status)
	}
	if len(runner.args) != 8 {
		t.Fatalf("bd was called %d time(s): %#v", len(runner.args), runner.args)
	}
	// The correction is written to the tracker rather than held in this process:
	// a status left as it was found is refused again by the next claim, and reads
	// as blocked to everybody who opens the item.
	corrected := runner.args[4]
	if corrected[0] != "update" || corrected[1] != "yoyodyne-1" || corrected[2] != "--status=open" {
		t.Fatalf("the stale status was not corrected: %#v", corrected)
	}
	// Appended rather than replaced. Notes written over are how this project has
	// lost goal attribution twice.
	if !strings.HasPrefix(corrected[3], "--append-notes=") {
		t.Fatalf("the correction did not append its account: %#v", corrected)
	}
	if !strings.Contains(corrected[3], "not claimable: status blocked") {
		t.Fatalf("the correction does not say what bd refused: %q", corrected[3])
	}
	// The note rides the write and cannot know whether the write landed, so it
	// says what the harness is doing and what the claim waits on, not that the
	// status is cleared: on 2026-09-20 a note saying "cleared" was followed by bd
	// refusing the claim on the same status.
	if strings.Contains(corrected[3], "cleared this item") || !strings.Contains(corrected[3], "reads the status back as open") {
		t.Fatalf("the correction claims the clear before the tracker confirmed it: %q", corrected[3])
	}
	// The refusal the note quotes is the one that came before the clear, and the
	// note says so ahead of the promise of a claim: quoted after it, the refusal
	// read as bd refusing the claim that followed the clear (yoyodyne-ifd.428.44).
	refusalAt := strings.Index(corrected[3], "not claimable: status blocked")
	if !strings.Contains(corrected[3], "came before anything below") || refusalAt > strings.Index(corrected[3], "The claim follows") {
		t.Fatalf("the correction does not put the refusal it quotes before the clear: %q", corrected[3])
	}
	// The write is not the correction; the read that returns open is. The claim
	// is made after that read and never before it.
	if !reflect.DeepEqual(runner.args[5], []string{"show", "yoyodyne-1", "--json"}) {
		t.Fatalf("the cleared status was not read back before the claim: %#v", runner.args[5])
	}
	if !reflect.DeepEqual(runner.args[6], []string{"update", "yoyodyne-1", "--claim", "--json"}) {
		t.Fatalf("the item was not claimed after the correction: %#v", runner.args[6])
	}
	// Both writes are recorded on the item: the clear on its own write, and the
	// claim on a note appended once the claim has landed.
	claimed := runner.args[7]
	if len(claimed) != 4 || claimed[0] != "update" || !strings.HasPrefix(claimed[2], "--append-notes=") || !strings.Contains(claimed[2], "claimed the item") {
		t.Fatalf("the claim was not recorded on the item after it landed: %#v", claimed)
	}
	want := &StaleBlockClear{Outcome: domain.StaleBlockClearConfirmed, Reads: 1, Status: "open"}
	if !reflect.DeepEqual(cleared, want) {
		t.Fatalf("Claim() account = %#v, want %#v", cleared, want)
	}
}

// A clear that lands late. The tracker still holds the status the write was
// meant to replace when it is first read back, and returns open on a later read
// within the bound; the claim is made on that read, and the account says it was
// the third rather than the first, because a tracker slower than the claim is
// worth knowing about before the day it is slower than the wait.
func TestClientClaimsPastAStaleBlockedStatusWhoseClearLandsLate(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		results: []execution.ProcessResult{{
			Status:   execution.ProcessFailed,
			ExitCode: 1,
			Stderr:   "Error claiming yoyodyne-1: issue not claimable: status blocked",
		}},
		responses: []string{
			"",
			blockedItemJSON(nil),
			`[]`,
			blockedItemJSON(nil),
			workItemJSON("open", ""),
			blockedItemJSON(nil),
			blockedItemJSON(nil),
			workItemJSON("open", ""),
			workItemJSON("in_progress", ""),
			"",
		},
	}
	var slept []time.Duration
	readBack := staleBlockClearReadBack{reads: 5, interval: 7 * time.Millisecond, sleep: func(_ context.Context, interval time.Duration) error {
		slept = append(slept, interval)
		return nil
	}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo", readBack: readBack}
	item, cleared, err := client.Claim(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if item.Status != "in_progress" {
		t.Fatalf("Claim() status = %q, want in_progress", item.Status)
	}
	if len(runner.args) != 10 {
		t.Fatalf("bd was called %d time(s): %#v", len(runner.args), runner.args)
	}
	for index := 5; index <= 7; index++ {
		if !reflect.DeepEqual(runner.args[index], []string{"show", "yoyodyne-1", "--json"}) {
			t.Fatalf("call %d = %#v, want the status read back", index, runner.args[index])
		}
	}
	if !reflect.DeepEqual(runner.args[8], []string{"update", "yoyodyne-1", "--claim", "--json"}) {
		t.Fatalf("the item was not claimed after the read that returned open: %#v", runner.args[8])
	}
	// The reads are spaced by the interval, and only between reads: nothing waits
	// before the first, and nothing waits after the one that confirmed the clear.
	if !reflect.DeepEqual(slept, []time.Duration{7 * time.Millisecond, 7 * time.Millisecond}) {
		t.Fatalf("waited %v between reads, want the interval twice", slept)
	}
	want := &StaleBlockClear{Outcome: domain.StaleBlockClearConfirmedLate, Reads: 3, Status: "open"}
	if !reflect.DeepEqual(cleared, want) {
		t.Fatalf("Claim() account = %#v, want %#v", cleared, want)
	}
}

// A clear that never lands. Every read within the bound returns the status the
// write was meant to replace, so the claim is not made: a claim on a status the
// tracker still holds as blocked is the refusal this was entered on, met a
// second time, and on 2026-09-20 that is how yoyodyne-ifd.415's re-run tripped
// on its own correction. The clear is reported as unconfirmed with what the
// tracker returned, never as cleared; the account is returned beside the error
// so the run's record can say so; and the item is left for the next pull.
func TestClientLeavesAnItemWhoseStaleBlockClearNeverLands(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		results: []execution.ProcessResult{{
			Status:   execution.ProcessFailed,
			ExitCode: 1,
			Stderr:   "Error claiming yoyodyne-1: issue not claimable: status blocked",
		}},
		responses: []string{
			"",
			blockedItemJSON(nil),
			`[]`,
			blockedItemJSON(nil),
			workItemJSON("open", ""),
			blockedItemJSON(nil),
			blockedItemJSON(nil),
			blockedItemJSON(nil),
			blockedItemJSON(nil),
		},
	}
	reads := 0
	readBack := staleBlockClearReadBack{reads: 3, interval: time.Second, sleep: func(_ context.Context, interval time.Duration) error {
		reads++
		return nil
	}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo", readBack: readBack}
	item, cleared, err := client.Claim(context.Background(), "yoyodyne-1")
	if err == nil {
		t.Fatal("Claim() error = nil, want the unconfirmed clear reported")
	}
	if item.ID != "" {
		t.Fatalf("Claim() = %#v, want no item on an unconfirmed clear", item)
	}
	// The refusal names the order of the writes ahead of the tracker's own words,
	// so it is not read as the first refusal met a second time.
	if !strings.HasPrefix(err.Error(), "bd refused the claim on yoyodyne-1 for its blocked status; the harness then wrote the status open and read it back") {
		t.Fatalf("Claim() error = %v, want it to lead with the order of the writes", err)
	}
	for _, want := range []string{"was never confirmed", `returned status "blocked"`, "3 read(s)", "2s", "no claim followed the clear", "left for the next pull", "not claimable: status blocked"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Claim() error = %v, want it to say %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "cleared") {
		t.Fatalf("Claim() error = %v, reports a clear the tracker never confirmed as cleared", err)
	}
	// Three reads within the bound, the wait between each pair, and then no claim:
	// the last bd call is the note saying what happened, not the claim.
	if len(runner.args) != 9 {
		t.Fatalf("bd was called %d time(s): %#v", len(runner.args), runner.args)
	}
	for index := 5; index <= 7; index++ {
		if !reflect.DeepEqual(runner.args[index], []string{"show", "yoyodyne-1", "--json"}) {
			t.Fatalf("call %d = %#v, want the status read back", index, runner.args[index])
		}
	}
	if reads != 2 {
		t.Fatalf("waited %d time(s) between reads, want 2", reads)
	}
	for _, call := range runner.args {
		if len(call) > 2 && call[2] == "--claim" && !reflect.DeepEqual(call, runner.args[0]) {
			t.Fatalf("the item was claimed on a status the tracker never read back as open: %#v", runner.args)
		}
	}
	// The note the write carried promised a claim on a read that never came, so
	// what came instead is appended beside it — appended, never replaced — and
	// nothing moves the status: the item is left for the next pull.
	note := runner.args[8]
	if note[0] != "update" || note[1] != "yoyodyne-1" || !strings.HasPrefix(note[2], "--append-notes=") || len(note) != 4 {
		t.Fatalf("the unconfirmed clear was not appended to the item's notes: %#v", note)
	}
	if !strings.Contains(note[2], "could not confirm the clear") || !strings.Contains(note[2], `returned status "blocked"`) || !strings.Contains(note[2], "left for the next pull") {
		t.Fatalf("the note does not say what the tracker returned: %q", note[2])
	}
	want := &StaleBlockClear{Outcome: domain.StaleBlockClearUnconfirmed, Reads: 3, Status: "blocked"}
	if !reflect.DeepEqual(cleared, want) {
		t.Fatalf("Claim() account = %#v, want %#v", cleared, want)
	}
}

// A claim bd refuses on the status after a read returned open. On 2026-09-22
// and 2026-09-23 a recorded re-run cleared the status on yoyodyne-ifd.432.10
// and on yoyodyne-ifd.117.3, and bd then refused the claim with "issue not
// claimable: status blocked", so the carry-out lost its pull and a later pull
// claimed each item. The claim is retried on a later read within the same
// bound and taken there, and the account says how many claims were refused.
func TestClientRetriesAClaimRefusedOnTheStatusAfterTheClearReadBackOpen(t *testing.T) {
	t.Parallel()

	refused := execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "Error claiming yoyodyne-1: issue not claimable: status blocked",
	}
	runner := &fakeRunner{
		results: []execution.ProcessResult{refused},
		// The claim after the first read that returned open is refused on the
		// status, exactly as the carry-outs met it.
		failures: map[int]execution.ProcessResult{6: refused},
		responses: []string{
			"",
			blockedItemJSON(nil),
			`[]`,
			blockedItemJSON(nil),
			workItemJSON("open", ""),
			workItemJSON("open", ""),
			"",
			workItemJSON("open", ""),
			workItemJSON("in_progress", ""),
			"",
		},
	}
	var slept []time.Duration
	readBack := staleBlockClearReadBack{reads: 5, interval: 7 * time.Millisecond, sleep: func(_ context.Context, interval time.Duration) error {
		slept = append(slept, interval)
		return nil
	}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo", readBack: readBack}
	item, cleared, err := client.Claim(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Claim() error = %v, want the claim retried and taken on the same call", err)
	}
	if item.Status != "in_progress" {
		t.Fatalf("Claim() status = %q, want in_progress", item.Status)
	}
	if len(runner.args) != 10 {
		t.Fatalf("bd was called %d time(s): %#v", len(runner.args), runner.args)
	}
	show := []string{"show", "yoyodyne-1", "--json"}
	claim := []string{"update", "yoyodyne-1", "--claim", "--json"}
	for index, want := range map[int][]string{5: show, 6: claim, 7: show, 8: claim} {
		if !reflect.DeepEqual(runner.args[index], want) {
			t.Fatalf("call %d = %#v, want %#v", index, runner.args[index], want)
		}
	}
	// The retry is spaced like any other read in the bound: one wait, between the
	// refused claim and the read the retried claim is made on.
	if !reflect.DeepEqual(slept, []time.Duration{7 * time.Millisecond}) {
		t.Fatalf("waited %v, want the interval once", slept)
	}
	if note := runner.args[9]; !strings.Contains(note[2], "refused the claim on the status 1 time(s)") || !strings.Contains(note[2], "claimed on read 2") {
		t.Fatalf("the claim that landed after a refused one was not recorded on the item: %#v", note)
	}
	want := &StaleBlockClear{Outcome: domain.StaleBlockClearConfirmedLate, Reads: 2, Status: "open", ClaimsRefused: 1}
	if !reflect.DeepEqual(cleared, want) {
		t.Fatalf("Claim() account = %#v, want %#v", cleared, want)
	}
}

// A tracker that lags the clear on both of its answers: the status is still
// blocked on the first read back, and once a read returns open bd still refuses
// the claim on the status once. The claim lands within the same call, the
// writes are made in the order clear, read back, claim, and both the clear and
// the claim are recorded on the item. This is the shape the reviewer of
// yoyodyne-ifd.428.44 read in its notes on 2026-09-28 and took for a claim the
// clear could not make room for.
func TestClientClaimLandsAgainstATrackerThatLagsTheClear(t *testing.T) {
	t.Parallel()

	refused := execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "Error claiming yoyodyne-1: issue not claimable: status blocked",
	}
	runner := &fakeRunner{
		results:  []execution.ProcessResult{refused},
		failures: map[int]execution.ProcessResult{7: refused},
		responses: []string{
			"",
			blockedItemJSON(nil),
			`[]`,
			blockedItemJSON(nil),
			workItemJSON("open", ""),
			blockedItemJSON(nil),
			workItemJSON("open", ""),
			"",
			workItemJSON("open", ""),
			workItemJSON("in_progress", ""),
			"",
		},
	}
	readBack := staleBlockClearReadBack{reads: 5, interval: time.Second, sleep: func(context.Context, time.Duration) error { return nil }}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo", readBack: readBack}
	item, cleared, err := client.Claim(context.Background(), "yoyodyne-1")
	if err != nil {
		t.Fatalf("Claim() error = %v, want the claim to land against a lagging tracker", err)
	}
	if item.Status != "in_progress" {
		t.Fatalf("Claim() status = %q, want in_progress", item.Status)
	}
	if len(runner.args) != 11 {
		t.Fatalf("bd was called %d time(s): %#v", len(runner.args), runner.args)
	}
	show := []string{"show", "yoyodyne-1", "--json"}
	claim := []string{"update", "yoyodyne-1", "--claim", "--json"}
	for index, want := range map[int][]string{5: show, 6: show, 7: claim, 8: show, 9: claim} {
		if !reflect.DeepEqual(runner.args[index], want) {
			t.Fatalf("call %d = %#v, want %#v", index, runner.args[index], want)
		}
	}
	// The clear comes before any read back and any claim after it.
	if clear := runner.args[4]; clear[2] != "--status=open" || !strings.HasPrefix(clear[3], "--append-notes=") {
		t.Fatalf("call 4 = %#v, want the clear with its note", clear)
	}
	// The claim is recorded after it landed, and only then.
	note := runner.args[10]
	if len(note) != 4 || !strings.HasPrefix(note[2], "--append-notes=") || !strings.Contains(note[2], "refused the claim on the status 1 time(s)") || !strings.Contains(note[2], "claimed on read 3") {
		t.Fatalf("the claim was not recorded on the item: %#v", note)
	}
	want := &StaleBlockClear{Outcome: domain.StaleBlockClearConfirmedLate, Reads: 3, Status: "open", ClaimsRefused: 1}
	if !reflect.DeepEqual(cleared, want) {
		t.Fatalf("Claim() account = %#v, want %#v", cleared, want)
	}
}

// A claim bd refuses on the status after every read that returned open is not
// taken, however the reads came back: the item is left for the next pull, the
// clear is reported as unconfirmed with the refused claims counted, and the
// note appended to the item says so.
func TestClientLeavesAnItemWhoseClaimIsRefusedOnTheStatusThroughoutTheBound(t *testing.T) {
	t.Parallel()

	refused := execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "Error claiming yoyodyne-1: issue not claimable: status blocked",
	}
	runner := &fakeRunner{
		results:  []execution.ProcessResult{refused},
		failures: map[int]execution.ProcessResult{6: refused, 8: refused},
		responses: []string{
			"",
			blockedItemJSON(nil),
			`[]`,
			blockedItemJSON(nil),
			workItemJSON("open", ""),
			workItemJSON("open", ""),
			"",
			workItemJSON("open", ""),
			"",
			workItemJSON("open", ""),
		},
	}
	readBack := staleBlockClearReadBack{reads: 2, interval: time.Second, sleep: func(context.Context, time.Duration) error { return nil }}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo", readBack: readBack}
	item, cleared, err := client.Claim(context.Background(), "yoyodyne-1")
	if err == nil {
		t.Fatal("Claim() error = nil, want the claim left for the next pull")
	}
	if item.ID != "" {
		t.Fatalf("Claim() = %#v, want no item", item)
	}
	if !strings.HasPrefix(err.Error(), "bd refused the claim on yoyodyne-1 for its blocked status; the harness then wrote the status open") {
		t.Fatalf("Claim() error = %v, want it to lead with the order of the writes", err)
	}
	for _, want := range []string{"was never confirmed", "refused the claim on the status 2 time(s)", "each claim made after a read returned open was refused on the status (2)", "left for the next pull"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Claim() error = %v, want it to say %q", err, want)
		}
	}
	if len(runner.args) != 10 {
		t.Fatalf("bd was called %d time(s): %#v", len(runner.args), runner.args)
	}
	note := runner.args[9]
	if note[0] != "update" || !strings.HasPrefix(note[2], "--append-notes=") || len(note) != 4 {
		t.Fatalf("the refused claims were not appended to the item's notes: %#v", note)
	}
	if !strings.Contains(note[2], "refused the claim on the status 2 time(s)") || !strings.Contains(note[2], "left for the next pull") {
		t.Fatalf("the note does not say the claims were refused: %q", note[2])
	}
	want := &StaleBlockClear{Outcome: domain.StaleBlockClearUnconfirmed, Reads: 2, Status: "open", ClaimsRefused: 2}
	if !reflect.DeepEqual(cleared, want) {
		t.Fatalf("Claim() account = %#v, want %#v", cleared, want)
	}
}

// A read-back that could not be made within the bound is an error beside the
// reads that were, with the account of how far the confirmation got: a context
// that ended while waiting is not a clear the tracker refused, and it is not a
// clear that was confirmed either.
func TestClientReportsAStaleBlockClearReadBackTheContextEnded(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		results: []execution.ProcessResult{{
			Status:   execution.ProcessFailed,
			ExitCode: 1,
			Stderr:   "Error claiming yoyodyne-1: issue not claimable: status blocked",
		}},
		responses: []string{
			"",
			blockedItemJSON(nil),
			`[]`,
			blockedItemJSON(nil),
			workItemJSON("open", ""),
			blockedItemJSON(nil),
		},
	}
	readBack := staleBlockClearReadBack{reads: 5, interval: time.Second, sleep: func(context.Context, time.Duration) error {
		return context.DeadlineExceeded
	}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo", readBack: readBack}
	_, cleared, err := client.Claim(context.Background(), "yoyodyne-1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Claim() error = %v, want the context's ending", err)
	}
	if len(runner.args) != 6 {
		t.Fatalf("bd was called %d time(s), want nothing after the read the wait ended on: %#v", len(runner.args), runner.args)
	}
	want := &StaleBlockClear{Outcome: domain.StaleBlockClearUnconfirmed, Reads: 1, Status: "blocked"}
	if !reflect.DeepEqual(cleared, want) {
		t.Fatalf("Claim() account = %#v, want %#v", cleared, want)
	}
}

// unsleepingReadBack is the default bound with the wait between reads replaced
// by a check that nothing asked for one: a test whose clear lands on the first
// read must not spend the interval, and a test that does wait sets its own.
func unsleepingReadBack(t *testing.T) staleBlockClearReadBack {
	t.Helper()
	return staleBlockClearReadBack{sleep: func(context.Context, time.Duration) error {
		t.Fatal("the read-back waited between reads when the first read confirmed the clear")
		return nil
	}}
}

// An item that really does wait on unfinished work is refused, and the refusal
// names the work. The point of correcting a stale status is that it is stale; a
// correction that could not tell the two apart would start runs over items whose
// blockers are still open, which is the failure the status field exists to
// prevent.
func TestClientRefusesToClaimAnItemWaitingOnUnfinishedWork(t *testing.T) {
	t.Parallel()

	waiting := []Dependency{{ID: "yoyodyne-2", Type: BlocksDependency}}
	runner := &fakeRunner{
		results: []execution.ProcessResult{{
			Status:   execution.ProcessFailed,
			ExitCode: 1,
			Stderr:   "Error claiming yoyodyne-1: issue not claimable: status blocked",
		}},
		responses: []string{
			"",
			blockedItemJSON(waiting),
			`[{"id":"yoyodyne-2","title":"The blocker","status":"open","priority":1,"issue_type":"task"}]`,
			`[]`,
		},
	}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	_, _, err := client.Claim(context.Background(), "yoyodyne-1")
	if err == nil {
		t.Fatal("Claim() error = nil, want the refusal to stand")
	}
	if !strings.Contains(err.Error(), "yoyodyne-2") {
		t.Fatalf("Claim() error = %v, want it to name the unfinished work", err)
	}
	if len(runner.args) != 4 {
		t.Fatalf("bd was called %d time(s), want the refusal to write nothing: %#v", len(runner.args), runner.args)
	}
}

// A refusal that is not about a stale status is returned as it came. The
// recovery is for one disagreement and must not become a retry of everything.
func TestClientReturnsAClaimRefusalItCannotJudge(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		results: []execution.ProcessResult{{
			Status:   execution.ProcessFailed,
			ExitCode: 1,
			Stderr:   "Error claiming yoyodyne-1: issue not found",
		}},
		responses: []string{""},
	}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	if _, _, err := client.Claim(context.Background(), "yoyodyne-1"); err == nil || !strings.Contains(err.Error(), "issue not found") {
		t.Fatalf("Claim() error = %v, want bd's own refusal", err)
	}
	if len(runner.args) != 1 {
		t.Fatalf("bd was called %d time(s), want one: %#v", len(runner.args), runner.args)
	}
}

// blockedItemJSON is the yoyodyne-ifd.285 shape: an item whose status says
// blocked, carrying whatever dependencies the case is about. With none of them a
// "blocks" edge on unfinished work, nothing is actually waiting.
func blockedItemJSON(dependencies []Dependency) string {
	encoded := make([]string, 0, len(dependencies)+1)
	encoded = append(encoded, `{"id":"yoyodyne-parent","dependency_type":"parent-child"}`)
	for _, dependency := range dependencies {
		encoded = append(encoded, fmt.Sprintf(`{"id":%q,"dependency_type":%q,"status":%q}`,
			dependency.ID, dependency.Type, dependency.Status))
	}
	return fmt.Sprintf(`[{
  "id":"yoyodyne-1",
  "title":"Implement feature",
  "status":"blocked",
  "priority":1,
  "issue_type":"task",
  "dependencies":[%s]
}]`, strings.Join(encoded, ","))
}

// The race rather than the disagreement. An item that reads as something other
// than blocked when it is re-read under the claim has moved since bd refused it,
// and the sharpest case is an item another run now holds: reopening that one
// takes work off whoever has it. Only the status bd refused on is corrected.
func TestClientDoesNotReopenAnItemThatHasMovedSinceTheRefusal(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		results: []execution.ProcessResult{{
			Status:   execution.ProcessFailed,
			ExitCode: 1,
			Stderr:   "Error claiming yoyodyne-1: issue not claimable: status blocked",
		}},
		responses: []string{"", workItemJSON("in_progress", "")},
	}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	_, _, err := client.Claim(context.Background(), "yoyodyne-1")
	if err == nil || !strings.Contains(err.Error(), "in_progress") {
		t.Fatalf("Claim() error = %v, want the refusal to stand and name what it found", err)
	}
	if len(runner.args) != 2 {
		t.Fatalf("bd was called %d time(s), want the claim and the re-read only: %#v", len(runner.args), runner.args)
	}
}

// The revision the harness closed a conversation-carried item on is written
// onto the item and read back, so a reopened item still says what it was closed
// on — and a record bd did not store must not read as recorded, because that is
// exactly the item the next pull closes a second time.
func TestClientRecordsAndReadsBackALanding(t *testing.T) {
	t.Parallel()

	landing := "docs/designs/management-and-supervision.md@2026-09-07T05:30:00Z"
	stored := fmt.Sprintf(`[{"id":"yoyodyne-1","title":"The architect designs it","status":"open","priority":2,"issue_type":"task","metadata":{"yoyodyne_executor":"conversation:architect","yoyodyne_landed":%q}}]`, landing)
	runner := &fakeRunner{responses: []string{stored}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	item, err := client.RecordLanding(context.Background(), "yoyodyne-1", landing)
	if err != nil {
		t.Fatalf("RecordLanding() error = %v", err)
	}
	if item.Landing != landing {
		t.Fatalf("RecordLanding() landing = %q, want %q", item.Landing, landing)
	}
	wantArgs := [][]string{{"update", "yoyodyne-1", "--set-metadata=yoyodyne_landed=" + landing, "--json"}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, wantArgs)
	}

	unstored := &fakeRunner{responses: []string{`[{"id":"yoyodyne-1","title":"The architect designs it","status":"open","priority":2,"issue_type":"task","metadata":{}}]`}}
	if _, err := (Client{Runner: unstored}).RecordLanding(context.Background(), "yoyodyne-1", landing); err == nil ||
		!strings.Contains(err.Error(), "after being recorded") {
		t.Fatalf("RecordLanding() unstored error = %v", err)
	}
	// Clearing is the same write with nothing in it, and reads back as nothing.
	cleared := &fakeRunner{responses: []string{`[{"id":"yoyodyne-1","title":"The architect designs it","status":"open","priority":2,"issue_type":"task","metadata":{"yoyodyne_landed":""}}]`}}
	if item, err := (Client{Runner: cleared}).RecordLanding(context.Background(), "yoyodyne-1", ""); err != nil || item.Landing != "" {
		t.Fatalf("RecordLanding() cleared = %#v, %v", item, err)
	}
	if _, err := (Client{Runner: &fakeRunner{}}).RecordLanding(context.Background(), "../escape", landing); err == nil {
		t.Fatal("RecordLanding() on an invalid id = nil error")
	}
}

// TestBDOutputIsRetainedWholeAndACutCopyIsRefused is the regression test for
// 2026-09-26, when a listing of every item passed the runner's general 8 MiB and
// the cut copy was decoded, failing every listing-backed path with a complaint
// about a stray bracket. The client asks for a bound sized for a whole tracker,
// and a copy the runner still had to cut is refused by name, never decoded.
func TestBDOutputIsRetainedWholeAndACutCopyIsRefused(t *testing.T) {
	whole := &fakeRunner{responses: []string{"[]"}}
	if _, err := (Client{Runner: whole}).run(context.Background(), "list", "--json"); err != nil {
		t.Fatalf("a whole listing was refused: %v", err)
	}
	if got := whole.commands[0].MaxOutputBytes; got != maxBDOutputBytes {
		t.Fatalf("bd ran with MaxOutputBytes %d, want %d", got, maxBDOutputBytes)
	}
	if maxBDOutputBytes <= 8<<20 {
		t.Fatalf("maxBDOutputBytes %d is not above the runner's 8 MiB default", maxBDOutputBytes)
	}

	cut := &fakeRunner{results: []execution.ProcessResult{{
		Status:           execution.ProcessSucceeded,
		Stdout:           "[{\"id\":\"yoyodyne-1\"",
		OutputTruncation: "output cut at the bound",
	}}}
	_, err := (Client{Runner: cut}).run(context.Background(), "list", "--json")
	if err == nil {
		t.Fatal("a cut copy of bd's output was accepted")
	}
	if !strings.Contains(err.Error(), "was cut and is not read") {
		t.Fatalf("the refusal does not say the output was cut: %v", err)
	}
}
