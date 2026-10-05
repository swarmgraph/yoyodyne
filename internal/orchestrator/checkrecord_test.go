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
	for _, named := range []bool{true, false} {
		output := strings.Repeat("earlier output\n", 1000)
		want := "No failing test or package was named"
		if named {
			output = "--- FAIL: TestBroken (0.01s)\n" + output
			want = "TestBroken"
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
		a := activeRun{state: runstate.State{}}
		a.recordCheckFailure(result)
		notes := strings.Join(renderCheckNotes(Outcome{Checks: []checks.Result{result}}), "\n")
		entry := triage.Entry{Check: docketCheck(a.state.CheckFailure)}
		for label, text := range map[string]string{"item": notes, "docket": entry.Render(), "run": a.state.CheckFailure.Output} {
			if strings.Contains(text, "secret-value") {
				t.Errorf("%s contains secret", label)
			}
			for _, required := range []string{want, truncationNotice, "last diagnostic"} {
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
