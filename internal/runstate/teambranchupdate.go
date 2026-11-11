package runstate

// The durable record of bringing one target branch up to date with the team's
// branch (docs/designs/integrating-to-a-local-branch.md, "Keeping the target
// branch current with the team's branch"). With execution.upstream_branch set,
// the harness builds a merge of the team's branch tip into the target, checks
// and reviews that exact merge commit, and lands it with compare-and-swap. This
// file keeps what each such attempt did, so a restart resumes it, a conflict or
// refusal is reported once, and every surface says the same thing. Nothing here
// builds a merge, runs a check, or moves a branch.
//
// One record exists per product and target branch, written through the shared
// confined write primitive. Working the update is a lease beside the record,
// a file lock like every lease in this package, so a process that died leaves
// no owner behind.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// TeamBranchUpdateSchemaVersion is the version of the record's shape.
const TeamBranchUpdateSchemaVersion = 1

// MaxTeamBranchUpdatesKept is how many landed updates a record keeps, oldest
// dropped first.
const MaxTeamBranchUpdatesKept = 20

const (
	teamBranchUpdatesDirectory = "team-branch-updates"
	teamBranchUpdateRecordName = "update.json"
	teamBranchUpdateLeaseName  = "updater.lock"
	teamBranchUpdateLeaseLabel = "team branch updater"

	// teamBranchApprove is the reviewer's approving decision, the value of
	// review.DecisionApprove. internal/review imports this package, so it cannot
	// be named here, so the two are kept equal by hand.
	teamBranchApprove = "approve"
)

// TeamBranchUpdate is one target branch's record of being brought up to date
// with the team's branch.
type TeamBranchUpdate struct {
	SchemaVersion  int    `json:"schema_version"`
	TargetBranch   string `json:"target_branch"`
	UpstreamBranch string `json:"upstream_branch"`
	// CheckedAt is the last time a check read both branches; CurrentAt the last
	// time it found the target already carrying the team's tip (CurrentWith that
	// tip).
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	CurrentAt   *time.Time `json:"current_at,omitempty"`
	CurrentWith string     `json:"current_with,omitempty"`
	// Pending is the update in progress or waiting, nil where none is.
	Pending *TeamBranchUpdateAttempt `json:"pending,omitempty"`
	// Landed is every update that landed, oldest first, at most
	// MaxTeamBranchUpdatesKept.
	Landed []TeamBranchUpdateAttempt `json:"landed,omitempty"`
}

// TeamBranchUpdateAttempt is one try at merging the team's branch tip into the
// target.
type TeamBranchUpdateAttempt struct {
	// TeamCommit is the tip of the team's branch brought in.
	TeamCommit string `json:"team_commit"`
	// TargetBase is the target commit the merge is built on.
	TargetBase string    `json:"target_base"`
	StartedAt  time.Time `json:"started_at"`
	// Candidate is the merge commit; empty for a conflict.
	Candidate        string                    `json:"candidate,omitempty"`
	Checks           []TeamBranchUpdateCheck   `json:"checks,omitempty"`
	ChecksFinishedAt *time.Time                `json:"checks_finished_at,omitempty"`
	Review           *TeamBranchUpdateReview   `json:"review,omitempty"`
	Conflict         *TeamBranchUpdateConflict `json:"conflict,omitempty"`
	// Resolution is the development manager's decision to have a run resolve
	// the conflict, and the rounds spent on it. It outlives the conflict itself:
	// a conflict resolved keeps it, so the record says how it was resolved.
	Resolution *TeamBranchResolution `json:"resolution,omitempty"`
	// Refused is why the update does not land as it stands (a check failed, the
	// reviewer did not approve), said in plain words; the update waits for either
	// branch to move.
	Refused   string     `json:"refused,omitempty"`
	RefusedAt *time.Time `json:"refused_at,omitempty"`
	// ReportedAt is when the development manager was told about the conflict or
	// refusal, so it is told once.
	ReportedAt     *time.Time `json:"reported_at,omitempty"`
	PullRequest    int        `json:"pull_request,omitempty"`
	PullRequestURL string     `json:"pull_request_url,omitempty"`
	// MergeRequestedAt is when the forge was asked to merge the pull request.
	MergeRequestedAt *time.Time `json:"merge_requested_at,omitempty"`
	LandedAt         *time.Time `json:"landed_at,omitempty"`
	// LandedCommit is the commit the target stands at with the update in it: the
	// candidate, or the forge's merge commit.
	LandedCommit string `json:"landed_commit,omitempty"`
}

