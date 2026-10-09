package readmodel

// The four lines, as they are printed.
//
// This is the one place the ratified format is written down. It is here rather
// than in the CLI because the format is the contract: the same four lines are
// printed in a terminal and said in a channel, and two renderings of one
// standing is exactly the disagreement an operator would then have to
// adjudicate.
//
// Three rules hold every line, and each of them exists because its opposite
// happened. Every line is always printed, so silence never has to be
// interpreted. A line with nothing in it says "nothing" in words, because a
// blank reads as a bug in the printing rather than as an empty state. A line
// whose source could not be read says so instead of saying "nothing", because a
// confident emptiness assembled from a file nobody could open is the worst
// answer this could give.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// maxListed bounds how many entries one line names before it counts the rest.
// The counts stay exact either way: what a status owes a reader is what is
// happening, not an export of the queue.
const maxListed = 10

// partialRead opens the line a count carries when its source answered in part.
// It is a constant because two things read it: the renderers that write it, and
// the brief rendering that keeps it while dropping every other indented line.
const partialRead = "  not fully read: "

// tally opens a line that counts part of a line rather than naming one entry of
// it: the not-startable line's ready work waiting for a slot, its groups by what
// they wait on, and whether any of it is the operator's. The brief rendering
// keeps these with the head, because a count by who moves it is exactly what the
// hourly message has to carry and what one figure over all of it hid.
const tally = "  - "

// Render is the four lines. It is deterministic, so the same standing always
// prints the same text, and it always returns exactly four labelled lines with
// whatever they carry indented under them.
//
// One thing can come above them, and only one: the harness being paused on the
// provider's usage window. It is a banner rather than a fifth line — the four
// are unchanged and are still all printed every time — and it is there because
// the operator asked that the cause be the first words of any message that
// reaches him when the system is paused on a window. Nothing else is ever put
// above them: a state that will not render into the four is a bug in the state.
func (s Standing) Render() string {
	if s.Paused == "" {
		return s.RenderLines()
	}
	return s.wording.Render(s.Titles.Cite(s.Paused)) + "\n" + s.RenderLines()
}

// RenderLines is the four lines without the banner, for the one caller that has
// already said what the banner says: the channel's own message about the
// provider's usage window, which opens with that sentence and then shows where
// the harness stands underneath it. Saying it twice in one message is repetition
// rather than emphasis, and every other caller wants Render.
func (s Standing) RenderLines() string {
	var rendered strings.Builder
	rendered.WriteString(s.renderRunning())
	rendered.WriteString(s.renderPaused())
	rendered.WriteString(s.renderWorking())
	rendered.WriteString(s.renderNotStartable())
	rendered.WriteString(s.renderNeedsHuman())
	return s.wording.Render(s.Titles.Cite(rendered.String()))
}

// RenderBrief is the same four lines with the queues counted and not listed, and
// it is what a message nobody asked for carries.
//
// It exists because pushing and answering are different acts. A person at a
// terminal typed `yoyo status`, and what they want is every run with its phase,
// its age and what it has spent — the whole point of having asked. A message
// that arrives on its own hour after hour is read by somebody who did not ask
// anything, and the operator's standard for those is an exec's: brief, and about
// what needs a decision. Under it an enumerated queue is a screen of detail
// nobody requested, repeated every hour, in front of the one sentence that says
// the line has stopped.
//
// It is not a second rendering of the standing. The labels, the words, the
// counts and the stated absences are the ones above and come from the same
// derivation; what it drops is the entries under a line and nothing else, so the
// two can differ in how much they say and never in what they say. The banner is
// carried for the reason Render carries it: the operator asked that a pause name
// its cause in the first words of any message that reaches him.
func (s Standing) RenderBrief() string {
	if s.Paused == "" {
		return s.RenderBriefLines()
	}
	return s.wording.Render(s.Titles.Cite(s.Paused)) + "\n" + s.RenderBriefLines()
}

