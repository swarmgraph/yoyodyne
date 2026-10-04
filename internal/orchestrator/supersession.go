package orchestrator

// Retiring a publication whose work landed by another vehicle.
//
// A run branch carries the run that published it, so an item attempted again
// publishes a new branch and opens a new pull request rather than reusing the
// first run's. Nothing revisited the first one: it sat open with a green build
// and no queued merge, indistinguishable from pending work until somebody asked
// why. That happened to the publications of runs killed and relaunched, and to
// the loser of a duplicate selection, and it took a person going looking each
// time.
//
// Closing one is not a decision. The work the request was opened for is on the
// target branch by another vehicle — the harness's own promotion, recorded — so
// a request that will never merge is left open by omission rather than on
// purpose, and what makes the forge's open list honest is closing it with the
// vehicle named. Nothing here judges the change, reopens anything, or touches a
// request that merged.
//
// Two places know it and both retire the publication through here. The
// convergence sweep finds every such publication whatever left it behind, which
// is what covers the ones nobody was present for; the re-run knows at the moment
// it retires what the stopped run held, which is what keeps the list honest
// between sweeps.
//
// What earns a close is the harness's own promotion record and nothing wider.
// An open request whose item no run of this harness ever landed is not closed
// on a guess — the work may have landed by a vehicle nothing here recorded, or
// may be pending on the preserved branch — and it is named instead, so what is
// left open at the forge is a list a person can decide rather than a list
// nobody has read.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// SupersededPublications is the forge access retiring a publication needs:
// closing a pull request that will never merge, with a comment saying where its
// work went. It can close and comment and nothing else — in particular it cannot
// merge, which keeps this the same distance from a promotion that every other
// harness-owned forge call is.
//
// It is satisfied by publish.GitHub.
type SupersededPublications interface {
	Close(ctx context.Context, request publish.CloseRequest) (publish.Closure, error)
}

// SupersededBranches is the repository access it needs: removing the branch the
// closed request carried. The deletion is a compare-and-swap on the exact commit
// the harness published, so a remote branch that moved is left for a person.
//
// It is satisfied by gitworktree.Manager.
type SupersededBranches interface {
	DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error
}

// Supersession is the vehicle a publication's work actually landed by: the run
// that landed it, the commit that reached the target branch, and the pull
// request the forge merged where there was one.
type Supersession struct {
	RunID        string `json:"run_id"`
	Commit       string `json:"commit"`
	TargetBranch string `json:"target_branch"`
	Number       int    `json:"number,omitempty"`
	URL          string `json:"url,omitempty"`
}

// PublicationRetirement is what became of one superseded publication. Closed
// and BranchDeleted are reported apart because they cannot be done atomically
// and because a reader acts on them differently: a request still open is a false
// signal on the forge, and a branch still there is debris.
type PublicationRetirement struct {
	Number        int    `json:"number"`
	URL           string `json:"url,omitempty"`
	Branch        string `json:"branch"`
	Closed        bool   `json:"closed"`
	BranchDeleted bool   `json:"branch_deleted"`
	Failure       string `json:"failure,omitempty"`
}

// supersessionOf reads the vehicle from a recorded run whose work landed, and
// supersessionOfOutcome from a run that has just finished. Both go through
// newSupersession, so a run read from the store and a run still in hand describe
// the same landing the same way.
//
// A run that integrated nothing supersedes nothing, and the empty vehicle says
// so: Landed reports false, and nothing is retired on it.
func supersessionOf(landed runstate.State) Supersession {
	if landed.Integration == nil {
		return Supersession{}
	}
	return newSupersession(landed.RunID, landed.Integration.SourceCommit, landed.Integration.TargetBranch, landed.PullRequest)
}

func supersessionOfOutcome(outcome Outcome) Supersession {
	if outcome.Integration == nil {
		return Supersession{}
	}
	return newSupersession(outcome.RunID, outcome.Integration.SourceCommit, outcome.Integration.TargetBranch, outcome.PullRequest)
}

