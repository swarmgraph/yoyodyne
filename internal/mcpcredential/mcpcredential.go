// Package mcpcredential is where an MCP server's credential and its own login
// live on this machine, named once and read from there
// (docs/designs/mcp-tools-through-the-harness.md, section 1).
//
// A project names the store a server's credential is kept in — the keychain or
// a file — and never a path. Every name a store uses is derived here from the
// product and the server's name alone, so no line of configuration can point
// the harness at another file on the machine and have it sent to a server. The
// diagnosis that says a credential is missing and the code that reads it look
// in the same place and name the same remedy, by construction, as the
// dashboard's token does.
//
// Nothing here speaks MCP; internal/mcpcall is the only package that will. What
// this package also holds is the one rule about where a credential may travel:
// a remote server's redirects stay on its own host, so a credential is never
// sent anywhere else.
package mcpcredential

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// KeychainAccount is the keychain account every credential is stored under,
// the one the Slack pair and the dashboard token use.
const KeychainAccount = "yoyo"

// DirectoryName is the directory under the product's state directory that
// holds every server's credential file and every local server's own folder.
const DirectoryName = "mcp"

// KeychainItem names one server's credential in the macOS keychain. It carries
// the product, so a sibling project's server of the same name cannot stand in
// for it.
func KeychainItem(productID domain.ProductID, server string) string {
	return "yoyo-mcp." + string(productID) + "." + server
}

// Directory is the product's MCP directory under a state root.
func Directory(stateRoot string, productID domain.ProductID) string {
	return filepath.Join(home.ProductDirectory(stateRoot, string(productID)), DirectoryName)
}

// File is where one server's file-stored credential lives: `<server>.token` in
// the product's MCP directory. It is the only path a file credential is ever
// read from.
func File(stateRoot string, productID domain.ProductID, server string) string {
	return filepath.Join(Directory(stateRoot, productID), server+".token")
}

// ServerDirectoryWithin is a local server's own folder, relative to the state
// root: its working directory and its home, so its login files are kept there
// and nowhere an agent can read them.
func ServerDirectoryWithin(stateRoot string, productID domain.ProductID, server string) string {
	return filepath.ToSlash(filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(stateRoot, string(productID))), DirectoryName, server))
}

// ServerDirectory is that folder as an absolute path.
func ServerDirectory(stateRoot string, productID domain.ProductID, server string) string {
	return filepath.Join(Directory(stateRoot, productID), server)
}

// KeychainStoreCommand stores one server's credential in the keychain. `-w`
// with no value makes the keychain prompt for it, so the credential never
// reaches a shell history.
func KeychainStoreCommand(productID domain.ProductID, server string) string {
	return fmt.Sprintf("security add-generic-password -s %s -a %s -w", KeychainItem(productID, server), KeychainAccount)
}

// FileStoreCommand writes one server's credential file, readable by nobody but
// the user who runs it: the credential is pasted at the terminal and ended with
// Control-D, so it never reaches a shell history either.
func FileStoreCommand(file string) string {
	return fmt.Sprintf("mkdir -p %s && (umask 077 && cat > %s)", shellQuote(filepath.Dir(file)), shellQuote(file))
}

// LoginCommand is the command that logs a server in on this machine.
func LoginCommand(server string) string {
	return "yoyo mcp login " + server
}

// Remedy is the one command to run for a server whose credential is not
// stored: its declared login where it has one, and otherwise the command that
// stores the credential in the store it names.
func Remedy(stateRoot string, productID domain.ProductID, server string, declared config.MCPServer) string {
	if len(declared.Login) > 0 {
		return LoginCommand(server)
	}
	if declared.Credential == config.MCPCredentialFile {
		return FileStoreCommand(File(stateRoot, productID, server))
	}
	return KeychainStoreCommand(productID, server)
}

// SecretReader produces one keychain item by name. slack.Keychain is the one
// the harness has; a test supplies its own, because the keychain is the
// machine's and a test must not read it.
type SecretReader interface {
	Secret(ctx context.Context, name string) (string, error)
}

// Stores is where a credential is read from.
type Stores struct {
	// Keychain reads an item out of the macOS keychain. It is nil on a platform
	// that has none, and the keychain store is refused there by name.
	Keychain SecretReader
	// StateRoot is the root the credential files live under.
	StateRoot string
	// Product is the product the servers are declared for.
	Product domain.ProductID
}

// ErrMissing is a credential the store would not produce: never stored, stored
// empty, or kept somewhere the harness will not read it from. The error that
// wraps it names the server and the store and never the value.
var ErrMissing = errors.New("the MCP server's credential is not stored")

// maxCredentialBytes bounds a credential file, so a file that is not one is
// refused rather than read whole.
const maxCredentialBytes = 64 << 10

