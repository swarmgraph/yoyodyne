package orchestrator

// What a repair's prompt carries beside the failure being repaired: what the
// development manager decided about the run and why, and the work item as it
// stands when the repair starts. The failure alone lets a developer repeat a fix
// that was already refused, or work from item text corrected since the run
// started, which the reviewer reads and the developer's session does not hold.
//
// It is added in two places, and every repair passes one of them: where a
// running process hands a failure back (handBackForRepair) and where a process
// takes up a run that owes a repair (resumeRun).

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	// maxRepairNotesBytes is how much of the notes added since the run started a
	// repair's prompt carries: the latest, because a later note corrects an
	// earlier one.
	maxRepairNotesBytes = 24 << 10
	// maxRepairItemFieldBytes is how much of each changed part of the item it
	// carries: the beginning.
	maxRepairItemFieldBytes = 16 << 10
)

// handBackForRepair is a repair prompt with what a repair is given beside the
// failure, the work item read again first. The item is kept on the run, so the
// review of this repair judges the same text its developer was given. A read
// the tracker does not answer is said in the prompt and costs the repair
// nothing else.
func (a *activeRun) handBackForRepair(ctx context.Context, prompt string) string {
	unread := ""
	item, err := a.pipeline.Tracker.Show(ctx, a.item.ID)
	switch {
	case err != nil:
		unread = oneLineOf(err)
	case item.ID != a.item.ID:
		unread = fmt.Sprintf("the tracker answered with %q", item.ID)
	default:
		a.item = item
	}
	return prompt + a.repairContext(unread)
}

// repairContext is the two sections, from the run's record, the item's triage
// record, and the item as last read. unread is why the item could not be read
// again for this repair, and empty where it was.
func (a *activeRun) repairContext(unread string) string {
	return a.decisionSection() + a.itemSinceStartSection(unread)
}

func (a *activeRun) decisionSection() string {
	counters, err := a.pipeline.Store.Triage().Counters(a.state.WorkItemID)
	return repairDecisionSection(a.state, counters, err)
}

// repairDecisionSection says what the development manager decided. A repair she
// ordered is read from the item's triage record, which keeps her reason whole.
// That record holds one decision for each run, so where a later one stands the
// order this run was continued under is read from the run's own record of it.
func repairDecisionSection(state runstate.State, counters runstate.TriageCounters, unread error) string {
	var section strings.Builder
	section.WriteString("\n\n# The development manager's decision\n\n")
	decision, found := counters.DecisionOf(state.RunID)
	continued := decidedContinuation(state)
	switch {
	case unread == nil && found && decision.Decision == runstate.TriageDecisionRepair:
		fmt.Fprintf(&section, "The development manager ordered a repair of this run, %s. Every repair attempt since is made under that order. Her reason:\n\n%s\n\n", decision.Cite(), strings.TrimSpace(decision.Reason))
		section.WriteString(decisionPrecedence)
		return section.String()
	case continued != nil:
		fmt.Fprintf(&section, "The development manager ordered a repair of this run, which was carried out at %s. What the run records of that order, cut where it is %d bytes long:\n\n%s\n\n",
			continued.ContinuedAt.Local().Format("2006-01-02 15:04 MST"), runstate.MaxSelectionReasonBytes, strings.TrimSpace(continued.Reason))
		if unread != nil {
			fmt.Fprintf(&section, "The item's triage record, which keeps her reason whole, could not be read: %s.\n\n", oneLineOf(unread))
		} else if found {
			fmt.Fprintf(&section, "Her latest decision about this run is a later one: %s\n\n", decision.Describe())
		}
		section.WriteString(decisionPrecedence)
		return section.String()
	case unread != nil:
		fmt.Fprintf(&section, "Whether the development manager has decided anything about this run is not known: the item's triage record could not be read (%s). The run's own record holds no repair she ordered. Work from the failure above and the work item.", oneLineOf(unread))
		return section.String()
	}
	section.WriteString("No decision of the development manager ordering a repair of this run is recorded. The harness handed the failure above back itself, so work from that failure and the work item.")
	if found {
		fmt.Fprintf(&section, "\n\nShe has recorded another decision about this run: %s", decision.Describe())
	}
	return section.String()
}

