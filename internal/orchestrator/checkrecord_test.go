package orchestrator

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

func TestFailedCheckRecordsNamesAndOutputOnItemAndDocket(t *testing.T) {
	for _, scenario := range []struct{ named, verbose bool }{{true, false}, {false, false}, {true, true}} {
		named := scenario.named
		output := strings.Repeat("earlier output\n", 1000)
		want := "No failing test or package was named in the captured output, so only its end is kept."
		if named {
			output = "--- FAIL: TestBroken (0.01s)\n" + output
			want = "TestBroken"
		}
		if scenario.verbose {
			output += strings.Repeat(strings.Repeat("x", 1023)+"\n", 8193)
			output += "--- FAIL: TestAfterCaptureLimit (0.01s)\n"
		}
		output += "last diagnostic"
		path := t.TempDir() + "/output"
		if err := os.WriteFile(path, []byte(output+"\nsecret-value"), 0600); err != nil {
			t.Fatal(err)
		}
		results, _, err := (checks.Runner{Process: execution.OSProcessRunner{}, RedactValues: []string{"secret-value"}}).Run(context.Background(), checks.Request{RunID: "run-check-record", Directory: t.TempDir(), Commands: []string{"cat '" + path + "'; exit 2"}}, func(execution.Event) error { return nil })
		if err != nil || len(results) != 1 || results[0].Passed {
			t.Fatalf("failed check: %v, %v", results, err)
		}
		result := results[0]
		if scenario.verbose {
			if result.Process.OutputTruncation == "" || strings.Contains(result.Process.Stdout, "last diagnostic") {
				t.Fatal("regression did not exceed the process capture limit")
			}
			if !strings.Contains(result.FailureOutput, "TestAfterCaptureLimit") {
				t.Fatal("observer lost the late failing test")
			}
		}
		a := activeRun{state: runstate.State{}}
		a.recordCheckFailure(result)
		notes := strings.Join(renderCheckNotes(Outcome{Checks: []checks.Result{result}}), "\n")
		entry := triage.Entry{Check: docketCheck(a.state.CheckFailure)}
		for label, text := range map[string]string{"item": notes, "docket": entry.Render(), "run": a.state.CheckFailure.Output} {
			if strings.Contains(text, "secret-value") {
				t.Errorf("%s contains secret", label)
			}
			requiredText := []string{want, truncationNotice, "last diagnostic"}
			if scenario.verbose {
				requiredText = append(requiredText, "TestAfterCaptureLimit")
			}
			for _, required := range requiredText {
				if !strings.Contains(text, required) {
					t.Errorf("%s missing %q: %s", label, required, text)
				}
			}
		}
		if len(a.state.CheckFailure.Output) > runstate.MaxCheckOutputBytes {
			t.Fatal("output exceeds bound")
		}
	}
}

// TestFailedCheckRecordsOutputAroundTheFirstFailure is a failing test whose
// own lines are printed far from the end of a long output, as a suite printing
// its summary after them does: the record keeps them, bounded with the cut
// declared, beside the end of the output.
func TestFailedCheckRecordsOutputAroundTheFirstFailure(t *testing.T) {
	var output strings.Builder
	output.WriteString(strings.Repeat("ok  \tearlier/package\t0.1s\n", 50))
	output.WriteString("=== RUN   TestBroken\n--- FAIL: TestBroken (0.01s)\n    broken_test.go:12: wanted 3, got 4 secret-value\n")
	output.WriteString(strings.Repeat("    broken_test.go:13: detail of the failure\n", 200))
	output.WriteString(strings.Repeat("ok  \tlater/package\t0.1s\n", 1000))
	output.WriteString("FAIL\tgithub.com/example/broken\t0.2s\nlast diagnostic")
	path := t.TempDir() + "/output"
	if err := os.WriteFile(path, []byte(output.String()), 0600); err != nil {
		t.Fatal(err)
	}
	results, _, err := (checks.Runner{Process: execution.OSProcessRunner{}, RedactValues: []string{"secret-value"}}).Run(context.Background(), checks.Request{RunID: "run-check-window", Directory: t.TempDir(), Commands: []string{"cat '" + path + "'; exit 2"}}, func(execution.Event) error { return nil })
	if err != nil || len(results) != 1 || results[0].Passed {
		t.Fatalf("failed check: %v, %v", results, err)
	}
	a := activeRun{state: runstate.State{}}
	a.recordCheckFailure(results[0])
	notes := strings.Join(renderCheckNotes(Outcome{Checks: results}), "\n")
	entry := triage.Entry{Check: docketCheck(a.state.CheckFailure)}
	for label, text := range map[string]string{"item": notes, "docket": entry.Render(), "run": a.state.CheckFailure.Output} {
		if strings.Contains(text, "secret-value") {
			t.Errorf("%s contains secret", label)
		}
		for _, required := range []string{
			"TestBroken",
			"Output around the first line reporting a failure:",
			"=== RUN   TestBroken",
			"broken_test.go:12: wanted 3, got 4",
			"[later lines after the failure cut]",
			"End of the output:",
			truncationNotice,
			"last diagnostic",
		} {
			if !strings.Contains(text, required) {
				t.Errorf("%s missing %q: %s", label, required, text)
			}
		}
	}
	if len(a.state.CheckFailure.Output) > runstate.MaxCheckOutputBytes {
		t.Fatalf("output is %d bytes, over the bound", len(a.state.CheckFailure.Output))
	}
}

func TestDocketReportsRemainingAttemptsAndReviewRounds(t *testing.T) {
	d := Docketer{Caps: runstate.TriageCaps{ReviewRounds: 5}, RepairLimit: 4}
	counters := d.counters(runstate.TriageCounters{ReviewRounds: 2}, runstate.State{}, 3, 0, publicationRearms{})
	text := (triage.Entry{Counters: counters}).Render()
	for _, want := range []string{"3 repair attempt(s) spent", "1 repair attempt(s); 3 review round(s) available"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
}
