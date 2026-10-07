package dashboard

// Where a supplied bearer token lives, named once, and read from there.
//
// Under the `generated` default the token is made at each start and printed
// once, and nothing here is consulted. A token the operator supplied — because
// the dashboard is bound outside loopback, where a token printed to one terminal
// is unusable from the other device the opt-in exists for, or simply because he
// does not want to paste a fresh one after every restart — comes from a store
// that is not the committed configuration. The two stores are the ones the
// Slack tokens already use, named for the product for the reason theirs are: a
// machine running several harnesses has several dashboards, and a token under a
// generic name is right for at most one of them.
//
// The names, the commands that store a token under them, and the reading are
// all here so that the diagnosis that asks whether the token is stored and the
// command that reads it look in one place and hand the operator one remedy:
// what `yoyo doctor` prints under `service:dashboard` is what `yoyo dashboard`
// prints when it refuses to start for want of the token, by construction.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// TokenAccount is the keychain account the token is stored under: the same one
// the Slack pair uses, so an operator who has stored those knows the shape.
const TokenAccount = slack.KeychainAccount

// TokenSecret names this product's dashboard token in the macOS keychain, under
// the same account the Slack pair is stored under.
func TokenSecret(productID domain.ProductID) string {
	return "yoyo-dashboard." + string(productID)
}

// TokenFileName is the file a token is kept in under the product's state
// directory, on a machine with no keychain or by an operator's choice.
const TokenFileName = "dashboard.token"

// TokenFile is where the file-sourced token lives: under the state root, in
// the product's own directory beside its runs and conversations, so it is
// outside the repository and belongs to this machine like everything else
// there.
func TokenFile(stateRoot string, productID domain.ProductID) string {
	return filepath.Join(home.ProductDirectory(stateRoot, string(productID)), TokenFileName)
}

// KeychainStoreCommand is the command that stores the keychain token. `-w`
// with no value makes the keychain prompt for it, so the token never reaches a
// shell history.
func KeychainStoreCommand(productID domain.ProductID) string {
	return fmt.Sprintf("security add-generic-password -s %s -a %s -w", TokenSecret(productID), TokenAccount)
}

// FileStoreCommand is the command that makes the token file where there is
// none: its directory, and then a fresh token in it readable by nobody else.
func FileStoreCommand(file string) string {
	return fmt.Sprintf("mkdir -p %s && %s", shellQuote(filepath.Dir(file)), FileRewriteCommand(file))
}

// FileRewriteCommand writes a fresh token over a file that is there and holds
// nothing usable.
func FileRewriteCommand(file string) string {
	return fmt.Sprintf("(umask 077 && openssl rand -hex 32 > %s)", shellQuote(file))
}

// SecretReader produces one keychain item by name. slack.Keychain is the one
// the harness has; a test supplies its own, because the keychain is the
// machine's and a test must not read it.
type SecretReader interface {
	Secret(ctx context.Context, name string) (string, error)
}

// TokenStores is where a supplied token is read from: the keychain, where the
// platform has one, and the file under the state root.
type TokenStores struct {
	// Keychain reads an item out of the macOS keychain. It is nil on a platform
	// that has none, and the keychain source is refused there by name.
	Keychain SecretReader
	// Platform is the operating system, named in that refusal.
	Platform string
	// StateRoot is the root the token file lives under.
	StateRoot string
}

// SuppliedToken is a token read from a store, beside where it was read from in
// words that never carry the value: Origin is what the command prints in the
// token's place.
type SuppliedToken struct {
	Value  string
	Origin string
}

// ErrTokenUnavailable is a supplied token the store would not produce: never
// stored, stored empty, or a store that could not be asked. The error it wraps
// carries the command that stores the token, which is the doctor's remedy.
var ErrTokenUnavailable = errors.New("the dashboard's token could not be read")

// Read produces the token the entry names from the store it names. The
// `generated` source names no store and is refused here: the caller generates
// that one, and asking this for it would be a caller that misread the entry.
func (s TokenStores) Read(ctx context.Context, source config.DashboardTokenSource, productID domain.ProductID) (SuppliedToken, error) {
	switch source {
	case config.DashboardTokenKeychain:
		return s.readKeychain(ctx, productID)
	case config.DashboardTokenFile:
		return s.readFile(productID)
	}
	return SuppliedToken{}, fmt.Errorf("%w: services.dashboard.token %q names no store to read it from", ErrTokenUnavailable, source)
}

func (s TokenStores) readKeychain(ctx context.Context, productID domain.ProductID) (SuppliedToken, error) {
	secret := TokenSecret(productID)
	if s.Keychain == nil {
		return SuppliedToken{}, fmt.Errorf("%w: services.dashboard.token is %q and this platform (%s) has no keychain; a file under the state root is the store this platform has, so set services.dashboard.token to %q and store the token with: %s",
			ErrTokenUnavailable, config.DashboardTokenKeychain, s.Platform, config.DashboardTokenFile, FileStoreCommand(TokenFile(s.StateRoot, productID)))
	}
	value, err := s.Keychain.Secret(ctx, secret)
	if err != nil {
		return SuppliedToken{}, fmt.Errorf("%w: the keychain item %s under the account %s is not there or could not be read (%v); store it with: %s",
			ErrTokenUnavailable, secret, TokenAccount, err, KeychainStoreCommand(productID))
	}
	token, err := oneLine(value)
	if err != nil {
		return SuppliedToken{}, fmt.Errorf("%w: the keychain item %s under the account %s %v; store a usable one with: %s",
			ErrTokenUnavailable, secret, TokenAccount, err, KeychainStoreCommand(productID))
	}
	return SuppliedToken{
		Value:  token,
		Origin: fmt.Sprintf("the keychain item %s under the account %s", secret, TokenAccount),
	}, nil
}

func (s TokenStores) readFile(productID domain.ProductID) (SuppliedToken, error) {
	file := TokenFile(s.StateRoot, productID)
	contents, err := os.ReadFile(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return SuppliedToken{}, fmt.Errorf("%w: the file %s is not there; store it with: %s", ErrTokenUnavailable, file, FileStoreCommand(file))
	case err != nil:
		return SuppliedToken{}, fmt.Errorf("%w: the file %s could not be read: %v", ErrTokenUnavailable, file, err)
	}
	token, err := oneLine(string(contents))
	if err != nil {
		return SuppliedToken{}, fmt.Errorf("%w: the file %s %v; write a usable one with: %s", ErrTokenUnavailable, file, err, FileRewriteCommand(file))
	}
	return SuppliedToken{Value: token, Origin: "the file " + file}, nil
}

// oneLine is the stored value as a token: trimmed of the newline a store leaves
// on the end, and refused when nothing is left or when what is left could not
// be sent as a header value at all.
func oneLine(value string) (string, error) {
	token := strings.TrimSpace(value)
	if token == "" {
		return "", errors.New("holds no value")
	}
	for _, r := range token {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("holds more than one line, and a token is one line sent as a header")
		}
	}
	return token, nil
}

// shellQuote makes a path safe to paste into a remedy. Remedies are commands, so
// a path with a space in it has to survive being one.
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n\"'\\$`*?[]{}()|&;<>#~!") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
