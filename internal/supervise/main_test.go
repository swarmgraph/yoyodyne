package supervise

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/home/hometest"
)

// Every test here records into a state root of its own, and none leaves a
// record in the machine's live home (hometest).
func TestMain(m *testing.M) {
	if os.Getenv(ignoringStopVariable) != "" {
		ignoreTheStop()
		return
	}
	os.Exit(hometest.GuardLiveHomes(func() int {
		root, err := os.MkdirTemp("", "yoyodyne-supervise-state-")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(root)
		if err := os.Setenv(home.StateHomeVariable, root); err != nil {
			panic(err)
		}
		return m.Run()
	}, os.Stderr))
}

// ignoringStopVariable makes this test binary, started again by a test, a
// process that ignores the stop signal and has started a child leading a
// process group of its own, as a scheduler hosting an AI session has. It is
// the test binary rather than a shell because whether a shell puts a
// background job in a group of its own depends on the shell and on having a
// terminal.
const ignoringStopVariable = "YOYODYNE_SUPERVISE_TEST_IGNORE_STOP"

// ignoreTheStop is that process. It and its child each end by themselves
// after a minute however the test that started it ends.
func ignoreTheStop() {
	signal.Ignore(syscall.SIGTERM)
	child := exec.Command("/bin/sleep", "60")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	time.Sleep(time.Minute)
}