// newSupersession builds the vehicle from the four things that describe it.
//
// The pull request is named only where it is the vehicle. A landing run whose
// own request the forge has not merged has published nothing to send anybody
// to — its merge may still be queued, or may have been dropped — while the
// promoted commit is on the target branch either way, and is what landed the
// work.
func newSupersession(runID, commit, targetBranch string, published *runstate.PullRequest) Supersession {
	by := Supersession{RunID: runID, Commit: commit, TargetBranch: targetBranch}
	if published != nil && published.Merged {
		by.Number = published.Number
		by.URL = published.URL
	}
	return by
}

// Landed reports the vehicle being one this can name. A run that integrated
// nothing supersedes nothing, and closing somebody's pull request on that would
// be an assertion rather than the recorded promotion this is allowed to act on.
func (s Supersession) Landed() bool {
	return s.RunID != "" && s.Commit != "" && s.TargetBranch != ""
}

// Vehicle names in one line what the work landed by, which is what the closing
// comment leads with and what the run's record keeps.
func (s Supersession) Vehicle() string {
	if s.Number > 0 {
		return fmt.Sprintf("pull request #%d, which the forge merged for run %s", s.Number, s.RunID)
	}
	return fmt.Sprintf("commit %s on %s, integrated by run %s", s.Commit, s.TargetBranch, s.RunID)
}

// retirablePublication reports a run holding a pull request that will never
// merge and that nothing has retired yet.
//
// Every clause is doing work. The run ended, so nothing is still going to
// promote from it. It integrated nothing, so the request carries work no
// promotion of its own ever took — a run that did integrate and whose merge the
// forge then dropped is the opposite case and belongs to a person. The request
// is not merged, so there is something still open. And nothing has retired it
// already, which is what keeps a sweep from asking the forge about the same
// request forever.
//
// The one integrated run that qualifies is one whose publication the
// development manager handed back for a fresh run: nothing ever asked the forge
// to merge it, the decision gave its change to the fresh run, and so its request
// is as superseded as a dead run's once that fresh run lands.
func retirablePublication(state runstate.State) bool {
	if state.Retirement != nil {
		return false
	}
	published := state.PullRequest
	return state.Status.Terminal() &&
		published != nil &&
		(state.Integration == nil || published.HandedBack != nil) &&
		!published.Merged &&
		strings.TrimSpace(published.Superseded) == ""
}

// handedBack reports a run whose publication was handed back for a fresh run.
// Its promotion is not a landing of the item: the change it carried was given
// to the fresh run, and it is that run's landing that supersedes this one.
func handedBack(state runstate.State) bool {
	return state.PullRequest != nil && state.PullRequest.HandedBack != nil
}

// landedAt is when a landing run got to where it now is. A run still finishing
// its own publication has no completion time yet, so what it last recorded
// stands in for one: it has integrated either way, which is the fact this is
// about.
func landedAt(landed runstate.State) time.Time {
	if landed.CompletedAt != nil {
		return *landed.CompletedAt
	}
	return landed.UpdatedAt
}

// landedAfter reports the landing run having got there after some moment.
func landedAfter(landed runstate.State, moment time.Time) bool {
	return landedAt(landed).After(moment)
}

// laterLanding reports one landing being the more recent of two.
//
// Which landing is named is not a presentational choice. The ordering rule this
// feeds refuses a publication opened after the landing, so naming an earlier
// landing than the one available would refuse orphans a later landing genuinely
// supersedes — an item worked, closed, reopened and worked again has two, and
// picking whichever was read first would skip the second run's orphan on the
// strength of the first run's landing. Taking the latest removes the case
// rather than failing safe through it.
//
// A tie is decided on the run identifier, so one sweep names what the next one
// will rather than depending on the order records happened to be read in.
func laterLanding(candidate, incumbent runstate.State) bool {
	candidateAt, incumbentAt := landedAt(candidate), landedAt(incumbent)
	if candidateAt.Equal(incumbentAt) {
		return candidate.RunID > incumbent.RunID
	}
	return candidateAt.After(incumbentAt)
}

// supersededPublication is one run's open publication paired with the run whose
// work landed in its place.
type supersededPublication struct {
	state runstate.State
	by    Supersession
}