// TeamBranchUpdateCheck is one configured check run on the candidate.
type TeamBranchUpdateCheck struct {
	Command     string `json:"command"`
	Passed      bool   `json:"passed"`
	ExitCode    int    `json:"exit_code"`
	Status      string `json:"status,omitempty"`
	CouldNotRun string `json:"could_not_run,omitempty"`
}

// TeamBranchUpdateReview is the independent review of the candidate.
type TeamBranchUpdateReview struct {
	SessionID  string    `json:"session_id,omitempty"`
	Model      string    `json:"model,omitempty"`
	Decision   string    `json:"decision,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	FinishedAt time.Time `json:"finished_at"`
}

// TeamBranchUpdateConflict is a merge that conflicts, which the harness does not
// resolve itself: the development manager names an item whose run resolves it
// (TeamBranchResolution).
type TeamBranchUpdateConflict struct {
	Paths []string `json:"paths"`
	// Items are the work items on the target whose landed commits touched those
	// paths.
	Items   []TeamBranchConflictItem `json:"items,omitempty"`
	FoundAt time.Time                `json:"found_at"`
}

// TeamBranchConflictItem is a work item named in a conflict, by the title it
// is known by with its identifier after it.
type TeamBranchConflictItem struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// Says is the item as every surface names it: its title with its identifier
// after it, or the identifier alone where no title is known.
func (i TeamBranchConflictItem) Says() string {
	title := strings.Join(strings.Fields(i.Title), " ")
	if title == "" {
		return i.ID
	}
	return fmt.Sprintf("%s (%s)", title, i.ID)
}

// MaxTeamBranchResolutionRounds is how many rounds of resolving one conflict the
// harness runs. The limit is the conflict's own and is not any item's repair
// budget, because another contributor's work caused the conflict.
const MaxTeamBranchResolutionRounds = 3

// TeamBranchResolution is the decision that a run of one item resolves the
// conflict on the target branch, and each round of that run.
type TeamBranchResolution struct {
	// WorkItemID and WorkItemTitle are the item whose run resolves the conflict.
	WorkItemID    string `json:"work_item_id"`
	WorkItemTitle string `json:"work_item_title,omitempty"`
	Reason        string `json:"reason"`
	// DecidedBy is the role that decided, such as "development-manager", or
	// "person".
	DecidedBy    string    `json:"decided_by"`
	Conversation string    `json:"conversation,omitempty"`
	Turn         int       `json:"turn,omitempty"`
	DecidedAt    time.Time `json:"decided_at"`
	// Paths are the files that conflicted when the first round started, kept
	// because a conflict resolved no longer carries them.
	Paths []string `json:"paths,omitempty"`
	// Rounds are the runs made so far, oldest first.
	Rounds []TeamBranchResolutionRound `json:"rounds,omitempty"`
	// SpentReportedAt is when the development manager was told every round was
	// used, so it is told once.
	SpentReportedAt *time.Time `json:"spent_reported_at,omitempty"`
	// Restarts counts the rounds that were not started, or were ended for a cause
	// outside the work, such as a paused provider or a process that died before
	// the developer's change was committed. None of them used a round; each makes
	// the next run of the conflict a new one.
	Restarts int `json:"restarts,omitempty"`
}

// TeamBranchResolutionRound is one run that tried to resolve the conflict.
type TeamBranchResolutionRound struct {
	RunID     string     `json:"run_id"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// Resolved is the approved head commit of the run, where it resolved the
	// conflict.
	Resolved string `json:"resolved,omitempty"`
	// Why is, in plain words, why the round did not resolve the conflict.
	Why string `json:"why,omitempty"`
}

