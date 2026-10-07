// Package home is the machine home: the one directory outside every repository
// where the harness keeps what it keeps on this machine, and what lies under it.
// docs/designs/machine-home.md is the design it builds.
//
// The home is `~/.yoyodyne` on every platform unless something moved it, and it
// holds one directory per project, named by the project's product id, with what
// no single project owns at the top:
//
//	~/.yoyodyne/
//	  accounts/                provider accounts, which serve every project
//	  operator-hold.json       the operator's hold, one for the whole machine
//	  logs/                    the harness's own logs
//	  projects/<product id>/
//	    config.yaml            the configuration, where the repository does not carry it
//	    repository.json        the binding: which repository this id is
//	    state/                 every record one product keeps
//	    worktrees/             the developer worktrees of the bound repository
//	    intent/                the companion intent repository, where one is used
//
// The earlier default home — the platform's application-data folder — kept a
// product's records under `products/<product id>/` and its worktrees under
// `worktrees/<product id>/<repository id>/`. A home laid out that way is read
// that way until the migration moves it, so a build deployed over a running
// harness finds its state where it left it; ProductDirectory and
// WorktreeDirectory are the one place that difference is decided.
//
// Which home a process uses — the environment variable, the machine file, then
// these defaults — is run state's ResolveRoot, which calls Default for the last
// layer. This package says what the defaults are and what lies under a home; it
// sits below run state and every store that keeps a product's records, because
// all of them need it.
package home

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DirectoryName is the home's name under the user's home directory.
const DirectoryName = ".yoyodyne"

// The origins a default home is reported under. `yoyo config show --origins`
// and `yoyo doctor` print them beside the origins run state names for the
// variable and the machine file.
const (
	// OriginDefault is `~/.yoyodyne`, which is what a machine nobody configured
	// gets.
	OriginDefault = "default"
	// OriginEarlierDefault is the platform's earlier default home, still in use
	// because it exists and `~/.yoyodyne` does not: the state is where the
	// earlier build left it and nothing has moved it yet.
	OriginEarlierDefault = "earlier-default"
)

// DefaultPath is `~/.yoyodyne`, whether or not it exists.
func DefaultPath(userHomeDir func() (string, error)) (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, DirectoryName), nil
}

// EarlierDefault is the platform's earlier default home: the application-data
// folder on macOS and Windows, and `~/.local/state/yoyodyne` elsewhere.
func EarlierDefault(getenv func(string) string, userHomeDir func() (string, error), goos string) (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Yoyodyne", "state"), nil
	case "windows":
		if localAppData := strings.TrimSpace(getenv("LOCALAPPDATA")); localAppData != "" {
			if !filepath.IsAbs(localAppData) {
				return "", errors.New("LOCALAPPDATA must be an absolute path")
			}
			return filepath.Join(localAppData, "Yoyodyne", "state"), nil
		}
		return filepath.Join(home, "AppData", "Local", "Yoyodyne", "state"), nil
	default:
		return filepath.Join(home, ".local", "state", "yoyodyne"), nil
	}
}

// Default is the home a machine gets when nothing set one: `~/.yoyodyne` on
// every platform, except that where `~/.yoyodyne` does not exist and the
// platform's earlier default home does, the earlier one is kept, because that is
// where the state is until `yoyo home migrate` moves it. The new build moves
// nothing on its own. It returns the path and the origin it is reported under.
func Default(getenv func(string) string, userHomeDir func() (string, error), goos string) (string, string, error) {
	fresh, err := DefaultPath(userHomeDir)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(fresh); errors.Is(err, os.ErrNotExist) {
		earlier, err := EarlierDefault(getenv, userHomeDir, goos)
		if err != nil {
			return "", "", err
		}
		if info, err := os.Stat(earlier); err == nil && info.IsDir() {
			return earlier, OriginEarlierDefault, nil
		}
	}
	return fresh, OriginDefault, nil
}

// ProjectsDirectoryName holds one directory per project.
const ProjectsDirectoryName = "projects"

// The machine-wide records at the top of a home.
const (
	AccountsDirectoryName = "accounts"
	OperatorHoldFileName  = "operator-hold.json"
	LogsDirectoryName     = "logs"
)

// Names inside a project directory.
const (
	ConfigFileName         = "config.yaml"
	BindingFileName        = "repository.json"
	StateDirectoryName     = "state"
	WorktreesDirectoryName = "worktrees"
	IntentDirectoryName    = "intent"
)

// earlierProductsDirectoryName is where the earlier layout kept each product's
// records, and earlierWorktreesDirectoryName its worktrees.
const (
	earlierProductsDirectoryName  = "products"
	earlierWorktreesDirectoryName = "worktrees"
)

// EarlierLayout reports whether a home keeps its records the way the earlier
// builds did, under `products/<product id>/`. It is read off the home itself
// rather than off how the home was found, so a home that YOYODYNE_STATE_HOME
// points at where the earlier builds kept their state defers the migration
// exactly as the earlier default does.
func EarlierLayout(root string) bool {
	info, err := os.Stat(filepath.Join(root, earlierProductsDirectoryName))
	return err == nil && info.IsDir()
}

// ProjectDirectory is one project's directory under a home.
func ProjectDirectory(root, productID string) string {
	return filepath.Join(filepath.Clean(root), ProjectsDirectoryName, productID)
}

// ProductDirectory is where one product's records are kept under a home:
// `projects/<id>/state/`, or `products/<id>/` in a home laid out the earlier
// way. Every store that keeps a product's records resolves its directory here.
func ProductDirectory(root, productID string) string {
	return filepath.Join(filepath.Clean(root), filepath.FromSlash(ProductDirectoryWithin(root, productID)))
}

// ProductDirectoryWithin is ProductDirectory relative to the home, in slash
// form, for a caller writing through the confined-write primitive rooted there.
func ProductDirectoryWithin(root, productID string) string {
	if EarlierLayout(root) {
		return path.Join(earlierProductsDirectoryName, productID)
	}
	return path.Join(ProjectsDirectoryName, productID, StateDirectoryName)
}

// WorktreeDirectory is where one product's developer worktrees are cut under a
// home when the configuration leaves the worktree root to the harness:
// `projects/<id>/worktrees/`, or `worktrees/<id>/<repository id>/` in a home
// laid out the earlier way, where the preserved worktrees of runs in flight are.
func WorktreeDirectory(root, productID, repositoryID string) string {
	if EarlierLayout(root) {
		return filepath.Join(filepath.Clean(root), earlierWorktreesDirectoryName, productID, repositoryID)
	}
	return filepath.Join(ProjectDirectory(root, productID), WorktreesDirectoryName)
}
