package cli

// These tests are the evidence that who a running dashboard accepts follows the
// configuration and the token stores with nobody restarting it: an operator
// bound after it started, and that operator's token file created, is accepted;
// a token changed or removed is refused afterwards; and a host added to or
// taken out of services.dashboard.allowed_hosts is answered or refused. Each
// drives the dashboard's own re-reading over a real listener, so what is
// checked is a request the running server answers.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
)

// accessConfig is a project configuration whose dashboard answers to the
// allowed hosts given and, where operator is set, binds that operator's own
// token from a file.
func accessConfig(operator bool, hosts ...string) string {
	return accessConfigUnder(config.DashboardTokenGenerated, operator, hosts...)
}

func accessConfigUnder(source config.DashboardTokenSource, operator bool, hosts ...string) string {
	text := `version: 1
extends: builtin:v1
product:
  id: yoyodyne
  repository: .
services:
  dashboard:
    enabled: true
    token: ` + string(source) + `
    allowed_hosts: [` + strings.Join(hosts, ", ") + `]
`
	if operator {
		text += `operators:
  mason:
    git_email: mason@example.com
    dashboard_token: file
    grants: [own-intent]
`
	}
	return text
}

// runningDashboard is a dashboard serving on a loopback port the operating
// system chose, re-reading who it accepts every few milliseconds from the
// configuration at path and the token files under stateRoot, as `yoyo
// dashboard` does every ten seconds. What it could not apply is kept as it
// changes, as the dashboard keeps it on its record.
type runningDashboard struct {
	t        *testing.T
	server   *dashboard.Server
	port     int
	mu       sync.Mutex
	problems []string
}

func startRunningDashboard(t *testing.T, path, stateRoot string, supervised bool) *runningDashboard {
	t.Helper()
	return startRunningDashboardOn(t, path, stateRoot, supervised, config.DashboardTokenGenerated)
}

// startRunningDashboardOn is startRunningDashboard serving under the shared
// token the source names, read from its store as `yoyo dashboard` reads it.
func startRunningDashboardOn(t *testing.T, path, stateRoot string, supervised bool, source config.DashboardTokenSource) *runningDashboard {
	t.Helper()
	stores := dashboard.TokenStores{Platform: "linux", StateRoot: stateRoot}
	server, _, err := dashboardServer(context.Background(), source, "yoyodyne", stores, stubDashboardReader{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Listen(0); err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("this environment grants no listener: %v", err)
		}
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(ctx)
	}()
	running := &runningDashboard{t: t, server: server, port: server.Port()}
	access := &dashboardAccess{
		configPath: path,
		stores:     stores,
		productID:  "yoyodyne",
		source:     source,
		supervised: supervised,
		every:      5 * time.Millisecond,
	}
	resolved, err := config.LoadResolved(path)
	if err != nil {
		t.Fatal(err)
	}
	reading := access.applyConfig(ctx, server, resolved.Config)
	running.recorded(reading.problems)
	access.changed(reading)
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		access.watch(ctx, server, discard{}, running.recorded)
	}()
	t.Cleanup(func() {
		cancel()
		<-watched
		<-served
	})
	return running
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func (r *runningDashboard) recorded(problems []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.problems = problems
}

func (r *runningDashboard) recordedProblems() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.problems, "\n")
}