// Says is the item the decision names, as every surface names it.
func (r TeamBranchResolution) Says() string {
	return TeamBranchConflictItem{ID: r.WorkItemID, Title: r.WorkItemTitle}.Says()
}

// RoundsLeft is how many rounds the conflict still has.
func (r TeamBranchResolution) RoundsLeft() int {
	return max(0, MaxTeamBranchResolutionRounds-len(r.Rounds))
}

// Spent reports every round of resolving the conflict has been used.
func (r TeamBranchResolution) Spent() bool { return r.RoundsLeft() == 0 }

// InProgress is the round that has started and not ended, or nil.
func (r *TeamBranchResolution) InProgress() *TeamBranchResolutionRound {
	if r == nil {
		return nil
	}
	for i := range r.Rounds {
		if r.Rounds[i].EndedAt == nil {
			return &r.Rounds[i]
		}
	}
	return nil
}

// ResolvedBy is the approved head of the round that resolved the conflict, and
// false where none did.
func (r *TeamBranchResolution) ResolvedBy() (TeamBranchResolutionRound, bool) {
	if r == nil {
		return TeamBranchResolutionRound{}, false
	}
	for _, round := range r.Rounds {
		if round.Resolved != "" {
			return round, true
		}
	}
	return TeamBranchResolutionRound{}, false
}

func (r TeamBranchResolution) validate(name string) error {
	var problems []error
	if strings.TrimSpace(r.WorkItemID) == "" {
		problems = append(problems, fmt.Errorf("%s resolution names no work item", name))
	}
	if strings.TrimSpace(r.Reason) == "" {
		problems = append(problems, fmt.Errorf("%s resolution gives no reason", name))
	}
	if strings.TrimSpace(r.DecidedBy) == "" {
		problems = append(problems, fmt.Errorf("%s resolution does not say who decided", name))
	}
	if r.DecidedAt.IsZero() {
		problems = append(problems, fmt.Errorf("%s resolution has no decided_at", name))
	}
	if len(r.Rounds) > MaxTeamBranchResolutionRounds {
		problems = append(problems, fmt.Errorf("%s resolution has %d rounds, more than the %d allowed", name, len(r.Rounds), MaxTeamBranchResolutionRounds))
	}
	open := 0
	for i, round := range r.Rounds {
		if !runIDPattern.MatchString(round.RunID) {
			problems = append(problems, fmt.Errorf("%s resolution round %d has an invalid run id", name, i+1))
		}
		if round.StartedAt.IsZero() {
			problems = append(problems, fmt.Errorf("%s resolution round %d has no started_at", name, i+1))
		}
		if round.Resolved != "" && !commitPattern.MatchString(round.Resolved) {
			problems = append(problems, fmt.Errorf("%s resolution round %d has an invalid resolved commit", name, i+1))
		}
		if round.EndedAt == nil {
			open++
		}
	}
	if open > 1 {
		problems = append(problems, fmt.Errorf("%s resolution has %d rounds in progress at once", name, open))
	}
	return errors.Join(problems...)
}

// TeamBranchResolutionDecision is a decision, as the development manager's
// recorded decision hands it over, that a run of one item resolves the conflict
// the update on a target branch is waiting on.
type TeamBranchResolutionDecision struct {
	TargetBranch  string
	WorkItemID    string
	WorkItemTitle string
	Reason        string
	// DecidedBy is the role that decided.
	DecidedBy    string
	Conversation string
	Turn         int
	At           time.Time
}

