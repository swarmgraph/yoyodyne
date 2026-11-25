package maintain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Rebuilding the live tracker store from its export.
//
// The design on keeping the store small (docs/designs/tracker-store-size.md)
// brings the store back under its limit by building a fresh one from the old
// one's export. Items, notes, dependencies, labels and comments are carried
// across and compared; what is dropped is the record of past versions, which no
// bd command gives back the disk space of. The build itself is
// internal/beads, storerebuild.go; this is when it runs and what is said about
// it.
//
// A rebuild is due when the live store is past
// services.maintenance.tracker_limits.store_megabytes on disk, or when the
// latest measurement of the store on a copy, taken since the store was last
// rebuilt, found either listing past rebuildListingLimit (rebuildDue), as the
// design's limit says. Neither asks the live store anything: the size is read
// from the disk and the listings' times from the measurement's record, because
// a store too large to answer in time is exactly the one that is due.
//
// The harness's own listings on the live store, which the limits step reports,
// do not make a rebuild due. Those are slow mostly from callers waiting their
// turn at the store, which a rebuild does not cure, and every rebuild holds
// every caller for up to rebuildBound, so the slow listings it causes would
// make the next pass rebuild again, without end.
//
// Each time one is due, it is done first on a copy of the live store, which
// holds the store lock only while the store is copied. Only where that copy's
// two exports held every record the same, and building took no more than
// rebuildBound, is the live store rebuilt. The live rebuild takes the store
// lock exclusive before anything else, so every other tracker call the harness
// makes waits behind it for that long rather than stopping it; only a process
// outside the harness still having the store open once the lock is held stops
// it. Nothing waits for runs in flight and nothing holds new runs: the bound is
// what keeps the wait inside the 120 seconds an ordinary tracker read allows.
//
// A rehearsal that failed, found a record different, or took too long is
// recorded and warns the development manager once, and is not tried again for
// rebuildRetry, so a store that cannot be rebuilt is not copied on every pass.
// A rebuilt store still past its limit warns once with its figures, and again
// only when they change.
//
// A rebuild that was due and did not go ahead for a reason outside it — a
// process outside the harness having the store open, the lock not had in time,
// the store changing while it was copied, or a directory it needed not made —
// is recorded with that reason and tried again at the next pass (holdBack).
// After heldBackWarnAfter such passes in a row the development manager is
// warned once, naming what stopped them, and not again until that changes.

const (
	// rebuildListingLimit is the design's limit on listing open items and listing
	// ready items.
	rebuildListingLimit = 10 * time.Second
	// keptFor is how long the store a rebuild replaced is kept.
	keptFor = 14 * 24 * time.Hour
	// rebuildBound is the longest a rebuild may take on the copy, and so the
	// longest it holds the live store.
	rebuildBound = 60 * time.Second
	// rebuildLiveBound ends a live rebuild that takes far longer than its
	// rehearsal did, before it has replaced anything.
	rebuildLiveBound = 3 * rebuildBound
	// rebuildRetry is how long after a rehearsal that did not pass the next is
	// tried.
	rebuildRetry = 24 * time.Hour
	// heldBackWarnAfter is how many passes in a row a due rebuild may not go
	// ahead before the development manager is warned.
	heldBackWarnAfter = 3
)

// What held a due rebuild back, as a record keeps it, so a warning about
// several passes in a row is repeated only when this changes.
const (
	heldByOutsideProcess = "a process outside the harness had the store open"
	heldByUnknownHolders = "who had the store open could not be told"
	heldByLockWait       = "the store lock was not had in time"
	heldByStoreWritten   = "the store was written to while it was copied"
	heldByFailure        = "a step before the rebuild failed"
)

// StoreRebuilder rebuilds the live tracker store. It is satisfied by
// beads.Client.
type StoreRebuilder interface {
	StoreSize() (string, int64, error)
	RehearseRebuild(ctx context.Context, work *repowrite.PinnedRoot) (beads.StoreRebuild, error)
	RebuildStore(ctx context.Context, work, kept *repowrite.PinnedRoot, name string, recorded func(beads.StoreRebuild) error) (beads.StoreRebuild, error)
}

