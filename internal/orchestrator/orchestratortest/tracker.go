package orchestratortest

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Tracker is the work tracker with the store taken out. It holds one work item,
// records every act the harness takes on it in the order taken, and can be told
// to refuse any of them.
type Tracker struct {
	Item beads.WorkItem
	// AlsoHolds is the other work this tracker has, by identifier. It is what
	// makes an impediment a landing named one the harness can confirm; a tracker
	// that answered for every identifier could not tell the two cases apart.
	AlsoHolds map[string]beads.WorkItem
	Claimed   bool
	Notes     string
	// NoteRecords is each note as it was recorded, kept beside the accumulated
	// text above so a reader can tell one account from the next one's. A run that
	// pauses and is resumed writes two, and the concatenation alone cannot say
	// where the first ended.
	NoteRecords []string
	Closed      bool
	CloseReason string
	Blocked     bool
	BlockReason string
	Calls       []string
	OnClaim     func() error
	// ShowFailures and TransientShowErr are how many reads are refused before one
	// answers, and what they are refused with. They are the read's half of what
	// CompleteFailures is for the write: a store that was busy rather than one
	// that keeps answering the same way. ShowCalls counts every read, so a test
	// can say which of a run's reads the refusals landed on.
	ShowFailures     int
	TransientShowErr error
	ShowCalls        int
	// StaleBlockClear is what the claim reports about a stale blocked status it
	// cleared, returned beside the item and beside OnClaim's error alike, as the
	// real client returns it. Nil is a claim that met none.
	StaleBlockClear *beads.StaleBlockClear
	CompleteErr     error
	// CompleteFailures and TransientCompleteErr are how many closures are refused
	// before one goes through, and what they are refused with. They are apart
	// from CompleteErr because that one is a tracker that keeps answering the
	// same way, and this is a store that was busy.
	CompleteFailures     int
	TransientCompleteErr error
	// ClaimFailures and TransientClaimErr, and RecordFailures and
	// TransientRecordErr, are the same for the claim and for an outcome note: a
	// write the store was too slow to answer. LandsBeforeFailing makes every one
	// of those refused writes, the closure's included, land before it is refused,
	// which is a `bd` killed at its bound after the store had taken the write.
	ClaimFailures      int
	TransientClaimErr  error
	RecordFailures     int
	TransientRecordErr error
	LandsBeforeFailing bool
	BlockErr           error
	// Released and ReleaseReason are the claim given back to the queue by a run
	// that ended on something nobody has to decide about.
	Released      bool
	ReleaseReason string
	// Reopened and ReopenReason are the item put back in the backlog by a run
	// that integrated its change and claimed the change does not discharge the
	// item. They are apart from the closure above because the whole point of the
	// landing claim is that the two are different acts.
	Reopened     bool
	ReopenReason string
	ReopenErr    error
	// Blockers is each dependency added to the item, in the order they were
	// added. It is what says the leave-open path actually made the item wait for
	// something rather than putting it back bare.
	Blockers   []string
	BlockerErr error
}

// Show answers for the item this tracker holds and for anything else explicitly
// put in it, and refuses everything else the way the real client does. Answering
// for every identifier would make "the tracker has no such work item" untestable,
// which is the case a landing's impediment marker has to be resolved against.
func (f *Tracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	f.ShowCalls++
	if f.ShowFailures > 0 {
		f.ShowFailures--
		return beads.WorkItem{}, f.TransientShowErr
	}
	if id == f.Item.ID {
		return f.Item, nil
	}
	if item, held := f.AlsoHolds[id]; held {
		return item, nil
	}
	return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
}

// Holds puts another open work item in this tracker, which is what makes an
// impediment a landing names one the harness can confirm.
func (f *Tracker) Holds(id string) *Tracker {
	return f.HoldsItem(beads.WorkItem{ID: id, Title: "The impediment", Status: "open"})
}

// HoldsItem is the same for work that has to be more than open — finished, or
// already waiting on something — because what the impediment says about itself is
// what decides whether waiting on it would hold anything back.
func (f *Tracker) HoldsItem(item beads.WorkItem) *Tracker {
	if f.AlsoHolds == nil {
		f.AlsoHolds = make(map[string]beads.WorkItem)
	}
	f.AlsoHolds[item.ID] = item
	return f
}

func (f *Tracker) Claim(context.Context, string) (beads.WorkItem, *beads.StaleBlockClear, error) {
	if f.OnClaim != nil {
		if err := f.OnClaim(); err != nil {
			return beads.WorkItem{}, f.StaleBlockClear, err
		}
	}
	if f.ClaimFailures > 0 {
		f.ClaimFailures--
		if f.LandsBeforeFailing {
			f.claim()
		}
		return beads.WorkItem{}, nil, f.TransientClaimErr
	}
	f.claim()
	return f.Item, f.StaleBlockClear, nil
}