// DecideResolution records the decision on the update waiting on a conflict.
// It takes the updater's lease without waiting, so it never changes a record the
// updater is working from, and refuses in plain words where the decision cannot
// stand: no update is waiting on a conflict, the reason or the item is missing,
// every round of resolving this conflict is used, or a round is running now.
// A decision made on an update that already carries one replaces the item, the
// reason and the decider and keeps the rounds spent, because the limit belongs
// to the conflict and not to the decision.
func (s *TeamBranchUpdateStore) DecideResolution(ctx context.Context, decision TeamBranchResolutionDecision) (TeamBranchUpdate, error) {
	if strings.TrimSpace(decision.WorkItemID) == "" {
		return TeamBranchUpdate{}, errors.New("a decision to resolve the conflict has to name the item whose run resolves it")
	}
	if strings.TrimSpace(decision.Reason) == "" {
		return TeamBranchUpdate{}, errors.New("a decision to resolve the conflict has to give its reason")
	}
	if strings.TrimSpace(decision.DecidedBy) == "" {
		return TeamBranchUpdate{}, errors.New("a decision to resolve the conflict has to say who made it")
	}
	lease, held, err := s.LeaseUpdater(ctx, decision.TargetBranch)
	if err != nil {
		return TeamBranchUpdate{}, err
	}
	if !held {
		return TeamBranchUpdate{}, fmt.Errorf("the update of %s is being worked on right now, so the decision was not recorded; record it again in a minute", decision.TargetBranch)
	}
	defer func() { _ = lease.Release() }()
	update, found, err := s.Load(decision.TargetBranch)
	if err != nil {
		return TeamBranchUpdate{}, err
	}
	if !found || update.Pending == nil || update.Pending.Conflict == nil {
		return TeamBranchUpdate{}, fmt.Errorf("no update of %s is waiting on a conflict, so there is nothing to resolve", decision.TargetBranch)
	}
	pending := update.Pending
	var rounds []TeamBranchResolutionRound
	if pending.Resolution != nil {
		if pending.Resolution.Spent() {
			return TeamBranchUpdate{}, fmt.Errorf("all %d rounds of resolving this conflict have been used; the update waits for a branch to move or for a person to merge %s into %s by hand", MaxTeamBranchResolutionRounds, update.UpstreamBranch, update.TargetBranch)
		}
		if pending.Resolution.InProgress() != nil {
			return TeamBranchUpdate{}, fmt.Errorf("a round of resolving this conflict is running now (run %s); the decision can be changed once it ends", pending.Resolution.InProgress().RunID)
		}
		rounds = pending.Resolution.Rounds
	}
	var paths []string
	if pending.Resolution != nil {
		paths = pending.Resolution.Paths
	}
	at := decision.At
	if at.IsZero() {
		at = time.Now()
	}
	pending.Resolution = &TeamBranchResolution{
		WorkItemID: strings.TrimSpace(decision.WorkItemID), WorkItemTitle: strings.Join(strings.Fields(decision.WorkItemTitle), " "),
		Reason: strings.TrimSpace(decision.Reason), DecidedBy: strings.TrimSpace(decision.DecidedBy),
		Conversation: decision.Conversation, Turn: decision.Turn, DecidedAt: at.UTC(), Paths: paths, Rounds: rounds,
	}
	if err := s.Save(update); err != nil {
		return TeamBranchUpdate{}, err
	}
	return update, nil
}

// ChecksPassed reports the checks finished, at least one ran, and every one
// passed and could be run.
func (a TeamBranchUpdateAttempt) ChecksPassed() bool {
	if a.ChecksFinishedAt == nil || len(a.Checks) == 0 {
		return false
	}
	for _, check := range a.Checks {
		if !check.Passed || check.CouldNotRun != "" {
			return false
		}
	}
	return true
}

// Approved reports the reviewer approved the candidate.
func (a TeamBranchUpdateAttempt) Approved() bool {
	return a.Review != nil && a.Review.Decision == teamBranchApprove
}

// Waiting is the reason the attempt has nothing to do until a branch moves, in
// plain words, and empty where it is not waiting.
func (a TeamBranchUpdateAttempt) Waiting() string {
	switch {
	case a.Conflict != nil:
		return fmt.Sprintf("merging %s of the team's branch into the target conflicts in %s", shortCommit(a.TeamCommit), strings.Join(a.Conflict.Paths, ", "))
	case a.Refused != "":
		return a.Refused
	}
	return ""
}

