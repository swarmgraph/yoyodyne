package cli

import (
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Every tracker call a yoyo process makes is kept in the product's log of
// them (runstate, trackercalllog.go), so the maintenance pass can count the
// harness's own load on the store by caller and read how long its listings
// took. The log is installed once, the first time the process loads a
// product's configuration, because that is where the product and its state
// directory become known; a command that loads none, such as the export the
// pre-commit hook takes, keeps nothing. Only the yoyo executable turns it on
// (KeepTrackerCalls), so a test that runs a command in this package writes no
// log into whichever state directory it loaded first.

var keptTrackerCalls struct {
	sync.Mutex
	process   string
	enabled   bool
	installed bool
}

// KeepTrackerCalls has this process keep every tracker call it makes from the
// first configuration it loads, named for the command it was started as.
func KeepTrackerCalls(args []string) {
	keptTrackerCalls.Lock()
	defer keptTrackerCalls.Unlock()
	keptTrackerCalls.enabled, keptTrackerCalls.process = true, trackerCallProcess(args)
}

// trackerCallProcess is what a process's calls are counted against when they
// name no caller: the command it was started as, such as "yoyo chat" or
// "yoyo work --watch".
func trackerCallProcess(args []string) string {
	process := "yoyo"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		process += " " + args[0]
	}
	if slices.Contains(args, "--watch") {
		process += " --watch"
	}
	return process
}

// installTrackerCallLog names this process's log of tracker calls, the first
// time a configuration is loaded where KeepTrackerCalls was called. A log that
// cannot be opened is not an error: the command runs, and its calls are not
// kept.
func installTrackerCallLog(resolved config.Resolved) {
	keptTrackerCalls.Lock()
	defer keptTrackerCalls.Unlock()
	if !keptTrackerCalls.enabled || keptTrackerCalls.installed {
		return
	}
	keptTrackerCalls.installed = true
	stateRoot, _, err := agreeProductStateRoot(resolved, os.Getenv, os.UserHomeDir, runtime.GOOS)
	if err != nil {
		return
	}
	log, err := runstate.NewTrackerCallLogStore(stateRoot, resolved.Config.Product.ID, keptTrackerCalls.process)
	if err != nil {
		return
	}
	beads.LogCallsTo(log)
}
