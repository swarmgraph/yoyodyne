package queuemode

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var (
	observedAt = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	mainQueue  = runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	harness    = Harness{PullRequests: true, CheckConfiguration: "checks-1"}
)

// completeNative is a forge whose queue establishes and enforces everything,
// which no adapter in the repository reports today: it is what qualifying
// looks like, so every shortfall below is one change away from it.
func completeNative(timeout int) publish.QueueCapabilities {
	observed := publish.QueueCapabilities{
		Forge: "test-forge", TargetBranch: "main", ObservedAt: observedAt, Protected: true, ProtectedBy: "ruleset",
		QueueAvailable: true, QueueRequired: true, TimeoutMinutes: timeout,
	}
	for _, requirement := range publish.QueueRequirements() {
		evidence := publish.QueueRequirementEvidence{Requirement: requirement, Established: true, Enforced: true}
		switch requirement {
		case publish.QueueRequiredChecks:
			evidence.BoundTo, evidence.Configuration = publish.BoundToCandidate, "checks-1"
		case publish.QueueIndependentApproval:
			evidence.BoundTo = publish.BoundToCandidate
		}
		observed.Requirements = append(observed.Requirements, evidence)
	}
	return observed
}

func withEvidence(observed publish.QueueCapabilities, requirement publish.QueueRequirement, change func(*publish.QueueRequirementEvidence)) publish.QueueCapabilities {
	observed.Requirements = slices.Clone(observed.Requirements)
	for i := range observed.Requirements {
		if observed.Requirements[i].Requirement == requirement {
			change(&observed.Requirements[i])
		}
	}
	return observed
}

func TestACompleteNativeContractSelectsTheForgesQueueAndRecordsItsActualTimeout(t *testing.T) {
	t.Parallel()

	for _, timeout := range []int{60, 15} {
		selection, err := Select("main", completeNative(timeout), harness, observedAt.Add(time.Minute))
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if selection.Mode != runstate.MergeQueueForge || selection.Hold != nil || len(selection.Evidence.Unmet) != 0 {
			t.Fatalf("Select() = %#v, want the forge's queue with nothing unmet", selection)
		}
		if selection.Evidence.TimeoutMinutes != timeout || !strings.Contains(selection.Evidence.Explanation, "waits "+strconv.Itoa(timeout)+" minutes") {
			t.Fatalf("Select() evidence = %#v, want the forge's own %d-minute timeout recorded", selection.Evidence, timeout)
		}
		if !selection.Evidence.ForgeQueue || !selection.Evidence.TargetProtected || !selection.Evidence.ObservedAt.Equal(observedAt) || selection.Evidence.Forge != "test-forge" {
			t.Fatalf("Select() evidence = %#v, want the observation recorded", selection.Evidence)
		}
	}
}

