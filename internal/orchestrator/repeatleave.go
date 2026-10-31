package orchestrator

// What happens when a role's recurring passes keep leaving the item first in
// their order with nothing outside the role holding it.
//
// Every pass that answers with its account and leaves the item first in its
// order records, on that item, how many of the task's passes in a row have
// left it, this one included (runstate.DeliveredWork.LeftInARow), and the
// reason each gave is on each pass's record and, once per reason, in a note on
// the item (notePassedOver). Only an item nothing outside the role holds is counted: one the
// tracker says depends on unfinished work or is blocked carries what holds it
// on the record instead (DeliveredWork.HeldOutside), may be left as often as it
// is held, and breaks the count. A parked item is never handed to a pass at
// all. The count is of passes rather than of identical reasons, because a role
// that rewords the same reason has still left the same item.
//
// Once runstate.LeftInARowBeforeSettling passes in a row have left an item,
// the next pass is told so plainly at its start, with the reasons, and asked to
// decide the item with its assumptions written down or to put one question
// about it to whoever can answer. A pass told that and still ending with the
// item left is sent back once, in a further turn of the same pass, to do one of
// the two; everything else the pass did stands. A pass that still leaves it is
// never failed for it: the record and the note on the item say plainly that it
// left the item again, and the development manager's next pass is told
// (leftAgainMessage).
//
// A decision or a question about the item ends the count, as taking it any
// other way does. Passes that go on leaving it afterwards are counted again
// from there, so a question excuses the item for two passes at most: the
// role's own word that it waits on an answer is not something the tracker
// records, and the next pass after two more leaves is told again.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// leaveStreak is the item first in a pass's order, as the task's earlier
// passes left it: how many of them in a row, and the reason each gave, oldest
// first. pressed marks a pass told to decide it or ask, and sentBack one that
// was then sent back to.
type leaveStreak struct {
	item     string
	earlier  int
	reasons  []string
	pressed  bool
	sentBack bool
}

// earlierLeaves reads the task's passes, newest first, for how many in a row
// left id first in their order with nothing outside the role holding it. A pass
// that gave no account neither left the item nor took it, and is stepped over.
func earlierLeaves(earlier []runstate.Sweep, id string) (int, []string) {
	var reasons []string
	for i := len(earlier) - 1; i >= 0; i-- {
		pass := earlier[i]
		if pass.Turns == 0 || pass.Result == nil {
			continue
		}
		first, handed := pass.FirstDelivered()
		if !handed || first.ID != id || first.HeldOutside != "" || first.Taken {
			break
		}
		reasons = append([]string{first.Reason}, reasons...)
	}
	return len(reasons), reasons
}

// streakFor is the leave count for the item first in what this pass was
// handed, and whether the pass is to be told to decide it or ask.
func streakFor(earlier []runstate.Sweep, delivered []runstate.DeliveredWork) *leaveStreak {
	if len(delivered) == 0 || delivered[0].HeldOutside != "" {
		return nil
	}
	times, reasons := earlierLeaves(earlier, delivered[0].ID)
	return &leaveStreak{item: delivered[0].ID, earlier: times, reasons: reasons, pressed: times >= runstate.LeftInARowBeforeSettling}
}

// message is what the pass is told at its start, and empty where it is not
// told anything.
func (s *leaveStreak) message() string {
	if s == nil || !s.pressed {
		return ""
	}
	return sweep.SettleContract(s.item, s.earlier, s.reasons)
}

// sendBack is the message that sends a pass told to decide or ask back to do
// it, where the pass is about to end having left the item; and empty where it
// did one of the two, took the item some other way, or was already sent back.
func (s *leaveStreak) sendBack(actedOn []string, heldForSending map[string]string, account *sweep.Result) string {
	if s == nil || !s.pressed || s.sentBack || account == nil {
		return ""
	}
	if judged := judgeDelivered([]runstate.DeliveredWork{{ID: s.item}}, actedOn, heldForSending, account); judged[0].Taken {
		return ""
	}
	s.sentBack = true
	return sweep.SentBack(s.item, s.earlier)
}

// record writes the count onto the judged first item, and returns what the
// pass's record says where a pass told to decide or ask left the item anyway.
func (s *leaveStreak) record(recorded *runstate.Sweep, name string) string {
	if s == nil || recorded.Turns == 0 || recorded.Result == nil || len(recorded.Delivered) == 0 || recorded.Delivered[0].ID != s.item {
		return ""
	}
	first := &recorded.Delivered[0]
	first.SentBack = s.sentBack
	if first.Taken {
		return ""
	}
	first.LeftInARow = s.earlier + 1
	if !s.pressed {
		return ""
	}
	return fmt.Sprintf("the pass of %s left %s for the %s time in a row with nothing outside the %s's role holding it, after it was told at its start to decide it or ask one question about it%s; everything else the pass did stands, the item's notes say so, and the development manager is told at that role's next pass",
		name, s.item, ordinal(first.LeftInARow), recorded.Role.Title(), sentBackSays(s.sentBack))
}

