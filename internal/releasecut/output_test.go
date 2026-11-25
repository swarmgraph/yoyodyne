package releasecut

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// printing has the stub release checks print what scripts/cut-release.sh
// prints, as its shell writes it.
func (f *fixture) printing(t *testing.T, shell string) {
	t.Helper()
	f.gate(t, "printed")
	if err := os.WriteFile(f.mode+".printed", []byte(shell), 0o600); err != nil {
		t.Fatal(err)
	}
}

// refusedOnce takes a pass whose checks fail and returns the refusal, which is
// on the record, on the pass, and on the release item.
func (f *fixture) refusedOnce(t *testing.T) string {
	t.Helper()
	step := f.cutter.Pass(context.Background())
	records, notes := f.records(t), f.tracker.noted()
	if len(records) != 1 || len(notes) != 1 || step.Outcome != runstate.StepRan {
		t.Fatalf("step = %+v, %d records, notes %q, want one refusal noted once", step, len(records), notes)
	}
	detail := records[0].Detail
	if step.Detail != detail || !strings.Contains(notes[0], detail) {
		t.Fatalf("step %q and note %q, want both to carry the record's %q", step.Detail, notes[0], detail)
	}
	if !strings.Contains(detail, "when tried at "+local(f.now)) || !strings.Contains(detail, "kept in "+f.outputPath()) {
		t.Errorf("detail = %q, want when it was tried and where its output is kept", detail)
	}
	if len(detail) > runstate.MaxReleaseCutTextBytes {
		t.Errorf("the refusal is %d bytes, past its bound", len(detail))
	}
	if _, err := os.Stat(filepath.Join(f.cutter.WorkRoot, "v0.6.0")); !os.IsNotExist(err) {
		t.Errorf("the checkout was not removed: %v", err)
	}
	return detail
}

func (f *fixture) outputPath() string {
	return filepath.Join(f.cutter.WorkRoot, "v0.6.0.failed-output.txt")
}

