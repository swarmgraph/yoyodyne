package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// projectActsFileName is where a project's state records the acts that point
// it at a repository or rename it, one JSON line each, with who ran them: a
// re-binding is the one thing that can point a project's history at another
// tree, so it is recorded where that history is.
const projectActsFileName = "project-acts.jsonl"

// projectAct is one line of that record.
type projectAct struct {
	Act string    `json:"act"`
	At  time.Time `json:"at"`
	By  string    `json:"by"`
	// From and To are what the act moved: the repositories of a bind, and the
	// ids of a rename.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

func runProject(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printProjectUsage(stderr)
		return 2
	}
	switch args[0] {
	case "bind":
		return bindProject(args[1:], stdout, stderr)
	case "rename":
		return renameProject(args[1:], stdout, stderr)
	case "list":
		return listProjects(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printProjectUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown project command %q\n\n", args[0])
		printProjectUsage(stderr)
		return 2
	}
}

// commandFailure reports an error in the form the command was asked for.
func commandFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, map[string]string{"error": err.Error()}); code != 0 {
			return code
		}
	} else {
		fmt.Fprintln(stderr, err)
	}
	return 1
}

// bindProject binds the current repository to the project directory for its
// id. It is the remedy for a moved or re-cloned repository, and for making a
// second clone the project's own.
func bindProject(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("project bind", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	product := flags.String("product", "", "the product id to bind, where no configuration can be found for this repository")
	directory := flags.String("directory", ".", "a directory in the repository to bind, with --product")
	replace := flags.Bool("replace", false, "unbind the repository the project is bound to now, although it is still there")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "project bind does not accept positional arguments")
		return 2
	}
	fail := func(err error) int { return commandFailure(stdout, stderr, *jsonOutput, err) }

	// The id and the repository come from the configuration where there is one.
	// A configuration kept in the project directory is found by the binding, so a
	// clone that moved cannot find it any more: --product names the id outright
	// for exactly that case.
	var productID, repository, remote string
	if strings.TrimSpace(*product) != "" {
		productID = strings.TrimSpace(*product)
		if err := domain.ValidateIdentifier("product id", productID); err != nil {
			return fail(err)
		}
		found, err := config.RepositoryRoot(*directory)
		if err != nil {
			return fail(err)
		}
		if found == "" {
			return fail(fmt.Errorf("%s is not inside a Git repository, so there is nothing to bind %s to", *directory, productID))
		}
		repository = found
	} else {
		resolved, err := loadConfiguration(*configPath)
		if err != nil {
			return fail(err)
		}
		productID = string(resolved.Config.Product.ID)
		remote = resolved.Config.Execution.Remote
		repository, err = resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
		if err != nil {
			return fail(fmt.Errorf("resolve product repository: %w", err))
		}
	}

	root, err := agreedHome(repository)
	if err != nil {
		return fail(err)
	}
	common, err := home.CommonGitDirectory(repository)
	if err != nil {
		return fail(err)
	}
	if common == "" {
		return fail(fmt.Errorf("%s is not a Git checkout, so there is no repository to bind %s to", repository, productID))
	}
	before, found, err := home.ReadBinding(root.Path, productID)
	if err != nil {
		return fail(err)
	}
	from := ""
	if found {
		from = before.Repository
		if home.SameDirectory(before.GitCommonDirectory, common) {
			return reportBinding(stdout, stderr, *jsonOutput, productID, before, false,
				fmt.Sprintf("project %s is already bound to %s; nothing to bind", productID, before.Repository))
		}
		if before.Present() && !*replace {
			return fail(fmt.Errorf("project %s is bound to %s, which is still there; binding %s instead would point the project's records at another tree, "+
				"so this refuses. Run `%s --replace` to unbind %s and bind this repository",
				productID, before.Repository, home.RepositoryOf(common), home.BindCommand, before.Repository))
		}
	}
	if err := refuseRunsInFlight(root.Path, productID, "bind"); err != nil {
		return fail(err)
	}
	binding, err := home.Bind(home.AgreeOptions{Root: root.Path, ProductID: productID, Checkout: repository, Remote: remote})
	if err != nil {
		return fail(err)
	}
	if err := recordProjectAct(root.Path, productID, projectAct{Act: "bind", From: from, To: binding.Repository}); err != nil {
		return fail(err)
	}
	said := fmt.Sprintf("bound project %s to %s, recorded in %s", productID, binding.Repository, home.BindingPath(root.Path, productID))
	if from != "" {
		said = fmt.Sprintf("bound project %s to %s in place of %s, recorded in %s", productID, binding.Repository, from, home.BindingPath(root.Path, productID))
	}
	return reportBinding(stdout, stderr, *jsonOutput, productID, binding, true, said)
}

