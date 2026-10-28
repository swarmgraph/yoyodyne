// Package checkrun is the check runner: the one schedule that runs open
// follow-ups' checks, records what each found when that changes, and ends a
// follow-up whose check passed (docs/designs/follow-ups.md, "What ends a
// follow-up").
//
// It is the only thing that re-checks the cause of a hold. A hold whose cause
// an environment check can confirm writes a follow-up carrying that check
// (HoldFollowUp) and then waits on the follow-up; it keeps no schedule of its
// own, and a test in this package fails where anything outside it runs an
// environment check.
//
// It is driven by whoever calls Round, which is the watch session's poll: the
// runner decides which checks are due, so the poll's own interval only bounds
// how late a due check can be.
package checkrun

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/followup"
)

// DefaultInterval is how often an open follow-up's check is run, and
// DefaultCommandInterval how often an environment or command check is: those
// start a process on the machine, so they are run half as often. The design
// names configuration settings for both (follow_ups.check_interval and
// follow_ups.command_check_interval); they are not read yet, and come with the
// deadlines piece of the design, which adds the follow_ups settings.
const (
	DefaultInterval        = 5 * time.Minute
	DefaultCommandInterval = 10 * time.Minute
)

// Store is where the follow-ups are kept. It is satisfied by
// *runstate.FollowUpStore.
type Store interface {
	Add(followup.FollowUp) (followup.FollowUp, error)
	Load(id string) (followup.FollowUp, error)
	List() ([]followup.FollowUp, error)
	Record(id string, result followup.CheckResult) (followup.FollowUp, bool, error)
	Pass(id string, at time.Time, result string) (followup.FollowUp, error)
}

// Finding is what one run of a check found. A check that could not be run has
// not passed, and says why.
type Finding struct {
	Passed      bool
	CouldNotRun string
	Detail      string
}

// Evaluator runs checks of one kind.
type Evaluator interface {
	Evaluate(ctx context.Context, check followup.Check) Finding
}

// Runner runs open follow-ups' checks.
type Runner struct {
	Store Store
	// Environment runs the environment checks the harness supplies, and
	// Commands the command checks the project declares. Checks names an
	// evaluator for each other check on the closed list this runner can run. A
	// check with no evaluator could not be run, which is recorded as such and
	// never passes.
	Environment Evaluator
	Commands    Evaluator
	Checks      map[followup.CheckName]Evaluator
	// Interval and CommandInterval are DefaultInterval and
	// DefaultCommandInterval where they are not positive.
	Interval        time.Duration
	CommandInterval time.Duration
	// Tell is how whoever waits on a follow-up is told its check passed. A
	// runner without one closes the follow-up and tells nobody.
	Tell *Teller

	mu sync.Mutex
	// last is when each follow-up's check last ran in this process. A process
	// starting afresh runs every open check at its first round.
	last map[string]time.Time
}

// Round is what one round of the runner did.
type Round struct {
	// Ran counts the checks it ran.
	Ran int
	// Changed is every follow-up whose finding changed, and Passed every one it
	// ended because its check passed.
	Changed []followup.FollowUp
	Passed  []followup.FollowUp
	// Told counts the passed follow-ups whose waiting role was told.
	Told int
	// Problems is what it could not read or write, in words.
	Problems []string
}

// Attach records a new follow-up, which is how a hold attaches the check its
// cause waits on.
func (r *Runner) Attach(written followup.FollowUp) (followup.FollowUp, error) {
	return r.Store.Add(written)
}

// Load reads one follow-up.
func (r *Runner) Load(id string) (followup.FollowUp, error) {
	return r.Store.Load(id)
}

// OpenAbout is the open follow-up about a hold, where there is one.
func (r *Runner) OpenAbout(hold string) (followup.FollowUp, bool, error) {
	kept, err := r.Store.List()
	for _, standing := range kept {
		if standing.State != followup.StateOpen {
			continue
		}
		for _, held := range standing.Holds() {
			if held == hold {
				return standing, true, nil
			}
		}
	}
	return followup.FollowUp{}, false, err
}

// Round runs every open follow-up's check that is due at now, one at a time,
// records each finding that changed, and ends each follow-up whose check
// passed.
func (r *Runner) Round(ctx context.Context, now time.Time) Round {
	var round Round
	kept, err := r.Store.List()
	if err != nil {
		round.Problems = append(round.Problems, fmt.Sprintf("some follow-ups could not be read: %v", err))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[string]time.Time{}
	}
	open := map[string]bool{}
	for _, standing := range kept {
		if standing.State != followup.StateOpen {
			continue
		}
		open[standing.ID] = true
		if ctx.Err() != nil {
			break
		}
		if last, ran := r.last[standing.ID]; ran && now.Sub(last) < r.intervalFor(standing.Check) {
			continue
		}
		r.last[standing.ID] = now
		round.Ran++
		found := r.evaluate(ctx, standing.Check)
		result := followup.CheckResult{At: now, Passed: found.Passed, CouldNotRun: found.CouldNotRun, Detail: found.Detail}
		recorded, changed, err := r.Store.Record(standing.ID, result)
		if err != nil {
			round.Problems = append(round.Problems, fmt.Sprintf("what the check of follow-up %s found could not be recorded: %v", standing.ID, err))
			continue
		}
		if changed {
			round.Changed = append(round.Changed, recorded)
		}
		if !result.Passed {
			continue
		}
		passed, err := r.Store.Pass(standing.ID, now, result.Says())
		if err != nil {
			round.Problems = append(round.Problems, fmt.Sprintf("follow-up %s passed its check and could not be closed: %v", standing.ID, err))
			continue
		}
		delete(r.last, standing.ID)
		round.Passed = append(round.Passed, passed)
		if r.Tell == nil {
			continue
		}
		told, err := r.Tell.tell(passed)
		if err != nil {
			round.Problems = append(round.Problems, fmt.Sprintf("follow-up %s passed its check and %s, who waits on it, could not be told: %v", passed.ID, passed.Waits, err))
		} else if told {
			round.Told++
		}
	}
	for id := range r.last {
		if !open[id] {
			delete(r.last, id)
		}
	}
	return round
}

// intervalFor is how often a check is run.
func (r *Runner) intervalFor(check followup.Check) time.Duration {
	if check.Name == followup.CheckEnvironment || check.Name == followup.CheckCommand {
		if r.CommandInterval > 0 {
			return r.CommandInterval
		}
		return DefaultCommandInterval
	}
	if r.Interval > 0 {
		return r.Interval
	}
	return DefaultInterval
}

func (r *Runner) evaluate(ctx context.Context, check followup.Check) Finding {
	var evaluator Evaluator
	switch check.Name {
	case followup.CheckEnvironment:
		evaluator = r.Environment
	case followup.CheckCommand:
		evaluator = r.Commands
	default:
		evaluator = r.Checks[check.Name]
	}
	if evaluator == nil {
		return Finding{CouldNotRun: fmt.Sprintf("this build of the harness has no way to run the %q check yet", check.Name)}
	}
	return evaluator.Evaluate(ctx, check)
}
