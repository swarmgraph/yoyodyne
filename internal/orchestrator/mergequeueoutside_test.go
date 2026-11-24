package orchestrator

// A change that reached the target outside the merge queue: its pull request
// merged by hand on the forge, or its approved head put on the target, while
// the entry waited, leaves the queue as landed without a candidate built or a
// check run; one whose pull request was closed by hand is released with that
// as its reason. Either way the entries behind it go on.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// handActedForge answers what became of a pull request a person acted on; it
// is asked nothing else.
type handActedForge struct {
	PullRequests
	mu     sync.Mutex
	answer publish.PullRequest
	err    error
	asked  []string
}

func (f *handActedForge) State(_ context.Context, head string) (publish.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, head)
	return f.answer, f.err
}

// publishedBy gives the admitted run the pull request a person then acts on,
// and the promoter a forge that answers about it.
func (f *promotionFixture) publishedBy(answer publish.PullRequest) *handActedForge {
	f.t.Helper()
	f.run.admittedRun.state.Branch = "change"
	f.run.admittedRun.state.PullRequest = &runstate.PullRequest{Branch: "change", Number: 1759, URL: "https://forge.example/pull/1759", HeadCommit: f.head}
	forge := &handActedForge{answer: answer}
	pipeline := *f.promoter.Pipeline
	pipeline.Publisher = forge
	f.promoter.Pipeline = &pipeline
	return forge
}

// assertBuiltNothing checks no candidate was built, checked, or reviewed.
func (f *promotionFixture) assertBuiltNothing() {
	f.t.Helper()
	if generations := f.generations(); len(generations) != 0 {
		f.t.Fatalf("%d candidates were built, want none for a change that left the queue before its turn", len(generations))
	}
	if f.checks.count != 0 || len(f.reviewer.requests) != 0 {
		f.t.Fatalf("checks ran %d times and the reviewer was asked %d times, want neither", f.checks.count, len(f.reviewer.requests))
	}
}

func (f *promotionFixture) assertLandedOutside(carrying string) {
	f.t.Helper()
	handback := f.landing().Handback
	if handback == nil || handback.Continuation != runstate.MergeQueueLandedOutside || handback.TargetBase != carrying || handback.HandedBackAt == nil {
		f.t.Fatalf("handback = %#v, want the entry landed outside the queue at %s and recorded", handback, carrying)
	}
	if saved := f.run.latest(); saved.Integration == nil || saved.Integration.TargetCommit != carrying {
		f.t.Fatalf("run integration = %#v, want the landing at %s recorded on the run", saved.Integration, carrying)
	}
	if !f.tracker.Closed || !strings.Contains(f.tracker.Notes, "outside the merge queue") {
		f.t.Fatalf("work item closed %v with notes %q, want it closed and told the change landed outside the queue", f.tracker.Closed, f.tracker.Notes)
	}
	if pending := f.pending(); len(pending) != 0 {
		f.t.Fatalf("Pending() = %v, want nothing left in the queue", pending)
	}
	f.assertBuiltNothing()
}

func TestAnEntryWhosePullRequestWasMergedByHandLeavesTheQueueAsLanded(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	// A person squash-merged the run's pull request on the forge while the entry
	// waited, so the target carries the change but not the approved head.
	runPipelineGit(t, f.repository, "merge", "--squash", f.head)
	runPipelineGit(t, f.repository, "commit", "-m", "the approved change, merged by hand (#1759)")
	merged := f.local("main")
	forge := f.publishedBy(publish.PullRequest{Number: 1759, State: "MERGED", Merged: true, MergeCommit: merged, HeadCommit: f.head})

	pass := f.pass(queueKey)
	if pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the entry recorded as landed", pass)
	}
	f.assertLandedOutside(merged)
	if !strings.Contains(f.landing().Handback.Reason, "pull request #1759") {
		t.Fatalf("reason = %q, want it to name the pull request merged by hand", f.landing().Handback.Reason)
	}
	if len(forge.asked) != 1 || forge.asked[0] != "change" {
		t.Fatalf("the forge was asked about %v, want the run's own branch once", forge.asked)
	}
	if f.local("main") != merged {
		t.Fatal("the target moved for a change that had already landed")
	}
	// Asked again, the entry stays where it is, and the item is told once.
	f.pass(queueKey)
	if count := strings.Count(f.tracker.Notes, mergeQueueLandedOutsideHeadline(f.entry)); count != 1 {
		t.Fatalf("the item was told %d times, want once", count)
	}
}

