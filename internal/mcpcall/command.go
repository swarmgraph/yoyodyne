package mcpcall

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// inheritedVariables are the only variables a command server is handed from
// the harness's own environment, each only where the harness has it set. The
// rest of what it gets is set here: its home, its working directory, and its
// credential in the variable it names. Nothing else the harness's environment
// holds — a provider's key, a token, a variable a test set — reaches it.
var inheritedVariables = []string{"PATH", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "USER", "LOGNAME", "TMPDIR"}

// stopGrace is how long a command server is given to exit by itself once its
// input is closed, and how long the client waits afterwards for whatever the
// group left holding its error output, before the group is killed and the
// client stops waiting. It is the client's own timer, not a bound on the work.
const stopGrace = 2 * time.Second

// stderrTail is how much of a command server's error output is kept, to say
// why it stopped.
const stderrTail = 4 << 10

// commandEnvironment is the environment a command server starts with.
func commandEnvironment(folder, credentialEnv, credential string) []string {
	environment := make([]string, 0, len(inheritedVariables)+3)
	for _, name := range inheritedVariables {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	environment = append(environment, "HOME="+folder, "PWD="+folder)
	if credential != "" {
		environment = append(environment, credentialEnv+"="+credential)
	}
	return environment
}

type commandTransport struct {
	name     string
	command  *exec.Cmd
	stdin    io.WriteCloser
	messages chan message
	// readErr is why the server's output ended, set before messages closes.
	readErr  error
	stdout   *os.File
	stderr   *os.File
	tail     *tailBuffer
	stderrOK chan struct{}
	exited   chan error
}

// startCommand starts a command server in a process group of its own, in its
// own folder, with the environment above. Output and error output come through
// pipes the client owns rather than through exec's copying, so a process the
// server left behind holding them cannot keep Wait from returning.
func startCommand(ctx context.Context, server Server, stores mcpcredential.Stores, credential string) (transport, error) {
	if !groupSupported {
		return nil, fmt.Errorf("%w: mcp server %q is a command, and this platform cannot give it a process group of its own", ErrRefused, server.Name)
	}
	if err := domain.ValidateIdentifier("mcp server name", server.Name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRefused, err)
	}
	if strings.TrimSpace(stores.StateRoot) == "" || stores.Product == "" {
		return nil, fmt.Errorf("%w: mcp server %q has no state directory to keep its own folder in", ErrRefused, server.Name)
	}
	root, err := repowrite.NewRoot(stores.StateRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: the state directory for mcp server %q: %v", ErrFailed, server.Name, err)
	}
	folder, err := root.MakeDirectory(mcpcredential.ServerDirectoryWithin(stores.StateRoot, stores.Product, server.Name), 0o700)
	if err != nil {
		return nil, fmt.Errorf("%w: the folder for mcp server %q could not be made: %v", ErrFailed, server.Name, err)
	}
	declared := server.Declared
	command := exec.CommandContext(ctx, declared.Command[0], declared.Command[1:]...)
	command.Dir = folder
	command.Env = commandEnvironment(folder, declared.CredentialEnv, credential)
	configureGroup(command)

	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: mcp server %q: %v", ErrFailed, server.Name, err)
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("%w: mcp server %q: %v", ErrFailed, server.Name, err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		stdoutRead.Close()
		stdoutWrite.Close()
		return nil, fmt.Errorf("%w: mcp server %q: %v", ErrFailed, server.Name, err)
	}
	command.Stdout, command.Stderr = stdoutWrite, stderrWrite
	startErr := command.Start()
	stdoutWrite.Close()
	stderrWrite.Close()
	if startErr != nil {
		stdoutRead.Close()
		stderrRead.Close()
		return nil, fmt.Errorf("%w: mcp server %q could not be started: %v", ErrFailed, server.Name, startErr)
	}
	t := &commandTransport{
		name:     server.Name,
		command:  command,
		stdin:    stdin,
		messages: make(chan message),
		stdout:   stdoutRead,
		stderr:   stderrRead,
		tail:     &tailBuffer{limit: stderrTail},
		stderrOK: make(chan struct{}),
		exited:   make(chan error, 1),
	}
	go t.read()
	go func() {
		_, _ = io.Copy(t.tail, stderrRead)
		close(t.stderrOK)
	}()
	go func() { t.exited <- command.Wait() }()
	return t, nil
}

// read turns the server's output into messages, one a line. A line that is
// not a JSON-RPC message is passed over; a line past the bound ends the
// output, since nothing after it can be trusted to start on a message.
func (t *commandTransport) read() {
	defer close(t.messages)
	scanner := bufio.NewScanner(t.stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), maxMessageBytes)
	for scanner.Scan() {
		var m message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			continue
		}
		t.messages <- m
	}
	if err := scanner.Err(); err != nil {
		t.readErr = err
	} else {
		t.readErr = io.EOF
	}
}

func (t *commandTransport) send(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFailed, err)
	}
	if _, err := t.stdin.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("%w: %w: mcp server %q stopped reading: %v%s", ErrFailed, ErrUnreachable, t.name, err, t.said())
	}
	return nil
}

func (t *commandTransport) call(ctx context.Context, id int64, request map[string]any) (message, error) {
	if err := t.send(request); err != nil {
		return message{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return message{}, fmt.Errorf("mcp server %q did not answer in time: %w", t.name, ctx.Err())
		case m, open := <-t.messages:
			if !open {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
					return message{}, fmt.Errorf("mcp server %q did not answer in time: %w", t.name, ctx.Err())
				}
				return message{}, fmt.Errorf("%w: %w: mcp server %q stopped before answering: %v%s", ErrFailed, ErrUnreachable, t.name, t.readErr, t.said())
			}
			switch {
			case m.answers(id):
				return m, nil
			case m.isRequest():
				if err := t.send(refusal(m.ID)); err != nil {
					return message{}, err
				}
			}
		}
	}
}

func (t *commandTransport) notify(_ context.Context, method string, params any) error {
	return t.send(notification(method, params))
}

func (t *commandTransport) settled(string) {}

// close ends the server: its input is closed, which is how MCP asks a command
// server to stop; it is given stopGrace to exit, and then whatever is left of
// its group is killed, on every way out. The client then waits, for at most
// stopGrace, for the group to let go of the error output, which is the sign
// that everything the group started has gone.
func (t *commandTransport) close() error {
	_ = t.stdin.Close()
	timer := time.NewTimer(stopGrace)
	defer timer.Stop()
	select {
	case <-t.exited:
	case <-timer.C:
		killGroup(t.command)
		<-t.exited
	}
	killGroup(t.command)
	settle := time.NewTimer(stopGrace)
	defer settle.Stop()
	select {
	case <-t.stderrOK:
	case <-settle.C:
	}
	_ = t.stdout.Close()
	_ = t.stderr.Close()
	for range t.messages {
	}
	return nil
}

// said is the end of what the server wrote to its error output, for an error.
func (t *commandTransport) said() string {
	text := strings.TrimSpace(t.tail.String())
	if text == "" {
		return ""
	}
	return "; it said: " + oneLine(text)
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
