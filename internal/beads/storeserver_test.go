package beads

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// fixedServerProbe is the operating system as a measurement's server meets
// it: the addresses the server listens on, and whether the connection attempt
// under the developer sandbox reaches it. It keeps what it was asked.
type fixedServerProbe struct {
	listening []string
	reached   bool

	mu      sync.Mutex
	pids    []int
	reaches []string
}

func (p *fixedServerProbe) Listening(_ context.Context, pid int) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pids = append(p.pids, pid)
	return p.listening, nil
}

func (p *fixedServerProbe) Reach(_ context.Context, address string) SandboxReach {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reaches = append(p.reaches, address)
	return SandboxReach{Address: address, How: "a test's sandbox", Tried: true, Reached: p.reached, ReachedUnsandboxed: true}
}

// A measurement starts server mode on a second copy of its copy, in server
// mode and with every variable that could point bd elsewhere taken out, and
// records the server's process, port, and data directory, where it listens,
// whether the developer sandbox reached it, and which writes grew the store
// over the last week. The server is stopped and the second copy removed, and
// the live store is left exactly as it was.
//
// Not parallel: it sets BEADS_DIR and a variable that would move bd's server
// onto another data directory for the whole process.
func TestAMeasurementStartsServerModeOnASecondCopy(t *testing.T) {
	project, store := liveStore(t)
	t.Setenv("BEADS_DIR", store)
	t.Setenv("BEADS_DOLT_DATA_DIR", store)
	before := snapshot(t, store)
	tracker := &fakeTracker{shrinkTo: defaultShrinks(), serve: true}
	probe := &fixedServerProbe{listening: []string{"127.0.0.1:13307"}, reached: true}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}, Server: probe}
	work, workDir := workDirectory(t)

	measured, err := client.MeasureStore(context.Background(), work)
	if err != nil || !measured.Measured() {
		t.Fatalf("MeasureStore() = %+v, %v, want a measurement", measured, err)
	}
	if after := snapshot(t, store); !reflect.DeepEqual(after, before) {
		t.Errorf("the live store changed:\nbefore %v\nafter  %v", before, after)
	}
	server := measured.Server
	if server == nil || server.Failed != "" || server.StopFailed != "" {
		t.Fatalf("server = %+v, want server mode measured and stopped", server)
	}
	secondCopy := filepath.Join(workDir, serverCopyName)
	if server.PID != 4321 || server.Port != 13307 || server.DataDir != filepath.Join(secondCopy, ".beads", "embeddeddolt") {
		t.Errorf("server = %+v, want process 4321 on port 13307 serving the second copy", server)
	}
	if !reflect.DeepEqual(server.Listening, []string{"127.0.0.1:13307"}) || !reflect.DeepEqual(probe.pids, []int{4321}) {
		t.Errorf("listening = %v asked of %v, want the server's own process asked", server.Listening, probe.pids)
	}
	if !server.Reach.Reached || server.Reach.Address != "127.0.0.1:13307" {
		t.Errorf("reach = %+v, want the listening address tried and reached", server.Reach)
	}
	growth := server.Growth
	want := []WriteGrowth{
		{Kind: "bd: update", Commits: 40, RowsChanged: 120, Tables: []string{"events", "issues"}},
		{Kind: "bd: create", Commits: 3, RowsChanged: 3, Tables: []string{"issues"}},
	}
	if growth.Failed != "" || growth.Commits != 43 || growth.RowsChanged != 123 || !reflect.DeepEqual(growth.Writes, want) || growth.Since.IsZero() {
		t.Errorf("growth = %+v, want the week's writes by kind", growth)
	}
	if tracker.serving {
		t.Errorf("the server is still running after the measurement")
	}
	if _, err := os.Stat(secondCopy); !os.IsNotExist(err) {
		t.Errorf("the second copy is still there: %v", err)
	}
	var started, stopped, queries int
	for _, command := range tracker.ran() {
		switch command.Args[0] {
		case "dolt", "sql":
		default:
			continue
		}
		if command.Dir != secondCopy || environmentValue(command.Env, "BEADS_DIR") != filepath.Join(secondCopy, ".beads") {
			t.Errorf("bd %v ran in %s with BEADS_DIR=%s, want the second copy", command.Args, command.Dir, environmentValue(command.Env, "BEADS_DIR"))
		}
		if environmentValue(command.Env, "BEADS_DOLT_DATA_DIR") != "" || environmentValue(command.Env, "BEADS_DOLT_SHARED_SERVER") != "0" {
			t.Errorf("bd %v ran with the inherited data directory or a shared server: %v", command.Args, command.Env)
		}
		for _, query := range command.Args {
			if strings.Contains(query, "DROP") {
				t.Errorf("bd %v was asked about a table whose name is not a name", command.Args)
			}
		}
		switch {
		case command.Args[0] == "sql":
			queries++
		case command.Args[1] == "start":
			started++
		case command.Args[1] == "stop":
			stopped++
		}
	}
	if started != 1 || stopped != 1 || queries != 4 {
		t.Errorf("started %d, stopped %d, read the history %d times; want one start, one stop, and four reads", started, stopped, queries)
	}
}