// RenderBriefLines is the brief four lines without the banner, for the caller
// that has already said what the banner says. It stands to RenderBrief exactly as
// RenderLines stands to Render, and for the same one caller: the channel's
// message about the provider's usage window opens with that sentence.
func (s Standing) RenderBriefLines() string {
	var rendered strings.Builder
	rendered.WriteString(brief(s.renderRunning()))
	if paused := s.renderPaused(); paused != "" {
		rendered.WriteString(brief(paused))
	}
	rendered.WriteString(brief(s.renderWorking()))
	rendered.WriteString(brief(s.renderNotStartable()))
	rendered.WriteString(brief(s.renderNeedsHuman()))
	return s.wording.Render(s.Titles.Cite(rendered.String()))
}

// brief is one rendered line with the entries under it dropped. It reads the
// full rendering rather than re-deriving a short one, which is what makes the
// two renderings incapable of disagreeing: the line a reader sees here is
// character for character the line the terminal prints, and what is removed is
// only what was indented under it.
//
// The trailing colon goes with them. "Running (2 developer runs):" promises a
// list that is not there, and a promise a message does not keep reads as
// something having been lost rather than as something having been left out.
//
// A line that is not indented is a head of its own and survives with the first:
// the fourth line prints one for each mover other than the operator, and the
// head is what says how much waits on each.
//
// One indented line survives, and it is the one that is not an entry: a count
// assembled from a source that could not be fully read says so under itself, and
// a brief rendering that dropped the caveat and kept the number would be the
// confident emptiness this format exists to refuse.
func brief(rendered string) string {
	lines := strings.SplitAfter(rendered, "\n")
	head, rest := lines[0], lines[1:]
	kept := strings.TrimSuffix(strings.TrimSuffix(head, "\n"), ":") + "\n"
	for _, line := range rest {
		switch {
		case strings.HasPrefix(line, partialRead), strings.HasPrefix(line, tally):
			kept += line
		case line != "" && !strings.HasPrefix(line, "  "):
			// A second head under the first — the fourth line's head for each
			// mover other than the operator — is a count rather than an entry,
			// and is kept as the first head is.
			kept += strings.TrimSuffix(strings.TrimSuffix(line, "\n"), ":") + "\n"
		}
	}
	return kept
}

func (s Standing) renderRunning() string {
	if s.RunningProblem != "" {
		return unreadable("Running", s.RunningProblem)
	}
	var rendered strings.Builder
	// A dispatch waiting out the tracker before it claims anything holds a slot
	// with no run record, so it is said in the head of the line as well as under
	// it: the brief rendering the channel carries keeps only the head, and a slot
	// held for two hours by a line that read "nothing" is the hang this is here to
	// tell apart from a wait.
	waiting := ""
	if len(s.Dispatching) > 0 {
		waiting = dispatches(len(s.Dispatching)) + " waiting out a tracker failure before claiming anything"
	}
	switch {
	case len(s.Running) == 0 && waiting == "":
		rendered.WriteString("Running: nothing\n")
	case len(s.Running) == 0:
		fmt.Fprintf(&rendered, "Running: no run yet, and %s:\n", waiting)
	case waiting == "":
		fmt.Fprintf(&rendered, "Running (%s%s):\n", count(len(s.Running), "developer run"), withNoProcess(s.Running))
	default:
		fmt.Fprintf(&rendered, "Running (%s%s, and %s):\n", count(len(s.Running), "developer run"), withNoProcess(s.Running), waiting)
	}
	if len(s.Running) > 0 {
		listed, further := bound(len(s.Running))
		for _, run := range s.Running[:listed] {
			fmt.Fprintf(&rendered, "  %s — %s%s, %s elapsed, %s%s\n",
				run.WorkItemID, phaseOf(run), atEffort(run.Model, run.Effort), age(run.Elapsed), spendOf(run), slotOf(run))
		}
		rendered.WriteString(remainder(further, "developer run"))
	}
	listed, further := bound(len(s.Dispatching))
	for _, wait := range s.Dispatching[:listed] {
		fmt.Fprintf(&rendered, "  %s\n", wait.Says())
	}
	if further > 0 {
		fmt.Fprintf(&rendered, "  and %s not named here\n", dispatches(further))
	}
	if s.DispatchingProblem != "" {
		fmt.Fprintf(&rendered, "%s%s\n", partialRead, s.DispatchingProblem)
	}
	// The slots with nothing in them, each with what it prefers, said under the
	// runs where some slot prefers a label. A free slot that prefers a label is
	// the one fact about capacity a reader cannot infer from the runs: it says
	// what the next pull will look for first. Nothing is said where no slot
	// prefers anything, so the line reads as it always did.
	for _, slot := range s.DeveloperSlots {
		if slot.Free() {
			fmt.Fprintf(&rendered, "  developer slot %d is free and prefers %s\n", slot.Number, slot.Preference())
		}
	}
	return rendered.String()
}

