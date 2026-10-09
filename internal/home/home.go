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
// The earlier default home — the folder the platform keeps application data in — kept a
// product's records under `products/<product id>/` and its worktrees under
// `worktrees/<product id>/<repository id>/`. A home laid out that way is read
// that way until the migration moves it, so a build deployed over a running
// harness finds its state where it left it; ProductDirectory and
// WorktreeDirectory are the one place that difference is decided.
//
// Which home a process uses — the environment variable, the machine file, then
// these defaults — is Resolve, which run state's ResolveRoot answers with. It is
// here rather than in run state because configuration discovery needs it too: a
// configuration kept outside its repository is found in the project directory
// whose binding names that repository. This package sits below configuration,
// run state, and every store that keeps a product's records.
package home

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// StateHomeVariable is the explicit instruction that moves the home for one
// shell, and it wins over everything else because it is one.
const StateHomeVariable = "YOYODYNE_STATE_HOME"

// MachineFileName is the machine's own settings file. It is kept at
// `~/.yoyodyne/machine.yaml` whatever it says, because it is the file that says
// where the rest of the home is: a setting kept under the directory it moves
// would be found only by already knowing where that was.
const MachineFileName = "machine.yaml"

// DirectoryName is the home's name under the user's home directory.
const DirectoryName = ".yoyodyne"

// The origins a home is reported under, in the order they are consulted, beside
// MachineOrigin for the machine file. `yoyo config show --origins`, `yoyo
// doctor`, and `yoyo project list` print them, so they are named in the
// vocabulary an operator would type to change the value.
const (
	OriginEnvironment = "environment:" + StateHomeVariable
	OriginXDG         = "environment:XDG_STATE_HOME"
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

// EarlierDefault is the platform's earlier default home: the folder the platform
// keeps application data in on macOS and Windows, and `~/.local/state/yoyodyne`
// elsewhere.
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
// every platform, except that where `~/.yoyodyne` does not exist, or holds the
// machine file and nothing else, and the platform's earlier default home does, the earlier one is kept, because that is
// where the state is until `yoyo home migrate` moves it. The new build moves
// nothing on its own. It returns the path and the origin it is reported under.
func Default(getenv func(string) string, userHomeDir func() (string, error), goos string) (string, string, error) {
	fresh, err := DefaultPath(userHomeDir)
	if err != nil {
		return "", "", err
	}
	if !inUse(fresh) {
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

// inUse reports whether `~/.yoyodyne` exists and is more than a directory
// holding the machine file and nothing else. The
// machine file is kept there whichever home is in use, so writing one on a
// machine still running from the earlier default home does not, on its own,
// move every product's records to an empty home.
func inUse(fresh string) bool {
	entries, err := os.ReadDir(fresh)
	if err != nil {
		return !errors.Is(err, os.ErrNotExist)
	}
	return len(entries) != 1 || entries[0].Name() != MachineFileName
}

// Resolved is the home one process resolved and the layer it came from.
type Resolved struct {
	Path string
	// Origin names the layer: one of the Origin constants, or MachineOrigin of
	// the machine file that set it.
	Origin string
}

// MachineOrigin is the origin of a home the machine file set.
func MachineOrigin(machinePath string) string {
	return "machine:" + machinePath
}

// MachinePath is where this machine's settings file is, whether or not it
// exists.
func MachinePath(userHomeDir func() (string, error)) (string, error) {
	fresh, err := DefaultPath(userHomeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(fresh, MachineFileName), nil
}

// machineDocument is the whole of what the machine file may say. It is decoded
// strictly, so a misspelled key is refused rather than leaving the home where it
// was with nothing to say why.
type machineDocument struct {
	StateRoot string `yaml:"state_root"`
}

// machineStateRoot is the home the machine file sets, or nothing where there is
// no file or it sets none.
//
// Until the migration has moved it, a machine whose file is still where earlier
// builds kept it — EarlierMachinePath says where — has that file read in place
// of the absent one, because the home it names is where that machine's
// state is: reading neither would resolve another home, and every command from
// a checkout whose marker names the first would refuse to start. A file in the
// machine home is the one read wherever it exists.
func machineStateRoot(getenv func(string) string, userHomeDir func() (string, error)) (string, string, error) {
	machine, err := MachinePath(userHomeDir)
	if err != nil {
		return "", "", err
	}
	source, err := os.ReadFile(machine)
	if errors.Is(err, os.ErrNotExist) {
		earlier, earlierErr := EarlierMachinePath(getenv, userHomeDir)
		if earlierErr != nil {
			return "", machine, nil
		}
		earlierSource, earlierErr := os.ReadFile(earlier)
		if earlierErr != nil {
			if errors.Is(earlierErr, os.ErrNotExist) {
				return "", machine, nil
			}
			return "", earlier, fmt.Errorf("read the machine configuration %s: %w", earlier, earlierErr)
		}
		machine, source, err = earlier, earlierSource, nil
	}
	if err != nil {
		return "", machine, fmt.Errorf("read the machine configuration %s: %w", machine, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	decoder.KnownFields(true)
	var document machineDocument
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return "", machine, nil
		}
		return "", machine, fmt.Errorf("read the machine configuration %s: %w", machine, err)
	}
	value := strings.TrimSpace(document.StateRoot)
	if value == "" {
		return "", machine, nil
	}
	if !filepath.IsAbs(value) {
		return "", machine, fmt.Errorf("state_root in %s must be an absolute path and is %q", machine, value)
	}
	return filepath.Clean(value), machine, nil
}

// Resolve is the one resolution of the home every process makes:
// YOYODYNE_STATE_HOME, then state_root in `~/.yoyodyne/machine.yaml` (or, until
// the migration moves it, the earlier builds' machine file where that one is
// absent), then
// XDG_STATE_HOME/yoyodyne, then Default. The variable wins because it is an
// explicit instruction for this shell; the machine key is the operator's
// standing answer for the machine; the last two are what a machine nobody
// configured gets.
//
// It resolves and never guards: the checkout's marker, which refuses two homes
// for one repository, is run state's AgreeRoot.
func Resolve(getenv func(string) string, userHomeDir func() (string, error), goos string) (Resolved, error) {
	if value := strings.TrimSpace(getenv(StateHomeVariable)); value != "" {
		if !filepath.IsAbs(value) {
			return Resolved{}, errors.New("YOYODYNE_STATE_HOME must be an absolute path")
		}
		return Resolved{Path: filepath.Clean(value), Origin: OriginEnvironment}, nil
	}
	machine, machinePath, err := machineStateRoot(getenv, userHomeDir)
	if err != nil {
		return Resolved{}, err
	}
	if machine != "" {
		return Resolved{Path: machine, Origin: MachineOrigin(machinePath)}, nil
	}
	if value := strings.TrimSpace(getenv("XDG_STATE_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			return Resolved{}, errors.New("XDG_STATE_HOME must be an absolute path")
		}
		return Resolved{Path: filepath.Join(filepath.Clean(value), "yoyodyne"), Origin: OriginXDG}, nil
	}
	path, origin, err := Default(getenv, userHomeDir, goos)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Path: path, Origin: origin}, nil
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

// ProductDirectories is every product's records directory under a home, in the
// layout the home has: what ProductDirectory resolves for each product the
// home keeps records for.
func ProductDirectories(root string) []string {
	pattern := filepath.Join(filepath.Clean(root), ProjectsDirectoryName, "*", StateDirectoryName)
	if EarlierLayout(root) {
		pattern = filepath.Join(filepath.Clean(root), earlierProductsDirectoryName, "*")
	}
	directories, _ := filepath.Glob(pattern)
	return directories
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