// OpenPublication is a pull request the harness left open and cannot close: the
// run that published it ended without landing anything, and the harness's own
// records name no later landing of its item that would make the request
// superseded. What became of the work is therefore not something a sweep can
// say — it may be pending on the preserved branch, waiting on a repair or a
// re-run, or it may have landed by a vehicle nothing here recorded, a hand
// merge or another item's change — and the two call for opposite actions, so
// the request is named rather than decided.
//
// It is what the sweep leaves open, reported so that the requests still
// open at the forge are a list a person has read rather than a list nobody has.
// A request already closed at the forge is not one of these: the refresh that
// runs ahead of this sweep has recorded it closed, and there is nothing left
// open for anybody to decide about. Neither is the request of a run that
// succeeded under a policy where a person approves the integration — that
// request is the run's deliverable, open because the policy says so.
type OpenPublication struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Number     int    `json:"number"`
	URL        string `json:"url,omitempty"`
	Branch     string `json:"branch"`
	// Reason is why nothing here can name a vehicle for it, in words a person
	// deciding about the request can act on.
	Reason string `json:"reason"`
}

// partitionPublications sorts every open publication the recorded runs hold
// into the ones a recorded landing supersedes and the ones nothing here can say
// anything about.
//
// The evidence for a supersession is the harness's own and it is two facts
// rather than one. The superseded run ended and integrated nothing, so its pull
// request carries work no promotion ever took. Another run of the same item did
// integrate, which is a promotion this harness made and recorded — so the work
// that request was opened for is on the target branch by that other vehicle.
//
// The landing run must also have got there after the superseded one began, and
// that clause is what keeps this from closing a publication that came *after* a
// landing: an item worked, closed, reopened and worked again has two runs, and
// only the second one's open request is pending. When the request itself was
// opened is not recorded, but it cannot precede the run that opened it, so the
// run's start is the bound available and the one that errs the safe way.
//
// Where an item has more than one landing it is the latest that is named, which
// is what makes that clause exact rather than merely safe: an earlier landing
// tested against a later orphan would refuse an orphan the later landing really
// does supersede.
func partitionPublications(recorded []runstate.State) ([]supersededPublication, []OpenPublication) {
	landed := make(map[string]runstate.State, len(recorded))
	for _, state := range recorded {
		if state.Integration == nil || handedBack(state) {
			continue
		}
		if known, seen := landed[state.WorkItemID]; seen && !laterLanding(state, known) {
			continue
		}
		landed[state.WorkItemID] = state
	}
	superseded := make([]supersededPublication, 0)
	open := make([]OpenPublication, 0)
	for _, state := range recorded {
		if !retirablePublication(state) {
			continue
		}
		by, found := landed[state.WorkItemID]
		switch {
		case found && by.RunID != state.RunID && landedAfter(by, state.StartedAt):
			superseded = append(superseded, supersededPublication{state: state, by: supersessionOf(by)})
		case !stillOpen(*state.PullRequest):
			// Closed at the forge already, by a person or by something else. There
			// is nothing open to decide about, and nothing recorded to retire it in
			// the name of.
		case state.Status == runstate.StatusSucceeded:
			// A run that succeeded without integrating is one whose integration a
			// person approves: its open request is the deliverable, waiting on that
			// person exactly as the policy says, and not a request in doubt.
		case !found || by.RunID == state.RunID:
			open = append(open, openPublication(state, fmt.Sprintf(
				"no run of %s has landed anything, so nothing recorded says what became of the work: it is pending on branch %s, or it landed by a vehicle the harness did not record",
				state.WorkItemID, state.PullRequest.Branch)))
		default:
			open = append(open, openPublication(state, fmt.Sprintf(
				"the latest landing of %s, by run %s, came before run %s began, so this publication is later work that nothing has landed yet",
				state.WorkItemID, by.RunID, state.RunID)))
		}
	}
	return superseded, open
}

// stillOpen reads the last state the forge reported for a request. A record
// that never captured one is read as open, because the one thing this must not
// do is drop a request from that list on the strength of an answer nobody
// recorded.
func stillOpen(published runstate.PullRequest) bool {
	state := strings.ToUpper(strings.TrimSpace(published.State))
	return state == "" || state == "OPEN"
}

