package runstate

// What the merge queue's worker verified, kept beside the queue's record:
// docs/designs/integration-through-a-merge-queue.md, "Harness-run queue". An
// admitted entry is verified as a generation — one candidate built from one
// target base and the entry's approved head — and the protected-path gate, the
// checks and the review are each recorded against that generation's binding, a
// digest of everything that makes the candidate what it is. Evidence recorded against one binding
// is no evidence about another, so a generation whose base, heads, candidate,
// content, check configuration, or author changes is a new generation that
// earns its own checks and review from nothing.
//
// The record is one file per entry, written whole by the queue's worker and
// by nothing else: a write presents the worker lease for the queue, so two
// processes never write one entry's generations, and a write cannot be made by
// a caller that does not hold the queue. A save that fails is read back before
// it is answered, as an admission's is.
//
// Nothing here builds a candidate, runs a check, or asks for a review; the
// orchestrator's merge queue worker does those and records them here. Nothing
// here moves a branch either: a verified generation is evidence that a later
// promotion reads, under the promotion lease, and checks again then.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// MergeQueueGenerationSchemaVersion is 1 and has never changed.
const MergeQueueGenerationSchemaVersion = 1

// maxMergeQueueGenerations bounds how many generations one entry keeps. Each
// one is a candidate the target moved out from under, so an entry past this is
// one the target never stops moving under, and that is somebody's to look at
// rather than a record to grow without end.
const maxMergeQueueGenerations = 200

// maxMergeQueueEvidenceText bounds the free text a piece of evidence carries:
// a reviewer's summary, or why a stage did not finish.
const maxMergeQueueEvidenceText = 4 << 10

// maxMergeQueueRefusedPaths bounds the refused paths a generation records,
// as a replay conflict's paths are bounded.
const maxMergeQueueRefusedPaths = MaxConflictedPaths

// maxMergeQueueLaunches bounds the processes one generation records: one per
// configured check and one review, each attempt, for as many attempts as a
// generation that keeps being interrupted plausibly takes.
const maxMergeQueueLaunches = 500

// MergeQueueCheckConfiguration is the checks a generation is verified by: the
// commands the project configured, in order, and a digest of them. A
// configuration that changes is a different gate, so the digest is part of a
// generation's binding.
type MergeQueueCheckConfiguration struct {
	Commands []string `json:"commands"`
	Digest   string   `json:"digest"`
}

// NewMergeQueueCheckConfiguration records a project's configured checks.
func NewMergeQueueCheckConfiguration(commands []string) MergeQueueCheckConfiguration {
	return MergeQueueCheckConfiguration{Commands: append([]string(nil), commands...), Digest: checkConfigurationDigest(commands)}
}

// Same reports whether two configurations are the same checks in the same
// order.
func (c MergeQueueCheckConfiguration) Same(other MergeQueueCheckConfiguration) bool {
	if c.Digest != other.Digest || len(c.Commands) != len(other.Commands) {
		return false
	}
	for index := range c.Commands {
		if c.Commands[index] != other.Commands[index] {
			return false
		}
	}
	return true
}

func checkConfigurationDigest(commands []string) string {
	sum := sha256.New()
	writeBound(sum, "merge-queue-checks/1")
	for _, command := range commands {
		writeBound(sum, command)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// writeBound writes one field length-first, so no two lists of fields digest
// alike by moving text across a boundary.
func writeBound(sum interface{ Write([]byte) (int, error) }, field string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(field)))
	_, _ = sum.Write(length[:])
	_, _ = sum.Write([]byte(field))
}