// session asks the running dashboard who a token is, naming host in the
// request as a browser pointed at that name would, and returns the status and
// the person it resolved to.
func (r *runningDashboard) session(host, token string) (int, string) {
	r.t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://"+host+":"+strconv.Itoa(r.port)+"/api/session", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	r.server.Handler().ServeHTTP(recorder, request)
	var answer struct {
		Person *dashboard.Person `json:"person"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &answer)
	if answer.Person == nil {
		return recorder.Code, ""
	}
	return recorder.Code, answer.Person.Name
}

// eventually waits for what the running dashboard answers to come round, for
// as long as a re-read every few milliseconds could take on a loaded machine.
func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("the running dashboard never came round: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func writePersonToken(t *testing.T, stateRoot, token string) string {
	t.Helper()
	file := dashboard.PersonTokenFile(stateRoot, "yoyodyne", "mason")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// An operator bound in the configuration of a dashboard already running, and
// whose token file is then created, is accepted as that person with nobody
// restarting it; in between, the dashboard records that it could not read the
// token, with the command that stores it.
func TestARunningDashboardAcceptsAnOperatorBoundAndStoredAfterItStarted(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	path := writeProjectConfig(t, accessConfig(false))
	running := startRunningDashboard(t, path, stateRoot, false)
	token := strings.Repeat("c3", 32)

	if code, _ := running.session("127.0.0.1", token); code != http.StatusUnauthorized {
		t.Fatalf("a token nobody bound: %d, want it refused", code)
	}
	if err := os.WriteFile(path, []byte(accessConfig(true)), 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the newly bound operator's missing token recorded", func() bool {
		return strings.Contains(running.recordedProblems(), "mason cannot approve, decline, or ask from the page") &&
			strings.Contains(running.recordedProblems(), "dashboard.mason.token")
	})
	if code, _ := running.session("127.0.0.1", token); code != http.StatusUnauthorized {
		t.Fatalf("a token not yet stored: %d, want it refused", code)
	}
	writePersonToken(t, stateRoot, token)
	eventually(t, "the operator's token accepted", func() bool {
		code, person := running.session("127.0.0.1", token)
		return code == http.StatusOK && person == "mason"
	})
	eventually(t, "nothing left recorded as unapplied", func() bool { return running.recordedProblems() == "" })
	if strings.Contains(running.recordedProblems(), token) {
		t.Fatal("a token was recorded")
	}
}

// A person's token changed in its store is refused from the next re-read on,
// and the new one accepted; a token taken out of its store is refused, and so
// is a person taken out of the configuration.
func TestARunningDashboardRefusesAnOperatorTokenChangedOrRemoved(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	path := writeProjectConfig(t, accessConfig(true))
	old := strings.Repeat("d4", 32)
	file := writePersonToken(t, stateRoot, old)
	running := startRunningDashboard(t, path, stateRoot, false)
	if code, person := running.session("127.0.0.1", old); code != http.StatusOK || person != "mason" {
		t.Fatalf("the stored token at start: %d %q", code, person)
	}

	replacement := strings.Repeat("e5", 32)
	writePersonToken(t, stateRoot, replacement)
	eventually(t, "the changed token accepted", func() bool {
		code, person := running.session("127.0.0.1", replacement)
		return code == http.StatusOK && person == "mason"
	})
	if code, _ := running.session("127.0.0.1", old); code != http.StatusUnauthorized {
		t.Fatalf("the old token after it was changed: %d, want it refused", code)
	}

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the removed token refused", func() bool {
		code, _ := running.session("127.0.0.1", replacement)
		return code == http.StatusUnauthorized
	})
	eventually(t, "the removed token recorded", func() bool {
		return strings.Contains(running.recordedProblems(), "is not there; store it with")
	})

	// Stored again, and then the person unbound in the configuration.
	writePersonToken(t, stateRoot, replacement)
	eventually(t, "the token stored again accepted", func() bool {
		code, _ := running.session("127.0.0.1", replacement)
		return code == http.StatusOK
	})
	if err := os.WriteFile(path, []byte(accessConfig(false)), 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the unbound person's token refused", func() bool {
		code, _ := running.session("127.0.0.1", replacement)
		return code == http.StatusUnauthorized
	})
}

// A dashboard the supervisor starts answers to a host added to
// services.dashboard.allowed_hosts while it runs, and refuses it once it is
// taken out again. A dashboard started by hand answers to the address it bound
// alone, whatever the list says.
func TestARunningDashboardFollowsTheAllowedHosts(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	path := writeProjectConfig(t, accessConfig(false))
	running := startRunningDashboard(t, path, stateRoot, true)
	shared := running.server.Token()

	if code, _ := running.session("yoyo.local", shared); code != http.StatusForbidden {
		t.Fatalf("a host not allowed: %d, want it refused", code)
	}
	if err := os.WriteFile(path, []byte(accessConfig(false, "yoyo.local")), 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the allowed host answered", func() bool {
		code, _ := running.session("yoyo.local", shared)
		return code == http.StatusOK
	})
	if err := os.WriteFile(path, []byte(accessConfig(false)), 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the host taken out refused", func() bool {
		code, _ := running.session("yoyo.local", shared)
		return code == http.StatusForbidden
	})
	if code, _ := running.session("127.0.0.1", shared); code != http.StatusOK {
		t.Fatalf("the bound address: %d", code)
	}

	byHand := startRunningDashboard(t, writeProjectConfig(t, accessConfig(false, "yoyo.local")), stateRoot, false)
	if code, _ := byHand.session("yoyo.local", byHand.server.Token()); code != http.StatusForbidden {
		t.Fatalf("a dashboard started by hand answered an allowed host: %d", code)
	}
}

// A configuration that does not load leaves who the dashboard accepts as it
// was, and says so.
func TestARunningDashboardKeepsWhoItAcceptsOverABrokenConfiguration(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	path := writeProjectConfig(t, accessConfig(true))
	token := strings.Repeat("f6", 32)
	writePersonToken(t, stateRoot, token)
	running := startRunningDashboard(t, path, stateRoot, false)
	if err := os.WriteFile(path, []byte("version: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the broken configuration recorded", func() bool {
		return strings.Contains(running.recordedProblems(), "who the dashboard accepts is left as it was")
	})
	if code, person := running.session("127.0.0.1", token); code != http.StatusOK || person != "mason" {
		t.Fatalf("over a broken configuration: %d %q, want the operator still accepted", code, person)
	}
}

// The dashboard's own token, read from the store services.dashboard.token
// names, is read again while it runs: a token changed there replaces the one
// it serves under, and one taken out of the store is refused until it is
// stored again, with the reason recorded.
func TestARunningDashboardFollowsItsOwnStoredToken(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	path := writeProjectConfig(t, accessConfigUnder(config.DashboardTokenFile, false))
	file := dashboard.TokenFile(stateRoot, "yoyodyne")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	old := strings.Repeat("a7", 32)
	if err := os.WriteFile(file, []byte(old+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	running := startRunningDashboardOn(t, path, stateRoot, false, config.DashboardTokenFile)
	if code, _ := running.session("127.0.0.1", old); code != http.StatusOK {
		t.Fatalf("the stored token at start: %d", code)
	}
	replacement := strings.Repeat("b8", 32)
	if err := os.WriteFile(file, []byte(replacement+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the changed shared token accepted", func() bool {
		code, _ := running.session("127.0.0.1", replacement)
		return code == http.StatusOK
	})
	if code, _ := running.session("127.0.0.1", old); code != http.StatusUnauthorized {
		t.Fatalf("the old shared token: %d, want it refused", code)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the removed shared token refused", func() bool {
		code, _ := running.session("127.0.0.1", replacement)
		return code == http.StatusUnauthorized
	})
	if code, _ := running.session("127.0.0.1", ""); code != http.StatusUnauthorized {
		t.Fatalf("an empty token with no shared token stored: %d, want it refused", code)
	}
	eventually(t, "the missing shared token recorded", func() bool {
		return strings.Contains(running.recordedProblems(), "the dashboard's own token could not be read again")
	})
}