// Rebuilds is the record of each rebuild decision, and where the replaced
// stores are kept. It is satisfied by *runstate.TrackerStoreRebuildStore.
type Rebuilds interface {
	Read() (runstate.TrackerStoreRebuilds, error)
	Record(runstate.TrackerStoreRebuild) error
	NewName(now time.Time) string
	KeptDirectory() (*repowrite.PinnedRoot, error)
	WorkDirectory() (*repowrite.PinnedRoot, error)
	RemoveKeptMadeBefore(cutoff time.Time) ([]string, error)
	Path() string
}

// Reports is where the pass files a warning. It is satisfied by
// *runstate.ReportStore.
type Reports interface {
	Append(report.Report) error
}

// rebuildTracker decides a rebuild of the live store, runs it where due, and
// removes the stores older than keptFor. It runs off the supervisor's
// goroutine, after the measurement, and touches nothing of the supervisor's.
func (p *Pass) rebuildTracker(ctx context.Context) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepTrackerStoreRebuild}
	if p.Rebuilder == nil || p.Rebuilds == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no rebuild of the tracker's store is wired, so it is not rebuilt"
		return step
	}
	now := p.now()
	removed, removeErr := p.Rebuilds.RemoveKeptMadeBefore(now.Add(-keptFor))
	step = p.decideRebuild(ctx, step, now)
	if len(removed) > 0 {
		step.Detail += fmt.Sprintf("; removed the stores kept more than %d days: %s", int(keptFor/(24*time.Hour)), strings.Join(removed, ", "))
	}
	if removeErr != nil {
		step.Outcome = runstate.StepFailed
		step.Detail += fmt.Sprintf("; a store kept more than %d days could not be removed, and the next pass tries again: %v", int(keptFor/(24*time.Hour)), removeErr)
	}
	return step
}

func (p *Pass) decideRebuild(ctx context.Context, step runstate.SweepStep, now time.Time) runstate.SweepStep {
	decided, err := p.Rebuilds.Read()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the record of earlier rebuilds of the tracker's store could not be read, so whether one was tried lately cannot be told: %v", err)
		return step
	}
	due, size, unread := p.rebuildDue(decided)
	if due == "" {
		step.Outcome, step.Detail = runstate.StepSkipped, "the tracker's store is within its limits, so it is not rebuilt"+unread
		if size > 0 {
			step.Detail = fmt.Sprintf("the tracker's store is %s on disk, within its limit, and no measurement on a copy since it was last rebuilt found its listings past their limit, so it is not rebuilt%s", runstate.SayBytes(size), unread)
		}
		return step
	}
	if last, found := decided.Latest(); found && last.Refused() && now.Sub(last.DecidedAt) < rebuildRetry {
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("the store is due a rebuild because %s, and the last attempt, at %s, did not go ahead: %s; the next is tried after %s", due, local(last.DecidedAt), last.Says(), local(last.DecidedAt.Add(rebuildRetry)))
		return step
	}
	record := runstate.TrackerStoreRebuild{Name: p.Rebuilds.NewName(now), DecidedAt: now, Reason: due, Bound: rebuildBound}
	work, err := p.Rebuilds.WorkDirectory()
	if err != nil {
		return p.holdBack(step, record, decided, heldByFailure, fmt.Sprintf("the directory to build it in could not be made: %v", err), runstate.StepFailed)
	}
	defer work.Close()
	rehearsal, err := p.Rebuilder.RehearseRebuild(ctx, work)
	switch {
	case rehearsal.Waited != "":
		return p.holdBack(step, record, decided, heldByLockWait, "the store lock could not be taken to copy the store: "+rehearsal.Waited, runstate.StepSkipped)
	case rehearsal.Moved != "":
		return p.holdBack(step, record, decided, heldByStoreWritten, rehearsal.Moved+", which only a process outside the harness can do while the store lock is held", runstate.StepSkipped)
	case rehearsal.Failed == "" && err != nil:
		return p.holdBack(step, record, decided, heldByFailure, fmt.Sprintf("a copy of it could not be made or cleaned up: %v", err), runstate.StepFailed)
	}
	record.Rehearsal = &rehearsal
	if rehearsal.Passed() && rehearsal.Took > rebuildBound {
		record.TooLong = rehearsal.Took
		return p.refuseRebuild(step, record, now, fmt.Sprintf("on a copy the rebuild took %s, longer than the %s a rebuild may hold the store. A rebuild that long is a question for the architect and is not run", roundTook(rehearsal.Took), roundTook(rebuildBound)))
	}
	if !rehearsal.Passed() {
		return p.refuseRebuild(step, record, now, p.describeRefusal(rehearsal, "on a copy"))
	}
	return p.rebuildLive(ctx, step, record, decided, work, now)
}

