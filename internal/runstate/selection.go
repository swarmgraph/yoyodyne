package runstate

// Why the harness is running this work item.
//
// While the operator started every run themselves, the answer was never worth
// recording: they typed the identifier, so they knew. Once a development manager
// pulls from the queue and work starts without anybody asking for it item by
// item, the answer stops being obvious and starts being the thing that separates
// autonomy from work happening behind somebody's back. A run nobody can account
// for looks the same whether it was chosen well or chosen by a bug.
//
// So it is recorded with the run rather than derived afterwards. It is written
// once, when the run is reserved, by the process that holds the run's lease, and
// nothing changes it: what made the harness pick this item is a fact about a
// moment, and a later reading of the queue is a different fact.
//
// It is absent from a run recorded before selections existed, and absence is
// reported as "no reason recorded" rather than rendered as a blank — an
// unaccounted run is exactly what this exists to make visible, so it must not be
// possible to mistake one for a run whose reason happened to be empty.

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// The things that choose work, named here rather than imported from the role
// vocabulary because this is durable evidence: a record written today has to
// keep meaning what it meant if the roles are ever renamed.
//
// The scheduler is not a role and is named apart from one deliberately. It pulls
// from the backlog the way a development manager does, but it is the harness
// choosing by the order and the readiness the tracker already holds rather than
// an agent weighing anything, and a record attributing its choice to a role that
// never ran would be evidence of something that did not happen.
const (
	SelectedByOperator           = "operator"
	SelectedByDevelopmentManager = "development manager"
	SelectedByScheduler          = "scheduler"
	// SelectedByBrake is the intake brake's probe: the one run the scheduler
	// starts under the brake's own hold to find out whether the line is fine.
	// It is named apart from the scheduler because it is the one harness
	// selection an intake hold lets through, and only where the hold's own
	// record names the item as its probe — see IntakeHold.Probing.
	SelectedByBrake = "intake brake"
	// SelectedByConversation is a document run: the owning role's conversation
	// supplied a document the harness confirmed under policy or the operator
	// approved, and the run publishes exactly that. No scheduler read the backlog to choose it.
	SelectedByConversation = "owning conversation"
)

// MaxSelectionReasonBytes bounds the recorded reason. It is generous enough for
// a development manager to say what it weighed and small enough that no
// selection can crowd out the run state it sits in.
const MaxSelectionReasonBytes = 4 << 10

