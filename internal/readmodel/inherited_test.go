package readmodel

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// Items held back through a waiting parent are their own group on the
// not-startable line and the dashboard, naming the parent and what it waits on,
// rather than counted among the items nothing here can explain.
func TestItemsBlockedThroughAParentAreAGroupNamingIt(t *testing.T) {
	t.Parallel()

	block := beads.InheritedBlock{Through: []string{"yoyodyne-ifd.433.21"},
		WaitsOn: []beads.Dependency{{ID: "yoyodyne-ifd.437.14", Type: beads.BlocksDependency}}}
	groups := newWaitGroups(Stall{}, switches{})
	for _, id := range []string{"yoyodyne-ifd.433.21.1", "yoyodyne-ifd.433.21.2"} {
		entry := backlog.Entry{ID: id, Status: "open", InheritedBlocks: []beads.InheritedBlock{block}}
		groups.add(entry, entry.HoldKind())
	}
	groups.add(backlog.Entry{ID: "yoyodyne-ifd.8", Status: "open"}, backlog.HeldUnread)

	listed := groups.list()
	if len(listed) != 2 || listed[0].Kind != backlog.HeldInherited || listed[0].Count != 2 || listed[0].Mover != MoverHarness {
		t.Fatalf("groups = %+v, want the inherited group of two first, the harness's", listed)
	}
	says := listed[0].Says()
	if want := "2 are blocked through yoyodyne-ifd.433.21, which waits on yoyodyne-ifd.437.14 and holds back the items under it"; !strings.HasPrefix(says, want) {
		t.Fatalf("says = %q, want it to open %q", says, want)
	}
	if listed[1].Kind != backlog.HeldUnread || listed[1].Count != 1 {
		t.Fatalf("groups = %+v, want the item nothing explains left in its own group", listed)
	}
}