func TestEachMissingRequirementSelectsTheHarnessQueueAndSaysWhy(t *testing.T) {
	t.Parallel()

	// The target is not protected here, so every shortfall falls back to the
	// harness's queue rather than to a hold.
	open := completeNative(60)
	open.Protected, open.QueueRequired = false, false
	breakages := map[string]func(*publish.QueueRequirementEvidence){
		"not established": func(e *publish.QueueRequirementEvidence) { e.Established = false; e.Missing = "" },
		"not enforced":    func(e *publish.QueueRequirementEvidence) { e.Enforced = false; e.Missing = "" },
		"said why missing": func(e *publish.QueueRequirementEvidence) {
			e.Established = false
			e.Missing = "the forge keeps this to itself"
		},
		"absent": func(e *publish.QueueRequirementEvidence) { e.Requirement = "something-else" },
	}
	for _, requirement := range publish.QueueRequirements() {
		for name, breakage := range breakages {
			observed := withEvidence(open, requirement, breakage)
			selection, err := Select("main", observed, harness, observedAt)
			if err != nil {
				t.Fatalf("%s %s: Select() error = %v", requirement, name, err)
			}
			if selection.Mode != runstate.MergeQueueHarness || selection.Hold != nil {
				t.Fatalf("%s %s: Select() = %#v, want the harness's queue", requirement, name, selection)
			}
			if !reflect.DeepEqual(selection.Evidence.Unmet, []string{string(requirement)}) {
				t.Fatalf("%s %s: unmet = %v, want only %s", requirement, name, selection.Evidence.Unmet, requirement)
			}
			explanation := selection.Evidence.Explanation
			if !strings.HasPrefix(explanation, "The forge's merge queue cannot be used for main: ") || !strings.HasSuffix(explanation, "The harness runs the queue itself.") {
				t.Fatalf("%s %s: explanation %q does not say plainly what native mode cannot establish", requirement, name, explanation)
			}
			if name == "said why missing" && !strings.Contains(explanation, "the forge keeps this to itself") {
				t.Fatalf("%s: explanation %q drops the adapter's reason", requirement, explanation)
			}
			if name != "said why missing" && requirement != publish.QueueTimeoutPolicy && !strings.Contains(explanation, requirement.Says()) {
				t.Fatalf("%s %s: explanation %q does not name %q", requirement, name, explanation, requirement.Says())
			}
		}
	}

	// A requirement the adapter answered twice is not one it established.
	twice := completeNative(60)
	twice.Protected, twice.QueueRequired = false, false
	twice.Requirements = append(twice.Requirements, publish.QueueRequirementEvidence{Requirement: publish.QueueCandidateCommit})
	if selection, err := Select("main", twice, harness, observedAt); err != nil || selection.Mode != runstate.MergeQueueHarness {
		t.Fatalf("Select() with a requirement answered twice = %#v, %v; want the harness's queue", selection, err)
	}
}

func TestChecksGreenOnEveryContributingHeadDoNotQualifyAGroupedCandidate(t *testing.T) {
	t.Parallel()

	// A group of two changes is tested and landed as a commit that is neither
	// change's head. Checks and approval that are green on each pull request's
	// head say nothing about that commit, so they never qualify the forge's
	// queue, whether or not it names the combined commit.
	open := completeNative(60)
	open.Protected, open.QueueRequired = false, false
	headBound := func(e *publish.QueueRequirementEvidence) { e.BoundTo = publish.BoundToPullRequestHead }
	observed := withEvidence(withEvidence(open, publish.QueueRequiredChecks, headBound), publish.QueueIndependentApproval, headBound)
	selection, err := Select("main", observed, harness, observedAt)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Mode != runstate.MergeQueueHarness || !reflect.DeepEqual(selection.Evidence.Unmet, []string{string(publish.QueueRequiredChecks), string(publish.QueueIndependentApproval)}) {
		t.Fatalf("Select() = %#v, want the harness's queue with checks and approval unmet", selection)
	}
	if !strings.Contains(selection.Evidence.Explanation, "about the pull request's head, not the combined commit") {
		t.Fatalf("explanation %q does not say the evidence is about the wrong commit", selection.Evidence.Explanation)
	}

	// And a queue that cannot name the combined commit falls short however its
	// checks are bound.
	unnamed := withEvidence(open, publish.QueueCandidateCommit, func(e *publish.QueueRequirementEvidence) { e.Established = false })
	if selection, err := Select("main", unnamed, harness, observedAt); err != nil || !slices.Contains(selection.Evidence.Unmet, string(publish.QueueCandidateCommit)) {
		t.Fatalf("Select() with no combined commit named = %#v, %v; want it unmet", selection, err)
	}
}

