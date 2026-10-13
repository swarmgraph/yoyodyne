package codex

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Codex's stream names a compaction without sizes, as an event of its own or as
// an item, and either is on the result.
func TestACodexCompactionIsOnTheResult(t *testing.T) {
	t.Parallel()
	parser := newStreamParser(testRunID, domain.RoleArchitect, 0, fixedClock{}, execution.NewRedactor(), nil, nil, Dialect{})
	for _, line := range []string{
		`{"id":"1","msg":{"type":"context_compacted"}}`,
		`{"type":"item.completed","item":{"id":"i","type":"context_compaction"}}`,
	} {
		if err := parser.ParseLine(line); err != nil {
			t.Fatalf("ParseLine(%s) error = %v", line, err)
		}
	}
	compactions := parser.Result().Compactions
	if len(compactions) != 2 || compactions[0].At.IsZero() || compactions[0].BeforeTokens != 0 {
		t.Fatalf("compactions = %+v", compactions)
	}
	if parser.FirstUnrecognized() != "" {
		t.Fatalf("a compaction was read as an unrecognized event: %q", parser.FirstUnrecognized())
	}
}
