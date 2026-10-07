package home

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// Binding is what `projects/<id>/repository.json` records: the repository a
// product id is, named by its Git common directory, which every worktree of one
// clone shares. A run's worktree, and a worktree a person made, are therefore
// the same project and never a second one.
type Binding struct {
	GitCommonDirectory string `json:"git_common_directory"`
	// Repository is the checkout the binding was written from, for reading; the
	// common directory is what is compared.
	Repository string `json:"repository"`
	// RemoteURL is the remote the repository publishes to where it has one. It
	// is what tells a second clone of one project from a second product that
	// happens to use the same id.
	RemoteURL string    `json:"remote_url,omitempty"`
	BoundAt   time.Time `json:"bound_at"`
	// BoundBy is who wrote the binding: the process and, where it can be read,
	// the person running it.
	BoundBy string `json:"bound_by"`
}

// BindingPath is where one project's binding is kept.
func BindingPath(root, productID string) string {
	return filepath.Join(ProjectDirectory(root, productID), BindingFileName)
}

// ReadBinding reads one project's binding. Found is false where the project
// has no binding yet.
func ReadBinding(root, productID string) (binding Binding, found bool, err error) {
	target := BindingPath(root, productID)
	content, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		return Binding{}, false, nil
	}
	if err != nil {
		return Binding{}, false, fmt.Errorf("read the binding %s: %w", target, err)
	}
	if err := json.Unmarshal(content, &binding); err != nil {
		return Binding{}, false, fmt.Errorf("read the binding %s: %w", target, err)
	}
	if strings.TrimSpace(binding.GitCommonDirectory) == "" {
		return Binding{}, false, fmt.Errorf("the binding %s names no repository", target)
	}
	return binding, true, nil
}

// Present reports whether the bound repository is still where the binding
// says it is.
func (b Binding) Present() bool {
	info, err := os.Stat(b.GitCommonDirectory)
	return err == nil && info.IsDir()
}

// CommonGitDirectory is the Git directory every worktree of the checkout shares
// — what `git rev-parse --git-common-dir` answers, made absolute — or nothing
// for a directory that is not a Git checkout. It reads the filesystem rather
// than asking Git, as configuration discovery does, so every process can answer
// it before anything else runs.
func CommonGitDirectory(checkout string) (string, error) {
	dotGit := filepath.Join(checkout, ".git")
	info, err := os.Stat(dotGit)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", dotGit, err)
	}
	if info.IsDir() {
		return absolute(dotGit), nil
	}
	// A linked worktree or a submodule: the file names the Git directory, and a
	// linked worktree's names the shared one in its commondir.
	content, err := os.ReadFile(dotGit)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dotGit, err)
	}
	named, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !ok {
		return "", fmt.Errorf("%s names no Git directory", dotGit)
	}
	gitDirectory := strings.TrimSpace(named)
	if !filepath.IsAbs(gitDirectory) {
		gitDirectory = filepath.Join(checkout, gitDirectory)
	}
	if common, err := os.ReadFile(filepath.Join(gitDirectory, "commondir")); err == nil {
		shared := strings.TrimSpace(string(common))
		if !filepath.IsAbs(shared) {
			shared = filepath.Join(gitDirectory, shared)
		}
		gitDirectory = shared
	}
	return absolute(gitDirectory), nil
}

func absolute(value string) string {
	if resolved, err := filepath.Abs(value); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(value)
}

// RepositoryOf is the checkout a common directory belongs to, for saying: the
// directory above a `.git`, and the directory itself for anything else.
func RepositoryOf(commonDirectory string) string {
	if filepath.Base(commonDirectory) == ".git" {
		return filepath.Dir(commonDirectory)
	}
	return commonDirectory
}

