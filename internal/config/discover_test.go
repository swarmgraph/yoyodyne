package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

// machine describes the environment a discovery runs on: the variables set on
// it and where its home directory is. Every test here uses one rather than the
// process's own, so a discovery test says which machine it means and an operator
// running the suite with YOYODYNE_CONFIG exported does not fail it.
type machine struct {
	variables map[string]string
	home      string
}

func (m machine) getenv(name string) string { return m.variables[name] }

func (m machine) userHomeDir() (string, error) {
	if m.home == "" {
		return "", errors.New("this machine has no home directory")
	}
	return m.home, nil
}

// discoverOn is Discover as it behaves on a described machine.
func discoverOn(m machine, start string) (string, error) {
	return DiscoverIn(m.getenv, m.userHomeDir, start)
}

// blank is a machine with nothing set and a home of its own, which is what most
// of these tests want: whatever they arrange is the only thing there is.
func blank(t *testing.T) machine {
	t.Helper()
	return machine{variables: map[string]string{}, home: t.TempDir()}
}

func TestDiscoverFindsTheNearestProjectConfiguration(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig, nil)
	nested := filepath.Join(project, "internal", "deeply", "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	want := filepath.Join(project, DirectoryName, FileName)

	for _, start := range []string{project, nested} {
		got, err := discoverOn(blank(t), start)
		if err != nil {
			t.Fatalf("Discover(%q) error = %v", start, err)
		}
		if got != want {
			t.Errorf("Discover(%q) = %q, want %q", start, got, want)
		}
	}
}

// A project that has not migrated yet keeps working: the legacy file is still
// discovered, and its personas resolve against the .yoyodyne directory the
// project would create during migration.
func TestDiscoverFallsBackToTheLegacyFile(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	path := filepath.Join(project, LegacyFileName)
	if err := os.WriteFile(path, []byte(validBootstrapConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := discoverOn(blank(t), project)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got != path {
		t.Fatalf("Discover() = %q, want %q", got, path)
	}
	directories := personaDirectories(got)
	if want := filepath.Join(project, DirectoryName); len(directories) != 1 || directories[0] != want {
		t.Fatalf("persona directories = %v, want only %q", directories, want)
	}
}

// The directory form wins over the legacy file in the same directory, so a
// half-finished migration cannot silently keep using the old configuration.
func TestDiscoverPrefersTheDirectoryForm(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig, nil)
	if err := os.WriteFile(filepath.Join(project, LegacyFileName), []byte(validBootstrapConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := discoverOn(blank(t), project)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if want := filepath.Join(project, DirectoryName, FileName); got != want {
		t.Fatalf("Discover() = %q, want %q", got, want)
	}
}

func TestDiscoverReportsWhatItLookedFor(t *testing.T) {
	t.Parallel()

	_, err := discoverOn(blank(t), t.TempDir())
	var notFound NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Discover() error = %v, want NotFoundError", err)
	}
	for _, expected := range []string{DirectoryName + "/" + FileName, LegacyFileName} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not mention %q", err, expected)
		}
	}
}

