package readmodel

// Who holds a work item now, in the words every surface says it in.
//
// The tracker's assignee cannot say this on its own. A harness claim sets it to
// this machine's Git name, which reads as a person having taken the work, and
// until claims recorded theirs nothing told the two apart
// (docs/diagnoses/yoyodyne-ifd-433-35-assignees-left-by-claims.md). So the
// answer is read from three things together: whether a run is working on the
// item, whether the assignee is one a claim set, and the item's status.

import (
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// HolderKind is which of the three a work item's holder is.
type HolderKind string

const (
	// HeldByRun is a run working on the item now.
	HeldByRun HolderKind = "run"
	// HeldByPerson is a person who set the item's assignee by hand.
	HeldByPerson HolderKind = "person"
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

// ItemHolder says who holds an item. running is the run working on it now, as
// the run records say, and runsRead is whether those records were read at all:
// a reader that has not read them — the tracker view a role reads — cannot say
// a claimed item has no run behind it, and says only what the tracker shows.
func ItemHolder(item beads.WorkItem, running string, runsRead bool) Holder {
	assignee := strings.TrimSpace(item.Assignee)
	switch {
	case running != "":
		return Holder{Kind: HeldByRun, RunID: running, Says: "a running run, " + running}
	case !runsRead && item.Status == "in_progress" && (item.ClaimAssignee || assignee == ""):
		return Holder{Kind: HeldByRun, Says: "a run the harness started; the tracker has the item claimed for it"}
	case assignee != "" && !item.ClaimAssignee && item.Status != "closed":
		return Holder{Kind: HeldByPerson, Person: assignee, Says: "a person, " + assignee + ", who set the assignee by hand"}
	}
	says := "nobody"
	switch {
	case item.Status == "closed":
		says = "nobody; the item is closed"
	case item.Status == "in_progress":
		says = "nobody; the tracker has the item claimed, but no run is working on it"
	case item.HeldByClaimAssignee():
		says = "nobody; the assignee " + assignee + " was left by a run's claim that has ended"
	}
	return Holder{Kind: HeldByNobody, Says: says}
}
