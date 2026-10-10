package readmodel

// One work item, whole, for the surface that opens it as a card.
//
// The standing status names work items by id and title and says where each
// stands; what an item actually is — its description, its design, its
// acceptance criteria, its notes, and what the harness last did to it — lives in
// the tracker and in the run records, and until this existed reaching any of it
// from a surface meant `bd show` at a terminal. This is that reading, one item
// at a time, so a surface that shows a card reads the projection rather than
// the tracker: the item's fields are the tracker's own, and the run beside them
// is summarized by the same derivation `yoyo status <item>` lists it with, so
// the card and the terminal cannot say different words about one run.
//
// It is one item rather than every item because it costs a tracker command,
// and a page that asks every ten seconds must not ask that of the whole
// backlog. It is asked for when somebody opens the card and not before.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// ErrNoSuchWorkItem is the tracker holding nothing under the id asked for. It
// is the tracker's own sentinel, said here so a surface reading this model can
// tell an item that does not exist from a tracker that could not be read
// without reading the tracker's package for the difference.
var ErrNoSuchWorkItem = beads.ErrNoSuchWorkItem

// ValidWorkItemID reports whether an id has the shape the tracker accepts. A
// surface checks it before it puts an id it was handed on a command line, and
// refuses one that does not without reflecting it.
func ValidWorkItemID(id string) bool {
	return beads.ValidIssueID(id)
}

// ItemTracker reads one work item whole. It is satisfied by beads.Client.
type ItemTracker interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
}

// Histories reads what became of the runs recorded for one work item, priced
// and summarized as `yoyo status` lists them. It is satisfied by
// *runstate.Store.
type Histories interface {
	History(runstate.RunQuery) (runstate.RunHistory, error)
}

// WorkItemSources are the records one work item is read from. Both are
// interfaces so the derivation can be exercised without a tracker or a state
// directory.
type WorkItemSources struct {
	Tracker ItemTracker
	// Runs is the run records, read for the item's latest run. It is optional,
	// and a reading without one says so in the answer rather than reporting that
	// nothing ever ran: RunsProblem is why there is none — a state root that
	// could not be resolved, a store that would not open — and is the answer's
	// RunProblem where it is set, so the card names what failed rather than a
	// wiring gap that is not there.
	Runs        Histories
	RunsProblem string
	// Remains is the repository the card asks what a finished run left, so the
	// card says what is there rather than what the run's removal flags say. Nil
	// answers from the record, and the card says nothing looked.
	Remains Remains
	// Passes is the recurring passes' records, read for the last pass that was
	// handed this item from the work waiting in a role's conversation. Nil
	// leaves the card saying nothing about passes.
	Passes Sweeps
	// TrackerTimeout bounds the tracker command, so an unresponsive tracker
	// costs this answer rather than hanging the surface that asked.
	TrackerTimeout time.Duration
	// Now stamps the reading. It defaults to the wall clock and is injected so a
	// test can pin an elapsed time.
	Now func() time.Time
}

