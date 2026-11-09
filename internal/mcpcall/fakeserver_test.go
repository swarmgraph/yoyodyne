//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package mcpcall

// The fake MCP server the client is tested against. It is the test binary
// itself: TestMain runs it in place of the tests when the binary's first
// argument is fakeServerArgument, which is how a command server can be started
// with an environment built from a list and still be found, since nothing the
// tests set in their own environment reaches it. The same answers are served
// over https by fakeHTTP.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	fakeServerArgument  = "mcpcall-fake-server"
	fakeSleeperArgument = "mcpcall-fake-sleeper"
	fakeCredentialEnv   = "FAKE_MCP_TOKEN"
	// fakeVersionMode makes the fake answer the opening exchange with a
	// protocol version off the list.
	fakeVersionMode = "unknown-version"
	// sleeperDeadline is how long the descendant a fake server starts lives if
	// nothing kills it, so it ends on its own however the test ends.
	sleeperDeadline = 60 * time.Second
	sleeperMarker   = "sleeper-reached-its-deadline"
	sleeperLock     = "sleeper.lock"
	// fakeHeldSecret is a value the call path's tests hold as secret, which
	// the leak tools put in what they say.
	fakeHeldSecret = "held-secret-value-0077"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case fakeServerArgument:
			os.Exit(runFakeServer(os.Args[2:]))
		case fakeSleeperArgument:
			os.Exit(runSleeper())
		}
	}
	os.Exit(m.Run())
}

// fakeCommand is the command that starts the fake server.
func fakeCommand(mode ...string) []string {
	return append([]string{os.Args[0], fakeServerArgument}, mode...)
}

// fakeState is what the fake learned during one session, answered by its
// tools so a test can see what the client sent.
type fakeState struct {
	mode       string
	initParams json.RawMessage
	credential string
}

var fakeTools = []map[string]any{
	{"name": "echo", "description": "says back its arguments", "inputSchema": map[string]any{"type": "object"}},
	{"name": "environment", "inputSchema": map[string]any{"type": "object"}},
	{"name": "opening", "inputSchema": map[string]any{"type": "object"}},
	{"name": "ask-back", "inputSchema": map[string]any{"type": "object"}},
	{"name": "spawn", "inputSchema": map[string]any{"type": "object"}},
	{"name": "fail-with-credential", "inputSchema": map[string]any{"type": "object"}},
	// The tools below are the call path's (path_test.go): send leaves a line in
	// the server's folder for each call it is given; hang and hang-act start a
	// descendant and never answer; flaky and flaky-act stop without answering
	// the first time each is called in a folder; mixed answers with binary
	// content beside text; big answers with as many bytes as asked; leak
	// answers with the credential and fakeHeldSecret, and leak-error says the
	// same in an error.
	{"name": "send", "inputSchema": map[string]any{"type": "object"}},
	{"name": "hang", "inputSchema": map[string]any{"type": "object"}},
	{"name": "hang-act", "inputSchema": map[string]any{"type": "object"}},
	{"name": "flaky", "inputSchema": map[string]any{"type": "object"}},
	{"name": "flaky-act", "inputSchema": map[string]any{"type": "object"}},
	{"name": "mixed", "inputSchema": map[string]any{"type": "object"}},
	{"name": "big", "inputSchema": map[string]any{"type": "object"}},
	{"name": "leak", "inputSchema": map[string]any{"type": "object"}},
	{"name": "leak-error", "inputSchema": map[string]any{"type": "object"}},
}

func text(value string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": value}}}
}

// answer is the fake's answer to one request, or nil where the request needs
// more than one message (ask-back), which each transport handles itself.
func (s *fakeState) answer(method string, params json.RawMessage) (result any, rpcError map[string]any) {
	switch method {
	case "initialize":
		s.initParams = params
		var asked struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &asked)
		version := asked.ProtocolVersion
		if s.mode == fakeVersionMode {
			version = "1999-01-01"
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fake", "version": "0"},
		}, nil
	case "tools/list":
		var asked struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(params, &asked)
		if asked.Cursor == "" {
			return map[string]any{"tools": fakeTools[:3], "nextCursor": "page-2"}, nil
		}
		return map[string]any{"tools": fakeTools[3:]}, nil
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(params, &call)
		switch call.Name {
		case "echo":
			return text(string(call.Arguments)), nil
		case "opening":
			return text(string(s.initParams)), nil
		case "environment":
			cwd, _ := os.Getwd()
			encoded, _ := json.Marshal(map[string]any{"env": os.Environ(), "cwd": cwd, "args": os.Args})
			return text(string(encoded)), nil
		case "fail-with-credential":
			return nil, map[string]any{"code": -32000, "message": "the token " + s.credential + " was not accepted"}
		case "mixed":
			return map[string]any{"content": []any{
				map[string]any{"type": "text", "text": "a caption"},
				map[string]any{"type": "image", "mimeType": "image/png", "data": strings.Repeat("A", 1000)},
				map[string]any{"type": "resource", "resource": map[string]any{"uri": "file:///a.txt", "text": "resource text"}},
				map[string]any{"type": "resource", "resource": map[string]any{"uri": "file:///a.bin", "blob": strings.Repeat("B", 500)}},
			}}, nil
		case "big":
			var asked struct {
				Bytes int `json:"bytes"`
			}
			_ = json.Unmarshal(call.Arguments, &asked)
			return text(strings.Repeat("x", asked.Bytes)), nil
		case "leak", "leak-error":
			said := "credential " + s.credential + " and secret " + fakeHeldSecret
			if call.Name == "leak-error" {
				return nil, map[string]any{"code": -32000, "message": said}
			}
			return text(said), nil
		}
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": "no such tool"}}, "isError": true}, nil
	}
	return nil, map[string]any{"code": -32601, "message": "no such method"}
}

