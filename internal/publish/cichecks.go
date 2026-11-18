package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// What checks on CI asks of the forge adapter (docs/designs/checks-on-ci.md):
// push a run's branch, open its pull request as a draft or update the one it
// has, and read each required check's result for a named commit, with the
// output of a failing one. The harness does every one of these; no agent holds
// the forge's credentials.
//
// internal/config/cichecks.go lists these as capabilities and refuses to turn
// the flow on for an adapter that lacks one; cichecks_test.go here runs each
// against a fake forge command, so a capability cannot be claimed there and
// missing here.

const (
	// MaxCIOutputLines and MaxCIOutputBytes bound the output a failing check
	// carries: the last 200 lines, and at most 16 KiB of them.
	MaxCIOutputLines = 200
	MaxCIOutputBytes = 16 << 10
	// maxFailingTests bounds the failing tests named for one check.
	maxFailingTests = 100
)

// CheckResult is how one required check stands on the commit asked about.
type CheckResult string

const (
	// CheckPassed is a check that finished with success.
	CheckPassed CheckResult = "passed"
	// CheckFailed is a check that finished failing, timed out, or needed an
	// action nobody took.
	CheckFailed CheckResult = "failed"
	// CheckPending is a check queued or running.
	CheckPending CheckResult = "pending"
	// CheckCancelled is a check that was cancelled before finishing.
	CheckCancelled CheckResult = "cancelled"
	// CheckSkipped is a check the forge skipped.
	CheckSkipped CheckResult = "skipped"
	// CheckNotStarted is a required check the forge has recorded nothing for on
	// this commit.
	CheckNotStarted CheckResult = "not started"
	// CheckOther is a check that finished in a state that is neither a pass nor
	// a failure, such as neutral or stale.
	CheckOther CheckResult = "other"
)

// RequiredCheck is one required check as the forge reports it on one commit.
// Commit is the commit the forge says the check ran on, which is the commit
// asked about for every check returned here and empty for one not started.
type RequiredCheck struct {
	Name   string
	Result CheckResult
	Commit string
	// Conclusion is the forge's own word for how it ended, empty while it runs.
	Conclusion string
	// ID is the forge's check run, which for an Actions job is the job whose
	// log CheckOutput reads. Zero for a check not started.
	ID  int64
	URL string
}

// RequiredCheckReading is the required checks' standing on one commit, in the
// order they were asked for.
type RequiredCheckReading struct {
	Commit string
	Checks []RequiredCheck
}

// AllPassed reports whether every required check passed on the commit. A
// reading of no checks has passed nothing.
func (r RequiredCheckReading) AllPassed() bool {
	if len(r.Checks) == 0 {
		return false
	}
	for _, check := range r.Checks {
		if check.Result != CheckPassed {
			return false
		}
	}
	return true
}

// fullCommitPattern is a commit named in full. A result is matched to a commit
// by the forge's own full identifier, never by a prefix.
var fullCommitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// PushRequest names the branch to publish and the exact commit it is to point
// at.
type PushRequest struct {
	Branch string
	Commit string
	// Previous is the commit the harness last pushed to this branch, or empty
	// for a branch it has not pushed. With it the push replaces the branch only
	// while the remote still holds that commit; without it the push is an
	// ordinary fast-forward and the remote refuses anything else.
	Previous string
}

func (r PushRequest) validate() error {
	if err := validateArgument("branch", r.Branch); err != nil {
		return err
	}
	if !commitPattern.MatchString(r.Commit) {
		return fmt.Errorf("commit %q is invalid", r.Commit)
	}
	if r.Previous != "" && !commitPattern.MatchString(r.Previous) {
		return fmt.Errorf("previous commit %q is invalid", r.Previous)
	}
	return nil
}

