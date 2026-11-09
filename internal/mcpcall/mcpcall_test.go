//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package mcpcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
)

const (
	testCredential                  = "s3cret-credential-value-0042"
	testProduct    domain.ProductID = "example"
)

// testStores is a state root of the test's own with a file-stored credential
// kept for each server named, where the stores read it from.
func testStores(t *testing.T, servers ...string) mcpcredential.Stores {
	t.Helper()
	stores := mcpcredential.Stores{StateRoot: t.TempDir(), Product: testProduct}
	if err := os.MkdirAll(mcpcredential.Directory(stores.StateRoot, testProduct), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, server := range servers {
		if err := os.WriteFile(mcpcredential.File(stores.StateRoot, testProduct, server), []byte(testCredential+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return stores
}

// commandServer is the fake declared as a command whose credential is kept in
// the file store, with the stores holding it.
func commandServer(t *testing.T, mode ...string) (Server, Options) {
	t.Helper()
	server := Server{Name: "fake", Declared: config.MCPServer{
		Command:       fakeCommand(mode...),
		Credential:    config.MCPCredentialFile,
		CredentialEnv: fakeCredentialEnv,
	}}
	return server, Options{Stores: testStores(t, server.Name)}
}

// serverFolder is where a command server's own folder is, as the client made
// it.
func serverFolder(t *testing.T, options Options, server Server) string {
	t.Helper()
	folder, err := filepath.EvalSymlinks(mcpcredential.ServerDirectory(options.Stores.StateRoot, testProduct, server.Name))
	if err != nil {
		t.Fatal(err)
	}
	return folder
}

func remoteServer(t *testing.T, fake *fakeHTTP) (Server, Options) {
	t.Helper()
	server := httptest.NewTLSServer(fake)
	t.Cleanup(server.Close)
	declared := Server{Name: "remote", Declared: config.MCPServer{URL: server.URL + "/mcp", Credential: config.MCPCredentialFile}}
	return declared, Options{Stores: testStores(t, declared.Name), Transport: server.Client().Transport}
}

func call(t *testing.T, session *Session, tool string, arguments string) Result {
	t.Helper()
	result, err := session.CallTool(context.Background(), tool, json.RawMessage(arguments))
	if err != nil {
		t.Fatalf("call %s: %v", tool, err)
	}
	return result
}

func onlyText(t *testing.T, result Result) string {
	t.Helper()
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("result content = %+v, want one text item", result.Content)
	}
	return result.Content[0].Text
}

// transports runs one test over both transports.
func transports(t *testing.T, run func(t *testing.T, server Server, options Options)) {
	t.Run("standard input", func(t *testing.T) {
		server, options := commandServer(t)
		run(t, server, options)
	})
	t.Run("https", func(t *testing.T) {
		server, options := remoteServer(t, newFakeHTTP())
		run(t, server, options)
	})
}

func TestTheClientOpensListsAndCallsOverBothTransports(t *testing.T) {
	transports(t, func(t *testing.T, server Server, options Options) {
		err := Batch(context.Background(), server, options, func(session *Session) error {
			if session.Version() != ProtocolVersions[0] {
				t.Errorf("settled version = %q, want %q", session.Version(), ProtocolVersions[0])
			}
			tools, err := session.ListTools(context.Background())
			if err != nil {
				return err
			}
			var names []string
			for _, tool := range tools {
				names = append(names, tool.Name)
			}
			if got, want := strings.Join(names, ","), "echo,environment,opening,ask-back,spawn,fail-with-credential"; got != want {
				t.Errorf("tools across both pages = %s, want %s", got, want)
			}
			if got := onlyText(t, call(t, session, "echo", `{"query":"invoice"}`)); got != `{"query":"invoice"}` {
				t.Errorf("echo = %s", got)
			}
			if result := call(t, session, "no-such-tool", ``); !result.IsError {
				t.Errorf("a tool the server reports failing is not marked as an error: %+v", result)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestTheClientDeclaresNoAbilities(t *testing.T) {
	transports(t, func(t *testing.T, server Server, options Options) {
		_ = Batch(context.Background(), server, options, func(session *Session) error {
			var opening struct {
				ProtocolVersion string                     `json:"protocolVersion"`
				Capabilities    map[string]json.RawMessage `json:"capabilities"`
				ClientInfo      struct {
					Name string `json:"name"`
				} `json:"clientInfo"`
			}
			if err := json.Unmarshal([]byte(onlyText(t, call(t, session, "opening", `{}`))), &opening); err != nil {
				t.Fatal(err)
			}
			if opening.Capabilities == nil || len(opening.Capabilities) != 0 {
				t.Errorf("capabilities declared = %v, want an empty object", opening.Capabilities)
			}
			if opening.ProtocolVersion != ProtocolVersions[0] || opening.ClientInfo.Name != ClientName {
				t.Errorf("opening = %+v", opening)
			}
			return nil
		})
	})
}

func TestEveryRequestAServerMakesBackIsRefused(t *testing.T) {
	transports(t, func(t *testing.T, server Server, options Options) {
		err := Batch(context.Background(), server, options, func(session *Session) error {
			got := onlyText(t, call(t, session, "ask-back", `{}`))
			if want := fmt.Sprintf("refused with %d for \"server-1\"", refusalCode); got != want {
				t.Errorf("the server heard %q, want %q", got, want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestAProtocolVersionOffTheListIsRefused(t *testing.T) {
	check := func(t *testing.T, err error) {
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "1999-01-01") {
			t.Fatalf("err = %v, want a refusal naming the version", err)
		}
	}
	t.Run("standard input", func(t *testing.T) {
		server, options := commandServer(t, fakeVersionMode)
		check(t, Batch(context.Background(), server, options, func(*Session) error {
			t.Fatal("the batch ran against a server speaking a version off the list")
			return nil
		}))
	})
	t.Run("https", func(t *testing.T) {
		fake := newFakeHTTP()
		fake.mode = fakeVersionMode
		server, options := remoteServer(t, fake)
		check(t, Batch(context.Background(), server, options, func(*Session) error {
			t.Fatal("the batch ran against a server speaking a version off the list")
			return nil
		}))
		if len(fake.deleted) != 1 {
			t.Errorf("sessions ended = %v, want the refused one ended", fake.deleted)
		}
	})
}

func TestACommandServerGetsAListedEnvironmentAndItsOwnFolder(t *testing.T) {
	t.Setenv("YOYO_MCP_LEAK_PROBE", "should-not-arrive")
	server, options := commandServer(t)
	var seen struct {
		Env  []string `json:"env"`
		Cwd  string   `json:"cwd"`
		Args []string `json:"args"`
	}
	err := Batch(context.Background(), server, options, func(session *Session) error {
		return json.Unmarshal([]byte(onlyText(t, call(t, session, "environment", `{}`))), &seen)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"HOME": true, "PWD": true, fakeCredentialEnv: true}
	for _, name := range inheritedVariables {
		want[name] = true
	}
	values := map[string]string{}
	for _, entry := range seen.Env {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = value
		if !want[name] {
			t.Errorf("the server was handed %s, which is not on the list", name)
		}
	}
	folder := serverFolder(t, options, server)
	cwd, _ := filepath.EvalSymlinks(seen.Cwd)
	home, _ := filepath.EvalSymlinks(values["HOME"])
	if cwd != folder || home != folder {
		t.Errorf("working directory %q and home %q, want both %q", seen.Cwd, values["HOME"], folder)
	}
	if values[fakeCredentialEnv] != testCredential {
		t.Errorf("the credential read from the file store did not arrive in %s", fakeCredentialEnv)
	}
	if strings.Contains(strings.Join(seen.Args, " "), testCredential) {
		t.Errorf("the credential is in the server's arguments: %v", seen.Args)
	}
}

func TestACommandServersGroupEndsWithTheBatch(t *testing.T) {
	server, options := commandServer(t)
	err := Batch(context.Background(), server, options, func(session *Session) error {
		call(t, session, "spawn", `{}`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sleeperGone(t, serverFolder(t, options, server)) {
		t.Fatal("what the server started outlived the batch until its own deadline")
	}
}

func TestACancelledBatchSaysSo(t *testing.T) {
	server, options := commandServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	err := Batch(ctx, server, options, func(session *Session) error {
		cancel()
		_, err := session.CallTool(ctx, "echo", nil)
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
}

func TestAURLServerGetsAFreshSessionForEachBatch(t *testing.T) {
	fake := newFakeHTTP()
	server, options := remoteServer(t, fake)
	for range 2 {
		if err := Batch(context.Background(), server, options, func(session *Session) error {
			call(t, session, "echo", `{}`)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(fake.opened, ","); got != "session-1,session-2" {
		t.Errorf("sessions opened = %s, want one for each batch", got)
	}
	if got := strings.Join(fake.deleted, ","); got != "session-1,session-2" {
		t.Errorf("sessions ended = %s, want each ended with its batch", got)
	}
	for i, header := range fake.auth {
		if header != "Bearer "+testCredential {
			t.Errorf("request %d carried Authorization %q", i, header)
		}
	}
	if fake.versions[0] != "" || fake.versions[len(fake.versions)-1] != ProtocolVersions[0] {
		t.Errorf("version headers = %v, want none on the opening and the settled one after", fake.versions)
	}
}

func TestACredentialIsInNoReturnedError(t *testing.T) {
	transports(t, func(t *testing.T, server Server, options Options) {
		err := Batch(context.Background(), server, options, func(session *Session) error {
			_, err := session.CallTool(context.Background(), "fail-with-credential", nil)
			return err
		})
		if !errors.Is(err, ErrFailed) {
			t.Fatalf("err = %v, want the server's error", err)
		}
		if strings.Contains(err.Error(), testCredential) {
			t.Fatalf("the credential is in the error: %v", err)
		}
		if !strings.Contains(err.Error(), "[credential removed]") {
			t.Fatalf("err = %v, want the server's message with the credential taken out", err)
		}
		for unwrapped := errors.Unwrap(err); unwrapped != nil; unwrapped = errors.Unwrap(unwrapped) {
			if strings.Contains(unwrapped.Error(), testCredential) {
				t.Fatalf("the credential is reachable by unwrapping: %v", unwrapped)
			}
		}
	})
}

func TestACredentialTheStoresWillNotProduceStopsTheBatch(t *testing.T) {
	server, _ := commandServer(t)
	options := Options{Stores: testStores(t)}
	err := Batch(context.Background(), server, options, func(*Session) error {
		t.Fatal("the batch ran without its credential")
		return nil
	})
	if !errors.Is(err, ErrCredential) || !strings.Contains(err.Error(), "fake.token") {
		t.Fatalf("err = %v, want the missing file named", err)
	}
}

func TestAServerDeclaringNoCredentialReadsNone(t *testing.T) {
	server, options := commandServer(t)
	server.Declared.Credential, server.Declared.CredentialEnv = config.MCPCredentialNone, ""
	options.Stores = testStores(t)
	var seen struct {
		Env []string `json:"env"`
	}
	if err := Batch(context.Background(), server, options, func(session *Session) error {
		return json.Unmarshal([]byte(onlyText(t, call(t, session, "environment", `{}`))), &seen)
	}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range seen.Env {
		if strings.HasPrefix(entry, fakeCredentialEnv+"=") {
			t.Errorf("a server declaring no credential was handed %s", entry)
		}
	}
}

func TestAServerDeclaredWrongIsRefusedBeforeAnythingStarts(t *testing.T) {
	command, options := commandServer(t)
	noVariable := command.Declared
	noVariable.CredentialEnv = ""
	cases := map[string]struct {
		server  Server
		options Options
	}{
		"neither":            {Server{Name: "x"}, options},
		"both":               {Server{Name: "x", Declared: config.MCPServer{Command: []string{"x"}, URL: "https://example.invalid/"}}, options},
		"plain http":         {Server{Name: "x", Declared: config.MCPServer{URL: "http://example.invalid/"}}, options},
		"user in the url":    {Server{Name: "x", Declared: config.MCPServer{URL: "https://user:pass@example.invalid/"}}, options},
		"no variable":        {Server{Name: "fake", Declared: noVariable}, options},
		"no state directory": {Server{Name: "x", Declared: config.MCPServer{Command: command.Declared.Command}}, Options{}},
		"a name that climbs": {Server{Name: "../x", Declared: config.MCPServer{Command: command.Declared.Command}}, options},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := Batch(context.Background(), c.server, c.options, func(*Session) error {
				t.Fatal("the batch ran")
				return nil
			})
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("err = %v, want a refusal", err)
			}
		})
	}
}

func TestARedirectIsFollowedOnlyOnTheSameHost(t *testing.T) {
	elsewhere := newFakeHTTP()
	other := httptest.NewTLSServer(elsewhere)
	t.Cleanup(other.Close)
	fake := newFakeHTTP()
	fake.elsewhere = other.URL + "/mcp"
	server, options := remoteServer(t, fake)
	base := strings.TrimSuffix(server.Declared.URL, "/mcp")

	server.Declared.URL = base + "/moved"
	if err := Batch(context.Background(), server, options, func(session *Session) error {
		call(t, session, "echo", `{}`)
		return nil
	}); err != nil {
		t.Fatalf("a redirect on the same host was not followed: %v", err)
	}
	if fake.redirects == 0 || len(fake.opened) != 1 {
		t.Fatalf("redirects = %d, sessions = %v; want the redirect followed to the session", fake.redirects, fake.opened)
	}

	server.Declared.URL = base + "/elsewhere"
	err := Batch(context.Background(), server, options, func(*Session) error { return nil })
	if !errors.Is(err, ErrFailed) || !strings.Contains(err.Error(), "another host") {
		t.Fatalf("err = %v, want the redirect to another host refused", err)
	}
	if len(elsewhere.auth) != 0 {
		t.Fatalf("the other host was sent %d requests, credential %v", len(elsewhere.auth), elsewhere.auth)
	}
	if strings.Contains(err.Error(), testCredential) {
		t.Fatalf("the credential is in the error: %v", err)
	}
}

func TestACallAfterTheBatchHasEndedIsRefused(t *testing.T) {
	server, options := commandServer(t)
	var kept *Session
	if err := Batch(context.Background(), server, options, func(session *Session) error {
		kept = session
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := kept.CallTool(context.Background(), "echo", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want the batch said to have ended", err)
	}
}

func TestArgumentsThatAreNotOneObjectAreRefused(t *testing.T) {
	server, options := commandServer(t)
	_ = Batch(context.Background(), server, options, func(session *Session) error {
		if _, err := session.CallTool(context.Background(), "echo", json.RawMessage(`[1]`)); !errors.Is(err, ErrRefused) {
			t.Errorf("err = %v, want a refusal", err)
		}
		return nil
	})
}