func (f *Tracker) claim() {
	f.Claimed = true
	f.Calls = append(f.Calls, "claim")
	f.Item.Status = "in_progress"
}

func (f *Tracker) RecordOutcome(_ context.Context, _ string, notes string) (beads.WorkItem, error) {
	if f.RecordFailures > 0 {
		f.RecordFailures--
		if f.LandsBeforeFailing {
			f.record(notes)
		}
		return beads.WorkItem{}, f.TransientRecordErr
	}
	f.record(notes)
	return f.Item, nil
}

// record appends a note to the item as well as to the record of notes, so a
// read-back of the item finds what was written the way it finds it in bd.
func (f *Tracker) record(notes string) {
	f.Notes += notes
	f.NoteRecords = append(f.NoteRecords, notes)
	f.Calls = append(f.Calls, "record")
	f.Item.Notes += notes
}

func (f *Tracker) Block(_ context.Context, _ string, reason string) (beads.WorkItem, error) {
	f.Calls = append(f.Calls, "block")
	if f.BlockErr != nil {
		return beads.WorkItem{}, f.BlockErr
	}
	f.Blocked = true
	f.BlockReason = reason
	f.Item.Status = "blocked"
	return f.Item, nil
}

func (f *Tracker) Release(_ context.Context, _ string, reason string) (beads.WorkItem, error) {
	f.Calls = append(f.Calls, "release")
	f.Released = true
	f.ReleaseReason = reason
	f.Claimed = false
	f.Item.Status = "open"
	return f.Item, nil
}

func (f *Tracker) Complete(_ context.Context, _ string, reason string) (beads.WorkItem, error) {
	f.Calls = append(f.Calls, "complete")
	// CompleteFailures is how many times the closure is refused before it goes
	// through, which is what a `bd` the store was too busy to run looks like.
	if f.CompleteFailures > 0 {
		f.CompleteFailures--
		if f.LandsBeforeFailing {
			f.Closed = true
			f.CloseReason = reason
			f.Item.Status = "closed"
		}
		return beads.WorkItem{}, f.TransientCompleteErr
	}
	if f.CompleteErr != nil {
		return beads.WorkItem{}, f.CompleteErr
	}
	f.Closed = true
	f.CloseReason = reason
	f.Item.Status = "closed"
	return f.Item, nil
}

func (f *Tracker) Reopen(_ context.Context, _ string, reason string, parking domain.WorkItemParking) (beads.WorkItem, error) {
	f.Calls = append(f.Calls, "reopen")
	if f.ReopenErr != nil {
		return beads.WorkItem{}, f.ReopenErr
	}
	f.Reopened = true
	f.ReopenReason = reason
	f.Notes += reason
	f.NoteRecords = append(f.NoteRecords, reason)
	f.Item.Status = "open"
	// The parking is applied exactly as the tracker applies it: an empty one is a
	// release rather than an omission, so an item put back unparked is one the
	// queue offers again.
	f.Item.Parking = parking
	return f.Item, nil
}

func (f *Tracker) AddBlocker(_ context.Context, _ string, blockerID string) error {
	f.Calls = append(f.Calls, "blocker")
	if f.BlockerErr != nil {
		return f.BlockerErr
	}
	f.Blockers = append(f.Blockers, blockerID)
	f.Item.Dependencies = append(f.Item.Dependencies, beads.Dependency{ID: blockerID, Type: "blocks"})
	return nil
}

// TrackerRecord is what a tracker has recorded, read in one piece. It is how a
// fixture shared by tests on this fake and tests on another reads the record
// without naming either fake.
type TrackerRecord struct {
	Item        beads.WorkItem
	Claimed     bool
	Notes       string
	NoteRecords []string
	Closed      bool
	CloseReason string
	Blocked     bool
	BlockReason string
	Calls       []string
}

// Record is what this tracker has recorded so far.
func (f *Tracker) Record() TrackerRecord {
	return TrackerRecord{
		Item: f.Item, Claimed: f.Claimed, Notes: f.Notes, NoteRecords: f.NoteRecords,
		Closed: f.Closed, CloseReason: f.CloseReason, Blocked: f.Blocked, BlockReason: f.BlockReason,
		Calls: f.Calls,
	}
}

// SetItemStatus puts the held item in a status nothing the harness did gave it,
// which is how a test expresses the item moving outside the run.
func (f *Tracker) SetItemStatus(status string) {
	f.Item.Status = status
}

// ForgetSettlement takes back a closure or a blocker as though neither had been
// recorded, which is what a process killed before either reached the tracker
// leaves.
func (f *Tracker) ForgetSettlement() {
	f.Blocked, f.BlockReason, f.Closed = false, "", false
}
