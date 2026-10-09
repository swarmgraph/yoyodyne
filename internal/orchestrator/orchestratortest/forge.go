package orchestratortest

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// Forge is the harness's forge adapter with the CLI taken out. It issues
// one pull request per branch, which is what lets a test tell an updated
// request from a second one, and it performs the merge itself the way the forge
// does: by moving the target branch on the remote, without a push.
type Forge struct {
	ReportedAvailability publish.Availability
	// ReportAvailability makes the zero availability meaningful, so a test can
	// express "the CLI is missing" rather than getting the usable default.
	ReportAvailability bool
	Opened             []publish.Request
	Number             int
	Merged             bool
	StateErr           error
	// OnEnsure runs when the pull request is opened, which is the moment after
	// the branch is published and before anything is promoted. It is how a test
	// expresses the world moving underneath a run.
	OnEnsure func()
	// Remote is the bare repository this forge merges into. A forge with none
	// records the merge and touches nothing, which is what an operator sees when
	// a forge reports a merge the remote does not show.
	Remote string
	Merges []publish.MergeRequest
	// MergeErr is what the forge answers instead of merging, which is how a
	// refusal by a protected branch is expressed.
	MergeErr error
	// QueueMerge makes the forge queue the merge instead of performing it, which
	// is what a base branch with required checks produces: the request is
	// accepted, nothing moves yet, and the forge merges minutes later. Queued is
	// the merge it is holding.
	QueueMerge bool
	Queued     bool
	// ReplayMerge makes the forge rewrite what it merges instead of merging it,
	// which is what GitHub's rebase and squash methods do: the base ends up with
	// a fresh commit carrying the same content, and the reviewed commit itself
	// never arrives.
	ReplayMerge bool
	// OpenReplies is how many times State reports the pull request still open
	// before reporting it merged, which is the forge's own record of a request
	// lagging the merge it just performed.
	OpenReplies int
	StateCalls  int
	// HeadCommit is the commit State reports the request carrying, where a test
	// needs the forge to say. A forge that says nothing leaves the reader to the
	// commit the harness itself pushed, which is what most tests want.
	HeadCommit string
	// EnsureResets and MergeResets are how many times the connection carrying
	// that call drops before it goes through. They are the failure that killed
	// four runs on 2026-09-03: nothing about the request reached the forge, so
	// the class says the next attempt may well succeed.
	EnsureResets int
	MergeResets  int
	// AfterMergeReset runs when a merge attempt is dropped, which is the moment
	// the run then spends waiting before it asks again. It is how a test
	// expresses the world moving during that wait.
	AfterMergeReset func()
	// TargetProtection is what the forge says protects the target branch, and
	// ProtectionErr is a forge that could not be asked. ProtectionAsked is every
	// branch it was asked about.
	TargetProtection publish.BranchProtection
	ProtectionErr    error
	ProtectionAsked  []string
	// OnMerge runs when the forge is asked to merge, before it answers. It is
	// how a test reads where the local target stood at that moment.
	OnMerge func()
	// Closable is the pull requests this forge holds beyond the one it issues
	// for the run, keyed by the branch that carries each: an earlier run's
	// publication, which is what a superseded one is. Closed is what it was asked
	// to close, and CloseErr is what it answers instead of closing.
	Closable map[string]publish.PullRequest
	Closed   []publish.CloseRequest
	CloseErr error
	// Removal is the forge's account of a queued merge it stopped holding, and
	// RemovalErr is a forge that could not be asked for it.
	Removal    publish.QueueRemoval
	RemovalErr error
}

// ConnectionReset is what the transport writes when it drops a request, in the
// words git and gh actually print. A test states the words rather than a class,
// because words are what the harness reads.
func ConnectionReset(what string) error {
	return fmt.Errorf("%s: OpenSSL SSL_read: Connection reset by peer, errno 54", what)
}

func (f *Forge) Availability(context.Context) (publish.Availability, error) {
	if f.ReportAvailability {
		return f.ReportedAvailability, nil
	}
	return publish.Availability{Installed: true, Authenticated: true}, nil
}

func (f *Forge) Ensure(_ context.Context, request publish.Request) (publish.PullRequest, error) {
	if f.EnsureResets > 0 {
		f.EnsureResets--
		return publish.PullRequest{}, ConnectionReset("open pull request for " + request.Head)
	}
	f.Opened = append(f.Opened, request)
	if f.OnEnsure != nil {
		f.OnEnsure()
	}
	if f.Number == 0 {
		f.Number = 1
	}
	return publish.PullRequest{Number: f.Number, URL: fmt.Sprintf("https://example.invalid/pull/%d", f.Number), State: "OPEN"}, nil
}

