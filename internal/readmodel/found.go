package readmodel

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// Look is the one way a surface finds out what a stopped run left: asked of the
// repository as the surface writes, never read off the run's removal flags.
// The hold the pull reads, the claim audit and the note it writes, the triage
// docket, and `yoyo status` all take one of these, so none of them can answer
// the question a way the others do not.
type Look func(run runstate.State) triage.Found

// Looking is the look one reading makes. A run that recorded neither a branch
// nor a worktree has nothing to look for and is answered without asking.
//
// remains may be nil, for a reading wired without a repository. It then answers
// from what the run's record says survived, and says in the answer that nothing
// looked, so no surface can pass a record off as a check. A look that fails
// answers nothing and says why, and triage.Found.Holds treats that as a change
// that may still be there.
func Looking(ctx context.Context, remains Remains, now func() time.Time) Look {
	if now == nil {
		now = time.Now
	}
	return func(run runstate.State) triage.Found {
		found := triage.Found{At: now(), Branch: run.Branch, WorktreePath: run.WorktreePath}
		if !found.Recorded() {
			return found
		}
		// The repository asked here is the project's. A run in the companion intent
		// repository has its branch and checkout there, so asking would answer for
		// a repository the run is not in; its record is read as written instead and
		// says that nothing looked.
		if remains == nil || run.Repository == runstate.RepositoryIntent {
			recorded := run.Artifacts()
			found.BranchThere = recorded.Branch != "" && !recorded.BranchRemoved
			found.WorktreeThere = recorded.WorktreePath != "" && !recorded.WorktreeRemoved
			found.Unchecked = "nothing was wired to look in the repository"
			if run.Repository == runstate.RepositoryIntent {
				found.Unchecked = "the run is in the companion intent repository, which this reading does not look in"
			}
			return found
		}
		survives, err := remains.Survives(ctx, gitworktree.Worktree{
			RunID:         run.RunID,
			WorkItemID:    run.WorkItemID,
			Path:          run.WorktreePath,
			Branch:        run.Branch,
			BaseCommit:    run.BaseCommit,
			TargetBranch:  run.TargetBranch,
			HarnessCommit: run.HarnessCommit,
		})
		if err != nil {
			found.Unknown = true
			found.Unchecked = boundUnchecked(fmt.Sprintf("the repository could not be asked (%v)", err))
			return found
		}
		found.BranchThere = run.Branch != "" && survives.BranchExists
		found.WorktreeThere = run.WorktreePath != "" && survives.WorktreePresent
		return found
	}
}

// LookFor is Looking for one run.
func LookFor(ctx context.Context, remains Remains, run runstate.State) triage.Found {
	return Looking(ctx, remains, nil)(run)
}

// Recorded is the durable run state a listing looks the runs up in. It is
// satisfied by *runstate.Store.
type Recorded interface {
	Recorded() ([]runstate.State, error)
}

// LookForSummaries puts what the repository holds onto every summarized run
// that is over and did not succeed, which is the set `yoyo status` says
// preservation about. The summary names the run but not its base commit, which
// the repository's ownership check asks for, so each is looked for through the
// run's own record; where those records cannot be read the summaries say that
// nothing could be looked for, rather than falling back on the flags unsaid.
func LookForSummaries(ctx context.Context, remains Remains, recorded Recorded, summaries []runstate.RunSummary) {
	byID := map[string]runstate.State{}
	runs, err := recorded.Recorded()
	for _, run := range runs {
		byID[run.RunID] = run
	}
	look := Looking(ctx, remains, nil)
	for index := range summaries {
		summary := &summaries[index]
		if !summary.Status.Terminal() || summary.Outcome == runstate.OutcomeSucceeded {
			continue
		}
		run, known := byID[summary.RunID]
		if !known {
			if err == nil {
				continue
			}
			summary.Found = &triage.Found{
				At:           time.Now(),
				Branch:       summary.Branch,
				WorktreePath: summary.WorktreePath,
				Unknown:      true,
				Unchecked:    boundUnchecked(fmt.Sprintf("the run records could not be read to look for it (%v)", err)),
			}
			continue
		}
		found := look(run)
		summary.Found = &found
	}
}

func boundUnchecked(reason string) string {
	if len(reason) <= triage.MaxFoundUncheckedBytes {
		return reason
	}
	cut := triage.MaxFoundUncheckedBytes - len("...")
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return reason[:cut] + "..."
}

// LookForPrices answers preservation beside each run's price from the same
// repository lookup as the hold. The price itself remains event-log evidence.
func LookForPrices(ctx context.Context, remains Remains, recorded Recorded, prices []runstate.ItemPrice) {
	runs, err := recorded.Recorded()
	byID := make(map[string]runstate.State, len(runs))
	for _, run := range runs {
		byID[run.RunID] = run
	}
	look := Looking(ctx, remains, nil)
	for i := range prices {
		for j := range prices[i].Runs {
			price := &prices[i].Runs[j]
			run, known := byID[price.RunID]
			if !known {
				reason := "the run record was not found"
				if err != nil {
					reason = fmt.Sprintf("the run records could not be read (%v)", err)
				}
				price.Found = &triage.Found{At: time.Now(), Unknown: true, Unchecked: boundUnchecked(reason)}
				price.Remains = price.Found.DescribeRemains()
				continue
			}
			found := look(run)
			price.Found = &found
			price.Remains = found.DescribeRemains()
		}
	}
}
