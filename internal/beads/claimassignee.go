package beads

// The assignee a run's claim sets, and giving it back.
//
// `bd update --claim` sets an item's assignee to the tracker's actor — this
// machine's Git name — as well as moving it to in progress, and none of the
// writes that give an item back touches the assignee. Left alone, every run
// leaves the name behind on its item whether it landed, stopped, failed, or was
// cancelled seconds after it started, and the name reads to everybody as a
// person having taken the work (docs/diagnoses/yoyodyne-ifd-433-35-assignees-left-by-claims.md).
//
// So a claim marks, in the item's metadata, that the assignee is the claim's,
// and every write that gives the item back clears an assignee so marked. An
// assignee nothing marked is a person's, set by hand, and is never cleared here.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// claimAssigneeKey is where the tracker records that an item's assignee was set
// by a harness claim rather than by a person. It is metadata for the reason the
// parking is: bd's assignee field cannot say who set it, and a marker in the
// notes is one the next writer of the notes replaces.
//
// It is written only by a claim that found the item with no assignee, or with
// one an earlier claim had marked, so an assignee a person put on the item
// before a run claimed it is never marked and never cleared.
const claimAssigneeKey = "yoyodyne_claim_assignee"

// claimAssigneeIn reads the marker. bd stores the "true" a claim writes as a
// JSON boolean, and a string is read too in case a later bd keeps it as
// written. Anything else reads as unmarked, so a value somebody else put under
// the key is never taken as leave to clear an assignee.
func claimAssigneeIn(metadata map[string]json.RawMessage) bool {
	var marked bool
	if raw, present := metadata[claimAssigneeKey]; present && json.Unmarshal(raw, &marked) == nil {
		return marked
	}
	return metadataString(metadata, claimAssigneeKey) == "true"
}

// HeldByClaimAssignee reports whether the item's assignee is one a harness
// claim set and nothing has given back yet. It is false for an item with no
// assignee, and for one whose assignee a person set.
func (w WorkItem) HeldByClaimAssignee() bool {
	return w.ClaimAssignee && strings.TrimSpace(w.Assignee) != ""
}

// claimMarks decides whether a claim about to be made on an item marks the
// assignee it sets as its own. It does where the item has no assignee, which is
// the claim setting one, and where an earlier claim marked the one there, which
// is the claim setting the same one again. An item a person assigned is claimed
// unmarked: bd only lets the claim through where that person is the actor, and
// the name they set stays when the run gives the item back.
func claimMarks(before WorkItem) bool {
	return strings.TrimSpace(before.Assignee) == "" || before.ClaimAssignee
}

// claimMarkArgs is the flag a marking claim adds to its write, in the same
// invocation as the claim so the assignee and the marker land together.
func claimMarkArgs(mark bool) []string {
	if !mark {
		return nil
	}
	return []string{"--set-metadata=" + claimAssigneeKey + "=true"}
}

// ClearLeftoverAssignee clears an item's assignee, and any claim marker beside
// it, with a note saying why. It is the clean-up's write, for an assignee a
// claim left behind: either one the claim marked, or one set before claims
// marked theirs, where the caller has decided from the run records that a claim
// put it there, which nothing on the item itself can say.
//
// Status is left as it is. Clearing the assignee gives nothing back to the
// queue on its own; it only stops the item naming somebody nobody chose.
func (c Client) ClearLeftoverAssignee(ctx context.Context, id, note string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(note) == "" {
		return WorkItem{}, errors.New("a note saying why the assignee is cleared is required")
	}
	return c.clearAssignee(ctx, id, note)
}

func (c Client) clearAssignee(ctx context.Context, id, note string) (WorkItem, error) {
	args := []string{"update", id, "--assignee=", "--unset-metadata=" + claimAssigneeKey}
	if strings.TrimSpace(note) != "" {
		args = append(args, "--append-notes="+note)
	}
	args = append(args, "--json")
	item, err := c.writeItem(ctx, id, func(item WorkItem) bool {
		return strings.TrimSpace(item.Assignee) == "" && !item.ClaimAssignee && endsWithNote(item, note)
	}, args...)
	if err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(item.Assignee) != "" {
		return WorkItem{}, errors.New("work item " + id + " still names " + item.Assignee + " as assignee after it was cleared")
	}
	if strings.TrimSpace(note) == "" {
		return item, nil
	}
	return c.confirmWritten(ctx, item, appendedNote(note))
}

// givenBack clears the claim's assignee from an item a give-back write has just
// answered with, where that claim marked it, and answers with the item as it
// then stands.
//
// It reads the answer to the give-back rather than the item beforehand, so a
// give-back costs nothing more where there is nothing to clear, which is every
// item claimed by a person and every item claimed before claims were marked.
//
// It does not fail the give-back. The status and the note have landed by the
// time this runs, and they are what frees the item; an assignee this could not
// clear is still marked as the claim's, so every surface says it is left over
// rather than a person's, and the clean-up clears it later.
//
// The clear is a second write rather than a flag on the first because bd's
// close cannot carry an assignee, and because the marker can only be read off
// the item: the first write is what answers with it. Between the two, an item
// given back to the queue could be claimed again; a claim landing there is
// cleared with the rest, which leaves a running item naming nobody rather than
// a finished one naming somebody.
func (c Client) givenBack(ctx context.Context, item WorkItem) WorkItem {
	if !item.HeldByClaimAssignee() {
		return item
	}
	cleared, err := c.clearAssignee(ctx, item.ID, "")
	if err != nil {
		return item
	}
	return cleared
}