// withNoProcess is how many of the runs in flight have no process behind them,
// said in the head of the line because the head is what the channel carries: a
// run holding a slot with nothing working on it is the one entry a reader of the
// brief rendering must not be left to think is running. It is empty where every
// run has a process.
func withNoProcess(running []RunningRun) string {
	missing := 0
	for _, run := range running {
		if run.NoProcess != "" {
			missing++
		}
	}
	if missing == 0 {
		return ""
	}
	return fmt.Sprintf(", %d with no process behind it", missing)
}

// renderPaused is the runs paused on work their items wait on, and nothing
// where there are none. They are said on a line of their own rather than on the
// running line because they hold no developer slot: a run counted there would be
// a slot said to be taken that the next pull fills.
func (s Standing) renderPaused() string {
	if len(s.PausedRuns) == 0 {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "Paused, holding no developer slot (%s):\n", count(len(s.PausedRuns), "developer run"))
	listed, further := bound(len(s.PausedRuns))
	for _, run := range s.PausedRuns[:listed] {
		fmt.Fprintf(&rendered, "  %s — waiting on unfinished work it depends on: %s, paused %s\n",
			run.WorkItemID, run.WaitingOn, pausedAgo(s.ObservedAt, run.PausedSince))
	}
	rendered.WriteString(remainder(further, "developer run"))
	return rendered.String()
}

// pausedAgo is how long a paused run has been waiting, said as an age, or that
// its record does not say where it carries no time.
func pausedAgo(now, since time.Time) string {
	if since.IsZero() {
		return "at a time its record does not say"
	}
	return age(now.Sub(since)) + " ago"
}

// dispatches counts dispatches, which count cannot: its plural is the noun with
// an s on it.
func dispatches(number int) string {
	if number == 1 {
		return "1 dispatch"
	}
	return fmt.Sprintf("%d dispatches", number)
}

// slotOf is which developer slot a run occupies, and what that slot prefers,
// said after the spend where some configured slot prefers a label. It is empty
// where none does — a project that configured no preference reads exactly as it
// did — and for a run in flight beyond the configured capacity, which no slot
// holds.
func slotOf(run RunningRun) string {
	if run.Slot == 0 {
		return ""
	}
	return ", in " + (DeveloperSlotStanding{Number: run.Slot, Preferred: run.SlotPrefers}).Says()
}

func (s Standing) renderWorking() string {
	if s.WorkingProblem != "" && len(s.Working) == 0 {
		return unreadable("Working", s.WorkingProblem) + s.renderWaitingTurns()
	}
	var rendered strings.Builder
	if len(s.Working) == 0 {
		if len(s.Waiting) == 0 {
			rendered.WriteString("Working: nothing\n")
		} else {
			fmt.Fprintf(&rendered, "Working (%s waiting):\n", count(len(s.Waiting), "conversation turn"))
		}
	} else {
		waiting := ""
		if len(s.Waiting) > 0 {
			waiting = "; " + count(len(s.Waiting), "turn") + " waiting"
		}
		fmt.Fprintf(&rendered, "Working (%s%s):\n", count(len(s.Working), "conversation"), waiting)
		listed, further := bound(len(s.Working))
		for _, turn := range s.Working[:listed] {
			fmt.Fprintf(&rendered, "  %s — %s%s, a turn in flight for %s after %s\n",
				turn.Agent, turn.Role, atEffort(turn.Model, turn.Effort), age(turn.Elapsed), count(turn.Turns, "recorded turn"))
		}
		rendered.WriteString(remainder(further, "conversation"))
	}
	// A partial answer is said under the count rather than in place of it: the
	// conversations that did answer are still worth reporting, and a count nobody
	// was told was partial is a count somebody trusts.
	if s.WorkingProblem != "" {
		fmt.Fprintf(&rendered, "%s%s\n", partialRead, s.WorkingProblem)
	}
	return rendered.String() + s.renderWaitingTurns()
}

