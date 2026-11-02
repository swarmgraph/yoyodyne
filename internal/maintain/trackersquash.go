package maintain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Squashing the live tracker store's history.
//
// The design on keeping the store small (docs/designs/tracker-store-size.md)
// lets the live store's record of past versions be squashed, never an item or
// a note, and only when all three of these hold:
//
//  1. the latest measurement on a copy shows every item and note unchanged
//     across that squash;
//  2. maintenance alone holds the store;
//  3. the live store has first been copied, the copy kept for 14 days and its
//     place recorded in the harness's state.
//
// The first is decided here, from the measurement; the second and third are
// held by the squash itself (internal/beads, storesquash.go), which copies the
// store, hands the copy back to be recorded, and only then runs bd.
//
// Which squash, if any, the latest measurement chooses is the design's own
// branching: none where the listings of open and ready items are within
// squashListingLimit as the store is or after the cleanup; history older than
// 30 days where squashing it brings them within the limit; all history only
// where that alone does not and squashing all of it does; and none where
// nothing does, which is the weekly check's to say. A measurement on which any
// stage changed an item or a note refuses both squashes, and the development
// manager is warned once, naming the stage and the records.
//
// A squash holds the store's exclusive lock for its whole length, so a run
// in the middle of its work when it starts can have its tracker reads time out
// over nothing it did. So a squash also has to start with no developer run in
// flight, and is held to the watch session's redeploy drain bound, the longest
// the harness already makes its runs wait for anything:
//
//   - one whose time on the measurement's copy is longer than that bound is not
//     run, and the development manager is warned once, naming the step and the
//     time;
//   - otherwise the store's size on disk is read, which asks bd nothing and so
//     answers at any size, and then new runs are held (runstate,
//     trackersquashhold.go): no watching session starts one while the hold
//     stands, and the squash waits for the runs already going to finish, for
//     as long as the bound;
//   - runs still in flight at the bound mean no squash: the hold is lifted, new
//     runs start again, and the development manager is warned once, naming the
//     step and how many were still going.
//
// The hold is lifted however the step ends, a failure partway included, and
// carries its own end besides, the bound and the squash's measured time on the
// copy after it was placed, so a pass that dies holding it does not hold the
// line for ever. Nothing in deciding the squash lists or counts work items:
// that is what an oversized store cannot answer in time, and the decision has
// to be made exactly when it is oversized.
//
// Each measurement is decided once. A squash that ran, failed, or was refused
// is recorded against the measurement it was decided on, and so is one skipped
// for its measured time or for runs still in flight, so its warning is filed
// once; the next is decided on the next measurement. One that never started,
// because another
// process had the store open or it changed while it was copied, is tried again
// at the next pass.

const (
	// squashListingLimit is the design's limit on listing open items and
	// listing ready items.
	squashListingLimit = 10 * time.Second
	// squashCopyKept is how long the copy made before a squash is kept.
	squashCopyKept = 14 * 24 * time.Hour
	// squashBound bounds bd's squash of the live store, the same bound each call
	// on a measurement's copy is given. A squash stopped there is recorded as
	// failed, and the copy made before it is what the store is restored from.
	squashBound = 20 * time.Minute
	// squashSettle is how long new runs are held before the runs in flight are
	// first counted. A pull that read no hold just before it was placed can still
	// start a run once it has read the tracker, which takes at most one tracker
	// read's wait, and that run has to be counted.
	squashSettle = beads.DefaultTimeout
	// squashRunsPoll is how often the runs in flight are counted again while the
	// squash waits for them.
	squashRunsPoll = 15 * time.Second
)

// StoreSquasher squashes the live tracker store. It is satisfied by
// beads.Client.
type StoreSquasher interface {
	StoreSize() (string, int64, error)
	SquashStore(ctx context.Context, stage string, copies *repowrite.PinnedRoot, name string, limit time.Duration, copied func(beads.StoreSquash) error) (beads.StoreSquash, error)
}

