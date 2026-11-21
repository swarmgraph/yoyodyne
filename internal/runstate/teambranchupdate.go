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
	// ConflictRounds counts the runs the development manager decided to resolve
	// this update's conflicts with, against MaxTeamBranchConflictRounds. It is
	// kept here, beside the update, because the update is what the conflicts
	// belong to: it outlives each merge built for it, and no work item's repair
	// budget pays for it, since another contributor's work caused the conflict.
	// It starts again from none once an update has landed.
	ConflictRounds int `json:"conflict_rounds,omitempty"`
	// Decision is the development manager's decision to have a run resolve the
	// conflict, from the moment it is recorded until the merge the run made is
	// landed, dropped, or given up on. Nil where none is standing.
	Decision *TeamBranchConflictDecision `json:"conflict_decision,omitempty"`
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
	// ResolvedBy is the run whose developer resolved the conflict in Candidate,
	// empty for a merge the harness built itself. A merge carrying a run's change
	// is not compared with the tree the harness builds again, which could never
	// match; the paths the run wrote are checked as any run's are.
	ResolvedBy string `json:"resolved_by,omitempty"`
	// ResolvedItem is the work item that run was for, whose grants the check on
	// protected paths reads.
	ResolvedItem string `json:"resolved_item,omitempty"`
	// ResolvedItemTitle and ResolvedPaths say what the run settled, so the
	// reviewer and the pull request can name it: the item as a person reads it,
	// and the files the merge conflicted in.
	ResolvedItemTitle string   `json:"resolved_item_title,omitempty"`
	ResolvedPaths     []string `json:"resolved_paths,omitempty"`
}

// Resolution says who resolved the merge's conflicts and where, in plain words,
// and is empty for a merge the harness built without a conflict.
func (a TeamBranchUpdateAttempt) Resolution() string {
	if a.ResolvedBy == "" {
		return ""
	}
	item := TeamBranchUpdateConflictItem{ID: a.ResolvedItem, Title: a.ResolvedItemTitle}.Name()
	return fmt.Sprintf("run %s of %s resolved the conflicts in %s", a.ResolvedBy, item, strings.Join(a.ResolvedPaths, ", "))
}

// MaxTeamBranchConflictRounds is how many runs the development manager may
// decide on to resolve the conflicts of one update (TeamBranchUpdate.
// ConflictRounds). A fourth is refused.
const MaxTeamBranchConflictRounds = 3

// TeamBranchConflictDecision is the development manager's decision that a run
// of one item resolves the conflict the update is waiting on.
type TeamBranchConflictDecision struct {
	// Item is the work item the run is of.
	Item TeamBranchUpdateConflictItem `json:"item"`
	// Reason is the development manager's own words, kept as she wrote them.
	Reason string `json:"reason"`
	// DecidedBy says who recorded it: the role, the conversation and the turn.
	DecidedBy string    `json:"decided_by"`
	DecidedAt time.Time `json:"decided_at"`
	// Round is which of the update's rounds this is, from one.
	Round int `json:"round"`
	// StartedAt is when the run was started, written before it is, so a record
	// that has it and no merge describes a run that did not finish.
	StartedAt *time.Time `json:"started_at,omitempty"`
	// RunID is the run, once it has one.
	RunID string `json:"run_id,omitempty"`
	// Merge is the merge commit the run made, set once it has.
	Merge string `json:"merge,omitempty"`
}

// Started reports the run was started.
func (d TeamBranchConflictDecision) Started() bool { return d.StartedAt != nil }

// DecideConflict records the decision, counting the round it is. It refuses,
// in plain words, a decision where no conflict is waiting, where the conflict
// already has a decision, where no item is named, and where the update's three
// rounds are spent.
func (u *TeamBranchUpdate) DecideConflict(decision TeamBranchConflictDecision) error {
	switch {
	case u.Pending == nil || u.Pending.Conflict == nil:
		return fmt.Errorf("no conflict is waiting while %s is brought up to date with %s, so there is nothing for a run to resolve", u.TargetBranch, u.UpstreamBranch)
	case u.Decision != nil:
		return fmt.Errorf("the conflict already has a decision: a run of %s is to resolve it (round %d of %d); a conflict is decided once", u.Decision.Item.Name(), u.Decision.Round, MaxTeamBranchConflictRounds)
	case strings.TrimSpace(decision.Item.ID) == "":
		return errors.New("a decision on the conflict names one work item, and none was named")
	case strings.TrimSpace(decision.Reason) == "":
		return errors.New("a decision on the conflict carries the reason for it, and none was given")
	case u.ConflictRounds >= MaxTeamBranchConflictRounds:
		return fmt.Errorf("a fourth round is refused: bringing %s up to date with %s has had its %d rounds of runs to resolve conflicts, and they are spent. The conflict stays on the development manager's list, and %s keeps taking new work while it waits; resolving it by hand, or escalating it, is what is left",
			u.TargetBranch, u.UpstreamBranch, MaxTeamBranchConflictRounds, u.TargetBranch)
	}
	u.ConflictRounds++
	decision.Round = u.ConflictRounds
	u.Decision = &decision
	return nil
}