func sentBackSays(sentBack bool) string {
	if sentBack {
		return " and sent back once to do it"
	}
	return ""
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

// noteSettled writes on each item the pass decided or asked about which of the
// two it did, and returns what the pass's record should say where a note could
// not be written.
func (t Trigger) noteSettled(ctx context.Context, pass string, recorded runstate.Sweep) string {
	if t.ItemNotes == nil || recorded.Result == nil {
		return ""
	}
	var problems []string
	for _, item := range recorded.Delivered {
		if item.Settled == "" {
			continue
		}
		settled, found := recorded.Result.SettledItem(item.ID)
		if !found {
			continue
		}
		write, stopWriting := trackerRecordContext(ctx)
		err := t.ItemNotes.AppendNote(write, item.ID, settledNote(pass, recorded.Role, recorded.StartedAt, settled))
		stopWriting()
		if err != nil {
			problems = append(problems, fmt.Sprintf("the note saying what the pass did about %s could not be written on the item: %v", item.ID, err))
		}
	}
	return strings.Join(problems, "; ")
}

// settledNote is the note an item carries when a pass told to decide it or ask
// about it did one of the two: which, and the decision with its assumptions or
// the question and who it was put to.
func settledNote(pass string, role domain.AgentRole, at time.Time, settled sweep.Settled) string {
	when := at.Local().Format("15:04 MST on January 2, 2006")
	if settled.Decided() {
		var assumptions []string
		for _, assumption := range settled.Assumptions {
			assumptions = append(assumptions, strings.TrimSuffix(strings.Join(strings.Fields(assumption), " "), "."))
		}
		return fmt.Sprintf("Decided by the %s's recurring pass %s at %s, after earlier passes left this item: %s. Assumptions it made: %s.",
			role.Title(), pass, when, strings.TrimSuffix(boundedLeftReason(settled.Decision), "."), strings.Join(assumptions, "; "))
	}
	return fmt.Sprintf("Asked about by the %s's recurring pass %s at %s, after earlier passes left this item: it put one question to %s: %s",
		role.Title(), pass, when, boundedLeftReason(settled.AskedOf), boundedLeftReason(settled.Question))
}

// leftAgain is an item a role's latest answered pass left with nothing outside
// the role holding it after being told to decide it or ask about it.
type leftAgain struct {
	task   string
	role   domain.AgentRole
	item   string
	times  int
	at     time.Time
	reason string
}

// itemsLeftAgain reads, for each recurring task over a role's conversation
// work, whether its latest answered pass left the item first in its order more
// times in a row than a pass may before it is told to decide or ask. An entry
// stands until a later pass of the task takes the item or puts it elsewhere in
// the order, so the development manager is told on every pass until then.
func itemsLeftAgain(recorded []runstate.Sweep) []leftAgain {
	latest := map[string]runstate.Sweep{}
	for _, pass := range recorded {
		if pass.Role == "" || pass.Agent != "" || pass.Turns == 0 || pass.Result == nil {
			continue
		}
		if seen, found := latest[pass.Task]; !found || !pass.StartedAt.Before(seen.StartedAt) {
			latest[pass.Task] = pass
		}
	}
	var stuck []leftAgain
	for task, pass := range latest {
		first, handed := pass.FirstDelivered()
		if !handed || first.Taken || first.LeftInARow <= runstate.LeftInARowBeforeSettling {
			continue
		}
		stuck = append(stuck, leftAgain{task: task, role: pass.Role, item: first.ID, times: first.LeftInARow, at: pass.StartedAt, reason: first.Reason})
	}
	sort.Slice(stuck, func(i, j int) bool { return stuck[i].task < stuck[j].task })
	return stuck
}

// leftAgainMessage is what the development manager's pass is told of them.
func leftAgainMessage(stuck []leftAgain) string {
	if len(stuck) == 0 {
		return ""
	}
	lines := []string{"Items a role's recurring passes keep leaving. Each was left first in that role's order by this many of its passes in a row, with nothing outside the role holding it, and its latest pass left it again after being told to decide it or ask one question about it. Deciding what becomes of each is yours, as for any work that is not moving:"}
	for _, entry := range stuck {
		reason := entry.reason
		if reason == "" {
			reason = "no reason given"
		}
		lines = append(lines, fmt.Sprintf("- %s: left %d times in a row by the %s's recurring pass %s, most recently at %s. Its reason: %s",
			entry.item, entry.times, entry.role.Title(), entry.task, entry.at.Local().Format("15:04 MST on January 2, 2006"), reason))
	}
	return strings.Join(lines, "\n")
}

// sentBackProblem is what the pass's record says of a turn that ended with the
// item still left and was followed by one sending the pass back to it.
func sentBackProblem(name string, turn int, item string) string {
	return fmt.Sprintf("turn %d of the recurring task %s ended with %s left again after the pass was told to decide it or ask one question about it, so the harness sent the pass back once to do one of the two, keeping everything else it did", turn, name, item)
}