// WorkItem is one work item as the tracker holds it, with the run the harness
// last made for it. Every field the tracker carries for the card is here under
// its own name, empty rather than absent where the item has none, so a card
// shows a plain label against nothing rather than leaving the reader to wonder
// whether the field was read.
type WorkItem struct {
	ObservedAt time.Time `json:"observed_at"`

	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Status             string   `json:"status"`
	Priority           int      `json:"priority"`
	Labels             []string `json:"labels"`
	RelevantGoals      []string `json:"relevant_goals"`
	Parent             string   `json:"parent"`
	Description        string   `json:"description"`
	Design             string   `json:"design"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	Notes              string   `json:"notes"`

	// Run is the run the harness last made for this item — the one in flight
	// on it, or the one whose change is preserved on it, or simply the latest —
	// and nil where no run of the item is recorded. Which of those it is, it
	// says itself.
	Run *ItemRun `json:"run,omitempty"`
	// RunProblem is the run records not having been read, and why: a card that
	// showed no run over records nobody could open would be reporting an item
	// nothing has ever touched.
	RunProblem string `json:"run_problem,omitempty"`
	// LastConsidered is the last recurring pass that was handed this item from
	// the work waiting in a role's conversation, and whether it took it; nil
	// where no pass record names it. ConsideredProblem is the pass records not
	// having been read to the end.
	LastConsidered    *ItemConsideration `json:"last_considered,omitempty"`
	ConsideredProblem string             `json:"considered_problem,omitempty"`
}

// ItemRun is the item's latest run, in the words `yoyo status` lists a run in.
type ItemRun struct {
	RunID string `json:"run_id"`
	// Outcome is what became of the run, in the run history's fixed vocabulary;
	// a run still going carries its own status word there.
	Outcome runstate.RunOutcome `json:"outcome"`
	Phase   runstate.Phase      `json:"phase,omitempty"`
	// ResumingIntegration is the run at its promotion again after the
	// environment stopped it there, said as the standing line says it.
	ResumingIntegration bool `json:"resuming_integration,omitempty"`
	// InFlight is the run still going, and Preserved is a run that ended with
	// its change still on a branch or in a checkout, as its record says. They are
	// the two conditions the card exists to show, and a run that is neither is
	// carried anyway, so the card says what the last run came to rather than
	// nothing.
	InFlight  bool `json:"in_flight"`
	Preserved bool `json:"preserved"`
	// Remains is what survives of the change, in the words `yoyo status` says
	// it in: what the repository held when the card was read, and whether it was
	// looked for. It is empty for a run in flight, which holds everything it has.
	Remains string `json:"remains,omitempty"`
	// Found is the look itself: the branch and the checkout, each there or not,
	// and when that was asked.
	Found             *triage.Found `json:"found,omitempty"`
	Branch            string        `json:"branch,omitempty"`
	WorktreePath      string        `json:"worktree_path,omitempty"`
	ProviderSessionID string        `json:"provider_session_id,omitempty"`
	// Failure is the run's own reason for ending, where it gave one. It is the
	// reason and never the verdict: what became of the run is Outcome.
	Failure string `json:"failure,omitempty"`
	// StopClass is which gate stopped the run, as its record names it, and Reason
	// is the reason as every surface prints it: that class as its first word, then
	// the run's own words. Reason is the run history's derivation, so the card and
	// `yoyo status` cannot word one stop two ways.
	StopClass   runstate.StopClass  `json:"stop_class,omitempty"`
	Reason      string              `json:"reason,omitempty"`
	StartedAt   time.Time           `json:"started_at"`
	CompletedAt *time.Time          `json:"completed_at,omitempty"`
	Elapsed     time.Duration       `json:"elapsed,omitempty"`
	CostText    string              `json:"cost_text"`
	Tokens      runstate.TokenUsage `json:"tokens"`
	CostUSD     float64             `json:"cost_usd"`
	// UnknownCost says why there is no figure rather than reporting one of
	// zero: a run whose evidence is gone did not cost nothing.
	UnknownCost string `json:"unknown_cost,omitempty"`
}

// ReadWorkItem reads one item from the tracker and its latest run from the run
// records. An error is the item not being readable at all — the tracker refused
// or could not be reached, or holds nothing under the id, which errors.Is tells
// apart as ErrNoSuchWorkItem. The run records failing costs the run and not the
// item, and says so in RunProblem.
func ReadWorkItem(ctx context.Context, sources WorkItemSources, id string) (WorkItem, error) {
	if sources.Tracker == nil {
		return WorkItem{}, errors.New("nothing was wired to read the tracker")
	}
	trackerCtx, cancel := sources.bounded(ctx)
	defer cancel()
	found, err := sources.Tracker.Show(trackerCtx, id)
	if err != nil {
		if errors.Is(err, ErrNoSuchWorkItem) {
			return WorkItem{}, err
		}
		return WorkItem{}, fmt.Errorf("the work item could not be read: %w", err)
	}
	now := sources.now()
	item := WorkItem{
		ObservedAt:         now,
		ID:                 found.ID,
		Title:              found.Title,
		Status:             found.Status,
		Priority:           found.Priority,
		Labels:             append([]string{}, found.Labels...),
		RelevantGoals:      append([]string{}, found.RelevantGoals...),
		Parent:             found.Parent,
		Description:        found.Description,
		Design:             found.Design,
		AcceptanceCriteria: found.AcceptanceCriteria,
		Notes:              found.Notes,
	}
	item.Run, item.RunProblem = readLatestRun(ctx, sources, found.ID, now)
	item.LastConsidered, item.ConsideredProblem = ReadLastConsidered(sources.Passes, found.ID)
	return item, nil
}

// readLatestRun is the item's most recent run, summarized and priced by the run
// history's own derivation — the one `yoyo status <item>` prints from — so the
// card's outcome, phase, remains, and cost are the terminal's words.
func readLatestRun(ctx context.Context, sources WorkItemSources, id string, now time.Time) (*ItemRun, string) {
	if sources.Runs == nil {
		if sources.RunsProblem != "" {
			return nil, "the runs could not be opened: " + sources.RunsProblem
		}
		return nil, "nothing was wired to read the runs"
	}
	history, err := sources.Runs.History(runstate.RunQuery{WorkItemID: id, Limit: 1})
	if err != nil {
		return nil, fmt.Sprintf("the runs of this item could not be read: %v", err)
	}
	if len(history.Runs) == 0 {
		return nil, ""
	}
	// The run's change is looked for in the repository, through the run's own
	// record, as `yoyo status` looks for it — where the store can list what it
	// holds, which the run store always can.
	if recorded, lists := sources.Runs.(Recorded); lists {
		LookForSummaries(ctx, sources.Remains, recorded, history.Runs[:1])
	}
	latest := history.Runs[0]
	run := &ItemRun{
		RunID:               latest.RunID,
		Outcome:             latest.Outcome,
		Phase:               latest.Phase,
		ResumingIntegration: latest.ResumingIntegration,
		InFlight:            latest.Status.InFlight(),
		Branch:              latest.Branch,
		WorktreePath:        latest.WorktreePath,
		ProviderSessionID:   latest.ProviderSessionID,
		Failure:             latest.Failure,
		StopClass:           latest.RecordedStopClass(),
		Reason:              latest.Reason(),
		StartedAt:           latest.StartedAt,
		CompletedAt:         latest.CompletedAt,
		CostUSD:             latest.CostUSD,
		CostText:            latest.Tokens.CostText(latest.CostUSD),
		Tokens:              latest.Tokens,
		UnknownCost:         latest.UnknownCost,
	}
	if run.InFlight {
		run.Elapsed = now.Sub(latest.StartedAt)
	} else {
		run.Preserved = latest.Preserved()
		run.Remains = latest.DescribeRemains()
		run.Found = latest.Found
	}
	return run, ""
}

func (s WorkItemSources) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.TrackerTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, s.TrackerTimeout)
}

func (s WorkItemSources) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
