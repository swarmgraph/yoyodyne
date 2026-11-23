package runstate

// Whether a parked item holds back a re-run decided about one of its runs, and
// who parked it.
//
// A parking is somebody deciding the item is not to be started yet, and a
// re-run the development manager decides is the harness choosing work, so the
// parking holds the re-run exactly as it holds the scheduler's pull: the re-run
// waits, spending nothing, and starts by itself once the item is unparked. The
// one parking it does not wait on is the one the run being answered placed
// itself — a run that landed evidence parks its item until somebody decides
// what happens next, and the re-run is that decision. A raise is the exception
// to the exception, and is the raise's own rule (orchestrator's raiseReleased):
// a raise parks its item until its owner amends it, so a re-run of a raise
// waits on that release as well.
//
// It lives here rather than beside either reader because three things ask it in
// the same words: the carry-out that would start the run, the conversation that
// tells the development manager what she recorded, and the pass that decides
// which decisions to attempt.

import (
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// runParkingOpening is how a parking a run placed on its own item opens: the
// raise's parking and the evidence landing's parking both begin with it
// (RaiseParking, and orchestrator's undischargedParking).
const runParkingOpening = "yoyodyne run "

// ParkingRun reports the run a parking says placed it, and whether it names
// one at all.
func ParkingRun(parking string) (string, bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(parking), runParkingOpening)
	if !found {
		return "", false
	}
	runID, _, _ := strings.Cut(rest, " ")
	if !ValidRunID(runID) {
		return "", false
	}
	return runID, true
}

// RerunWaitsOnParking reports an item parked by anything other than the run a
// re-run answers, which is a parking the re-run waits on, and who parked it.
// An item that is not parked, or is parked only by that run, does not hold the
// re-run here.
func RerunWaitsOnParking(item beads.WorkItem, priorRunID string) (parkedBy string, waits bool) {
	if !item.Parking.Parked() {
		return "", false
	}
	if runID, byRun := ParkingRun(item.Parking.Reason()); byRun && runID == strings.TrimSpace(priorRunID) {
		return "", false
	}
	return ParkedBy(item), true
}

// ParkedBy names who parked an item, in words a person reads: the run whose
// parking it is, or the role and conversation the latest parking note on the
// item names. Where neither says, it says that rather than guess.
func ParkedBy(item beads.WorkItem) string {
	if runID, byRun := ParkingRun(item.Parking.Reason()); byRun {
		return "run " + runID
	}
	// A role parks an item from its conversation, and the note it appends says
	// who and where: "Parked by the <role> in conversation <id>, after turn N."
	const opening = "Parked by "
	notes := item.Notes
	if at := strings.LastIndex(notes, opening); at >= 0 {
		rest := notes[at+len(opening):]
		if end := strings.Index(rest, ", after turn"); end > 0 {
			return strings.TrimSpace(rest[:end])
		}
		if end := strings.IndexAny(rest, ".\n"); end > 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	return "somebody the item's notes do not name"
}
