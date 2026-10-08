package home

// The migration's half of the machine home: where earlier builds kept what this
// one keeps in the home, and the moves that bring it there. `yoyo home migrate`
// is the one thing that makes them, and it makes them only once it has found
// nothing in flight, which run state decides because only it can read the runs;
// this file moves files and knows nothing about what is in them beyond a
// configuration's product id. docs/designs/machine-home.md is the design.
//
// It is also the one place allowed to name the configurations home earlier
// builds kept at ~/.config/yoyodyne, which the configuration package's
// TestNothingButTheMigrationNamesTheEarlierConfigurationsHome holds to: the
// migration reads it, and so does the machine file's fallback below until the
// migration has moved the file out of it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"go.yaml.in/yaml/v3"
)

// MigrateCommand is the one command that moves a home laid out the earlier way
// into the machine home.
const MigrateCommand = "yoyo home migrate"

// earlierConfigurationHomeVariable moved the configurations home earlier builds
// kept, as XDG_CONFIG_HOME did; both are honoured where the migration reads it,
// because whoever set one put the files there.
const earlierConfigurationHomeVariable = "YOYODYNE_CONFIG_HOME"

// EarlierConfigurationHome is where earlier builds kept a machine's settings
// file and each repository's external configuration: YOYODYNE_CONFIG_HOME, then
// XDG_CONFIG_HOME/yoyodyne, then ~/.config/yoyodyne.
func EarlierConfigurationHome(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	if value := strings.TrimSpace(getenv(earlierConfigurationHomeVariable)); value != "" {
		if !filepath.IsAbs(value) {
			return "", fmt.Errorf("%s must be an absolute path and is %q", earlierConfigurationHomeVariable, value)
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

// EarlierMachinePath is where earlier builds kept the machine file, whether or
// not it is there.
func EarlierMachinePath(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	configurations, err := EarlierConfigurationHome(getenv, userHomeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(configurations, MachineFileName), nil
}

// MovedMarkerName is the file the migration leaves in the home it emptied,
// naming the home the state went to. Run state's marker check reads it, so a
// checkout whose state-root marker names the earlier home follows the state to
// where it went rather than refusing every command over a split nothing made.
const MovedMarkerName = "moved-to-machine-home"

// MovedTo is the home a migrated home's state went to, read off the marker the
// migration left in it, or nothing where there is no marker.
func MovedTo(root string) (string, bool) {
	content, err := os.ReadFile(filepath.Join(root, MovedMarkerName))
	if err != nil {
		return "", false
	}
	first, _, _ := strings.Cut(string(content), "\n")
	moved := strings.TrimSpace(first)
	if moved == "" || !filepath.IsAbs(moved) {
		return "", false
	}
	return filepath.Clean(moved), true
}

// StillEarlier is the sentence a process says while the home it resolved is
// still laid out the earlier way, or nothing where it is not. A home the
// variable named is the operator's explicit choice for that shell, which is how
// the migration is deferred, so it is not said there.
func StillEarlier(resolved Resolved) (string, bool) {
	if resolved.Origin == OriginEnvironment || !EarlierLayout(resolved.Path) {
		return "", false
	}
	return fmt.Sprintf("the harness's state is still in %s, laid out the way earlier builds kept it, and this build moves nothing on its own; "+
		"`%s` moves it into the machine home once nothing is in flight", resolved.Path, MigrateCommand), true
}

// Moved is one thing the migration moved: what it is, in words, and both ends.
type Moved struct {
	What string `json:"what"`
	From string `json:"from"`
	To   string `json:"to"`
}

// LeftBehind is one thing the migration did not move, and why. Failed is a move
// that was attempted and did not happen, which a second run of the migration
// attempts again; the rest are left on purpose.
type LeftBehind struct {
	What   string `json:"what"`
	Path   string `json:"path"`
	Why    string `json:"why"`
	Failed bool   `json:"failed,omitempty"`
}

// MigrateOptions is what one migration moves, from where to where.
type MigrateOptions struct {
	// From is the home the state is in now, and To the machine home it moves
	// into. They are the same directory where the home is not the platform's
	// earlier default — the machine file or XDG_STATE_HOME named it — and then
	// it is laid out the new way where it stands.
	From string
	To   string
	// ConfigurationHome is the configurations home earlier builds kept, whose
	// external configurations move into their project directories.
	ConfigurationHome string
	// MachineFrom and MachineTo are the machine file's earlier place and its
	// place in the machine home.
	MachineFrom string
	MachineTo   string
	// Checkouts names, by product id, a checkout of the repository a product's
	// state belongs to, where the caller knows one: the checkout the command
	// was run from. The binding of every other product is written from a
	// checkout its records name — a preserved worktree, or the repository an
	// external configuration names — or at its first start.
	Checkouts map[string]string
	Now       func() time.Time
	BoundBy   string
}

// Migration is what one migration did.
type Migration struct {
	From       string       `json:"from"`
	To         string       `json:"to"`
	Moved      []Moved      `json:"moved"`
	LeftBehind []LeftBehind `json:"left_behind"`
	// Products are the products whose records are under To once it is done.
	Products []string `json:"moved_products"`
	// Bound says each binding the migration wrote, in the sentence a first
	// start says.
	Bound []string `json:"bound,omitempty"`
}

// Failed reports a migration that attempted a move and did not make it.
func (m Migration) Failed() bool {
	for _, left := range m.LeftBehind {
		if left.Failed {
			return true
		}
	}
	return false
}

// homeDirectoryMode is what the home and the directories the migration creates
// in it are given: the records are this user's and nobody else's.
const homeDirectoryMode = 0o700

// ConflictError is a migration refused before anything moved, because a
// product's records would land on records already in the machine home.
type ConflictError struct {
	Conflicts []string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("nothing was moved, because the machine home already holds records where these would go: %s; "+
		"a home that has been used since it was created is not one the migration merges into, so move those records aside first",
		strings.Join(e.Conflicts, "; "))
}

// MoveHome makes the migration's moves, in the design's order: the home
// created, each product's records into its project directory, each external
// configuration into the project directory its product id names, the worktrees
// under their project with Git told where each now is, the machine-wide records
// to the top, the machine file into the home, and a marker left in the emptied
// home naming the new one. Writing each binding comes after the records it
// binds. Run records naming a worktree path are run state's to rewrite, from
// MigratedWorktreePath, once this returns.
//
// Every step is one rename that is made or not, so a migration that fails part
// way leaves each thing either moved or where it was, says which, and is safe
// to run again. What it refuses before moving anything is a product whose
// records would land on records already there.
func MoveHome(options MigrateOptions) (Migration, error) {
	from, to := filepath.Clean(options.From), filepath.Clean(options.To)
	migration := Migration{From: from, To: to, Moved: []Moved{}, LeftBehind: []LeftBehind{}, Products: []string{}}
	inPlace := SameDirectory(from, to)
	products, err := earlierProducts(from)
	if err != nil {
		return migration, err
	}
	var conflicts []string
	for _, id := range products {
		if destination := filepath.Join(ProjectDirectory(to, id), StateDirectoryName); exists(destination) {
			conflicts = append(conflicts, fmt.Sprintf("the records of %s would go to %s, which exists", id, destination))
		}
	}
	if len(conflicts) > 0 {
		return migration, &ConflictError{Conflicts: conflicts}
	}
	if err := os.MkdirAll(to, homeDirectoryMode); err != nil {
		return migration, fmt.Errorf("create the machine home %s: %w", to, err)
	}
	fromRoot, err := repowrite.NewRoot(from)
	if err != nil {
		return migration, fmt.Errorf("open the earlier home %s: %w", from, err)
	}
	toRoot, err := repowrite.NewRoot(to)
	if err != nil {
		return migration, fmt.Errorf("open the machine home %s: %w", to, err)
	}
	move := func(what string, source repowrite.Root, sourcePath string, target repowrite.Root, targetPath string) bool {
		landed, err := repowrite.Move(source, sourcePath, target, targetPath, homeDirectoryMode)
		if err != nil {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: what, Path: filepath.Join(source.Path(), filepath.FromSlash(sourcePath)),
				Why: err.Error(), Failed: true})
			return false
		}
		migration.Moved = append(migration.Moved, Moved{What: what, From: filepath.Join(source.Path(), filepath.FromSlash(sourcePath)), To: landed})
		return true
	}

	// Each product's records.
	for _, id := range products {
		if move("the records of "+id, fromRoot, path.Join(earlierProductsDirectoryName, id),
			toRoot, path.Join(ProjectsDirectoryName, id, StateDirectoryName)) {
			migration.Products = append(migration.Products, id)
		}
	}
	removeIfEmpty(fromRoot, earlierProductsDirectoryName)

	// Each external configuration, under the id it names.
	checkouts := map[string]string{}
	for id, checkout := range options.Checkouts {
		checkouts[id] = checkout
	}
	if configurations := strings.TrimSpace(options.ConfigurationHome); configurations != "" {
		moveExternalConfigurations(&migration, configurations, toRoot, checkouts, move)
	}

	// The worktrees, under their project, each re-registered with its repository
	// at the path it now has.
	moveWorktrees(&migration, fromRoot, toRoot, checkouts, move)

	// The machine-wide records, which are already at the top of a home laid out
	// in place.
	if !inPlace {
		for _, name := range []string{OperatorHoldFileName, AccountsDirectoryName, LogsDirectoryName} {
			if !exists(filepath.Join(from, name)) {
				continue
			}
			move(machineWideWhat(name), fromRoot, name, toRoot, name)
		}
	}

	// The machine file.
	if machineFrom := strings.TrimSpace(options.MachineFrom); machineFrom != "" && exists(machineFrom) {
		machineTo := filepath.Clean(options.MachineTo)
		if exists(machineTo) {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the earlier machine file", Path: machineFrom,
				Why: fmt.Sprintf("%s already exists and is the one read, so the earlier file is left for you to compare and remove", machineTo)})
		} else if sourceRoot, err := repowrite.NewRoot(filepath.Dir(machineFrom)); err != nil {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the machine file", Path: machineFrom, Why: err.Error(), Failed: true})
		} else if targetRoot, err := ensureRoot(filepath.Dir(machineTo)); err != nil {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the machine file", Path: machineFrom, Why: err.Error(), Failed: true})
		} else {
			move("the machine file", sourceRoot, filepath.Base(machineFrom), targetRoot, filepath.Base(machineTo))
		}
	}
	if configurations := strings.TrimSpace(options.ConfigurationHome); configurations != "" {
		if root, err := repowrite.NewRoot(configurations); err == nil {
			removeIfEmpty(root, ProjectsDirectoryName)
		}
		removeEmptyDirectory(configurations)
	}

	// The bindings, from the checkouts the records name: every product whose
	// records moved, and every one whose external configuration did, which is
	// found by its binding and by nothing else.
	toBind := append([]string(nil), migration.Products...)
	for id := range checkouts {
		if !contains(toBind, id) && exists(ProjectDirectory(to, id)) {
			toBind = append(toBind, id)
		}
	}
	sort.Strings(toBind)
	for _, id := range toBind {
		checkout, known := checkouts[id]
		if !known {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the binding of " + id, Path: BindingPath(to, id),
				Why: fmt.Sprintf("nothing moved names a checkout of %s's repository, so its first start from that repository binds it and says so", id)})
			continue
		}
		agreement, err := Agree(AgreeOptions{Root: to, ProductID: id, Checkout: checkout, Now: options.Now, BoundBy: options.BoundBy})
		if err != nil {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the binding of " + id, Path: BindingPath(to, id), Why: err.Error(), Failed: true})
			continue
		}
		if agreement.Bound {
			migration.Bound = append(migration.Bound, agreement.Says(id))
		}
	}

	// What is left in the earlier home is named rather than moved: it is not a
	// record any product or the machine keeps.
	if !inPlace {
		if entries, err := os.ReadDir(from); err == nil {
			for _, entry := range entries {
				if entry.Name() == MovedMarkerName || leftAlready(migration, filepath.Join(from, entry.Name())) {
					continue
				}
				migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: entry.Name(), Path: filepath.Join(from, entry.Name()),
					Why: "it is not a record the harness keeps for a product or for the machine, so it is yours to keep or remove"})
			}
		}
		marker := fmt.Sprintf("%s\nThe harness's state was moved from here to %s by `%s`. Nothing reads this directory any more.\n", to, to, MigrateCommand)
		if _, err := fromRoot.WriteFile(MovedMarkerName, []byte(marker)); err != nil {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the marker naming the machine home", Path: filepath.Join(from, MovedMarkerName),
				Why: err.Error(), Failed: true})
		}
	}
	sort.Strings(migration.Products)
	return migration, nil
}