func reportBinding(stdout, stderr io.Writer, jsonOutput bool, productID string, binding home.Binding, changed bool, said string) int {
	if jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"product":    productID,
			"repository": binding.Repository,
			"binding":    binding,
			"changed":    changed,
			"says":       said,
		})
	}
	fmt.Fprintln(stdout, said)
	return 0
}

// refuseRunsInFlight refuses an act on a project while any of its runs is in
// flight: a run holds a worktree of the bound repository and records under the
// project's directory, and neither may move under it.
func refuseRunsInFlight(root, productID, act string) error {
	store, err := runstate.NewStore(root, domain.ProductID(productID))
	if err != nil {
		return err
	}
	runs, err := store.Incomplete()
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return nil
	}
	named := make([]string, 0, len(runs))
	for _, run := range runs {
		named = append(named, fmt.Sprintf("%s (%s)", run.RunID, run.WorkItemID))
	}
	return fmt.Errorf("project %s has %d run(s) in flight, %s; %s refuses until they have ended, or `yoyo stop` has stopped them",
		productID, len(runs), strings.Join(named, ", "), act)
}

func recordProjectAct(root, productID string, act projectAct) error {
	if act.At.IsZero() {
		act.At = time.Now().UTC().Truncate(time.Second)
	}
	if act.By == "" {
		act.By = home.ProcessAccount()
	}
	line, err := json.Marshal(act)
	if err != nil {
		return err
	}
	writer, err := repowrite.NewRoot(root)
	if err != nil {
		return fmt.Errorf("record the %s of %s: %w", act.Act, productID, err)
	}
	relative := path.Join(home.ProductDirectoryWithin(root, productID), projectActsFileName)
	file, err := writer.OpenAppend(relative, 0o600, 0o700)
	if err != nil {
		return fmt.Errorf("record the %s of %s: %w", act.Act, productID, err)
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("record the %s of %s: %w", act.Act, productID, err)
	}
	return file.Close()
}

// renameProject moves a project directory, and everything under it, to a new
// id. The id in the configuration and the directory never disagree: a
// configuration kept in the project directory has its product.id rewritten, and
// a committed one has to read the new id already.
func renameProject(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("project rename", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 2 {
		fmt.Fprintln(stderr, "project rename takes the old id and the new one: yoyo project rename <old> <new>")
		return 2
	}
	fail := func(err error) int { return commandFailure(stdout, stderr, *jsonOutput, err) }
	oldID, newID := positional[0], positional[1]
	for _, id := range []string{oldID, newID} {
		if err := domain.ValidateIdentifier("product id", id); err != nil {
			return fail(err)
		}
	}
	if oldID == newID {
		return fail(fmt.Errorf("project %s is already called %s; nothing to rename", oldID, newID))
	}
	root, err := runstate.ResolveRoot(os.Getenv, os.UserHomeDir, runtime.GOOS)
	if err != nil {
		return fail(err)
	}
	renamed, err := renameProjectDirectory(root.Path, oldID, newID)
	if err != nil {
		return fail(err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"from":          oldID,
			"to":            newID,
			"directory":     renamed.directory,
			"configuration": renamed.configuration,
			"rewritten":     renamed.rewritten,
			"says":          renamed.says,
		})
	}
	fmt.Fprintln(stdout, renamed.says)
	return 0
}

type renamedProject struct {
	directory     string
	configuration string
	rewritten     bool
	says          string
}

