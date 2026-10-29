package admission

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// family is the backlog with one parent's children added, as the tracker held
// them when the next child was asked for.
func family(parent string, children ...[2]string) []beads.WorkItem {
	admitted := backlog()
	for _, child := range children {
		admitted = append(admitted, beads.WorkItem{ID: child[0], Title: child[1], Parent: parent, Status: "open"})
	}
	return admitted
}

// The run-lifecycle split (yoyodyne-ifd.428.126.1): seven move pieces on one
// template, "Run lifecycle move N: … write run records through narrow updates",
// each naming a different part of the pipeline. Four were refused against the
// first one created. Each is admitted here in turn, against every piece admitted
// before it, which is how the tracker meets them.
func TestSiblingsOnOneTemplateWithDifferentSubjectsAreAllAdmitted(t *testing.T) {
	t.Parallel()

	const parent = "yoyodyne-ifd.428.126.1"
	admitted := family(parent,
		[2]string{"yoyodyne-ifd.428.126.1.1", "A make test check refuses a whole run-record save, a discarded save error, or a direct run status or step write outside internal/runstate, except at listed sites, and fails when one is added"},
		[2]string{"yoyodyne-ifd.428.126.1.2", "Each run keeps exactly what each of its invocations was sent, or a record that rebuilds it exactly, readable from the run"},
	)
	pieces := []string{
		"Run lifecycle move 1: the pipeline's completion and ending paths write run records through narrow updates",
		"Run lifecycle move 2: pipeline pauses write run records through narrow updates",
		"Run lifecycle move 3: pipeline development and review steps write run records through narrow updates",
		"Run lifecycle move 4: continuations write run records through narrow updates",
		"Run lifecycle move 5: carrying out triage decisions writes run records through narrow updates",
		"Run lifecycle move 6: reconciliation writes run records through narrow updates, beyond the cleanup progress the run-record fix moves",
		"Run lifecycle move 7: document landing and merge-queue leftovers write run records through narrow updates",
	}
	for i, title := range pieces {
		if matches := Resembling(Candidate{Title: title, Parent: parent}, admitted); len(matches) != 0 {
			t.Fatalf("piece %d %q matched %s, want it admitted beside the pieces before it", i+1, title, Describe(matches))
		}
		admitted = append(admitted, beads.WorkItem{ID: parent + ".x" + string(rune('1'+i)), Title: title, Parent: parent, Status: "open"})
	}

	// A repeat of one of them is still the same work, whether it is the same
	// sentence or the same subject renumbered.
	for _, repeat := range []string{
		pieces[1],
		"Run lifecycle move 8: pipeline pauses write run records through narrow updates",
	} {
		matches := Resembling(Candidate{Title: repeat, Parent: parent}, admitted)
		if got := ids(matches); len(got) != 1 || got[0] != parent+".x2" {
			t.Fatalf("Resembling(%q) = %v, want the pipeline-pauses piece", repeat, got)
		}
	}
}

// The orchestrator test move (yoyodyne-ifd.429.14): groups on one template that
// differ only in which tests they move, the first-half and second-half groups
// differing by a single word.
func TestSiblingsThatDifferOnlyInTheTestsTheyMoveAreAllAdmitted(t *testing.T) {
	t.Parallel()

	const parent = "yoyodyne-ifd.429.14"
	admitted := family(parent,
		[2]string{"yoyodyne-ifd.429.14.1", "Move the first half of pipeline_test.go's orchestrator tests that touch no internals into their own package"},
		[2]string{"yoyodyne-ifd.429.14.2", "Move the branch-protection and landing orchestrator tests that touch no internals into their own package"},
		[2]string{"yoyodyne-ifd.429.14.3", "Re-take the orchestrator race-binary measure once every movable test has moved, and record it in the diagnosis"},
		[2]string{"yoyodyne-ifd.429.14.4", "Move the reconciliation and stopped-run recovery orchestrator tests that touch no internals into their own package"},
	)
	for _, title := range []string{
		"Move the second half of pipeline_test.go's orchestrator tests that touch no internals into their own package",
		"Move the merge queue orchestrator tests that touch no internals into their own package",
		"Move the scheduler orchestrator tests that touch no internals into their own package",
	} {
		if matches := Resembling(Candidate{Title: title, Parent: parent}, admitted); len(matches) != 0 {
			t.Fatalf("%q matched %s, want it admitted", title, Describe(matches))
		}
	}

	matches := Resembling(Candidate{
		Title:  "Move the branch-protection and landing orchestrator tests that touch no internals into their own package",
		Parent: parent,
	}, admitted)
	if got := ids(matches); len(got) != 1 || got[0] != "yoyodyne-ifd.429.14.2" {
		t.Fatalf("a repeat of the landing group matched %v, want yoyodyne-ifd.429.14.2", got)
	}
	// The refusal says what in the two titles was read as the same, so the role
	// refused can see why and say what separates them.
	for _, word := range []string{"yoyodyne-ifd.429.14", "branch protection landing orchestrator tests"} {
		if !strings.Contains(matches[0].Because, word) {
			t.Fatalf("Because = %q, want it to name %q", matches[0].Because, word)
		}
	}
}

// Two siblings whose titles are the same but whose descriptions name different
// files are different work; the same files, or one naming everything the other
// does and more, is the same work.
func TestSiblingsNamingDifferentFilesAreDifferentWork(t *testing.T) {
	t.Parallel()

	const parent = "yoyodyne-ifd.428.126.1"
	const title = "Run lifecycle move: these files write run records through narrow updates"
	admitted := append(backlog(), beads.WorkItem{
		ID:          parent + ".4",
		Title:       title,
		Description: "Moves repaircontinue.go and continue.go in internal/orchestrator.",
		Parent:      parent,
		Status:      "open",
	})

	different := Candidate{Title: title, Parent: parent, Description: "Moves pause.go and pipelinepause.go in internal/orchestrator."}
	if matches := Resembling(different, admitted); len(matches) != 0 {
		t.Fatalf("Resembling() = %s, want nothing for a sibling naming different files", Describe(matches))
	}

	for _, description := range []string{
		"Moves `repaircontinue.go` and continue.go, in internal/orchestrator.",
		"Moves repaircontinue.go, continue.go and continuation_test.go in internal/orchestrator.",
		"",
	} {
		matches := Resembling(Candidate{Title: title, Parent: parent, Description: description}, admitted)
		if got := ids(matches); len(got) != 1 || got[0] != parent+".4" {
			t.Fatalf("Resembling() with description %q = %v, want the sibling naming the same files", description, got)
		}
	}
	matches := Resembling(Candidate{Title: title, Parent: parent, Description: "Moves repaircontinue.go and continue.go."}, admitted)
	if len(matches) != 1 || !strings.Contains(matches[0].Because, "both name continue.go, repaircontinue.go") {
		t.Fatalf("Because = %v, want it to name the files both name", matches)
	}
}

func TestNamedThingsAreFilesAndPackagesRatherThanProse(t *testing.T) {
	t.Parallel()

	named := namedThings("Move pipeline_test.go's tests (see internal/orchestrator, docs/x.md). And/or read/write, e.g. v1.2, yoyodyne-ifd.429.14, https://example.com/a.")
	var got []string
	for name := range named {
		got = append(got, name)
	}
	want := map[string]bool{"pipeline_test.go": true, "internal/orchestrator": true, "docs/x.md": true, "read/write": true}
	if len(got) != len(want) {
		t.Fatalf("namedThings() = %v, want %v", got, want)
	}
	for _, name := range got {
		if !want[name] {
			t.Fatalf("namedThings() = %v, want %v", got, want)
		}
	}
}