func (s Standing) renderWaitingTurns() string {
	var rendered strings.Builder
	if len(s.Waiting) > 0 {
		listed, further := bound(len(s.Waiting))
		for _, wait := range s.Waiting[:listed] {
			fmt.Fprintf(&rendered, "  %s — conversation %s, process %d: %s; this turn releases the conversation while it waits\n",
				wait.Agent, wait.ConversationID, wait.PID, wait.Reason)
		}
		rendered.WriteString(remainder(further, "waiting turn"))
	}
	if s.WaitingProblem != "" {
		fmt.Fprintf(&rendered, "%s%s\n", partialRead, s.WaitingProblem)
	}
	return rendered.String()
}

// renderNotStartable is the not-startable line: its head, then the ready work
// waiting only for a developer slot, which is counted apart and never in the
// head's figure, then the refused work counted by what it waits on with the next
// step and whose it is, then one sentence on whether any of it is the
// operator's, and then the items themselves.
func (s Standing) renderNotStartable() string {
	if s.NotStartableProblem != "" && len(s.NotStartable) == 0 && s.WaitingForSlot == nil {
		return unreadable("Not startable", s.NotStartableProblem)
	}
	var rendered strings.Builder
	if len(s.NotStartable) == 0 {
		fmt.Fprintf(&rendered, "Not startable: nothing, of %s\n", count(s.Admitted, "admitted item"))
	} else {
		fmt.Fprintf(&rendered, "Not startable (%d of %s%s):\n",
			len(s.NotStartable), count(s.Admitted, "admitted item"), s.heldSplit())
	}
	if s.WaitingForSlot != nil {
		fmt.Fprintf(&rendered, "%s%s — %s\n", tally, s.WaitingForSlot.Says(), slotWaitNext)
	}
	for _, group := range s.NotStartableGroups {
		fmt.Fprintf(&rendered, "%s%s\n", tally, group.Says())
	}
	if s.NotStartableForOperator != "" && (len(s.NotStartable) > 0 || s.WaitingForSlot != nil) {
		fmt.Fprintf(&rendered, "%s%s\n", tally, s.NotStartableForOperator)
	}
	if len(s.NotStartable) > 0 {
		listed, further := bound(len(s.NotStartable))
		for _, refused := range s.NotStartable[:listed] {
			fmt.Fprintf(&rendered, "  %s — %s%s\n", refused.WorkItemID, heldSince(refused.HeldSince, s.ObservedAt), refused.Reason)
		}
		rendered.WriteString(remainder(further, "refused item"))
	}
	if s.NotStartableProblem != "" {
		fmt.Fprintf(&rendered, "%s%s\n", partialRead, s.NotStartableProblem)
	}
	return rendered.String()
}

