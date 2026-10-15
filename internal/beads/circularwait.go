package beads

import (
	"fmt"
	"strings"
)

// CircularWait is a wait an item would come to hold on its own work. The
// tracker holds every item under a waiting parent back with that parent (see
// InheritedBlock), so a parent that waits on something broken out of it is
// waiting on an item that is itself held back until the parent stops waiting,
// and neither is ever offered. Every sibling the held item waits on is held the
// same way, so the whole decomposition stops with nothing on any one item's own
// links saying why.
//
// Two writes make the arrangement: linking an item to wait on work that leads
// back into its own decomposition, and placing an item under a parent whose
// waits lead back into it. Both are found by the same walk, before either is
// written; see FindCircularWait.
type CircularWait struct {
	// Waiter is the item that would wait on its own work.
	Waiter string
	// Chain is what the waiter would come to wait on, from the item the write
	// names to the one that leads back into the waiter, each step saying how it
	// reaches the next.
	Chain []WaitStep
	// Titles is the title of each item named above, by identifier, where the
	// reading had one.
	Titles map[string]string
}

// WaitStep is one item on a circular wait's chain and how the item before it
// reaches it.
type WaitStep struct {
	ID string
	// Via is how the previous step, or the waiter for the first, reaches this
	// one: WaitViaLink where it records a link waiting on this one, WaitViaParent
	// where this one is its parent and holds it back.
	Via string
}

// How one step of a circular wait reaches the next.
const (
	WaitViaLink   = "link"
	WaitViaParent = "parent"
)

// maxCircularWaitItems bounds how many items one search reads. A wait that
// reaches further than this is not one any decomposition here has made, and
// the bound is what keeps a graph the tracker got wrong from being walked for
// ever. A search that reaches it says so rather than reporting no loop.
const maxCircularWaitItems = 256

// FindCircularWait answers whether waiter would wait on its own work once the
// write first names lands: a link waiting on first.ID (Via WaitViaLink), or
// first.ID made its parent (Via WaitViaParent), whose waits the waiter would
// then be held back by. It returns nil where it would not.
//
// The walk follows every wait each item it reaches is under — the unfinished
// work it links to, and its parent, which holds it back with whatever that
// parent waits on — and stops at the first item that is the waiter or was
// broken out of it. lookup answers an item by identifier, and reports false for
// one the reading does not hold; a closed item, or one not found, is nobody's
// wait and is not walked past.
//
// It reads the tracker as it holds the arrangement now and decides nothing about
// readiness: a genuine wait on work outside the waiter's own decomposition is
// never reported, however long its chain.
func FindCircularWait(waiter string, first WaitStep, lookup func(id string) (WorkItem, bool, error)) (*CircularWait, error) {
	waiter = strings.TrimSpace(waiter)
	type visit struct {
		from string
		via  string
	}
	cache := map[string]WorkItem{}
	held := map[string]bool{}
	read := func(id string) (WorkItem, bool, error) {
		if item, cached := cache[id]; cached {
			return item, held[id], nil
		}
		item, found, err := lookup(id)
		if err != nil {
			return WorkItem{}, false, fmt.Errorf("read %s: %w", id, err)
		}
		cache[id], held[id] = item, found && item.Status != statusClosed
		return item, held[id], nil
	}
	// descendsFrom reports whether id was broken out of the waiter, at any depth.
	descendsFrom := func(item WorkItem) (bool, error) {
		seen := map[string]struct{}{strings.TrimSpace(item.ID): {}}
		current := item
		for depth := 0; depth < maxInheritedDepth; depth++ {
			parent := current.DecomposedFrom()
			if parent == "" {
				return false, nil
			}
			if parent == waiter {
				return true, nil
			}
			if _, cycle := seen[parent]; cycle {
				return false, nil
			}
			seen[parent] = struct{}{}
			ancestor, found, err := read(parent)
			if err != nil {
				return false, err
			}
			if !found {
				return false, nil
			}
			current = ancestor
		}
		return false, nil
	}

	first.ID = strings.TrimSpace(first.ID)
	if first.ID == "" {
		return nil, nil
	}
	// The waiter is read for its title alone, so the refusal names it by what it
	// is; a waiter that cannot be read is named by its identifier.
	_, _, _ = read(waiter)
	reached := map[string]visit{first.ID: {via: first.Via}}
	queue := []string{first.ID}
	for walked := 0; len(queue) > 0; walked++ {
		if walked >= maxCircularWaitItems {
			return nil, fmt.Errorf("the waits reachable from %s go past %d items, so whether %s would wait on its own work could not be settled",
				first.ID, maxCircularWaitItems, waiter)
		}
		id := queue[0]
		queue = queue[1:]
		item, found, err := read(id)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		loops := id == waiter
		if !loops {
			if loops, err = descendsFrom(item); err != nil {
				return nil, err
			}
		}
		if loops {
			return circularWaitTo(waiter, id, func(step string) (string, string) {
				return reached[step].from, reached[step].via
			}, cache), nil
		}
		next := func(to, via string) {
			to = strings.TrimSpace(to)
			if to == "" {
				return
			}
			if _, seen := reached[to]; seen {
				return
			}
			reached[to] = visit{from: id, via: via}
			queue = append(queue, to)
		}
		for _, dependency := range item.Dependencies {
			if dependency.Type == BlocksDependency && dependency.Status != statusClosed {
				next(dependency.ID, WaitViaLink)
			}
		}
		next(item.DecomposedFrom(), WaitViaParent)
	}
	return nil, nil
}