// resolutionSays is what is decided about resolving the conflict, as the end of
// the conflict's line.
func (a TeamBranchUpdateAttempt) resolutionSays() string {
	r := a.Resolution
	switch {
	case r == nil:
		return "waiting for the development manager to name an item to resolve it"
	case r.Spent():
		return fmt.Sprintf("rounds of resolving it are spent (%d of %d)", len(r.Rounds), MaxTeamBranchResolutionRounds)
	case r.InProgress() != nil:
		return fmt.Sprintf("the %s decided that %s resolves it (round %d of %d in progress)", decidedByWords(r.DecidedBy), r.Says(), len(r.Rounds), MaxTeamBranchResolutionRounds)
	case len(r.Rounds) > 0:
		return fmt.Sprintf("the %s decided that %s resolves it (%d of %d rounds used; the next starts at the next check)", decidedByWords(r.DecidedBy), r.Says(), len(r.Rounds), MaxTeamBranchResolutionRounds)
	}
	return fmt.Sprintf("the %s decided that %s resolves it (round 1 of %d starts at the next check)", decidedByWords(r.DecidedBy), r.Says(), MaxTeamBranchResolutionRounds)
}

// decidedByWords is a deciding role as words: the development manager, a person.
func decidedByWords(by string) string { return strings.ReplaceAll(by, "-", " ") }

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func teamBranchTime(t time.Time) string { return t.Local().Format("2006-01-02 15:04 MST") }

// Says is the record in the words every surface uses for it, one sentence a
// line.
func (u TeamBranchUpdate) Says() []string {
	var lines []string
	switch {
	case len(u.Landed) > 0:
		last := u.Landed[len(u.Landed)-1]
		at := ""
		if last.LandedAt != nil {
			at = " at " + teamBranchTime(*last.LandedAt)
		}
		lines = append(lines, fmt.Sprintf("%s was last brought up to date with %s%s (merge %s).", u.TargetBranch, u.UpstreamBranch, at, shortCommit(last.LandedCommit)))
	case u.CurrentAt != nil:
		lines = append(lines, fmt.Sprintf("%s already carried %s as of %s.", u.TargetBranch, u.UpstreamBranch, teamBranchTime(*u.CurrentAt)))
	}
	if p := u.Pending; p != nil {
		waiting := fmt.Sprintf("Bringing %s up to date with %s is waiting: ", u.TargetBranch, u.UpstreamBranch)
		switch {
		case p.Conflict != nil:
			told := ""
			if p.ReportedAt != nil {
				told = "; the development manager has been told"
			}
			lines = append(lines, fmt.Sprintf("%smerging %s at %s conflicts in %s%s; %s.", waiting, u.UpstreamBranch, shortCommit(p.TeamCommit), strings.Join(p.Conflict.Paths, ", "), told, p.resolutionSays()))
		case p.Refused != "":
			lines = append(lines, fmt.Sprintf("%s%s.", waiting, strings.TrimRight(strings.TrimSpace(p.Refused), ".")))
		case p.PullRequest != 0 && p.MergeRequestedAt != nil:
			lines = append(lines, fmt.Sprintf("Bringing %s up to date with %s: pull request #%d is waiting for the forge to merge it.", u.TargetBranch, u.UpstreamBranch, p.PullRequest))
		default:
			lines = append(lines, fmt.Sprintf("Bringing %s up to date with %s at %s is in progress.", u.TargetBranch, u.UpstreamBranch, shortCommit(p.TeamCommit)))
		}
	}
	return lines
}

// Land moves the pending update into the landed ones, as landed at the commit
// the target now stands at, and keeps the newest MaxTeamBranchUpdatesKept.
func (u *TeamBranchUpdate) Land(at time.Time, commit string) {
	if u.Pending == nil {
		return
	}
	landed := *u.Pending
	landedAt := at
	landed.LandedAt = &landedAt
	landed.LandedCommit = commit
	u.Landed = append(u.Landed, landed)
	if extra := len(u.Landed) - MaxTeamBranchUpdatesKept; extra > 0 {
		u.Landed = append([]TeamBranchUpdateAttempt(nil), u.Landed[extra:]...)
	}
	u.Pending = nil
}

