package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

const (
	// DirectoryName is the portable project configuration directory. It holds
	// the project configuration and any persona overrides, and nothing that
	// depends on the machine it was written on.
	DirectoryName = ".yoyodyne"
	// FileName is the project configuration file inside DirectoryName. It is
	// also what names a configuration directory anywhere else: a directory
	// holding this file holds the personas beside it.
	FileName = "config.yaml"
	// LegacyFileName is the pre-directory configuration file. It is still
	// accepted so an existing project keeps working without being migrated.
	LegacyFileName = ".yoyodyne.yaml"
)

const (
	// EnvironmentVariable names one configuration outright, wherever it is. It
	// is read before anything is searched for, so a shell that exports it
	// configures every command run in that shell without repeating --config.
	EnvironmentVariable = "YOYODYNE_CONFIG"
	// EarlierHomeVariable relocated the directory earlier builds kept external
	// configurations in. That directory is no longer read; the variable is still
	// honoured in saying where a configuration left there is.
	EarlierHomeVariable = "YOYODYNE_CONFIG_HOME"
	// earlierExternalDirectoryName held one directory per repository inside that
	// earlier home.
	earlierExternalDirectoryName = "projects"
)

// externalKeyDigits is how much of a repository's digest its directory name
// carries: enough that two repositories one machine holds never collide, and
// short enough that an operator can still match a directory to a project by eye
// from the readable half beside it. It is the configuration revision's number
// for the configuration revision's reason.
const externalKeyDigits = 12

// gitDirectoryName marks the root of the repository an external configuration is
// keyed by. It is read off the filesystem rather than asked of Git, because
// discovery happens before anything else and must not depend on a subprocess.
const gitDirectoryName = ".git"

// NotFoundError reports that no configuration exists for a directory. It names
// every place that was looked in so an operator can create the right file rather
// than guess, and the machine home's projects only when there was a repository
// to find a binding for — naming a place nothing could have looked at would be
// inventing one.
type NotFoundError struct {
	StartDirectory string
	// Projects is the projects directory of the machine home a binding for the
	// repository was looked for in, and is empty when the start directory is in
	// no repository.
	Projects string
	// BoundProject is the project whose binding names the repository, where one
	// does and it keeps no configuration.
	BoundProject string
	// Earlier is a configuration kept for this repository where earlier builds
	// kept one, which is no longer read.
	Earlier string
}

func (e NotFoundError) Error() string {
	message := fmt.Sprintf("no Yoyodyne configuration found in %s or any parent directory (looked for %s/%s and %s)",
		e.StartDirectory, DirectoryName, FileName, LegacyFileName)
	switch {
	case e.BoundProject != "":
		message += fmt.Sprintf(", and the project %s bound to this repository keeps no %s in %s",
			e.BoundProject, FileName, filepath.Join(e.Projects, e.BoundProject))
	case e.Projects != "":
		message += fmt.Sprintf(", nor in a project directory under %s bound to this repository, where a configuration kept outside the repository lives", e.Projects)
	}
	if e.Earlier != "" {
		message += fmt.Sprintf("; a configuration earlier builds kept for this repository is at %s and is no longer read there — "+
			"move it to %s and run `%s --product <id>` from the repository",
			e.Earlier, filepath.Join(e.projectsOrHome(), "<id>", FileName), home.BindCommand)
	}
	return message + fmt.Sprintf("; write one with `yoyo init`, keep one outside the repository with `yoyo init --external`, or name one with --config or %s",
		EnvironmentVariable)
}

func (e NotFoundError) projectsOrHome() string {
	if e.Projects != "" {
		return e.Projects
	}
	return filepath.Join("~", home.DirectoryName, home.ProjectsDirectoryName)
}

// Discover finds the configuration that governs a directory, using this
// process's own environment.
func Discover(startDirectory string) (string, error) {
	return DiscoverIn(os.Getenv, os.UserHomeDir, startDirectory)
}

