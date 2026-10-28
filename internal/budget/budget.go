// Package budget decides whether one more paid call may be taken. It owns the
// rule and nothing else: it keeps no store of its own, and what has been spent
// stays on whatever already records it. A caller says what has been spent and
// which limits apply, and the decision says whether one more may be taken or,
// when it may not, why, in words an operator can read.
//
// The first thing it decides is a provider turn in a management conversation.
// One message from a person, or one recurring pass, may take several turns: the
// role asks for something (the tracker, research, a repository read, a log, a
// run, the factory's health, a recovery), the harness carries it out, and the
// result goes back to the role in another turn. Every one of those turns is
// paid for, so each is taken only on a Permission this package grants.
package budget

import (
	"fmt"
	"strings"
	"time"
)

// Default limits on one message. A message is one thing a person said, or one
// recurring pass, however many provider turns answering it takes.
//
// DefaultMessageTurns leaves room for every tool's own per-message limit to be
// used once in a message that uses several of them, and stops a role that keeps
// asking well before an outside deadline would. DefaultMessageElapsed is longer
// than one turn's own time limit, so one slow turn is not cut short by it; time
// spent waiting out a provider that refused is not counted against it, because
// waiting costs nothing.
const (
	DefaultMessageTurns   = 16
	DefaultMessageElapsed = 45 * time.Minute
)

// Limits are what one message may spend. Turns and Elapsed always apply; a zero
// takes the default. SpendUSD applies only where it is set above zero and the
// provider reports what each turn cost.
type Limits struct {
	Turns    int
	Elapsed  time.Duration
	SpendUSD float64
}

// MessageLimits is limits with every unset field given its default.
func MessageLimits(limits Limits) Limits {
	if limits.Turns <= 0 {
		limits.Turns = DefaultMessageTurns
	}
	if limits.Elapsed <= 0 {
		limits.Elapsed = DefaultMessageElapsed
	}
	if limits.SpendUSD < 0 {
		limits.SpendUSD = 0
	}
	return limits
}

// Spent is what one message has spent so far.
type Spent struct {
	// Turns is how many provider turns the message has taken.
	Turns int
	// Elapsed is how long the message has been answered for, less any time spent
	// waiting out a provider that refused it.
	Elapsed time.Duration
	// SpendUSD is what the provider reported the message's turns cost.
	SpendUSD float64
	// ToolLimit names, in ordinary words, the first tool whose own per-message
	// limit was reached, and is empty while none has been. ToolLimitTurn is how
	// many turns the message had taken when that happened.
	ToolLimit     string
	ToolLimitTurn int
}

// Permission is leave to take one provider turn. Only Decide grants one: its
// fields are unexported, so code outside this package cannot make a valid one,
// and a turn that requires one cannot be taken without asking.
type Permission struct {
	granted bool
	closing bool
}

// Granted says whether this permission came from a decision that allowed the
// turn. The zero Permission is not granted.
func (p Permission) Granted() bool { return p.granted }

// Closing says the permitted turn is the last one the message may take, because
// a tool's own limit has been reached. Whatever it asks for is carried out, but
// nothing it produces is answered with another turn.
func (p Permission) Closing() bool { return p.closing }

// Decision is the answer to whether one more turn may be taken. Exactly one of
// its two halves is set: a granted Permission, or the Stop that says why not.
type Decision struct {
	Permission Permission
	Stop       string
}

// Decide says whether one more provider turn may be taken in a message that has
// spent what it has, under the given limits.
//
// A message stops when it has taken as many turns, run for as long, or spent as
// much as its limits allow. Once any tool's own limit has been reached, the
// message takes one closing turn, in which the role hears the last results it
// asked for and answers, and no more: a role that goes on asking after that is
// not answered with another paid turn, whichever tool it asks for.
func Decide(spent Spent, limits Limits) Decision {
	limits = MessageLimits(limits)
	if spent.ToolLimit != "" && spent.Turns > spent.ToolLimitTurn {
		return Decision{Stop: fmt.Sprintf("%s reached the most one message may do, and the turn after that was its last", spent.ToolLimit)}
	}
	if spent.Turns >= limits.Turns {
		return Decision{Stop: fmt.Sprintf("this message has taken %s, the most one message may take", plural(spent.Turns, "provider turn"))}
	}
	if spent.Elapsed >= limits.Elapsed {
		return Decision{Stop: fmt.Sprintf("this message has been answered for %s, the longest one message may run (%s)", plainDuration(spent.Elapsed), plainDuration(limits.Elapsed))}
	}
	if limits.SpendUSD > 0 && spent.SpendUSD >= limits.SpendUSD {
		return Decision{Stop: fmt.Sprintf("this message has cost $%.2f, the most one message may spend ($%.2f)", spent.SpendUSD, limits.SpendUSD)}
	}
	return Decision{Permission: Permission{granted: true, closing: spent.ToolLimit != ""}}
}

// plainDuration says a duration in minutes and seconds, the way a person would.
func plainDuration(d time.Duration) string {
	d = d.Round(time.Second)
	minutes, seconds := int(d/time.Minute), int((d%time.Minute)/time.Second)
	var parts []string
	if minutes > 0 {
		parts = append(parts, plural(minutes, "minute"))
	}
	if seconds > 0 || minutes == 0 {
		parts = append(parts, plural(seconds, "second"))
	}
	return strings.Join(parts, " ")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
