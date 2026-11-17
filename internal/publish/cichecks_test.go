package publish

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const (
	ciCommit  = "0123456789abcdef0123456789abcdef01234567"
	ciOther   = "fedcba9876543210fedcba9876543210fedcba98"
	ciBranch  = "yoyodyne/task/abcd1234"
	ciRemote  = "https://example.invalid/acme/thing\n"
	succeeded = execution.ProcessSucceeded
)

func ciRunner() *scriptedRunner {
	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: succeeded, Stdout: ciRemote})
	return runner
}

func ciRequest() Request {
	return Request{Head: ciBranch, Base: "main", Title: "task", Body: "opened by the harness", Mark: testMark}
}

func TestPushBranchPushesTheExactCommitAndProvesItArrived(t *testing.T) {
	t.Parallel()

	runner := ciRunner()
	runner.reply("ls-remote", execution.ProcessResult{Status: succeeded, Stdout: ciCommit + "\trefs/heads/" + ciBranch + "\n"})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}
	if err := forge.PushBranch(context.Background(), PushRequest{Branch: ciBranch, Commit: ciCommit}); err != nil {
		t.Fatalf("PushBranch() error = %v", err)
	}
	pushed := runner.matching(" push ")
	if len(pushed) != 1 {
		t.Fatalf("pushes = %v", pushed)
	}
	joined := strings.Join(pushed[0], " ")
	if !strings.Contains(joined, "origin "+ciCommit+":refs/heads/"+ciBranch) || strings.Contains(joined, "force") {
		t.Errorf("push = %s, want the full refspec for the exact commit and no force", joined)
	}
}

func TestPushBranchReplacesOnlyUnderALeaseOnThePreviousCommit(t *testing.T) {
	t.Parallel()

	runner := ciRunner()
	runner.reply("ls-remote", execution.ProcessResult{Status: succeeded, Stdout: ciCommit + "\trefs/heads/" + ciBranch + "\n"})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}
	if err := forge.PushBranch(context.Background(), PushRequest{Branch: ciBranch, Commit: ciCommit, Previous: ciOther}); err != nil {
		t.Fatalf("PushBranch() error = %v", err)
	}
	if joined := strings.Join(runner.matching(" push ")[0], " "); !strings.Contains(joined, "--force-with-lease=refs/heads/"+ciBranch+":"+ciOther) {
		t.Errorf("push = %s, want a lease on the previous commit", joined)
	}
}

func TestPushBranchReportsARefusalAndABranchThatDidNotMove(t *testing.T) {
	t.Parallel()

	refused := ciRunner()
	refused.reply(" push ", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "remote: protected branch\n"})
	forge := GitHub{Runner: refused, Dir: t.TempDir()}
	if err := forge.PushBranch(context.Background(), PushRequest{Branch: ciBranch, Commit: ciCommit}); err == nil || !strings.Contains(err.Error(), "protected branch") {
		t.Errorf("refused push error = %v", err)
	}

	stale := ciRunner()
	stale.reply("ls-remote", execution.ProcessResult{Status: succeeded, Stdout: ciOther + "\trefs/heads/" + ciBranch + "\n"})
	if err := (GitHub{Runner: stale, Dir: t.TempDir()}).PushBranch(context.Background(), PushRequest{Branch: ciBranch, Commit: ciCommit}); err == nil {
		t.Error("a push the remote does not show returned no error")
	}
	if err := forge.PushBranch(context.Background(), PushRequest{Branch: ciBranch, Commit: "not-a-commit"}); err == nil {
		t.Error("a push of no commit returned no error")
	}
}

