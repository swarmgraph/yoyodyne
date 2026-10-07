package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// runInit writes a project its own complete configuration. The built-in bundle
// is the template it is generated from rather than a layer it keeps inheriting,
// so what the project can read is all of what runs.
func runInit(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("directory", ".", "project directory to configure")
	product := flags.String("product", "", "product id (default: the project directory name)")
	trackerRemote := flags.String("tracker-remote", "", "URL the tracker syncs through (default: this project's Git remote)")
	force := flags.Bool("force", false, "overwrite files that already exist")
	external := flags.Bool("external", false, "write the configuration outside the repository, where only this machine reads it")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "init does not accept positional arguments")
		return 2
	}

	initialization, err := initializeProject(initializeOptions{
		Directory: *directory,
		ProductID: *product,
		Force:     *force,
		External:  *external,
	})
	if err != nil {
		// A reported failure exits nonzero whichever form it was reported in,
		// so a script reading the JSON does not have to parse it to notice.
		if *jsonOutput {
			if code := writeJSON(stdout, stderr, map[string]any{"status": "failed", "error": err.Error()}); code != 0 {
				return code
			}
		} else {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}

	tracker := configureTrackerRemote(ctx, initialization.repository, *trackerRemote, execution.OSProcessRunner{})
	// A configuration the repository ignores is one this machine has and no
	// other will, and the moment it was written is the moment somebody can still
	// decide about it cheaply. An external configuration is not in the repository
	// for a rule to reach, and answers no without Git being asked.
	ignored := doctor.ConfigurationIgnored(ctx, execution.OSProcessRunner{}, initialization.repository, initialization.config)

	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"status": "written",
			"bundle": config.BuiltinV1,
			"config": initialization.config,
			"files":  initialization.written,
			// Where the configuration went, and what it configures, are reported
			// apart because an external one is the case where they differ.
			"external":   initialization.external,
			"repository": initialization.repository,
			// What was written, and what was detected, are reported separately:
			// a candidate is detected and deliberately not written, and a caller
			// that could not tell them apart could not check either. The three
			// detected lists are kept apart for the same reason -- they ask the
			// operator for a decision, for an optional one, and for nothing.
			"checks":   initialization.detection.Commands(),
			"detected": initialization.detection,
			"tracker":  tracker,
			"ignored":  ignored,
		})
	}
	for _, path := range initialization.written {
		fmt.Fprintf(stdout, "wrote %s\n", path)
	}
	fmt.Fprintln(stdout, "the configuration is complete and inherits nothing")
	if initialization.external {
		fmt.Fprintln(stdout, describeExternalConfiguration(initialization))
	}
	fmt.Fprintln(stdout, describeDetection(initialization.detection, initialization.config))
	// The tracker step is reported on the stream its outcome belongs to and
	// never changes the exit code. The configuration is written and valid either
	// way, and an init that failed after writing it would be describing a
	// project it had already changed as one it had not.
	if tracker.Status == trackerRemoteFailed {
		fmt.Fprintln(stderr, describeTrackerRemote(tracker))
	} else {
		fmt.Fprintln(stdout, describeTrackerRemote(tracker))
	}
	// Reported like the tracker's failures and for the same reason: the files are
	// written and valid, so an ignored configuration is something to know about
	// an init that worked rather than a reason to call it one that did not.
	if ignored.Ignored {
		fmt.Fprintln(stderr, doctor.DescribeIgnoredConfiguration(ignored))
	}
	return 0
}

// The tracker rides the project's own Git remote by default: its history moves
// under refs of Dolt's own beside the code it tracks, which is one repository,
// one permission model, and nothing to stand up. The remote is named `origin`
// on both sides because that is the remote each tool means by default.
const (
	trackerRemoteName = "origin"
	// trackerCommandTimeout bounds the two commands the tracker step runs:
	// reading where Git points, and telling bd about it. Neither reaches the
	// remote, but bd's first invocation in a project starts its database engine,
	// so the budget is the one every other bd call here is given rather than the
	// shorter one a purely local command would suggest.
	trackerCommandTimeout = 30 * time.Second
)

