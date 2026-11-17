package orchestrator

// While GitHub holds a merge the queue asked for, every pass asks GitHub what
// the merge is waiting on and keeps the answer on the entry. A withdrawal for
// waiting too long names what GitHub last said; a read that failed is kept as
// a failure and not counted as GitHub holding the merge; and a required check
// that failed ends the wait at once.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recordedGitHub answers the GitHub client's merge-wait question with a reply
// recorded under internal/publish/testdata/mergewait, or fails it with the
// message given, as the gh command line does when GitHub cannot be reached.
type recordedGitHub struct {
	reply  string
	refuse string
	asked  int
}

func (r *recordedGitHub) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	joined := strings.Join(command.Args, " ")
	switch {
	case strings.Contains(joined, "remote get-url"):
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"}, nil
	case strings.Contains(joined, "mergeStateStatus"):
		r.asked++
		if r.refuse != "" {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: r.refuse}, nil
		}
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: r.reply}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "not recorded: " + joined}, nil
}

func recordedReply(t *testing.T, name string) string {
	t.Helper()
	reply, err := os.ReadFile(filepath.Join("..", "publish", "testdata", "mergewait", name))
	if err != nil {
		t.Fatalf("read the recorded reply %s: %v", name, err)
	}
	return string(reply)
}

// heldMergeFixture is a merge GitHub holds for the queue's first entry, with
// the promoter asking GitHub through gh answered from a recording.
func heldMergeFixture(t *testing.T, github *recordedGitHub) (*promotionFixture, *withdrawingForge, *recordingRepair) {
	t.Helper()
	f, withdrawer, _ := queuedMergeFixture(t)
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	f.promoter.Waits, f.promoter.Readings = publish.GitHub{Runner: github}, f.queue
	return f, withdrawer, repair
}

// passAt runs a pass whose driver reads the time as now plus ahead, with the
// limit on a held merge's wait set to an hour.
func (f *promotionFixture) passAt(ahead time.Duration) runstate.MergeQueuePass {
	f.t.Helper()
	f.worker.Pipeline.Config.Triage.StuckMergeAge = config.Duration(time.Hour)
	driver := f.driver()
	driver.Now = func() time.Time { return time.Now().Add(ahead) }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return driver.Pass(ctx, queueKey)
}

func (f *promotionFixture) forgeReading() runstate.MergeQueueForgeReading {
	f.t.Helper()
	reading, found, err := f.queue.ForgeReading(queueKey, f.entry.EntryID)
	if err != nil || !found {
		f.t.Fatalf("ForgeReading() = %t, %v; want the latest reading kept on the entry", found, err)
	}
	return reading
}

func TestAMergeHeldOnAPendingRequiredCheckIsWithdrawnNamingThatCheck(t *testing.T) {
	t.Parallel()

	github := &recordedGitHub{reply: recordedReply(t, "pending-required-check.json")}
	f, withdrawer, repair := heldMergeFixture(t, github)

	// Inside the limit: the pass waits, says what GitHub said, and keeps it.
	pass := f.passAt(0)
	if pass.Outcome != runstate.MergeQueuePassWaiting || !strings.Contains(pass.Says, `the required check "build" has not finished`) {
		t.Fatalf("Pass() = %#v, want it waiting and naming the build GitHub waits on", pass)
	}
	reading := f.forgeReading()
	if reading.Unread != "" || reading.MergeState != "BLOCKED" || len(reading.PendingChecks) != 1 || reading.PendingChecks[0] != "build" || reading.At.IsZero() {
		t.Fatalf("reading = %#v, want GitHub's BLOCKED with the pending build, and its time", reading)
	}
	if withdrawer.count() != 0 {
		t.Fatal("a merge inside the limit was withdrawn")
	}

	// Past the limit: withdrawn, and the handback names what GitHub last said.
	pass = f.passAt(2 * time.Hour)
	if pass.Outcome != runstate.MergeQueuePassHandedBack || withdrawer.count() != 1 || repair.count() != 1 {
		t.Fatalf("Pass() = %#v with %d withdrawals, want the merge withdrawn and handed back", pass, withdrawer.count())
	}
	reason := f.landing().Handback.Reason
	for _, want := range []string{"longer than triage.stuck_merge_age (1 hour)", "GitHub said the merge of pull request", `the required check "build" has not finished`, "merge state BLOCKED"} {
		if !strings.Contains(reason, want) {
			t.Errorf("handback reason = %q, want it to say %q", reason, want)
		}
	}
}