// Squashes is the record of each squash decision, and where the copies are
// kept. It is satisfied by *runstate.TrackerStoreSquashStore.
type Squashes interface {
	Read() (runstate.TrackerStoreSquashes, error)
	Record(runstate.TrackerStoreSquash) error
	NewName(now time.Time) string
	CopyDirectory() (*repowrite.PinnedRoot, error)
	RemoveCopiesMadeBefore(cutoff time.Time) ([]string, error)
	Path() string
}

// SquashHolds is the hold on new runs while a squash is due or running. It is
// satisfied by *runstate.TrackerSquashHoldStore.
type SquashHolds interface {
	Place(runstate.TrackerSquashHold) error
	Lift() error
	Path() string
}

// RunsInFlight is the product's runs as the squash counts the developer runs
// in flight. It is satisfied by *runstate.Store.
type RunsInFlight interface {
	Incomplete() ([]runstate.State, error)
	HoldsDeveloperSlotNow(state runstate.State, now time.Time) bool
}

// Reports is where the pass files a warning. It is satisfied by
// *runstate.ReportStore.
type Reports interface {
	Append(report.Report) error
}

// squashTracker decides a squash of the live store on the latest measurement,
// where none has been decided on it yet, and removes the copies older than
// squashCopyKept. It runs off the supervisor's goroutine, after the
// measurement, and touches nothing of the supervisor's.
func (p *Pass) squashTracker(ctx context.Context) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepTrackerSquash}
	if p.Squasher == nil || p.Squashes == nil || p.Measurements == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no squash of the tracker's store is wired, so it is not squashed"
		return step
	}
	if p.SquashHolds == nil || p.Runs == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "nothing is wired to hold new runs or count the runs in flight, so the tracker's store is not squashed, because a squash that started under a run would make its tracker reads wait"
		return step
	}
	now := p.now()
	removed, removeErr := p.Squashes.RemoveCopiesMadeBefore(now.Add(-squashCopyKept))
	step = p.decideSquash(ctx, step, now)
	if len(removed) > 0 {
		step.Detail += fmt.Sprintf("; removed the copies of the store made more than %d days ago: %s", int(squashCopyKept/(24*time.Hour)), strings.Join(removed, ", "))
	}
	if removeErr != nil {
		step.Outcome = runstate.StepFailed
		step.Detail += fmt.Sprintf("; a copy of the store made more than %d days ago could not be removed, and the next pass tries again: %v", int(squashCopyKept/(24*time.Hour)), removeErr)
	}
	return step
}

func (p *Pass) decideSquash(ctx context.Context, step runstate.SweepStep, now time.Time) runstate.SweepStep {
	measurements, err := p.Measurements.Read()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the record of measurements of the tracker's store could not be read, so no squash can be decided: %v", err)
		return step
	}
	latest, found := measurements.Latest()
	if !found {
		step.Outcome, step.Detail = runstate.StepSkipped, "the tracker's store has not been measured on a copy yet, so no squash can be decided"
		return step
	}
	decided, err := p.Squashes.Read()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the record of earlier squashes of the tracker's store could not be read, so whether one was already decided cannot be told: %v", err)
		return step
	}
	if earlier, found := decided.OnMeasurement(latest.StartedAt()); found {
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("already decided on the latest measurement: %s; the next squash is decided on the next measurement", earlier.Says())
		return step
	}
	if changed := latest.ChangedRecords(); len(changed) > 0 {
		return p.refuseSquash(step, latest, changed, now)
	}
	stage, reason, none := squashChoice(latest)
	if none != "" {
		step.Outcome, step.Detail = runstate.StepSkipped, none
		return step
	}
	record := runstate.TrackerStoreSquash{
		Name:       p.Squashes.NewName(now),
		MeasuredAt: latest.StartedAt(),
		Reason:     reason,
		Squash:     beads.StoreSquash{Stage: stage},
	}
	bound := p.drainBound()
	measured, _ := latest.Measurement.Stage(stage)
	if measured.Took > bound {
		record.TooLong, record.DrainBound = measured.Took, bound
		return p.skipSquash(step, record, now)
	}
	return p.holdAndSquash(ctx, step, latest, record, latest.Measurement.CopyTook+measured.Took, now)
}

