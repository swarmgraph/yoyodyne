package supervise

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// servingProcess is a stand-in for dolt: a process that only sleeps, and a
// listener on the port dolt was asked to listen on. It answers for what holds
// the database, and for the commands run beside it.
type servingProcess struct {
	t        *testing.T
	mu       sync.Mutex
	launched [][]string
	environs [][]string
	pids     []int
	listener net.Listener
	account  string
	commands []execution.Command
	ignored  bool
	refuse   bool
	// silent launches a process that never listens and never holds the database.
	silent    bool
	silentPID int
}

func (s *servingProcess) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, command)
	if command.Stdin != nil {
		said, _ := io.ReadAll(command.Stdin)
		s.account = string(said)
	}
	if command.Name == "git" && !s.ignored {
		return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

func (s *servingProcess) launch(_ string, args []string, _ string, environ []string, _, _ string) (int, error) {
	if s.refuse {
		return 0, errors.New("no such program")
	}
	process := exec.Command("sleep", "60")
	if err := process.Start(); err != nil {
		return 0, err
	}
	pid := process.Process.Pid
	s.t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	if s.silent {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.launched = append(s.launched, args)
		s.silentPID = pid
		return pid, nil
	}
	port := args[slices.Index(args, "--port")+1]
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return 0, err
	}
	s.t.Cleanup(func() { listener.Close() })
	s.mu.Lock()
	defer s.mu.Unlock()
	s.launched, s.environs, s.pids, s.listener = append(s.launched, args), append(s.environs, environ), append(s.pids, pid), listener
	return pid, nil
}

// Holders is every launched process that still runs.
func (s *servingProcess) Holders(context.Context, string) ([]beads.StoreHolder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var holders []beads.StoreHolder
	for _, pid := range s.pids {
		if look, err := lookProcess(pid); err == nil && look.Exists && !look.Exited {
			holders = append(holders, beads.StoreHolder{PID: pid})
		}
	}
	return holders, nil
}

func newTrackerServer(t *testing.T) (*TrackerServer, *servingProcess, string) {
	t.Helper()
	repository, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(repository, ".beads")
	if err := os.MkdirAll(filepath.Join(store, beads.EmbeddedDatabase, "calc"), 0o700); err != nil {
		t.Fatal(err)
	}
	process := &servingProcess{t: t, ignored: true}
	server := &TrackerServer{
		Tracker:    beads.Client{Runner: process, Dir: repository, Timeout: 5 * time.Second, Holders: process},
		Repository: repository,
		StateRoot:  t.TempDir(),
		Product:    "calc",
		Program:    func() (string, error) { return "/tools/dolt", nil },
		Build:      "build-1",
		Runner:     process,
		Launch:     process.launch,
		Log:        t.Logf,
	}
	return server, process, store
}