// Merge is the forge merging the pull request, which is what moves the remote
// target branch now. The update is written inside the bare remote rather than
// pushed into it, so a branch that refuses direct pushes is merged into exactly
// as a real forge merges into one, and it is a real merge commit: a fresh
// commit whose first parent is the base, whose second parent is the published
// head, and whose tree is the head's. Modelling that rather than moving the
// base ref onto the head is the difference between exercising the harness and
// exercising an assumption — no forge merge method leaves the base at the
// commit the harness promoted.
func (f *Forge) Merge(_ context.Context, request publish.MergeRequest) (publish.MergeResult, error) {
	if f.MergeResets > 0 {
		f.MergeResets--
		if f.AfterMergeReset != nil {
			f.AfterMergeReset()
		}
		return publish.MergeResult{}, ConnectionReset(fmt.Sprintf("merge pull request %d", request.Number))
	}
	if f.OnMerge != nil {
		f.OnMerge()
	}
	if f.MergeErr != nil {
		return publish.MergeResult{}, f.MergeErr
	}
	f.Merges = append(f.Merges, request)
	// A queued merge accepts the request and moves nothing: what the forge does
	// with it happens after the run that asked for it has finished.
	if f.QueueMerge {
		f.Queued = true
		return publish.MergeResult{Queued: true}, nil
	}
	if f.Remote != "" {
		if err := f.MergeIntoRemote(f.Opened[len(f.Opened)-1].Base, request.HeadCommit); err != nil {
			return publish.MergeResult{}, err
		}
	}
	f.Merged = true
	return publish.MergeResult{}, nil
}

// PerformQueuedMerge is the forge merging a request it queued, which is what
// happens once the base branch's required checks pass — minutes after the run
// that asked for it ended.
func (f *Forge) PerformQueuedMerge(t *testing.T) {
	t.Helper()
	if !f.Queued {
		t.Fatal("no merge is queued with the forge")
	}
	if err := f.MergeIntoRemote(f.Opened[len(f.Opened)-1].Base, f.Merges[len(f.Merges)-1].HeadCommit); err != nil {
		t.Fatalf("perform the queued merge: %v", err)
	}
	f.Queued = false
	f.Merged = true
}

// DropQueuedMerge is the forge giving up on a merge it queued, which is what a
// required check that failed leaves behind: an open request with nothing
// waiting to merge it.
func (f *Forge) DropQueuedMerge() {
	f.Queued = false
}

// QueueRemoval answers with the account the test gave the forge.
func (f *Forge) QueueRemoval(_ context.Context, number int) (publish.QueueRemoval, error) {
	if f.RemovalErr != nil {
		return publish.QueueRemoval{}, f.RemovalErr
	}
	removal := f.Removal
	removal.Number = number
	return removal, nil
}

// MergeIntoRemote writes the forge's own merge of a request into the bare
// remote. A replaying forge — GitHub's rebase and squash methods — is modelled
// by leaving the published head out of the new commit's parents, so the commit
// that was reviewed never reaches the base at all.
func (f *Forge) MergeIntoRemote(base, head string) error {
	tip, err := f.Git("rev-parse", "refs/heads/"+base)
	if err != nil {
		return err
	}
	tree, err := f.Git("rev-parse", head+"^{tree}")
	if err != nil {
		return err
	}
	arguments := []string{
		"-c", "user.name=Forge",
		"-c", "user.email=forge@example.invalid",
		"commit-tree", tree, "-p", tip,
	}
	if !f.ReplayMerge {
		arguments = append(arguments, "-p", head)
	}
	merged, err := f.Git(append(arguments, "-m", fmt.Sprintf("Merge pull request #%d", f.Number))...)
	if err != nil {
		return err
	}
	_, err = f.Git("update-ref", "refs/heads/"+base, merged, tip)
	return err
}

