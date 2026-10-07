// Package home is the machine home: the one directory outside every repository
// where the harness keeps what it keeps on this machine, and what lies under it.
//
// The home is `~/.yoyodyne` on every platform unless something moved it, and it
// holds one directory per project, named by the project's product id, with the
// machine-wide records at the top:
//
//	~/.yoyodyne/
//	  machine.yaml             the machine's own settings; state_root moves the rest
//	  accounts/                provider accounts, which serve every project
//	  operator-hold.json       the operator's hold, one for the whole machine
//	  logs/                    the harness's own logs
//	  projects/<product id>/
//	    config.yaml            the configuration, where the repository does not carry it
//	    repository.json        the binding: which repository this id is
//	    state/                 every record one product keeps
//	    worktrees/             the developer worktrees of the bound repository
//
// The earlier default home — the platform's application-data folder — kept a
// product's records under `products/<product id>/` and its worktrees under
// `worktrees/<product id>/`. A root laid out that way is read that way until
// the migration moves it, so a build deployed over a running harness finds its
// state where it left it; ProductDirectory and WorktreeDirectory are the one
// place that difference is decided.
//
// It is a package of its own, below both configuration and run state, because
// both need it: configuration discovery finds a project's configuration by the
// binding, and every run-state store keeps its records in the project's
// directory.
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

// DirectoryName is the home's name under the user's home directory.
const DirectoryName = ".yoyodyne"

// MachineFileName is the machine's own settings file. It lives in the default
// home, `~/.yoyodyne/machine.yaml`, whatever it says, because it is the file
// that says where the rest of the home is: a setting kept under the directory
// it moves would be found only by already knowing where that was.
const MachineFileName = "machine.yaml"

// The origins a home is reported under, in the order they are consulted.
// `yoyo config show --origins`, `yoyo doctor`, and `yoyo home` print them, so
// they are named in the vocabulary an operator would type to change the value.
const (
	OriginEnvironment = "environment:" + StateHomeVariable
	originMachine     = "machine:"
	OriginXDG         = "environment:XDG_STATE_HOME"
	// OriginDefault is `~/.yoyodyne`, which is what a machine nobody configured
	// gets.
	OriginDefault = "default"
	// OriginEarlierDefault is the platform's earlier default home, still in use
	// because it exists and `~/.yoyodyne` does not: the state is where the
	// earlier build left it and nothing has moved it yet.
	OriginEarlierDefault = "earlier-default"
)

// Resolved is the home one process resolved and the layer it came from.
type Resolved struct {
	Path string
	// Origin names the layer: one of the Origin constants, or "machine:"
	// followed by the machine file that set it.
	Origin string
}

// Earlier reports whether this is the platform's earlier default home, kept in
// use until the migration moves it.
func (r Resolved) Earlier() bool {
	return r.Origin == OriginEarlierDefault
}

// MachineOrigin is the origin of a home the machine file set.
func MachineOrigin(machinePath string) string {
	return originMachine + machinePath
}

// machineDocument is the whole of what the machine file may say. It is decoded
// strictly, so a misspelled key is refused rather than leaving the home where it
// was with nothing to say why.
type machineDocument struct {
	StateRoot string `yaml:"state_root"`
}

// DefaultPath is `~/.yoyodyne`, whether or not it exists.
func DefaultPath(userHomeDir func() (string, error)) (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, DirectoryName), nil
}

// MachinePath is where this machine's settings file is, whether or not it
// exists.
func MachinePath(userHomeDir func() (string, error)) (string, error) {
	home, err := DefaultPath(userHomeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, MachineFileName), nil
}

// EarlierConfigurationsHome is the directory the earlier builds kept this
// machine's settings file and the configurations of projects kept outside their
// repositories in. It is no longer read for either; it is named so that a file
// left there is reported rather than silently ignored, and so the migration
// knows where to move them from.
func EarlierConfigurationsHome(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	if value := strings.TrimSpace(getenv("YOYODYNE_CONFIG_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			return "", fmt.Errorf("YOYODYNE_CONFIG_HOME must be an absolute path and is %q", value)
		}
		return filepath.Clean(value), nil
	}
	if value := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
		}
		return filepath.Join(filepath.Clean(value), "yoyodyne"), nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, ".config", "yoyodyne"), nil
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