// A server bd says serves a data directory outside the second copy is stopped
// at once, and nothing is asked of it.
func TestAServerServingDataOutsideTheCopyIsStoppedAtOnce(t *testing.T) {
	t.Parallel()
	project, store := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks(), serve: true, serveOutside: filepath.Join(store, "embeddeddolt")}
	probe := &fixedServerProbe{reached: true}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}, Server: probe}
	work, _ := workDirectory(t)

	measured, err := client.MeasureStore(context.Background(), work)
	if err != nil || measured.Server == nil {
		t.Fatalf("MeasureStore() = %+v, %v, want a measurement", measured, err)
	}
	if !strings.Contains(measured.Server.Failed, "not inside the second copy") {
		t.Errorf("server = %+v, want it refused for serving data outside the copy", measured.Server)
	}
	if tracker.serving || len(probe.reaches) != 0 {
		t.Errorf("serving = %t, reach attempts = %v; want the server stopped and nothing tried", tracker.serving, probe.reaches)
	}
	for _, command := range tracker.ran() {
		if command.Args[0] == "sql" {
			t.Errorf("bd %v was asked of a server serving data outside the copy", command.Args)
		}
	}
}

// An embedded store's refusal to start a server is said on the measurement,
// and every stage is still measured.
func TestAServerThatDoesNotStartLeavesTheStagesMeasured(t *testing.T) {
	t.Parallel()
	project, _ := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks()}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}, Server: &fixedServerProbe{}}
	work, _ := workDirectory(t)

	measured, err := client.MeasureStore(context.Background(), work)
	if err != nil || len(measured.Stages) != 4 {
		t.Fatalf("MeasureStore() = %+v, %v, want every stage measured", measured, err)
	}
	if measured.Server == nil || !strings.Contains(measured.Server.Failed, "not supported in embedded mode") || measured.Server.StopFailed != "" {
		t.Errorf("server = %+v, want bd's refusal said and nothing left to stop", measured.Server)
	}
}

// A server an earlier measurement left running, when its process was killed,
// is stopped before its copy is removed.
func TestAServerLeftRunningIsStoppedFirst(t *testing.T) {
	t.Parallel()
	project, _ := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks(), serve: true, serving: true}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}, Server: &fixedServerProbe{reached: true}}
	work, workDir := workDirectory(t)
	if err := os.MkdirAll(filepath.Join(workDir, serverCopyName, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(workDir, serverCopyName, ".beads", "dolt-server.pid"), "4000")

	if _, err := client.MeasureStore(context.Background(), work); err != nil {
		t.Fatalf("MeasureStore() error = %v", err)
	}
	first := tracker.ran()[0]
	if !reflect.DeepEqual(first.Args, []string{"dolt", "stop", "--force"}) || first.Dir != filepath.Join(workDir, serverCopyName) {
		t.Errorf("first command = %v in %s, want the left server stopped in its copy", first.Args, first.Dir)
	}
}

func TestParseServerStartReadsProcessPortAndData(t *testing.T) {
	t.Parallel()
	pid, port, data := parseServerStart("Dolt server started (PID 812, port 49152)\n  Data: /tmp/x/.beads/dolt\n  Logs: /tmp/x/.beads/dolt-server.log\n")
	if pid != 812 || port != 49152 || data != "/tmp/x/.beads/dolt" {
		t.Errorf("parseServerStart() = %d, %d, %q", pid, port, data)
	}
	if pid, port, data := parseServerStart("Error: something else"); pid != 0 || port != 0 || data != "" {
		t.Errorf("parseServerStart() of a refusal = %d, %d, %q, want nothing", pid, port, data)
	}
}