// What init did about the tracker's sync remote. The four outcomes are kept
// apart because they ask the operator for different things: a configured
// tracker and one left as it was ask nothing, a skipped one asks for a Git
// remote, and a failed one asks for a look at why bd refused.
const (
	trackerRemoteConfigured = "configured"
	trackerRemoteUnchanged  = "unchanged"
	trackerRemoteSkipped    = "skipped"
	trackerRemoteFailed     = "failed"
)

// trackerRemote is what became of the tracker's sync remote, reported beside
// the files init wrote. An unconfigured tracker is the first gap of running
// Yoyodyne as a team -- every machine's backlog drifting apart silently -- and
// it is closed at install time here rather than discovered at first divergence.
type trackerRemote struct {
	Status string `json:"status"`
	Name   string `json:"name,omitempty"`
	URL    string `json:"url,omitempty"`
	// Reason says why a tracker was left unconfigured, and is empty when one
	// was configured. It carries Git's or bd's own words rather than a summary
	// of them, because the remedy is a command to one of those two tools.
	Reason string `json:"reason,omitempty"`
}

// configureTrackerRemote points the tracker at the URL it should sync through.
//
// It never overwrites the remote the tracker already holds under this name
// unless a URL was named explicitly: a project that deliberately syncs its
// tracker somewhere else -- a repository of its own, which bd supports with any
// Git URL -- must not have that decision undone by a later `yoyo init`. A named
// URL does replace it, which is what the flag is for, and bd replaces a remote
// it already holds rather than refusing the name it is given.
//
// Its failures are reported rather than raised. Everything init promises is
// already on disk by the time this runs, and the tracker is a separate tool
// with its own initialization, so a project whose bd is not ready yet is told
// what to run instead of being handed a failed init over files that are fine.
func configureTrackerRemote(ctx context.Context, root, requested string, runner execution.ProcessRunner) trackerRemote {
	client := beads.Client{Runner: runner, Dir: root, Timeout: trackerCommandTimeout}
	url := strings.TrimSpace(requested)
	if url == "" {
		var absence string
		url, absence = gitRemoteURL(ctx, runner, root, trackerRemoteName)
		if url == "" {
			return trackerRemote{Status: trackerRemoteSkipped, Reason: absence}
		}
		existing, err := client.SyncRemotes(ctx)
		if err != nil {
			return trackerRemote{Status: trackerRemoteFailed, Reason: err.Error()}
		}
		for _, remote := range existing {
			if remote.Name == trackerRemoteName {
				return trackerRemote{Status: trackerRemoteUnchanged, Name: remote.Name, URL: remote.URL}
			}
		}
	}
	configured, err := client.SetSyncRemote(ctx, trackerRemoteName, url)
	if err != nil {
		return trackerRemote{Status: trackerRemoteFailed, Reason: err.Error()}
	}
	return trackerRemote{Status: trackerRemoteConfigured, Name: configured.Name, URL: configured.URL}
}

// gitRemoteURL reads where one of the project's Git remotes points. A project
// that has no such remote, and a directory that is not a repository at all,
// both answer with no URL and the reason there is none: neither is a failure of
// init, and both mean there is nothing yet for the tracker to sync to.
func gitRemoteURL(ctx context.Context, runner execution.ProcessRunner, root, remote string) (string, string) {
	result, err := runner.Run(ctx, execution.Command{
		Name:    "git",
		Args:    []string{"-C", root, "remote", "get-url", remote},
		Env:     execution.GitEnvironment(nil),
		Timeout: trackerCommandTimeout,
	}, nil)
	if err != nil {
		return "", fmt.Sprintf("git could not be run here: %v", err)
	}
	if result.Status != execution.ProcessSucceeded {
		if message := singleLine(firstNonEmpty(result.Stderr, result.Stdout)); message != "" {
			return "", message
		}
		return "", fmt.Sprintf("this project has no Git remote named %s", remote)
	}
	url := strings.TrimSpace(result.Stdout)
	if url == "" {
		return "", fmt.Sprintf("the Git remote %s reported no URL", remote)
	}
	return url, ""
}