// machineStateRoot is the home the machine file sets, or nothing where there is
// no file or it sets none.
func machineStateRoot(getenv func(string) string, userHomeDir func() (string, error)) (string, string, error) {
	machine, err := MachinePath(userHomeDir)
	if err != nil {
		return "", "", err
	}
	source, err := os.ReadFile(machine)
	if errors.Is(err, os.ErrNotExist) {
		if err := refuseEarlierMachineFile(getenv, userHomeDir, machine); err != nil {
			return "", machine, err
		}
		return "", machine, nil
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

// refuseEarlierMachineFile refuses a machine file left where the earlier builds
// kept it while none is where this build reads it. Reading it anyway would keep
// the earlier home alive by another name; ignoring it would move the state root
// under the operator without a word, which the two-roots guard would then refuse
// in terms that do not say why. So it is only looked for, and the refusal names
// the one command that puts it where it is read.
func refuseEarlierMachineFile(getenv func(string) string, userHomeDir func() (string, error), machine string) error {
	earlierHome, err := EarlierConfigurationsHome(getenv, userHomeDir)
	if err != nil {
		return nil
	}
	earlier := filepath.Join(earlierHome, MachineFileName)
	if _, err := os.Stat(earlier); err != nil {
		return nil
	}
	return fmt.Errorf("this machine's settings file is at %s, where earlier builds kept it, and this build reads it only at %s; "+
		"move it there with `mkdir -p %s && mv %s %s`",
		earlier, machine, shellQuote(filepath.Dir(machine)), shellQuote(earlier), shellQuote(machine))
}

// Resolve is the one resolution of the home every process makes:
// YOYODYNE_STATE_HOME, then state_root in the machine file, then
// XDG_STATE_HOME/yoyodyne, then `~/.yoyodyne` — except that where `~/.yoyodyne`
// does not exist and the platform's earlier default home does, the earlier one
// is kept, because that is where the state is until the migration moves it. The
// variable wins because it is an explicit instruction for this shell; the
// machine key is the operator's standing answer for the machine; the last two
// are what a machine nobody configured gets.
//
// It resolves and never guards: the checkout's marker, which refuses two homes
// for one repository, is run state's to agree.
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
	fresh, err := DefaultPath(userHomeDir)
	if err != nil {
		return Resolved{}, err
	}
	if _, err := os.Stat(fresh); errors.Is(err, os.ErrNotExist) {
		earlier, err := EarlierDefault(getenv, userHomeDir, goos)
		if err != nil {
			return Resolved{}, err
		}
		if info, err := os.Stat(earlier); err == nil && info.IsDir() {
			return Resolved{Path: earlier, Origin: OriginEarlierDefault}, nil
		}
	}
	return Resolved{Path: fresh, Origin: OriginDefault}, nil
}

// ProjectsDirectoryName holds one directory per project.
const ProjectsDirectoryName = "projects"

// earlierProductsDirectoryName is where the earlier layout kept each product's
// records, and earlierWorktreesDirectoryName its worktrees.
const (
	earlierProductsDirectoryName  = "products"
	earlierWorktreesDirectoryName = "worktrees"
)

// Names inside a project directory.
const (
	ConfigFileName        = "config.yaml"
	BindingFileName       = "repository.json"
	StateDirectoryName    = "state"
	WorktreeDirectoryName = "worktrees"
)

// EarlierLayout reports whether a home keeps its records the way the earlier
// builds did, under `products/<product id>/`. That is what decides where every
// product's records are read from, and it is read off the home itself rather
// than off how the home was found: a home set by YOYODYNE_STATE_HOME to where
// the earlier builds kept their state defers the migration, exactly as the
// earlier default does.
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
// home when the configuration leaves the worktree root to the harness.
func WorktreeDirectory(root, productID, repositoryID string) string {
	if EarlierLayout(root) {
		return filepath.Join(filepath.Clean(root), earlierWorktreesDirectoryName, productID, repositoryID)
	}
	return filepath.Join(ProjectDirectory(root, productID), WorktreeDirectoryName)
}

// shellQuote quotes a path for a command an operator is told to paste.
func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-~", r))
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
