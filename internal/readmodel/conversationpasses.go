package readmodel

// What the recurring passes did with the work waiting in their roles'
// conversations, read from the pass records for `yoyo status`, the item card,
// and `yoyo status <item>`.
//
// Each pass records the items it was handed in the order it was handed them and
// which of them it took (orchestrator/passedover.go). The question these answer
// is the one nobody could answer when the record did not carry that: is the
// item first in a role's order being reached, and if not, are the passes
// running, failing, or taking something else. Nothing here reads the tracker;
// what a pass was handed is what its record says it was handed.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ConversationPass is one recurring task's latest answered pass over the work
// waiting in its role's conversation: when it ran, how many items it was
// handed, the item first in its order and whether it took it, and what became
// of any firing of the task after it that did not answer.
type ConversationPass struct {
	Task string           `json:"task"`
	Role domain.AgentRole `json:"role"`
	At   time.Time        `json:"at"`
	// Handed is how many items the pass was handed, and First the one first in
	// its order; First is empty where it was handed none.
	Handed int                     `json:"handed"`
	First  *runstate.DeliveredWork `json:"first,omitempty"`
	// Took is how many of the items it was handed the pass took.
	Took int `json:"took"`
	// Since is the task's most recent firing where it came after this pass and
	// gave no account: missed, failed before its first turn, or failed after
	// it. It is what tells a role whose passes stopped answering from one that
	// answered and took something else. Empty where nothing came after.
	Since string `json:"since,omitempty"`
}

// ReadConversationPasses is one entry per recurring task whose passes have
// ever recorded the work they were handed, sorted by task. A task whose records
// never carried it — a role with no conversation work, or a log written before
// passes recorded it — has nothing to say here and is left out.
func ReadConversationPasses(sources Sources) ([]ConversationPass, string) {
	if sources.Sweeps == nil {
		return nil, ""
	}
	recorded, _, err := sources.Sweeps.List()
	problem := ""
	if err != nil {
		problem = fmt.Sprintf("the recurring passes' records could not be read to the end, so what a pass was last handed may be older than shown: %v", err)
	}
	return ConversationPasses(recorded), problem
}

// ConversationPasses derives the entries from the pass records, as
// ReadConversationPasses says.
func ConversationPasses(recorded []runstate.Sweep) []ConversationPass {
	byTask := map[string][]runstate.Sweep{}
	handedEver := map[string]bool{}
	for _, pass := range recorded {
		if pass.Role == "" || pass.Agent != "" || pass.HarnessPass() {
			continue
		}
		byTask[pass.Task] = append(byTask[pass.Task], pass)
		if len(pass.Delivered) > 0 {
			handedEver[pass.Task] = true
		}
	}
	var entries []ConversationPass
	for task, passes := range byTask {
		if !handedEver[task] {
			continue
		}
		sort.SliceStable(passes, func(i, j int) bool { return passes[i].StartedAt.Before(passes[j].StartedAt) })
		last, found := latestAnswered(passes)
		if !found {
			continue
		}
		entry := ConversationPass{Task: task, Role: last.Role, At: last.StartedAt, Handed: len(last.Delivered)}
		if first, handed := last.FirstDelivered(); handed {
			entry.First = &first
		}
		for _, item := range last.Delivered {
			if item.Taken {
				entry.Took++
			}
		}
		if latest := passes[len(passes)-1]; latest.StartedAt.After(last.StartedAt) {
			entry.Since = fmt.Sprintf("its latest firing, at %s, %s", localMoment(latest.StartedAt), unanswered(latest))
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Task < entries[j].Task })
	return entries
}

// latestAnswered is the last pass that took a turn and gave its account, which
// is the only kind whose record says what it took.
func latestAnswered(passes []runstate.Sweep) (runstate.Sweep, bool) {
	for i := len(passes) - 1; i >= 0; i-- {
		if passes[i].Turns > 0 && passes[i].Result != nil {
			return passes[i], true
		}
	}
	return runstate.Sweep{}, false
}

// unanswered says what became of a firing that gave no account.
func unanswered(pass runstate.Sweep) string {
	switch {
	case pass.IsMiss():
		return "was missed"
	case pass.NotStarted != "":
		return "failed before its first turn"
	case pass.Turns == 0:
		return "took no turn"
	default:
		return "failed without giving its account"
	}
}