func rpcAnswer(id json.RawMessage, result any, rpcError map[string]any) map[string]any {
	answer := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcError != nil {
		answer["error"] = rpcError
	} else {
		answer["result"] = result
	}
	return answer
}

type fakeMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error"`
}

// runFakeServer serves MCP over standard input and output until its input
// closes.
func runFakeServer(mode []string) int {
	state := &fakeState{credential: os.Getenv(fakeCredentialEnv)}
	if len(mode) > 0 {
		state.mode = mode[0]
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64<<10), 8<<20)
	out := json.NewEncoder(os.Stdout)
	fmt.Fprintln(os.Stdout, "a line that is not a message, which the client passes over")
	for in.Scan() {
		var m fakeMessage
		if err := json.Unmarshal(in.Bytes(), &m); err != nil || len(m.ID) == 0 || m.Method == "" {
			continue
		}
		var call struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(m.Params, &call)
		switch {
		case m.Method == "tools/call" && call.Name == "ask-back":
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{"level": "info"}})
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": "server-1", "method": "sampling/createMessage", "params": map[string]any{}})
			reply := "no reply"
			if in.Scan() {
				var back fakeMessage
				_ = json.Unmarshal(in.Bytes(), &back)
				if back.Error != nil {
					reply = "refused with " + strconv.Itoa(back.Error.Code) + " for " + string(back.ID)
				} else {
					reply = "answered: " + in.Text()
				}
			}
			_ = out.Encode(rpcAnswer(m.ID, text(reply), nil))
		case m.Method == "tools/call" && call.Name == "send":
			appendLine("sent.log", string(m.Params))
			_ = out.Encode(rpcAnswer(m.ID, text("sent"), nil))
		case m.Method == "tools/call" && strings.HasPrefix(call.Name, "hang"):
			// Never answers: it starts a descendant in its group, then waits out
			// the sleeper's deadline whatever happens to its input, so only the
			// group being killed ends either of them early.
			_, _ = spawnSleeper()
			end := time.Now().Add(sleeperDeadline)
			for time.Now().Before(end) {
				time.Sleep(100 * time.Millisecond)
			}
			return 0
		case m.Method == "tools/call" && strings.HasPrefix(call.Name, "flaky"):
			appendLine(call.Name+".log", "called")
			if _, err := os.Stat(call.Name + ".seen"); err != nil {
				_ = os.WriteFile(call.Name+".seen", []byte("seen"), 0o600)
				return 1
			}
			_ = out.Encode(rpcAnswer(m.ID, text("answered the second time"), nil))
		case m.Method == "tools/call" && call.Name == "spawn":
			pid, err := spawnSleeper()
			if err != nil {
				_ = out.Encode(rpcAnswer(m.ID, nil, map[string]any{"code": -32000, "message": err.Error()}))
				continue
			}
			_ = out.Encode(rpcAnswer(m.ID, text(strconv.Itoa(pid)), nil))
		default:
			result, rpcError := state.answer(m.Method, m.Params)
			_ = out.Encode(rpcAnswer(m.ID, result, rpcError))
		}
	}
	return 0
}

// appendLine adds a line to a file in the server's folder.
func appendLine(name, line string) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintln(file, line)
}

// spawnSleeper starts a descendant in the server's own process group, holding
// the server's error output and a lock in the server's folder, and returns
// once it holds the lock.
func spawnSleeper() (int, error) {
	command := exec.Command(os.Args[0], fakeSleeperArgument)
	command.Stderr = os.Stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := command.Start(); err != nil {
		return 0, err
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		return 0, fmt.Errorf("the sleeper did not get ready: %q %v", line, err)
	}
	return command.Process.Pid, nil
}

