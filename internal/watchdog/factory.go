package watchdog

// The factory stall, noticed by the supervisor and said as a report.
//
// The stall check beside this one asks whether anything has started over ready
// work, and asks the tracker how much is ready to answer it. That is exactly
// the question it cannot answer when the tracker is what has stopped. From
// 21:51 PDT on 2026-09-29 no recurring pass completed, the watch pulled
// nothing, and nothing said so for twelve hours; it was read then as the
// tracker's listings timing out, and was the machine asleep with one pass
// holding the watch's poll across the sleep
// (docs/diagnoses/yoyodyne-ifd-433-20-tracker-listing-timeouts.md). Either way
// the check has to stand apart from what stopped. It reads only the run records
// and the sweep log, asks no tracker and
// no provider, and runs on the supervisor's own poll rather than inside the
// watch whose passes are the ones failing.
//
// What it says it says as the harness's own reports, into the pile every other
// report goes to: a critical one when the stall begins, which is put in front
// of the operator where he is and delivered to the Lead Product Manager as a
// turn of its own, and a note when it clears. The durable record is what makes
// each of those once per stall rather than once per minute.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultFactoryEvery is how often the supervisor reads whether the factory
// has stalled. Every reading is two local files, so a minute costs nothing and
// is the most a stall waits past its limit to be said.
const DefaultFactoryEvery = time.Minute

// Passes is the sweep log, read for what each recurring pass came to. It is
// satisfied by *runstate.SweepStore.
type Passes interface {
	List() ([]runstate.Sweep, []runstate.UnreadableSweep, error)
}

// Reports is the pile the harness's own reports are filed into. It is
// satisfied by *runstate.ReportStore.
type Reports interface {
	Append(reported report.Report) error
}

// FactoryWatch reads whether the factory has stalled and says so once. It is a
// resident of the supervisor: Name and Look are what the supervisor asks of
// one.
type FactoryWatch struct {
	Runs   Runs
	Passes Passes
	Holds  readmodel.OperatorHolds
	Stalls *runstate.FactoryStallStore
	// Reports is where the stall and its recovery are filed, under Attribution's
	// product, repository, and build.
	Reports     Reports
	Attribution report.Attribution
	// Limit is execution.factory_stall_after; zero takes the read model's
	// default. Every is how often Look reads; zero takes DefaultFactoryEvery.
	Limit time.Duration
	Every time.Duration
	Log   func(format string, args ...any)

	lastLook time.Time
}

// FactoryReading is what one check came to.
type FactoryReading struct {
	Stall    *readmodel.FactoryStall
	Opened   *runstate.FactoryStallEvent
	Closed   *runstate.FactoryStallEvent
	Standing *runstate.FactoryStallEvent
}

// Name is how the supervisor names the resident in what it says.
func (w *FactoryWatch) Name() string { return "factory-stall" }

// Look checks at most once per Every, and says in the supervisor's log what a
// check could not do; the next look asks again.
func (w *FactoryWatch) Look(ctx context.Context, now time.Time) {
	every := w.Every
	if every <= 0 {
		every = DefaultFactoryEvery
	}
	if !w.lastLook.IsZero() && now.Before(w.lastLook.Add(every)) {
		return
	}
	w.lastLook = now
	reading, err := w.Check(ctx, now)
	switch {
	case err != nil:
		w.log("could not read whether the factory has stalled: %v", err)
	case reading.Opened != nil:
		w.log("the factory has stalled: %s", reading.Opened.Says)
	case reading.Closed != nil:
		w.log("the factory stall that began at %s has cleared: %s", reading.Closed.Since.UTC().Format(time.RFC3339), reading.Closed.Cleared)
	}
}

