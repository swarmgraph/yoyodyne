package readmodel

// The held line: the brake's hold on new work as the development manager's
// pass is shown it, with what it is costing.
//
// A brake hold is put to her when it trips, and before this it was put to her
// only then and when a probe blocked. A decision she recorded — a probe, most
// often — took the hold off every later pass of hers, so a probe that neither
// landed nor blocked left the line held with developer slots idle and nobody
// asked about it again. The held line is the hold said on every pass while it
// stands, as a decision to make again whatever she decided before, with the
// three numbers that say what waiting costs: how long it has stood, how many
// developer slots are idle under it, and how long the probe run has been out.
// A probe out past execution.brake_probe_overdue_after is put back on her list
// of stopped runs, because a run that neither lands nor blocks is stopped in
// every sense that matters to the hold.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// HeldLine is a brake hold on new work and what it is costing, as one reading
// saw it.
type HeldLine struct {
	// HeldAt is when the hold was placed, and HeldFor how long it had stood at
	// the reading.
	HeldAt  time.Time     `json:"held_at"`
	HeldFor time.Duration `json:"held_for"`
	// Reason is the hold's own reason, and Standing what happens to it next, in
	// the hold's own words.
	Reason   string `json:"reason,omitempty"`
	Standing string `json:"standing,omitempty"`
	// Capacity is the configured developer slots, Busy the ones a run or a
	// dispatch holds, and Idle the rest: the work the hold is not starting.
	// SlotsProblem is why the busy slots could not be counted, where they could
	// not; Idle is then not stated.
	Capacity     int    `json:"capacity"`
	Busy         int    `json:"busy"`
	Idle         int    `json:"idle"`
	SlotsProblem string `json:"slots_problem,omitempty"`
	// Decision is her last recorded decision about the hold, where she has
	// recorded one, with when and why. It is said so she can see it, never as
	// the reason the hold is not put to her.
	Decision       runstate.IntakeBrakeDecision `json:"decision,omitempty"`
	DecidedAt      *time.Time                   `json:"decided_at,omitempty"`
	DecisionReason string                       `json:"decision_reason,omitempty"`
	// Probe is the probe run in flight under the hold, where one is, and
	// ProbeOut how long it has been out. ProbeOverdue is a probe out longer than
	// OverdueAfter, which is execution.brake_probe_overdue_after as the caller
	// read it; zero puts no probe back.
	Probe        *runstate.IntakeProbe `json:"probe,omitempty"`
	ProbeOut     time.Duration         `json:"probe_out,omitempty"`
	ProbeOverdue bool                  `json:"probe_overdue,omitempty"`
	OverdueAfter time.Duration         `json:"overdue_after,omitempty"`
}

// HeldLineOf reads the held line from a hold, the configured capacity, and the
// developer slots taken, and reports false for anything but a hold the brake
// placed: the operator's own hold is his, and nothing about it is hers to
// decide again. busy is negative where the slots taken could not be counted,
// with slotsProblem saying why.
func HeldLineOf(hold runstate.IntakeHold, capacity, busy int, slotsProblem string, overdueAfter time.Duration, now time.Time) (HeldLine, bool) {
	if !hold.Braked() {
		return HeldLine{}, false
	}
	brake := *hold.Brake
	line := HeldLine{
		HeldAt:         hold.HeldAt,
		HeldFor:        now.Sub(hold.HeldAt),
		Reason:         strings.TrimSpace(hold.Reason),
		Standing:       brake.Standing(),
		Capacity:       capacity,
		Decision:       brake.Decision,
		DecidedAt:      brake.DecidedAt,
		DecisionReason: brake.DecisionReason,
		OverdueAfter:   overdueAfter,
	}
	if busy < 0 || strings.TrimSpace(slotsProblem) != "" {
		line.SlotsProblem = strings.TrimSpace(slotsProblem)
		if line.SlotsProblem == "" {
			line.SlotsProblem = "the runs in flight could not be read"
		}
	} else {
		line.Busy = busy
		line.Idle = max(capacity-busy, 0)
	}
	if brake.Probing() {
		probe := *brake.Probe
		line.Probe = &probe
		line.ProbeOut = now.Sub(probe.StartedAt)
		line.ProbeOverdue = overdueAfter > 0 && line.ProbeOut > overdueAfter
	}
	return line, true
}

