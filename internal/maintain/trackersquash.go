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
// Each measurement is decided once. A squash that ran, failed, or was refused
// is recorded against the measurement it was decided on, and the next is
// decided on the next measurement; one that never started, because another
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
)

// StoreSquasher squashes the live tracker store. It is satisfied by
// beads.Client.
type StoreSquasher interface {
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
	return p.runSquash(ctx, step, latest, stage, reason, now)
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

// runSquash squashes the live store as stage says, recording the copy before
// bd runs and what the squash came to once it has ended.
func (p *Pass) runSquash(ctx context.Context, step runstate.SweepStep, measured runstate.TrackerStoreMeasurement, stage, reason string, now time.Time) runstate.SweepStep {
	copies, err := p.Squashes.CopyDirectory()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's store is due a squash of its %s because %s, and the directory its copy is made in could not be made, so it was not squashed: %v", runstate.SquashedHistory(stage), reason, err)
		return step
	}
	defer copies.Close()
	record := runstate.TrackerStoreSquash{
		Name:       p.Squashes.NewName(now),
		MeasuredAt: measured.StartedAt(),
		Reason:     reason,
	}
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