func (f *Forge) Git(arguments ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", f.Remote}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v in the forge: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

// State reports merged once the forge has merged the request — after
// OpenReplies further answers of "still open", which is the forge's own record
// lagging the merge it performed rather than the merge being unfinished.
func (f *Forge) State(context.Context, string) (publish.PullRequest, error) {
	if f.StateErr != nil {
		return publish.PullRequest{}, f.StateErr
	}
	f.StateCalls++
	url := fmt.Sprintf("https://example.invalid/pull/%d", f.Number)
	if !f.Merged || f.StateCalls <= f.OpenReplies {
		return publish.PullRequest{Number: f.Number, URL: url, State: "OPEN", AutoMerge: f.Queued, HeadCommit: f.HeadCommit}, nil
	}
	return publish.PullRequest{Number: f.Number, URL: url, State: "MERGED", Merged: true, HeadCommit: f.HeadCommit}, nil
}

// Close retires a request the forge holds beyond the run's own, the way the
// adapter does: an open one is closed once and a closed one is reported as it
// stands, so a sweep that repeats leaves one comment rather than one per pass.
func (f *Forge) Close(_ context.Context, request publish.CloseRequest) (publish.Closure, error) {
	if f.CloseErr != nil {
		return publish.Closure{}, f.CloseErr
	}
	existing, known := f.Closable[request.Head]
	if !known {
		return publish.Closure{}, fmt.Errorf("no pull request exists for branch %s", request.Head)
	}
	if existing.Merged || !strings.EqualFold(existing.State, "OPEN") {
		return publish.Closure{State: existing.State, Merged: existing.Merged}, nil
	}
	f.Closed = append(f.Closed, request)
	existing.State = "CLOSED"
	f.Closable[request.Head] = existing
	return publish.Closure{Closed: true, State: "CLOSED"}, nil
}

// Hold gives the forge a request on a branch some earlier run published, which
// is what a superseded publication looks like from here. A test states the
// request's state, so one a person already closed is expressed the same way.
func (f *Forge) Hold(branch string, request publish.PullRequest) {
	if f.Closable == nil {
		f.Closable = map[string]publish.PullRequest{}
	}
	f.Closable[branch] = request
}

// ClosedRequests is every close this forge performed, comments and all.
func (f *Forge) ClosedRequests() []publish.CloseRequest { return f.Closed }

// Protection answers what the forge says about the target branch. A forge that
// was told nothing reports it unprotected, which is the arrangement every test
// written before protection was asked about assumed.
func (f *Forge) Protection(_ context.Context, branch string) (publish.BranchProtection, error) {
	f.ProtectionAsked = append(f.ProtectionAsked, branch)
	if f.ProtectionErr != nil {
		return publish.BranchProtection{}, f.ProtectionErr
	}
	return f.TargetProtection, nil
}

// OpenedRequests is every pull request this forge was asked to open, in order.
func (f *Forge) OpenedRequests() []publish.Request {
	return f.Opened
}

// MergeRequests is every merge this forge accepted, in order.
func (f *Forge) MergeRequests() []publish.MergeRequest {
	return f.Merges
}

// HoldsQueuedMerge says whether the forge is holding a queued merge.
func (f *Forge) HoldsQueuedMerge() bool {
	return f.Queued
}

// HoldQueuedMerge is the forge holding a merge nobody asked this process for,
// which is what a request queued before a process died leaves behind.
func (f *Forge) HoldQueuedMerge() {
	f.Queued = true
}

// ForgetMerges is the forge losing every merge it was asked for, queued or not.
func (f *Forge) ForgetMerges() {
	f.Queued = false
	f.Merges = nil
}

// MergeByHand is somebody merging head into base at the forge outside any
// request the harness made, which leaves the pull request merged and nothing
// queued.
func (f *Forge) MergeByHand(base, head string) error {
	if err := f.MergeIntoRemote(base, head); err != nil {
		return err
	}
	f.Queued, f.Merged = false, true
	return nil
}

// SetQueueMerge, SetReplayMerge, SetMergeErr, SetHeadCommit,
// SetTargetProtection, and SetOnMerge arrange the forge after it was built, for
// a fixture that builds it before the test says how it behaves.
func (f *Forge) SetQueueMerge(queue bool) { f.QueueMerge = queue }

func (f *Forge) SetReplayMerge(replay bool) { f.ReplayMerge = replay }

func (f *Forge) SetMergeErr(err error) { f.MergeErr = err }

func (f *Forge) SetHeadCommit(commit string) { f.HeadCommit = commit }

func (f *Forge) SetTargetProtection(protection publish.BranchProtection) {
	f.TargetProtection = protection
}

func (f *Forge) SetOnMerge(onMerge func()) { f.OnMerge = onMerge }
