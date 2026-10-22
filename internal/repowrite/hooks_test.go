package repowrite

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A test hook changes what a confined write does, so no program but a test
// binary may set one. This builds an ordinary program that tries, runs it, and
// requires it to stop with the refusal rather than set the hook.
func TestATestHookCannotBeSetOutsideATestBinary(t *testing.T) {
	t.Parallel()
	goTool, err := exec.LookPath("go")
	if err != nil {
		goTool = filepath.Join(os.Getenv("GOROOT"), "bin", "go")
		if _, statErr := os.Stat(goTool); statErr != nil {
			t.Fatalf("the go command is needed to build a program that is not a test: %v", err)
		}
	}
	program := filepath.Join(t.TempDir(), "hookoutsidetests")
	build := exec.Command(goTool, "build", "-o", program, "./testdata/hookoutsidetests")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the program that sets a hook: %v\n%s", err, output)
	}
	output, err := exec.Command(program).CombinedOutput()
	if err == nil {
		t.Fatalf("a program that is not a test set a test hook and exited cleanly:\n%s", output)
	}
	if !strings.Contains(string(output), "repowrite.SetTestHook is only for tests") || strings.Contains(string(output), "the hook was set") {
		t.Fatalf("the program did not stop with the refusal to set a hook:\n%s", output)
	}
}