// holdAndSquash reads the store's size on disk, holds new runs, waits for the
// developer runs in flight to finish, and squashes once none is, lifting the
// hold however it ends. expected is the copy and the squash's time on the
// measurement's copy, which the hold's own end allows for.
func (p *Pass) holdAndSquash(ctx context.Context, step runstate.SweepStep, measured runstate.TrackerStoreMeasurement, record runstate.TrackerStoreSquash, expected time.Duration, now time.Time) (ended runstate.SweepStep) {
	stage := record.Squash.Stage
	due := fmt.Sprintf("the tracker's store is due a squash of its %s because %s", runstate.SquashedHistory(stage), record.Reason)
	_, size, err := p.Squasher.StoreSize()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("%s, and its size on disk could not be read, so new runs were not held and it was not squashed: %v; the next pass tries again", due, err)
		return step
	}
	record.SizeOnDisk = size
	bound := p.drainBound()
	since := p.now()
	if err := p.SquashHolds.Place(runstate.TrackerSquashHold{Since: since, Until: since.Add(bound + expected), Reason: record.Reason}); err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("%s, and new runs could not be held, so it was not squashed: %v; the next pass tries again", due, err)
		return step
	}
	record.HeldRunsAt = since
	defer func() {
		if err := p.SquashHolds.Lift(); err != nil {
			ended.Outcome = runstate.StepFailed
			ended.Detail += fmt.Sprintf("; the hold on new runs in %s could not be lifted, so no run starts until it ends on its own at %s: %v", p.SquashHolds.Path(), local(since.Add(bound+expected)), err)
		}
	}()
	still, err := p.awaitRuns(ctx, since.Add(bound))
	record.WaitedForRuns = p.now().Sub(since)
	switch {
	case err != nil:
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("%s, and new runs were held, but the runs in flight could not be counted, so it was not squashed and new runs start again: %v; the next pass tries again", due, err)
		return step
	case still > 0:
		record.StillRunning, record.DrainBound = still, bound
		return p.skipSquash(step, record, now)
	}
	return p.runSquash(ctx, step, measured, record, now)
}

// awaitRuns waits, from when new runs were held until deadline, for no
// developer run to be in flight, and says how many still were where some were
// at the deadline. It looks first once squashSettle has passed, so a run a pull
// started just as the hold was placed is counted.
func (p *Pass) awaitRuns(ctx context.Context, deadline time.Time) (int, error) {
	wait := min(squashSettle, max(deadline.Sub(p.now()), 0))
	for {
		if !p.sleep(ctx, wait) {
			return 0, fmt.Errorf("the pass was stopped while it waited: %w", ctx.Err())
		}
		inFlight, err := p.developerRunsInFlight()
		if err != nil || inFlight == 0 {
			return 0, err
		}
		left := deadline.Sub(p.now())
		if left <= 0 {
			return inFlight, nil
		}
		wait = min(squashRunsPoll, left)
	}
}

// developerRunsInFlight counts the runs holding a developer slot now. A run a
// session stopped for its redeploy is not counted: it is doing nothing, and
// the session that would pick it up again starts nothing while new runs are
// held.
func (p *Pass) developerRunsInFlight() (int, error) {
	incomplete, err := p.Runs.Incomplete()
	if err != nil {
		return 0, err
	}
	now, count := p.now(), 0
	for _, state := range incomplete {
		if state.RedeployStop == nil && p.Runs.HoldsDeveloperSlotNow(state, now) {
			count++
		}
	}
	return count, nil
}