// holdBack records a due rebuild that did not go ahead, saying why, and warns
// the development manager once it is the heldBackWarnAfter-th such pass in a
// row, unless a warning in this run of passes already named the same kind of
// reason. earlier is every decision recorded before this one. A warning that
// cannot be filed leaves the record unwarned, so the next pass held back for
// the same reason files it.
func (p *Pass) holdBack(step runstate.SweepStep, record runstate.TrackerStoreRebuild, earlier runstate.TrackerStoreRebuilds, kind, why string, outcome runstate.SweepStepOutcome) runstate.SweepStep {
	record.HeldBack, record.HeldBackBy, record.Ended = why, kind, true
	inARow := append(earlier.HeldBackInARow(), record)
	step.Outcome = outcome
	step.Detail = fmt.Sprintf("the tracker's store is due a rebuild from its export because %s, and it did not go ahead, because %s; %d pass(es) in a row have not rebuilt it, and the next pass tries again", record.Reason, why, len(inARow))
	if len(inARow) >= heldBackWarnAfter {
		step = p.warnHeldBack(step, &record, inARow)
	}
	if err := p.Rebuilds.Record(record); err != nil {
		step.Outcome = runstate.StepFailed
		step.Detail += fmt.Sprintf("; that it did not go ahead could not be recorded: %v", err)
		return step
	}
	step.Detail += "; recorded in " + p.Rebuilds.Path()
	return step
}

// warnHeldBack files the warning about passes in a row that did not rebuild
// the store, unless the latest warning among them was about the same kind of
// reason as the last.
func (p *Pass) warnHeldBack(step runstate.SweepStep, record *runstate.TrackerStoreRebuild, inARow runstate.TrackerStoreRebuilds) runstate.SweepStep {
	for index := len(inARow) - 2; index >= 0; index-- {
		if inARow[index].Reported == "" {
			continue
		}
		if inARow[index].HeldBackBy == record.HeldBackBy {
			step.Detail += "; the development manager was warned in " + inARow[index].Reported
			return step
		}
		break
	}
	if p.Reports == nil {
		step.Outcome = runstate.StepFailed
		step.Detail += "; no report pile is wired, so the development manager could not be warned"
		return step
	}
	message := fmt.Sprintf("Maintenance has not rebuilt the tracker store from its export for %d passes in a row (the %s step), though it is due because %s. "+
		"The latest pass did not go ahead because %s. The live store was not changed, and every pass tries again. "+
		"Each pass is recorded in %s, and this warning is not filed again until what stops the rebuild changes.",
		len(inARow), StepTrackerStoreRebuild, record.Reason, record.HeldBack, p.Rebuilds.Path())
	filed, err := p.warn(*record, message, record.DecidedAt)
	if err != nil {
		step.Outcome = runstate.StepFailed
		step.Detail += fmt.Sprintf("; the warning to the development manager could not be filed, and the next pass tries again: %v", err)
		return step
	}
	record.Reported = filed
	step.Detail += "; the development manager was warned in " + filed
	return step
}

// describeRefusal says what stopped a rebuild, for the warning.
func (p *Pass) describeRefusal(rebuild beads.StoreRebuild, where string) string {
	if rebuild.Failed != "" {
		return fmt.Sprintf("%s the step %q failed: %s", where, rebuild.Step, rebuild.Failed)
	}
	return fmt.Sprintf("%s the export of the new store differed from the old store's in %d record(s) at the step %q, the first being %s", where, rebuild.Differing, rebuild.Step, rebuild.FirstDifference())
}