// Selection is why this run exists: who chose the work, when, and on what
// grounds. Reason is prose meant for the operator rather than a code, because
// what makes a choice defensible is the argument for it and not a category.
type Selection struct {
	By     string    `json:"by"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
	// Lift names a change an earlier run of the item left on its branch that this
	// run starts from rather than from the target branch alone. It is set by one
	// selection only — the development manager's re-run of an item whose earlier
	// run kept its branch, whatever ended that run — and it is recorded with the
	// rest of the selection because what a run was given to start from is part of
	// why it came out the way it did.
	Lift *Lift `json:"lift,omitempty"`
	// UpdatePullRequest names the pull request this run's change replaces the head
	// of, instead of opening one of its own. It is set by one selection only: the
	// carrying out of the development manager's repair, from feedback, of an item
	// whose change waits on a pull request the harness opened.
	UpdatePullRequest *PullRequestUpdate `json:"update_pull_request,omitempty"`
	// Environments names the environments an earlier run of the item left still
	// to be checked when its change landed. It is set by one selection only: the
	// development manager's re-run of such an item, which runs the checks and
	// only those environments' tests at the target's head before any developer
	// turn (docs/designs/checking-a-run-environment.md, section 7).
	Environments *EnvironmentsRecheck `json:"environments,omitempty"`
}

// EnvironmentsRecheck is the earlier run whose change landed with environments
// still to be checked, the commit it landed at, and those environments' names.
type EnvironmentsRecheck struct {
	RunID  string   `json:"run_id"`
	Landed string   `json:"landed_commit,omitempty"`
	Names  []string `json:"names"`
}

// maxRecheckNameBytes bounds each environment name a recheck carries, which is
// a name the project declared and so far shorter than this.
const maxRecheckNameBytes = 256

// Validate refuses a recheck that names no run or no environment.
func (r EnvironmentsRecheck) Validate() error {
	var problems []error
	if !ValidRunID(strings.TrimSpace(r.RunID)) {
		problems = append(problems, fmt.Errorf("environments recheck run %q is not a run identifier", r.RunID))
	}
	if len(r.Names) == 0 {
		problems = append(problems, errors.New("an environments recheck names at least one environment"))
	}
	for _, name := range r.Names {
		if trimmed := strings.TrimSpace(name); trimmed == "" || len(trimmed) > maxRecheckNameBytes {
			problems = append(problems, fmt.Errorf("environments recheck name %q is not an environment name", name))
		}
	}
	if r.Landed != "" && !commitPattern.MatchString(r.Landed) {
		problems = append(problems, fmt.Errorf("environments recheck commit %q is not a commit", r.Landed))
	}
	return errors.Join(problems...)
}

// Rechecks reports whether name is one of the environments the recheck names.
func (r *EnvironmentsRecheck) Rechecks(name string) bool {
	if r == nil {
		return false
	}
	for _, named := range r.Names {
		if strings.TrimSpace(named) == strings.TrimSpace(name) {
			return true
		}
	}
	return false
}

// PullRequestUpdate is the pull request a run publishes onto: the earlier run
// that opened it, the request's number, the branch it carries, and the head the
// harness last put there. The run's own change is pushed to that branch, from
// exactly that head, so the same request carries it.
type PullRequestUpdate struct {
	RunID      string `json:"run_id"`
	Number     int    `json:"number"`
	Branch     string `json:"branch"`
	HeadCommit string `json:"head_commit"`
}

// Validate refuses an update that names no request, branch or head.
func (u PullRequestUpdate) Validate() error {
	var problems []error
	if !ValidRunID(strings.TrimSpace(u.RunID)) {
		problems = append(problems, fmt.Errorf("pull request update run %q is not a run identifier", u.RunID))
	}
	if u.Number <= 0 {
		problems = append(problems, errors.New("a pull request update names the request by its number"))
	}
	if branch := strings.TrimSpace(u.Branch); branch == "" || len(branch) > maxLiftBranchBytes || strings.ContainsAny(branch, " \t\r\n") {
		problems = append(problems, fmt.Errorf("pull request update branch %q is not a branch name the harness cut", u.Branch))
	}
	if !commitPattern.MatchString(u.HeadCommit) {
		problems = append(problems, fmt.Errorf("pull request update head %q is not a commit", u.HeadCommit))
	}
	return errors.Join(problems...)
}

// Lift is the preserved change a run starts from: the earlier run that made it
// and the branch it stands on. What is lifted is what the branch carries past
// where it and the target branch last agreed, applied to the fresh worktree
// uncommitted, so the fresh run's change is judged whole against the target
// exactly as any other run's is.
type Lift struct {
	RunID  string `json:"run_id"`
	Branch string `json:"branch"`
	// FallBackToTarget says the run starts from the target branch alone when the
	// change's branch has gone, rather than ending. A re-run of a run that
	// stopped is given it: re-running from the target is what such a re-run did
	// before it started from the kept change. A re-run of a raise is not, because
	// it was started to carry that change forward once its item was amended. It
	// never drops a change that conflicts with the target: that change is lifted
	// with its conflicts left for the developer to resolve.
	FallBackToTarget bool `json:"fall_back_to_target,omitempty"`
}

// MaxLiftConflicts bounds how many conflicting files a run records its lift
// left, and maxLiftConflictPathBytes how long each name may be. A lift past the
// count records the first of them; the rest are in the worktree for the
// developer to find.
const (
	MaxLiftConflicts         = 200
	maxLiftConflictPathBytes = 4096
)

// maxLiftBranchBytes bounds the branch a lift names, which is a branch the
// harness cut for an earlier run and so far shorter than this.
const maxLiftBranchBytes = 256

// Validate refuses a lift that names nothing to start from.
func (l Lift) Validate() error {
	var problems []error
	if !ValidRunID(strings.TrimSpace(l.RunID)) {
		problems = append(problems, fmt.Errorf("lift run %q is not a run identifier", l.RunID))
	}
	switch branch := strings.TrimSpace(l.Branch); {
	case branch == "":
		problems = append(problems, errors.New("a lift names the branch the preserved change stands on"))
	case len(branch) > maxLiftBranchBytes || strings.ContainsAny(branch, " \t\r\n"):
		problems = append(problems, fmt.Errorf("lift branch %q is not a branch name the harness cut", l.Branch))
	}
	return errors.Join(problems...)
}

// Validate rejects a selection that could not account for a run. Every field is
// required: a selection naming nobody, or giving no reason, records the absence
// this exists to prevent while looking like a record of something.
func (s Selection) Validate() error {
	var problems []error
	if strings.TrimSpace(s.By) == "" {
		problems = append(problems, errors.New("selection by is required"))
	}
	if strings.TrimSpace(s.Reason) == "" {
		problems = append(problems, errors.New("selection reason is required"))
	}
	if len(s.Reason) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("selection reason is %d bytes, which exceeds the %d byte bound", len(s.Reason), MaxSelectionReasonBytes))
	}
	if s.At.IsZero() {
		problems = append(problems, errors.New("selection at is required"))
	}
	if s.Lift != nil {
		if err := s.Lift.Validate(); err != nil {
			problems = append(problems, err)
		}
	}
	if s.UpdatePullRequest != nil {
		if err := s.UpdatePullRequest.Validate(); err != nil {
			problems = append(problems, err)
		}
	}
	if s.Environments != nil {
		if err := s.Environments.Validate(); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// Stated reports a selection that actually accounts for a choice. It is the
// question every caller asks before recording one, because a half-filled
// selection is the absence this exists to make visible rather than a weaker
// version of a record.
func (s Selection) Stated() bool {
	return strings.TrimSpace(s.By) != "" && strings.TrimSpace(s.Reason) != ""
}

// Stamped dates a stated selection and reports whether there was one to date. A
// caller that supplied its own moment keeps it; one that did not gets the moment
// the run was reserved, which is when the choice took effect. An unstated
// selection is reported as nothing rather than stored half-formed.
//
// A stated one is always recorded. Whether a caller said who chose the work and
// why is the whole of the question here: nothing else about a stated selection
// makes it unrecordable, because the length of its reason is folded to the bound
// rather than refused and the moment is supplied where it is missing.
func (s Selection) Stamped(at time.Time) (Selection, bool) {
	if !s.Stated() {
		return Selection{}, false
	}
	stamped := Selection{By: strings.TrimSpace(s.By), Reason: boundReason(strings.TrimSpace(s.Reason)), At: s.At, Lift: s.Lift, UpdatePullRequest: s.UpdatePullRequest, Environments: s.Environments}
	if stamped.At.IsZero() {
		stamped.At = at.UTC()
	}
	return stamped, true
}

// boundReason folds an over-length reason to what a selection may carry, and
// says in the record itself where it was cut.
//
// Truncating is the decision, over refusing the selection, and the two are not
// close. An over-length reason is the one rule of Validate that a stated
// selection can still break, and it breaks it in the record that answers why the
// run exists at all. Cut, that answer stands and is visibly incomplete: an
// operator reads the argument up to the marker and can see there was more of it.
// Refused, the run records no selection whatsoever, which is indistinguishable
// from work nobody accounted for — precisely what
// `selected-work-passes-intake-and-records-why` exists to make visible, arriving
// as silent success. A reason too long is a caller assembling prose badly; it is
// not evidence that the choice was unaccounted for, and must not be recorded as
// though it were.
//
// The fold is here rather than in each caller because every caller inherits the
// bound and none of them can see it: a reason is assembled from a fixed sentence
// and prose that arrived with an instruction, and whether the two together cross
// the bound is not knowable where they are written.
//
// It is cut on a rune boundary: a reason truncated mid-rune is not text.
func boundReason(reason string) string {
	if len(reason) <= MaxSelectionReasonBytes {
		return reason
	}
	const marker = " …truncated to the recorded bound"
	cut := MaxSelectionReasonBytes - len(marker)
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return strings.TrimRight(reason[:cut], " ") + marker
}

// SelectedByHarness reports a selection that is not the operator naming the
// work, which is what an intake hold applies to. An unstated selection counts as
// the harness choosing: unaccounted work is exactly what the hold exists to
// catch, so the uncertain case belongs on the side that is held rather than the
// side that runs.
func (s Selection) SelectedByHarness() bool {
	return strings.TrimSpace(s.By) != SelectedByOperator
}

// NamedByOperator reports a run the operator started by name, which no hold on
// new work is over. A run recorded with no selection was not.
func (s State) NamedByOperator() bool {
	return s.Selection != nil && !s.Selection.SelectedByHarness()
}

// OperatorSelection is the selection every run an operator asked for by name
// carries. The reason is supplied by whoever took the instruction, because
// "from a conversation, after turn 12" and "from a command line" are different
// accounts of the same authority and the operator can tell them apart.
func OperatorSelection(reason string, at time.Time) Selection {
	return Selection{By: SelectedByOperator, Reason: strings.TrimSpace(reason), At: at.UTC()}
}
