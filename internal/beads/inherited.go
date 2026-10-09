package beads

import (
	"fmt"
	"strings"
)

// InheritedBlock is a wait an item is under that it does not record itself: an
// ancestor it was broken out of carries a blocking dependency on unfinished
// work, and the tracker holds every item under a waiting ancestor back with it.
// Nothing on the item's own links says so, which is how a child of a blocked
// parent came to be read as ready by three roles while the tracker offered it
// to nobody.
//
// It reports a state the tracker already holds; it decides nothing about
// readiness, which stays the tracker's answer.
type InheritedBlock struct {
	// Through is the chain of ancestors from the item's parent up to the one
	// that waits, nearest first, so the last of them is the one with the link.
	Through []string `json:"through"`
	// WaitsOn is the unfinished work that ancestor waits on, with the status the
	// reading carried where it carried one.
	WaitsOn []Dependency `json:"waits_on"`
}

// maxInheritedDepth bounds how far up the parent chain a reading walks. A
// decomposition deeper than this is not one anybody has made, and the bound is
// what keeps a chain the tracker got wrong from being walked for ever.
const maxInheritedDepth = 16

// InheritedBlocks walks an item's ancestors and names each one that waits on
// unfinished work, as lookup and waiting read it. lookup answers an ancestor by
// identifier, and reports false for one the reading does not hold — a parent
// that has left the backlog is not a wait; waiting names what one ancestor
// waits on, which is the caller's judgement because a listing and a show carry
// different evidence about a dependency's status (see WorkItem.WaitingOn).
//
// Work the item already waits on itself is left out: its own links say so, and
// what this adds is only what they do not.
func InheritedBlocks(item WorkItem, lookup func(id string) (WorkItem, bool, error), waiting func(WorkItem) []string) ([]InheritedBlock, error) {
	own := map[string]struct{}{}
	for _, id := range waiting(item) {
		own[id] = struct{}{}
	}
	seen := map[string]struct{}{strings.TrimSpace(item.ID): {}}
	var blocks []InheritedBlock
	var through []string
	current := item
	for depth := 0; depth < maxInheritedDepth; depth++ {
		parent := current.DecomposedFrom()
		if parent == "" {
			break
		}
		if _, cycle := seen[parent]; cycle {
			break
		}
		seen[parent] = struct{}{}
		ancestor, found, err := lookup(parent)
		if err != nil {
			return blocks, fmt.Errorf("read %s, which %s descends from: %w", parent, item.ID, err)
		}
		if !found {
			break
		}
		through = append(through, parent)
		var waits []Dependency
		for _, id := range waiting(ancestor) {
			if _, recorded := own[id]; recorded {
				continue
			}
			waits = append(waits, dependencyOn(ancestor, id))
		}
		if len(waits) > 0 {
			blocks = append(blocks, InheritedBlock{Through: append([]string(nil), through...), WaitsOn: waits})
		}
		current = ancestor
	}
	return blocks, nil
}

// dependencyOn is the blocking link an ancestor records to id.
func dependencyOn(ancestor WorkItem, id string) Dependency {
	for _, dependency := range ancestor.Dependencies {
		if dependency.ID == id && dependency.Type == BlocksDependency {
			return dependency
		}
	}
	return Dependency{ID: id, Type: BlocksDependency}
}

// Describe says the inherited block in plain words: which ancestor waits, the
// chain of parents that reaches it, and what it waits on. It is the one wording
// every surface uses, so a read of the item and a listing of the queue say the
// same thing about it.
func (b InheritedBlock) Describe() string {
	if len(b.Through) == 0 {
		return ""
	}
	var chain strings.Builder
	fmt.Fprintf(&chain, "its parent %s", b.Through[0])
	for i := 1; i < len(b.Through); i++ {
		fmt.Fprintf(&chain, ", whose parent is %s", b.Through[i])
	}
	waits := make([]string, 0, len(b.WaitsOn))
	for _, dependency := range b.WaitsOn {
		if status := strings.TrimSpace(dependency.Status); status != "" {
			waits = append(waits, fmt.Sprintf("%s (%s)", dependency.ID, status))
			continue
		}
		waits = append(waits, dependency.ID)
	}
	return fmt.Sprintf("blocked through %s, which waits on %s", chain.String(), strings.Join(waits, ", "))
}

// DescribeInheritedBlocks is every inherited block on one line, followed by
// what that means for the item, and empty where there is none.
func DescribeInheritedBlocks(blocks []InheritedBlock) string {
	described := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := block.Describe(); text != "" {
			described = append(described, text)
		}
	}
	if len(described) == 0 {
		return ""
	}
	return strings.Join(described, "; and ") +
		". The item records no such link itself; the tracker holds it back with its parent, and offers it once that work is finished"
}
