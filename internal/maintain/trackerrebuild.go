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
// harness's own listings of open and ready work over the hour before took longer
// than listing_seconds (rebuildDue). Both are read without asking the store
// anything: the size from the disk and the listings' times from the log of
// calls every harness process keeps, because a store too large to answer in
// time is exactly the one that is due.
//
// Each time one is due, it is done first on a copy of the live store. Only
// where that copy's two exports held every record the same, and building took
// no more than rebuildBound, is the live store rebuilt, holding the store lock
// exclusive so every other tracker call waits behind it for that long. Nothing
// waits for runs in flight and nothing holds new runs: the bound is what keeps
// the wait inside the 120 seconds an ordinary tracker read allows.
//
// A rehearsal that failed, found a record different, or took too long is
// recorded and warns the development manager once, and is not tried again for
// rebuildRetry, so a store that cannot be rebuilt is not copied on every pass.
// A rebuilt store still past its limit warns once with its figures, and again
// only when they change. One that never started, because another process had
// the store open or it changed while it was copied, is tried again at the next
// pass and recorded nowhere.

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
	due, size, unread := p.rebuildDue(now)
	if due == "" {
		step.Outcome, step.Detail = runstate.StepSkipped, "the tracker's store is within its limits, so it is not rebuilt"+unread
		if size > 0 {
			step.Detail = fmt.Sprintf("the tracker's store is %s on disk and its listings within their limit, so it is not rebuilt%s", runstate.SayBytes(size), unread)
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
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the store is due a rebuild because %s, and the directory to build it in could not be made, so it was not rebuilt: %v", due, err)
		return step
	}
	defer work.Close()
	rehearsal, err := p.Rebuilder.RehearseRebuild(ctx, work)
	switch {
	case len(rehearsal.Busy) > 0:
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("the store is due a rebuild because %s, and it was not tried because another process had the store open: %s; the next pass tries again", due, beads.SayHolders(rehearsal.Busy))
		return step
	case rehearsal.Moved != "":
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("the store is due a rebuild because %s, and it was not tried because %s; the next pass tries again", due, rehearsal.Moved)
		return step
	case rehearsal.Failed == "" && err != nil:
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the store is due a rebuild because %s, and a copy of it could not be made or cleaned up: %v; the next pass tries again", due, err)
		return step
	}
	record.Rehearsal = &rehearsal
	if rehearsal.Passed() && rehearsal.Took > rebuildBound {
		record.TooLong = rehearsal.Took
		return p.refuseRebuild(step, record, now, fmt.Sprintf("on a copy the rebuild took %s, longer than the %s a rebuild may hold the store. A rebuild that long is a question for the architect and is not run", roundTook(rehearsal.Took), roundTook(rebuildBound)))
	}
	if !rehearsal.Passed() {
		return p.refuseRebuild(step, record, now, p.describeRefusal(rehearsal, "on a copy"))
	}
	return p.rebuildLive(ctx, step, record, work, now)
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
// or the harness's own listings of open and ready work over the hour before took
// longer than listing_seconds. size is the size read, zero where it was not, and
// unread says, as a clause to add to the step, a measure that could not be
// read. Both measures ask the store nothing, so they answer at any size.
func (p *Pass) rebuildDue(now time.Time) (due string, size int64, unread string) {
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
	if p.Calls != nil {
		if timed, err := p.liveListings(now, limits); err != nil {
			unread += fmt.Sprintf("; the log of the harness's tracker calls could not be read, so whether its listings on the live store were slow is not known: %v", err)
		} else if timed.over > 0 {
			reasons = append(reasons, timed.saysOver(limits, "limit"))
		}
	}
	return strings.Join(reasons, ", and "), size, unread
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
func (p *Pass) rebuildLive(ctx context.Context, step runstate.SweepStep, record runstate.TrackerStoreRebuild, work *repowrite.PinnedRoot, now time.Time) runstate.SweepStep {
	due := "the tracker's store is due a rebuild from its export because " + record.Reason
	kept, err := p.Rebuilds.KeptDirectory()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("%s, and the directory its old store is kept in could not be made, so it was not rebuilt: %v", due, err)
		return step
	}
	defer kept.Close()
	live, cancel := context.WithTimeout(ctx, rebuildLiveBound)
	defer cancel()
	rebuilt, err := p.Rebuilder.RebuildStore(live, work, kept, record.Name, func(placed beads.StoreRebuild) error {
		record.Rebuild = &placed
		return p.Rebuilds.Record(record)
	})
	switch {
	case len(rebuilt.Busy) > 0:
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("%s, and it was not rebuilt because another process had it open: %s; the next pass tries again", due, beads.SayHolders(rebuilt.Busy))
		return step
	case rebuilt.Unknown != "":
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("%s, and it was not rebuilt because whether another process had it open could not be told: %s; the next pass tries again", due, rebuilt.Unknown)
		return step
	}
	record.Rebuild, record.Ended = &rebuilt, true
	if !rebuilt.Swapped {
		return p.refuseLive(step, record, now, p.describeRefusal(rebuilt, "on the live store"))
	}
	step.Outcome = runstate.StepRan
	recorded := "; recorded in " + p.Rebuilds.Path()
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
