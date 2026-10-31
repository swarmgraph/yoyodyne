package runstate

// A queued merge's checks, as the forge last reported them.
//
// A merge the forge queued is waiting on the base branch's requirements, and
// until yoyodyne-ifd.429.16 the record said only that it was queued. Pull
// request 609 sat queued for 33 hours against a red build that way, and 713 sat
// 31 commits behind main failing two tests its change never touched, while the
// development manager waited on it because the record said the merge was
// queued. What the record was missing is the thing that decides whether a
// queued merge is ever going to land: its checks. The reconciling sweep reads
// them on every pass and writes them here, so every surface that says a merge
// is queued says what its checks are beside it.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// MaxRecordedFailingChecks bounds the failing checks one reading keeps. A
	// head failing more than this is red whatever the rest say.
	MaxRecordedFailingChecks = 20
	// MaxRecordedCheckPaths bounds the files kept for one failing check.
	MaxRecordedCheckPaths = 10
	// maxCheckNameBytes bounds one check's name, and the forge's word for how
	// it ended.
	maxCheckNameBytes = 200
	// MaxRecordedCheckAnnotations bounds the forge's annotations kept whole for
	// one failing check, and MaxCheckAnnotationBytes one annotation's message.
	MaxRecordedCheckAnnotations = 10
	MaxCheckAnnotationBytes     = 400
	// maxCheckURLBytes bounds the link to a check run's page on the forge.
	maxCheckURLBytes = 400
)

// PullRequestChecks is the forge's check state for a pull request's head, as
// the reconciling sweep last read it.
type PullRequestChecks struct {
	// HeadCommit is the head the checks ran on, which is what makes the reading
	// about one commit rather than about the request whatever it later carries.
	HeadCommit string    `json:"head_commit"`
	ReadAt     time.Time `json:"read_at"`
	// ReadError records an unsuccessful attempt. Any earlier reading remains
	// for its head's re-run accounting, but says nothing about the checks now.
	ReadError string         `json:"read_error,omitempty"`
	Failing   []FailingCheck `json:"failing,omitempty"`
	Pending   int            `json:"pending,omitempty"`
	Passing   int            `json:"passing,omitempty"`
	// BehindBy is how many commits the target branch carries that the head does
	// not, which is what an update onto the target would bring in.
	BehindBy int `json:"behind_by,omitempty"`
	// Reruns is how many times the harness has asked the forge to run this
	// head's failed jobs again because the forge ended them rather than a step
	// failing (InTheJob). RerunChecks are the check runs it asked about: the
	// forge gives a re-run a check run of its own, so a reading that still names
	// only these is one taken before the re-run began, and spends nothing. Both
	// are carried from one reading of the same head to the next, and bounded by
	// MaxCheckReruns.
	Reruns      int     `json:"reruns,omitempty"`
	RerunChecks []int64 `json:"rerun_checks,omitempty"`
}

// MaxCheckReruns bounds the re-runs one head is given before a job the forge
// ended is handed to a person. One is enough for a runner that was lost or a
// run the forge cancelled; a job ended the same way twice on one head is not
// the runner having a bad minute.
const MaxCheckReruns = 2

// maxRerunChecks bounds the check runs one head's re-runs record.
const maxRerunChecks = MaxRecordedFailingChecks * MaxCheckReruns

// jobAnnotationPath is where the forge files an annotation that names no file:
// a job cancelled, timed out, or never started, but also an ordinary step that
// exited non-zero ("Process completed with exit code 2"), such as a test
// failing. So the path alone never says whose failure it is; the conclusion
// does (jobConclusions). It is a directory, and nothing a change touches is
// ever it.
const jobAnnotationPath = ".github"

// jobConclusions are the conclusions the forge gives a job it ended itself,
// before or instead of any step failing: nothing in the tree decided them.
// "failure" is not one of them — it is a step exiting non-zero, which is what a
// genuine red test is.
var jobConclusions = map[string]bool{
	"cancelled":       true,
	"timed_out":       true,
	"startup_failure": true,
}

