package cli

// Diagnosing an installation, and saying what would fix it.
//
// The verb the design's CLI surface has promised since Milestone 0, and the one
// place the harness states what a working installation actually is. Everything
// else in this executable assumes that state and fails somewhere downstream of
// it: an unauthenticated provider is discovered when a run is claimed, an
// unrunnable check when a change has already been written, a stopped sink not at
// all. Here the whole of it is asked at once, before anything is spent.
//
// Every finding carries a remedy that is a command. That is what separates this
// from a status listing: an operator reading it is not being asked to work out
// what to do, and neither is an agent -- `--json` carries the same findings with
// the same remedies, structurally, which is what makes an automated repair
// possible without anything having to parse this prose.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer, version string) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	quiet := flags.Bool("quiet", false, "report only what is wrong, leaving out what is already healthy")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "doctor does not accept positional arguments: it checks the whole installation")
		printDoctorUsage(stderr)
		return 2
	}

	report := doctor.Diagnose(ctx, doctor.Environment{
		Runner:      execution.OSProcessRunner{},
		LookPath:    exec.LookPath,
		Getenv:      os.Getenv,
		UserHomeDir: os.UserHomeDir,
		GOOS:        runtime.GOOS,
		Version:     version,
		Build:       buildinfo.Commit(),
		Load:        func() (config.Resolved, error) { return loadConfiguration(*configPath) },
		Locate:      func() (string, error) { return configurationPath(*configPath) },
	})

	if *jsonOutput {
		if writeJSON(stdout, stderr, report) != 0 {
			// Nothing readable reached stdout, so this is not the diagnosis
			// exiting 1 with a report to act on. It is the command failing to do
			// what it was asked, which is what 2 says -- and what keeps anything
			// reading an exit 1 sure there is a report under it.
			return 2
		}
	} else {
		renderDiagnosis(stdout, report, *quiet)
	}
	// What the project's template has improved since its configuration was
	// generated, said without being asked for it.
	//
	// It is not a finding. A finding is something about whether work can run
	// here, and every one that is not healthy carries a remedy and counts toward
	// the verdict line; an improvement available is neither wrong nor a
	// diagnosis, and counting it as a warning would put a persona that got better
	// in the same list as a provider nobody authenticated. So it goes to standard
	// error beside the report, in both forms -- the JSON on standard output is
	// still exactly the report -- and it is silent unless there is something.
	printDriftNotice(stderr, report.Config)
	// A broken installation exits nonzero whichever form it was reported in, so a
	// script does not have to read the findings to notice. A warning does not:
	// this is work that can run with something worth knowing about it, and an
	// exit code that could not tell the two apart would be one nobody could gate
	// on.
	if !report.Healthy() {
		return 1
	}
	return 0
}

// printDriftNotice says what the project's template has improved that nobody
// here edited, and says nothing at all otherwise -- which is the ordinary case
// and the reason it can be printed on every run.
//
// A configuration the diagnosis could not load has nothing to compare, and so
// does one with no baseline beside it. Both are silent: the first is already the
// report's first finding, and the second is a project that predates the record,
// which decides nothing about how it runs and is not something to be told about
// on every invocation.
func printDriftNotice(stderr io.Writer, configPath string) {
	if configPath == "" {
		return
	}
	resolved, err := config.LoadResolved(configPath)
	if err != nil {
		return
	}
	drift, _ := config.ReadDrift(resolved)
	if notice := drift.Notice(); notice != "" {
		fmt.Fprintln(stderr, notice)
	}
}

// renderDiagnosis prints the findings in the order they were made, with each
// remedy on a line of its own directly under what it remedies. Keeping them
// adjacent is the whole point: a list of problems followed by a list of commands
// is a list an operator has to match up by hand.
//
// Severity deliberately does not reorder them. The order they were made is the
// order somebody would fix them in -- the tools, then the project, then what the
// project turns on -- and the first problem in that list is usually why the ones
// under it are problems too.
func renderDiagnosis(stdout io.Writer, report doctor.Report, quiet bool) {
	fmt.Fprintln(stdout, verdict(report))
	if report.Config != "" {
		fmt.Fprintf(stdout, "configuration: %s\n", report.Config)
	}
	fmt.Fprintln(stdout)
	for _, finding := range report.Findings {
		if quiet && finding.Status == doctor.StatusOK {
			continue
		}
		fmt.Fprintf(stdout, "%-8s %-22s %s\n", finding.Status, finding.Check, finding.Summary)
		if finding.Detail != "" {
			fmt.Fprintf(stdout, "%-8s %-22s %s\n", "", "", finding.Detail)
		}
		if finding.Remedy != "" {
			fmt.Fprintf(stdout, "%-8s %-22s fix: %s\n", "", "", finding.Remedy)
		}
	}
}

// verdict is the one line somebody reads first. A healthy installation says so
// plainly rather than leaving them to infer it from an absence of complaints.
func verdict(report doctor.Report) string {
	_, warnings, problems := report.Counts()
	product := report.Product
	if product == "" {
		product = "this installation"
	}
	switch {
	case problems > 0 && warnings > 0:
		return fmt.Sprintf("%s cannot run work: %s, and %s worth knowing about",
			product, countOf(problems, "problem"), countOf(warnings, "warning"))
	case problems > 0:
		return fmt.Sprintf("%s cannot run work: %s", product, countOf(problems, "problem"))
	case warnings > 0:
		return fmt.Sprintf("%s is ready to run work, with %s worth knowing about",
			product, countOf(warnings, "warning"))
	default:
		return fmt.Sprintf("%s is ready to run work; everything checked is healthy", product)
	}
}

func printDoctorUsage(writer io.Writer) {
	fmt.Fprintln(writer, strings.TrimSpace(`
Usage: yoyo doctor [options]

Check whether this installation can actually run work, and say what would fix it
where it cannot. It reads the build on PATH against the one running, Git, the
tracker, the configuration, the deterministic checks, whether each artifact home
still says what is filed there and who owns it, each provider the agents
name -- installed always, and authenticated where the harness has an adapter that
can ask -- forge access where the project publishes, and, where reporting is on,
this project's own Slack secrets and the sink that is supposed to be using them.

Every finding that is not healthy carries a remedy, and a remedy is a command:
what it prints under a problem is what to run. `+"`--json`"+` carries the same findings
with the same remedies, so anything automating a repair reads them structurally
rather than parsing this output.

It changes nothing. Nothing here installs, authenticates, restarts, or edits a
configuration, and no credential is read: whether a secret is stored is asked in
the form that answers without producing the value.

Beside the report, on standard error, it says one line where the template this
project was generated from has since improved a value nobody here edited. That is
not a finding and not a diagnosis: it is silent when there is nothing, it never
changes the exit code, and `+"`yoyo config drift`"+` is where the whole comparison is.

It exits 1 when something would stop work running, and 0 otherwise. That 1 is the
diagnosis rather than a failure of the command, so it always comes with the report
and is not worth retrying. Anything it could not do -- a flag it does not have, a
positional argument, a report it could not write -- exits 2 instead, says why on
standard error, and reports nothing. A warning is not a small problem: it is
something about an installation that works. Every reporting finding is one,
because reporting is an observation and never a gate -- a sink nobody started
leaves every run exactly as it was. It is still named in full, with the command
that ends it.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --quiet           report only what is wrong, leaving out what is already healthy
  --json            emit machine-readable JSON`))
}