func TestStaleOrMismatchedEvidenceNeverQualifies(t *testing.T) {
	t.Parallel()

	open := completeNative(60)
	open.Protected, open.QueueRequired = false, false
	unmet := map[string]struct {
		observed publish.QueueCapabilities
		harness  Harness
		want     publish.QueueRequirement
	}{
		"checks configured otherwise":              {withEvidence(open, publish.QueueRequiredChecks, func(e *publish.QueueRequirementEvidence) { e.Configuration = "checks-0" }), harness, publish.QueueRequiredChecks},
		"checks with no configuration":             {withEvidence(open, publish.QueueRequiredChecks, func(e *publish.QueueRequirementEvidence) { e.Configuration = "" }), harness, publish.QueueRequiredChecks},
		"a project with no configuration to match": {open, Harness{PullRequests: true}, publish.QueueRequiredChecks},
		"approval of the pull request's head":      {withEvidence(open, publish.QueueIndependentApproval, func(e *publish.QueueRequirementEvidence) { e.BoundTo = publish.BoundToPullRequestHead }), harness, publish.QueueIndependentApproval},
		"approval bound to nothing":                {withEvidence(open, publish.QueueIndependentApproval, func(e *publish.QueueRequirementEvidence) { e.BoundTo = "" }), harness, publish.QueueIndependentApproval},
	}
	for name, tc := range unmet {
		selection, err := Select("main", tc.observed, tc.harness, observedAt)
		if err != nil || selection.Mode != runstate.MergeQueueHarness || !reflect.DeepEqual(selection.Evidence.Unmet, []string{string(tc.want)}) {
			t.Fatalf("%s: Select() = %#v, %v; want the harness's queue with %s unmet", name, selection, err, tc.want)
		}
	}

	// An answer about another branch, or from too long ago, or from no time at
	// all, is no answer: nothing is chosen from it.
	other := completeNative(60)
	other.TargetBranch = "release"
	undated := completeNative(60)
	undated.ObservedAt = time.Time{}
	refused := map[string]struct {
		observed publish.QueueCapabilities
		now      time.Time
	}{
		"another branch":         {other, observedAt},
		"an old answer":          {completeNative(60), observedAt.Add(MaxObservationAge + time.Second)},
		"an answer from later":   {completeNative(60), observedAt.Add(-MaxObservationAge - time.Second)},
		"an answer with no time": {undated, observedAt},
	}
	for name, tc := range refused {
		if selection, err := Select("main", tc.observed, harness, tc.now); err == nil {
			t.Fatalf("%s: Select() = %#v, want it refused", name, selection)
		}
	}
}

func TestTimeoutPolicyIsTheForgesOwnOrUnmet(t *testing.T) {
	t.Parallel()

	for _, timeout := range []int{60, 15, 0} {
		observed := completeNative(timeout)
		observed.Protected, observed.QueueRequired = false, false
		selection, err := Select("main", observed, harness, observedAt)
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if timeout == 0 {
			if selection.Mode != runstate.MergeQueueHarness || !reflect.DeepEqual(selection.Evidence.Unmet, []string{string(publish.QueueTimeoutPolicy)}) {
				t.Fatalf("Select() with no timeout reported = %#v, want the harness's queue with the timeout unmet", selection)
			}
			continue
		}
		if selection.Mode != runstate.MergeQueueForge || selection.Evidence.TimeoutMinutes != timeout {
			t.Fatalf("Select() with a %d-minute timeout = %#v, want it recorded as reported", timeout, selection)
		}
	}
}

func TestAForgeWithoutANativeQueueSelectsTheHarnessQueue(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		observed publish.QueueCapabilities
		says     string
	}{
		"a forge with no queue for the branch": {publish.QueueCapabilities{Forge: "github", TargetBranch: "main", ObservedAt: observedAt, Protected: true}, "the forge has none for it"},
		"no forge at all":                      {publish.QueueCapabilities{TargetBranch: "main", ObservedAt: observedAt}, "no forge that can say"},
	}
	for name, tc := range cases {
		selection, err := Select("main", tc.observed, harness, observedAt)
		if err != nil {
			t.Fatalf("%s: Select() error = %v", name, err)
		}
		if selection.Mode != runstate.MergeQueueHarness || selection.Hold != nil || len(selection.Evidence.Unmet) != len(publish.QueueRequirements()) {
			t.Fatalf("%s: Select() = %#v, want the harness's queue with every requirement unmet", name, selection)
		}
		if !strings.Contains(selection.Evidence.Explanation, tc.says) || selection.Evidence.ForgeQueue {
			t.Fatalf("%s: evidence %#v does not say why", name, selection.Evidence)
		}
	}
}