func TestEnsureDraftOpensADraftAndUpdatesTheSameRequestLater(t *testing.T) {
	t.Parallel()

	runner := ciRunner()
	runner.reply("pr list", execution.ProcessResult{Status: succeeded, Stdout: "[]\n"})
	runner.replyAfter("pr list", 1, execution.ProcessResult{Status: succeeded,
		Stdout: `[{"number":7,"url":"https://example.invalid/pull/7","state":"OPEN","mergedAt":""}]`})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	opened, err := forge.EnsureDraft(context.Background(), ciRequest())
	if err != nil || opened.Number != 7 {
		t.Fatalf("EnsureDraft() = %#v, %v", opened, err)
	}
	created := runner.matching("pr create")
	if len(created) != 1 || !contains(created[0], "--draft") {
		t.Fatalf("pr create calls = %v, want one, as a draft", created)
	}

	again, err := forge.EnsureDraft(context.Background(), ciRequest())
	if err != nil || again.Number != 7 {
		t.Fatalf("EnsureDraft() again = %#v, %v", again, err)
	}
	if created := runner.matching("pr create"); len(created) != 1 {
		t.Errorf("pull requests created = %d after a second call, want the one", len(created))
	}
	edited := runner.matching("pr edit 7")
	if len(edited) != 1 || contains(edited[0], "--draft") || contains(edited[0], "--ready") {
		t.Errorf("pr edit calls = %v, want one that leaves the draft state alone", edited)
	}
}

func TestEnsureDraftRefusesARequestThatIsNoLongerOpen(t *testing.T) {
	t.Parallel()

	runner := ciRunner()
	runner.reply("pr list", execution.ProcessResult{Status: succeeded,
		Stdout: `[{"number":9,"url":"https://example.invalid/pull/9","state":"MERGED","mergedAt":"2026-08-16T00:00:00Z"}]`})
	if _, err := (GitHub{Runner: runner}).EnsureDraft(context.Background(), ciRequest()); err == nil || !strings.Contains(err.Error(), "cannot be republished into") {
		t.Fatalf("EnsureDraft() merged error = %v", err)
	}
}

func checkRunsAnswer(runs ...string) string {
	return fmt.Sprintf(`{"total_count":%d,"check_runs":[%s]}`, len(runs), strings.Join(runs, ","))
}

