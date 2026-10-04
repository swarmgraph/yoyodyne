package runstate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// StateSchemaVersion stays 1 because every addition since has been an optional
// key. A state file written before them still decodes, which is the bar every
// schema change has to clear: `review_findings` keeps the meaning and the
// integer type it was written with, the structured findings the repair loop
// needs live under their own key beside it, as does the failing check that
// triggers the same loop, and the published pull request is absent entirely
// from a run that never published one — as is the queued merge inside it, whose
// absence means what it meant before queued merges existed: no merge is waiting
// on the forge. The recorded account of what a run changed is the same kind of
// addition: a state file written before it decodes unchanged, and its absence
// means what it always meant, which is that nothing summarized the change. The
// count of retried promotions is the same again: absent means no promotion of
// this run was ever re-prepared, which is what every run written before the
// retry existed did. The directive a run paused for is the same again: absent
// means no user directive ever held this run up, which is what every run
// written before directives were enforced meant. The operator hold a run parked
// on is the last of them: absent means the operator never held this run, which
// is what every run written before the hold existed meant. Why a run was
// selected is the newest of them and behaves identically: absent means nothing
// accounted for the choice, which is what every run written before selections
// were recorded meant. The protected paths a change was refused for is the
// newest of them and behaves the same way: absent means no such refusal was
// ever recorded against this run, which is what every run written before the
// gate existed meant. The review rounds a run accumulated and the blocker it
// stopped on are the two newest, and both behave identically: absent means
// nothing counted the rounds and nothing blocked this run, which is what every
// run written before triage docketed anything meant. What the work item was
// called is the newest of them and behaves the same way: absent means nothing
// recorded a title for it, which is what every run written before a surface
// needed to name the work in words meant. The account the run ran under and the
// configuration revision in force are the two newest, and they behave the same
// way: absent means nothing recorded which account or which configuration, which
// is what every run written before either was carried meant. The grants of
// further repair attempts triage has continued a stopped run on are the newest
// and behave identically: absent means nothing ever continued this run, which is
// what every run written before triage could meant, and the configured budget is
// then the whole of what bounded its repairs. What the work item's notes lost to
// the context budget is the newest of them and behaves the same way: absent
// means the item was delivered whole, which is what every run written before the
// notes were ever truncated meant. The replay a moved target refused, handed
// back to the developer to reconcile, is newer still and behaves the same way:
// absent means no replay of this run was ever refused, which is what every run
// written before a conflict went back to its author meant.
const StateSchemaVersion = 1

// The shape of the three things a run records about how it was configured and
// what was executing it. They are stated here rather than imported from the
// configuration package for the reason the review decisions above are: the
// durable schema stays independent of the code that produces what it stores, so
// a record is checked against what a record may hold rather than against what
// this version of the harness happens to write.
//
// buildPattern is shared by every record in this package that pins a harness
// build, and it holds one to being a Git object name because that is the only
// thing it ever is: a reader measures how old a build is by handing it to Git,
// and a field that could carry anything is a field that could carry an option.
var (
	accountAliasPattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	configRevisionPattern = regexp.MustCompile(`^cfg-[a-f0-9]{8,}$`)
	buildPattern          = regexp.MustCompile(`^[a-f0-9]{7,64}$`)
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusTimedOut  Status = "timed_out"
)

// Phase names the step a run reached. It is recorded alongside the status so a
// terminal run says where it stopped and an interrupted run says what was in
// flight, which is what reconciliation and diagnosis both need.
type Phase string

const (
	PhaseDeveloping  Phase = "developing"
	PhaseChecking    Phase = "checking"
	PhaseReviewing   Phase = "reviewing"
	PhaseIntegrating Phase = "integrating"
	PhaseCompleting  Phase = "completing"
	PhaseCleaningUp  Phase = "cleaning_up"
	PhaseComplete    Phase = "complete"
)

// StallResumeStep reads a docket entry's resumes_at back as the phase it names,
// and refuses anything but the two steps a stall is continued at once its
// developer attempt is complete: the checks and the review. The docket carries
// it as a plain string because the triage package sits beneath this one, so this
// is the one conversion between the two spellings.
func StallResumeStep(step string) (Phase, bool) {
	switch phase := Phase(step); phase {
	case PhaseChecking, PhaseReviewing:
		return phase, true
	default:
		return "", false
	}
}

// Review decisions and finding severities are duplicated here rather than
// imported so the durable schema stays independent of the review implementation
// that produces them.
const (
	ReviewApprove = "approve"
	ReviewRepair  = "repair"
	// ReviewEscalate is the reviewer having said the work item cannot be met as it
	// stands. It approves nothing and asks for nothing, so it neither integrates
	// nor sends the change back: what it produces is a decision for the
	// development manager, and this is the record that says one was raised.
	ReviewEscalate = "escalate"
)

// What an approval approves is duplicated here for the same reason, and it is
// the review vocabulary that decides most: an approval of evidence closes no
// work item, so a record that could not carry the word would settle the item as
// though the reviewer had said the other thing.
const (
	ApprovesImplementation = "implementation"
	ApprovesEvidence       = "evidence"
)

const (
	SeverityBlocker = "blocker"
	SeverityMajor   = "major"
	SeverityMinor   = "minor"
)

// A finding's disposition is duplicated here for the reason its severity is, and
// it decides more than the severity does: whether the repair that carried it
// cost the work item a review round. A record that could not carry it would be
// refused at the save of a verdict the harness had already charged or not
// charged by it.
const (
	DispositionOutOfScope = "out_of_scope"
)

// Landing outcomes are duplicated here for the reason the review vocabularies
// are: the durable schema stays independent of the package that produces them.
// What a landing outcome decides is whether the work item closes, so an
// unrecognized one is refused at the save rather than guessed at by the closure.
const (
	LandingDischarged = "discharged"
	LandingEvidence   = "evidence"
	// LandingEscalate is the developer's half of the same verb the reviewer has
	// above: the item cannot be met as it stands, and what the run produced is a
	// decision for the development manager rather than a change.
	LandingEscalate = "escalate"
)

// What a developer recorded of its own executions is duplicated here for the
// reason the landing outcomes are. What it decides is whether a change may be
// handed to a reviewer at all, and whether the run ends on its environment, so
// an unrecognized outcome is refused at the save rather than read as something
// the gate then acts on.
const (
	VerificationPassed = "passed"
	// VerificationFailed is a command that ran and exited non-zero, and
	// VerificationRefused one that never started. They are separate words because
	// the harness answers them oppositely — one is a change or a base commit to
	// repair, the other is a run to end — and a record that collapsed them would
	// file every red baseline as a broken sandbox.
	VerificationFailed  = "failed"
	VerificationRefused = "refused"
)

// The vocabularies above stated as lists, which is what the validation below
// reads. None of them is repeated in a switch anywhere in this package, so a list
// and what a record may carry cannot come to disagree.
//
// Every list is closed and stays closed. A stored value nothing recognizes
// is worse than a refused one here: what reads these fields ranks a severity to
// order a listing, and builds the repair prompt a developer is handed back from
// them, and neither has an answer for a word it has never seen. Opening them —
// the tolerant reader, which is right where a record arrives from somewhere this
// code does not control — would move the refusal out of the save and into those
// readers, where it is silent.
//
// What closing them costs is the trap they once sprang: the reviewer's
// vocabulary grew, the durable one did not, and the addition was refused at save
// time in a process that had already reported the verdict to the tracker and the
// operator. That price is now paid where it can be seen instead.
// TestTheDurableSchemaStoresEveryVerdictTheReviewerCanProduce holds the review
// package's vocabularies to being subsets of these, so an addition there fails a
// check rather than somebody's run.
var (
	reviewDecisions   = []string{ReviewApprove, ReviewRepair, ReviewEscalate}
	reviewApprovals   = []string{ApprovesImplementation, ApprovesEvidence}
	findingSeverities = []string{SeverityBlocker, SeverityMajor, SeverityMinor}
	// findingDispositions does not list the empty disposition, which every
	// finding may carry and most do: it is the ordinary finding, one this change
	// has to act on.
	findingDispositions = []string{DispositionOutOfScope}
	landingOutcomes     = []string{LandingDischarged, LandingEvidence, LandingEscalate}

	verificationOutcomes = []string{VerificationPassed, VerificationFailed, VerificationRefused}
)

// ReviewDecisions and FindingSeverities are those vocabularies as a caller
// outside this package reads them. Each answers with a copy, because a
// package-level slice is a vocabulary anybody holding it could rewrite.
func ReviewDecisions() []string { return slices.Clone(reviewDecisions) }

func FindingSeverities() []string { return slices.Clone(findingSeverities) }

// FindingDispositions is what a finding may say beside its severity, read the
// same way and closed for the same reason. The empty disposition is always
// permitted and is not in the list.
func FindingDispositions() []string { return slices.Clone(findingDispositions) }

// ReviewApprovals is what an approval may say it approves, read the same way and
// closed for the same reason.
func ReviewApprovals() []string { return slices.Clone(reviewApprovals) }

// LandingOutcomes is the landing vocabulary the durable schema stores, read the
// same way and closed for the same reason.
func LandingOutcomes() []string { return slices.Clone(landingOutcomes) }

// VerificationOutcomes is how a recorded execution may say it ended, read the
// same way and closed for the same reason.
func VerificationOutcomes() []string { return slices.Clone(verificationOutcomes) }

// quotedAlternatives names a vocabulary the way a refusal has to: every value
// quoted, the last joined with "or". It is derived from the list rather than
// written out beside it so a value added to one is named by the other.
func quotedAlternatives(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	switch len(quoted) {
	case 0:
		return "nothing"
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " or " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
	}
}

// The two ways the harness itself stops a provider invocation on time. They are
// recorded apart because they describe opposite things: a stalled invocation
// stopped emitting events and was doing nothing, while one that exhausted its
// budget was still emitting them and simply ran out of run. Neither is the
// provider reporting a failure, and neither is stored as one.
const (
	ProviderStopStalled         = "stalled"
	ProviderStopBudgetExhausted = "budget_exhausted"
)

// The two ways a provider refuses an attempt without judging the work, and so
// the two things a paused run can be waiting on. They share one deadline, one
// budget, and one polling discipline; what differs is the words an operator
// reads and the clock the wait is set by, and neither can be recovered from a
// deadline on its own. The empty cause reads as an exhausted usage limit, so a
// record written before an overload was waitable still describes itself.
const (
	PauseUsageLimit     = "usage_limit"
	PauseServerOverload = "server_overload"
)

// The two ways a provider answers nobody at all, and so the two waits a run
// takes that spend nothing: no relaunch, no repair attempt, no pause budget, and
// no blocker. They share the usage-limit pause's deadline and polling
// discipline — the deadline is the next probe rather than a reset the provider
// named — and none of its budgets, because every budget the harness keeps is
// for something a run can do something about, and a login and a network are
// not. The cause carries which of the two it is because that is what decides
// what the operator is told to do.
const (
	PauseProviderUnauthenticated = "provider_unauthenticated"
	PauseProviderUnreachable     = "provider_unreachable"
)

// PauseCauseForOutage is the pause a run takes on a provider outage of the
// given cause. It is the one conversion between the domain's vocabulary and the
// run record's, kept beside the causes it names.
func PauseCauseForOutage(cause domain.ProviderOutageCause) string {
	if cause == domain.ProviderUnreachable {
		return PauseProviderUnreachable
	}
	return PauseProviderUnauthenticated
}

// PausedForProviderOutage reports a pause cause that is one of the two above,
// and which. A run in one of them is waiting on the operator or the network
// rather than on a clock, which is what every reader of the cause has to know
// before it prices the wait or says what lifts it.
func PausedForProviderOutage(cause string) (domain.ProviderOutageCause, bool) {
	switch cause {
	case PauseProviderUnauthenticated:
		return domain.ProviderUnauthenticated, true
	case PauseProviderUnreachable:
		return domain.ProviderUnreachable, true
	default:
		return "", false
	}
}

// DescribePause names what a paused run is waiting on, as the object of "paused
// for" or "waiting out". kind is the provider's own name for an exhausted usage
// limit and says nothing about any other cause.
func DescribePause(cause, kind string) string {
	if cause == PauseOperatorHold {
		return "an operator hold on all harness activity"
	}
	if cause == PauseServerOverload {
		return "a transient provider server overload"
	}
	if outage, away := PausedForProviderOutage(cause); away {
		return DescribeProviderOutage(outage)
	}
	if strings.TrimSpace(kind) == "" {
		kind = "provider"
	}
	return "an exhausted " + kind + " usage limit"
}

// MaxCheckOutputBytes bounds the captured output a failing check may carry into
// durable state and into the developer's next attempt. A verbose suite must not
// be able to fill either with output that is mostly unrelated to the failure.
const MaxCheckOutputBytes = 8 << 10

// CheckFailure is the local or forge check a repair attempt was handed back. It
// is durable for the same reason the findings are: an attempt interrupted
// before it ran has to be reissued with exactly the input it was given, and a
// run that spends its attempts has to name what still fails. The output is the
// bounded capture rather than everything the check printed.
type CheckFailure struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output,omitempty"`
	// ForgeHeadCommit names the revision a forge check failed on. The forge
	// reports a conclusion rather than a process exit code, so ExitCode is not
	// used for this input.
	ForgeHeadCommit string `json:"forge_head_commit,omitempty"`
	// LocalPromotion keeps the history that authorized cleanup when the failed
	// forge revision was already promoted locally. It is not current integration
	// or approval credit: a repair must pass the gates again.
	LocalPromotion *Integration `json:"local_promotion,omitempty"`
}

// Validate reports every contract violation in the recorded check at once.
func (c CheckFailure) Validate() error {
	var problems []error
	if strings.TrimSpace(c.Command) == "" {
		problems = append(problems, errors.New("command is required"))
	}
	if c.ForgeHeadCommit != "" && !commitPattern.MatchString(c.ForgeHeadCommit) {
		problems = append(problems, errors.New("forge_head_commit must be a full commit id"))
	}
	if c.LocalPromotion != nil {
		if err := c.LocalPromotion.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("local_promotion: %w", err))
		}
		if c.LocalPromotion.ThroughPullRequest || c.ForgeHeadCommit == "" || c.LocalPromotion.SourceCommit != c.ForgeHeadCommit {
			problems = append(problems, errors.New("local_promotion must be the local promotion of the failed forge revision"))
		}
	}
	if len(c.Output) > MaxCheckOutputBytes {
		problems = append(problems, fmt.Errorf("output is %d bytes, which exceeds the %d byte bound", len(c.Output), MaxCheckOutputBytes))
	}
	return errors.Join(problems...)
}

// The bounds a recorded execution is held to. They are this package's own
// rather than the reading package's for the reason the vocabulary above is:
// what a record may hold is decided by the schema that stores it.
const (
	MaxVerificationCommandBytes = 512
	MaxVerificationDetailBytes  = 2 << 10
	// MaxVerificationChecks bounds how many executions one record carries. A
	// developer that ran twenty commands against its change ran enough of them,
	// and a longer list is padding — which is the failure this gate has to avoid
	// teaching, so the record cannot grow without bound as a way of looking
	// thorough.
	MaxVerificationChecks = 20
	// MaxVerificationOwed bounds what one record is told it still owes. The list
	// is written by the harness rather than by an agent, so this is a guard on a
	// record growing rather than on anything untrusted.
	MaxVerificationOwed = 8
)

// VerificationExecution is one command a developer recorded running, and how it
// ended.
type VerificationExecution struct {
	Command string `json:"command"`
	Outcome string `json:"outcome"`
	// Detail is what refused or what broke, on an execution that failed.
	Detail string `json:"detail,omitempty"`
}

// Validate reports every contract violation in one recorded execution at once.
func (v VerificationExecution) Validate() error {
	var problems []error
	switch trimmed := strings.TrimSpace(v.Command); {
	case trimmed == "":
		problems = append(problems, errors.New("command is required"))
	case len(trimmed) > MaxVerificationCommandBytes:
		problems = append(problems, fmt.Errorf("command is %d bytes, which exceeds the %d byte bound", len(trimmed), MaxVerificationCommandBytes))
	}
	if !slices.Contains(verificationOutcomes, v.Outcome) {
		problems = append(problems, fmt.Errorf("outcome %q is invalid, want %s", v.Outcome, quotedAlternatives(verificationOutcomes)))
	}
	if len(v.Detail) > MaxVerificationDetailBytes {
		problems = append(problems, fmt.Errorf("detail is %d bytes, which exceeds the %d byte bound", len(v.Detail), MaxVerificationDetailBytes))
	}
	return errors.Join(problems...)
}

// CheckStage is the deterministic check stage as the record last saw it: when
// it began, the bound it runs under, which check it is on, and — once it has
// ended — what it spent. It is durable so that a surface reading the run while
// its checks run can say how much of the bound has gone rather than only how
// long the run has been going, which on 2026-09-19 was the difference between a
// two-hour check stage being visible and being discovered by looking.
//
// It describes the current attempt's stage. Every attempt runs the checks
// again, so it is written afresh as each stage starts and the previous
// attempt's stage is not kept beside it.
type CheckStage struct {
	StartedAt time.Time `json:"started_at"`
	// BoundSeconds is the bound in force for the stage, in seconds for the
	// reason every other span on the record is: execution.check_stage_timeout
	// scaled for the machine's load the way a local Git command's budget is.
	BoundSeconds int64 `json:"bound_seconds"`
	// ConfiguredSeconds is execution.check_stage_timeout as configured, before
	// the load scaled it, and Load and Cores are the reading that scaled it: the
	// largest one-minute load average seen as a check began, and the cores it was
	// read against. A stage whose load could not be read records no Cores, and
	// a record written before the bound scaled records none of the three.
	ConfiguredSeconds int64   `json:"configured_seconds,omitempty"`
	Load              float64 `json:"load,omitempty"`
	Cores             int     `json:"cores,omitempty"`
	// Command is the check the stage is on, or the last one it ran.
	Command string `json:"command,omitempty"`
	// FinishedAt and ElapsedSeconds are written when the stage ends, however it
	// ends. Absent, the stage is still running. The spend carries no `omitempty`
	// because a stage that ended inside its first second is a stage that
	// ended, and a record that dropped the figure would read as one still
	// running.
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	ElapsedSeconds int64      `json:"elapsed_seconds"`
	// Narrowed is what the gate was told the change touches, in the words the
	// checks were given it in, so a run over a change to one package can be
	// read afterwards as having been narrowed to it. Where the change touched
	// what a configured path check vouches for, it also says which check the
	// gate added and which path added it.
	Narrowed string `json:"narrowed,omitempty"`
	// StoppedAtBound reports a stage that ended because it reached its bound,
	// with Command naming the check it stopped.
	StoppedAtBound bool `json:"stopped_at_bound,omitempty"`
	// Interrupted reports a stage the sweep closed because the process running
	// it died, with Command naming the check it was on. It is written by the
	// settlement rather than by the run, because a run that died wrote nothing;
	// without it a blocked run read as one still in its checks, with a spend
	// that grew for as long as the record stood.
	Interrupted bool `json:"interrupted,omitempty"`
}

// CloseInterrupted ends a stage the process running it never ended: the sweep
// settling the run calls it, so the record says the checks were interrupted
// rather than still running. A stage already ended is left as it is.
func (c *CheckStage) CloseInterrupted(now time.Time) {
	if c == nil || !c.Running() {
		return
	}
	finished := now
	c.FinishedAt = &finished
	c.ElapsedSeconds = int64(c.SpentBy(now) / time.Second)
	c.Interrupted = true
}

// Validate reports every contract violation in the recorded stage at once.
func (c CheckStage) Validate() error {
	var problems []error
	if c.StartedAt.IsZero() {
		problems = append(problems, errors.New("started_at is required"))
	}
	if c.BoundSeconds <= 0 {
		problems = append(problems, errors.New("bound_seconds must be positive"))
	}
	if c.ElapsedSeconds < 0 {
		problems = append(problems, errors.New("elapsed_seconds cannot be negative"))
	}
	if c.ConfiguredSeconds < 0 || (c.ConfiguredSeconds > 0 && c.ConfiguredSeconds > c.BoundSeconds) {
		problems = append(problems, errors.New("configured_seconds must be positive and no more than the bound the load scaled it to"))
	}
	if c.Load < 0 || c.Cores < 0 {
		problems = append(problems, errors.New("load and cores cannot be negative"))
	}
	if c.FinishedAt != nil && c.FinishedAt.Before(c.StartedAt) {
		problems = append(problems, errors.New("finished_at cannot precede started_at"))
	}
	return errors.Join(problems...)
}

// Passed reports an execution that ran and succeeded.
func (v VerificationExecution) Passed() bool { return v.Outcome == VerificationPassed }

// Started reports an execution that ran, whichever way it then went. It is what
// the probe answers, and it is deliberately not Passed: a suite that ran and
// failed has proved the environment works.
func (v VerificationExecution) Started() bool { return v.Outcome != VerificationRefused }

// Verification is what the developer recorded executing: the probe it ran before
// it changed anything, the checks it ran against the change, and — where the
// record did not meet the bar — what it still owes.
//
// It is durable because three different readers need it after the process that
// took it is gone. A repair attempt interrupted before it ran has to be reissued
// with exactly the input it was given; the reviewer is shown what the developer
// executed beside the change it is judging; and a run that stops for want of a
// record has to be able to say so on the work item, where the whole point is
// that nobody has to take the run's word for what was run.
type Verification struct {
	// Probe is the execution made before anything was changed, and is absent on a
	// record that never made one.
	Probe *VerificationExecution `json:"probe,omitempty"`
	// Checks are the executions made against the change itself.
	Checks []VerificationExecution `json:"checks,omitempty"`
	// Owed is what the record still lacks, in the harness's own words. A record
	// that meets the bar owes nothing, and this is empty.
	Owed []string `json:"owed,omitempty"`
	// Problem is a block the harness could not read, recorded rather than
	// discarded: a developer that wrote one was trying to say what it ran, and
	// the difference between an unreadable record and no record at all is what
	// whoever reads this afterwards needs.
	Problem string `json:"problem,omitempty"`
}

// Met reports a record that satisfies the bar: something was recorded, and
// nothing is owed.
func (v Verification) Met() bool {
	return v.Probe != nil && len(v.Owed) == 0 && strings.TrimSpace(v.Problem) == ""
}

// Validate reports every contract violation in the recorded verification at once.
func (v Verification) Validate() error {
	var problems []error
	if v.Probe != nil {
		if err := v.Probe.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("probe: %w", err))
		}
	}
	if len(v.Checks) > MaxVerificationChecks {
		problems = append(problems, fmt.Errorf("%d checks are recorded, which exceeds the bound of %d", len(v.Checks), MaxVerificationChecks))
	}
	for index, check := range v.Checks {
		if err := check.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("checks[%d]: %w", index, err))
		}
	}
	if len(v.Owed) > MaxVerificationOwed {
		problems = append(problems, fmt.Errorf("%d owed entries are recorded, which exceeds the bound of %d", len(v.Owed), MaxVerificationOwed))
	}
	if len(v.Problem) > MaxChannelProblemBytes {
		problems = append(problems, fmt.Errorf("problem is %d bytes, which exceeds the %d byte bound", len(v.Problem), MaxChannelProblemBytes))
	}
	return errors.Join(problems...)
}

// Bound is the stage's bound as a span.
func (c CheckStage) Bound() time.Duration {
	return time.Duration(c.BoundSeconds) * time.Second
}

// Configured is execution.check_stage_timeout as configured, and the bound
// itself on a record that predates the load scaling it.
func (c CheckStage) Configured() time.Duration {
	if c.ConfiguredSeconds <= 0 {
		return c.Bound()
	}
	return time.Duration(c.ConfiguredSeconds) * time.Second
}

// Scaled reports a bound the machine's load raised above the configured figure.
func (c CheckStage) Scaled() bool {
	return c.Bound() > c.Configured()
}

// LoadSays names the load reading the bound was scaled by, in the words every
// surface uses for it, and is empty where the load could not be read.
func (c CheckStage) LoadSays() string {
	if c.Cores <= 0 {
		return ""
	}
	return fmt.Sprintf("a one-minute load average of %.1f on %d cores", c.Load, c.Cores)
}

// BoundSays says the bound in force beside the configured figure: the bound
// alone where the load did not raise it, and where it did, the configured
// figure and the load that scaled it.
func (c CheckStage) BoundSays() string {
	if !c.Scaled() {
		return describeSpan(c.Bound())
	}
	return fmt.Sprintf("%s (%s configured, scaled for %s)", describeSpan(c.Bound()), describeSpan(c.Configured()), c.LoadSays())
}

// Running reports a stage that has started and not ended.
func (c CheckStage) Running() bool {
	return c.FinishedAt == nil
}

// Elapsed is what the stage spent, as recorded when it ended.
func (c CheckStage) Elapsed() time.Duration {
	return time.Duration(c.ElapsedSeconds) * time.Second
}

// SpentBy is what the stage has spent as of a moment: the recorded spend once
// it has ended, and the time since it began while it runs.
func (c CheckStage) SpentBy(now time.Time) time.Duration {
	if c.FinishedAt != nil {
		return time.Duration(c.ElapsedSeconds) * time.Second
	}
	if spent := now.Sub(c.StartedAt); spent > 0 {
		return spent
	}
	return 0
}