func TestAMergeBlockedByABranchRuleIsWithdrawnNamingTheRule(t *testing.T) {
	t.Parallel()

	github := &recordedGitHub{reply: recordedReply(t, "branch-rule.json")}
	f, withdrawer, _ := heldMergeFixture(t, github)
	if pass := f.passAt(2 * time.Hour); pass.Outcome != runstate.MergeQueuePassHandedBack || withdrawer.count() != 1 {
		t.Fatalf("Pass() = %#v with %d withdrawals, want the merge withdrawn past the limit", pass, withdrawer.count())
	}
	if reason := f.landing().Handback.Reason; !strings.Contains(reason, "a branch rule requires an approving review") {
		t.Errorf("handback reason = %q, want the required review GitHub reports named", reason)
	}
}

func TestAFailedReadOfGitHubIsKeptAndNotCountedAsGitHubHoldingTheMerge(t *testing.T) {
	t.Parallel()

	github := &recordedGitHub{refuse: `Post "https://api.github.com/graphql": tls: failed to verify certificate: x509: certificate signed by unknown authority`}
	f, withdrawer, _ := heldMergeFixture(t, github)

	// Past the limit, but GitHub cannot be asked: nothing is withdrawn, and the
	// entry keeps the failure rather than a reason.
	pass := f.passAt(2 * time.Hour)
	if pass.Outcome != runstate.MergeQueuePassWaiting || withdrawer.count() != 0 {
		t.Fatalf("Pass() = %#v with %d withdrawals, want it waiting with nothing withdrawn", pass, withdrawer.count())
	}
	if !strings.Contains(pass.Says, "GitHub could not be asked") || !strings.Contains(pass.Says, "x509") {
		t.Errorf("pass says %q, want the failed read named", pass.Says)
	}
	if reading := f.forgeReading(); reading.Unread == "" || reading.WaitingOn != "" || !strings.Contains(reading.Unread, "certificate") {
		t.Fatalf("reading = %#v, want the failed read kept as the reading", reading)
	}

	// GitHub answers again. The time since the failed read is taken off the
	// wait, so a merge that would otherwise be past the limit is not.
	reading := f.forgeReading()
	reading.At = reading.At.Add(-2 * time.Hour)
	if err := f.queue.RecordForgeReading(context.Background(), queueKey, f.entry.EntryID, reading); err != nil {
		t.Fatal(err)
	}
	github.refuse, github.reply = "", recordedReply(t, "pending-required-check.json")
	pass = f.passAt(2 * time.Hour)
	if pass.Outcome != runstate.MergeQueuePassWaiting || withdrawer.count() != 0 {
		t.Fatalf("Pass() = %#v with %d withdrawals, want the unread time left out of the wait", pass, withdrawer.count())
	}
	if after := f.forgeReading(); after.Unread != "" || after.UnreadFor < 2*time.Hour {
		t.Fatalf("reading = %#v, want GitHub's answer kept with at least two hours unread", after)
	}
}

func TestAFailedRequiredCheckEndsTheWaitAtOnce(t *testing.T) {
	t.Parallel()

	github := &recordedGitHub{reply: recordedReply(t, "failed-required-check.json")}
	f, withdrawer, repair := heldMergeFixture(t, github)

	// Well inside the limit, the failed build ends the wait.
	pass := f.passAt(0)
	if pass.Outcome != runstate.MergeQueuePassHandedBack || withdrawer.count() != 1 || repair.count() != 1 {
		t.Fatalf("Pass() = %#v with %d withdrawals, want the merge withdrawn at once and handed back", pass, withdrawer.count())
	}
	reason := f.landing().Handback.Reason
	if !strings.Contains(reason, `required check "build" (failure) failed`) || strings.Contains(reason, "docs-preview") {
		t.Errorf("handback reason = %q, want the failed required build named and nothing else", reason)
	}
}

// A required check still running keeps the merge waiting even beside one that
// failed, since GitHub may yet be waiting on a re-run of it.
func TestAFailedRequiredCheckBesideOneStillRunningLeavesTheMergeWaiting(t *testing.T) {
	t.Parallel()

	wait := publish.MergeWait{Required: []publish.RequiredCheck{
		{Name: "build", State: publish.RequiredCheckFailed, Conclusion: "failure"},
		{Name: "race", State: publish.RequiredCheckPending},
	}}
	if got := requiredFailure(wait, 1); got != "" {
		t.Errorf("requiredFailure() = %q, want nothing while a required check runs", got)
	}
	wait.Required[1] = publish.RequiredCheck{Name: "race", State: publish.RequiredCheckFailed, Conclusion: "cancelled"}
	if got := requiredFailure(wait, 1); !strings.Contains(got, `"build" (failure)`) || strings.Contains(got, "race") {
		t.Errorf("requiredFailure() = %q, want the failed build named and the cancelled race left to its re-run", got)
	}
}