func TestAnEntryWhoseApprovedHeadIsAlreadyOnTheTargetLeavesTheQueueAsLanded(t *testing.T) {
	t.Parallel()

	// No forge is asked: the target itself already carries the approved head.
	f := newPromotionFixture(t, landLocally)
	runPipelineGit(t, f.repository, "merge", "--no-ff", "-m", "merged by hand", f.head)
	merged := f.local("main")

	pass := f.pass(queueKey)
	if pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the entry recorded as landed", pass)
	}
	f.assertLandedOutside(merged)
}

func TestAnEntryWhosePullRequestWasClosedByHandIsReleasedAndTheQueueGoesOn(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	f.publishedBy(publish.PullRequest{Number: 1759, State: "CLOSED", HeadCommit: f.head})
	behind := f.admitAnother(queueKey, "behind", "feature.txt", "implemented behind it\n")
	base := f.local("main")

	f.pass(queueKey)
	handback := f.landing().Handback
	if handback == nil || handback.Continuation != runstate.MergeQueueReleased || !strings.Contains(handback.Reason, "closed without being merged") {
		t.Fatalf("handback = %#v, want the entry released because its pull request was closed by hand", handback)
	}
	if repair.count() != 1 || repair.given[0].Continuation != runstate.MergeQueueReleased {
		t.Fatalf("repair given %#v, want the change handed back to its run once, kept", repair.given)
	}
	if generations := f.generations(); len(generations) != 0 {
		t.Fatalf("%d candidates were built for the closed entry, want none", len(generations))
	}
	if f.local("main") == base {
		t.Fatal("the entry behind the closed one did not land")
	}
	landed, found, err := f.queue.Landing(queueKey, behind.EntryID)
	if err != nil || !found || landed.Completion == nil {
		t.Fatalf("Landing(behind) = %#v, %t, %v; want the entry behind landed through the queue", landed, found, err)
	}
}

func TestAnOpenPullRequestLeavesTheEntryToBeBuilt(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	f.publishedBy(publish.PullRequest{Number: 1759, State: "OPEN", HeadCommit: f.head})
	pass := f.pass(queueKey)
	if pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the change landed through the queue", pass)
	}
	if landing := f.landing(); landing.Handback != nil || landing.Completion == nil {
		t.Fatalf("landing = %#v, want it landed through the queue's own candidate", landing)
	}
}

func TestAForgeWhoseCertificateCannotBeVerifiedIsNotReadAsAnUnmergedPullRequest(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	forge := f.publishedBy(publish.PullRequest{})
	forge.err = errors.New(`gh pr list failed with exit code 1: Post "https://api.github.com/graphql": tls: failed to verify certificate: x509: OSStatus -26276`)
	base := f.local("main")

	pass := f.pass(queueKey)
	if pass.Outcome != runstate.MergeQueuePassStopped {
		t.Fatalf("Pass() = %#v, want the pass stopped on a forge it cannot verify", pass)
	}
	for _, want := range []string{"could not verify GitHub's certificate", "pull request #1759", "x509: OSStatus -26276"} {
		if !strings.Contains(pass.Says, want) {
			t.Fatalf("the pass says %q, want it to name %q", pass.Says, want)
		}
	}
	f.assertBuiltNothing()
	if landing := f.landing(); landing.Handback != nil || landing.Completion != nil {
		t.Fatalf("landing = %#v, want the entry left waiting for the forge to be read", landing)
	}
	if f.local("main") != base {
		t.Fatal("the target moved while the forge could not be read")
	}
	if pass.PersonOnly != "" {
		t.Fatalf("the pass records %q as only a person's to act on, want nothing: a certificate is not a credential the forge refused", pass.PersonOnly)
	}
}

// An error a pass stops on that is the forge refusing the harness's token,
// however it is wrapped, records a credential only a person can give, so a stop
// that repeats is put to the operator for that reason rather than to a role;
// any other error, a certificate the machine cannot verify among them, records
// nothing.
func TestAStopOnARefusedForgeTokenRecordsACredential(t *testing.T) {
	t.Parallel()

	refused := fmt.Errorf("push the candidate: gh failed with exit code 1: Resource not accessible by integration (HTTP 403): %w", publish.ErrForgeAccessRefused)
	if got := stopPersonOnly(fmt.Errorf("land merge queue entry 45: %w", refused)); got != ownership.PersonCredential {
		t.Fatalf("stopPersonOnly(a refused token) = %q, want a credential", got)
	}
	for _, err := range []error{
		nil,
		fmt.Errorf("%w (%w)", gitworktree.ErrRemoteAuthRefused, gitworktree.ErrRemoteCredentialNotPresented),
		fmt.Errorf("%w: protected branch hook declined", gitworktree.ErrRemotePushRejected),
		errors.New("Permission denied (publickey)"),
		errors.New("tls: failed to verify certificate: x509: OSStatus -26276"),
		errors.New("the forge refused the harness's token access"),
	} {
		if got := stopPersonOnly(err); got != "" {
			t.Fatalf("stopPersonOnly(%v) = %q, want nothing: only the error's type says a person has to act", err, got)
		}
	}
}

