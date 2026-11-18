// Package forgehygiene is the harness's own look at the forge on the development
// manager's recurring pass: which pull requests the forge holds open for work
// that is over.
//
// A run publishes its branch as a pull request and the reviewer's verdict merges
// it, so on the ordinary path a request closes with the run that opened it. Two
// things leave one open instead. A later run for the same item lands its own
// request and closes the item, and the earlier request sits open on a branch
// nobody will merge; or the branch was brought onto its target some other way
// — a merge by hand, a replay — and the request stays open carrying nothing.
// Neither is visible from the tracker, which is what the pass read until now,
// and thirty of them accumulated before anybody looked at the forge itself.
//
// This notices and does nothing else. Each request it finds is stated as a
// finding on the pass, keyed on the request so it is said once rather than
// once an hour. Closing is not this package's: the maintenance sweep closes the
// requests a recorded run opened, on the rules in internal/orchestrator's
// closeditems.go, and a request no run of this harness opened is somebody
// else's, so the harness's part is to make sure they know it is there.
package forgehygiene

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Forge is the forge as this pass reads it. It is satisfied by publish.GitHub,
// which is the same client the publication path opens and merges through, so
// what this reads is the repository runs publish into and not one inferred from
// wherever the process happens to be standing.
type Forge interface {
	ListOpen(ctx context.Context) ([]publish.PullRequest, error)
	Contains(ctx context.Context, base, commit string) (bool, error)
}

// Tracker is the one reading of the tracker this takes: which work is closed.
// It is satisfied by beads.Client.
type Tracker interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
}

// Runs is the harness's own record of what it published, read to say which work
// item a request was opened for. It is satisfied by *runstate.Store.
type Runs interface {
	Recorded() ([]runstate.State, error)
}

// closedStatus is the tracker's name for work that is finished.
const closedStatus = "closed"

// branchPrefix is what every run branch the harness pushes starts with. A
// request whose head is not under it was opened by somebody else, and the pass
// can still say whether its branch is already carried — that is a fact about
// the forge — but not what work it was for.
const branchPrefix = "yoyodyne/"

// Sweeper reads the forge once per pass and reports what it holds open for
// nothing.
type Sweeper struct {
	Forge   Forge
	Tracker Tracker
	// Runs is optional. Without it a request's work item is read from its branch
	// name alone, which names the item in the form the branch was cut in rather
	// than the tracker's own; a run record names it exactly.
	Runs Runs
}

