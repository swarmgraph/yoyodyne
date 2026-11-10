package orchestrator

// Pushing a merge queue candidate: a push that ends before the remote answers
// is looked at and asked for again rather than kept as a refusal, a push the
// remote refuses hands back only its own entry so the queue goes on, an entry
// an earlier build stopped on that way is taken up by the next pass, and a
// person can take up again or hand back an entry the queue stopped on.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// pushingLander is the fixture's lander with the candidate push replaced by
// what a test says, once per call; nil pushes for real.
type pushingLander struct {
	*stoppingLander
	mu     sync.Mutex
	pushes int
	push   func(ctx context.Context, branch, candidate, expected string) error
}

func (l *pushingLander) PublishQueueCandidate(ctx context.Context, branch, candidate, expected string) error {
	l.mu.Lock()
	push := l.push
	if expected != candidate {
		l.pushes++
	}
	l.mu.Unlock()
	if push != nil {
		return push(ctx, branch, candidate, expected)
	}
	return l.stoppingLander.PublishQueueCandidate(ctx, branch, candidate, expected)
}

func (l *pushingLander) set(push func(ctx context.Context, branch, candidate, expected string) error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.push = push
}

func (l *pushingLander) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pushes
}

func pushingFixture(t *testing.T) (*promotionFixture, *pushingLander) {
	t.Helper()
	f := newPromotionFixture(t, landProtected)
	lander := &pushingLander{stoppingLander: f.lander}
	f.promoter.Lander = lander
	return f, lander
}

// A push cut off by the session stopping — a restart onto a new build cancels
// the pass mid-push — is left for the next process to look at, whether or not
// it reached the remote, and the change lands with the candidate pushed no
// more than it needed to be.
func TestAPushCutOffByARestartIsAskedForAgainAndNeverKeptAsARefusal(t *testing.T) {
	t.Parallel()

	for _, arrived := range []bool{false, true} {
		t.Run(fmt.Sprintf("arrived %t", arrived), func(t *testing.T) {
			t.Parallel()
			f, lander := pushingFixture(t)
			generation := f.verify()
			lander.set(func(ctx context.Context, branch, candidate, expected string) error {
				if arrived {
					if err := f.lander.PublishQueueCandidate(ctx, branch, candidate, expected); err != nil {
						return err
					}
				}
				// The session stops: every command it runs from here is cut off.
				stopped, stop := context.WithCancel(ctx)
				stop()
				return f.lander.Manager.PublishQueueCandidate(stopped, branch, candidate, expected)
			})
			cut, err := f.promote()
			if err != nil || cut.Waiting == "" || cut.Unresolved != "" || cut.HandedBack != nil {
				t.Fatalf("Promote() with the push cut off = %#v, %v; want it waiting for the next pass", cut, err)
			}
			attempt, _ := f.landing().Current()
			if record, unsettled := attempt.Unsettled(); !unsettled || record.Mutation != runstate.MergeQueuePushCandidate {
				t.Fatalf("attempt = %#v, want the push left with no answer recorded rather than settled as a refusal", attempt)
			}

			lander.set(nil)
			landed, err := f.promote()
			if err != nil || !landed.Landed || !landed.Completed {
				t.Fatalf("Promote() after the restart = %#v, %v; want the push looked at and the change landed", landed, err)
			}
			f.assertLandedOnce(generation)
			// A push that reached the remote is recorded made rather than pushed
			// again; one that did not is asked for once more. The cut-off call is
			// the first of them.
			want := 2
			if arrived {
				want = 1
			}
			if got := lander.count(); got != want {
				t.Fatalf("the candidate push was asked for %d times, want %d", got, want)
			}
		})
	}
}