// The escape hatch as a first-class mode: a contributor who cannot commit tool
// config to somebody else's repository keeps their configuration on this machine
// and runs `yoyo` in that repository with nothing else typed.
func TestDiscoverFindsThisMachinesConfigurationForTheRepository(t *testing.T) {
	t.Parallel()

	repository := gitRepository(t)
	nested := filepath.Join(repository, "internal", "deeply", "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	host := blank(t)
	want := writeExternalProject(t, host, repository, minimalProjectConfig)

	for _, start := range []string{repository, nested} {
		got, err := discoverOn(host, start)
		if err != nil {
			t.Fatalf("Discover(%q) error = %v", start, err)
		}
		if got != want {
			t.Errorf("Discover(%q) = %q, want %q", start, got, want)
		}
	}
}

// A repository that describes itself is what every collaborator gets, so the
// project's own configuration wins over one machine's. The other order would let
// a file somebody wrote once quietly govern a project that carries its own.
func TestTheProjectsOwnConfigurationWinsOverThisMachines(t *testing.T) {
	t.Parallel()

	repository := gitRepository(t)
	writeProject(t, repository, minimalProjectConfig, nil)
	host := blank(t)
	writeExternalProject(t, host, repository, minimalProjectConfig)

	got, err := discoverOn(host, repository)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if want := filepath.Join(repository, DirectoryName, FileName); got != want {
		t.Errorf("Discover() = %q, want the project's own configuration %q", got, want)
	}
}

// An external configuration is found by the repository's binding rather than by
// the checkout it is read from, so a run's worktree -- and a check or a hook that
// shells out to `yoyo` from inside one -- finds the configuration the checkout it
// was added from is configured by.
func TestDiscoverFindsOneConfigurationFromEveryWorktreeOfARepository(t *testing.T) {
	t.Parallel()

	repository := gitRepository(t)
	host := blank(t)
	want := writeExternalProject(t, host, repository, minimalProjectConfig)

	// What `git worktree add` leaves behind: a `.git` file pointing at an
	// administrative directory inside the repository, and a `commondir` beside
	// that naming the repository they share.
	worktree := filepath.Join(t.TempDir(), "run-worktree")
	administrative := filepath.Join(repository, ".git", "worktrees", "run-worktree")
	if err := os.MkdirAll(administrative, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(administrative, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+administrative+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := discoverOn(host, worktree)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got != want {
		t.Errorf("Discover() = %q, want the repository's own configuration %q", got, want)
	}
}

// A submodule carries the same kind of `.git` file a linked worktree does, and
// is the opposite case: a checkout of its own, with its own history and its own
// checks, so it is found as itself. Following its pointer the way a worktree's
// is followed would find it by `<super>/.git/modules`, which is not a checkout at
// all and which every submodule of one superproject would share -- so a
// configuration written for one would be discovered from the next.
func TestASubmoduleIsAConfigurableCheckoutOfItsOwn(t *testing.T) {
	t.Parallel()

	superproject := gitRepository(t)
	var submodules []string
	for _, name := range []string{"first", "second"} {
		submodule := filepath.Join(superproject, name)
		// What `git submodule add` leaves behind: a `.git` file pointing into the
		// superproject's modules directory, and -- unlike a linked worktree -- no
		// `commondir` beside what it points at.
		administrative := filepath.Join(superproject, ".git", "modules", name)
		if err := os.MkdirAll(administrative, 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.MkdirAll(submodule, 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		pointer := "gitdir: " + filepath.Join("..", ".git", "modules", name) + "\n"
		if err := os.WriteFile(filepath.Join(submodule, ".git"), []byte(pointer), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		submodules = append(submodules, submodule)

		root, err := RepositoryRoot(submodule)
		if err != nil {
			t.Fatalf("RepositoryRoot(%q) error = %v", submodule, err)
		}
		if root != submodule {
			t.Errorf("RepositoryRoot(%q) = %q, want the submodule's own checkout", submodule, root)
		}
	}
	// End to end, which is what the mis-resolution would actually have cost: a
	// configuration written for one submodule is not discovered from the other.
	host := blank(t)
	want := writeExternalProject(t, host, submodules[0], minimalProjectConfig)
	got, err := discoverOn(host, submodules[0])
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got != want {
		t.Errorf("Discover() = %q, want %q", got, want)
	}
	if _, err := discoverOn(host, submodules[1]); err == nil {
		t.Error("the second submodule was configured by the first submodule's configuration")
	}
}

// A configuration left where earlier builds kept one — under the configurations
// home, in a directory keyed by the checkout's path, wherever
// YOYODYNE_CONFIG_HOME or XDG_CONFIG_HOME pointed that home — is not read: only
// the migration reads that home now.
func TestAConfigurationInTheEarlierConfigurationsHomeIsNotFound(t *testing.T) {
	t.Parallel()

	repository := gitRepository(t)
	resolved, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(resolved))
	key := filepath.Base(resolved) + "-" + hex.EncodeToString(digest[:])[:12]
	host := blank(t)
	relocated, xdg := t.TempDir(), t.TempDir()
	for _, earlier := range []string{filepath.Join(host.home, ".config", "yoyodyne"), relocated, filepath.Join(xdg, "yoyodyne")} {
		path := filepath.Join(earlier, "projects", key, FileName)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(minimalProjectConfig), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, variables := range []map[string]string{{}, {"YOYODYNE_CONFIG_HOME": relocated}, {"XDG_CONFIG_HOME": xdg}} {
		host.variables = variables
		got, err := discoverOn(host, repository)
		var notFound NotFoundError
		if !errors.As(err, &notFound) {
			t.Fatalf("Discover() with %v = %q, %v; want nothing found", variables, got, err)
		}
	}
}

// A configuration the operator named outright is used wherever it is, and a
// variable naming nothing readable is a failure rather than a step that is
// skipped: falling through to a different configuration than the one that was
// named is how a command does the right thing to the wrong project.
func TestDiscoverReadsTheConfigurationTheEnvironmentNames(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig, nil)
	elsewhere := t.TempDir()
	writeProject(t, elsewhere, minimalProjectConfig, nil)
	named := filepath.Join(elsewhere, DirectoryName, FileName)

	// The file itself, and the directory holding it, both name it: an operator
	// has one or the other in hand, and refusing the directory would be refusing
	// what `init --external` just printed.
	for _, value := range []string{named, filepath.Join(elsewhere, DirectoryName)} {
		host := blank(t)
		host.variables[EnvironmentVariable] = value
		got, err := discoverOn(host, project)
		if err != nil {
			t.Fatalf("Discover() with %s=%q error = %v", EnvironmentVariable, value, err)
		}
		if got != named {
			t.Errorf("Discover() with %s=%q = %q, want %q", EnvironmentVariable, value, got, named)
		}
	}

	for _, refused := range []struct {
		name  string
		value string
	}{
		{name: "a path that is not absolute", value: filepath.Join(DirectoryName, FileName)},
		{name: "a file that is not there", value: filepath.Join(elsewhere, "absent.yaml")},
		{name: "a directory with no configuration in it", value: elsewhere},
	} {
		host := blank(t)
		host.variables[EnvironmentVariable] = refused.value
		if _, err := discoverOn(host, project); err == nil {
			t.Errorf("%s: Discover() found a configuration, want a refusal naming %s", refused.name, EnvironmentVariable)
		}
	}
}

// What a refusal has to say is where to put a configuration, and this machine's
// place for one is not somewhere an operator would guess.
func TestNotFoundNamesWhereThisMachineWouldKeepOne(t *testing.T) {
	t.Parallel()

	repository := gitRepository(t)
	host := blank(t)
	_, err := discoverOn(host, repository)
	var notFound NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Discover() error = %v, want NotFoundError", err)
	}
	projects := filepath.Join(host.home, ".yoyodyne", "projects")
	if notFound.Projects != projects {
		t.Errorf("NotFoundError.Projects = %q, want %q", notFound.Projects, projects)
	}
	for _, expected := range []string{projects, "yoyo init --external", EnvironmentVariable} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not mention %q", err, expected)
		}
	}

	// A project bound to the repository that keeps no configuration is named.
	writeBindingOnly(t, host, repository)
	_, err = discoverOn(host, repository)
	if !errors.As(err, &notFound) || notFound.BoundProject != filepath.Base(repository) {
		t.Fatalf("Discover() with a bare binding = %v, want NotFoundError naming the bound project", err)
	}
}

// A directory in no repository has nothing to find a binding for, so nothing was
// looked up and the refusal does not name a place nothing could have read.
func TestNotFoundNamesNoProjectsOutsideARepository(t *testing.T) {
	t.Parallel()

	_, err := discoverOn(blank(t), t.TempDir())
	var notFound NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Discover() error = %v, want NotFoundError", err)
	}
	if notFound.Projects != "" {
		t.Errorf("NotFoundError.Projects = %q, want nothing for a directory in no repository", notFound.Projects)
	}
}

// developerPersonaOverride replaces one inherited persona with a file of the
// project's own, so a test can say which directory that file was read from.
const developerPersonaOverride = minimalProjectConfig + `agents:
  developer:
    persona:
      version: project-1
      path: personas/developer.md
`

// The personas of a configuration kept outside a repository are beside it, which
// is what makes the directory `init --external` writes self-contained.
func TestPersonasResolveBesideAnExternalConfiguration(t *testing.T) {
	t.Parallel()

	repository := gitRepository(t)
	host := blank(t)
	path := writeExternalProject(t, host, repository, developerPersonaOverride)
	body := "# Developer\n\nKept on this machine.\n"
	writePersona(t, filepath.Dir(path), body)

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.Agents["developer"].Persona.Text; got != body {
		t.Errorf("developer persona = %q, want the file beside the configuration", got)
	}
}

// The other half of that rule, and the reason it is a fallback rather than a
// move: a configuration somebody placed by hand and named with --config kept its
// personas in a .yoyodyne directory beside it, and goes on reading them. Where
// both exist, the one beside the file wins, because that is the directory this
// layout is written and moved as one.
func TestPersonasStillResolveFromTheSiblingDirectoryOfAPlacedConfiguration(t *testing.T) {
	t.Parallel()

	placed := t.TempDir()
	path := filepath.Join(placed, FileName)
	if err := os.WriteFile(path, []byte(developerPersonaOverride), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	sibling := "# Developer\n\nWhere it was before.\n"
	writePersona(t, filepath.Join(placed, DirectoryName), sibling)

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.Agents["developer"].Persona.Text; got != sibling {
		t.Errorf("developer persona = %q, want the one in the %s directory beside it", got, DirectoryName)
	}

	beside := "# Developer\n\nBeside the configuration.\n"
	writePersona(t, placed, beside)
	loaded, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.Agents["developer"].Persona.Text; got != beside {
		t.Errorf("developer persona = %q, want the file beside the configuration to win", got)
	}
}

// writePersona puts the developer persona the override above names under one
// directory.
func writePersona(t *testing.T, directory, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(directory, "personas"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "personas", "developer.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// gitRepository is a directory a configuration can be keyed by. It needs the
// marker Git leaves and nothing else: what discovery reads is the filesystem
// rather than a working Git, so that it never depends on a subprocess.
func gitRepository(t *testing.T) string {
	t.Helper()

	repository := filepath.Join(t.TempDir(), "their-project")
	if err := os.MkdirAll(filepath.Join(repository, ".git"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	return repository
}

// writeExternalProject puts a configuration where a machine keeps the one it
// holds for a repository — the project directory, named for the checkout,
// whose binding names that repository — and returns the file it wrote.
func writeExternalProject(t *testing.T, host machine, repository, contents string) string {
	t.Helper()

	directory := writeBindingOnly(t, host, repository)
	path := filepath.Join(directory, FileName)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// writeBindingOnly binds a project directory named for the checkout to the
// repository, and returns the directory.
func writeBindingOnly(t *testing.T, host machine, repository string) string {
	t.Helper()

	common, err := home.CommonGitDirectory(repository)
	if err != nil || common == "" {
		t.Fatalf("CommonGitDirectory(%q) = %q, %v", repository, common, err)
	}
	directory := filepath.Join(host.home, home.DirectoryName, home.ProjectsDirectoryName, filepath.Base(repository))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content, err := json.Marshal(home.Binding{GitCommonDirectory: common, Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, home.BindingFileName), content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return directory
}

func TestProjectDirectoryIsTheProjectNotTheConfigurationDirectory(t *testing.T) {
	t.Parallel()

	project := filepath.Join(string(filepath.Separator), "tmp", "example")
	if got := ProjectDirectory(filepath.Join(project, DirectoryName, FileName)); got != project {
		t.Errorf("ProjectDirectory() = %q, want %q", got, project)
	}
	if got := ProjectDirectory(filepath.Join(project, LegacyFileName)); got != project {
		t.Errorf("ProjectDirectory() legacy = %q, want %q", got, project)
	}
}

// earlierHomeReaders are the files allowed to name the configurations home
// earlier builds kept at ~/.config/yoyodyne: the migration that moves what is
// there, and nothing else. It is empty until the migration lands.
var earlierHomeReaders = map[string]bool{}

// Nothing but the migration reads the earlier configurations home. The behaviour
// tests above show discovery and the machine file no longer look there; this
// reads every source file and script the product ships, so a new reader of that
// home fails here rather than quietly keeping it alive.
func TestNothingButTheMigrationNamesTheEarlierConfigurationsHome(t *testing.T) {
	t.Parallel()

	earlier := regexp.MustCompile(`YOYODYNE_CONFIG_HOME|\.config/yoyodyne|"\.config",\s*"yoyodyne"|XDG_CONFIG_HOME.*"yoyodyne"`)
	repository := filepath.Join("..", "..")
	err := filepath.WalkDir(repository, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".git" || name == "node_modules" || name == "testdata" || name == "docs" {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(repository, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		shipped := strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") ||
			strings.HasPrefix(relative, "bin/") || strings.HasSuffix(path, ".sh")
		if !shipped || earlierHomeReaders[relative] {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for number, line := range strings.Split(string(source), "\n") {
			if earlier.MatchString(line) {
				t.Errorf("%s:%d names the earlier configurations home, which only the migration reads: %s", relative, number+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
