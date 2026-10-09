package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A developer session that dies before the run's first check says why on the
// run's record, on the item's notes and on the docket entry, and the item's
// notes end on that cause rather than on the diff stat. The session here is the
// one that resumed a session id its provider did not have: it wrote one line to
// standard error, a terminal with no message, and exited with status 1, which
// the adapter alone records as "process_exit_1".
func TestADeveloperRunThatFailsBeforeItsFirstCheckRecordsWhatEndedIt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		result backend.RunResult
		want   []string
	}{
		{
			name: "the session wrote its reason to standard error",
			result: backend.RunResult{
				IsError:    true,
				StopReason: "process_exit_1",
				Process: execution.ProcessResult{
					Status:   execution.ProcessFailed,
					ExitCode: 1,
					Stderr:   "starting\n\nNo conversation found with session ID: 01a106a4\n",
				},
			},
			want: []string{
				"the provider gave no answer of its own",
				"the session exited with status 1",
				"the last lines it wrote to standard error were:",
				"No conversation found with session ID: 01a106a4",
			},
		},
		{
			name: "the provider answered and the session wrote nothing",
			result: backend.RunResult{
				IsError:    true,
				StopReason: "api_error",
				FinalText:  "credit balance is too low",
				Process:    execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 2},
			},
			want: []string{
				"the provider answered: api_error: credit balance is too low",
				"the session exited with status 2",
				"it wrote nothing to standard error",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Fail early", Status: "open"}}
			provider := &orchestratortest.Backend{Respond: func(request backend.RunRequest) (backend.RunResult, error) {
				if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "partial.txt"), []byte("partial"), 0o600); err != nil {
					return backend.RunResult{}, err
				}
				return test.result, nil
			}, DeveloperRecordsNoExecution: true}
			pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
			docket := &memoryDocket{}
			pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			assertSavedStopClass(t, store, outcome.RunID, runstate.StopProvider)
			if err == nil || !strings.Contains(err.Error(), "developer reported failure") {
				t.Fatalf("Run() error = %v", err)
			}
			if len(outcome.Checks) != 0 {
				t.Fatalf("checks ran before the failure: %#v", outcome.Checks)
			}
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if len(docket.entries) != 1 {
				t.Fatalf("the stoppage reached nobody: %#v", docket.entries)
			}
			entry := docket.entries[0]
			for label, text := range map[string]string{
				"run record":     state.Failure,
				"item notes":     tracker.Notes,
				"docket entry":   entry.Failure,
				"rendered entry": entry.Render(),
			} {
				for _, want := range test.want {
					if !strings.Contains(text, want) {
						t.Errorf("%s does not say %q:\n%s", label, want, text)
					}
				}
				if strings.Contains(text, "process_exit_") {
					t.Errorf("%s carries the adapter's stand-in reason rather than plain words:\n%s", label, text)
				}
			}
			// The note carries the diff stat and does not end on it.
			stat := strings.Index(tracker.Notes, "Diff stat when the run ended:")
			ending := strings.LastIndex(tracker.Notes, "Ended before any check ran. What ended it: developer reported failure: ")
			if stat < 0 || ending < stat {
				t.Fatalf("the note does not end on the cause after its diff stat:\n%s", tracker.Notes)
			}
		})
	}
}

// The end of a stream is bounded to ten lines and 2 KiB, so a session that
// wrote pages to standard error before it died still leaves a failure the record
// takes, and the line that ended it is the one kept.
func TestSessionLastLinesKeepsTheEndOfTheStream(t *testing.T) {
	t.Parallel()

	var stream strings.Builder
	for range 50 {
		stream.WriteString(strings.Repeat("x", 300) + "\n")
	}
	stream.WriteString("the line that ended it\n")
	last := sessionLastLines(stream.String())
	if !strings.HasSuffix(last, "  the line that ended it") {
		t.Fatalf("the last line is not kept: %q", last)
	}
	if len(last) > 2<<10 || strings.Count(last, "\n") >= 10 {
		t.Fatalf("the end of the stream is %d bytes over %d lines", len(last), strings.Count(last, "\n")+1)
	}
	if sessionLastLines("\n \n") != "" {
		t.Fatal("a stream of blank lines is reported as something written")
	}
}