// Describe says where the stage stands, in the words every surface uses for
// it: what it has spent of its bound, and which check it is on.
func (c CheckStage) Describe(now time.Time) string {
	said := fmt.Sprintf("checks: %s of %s", describeSpan(c.SpentBy(now)), c.BoundSays())
	if c.Command != "" {
		switch {
		case c.StoppedAtBound:
			said += ", stopped at the bound during " + c.Command
		case c.Interrupted:
			said += ", interrupted during " + c.Command
		case c.Running():
			said += ", on " + c.Command
		}
	}
	return said
}

// describeSpan says a span the way an operator reads one: whole minutes once
// it is minutes, and seconds under that.
func describeSpan(span time.Duration) string {
	if span < time.Minute {
		return fmt.Sprintf("%ds", int(span.Seconds()))
	}
	return fmt.Sprintf("%dm", int(span.Minutes()))
}

// LandingChecks is what the landing checks made of the commit a run integrated: the
// commit, each check with its result, whether the landing was green, and the
// work item a red landing filed. It is on the run rather than in a record of
// its own because a landing is a fact about the change that run landed, and
// the run's record is what every surface already reads for it.
//
// It is written after the run is terminal and never changes what the run
// recorded about itself: the run succeeded, its item closed, and a red landing
// is news about the target branch rather than a verdict on the attempt.
type LandingChecks struct {
	Commit    string    `json:"commit"`
	StartedAt time.Time `json:"started_at"`
	// FinishedAt is absent while the landing checks run.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// TargetBranch is the branch the commit landed on, which is what landings
	// queue on: at most one landing per target branch runs its checks at a time.
	TargetBranch string `json:"target_branch,omitempty"`
	// WaitingSince is when the landing found another landing on its branch
	// running and began waiting its turn, and AdmittedAt when that landing let
	// it in. Both are absent on a landing that never waited. A landing with
	// WaitingSince and no AdmittedAt is waiting — or, once finished, waited out
	// its bound or died waiting — and has run no check and cut no checkout.
	WaitingSince *time.Time `json:"waiting_since,omitempty"`
	AdmittedAt   *time.Time `json:"admitted_at,omitempty"`
	// BoundSeconds is the budget each landing check ran under —
	// execution.landing_check_timeout — which is its own rather than the
	// gate's: the suite moved to the landing is the one too long for the gate's
	// stage bound, so a landing bounded the same way would stop every time.
	// The landing has no stage bound; its checks together may take the sum.
	BoundSeconds int64                `json:"bound_seconds"`
	Checks       []LandingCheckResult `json:"checks,omitempty"`
	// Ran reports the checks ran to a verdict of their own — every one of them
	// passed or failed on its own exit — and Green that every one of them
	// passed. Both are meaningful once FinishedAt is set. A landing whose checks
	// could not run, or were stopped before they finished — at a budget, by a
	// cancelled process, by a process that died — is neither green nor red: a
	// stopped check judged nothing, so the landing is unverified, and Problem
	// says why.
	Ran   bool `json:"ran,omitempty"`
	Green bool `json:"green,omitempty"`
	// FiledWorkItem is the item a red landing filed, and FilingProblem is why
	// none could be, so a red landing whose item the tracker refused is read as
	// exactly that rather than as one nobody filed for. FiledEarlier reports
	// that the item was filed by an earlier landing of the same check on the
	// same branch and this landing was noted on it rather than filed again.
	FiledWorkItem string `json:"filed_work_item,omitempty"`
	FiledEarlier  bool   `json:"filed_earlier,omitempty"`
	FilingProblem string `json:"filing_problem,omitempty"`
	// Problem is what went wrong around the checks rather than in them — no
	// checkout could be cut, they could not be run, the checkout would not go
	// away, the item would not take the note — which is a different fact from a
	// red landing and is said beside whichever result there is.
	Problem string `json:"problem,omitempty"`
}

// LandingCheckResult is one landing check's result, in the same figures the gate's
// checks record.
type LandingCheckResult struct {
	Command        string `json:"command"`
	Passed         bool   `json:"passed"`
	ExitCode       int    `json:"exit_code"`
	ElapsedSeconds int64  `json:"elapsed_seconds"`
	// StoppedAtBound reports a check stopped at its budget rather than one that
	// ran to its own exit, which makes the landing unverified rather than red.
	StoppedAtBound bool `json:"stopped_at_bound,omitempty"`
	// Output is the bounded capture of a failing check, for the item a red
	// landing files and for whoever reads the run.
	Output string `json:"output,omitempty"`
}

// Validate reports every contract violation in the recorded landing at once.
func (l LandingChecks) Validate() error {
	var problems []error
	if strings.TrimSpace(l.Commit) == "" {
		problems = append(problems, errors.New("commit is required"))
	}
	if l.StartedAt.IsZero() {
		problems = append(problems, errors.New("started_at is required"))
	}
	if l.BoundSeconds <= 0 {
		problems = append(problems, errors.New("bound_seconds must be positive"))
	}
	for index, check := range l.Checks {
		if strings.TrimSpace(check.Command) == "" {
			problems = append(problems, fmt.Errorf("check %d: command is required", index))
		}
		if len(check.Output) > MaxCheckOutputBytes {
			problems = append(problems, fmt.Errorf("check %d: output is %d bytes, which exceeds the %d byte bound", index, len(check.Output), MaxCheckOutputBytes))
		}
	}
	return errors.Join(problems...)
}

// Finished reports a landing whose checks have ended, one way or another.
func (l LandingChecks) Finished() bool {
	return l.FinishedAt != nil
}

// CloseInterrupted ends a landing the process running its checks never ended,
// as unverified: the sweep settling the run calls it, because a landing that
// reads as running forever is one nobody was told went unverified. A landing
// already ended is left as it is.
func (l *LandingChecks) CloseInterrupted(now time.Time) {
	if l == nil || l.Finished() {
		return
	}
	finished := now
	l.FinishedAt = &finished
	l.Ran = false
	l.Green = false
	if l.Waiting() {
		l.Problem = strings.TrimPrefix(l.Problem+"; the process waiting its turn behind another landing died before the landing checks started", "; ")
		return
	}
	l.Problem = strings.TrimPrefix(l.Problem+"; the process running the landing checks died before they ended", "; ")
}

// Waiting reports a landing that found another landing on its branch running
// and was never let in: while it is unfinished, it is queued rather than
// running.
func (l LandingChecks) Waiting() bool {
	return l.WaitingSince != nil && l.AdmittedAt == nil
}

// Red reports a finished landing whose checks ran and did not all pass.
func (l LandingChecks) Red() bool {
	return l.Finished() && l.Ran && !l.Green
}

// Unverified reports a finished landing whose checks could not run, which is
// neither green nor red and is the state Problem explains.
func (l LandingChecks) Unverified() bool {
	return l.Finished() && !l.Ran
}

// AllPassed reports every recorded check passed.
func (l LandingChecks) AllPassed() bool {
	for _, check := range l.Checks {
		if !check.Passed {
			return false
		}
	}
	return true
}

// Bound is the stage bound the landing checks ran under, as a span.
func (l LandingChecks) Bound() time.Duration {
	return time.Duration(l.BoundSeconds) * time.Second
}

// Failing is the first landing check that did not pass, and whether there is
// one.
func (l LandingChecks) Failing() (LandingCheckResult, bool) {
	for _, check := range l.Checks {
		if !check.Passed {
			return check, true
		}
	}
	return LandingCheckResult{}, false
}

// Describe says what became of the landing, in one line every surface uses.
func (l LandingChecks) Describe() string {
	commit := l.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	var said string
	switch {
	case !l.Finished() && l.Waiting():
		return fmt.Sprintf("landing checks waiting over %s behind another landing on %s, since %s", commit, nonEmptyBranch(l.TargetBranch), l.WaitingSince.UTC().Format(time.RFC3339))
	case !l.Finished():
		return fmt.Sprintf("landing checks running over %s, each bounded at %s", commit, describeSpan(l.Bound()))
	case !l.Ran:
		said = fmt.Sprintf("unverified landing: the landing checks did not run to the end over %s", commit)
	case l.Green:
		said = fmt.Sprintf("green landing: %s passed over %s in %s", count(len(l.Checks), "landing check"), commit, describeSpan(l.spent()))
	default:
		failing, _ := l.Failing()
		said = fmt.Sprintf("red landing: %s exited %d over %s", failing.Command, failing.ExitCode, commit)
		switch {
		case l.FiledWorkItem != "" && l.FiledEarlier:
			said += "; red again on " + l.FiledWorkItem + ", filed by an earlier landing"
		case l.FiledWorkItem != "":
			said += "; filed as " + l.FiledWorkItem
		case l.FilingProblem != "":
			said += "; no item could be filed: " + l.FilingProblem
		}
	}
	if l.WaitingSince != nil && l.AdmittedAt != nil {
		said += fmt.Sprintf(", after waiting %s behind another landing on %s", describeSpan(l.AdmittedAt.Sub(*l.WaitingSince)), nonEmptyBranch(l.TargetBranch))
	}
	if l.Problem != "" {
		said += " (" + l.Problem + ")"
	}
	return said
}

// nonEmptyBranch names a landing's branch, or says it went unrecorded, which is
// every landing recorded before landings queued.
func nonEmptyBranch(branch string) string {
	if branch == "" {
		return "its target branch"
	}
	return branch
}

// spent is how long the checks themselves took: from the landing's admission
// where it waited its turn, so the wait is said once and not counted twice.
func (l LandingChecks) spent() time.Duration {
	if l.FinishedAt == nil {
		return 0
	}
	if l.AdmittedAt != nil {
		return l.FinishedAt.Sub(*l.AdmittedAt)
	}
	return l.FinishedAt.Sub(l.StartedAt)
}

// ChecksPassed is the evidence that every configured check passed over the
// change this run is carrying, bound to the change it passed over: the content
// of the change as the worktree held it, the attempt that produced it, and the
// commit the harness made of it. It is what the promotion reads before it moves the target branch.
// Control flow already orders the checks in front of the promotion, and that is
// not the same guarantee: an integration reached by any route has to find this
// on the record, for exactly the content it is about to promote, or refuse. A
// later attempt invalidates it by moving the attempt count, an edit to the
// worktree invalidates it by moving the content, and a failing check clears it,
// so it never describes a change the gate has moved past.
type ChecksPassed struct {
	// Content names the change the checks passed over, as the worktree manager
	// names it: a versioned digest over the base, every path the change touches,
	// its Git mode, and its regular-file blob or symlink target text. It is the
	// binding that always holds and makes this evidence about a revision rather
	// than about a moment. The promotion reads the worktree again and refuses
	// a change whose content differs, whether or not anything was ever committed.
	Content string `json:"content"`
	// Attempt is the repair attempt the checks ran over, which is the run's
	// RepairAttempts at the time. The first attempt is zero.
	Attempt int `json:"attempt"`
	// Commit is the harness commit the worktree stood at when the checks ran.
	// Every developer invocation is committed as it ends, publishing or not, and
	// a replayed promotion records the rebased commit before the gate is
	// re-earned, so a change the checks passed over has one; it is empty only for
	// an attempt that changed nothing. It is held to beside the content because a
	// commit is what the branch and the forge name the change by.
	Commit string `json:"commit,omitempty"`
	// Commands are the configured checks that passed, in the order they ran.
	Commands []string  `json:"commands,omitempty"`
	At       time.Time `json:"at"`
}

// Validate rejects evidence that cannot describe checks that actually ran.
func (c ChecksPassed) Validate() error {
	var problems []error
	if strings.TrimSpace(c.Content) == "" {
		problems = append(problems, errors.New("content is required: evidence bound to no change is evidence for any change"))
	}
	if c.Attempt < 0 {
		problems = append(problems, fmt.Errorf("attempt %d cannot be negative", c.Attempt))
	}
	if c.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	for i, command := range c.Commands {
		if strings.TrimSpace(command) == "" {
			problems = append(problems, fmt.Errorf("commands[%d] is blank", i))
		}
	}
	return errors.Join(problems...)
}

// MaxRefusedPaths bounds how many protected paths a refusal carries into
// durable state and into the developer's next attempt. A change that rewrote a
// whole artifact home must not be able to fill either with a listing, and the
// count of what was dropped is kept beside what was kept, so a bounded refusal
// never reads as the whole of what the gate caught.
const MaxRefusedPaths = 50

// PathRefusal is the protected-path gate's refusal a repair attempt was handed
// back: the upstream paths the change touched that the work item never granted.
// It is durable for the reason a failing check is, and for one more. An attempt
// interrupted before it ran has to be reissued with exactly the input it was
// given, and a run that spends its attempts has to name what it still refuses —
// and unlike a check, nothing re-derives this for a reader afterwards, because
// the worktree it describes is removed when the run is cleaned up.
type PathRefusal struct {
	// Paths are the refused paths, repository-relative, in the order they sort.
	Paths []string `json:"paths"`
	// Omitted is how many further refused paths the bound above dropped.
	Omitted int `json:"omitted,omitempty"`
	// Grants is what the work item did grant, recorded beside the refusal
	// because the two are read together: a refusal that looks wrong is most often
	// a grant that named the path differently, and a reader with only one half of
	// that cannot see it.
	Grants []string `json:"grants,omitempty"`
}

// Validate reports every contract violation in the recorded refusal at once.
func (p PathRefusal) Validate() error {
	var problems []error
	if len(p.Paths) == 0 {
		problems = append(problems, errors.New("at least one refused path is required"))
	}
	if len(p.Paths) > MaxRefusedPaths {
		problems = append(problems, fmt.Errorf("%d refused paths are recorded, which exceeds the bound of %d", len(p.Paths), MaxRefusedPaths))
	}
	for index, refused := range p.Paths {
		if strings.TrimSpace(refused) == "" {
			problems = append(problems, fmt.Errorf("paths[%d] is empty", index))
		}
	}
	if p.Omitted < 0 {
		problems = append(problems, errors.New("omitted cannot be negative"))
	}
	return errors.Join(problems...)
}

// MaxConflictedPaths bounds how many paths a refused replay carries into
// durable state and into the developer's next attempt, and
// MaxConflictDetailBytes bounds Git's own account of the refusal. A replay that
// stopped on a hundred files must not be able to fill either, and what the
// bound drops is counted beside what it kept, for the reason a bounded path
// refusal counts its own.
const (
	MaxConflictedPaths     = 50
	MaxConflictDetailBytes = 4 << 10
)

// MaxCarriedAmendmentRefusals bounds how many refused amendment proposals a run
// carries into a role's next invocation, and MaxAmendmentRefusalBytes bounds one
// of them. Both are generous for what actually accumulates — one reply proposes
// at most a handful of changes, and what one role is carrying is emptied by that
// role's next reply — and they are here so a provider emitting nothing but
// unreadable blocks cannot fill durable state or the next prompt with them.
const (
	MaxCarriedAmendmentRefusals = 10
	MaxAmendmentRefusalBytes    = 1 << 10
)

// AmendmentRefusal is one proposed change the harness could not record, waiting
// to be put in front of the agent that proposed it.
//
// The role is recorded with the words rather than left to be assumed from
// whichever agent is invoked next. What refuses a proposal is per-role — a
// proposal from the role that already owns the document is refused for being
// that role's own to make — so a refusal that arrived without its proposer could
// be shown to an agent that is then told not to claim something it never said,
// while the agent that did say it is still never told. That is the same false
// record this exists to end, one role over.
type AmendmentRefusal struct {
	// Role is the contract the proposer was working under when the harness
	// refused what it proposed, and is what decides whose next invocation opens
	// with this.
	Role domain.AgentRole `json:"role"`
	// Problem is the refusal in the harness's own words. It is carried verbatim:
	// what is wrong with the block is the whole of what its author needs to write
	// a different one, and a paraphrase is the harness guessing at that.
	Problem string `json:"problem"`
}

// MaxRunAmendments bounds how many proposals, raised and dropped together, a
// run's record keeps. One reply proposes at most amendment.MaxProposalsPerReply
// changes, so this is several times what a run's attempts ordinarily produce; it
// is here so a provider proposing on every reply cannot grow the record without
// limit. Past it nothing is dropped as a restatement, because a drop the record
// cannot hold is an argument lost with nothing anybody can find: the proposal is
// raised instead, which costs its owner a second copy at worst.
const MaxRunAmendments = 32

// RunAmendment is one proposal a run's agent made, and what the harness did with
// it: raised it, or dropped it as a restatement of one this run had already
// raised.
//
// It is on the run's record for two reasons. It is the memory a restatement is
// compared against, so a run continued in a second process — a usage-limit pause
// that exited on its in-process bound, a repair re-entering a stopped run —
// folds a restatement made there exactly as the first process would have. And a
// dropped restatement is written down with what it was folded into and how alike
// the two read, so a pair the comparison folded wrongly loses its second argument
// somewhere somebody can find it rather than nowhere.
type RunAmendment struct {
	// Role is the contract the proposer was working under.
	Role domain.AgentRole `json:"role"`
	// Artifact is the document the change is to, and Change is what it asks for,
	// in the proposer's own words — which is what the next proposal is compared
	// against, and what somebody reading a dropped one needs to judge the fold.
	Artifact string `json:"artifact"`
	Change   string `json:"change"`
	// ID is the recorded proposal, for one that was raised. It is empty for a
	// dropped one, which was never recorded and has no id anybody could look up.
	ID string `json:"id,omitempty"`
	// FoldedInto is the raised proposal a dropped one was read as restating, and
	// Likeness is the share of content words the two have in common, the score the
	// comparison folded it on. Both are empty for a raised proposal.
	FoldedInto string  `json:"folded_into,omitempty"`
	Likeness   float64 `json:"likeness,omitempty"`
}

// Dropped reports whether the proposal was folded into one already raised
// rather than raised itself.
func (a RunAmendment) Dropped() bool {
	return a.FoldedInto != ""
}

// Validate reports every contract violation in the recorded proposal at once.
func (a RunAmendment) Validate() error {
	var problems []error
	if err := domain.ValidateIdentifier("role", string(a.Role)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(a.Artifact) == "" {
		problems = append(problems, errors.New("artifact is required"))
	}
	if strings.TrimSpace(a.Change) == "" {
		problems = append(problems, errors.New("change is required"))
	}
	if len(a.Change) > amendment.MaxTextBytes {
		problems = append(problems, fmt.Errorf("change is %d bytes, which exceeds the %d byte bound", len(a.Change), amendment.MaxTextBytes))
	}
	switch {
	case a.ID == "" && a.FoldedInto == "":
		problems = append(problems, errors.New("a proposal is either raised, with an id, or dropped, with the id it was folded into"))
	case a.ID != "" && a.FoldedInto != "":
		problems = append(problems, errors.New("a raised proposal is not folded into another"))
	}
	if a.Likeness < 0 || a.Likeness > 1 {
		problems = append(problems, fmt.Errorf("likeness %v is outside 0 to 1", a.Likeness))
	}
	if a.FoldedInto == "" && a.Likeness != 0 {
		problems = append(problems, errors.New("likeness is recorded only for a dropped proposal"))
	}
	return errors.Join(problems...)
}

// Validate reports every contract violation in the carried refusal at once.
func (a AmendmentRefusal) Validate() error {
	var problems []error
	if err := domain.ValidateIdentifier("role", string(a.Role)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(a.Problem) == "" {
		problems = append(problems, errors.New("problem is required"))
	}
	if len(a.Problem) > MaxAmendmentRefusalBytes {
		problems = append(problems, fmt.Errorf("problem is %d bytes, which exceeds the %d byte bound", len(a.Problem), MaxAmendmentRefusalBytes))
	}
	return errors.Join(problems...)
}

// ContextTruncation is what an item's notes lost to the context budget when the
// run's context was assembled. It is recorded because the loss is otherwise
// visible only inside the text an agent was handed, and it is the kind of thing
// that arrives quietly: an item's notes grow with every run appended to them and
// nothing removes them, so the first item to outgrow the budget does so between
// one run and the next, without anybody deciding it. The context still says so
// in its own marker; this is how a reader of the record sees it without opening
// the context.
type ContextTruncation struct {
	// DroppedNotes is how many of the oldest notes the context left out.
	DroppedNotes int `json:"dropped_notes"`
	// DroppedBytes is how much of the notes that was.
	DroppedBytes int `json:"dropped_bytes"`
	// KeptBytes is how much of the notes the context still carried, recorded
	// beside the loss because the two are read together: what was dropped means
	// something different against an item still carrying most of its notes than
	// against one carrying almost none.
	KeptBytes int `json:"kept_bytes,omitempty"`
}

// Validate reports every contract violation in the recorded truncation at once.
func (c ContextTruncation) Validate() error {
	var problems []error
	if c.DroppedNotes < 1 {
		problems = append(problems, errors.New("a recorded truncation drops at least one note"))
	}
	if c.DroppedBytes < 1 {
		problems = append(problems, errors.New("a recorded truncation drops at least one byte"))
	}
	if c.KeptBytes < 0 {
		problems = append(problems, errors.New("kept_bytes cannot be negative"))
	}
	return errors.Join(problems...)
}

// StaleBlockClear is what became of the stale blocked status the claim cleared
// on its way to this run's item: whether the tracker read the status back as
// open, how many reads that took, and what the last read returned. It is
// recorded because the claim is the one place the harness has the tracker's
// answer in hand, and because the ending that matters most is the one nothing
// else records — a clear no read confirmed leaves the item for the next pull
// and the run dead at the claim, which until yoyodyne-ifd.428.3 read as a run
// that died for nothing anybody could see. On 2026-09-20 the claim on
// yoyodyne-ifd.415 recorded its clear as made and the claim that followed was
// refused on the same status.
type StaleBlockClear struct {
	// Outcome is which of the read-back's three endings this was.
	Outcome domain.StaleBlockClearOutcome `json:"outcome"`
	// Reads is how many times the status was read back, the read that confirmed
	// it included; on an unconfirmed clear, every read the bound allowed.
	Reads int `json:"reads"`
	// Status is what the last read returned.
	Status string `json:"status,omitempty"`
	// ClaimsRefused is how many claims the tracker refused on the status after a
	// read had returned open, each retried on a later read within the same bound.
	// On 2026-09-22 and 2026-09-23 two re-runs met exactly that and lost their
	// pull to it (yoyodyne-ifd.428.12).
	ClaimsRefused int `json:"claims_refused,omitempty"`
}

// Describe says which ending the clear had in the words a surface prints, so
// every surface that names it names it the same way.
func (c StaleBlockClear) Describe() string {
	switch c.Outcome {
	case domain.StaleBlockClearConfirmed:
		return "the tracker read the cleared status back as open on the first read, and the item was claimed"
	case domain.StaleBlockClearConfirmedLate:
		if c.ClaimsRefused > 0 {
			return fmt.Sprintf("the tracker read the cleared status back as open, refused the claim on the status %d time(s), and the item was claimed on read %d", c.ClaimsRefused, c.Reads)
		}
		return fmt.Sprintf("the tracker read the cleared status back as open on read %d, and the item was claimed", c.Reads)
	case domain.StaleBlockClearUnconfirmed:
		if c.ClaimsRefused > 0 {
			return fmt.Sprintf("no claim confirmed the clear: the tracker refused the claim on the status %d time(s) after a read returned open, the last of %d read(s) returned status %q, and the item was left for the next pull", c.ClaimsRefused, c.Reads, c.Status)
		}
		return fmt.Sprintf("no read confirmed the clear: %d read(s) returned status %q rather than open, and the item was left for the next pull", c.Reads, c.Status)
	}
	return fmt.Sprintf("the clear ended in a way the record does not name (%q)", c.Outcome)
}

// Validate reports every contract violation in the recorded clear at once.
func (c StaleBlockClear) Validate() error {
	var problems []error
	if !c.Outcome.Valid() {
		problems = append(problems, fmt.Errorf("outcome %q is not one the harness names", c.Outcome))
	}
	if c.Reads < 0 {
		problems = append(problems, errors.New("reads cannot be negative"))
	}
	if c.Outcome != domain.StaleBlockClearUnconfirmed && c.Reads < 1 {
		problems = append(problems, errors.New("a confirmed clear was read back at least once"))
	}
	if c.ClaimsRefused < 0 {
		problems = append(problems, errors.New("claims_refused cannot be negative"))
	}
	if c.ClaimsRefused > c.Reads {
		problems = append(problems, errors.New("claims_refused cannot exceed reads: a claim is made only after a read"))
	}
	return errors.Join(problems...)
}

// Finding is one durable reviewer finding. Findings are recorded rather than
// only counted because they are the developer's input for the next repair
// attempt: a run interrupted between attempts has to hand back exactly what the
// reviewer asked for, and a run that spends its attempts has to name what is
// still unresolved.
type Finding struct {
	Severity    string `json:"severity"`
	Disposition string `json:"disposition,omitempty"`
	Absent      string `json:"absent,omitempty"`
	Message     string `json:"message"`
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
}

// Validate reports every contract violation in the finding at once.
func (f Finding) Validate() error {
	var problems []error
	if !slices.Contains(findingSeverities, f.Severity) {
		problems = append(problems, fmt.Errorf("severity %q must be %s", f.Severity, quotedAlternatives(findingSeverities)))
	}
	if f.Disposition != "" && !slices.Contains(findingDispositions, f.Disposition) {
		problems = append(problems, fmt.Errorf("disposition %q must be %s or omitted", f.Disposition, quotedAlternatives(findingDispositions)))
	}
	if strings.TrimSpace(f.Message) == "" {
		problems = append(problems, errors.New("message is required"))
	}
	if f.Line < 0 {
		problems = append(problems, fmt.Errorf("line %d cannot be negative", f.Line))
	}
	if f.Line > 0 && strings.TrimSpace(f.File) == "" {
		problems = append(problems, errors.New("line requires a file"))
	}
	return errors.Join(problems...)
}

// PullRequest is the durable record of the pull request a run published its
// work through: which remote carries the branch, which commit was last pushed
// to it, and what the forge says about the request itself. It is recorded from
// the developer phase onward, so a run that stops anywhere after that still
// names the published work rather than leaving it to be rediscovered by hand.
type PullRequest struct {
	Remote     string `json:"remote"`
	Branch     string `json:"branch"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	HeadCommit string `json:"head_commit"`
	State      string `json:"state,omitempty"`
	Merged     bool   `json:"merged,omitempty"`
	// MergeMethod is the method the forge was asked to merge by, recorded
	// because it decides what the remote history looks like: only one of the
	// methods puts the promoted commit itself on the remote target rather than a
	// rewritten copy of it. MergeCommit is where that merge left the remote
	// target branch, which is the forge's own merge commit and therefore a
	// commit the local target branch does not carry.
	MergeMethod string `json:"merge_method,omitempty"`
	MergeCommit string `json:"merge_commit,omitempty"`
	// MergeQueued reports a merge the forge accepted and the harness has not seen
	// the end of. Ordinarily that is a merge the forge has not performed: it
	// merges the request itself once the base branch's requirements are met,
	// which happens long after the run that asked for it has finished. It keeps
	// the run outstanding until somebody knows which way that went, and is
	// cleared when reconciliation observes the merge land or be dropped.
	//
	// A repeated merge request sets it on either answer, including a merge the
	// forge performed on the spot. Finishing a publication — confirming the remote
	// target, recording the merge commit, deleting the consumed branch, catching
	// the local target up — is the settle path's work and needs a worktree the
	// re-arm has not got, so what it records is that the forge took the request
	// and the harness has not confirmed the end of it. Reconciliation asks, finds
	// it landed, and finishes it.
	MergeQueued bool `json:"merge_queued,omitempty"`
	// MergeRearms is how many times the harness has repeated this publication's
	// merge request after the forge dropped the merge it had queued. It is the
	// durable once-per-publication counter the re-arm is bounded by, and it lives
	// on the publication rather than beside it because the publication is what it
	// bounds: the development manager's decision is counted on the work item, and
	// this is what says the decision has been carried out. Without it one recorded
	// decision would authorize every repeat anybody asked for.
	//
	// It is written before the repeated request is made, which is the direction
	// every triage counter fails in: a process that dies between the two has
	// recorded a re-arm it did not make rather than made one it did not record.
	MergeRearms int `json:"merge_rearms,omitempty"`
	// Superseded names the vehicle this publication's work landed by, when it
	// landed by another one: a later run's pull request, or the commit that run
	// integrated. A branch carries the run that published it, so an item run
	// again publishes a fresh branch and a fresh request rather than reusing this
	// one, and this request would otherwise sit open for work that is already on
	// the target branch.
	//
	// It is written when the harness retires the publication — the request
	// closed with a comment naming that vehicle, the branch it carried deleted —
	// and it is what stops a later sweep asking the forge about a request it has
	// already dealt with. Absent is every publication that merged, that is still
	// pending, or that stopped for a reason somebody has to decide about.
	Superseded string `json:"superseded,omitempty"`
	// Checks is the forge's check state for the request's head as the
	// reconciling sweep last read it, written on every sweep that finds the merge
	// still queued. It is what says whether a queued merge is going to land at
	// all, and it is absent until a sweep has read it.
	Checks *PullRequestChecks `json:"checks,omitempty"`
	// HandedBack is the development manager's re-run of a publication nothing
	// ever asked the forge to merge having been carried out: the change was
	// handed back for a fresh run, so this request is no longer a publication
	// anybody is waiting on or has to decide. Absent on every other publication.
	HandedBack *PublicationHandBack `json:"handed_back,omitempty"`
	// TargetRed is a queued merge the reconciling sweep withdrew because its
	// checks failed on the target rather than on the change, and the items it
	// waits on. It is cleared when the merge is armed again.
	TargetRed *TargetRed `json:"target_red,omitempty"`
}

// PublicationHandBack records a publication handed back for a fresh run: when,
// the docket entry the re-run was carried out against, and the reason the
// fresh run records as why it exists.
type PublicationHandBack struct {
	At        time.Time `json:"at"`
	DocketKey string    `json:"docket_key,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}

// Closed reports a request the forge closed without merging it.
func (p PullRequest) Closed() bool {
	return !p.Merged && strings.EqualFold(strings.TrimSpace(p.State), "closed")
}

// MergeDrop is the moment a promoted change stopped being something the forge
// was going to merge: the merge was refused while the run watched — the remote
// target moved under it is the ordinary way — or the forge gave up on one it had
// already queued.
//
// It is recorded rather than derived because a state that only exists at read
// time cannot be said. That the publication is outstanding was always readable
// from the record, by a status surface or a reconcile sweep somebody ran; what
// nothing held was the moment it became true, so nothing could announce it and
// four such merges once waited hours with the channel silent. This is that
// moment, written where it happens and never revised afterwards.
type MergeDrop struct {
	At time.Time `json:"at"`
	// Reason is why the merge is not going to happen, in the words of whoever
	// found out. It is the same sentence the publication failure carries, kept
	// beside the moment so a reader of the drop alone is not sent to another
	// field to find out what it was.
	Reason string `json:"reason"`
}

// Validate rejects a drop that cannot describe a real one.
func (d MergeDrop) Validate() error {
	var problems []error
	if d.At.IsZero() {
		problems = append(problems, errors.New("merge_drop at is required"))
	}
	if strings.TrimSpace(d.Reason) == "" {
		problems = append(problems, errors.New("merge_drop reason is required: a drop nobody can say the cause of is not worth recording"))
	}
	return errors.Join(problems...)
}

// Validate rejects a published record that cannot describe a real pull request.
func (p PullRequest) Validate() error {
	var problems []error
	if strings.TrimSpace(p.Remote) == "" {
		problems = append(problems, errors.New("pull_request remote is required"))
	}
	if !validLocalBranch(p.Branch) {
		problems = append(problems, errors.New("pull_request branch must be a local branch name"))
	}
	if p.Number <= 0 {
		problems = append(problems, errors.New("pull_request number must be positive"))
	}
	if strings.TrimSpace(p.URL) == "" {
		problems = append(problems, errors.New("pull_request url is required"))
	}
	if !commitPattern.MatchString(p.HeadCommit) {
		problems = append(problems, errors.New("pull_request head_commit is invalid"))
	}
	if p.MergeRearms < 0 {
		problems = append(problems, errors.New("pull_request merge_rearms cannot be negative"))
	}
	// A re-arm repeats the request the run's own merge asked for, so a
	// publication that records one and no method could not have been re-armed by
	// the only thing that re-arms one: the method is what says which request was
	// repeated.
	if p.MergeRearms > 0 && strings.TrimSpace(p.MergeMethod) == "" {
		problems = append(problems, errors.New("pull_request merge_rearms requires the merge method the repeated request was made by"))
	}
	// The two are contradictory claims about one request: a merge is this
	// publication's work reaching the remote, and a supersession is another
	// vehicle's work reaching it instead.
	if p.Merged && strings.TrimSpace(p.Superseded) != "" {
		problems = append(problems, errors.New("a merged pull request was not superseded; what landed is its own work"))
	}
	if p.Checks != nil {
		if err := p.Checks.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("pull_request %w", err))
		}
	}
	if p.TargetRed != nil {
		if err := p.TargetRed.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("pull_request %w", err))
		}
	}
	return errors.Join(problems...)
}