func recordOf(t *testing.T, store string) beads.ServerRecord {
	t.Helper()
	record, _, err := beads.ReadServerRecord(store)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestTheServerIsStartedOnThisMachineOnlyBehindAPasswordKeptInTheServicesFolder(t *testing.T) {
	server, process, store := newTrackerServer(t)
	server.Look(t.Context(), time.Now())
	record := recordOf(t, store)
	if record.State != beads.ServerRunning || record.PID != process.pids[0] || record.Port == 0 || record.StartedAt.IsZero() {
		t.Fatalf("record = %#v", record)
	}
	args := strings.Join(process.launched[0], " ")
	if !strings.Contains(args, "--host 127.0.0.1 --port "+strconv.Itoa(record.Port)+" ") || !strings.Contains(args, "--skip-root-user-initialization") {
		t.Errorf("dolt was started with %s", args)
	}
	services, _ := execution.PrepareServicesDirectory(server.StateRoot)
	if !strings.HasPrefix(record.Credentials, services+"/") || !strings.Contains(args, "--privilege-file "+services+"/") {
		t.Errorf("a credential is outside %s: %s, %s", services, record.Credentials, args)
	}
	info, err := os.Stat(record.Credentials)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials: %v, %v", info, err)
	}
	content, _ := os.ReadFile(record.Credentials)
	_, password, _ := strings.Cut(strings.TrimSpace(string(content)), "password=")
	if len(password) != 64 || !strings.Contains(process.account, password) {
		t.Fatalf("the account was not made with the kept password")
	}
	if target, err := os.Readlink(filepath.Join(store, beads.EmbeddedDatabase, beads.ServerLink)); err != nil || !strings.HasPrefix(target, services+"/") {
		t.Errorf("dolt's own secret would be kept at %q (%v)", target, err)
	}
	// The password is in no argument, environment, record or setting.
	said := []string{args, strings.Join(process.environs[0], " ")}
	for _, command := range process.commands {
		said = append(said, strings.Join(command.Args, " "), strings.Join(command.Env, " "))
	}
	for _, name := range []string{beads.LocalSettingPath(store), filepath.Join(store, "yoyodyne-writes", "tracker-server.json")} {
		kept, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		said = append(said, string(kept))
	}
	for _, each := range said {
		if strings.Contains(each, password) {
			t.Errorf("the password is in %q", each)
		}
	}
	server.Look(t.Context(), time.Now())
	if len(process.launched) != 1 {
		t.Errorf("a running server was started again")
	}
}

func TestTheServerIsStartedAgainWhenItDiesOrTheBuildMoves(t *testing.T) {
	server, process, store := newTrackerServer(t)
	server.Look(t.Context(), time.Now())
	first := recordOf(t, store)
	server.Build = "build-2"
	server.Look(t.Context(), time.Now())
	second := recordOf(t, store)
	if second.PID == first.PID || second.Build != "build-2" || second.State != beads.ServerRunning {
		t.Fatalf("after a new build record = %#v", second)
	}
	if runs, _ := server.Tracker.ServerProcessRuns(t.Context(), first.PID, ""); runs {
		t.Error("the earlier server was left running")
	}
	process.listener.Close()
	_ = killTree(second.PID)
	server.Look(t.Context(), time.Now())
	if third := recordOf(t, store); third.PID == second.PID || third.State != beads.ServerRunning {
		t.Fatalf("after it died record = %#v", third)
	}
}

func TestTheServerIsNotStartedWhileATrackerCallIsInProgress(t *testing.T) {
	server, process, store := newTrackerServer(t)
	server.Tracker.Timeout = 100 * time.Millisecond
	other := server.Tracker
	unlock, err := other.HoldStore(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	server.Look(t.Context(), now)
	unlock()
	if len(process.launched) != 0 {
		t.Fatal("the server was started without the store lock")
	}
	server.Look(t.Context(), now.Add(time.Second))
	if len(process.launched) != 0 {
		t.Fatal("a start that failed was not waited on")
	}
	server.Look(t.Context(), now.Add(trackerServerFirstWait))
	if recordOf(t, store).State != beads.ServerRunning {
		t.Fatal("the server was not started once the lock was free")
	}
}

func TestStoppingTheServerReturnsTheStoreToDirectOpening(t *testing.T) {
	server, process, store := newTrackerServer(t)
	server.Look(t.Context(), time.Now())
	pid := recordOf(t, store).PID
	if said, err := server.StopIfRunning(t.Context(), "the product was stopped"); err != nil || said == "" {
		t.Fatalf("StopIfRunning() = %q, %v", said, err)
	}
	record := recordOf(t, store)
	if record.State != beads.ServerStopped || record.Reason != "the product was stopped" {
		t.Fatalf("record = %#v", record)
	}
	if holders, _ := process.Holders(t.Context(), ""); len(holders) != 0 {
		t.Errorf("process %d still runs", pid)
	}
	for _, name := range []string{beads.LocalSettingPath(store), filepath.Join(store, beads.EmbeddedDatabase, beads.ServerLink)} {
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was left: %v", name, err)
		}
	}
}