// FailingCheck is one check that failed on the head.
type FailingCheck struct {
	Name string `json:"name"`
	// Paths are the files the check's annotations named. OnChange is the part of
	// them the change itself touches, which is what makes the failure the
	// change's own: a failing check whose annotations name only files the change
	// does not touch failed on something the change did not bring.
	Paths    []string `json:"paths,omitempty"`
	OnChange []string `json:"on_change,omitempty"`
	// CheckRun is the forge's id for the check run, which is what a re-run
	// names. Conclusion is how the forge said it ended: failure, cancelled,
	// timed_out, startup_failure, or action_required.
	CheckRun   int64  `json:"check_run,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
	// URL is the forge's page for the check run — for an Actions job, its log —
	// and Annotations the forge's own account of the failure. They are carried
	// so the item a merge is withdrawn or handed back on says what failed in the
	// forge's words, rather than sending whoever works it to the forge: a
	// developer run may not reach it (yoyodyne-ifd.429.35).
	URL         string            `json:"url,omitempty"`
	Annotations []CheckAnnotation `json:"annotations,omitempty"`
}

// CheckAnnotation is one annotation the forge made on a failing check: the file
// and line it points at, where it points at one, its level, and its message.
type CheckAnnotation struct {
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Level   string `json:"level,omitempty"`
	Message string `json:"message,omitempty"`
}

// Describe is the annotation as one line: where, at what level, and what the
// forge said.
func (a CheckAnnotation) Describe() string {
	where := a.Path
	if where == "" {
		where = "no file"
	} else if a.Line > 0 {
		where = fmt.Sprintf("%s:%d", a.Path, a.Line)
	}
	level := a.Level
	if level == "" {
		level = "annotation"
	}
	return fmt.Sprintf("%s (%s): %s", where, level, a.Message)
}

// InTheJob reports a failing check the forge ended itself — cancelled, timed
// out, or never started — naming no file in the tree. Nothing in the tree
// decided such an ending, and the same job over the same tree can pass once the
// runner is healthy, so it is re-run before anyone is told the checks are red.
// A step that failed ("failure") is not one, wherever its annotation was filed:
// that is what a genuine red test looks like. Nor is a job waiting on a
// person's approval: running it again waits on the same approval.
func (f FailingCheck) InTheJob() bool {
	if len(f.OnChange) > 0 || !jobConclusions[f.Conclusion] {
		return false
	}
	return f.namesNoFile()
}

// namesNoFile reports a failing check whose annotations are only the forge's
// own, filed on jobAnnotationPath, or that has none.
func (f FailingCheck) namesNoFile() bool {
	for _, path := range f.Paths {
		if path != jobAnnotationPath {
			return false
		}
	}
	return true
}

// Red reports a reading with a check that failed.
func (c PullRequestChecks) Red() bool { return c.ReadError == "" && len(c.Failing) > 0 }

// FailedInTheJob reports a red reading every one of whose failing checks is a
// job the forge ended itself (FailingCheck.InTheJob).
func (c PullRequestChecks) FailedInTheJob() bool {
	if !c.Red() {
		return false
	}
	for _, failing := range c.Failing {
		if !failing.InTheJob() {
			return false
		}
	}
	return true
}

// AwaitingRerun reports a reading of jobs the forge ended whose every failing
// check run is one the harness already asked the forge to run again: the
// reading was taken before the re-run began, and says nothing new.
func (c PullRequestChecks) AwaitingRerun() bool {
	if !c.FailedInTheJob() {
		return false
	}
	asked := make(map[int64]bool, len(c.RerunChecks))
	for _, id := range c.RerunChecks {
		asked[id] = true
	}
	for _, failing := range c.Failing {
		if failing.CheckRun <= 0 || !asked[failing.CheckRun] {
			return false
		}
	}
	return true
}

// RecordRerun counts one re-run of the reading's failing checks.
func (c *PullRequestChecks) RecordRerun() {
	c.Reruns++
	for _, failing := range c.Failing {
		if failing.CheckRun > 0 && len(c.RerunChecks) < maxRerunChecks {
			c.RerunChecks = append(c.RerunChecks, failing.CheckRun)
		}
	}
}

// ChangeFails reports a failing check whose annotations name a file the change
// touches, which is the change's own failure rather than one it met.
func (c PullRequestChecks) ChangeFails() bool {
	if c.ReadError != "" {
		return false
	}
	for _, failing := range c.Failing {
		if len(failing.OnChange) > 0 {
			return true
		}
	}
	return false
}

// Describe is the one sentence every surface says of the reading: whether the
// checks pass, which failed and on what, how far behind the target the head
// is, and when this was read. It is here rather than per surface for the
// reason the outcome vocabulary is: the docket and the attention line must not
// word one reading two ways.
func (c PullRequestChecks) Describe(targetBranch string) string {
	if c.ReadError != "" {
		return fmt.Sprintf("checks unread: %s; attempted %s", c.ReadError, c.ReadAt.UTC().Format(time.RFC3339))
	}
	target := targetBranch
	if strings.TrimSpace(target) == "" {
		target = "its target"
	}
	var standing string
	switch {
	case c.Red():
		named := make([]string, 0, len(c.Failing))
		for _, failing := range c.Failing {
			named = append(named, failing.describe())
		}
		standing = "failing: " + strings.Join(named, "; ")
	case c.Pending > 0:
		standing = fmt.Sprintf("%d still running, none failing", c.Pending)
	case c.Passing > 0:
		standing = "passing"
	default:
		standing = "none reported"
	}
	behind := fmt.Sprintf("level with %s", target)
	if c.BehindBy > 0 {
		behind = fmt.Sprintf("%d commit(s) behind %s", c.BehindBy, target)
	}
	head := c.HeadCommit
	if len(head) > 12 {
		head = head[:12]
	}
	rerun := ""
	if c.Reruns > 0 {
		rerun = fmt.Sprintf("; run again %d time(s) on this head", c.Reruns)
	}
	return fmt.Sprintf("checks %s; head %s %s%s; read %s", standing, head, behind, rerun, c.ReadAt.UTC().Format(time.RFC3339))
}

func (f FailingCheck) describe() string {
	switch {
	case len(f.OnChange) > 0:
		return fmt.Sprintf("%s (on %s, which this change touches)", f.Name, strings.Join(f.OnChange, ", "))
	case f.InTheJob():
		return fmt.Sprintf("%s (the forge %s the job before any step failed, naming no file)", f.Name, endedAs(f.Conclusion))
	case len(f.Paths) > 0 && f.namesNoFile():
		// A step that failed without naming a file: the forge's log of the run
		// says which step and why, and nothing here can say whose fault it is.
		// The harness reads that log itself and carries it onto the item.
		return fmt.Sprintf("%s (a step failed without naming a file; the forge filed it on %s, and the forge's account of it on the item says which step)", f.Name, jobAnnotationPath)
	case len(f.Paths) > 0:
		return fmt.Sprintf("%s (on %s, which this change does not touch)", f.Name, strings.Join(f.Paths, ", "))
	default:
		return f.Name + " (naming no file)"
	}
}

// endedAs is how the forge ending a job reads in a sentence.
func endedAs(conclusion string) string {
	switch conclusion {
	case "cancelled":
		return "cancelled"
	case "timed_out":
		return "timed out"
	default:
		return "could not start"
	}
}

// Validate rejects a reading that cannot describe a real one.
func (c PullRequestChecks) Validate() error {
	var problems []error
	if !commitPattern.MatchString(c.HeadCommit) && !(c.HeadCommit == "" && strings.TrimSpace(c.ReadError) != "") {
		problems = append(problems, errors.New("checks head_commit is invalid"))
	}
	if c.ReadAt.IsZero() {
		problems = append(problems, errors.New("checks read_at is required"))
	}
	if len(c.RerunChecks) > maxRerunChecks {
		problems = append(problems, fmt.Errorf("%d re-run check runs are recorded, which exceeds the bound of %d", len(c.RerunChecks), maxRerunChecks))
	}
	if c.Pending < 0 || c.Passing < 0 || c.BehindBy < 0 || c.Reruns < 0 {
		problems = append(problems, errors.New("checks counts cannot be negative"))
	}
	if len(c.Failing) > MaxRecordedFailingChecks {
		problems = append(problems, fmt.Errorf("%d failing checks are recorded, which exceeds the bound of %d", len(c.Failing), MaxRecordedFailingChecks))
	}
	for index, failing := range c.Failing {
		if strings.TrimSpace(failing.Name) == "" || len(failing.Name) > maxCheckNameBytes {
			problems = append(problems, fmt.Errorf("checks failing[%d] name must be present and at most %d bytes", index, maxCheckNameBytes))
		}
		if len(failing.Conclusion) > maxCheckNameBytes {
			problems = append(problems, fmt.Errorf("checks failing[%d] conclusion must be at most %d bytes", index, maxCheckNameBytes))
		}
		if len(failing.Paths) > MaxRecordedCheckPaths || len(failing.OnChange) > MaxRecordedCheckPaths {
			problems = append(problems, fmt.Errorf("checks failing[%d] names more than %d files", index, MaxRecordedCheckPaths))
		}
		if len(failing.URL) > maxCheckURLBytes {
			problems = append(problems, fmt.Errorf("checks failing[%d] url must be at most %d bytes", index, maxCheckURLBytes))
		}
		if len(failing.Annotations) > MaxRecordedCheckAnnotations {
			problems = append(problems, fmt.Errorf("checks failing[%d] carries more than %d annotations", index, MaxRecordedCheckAnnotations))
		}
		for _, annotation := range failing.Annotations {
			if len(annotation.Message) > MaxCheckAnnotationBytes || len(annotation.Path) > MaxCheckAnnotationBytes || len(annotation.Level) > maxCheckNameBytes || annotation.Line < 0 {
				problems = append(problems, fmt.Errorf("checks failing[%d] carries an annotation past its bounds", index))
				break
			}
		}
	}
	return errors.Join(problems...)
}

// TargetRed is a queued merge the reconciling sweep withdrew because its checks
// failed on a head level with the target, on no file the change touches: the
// target's own failure rather than the change's. Nothing but the change differs
// between the head and the target, so bringing the head up to date changes
// nothing, and the failure is one every request queued behind it meets too.
//
// Until yoyodyne-m5p that was handed to a person as a dropped merge. On
// 2026-09-28 the adoption check went red on main itself, and the queued merge of
// pull request 863 became a hand step, as every merge queued behind it would
// have. Now the failure is filed as the target's, one p0 item per target branch
// and check, exactly as a red landing is, and the publication waits on those
// items with the harness as the one to move: once they close, the watch re-arms
// the merge on a level head that passes, and the reconciling sweep replays a
// head the fix has left behind.
type TargetRed struct {
	At           time.Time `json:"at"`
	TargetBranch string    `json:"target_branch"`
	// HeadCommit is the head that was level with the target when its checks
	// failed.
	HeadCommit string `json:"head_commit"`
	// Checks are the failing checks, each with the item it waits on.
	Checks []TargetRedCheck `json:"checks"`
}

// TargetRedCheck is one check failing on the target, and the p0 item the
// harness filed for it or found already open.
type TargetRedCheck struct {
	Name     string `json:"name"`
	WorkItem string `json:"work_item"`
	// FiledEarlier says the item was already open for this check on this branch,
	// filed for an earlier request, and this request was noted on it.
	FiledEarlier bool `json:"filed_earlier,omitempty"`
}

// WaitingOn is every item the publication waits on, in the order the checks
// were read, each once.
func (t TargetRed) WaitingOn() []string {
	seen := make(map[string]bool, len(t.Checks))
	items := make([]string, 0, len(t.Checks))
	for _, check := range t.Checks {
		if check.WorkItem == "" || seen[check.WorkItem] {
			continue
		}
		seen[check.WorkItem] = true
		items = append(items, check.WorkItem)
	}
	return items
}

// Describe is the one sentence every surface says of the wait: which checks
// fail on the target, and the items the merge waits on.
func (t TargetRed) Describe() string {
	named := make([]string, 0, len(t.Checks))
	for _, check := range t.Checks {
		named = append(named, fmt.Sprintf("%s (filed as %s)", check.Name, check.WorkItem))
	}
	target := t.TargetBranch
	if strings.TrimSpace(target) == "" {
		target = "its target"
	}
	return fmt.Sprintf("waits on %s's red check, not on this change: %s failed with the head level with %s on no file the change touches",
		target, strings.Join(named, "; "), target)
}

// Validate rejects a wait that cannot describe a real one.
func (t TargetRed) Validate() error {
	var problems []error
	if t.At.IsZero() {
		problems = append(problems, errors.New("target_red at is required"))
	}
	if !validLocalBranch(t.TargetBranch) {
		problems = append(problems, errors.New("target_red target_branch must be a local branch name"))
	}
	if !commitPattern.MatchString(t.HeadCommit) {
		problems = append(problems, errors.New("target_red head_commit is invalid"))
	}
	if len(t.Checks) == 0 || len(t.Checks) > MaxRecordedFailingChecks {
		problems = append(problems, fmt.Errorf("target_red names %d checks, and it names between 1 and %d", len(t.Checks), MaxRecordedFailingChecks))
	}
	for index, check := range t.Checks {
		if strings.TrimSpace(check.Name) == "" || len(check.Name) > maxCheckNameBytes {
			problems = append(problems, fmt.Errorf("target_red checks[%d] name must be present and at most %d bytes", index, maxCheckNameBytes))
		}
		if strings.TrimSpace(check.WorkItem) == "" {
			problems = append(problems, fmt.Errorf("target_red checks[%d] names no work item, and the wait is on one", index))
		}
	}
	return errors.Join(problems...)
}

// WaitingOnRedTarget reports a publication whose merge the sweep withdrew for
// the target's red check and that nothing has armed, merged, or handed back
// since: it waits on the items filed for that check, and its next mover is the
// harness.
func (s State) WaitingOnRedTarget() bool {
	if !s.Status.Terminal() || s.Integration == nil || s.PullRequest == nil {
		return false
	}
	published := s.PullRequest
	return published.TargetRed != nil && !published.Merged && !published.MergeQueued && published.HandedBack == nil
}

// UpdatingQueuedHead reports a run the reconciling sweep put back at its
// promotion to bring a queued head up to date, and that is still waiting there
// for the sweep to carry it through the update. It is a pending continuation in
// the claim audit's sense: the sweep made it live and hosts it as its last step,
// so a record gone still in between — a sweep that died before hosting it — is
// owed that continuation, not a claim nothing is working on. Given back, its
// item would be developed again from scratch over a branch and an open pull
// request that still hold the approved change.
func (s State) UpdatingQueuedHead() bool {
	if !s.Status.InFlight() || s.Phase != PhaseIntegrating || s.Integration != nil {
		return false
	}
	if len(s.IntegrationResumptions) == 0 {
		return false
	}
	return s.IntegrationResumptions[len(s.IntegrationResumptions)-1].Cause == CauseQueuedHeadBehind
}

// MaxRecordedQueuedCheckAccounts bounds the failing checks on a queued merge
// whose account — the failing step and the end of its output — the record
// keeps, because each one is a log read and kilobytes of record. The rest are
// still named.
const MaxRecordedQueuedCheckAccounts = 3

// QueuedMergeChecks is how the forge's checks ended on the merge its merge queue
// built of a pull request, read when the forge was found to have dropped that
// merge. The queue runs its checks on a commit of its own — the request's head
// merged onto the target — rather than on the request's head, so a head whose
// own checks pass can be removed from the queue for failed checks, and this is
// the reading that names them (publish.QueuedMergeChecks).
//
// A reading that could not be taken says why in ReadError. One that was taken
// and found no queued merge leaves Commit empty, which says nothing about
// whether the checks passed.
type QueuedMergeChecks struct {
	// Commit is the merge the queue built and ran its checks on.
	Commit    string               `json:"commit,omitempty"`
	ReadAt    time.Time            `json:"read_at"`
	ReadError string               `json:"read_error,omitempty"`
	Failing   []QueuedFailingCheck `json:"failing,omitempty"`
	Pending   int                  `json:"pending,omitempty"`
	Passing   int                  `json:"passing,omitempty"`
}

// QueuedFailingCheck is one check that failed on a queued merge: its name, how
// the forge ended it, the link to its run, and the forge's account of it as the
// item carries it — the job and step that failed and the end of that step's
// output, or why the log could not be read. Account is empty past
// MaxRecordedQueuedCheckAccounts.
type QueuedFailingCheck struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion,omitempty"`
	URL        string `json:"url,omitempty"`
	Account    string `json:"account,omitempty"`
}

