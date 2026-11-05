package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// `yoyo tracker slow-calls` says the product's recorded slow tracker calls
// together, so nobody reads the files one by one.
func TestTrackerSlowCallsSummarizesTheRecordedCalls(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewTrackerSlowCallStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	for index, who := range []string{"yoyo reconcile", "yoyo reconcile", "the development manager's conversation"} {
		if err := store.RecordSlowCall(beads.SlowCall{Call: "listing open work", Verb: "list", For: who,
			StartedAt: started.Add(time.Duration(index) * time.Minute), Ran: 2 * time.Minute, Limit: 2 * time.Minute,
			StoreBytes: 1000 << 20, Holders: []beads.StoreHolder{{PID: 7, Command: "bd export -o .beads/issues.jsonl"}}}); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr, code := runCLI(t, "tracker", "slow-calls", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"3 tracker calls ran out of time",
		"yoyo reconcile: 2 calls",
		"the development manager's conversation: 1 call",
		"bd export, while 3 calls waited",
		"Each call's own record is in " + store.Path(),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q in it", stdout, want)
		}
	}
}

func TestATrackerCallProcessIsNamedForItsCommand(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"work", "--watch"}, "yoyo work --watch"},
		{[]string{"chat"}, "yoyo chat"},
		{[]string{"--config", "x"}, "yoyo"},
		{nil, "yoyo"},
	} {
		if got := trackerCallProcess(test.args); got != test.want {
			t.Errorf("trackerCallProcess(%v) = %q, want %q", test.args, got, test.want)
		}
	}
}

// A process that keeps its tracker calls starts keeping them once it loads a
// product's configuration, under the product's state, named for its command.
//
// Not parallel: it names the log every tracker client in this process hands
// its calls to, and the state root the configuration resolves to.
func TestAProcessThatKeepsTrackerCallsLogsThemOnceItLoadsAConfiguration(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	keptTrackerCalls.Lock()
	saved := struct {
		process            string
		enabled, installed bool
	}{keptTrackerCalls.process, keptTrackerCalls.enabled, keptTrackerCalls.installed}
	keptTrackerCalls.installed = false
	keptTrackerCalls.Unlock()
	t.Cleanup(func() {
		beads.LogCallsTo(nil)
		keptTrackerCalls.Lock()
		keptTrackerCalls.process, keptTrackerCalls.enabled, keptTrackerCalls.installed = saved.process, saved.enabled, saved.installed
		keptTrackerCalls.Unlock()
	})

	KeepTrackerCalls([]string{"chat"})
	if _, err := loadConfiguration(configPath); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{stdout: "[]"}
	if _, err := (beads.Client{Runner: runner, Dir: t.TempDir()}).Ready(beads.OnBehalfOf(context.Background(), "the architect's conversation")); err != nil {
		t.Fatal(err)
	}
	log, err := runstate.NewTrackerCallLogStore(stateRoot, "yoyodyne", "")
	if err != nil {
		t.Fatal(err)
	}
	records, _, err := log.Read(time.Now().Add(-time.Hour), time.Now().Add(time.Minute))
	if err != nil || len(records) == 0 {
		t.Fatalf("Read() = %+v, %v, want the call kept", records, err)
	}
	if records[0].Process != "yoyo chat" || records[0].For != "the architect's conversation" || records[0].Verb != "ready" {
		t.Errorf("kept %+v, want the ready listing kept for the architect's conversation from yoyo chat", records[0])
	}
}
