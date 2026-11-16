package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
)

// landingRepository is a checkout with the recommended artifact layout and one
// design whose revision log is the one the incident left behind: the
// side-conversations revision opening with yoyodyne-ifd.330, a backfill that
// mentions yoyodyne-ifd.280 in passing, and a revision for a child of 330.
func landingRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(relative, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/product/brief.md", "---\nid: brief\nkind: brief\ntitle: The brief\nstatus: active\nrevisions:\n"+
		"    - action: created\n      by: product-manager\n      at: 2026-08-17T12:00:00Z\n      reason: 'yoyodyne-ifd.82.1 - team mode scoped: what it promises and who counts as an operator'\n---\n# Brief\n")
	write("docs/designs/management-and-supervision.md", "---\nid: management-and-supervision\nkind: design\ntitle: Management and supervision\nstatus: active\nsupports: [brief]\nrevisions:\n"+
		"    - action: created\n      by: architect\n      at: 2026-08-20T12:00:00Z\n      reason: yoyodyne-ifd.130.1 - the one-pane-of-glass architecture\n"+
		"    - action: amended\n      by: architect\n      at: 2026-09-05T17:40:00Z\n      reason: backfill of approved amendment edbbd603 (yoyodyne-ifd.100.1, decided 2026-08-23); published under yoyodyne-ifd.280 after three reviewer reports\n"+
		"    - action: amended\n      by: architect\n      at: 2026-09-07T05:30:00Z\n      reason: yoyodyne-ifd.330 - side conversations designed, judgment-and-read side streams with their own leases\n"+
		"    - action: amended\n      by: architect\n      at: 2026-09-08T05:30:00Z\n      reason: yoyodyne-ifd.330.1 - the record and lease named\n"+
		"---\n# Management\n")
	return root
}

func landingProduct() config.Product {
	return config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	}
}

func architectEntry(id string) backlog.Entry {
	return backlog.Entry{ID: id, Title: id, Status: "open", Executor: domain.ConversationWith(domain.RoleArchitect)}
}

// yoyodyne-ifd.330's landing, read the way the product manager read it at turn
// 512 — and the ones that are not landings: a revision that mentions 280 in
// passing, a revision for 330's child, and 330's own revision read for a role
// whose conversation does not own the design.
func TestAConversationsItemClosesOnTheRevisionThatOpensWithItsIdentifier(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.LandingTracker{}
	lander := ConversationLander{Tracker: tracker, Repository: landingRepository(t), Product: landingProduct()}
	entries := []backlog.Entry{
		architectEntry("yoyodyne-ifd.330"),
		architectEntry("yoyodyne-ifd.280"),
		architectEntry("yoyodyne-ifd.306"),
		{ID: "yoyodyne-ifd.330.1", Title: "The record and lease", Status: "open"},
		{ID: "yoyodyne-ifd.82.1", Title: "Scope team mode", Status: "open", Executor: domain.ConversationWith(domain.RoleProductManager)},
		{ID: "yoyodyne-ifd.250", Title: "Settle the docket entries", Status: "open", Executor: domain.ConversationWith(domain.RoleDevelopmentManager)},
	}

	sweep, err := lander.Settle(context.Background(), entries, itemsFor(entries))
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if len(sweep.Landed) != 2 || sweep.Landed[0].WorkItemID != "yoyodyne-ifd.330" || sweep.Landed[1].WorkItemID != "yoyodyne-ifd.82.1" {
		t.Fatalf("landed = %#v, want 330 on the architect's revision and 82.1 on the product manager's, and nothing else", sweep.Landed)
	}
	if len(sweep.Problems) != 0 {
		t.Fatalf("problems = %v, want none", sweep.Problems)
	}
	landed := sweep.Landed[0]
	if landed.Document != "docs/designs/management-and-supervision.md" || landed.RevisedAt.Format("2006-01-02T15:04") != "2026-09-07T05:30" {
		t.Fatalf("landed = %#v, want the side-conversations revision named", landed)
	}
	// The close carries the whole account: the document, the revision, the
	// convention it was read by, and the revision's own words.
	reason := tracker.Closed["yoyodyne-ifd.330"]
	for _, want := range []string{"Closed by the harness", "no named document was checked", "architect revision of docs/designs/management-and-supervision.md", "2026-09-07T05:30:00Z", "opens with this item's identifier", "side conversations designed"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("close reason %q never says %q", reason, want)
		}
	}
	// The revision was written onto the item before the close, so the item
	// carries what it was closed on.
	if got := tracker.Current("yoyodyne-ifd.330"); got != "docs/designs/management-and-supervision.md@2026-09-07T05:30:00Z" {
		t.Fatalf("landing recorded on 330 = %q, want the document and the revision's time", got)
	}
	// 280 is mentioned and not landed; 330.1 is a developer item however its
	// revision reads; 250 is the development manager's, who owns no document.
	for _, id := range []string{"yoyodyne-ifd.280", "yoyodyne-ifd.306", "yoyodyne-ifd.330.1", "yoyodyne-ifd.250"} {
		if _, closed := tracker.Closed[id]; closed {
			t.Fatalf("%s was closed, want it left open", id)
		}
	}
	if !strings.Contains(sweep.Render(), "yoyodyne-ifd.330 was closed: its architect landed as the 2026-09-07 05:30:00Z revision") {
		t.Fatalf("rendered = %q, want the close readable by an operator", sweep.Render())
	}
}

