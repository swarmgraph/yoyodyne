package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestTrackerExportAsksForItsSubcommandAndRefusesACheckoutWithNoTracker(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"tracker"}, &stdout, &stderr, "test"); code != 2 || !strings.Contains(stderr.String(), "yoyo tracker export") {
		t.Fatalf("tracker with no subcommand = %d, %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"tracker", "export", "--dir", t.TempDir()}, &stdout, &stderr, "test"); code != 1 || !strings.Contains(stderr.String(), "no tracker store is reachable") {
		t.Fatalf("tracker export with no tracker = %d, %q, %q", code, stdout.String(), stderr.String())
	}
}