// rebuildDue says why the live store is due a rebuild, empty where it is not:
// its size on disk is past services.maintenance.tracker_limits.store_megabytes,
// or the latest measurement on a copy, taken after the last rebuild that
// replaced the store (decided), found a listing past rebuildListingLimit. size
// is the size read, zero where it was not, and unread says, as a clause to add
// to the step, a measure that could not be read. Both measures ask the store
// nothing, so they answer at any size.
func (p *Pass) rebuildDue(decided runstate.TrackerStoreRebuilds) (due string, size int64, unread string) {
	limits := p.trackerLimits()
	var reasons []string
	if p.Tracker != nil {
		if _, read, err := p.Tracker.StoreSize(); err != nil {
			unread += fmt.Sprintf("; the live store's size on disk could not be read, so whether it is past its limit is not known: %v", err)
		} else {
			size = read
			if bound := int64(limits.StoreMegabytes) << 20; read > bound {
				reasons = append(reasons, fmt.Sprintf("the live store is %s on disk, past its limit of %d MB (services.maintenance.tracker_limits.store_megabytes)", runstate.SayBytes(read), limits.StoreMegabytes))
			}
		}
	}
	if p.Measurements != nil {
		if measured, err := p.Measurements.Read(); err != nil {
			unread += fmt.Sprintf("; the record of the store's measurements could not be read, so whether the latest found its listings slow is not known: %v", err)
		} else if slow := slowMeasuredListings(measured, decided); slow != "" {
			reasons = append(reasons, slow)
		}
	}
	return strings.Join(reasons, ", and "), size, unread
}

// slowMeasuredListings says that the latest measurement of the store on a copy
// found a listing past rebuildListingLimit, and empty where it did not, timed
// none, or was taken before the last rebuild that replaced the store, whose
// store it no longer describes.
func slowMeasuredListings(measured runstate.TrackerStoreMeasurements, decided runstate.TrackerStoreRebuilds) string {
	latest, found := measured.Latest()
	if !found || latest.Listings == nil || latest.Listings.Within {
		return ""
	}
	if last, rebuilt := decided.LastReplaced(); rebuilt && !latest.StartedAt().After(last.DecidedAt) {
		return ""
	}
	return fmt.Sprintf("the measurement of the store on a copy at %s found listing open or ready items took longer than its limit of %s", local(latest.StartedAt()), roundTook(rebuildListingLimit))
}

// refuseRebuild records a rebuild that was not made on its rehearsal, and warns
// the development manager once. A warning that cannot be filed is not recorded,
// so the next pass tries the rehearsal again and files it.
func (p *Pass) refuseRebuild(step runstate.SweepStep, record runstate.TrackerStoreRebuild, now time.Time, why string) runstate.SweepStep {
	said := record.Says()
	if p.Reports == nil {
		step.Outcome, step.Detail = runstate.StepFailed, said+"; no report pile is wired, so the development manager could not be warned, and the next pass tries again"
		return step
	}
	message := fmt.Sprintf("Maintenance did not rebuild the tracker store from its export (the %s step), because %s. The live store was not changed. "+
		"The store is rebuilt only where the new store's export holds every item, note, dependency, label and comment the old one did and the rebuild takes no more than %s. It is tried again after %s; the decision is recorded in %s.",
		StepTrackerStoreRebuild, why, roundTook(rebuildBound), local(now.Add(rebuildRetry)), p.Rebuilds.Path())
	filed, err := p.warn(record, message, now)
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("%s; the warning to the development manager could not be filed, and the next pass tries again: %v", said, err)
		return step
	}
	record.Reported, record.Ended = filed, true
	step.Outcome, step.Detail = runstate.StepSkipped, record.Says()
	if err := p.Rebuilds.Record(record); err != nil {
		step.Outcome = runstate.StepFailed
		step.Detail += fmt.Sprintf("; the decision could not be recorded, so the next pass may warn again: %v", err)
	}
	return step
}

