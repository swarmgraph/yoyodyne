package readmodel

// The factory as a whole having stopped: no work pulled and no recurring pass
// succeeding, for longer than the configured limit.
//
// From 21:51 PDT on 2026-09-29 to 09:48 PDT on 2026-09-30 no recurring pass
// completed, the watch pulled nothing, and no developer run succeeded. It was
// read at the time as every pass failing on a tracker listing that timed out;
// the machine was in fact asleep with its lid closed, and one pass spanning the
// sleep held the watch's poll
// (docs/diagnoses/yoyodyne-ifd-433-20-tracker-listing-timeouts.md). The roles
// that would have noticed were the ones not running, and the one trace was a
// line in the operator's maintenance log. The
// stall below is read from the run records and the sweep log alone — nothing a
// role writes, and nothing that asks the tracker — so it is still readable when
// the tracker is what stopped everything.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultFactoryStallAfter is the limit a reading takes where none was
// configured. It matches execution.factory_stall_after's default.
const DefaultFactoryStallAfter = 2 * time.Hour

// FactoryStall is the factory having pulled no work and completed no
// recurring pass for longer than its limit, while its passes kept being
// attempted.
type FactoryStall struct {
	// Since is the later of the last pull and the last successful pass: the
	// moment the factory last did anything, which is what the stall is measured
	// from.
	Since time.Time `json:"since"`
	// LastPull is when a developer run last started, and zero where none ever
	// has. LastPass is when the last successful recurring pass started, and
	// LastPassTask which task it was; both are zero where none ever has.
	LastPull     time.Time `json:"last_pull,omitzero"`
	LastPass     time.Time `json:"last_pass,omitzero"`
	LastPassTask string    `json:"last_pass_task,omitempty"`
	// Limit is the configured limit the stall was read against, and At when the
	// reading was taken.
	Limit time.Duration `json:"limit"`
	At    time.Time     `json:"at"`
	// Failures is what each pass attempted since Since last recorded, one per
	// task, in task order.
	Failures []PassFailure `json:"failures"`
}

// PassFailure is the latest attempt of one recurring task that did not
// succeed, and what its record says stopped it.
type PassFailure struct {
	Task    string           `json:"task"`
	Role    domain.AgentRole `json:"role,omitempty"`
	At      time.Time        `json:"at"`
	Problem string           `json:"problem"`
	// Attempts is how many of the task's attempts since the stall's start did
	// not succeed.
	Attempts int `json:"attempts"`
}

// For is how long the factory had done nothing when the reading was taken.
func (f FactoryStall) For() time.Duration {
	return f.At.Sub(f.Since)
}

// Says is the stall as one sentence: how long, the last success, and what each
// pass failed on.
func (f FactoryStall) Says() string {
	var said strings.Builder
	fmt.Fprintf(&said, "no work has been pulled and no recurring pass has succeeded for %s, past the %s limit: %s",
		f.For().Round(time.Minute), f.Limit, f.lastSuccess())
	if failures := f.failures(); failures != "" {
		said.WriteString("; each pass's latest failure: " + failures)
	}
	return said.String()
}

// lastSuccess says when work was last pulled and a pass last succeeded, each
// or both as never where the records hold none.
func (f FactoryStall) lastSuccess() string {
	pull := "no work has ever been pulled"
	if !f.LastPull.IsZero() {
		pull = "work was last pulled at " + localMoment(f.LastPull)
	}
	pass := "no recurring pass has ever succeeded"
	if !f.LastPass.IsZero() {
		pass = fmt.Sprintf("the last successful pass was %s at %s", f.LastPassTask, localMoment(f.LastPass))
	}
	return pull + ", and " + pass
}

// failures is each task's latest failure, one clause each.
func (f FactoryStall) failures() string {
	clauses := make([]string, 0, len(f.Failures))
	for _, failure := range f.Failures {
		clauses = append(clauses, fmt.Sprintf("%s failed %s since, latest at %s: %s",
			failure.Task, count(failure.Attempts, "time"), localMoment(failure.At), singleLine(failure.Problem, maxRefusalBytes)))
	}
	return strings.Join(clauses, "; ")
}

// PassSucceeded reports a recurring pass that completed: it took a turn, the
// role gave an account of it, and nothing marks it failed or missed. A pass
// with a problem beside an account still succeeded — the role answered — and
// one that took turns and gave no account did not.
func PassSucceeded(pass runstate.Sweep) bool {
	return pass.Turns > 0 && pass.Result != nil && !pass.Failed && pass.Missed == nil && pass.NotStarted == ""
}