// Validate checks the record names its branches, holds well-formed attempts,
// and keeps no more landed updates than it is bounded to.
func (u TeamBranchUpdate) Validate() error {
	var problems []error
	if u.SchemaVersion != TeamBranchUpdateSchemaVersion {
		problems = append(problems, fmt.Errorf("team branch update schema version %d is not supported", u.SchemaVersion))
	}
	if !validLocalBranch(u.TargetBranch) {
		problems = append(problems, fmt.Errorf("team branch update target_branch %q is not a local branch name", u.TargetBranch))
	}
	if !validLocalBranch(u.UpstreamBranch) {
		problems = append(problems, fmt.Errorf("team branch update upstream_branch %q is not a local branch name", u.UpstreamBranch))
	}
	if u.CurrentWith != "" && !commitPattern.MatchString(u.CurrentWith) {
		problems = append(problems, errors.New("team branch update current_with is invalid"))
	}
	if u.Pending != nil {
		if err := u.Pending.validate("pending"); err != nil {
			problems = append(problems, err)
		}
		if u.Pending.LandedAt != nil {
			problems = append(problems, errors.New("the pending team branch update has landed"))
		}
	}
	if len(u.Landed) > MaxTeamBranchUpdatesKept {
		problems = append(problems, fmt.Errorf("%d landed team branch updates exceed the %d kept", len(u.Landed), MaxTeamBranchUpdatesKept))
	}
	for i, landed := range u.Landed {
		name := fmt.Sprintf("landed update %d", i+1)
		if err := landed.validate(name); err != nil {
			problems = append(problems, err)
		}
		if landed.LandedAt == nil {
			problems = append(problems, fmt.Errorf("%s has no landed_at", name))
		}
		if !commitPattern.MatchString(landed.LandedCommit) {
			problems = append(problems, fmt.Errorf("%s has no valid landed_commit", name))
		}
	}
	return errors.Join(problems...)
}

func (a TeamBranchUpdateAttempt) validate(name string) error {
	var problems []error
	if !commitPattern.MatchString(a.TeamCommit) {
		problems = append(problems, fmt.Errorf("%s team_commit is invalid", name))
	}
	if !commitPattern.MatchString(a.TargetBase) {
		problems = append(problems, fmt.Errorf("%s target_base is invalid", name))
	}
	if a.Candidate != "" && !commitPattern.MatchString(a.Candidate) {
		problems = append(problems, fmt.Errorf("%s candidate is invalid", name))
	}
	if a.LandedCommit != "" && !commitPattern.MatchString(a.LandedCommit) {
		problems = append(problems, fmt.Errorf("%s landed_commit is invalid", name))
	}
	if a.StartedAt.IsZero() {
		problems = append(problems, fmt.Errorf("%s started_at is required", name))
	}
	if a.PullRequest < 0 {
		problems = append(problems, fmt.Errorf("%s pull_request is negative", name))
	}
	if a.Resolution != nil {
		if err := a.Resolution.validate(name); err != nil {
			problems = append(problems, err)
		}
	}
	if a.Conflict != nil {
		for _, item := range a.Conflict.Items {
			if strings.TrimSpace(item.ID) == "" {
				problems = append(problems, fmt.Errorf("%s conflict names an item with no id", name))
			}
		}
	}
	return errors.Join(problems...)
}

// TeamBranchUpdateStore keeps the records of a product's target branches.
type TeamBranchUpdateStore struct {
	stateRoot, anchor string
	productID         domain.ProductID
	leaseWait         time.Duration
}

