package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeKeychain is a keychain holding whatever items the test stored, so the
// keychain source is driven without reading the machine's.
type fakeKeychain struct {
	items map[string]string
	asked []string
}

func (k *fakeKeychain) Secret(_ context.Context, name string) (string, error) {
	k.asked = append(k.asked, name)
	value, stored := k.items[name]
	if !stored {
		return "", errors.New("the stored secret could not be read: The specified item could not be found in the keychain.")
	}
	return value, nil
}

// The command reads the token from the store services.dashboard.token names
// and serves under it, printing where it came from and never what it is, so
// the same token serves after a restart; only the generated source prints a
// token. A store that does not hold it refuses to start with the command that
// stores it — the one `yoyo doctor` prints under service:dashboard.
func TestDashboardReadsTheTokenFromTheConfiguredSource(t *testing.T) {
	t.Parallel()
	const product = "yoyodyne"
	stateRoot := t.TempDir()
	file := dashboard.TokenFile(stateRoot, product)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	stores := func(keychain *fakeKeychain) dashboard.TokenStores {
		s := dashboard.TokenStores{Platform: "darwin", StateRoot: stateRoot}
		if keychain != nil {
			s.Keychain = keychain
		}
		return s
	}
	reader := dashboardReader{}

	t.Run("generated prints a fresh token once", func(t *testing.T) {
		server, lines, err := dashboardServer(context.Background(), config.DashboardTokenGenerated, product, stores(&fakeKeychain{}), reader)
		if err != nil {
			t.Fatal(err)
		}
		printed := strings.Join(lines, "\n")
		if len(server.Token()) != 64 || strings.Count(printed, server.Token()) != 1 || !strings.Contains(printed, "token: "+server.Token()) {
			t.Fatalf("the generated token is not printed once:\n%s", printed)
		}
		if !strings.Contains(printed, "a restarted dashboard prints a new one") {
			t.Fatalf("the generated source does not say a restart makes a new token:\n%s", printed)
		}
	})

	t.Run("keychain serves under the stored item and never prints it", func(t *testing.T) {
		keychain := &fakeKeychain{items: map[string]string{"yoyo-dashboard.yoyodyne": "stored-keychain-token\n"}}
		server, lines, err := dashboardServer(context.Background(), config.DashboardTokenKeychain, product, stores(keychain), reader)
		if err != nil {
			t.Fatal(err)
		}
		if server.Token() != "stored-keychain-token" {
			t.Fatalf("token = %q, want the stored item without its trailing newline", server.Token())
		}
		if len(keychain.asked) != 1 || keychain.asked[0] != "yoyo-dashboard.yoyodyne" {
			t.Fatalf("asked the keychain for %v, want the product's own item", keychain.asked)
		}
		printed := strings.Join(lines, "\n")
		if strings.Contains(printed, "stored-keychain-token") {
			t.Fatalf("the supplied token is printed:\n%s", printed)
		}
		for _, want := range []string{
			"read from the keychain item yoyo-dashboard.yoyodyne under the account yoyo",
			"is not printed",
			"a restarted dashboard reads the same one",
		} {
			if !strings.Contains(printed, want) {
				t.Fatalf("the lines lack %q:\n%s", want, printed)
			}
		}
	})

	t.Run("a missing keychain item refuses with the command that stores it", func(t *testing.T) {
		_, _, err := dashboardServer(context.Background(), config.DashboardTokenKeychain, product, stores(&fakeKeychain{}), reader)
		if !errors.Is(err, dashboard.ErrTokenUnavailable) {
			t.Fatalf("err = %v, want the token unavailable", err)
		}
		// The remedy is the doctor's, from the one function both read it from.
		if want := "security add-generic-password -s yoyo-dashboard.yoyodyne -a yoyo -w"; !strings.Contains(err.Error(), want) || dashboard.KeychainStoreCommand(product) != want {
			t.Fatalf("err = %v, want it to carry %q", err, want)
		}
		if !strings.Contains(err.Error(), "yoyo-dashboard.yoyodyne under the account yoyo") {
			t.Fatalf("err = %v, want the item named", err)
		}
	})

	t.Run("the keychain source on a platform without one refuses naming the file store", func(t *testing.T) {
		s := stores(nil)
		s.Platform = "linux"
		_, _, err := dashboardServer(context.Background(), config.DashboardTokenKeychain, product, s, reader)
		if !errors.Is(err, dashboard.ErrTokenUnavailable) || !strings.Contains(err.Error(), "this platform (linux) has no keychain") || !strings.Contains(err.Error(), dashboard.FileStoreCommand(file)) {
			t.Fatalf("err = %v, want the platform named and the file store's command", err)
		}
	})

	t.Run("a missing file refuses with the command that writes it", func(t *testing.T) {
		_, _, err := dashboardServer(context.Background(), config.DashboardTokenFile, product, stores(nil), reader)
		if !errors.Is(err, dashboard.ErrTokenUnavailable) || !strings.Contains(err.Error(), file) || !strings.Contains(err.Error(), dashboard.FileStoreCommand(file)) {
			t.Fatalf("err = %v, want the file named and the command that writes it", err)
		}
		if !strings.Contains(err.Error(), "mkdir -p") || !strings.Contains(err.Error(), "umask 077 && openssl rand -hex 32 > ") {
			t.Fatalf("err = %v, want the doctor's own remedy", err)
		}
	})

	t.Run("an empty file refuses with the command that rewrites it", func(t *testing.T) {
		if err := os.WriteFile(file, []byte("\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := dashboardServer(context.Background(), config.DashboardTokenFile, product, stores(nil), reader)
		if !errors.Is(err, dashboard.ErrTokenUnavailable) || !strings.Contains(err.Error(), "holds no value") || !strings.Contains(err.Error(), dashboard.FileRewriteCommand(file)) {
			t.Fatalf("err = %v, want the empty file refused with the rewrite", err)
		}
	})

	t.Run("file serves under the stored token and never prints it", func(t *testing.T) {
		if err := os.WriteFile(file, []byte("stored-file-token\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		server, lines, err := dashboardServer(context.Background(), config.DashboardTokenFile, product, stores(nil), reader)
		if err != nil {
			t.Fatal(err)
		}
		if server.Token() != "stored-file-token" {
			t.Fatalf("token = %q, want the file's contents without the newline", server.Token())
		}
		printed := strings.Join(lines, "\n")
		if strings.Contains(printed, "stored-file-token") {
			t.Fatalf("the supplied token is printed:\n%s", printed)
		}
		if !strings.Contains(printed, "read from the file "+file) || !strings.Contains(printed, "a restarted dashboard reads the same one") {
			t.Fatalf("the lines do not say where the token came from:\n%s", printed)
		}
		// A second start reads the same token: that is the whole point.
		again, _, err := dashboardServer(context.Background(), config.DashboardTokenFile, product, stores(nil), reader)
		if err != nil || again.Token() != server.Token() {
			t.Fatalf("a restart read %q (%v), want the same token %q", again.Token(), err, server.Token())
		}
	})
}

// A store a reading cannot open costs that reading its figures, and the reason
// reaches the JSON the page reads: the error state says what failed rather than
// that a source was absent. The throughput and the spend read different stores
// and carry the refusal each under its own name.
func TestDashboardReadingsNameAStoreTheyCouldNotOpen(t *testing.T) {
	t.Parallel()
	// Both stores refuse a relative root.
	throughput := throughputSources("relative/state", "yoyodyne")
	if throughput.Runs != nil || throughput.RunsProblem == "" {
		t.Fatalf("throughput sources over an unopenable root: %+v", throughput)
	}
	spend := spendSources("relative/state", "yoyodyne")
	if spend.Ledger != nil || spend.LedgerProblem == "" {
		t.Fatalf("spend sources over an unopenable root: %+v", spend)
	}

	encoded, err := json.Marshal(readmodel.ReadThroughput(context.Background(), throughput))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"runs_problem":"the recorded runs could not be opened: state root must be an absolute path"`) {
		t.Fatalf("the throughput JSON does not name the store it could not open: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"label":"today"`) {
		t.Fatalf("the windows are missing from a reading with unopenable sources: %s", encoded)
	}

	priced, err := json.Marshal(readmodel.ReadSpend(context.Background(), spend))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(priced), `"problem":"the spend could not be opened: state root must be an absolute path"`) {
		t.Fatalf("the spend JSON does not name the store it could not open: %s", priced)
	}
	if !strings.Contains(string(priced), `"label":"last 24 hours"`) || !strings.Contains(string(priced), `"days":null`) {
		t.Fatalf("an unreadable spend drops its windows or invents a listing: %s", priced)
	}
}

// The verb refuses at the terminal, before it binds anything, when the state it
// would serve cannot be read: a dashboard over an unreadable state root is a
// page that looks like a dashboard and is not one.
func TestDashboardRefusesAnUnreadableConfiguration(t *testing.T) {
	t.Parallel()
	stdout, stderr, code := runCLI(t, "dashboard", "--config", "/nowhere/config.yaml")
	if code != 1 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if stdout != "" || stderr == "" {
		t.Fatalf("stdout = %q, stderr = %q, want the refusal on stderr alone", stdout, stderr)
	}
	if _, _, code := runCLI(t, "dashboard", "extra"); code != 2 {
		t.Fatalf("positional argument: code = %d", code)
	}
}

// guardedBuffer is a buffer the command writes from its own goroutine while the
// test reads it, which a bare bytes.Buffer is not safe for.
type guardedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *guardedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *guardedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// announcingBuffer is a guardedBuffer that says when a line beginning with its
// prefix has been written, so a test waits on the command having printed it
// rather than polling for it against a length of time.
type announcingBuffer struct {
	guardedBuffer
	prefix    string
	announced chan struct{}
	once      sync.Once
}

func newAnnouncingBuffer(prefix string) *announcingBuffer {
	return &announcingBuffer{prefix: prefix, announced: make(chan struct{})}
}

func (b *announcingBuffer) Write(p []byte) (int, error) {
	n, err := b.guardedBuffer.Write(p)
	// Only whole lines count, so the line is announced once it is all there.
	written := b.String()
	complete := written[:strings.LastIndex(written, "\n")+1]
	for _, line := range strings.Split(complete, "\n") {
		if strings.HasPrefix(line, b.prefix) {
			b.once.Do(func() { close(b.announced) })
			break
		}
	}
	return n, err
}

// The verb starts, prints its URL and its token, and stops when asked. The
// token is printed once and is not in the URL. The bind is what a sandbox
// refuses, so this skips rather than fails where it is refused.
func TestDashboardPrintsItsURLAndTokenAndStopsWhenAsked(t *testing.T) {
	// Not parallel: the state root the command reads is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := newAnnouncingBuffer("token: ")
	var stderr guardedBuffer
	done := make(chan int, 1)
	go func() {
		done <- RunContext(ctx, []string{"dashboard", "--config", configPath}, stdout, &stderr, "test")
	}()

	// Wait for the token to be printed or the command to give up, whichever it
	// does; a command that does neither is reported by go test's own timeout,
	// with the stack of what it was doing, rather than by a bound set here.
	select {
	case <-stdout.announced:
	case code := <-done:
		if strings.Contains(stderr.String(), "operation not permitted") {
			t.Skipf("this environment grants no listener: %s", strings.TrimSpace(stderr.String()))
		}
		t.Fatalf("dashboard exited early with %d: stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	printed := stdout.String()
	if !strings.Contains(printed, "serving at http://127.0.0.1:") || !strings.Contains(printed, "token: ") {
		t.Fatalf("stdout = %q, want the URL and the token", printed)
	}
	var token string
	for _, line := range strings.Split(printed, "\n") {
		if rest, found := strings.CutPrefix(line, "token: "); found {
			token = rest
		}
	}
	if len(token) != 64 || strings.Contains(printed[:strings.Index(printed, "token: ")], token) {
		t.Fatalf("token %q is not printed once on its own line, apart from the URL:\n%s", token, printed)
	}

	cancel()
	if code := <-done; code != 0 || !strings.Contains(stdout.String(), "dashboard stopped") {
		t.Fatalf("stop: code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

// A dashboard the supervisor starts reads services.dashboard.bind, and one
// that is not a loopback address is refused before anything is bound, in
// words that name the setting and say the dashboard listens only on this
// machine.
func TestASupervisedDashboardRefusesABindOutsideLoopback(t *testing.T) {
	// Not parallel: the state root the command reads is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig+"services:\n  dashboard:\n    bind: 192.168.1.20\n    token: keychain\n")
	startID, err := runstate.NewDashboardStartID()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := RunContext(context.Background(), []string{"dashboard", "--config", configPath, "--port", "8765", "--start-id", startID}, &stdout, &stderr, "test")
	if code != 1 || !strings.Contains(stderr.String(), `services.dashboard.bind "192.168.1.20" is not a loopback address, and the dashboard listens only on this machine`) {
		t.Fatalf("code = %d, stderr = %q, want the bind refused", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "serving at") {
		t.Fatalf("stdout = %q, want nothing served", stdout.String())
	}
}