// skipSquash records a squash not run for its measured time or for runs still
// in flight, and warns the development manager once. A warning that cannot be
// filed is not recorded as decided, so the next pass files it.
func (p *Pass) skipSquash(step runstate.SweepStep, record runstate.TrackerStoreSquash, now time.Time) runstate.SweepStep {
	said := record.Says()
	if p.Reports == nil {
		step.Outcome, step.Detail = runstate.StepFailed, said+"; no report pile is wired, so the development manager could not be warned, and the next pass tries again"
		return step
	}
	var message string
	if record.TooLong > 0 {
		message = fmt.Sprintf("Maintenance did not squash the tracker store's %s (the %s step), because on the copy measured at %s the squash held the store %s, longer than the watch session's redeploy drain bound of %s. "+
			"A squash holds the store for its whole length, so every run's tracker reads would wait behind it; a squash that long is a question for the design on keeping the store small rather than something to run. The measurement is recorded in %s.",
			runstate.SquashedHistory(record.Squash.Stage), StepTrackerSquash, local(record.MeasuredAt), roundTook(record.TooLong), roundTook(record.DrainBound), p.Measurements.Path())
	} else {
		message = fmt.Sprintf("Maintenance did not squash the tracker store's %s (the %s step), because %d developer run(s) were still in flight after it had held new runs for %s, the watch session's redeploy drain bound. "+
			"New runs are being started again. The next squash is decided on the next measurement of the store; the decision is recorded in %s.",
			runstate.SquashedHistory(record.Squash.Stage), StepTrackerSquash, record.StillRunning, roundTook(record.DrainBound), p.Squashes.Path())
	}
	filed, err := p.warn(record, message, now)
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("%s; the warning to the development manager could not be filed, and the next pass tries again: %v", said, err)
		return step
	}
	record.Reported = filed
	step.Outcome, step.Detail = runstate.StepSkipped, record.Says()
	if err := p.Squashes.Record(record); err != nil {
		step.Outcome = runstate.StepFailed
		step.Detail += fmt.Sprintf("; the decision could not be recorded, so the next pass may warn again: %v", err)
	}
	return step
}

// drainBound is the watch session's redeploy drain bound, and the
// configuration's default where none is wired.
func (p *Pass) drainBound() time.Duration {
	if p.DrainBound > 0 {
		return p.DrainBound
	}
	return config.DefaultRedeployDrainLimit()
}

// sleep waits for d, and reports false where ctx ended first.
func (p *Pass) sleep(ctx context.Context, d time.Duration) bool {
	if p.Sleep != nil {
		return p.Sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// refuseSquash records that the store is not squashed on a measurement that
// changed records, and warns the development manager once. A warning that
// cannot be filed is not recorded as decided, so the next pass files it.
func (p *Pass) refuseSquash(step runstate.SweepStep, measured runstate.TrackerStoreMeasurement, changed []runstate.ChangedRecords, now time.Time) runstate.SweepStep {
	record := runstate.TrackerStoreSquash{
		Name:       p.Squashes.NewName(now),
		MeasuredAt: measured.StartedAt(),
		Changed:    changed,
	}
	said := record.Says()
	if p.Reports == nil {
		step.Outcome, step.Detail = runstate.StepFailed, said+"; no report pile is wired, so the development manager could not be warned, and the next pass tries again"
		return step
	}
	filed, err := p.warnChanged(record, now)
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("%s; the warning to the development manager could not be filed, and the next pass tries again: %v", said, err)
		return step
	}
	record.Reported = filed
	step.Outcome, step.Detail = runstate.StepSkipped, record.Says()
	if err := p.Squashes.Record(record); err != nil {
		step.Outcome = runstate.StepFailed
		step.Detail += fmt.Sprintf("; the decision could not be recorded, so the next pass may warn again: %v", err)
	}
	return step
}

// warnChanged files the one warning for the development manager that a
// measurement changed records, naming the stage and the records, and returns
// its id.
func (p *Pass) warnChanged(record runstate.TrackerStoreSquash, now time.Time) (string, error) {
	var stages []string
	for _, changed := range record.Changed {
		stages = append(stages, changed.Says())
	}
	message := fmt.Sprintf("Maintenance did not squash the tracker store's history, because the measurement on a copy at %s showed work items or notes changed: %s. "+
		"The store is squashed only where every item and note is unchanged across the squash on a copy, so neither squash runs until a later measurement shows none changed; somebody has to find out why bd changed those records on the copy. The measurement is recorded in %s.",
		record.MeasuredAt.Local().Format("2006-01-02 15:04 MST"), strings.Join(stages, "; "), p.Measurements.Path())
	return p.warn(record, message, now)
}

// warn files one warning about a squash decision for the development manager,
// and returns its id.
func (p *Pass) warn(record runstate.TrackerStoreSquash, message string, now time.Time) (string, error) {
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityWarning,
		For:      domain.RoleDevelopmentManager,
		Message:  oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)),
	}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        "tracker-store-squash:" + record.Name,
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