// A push that exits with no message from the remote — the exit code -1 a
// killed git reports, or a non-zero exit with nothing printed — is not a
// refusal either.
func TestAPushEndingWithNoMessageFromTheRemoteIsNotARefusal(t *testing.T) {
	t.Parallel()

	f, lander := pushingFixture(t)
	generation := f.verify()
	lander.set(func(_ context.Context, branch, candidate, expected string) error {
		return fmt.Errorf("%w: publish %s on %s over %q ended failed with exit code -1 and no answer from the remote: nothing printed",
			gitworktree.ErrRemotePushInterrupted, candidate, branch, expected)
	})
	waiting, err := f.promote()
	if err != nil || waiting.Waiting == "" || waiting.Unresolved != "" {
		t.Fatalf("Promote() = %#v, %v; want it waiting for the next pass", waiting, err)
	}
	lander.set(nil)
	if landed, err := f.promote(); err != nil || !landed.Landed {
		t.Fatalf("Promote() on the next pass = %#v, %v; want the change landed", landed, err)
	}
	f.assertLandedOnce(generation)
}

// A push the remote refuses hands back only its own entry, with the refusal in
// the remote's words, and the queue goes on to land the entry behind it.
func TestAPushTheRemoteRefusesHandsBackOnlyItsEntry(t *testing.T) {
	t.Parallel()

	f, lander := pushingFixture(t)
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	// The entry behind carries a change of its own that passes the project's
	// check without the refused entry's.
	behind := f.admitAnother(queueKey, "behind", "feature.txt", "implemented behind\n")
	refused := runstate.MergeQueueCandidateBranch(f.entry.EntryID)
	lander.set(func(ctx context.Context, branch, candidate, expected string) error {
		if branch == refused {
			return fmt.Errorf("%w: publish %s on %s over %q failed with exit code 1: ! [remote rejected] (protected branch hook declined)",
				gitworktree.ErrRemotePushRejected, candidate, branch, expected)
		}
		return f.lander.PublishQueueCandidate(ctx, branch, candidate, expected)
	})

	pass := f.pass(queueKey)
	if pass.Outcome == runstate.MergeQueuePassStopped {
		t.Fatalf("Pass() = %#v, want the queue to go on past the refused entry", pass)
	}
	landing := f.landing()
	handback := landing.Handback
	if handback == nil || handback.Continuation != runstate.MergeQueueReleased || handback.Mover != ownership.MoverDevelopmentManager ||
		!strings.Contains(handback.Reason, "protected branch hook declined") || !strings.Contains(handback.Reason, "the remote refused the push") {
		t.Fatalf("handback = %#v, want the entry handed back for the development manager with the refusal in plain words", handback)
	}
	if repair.count() != 1 || repair.entity != f.entry.RunID {
		t.Fatalf("repair given %#v, want the refused entry's run given it once", repair.given)
	}
	behindLanding, _, err := f.queue.Landing(queueKey, behind.EntryID)
	if err != nil || behindLanding.Completion == nil || !behindLanding.Completion.Whole() {
		t.Fatalf("the entry behind = %#v, %v; want it landed in the same pass", behindLanding, err)
	}
}

// An entry an earlier build stopped on — a push cut off by a restart and
// recorded as the remote refusing it — is taken up by the next pass with no
// record edited by hand.
func TestAPushAnEarlierBuildRecordedAsRefusedIsTakenUpByTheNextPass(t *testing.T) {
	t.Parallel()

	f, lander := pushingFixture(t)
	generation := f.verify()
	f.lander.stop[runstate.MergeQueuePushCandidate] = "before"
	if _, err := f.promote(); !errors.Is(err, errStopped) {
		t.Fatalf("Promote() error = %v, want the stop", err)
	}
	// What the earlier build wrote: the push settled not made, as a refusal,
	// with exit code -1 and nothing from the remote.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	worker, held, err := f.landings.LeaseWorker(ctx, queueKey)
	if err != nil || !held {
		t.Fatalf("LeaseWorker() = %t, %v", held, err)
	}
	landing := f.landing()
	attempt := &landing.Attempts[len(landing.Attempts)-1]
	attempt.Mutations[len(attempt.Mutations)-1].Settled = &runstate.MergeQueueSettlement{
		Result: runstate.MergeQueueMutationNotMade, At: time.Now().UTC(),
		Detail: "remote rejected the push: publish " + generation.Candidate + " on " + attempt.CandidateBranch + ` over "" failed with exit code -1: `,
	}
	if err := f.landings.RecordLanding(worker, queueKey, landing); err != nil {
		t.Fatal(err)
	}
	_ = worker.Release()

	landed, err := f.promote()
	if err != nil || !landed.Landed || !landed.Completed {
		t.Fatalf("Promote() = %#v, %v; want the push asked for again and the change landed", landed, err)
	}
	recorded := f.assertLandedOnce(generation)
	if len(recorded.Attempts) != 2 || recorded.Attempts[0].SetAside == nil || !strings.Contains(recorded.Attempts[0].SetAside.Reason, "asked for again") {
		t.Fatalf("attempts = %#v, want the stopped one set aside saying it is asked for again", recorded.Attempts)
	}
	// The first call is the one the earlier process stopped before it pushed.
	if lander.count() != 2 {
		t.Fatalf("the candidate push was asked for %d times, want the stopped call and one push", lander.count())
	}
}

