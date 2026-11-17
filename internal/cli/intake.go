package cli

import (
	"fmt"
	"sort"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// describeIntakeQueries is one line for each scheduled query, in name order,
// saying what it calls, how often, and whose messages it takes. A query that
// takes messages from anybody says so in words and as the line that set it,
// because that is the setting an operator most needs to see.
func describeIntakeQueries(intake config.Intake) []string {
	names := make([]string, 0, len(intake.Queries))
	for name := range intake.Queries {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		query := intake.Queries[name]
		from := fmt.Sprintf("messages from %d listed senders", len(query.Senders))
		if len(query.Senders) == 1 {
			from = "messages from 1 listed sender"
		}
		if query.Anyone {
			from = "messages from anyone (anyone: true)"
		}
		lines = append(lines, fmt.Sprintf("intake query %s: calls %s every %s, %s, judged by the rules in %s, questions to %s",
			name, query.Tool, query.Every, from, query.Rules, query.Questions))
	}
	return lines
}
