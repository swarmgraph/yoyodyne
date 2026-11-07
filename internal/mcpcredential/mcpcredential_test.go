package mcpcredential

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const product = domain.ProductID("acme")

func fileServer() config.MCPServer {
	return config.MCPServer{URL: "https://mcp.example.com/", Credential: config.MCPCredentialFile, Tools: map[string]config.MCPToolClass{"list": config.MCPRead}}
}

func writeCredential(t *testing.T, path, value string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// A file credential is read from the one path the server's name derives, and
// from nowhere else: not a file elsewhere holding a credential, not a link at
// the derived path pointing at one, not a file other users can read, and not a
// path a server name could climb out of.
func TestAFileCredentialIsReadOnlyFromThePathTheServerNameDerives(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stores := Stores{StateRoot: root, Product: product}
	derived := File(root, product, "tickets")
	if want := filepath.Join(Directory(root, product), "tickets.token"); derived != want {
		t.Fatalf("File() = %s, want %s", derived, want)
	}

	// Nothing at the derived path: a credential stored anywhere else is not read.
	elsewhere := filepath.Join(root, "elsewhere", "tickets.token")
	writeCredential(t, elsewhere, "secret-elsewhere\n", 0o600)
	if _, err := stores.Read(context.Background(), "tickets", fileServer()); !errors.Is(err, ErrMissing) {
		t.Fatalf("Read() with no file at the derived path = %v, want ErrMissing", err)
	}

	// A link at the derived path pointing at that file is refused, not followed.
	if err := os.MkdirAll(filepath.Dir(derived), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, derived); err != nil {
		t.Fatal(err)
	}
	if value, err := stores.Read(context.Background(), "tickets", fileServer()); !errors.Is(err, ErrMissing) || value != "" {
		t.Fatalf("Read() through a link = %q, %v; want it refused", value, err)
	}
	if err := os.Remove(derived); err != nil {
		t.Fatal(err)
	}

	// A file other users can read is refused, naming the chmod that fixes it.
	writeCredential(t, derived, "secret-derived\n", 0o644)
	if _, err := stores.Read(context.Background(), "tickets", fileServer()); !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("Read() of a file others can read = %v, want it refused naming chmod 600", err)
	}

	// The file at the derived path, readable by its owner alone, is the credential.
	writeCredential(t, derived, "secret-derived\n", 0o600)
	value, err := stores.Read(context.Background(), "tickets", fileServer())
	if err != nil || value != "secret-derived" {
		t.Fatalf("Read() = %q, %v; want the derived file's credential", value, err)
	}

	// A server name that could carry a separator derives nothing.
	for _, name := range []string{"../elsewhere/tickets", "tickets/../../x", ""} {
		if _, err := stores.Read(context.Background(), name, fileServer()); !errors.Is(err, ErrMissing) {
			t.Errorf("Read(%q) = %v, want it refused", name, err)
		}
	}
}

// Every error a read returns names the server and the store and never the value.
func TestACredentialReadNeverPutsTheValueInAnError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stores := Stores{StateRoot: root, Product: product}
	derived := File(root, product, "tickets")
	writeCredential(t, derived, "line-one-secret\nline-two-secret\n", 0o600)
	_, err := stores.Read(context.Background(), "tickets", fileServer())
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("Read() of a two-line file = %v, want it refused without the value", err)
	}
}

type keychain map[string]string

func (k keychain) Secret(_ context.Context, name string) (string, error) {
	if value, ok := k[name]; ok {
		return value, nil
	}
	return "", errors.New("not found")
}