// A queue with nothing a document could land is not a reading of the
// repository at all, so the pull pays nothing for this where it has nothing to
// find — and a repository whose homes cannot be read closes nothing and says
// so, rather than closing on a document nobody could read.
func TestALandingSweepReadsNothingItHasNoUseForAndClosesNothingItCannotRead(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.LandingTracker{}
	unreadable := ConversationLander{Tracker: tracker, Repository: filepath.Join(t.TempDir(), "missing"), Product: landingProduct()}
	sweep, err := unreadable.Settle(context.Background(), []backlog.Entry{
		{ID: "yoyodyne-ifd.1", Status: "open"},
		{ID: "yoyodyne-ifd.250", Status: "open", Executor: domain.ConversationWith(domain.RoleDevelopmentManager)},
	}, nil)
	if err != nil || len(sweep.Landed) != 0 || len(sweep.Problems) != 0 {
		t.Fatalf("Settle() over nothing landable = %#v, %v, want nothing read and nothing said", sweep, err)
	}
	if _, err := unreadable.Settle(context.Background(), []backlog.Entry{architectEntry("yoyodyne-ifd.330")}, itemsFor([]backlog.Entry{architectEntry("yoyodyne-ifd.330")})); err == nil {
		t.Fatal("Settle() over an unreadable repository = nil error, want the reading reported")
	}
	if len(tracker.Closed) != 0 {
		t.Fatalf("closed = %v, want nothing closed on a repository nobody could read", tracker.Closed)
	}
}