// circularWaitTo rebuilds the chain the walk took to the item that leads back
// into the waiter.
func circularWaitTo(waiter, end string, previous func(id string) (string, string), cache map[string]WorkItem) *CircularWait {
	var reversed []WaitStep
	for id := end; id != ""; {
		from, via := previous(id)
		reversed = append(reversed, WaitStep{ID: id, Via: via})
		id = from
	}
	wait := &CircularWait{Waiter: waiter, Titles: map[string]string{}}
	for i := len(reversed) - 1; i >= 0; i-- {
		wait.Chain = append(wait.Chain, reversed[i])
	}
	for id, item := range cache {
		if title := strings.TrimSpace(item.Title); title != "" {
			wait.Titles[id] = title
		}
	}
	return wait
}

// Name names one item of the wait by what it is and then its identifier, or
// by its identifier alone where the reading had no title.
func (c CircularWait) Name(id string) string {
	return InheritedBlock{Titles: c.Titles}.Name(id, "")
}

// Describe says the wait in plain words: what the waiter would wait on, how
// that leads back into its own work, and why that means neither is ever
// offered. It is the one wording every refusal of the arrangement uses.
func (c CircularWait) Describe() string {
	if len(c.Chain) == 0 {
		return ""
	}
	waiter := c.Name(c.Waiter)
	var chain strings.Builder
	chain.WriteString(waiter)
	for i, step := range c.Chain {
		switch {
		case step.Via == WaitViaParent && i == 0:
			fmt.Fprintf(&chain, " would be held back with its new parent %s", c.Name(step.ID))
		case step.Via == WaitViaParent:
			fmt.Fprintf(&chain, ", which is held back with its parent %s", c.Name(step.ID))
		case i == 0:
			fmt.Fprintf(&chain, " would wait on %s", c.Name(step.ID))
		default:
			fmt.Fprintf(&chain, ", which waits on %s", c.Name(step.ID))
		}
	}
	// A chain that ends at the waiter has already named it; one that ends at its
	// work says how that work is the waiter's.
	if end := c.Chain[len(c.Chain)-1].ID; end != c.Waiter {
		fmt.Fprintf(&chain, ", which was broken out of %s", waiter)
	}
	return fmt.Sprintf("%s. The tracker holds work broken out of an item back until that item stops waiting, so %s would never stop waiting and that work would never be offered",
		chain.String(), waiter)
}