func renameProjectDirectory(root, oldID, newID string) (renamedProject, error) {
	if home.EarlierLayout(root) {
		return renamedProject{}, fmt.Errorf("the home %s is still laid out the way earlier builds kept it, with each product's records under products/, "+
			"and a rename would leave them there under the old id; rename once the home has been migrated", root)
	}
	source := home.ProjectDirectory(root, oldID)
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		return renamedProject{}, fmt.Errorf("there is no project %s under %s; `%s` names the projects there", oldID, root, home.ListCommand)
	}
	binding, bound, err := home.ReadBinding(root, oldID)
	if err != nil {
		return renamedProject{}, err
	}
	if err := refuseTakenProject(root, newID, binding, bound); err != nil {
		return renamedProject{}, err
	}
	if err := refuseRunsInFlight(root, oldID, "rename"); err != nil {
		return renamedProject{}, err
	}
	// A preserved worktree is registered with the bound repository at its path,
	// and moving the directory would leave every one of them registered at a path
	// that is gone.
	worktrees := filepath.Join(source, home.WorktreesDirectoryName)
	if entries, err := os.ReadDir(worktrees); err == nil && len(entries) > 0 {
		return renamedProject{}, fmt.Errorf("project %s still holds %d worktree(s) under %s, which the repository has registered at those paths; "+
			"a rename would leave them registered where nothing is, so it refuses until they are retired (`yoyo reconcile` sweeps the ones nothing needs)",
			oldID, len(entries), worktrees)
	}

	// Where the configuration is decides what makes the id agree: one kept in the
	// project directory is rewritten, and a committed one must already say it.
	result := renamedProject{directory: home.ProjectDirectory(root, newID)}
	kept := filepath.Join(source, home.ConfigFileName)
	var rewritten []byte
	if content, err := os.ReadFile(kept); err == nil {
		rewritten, err = rewriteProductID(content, oldID, newID)
		if err != nil {
			return renamedProject{}, fmt.Errorf("rewrite product.id in %s: %w", kept, err)
		}
		result.configuration = filepath.Join(result.directory, home.ConfigFileName)
		result.rewritten = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return renamedProject{}, fmt.Errorf("read %s: %w", kept, err)
	} else if bound {
		committed, err := config.Discover(binding.Repository)
		if err != nil {
			return renamedProject{}, fmt.Errorf("the configuration of %s could not be found to check its product.id: %w", binding.Repository, err)
		}
		loaded, err := config.Load(committed)
		if err != nil {
			return renamedProject{}, err
		}
		if string(loaded.Product.ID) != newID {
			return renamedProject{}, fmt.Errorf("the configuration committed at %s says product.id is %s; set it to %s and commit it first, "+
				"so the id in the repository and the project directory never disagree", committed, loaded.Product.ID, newID)
		}
		result.configuration = committed
	}

	writer, err := repowrite.NewRoot(root)
	if err != nil {
		return renamedProject{}, err
	}
	// A project directory a start made for the new id after its configuration
	// was changed holds only a binding to this same repository, and is taken
	// back out of the way of the one being renamed onto it.
	if _, err := os.Stat(result.directory); err == nil {
		if _, err := writer.RemoveDirectory(path.Join(home.ProjectsDirectoryName, newID)); err != nil {
			return renamedProject{}, err
		}
	}
	if _, err := writer.RenameDirectory(path.Join(home.ProjectsDirectoryName, oldID), path.Join(home.ProjectsDirectoryName, newID)); err != nil {
		return renamedProject{}, err
	}
	if result.rewritten {
		if _, err := writer.WriteFile(path.Join(home.ProjectsDirectoryName, newID, home.ConfigFileName), rewritten); err != nil {
			return renamedProject{}, fmt.Errorf("the project moved to %s, and rewriting product.id in its configuration failed: %w", result.directory, err)
		}
	}
	if err := recordProjectAct(root, newID, projectAct{Act: "rename", From: oldID, To: newID}); err != nil {
		return renamedProject{}, err
	}
	result.says = fmt.Sprintf("renamed project %s to %s, now at %s", oldID, newID, result.directory)
	if result.rewritten {
		result.says += fmt.Sprintf("; product.id in %s now reads %s", result.configuration, newID)
	}
	return result, nil
}

// refuseTakenProject refuses a rename onto an id another project already has.
// The one directory it lets through is what a start leaves when a committed
// configuration was changed to the new id before the rename: a binding to the
// same repository and nothing of substance beside it.
func refuseTakenProject(root, newID string, binding home.Binding, bound bool) error {
	target := home.ProjectDirectory(root, newID)
	entries, err := os.ReadDir(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", target, err)
	}
	taken := fmt.Errorf("the id %s is taken: %s already exists; `%s` names the projects there", newID, target, home.ListCommand)
	existing, found, err := home.ReadBinding(root, newID)
	if err != nil || !found || !bound || !home.SameDirectory(existing.GitCommonDirectory, binding.GitCommonDirectory) {
		return taken
	}
	for _, entry := range entries {
		switch entry.Name() {
		case home.BindingFileName:
		case home.StateDirectoryName:
			if !onlyActs(filepath.Join(target, home.StateDirectoryName)) {
				return taken
			}
		default:
			return taken
		}
	}
	return nil
}

// onlyActs reports whether a project's state directory holds nothing but the
// record of acts, or nothing at all.
func onlyActs(directory string) bool {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() != projectActsFileName {
			return false
		}
	}
	return true
}