// DiscoverIn finds the configuration that governs a directory on a described
// machine, in this order:
//
//  1. the configuration YOYODYNE_CONFIG names, if it names one;
//  2. otherwise the nearest project configuration, walking from the starting
//     directory towards the filesystem root, so Yoyodyne is runnable from a
//     nested directory of a project rather than only from its root;
//  3. otherwise this machine's own configuration for the repository that
//     directory is in, which is what a contributor who cannot commit tool
//     config to somebody else's repository keeps.
//
// The project's own configuration is looked for before this machine's on
// purpose: a repository that describes itself is what every collaborator gets,
// and a file on one machine must not quietly win over it.
func DiscoverIn(getenv func(string) string, userHomeDir func() (string, error), startDirectory string) (string, error) {
	start, err := filepath.Abs(startDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve start directory: %w", err)
	}
	named, err := namedConfiguration(getenv)
	if err != nil || named != "" {
		return named, err
	}
	project, err := projectConfiguration(start)
	if err != nil || project != "" {
		return project, err
	}

	// A configuration kept outside the repository is found by the binding of the
	// project directory it is kept in, so a directory in no repository has
	// nothing to find a binding for: that is reported as having searched one
	// place fewer rather than as a failure to read this machine.
	return boundConfiguration(getenv, userHomeDir, start)
}

// namedConfiguration reads the configuration YOYODYNE_CONFIG names, and answers
// with nothing at all when the variable is unset.
//
// A variable that is set and names nothing readable is a failure rather than a
// step that is skipped. It is an instruction, exactly as --config is, and
// silently falling through to a different configuration than the one the
// operator named is how a command ends up doing the right thing to the wrong
// project.
func namedConfiguration(getenv func(string) string) (string, error) {
	value := strings.TrimSpace(getenv(EnvironmentVariable))
	if value == "" {
		return "", nil
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be an absolute path and is %q", EnvironmentVariable, value)
	}
	candidate := filepath.Clean(value)
	info, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("%s names %s: %w", EnvironmentVariable, candidate, err)
	}
	// A directory is taken as a configuration directory rather than refused, so
	// the variable accepts what an operator has in hand: `.yoyodyne` and the
	// directory `init --external` reports are both directories holding the file.
	if info.IsDir() {
		candidate = filepath.Join(candidate, FileName)
		if info, err = os.Stat(candidate); err != nil {
			return "", fmt.Errorf("%s names a directory with no %s in it: %w", EnvironmentVariable, FileName, err)
		}
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s names %s, which is not a file", EnvironmentVariable, candidate)
	}
	return candidate, nil
}