// renderNeedsHuman is the fourth line. It lists only what waits on the
// operator, because the operator is the one human the line is named for:
// under the operator's ruling of 2026-09-26 he moves only a change to the
// fundamental goals, and a line that listed sixty things as needing a human
// while three of them were his told him something false about the rest. What
// waits on a role, the harness, the forge, or nobody is printed under it, one
// headed line per mover in the movers' order, each head counting its entries,
// so every entry is still printed and none of them is said to need a person.
func (s Standing) renderNeedsHuman() string {
	if s.NeedsHumanProblem != "" && len(s.NeedsHuman) == 0 {
		return unreadable("Needs a human", s.NeedsHumanProblem)
	}
	byMover := map[Mover][]Attention{}
	order := Movers()
	for _, waiting := range s.NeedsHuman {
		if _, seen := byMover[waiting.Mover]; !seen && !waiting.Mover.Valid() {
			order = append(order, waiting.Mover)
		}
		byMover[waiting.Mover] = append(byMover[waiting.Mover], waiting)
	}
	var rendered strings.Builder
	operator := byMover[MoverOperator]
	if len(operator) == 0 {
		rendered.WriteString("Needs a human: nothing\n")
	} else {
		fmt.Fprintf(&rendered, "Needs a human (%d):\n", len(operator))
		rendered.WriteString(renderWaiting(operator))
	}
	// A partial reading qualifies the whole line, every mover's head as well as
	// the operator's, so it is said once under the line's own head and says so.
	if s.NeedsHumanProblem != "" {
		fmt.Fprintf(&rendered, "%s%s (this covers every head of this line)\n", partialRead, s.NeedsHumanProblem)
	}
	for _, mover := range order {
		entries := byMover[mover]
		if mover == MoverOperator || len(entries) == 0 {
			continue
		}
		fmt.Fprintf(&rendered, "%s (%d):\n", mover.WaitingOn(), len(entries))
		rendered.WriteString(renderWaiting(entries))
	}
	return rendered.String()
}

// renderWaiting is the entries under one mover's head. A named entry is
// printed wherever it falls and is never counted into the remainder; the
// bound is spent on the rest. A finding only the operator can act on that
// reached him as "and 3 things not named here" is one that did not reach him.
func renderWaiting(entries []Attention) string {
	var rendered strings.Builder
	listed, further := 0, 0
	for _, waiting := range entries {
		if !waiting.Named() {
			if listed >= maxListed {
				further++
				continue
			}
			listed++
		}
		fmt.Fprintf(&rendered, "  %s — %s\n", waiting.CitedWhat(), waiting.CitedWhose())
	}
	rendered.WriteString(remainder(further, "thing waiting on somebody"))
	return rendered.String()
}

// heldSplit is how much of the not-startable line is held work, said in the head
// and split by whose move it is: items the development manager has still to
// decide about, and items whose decision she recorded and the harness has still
// to act on.
//
// It is in the head rather than under it because the head is the whole of what
// an hourly message carries — the brief rendering drops every entry and keeps
// the heads — so a distinction only the entries made would be invisible in
// exactly the message that woke somebody. It says nothing at all when neither
// kind is present, which is a queue held by dependencies and directives and
// where a clause about triage would be noise.
//
// The decisions the harness has not carried out are counted beside it by what
// became of them — refused by a gate, or never attempted by a pass — whenever
// either kind stands, because the two are fixed in different places and the
// second is the one that used to be invisible.
func (s Standing) heldSplit() string {
	decision := fmt.Sprintf("%d %s the development manager's decision", s.AwaitingDecision, awaits(s.AwaitingDecision))
	carryOut := fmt.Sprintf("%d %s the harness carrying out a decision already recorded", s.AwaitingCarryOut, awaits(s.AwaitingCarryOut))
	var split string
	switch {
	case s.AwaitingDecision > 0 && s.AwaitingCarryOut > 0:
		split = "; " + decision + ", " + carryOut
	case s.AwaitingDecision > 0:
		split = "; " + decision
	case s.AwaitingCarryOut > 0:
		split = "; " + carryOut
	}
	// Waits on named work are said apart from both, because neither person has
	// anything to do about them until that work lands.
	if s.AwaitingWork > 0 {
		split += fmt.Sprintf("; %d %s admitted work the development manager decided to wait for", s.AwaitingWork, awaits(s.AwaitingWork))
	}
	if s.CarryOutsRefused > 0 || s.CarryOutsUnattempted > 0 {
		split += fmt.Sprintf("; decisions not carried out: %d refused, %d unattempted", s.CarryOutsRefused, s.CarryOutsUnattempted)
	}
	return split
}

// unreadable is a line whose source could not be read. It is never "nothing":
// what this says is that the harness does not know, which is a different answer
// and the one a reader has to act on.
func unreadable(label, problem string) string {
	return fmt.Sprintf("%s: could not be read — %s\n", label, problem)
}