func TestReachAddressReadsAWildcardAsThisMachine(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		listening []string
		want      string
	}{
		{[]string{"127.0.0.1:3307"}, "127.0.0.1:3307"},
		{[]string{"*:3307"}, "127.0.0.1:3307"},
		{[]string{"[::1]:3307"}, "[::1]:3307"},
		{nil, "127.0.0.1:9000"},
	} {
		if got := reachAddress(test.listening, 9000); got != test.want {
			t.Errorf("reachAddress(%v) = %q, want %q", test.listening, got, test.want)
		}
	}
}

// recordingRunner answers every command with one result and keeps them.
type recordingRunner struct {
	mu       sync.Mutex
	commands []execution.Command
	answer   func(execution.Command) execution.ProcessResult
}

func (r *recordingRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, command)
	return r.answer(command), nil
}

func TestTheOperatingSystemProbeReadsListeningAddresses(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{answer: func(execution.Command) execution.ProcessResult {
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "p4321\nf12\nn127.0.0.1:13307\nf13\nn[::1]:13307\n"}
	}}
	listening, err := OSServerProbe{Runner: runner}.Listening(context.Background(), 4321)
	if err != nil || !reflect.DeepEqual(listening, []string{"127.0.0.1:13307", "[::1]:13307"}) {
		t.Errorf("Listening() = %v, %v", listening, err)
	}
	if args := runner.commands[0].Args; runner.commands[0].Name != "lsof" || !strings.Contains(strings.Join(args, " "), "-a -p 4321 -iTCP -sTCP:LISTEN") {
		t.Errorf("lsof was run as %s %v", runner.commands[0].Name, args)
	}
}

// The reach attempt is made under sandbox-exec with the developer sandbox's
// network rules, and once without it, so a refusal is the sandbox's only where
// the server answered the attempt made outside it.
func TestTheOperatingSystemProbeTriesTheConnectionUnderTheDeveloperSandbox(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		reach := OSServerProbe{Runner: &recordingRunner{}}.Reach(context.Background(), "127.0.0.1:1")
		if reach.Failed == "" || reach.Tried {
			t.Errorf("Reach() off macOS = %+v, want it said that the sandbox is not reproduced here", reach)
		}
		return
	}
	runner := &recordingRunner{answer: func(command execution.Command) execution.ProcessResult {
		if command.Name == sandboxExecPath {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "nc: connectx to 127.0.0.1 port 13307 (tcp) failed: Operation not permitted"}
		}
		return execution.ProcessResult{Status: execution.ProcessSucceeded}
	}}
	reach := OSServerProbe{Runner: runner}.Reach(context.Background(), "127.0.0.1:13307")
	if !reach.Tried || reach.Reached || !reach.ReachedUnsandboxed || !strings.Contains(reach.Said, "Operation not permitted") || reach.How != DeveloperSandboxReproduced {
		t.Errorf("Reach() = %+v, want the sandbox's refusal said beside an unsandboxed connection", reach)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("ran %d commands, want the attempt without and with the sandbox", len(runner.commands))
	}
	sandboxed := runner.commands[1]
	if sandboxed.Args[0] != "-p" || sandboxed.Args[1] != developerNetworkProfile || sandboxed.Args[2] != netcatPath {
		t.Errorf("sandbox-exec was run as %v, want nc under the developer network profile", sandboxed.Args)
	}
	if !strings.Contains(developerNetworkProfile, `(allow network-outbound (remote ip "localhost:*"))`) || !strings.Contains(developerNetworkProfile, "(deny network*)") {
		t.Errorf("the profile does not reproduce the developer sandbox's network rules:\n%s", developerNetworkProfile)
	}
}

func TestMoreKindsOfWriteThanAreNamedAreCountedTogether(t *testing.T) {
	t.Parallel()
	writes := map[string]*WriteGrowth{}
	for index := 0; index < maxGrowthKinds+3; index++ {
		kind := strings.Repeat("k", index+1)
		writes[kind] = &WriteGrowth{Kind: kind, Commits: 1, RowsChanged: index + 1}
	}
	ranked := rankedWrites(writes)
	if len(ranked) != maxGrowthKinds+1 {
		t.Fatalf("ranked = %d kinds, want %d named and the rest together", len(ranked), maxGrowthKinds)
	}
	if rest := ranked[maxGrowthKinds]; rest.Kind != otherWritesKind || rest.Commits != 3 || rest.RowsChanged != 6 {
		t.Errorf("the rest = %+v, want the three smallest counted together", rest)
	}
	if ranked[0].RowsChanged != maxGrowthKinds+3 {
		t.Errorf("first = %+v, want the most rows changed first", ranked[0])
	}
}
