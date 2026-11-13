package readmodel

// Who holds a work item now, in the words every surface says it in.
//
// The tracker's assignee cannot say this on its own. A harness claim sets it to
// this machine's Git name, which reads as a person having taken the work, and
// until claims recorded theirs nothing told the two apart
// (docs/diagnoses/yoyodyne-ifd-433-35-assignees-left-by-claims.md). So the
// answer is read from what is known beside it: whether a run is working on the
// item, whether any run ever claimed it, whether the assignee is one a claim
// recorded setting, and the item's status. A name nothing marks is said to be
// a person's only where the run records show no run ever claimed the item,
// the same rule the clean-up of those names decides by
// (orchestrator.AssigneeCleanup).

import (
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// HolderKind is which kind of holder a work item has.
type HolderKind string

const (
	// HeldByRun is a run working on the item now.
	HeldByRun HolderKind = "run"
	// HeldByPerson is a person who set the item's assignee by hand.
	HeldByPerson HolderKind = "person"
	// HeldByAssignee is an assignee nothing says the origin of: the reader did
	// not read the run records, and no claim recorded setting it.
	HeldByAssignee HolderKind = "assignee"
	// HeldByNobody is nothing working on the item and nobody assigned to it.
	HeldByNobody HolderKind = "nobody"
)

// Holder is who holds one work item now.
type Holder struct {
	Kind HolderKind `json:"kind"`
	// RunID is the run working on the item, where Kind is a run and the run is
	// known; Person is the name a person set, where Kind is a person.
	RunID  string `json:"run_id,omitempty"`
	Person string `json:"person,omitempty"`
	// Says is the whole answer in one line, the words every surface prints.
	Says string `json:"says"`
}

// HolderRuns is what the run records say about one item, for ItemHolder.
type HolderRuns struct {
	// Read is whether the run records were read at all. A reader that has not
	// read them — the tracker view a role reads — cannot say who set a name
	// nothing marks, and says only what the tracker shows.
	Read bool
	// Running is the run working on the item now, if any.
	Running string
	// Claimed is whether any run was ever recorded for the item, which is what
	// says a run's claim set a name nothing marks.
	Claimed bool
}

// ItemHolder says who holds an item.
func ItemHolder(item beads.WorkItem, runs HolderRuns) Holder {
	assignee := strings.TrimSpace(item.Assignee)
	switch {
	case runs.Running != "":
		return Holder{Kind: HeldByRun, RunID: runs.Running, Says: "a running run, " + runs.Running}
	case !runs.Read && item.Status == "in_progress" && (item.ClaimAssignee || assignee == ""):
		return Holder{Kind: HeldByRun, Says: "a run the harness started; the tracker has the item claimed for it"}
	case item.Status == "closed":
		return Holder{Kind: HeldByNobody, Says: "nobody; the item is closed"}
	case assignee != "" && !item.ClaimAssignee && !runs.Read:
		return Holder{Kind: HeldByAssignee, Says: "the assignee " + assignee + ", which no run's claim recorded setting"}
	case assignee != "" && !item.ClaimAssignee && !runs.Claimed:
		return Holder{Kind: HeldByPerson, Person: assignee, Says: "a person, " + assignee + ", who set the assignee by hand"}
	}
	says := "nobody"
	switch {
	case item.Status == "in_progress":
		says = "nobody; the tracker has the item claimed, but no run is working on it"
	case item.HeldByClaimAssignee():
		says = "nobody; the assignee " + assignee + " was left by a run's claim that has ended"
	case assignee != "":
		says = "nobody; the assignee " + assignee + " was most likely left by an earlier run's claim, since a run has claimed this item before and nothing records a person setting it"
	}
	return Holder{Kind: HeldByNobody, Says: says}
}