// rebuildLive rebuilds the live store, recording where the old one is kept
// before it is moved there and what the rebuild came to once it has ended.
// earlier is every decision recorded before this one.
func (p *Pass) rebuildLive(ctx context.Context, step runstate.SweepStep, record runstate.TrackerStoreRebuild, earlier runstate.TrackerStoreRebuilds, work *repowrite.PinnedRoot, now time.Time) runstate.SweepStep {
	kept, err := p.Rebuilds.KeptDirectory()
	if err != nil {
		return p.holdBack(step, record, earlier, heldByFailure, fmt.Sprintf("the directory its old store is kept in could not be made: %v", err), runstate.StepFailed)
	}
	defer kept.Close()
	live, cancel := context.WithTimeout(ctx, rebuildLiveBound)
	defer cancel()
	rebuilt, err := p.Rebuilder.RebuildStore(live, work, kept, record.Name, func(placed beads.StoreRebuild) error {
		record.Rebuild = &placed
		return p.Rebuilds.Record(record)
	})
	switch {
	case rebuilt.Waited != "":
		return p.holdBack(step, record, earlier, heldByLockWait, "the store lock could not be taken for the rebuild: "+rebuilt.Waited, runstate.StepSkipped)
	case len(rebuilt.Busy) > 0:
		return p.holdBack(step, record, earlier, heldByOutsideProcess, "with the store lock held, a process outside the harness still had the store open: "+beads.SayHolders(rebuilt.Busy), runstate.StepSkipped)
	case rebuilt.Unknown != "":
		return p.holdBack(step, record, earlier, heldByUnknownHolders, "with the store lock held, whether a process outside the harness still had the store open could not be told: "+rebuilt.Unknown, runstate.StepSkipped)
	case rebuilt.ServerKept != "":
		return p.holdBack(step, record, earlier, heldByFailure, "the tracker's server could not be stopped for the rebuild: "+rebuilt.ServerKept, runstate.StepFailed)
	case err != nil && rebuilt.Step == "" && rebuilt.Failed == "" && !rebuilt.Swapped:
		return p.holdBack(step, record, earlier, heldByFailure, fmt.Sprintf("the rebuild could not start: %v", err), runstate.StepFailed)
	}
	record.Rebuild, record.Ended = &rebuilt, true
	if !rebuilt.Swapped {
		return p.refuseLive(step, record, now, p.describeRefusal(rebuilt, "on the live store"))
	}
	step.Outcome = runstate.StepRan
	recorded := "; recorded in " + p.Rebuilds.Path()
	if rebuilt.ServerDown != "" {
		recorded += "; the tracker's server, stopped for the rebuild, could not be started again, and the supervisor tries again: " + rebuilt.ServerDown
	}
	if size, over := p.stillOverAfter(rebuilt); over {
		record.StillOver = size
	}
	if err := p.Rebuilds.Record(record); err != nil {
		recorded = fmt.Sprintf("; its end could not be recorded, so the record says only that the old store's place was recorded: %v", err)
		step.Outcome = runstate.StepFailed
	}
	step.Detail = fmt.Sprintf("rebuilt the tracker's store from its export: it went from %s to %s on disk, every other tracker call waited behind the rebuild for %s, and the old store is kept until %s at %s%s",
		runstate.SayBytes(rebuilt.SizeBefore), runstate.SayBytes(rebuilt.SizeAfter), roundTook(rebuilt.HeldFor), local(rebuilt.KeptAt.Add(keptFor)), rebuilt.Kept, recorded)
	if record.StillOver != "" && step.Outcome == runstate.StepRan {
		return p.warnStillOver(step, record, decidedBefore(p, record), now)
	}
	return step
}

// refuseLive records a live rebuild whose new store was not put in place, and
// warns once. The live store is as it was, or, where a move failed, the old
// store was put back.
func (p *Pass) refuseLive(step runstate.SweepStep, record runstate.TrackerStoreRebuild, now time.Time, why string) runstate.SweepStep {
	if record.Rebuild != nil && record.Rebuild.Kept != "" && !record.Rebuild.Restored {
		// The old store was moved aside and never replaced: whoever reads this
		// has to put it back by hand, so it is said as a failure.
		step = p.refuseRebuild(step, record, now, why+fmt.Sprintf(". The old store is at %s and the live store may be missing", record.Rebuild.Kept))
		step.Outcome = runstate.StepFailed
		return step
	}
	return p.refuseRebuild(step, record, now, why)
}

