package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/teamrecords"
)

// With team mode off nothing is wired: no Sharer is built and nothing reads the
// remote.
func TestTeamRecordsIsOffWithoutTeamMode(t *testing.T) {
	t.Parallel()

	_, on, err := teamRecords(context.Background(), components{})
	if err != nil || on {
		t.Fatalf("teamRecords() = on %v, error %v, want nothing wired and no error", on, err)
	}
}

// A forge check with team mode off says nothing about shared records, in either
// output form.
func TestForgeCheckOutputCarriesNoRecordsWithoutTeamMode(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := reportForgeCheck(&stdout, &stderr, true, forgeCheckOutput{}, nil); code != 0 {
		t.Fatalf("reportForgeCheck() = %d, stderr %q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "records") {
		t.Fatalf("output = %s, want no records with team mode off", stdout.String())
	}
}

// With team mode on, a forge check's text output says what sharing the
// team's records did, and says it before the closing count.
func TestTheForgeCheckSaysWhatSharingRecordsDidBeforeItsCount(t *testing.T) {
	t.Parallel()

	output := forgeCheckOutput{Records: &teamrecords.Result{Copied: 2, Shared: 1}}
	var stdout, stderr bytes.Buffer
	printForgeCheckFindings(&stdout, &stderr, output)
	said := stdout.String()
	line := "shared records: copied 2 from other factories, shared 1"
	at := strings.Index(said, line)
	if at < 0 {
		t.Fatalf("stdout lacks %q:\n%s", line, said)
	}
	if count := strings.Index(said, "forge check:"); count < 0 || count < at {
		t.Fatalf("the closing count is missing or comes before the records line:\n%s", said)
	}

	stdout.Reset()
	printForgeCheckFindings(&stdout, &stderr, forgeCheckOutput{})
	if strings.Contains(stdout.String(), "shared records:") {
		t.Fatalf("with team mode off nothing about shared records is said:\n%s", stdout.String())
	}
}
