package readmodel

// What the measured launches of each heavy check say its limit should be.
//
// Every launch of a heavy check is measured (runstate's heavycheckmeasure.go):
// how long it waited, how long it ran against its time limit, how it ended, and
// how many of it were running in their places as it launched. Read by how many
// were running, those say at which number the check still finishes inside its
// time limit, and that number is the limit proposed for it. It is a proposal:
// the development manager applies a limit through the reviewed configuration
// path, and nothing here changes one.
//
// The proposal reads only what was measured and never extrapolates. A check
// limited to two at once has never been seen running three at once, so the
// most it can be shown to hold at is two, and a limit above the one it was
// measured under is never proposed. A number of running checks with too few
// launches finished at it is not judged at all.

import (
	"fmt"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	// HeavyCheckJudgedLaunches is how many finished launches at one number
	// running at once it takes to judge that number.
	HeavyCheckJudgedLaunches = 5
	// HeavyCheckBudgetShare is the share of its time limit the longest launch
	// at a number may take for that number to hold: a check that already
	// takes most of its limit fails it on the first slower day.
	HeavyCheckBudgetShare = 0.75
)

// HeavyCheckLevel is every launch of one check made with the same number of it
// running at once, itself included.
type HeavyCheckLevel struct {
	Running  int `json:"running"`
	Launches int `json:"launches"`
	// Finished is the launches whose end was written down with the time
	// limit they launched with, which are the ones judged.
	Finished int `json:"finished"`
	// TimedOut is the launches stopped by their time limit.
	TimedOut int `json:"timed_out"`
	// RunMedian and RunLongest are the time the finished launches ran, and
	// LongestShare the largest share of its own time limit any of them took.
	RunMedian    time.Duration `json:"run_median"`
	RunLongest   time.Duration `json:"run_longest"`
	LongestShare float64       `json:"longest_share"`
	// Holds is whether the check finished inside its time limit at this
	// number, and Judged whether enough launches finished to say.
	Judged bool `json:"judged"`
	Holds  bool `json:"holds"`
}

// HeavyCheckLimitReading is one heavy check's measurements, read whole, and
// the limit they propose.
type HeavyCheckLimitReading struct {
	Check string `json:"check"`
	// Limit is how many may run at once now, as the last launch asked with.
	Limit    int                                `json:"limit"`
	Launches int                                `json:"launches"`
	Outcomes map[runstate.HeavyCheckOutcome]int `json:"outcomes"`
	// WaitMedian and WaitLongest are the time launches waited for a place.
	WaitMedian  time.Duration `json:"wait_median"`
	WaitLongest time.Duration `json:"wait_longest"`
	// LoadMedian is the median one-minute load the launches started under,
	// where any was read.
	LoadMedian *float64 `json:"load_median,omitempty"`
	// Outside is how many launches started while a process outside every
	// place was running a heavy check's command: a developer's own suite.
	Outside int               `json:"outside"`
	Levels  []HeavyCheckLevel `json:"levels"`
	// Proposed is the limit the measurements propose, and zero where they
	// propose none; Because says why, in a sentence.
	Proposed int    `json:"proposed,omitempty"`
	Because  string `json:"because"`
}

// HeavyCheckLoadLevel is the machine's load against how many heavy checks of
// every name held places as a launch started, that one included.
type HeavyCheckLoadLevel struct {
	Running    int      `json:"running"`
	Launches   int      `json:"launches"`
	LoadMedian *float64 `json:"load_median,omitempty"`
	// FreeMemoryMedian is the median free memory read, in bytes.
	FreeMemoryMedian *uint64 `json:"free_memory_median,omitempty"`
	// Outside is how many of these launches started beside a suite run
	// outside every place, and OutsideMost the most such processes any one
	// saw.
	Outside     int `json:"outside"`
	OutsideMost int `json:"outside_most"`
}

// HeavyCheckLimits is every heavy check's reading, by name, and the machine's
// load by how many heavy checks were running.
type HeavyCheckLimits struct {
	Checks  []HeavyCheckLimitReading `json:"checks"`
	Machine []HeavyCheckLoadLevel    `json:"machine"`
	// Unlisted is how many launches the machine's processes could not be
	// listed for, so whether a developer's own suite ran beside them is not
	// known.
	Unlisted int `json:"unlisted"`
}

// ReadHeavyCheckLimits reads the measurements into each check's proposal, the
// checks by name.
func ReadHeavyCheckLimits(measurements runstate.HeavyCheckMeasurements) HeavyCheckLimits {
	byCheck := map[string][]runstate.HeavyCheckMeasurement{}
	byRunning := map[int][]runstate.HeavyCheckMeasurement{}
	var limits HeavyCheckLimits
	for _, measurement := range measurements.Measurements {
		byCheck[measurement.Check] = append(byCheck[measurement.Check], measurement)
		total := 0
		for _, running := range measurement.Running {
			total += running
		}
		byRunning[total] = append(byRunning[total], measurement)
		if !measurement.Machine.Listed {
			limits.Unlisted++
		}
	}
	names := make([]string, 0, len(byCheck))
	for name := range byCheck {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		limits.Checks = append(limits.Checks, readHeavyCheck(name, byCheck[name]))
	}
	totals := make([]int, 0, len(byRunning))
	for total := range byRunning {
		totals = append(totals, total)
	}
	sort.Ints(totals)
	for _, total := range totals {
		limits.Machine = append(limits.Machine, readLoadLevel(total, byRunning[total]))
	}
	return limits
}