// rewriteProductID rewrites the `id` under `product` in a configuration,
// leaving every other line, comments included, exactly as it was.
func rewriteProductID(content []byte, oldID, newID string) ([]byte, error) {
	lines := strings.SplitAfter(string(content), "\n")
	inProduct := false
	for index, line := range lines {
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" || strings.HasPrefix(strings.TrimSpace(trimmed), "#") {
			continue
		}
		indented := strings.HasPrefix(trimmed, " ") || strings.HasPrefix(trimmed, "\t")
		if !indented {
			inProduct = strings.TrimSpace(strings.SplitN(trimmed, "#", 2)[0]) == "product:"
			continue
		}
		if !inProduct {
			continue
		}
		key, value, found := strings.Cut(strings.TrimSpace(trimmed), ":")
		if !found || key != "id" {
			continue
		}
		value = strings.TrimSpace(strings.SplitN(value, "#", 2)[0])
		if strings.Trim(value, `"'`) != oldID {
			return nil, fmt.Errorf("product.id reads %s rather than %s", value, oldID)
		}
		lines[index] = strings.Replace(line, value, newID, 1)
		return []byte(strings.Join(lines, "")), nil
	}
	return nil, errors.New("it has no product.id to rewrite")
}

// listProjects names every project directory in the home, its binding, and
// whether the bound repository is still there.
func listProjects(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("project list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "project list does not accept positional arguments")
		return 2
	}
	root, err := runstate.ResolveRoot(os.Getenv, os.UserHomeDir, runtime.GOOS)
	if err != nil {
		return commandFailure(stdout, stderr, *jsonOutput, err)
	}
	projects, err := home.Projects(root.Path)
	if err != nil {
		return commandFailure(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		type listed struct {
			ID            string        `json:"id"`
			Directory     string        `json:"directory"`
			Bound         bool          `json:"bound"`
			Binding       *home.Binding `json:"binding,omitempty"`
			Present       bool          `json:"present"`
			Configuration string        `json:"configuration,omitempty"`
			Earlier       bool          `json:"earlier_layout,omitempty"`
			Problem       string        `json:"problem,omitempty"`
		}
		out := make([]listed, 0, len(projects))
		for _, project := range projects {
			entry := listed{ID: project.ID, Directory: project.Directory, Bound: project.Bound,
				Configuration: project.Configuration, Earlier: project.Earlier, Problem: project.BindingProblem}
			if project.Bound {
				binding := project.Binding
				entry.Binding = &binding
				entry.Present = binding.Present()
			}
			out = append(out, entry)
		}
		return writeJSON(stdout, stderr, map[string]any{"home": root.Path, "origin": root.Origin, "projects": out})
	}
	fmt.Fprintf(stdout, "home: %s (from %s)\n", root.Path, root.Origin)
	if len(projects) == 0 {
		fmt.Fprintln(stdout, "no projects")
		return 0
	}
	for _, project := range projects {
		fmt.Fprintln(stdout, describeListedProject(project))
	}
	return 0
}

func describeListedProject(project home.Project) string {
	var line string
	switch {
	case project.BindingProblem != "":
		line = fmt.Sprintf("%s: the binding could not be read: %s", project.ID, project.BindingProblem)
	case project.Earlier:
		line = fmt.Sprintf("%s: kept the earlier way, under products/, and bound to no repository yet", project.ID)
	case !project.Bound:
		line = fmt.Sprintf("%s: bound to no repository", project.ID)
	case project.Binding.Present():
		line = fmt.Sprintf("%s: bound to %s, which is there", project.ID, project.Binding.Repository)
	default:
		line = fmt.Sprintf("%s: bound to %s, which is missing; `%s` from where it now is rebinds it", project.ID, project.Binding.Repository, home.BindCommand)
	}
	if project.Configuration != "" {
		line += "; configuration kept at " + project.Configuration
	}
	return line
}

func printProjectUsage(writer io.Writer) {
	fmt.Fprintln(writer, strings.TrimSpace(`
Usage: yoyo project <bind|rename|list> [options]

Each project on this machine has a directory in the machine home, named by its
product id and bound to one repository by that repository's Git common
directory. A start from a second clone, from another product using the same id,
or against a bound repository that is gone refuses and names one of these.

  bind              bind this repository to the project directory for its id
  rename <old> <new>
                    move a project directory, and everything under it, to a new id
  list              name every project directory, its binding, and whether the
                    bound repository is there

bind options:
  --config <path>   configuration file (default: the nearest project configuration)
  --product <id>    the id to bind, where no configuration is found for this repository
  --directory <dir> a directory in the repository to bind, with --product (default: .)
  --replace         unbind the repository bound now although it is still there
  --json            emit machine-readable JSON

bind refuses while a run is in flight for the project. rename refuses while a
run is in flight, while the project holds worktrees, or where the new id is
taken; a configuration kept in the project directory has its product.id
rewritten, and a committed one must already read the new id.`))
}