// MergeQueueGeneration is one candidate for one admitted entry and what was
// learned about it. Everything above CreatedAt is the generation's binding and
// is fixed when the generation is recorded; the evidence below it is added as
// the worker earns it.
type MergeQueueGeneration struct {
	// Number is the generation's place among its entry's, from 1.
	Number     uint64 `json:"number"`
	EntryID    string `json:"entry_id"`
	EntryOrder uint64 `json:"entry_order"`
	// TargetBranch and TargetBase are the target the candidate was built on, as
	// the authoritative branch stood when it was read.
	TargetBranch string `json:"target_branch"`
	TargetBase   string `json:"target_base"`
	// Heads are the approved heads merged onto the base, in the order they were
	// merged. The harness's own queue merges one entry's head at a time.
	Heads []string `json:"heads"`
	// Candidate is the commit the checks and the review judge, and Content is
	// its tree: what a promotion would put on the target.
	Candidate string `json:"candidate"`
	Content   string `json:"content"`
	// Checks is the configured checks the candidate is verified by.
	Checks MergeQueueCheckConfiguration `json:"check_configuration"`
	// AuthorSession is the provider session that wrote the entry's change, which
	// the candidate's reviewer must not be.
	AuthorSession string `json:"author_session"`
	// CreatedAt is when the generation was recorded, which is before any check
	// ran on it.
	CreatedAt time.Time `json:"created_at"`

	// Checkout is where the candidate was checked out for its checks. It is
	// where the candidate is, not what it is, so it is not part of the binding.
	Checkout string `json:"checkout,omitempty"`
	// LastSequence is the last event written to the generation's own event
	// stream, which its checks and its review append to.
	LastSequence uint64 `json:"last_sequence,omitempty"`
	// Paths, CheckRun and Review are the three parts of the candidate's gate:
	// the protected-path gate asked of exactly what would land, the configured
	// checks, and an independent review.
	Paths    *MergeQueuePathEvidence   `json:"paths,omitempty"`
	CheckRun *MergeQueueCheckEvidence  `json:"checks,omitempty"`
	Review   *MergeQueueReviewEvidence `json:"review,omitempty"`
	// BaseCheck is a check that failed on the candidate, asked of the target at
	// the candidate's base, which is what says whether the failure is the
	// change's or the target's (ClassifyMergeQueueFailure in the orchestrator).
	BaseCheck *MergeQueueBaseEvidence `json:"base_check,omitempty"`
	// Interruptions are attempts at a stage that earned nothing and were set
	// aside: started and never ended, or ended without a result. Each is kept so
	// a reader can see why a stage ran twice.
	Interruptions []MergeQueueInterruption `json:"interruptions,omitempty"`
	// Launches are the processes the generation's checks and review were
	// started as, each written down before it could do any work. They are what
	// a later worker reads, beside the stage's hold, to tell whether a process
	// an earlier worker started is still running before it starts another.
	Launches []MergeQueueLaunch `json:"launches,omitempty"`
	// Invalidated is set once the generation can no longer be promoted, and is
	// never cleared: a generation that stopped being promotable is replaced by a
	// new one rather than revived.
	Invalidated *MergeQueueInvalidation `json:"invalidated,omitempty"`
}

// Binding is the digest every piece of this generation's evidence names. A
// change to any field it covers changes it, so evidence recorded against one
// generation never answers for another.
func (g MergeQueueGeneration) Binding() string {
	sum := sha256.New()
	writeBound(sum, "merge-queue-generation/1")
	writeBound(sum, g.EntryID)
	writeBound(sum, fmt.Sprint(g.EntryOrder))
	writeBound(sum, g.TargetBranch)
	writeBound(sum, g.TargetBase)
	writeBound(sum, fmt.Sprint(len(g.Heads)))
	for _, head := range g.Heads {
		writeBound(sum, head)
	}
	writeBound(sum, g.Candidate)
	writeBound(sum, g.Content)
	writeBound(sum, g.Checks.Digest)
	writeBound(sum, g.AuthorSession)
	return hex.EncodeToString(sum.Sum(nil))
}

// EventStream names the generation's own event stream: what its checks and
// its review emit, apart from the run that made the change, whose own stream
// ended when it was admitted.
func (g MergeQueueGeneration) EventStream() string {
	return fmt.Sprintf("%s-generation-%d", g.EntryID, g.Number)
}

// MergeQueueStage names one half of a candidate's gate.
type MergeQueueStage string

const (
	MergeQueueStageChecks MergeQueueStage = "checks"
	MergeQueueStageReview MergeQueueStage = "review"
	// MergeQueueStageBase is a check that failed on the candidate run again on
	// the target at the candidate's base.
	MergeQueueStageBase MergeQueueStage = "base-check"
)

func (s MergeQueueStage) valid() bool {
	return s == MergeQueueStageChecks || s == MergeQueueStageReview || s == MergeQueueStageBase
}

// MergeQueuePathEvidence is the protected-path gate asked of a generation's
// candidate: every path the candidate changes against its base, less what the
// entry's work item grants. A candidate with any path refused earns nothing,
// whatever its checks and review say.
type MergeQueuePathEvidence struct {
	Binding   string    `json:"binding"`
	CheckedAt time.Time `json:"checked_at"`
	// Changed is how many paths the candidate changes against its base.
	Changed int `json:"changed"`
	// Refused is every changed path the gate refuses, and empty where it
	// refuses none.
	Refused []string `json:"refused,omitempty"`
}

