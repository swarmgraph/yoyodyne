package cli

// The gate for the governed documents, run by whoever owns one.
//
// The rest of the artifact commands report what is wrong and exit zero, which is
// the right thing for a listing: an unapproved document, a broken relationship,
// and a revision crossing the ownership boundary are all things that stop
// nothing, and a command that refused over one would be a command nobody could
// use to look. This one exits non-zero on any of them, and that is the whole
// difference: it is the check an owner runs after editing, and the place a
// defect in a governed document fails.
//
// It exists because the failing used to happen somewhere nobody could act. The
// repository-coupled gates in `make test` read these same documents, and a
// malformed one turned every developer's run red over a file every developer's
// diff refuses — so the only route out was an amendment proposal made while
// every run stayed red. Those gates escalate now (internal/governeddoc), which
// leaves this as the place the defect still fails loudly, run by the one person
// who can put it right. The index internal/artifacthome writes at the door of
// every home names it, so an owner standing in the directory they just edited
// meets the command — once `yoyo setup` has written that index, which is the one
// thing a developer's change cannot do for them.

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doclink"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/governeddoc"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// governedCheck is what the check found, for a machine.
type governedCheck struct {
	// Homes are the directories that were read, so a report that found nothing
	// can say where it looked rather than only that it is clean.
	Homes   []string         `json:"homes,omitempty"`
	Defects []governedDefect `json:"defects,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// governedDefect is one defect and the role it belongs to.
type governedDefect struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
	// Home and Owner are the artifact home the document is filed in and the role
	// that may change it, and are empty for a file no home claims.
	Home  string `json:"home,omitempty"`
	Owner string `json:"owner,omitempty"`
	// Route is what to do about it, present exactly when the defect is not the
	// reader's own to fix.
	Route string `json:"route,omitempty"`
}

// checkGovernedDocuments reads every governed document and reports what is
// wrong with each, routed to the role that owns it.
//
// Every kind of finding is read in one pass rather than left to a command each:
// an owner who has just edited a goals document wants one answer about what they
// wrote, and a check they have to run five times is a check they run none of.
//
// The set is exactly the one the gates during a run escalate, and it is exactly
// that set because a class escalated there and failed nowhere here would leave a
// defect no gate at all catches. What the gates escalate is decided by
// governeddoc.Protected, and so is what the links read below are narrowed to, so
// the two cannot come to disagree: the artifacts, the goals stated in them, the
// invariants, the index at the door of each artifact home, and every link written
// in a document no developer's diff may touch.
//
// One thing the repository gates fail on rather than escalate is not read here
// and does not need to be: whether the goal this repository's attributed work
// items name still resolves. Putting that right is a tracker action and an edit
// to the test that holds the wording, neither of them a protected path, so it is
// a defect the change in hand can fix and it fails where it is found.
func checkGovernedDocuments(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("artifact check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	parsed, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(parsed) != 0 {
		fmt.Fprintln(stderr, "artifact check does not accept positional arguments: it reads every governed document")
		printArtifactUsage(stderr)
		return 2
	}

	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		return reportGovernedCheckError(stdout, stderr, *jsonOutput, err)
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return reportGovernedCheckError(stdout, stderr, *jsonOutput, fmt.Errorf("resolve product repository: %w", err))
	}
	product := resolved.Config.Product
	// The governed documents are read where every other reader reads them: the
	// companion intent repository where the configuration names one. The links
	// are walked in the project's own repository, which is where the protected
	// paths a developer's diff is refused are.
	intent := product.IntentRoot(repository)
	artifacts, err := artifactStore(repository, product).Load()
	if err != nil {
		return reportGovernedCheckError(stdout, stderr, *jsonOutput, err)
	}
	invariants, err := invariantProblems(intent, product)
	if err != nil {
		return reportGovernedCheckError(stdout, stderr, *jsonOutput, err)
	}
	links, err := governedLinkProblems(repository, resolved.Config)
	if err != nil {
		return reportGovernedCheckError(stdout, stderr, *jsonOutput, err)
	}
	indexes, err := indexProblems(intent, resolved.Config)
	if err != nil {
		return reportGovernedCheckError(stdout, stderr, *jsonOutput, err)
	}

	defects := governedDefects(artifacts.Problems, artifacts.ReferenceProblems,
		goal.Collect(intent, artifacts), invariants, links)
	defects = append(defects, indexes...)
	routed := governeddoc.Route(resolved.Config, defects...)

	read := readHomes(artifacts.Homes, product)
	if *jsonOutput {
		report := governedCheck{Homes: read}
		for _, entry := range routed {
			report.Defects = append(report.Defects, governedDefect{
				Path:   entry.Path,
				Detail: entry.Detail,
				Home:   entry.Home,
				Owner:  string(entry.Owner),
				Route:  entry.Route,
			})
		}
		if code := writeJSON(stdout, stderr, report); code != 0 {
			return code
		}
		if len(routed) > 0 {
			return 1
		}
		return 0
	}

	if len(routed) == 0 {
		fmt.Fprintf(stdout, "every governed document, index, and link under %s is well-formed\n", strings.Join(read, ", "))
		return 0
	}
	// One defect to an entry, with the route under it: what somebody has to do is
	// open one file, and a list of defects followed by a list of routes is a list
	// they have to match up by hand.
	for _, entry := range routed {
		fmt.Fprintf(stderr, "%s\n", entry)
	}
	fmt.Fprintf(stderr, "\n%s in the governed documents\n", countOf(len(routed), "defect"))
	return 1
}

// governedDefects folds every reader's own problem type into the one shape the
// routing is done over. Each reason is carried through in the words the reader
// wrote it in and prefixed with what kind of finding it is, because "not read as
// an artifact" and "read, and its place in the chain is wrong" are two different
// things to do about the same file.
//
// Every finding here names a file. That is not decoration: what a defect is
// routed to its owner by is the document it is written in, so one arriving with
// no path would be attributed to nobody and reported as somebody else's.
func governedDefects(
	refused []artifact.Problem,
	references []artifact.ReferenceProblem,
	goals goal.Set,
	invariants []invariant.Problem,
	links []doclink.Problem,
) []governeddoc.Defect {
	var defects []governeddoc.Defect
	add := func(path, detail string) {
		defects = append(defects, governeddoc.Defect{Path: path, Detail: detail})
	}
	for _, problem := range refused {
		add(problem.Path, "it is not read as an artifact: "+problem.Reason)
	}
	for _, problem := range references {
		add(problem.Path, string(problem.Kind)+": "+problem.Reason)
	}
	for _, problem := range goals.Problems {
		add(problem.Path, "its goals could not be read: "+problem.Reason)
	}
	for _, problem := range goals.LinkProblems {
		// A problem about the brief rather than about any one goal carries no path
		// of its own: it is the root of the chain missing, reported once instead of
		// against every goal below it. The brief is the file to open, and the set
		// carries where that is — without this the finding would name no document
		// and route to nobody.
		add(pathOr(problem.Path, goals.BriefPath), "a goal is not linked to the brief: "+problem.Reason)
	}
	for _, problem := range goals.WrapProblems {
		add(problem.Path, fmt.Sprintf("line %d: a goal is not written on one line: %s", problem.Line, problem.Reason))
	}
	for _, problem := range invariants {
		add(problem.Path, "it is not read as an invariant: "+problem.Reason)
	}
	for _, problem := range links {
		add(problem.Path, fmt.Sprintf("line %d: a link resolves to nothing: %s", problem.Line, problem.Reason))
	}
	return defects
}

// readHomes is every directory this check actually looked in: the artifact homes,
// the invariants home beside them, and the configuration directory. The
// invariants carry an identity scheme of their own and are excluded from the
// artifact set, and the configuration directory is no home at all — it holds no
// governed document, but it is a protected path carrying Markdown, the personas
// among it, so a broken link written there is escalated by the gates and is this
// command's to fail on. A clean report naming only the artifact homes would claim
// less than the check covered, and one naming more than it read would be worse.
func readHomes(homes []string, product config.Product) []string {
	read := append([]string(nil), homes...)
	if invariants := strings.TrimSpace(product.Invariants); invariants != "" {
		read = append(read, invariants)
	}
	return append(read, config.DirectoryName)
}

// pathOr is the file a finding sends somebody to, and the file to fall back on
// where the finding names none.
func pathOr(path, fallback string) string {
	if strings.TrimSpace(path) == "" {
		return fallback
	}
	return path
}

// invariantProblems is the invariants home read for the same question. A
// directory that is not configured, or that does not exist, records no
// invariants and is not a defect; anything else that stopped it being read is
// returned, because an invariants home this command could not open and reported
// as clean is a false green over documents it says it reads.
func invariantProblems(repository string, product config.Product) ([]invariant.Problem, error) {
	if strings.TrimSpace(product.Invariants) == "" {
		return nil, nil
	}
	set, err := (invariant.Store{RepositoryRoot: repository, Directory: product.Invariants}).Load()
	if err != nil {
		return nil, fmt.Errorf("read the invariants in %s: %w", product.Invariants, err)
	}
	return set.Problems, nil
}

// governedLinkProblems is every broken link written in a document no developer's
// diff may touch — the artifact homes and the configuration directory both.
//
// The links are read here because the gates that read these documents during a
// run escalate a broken one written in such a document, and a class escalated in
// every gate and failed in none would be exactly the reporting this command
// exists to be the other half of.
//
// The walk is the whole repository rather than those directories alone, because
// resolving a link needs the document at its other end and that document is
// usually somewhere else. Which of the results this command answers for is then
// decided by governeddoc.Protected — the same question the escalation is decided
// by, so the two sets are one set. A broken link anywhere else is a developer's,
// and it fails in the check that found it rather than here.
func governedLinkProblems(repository string, cfg config.Config) ([]doclink.Problem, error) {
	problems, err := doclink.Check(repository)
	if err != nil {
		return nil, fmt.Errorf("read the links this repository's documents make: %w", err)
	}
	governed := make([]doclink.Problem, 0, len(problems))
	for _, problem := range problems {
		if governeddoc.Protected(cfg, problem.Path) {
			governed = append(governed, problem)
		}
	}
	return governed, nil
}

// indexProblems is what each artifact home's index is, for the homes whose index
// is not what it should be. The indexes are the one file in a home that is not a
// governed document, and they are checked here for the reason the documents
// beside them are: they sit inside the homes, so every developer's diff refuses
// them, and the gate that reads them during a run escalates what it finds.
//
// What is asked is the three questions the index exists to answer — what is filed
// here, whose it is, whether you may edit one by hand — rather than whether the
// file matches what the harness would write. That line is artifacthome's and not
// this command's to redraw: an operator who added a paragraph of their own has
// not broken their index, and a check that said otherwise is one they would learn
// to skip past.
func indexProblems(repository string, cfg config.Config) ([]governeddoc.Defect, error) {
	root, err := repowrite.NewRoot(repository)
	if err != nil {
		return nil, fmt.Errorf("read the artifact homes in %s: %w", repository, err)
	}
	return governeddoc.IndexDefects(artifacthome.Inspect(root, cfg)), nil
}

func reportGovernedCheckError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, governedCheck{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}