func TestProtectionNeitherQueueCanSatisfyIsAHoldWithItsOwner(t *testing.T) {
	t.Parallel()

	// A branch that lands only through the forge's queue, whose queue falls
	// short: the harness's queue would land a candidate that is not what the
	// forge lands, and changing the branch's rules is a repository setting.
	required := withEvidence(completeNative(60), publish.QueueIndependentApproval, func(e *publish.QueueRequirementEvidence) {
		e.BoundTo, e.Missing = publish.BoundToPullRequestHead, ""
	})
	selection, err := Select("main", required, harness, observedAt)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	hold := selection.Hold
	if selection.Mode != "" || hold == nil || hold.Mover != ownership.MoverOperator || hold.PersonOnly == nil ||
		hold.PersonOnly.Reason != ownership.PersonRepositorySetting || hold.PersonOnly.Validate() != nil {
		t.Fatalf("Select() = %#v, want a hold the operator lifts by a repository setting", selection)
	}
	for _, want := range []string{"only through the forge's merge queue", "the pull request's head"} {
		if !strings.Contains(hold.Requirement, want) {
			t.Fatalf("hold requirement %q does not say %q", hold.Requirement, want)
		}
	}
	if !strings.Contains(hold.Step, "protection rules") || selection.Evidence.Explanation != hold.Requirement {
		t.Fatalf("hold %#v with evidence %#v does not name the step or record the reason", hold, selection.Evidence)
	}

	// A protected branch the project does not publish into through pull
	// requests: nothing only a person can do lifts that, so it is the
	// development manager's.
	protected := publish.QueueCapabilities{Forge: "github", TargetBranch: "main", ObservedAt: observedAt, Protected: true, ProtectedBy: "branch protection"}
	selection, err = Select("main", protected, Harness{CheckConfiguration: "checks-1"}, observedAt)
	if err != nil || selection.Hold == nil || selection.Hold.Mover != ownership.MoverDevelopmentManager || selection.Hold.PersonOnly != nil {
		t.Fatalf("Select() for a protected branch without pull requests = %#v, %v; want the development manager's hold", selection, err)
	}

	// The same branch with a qualifying queue, or with pull requests, holds
	// nothing.
	if selection, err := Select("main", completeNative(60), harness, observedAt); err != nil || selection.Mode != runstate.MergeQueueForge {
		t.Fatalf("Select() with a qualifying required queue = %#v, %v; want the forge's queue", selection, err)
	}
	if selection, err := Select("main", protected, harness, observedAt); err != nil || selection.Mode != runstate.MergeQueueHarness {
		t.Fatalf("Select() for a protected branch with pull requests = %#v, %v; want the harness's queue", selection, err)
	}
}

// reader is a forge adapter whose answer the test sets, counting how often it
// was asked.
type reader struct {
	answer publish.QueueCapabilities
	err    error
	asked  int
}

func (r *reader) QueueCapabilities(_ context.Context, branch string) (publish.QueueCapabilities, error) {
	r.asked++
	answer := r.answer
	if answer.TargetBranch == "" {
		answer.TargetBranch = branch
	}
	return answer, r.err
}

func noQueue() publish.QueueCapabilities {
	return publish.QueueCapabilities{Forge: "github", ObservedAt: observedAt}
}