// phaseOf is where a run has got to, or the stated absence. A record written
// before the run reached a phase has none, and a blank in the middle of a line
// reads as a bug in the printing.
//
// A run promoting again after the environment stopped it says so in the read
// model's own words rather than as the bare phase: what an operator who signed
// overrides for that stop is reading the line for is that the approval stood
// and nothing was spent.
//
// A run in its checks says where the stage stands rather than the bare phase,
// for the same reason: "checks: 14m of 30m" is what an operator watching a
// slow stage is reading the line for, and the bound is the number that says
// whether it is slow.
//
// A run with no process behind it says that first and in place of the phase,
// because the phase is what the dead process last wrote and reads as work under
// way: run-3b94404c read "checking" in slot 1 for twenty hours on 2026-09-26.
func phaseOf(run RunningRun) string {
	if run.NoProcess != "" {
		recorded := strings.TrimSpace(string(run.Phase))
		if recorded == "" {
			recorded = "no phase"
		}
		said := "no process can be found behind it: " + run.NoProcess + "; recorded as " + recorded
		if run.NoProcessRemedy != "" {
			said += "; " + run.NoProcessRemedy
		}
		return said
	}
	if run.ResumingIntegration {
		return runstate.ResumingIntegrationSays
	}
	if run.Checks != "" {
		return run.Checks
	}
	if run.AfterReply != "" {
		return run.AfterReply
	}
	if strings.TrimSpace(string(run.Phase)) == "" {
		return "no phase recorded yet"
	}
	return string(run.Phase)
}

// spendOf is what a run has spent so far. A run whose evidence cannot be read is
// stated as unpriceable rather than as free, and it says why: a figure of zero
// against an hour of provider work is the one number nobody may print.
func spendOf(run RunningRun) string {
	if run.UnknownCost != "" {
		return "cost unknown (" + run.UnknownCost + ")"
	}
	return run.Tokens.CostText(run.CostUSD) + " so far"
}

// count says a number with the noun it counts, in the three forms that read
// differently: none, one, and several. "0 developer runs" is arithmetic; "no
// developer runs" is an answer.
func count(number int, noun string) string {
	switch number {
	case 0:
		return "no " + noun + "s"
	case 1:
		return "1 " + noun
	default:
		return fmt.Sprintf("%d %ss", number, noun)
	}
}

// bound is how many entries a line names and how many it only counts.
func bound(total int) (int, int) {
	if total <= maxListed {
		return total, 0
	}
	return maxListed, total - maxListed
}

// remainder says what a bounded line did not name. A line that silently stopped
// at ten would read as ten being all there was, which is the truncation that
// makes a status worse than no status.
func remainder(further int, noun string) string {
	if further == 0 {
		return ""
	}
	return fmt.Sprintf("  and %s not named here\n", count(further, noun))
}

// heldSince opens a held item's entry with when it was held and how long ago
// that was, in the reader's own zone, so a stoppage from yesterday and one from
// three weeks ago do not read alike. It is empty for an entry that names no
// moment.
func heldSince(since *time.Time, now time.Time) string {
	if since == nil || since.IsZero() {
		return ""
	}
	return fmt.Sprintf("held since %s, %s; ", localMoment(*since), agoSaid(now.Sub(*since)))
}

// localMoment is a moment as a person reads one: the day and the minute in the
// machine's local zone, with the zone named.
func localMoment(moment time.Time) string {
	return moment.Local().Format("2006-01-02 15:04 MST")
}

// agoSaid is how long ago something happened, in words rather than in the
// compact form the running line uses: a hold is read for whether it has sat for
// hours or for weeks, and "3 days ago" says that without being decoded.
func agoSaid(elapsed time.Duration) string {
	switch {
	case elapsed < 0:
		return "a moment stamped ahead of this reading"
	case elapsed < time.Minute:
		return "less than a minute ago"
	case elapsed < time.Hour:
		return count(int(elapsed.Minutes()), "minute") + " ago"
	case elapsed < 48*time.Hour:
		return count(int(elapsed.Hours()), "hour") + " ago"
	default:
		return count(int(elapsed.Hours())/24, "day") + " ago"
	}
}