// ProductIDs lists the products a home keeps records for, in whichever layout
// it is in, by the names of their directories.
func ProductIDs(root string) ([]string, error) {
	if EarlierLayout(root) {
		return earlierProducts(root)
	}
	return productDirectories(filepath.Join(root, ProjectsDirectoryName))
}

// earlierProducts lists the products a home laid out the earlier way keeps
// records for, by the names of their directories under products/.
func earlierProducts(root string) ([]string, error) {
	return productDirectories(filepath.Join(root, earlierProductsDirectoryName))
}

func productDirectories(listing string) ([]string, error) {
	entries, err := os.ReadDir(listing)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the products in %s: %w", listing, err)
	}
	var products []string
	for _, entry := range entries {
		if entry.IsDir() && domain.ValidateIdentifier("product id", entry.Name()) == nil {
			products = append(products, entry.Name())
		}
	}
	sort.Strings(products)
	return products, nil
}

// moveExternalConfigurations moves each repository's directory in the earlier
// configurations home into the project directory of the product id its
// configuration names, entry by entry, so personas kept beside a configuration
// stay beside it. A configuration naming its repository outright, as `yoyo init
// --external` wrote one, names the checkout its binding is written from.
func moveExternalConfigurations(migration *Migration, configurations string, toRoot repowrite.Root, checkouts map[string]string,
	move func(string, repowrite.Root, string, repowrite.Root, string) bool) {
	keys, err := os.ReadDir(filepath.Join(configurations, ProjectsDirectoryName))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the external configurations", Path: configurations, Why: err.Error(), Failed: true})
		}
		return
	}
	root, err := repowrite.NewRoot(configurations)
	if err != nil {
		migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the external configurations", Path: configurations, Why: err.Error(), Failed: true})
		return
	}
	for _, key := range keys {
		if !key.IsDir() {
			continue
		}
		directory := filepath.Join(configurations, ProjectsDirectoryName, key.Name())
		id, repository, err := externalProductID(filepath.Join(directory, ConfigFileName))
		if err != nil {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the external configuration " + key.Name(), Path: directory,
				Why: "its product id could not be read, so there is no project directory to move it into: " + err.Error()})
			continue
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the external configuration of " + id, Path: directory, Why: err.Error(), Failed: true})
			continue
		}
		for _, entry := range entries {
			what := "the external configuration of " + id
			if entry.Name() != ConfigFileName {
				what = fmt.Sprintf("%s, kept beside the external configuration of %s", entry.Name(), id)
			}
			move(what, root, path.Join(ProjectsDirectoryName, key.Name(), entry.Name()), toRoot, path.Join(ProjectsDirectoryName, id, entry.Name()))
		}
		removeIfEmpty(root, path.Join(ProjectsDirectoryName, key.Name()))
		if _, known := checkouts[id]; !known && filepath.IsAbs(repository) {
			if common, err := CommonGitDirectory(repository); err == nil && common != "" {
				checkouts[id] = repository
			}
		}
	}
}