// projectConfiguration is the nearest configuration a project carries, or
// nothing when no directory up to the filesystem root carries one.
func projectConfiguration(start string) (string, error) {
	for directory := start; ; {
		for _, candidate := range []string{
			filepath.Join(directory, DirectoryName, FileName),
			filepath.Join(directory, LegacyFileName),
		} {
			regular, err := isRegularFile(candidate)
			if err != nil {
				return "", err
			}
			if regular {
				return candidate, nil
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", nil
		}
		directory = parent
	}
}

// boundConfiguration is the configuration kept in the machine home's project
// directory whose binding names the repository a directory is in. It is found
// by the binding rather than by where the checkout is, so it is found from the
// repository root, from any directory beneath it, and from any worktree of it.
func boundConfiguration(getenv func(string) string, userHomeDir func() (string, error), start string) (string, error) {
	repository, err := RepositoryRoot(start)
	if err != nil {
		return "", err
	}
	if repository == "" {
		return "", NotFoundError{StartDirectory: start}
	}
	notFound := NotFoundError{StartDirectory: start, Earlier: earlierExternalConfiguration(getenv, userHomeDir, repository)}
	resolved, err := home.Resolve(getenv, userHomeDir, runtime.GOOS)
	if err != nil {
		// A machine with no home directory has nowhere to keep one, which is a
		// place fewer to look rather than a failure of the search. A variable
		// that was set and is wrong is the operator's instruction, and still
		// fails.
		if strings.TrimSpace(getenv(home.StateHomeVariable)) != "" {
			return "", err
		}
		return "", notFound
	}
	notFound.Projects = filepath.Join(resolved.Path, home.ProjectsDirectoryName)
	project, found, err := home.BoundProject(resolved.Path, repository)
	if err != nil {
		return "", err
	}
	if !found {
		return "", notFound
	}
	if project.Configuration == "" {
		notFound.BoundProject = project.ID
		return "", notFound
	}
	return project.Configuration, nil
}

// earlierExternalConfiguration is the configuration earlier builds kept for a
// repository outside it, where one is still there. It is only looked for, so
// that a configuration nobody has moved is named rather than silently not
// found.
func earlierExternalConfiguration(getenv func(string) string, userHomeDir func() (string, error), repository string) string {
	earlier, err := EarlierExternalPath(getenv, userHomeDir, repository)
	if err != nil {
		return ""
	}
	if regular, err := isRegularFile(earlier); err != nil || !regular {
		return ""
	}
	return earlier
}

func isRegularFile(candidate string) (bool, error) {
	info, err := os.Stat(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect %q: %w", candidate, err)
	}
	return info.Mode().IsRegular(), nil
}

// EarlierExternalPath is where earlier builds kept the configuration of one
// repository that does not carry its own: under the configurations home, in a
// directory keyed by where the checkout is. Nothing reads it any more; it is
// named so a configuration left there is reported, and so the migration knows
// where to move it from.
func EarlierExternalPath(getenv func(string) string, userHomeDir func() (string, error), repositoryRoot string) (string, error) {
	earlierHome, err := home.EarlierConfigurationsHome(getenv, userHomeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(earlierHome, earlierExternalDirectoryName, externalKey(repositoryRoot), FileName), nil
}

// externalKey names one repository's directory inside the earlier
// configurations home.
//
// It is two halves because it answers to two readers. The digest is what makes
// it a key: a repository is identified by where it is checked out, which is a
// path rather than a name, and two checkouts of the same project on one machine
// are two projects to configure. The readable half is for the operator listing
// the directory, who otherwise has a home full of hex.
//
// The path is resolved through its own symlinks first, so a checkout reached by
// two names is one key rather than two configurations that silently disagree.
func externalKey(repositoryRoot string) string {
	resolved := repositoryRoot
	if absolute, err := filepath.Abs(repositoryRoot); err == nil {
		resolved = absolute
		if linked, err := filepath.EvalSymlinks(absolute); err == nil {
			resolved = linked
		}
	}
	digest := sha256.Sum256([]byte(resolved))
	key := hex.EncodeToString(digest[:])[:externalKeyDigits]
	if name := externalKeyName(filepath.Base(resolved)); name != "" {
		return name + "-" + key
	}
	return key
}

// externalKeyName is the readable half of a key: the checkout's own directory
// name, reduced to what is a directory name everywhere. It carries no meaning —
// the digest beside it is what identifies the repository — so a name that
// reduces to nothing is simply left out rather than invented.
func externalKeyName(name string) string {
	var builder strings.Builder
	separated := false
	for _, letter := range strings.ToLower(name) {
		switch {
		case letter >= 'a' && letter <= 'z', letter >= '0' && letter <= '9':
			if separated && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			separated = false
			builder.WriteRune(letter)
		default:
			separated = true
		}
	}
	return builder.String()
}

// RepositoryRoot is the repository a directory belongs to, or nothing when it
// belongs to none. It is what an external configuration is keyed by: a
// configuration kept outside a repository still describes that repository, and
// it has to be found from any directory inside it.
//
// A linked worktree is resolved to the repository it was added from rather than
// treated as a repository of its own, because it is the same project: a run's
// worktree, and a check or a hook that shells out to `yoyo` from inside one,
// must find the configuration the checkout it came from is configured by. A
// submodule is the other way about — a checkout of its own, configured on its
// own — and every other `.git` file is read as one of those rather than guessed
// at.
func RepositoryRoot(startDirectory string) (string, error) {
	start, err := filepath.Abs(startDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve start directory: %w", err)
	}
	for directory := start; ; {
		marker := filepath.Join(directory, gitDirectoryName)
		info, err := os.Stat(marker)
		switch {
		case err == nil && info.IsDir():
			return directory, nil
		case err == nil && info.Mode().IsRegular():
			common, err := commonGitDirectory(marker)
			if err != nil {
				return "", err
			}
			if common == "" {
				// Everything else a `.git` file marks is the root of its own
				// checkout: a submodule, a clone made with --separate-git-dir, and a
				// pointer this does not understand. Each is answered as the directory
				// the file was found in, which is the checkout somebody is standing
				// in and the one they would configure.
				return directory, nil
			}
			return filepath.Dir(common), nil
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return "", fmt.Errorf("inspect %q: %w", marker, err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", nil
		}
		directory = parent
	}
}

// commonGitDirectory reads the repository directory a linked worktree's `.git`
// file points at, and answers with nothing for a `.git` file that is not a
// linked worktree's.
//
// The pointer alone does not say which it is. Git writes `gitdir: <path>` in the
// `.git` file of a linked worktree, of a submodule, and of a clone made with
// --separate-git-dir. What marks a linked worktree is the `commondir` file
// beside the directory it points at, naming the repository every worktree of it
// shares, so that is what is followed and nothing else is.
//
// A submodule has no such file, and guessing from its pointer costs more than
// not answering: `gitdir: ../.git/modules/sub` would resolve to
// `/super/.git/modules`, which is not a checkout at all and which every
// submodule of that superproject would resolve to alike. A submodule is a
// checkout in its own right -- its own history, its own remote, its own
// checks -- so it is keyed as the directory it is.
func commonGitDirectory(marker string) (string, error) {
	content, err := os.ReadFile(marker)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", marker, err)
	}
	pointer, found := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !found {
		return "", nil
	}
	administrative := strings.TrimSpace(pointer)
	if administrative == "" {
		return "", nil
	}
	if !filepath.IsAbs(administrative) {
		administrative = filepath.Join(filepath.Dir(marker), administrative)
	}
	common, err := os.ReadFile(filepath.Join(administrative, "commondir"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the repository directory of the worktree at %q: %w", filepath.Dir(marker), err)
	}
	shared := strings.TrimSpace(string(common))
	if shared == "" {
		return "", nil
	}
	if !filepath.IsAbs(shared) {
		shared = filepath.Join(administrative, shared)
	}
	return filepath.Clean(shared), nil
}

// ProjectDirectory is the directory a configuration's relative paths resolve
// against. A configuration inside .yoyodyne describes the project that contains
// that directory, not the directory itself, so "repository: ." keeps meaning
// the project root after migration.
//
// A configuration kept outside the repository it describes has no such project
// above it, and this answers with the directory the file is in. That is why an
// external configuration states an absolute `product.repository` — which is what
// `yoyo init --external` writes — rather than a relative one that would resolve
// against a directory holding nothing but the configuration.
func ProjectDirectory(configPath string) string {
	directory := filepath.Dir(configPath)
	if filepath.Base(directory) == DirectoryName {
		return filepath.Dir(directory)
	}
	return directory
}

// ConfigurationDirectories are the directories a configuration file keeps the
// project's own files in — its personas, and the workflow definitions it owns —
// in the order they are looked for.
//
// A configuration inside .yoyodyne uses that directory, and a legacy
// .yoyodyne.yaml uses the .yoyodyne directory of the same project, which is
// where migrating it would put them. Both are one place.
//
// A config.yaml somewhere else — the layout `init --external` writes — keeps its
// files beside it, which is what makes that directory self-contained and
// movable. The .yoyodyne sibling is still looked in after it, and only after it:
// somebody who placed a configuration by hand before this existed and named it
// with --config kept their personas there, and a rule that stopped reading them
// would break a working installation to tidy up a layout.
func ConfigurationDirectories(configPath string) []string {
	directory := filepath.Dir(configPath)
	if filepath.Base(directory) == DirectoryName {
		return []string{directory}
	}
	if filepath.Base(configPath) == FileName {
		return []string{directory, filepath.Join(directory, DirectoryName)}
	}
	return []string{filepath.Join(directory, DirectoryName)}
}

// personaDirectories are where one configuration file's project personas live,
// which is where everything else it owns lives: a project has one place its
// configuration keeps what belongs to it rather than a rule per kind of file.
func personaDirectories(configPath string) []string {
	return ConfigurationDirectories(configPath)
}

// personaLoaderFor reads the personas of one configuration file, from wherever
// that file keeps them.
func personaLoaderFor(configPath string) personaLoader {
	directories := personaDirectories(configPath)
	if len(directories) == 1 {
		return directoryPersonaLoader{root: directories[0]}
	}
	loaders := make([]personaLoader, 0, len(directories))
	for _, directory := range directories {
		loaders = append(loaders, directoryPersonaLoader{root: directory})
	}
	return firstPersonaLoader{loaders: loaders}
}
