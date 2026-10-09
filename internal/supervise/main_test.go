package supervise

import (
	"os"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/home/hometest"
)

// Every test here records into a state root of its own, and none leaves a
// record in the machine's live home (hometest).
func TestMain(m *testing.M) {
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