// PushBranch advances a run's branch on the push remote to an exact commit and
// proves it arrived, by asking the remote where the branch is afterwards. The
// refspec is written out in full so the push never depends on push.default, and
// hooks are off. It never forces: a replacement is a lease on Previous.
func (g GitHub) PushBranch(ctx context.Context, request PushRequest) error {
	if err := request.validate(); err != nil {
		return err
	}
	remote := g.pushRemoteName()
	if err := validateArgument("remote", remote); err != nil {
		return err
	}
	ref := "refs/heads/" + request.Branch
	args := []string{"-C", g.Dir, "-c", "core.hooksPath=" + os.DevNull, "push"}
	if request.Previous != "" {
		args = append(args, "--force-with-lease="+ref+":"+request.Previous)
	}
	args = append(args, remote, request.Commit+":"+ref)
	pushed, err := g.runGit(ctx, args...)
	if err != nil {
		return fmt.Errorf("push %s to %s: %w", request.Branch, remote, err)
	}
	if pushed.Status != execution.ProcessSucceeded {
		return fmt.Errorf("push %s to %s was refused with exit code %d: %s", request.Branch, remote, pushed.ExitCode, g.redact(firstLine(strings.TrimSpace(pushed.Stderr))))
	}
	listed, err := g.runGit(ctx, "-C", g.Dir, "ls-remote", "--heads", remote, ref)
	if err != nil {
		return fmt.Errorf("read %s on %s after the push: %w", request.Branch, remote, err)
	}
	if listed.Status != execution.ProcessSucceeded {
		return fmt.Errorf("read %s on %s after the push: exit code %d: %s", request.Branch, remote, listed.ExitCode, g.redact(firstLine(strings.TrimSpace(listed.Stderr))))
	}
	fields := strings.Fields(listed.Stdout)
	if len(fields) == 0 || !strings.EqualFold(fields[0], request.Commit) {
		return fmt.Errorf("%s on %s does not hold %s after the push", request.Branch, remote, request.Commit)
	}
	return nil
}

// runGit runs Git where it reaches a remote, so it is given the forge's
// credentials and addressing and nothing else the harness holds.
func (g GitHub) runGit(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	return g.Runner.Run(ctx, execution.Command{
		Name:     g.gitBinary(),
		Args:     args,
		Dir:      g.Dir,
		Env:      execution.ForgeEnvironment(nil),
		Timeout:  g.timeout(),
		Redactor: execution.NewRedactor(g.RedactValues...),
	}, nil)
}

// EnsureDraft returns the pull request for a published branch, opening it as a
// draft when the branch has none and refreshing its title and description when
// it has, so a later push updates the one request instead of opening another.
// A request already open keeps whatever draft state it is in: marking it ready
// is a later step with its own conditions.
func (g GitHub) EnsureDraft(ctx context.Context, request Request) (PullRequest, error) {
	if err := request.validate(); err != nil {
		return PullRequest{}, err
	}
	existing, found, err := g.find(ctx, request.Head)
	if err != nil {
		return PullRequest{}, err
	}
	if !found {
		return g.open(ctx, request, true)
	}
	if existing.State != "" && !strings.EqualFold(existing.State, "OPEN") {
		return PullRequest{}, fmt.Errorf("pull request %d for branch %s is %s and cannot be republished into", existing.Number, request.Head, strings.ToLower(existing.State))
	}
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return PullRequest{}, err
	}
	edited, err := g.exec(ctx, append([]string{"pr", "edit", strconv.Itoa(existing.Number)}, append(scope,
		"--title", boundedTitle(request.Title),
		"--body", markedBody(request.Body, request.Mark))...)...)
	if err != nil {
		return PullRequest{}, fmt.Errorf("update pull request %d for %s: %w", existing.Number, request.Head, err)
	}
	if edited.Status != execution.ProcessSucceeded {
		return PullRequest{}, g.forgeFailure(fmt.Sprintf("update pull request %d for %s", existing.Number, request.Head), edited)
	}
	return existing, nil
}