// externalProductID reads the product id, and the repository where it is named
// outright, from an external configuration. Nothing else in it is read: the
// migration moves the file, and loading it is the configuration package's.
func externalProductID(configuration string) (string, string, error) {
	content, err := os.ReadFile(configuration)
	if err != nil {
		return "", "", err
	}
	var document struct {
		Product struct {
			ID         string `yaml:"id"`
			Repository string `yaml:"repository"`
		} `yaml:"product"`
	}
	if err := yaml.Unmarshal(content, &document); err != nil {
		return "", "", err
	}
	id := strings.TrimSpace(document.Product.ID)
	if err := domain.ValidateIdentifier("product id", id); err != nil {
		return "", "", err
	}
	return id, strings.TrimSpace(document.Product.Repository), nil
}

// moveWorktrees moves every worktree the earlier layout cut under
// worktrees/<product id>/<repository id>/ to its project's worktrees/, and asks
// Git, from inside each, to record the path it now has: a linked worktree that
// moved still names its repository, and `git worktree repair` run in it rewrites
// the repository's record of where it is. The first worktree of a product names
// the checkout its binding is written from, where nothing else did.
func moveWorktrees(migration *Migration, fromRoot, toRoot repowrite.Root, checkouts map[string]string,
	move func(string, repowrite.Root, string, repowrite.Root, string) bool) {
	from := fromRoot.Path()
	products, err := os.ReadDir(filepath.Join(from, earlierWorktreesDirectoryName))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the worktrees", Path: filepath.Join(from, earlierWorktreesDirectoryName),
				Why: err.Error(), Failed: true})
		}
		return
	}
	for _, product := range products {
		id := product.Name()
		if !product.IsDir() || domain.ValidateIdentifier("product id", id) != nil {
			continue
		}
		repositories, err := os.ReadDir(filepath.Join(from, earlierWorktreesDirectoryName, id))
		if err != nil {
			migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the worktrees of " + id,
				Path: filepath.Join(from, earlierWorktreesDirectoryName, id), Why: err.Error(), Failed: true})
			continue
		}
		for _, repository := range repositories {
			if !repository.IsDir() {
				continue
			}
			within := path.Join(earlierWorktreesDirectoryName, id, repository.Name())
			worktrees, err := os.ReadDir(filepath.Join(from, filepath.FromSlash(within)))
			if err != nil {
				migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the worktrees of " + id,
					Path: filepath.Join(from, filepath.FromSlash(within)), Why: err.Error(), Failed: true})
				continue
			}
			for _, worktree := range worktrees {
				what := fmt.Sprintf("the worktree %s of %s", worktree.Name(), id)
				if !move(what, fromRoot, path.Join(within, worktree.Name()), toRoot, path.Join(ProjectsDirectoryName, id, WorktreesDirectoryName, worktree.Name())) {
					continue
				}
				moved := filepath.Join(ProjectDirectory(toRoot.Path(), id), WorktreesDirectoryName, worktree.Name())
				if !isLinkedWorktree(moved) {
					continue
				}
				if err := repairWorktree(moved); err != nil {
					migration.LeftBehind = append(migration.LeftBehind, LeftBehind{What: "the repository's record of " + what, Path: moved,
						Why:    fmt.Sprintf("the worktree moved and Git could not be told where it is now (%v); `git worktree repair %s` run from its repository records it", err, moved),
						Failed: true})
					continue
				}
				if _, known := checkouts[id]; !known {
					checkouts[id] = moved
				}
			}
			removeIfEmpty(fromRoot, within)
		}
		removeIfEmpty(fromRoot, path.Join(earlierWorktreesDirectoryName, id))
	}
	// worktrees/ is the earlier layout's alone, in a home laid out in place as
	// in the one emptied; the machine home keeps worktrees under each project.
	removeIfEmpty(fromRoot, earlierWorktreesDirectoryName)
}