// MaxChangeRecordBytes bounds each half of the recorded account of what a run
// changed. A run that touched two hundred files must not be able to fill the
// state file with its own listing, and what the bound cuts is the tail of a
// summary rather than any part of the run's evidence: the change itself is in
// the commit.
const MaxChangeRecordBytes = 8 << 10

// Changes is what a run's worktree held when the harness last summarized it:
// the files it had touched and how much of each. It is recorded because it is
// the only account of what a run changed that outlives the worktree — cleanup
// removes the tree and the branch, and a diff nobody can take any more is a
// diff nobody can be shown.
type Changes struct {
	// Files is the name-status listing, and DiffStat is Git's own summary of how
	// much each file changed. Both are Git's words, kept as they were produced.
	Files    string `json:"files,omitempty"`
	DiffStat string `json:"diff_stat,omitempty"`
}

// RecordChanges makes a bounded record of a summarized change, or nothing at
// all when the summary is empty. A summary too long to keep is cut rather than
// refused: losing a run's state file over a verbose listing would cost far more
// than the tail of one.
func RecordChanges(files, diffStat string) *Changes {
	files = boundChangeRecord(files)
	diffStat = boundChangeRecord(diffStat)
	if files == "" && diffStat == "" {
		return nil
	}
	return &Changes{Files: files, DiffStat: diffStat}
}

// Validate rejects a recorded change that could not have been produced within
// the bounds the harness records under.
func (c Changes) Validate() error {
	var problems []error
	if len(c.Files) > MaxChangeRecordBytes {
		problems = append(problems, fmt.Errorf("changes files is %d bytes, which exceeds the %d byte bound", len(c.Files), MaxChangeRecordBytes))
	}
	if len(c.DiffStat) > MaxChangeRecordBytes {
		problems = append(problems, fmt.Errorf("changes diff_stat is %d bytes, which exceeds the %d byte bound", len(c.DiffStat), MaxChangeRecordBytes))
	}
	return errors.Join(problems...)
}

// boundChangeRecord cuts one half of a change record to its bound and says that
// it was cut, so nobody reads a clamped listing as a complete one.
func boundChangeRecord(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	trimmed := strings.TrimRight(text, "\n")
	if len(trimmed) <= MaxChangeRecordBytes {
		return trimmed
	}
	cut := MaxChangeRecordBytes - len(changeRecordCutNote)
	for cut > 0 && !utf8.RuneStart(trimmed[cut]) {
		cut--
	}
	return strings.TrimRight(trimmed[:cut], "\n") + changeRecordCutNote
}

const changeRecordCutNote = "\n[cut; the rest of this summary was not recorded]"

// MaxBlockerBytes bounds the durable blocker a run carries. It is the docket's
// own bound, shared rather than restated: a docket entry has to carry the
// blocker in the words it was recorded in, and two bounds that could drift
// would be a blocker the harness recorded and the docket then refused.
const MaxBlockerBytes = triage.MaxBlockerBytes

// RecordBlocker makes a bounded record of a blocker the harness recorded on a
// work item. A blocker too long to keep is cut rather than refused, exactly as
// a verbose change summary is: losing a run's state file over a reviewer that
// wrote at length would cost far more than the tail of one blocker, and the
// tracker holds the whole of it either way.
func RecordBlocker(notes string) string {
	return boundRecordedText(notes, MaxBlockerBytes, blockerCutNote)
}

const blockerCutNote = "\n[cut; the work item carries the whole of this blocker]"

// RecordFailure makes a bounded record of the reason a run ended. It is how
// State.Failure is written, because what ends a run is often an error with a
// provider's whole output folded into it, and the record is held to the same
// bound as every other recorded reason: a reason cut here still says why the run
// stopped, while one the store refuses is a terminal record that never lands.
//
// It is applied again by whoever carries the reason somewhere with a bound of its
// own — the docket entry a stoppage produces is the case that found this — and
// that second cut is a no-op over an already-bounded reason rather than a bound
// the record is trusted to have honoured.
func RecordFailure(failure string) string {
	return boundRecordedText(failure, MaxBlockerBytes, failureCutNote)
}

// The note promises nothing about where the rest went, unlike the blocker's,
// because there is nowhere it reliably is: the record is the run's own account of
// why it stopped and this is now the first cut rather than a second one, so a
// note sending a reader to the record would send them to the copy they are
// already reading.
const failureCutNote = "\n[cut; the rest of this failure was not recorded]"

// RecordEscalationReason makes a bounded record of what a role said when it
// raised the item as unmeetable, for the docket entry that carries it to the
// development manager. The reason is the whole of what she decides from and it is
// cut rather than refused for the reason a blocker is: an entry refused for its
// length reaches nobody, which is worse than one whose tail is somewhere else.
//
// Where the whole of it is depends on who raised it — the developer's claim is in
// the run's landing reason and the reviewer's is in its review summary — so the
// note names the run rather than one of the two.
func RecordEscalationReason(reason string) string {
	return boundRecordedText(reason, MaxBlockerBytes, escalationCutNote)
}

const escalationCutNote = "\n[cut; the run's own record carries the whole of this reason]"

// MaxReviewSummaryBytes bounds the reviewer's summary a run carries. It is the
// docket's own bound on a summary, shared rather than restated for the reason
// the blocker's is: a docket entry carries the summary in the reviewer's words,
// and two bounds that could drift would be a summary the harness recorded and
// the docket then refused.
const MaxReviewSummaryBytes = triage.MaxMessageBytes

// RecordReviewSummary makes a bounded record of what the reviewer said about the
// change. It is how State.ReviewSummary is written, because a reviewer writes at
// whatever length it likes and the summary is carried onward by readers that
// cannot take an unbounded one — the docket entry a stoppage produces is bounded
// to 4 KiB, and an entry refused for its length is a stopped run the development
// manager never hears about.
func RecordReviewSummary(summary string) string {
	return boundRecordedText(summary, MaxReviewSummaryBytes, reviewSummaryCutNote)
}

// The note promises nothing about where the rest went, as the failure's does not:
// this is the first cut rather than a second one, so the record a reader would be
// sent to is the copy they are already reading.
const reviewSummaryCutNote = "\n[cut; the rest of this summary was not recorded]"

// DeveloperSummary is the developer's account of one completed invocation,
// bound to the attempt and the content it left. It is a claim, never gate
// evidence. A new invocation clears it before it can change the worktree.
type DeveloperSummary struct {
	Text    string `json:"text"`
	Content string `json:"content"`
	Attempt int    `json:"attempt"`
}

func (s DeveloperSummary) Validate() error {
	var problems []error
	if strings.TrimSpace(s.Text) == "" {
		problems = append(problems, errors.New("text is required"))
	}
	if len(s.Text) > MaxRecordedTextBytes {
		problems = append(problems, fmt.Errorf("text exceeds the %d byte bound", MaxRecordedTextBytes))
	}
	if strings.TrimSpace(s.Content) == "" {
		problems = append(problems, errors.New("content is required"))
	}
	if s.Attempt < 0 {
		problems = append(problems, errors.New("attempt cannot be negative"))
	}
	return errors.Join(problems...)
}

// RecordDeveloperSummary retains the account within the existing recorded-text
// bound, marking a cut on a rune boundary so a partial account is never whole
// evidence to the reviewer.
func RecordDeveloperSummary(text string) string {
	return boundRecordedText(text, MaxRecordedTextBytes, reviewSummaryCutNote)
}

// MaxChannelProblemBytes bounds the record of what a run's two side channels —
// the reports its agents filed and the amendments they proposed — could not
// read or could not keep. One lost entry is folded to a line by the caller
// before it gets here, and a run accumulates one line per lost entry, so this
// is a bound on a run whose every reply is an unreadable block rather than on
// anything that ordinarily happens.
const MaxChannelProblemBytes = 4 << 10

// RecordChannelProblem makes a bounded record of what a run's report or
// amendment channel could not keep. It is how State.ReportProblem and
// State.AmendmentProblem are written, and it exists because those two were for a
// long time written only to the outcome `yoyo run` prints: a proposal that was
// refused, or that was made on a run whose process died before it reported,
// read afterwards exactly as one that was never made. Three attempts on one run
// proposed a change and the store held none of them, and nothing could say
// afterwards whether they were refused or lost.
func RecordChannelProblem(problem string) string {
	return boundRecordedText(problem, MaxChannelProblemBytes, channelProblemCutNote)
}

const channelProblemCutNote = "\n[cut; the rest of what this channel could not keep was not recorded]"

// boundRecordedText cuts one recorded reason to its bound and says that it was
// cut, so nobody reads a clamped account as a complete one. The cut lands on a
// rune boundary, because a record ending mid-character is one a later reader
// cannot decode at all.
func boundRecordedText(text string, limit int, cutNote string) string {
	trimmed := strings.TrimRight(text, "\n")
	if strings.TrimSpace(trimmed) == "" {
		return ""
	}
	if len(trimmed) <= limit {
		return trimmed
	}
	cut := limit - len(cutNote)
	for cut > 0 && !utf8.RuneStart(trimmed[cut]) {
		cut--
	}
	return strings.TrimRight(trimmed[:cut], "\n") + cutNote
}

// MaxRecordedTextBytes bounds every free-text field on the run record that has
// no bound of its own. It is the docket's bound on a message, shared for the
// reason the review summary's is: these are the fields a docket entry, a
// notification, or a status line carries onward, and a bound that could drift
// from theirs is a reason recorded here and refused there.
const MaxRecordedTextBytes = triage.MaxMessageBytes

// truncatedNote is the marker a field cut to its bound ends on, where no older
// note of its own was already established. It is the shape yoyodyne-ifd.258 gave
// a process's cut output — the bound it was cut at, and where the rest is — and
// it says the rest was not recorded because for these fields the record is the
// only copy.
func truncatedNote(limit int) string {
	return fmt.Sprintf("\n[truncated at %d bytes; the rest was not recorded]", limit)
}

// selectionCutNote is the marker a selection's reason has always been cut with
// by Selection.Stamped, used again here so a reason cut on its way into the store
// reads the same as one cut where it was stamped.
const selectionCutNote = " …truncated to the recorded bound"

// recordedText is one free-text field of the run record and the bound it is
// held to.
type recordedText struct {
	// key is the field's place in the record, with [] standing for any element
	// of a list, and path is the same place with the element's index.
	key   string
	path  string
	text  *string
	limit int
	// cutNote is what a cut copy of the field ends on, saying it was cut.
	cutNote string
	// stated reports a bound the nested record's own Validate already states,
	// so State.Validate does not say it a second time.
	stated bool
}