func (f *fixture) output(t *testing.T) string {
	t.Helper()
	text, err := os.ReadFile(f.outputPath())
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// again takes a pass after main moved, and wants that many notes in all.
func (f *fixture) again(t *testing.T, notes int) {
	t.Helper()
	f.moveMain(t)
	f.cutter.Pass(context.Background())
	if got := f.tracker.noted(); len(got) != notes {
		t.Errorf("after another try the item carries %q, want %d", got, notes)
	}
}

// walkthrough is a red walkthrough as the script at v0.7.0's named commit
// prints it: everything on standard output, and the failures again on standard
// error.
const walkthrough = `
echo "=== gate: release readiness"
echo "  FAIL: not the check that failed"
echo "=== gate: the getting-started walkthrough"
echo "=== step 3: a first run"
echo "  ok: the binary is installed"
echo "  FAIL: yoyo init wrote nothing in /tmp/walk.$$/project"
echo "key: $SECRET ghp_abcdefghijklmnopqrstuvwxyz0123456789"
echo "  FAIL: %s"
printf '\nwhat the walkthrough found:\n  FAIL: %s\n' >&2
echo "cut-release: the getting-started walkthrough is red, so $1 was not cut and nothing was written" >&2
exit 1
`

func TestAWalkthroughsFailuresAreRecordedAndItsOutputKept(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.cutter.Environ = []string{"PATH=" + os.Getenv("PATH")}
	f.cutter.RedactValues = []string{"held-value"}
	first := strings.ReplaceAll(walkthrough, "%s", "held-value was refused")
	f.printing(t, first)
	detail := f.refusedOnce(t)
	if !strings.Contains(detail, "the getting-started walkthrough is red") ||
		!strings.Contains(detail, "; what failed, during the getting-started walkthrough: FAIL: yoyo init wrote nothing") ||
		!strings.HasSuffix(detail, " | FAIL: [REDACTED] was refused") || strings.Contains(detail, "not the check that failed") {
		t.Errorf("detail = %q, want exactly the walkthrough's failures, masked", detail)
	}
	kept := f.output(t)
	for _, secret := range []string{"held-value", "ghp_abc"} {
		if strings.Contains(kept, secret) {
			t.Errorf("the kept output carries the secret %q", secret)
		}
	}
	for _, want := range []string{"=== step 3: a first run", "ok: the binary is installed", "cut-release: the getting-started", f.named} {
		if !strings.Contains(kept, want) {
			t.Errorf("the kept output lacks %q:\n%s", want, kept)
		}
	}
	// The same failure in another temporary folder is the same one; another
	// failure is noted, and replaces the output kept.
	f.again(t, 1)
	f.printing(t, strings.ReplaceAll(walkthrough, "%s", "the second run found nothing"))
	f.again(t, 2)
	if kept := f.output(t); !strings.Contains(kept, "the second run found nothing") || strings.Contains(kept, "was refused") {
		t.Errorf("the kept output is not the newest try's alone:\n%s", kept)
	}
	if entries, _ := os.ReadDir(f.cutter.WorkRoot); len(entries) != 1 {
		t.Errorf("%v were kept, want one output for the version", entries)
	}
}

func TestAWalkthroughThatDidNotFinishIsRecordedByItsLastLines(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.printing(t, `
echo "=== gate: the getting-started walkthrough"
for n in one two three four; do echo "line $n"; done
echo "bd: cannot reach /tmp/bd.$$ after 1.${RANDOM}s"
echo "the last of it"
printf '\nthe walkthrough did not finish:\nthe last of it\n' >&2
echo "cut-release: the getting-started walkthrough is red, so $1 was not cut and nothing was written" >&2
exit 1
`)
	detail := f.refusedOnce(t)
	if !strings.Contains(detail, "it stopped during the getting-started walkthrough without naming a failure, and its last lines are: line two | line three | line four | bd: cannot reach") ||
		!strings.HasSuffix(detail, " | the last of it") {
		t.Errorf("detail = %q, want the walkthrough's last five lines", detail)
	}
	if !strings.Contains(f.output(t), "line one") {
		t.Errorf("the kept output lacks the walkthrough's earlier lines")
	}
	f.again(t, 1)
}

func TestAFailedMakeCheckIsRecordedWithItsFailures(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.printing(t, `
echo "=== gate: the getting-started walkthrough"
echo "  ok: all of it"
echo "=== gate: make check"
flip="$(cat '`+f.mode+`.flip' 2>/dev/null)"
if [ -z "$flip" ]; then
  printf -- '--- FAIL: TestOne (0.01s)\nFAIL\nFAIL\tcalc/one\t1.234s\n--- FAIL: TestTwo (0.50s)\nFAIL\tcalc/two\t0.9s\n'
  echo 1 > '`+f.mode+`.flip'
else
  printf -- '--- FAIL: TestTwo (2.00s)\nFAIL\tcalc/two\t4.1s\n--- FAIL: TestOne (0.02s)\nFAIL\nFAIL\tcalc/one\t2.5s\n'
fi
echo "make: *** [test] Error 1" >&2
echo "cut-release: make check is red, so $1 was not cut and nothing was written" >&2
exit 1
`)
	detail := f.refusedOnce(t)
	if !strings.HasSuffix(detail, "make check is red, so v0.6.0 was not cut and nothing was written; what failed, during make check: "+
		"--- FAIL: TestOne (0.01s) | FAIL calc/one 1.234s | --- FAIL: TestTwo (0.50s) | FAIL calc/two 0.9s") {
		t.Errorf("detail = %q, want the tests and packages that failed", detail)
	}
	if !strings.Contains(f.output(t), "make: *** [test] Error 1") {
		t.Errorf("the kept output lacks what make said on standard error")
	}
	// The same tests failing in another order, taking longer.
	f.again(t, 1)
}

func TestAFailedReleaseBuildIsRecordedByItsLastLines(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.printing(t, `
echo "=== gate: make check"
echo "=== build: the archives and their checksums for $1"
echo "building calc"
echo "undefined: calc.Add" >&2
echo "cut-release: the release build for $1 failed, so no tag was written" >&2
exit 1
`)
	if detail := f.refusedOnce(t); !strings.HasSuffix(detail, "it stopped during the release build without naming a failure, and its last lines are: building calc | undefined: calc.Add") {
		t.Errorf("detail = %q, want the build's last lines", detail)
	}
	f.again(t, 1)
}

// A refusal quoting more than its record holds says how much it left out.
func TestWhatARefusalQuotesIsBounded(t *testing.T) {
	t.Parallel()
	output := &cutOutput{}
	output.observe(execution.Output{Stream: execution.StreamStdout, Text: "=== gate: make check"})
	for n := range 50 {
		output.observe(execution.Output{Stream: execution.StreamStdout, Text: fmt.Sprintf("--- FAIL: Test%d %s", n, strings.Repeat("x", 500))})
	}
	said := output.failure()
	if len(said) > maxQuotedBytes+200 || !strings.HasSuffix(said, " more") {
		t.Errorf("a long refusal is %d bytes ending %q", len(said), said[max(len(said)-40, 0):])
	}
}