// queueStoppedOnMerge leaves an entry the queue stops on: the merge of its pull request
// was asked for, the answer was lost, and the forge shows it neither merged nor
// queued, which the promotion keeps as it is rather than asking again.
func queueStoppedOnMerge(t *testing.T) (*promotionFixture, runstate.MergeQueueGeneration) {
	t.Helper()
	f := newPromotionFixture(t, landProtected)
	generation := f.verify()
	f.forge.stop[runstate.MergeQueueRequestMerge] = "before"
	if _, err := f.promote(); !errors.Is(err, errStopped) {
		t.Fatalf("Promote() error = %v, want the stop", err)
	}
	stuck, err := f.promote()
	if err != nil || stuck.Unresolved == "" {
		t.Fatalf("Promote() = %#v, %v; want the queue stopped on the entry", stuck, err)
	}
	return f, generation
}

func TestAPersonTakesUpAgainAnEntryTheQueueStoppedOn(t *testing.T) {
	t.Parallel()

	f, generation := queueStoppedOnMerge(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := f.promoter.Retry(ctx, queueKey, f.entry.EntryID, ""); err == nil {
		t.Fatal("Retry() with no reason = nil, want it refused")
	}
	retried, err := f.promoter.Retry(ctx, queueKey, f.entry.EntryID, "the forge has no merge for it")
	if err != nil || retried.Attempt.SetAside == nil || !strings.Contains(retried.Attempt.SetAside.Reason, "the forge has no merge for it") {
		t.Fatalf("Retry() = %#v, %v; want the stopped attempt set aside with the reason", retried, err)
	}
	landed, err := f.promote()
	if err != nil || !landed.Landed || !landed.Completed {
		t.Fatalf("Promote() after the retry = %#v, %v; want the same candidate landed", landed, err)
	}
	f.assertLandedOnce(generation)

	var refusal MergeQueueEntryRefusal
	if _, err := f.promoter.Retry(ctx, queueKey, f.entry.EntryID, "again"); !errors.As(err, &refusal) {
		t.Fatalf("Retry() of a landed entry error = %v, want it refused", err)
	}
}

func TestAPersonHandsBackAnEntryTheQueueStoppedOn(t *testing.T) {
	t.Parallel()

	f, _ := queueStoppedOnMerge(t)
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	handed, err := f.promoter.HandBack(ctx, queueKey, f.entry.EntryID, "the development manager wants it rebuilt")
	if err != nil || handed.HandedBack == nil || !strings.Contains(handed.HandedBack.Reason, "the development manager wants it rebuilt") {
		t.Fatalf("HandBack() = %#v, %v; want the entry handed back with the reason", handed, err)
	}
	if repair.count() != 1 || f.landing().Handback == nil {
		t.Fatalf("repair given %#v, want the entry's run given it once", repair.given)
	}
	if next, err := f.promote(); err != nil || next.Entry.EntryID != "" {
		t.Fatalf("Promote() after the hand-back = %#v, %v; want nothing left in the queue", next, err)
	}
	var refusal MergeQueueEntryRefusal
	if _, err := f.promoter.HandBack(ctx, queueKey, f.entry.EntryID, "again"); !errors.As(err, &refusal) {
		t.Fatalf("HandBack() a second time error = %v, want it refused", err)
	}
}
