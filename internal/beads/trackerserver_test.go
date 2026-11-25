package beads

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// servedStore is a store with a database directory, and a client whose bd
// answers an empty listing and whose commands are kept.
func servedStore(t *testing.T) (Client, string, *fakeTracker) {
	t.Helper()
	project := t.TempDir()
	store := filepath.Join(project, ".beads")
	if err := os.MkdirAll(filepath.Join(store, EmbeddedDatabase, "calc"), 0o700); err != nil {
		t.Fatal(err)
	}
	bd := &fakeTracker{}
	return Client{Runner: bd, Dir: project, Timeout: 700 * time.Millisecond, Holders: fixedHolders{}}, store, bd
}

func recordServer(t *testing.T, store string, record ServerRecord) {
	t.Helper()
	if err := WriteServerRecord(store, record); err != nil {
		t.Fatal(err)
	}
}

func listenLocal(t *testing.T) (net.Listener, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener, listener.Addr().(*net.TCPAddr).Port
}

func TestACallGoesThroughTheServerTheRecordSaysIsRunning(t *testing.T) {
	client, store, bd := servedStore(t)
	t.Setenv("BEADS_DOLT_PASSWORD", "from-outside")
	_, port := listenLocal(t)
	recordServer(t, store, ServerRecord{State: ServerRunning, PID: os.Getpid(), Port: port, Credentials: "/services/credentials", StartedAt: time.Now()})
	if _, err := client.run(t.Context(), "list", "--json"); err != nil {
		t.Fatal(err)
	}
	environment := bd.commands[0].Env
	for name, want := range map[string]string{"BEADS_DOLT_SERVER_MODE": "1", "BEADS_DOLT_AUTO_START": "0", "BEADS_DOLT_SERVER_HOST": "127.0.0.1", "BEADS_CREDENTIALS_FILE": "/services/credentials", "BEADS_DOLT_PASSWORD": ""} {
		if got := environmentValue(environment, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	reach, found := client.Reach(t.Context())
	if !found || !reach.ThroughServer || !strings.Contains(reach.Says(), "through its server") {
		t.Errorf("Reach() = %#v, %v", reach, found)
	}
}

func TestTheStoreIsOpenedDirectlyOnlyWhileTheServerIsKnownToBeStopped(t *testing.T) {
	listener, port := listenLocal(t)
	listener.Close()
	self := fixedHolders{holders: []StoreHolder{{PID: os.Getpid()}}}
	for name, test := range map[string]struct {
		record  string
		holders HolderFinder
		direct  bool
	}{
		"stopped":                          {`{"state":"stopped","reason":"the product was stopped"}`, fixedHolders{}, true},
		"stopped, its process still holds": {`{"state":"stopped","pid":` + strconv.Itoa(os.Getpid()) + `}`, self, false},
		"stopped, the number taken since":  {`{"state":"stopped","pid":` + strconv.Itoa(os.Getpid()) + `}`, fixedHolders{}, true},
		"running, connection refused":      {`{"state":"running","pid":1,"port":` + strconv.Itoa(port) + `,"credentials":"/c"}`, fixedHolders{}, false},
		"being started":                    {`{"state":"starting"}`, fixedHolders{}, false},
		"unreadable":                       {`{`, fixedHolders{}, false},
		"a state this build does not know": {`{"state":"resting"}`, fixedHolders{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			client, store, bd := servedStore(t)
			client.Holders = test.holders
			recordServer(t, store, ServerRecord{State: ServerStopped})
			if err := os.WriteFile(filepath.Join(store, serverRecordName), []byte(test.record), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := client.run(t.Context(), "list", "--json")
			if test.direct {
				if err != nil || environmentValue(bd.commands[0].Env, "BEADS_DOLT_SERVER_MODE") != "0" {
					t.Fatalf("run() = %v, commands %v, want bd opening the store directly", err, bd.commands)
				}
				return
			}
			if !errors.Is(err, ErrUnanswered) || len(bd.commands) != 0 {
				t.Fatalf("run() = %v with %d commands, want unanswered with bd never run", err, len(bd.commands))
			}
			if reach, _ := client.Reach(t.Context()); !reach.Waiting {
				t.Errorf("Reach() = %#v, want it to say calls wait", reach)
			}
		})
	}
}

// A waiting call lets the store lock go, so the supervisor can take it to start
// the server, and the call goes on once it has.
func TestAWaitingCallLetsTheServerStartAndThenGoesThroughIt(t *testing.T) {
	client, store, bd := servedStore(t)
	client.Timeout = 10 * time.Second
	recordServer(t, store, ServerRecord{State: ServerStarting})
	done := make(chan error, 1)
	go func() { _, err := client.run(context.Background(), "list", "--json"); done <- err }()
	unlock, err := client.HoldStore(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, port := listenLocal(t)
	recordServer(t, store, ServerRecord{State: ServerRunning, PID: os.Getpid(), Port: port, Credentials: "/c"})
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := environmentValue(bd.commands[0].Env, "BEADS_DOLT_SERVER_MODE"); got != "1" {
		t.Fatalf("server mode = %q, want through the server", got)
	}
}

func TestAStoreNoServerWasEverRunForIsReachedAsItAlwaysWas(t *testing.T) {
	client, _, bd := servedStore(t)
	t.Setenv("BEADS_DOLT_SERVER_PORT", "3307")
	if _, err := client.run(t.Context(), "list", "--json"); err != nil {
		t.Fatal(err)
	}
	if got := environmentValue(bd.commands[0].Env, "BEADS_DOLT_SERVER_PORT"); got != "3307" {
		t.Fatalf("the environment was changed: port = %q", got)
	}
	if reach, _ := client.Reach(t.Context()); !strings.Contains(reach.Says(), "opened directly: its server is not switched on") {
		t.Errorf("Reach() says %q", reach.Says())
	}
}

// A client pinned to a copy never reaches the live store's server, and the copy
// carries neither the local setting nor the link to the server's folder.
func TestACopyNeverReachesTheLiveServer(t *testing.T) {
	client, store, _ := servedStore(t)
	t.Setenv("BEADS_DOLT_SERVER_MODE", "1")
	t.Setenv("BEADS_CREDENTIALS_FILE", "/c")
	pinned := client.pinnedTo(t.TempDir(), "/copy/.beads").environment()
	if environmentValue(pinned, "BEADS_DOLT_SERVER_MODE") != "0" || environmentValue(pinned, "BEADS_CREDENTIALS_FILE") != "" {
		t.Errorf("a pinned client's environment can reach a server")
	}
	if err := WriteLocalSetting(store, ServerRecord{Port: 4, Credentials: "/c"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/elsewhere", filepath.Join(store, serverLinkPath)); err != nil {
		t.Fatal(err)
	}
	work := pinnedDirectory(t)
	if err := copyStore(store, work, "copy"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{localSettingName, serverLinkPath} {
		if _, err := os.Lstat(filepath.Join(work.Path(), "copy", ".beads", name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the copy carries %s: %v", name, err)
		}
	}
}

func TestTheLocalSettingKeepsAPersonsOwnLines(t *testing.T) {
	_, store, _ := servedStore(t)
	path := LocalSettingPath(store)
	if err := os.WriteFile(path, []byte("MINE=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := ServerRecord{Port: 41877, Credentials: "/state dir/services/credentials"}
	for range 2 {
		if err := WriteLocalSetting(store, record); err != nil {
			t.Fatal(err)
		}
	}
	content, _ := os.ReadFile(path)
	if said := string(content); !strings.HasPrefix(said, "MINE=1\n") || strings.Count(said, "BEADS_DOLT_SERVER_PORT=41877\n") != 1 || strings.Contains(said, "PASSWORD") {
		t.Fatalf("the local setting is %q", said)
	}
	if err := RemoveLocalSetting(store); err != nil {
		t.Fatal(err)
	}
	if content, _ = os.ReadFile(path); string(content) != "MINE=1\n" {
		t.Fatalf("after removal it is %q", content)
	}
	if err := os.WriteFile(path, []byte("BEADS_DOLT_SERVER_PORT=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteLocalSetting(store, record); err == nil {
		t.Fatal("a person's own server setting was not refused")
	}
}

type recordingSwitch struct {
	client   Client
	stops    int
	starts   int
	lockHeld []bool
	stopErr  error
	stillUp  bool
}

func (s *recordingSwitch) held() bool {
	held := s.client
	held.Timeout = 50 * time.Millisecond
	unlock, err := held.lockStore(context.Background(), true)
	if err == nil {
		unlock()
	}
	return err != nil
}

func (s *recordingSwitch) StopHeld(context.Context, string) (bool, error) {
	s.stops++
	s.lockHeld = append(s.lockHeld, s.held())
	return !s.stillUp, s.stopErr
}

func (s *recordingSwitch) StartHeld(context.Context) error {
	s.starts++
	s.lockHeld = append(s.lockHeld, s.held())
	return nil
}

func TestTheLiveRebuildStopsTheServerAfterItsCheckAndStartsItBeforeTheLockGoes(t *testing.T) {
	for name, test := range map[string]struct {
		holders fixedHolders
		stopErr error
		stillUp bool
		stops   int
		starts  int
	}{
		"a rebuild that fails":       {fixedHolders{}, nil, false, 1, 1},
		"a server that did not stop": {fixedHolders{}, errors.New("it did not stop"), true, 1, 0},
		"stopped, and not cleared":   {fixedHolders{}, errors.New("the local setting stayed"), false, 1, 1},
		"an outside process":         {fixedHolders{holders: []StoreHolder{{PID: 777}}}, nil, false, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			client, _, bd := servedStore(t)
			bd.fail = "export"
			client.Holders = test.holders
			server := &recordingSwitch{client: client, stopErr: test.stopErr, stillUp: test.stillUp}
			client.ServerSwitch = server
			work, kept := pinnedDirectory(t), pinnedDirectory(t)
			rebuild, _ := client.RebuildStore(t.Context(), work, kept, "old", func(StoreRebuild) error { return nil })
			if server.stops != test.stops || server.starts != test.starts {
				t.Fatalf("stopped %d times and started %d, want %d and %d", server.stops, server.starts, test.stops, test.starts)
			}
			for _, held := range server.lockHeld {
				if !held {
					t.Error("the server was stopped or started without the store lock")
				}
			}
			if test.stopErr != nil && (rebuild.ServerKept == "" || len(bd.commands) != 0) {
				t.Errorf("rebuild = %#v, want it not to go ahead", rebuild)
			}
		})
	}
}

func TestARehearsalPassesOnAStoreHoldingTheServersLinkAndNoOther(t *testing.T) {
	t.Parallel()
	client, project := realStore(t)
	store := filepath.Join(project, ".beads")
	if err := os.Symlink(t.TempDir(), filepath.Join(store, serverLinkPath)); err != nil {
		t.Fatal(err)
	}
	rebuild, err := client.RehearseRebuild(t.Context(), pinnedDirectory(t))
	if err != nil || !rebuild.Passed() {
		t.Fatalf("RehearseRebuild() = %+v, %v, want it passed", rebuild, err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(store, EmbeddedDatabase, "other")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RehearseRebuild(t.Context(), pinnedDirectory(t)); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("RehearseRebuild() error = %v, want another link refused", err)
	}
}

func TestAMeasurementIsNotHeldBackByTheServer(t *testing.T) {
	t.Parallel()
	project, store := liveStore(t)
	bd := &fakeTracker{shrinkTo: map[string]int{"gc": 2048, "compact": 1024, "flatten": 512}}
	server, person := StoreHolder{PID: 4242, Command: "dolt sql-server"}, StoreHolder{PID: 777, Command: "bd list"}
	recordServer(t, store, ServerRecord{State: ServerRunning, PID: server.PID, Port: 1, Credentials: "/c"})
	if err := os.Symlink(t.TempDir(), filepath.Join(store, serverLinkPath)); err != nil {
		t.Fatal(err)
	}
	client := Client{Runner: bd, Dir: project, Holders: fixedHolders{holders: []StoreHolder{server}}}
	measured, err := client.MeasureStore(t.Context(), pinnedDirectory(t))
	if err != nil || len(measured.Busy) != 0 || len(measured.Stages) == 0 {
		t.Fatalf("MeasureStore() = %+v, %v, want the store measured", measured, err)
	}
	client.Holders = fixedHolders{holders: []StoreHolder{server, person}}
	measured, err = client.MeasureStore(t.Context(), pinnedDirectory(t))
	if err != nil || len(measured.Busy) != 1 || measured.Busy[0].PID != person.PID {
		t.Fatalf("MeasureStore() = %+v, %v, want only the person's bd named", measured, err)
	}
}