// The remedy every document names for a wrong close is reopening the item with
// a note, and it holds only if the next pull does not close it again. The item
// the harness closed carries the revision it was closed on; reopened, it comes
// back into the queue carrying it, and the sweep that reads the same revision
// again leaves it where the person put it — until a later revision opening
// with its identifier lands, which is a new landing and closes it again.
func TestAReopenedItemIsNotClosedAgainOnTheSameRevision(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.LandingTracker{}
	lander := ConversationLander{Tracker: tracker, Repository: landingRepository(t), Product: landingProduct()}
	first, err := lander.Settle(context.Background(), []backlog.Entry{architectEntry("yoyodyne-ifd.330")}, itemsFor([]backlog.Entry{architectEntry("yoyodyne-ifd.330")}))
	if err != nil || len(first.Landed) != 1 {
		t.Fatalf("Settle() = %#v, %v, want 330 closed on its landing", first, err)
	}

	// Reopened by hand: the item is back in the queue, open, carrying the
	// landing the harness wrote on it.
	reopened := architectEntry("yoyodyne-ifd.330")
	reopened.Landing = tracker.Current("yoyodyne-ifd.330")
	delete(tracker.Closed, "yoyodyne-ifd.330")
	second, err := lander.Settle(context.Background(), []backlog.Entry{reopened}, itemsFor([]backlog.Entry{reopened}))
	if err != nil {
		t.Fatalf("Settle() after the reopen error = %v", err)
	}
	if len(second.Landed) != 0 || len(second.Problems) != 0 {
		t.Fatalf("Settle() after the reopen = %#v, want the item left open", second)
	}
	if len(second.Reopened) != 1 || second.Reopened[0] != "yoyodyne-ifd.330" {
		t.Fatalf("reopened = %v, want the item named as one the harness is leaving alone", second.Reopened)
	}
	if _, closed := tracker.Closed["yoyodyne-ifd.330"]; closed {
		t.Fatal("the reopened item was closed again on the revision it was reopened from")
	}
	if len(tracker.Landings["yoyodyne-ifd.330"]) != 1 {
		t.Fatalf("landings written = %v, want nothing written to a reopened item", tracker.Landings["yoyodyne-ifd.330"])
	}
	// A landing the harness cannot read back is still the harness's own mark,
	// and an item carrying one is left alone rather than closed over it.
	garbled := architectEntry("yoyodyne-ifd.330")
	garbled.Landing = "not a landing anybody wrote"
	if sweep, err := lander.Settle(context.Background(), []backlog.Entry{garbled}, itemsFor([]backlog.Entry{garbled})); err != nil || len(sweep.Landed) != 0 || len(sweep.Reopened) != 1 {
		t.Fatalf("Settle() over an unreadable landing = %#v, %v, want the item left alone", sweep, err)
	}

	// A later revision opening with the identifier is a new landing.
	root := landingRepository(t)
	path := filepath.Join(root, "docs", "designs", "management-and-supervision.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	amended := strings.Replace(string(content), "---\n# Management\n",
		"    - action: amended\n      by: architect\n      at: 2026-09-12T09:00:00Z\n      reason: yoyodyne-ifd.330 - the merge-back ratified after the reopen\n---\n# Management\n", 1)
	if err := os.WriteFile(path, []byte(amended), 0o644); err != nil {
		t.Fatal(err)
	}
	later := ConversationLander{Tracker: tracker, Repository: root, Product: landingProduct()}
	third, err := later.Settle(context.Background(), []backlog.Entry{reopened}, itemsFor([]backlog.Entry{reopened}))
	if err != nil || len(third.Landed) != 1 || third.Landed[0].RevisedAt.Format("2006-01-02") != "2026-09-12" {
		t.Fatalf("Settle() on a later revision = %#v, %v, want the item closed on the new landing", third, err)
	}
	// The earliest revision that lands the item is the landing where the item
	// has not been closed on it; here it has, so the one after it is.
	if got := tracker.Current("yoyodyne-ifd.330"); !strings.HasSuffix(got, "@2026-09-12T09:00:00Z") {
		t.Fatalf("landing recorded = %q, want the later revision", got)
	}
}