// NewTeamBranchUpdateStore opens the store under the state root.
func NewTeamBranchUpdateStore(root string, productID domain.ProductID) (*TeamBranchUpdateStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the team branch update state root: %w", err)
	}
	return &TeamBranchUpdateStore{stateRoot: stateRoot, anchor: anchor, productID: productID, leaseWait: leaseGrace}, nil
}

func (s *TeamBranchUpdateStore) directory() string {
	return filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), teamBranchUpdatesDirectory)
}

// teamBranchDirectory is a branch's directory under the product's records. The
// branch is digested, because a name like release/1.2 is not one directory
// name; the readable name is in the record.
func teamBranchDirectory(targetBranch string) string {
	sum := sha256.Sum256([]byte(targetBranch))
	return hex.EncodeToString(sum[:16])
}

func (s *TeamBranchUpdateStore) open(targetBranch string) (*repowrite.PinnedRoot, error) {
	if !validLocalBranch(targetBranch) {
		return nil, fmt.Errorf("target branch %q is not a local branch name", targetBranch)
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, fmt.Errorf("pin the team branch update state root: %w", err)
	}
	defer root.Close()
	directory := path.Join(filepath.ToSlash(s.directory()), teamBranchDirectory(targetBranch))
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create the team branch update directory for %s: %w", targetBranch, err)
	}
	pinned, err := root.OpenDirectory(directory)
	if err != nil {
		return nil, fmt.Errorf("open the team branch update directory for %s: %w", targetBranch, err)
	}
	return pinned, nil
}

func decodeTeamBranchUpdate(encoded []byte) (TeamBranchUpdate, error) {
	var update TeamBranchUpdate
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		return TeamBranchUpdate{}, err
	}
	if decoder.More() {
		return TeamBranchUpdate{}, errors.New("trailing data after the record")
	}
	if err := update.Validate(); err != nil {
		return TeamBranchUpdate{}, err
	}
	return update, nil
}

func readTeamBranchUpdate(pinned *repowrite.PinnedRoot) (TeamBranchUpdate, error) {
	encoded, err := pinned.ReadFile(teamBranchUpdateRecordName)
	if err != nil {
		return TeamBranchUpdate{}, err
	}
	if len(encoded) > maxEncodedStateBytes {
		return TeamBranchUpdate{}, fmt.Errorf("the record is %d bytes, which exceeds the %d byte bound", len(encoded), maxEncodedStateBytes)
	}
	return decodeTeamBranchUpdate(encoded)
}

// Load reads a target branch's record, and reports false where none was written.
func (s *TeamBranchUpdateStore) Load(targetBranch string) (TeamBranchUpdate, bool, error) {
	pinned, err := s.open(targetBranch)
	if err != nil {
		return TeamBranchUpdate{}, false, err
	}
	defer pinned.Close()
	update, err := readTeamBranchUpdate(pinned)
	if errors.Is(err, fs.ErrNotExist) {
		return TeamBranchUpdate{}, false, nil
	}
	if err != nil {
		return TeamBranchUpdate{}, false, fmt.Errorf("read the team branch update record for %s: %w", targetBranch, err)
	}
	if update.TargetBranch != targetBranch {
		return TeamBranchUpdate{}, false, fmt.Errorf("the team branch update record kept for %s is for %q", targetBranch, update.TargetBranch)
	}
	return update, true, nil
}

