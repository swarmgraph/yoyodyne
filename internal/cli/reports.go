package cli

// Reading what agents reported, without opening a conversation.
//
// A report is how something an agent noticed while its own work succeeded
// reaches the operator, and until this verb existed the only way to read the
// pile was `/reports` inside `yoyo chat`. That fails the operator the channel
// exists for: a run that finishes overnight tells them a report was filed, and
// reading it costs them an interactive conversation with a provider behind it.
// It also puts the pile out of reach of anything scripted, which is the surface
// a channel meant to be triaged eventually needs.
//
// So this reads the same durable store the conversation reads and nothing else.
// It is read-only: a report is written once and never revised, and nothing here
// retires one, handles one, or decides anything about the pile. What it does
// show is what somebody else decided — the product manager records what became
// of a report beside the pile, and a listing that could not tell a report
// somebody dealt with from one nobody has read would send an operator looking
// for work that is already done.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type reportsOutput struct {
	Reports []report.Report `json:"reports"`
	// Handlings are what became of the reports somebody has decided about, in
	// the order they were recorded. They are a separate list rather than a field
	// on each report because that is what they are on disk: the pile is never
	// rewritten, and a disposition is a second record about it.
	Handlings []report.Handling `json:"handlings"`
	// Builds is how far each build the reports carry is behind the target
	// branch's tip, keyed by the build. It is a map beside the reports rather
	// than a field on each for the reason handlings are: the report is what its
	// author filed, and this is a reading taken now that will be different
	// tomorrow. A build that could not be counted carries why instead.
	Builds map[string]report.Lag `json:"builds,omitempty"`
	// BuildsProblem says why no build was counted at all, where the repository
	// they are counted in could not be found.
	BuildsProblem string `json:"builds_problem,omitempty"`
	Error         string `json:"error,omitempty"`
}

func readReports(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("reports", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "reports does not accept positional arguments: it reads the whole pile")
		printReportsUsage(stderr)
		return 2
	}

	store, err := reportStore(*configPath)
	if err != nil {
		return reportReportsError(stdout, stderr, *jsonOutput, err)
	}
	collected, err := store.List()
	if err != nil {
		return reportReportsError(stdout, stderr, *jsonOutput, err)
	}
	// What became of them is read after the pile and fails the whole command with
	// it. Unlike the conversation listing, which is one line beside an answer that
	// has already been given, this is the answer: an operator or a script told
	// "nothing is handled" by a log that could not be read is being told something
	// false, and there is nothing else here for that to be a footnote to.
	handlings, err := store.Handlings()
	if err != nil {
		return reportReportsError(stdout, stderr, *jsonOutput, err)
	}
	// How far each report's build is behind the target branch, asked of the
	// product's repository. A repository that cannot be found costs the counts
	// and nothing else: every report still names its build.
	builds, buildsErr := reportBuilds(*configPath)
	gauge := report.NewGauge(context.Background(), builds)
	if *jsonOutput {
		// Both lists are always encoded, so a caller reading JSON tells "nobody has
		// reported anything" from a failure by the field being empty rather than
		// absent.
		if collected == nil {
			collected = []report.Report{}
		}
		if handlings == nil {
			handlings = []report.Handling{}
		}
		output := reportsOutput{Reports: collected, Handlings: handlings, Builds: gauge.Measure(collected)}
		if buildsErr != nil {
			output.BuildsProblem = buildsErr.Error()
		}
		return writeJSON(stdout, stderr, output)
	}
	if len(collected) == 0 {
		// "Nothing has been reported" is an answer, and where the pile would be is
		// part of it: an operator looking for a report they were told about needs
		// to know they are reading the right product's store.
		fmt.Fprintf(stdout, "nothing has been reported for this product; the pile would be %s\n", store.Path())
		return 0
	}
	// The whole pile, oldest first, unlike `/reports` in a conversation: that is a
	// listing beside an ongoing conversation and lists the twenty most recent,
	// and this is a command whose output can be paged, filtered, or piped.
	//
	// Each one is dressed by the severity it was filed at, and what became of it
	// is not: a pile printed oldest first is the surface where a critical report
	// is furthest from the reader's eye, and the plain line under a loud one is
	// what says somebody has already dealt with that one.
	handled := report.Handled(handlings)
	theme := console.ThemeFor(stdout, os.Getenv)
	// How the pile stands leads the listing, because that is the question an
	// operator opens it with and the reports themselves do not answer it: whether
	// the pile is being worked through is the count and the oldest unhandled
	// report's age over time, and neither is legible from a page of reports. It is
	// the shared derivation rather than arithmetic done here, so this number and
	// the one the channel says cannot come apart.
	fmt.Fprintf(stdout, "reports: %s\n", report.SummarizeHandled(collected, handled, time.Now()).Describe())
	// A report is a role's own words, a program manager's digest among them, so
	// each work item it names is printed with its priority, labels, and title.
	// A tracker that cannot be listed marks each item as unreadable.
	writeReports(stdout, theme, collected, handled, gauge, reportTitles(*configPath), reportTerms(*configPath))
	// Why the counts are missing is said once, under the listing, rather than
	// under every report it affects.
	switch {
	case buildsErr != nil:
		fmt.Fprintf(stdout, "no build was counted against the target branch: %s\n", buildsErr)
	case gauge.Problem() != "":
		fmt.Fprintln(stdout, gauge.Problem())
	}
	return 0
}