// MigratedWorktreePath is where a worktree path a run recorded in the earlier
// layout is once the migration has moved it — `worktrees/<id>/<repository
// id>/<name>` under from is `projects/<id>/worktrees/<name>` under to — and
// whether the recorded path is one the migration moves at all. It is read off
// the paths rather than off a record of the moves, so rewriting a run record is
// as safe to repeat as the move was.
func MigratedWorktreePath(from, to, productID, recorded string) (string, bool) {
	recorded = filepath.Clean(strings.TrimSpace(recorded))
	if recorded == "." || !filepath.IsAbs(recorded) {
		return "", false
	}
	for _, base := range []string{filepath.Clean(from), resolvedOrSelf(from)} {
		relative, err := filepath.Rel(filepath.Join(base, earlierWorktreesDirectoryName, productID), recorded)
		if err != nil {
			continue
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) != 2 || parts[0] == ".." || parts[0] == "." || parts[1] == ".." {
			continue
		}
		return filepath.Join(ProjectDirectory(to, productID), WorktreesDirectoryName, parts[1]), true
	}
	return "", false
}

func resolvedOrSelf(value string) string {
	if resolved, err := filepath.EvalSymlinks(value); err == nil {
		return resolved
	}
	return filepath.Clean(value)
}

// isLinkedWorktree reports a directory whose .git is a file naming the
// repository it belongs to, which is every worktree the harness cuts.
func isLinkedWorktree(directory string) bool {
	info, err := os.Lstat(filepath.Join(directory, ".git"))
	return err == nil && info.Mode().IsRegular()
}