// A landing the tracker will not close is named rather than lost: the item is
// done and nothing will close it, which is exactly the state this exists to
// stop being invisible. And the revision recorded ahead of the close is taken
// back off it, so the next pull tries again rather than reading a reopen.
func TestALandingTheTrackerRefusesToCloseIsReported(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.LandingTracker{Refused: map[string]error{"yoyodyne-ifd.330": errors.New("bd: the store is locked")}}
	lander := ConversationLander{Tracker: tracker, Repository: landingRepository(t), Product: landingProduct()}
	sweep, err := lander.Settle(context.Background(), []backlog.Entry{architectEntry("yoyodyne-ifd.330")}, itemsFor([]backlog.Entry{architectEntry("yoyodyne-ifd.330")}))
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if len(sweep.Landed) != 0 || len(sweep.Problems) != 1 {
		t.Fatalf("sweep = %#v, want the refused close named as a problem", sweep)
	}
	for _, want := range []string{"yoyodyne-ifd.330", "docs/designs/management-and-supervision.md", "closed by hand", "the store is locked"} {
		if !strings.Contains(sweep.Problems[0], want) {
			t.Fatalf("problem %q never says %q", sweep.Problems[0], want)
		}
	}
	if got := tracker.Current("yoyodyne-ifd.330"); got != "" {
		t.Fatalf("landing left on the unclosed item = %q, want it cleared so the next pull retries", got)
	}

	// A revision that cannot be recorded closes nothing: the close would be one
	// a reopen could not hold against.
	unrecordable := &orchestratortest.LandingTracker{Unrecordable: map[string]error{"yoyodyne-ifd.330": errors.New("bd: the store is locked")}}
	sweep, err = ConversationLander{Tracker: unrecordable, Repository: landingRepository(t), Product: landingProduct()}.Settle(context.Background(), []backlog.Entry{architectEntry("yoyodyne-ifd.330")}, itemsFor([]backlog.Entry{architectEntry("yoyodyne-ifd.330")}))
	if err != nil || len(sweep.Landed) != 0 || len(sweep.Problems) != 1 || len(unrecordable.Closed) != 0 {
		t.Fatalf("Settle() with the record refused = %#v, %v, closed %v; want nothing closed and the refusal named", sweep, err, unrecordable.Closed)
	}
	if !strings.Contains(sweep.Problems[0], "could not be recorded on it, so it was not closed") {
		t.Fatalf("problem %q never says the record was refused", sweep.Problems[0])
	}

	// A record that could be written and not cleared after a refused close is
	// the one state a later pull reads as a reopen, so it is said with what to
	// clear.
	stuck := &orchestratortest.LandingTracker{
		Refused:     map[string]error{"yoyodyne-ifd.330": errors.New("bd: the store is locked")},
		Unclearable: map[string]error{"yoyodyne-ifd.330": errors.New("bd: still locked")},
	}
	sweep, err = ConversationLander{Tracker: stuck, Repository: landingRepository(t), Product: landingProduct()}.Settle(context.Background(), []backlog.Entry{architectEntry("yoyodyne-ifd.330")}, itemsFor([]backlog.Entry{architectEntry("yoyodyne-ifd.330")}))
	if err != nil || len(sweep.Problems) != 1 {
		t.Fatalf("Settle() with the clear refused = %#v, %v, want one problem", sweep, err)
	}
	for _, want := range []string{"could not be cleared", "reads the item as reopened", beads.LandingKey, "still locked"} {
		if !strings.Contains(sweep.Problems[0], want) {
			t.Fatalf("problem %q never says %q", sweep.Problems[0], want)
		}
	}
}

// The convention is that the reason opens with the identifier as a whole word:
// quoted or bracketed reasons open with it too, and a longer identifier or a
// mention further in does not.
func TestARevisionReasonOpensWithAnIdentifierAsAWholeWord(t *testing.T) {
	t.Parallel()

	for reason, want := range map[string]bool{
		"yoyodyne-ifd.330 - side conversations designed":           true,
		"yoyodyne-ifd.330: side conversations designed":            true,
		"'yoyodyne-ifd.330 - the reason was quoted for its colon'": true,
		"  yoyodyne-ifd.330":                                   true,
		"(yoyodyne-ifd.330) side conversations":                true,
		"yoyodyne-ifd.330.1 - the record and lease":            false,
		"yoyodyne-ifd.3301 - something else":                   false,
		"yoyodyne-ifd.330-followup":                            false,
		"published under yoyodyne-ifd.330 after three reports": false,
		"": false,
	} {
		if got := opensWith(reason, "yoyodyne-ifd.330"); got != want {
			t.Fatalf("opensWith(%q) = %v, want %v", reason, got, want)
		}
	}
}

// itemsFor is the tracker's reading of the entries as the queue read them: each
// item with its title and executor and no text naming any document.
func itemsFor(entries []backlog.Entry) map[string]beads.WorkItem {
	items := map[string]beads.WorkItem{}
	for _, entry := range entries {
		items[entry.ID] = beads.WorkItem{ID: entry.ID, Title: entry.Title, Status: entry.Status, Executor: entry.Executor}
	}
	return items
}

// designRevision is one revision the architect made of a design.
type designRevision struct {
	at     string
	reason string
}

// namedDesigns are the three designs the incident's item touched, by identifier.
var namedDesigns = []string{"artifact-contract", "v1-harness-design", "portable-agent-configuration"}

