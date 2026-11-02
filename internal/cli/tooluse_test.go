package cli

import (
	"os"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/tooluse"
)

// Every invocation a command makes shows its unexpected tool use on that
// command's standard error as it is filed, beside the conversation or run the
// person is watching.
func TestToolUseIsShownOnTheCommandsStandardError(t *testing.T) {
	watch, ok := components{}.toolUse().(tooluse.Watch)
	if !ok || watch.Show != os.Stderr {
		t.Fatalf("toolUse() = %#v, want a watch that shows each report on standard error", watch)
	}
	if watch.Reports != nil {
		t.Fatalf("a pile was handed on for parts that have none: %#v", watch.Reports)
	}
}
