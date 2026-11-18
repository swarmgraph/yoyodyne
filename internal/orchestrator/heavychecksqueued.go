package orchestrator

import (
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The heavy checks the merge queue still needs, which is what keeps a place
// free for a landing check (docs/designs/heavy-check-capacity.md, "Keeping a
// place for landing checks"). Nothing is stored for it: it is read from the
// queue's own record each time a place is asked for, so a place kept for an
// entry stops being kept the moment the entry lands, is handed back, or is
// withdrawn.

// QueuedHeavyChecks reads the product's merge queues for the heavy checks an
// entry in them has yet to pass.
type QueuedHeavyChecks struct {
	Queues    QueuedHeavyChecksDirectory
	Config    config.Config
	Execution config.Execution
}

// QueuedHeavyChecksDirectory is the part of the merge queues' record it reads.
// *runstate.MergeQueueStore satisfies it.
type QueuedHeavyChecksDirectory interface {
	Keys() ([]runstate.MergeQueueKey, error)
	Entries(key runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error)
	Landing(key runstate.MergeQueueKey, entryID string) (runstate.MergeQueueLanding, bool, error)
	Generations(key runstate.MergeQueueKey, entryID string) ([]runstate.MergeQueueGeneration, error)
}

// Needs is the set of heavy checks, by their execution.heavy_checks names,
// that some entry the queue has not finished with has no passing result for on
// its candidate. An entry whose candidate is not built yet needs every heavy
// check its queue run would make. A record that cannot be read keeps no place:
// the landing check then waits in the line as it did before places were kept,
// which costs time and never a wrong grant.
func (q QueuedHeavyChecks) Needs() map[string]bool {
	needs := map[string]bool{}
	if q.Queues == nil {
		return needs
	}
	keys, err := q.Queues.Keys()
	if err != nil {
		return map[string]bool{}
	}
	for _, key := range keys {
		entries, err := q.Queues.Entries(key)
		if err != nil {
			return map[string]bool{}
		}
		for _, entry := range entries {
			landing, found, err := q.Queues.Landing(key, entry.EntryID)
			if err != nil {
				return map[string]bool{}
			}
			if found && !landing.Waiting() {
				continue
			}
			generations, err := q.Queues.Generations(key, entry.EntryID)
			if err != nil {
				return map[string]bool{}
			}
			for _, command := range q.commandsLeft(entry, generations) {
				if name, _, heavy := q.Execution.HeavyCheck(command); heavy {
					needs[name] = true
				}
			}
		}
	}
	return needs
}

// commandsLeft is the checks an entry's candidate has not yet passed: those of
// its current candidate with no passing result, which a candidate stopped and
// run again keeps, or every check the queue would run where none is built.
func (q QueuedHeavyChecks) commandsLeft(entry runstate.MergeQueueEntry, generations []runstate.MergeQueueGeneration) []string {
	for index := len(generations) - 1; index >= 0; index-- {
		generation := generations[index]
		if generation.Invalidated != nil {
			continue
		}
		var left []string
		for _, command := range generation.Checks.Commands {
			if !generationPassed(generation, command) {
				left = append(left, command)
			}
		}
		return left
	}
	return q.configured(entry)
}

func generationPassed(generation runstate.MergeQueueGeneration, command string) bool {
	if generation.CheckRun == nil {
		return false
	}
	for _, result := range generation.CheckRun.Results {
		if result.Command == command && result.Passed && result.CouldNotRun == "" {
			return true
		}
	}
	return false
}

// configured is what a candidate not yet built will be checked by, read from the
// configuration alone: a document entry by the document checks, an entry where
// the forge's required checks stand in for the full run by the short local
// checks, and any other by the declared queue list and its race check, or every
// configured check. It may name a check the entry turns out not to owe, which
// keeps a place a little longer, never shorter.
func (q QueuedHeavyChecks) configured(entry runstate.MergeQueueEntry) []string {
	cfg := q.Config
	switch {
	case entry.WorkItemID == entry.RunID && runstate.ValidRunID(entry.RunID) && len(cfg.DocumentChecks) > 0:
		return slices.Clone(cfg.DocumentChecks)
	case cfg.Execution.CIChecks.Enabled && len(cfg.Execution.CIChecks.Required) > 0 && cfg.Approvals.Publishing == domain.ApprovalAutomatic:
		return shortLocalChecks(cfg.Checks, cfg.Execution.CIChecks.Local)
	case cfg.DeclaresMergeQueueChecks():
		commands := slices.Clone(cfg.MergeQueueChecks)
		if race := strings.TrimSpace(cfg.MergeQueueRaceCheck); race != "" && !slices.Contains(commands, cfg.MergeQueueRaceCheck) {
			commands = append(commands, cfg.MergeQueueRaceCheck)
		}
		return commands
	}
	return slices.Clone(cfg.Checks)
}