// writeDesigns writes the architect's three designs under root, each with the
// revisions given for its identifier after its creation, replacing what was there.
func writeDesigns(t *testing.T, root string, revisions map[string][]designRevision) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "docs", "designs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range namedDesigns {
		var content strings.Builder
		content.WriteString("---\nid: " + id + "\nkind: design\ntitle: " + id + "\nstatus: active\nsupports: []\nrevisions:\n")
		content.WriteString("    - action: created\n      by: architect\n      at: 2026-09-01T12:00:00Z\n      reason: yoyodyne-ifd.1 - " + id + " created\n")
		for _, revision := range revisions[id] {
			content.WriteString("    - action: amended\n      by: architect\n      at: " + revision.at + "\n      reason: " + revision.reason + "\n")
		}
		content.WriteString("---\n# " + id + "\n")
		if err := os.WriteFile(filepath.Join(root, "docs", "designs", id+".md"), []byte(content.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const incidentItem = "yoyodyne-ifd.437.36"

// incidentItemText is the incident item as the tracker holds it: its
// description names one design by path and another by identifier.
func incidentItemText(notes string) map[string]beads.WorkItem {
	return map[string]beads.WorkItem{incidentItem: {
		ID: incidentItem, Title: "Revise the designs", Status: "open", Executor: domain.ConversationWith(domain.RoleArchitect),
		Description: "Amend docs/designs/artifact-contract.md and the v1-harness-design so both carry the contract.",
		Notes:       notes,
	}}
}

func incidentLander(t *testing.T, tracker *orchestratortest.LandingTracker, revisions map[string][]designRevision) (ConversationLander, string) {
	t.Helper()
	root := t.TempDir()
	writeDesigns(t, root, revisions)
	return ConversationLander{Tracker: tracker, Repository: root, Product: landingProduct()}, root
}

// The incident replayed. The architect revised a third design under the item's
// identifier and left the two documents the item names untouched. The item stays
// open, the revision is written onto it as related exactly once, and the close
// comes only when each named document has a revision opening with the identifier.
func TestAnItemNamingDocumentsIsNotClosedByARevisionOfAnotherDocument(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.LandingTracker{}
	revisions := map[string][]designRevision{
		"portable-agent-configuration": {{"2026-10-01T10:00:00Z", incidentItem + " - the portable configuration revised"}},
	}
	lander, root := incidentLander(t, tracker, revisions)
	entries := []backlog.Entry{architectEntry(incidentItem)}

	sweep, err := lander.Settle(context.Background(), entries, incidentItemText(""))
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if len(sweep.Landed) != 0 || len(tracker.Closed) != 0 || len(tracker.Landings[incidentItem]) != 0 {
		t.Fatalf("sweep = %#v, closed %v, landings %v; want the item left open with nothing recorded", sweep, tracker.Closed, tracker.Landings)
	}
	if len(tracker.Notes[incidentItem]) != 1 {
		t.Fatalf("notes written = %v, want exactly one note on the item", tracker.Notes[incidentItem])
	}
	note := tracker.Notes[incidentItem][0]
	for _, want := range []string{"docs/designs/portable-agent-configuration.md@2026-10-01T10:00:00Z", "does not name", "docs/designs/artifact-contract.md", "docs/designs/v1-harness-design.md"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note %q never says %q", note, want)
		}
	}
	if len(sweep.Related) != 1 || sweep.Related[0] != incidentItem {
		t.Fatalf("related = %v, want the item named", sweep.Related)
	}

	// The next pull reads the item with the note on it and says nothing more.
	again, err := lander.Settle(context.Background(), entries, incidentItemText(note))
	if err != nil {
		t.Fatalf("Settle() second pull error = %v", err)
	}
	if len(tracker.Notes[incidentItem]) != 1 || len(again.Related) != 0 || len(again.Landed) != 0 {
		t.Fatalf("second pull = %#v, notes %v; want no further note and no close", again, tracker.Notes[incidentItem])
	}

	// A revision of one named document is not enough, and says nothing new.
	revisions["artifact-contract"] = []designRevision{{"2026-10-02T10:00:00Z", incidentItem + " - the contract amended"}}
	writeDesigns(t, root, revisions)
	half, err := lander.Settle(context.Background(), entries, incidentItemText(note))
	if err != nil || len(half.Landed) != 0 || len(tracker.Closed) != 0 {
		t.Fatalf("Settle() with one named document revised = %#v, %v, closed %v; want the item open", half, err, tracker.Closed)
	}
	if len(tracker.Notes[incidentItem]) != 1 {
		t.Fatalf("notes written = %v, want the already noted revision not noted again", tracker.Notes[incidentItem])
	}

	// The second named document completes the set, and the close rests on both.
	revisions["v1-harness-design"] = []designRevision{{"2026-10-03T10:00:00Z", incidentItem + " - the harness design amended"}}
	writeDesigns(t, root, revisions)
	done, err := lander.Settle(context.Background(), entries, incidentItemText(note))
	if err != nil || len(done.Landed) != 1 {
		t.Fatalf("Settle() with both named documents revised = %#v, %v, want the item closed", done, err)
	}
	landed := done.Landed[0]
	if len(landed.Named) != 2 {
		t.Fatalf("named = %#v, want one revision per named document", landed.Named)
	}
	reason := tracker.Closed[incidentItem]
	for _, want := range []string{"every document this item's text names", "docs/designs/artifact-contract.md", "docs/designs/v1-harness-design.md"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("close reason %q never says %q", reason, want)
		}
	}
	if got := tracker.Current(incidentItem); got != "docs/designs/v1-harness-design.md@2026-10-03T10:00:00Z" {
		t.Fatalf("landing recorded = %q, want the later of the two revisions", got)
	}
}

