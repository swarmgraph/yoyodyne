package maintain

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The limits step: the live tracker store held to the size and listing time
// services.maintenance.tracker_limits sets, on every pass.
//
// It asks the store nothing. The size is read from the disk, as the cleanup
// reads it, and the listing times from the log of calls every harness process
// keeps (runstate, trackercalllog.go), over the hour before the pass, so the
// check costs the store no read and is not itself slowed by the slowness it
// looks for. Within both bounds the step says so with the figures. Past either,
// it says which in ordinary words and warns the development manager once; it
// warns again only when what is past its bound changes, and once more after
// the supervisor restarts, since what was warned is kept in the pass rather
// than recorded.

// TrackerCalls is the log of every tracker call the harness's processes made,
// read back. It is satisfied by *runstate.TrackerCallLogStore.
type TrackerCalls interface {
	Read(since, until time.Time) ([]runstate.TrackerCallRecord, int, error)
	Path() string
}

// callWindow is how far back the harness's own calls are read, for the limits
// and for a measurement's count of them by caller.
const callWindow = time.Hour

const (
	limitStore    = "store"
	limitListings = "listings"
)

// checkTrackerLimits compares the live store's size and the harness's listing
// times over the last hour with their bounds. It runs off the supervisor's
// goroutine, after the squash, and touches nothing of the supervisor's but
// limitsWarned, which only this goroutine reads or writes while a pass runs.
func (p *Pass) checkTrackerLimits() runstate.SweepStep {
	step := runstate.SweepStep{Name: StepTrackerLimits}
	if p.Tracker == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no tracker store is wired, so its limits are not checked"
		return step
	}
	limits := p.trackerLimits()
	now := p.now()
	var past, within, unchecked []string
	var passed []string

	if _, size, err := p.Tracker.StoreSize(); err != nil {
		unchecked = append(unchecked, fmt.Sprintf("the store's size could not be read: %v", err))
	} else if bound := int64(limits.StoreMegabytes) << 20; size > bound {
		passed = append(passed, limitStore)
		past = append(past, fmt.Sprintf("the tracker's store is %s on disk, past its bound of %d MB (services.maintenance.tracker_limits.store_megabytes)", beads.SayBytes(size), limits.StoreMegabytes))
	} else {
		within = append(within, fmt.Sprintf("the store is %s on disk, within its bound of %d MB", beads.SayBytes(size), limits.StoreMegabytes))
	}

	if p.Calls == nil {
		unchecked = append(unchecked, "no log of the harness's tracker calls is wired, so the listings' times are not checked")
	} else if records, _, err := p.Calls.Read(now.Add(-callWindow), now); err != nil {
		unchecked = append(unchecked, fmt.Sprintf("the log of the harness's tracker calls could not be read, so the listings' times are not checked: %v", err))
	} else {
		bound := time.Duration(limits.ListingSeconds) * time.Second
		listings, over, slowest := 0, 0, runstate.TrackerCallRecord{}
		for _, record := range records {
			if !openOrReadyListing(record) {
				continue
			}
			listings++
			if record.Took > bound || record.StoppedAtLimit {
				over++
			}
			if record.Took > slowest.Took {
				slowest = record
			}
		}
		switch {
		case listings == 0:
			within = append(within, "the harness listed no open or ready work in the hour before")
		case over > 0:
			passed = append(passed, limitListings)
			past = append(past, fmt.Sprintf("%d of the %d listings of open or ready work the harness made in the hour before took longer than their bound of %d seconds (services.maintenance.tracker_limits.listing_seconds); the slowest, %s for %s, took %s",
				over, listings, limits.ListingSeconds, slowest.Call, slowest.Caller(), roundTook(slowest.Took)))
		default:
			within = append(within, fmt.Sprintf("every listing of open or ready work in the hour before (%d of them) took at most %d seconds, the slowest %s", listings, limits.ListingSeconds, roundTook(slowest.Took)))
		}
	}

	step.Outcome = runstate.StepRan
	tail := ""
	if len(within) > 0 {
		tail += "; " + strings.Join(within, "; ")
	}
	if len(unchecked) > 0 {
		tail += "; " + strings.Join(unchecked, "; ")
	}
	if len(past) == 0 {
		p.limitsWarned = ""
		if len(within) == 0 {
			step.Outcome, step.Detail = runstate.StepSkipped, "the tracker's limits could not be checked"+tail
			return step
		}
		step.Detail = "the tracker is within its limits" + tail
		return step
	}
	step.Detail = "THE TRACKER IS PAST ITS LIMITS: " + strings.Join(past, "; ") + tail
	key := strings.Join(passed, ",")
	if key == p.limitsWarned {
		step.Detail += "; the development manager was already warned"
		return step
	}
	if p.Reports == nil {
		step.Detail += "; no report pile is wired, so the development manager could not be warned"
		return step
	}
	message := fmt.Sprintf("Maintenance found the tracker past its limits (the %s step): %s. Every role and run waits on the tracker, so a store or listings past their bounds slow the whole factory; the design on keeping the store small says what is done about it (docs/designs/tracker-store-size.md), and the latest measurement of the store on a copy says what its size is made of.",
		StepTrackerLimits, strings.Join(past, "; "))
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityWarning,
		For:      domain.RoleDevelopmentManager,
		Message:  oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)),
	}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        "tracker-limits:" + now.UTC().Format("20060102T150405"),
		Build:        p.Build,
		ProductID:    p.Product,
		RepositoryID: p.RepositoryID,
	}, now)
	if err == nil {
		err = p.Reports.Append(collected[0])
	}
	if err != nil {
		step.Detail += fmt.Sprintf("; the warning to the development manager could not be filed, and the next pass tries again: %v", err)
		return step
	}
	p.limitsWarned = key
	step.Detail += "; the development manager was warned"
	return step
}

// openOrReadyListing reports a call that listed open or ready work, which is
// what the listing bound holds: every listing but one of closed work or of
// every status.
func openOrReadyListing(record runstate.TrackerCallRecord) bool {
	switch record.Verb {
	case "ready":
		return true
	case "list":
		return !strings.HasSuffix(record.Call, " of every status") && !strings.HasSuffix(record.Call, " with status closed")
	}
	return false
}

// trackerLimits is the configured limits, and the configuration's default
// where none is wired.
func (p *Pass) trackerLimits() config.TrackerLimits {
	limits := p.TrackerLimits
	defaults := config.DefaultTrackerLimits()
	if limits.StoreMegabytes == 0 {
		limits.StoreMegabytes = defaults.StoreMegabytes
	}
	if limits.ListingSeconds == 0 {
		limits.ListingSeconds = defaults.ListingSeconds
	}
	return limits
}

// countCalls is the harness's own tracker calls over the hour before until,
// counted by caller, and nil where no log is wired.
func (p *Pass) countCalls(until time.Time) *runstate.TrackerCallCount {
	if p.Calls == nil {
		return nil
	}
	since := until.Add(-callWindow)
	records, unreadable, err := p.Calls.Read(since, until)
	if err != nil {
		return &runstate.TrackerCallCount{Since: since.UTC(), Until: until.UTC(), Failed: oneline.Fold(err.Error(), maxOutputBytes)}
	}
	count := runstate.CountTrackerCalls(records, since, until, unreadable)
	return &count
}
