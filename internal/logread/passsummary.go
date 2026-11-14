package logread

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// maxSummaryProblemBytes bounds the pass's own problem as a summary repeats
// it; the whole of it is in the entry.
const maxSummaryProblemBytes = 600

// PassSummary is one pass entry said in a few lines: whose pass it was, when
// it started and ended, and how it ended. It is what a read returns beside a
// pass entry too long to return whole, so whether a role is running is
// answered by one read rather than by reading the entry to its end.
type PassSummary struct {
	Task      string     `json:"task,omitempty"`
	Role      string     `json:"role,omitempty"`
	Agent     string     `json:"agent,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// Ended is how the pass ended, in words.
	Ended string `json:"ended"`
	// Incomplete says the entry could not be read to its end, so a field it
	// carries after the point read to may be missing here.
	Incomplete bool `json:"incomplete,omitempty"`
}

// passFields is what a summary reads of a pass entry, by the names the pass
// record gives them.
type passFields struct {
	Task              string     `json:"task"`
	Role              string     `json:"role"`
	Agent             string     `json:"agent"`
	StartedAt         *time.Time `json:"started_at"`
	EndedAt           *time.Time `json:"ended_at"`
	Turns             *int       `json:"turns"`
	Failed            bool       `json:"failed"`
	MissingReport     bool       `json:"missing_report"`
	StoppedForRestart bool       `json:"stopped_for_restart"`
	NotStarted        string     `json:"not_started"`
	Problem           string     `json:"problem"`
	Missed            *struct {
		Trigger string `json:"trigger"`
		How     string `json:"how"`
	} `json:"missed"`
}

// summarizePass reads the summary's fields from a pass entry, whole or only a
// prefix of it. It walks the entry's top level one field at a time, so a field
// that is cut or will not decode stops the walk without losing what was read
// before it.
func summarizePass(entry []byte, redactor execution.Redactor) *PassSummary {
	decoder := json.NewDecoder(bytes.NewReader(entry))
	fields := map[string]json.RawMessage{}
	complete := false
	if token, err := decoder.Token(); err == nil && token == json.Delim('{') {
		for {
			token, err := decoder.Token()
			if err != nil {
				break
			}
			if token == json.Delim('}') {
				complete = true
				break
			}
			key, isKey := token.(string)
			if !isKey {
				break
			}
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				break
			}
			switch key {
			case "task", "role", "agent", "started_at", "ended_at", "turns", "failed", "missing_report", "stopped_for_restart", "not_started", "problem", "missed":
				fields[key] = value
			}
		}
	}
	var read passFields
	for key, value := range fields {
		one, _ := json.Marshal(map[string]json.RawMessage{key: value})
		_ = json.Unmarshal(one, &read)
	}
	summary := &PassSummary{Task: read.Task, Role: read.Role, Agent: read.Agent, StartedAt: read.StartedAt, EndedAt: read.EndedAt, Incomplete: !complete}
	summary.Ended = redactor.Redact(howPassEnded(read, complete))
	return summary
}

// howPassEnded says how a pass ended from the fields its record carries.
func howPassEnded(read passFields, complete bool) string {
	turns := "an unknown number of turns"
	if read.Turns != nil {
		turns = fmt.Sprintf("%d turn(s)", *read.Turns)
	}
	var said string
	switch {
	case read.Missed != nil:
		said = fmt.Sprintf("missed: the pass its %s trigger owed was %s", read.Missed.Trigger, read.Missed.How)
	case read.NotStarted != "":
		said = "failed before its first turn: " + read.NotStarted
	case read.StoppedForRestart:
		said = "stopped part-way after " + turns + " for a restart onto a new build"
	case read.Failed:
		said = "failed after " + turns
	case read.Turns != nil && *read.Turns == 0:
		said = "took no turn"
	case read.Turns != nil:
		said = "completed after " + turns
		if read.MissingReport {
			said += ", without the account it owed"
		}
	default:
		said = "not known: the entry was not read far enough to say"
	}
	if problem := strings.TrimSpace(read.Problem); problem != "" {
		if len(problem) > maxSummaryProblemBytes {
			problem = problem[:characterStart(problem, maxSummaryProblemBytes)] + " [cut here; the whole problem is in the entry]"
		}
		said += "; its record says: " + problem
	}
	if !complete {
		said += " (read from the part of the entry that could be read; a field after that point is not counted)"
	}
	return said
}
