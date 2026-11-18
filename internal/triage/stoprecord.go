package triage

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// StopRecord is the run's own record of the stop an entry is about, copied
// from the run record's stop (runstate.RunStop) when the entry is built. The
// cause and the reason are the run state's wire names, converted at the owning
// type; this package declares no vocabulary of its own for them, and the words
// a person reads are carried in CauseSays.
type StopRecord struct {
	Attempt   int       `json:"attempt"`
	StoppedAt time.Time `json:"stopped_at"`
	Step      string    `json:"step,omitempty"`
	Candidate string    `json:"candidate,omitempty"`
	Kept      bool      `json:"kept"`
	Reason    string    `json:"reason"`
	Cause     string    `json:"cause"`
	CauseSays string    `json:"cause_says"`
	// Charged says the stop counts against the item's repair attempts and
	// review rounds, which only a stop about the work does.
	Charged bool `json:"charged"`
	// Unrecognised says the run's stop reason is one the build that recorded it
	// did not recognise, which is why it was decided as about the work.
	Unrecognised bool `json:"unrecognised,omitempty"`
}

// Validate rejects a stop that cannot describe a recorded one.
func (s StopRecord) Validate() error {
	var problems []error
	if s.StoppedAt.IsZero() {
		problems = append(problems, errors.New("stopped_at is required"))
	}
	if strings.TrimSpace(s.Reason) == "" {
		problems = append(problems, errors.New("reason is required"))
	}
	if strings.TrimSpace(s.Cause) == "" || strings.TrimSpace(s.CauseSays) == "" {
		problems = append(problems, errors.New("cause and cause_says are required"))
	}
	if len(s.CauseSays) > MaxMessageBytes {
		problems = append(problems, fmt.Errorf("cause_says is %d bytes, limit is %d", len(s.CauseSays), MaxMessageBytes))
	}
	return errors.Join(problems...)
}

// Describe is the stop in one sentence: when and where it stopped, its cause,
// whether it was charged, and whether the change is kept.
func (s StopRecord) Describe() string {
	var said strings.Builder
	fmt.Fprintf(&said, "attempt %d stopped at %s", s.Attempt, s.StoppedAt.UTC().Format(time.RFC3339))
	if s.Step != "" {
		fmt.Fprintf(&said, " at the %s step", s.Step)
	}
	fmt.Fprintf(&said, "; cause %s", s.CauseSays)
	if s.Unrecognised {
		fmt.Fprintf(&said, ", because its stop reason %q is not one the harness recognises", s.Reason)
	}
	if s.Charged {
		said.WriteString("; charged against the item's repair attempts and review rounds")
	} else {
		said.WriteString("; no repair attempt or review round charged")
	}
	switch {
	case s.Kept && s.Candidate != "":
		fmt.Fprintf(&said, "; the change is kept at %s", s.Candidate)
	case s.Kept:
		said.WriteString("; the change is kept")
	default:
		said.WriteString("; no change was kept")
	}
	return said.String()
}

func (e Entry) renderStop() string {
	if e.Stop == nil {
		return ""
	}
	return indented("Stop", e.Stop.Describe())
}