// repairWorktree tells the repository a linked worktree belongs to where the
// worktree now is.
func repairWorktree(worktree string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "-C", worktree, "worktree", "repair").CombinedOutput()
	if err != nil {
		if said := strings.TrimSpace(string(output)); said != "" {
			return fmt.Errorf("%w: %s", err, said)
		}
		return err
	}
	return nil
}

func machineWideWhat(name string) string {
	switch name {
	case OperatorHoldFileName:
		return "the operator's hold"
	case AccountsDirectoryName:
		return "the provider accounts"
	default:
		return "the harness's logs"
	}
}

// leftAlready reports a path the migration has already named as left behind.
func leftAlready(migration Migration, target string) bool {
	for _, left := range migration.LeftBehind {
		if left.Path == target {
			return true
		}
	}
	return false
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func exists(target string) bool {
	_, err := os.Lstat(target)
	return err == nil
}

// ensureRoot is a directory to write through, created where it is missing.
func ensureRoot(directory string) (repowrite.Root, error) {
	if err := os.MkdirAll(directory, homeDirectoryMode); err != nil {
		return repowrite.Root{}, fmt.Errorf("create %s: %w", directory, err)
	}
	return repowrite.NewRoot(directory)
}

// removeIfEmpty removes a directory the migration emptied, and leaves one that
// still holds anything.
func removeIfEmpty(root repowrite.Root, relative string) {
	entries, err := os.ReadDir(filepath.Join(root.Path(), filepath.FromSlash(relative)))
	if err != nil || len(entries) > 0 {
		return
	}
	_, _ = root.RemoveDirectory(relative)
}

// removeEmptyDirectory removes the configurations home itself once nothing is
// left in it, through the directory that holds it.
func removeEmptyDirectory(directory string) {
	parent, err := repowrite.NewRoot(filepath.Dir(directory))
	if err != nil {
		return
	}
	removeIfEmpty(parent, filepath.Base(directory))
}