// GiveUpConflictRun ends the standing decision without a merge, because its run
// did not make one. The round it spent stays spent; the conflict waits for a
// new decision, if any round is left.
func (u *TeamBranchUpdate) GiveUpConflictRun() { u.Decision = nil }

// RefundUnstartedRound takes back the round of a decision whose run was never
// started, because the conflict it answered went away first, and drops the
// decision. A round is spent by a run that started.
func (u *TeamBranchUpdate) RefundUnstartedRound() {
	if u.Decision != nil && !u.Decision.Started() && u.ConflictRounds > 0 {
		u.ConflictRounds--
	}
	u.Decision = nil
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

// TeamBranchUpdateConflict is a merge that conflicts, which is never resolved
// by the harness.
type TeamBranchUpdateConflict struct {
	Paths []string `json:"paths"`
	// Items are the work items on the target whose landed commits touched those
	// paths.
	Items   []TeamBranchUpdateConflictItem `json:"items,omitempty"`
	FoundAt time.Time                      `json:"found_at"`
}

// TeamBranchUpdateConflictItem is one work item a conflict names, with the
// title its run recorded, so a person is told what the item is rather than
// handed an identifier to look up.
type TeamBranchUpdateConflictItem struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// Name is the item as a person reads it: its title with its identifier after
// it, or the identifier alone where no title was recorded.
func (i TeamBranchUpdateConflictItem) Name() string {
	if title := strings.TrimSpace(i.Title); title != "" {
		return fmt.Sprintf("%s (%s)", title, i.ID)
	}
	return i.ID
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
			lines = append(lines, fmt.Sprintf("%smerging %s at %s conflicts in %s%s.", waiting, u.UpstreamBranch, shortCommit(p.TeamCommit), strings.Join(p.Conflict.Paths, ", "), told))
			if d := u.Decision; d != nil {
				lines = append(lines, fmt.Sprintf("A run of %s is to resolve the conflict (round %d of %d).", d.Item.Name(), d.Round, MaxTeamBranchConflictRounds))
			}
		case p.Refused != "":
			lines = append(lines, fmt.Sprintf("%s%s.", waiting, strings.TrimRight(strings.TrimSpace(p.Refused), ".")))
		case p.ResolvedBy != "" && p.PullRequest == 0:
			lines = append(lines, fmt.Sprintf("Bringing %s up to date with %s: the merge %s, which run %s made to resolve the conflict, is being checked and reviewed.", u.TargetBranch, u.UpstreamBranch, shortCommit(p.Candidate), p.ResolvedBy))
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
	u.ConflictRounds, u.Decision = 0, nil
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
	if u.ConflictRounds < 0 || u.ConflictRounds > MaxTeamBranchConflictRounds {
		problems = append(problems, fmt.Errorf("team branch update conflict_rounds %d is outside 0 to %d", u.ConflictRounds, MaxTeamBranchConflictRounds))
	}
	if d := u.Decision; d != nil {
		if strings.TrimSpace(d.Item.ID) == "" || strings.TrimSpace(d.Reason) == "" || strings.TrimSpace(d.DecidedBy) == "" || d.DecidedAt.IsZero() {
			problems = append(problems, errors.New("the conflict decision needs an item, a reason, who decided it and when"))
		}
		if d.Round < 1 || d.Round > MaxTeamBranchConflictRounds {
			problems = append(problems, fmt.Errorf("the conflict decision's round %d is outside 1 to %d", d.Round, MaxTeamBranchConflictRounds))
		}
		if d.Merge != "" && !commitPattern.MatchString(d.Merge) {
			problems = append(problems, errors.New("the conflict decision's merge is invalid"))
		}
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
		if a.Review != nil {
			review := *a.Review
			review.Summary = boundRecordedText(review.Summary, MaxRecordedTextBytes, note)
			a.Review = &review
		}
		return a
	}
	if d := update.Decision; d != nil {
		kept := *d
		kept.Reason = boundRecordedText(kept.Reason, MaxRecordedTextBytes, note)
		update.Decision = &kept
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

// DecideConflict records the development manager's decision on the conflict a
// target branch's update is waiting on, under the lease that makes a process
// the one working the update, so it cannot be written over by a check. It
// answers the record as it stands, and reports false with a reason in plain
// words where the update is being worked now and the decision is to be made
// again in a moment.
func (s *TeamBranchUpdateStore) DecideConflict(ctx context.Context, targetBranch string, decision TeamBranchConflictDecision) (TeamBranchUpdate, error) {
	lease, held, err := s.LeaseUpdater(ctx, targetBranch)
	if err != nil {
		return TeamBranchUpdate{}, err
	}
	if !held {
		return TeamBranchUpdate{}, fmt.Errorf("another process is bringing %s up to date at this moment, so the decision was not recorded; record it again in a minute", targetBranch)
	}
	defer func() { _ = lease.Release() }()
	update, found, err := s.Load(targetBranch)
	if err != nil {
		return TeamBranchUpdate{}, err
	}
	if !found {
		return TeamBranchUpdate{}, fmt.Errorf("%s is not being brought up to date with a team's branch, so it has no conflict to decide", targetBranch)
	}
	if err := update.DecideConflict(decision); err != nil {
		return update, err
	}
	if err := s.Save(update); err != nil {
		return update, err
	}
	return update, nil
}

// TeamConflictRun names what a run that resolves a team branch conflict is
// resolving, so a reader can follow the chain from the development manager's
// decision to the merge it made.
type TeamConflictRun struct {
	TargetBranch   string `json:"target_branch"`
	UpstreamBranch string `json:"upstream_branch"`
	// TeamCommit is the team's commit being merged in.
	TeamCommit string `json:"team_commit"`
	// Round is which of the update's rounds this run is.
	Round int `json:"round"`
	// Reason is the development manager's reason for the decision, and DecidedBy
	// who recorded it.
	Reason    string `json:"reason"`
	DecidedBy string `json:"decided_by"`
	// Merge is the merge commit the run made, empty until it has.
	Merge string `json:"merge,omitempty"`
}

// TeamConflictStarted puts a run that resolves a team branch conflict at its
// developing step, once its worktree and the open merge in it exist, and
// records that worktree with it.
type TeamConflictStarted struct {
	WorktreePath, Branch, BaseCommit, TargetBranch string
	At                                             time.Time
}

func (u TeamConflictStarted) apply(state *State) (bool, error) {
	if state.TeamConflict == nil {
		return false, fmt.Errorf("run %s does not resolve a team branch conflict", state.RunID)
	}
	if state.Status == StatusRunning && state.Phase == PhaseDeveloping {
		return false, nil
	}
	state.WorktreePath, state.Branch, state.BaseCommit, state.TargetBranch = u.WorktreePath, u.Branch, u.BaseCommit, u.TargetBranch
	state.Status = StatusRunning
	state.Phase = PhaseDeveloping
	state.UpdatedAt = u.At
	return true, nil
}

// TeamConflictMerged ends a run that resolved a team branch conflict with the
// merge commit it made. The run is over and its change is not one to promote:
// the update takes the merge and lands it (StopIntegrationPolicy is how a
// succeeded run that integrated nothing says so).
type TeamConflictMerged struct {
	Merge string
	At    time.Time
}

func (u TeamConflictMerged) apply(state *State) (bool, error) {
	if state.TeamConflict == nil {
		return false, fmt.Errorf("run %s does not resolve a team branch conflict", state.RunID)
	}
	if !commitPattern.MatchString(u.Merge) {
		return false, fmt.Errorf("run %s cannot end with merge %q, which is not a full commit id", state.RunID, u.Merge)
	}
	if state.TeamConflict.Merge == u.Merge && state.Status == StatusSucceeded {
		return false, nil
	}
	resolved := *state.TeamConflict
	resolved.Merge = u.Merge
	state.TeamConflict = &resolved
	completedAt := u.At
	state.ProviderStop = ""
	state.RedeployStop = nil
	state.OperatorHeldSince = nil
	state.Status = StatusSucceeded
	state.Phase = PhaseComplete
	state.StopClass = StopIntegrationPolicy
	state.UpdatedAt, state.CompletedAt = u.At, &completedAt
	return true, nil
}