// decisionPrecedence is which governs where the order and the failure differ,
// and what the order does not change.
const decisionPrecedence = "Where this decision and the findings or the failure above differ, the decision takes precedence: it says which of them this repair answers, how, and how far it goes. Do what it says even where a finding asks for more or for something else, and name in your summary each finding you left or answered differently because of it, so the reviewer can see why. The decision changes nothing the work item asks for, grants no path, and lifts no check, invariant or review: where it and the work item as it now stands differ, the work item governs, and say so in your summary naming both."

// decidedContinuation is the latest continuation of the run the development
// manager decided and that was not given back, or nil.
func decidedContinuation(state runstate.State) *runstate.RepairContinuation {
	for index := len(state.RepairContinuations) - 1; index >= 0; index-- {
		if continuation := state.RepairContinuations[index]; !continuation.ByHarness && !continuation.Returned {
			return &continuation
		}
	}
	return nil
}

// itemSinceStartSection says what of the work item changed since the run
// started: each changed part as it now stands, and the notes added since.
func (a *activeRun) itemSinceStartSection(unread string) string {
	var section strings.Builder
	section.WriteString("\n\n# The work item as it stands now\n\n")
	if unread != "" {
		fmt.Fprintf(&section, "The work item could not be read again for this repair (%s). What follows is from the last time this run read it, so anything added since is missing.\n\n", unread)
	}
	start := a.state.ItemAtStart
	if start == nil {
		section.WriteString("This run kept no record of what its work item said when it started, so the item is given here as it stands now, and nothing says which of it is new.\n\n")
		start = &runstate.ItemAtStart{}
	}
	changed := false
	for _, field := range []struct{ name, text, was string }{
		{"title", a.item.Title, start.Title},
		{"description", a.item.Description, start.Description},
		{"design guidance", a.item.Design, start.Design},
		{"acceptance criteria", a.item.AcceptanceCriteria, start.AcceptanceCriteria},
	} {
		if runstate.ItemTextDigest(field.text) == field.was {
			continue
		}
		changed = true
		text := strings.TrimSpace(field.text)
		if text == "" {
			text = "(now empty)"
		}
		fmt.Fprintf(&section, "## The %s now\n\n%s\n\n", field.name, firstBytes(text, maxRepairItemFieldBytes))
	}
	if a.state.ItemAtStart != nil {
		if changed {
			section.WriteString("Each part above replaces what you were given when the run started. Every other part of the item is unchanged. Where a finding asks for something the item no longer asks for, the item as it stands now governs; say so in your summary.\n\n")
		} else {
			section.WriteString("Its title, description, design guidance and acceptance criteria are as they were when the run started.\n\n")
		}
	}
	notes, kept := start.NotesSince(a.item.Notes)
	if a.state.ItemAtStart != nil && !kept {
		section.WriteString("The notes the item had when the run started are no longer there unchanged, so every note it has now is given.\n\n")
	}
	switch {
	case notes != "" && a.state.ItemAtStart != nil && kept:
		fmt.Fprintf(&section, "## Notes added since the run started\n\n%s", lastBytes(notes, maxRepairNotesBytes))
	case notes != "":
		fmt.Fprintf(&section, "## The item's notes\n\n%s", lastBytes(notes, maxRepairNotesBytes))
	case a.state.ItemAtStart != nil && kept:
		section.WriteString("No notes were added since the run started.")
	default:
		section.WriteString("The item has no notes.")
	}
	return strings.TrimRight(section.String(), "\n")
}

// firstBytes is the beginning of text within limit, saying what it left out.
func firstBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n[The harness cut this at its limit of %d bytes; the last %d bytes are left out.]", text[:cut], limit, len(text)-cut)
}

// lastBytes is the end of text within limit, saying what it left out.
func lastBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := len(text) - limit
	for cut < len(text) && !utf8.RuneStart(text[cut]) {
		cut++
	}
	return fmt.Sprintf("[The harness gives the latest %d bytes of these notes, which is its limit; the %d bytes before them are left out.]\n%s", limit, cut, text[cut:])
}

func oneLineOf(err error) string {
	return singleLine(err.Error(), 500)
}

// itemAtStart records what an item says as a run is started on it.
func itemAtStart(item beads.WorkItem) *runstate.ItemAtStart {
	return runstate.NewItemAtStart(item.Title, item.Description, item.Design, item.AcceptanceCriteria, item.Notes)
}
