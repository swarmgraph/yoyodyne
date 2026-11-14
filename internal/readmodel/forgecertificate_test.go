package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeForgeCertificate struct {
	record runstate.ForgeCertificateFailure
	err    error
}

func (f fakeForgeCertificate) Read() (runstate.ForgeCertificateFailure, error) {
	return f.record, f.err
}

func forgeCertificateEntries(entries []Attention) []Attention {
	var found []Attention
	for _, entry := range entries {
		if entry.Kind == AttentionForgeCertificate {
			found = append(found, entry)
		}
	}
	return found
}

// A supervisor whose forge checks all fail on the certificate store is said
// once on each line, with the cause and the fix, and only while it is the
// supervisor running.
func TestAForgeCertificateFailureIsSaidOnceWhileItsSupervisorRuns(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 9, 30, 18, 13, 0, 0, time.UTC)
	failing := func(pid int) runstate.ForgeCertificateFailure {
		return runstate.ForgeCertificateFailure{
			SupervisorPID: pid, Since: since, Checks: 4, LatestAt: since.Add(time.Hour),
			Latest: "x509: OSStatus -26276",
		}
	}
	for name, test := range map[string]struct {
		running bool
		record  runstate.ForgeCertificateFailure
		want    int
	}{
		"the supervisor that failed is running": {true, failing(4242), 1},
		"a different supervisor is running":     {true, failing(99), 0},
		"the supervisor has stopped":            {false, failing(4242), 0},
		"nothing is failing":                    {true, runstate.ForgeCertificateFailure{SupervisorPID: 4242}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			sources := quietSources()
			sources.Supervision = fakeSupervision{running: test.running, found: true, recorded: runstate.Supervision{PID: 4242}}
			sources.ForgeCertificates = fakeForgeCertificate{record: test.record}
			standing := ReadStanding(context.Background(), sources)
			problems := forgeCertificateEntries(standing.FactoryProblems)
			needs := forgeCertificateEntries(standing.NeedsHuman)
			if len(problems) != test.want || len(needs) != test.want {
				t.Fatalf("FactoryProblems = %+v, NeedsHuman = %+v, want %d of the kind on each", standing.FactoryProblems, standing.NeedsHuman, test.want)
			}
			if test.want == 0 {
				return
			}
			got := needs[0]
			if !strings.Contains(got.What(), "system certificate store") || !strings.Contains(got.Whose(), "yoyo stop") || !got.Since().Equal(since) {
				t.Errorf("entry = %q / %q / %s, want the cause, the fix, and since when", got.What(), got.Whose(), got.Since())
			}
			if got.ID != "4242:2026-09-30T18:13:00Z" {
				t.Errorf("ID = %q", got.ID)
			}
		})
	}
}

// A record that cannot be read is said as that, never as checks that are well.
func TestAnUnreadableForgeCertificateRecordIsSaid(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.ForgeCertificates = fakeForgeCertificate{err: errors.New("decode forge certificate record: bad")}
	standing := ReadStanding(context.Background(), sources)
	if !strings.Contains(standing.FactoryProblemsProblem, "forge checks are failing on the system certificate store could not be read") {
		t.Fatalf("FactoryProblemsProblem = %q", standing.FactoryProblemsProblem)
	}
}