func newStore(t *testing.T, root string) *runstate.MergeQueueStore {
	t.Helper()
	store, err := runstate.NewMergeQueueStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMergeQueueStore() error = %v", err)
	}
	return store
}

func admission(t *testing.T) runstate.MergeQueueAdmission {
	t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	return runstate.MergeQueueAdmission{
		Key: mainQueue, WorkItemID: "yoyodyne-test", WorkItemTitle: "Select merge-queue mode", RunID: runID,
		Publication: "https://example.test/pull/1", ApprovedHead: strings.Repeat("a", 40), IntegrationPolicy: "pull-request",
		At: observedAt.Add(time.Minute),
	}
}

func clock() time.Time { return observedAt.Add(time.Minute) }

func TestAdmissionRecordsTheChosenModeAndItsEvidence(t *testing.T) {
	t.Parallel()

	store := newStore(t, t.TempDir())
	asked := admission(t)
	// Whatever mode the caller names is the selector's to choose.
	asked.Mode = runstate.MergeQueueForge
	admitter := Admitter{Queue: store, Forge: &reader{answer: noQueue()}, Harness: harness, Now: clock}
	admitted, err := admitter.Admit(context.Background(), asked)
	if err != nil || !admitted.New || admitted.Hold != nil {
		t.Fatalf("Admit() = %#v, %v; want a new entry", admitted, err)
	}
	recorded, err := store.Entries(mainQueue)
	if err != nil || len(recorded) != 1 || !reflect.DeepEqual(recorded[0], admitted.Entry) {
		t.Fatalf("Entries() = %#v, %v; want the admitted entry", recorded, err)
	}
	evidence := recorded[0].ModeEvidence
	if recorded[0].Mode != runstate.MergeQueueHarness || evidence.Forge != "github" || !evidence.ObservedAt.Equal(observedAt) ||
		len(evidence.Unmet) != len(publish.QueueRequirements()) || !strings.Contains(evidence.Explanation, "the forge has none for it") {
		t.Fatalf("recorded entry %#v, want the harness's queue with the evidence it was chosen on", recorded[0])
	}

	// A forge whose queue qualifies is recorded as such.
	native := Admitter{Queue: store, Forge: &reader{answer: completeNative(15)}, Harness: harness, Now: clock}
	admitted, err = native.Admit(context.Background(), admission(t))
	if err != nil || admitted.Entry.Mode != runstate.MergeQueueForge || admitted.Entry.ModeEvidence.TimeoutMinutes != 15 || len(admitted.Entry.ModeEvidence.Unmet) != 0 {
		t.Fatalf("Admit() with a qualifying queue = %#v, %v; want the forge's queue recorded with its timeout", admitted, err)
	}
}

func TestRepeatedAdmissionAndRestartKeepTheRecordedModeWhateverTheForgeNowSays(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	asked := admission(t)
	first, err := Admitter{Queue: newStore(t, root), Forge: &reader{answer: noQueue()}, Harness: harness, Now: clock}.Admit(context.Background(), asked)
	if err != nil || first.Entry.Mode != runstate.MergeQueueHarness {
		t.Fatalf("Admit() = %#v, %v", first, err)
	}

	// After a restart the forge's queue qualifies. The entry is not moved to
	// it, gains no way to land it did not have, and the forge is not asked.
	changed := &reader{answer: completeNative(60)}
	again, err := Admitter{Queue: newStore(t, root), Forge: changed, Harness: harness, Now: clock}.Admit(context.Background(), asked)
	if err != nil || again.New || !reflect.DeepEqual(again.Entry, first.Entry) || changed.asked != 0 {
		t.Fatalf("Admit() after a restart = %#v, %v (forge asked %d times); want the entry as admitted, the forge unasked", again, err, changed.asked)
	}
	// The same run at another head is still refused, not answered with the
	// entry it does not match.
	moved := asked
	moved.ApprovedHead = strings.Repeat("b", 40)
	var conflict runstate.MergeQueueConflictError
	if _, err := (Admitter{Queue: newStore(t, root), Forge: changed, Harness: harness, Now: clock}).Admit(context.Background(), moved); !errors.As(err, &conflict) {
		t.Fatalf("Admit() at another head error = %v, want the conflict", err)
	}
	recorded, err := newStore(t, root).Entries(mainQueue)
	if err != nil || len(recorded) != 1 || !reflect.DeepEqual(recorded[0], first.Entry) {
		t.Fatalf("Entries() = %#v, %v; want the one entry unchanged", recorded, err)
	}
}

