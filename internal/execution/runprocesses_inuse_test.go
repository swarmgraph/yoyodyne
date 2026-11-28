package execution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func running(fake *fakeProcesses, pid int) bool {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	_, ok := fake.entries[pid]
	return ok
}

// A process of the run that a person's process is connected to is left running
// and named with that process, not counted as one the harness could not stop;
// once the connection closes, the next stop stops it.
func TestRunProcessesLeavesRunningOneAPersonsProcessIsConnectedTo(t *testing.T) {
	fake := newFakeProcesses(marked(4242, "run-a"), processEntry{PID: 5150, Parent: 1, Start: "1", Command: "zsh", Environment: []string{"PATH=/bin"}})
	fake.connect(4242, 5150, "TCP between 127.0.0.1:8080 and 127.0.0.1:61000")
	left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"})
	if len(left) != 1 || left[0].PID != 4242 || !left[0].InUse || len(fake.signalled) != 0 {
		t.Fatalf("Stop() left %+v and sent %v, want the process left running unsignalled", left, fake.signalled)
	}
	want := "a process this run started is still running: process 4242, java, left running because process 5150 (zsh, which carries no run's identifier) is connected to it over TCP between 127.0.0.1:8080 and 127.0.0.1:61000"
	if got := left[0].Describe(); got != want {
		t.Fatalf("Describe() = %q\nwant %q", got, want)
	}
	fake.disconnect()
	if left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"}); len(left) != 0 || running(fake, 4242) {
		t.Fatalf("Stop() after the connection closed left %+v", left)
	}
}

// A port listened on with nothing connected, and a connection only to another
// process the same stop is stopping, are not use.
func TestRunProcessesStopsOneThatOnlyListensOrIsConnectedToItsOwnRun(t *testing.T) {
	fake := newFakeProcesses(marked(4001, "run-a"), marked(4002, "run-a"), marked(4003, "run-a"))
	fake.ports[4001] = []string{"127.0.0.1:25565"}
	fake.connect(4002, 4003, "the socket file /tmp/build.sock")
	if left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"}); len(left) != 0 {
		t.Fatalf("Stop() left %+v, want all three stopped", left)
	}
	for _, pid := range []int{4001, 4002, 4003} {
		if running(fake, pid) {
			t.Errorf("process %d is still running", pid)
		}
	}
}

// Another run's process keeps a connected process running while that run has
// something going, which a stop that is not told otherwise assumes; one whose
// run has nothing going does not.
func TestRunProcessesLeavesRunningOneAnotherBusyRunIsConnectedTo(t *testing.T) {
	fake := newFakeProcesses(marked(4001, "run-a"), marked(4100, "run-b"))
	fake.connect(4001, 4100, "a local socket")
	left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"})
	if len(left) != 1 || !left[0].InUse || !strings.Contains(left[0].Why, "process 4100 (java, run run-b) is connected to it") {
		t.Fatalf("Stop() left %+v, want it left running for run-b's process", left)
	}
	busy := func(string) bool { return false }
	if left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a", Idle: busy}); len(left) != 1 {
		t.Fatalf("Stop() with run-b busy left %+v, want it left running", left)
	}
	idle := func(id string) bool { return id == "run-b" }
	if left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a", Idle: idle}); len(left) != 0 || running(fake, 4001) {
		t.Fatalf("Stop() with run-b idle left %+v, want it stopped", left)
	}
	if !running(fake, 4100) {
		t.Fatal("run-b's process was stopped by run-a's stop")
	}
}

// An end the harness cannot find or read, the harness's own process, and a
// listing of connections that cannot be read at all each leave the process
// running.
func TestRunProcessesLeavesRunningOneWhoseOtherEndItCannotRead(t *testing.T) {
	fake := newFakeProcesses(marked(4001, "run-a"), marked(4002, "run-a"), marked(4003, "run-a"))
	fake.connected[4001] = []localConnection{{Over: "the socket file /var/run/daemon.sock"}}
	fake.connected[4002] = []localConnection{{Over: "TCP between 127.0.0.1:1 and 127.0.0.1:2", Peers: []int{9999}}}
	fake.connected[4003] = []localConnection{{Over: "a local socket", Peers: []int{os.Getpid()}}}
	left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"})
	if len(left) != 3 || len(fake.signalled) != 0 {
		t.Fatalf("Stop() left %+v and sent %v, want all three left running", left, fake.signalled)
	}
	for index, want := range []string{
		"the other end of its connection over the socket file /var/run/daemon.sock could not be read",
		"process 9999, which the harness cannot look at, is connected to it",
		"the harness itself) is connected to it",
	} {
		if !left[index].InUse || !strings.Contains(left[index].Why, want) {
			t.Errorf("left[%d] = %+v, want it to say %q", index, left[index], want)
		}
	}

	unreadable := newFakeProcesses(marked(4001, "run-a"))
	unreadable.connectionErr = errors.New("lsof could not be run")
	left = quickStop(unreadable).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"})
	if len(left) != 1 || !left[0].InUse || !strings.Contains(left[0].Why, "could not read its local connections: lsof could not be run") || len(unreadable.signalled) != 0 {
		t.Fatalf("Stop() with no connections readable left %+v and sent %v", left, unreadable.signalled)
	}
}