// reportBuilds is what counts a report's build against the target branch: the
// product's repository, asked exactly as the channel asks it how far a watch
// session's build is behind, so the two surfaces cannot count one build two
// ways. HEAD of the product's checkout is the target branch every run is written
// against and promoted into.
//
// The builds are the harness's own revisions, so the count means something only
// where the product is the harness's own source. Nothing assumes it: a build the
// repository does not hold is refused rather than counted, and the listing says
// so.
func reportBuilds(configPath string) (report.Builds, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, err
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return nil, fmt.Errorf("resolve product repository: %w", err)
	}
	return repositoryDeployments{
		repository: repository,
		runner:     execution.OSProcessRunner{},
		timeout:    chatTrackerTimeout,
	}, nil
}

// writeReports prints each report dressed by its severity, with what became of
// it plainly under it, and every work item either names with its priority, labels, and title.
func writeReports(out io.Writer, theme console.Theme, collected []report.Report, handled map[string]report.Handling, gauge *report.Gauge, titles *readmodel.WorkItemTitles, wording ...*readmodel.TextTerms) {
	var words *readmodel.TextTerms
	if len(wording) > 0 {
		words = wording[0]
	}
	unresolved := 0
	for _, reported := range collected {
		text := words.Render(titles.Cite(reported.RenderAgainst(gauge)))
		fmt.Fprint(out, theme.Severity(console.Severity(reported.Severity), text))
		if !reported.ReferencesKept {
			unresolved += titles.Unresolved(reported.Message)
		}
		if handling, done := handled[reported.ID]; done {
			fmt.Fprint(out, words.Render(titles.Cite(handling.Render())))
		}
	}
	if said := readmodel.UnresolvedSays(unresolved); said != "" {
		fmt.Fprintln(out, said)
	}
}

// reportTitles is one reading of the product's tracker, for the work items the
// listing names: nil where the tracker cannot be found, and the unreadable
// reading where it could not be listed, so each item is shown marked.
func reportTitles(configPath string) *readmodel.WorkItemTitles {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return nil
	}
	tracker := beads.Client{Runner: execution.OSProcessRunner{}, Dir: repository}
	titles, _ := readmodel.ReadWorkItemTitles(context.Background(), readmodel.Sources{Tracker: tracker, TrackerTimeout: chatTrackerTimeout, TrackerPrefixes: []string{string(resolved.Config.Product.ID)}})
	return titles
}

func reportTerms(configPath string) *readmodel.TextTerms {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return nil
	}
	return readmodel.ReadTextTerms(repository)
}

// reportStore resolves the same product-scoped pile every run appends to and
// the conversation reads, from the configuration and the state root alone. It
// deliberately does not go through buildComponents: reading what agents
// reported needs no repository, no worktree manager, and no process runner, and
// a verb that exists to be reachable from anywhere should not refuse to read a
// pile because of where the caller's checkout happens to sit — inside a
// harness-managed worktree, for one.
//
// What that shortcut must not do is address a different pile. The state root is
// productStateRoot for every command that has one, and the project
// configuration has no say in it, so the product id below is the whole of what decides which
// store this is — and a test pins this path to the one buildComponents builds,
// because a reports verb reading the wrong root would answer "nothing has been
// reported" against a pile that is not empty.
func reportStore(configPath string) (*runstate.ReportStore, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, err
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return nil, err
	}
	return runstate.NewReportStore(stateRoot, resolved.Config.Product.ID)
}

func reportReportsError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, reportsOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printReportsUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo reports [options]

What agents reported without it stopping their work: a risk one worked around,
an assumption that may not hold, a defect or a stale document outside the work
it was given, something in its environment that stopped it verifying what it
wanted to. Every role files them the same way and they land in one pile per
product, which is what this reads.

Each report names itself, the role and the configured agent that made it, the run
or conversation it came from, the work item where there was one, a severity —
critical, warning, or note — and the text.

Beside the run it names the harness build that run executed, and how many changes
the target branch has taken since. A report is a claim about that build, so a
report from a build behind the tip may describe something already fixed. Check
before admitting work from it. A report filed before reports carried a build says
"no build recorded", and a build the product's repository does not hold is named
as not counted. A report the Lead Product Manager has
decided about carries what it decided, under it; everything else is still
waiting on somebody. The whole pile is printed, oldest first; `+"`/reports`"+` in
`+"`yoyo chat`"+` shows the same reports beside the conversation, listing the
twenty most recent.

A report filed at `+"`critical`"+` or `+"`warning`"+` is marked at the left margin, with
`+"`!!`"+` and `+"`!`"+`, and coloured where the terminal permits colour — so a pile can be
scanned for what is already costing somebody without every line being read. The
marker is the part that survives: a listing piped to a file, read where
`+"`NO_COLOR`"+` is set, or shown on a terminal that says it is dumb still says which
reports are which. `+"`--json`"+` carries none of it and is unchanged.

The listing leads with how the pile stands: how many of the collected reports
nobody has decided about, how long ago the oldest of those was filed, and the
worst severity among them. That is the line to read over a week rather than
once — a pile being worked through has a falling count and a falling oldest age,
and a pile nothing is draining looks identical to it in any single reading.

A report decides nothing and nothing waits on it, so this is read-only: it
retires nothing, handles nothing, and changes no work. Deciding what becomes of
a report is the Lead Product Manager's, in a conversation, and the unhandled
ones are carried into that conversation without anybody having to fetch them,
oldest first with anything critical ahead of them, resuming where the last turn
stopped.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