// Render is the held line as a section of the development manager's pass: the
// hold put to her as a decision to make on this pass, with its cost.
func (h HeldLine) Render() string {
	var rendered strings.Builder
	rendered.WriteString("## The hold on new work: decide it again on this pass\n\n")
	fmt.Fprintf(&rendered, "New work has been held since %s, for %s: %s.\n", localMoment(h.HeldAt), spanSaid(h.HeldFor), h.Reason)
	if h.SlotsProblem != "" {
		fmt.Fprintf(&rendered, "How many of the %s are idle could not be counted: %s.\n", count(h.Capacity, "developer slot"), h.SlotsProblem)
	} else {
		fmt.Fprintf(&rendered, "%d of %s %s idle under it. A held line costs work every minute it stands: each idle slot is a run that is not happening.\n",
			h.Idle, count(h.Capacity, "developer slot"), isAre(h.Idle))
	}
	if h.Probe != nil {
		fmt.Fprintf(&rendered, "The probe run %s has been out for %s, since %s.\n", h.probeNamed(), spanSaid(h.ProbeOut), localMoment(h.Probe.StartedAt))
	} else {
		rendered.WriteString("No probe run is out.\n")
	}
	fmt.Fprintf(&rendered, "Where it stands: %s.\n", h.Standing)
	if h.Decision != "" && h.DecidedAt != nil {
		decided := fmt.Sprintf("You decided %q at %s", string(h.Decision), localMoment(*h.DecidedAt))
		if reason := strings.TrimSpace(h.DecisionReason); reason != "" {
			decided += ": " + singleLine(reason, maxHeldLineReasonBytes)
		}
		rendered.WriteString(decided + ". That decision does not settle the hold for later passes; decide it again now.\n")
	}
	rendered.WriteString("\nRelease it with {\"action\":\"brake\",\"decision\":\"release\",\"reason\":\"why\"} if the line is fine or what stopped it is dealt with. If it stays held, say in your account what keeps it held and why that is worth the idle slots.\n")
	return rendered.String()
}

// OverdueEntry is the probe run put back on her list of stopped runs, and empty
// for a probe that is not overdue.
func (h HeldLine) OverdueEntry() string {
	if !h.ProbeOverdue || h.Probe == nil {
		return ""
	}
	return fmt.Sprintf("- The probe run %s, started under the hold on new work at %s, has been out for %s, past the %s a probe run is allowed, and has neither landed nor blocked. It is back on this list as a stopped run: decide whether the hold keeps waiting on it, and release the hold if the line is fine.\n",
		h.probeNamed(), localMoment(h.Probe.StartedAt), spanSaid(h.ProbeOut), spanSaid(h.OverdueAfter))
}

func (h HeldLine) probeNamed() string {
	if run := strings.TrimSpace(h.Probe.RunID); run != "" {
		return run + " of " + h.Probe.WorkItemID
	}
	return "of " + h.Probe.WorkItemID
}

// maxHeldLineReasonBytes bounds her earlier reason as the held line quotes it.
const maxHeldLineReasonBytes = 400

// spanSaid is a length of time in words, to the minute: "3 hours 12 minutes".
func spanSaid(elapsed time.Duration) string {
	switch {
	case elapsed < time.Minute:
		return "less than a minute"
	case elapsed < time.Hour:
		return count(int(elapsed.Minutes()), "minute")
	}
	hours, minutes := int(elapsed.Hours()), int(elapsed.Minutes())%60
	if minutes == 0 {
		return count(hours, "hour")
	}
	return count(hours, "hour") + " " + count(minutes, "minute")
}