func TestAHeldServerIsNeverStartedAndOneFoundIsStopped(t *testing.T) {
	server, process, store := newTrackerServer(t)
	server.Hold = "not yet"
	server.Look(t.Context(), time.Now())
	if _, found, _ := beads.ReadServerRecord(store); found || len(process.launched) != 0 {
		t.Fatal("a held server left a record or was started")
	}
	if err := server.StartHeld(t.Context()); err == nil {
		t.Fatal("StartHeld() started a held server")
	}
	server.Hold = ""
	server.Look(t.Context(), time.Now())
	server.Hold = "not any more"
	server.Look(t.Context(), time.Now())
	if record := recordOf(t, store); record.State != beads.ServerStopped || record.Reason != "not any more" {
		t.Fatalf("record = %#v", record)
	}
}

func TestAServerThatCannotBeStartedLeavesTheStoreOpenedDirectlyAndSaysWhy(t *testing.T) {
	for name, test := range map[string]struct {
		arrange func(*TrackerServer, *servingProcess)
		want    string
	}{
		"no dolt": {func(s *TrackerServer, _ *servingProcess) {
			s.Program = func() (string, error) { return "", errors.New("dolt is not installed") }
		}, "dolt is not installed"},
		"a start that fails":   {func(_ *TrackerServer, p *servingProcess) { p.refuse = true }, "no such program"},
		"a setting git tracks": {func(_ *TrackerServer, p *servingProcess) { p.ignored = false }, "could be committed"},
		"a directory at .dolt": {func(s *TrackerServer, _ *servingProcess) {
			_, database := s.Tracker.LiveStore()
			_ = os.Mkdir(filepath.Join(database, beads.ServerLink), 0o700)
		}, "is not the link"},
	} {
		t.Run(name, func(t *testing.T) {
			server, process, store := newTrackerServer(t)
			test.arrange(server, process)
			server.Look(t.Context(), time.Now())
			if record := recordOf(t, store); record.State != beads.ServerStopped || !strings.Contains(record.Reason, test.want) || len(process.launched) != 0 {
				t.Fatalf("record = %#v, launched %d", record, len(process.launched))
			}
		})
	}
}

// A process that was launched and never became a server is ended before the
// store is recorded as opened directly, though it holds nothing.
func TestAServerThatNeverBecomesReadyIsEnded(t *testing.T) {
	for name, unknownStart := range map[string]bool{"its start time known": false, "its start time not known": true} {
		t.Run(name, func(t *testing.T) {
			server, process, store := newTrackerServer(t)
			process.silent, server.readyLimit = true, 100*time.Millisecond
			if unknownStart {
				lookedAt := lookProcess
				lookProcess = func(pid int) (runstate.ProcessLook, error) {
					look, err := lookedAt(pid)
					look.StartedAt = time.Time{}
					return look, err
				}
				t.Cleanup(func() { lookProcess = lookedAt })
			}
			server.Look(t.Context(), time.Now())
			record := recordOf(t, store)
			if record.State != beads.ServerStopped || record.PID != process.silentPID {
				t.Fatalf("record = %#v, want stopped naming process %d", record, process.silentPID)
			}
			if look, err := lookProcess(process.silentPID); err != nil || look.Running() {
				t.Fatalf("process %d still runs: %v", process.silentPID, err)
			}
		})
	}
}

