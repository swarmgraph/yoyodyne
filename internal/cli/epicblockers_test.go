package cli

import (
	"reflect"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

func waitingOn(ids ...string) []beads.Dependency {
	var dependencies []beads.Dependency
	for _, id := range ids {
		dependencies = append(dependencies, beads.Dependency{ID: id, Type: beads.BlocksDependency})
	}
	return dependencies
}

// setItem replaces the bench's item of that identifier, or adds it.
func (b *epicBench) setItem(item beads.WorkItem) {
	for at := range *b.items {
		if (*b.items)[at].ID == item.ID {
			(*b.items)[at] = item
			return
		}
	}
	*b.items = append(*b.items, item)
}

// A start is refused while a member waits on open work outside the epic, through
// a chain several links deep, naming each item and the one command that names the
// epic with all of them; that command then starts it. A closed prerequisite
// refuses nothing. Nothing is held between the two commands, so the second reads
// the links again as a restarted harness would.
func TestEpicStartIsRefusedWhileOutsideWorkCouldBlockAMember(t *testing.T) {
	bench := newEpicBench(t)
	bench.setItem(beads.WorkItem{ID: "yoyodyne-a.2", Title: "Linked", Status: "blocked", Dependencies: append(waitingOn("yoyodyne-x", "yoyodyne-a.1"), beads.Dependency{ID: "yoyodyne-a", Type: "parent-child"})})
	bench.setItem(beads.WorkItem{ID: "yoyodyne-x", Title: "Outside", Status: "open", Dependencies: waitingOn("yoyodyne-y")})
	bench.setItem(beads.WorkItem{ID: "yoyodyne-y", Title: "Further out", Status: "open"})
	bench.setItem(beads.WorkItem{ID: "yoyodyne-y.1", Title: "Under it", Status: "open", Parent: "yoyodyne-y"})

	bench.refuses([]string{"start", "yoyodyne-a"},
		"cannot be started while open work outside it could block a member",
		"yoyodyne-x (Outside) could block yoyodyne-a.2: yoyodyne-a.2 waits on it",
		"yoyodyne-y (Further out) could block yoyodyne-a.2: yoyodyne-x waits on it, which could block yoyodyne-a.2",
		"yoyodyne-y.1 (Under it) could block yoyodyne-a.2: it is a child of yoyodyne-y",
		"yoyo epic start yoyodyne-a yoyodyne-x yoyodyne-y yoyodyne-y.1")
	if bench.record().On() {
		t.Fatal("a refused start turned epic mode on")
	}

	bench.does("start", "yoyodyne-a", "yoyodyne-x", "yoyodyne-y", "yoyodyne-y.1")
	if got, want := members(bench.record().Epics[0]), []string{"yoyodyne-a.1.1", "yoyodyne-a.2", "yoyodyne-x", "yoyodyne-y", "yoyodyne-y.1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
}

// The epic's own item counts as inside, so work it waits on outside refuses the
// start, and closing that work lets the same start go ahead.
func TestEpicStartIsRefusedWhileTheEpicItselfWaitsOnOutsideWork(t *testing.T) {
	bench := newEpicBench(t)
	bench.setItem(beads.WorkItem{ID: "yoyodyne-a", Title: "Epic A", Status: "open", Description: "What A is.", Dependencies: waitingOn("yoyodyne-loose")})
	bench.refuses([]string{"start", "yoyodyne-a"}, "yoyodyne-loose (Loose) could block yoyodyne-a: yoyodyne-a waits on it", "yoyo epic start yoyodyne-a yoyodyne-loose")

	bench.setItem(beads.WorkItem{ID: "yoyodyne-loose", Title: "Loose", Status: "closed"})
	bench.does("start", "yoyodyne-a")
}

// An add is refused while the item reaches open work outside the epic, naming
// every blocker and the one add that brings the item and all of them in; that add
// makes them members in one write. An item that reaches only what is inside goes
// in as asked.
func TestEpicAddIsRefusedWhileTheItemReachesOutsideWork(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a")
	bench.setItem(beads.WorkItem{ID: "yoyodyne-n", Title: "New", Status: "open", Dependencies: waitingOn("yoyodyne-m", "yoyodyne-a.2")})
	bench.setItem(beads.WorkItem{ID: "yoyodyne-m", Title: "Its prerequisite", Status: "open"})
	bench.setItem(beads.WorkItem{ID: "yoyodyne-m.1", Title: "Its child", Status: "open", Parent: "yoyodyne-m"})

	bench.refuses([]string{"add", "yoyodyne-a", "yoyodyne-n"},
		"adding to epic",
		"yoyodyne-m (Its prerequisite) could block yoyodyne-n: yoyodyne-n waits on it",
		"yoyodyne-m.1 (Its child) could block yoyodyne-n: it is a child of yoyodyne-m",
		"yoyo epic add yoyodyne-a yoyodyne-n yoyodyne-m yoyodyne-m.1")

	bench.does("add", "yoyodyne-a", "yoyodyne-n", "yoyodyne-m", "yoyodyne-m.1")
	epic := bench.record().Epics[0]
	if got, want := members(epic), []string{"yoyodyne-a.1.1", "yoyodyne-a.2", "yoyodyne-n", "yoyodyne-m", "yoyodyne-m.1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
	if added := epic.Members[2:]; added[0].AddedAt != added[1].AddedAt || added[1].AddedAt != added[2].AddedAt {
		t.Fatalf("members added = %+v, want them added as one act", added)
	}
	bench.does("add", "yoyodyne-a", "yoyodyne-loose")
}