// Read produces the credential the server's declaration names, from the store
// it names, under the name derived from the server's name. A server naming no
// store has no credential, and asking for one is refused.
func (s Stores) Read(ctx context.Context, server string, declared config.MCPServer) (string, error) {
	if err := domain.ValidateIdentifier("mcp server name", server); err != nil {
		return "", fmt.Errorf("%w: %v", ErrMissing, err)
	}
	switch declared.Credential {
	case config.MCPCredentialKeychain:
		return s.readKeychain(ctx, server)
	case config.MCPCredentialFile:
		return s.readFile(server)
	}
	return "", fmt.Errorf("%w: mcp server %q names no credential store", ErrMissing, server)
}

func (s Stores) readKeychain(ctx context.Context, server string) (string, error) {
	item := KeychainItem(s.Product, server)
	if s.Keychain == nil {
		return "", fmt.Errorf("%w: mcp server %q keeps its credential in the keychain, which this platform has not got; name the file store instead", ErrMissing, server)
	}
	value, err := s.Keychain.Secret(ctx, item)
	if err != nil {
		return "", fmt.Errorf("%w: the keychain item %s under the account %s for mcp server %q is not there or could not be read", ErrMissing, item, KeychainAccount, server)
	}
	return oneLine(server, value)
}

// readFile reads the one file a server's credential may come from. The file is
// opened without following a final symbolic link, and the directory holding it
// must be a directory rather than a link, so a link planted at the derived path
// cannot turn the read into a read of some other file on the machine. A file
// any other user may read is refused, because the design keeps it readable by
// the user the harness runs as alone.
func (s Stores) readFile(server string) (string, error) {
	directory := Directory(s.StateRoot, s.Product)
	file := File(s.StateRoot, s.Product, server)
	info, err := os.Lstat(directory)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("%w: the file %s for mcp server %q is not there", ErrMissing, file, server)
	case err != nil:
		return "", fmt.Errorf("%w: the directory %s could not be read: %v", ErrMissing, directory, err)
	case !info.IsDir():
		return "", fmt.Errorf("%w: %s is not a directory, and a credential is read only from the file the server's name derives", ErrMissing, directory)
	}
	handle, err := os.OpenFile(file, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("%w: the file %s for mcp server %q is not there", ErrMissing, file, server)
	case err != nil:
		return "", fmt.Errorf("%w: the file %s for mcp server %q could not be opened as a plain file: %v", ErrMissing, file, server, err)
	}
	defer handle.Close()
	stat, err := handle.Stat()
	switch {
	case err != nil:
		return "", fmt.Errorf("%w: the file %s could not be read: %v", ErrMissing, file, err)
	case !stat.Mode().IsRegular():
		return "", fmt.Errorf("%w: %s is not a plain file", ErrMissing, file)
	case stat.Mode().Perm()&0o077 != 0:
		return "", fmt.Errorf("%w: %s is mode %04o, readable by other users on this machine; make it readable by its owner alone with: chmod 600 %s", ErrMissing, file, stat.Mode().Perm(), shellQuote(file))
	}
	contents, err := io.ReadAll(io.LimitReader(handle, maxCredentialBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: the file %s could not be read: %v", ErrMissing, file, err)
	}
	if len(contents) > maxCredentialBytes {
		return "", fmt.Errorf("%w: %s holds more than %d bytes, which is not a credential", ErrMissing, file, maxCredentialBytes)
	}
	return oneLine(server, string(contents))
}

// oneLine is the stored value as a credential: trimmed of the newline a store
// leaves on the end, and refused where nothing is left or where what is left
// could not be sent as a header value.
func oneLine(server, value string) (string, error) {
	credential := strings.TrimSpace(value)
	if credential == "" {
		return "", fmt.Errorf("%w: the credential stored for mcp server %q is empty", ErrMissing, server)
	}
	for _, r := range credential {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: the credential stored for mcp server %q holds more than one line", ErrMissing, server)
		}
	}
	return credential, nil
}

// ErrRedirectRefused is a redirect a remote server's request does not follow:
// to another host, off https, or past the bound.
var ErrRedirectRefused = errors.New("the MCP server's redirect is not followed")

// CheckRedirect is the redirect rule for every request to a remote server, for
// an http.Client's CheckRedirect. A redirect is followed only to the same host
// and port over https, at most config.MCPMaxRedirects of them; anything else
// stops the request where it is, before a byte is sent to the new address, so
// a credential is never sent to any other host.
func CheckRedirect(request *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	origin := via[0].URL
	switch {
	case len(via) > config.MCPMaxRedirects:
		return fmt.Errorf("%w: more than %d redirects from %s", ErrRedirectRefused, config.MCPMaxRedirects, origin.Host)
	case request.URL.Scheme != "https":
		return fmt.Errorf("%w: %s redirected off https", ErrRedirectRefused, origin.Host)
	case !strings.EqualFold(request.URL.Host, origin.Host):
		return fmt.Errorf("%w: %s redirected to another host, %s", ErrRedirectRefused, origin.Host, request.URL.Host)
	}
	return nil
}

// shellQuote makes a path safe to paste into a remedy.
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n\"'\\$`*?[]{}()|&;<>#~!") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