// An item whose text names no document has nothing to check the revision
// against, so the first revision opening with its identifier closes it, in
// whatever document, and the close says nothing was checked.
func TestAnItemNamingNoDocumentClosesOnAnyRevisionAndSaysSo(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.LandingTracker{}
	lander, _ := incidentLander(t, tracker, map[string][]designRevision{
		"portable-agent-configuration": {{"2026-10-01T10:00:00Z", incidentItem + " - the portable configuration revised"}},
	})
	entries := []backlog.Entry{architectEntry(incidentItem)}
	sweep, err := lander.Settle(context.Background(), entries, itemsFor(entries))
	if err != nil || len(sweep.Landed) != 1 {
		t.Fatalf("Settle() = %#v, %v, want the item closed", sweep, err)
	}
	if !strings.Contains(tracker.Closed[incidentItem], "no named document was checked") {
		t.Fatalf("close reason %q never says no named document was checked", tracker.Closed[incidentItem])
	}
	if len(tracker.Notes) != 0 || len(sweep.Related) != 0 || len(sweep.Landed[0].Named) != 0 {
		t.Fatalf("notes %v, related %v, named %v; want nothing noted", tracker.Notes, sweep.Related, sweep.Landed[0].Named)
	}
}

// An entry the tracker's reading does not hold is one whose text is unknown, so
// it is left open and named rather than closed on a guess.
func TestAnEntryMissingFromTheItemsIsLeftOpenAndNamed(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.LandingTracker{}
	lander, _ := incidentLander(t, tracker, map[string][]designRevision{
		"artifact-contract": {{"2026-10-01T10:00:00Z", incidentItem + " - the contract amended"}},
	})
	sweep, err := lander.Settle(context.Background(), []backlog.Entry{architectEntry(incidentItem)}, map[string]beads.WorkItem{})
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if len(sweep.Landed) != 0 || len(tracker.Closed) != 0 {
		t.Fatalf("sweep = %#v, closed %v, want the item left open", sweep, tracker.Closed)
	}
	if len(sweep.Problems) != 1 || !strings.Contains(sweep.Problems[0], incidentItem) {
		t.Fatalf("problems = %v, want the item named", sweep.Problems)
	}
}

// A note the tracker refuses is reported, and costs the close nothing: an item
// whose named documents have all landed closes, with the unnamed revision's
// note the thing that failed.
func TestAFailedRelatedNoteIsReportedAndDoesNotStopTheClose(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.LandingTracker{Unnotable: map[string]error{incidentItem: errors.New("bd: the store is locked")}}
	lander, _ := incidentLander(t, tracker, map[string][]designRevision{
		"artifact-contract":            {{"2026-10-02T10:00:00Z", incidentItem + " - the contract amended"}},
		"v1-harness-design":            {{"2026-10-03T10:00:00Z", incidentItem + " - the harness design amended"}},
		"portable-agent-configuration": {{"2026-10-01T10:00:00Z", incidentItem + " - the portable configuration revised"}},
	})
	entries := []backlog.Entry{architectEntry(incidentItem)}
	sweep, err := lander.Settle(context.Background(), entries, incidentItemText(""))
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if len(sweep.Problems) != 1 || !strings.Contains(sweep.Problems[0], "the store is locked") || !strings.Contains(sweep.Problems[0], incidentItem) {
		t.Fatalf("problems = %v, want the refused note named", sweep.Problems)
	}
	if len(sweep.Related) != 0 {
		t.Fatalf("related = %v, want an item whose note failed not reported as noted", sweep.Related)
	}
	if len(sweep.Landed) != 1 || tracker.Closed[incidentItem] == "" {
		t.Fatalf("sweep = %#v, closed %v, want the item closed all the same", sweep, tracker.Closed)
	}
}

