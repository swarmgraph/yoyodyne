package readmodel

// The holds a person's script has placed on landing, as every surface shows
// them: one line per target branch, saying why landing stopped there, when the
// hold was set, and when it ends. A hold is not the pause and not the intake
// hold, so the line says what keeps going.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// LandingHolds is the landing holds the read model projects. It is satisfied
// by *runstate.LandingHoldStore.
type LandingHolds interface {
	List(now time.Time) ([]runstate.LandingHold, error)
}

// ReadLandingHolds is every hold standing at now, and what could not be read.
func ReadLandingHolds(holds LandingHolds, now time.Time) ([]runstate.LandingHold, string) {
	if holds == nil {
		return nil, ""
	}
	standing, err := holds.List(now)
	if err != nil {
		return nil, fmt.Sprintf("the landing holds could not be read, so landing may be held where this does not say: %v", err)
	}
	return standing, ""
}

// RenderLandingHolds is one line per hold standing, printed under the four
// lines, and nothing where there are none.
func (s Standing) RenderLandingHolds() string {
	if len(s.LandingHolds) == 0 && s.LandingHoldsProblem == "" {
		return ""
	}
	const layout = "2006-01-02 15:04 MST"
	var rendered strings.Builder
	for _, hold := range s.LandingHolds {
		fmt.Fprintf(&rendered, "Landing on %s is held: %s. Set at %s; ends at %s, or sooner with `yoyo landing release %s`. Checks and reviews carry on, and a change ready to land waits with its evidence kept.\n",
			hold.TargetBranch, strings.TrimRight(hold.Reason, ".;: "), hold.SetAt.Local().Format(layout), hold.Until.Local().Format(layout), hold.TargetBranch)
	}
	if s.LandingHoldsProblem != "" {
		rendered.WriteString(partialRead + s.LandingHoldsProblem + "\n")
	}
	return s.wording.Render(rendered.String())
}