// FactoryStallOf reads the stall from the run records and the sweep log.
//
// The factory is stalled when the later of its last pull and its last
// successful pass is further back than the limit, and at least one pass has
// been attempted since then. The attempt is what separates a factory whose
// passes are failing from one whose recurring tasks are simply switched off
// over an empty queue: the second is a rest, and a report every few hours about
// it is one nobody would keep reading. Where nothing has ever succeeded, the
// first attempt is what the stall is measured from. The operator's pause is
// never a stall: it is a stop somebody placed on purpose, and nothing fires
// under it.
func FactoryStallOf(runs []runstate.State, passes []runstate.Sweep, paused bool, limit time.Duration, now time.Time) (FactoryStall, bool) {
	if paused {
		return FactoryStall{}, false
	}
	if limit <= 0 {
		limit = DefaultFactoryStallAfter
	}
	stall := FactoryStall{Limit: limit, At: now}
	for _, run := range runs {
		if run.StartedAt.After(stall.LastPull) {
			stall.LastPull = run.StartedAt
		}
	}
	for _, pass := range passes {
		if PassSucceeded(pass) && pass.StartedAt.After(stall.LastPass) {
			stall.LastPass, stall.LastPassTask = pass.StartedAt, pass.Task
		}
	}
	stall.Since = stall.LastPull
	if stall.LastPass.After(stall.Since) {
		stall.Since = stall.LastPass
	}
	byTask := map[string]*PassFailure{}
	var earliest time.Time
	for _, pass := range passes {
		if PassSucceeded(pass) || pass.StartedAt.Before(stall.Since) {
			continue
		}
		if earliest.IsZero() || pass.StartedAt.Before(earliest) {
			earliest = pass.StartedAt
		}
		failure, seen := byTask[pass.Task]
		if !seen {
			failure = &PassFailure{Task: pass.Task}
			byTask[pass.Task] = failure
		}
		failure.Attempts++
		if !pass.StartedAt.Before(failure.At) {
			failure.Role, failure.At, failure.Problem = pass.Role, pass.StartedAt, passProblem(pass)
		}
	}
	if len(byTask) == 0 {
		return FactoryStall{}, false
	}
	if stall.Since.IsZero() {
		stall.Since = earliest
	}
	if now.Sub(stall.Since) <= limit {
		return FactoryStall{}, false
	}
	for _, failure := range byTask {
		stall.Failures = append(stall.Failures, *failure)
	}
	sort.Slice(stall.Failures, func(first, second int) bool { return stall.Failures[first].Task < stall.Failures[second].Task })
	return stall, true
}

// passProblem is what an unsuccessful pass's record says stopped it, in the
// record's own words where it has any.
func passProblem(pass runstate.Sweep) string {
	if problem := strings.TrimSpace(pass.Problem); problem != "" {
		return problem
	}
	switch {
	case pass.NotStarted != "":
		return pass.NotStarted.Describe()
	case pass.Missed != nil:
		return "the pass was missed"
	case pass.Turns > 0:
		return "the role answered without an account of the pass"
	}
	return "the pass recorded no turn and no reason"
}

// ReadFactoryStall reads the run records, the sweep log, and the operator's
// pause, and derives the stall from them. A reading missing either record says
// nothing rather than deciding over what it could not read; one that could not
// read them says so.
func ReadFactoryStall(sources Sources) (*FactoryStall, string) {
	if sources.Runs == nil || sources.Passes == nil {
		return nil, ""
	}
	runs, err := sources.Runs.Recorded()
	if err != nil {
		return nil, fmt.Sprintf("the runs could not be read to say whether the factory has stalled: %v", err)
	}
	passes, _, err := sources.Passes.List()
	if err != nil && len(passes) == 0 {
		return nil, fmt.Sprintf("the recurring passes could not be read to say whether the factory has stalled: %v", err)
	}
	paused := false
	if sources.OperatorHolds != nil {
		if _, held, err := sources.OperatorHolds.Held(); err == nil {
			paused = held
		}
	}
	stall, stalled := FactoryStallOf(runs, passes, paused, sources.FactoryStallAfter, sources.now())
	if !stalled {
		return nil, ""
	}
	return &stall, ""
}

// factoryStallAttention is the stall as the attention line carries it. It is
// the harness's move: every pass it runs is failing, which is a defect or a
// dependency of the harness's rather than anything a person configured, and
// the critical report filed when it began is what puts it in front of the
// operator and the Lead Product Manager.
func factoryStallAttention(stall FactoryStall) Attention {
	return resolved(Attention{Kind: AttentionFactoryStall, ID: stall.Since.UTC().Format(time.RFC3339), FactoryStall: &stall})
}
