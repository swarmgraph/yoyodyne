package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
