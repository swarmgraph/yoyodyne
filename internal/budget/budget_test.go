package budget

import (
	"strings"
	"testing"
	"time"
)

func TestTheFirstTurnOfAMessageIsPermitted(t *testing.T) {
	t.Parallel()
	decision := Decide(Spent{}, Limits{})
	if decision.Stop != "" || !decision.Permission.Granted() || decision.Permission.Closing() {
		t.Fatalf("decision = %+v", decision)
	}
	if (Permission{}).Granted() {
		t.Fatal("a permission nobody decided is granted")
	}
}

// Each limit stops the message on its own, and says which in ordinary words.
func TestEachLimitStopsTheMessageAndSaysWhy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		spent  Spent
		limits Limits
		want   string
	}{
		{name: "turns", spent: Spent{Turns: 3}, limits: Limits{Turns: 3}, want: "this message has taken 3 provider turns, the most one message may take"},
		{name: "one turn", spent: Spent{Turns: 1}, limits: Limits{Turns: 1}, want: "taken 1 provider turn,"},
		{name: "default turns", spent: Spent{Turns: DefaultMessageTurns}, want: "the most one message may take"},
		{name: "elapsed", spent: Spent{Turns: 1, Elapsed: 10*time.Minute + 5*time.Second}, limits: Limits{Elapsed: 10 * time.Minute}, want: "answered for 10 minutes 5 seconds, the longest one message may run (10 minutes)"},
		{name: "spend", spent: Spent{Turns: 1, SpendUSD: 2.5}, limits: Limits{SpendUSD: 2}, want: "this message has cost $2.50, the most one message may spend ($2.00)"},
		{name: "closing turn taken", spent: Spent{Turns: 3, ToolLimit: "reading the repository", ToolLimitTurn: 2}, want: "reading the repository reached the most one message may do, and the turn after that was its last"},
	} {
		decision := Decide(test.spent, test.limits)
		if decision.Permission.Granted() || !strings.Contains(decision.Stop, test.want) {
			t.Errorf("%s: decision = %+v, want a stop saying %q", test.name, decision, test.want)
		}
	}
}

// Spend is not a limit unless one is set, and nothing below a limit stops.
func TestAMessageInsideItsLimitsContinues(t *testing.T) {
	t.Parallel()
	decision := Decide(Spent{Turns: 2, Elapsed: time.Minute, SpendUSD: 100}, Limits{Turns: 5, Elapsed: time.Hour})
	if !decision.Permission.Granted() || decision.Permission.Closing() || decision.Stop != "" {
		t.Fatalf("decision = %+v", decision)
	}
}

// Once a tool's own limit is reached, exactly one more turn is permitted, and
// it is marked as the message's last.
func TestAToolLimitAllowsOneClosingTurnAndNoMore(t *testing.T) {
	t.Parallel()
	spent := Spent{Turns: 2, ToolLimit: "reading runs", ToolLimitTurn: 2}
	closing := Decide(spent, Limits{})
	if !closing.Permission.Granted() || !closing.Permission.Closing() {
		t.Fatalf("closing decision = %+v", closing)
	}
	spent.Turns++
	if after := Decide(spent, Limits{}); after.Permission.Granted() || after.Stop == "" {
		t.Fatalf("a turn after the closing one was permitted: %+v", after)
	}
}

// The overall limits still apply to the closing turn: a message already at its
// turn limit does not get one more because a tool reached its own.
func TestTheClosingTurnStaysInsideTheMessageLimits(t *testing.T) {
	t.Parallel()
	decision := Decide(Spent{Turns: 4, ToolLimit: "reading runs", ToolLimitTurn: 4}, Limits{Turns: 4})
	if decision.Permission.Granted() || !strings.Contains(decision.Stop, "4 provider turns") {
		t.Fatalf("decision = %+v", decision)
	}
}