// describeTrackerRemote says what became of the tracker's sync remote, and what
// the operator would run to finish the job themselves where it was not done.
// A reason is folded to one line here and kept whole in the JSON report: bd
// answers a question it cannot answer with a hint on a second line, and a line
// an operator reads among the files that were written stays a line.
func describeTrackerRemote(tracker trackerRemote) string {
	switch tracker.Status {
	case trackerRemoteConfigured:
		return fmt.Sprintf("the tracker syncs through %s: %s", tracker.Name, tracker.URL)
	case trackerRemoteUnchanged:
		return fmt.Sprintf("the tracker already syncs through %s: %s; name a URL with --tracker-remote to point it elsewhere",
			tracker.Name, tracker.URL)
	case trackerRemoteSkipped:
		return fmt.Sprintf("the tracker syncs nowhere -- %s; configure one with `bd dolt remote add %s <url>` so the backlog is not per-machine",
			singleLine(tracker.Reason), trackerRemoteName)
	default:
		// bd's own words are carried above, hint and all, so the remedy here
		// names the command to run rather than guessing at what went wrong: an
		// uninitialized tracker and one that refused for another reason are not
		// put right the same way.
		return fmt.Sprintf("the tracker syncs nowhere -- %s; the configuration was written, so configure it with `bd dolt remote add %s <url>` once bd answers here",
			singleLine(tracker.Reason), trackerRemoteName)
	}
}

// describeDetection says what became of the checks list, which is the one thing
// in a generated configuration an operator still owes a decision on.
func describeDetection(detection config.Detection, path string) string {
	switch {
	case len(detection.Checks) > 0:
		reported := fmt.Sprintf("proposed %s from %s; review the checks in %s before running work",
			countOf(len(detection.Checks), "check"), strings.Join(config.ProposalSources(detection.Checks), ", "), path)
		// Alternatives are not counted here: nothing is owed on them, and a line
		// that counts what needs no decision alongside what does is a line that
		// stops distinguishing them.
		if len(detection.Candidates) > 0 {
			reported += fmt.Sprintf(", with %s it could not settle left commented out beside them",
				countOf(len(detection.Candidates), "candidate"))
		}
		return reported
	case len(detection.Candidates) > 0:
		return fmt.Sprintf("checks is empty: %s are commented in %s, and one of them has to be chosen before work can run",
			countOf(len(detection.Candidates), "candidate"), path)
	default:
		return fmt.Sprintf("checks is empty and nothing in this project proposed one; write your own in %s before running work", path)
	}
}