// RemoteURL is the URL a checkout's named remote fetches from, or nothing where
// it has none or Git cannot say. It is read only when a binding is written or
// two bindings disagree, never on an ordinary start.
func RemoteURL(checkout, remote string) string {
	if strings.TrimSpace(remote) == "" {
		remote = "origin"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "-C", checkout, "config", "--get", "remote."+remote+".url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// sameRemote compares two remote URLs as the place they name, so the SSH and
// HTTPS spellings of one repository are one remote. Either being empty is not a
// match: an unknown remote says nothing about whether two clones are one
// project.
func sameRemote(left, right string) bool {
	left, right = normalizeRemote(left), normalizeRemote(right)
	return left != "" && left == right
}

func normalizeRemote(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if _, rest, found := strings.Cut(value, "://"); found {
		value = rest
		if host, _, _ := strings.Cut(value, "/"); strings.Contains(host, "@") {
			value = value[strings.Index(value, "@")+1:]
		}
	} else if at := strings.Index(value, "@"); at >= 0 && strings.Contains(value, ":") {
		// scp-like syntax: user@host:path
		value = strings.Replace(value[at+1:], ":", "/", 1)
	}
	value = strings.TrimSuffix(strings.TrimSuffix(value, "/"), ".git")
	return strings.ToLower(value)
}

// sameDirectory compares two directories as the directory they are where both
// exist, so one reached through a symlink is the one it links to.
func SameDirectory(left, right string) bool {
	if filepath.Clean(left) == filepath.Clean(right) {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

// The commands a binding refusal names. Each refusal names exactly one.
const (
	BindCommand   = "yoyo project bind"
	RenameCommand = "yoyo project rename"
	ListCommand   = "yoyo project list"
)

// BindingProblem is which of the three refusals a start met.
type BindingProblem string

const (
	// SecondClone is a second clone of a project whose id is already bound
	// to another clone on this machine: the two remotes match.
	SecondClone BindingProblem = "second-clone"
	// SharedID is two products using one id: the remotes differ, or one of
	// them has none.
	SharedID BindingProblem = "shared-id"
	// MissingRepository is a bound repository that is no longer at its path.
	MissingRepository BindingProblem = "missing-repository"
)

// BindingError is a start refused because this repository is not the one the
// product id is bound to. It names both repositories and one command.
type BindingError struct {
	Problem   BindingProblem
	ProductID string
	Binding   Binding
	// BindingPath is where the binding is recorded.
	BindingPath string
	// Repository is the checkout this process started in, and RemoteURL its
	// remote where one was read.
	Repository string
	RemoteURL  string
}

func (e *BindingError) Error() string {
	bound := RepositoryOf(e.Binding.GitCommonDirectory)
	switch e.Problem {
	case SecondClone:
		return fmt.Sprintf("project %s is bound to the clone at %s, and %s is a second clone of the same project (both fetch from %s); "+
			"this machine runs one clone of a project, and for %s it is %s, so this refuses to start. "+
			"Run yoyo from %s, or make this clone the project's with `%s --replace` run from here (binding recorded in %s)",
			e.ProductID, bound, e.Repository, e.RemoteURL, e.ProductID, bound, bound, BindCommand, e.BindingPath)
	case SharedID:
		return fmt.Sprintf("two products share the id %s: it is bound to the repository at %s%s, and %s%s names the same id, "+
			"so this refuses to start rather than mix their records. Give one of them another id: from %s, `%s %s <new>` "+
			"renames the one already bound (binding recorded in %s)",
			e.ProductID, bound, remoteClause(e.Binding.RemoteURL), e.Repository, remoteClause(e.RemoteURL),
			bound, RenameCommand, e.ProductID, e.BindingPath)
	default:
		return fmt.Sprintf("project %s is bound to the repository at %s, which is no longer there, so this refuses to start; "+
			"a moved clone and an unmounted volume look the same, so the harness never re-binds on that alone. "+
			"If %s is where the project now lives, run `%s` from here (binding recorded in %s)",
			e.ProductID, bound, e.Repository, BindCommand, e.BindingPath)
	}
}

func remoteClause(url string) string {
	if url == "" {
		return " (no remote)"
	}
	return " (fetching from " + url + ")"
}

// Agreement is what a start found about its project's binding.
type Agreement struct {
	// Bound is true where this start wrote the binding, which the start says.
	Bound   bool
	Binding Binding
	Path    string
}

// Says is the sentence a start that wrote the binding prints.
func (a Agreement) Says(productID string) string {
	return fmt.Sprintf("created the project directory for %s and bound it to the repository at %s, recorded in %s; "+
		"this machine runs %s from this clone from now on",
		productID, RepositoryOf(a.Binding.GitCommonDirectory), a.Path, productID)
}

// AgreeOptions describes the start asking.
type AgreeOptions struct {
	Root      string
	ProductID string
	// Checkout is the repository the process was started against; any of its
	// worktrees agrees the same way.
	Checkout string
	// Remote is the name of the remote the project publishes to.
	Remote string
	// Now and BoundBy describe the writer, and default to the moment and this
	// process.
	Now     func() time.Time
	BoundBy string
	// EvenInEarlierLayout writes a missing binding in a home still laid out the
	// earlier way too. `yoyo init --external` asks for it, because the
	// configuration it writes is found by the binding and by nothing else.
	EvenInEarlierLayout bool
}

// Agree holds a start to its project's binding: where the project has no
// binding its directory is created and the binding written, and a start against
// any other repository than the one bound is refused with a BindingError.
//
// In a home still laid out the earlier way, a project with no binding is left
// without one: the earlier home's state predates bindings, and the migration is
// what writes each one, from the checkout the state names. A binding already
// there — one `yoyo project bind` or `yoyo init --external` wrote — is held to
// in either layout.
//
// A checkout that is not a Git repository has nothing to bind by and is let
// through, as the state-root marker lets it through.
func Agree(options AgreeOptions) (Agreement, error) {
	common, err := CommonGitDirectory(options.Checkout)
	if err != nil || common == "" {
		return Agreement{}, err
	}
	target := BindingPath(options.Root, options.ProductID)
	binding, found, err := ReadBinding(options.Root, options.ProductID)
	if err != nil {
		return Agreement{}, err
	}
	if !found {
		if EarlierLayout(options.Root) && !options.EvenInEarlierLayout {
			return Agreement{}, nil
		}
		written, created, err := writeBinding(options, common, false)
		if err != nil {
			return Agreement{}, err
		}
		if created {
			return Agreement{Bound: true, Binding: written, Path: target}, nil
		}
		// Another process bound it first; hold this one to what it wrote.
		if binding, found, err = ReadBinding(options.Root, options.ProductID); err != nil {
			return Agreement{}, err
		} else if !found {
			return Agreement{}, fmt.Errorf("the binding %s could not be read back after it was written", target)
		}
	}
	if SameDirectory(binding.GitCommonDirectory, common) {
		return Agreement{Binding: binding, Path: target}, nil
	}
	refusal := &BindingError{ProductID: options.ProductID, Binding: binding, BindingPath: target, Repository: RepositoryOf(common)}
	if !binding.Present() {
		refusal.Problem = MissingRepository
		return Agreement{}, refusal
	}
	refusal.RemoteURL = RemoteURL(options.Checkout, options.Remote)
	if sameRemote(binding.RemoteURL, refusal.RemoteURL) {
		refusal.Problem = SecondClone
	} else {
		refusal.Problem = SharedID
	}
	return Agreement{}, refusal
}

// Bind writes the binding of a project to the checkout, replacing whatever it
// named before. It is `yoyo project bind`'s write; what refuses a bind — a
// recorded repository still present without --replace, a run in flight — is
// decided by the caller, which can read the runs.
func Bind(options AgreeOptions) (Binding, error) {
	common, err := CommonGitDirectory(options.Checkout)
	if err != nil {
		return Binding{}, err
	}
	if common == "" {
		return Binding{}, fmt.Errorf("%s is not a Git checkout, so there is no repository to bind %s to", options.Checkout, options.ProductID)
	}
	written, _, err := writeBinding(options, common, true)
	return written, err
}

// writeBinding writes the project's binding, and with it the project
// directory. A first start creates it rather than replacing it, so of two first
// starts at once exactly one binds and the other reads its binding back; a bind
// replaces it.
func writeBinding(options AgreeOptions, common string, replace bool) (Binding, bool, error) {
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	boundBy := options.BoundBy
	if boundBy == "" {
		boundBy = ProcessAccount()
	}
	binding := Binding{
		GitCommonDirectory: common,
		Repository:         RepositoryOf(common),
		RemoteURL:          RemoteURL(options.Checkout, options.Remote),
		BoundAt:            now().UTC().Truncate(time.Second),
		BoundBy:            boundBy,
	}
	content, err := json.MarshalIndent(binding, "", "  ")
	if err != nil {
		return Binding{}, false, err
	}
	content = append(content, '\n')
	if err := os.MkdirAll(options.Root, 0o700); err != nil {
		return Binding{}, false, fmt.Errorf("create the home %s: %w", options.Root, err)
	}
	root, err := repowrite.NewRoot(options.Root)
	if err != nil {
		return Binding{}, false, fmt.Errorf("open the home %s: %w", options.Root, err)
	}
	relative := path.Join(ProjectsDirectoryName, options.ProductID, BindingFileName)
	if replace {
		if _, err := root.WriteFile(relative, content); err != nil {
			return Binding{}, false, fmt.Errorf("record the binding of %s: %w", options.ProductID, err)
		}
		return binding, true, nil
	}
	_, created, err := root.CreateFile(relative, content)
	if err != nil {
		return Binding{}, false, fmt.Errorf("record the binding of %s: %w", options.ProductID, err)
	}
	return binding, created, nil
}

// ProcessAccount says which process is writing, and for whom where that can be
// read: its process id, its command line, and the user running it.
func ProcessAccount() string {
	command := "an unnamed command"
	if len(os.Args) > 0 {
		arguments := append([]string{filepath.Base(os.Args[0])}, os.Args[1:]...)
		command = "`" + strings.Join(arguments, " ") + "`"
	}
	account := fmt.Sprintf("process %d, running %s", os.Getpid(), command)
	if user := strings.TrimSpace(os.Getenv("USER")); user != "" {
		account += ", as " + user
	}
	return account
}

// Project is one project directory under a home, as `yoyo project list` names
// it.
type Project struct {
	ID        string
	Directory string
	Binding   Binding
	Bound     bool
	// BindingProblem is a binding that could not be read.
	BindingProblem string
	// Configuration is the configuration kept in the project directory, where
	// one is.
	Configuration string
	// Earlier is a product whose records are still in the earlier layout's
	// `products/` directory and which has no binding yet.
	Earlier bool
}

// Projects lists every project directory under a home, and in a home laid out
// the earlier way every product the earlier layout keeps records for, sorted by
// id.
func Projects(root string) ([]Project, error) {
	byID := map[string]*Project{}
	add := func(id string) *Project {
		if existing, ok := byID[id]; ok {
			return existing
		}
		project := &Project{ID: id, Directory: ProjectDirectory(root, id)}
		byID[id] = project
		return project
	}
	entries, err := os.ReadDir(filepath.Join(root, ProjectsDirectoryName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read the projects of %s: %w", root, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		project := add(entry.Name())
		binding, found, err := ReadBinding(root, entry.Name())
		switch {
		case err != nil:
			project.BindingProblem = err.Error()
		case found:
			project.Binding, project.Bound = binding, true
		}
		configuration := filepath.Join(project.Directory, ConfigFileName)
		if info, err := os.Stat(configuration); err == nil && info.Mode().IsRegular() {
			project.Configuration = configuration
		}
	}
	if EarlierLayout(root) {
		earlier, err := os.ReadDir(filepath.Join(root, earlierProductsDirectoryName))
		if err != nil {
			return nil, fmt.Errorf("read the products of %s: %w", root, err)
		}
		for _, entry := range earlier {
			if !entry.IsDir() {
				continue
			}
			if project := add(entry.Name()); !project.Bound {
				project.Earlier = true
			}
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	projects := make([]Project, 0, len(ids))
	for _, id := range ids {
		projects = append(projects, *byID[id])
	}
	return projects, nil
}

// BoundProject is the project whose binding names the repository a checkout
// belongs to, or nothing where none does. It is how a configuration kept in a
// project directory is found from inside the repository it describes, and from
// any worktree of it.
func BoundProject(root, checkout string) (Project, bool, error) {
	common, err := CommonGitDirectory(checkout)
	if err != nil || common == "" {
		return Project{}, false, err
	}
	projects, err := Projects(root)
	if err != nil {
		return Project{}, false, err
	}
	for _, project := range projects {
		if project.Bound && SameDirectory(project.Binding.GitCommonDirectory, common) {
			return project, true, nil
		}
	}
	return Project{}, false, nil
}