// A server whose process will not end is not recorded as stopped, nothing of
// its is cleared away, and no other is started beside it.
func TestAServerThatWillNotEndIsNeverRecordedStoppedOrStartedOver(t *testing.T) {
	server, process, store := newTrackerServer(t)
	server.Look(t.Context(), time.Now())
	before := recordOf(t, store)
	server.stopLimit = 50 * time.Millisecond
	lookedAt := lookProcess
	lookProcess = func(int) (runstate.ProcessLook, error) {
		return runstate.ProcessLook{Exists: true, StartedAt: before.ProcessStarted}, nil
	}
	t.Cleanup(func() { lookProcess = lookedAt })
	if ended, err := server.StopHeld(t.Context(), "the store is being rebuilt"); ended || err == nil {
		t.Fatalf("StopHeld() = %v, %v, want an error and nothing ended", ended, err)
	}
	if err := server.StartHeld(t.Context()); err == nil || len(process.launched) != 1 {
		t.Fatalf("StartHeld() = %v after %d launches, want it refused", err, len(process.launched))
	}
	if after := recordOf(t, store); after != before {
		t.Errorf("the record became %#v", after)
	}
	for _, kept := range []string{before.Credentials, beads.LocalSettingPath(store)} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("the running server's %s is gone: %v", kept, err)
		}
	}
}

