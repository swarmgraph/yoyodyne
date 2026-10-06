package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	sweep, err := lander.Settle(context.Background(), entries)
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
	for _, want := range []string{"Closed by the harness", "architect revision of docs/designs/management-and-supervision.md", "2026-09-07T05:30:00Z", "opens with this item's identifier", "side conversations designed"} {
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
	})
	if err != nil || len(sweep.Landed) != 0 || len(sweep.Problems) != 0 {
		t.Fatalf("Settle() over nothing landable = %#v, %v, want nothing read and nothing said", sweep, err)
	}
	if _, err := unreadable.Settle(context.Background(), []backlog.Entry{architectEntry("yoyodyne-ifd.330")}); err == nil {
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
	first, err := lander.Settle(context.Background(), []backlog.Entry{architectEntry("yoyodyne-ifd.330")})
	if err != nil || len(first.Landed) != 1 {
		t.Fatalf("Settle() = %#v, %v, want 330 closed on its landing", first, err)
	}

	// Reopened by hand: the item is back in the queue, open, carrying the
	// landing the harness wrote on it.
	reopened := architectEntry("yoyodyne-ifd.330")
	reopened.Landing = tracker.Current("yoyodyne-ifd.330")
	delete(tracker.Closed, "yoyodyne-ifd.330")
	second, err := lander.Settle(context.Background(), []backlog.Entry{reopened})
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
	if sweep, err := lander.Settle(context.Background(), []backlog.Entry{garbled}); err != nil || len(sweep.Landed) != 0 || len(sweep.Reopened) != 1 {
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
	third, err := later.Settle(context.Background(), []backlog.Entry{reopened})
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
	sweep, err := lander.Settle(context.Background(), []backlog.Entry{architectEntry("yoyodyne-ifd.330")})
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
	sweep, err = ConversationLander{Tracker: unrecordable, Repository: landingRepository(t), Product: landingProduct()}.Settle(context.Background(), []backlog.Entry{architectEntry("yoyodyne-ifd.330")})
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
	sweep, err = ConversationLander{Tracker: stuck, Repository: landingRepository(t), Product: landingProduct()}.Settle(context.Background(), []backlog.Entry{architectEntry("yoyodyne-ifd.330")})
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