func readHeavyCheck(name string, launches []runstate.HeavyCheckMeasurement) HeavyCheckLimitReading {
	reading := HeavyCheckLimitReading{Check: name, Launches: len(launches), Outcomes: map[runstate.HeavyCheckOutcome]int{}}
	var waits []time.Duration
	var loads []float64
	byRunning := map[int][]runstate.HeavyCheckMeasurement{}
	for _, launch := range launches {
		reading.Limit = launch.Limit
		if launch.ExitedAt != nil {
			reading.Outcomes[launch.Outcome]++
		}
		waits = append(waits, launch.Waited())
		if launch.Machine.Load != nil {
			loads = append(loads, *launch.Machine.Load)
		}
		if launch.Outside > 0 {
			reading.Outside++
		}
		running := launch.Running[name]
		if running < 1 {
			running = 1
		}
		byRunning[running] = append(byRunning[running], launch)
	}
	reading.WaitMedian, reading.WaitLongest = medianDuration(waits), longestDuration(waits)
	if len(loads) > 0 {
		median := medianFloat(loads)
		reading.LoadMedian = &median
	}
	numbers := make([]int, 0, len(byRunning))
	for number := range byRunning {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	for _, number := range numbers {
		reading.Levels = append(reading.Levels, readLevel(number, byRunning[number]))
	}
	reading.Proposed, reading.Because = proposeHeavyCheckLimit(reading)
	return reading
}

func readLevel(running int, launches []runstate.HeavyCheckMeasurement) HeavyCheckLevel {
	level := HeavyCheckLevel{Running: running, Launches: len(launches)}
	var runs []time.Duration
	for _, launch := range launches {
		ran, ended := launch.Ran()
		if !ended || launch.Budget() <= 0 {
			continue
		}
		level.Finished++
		if launch.Outcome == runstate.HeavyCheckTimedOut {
			level.TimedOut++
		}
		runs = append(runs, ran)
		if share := float64(ran) / float64(launch.Budget()); share > level.LongestShare {
			level.LongestShare = share
		}
	}
	level.RunMedian, level.RunLongest = medianDuration(runs), longestDuration(runs)
	// A launch stopped by its time limit fails the number it ran at however
	// few others there are; holding it takes enough launches to judge.
	level.Judged = level.TimedOut > 0 || level.Finished >= HeavyCheckJudgedLaunches
	level.Holds = level.Judged && level.TimedOut == 0 && level.LongestShare <= HeavyCheckBudgetShare
	return level
}

// proposeHeavyCheckLimit is the largest number running at once, no higher than
// the limit measured under, at which every judged number up to it held.
func proposeHeavyCheckLimit(reading HeavyCheckLimitReading) (int, string) {
	for _, level := range reading.Levels {
		if !level.Judged || level.Holds {
			continue
		}
		proposed := level.Running - 1
		if proposed < 1 {
			proposed = 1
		}
		why := fmt.Sprintf("the longest of %d launches with %d running at once took %.0f%% of its time limit, more than the %.0f%% that leaves room for a slower day",
			level.Finished, level.Running, level.LongestShare*100, HeavyCheckBudgetShare*100)
		if level.TimedOut > 0 {
			why = fmt.Sprintf("%d of %d launches with %d running at once were stopped by their time limit", level.TimedOut, level.Finished, level.Running)
		}
		if level.Running == 1 {
			return proposed, why + "; fewer at once cannot help, so its time limit or the suite itself is what has to change"
		}
		return proposed, why
	}
	limit := reading.Limit
	if limit < 1 {
		limit = 1
	}
	for _, level := range reading.Levels {
		if level.Running != limit {
			continue
		}
		if !level.Judged {
			return 0, fmt.Sprintf("only %d launches finished with %d running at once, and it takes %d to judge whether the limit of %d holds", level.Finished, limit, HeavyCheckJudgedLaunches, limit)
		}
		return limit, fmt.Sprintf("every launch finished inside %.0f%% of its time limit with up to %d running at once; no more than %d has ever run at once, so whether a higher limit would hold is not measured", HeavyCheckBudgetShare*100, limit, limit)
	}
	return 0, fmt.Sprintf("no launch has started with %d running at once yet, so whether the limit of %d holds is not measured", limit, limit)
}

func readLoadLevel(running int, launches []runstate.HeavyCheckMeasurement) HeavyCheckLoadLevel {
	level := HeavyCheckLoadLevel{Running: running, Launches: len(launches)}
	var loads []float64
	var free []uint64
	for _, launch := range launches {
		if launch.Machine.Load != nil {
			loads = append(loads, *launch.Machine.Load)
		}
		if launch.Machine.FreeMemory != nil {
			free = append(free, *launch.Machine.FreeMemory)
		}
		if launch.Outside > 0 {
			level.Outside++
		}
		if launch.Outside > level.OutsideMost {
			level.OutsideMost = launch.Outside
		}
	}
	if len(loads) > 0 {
		median := medianFloat(loads)
		level.LoadMedian = &median
	}
	if len(free) > 0 {
		sort.Slice(free, func(first, second int) bool { return free[first] < free[second] })
		median := free[len(free)/2]
		level.FreeMemoryMedian = &median
	}
	return level
}

func medianDuration(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(first, second int) bool { return sorted[first] < sorted[second] })
	return sorted[len(sorted)/2]
}

func longestDuration(values []time.Duration) time.Duration {
	var longest time.Duration
	for _, value := range values {
		if value > longest {
			longest = value
		}
	}
	return longest
}

func medianFloat(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}