// Notice reads the forge's open pull requests and reports each one whose work
// item is closed or whose head is already contained in its base — except those
// whose numbers are in reported, which an earlier pass already said.
//
// It reports every condition it can establish and stops on the readings it
// cannot make: a forge that cannot be listed or a tracker that cannot be read
// is an error rather than a quiet pass, because a pass that found nothing and a
// pass that could not look must not be the same record. One request whose
// comparison fails is set aside with its own error and the rest are reported,
// since a request the forge would not compare says nothing about the others.
func (s Sweeper) Notice(ctx context.Context, reported map[int]bool) ([]runstate.ForgeNotice, error) {
	if s.Forge == nil || s.Tracker == nil {
		return nil, errors.New("the forge-hygiene pass needs the forge to list requests from and the tracker to read closed work from")
	}
	open, err := s.Forge.ListOpen(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the forge's open pull requests: %w", err)
	}
	candidates := make([]publish.PullRequest, 0, len(open))
	for _, request := range open {
		if !reported[request.Number] {
			candidates = append(candidates, request)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	items, err := s.workItems(candidates)
	if err != nil {
		return nil, err
	}
	// The tracker is read once per pass and only when some request names work
	// to look up: a tracker read is the slow reading here, and a forge holding
	// only requests nobody here opened gives it nothing to answer.
	//
	// A tracker that does not answer costs the pass the half of the reading it
	// feeds and not the other half. Until yoyodyne-ifd.433.20 it cost both: the
	// listing failing returned before any request was compared, so a pass on a
	// contended store noticed nothing at all, including requests whose branches
	// the forge itself says are carried. Now every request is still compared,
	// and what could not be read is named beside what was found.
	closed := map[string]string{}
	var problems []error
	for _, item := range items {
		if item != "" {
			if closed, err = s.closedWorkItems(ctx); err != nil {
				closed = map[string]string{}
				problems = append(problems, fmt.Errorf("%w; so no request was judged on its work item being closed, only on whether its branch is carried, and the harness reads the tracker again on the next pass", err))
			}
			break
		}
	}
	var notices []runstate.ForgeNotice
	for _, request := range candidates {
		notice := runstate.ForgeNotice{
			Number:     request.Number,
			URL:        request.URL,
			HeadBranch: request.HeadBranch,
			BaseBranch: request.BaseBranch,
			WorkItemID: items[request.Number],
		}
		if notice.WorkItemID != "" {
			if exact, closedItem := closed[notice.WorkItemID]; closedItem {
				notice.WorkItemID = exact
				notice.ItemClosed = true
			}
		}
		if request.BaseBranch != "" && request.HeadCommit != "" {
			contained, err := s.Forge.Contains(ctx, request.BaseBranch, request.HeadCommit)
			if err != nil {
				problems = append(problems, fmt.Errorf("pull request #%d: %w", request.Number, err))
			} else {
				notice.Contained = contained
			}
		}
		if notice.ItemClosed || notice.Contained {
			notices = append(notices, notice)
		}
	}
	sort.Slice(notices, func(i, j int) bool { return notices[i].Number < notices[j].Number })
	return notices, errors.Join(problems...)
}

// workItems names the work each request was opened for, by number. The run
// record is asked first, because it names the item exactly; the branch is what
// is left for a request no recorded run opened, and it names the item in the
// form the branch was cut in.
func (s Sweeper) workItems(requests []publish.PullRequest) (map[int]string, error) {
	items := make(map[int]string, len(requests))
	byNumber := map[int]string{}
	byBranch := map[string]string{}
	if s.Runs != nil {
		states, err := s.Runs.Recorded()
		if err != nil {
			return nil, fmt.Errorf("read which work the recorded runs published: %w", err)
		}
		for _, state := range states {
			if state.PullRequest != nil {
				byNumber[state.PullRequest.Number] = state.WorkItemID
			}
			if state.Branch != "" {
				byBranch[state.Branch] = state.WorkItemID
			}
		}
	}
	for _, request := range requests {
		switch {
		case byNumber[request.Number] != "":
			items[request.Number] = byNumber[request.Number]
		case byBranch[request.HeadBranch] != "":
			items[request.Number] = byBranch[request.HeadBranch]
		default:
			items[request.Number] = itemFromBranch(request.HeadBranch)
		}
	}
	return items, nil
}

// closedWorkItems reads the closed work once and keys it two ways: by the
// tracker's own identifier, and by the form a run branch carries it in — lower
// case, dots made hyphens — so an item read off a branch name is still found.
// The value is the tracker's identifier either way, which is what a finding
// names.
func (s Sweeper) closedWorkItems(ctx context.Context) (map[string]string, error) {
	items, err := s.Tracker.List(ctx, closedStatus)
	if err != nil {
		return nil, fmt.Errorf("read the closed work items: %w", err)
	}
	closed := make(map[string]string, 2*len(items))
	for _, item := range items {
		closed[item.ID] = item.ID
		closed[branchForm(item.ID)] = item.ID
	}
	return closed, nil
}

// itemFromBranch reads the work item segment out of a run branch —
// yoyodyne/<item>/<run> — and reports nothing for a branch of any other shape.
func itemFromBranch(branch string) string {
	if !strings.HasPrefix(branch, branchPrefix) {
		return ""
	}
	item, _, found := strings.Cut(strings.TrimPrefix(branch, branchPrefix), "/")
	if !found || item == "" {
		return ""
	}
	return item
}

// branchForm is the work item identifier as a run branch carries it. It mirrors
// what the worktree manager does when it cuts one.
func branchForm(workItemID string) string {
	return strings.ToLower(strings.ReplaceAll(workItemID, ".", "-"))
}