// runSquash squashes the live store as the record's stage says, recording the
// copy before bd runs and what the squash came to once it has ended.
func (p *Pass) runSquash(ctx context.Context, step runstate.SweepStep, measured runstate.TrackerStoreMeasurement, record runstate.TrackerStoreSquash, now time.Time) runstate.SweepStep {
	stage, reason := record.Squash.Stage, record.Reason
	copies, err := p.Squashes.CopyDirectory()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's store is due a squash of its %s because %s, and the directory its copy is made in could not be made, so it was not squashed: %v", runstate.SquashedHistory(stage), reason, err)
		return step
	}
	defer copies.Close()
	squashed, err := p.Squasher.SquashStore(ctx, stage, copies, record.Name, squashBound, func(copied beads.StoreSquash) error {
		record.Squash = copied
		return p.Squashes.Record(record)
	})
	due := fmt.Sprintf("the tracker's store is due a squash of its %s because %s", runstate.SquashedHistory(stage), reason)
	switch {
	case len(squashed.Busy) > 0:
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("%s, and it was not squashed because another process had it open: %s; the next pass tries again", due, beads.SayHolders(squashed.Busy))
		return step
	case squashed.Unknown != "":
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("%s, and it was not squashed because whether another process had it open could not be told: %s; the next pass tries again", due, squashed.Unknown)
		return step
	case squashed.Moved != "":
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("%s, and it was not squashed because %s; the next pass tries again", due, squashed.Moved)
		return step
	case !squashed.Ran():
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("%s, and it was not squashed: %v; the next pass tries again", due, err)
		return step
	}
	record.Squash, record.Ended = squashed, true
	if err != nil {
		record.Failed = err.Error()
	}
	recorded := "; recorded in " + p.Squashes.Path()
	if err := p.Squashes.Record(record); err != nil {
		recorded = fmt.Sprintf("; its end could not be recorded, so the record says it started and not how it ended: %v", err)
	}
	step.Outcome, step.Detail = runstate.StepRan, fmt.Sprintf("squashed the tracker store's %s; the copy is kept until %s, and every other tracker call waited behind bd while it held the store%s",
		record.Says(), local(squashed.CopiedAt.Add(squashCopyKept)), recorded)
	if record.Failed != "" || !strings.HasPrefix(recorded, "; recorded") {
		step.Outcome = runstate.StepFailed
		step.Detail = fmt.Sprintf("tried to squash the tracker store's %s; it did not finish, and the copy is kept until %s and is what the store is restored from if it was left damaged%s",
			record.Says(), local(squashed.CopiedAt.Add(squashCopyKept)), recorded)
	}
	return step
}