// The record a stop leaves names the process it stopped, so a call asks whether
// that process is gone before it opens the store directly.
func TestTheRecordAStopLeavesIsCheckedAgainstItsProcess(t *testing.T) {
	server, process, store := newTrackerServer(t)
	server.Look(t.Context(), time.Now())
	pid := recordOf(t, store).PID
	if _, err := server.Stop(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
	record := recordOf(t, store)
	if record.State != beads.ServerStopped || record.PID != pid {
		t.Fatalf("record = %#v, want stopped naming process %d", record, pid)
	}
	client := server.Tracker
	client.Timeout = 300 * time.Millisecond
	if reach, _ := client.Reach(t.Context()); reach.Waiting || reach.ThroughServer {
		t.Fatalf("Reach() = %#v, want direct opening", reach)
	}
	// A stopped record whose process turns out to hold the store.
	process.mu.Lock()
	process.pids = []int{os.Getpid()}
	process.mu.Unlock()
	record.PID = os.Getpid()
	if err := beads.WriteServerRecord(store, record); err != nil {
		t.Fatal(err)
	}
	if reach, _ := client.Reach(t.Context()); !reach.Waiting {
		t.Fatalf("Reach() = %#v, want calls to wait", reach)
	}
}

// A dolt server nobody recorded, holding the store, is never recorded away.
func TestAServerTheRecordDoesNotNameKeepsTheRecordFromSayingStopped(t *testing.T) {
	server, process, store := newTrackerServer(t)
	if err := beads.WriteServerRecord(store, beads.ServerRecord{State: beads.ServerStarting}); err != nil {
		t.Fatal(err)
	}
	server.Tracker.Holders = namedHolder{}
	server.Look(t.Context(), time.Now())
	if record := recordOf(t, store); record.State != beads.ServerStarting || len(process.launched) != 0 {
		t.Fatalf("record = %#v after %d launches", record, len(process.launched))
	}
}

type namedHolder struct{}

func (namedHolder) Holders(context.Context, string) ([]beads.StoreHolder, error) {
	return []beads.StoreHolder{{PID: 1, Command: "dolt sql-server --data-dir x"}}, nil
}

// A server started by a supervisor that died before recording it is found
// through the file dolt keeps, and stopped before another is started.
func TestAServerLeftUnrecordedIsStoppedFirst(t *testing.T) {
	server, process, store := newTrackerServer(t)
	server.Look(t.Context(), time.Now())
	left := recordOf(t, store).PID
	directory, _ := server.directory()
	if err := os.WriteFile(filepath.Join(directory, trackerServerInfo), []byte(strconv.Itoa(left)+":1:secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := beads.WriteServerRecord(store, beads.ServerRecord{State: beads.ServerStarting}); err != nil {
		t.Fatal(err)
	}
	server.Look(t.Context(), time.Now())
	holders, _ := process.Holders(t.Context(), "")
	if len(holders) != 1 || holders[0].PID == left {
		t.Fatalf("the processes left running are %v, want only the new server", holders)
	}
}

// With the real dolt and bd: the server refuses a connection without its
// password, bd reads through it by the local setting alone, nothing in the store
// holds a secret, and the store opens directly again once it is stopped.
func TestTheServerConformance(t *testing.T) {
	for _, program := range []string{"bd", "dolt", "git", "lsof"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("SKIPPED, not passed: %s is not installed", program)
		}
	}
	dolt, _ := exec.LookPath("dolt")
	repository, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := func(environ []string, name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, name, args...)
		command.Dir, command.Env = repository, append(os.Environ(), environ...)
		said, err := command.CombinedOutput()
		return string(said), err
	}
	for _, args := range [][]string{{"git", "init", "-q"}, {"git", "config", "user.email", "t@example.invalid"}, {"git", "config", "user.name", "t"}, {"bd", "init", "--prefix", "calc", "--non-interactive", "--skip-hooks", "--skip-agents"}, {"bd", "create", "first"}} {
		if said, err := run(nil, args[0], args[1:]...); err != nil {
			t.Fatalf("%v: %v: %s", args, err, said)
		}
	}
	runner := execution.OSProcessRunner{}
	server := &TrackerServer{
		Tracker:    beads.Client{Runner: runner, Dir: repository, Timeout: 30 * time.Second},
		Repository: repository, StateRoot: t.TempDir(), Product: "calc", Build: "b", Runner: runner,
		Program: func() (string, error) { return dolt, nil },
		Launch: func(program string, args []string, dir string, environ []string, logRoot, log string) (int, error) {
			command := exec.Command(program, args...)
			command.Dir, command.Env = dir, environ
			output, err := os.Create(filepath.Join(logRoot, log))
			if err != nil {
				return 0, err
			}
			defer output.Close()
			command.Stdout, command.Stderr = output, output
			if err := command.Start(); err != nil {
				return 0, err
			}
			t.Cleanup(func() { _ = command.Process.Kill() })
			return command.Process.Pid, nil
		},
		Log: t.Logf,
	}
	server.Look(t.Context(), time.Now())
	store, database := server.Tracker.LiveStore()
	record := recordOf(t, store)
	if record.State != beads.ServerRunning {
		directory, _ := server.directory()
		said, _ := os.ReadFile(filepath.Join(directory, trackerServerLog))
		t.Fatalf("record = %#v, and dolt said %s", record, said)
	}
	if _, err := server.Tracker.Create(t.Context(), beads.NewWorkItem{Title: "second", Description: "d", Type: "task"}); err != nil {
		t.Fatalf("a write through the server: %v", err)
	}
	if said, err := run(nil, "bd", "list"); err != nil || !strings.Contains(said, "second") {
		t.Fatalf("bd by the local setting: %v: %s", err, said)
	}
	if said, err := run([]string{"BEADS_CREDENTIALS_FILE=/nowhere"}, "bd", "list"); err == nil || !strings.Contains(said, "Access denied") {
		t.Fatalf("without the password bd answered %v: %s", err, said)
	}
	content, _ := os.ReadFile(record.Credentials)
	_, password, _ := strings.Cut(strings.TrimSpace(string(content)), "password=")
	secret := ""
	if info, err := os.ReadFile(filepath.Join(filepath.Dir(record.Credentials), trackerServerInfo)); err == nil {
		secret = string(info[strings.LastIndex(string(info), ":")+1:])
	}
	if password == "" || strings.TrimSpace(secret) == "" {
		t.Fatal("the server's secrets are not in its folder under the services folder")
	}
	err = filepath.WalkDir(store, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		kept, err := os.ReadFile(path)
		if strings.Contains(string(kept), password) || strings.Contains(string(kept), strings.TrimSpace(secret)) {
			t.Errorf("%s holds a secret", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.StopIfRunning(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
	if runs, err := server.Tracker.ServerProcessRuns(t.Context(), record.PID, database); runs || err != nil {
		t.Fatalf("the server still runs: %v, %v", runs, err)
	}
	if said, err := run(nil, "bd", "list"); err != nil || !strings.Contains(said, "second") {
		t.Fatalf("bd opening the store directly: %v: %s", err, said)
	}
}
