package checks

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A developer environment's check is started under the developer's
// restrictions, through the same runner as a declared check, bound by its own
// time limit, and given nothing an install step is given; a harness one is
// started exactly as a declared check is.
func TestAnEnvironmentsCheckRunsWhereItsPlaceSays(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	scratch := t.TempDir()
	for _, developer := range []bool{true, false} {
		process := &recordingProcess{}
		request := EnvironmentRequest{RunID: "run-a", Directory: worktree, Command: "scripts/ready-browser.sh", TimeLimit: 3 * time.Minute}
		if developer {
			request.Restrictions = &DeveloperRestrictions{Worktree: worktree, Scratch: scratch}
		}
		result, _, err := RunEnvironmentCheckWith(context.Background(), Runner{Process: process}, request, nil)
		if developer && runtime.GOOS != "darwin" {
			// The developer's restrictions are reproduced only on macOS, and a
			// check that cannot have them is refused rather than run without.
			if err == nil || len(process.commands) != 0 {
				t.Fatalf("off macOS: error %v, commands %#v; want the check refused", err, process.commands)
			}
			continue
		}
		if err != nil {
			t.Fatalf("developer=%v: error = %v", developer, err)
		}
		if !result.Passed || len(process.commands) != 1 {
			t.Fatalf("developer=%v: result %#v, commands %#v", developer, result, process.commands)
		}
		started := process.commands[0]
		if started.Timeout != 3*time.Minute {
			t.Fatalf("developer=%v: timeout %s, want the environment's own limit", developer, started.Timeout)
		}
		for _, variable := range started.Env {
			if strings.HasPrefix(variable, InstallCacheVariable+"=") {
				t.Fatalf("developer=%v: given the install cache: %q", developer, variable)
			}
		}
		if developer {
			if started.Name != sandboxExec || !strings.Contains(started.Args[1], "(deny network*)") || started.Args[2] != "/bin/sh" || started.Args[len(started.Args)-1] != "scripts/ready-browser.sh" {
				t.Fatalf("developer check started as %s %q, want it under the developer's restrictions", started.Name, started.Args)
			}
		} else if started.Name != "/bin/sh" || started.Args[len(started.Args)-1] != "scripts/ready-browser.sh" {
			t.Fatalf("harness check started as %s %q, want it as a declared check", started.Name, started.Args)
		}
	}
}

type recordingProcess struct {
	commands []execution.Command
}

func (p *recordingProcess) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	p.commands = append(p.commands, command)
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}
