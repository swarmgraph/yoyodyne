package beads

import (
	"fmt"
	"strings"
)

// OutsideBlocker is an open item outside a named epic that could block one of
// its members. Member is the item inside the epic the walk started from, and
// From the item it was reached from: Member itself, or an item outside the epic
// between the two. Child says it is a child From holds; otherwise From waits on
// it by a dependency.
type OutsideBlocker struct {
	ID, Title string
	Member    string
	From      string
	Child     bool
}

// Link is how the blocker is tied to the item it was reached from, in the words
// a refusal says it with.
func (b OutsideBlocker) Link() string {
	how := b.From + " waits on it"
	if b.Child {
		how = "it is a child of " + b.From
	}
	if b.From != b.Member {
		how += ", which could block " + b.Member
	}
	return how
}

// Describe names the blocker, its title, and how it could block the member.
func (b OutsideBlocker) Describe() string {
	name := b.ID
	if title := strings.TrimSpace(b.Title); title != "" {
		name = fmt.Sprintf("%s (%s)", b.ID, title)
	}
	return fmt.Sprintf("%s could block %s: %s", name, b.Member, b.Link())
}

// OutsideBlockers is every open item that could block an item of starts and is
// not inside, each once, in the order the walk reaches it. From each item it
// follows the dependencies the item waits on and the children it holds, then the
// same links from every item reached, at any depth; it stops at a closed item and
// at one inside. docs/designs/epic-mode-second-part.md states the rule, under "An
// item outside the epic that could block a member".
//
// items is a listing that carries closed work too. A linked item the listing
// does not hold is treated as open, because nothing shows it closed and the
// refusal that follows is the safe side.
func OutsideBlockers(starts []string, inside func(id string) bool, items []WorkItem) []OutsideBlocker {
	byID := make(map[string]WorkItem, len(items))
	children := map[string][]string{}
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		byID[id] = item
		if parent := item.DecomposedFrom(); parent != "" {
			children[parent] = append(children[parent], id)
		}
	}
	type step struct{ id, member string }
	var queue []step
	seen := map[string]bool{}
	for _, start := range starts {
		start = strings.TrimSpace(start)
		if start != "" && !seen[start] {
			seen[start] = true
			queue = append(queue, step{start, start})
		}
	}
	var found []OutsideBlocker
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		reach := func(id string, child bool) {
			id = strings.TrimSpace(id)
			if id == "" || id == at.id || seen[id] {
				return
			}
			seen[id] = true
			linked, listed := byID[id]
			if listed && strings.TrimSpace(linked.Status) == statusClosed {
				return
			}
			if inside(id) {
				return
			}
			found = append(found, OutsideBlocker{ID: id, Title: linked.Title, Member: at.member, From: at.id, Child: child})
			queue = append(queue, step{id, at.member})
		}
		for _, dependency := range byID[at.id].Dependencies {
			if !strings.EqualFold(strings.TrimSpace(dependency.Type), BlocksDependency) {
				continue
			}
			if issue := strings.TrimSpace(dependency.IssueID); issue != "" && issue != at.id {
				continue
			}
			reach(dependency.ID, false)
		}
		for _, child := range children[at.id] {
			reach(child, true)
		}
	}
	return found
}
