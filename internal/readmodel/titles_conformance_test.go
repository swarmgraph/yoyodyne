package readmodel

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// TestClosedWorkIsTitledFromTheRealListing pins title resolution against bd
// itself rather than a fake keyed on the empty status. Most items a report or a
// sweep names have closed by the time anybody reads it, and bd's own listing
// given no status leaves closed work out, so a resolution that read that
// listing would show every one of them as unknown to the tracker. The item is
// closed the way the harness closes one, through the client the surfaces are
// handed, and the open one beside it is the control.
func TestClosedWorkIsTitledFromTheRealListing(t *testing.T) {
	t.Parallel()

	project := newTitlesTracker(t)
	client := beads.Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: 10 * time.Minute}
	ctx := context.Background()

	finished, err := client.Create(ctx, beads.NewWorkItem{
		Title:       "Price a resumed session at what it moved by",
		Description: "Landed before the report naming it was read.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := client.Complete(ctx, finished.ID, "landed"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	unfinished, err := client.Create(ctx, beads.NewWorkItem{
		Title:       "Say the provider's reset in local time",
		Description: "Still somebody's.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	titles, err := ReadWorkItemTitles(ctx, Sources{Tracker: client})
	if err != nil {
		t.Fatalf("ReadWorkItemTitles() error = %v", err)
	}
	if title, known := titles.Title(finished.ID); !known || title != finished.Title {
		t.Fatalf("Title(%s) = %q (known = %v), want the closed item's title %q; a closed item a report names "+
			"would read as unknown to the tracker", finished.ID, title, known, finished.Title)
	}
	if title, known := titles.Title(unfinished.ID); !known || title != unfinished.Title {
		t.Fatalf("Title(%s) = %q (known = %v), want the open item's title %q", unfinished.ID, title, known, unfinished.Title)
	}

	text := "Blocked on " + finished.ID + " until it landed."
	got := titles.Cite(text)
	if want := titles.Name(finished.ID); !strings.Contains(got, want) {
		t.Fatalf("Cite(%q) = %q, want the closed item shown as %q", text, got, want)
	}
	if strings.Contains(got, "unknown to the tracker") {
		t.Fatalf("Cite(%q) = %q, shows a closed item as unknown to the tracker", text, got)
	}
}

// newTitlesTracker is a fresh bd project, or a skipped test where bd is not
// installed. It is internal/beads' conformance fixture, which a test outside
// that package cannot reach.
func newTitlesTracker(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("bd"); err != nil {
		t.Skipf("bd is not installed: %v", err)
	}
	root := t.TempDir()
	project := filepath.Join(root, "tracker")
	runTitlesCommand(t, root, "git", "init", "-q", "-b", "main", project)
	// bd init commits what it writes, which needs an identity the machine may
	// not have configured.
	runTitlesCommand(t, project, "git", "config", "user.email", "yoyodyne@example.invalid")
	runTitlesCommand(t, project, "git", "config", "user.name", "Yoyodyne Test")
	runTitlesCommand(t, project, "bd", "init")
	return project
}

func runTitlesCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()

	command := exec.Command(name, args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v error = %v: %s", name, args, err, output)
	}
}