// racingQueue admits the same run in another mode the moment it is first
// asked to admit, as another process would between the read and the write.
type racingQueue struct {
	*runstate.MergeQueueStore
	raced bool
}

func (q *racingQueue) Admit(ctx context.Context, asked runstate.MergeQueueAdmission) (runstate.MergeQueueEntry, bool, error) {
	if !q.raced {
		q.raced = true
		other := asked
		other.Mode = runstate.MergeQueueForge
		other.ModeEvidence = runstate.MergeQueueModeEvidence{Forge: "test-forge", ObservedAt: observedAt, ForgeQueue: true, Explanation: "the forge's queue met every requirement then"}
		if _, _, err := q.MergeQueueStore.Admit(ctx, other); err != nil {
			return runstate.MergeQueueEntry{}, false, err
		}
	}
	return q.MergeQueueStore.Admit(ctx, asked)
}

func TestAnAdmissionThatLostARaceIsTheEntryThatWonIt(t *testing.T) {
	t.Parallel()

	queue := &racingQueue{MergeQueueStore: newStore(t, t.TempDir())}
	admitted, err := Admitter{Queue: queue, Forge: &reader{answer: noQueue()}, Harness: harness, Now: clock}.Admit(context.Background(), admission(t))
	if err != nil || admitted.New || admitted.Entry.Mode != runstate.MergeQueueForge {
		t.Fatalf("Admit() = %#v, %v; want the entry the race admitted, in its mode", admitted, err)
	}
	if recorded, err := queue.Entries(mainQueue); err != nil || len(recorded) != 1 {
		t.Fatalf("Entries() = %#v, %v; want one entry", recorded, err)
	}
}

// uncertainQueue fails its first admission as a save that may or may not have
// landed, landing it or not, and records the order the queue was read and
// written in.
type uncertainQueue struct {
	*runstate.MergeQueueStore
	land   bool
	failed bool
	calls  []string
}

func (q *uncertainQueue) Admit(ctx context.Context, asked runstate.MergeQueueAdmission) (runstate.MergeQueueEntry, bool, error) {
	q.calls = append(q.calls, "admit")
	if !q.failed {
		q.failed = true
		if q.land {
			if _, _, err := q.MergeQueueStore.Admit(ctx, asked); err != nil {
				return runstate.MergeQueueEntry{}, false, err
			}
		}
		return runstate.MergeQueueEntry{}, false, runstate.ErrMergeQueueSaveUncertain
	}
	return q.MergeQueueStore.Admit(ctx, asked)
}

func (q *uncertainQueue) Entries(key runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error) {
	q.calls = append(q.calls, "read")
	return q.MergeQueueStore.Entries(key)
}

func (q *uncertainQueue) Standing(key runstate.MergeQueueKey, runID string) (runstate.MergeQueueEntry, bool, error) {
	q.calls = append(q.calls, "read")
	return q.MergeQueueStore.Standing(key, runID)
}

