package checks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The three outcomes a project's own check can end in, through a real shell:
// a pass, a failure that stops the list, and a check that said it could not
// run, which judged nothing — it carries its reason and the list goes on to the
// checks after it. The line is found among whatever else the check printed,
// on either stream, the way a make target wrapping it would leave it.
func TestACheckThatCouldNotRunCarriesItsReasonAndTheListGoesOn(t *testing.T) {
	t.Parallel()

	var completed []map[string]any
	results, _, err := (Runner{Process: execution.OSProcessRunner{}}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{
			"true",
			"echo starting; echo '" + CouldNotRunPrefix + " codex is not installed on this machine' >&2; echo 'make: *** [codex-resume] Error 1'; exit 2",
			"exit 3",
			"true",
		}},
		func(event execution.Event) error {
			if event.Type == execution.EventCommandCompleted {
				var payload map[string]any
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					t.Fatalf("decode: %v", err)
				}
				completed = append(completed, payload)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %#v, want the pass, the check that could not run, and the failure that stopped the list", results)
	}
	if !results[0].Passed || results[0].CouldNotRun != "" {
		t.Fatalf("first = %#v, want a plain pass", results[0])
	}
	if results[1].Passed || results[1].CouldNotRun != "codex is not installed on this machine" {
		t.Fatalf("second = %#v, want could not run with its reason", results[1])
	}
	if results[2].Passed || results[2].CouldNotRun != "" || results[2].Process.ExitCode != 3 {
		t.Fatalf("third = %#v, want a real failure", results[2])
	}
	if got := completed[1]["could_not_run"]; got != "codex is not installed on this machine" {
		t.Fatalf("completion event could_not_run = %#v", got)
	}
}

// The line is a convention only beside a non-zero exit and with a reason
// after it: a check that exits zero passed whatever it printed, and a bare
// prefix is a failure, so a check written wrong is handed back rather than
// waved through.
func TestTheCouldNotRunLineCountsOnlyWithAReasonAndANonZeroExit(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		command    string
		wantPassed bool
	}{
		{name: "exit zero", command: "echo '" + CouldNotRunPrefix + " nothing to do'; exit 0", wantPassed: true},
		{name: "no reason", command: "echo '" + CouldNotRunPrefix + "   '; exit 1"},
		{name: "not at the start of a line", command: "echo 'said: " + CouldNotRunPrefix + " codex missing'; exit 1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			results, _, err := (Runner{Process: execution.OSProcessRunner{}}).Run(
				context.Background(),
				Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{test.command, "true"}},
				nil,
			)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if results[0].CouldNotRun != "" || results[0].Passed != test.wantPassed {
				t.Fatalf("result = %#v, want passed=%t and no could-not-run reason", results[0], test.wantPassed)
			}
			if !test.wantPassed && len(results) != 1 {
				t.Fatalf("results = %d, want the failure to stop the list", len(results))
			}
		})
	}
}

func TestACouldNotRunReasonIsBounded(t *testing.T) {
	t.Parallel()
	reason, ok := couldNotRunReason("  " + CouldNotRunPrefix + " " + strings.Repeat("é", maxCouldNotRunReason))
	if !ok || len(reason) > maxCouldNotRunReason+len("…") || !strings.HasSuffix(reason, "…") {
		t.Fatalf("reason = %q (%d bytes), ok = %t", reason, len(reason), ok)
	}
}

// A check that printed the line and also names a failing test ran something
// that failed: one step of a make target could not run and another failed, or
// the code under test printed the line itself. It is a real failure, handed
// back like any other, and the list stops there.
func TestACheckThatAlsoNamesAFailureIsARealFailure(t *testing.T) {
	t.Parallel()
	results, _, err := (Runner{Process: execution.OSProcessRunner{}}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{
			"echo '" + CouldNotRunPrefix + " codex is not installed'; echo '--- FAIL: TestSomething (0.00s)'; echo 'FAIL'; exit 1",
			"true",
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 1 || results[0].Passed || results[0].CouldNotRun != "" {
		t.Fatalf("results = %#v, want one real failure that stops the list", results)
	}
}
