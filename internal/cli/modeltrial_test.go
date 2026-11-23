package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestModelTrialDefectRefusesAMissingRunOrNote(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"model-trial", "defect", "--note", "broke the status line"},
		{"model-trial", "defect", "run-1"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr, "test"); code != 2 {
			t.Fatalf("Run(%v) code = %d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "takes one run id and a --note") {
			t.Fatalf("Run(%v) stderr = %q", args, stderr.String())
		}
	}
}

func TestModelTrialReviewsTakesNoArguments(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"model-trial", "reviews", "run-1"}, &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `model-trial reviews does not take "run-1"`) || !strings.Contains(stderr.String(), "yoyo model-trial reviews [options]") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
