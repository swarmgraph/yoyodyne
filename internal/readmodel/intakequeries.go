package readmodel

// The scheduled intake queries, counted for every surface
// (docs/designs/scheduled-message-intake.md, sections 1 and 8).
//
// The intake record holds the text of every message a query read. None of it
// reaches a surface: what is projected here is each query by name, how many
// messages it recorded, how many it ignored because their sender is not
// listed, how many minutes it skipped as too large to read, how far it has
// read, and how many runs in a row have failed. IntakeQueries and
// IntakeQueryCounts carry no field a message's text, sender or link could be
// put in.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/intake"
)

// IntakeRecord is the intake record. It is satisfied by *intake.Records.
type IntakeRecord interface {
	Read() (intake.State, error)
}

// IntakeQueries is every query the intake record holds anything of, counted.
type IntakeQueries struct {
	Queries []IntakeQueryCounts `json:"queries"`
	// Unreadable is how many lines of the record could not be read.
	Unreadable int `json:"unreadable,omitempty"`
}

// IntakeQueryCounts is one query, counted, with no content.
type IntakeQueryCounts struct {
	Query string `json:"query"`
	// ReadUpTo is the time the query has read every message up to; zero
	// before it has been switched on.
	ReadUpTo time.Time `json:"read_up_to,omitzero"`
	// Messages is how many messages the query recorded first, and Ignored how
	// many of those were ignored because their sender is not listed.
	Messages int `json:"messages"`
	Ignored  int `json:"ignored,omitempty"`
	// SkippedMinutes is how many one-minute windows the query skipped because
	// even one minute of messages was too large to read.
	SkippedMinutes int `json:"skipped_minutes,omitempty"`
	// FailedInARow is how many of its runs in a row have failed.
	FailedInARow int `json:"failed_in_a_row,omitempty"`
}

// ReadIntakeQueries counts the intake record. A product with no record wired,
// or whose record holds nothing, reads nil and says nothing, so a project with
// no intake section shows exactly what it showed before intake existed.
func ReadIntakeQueries(record IntakeRecord) (*IntakeQueries, string) {
	if record == nil {
		return nil, ""
	}
	state, err := record.Read()
	if err != nil {
		return nil, fmt.Sprintf("the intake record could not be read: %v", err)
	}
	if len(state.Queries) == 0 && state.Unreadable == 0 {
		return nil, ""
	}
	counted := &IntakeQueries{Unreadable: state.Unreadable}
	for name, query := range state.Queries {
		counted.Queries = append(counted.Queries, IntakeQueryCounts{
			Query: name, ReadUpTo: query.ReadUpTo, Messages: query.Messages, Ignored: query.Ignored,
			SkippedMinutes: len(query.Skipped), FailedInARow: query.FailedInARow,
		})
	}
	sort.Slice(counted.Queries, func(i, j int) bool { return counted.Queries[i].Query < counted.Queries[j].Query })
	return counted, ""
}

// RenderIntakeQueries is the intake queries as `yoyo status` prints them:
// nothing where the product has none, and otherwise one line for each query.
func (s Standing) RenderIntakeQueries() string {
	if s.IntakeQueriesProblem != "" {
		return "\nIntake queries: could not be read: " + s.IntakeQueriesProblem + "\n"
	}
	if s.IntakeQueries == nil {
		return ""
	}
	const layout = "2006-01-02 15:04 MST"
	var text strings.Builder
	text.WriteString("\nIntake queries:\n")
	for _, query := range s.IntakeQueries.Queries {
		parts := []string{}
		if query.ReadUpTo.IsZero() {
			parts = append(parts, "not switched on yet")
		} else {
			parts = append(parts, "read up to "+query.ReadUpTo.Local().Format(layout))
		}
		parts = append(parts, fmt.Sprintf("%d message(s) recorded", query.Messages))
		if query.Ignored > 0 {
			parts = append(parts, fmt.Sprintf("%d of them ignored because the sender is not listed", query.Ignored))
		}
		if query.SkippedMinutes > 0 {
			parts = append(parts, fmt.Sprintf("%d minute(s) skipped as too large to read", query.SkippedMinutes))
		}
		if query.FailedInARow > 0 {
			parts = append(parts, fmt.Sprintf("its last %d run(s) failed", query.FailedInARow))
		}
		fmt.Fprintf(&text, "  %s: %s\n", query.Query, strings.Join(parts, "; "))
	}
	if s.IntakeQueries.Unreadable > 0 {
		fmt.Fprintf(&text, "  %d line(s) of the intake record could not be read\n", s.IntakeQueries.Unreadable)
	}
	return text.String()
}