// A keychain credential is read from the item the product and server derive.
func TestAKeychainCredentialIsReadFromTheDerivedItem(t *testing.T) {
	t.Parallel()
	server := config.MCPServer{URL: "https://mcp.example.com/", Credential: config.MCPCredentialKeychain}
	stores := Stores{Keychain: keychain{"yoyo-mcp.acme.tickets": "kept\n", "yoyo-mcp.other.tickets": "not-ours"}, Product: product}
	if value, err := stores.Read(context.Background(), "tickets", server); err != nil || value != "kept" {
		t.Fatalf("Read() = %q, %v; want the derived item's credential", value, err)
	}
	if _, err := stores.Read(context.Background(), "mail", server); !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), "yoyo-mcp.acme.mail") {
		t.Fatalf("Read() of an item not stored = %v, want ErrMissing naming the item", err)
	}
	if _, err := (Stores{Product: product}).Read(context.Background(), "tickets", server); !errors.Is(err, ErrMissing) {
		t.Fatalf("Read() with no keychain = %v, want ErrMissing", err)
	}
}

// A remote server's redirect to another host is not followed, so the
// credential a request carries never reaches it; a redirect on the same host is
// followed with the credential, up to the bound.
func TestACredentialIsSentToNoOtherHostOnARedirect(t *testing.T) {
	t.Parallel()
	var stranger atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stranger.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	var sawCredential atomic.Int32
	hops := 0
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer kept" {
			sawCredential.Add(1)
		}
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, other.URL+"/landed", http.StatusTemporaryRedirect)
		case "/near":
			http.Redirect(w, r, "/landed", http.StatusTemporaryRedirect)
		case "/loop":
			hops++
			http.Redirect(w, r, "/loop", http.StatusTemporaryRedirect)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer origin.Close()

	client := origin.Client()
	client.CheckRedirect = CheckRedirect
	ask := func(path string) (*http.Response, error) {
		request, err := http.NewRequest(http.MethodPost, origin.URL+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer kept")
		return client.Do(request)
	}

	if response, err := ask("/away"); err == nil || !errors.Is(err, ErrRedirectRefused) {
		if response != nil {
			response.Body.Close()
		}
		t.Fatalf("a redirect to another host = %v, want ErrRedirectRefused", err)
	}
	if got := stranger.Load(); got != 0 {
		t.Fatalf("the other host was sent %d requests, want none", got)
	}

	before := sawCredential.Load()
	response, err := ask("/near")
	if err != nil {
		t.Fatalf("a redirect on the same host = %v, want it followed", err)
	}
	response.Body.Close()
	if got := sawCredential.Load() - before; got != 2 {
		t.Fatalf("the same host saw the credential %d times across the redirect, want 2", got)
	}

	if response, err := ask("/loop"); !errors.Is(err, ErrRedirectRefused) {
		if response != nil {
			response.Body.Close()
		}
		t.Fatalf("an endless redirect = %v, want ErrRedirectRefused", err)
	}
	if hops != config.MCPMaxRedirects+1 {
		t.Fatalf("the server was asked %d times on the loop, want %d", hops, config.MCPMaxRedirects+1)
	}
}

// The remedy for a missing credential is the declared login where there is
// one, and otherwise the command that stores it.
func TestTheRemedyIsTheLoginOrTheStoreCommand(t *testing.T) {
	t.Parallel()
	root := "/state"
	withLogin := config.MCPServer{Command: []string{"mail-mcp"}, Login: []string{"mail-mcp", "login"}, Credential: config.MCPCredentialKeychain, CredentialEnv: "MAIL_TOKEN"}
	if got := Remedy(root, product, "mail", withLogin); got != "yoyo mcp login mail" {
		t.Errorf("Remedy() with a login = %q", got)
	}
	keychainOnly := config.MCPServer{URL: "https://x.example/", Credential: config.MCPCredentialKeychain}
	if got := Remedy(root, product, "tickets", keychainOnly); got != "security add-generic-password -s yoyo-mcp.acme.tickets -a yoyo -w" {
		t.Errorf("Remedy() for the keychain = %q", got)
	}
	if got := Remedy(root, product, "tickets", fileServer()); !strings.Contains(got, File(root, product, "tickets")) || !strings.Contains(got, "umask 077") {
		t.Errorf("Remedy() for a file = %q", got)
	}
}
