package orchestrator

// The checks a project declares for the harness's merge queue
// (config.MergeQueueChecks), and the narrowed race check the queue adds for a
// change that owes one (docs/designs/merge-queue-pace.md, decision 1).
//
// A project that declares none has every configured check run whole on the
// candidate, as the queue always did. A project that declares a list has
// exactly those checks run whole on exactly the candidate commit. The declared
// list and its digest are recorded with the candidate's evidence
// (runstate.MergeQueueCheckConfiguration.Declared), and the generation's gate
// refuses a candidate not verified by the list in force now.
//
// What the declaration takes off the queue is the check a change's own run
// already made on its own code. So where the change's code was never checked
// by the race check (config.MergeQueueRaceCheck) — the approved head carries
// no passing result of it, and the change touches files it covers — the queue
// adds that check to this candidate's run, told only the packages the change
// touches (MergeQueueWorker.check). No change lands without a race check on its
// own code. Where the record cannot say, the check is added: when in doubt,
// more runs.

import (
	"slices"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// queueDeclaration is what the project declared for its merge queue, and the
// approved head it is asked of.
type queueDeclaration struct {
	// Checks is the declared list, empty where the project declares none.
	Checks []string
	// Race is the check the queue adds where a change owes it.
	Race string
	// Head is the approved head of the entry the declaration is read for.
	Head string
}

func (d queueDeclaration) declared() bool { return len(d.Checks) > 0 }

// wanted is every check the queue owes the run's change: the declared list, and
// the race check where this run's evidence leaves it owed.
func (d queueDeclaration) wanted(run runstate.State) []string {
	wanted := slices.Clone(d.Checks)
	if d.Race != "" && !slices.Contains(wanted, d.Race) && raceOwed(run, d.Head, d.Race) {
		wanted = append(wanted, d.Race)
	}
	return wanted
}

// declaration is what the project declared for its queue, read for one entry.
func (p *Pipeline) declaration(entry runstate.MergeQueueEntry) queueDeclaration {
	return queueDeclaration{Checks: p.Config.MergeQueueChecks, Race: p.Config.MergeQueueRaceCheck, Head: entry.ApprovedHead}
}

// declaredQueueChecks is the checks the queue verifies an entry's candidate by
// where the project declares a list: that list, with the race check added where
// the entry owes it. A run record that cannot be read owes it.
func (p *Pipeline) declaredQueueChecks(entry runstate.MergeQueueEntry) runstate.MergeQueueCheckConfiguration {
	declaration := p.declaration(entry)
	commands := slices.Clone(declaration.Checks)
	if declaration.Race != "" && !slices.Contains(commands, declaration.Race) {
		owed := true
		if p.Store != nil {
			if run, err := p.Store.Load(entry.RunID); err == nil {
				owed = raceOwed(run, declaration.Head, declaration.Race)
			}
		}
		if owed {
			commands = append(commands, declaration.Race)
		}
	}
	return runstate.NewMergeQueueDeclaredConfiguration(commands, declaration.Checks)
}

// raceOwed reports whether a change owes the race check at the queue: its
// approved head carries no passing result of it, and the change touches files
// it covers. A passing result counts only over the head being admitted, on the
// attempt the run recorded. A change recorded as touching no Go package has
// nothing for the check to cover. A run with no evidence owes it.
func raceOwed(run runstate.State, head, race string) bool {
	passed := run.ChecksPassed
	if passed == nil {
		return true
	}
	if passed.Commit != "" && passed.Commit == head && slices.Contains(passed.Commands, race) {
		return false
	}
	return !checks.TouchesNoGoPackage(passed.Narrowed)
}