// Read reports a reading that was taken, whatever it found.
func (q QueuedMergeChecks) Read() bool { return strings.TrimSpace(q.ReadError) == "" }

// Found reports a reading that found the forge's queued merge.
func (q QueuedMergeChecks) Found() bool { return q.Read() && q.Commit != "" }

// Red reports a queued merge the forge's checks failed on.
func (q QueuedMergeChecks) Red() bool { return q.Found() && len(q.Failing) > 0 }

// Names is the failing checks, each with how the forge ended it.
func (q QueuedMergeChecks) Names() string {
	names := make([]string, 0, len(q.Failing))
	for _, failing := range q.Failing {
		names = append(names, fmt.Sprintf("%s (%s)", failing.Name, nonEmptyWord(failing.Conclusion, "no conclusion reported")))
	}
	return strings.Join(names, ", ")
}

// Validate rejects a reading that cannot describe a real one.
func (q QueuedMergeChecks) Validate() error {
	var problems []error
	if q.Commit != "" && !commitPattern.MatchString(q.Commit) {
		problems = append(problems, errors.New("queued checks commit is invalid"))
	}
	if q.ReadAt.IsZero() {
		problems = append(problems, errors.New("queued checks read_at is required"))
	}
	if q.Pending < 0 || q.Passing < 0 {
		problems = append(problems, errors.New("queued checks counts cannot be negative"))
	}
	if len(q.Failing) > MaxRecordedFailingChecks {
		problems = append(problems, fmt.Errorf("%d failing queued checks are recorded, which exceeds the bound of %d", len(q.Failing), MaxRecordedFailingChecks))
	}
	for index, failing := range q.Failing {
		if strings.TrimSpace(failing.Name) == "" {
			problems = append(problems, fmt.Errorf("queued checks failing[%d] name is required", index))
		}
	}
	return errors.Join(problems...)
}

func nonEmptyWord(value, otherwise string) string {
	if strings.TrimSpace(value) == "" {
		return otherwise
	}
	return value
}