// MergeQueueBaseEvidence is one check that failed on a candidate, asked of the
// target at the candidate's base. A candidate's failure is the change's only
// where the target is known to pass the same check at that base; where the
// target fails it too, the failure is the target's.
type MergeQueueBaseEvidence struct {
	Binding    string     `json:"binding"`
	Base       string     `json:"base"`
	Command    string     `json:"command"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// Passed is the check passing on the base by its own exit.
	Passed   bool `json:"passed,omitempty"`
	ExitCode int  `json:"exit_code,omitempty"`
	// KnownFrom says the base's result was known without running the check
	// again, and how: the base is a candidate this queue verified and landed
	// under the checks configured now.
	KnownFrom string `json:"known_from,omitempty"`
	// Problem is why the check judged nothing on the base: stopped, unable to
	// run, or a runner that failed. Evidence with a problem says nothing.
	Problem string `json:"problem,omitempty"`
	// RedItem is the unfinished work item that records the target red on this
	// check, found or filed once the base failed it.
	RedItem string `json:"red_item,omitempty"`
}

// Settled reports base evidence that says something: finished, with nothing
// that stopped it from judging.
func (b *MergeQueueBaseEvidence) Settled() bool {
	return b != nil && b.FinishedAt != nil && b.Problem == ""
}

// WaitsOnTarget reports a generation whose failing check fails on the target
// at its base as well. Nothing about the change can pass that check until the
// target moves, so the entry steps aside for the entries behind it — one of
// which may be the change that makes the target pass again — and is built
// afresh once the target has moved.
func (g MergeQueueGeneration) WaitsOnTarget() bool {
	base := g.BaseCheck
	return g.Invalidated == nil && base.Settled() && !base.Passed && base.Base == g.TargetBase && base.Binding == g.Binding()
}

// MergeQueueCheckEvidence is one run of the configured checks over a
// generation's candidate. It is written when the checks start, with no
// FinishedAt, and again when they end, so a worker that died between the two
// leaves a record that says the checks began and earned nothing.
type MergeQueueCheckEvidence struct {
	Binding    string                  `json:"binding"`
	StartedAt  time.Time               `json:"started_at"`
	FinishedAt *time.Time              `json:"finished_at,omitempty"`
	Results    []MergeQueueCheckResult `json:"results,omitempty"`
	// Problem is why the checks ended without a result for every command: the
	// runner failed, or the stage was stopped. Checks with a problem earn
	// nothing whatever their results say.
	Problem string `json:"problem,omitempty"`
}

// MergeQueueCheckResult is how one configured check ended over a candidate.
type MergeQueueCheckResult struct {
	Command  string `json:"command"`
	Passed   bool   `json:"passed"`
	ExitCode int    `json:"exit_code"`
	// Status is how the check's process ended, in the execution package's words.
	Status string `json:"status"`
	// CouldNotRun is the reason a check gave for judging nothing. A check that
	// judged nothing earned nothing, so a candidate with one is not verified.
	CouldNotRun string `json:"could_not_run,omitempty"`
}

// MergeQueueReviewEvidence is one independent review of a generation's
// candidate, written when it is asked for and again when it is answered.
type MergeQueueReviewEvidence struct {
	Binding    string     `json:"binding"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// Decision is the reviewer's verdict, in the review package's words, and
	// empty where none was reached.
	Decision      string `json:"decision,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	Model         string `json:"model,omitempty"`
	ResolvedModel string `json:"resolved_model,omitempty"`
	Summary       string `json:"summary,omitempty"`
	// Problem is why the review ended without a verdict.
	Problem string `json:"problem,omitempty"`
}

// MergeQueueInterruption is one attempt at a stage that earned nothing and was
// set aside for another: one a later worker found started and never ended, or
// one that ended without a result for every check or without a verdict.
type MergeQueueInterruption struct {
	Stage     MergeQueueStage `json:"stage"`
	StartedAt time.Time       `json:"started_at"`
	FoundAt   time.Time       `json:"found_at"`
	// Problem is why a finished attempt earned nothing, and empty for one that
	// never finished.
	Problem string `json:"problem,omitempty"`
}

// MergeQueueLaunch is one process a stage of a generation was started as.
type MergeQueueLaunch struct {
	Stage MergeQueueStage `json:"stage"`
	// Command is the check the process runs, and empty for the review.
	Command      string    `json:"command,omitempty"`
	Host         string    `json:"host"`
	PID          int       `json:"pid"`
	ProcessGroup int       `json:"process_group"`
	StartedAt    time.Time `json:"started_at"`
}

// MergeQueueInvalidationReason says why a generation stopped being one a
// promotion could use.
type MergeQueueInvalidationReason string

const (
	// MergeQueueTargetMoved is the target branch moving off the generation's
	// base. It says nothing about the change, so it is never charged to the
	// change: the entry is built again on the new target and verified afresh.
	MergeQueueTargetMoved MergeQueueInvalidationReason = "target_moved"
	// MergeQueueChecksChanged is the project's configured checks changing.
	MergeQueueChecksChanged MergeQueueInvalidationReason = "check_configuration_changed"
	// MergeQueueCandidateLost is a candidate commit the repository no longer
	// holds, so nothing could be checked or promoted from it.
	MergeQueueCandidateLost MergeQueueInvalidationReason = "candidate_lost"
)

func (r MergeQueueInvalidationReason) valid() bool {
	return r == MergeQueueTargetMoved || r == MergeQueueChecksChanged || r == MergeQueueCandidateLost
}

// MergeQueueInvalidation is when and why a generation stopped being
// promotable, and what was seen instead.
type MergeQueueInvalidation struct {
	Reason MergeQueueInvalidationReason `json:"reason"`
	// ObservedTarget is where the target stood when the generation was
	// invalidated, where that is the reason.
	ObservedTarget string    `json:"observed_target,omitempty"`
	At             time.Time `json:"at"`
}

// MergeQueueGateError is a generation that is not verified, and why.
type MergeQueueGateError struct {
	Generation uint64
	Reason     string
}

func (e MergeQueueGateError) Error() string {
	return fmt.Sprintf("merge queue generation %d is not verified: %s", e.Generation, e.Reason)
}

// Gate reports whether this generation's candidate earned promotion under the
// checks configured now: the protected-path gate refused none of the paths it
// changes, every configured check ran to its own end and passed over this
// binding, and an independent reviewer approved this binding. Any missing,
// unfinished, mismatched, or failed part refuses, and so does a generation
// already invalidated. It is the only question a promotion asks of
// a generation, and the promotion asks it again under its own lease.
func (g MergeQueueGeneration) Gate(configured MergeQueueCheckConfiguration) error {
	refuse := func(format string, args ...any) error {
		return MergeQueueGateError{Generation: g.Number, Reason: fmt.Sprintf(format, args...)}
	}
	if err := g.validateBinding(); err != nil {
		return refuse("its record is not a whole generation: %v", err)
	}
	if g.Invalidated != nil {
		return refuse("it was invalidated (%s)", g.Invalidated.Reason)
	}
	if !g.Checks.Same(configured) {
		return refuse("it was verified by checks %s, and the project now configures %s", g.Checks.Digest, configured.Digest)
	}
	binding := g.Binding()
	paths := g.Paths
	switch {
	case paths == nil:
		return refuse("no protected-path check was recorded")
	case paths.Binding != binding:
		return refuse("its protected-path check was recorded against another generation")
	case len(paths.Refused) > 0:
		return refuse("it changes protected paths its work item does not grant: %s", strings.Join(paths.Refused, ", "))
	}
	checks := g.CheckRun
	switch {
	case checks == nil:
		return refuse("no checks were recorded")
	case checks.Binding != binding:
		return refuse("its checks were recorded against another generation")
	case checks.FinishedAt == nil:
		return refuse("its checks never finished")
	case checks.Problem != "":
		return refuse("its checks did not complete: %s", checks.Problem)
	case len(checks.Results) != len(g.Checks.Commands):
		return refuse("%d of %d configured checks have a result", len(checks.Results), len(g.Checks.Commands))
	}
	for index, result := range checks.Results {
		switch {
		case result.Command != g.Checks.Commands[index]:
			return refuse("check %d is %q where %q is configured", index+1, result.Command, g.Checks.Commands[index])
		case result.CouldNotRun != "":
			return refuse("%s could not run: %s", result.Command, result.CouldNotRun)
		case !result.Passed:
			return refuse("%s did not pass", result.Command)
		}
	}
	verdict := g.Review
	switch {
	case verdict == nil:
		return refuse("no independent review was recorded")
	case verdict.Binding != binding:
		return refuse("its review was recorded against another generation")
	case verdict.FinishedAt == nil:
		return refuse("its review never finished")
	case verdict.Problem != "":
		return refuse("its review did not complete: %s", verdict.Problem)
	case verdict.Decision != "approve":
		return refuse("its reviewer decided %q, not approve", verdict.Decision)
	case strings.TrimSpace(verdict.SessionID) == "" || strings.TrimSpace(verdict.Model) == "":
		return refuse("its review recorded no session or model")
	case verdict.SessionID == g.AuthorSession:
		return refuse("its reviewer is the session that wrote the change")
	}
	return nil
}

// validateBinding checks the fields the binding covers.
func (g MergeQueueGeneration) validateBinding() error {
	var problems []error
	if g.Number == 0 {
		problems = append(problems, errors.New("generation numbers start at 1"))
	}
	if !mergeQueueEntryIDPattern.MatchString(g.EntryID) {
		problems = append(problems, fmt.Errorf("entry id %q is not a merge queue entry id", g.EntryID))
	}
	if g.EntryOrder == 0 {
		problems = append(problems, errors.New("entry order starts at 1"))
	}
	if !validLocalBranch(g.TargetBranch) {
		problems = append(problems, fmt.Errorf("target branch %q is not a local branch name", g.TargetBranch))
	}
	for field, value := range map[string]string{"target base": g.TargetBase, "candidate": g.Candidate, "content": g.Content} {
		if !commitPattern.MatchString(value) {
			problems = append(problems, fmt.Errorf("%s %q is not a full object id", field, value))
		}
	}
	if len(g.Heads) == 0 {
		problems = append(problems, errors.New("a candidate merges at least one head"))
	}
	for _, head := range g.Heads {
		if !commitPattern.MatchString(head) {
			problems = append(problems, fmt.Errorf("head %q is not a full commit id", head))
		}
	}
	if len(g.Checks.Commands) == 0 {
		problems = append(problems, errors.New("a candidate is verified by at least one configured check"))
	}
	if g.Checks.Digest != checkConfigurationDigest(g.Checks.Commands) {
		problems = append(problems, errors.New("the check configuration's digest is not its commands'"))
	}
	if err := mergeQueueText("author session", g.AuthorSession, true); err != nil {
		problems = append(problems, err)
	}
	if g.CreatedAt.IsZero() {
		problems = append(problems, errors.New("creation time is required"))
	}
	return errors.Join(problems...)
}

// validate checks a whole generation: its binding, and that every piece of
// evidence it carries names that binding. Evidence about another binding is
// never written, so it is never there to be mistaken for this one's.
func (g MergeQueueGeneration) validate() error {
	problems := []error{g.validateBinding()}
	binding := g.Binding()
	if g.Paths != nil {
		if g.Paths.Binding != binding || g.Paths.CheckedAt.IsZero() || g.Paths.Changed < len(g.Paths.Refused) {
			problems = append(problems, errors.New("its protected-path check is not a whole record of this generation"))
		}
		if len(g.Paths.Refused) > maxMergeQueueRefusedPaths {
			problems = append(problems, fmt.Errorf("%d refused paths exceeds the %d a generation keeps", len(g.Paths.Refused), maxMergeQueueRefusedPaths))
		}
		for _, refused := range g.Paths.Refused {
			if refused == "" || len(refused) > maxMergeQueueEvidenceText {
				problems = append(problems, errors.New("a refused path is empty or exceeds its bound"))
			}
		}
	}
	if base := g.BaseCheck; base != nil {
		if base.Binding != binding || base.Base != g.TargetBase || base.StartedAt.IsZero() || mergeQueueText("base check command", base.Command, true) != nil {
			problems = append(problems, errors.New("its base check is not a whole record of this generation's base"))
		}
		if len(base.Problem) > maxMergeQueueEvidenceText || len(base.KnownFrom) > maxMergeQueueEvidenceText || mergeQueueText("red item", base.RedItem, false) != nil {
			problems = append(problems, errors.New("its base check's text exceeds its bound"))
		}
	}
	if g.CheckRun != nil {
		if g.CheckRun.Binding != binding {
			problems = append(problems, errors.New("its checks name another generation"))
		}
		if g.CheckRun.StartedAt.IsZero() {
			problems = append(problems, errors.New("its checks record no start"))
		}
		if len(g.CheckRun.Problem) > maxMergeQueueEvidenceText {
			problems = append(problems, errors.New("its checks' problem exceeds its bound"))
		}
		for _, result := range g.CheckRun.Results {
			if len(result.CouldNotRun) > maxMergeQueueEvidenceText {
				problems = append(problems, errors.New("the reason a check gave for not running exceeds its bound"))
			}
		}
	}
	if g.Review != nil {
		if g.Review.Binding != binding {
			problems = append(problems, errors.New("its review names another generation"))
		}
		if g.Review.StartedAt.IsZero() {
			problems = append(problems, errors.New("its review records no start"))
		}
		if len(g.Review.Summary) > maxMergeQueueEvidenceText || len(g.Review.Problem) > maxMergeQueueEvidenceText {
			problems = append(problems, errors.New("its review's text exceeds its bound"))
		}
	}
	if len(g.Launches) > maxMergeQueueLaunches {
		problems = append(problems, fmt.Errorf("%d launches exceeds the %d a generation keeps", len(g.Launches), maxMergeQueueLaunches))
	}
	for _, launch := range g.Launches {
		if !launch.Stage.valid() || launch.PID <= 0 || launch.StartedAt.IsZero() ||
			len(launch.Command) > maxMergeQueueEvidenceText || mergeQueueText("launch host", launch.Host, true) != nil {
			problems = append(problems, fmt.Errorf("a launch of the %s is not a whole record of a process", launch.Stage))
		}
	}
	for _, interruption := range g.Interruptions {
		if len(interruption.Problem) > maxMergeQueueEvidenceText {
			problems = append(problems, errors.New("an interruption's problem exceeds its bound"))
		}
		if !interruption.Stage.valid() {
			problems = append(problems, fmt.Errorf("interrupted stage %q is not one of the stages a generation has", interruption.Stage))
		}
	}
	if g.Invalidated != nil {
		if !g.Invalidated.Reason.valid() {
			problems = append(problems, fmt.Errorf("invalidation reason %q is not one this build knows", g.Invalidated.Reason))
		}
		if g.Invalidated.At.IsZero() {
			problems = append(problems, errors.New("invalidation time is required"))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("merge queue generation %d: %w", g.Number, err)
	}
	return nil
}

// MergeQueueGenerations is one entry's record: every generation built for it,
// in order.
type MergeQueueGenerations struct {
	SchemaVersion int                    `json:"schema_version"`
	ProductID     domain.ProductID       `json:"product_id"`
	Repository    string                 `json:"repository"`
	TargetBranch  string                 `json:"target_branch"`
	EntryID       string                 `json:"entry_id"`
	Generations   []MergeQueueGeneration `json:"generations"`
}

func (r MergeQueueGenerations) validate(productID domain.ProductID, key MergeQueueKey, entryID string) error {
	var problems []error
	if r.SchemaVersion != MergeQueueGenerationSchemaVersion {
		problems = append(problems, fmt.Errorf("merge queue generations schema version %d is not supported", r.SchemaVersion))
	}
	if r.ProductID != productID || r.Repository != key.Repository || r.TargetBranch != key.TargetBranch || r.EntryID != entryID {
		problems = append(problems, fmt.Errorf("the record is for entry %s of %s on %q, not entry %s of %s on %q",
			r.EntryID, r.TargetBranch, r.Repository, entryID, key.TargetBranch, key.Repository))
	}
	if len(r.Generations) > maxMergeQueueGenerations {
		problems = append(problems, fmt.Errorf("%d generations exceeds the %d an entry keeps", len(r.Generations), maxMergeQueueGenerations))
	}
	for index, generation := range r.Generations {
		if err := generation.validate(); err != nil {
			problems = append(problems, err)
			continue
		}
		if generation.Number != uint64(index+1) {
			problems = append(problems, fmt.Errorf("generation %d is recorded in place %d", generation.Number, index+1))
		}
		if generation.EntryID != entryID || generation.TargetBranch != key.TargetBranch {
			problems = append(problems, fmt.Errorf("generation %d belongs to another entry", generation.Number))
		}
		// Only the newest generation may stand: each one before it was replaced
		// because it stopped being promotable, so at most one is live at once.
		if index < len(r.Generations)-1 && generation.Invalidated == nil {
			problems = append(problems, fmt.Errorf("generation %d was replaced without being invalidated", generation.Number))
		}
	}
	return errors.Join(problems...)
}

// ErrMergeQueueWorkerLeaseRequired is a generation write made without the
// worker lease for its queue.
var ErrMergeQueueWorkerLeaseRequired = errors.New("only the merge queue's worker records its generations, and the lease presented is not that queue's worker lease")

// ErrMergeQueueGenerationSaveUncertain is a generation write that failed in a
// way that does not say whether it landed, and whose readback could not
// settle it either. The worker settles it by reading the record again before
// it does anything that depends on it.
var ErrMergeQueueGenerationSaveUncertain = errors.New("the merge queue generation may or may not have been saved, and reading it back did not say")

// MergeQueueGenerationConflictError is a write that would change what a
// recorded generation already says in a way a generation never changes.
type MergeQueueGenerationConflictError struct {
	Generation uint64
	Reason     string
}

func (e MergeQueueGenerationConflictError) Error() string {
	return fmt.Sprintf("merge queue generation %d cannot be recorded: %s", e.Generation, e.Reason)
}

// Generations reports every generation recorded for one entry, oldest first.
// An entry nothing has been built for has none; a record that cannot be read
// is an error, never an empty list.
func (s *MergeQueueStore) Generations(key MergeQueueKey, entryID string) ([]MergeQueueGeneration, error) {
	if err := key.validate(); err != nil {
		return nil, fmt.Errorf("merge queue: %w", err)
	}
	if !mergeQueueEntryIDPattern.MatchString(entryID) {
		return nil, fmt.Errorf("entry id %q is not a merge queue entry id", entryID)
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, fmt.Errorf("pin the merge queue state root: %w", err)
	}
	defer root.Close()
	queueRoot, err := root.OpenDirectory(path.Join(filepath.ToSlash(s.directory()), key.directory()))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the merge queue for %s: %w", key.TargetBranch, err)
	}
	defer queueRoot.Close()
	record, err := s.loadGenerations(queueRoot, key, entryID, false)
	if err != nil {
		return nil, err
	}
	return record.Generations, nil
}

// RecordGeneration writes one generation of an entry: a new one, numbered one
// past the last and recorded only once every earlier one is invalidated, or a
// recorded one with evidence added. A recorded generation's binding is never
// rewritten, its evidence is never moved to another binding, a stage that
// finished is never replaced, and an invalidation is never withdrawn; a write
// that would do any of those is refused with MergeQueueGenerationConflictError.
//
// The worker lease for the queue is what a caller presents to write: the
// worker is the one process working the queue, and holding its lease is what
// makes this record one nobody else is writing.
func (s *MergeQueueStore) RecordGeneration(worker *Lease, key MergeQueueKey, generation MergeQueueGeneration) error {
	if err := key.validate(); err != nil {
		return fmt.Errorf("merge queue: %w", err)
	}
	if worker == nil || worker.file == nil || worker.label != mergeQueueWorkerLabel || worker.scope != key.directory() {
		return ErrMergeQueueWorkerLeaseRequired
	}
	if err := generation.validate(); err != nil {
		return err
	}
	if generation.TargetBranch != key.TargetBranch {
		return fmt.Errorf("merge queue generation %d is for %s, not %s", generation.Number, generation.TargetBranch, key.TargetBranch)
	}
	queueRoot, err := s.openQueue(key)
	if err != nil {
		return err
	}
	defer queueRoot.Close()
	record, err := s.loadGenerations(queueRoot, key, generation.EntryID, true)
	if err != nil {
		return err
	}
	count := uint64(len(record.Generations))
	switch {
	case generation.Number == count+1:
		if count > 0 && record.Generations[count-1].Invalidated == nil {
			return MergeQueueGenerationConflictError{Generation: generation.Number, Reason: fmt.Sprintf("generation %d still stands and has to be invalidated first", count)}
		}
		record.Generations = append(record.Generations, generation)
	case generation.Number >= 1 && generation.Number <= count:
		if err := revisable(record.Generations[generation.Number-1], generation); err != nil {
			return err
		}
		record.Generations[generation.Number-1] = generation
	default:
		return MergeQueueGenerationConflictError{Generation: generation.Number, Reason: fmt.Sprintf("the entry has %d generations, so the next is %d", count, count+1)}
	}
	if err := record.validate(s.productID, key, generation.EntryID); err != nil {
		return err
	}
	encoded, err := encodeRecord("merge queue generations", record)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedStateBytes {
		return StopError{Class: StopStateBound, Cause: fmt.Errorf("the merge queue generations for entry %s would be %d bytes, limit is %d", generation.EntryID, len(encoded), maxEncodedStateBytes)}
	}
	name := generationsRecordName(generation.EntryID)
	saveErr := s.saveGenerations(queueRoot, name, encoded)
	if saveErr == nil {
		return nil
	}
	// Whether a failed write landed is what the record says, as it is for an
	// admission: a rename can succeed and the directory sync behind it fail.
	landed, readErr := s.readGenerationsBack(queueRoot, name)
	if errors.Is(readErr, fs.ErrNotExist) {
		return fmt.Errorf("save merge queue generation %d of entry %s: %w; reading it back found no record", generation.Number, generation.EntryID, saveErr)
	}
	if readErr != nil {
		return fmt.Errorf("%w: save: %w; readback: %w", ErrMergeQueueGenerationSaveUncertain, saveErr, readErr)
	}
	if string(landed) == string(encoded) {
		return nil
	}
	return fmt.Errorf("save merge queue generation %d of entry %s: %w; reading it back found it was not saved", generation.Number, generation.EntryID, saveErr)
}

// revisable refuses a write that changes what a generation already says
// rather than adding to it.
func revisable(recorded, revised MergeQueueGeneration) error {
	conflict := func(reason string) error {
		return MergeQueueGenerationConflictError{Generation: recorded.Number, Reason: reason}
	}
	if recorded.Binding() != revised.Binding() || !recorded.CreatedAt.Equal(revised.CreatedAt) {
		return conflict("its binding is fixed when it is recorded")
	}
	if recorded.Invalidated != nil && (revised.Invalidated == nil || *revised.Invalidated != *recorded.Invalidated) {
		return conflict("an invalidation is never withdrawn or rewritten")
	}
	// A finished stage that reached a result is never replaced; one that ended
	// without one earned nothing, and is set aside for another attempt.
	if recorded.CheckRun != nil && recorded.CheckRun.FinishedAt != nil && recorded.CheckRun.Problem == "" && !sameCheckEvidence(recorded.CheckRun, revised.CheckRun) {
		return conflict("its checks finished, and finished checks are never replaced")
	}
	if recorded.Review != nil && recorded.Review.FinishedAt != nil && recorded.Review.Problem == "" && !sameReviewEvidence(recorded.Review, revised.Review) {
		return conflict("its review finished, and a finished review is never replaced")
	}
	if recorded.Paths != nil && !samePathEvidence(recorded.Paths, revised.Paths) {
		return conflict("its protected-path check is recorded, and is never replaced")
	}
	if recorded.BaseCheck.Settled() && !sameBaseEvidence(recorded.BaseCheck, revised.BaseCheck) {
		return conflict("its base check finished, and a finished base check is never replaced")
	}
	if revised.LastSequence < recorded.LastSequence {
		return conflict("its event stream never goes backwards")
	}
	if len(revised.Launches) < len(recorded.Launches) {
		return conflict("a launch is never removed")
	}
	for index, launch := range recorded.Launches {
		if revised.Launches[index] != launch {
			return conflict("a launch is never rewritten")
		}
	}
	if len(revised.Interruptions) < len(recorded.Interruptions) {
		return conflict("an interruption is never removed")
	}
	for index, interruption := range recorded.Interruptions {
		if revised.Interruptions[index] != interruption {
			return conflict("an interruption is never rewritten")
		}
	}
	return nil
}

func sameCheckEvidence(a, b *MergeQueueCheckEvidence) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Binding != b.Binding || !a.StartedAt.Equal(b.StartedAt) || !sameTime(a.FinishedAt, b.FinishedAt) || a.Problem != b.Problem || len(a.Results) != len(b.Results) {
		return false
	}
	for index := range a.Results {
		if a.Results[index] != b.Results[index] {
			return false
		}
	}
	return true
}

func sameReviewEvidence(a, b *MergeQueueReviewEvidence) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Binding == b.Binding && a.StartedAt.Equal(b.StartedAt) && sameTime(a.FinishedAt, b.FinishedAt) &&
		a.Decision == b.Decision && a.SessionID == b.SessionID && a.Model == b.Model &&
		a.ResolvedModel == b.ResolvedModel && a.Summary == b.Summary && a.Problem == b.Problem
}

func samePathEvidence(a, b *MergeQueuePathEvidence) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Binding != b.Binding || !a.CheckedAt.Equal(b.CheckedAt) || a.Changed != b.Changed || len(a.Refused) != len(b.Refused) {
		return false
	}
	for index := range a.Refused {
		if a.Refused[index] != b.Refused[index] {
			return false
		}
	}
	return true
}

// sameBaseEvidence compares a finished base check with its revision, which
// may add the red item it was found or filed under and nothing else.
func sameBaseEvidence(a, b *MergeQueueBaseEvidence) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.RedItem != "" && a.RedItem != b.RedItem {
		return false
	}
	return a.Binding == b.Binding && a.Base == b.Base && a.Command == b.Command && a.StartedAt.Equal(b.StartedAt) &&
		sameTime(a.FinishedAt, b.FinishedAt) && a.Passed == b.Passed && a.ExitCode == b.ExitCode &&
		a.KnownFrom == b.KnownFrom && a.Problem == b.Problem
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// VerifiedGeneration is the generation of an entry a promotion may use: the
// newest one, when it passes its gate under the checks configured now. Every
// other answer is an error saying why there is none, and a
// MergeQueueGateError where a generation exists and has not earned it.
func (s *MergeQueueStore) VerifiedGeneration(key MergeQueueKey, entryID string, configured MergeQueueCheckConfiguration) (MergeQueueGeneration, error) {
	generations, err := s.Generations(key, entryID)
	if err != nil {
		return MergeQueueGeneration{}, err
	}
	if len(generations) == 0 {
		return MergeQueueGeneration{}, fmt.Errorf("entry %s has no candidate generation recorded", entryID)
	}
	latest := generations[len(generations)-1]
	if err := latest.Gate(configured); err != nil {
		return MergeQueueGeneration{}, err
	}
	return latest, nil
}

func generationsRecordName(entryID string) string {
	return "generations-" + entryID + ".json"
}

func (s *MergeQueueStore) loadGenerations(queueRoot *repowrite.PinnedRoot, key MergeQueueKey, entryID string, strict bool) (MergeQueueGenerations, error) {
	encoded, err := readGenerations(queueRoot, generationsRecordName(entryID))
	if errors.Is(err, fs.ErrNotExist) {
		return MergeQueueGenerations{
			SchemaVersion: MergeQueueGenerationSchemaVersion, ProductID: s.productID,
			Repository: key.Repository, TargetBranch: key.TargetBranch, EntryID: entryID,
		}, nil
	}
	if err != nil {
		return MergeQueueGenerations{}, fmt.Errorf("read the merge queue generations of entry %s: %w", entryID, err)
	}
	return s.decodeGenerations(encoded, key, entryID, strict)
}

func (s *MergeQueueStore) decodeGenerations(encoded []byte, key MergeQueueKey, entryID string, strict bool) (MergeQueueGenerations, error) {
	var record MergeQueueGenerations
	var err error
	if strict {
		err = decodeStrictly(encoded, &record)
	} else {
		var unknown []string
		unknown, err = decodeTolerating(encoded, &record)
		noteUnknownFields("merge queue generations", unknown)
	}
	if err != nil {
		return MergeQueueGenerations{}, fmt.Errorf("decode the merge queue generations of entry %s: %w", entryID, err)
	}
	if err := record.validate(s.productID, key, entryID); err != nil {
		return MergeQueueGenerations{}, fmt.Errorf("the merge queue generations of entry %s: %w", entryID, err)
	}
	return record, nil
}

func (s *MergeQueueStore) saveGenerations(queueRoot *repowrite.PinnedRoot, name string, encoded []byte) error {
	if s.saveGeneration != nil {
		return s.saveGeneration(queueRoot, name, encoded)
	}
	if err := queueRoot.WriteFile(name, encoded, 0o600, false); err != nil {
		return err
	}
	return queueRoot.Sync()
}

func (s *MergeQueueStore) readGenerationsBack(queueRoot *repowrite.PinnedRoot, name string) ([]byte, error) {
	if s.readGenerationBack != nil {
		return s.readGenerationBack(queueRoot, name)
	}
	return readGenerations(queueRoot, name)
}

func readGenerations(queueRoot *repowrite.PinnedRoot, name string) ([]byte, error) {
	encoded, err := queueRoot.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxEncodedStateBytes {
		return nil, fmt.Errorf("the merge queue generations record is %d bytes, which exceeds the %d byte bound", len(encoded), maxEncodedStateBytes)
	}
	return encoded, nil
}
