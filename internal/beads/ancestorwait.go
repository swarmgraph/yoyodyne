package beads

import (
	"context"
	"fmt"
	"strings"
)

// AncestorChain answers whether waitsOn is item's parent or an item above it,
// and where it is, the chain of items from item's parent up to waitsOn. It
// returns nil where waitsOn is not above item.
//
// An item cannot close while work under it is still open, so a decision that
// item's stoppage waits until one of these closes waits for ever: the stopped
// item is part of what holds it open. lookup answers an item by identifier and
// reports false for one the reading does not hold; the walk stops there, and at
// the depth the tracker's own holding-back is read to (maxInheritedDepth).
func AncestorChain(item, waitsOn string, lookup func(id string) (WorkItem, bool, error)) ([]string, error) {
	item, waitsOn = strings.TrimSpace(item), strings.TrimSpace(waitsOn)
	if item == "" || waitsOn == "" || item == waitsOn {
		return nil, nil
	}
	current, found, err := lookup(item)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", item, err)
	}
	if !found {
		return nil, nil
	}
	seen := map[string]bool{item: true}
	var chain []string
	for depth := 0; depth < maxInheritedDepth; depth++ {
		parent := current.DecomposedFrom()
		if parent == "" || seen[parent] {
			return nil, nil
		}
		seen[parent] = true
		chain = append(chain, parent)
		if parent == waitsOn {
			return chain, nil
		}
		current, found, err = lookup(parent)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", parent, err)
		}
		if !found {
			return nil, nil
		}
	}
	return nil, nil
}

// DescribeAncestry says where an AncestorChain puts item, in the words a
// refusal or a docket closure says it with: "x is under y, which is under z".
func DescribeAncestry(item string, chain []string) string {
	described := strings.TrimSpace(item) + " is under " + chain[0]
	for _, above := range chain[1:] {
		described += ", which is under " + above
	}
	return described
}

// UnfinishedChildren reports the items broken out of parent that are not
// closed, as `bd list --parent` reads them: one listing, asked again after the
// waits any listing takes, and recorded the same way.
//
// An item with work under it still open closes only once all of that work is
// done, so a decision to wait on it waits for the whole area rather than for
// the one piece of work it meant (chat's refuseFinishedWaitsOn). bd leaves
// closed work out of a listing given no status; anything closed it returns all
// the same is left out here too.
func (c Client) UnfinishedChildren(ctx context.Context, parent string) ([]WorkItem, error) {
	if err := validateIssueID(parent); err != nil {
		return nil, err
	}
	items, err := c.listPatiently(ctx, []string{"--parent=" + strings.TrimSpace(parent)})
	c.recordListing(ctx, err)
	if err != nil {
		return nil, err
	}
	var unfinished []WorkItem
	for _, item := range items {
		if strings.TrimSpace(item.Status) == statusClosed || strings.TrimSpace(item.ID) == strings.TrimSpace(parent) {
			continue
		}
		unfinished = append(unfinished, item)
	}
	return unfinished, nil
}

// DescribeOpenChildren names an item's unfinished children in the words a
// refusal or a docket closure says them with: how many, and the first few by
// identifier.
func DescribeOpenChildren(children []WorkItem) string {
	const named = 3
	ids := make([]string, 0, named)
	for _, child := range children {
		if len(ids) == named {
			break
		}
		ids = append(ids, strings.TrimSpace(child.ID))
	}
	described := fmt.Sprintf("%d open child item(s), among them %s", len(children), strings.Join(ids, ", "))
	if len(children) <= named {
		described = fmt.Sprintf("%d open child item(s): %s", len(children), strings.Join(ids, ", "))
	}
	return described
}