// RequiredChecks reads how each named check stands on one commit, which must be
// named in full. Only check runs the forge says ran on that commit count: a
// result recorded for any other commit is never returned as this one's, and a
// reading the forge answered with a different commit's run is an error.
//
// A required name with no run on the commit reads as not started rather than as
// absent from the answer, and a forge that cannot be read, answers something
// undecodable, or has more runs than were read is an error: none of those is
// ever an empty or passing reading. Where a name has several runs the latest
// stands, as the forge reports them.
func (g GitHub) RequiredChecks(ctx context.Context, commit string, required []string) (RequiredCheckReading, error) {
	commit = strings.ToLower(strings.TrimSpace(commit))
	if !fullCommitPattern.MatchString(commit) {
		return RequiredCheckReading{}, fmt.Errorf("commit %q is not a full commit identifier, and a check result is matched to its commit by that", commit)
	}
	names := make([]string, 0, len(required))
	seen := map[string]bool{}
	for _, name := range required {
		name = strings.TrimSpace(name)
		if name == "" {
			return RequiredCheckReading{}, fmt.Errorf("a required check has no name")
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return RequiredCheckReading{}, fmt.Errorf("no required checks were named, so nothing would decide whether %s passed", commit)
	}
	runs, err := g.api(ctx, "repos/{owner}/{repo}/commits/"+commit+"/check-runs?filter=latest&per_page="+strconv.Itoa(maxReadCheckRuns))
	if err != nil {
		return RequiredCheckReading{}, fmt.Errorf("ask the forge for the checks on %s: %w", commit, err)
	}
	if runs.Status != execution.ProcessSucceeded {
		return RequiredCheckReading{}, g.forgeFailure(fmt.Sprintf("ask the forge for the checks on %s", commit), runs)
	}
	if runs.OutputTruncation != "" {
		return RequiredCheckReading{}, fmt.Errorf("the forge's answer about the checks on %s was cut and is not read: %s", commit, runs.OutputTruncation)
	}
	var reported struct {
		TotalCount *int `json:"total_count"`
		CheckRuns  []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			HeadSHA    string `json:"head_sha"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(runs.Stdout)), &reported); err != nil {
		return RequiredCheckReading{}, fmt.Errorf("decode the checks on %s: %w", commit, err)
	}
	if reported.TotalCount == nil {
		return RequiredCheckReading{}, fmt.Errorf("the forge's answer about the checks on %s did not say how many there are", commit)
	}
	if *reported.TotalCount > len(reported.CheckRuns) {
		return RequiredCheckReading{}, fmt.Errorf("the forge has %d checks on %s and %d were read, so a required one may be among those not read", *reported.TotalCount, commit, len(reported.CheckRuns))
	}
	latest := map[string]int{}
	for index, run := range reported.CheckRuns {
		if head := strings.ToLower(strings.TrimSpace(run.HeadSHA)); head != commit {
			return RequiredCheckReading{}, fmt.Errorf("the forge answered with check %q that ran on %q when the checks on %s were asked for", strings.TrimSpace(run.Name), head, commit)
		}
		name := strings.TrimSpace(run.Name)
		if !seen[name] {
			continue
		}
		if held, ok := latest[name]; !ok || run.ID > reported.CheckRuns[held].ID {
			latest[name] = index
		}
	}
	reading := RequiredCheckReading{Commit: commit}
	for _, name := range names {
		index, ok := latest[name]
		if !ok {
			reading.Checks = append(reading.Checks, RequiredCheck{Name: name, Result: CheckNotStarted})
			continue
		}
		run := reported.CheckRuns[index]
		conclusion := strings.ToLower(strings.TrimSpace(run.Conclusion))
		reading.Checks = append(reading.Checks, RequiredCheck{
			Name:       name,
			Result:     checkResult(run.Status, conclusion),
			Commit:     commit,
			Conclusion: conclusion,
			ID:         run.ID,
			URL:        strings.TrimSpace(run.HTMLURL),
		})
	}
	return reading, nil
}

// WorkflowDigest is a digest of the forge's workflow files as they stand at one
// commit, named in full: each file's path and the forge's identifier for its
// content, in path order. A check result is recorded beside it, so a change to
// any workflow file withdraws the result. A commit with no workflow files, or an
// answer that cannot be read, is an error: a result with nothing to say which
// workflow produced it is not recorded.
func (g GitHub) WorkflowDigest(ctx context.Context, commit string) (string, error) {
	commit = strings.ToLower(strings.TrimSpace(commit))
	if !fullCommitPattern.MatchString(commit) {
		return "", fmt.Errorf("commit %q is not a full commit identifier, and the workflow files are read at one", commit)
	}
	listed, err := g.api(ctx, "repos/{owner}/{repo}/contents/.github/workflows?ref="+commit)
	if err != nil {
		return "", fmt.Errorf("ask the forge for the workflow files at %s: %w", commit, err)
	}
	if listed.Status != execution.ProcessSucceeded {
		return "", g.forgeFailure(fmt.Sprintf("ask the forge for the workflow files at %s", commit), listed)
	}
	if listed.OutputTruncation != "" {
		return "", fmt.Errorf("the forge's list of workflow files at %s was cut and is not read: %s", commit, listed.OutputTruncation)
	}
	var files []struct {
		Path string `json:"path"`
		SHA  string `json:"sha"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(listed.Stdout)), &files); err != nil {
		return "", fmt.Errorf("decode the workflow files at %s: %w", commit, err)
	}
	var lines []string
	for _, file := range files {
		if file.Type != "" && file.Type != "file" {
			continue
		}
		if strings.TrimSpace(file.Path) == "" || strings.TrimSpace(file.SHA) == "" {
			return "", fmt.Errorf("the forge listed a workflow file at %s without a path or content identifier", commit)
		}
		lines = append(lines, file.Path+"\x00"+file.SHA+"\n")
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("the forge has no workflow files at %s", commit)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "")))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// checkResult maps the forge's status and conclusion to a CheckResult. Only a
// finished run with the conclusion success passes.
func checkResult(status, conclusion string) CheckResult {
	if !strings.EqualFold(strings.TrimSpace(status), "completed") {
		return CheckPending
	}
	switch conclusion {
	case "success":
		return CheckPassed
	case "cancelled":
		return CheckCancelled
	case "skipped":
		return CheckSkipped
	}
	if failingConclusions[conclusion] {
		return CheckFailed
	}
	return CheckOther
}