func TestAnUncertainSaveIsReadBackBeforeAnotherAdmissionIsWritten(t *testing.T) {
	t.Parallel()

	for _, land := range []bool{true, false} {
		root := t.TempDir()
		queue := &uncertainQueue{MergeQueueStore: newStore(t, root), land: land}
		asked := admission(t)
		if _, err := (Admitter{Queue: queue, Forge: &reader{answer: noQueue()}, Harness: harness, Now: clock}).Admit(context.Background(), asked); !errors.Is(err, runstate.ErrMergeQueueSaveUncertain) {
			t.Fatalf("land=%t: Admit() error = %v, want the uncertain save reported", land, err)
		}

		// The retry, after a restart and against a forge that now answers
		// otherwise, reads the queue before it writes.
		queue.calls = nil
		changed := &reader{answer: completeNative(60)}
		retried, err := Admitter{Queue: queue, Forge: changed, Harness: harness, Now: clock}.Admit(context.Background(), asked)
		if err != nil || len(queue.calls) == 0 || queue.calls[0] != "read" {
			t.Fatalf("land=%t: retried Admit() = %#v, %v with calls %v; want the queue read first", land, retried, err, queue.calls)
		}
		if land && (retried.New || retried.Entry.Mode != runstate.MergeQueueHarness || changed.asked != 0) {
			t.Fatalf("retried Admit() after a save that landed = %#v (forge asked %d times); want the landed entry in its recorded mode", retried, changed.asked)
		}
		if !land && (!retried.New || retried.Entry.Mode != runstate.MergeQueueForge || retried.Entry.Order != 1) {
			t.Fatalf("retried Admit() after a save that did not land = %#v; want it admitted now, first, in the mode chosen now", retried)
		}
		recorded, err := newStore(t, root).Entries(mainQueue)
		if err != nil || len(recorded) != 1 || !reflect.DeepEqual(recorded[0], retried.Entry) {
			t.Fatalf("land=%t: Entries() = %#v, %v; want exactly the one entry", land, recorded, err)
		}
	}
}

func TestNothingIsAdmittedOnAHoldOrAnUnreadableForge(t *testing.T) {
	t.Parallel()

	store := newStore(t, t.TempDir())
	held := withEvidence(completeNative(60), publish.QueueCandidateCommit, func(e *publish.QueueRequirementEvidence) { e.Established = false })
	admitted, err := Admitter{Queue: store, Forge: &reader{answer: held}, Harness: harness, Now: clock}.Admit(context.Background(), admission(t))
	if err != nil || admitted.Hold == nil || admitted.Hold.Mover != ownership.MoverOperator || admitted.Entry.RunID != "" || admitted.Evidence.Explanation == "" {
		t.Fatalf("Admit() on a branch neither queue can land into = %#v, %v; want a hold and no entry", admitted, err)
	}

	unreadable := &reader{err: errors.New("gh: Server Error (HTTP 502)")}
	if _, err := (Admitter{Queue: store, Forge: unreadable, Harness: harness, Now: clock}).Admit(context.Background(), admission(t)); err == nil || !strings.Contains(err.Error(), "HTTP 502") || !strings.Contains(err.Error(), "nothing was admitted") {
		t.Fatalf("Admit() against a forge that did not answer error = %v, want it refused naming the forge's answer", err)
	}
	stale := noQueue()
	stale.ObservedAt = observedAt.Add(-time.Hour)
	if _, err := (Admitter{Queue: store, Forge: &reader{answer: stale}, Harness: harness, Now: clock}).Admit(context.Background(), admission(t)); err == nil {
		t.Fatal("Admit() chose a mode from an hour-old answer")
	}
	if recorded, err := store.Entries(mainQueue); err != nil || len(recorded) != 0 {
		t.Fatalf("Entries() = %#v, %v; want nothing admitted", recorded, err)
	}

	// A project with no forge adapter is admitted to the harness's queue.
	admitted, err = Admitter{Queue: store, Harness: harness, Now: clock}.Admit(context.Background(), admission(t))
	if err != nil || admitted.Entry.Mode != runstate.MergeQueueHarness || admitted.Entry.ModeEvidence.Forge != "" {
		t.Fatalf("Admit() with no forge = %#v, %v; want the harness's queue", admitted, err)
	}
}