// age is an elapsed time as somebody says one. It is coarse on purpose: what a
// status answers is roughly how long this has been going, and a duration printed
// to the nanosecond is a number a reader has to parse before they can read it.
func age(elapsed time.Duration) string {
	switch {
	case elapsed < 0:
		// A record stamped ahead of this reading. It is said as itself rather than
		// as a negative duration, which reads as a bug in the arithmetic.
		return "no time at all; its record is stamped ahead of this reading"
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(elapsed.Hours()), int(elapsed.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(elapsed.Hours())/24, int(elapsed.Hours())%24)
	}
}

// RenderProgramManagers is one line per program manager instance, printed under
// the four lines rather than as a fifth: nothing on it waits on a person, and
// the operator reads an instance when he chooses. Each line names the instance,
// its lane, and its status word, and says why where the word is not working
// or where the instance has missed a pass.
// It is empty where no instance is configured and none has asked for anything,
// and says so where the instances were listed but something behind them could
// not be read.
func (s Standing) RenderProgramManagers() string {
	if len(s.ProgramManagers) == 0 && s.ProgramManagersProblem == "" {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "Program managers (%d):\n", len(s.ProgramManagers))
	for _, instance := range s.ProgramManagers {
		lane := instance.Lane
		if lane == "" {
			lane = "no lane configured"
		} else {
			lane = "lane " + lane
		}
		fmt.Fprintf(&rendered, "  %s — %s — %s%s\n", instance.Agent, lane, instance.Status, instance.why())
	}
	if s.ProgramManagersProblem != "" {
		rendered.WriteString(partialRead + s.ProgramManagersProblem + "\n")
	}
	return s.wording.Render(s.Titles.Cite(rendered.String()))
}

// why is what follows an instance's status word: the reason it is stale, how
// many open asks of its own block it, how many of its report's blockers the
// record does not bear out, and the pass it missed since its last completed
// one. Both halves are said where both hold, because stale outranks blocked in
// the word and the reader should still see the second; a missed pass is said
// under any word, because it is the stall before the instance reads stale.
func (p ProgramManager) why() string {
	var parts []string
	if p.Stale {
		parts = append(parts, p.StaleSays)
	}
	if p.Blocked {
		cited := make([]string, 0, len(p.Blockers))
		for _, blocker := range p.Blockers {
			cited = append(cited, blocker.Cites)
		}
		parts = append(parts, fmt.Sprintf("blocked on %s (%s)", count(len(p.Blockers), "open ask"), strings.Join(cited, ", ")))
	}
	if len(p.Claims) > 0 {
		parts = append(parts, fmt.Sprintf("%s its report names that the record does not bear out", count(len(p.Claims), "blocker")))
	}
	if p.MissedPass != nil {
		parts = append(parts, p.MissedPass.sentence())
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, "; ")
}

// StaleProgramManagersLine is the count the channel's hourly line carries of
// stale instances, and empty where none is stale. It is said only inside a
// message that line already posts for another reason: a stale instance is
// something the operator reviews when he chooses, and a message sent for it
// alone would be the push he asked not to be sent.
func (s Standing) StaleProgramManagersLine() string {
	stale := s.StaleProgramManagers()
	if len(stale) == 0 {
		return ""
	}
	return fmt.Sprintf("Program managers stale: %d of %d (%s)\n", len(stale), len(s.ProgramManagers), strings.Join(stale, ", "))
}

// atEffort is the model a line's invocation asked for and the effort level it
// asked at, said in the words `yoyo agent list` uses, and nothing where no level
// was recorded: a record naming none asked for none, and a line that named the
// model alone would be saying something it never said before this level existed.
func atEffort(model, effort string) string {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return ""
	}
	if model = strings.TrimSpace(model); model == "" {
		return ", at " + effort + " effort"
	}
	return ", on " + model + " at " + effort + " effort"
}