func countOf(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// initializeOptions is what an initialization was asked for: which project to
// configure, what to call the product, whether an existing file may be
// overwritten, and whether the configuration is kept outside the repository.
type initializeOptions struct {
	Directory string
	ProductID string
	Force     bool
	External  bool
}

// initialized is what an initialization produced. Where the configuration went
// and what it configures are separate fields because an external configuration
// is the case where they are different directories, and every caller that
// reports one of them means one of them in particular.
type initialized struct {
	// config is the configuration file, which is the first file written.
	config string
	// repository is the project the configuration describes.
	repository string
	// written is everything the initialization wrote, configuration first.
	written []string
	// detection is what reading the project proposed as checks, so a caller can
	// report what was found alongside what was written.
	detection config.Detection
	external  bool
}

// initializeProject renders the scaffold and writes it, configuration file
// first. Every target is checked before anything is written, so a refusal to
// overwrite leaves the project exactly as it was rather than half-configured.
func initializeProject(options initializeOptions) (initialized, error) {
	var result initialized
	// The project is the repository this reads and, ordinarily, writes into. What
	// it writes there is confined to it: a scaffold that landed outside is a
	// configuration the operator was told about and cannot find, in a place
	// nothing reviews.
	project, err := repowrite.NewRoot(options.Directory)
	if err != nil {
		return result, fmt.Errorf("open project directory %q: %w", options.Directory, err)
	}
	root := project.Path()
	// What the operator called the project, which is what the files it wrote are
	// reported as. Confinement is decided against the resolved root, but a person
	// who typed one path and is told about another has to work out for themselves
	// that the two are the same directory.
	named, err := filepath.Abs(options.Directory)
	if err != nil {
		return result, fmt.Errorf("resolve project directory %q: %w", options.Directory, err)
	}
	// An external configuration is keyed by the repository it describes, and the
	// key is what discovery looks it up by from anywhere inside that repository.
	// So the repository is settled first, and a directory that is in none is
	// refused before anything is generated: a configuration nothing could find
	// again is worse than one that was never written.
	repository := named
	if options.External {
		found, err := config.RepositoryRoot(root)
		if err != nil {
			return result, err
		}
		if found == "" {
			return result, fmt.Errorf("%s is not inside a Git repository, and a configuration kept outside a repository is keyed by the "+
				"repository it describes; name a checkout with --directory, or write the configuration into the project without --external", named)
		}
		repository, root = found, found
	}

	identifier := strings.TrimSpace(options.ProductID)
	if identifier == "" {
		identifier = defaultProductID(root)
	}
	if err := domain.ValidateIdentifier("product id", identifier); err != nil {
		return result, fmt.Errorf("%w; name one with --product", err)
	}

	// The project's own files are read before anything is generated, so what the
	// scaffold proposes for `checks` comes from this repository rather than from
	// the template. Reading is all that happens: no command here is executed.
	detection := config.DetectChecks(root)
	// A project configuration describes the repository that contains it, and
	// relative paths resolve against that repository rather than against the
	// .yoyodyne directory, so "." is the project root. An external one has no
	// repository above it to resolve against, so it names the checkout outright.
	repositoryValue := "."
	if options.External {
		repositoryValue = repository
	}
	scaffold, err := config.NewScaffold(config.BuiltinV1, config.ScaffoldOptions{
		ProductID:  identifier,
		Repository: repositoryValue,
		Detection:  detection,
	})
	if err != nil {
		return result, err
	}

	destination, prefix, reported, err := scaffoldDestination(project, named, repository, options.External)
	if err != nil {
		return result, err
	}

	files := scaffold.Files()
	// Every target is resolved before anything is written, so a scaffold that
	// would leave the directory it belongs in — through a `.yoyodyne` somebody
	// symlinked elsewhere, or a directory above it — is refused with nothing
	// touched rather than half of it written somewhere nobody looks.
	targets := make([]string, 0, len(files))
	paths := make([]string, 0, len(files))
	for _, file := range files {
		target := prefix + file.Path
		path := filepath.Join(reported, filepath.FromSlash(target))
		// Where the bytes would actually land, which is what an existing file has to
		// be looked for at: a target the destination does not contain is refused
		// here, with nothing written.
		resolved, err := destination.Resolve(target)
		if err != nil {
			return result, fmt.Errorf("write %s into %q: %w", target, destination.Path(), err)
		}
		if !options.Force {
			if _, err := os.Lstat(resolved); err == nil {
				return result, fmt.Errorf("%s already exists; pass --force to overwrite it", path)
			} else if !os.IsNotExist(err) {
				return result, fmt.Errorf("inspect %q: %w", path, err)
			}
		}
		targets = append(targets, target)
		paths = append(paths, path)
	}

	for index := range files {
		if _, err := destination.WriteFile(targets[index], files[index].Content); err != nil {
			return result, fmt.Errorf("write %s into %q: %w", targets[index], destination.Path(), err)
		}
	}

	// Loading what was just written is the only proof that it is usable: the
	// personas have to resolve from the directory they were copied into, not from
	// the bundle they came from.
	loaded, err := config.Load(paths[0])
	if err != nil {
		return result, fmt.Errorf("the generated configuration does not load: %w", err)
	}

	result = initialized{
		config:     paths[0],
		repository: repository,
		written:    paths,
		detection:  detection,
		external:   options.External,
	}
	// An external configuration writes nothing into the repository at all, which
	// is the whole of what it is for: an index at the door of somebody else's
	// `docs/` tree is exactly the untracked file a guest in that repository came
	// here unable to add. `yoyo doctor` reports each home without one, so an
	// installation configured this way is told what it is missing rather than
	// left to find out.
	if options.External {
		return result, nil
	}

	// The artifact homes the configuration just named each get an index saying
	// what is filed there, whose it is, and whether the operator may edit one by
	// hand. They are written from the loaded configuration rather than from the
	// scaffold, so a project that pointed its designs somewhere else is indexed
	// where its designs actually are.
	indexes, err := writeArtifactHomeIndexes(project, named, loaded)
	if err != nil {
		return initialized{}, err
	}
	result.written = append(result.written, indexes...)
	return result, nil
}

// scaffoldDestination is where an initialization's files go: the root every one
// of them is confined to, what a scaffold file is called inside it, and the
// directory the operator is told the paths relative to.
//
// The external root is the configurations home rather than the project's own
// directory inside it, so the `projects/<key>` path is resolved through the
// primitive like any other: a root declared at the leaf would resolve a symlink
// standing where that leaf goes and confine the write to wherever it pointed,
// which is exactly the escape the primitive exists to refuse.
func scaffoldDestination(project repowrite.Root, named, repository string, external bool) (repowrite.Root, string, string, error) {
	if !external {
		return project, config.DirectoryName + "/", named, nil
	}
	home, err := config.ExternalHome(os.Getenv, os.UserHomeDir)
	if err != nil {
		return repowrite.Root{}, "", "", err
	}
	// The home is created before it is declared as a write root rather than
	// through one: confinement is decided against a root that already exists, and
	// this is the directory that root will be. Everything written inside it goes
	// through the primitive.
	if err := os.MkdirAll(home, externalHomePermissions); err != nil {
		return repowrite.Root{}, "", "", fmt.Errorf("create the configurations directory %q: %w", home, err)
	}
	root, err := repowrite.NewRoot(home)
	if err != nil {
		return repowrite.Root{}, "", "", fmt.Errorf("open the configurations directory %q: %w", home, err)
	}
	return root, config.ExternalDirectory(repository) + "/", home, nil
}

// externalHomePermissions is what the configurations home is created as: the
// mode the run-state root this machine keeps beside it is created with, because
// it is the same kind of directory — one user's own, under their home, created
// by the harness rather than checked out.
const externalHomePermissions = 0o700

// describeExternalConfiguration says what an external configuration is and what
// finds it, because the one thing an operator cannot see from the paths that
// were just printed is that nothing has to be passed to use them.
func describeExternalConfiguration(initialization initialized) string {
	return fmt.Sprintf("this configuration is on this machine only and nothing was written into %s; yoyo finds it from anywhere in that repository, "+
		"and its worktrees, without --config", initialization.repository)
}

// writeArtifactHomeIndexes writes the README each artifact home gets, and
// returns the ones it wrote.
//
// An index that is already there is left exactly as it is, and `--force` does not
// reach these: that flag is about regenerating the configuration this command
// owns, and a README in a documentation directory is the project's own prose
// rather than something init generated. Refusing the whole init over one would be
// worse still — it would refuse `yoyo init` in every repository that already has
// a word of its own at the door of `docs/`. What to do about an index that is
// there and no longer answers the three questions is `yoyo doctor`'s to report
// and `yoyo setup`'s to offer, where replacing somebody's prose is something they
// are asked about first.
func writeArtifactHomeIndexes(project repowrite.Root, named string, cfg config.Config) ([]string, error) {
	var written []string
	for _, status := range artifacthome.Inspect(project, cfg) {
		if status.State != artifacthome.StateMissing {
			continue
		}
		if _, err := artifacthome.Write(project, status.Home); err != nil {
			return nil, err
		}
		// Named the way the operator named the project, like every other path
		// this command reports, rather than the resolved form the write returns.
		written = append(written, filepath.Join(named, filepath.FromSlash(status.Path)))
	}
	return written, nil
}

// defaultProductID names the product after the directory being configured,
// which is what an operator would have typed anyway in the ordinary case.
func defaultProductID(root string) string {
	return strings.ToLower(strings.TrimSpace(filepath.Base(root)))
}
