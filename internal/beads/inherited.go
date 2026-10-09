package beads

import (
	"fmt"
	"strings"
	"unicode/utf8"
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
	// Titles is the title of each item named above, by identifier, where the
	// reading had one, so a person is told what each item is and not only its
	// identifier.
	Titles map[string]string `json:"titles,omitempty"`
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
	titles := map[string]string{}
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
		if title := strings.TrimSpace(ancestor.Title); title != "" {
			titles[parent] = title
		}
		var waits []Dependency
		blockTitles := map[string]string{}
		for _, id := range waiting(ancestor) {
			if _, recorded := own[id]; recorded {
				continue
			}
			dependency := dependencyOn(ancestor, id)
			waits = append(waits, dependency)
			if title := awaitedTitle(dependency, lookup); title != "" {
				blockTitles[id] = title
			}
		}
		if len(waits) > 0 {
			for _, id := range through {
				if title, known := titles[id]; known {
					blockTitles[id] = title
				}
			}
			blocks = append(blocks, InheritedBlock{Through: append([]string(nil), through...), WaitsOn: waits, Titles: blockTitles})
		}
		current = ancestor
	}
	return blocks, nil
}

// awaitedTitle is the title of the work an ancestor waits on: the one its link
// carried, or else the one a lookup of that work gives. A lookup that fails
// leaves the work named by its identifier alone, which is all that is lost.
func awaitedTitle(dependency Dependency, lookup func(id string) (WorkItem, bool, error)) string {
	if title := strings.TrimSpace(dependency.Title); title != "" {
		return title
	}
	awaited, _, err := lookup(dependency.ID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(awaited.Title)
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
	fmt.Fprintf(&chain, "its parent %s", b.Name(b.Through[0], ""))
	for i := 1; i < len(b.Through); i++ {
		fmt.Fprintf(&chain, ", whose parent is %s", b.Name(b.Through[i], ""))
	}
	waits := make([]string, 0, len(b.WaitsOn))
	for _, dependency := range b.WaitsOn {
		waits = append(waits, b.Name(dependency.ID, dependency.Status))
	}
	return fmt.Sprintf("blocked through %s, which waits on %s", chain.String(), strings.Join(waits, ", "))
}

// Waiting is the ancestor that waits, the last of the chain.
func (b InheritedBlock) Waiting() string {
	if len(b.Through) == 0 {
		return ""
	}
	return b.Through[len(b.Through)-1]
}

// Name names one item of the block by what it is and then its identifier —
// 'Automatic document publication' (yoyodyne-ifd.433.21) — with its status
// beside the identifier where one is given. An item whose title the reading did
// not have is named by its identifier alone.
func (b InheritedBlock) Name(id, status string) string {
	status = strings.TrimSpace(status)
	title := b.Titles[id]
	switch {
	case title != "" && status != "":
		return fmt.Sprintf("'%s' (%s, %s)", oneLine(title), id, status)
	case title != "":
		return fmt.Sprintf("'%s' (%s)", oneLine(title), id)
	case status != "":
		return fmt.Sprintf("%s (%s)", id, status)
	default:
		return id
	}
}

// maxInheritedTitleBytes bounds one title in the sentence, so a long title
// cannot become the read.
const maxInheritedTitleBytes = 120

// oneLine folds a title onto one bounded line.
func oneLine(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if len(title) <= maxInheritedTitleBytes {
		return title
	}
	cut := maxInheritedTitleBytes
	for cut > 0 && !utf8.RuneStart(title[cut]) {
		cut--
	}
	return title[:cut] + "…"
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