func openPublication(state runstate.State, reason string) OpenPublication {
	published := *state.PullRequest
	return OpenPublication{
		RunID:      state.RunID,
		WorkItemID: state.WorkItemID,
		Number:     published.Number,
		URL:        published.URL,
		Branch:     published.Branch,
		Reason:     reason,
	}
}

// retirePublication closes one superseded run's pull request and deletes the
// branch it published. It reports what it managed rather than failing: the work
// itself is on the target branch, so a request that could not be closed is a
// false signal for somebody to read rather than anything at risk.
//
// The close comes first because it is the half a person sees. A branch left
// behind a closed request is debris on the remote; a branch deleted under a
// request still open is a pull request nobody can even read the diff of.
//
// A branch that is already gone — a hand-closer who used the forge's own delete
// button, an earlier retirement whose record never reached disk — is a deletion
// already made, and the manager reports it as one; that is what lets the
// supersession be recorded and the sweep settle rather than reporting the same
// failure on every later pass.
func retirePublication(ctx context.Context, forge SupersededPublications, branches SupersededBranches, superseded runstate.State, by Supersession) PublicationRetirement {
	published := *superseded.PullRequest
	retirement := PublicationRetirement{Number: published.Number, URL: published.URL, Branch: published.Branch}
	closure, err := forge.Close(ctx, publish.CloseRequest{
		Head:    published.Branch,
		Number:  published.Number,
		Comment: renderSupersededComment(superseded, by),
	})
	if err != nil {
		retirement.Failure = fmt.Errorf("close the superseded pull request %d of run %s: %w", published.Number, superseded.RunID, err).Error()
		return retirement
	}
	retirement.Closed = closure.Closed
	// The record said unmerged and the forge says merged, so this publication's
	// own work is on the remote after all. Nothing was closed — the forge is asked
	// before anything is — and the branch stays, because deleting it would retire
	// a merge two records now disagree about.
	if closure.Merged {
		retirement.Failure = fmt.Sprintf(
			"pull request %d of run %s is merged at the forge, so its own work reached the remote and it is not superseded; run %s is recorded as having integrated %s into %s",
			published.Number, superseded.RunID, by.RunID, by.Commit, by.TargetBranch)
		return retirement
	}
	// The branch deleted is the one the request was published on, at the commit
	// the harness last pushed there: a remote branch anything else has moved is
	// refused by the manager and left for a person.
	worktree := worktreeOf(superseded)
	worktree.Branch = published.Branch
	if err := branches.DeleteRemoteBranch(ctx, worktree, published.HeadCommit); err != nil {
		retirement.Failure = fmt.Errorf("delete the branch pull request %d published: %w", published.Number, err).Error()
		return retirement
	}
	retirement.BranchDeleted = true
	return retirement
}

// renderSupersededComment is what the closed pull request is left carrying. It
// says which vehicle the work landed by and how to check that for yourself,
// because a request closed by a machine with no account of why is the same
// unexplained state as one left open.
func renderSupersededComment(superseded runstate.State, by Supersession) string {
	lines := []string{
		fmt.Sprintf("Yoyodyne closed this pull request: the work it was opened for landed by %s, so nothing here is pending review or waiting to merge.", by.Vehicle()),
		"",
		"- Work item: `" + superseded.WorkItemID + "`",
		"- This publication: run `" + superseded.RunID + "` on branch `" + superseded.PullRequest.Branch + "`",
		fmt.Sprintf("- Landed instead: run `%s`, commit `%s` on `%s`", by.RunID, by.Commit, by.TargetBranch),
	}
	if by.URL != "" {
		lines = append(lines, "- Superseding pull request: "+by.URL)
	}
	account := "Nothing on that branch was promoted — run `" + superseded.RunID + "` ended without integrating anything — and the item's work reached the target branch by the vehicle above."
	if handedBack(superseded) {
		account = "Nothing ever asked the forge to merge this request, and its change was handed back for a fresh run — run `" + by.RunID + "` — whose landing is the vehicle above."
	}
	return strings.Join(append(lines, "",
		"The branch this request carries is being deleted with it. "+account,
	), "\n")
}