// recordedTexts is every free-text field in the run record, nested ones
// included, each with its bound. It is the one list the bound is applied from —
// by the store on every write and every read, and by Validate — so a field is
// bounded by being named here rather than by every writer of it remembering to
// be.
//
// That is the lesson of three work items bounding three fields one at a time:
// each moved the unbounded case onto the next field nobody had listed.
// TestEveryStringInTheRunRecordIsBoundedOrStructured walks the whole of State,
// through every nested record and list, and fails on any string that is neither
// here nor in its short list of identifiers and enumerations, and on any kind
// of field it cannot see strings inside. A field added later, at any depth, is
// bounded or classified on purpose and never unbounded by omission.
//
// Nested records mostly carry their bounds already, in their own Validate. Those
// are named here at the same bound, so a field over it is cut on its way into
// the store rather than costing the whole record.
func (s *State) recordedTexts() []recordedText {
	var texts []recordedText
	add := func(key, path string, text *string, limit int, cutNote string, stated bool) {
		texts = append(texts, recordedText{key: key, path: path, text: text, limit: limit, cutNote: cutNote, stated: stated})
	}
	own := func(key string, text *string, limit int, cutNote string) {
		add(key, key, text, limit, cutNote, false)
	}
	// nested names a field of a record inside this one, whose own Validate
	// states its bound unless unstated says otherwise.
	nested := func(key, path string, text *string, limit int) {
		add(key, path, text, limit, truncatedNote(limit), true)
	}
	unstated := func(key, path string, text *string, limit int) {
		add(key, path, text, limit, truncatedNote(limit), false)
	}
	at := func(prefix string, index int, field string) string {
		return fmt.Sprintf("%s[%d].%s", prefix, index, field)
	}

	own("work_item_title", &s.WorkItemTitle, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	if s.Selection != nil {
		add("selection.reason", "selection.reason", &s.Selection.Reason, MaxSelectionReasonBytes, selectionCutNote, true)
	}
	own("workflow_divergence", &s.WorkflowDivergence, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	own("workflow_unobserved", &s.WorkflowUnobserved, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	own("developer_model_reason", &s.DeveloperModelReason, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	own("review_summary", &s.ReviewSummary, MaxReviewSummaryBytes, reviewSummaryCutNote)
	if s.DeveloperSummary != nil {
		add("developer_summary.text", "developer_summary.text", &s.DeveloperSummary.Text, MaxRecordedTextBytes, reviewSummaryCutNote, true)
	}
	// The landing reason is carried onward as an escalation's account, which is
	// held to the blocker's bound, so it is held to that bound here too.
	own("landing_reason", &s.LandingReason, MaxBlockerBytes, truncatedNote(MaxBlockerBytes))
	own("landing_impediment_problem", &s.LandingImpedimentProblem, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	own("landing_problem", &s.LandingProblem, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	// A finding is the reviewer's own prose, message and file both, and its
	// record states no bound of its own; the count of findings is the docket's.
	for index := range s.ReviewFindingDetails {
		finding := &s.ReviewFindingDetails[index]
		unstated("review_finding_details[].message", at("review_finding_details", index, "message"), &finding.Message, MaxRecordedTextBytes)
		unstated("review_finding_details[].file", at("review_finding_details", index, "file"), &finding.File, MaxRecordedTextBytes)
	}
	if s.CheckFailure != nil {
		nested("check_failure.output", "check_failure.output", &s.CheckFailure.Output, MaxCheckOutputBytes)
	}
	if s.Verification != nil {
		if s.Verification.Probe != nil {
			nested("verification.probe.command", "verification.probe.command", &s.Verification.Probe.Command, MaxVerificationCommandBytes)
			nested("verification.probe.detail", "verification.probe.detail", &s.Verification.Probe.Detail, MaxVerificationDetailBytes)
		}
		for index := range s.Verification.Checks {
			check := &s.Verification.Checks[index]
			nested("verification.checks[].command", at("verification.checks", index, "command"), &check.Command, MaxVerificationCommandBytes)
			nested("verification.checks[].detail", at("verification.checks", index, "detail"), &check.Detail, MaxVerificationDetailBytes)
		}
		for index := range s.Verification.Owed {
			unstated("verification.owed[]", fmt.Sprintf("verification.owed[%d]", index), &s.Verification.Owed[index], MaxRecordedTextBytes)
		}
		nested("verification.problem", "verification.problem", &s.Verification.Problem, MaxChannelProblemBytes)
	}
	for index := range s.RefusedAmendments {
		nested("refused_amendments[].problem", at("refused_amendments", index, "problem"), &s.RefusedAmendments[index].Problem, MaxAmendmentRefusalBytes)
	}
	for index := range s.Amendments {
		nested("amendments[].change", at("amendments", index, "change"), &s.Amendments[index].Change, amendment.MaxTextBytes)
	}
	// What the gate was told the change touches is a list of packages as long as
	// the change is wide, so it is held to the ordinary bound; the check the stage
	// is on is a command the configuration declares.
	if s.CheckStage != nil {
		unstated("check_stage.narrowed", "check_stage.narrowed", &s.CheckStage.Narrowed, MaxRecordedTextBytes)
	}
	own("check_stage_continuation_refused", &s.CheckStageContinuationRefused, MaxBlockerBytes, truncatedNote(MaxBlockerBytes))
	own("check_stage_continuation_wait_noted", &s.CheckStageContinuationWaitNoted, MaxBlockerBytes, truncatedNote(MaxBlockerBytes))
	own("stall_continuation_refused", &s.StallContinuationRefused, MaxBlockerBytes, truncatedNote(MaxBlockerBytes))
	if s.EscalationEnded != nil {
		nested("escalation_ended.why", "escalation_ended.why", &s.EscalationEnded.Why, MaxBlockerBytes)
	}
	if s.LandingChecks != nil {
		for index := range s.LandingChecks.Checks {
			nested("landing_checks.checks[].output", at("landing_checks.checks", index, "output"), &s.LandingChecks.Checks[index].Output, MaxCheckOutputBytes)
		}
		unstated("landing_checks.filing_problem", "landing_checks.filing_problem", &s.LandingChecks.FilingProblem, MaxRecordedTextBytes)
		unstated("landing_checks.problem", "landing_checks.problem", &s.LandingChecks.Problem, MaxRecordedTextBytes)
	}
	own("report_problem", &s.ReportProblem, MaxChannelProblemBytes, channelProblemCutNote)
	own("amendment_problem", &s.AmendmentProblem, MaxChannelProblemBytes, channelProblemCutNote)
	for index := range s.RepairContinuations {
		continuation := &s.RepairContinuations[index]
		nested("repair_continuations[].reason", at("repair_continuations", index, "reason"), &continuation.Reason, MaxSelectionReasonBytes)
		nested("repair_continuations[].superseded_blocker", at("repair_continuations", index, "superseded_blocker"), &continuation.SupersededBlocker, MaxBlockerBytes)
		if continuation.SupersededCheckFailure != nil {
			nested("repair_continuations[].superseded_check_failure.output", at("repair_continuations", index, "superseded_check_failure.output"), &continuation.SupersededCheckFailure.Output, MaxCheckOutputBytes)
		}
	}
	environmental := func(key, path string, refusal *EnvironmentalRefusal) {
		nested(key+".detail", path+".detail", &refusal.Detail, MaxEnvironmentalDetailBytes)
		nested(key+".problem", path+".problem", &refusal.Problem, MaxEnvironmentalProblemBytes)
	}
	if s.Environmental != nil {
		environmental("environmental", "environmental", s.Environmental)
	}
	if s.IntegrationStop != nil {
		nested("integration_stop.detail", "integration_stop.detail", &s.IntegrationStop.Detail, MaxEnvironmentalDetailBytes)
	}
	if s.ReplayConflict != nil {
		nested("replay_conflict.detail", "replay_conflict.detail", &s.ReplayConflict.Detail, MaxConflictDetailBytes)
	}
	for index := range s.IntegrationResumptions {
		resumption := &s.IntegrationResumptions[index]
		nested("integration_resumptions[].reason", at("integration_resumptions", index, "reason"), &resumption.Reason, MaxSelectionReasonBytes)
		nested("integration_resumptions[].superseded_failure", at("integration_resumptions", index, "superseded_failure"), &resumption.SupersededFailure, MaxBlockerBytes)
		nested("integration_resumptions[].superseded_blocker", at("integration_resumptions", index, "superseded_blocker"), &resumption.SupersededBlocker, MaxBlockerBytes)
		if resumption.SupersededRefusal != nil {
			environmental("integration_resumptions[].superseded_refusal", at("integration_resumptions", index, "superseded_refusal"), resumption.SupersededRefusal)
		}
	}
	for index := range s.SweepContinuations {
		nested("sweep_continuations[].reason", at("sweep_continuations", index, "reason"), &s.SweepContinuations[index].Reason, MaxSelectionReasonBytes)
	}
	for index := range s.CheckStageContinuations {
		continuation := &s.CheckStageContinuations[index]
		nested("check_stage_continuations[].reason", at("check_stage_continuations", index, "reason"), &continuation.Reason, MaxSelectionReasonBytes)
		nested("check_stage_continuations[].superseded_failure", at("check_stage_continuations", index, "superseded_failure"), &continuation.SupersededFailure, MaxBlockerBytes)
	}
	for index := range s.Retries {
		nested("retries[].failure", at("retries", index, "failure"), &s.Retries[index].Failure, MaxRetryFailureBytes)
	}
	// The usage limit's kind is the provider's own name for the limit, read off
	// its refusal rather than chosen from a set this harness keeps, so it is held
	// to a bound like any other text the provider wrote.
	own("usage_limit_kind", &s.UsageLimitKind, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	if s.DirectivePause != nil {
		unstated("directive_pause.unresolved", "directive_pause.unresolved", &s.DirectivePause.Unresolved, MaxRecordedTextBytes)
	}
	if s.TrackerPause != nil {
		nested("tracker_pause.failure", "tracker_pause.failure", &s.TrackerPause.Failure, MaxRetryFailureBytes)
	}
	if s.Changes != nil {
		nested("changes.files", "changes.files", &s.Changes.Files, MaxChangeRecordBytes)
		nested("changes.diff_stat", "changes.diff_stat", &s.Changes.DiffStat, MaxChangeRecordBytes)
	}
	// A check's name is the forge's, and the repository's workflows phrase it.
	// Its conclusion is the forge's word for how it ended.
	if s.PullRequest != nil && s.PullRequest.Checks != nil {
		unstated("pull_request.checks.read_error", "pull_request.checks.read_error", &s.PullRequest.Checks.ReadError, MaxRecordedTextBytes)
		for index := range s.PullRequest.Checks.Failing {
			nested("pull_request.checks.failing[].name", at("pull_request.checks.failing", index, "name"), &s.PullRequest.Checks.Failing[index].Name, maxCheckNameBytes)
			nested("pull_request.checks.failing[].conclusion", at("pull_request.checks.failing", index, "conclusion"), &s.PullRequest.Checks.Failing[index].Conclusion, maxCheckNameBytes)
			// The link and the annotations are the forge's own account of the
			// failure, carried onto the item a merge is handed back on.
			failing := &s.PullRequest.Checks.Failing[index]
			nested("pull_request.checks.failing[].url", at("pull_request.checks.failing", index, "url"), &failing.URL, maxCheckURLBytes)
			annotations := at("pull_request.checks.failing", index, "annotations")
			for inner := range failing.Annotations {
				nested("pull_request.checks.failing[].annotations[].path", at(annotations, inner, "path"), &failing.Annotations[inner].Path, MaxCheckAnnotationBytes)
				nested("pull_request.checks.failing[].annotations[].level", at(annotations, inner, "level"), &failing.Annotations[inner].Level, maxCheckNameBytes)
				nested("pull_request.checks.failing[].annotations[].message", at(annotations, inner, "message"), &failing.Annotations[inner].Message, MaxCheckAnnotationBytes)
			}
		}
	}
	// A wait on the target's red check names the checks the forge reported,
	// phrased by the repository's workflows as the reading's own names are.
	if s.PullRequest != nil && s.PullRequest.TargetRed != nil {
		for index := range s.PullRequest.TargetRed.Checks {
			nested("pull_request.target_red.checks[].name", at("pull_request.target_red.checks", index, "name"), &s.PullRequest.TargetRed.Checks[index].Name, maxCheckNameBytes)
		}
	}
	// A hand-back carries the re-run's own reason, which is the selection
	// reason's sentence, and the docket key it was carried out against.
	if s.PullRequest != nil && s.PullRequest.HandedBack != nil {
		unstated("pull_request.handed_back.reason", "pull_request.handed_back.reason", &s.PullRequest.HandedBack.Reason, MaxRecordedTextBytes)
		unstated("pull_request.handed_back.docket_key", "pull_request.handed_back.docket_key", &s.PullRequest.HandedBack.DocketKey, MaxRecordedTextBytes)
	}
	// A supersession names the vehicle the work landed by in one line.
	if s.PullRequest != nil {
		unstated("pull_request.superseded", "pull_request.superseded", &s.PullRequest.Superseded, MaxRecordedTextBytes)
	}
	own("publish_failure", &s.PublishFailure, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	// The drop's reason is the publication failure's sentence kept beside the
	// moment, so it is held to the same bound.
	if s.MergeDrop != nil {
		unstated("merge_drop.reason", "merge_drop.reason", &s.MergeDrop.Reason, MaxRecordedTextBytes)
	}
	own("failure", &s.Failure, MaxBlockerBytes, failureCutNote)
	own("blocker", &s.Blocker, MaxBlockerBytes, blockerCutNote)
	own("cleanup_failure", &s.CleanupFailure, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	own("completion_recording_failure", &s.CompletionRecordingFailure, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	for index := range s.ReconcileFindings {
		nested("reconcile_findings[].problem", at("reconcile_findings", index, "problem"), &s.ReconcileFindings[index].Problem, MaxBlockerBytes)
	}
	if s.ConfigComparison != nil {
		own("config_comparison.active_problem", &s.ConfigComparison.ActiveProblem, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
		own("config_comparison.template_problem", &s.ConfigComparison.TemplateProblem, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
		own("config_comparison.delivery_failure", &s.ConfigComparison.DeliveryFailure, MaxRecordedTextBytes, truncatedNote(MaxRecordedTextBytes))
	}
	return texts
}

// overBound reports a record holding some free-text field longer than its bound.
func (s *State) overBound() bool {
	for _, field := range s.recordedTexts() {
		if len(*field.text) > field.limit {
			return true
		}
	}
	return false
}

// boundRecordedTexts cuts every free-text field in the record to its bound,
// saying in the field that it was cut. It is applied on both sides of the store.
//
// On write it is what makes the bound hold for every writer: a reason is often
// an error with a provider's or a forge's whole output folded into it, and a
// record the store refused for one over-long field is a record that never lands
// — a cleanup, a publication, or a stoppage nobody hears about. The writers that
// cut their own field first (RecordFailure and the rest) still do, because the
// in-memory copy they go on to carry elsewhere is not the one saved here.
//
// On read it is the tolerance half of bound on write, tolerate on read: a record
// written before one of these bounds existed can hold a field longer than
// Validate accepts, and refusing it on the way in would make that run unreadable
// rather than rendering the one field truncated. It is worse than one lost
// record: every scan over the store walks every file, so one old record the
// loader refuses is the whole history nobody can list.
//
// It touches only a field that is actually over its bound, so it is a no-op over
// every record already within its bounds. It writes through the record's nested
// pointers and lists, so a caller that does not own them uses
// withRecordedTextsBounded instead.
func (s *State) boundRecordedTexts() {
	for _, field := range s.recordedTexts() {
		if len(*field.text) > field.limit {
			*field.text = boundRecordedText(*field.text, field.limit, field.cutNote)
		}
	}
}

// withRecordedTextsBounded is the record with every free-text field cut to its
// bound, for a writer handed a record it does not own. A State passed by value
// still shares its nested records and lists with the caller, so cutting one in
// place would rewrite the caller's copy underneath it; an over-long record is
// copied whole first instead. A record within its bounds, which is nearly every
// one, is returned as it was.
func (s State) withRecordedTextsBounded() (State, error) {
	if !s.overBound() {
		return s, nil
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		return State{}, fmt.Errorf("copy run state to bound its text: %w", err)
	}
	var copied State
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return State{}, fmt.Errorf("copy run state to bound its text: %w", err)
	}
	copied.boundRecordedTexts()
	return copied, nil
}

// DirectivePause is the user directive a run stopped short for. It is recorded
// before the run returns, for the same reason a usage-limit deadline is: the
// pause has to survive the process, so a later invocation can tell a run that is
// waiting from one that was interrupted, and can resume it rather than start a
// second attempt at the same item.
//
// The directive itself lives in the product's own directive store, which is what
// makes it reachable from every process. What is copied here is only what a
// reader of this run needs in order to say what the run is waiting for without
// going and finding it: which directive, and what about it is unresolved.
type DirectivePause struct {
	DirectiveID string `json:"directive_id"`
	Kind        string `json:"kind"`
	Unresolved  string `json:"unresolved"`
}

// Validate rejects a recorded pause that cannot say what the run is waiting for.
func (d DirectivePause) Validate() error {
	var problems []error
	if strings.TrimSpace(d.DirectiveID) == "" {
		problems = append(problems, errors.New("directive_id is required"))
	}
	if strings.TrimSpace(d.Kind) == "" {
		problems = append(problems, errors.New("kind is required"))
	}
	// A pause nobody can name the reason for is a pause nobody can lift, which is
	// exactly the state enforcing directives exists to prevent.
	if strings.TrimSpace(d.Unresolved) == "" {
		problems = append(problems, errors.New("unresolved is required"))
	}
	return errors.Join(problems...)
}

// DependencyPause is the unfinished work a run stopped short for: the blocking
// dependencies its work item carried when the run last read it. It is recorded
// before the run returns, for the same reason a directive pause is — the pause
// has to survive the process, so a later invocation can tell a run that is
// waiting from one that was interrupted, and resume it rather than start a
// second attempt at the same item.
//
// The dependency graph itself lives in the tracker, which is what makes it
// reachable from every process. What is copied here is only what a reader of
// this run needs in order to say what the run is waiting for without going and
// finding it: the items it is waiting on.
type DependencyPause struct {
	Blockers []string `json:"blockers"`
}

// Summary names the work this pause is waiting on, in one line, for a reader who
// needs to know what to close rather than the whole dependency graph.
func (d DependencyPause) Summary() string {
	return strings.Join(d.Blockers, ", ")
}

// Validate rejects a recorded pause that cannot say what the run is waiting for.
// A pause nobody can name the blocker of is a pause nobody can lift, which is
// exactly the state enforcing dependencies exists to prevent.
func (d DependencyPause) Validate() error {
	if len(d.Blockers) == 0 {
		return errors.New("blockers is required")
	}
	for _, blocker := range d.Blockers {
		if strings.TrimSpace(blocker) == "" {
			return errors.New("every blocker must name the work item it waits on")
		}
	}
	return nil
}

// TrackerPause is a run parked because the tracker would not answer the read it
// makes at a gate boundary, for the whole of that boundary's recovery window. It
// is recorded before the run returns, for the reason a dependency pause is: the
// park has to survive the process, so a later invocation can tell a run that is
// waiting from one that was interrupted, and resume it rather than start a
// second attempt at the same item.
//
// It is a separate pause from the dependency one because what is being waited on
// differs. A dependency pause knows what the item waits for and is lifted by that
// work finishing; this is a run that could not find out, and is lifted by the
// tracker answering. Failing the run instead is what ended three runs in two
// days, two of them holding work a reviewer had already approved or a developer
// had already written.
type TrackerPause struct {
	// Boundary is the RetryDependencyRead-style name of the read that went
	// unanswered, so the record says which of a run's tracker reads this was.
	Boundary string `json:"boundary"`
	// Attempts and WaitedSeconds are what the window was spent on, carried here
	// rather than derived from the retries so a reader of the park sees what it
	// cost without walking the run's whole retry log.
	Attempts      int   `json:"attempts"`
	WaitedSeconds int64 `json:"waited_seconds"`
	// Failure is the last thing the tracker said, bounded like every other
	// recorded failure. It is what tells a person reading the park whether the
	// store was contended or broken.
	Failure string `json:"failure,omitempty"`
}

// Waited is how long the run spent asking before it parked.
func (t TrackerPause) Waited() time.Duration {
	return time.Duration(t.WaitedSeconds) * time.Second
}

// Summary names what the run is waiting for, in one line, for a reader who needs
// to know what to look at rather than the whole retry log.
func (t TrackerPause) Summary() string {
	summary := fmt.Sprintf("the tracker did not answer while %s: %d attempt(s) over %s",
		t.Boundary, t.Attempts, t.Waited().Round(time.Second))
	if strings.TrimSpace(t.Failure) != "" {
		summary += "; last failure: " + t.Failure
	}
	return summary
}

// Validate rejects a recorded park that cannot say what went unanswered. A park
// nobody can name the boundary of is one nobody can tell from a run that simply
// stopped, which is the whole thing this records against.
func (t TrackerPause) Validate() error {
	var problems []error
	if strings.TrimSpace(t.Boundary) == "" {
		problems = append(problems, errors.New("boundary is required"))
	}
	if t.Attempts <= 0 {
		problems = append(problems, errors.New("attempts must name at least one attempt that was made"))
	}
	if t.WaitedSeconds < 0 {
		problems = append(problems, errors.New("waited_seconds cannot be negative"))
	}
	if len(t.Failure) > MaxRetryFailureBytes {
		problems = append(problems, fmt.Errorf("failure is %d bytes, which exceeds the %d byte bound", len(t.Failure), MaxRetryFailureBytes))
	}
	return errors.Join(problems...)
}

// RedeployStop is a run the watch session hosting it stopped so that the
// session could restart into a build deployed over it: when, at which phase, how
// long the session had drained before it gave up waiting, and which session did
// it. The phase is recorded rather than read off the run because it is what the
// continuation is owed — a developer attempt in the same session, or the gate
// from its checks — and the bound is recorded because it is what a reader asks
// first about a run the harness stopped on its own clock.
type RedeployStop struct {
	At    time.Time `json:"at"`
	Phase Phase     `json:"phase"`
	// Bound is the drain limit that ran out, in seconds. It is the configured
	// execution.redeploy_drain_limit as the session read it.
	BoundSeconds int64 `json:"bound_seconds"`
	// SessionID is the watch session that stopped the run, so the run's record
	// and the watch log can be read against each other.
	SessionID string `json:"session_id,omitempty"`
}

// Bound is the drain limit that ran out.
func (r RedeployStop) Bound() time.Duration {
	return time.Duration(r.BoundSeconds) * time.Second
}

// Validate rejects a recorded stop that cannot say what the run is owed: a stop
// with no moment is one nothing can age, and one with no phase is one nothing
// knows how to continue.
func (r RedeployStop) Validate() error {
	var problems []error
	if r.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if !r.Phase.Valid() {
		problems = append(problems, fmt.Errorf("phase %q is not one a run reaches", r.Phase))
	}
	if r.BoundSeconds < 0 {
		problems = append(problems, fmt.Errorf("bound_seconds is %d, which is not a duration", r.BoundSeconds))
	}
	// The session is named by its identifier and nothing longer, so the field is
	// structured rather than free text a record would have to bound.
	if r.SessionID != "" {
		if err := domain.ValidateIdentifier("session_id", r.SessionID); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// MaxRepairContinuations bounds how many granted continuations one run's record
// may carry. What actually bounds them is the item's per-item grant cap, which
// refuses long before this; this is the record's own bound, so a budget somebody
// configured absurdly cannot grow a state file without limit.
const MaxRepairContinuations = 16

// RepairContinuation is one carried-out repair grant that re-entered this run's
// repair loop after it had already stopped. It is the run's half of a triage
// decision the item's durable counters record the other half of: the counters
// say what the development manager granted the item and whether the round cap
// cut it, and this says what was actually done with that grant here — how many
// attempts this run may now make, and the reasoning the harness was given when
// it was asked to continue.
//
// The blocker it superseded travels with it. Re-entry clears the run's standing
// blocker, because a run that is going again has not stopped and the docket,
// `yoyo status`, and reconciliation all read that field as the fact that it has;
// keeping the words here is what stops the clearing losing the evidence of what
// the run was stopped for.
type RepairContinuation struct {
	// GrantedAttempts is what this continuation added to the run's repair budget,
	// out of the rounds the item's record says triage granted it. Summed across
	// every run of one item it is what says how much of that grant has been
	// carried out, which is what stops one decision being acted on twice.
	GrantedAttempts int `json:"granted_attempts"`
	// Reason is the development manager's triage reasoning as the harness was
	// given it, which is why this run is going again. A continuation nobody can
	// account for is exactly the work that looks like it is happening behind
	// somebody's back.
	Reason      string    `json:"reason"`
	ContinuedAt time.Time `json:"continued_at"`
	// SupersededBlocker is the durable blocker this re-entry cleared, in the
	// words it was recorded in. It is absent on a re-entry that carried none,
	// such as a check-stage timeout.
	SupersededBlocker string `json:"superseded_blocker,omitempty"`
	// SupersededCheckFailure keeps the failure this continuation was handed,
	// including any earlier local promotion. It is history, not current check,
	// review, promotion, or cleanup credit for the continued change.
	SupersededCheckFailure *CheckFailure `json:"superseded_check_failure,omitempty"`
	// Returned says the round this continuation bought was environmentally
	// refused, so the grant it came out of was never actually spent on anything.
	// It is what keeps the attempts still counting toward this run's own budget —
	// the run did spend an attempt slot on the refusal — while leaving the item's
	// grant where it was, which is the two different questions the same number
	// answered before: what this run may still do, and what triage has already
	// handed the item. See environmental.go.
	Returned bool `json:"returned,omitempty"`
	// Stall says this continuation resumed the step the harness stopped, without
	// returning a new failure to its developer. Earlier repair input and the
	// attempt already charged survive; continuing that attempt is not counted
	// against the run, and this is what accounts for a record carrying a
	// continuation with no attempt beside it — which without it reads as a
	// counter somebody forgot to move. A decided continuation consumes its grant;
	// a ByHarness continuation grants and consumes none.
	Stall bool `json:"stall,omitempty"`
	// CheckStage says a decided repair continued checks the stage bound stopped.
	// The developer already finished, so this continuation counts no attempt.
	CheckStage bool `json:"check_stage,omitempty"`
	// ByHarness says nobody decided this continuation: it is the harness carrying
	// on a first silent-stream stall itself, which it does at most
	// MaxHarnessStallContinuations times for one run. It spends no grant, so it
	// records none, and it is always a stall.
	ByHarness bool `json:"by_harness,omitempty"`
}

// Validate reports every contract violation in the recorded continuation at once.
func (c RepairContinuation) Validate() error {
	var problems []error
	if c.CheckStage && (c.Stall || c.ByHarness) {
		problems = append(problems, errors.New("a decided check-stage continuation is neither a stall nor a harness grant"))
	}

	switch {
	case c.ByHarness && (!c.Stall || c.GrantedAttempts != 0):
		problems = append(problems, errors.New("a continuation the harness made itself carries on a stall and grants no repair attempt"))
	case !c.ByHarness && c.GrantedAttempts < 1:
		problems = append(problems, errors.New("a continuation grants at least one repair attempt"))
	}
	if strings.TrimSpace(c.Reason) == "" {
		problems = append(problems, errors.New("the triage reasoning this run was continued on is required"))
	}
	if len(c.Reason) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("reason is %d bytes, which exceeds the %d byte bound", len(c.Reason), MaxSelectionReasonBytes))
	}
	if c.ContinuedAt.IsZero() {
		problems = append(problems, errors.New("continued_at is required"))
	}
	if len(c.SupersededBlocker) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("superseded_blocker is %d bytes, which exceeds the %d byte bound", len(c.SupersededBlocker), MaxBlockerBytes))
	}
	if c.SupersededCheckFailure != nil {
		if err := c.SupersededCheckFailure.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("superseded_check_failure: %w", err))
		}
	}
	return errors.Join(problems...)
}

// The boundaries a run can meet a recoverable failure at, named here rather than
// where each one is retried so the record speaks one vocabulary. Each is a place
// a network drop killed a run outright before yoyodyne-ifd.264, and each stands
// for one recovery window: a run that spent its whole window pushing a branch
// still gets a fresh one at the merge, because the two are different failures
// however alike they read.
//
// The provider's is deliberately one boundary rather than two. The developer and
// the reviewer share the relaunch budget already, for the reason stated on
// TransientRelaunches, and a recovery window they did not share would let a run
// alternating between them absorb twice what one boundary is worth.
const (
	RetryPublishBranch      = "publishing the run branch"
	RetryOpenPullRequest    = "opening the pull request"
	RetryRepublishBranch    = "republishing the replayed run branch"
	RetryRemoteTarget       = "reading the remote target branch"
	RetryMerge              = "merging the pull request"
	RetryMergeConfirmation  = "confirming the merge"
	RetryDeleteRemoteBranch = "deleting the merged remote branch"
	RetryCatchUpTarget      = "catching the local target branch up"
	RetryProviderInvocation = "invoking the provider"
	// RetryTrackerWrite covers the writes a finishing run makes to the tracker —
	// the outcome recorded on the item, the closure, and the price. They are one
	// boundary rather than three for the reason the remote target's three call
	// sites are one: they are the same store reached the same way within one
	// step, and a `bd` that could not be run for one of them could not be run for
	// the next. A transient failure there recorded finished, integrated work as a
	// failed run, which is the same loss the forge boundaries carried and is why
	// the product manager joined them to this item's set.
	RetryTrackerWrite = "writing to the tracker"
	// RetryTracker is the same boundary met from a conversation rather than from
	// a run: every call a role's conversation makes to the tracker, the reads
	// that gate its writes and the writes themselves. It is one boundary rather
	// than a read and a write one for the reason the run's three writes are one —
	// a `bd` that could not be run for the read could not be run for the write —
	// and for one more: a write that spent the window is read back afterwards
	// under a context nothing can cancel, and that read has to find the window
	// already spent rather than a fresh one to wait out. It is recorded on the
	// conversation's event log rather than on a run's state, since a
	// conversation has no run, and it is named here so the record speaks one
	// vocabulary wherever a wait was taken.
	RetryTracker = "reaching the tracker"
	// RetryDependencyRead is the tracker read a run makes at each of its gate
	// boundaries to find out what its work item waits on: before the claim or the
	// resume, at the start of every repair round, and once more before the
	// promotion. It is a boundary of its own rather than part of the write above
	// because it is met at a different moment and by a different kind of run — the
	// writes are what a finishing run makes, and this is what a run that is still
	// working asks before it may take another step.
	//
	// It is one boundary rather than three call sites for the reason the tracker
	// writes are one: the same store reached the same way, where a `bd` that could
	// not be run for the round's read could not be run for the promotion's either.
	// Three runs died on it in two days — yoyodyne-ifd.436.4 with its change
	// already approved, yoyodyne-ifd.117.1 with its files already lifted — each on
	// one `bd show` that timed out under load and would have answered on the next
	// attempt.
	RetryDependencyRead = "reading what this item waits on"
)

// MaxRetries bounds how many recoverable failures one run records. The window
// and the interval cap already bound them to about twenty per boundary, so this
// is a backstop against a boundary retried in a loop nobody meant to write
// rather than a budget: a run that reaches it has stopped being a run whose
// record anybody can read.
const MaxRetries = 256

// MaxRetryFailureBytes bounds what one retry keeps of the failure it waited out.
// It is the message that says why, not the record of the failure itself — the
// run's event stream carries that whole — and a hundred of these in one state
// file is what the bound exists for.
const MaxRetryFailureBytes = 512

// Retry is one recoverable failure a run waited out and asked again. It is the
// evidence half of the operator's rule: a retry nobody can see is a run that
// looks like it simply took longer, and the reason four runs' worth of lost work
// took a day to diagnose is that nothing anywhere said a connection had been
// reset.
type Retry struct {
	// Boundary is where the failure happened, from the vocabulary above.
	Boundary string `json:"boundary"`
	// Attempt is which retry at that boundary this was, counted from 1. It is
	// what the interval was derived from, so a reader can check the backoff
	// against the record rather than believing it.
	Attempt int `json:"attempt"`
	// DelaySeconds is how long the run waited before asking again. It is recorded
	// rather than recomputed because the series is the harness's and may change,
	// and a record that had to be read against the build that wrote it is not a
	// record.
	DelaySeconds int64 `json:"delay_seconds"`
	// At is when the failure was met, which is also when the wait was committed.
	At time.Time `json:"at"`
	// Failure is what failed, bounded. It is the provider's or the forge's own
	// words rather than a class the harness assigned, because which of them it
	// was is exactly what somebody reading a run afterwards wants.
	Failure string `json:"failure"`
}

// Delay is the recorded wait as a duration.
func (r Retry) Delay() time.Duration {
	return time.Duration(r.DelaySeconds) * time.Second
}

// Validate reports every contract violation in one recorded retry at once.
func (r Retry) Validate() error {
	var problems []error
	if strings.TrimSpace(r.Boundary) == "" {
		problems = append(problems, errors.New("the boundary a retry happened at is required"))
	}
	if r.Attempt < 1 {
		problems = append(problems, errors.New("attempt is counted from 1"))
	}
	if r.DelaySeconds < 0 {
		problems = append(problems, errors.New("delay_seconds cannot be negative"))
	}
	if r.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if len(r.Failure) > MaxRetryFailureBytes {
		problems = append(problems, fmt.Errorf("failure is %d bytes, which exceeds the %d byte bound", len(r.Failure), MaxRetryFailureBytes))
	}
	return errors.Join(problems...)
}

// Integration is the durable evidence of a completed promotion: exactly which
// commit the harness created and which commit the target moved from and to.
type Integration struct {
	TargetBranch         string `json:"target_branch"`
	SourceCommit         string `json:"source_commit"`
	TargetCommit         string `json:"target_commit"`
	PreviousTargetCommit string `json:"previous_target_commit"`
	// ThroughPullRequest records a promotion onto a target branch the forge
	// protects, which never moves the local target: the change lands only by the
	// forge merging its pull request, and TargetCommit names the commit being
	// landed rather than where the local target stands. Absent is a local
	// promotion, which every run recorded before this was.
	ThroughPullRequest bool `json:"through_pull_request,omitempty"`
}

// ReconcileStep names the settlement whose refusal is recorded on a run.
type ReconcileStep string

const (
	ReconcileRun         ReconcileStep = "run"
	ReconcilePublication ReconcileStep = "publication"
	ReconcileRecovery    ReconcileStep = "publication-recovery"
	ReconcileRefresh     ReconcileStep = "publication-refresh"
	ReconcileBranch      ReconcileStep = "branch"
	ReconcileWorktree    ReconcileStep = "worktree"
	ReconcileSuperseded  ReconcileStep = "superseded-publication"
	ReconcileRedTarget   ReconcileStep = "red-target"
	ReconcileEscalation  ReconcileStep = "escalation"
)

var reconcileSteps = []ReconcileStep{ReconcileRun, ReconcilePublication, ReconcileRecovery, ReconcileRefresh, ReconcileBranch, ReconcileWorktree, ReconcileSuperseded, ReconcileRedTarget, ReconcileEscalation}

// RecordReconcileProblem uses the same bound and cut marker as the nested
// durable finding, so comparing a repeated refusal is stable after a save.
func RecordReconcileProblem(problem string) string {
	return boundRecordedText(problem, MaxBlockerBytes, truncatedNote(MaxBlockerBytes))
}

// ReconcileFinding is a settlement this run could not make. Pending means its
// note still needs delivery to the work item. Separate steps keep one refusal
// from replacing another on the same run.
type ReconcileFinding struct {
	Step    ReconcileStep `json:"step"`
	Problem string        `json:"problem"`
	Pending bool          `json:"pending,omitempty"`
}

type State struct {
	SchemaVersion int              `json:"schema_version"`
	RunID         string           `json:"run_id"`
	ProductID     domain.ProductID `json:"product_id"`
	RepositoryID  string           `json:"repository_id"`
	WorkItemID    string           `json:"work_item_id"`
	// WorkItemTitle is what the item is called, written with the run because the
	// claim is where the harness has the tracker's answer in hand and everything
	// reading the record afterwards does not. It is what lets a surface name the
	// work in words a person reads rather than in an identifier they would have to
	// resolve, and it is a copy rather than a reference on purpose: the record says
	// what the item was called when the run started, which is what an account of
	// that run should say however the item is renamed later. Absent means nothing
	// recorded a title, which is what every run written before this did.
	WorkItemTitle string `json:"work_item_title,omitempty"`
	// WorkItemLabels is the tracker's labels on the item, written with the run
	// for the reason the title is: the claim is where the harness has the
	// tracker's answer in hand, and what reads the record afterwards reads only
	// the record. It is what lets the standing status say which developer slot a
	// run occupies — a slot that prefers a label holds the runs over work carrying
	// it — without asking the tracker, and it is a copy on purpose: the record says
	// what the item carried when the run started, which is what it was pulled as.
	// Absent means nothing recorded labels, which is what every run written before
	// this did, and such a run reads as one over unlabelled work.
	WorkItemLabels []string `json:"work_item_labels,omitempty"`
	// WorkItemClaimedAt is when this run took its work item, and is absent on a
	// run that never got that far.
	//
	// It is here because the claim is the first thing a run changes outside
	// itself, and until yoyodyne-ifd.338 nothing recorded which side of it a run
	// died on. A run that fails after claiming leaves a branch, a worktree, and an
	// item it holds; a run that fails before claiming leaves none of those, so
	// every rule the harness uses to decide that a failure is worth somebody's
	// attention — a durable blocker, a preserved change — reads it as nothing
	// having happened. Twenty-nine dispatches of yoyodyne-ifd.285 died at the
	// claim in twenty hours and not one of them reached a surface anybody reads.
	//
	// Absent means nothing recorded a claim, which is what every run written
	// before this did as well as every run that really never claimed. That
	// ambiguity is why it is only ever asked where the death happens, and never by
	// the scan that walks the recorded history; see orchestrator.unstartedRun.
	WorkItemClaimedAt *time.Time `json:"work_item_claimed_at,omitempty"`
	// Selection is why the harness is running this item: who chose it and on
	// what grounds. It is written when the run is reserved and never rewritten.
	// Absent means nothing accounted for the choice, which is not the same as a
	// choice with no reason and is reported as such.
	Selection *Selection `json:"selection,omitempty"`
	// LiftedCommit is the commit of an earlier run's preserved branch this run
	// was started from, where its selection asked for one and the lift was made:
	// what that branch carried past the target was applied to this run's worktree
	// before its developer was invoked. It is absent on every other run.
	LiftedCommit string         `json:"lifted_commit,omitempty"`
	Backend      domain.Backend `json:"backend"`
	// AccountAlias is the provider account this run's agents ran under, named by
	// the alias the configuration gives it. It is written when the run is reserved
	// and never rewritten, because it is a fact about what was spent rather than
	// about what is configured now: one account is what the harness runs today, so
	// what this buys is that every record already says which — and the day there
	// is a second account, nothing written before it has to be guessed at.
	//
	// Absent means nothing recorded an account, which is what every run written
	// before this did.
	AccountAlias string `json:"account_alias,omitempty"`
	// ConfigRevision identifies the configuration in force when this run was
	// started: a digest of every effective value, so two runs carrying one
	// revision were configured identically and a run whose configuration was
	// edited under it is distinguishable from one that was not. Like the account,
	// it is written once and never rewritten — a run resumed by a later process
	// keeps the revision it was set up under, which is what makes it evidence
	// about this run rather than a reading of whatever the file says now.
	//
	// Absent means nothing recorded a configuration, which is what every run
	// written before this did.
	ConfigRevision string `json:"config_revision,omitempty"`
	// Build is the repository revision the harness binary that reserved this run
	// was built from. It is written with the account and the configuration and
	// never rewritten, for the same reason and for one of its own: a run picked up
	// again by a later process still says which build started it, which is what an
	// account of what that run did has to be able to answer.
	//
	// It is here because a process goes on running whatever it was started with
	// while the harness moves on underneath it, and nothing else in a run's record
	// says which of the two dispatched it. That gap is not hypothetical: four
	// repair dispatches in the week of 2026-08-27 were made after the refusal that
	// should have turned each of them away had merged, by a resident scheduler
	// nobody could show was running the merged code, and the diagnosis had to stop
	// there because no record could name the binary. Most of that week's code
	// defects were deployment defects and nothing could tell the two apart.
	//
	// Absent means nothing recorded a build, which is what every run written
	// before this did and what a binary carrying no revision of its own produces —
	// a comparison nobody can make, rather than a run that is current.
	Build string `json:"build,omitempty"`
	// WorkflowInstanceID is the workflow instance this run is observed through,
	// and is what tells a run on the declarative path from a run on the legacy
	// one: a run carrying one records a position in the built-in delivery
	// definition beside everything else it records, and a run carrying none is
	// executing the same sequence with nothing watching it. It is written when
	// the run is created and never afterwards, which is what makes the path a
	// property of the run rather than of the configuration a later process reads:
	// a run started before the declarative path was the default, or by a project
	// that had rolled back to the legacy one, stays a legacy run for the whole of
	// its life, however many processes serve it.
	//
	// The instance itself lives beside this record in the same store, under this
	// identifier. Nothing about the run depends on it: it is an observation, and
	// a run whose instance could not be created or stepped delivers exactly as it
	// would have.
	WorkflowInstanceID string `json:"workflow_instance_id,omitempty"`
	// WorkflowDivergence is why this run stopped stepping that instance — the
	// definition sent the run somewhere it did not go, refused an outcome it
	// produced, or could not be stepped at all. It is what the observation is
	// for: a run carrying one is a run somebody has to read before the definition
	// is trusted to decide anything, which is still ahead of it. Absent means the
	// instance and the run agreed at every boundary the run reached.
	WorkflowDivergence string `json:"workflow_divergence,omitempty"`
	// WorkflowUnobserved is why this run has no instance although its project
	// asked for one: the definition could not be read, or the instance could not
	// be created. It is the other way a run comes to carry no
	// WorkflowInstanceID, and the two are told apart here because they are not
	// the same fact. A project that rolled back records neither and is a legacy
	// run; a run that was to be observed and is not carries this, and is a run
	// nothing watched.
	//
	// Without it the two are one absent field, and the silence is read as the
	// harmless one. That is not hypothetical: a run whose trial failed to start
	// was recorded as the baseline of a delivery path, agreed with three
	// consecutive full checks, and began failing only when the same code
	// reliably produced an instance. It says nothing about the work — a run
	// nothing observed delivers exactly as it would have — but a count of runs
	// the definition agreed with must not include it, and a count reading an
	// absent divergence is exactly what would.
	WorkflowUnobserved string `json:"workflow_unobserved,omitempty"`
	// ProviderSessionID is the developer session. The reviewer's session is
	// recorded separately because the two are always distinct invocations.
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	// ProviderModel is the selector the developer invocation requested and
	// ProviderResolvedModel is what the provider reported serving it. A
	// floating alias makes the resolved identifier the only real audit record.
	ProviderModel         string `json:"provider_model,omitempty"`
	ProviderResolvedModel string `json:"provider_resolved_model,omitempty"`
	// ProviderEffort is the effort level the developer invocations ask for,
	// settled from the developer agent's configuration when the run is reserved
	// and read back by every invocation after, as the account is. Empty is a
	// developer agent that configured none, and every run recorded before the
	// level was configurable; either way the provider resolved its own.
	ProviderEffort string `json:"provider_effort,omitempty"`
	// EffortSettled says ProviderEffort was settled when this run was reserved,
	// so an empty one means the developer agent named no level then rather than
	// that the run predates the field. It is what keeps an edit to the level from
	// reaching a run already in flight.
	EffortSettled bool `json:"effort_settled,omitempty"`
	// DeveloperModel is the selector execution.developer_models chose for this
	// run from the labels its item carried, and DeveloperModelReason is why that
	// entry rather than another or than none. They are settled once, when the run
	// is reserved, and read back off the record by every developer invocation the
	// run goes on to make — the first attempt, each repair, and anything a later
	// process resumes — for the reason the account alias is: a run that resolved
	// the mapping again per invocation would split one piece of work across two
	// models the first time the file was edited under it.
	//
	// Both are absent on a run whose project configured no mapping, which reads
	// as the developer's configured model and is what every run written before
	// this did. The reason is recorded even where the item was unmapped, because
	// an unmapped item and a mapping nobody read are two different accounts of
	// one model and only the record can tell them apart.
	DeveloperModel       string     `json:"developer_model,omitempty"`
	DeveloperModelReason string     `json:"developer_model_reason,omitempty"`
	Status               Status     `json:"status"`
	Phase                Phase      `json:"phase,omitempty"`
	LastSequence         uint64     `json:"last_sequence"`
	StartedAt            time.Time  `json:"started_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
	// SettledQuietSince is set only where the harness ended a run whose own
	// process was already gone: `yoyo reconcile` settling a run nothing was
	// carrying, or the claim audit cancelling a dead claim. It is the moment the
	// run's record last moved before that settlement overwrote UpdatedAt, which
	// is the last moment the run demonstrably held its slot. CompletedAt on such a
	// run is when somebody noticed rather than when the run ended, and the stall
	// reading dates the silence from this instead, so a settlement is never read
	// as activity (see readmodel.LastHeld).
	SettledQuietSince *time.Time `json:"settled_quiet_since,omitempty"`
	WorktreePath      string     `json:"worktree_path,omitempty"`
	Branch            string     `json:"branch,omitempty"`
	BaseCommit        string     `json:"base_commit,omitempty"`
	// HarnessCommit is the last commit the harness itself made in this run's
	// worktree, which publishing needs before it can push a branch. It is durable
	// because it is what permits the worktree's HEAD to have moved: a resumed run
	// that could not name the commit it made would either refuse its own work or
	// have to accept whatever it finds, and the second is how an agent's commit
	// gets promoted.
	HarnessCommit string `json:"harness_commit,omitempty"`
	// WorktreeRemoved and BranchRemoved record the two cleanup steps
	// separately, because they cannot be performed atomically. Recording them
	// apart is what lets an interrupted cleanup be resumed and what keeps a
	// preserved-artifact claim truthful.
	WorktreeRemoved bool `json:"worktree_removed,omitempty"`
	// CheckoutRestorePending is written before a repair or automatic check
	// continuation restores a missing checkout and cleared only after the whole
	// checkout has been verified.
	// A process interrupted during Git's add cannot pass a partial directory
	// off as the preserved worktree on its next invocation.
	CheckoutRestorePending bool `json:"checkout_restore_pending,omitempty"`
	BranchRemoved          bool `json:"branch_removed,omitempty"`
	// ArtifactsRetiredBy names the run that superseded this one, on a run whose
	// artifacts were retired by triage rather than cleaned up after a promotion
	// of its own. It is the second way a removal is earned, and the reason it is
	// recorded rather than inferred: a stopped run integrates nothing, so without
	// it a removal on one would be a claim with no evidence behind it — which is
	// exactly what the rule below refuses.
	//
	// Absent is every run whose artifacts it removed itself, which is all of them
	// until triage retires one.
	ArtifactsRetiredBy string `json:"artifacts_retired_by,omitempty"`
	// WorktreeSweptAt is when the convergence sweep retired this run's checkout
	// as hygiene, on a settled run that neither promoted anything nor was
	// superseded. It is the third way a removal is earned and it covers the
	// checkout alone, because retiring a checkout never touches a branch: what
	// that checkout carried in commits is still on the branch, and what it held
	// uncommitted is on PreservedWorkRef below, so nothing about the run stops
	// being recoverable. Deleting the branch is earned separately, by
	// BranchSweptAt.
	//
	// It is recorded for the reason the other two are. `yoyo status`, the triage
	// docket, and a re-run all read WorktreeRemoved as the answer to whether the
	// directory is still there, and a removal nothing wrote down leaves every one
	// of them sending somebody after a checkout that is gone.
	//
	// Absent is every run whose checkout the sweep has not taken, which is all of
	// them while it is still within the tail the sweep holds back.
	WorktreeSweptAt *time.Time `json:"worktree_swept_at,omitempty"`
	// BranchSweptAt is when the convergence sweep deleted this run's branch as
	// hygiene, on a settled run whose work the target branch provably carries. It
	// is the fourth way a removal is earned and it covers the branch alone, for
	// the reason the sweep is allowed to make it at all: containment is proved in
	// the repository before the deletion, so what the branch held is in the target
	// and the deletion loses nothing.
	//
	// It is recorded because the alternative is the claim this field exists to
	// stop. The sweep deleted branches for weeks and wrote nothing down, so every
	// run it swept went on reading as one whose change was preserved on a branch —
	// `Artifacts.Preserved()` asks BranchRemoved and nothing else — and run
	// run-48216ea9 was still advertising a branch and a worktree that were both
	// gone when a developer went looking for its work.
	//
	// It dates a branch the sweep found gone as well as one it deleted, as
	// WorktreeSweptAt does and for the same reason: the branch is gone either way,
	// and only one of the two answers stops sending somebody after it.
	//
	// Absent is every run whose branch the sweep has not deleted, which is every
	// run whose work the target does not carry and every run that cleaned up after
	// its own promotion.
	BranchSweptAt *time.Time `json:"branch_swept_at,omitempty"`
	// ReleaseCorrectedAt is when the convergence sweep told this run's work item
	// that the claim audit had given it back without saying the run's change was
	// still on its branch or in its checkout. A release note written before the
	// audit looked in the repository said only that nothing was working on the
	// item, and on 2026-09-23 that was read as run-838ffc48 having preserved
	// nothing while its branch held the approved change. It is here so the
	// correction is made once rather than on every sweep.
	ReleaseCorrectedAt *time.Time `json:"release_corrected_at,omitempty"`
	// EscalationEnded is when and why the development manager's escalation of this
	// run's stoppage to the operator stopped being one, written by the reconcile
	// sweep once it has told the run's work item. An escalation is a finding on the
	// operator's line while it is the decision standing on the item's latest
	// stopped run, and until yoyodyne-ifd.428.54 nothing about the item being
	// parked or the run's change being gone ended it: run-95b34031's escalation
	// stood on the line for two days after the Lead Product Manager parked its
	// item and its branch and worktree were both gone. It is here so the item is
	// told once, and so a surface that cannot ask the tracker or the repository —
	// the channel — reads the escalation as over.
	EscalationEnded *EscalationEnding `json:"escalation_ended,omitempty"`
	// PreservedWorkRef names the ref carrying whatever this run left uncommitted
	// in its checkout, written when the sweep retired that checkout and had
	// something to move out of it first. It is deliberately not a branch: a branch
	// would be swept, listed, and considered by every containment proof the
	// harness makes, and this is none of those things — only a garbage-collection
	// root and an answer to where the work went.
	//
	// This record is the only place that answer lives. A stopped run's
	// half-finished change is exactly what somebody comes looking for months
	// later, and nothing else in the repository connects the ref to the item it
	// belonged to.
	//
	// Absent is every run whose checkout held nothing to move, and every run whose
	// checkout is still there.
	PreservedWorkRef string `json:"preserved_work_ref,omitempty"`
	// PreservedWorkNotedAt records delivery of the retired checkout's recovery
	// note. Until it is set, convergence retries the note even though the
	// checkout itself no longer needs retiring.
	PreservedWorkNotedAt *time.Time `json:"preserved_work_noted_at,omitempty"`
	// TargetBranch is the integration target fixed when the worktree was
	// created. It is durable so a resumed run promotes the work into the branch
	// it was written against rather than whatever happens to be checked out
	// when the run is picked up again.
	TargetBranch        string `json:"target_branch,omitempty"`
	ReviewSessionID     string `json:"review_session_id,omitempty"`
	ReviewModel         string `json:"review_model,omitempty"`
	ReviewResolvedModel string `json:"review_resolved_model,omitempty"`
	// ReviewEffort is the effort level the review asked the provider for, and
	// empty where the reviewer agent configured none.
	ReviewEffort string `json:"review_effort,omitempty"`
	// ReviewBaseCommit and ReviewHeadCommit are the two commits the change the
	// reviewer was shown was measured between: the base it was cut from, and
	// the branch's tip at the moment of the review, with the uncommitted
	// worktree above it. They are recorded so what a verdict was judged against
	// can be read back as two commits, rather than reconstructed from the branch
	// and a patch byte count — which is how nine hedged verdicts had to be
	// reconstructed once. They are cleared with the rest of the review evidence
	// when the next attempt is judged, and they are empty on every record
	// written before they were carried.
	ReviewBaseCommit string `json:"review_base_commit,omitempty"`
	ReviewHeadCommit string `json:"review_head_commit,omitempty"`
	ReviewDecision   string `json:"review_decision,omitempty"`
	// ReviewApproves is what the reviewer said its approval approves: the work the
	// item asked for, or evidence that does not discharge it. It is durable for the
	// reason the landing claim below is — the closure is not always made by the
	// process that read the verdict — and it is empty on a repair, which closes
	// nothing, and on every run recorded before an approval said which it was.
	ReviewApproves string `json:"review_approves,omitempty"`
	// ReviewSummary is what the reviewer said about the change, in its own words,
	// and it is what every surface prints where it answers "what did the review
	// say". It is bounded like every other recorded reason, and RecordReviewSummary
	// is how it is written: a reviewer writes at whatever length it likes, and a
	// summary nothing downstream can carry is a stopped run that validates here and
	// is then refused by the docket entry that would have told the development
	// manager about it.
	ReviewSummary  string `json:"review_summary,omitempty"`
	ReviewFindings int    `json:"review_findings,omitempty"`
	// DeveloperSummary is absent on older runs, on invocations without a final
	// account, and while a new developer invocation is in flight. Review only
	// receives it when both its attempt and content still match the candidate.
	DeveloperSummary *DeveloperSummary `json:"developer_summary,omitempty"`
	// LandingOutcome is what the developer claimed its change does to the work
	// item, and LandingReason is its own account of the claim. They are durable
	// because the closure is not always made by the process that read them: a run
	// whose merge the forge only queued is closed by a later sweep, and a sweep
	// deciding from integration alone is exactly the closure this record exists
	// to stop.
	//
	// An empty outcome is the ordinary landing, which is what every run recorded
	// before this channel existed carries and what a reply claiming nothing
	// leaves.
	LandingOutcome string `json:"landing_outcome,omitempty"`
	LandingReason  string `json:"landing_reason,omitempty"`
	// LandingBlockedBy is the impediment a landing named to have its item left
	// open rather than parked, as the work item the item now waits on. It is
	// durable for the same reason the outcome is — the sweep that settles a queued
	// merge decides where the item goes, and a sweep that could not read the
	// marker would park an item whose landing asked for the dependency — and it is
	// empty for the parking default and for every landing that discharges.
	//
	// It holds a marker the harness resolved against the tracker rather than one
	// the developer wrote: a marker naming work the tracker does not have is one
	// the dependency write would be refused for, on a run whose change is already
	// integrated. What became of an unusable one is the field below.
	LandingBlockedBy string `json:"landing_blocked_by,omitempty"`
	// LandingImpedimentProblem is why a marker the landing carried was not one the
	// item could be made to wait on. The two fields are exclusive: a marker that
	// resolved is above, and one that did not is here with the item parked
	// instead.
	//
	// It is recorded rather than discarded because a developer that named an
	// impediment asked for something, and an item parked with no trace of the
	// request reads afterwards as a developer that asked for nothing. It is the
	// operator's line, not the developer's: the run has ended by the time anybody
	// reads it.
	LandingImpedimentProblem string `json:"landing_impediment_problem,omitempty"`
	// LandingProblem names a claim that arrived and could not be read. It is
	// separate from the outcome because the two say different things: no claim is
	// a developer that made none, and an unreadable one is a developer that tried
	// to. Only the second can be a claim that the item is not discharged, so it
	// withholds the closure — an item left open is something a person can settle,
	// and the false closure it would otherwise be is what nobody can see.
	LandingProblem string `json:"landing_problem,omitempty"`
	// ReviewFindingDetails carries the findings themselves. It is a separate key
	// from the ReviewFindings count, which predates it and still means what it
	// always did, so a state file written before the repair loop existed keeps
	// decoding unchanged.
	ReviewFindingDetails []Finding `json:"review_finding_details,omitempty"`
	// ReviewRounds counts the reviews this run obtained a verdict from, whichever
	// way each of them went. It is not the same thing as the repair attempts
	// below and cannot be derived from them: a repair attempt handed back a
	// refused path or a failing check never reaches a reviewer, and an approved
	// change was reviewed without any repair at all. It is cumulative rather
	// than descriptive of the current attempt, so unlike every other piece of
	// review evidence it survives an attempt being cleared — what triage measures
	// against its configured cap is how many rounds the work has taken in total,
	// across repairs and across runs, and a counter reset by the next attempt
	// would answer a different question every time it was read.
	ReviewRounds int `json:"review_rounds,omitempty"`
	// CheckFailure carries the failing local or forge check a repair attempt was
	// handed. It and ReviewFindingDetails are the two kinds of repair input, and
	// at most one of them describes the current attempt: the checks are re-run
	// after every attempt, so recording a failing check clears findings that
	// describe a change the gate has already moved past, and passing checks
	// clear the failure.
	CheckFailure *CheckFailure `json:"check_failure,omitempty"`
	// ChecksPassed is the other answer the checks give, kept for the opposite
	// reason: it is what the promotion has to find on the record before it moves
	// the target branch, bound to the content and the attempt it is about to
	// promote. Recording a failing check clears it, and recording it clears the
	// failure, so at most one of the two describes the current attempt.
	ChecksPassed *ChecksPassed `json:"checks_passed,omitempty"`
	// PathRefusal carries the protected paths the gate refused before any check
	// ran. It is the third kind of repair input and behaves as the other two do:
	// at most one of the three describes the current attempt, and because this
	// gate is decided before the checks, recording a refusal clears both of the
	// others rather than competing with them for the next attempt.
	PathRefusal *PathRefusal `json:"path_refusal,omitempty"`
	// Verification carries what the developer recorded executing — the probe it
	// ran before it changed anything, and what it ran against the change. It is
	// the fourth kind of repair input and behaves as the three above do: a record
	// that does not meet the bar is handed back before any check runs, so
	// recording one clears the others rather than competing with them.
	//
	// Unlike them it is also kept when it owes nothing, because then it is the
	// evidence rather than the complaint: it is what the reviewer is shown beside
	// the change, and what says afterwards that somebody executed this before it
	// was handed over.
	Verification *Verification `json:"verification,omitempty"`
	// CheckStage is the current attempt's check stage: when it began, the bound
	// it runs under, which check it is on, and what it spent. It is not repair
	// input and is handed to nobody; it is what a surface reads to say "checks:
	// 14m of 30m" while the stage runs, and what says afterwards that a stage
	// was stopped at its bound rather than by a check that failed.
	CheckStage *CheckStage `json:"check_stage,omitempty"`
	// LandingChecks is what the landing checks made of the commit this run
	// integrated. It is written after the run is terminal and changes nothing
	// the run recorded about itself: a red landing is news about the target
	// branch, reported and filed as its own work, never a verdict on this run.
	LandingChecks *LandingChecks `json:"landing_checks,omitempty"`
	// RefusedAmendments are the changes agents on this run proposed that the
	// harness could not record, each with the role that proposed it, waiting to be
	// put in front of that role. It is not a fourth kind of repair input and
	// competes with none of the three: a refused proposal costs the run nothing and
	// buys no attempt, it rides along with whatever prompt that role was going to
	// be sent next, and the reply that is shown it drops what it was shown.
	//
	// It is durable because the refusal used to reach the operator and nobody
	// else. A developer that named a document the repository does not record was
	// never told, and went on to write into a checked-in file that it had raised a
	// proposal nothing was holding — a false claim that outlived the run, which
	// only `yoyo amendment list` disproved.
	RefusedAmendments []AmendmentRefusal `json:"refused_amendments,omitempty"`
	// Amendments is every proposal this run's agents made, in the order they were
	// made: each one raised with the id it was recorded under, and each one
	// dropped as a restatement with the raised proposal it was folded into and
	// the likeness it was folded on. It is the memory a restatement is compared
	// against, read back by whichever process continues the run, and it is the
	// record of a drop — which nothing else keeps, since a dropped proposal
	// reaches neither the amendment log nor the channel's problems.
	Amendments []RunAmendment `json:"amendments,omitempty"`
	// ReportProblem and AmendmentProblem are the run's whole account of what its
	// two side channels could not keep: every report that could not be read or
	// collected, and every proposal that could not be read or recorded, each in
	// the harness's own words and accumulated across the run's attempts. They
	// are the durable twins of the two fields the outcome carries, and they are
	// on the record because the outcome is printed once and gone — a refusal
	// written only there was, after the fact, indistinguishable from a proposal
	// never made, and one run's three lost proposals went unnoticed for four runs
	// on exactly that account. Unlike RefusedAmendments above, nothing spends
	// these: they are what an auditor reads, not what an agent is shown.
	ReportProblem    string `json:"report_problem,omitempty"`
	AmendmentProblem string `json:"amendment_problem,omitempty"`
	// ContextTruncation is what the work item's own notes lost to the context
	// budget when this run's context was assembled. It is not repair input and
	// nothing is handed back for it: the run proceeds exactly as it would have,
	// on an item saying slightly less than the tracker holds. Absent is every run
	// whose item fitted whole, which is nearly all of them.
	ContextTruncation *ContextTruncation `json:"context_truncation,omitempty"`
	// StaleBlockClear is what became of the stale blocked status the claim
	// cleared on its way to this run's item. Absent is every claim that met no
	// stale status, which is nearly all of them; present on a run that died at
	// the claim, it is what says the clear was never confirmed and the item was
	// left for the next pull.
	StaleBlockClear *StaleBlockClear `json:"stale_block_clear,omitempty"`
	// RepairAttempts counts the repair attempts already handed back to the
	// developer, whichever kind of failure triggered them: one budget covers
	// both, so it bounds the developer invocations a run can make rather than
	// bounding each trigger separately. It is recorded before each attempt
	// starts, so an interrupted run resumes at the attempt it reached and a
	// restart cannot buy the run a fresh budget.
	RepairAttempts int `json:"repair_attempts,omitempty"`
	// RepairContinuations are the grants of further repair attempts triage has
	// used to re-enter this run's repair loop after it stopped. They add to the
	// configured budget rather than replacing it, so the count above stays what it
	// always was — every attempt this run has handed back — and what bounds it is
	// read from the two together. Absent is every run nothing continued, which is
	// all of them until triage does.
	RepairContinuations []RepairContinuation `json:"repair_continuations,omitempty"`
	// Environmental is why this run's round delivered nothing, where the answer
	// is the environment rather than the work: a worktree that held none of the
	// change, a checkout the harness does not own, a sandbox that could not be
	// entered, a build older than the decision it carried out. It is what a round
	// is classified environmental by at settle, which is what stops the harness's
	// own failures spending the item's budgets. Absent is every run nothing
	// refused, which is nearly all of them. See environmental.go.
	Environmental *EnvironmentalRefusal `json:"environmental,omitempty"`
	// IntegrationStop is the environment having stopped this run after its change
	// was approved and before that change was promoted: a dirty primary checkout,
	// a tracker or a forge that did not answer, a network that went away. It is
	// what says the run is resumable at the step it stopped in with its approval
	// standing, and it is written where the run fails, from the error that ended
	// it — a dirty checkout by its sentinel, a transport that did not answer by
	// the recovery package's closed reading — so nothing decides it from the
	// run's prose afterwards. Absent is every run
	// the environment did not stop there, which is nearly all of them. See
	// integrationresume.go.
	IntegrationStop *IntegrationStop `json:"integration_stop,omitempty"`
	// ReplayConflict is this run's approved change having conflicted when it was
	// replayed onto what its target became: the one stop after an approval that
	// is a person's to settle rather than the harness's to resume past. It is
	// written where the conflict is decided, before the blocker about it is
	// attempted on the tracker, so the record says what stopped the run even when
	// that write fails — and a record carrying it is never an integration stop.
	// Absent is every run whose replay did not conflict, which is nearly all of
	// them. See integrationresume.go.
	ReplayConflict *ReplayConflict `json:"replay_conflict,omitempty"`
	// IntegrationResumptions are the continuations of this run's integration
	// after an environmental stop: the run made live again at the promotion, with
	// its approval standing and no attempt, round, or grant charged. Absent is
	// every run nothing resumed, which is nearly all of them.
	IntegrationResumptions []IntegrationResumption `json:"integration_resumptions,omitempty"`
	// SweepContinuations are the times the reconcile sweep continued this run
	// after the process serving its usage-limit wait had exited on the
	// in-process bound: the wait served by the sweep rather than by a person
	// typing `yoyo run`, with no attempt, round, or grant charged. Absent is
	// every run the sweep never continued, which is nearly all of them. See
	// sweepcontinue.go.
	SweepContinuations []SweepContinuation `json:"sweep_continuations,omitempty"`
	// CheckStageContinuations are the times the harness continued this run at
	// its checks after execution.check_stage_timeout stopped the stage, on the
	// change it already had and with no attempt, round, or grant charged. Absent
	// is every run the bound never stopped, which is nearly all of them. See
	// checkstagecontinue.go.
	CheckStageContinuations []CheckStageContinuation `json:"check_stage_continuations,omitempty"`
	// CheckStageContinuationRefused is why the harness declined to continue a
	// stage the bound stopped, where what declined it is something only a person
	// settles — a worktree somebody has been in, a change that is no longer
	// there. It hands the stoppage to the development manager rather than having
	// the harness ask again on every pull.
	CheckStageContinuationRefused string `json:"check_stage_continuation_refused,omitempty"`
	// CheckStageContinuationWaitNoted is the latest overdue wait note written
	// onto the item. It prevents duplicate notes across watcher restarts and is
	// cleared when the run continues, for the next stoppage's own deadline.
	CheckStageContinuationWaitNoted string `json:"check_stage_continuation_wait_noted,omitempty"`
	// IntegrationRetries counts the races for its target branch this run has
	// lost: each one a promotion refused because the target moved, answered by
	// replaying the change onto where the target went, re-checking it, and
	// re-reviewing it. It is recorded before the replay begins, so a process that
	// dies mid-replay comes back to the count it had. It is the record of the
	// races and bounds nothing: a lost race never stops a run, and a run keeps
	// replaying for as long as its replays keep passing.
	IntegrationRetries int `json:"integration_retries,omitempty"`
	// ChargedReplays counts the replays that stopped on the change rather than on
	// the target: the replay conflicted, or the replayed change was handed back
	// for a failing check, a refused path, missing verification, or a repair
	// verdict. These, and only these, spend
	// execution.integration_retries_before_reconciliation, and the replay that
	// takes the count past it stops the run there, on the change. Each replay is
	// charged at most once.
	ChargedReplays int `json:"charged_replays,omitempty"`
	// ReplayUnjudged is set when a replay is prepared and cleared by the first
	// thing its gate says: a stop on the change, which charges it, or the next
	// lost race or the promotion, which says it passed. It is what keeps one replay
	// from being charged twice however many repairs it goes on to need.
	ReplayUnjudged bool `json:"replay_unjudged,omitempty"`
	// TransientRelaunches counts the provider invocations this run has reissued
	// after one died without judging the work — an API error the provider's own
	// retries did not outlast, or a response cut off mid-flight. One budget covers
	// the developer and the reviewer both, because what it bounds is how many
	// times a run absorbs the provider dying under it rather than how often either
	// role is asked; separate budgets would let a run alternating between them
	// absorb twice what an operator configured. It is bounded apart from the
	// repair budget for the reason that budget is bounded apart from the
	// integration one: nothing here is a fault in the change, so spending a repair
	// attempt on it would charge the developer for the provider's weather. It is
	// recorded before each relaunch begins, so a process that dies mid-relaunch
	// resumes against the budget it had rather than buying a fresh one.
	TransientRelaunches int `json:"transient_relaunches,omitempty"`
	// Retries are the recoverable failures this run waited out and asked again, in
	// the order it met them, each carrying the boundary it happened at, which
	// attempt at that boundary it was, and how long the run waited before the next
	// one. A run that finishes carrying some of these is a run a connection reset
	// would have recorded as failed before yoyodyne-ifd.264, which is why they are
	// durable rather than only logged: what they evidence is completed work that
	// was nearly lost, and an operator reading the record afterwards has to be
	// able to see how close it came.
	//
	// Each is appended before its wait begins, exactly as the counters beside it
	// are recorded before the thing they bound, so a process that dies mid-wait
	// comes back to the recovery it had already spent rather than to a fresh
	// window.
	Retries []Retry `json:"retries,omitempty"`
	// UsageLimitResetsAt is the deadline a run paused for an exhausted provider
	// usage limit is waiting on. It is written before the wait begins, so a
	// process that dies during the wait does not lose the deadline and a restart
	// honors it instead of retrying straight back into the same limit. It is
	// cleared once the deadline passes and the attempt is reissued, so a run
	// carrying one is a run that is still waiting.
	UsageLimitResetsAt *time.Time `json:"usage_limit_resets_at,omitempty"`
	// UsageLimitPausedSince is when the pause the deadline above belongs to
	// began. It is written beside the deadline and cleared with it, and it is
	// here because the deadline alone cannot say how long the run has been
	// parked: every probe re-records the wait, so UpdatedAt is the start of the
	// probe being slept rather than of the pause. The capacity hold is read off
	// this — a run parked on a limit is a refusal like any in the usage-limit
	// log, and the hold's age is measured from its earliest standing refusal —
	// and a hold whose start moved with every probe would never stand long
	// enough to be said as critical.
	UsageLimitPausedSince *time.Time `json:"usage_limit_paused_since,omitempty"`
	// UsageLimitResetUnknown reports that the deadline above is the harness's own
	// next probe rather than a reset the provider named: an exhausted limit the
	// provider gave no reset for, a server overload, or an outage. A reader
	// saying when the wait lifts has to know which, because a probe interval
	// said as the provider's reset is a time the provider never named — and a
	// hold marked by it would be a fresh hold every probe. Records written before
	// this was carried read as a reset the provider named, which is what nearly
	// every recorded deadline was.
	UsageLimitResetUnknown bool `json:"usage_limit_reset_unknown,omitempty"`
	// UsageLimitModel is the model selector the refused invocation asked for,
	// recorded so a run's park can be read back as a refusal of that model in the
	// same way a refusal outside a run is. It is written with the deadline and
	// outlives it like the kind does: the developer's model is already on the
	// record as ProviderModel, but the reviewer's is recorded only once a review
	// has answered, and a refused review has not.
	UsageLimitModel string `json:"usage_limit_model,omitempty"`
	// UsageLimitKind is the provider's own name for the limit that paused the
	// run, kept as evidence for whoever reads the record afterwards. It outlives
	// the deadline: what stopped the run is worth knowing even once the run has
	// resumed.
	UsageLimitKind string `json:"usage_limit_kind,omitempty"`
	// UsageLimitPausedSeconds is how much waiting this run has committed to
	// across every pause it has taken. It is what bounds a run against the
	// configured maximum pause: bounding each wait on its own would let a
	// provider that refuses repeatedly walk a run past the maximum an operator
	// configured, one acceptable-looking wait at a time. It is recorded in whole
	// seconds because the provider states reset times in whole seconds, and it is
	// added to when a wait is committed rather than as it elapses, so a restart
	// part-way through a wait cannot buy the run a fresh budget.
	UsageLimitPausedSeconds int64 `json:"usage_limit_paused_seconds,omitempty"`
	// PauseCause is which refusal the recorded deadline is being waited out for.
	// The deadline and the budget are shared by both, so without this a run
	// waiting out a transiently overloaded server would be described to its
	// operator as one waiting out an exhausted account. It is empty on a run that
	// is not waiting, and an empty value alongside a deadline reads as a usage
	// limit, which is what every record written before this field described.
	PauseCause string `json:"pause_cause,omitempty"`
	// ProviderOutageChannel is where the provider's refusal was read on the
	// outage pause this run took: on the terminal of its stream, or on its
	// process's stderr or plain stdout because the provider refused before it
	// wrote a terminal at all. It is kept beside UsageLimitKind as evidence and
	// outlives the deadline like it, because which channel a refusal came on says
	// whether the dialect read an ending the provider wrote or a process that
	// died before writing one — the shape yoyodyne-ifd.377 could not see. It is
	// empty on a run that never waited on an outage, and on one whose wait was
	// recorded before the channel was.
	ProviderOutageChannel domain.ProviderChannel `json:"provider_outage_channel,omitempty"`
	// OperatorHeldSince is when this run parked at a provider-call boundary
	// because the operator holds all harness activity. It is written before the
	// wait begins, exactly as a usage-limit deadline is, so a process that dies
	// while the harness is held leaves a run that still says why it stopped and
	// can be picked up again. It is cleared as the run carries on, so a run
	// carrying one is a run still waiting on the operator.
	//
	// There is no deadline beside it because there is nothing to record: what
	// lifts an operator hold is the operator.
	OperatorHeldSince *time.Time `json:"operator_held_since,omitempty"`
	// OperatorHeldSeconds is how much of this run's elapsed time the operator's
	// hold accounts for, across every hold it has parked on. It is kept apart
	// from the usage-limit budget above because it answers a different question
	// and is bounded by nothing the harness configures: the provider never
	// refused this run, and a maximum pause that stopped a held run would be the
	// harness overriding the operator. What it is for is the ledger — so time a
	// run spent doing nothing says whose decision that was.
	OperatorHeldSeconds int64 `json:"operator_held_seconds,omitempty"`
	// ProviderStop records that the harness stopped a provider invocation on
	// time -- because it stalled, or because it exhausted its total budget --
	// rather than the provider ending it. It is written only when what the
	// invocation leaves behind can still be continued, so a run carrying one is
	// a run owed a continuation, exactly as a recorded usage-limit deadline is.
	// It is cleared by the next attempt, whichever way that one goes.
	ProviderStop string `json:"provider_stop,omitempty"`
	// AfterReply is the latest developer attempt's account of a session that
	// went on running after it had written its final reply — kept alive by work
	// it started in the background. While the harness waits that work out it has
	// no outcome, and every surface says "reply written, waiting for background
	// processes" for the run in place of its phase; once the attempt returns it
	// says whether the work ended on its own or was ended at its bound. It is
	// never a stall and never carries a ProviderStop: the turn had ended. An
	// attempt whose session exited with its reply clears it.
	AfterReply *execution.AfterReply `json:"after_reply,omitempty"`
	// DirectivePause records that an unresolved user directive stopped this run
	// short of finishing: one that changes a governed artifact this work derives
	// from, or one nobody can act on until the operator says what they meant. Like
	// a recorded deadline it is an instruction to resume later rather than a
	// failure, so a run carrying one keeps its claim, its worktree, and its
	// branch, and is picked up again once the directive is resolved. It is cleared
	// as the run resumes.
	DirectivePause *DirectivePause `json:"directive_pause,omitempty"`
	// DependencyPause records that unfinished work this item was made to wait on
	// stopped the run short of finishing. Like a directive pause it is an
	// instruction to resume later rather than a failure, so a run carrying one
	// keeps its claim, its worktree, and its branch, and is picked up again once
	// the work it waits on is closed. It is cleared as the run resumes.
	//
	// It is a separate field from the directive pause rather than a second kind of
	// one because what lifts them differs: a directive is settled by a person
	// deciding, and this is lifted by other work finishing.
	DependencyPause *DependencyPause `json:"dependency_pause,omitempty"`
	// TrackerPause records that the tracker would not answer the read this run
	// makes at a gate boundary, for the whole of that boundary's recovery window.
	// Like a dependency pause it is an instruction to resume later rather than a
	// failure, so a run carrying one keeps its claim, its worktree, its branch,
	// and its developer session, and is picked up again once the store answers.
	// It is cleared as the run resumes.
	//
	// It is a separate field from the dependency pause rather than a second kind
	// of one for the reason that pause is separate from the directive's: what
	// lifts them differs. That one is lifted by other work finishing, and this by
	// a store that was contended becoming reachable again.
	TrackerPause *TrackerPause `json:"tracker_pause,omitempty"`
	// RedeployStop records that the watch session hosting this run stopped it
	// in order to restart into a build deployed over it, once the drain the
	// session was given ran out with this run still going. Like a recorded
	// provider stop it is an instruction to continue later rather than a
	// failure: the run keeps its claim, its worktree, its branch, and its
	// developer session, and the session that comes back re-adopts it from
	// exactly here, with every counter as it was. It is written only when what
	// the run leaves behind can be continued, and cleared as the run resumes.
	RedeployStop *RedeployStop `json:"redeploy_stop,omitempty"`
	// Readopted is the most recent redeploy stop this run was continued from:
	// the stop above as it stood when the run resumed and cleared it. It is kept
	// because the stop itself is cleared, and a stall later in the run is read
	// differently once it is known to have begun in the session a re-adoption
	// resumed.
	Readopted *RedeployStop `json:"readopted,omitempty"`
	// StallContinuationRefused is why the harness declined to continue a first
	// silent-stream stall itself, where what declined it is something only a
	// person settles — a worktree somebody has been in, a change that is no
	// longer there. It hands the stoppage to the development manager rather than
	// having the harness ask again on every pull.
	StallContinuationRefused string `json:"stall_continuation_refused,omitempty"`
	// Changes is what the run's worktree held when it was last summarized. It is
	// absent from a run that never got as far as producing one, and it outlives
	// the worktree it describes, which is the whole reason it is here rather than
	// only in the outcome the run returned.
	Changes     *Changes     `json:"changes,omitempty"`
	Integration *Integration `json:"integration,omitempty"`
	// PullRequest records the published pull request when the project opted in
	// to publishing and the repository had a remote to publish to. It is absent
	// for a purely local run, which is what a project gets by default.
	PullRequest *PullRequest `json:"pull_request,omitempty"`
	// PublishFailure explains why publishing a promotion did not finish. The
	// local target branch is the authoritative one, so a promotion that could not
	// be pushed is an outstanding publication rather than a failed run — the same
	// kind of fact as an outstanding cleanup.
	PublishFailure string `json:"publish_failure,omitempty"`
	// MergeDrop is when the merge of this run's promotion stopped being something
	// the forge was going to do, and why. It is absent from every run whose merge
	// happened, is still queued, or was never asked for, and it is written once by
	// whoever found out — the run whose merge the forge refused, or the sweep that
	// asked what became of a queued one.
	MergeDrop *MergeDrop `json:"merge_drop,omitempty"`
	// Failure is why this run ended, in the words of whoever ended it, and it is
	// what every surface prints where it answers "why did this stop". It is
	// written by whatever made the run terminal — the pipeline as it fails a run,
	// the sweep as it settles one — and a sweep settling a stoppage onto a record
	// that was already terminal fills it in where that record gives no reason,
	// because a stoppage nobody can read a reason for is what the surfaces cannot
	// recover from afterwards.
	//
	// It is text and never a test. Whether a run stopped is Blocker's to answer
	// and Outcome()'s to say: a run can end with a reason and no blocker, which is
	// a failure nobody has to decide about, and a stoppage can reach a record
	// whose reason was written before it. A reader inferring a stoppage from a
	// non-empty Failure is a second classification that will disagree with the
	// read model's, which is exactly what the fixed outcome vocabulary exists to
	// prevent.
	//
	// It is bounded like every other recorded reason, and RecordFailure is how it
	// is written: what ends a run is often an error with a provider's whole output
	// folded into it, and a reason nothing downstream can carry is a stoppage that
	// validates here and then reaches nobody.
	Failure string `json:"failure,omitempty"`
	// StopClass is which gate or bound stopped the run, written where the harness
	// stopped it: at every blocker and every failure it records, and beside a
	// publication, a cleanup, or a completion record a succeeded run could not
	// finish. It is what every surface prints as the first word of the reason, so a
	// run the provider killed while a check failure was still on its record reads
	// as the provider's stop rather than the check's.
	//
	// It is a classification of the stop and never of the outcome: Outcome() says
	// what became of the run, and a succeeded run can carry a class naming what it
	// stopped short of. Absent is a run nothing stopped, or a record written before
	// the field existed. RecordedStopClass reads the latter as unknown.
	StopClass StopClass `json:"stop_class,omitempty"`
	// Blocker is the durable blocker exactly as it was recorded on the work item
	// when this run stopped on something no further attempt of the harness could
	// resolve. The tracker holds the authoritative copy; this one is kept because
	// the blocker is evidence about the run and the run is what outlives the
	// process that wrote it — a triage docket entry built from this record has to
	// carry the blocker in the words it was recorded in, and a later reader of the
	// run must not have to go and find which of the item's notes was this run's.
	// Absent means this run stopped on nothing anybody has to decide, which is
	// what every record written before the docket existed means.
	//
	// It is the one field a stoppage is inferred from, which is why the words and
	// the fact are two fields rather than one: Failure above says why a run ended
	// and this says that somebody now owns it.
	Blocker string `json:"blocker,omitempty"`
	// CleanupFailure explains why post-completion cleanup did not finish
	// cleanly. The run's work is already integrated, closed, and durable when it
	// is set, so it is reconciliation input rather than a run failure. It says
	// nothing on its own about what survives: WorktreeRemoved and BranchRemoved
	// carry that, and either can still be false, leaving a real artifact behind,
	// or both can be true because the removals succeeded and only the check that
	// confirms them could not run. A reconciler resumes cleanup in both cases,
	// which is a safe no-op over artifacts that are already gone.
	CleanupFailure string `json:"cleanup_failure,omitempty"`
	// CompletionRecordingFailure explains why a completed run's final record
	// took more than the normal write to land. The failure it names is the
	// store refusing the terminal save, so the field reaches disk only when a
	// later best-effort write succeeds — at which point the record is whole
	// and this says it arrived late. It matters because it is the one failure
	// class whose work-item note is itself unreliable: recording that note is
	// part of what was failing, so the run record is its authoritative home.
	CompletionRecordingFailure string `json:"completion_recording_failure,omitempty"`
	// ConfigComparison is saved before delivery to the work item. A pending
	// delivery remains outstanding even after settlement and cleanup finish.
	ConfigComparison  *ConfigComparison  `json:"config_comparison,omitempty"`
	ReconcileFindings []ReconcileFinding `json:"reconcile_findings,omitempty"`
}

var (
	runIDPattern  = regexp.MustCompile(`^run-[a-f0-9]{32}$`)
	commitPattern = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)
	branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

// ValidRunID reports a run identifier of the shape this package mints. It is
// exported because a run is named from outside the harness too: a triage
// decision names the run whose stoppage it settles, copied out of a docket entry
// by an agent, and a name that could never have identified a run is refused
// where it is read rather than written onto a work item.
func ValidRunID(runID string) bool {
	return runIDPattern.MatchString(strings.TrimSpace(runID))
}

// validLocalBranch mirrors the integration target rule the worktree manager
// enforces: a plain local branch name, never HEAD and never a fully qualified
// ref. Durable evidence is re-read by a reconciler that acts on it, so it is
// held to the same shape here rather than only at the point it was produced.
func validLocalBranch(branch string) bool {
	branch = strings.TrimSpace(branch)
	if !branchPattern.MatchString(branch) {
		return false
	}
	if strings.Contains(branch, "..") || strings.Contains(branch, "//") || strings.HasSuffix(branch, "/") {
		return false
	}
	return branch != "HEAD" && !strings.HasPrefix(branch, "refs/")
}

func NewRunID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return "run-" + hex.EncodeToString(bytes), nil
}

func (s State) Validate() error {
	var problems []error
	seenFindings := make(map[ReconcileStep]bool)
	for _, finding := range s.ReconcileFindings {
		if !slices.Contains(reconcileSteps, finding.Step) || seenFindings[finding.Step] || strings.TrimSpace(finding.Problem) == "" {
			problems = append(problems, errors.New("reconcile_findings requires one refusal per recognized settlement step"))
		}
		seenFindings[finding.Step] = true
		if len(finding.Problem) > MaxBlockerBytes {
			problems = append(problems, errors.New("reconcile_findings.problem exceeds its recorded text bound"))
		}
	}
	if c := s.ConfigComparison; c != nil {
		if s.Integration == nil || !commitPattern.MatchString(c.TargetCommit) || !commitPattern.MatchString(c.PreviousTargetCommit) {
			problems = append(problems, errors.New("config_comparison requires an integration and valid compared revisions"))
		} else if c.PreviousTargetCommit != s.Integration.PreviousTargetCommit {
			problems = append(problems, errors.New("config_comparison previous revision differs from the integration"))
		}
		if !c.Pending && c.DeliveryFailure != "" {
			problems = append(problems, errors.New("config_comparison delivery_failure requires pending delivery"))
		}
	}
	if s.SchemaVersion != StateSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", StateSchemaVersion))
	}
	if !runIDPattern.MatchString(s.RunID) {
		problems = append(problems, errors.New("run_id is invalid"))
	}
	if err := domain.ValidateIdentifier("product id", string(s.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(s.RepositoryID) == "" {
		problems = append(problems, errors.New("repository_id is required"))
	}
	if strings.TrimSpace(s.WorkItemID) == "" {
		problems = append(problems, errors.New("work_item_id is required"))
	}
	if !s.Backend.Valid() {
		problems = append(problems, errors.New("backend is invalid"))
	}
	if !s.Status.Valid() {
		problems = append(problems, errors.New("status is invalid"))
	}
	if s.StartedAt.IsZero() || s.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("started_at and updated_at are required"))
	}
	if s.UpdatedAt.Before(s.StartedAt) {
		problems = append(problems, errors.New("updated_at cannot be before started_at"))
	}
	if s.Status.Terminal() && s.CompletedAt == nil {
		problems = append(problems, errors.New("terminal status requires completed_at"))
	}
	if !s.Status.Terminal() && s.CompletedAt != nil {
		problems = append(problems, errors.New("non-terminal status cannot have completed_at"))
	}
	if s.SettledQuietSince != nil && (s.CompletedAt == nil || s.SettledQuietSince.IsZero() || s.SettledQuietSince.After(*s.CompletedAt)) {
		problems = append(problems, errors.New("settled_quiet_since requires completed_at and cannot be after it"))
	}
	worktreeFields := 0
	for _, value := range []string{s.WorktreePath, s.Branch, s.BaseCommit} {
		if value != "" {
			worktreeFields++
		}
	}
	if worktreeFields != 0 && worktreeFields != 3 {
		problems = append(problems, errors.New("worktree_path, branch, and base_commit must be recorded together"))
	}
	if s.BaseCommit != "" && !commitPattern.MatchString(s.BaseCommit) {
		problems = append(problems, errors.New("base_commit is invalid"))
	}
	if s.HarnessCommit != "" {
		if !commitPattern.MatchString(s.HarnessCommit) {
			problems = append(problems, errors.New("harness_commit is invalid"))
		}
		// The commit was made in this run's worktree, so a record of one without
		// the worktree that produced it permits a HEAD nothing accounted for.
		if s.WorktreePath == "" {
			problems = append(problems, errors.New("harness_commit requires the worktree that produced it"))
		}
		if s.HarnessCommit == s.BaseCommit {
			problems = append(problems, errors.New("harness_commit cannot be the base commit, which proves no commit was made"))
		}
	}
	// The two commits a review was judged between are recorded together or not
	// at all: a review that names a base and no tip says less than one naming
	// neither, because it reads as a record of what was shown.
	if (s.ReviewBaseCommit == "") != (s.ReviewHeadCommit == "") {
		problems = append(problems, errors.New("review_base_commit and review_head_commit must be recorded together"))
	}
	if s.ReviewBaseCommit != "" && !commitPattern.MatchString(s.ReviewBaseCommit) {
		problems = append(problems, errors.New("review_base_commit is invalid"))
	}
	if s.ReviewHeadCommit != "" && !commitPattern.MatchString(s.ReviewHeadCommit) {
		problems = append(problems, errors.New("review_head_commit is invalid"))
	}
	if s.Selection != nil {
		if err := s.Selection.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("selection: %w", err))
		}
	}
	// A lifted commit is evidence of what the run started from, so it names a
	// commit and it names one only where the run was selected to lift one.
	if s.LiftedCommit != "" {
		if !commitPattern.MatchString(s.LiftedCommit) {
			problems = append(problems, errors.New("lifted_commit is invalid"))
		}
		if s.Selection == nil || s.Selection.Lift == nil {
			problems = append(problems, errors.New("lifted_commit requires the selection that asked for the lift"))
		}
	}
	// All three are absent from every record written before they were carried, so
	// what is checked is the shape of one that is there: a record naming an
	// account, a configuration, or a build nothing could have produced says less
	// than one naming none of them, because it reads as evidence.
	if s.AccountAlias != "" && !accountAliasPattern.MatchString(s.AccountAlias) {
		problems = append(problems, errors.New("account_alias is not an account alias"))
	}
	if s.ConfigRevision != "" && !configRevisionPattern.MatchString(s.ConfigRevision) {
		problems = append(problems, errors.New("config_revision is not a configuration revision"))
	}
	if s.Build != "" && !buildPattern.MatchString(s.Build) {
		problems = append(problems, errors.New("build is not a revision"))
	}
	// The instance is checked to the shape an instance is recorded under, for the
	// same reason: a run naming an instance nothing could be stored as names
	// nothing at all. A divergence without one is worse than either — it reports
	// an observation about a run that was never observed — so it is refused
	// rather than carried.
	if s.WorkflowInstanceID != "" {
		if err := domain.ValidateIdentifier("workflow_instance_id", s.WorkflowInstanceID); err != nil {
			problems = append(problems, err)
		}
	}
	if s.WorkflowDivergence != "" && s.WorkflowInstanceID == "" {
		problems = append(problems, errors.New("workflow_divergence requires the workflow_instance_id it was observed on"))
	}
	// And the same contradiction from the other side: a run naming an instance was
	// observed, so a reason it is not observed said beside one is a record that
	// disagrees with itself about which of the two a reader should believe.
	if s.WorkflowUnobserved != "" && s.WorkflowInstanceID != "" {
		problems = append(problems, errors.New("workflow_unobserved is why a run has no instance, and this run names workflow_instance_id"))
	}
	if s.Phase != "" && !s.Phase.Valid() {
		problems = append(problems, errors.New("phase is invalid"))
	}
	if s.ReviewDecision != "" && !slices.Contains(reviewDecisions, s.ReviewDecision) {
		problems = append(problems, errors.New("review_decision is invalid"))
	}
	if s.ReviewApproves != "" && !slices.Contains(reviewApprovals, s.ReviewApproves) {
		problems = append(problems, errors.New("review_approves is invalid"))
	}
	// What an approval approves is only recorded against an approval. A repair
	// closes nothing, so a record carrying both would say the settlement was
	// decided by a verdict that sent the change back.
	if s.ReviewApproves != "" && s.ReviewDecision != ReviewApprove {
		problems = append(problems, errors.New("review_approves is only for a verdict recorded as approve"))
	}
	// An escalation with no account of itself is the half of the record that would
	// be read afterwards. It is the whole of what the development manager decides
	// from, so a record that lost it would put an item in front of her saying only
	// that somebody thought it unmeetable.
	if s.ReviewDecision == ReviewEscalate && strings.TrimSpace(s.ReviewSummary) == "" {
		problems = append(problems, errors.New("review_decision escalate requires the review_summary it was raised with"))
	}
	if s.LandingOutcome != "" && !slices.Contains(landingOutcomes, s.LandingOutcome) {
		problems = append(problems, errors.New("landing_outcome is invalid"))
	}
	if s.StopClass != "" && !s.StopClass.Valid() {
		problems = append(problems, errors.New("stop_class is invalid"))
	}
	// A claim with no account of itself is the half of the record that would be
	// read afterwards, so an outcome without one is refused here rather than
	// leaving an item open for a reason nobody wrote down.
	if s.LandingOutcome != "" && strings.TrimSpace(s.LandingReason) == "" {
		problems = append(problems, errors.New("landing_outcome requires the landing_reason it was claimed for"))
	}
	// The marker only means anything on a landing that leaves the item open, and a
	// record carrying one anywhere else is a record the settlement would read: an
	// item closed against a discharge does not wait for anything, and one whose
	// claim could not be read is not left open on the strength of a marker in the
	// same unreadable block.
	if strings.TrimSpace(s.LandingBlockedBy) != "" && (s.LandingOutcome != LandingEvidence || s.LandingProblem != "") {
		problems = append(problems, errors.New("landing_blocked_by is only for a landing recorded as evidence"))
	}
	if strings.TrimSpace(s.LandingImpedimentProblem) != "" && (s.LandingOutcome != LandingEvidence || s.LandingProblem != "") {
		problems = append(problems, errors.New("landing_impediment_problem is only for a landing recorded as evidence"))
	}
	// The two are exclusive by construction: the resolution writes the marker or
	// the reason it could not, never both. A record carrying both says the item
	// waits and is parked at once, which is two different settlements.
	if strings.TrimSpace(s.LandingBlockedBy) != "" && strings.TrimSpace(s.LandingImpedimentProblem) != "" {
		problems = append(problems, errors.New("landing_blocked_by and landing_impediment_problem cannot both be recorded"))
	}
	// Nothing waits on itself. The resolution refuses this before it is stored, so
	// a record carrying it is one nothing here produced, and the settlement it
	// would drive is a dependency the tracker refuses as a cycle.
	if marker := strings.TrimSpace(s.LandingBlockedBy); marker != "" && marker == s.WorkItemID {
		problems = append(problems, errors.New("landing_blocked_by cannot name the work item it was claimed on"))
	}
	if s.ReviewFindings < 0 {
		problems = append(problems, errors.New("review_findings cannot be negative"))
	}
	for index, finding := range s.ReviewFindingDetails {
		if err := finding.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("review_finding_details[%d]: %w", index, err))
		}
	}
	// The count predates the findings themselves and a file written before them
	// carries only the count, so the two are held to agreeing only once both are
	// present. Disagreement there would mean the recorded evidence and the
	// developer's next input describe different reviews.
	if len(s.ReviewFindingDetails) > 0 && s.ReviewFindings != len(s.ReviewFindingDetails) {
		problems = append(problems, fmt.Errorf("review_findings is %d but %d review_finding_details are recorded", s.ReviewFindings, len(s.ReviewFindingDetails)))
	}
	if s.CheckStage != nil {
		if err := s.CheckStage.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("check_stage: %w", err))
		}
	}
	if s.LandingChecks != nil {
		if err := s.LandingChecks.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("landing_checks: %w", err))
		}
	}
	if s.CheckFailure != nil {
		if err := s.CheckFailure.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("check_failure: %w", err))
		}
	}
	if s.ChecksPassed != nil {
		if err := s.ChecksPassed.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("checks_passed: %w", err))
		}
		// The two are the opposite answers to one question about one attempt, so
		// a record carrying both describes a gate that has not decided.
		if s.CheckFailure != nil {
			problems = append(problems, errors.New("checks_passed and check_failure cannot both describe the current attempt"))
		}
	}
	if s.CheckoutRestorePending && (!s.Status.Terminal() || s.ChecksPassed != nil) {
		problems = append(problems, errors.New("an unfinished checkout restoration requires a stopped run with verification credit cleared"))
	}
	if s.PathRefusal != nil {
		if err := s.PathRefusal.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("path_refusal: %w", err))
		}
	}
	if s.Verification != nil {
		if err := s.Verification.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("verification: %w", err))
		}
	}
	if s.DeveloperSummary != nil {
		if err := s.DeveloperSummary.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("developer_summary: %w", err))
		}
	}
	if len(s.RefusedAmendments) > MaxCarriedAmendmentRefusals {
		problems = append(problems, fmt.Errorf("%d refused amendments are carried, which exceeds the bound of %d", len(s.RefusedAmendments), MaxCarriedAmendmentRefusals))
	}
	for index, refused := range s.RefusedAmendments {
		if err := refused.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("refused_amendments[%d]: %w", index, err))
		}
	}
	if len(s.Amendments) > MaxRunAmendments {
		problems = append(problems, fmt.Errorf("%d proposals are recorded, which exceeds the bound of %d", len(s.Amendments), MaxRunAmendments))
	}
	for index, proposed := range s.Amendments {
		if err := proposed.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("amendments[%d]: %w", index, err))
		}
	}
	for _, field := range s.recordedTexts() {
		if !field.stated && len(*field.text) > field.limit {
			problems = append(problems, fmt.Errorf("%s is %d bytes, which exceeds the %d byte bound", field.path, len(*field.text), field.limit))
		}
	}
	if s.ContextTruncation != nil {
		if err := s.ContextTruncation.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("context_truncation: %w", err))
		}
	}
	if s.StaleBlockClear != nil {
		if err := s.StaleBlockClear.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("stale_block_clear: %w", err))
		}
	}
	if s.Changes != nil {
		if err := s.Changes.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("changes: %w", err))
		}
	}
	if s.RepairAttempts < 0 {
		problems = append(problems, errors.New("repair_attempts cannot be negative"))
	}
	if len(s.RepairContinuations) > MaxRepairContinuations {
		problems = append(problems, fmt.Errorf("%d repair continuations are recorded, which exceeds the bound of %d", len(s.RepairContinuations), MaxRepairContinuations))
	}
	for index, continuation := range s.RepairContinuations {
		if err := continuation.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("repair_continuations[%d]: %w", index, err))
		}
	}
	if s.Environmental != nil {
		if err := s.Environmental.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("stop outside the work: %w", err))
		}
	}
	problems = append(problems, s.validateIntegrationResume()...)
	problems = append(problems, s.validateSweepContinuations()...)
	problems = append(problems, s.validateCheckStageContinuations()...)
	if s.ReviewRounds < 0 {
		problems = append(problems, errors.New("review_rounds cannot be negative"))
	}
	if s.IntegrationRetries < 0 {
		problems = append(problems, errors.New("integration_retries cannot be negative"))
	}
	if s.ChargedReplays < 0 {
		problems = append(problems, errors.New("charged_replays cannot be negative"))
	}
	if s.TransientRelaunches < 0 {
		problems = append(problems, errors.New("transient_relaunches cannot be negative"))
	}
	if s.UsageLimitPausedSeconds < 0 {
		problems = append(problems, errors.New("usage_limit_paused_seconds cannot be negative"))
	}
	if len(s.Retries) > MaxRetries {
		problems = append(problems, fmt.Errorf("retries holds %d entries, which exceeds the %d entry bound", len(s.Retries), MaxRetries))
	}
	for index, retry := range s.Retries {
		if err := retry.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("retries[%d]: %w", index, err))
		}
	}
	if s.UsageLimitResetsAt != nil {
		// A pause is an instruction to resume later, so it is only coherent on a
		// run that can still be resumed. Recorded on a terminal run it would
		// promise a continuation that nothing will ever make.
		if s.UsageLimitResetsAt.IsZero() {
			problems = append(problems, errors.New("usage_limit_resets_at cannot be the zero time"))
		}
		if s.Status.Terminal() {
			problems = append(problems, errors.New("usage_limit_resets_at requires a run that is still in flight"))
		}
	}
	if s.UsageLimitPausedSince != nil {
		// The start of a pause belongs beside the deadline of one: on its own it
		// would describe a wait the run is not taking.
		if s.UsageLimitPausedSince.IsZero() {
			problems = append(problems, errors.New("usage_limit_paused_since cannot be the zero time"))
		}
		if s.UsageLimitResetsAt == nil {
			problems = append(problems, errors.New("usage_limit_paused_since requires usage_limit_resets_at"))
		}
	}
	if _, outage := PausedForProviderOutage(s.PauseCause); s.PauseCause != "" && !outage &&
		s.PauseCause != PauseUsageLimit && s.PauseCause != PauseServerOverload && s.PauseCause != PauseOperatorHold {
		problems = append(problems, errors.New("pause_cause is invalid"))
	}
	if s.ProviderOutageChannel != "" && !s.ProviderOutageChannel.Valid() {
		problems = append(problems, errors.New("provider_outage_channel is invalid"))
	}
	if s.OperatorHeldSeconds < 0 {
		problems = append(problems, errors.New("operator_held_seconds cannot be negative"))
	}
	if s.OperatorHeldSince != nil {
		if s.OperatorHeldSince.IsZero() {
			problems = append(problems, errors.New("operator_held_since cannot be the zero time"))
		}
		// A hold is an instruction to carry on once the operator lifts it, so like
		// every other pause it is only coherent on a run something can still carry
		// on: recorded on a terminal run it would promise a continuation that
		// nothing will ever make.
		if s.Status.Terminal() {
			problems = append(problems, errors.New("operator_held_since requires a run that is still in flight"))
		}
	}
	if s.ProviderStop != "" {
		if s.ProviderStop != ProviderStopStalled && s.ProviderStop != ProviderStopBudgetExhausted {
			problems = append(problems, errors.New("provider_stop is invalid"))
		}
		// Like a recorded pause, a recorded stop is an instruction to continue
		// later. On a terminal run it would promise a continuation nothing will
		// ever make.
		if s.Status.Terminal() {
			problems = append(problems, errors.New("provider_stop requires a run that is still in flight"))
		}
	}
	if s.DirectivePause != nil {
		if err := s.DirectivePause.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("directive_pause: %w", err))
		}
		// A directive pause is the same kind of instruction as the two above:
		// resume this later. Recorded on a terminal run it would promise a
		// continuation nothing will ever make.
		if s.Status.Terminal() {
			problems = append(problems, errors.New("directive_pause requires a run that is still in flight"))
		}
	}
	if s.DependencyPause != nil {
		if err := s.DependencyPause.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("dependency_pause: %w", err))
		}
		// A dependency pause is the same kind of instruction as the ones above:
		// resume this later. Recorded on a terminal run it would promise a
		// continuation nothing will ever make.
		if s.Status.Terminal() {
			problems = append(problems, errors.New("dependency_pause requires a run that is still in flight"))
		}
	}
	if s.TrackerPause != nil {
		if err := s.TrackerPause.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("tracker_pause: %w", err))
		}
		// A tracker pause is the same kind of instruction as the ones above:
		// resume this later. Recorded on a terminal run it would promise a
		// continuation nothing will ever make.
		if s.Status.Terminal() {
			problems = append(problems, errors.New("tracker_pause requires a run that is still in flight"))
		}
	}
	if s.RedeployStop != nil {
		if err := s.RedeployStop.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("redeploy_stop: %w", err))
		}
		// A redeploy stop is the same kind of instruction again: the session that
		// comes back continues this. Recorded on a terminal run it would promise a
		// continuation nothing will ever make.
		if s.Status.Terminal() {
			problems = append(problems, errors.New("redeploy_stop requires a run that is still in flight"))
		}
	}
	if s.Readopted != nil {
		if err := s.Readopted.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("readopted: %w", err))
		}
	}
	if len(s.StallContinuationRefused) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("stall_continuation_refused is %d bytes, which exceeds the %d byte bound", len(s.StallContinuationRefused), MaxBlockerBytes))
	}
	if s.EscalationEnded != nil {
		if err := s.EscalationEnded.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("escalation_ended: %w", err))
		}
	}
	if s.TargetBranch != "" && !validLocalBranch(s.TargetBranch) {
		problems = append(problems, errors.New("target_branch must be a local branch name"))
	}
	if s.Integration != nil {
		// Recorded integration is a claim that approved work was promoted, so it
		// is only coherent with the approval, the worktree that produced it, and
		// the two independent invocations that authorized it.
		if s.ReviewDecision != ReviewApprove {
			problems = append(problems, errors.New("integration requires an approving review decision"))
		}
		if s.BaseCommit == "" {
			problems = append(problems, errors.New("integration requires the integrated worktree"))
		}
		// Review and integration are reachable only through passing checks, so a
		// promotion recorded alongside a failing one describes a gate that was
		// never actually cleared.
		if s.CheckFailure != nil {
			problems = append(problems, errors.New("integration requires no recorded failing check"))
		}
		// And the same for the gate in front of them: a promotion recorded
		// alongside a refused protected path describes a change that reached
		// integration carrying an edit to the intent it was written against.
		if s.PathRefusal != nil {
			problems = append(problems, errors.New("integration requires no recorded protected-path refusal"))
		}
		// The target is fixed before the work starts, so an integration into a
		// different branch describes a promotion this run was never set up to
		// make.
		if s.TargetBranch != "" && s.Integration.TargetBranch != s.TargetBranch {
			problems = append(problems, fmt.Errorf("integration target_branch %q does not match the recorded target_branch %q", s.Integration.TargetBranch, s.TargetBranch))
		}
		// Integration is only produced by the integrating step, so evidence of it
		// alongside an earlier phase describes a run history that cannot have
		// happened. A reconciler reads the phase to decide what remains to do, so
		// an impossible pairing must not be storable.
		if !s.Phase.reached(PhaseIntegrating) {
			problems = append(problems, errors.New("integration requires the integrating phase or later"))
		}
		problems = append(problems, s.validateIndependentInvocations()...)
		if err := s.Integration.Validate(); err != nil {
			problems = append(problems, err)
		}
	}
	if s.PullRequest != nil {
		if err := s.PullRequest.Validate(); err != nil {
			problems = append(problems, err)
		}
		// The pull request publishes this run's branch, so a record naming a
		// different one describes a publication this run never made.
		if s.Branch != "" && s.PullRequest.Branch != s.Branch {
			problems = append(problems, fmt.Errorf("pull_request branch %q does not match the run branch %q", s.PullRequest.Branch, s.Branch))
		}
		// The forge is only asked to merge a pull request once the promotion it
		// carries has been made locally, so a merge this run asked for cannot be
		// recorded before the promotion that authorized it is. The merge method is
		// what says the run asked: it is written where the harness makes the merge
		// request, which never runs without an integration in hand.
		//
		// A merged request with no method is the other thing entirely — a merge
		// nobody here asked for, which somebody made on the forge after the run was
		// over. Recording it is an observation of what the forge did rather than a
		// claim that this run promoted anything, and refusing to store it is what
		// froze a failed run's publication at its death-moment state for good.
		if s.PullRequest.Merged && s.PullRequest.MergeMethod != "" && s.Integration == nil {
			problems = append(problems, errors.New("a merged pull request the run asked the forge for requires recorded integration"))
		}
	}
	if s.MergeDrop != nil {
		if err := s.MergeDrop.Validate(); err != nil {
			problems = append(problems, err)
		}
		// A dropped merge is a merge of something, so the record has to name the
		// request it was going to merge. Without one the drop says a publication
		// somebody has to look at is outstanding and nothing says which.
		if s.PullRequest == nil {
			problems = append(problems, errors.New("a recorded merge_drop requires the pull request whose merge was dropped"))
		}
	}
	// A removal is only ever recorded with the evidence that earned it. There are
	// four kinds: the run promoted its own work and cleaned up after it, triage
	// retired what it preserved once another run superseded it, the convergence
	// sweep retired an empty checkout as hygiene, or that same sweep deleted a
	// branch the target branch provably carries. A record carrying none of them
	// describes cleanup nothing authorized.
	//
	// The last two are recorded apart because they are earned apart and performed
	// apart: the checkout sweep never touches a branch, and the branch sweep
	// proves containment in the repository and never touches a checkout. Each
	// therefore only ever excuses its own artifact.
	retiredBy := strings.TrimSpace(s.ArtifactsRetiredBy)
	sweptWorktree := s.WorktreeSweptAt != nil
	sweptBranch := s.BranchSweptAt != nil
	// A forge failure takes back current promotion credit without erasing the
	// local promotion that already earned cleanup of these artifacts.
	priorPromotion := s.CheckFailure != nil && s.CheckFailure.LocalPromotion != nil
	if priorPromotion && (s.PullRequest == nil || s.MergeDrop == nil || s.PullRequest.HeadCommit != s.CheckFailure.ForgeHeadCommit) {
		problems = append(problems, errors.New("a failing check's local promotion requires the dropped publication of that revision"))
	}
	if ((s.WorktreeRemoved && !sweptWorktree) || (s.BranchRemoved && !sweptBranch)) && s.Integration == nil && !priorPromotion && retiredBy == "" {
		problems = append(problems, errors.New("removed artifacts require recorded integration, the run that superseded this one and retired them, or the convergence sweep that retired the checkout or deleted the branch"))
	}
	if sweptWorktree && !s.WorktreeRemoved {
		problems = append(problems, errors.New("a recorded checkout sweep names a checkout that was removed, and this one removed none"))
	}
	if sweptBranch && !s.BranchRemoved {
		problems = append(problems, errors.New("a recorded branch sweep names a branch that was deleted, and this one deleted none"))
	}
	if preservedWork := strings.TrimSpace(s.PreservedWorkRef); preservedWork != "" {
		// Only the sweep writes it, and it writes it as part of the removal, so a
		// record carrying one without the sweep describes a capture nothing did.
		if !sweptWorktree {
			problems = append(problems, errors.New("preserved_work_ref requires the checkout sweep that recorded it"))
		}
		// A branch here would be swept by the branch sweep and answer the
		// containment proofs the harness makes about run branches, which is exactly
		// what keeping the capture out of refs/heads avoids.
		if strings.HasPrefix(preservedWork, "refs/heads/") || !strings.HasPrefix(preservedWork, "refs/") {
			problems = append(problems, fmt.Errorf("preserved_work_ref %q must be a ref outside refs/heads", s.PreservedWorkRef))
		}
	}
	if s.PreservedWorkNotedAt != nil && strings.TrimSpace(s.PreservedWorkRef) == "" {
		problems = append(problems, errors.New("preserved_work_noted_at requires the preserved work its note names"))
	}
	if retiredBy != "" {
		if !ValidRunID(retiredBy) {
			problems = append(problems, fmt.Errorf("artifacts_retired_by %q is not a run identifier", s.ArtifactsRetiredBy))
		}
		if retiredBy == s.RunID {
			problems = append(problems, errors.New("a run does not supersede itself; artifacts it removed after its own promotion are recorded by that integration"))
		}
		if !s.WorktreeRemoved && !s.BranchRemoved {
			problems = append(problems, errors.New("a recorded retirement names what it removed, and this one removed neither artifact"))
		}
	}
	// A run is complete only once nothing is left to clean up; anything else is
	// still an outstanding-cleanup marker.
	if s.Integration != nil && s.Phase == PhaseComplete && (!s.WorktreeRemoved || !s.BranchRemoved) {
		problems = append(problems, errors.New("complete phase requires both the worktree and branch to be removed"))
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid run state: %w", errors.Join(problems...))
	}
	return nil
}

// UsageLimitPaused reports how long this run has already committed to waiting
// out provider usage limits, which is what its remaining pause budget is
// measured against.
func (s State) UsageLimitPaused() time.Duration {
	return time.Duration(s.UsageLimitPausedSeconds) * time.Second
}

// RetryAttempts is how many recoverable failures this run has already waited out
// at one boundary. It is read off the durable record rather than off anything a
// process is holding, so a run picked up after a crash carries on from the
// backoff it had reached instead of starting the series again.
func (s State) RetryAttempts(boundary string) int {
	attempts := 0
	for _, retry := range s.Retries {
		if retry.Boundary == boundary {
			attempts++
		}
	}
	return attempts
}

// RetryWaited is how much of one boundary's recovery window this run has already
// committed, which is what the next wait is measured against. It is added to
// when a wait is committed rather than as it elapses, for the reason the usage
// limit's budget is: a restart part-way through a wait must not buy a fresh
// window.
func (s State) RetryWaited(boundary string) time.Duration {
	var waited time.Duration
	for _, retry := range s.Retries {
		if retry.Boundary == boundary {
			waited += retry.Delay()
		}
	}
	return waited
}

// GrantedRepairAttempts is how many further repair attempts triage has granted
// this run across every continuation of it.
func (s State) GrantedRepairAttempts() int {
	granted := 0
	for _, continuation := range s.RepairContinuations {
		granted += continuation.GrantedAttempts
	}
	return granted
}

// CarriedOutRepairAttempts is how much of the item's repair grant this run has
// actually consumed. It is the same sum as above minus the continuations whose
// round the environment refused, because a round handed an empty worktree bought
// the item nothing and must not read afterwards as a grant that was used.
//
// The two are deliberately not one number. This run's own budget still counts a
// refused continuation — the attempt slot was spent, and a run whose attempts
// and budget disagreed would hand a developer a prompt saying which attempt of
// how many this is and be wrong. What the item was charged is a different
// question, and it is this one.
func (s State) CarriedOutRepairAttempts() int {
	carried := 0
	for _, continuation := range s.RepairContinuations {
		if continuation.Returned {
			continue
		}
		carried += continuation.GrantedAttempts
	}
	return carried
}

// RepairContinuedSince reports a repair grant handed back to this run at or
// after a moment: a continuation somebody decided, which the environment did
// not refuse. It is what says a repair decided at that moment was carried out,
// counted the way CarriedOutRepairAttempts counts it — a returned round bought
// nothing, and the harness carrying on a stall itself spends no grant.
func (s State) RepairContinuedSince(decidedAt time.Time) bool {
	for _, continuation := range s.RepairContinuations {
		if continuation.Returned || continuation.ByHarness {
			continue
		}
		if !continuation.ContinuedAt.Before(decidedAt) {
			return true
		}
	}
	return false
}

// ContinuedStall reports a run the carry-out made live again to carry on a step
// the harness stopped, without charging another developer attempt. It is what
// such a run is recognized by afterwards, and it has to
// be the continuation rather than the environmental account of the stoppage:
// a run that is going again has not stopped, so that account is cleared as the
// re-entry is written, and the continuation is the half of the record that
// survives it.
//
// The most recent continuation is the whole of the answer, for the reason it is
// the whole of what a granted round can be given back from: an earlier one
// describes a re-entry this run has already been through.
func (s State) ContinuedStall() bool {
	last := len(s.RepairContinuations) - 1
	return last >= 0 && s.RepairContinuations[last].Stall
}

// ContinuedCheckStage reports the latest decided repair continuing the checks
// the stage bound stopped, without charging another developer attempt.
func (s State) ContinuedCheckStage() bool {
	last := len(s.RepairContinuations) - 1
	return last >= 0 && s.RepairContinuations[last].CheckStage
}

// ReturnGrantedRound gives back the granted repair round the most recent
// continuation consumed, and reports whether there was one to give back. It is
// the run's half of settling an environmental round: the item's own record keeps
// the grant, and this is what stops the grant reading as carried out.
//
// The most recent continuation is the whole of what can be returned, because it
// is the one that bought the round now settling. An earlier one bought a round
// that was already judged on its own merits.
func (s *State) ReturnGrantedRound() bool {
	last := len(s.RepairContinuations) - 1
	// A settle asked twice for the same round finds the return already made: the
	// record is what the caller wanted it to be, and nothing more is given back.
	if last < 0 || s.RepairContinuations[last].Returned {
		return false
	}
	// A continuation the harness made itself spent no grant, so there is none
	// to give back.
	if s.RepairContinuations[last].ByHarness {
		return false
	}
	s.RepairContinuations[last].Returned = true
	return true
}

// RepairBudget is how many repair attempts this run may make in total: what the
// project configured, plus what triage has granted it since. It is derived here
// rather than at each reader because the two have to agree — the loop that stops
// spending and the prompt that tells a developer which attempt of how many this
// is are the same budget said twice, and a budget that read differently in the
// two would hand back an attempt describing itself as the last.
func (s State) RepairBudget(configured int) int {
	if configured < 0 {
		configured = 0
	}
	return configured + s.GrantedRepairAttempts()
}

// OperatorHeld reports how much of this run's elapsed time the operator's hold
// accounts for. It bounds nothing — nothing bounds an operator — and is the
// ledger's answer to why a run took as long as it did.
func (s State) OperatorHeld() time.Duration {
	return time.Duration(s.OperatorHeldSeconds) * time.Second
}

// Outstanding reports that a run still owes a step somebody has to take. A run
// that never reached a terminal status was interrupted mid-flight. A terminal
// run with nothing integrated owes nothing: its artifacts are deliberately
// preserved. An integrated one owes cleanup until it reaches the complete
// phase, because integration is what schedules the removal of the artifacts
// that produced it, and it owes the answer to a merge the forge queued for as
// long as that merge is unresolved — the forge performs it minutes after the
// run itself is over, and what it did with it has to be found out.
func (s State) Outstanding() bool {
	if !s.Status.Terminal() {
		return true
	}
	if s.Integration == nil {
		return false
	}
	if s.ConfigComparison != nil && s.ConfigComparison.Pending {
		return true
	}
	// A landing whose checks the record says are still running is owed a
	// settlement: the process running them either still holds the run, in which
	// case the sweep leaves it alone, or died, in which case the landing is
	// unverified and its checkout is standing.
	if s.LandingChecks != nil && !s.LandingChecks.Finished() {
		return true
	}
	queued := s.PullRequest != nil && s.PullRequest.MergeQueued
	// A landing through the pull request that ended with the forge neither having
	// merged it nor holding the merge landed nowhere: the run stopped and handed
	// the item to a person, and the harness owes it nothing until a re-arm queues
	// the merge again. Counting it outstanding would have every sweep settle it
	// against a local target it never meant to move.
	if s.Integration.ThroughPullRequest && !queued && (s.PullRequest == nil || !s.PullRequest.Merged) {
		return false
	}
	return s.Phase != PhaseComplete || queued
}

// AwaitingForge reports a promotion whose publication the forge has not
// finished: the run is over, its change is on the target branch, and the pull
// request that carries it to the remote is not recorded merged. A merge the
// forge still has queued is one of these, and so is one it dropped — the drop
// is the moment somebody has to be told about, and the waiting goes on until a
// person or a later sweep resolves it.
//
// It is deliberately not Outstanding above, which asks whether the harness still
// owes a step: a dropped merge stops the harness owing anything the moment the
// run is settled, and the publication is still not on the remote. The two are
// separate questions and reading one for the other is what let a settled run's
// unpublished promotion stop being counted anywhere.
func (s State) AwaitingForge() bool {
	if !s.Status.Terminal() || s.Integration == nil {
		return false
	}
	if s.PullRequest == nil {
		return s.PublicationUnrecorded()
	}
	// A publication handed back for a fresh run, or already superseded by its
	// landing, is not awaited: nothing is waiting on this request.
	if s.PullRequest.HandedBack != nil || s.PullRequest.Superseded != "" {
		return false
	}
	return !s.PullRequest.Merged
}

// lostPublicationAccount is the clause every account of an unrecorded
// publication carries, and the one thing that tells such a record from a local
// promotion: a purely local run promotes and records no request and no failure,
// so the record itself carries nothing else that says the run was publishing.
// The account is what the run writes in place of the request it does not hold,
// and it is written and read through the two functions below so a reader
// selects on exactly the sentence the writer wrote.
const lostPublicationAccount = "its record holds no pull request for branch"

// LostPublication is what a publishing run records about a promotion whose
// pull request it does not hold: the branch, because the branch is the one
// durable handle the forge can still be asked by, and the sweep, because the
// sweep is what turns the account into a request on the record.
func LostPublication(runID, workItemID, targetBranch, branch string) string {
	return fmt.Sprintf("run %s promoted %s into %s and %s %s, so nothing was asked of the forge; `yoyo reconcile` looks the request up on the forge by that branch, records it, and arms its merge",
		runID, workItemID, targetBranch, lostPublicationAccount, branch)
}

// PublicationUnrecorded reports a promotion whose record says it published and
// holds no request: the run is over, its change is on the target branch, its
// reviewer approved it, and what stands in place of the request is the account
// a publishing run writes when it reaches its promotion with none. It is the
// one reading the docket, the status line, and the recovering sweep all take
// of that record, so a promotion in this state is named everywhere a
// publication is before anything has asked the forge about it. The approving
// verdict is asked for as well as the promotion, because a reader that goes on
// to publish the change must not infer the approval from the promotion.
func (s State) PublicationUnrecorded() bool {
	return s.Status.Terminal() &&
		s.Integration != nil &&
		s.PullRequest == nil &&
		s.ReviewDecision == ReviewApprove &&
		strings.TrimSpace(s.Branch) != "" &&
		strings.Contains(s.PublishFailure, lostPublicationAccount)
}

// PublicationUnarmed reports a promotion whose record holds its pull request
// and says nothing ever asked the forge to merge it: the run is over, its change
// is promoted, its reviewer approved it, and the request is neither merged, nor
// queued, nor dropped, with no account of anything having gone wrong beside it.
// A merge the run asked for leaves one of those marks whichever way the forge
// answered, so a record without any of them is a merge nobody made.
//
// Until yoyodyne-ifd.429.31 this state was read as the operator's, and its only
// exit was a person merging the request on the forge. It is now what the docket
// puts to the development manager at once, what the re-arm verb arms on her
// decision, and what a re-run may hand back for a fresh run; this predicate is
// the one reading all three, and the status line, take of it.
//
// A request the forge has closed is excluded: there is nothing left on the forge
// to arm, so the one answer is a re-run, and PublicationUnasked is what says so.
func (s State) PublicationUnarmed() bool {
	return s.PublicationUnasked() && !s.PullRequest.Closed()
}

// PublicationUnasked is PublicationUnarmed with a request the forge closed kept
// in: a promotion whose request nothing ever asked the forge to merge, and that
// nobody has yet handed back for a fresh run. It is what the docket puts to the
// development manager and what a re-run may hand back; whether a re-arm is also
// offered is PublicationUnarmed's.
func (s State) PublicationUnasked() bool {
	if !s.Status.Terminal() || s.Integration == nil || s.PullRequest == nil {
		return false
	}
	published := s.PullRequest
	return s.ReviewDecision == ReviewApprove &&
		!published.Merged &&
		!published.MergeQueued &&
		published.HandedBack == nil &&
		s.MergeDrop == nil &&
		strings.TrimSpace(s.PublishFailure) == ""
}

// Discharges reports whether this run closes its work item. It is the one
// derivation every closure site reads — the run's own completion, the sweep that
// settles a queued merge, and the sweep that finishes an interrupted run — so a
// change that integrates cannot be closed by one of them and left open by
// another.
//
// Two readers of the same change answer it, and either one saying "this is not
// the work" leaves the item open. The developer's claim is one, and the
// reviewer's approval is the other: the claim decides by default and its default
// is the one nobody writes, so an approval that reads the change as evidence is
// the only thing standing between a diagnosis and the closure it would otherwise
// get. yoyodyne-ifd.284 is what that costs when the second reader has nowhere to
// say it.
func (s State) Discharges() bool {
	return s.LandingDischarges() && s.ApprovalDischarges() && !s.Escalated()
}

// DiedBeforeClaiming reports a run that failed before it took its work item.
//
// It is the one failure that leaves nothing at all behind: no blocker, because
// the item was never taken and nothing could write one on it, and no branch or
// checkout, because the worktree is cut immediately after the claim. Everything
// that decides whether a failure is worth somebody's attention reads one of those
// two, so without this the whole class reads as nothing having happened — which
// is how yoyodyne-ifd.285 was dispatched twenty-nine times in twenty hours and
// reached no surface anybody looks at.
//
// The worktree is asked as well as the claim time because the claim time is a
// field yoyodyne-ifd.338 added: every record written before it carries none
// however far the run actually got, and a run with a checkout got past the claim
// whatever its record lost.
//
// It is here rather than beside either caller because two of them ask it — what
// the docket records, and whose move a reader is told follows — and two
// derivations of one fact are two answers about the same run.
func (s State) DiedBeforeClaiming() bool {
	return s.Status == StatusFailed &&
		s.WorkItemClaimedAt == nil &&
		strings.TrimSpace(s.WorktreePath) == "" &&
		strings.TrimSpace(s.Failure) != ""
}

// DiedInItsOwnProcess reports a run that failed inside its own process, handing
// nobody a blocker and integrating nothing, with an account of what killed it.
//
// It is the ending that leaves a stoppage nothing announces. A run whose process
// was killed is settled by a sweep, which blocks the item when the change
// survives; a run that fails inside its own process deliberately writes no
// blocker, because the harness may yet resume it and a blocked item is one it
// would refuse to resume. The item is left claimed, the change is left on its
// branch, and every rule that decides whether a failure is worth somebody's
// attention by reading a blocker reads this as nothing having happened.
//
// It says nothing about whether the change survived, which is a separate
// question each caller asks its own way: the docket asks the run's own removal
// flags at the moment of the death, and the hold the pull reads asks the
// repository. It is here rather than beside either of them because both ask it,
// and two derivations of one fact are two answers about the same run.
func (s State) DiedInItsOwnProcess() bool {
	return s.Status == StatusFailed &&
		s.Integration == nil &&
		strings.TrimSpace(s.Failure) != ""
}

// Escalated reports a run either role ended by saying the work item cannot be
// met as it stands. Nothing is integrated on such a run, the item is parked, and
// what the run produced is a decision for the development manager.
//
// It is asked of the record rather than carried as a field of its own, because
// each role already writes the verb where its own vocabulary lives: the
// developer's is a landing outcome and the reviewer's is a review decision, and a
// third field would be a second answer either of them could contradict.
func (s State) Escalated() bool {
	return s.LandingOutcome == LandingEscalate || s.ReviewDecision == ReviewEscalate
}

// EscalatedBy names the role that raised it, and is empty on every run nobody
// escalated. The reviewer is answered for first because it is the later of the
// two readers: a developer that escalates ends the run before any review, so a
// record carrying the reviewer's verb is one the reviewer raised.
func (s State) EscalatedBy() domain.AgentRole {
	switch {
	case s.ReviewDecision == ReviewEscalate:
		return domain.RoleReviewer
	case s.LandingOutcome == LandingEscalate:
		return domain.RoleDeveloper
	default:
		return ""
	}
}

// EscalationReason is the account the role that raised it gave, in its own
// words. It is the whole of what the development manager decides from, so it is
// read from whichever channel the raiser wrote it in rather than summarized
// anywhere.
func (s State) EscalationReason() string {
	switch {
	case s.ReviewDecision == ReviewEscalate:
		return s.ReviewSummary
	case s.LandingOutcome == LandingEscalate:
		return s.LandingReason
	default:
		return ""
	}
}

// raiseParkingOpening is how the parking an unmeetable raise places on its item
// opens, up to the run identifier and after it. It is spelled once here because
// two packages read it: the run that raises writes it, and the conversation that
// releases the parking reads it back to know the release is the one that ends a
// raise rather than one that lifts somebody's deferral.
const (
	raiseParkingOpening = "yoyodyne run "
	raiseParkingRaised  = " raised this item as one that cannot be met as it stands"
)

// RaiseParking is the parking reason an unmeetable raise places on its item: the
// raising run, what that parking waits on, and the raiser's own account.
func RaiseParking(runID, account string) string {
	return raiseParkingOpening + runID + raiseParkingRaised +
		"; it is with the development manager to decide and is not to be started again until its owner amends and releases it or she retires the raise: " + account
}

// RaisedBy reports the run whose unmeetable raise placed a parking, and whether
// the parking is one at all. A parking anybody else wrote — a deferral, a run's
// evidence landing — reports nothing.
func RaisedBy(parking string) (string, bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(parking), raiseParkingOpening)
	if !found {
		return "", false
	}
	runID, _, found := strings.Cut(rest, raiseParkingRaised)
	if !found || !ValidRunID(runID) {
		return "", false
	}
	return runID, true
}

// LandingDischarges is the developer's half of the question above: whether what
// this run claimed about its own change closes the item.
//
// It answers no in exactly three cases: a claim of evidence, a claim that the
// item cannot be met as it stands, and a claim that arrived unreadable.
// Everything else discharges, which is every run that claimed nothing and every
// run recorded before this channel existed.
func (s State) LandingDischarges() bool {
	return s.LandingOutcome != LandingEvidence && s.LandingOutcome != LandingEscalate && s.LandingProblem == ""
}

// ApprovalDischarges is the reviewer's half: whether the approval this change
// was integrated on is one that closes the item.
//
// A verdict that recorded nothing about what it approves discharges, which is
// every run reviewed before an approval said which it was, and every run whose
// change was never approved at all — the closure sites all ask this of a change
// that integrated, and nothing integrates without an approval.
func (s State) ApprovalDischarges() bool {
	return s.ReviewApproves != ApprovesEvidence
}

// LandingImpediment is the work item this run's landing named as what its own
// item now waits for, and is empty where the landing named nothing the harness
// could use. Whether a named one is usable was decided against the tracker when
// the claim was read, so this only reads the answer back.
//
// An item naming itself answers empty whatever is stored, which the validation
// above also refuses. The derivation is total on purpose: it decides where an
// integrated change's item goes, and a record that reached here malformed must
// take the parking rather than drive a dependency write the tracker refuses.
func (s State) LandingImpediment() string {
	impediment := strings.TrimSpace(s.LandingBlockedBy)
	if impediment == s.WorkItemID {
		return ""
	}
	return impediment
}

// Parks reports an item this run returns to the backlog parked, which is what an
// undischarged run does unless its landing named the impediment it waits for. It
// is derived here beside the closure so that both settlement sites — the run's
// own and the sweep that finishes an interrupted one — put the item in the same
// place.
//
// A claim that could not be read parks too. The marker would have come out of
// the same block the outcome did, so there is nothing to hold the item back with,
// and an item returned bare is one selection picks again immediately. So does an
// item the reviewer approved evidence for: the marker is the developer's channel
// and a reviewer has none, which leaves the parking as the only disposition that
// holds such an item back. And so does an escalated item, which carries no marker
// by contract: the parking is what holds it while the development manager
// decides, and her decision is what releases it.
func (s State) Parks() bool {
	return !s.Discharges() && s.LandingImpediment() == ""
}

// validateIndependentInvocations enforces what an integrated change claims: two
// separate provider invocations, each with its own recorded session and its own
// declared model selector. A missing or reused session identity means nothing
// independent was proven, so it must never appear alongside integration.
func (s State) validateIndependentInvocations() []error {
	var problems []error
	// Compare the normalized identifiers: two sessions that differ only in
	// surrounding whitespace are one session, and must not read as independent.
	developer := strings.TrimSpace(s.ProviderSessionID)
	reviewer := strings.TrimSpace(s.ReviewSessionID)
	if developer == "" || reviewer == "" {
		problems = append(problems, errors.New("integration requires recorded developer and reviewer session identifiers"))
	} else if developer == reviewer {
		problems = append(problems, errors.New("integration requires distinct developer and reviewer session identifiers"))
	}
	if strings.TrimSpace(s.ProviderModel) == "" || strings.TrimSpace(s.ReviewModel) == "" {
		problems = append(problems, errors.New("integration requires recorded developer and reviewer model selectors"))
	}
	return problems
}

// Validate rejects integration evidence that cannot describe a real promotion.
func (i Integration) Validate() error {
	var problems []error
	if strings.TrimSpace(i.TargetBranch) == "" {
		problems = append(problems, errors.New("integration target_branch is required"))
	} else if !validLocalBranch(i.TargetBranch) {
		problems = append(problems, errors.New("integration target_branch must be a local branch name"))
	}
	for _, commit := range []struct {
		field string
		value string
	}{
		{field: "source_commit", value: i.SourceCommit},
		{field: "target_commit", value: i.TargetCommit},
		{field: "previous_target_commit", value: i.PreviousTargetCommit},
	} {
		if !commitPattern.MatchString(commit.value) {
			problems = append(problems, fmt.Errorf("integration %s is invalid", commit.field))
		}
	}
	if i.SourceCommit != "" && i.SourceCommit == i.PreviousTargetCommit {
		problems = append(problems, errors.New("integration did not move the target"))
	}
	// Integration is fast-forward only: the target ends up at exactly the commit
	// the harness created. Any other pair describes a merge or a reset that this
	// harness never performs, so it is rejected rather than recorded.
	if i.SourceCommit != "" && i.TargetCommit != "" && i.SourceCommit != i.TargetCommit {
		problems = append(problems, errors.New("integration target_commit must equal the fast-forwarded source_commit"))
	}
	return errors.Join(problems...)
}

// phaseOrder lists the phases in the order a run reaches them, so evidence can
// be checked against the step that must have produced it.
var phaseOrder = []Phase{
	PhaseDeveloping, PhaseChecking, PhaseReviewing,
	PhaseIntegrating, PhaseCompleting, PhaseCleaningUp, PhaseComplete,
}

// reached reports whether this phase is at or past the given one. An unknown
// phase reaches nothing: it is already rejected as invalid, and treating it as
// satisfying an ordering would let a malformed record pass a coherence check.
func (p Phase) reached(other Phase) bool {
	position := func(phase Phase) int {
		for index, candidate := range phaseOrder {
			if candidate == phase {
				return index
			}
		}
		return -1
	}
	self, target := position(p), position(other)
	return self >= 0 && target >= 0 && self >= target
}

func (p Phase) Valid() bool {
	switch p {
	case PhaseDeveloping, PhaseChecking, PhaseReviewing, PhaseIntegrating, PhaseCompleting, PhaseCleaningUp, PhaseComplete:
		return true
	default:
		return false
	}
}

func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusRunning, StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	default:
		return false
	}
}

func (s Status) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled || s == StatusTimedOut
}

// InFlight reports a run that is still going: reserved and not yet ended,
// whatever phase it is in — a run integrating is as much in flight as one
// developing. It is the complement of Terminal over the valid statuses, and it
// is stated once, here, because three readers have to agree on it: the store's
// listing of incomplete runs, the status surface's count of running runs, and
// the scheduler's reading of which items hold a developer slot and an epic. A
// run that failed, whatever it left behind and whatever a person has yet to
// decide about it, is a record and not one of these.
func (s Status) InFlight() bool {
	return s == StatusPending || s == StatusRunning
}

// HoldsDeveloperSlot reports a run counted against
// execution.max_concurrent_developers: one in flight, and not paused on work its
// item waits on. A dependency pause is recorded as the run exits, so the run
// behind it has no process and spends nothing until that work closes, and a slot
// it went on holding would be throughput lost for as long as the wait lasts — on
// 2026-09-27 that was nineteen hours beside a ready queue. It keeps its claim,
// its branch, its worktree, and its session, and is still in flight in every
// other sense: continuing it takes a slot again under the same limit, through
// Store.ReclaimSlot. The reservation, the scheduler's free slots, the triage
// actions' slot check, and the status's running line all count by this, so what
// each says is free is one fact.
func (s State) HoldsDeveloperSlot() bool {
	return s.Status.InFlight() && s.DependencyPause == nil
}