// Says is the entry as `yoyo status` prints it.
func (p ConversationPass) Says() string {
	var said strings.Builder
	fmt.Fprintf(&said, "%s (%s) — its last answered pass, at %s, ", p.Task, p.Role.Title(), localMoment(p.At))
	if p.First == nil {
		said.WriteString("was handed no waiting work")
	} else {
		fmt.Fprintf(&said, "was handed %s; first in its order: %s (P%d), %s", count(p.Handed, "item"), p.First.ID, p.First.Priority, takenSays(*p.First))
		if p.Handed > 1 {
			fmt.Fprintf(&said, "; it took %d of the %d", p.Took, p.Handed)
		}
	}
	if p.Since != "" {
		said.WriteString("; " + p.Since)
	}
	return said.String()
}

// takenSays is whether a pass took an item, in capitals where it did not, so
// the distinction survives a terminal that renders no emphasis.
func takenSays(item runstate.DeliveredWork) string {
	if item.Taken {
		return "taken"
	}
	reason := item.Reason
	if reason == "" {
		reason = "no reason given"
	}
	return "NOT TAKEN — " + reason
}

// RenderConversationPasses is the passes' entries under the four lines, and
// nothing where no task has recorded what it was handed.
func (s Standing) RenderConversationPasses() string {
	if len(s.ConversationPasses) == 0 && s.ConversationPassesProblem == "" {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "Recurring passes over conversation work (%d):\n", len(s.ConversationPasses))
	for _, pass := range s.ConversationPasses {
		fmt.Fprintf(&rendered, "  %s\n", pass.Says())
	}
	if s.ConversationPassesProblem != "" {
		rendered.WriteString(partialRead + s.ConversationPassesProblem + "\n")
	}
	return s.wording.Render(s.Titles.Cite(rendered.String()))
}

// ItemConsideration is the last recurring pass that was handed one item:
// which task and role, when, where the item stood in what it was handed, and
// whether the pass took it.
type ItemConsideration struct {
	Task     string           `json:"task"`
	Role     domain.AgentRole `json:"role"`
	At       time.Time        `json:"at"`
	Position int              `json:"position"`
	Of       int              `json:"of"`
	Taken    bool             `json:"taken"`
	// Answered is false where the pass gave no account, and then whether it
	// took the item is not known beyond the tracker actions it took.
	Answered bool   `json:"answered"`
	Reason   string `json:"reason,omitempty"`
	// Says is the line every surface prints, so the card and the terminal say
	// one thing about one pass.
	Says string `json:"says"`
}

// LastConsidered is the latest pass whose record says it was handed the item,
// and false where none does.
func LastConsidered(recorded []runstate.Sweep, id string) (ItemConsideration, bool) {
	var found ItemConsideration
	ok := false
	for _, pass := range recorded {
		if ok && !pass.StartedAt.After(found.At) {
			continue
		}
		for index, item := range pass.Delivered {
			if item.ID != id {
				continue
			}
			found = ItemConsideration{
				Task: pass.Task, Role: pass.Role, At: pass.StartedAt,
				Position: index + 1, Of: len(pass.Delivered),
				Taken: item.Taken, Answered: pass.Turns > 0 && pass.Result != nil, Reason: item.Reason,
			}
			ok = true
			break
		}
	}
	return found, ok
}

// ReadLastConsidered reads the pass records for one item's last consideration.
// A log that could not be read to the end still answers from what was read,
// and says so beside it.
func ReadLastConsidered(passes Sweeps, id string) (*ItemConsideration, string) {
	if passes == nil {
		return nil, ""
	}
	recorded, _, err := passes.List()
	problem := ""
	if err != nil {
		problem = fmt.Sprintf("the recurring passes' records could not be read to the end, so a later pass may have considered this item: %v", err)
	}
	considered, found := LastConsidered(recorded, id)
	if !found {
		return nil, problem
	}
	considered.Says = considered.Sentence()
	return &considered, problem
}

// Sentence is the consideration as one line.
func (c ItemConsideration) Sentence() string {
	where := fmt.Sprintf("%s of the %d items it was handed", ordinal(c.Position), c.Of)
	if c.Of == 1 {
		where = "the only item it was handed"
	}
	said := fmt.Sprintf("last considered by the %s's recurring pass %s at %s, %s", c.Role.Title(), c.Task, localMoment(c.At), where)
	switch {
	case c.Taken:
		return said + "; it took this item"
	case !c.Answered:
		return said + "; the pass gave no account, so whether it took this item is not recorded"
	}
	return said + "; it did NOT take this item — " + takenReason(c.Reason)
}

func takenReason(reason string) string {
	if reason == "" {
		return "no reason given"
	}
	return reason
}

func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return fmt.Sprintf("%d%s", n, suffix)
}