// assertStopsRepeat runs three passes and checks each stopped on the entry with
// the same words and recorded want, which is what status reads a repeated stop
// and its owner from.
func (f *promotionFixture) assertStopsRepeat(want ownership.PersonOnlyReason) {
	f.t.Helper()
	var first runstate.MergeQueuePass
	for pass := range 3 {
		stopped := f.pass(queueKey)
		if stopped.Outcome != runstate.MergeQueuePassStopped || stopped.EntryID != f.entry.EntryID || stopped.PersonOnly != want {
			f.t.Fatalf("Pass() = %#v, want it stopped on the entry recording %q", stopped, want)
		}
		if pass == 0 {
			first = stopped
		} else if stopped.Says != first.Says {
			f.t.Fatalf("pass %d says %q, want the same stop as before: %q", pass+1, stopped.Says, first.Says)
		}
	}
}

// The Git remote refusing the harness's SSH key or login keeps its type to the
// end of the pass, and a refusal in which no key was presented is not one.
func TestAStopOnAGitRemoteRefusingTheKeyRecordsACredential(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		err  error
		want ownership.PersonOnlyReason
	}{
		{"the SSH key", fmt.Errorf("%w: git push exited 128: git@github.com: Permission denied (publickey).", gitworktree.ErrRemoteAuthRefused), ownership.PersonCredential},
		{"the login", fmt.Errorf("%w: git push exited 128: fatal: Authentication failed for 'https://github.com/x/y.git/'", gitworktree.ErrRemoteAuthRefused), ownership.PersonCredential},
		{"no key presented", fmt.Errorf("%w: git push exited 255: No user exists for uid 501 (%w)", gitworktree.ErrRemoteAuthRefused, gitworktree.ErrRemoteCredentialNotPresented), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f, lander := pushingFixture(t)
			f.verify()
			lander.set(func(context.Context, string, string, string) error { return test.err })
			f.assertStopsRepeat(test.want)
			lander.set(nil)
			if cleared := f.pass(queueKey); cleared.Outcome != runstate.MergeQueuePassLanded || cleared.PersonOnly != "" {
				t.Fatalf("Pass() once the remote takes the push = %#v, want the change landed", cleared)
			}
		})
	}
}

// GitHub refusing the harness's access while a merge is withdrawn, or while the
// pull request is read to confirm it, keeps its type to the end of the pass,
// though the withdrawal is reported as unresolved rather than as an error. Any
// other answer is the development manager's, and an outage does not stop the
// pass at all.
func TestAStopOnARefusedWithdrawalRecordsACredential(t *testing.T) {
	t.Parallel()

	refused := fmt.Errorf("withdraw the queued merge of pull request 1: exit code 1: HTTP 403: Resource not accessible by integration: %w", publish.ErrForgeAccessRefused)
	for _, test := range []struct {
		name            string
		withdraw, state error
		want            ownership.PersonOnlyReason
	}{
		{"the withdrawal is refused", refused, nil, ownership.PersonCredential},
		{"the read confirming it is refused", nil, refused, ownership.PersonCredential},
		{"the forge answers without saying", errors.New("HTTP 422: the request cannot be changed"), nil, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f, withdrawer, _ := queuedMergeFixture(t)
			f.worker.Pipeline.Config.Triage.StuckMergeAge = config.Duration(time.Nanosecond)
			withdrawer.answer = func(int) error {
				f.forge.StateErr = test.state
				return test.withdraw
			}
			f.assertStopsRepeat(test.want)
		})
	}

	f, withdrawer, _ := queuedMergeFixture(t)
	f.worker.Pipeline.Config.Triage.StuckMergeAge = config.Duration(time.Nanosecond)
	withdrawer.answer = func(int) error { return orchestratortest.ConnectionReset("disable auto-merge") }
	if pass := f.pass(queueKey); pass.Outcome != runstate.MergeQueuePassWaiting || pass.PersonOnly != "" {
		t.Fatalf("Pass() during an outage = %#v, want it waiting for GitHub", pass)
	}
}