// Save bounds the record's free text, validates it, and replaces the record
// whole and synced.
func (s *TeamBranchUpdateStore) Save(update TeamBranchUpdate) error {
	update.SchemaVersion = TeamBranchUpdateSchemaVersion
	note := truncatedNote(MaxRecordedTextBytes)
	bound := func(a TeamBranchUpdateAttempt) TeamBranchUpdateAttempt {
		a.Refused = boundRecordedText(a.Refused, MaxRecordedTextBytes, note)
		if a.Resolution != nil {
			resolution := *a.Resolution
			resolution.Reason = boundRecordedText(resolution.Reason, MaxRecordedTextBytes, note)
			resolution.WorkItemTitle = boundRecordedText(resolution.WorkItemTitle, MaxRecordedTextBytes, note)
			resolution.Rounds = append([]TeamBranchResolutionRound(nil), resolution.Rounds...)
			for i := range resolution.Rounds {
				resolution.Rounds[i].Why = boundRecordedText(resolution.Rounds[i].Why, MaxRecordedTextBytes, note)
			}
			a.Resolution = &resolution
		}
		if a.Conflict != nil {
			conflict := *a.Conflict
			conflict.Items = append([]TeamBranchConflictItem(nil), conflict.Items...)
			for i := range conflict.Items {
				conflict.Items[i].Title = boundRecordedText(conflict.Items[i].Title, MaxRecordedTextBytes, note)
			}
			a.Conflict = &conflict
		}
		if a.Review != nil {
			review := *a.Review
			review.Summary = boundRecordedText(review.Summary, MaxRecordedTextBytes, note)
			a.Review = &review
		}
		return a
	}
	if update.Pending != nil {
		pending := bound(*update.Pending)
		update.Pending = &pending
	}
	if len(update.Landed) > 0 {
		landed := make([]TeamBranchUpdateAttempt, len(update.Landed))
		for i, attempt := range update.Landed {
			landed[i] = bound(attempt)
		}
		update.Landed = landed
	}
	if err := update.Validate(); err != nil {
		return fmt.Errorf("team branch update for %s: %w", update.TargetBranch, err)
	}
	encoded, err := json.MarshalIndent(update, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the team branch update for %s: %w", update.TargetBranch, err)
	}
	encoded = append(encoded, '\n')
	pinned, err := s.open(update.TargetBranch)
	if err != nil {
		return err
	}
	defer pinned.Close()
	if err := pinned.WriteFile(teamBranchUpdateRecordName, encoded, 0o600, false); err != nil {
		return fmt.Errorf("write the team branch update record for %s: %w", update.TargetBranch, err)
	}
	if err := pinned.Sync(); err != nil {
		return fmt.Errorf("sync the team branch update record for %s: %w", update.TargetBranch, err)
	}
	return nil
}

// LeaseUpdater takes the lease that makes a process the one bringing the
// target branch up to date, without waiting, and reports false where another
// process holds it.
func (s *TeamBranchUpdateStore) LeaseUpdater(ctx context.Context, targetBranch string) (*Lease, bool, error) {
	pinned, err := s.open(targetBranch)
	if err != nil {
		return nil, false, err
	}
	defer pinned.Close()
	file, err := pinned.OpenLock(teamBranchUpdateLeaseName, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open the team branch update lease for %s: %w", targetBranch, err)
	}
	held, err := acquireLease(ctx, file, s.leaseWait, nil)
	if err != nil {
		file.Close()
		return nil, false, fmt.Errorf("lock the team branch update lease for %s: %w", targetBranch, err)
	}
	if !held {
		file.Close()
		return nil, false, nil
	}
	return &Lease{label: teamBranchUpdateLeaseLabel, file: file, scope: teamBranchDirectory(targetBranch)}, true, nil
}

// All reads every record kept for the product, by target branch.
func (s *TeamBranchUpdateStore) All() ([]TeamBranchUpdate, error) {
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, fmt.Errorf("pin the team branch update state root: %w", err)
	}
	defer root.Close()
	directory := path.Join(filepath.ToSlash(s.directory()))
	entries, err := root.ReadDirectory(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list the team branch update records: %w", err)
	}
	var updates []TeamBranchUpdate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pinned, err := root.OpenDirectory(path.Join(directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("open the team branch update directory %s: %w", entry.Name(), err)
		}
		update, err := readTeamBranchUpdate(pinned)
		pinned.Close()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read the team branch update record in %s: %w", entry.Name(), err)
		}
		if teamBranchDirectory(update.TargetBranch) != entry.Name() {
			return nil, fmt.Errorf("the team branch update record in %s is for %q", entry.Name(), update.TargetBranch)
		}
		updates = append(updates, update)
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].TargetBranch < updates[j].TargetBranch })
	return updates, nil
}