// Connections are read again before every signal: one made after the stop
// signal, before the kill signal, keeps the process running. A process of the
// run connected only to one left running is left running too.
func TestRunProcessesReadsConnectionsAgainBeforeEverySignal(t *testing.T) {
	fake := newFakeProcesses(marked(4001, "run-a"), marked(4002, "run-a"), processEntry{PID: 5150, Parent: 1, Start: "1", Command: "gradle", Environment: []string{"PATH=/bin"}})
	fake.ignore[4001] = syscall.SIGTERM
	fake.ignore[4002] = syscall.SIGTERM
	fake.connect(4001, 4002, "a local socket")
	fake.onConnections = func(reading int) {
		if reading == 2 {
			fake.connect(4001, 5150, "TCP between 127.0.0.1:9000 and 127.0.0.1:61001")
		}
	}
	left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"})
	if signalled := slices.Sorted(slices.Values(fake.signalled)); !slices.Equal(signalled, []string{"terminated 4001", "terminated 4002"}) {
		t.Fatalf("signals = %v, want only the stop signals sent before the connection was made", fake.signalled)
	}
	if len(left) != 2 || !left[0].InUse || !left[1].InUse ||
		!strings.Contains(left[0].Why, "process 5150 (gradle") || !strings.Contains(left[1].Why, "process 4001 (java, run run-a) is connected to it") {
		t.Fatalf("Stop() left %+v", left)
	}
}

// What lsof says of established TCP sockets and of socket files is read into
// connections with the process at each other end.
func TestLsofConnectionsFindsEachOtherEnd(t *testing.T) {
	tcp := []byte("p100\nf5\nd0xa\nn127.0.0.1:8080->127.0.0.1:61000\np200\nf7\nd0xb\nn127.0.0.1:61000->127.0.0.1:8080\np300\nf3\nd0xc\nn10.0.0.5:51000->140.82.112.3:443\np400\nf4\nd0xd\nn127.0.0.1:7000->127.0.0.1:62000\n")
	sockets := []byte("p100\nf9\nd0x1\n" + "n/tmp/build.sock\n" +
		"p200\nf8\nd0x2\nn->0x1\n" +
		"p100\nf10\nd0x3\nn/tmp/listen.sock\n" +
		"p100\nf11\nd0x4\nn->(none)\n" +
		"p100\nf12\nd0x5\nn->0x99\n")
	got := lsofConnections(tcp, sockets)
	want100 := []localConnection{
		{Over: "TCP between 127.0.0.1:8080 and 127.0.0.1:61000", Peers: []int{200}},
		{Over: "the socket file /tmp/build.sock", Peers: []int{200}},
		{Over: "a local socket"},
	}
	if !connectionsEqual(got[100], want100) {
		t.Errorf("process 100: %+v\nwant %+v", got[100], want100)
	}
	want200 := []localConnection{
		{Over: "TCP between 127.0.0.1:61000 and 127.0.0.1:8080", Peers: []int{100}},
		{Over: "a local socket", Peers: []int{100}},
	}
	if !connectionsEqual(got[200], want200) {
		t.Errorf("process 200: %+v\nwant %+v", got[200], want200)
	}
	if len(got[300]) != 0 {
		t.Errorf("process 300's connection off the machine was read as local: %+v", got[300])
	}
	if want := []localConnection{{Over: "TCP between 127.0.0.1:7000 and 127.0.0.1:62000"}}; !connectionsEqual(got[400], want) {
		t.Errorf("process 400, whose other end lsof does not show: %+v", got[400])
	}
}

// Linux's /proc/net/tcp addresses are read in the kernel's byte order, and the
// processes at each end found by socket inode, for TCP and socket files alike.
func TestProcConnectionsFindsEachOtherEnd(t *testing.T) {
	content := []byte("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:1F90 0100007F:EE48 01 00000000:00000000 00:00000000 00000000  1000        0 111 1 0 20 4 30 10 -1\n" +
		"   1: 0100007F:EE48 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 222 1 0 20 4 30 10 -1\n" +
		"   2: 0100007F:1F91 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 333 1 0 20 4 30 10 -1\n")
	tcp := parseProcNetTCP(content)
	if len(tcp) != 3 || tcp[0].Local != "127.0.0.1:8080" || tcp[0].Remote != "127.0.0.1:61000" || !tcp[0].Established || tcp[2].Established {
		t.Fatalf("parseProcNetTCP() = %+v", tcp)
	}
	six := parseProcNetTCP([]byte("header\n   0: 00000000000000000000000001000000:1F90 00000000000000000000000001000000:EE48 01 0:0 0:0 0 1000 0 444\n"))
	if len(six) != 1 || six[0].Local != "[::1]:8080" {
		t.Fatalf("parseProcNetTCP(tcp6) = %+v", six)
	}
	owners := map[uint64][]int{111: {100}, 222: {200}, 333: {100}, 555: {100}, 556: {300}, 557: {100}}
	got := procConnections(tcp, map[uint64]uint64{555: 556, 556: 555, 557: 9999}, owners)
	want100 := []localConnection{
		{Over: "TCP between 127.0.0.1:8080 and 127.0.0.1:61000", Peers: []int{200}},
		{Over: "a local socket", Peers: []int{300}},
		{Over: "a local socket"},
	}
	if !connectionsEqual(got[100], want100) {
		t.Errorf("process 100: %+v\nwant %+v", got[100], want100)
	}
}

// connectionsEqual compares two processes' connections in any order.
func connectionsEqual(got, want []localConnection) bool {
	if len(got) != len(want) {
		return false
	}
	key := func(c localConnection) string {
		peers := slices.Clone(c.Peers)
		slices.Sort(peers)
		return c.Over + "|" + fmt.Sprint(peers)
	}
	a, b := make([]string, 0, len(got)), make([]string, 0, len(want))
	for _, c := range got {
		a = append(a, key(c))
	}
	for _, c := range want {
		b = append(b, key(c))
	}
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
