package orchestrator

import (
	"fmt"
	"strings"
)

// LandingStep is the step of landing an approved change a run is taking: the
// one it stopped at, when a landing stops. It is said on every note a stopped
// landing writes on its item, as the note's last line, so the item says which
// step failed and what the forge said without its reader having to find the
// reason inside a blocker that also carries the review and the target branch's
// protection. A landing whose last line was the protection line read as one that
// stopped on its way to the forge with nothing said, when what stopped it was a
// replay onto a target another change had moved.
type LandingStep struct {
	// Doing is the step, in words that finish "Landing stopped at: ...".
	Doing string `json:"doing"`
	// AsksForge is a step whose failure is the forge's answer, or the remote's,
	// rather than this repository's.
	AsksForge bool `json:"asks_forge,omitempty"`
}

// landingAt records the step the run's landing is taking now. It lives on the
// outcome only: a landing a process restarts begins its steps again, and a
// stopped one carries the step into the notes written as it ends.
func (a *activeRun) landingAt(doing string, asksForge bool) {
	a.outcome.LandingStep = &LandingStep{Doing: doing, AsksForge: asksForge}
}

// leftLanding is the run going back to its gate, or on past its landing, so
// that nothing it writes later says it stopped while landing.
func (a *activeRun) leftLanding() {
	a.outcome.LandingStep = nil
}

// maxForgeAnswerBytes keeps what the forge said to one readable line. The whole
// of it is on the run record and in the note's failure line.
const maxForgeAnswerBytes = 600

// renderLandingStop is the line a stopped landing ends its notes with: the step
// it stopped at, and what the forge said there. A step that does not ask the
// forge says the merge was never asked for, because "nothing the forge said is
// recorded" is exactly what a reader cannot tell apart from a lost answer. A
// run that is not landing writes nothing.
func renderLandingStop(outcome Outcome, failure string) string {
	step := outcome.LandingStep
	if step == nil || step.Doing == "" {
		return ""
	}
	line := "Landing stopped at: " + step.Doing + "."
	answer := strings.TrimSpace(outcome.PublishFailure)
	if answer == "" {
		answer = strings.TrimSpace(failure)
	}
	switch {
	case step.AsksForge && answer != "":
		line += " What the forge said: " + singleLine(answer, maxForgeAnswerBytes)
	case step.AsksForge:
		line += " The forge gave no answer this run could read."
	case outcome.PullRequest != nil && !outcome.PullRequest.Merged && !outcome.PullRequest.MergeQueued:
		line += fmt.Sprintf(" The forge was never asked to merge pull request #%d, so nothing the forge said stopped this landing; the failure above is this repository's.", outcome.PullRequest.Number)
	}
	return line
}

// withLandingStop appends the landing's stop line to a note that has one.
func withLandingStop(notes string, outcome Outcome, failure string) string {
	if line := renderLandingStop(outcome, failure); line != "" {
		return notes + "\n" + line
	}
	return notes
}