func checkRun(id int, name, sha, status, conclusion string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"head_sha":%q,"status":%q,"conclusion":%q,"html_url":"https://example.invalid/runs/%d"}`,
		id, name, sha, status, conclusion, id)
}

func TestRequiredChecksReturnsNameResultAndCommitOfEachRequiredCheckOnly(t *testing.T) {
	t.Parallel()

	runner := ciRunner()
	runner.reply("/check-runs?", execution.ProcessResult{Status: succeeded, Stdout: checkRunsAnswer(
		checkRun(1, "test", ciCommit, "completed", "success"),
		checkRun(2, "race", ciCommit, "completed", "failure"),
		checkRun(3, "vet", ciCommit, "in_progress", ""),
		checkRun(4, "lint", ciCommit, "completed", "failure"),
	)})
	reading, err := (GitHub{Runner: runner}).RequiredChecks(context.Background(), ciCommit, []string{"test", "race", "vet", "build"})
	if err != nil {
		t.Fatalf("RequiredChecks() error = %v", err)
	}
	want := map[string]CheckResult{"test": CheckPassed, "race": CheckFailed, "vet": CheckPending, "build": CheckNotStarted}
	if len(reading.Checks) != len(want) || reading.AllPassed() {
		t.Fatalf("reading = %#v", reading)
	}
	for _, check := range reading.Checks {
		if check.Result != want[check.Name] {
			t.Errorf("check %q = %q, want %q", check.Name, check.Result, want[check.Name])
		}
		if check.Result != CheckNotStarted && check.Commit != ciCommit {
			t.Errorf("check %q commit = %q, want %q", check.Name, check.Commit, ciCommit)
		}
	}
	if got := runner.matching("check-runs")[0]; !strings.Contains(strings.Join(got, " "), "commits/"+ciCommit+"/check-runs") {
		t.Errorf("the forge was asked %v, want the named commit", got)
	}
}

func TestRequiredChecksPassesOnlyWhenEveryRequiredCheckPassed(t *testing.T) {
	t.Parallel()

	runner := ciRunner()
	runner.reply("/check-runs?", execution.ProcessResult{Status: succeeded, Stdout: checkRunsAnswer(
		checkRun(1, "test", ciCommit, "completed", "success"),
		checkRun(2, "race", ciCommit, "completed", "success"),
	)})
	reading, err := (GitHub{Runner: runner}).RequiredChecks(context.Background(), ciCommit, []string{"test", "race"})
	if err != nil || !reading.AllPassed() {
		t.Fatalf("reading = %#v, %v", reading, err)
	}
}

// A result the forge recorded for another commit is never this commit's: a
// forge that answers with one is an error, and one that has only another
// commit's results reads as not started.
func TestRequiredChecksNeverReturnsAnotherCommitsResult(t *testing.T) {
	t.Parallel()

	mixed := ciRunner()
	mixed.reply("/check-runs?", execution.ProcessResult{Status: succeeded, Stdout: checkRunsAnswer(
		checkRun(1, "test", ciOther, "completed", "success"),
	)})
	if reading, err := (GitHub{Runner: mixed}).RequiredChecks(context.Background(), ciCommit, []string{"test"}); err == nil {
		t.Fatalf("a result for another commit was read as %#v", reading)
	}

	none := ciRunner()
	none.reply("/check-runs?", execution.ProcessResult{Status: succeeded, Stdout: checkRunsAnswer()})
	reading, err := (GitHub{Runner: none}).RequiredChecks(context.Background(), ciCommit, []string{"test"})
	if err != nil || reading.AllPassed() || reading.Checks[0].Result != CheckNotStarted {
		t.Fatalf("no runs read as %#v, %v", reading, err)
	}

	if _, err := (GitHub{Runner: none}).RequiredChecks(context.Background(), "0123456", []string{"test"}); err == nil {
		t.Error("a commit named by a prefix was accepted")
	}
}

func TestRequiredChecksTakesTheLatestRunOfAName(t *testing.T) {
	t.Parallel()

	runner := ciRunner()
	runner.reply("/check-runs?", execution.ProcessResult{Status: succeeded, Stdout: checkRunsAnswer(
		checkRun(5, "test", ciCommit, "completed", "failure"),
		checkRun(9, "test", ciCommit, "completed", "success"),
	)})
	reading, err := (GitHub{Runner: runner}).RequiredChecks(context.Background(), ciCommit, []string{"test"})
	if err != nil || !reading.AllPassed() || reading.Checks[0].ID != 9 {
		t.Fatalf("reading = %#v, %v", reading, err)
	}
}

func TestRequiredChecksMapsEveryOtherEndingAwayFromPassed(t *testing.T) {
	t.Parallel()

	for conclusion, want := range map[string]CheckResult{
		"cancelled": CheckCancelled, "skipped": CheckSkipped, "timed_out": CheckFailed,
		"action_required": CheckFailed, "neutral": CheckOther, "stale": CheckOther, "": CheckOther,
	} {
		if got := checkResult("completed", conclusion); got != want {
			t.Errorf("conclusion %q = %q, want %q", conclusion, got, want)
		}
	}
	if got := checkResult("queued", ""); got != CheckPending {
		t.Errorf("queued = %q", got)
	}
}

// A forge the harness cannot read is an error, never an empty or passing
// reading.
func TestRequiredChecksOnAForgeItCannotReadIsAnError(t *testing.T) {
	t.Parallel()

	unreachable := ciRunner()
	unreachable.reply("/check-runs?", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 502: bad gateway"})
	garbled := ciRunner()
	garbled.reply("/check-runs?", execution.ProcessResult{Status: succeeded, Stdout: "<html>"})
	unsized := ciRunner()
	unsized.reply("/check-runs?", execution.ProcessResult{Status: succeeded, Stdout: `{"check_runs":[]}`})
	truncated := ciRunner()
	truncated.reply("/check-runs?", execution.ProcessResult{Status: succeeded, Stdout: checkRunsAnswer(), OutputTruncation: "stopped at 1 MiB"})
	partial := ciRunner()
	partial.reply("/check-runs?", execution.ProcessResult{Status: succeeded, Stdout: `{"total_count":150,"check_runs":[` + checkRun(1, "test", ciCommit, "completed", "success") + `]}`})
	failed := ciRunner()
	failed.fail("/check-runs?", errors.New("spawn gh: no such file"))

	for name, runner := range map[string]*scriptedRunner{"unreachable": unreachable, "garbled": garbled, "unsized": unsized, "truncated": truncated, "partial": partial, "no gh": failed} {
		reading, err := (GitHub{Runner: runner}).RequiredChecks(context.Background(), ciCommit, []string{"test"})
		if err == nil {
			t.Errorf("%s: read %#v with no error", name, reading)
		}
		if reading.AllPassed() || len(reading.Checks) != 0 {
			t.Errorf("%s: returned a reading %#v beside the error", name, reading)
		}
	}
	if _, err := (GitHub{Runner: ciRunner()}).RequiredChecks(context.Background(), ciCommit, nil); err == nil {
		t.Error("no required checks returned no error")
	}
}

func TestCheckOutputNamesFailingTestsAndKeepsTheEndOfTheOutput(t *testing.T) {
	t.Parallel()

	var log strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&log, "2026-11-17T10:00:00.0000000Z line %d\n", i)
	}
	log.WriteString("2026-11-17T10:00:01.0000000Z --- FAIL: TestOne (0.00s)\n")
	log.WriteString("2026-11-17T10:00:01.0000000Z     --- FAIL: TestOne/sub (0.00s)\n")
	log.WriteString("2026-11-17T10:00:01.0000000Z --- FAIL: TestOne (0.00s)\n")
	log.WriteString("2026-11-17T10:00:02.0000000Z FAIL\tgithub.com/acme/thing/pkg\t0.1s\n")
	log.WriteString("2026-11-17T10:00:03.0000000Z ##[error]Process completed with exit code 1.\n")
	runner := ciRunner()
	runner.reply("actions/jobs/42/logs", execution.ProcessResult{Status: succeeded, Stdout: log.String()})

	output, err := (GitHub{Runner: runner}).CheckOutput(context.Background(), RequiredCheck{Name: "test", ID: 42})
	if err != nil {
		t.Fatalf("CheckOutput() error = %v", err)
	}
	if strings.Join(output.FailingTests, ",") != "TestOne,TestOne/sub" {
		t.Errorf("failing tests = %v", output.FailingTests)
	}
	lines := strings.Split(output.Tail, "\n")
	if len(lines) != MaxCIOutputLines || !strings.Contains(lines[len(lines)-1], "Process completed") {
		t.Errorf("tail has %d lines ending %q, want %d ending at the error", len(lines), lines[len(lines)-1], MaxCIOutputLines)
	}
}

func TestCheckOutputIsBoundedTo16KiB(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 1000)
	var log strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&log, "%s %d\n", long, i)
	}
	runner := ciRunner()
	runner.reply("actions/jobs/42/logs", execution.ProcessResult{Status: succeeded, Stdout: log.String()})
	output, err := (GitHub{Runner: runner}).CheckOutput(context.Background(), RequiredCheck{Name: "test", ID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Tail) > MaxCIOutputBytes || !output.Cut || !strings.HasSuffix(output.Tail, " 99") {
		t.Errorf("tail is %d bytes, cut %v, ending %q", len(output.Tail), output.Cut, output.Tail[max(len(output.Tail)-8, 0):])
	}
}

// Secrets are removed before anything is returned for a caller to write: the
// values the harness holds, and ones recognized by their shape.
func TestCheckOutputRemovesSecretsFromTheOutputAndTheTestNames(t *testing.T) {
	t.Parallel()

	const held = "hunter2-held-secret"
	const shaped = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	log := "--- FAIL: TestUses_" + held + " (0.00s)\n" +
		"token " + held + " and " + shaped + "\n" +
		"##[error]Process completed with exit code 1.\n"
	runner := ciRunner()
	runner.reply("actions/jobs/42/logs", execution.ProcessResult{Status: succeeded, Stdout: log})
	output, err := (GitHub{Runner: runner, RedactValues: []string{held}}).CheckOutput(context.Background(), RequiredCheck{Name: "test", ID: 42})
	if err != nil {
		t.Fatal(err)
	}
	everything := output.Tail + strings.Join(output.FailingTests, "\n")
	if strings.Contains(everything, held) || strings.Contains(everything, shaped) {
		t.Errorf("a secret survived: %q", everything)
	}
	if len(output.FailingTests) != 1 {
		t.Errorf("failing tests = %v, want the one test still named", output.FailingTests)
	}
}

func TestCheckOutputOnALogItCannotReadIsAnError(t *testing.T) {
	t.Parallel()

	refused := ciRunner()
	refused.reply("actions/jobs/42/logs", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 403: Resource not accessible by integration"})
	if _, err := (GitHub{Runner: refused}).CheckOutput(context.Background(), RequiredCheck{Name: "test", ID: 42}); !errors.Is(err, ErrForgeAccessRefused) {
		t.Errorf("refused log error = %v", err)
	}
	cut := ciRunner()
	cut.reply("actions/jobs/42/logs", execution.ProcessResult{Status: succeeded, Stdout: "start", OutputTruncation: "stopped"})
	if _, err := (GitHub{Runner: cut}).CheckOutput(context.Background(), RequiredCheck{Name: "test", ID: 42}); err == nil {
		t.Error("a log whose end was not kept returned no error")
	}
	if _, err := (GitHub{Runner: ciRunner()}).CheckOutput(context.Background(), RequiredCheck{Name: "test"}); err == nil {
		t.Error("a check with no run returned no error")
	}
}

// The table in internal/config says what the GitHub adapter does. Each
// capability it marks is run here against a fake forge command, and a
// capability the list names that is not run here fails, so the table cannot
// claim something the adapter does not do.
func TestTheConfigurationTableMatchesWhatTheAdapterDoes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	probes := map[config.ForgeCapability]func(t *testing.T) error{
		config.ForgePushBranch: func(t *testing.T) error {
			runner := ciRunner()
			runner.reply("ls-remote", execution.ProcessResult{Status: succeeded, Stdout: ciCommit + "\trefs/heads/" + ciBranch + "\n"})
			return (GitHub{Runner: runner, Dir: t.TempDir()}).PushBranch(ctx, PushRequest{Branch: ciBranch, Commit: ciCommit})
		},
		config.ForgeOpenDraftRequest: func(t *testing.T) error {
			runner := ciRunner()
			runner.reply("pr list", execution.ProcessResult{Status: succeeded, Stdout: "[]\n"})
			runner.replyAfter("pr list", 1, execution.ProcessResult{Status: succeeded, Stdout: `[{"number":7,"url":"u","state":"OPEN"}]`})
			if _, err := (GitHub{Runner: runner, Dir: t.TempDir()}).EnsureDraft(ctx, ciRequest()); err != nil {
				return err
			}
			if created := runner.matching("pr create"); len(created) != 1 || !contains(created[0], "--draft") {
				return fmt.Errorf("no draft was opened: %v", created)
			}
			return nil
		},
		config.ForgeReadCheckResults: func(t *testing.T) error {
			runner := ciRunner()
			runner.reply("/check-runs?", execution.ProcessResult{Status: succeeded, Stdout: checkRunsAnswer(checkRun(1, "test", ciCommit, "completed", "success"))})
			reading, err := (GitHub{Runner: runner}).RequiredChecks(ctx, ciCommit, []string{"test"})
			if err == nil && !reading.AllPassed() {
				return errors.New("a passing check did not read as passed")
			}
			return err
		},
		config.ForgeReadCheckOutput: func(t *testing.T) error {
			runner := ciRunner()
			runner.reply("actions/jobs/1/logs", execution.ProcessResult{Status: succeeded, Stdout: "--- FAIL: TestX\n"})
			output, err := (GitHub{Runner: runner}).CheckOutput(ctx, RequiredCheck{Name: "test", ID: 1})
			if err == nil && len(output.FailingTests) != 1 {
				return errors.New("a failing test was not named")
			}
			return err
		},
	}
	for _, capability := range config.CIForgeCapabilities() {
		probe, ok := probes[capability]
		if !ok {
			t.Errorf("capability %q is listed by the configuration and not exercised here: the adapter has no probe for it", capability)
			continue
		}
		if !config.ForgeAdapterCan("github", capability) {
			t.Errorf("the adapter does %q, and the configuration's table says it cannot", capability)
		}
		if err := probe(t); err != nil {
			t.Errorf("capability %q: the table says the adapter does it, and it did not: %v", capability, err)
		}
	}
}