// A reopen holds for an item naming several documents, and what closes it again
// is a later revision of one of them.
func TestAReopenedItemNamingDocumentsClosesAgainOnALaterRevisionOfOneOfThem(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.LandingTracker{}
	revisions := map[string][]designRevision{
		"artifact-contract": {{"2026-10-02T10:00:00Z", incidentItem + " - the contract amended"}},
		"v1-harness-design": {{"2026-10-03T10:00:00Z", incidentItem + " - the harness design amended"}},
	}
	lander, root := incidentLander(t, tracker, revisions)
	entries := []backlog.Entry{architectEntry(incidentItem)}
	first, err := lander.Settle(context.Background(), entries, incidentItemText(""))
	if err != nil || len(first.Landed) != 1 {
		t.Fatalf("Settle() = %#v, %v, want the item closed", first, err)
	}

	reopened := architectEntry(incidentItem)
	reopened.Landing = tracker.Current(incidentItem)
	delete(tracker.Closed, incidentItem)
	second, err := lander.Settle(context.Background(), []backlog.Entry{reopened}, incidentItemText(""))
	if err != nil || len(second.Landed) != 0 || len(second.Problems) != 0 {
		t.Fatalf("Settle() after the reopen = %#v, %v, want the item left open", second, err)
	}
	if len(second.Reopened) != 1 || second.Reopened[0] != incidentItem {
		t.Fatalf("reopened = %v, want the item named as one left alone", second.Reopened)
	}
	if _, closed := tracker.Closed[incidentItem]; closed {
		t.Fatal("the reopened item was closed again on the revisions it was reopened from")
	}

	revisions["artifact-contract"] = append(revisions["artifact-contract"], designRevision{"2026-10-05T09:00:00Z", incidentItem + " - the contract amended again"})
	writeDesigns(t, root, revisions)
	third, err := lander.Settle(context.Background(), []backlog.Entry{reopened}, incidentItemText(""))
	if err != nil || len(third.Landed) != 1 {
		t.Fatalf("Settle() on a later revision = %#v, %v, want the item closed again", third, err)
	}
	if got := tracker.Current(incidentItem); got != "docs/designs/artifact-contract.md@2026-10-05T09:00:00Z" {
		t.Fatalf("landing recorded = %q, want the later revision", got)
	}
	if len(third.Landed[0].Named) != 2 {
		t.Fatalf("named = %#v, want a revision for each named document", third.Landed[0].Named)
	}
}

// A document is named by its path or by its identifier as a whole word, and an
// identifier inside a longer word names nothing.
func TestNamesDocument(t *testing.T) {
	t.Parallel()

	document := artifact.Artifact{ID: "artifact-contract", Path: "docs/designs/artifact-contract.md"}
	for name, test := range map[string]struct {
		text     string
		document artifact.Artifact
		want     bool
	}{
		"path":                  {"Amend docs/designs/artifact-contract.md today", document, true},
		"identifier":            {"Amend the artifact-contract design", document, true},
		"identifier at the end": {"the artifact-contract", document, true},
		"identifier alone":      {"artifact-contract", document, true},
		"longer hyphenated":     {"see artifact-contract-notes for more", document, false},
		"prefixed hyphenated":   {"see old-artifact-contract for more", document, false},
		"underscore continues":  {"artifact-contract_v2", document, false},
		"trailing full stop":    {"Amend artifact-contract.", document, true},
		"trailing parenthesis":  {"(the artifact-contract)", document, true},
		"second occurrence":     {"artifact-contract-notes and artifact-contract", document, true},
		"empty identifier":      {"anything at all", artifact.Artifact{}, false},
		"other document":        {"Amend the v1-harness-design", document, false},
	} {
		if got := namesDocument(test.text, test.document); got != test.want {
			t.Errorf("%s: namesDocument(%q) = %v, want %v", name, test.text, got, test.want)
		}
	}
}