// stillOverAfter says the rebuilt store's size, and whether it is still past
// its limit.
func (p *Pass) stillOverAfter(rebuilt beads.StoreRebuild) (string, bool) {
	limits := p.trackerLimits()
	if rebuilt.SizeAfter <= int64(limits.StoreMegabytes)<<20 {
		return "", false
	}
	return fmt.Sprintf("the rebuilt store is %s on disk, past its limit of %d MB", runstate.SayBytes(rebuilt.SizeAfter), limits.StoreMegabytes), true
}

// decidedBefore is every decision recorded before record, which the warning
// about a store still past its limit compares its figures with.
func decidedBefore(p *Pass, record runstate.TrackerStoreRebuild) runstate.TrackerStoreRebuilds {
	decided, err := p.Rebuilds.Read()
	if err != nil {
		return nil
	}
	var earlier runstate.TrackerStoreRebuilds
	for _, each := range decided {
		if each.Name != record.Name {
			earlier = append(earlier, each)
		}
	}
	return earlier
}

// warnStillOver files the one warning for the development manager that the
// rebuilt store is still past its limit, unless the last rebuild before it that
// replaced the store carried the same figures and its warning was filed, and
// records which. A warning that cannot be filed is filed by the next pass: the
// record says it is unwarned.
func (p *Pass) warnStillOver(step runstate.SweepStep, record runstate.TrackerStoreRebuild, earlier runstate.TrackerStoreRebuilds, now time.Time) runstate.SweepStep {
	if last, found := earlier.LastReplaced(); found && last.StillOver == record.StillOver && last.StillOverReported != "" {
		record.StillOverReported = last.StillOverReported
	}
	if record.StillOverReported == "" {
		if p.Reports == nil {
			step.Outcome = runstate.StepFailed
			step.Detail += "; the store is still past its limit after the rebuild, and no report pile is wired, so the development manager could not be warned"
			return step
		}
		message := fmt.Sprintf("Maintenance rebuilt the tracker store from its export (the %s step), and the store is still past its limit: %s. "+
			"The design on keeping the store small (docs/designs/tracker-store-size.md) says that when a rebuild leaves the store past its limit the current data is too large, and fewer writes to the store are the remedy. "+
			"This warning is not filed again until these figures change. The rebuild is recorded in %s.",
			StepTrackerStoreRebuild, record.StillOver, p.Rebuilds.Path())
		filed, err := p.warn(record, message, now)
		if err != nil {
			step.Outcome = runstate.StepFailed
			step.Detail += fmt.Sprintf("; the store is still past its limit after the rebuild, and the warning to the development manager could not be filed: %v", err)
			return step
		}
		record.StillOverReported = filed
	}
	if err := p.Rebuilds.Record(record); err != nil {
		step.Outcome = runstate.StepFailed
		step.Detail += fmt.Sprintf("; the store is still past its limit after the rebuild, and which warning says so could not be recorded: %v", err)
		return step
	}
	step.Detail += "; the store is STILL PAST its limit (" + record.StillOver + "), and the development manager was warned in " + record.StillOverReported
	return step
}

// warn files one warning about a rebuild decision for the development manager,
// and returns its id.
func (p *Pass) warn(record runstate.TrackerStoreRebuild, message string, now time.Time) (string, error) {
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityWarning,
		For:      domain.RoleDevelopmentManager,
		Message:  oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)),
	}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        "tracker-store-rebuild:" + record.Name,
		Build:        p.Build,
		ProductID:    p.Product,
		RepositoryID: p.RepositoryID,
	}, now)
	if err != nil {
		return "", err
	}
	if err := p.Reports.Append(collected[0]); err != nil {
		return "", err
	}
	return collected[0].ID, nil
}
