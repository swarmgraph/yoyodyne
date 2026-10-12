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
	// AsksForge is a step whose failure is the forge's own answer to a request
	// made of it: the merge, or the question whether it merged.
	AsksForge bool `json:"asks_forge,omitempty"`
	// ReadsRemote is a step that reads or pushes to the remote repository
	// rather than asking the forge anything, so its failure is what git found
	// there — a branch somebody rewrote, a push the remote turned away — and is
	// never said as the forge's refusal.
	ReadsRemote bool `json:"reads_remote,omitempty"`
}

// What a landing step asks of anything outside this repository, which decides
// whose words its failure is said in.
type landingAsks int

const (
	landingAsksNothing landingAsks = iota
	landingAsksForge
	landingReadsRemote
)

// landingAt records the step the run's landing is taking now. It lives on the
// outcome only: a landing a process restarts begins its steps again, and a
// stopped one carries the step into the notes written as it ends.
func (a *activeRun) landingAt(doing string, asks landingAsks) {
	a.outcome.LandingStep = &LandingStep{Doing: doing, AsksForge: asks == landingAsksForge, ReadsRemote: asks == landingReadsRemote}
}

// leftLanding is the run going back to its gate, or on past its landing, so
// that nothing it writes later says it stopped while landing.
func (a *activeRun) leftLanding() {
	a.outcome.LandingStep = nil
}

// maxForgeAnswerBytes keeps what the forge or the remote said to one readable
// line. The whole of it is on the run record and in the note's failure line.
const maxForgeAnswerBytes = 600

// renderLandingStop is the line a stopped landing ends its notes with: the step
// it stopped at, and what the forge said there. A step that did not ask the
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
	if step.AsksForge {
		if answer == "" {
			return line + " The forge gave no answer this run could read."
		}
		return line + " What the forge said: " + singleLine(answer, maxForgeAnswerBytes)
	}
	if step.ReadsRemote && answer != "" {
		line += " What the remote showed: " + strings.TrimRight(singleLine(answer, maxForgeAnswerBytes), ".") + "."
	}
	pull := outcome.PullRequest
	if pull == nil || pull.Merged || pull.MergeQueued {
		return line
	}
	if step.ReadsRemote {
		return line + fmt.Sprintf(" The forge was never asked to merge pull request #%d.", pull.Number)
	}
	return line + fmt.Sprintf(" The forge was never asked to merge pull request #%d, so nothing the forge said stopped this landing; the failure above is this repository's.", pull.Number)
}

// withLandingStop appends the landing's stop line to a note that has one.
func withLandingStop(notes string, outcome Outcome, failure string) string {
	if line := renderLandingStop(outcome, failure); line != "" {
		return notes + "\n" + line
	}
	return notes
}