// CheckOutput is what a failing check says, as the harness keeps it: the tests
// its output names as failing, and the end of its output. Both are scrubbed of
// secrets.
type CheckOutput struct {
	FailingTests []string
	// Tail is the last MaxCIOutputLines lines, cut from the front to at most
	// MaxCIOutputBytes. Cut reports that earlier output was left out.
	Tail string
	Cut  bool
}

var (
	goFailingTest     = regexp.MustCompile(`--- FAIL: (\S+)`)
	pytestFailingTest = regexp.MustCompile(`^FAILED (\S+)`)
	// logStamp is the timestamp the forge puts at the start of each log line.
	logStamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z `)
)

// CheckOutput reads the log of the Actions job behind a failing check and
// returns its failing tests and its end. A log that cannot be read, or is
// longer than the harness keeps of a command's output (so that its end was not
// kept), is an error rather than an empty output.
//
// Secrets are removed — the values the harness knows and any recognized by
// their shape — before the output is returned, so nothing written from it
// carries one.
func (g GitHub) CheckOutput(ctx context.Context, check RequiredCheck) (CheckOutput, error) {
	if check.ID <= 0 {
		return CheckOutput{}, fmt.Errorf("check %q has no check run to read the output of", check.Name)
	}
	result, err := g.api(ctx, fmt.Sprintf("repos/{owner}/{repo}/actions/jobs/%d/logs", check.ID))
	if err != nil {
		return CheckOutput{}, fmt.Errorf("read the forge's log of check %q: %w", check.Name, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return CheckOutput{}, g.forgeFailure(fmt.Sprintf("read the forge's log of check %q", check.Name), result)
	}
	if result.OutputTruncation != "" {
		return CheckOutput{}, fmt.Errorf("the forge's log of check %q is longer than the harness keeps of a command's output, so its end was not read: %s", check.Name, result.OutputTruncation)
	}
	scrub := execution.NewRedactor(g.RedactValues...).WithSecretShapes()
	log := scrub.Redact(result.Stdout)
	output := CheckOutput{FailingTests: failingTests(log)}
	tail := logTail(log, MaxCIOutputLines)
	output.Tail, output.Cut = cutFront(tail, MaxCIOutputBytes)
	return output, nil
}

// failingTests names the tests a log reports failing, each once, in the order
// it first names them, for the runners whose output the harness recognizes: Go
// and pytest.
func failingTests(log string) []string {
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimRight(logStamp.ReplaceAllString(line, ""), "\r")
		var name string
		if match := goFailingTest.FindStringSubmatch(line); match != nil {
			name = match[1]
		} else if match := pytestFailingTest.FindStringSubmatch(line); match != nil {
			name = match[1]
		}
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) > maxFailingTests {
		names = names[:maxFailingTests]
	}
	return names
}

// cutFront keeps the end of text within maxBytes, starting at a line when it
// can and otherwise at a character, and reports whether it left anything out.
func cutFront(text string, maxBytes int) (string, bool) {
	if len(text) <= maxBytes {
		return text, false
	}
	start := len(text) - maxBytes
	if newline := strings.IndexByte(text[start:], '\n'); newline >= 0 && start+newline+1 < len(text) {
		return text[start+newline+1:], true
	}
	for start < len(text) && !isRuneStart(text[start]) {
		start++
	}
	return text[start:], true
}