// runSleeper holds a lock in its working directory until it is killed or its
// own deadline passes, and leaves a marker only in the second case.
func runSleeper() int {
	lock, err := os.OpenFile(sleeperLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 1
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return 1
	}
	fmt.Println("ready")
	end := time.Now().Add(sleeperDeadline)
	for time.Now().Before(end) {
		time.Sleep(time.Second)
	}
	_ = os.WriteFile(sleeperMarker, []byte("deadline"), 0o600)
	return 0
}

// fakeHTTP serves the same answers over MCP's streamable HTTP.
type fakeHTTP struct {
	mu        sync.Mutex
	mode      string
	sessions  map[string]*fakeState
	opened    []string
	deleted   []string
	auth      []string
	versions  []string
	refusals  map[string]chan fakeMessage
	redirects int
	// elsewhere is where /elsewhere redirects to: another host.
	elsewhere string
}

func newFakeHTTP() *fakeHTTP {
	return &fakeHTTP{sessions: map[string]*fakeState{}, refusals: map[string]chan fakeMessage{}}
}

func (f *fakeHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/elsewhere" {
		http.Redirect(w, r, f.elsewhere, http.StatusTemporaryRedirect)
		return
	}
	if r.URL.Path == "/moved" {
		f.mu.Lock()
		f.redirects++
		f.mu.Unlock()
		http.Redirect(w, r, "/mcp", http.StatusTemporaryRedirect)
		return
	}
	f.mu.Lock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.versions = append(f.versions, r.Header.Get(versionHeader))
	session := r.Header.Get(sessionHeader)
	f.mu.Unlock()
	if r.Method == http.MethodDelete {
		f.mu.Lock()
		f.deleted = append(f.deleted, session)
		delete(f.sessions, session)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}
	var m fakeMessage
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		http.Error(w, "not json", http.StatusBadRequest)
		return
	}
	if m.Method == "initialize" {
		f.mu.Lock()
		session = fmt.Sprintf("session-%d", len(f.opened)+1)
		f.opened = append(f.opened, session)
		f.sessions[session] = &fakeState{mode: f.mode, credential: strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")}
		f.mu.Unlock()
		w.Header().Set(sessionHeader, session)
	}
	f.mu.Lock()
	state, known := f.sessions[session]
	f.mu.Unlock()
	if !known {
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}
	if len(m.ID) == 0 || m.Method == "" {
		// A notification, or the answer to a request of ours.
		if m.Method == "" {
			f.mu.Lock()
			waiting := f.refusals[string(m.ID)]
			f.mu.Unlock()
			if waiting != nil {
				waiting <- m
			}
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var call struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(m.Params, &call)
	if m.Method == "tools/call" && call.Name == "ask-back" {
		f.askBack(w, m)
		return
	}
	result, rpcError := state.answer(m.Method, m.Params)
	if m.Method == "tools/list" {
		// The listing comes as an event stream, the other answers as JSON, so
		// both shapes of answer are read.
		w.Header().Set("Content-Type", eventStream)
		writeEvent(w, rpcAnswer(m.ID, result, rpcError))
		return
	}
	w.Header().Set("Content-Type", jsonMedia)
	_ = json.NewEncoder(w).Encode(rpcAnswer(m.ID, result, rpcError))
}

// askBack answers with an event stream that makes a request of the client
// first, and finishes once the client has answered that request.
func (f *fakeHTTP) askBack(w http.ResponseWriter, m fakeMessage) {
	back := make(chan fakeMessage, 1)
	f.mu.Lock()
	f.refusals[`"server-1"`] = back
	f.mu.Unlock()
	w.Header().Set("Content-Type", eventStream)
	writeEvent(w, map[string]any{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]any{}})
	writeEvent(w, map[string]any{"jsonrpc": "2.0", "id": "server-1", "method": "elicitation/create", "params": map[string]any{}})
	got := <-back
	reply := "answered"
	if got.Error != nil {
		reply = "refused with " + strconv.Itoa(got.Error.Code) + " for " + string(got.ID)
	}
	writeEvent(w, rpcAnswer(m.ID, text(reply), nil))
}

func writeEvent(w http.ResponseWriter, value any) {
	encoded, _ := json.Marshal(value)
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", encoded)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// sleeperGone waits for the sleeper to let go of its lock, which it does only
// by dying, and reports whether it was killed rather than reaching its own
// deadline.
func sleeperGone(t *testing.T, folder string) bool {
	t.Helper()
	lock, err := os.OpenFile(filepath.Join(folder, sleeperLock), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open the sleeper's lock: %v", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("take the sleeper's lock: %v", err)
	}
	_, err = os.Stat(filepath.Join(folder, sleeperMarker))
	return os.IsNotExist(err)
}