// Check reads the records once, reconciles the stall record against them, and
// files a report for a stall it opened or closed. Nothing is recorded where a
// record could not be read: deciding over a record it could not read is the
// mistake in both directions.
func (w *FactoryWatch) Check(ctx context.Context, now time.Time) (FactoryReading, error) {
	if err := w.validate(); err != nil {
		return FactoryReading{}, err
	}
	runs, err := w.Runs.Recorded()
	if err != nil {
		return FactoryReading{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	passes, _, err := w.Passes.List()
	if err != nil && len(passes) == 0 {
		return FactoryReading{}, fmt.Errorf("read the recurring passes: %w", err)
	}
	_, paused, err := w.Holds.Held()
	if err != nil {
		return FactoryReading{}, fmt.Errorf("read the operator's pause: %w", err)
	}
	stall, stalled := readmodel.FactoryStallOf(runs, passes, paused, w.Limit, now)
	observation := runstate.FactoryStallObservation{Stalled: stalled, At: now}
	if stalled {
		observation.Since, observation.Says = stall.Since, stall.Says()
	} else {
		// A reading that finds no stall closes a standing one only where the
		// factory actually moved after it began: a pull, or a pass that
		// succeeded. The operator's pause, a longer limit, or a sweep log that no
		// longer holds the failures each stop the reading calling it a stall
		// without anything having recovered, and closing on one of them would
		// file a recovery that did not happen and, once it lifted, a second
		// report of the same stall. Such a reading leaves the record as it is.
		standing, open, err := w.Stalls.Standing()
		if err != nil {
			return FactoryReading{}, err
		}
		if !open {
			return FactoryReading{}, nil
		}
		cleared, moved := movedSince(runs, passes, standing.Since)
		if !moved {
			return FactoryReading{Standing: &standing}, nil
		}
		observation.Cleared = cleared
	}
	reconciled, err := w.Stalls.Reconcile(observation)
	if err != nil {
		return FactoryReading{}, err
	}
	reading := FactoryReading{Opened: reconciled.Opened, Closed: reconciled.Closed, Standing: reconciled.Standing}
	if stalled {
		reading.Stall = &stall
	}
	var problems []error
	if reconciled.Opened != nil {
		if err := w.file(report.SeverityCritical, *reconciled.Opened, stallMessage(*reconciled.Opened), now); err != nil {
			problems = append(problems, fmt.Errorf("report the factory stall: %w", err))
		}
	}
	if reconciled.Closed != nil {
		if err := w.file(report.SeverityNote, *reconciled.Closed, recoveryMessage(*reconciled.Closed), now); err != nil {
			problems = append(problems, fmt.Errorf("report the factory's recovery: %w", err))
		}
	}
	return reading, errors.Join(problems...)
}

// file is one of the harness's own reports about a stall. The run it names is
// the stall, by the moment it is measured from, because no run produced it.
func (w *FactoryWatch) file(severity report.Severity, event runstate.FactoryStallEvent, message string, now time.Time) error {
	attribution := w.Attribution
	attribution.Role = report.HarnessReporter
	attribution.Agent = ""
	attribution.RunID = "factory-stall@" + event.Since.UTC().Format(time.RFC3339)
	attribution.WorkItemID = ""
	collected, err := report.Collect([]report.Entry{{Severity: severity, Message: message}}, attribution, now)
	if err != nil {
		return err
	}
	for _, reported := range collected {
		if err := w.Reports.Append(reported); err != nil {
			return err
		}
	}
	return nil
}

// stallMessage is the two sentences the stall is reported in.
func stallMessage(event runstate.FactoryStallEvent) string {
	return fmt.Sprintf("The factory has stalled: %s. "+
		"No role is looking at anything while it stands, so this report is the only thing that will say so; the first pull or successful pass ends it and files a note saying it has cleared.",
		oneline.Bound(event.Says, report.MaxMessageBytes*3/4))
}

// recoveryMessage is the two sentences the stall's end is reported in.
func recoveryMessage(event runstate.FactoryStallEvent) string {
	return fmt.Sprintf("The factory stall that began at %s and was reported at %s has cleared after %s: %s. "+
		"Work is being pulled or passes are succeeding again, and the critical report filed when it began can be handled as over.",
		event.Since.Local().Format("2006-01-02 15:04 MST"), event.OpenedAt.Local().Format("2006-01-02 15:04 MST"),
		event.ClosedAt.Sub(event.Since).Round(time.Minute), oneline.Bound(event.Cleared, report.MaxMessageBytes/2))
}

// movedSince says what the factory did after a stall began, and whether it
// did anything: the latest pull or successful pass started after since.
func movedSince(runs []runstate.State, passes []runstate.Sweep, since time.Time) (string, bool) {
	var pull, pass time.Time
	var task string
	for _, run := range runs {
		if run.StartedAt.After(since) && run.StartedAt.After(pull) {
			pull = run.StartedAt
		}
	}
	for _, recorded := range passes {
		if readmodel.PassSucceeded(recorded) && recorded.StartedAt.After(since) && recorded.StartedAt.After(pass) {
			pass, task = recorded.StartedAt, recorded.Task
		}
	}
	switch {
	case !pass.IsZero() && pass.After(pull):
		return fmt.Sprintf("the recurring pass %s succeeded at %s", task, pass.Local().Format("2006-01-02 15:04 MST")), true
	case !pull.IsZero():
		return "work was pulled at " + pull.Local().Format("2006-01-02 15:04 MST"), true
	default:
		return "", false
	}
}

func (w *FactoryWatch) validate() error {
	var problems []error
	if w.Runs == nil {
		problems = append(problems, errors.New("a factory stall check requires the recorded runs"))
	}
	if w.Passes == nil {
		problems = append(problems, errors.New("a factory stall check requires the recurring passes"))
	}
	if w.Holds == nil {
		problems = append(problems, errors.New("a factory stall check requires the operator's pause"))
	}
	if w.Stalls == nil {
		problems = append(problems, errors.New("a factory stall check requires the product's factory stall record"))
	}
	if w.Reports == nil {
		problems = append(problems, errors.New("a factory stall check requires the report pile"))
	}
	return errors.Join(problems...)
}

func (w *FactoryWatch) log(format string, args ...any) {
	if w.Log != nil {
		w.Log(format, args...)
	}
}