// squashChoice is the squash the measurement chooses and why, or, where it
// chooses none, why not. It is decided only on a measurement no stage of which
// changed a record.
func squashChoice(measured runstate.TrackerStoreMeasurement) (stage, reason, none string) {
	at := measured.StartedAt().Local().Format("2006-01-02 15:04 MST")
	if measured.Failed != "" || !measured.Measurement.Measured() {
		return "", "", fmt.Sprintf("the latest measurement of the tracker's store, at %s, did not finish, so no squash is decided on it", at)
	}
	limit := sayLimit(squashListingLimit)
	for _, name := range []string{beads.StageAsItIs, beads.StageCleanedUp} {
		if within, said := listingsWithin(measured, name); within {
			return "", "", fmt.Sprintf("the latest measurement, at %s, listed open and ready items within %s %s (%s), so no squash is needed", at, limit, name, said)
		}
	}
	unchanged := []string{beads.StageCleanedUp}
	cleaned := sayListings(measured, beads.StageCleanedUp)
	for _, name := range []string{beads.StageOldSquashed, beads.StageAllSquashed} {
		unchanged = append(unchanged, name)
		for _, earlier := range unchanged {
			if shown, why := exportShown(measured, earlier); !shown {
				return "", "", fmt.Sprintf("the latest measurement, at %s, cannot show every item and note unchanged %s, because %s, so the store is not squashed", at, earlier, why)
			}
		}
		within, said := listingsWithin(measured, name)
		if !within {
			continue
		}
		squashed, _ := measured.Measurement.Stage(name)
		return name, fmt.Sprintf("the latest measurement, at %s, listed open and ready items %s after the cleanup and within %s only %s (%s), with every item and note unchanged; on the copy, bd %s held the store %s",
			at, cleaned, limit, name, said, strings.Join(squashed.Command, " "), roundTook(squashed.Took)), ""
	}
	return "", "", fmt.Sprintf("the latest measurement, at %s, listed open and ready items within %s at no stage, not even after squashing all history (%s), so no squash brings them within it and the store is not squashed",
		at, limit, sayListings(measured, beads.StageAllSquashed))
}

// listingsWithin reports a stage whose listings of open and ready items both
// answered within squashListingLimit, and says how long they took.
func listingsWithin(measured runstate.TrackerStoreMeasurement, name string) (bool, string) {
	stage, found := measured.Measurement.Stage(name)
	if !found || stage.Failed != "" {
		return false, "not measured"
	}
	within := true
	for _, read := range []string{beads.ReadOpenListing, beads.ReadReadyListing} {
		listing, found := stage.Read(read)
		if !found || listing.Failed != "" || listing.Took > squashListingLimit {
			within = false
		}
	}
	return within, sayListings(measured, name)
}

// sayListings is how long a stage's two listings took.
func sayListings(measured runstate.TrackerStoreMeasurement, name string) string {
	stage, found := measured.Measurement.Stage(name)
	if !found || stage.Failed != "" {
		return "not measured"
	}
	var said []string
	for _, read := range []string{beads.ReadOpenListing, beads.ReadReadyListing} {
		listing, found := stage.Read(read)
		switch {
		case !found:
			said = append(said, read+" not timed")
		case listing.Failed != "":
			said = append(said, fmt.Sprintf("%s failed after %s", read, roundTook(listing.Took)))
		default:
			said = append(said, fmt.Sprintf("%s %s", read, roundTook(listing.Took)))
		}
	}
	return strings.Join(said, ", ")
}

// exportShown reports a stage whose export was taken and compared with the one
// before it, and says why where it was not.
func exportShown(measured runstate.TrackerStoreMeasurement, name string) (bool, string) {
	stage, found := measured.Measurement.Stage(name)
	switch {
	case !found:
		return false, "it did not reach that stage"
	case stage.Failed != "":
		return false, "that stage failed: " + stage.Failed
	case stage.Export.Failed != "":
		return false, "its export at that stage failed: " + stage.Export.Failed
	case !stage.Export.Unchanged():
		return false, "its export at that stage was not compared with the one before"
	}
	return true, ""
}

func sayLimit(limit time.Duration) string {
	return fmt.Sprintf("%d seconds", int(limit/time.Second))
}
