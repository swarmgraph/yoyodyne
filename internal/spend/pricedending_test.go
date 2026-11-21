package spend

import (
	"context"
	"errors"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
)

// An adapter hands back the priced terminal it read beside the error it found
// after it (internal/backend/claudecode and codex). The line records what the
// provider said the invocation cost, and the invocation still fails: the money
// was spent whichever way it ended, and reading it as unknown because something
// later went wrong is the understatement the line exists to prevent.
func TestAPricedEndingReturnedBesideAnErrorIsRecordedAtItsCost(t *testing.T) {
	t.Parallel()

	log := &recordingLog{}
	malformed := errors.New("parse Claude Code stream: decode stream event: invalid character 'n'")
	metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: "claude-code", SessionID: "session-1", CostUSD: 3.25, CostReported: true, IsError: true}, malformed
	})
	if _, err := metered.Run(context.Background(), testRequest()); !errors.Is(err, malformed) {
		t.Fatalf("Run() error = %v, want the invocation's own failure", err)
	}
	if len(log.lines) != 1 {
		t.Fatalf("recorded %d line(s), want exactly one", len(log.lines))
	}
	if line := log.lines[0]; !line.Known() || line.AmountUSD != 3.25 || line.Unknown != "" {
		t.Fatalf("recorded = %#v, want the terminal's 3.25 recorded as known", line)
	}
}
