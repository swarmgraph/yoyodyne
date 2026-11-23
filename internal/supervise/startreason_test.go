package supervise

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Each way a supervisor starts is told apart by the trace it leaves, and the
// start at login after a restart is the one the launch agent makes when the
// last record predates the machine starting.
func TestStartReasonTellsEachWayASupervisorStartsApart(t *testing.T) {
	t.Parallel()
	const agent = "com.yoyodyne.supervisor.yoyodyne"
	booted := time.Date(2026, 11, 12, 8, 9, 0, 0, time.UTC)
	beforeBoot := runstate.Supervision{PID: 411, Build: "aaaaaaaaaaaaaaaa", StartedAt: booted.Add(-48 * time.Hour), ObservedAt: booted.Add(-time.Minute)}
	sinceBoot := runstate.Supervision{PID: 411, Build: "aaaaaaaaaaaaaaaa", StartedAt: booted.Add(time.Minute), ObservedAt: booted.Add(time.Hour)}
	underAgent := []string{"HOME=/Users/mason", execution.LaunchdJobVariable + "=" + agent}
	cases := []struct {
		name  string
		facts StartFacts
		want  string
	}{
		{
			name:  "the launch agent at login after a restart",
			facts: StartFacts{LaunchAgent: agent, PID: 512, Build: "bbbb", Previous: beforeBoot, PreviousFound: true, Environ: underAgent, BootedAt: booted},
			want:  "its launch agent " + agent + " started it when you logged in, after the machine started at",
		},
		{
			name:  "the launch agent at the first login there has been",
			facts: StartFacts{LaunchAgent: agent, PID: 512, Build: "bbbb", Environ: underAgent, BootedAt: booted},
			want:  "started it when you logged in",
		},
		{
			name:  "the launch agent while the machine has been up",
			facts: StartFacts{LaunchAgent: agent, PID: 512, Build: "bbbb", Previous: sinceBoot, PreviousFound: true, Environ: underAgent, BootedAt: booted},
			want:  "its launch agent " + agent + " started it: the supervisor before it had exited",
		},
		{
			name:  "the launch agent where when the machine started is not known",
			facts: StartFacts{LaunchAgent: agent, PID: 512, Build: "bbbb", Previous: beforeBoot, PreviousFound: true, Environ: underAgent},
			want:  "its launch agent " + agent + " started it: the supervisor before it had exited",
		},
		{
			name:  "taking up a deployed build",
			facts: StartFacts{LaunchAgent: agent, PID: 411, Build: "bbbbbbbbbbbbbbbb", Previous: sinceBoot, PreviousFound: true, Environ: underAgent, BootedAt: booted},
			want:  "re-executed itself to take up the deployed build bbbbbbbbbbbb",
		},
		{
			name:  "a process number reused after a restart is not a re-execution",
			facts: StartFacts{LaunchAgent: agent, PID: 411, Build: "bbbb", Previous: beforeBoot, PreviousFound: true, Environ: underAgent, BootedAt: booted},
			want:  "started it when you logged in",
		},
		{
			name:  "yoyo start",
			facts: StartFacts{LaunchAgent: agent, PID: 512, Environ: []string{StartVariable + "=" + StartedByYoyoStart}, BootedAt: booted},
			want:  StartedByYoyoStart,
		},
		{
			name:  "another product's launch agent is not this one's",
			facts: StartFacts{LaunchAgent: agent, PID: 512, Environ: []string{execution.LaunchdJobVariable + "=com.yoyodyne.supervisor.other"}, BootedAt: booted},
			want:  "started directly with `yoyo start --foreground`",
		},
		{
			name:  "typed in a terminal",
			facts: StartFacts{LaunchAgent: agent, PID: 512, Environ: []string{execution.LaunchdJobVariable + "=0"}},
			want:  "started directly with `yoyo start --foreground`",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := StartReason(tc.facts); !strings.Contains(got, tc.want) {
				t.Errorf("StartReason() = %q, want %q in it", got, tc.want)
			}
		})
	}
}

func TestParseBootTimeReadsSysctl(t *testing.T) {
	t.Parallel()
	got, err := parseBootTime("{ sec = 1762934941, usec = 81268 } Wed Nov 12 00:09:01 2026\n")
	if err != nil || !got.Equal(time.Unix(1762934941, 0)) {
		t.Errorf("parseBootTime() = %v, %v, want %v", got, err, time.Unix(1762934941, 0))
	}
	if _, err := parseBootTime("kern.boottime: unknown oid"); err == nil {
		t.Error("parseBootTime() of an answer with no seconds returned no error")
	}
}

// The supervisor records why it started, so `yoyo status` can say it.
func TestTheRecordCarriesWhyTheSupervisorStarted(t *testing.T) {
	t.Parallel()
	s := &Supervisor{Product: "yoyodyne", PID: 512, StartedBy: StartedByYoyoStart, startedAt: time.Now()}
	if got := s.Supervision(time.Now()); got.StartedBy != StartedByYoyoStart {
		t.Errorf("Supervision().StartedBy = %q, want %q", got.StartedBy, StartedByYoyoStart)
	}
}
